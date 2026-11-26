package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, params, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected PHC encoding: %s", hash)
	}
	if params.Memory != DefaultArgonParams.Memory || params.KeyLength != DefaultArgonParams.KeyLength {
		t.Fatalf("params = %+v", params)
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword(correct) = %v, %v", ok, err)
	}
	ok, err = VerifyPassword("wrong password", hash)
	if err != nil || ok {
		t.Fatalf("VerifyPassword(wrong) = %v, %v", ok, err)
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	first, _, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, _, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Fatal("two hashes of the same password are identical")
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, _, err := HashPassword(""); !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("err = %v, want ErrEmptyPassword", err)
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=0,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$***$aGFzaA",
	}
	for _, encoded := range cases {
		if _, err := VerifyPassword("password", encoded); !errors.Is(err, ErrUnsupportedHash) {
			t.Fatalf("VerifyPassword(%q) err = %v, want ErrUnsupportedHash", encoded, err)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	hash, _, err := HashPassword("password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if NeedsRehash(hash) {
		t.Fatal("fresh hash should not need rehashing")
	}
	if !NeedsRehash("garbage") {
		t.Fatal("unparseable hash should need rehashing")
	}

	weaker := "$argon2id$v=19$m=4096,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2g"
	if !NeedsRehash(weaker) {
		t.Fatal("weaker parameters should need rehashing")
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"+256 700-000 000": "+256700000000",
		"(0772) 123.456":   "0772123456",
		"  0700 000 000 ":  "0700000000",
		"":                 "",
	}
	for input, want := range cases {
		if got := NormalizePhone(input); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", input, got, want)
		}
	}
}
