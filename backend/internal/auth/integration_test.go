package auth

// Database integration tests. They run when TEST_DATABASE_URL points at a
// PostgreSQL 16 instance where the connecting user may create roles (CI and
// the development stack provide one). Without it they skip, so `go test ./...`
// still works on a workstation with no database.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/platform/config"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/platform/keyring"
)

const appRolePassword = "pamoja_test_app_password"

var (
	setupOnce sync.Once
	ownerPool *pgxpool.Pool
	appPool   *pgxpool.Pool
	setupErr  error
)

func testPools(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping database integration tests")
	}
	setupOnce.Do(func() { setupErr = setupDatabase(dsn) })
	if setupErr != nil {
		t.Fatalf("database setup: %v", setupErr)
	}
	return ownerPool, appPool
}

func setupDatabase(dsn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The migrations contain multiple statements, so apply them over the simple
	// protocol.
	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return err
	}
	connConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	for _, name := range []string{"000001_baseline.up.sql", "000002_p1_auth_rls.up.sql"} {
		path := filepath.Join("..", "..", "db", "migrations", name)
		script, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if _, execErr := conn.Exec(ctx, string(script)); execErr != nil {
			return fmt.Errorf("apply %s: %w", name, execErr)
		}
	}
	if _, err := conn.Exec(ctx, "ALTER ROLE pamoja_app PASSWORD '"+appRolePassword+"'"); err != nil {
		return err
	}

	ownerPool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}

	appDSN, err := appRoleDSN(dsn)
	if err != nil {
		return err
	}
	appPool, err = pgxpool.New(ctx, appDSN)
	if err != nil {
		return err
	}

	return nil
}

func appRoleDSN(dsn string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword("pamoja_app", appRolePassword)
	return parsed.String(), nil
}

func serviceConfig() config.Auth {
	return config.Auth{
		AccessTTL:              15 * time.Minute,
		RefreshTTL:             14 * 24 * time.Hour,
		IdleStaff:              30 * time.Minute,
		IdleSharedPhone:        5 * time.Minute,
		StepUpWindow:           5 * time.Minute,
		LockoutFailures:        5,
		LockoutBase:            time.Minute,
		LockoutMax:             15 * time.Minute,
		MaxDevices:             5,
		LoginPerIPPerMinute:    1000,
		LoginPerAccountPerHour: 1000,
		Issuer:                 "pamoja-api",
		Audience:               "pamoja-client",
		TokenSecret:            "integration-token-secret-0123456789",
		LookupKey:              "integration-lookup-key",
		MasterKey:              "integration-master-key",
	}
}

func newTestService(t *testing.T, app *pgxpool.Pool) *Service {
	t.Helper()
	keys, err := keyring.NewLocal("integration-master-key", "integration-lookup-key")
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return NewService(Options{
		Pool:   app,
		Config: serviceConfig(),
		Env:    "development",
		Keys:   keys,
		Audit:  audit.New(app),
	})
}

type testUser struct {
	ID       string
	Phone    string
	Password string
	TOTP     string
}

