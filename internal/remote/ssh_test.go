package remote

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// startSSHServer runs an in-process SSH server that accepts exactly one
// public key and serves the SFTP subsystem over the real filesystem.
// It returns the address, a known_hosts file trusting the server, and the
// client identity file vrs should present.
func mustEd25519(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func mustClientSigner(t *testing.T, keyPath string) ssh.Signer {
	t.Helper()
	b, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.ParsePrivateKey(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// startSSHServerKeys runs an in-process SSH server offering the given
// host keys, accepting a freshly generated client key. It returns the
// listen address and the client key path; the test writes its own
// known_hosts.
func startSSHServerKeys(t *testing.T, hostSigners ...ssh.Signer) (addr, clientKeyPath string) {
	t.Helper()
	dir := t.TempDir()

	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wantKey, err := ssh.NewPublicKey(clientPub)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	clientKeyPath = filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(clientKeyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), wantKey.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("unknown key")
		},
	}
	for _, hs := range hostSigners {
		cfg.AddHostKey(hs)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(conn, cfg)
		}
	}()
	return ln.Addr().String(), clientKeyPath
}

func startSSHServer(t *testing.T) (addr, knownHosts, clientKeyPath string) {
	t.Helper()
	hostSigner, err := ssh.NewSignerFromKey(mustEd25519(t))
	if err != nil {
		t.Fatal(err)
	}
	addr, clientKeyPath = startSSHServerKeys(t, hostSigner)

	host, portStr, _ := net.SplitHostPort(addr)
	knownHosts = filepath.Join(t.TempDir(), "known_hosts")
	line := fmt.Sprintf("[%s]:%s %s", host, portStr, string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey())))
	if err := os.WriteFile(knownHosts, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return addr, knownHosts, clientKeyPath
}

func handleSSHConn(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "sessions only")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
			for req := range reqs {
				if req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp" {
					req.Reply(true, nil)
					srv, err := sftp.NewServer(ch)
					if err == nil {
						_ = srv.Serve()
					}
					return
				}
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}(ch, chReqs)
	}
}

