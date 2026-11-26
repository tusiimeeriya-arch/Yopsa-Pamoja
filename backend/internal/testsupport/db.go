// Package testsupport provides the PostgreSQL harness shared by integration
// tests. It applies every migration in order, creates the least-privilege app
// role password, and returns an owner pool plus an RLS-enforced app pool.
//
// Tests skip when TEST_DATABASE_URL is unset, so `go test ./...` still works on
// a workstation with no database. CI and the dev stack set it.
package testsupport

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AppRolePassword matches the development stack so running integration tests
// against the same PostgreSQL cluster never changes the password the live
// preview API uses.
const AppRolePassword = "pamoja_app_dev_password"

var (
	once      sync.Once
	ownerPool *pgxpool.Pool
	appPool   *pgxpool.Pool
	setupErr  error
)

// Pools returns the owner and application pools, applying migrations once.
func Pools(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping database integration tests")
	}
	once.Do(func() { setupErr = setup(dsn) })
	if setupErr != nil {
		t.Fatalf("database setup: %v", setupErr)
	}
	return ownerPool, appPool
}

func setup(dsn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

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

	// Serialise migration application across test packages and processes.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(918273645)`); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock(918273645)`) }()

	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.up.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, path := range files {
		script, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if _, execErr := conn.Exec(ctx, string(script)); execErr != nil {
			return fmt.Errorf("apply %s: %w", filepath.Base(path), execErr)
		}
	}
	// The preview API shares this cluster, so only rewrite the cluster-wide role
	// password when the expected one does not work. A needless ALTER races a
	// live connection and shows up as a transient authentication failure.
	if err := ensureAppPassword(ctx, conn, dsn); err != nil {
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

// ensureAppPassword makes sure pamoja_app can log in with AppRolePassword and
// leaves the role untouched when it already can.
func ensureAppPassword(ctx context.Context, owner *pgx.Conn, dsn string) error {
	appDSN, err := appRoleDSN(dsn)
	if err != nil {
		return err
	}
	probe, err := pgx.Connect(ctx, appDSN)
	if err == nil {
		return probe.Close(ctx)
	}
	_, err = owner.Exec(ctx, "ALTER ROLE pamoja_app PASSWORD '"+AppRolePassword+"'")
	return err
}

func appRoleDSN(dsn string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword("pamoja_app", AppRolePassword)
	return parsed.String(), nil
}

// Tenant is a fresh legal entity, branch and group for one test.
type Tenant struct {
	LegalEntityID string
	BranchID      string
	GroupID       string
}

// CreateTenant inserts an isolated tenant. The caller owns cleanup of its own
// records and may simply leave the demo rows; no shared row is mutated.
func CreateTenant(t *testing.T, owner *pgxpool.Pool, name string) Tenant {
	t.Helper()
	ctx := context.Background()
	var out Tenant
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := owner.QueryRow(ctx,
		`INSERT INTO pamoja.legal_entity (code, name) VALUES ($1, $2) RETURNING id::text`,
		"LE-"+suffix, name).Scan(&out.LegalEntityID); err != nil {
		t.Fatalf("legal entity: %v", err)
	}
	if err := owner.QueryRow(ctx,
		`INSERT INTO pamoja.branch (legal_entity_id, code, name, district)
		 VALUES ($1::uuid, $2, $3, 'Kampala') RETURNING id::text`,
		out.LegalEntityID, "BR-"+suffix, name+" branch").Scan(&out.BranchID); err != nil {
		t.Fatalf("branch: %v", err)
	}
	if err := owner.QueryRow(ctx,
		`INSERT INTO pamoja.groups (legal_entity_id, branch_id, type_code, name, status)
		 VALUES ($1::uuid, $2::uuid, 'vsla', $3, 'active') RETURNING id::text`,
		out.LegalEntityID, out.BranchID, name).Scan(&out.GroupID); err != nil {
		t.Fatalf("group: %v", err)
	}
	return out
}

// CreateUser inserts an active staff user and returns its id.
func CreateUser(t *testing.T, owner *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := owner.QueryRow(context.Background(),
		`INSERT INTO pamoja.app_user (full_name, phone_hash, is_staff)
		 VALUES ($1, $2, true) RETURNING id::text`,
		name, "test-"+name+"-"+fmt.Sprintf("%d", time.Now().UnixNano())).Scan(&id); err != nil {
		t.Fatalf("user: %v", err)
	}
	return id
}

// CreateMember inserts a member and an active membership, optionally linked to
// an app user.
func CreateMember(t *testing.T, owner *pgxpool.Pool, tenant Tenant, name string, userID string) string {
	t.Helper()
	ctx := context.Background()
	var memberID string
	var user any
	if userID != "" {
		user = userID
	}
	if err := owner.QueryRow(ctx,
		`INSERT INTO pamoja.member (user_id, full_name, phone_hash)
		 VALUES ($1::uuid, $2, $3) RETURNING id::text`,
		user, name, "member-"+name+"-"+fmt.Sprintf("%d", time.Now().UnixNano())).Scan(&memberID); err != nil {
		t.Fatalf("member: %v", err)
	}
	if _, err := owner.Exec(ctx,
		`INSERT INTO pamoja.membership
		   (member_id, group_id, legal_entity_id, branch_id, member_no, joined_on)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, current_date - 365)`,
		memberID, tenant.GroupID, tenant.LegalEntityID, tenant.BranchID,
		"M-"+fmt.Sprintf("%d", time.Now().UnixNano())); err != nil {
		t.Fatalf("membership: %v", err)
	}
	return memberID
}
