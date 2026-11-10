package thumb

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type Generator struct {
	CacheDir string
	Enable   bool
	VAAPI    bool // legacy THUMB_VAAPI; used when Mode is unset
	Mode     Mode
	mu       sync.Mutex
}

func (g *Generator) cacheKey(src string) string {
	h := sha1.Sum([]byte(src))
	return hex.EncodeToString(h[:]) + ".jpg"
}

func (g *Generator) PathFor(src string) string {
	return filepath.Join(g.CacheDir, g.cacheKey(src))
}

// Get returns path to a JPEG thumbnail, generating if needed.
// Returns ("", err) if disabled, ffmpeg missing, or generation fails.
func (g *Generator) Get(src string) (string, error) {
	if !g.Enable {
		return "", fmt.Errorf("thumbnails disabled")
	}
	if err := os.MkdirAll(g.CacheDir, 0o755); err != nil {
		return "", err
	}
	out := g.PathFor(src)
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out, nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found")
	}
	ext := strings.ToLower(filepath.Ext(src))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".mp4", ".webm", ".mkv", ".mov", ".m4v", ".ogg":
		// ok
	default:
		return "", fmt.Errorf("unsupported type")
	}

	mode := g.Mode
	if mode == "" {
		mode = ParseMode("", g.VAAPI)
	}
	plan := SelectPlan(mode, detectGPUs())
	hw, soft := HWAccelArgs(plan, src, out)
	if len(hw) > 0 {
		if err := exec.Command("ffmpeg", hw...).Run(); err == nil {
			return out, nil
		}
		_ = os.Remove(out)
	}
	cmd := exec.Command("ffmpeg", soft...)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(out)
		return "", fmt.Errorf("ffmpeg failed: %w", err)
	}
	return out, nil
}