func createTestUser(t *testing.T, owner *pgxpool.Pool, mfa bool) testUser {
	t.Helper()
	ctx := context.Background()

	keys, err := keyring.NewLocal("integration-master-key", "integration-lookup-key")
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	user := testUser{
		ID:       uuid.NewString(),
		Phone:    "+2567" + fmt.Sprintf("%08d", time.Now().UnixNano()%100000000),
		Password: "integration-test-password",
	}
	phoneHash := keys.LookupHash(NormalizePhone(user.Phone))

	if _, err := owner.Exec(ctx,
		`INSERT INTO pamoja.app_user (id, full_name, phone_hash, is_staff, mfa_required)
		 VALUES ($1::uuid, $2, $3, true, $4)`,
		user.ID, "Integration Test User", phoneHash, mfa); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	hash, params, err := HashPassword(user.Password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	paramsJSON := jsonParams(params)
	if _, err := owner.Exec(ctx,
		`INSERT INTO pamoja.user_credential (user_id, password_hash, argon2_params)
		 VALUES ($1::uuid, $2, $3::jsonb)`,
		user.ID, hash, paramsJSON); err != nil {
		t.Fatalf("insert credential: %v", err)
	}

	if mfa {
		secret, err := GenerateTOTPSecret()
		if err != nil {
			t.Fatalf("generate totp: %v", err)
		}
		sealed, err := keys.Encrypt([]byte(secret))
		if err != nil {
			t.Fatalf("encrypt totp: %v", err)
		}
		if _, err := owner.Exec(ctx,
			`INSERT INTO pamoja.totp_secret (user_id, secret_enc, confirmed_at)
			 VALUES ($1::uuid, $2, now())`,
			user.ID, sealed); err != nil {
			t.Fatalf("insert totp: %v", err)
		}
		user.TOTP = secret
	}

	if _, err := owner.Exec(ctx,
		`INSERT INTO pamoja.role_grant (user_id, role, group_id)
		 VALUES ($1::uuid, 'treasurer', $2::uuid)`,
		user.ID, uuid.NewString()); err != nil {
		t.Fatalf("insert role grant: %v", err)
	}

	t.Cleanup(func() {
		// Deleting the user cascades credentials, sessions and grants. Audit
		// rows are append-only by design and stay.
		if _, err := owner.Exec(context.Background(),
			`DELETE FROM pamoja.app_user WHERE id = $1::uuid`, user.ID); err != nil {
			t.Logf("cleanup user %s: %v", user.ID, err)
		}
	})

	return user
}

func jsonParams(params ArgonParams) string {
	return fmt.Sprintf(`{"m":%d,"t":%d,"p":%d,"k":%d,"s":%d}`,
		params.Memory, params.Iterations, params.Parallelism,
		params.KeyLength, params.SaltLength)
}

func currentTOTP(t *testing.T, secret string) string {
	t.Helper()
	key, err := totpEncoding.DecodeString(secret)
	if err != nil {
		t.Fatalf("decode totp secret: %v", err)
	}
	return totpCode(key, uint64(time.Now().Unix()/totpPeriod))
}

func TestRLSWithoutActorSeesNothing(t *testing.T) {
	owner, app := testPools(t)
	// Existing rows must stay invisible to an actor-less connection.
	createTestUser(t, owner, false)
	ctx := context.Background()

	var users int
	if err := app.QueryRow(ctx, `SELECT count(*) FROM pamoja.app_user`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 0 {
		t.Fatalf("app role without an actor saw %d users, want 0", users)
	}

	var sessions int
	if err := app.QueryRow(ctx, `SELECT count(*) FROM pamoja.session`).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("app role without an actor saw %d sessions, want 0", sessions)
	}

	var scopes []string
	if err := app.QueryRow(ctx, `SELECT pamoja.current_scopes()`).Scan(&scopes); err != nil {
		t.Fatalf("current_scopes: %v", err)
	}
	if len(scopes) != 0 {
		t.Fatalf("current_scopes = %v, want empty", scopes)
	}
}

func TestRLSScopesToActor(t *testing.T) {
	owner, app := testPools(t)
	first := createTestUser(t, owner, false)
	second := createTestUser(t, owner, false)

	var visible []string
	err := db.WithActor(context.Background(), app,
		db.Actor{UserID: first.ID, Scopes: []string{"scope-a", "scope-b"}},
		"req-rls", func(ctx context.Context, tx pgx.Tx) error {
			var scopes []string
			if err := tx.QueryRow(ctx, `SELECT pamoja.current_scopes()`).Scan(&scopes); err != nil {
				return err
			}
			if len(scopes) != 2 {
				t.Errorf("current_scopes = %v, want two ids", scopes)
			}

			rows, err := tx.Query(ctx, `SELECT id::text FROM pamoja.app_user ORDER BY id`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return err
				}
				visible = append(visible, id)
			}
			return rows.Err()
		})
	if err != nil {
		t.Fatalf("actor transaction: %v", err)
	}

	if len(visible) != 1 || visible[0] != first.ID {
		t.Fatalf("visible users = %v, want only %s (not %s)", visible, first.ID, second.ID)
	}
}

