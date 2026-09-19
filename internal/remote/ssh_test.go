package remote

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// startSSHServer runs an in-process SSH server that accepts exactly one
// public key and serves the SFTP subsystem over the real filesystem.
// It returns the address, a known_hosts file trusting the server, and the
// client identity file vrs should present.
func startSSHServer(t *testing.T) (addr, knownHosts, clientKeyPath string) {
	t.Helper()
	dir := t.TempDir()

	hostPub, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = hostPub
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}

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
	cfg.AddHostKey(hostSigner)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(conn, cfg)
		}
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	knownHosts = filepath.Join(dir, "known_hosts")
	line := fmt.Sprintf("[%s]:%s %s", host, portStr, string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey())))
	if err := os.WriteFile(knownHosts, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return ln.Addr().String(), knownHosts, clientKeyPath
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

	got, err := ResolveSSH("", "deploy", cfgPath, "/tmp/kh")
	if err != nil {
		t.Fatal(err)
	}
	if got.User != "web" || got.Addr != "127.0.0.1:2222" || got.IdentityFile != keyPath || got.KnownHosts != "/tmp/kh" {
		t.Fatalf("alias resolution: %+v", got)
	}

	// Explicit user wins over the config; missing config falls back.
	got, err = ResolveSSH("root", "nosuchalias", "", "/tmp/kh")
	if err != nil {
		t.Fatal(err)
	}
	if got.User != "root" || got.Addr != "nosuchalias:22" || got.IdentityFile != "" {
		t.Fatalf("defaults: %+v", got)
	}
}
