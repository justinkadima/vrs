package remote

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"
)

// SSHConfig holds resolved connection parameters for an SSH target.
type SSHConfig struct {
	User           string
	Addr           string   // host:port
	IdentityFiles  []string // explicit keys from ssh_config (may be empty)
	KnownHosts     string   // known_hosts file
	IdentitiesOnly bool     // offer only IdentityFiles (agent keys used just to supply those identities)

	// PassphrasePrompt, when set, is asked for the passphrase of an
	// encrypted identity file the agent doesn't already hold. Nil means
	// encrypted keys are skipped (scripts, tests).
	PassphrasePrompt func(path string) (string, error)
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
	var idents []string
	identitiesOnly := false

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
			if vs, err := cfg.GetAll(host, "identityfile"); err == nil {
				for _, v := range vs {
					v = expandTilde(v)
					if _, err := os.Stat(v); err == nil {
						idents = append(idents, v)
					}
				}
			}
			if v, _ := cfg.Get(host, "identitiesonly"); strings.EqualFold(strings.TrimSpace(v), "yes") {
				identitiesOnly = true
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
		User:           user,
		Addr:           net.JoinHostPort(host, strconv.Itoa(port)),
		IdentityFiles:  idents,
		KnownHosts:     knownHostsPath,
		IdentitiesOnly: identitiesOnly,
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
// ~/.ssh/config, keys and known_hosts. Encrypted keys the agent doesn't
// hold prompt for a passphrase when run on a terminal.
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
	if cfg.PassphrasePrompt == nil {
		cfg.PassphrasePrompt = TerminalPassphrasePrompt
	}
	return Connect(cfg)
}

// TerminalPassphrasePrompt reads a key passphrase from the terminal with
// echo disabled. It refuses when stdin is not a TTY (scripts, MCP) —
// no silent hangs.
func TerminalPassphrasePrompt(path string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("stdin is not a terminal — load the key with ssh-add instead")
	}
	fmt.Fprintf(os.Stderr, "Enter passphrase for %s: ", path)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(pw), nil
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

	var agentSigners []ssh.Signer
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			ag := agent.NewClient(conn)
			if s, err := ag.Signers(); err == nil {
				agentSigners = s
			}
			conn.Close()
		}
	}
	signers, err := authSigners(cfg, agentSigners)
	if err != nil {
		return nil, err
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

// authSigners assembles the keys offered for client authentication,
// mirroring OpenSSH semantics:
//
//   - agent keys are offered unless IdentitiesOnly is set;
//   - identity files are parsed (multiple IdentityFile lines supported);
//     for each, the agent's copy of the same key is preferred when present,
//     otherwise the file is decrypted, prompting up to three times;
//   - keys are deduplicated by public key.
//
// Encrypted keys that can't be obtained are collected so the caller can
// explain exactly what's missing.
func authSigners(cfg SSHConfig, agentSigners []ssh.Signer) ([]ssh.Signer, error) {
	var signers []ssh.Signer
	have := map[string]bool{}
	add := func(s ssh.Signer) {
		if k := string(s.PublicKey().Marshal()); !have[k] {
			have[k] = true
			signers = append(signers, s)
		}
	}

	agentByKey := map[string]ssh.Signer{}
	for _, s := range agentSigners {
		agentByKey[string(s.PublicKey().Marshal())] = s
	}
	if !cfg.IdentitiesOnly {
		for _, s := range agentSigners {
			add(s)
		}
	}

	identityFiles := cfg.IdentityFiles
	if len(identityFiles) == 0 {
		for _, name := range defaultIdentityFiles {
			if p, err := homePath(".ssh", name); err == nil {
				identityFiles = append(identityFiles, p)
			}
		}
	}

	var skipped []string
	for _, p := range identityFiles {
		b, err := os.ReadFile(p)
		if err != nil {
			continue // defaults may not exist; explicit config is resolved earlier
		}
		s, err := ssh.ParsePrivateKey(b)
		if err == nil {
			add(s)
			continue
		}
		var missing *ssh.PassphraseMissingError
		if !errors.As(err, &missing) {
			continue
		}
		// Encrypted: prefer the agent's copy of the same key (no prompt),
		// like OpenSSH with an agent holding the identity.
		if missing.PublicKey != nil {
			if as, ok := agentByKey[string(missing.PublicKey.Marshal())]; ok {
				add(as)
				continue
			}
		}
		if cfg.PassphrasePrompt == nil {
			skipped = append(skipped, p)
			continue
		}
		if s, err = decryptIdentity(p, b, cfg.PassphrasePrompt); err == nil {
			add(s)
		} else {
			skipped = append(skipped, p)
		}
	}

	if len(signers) == 0 {
		if len(skipped) > 0 {
			return nil, fmt.Errorf("no usable ssh credentials — could not use encrypted key(s) %s; load with `ssh-add` or enter the passphrase", strings.Join(skipped, ", "))
		}
		return nil, fmt.Errorf("no usable ssh credentials — add a key to the agent or ~/.ssh/id_ed25519 (password auth is not supported)")
	}
	return signers, nil
}

// decryptIdentity asks for the passphrase up to three times (a wrong one
// is a typo, not a decision).
func decryptIdentity(path string, b []byte, prompt func(string) (string, error)) (ssh.Signer, error) {
	var err error
	for try := 0; try < 3; try++ {
		pw, perr := prompt(path)
		if perr != nil {
			return nil, perr
		}
		var s ssh.Signer
		s, err = ssh.ParsePrivateKeyWithPassphrase(b, []byte(pw))
		if err == nil {
			return s, nil
		}
	}
	return nil, err
}
