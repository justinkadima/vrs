package snap

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/restic/chunker"
)

// Working-copy states relative to a snapshot.
const (
	StatusSame     = "same"
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusDeleted  = "deleted"
)

// FileStatus is the state of one path vs a snapshot.
type FileStatus struct {
	Path   string
	Status string
}

// Status compares the working copy within scope against the target snapshot's
// entries. scope is "" (whole tree) or a repo-relative file or directory.
// Ignore rules apply to walked paths; an explicitly named file is exempt
// (the user asked about it directly).
func Status(root, scope string, target map[string]Entry, cache map[string]CacheEntry, pol chunker.Pol, ign Ignore) ([]FileStatus, error) {
	if scope == "" {
		return statusWalk(root, "", target, cache, pol, ign)
	}
	full := filepath.Join(root, filepath.FromSlash(scope))
	fi, err := os.Stat(full)
	if os.IsNotExist(err) {
		if _, ok := target[scope]; ok {
			return []FileStatus{{Path: scope, Status: StatusDeleted}}, nil
		}
		return nil, fmt.Errorf("no such path: %s", scope)
	}
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return statusWalk(root, scope, target, cache, pol, ign)
	}
	st, err := classify(root, scope, fi, target, cache, pol)
	if err != nil {
		return nil, err
	}
	return []FileStatus{{Path: scope, Status: st}}, nil
}

func statusWalk(root, scope string, target map[string]Entry, cache map[string]CacheEntry, pol chunker.Pol, ign Ignore) ([]FileStatus, error) {
	seen := map[string]bool{}
	var out []FileStatus
	walkRoot := root
	if scope != "" {
		walkRoot = filepath.Join(root, filepath.FromSlash(scope))
	}
	err := filepath.WalkDir(walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := relPath(walkRoot, p)
		if rel == "" {
			return nil
		}
		if scope != "" {
			rel = scope + "/" + rel
		}
		if d.IsDir() {
			if d.Name() == ".vrs" || ign.Match(rel, true) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || ign.Match(rel, false) {
			return nil
		}
		seen[rel] = true
		info, err := d.Info()
		if err != nil {
			return err
		}
		st, err := classify(root, rel, info, target, cache, pol)
		if err != nil {
			return err
		}
		out = append(out, FileStatus{Path: rel, Status: st})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Target entries within scope that no longer exist on disk.
	var gone []string
	for rel := range target {
		if !seen[rel] && inScope(rel, scope) {
			gone = append(gone, rel)
		}
	}
	sort.Strings(gone)
	for _, rel := range gone {
		out = append(out, FileStatus{Path: rel, Status: StatusDeleted})
	}
	return out, nil
}

// classify determines one on-disk file's state vs the target snapshot.
func classify(root, rel string, info fs.FileInfo, target map[string]Entry, cache map[string]CacheEntry, pol chunker.Pol) (string, error) {
	tgt, ok := target[rel]
	if !ok {
		return StatusAdded, nil
	}
	perm := uint32(info.Mode().Perm())
	if c, ok := cache[rel]; ok &&
		c.Size == info.Size() && c.MtimeNS == info.ModTime().UnixNano() &&
		c.Mode == perm && c.Mode == tgt.Mode && c.Hash == tgt.Hash {
		return StatusSame, nil
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h, _, err := HashFile(f, pol)
	if err != nil {
		return "", err
	}
	if h == tgt.Hash && perm == tgt.Mode {
		return StatusSame, nil
	}
	return StatusModified, nil
}

func inScope(rel, scope string) bool {
	if scope == "" {
		return true
	}
	return rel == scope || strings.HasPrefix(rel, scope+"/")
}
