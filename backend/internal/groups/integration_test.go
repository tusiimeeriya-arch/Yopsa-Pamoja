package groups

// Database integration tests. They run when TEST_DATABASE_URL points at a
// PostgreSQL 16 instance where the connecting user may create roles (CI and the
// development stack provide one). Without it they skip, so `go test ./...`
// still works on a workstation with no database.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/platform/keyring"
	"github.com/yopsa/pamoja/backend/internal/platform/phone"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

const appRolePassword = "pamoja_test_app_password"

var (
	setupOnce sync.Once
	ownerPool *pgxpool.Pool
	appPool   *pgxpool.Pool
	setupErr  error
)

func testPools(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping database integration tests")
	}
	setupOnce.Do(func() { setupErr = setupDatabase(dsn) })
	if setupErr != nil {
		t.Fatalf("database setup: %v", setupErr)
	}
	return ownerPool
}

func setupDatabase(dsn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	defer func() { _ = conn.Close(context.Background()) }()

	// Serialise migration application with the auth package's integration
	// tests, which may run in parallel in another test binary.
	if _, err := conn.Exec(ctx,
		`SELECT pg_advisory_lock(hashtext('pamoja-test-migrations'))`); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.Background(),
			`SELECT pg_advisory_unlock(hashtext('pamoja-test-migrations'))`)
	}()

	paths, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.up.sql"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		script, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if _, execErr := conn.Exec(ctx, string(script)); execErr != nil {
			return fmt.Errorf("apply %s: %w", filepath.Base(path), execErr)
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
	return err
}

func appRoleDSN(dsn string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword("pamoja_app", appRolePassword)
	return parsed.String(), nil
}

func testPhone() string {
	random := uuid.New()
	return fmt.Sprintf("+2567%08d", binary.BigEndian.Uint32(random[:4])%100000000)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	keys, err := keyring.NewLocal("integration-master-key", "integration-lookup-key")
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	service, err := NewService(Options{Pool: appPool, Keys: keys})
	if err != nil {
		t.Fatalf("groups service: %v", err)
	}
	return service
}

type fixture struct {
	t        *testing.T
	owner    *pgxpool.Pool
	entityID string
	branchID string
	userIDs  []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	owner := testPools(t)
	f := &fixture{t: t, owner: owner}
	suffix := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:6])
	f.entityID = uuid.NewString()
	f.mustExec(`INSERT INTO pamoja.legal_entity (id, code, name)
	            VALUES ($1::uuid, $2, 'Integration Test Entity')`, f.entityID, "E"+suffix)
	f.branchID = f.addBranch("B" + suffix)
	t.Cleanup(f.cleanup)
	return f
}

func (f *fixture) mustExec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.owner.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *fixture) addBranch(code string) string {
	f.t.Helper()
	id := uuid.NewString()
	f.mustExec(`INSERT INTO pamoja.branch (id, legal_entity_id, code, name, district)
	            VALUES ($1::uuid, $2::uuid, $3, 'Integration Branch', 'Kampala')`,
		id, f.entityID, code)
	return id
}

func (f *fixture) user(label string) string {
	f.t.Helper()
	keys, err := keyring.NewLocal("integration-master-key", "integration-lookup-key")
	if err != nil {
		f.t.Fatalf("keyring: %v", err)
	}
	random := uuid.New()
	number := fmt.Sprintf("+2567%08d", binary.BigEndian.Uint32(random[:4])%100000000)
	id := uuid.NewString()
	f.mustExec(`INSERT INTO pamoja.app_user (id, full_name, phone_hash, is_staff)
	            VALUES ($1::uuid, $2, $3, true)`,
		id, "Test "+label, keys.LookupHash(phone.Normalize(number)))
	f.userIDs = append(f.userIDs, id)
	return id
}

func (f *fixture) actor(userID string, bindings ...rbac.Binding) rbac.Actor {
	return rbac.Actor{UserID: userID, Bindings: bindings}
}

func (f *fixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	statements := []string{
		`WITH g AS (SELECT id FROM pamoja.groups WHERE legal_entity_id = $1::uuid),
		      mem AS (SELECT member_id FROM pamoja.membership WHERE group_id IN (SELECT id FROM g)),
		      d_auth AS (DELETE FROM pamoja.authority_record WHERE group_id IN (SELECT id FROM g)),
		      d_consent AS (DELETE FROM pamoja.consent_record WHERE member_id IN (SELECT member_id FROM mem)),
		      d_membership AS (DELETE FROM pamoja.membership WHERE group_id IN (SELECT id FROM g)),
		      d_member AS (DELETE FROM pamoja.member WHERE id IN (SELECT member_id FROM mem))
		 DELETE FROM pamoja.group_branch_history WHERE group_id IN (SELECT id FROM g)`,
		`DELETE FROM pamoja.groups WHERE legal_entity_id = $1::uuid`,
		`DELETE FROM pamoja.branch WHERE legal_entity_id = $1::uuid`,
		`DELETE FROM pamoja.legal_entity WHERE id = $1::uuid`,
	}
	for _, sql := range statements {
		if _, err := f.owner.Exec(ctx, sql, f.entityID); err != nil {
			f.t.Logf("cleanup: %v", err)
		}
	}
	for _, id := range f.userIDs {
		if _, err := f.owner.Exec(ctx, `DELETE FROM pamoja.app_user WHERE id = $1::uuid`, id); err != nil {
			f.t.Logf("cleanup user %s: %v", id, err)
		}
	}
}

