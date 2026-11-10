package api

import (
	"net/http"
	"path/filepath"
	"strings"
)

// scriptableExts are types that a browser may execute as a document or script
// if served inline from the Filehub origin. These are forced to download.
var scriptableExts = map[string]bool{
	".html":  true,
	".htm":   true,
	".shtml": true,
	".xhtml": true,
	".xhtm":  true,
	".svg":   true,
	".svgz":  true,
	".xml":   true,
	".xsl":   true,
	".xslt":  true,
	".js":    true,
	".mjs":   true,
	".cjs":   true,
	".mhtml": true,
	".mht":   true,
	".hta":   true,
}

func isScriptableName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return scriptableExts[ext]
}

func fallbackASCIIFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if r > 0x7e {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "download"
	}
	return s
}

func rfc5987Filename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			strings.ContainsRune("!#$&+-.^_`|~", r) {
			b.WriteRune(r)
			continue
		}
		for _, by := range []byte(string(r)) {
			b.WriteByte('%')
			const hexdigits = "0123456789ABCDEF"
			b.WriteByte(hexdigits[by>>4])
			b.WriteByte(hexdigits[by&0x0f])
		}
	}
	return b.String()
}

// contentDisposition builds a RFC 6266 / 5987 Content-Disposition value.
// Newlines and NULs are stripped; quotes and backslashes are escaped in the
// quoted-string fallback; non-ASCII or escaped names also get filename*.
func contentDisposition(disposition, filename string) string {
	name := filepath.Base(filename)
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "download"
	}
	fallback := fallbackASCIIFilename(name)
	quoted := strings.ReplaceAll(fallback, `\`, `\\`)
	quoted = strings.ReplaceAll(quoted, `"`, `\"`)
	out := disposition + `; filename="` + quoted + `"`
	if fallback != name || strings.ContainsAny(name, `"\`) {
		out += "; filename*=UTF-8''" + rfc5987Filename(name)
	}
	return out
}

const (
	documentCSP       = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; media-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-src 'self' blob:; frame-ancestors 'none'"
	frameableCSP      = "frame-ancestors 'self'"
	permissionsPolicy = "camera=(), microphone=(), geolocation=()"
)

func setFileDisposition(w http.ResponseWriter, name string, inline bool) {
	if inline && isScriptableName(name) {
		inline = false
		w.Header().Set("Content-Type", "application/octet-stream")
	} else if !inline && isScriptableName(name) {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if inline {
		// Override global DENY so same-origin <iframe>/<embed> can preview PDFs.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", frameableCSP)
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".pdf" {
			w.Header().Set("Content-Type", "application/pdf")
		}
		w.Header().Set("Content-Disposition", contentDisposition("inline", name))
		return
	}
	w.Header().Set("Content-Disposition", contentDisposition("attachment", name))
}

func setInlineFileHeaders(w http.ResponseWriter, name string) {
	w.Header().Del("Content-Disposition")
	setFileDisposition(w, name, true)
}
