package snap

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/justinkadima/vrs/internal/ignore"
)

func writeTestFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func entryPaths(res *CaptureResult) []string {
	out := make([]string, len(res.Entries))
	for i, e := range res.Entries {
		out[i] = e.Path
	}
	return out
}

func TestCaptureWalksIgnoresAndCache(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.txt", "alpha")
	writeTestFile(t, dir, "sub/b.txt", "beta")
	writeTestFile(t, dir, "node_modules/x.js", "junk")
	writeTestFile(t, dir, "skipme.txt", "no")
	writeTestFile(t, dir, ".env", "SECRET=1")
	writeTestFile(t, dir, "build", "a regular file named build")
	if err := os.WriteFile(filepath.Join(dir, ".vrsignore"), []byte("skipme.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ig, err := ignore.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	polyHex, err := RandomPolynomialHex()
	if err != nil {
		t.Fatal(err)
	}
	pol, err := ParsePolynomial(polyHex)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Capture(dir, pol, nil, ig)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".vrsignore", "a.txt", "build", "sub/b.txt"}
	if !equal(entryPaths(res), want) {
		t.Fatalf("entries: want %v, got %v", want, entryPaths(res))
	}
	if len(res.Versions) != 4 {
		t.Fatalf("want 4 versions (all files hashed), got %d", len(res.Versions))
	}

	// Fast path: feeding the capture back as the cache re-hashes nothing.
	cache := make(map[string]CacheEntry)
	for _, e := range res.Entries {
		cache[e.Path] = CacheEntry{MtimeNS: e.MtimeNS, Size: e.Size, Hash: e.Hash, Mode: e.Mode}
	}
	res2, err := Capture(dir, pol, cache, ig)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Versions) != 0 {
		t.Fatalf("fast path failed: %d versions re-hashed", len(res2.Versions))
	}
	if !equal(entryPaths(res2), want) {
		t.Fatal("fast path changed entries")
	}

	// A modified file (size change → cache miss) yields exactly one version.
	writeTestFile(t, dir, "a.txt", "alpha-changed")
	res3, err := Capture(dir, pol, cache, ig)
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Versions) != 1 || res3.Versions[0].FileHash == cache["a.txt"].Hash {
		t.Fatalf("modified file not detected: %+v", res3.Versions)
	}

	// Deleted files simply disappear from the new capture.
	if err := os.Remove(filepath.Join(dir, "sub", "b.txt")); err != nil {
		t.Fatal(err)
	}
	res4, err := Capture(dir, pol, cache, ig)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(entryPaths(res4), []string{".vrsignore", "a.txt", "build"}) {
		t.Fatalf("deletion not reflected: %v", entryPaths(res4))
	}
}

func TestIgnoreNegationReincludesBuiltin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".vrsignore"), []byte("!node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ig, err := ignore.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ig.Match("node_modules/x.js", false) {
		t.Fatal("negation should re-include node_modules")
	}
	if !ig.Match("target/x", false) {
		t.Fatal("other builtins must still apply")
	}
}

func TestSummarize(t *testing.T) {
	prev := map[string]Entry{
		"a.txt": {Hash: "old", Size: 1, Mode: 0o644},
		"c.txt": {Hash: "gone", Size: 1, Mode: 0o644},
	}
	entries := []Entry{
		{Path: "a.txt", Hash: "new", Mode: 0o644},
		{Path: "b.txt", Hash: "b", Mode: 0o644},
	}
	added, modified, deleted := Summarize(entries, prev)
	if added != 1 || modified != 1 || deleted != 1 {
		t.Fatalf("want 1/1/1, got %d/%d/%d", added, modified, deleted)
	}
	if got := ChangedPaths(entries, prev); len(got) != 2 {
		t.Fatalf("changed paths: %v", got)
	}
	// Mode-only change counts as modified.
	entries[0].Hash = "old" // content now matches prev
	entries[0].Mode = 0o600
	_, modified, _ = Summarize(entries, prev)
	if modified != 1 {
		t.Fatalf("mode change not detected: %d", modified)
	}
	// First snapshot: everything added.
	a, m, d := Summarize(entries, nil)
	if a != 2 || m != 0 || d != 0 {
		t.Fatalf("first snapshot summary: %d/%d/%d", a, m, d)
	}
}

func equal(a, b []string) bool {
	a2 := append([]string{}, a...)
	sort.Strings(a2)
	b2 := append([]string{}, b...)
	sort.Strings(b2)
	if len(a2) != len(b2) {
		return false
	}
	for i := range a2 {
		if a2[i] != b2[i] {
			return false
		}
	}
	return true
}