func TestGroupRegistrationLifecycle(t *testing.T) {
	f := newFixture(t)
	service := newTestService(t)
	ctx := context.Background()

	officer := f.user("field officer")
	approver := f.user("credit approver")
	outsider := f.user("other branch officer")
	secondBranch := f.addBranch("B2-" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:5]))

	officerActor := f.actor(officer, rbac.Binding{
		Role: rbac.RoleFieldOfficer, Scope: rbac.ScopeBranch, ScopeIDs: []string{f.branchID}})
	approverActor := f.actor(approver, rbac.Binding{
		Role: rbac.RoleCreditApprover, Scope: rbac.ScopeBranch, ScopeIDs: []string{f.branchID}})
	outsiderActor := f.actor(outsider, rbac.Binding{
		Role: rbac.RoleFieldOfficer, Scope: rbac.ScopeBranch, ScopeIDs: []string{secondBranch}})

	group, err := service.CreateGroup(ctx, officerActor, CreateGroupInput{
		BranchID: f.branchID,
		TypeCode: "vsla",
		Name:     "Kampala Women's VSLA",
		FormedOn: "2026-01-15",
	}, "req-create")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if group.RegistrationStatus != "draft" || group.Version != 1 {
		t.Fatalf("new group = %+v", group)
	}

	if _, err := service.GetGroup(ctx, outsiderActor, group.ID, "req-outsider"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider GetGroup error = %v, want ErrNotFound", err)
	}

	evidence := uuid.NewString()
	group, err = service.UpdateGroup(ctx, officerActor, group.ID, UpdateGroupInput{
		RegistrationEvidenceID: &evidence,
		Version:                group.Version,
	}, "req-update")
	if err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}

	if _, err := service.SubmitGroup(ctx, officerActor, group.ID, group.Version, "req-submit-1"); !errors.Is(err, ErrValidation) {
		t.Fatalf("submit without members error = %v, want ErrValidation", err)
	}

	membership, err := service.AddMembership(ctx, officerActor, group.ID, AddMembershipInput{
		FullName: "Amina Nakato",
		Phone:    testPhone(),
		Gender:   "female",
		MemberNo: "M-001",
		JoinedOn: "2026-02-01",
	}, "req-member")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	if _, err := service.SubmitGroup(ctx, officerActor, group.ID, group.Version, "req-submit-2"); !errors.Is(err, ErrValidation) {
		t.Fatalf("submit without consent error = %v, want ErrValidation", err)
	}

	if _, err := service.RecordConsent(ctx, officerActor, membership.MemberID, RecordConsentInput{
		Purpose: "membership",
		Granted: true,
	}, "req-consent"); err != nil {
		t.Fatalf("RecordConsent: %v", err)
	}

	group, err = service.SubmitGroup(ctx, officerActor, group.ID, group.Version, "req-submit-3")
	if err != nil {
		t.Fatalf("SubmitGroup: %v", err)
	}
	if group.RegistrationStatus != "submitted" {
		t.Fatalf("status = %s, want submitted", group.RegistrationStatus)
	}

	if _, err := service.ReviewGroup(ctx, officerActor, group.ID, ReviewInput{
		Decision: "activate", Version: group.Version,
	}, "req-review-denied"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("officer review error = %v, want ErrForbidden", err)
	}

	group, err = service.ReviewGroup(ctx, approverActor, group.ID, ReviewInput{
		Decision: "activate", Version: group.Version,
	}, "req-review")
	if err != nil {
		t.Fatalf("ReviewGroup: %v", err)
	}
	if group.RegistrationStatus != "active" || group.Status != "active" {
		t.Fatalf("reviewed group = %+v", group)
	}
	if group.ReviewedBy == nil || *group.ReviewedBy != approver {
		t.Fatalf("reviewed_by = %v", group.ReviewedBy)
	}

	groups, err := service.ListGroups(ctx, officerActor, f.branchID, "", "req-list")
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].ID != group.ID {
		t.Fatalf("ListGroups = %+v", groups)
	}
	if _, err := service.ListGroups(ctx, outsiderActor, f.branchID, "", "req-list-denied"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider ListGroups error = %v, want ErrNotFound", err)
	}

	var auditActions int
	if err := f.owner.QueryRow(ctx,
		`SELECT count(DISTINCT action) FROM pamoja.audit_log
		 WHERE actor_user_id IN ($1::uuid, $2::uuid)
		   AND action IN (
		       'registration.group.create', 'registration.group.update',
		       'registration.group.submit', 'registration.group.review',
		       'registration.membership.create', 'registration.consent.record')`,
		officer, approver).Scan(&auditActions); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditActions != 6 {
		t.Fatalf("audited registration actions = %d, want 6", auditActions)
	}
}

