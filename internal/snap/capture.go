package snap

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/restic/chunker"
)

// Entry is one tracked file at a point in time.
type Entry struct {
	Path    string // repo-relative, forward slashes
	Hash    string // sha256 hex of full content
	Size    int64
	Mode    uint32 // permission bits
	MtimeNS int64
}

// Version is the chunk list of one content version. Capture produces one per
// re-hashed file, deduplicated by content.
type Version struct {
	FileHash string
	Size     int64
	Chunks   []ChunkData
}

// CaptureResult is the full current state of the tree.
type CaptureResult struct {
	Entries  []Entry
	Versions []Version // changed files only (fast-path hits excluded)
}

// CacheEntry mirrors the wc_cache fast path.
type CacheEntry struct {
	MtimeNS int64
	Size    int64
	Hash    string
	Mode    uint32
}

// Ignore decides which paths to exclude from snapshots.
type Ignore interface {
	Match(rel string, isDir bool) bool
}

// Capture walks root and captures the current state of every tracked file.
// Files whose (mtime, size) match cache are trusted unchanged and re-hashing
// is skipped. .vrs is always skipped. Only regular files are tracked;
// symlinks and other special files are ignored.
func Capture(root string, pol chunker.Pol, cache map[string]CacheEntry, ign Ignore) (*CaptureResult, error) {
	res := &CaptureResult{}
	seenVer := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := relPath(root, p)
		if rel == "" {
			return nil
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
		info, err := d.Info()
		if err != nil {
			return err
		}
		hash := ""
		if c, ok := cache[rel]; ok && c.Size == info.Size() && c.MtimeNS == info.ModTime().UnixNano() {
			hash = c.Hash
		} else {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			h, chunks, herr := HashFile(f, pol)
			f.Close()
			if herr != nil {
				return herr
			}
			hash = h
			if !seenVer[h] {
				seenVer[h] = true
				res.Versions = append(res.Versions, Version{FileHash: h, Size: info.Size(), Chunks: chunks})
			}
		}
		res.Entries = append(res.Entries, Entry{
			Path:    rel,
			Hash:    hash,
			Size:    info.Size(),
			Mode:    uint32(info.Mode().Perm()),
			MtimeNS: info.ModTime().UnixNano(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(res.Entries, func(i, j int) bool { return res.Entries[i].Path < res.Entries[j].Path })
	return res, nil
}

func relPath(root, p string) string {
	r := strings.TrimPrefix(p, root)
	r = strings.TrimPrefix(r, string(filepath.Separator))
	return filepath.ToSlash(r)
}

// Summarize diffs current entries against a parent snapshot's state.
// A nil parent counts everything as added (first snapshot).
func Summarize(entries []Entry, prev map[string]Entry) (added, modified, deleted int) {
	if prev == nil {
		return len(entries), 0, 0
	}
	cur := make(map[string]Entry, len(entries))
	for _, e := range entries {
		cur[e.Path] = e
		if p, ok := prev[e.Path]; ok {
			if p.Hash != e.Hash || p.Mode != e.Mode {
				modified++
			}
		} else {
			added++
		}
	}
	for path := range prev {
		if _, ok := cur[path]; !ok {
			deleted++
		}
	}
	return added, modified, deleted
}

// ChangedPaths lists the paths of added/modified files vs a parent snapshot.
func ChangedPaths(entries []Entry, prev map[string]Entry) []string {
	var out []string
	for _, e := range entries {
		if prev == nil {
			return nil
		}
		p, ok := prev[e.Path]
		if !ok || p.Hash != e.Hash || p.Mode != e.Mode {
			out = append(out, e.Path)
		}
	}
	return out
}
