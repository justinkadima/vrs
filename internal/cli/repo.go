package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/justinkadima/vrs/internal/ignore"
	"github.com/justinkadima/vrs/internal/paths"
	"github.com/justinkadima/vrs/internal/snap"
	"github.com/justinkadima/vrs/internal/store"
	"github.com/restic/chunker"
)

// repoCtx bundles everything a command needs from an open repository.
type repoCtx struct {
	root  string
	st    *store.Store
	pol   chunker.Pol
	ig    *ignore.Ignore
	cache map[string]snap.CacheEntry
}

func openRepo() (*repoCtx, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, ok := paths.FindRoot(cwd)
	if !ok {
		return nil, fmt.Errorf("not a vrs repository — run `vrs save` to start one here")
	}
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	polyHex, err := st.Meta("chunker_poly")
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("read chunker polynomial: %w", err)
	}
	pol, err := snap.ParsePolynomial(polyHex)
	if err != nil {
		st.Close()
		return nil, err
	}
	ig, err := ignore.Load(root)
	if err != nil {
		st.Close()
		return nil, err
	}
	cache, err := st.LoadCache()
	if err != nil {
		st.Close()
		return nil, err
	}
	return &repoCtx{root: root, st: st, pol: pol, ig: ig, cache: cache}, nil
}

// resolveScope maps an optional user path argument (relative to the current
// directory) to a repo-relative scope. No argument means the current
// directory's subtree; at the repository root that is the whole tree ("").
func (rc *repoCtx) resolveScope(arg string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	target := cwd
	if arg != "" {
		target = filepath.Join(cwd, arg)
	}
	return paths.Rel(rc.root, target)
}

func scopeLabel(scope string) string {
	if scope == "" {
		return "working tree"
	}
	return scope
}

// initRepoHere auto-initializes a repository at dir (used by save and
// import — the two commands that legitimately create a repo). The caller
// prints the announcement.
func initRepoHere(dir string) (*store.Store, string, error) {
	poly, err := snap.RandomPolynomialHex()
	if err != nil {
		return nil, "", err
	}
	st, err := store.InitAt(dir, poly)
	if err != nil {
		return nil, "", err
	}
	return st, dir, nil
}

// baseOrRef resolves the snapshot a command operates on: the working
// position, or an explicit @ref. Returns 0 when the repo has no snapshots.
func (rc *repoCtx) baseOrRef(ref *refSpec) (int64, error) {
	id, err := rc.st.Base()
	if err != nil {
		return 0, err
	}
	if ref != nil {
		if id, err = resolveRef(rc.st, *ref); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// trashDir returns a fresh timestamped trash directory for a mutating op.
func (rc *repoCtx) trashDir() string {
	return filepath.Join(rc.root, ".vrs", "trash", fmt.Sprintf("%d", time.Now().UnixNano()))
}
