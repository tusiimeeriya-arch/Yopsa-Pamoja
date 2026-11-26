package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/platform/config"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/platform/keyring"
	"github.com/yopsa/pamoja/backend/internal/platform/phone"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// Authentication failures returned to callers. The HTTP layer maps them to
// generic responses; none of them names an account or its state.
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrMFARequired        = errors.New("auth: mfa required")
	ErrRateLimited        = errors.New("auth: too many attempts")
	ErrInvalidGrant       = errors.New("auth: invalid refresh grant")
	ErrUnauthorized       = errors.New("auth: unauthorized")
	ErrSessionNotFound    = errors.New("auth: session not found")
)

// errRotateRaced means the presented refresh token was rotated by a concurrent
// request between the lookup and the rotation.
var errRotateRaced = errors.New("auth: refresh token already rotated")

// Principal is the authenticated caller of one request.
type Principal struct {
	UserID      uuid.UUID
	SessionID   uuid.UUID
	FamilyID    uuid.UUID
	Roles       []string
	Scopes      []string
	Bindings    []rbac.Binding
	IsStaff     bool
	MFAVerified bool
	StepUpAt    *time.Time
}

// StepUpFresh reports whether a step-up happened within window.
func (p *Principal) StepUpFresh(window time.Duration, now time.Time) bool {
	return p.StepUpAt != nil && now.Sub(*p.StepUpAt) <= window
}

// LoginInput is one login attempt.
type LoginInput struct {
	Phone       string
	Password    string
	TOTPCode    string
	DeviceID    string
	UserAgent   string
	IP          string
	SharedPhone bool
	RequestID   string
}

// RefreshInput is one refresh attempt.
type RefreshInput struct {
	RefreshToken string
	DeviceID     string
	UserAgent    string
	IP           string
	RequestID    string
}

// StepUpInput re-authenticates a live session before a sensitive action.
type StepUpInput struct {
	Password  string
	TOTPCode  string
	RequestID string
}

// Tokens is a successful login or refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	SessionID    string
	UserID       string
	IsStaff      bool
	MFARequired  bool
}

// SessionInfo describes one of the caller's sessions.
type SessionInfo struct {
	ID         string
	DeviceID   string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	Current    bool
}

// Options configure a Service.
type Options struct {
	Pool   *pgxpool.Pool
	Config config.Auth
	Env    string
	Keys   keyring.Provider
	Audit  *audit.Recorder
	Logger *slog.Logger
	Now    func() time.Time
}

// Service implements login, refresh rotation, session management and step-up.
type Service struct {
	pool   *pgxpool.Pool
	cfg    config.Auth
	keys   keyring.Provider
	audit  *audit.Recorder
	log    *slog.Logger
	now    func() time.Time
	tokens *tokenIssuer

	ipLimiter      *limiter
	accountLimiter *limiter
}

// NewService builds a Service.
func NewService(o Options) *Service {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Service{
		pool:           o.Pool,
		cfg:            o.Config,
		keys:           o.Keys,
		audit:          o.Audit,
		log:            log,
		now:            now,
		tokens:         newTokenIssuer(o.Config, o.Env, now),
		ipLimiter:      newLimiter(o.Config.LoginPerIPPerMinute, time.Minute),
		accountLimiter: newLimiter(o.Config.LoginPerAccountPerHour, time.Hour),
	}
}

type userRecord struct {
	ID              string
	Status          string
	IsStaff         bool
	MFARequired     bool
	LockedUntil     *time.Time
	PasswordHash    string
	TOTPSecretEnc   []byte
	TOTPConfirmedAt *time.Time
}

type sessionRecord struct {
	ID                string
	UserID            string
	FamilyID          string
	Status            string
	IsStaff           bool
	MFARequired       bool
	SharedPhone       bool
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
	RevokedAt         *time.Time
	RevokedReason     string
	LastSeenAt        time.Time
	MFAVerifiedAt     *time.Time
	StepUpAt          *time.Time
}

// NormalizePhone strips separators so equivalent inputs hash equally. It
// never stores or logs the number itself.
func NormalizePhone(raw string) string { return phone.Normalize(raw) }

