// Package auth implements the platform's authentication core: Argon2id
// credentials, TOTP second factors, short-lived signed access tokens and
// rotating refresh-token sessions with reuse detection (ARCHITECTURE §8).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// ArgonParams are the Argon2id work factors stored alongside a credential so a
// future change can rehash on next login.
type ArgonParams struct {
	Memory      uint32 `json:"m"`
	Iterations  uint32 `json:"t"`
	Parallelism uint8  `json:"p"`
	KeyLength   uint32 `json:"k"`
	SaltLength  uint32 `json:"s"`
}

// DefaultArgonParams target roughly 50–100 ms on a modest server: 64 MiB,
// three passes, two lanes. Tuning on the deployment hardware is a P9 task.
var DefaultArgonParams = ArgonParams{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	KeyLength:   32,
	SaltLength:  16,
}

const argonVersion = argon2.Version

// ErrEmptyPassword is returned when hashing an empty password.
var ErrEmptyPassword = errors.New("auth: empty password")

// ErrUnsupportedHash is returned for a hash this build cannot verify.
var ErrUnsupportedHash = errors.New("auth: unsupported password hash")

// HashPassword returns a PHC-encoded Argon2id hash and the parameters used.
func HashPassword(password string) (string, ArgonParams, error) {
	if password == "" {
		return "", ArgonParams{}, ErrEmptyPassword
	}
	params := DefaultArgonParams

	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", ArgonParams{}, fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argonVersion, params.Memory, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
	return encoded, params, nil
}

// VerifyPassword checks password against a PHC-encoded Argon2id hash in
// constant time with respect to the stored hash.
func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, uint32(len(want))) // #nosec G115 -- key length is bounded by the PHC hash.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether encoded was produced with different parameters
// than the current defaults, or cannot be parsed at all.
func NeedsRehash(encoded string) bool {
	params, _, _, err := decodeHash(encoded)
	if err != nil {
		return true
	}
	return params.Memory != DefaultArgonParams.Memory ||
		params.Iterations != DefaultArgonParams.Iterations ||
		params.Parallelism != DefaultArgonParams.Parallelism ||
		params.KeyLength != DefaultArgonParams.KeyLength
}

func decodeHash(encoded string) (ArgonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ArgonParams{}, nil, nil, ErrUnsupportedHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argonVersion {
		return ArgonParams{}, nil, nil, ErrUnsupportedHash
	}

	var params ArgonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.Memory, &params.Iterations, &params.Parallelism); err != nil {
		return ArgonParams{}, nil, nil, ErrUnsupportedHash
	}
	if params.Memory == 0 || params.Iterations == 0 || params.Parallelism == 0 {
		return ArgonParams{}, nil, nil, ErrUnsupportedHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return ArgonParams{}, nil, nil, ErrUnsupportedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return ArgonParams{}, nil, nil, ErrUnsupportedHash
	}

	params.KeyLength = uint32(len(key))   // #nosec G115 -- bounded by the encoded hash.
	params.SaltLength = uint32(len(salt)) // #nosec G115 -- bounded by the encoded hash.
	return params, salt, key, nil
}

// dummyHash is verified when the account does not exist so that a failed login
// costs the same whether or not the account is real (T-SEC-04).
var dummyHash = sync.OnceValue(func() string {
	hash, _, err := HashPassword("pamoja-timing-equalisation-dummy-password")
	if err != nil {
		return ""
	}
	return hash
})
