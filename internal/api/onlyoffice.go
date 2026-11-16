package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
)

const (
	onlyOfficeContentTTL  = 2 * time.Hour
	onlyOfficeCallbackTTL = 24 * time.Hour
)

var onlyOfficeExtTypes = map[string]string{
	".doc":  "word",
	".docx": "word",
	".odt":  "word",
	".rtf":  "word",
	".txt":  "word",
	".xls":  "cell",
	".xlsx": "cell",
	".ods":  "cell",
	".csv":  "cell",
	".ppt":  "slide",
	".pptx": "slide",
	".odp":  "slide",
}

func onlyOfficeFileType(name string) (fileType, docType string, ok bool) {
	ext := strings.ToLower(filepath.Ext(name))
	docType, ok = onlyOfficeExtTypes[ext]
	if !ok {
		return "", "", false
	}
	return strings.TrimPrefix(ext, "."), docType, true
}

func (s *Server) effectiveOnlyOffice() (db.OnlyOfficeSettings, error) {
	return s.DB.GetOnlyOfficeSettings()
}

func (s *Server) onlyOfficeReady() bool {
	if s.Cfg.PublicBaseURL == "" {
		return false
	}
	cfg, err := s.effectiveOnlyOffice()
	return err == nil && cfg.Ready()
}

func (s *Server) onlyOfficePublic(cfg db.OnlyOfficeSettings) map[string]any {
	ready := cfg.Ready() && s.Cfg.PublicBaseURL != ""
	out := map[string]any{
		"enabled":          cfg.Enabled,
		"url":              cfg.URL,
		"jwtSecretSet":     cfg.JWTSecretSet,
		"ready":            ready,
		"publicBaseURLSet": s.Cfg.PublicBaseURL != "",
	}
	if cfg.Enabled && strings.TrimSpace(cfg.URL) != "" {
		s.ooHealthMu.Lock()
		cached := !s.ooHealthAt.IsZero()
		ok, msg := s.ooHealthOK, s.ooHealthMsg
		s.ooHealthMu.Unlock()
		if cached {
			out["documentServerReachable"] = ok
			out["documentServerHealth"] = msg
		}
		go s.documentServerHealth(cfg)
	}
	return out
}

func (s *Server) validateOnlyOfficeSettings(in db.OnlyOfficeSettings) error {
	if err := db.ValidateOnlyOfficeURL(in.URL); err != nil {
		return err
	}
	if !in.Enabled {
		return nil
	}
	if s.Cfg.PublicBaseURL == "" {
		return fmt.Errorf("PUBLIC_BASE_URL is required to enable ONLYOFFICE")
	}
	if strings.TrimSpace(in.URL) == "" {
		return fmt.Errorf("ONLYOFFICE Document Server URL is required")
	}
	if strings.TrimSpace(in.JWTSecret) == "" {
		return fmt.Errorf("ONLYOFFICE JWT secret is required")
	}
	return nil
}

func b64url(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}