// Login verifies credentials, applies lockout and rate limits, and opens a new
// session family. It returns ErrInvalidCredentials for every credential
// problem so callers cannot enumerate accounts.
func (s *Service) Login(ctx context.Context, in LoginInput) (Tokens, error) {
	phone := NormalizePhone(in.Phone)
	if phone == "" || in.Password == "" {
		return Tokens{}, ErrInvalidCredentials
	}
	phoneHash := s.keys.LookupHash(phone)

	if !s.ipLimiter.allow(in.IP, s.now()) || !s.accountLimiter.allow(phoneHash, s.now()) {
		return Tokens{}, ErrRateLimited
	}

	user, err := s.findUser(ctx, phoneHash)
	if err != nil {
		return Tokens{}, err
	}
	if user == nil {
		// Equalise timing, then record the attempt without an actor.
		_, _ = VerifyPassword(in.Password, dummyHash())
		s.recordUnknownFailure(ctx, in)
		return Tokens{}, ErrInvalidCredentials
	}

	if user.Status != "active" || user.PasswordHash == "" {
		return Tokens{}, ErrInvalidCredentials
	}
	if user.LockedUntil != nil && user.LockedUntil.After(s.now()) {
		return Tokens{}, ErrInvalidCredentials
	}

	ok, err := VerifyPassword(in.Password, user.PasswordHash)
	if err != nil || !ok {
		s.recordFailure(ctx, user, in)
		return Tokens{}, ErrInvalidCredentials
	}

	mfaVerified := false
	if user.MFARequired {
		if strings.TrimSpace(in.TOTPCode) == "" {
			return Tokens{}, ErrMFARequired
		}
		if !s.verifyTOTP(user, in.TOTPCode) {
			s.recordFailure(ctx, user, in)
			return Tokens{}, ErrInvalidCredentials
		}
		mfaVerified = true
	}

	return s.openSession(ctx, user, mfaVerified, in)
}

