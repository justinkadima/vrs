package remote

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"os"
	"strings"

	"golang.org/x/crypto/ssh/knownhosts"
)

// defaultHostKeyAlgos tails the preference list: the non-certificate
// host key algorithms x/crypto supports, in its usual order. A server
// offering none of the recorded types still negotiates one of these —
// and verification then legitimately fails (or matches) as before.
var defaultHostKeyAlgos = []string{
	"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
	"rsa-sha2-256", "rsa-sha2-512", "ssh-rsa", "ssh-ed25519",
}

// algoForKnownHostType maps a known_hosts key-type field to the client
// host key algorithms that present the same key. known_hosts matching is
// on the key blob, so any signature scheme for a recorded key verifies.
var algoForKnownHostType = map[string][]string{
	"ssh-ed25519":                        {"ssh-ed25519"},
	"ecdsa-sha2-nistp256":                {"ecdsa-sha2-nistp256"},
	"ecdsa-sha2-nistp384":                {"ecdsa-sha2-nistp384"},
	"ecdsa-sha2-nistp521":                {"ecdsa-sha2-nistp521"},
	"sk-ssh-ed25519@openssh.com":         {"sk-ssh-ed25519@openssh.com"},
	"sk-ecdsa-sha2-nistp256@openssh.com": {"sk-ecdsa-sha2-nistp256@openssh.com"},
	"ssh-rsa":                            {"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa"},
	"rsa-sha2-256":                       {"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa"},
	"rsa-sha2-512":                       {"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa"},
}

// preferredHostKeyAlgos orders HostKeyAlgorithms by the key types
// already recorded in known_hosts for addr — the behavior OpenSSH
// clients have (they order host key preference by existing entries).
//
// Without this, a server offering several host key types (ed25519 +
// ECDSA + RSA is common) can be asked for a type with no recorded
// entry, and verification fails with "knownhosts: key mismatch" even
// though the host is properly known. Verification itself stays strict:
// this only changes WHICH of the server's keys gets negotiated.
func preferredHostKeyAlgos(addr, knownHostsPath string) []string {
	lookup := knownhosts.Normalize(addr)
	var algos []string
	seen := map[string]bool{}
	add := func(list []string) {
		for _, a := range list {
			if !seen[a] {
				seen[a] = true
				algos = append(algos, a)
			}
		}
	}

	f, err := os.Open(knownHostsPath)
	if err != nil {
		return nil // let the strict callback report unknown/missing entries
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Markers (@cert-authority, @revoked) don't set plain-host
		// preferences.
		if strings.HasPrefix(line, "@") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if knownHostPatternMatch(fields[0], lookup) {
			add(algoForKnownHostType[fields[1]])
		}
	}
	add(defaultHostKeyAlgos)
	return algos
}

// knownHostPatternMatch reports whether a comma-separated host pattern
// list matches the lookup name. Patterns may use '*' and '?', be
// negated with '!' (negation excludes the whole line), or be hashed
// ('|1|salt|hash' — OpenSSH's HMAC-SHA1 form).
func knownHostPatternMatch(patterns, lookup string) bool {
	matched := false
	for _, pat := range strings.Split(patterns, ",") {
		negated := strings.HasPrefix(pat, "!")
		pat = strings.TrimPrefix(pat, "!")
		if pat == "" {
			continue
		}
		if hostnameMatches(pat, lookup) {
			if negated {
				return false
			}
			matched = true
		}
	}
	return matched
}

func hostnameMatches(pat, lookup string) bool {
	if strings.HasPrefix(pat, "|1|") {
		return hashedHostnameMatch(pat, lookup)
	}
	// An explicit "[host]:22" pattern also matches default-port lookups
	// (which Normalize renders as plain "host").
	if lookup != "" && !strings.HasPrefix(lookup, "[") &&
		strings.HasPrefix(pat, "[") && strings.HasSuffix(pat, "]:22") {
		if globMatch(pat[1:len(pat)-4], lookup) {
			return true
		}
	}
	return globMatch(pat, lookup)
}

func hashedHostnameMatch(pat, hostname string) bool {
	parts := strings.Split(pat, "|") // ["", "1", salt, hash]
	if len(parts) != 4 || parts[1] != "1" {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(hostname))
	return hmac.Equal(mac.Sum(nil), want)
}

// globMatch reports whether s matches pat, honoring '*' (any run) and
// '?' (any single char). No other semantics — no bracket classes.
func globMatch(pat, s string) bool {
	px, sx := 0, 0
	star, mark := -1, -1
	for sx < len(s) {
		if px < len(pat) && (pat[px] == '?' || pat[px] == s[sx]) {
			px++
			sx++
		} else if px < len(pat) && pat[px] == '*' {
			star = px
			mark = sx
			px++
		} else if star >= 0 {
			px = star + 1
			mark++
			sx = mark
		} else {
			return false
		}
	}
	for px < len(pat) && pat[px] == '*' {
		px++
	}
	return px == len(pat)
}
