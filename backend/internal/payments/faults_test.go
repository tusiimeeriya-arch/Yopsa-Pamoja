package payments_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/ledger"
	"github.com/yopsa/pamoja/backend/internal/payments"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

// failingTx refuses the first statement whose SQL contains marker, so a test
// can prove that a database error is returned to the caller rather than
// swallowed.
type failingTx struct {
	pgx.Tx
	marker string
	err    error
}

func (t *failingTx) check(sql string) error {
	if strings.Contains(sql, t.marker) {
		return t.err
	}
	return nil
}

func (t *failingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := t.check(sql); err != nil {
		return pgconn.CommandTag{}, err
	}
	return t.Tx.Exec(ctx, sql, args...)
}

func (t *failingTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := t.check(sql); err != nil {
		return nil, err
	}
	return t.Tx.Query(ctx, sql, args...)
}

func (t *failingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := t.check(sql); err != nil {
		return failingRow{err: err}
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}

type failingRow struct{ err error }

func (r failingRow) Scan(...any) error { return r.err }

// runFailing runs fn in a real transaction that refuses one statement.
func (f fixture) runFailing(t *testing.T, actor, marker string, fn func(context.Context, pgx.Tx) error) error {
	t.Helper()
	return db.WithActor(context.Background(), f.app,
		db.Actor{UserID: actor, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(),
		func(ctx context.Context, tx pgx.Tx) error {
			return fn(ctx, &failingTx{Tx: tx, marker: marker, err: errors.New("injected database failure")})
		})
}

func (f fixture) assertSurfaced(t *testing.T, markers []string, fn func(context.Context, pgx.Tx) error) {
	t.Helper()
	for _, marker := range markers {
		err := f.runFailing(t, f.treasurer, marker, fn)
		if err == nil {
			t.Fatalf("%q: the database error was swallowed", marker)
		}
		if domain.IsValidation(err) {
			t.Fatalf("%q: a database failure must not look like a validation error: %v", marker, err)
		}
		if errors.Is(err, domain.ErrConflict) {
			t.Fatalf("%q: the scenario did not reach the injected failure: %v", marker, err)
		}
	}
}

func TestLockSubmissionSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t, 1)
	svc := payments.New(nil)
	f.assertSurfaced(t, []string{
		"SELECT status FROM pamoja.group_submission",
		"SET submission_id = $1::uuid, updated_at = now()",
		"SET status = 'locked', declared_total_ugx",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.LockSubmission(ctx, tx, payments.LockInput{
			GroupID: f.tenant.GroupID, PeriodStart: time.Now().UTC().AddDate(0, 0, -30),
			PeriodEnd: time.Now().UTC(), ActorID: f.treasurer,
		})
		return err
	})
}

func TestSubmitSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t, 1)
	sub := f.lockAndSubmit(t)
	svc := payments.New(nil)
	f.assertSurfaced(t, []string{
		"SET status = 'under_review'",
		"SET status = 'submitted'",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Submit(ctx, tx, sub.ID, f.treasurer)
		return err
	})
}

func TestCreateEntrySurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t, 0)
	svc := payments.New(nil)
	member := testsupport.CreateMember(t, f.owner, f.tenant, "Fault Member", "")
	key := "dup-" + uuid.NewString()
	create := func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.CreateEntry(ctx, tx, payments.CreateEntryInput{
			GroupID: f.tenant.GroupID, MemberID: member, Category: "savings",
			AmountUGX: 1000, Channel: "cash", RecordedBy: f.treasurer, IdempotencyKey: key,
		})
		return err
	}
	if err := f.runActor(t, f.treasurer, create); err != nil {
		t.Fatalf("first entry: %v", err)
	}
	// The key now exists, so the insert yields no row and the follow-up lookup
	// runs; that is the statement this test makes fail.
	f.assertSurfaced(t, []string{"SELECT EXISTS (SELECT 1 FROM pamoja.groups"}, create)
}

func TestVerifySurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t, 1)
	sub := f.lockAndSubmit(t)
	svc := payments.New(nil)
	f.assertSurfaced(t, []string{
		"SELECT id::text, group_id::text, period_start",
		"SELECT id::text FROM pamoja.receipt",
		"FROM pamoja.payment_entry WHERE submission_id",
		"INSERT INTO pamoja.receipt",
		"SET status = $2, verified_by",
		"SET status = 'verified'",
		"INSERT INTO pamoja.allocation",
		"UPDATE pamoja.allocation SET status = 'posted'",
		"UPDATE pamoja.payment_entry SET status = 'posted'",
		"UPDATE pamoja.group_submission SET status = 'posted'",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Verify(ctx, tx, payments.VerifyInput{
			SubmissionID: sub.ID, ReceivedUGX: 20000, Source: "mobile_money",
			ProviderRef: "PR-" + uuid.NewString(), EvidenceID: uuid.NewString(),
			ActorID: f.verifier,
		})
		return err
	})
}

func TestReturnSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t, 1)
	sub := f.lockAndSubmit(t)
	svc := payments.New(nil)
	f.assertSurfaced(t, []string{
		"SET status = 'returned', decision_note",
		"SET status = 'returned', version = version + 1",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Return(ctx, tx, sub.ID, f.reviewer, "The cash book does not match")
		return err
	})
}

func TestRetryPostingSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t, 1)
	sub := f.lockAndSubmit(t)
	sandbox := ledger.NewSandbox()
	sandbox.FailRefs["SBX-"+f.entries[0].ID] = true
	svc := payments.New(sandbox)
	if _, err := f.verify(t, svc, sub, 20000, false, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
	var postingID string
	if err := f.owner.QueryRow(context.Background(),
		`SELECT p.id::text FROM pamoja.ledger_posting p
		 JOIN pamoja.allocation a ON a.id = p.allocation_id
		 JOIN pamoja.payment_entry e ON e.id = a.entry_id
		 WHERE e.id = $1::uuid`, f.entries[0].ID).Scan(&postingID); err != nil {
		t.Fatalf("posting id: %v", err)
	}

	sandbox.FailRefs = map[string]bool{}
	f.assertSurfaced(t, []string{
		"UPDATE pamoja.ledger_posting SET status = 'confirmed'",
		"UPDATE pamoja.allocation SET status = 'posted'",
		"UPDATE pamoja.payment_entry SET status = 'posted'",
		"SELECT count(*) FROM pamoja.ledger_posting p",
		"UPDATE pamoja.group_submission SET status = 'posted'",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.RetryPosting(ctx, tx, postingID, f.verifier)
		return err
	})
}

func TestSummariseSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t, 1)
	svc := payments.New(nil)
	f.assertSurfaced(t, []string{
		"COALESCE(sum(amount_ugx) FILTER",
		"FROM pamoja.allocation a JOIN pamoja.ledger_posting p",
		"FROM pamoja.finance_exception",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Summarise(ctx, tx, f.tenant.GroupID)
		return err
	})
}

// A partial verification that leaves part of the receipt unallocated records a
// finance exception instead of hiding the money.
func TestPartialVerificationRecordsUnallocatedException(t *testing.T) {
	f := setupFixture(t, 1)
	sub := f.lockAndSubmit(t)
	svc := payments.New(ledger.NewSandbox())
	var result payments.VerifyResult
	err := f.runActor(t, f.verifier, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = svc.Verify(ctx, tx, payments.VerifyInput{
			SubmissionID: sub.ID, ReceivedUGX: 15000, Source: "mobile_money",
			ProviderRef: "PR-" + uuid.NewString(), EvidenceID: uuid.NewString(),
			Partial:     true,
			Allocations: []payments.Allocation{{EntryID: f.entries[0].ID, AmountUGX: 12000}},
			ActorID:     f.verifier,
		})
		return err
	})
	if err != nil {
		t.Fatalf("partial verify: %v", err)
	}
	if !result.Partial || result.Exceptions != 1 {
		t.Fatalf("an unallocated receipt must raise one exception: %+v", result)
	}
}