// Refresh rotates a refresh token. Presenting a token that was already
// rotated revokes the whole family (T-SEC-02).
func (s *Service) Refresh(ctx context.Context, in RefreshInput) (Tokens, error) {
	if strings.TrimSpace(in.RefreshToken) == "" {
		return Tokens{}, ErrInvalidGrant
	}
	session, err := s.sessionByRefresh(ctx, hashRefresh(in.RefreshToken))
	if err != nil {
		return Tokens{}, err
	}
	if session == nil {
		return Tokens{}, ErrInvalidGrant
	}

	if session.RevokedAt != nil {
		if err := s.revokeFamily(ctx, session.FamilyID, "refresh_reuse", in.RequestID); err != nil {
			s.log.Error("revoke family after refresh reuse", "error", err.Error())
		}
		return Tokens{}, ErrInvalidGrant
	}

	now := s.now()
	idle := s.cfg.IdleStaff
	if session.SharedPhone {
		idle = s.cfg.IdleSharedPhone
	}
	if session.Status != "active" ||
		now.After(session.ExpiresAt) ||
		now.After(session.AbsoluteExpiresAt) ||
		now.Sub(session.LastSeenAt) > idle {
		if err := s.revokeFamily(ctx, session.FamilyID, "expired", in.RequestID); err != nil {
			s.log.Error("revoke expired family", "error", err.Error())
		}
		return Tokens{}, ErrInvalidGrant
	}

	refreshToken, err := newRefreshToken()
	if err != nil {
		return Tokens{}, err
	}

	var newSessionID string
	err = db.WithRequest(ctx, s.pool, in.RequestID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT pamoja.auth_rotate_session($1::uuid, $2, $3, $4, $5, $6, $7)::text`,
			session.ID, hashRefresh(refreshToken), in.DeviceID, in.UserAgent, in.IP,
			session.AbsoluteExpiresAt, session.AbsoluteExpiresAt).Scan(&newSessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errRotateRaced
		}
		if err != nil {
			return err
		}
		_, err = s.audit.Append(ctx, in.RequestID, audit.Entry{
			ActorUserID: session.UserID,
			Action:      "auth.refresh",
			EntityType:  "session",
			EntityID:    newSessionID,
			IP:          in.IP,
			DeviceID:    in.DeviceID,
		})
		return err
	})
	if errors.Is(err, errRotateRaced) {
		if revokeErr := s.revokeFamily(ctx, session.FamilyID, "refresh_reuse", in.RequestID); revokeErr != nil {
			s.log.Error("revoke family after rotation race", "error", revokeErr.Error())
		}
		return Tokens{}, ErrInvalidGrant
	}
	if err != nil {
		return Tokens{}, err
	}

	roles, scopes, err := s.rolesAndScopes(ctx, session.UserID)
	if err != nil {
		return Tokens{}, err
	}
	access, _, err := s.tokens.issue(session.UserID, newSessionID, roles, scopes, session.MFAVerifiedAt != nil)
	if err != nil {
		return Tokens{}, err
	}

	return Tokens{
		AccessToken:  access,
		RefreshToken: refreshToken,
		ExpiresIn:    int(s.cfg.AccessTTL.Seconds()),
		SessionID:    newSessionID,
		UserID:       session.UserID,
		IsStaff:      session.IsStaff,
		MFARequired:  session.MFARequired,
	}, nil
}

// Authenticate validates an access token and the session it names.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	claims, err := s.tokens.parse(rawToken)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}

	session, err := s.principal(ctx, sessionID.String())
	if err != nil {
		return Principal{}, err
	}
	if session == nil ||
		session.UserID != userID.String() ||
		session.Status != "active" ||
		session.RevokedAt != nil ||
		s.now().After(session.ExpiresAt) {
		return Principal{}, ErrUnauthorized
	}
	familyID, err := uuid.Parse(session.FamilyID)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	bindings, err := s.bindings(ctx, userID.String())
	if err != nil {
		return Principal{}, err
	}

	return Principal{
		UserID:      userID,
		SessionID:   sessionID,
		FamilyID:    familyID,
		Roles:       claims.Roles,
		Scopes:      claims.Scopes,
		Bindings:    bindings,
		IsStaff:     session.IsStaff,
		MFAVerified: session.MFAVerifiedAt != nil,
		StepUpAt:    session.StepUpAt,
	}, nil
}

// bindings loads the caller's live role grants as authorisation bindings. The
// query runs through the definer function, which applies the effective-date
// and status rules; a revoked or expired grant disappears immediately rather
// than at the next token refresh.
func (s *Service) bindings(ctx context.Context, userID string) ([]rbac.Binding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT role, legal_entity_id::text, branch_id::text, group_id::text
		 FROM pamoja.auth_roles($1::uuid)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bindings []rbac.Binding
	for rows.Next() {
		var role string
		var legalEntity, branch, group *string
		if err := rows.Scan(&role, &legalEntity, &branch, &group); err != nil {
			return nil, err
		}
		binding := rbac.Binding{Role: rbac.Role(role), Scope: rbac.ScopePlatform}
		switch {
		case role == string(rbac.RoleMember):
			// A member's grants are self-scoped; the group id only identifies
			// which group the membership belongs to.
			binding.Scope = rbac.ScopeSelf
			if group != nil && *group != "" {
				binding.ScopeIDs = []string{*group}
			}
		case group != nil && *group != "":
			binding.Scope = rbac.ScopeGroup
			binding.ScopeIDs = []string{*group}
		case branch != nil && *branch != "":
			binding.Scope = rbac.ScopeBranch
			binding.ScopeIDs = []string{*branch}
		case legalEntity != nil && *legalEntity != "":
			binding.Scope = rbac.ScopeLegalEntity
			binding.ScopeIDs = []string{*legalEntity}
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

// Logout revokes the caller's whole session family.
func (s *Service) Logout(ctx context.Context, p *Principal, requestID string) error {
	return db.WithActor(ctx, s.pool, db.Actor{UserID: p.UserID.String(), Scopes: p.Scopes}, requestID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pamoja.auth_revoke_family($1::uuid, $2)`,
			p.FamilyID.String(), "logout"); err != nil {
			return err
		}
		_, err := s.audit.Append(ctx, requestID, audit.Entry{
			ActorUserID: p.UserID.String(),
			Action:      "auth.logout",
			EntityType:  "session",
			EntityID:    p.SessionID.String(),
			Channel:     "self",
		})
		return err
	})
}

