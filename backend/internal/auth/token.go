package auth

import (
	"crypto/sha256"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/yopsa/pamoja/backend/internal/platform/config"
)

// tokenLeeway tolerates a small clock difference between the API and the
// client when checking nbf/exp.
const tokenLeeway = 30 * time.Second

// accessClaims is the payload of an access token. Roles and scopes are a
// snapshot taken when the token was issued; the session itself is re-checked
// on every request, so a revoked session stops working immediately and a role
// change takes effect within one access-token lifetime (≤ 15 minutes).
type accessClaims struct {
	jwt.RegisteredClaims
	SessionID   string   `json:"sid"`
	Environment string   `json:"env"`
	Roles       []string `json:"roles,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	MFA         bool     `json:"mfa"`
}

type tokenIssuer struct {
	key      []byte
	issuer   string
	audience string
	env      string
	ttl      time.Duration
	now      func() time.Time
}

func newTokenIssuer(cfg config.Auth, env string, now func() time.Time) *tokenIssuer {
	// Stretch the deployment secret so any high-entropy string yields a
	// 256-bit signing key.
	sum := sha256.Sum256([]byte("pamoja-jwt-v1|" + cfg.TokenSecret))
	return &tokenIssuer{
		key:      sum[:],
		issuer:   cfg.Issuer,
		audience: cfg.Audience,
		env:      env,
		ttl:      cfg.AccessTTL,
		now:      now,
	}
}

func (t *tokenIssuer) issue(userID, sessionID string, roles, scopes []string, mfa bool) (string, time.Time, error) {
	now := t.now()
	expires := now.Add(t.ttl)

	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{t.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-tokenLeeway)),
			ExpiresAt: jwt.NewNumericDate(expires),
			ID:        uuid.NewString(),
		},
		SessionID:   sessionID,
		Environment: t.env,
		Roles:       roles,
		Scopes:      scopes,
		MFA:         mfa,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.key)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expires, nil
}

func (t *tokenIssuer) parse(raw string) (*accessClaims, error) {
	claims := &accessClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("auth: unexpected signing method")
		}
		return t.key, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(t.issuer),
		jwt.WithAudience(t.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(tokenLeeway),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil || !token.Valid {
		return nil, ErrUnauthorized
	}
	if claims.Environment != t.env {
		// A token minted for another environment is never accepted (T-SEC-06).
		return nil, ErrUnauthorized
	}
	if claims.Subject == "" || claims.SessionID == "" {
		return nil, ErrUnauthorized
	}
	return claims, nil
}
