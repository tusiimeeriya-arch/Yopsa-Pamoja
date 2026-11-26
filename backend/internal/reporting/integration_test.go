package reporting_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/reporting"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

type fixture struct {
	owner   *pgxpool.Pool
	app     *pgxpool.Pool
	tenant  testsupport.Tenant
	actor   string
	service *reporting.Service
}

func setupFixture(t *testing.T) fixture {
	t.Helper()
	owner, app := testsupport.Pools(t)
	tenant := testsupport.CreateTenant(t, owner, "Reporting group")
	f := fixture{owner: owner, app: app, tenant: tenant, service: reporting.New(),
		actor: testsupport.CreateUser(t, owner, "Manager")}
	testsupport.CreateMember(t, owner, tenant, "Reporting Member", "")
	return f
}

func (f fixture) run(t *testing.T, fn func(context.Context, pgx.Tx) error) error {
	t.Helper()
	return db.WithActor(context.Background(), f.app,
		db.Actor{UserID: f.actor, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(), fn)
}

// T-DASH-01: an empty group reports zeros rather than inventing figures.
func TestDashboardOnEmptyGroup(t *testing.T) {
	f := setupFixture(t)
	var dash reporting.Dashboard
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		dash, err = f.service.Dashboard(ctx, tx, f.tenant.GroupID)
		return err
	})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if len(dash.Cards) != 7 {
		t.Fatalf("expected seven cards, got %d", len(dash.Cards))
	}
	byKey := map[string]reporting.Card{}
	for _, c := range dash.Cards {
		byKey[c.Key] = c
	}
	if byKey["members"].Value != 1 {
		t.Fatalf("active members not counted: %+v", byKey["members"])
	}
	for _, key := range []string{"attendance_rate", "savings_ugx", "loans_outstanding_ugx", "arrears_ugx", "pending_verifications", "finance_exceptions"} {
		if byKey[key].Value != 0 {
			t.Fatalf("%s must be zero on an empty group, got %v", key, byKey[key].Value)
		}
	}
	if byKey["arrears_ugx"].DrillDown != "arrears" {
		t.Fatalf("card does not name its drill-down: %+v", byKey["arrears_ugx"])
	}
}

// T-DASH-02: a drill-down returns the rows behind the card and refuses an
// unknown card.
func TestDrillDown(t *testing.T) {
	f := setupFixture(t)
	var rows []map[string]any
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		rows, err = f.service.DrillDown(ctx, tx, f.tenant.GroupID, "members", 100)
		return err
	})
	if err != nil {
		t.Fatalf("drill-down: %v", err)
	}
	if len(rows) != 1 || rows[0]["full_name"] != "Reporting Member" {
		t.Fatalf("member drill-down wrong: %+v", rows)
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.DrillDown(ctx, tx, f.tenant.GroupID, "not_a_card", 100)
		return err
	})
	if err == nil || !domain.IsValidation(err) {
		t.Fatalf("an unknown card must be rejected, got %v", err)
	}
}

// T-EXP-01: an export records its row count, hash and exact content, and the
// stored bytes hash to the recorded value.
func TestExportIsAudited(t *testing.T) {
	f := setupFixture(t)
	var export reporting.Export
	var content string
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		export, content, err = f.service.Generate(ctx, tx, f.tenant.GroupID, "members", nil, f.actor)
		return err
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if export.RowCount != 1 {
		t.Fatalf("row count wrong: %+v", export)
	}
	sum := sha256.Sum256([]byte(content))
	if export.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("recorded hash does not match the content")
	}

	var stored reporting.Export
	var storedContent string
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		stored, storedContent, err = f.service.GetExport(ctx, tx, export.ID)
		return err
	})
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if storedContent != content || stored.SHA256 != export.SHA256 {
		t.Fatalf("stored export differs from what was generated")
	}

	// A recorded export is immutable evidence.
	if _, err := f.owner.Exec(context.Background(),
		`UPDATE pamoja.report_export SET row_count = 0 WHERE id = $1::uuid`, export.ID); err == nil {
		t.Fatal("an export must not be updatable")
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := f.service.Generate(ctx, tx, f.tenant.GroupID, "not_a_report", nil, f.actor)
		return err
	})
	if err == nil || !domain.IsValidation(err) {
		t.Fatalf("an unknown report kind must be rejected, got %v", err)
	}
}

// T-RANK-05: a snapshot is stored once per period, is readable afterwards and
// cannot be rewritten.
func TestRankingSnapshot(t *testing.T) {
	f := setupFixture(t)
	var first reporting.Snapshot
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		first, err = f.service.Snapshot(ctx, tx, f.tenant.GroupID, f.actor)
		return err
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// No loans, no meetings: only the inverted arrears weight contributes.
	if first.Score != 25 {
		t.Fatalf("unexpected score for an inactive group: %v", first.Score)
	}
	if first.RankInBranch != 1 {
		t.Fatalf("first snapshot in a branch must rank 1, got %d", first.RankInBranch)
	}
	if eligible, _ := first.Components["eligible"].(bool); eligible {
		t.Fatalf("a group with no loans or meetings is not eligible: %+v", first.Components)
	}

	var second reporting.Snapshot
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		second, err = f.service.Snapshot(ctx, tx, f.tenant.GroupID, f.actor)
		return err
	})
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("a second snapshot in the same period must return the stored row")
	}

	var latest reporting.Snapshot
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		latest, err = f.service.LatestSnapshot(ctx, tx, f.tenant.GroupID)
		return err
	})
	if err != nil || latest.ID != first.ID {
		t.Fatalf("latest snapshot: %v (%+v)", err, latest)
	}

	if _, err := f.owner.Exec(context.Background(),
		`UPDATE pamoja.ranking_snapshot SET score = 0 WHERE id = $1::uuid`, first.ID); err == nil {
		t.Fatal("a snapshot must not be updatable")
	}

	other := testsupport.CreateTenant(t, f.owner, "No snapshot group")
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.LatestSnapshot(ctx, tx, other.GroupID)
		return err
	})
	if !errors.Is(err, reporting.ErrNoSnapshot) {
		t.Fatalf("a group without a snapshot must report ErrNoSnapshot, got %v", err)
	}
}