// ListSessions returns the caller's recent sessions. Row-level security
// restricts the query to the caller's own rows even if the predicate were
// wrong.
func (s *Service) ListSessions(ctx context.Context, p *Principal, requestID string) ([]SessionInfo, error) {
	var sessions []SessionInfo
	err := db.WithActor(ctx, s.pool, db.Actor{UserID: p.UserID.String(), Scopes: p.Scopes}, requestID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id::text, device_id, user_agent, created_at, last_seen_at,
			        expires_at, revoked_at
			 FROM pamoja.session
			 WHERE user_id = $1::uuid
			 ORDER BY last_seen_at DESC
			 LIMIT 50`, p.UserID.String())
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var info SessionInfo
			if err := rows.Scan(&info.ID, &info.DeviceID, &info.UserAgent,
				&info.CreatedAt, &info.LastSeenAt, &info.ExpiresAt,
				&info.RevokedAt); err != nil {
				return err
			}
			info.Current = info.ID == p.SessionID.String()
			sessions = append(sessions, info)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// RevokeSession revokes one of the caller's session families by session id.
func (s *Service) RevokeSession(ctx context.Context, p *Principal, sessionID, requestID string) error {
	if _, err := uuid.Parse(sessionID); err != nil {
		return ErrSessionNotFound
	}

	var familyID string
	err := db.WithActor(ctx, s.pool, db.Actor{UserID: p.UserID.String(), Scopes: p.Scopes}, requestID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT family_id::text FROM pamoja.session
			 WHERE id = $1::uuid AND user_id = $2::uuid`,
			sessionID, p.UserID.String()).Scan(&familyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`SELECT pamoja.auth_revoke_family($1::uuid, $2)`,
			familyID, "user_revoked"); err != nil {
			return err
		}
		_, err = s.audit.Append(ctx, requestID, audit.Entry{
			ActorUserID: p.UserID.String(),
			Action:      "auth.session_revoked",
			EntityType:  "session_family",
			EntityID:    familyID,
			Reason:      "user_revoked",
			Channel:     "self",
		})
		return err
	})
	if err != nil {
		return err
	}
	return nil
}