func b64urlDecode(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

func signHS256JWT(secret string, payload any) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("jwt secret required")
	}
	header := b64url([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadPart := b64url(body)
	signingInput := header + "." + payloadPart
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	sig := b64url(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

func verifyHS256JWT(secret, token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid jwt")
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	expected := b64url(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, fmt.Errorf("invalid jwt signature")
	}
	raw, err := b64urlDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid jwt payload")
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, fmt.Errorf("invalid jwt payload")
	}
	if exp, ok := claims["exp"]; ok {
		var expN int64
		switch v := exp.(type) {
		case float64:
			expN = int64(v)
		case json.Number:
			expN, _ = v.Int64()
		case int64:
			expN = v
		}
		if expN > 0 && time.Now().Unix() > expN {
			return nil, fmt.Errorf("jwt expired")
		}
	}
	return claims, nil
}

func claimString(claims map[string]any, key string) string {
	v, ok := claims[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}

func claimInt64(claims map[string]any, key string) (int64, bool) {
	v, ok := claims[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case int64:
		return t, true
	case int:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// onlyOfficeDocumentKey identifies an on-disk file for ONLYOFFICE co-editing.
// It must be the same for every accessor of the same file (My Files vs Shared with me),
// so it hashes the resolved absolute path plus mtime/size — not the client root/path.
func onlyOfficeDocumentKey(full string, st os.FileInfo) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", filepath.Clean(full), st.ModTime().UnixNano(), st.Size())))
	return fmt.Sprintf("%x", sum[:20])
}

func (s *Server) handleOnlyOfficeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	cfg, err := s.effectiveOnlyOffice()
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	writeJSON(w, 200, map[string]any{
		"ready":            s.onlyOfficeReady(),
		"enabled":          cfg.Enabled,
		"publicBaseURLSet": s.Cfg.PublicBaseURL != "",
	})
}

func (s *Server) handleOnlyOfficeConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	if !s.onlyOfficeReady() {
		writeErr(w, 400, "ONLYOFFICE is not configured")
		return
	}
	u := s.currentUser(r)
	if u == nil {
		writeErr(w, 401, "unauthorized")
		return
	}
	if !s.allowOnlyOfficeConfig(u.ID) {
		writeErr(w, 429, "too many requests")
		return
	}
	rootID := r.URL.Query().Get("root")
	rel := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
	if rootID == "" {
		rootID = "home"
	}
	if rel == "" {
		writeErr(w, 400, "path required")
		return
	}
	if rootID == "trash" {
		writeErr(w, 403, "cannot edit files in the Recycle bin")
		return
	}
	// Resolve first: incoming item-share paths are share ids without an extension.
	scFile, err := s.resolveScope(u, rootID, rel)
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	if scFile.Kind == "trash" {
		writeErr(w, 403, "cannot edit files in the Recycle bin")
		return
	}
	full, err := files.SafeJoin(scFile.Root, scFile.Rel)
	if err != nil {
		writeErr(w, 400, "invalid path")
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		writeErr(w, 404, "file not found")
		return
	}
	realName := filepath.Base(full)
	fileType, docType, ok := onlyOfficeFileType(realName)
	if !ok {
		writeErr(w, 400, "unsupported office file type")
		return
	}
	canEdit := scFile.canWrite() && scFile.Kind != "trash"
	mode := "view"
	if canEdit {
		mode = "edit"
	}
	cfg, err := s.effectiveOnlyOffice()
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	base := strings.TrimRight(s.Cfg.PublicBaseURL, "/")
	now := time.Now().Unix()
	contentClaims := map[string]any{
		"typ":  "oo-content",
		"uid":  u.ID,
		"root": rootID,
		"path": rel,
		"exp":  now + int64(onlyOfficeContentTTL.Seconds()),
	}
	contentTok, err := signHS256JWT(cfg.JWTSecret, contentClaims)
	if err != nil {
		writeErr(w, 500, "token error")
		return
	}
	callbackClaims := map[string]any{
		"typ":  "oo-callback",
		"uid":  u.ID,
		"root": rootID,
		"path": rel,
		"exp":  now + int64(onlyOfficeCallbackTTL.Seconds()),
	}
	callbackTok, err := signHS256JWT(cfg.JWTSecret, callbackClaims)
	if err != nil {
		writeErr(w, 500, "token error")
		return
	}
	title := realName
	docKey := onlyOfficeDocumentKey(full, st)
	config := map[string]any{
		"documentType": docType,
		"document": map[string]any{
			"fileType": fileType,
			"key":      docKey,
			"title":    title,
			"url":      base + "/api/onlyoffice/content?token=" + url.QueryEscape(contentTok),
			"permissions": map[string]any{
				"edit":     canEdit,
				"download": true,
				"print":    true,
				"review":   canEdit,
				"comment":  canEdit,
			},
		},
		"editorConfig": map[string]any{
			"callbackUrl": base + "/api/onlyoffice/callback?token=" + url.QueryEscape(callbackTok),
			"mode":        mode,
			"lang":        "en",
			"user": map[string]any{
				"id":   strconv.FormatInt(u.ID, 10),
				"name": u.Username,
			},
			"customization": map[string]any{
				"forcesave": true,
				"autosave":  true,
			},
			"coEditing": map[string]any{
				"mode":   "fast",
				"change": true,
			},
		},
	}
	token, err := signHS256JWT(cfg.JWTSecret, config)
	if err != nil {
		writeErr(w, 500, "token error")
		return
	}
	config["token"] = token
	apiScript := cfg.URL + "/web-apps/apps/api/documents/api.js"
	s.audit(r, u.ID, u.Username, "onlyoffice.open", "root="+rootID+" kind="+scFile.Kind+" path="+rel+" mode="+mode)
	writeJSON(w, 200, map[string]any{
		"documentServerUrl":       cfg.URL,
		"documentServerApiScript": apiScript,
		"config":                  config,
		"mode":                    mode,
		"documentKey":             docKey,
	})
}

func (s *Server) handleOnlyOfficeContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, 405, "method not allowed")
		return
	}
	cfg, err := s.effectiveOnlyOffice()
	if err != nil || !cfg.Ready() || s.Cfg.PublicBaseURL == "" {
		writeErr(w, 400, "ONLYOFFICE is not configured")
		return
	}
	tok := r.URL.Query().Get("token")
	if tok == "" {
		writeErr(w, 401, "token required")
		return
	}
	claims, err := verifyHS256JWT(cfg.JWTSecret, tok)
	if err != nil {
		writeErr(w, 401, "invalid token")
		return
	}
	if claimString(claims, "typ") != "oo-content" {
		writeErr(w, 401, "invalid token")
		return
	}
	uid, ok := claimInt64(claims, "uid")
	if !ok || uid <= 0 {
		writeErr(w, 401, "invalid token")
		return
	}
	rootID := claimString(claims, "root")
	rel := strings.TrimPrefix(claimString(claims, "path"), "/")
	u, err := s.DB.GetUserByID(uid)
	if err != nil || u == nil || u.Disabled {
		writeErr(w, 401, "unauthorized")
		return
	}
	sc, err := s.resolveScope(u, rootID, rel)
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	full, err := files.SafeJoin(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 400, "invalid path")
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		writeErr(w, 404, "file not found")
		return
	}
	f, err := os.Open(full)
	if err != nil {
		writeErr(w, 404, "file not found")
		return
	}
	defer f.Close()
	name := filepath.Base(full)
	w.Header().Set("Content-Type", onlyOfficeContentType(name))
	w.Header().Set("Content-Disposition", contentDisposition("attachment", name))
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func onlyOfficeContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".odt":
		return "application/vnd.oasis.opendocument.text"
	case ".rtf":
		return "application/rtf"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".odp":
		return "application/vnd.oasis.opendocument.presentation"
	default:
		return "application/octet-stream"
	}
}

