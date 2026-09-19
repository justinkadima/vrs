package snap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/justinkadima/vrs/internal/ignore"
)

func TestStatus(t *testing.T) {
	dir := t.TempDir()
	polyHex, err := RandomPolynomialHex()
	if err != nil {
		t.Fatal(err)
	}
	pol, err := ParsePolynomial(polyHex)
	if err != nil {
		t.Fatal(err)
	}
	ig, err := ignore.Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	// The target snapshot's state.
	target := map[string]Entry{}
	for path, content := range map[string]string{
		"a.txt":     "v1",
		"same.txt":  "same",
		"gone.txt":  "gone",
		"sub/b.txt": "keep",
	} {
		h, _, err := HashBytes([]byte(content), pol)
		if err != nil {
			t.Fatal(err)
		}
		target[path] = Entry{Path: path, Hash: h, Size: int64(len(content)), Mode: 0o644}
	}

	// Disk: a modified, same unchanged, gone deleted, new added, junk ignored.
	writeTestFile(t, dir, "a.txt", "v2")
	writeTestFile(t, dir, "same.txt", "same")
	writeTestFile(t, dir, "new.txt", "new")
	writeTestFile(t, dir, "sub/b.txt", "keep")
	writeTestFile(t, dir, "node_modules/x.js", "junk")

	stats, err := Status(dir, "", target, nil, pol, ig)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, st := range stats {
		got[st.Path] = st.Status
	}
	want := map[string]string{
		"a.txt":     StatusModified,
		"same.txt":  StatusSame,
		"new.txt":   StatusAdded,
		"gone.txt":  StatusDeleted,
		"sub/b.txt": StatusSame,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p, w := range want {
		if got[p] != w {
			t.Fatalf("%s: got %q want %q (all: %v)", p, got[p], w, got)
		}
	}

	// Directory scope only reports that subtree.
	stats, err = Status(dir, "sub", target, nil, pol, ig)
	if err != nil || len(stats) != 1 || stats[0].Path != "sub/b.txt" || stats[0].Status != StatusSame {
		t.Fatalf("dir scope: %v %v", stats, err)
	}

	// Explicit file scope.
	stats, err = Status(dir, "a.txt", target, nil, pol, ig)
	if err != nil || len(stats) != 1 || stats[0].Status != StatusModified {
		t.Fatalf("file scope: %v %v", stats, err)
	}

	// A file deleted from disk that exists in the target.
	stats, err = Status(dir, "gone.txt", target, nil, pol, ig)
	if err != nil || len(stats) != 1 || stats[0].Status != StatusDeleted {
		t.Fatalf("deleted scope: %v %v", stats, err)
	}

	// A missing file that is not in the target is an error.
	if _, err = Status(dir, "nope.txt", target, nil, pol, ig); err == nil {
		t.Fatal("want error for unknown path")
	}

	// Mode-only change (chmod) is detected as modified.
	if err := os.Chmod(filepath.Join(dir, "same.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	stats, err = Status(dir, "same.txt", target, nil, pol, ig)
	if err != nil || stats[0].Status != StatusModified {
		t.Fatalf("chmod not detected: %v %v", stats, err)
	}

	// Fast path: a matching cache entry classifies without rehashing.
	if err := os.Chmod(filepath.Join(dir, "same.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "same.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cache := map[string]CacheEntry{
		"same.txt": {MtimeNS: fi.ModTime().UnixNano(), Size: fi.Size(), Mode: 0o644, Hash: target["same.txt"].Hash},
	}
	stats, err = Status(dir, "same.txt", target, cache, pol, ig)
	if err != nil || stats[0].Status != StatusSame {
		t.Fatalf("fast path: %v %v", stats, err)
	}
}