func TestReviewerCannotActivateOwnGroup(t *testing.T) {
	f := newFixture(t)
	service := newTestService(t)
	ctx := context.Background()

	dual := f.user("dual role")
	dualActor := f.actor(dual,
		rbac.Binding{Role: rbac.RoleFieldOfficer, Scope: rbac.ScopeBranch, ScopeIDs: []string{f.branchID}},
		rbac.Binding{Role: rbac.RoleCreditApprover, Scope: rbac.ScopeBranch, ScopeIDs: []string{f.branchID}},
	)

	group, err := service.CreateGroup(ctx, dualActor, CreateGroupInput{
		BranchID: f.branchID, TypeCode: "sacco", Name: "Self Review SACCO",
		FormedOn: "2026-01-10", RegistrationEvidenceID: uuid.NewString(),
	}, "req-create")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	membership, err := service.AddMembership(ctx, dualActor, group.ID, AddMembershipInput{
		FullName: "John Okello", Phone: testPhone(), MemberNo: "M-001", JoinedOn: "2026-01-11",
	}, "req-member")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	if _, err := service.RecordConsent(ctx, dualActor, membership.MemberID, RecordConsentInput{
		Purpose: "membership", Granted: true,
	}, "req-consent"); err != nil {
		t.Fatalf("RecordConsent: %v", err)
	}
	group, err = service.SubmitGroup(ctx, dualActor, group.ID, group.Version, "req-submit")
	if err != nil {
		t.Fatalf("SubmitGroup: %v", err)
	}
	if _, err := service.ReviewGroup(ctx, dualActor, group.ID, ReviewInput{
		Decision: "activate", Version: group.Version,
	}, "req-self-review"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self review error = %v, want ErrForbidden (SD-06)", err)
	}
}

func TestMembershipAndAuthorityLifecycle(t *testing.T) {
	f := newFixture(t)
	service := newTestService(t)
	ctx := context.Background()

	officer := f.user("field officer")
	officerActor := f.actor(officer, rbac.Binding{
		Role: rbac.RoleFieldOfficer, Scope: rbac.ScopeBranch, ScopeIDs: []string{f.branchID}})

	group, err := service.CreateGroup(ctx, officerActor, CreateGroupInput{
		BranchID: f.branchID, TypeCode: "investment_club", Name: "Kampala Investors",
		FormedOn: "2026-01-05",
	}, "req-create")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	membership, err := service.AddMembership(ctx, officerActor, group.ID, AddMembershipInput{
		FullName: "Sarah Namusoke", Phone: testPhone(), MemberNo: "M-001",
		JoinedOn: "2026-01-06",
	}, "req-member")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	authority, err := service.AppointAuthority(ctx, officerActor, group.ID, AppointAuthorityInput{
		MemberID: membership.MemberID, Office: "chairperson", EffectiveFrom: "2026-01-07",
	}, "req-appoint")
	if err != nil {
		t.Fatalf("AppointAuthority: %v", err)
	}
	if authority.Office != "chairperson" || authority.MemberName != "Sarah Namusoke" {
		t.Fatalf("authority = %+v", authority)
	}
	if _, err := service.AppointAuthority(ctx, officerActor, group.ID, AppointAuthorityInput{
		MemberID: membership.MemberID, Office: "chairperson", EffectiveFrom: "2026-01-08",
	}, "req-duplicate"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate office error = %v, want ErrConflict", err)
	}
	if _, err := service.EndAuthority(ctx, officerActor, group.ID, authority.ID, EndAuthorityInput{
		EffectiveTo: "2026-02-01",
	}, "req-end-authority"); err != nil {
		t.Fatalf("EndAuthority: %v", err)
	}
	if _, err := service.AppointAuthority(ctx, officerActor, group.ID, AppointAuthorityInput{
		MemberID: membership.MemberID, Office: "chairperson", EffectiveFrom: "2026-02-02",
	}, "req-reappoint"); err != nil {
		t.Fatalf("re-appoint: %v", err)
	}

	if _, err := service.EndMembership(ctx, officerActor, group.ID, membership.ID, EndMembershipInput{
		LeftOn: "2026-03-01",
	}, "req-leave"); err != nil {
		t.Fatalf("EndMembership: %v", err)
	}
	listed, err := service.ListMemberships(ctx, officerActor, group.ID, "req-members")
	if err != nil {
		t.Fatalf("ListMemberships: %v", err)
	}
	if len(listed) != 1 || listed[0].Status != "left" || listed[0].LeftOn == nil {
		t.Fatalf("memberships = %+v", listed)
	}
}

