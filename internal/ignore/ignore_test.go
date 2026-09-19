package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func loadWith(t *testing.T, dir, vrsignore string) *Ignore {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if vrsignore != "" {
		if err := os.WriteFile(filepath.Join(dir, ".vrsignore"), []byte(vrsignore), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ig, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ig
}

func TestBuiltins(t *testing.T) {
	ig := loadWith(t, t.TempDir(), "")
	want := map[string]bool{
		"node_modules/x.js":  true,
		"web/node_modules/y": true,
		".git/config":        true,
		"target/debug/x":     true,
		"dist/bundle.js":     true,
		"build/out.o":        true,
		"out/app.js":         true,
		"__pycache__/m.pyc":  true,
		".venv/bin/python":   true,
		"venv/lib/x":         true,
		".DS_Store":          true,
		"deep/nested/x.pyc":  true,
		".env":               true,
		".env.production":    true,
		"src/main.go":        false,
		"builder/built.go":   false, // prefix, not a dir pattern
		"outfile.txt":        false,
		"envelope.txt":       false,
	}
	for p, want := range want {
		if got := ig.Match(p, false); got != want {
			t.Errorf("Match(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestUserPatterns(t *testing.T) {
	dir := t.TempDir()
	body := "# comment line\n" +
		"*.log\n" +
		"/vendor/\n" + // anchored: only the root vendor dir
		"docs/_build/\n" +
		"secret.key\n" +
		"!keep.secret.key\n" + // negation beats the earlier rule
		"**/tmp/\n" + // any tmp dir at any depth
		"*.tmp\n" +
		"!important.tmp\n"
	ig := loadWith(t, dir, body)

	want := map[string]bool{
		"app.log":            true,
		"deep/nested/x.log":  true,
		"vendor/pkg.go":      true,  // root vendor dir
		"a/vendor/pkg.go":    false, // nested vendor is not the anchored one
		"docs/_build/x.html": true,
		"secret.key":         true,
		"keep.secret.key":    false, // negated
		"a/b/tmp/file":       true,  // **/tmp/
		"tmp/file":           true,
		"scratch.tmp":        true,
		"important.tmp":      false, // negated
	}
	for p, want := range want {
		if got := ig.Match(p, false); got != want {
			t.Errorf("Match(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestNegatedBuiltin(t *testing.T) {
	ig := loadWith(t, t.TempDir(), "!node_modules/\n!*.pyc\n")
	if ig.Match("node_modules/x.js", false) {
		t.Fatal("node_modules should be re-included")
	}
	if ig.Match("deep/x.pyc", false) {
		t.Fatal("*.pyc should be re-included")
	}
	if !ig.Match(".git/config", false) {
		t.Fatal("other builtins must still apply")
	}
}
