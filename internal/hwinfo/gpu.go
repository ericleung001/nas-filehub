package hwinfo

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Device is one detected GPU / DRM adapter.
type Device struct {
	Vendor       string `json:"vendor"`     // intel, amd, nvidia, unknown
	VendorName   string `json:"vendorName"` // Intel, AMD, NVIDIA, Unknown
	Type         string `json:"type"`       // VAAPI/QSV, VAAPI, NVDEC, unknown
	Driver       string `json:"driver,omitempty"`
	Name         string `json:"name,omitempty"`
	PCIID        string `json:"pciId,omitempty"`
	Card         string `json:"card,omitempty"`
	Render       string `json:"render,omitempty"`
	VAAPICapable bool   `json:"vaapiCapable"`
}

// Status is the GPU / thumbnail-acceleration snapshot shown on Settings → System.
type Status struct {
	ThumbVAAPI  bool     `json:"thumbVAAPI"`
	DevDri      bool     `json:"devDri"`
	RenderD128  bool     `json:"renderD128"`
	RenderNodes []string `json:"renderNodes"`
	Devices     []Device `json:"devices"`
	Summary     string   `json:"summary"`
	ThumbNote   string   `json:"thumbNote"`
	CanUseVAAPI bool     `json:"canUseVAAPI"`
}

var cardNameRe = regexp.MustCompile(`^card\d+$`)
var renderNameRe = regexp.MustCompile(`^renderD\d+$`)

var pciVendors = map[string]struct {
	id, name, kind string
	vaapi          bool
}{
	"8086": {"intel", "Intel", "VAAPI/QSV", true},
	"1002": {"amd", "AMD", "VAAPI", true},
	"1022": {"amd", "AMD", "VAAPI", true},
	"10de": {"nvidia", "NVIDIA", "NVDEC", false},
}

// Detect inspects hostRoot (usually "/") for /dev/dri and /sys/class/drm.
// thumbVAAPI is the current process setting (THUMB_VAAPI).
func Detect(hostRoot string, thumbVAAPI bool) Status {
	if hostRoot == "" {
		hostRoot = "/"
	}
	st := Status{
		ThumbVAAPI:  thumbVAAPI,
		RenderNodes: []string{},
		Devices:     []Device{},
	}

	driDir := filepath.Join(hostRoot, "dev", "dri")
	if info, err := os.Stat(driDir); err == nil && info.IsDir() {
		st.DevDri = true
		entries, _ := os.ReadDir(driDir)
		for _, e := range entries {
			name := e.Name()
			if renderNameRe.MatchString(name) {
				st.RenderNodes = append(st.RenderNodes, name)
				if name == "renderD128" {
					st.RenderD128 = true
				}
			}
		}
		sort.Strings(st.RenderNodes)
	}

	nvidiaModels := readNvidiaModels(hostRoot)
	devices := detectDRMCards(hostRoot)
	// Attach unused render nodes by matching device identity already done in detectDRMCards.
	// If we found /dev/dri/renderD* but no sysfs cards, still record that a render node exists.
	if len(devices) == 0 && st.RenderD128 {
		devices = append(devices, Device{
			Vendor:     "unknown",
			VendorName: "Unknown",
			Type:       "unknown",
			Name:       "DRM render device",
			Render:     "renderD128",
		})
	}
	for i := range devices {
		applyNvidiaModel(&devices[i], nvidiaModels)
		finalizeDevice(&devices[i])
	}
	st.Devices = devices

	anyVAAPI := false
	for _, d := range devices {
		if d.VAAPICapable {
			anyVAAPI = true
			break
		}
	}
	st.CanUseVAAPI = st.RenderD128 && anyVAAPI
	st.Summary = summarize(st)
	st.ThumbNote = thumbNote(st)
	return st
}

