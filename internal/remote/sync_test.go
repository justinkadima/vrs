package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/justinkadima/vrs/internal/snap"
)

type fakeSource map[string][]byte

func (f fakeSource) ReadVersion(hash string) ([]byte, error) {
	b, ok := f[hash]
	if !ok {
		return nil, fmt.Errorf("no such version %s", hash)
	}
	return b, nil
}

func hashOf(content string) string {
	// The engine compares manifest hashes as opaque strings; use readable fakes.
	return "hash:" + content
}

func writeLocal(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readLocal(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func entriesOf(files map[string]string) map[string]snap.Entry {
	m := map[string]snap.Entry{}
	for p, content := range files {
		m[p] = snap.Entry{Path: p, Hash: hashOf(content), Size: int64(len(content)), Mode: 0o644}
	}
	return m
}

func TestExportTreeLocal(t *testing.T) {
	dst := t.TempDir()
	src := fakeSource{}
	files := map[string]string{"a.txt": "alpha", "sub/b.txt": "beta"}
	for _, c := range files {
		src[hashOf(c)] = []byte(c)
	}

	res, err := ExportTree(entriesOf(files), src, NewLocalFs(), dst, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 2 || res.Skipped != 0 {
		t.Fatalf("first export: %+v", res)
	}
	if readLocal(t, dst, "a.txt") != "alpha" || readLocal(t, dst, "sub/b.txt") != "beta" {
		t.Fatal("content missing after export")
	}
	if _, err := os.Stat(filepath.Join(dst, ManifestName)); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}

	// Idempotent: everything now matches the manifest.
	res, err = ExportTree(entriesOf(files), src, NewLocalFs(), dst, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 || res.Skipped != 2 {
		t.Fatalf("second export: %+v", res)
	}

	// Mode-only change between snapshots: content identical, mode differs.
	if err := os.Chmod(filepath.Join(dst, "a.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	files2 := entriesOf(files)
	e2 := files2["a.txt"]
	e2.Mode = 0o600
	files2["a.txt"] = e2
	res, err = ExportTree(files2, src, NewLocalFs(), dst, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ModeOnly != 1 || res.Skipped != 1 || len(res.Written) != 0 {
		t.Fatalf("mode-only export: %+v", res)
	}
	if fi, err := os.Stat(filepath.Join(dst, "a.txt")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("target mode not fixed: %v %v", fi.Mode().Perm(), err)
	}

	// --prune moves target extras to the target trash.
	writeLocal(t, dst, "extra.txt", "extra", 0o644)
	res, err = ExportTree(entriesOf(files), src, NewLocalFs(), dst, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pruned) != 1 || res.Pruned[0] != "extra.txt" {
		t.Fatalf("prune: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dst, "extra.txt")); !os.IsNotExist(err) {
		t.Fatal("extra.txt not pruned")
	}
	trashed, _ := filepath.Glob(filepath.Join(dst, TrashDirName, "*", "extra.txt"))
	if len(trashed) != 1 {
		t.Fatalf("extra.txt not in trash: %v", trashed)
	}
}

func TestImportPlanApplyLocal(t *testing.T) {
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	// A source tree with ignored noise, a vrs repo dir, and reserved names.
	writeLocal(t, srcRoot, "index.html", "<html/>", 0o644)
	writeLocal(t, srcRoot, "assets/app.js", "console.log(1)", 0o644)
	writeLocal(t, srcRoot, "node_modules/pkg/index.js", "junk", 0o644)
	writeLocal(t, srcRoot, ".env", "SECRET=1", 0o644)
	writeLocal(t, srcRoot, ".vrs/vrs.db", "sqlite", 0o644)
	writeLocal(t, srcRoot, ManifestName, "{}", 0o644)
	writeLocal(t, srcRoot, TrashDirName+"/old/x", "old", 0o644)
	// A symlink must be skipped like the capture walk skips it.
	if err := os.Symlink("index.html", filepath.Join(srcRoot, "link.html")); err != nil {
		t.Fatal(err)
	}

	ig := &staticIgnore{ignored: map[string]bool{"node_modules/": true, ".env": true}}

	plan, err := PlanImport(NewLocalFs(), srcRoot, NewLocalFs(), dstRoot, ig, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 2 {
		t.Fatalf("plan: %+v", plan.Files)
	}
	for _, f := range plan.Files {
		if f.Rel != "index.html" && f.Rel != "assets/app.js" {
			t.Fatalf("unexpected planned file: %+v", f)
		}
	}

	if err := ApplyImport(plan, NewLocalFs(), srcRoot, NewLocalFs(), dstRoot, filepath.Join(t.TempDir(), "trash"), nil); err != nil {
		t.Fatal(err)
	}
	if readLocal(t, dstRoot, "index.html") != "<html/>" {
		t.Fatal("index.html not imported")
	}
	if readLocal(t, dstRoot, "assets/app.js") != "console.log(1)" {
		t.Fatal("app.js not imported")
	}
	for _, p := range []string{"node_modules/pkg/index.js", ".env", ".vrs/vrs.db", ManifestName, TrashDirName + "/old/x", "link.html"} {
		if _, err := os.Stat(filepath.Join(dstRoot, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Fatalf("%s should not be imported", p)
		}
	}

	// Quick-check: a re-plan with no source changes downloads nothing.
	plan, err = PlanImport(NewLocalFs(), srcRoot, NewLocalFs(), dstRoot, ig, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 0 {
		t.Fatalf("quick-check failed: %+v", plan.Files)
	}

	// A changed file (size differs) is planned again; --prune lists the
	// local-only tracked file.
	writeLocal(t, dstRoot, "keep.txt", "keep", 0o644)
	writeLocal(t, srcRoot, "index.html", "<html>changed</html>", 0o644)
	plan, err = PlanImport(NewLocalFs(), srcRoot, NewLocalFs(), dstRoot, ig, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Rel != "index.html" {
		t.Fatalf("changed plan: %+v", plan.Files)
	}
	if len(plan.Pruned) != 1 || plan.Pruned[0] != "keep.txt" {
		t.Fatalf("prune plan: %+v", plan.Pruned)
	}

	trashBase := filepath.Join(dstRoot, ".vrs", "trash", "1")
	if err := ApplyImport(plan, NewLocalFs(), srcRoot, NewLocalFs(), dstRoot, trashBase, nil); err != nil {
		t.Fatal(err)
	}
	if readLocal(t, dstRoot, "index.html") != "<html>changed</html>" {
		t.Fatal("changed file not re-imported")
	}
	if _, err := os.Stat(filepath.Join(dstRoot, "keep.txt")); !os.IsNotExist(err) {
		t.Fatal("keep.txt not pruned")
	}
	if _, err := os.Stat(filepath.Join(trashBase, "keep.txt")); err != nil {
		t.Fatalf("keep.txt not in trash: %v", err)
	}
}

// staticIgnore is a tiny Ignore for tests (the real matcher is covered by
// the ignore package's own tests).
type staticIgnore struct {
	ignored map[string]bool
}

func (s *staticIgnore) Match(rel string, isDir bool) bool {
	if isDir {
		return s.ignored[rel+"/"]
	}
	return s.ignored[rel]
}

func TestLoadManifestCorrupt(t *testing.T) {
	dst := t.TempDir()
	writeLocal(t, dst, ManifestName, "not json", 0o644)
	m, err := loadManifest(NewLocalFs(), dst)
	if err != nil || len(m.Files) != 0 {
		t.Fatalf("corrupt manifest: %+v %v", m, err)
	}
}

func TestProgressCallbacks(t *testing.T) {
	dstRoot := t.TempDir()

	files := map[string]string{"a.txt": "one", "b/c.txt": "two", "d.txt": "three"}
	src := fakeSource{}
	for _, c := range files {
		src[hashOf(c)] = []byte(c)
	}

	type pcall struct {
		done, total int
		path        string
		size        int64
	}
	var calls []pcall
	rec := func(done, total int, path string, size int64) {
		calls = append(calls, pcall{done, total, path, size})
	}

	if _, err := ExportTree(entriesOf(files), src, NewLocalFs(), dstRoot, false, rec); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0].done != 1 || calls[0].total != 3 || calls[2].done != 3 {
		t.Fatalf("export progress: %+v", calls)
	}
	if calls[0].size != int64(len("one")) {
		t.Fatalf("export progress size: %+v", calls[0])
	}

	// Idempotent re-export: nothing transferred, no calls.
	calls = nil
	if _, err := ExportTree(entriesOf(files), src, NewLocalFs(), dstRoot, false, rec); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("re-export should not report transfers: %+v", calls)
	}

	// Import scan: one call per candidate file, in walk order.
	var scans []string
	scanRec := func(n int, path string) { scans = append(scans, fmt.Sprintf("%d %s", n, path)) }
	plan, err := PlanImport(NewLocalFs(), filepath.ToSlash(dstRoot), NewLocalFs(), t.TempDir(), &staticIgnore{}, false, scanRec)
	if err != nil {
		t.Fatal(err)
	}
	if len(scans) != len(plan.Files) {
		t.Fatalf("scan calls %d, plan files %d", len(scans), len(plan.Files))
	}

	// Apply: done goes 1..N with the total.
	calls = nil
	if err := ApplyImport(plan, NewLocalFs(), filepath.ToSlash(dstRoot), NewLocalFs(), t.TempDir(), filepath.Join(t.TempDir(), "trash"), rec); err != nil {
		t.Fatal(err)
	}
	if len(calls) != len(plan.Files) || calls[0].done != 1 || calls[len(calls)-1].done != len(plan.Files) || calls[0].total != len(plan.Files) {
		t.Fatalf("apply progress: %+v", calls)
	}
}

// Repo config never ships; nested same-name files are user content and do.
// A target's own .vrsignore (e.g. left by an older vrs) is never touched,
// including by --prune.
func TestRepoConfigNotShipped(t *testing.T) {
	files := map[string]string{".vrsignore": "rules", "sub/.vrsignore": "nested", "a.txt": "one"}
	src := fakeSource{}
	for _, c := range files {
		src[hashOf(c)] = []byte(c)
	}
	dstRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(dstRoot, ".vrsignore"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ExportTree(entriesOf(files), src, NewLocalFs(), dstRoot, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 2 || res.Skipped != 0 {
		t.Fatalf("written: %+v", res)
	}
	if readLocal(t, dstRoot, "sub/.vrsignore") != "nested" {
		t.Fatal("nested .vrsignore should ship (user content)")
	}
	if readLocal(t, dstRoot, ".vrsignore") != "stale" {
		t.Fatal("target's own .vrsignore must be untouched")
	}

	res, err = ExportTree(entriesOf(files), src, NewLocalFs(), dstRoot, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Pruned {
		if p == ".vrsignore" {
			t.Fatalf("prune touched the target's .vrsignore: %+v", res.Pruned)
		}
	}
}
