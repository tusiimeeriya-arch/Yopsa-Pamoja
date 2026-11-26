package attendance_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

// T-ATT-05: a closed register is amended only through the controlled path
// (original, new, reason, reviewer, timestamp); the original row is retained.
func TestClosedRegisterAmendmentIsControlled(t *testing.T) {
	owner, app := testsupport.Pools(t)
	ctx := context.Background()
	tenant := testsupport.CreateTenant(t, owner, "Amendment group")
	secretary := testsupport.CreateUser(t, owner, "Secretary")
	reviewer := testsupport.CreateUser(t, owner, "Reviewer")
	member := testsupport.CreateMember(t, owner, tenant, "Member One", "")

	var meetingID string
	if err := owner.QueryRow(ctx,
		`INSERT INTO pamoja.meeting
		   (legal_entity_id, branch_id, group_id, scheduled_for, location, status, created_by, opened_by, opened_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, now(), 'Hall', 'open', $4::uuid, $4::uuid, now())
		 RETURNING id::text`,
		tenant.LegalEntityID, tenant.BranchID, tenant.GroupID, secretary).Scan(&meetingID); err != nil {
		t.Fatalf("meeting: %v", err)
	}

	var attendanceID string
	if err := owner.QueryRow(ctx,
		`INSERT INTO pamoja.meeting_attendance
		   (legal_entity_id, branch_id, group_id, meeting_id, member_id, status, checkin_channel, recorded_by)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'present', 'secretary', $6::uuid)
		 RETURNING id::text`,
		tenant.LegalEntityID, tenant.BranchID, tenant.GroupID, meetingID, member, secretary).Scan(&attendanceID); err != nil {
		t.Fatalf("attendance: %v", err)
	}

	if _, err := owner.Exec(ctx,
		`UPDATE pamoja.meeting SET status = 'register_confirmed', register_confirmed_by = $2::uuid,
		 register_confirmed_at = now() WHERE id = $1::uuid`, meetingID, secretary); err != nil {
		t.Fatalf("confirm register: %v", err)
	}
	if _, err := owner.Exec(ctx,
		`UPDATE pamoja.meeting_attendance SET confirmed_by = $2::uuid, confirmed_at = now()
		 WHERE id = $1::uuid`, attendanceID, secretary); err != nil {
		t.Fatalf("confirm attendance: %v", err)
	}
	if _, err := owner.Exec(ctx,
		`UPDATE pamoja.meeting SET status = 'closed', closed_by = $2::uuid, closed_at = now()
		 WHERE id = $1::uuid`, meetingID, secretary); err != nil {
		t.Fatalf("close meeting: %v", err)
	}

	// Editing the confirmed row is blocked by the database, not just the service.
	if _, err := owner.Exec(ctx,
		`UPDATE pamoja.meeting_attendance SET status = 'absent' WHERE id = $1::uuid`, attendanceID); err == nil {
		t.Fatal("editing a confirmed attendance row was allowed; the trigger did not fire")
	}

	// An amendment must name an independent reviewer.
	if _, err := owner.Exec(ctx,
		`INSERT INTO pamoja.meeting_attendance
		   (legal_entity_id, branch_id, group_id, meeting_id, member_id, status, checkin_channel,
		    recorded_by, amendment_of_id, amendment_reason, reviewer_id)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'absent', 'assisted',
		         $6::uuid, $7::uuid, 'member disputed the mark', $6::uuid)`,
		tenant.LegalEntityID, tenant.BranchID, tenant.GroupID, meetingID, member, secretary, attendanceID); err == nil {
		t.Fatal("self-reviewed amendment was allowed; SD-09 trigger did not fire")
	}

	var amendmentID string
	if err := owner.QueryRow(ctx,
		`INSERT INTO pamoja.meeting_attendance
		   (legal_entity_id, branch_id, group_id, meeting_id, member_id, status, checkin_channel,
		    recorded_by, amendment_of_id, amendment_reason, reviewer_id)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'absent', 'assisted',
		         $6::uuid, $7::uuid, 'member disputed the mark', $8::uuid)
		 RETURNING id::text`,
		tenant.LegalEntityID, tenant.BranchID, tenant.GroupID, meetingID, member, secretary, attendanceID, reviewer).
		Scan(&amendmentID); err != nil {
		t.Fatalf("independent amendment: %v", err)
	}

	var originalStatus, reason, amendmentOf string
	var recordedAt time.Time
	if err := owner.QueryRow(ctx,
		`SELECT status, amendment_reason, amendment_of_id::text, recorded_at
		 FROM pamoja.meeting_attendance WHERE id = $1::uuid`, amendmentID).
		Scan(&originalStatus, &reason, &amendmentOf, &recordedAt); err != nil {
		t.Fatalf("read amendment: %v", err)
	}
	if originalStatus != "absent" || reason == "" || amendmentOf != attendanceID || recordedAt.IsZero() {
		t.Fatalf("amendment did not retain original/new/reason/timestamp: %+v", originalStatus)
	}
	var count int
	if err := owner.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.meeting_attendance WHERE id = $1::uuid AND status = 'present'`,
		attendanceID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("original row was not retained: count=%d err=%v", count, err)
	}
	_ = app
}

// RLS: an actor scoped to one group cannot read another group's meetings.
func TestMeetingRLSIsolatesGroups(t *testing.T) {
	owner, app := testsupport.Pools(t)
	ctx := context.Background()
	tenantA := testsupport.CreateTenant(t, owner, "Tenant A")
	tenantB := testsupport.CreateTenant(t, owner, "Tenant B")
	secretary := testsupport.CreateUser(t, owner, "RLS Secretary")

	var meetingB string
	if err := owner.QueryRow(ctx,
		`INSERT INTO pamoja.meeting
		   (legal_entity_id, branch_id, group_id, scheduled_for, status, created_by)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, now(), 'scheduled', $4::uuid)
		 RETURNING id::text`,
		tenantB.LegalEntityID, tenantB.BranchID, tenantB.GroupID, secretary).Scan(&meetingB); err != nil {
		t.Fatalf("meeting B: %v", err)
	}

	var visible int
	err := db.WithActor(ctx, app, db.Actor{UserID: secretary, Scopes: []string{tenantA.GroupID}}, "rls-test",
		func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM pamoja.meeting`).Scan(&visible)
		})
	if err != nil {
		t.Fatalf("scoped query: %v", err)
	}
	if visible != 0 {
		t.Fatalf("RLS exposed %d meetings outside the actor's scope", visible)
	}

	var own int
	err = db.WithActor(ctx, app, db.Actor{UserID: secretary, Scopes: []string{tenantB.GroupID}}, "rls-test",
		func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM pamoja.meeting`).Scan(&own)
		})
	if err != nil {
		t.Fatalf("own-scope query: %v", err)
	}
	if own != 1 {
		t.Fatalf("RLS hid the actor's own meeting: got %d", own)
	}
}
