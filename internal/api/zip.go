package api

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/eric/nas-filehub/internal/files"
)

func (s *Server) handleZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	sc, err := s.resolveScope(u, r.URL.Query().Get("root"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	if sc.Kind == "trash" {
		writeErr(w, 400, "use download from the recycle bin item")
		return
	}
	s.streamZip(w, sc)
}

func (s *Server) streamZip(w http.ResponseWriter, sc *scope) {
	full, err := files.SafeJoin(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 400, "bad path")
		return
	}
	st, err := os.Lstat(full)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	name := st.Name()
	if name == "" || name == "." {
		name = "download"
	}
	if !st.IsDir() {
		name = name + ".zip"
	} else {
		name = name + ".zip"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(name)+`"`)
	if _, err := files.WriteZip(w, sc.Root, sc.Rel, time.Now()); err != nil {
		// Too late to change status if bytes were already written.
		return
	}
}
