package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/yopsa/pamoja/backend/internal/platform/config"
)

func testAuthConfig() config.Auth {
	return config.Auth{
		AccessTTL:   15 * time.Minute,
		RefreshTTL:  14 * 24 * time.Hour,
		Issuer:      "pamoja-api",
		Audience:    "pamoja-client",
		TokenSecret: "test-token-secret-0123456789abcdef",
	}
}

func TestTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	issuer := newTokenIssuer(testAuthConfig(), "development", func() time.Time { return now })

	token, expires, err := issuer.issue("user-1", "session-1", []string{"treasurer"}, []string{"group-1"}, true)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !expires.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("expires = %v, want now+15m", expires)
	}

	claims, err := issuer.parse(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != "user-1" || claims.SessionID != "session-1" {
		t.Fatalf("claims = %+v", claims)
	}
	if !claims.MFA || len(claims.Roles) != 1 || len(claims.Scopes) != 1 {
		t.Fatalf("claims payload = %+v", claims)
	}
}

func TestTokenExpires(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	issuer := newTokenIssuer(testAuthConfig(), "development", func() time.Time { return now })

	token, _, err := issuer.issue("user-1", "session-1", nil, nil, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	now = now.Add(16 * time.Minute)
	if _, err := issuer.parse(token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired token: err = %v, want ErrUnauthorized", err)
	}
}

func TestTokenRejectsWrongKeyAudienceAndEnvironment(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	issuer := newTokenIssuer(testAuthConfig(), "development", clock)
	token, _, err := issuer.issue("user-1", "session-1", nil, nil, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	otherSecret := testAuthConfig()
	otherSecret.TokenSecret = "another-token-secret-0123456789"
	if _, err := newTokenIssuer(otherSecret, "development", clock).parse(token); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("wrong key: err = %v, want ErrUnauthorized", err)
	}

	otherAudience := testAuthConfig()
	otherAudience.Audience = "another-client"
	if _, err := newTokenIssuer(otherAudience, "development", clock).parse(token); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("wrong audience: err = %v, want ErrUnauthorized", err)
	}

	if _, err := newTokenIssuer(testAuthConfig(), "production", clock).parse(token); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("cross-environment token: err = %v, want ErrUnauthorized", err)
	}
}

func TestTokenRejectsAlgNoneAndGarbage(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	issuer := newTokenIssuer(testAuthConfig(), "development", func() time.Time { return now })

	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": "pamoja-api",
		"aud": "pamoja-client",
		"sub": "user-1",
		"sid": "session-1",
		"env": "development",
		"exp": now.Add(time.Hour).Unix(),
	})
	raw, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}

	for _, token := range []string{raw, "", "not-a-jwt"} {
		if _, err := issuer.parse(token); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("parse(%q) err = %v, want ErrUnauthorized", token, err)
		}
	}
}
