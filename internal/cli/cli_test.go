package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errB bytes.Buffer
	err := Run(args, &out, &errB)
	return out.String() + errB.String(), err
}

func writeFile(t *testing.T, rel, content string) {
	t.Helper()
	if err := os.WriteFile(rel, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSaveLogLifecycle(t *testing.T) {
	t.Chdir(t.TempDir())

	// First save auto-initializes (empty tree is fine).
	out, err := run(t, "save", "first")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "initialized") || !strings.Contains(out, "#1") {
		t.Fatalf("unexpected first save output: %q", out)
	}

	writeFile(t, "a.txt", "hello")
	out, err = run(t, "save")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 added") {
		t.Fatalf("unexpected second save output: %q", out)
	}

	writeFile(t, "a.txt", "hello world")
	out, err = run(t, "save", "-m", "edit a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 modified") {
		t.Fatalf("unexpected third save output: %q", out)
	}

	// Clean tree: refuses empty save.
	out, err = run(t, "save")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to save") {
		t.Fatalf("empty save not refused: %q", out)
	}

	// --force saves anyway.
	out, err = run(t, "save", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#4") {
		t.Fatalf("forced save output: %q", out)
	}

	// Log shows the timeline; -n limits it.
	out, err = run(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "edit a") || !strings.Contains(out, "#4") {
		t.Fatalf("log output: %q", out)
	}
	out, err = run(t, "log", "-n", "2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "#") != 2 {
		t.Fatalf("log -n 2 output: %q", out)
	}

	if _, err := os.Stat(".vrs/vrs.db"); err != nil {
		t.Fatalf("no database: %v", err)
	}
}

func TestDiffUndoRedoCycle(t *testing.T) {
	t.Chdir(t.TempDir())

	if out, err := run(t, "save", "base"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(out, "#1") {
		t.Fatalf("base: %q", out)
	}
	writeFile(t, "a.txt", "one\n")
	writeFile(t, "keep.txt", "keep\n")
	if out, err := run(t, "save"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(out, "#2") {
		t.Fatalf("second: %q", out)
	}

	// modify, add, delete
	writeFile(t, "a.txt", "two\n")
	writeFile(t, "b.txt", "brand new\n")
	if err := os.Remove("keep.txt"); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "diff")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"modified  a.txt", "added     b.txt", "deleted   keep.txt"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q in:\n%s", want, out)
		}
	}

	out, err = run(t, "diff", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--- #2:a.txt", "-one", "+two"} {
		if !strings.Contains(out, want) {
			t.Fatalf("file diff missing %q:\n%s", want, out)
		}
	}

	// undo restores the whole scope to #2
	out, err = run(t, "undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "restored to #2") {
		t.Fatalf("undo: %s", out)
	}
	if read(t, "a.txt") != "one\n" {
		t.Fatalf("a.txt after undo: %q", read(t, "a.txt"))
	}
	if read(t, "keep.txt") != "keep\n" {
		t.Fatal("keep.txt not recreated by undo")
	}
	if _, err := os.Stat("b.txt"); !os.IsNotExist(err) {
		t.Fatal("b.txt not trashed by undo")
	}
	trashed, _ := filepath.Glob(".vrs/trash/*/b.txt")
	if len(trashed) != 1 {
		t.Fatalf("b.txt not in trash: %v", trashed)
	}

	out, err = run(t, "diff")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no changes — matches #2") {
		t.Fatalf("post-undo diff: %s", out)
	}

	out, err = run(t, "log", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[capture]") {
		t.Fatalf("log --all: %s", out)
	}

	// redo re-applies the undone changes
	out, err = run(t, "redo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "redone") {
		t.Fatalf("redo: %s", out)
	}
	if read(t, "a.txt") != "two\n" {
		t.Fatal("redo did not reapply a.txt")
	}
	if read(t, "b.txt") != "brand new\n" {
		t.Fatal("redo did not restore b.txt")
	}
	if _, err := os.Stat("keep.txt"); !os.IsNotExist(err) {
		t.Fatal("redo should re-delete keep.txt")
	}

	// save clears the redo stack (#1 base, #2, #3 capture, #4 settle)
	out, err = run(t, "save", "-m", "settle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#4") {
		t.Fatalf("settle: %s", out)
	}
	out, err = run(t, "redo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to redo") {
		t.Fatalf("redo after save: %s", out)
	}

	// invalidation: edits after undo make redo refuse; --force overrides
	writeFile(t, "a.txt", "three\n")
	out, err = run(t, "undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "restored to #4") {
		t.Fatalf("undo 2: %s", out)
	}
	if read(t, "a.txt") != "two\n" {
		t.Fatalf("undo 2 result: %q", read(t, "a.txt"))
	}
	writeFile(t, "a.txt", "four\n")
	out, err = run(t, "redo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "refusing to redo") {
		t.Fatalf("invalidation: %s", out)
	}
	out, err = run(t, "redo", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--force") {
		t.Fatalf("force: %s", out)
	}
	if read(t, "a.txt") != "three\n" {
		t.Fatalf("force redo result: %q", read(t, "a.txt"))
	}

	// undo again returns to #4, then the tree is clean
	out, err = run(t, "undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "restored to #4") {
		t.Fatalf("undo 3: %s", out)
	}
	if read(t, "a.txt") != "two\n" {
		t.Fatalf("undo 3 result: %q", read(t, "a.txt"))
	}
	out, err = run(t, "undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to undo") {
		t.Fatalf("clean undo: %s", out)
	}
}