func (s *Server) handleOnlyOfficeCallback(w http.ResponseWriter, r *http.Request) {
	// ALWAYS respond with JSON error field for ONLYOFFICE; use helper.
	reply := func(code int) {
		writeJSON(w, 200, map[string]int{"error": code})
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		reply(1)
		return
	}
	cfg, err := s.effectiveOnlyOffice()
	if err != nil || !cfg.Ready() || s.Cfg.PublicBaseURL == "" {
		reply(1)
		return
	}
	tok := r.URL.Query().Get("token")
	if tok == "" {
		reply(1)
		return
	}
	pathClaims, err := verifyHS256JWT(cfg.JWTSecret, tok)
	if err != nil || claimString(pathClaims, "typ") != "oo-callback" {
		reply(1)
		return
	}
	uid, ok := claimInt64(pathClaims, "uid")
	if !ok || uid <= 0 {
		reply(1)
		return
	}
	rootID := claimString(pathClaims, "root")
	rel := strings.TrimPrefix(claimString(pathClaims, "path"), "/")

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		reply(1)
		return
	}
	var body map[string]any
	// Body may be raw JWT or JSON (possibly with "token").
	trimmed := strings.TrimSpace(string(bodyBytes))
	if strings.Count(trimmed, ".") == 2 && !strings.HasPrefix(trimmed, "{") {
		claims, err := verifyHS256JWT(cfg.JWTSecret, trimmed)
		if err != nil {
			reply(1)
			return
		}
		body = claims
	} else {
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			reply(1)
			return
		}
		if t, ok := body["token"].(string); ok && t != "" {
			claims, err := verifyHS256JWT(cfg.JWTSecret, t)
			if err != nil {
				reply(1)
				return
			}
			body = claims
		} else if ah := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(ah), "bearer ") {
			claims, err := verifyHS256JWT(cfg.JWTSecret, strings.TrimSpace(ah[7:]))
			if err != nil {
				reply(1)
				return
			}
			// Merge path-bearing payload from Authorization JWT when body lacked token.
			for k, v := range claims {
				if _, exists := body[k]; !exists {
					body[k] = v
				}
			}
			if st, ok := claims["status"]; ok {
				body["status"] = st
			}
			if u, ok := claims["url"]; ok {
				body["url"] = u
			}
		}
	}

	status, _ := claimInt64(body, "status")
	// 1 = editing, 4 = closed no changes — acknowledge.
	if status == 1 || status == 4 || status == 0 {
		reply(0)
		return
	}
	// 2 = ready for save, 6 = force save
	if status != 2 && status != 6 {
		// 3/7 errors — acknowledge so DS does not retry forever.
		reply(0)
		return
	}
	fileURL := claimString(body, "url")
	if fileURL == "" {
		reply(1)
		return
	}
	u, err := s.DB.GetUserByID(uid)
	if err != nil || u == nil || u.Disabled {
		reply(1)
		return
	}
	sc, err := s.resolveScope(u, rootID, rel)
	if err != nil || !sc.canWrite() || sc.Kind == "trash" {
		reply(1)
		return
	}
	full, err := files.SafeJoin(sc.Root, sc.Rel)
	if err != nil {
		reply(1)
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		reply(1)
		return
	}
	oldSize := st.Size()

	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodGet, fileURL, nil)
	if err != nil {
		reply(1)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		reply(1)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		reply(1)
		return
	}
	// Stream to temp then rename over target (in-place replace; do not trash).
	dir := filepath.Dir(full)
	tmp, err := os.CreateTemp(dir, ".oo-save-*")
	if err != nil {
		reply(1)
		return
	}
	tmpName := tmp.Name()
	written, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, s.maxUploadBytes()))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		reply(1)
		return
	}
	delta := written - oldSize
	if delta > 0 {
		if err := s.checkScopeQuota(u, sc, delta); err != nil {
			_ = os.Remove(tmpName)
			reply(1)
			return
		}
	}
	if err := os.Rename(tmpName, full); err != nil {
		_ = os.Remove(tmpName)
		reply(1)
		return
	}
	s.attributeWrite(u, sc, sc.Rel)
	s.audit(r, u.ID, u.Username, "onlyoffice.save", "root="+rootID+" kind="+sc.Kind+" path="+rel)
	reply(0)
}

