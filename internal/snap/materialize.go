package snap

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/restic/chunker"
)

// ContentSource reads back the full bytes of a stored file version.
type ContentSource interface {
	ReadVersion(fileHash string) ([]byte, error)
}

// MatResult reports what a Materialize did.
type MatResult struct {
	Written []Entry  // files written to disk (fresh mtimes)
	Trashed []string // paths moved under the trash directory
}

// Materialize makes the working copy within scope match target: modified
// files are overwritten, files missing from disk are recreated, and files on
// disk that are not in target are moved under trashBase (never deleted).
// Files already matching are untouched, so it is idempotent.
func Materialize(root, scope string, target map[string]Entry, cache map[string]CacheEntry, src ContentSource, trashBase string, pol chunker.Pol, ign Ignore) (*MatResult, error) {
	stats, err := Status(root, scope, target, cache, pol, ign)
	if err != nil {
		return nil, err
	}
	res := &MatResult{}
	for _, st := range stats {
		switch st.Status {
		case StatusSame:
			continue
		case StatusModified, StatusDeleted:
			if err := writeFile(root, target[st.Path], src, res); err != nil {
				return nil, err
			}
		case StatusAdded:
			if err := trashFile(root, st.Path, trashBase, res); err != nil {
				return nil, err
			}
		}
	}
	return res, nil
}

func writeFile(root string, e Entry, src ContentSource, res *MatResult) error {
	data, err := src.ReadVersion(e.Hash)
	if err != nil {
		return fmt.Errorf("read %s: %w", e.Path, err)
	}
	dst := filepath.Join(root, filepath.FromSlash(e.Path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".vrs-tmp"
	if err := os.WriteFile(tmp, data, fs.FileMode(e.Mode)); err != nil {
		return err
	}
	if err := os.Chmod(tmp, fs.FileMode(e.Mode)); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return err
	}
	res.Written = append(res.Written, Entry{
		Path: e.Path, Hash: e.Hash, Size: info.Size(),
		Mode: uint32(info.Mode().Perm()), MtimeNS: info.ModTime().UnixNano(),
	})
	return nil
}

func trashFile(root, rel, trashBase string, res *MatResult) error {
	src := filepath.Join(root, filepath.FromSlash(rel))
	dst := filepath.Join(trashBase, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	res.Trashed = append(res.Trashed, rel)
	return nil
}
