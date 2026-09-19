// Package paths locates vrs repositories on disk.
package paths

import (
	"os"
	"path/filepath"
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
