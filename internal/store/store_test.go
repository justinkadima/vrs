package store

import (
	"bytes"
	"fmt"
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
	pe, err := s.SnapshotEntries(info3.ID)
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

func TestSaveVsCaptureSemantics(t *testing.T) {
	s := newTestStore(t)
	e, v := versionOf(t, s, "a.txt", "hello")
	res := &snap.CaptureResult{Entries: []snap.Entry{e}, Versions: []snap.Version{v}}
	info1, err := s.Save(res, "first", "save", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Stack is LIFO.
	if err := s.RedoPush(1, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RedoPush(2, 2, ""); err != nil {
		t.Fatal(err)
	}
	top, err := s.RedoPeek()
	if err != nil || top == nil || top.Target != 2 {
		t.Fatalf("peek: %v %v", top, err)
	}

	// Captures: no base advance, no redo clear, hidden from log.
	capInfo, err := s.Save(&snap.CaptureResult{Entries: []snap.Entry{e}}, "", "capture", info1.ID)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := s.Base()
	if base != info1.ID {
		t.Fatalf("capture advanced base: %d", base)
	}
	if top, _ = s.RedoPeek(); top == nil {
		t.Fatal("capture cleared the redo stack")
	}
	rows, _ := s.ListSnapshots(false, 0)
	for _, r := range rows {
		if r.ID == capInfo.ID {
			t.Fatal("capture visible in default log")
		}
	}
	all, _ := s.ListSnapshots(true, 0)
	if len(all) != 2 {
		t.Fatalf("log --all should show capture: %+v", all)
	}

	// Saves: advance base and clear the redo stack.
	info3, err := s.Save(&snap.CaptureResult{Entries: []snap.Entry{e}}, "second", "save", base)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Base(); b != info3.ID {
		t.Fatalf("save did not advance base: %d", b)
	}
	if top, _ = s.RedoPeek(); top != nil {
		t.Fatal("save did not clear the redo stack")
	}

	// Pop semantics.
	if err := s.RedoPush(10, 11, "x"); err != nil {
		t.Fatal(err)
	}
	entry, _ := s.RedoPeek()
	if err := s.RedoDelete(entry.ID); err != nil {
		t.Fatal(err)
	}
	if top, _ = s.RedoPeek(); top != nil {
		t.Fatal("redo delete failed")
	}
}

func TestReadVersionRoundtrip(t *testing.T) {
	s := newTestStore(t)

	var b bytes.Buffer
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&b, "line %04d of a somewhat compressible payload\n", i)
	}
	content := b.String() // ~140 KiB → multiple chunks

	e, v := versionOf(t, s, "big.txt", content)
	if len(v.Chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(v.Chunks))
	}
	if _, err := s.Save(&snap.CaptureResult{Entries: []snap.Entry{e}, Versions: []snap.Version{v}}, "big", "save", 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadVersion(e.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(content)) {
		t.Fatalf("roundtrip mismatch: %d bytes", len(got))
	}

	// Unknown version errors.
	if _, err := s.ReadVersion("nope"); err == nil {
		t.Fatal("expected error for unknown version")
	}

	// Empty file: zero chunks, empty read.
	hexStr, _ := s.Meta("chunker_poly")
	pol, _ := snap.ParsePolynomial(hexStr)
	emptyHash, emptyChunks, err := snap.HashBytes(nil, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyChunks) != 0 {
		t.Fatalf("empty file should chunk to nothing, got %d", len(emptyChunks))
	}
	ee := snap.Entry{Path: "empty.txt", Hash: emptyHash, Size: 0, Mode: 0o644, MtimeNS: 1}
	ev := snap.Version{FileHash: emptyHash, Size: 0}
	if _, err := s.Save(&snap.CaptureResult{Entries: []snap.Entry{ee}, Versions: []snap.Version{ev}}, "empty", "save", 1); err != nil {
		t.Fatal(err)
	}
	if got, err = s.ReadVersion(emptyHash); err != nil || len(got) != 0 {
		t.Fatalf("empty read: %d bytes, err %v", len(got), err)
	}
}
