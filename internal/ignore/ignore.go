// Package ignore compiles vrs ignore rules: built-in defaults plus the
// repository's .vrsignore (gitignore syntax).
package ignore

import (
	"os"
	"path/filepath"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
)

// builtins always apply; .vrsignore lines are appended after them so users can
// re-include a default with !pattern (later rules win, like gitignore).
var builtins = []string{
	"node_modules/",
	".git/",
	"target/",
	"dist/",
	"build/",
	"out/",
	"__pycache__/",
	".venv/",
	"venv/",
	".DS_Store",
	"*.pyc",
	".env*",
}

// Ignore answers whether a path should be excluded from snapshots.
type Ignore struct {
	gi *gitignore.GitIgnore
}

// Load compiles the built-in defaults plus the .vrsignore found at root, if any.
func Load(root string) (*Ignore, error) {
	lines := append([]string{}, builtins...)
	if b, err := os.ReadFile(filepath.Join(root, ".vrsignore")); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			lines = append(lines, strings.TrimRight(ln, "\r"))
		}
	}
	return &Ignore{gi: gitignore.CompileIgnoreLines(lines...)}, nil
}

// Match reports whether the repo-relative path (forward slashes) is ignored.
// The walk skips ignored directories early, so children of an ignored
// directory are never asked about.
func (ig *Ignore) Match(rel string, isDir bool) bool {
	return ig.gi.MatchesPath(rel)
}
