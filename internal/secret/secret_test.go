package secret

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTripAndTamper(t *testing.T) {
	if err := SetKey(bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	enc, err := Encrypt("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, prefix) || strings.Contains(enc, "hunter2") {
		t.Fatalf("ciphertext looks wrong: %q", enc)
	}
	enc2, _ := Encrypt("hunter2")
	if enc == enc2 {
		t.Fatal("nonce reuse: identical ciphertexts")
	}
	got, err := Decrypt(enc)
	if err != nil || got != "hunter2" {
		t.Fatalf("Decrypt = %q, %v", got, err)
	}
	if e, _ := Encrypt(""); e != "" {
		t.Fatalf("empty must stay empty, got %q", e)
	}
	if _, err := Decrypt("plaintext-password"); err == nil {
		t.Fatal("plaintext must not be accepted")
	}
	_ = SetKey(bytes.Repeat([]byte{8}, 32))
	if _, err := Decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong key succeeded")
	}
}

func TestInitKeyFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvKey, "")
	p, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v, want 0600", st.Mode().Perm())
	}
	enc, _ := Encrypt("x")
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if got, err := Decrypt(enc); err != nil || got != "x" {
		t.Fatalf("after reload: %q, %v", got, err)
	}
	if got, _ := filepath.Abs(p); filepath.Dir(got) != dir {
		t.Fatalf("key file not in dir: %s", p)
	}

	t.Setenv(EnvKey, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if p, err := Init(dir); err != nil || p != "" {
		t.Fatalf("env key: %q, %v", p, err)
	}
	t.Setenv(EnvKey, "too-short")
	if _, err := Init(dir); err == nil {
		t.Fatal("bad env key accepted")
	}
}
