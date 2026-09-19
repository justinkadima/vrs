// Package e2e runs scenario tests against the built vrs binary: exit codes,
// real process boundaries, and the performance targets from PLAN.md.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/justinkadima/vrs/internal/perf"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "vrs-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin = filepath.Join(dir, "vrs")
	out, err := exec.Command("go", "build", "-o", bin, "../cmd/vrs").CombinedOutput()
	if err != nil {
		os.RemoveAll(dir)
		fmt.Fprintf(os.Stderr, "building vrs: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// run executes the binary in dir and returns combined output and the exit code.
func run(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run vrs %v: %v", args, err)
	}
	return string(out), code
}

func TestExitCodes(t *testing.T) {
	dir := t.TempDir()

	out, code := run(t, dir, "version")
	if code != 0 || !strings.HasPrefix(out, "vrs ") {
		t.Fatalf("version: %q code=%d", out, code)
	}
	if _, code = run(t, dir, "help"); code != 0 {
		t.Fatalf("help exit code %d", code)
	}
	_, code = run(t, dir, "frobnicate")
	if code != 2 {
		t.Fatalf("unknown command: want exit 2, got %d", code)
	}
	_, code = run(t, dir, "log")
	if code != 1 {
		t.Fatalf("log outside repo: want exit 1, got %d", code)
	}
}

func TestBinaryNarrative(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, code := run(t, dir, "save", "base")
	if code != 0 || !strings.Contains(out, "initialized") || !strings.Contains(out, "#1") {
		t.Fatalf("first save: %q code=%d", out, code)
	}
	write("a.txt", "one\n")
	write("keep.txt", "keep\n")
	if out, code = run(t, dir, "save"); code != 0 || !strings.Contains(out, "#2") {
		t.Fatalf("second save: %q code=%d", out, code)
	}

	write("a.txt", "two\n")
	write("b.txt", "new\n")
	os.Remove(filepath.Join(dir, "keep.txt"))

	out, code = run(t, dir, "diff")
	if code != 0 || !strings.Contains(out, "modified  a.txt") {
		t.Fatalf("diff: %q code=%d", out, code)
	}
	out, code = run(t, dir, "diff", "a.txt")
	if code != 0 || !strings.Contains(out, "-one") || !strings.Contains(out, "+two") {
		t.Fatalf("file diff: %q code=%d", out, code)
	}

	out, code = run(t, dir, "undo")
	if code != 0 || !strings.Contains(out, "restored to #2") {
		t.Fatalf("undo: %q code=%d", out, code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\n" {
		t.Fatalf("a.txt after undo: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("b.txt not trashed")
	}

	out, code = run(t, dir, "redo")
	if code != 0 || !strings.Contains(out, "redone") {
		t.Fatalf("redo: %q code=%d", out, code)
	}

	out, code = run(t, dir, "goto", "@1")
	if code != 0 || !strings.Contains(out, "working copy now at #1") {
		t.Fatalf("goto: %q code=%d", out, code)
	}
	out, code = run(t, dir, "log", "--all")
	if code != 0 || !strings.Contains(out, "[capture]") {
		t.Fatalf("log --all: %q code=%d", out, code)
	}
}

// TestPerf10k measures the PLAN.md performance targets against the real
// binary on a 10k-file fixture: clean save <300ms, clean diff <300ms,
// 100-file-change save <2s. Asserts a 5× margin (CI noise), logs the rest.
func TestPerf10k(t *testing.T) {
	if testing.Short() {
		t.Skip("perf measurement skipped in -short mode")
	}
	dir := t.TempDir()
	if err := perf.GenRepo(dir, perf.Spec{Files: 10000, Seed: 42}); err != nil {
		t.Fatal(err)
	}

	timed := func(args ...string) (string, time.Duration) {
		start := time.Now()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("vrs %v: %v\n%s", args, err, out)
		}
		return string(out), time.Since(start)
	}

	// Cold save: hash + chunk + compress + first DB write of everything.
	out, cold := timed("save", "cold")
	if !strings.Contains(out, "#1 saved") {
		t.Fatalf("cold save output: %s", out)
	}
	t.Logf("cold save (10k files, first snapshot): %s", cold)

	scenario := func(name string, target time.Duration, runs []time.Duration) {
		sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
		p50 := median(runs)
		status := "ok"
		if p50 > 5*target {
			status = "REGRESSION"
		}
		t.Logf("%-22s p50 %-9v min %-9v max %-9v (target <%v, margin 5×)  %s",
			name, p50, runs[0], runs[len(runs)-1], target, status)
		if p50 > 5*target {
			t.Errorf("%s: p50 %v exceeds 5× target %v", name, p50, 5*target)
		}
	}

	// Clean save (must be refused — no writes) and clean diff.
	var runs []time.Duration
	for i := 0; i < 7; i++ {
		out, d := timed("save")
		if i > 0 && !strings.Contains(out, "nothing to save") {
			t.Fatalf("clean save not refused: %s", out)
		}
		runs = append(runs, d)
	}
	scenario("clean save", 300*time.Millisecond, runs[1:]) // drop warm-up

	runs = runs[:0]
	for i := 0; i < 7; i++ {
		_, d := timed("diff")
		runs = append(runs, d)
	}
	scenario("clean diff", 300*time.Millisecond, runs[1:])

	// 100-file-change saves: each iteration mutates a fresh batch of 100.
	runs = runs[:0]
	for i := 0; i < 6; i++ {
		if err := perf.Mutate(dir, 100, int64(1000+i)); err != nil {
			t.Fatal(err)
		}
		out, d := timed("save", "-m", "batch")
		if i > 0 && !strings.Contains(out, "100 modified") {
			t.Fatalf("batch save output: %s", out)
		}
		runs = append(runs, d)
	}
	scenario("save, 100 changed", 2*time.Second, runs[1:])
}

func median(runs []time.Duration) time.Duration {
	s := append([]time.Duration{}, runs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}
