package cli

import (
	"os"
	"strings"
	"testing"
)

func TestCapture(t *testing.T) {
	t.Chdir(t.TempDir())

	// Outside a repository: plain error.
	_, err := run(t, "capture")
	if err == nil || !strings.Contains(err.Error(), "not a vrs repository") {
		t.Fatalf("want not-a-repo error, got %v", err)
	}

	if _, err := run(t, "save", "base"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, "a.txt", "one\n")
	if _, err := run(t, "save"); err != nil {
		t.Fatal(err)
	}

	// Clean tree: idempotent — nothing stored, existing id returned.
	out, err := run(t, "capture")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unchanged — state already recorded as #2") {
		t.Fatalf("clean capture: %q", out)
	}
	if out2, _ := run(t, "log", "--all"); strings.Count(out2, "#") != 2 {
		t.Fatalf("clean capture stored something:\n%s", out2)
	}

	// Dirty tree: hidden checkpoint, tagged, base untouched.
	writeFile(t, "a.txt", "two\n")
	out, err = run(t, "capture", "-t", "mid")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "checkpoint #3") || !strings.Contains(out, "1 file(s) changed") || !strings.Contains(out, "tag: mid") {
		t.Fatalf("capture: %q", out)
	}
	if out, _ = run(t, "log"); strings.Contains(out, "mid") {
		t.Fatalf("checkpoint leaked into default log:\n%s", out)
	}
	if out, _ = run(t, "log", "--all"); !strings.Contains(out, "mid") || !strings.Contains(out, "[capture]") {
		t.Fatalf("log --all missing checkpoint:\n%s", out)
	}

	// Base still #2: diff reports the edit against the position, and a
	// repeated capture is deduplicated against the newest snapshot.
	if out, _ = run(t, "diff"); !strings.Contains(out, "modified  a.txt") {
		t.Fatalf("diff after capture: %s", out)
	}
	if out, _ = run(t, "capture"); !strings.Contains(out, "unchanged — state already recorded as #3") {
		t.Fatalf("repeated capture not deduplicated: %q", out)
	}

	// Captures never invalidate pending redos (unlike mutations).
	writeFile(t, "a.txt", "three\n")
	if out, _ = run(t, "undo"); !strings.Contains(out, "restored to #2") {
		t.Fatalf("undo: %s", out)
	}
	if out, _ = run(t, "capture"); !strings.Contains(out, "unchanged — state already recorded as #2") {
		t.Fatalf("capture after undo: %q", out)
	}
	if out, err = run(t, "redo"); err != nil || !strings.Contains(out, "redone") {
		t.Fatalf("capture cleared the redo stack: %s (%v)", out, err)
	}
	if b, _ := os.ReadFile("a.txt"); string(b) != "three\n" {
		t.Fatalf("redo result: %q", b)
	}
}