func TestConsentUpsertKeepsOneRowPerNotice(t *testing.T) {
	f := newFixture(t)
	service := newTestService(t)
	ctx := context.Background()

	officer := f.user("field officer")
	officerActor := f.actor(officer, rbac.Binding{
		Role: rbac.RoleFieldOfficer, Scope: rbac.ScopeBranch, ScopeIDs: []string{f.branchID}})
	group, err := service.CreateGroup(ctx, officerActor, CreateGroupInput{
		BranchID: f.branchID, TypeCode: "association", Name: "Consent Association",
		FormedOn: "2026-01-05",
	}, "req-create")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	membership, err := service.AddMembership(ctx, officerActor, group.ID, AddMembershipInput{
		FullName: "Grace Auma", Phone: testPhone(), MemberNo: "M-001", JoinedOn: "2026-01-06",
	}, "req-member")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	for _, granted := range []bool{true, false} {
		if _, err := service.RecordConsent(ctx, officerActor, membership.MemberID, RecordConsentInput{
			Purpose: "membership", Granted: granted,
		}, "req-consent"); err != nil {
			t.Fatalf("RecordConsent(%v): %v", granted, err)
		}
	}
	var rows int
	var granted bool
	if err := f.owner.QueryRow(ctx,
		`SELECT count(*), bool_and(granted) FROM pamoja.consent_record WHERE member_id = $1::uuid`,
		membership.MemberID).Scan(&rows, &granted); err != nil {
		t.Fatalf("consent query: %v", err)
	}
	if rows != 1 || granted {
		t.Fatalf("consent rows=%d granted=%v, want one denied row", rows, granted)
	}
}

func TestSQLInjectionPayloadIsInert(t *testing.T) {
	f := newFixture(t)
	service := newTestService(t)
	ctx := context.Background()

	officer := f.user("field officer")
	officerActor := f.actor(officer, rbac.Binding{
		Role: rbac.RoleFieldOfficer, Scope: rbac.ScopeBranch, ScopeIDs: []string{f.branchID}})
	payload := "Robert'); DROP TABLE pamoja.groups;--"

	group, err := service.CreateGroup(ctx, officerActor, CreateGroupInput{
		BranchID: f.branchID, TypeCode: "vsla", Name: payload, FormedOn: "2026-01-05",
	}, "req-create")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if group.Name != payload {
		t.Fatalf("stored name = %q, want the literal payload", group.Name)
	}
	var tableExists bool
	if err := f.owner.QueryRow(ctx,
		`SELECT EXISTS (
		    SELECT 1 FROM pg_class c
		    JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = 'pamoja' AND c.relname = 'groups')`).Scan(&tableExists); err != nil {
		t.Fatalf("table check: %v", err)
	}
	if !tableExists {
		t.Fatal("groups table missing after injection payload")
	}
}

func TestRLSHidesRowsWithoutScopes(t *testing.T) {
	f := newFixture(t)
	service := newTestService(t)
	ctx := context.Background()

	officer := f.user("field officer")
	officerActor := f.actor(officer, rbac.Binding{
		Role: rbac.RoleFieldOfficer, Scope: rbac.ScopeBranch, ScopeIDs: []string{f.branchID}})
	if _, err := service.CreateGroup(ctx, officerActor, CreateGroupInput{
		BranchID: f.branchID, TypeCode: "vsla", Name: "Scoped Group", FormedOn: "2026-01-05",
	}, "req-create"); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	var visible int
	if err := appPool.QueryRow(ctx, `SELECT count(*) FROM pamoja.groups`).Scan(&visible); err != nil {
		t.Fatalf("direct count: %v", err)
	}
	if visible != 0 {
		t.Fatalf("rows visible without actor scopes = %d, want 0", visible)
	}
}

func TestGroupTypesAreSeeded(t *testing.T) {
	testPools(t)
	service := newTestService(t)
	types, err := service.ListGroupTypes(context.Background())
	if err != nil {
		t.Fatalf("ListGroupTypes: %v", err)
	}
	if len(types) < 5 {
		t.Fatalf("group types = %d, want at least 5", len(types))
	}
}
