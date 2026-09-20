package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportLocal(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, "a.txt", "one\n")
	if _, err := run(t, "save", "base"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, "a.txt", "two\n")
	if _, err := run(t, "save"); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join("..", "export-out")

	// Full export of the position (#2): 2 files (a.txt + .vrsignore).
	out, err := run(t, "export", outDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exported #2 to") || !strings.Contains(out, "2 file(s) written") {
		t.Fatalf("export: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(outDir, "a.txt")); string(b) != "two\n" {
		t.Fatalf("exported content: %q", b)
	}
	if _, err := os.Stat(filepath.Join(outDir, ".vrs-manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	// Idempotent: manifest matches now.
	out, err = run(t, "export", outDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0 file(s) written") || !strings.Contains(out, "2 unchanged") {
		t.Fatalf("re-export: %s", out)
	}

	// Explicit @ref: back to #1's content, incremental.
	out, err = run(t, "export", outDir, "@1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 file(s) written") {
		t.Fatalf("export @1: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(outDir, "a.txt")); string(b) != "one\n" {
		t.Fatalf("export @1 content: %q", b)
	}

	// --prune trashes target extras, never deletes.
	writeFile(t, filepath.Join(outDir, "extra.txt"), "extra\n")
	out, err = run(t, "export", "--prune", outDir, "@2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ".vrs-trash") || !strings.Contains(out, "1") {
		t.Fatalf("prune: %s", out)
	}
	if _, err := os.Stat(filepath.Join(outDir, "extra.txt")); !os.IsNotExist(err) {
		t.Fatal("extra.txt not pruned")
	}
	trashed, _ := filepath.Glob(filepath.Join(outDir, ".vrs-trash", "*", "extra.txt"))
	if len(trashed) != 1 {
		t.Fatalf("extra.txt not in target trash: %v", trashed)
	}

	// Refuse to export into the repository itself.
	if _, err := run(t, "export", "."); err == nil || !strings.Contains(err.Error(), "inside the repository") {
		t.Fatalf("export inside repo: %v", err)
	}

	// Reserved names are refused.
	writeFile(t, ".vrs-manifest.json", "{}")
	if _, err := run(t, "save", "oops"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "export", outDir); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved name: %v", err)
	}
}

func TestImportLocal(t *testing.T) {
	// Bootstrap: an empty folder becomes a versioned mirror of the source.
	repo := t.TempDir()
	t.Chdir(repo)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "index.html"), "<html/>")
	writeFile(t, filepath.Join(src, "assets", "app.js"), "console.log(1)")
	writeFile(t, filepath.Join(src, "node_modules", "pkg", "x.js"), "junk")
	writeFile(t, filepath.Join(src, ".env"), "SECRET=1")

	out, err := run(t, "import", src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"initialized vrs repository",
		"imported 2 file(s) from " + src,
		"snapshot #1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("bootstrap import missing %q:\n%s", want, out)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "index.html")); string(b) != "<html/>" {
		t.Fatal("index.html not imported")
	}
	for _, p := range []string{"node_modules/pkg/x.js", ".env"} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Fatalf("%s should be ignore-filtered", p)
		}
	}
	// The imported state is a real snapshot with provenance.
	logOut, _ := run(t, "log")
	if !strings.Contains(logOut, "import from "+src) {
		t.Fatalf("log missing import message:\n%s", logOut)
	}

	// Nothing changed: no capture, no snapshot.
	out, err = run(t, "import", src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to import") {
		t.Fatalf("no-op import: %s", out)
	}

	// Hot-fix flow: the source changes, import picks it up, and the
	// pre-import state is recoverable via goto.
	writeFile(t, filepath.Join(src, "index.html"), "<html>hotfix</html>")
	out, err = run(t, "import", src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "imported 1 file(s)") || !strings.Contains(out, "previous state captured as #2") {
		t.Fatalf("hotfix import:\n%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "index.html")); string(b) != "<html>hotfix</html>" {
		t.Fatal("hotfix not applied")
	}
	// Recover the pre-import state from the capture.
	if _, err := run(t, "goto", "@2"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "index.html")); string(b) != "<html/>" {
		t.Fatalf("pre-import state not recoverable: %q", b)
	}

	// --prune: local tracked files absent from the source go to trash.
	writeFile(t, "keep.txt", "keep\n")
	out, err = run(t, "import", "--prune", src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 local file(s)") || !strings.Contains(out, "--prune") {
		t.Fatalf("prune import: %s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, "keep.txt")); !os.IsNotExist(err) {
		t.Fatal("keep.txt not pruned")
	}
	trashed, _ := filepath.Glob(filepath.Join(".vrs", "trash", "*", "keep.txt"))
	if len(trashed) != 1 {
		t.Fatalf("keep.txt not in trash: %v", trashed)
	}

	// Import takes no @ref.
	if _, err := run(t, "import", src, "@2"); err == nil || !strings.Contains(err.Error(), "no @ref") {
		t.Fatalf("import @ref: %v", err)
	}
}

// Progress goes to stderr: stdout keeps the summary line, stderr carries
// the scanning heartbeat and per-file transfer lines.
func TestTransferProgress(t *testing.T) {
	t.Chdir(t.TempDir())
	srcDir := filepath.Join("..", "progress-src")
	if err := os.MkdirAll(filepath.Join(srcDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "index.html"), []byte("<html/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errB bytes.Buffer
	if err := Run([]string{"import", srcDir}, &out, &errB); err != nil {
		t.Fatal(err)
	}
	stderr := errB.String()
	for _, want := range []string{
		"scanning " + srcDir,
		"transferring 2 file(s) from " + srcDir,
		"  [1/2] assets/app.js · 14 B",
		"  [2/2] index.html · 7 B",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("import stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(out.String(), "[1/2]") {
		t.Errorf("progress leaked into stdout: %q", out.String())
	}

	// Fresh export target: per-file lines for every written file.
	var out2, errB2 bytes.Buffer
	if err := Run([]string{"export", filepath.Join("..", "progress-dst")}, &out2, &errB2); err != nil {
		t.Fatal(err)
	}
	if s := errB2.String(); !strings.Contains(s, "exporting #1 to") || !strings.Contains(s, "[3/3] index.html · 7 B") {
		t.Errorf("export stderr:\n%s", s)
	}
	if strings.Contains(out2.String(), "[1/3]") {
		t.Errorf("progress leaked into export stdout: %q", out2.String())
	}
}
