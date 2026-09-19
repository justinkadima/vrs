package remote

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Target is a parsed export/import destination.
type Target struct {
	Kind string // "local" | "ssh"
	User string // ssh only
	Host string // ssh only, after alias resolution
	Port int    // ssh only; 0 = unspecified (ssh_config Port, else 22)
	Path string // absolute local path, or absolute remote path
	Arg  string // the target as the user wrote it (used in messages)
}

// String renders the target as the user wrote it.
func (t Target) String() string {
	if t.Arg != "" {
		return t.Arg
	}
	if t.Kind != "ssh" {
		return t.Path
	}
	if t.User != "" {
		return t.User + "@" + t.Host + ":" + t.Path
	}
	return t.Host + ":" + t.Path
}

// ParseTarget recognizes:
//
//	ssh://[user@]host[:port]/abs/path   (URI form — the only way to give a port)
//	[user@]host:/abs/path               (scp form)
//	local paths                          (relative or absolute, ~ expanded)
//
// Remote paths must be absolute. In the URI form an explicit port
// overrides ~/.ssh/config's Port; everything else (user, hostname alias,
// identity) still resolves through ssh_config like ssh itself.
func ParseTarget(arg string) (Target, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Target{}, fmt.Errorf("empty target")
	}

	if strings.HasPrefix(arg, "ssh://") {
		return parseURITarget(arg)
	}

	if i := strings.Index(arg, ":"); i >= 0 && !strings.ContainsAny(arg[:i], "/\\") {
		if i == 0 {
			return Target{}, fmt.Errorf("bad target %q — no host before ':'", arg)
		}
		hostpart := arg[:i]
		remotePath := arg[i+1:]
		if !strings.HasPrefix(remotePath, "/") {
			return Target{}, fmt.Errorf("remote target %q must use an absolute path (e.g. user@host:/var/www or ssh://user@host:2222/var/www)", arg)
		}
		t := Target{Kind: "ssh", Path: remotePath, Arg: arg}
		if at := strings.LastIndex(hostpart, "@"); at >= 0 {
			t.User = hostpart[:at]
			t.Host = hostpart[at+1:]
		} else {
			t.Host = hostpart
		}
		if t.Host == "" {
			return Target{}, fmt.Errorf("bad target %q — no host before ':'", arg)
		}
		return t, nil
	}

	// Local path: expand a leading ~ (flag-style shells don't do it for us).
	if strings.HasPrefix(arg, "~/") || arg == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Target{}, err
		}
		arg = filepath.Join(home, strings.TrimPrefix(arg, "~"))
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "local", Path: abs, Arg: arg}, nil
}

func parseURITarget(arg string) (Target, error) {
	u, err := url.Parse(arg)
	if err != nil {
		return Target{}, fmt.Errorf("bad ssh target %q: %w", arg, err)
	}
	if u.Host == "" {
		return Target{}, fmt.Errorf("bad ssh target %q — no host (use ssh://user@host:2222/var/www)", arg)
	}
	if u.Path == "" || u.Path == "/" {
		return Target{}, fmt.Errorf("remote target %q must include a path (ssh://user@host:2222/var/www)", arg)
	}

	t := Target{Kind: "ssh", Path: u.Path, User: u.User.Username(), Arg: arg}
	if u.Port() != "" {
		p, err := strconv.Atoi(u.Port())
		if err != nil || p <= 0 || p > 65535 {
			return Target{}, fmt.Errorf("bad port in %q — use 1–65535", arg)
		}
		t.Port = p
		t.Host = u.Hostname()
	} else {
		t.Host = u.Host // keep raw host: ssh_config aliases must still match
	}
	if t.Host == "" {
		return Target{}, fmt.Errorf("bad ssh target %q — no host", arg)
	}
	return t, nil
}
