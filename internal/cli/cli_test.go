package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errB bytes.Buffer
	err := Run(args, &out, &errB)
	return out.String() + errB.String(), err
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

	if err := os.WriteFile("a.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "save")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 added") {
		t.Fatalf("unexpected second save output: %q", out)
	}

	if err := os.WriteFile("a.txt", []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
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

	// The database exists and is small enough to inspect.
	if _, err := os.Stat(".vrs/vrs.db"); err != nil {
		t.Fatalf("no database: %v", err)
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
	out, err := run(t, "save", "bug fixed")
	if err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "bug fixed") {
		t.Fatalf("positional message lost: %q", out)
	}
}