func sanitizeOfficeCreateName(name, ext string) (string, error) {
	name = strings.TrimSpace(name)
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid filename")
	}
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid filename")
	}
	ext = strings.ToLower(ext)
	if !strings.HasSuffix(strings.ToLower(name), ext) {
		name += ext
	}
	return name, nil
}

func (s *Server) handleOnlyOfficeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	if !s.onlyOfficeReady() {
		writeErr(w, 400, "ONLYOFFICE is not configured")
		return
	}
	u := s.currentUser(r)
	var body struct {
		Root string `json:"root"`
		Path string `json:"path"`
		Name string `json:"name"`
		Kind string `json:"kind"` // document|spreadsheet|presentation
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	rootID := body.Root
	if rootID == "" {
		rootID = "home"
	}
	if rootID == "trash" {
		writeErr(w, 403, "cannot create files in the Recycle bin")
		return
	}
	dir := strings.TrimPrefix(body.Path, "/")
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	var ext string
	var payload []byte
	switch kind {
	case "document", "docx", "word", "":
		kind = "document"
		ext = ".docx"
		payload = onlyOfficeBlankDOCX
	case "spreadsheet", "xlsx", "cell", "excel":
		kind = "spreadsheet"
		ext = ".xlsx"
		payload = onlyOfficeBlankXLSX
	case "presentation", "pptx", "slide", "powerpoint":
		kind = "presentation"
		ext = ".pptx"
		payload = onlyOfficeBlankPPTX
	default:
		writeErr(w, 400, "unsupported kind (use document, spreadsheet, or presentation)")
		return
	}
	name, err := sanitizeOfficeCreateName(body.Name, ext)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	sc, err := s.resolveScope(u, rootID, dir)
	if err != nil || !sc.canWrite() || sc.Kind == "trash" {
		writeErr(w, 403, "forbidden")
		return
	}
	// Conflict: rename like uploads (NextAvailableName).
	res, err := s.applyUploadedFile(u, sc, rootID, name, bytes.NewReader(payload), int64(len(payload)), files.ConflictRename)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.audit(r, u.ID, u.Username, "onlyoffice.create", rootID+":"+res.Rel)
	writeJSON(w, 200, map[string]any{
		"ok":   "1",
		"root": rootID,
		"path": res.Rel,
		"name": res.Name,
		"kind": kind,
	})
}

