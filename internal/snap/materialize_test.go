package snap

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/justinkadima/vrs/internal/ignore"
)

type fakeSrc map[string][]byte

func (f fakeSrc) ReadVersion(h string) ([]byte, error) {
	b, ok := f[h]
	if !ok {
		return nil, fmt.Errorf("no such version %s", h)
	}
	return b, nil
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMaterialize(t *testing.T) {
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

	target := map[string]Entry{}
	src := fakeSrc{}
	for path, content := range map[string]string{
		"a.txt":     "v1",
		"sub/b.txt": "keep",
		"gone.txt":  "gone",
	} {
		h, _, err := HashBytes([]byte(content), pol)
		if err != nil {
			t.Fatal(err)
		}
		target[path] = Entry{Path: path, Hash: h, Size: int64(len(content)), Mode: 0o644}
		src[h] = []byte(content)
	}

	// Disk: a modified, b same, extra added, gone missing.
	writeTestFile(t, dir, "a.txt", "v2")
	writeTestFile(t, dir, "sub/b.txt", "keep")
	writeTestFile(t, dir, "extra.txt", "extra")

	trashBase := filepath.Join(dir, ".vrs", "trash", "1")
	mat, err := Materialize(dir, "", target, nil, src, trashBase, pol, ig)
	if err != nil {
		t.Fatal(err)
	}

	if got := read(t, dir, "a.txt"); got != "v1" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := read(t, dir, "gone.txt"); got != "gone" {
		t.Fatalf("gone.txt = %q", got)
	}
	if got := read(t, dir, "sub/b.txt"); got != "keep" {
		t.Fatalf("sub/b.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "extra.txt")); !os.IsNotExist(err) {
		t.Fatal("extra.txt still on disk")
	}
	if _, err := os.Stat(filepath.Join(trashBase, "extra.txt")); err != nil {
		t.Fatalf("extra.txt not in trash: %v", err)
	}
	if len(mat.Written) != 2 || len(mat.Trashed) != 1 || mat.Trashed[0] != "extra.txt" {
		t.Fatalf("written=%v trashed=%v", mat.Written, mat.Trashed)
	}
	for _, e := range mat.Written {
		if e.MtimeNS == 0 {
			t.Fatal("no mtime recorded for written entry")
		}
	}

	// Idempotent: with the updated cache nothing is touched.
	cache := map[string]CacheEntry{}
	for _, e := range mat.Written {
		cache[e.Path] = CacheEntry{MtimeNS: e.MtimeNS, Size: e.Size, Hash: e.Hash, Mode: e.Mode}
	}
	fi, err := os.Stat(filepath.Join(dir, "sub", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cache["sub/b.txt"] = CacheEntry{
		MtimeNS: fi.ModTime().UnixNano(), Size: fi.Size(),
		Mode: uint32(fi.Mode().Perm()), Hash: target["sub/b.txt"].Hash,
	}

	trash2 := filepath.Join(dir, ".vrs", "trash", "2")
	mat2, err := Materialize(dir, "", target, cache, src, trash2, pol, ig)
	if err != nil {
		t.Fatal(err)
	}
	if len(mat2.Written) != 0 || len(mat2.Trashed) != 0 {
		t.Fatalf("not idempotent: written=%v trashed=%v", mat2.Written, mat2.Trashed)
	}

	// Scope narrowing: materializing only sub/ leaves a divergent a.txt alone.
	writeTestFile(t, dir, "a.txt", "v3")
	trash3 := filepath.Join(dir, ".vrs", "trash", "3")
	mat3, err := Materialize(dir, "sub", target, cache, src, trash3, pol, ig)
	if err != nil {
		t.Fatal(err)
	}
	if len(mat3.Written) != 0 || len(mat3.Trashed) != 0 {
		t.Fatalf("scope leaked: written=%v trashed=%v", mat3.Written, mat3.Trashed)
	}
	if got := read(t, dir, "a.txt"); got != "v3" {
		t.Fatalf("scope leak: a.txt = %q", got)
	}
}
