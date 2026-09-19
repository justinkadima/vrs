package remote

import (
	"testing"
)

func TestParseTarget(t *testing.T) {
	ssh := func(user, host, path string) Target {
		return Target{Kind: "ssh", User: user, Host: host, Path: path}
	}
	good := []struct {
		in   string
		want Target
	}{
		{"server:/var/www", ssh("", "server", "/var/www")},
		{"user@server:/srv/app", ssh("user", "server", "/srv/app")},
		{"deploy@web.example:/x/y", ssh("deploy", "web.example", "/x/y")},
		{"host.example.com:/", ssh("", "host.example.com", "/")},
	}
	for _, c := range good {
		got, err := ParseTarget(c.in)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseTarget(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}

	if got, err := ParseTarget("/tmp/x"); err != nil || got.Kind != "local" {
		t.Fatalf("local absolute: %+v %v", got, err)
	}
	if got, err := ParseTarget("../rel"); err != nil || got.Kind != "local" {
		t.Fatalf("local relative: %+v %v", got, err)
	}
	if got, err := ParseTarget("./sub:x"); err != nil || got.Kind != "local" {
		t.Fatalf("colon after slash is local: %+v %v", got, err)
	}

	bad := []string{
		"",
		"host:no-leading-slash",
		":/path",
		"@:/path",
		"user@:no-host:/x",
	}
	for _, in := range bad {
		if got, err := ParseTarget(in); err == nil {
			t.Fatalf("ParseTarget(%q) should fail, got %+v", in, got)
		}
	}
}

func TestReserved(t *testing.T) {
	for _, p := range []string{ManifestName, TrashDirName, TrashDirName + "/whatever"} {
		if !Reserved(p) {
			t.Errorf("Reserved(%q) = false", p)
		}
	}
	for _, p := range []string{"a.txt", "sub/" + TrashDirName, "vrs" + TrashDirName, ".vrsignore"} {
		if Reserved(p) {
			t.Errorf("Reserved(%q) = true", p)
		}
	}
}
