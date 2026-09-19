package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Target is a parsed export/import destination.
type Target struct {
	Kind string // "local" | "ssh"
	User string // ssh only
	Host string // ssh only, after alias resolution
	Path string // absolute local path, or absolute remote path
}

// String renders the target as the user wrote it, roughly.
func (t Target) String() string {
	if t.Kind != "ssh" {
		return t.Path
	}
	if t.User != "" {
		return t.User + "@" + t.Host + ":" + t.Path
	}
	return t.Host + ":" + t.Path
}

// ParseTarget recognizes [user@]host:/abs/path (SSH/SFTP) and local
// directory paths. Remote paths must be absolute — the common scp/sftp
// forms carry no ambiguity that way.
func ParseTarget(arg string) (Target, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Target{}, fmt.Errorf("empty target")
	}

	if i := strings.Index(arg, ":"); i >= 0 && !strings.ContainsAny(arg[:i], "/\\") {
		if i == 0 {
			return Target{}, fmt.Errorf("bad target %q — no host before ':'", arg)
		}
		hostpart := arg[:i]
		remotePath := arg[i+1:]
		if !strings.HasPrefix(remotePath, "/") {
			return Target{}, fmt.Errorf("remote target %q must use an absolute path (e.g. user@host:/var/www)", arg)
		}
		t := Target{Kind: "ssh", Path: remotePath}
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
	return Target{Kind: "local", Path: abs}, nil
}
