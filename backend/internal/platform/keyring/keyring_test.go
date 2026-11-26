package keyring

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func newTestKeyring(t *testing.T) *Local {
	t.Helper()
	l, err := NewLocal("test-master-key-material", "test-lookup-key-material")
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return l
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	l := newTestKeyring(t)
	plaintext := []byte("+256700000000")

	sealed, err := l.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(string(sealed), "v1:"+l.KeyID()+":") {
		t.Fatalf("ciphertext header = %q", sealed)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("ciphertext contains the plaintext")
	}

	opened, err := l.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("Decrypt = %q, want %q", opened, plaintext)
	}
}

func TestEncryptIsRandomised(t *testing.T) {
	l := newTestKeyring(t)
	first, err := l.Encrypt([]byte("same"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := l.Encrypt([]byte("same"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of the same plaintext are identical")
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	l := newTestKeyring(t)
	sealed, err := l.Encrypt([]byte("secret-value"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	parts := strings.Split(string(sealed), ":")
	parts[3] = parts[3][:len(parts[3])-2] + "AA"
	if _, err := l.Decrypt([]byte(strings.Join(parts, ":"))); err == nil {
		t.Fatal("expected tampered ciphertext to fail")
	}
}

func TestDecryptRejectsOtherKeyAndGarbage(t *testing.T) {
	l := newTestKeyring(t)
	sealed, err := l.Encrypt([]byte("secret-value"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	other, err := NewLocal("another-master-key", "test-lookup-key-material")
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if _, err := other.Decrypt(sealed); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("other key: err = %v, want ErrUnknownKey", err)
	}

	for _, bad := range [][]byte{nil, []byte("nonsense"), []byte("v2:a:b:c"), []byte("v1:a:b")} {
		if _, err := l.Decrypt(bad); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Decrypt(%q) err = %v, want ErrMalformed", bad, err)
		}
	}
}

func TestNewLocalRequiresKeyMaterial(t *testing.T) {
	if _, err := NewLocal("", "lookup"); err == nil {
		t.Error("expected error for empty master key")
	}
	if _, err := NewLocal("master", " "); err == nil {
		t.Error("expected error for empty lookup key")
	}
}

func TestLookupHashIsStableAndKeyed(t *testing.T) {
	l := newTestKeyring(t)
	first := l.LookupHash("+256700000000")
	second := l.LookupHash("+256700000000")
	if first != second {
		t.Fatal("lookup hash is not stable")
	}
	if l.LookupHash("+256700000000") == l.LookupHash("+256700000001") {
		t.Fatal("different values produced the same hash")
	}

	other, err := NewLocal("test-master-key-material", "another-lookup-key")
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if other.LookupHash("+256700000000") == l.LookupHash("+256700000000") {
		t.Fatal("lookup hash does not depend on the lookup key")
	}
}