func detectDRMCards(hostRoot string) []Device {
	drmDir := filepath.Join(hostRoot, "sys", "class", "drm")
	entries, err := os.ReadDir(drmDir)
	if err != nil {
		return nil
	}
	type cardInfo struct {
		name   string
		devDir string
	}
	var cards []cardInfo
	renders := map[string]string{} // resolved device dir -> renderD*
	for _, e := range entries {
		name := e.Name()
		link := filepath.Join(drmDir, name)
		if cardNameRe.MatchString(name) {
			devDir := resolveDeviceDir(link)
			cards = append(cards, cardInfo{name: name, devDir: devDir})
		}
		if renderNameRe.MatchString(name) {
			devDir := resolveDeviceDir(link)
			if devDir != "" {
				if _, ok := renders[devDir]; !ok {
					renders[devDir] = name
				}
			}
		}
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].name < cards[j].name })

	var out []Device
	seen := map[string]bool{}
	for _, c := range cards {
		key := c.devDir
		if key == "" {
			key = c.name
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		d := readCard(c.name, c.devDir)
		if r, ok := renders[c.devDir]; ok && c.devDir != "" {
			d.Render = r
		}
		out = append(out, d)
	}
	return out
}

func resolveDeviceDir(drmLink string) string {
	// /sys/class/drm/card0 is a symlink into a PCI (or platform) device's drm/card0.
	// The PCI device itself is at .../card0/device or the parent of the drm node.
	target, err := filepath.EvalSymlinks(drmLink)
	if err != nil {
		// Fall back to the "device" subdir even if the class entry is a real dir (tests).
		dev := filepath.Join(drmLink, "device")
		if resolved, err := filepath.EvalSymlinks(dev); err == nil {
			return resolved
		}
		if st, err := os.Stat(dev); err == nil && st.IsDir() {
			return dev
		}
		return ""
	}
	dev := filepath.Join(target, "device")
	if resolved, err := filepath.EvalSymlinks(dev); err == nil {
		return resolved
	}
	if st, err := os.Stat(dev); err == nil && st.IsDir() {
		return dev
	}
	// Already pointing at the PCI device in some layouts.
	return target
}

func readCard(cardName, devDir string) Device {
	d := Device{Card: cardName, Vendor: "unknown", VendorName: "Unknown", Type: "unknown"}
	if devDir == "" {
		return d
	}
	vendor := normalizePCIID(readTrim(filepath.Join(devDir, "vendor")))
	device := normalizePCIID(readTrim(filepath.Join(devDir, "device")))
	if vendor != "" && device != "" {
		d.PCIID = strings.ToLower(vendor + ":" + device)
	} else if vendor != "" {
		d.PCIID = strings.ToLower(vendor)
	}

	uevent := readUevent(filepath.Join(devDir, "uevent"))
	d.Driver = firstNonEmpty(readDriverName(devDir), uevent["DRIVER"])
	if d.PCIID == "" {
		if pci := uevent["PCI_ID"]; pci != "" {
			d.PCIID = strings.ToLower(pci)
			if parts := strings.Split(d.PCIID, ":"); len(parts) == 2 {
				vendor = parts[0]
			}
		}
	}

	if info, ok := pciVendors[strings.ToLower(vendor)]; ok {
		d.Vendor, d.VendorName, d.Type, d.VAAPICapable = info.id, info.name, info.kind, info.vaapi
	}

	// Driver can refine NVIDIA nouveau (limited VAAPI) vs proprietary (NVDEC).
	switch strings.ToLower(d.Driver) {
	case "i915", "xe", "i965":
		d.Vendor, d.VendorName, d.Type, d.VAAPICapable = "intel", "Intel", "VAAPI/QSV", true
	case "amdgpu", "radeon":
		d.Vendor, d.VendorName, d.Type, d.VAAPICapable = "amd", "AMD", "VAAPI", true
	case "nvidia":
		d.Vendor, d.VendorName, d.Type, d.VAAPICapable = "nvidia", "NVIDIA", "NVDEC", false
	case "nouveau":
		d.Vendor, d.VendorName, d.Type, d.VAAPICapable = "nvidia", "NVIDIA", "VAAPI (nouveau)", true
	}

	if name := strings.TrimSpace(readTrim(filepath.Join(devDir, "label"))); name != "" {
		d.Name = name
	}
	return d
}

