package api

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
	"github.com/eric/nas-filehub/internal/trash"
)

func (sc *scope) dataRel(rel string) string {
	if sc == nil {
		return db.NormalizeDataRel(rel)
	}
	if rel == "" {
		rel = sc.Rel
	}
	rel = db.NormalizeDataRel(rel)
	base := db.NormalizeDataRel(sc.BaseRel)
	if rel == "" {
		return base
	}
	if base == "" {
		return rel
	}
	return db.NormalizeDataRel(base + "/" + rel)
}

func (s *Server) usedBytes(u *db.User) int64 {
	if u == nil {
		return 0
	}
	home, err := files.DirSize(s.Cfg.DataRoot, u.HomePath)
	if err != nil {
		home = 0
	}
	extra, err := s.DB.SumOwnedBytes(u.ID)
	if err != nil {
		extra = 0
	}
	return home + extra
}

func (s *Server) homeUsedBytes(u *db.User) int64 { return s.usedBytes(u) }

func (s *Server) checkUserQuota(u *db.User, extra int64) error {
	if u == nil || u.QuotaBytes <= 0 {
		return nil
	}
	if extra < 0 {
		extra = 0
	}
	used := s.usedBytes(u)
	if used+extra > u.QuotaBytes {
		return fmt.Errorf("quota exceeded: used %d + %d > limit %d bytes", used, extra, u.QuotaBytes)
	}
	return nil
}

func (s *Server) checkHomeQuota(u *db.User, extra int64) error {
	return s.checkUserQuota(u, extra)
}

func (s *Server) attributeWrite(u *db.User, sc *scope, rel string) {
	if u == nil || sc == nil || sc.Kind != "share" {
		return
	}
	sizes, err := files.ListFileSizes(sc.Root, rel)
	if err != nil {
		return
	}
	for _, f := range sizes {
		_ = s.DB.SetFileOwner(sc.dataRel(f.Rel), u.ID, f.Size)
	}
}

func (s *Server) afterRename(sc *scope, from, to string) {
	if sc == nil || sc.Kind != "share" {
		return
	}
	_ = s.DB.RelocateFileOwnerPrefix(sc.dataRel(from), sc.dataRel(to))
}

func (s *Server) afterTransfer(u *db.User, src, dst *scope, srcRel, dstRel string, move bool) {
	if dst != nil && dst.Kind == "share" {
		if move && src != nil && src.Kind == "share" {
			_ = s.DB.RelocateFileOwnerPrefix(src.dataRel(srcRel), dst.dataRel(dstRel))
			return
		}
		s.attributeWrite(u, dst, dstRel)
	}
	if move && src != nil && src.Kind == "share" && (dst == nil || dst.Kind != "share") {
		_ = s.DB.DeleteFileOwnerPrefix(src.dataRel(srcRel))
	}
}

func (s *Server) ledgerTrashPayload(it trash.Item, ownerID int64) {
	if ownerID <= 0 || it.ID == "" {
		return
	}
	dir := filepath.Join(s.Cfg.DataRoot, trash.DirName, it.ID)
	sizes, err := files.ListFileSizes(dir, "item")
	if err != nil {
		return
	}
	for _, f := range sizes {
		_ = s.DB.SetFileOwner(trash.DirName+"/"+it.ID+"/"+f.Rel, ownerID, f.Size)
	}
}

func (s *Server) trashDelete(u *db.User, sc *scope, rel, rootID string) error {
	if sc == nil || rel == "" || rel == "." {
		return fmt.Errorf("path required")
	}
	if rootID == "" {
		rootID = "home"
	}
	dataRel := sc.dataRel(rel)
	ownerID, _, owned, _ := s.DB.GetFileOwner(dataRel)
	if !owned && sc.Kind == "home" {
		ownerID = u.ID
	}
	it, err := trash.MoveIn(s.Cfg.DataRoot, sc.Root, rel, rootID, rel, ownerID, u.ID)
	if err != nil {
		return err
	}
	if owned {
		_ = s.DB.DeleteFileOwnerPrefix(dataRel)
		s.ledgerTrashPayload(it, ownerID)
	} else if sc.Kind == "home" {
		s.ledgerTrashPayload(it, ownerID)
	}
	_, _ = s.DB.DeleteLinkSharesForPath(rootID, rel)
	return nil
}

func (s *Server) registrationRequireApproval() bool {
	on, err := s.DB.RegistrationRequireApproval()
	if err != nil {
		return true
	}
	return on
}

func userPending(u *db.User) bool {
	return u != nil && db.NormalizeUserStatus(u.Status) == db.UserStatusPending
}

func userActive(u *db.User) bool {
	return u != nil && !u.Disabled && db.NormalizeUserStatus(u.Status) == db.UserStatusActive
}

func joinHomeRestored(homePath, name string) string {
	homePath = strings.Trim(filepath.ToSlash(homePath), "/")
	name = filepath.Base(name)
	if name == "" || name == "." {
		name = "restored"
	}
	return db.NormalizeDataRel(homePath + "/Restored/" + name)
}
