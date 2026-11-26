package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yopsa/pamoja/backend/internal/auth"
	"github.com/yopsa/pamoja/backend/internal/platform/config"
)

type fakeAuth struct {
	login        func(ctx context.Context, in auth.LoginInput) (auth.Tokens, error)
	refresh      func(ctx context.Context, in auth.RefreshInput) (auth.Tokens, error)
	authenticate func(ctx context.Context, token string) (auth.Principal, error)
	logout       func(ctx context.Context, p *auth.Principal, requestID string) error
	listSessions func(ctx context.Context, p *auth.Principal, requestID string) ([]auth.SessionInfo, error)
	revoke       func(ctx context.Context, p *auth.Principal, sessionID, requestID string) error
	stepUp       func(ctx context.Context, p *auth.Principal, in auth.StepUpInput) (time.Time, error)
}

func (f *fakeAuth) Login(ctx context.Context, in auth.LoginInput) (auth.Tokens, error) {
	return f.login(ctx, in)
}

func (f *fakeAuth) Refresh(ctx context.Context, in auth.RefreshInput) (auth.Tokens, error) {
	return f.refresh(ctx, in)
}

func (f *fakeAuth) Authenticate(ctx context.Context, token string) (auth.Principal, error) {
	return f.authenticate(ctx, token)
}

func (f *fakeAuth) Logout(ctx context.Context, p *auth.Principal, requestID string) error {
	return f.logout(ctx, p, requestID)
}

func (f *fakeAuth) ListSessions(ctx context.Context, p *auth.Principal, requestID string) ([]auth.SessionInfo, error) {
	return f.listSessions(ctx, p, requestID)
}

func (f *fakeAuth) RevokeSession(ctx context.Context, p *auth.Principal, sessionID, requestID string) error {
	return f.revoke(ctx, p, sessionID, requestID)
}

func (f *fakeAuth) StepUp(ctx context.Context, p *auth.Principal, in auth.StepUpInput) (time.Time, error) {
	return f.stepUp(ctx, p, in)
}

