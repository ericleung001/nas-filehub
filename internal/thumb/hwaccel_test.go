package thumb

import (
	"strings"
	"testing"

	"github.com/eric/nas-filehub/internal/hwinfo"
)

func TestParseMode(t *testing.T) {
	if ParseMode("", false) != ModeOff {
		t.Fatal("empty + no VAAPI should be off")
	}
	if ParseMode("", true) != ModeAuto {
		t.Fatal("legacy THUMB_VAAPI=true should be auto")
	}
	if ParseMode("auto", false) != ModeAuto {
		t.Fatal("auto")
	}
	if ParseMode("off", true) != ModeOff {
		t.Fatal("explicit off wins")
	}
	if ParseMode("vaapi", false) != ModeVAAPI {
		t.Fatal("vaapi")
	}
	if ParseMode("nvdec", false) != ModeNVDEC {
		t.Fatal("nvdec")
	}
	if ParseMode("cuda", false) != ModeNVDEC {
		t.Fatal("cuda alias")
	}
}

func TestSelectPlanIntelUsesRenderNode(t *testing.T) {
	st := hwinfo.Status{
		RenderD128:  true,
		RenderNodes: []string{"renderD128"},
		Devices: []hwinfo.Device{{
			Vendor: "intel", Type: "VAAPI/QSV", Driver: "i915",
			Render: "renderD129", VAAPICapable: true,
		}},
	}
	p := SelectPlan(ModeAuto, st)
	if p.Backend != "vaapi" || p.Device != "/dev/dri/renderD129" {
		t.Fatalf("plan %+v, want vaapi on renderD129", p)
	}
	hw, soft := HWAccelArgs(p, "in.mp4", "out.jpg")
	joined := strings.Join(hw, " ")
	if !strings.Contains(joined, "-hwaccel vaapi") || !strings.Contains(joined, "/dev/dri/renderD129") {
		t.Fatalf("vaapi args: %v", hw)
	}
	if !strings.Contains(strings.Join(soft, " "), "scale=320:-2") {
		t.Fatalf("software args: %v", soft)
	}
}

func TestSelectPlanNVIDIAUsesNVDEC(t *testing.T) {
	st := hwinfo.Status{
		RenderD128: true,
		Devices: []hwinfo.Device{{
			Vendor: "nvidia", Type: "NVDEC", Driver: "nvidia", VAAPICapable: false,
		}},
	}
	p := SelectPlan(ModeAuto, st)
	if p.Backend != "nvdec" {
		t.Fatalf("plan %+v, want nvdec", p)
	}
	hw, _ := HWAccelArgs(p, "in.mp4", "out.jpg")
	joined := strings.Join(hw, " ")
	if !strings.Contains(joined, "-hwaccel cuda") {
		t.Fatalf("nvdec args: %v", hw)
	}
}

func TestSelectPlanOffIsSoftware(t *testing.T) {
	st := hwinfo.Status{
		RenderD128: true,
		Devices:    []hwinfo.Device{{Vendor: "intel", VAAPICapable: true, Render: "renderD128"}},
	}
	p := SelectPlan(ModeOff, st)
	if p.Backend != "software" || p.CanUseHardware() {
		t.Fatalf("off should be software: %+v", p)
	}
	hw, _ := HWAccelArgs(p, "a", "b")
	if hw != nil {
		t.Fatalf("software plan should have no hw args: %v", hw)
	}
}