// StepUp re-verifies the caller's credentials and records fresh MFA on the
// session. Sensitive actions require a step-up within the configured window.
func (s *Service) StepUp(ctx context.Context, p *Principal, in StepUpInput) (time.Time, error) {
	user, err := s.userSecrets(ctx, p.UserID.String())
	if err != nil {
		return time.Time{}, err
	}
	if user == nil || user.Status != "active" || user.PasswordHash == "" {
		return time.Time{}, ErrUnauthorized
	}

	ok, err := VerifyPassword(in.Password, user.PasswordHash)
	if err != nil || !ok {
		return time.Time{}, ErrInvalidCredentials
	}
	if user.MFARequired {
		if strings.TrimSpace(in.TOTPCode) == "" {
			return time.Time{}, ErrMFARequired
		}
		if !s.verifyTOTP(user, in.TOTPCode) {
			return time.Time{}, ErrInvalidCredentials
		}
	}

	var at time.Time
	err = db.WithActor(ctx, s.pool, db.Actor{UserID: p.UserID.String(), Scopes: p.Scopes}, in.RequestID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT pamoja.auth_set_step_up($1::uuid)`,
			p.SessionID.String()).Scan(&at); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrUnauthorized
			}
			return err
		}
		_, err := s.audit.Append(ctx, in.RequestID, audit.Entry{
			ActorUserID: p.UserID.String(),
			Action:      "auth.step_up",
			EntityType:  "session",
			EntityID:    p.SessionID.String(),
			Channel:     "self",
		})
		return err
	})
	if err != nil {
		return time.Time{}, err
	}
	return at, nil
}

func (s *Service) openSession(ctx context.Context, user *userRecord, mfaVerified bool, in LoginInput) (Tokens, error) {
	refreshToken, err := newRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	familyID := uuid.NewString()
	now := s.now()
	absoluteExpiry := now.Add(s.cfg.RefreshTTL)

	roles, scopes, err := s.rolesAndScopes(ctx, user.ID)
	if err != nil {
		return Tokens{}, err
	}

	var sessionID string
	err = db.WithRequest(ctx, s.pool, in.RequestID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pamoja.auth_record_success($1::uuid)`, user.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`SELECT pamoja.auth_revoke_oldest_sessions($1::uuid, $2)`,
			user.ID, s.cfg.MaxDevices); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT pamoja.auth_create_session($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10)::text`,
			user.ID, familyID, hashRefresh(refreshToken), in.DeviceID, in.UserAgent,
			in.IP, absoluteExpiry, absoluteExpiry, in.SharedPhone, mfaVerified).Scan(&sessionID); err != nil {
			return err
		}
		_, err := s.audit.Append(ctx, in.RequestID, audit.Entry{
			ActorUserID: user.ID,
			Action:      "auth.login",
			EntityType:  "session",
			EntityID:    sessionID,
			After:       audit.Redact(map[string]any{"is_staff": user.IsStaff, "mfa": mfaVerified}),
			IP:          in.IP,
			DeviceID:    in.DeviceID,
		})
		return err
	})
	if err != nil {
		return Tokens{}, err
	}

	accessToken, _, err := s.tokens.issue(user.ID, sessionID, roles, scopes, mfaVerified)
	if err != nil {
		return Tokens{}, err
	}

	return Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(s.cfg.AccessTTL.Seconds()),
		SessionID:    sessionID,
		UserID:       user.ID,
		IsStaff:      user.IsStaff,
		MFARequired:  user.MFARequired,
	}, nil
}

func (s *Service) findUser(ctx context.Context, phoneHash string) (*userRecord, error) {
	var user userRecord
	var passwordHash *string
	var totpEnc []byte
	var confirmedAt *time.Time

	err := s.pool.QueryRow(ctx,
		`SELECT user_id::text, status, is_staff, mfa_required, locked_until,
		        COALESCE(password_hash, ''), totp_secret_enc, totp_confirmed_at
		 FROM pamoja.auth_find_user($1)`, phoneHash).
		Scan(&user.ID, &user.Status, &user.IsStaff, &user.MFARequired,
			&user.LockedUntil, &passwordHash, &totpEnc, &confirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if passwordHash != nil {
		user.PasswordHash = *passwordHash
	}
	user.TOTPSecretEnc = totpEnc
	user.TOTPConfirmedAt = confirmedAt
	return &user, nil
}

func (s *Service) userSecrets(ctx context.Context, userID string) (*userRecord, error) {
	var user userRecord
	var passwordHash *string
	var totpEnc []byte
	var confirmedAt *time.Time

	err := s.pool.QueryRow(ctx,
		`SELECT user_id::text, status, mfa_required,
		        COALESCE(password_hash, ''), totp_secret_enc, totp_confirmed_at
		 FROM pamoja.auth_user_secrets($1::uuid)`, userID).
		Scan(&user.ID, &user.Status, &user.MFARequired, &passwordHash, &totpEnc, &confirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if passwordHash != nil {
		user.PasswordHash = *passwordHash
	}
	user.TOTPSecretEnc = totpEnc
	user.TOTPConfirmedAt = confirmedAt
	return &user, nil
}

func (s *Service) sessionByRefresh(ctx context.Context, refreshHash string) (*sessionRecord, error) {
	var session sessionRecord
	err := s.pool.QueryRow(ctx,
		`SELECT session_id::text, user_id::text, family_id::text, status,
		        is_staff, mfa_required, shared_phone, expires_at,
		        absolute_expires_at, revoked_at, revoked_reason, last_seen_at,
		        mfa_verified_at, step_up_at
		 FROM pamoja.auth_session_by_refresh($1)`, refreshHash).
		Scan(&session.ID, &session.UserID, &session.FamilyID, &session.Status,
			&session.IsStaff, &session.MFARequired, &session.SharedPhone,
			&session.ExpiresAt, &session.AbsoluteExpiresAt, &session.RevokedAt,
			&session.RevokedReason, &session.LastSeenAt,
			&session.MFAVerifiedAt, &session.StepUpAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *Service) principal(ctx context.Context, sessionID string) (*sessionRecord, error) {
	var session sessionRecord
	err := s.pool.QueryRow(ctx,
		`SELECT session_id::text, user_id::text, family_id::text, status,
		        is_staff, mfa_required, shared_phone, mfa_verified_at,
		        step_up_at, expires_at, revoked_at
		 FROM pamoja.auth_principal($1::uuid)`, sessionID).
		Scan(&session.ID, &session.UserID, &session.FamilyID, &session.Status,
			&session.IsStaff, &session.MFARequired, &session.SharedPhone,
			&session.MFAVerifiedAt, &session.StepUpAt, &session.ExpiresAt,
			&session.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *Service) rolesAndScopes(ctx context.Context, userID string) ([]string, []string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT role, legal_entity_id::text, branch_id::text, group_id::text
		 FROM pamoja.auth_roles($1::uuid)`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	roleSet := map[string]bool{}
	scopeSet := map[string]bool{}
	for rows.Next() {
		var role string
		var legalEntity, branch, group *string
		if err := rows.Scan(&role, &legalEntity, &branch, &group); err != nil {
			return nil, nil, err
		}
		roleSet[role] = true
		for _, scope := range []*string{legalEntity, branch, group} {
			if scope != nil && *scope != "" {
				scopeSet[*scope] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	roles := make([]string, 0, len(roleSet))
	for role := range roleSet {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	scopes := make([]string, 0, len(scopeSet))
	for scope := range scopeSet {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)

	return roles, scopes, nil
}

func (s *Service) verifyTOTP(user *userRecord, code string) bool {
	if len(user.TOTPSecretEnc) == 0 || user.TOTPConfirmedAt == nil {
		s.log.Error("mfa required but no confirmed totp secret")
		return false
	}
	secret, err := s.keys.Decrypt(user.TOTPSecretEnc)
	if err != nil {
		s.log.Error("decrypt totp secret", "error", err.Error())
		return false
	}
	return VerifyTOTP(string(secret), code, s.now())
}

func (s *Service) recordFailure(ctx context.Context, user *userRecord, in LoginInput) {
	err := db.WithRequest(ctx, s.pool, in.RequestID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pamoja.auth_register_failure($1::uuid, $2, $3, $4)`,
			user.ID, s.cfg.LockoutFailures,
			int(s.cfg.LockoutBase.Seconds()), int(s.cfg.LockoutMax.Seconds())); err != nil {
			return err
		}
		_, err := s.audit.Append(ctx, in.RequestID, audit.Entry{
			ActorUserID: user.ID,
			Action:      "auth.login_failed",
			EntityType:  "user",
			EntityID:    user.ID,
			IP:          in.IP,
			DeviceID:    in.DeviceID,
		})
		return err
	})
	if err != nil {
		s.log.Error("record failed login", "error", err.Error())
	}
}

func (s *Service) recordUnknownFailure(ctx context.Context, in LoginInput) {
	if _, err := s.audit.Append(ctx, in.RequestID, audit.Entry{
		Action:   "auth.login_failed",
		Reason:   "unknown_account",
		IP:       in.IP,
		DeviceID: in.DeviceID,
	}); err != nil {
		s.log.Error("record unknown-account login", "error", err.Error())
	}
}

func (s *Service) revokeFamily(ctx context.Context, familyID, reason, requestID string) error {
	return db.WithRequest(ctx, s.pool, requestID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pamoja.auth_revoke_family($1::uuid, $2)`,
			familyID, reason); err != nil {
			return err
		}
		_, err := s.audit.Append(ctx, requestID, audit.Entry{
			Action:     "auth.session_revoked",
			EntityType: "session_family",
			EntityID:   familyID,
			Reason:     reason,
			Channel:    "system",
		})
		return err
	})
}

func newRefreshToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashRefresh(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