func finalizeDevice(d *Device) {
	if d.Name != "" {
		return
	}
	switch d.Vendor {
	case "intel":
		d.Name = "Intel graphics"
	case "amd":
		d.Name = "AMD graphics"
	case "nvidia":
		d.Name = "NVIDIA graphics"
	default:
		if d.Driver != "" {
			d.Name = "Graphics (" + d.Driver + ")"
		} else {
			d.Name = "Unknown graphics"
		}
	}
}

func applyNvidiaModel(d *Device, models []string) {
	if d.Vendor != "nvidia" || len(models) == 0 || d.Name != "" && !strings.EqualFold(d.Name, "NVIDIA graphics") {
		if d.Vendor == "nvidia" && d.Name == "" && len(models) == 1 {
			d.Name = models[0]
		}
		return
	}
	if len(models) == 1 {
		d.Name = models[0]
		return
	}
	// Multiple NVIDIA GPUs: leave generic unless we only have one nvidia device later.
	if d.Name == "" {
		d.Name = models[0]
	}
}

func readNvidiaModels(hostRoot string) []string {
	base := filepath.Join(hostRoot, "proc", "driver", "nvidia", "gpus")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var models []string
	for _, e := range entries {
		info := filepath.Join(base, e.Name(), "information")
		f, err := os.Open(info)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(strings.ToLower(line), "model:") {
				m := strings.TrimSpace(line[len("Model:"):])
				if m == "" {
					// handle "Model:\t..."
					m = strings.TrimSpace(strings.TrimPrefix(line, line[:strings.Index(line, ":")+1]))
				}
				if i := strings.Index(line, ":"); i >= 0 {
					m = strings.TrimSpace(line[i+1:])
				}
				if m != "" {
					models = append(models, m)
				}
				break
			}
		}
		f.Close()
	}
	return models
}

func readDriverName(devDir string) string {
	link := filepath.Join(devDir, "driver")
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func readUevent(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func normalizePCIID(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return ""
	}
	if n, err := strconv.ParseUint(s, 16, 32); err == nil {
		return strings.ToLower(strconv.FormatUint(n, 16))
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func summarize(st Status) string {
	if len(st.Devices) == 0 {
		if st.DevDri {
			return "DRM device present, vendor unknown"
		}
		return "No GPU acceleration device found"
	}
	parts := make([]string, 0, len(st.Devices))
	for _, d := range st.Devices {
		label := d.VendorName
		if d.Type != "" && d.Type != "unknown" {
			label += " (" + d.Type + ")"
		}
		if d.Name != "" && !strings.EqualFold(d.Name, d.VendorName+" graphics") {
			label += " — " + d.Name
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "; ")
}

func thumbNote(st Status) string {
	if !st.DevDri && len(st.Devices) == 0 {
		return "No GPU acceleration device was found. Thumbnails use software ffmpeg."
	}
	if st.ThumbVAAPI && st.CanUseVAAPI {
		return "THUMB_VAAPI is on; ffmpeg can use VAAPI on /dev/dri/renderD128."
	}
	if st.ThumbVAAPI && !st.RenderD128 {
		return "THUMB_VAAPI is on, but /dev/dri/renderD128 was not found; thumbnails will fall back to software."
	}
	if st.ThumbVAAPI && !st.CanUseVAAPI {
		return "THUMB_VAAPI is on, but the current VAAPI thumbnail path cannot use the detected GPU; thumbnails stay on software."
	}
	if !st.ThumbVAAPI && st.CanUseVAAPI {
		return "A VAAPI-capable GPU and /dev/dri/renderD128 are present, but THUMB_VAAPI is off (software thumbnails)."
	}
	if !st.ThumbVAAPI {
		return "THUMB_VAAPI is off. Thumbnails use software ffmpeg."
	}
	return "Thumbnails use software ffmpeg."
}
