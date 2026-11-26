package auth

import (
	"testing"
	"time"
)

// RFC 6238 SHA-1 test secret ("12345678901234567890") encoded as base32.
// Public test vector from the RFC, not a credential.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" //gitleaks:allow

func TestVerifyTOTPRFC6238Vectors(t *testing.T) {
	cases := []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, tc := range cases {
		at := time.Unix(tc.unix, 0)
		if !VerifyTOTP(rfcSecret, tc.code, at) {
			t.Errorf("VerifyTOTP at %d with %s = false, want true", tc.unix, tc.code)
		}
	}
}

func TestVerifyTOTPRejectsBadInput(t *testing.T) {
	at := time.Unix(59, 0)
	if VerifyTOTP(rfcSecret, "000000", at) {
		t.Error("accepted an incorrect code")
	}
	if VerifyTOTP(rfcSecret, "2870821", at) {
		t.Error("accepted a code with the wrong length")
	}
	if VerifyTOTP("not-base32!!", "287082", at) {
		t.Error("accepted a code for an invalid secret")
	}
	if VerifyTOTP("", "287082", at) {
		t.Error("accepted a code for an empty secret")
	}
}

func TestVerifyTOTPAllowsOnePeriodOfSkew(t *testing.T) {
	// 287082 belongs to counter 1 (t=30..59). A code presented one period
	// earlier or later must still verify.
	if !VerifyTOTP(rfcSecret, "287082", time.Unix(59+totpPeriod, 0)) {
		t.Error("rejected a code one period in the future")
	}
	if !VerifyTOTP(rfcSecret, "287082", time.Unix(59-totpPeriod, 0)) {
		t.Error("rejected a code one period in the past")
	}
	if VerifyTOTP(rfcSecret, "287082", time.Unix(59+3*totpPeriod, 0)) {
		t.Error("accepted a code three periods away")
	}
}

func TestGenerateTOTPSecret(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if len(secret) != 32 {
		t.Fatalf("secret length = %d, want 32 base32 characters", len(secret))
	}

	code := totpCode(mustDecode(t, secret), uint64(time.Now().Unix()/totpPeriod))
	if !VerifyTOTP(secret, code, time.Now()) {
		t.Fatal("a freshly generated secret did not verify its own code")
	}
}

func mustDecode(t *testing.T, secret string) []byte {
	t.Helper()
	key, err := totpEncoding.DecodeString(secret)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	return key
}
