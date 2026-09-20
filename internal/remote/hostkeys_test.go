package remote

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

func writeKnownHosts(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPreferredHostKeyAlgos(t *testing.T) {
	kh := writeKnownHosts(t,
		"[203.0.113.7]:2222 ssh-ed25519 AAAAkey1",
		"rsa.example.com ssh-rsa AAAAkey2",
		"*.example.com,!bad.example.com ecdsa-sha2-nistp256 AAAAkey3",
		knownhosts.HashHostname("hashed.other.org")+" ssh-rsa AAAAkey4",
		"@cert-authority *.certs.example ssh-rsa AAAAkey5",
		"# a comment",
		"   ",
	)

	// Non-default port: bracketed entry matches, its type goes first (and
	// is not duplicated in the tail).
	got := preferredHostKeyAlgos("203.0.113.7:2222", kh)
	if got[0] != "ssh-ed25519" || strings.Count(strings.Join(got, ","), "ssh-ed25519") != 1 {
		t.Fatalf("bracketed entry: %v", got)
	}
	if len(got) != len(defaultHostKeyAlgos) { // deduped against the tail
		t.Fatalf("expected deduped defaults tail, got %v", got)
	}

	// Default port normalizes to the plain name; RSA maps to all its
	// signature schemes.
	got = preferredHostKeyAlgos("rsa.example.com:22", kh)
	want := []string{"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa",
		"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "ssh-ed25519"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rsa mapping: %v", got)
	}

	// Wildcard patterns match; negation excludes.
	if got := preferredHostKeyAlgos("www.example.com:22", kh); got[0] != "ecdsa-sha2-nistp256" {
		t.Fatalf("wildcard: %v", got)
	}
	if got := preferredHostKeyAlgos("bad.example.com:22", kh); got[0] != defaultHostKeyAlgos[0] {
		t.Fatalf("negated host fell back: %v", got)
	}

	// Hashed hostnames (HashKnownHosts yes) still set preference.
	if got := preferredHostKeyAlgos("hashed.other.org:22", kh); got[0] != "rsa-sha2-512" {
		t.Fatalf("hashed entry: %v", got)
	}

	// Plain entries don't match non-default ports (OpenSSH semantics).
	if got := preferredHostKeyAlgos("rsa.example.com:2222", kh); got[0] != defaultHostKeyAlgos[0] {
		t.Fatalf("plain entry matched other port: %v", got)
	}

	// Unknown host: pure defaults, unchanged behavior.
	if got := preferredHostKeyAlgos("other.example.org:22", kh); !reflect.DeepEqual(got, defaultHostKeyAlgos) {
		t.Fatalf("unknown host: %v", got)
	}

	// Missing file: nil (Connect's default ordering applies).
	if got := preferredHostKeyAlgos("x:22", filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Fatalf("missing file: %v", got)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pat, s string
		want   bool
	}{
		{"host", "host", true},
		{"host", "other", false},
		{"*.example.com", "www.example.com", true},
		{"*.example.com", "example.com", false},
		{"?eb", "web", true},
		{"?eb", "wweb", false},
		{"[h]ost", "host", false}, // no bracket classes, literal
		{"*", "anything", true},
		{"", "", true},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pat, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pat, c.s, got, c.want)
		}
	}
}
