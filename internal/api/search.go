package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/eric/nas-filehub/internal/files"
)

type searchResult struct {
	Root   string `json:"root"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	IsDir  bool   `json:"isDir"`
	Size   int64  `json:"size"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 1 {
		writeJSON(w, 200, map[string]any{"query": q, "results": []searchResult{}})
		return
	}
	const maxTotal = 200
	var out []searchResult

	type loc struct{ id, name, kind string }
	locs := []loc{{"home", "My Files", "home"}}
	if shares, err := s.DB.SharesForUser(u.ID, u.IsAdmin); err == nil {
		for _, sa := range shares {
			locs = append(locs, loc{
				id:   "share:" + strconv.FormatInt(sa.Share.ID, 10),
				name: sa.Share.Name,
				kind: "share",
			})
		}
	}
	for _, loc := range locs {
		if len(out) >= maxTotal {
			break
		}
		sc, err := s.resolveScope(u, loc.id, "")
		if err != nil {
			continue
		}
		hits, err := files.SearchNames(sc.Root, "", q, maxTotal-len(out))
		if err != nil {
			continue
		}
		for _, h := range hits {
			out = append(out, searchResult{
				Root: loc.id, Name: h.Name, Path: h.Path, IsDir: h.IsDir,
				Size: h.Size, Kind: h.Kind, Source: loc.name,
			})
		}
	}
	writeJSON(w, 200, map[string]any{"query": q, "results": out})
}
