package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// writeEncryptedKey writes an ed25519 key encrypted with passphrase.
func writeEncryptedKey(t *testing.T, dir, passphrase string) (path string, signer ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err = ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "id_encrypted")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, signer
}

func writePlaintextKey(t *testing.T, dir string) (path string, signer ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err = ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "id_plain")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, signer
}

func TestAuthSigners(t *testing.T) {
	dir := t.TempDir()
	plainPath, plainSigner := writePlaintextKey(t, dir)
	encPath, encSigner := writeEncryptedKey(t, dir, "secret")
	cfgOf := func(files ...string) SSHConfig {
		return SSHConfig{IdentityFiles: files}
	}
	prompt := func(path string) (string, error) { return "secret", nil }

	marshal := func(s ssh.Signer) string { return string(s.PublicKey().Marshal()) }
	contains := func(signers []ssh.Signer, want ssh.Signer) bool {
		for _, s := range signers {
			if marshal(s) == marshal(want) {
				return true
			}
		}
		return false
	}

	// Encrypted key without a prompt is skipped; plaintext loads.
	got, err := authSigners(cfgOf(plainPath, encPath), nil)
	if err != nil || len(got) != 1 || !contains(got, plainSigner) {
		t.Fatalf("no prompt: %v %v", got, err)
	}

	// With a prompt, the encrypted key decrypts.
	got, err = authSigners(SSHConfig{IdentityFiles: []string{plainPath, encPath}, PassphrasePrompt: prompt}, nil)
	if err != nil || len(got) != 2 || !contains(got, encSigner) {
		t.Fatalf("with prompt: %v %v", got, err)
	}

	// The agent's copy of the encrypted key is preferred — no prompt needed.
	got, err = authSigners(cfgOf(plainPath, encPath), []ssh.Signer{encSigner})
	if err != nil || len(got) != 2 || !contains(got, encSigner) {
		t.Fatalf("agent copy: %v %v", got, err)
	}

	// IdentitiesOnly drops unrelated agent keys but still serves identities.
	otherSigner := func() ssh.Signer {
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		s, _ := ssh.NewSignerFromKey(priv)
		return s
	}()
	got, err = authSigners(SSHConfig{IdentityFiles: []string{plainPath}, IdentitiesOnly: true},
		[]ssh.Signer{otherSigner, encSigner})
	if err != nil || len(got) != 1 || !contains(got, plainSigner) {
		t.Fatalf("identitiesonly: %v %v", got, err)
	}

	// Nothing usable: the error names the encrypted key and the fix.
	_, err = authSigners(cfgOf(encPath), nil)
	if err == nil || !strings.Contains(err.Error(), "ssh-add") {
		t.Fatalf("missing-key error: %v", err)
	}

	// Nothing usable at all (explicit identity that doesn't exist — the
	// default-identity fallback would otherwise pick up the real ~/.ssh).
	_, err = authSigners(SSHConfig{IdentityFiles: []string{filepath.Join(dir, "missing")}}, nil)
	if err == nil || !strings.Contains(err.Error(), "no usable ssh credentials") {
		t.Fatalf("empty error: %v", err)
	}
}

// End to end: an encrypted identity file connects through the in-process
// server when the prompt supplies the passphrase — including after two
// wrong attempts (a typo is not a decision).
func TestEncryptedKeyConnect(t *testing.T) {
	addr, knownHosts, clientKeyPath := startSSHServer(t)

	// Encrypt the very key the server accepts.
	raw, err := os.ReadFile(clientKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ssh.ParseRawPrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("open sesame"))
	if err != nil {
		t.Fatal(err)
	}
	encPath := filepath.Join(t.TempDir(), "id_encrypted")
	if err := os.WriteFile(encPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	attempts := 0
	prompt := func(path string) (string, error) {
		attempts++
		if attempts < 3 {
			return fmt.Sprintf("wrong-%d", attempts), nil
		}
		return "open sesame", nil
	}
	fs, err := Connect(SSHConfig{
		User: "test", Addr: addr, KnownHosts: knownHosts,
		IdentityFiles: []string{encPath}, PassphrasePrompt: prompt,
	})
	if err != nil {
		t.Fatalf("connect with encrypted key: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	defer fs.Close()
}
