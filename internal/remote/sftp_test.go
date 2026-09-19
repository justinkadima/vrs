package remote

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
)

// TestSftpFsOverPipe exercises the SftpFs adapter against a real in-process
// SFTP server (pkg/sftp's own server over a pipe) — no SSH, no network.
func TestSftpFsOverPipe(t *testing.T) {
	c1, c2 := net.Pipe()
	srv, err := sftp.NewServer(c2)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	client, err := sftp.NewClientPipe(c1, c1)
	if err != nil {
		t.Fatal(err)
	}
	fs := NewSftpFs(client)
	defer func() { client.Close(); srv.Close() }()

	root := t.TempDir()
	p := filepath.ToSlash(filepath.Join(root, "a", "b.txt"))

	if err := fs.WriteFile(p, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	fi, err := fs.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size != 5 || fi.Mode != 0o640 || !fi.Regular {
		t.Fatalf("stat: %+v", fi)
	}
	if err := fs.SetMtime(p, 1700000000000000000); err != nil {
		t.Fatal(err)
	}
	fi, _ = fs.Stat(p)
	if fi.Mtime != 1700000000000000000 {
		t.Fatalf("mtime not preserved: %d", fi.Mtime)
	}
	b, err := fs.ReadFile(p)
	if err != nil || string(b) != "hello" {
		t.Fatalf("read: %q %v", b, err)
	}

	// Overwrite via PosixRename, and MkdirAll for deep dirs.
	deep := filepath.ToSlash(filepath.Join(root, "x", "y", "z", "d.txt"))
	if err := fs.WriteFile(deep, []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile(deep, []byte("deeper"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := fs.ReadFile(deep); string(b) != "deeper" {
		t.Fatalf("overwrite: %q", b)
	}

	fis, err := fs.ReadDir(filepath.ToSlash(root))
	if err != nil || len(fis) != 2 { // a/ and x/ (tmp files renamed away)
		t.Fatalf("readdir: %+v %v", fis, err)
	}

	// Not-exist detection drives manifest handling.
	_, err = fs.Stat(filepath.ToSlash(filepath.Join(root, "nope")))
	if !IsNotExist(err) {
		t.Fatalf("want not-exist, got %v", err)
	}
}
