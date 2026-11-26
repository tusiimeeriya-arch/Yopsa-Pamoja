package offline_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/offline"
	"github.com/yopsa/pamoja/backend/internal/payments"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

type fixture struct {
	owner   *pgxpool.Pool
	app     *pgxpool.Pool
	tenant  testsupport.Tenant
	actor   string
	member  string
	meeting string
	service *offline.Service
}

func setupFixture(t *testing.T) fixture {
	t.Helper()
	owner, app := testsupport.Pools(t)
	tenant := testsupport.CreateTenant(t, owner, "Offline group")
	f := fixture{
		owner: owner, app: app, tenant: tenant, service: offline.New(payments.New(nil)),
		actor: testsupport.CreateUser(t, owner, "Offline Officer"),
	}
	f.member = testsupport.CreateMember(t, owner, tenant, "Offline Member", "")
	if err := owner.QueryRow(context.Background(),
		`INSERT INTO pamoja.meeting
		   (legal_entity_id, branch_id, group_id, scheduled_for, status, created_by)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, now(), 'open', $4::uuid)
		 RETURNING id::text`,
		tenant.LegalEntityID, tenant.BranchID, tenant.GroupID, f.actor).Scan(&f.meeting); err != nil {
		t.Fatalf("meeting: %v", err)
	}
	return f
}

func (f fixture) submit(t *testing.T, ops []offline.OperationInput) offline.Result {
	t.Helper()
	var out offline.Result
	err := db.WithActor(context.Background(), f.app,
		db.Actor{UserID: f.actor, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(),
		func(ctx context.Context, tx pgx.Tx) error {
			var err error
			out, err = f.service.Submit(ctx, tx, offline.SubmitInput{
				GroupID: f.tenant.GroupID, ClientID: "device-1",
				ClientTime: time.Now().UTC().Add(90 * time.Second),
				ActorID:    f.actor, Operations: ops,
			})
			return err
		})
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}
	return out
}

// T-OFF-04: a queued payment and check-in are applied, the server time is
// returned with the skew, and replaying the batch changes nothing.
func TestBatchAppliesAndReplaysSafely(t *testing.T) {
	f := setupFixture(t)

	paymentPayload, _ := json.Marshal(map[string]any{
		"member_id": f.member, "category": "savings", "amount_ugx": 15000,
		"channel": "mobile_money", "source_ref": "MM-OFFLINE",
	})
	checkInPayload, _ := json.Marshal(map[string]any{
		"meeting_id": f.meeting, "member_id": f.member, "status": "present", "channel": "assisted",
	})
	// Keys are unique per run: the server's idempotency keys are global, so a
	// fixed key would replay the previous run's rows in a shared test database.
	ops := []offline.OperationInput{
		{Seq: 1, Kind: offline.KindPaymentEntry, IdempotencyKey: "batch1-op1-" + uuid.NewString(),
			ClientCreatedAt: time.Now().UTC(), Payload: paymentPayload},
		{Seq: 2, Kind: offline.KindAttendanceCheckIn, IdempotencyKey: "batch1-op2-" + uuid.NewString(),
			ClientCreatedAt: time.Now().UTC(), Payload: checkInPayload},
	}

	first := f.submit(t, ops)
	if first.Applied != 2 || first.Conflicts != 0 || first.Status != "completed" {
		t.Fatalf("batch did not apply cleanly: %+v", first)
	}
	if first.SkewMS < 60000 {
		t.Fatalf("the reported skew does not match the client clock: %d", first.SkewMS)
	}

	var entries, marks int
	if err := f.owner.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM pamoja.payment_entry WHERE group_id = $1::uuid),
		        (SELECT count(*) FROM pamoja.meeting_attendance WHERE group_id = $1::uuid)`,
		f.tenant.GroupID).Scan(&entries, &marks); err != nil {
		t.Fatalf("counts: %v", err)
	}
	if entries != 1 || marks != 1 {
		t.Fatalf("expected one entry and one attendance mark, got %d and %d", entries, marks)
	}

	// The same batch again: the stored outcomes are returned, nothing doubles.
	second := f.submit(t, ops)
	if second.Applied != 2 || second.Conflicts != 0 {
		t.Fatalf("replay did not reuse the stored outcomes: %+v", second)
	}
	if err := f.owner.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM pamoja.payment_entry WHERE group_id = $1::uuid),
		        (SELECT count(*) FROM pamoja.meeting_attendance WHERE group_id = $1::uuid)`,
		f.tenant.GroupID).Scan(&entries, &marks); err != nil {
		t.Fatalf("counts: %v", err)
	}
	if entries != 1 || marks != 1 {
		t.Fatalf("replay duplicated rows: %d entries, %d marks", entries, marks)
	}
}

// T-OFF-05: a change the server no longer accepts is reported as a conflict,
// not forced through.
func TestClosedRegisterIsAConflict(t *testing.T) {
	f := setupFixture(t)
	// A meeting that was already closed while the device was offline. The
	// transition guard forbids open -> closed directly, so it is created closed.
	if err := f.owner.QueryRow(context.Background(),
		`INSERT INTO pamoja.meeting
		   (legal_entity_id, branch_id, group_id, scheduled_for, status,
		    closed_by, closed_at, created_by)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, now() - interval '1 day', 'closed',
		         $4::uuid, now(), $4::uuid)
		 RETURNING id::text`,
		f.tenant.LegalEntityID, f.tenant.BranchID, f.tenant.GroupID, f.actor).Scan(&f.meeting); err != nil {
		t.Fatalf("closed meeting: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"meeting_id": f.meeting, "member_id": f.member, "status": "present", "channel": "assisted",
	})
	result := f.submit(t, []offline.OperationInput{{
		Seq: 1, Kind: offline.KindAttendanceCheckIn, IdempotencyKey: "late-op-" + uuid.NewString(),
		ClientCreatedAt: time.Now().UTC(), Payload: payload,
	}})
	if result.Conflicts != 1 || result.Applied != 0 || result.Status != "completed_with_conflicts" {
		t.Fatalf("a closed register must be a conflict: %+v", result)
	}
	if result.Operations[0].ConflictReason == "" {
		t.Fatalf("the conflict has no reason: %+v", result.Operations[0])
	}

	var marks int
	if err := f.owner.QueryRow(context.Background(),
		`SELECT count(*) FROM pamoja.meeting_attendance WHERE group_id = $1::uuid`,
		f.tenant.GroupID).Scan(&marks); err != nil {
		t.Fatalf("count marks: %v", err)
	}
	if marks != 0 {
		t.Fatalf("a conflicted check-in must not be written, found %d marks", marks)
	}
}
