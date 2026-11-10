package thumb

import (
	"strings"

	"github.com/eric/nas-filehub/internal/hwinfo"
)

// Mode is the THUMB_HWACCEL setting (auto, off, vaapi, nvdec).
type Mode string

const (
	ModeOff   Mode = "off"
	ModeAuto  Mode = "auto"
	ModeVAAPI Mode = "vaapi"
	ModeNVDEC Mode = "nvdec"
)

// ParseMode resolves THUMB_HWACCEL and the legacy THUMB_VAAPI flag.
// If hwaccel is unset/empty and thumbVAAPI is true, Auto is used.
// If hwaccel is unset and thumbVAAPI is false, Off is used.
func ParseMode(hwaccel string, thumbVAAPI bool) Mode {
	s := strings.ToLower(strings.TrimSpace(hwaccel))
	switch s {
	case "", "legacy":
		if thumbVAAPI {
			return ModeAuto
		}
		return ModeOff
	case "0", "false", "no", "off", "none", "software":
		return ModeOff
	case "1", "true", "yes", "on", "auto":
		return ModeAuto
	case "vaapi":
		return ModeVAAPI
	case "nvdec", "cuda", "cuvid", "nvidia":
		return ModeNVDEC
	default:
		if thumbVAAPI {
			return ModeAuto
		}
		return ModeOff
	}
}

// Plan is a selected hardware (or software) thumbnail path.
type Plan struct {
	Backend    string // software, vaapi, nvdec
	Device     string // e.g. /dev/dri/renderD128
	Vendor     string
	FallbackOK bool
}

// SelectPlan chooses an ffmpeg backend from detected GPUs and the configured mode.
func SelectPlan(mode Mode, st hwinfo.Status) Plan {
	if mode == ModeOff {
		return Plan{Backend: "software"}
	}
	wantVAAPI := mode == ModeVAAPI || mode == ModeAuto
	wantNV := mode == ModeNVDEC || mode == ModeAuto

	if wantVAAPI {
		for _, d := range st.Devices {
			if !d.VAAPICapable {
				continue
			}
			dev := renderDevice(d, st)
			if dev == "" {
				continue
			}
			return Plan{Backend: "vaapi", Device: dev, Vendor: d.Vendor, FallbackOK: true}
		}
	}
	if wantNV {
		for _, d := range st.Devices {
			if d.Vendor != "nvidia" {
				continue
			}
			// Proprietary NVIDIA: CUDA/NVDEC path (may be unavailable in the
			// Debian ffmpeg build; caller must fall back to software).
			if strings.EqualFold(d.Driver, "nouveau") && wantVAAPI && d.VAAPICapable {
				dev := renderDevice(d, st)
				if dev != "" {
					return Plan{Backend: "vaapi", Device: dev, Vendor: d.Vendor, FallbackOK: true}
				}
			}
			if !strings.EqualFold(d.Driver, "nouveau") {
				return Plan{Backend: "nvdec", Vendor: d.Vendor, FallbackOK: true}
			}
		}
	}
	return Plan{Backend: "software"}
}

func renderDevice(d hwinfo.Device, st hwinfo.Status) string {
	if d.Render != "" {
		return "/dev/dri/" + d.Render
	}
	if st.RenderD128 {
		return "/dev/dri/renderD128"
	}
	if len(st.RenderNodes) > 0 {
		return "/dev/dri/" + st.RenderNodes[0]
	}
	return ""
}

// HWAccelArgs is the ffmpeg argument list before -i and after, excluding the
// output path. Software is a plain -i pipeline; VAAPI/NVDEC add hwaccel flags.
func HWAccelArgs(plan Plan, src, out string) (hw []string, soft []string) {
	soft = []string{"-hide_banner", "-loglevel", "error", "-i", src, "-vf", "scale=320:-2", "-frames:v", "1", "-q:v", "4", "-y", out}
	switch plan.Backend {
	case "vaapi":
		dev := plan.Device
		if dev == "" {
			dev = "/dev/dri/renderD128"
		}
		hw = []string{
			"-hide_banner", "-loglevel", "error",
			"-hwaccel", "vaapi", "-hwaccel_device", dev, "-hwaccel_output_format", "vaapi",
			"-i", src,
			"-vf", "scale_vaapi=w=320:h=-2,hwdownload,format=nv12,format=yuv420p",
			"-frames:v", "1", "-q:v", "4", "-y", out,
		}
	case "nvdec":
		hw = []string{
			"-hide_banner", "-loglevel", "error",
			"-hwaccel", "cuda", "-hwaccel_output_format", "cuda",
			"-i", src,
			"-vf", "scale_cuda=320:-2,hwdownload,format=nv12",
			"-frames:v", "1", "-q:v", "4", "-y", out,
		}
	}
	return hw, soft
}

func (p Plan) CanUseHardware() bool {
	return p.Backend == "vaapi" || p.Backend == "nvdec"
}

func (p Plan) Note(mode Mode) string {
	if mode == ModeOff || p.Backend == "software" {
		if mode == ModeOff {
			return "Hardware thumbnail acceleration is off. Thumbnails use software ffmpeg."
		}
		return "No usable hardware path was selected. Thumbnails use software ffmpeg."
	}
	if p.Backend == "vaapi" {
		return "ffmpeg will try VAAPI on " + p.Device + " and fall back to software on failure."
	}
	return "ffmpeg will try NVIDIA CUDA/NVDEC and fall back to software on failure. This requires an ffmpeg build with CUDA support and a working NVIDIA runtime."
}

func detectGPUs() hwinfo.Status {
	return hwinfo.Detect("/", false)
}
