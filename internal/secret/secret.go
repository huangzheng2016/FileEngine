// Package secret encrypts credentials at rest (Filesystem.Password,
// ModelProvider.APIKey) with AES-256-GCM. The key comes from FE_SECRET_KEY
// (base64 of 32 bytes) or, when unset, from a generated key file (0600) next
// to the database. There is no plaintext fallback: a stored value that does
// not decrypt is an error, never silently returned as-is.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// KeyFileName is the generated key file placed next to the database.
const KeyFileName = "secret.key"

// EnvKey names the environment variable that overrides the key file.
const EnvKey = "FE_SECRET_KEY"

// prefix versions the ciphertext format so a future algorithm change is a
// clean break rather than a guess.
const prefix = "v1:"

var (
	mu  sync.RWMutex
	gcm cipher.AEAD
)

// ErrNoKey is returned when a secret is encrypted/decrypted before Init/SetKey.
var ErrNoKey = errors.New("secret key not initialized")

// Init loads the key from FE_SECRET_KEY, or from dir/secret.key — generating
// that file (0600) on first start. It returns the key file path used ("" when
// the env var supplied the key).
func Init(dir string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(EnvKey)); v != "" {
		k, err := decodeKey(v)
		if err != nil {
			return "", fmt.Errorf("%s: %w", EnvKey, err)
		}
		return "", SetKey(k)
	}
	p := filepath.Join(dir, KeyFileName)
	data, err := os.ReadFile(p)
	switch {
	case err == nil:
		k, err := decodeKey(strings.TrimSpace(string(data)))
		if err != nil {
			return "", fmt.Errorf("%s: %w", p, err)
		}
		return p, SetKey(k)
	case errors.Is(err, os.ErrNotExist):
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return "", err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		enc := base64.StdEncoding.EncodeToString(k) + "\n"
		if err := os.WriteFile(p, []byte(enc), 0o600); err != nil {
			return "", fmt.Errorf("write %s: %w", p, err)
		}
		return p, SetKey(k)
	default:
		return "", err
	}
}

func decodeKey(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if k, err := enc.DecodeString(s); err == nil && len(k) == 32 {
			return k, nil
		}
	}
	return nil, errors.New("key must be base64 of exactly 32 bytes")
}

// SetKey installs a raw 32-byte key (tests, embedders).
func SetKey(k []byte) error {
	if len(k) != 32 {
		return errors.New("key must be 32 bytes")
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	mu.Lock()
	gcm = g
	mu.Unlock()
	return nil
}

func aead() (cipher.AEAD, error) {
	mu.RLock()
	defer mu.RUnlock()
	if gcm == nil {
		return nil, ErrNoKey
	}
	return gcm, nil
}

// Encrypt seals plain. The empty string stays empty so "no secret set" needs
// no key and stays distinguishable in the DB.
func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	g, err := aead()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := g.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value produced by Encrypt.
func Decrypt(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, prefix) {
		return "", errors.New("stored secret is not encrypted (unsupported format)")
	}
	g, err := aead()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(stored[len(prefix):])
	if err != nil || len(raw) < g.NonceSize() {
		return "", errors.New("stored secret is malformed")
	}
	plain, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
	if err != nil {
		return "", errors.New("stored secret does not decrypt with the current key")
	}
	return string(plain), nil
}
