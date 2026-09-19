// Package paths locates vrs repositories on disk and resolves scopes.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FindRoot walks up from start looking for a .vrs directory.
// It returns the repository root and whether one was found.
func FindRoot(start string) (string, bool) {
	cur, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if fi, err := os.Stat(filepath.Join(cur, ".vrs")); err == nil && fi.IsDir() {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

// Rel returns the forward-slash path of target relative to root, or "" if
// target is root itself. It errors when target lies outside root.
func Rel(root, target string) (string, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("path %s is outside the repository", target)
	}
	if rel == "." {
		return "", nil
	}
	return rel, nil
}
