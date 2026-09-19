package store

import (
	"testing"

	"github.com/justinkadima/vrs/internal/snap"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	poly, err := snap.RandomPolynomialHex()
	if err != nil {
		t.Fatal(err)
	}
	s, err := InitAt(dir, poly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// versionOf builds an Entry/Version pair for content at path.
func versionOf(t *testing.T, s *Store, path, content string) (snap.Entry, snap.Version) {
	t.Helper()
	hexStr, err := s.Meta("chunker_poly")
	if err != nil {
		t.Fatal(err)
	}
	pol, err := snap.ParsePolynomial(hexStr)
	if err != nil {
		t.Fatal(err)
	}
	hash, chunks, err := snap.HashBytes([]byte(content), pol)
	if err != nil {
		t.Fatal(err)
	}
	e := snap.Entry{Path: path, Hash: hash, Size: int64(len(content)), Mode: 0o644, MtimeNS: 1}
	v := snap.Version{FileHash: hash, Size: int64(len(content)), Chunks: chunks}
	return e, v
}

func TestSaveDedupAndRefcounts(t *testing.T) {
	s := newTestStore(t)

	e1, v1 := versionOf(t, s, "a.txt", "hello world")
	res1 := &snap.CaptureResult{Entries: []snap.Entry{e1}, Versions: []snap.Version{v1}}
	info1, err := s.Save(res1, "first", "save", 0)
	if err != nil {
		t.Fatal(err)
	}
	if info1.ID != 1 {
		t.Fatalf("want snapshot #1, got #%d", info1.ID)
	}
	if info1.NewRaw != 11 {
		t.Fatalf("want 11 raw bytes stored, got %d", info1.NewRaw)
	}

	// Identical save again (fast path: no versions) — nothing new stored.
	res2 := &snap.CaptureResult{Entries: []snap.Entry{e1}}
	info2, err := s.Save(res2, "second", "save", info1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info2.ID != 2 || info2.NewRaw != 0 {
		t.Fatalf("dedup failed: %+v", info2)
	}

	// Same content at a second path: duplicate version rows are skipped,
	// chunk refcount stays at 1 (one registered version).
	e2, v2 := versionOf(t, s, "copy.txt", "hello world")
	res3 := &snap.CaptureResult{Entries: []snap.Entry{e1, e2}, Versions: []snap.Version{v1, v2}}
	info3, err := s.Save(res3, "third", "save", info2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info3.NewRaw != 0 {
		t.Fatalf("re-registering a known version stored new bytes: %+v", info3)
	}
	var rc int
	if err := s.db.QueryRow("SELECT refcount FROM chunks WHERE hash = ?", v1.Chunks[0].Hash).Scan(&rc); err != nil {
		t.Fatal(err)
	}
	if rc != 1 {
		t.Fatalf("want refcount 1, got %d", rc)
	}

	// Changed content stores exactly the new chunk.
	e3, v3 := versionOf(t, s, "a.txt", "hello world v2")
	res4 := &snap.CaptureResult{Entries: []snap.Entry{e3, e2}, Versions: []snap.Version{v3}}
	info4, err := s.Save(res4, "fourth", "save", info3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info4.NewRaw != 14 {
		t.Fatalf("want 14 raw bytes for changed file, got %d", info4.NewRaw)
	}

	// Base pointer advances to the newest snapshot.
	base, err := s.Base()
	if err != nil {
		t.Fatal(err)
	}
	if base != info4.ID {
		t.Fatalf("want base %d, got %d", info4.ID, base)
	}

	// Manifests are per-snapshot and correct.
	pe, err := s.ParentEntries(info3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pe) != 2 || pe["a.txt"].Hash != e1.Hash {
		t.Fatalf("bad manifest for #3: %+v", pe)
	}

	// Timeline lists saves newest first.
	rows, err := s.ListSnapshots(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].ID != info4.ID {
		t.Fatalf("bad timeline: %+v", rows)
	}
}

func TestListSnapshotsLimit(t *testing.T) {
	s := newTestStore(t)
	e, v := versionOf(t, s, "a.txt", "x")
	var last int64
	for i := 0; i < 3; i++ {
		info, err := s.Save(&snap.CaptureResult{Entries: []snap.Entry{e}, Versions: []snap.Version{v}}, "m", "save", last)
		if err != nil {
			t.Fatal(err)
		}
		last = info.ID
	}
	rows, err := s.ListSnapshots(false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != 3 {
		t.Fatalf("limit broken: %+v", rows)
	}
}
