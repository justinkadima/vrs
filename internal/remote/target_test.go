package remote

import "testing"

func TestParseTarget(t *testing.T) {
	ssh := func(user, host, path string, port int, arg string) Target {
		return Target{Kind: "ssh", User: user, Host: host, Path: path, Port: port, Arg: arg}
	}
	good := []struct {
		in   string
		want Target
	}{
		// scp form: no port slot (unambiguous grammar)
		{"server:/var/www", ssh("", "server", "/var/www", 0, "server:/var/www")},
		{"user@server:/srv/app", ssh("user", "server", "/srv/app", 0, "user@server:/srv/app")},
		{"deploy@web.example:/x/y", ssh("deploy", "web.example", "/x/y", 0, "deploy@web.example:/x/y")},
		// URI form: the only way to give a port
		{"ssh://host/var", ssh("", "host", "/var", 0, "ssh://host/var")},
		{"ssh://user@host:2222/var/www", ssh("user", "host", "/var/www", 2222, "ssh://user@host:2222/var/www")},
		{"ssh://host:22/x", ssh("", "host", "/x", 22, "ssh://host:22/x")},
		{"ssh://alias:9000/deep/path", ssh("", "alias", "/deep/path", 9000, "ssh://alias:9000/deep/path")},
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

	// Local targets.
	if got, err := ParseTarget("/tmp/x"); err != nil || got.Kind != "local" || got.Path != "/tmp/x" {
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
		// URI form errors
		"ssh://host",            // no path
		"ssh://host:0/x",        // port 0
		"ssh://host:99999/x",    // port out of range
		"ssh://host:notaport/x", // port not a number
		"ssh:///var",            // no host
		"ssh://:22/var",         // empty host with port
	}
	for _, in := range bad {
		if got, err := ParseTarget(in); err == nil {
			t.Fatalf("ParseTarget(%q) should fail, got %+v", in, got)
		}
	}
}

func TestTargetString(t *testing.T) {
	// String() echoes what the user wrote — provenance in messages stays
	// faithful to the input form.
	for _, in := range []string{"ssh://user@host:2222/var", "user@host:/var", "host:/var"} {
		tgt, err := ParseTarget(in)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", in, err)
		}
		if tgt.String() != in {
			t.Fatalf("String() = %q, want %q", tgt.String(), in)
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