func (s *Server) allowOnlyOfficeConfig(userID int64) bool {
	if s == nil {
		return true
	}
	s.ooLimitOnce.Do(func() {
		if s.OOConfigLimiter == nil {
			s.OOConfigLimiter = auth.NewLoginLimiter(60)
		}
	})
	if s.OOConfigLimiter == nil {
		return true
	}
	return s.OOConfigLimiter.Allow("u:" + strconv.FormatInt(userID, 10))
}

func (s *Server) documentServerHealth(cfg db.OnlyOfficeSettings) (bool, string) {
	s.ooHealthMu.Lock()
	if !s.ooHealthAt.IsZero() && time.Since(s.ooHealthAt) < 30*time.Second {
		ok, msg := s.ooHealthOK, s.ooHealthMsg
		s.ooHealthMu.Unlock()
		return ok, msg
	}
	s.ooHealthMu.Unlock()

	ok, msg := pingOnlyOfficeDocumentServer(cfg.URL)
	s.ooHealthMu.Lock()
	s.ooHealthOK, s.ooHealthMsg, s.ooHealthAt = ok, msg, time.Now()
	s.ooHealthMu.Unlock()
	return ok, msg
}

func pingOnlyOfficeDocumentServer(rawURL string) (bool, string) {
	base := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if base == "" {
		return false, "no Document Server URL"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, path := range []string{"/healthcheck", "/isHealthy"} {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err.Error()
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return true, "ok"
		}
	}
	return false, "Document Server health check failed"
}

// WatchOnlyOfficeHealth periodically logs a warning when ONLYOFFICE is enabled
// but the Document Server does not respond.
func (s *Server) WatchOnlyOfficeHealth() {
	s.logOnlyOfficeHealth()
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		s.logOnlyOfficeHealth()
	}
}

func (s *Server) logOnlyOfficeHealth() {
	if s == nil || s.DB == nil {
		return
	}
	cfg, err := s.effectiveOnlyOffice()
	if err != nil || !cfg.Enabled || strings.TrimSpace(cfg.URL) == "" {
		return
	}
	s.ooHealthMu.Lock()
	s.ooHealthAt = time.Time{} // force a fresh probe
	s.ooHealthMu.Unlock()
	ok, msg := s.documentServerHealth(cfg)
	if !ok {
		log.Printf("ONLYOFFICE Document Server health check failed (%s): %s", cfg.URL, msg)
	}
}
