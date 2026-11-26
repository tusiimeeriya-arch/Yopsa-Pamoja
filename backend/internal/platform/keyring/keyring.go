// Package keyring implements the envelope encryption and keyed lookup from
// ADR-0007: a data key derived from the deployment's master key, AES-256-GCM
// with a versioned ciphertext prefix, and an HMAC-SHA256 keyed hash for
// searching encrypted fields (for example a phone number) without decrypting.
package keyring

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Ciphertext format version. A future key scheme ships as v2.
const version = "v1"

// Errors returned when a sealed value cannot be opened.
var (
	ErrMalformed  = errors.New("keyring: malformed ciphertext")
	ErrUnknownKey = errors.New("keyring: ciphertext was sealed with another key")
	ErrDecrypt    = errors.New("keyring: authentication failed")
)

// Provider seals and opens high-sensitivity values and produces keyed lookup
// hashes. The interface keeps a cloud KMS implementation a drop-in change.
type Provider interface {
	// Encrypt returns versioned ciphertext: v1:<keyid>:<nonce>:<ciphertext>.
	Encrypt(plaintext []byte) ([]byte, error)
	// Decrypt opens a value produced by Encrypt with the same key material.
	Decrypt(sealed []byte) ([]byte, error)
	// LookupHash returns a stable keyed hash for equality lookups.
	LookupHash(value string) string
	// KeyID identifies the active key in ciphertext headers.
	KeyID() string
}

// Local is the single-host implementation: key material comes from the
// deployment environment and is stretched with SHA-256 before use.
type Local struct {
	aead      cipher.AEAD
	keyID     string
	lookupKey []byte
}

// NewLocal builds a Provider from the deployment's master and lookup keys.
// Any high-entropy string works; both are stretched with SHA-256.
func NewLocal(masterKey, lookupKey string) (*Local, error) {
	if strings.TrimSpace(masterKey) == "" {
		return nil, errors.New("keyring: master key material is required")
	}
	if strings.TrimSpace(lookupKey) == "" {
		return nil, errors.New("keyring: lookup key material is required")
	}

	encKey := sha256.Sum256([]byte("pamoja-kek-v1|" + masterKey))
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, fmt.Errorf("keyring: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keyring: gcm: %w", err)
	}

	lookup := sha256.Sum256([]byte("pamoja-lookup-v1|" + lookupKey))

	return &Local{
		aead:      aead,
		keyID:     hex.EncodeToString(encKey[:4]),
		lookupKey: lookup[:],
	}, nil
}

// KeyID identifies the active key.
func (l *Local) KeyID() string { return l.keyID }

// Encrypt seals plaintext and returns v1:<keyid>:<nonce>:<ciphertext>.
func (l *Local) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, l.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("keyring: nonce: %w", err)
	}
	sealed := l.aead.Seal(nil, nonce, plaintext, []byte(l.keyID))
	encoded := fmt.Sprintf("%s:%s:%s:%s", version, l.keyID,
		base64.RawStdEncoding.EncodeToString(nonce),
		base64.RawStdEncoding.EncodeToString(sealed))
	return []byte(encoded), nil
}

// Decrypt opens a value produced by Encrypt.
func (l *Local) Decrypt(sealed []byte) ([]byte, error) {
	parts := strings.Split(string(sealed), ":")
	if len(parts) != 4 || parts[0] != version {
		return nil, ErrMalformed
	}
	if parts[1] != l.keyID {
		return nil, ErrUnknownKey
	}
	nonce, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return nil, ErrMalformed
	}
	if len(nonce) != l.aead.NonceSize() {
		return nil, ErrMalformed
	}
	plaintext, err := l.aead.Open(nil, nonce, ciphertext, []byte(l.keyID))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// LookupHash returns the hex HMAC-SHA256 of value. Callers normalise the value
// first (see auth.NormalizePhone) so equivalent inputs hash equally.
func (l *Local) LookupHash(value string) string {
	mac := hmac.New(sha256.New, l.lookupKey)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
