package remote

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kevinburke/ssh_config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHConfig holds resolved connection parameters for an SSH target.
type SSHConfig struct {
	User         string
	Addr         string // host:port
	IdentityFile string // optional explicit key
	KnownHosts   string // known_hosts file
}

// defaultIdentityFiles are tried when no IdentityFile is configured.
var defaultIdentityFiles = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

func homePath(rel ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, rel...)...), nil
}

// loadSSHConfig reads and decodes an ssh_config file.
func loadSSHConfig(path string) (*ssh_config.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh_config.Decode(bytes.NewReader(b))
}

// ResolveSSH expands an ssh_config alias into concrete connection
// parameters. An explicit port (URI form) overrides the config's Port; a
// missing config file falls back to defaults; an empty cfgPath skips alias
// resolution entirely (tests).
func ResolveSSH(user, host string, port int, cfgPath, knownHostsPath string) (SSHConfig, error) {
	cfgPort := 22
	ident := ""

	if cfgPath != "" {
		if cfg, err := loadSSHConfig(cfgPath); err == nil {
			// Resolve everything against the original alias; the hostname
			// applies last — matching is pattern-based, not identity-based.
			if user == "" {
				if v, _ := cfg.Get(host, "user"); v != "" {
					user = v
				}
			}
			if v, _ := cfg.Get(host, "port"); v != "" {
				if p, err := strconv.Atoi(v); err == nil {
					cfgPort = p
				}
			}
			if v, _ := cfg.Get(host, "identityfile"); v != "" {
				v = expandTilde(v)
				if _, err := os.Stat(v); err == nil {
					ident = v
				}
			}
			if v, _ := cfg.Get(host, "hostname"); v != "" {
				host = v
			}
		}
	}
	if port <= 0 {
		port = cfgPort
	}

	if knownHostsPath == "" {
		p, err := homePath(".ssh", "known_hosts")
		if err != nil {
			return SSHConfig{}, err
		}
		knownHostsPath = p
	}
	return SSHConfig{
		User:         user,
		Addr:         net.JoinHostPort(host, strconv.Itoa(port)),
		IdentityFile: ident,
		KnownHosts:   knownHostsPath,
	}, nil
}

func expandTilde(p string) string {
	if len(p) >= 2 && p[0] == '~' && (p[1] == '/' || p[1] == '\\') {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Dial opens an SFTP filesystem for an SSH target, resolving the user's
// ~/.ssh/config, keys and known_hosts.
func Dial(t Target) (*SftpFs, error) {
	return DialWith(t, "", "")
}

// DialWith is Dial with injectable ssh_config and known_hosts paths
// (tests).
func DialWith(t Target, cfgPath, knownHostsPath string) (*SftpFs, error) {
	if t.Kind != "ssh" {
		return nil, fmt.Errorf("dial: not an ssh target")
	}
	if cfgPath == "" {
		p, err := homePath(".ssh", "config")
		if err != nil {
			return nil, err
		}
		cfgPath = p
	}
	cfg, err := ResolveSSH(t.User, t.Host, t.Port, cfgPath, knownHostsPath)
	if err != nil {
		return nil, err
	}
	return Connect(cfg)
}

// Connect opens an SSH+SFTP session with explicit parameters (tests use
// this directly; Dial resolves the user's configuration first).
func Connect(cfg SSHConfig) (*SftpFs, error) {
	if cfg.User == "" {
		return nil, fmt.Errorf("no user for ssh target — set it in ~/.ssh/config or use user@host:/path")
	}
	hostKeyCb, err := knownhosts.New(cfg.KnownHosts)
	if err != nil {
		return nil, fmt.Errorf("known_hosts: %w", err)
	}

	var signers []ssh.Signer
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			ag := agent.NewClient(conn)
			if s, err := ag.Signers(); err == nil {
				signers = append(signers, s...)
			}
			conn.Close()
		}
	}
	identityFiles := []string{cfg.IdentityFile}
	if cfg.IdentityFile == "" {
		for _, name := range defaultIdentityFiles {
			p, err := homePath(".ssh", name)
			if err != nil {
				continue
			}
			identityFiles = append(identityFiles, p)
		}
	}
	for _, p := range identityFiles {
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue // defaults may not exist; explicit config is resolved earlier
		}
		if s, err := ssh.ParsePrivateKey(b); err == nil {
			signers = append(signers, s)
		}
		// Passphrase-protected keys are skipped: v1 does not prompt.
	}
	if len(signers) == 0 {
		return nil, fmt.Errorf("no usable ssh credentials — add a key to the agent or ~/.ssh/id_ed25519 (password auth is not supported)")
	}

	conn, err := ssh.Dial("tcp", cfg.Addr, &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback: hostKeyCb,
		// Prefer host key types already recorded in known_hosts — without
		// this, servers offering several key types can present one with no
		// entry and fail with "key mismatch" even though the host is known.
		HostKeyAlgorithms: preferredHostKeyAlgos(cfg.Addr, cfg.KnownHosts),
		Timeout:           15 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("ssh %s: %w", cfg.Addr, err)
	}
	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("sftp: %w", err)
	}
	return newSftpFsWithConn(sftpClient, conn), nil
}
