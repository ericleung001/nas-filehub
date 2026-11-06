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
	VAAPI    bool
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

	var cmd *exec.Cmd
	if g.VAAPI {
		// Intel Quick Sync / VAAPI path for Unraid J4125 with --device=/dev/dri
		cmd = exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
			"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi",
			"-i", src,
			"-vf", "scale_vaapi=w=320:h=-2,hwdownload,format=nv12,format=yuv420p",
			"-frames:v", "1", "-q:v", "4", "-y", out,
		)
		if err := cmd.Run(); err == nil {
			return out, nil
		}
		// fall through to software
		_ = os.Remove(out)
	}

	cmd = exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", src,
		"-vf", "scale=320:-2",
		"-frames:v", "1", "-q:v", "4", "-y", out,
	)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(out)
		return "", fmt.Errorf("ffmpeg failed: %w", err)
	}
	return out, nil
}