func TestUndoScopeIsCwdSubtree(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := run(t, "save", "base"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, "x.txt", "one\n")
	if err := os.MkdirAll("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, "sub/y.txt", "one\n")
	if _, err := run(t, "save"); err != nil {
		t.Fatal(err)
	}

	writeFile(t, "x.txt", "changed\n")
	writeFile(t, "sub/y.txt", "changed\n")

	// From inside sub/: only the subtree is undone.
	if err := os.Chdir("sub"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sub") {
		t.Fatalf("scope not mentioned: %s", out)
	}
	if err := os.Chdir(".."); err != nil {
		t.Fatal(err)
	}
	if read(t, "x.txt") != "changed\n" {
		t.Fatal("undo leaked outside the scope")
	}
	if read(t, "sub/y.txt") != "one\n" {
		t.Fatal("subtree not restored")
	}
}

func TestLogOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := run(t, "log")
	if err == nil || !strings.Contains(err.Error(), "not a vrs repository") {
		t.Fatalf("want not-a-repo error, got %v", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	_, err := run(t, "frobnicate")
	if err != ErrUsage {
		t.Fatalf("want ErrUsage, got %v", err)
	}
}

func TestMessageFromPositional(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := run(t, "save", "bug fixed"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "bug fixed") {
		t.Fatalf("positional message lost: %q", out)
	}
}

func TestGotoAndRefs(t *testing.T) {
	t.Chdir(t.TempDir())

	run2 := func(args ...string) string {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("vrs %v: %v\n%s", args, err, out)
		}
		return out
	}

	// #1 a.txt=one; #2 a.txt=two; #3 adds b.txt.
	writeFile(t, "a.txt", "one\n")
	run2("save", "base")
	writeFile(t, "a.txt", "two\n")
	run2("save", "edit")
	writeFile(t, "b.txt", "beta\n")
	run2("save", "add b")

	// goto @1: full-tree materialize, capture #4 first.
	out := run2("goto", "@1")
	if !strings.Contains(out, "working copy now at #1") || !strings.Contains(out, "captured as #4") {
		t.Fatalf("goto: %s", out)
	}
	if read(t, "a.txt") != "one\n" {
		t.Fatalf("goto did not restore a.txt: %q", read(t, "a.txt"))
	}
	if _, err := os.Stat("b.txt"); !os.IsNotExist(err) {
		t.Fatal("b.txt should be trashed by goto")
	}

	// Position semantics: diff/undo compare against #1, not the tip #3.
	out = run2("diff")
	if !strings.Contains(out, "no changes — matches #1") {
		t.Fatalf("diff after goto: %s", out)
	}
	out = run2("undo")
	if !strings.Contains(out, "nothing to undo — working tree matches #1") {
		t.Fatalf("undo after goto: %s", out)
	}

	// log shows the position when behind the tip.
	out = run2("log")
	if !strings.Contains(out, "you are at #1") || !strings.Contains(out, "the tip is #3") {
		t.Fatalf("log position line: %s", out)
	}

	// Saving from a past position forks a new line (#5, parent = #1).
	writeFile(t, "a.txt", "three\n")
	out = run2("save", "-m", "fork")
	if !strings.Contains(out, "#5 saved") || !strings.Contains(out, "starts a new line from #1 (the tip was #3)") {
		t.Fatalf("fork save: %s", out)
	}
	out = run2("log")
	if strings.Contains(out, "you are at") {
		t.Fatalf("position line after fork save should be gone: %s", out)
	}

	// diff @1 compares the position (#5 state, no b.txt — goto trashed it)
	// against snapshot #1.
	out = run2("diff", "@1")
	if !strings.Contains(out, "modified  a.txt") {
		t.Fatalf("diff @1: %s", out)
	}
	// Per-file diff against an @ref.
	out = run2("diff", "a.txt", "@1")
	for _, want := range []string{"--- #1:a.txt", "-one", "+three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diff a.txt @1 missing %q:\n%s", want, out)
		}
	}

	// undo @2: restore from an older snapshot, then redo re-applies.
	writeFile(t, "a.txt", "four\n")
	out = run2("undo", "@2")
	if !strings.Contains(out, "restored to #2") {
		t.Fatalf("undo @2: %s", out)
	}
	if read(t, "a.txt") != "two\n" {
		t.Fatalf("undo @2 result: %q", read(t, "a.txt"))
	}
	out = run2("redo")
	if !strings.Contains(out, "redone") {
		t.Fatalf("redo after undo @2: %s", out)
	}
	if read(t, "a.txt") != "four\n" {
		t.Fatalf("redo result: %q", read(t, "a.txt"))
	}

	// Relative ref: saves are #1 #2 #3 #5 → @-1 = #3.
	out = run2("goto", "@-1")
	if !strings.Contains(out, "working copy now at #3") {
		t.Fatalf("goto @-1: %s", out)
	}
	if read(t, "a.txt") != "two\n" || read(t, "b.txt") != "beta\n" {
		t.Fatal("goto @-1 did not restore the #3 state")
	}

	// No-argument goto returns to the tip (#5 = the fork save, a.txt=three;
	// the "four" state lives on in capture #6, nothing lost).
	out = run2("goto")
	if !strings.Contains(out, "working copy now at #5") {
		t.Fatalf("goto to tip: %s", out)
	}
	if read(t, "a.txt") != "three\n" {
		t.Fatalf("goto to tip result: %q", read(t, "a.txt"))
	}

	// Idempotent goto: already at the tip, no capture.
	out = run2("goto", "@5")
	if !strings.Contains(out, "working copy already at #5") {
		t.Fatalf("noop goto: %s", out)
	}

	// goto cleared the redo stack (mutation invalidates redo).
	out = run2("redo")
	if !strings.Contains(out, "nothing to redo") {
		t.Fatalf("redo after goto: %s", out)
	}

	// Errors: unknown id, out-of-range relative, nothing that old.
	if _, err := run(t, "goto", "@99"); err == nil || !strings.Contains(err.Error(), "no snapshot #99") {
		t.Fatalf("goto @99: %v", err)
	}
	if _, err := run(t, "goto", "@-9"); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("goto @-9: %v", err)
	}
	if _, err := run(t, "goto", "@2h"); err == nil || !strings.Contains(err.Error(), "that old") {
		t.Fatalf("goto @2h: %v", err)
	}
	if _, err := run(t, "diff", "a.txt", "@bogus"); err == nil || !strings.Contains(err.Error(), "bad snapshot reference") {
		t.Fatalf("diff @bogus: %v", err)
	}
}