func testDeps(svc AuthService) Deps {
	return Deps{
		Config: config.Config{AppEnv: config.EnvDevelopment, MaxBodyBytes: 1 << 20},
		Auth:   svc,
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestLoginReturnsTokens(t *testing.T) {
	var captured auth.LoginInput
	svc := &fakeAuth{login: func(_ context.Context, in auth.LoginInput) (auth.Tokens, error) {
		captured = in
		return auth.Tokens{
			AccessToken:  "access",
			RefreshToken: "refresh",
			ExpiresIn:    900,
			SessionID:    uuid.NewString(),
			UserID:       uuid.NewString(),
			IsStaff:      true,
		}, nil
	}}

	rec := doJSON(t, New(testDeps(svc)), http.MethodPost, "/api/v1/auth/login",
		`{"phone":"+256700000000","password":"secret","totp_code":"123456","device_id":"phone-1"}`,
		map[string]string{"X-Request-ID": "req-login"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["access_token"] != "access" || body["token_type"] != "Bearer" || body["refresh_token"] != "refresh" {
		t.Fatalf("body = %v", body)
	}
	if captured.Phone != "+256700000000" || captured.TOTPCode != "123456" || captured.RequestID != "req-login" {
		t.Fatalf("captured input = %+v", captured)
	}
}

func TestLoginErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid credentials", auth.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
		{"mfa required", auth.ErrMFARequired, http.StatusUnauthorized, "mfa_required"},
		{"rate limited", auth.ErrRateLimited, http.StatusTooManyRequests, "too_many_requests"},
		{"internal", context.DeadlineExceeded, http.StatusInternalServerError, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeAuth{login: func(context.Context, auth.LoginInput) (auth.Tokens, error) {
				return auth.Tokens{}, tc.err
			}}
			rec := doJSON(t, New(testDeps(svc)), http.MethodPost, "/api/v1/auth/login",
				`{"phone":"+256700000000","password":"wrong"}`, nil)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if body := decodeBody(t, rec); body["error"] != tc.code {
				t.Fatalf("error = %v, want %s", body["error"], tc.code)
			}
			if tc.code == "too_many_requests" && rec.Header().Get("Retry-After") == "" {
				t.Fatal("rate-limited response has no Retry-After header")
			}
		})
	}
}

func TestLoginRejectsUnknownFields(t *testing.T) {
	svc := &fakeAuth{login: func(context.Context, auth.LoginInput) (auth.Tokens, error) {
		t.Fatal("handler should not reach the service")
		return auth.Tokens{}, nil
	}}
	rec := doJSON(t, New(testDeps(svc)), http.MethodPost, "/api/v1/auth/login",
		`{"phone":"+256700000000","password":"x","admin":true}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestProtectedRoutesRequireToken(t *testing.T) {
	svc := &fakeAuth{
		authenticate: func(context.Context, string) (auth.Principal, error) {
			return auth.Principal{}, auth.ErrUnauthorized
		},
		login:   func(context.Context, auth.LoginInput) (auth.Tokens, error) { return auth.Tokens{}, nil },
		refresh: func(context.Context, auth.RefreshInput) (auth.Tokens, error) { return auth.Tokens{}, nil },
	}

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/sessions"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodPost, "/api/v1/auth/step-up"},
		{http.MethodPost, "/api/v1/auth/sessions/" + uuid.NewString() + "/revoke"},
	} {
		rec := doJSON(t, New(testDeps(svc)), route.method, route.path, `{}`, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", route.method, route.path, rec.Code)
		}
	}
}

func TestSessionsListAndRevoke(t *testing.T) {
	userID := uuid.New()
	var revoked string
	svc := &fakeAuth{
		login:   func(context.Context, auth.LoginInput) (auth.Tokens, error) { return auth.Tokens{}, nil },
		refresh: func(context.Context, auth.RefreshInput) (auth.Tokens, error) { return auth.Tokens{}, nil },
		authenticate: func(context.Context, string) (auth.Principal, error) {
			return auth.Principal{UserID: userID, SessionID: uuid.New()}, nil
		},
		listSessions: func(_ context.Context, p *auth.Principal, _ string) ([]auth.SessionInfo, error) {
			if p.UserID != userID {
				t.Fatalf("principal user = %s, want %s", p.UserID, userID)
			}
			return []auth.SessionInfo{{
				ID:         "session-1",
				DeviceID:   "phone-1",
				UserAgent:  "test",
				CreatedAt:  time.Now(),
				LastSeenAt: time.Now(),
				ExpiresAt:  time.Now().Add(time.Hour),
				Current:    true,
			}}, nil
		},
		revoke: func(_ context.Context, _ *auth.Principal, sessionID, _ string) error {
			revoked = sessionID
			return nil
		},
		stepUp: func(context.Context, *auth.Principal, auth.StepUpInput) (time.Time, error) {
			return time.Now(), nil
		},
	}

	handler := New(testDeps(svc))
	authHeader := map[string]string{"Authorization": "Bearer token"}

	rec := doJSON(t, handler, http.MethodGet, "/api/v1/auth/sessions", "", authHeader)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body)
	}
	var listed struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != "session-1" || !listed.Sessions[0].Current {
		t.Fatalf("sessions = %+v", listed.Sessions)
	}

	target := uuid.NewString()
	rec = doJSON(t, handler, http.MethodPost, "/api/v1/auth/sessions/"+target+"/revoke", "", authHeader)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, body = %s", rec.Code, rec.Body)
	}
	if revoked != target {
		t.Fatalf("revoked = %q, want %q", revoked, target)
	}
}

func TestStepUpReturnsFreshTime(t *testing.T) {
	svc := &fakeAuth{
		login:   func(context.Context, auth.LoginInput) (auth.Tokens, error) { return auth.Tokens{}, nil },
		refresh: func(context.Context, auth.RefreshInput) (auth.Tokens, error) { return auth.Tokens{}, nil },
		authenticate: func(context.Context, string) (auth.Principal, error) {
			return auth.Principal{UserID: uuid.New(), SessionID: uuid.New()}, nil
		},
		stepUp: func(_ context.Context, _ *auth.Principal, in auth.StepUpInput) (time.Time, error) {
			if in.Password == "" {
				t.Fatal("password not forwarded")
			}
			return time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC), nil
		},
	}

	rec := doJSON(t, New(testDeps(svc)), http.MethodPost, "/api/v1/auth/step-up",
		`{"password":"secret","totp_code":"123456"}`,
		map[string]string{"Authorization": "Bearer token"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if body := decodeBody(t, rec); body["step_up_at"] != "2026-11-26T09:00:00Z" {
		t.Fatalf("step_up_at = %v", body["step_up_at"])
	}
}

func TestRefreshReturnsTokens(t *testing.T) {
	svc := &fakeAuth{
		login: func(context.Context, auth.LoginInput) (auth.Tokens, error) { return auth.Tokens{}, nil },
		refresh: func(_ context.Context, in auth.RefreshInput) (auth.Tokens, error) {
			if in.RefreshToken != "old-refresh" {
				t.Fatalf("refresh token = %q", in.RefreshToken)
			}
			return auth.Tokens{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 900}, nil
		},
	}

	rec := doJSON(t, New(testDeps(svc)), http.MethodPost, "/api/v1/auth/refresh",
		`{"refresh_token":"old-refresh"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if body := decodeBody(t, rec); body["refresh_token"] != "new-refresh" {
		t.Fatalf("body = %v", body)
	}
}
