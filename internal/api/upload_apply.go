package api

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
)

func (s *Server) maxUploadBytes() int64 {
	mb := s.Cfg.MaxUploadMB
	if mb <= 0 {
		mb = 2048
	}
	return mb << 20
}

func (s *Server) stagingRoot() string {
	return filepath.Join(s.Cfg.DataRoot, files.UploadsDirName)
}

func (s *Server) newStagingID() (string, error) {
	return auth.NewToken()
}

// writeStaging copies r into DATA_ROOT/.filehub-uploads/<id>/data and returns the id.
func (s *Server) writeStaging(id string, r io.Reader) (string, error) {
	if id == "" {
		var err error
		id, err = s.newStagingID()
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Join(s.stagingRoot(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, "data")
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.RemoveAll(dir)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.RemoveAll(dir)
		return "", closeErr
	}
	return id, nil
}

func (s *Server) removeStaging(id string) {
	if id == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(s.stagingRoot(), id))
}

// resolveConflict decides the destination filename and whether the existing
// file must be moved to the recycle bin. It does not write bytes.
func (s *Server) resolveConflict(sc *scope, filename string, action files.ConflictAction) (chosen string, overwrite bool, skipped bool, err error) {
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == ".." {
		return "", false, false, fmt.Errorf("invalid filename")
	}
	exists, isDir, err := files.Exists(sc.Root, sc.Rel, filename)
	if err != nil {
		return "", false, false, err
	}
	if !exists {
		return filename, false, false, nil
	}
	switch action {
	case files.ConflictSkip:
		return filename, false, true, nil
	case files.ConflictOverwrite:
		if isDir {
			return "", false, false, files.ErrIsDirectory
		}
		if !sc.canDelete() {
			return "", false, false, fmt.Errorf("overwrite requires delete permission")
		}
		return filename, true, false, nil
	case files.ConflictRename:
		dir, err := files.SafeJoin(sc.Root, sc.Rel)
		if err != nil {
			return "", false, false, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", false, false, err
		}
		chosen, err = files.NextAvailableName(dir, filename)
		if err != nil {
			return "", false, false, err
		}
		return chosen, false, false, nil
	default:
		return "", false, false, files.ErrExists
	}
}

// applyUploadedFile writes r into place under sc, honoring conflict policy.
// Overwrite moves the existing file into the recycle bin first. If that move
// fails, the original file is left in place and the staged upload is discarded.
func (s *Server) applyUploadedFile(u *db.User, sc *scope, rootID, filename string, r io.Reader, size int64, action files.ConflictAction) (files.SaveUploadResult, error) {
	filename = filepath.Base(filename)
	chosen, overwrite, skipped, err := s.resolveConflict(sc, filename, action)
	if err != nil {
		return files.SaveUploadResult{}, err
	}
	rel := joinDest(sc.Rel, chosen)
	if skipped {
		return files.SaveUploadResult{Rel: joinDest(sc.Rel, filename), Name: filename, Skipped: true}, nil
	}
	if size < 0 {
		size = 0
	}
	if err := s.checkUserQuota(u, size); err != nil {
		return files.SaveUploadResult{}, err
	}
	id, err := s.writeStaging("", r)
	if err != nil {
		return files.SaveUploadResult{}, err
	}
	if err := s.commitStaging(u, sc, rootID, filename, chosen, overwrite, id); err != nil {
		s.removeStaging(id)
		return files.SaveUploadResult{}, err
	}
	return files.SaveUploadResult{
		Rel:         rel,
		Name:        chosen,
		Overwritten: overwrite,
		Renamed:     chosen != filename,
	}, nil
}

func (s *Server) commitStaging(u *db.User, sc *scope, rootID, origName, chosen string, overwrite bool, stagingID string) error {
	if overwrite {
		existing := joinDest(sc.Rel, origName)
		if err := s.trashDelete(u, sc, existing, rootID); err != nil {
			return err
		}
	}
	dir, err := files.SafeJoin(sc.Root, sc.Rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	destRel := joinDest(sc.Rel, chosen)
	srcRel := filepath.ToSlash(filepath.Join(files.UploadsDirName, stagingID, "data"))
	if err := files.MoveEntry(s.Cfg.DataRoot, srcRel, sc.Root, destRel); err != nil {
		return err
	}
	s.removeStaging(stagingID)
	s.attributeWrite(u, sc, destRel)
	return nil
}
