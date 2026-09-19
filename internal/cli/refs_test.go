package cli

import (
	"testing"
	"time"
)

func TestParseRef(t *testing.T) {
	good := []struct {
		in   string
		kind string
		n    int64
		dur  time.Duration
	}{
		{"@3", "id", 3, 0},
		{"@0", "id", 0, 0},
		{"@999999", "id", 999999, 0},
		{"@-0", "back", 0, 0},
		{"@-1", "back", 1, 0},
		{"@-42", "back", 42, 0},
		{"@2h", "time", 0, 2 * time.Hour},
		{"@3d", "time", 0, 72 * time.Hour},
		{"@45s", "time", 0, 45 * time.Second},
		{"@30m", "time", 0, 30 * time.Minute},
		{"@1w", "time", 0, 7 * 24 * time.Hour},
		{"@1h30m", "time", 0, 90 * time.Minute},
	}
	for _, c := range good {
		spec, err := parseRef(c.in)
		if err != nil {
			t.Fatalf("parseRef(%q): %v", c.in, err)
		}
		if spec.kind != c.kind || spec.n != c.n || spec.dur != c.dur {
			t.Fatalf("parseRef(%q) = %+v, want kind=%s n=%d dur=%s", c.in, spec, c.kind, c.n, c.dur)
		}
	}

	bad := []string{
		"", "3", "abc", "@", "@-", "@x", "@-x", "@h", "@2x", "@2h30", "@1h30m5", "@--3", "@1.5h",
	}
	for _, in := range bad {
		if spec, err := parseRef(in); err == nil {
			t.Fatalf("parseRef(%q) should fail, got %+v", in, spec)
		}
	}
}

func TestScanTargetArgs(t *testing.T) {
	path, ref, err := scanTargetArgs([]string{"a.txt"})
	if err != nil || path != "a.txt" || ref != nil {
		t.Fatalf("path only: %q %v %v", path, ref, err)
	}
	path, ref, err = scanTargetArgs([]string{"@3"})
	if err != nil || path != "" || ref == nil || ref.kind != "id" {
		t.Fatalf("ref only: %q %v %v", path, ref, err)
	}
	path, ref, err = scanTargetArgs([]string{"a.txt", "@3"})
	if err != nil || path != "a.txt" || ref == nil {
		t.Fatalf("path then ref: %q %v %v", path, ref, err)
	}
	path, ref, err = scanTargetArgs([]string{"@3", "a.txt"})
	if err != nil || path != "a.txt" || ref == nil {
		t.Fatalf("ref then path: %q %v %v", path, ref, err)
	}
	if _, _, err = scanTargetArgs([]string{"@3", "@4"}); err == nil {
		t.Fatal("two refs should fail")
	}
	if _, _, err = scanTargetArgs([]string{"a.txt", "b.txt"}); err == nil {
		t.Fatal("two paths should fail")
	}
	if _, _, err = scanTargetArgs([]string{"@bogus"}); err == nil {
		t.Fatal("bad ref should fail")
	}
}
