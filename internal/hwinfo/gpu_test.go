package hwinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addCard(t *testing.T, root, card, vendor, device, driver string) string {
	t.Helper()
	dev := filepath.Join(root, "sys", "devices", card+"-dev")
	writeFile(t, filepath.Join(dev, "vendor"), "0x"+vendor+"\n")
	writeFile(t, filepath.Join(dev, "device"), "0x"+device+"\n")
	writeFile(t, filepath.Join(dev, "uevent"), "DRIVER="+driver+"\nPCI_ID="+vendor+":"+device+"\n")
	driverTarget := filepath.Join(root, "sys", "bus", "pci", "drivers", driver)
	if err := os.MkdirAll(driverTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driverTarget, filepath.Join(dev, "driver")); err != nil {
		t.Fatal(err)
	}
	drm := filepath.Join(root, "sys", "class", "drm")
	if err := os.MkdirAll(drm, 0o755); err != nil {
		t.Fatal(err)
	}
	// Class entry is a directory with a device symlink, as used in tests
	// (real systems often symlink the class entry itself).
	cardDir := filepath.Join(drm, card)
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dev, filepath.Join(cardDir, "device")); err != nil {
		t.Fatal(err)
	}
	return dev
}

func addRender(t *testing.T, root, name, devDir string) {
	t.Helper()
	drm := filepath.Join(root, "sys", "class", "drm", name)
	if err := os.MkdirAll(drm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(devDir, filepath.Join(drm, "device")); err != nil {
		t.Fatal(err)
	}
	dri := filepath.Join(root, "dev", "dri")
	if err := os.MkdirAll(dri, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dri, name), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dri, "card0"), []byte(""), 0o644); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
}

func TestDetectIntelVAAPI(t *testing.T) {
	root := t.TempDir()
	dev := addCard(t, root, "card0", "8086", "3185", "i915")
	addRender(t, root, "renderD128", dev)

	st := Detect(root, true)
	if !st.DevDri || !st.RenderD128 {
		t.Fatalf("expected /dev/dri and renderD128: %+v", st)
	}
	if !st.CanUseVAAPI {
		t.Fatalf("Intel + renderD128 should be VAAPI-usable: %+v", st)
	}
	if len(st.Devices) != 1 {
		t.Fatalf("devices=%d %+v", len(st.Devices), st.Devices)
	}
	d := st.Devices[0]
	if d.Vendor != "intel" || d.Type != "VAAPI/QSV" || d.Driver != "i915" || !d.VAAPICapable {
		t.Fatalf("device %+v", d)
	}
	if d.PCIID != "8086:3185" {
		t.Fatalf("pciId %q", d.PCIID)
	}
	if d.Render != "renderD128" {
		t.Fatalf("render %q", d.Render)
	}
	if st.ThumbNote != "THUMB_VAAPI is on; ffmpeg can use VAAPI on /dev/dri/renderD128." {
		t.Fatalf("note: %s", st.ThumbNote)
	}
}

func TestDetectAMDAndNVIDIAListedSeparately(t *testing.T) {
	root := t.TempDir()
	amd := addCard(t, root, "card0", "1002", "15d8", "amdgpu")
	addRender(t, root, "renderD128", amd)
	_ = addCard(t, root, "card1", "10de", "1c82", "nvidia")
	writeFile(t, filepath.Join(root, "proc", "driver", "nvidia", "gpus", "0000:01:00.0", "information"), "Model:     GeForce GTX 1050 Ti\n")

	st := Detect(root, false)
	if len(st.Devices) != 2 {
		t.Fatalf("want 2 devices, got %+v", st.Devices)
	}
	var sawAMD, sawNV bool
	for _, d := range st.Devices {
		switch d.Vendor {
		case "amd":
			sawAMD = true
			if d.Type != "VAAPI" || !d.VAAPICapable || d.Driver != "amdgpu" {
				t.Fatalf("amd device %+v", d)
			}
		case "nvidia":
			sawNV = true
			if d.Type != "NVDEC" || d.VAAPICapable {
				t.Fatalf("nvidia should be NVDEC and not VAAPI-capable: %+v", d)
			}
			if d.Name != "GeForce GTX 1050 Ti" {
				t.Fatalf("nvidia name %q", d.Name)
			}
		}
	}
	if !sawAMD || !sawNV {
		t.Fatalf("missing vendor in %+v", st.Devices)
	}
	if !st.CanUseVAAPI {
		t.Fatal("AMD + renderD128 should still allow VAAPI")
	}
	if st.ThumbNote != "A VAAPI-capable GPU and /dev/dri/renderD128 are present, but THUMB_VAAPI is off (software thumbnails)." {
		t.Fatalf("note: %s", st.ThumbNote)
	}
}

func TestDetectNone(t *testing.T) {
	root := t.TempDir()
	st := Detect(root, false)
	if st.DevDri || st.RenderD128 || len(st.Devices) != 0 || st.CanUseVAAPI {
		t.Fatalf("expected empty detection: %+v", st)
	}
	if st.Summary != "No GPU acceleration device found" {
		t.Fatalf("summary %q", st.Summary)
	}
	if st.ThumbNote != "No GPU acceleration device was found. Thumbnails use software ffmpeg." {
		t.Fatalf("note: %s", st.ThumbNote)
	}
}

func TestDetectNVIDIAOnlyCannotUseCurrentVAAPIPath(t *testing.T) {
	root := t.TempDir()
	dev := addCard(t, root, "card0", "10de", "2204", "nvidia")
	addRender(t, root, "renderD128", dev)
	st := Detect(root, true)
	if st.CanUseVAAPI {
		t.Fatalf("proprietary NVIDIA should not be treated as usable by the VAAPI thumb path: %+v", st)
	}
	if !st.RenderD128 {
		t.Fatal("render node should still be reported")
	}
	if st.ThumbNote != "THUMB_VAAPI is on, but the current VAAPI thumbnail path cannot use the detected GPU; thumbnails stay on software." {
		t.Fatalf("note: %s", st.ThumbNote)
	}
}

func TestDetectMissingRenderNode(t *testing.T) {
	root := t.TempDir()
	_ = addCard(t, root, "card0", "8086", "3e92", "i915")
	// /dev/dri exists but no render node
	if err := os.MkdirAll(filepath.Join(root, "dev", "dri"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := Detect(root, true)
	if st.CanUseVAAPI || st.RenderD128 {
		t.Fatalf("no render node: %+v", st)
	}
	if st.ThumbNote != "THUMB_VAAPI is on, but /dev/dri/renderD128 was not found; thumbnails will fall back to software." {
		t.Fatalf("note: %s", st.ThumbNote)
	}
}