func TestAuditIsAppendOnlyAndChained(t *testing.T) {
	owner, app := testPools(t)
	ctx := context.Background()

	recorder := audit.New(app)
	for i := 0; i < 3; i++ {
		if _, err := recorder.Append(ctx, "req-audit", audit.Entry{
			Action:     "auth.test",
			EntityType: "test",
			EntityID:   uuid.NewString(),
			After:      audit.Redact(map[string]any{"step": i, "password": "never-stored"}),
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	result, err := recorder.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.Intact() || result.Checked < 3 {
		t.Fatalf("verify = %+v, want at least three intact rows", result)
	}

	// The application role cannot rewrite or delete an audit row.
	if _, err := app.Exec(ctx, `UPDATE pamoja.audit_log SET action = 'tampered'`); err == nil {
		t.Fatal("app role rewrote an audit row")
	}
	if _, err := app.Exec(ctx, `DELETE FROM pamoja.audit_log`); err == nil {
		t.Fatal("app role deleted an audit row")
	}

	// Nor can the owner: the append-only trigger rejects the statement.
	if _, err := owner.Exec(ctx, `UPDATE pamoja.audit_log SET action = 'tampered'`); err == nil {
		t.Fatal("owner rewrote an audit row despite the append-only trigger")
	}

	// Simulate a tamper that bypasses the trigger inside a transaction, prove
	// the verifier detects it, then roll back.
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var victim int64
	if err := tx.QueryRow(ctx, `SELECT seq FROM pamoja.audit_log ORDER BY seq LIMIT 1`).Scan(&victim); err != nil {
		t.Fatalf("pick audit row: %v", err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE pamoja.audit_log DISABLE TRIGGER audit_log_append_only`); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE pamoja.audit_log SET action = 'tampered' WHERE seq = $1`, victim); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	var checked int64
	var firstBad *int64
	var reason *string
	if err := tx.QueryRow(ctx,
		`SELECT checked, first_bad_seq, reason FROM pamoja.audit_verify(1)`).
		Scan(&checked, &firstBad, &reason); err != nil {
		t.Fatalf("verify tampered chain: %v", err)
	}
	if firstBad == nil || *firstBad != victim {
		t.Fatalf("first bad seq = %v, want %d", firstBad, victim)
	}
}

func TestLoginRefreshReuseAndLogout(t *testing.T) {
	owner, app := testPools(t)
	user := createTestUser(t, owner, true)
	svc := newTestService(t, app)
	ctx := context.Background()

	login := func() Tokens {
		t.Helper()
		tokens, err := svc.Login(ctx, LoginInput{
			Phone:     user.Phone,
			Password:  user.Password,
			TOTPCode:  currentTOTP(t, user.TOTP),
			DeviceID:  "test-device",
			IP:        "127.0.0.1",
			RequestID: "req-login",
		})
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		return tokens
	}

	tokens := login()
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.SessionID == "" {
		t.Fatalf("incomplete tokens: %+v", tokens)
	}

	principal, err := svc.Authenticate(ctx, tokens.AccessToken)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if principal.UserID.String() != user.ID {
		t.Fatalf("principal user = %s, want %s", principal.UserID, user.ID)
	}
	if len(principal.Roles) != 1 || principal.Roles[0] != "treasurer" || len(principal.Scopes) != 1 {
		t.Fatalf("principal roles/scopes = %v/%v", principal.Roles, principal.Scopes)
	}
	if !principal.MFAVerified {
		t.Fatal("principal should be marked MFA-verified")
	}

	rotated, err := svc.Refresh(ctx, RefreshInput{
		RefreshToken: tokens.RefreshToken,
		DeviceID:     "test-device",
		IP:           "127.0.0.1",
		RequestID:    "req-refresh",
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.RefreshToken == tokens.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	// Reusing the old token is treated as theft: the whole family is revoked.
	if _, err := svc.Refresh(ctx, RefreshInput{
		RefreshToken: tokens.RefreshToken,
		DeviceID:     "test-device",
		IP:           "127.0.0.1",
		RequestID:    "req-reuse",
	}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("reuse err = %v, want ErrInvalidGrant", err)
	}
	if _, err := svc.Refresh(ctx, RefreshInput{
		RefreshToken: rotated.RefreshToken,
		DeviceID:     "test-device",
		IP:           "127.0.0.1",
		RequestID:    "req-after-reuse",
	}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("rotated token after reuse err = %v, want ErrInvalidGrant", err)
	}
	if _, err := svc.Authenticate(ctx, rotated.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("access token after family revocation err = %v, want ErrUnauthorized", err)
	}

	// A fresh session can be stepped up, listed and logged out.
	fresh := login()
	at, err := svc.StepUp(ctx, &Principal{
		UserID:    mustUUID(t, fresh.UserID),
		SessionID: mustUUID(t, fresh.SessionID),
	}, StepUpInput{
		Password:  user.Password,
		TOTPCode:  currentTOTP(t, user.TOTP),
		RequestID: "req-step-up",
	})
	if err != nil {
		t.Fatalf("step-up: %v", err)
	}
	if time.Since(at) > time.Minute {
		t.Fatalf("step-up time = %v, want now", at)
	}

	principal, err = svc.Authenticate(ctx, fresh.AccessToken)
	if err != nil {
		t.Fatalf("authenticate after step-up: %v", err)
	}
	if !principal.StepUpFresh(5*time.Minute, time.Now()) {
		t.Fatal("principal should have a fresh step-up")
	}

	sessions, err := svc.ListSessions(ctx, &principal, "req-sessions")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("no sessions listed")
	}

	if err := svc.Logout(ctx, &principal, "req-logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.Authenticate(ctx, fresh.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("access token after logout err = %v, want ErrUnauthorized", err)
	}
}

func TestLoginLockoutAndGenericFailures(t *testing.T) {
	owner, app := testPools(t)
	user := createTestUser(t, owner, false)
	svc := newTestService(t, app)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.Login(ctx, LoginInput{
			Phone:     user.Phone,
			Password:  "wrong-password",
			IP:        "10.0.0." + fmt.Sprint(i+1),
			RequestID: "req-fail",
		}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d err = %v, want ErrInvalidCredentials", i+1, err)
		}
	}

	// The account is now locked: even the right password is refused with the
	// same generic error.
	if _, err := svc.Login(ctx, LoginInput{
		Phone:     user.Phone,
		Password:  user.Password,
		IP:        "10.0.0.99",
		RequestID: "req-locked",
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("locked login err = %v, want ErrInvalidCredentials", err)
	}

	// An unknown account fails identically.
	if _, err := svc.Login(ctx, LoginInput{
		Phone:     "+256799999999",
		Password:  "whatever",
		IP:        "10.0.0.100",
		RequestID: "req-unknown",
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown login err = %v, want ErrInvalidCredentials", err)
	}
}

func mustUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", raw, err)
	}
	return id
}
