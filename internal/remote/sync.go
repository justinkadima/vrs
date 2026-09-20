package remote

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/justinkadima/vrs/internal/snap"
)

// manifest is the state record vrs maintains at export targets.
type manifest struct {
	Vrs   int                      `json:"vrs"`
	Files map[string]manifestEntry `json:"files"`
}

type manifestEntry struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
	Mode uint32 `json:"mode"`
}

// RepoConfigName is the repository's own config file. It is tracked and
// versioned like any other file, but never shipped: a target directory
// is not a vrs repository. Nested files of the same name are user
// content (per-directory scoping, like gitignore) and do ship.
const RepoConfigName = ".vrsignore"

// Reserved reports whether a snapshot entry path collides with the names vrs
// owns at a target.
func Reserved(p string) bool {
	return p == ManifestName || p == TrashDirName || len(p) > len(TrashDirName)+1 &&
		p[:len(TrashDirName)+1] == TrashDirName+"/"
}

func loadManifest(fs Fs, root string) (manifest, error) {
	m := manifest{Files: map[string]manifestEntry{}}
	b, err := fs.ReadFile(posixJoin(root, ManifestName))
	if err != nil {
		if IsNotExist(err) {
			return m, nil // first export
		}
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil || m.Vrs != 1 {
		// Corrupt or foreign manifest: treat as absent (full upload).
		return manifest{Files: map[string]manifestEntry{}}, nil
	}
	return m, nil
}

func saveManifest(fs Fs, root string, m manifest) error {
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return fs.WriteFile(posixJoin(root, ManifestName), b, 0o644)
}

// ScanProgress is called as a source walk considers each file: scanned
// counts files seen so far. Calls are cheap — callers throttle.
type ScanProgress func(scanned int, path string)

// FileProgress reports a file being transferred: done is 1-based, total
// is the number of files in the pass (0 if unknown), size in bytes.
type FileProgress func(done, total int, path string, size int64)

// ExportResult reports what an export did.
type ExportResult struct {
	Snapshot int64
	Written  []string // snapshot paths uploaded
	ModeOnly int      // files present and correct but chmod'ed
	Skipped  int      // already at the recorded state
	Pruned   []string // target paths moved to trash (--prune)
}

// ExportTree materializes snapshot entries onto dst, rooted at root.
// src provides file content by hash (the vrs store). Incremental via the
// target manifest; the manifest is rewritten only after everything
// succeeded, so an interrupted export leaves the target consistent.
func ExportTree(entries map[string]snap.Entry, src snap.ContentSource, dst Fs, root string, prune bool, prog FileProgress) (*ExportResult, error) {
	old, err := loadManifest(dst, root)
	if err != nil {
		return nil, err
	}
	if err := dst.MkdirAll(root); err != nil {
		return nil, err
	}

	res := &ExportResult{}
	paths := make([]string, 0, len(entries))
	for p := range entries {
		if p == RepoConfigName {
			continue // repo config: versioned, but targets are not vrs repos
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for i, p := range paths {
		e := entries[p]
		if m, ok := old.Files[p]; ok && m.Hash == e.Hash && m.Size == e.Size {
			if m.Mode == e.Mode {
				res.Skipped++
				continue
			}
			// Content identical, permissions drifted: fix in place.
			if err := dst.Chmod(posixJoin(root, p), e.Mode); err != nil {
				return nil, err
			}
			res.ModeOnly++
			continue
		}
		data, err := src.ReadVersion(e.Hash)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if prog != nil {
			prog(i+1, len(paths), p, e.Size)
		}
		if err := dst.WriteFile(posixJoin(root, p), data, e.Mode); err != nil {
			return nil, fmt.Errorf("write %s: %w", p, err)
		}
		res.Written = append(res.Written, p)
	}

	if prune {
		trashBase := posixJoin(root, TrashDirName, fmt.Sprint(time.Now().UnixNano()))
		targetPaths, err := walkFiles(dst, root)
		if err != nil {
			return nil, err
		}
		for _, p := range targetPaths {
			if _, tracked := entries[p]; tracked {
				continue
			}
			dstPath := posixJoin(trashBase, p)
			if err := dst.MkdirAll(posixDir(dstPath)); err != nil {
				return nil, err
			}
			if err := dst.Rename(posixJoin(root, p), dstPath); err != nil {
				return nil, fmt.Errorf("prune %s: %w", p, err)
			}
			res.Pruned = append(res.Pruned, p)
		}
	}

	newManifest := manifest{Vrs: 1, Files: make(map[string]manifestEntry, len(entries))}
	for p, e := range entries {
		newManifest.Files[p] = manifestEntry{Hash: e.Hash, Size: e.Size, Mode: e.Mode}
	}
	if err := saveManifest(dst, root, newManifest); err != nil {
		return nil, err
	}
	return res, nil
}

// Planned is one file an import will download.
type Planned struct {
	Rel   string
	Size  int64
	Mode  uint32
	Mtime int64
}

// ImportPlan is the computed difference between a source tree and the
// working tree, before anything is touched.
type ImportPlan struct {
	Source  string
	Files   []Planned // remote files to download (overlay)
	Pruned  []string  // local tracked files absent from the source (--prune)
	present map[string]bool
}

// QuickCheck reports whether a local file can be assumed to match the
// source: same size and mtime, and both mtimes known. Same deliberate
// tradeoff as rsync/git — document, don't fix with a rehash of everything.
func quickMatch(local, remote FileInfo) bool {
	return local.Size == remote.Size && local.Mtime != 0 && remote.Mtime != 0 &&
		local.Mtime == remote.Mtime
}

// PlanImport walks the source tree and computes what an import would do,
// without touching anything. ign is the repository's ignore rules; they
// filter the download set exactly as a save would.
func PlanImport(src Fs, srcRoot string, dst Fs, dstRoot string, ign snap.Ignore, prune bool, scan ScanProgress) (*ImportPlan, error) {
	plan := &ImportPlan{present: map[string]bool{}}

	var walk func(rel string) error
	walk = func(rel string) error {
		fis, err := src.ReadDir(posixJoin(srcRoot, rel))
		if err != nil {
			return err
		}
		sort.Slice(fis, func(i, j int) bool { return fis[i].Name < fis[j].Name })
		for _, fi := range fis {
			name := fi.Name
			child := name
			if rel != "" {
				child = rel + "/" + name
			}
			if name == ".vrs" || name == TrashDirName {
				continue // never touch a repo's storage or our own trash, on either side
			}
			if fi.IsDir {
				if ign.Match(child, true) {
					continue
				}
				if err := walk(child); err != nil {
					return err
				}
				continue
			}
			if !fi.Regular || name == ManifestName || name == TrashDirName {
				continue
			}
			if ign.Match(child, false) {
				continue
			}
			plan.present[child] = true
			if scan != nil {
				scan(len(plan.present), child)
			}

			local, err := dst.Stat(posixJoin(dstRoot, child))
			switch {
			case err == nil && quickMatch(local, fi):
				continue // already have it
			case err == nil:
				plan.Files = append(plan.Files, Planned{Rel: child, Size: fi.Size, Mode: fi.Mode, Mtime: fi.Mtime})
			case IsNotExist(err):
				plan.Files = append(plan.Files, Planned{Rel: child, Size: fi.Size, Mode: fi.Mode, Mtime: fi.Mtime})
			default:
				return err
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}

	if prune {
		localPaths, err := walkFiltered(dst, dstRoot, ign)
		if err != nil {
			return nil, err
		}
		for _, p := range localPaths {
			if !plan.present[p] {
				plan.Pruned = append(plan.Pruned, p)
			}
		}
	}
	return plan, nil
}

// ApplyImport performs a plan: download files (preserving mtimes, so
// repeats are incremental), then move pruned files to trashBase.
func ApplyImport(plan *ImportPlan, src Fs, srcRoot string, dst Fs, dstRoot, trashBase string, prog FileProgress) error {
	for i, pl := range plan.Files {
		if prog != nil {
			prog(i+1, len(plan.Files), pl.Rel, pl.Size)
		}
		data, err := src.ReadFile(posixJoin(srcRoot, pl.Rel))
		if err != nil {
			return fmt.Errorf("read %s: %w", pl.Rel, err)
		}
		if err := dst.WriteFile(posixJoin(dstRoot, pl.Rel), data, pl.Mode); err != nil {
			return fmt.Errorf("write %s: %w", pl.Rel, err)
		}
		if pl.Mtime != 0 {
			if err := dst.SetMtime(posixJoin(dstRoot, pl.Rel), pl.Mtime); err != nil {
				return fmt.Errorf("mtime %s: %w", pl.Rel, err)
			}
		}
	}
	for _, p := range plan.Pruned {
		dstPath := posixJoin(trashBase, p)
		if err := dst.MkdirAll(posixDir(dstPath)); err != nil {
			return err
		}
		if err := dst.Rename(posixJoin(dstRoot, p), dstPath); err != nil {
			return fmt.Errorf("prune %s: %w", p, err)
		}
	}
	return nil
}

// walkFiles lists every regular file under root (rel paths), skipping the
// names vrs owns. Used by export --prune to find target extras.
func walkFiles(fs Fs, root string) ([]string, error) {
	var out []string
	var walk func(rel string) error
	walk = func(rel string) error {
		fis, err := fs.ReadDir(posixJoin(root, rel))
		if err != nil {
			return err
		}
		for _, fi := range fis {
			name := fi.Name
			child := name
			if rel != "" {
				child = rel + "/" + name
			}
			if name == ManifestName || name == TrashDirName || (rel == "" && name == RepoConfigName) {
				continue
			}
			if fi.IsDir {
				if err := walk(child); err != nil {
					return err
				}
				continue
			}
			if fi.Regular {
				out = append(out, child)
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// walkFiltered lists tracked-candidate files under root: regular, not
// ignored, not inside .vrs. The repo's own .vrsignore is never a prune
// candidate — it is repository config, not working content. Mirrors the
// capture walk's semantics.
func walkFiltered(fs Fs, root string, ign snap.Ignore) ([]string, error) {
	var out []string
	var walk func(rel string) error
	walk = func(rel string) error {
		fis, err := fs.ReadDir(posixJoin(root, rel))
		if err != nil {
			return err
		}
		for _, fi := range fis {
			name := fi.Name
			child := name
			if rel != "" {
				child = rel + "/" + name
			}
			if name == ".vrs" || name == ".vrsignore" {
				continue
			}
			if fi.IsDir {
				if !ign.Match(child, true) {
					if err := walk(child); err != nil {
						return err
					}
				}
				continue
			}
			if fi.Regular && !ign.Match(child, false) {
				out = append(out, child)
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func posixDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			if i == 0 {
				return "/"
			}
			return p[:i]
		}
	}
	return "."
}
