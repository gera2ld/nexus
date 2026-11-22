package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeyEnv names the environment variable holding the secrets key: 32 random
// bytes, base64 (for example `openssl rand -base64 32`).
const KeyEnv = "NEXUS_ENC_KEY"

// ErrNoKey is returned when secrets must be read or written but no key is set.
var ErrNoKey = errors.New("storage: " + KeyEnv + " is not set; it is needed to store or use storage account secrets")

const sealedPrefix = "v1:"

// Sealer encrypts storage account secrets with AES-256-GCM.
type Sealer struct{ aead cipher.AEAD }

// NewSealer parses a base64 32-byte key. An empty key gives a nil Sealer,
// whose methods return ErrNoKey.
func NewSealer(b64 string) (*Sealer, error) {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("storage: %s is not valid base64: %w", KeyEnv, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("storage: %s must be 32 bytes, got %d", KeyEnv, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext; ad binds it to its context (the account id), so
// a ciphertext copied to another account won't open.
func (s *Sealer) Seal(plaintext []byte, ad string) (string, error) {
	if s == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, plaintext, []byte(ad))
	return sealedPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value from Seal.
func (s *Sealer) Open(sealed, ad string) ([]byte, error) {
	if s == nil {
		return nil, ErrNoKey
	}
	raw, ok := strings.CutPrefix(sealed, sealedPrefix)
	if !ok {
		return nil, errors.New("storage: unknown secrets format")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(b) < s.aead.NonceSize() {
		return nil, errors.New("storage: corrupt secrets")
	}
	n := s.aead.NonceSize()
	pt, err := s.aead.Open(nil, b[:n], b[n:], []byte(ad))
	if err != nil {
		return nil, errors.New("storage: cannot decrypt secrets (wrong " + KeyEnv + "?)")
	}
	return pt, nil
}