func TestSSHConnectAndExport(t *testing.T) {
	addr, knownHosts, keyPath := startSSHServer(t)
	remoteRoot := filepath.ToSlash(t.TempDir())

	fs, err := Connect(SSHConfig{User: "test", Addr: addr, IdentityFile: keyPath, KnownHosts: knownHosts})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer fs.Close()

	// Export a small snapshot over the wire.
	files := map[string]string{"index.html": "<html/>", "assets/app.js": "console.log(1)"}
	src := fakeSource{}
	for _, c := range files {
		src[hashOf(c)] = []byte(c)
	}
	res, err := ExportTree(entriesOf(files), src, fs, remoteRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 2 {
		t.Fatalf("ssh export: %+v", res)
	}

	// Incremental: the manifest survives the round trip.
	res, err = ExportTree(entriesOf(files), src, fs, remoteRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 || res.Skipped != 2 {
		t.Fatalf("ssh re-export not incremental: %+v", res)
	}

	// Import back over the wire into a fresh local tree (quick-check skips
	// nothing — different machines would compare stats; here mtime was never
	// set on export, but content still round-trips).
	dstRoot := t.TempDir()
	plan, err := PlanImport(fs, remoteRoot, NewLocalFs(), dstRoot, &staticIgnore{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 2 {
		t.Fatalf("ssh import plan: %+v", plan.Files)
	}
	if err := ApplyImport(plan, fs, remoteRoot, NewLocalFs(), dstRoot, filepath.Join(dstRoot, "trash")); err != nil {
		t.Fatal(err)
	}
	if readLocal(t, dstRoot, "assets/app.js") != "console.log(1)" {
		t.Fatal("ssh import lost content")
	}
}

func TestSSHConnectRejects(t *testing.T) {
	addr, knownHosts, keyPath := startSSHServer(t)

	// Unknown host key (empty known_hosts) is refused — never auto-accept.
	_, err := Connect(SSHConfig{User: "test", Addr: addr, IdentityFile: keyPath, KnownHosts: filepath.Join(t.TempDir(), "empty")})
	if err == nil || !strings.Contains(err.Error(), "known") {
		t.Fatalf("want known_hosts failure, got %v", err)
	}

	// No user → refuse before dialing.
	if _, err := Connect(SSHConfig{Addr: addr, IdentityFile: keyPath, KnownHosts: knownHosts}); err == nil {
		t.Fatal("want missing-user error")
	}

	// No credentials → refuse before dialing.
	if _, err := Connect(SSHConfig{User: "test", Addr: addr, KnownHosts: knownHosts, IdentityFile: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("want no-credentials error")
	}
}

func TestResolveSSHAlias(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "deploy_key")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config")
	cfg := fmt.Sprintf("Host deploy\n  HostName 127.0.0.1\n  User web\n  Port 2222\n  IdentityFile %s\n", keyPath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveSSH("", "deploy", 0, cfgPath, "/tmp/kh")
	if err != nil {
		t.Fatal(err)
	}
	if got.User != "web" || got.Addr != "127.0.0.1:2222" || got.IdentityFile != keyPath || got.KnownHosts != "/tmp/kh" {
		t.Fatalf("alias resolution: %+v", got)
	}

	// An explicit port (URI form) overrides the config's Port.
	got, err = ResolveSSH("", "deploy", 9000, cfgPath, "/tmp/kh")
	if err != nil {
		t.Fatal(err)
	}
	if got.Addr != "127.0.0.1:9000" {
		t.Fatalf("port override: %+v", got)
	}

	// Explicit user wins over the config; missing config falls back.
	got, err = ResolveSSH("root", "nosuchalias", 0, "", "/tmp/kh")
	if err != nil {
		t.Fatal(err)
	}
	if got.User != "root" || got.Addr != "nosuchalias:22" || got.IdentityFile != "" {
		t.Fatalf("defaults: %+v", got)
	}
}

// The full production path for a URI target: ParseTarget → ssh_config
// resolution → URI port override → known_hosts → SFTP → export.
func TestURITargetExport(t *testing.T) {
	addr, knownHosts, keyPath := startSSHServer(t)
	host, port, _ := strings.Cut(addr, ":")

	// A config with a *wrong* Port for this host: the URI port must win.
	cfgPath := filepath.Join(t.TempDir(), "config")
	cfgText := "Host " + host + "\n    Port 22\n    IdentityFile " + keyPath + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}

	tgt, err := ParseTarget("ssh://test@" + host + ":" + port + "/srv/out")
	if err != nil {
		t.Fatal(err)
	}
	fs, err := DialWith(tgt, cfgPath, knownHosts)
	if err != nil {
		t.Fatalf("dial via URI target: %v", err)
	}
	defer fs.Close()

	files := map[string]string{"index.html": "<html/>"}
	src := fakeSource{}
	for _, c := range files {
		src[hashOf(c)] = []byte(c)
	}
	res, err := ExportTree(entriesOf(files), src, fs, filepath.ToSlash(t.TempDir()), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 1 {
		t.Fatalf("uri export: %+v", res)
	}
}

// The regression test for the "knownhosts: key mismatch" trap: a server
// offering several host key types (here ECDSA + ed25519), but known_hosts
// recording only one of them (ed25519 — what OpenSSH clients normally
// write). x/crypto's default preference asks for ECDSA first; without
// reordering by known_hosts, verification fails on a properly known host.
func TestHostKeyAlgoPreference(t *testing.T) {
	dir := t.TempDir()

	edSigner, err := ssh.NewSignerFromKey(mustEd25519(t))
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecSigner, err := ssh.NewSignerFromKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}

	addr, clientKeyPath := startSSHServerKeys(t, edSigner, ecSigner)
	host, port, _ := net.SplitHostPort(addr)

	// known_hosts records ONLY the ed25519 key.
	knownHosts := filepath.Join(dir, "known_hosts")
	line := fmt.Sprintf("[%s]:%s %s", host, port, string(ssh.MarshalAuthorizedKey(edSigner.PublicKey())))
	if err := os.WriteFile(knownHosts, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	// Proof of the trap: the default algorithm order fails against a
	// properly known host.
	kh, err := knownhosts.New(knownHosts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(mustClientSigner(t, clientKeyPath))},
		HostKeyCallback: kh,
		Timeout:         5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "key mismatch") {
		t.Fatalf("default order should hit the key-mismatch trap, got %v", err)
	}

	// vrs's Connect reorders by known_hosts and connects.
	fs, err := Connect(SSHConfig{User: "test", Addr: addr, IdentityFile: clientKeyPath, KnownHosts: knownHosts})
	if err != nil {
		t.Fatalf("connect with reordered host key algos: %v", err)
	}
	defer fs.Close()
}
