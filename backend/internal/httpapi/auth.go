package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yopsa/pamoja/backend/internal/auth"
)

// AuthService is the authentication surface the HTTP layer needs. The concrete
// implementation is *auth.Service; the interface keeps handler tests free of a
// database.
type AuthService interface {
	Login(ctx context.Context, in auth.LoginInput) (auth.Tokens, error)
	Refresh(ctx context.Context, in auth.RefreshInput) (auth.Tokens, error)
	Authenticate(ctx context.Context, rawToken string) (auth.Principal, error)
	Logout(ctx context.Context, p *auth.Principal, requestID string) error
	ListSessions(ctx context.Context, p *auth.Principal, requestID string) ([]auth.SessionInfo, error)
	RevokeSession(ctx context.Context, p *auth.Principal, sessionID, requestID string) error
	StepUp(ctx context.Context, p *auth.Principal, in auth.StepUpInput) (time.Time, error)
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p auth.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(auth.Principal)
	return p, ok
}

// requireAuth resolves the bearer token and puts the principal in the request
// context. Every route that is not public goes through it.
func (d Deps) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeError(w, r, http.StatusServiceUnavailable, "auth_unavailable")
			return
		}
		raw, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		principal, err := d.Auth.Authenticate(r.Context(), raw)
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r.WithContext(withPrincipal(r.Context(), principal)))
	}
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") || fields[1] == "" {
		return "", false
	}
	return fields[1], true
}

type loginRequest struct {
	Phone       string `json:"phone"`
	Password    string `json:"password"`
	TOTPCode    string `json:"totp_code"`
	DeviceID    string `json:"device_id"`
	SharedPhone bool   `json:"shared_phone"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
}

type stepUpRequest struct {
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

type tokensUser struct {
	ID          string `json:"id"`
	IsStaff     bool   `json:"is_staff"`
	MFARequired bool   `json:"mfa_required"`
}

type tokensResponse struct {
	AccessToken  string     `json:"access_token"`
	TokenType    string     `json:"token_type"`
	ExpiresIn    int        `json:"expires_in"`
	RefreshToken string     `json:"refresh_token"`
	SessionID    string     `json:"session_id"`
	User         tokensUser `json:"user"`
}

type sessionResponse struct {
	ID         string     `json:"id"`
	DeviceID   string     `json:"device_id"`
	UserAgent  string     `json:"user_agent"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Current    bool       `json:"current"`
}

func (d Deps) handleLogin(w http.ResponseWriter, r *http.Request) {
	if d.Auth == nil {
		writeError(w, r, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}

	tokens, err := d.Auth.Login(r.Context(), auth.LoginInput{
		Phone:       req.Phone,
		Password:    req.Password,
		TOTPCode:    req.TOTPCode,
		DeviceID:    req.DeviceID,
		UserAgent:   r.UserAgent(),
		IP:          clientIP(r),
		SharedPhone: req.SharedPhone,
		RequestID:   RequestIDFrom(r.Context()),
	})
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokensJSON(tokens))
}

func (d Deps) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if d.Auth == nil {
		writeError(w, r, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	var req refreshRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}

	tokens, err := d.Auth.Refresh(r.Context(), auth.RefreshInput{
		RefreshToken: req.RefreshToken,
		DeviceID:     req.DeviceID,
		UserAgent:    r.UserAgent(),
		IP:           clientIP(r),
		RequestID:    RequestIDFrom(r.Context()),
	})
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokensJSON(tokens))
}

func (d Deps) handleLogout(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := d.Auth.Logout(r.Context(), &principal, RequestIDFrom(r.Context())); err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleListSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	sessions, err := d.Auth.ListSessions(r.Context(), &principal, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}

	out := make([]sessionResponse, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, sessionResponse{
			ID:         session.ID,
			DeviceID:   session.DeviceID,
			UserAgent:  session.UserAgent,
			CreatedAt:  session.CreatedAt,
			LastSeenAt: session.LastSeenAt,
			ExpiresAt:  session.ExpiresAt,
			RevokedAt:  session.RevokedAt,
			Current:    session.Current,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (d Deps) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := d.Auth.RevokeSession(r.Context(), &principal,
		chi.URLParam(r, "sessionID"), RequestIDFrom(r.Context())); err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleStepUp(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req stepUpRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}

	at, err := d.Auth.StepUp(r.Context(), &principal, auth.StepUpInput{
		Password:  req.Password,
		TOTPCode:  req.TOTPCode,
		RequestID: RequestIDFrom(r.Context()),
	})
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"step_up_at": at.UTC().Format(time.RFC3339)})
}

// handleAuthError maps service errors to responses that never disclose whether
// an account exists or why a credential failed.
func (d Deps) handleAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		writeError(w, r, http.StatusTooManyRequests, "too_many_requests")
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, r, http.StatusUnauthorized, "mfa_required")
	case errors.Is(err, auth.ErrInvalidGrant):
		writeError(w, r, http.StatusUnauthorized, "invalid_grant")
	case errors.Is(err, auth.ErrSessionNotFound):
		writeError(w, r, http.StatusNotFound, "not_found")
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrUnauthorized):
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials")
	default:
		d.logger().Error("auth request failed",
			"error", err.Error(),
			"request_id", RequestIDFrom(r.Context()))
		writeError(w, r, http.StatusInternalServerError, "internal_error")
	}
}

func tokensJSON(tokens auth.Tokens) tokensResponse {
	return tokensResponse{
		AccessToken:  tokens.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    tokens.ExpiresIn,
		RefreshToken: tokens.RefreshToken,
		SessionID:    tokens.SessionID,
		User: tokensUser{
			ID:          tokens.UserID,
			IsStaff:     tokens.IsStaff,
			MFARequired: tokens.MFARequired,
		},
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("httpapi: unexpected trailing data")
	}
	return nil
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code string) {
	writeJSON(w, status, map[string]string{
		"error":      code,
		"request_id": RequestIDFrom(r.Context()),
	})
}

// clientIP is the peer address. Forwarding headers are not trusted here; the
// deployment's proxy allow-list is a documented P9 hardening step.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (d Deps) logger() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return newDiscardLogger()
}
