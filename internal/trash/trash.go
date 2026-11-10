package trash

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/eric/nas-filehub/internal/files"
)

const DirName = files.TrashDirName

type Item struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	IsDir      bool      `json:"isDir"`
	Size       int64     `json:"size"`
	OwnerID    int64     `json:"ownerId"`
	DeletedBy  int64     `json:"deletedBy"`
	OriginRoot string    `json:"originRoot"`
	OriginPath string    `json:"originPath"`
	DataRel    string    `json:"dataRel"`
	DeletedAt  time.Time `json:"deletedAt"`
}

func Root(dataRoot string) string {
	return filepath.Join(dataRoot, DirName)
}

func itemDir(dataRoot, id string) string {
	return filepath.Join(Root(dataRoot), id)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func writeMeta(dir string, it Item) error {
	f, err := os.Create(filepath.Join(dir, "meta.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(it)
}

func readMeta(dir string) (Item, error) {
	var it Item
	f, err := os.Open(filepath.Join(dir, "meta.json"))
	if err != nil {
		return it, err
	}
	defer f.Close()
	err = json.NewDecoder(f).Decode(&it)
	return it, err
}

// RelPath is the data-root-relative path of the trashed payload (for the quota ledger).
func RelPath(id string) string {
	return DirName + "/" + id + "/item"
}

// MoveIn moves srcRoot/srcRel into the recycle bin and returns the new item.
func MoveIn(dataRoot, srcRoot, srcRel, originRoot, originPath string, ownerID, deletedBy int64) (Item, error) {
	var it Item
	src, err := files.SafeJoin(srcRoot, srcRel)
	if err != nil {
		return it, err
	}
	st, err := os.Lstat(src)
	if err != nil {
		return it, err
	}
	id, err := newID()
	if err != nil {
		return it, err
	}
	dir := itemDir(dataRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return it, err
	}
	dest := filepath.Join(dir, "item")
	size, _ := files.EntrySize(srcRoot, srcRel)
	it = Item{
		ID: id, Name: st.Name(), IsDir: st.IsDir(), Size: size,
		OwnerID: ownerID, DeletedBy: deletedBy,
		OriginRoot: originRoot, OriginPath: originPath,
		DataRel: RelPath(id), DeletedAt: time.Now().UTC(),
	}
	if err := writeMeta(dir, it); err != nil {
		_ = os.RemoveAll(dir)
		return it, err
	}
	if err := os.Rename(src, dest); err != nil {
		// Cross-device fallback: copy then delete.
		if copyErr := files.CopyEntry(srcRoot, srcRel, dir, "item"); copyErr != nil {
			_ = os.RemoveAll(dir)
			return it, err
		}
		if delErr := files.Delete(srcRoot, srcRel); delErr != nil {
			_ = os.RemoveAll(dir)
			return it, delErr
		}
	}
	return it, nil
}

func Load(dataRoot, id string) (Item, error) {
	return readMeta(itemDir(dataRoot, id))
}

func List(dataRoot string, userID int64, admin bool) ([]Item, error) {
	root := Root(dataRoot)
	ents, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Item
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		it, err := readMeta(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		if !admin && it.OwnerID != userID && it.DeletedBy != userID {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DeletedAt.After(out[j].DeletedAt)
	})
	return out, nil
}

// Count returns the number of recycle-bin entries visible to the user.
// The set matches List: top-level trash items, not a recursive file walk.
func Count(dataRoot string, userID int64, admin bool) (int, error) {
	items, err := List(dataRoot, userID, admin)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func RestoreDest(dataRoot, destRoot, destRel string) (string, error) {
	full, err := files.SafeJoin(destRoot, destRel)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(full); os.IsNotExist(err) {
		return destRel, nil
	} else if err != nil {
		return "", err
	}
	ext := filepath.Ext(destRel)
	base := destRel[:len(destRel)-len(ext)]
	for i := 1; i < 1000; i++ {
		cand := fmt.Sprintf("%s-restored-%d%s", base, i, ext)
		p, err := files.SafeJoin(destRoot, cand)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("could not find a free restore name")
}

func Restore(dataRoot, id, destRoot, destRel string) (Item, string, error) {
	var it Item
	it, err := Load(dataRoot, id)
	if err != nil {
		return it, "", err
	}
	dir := itemDir(dataRoot, id)
	src := filepath.Join(dir, "item")
	if _, err := os.Lstat(src); err != nil {
		return it, "", err
	}
	finalRel, err := RestoreDest(dataRoot, destRoot, destRel)
	if err != nil {
		return it, "", err
	}
	if err := os.MkdirAll(filepath.Dir(mustJoin(destRoot, finalRel)), 0o755); err != nil {
		return it, "", err
	}
	if err := files.MoveEntry(dir, "item", destRoot, finalRel); err != nil {
		return it, "", err
	}
	_ = os.RemoveAll(dir)
	return it, finalRel, nil
}

func mustJoin(root, rel string) string {
	p, err := files.SafeJoin(root, rel)
	if err != nil {
		return filepath.Join(root, rel)
	}
	return p
}

func Purge(dataRoot, id string) (Item, error) {
	it, err := Load(dataRoot, id)
	if err != nil {
		return it, err
	}
	if err := os.RemoveAll(itemDir(dataRoot, id)); err != nil {
		return it, err
	}
	return it, nil
}

func Empty(dataRoot string, userID int64, admin bool) (int, error) {
	items, err := List(dataRoot, userID, admin)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if _, err := Purge(dataRoot, it.ID); err == nil {
			n++
		}
	}
	return n, nil
}
