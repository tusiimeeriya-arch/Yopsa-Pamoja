package payments_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/payments"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

// runActor runs fn in one actor-scoped transaction.
func (f fixture) runActor(t *testing.T, actor string, fn func(context.Context, pgx.Tx) error) error {
	t.Helper()
	return db.WithActor(context.Background(), f.app,
		db.Actor{UserID: actor, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(), fn)
}

// Validation happens before any statement, so these cases need no database.
func TestCreateEntryRejectsInvalidInput(t *testing.T) {
	svc := payments.New(nil)
	base := payments.CreateEntryInput{
		GroupID: uuid.NewString(), MemberID: "member-1", Category: "savings",
		AmountUGX: 5000, Channel: "cash", IdempotencyKey: "key-1",
	}
	cases := map[string]func(*payments.CreateEntryInput){
		"member":      func(in *payments.CreateEntryInput) { in.MemberID = " " },
		"amount":      func(in *payments.CreateEntryInput) { in.AmountUGX = 0 },
		"category":    func(in *payments.CreateEntryInput) { in.Category = "not_a_category" },
		"channel":     func(in *payments.CreateEntryInput) { in.Channel = "carrier_pigeon" },
		"paid_on":     func(in *payments.CreateEntryInput) { in.PaidOn = time.Now().UTC().AddDate(0, 0, 3) },
		"idempotency": func(in *payments.CreateEntryInput) { in.IdempotencyKey = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			if _, err := svc.CreateEntry(context.Background(), nil, in); !domain.IsValidation(err) {
				t.Fatalf("%s: expected a validation error, got %v", name, err)
			}
		})
	}
}

func TestCreateEntryConflictsAndMissingGroup(t *testing.T) {
	f := setupFixture(t, 0)
	member := testsupport.CreateMember(t, f.owner, f.tenant, "Duplicate Member", "")
	svc := payments.New(nil)
	key := "dup-" + uuid.NewString()
	create := func(groupID string) error {
		return f.runActor(t, f.treasurer, func(ctx context.Context, tx pgx.Tx) error {
			_, err := svc.CreateEntry(ctx, tx, payments.CreateEntryInput{
				GroupID: groupID, MemberID: member, Category: "savings", AmountUGX: 1000,
				Channel: "cash", RecordedBy: f.treasurer, IdempotencyKey: key,
			})
			return err
		})
	}
	if err := create(f.tenant.GroupID); err != nil {
		t.Fatalf("first entry: %v", err)
	}
	if err := create(f.tenant.GroupID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a repeated idempotency key must be a conflict, got %v", err)
	}
	if err := create(uuid.NewString()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown group must be not found, got %v", err)
	}
}

func TestLockSubmissionRejectsBackwardsPeriod(t *testing.T) {
	svc := payments.New(nil)
	_, err := svc.LockSubmission(context.Background(), nil, payments.LockInput{
		GroupID: uuid.NewString(), PeriodStart: time.Now().UTC(),
		PeriodEnd: time.Now().UTC().AddDate(0, 0, -1),
	})
	if !domain.IsValidation(err) {
		t.Fatalf("expected a validation error, got %v", err)
	}
}

func TestLockRejectsEmptyPeriod(t *testing.T) {
	f := setupFixture(t, 0)
	svc := payments.New(nil)
	err := f.runActor(t, f.treasurer, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.LockSubmission(ctx, tx, payments.LockInput{
			GroupID: f.tenant.GroupID, PeriodStart: time.Now().UTC().AddDate(0, 0, -30),
			PeriodEnd: time.Now().UTC(), ActorID: f.treasurer,
		})
		return err
	})
	if !domain.IsValidation(err) {
		t.Fatalf("an empty period must be refused, got %v", err)
	}
}

func TestReturnedSubmissionCanBeRelocked(t *testing.T) {
	f := setupFixture(t, 2)
	sub := f.lockAndSubmit(t)
	svc := payments.New(nil)
	period := payments.LockInput{
		GroupID: f.tenant.GroupID, PeriodStart: sub.PeriodStart, PeriodEnd: sub.PeriodEnd,
		ActorID: f.treasurer,
	}

	// A return needs a reason.
	err := f.runActor(t, f.reviewer, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Return(ctx, tx, sub.ID, f.reviewer, "")
		return err
	})
	if !domain.IsValidation(err) {
		t.Fatalf("a return without a reason must be refused, got %v", err)
	}

	var returned payments.Submission
	err = f.runActor(t, f.reviewer, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		returned, err = svc.Return(ctx, tx, sub.ID, f.reviewer, "The cash book does not match the bank statement")
		return err
	})
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if returned.Status != "returned" {
		t.Fatalf("submission not returned: %+v", returned)
	}

	// Re-locking a returned submission puts its entries back to draft.
	var relocked payments.Submission
	err = f.runActor(t, f.treasurer, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		relocked, err = svc.LockSubmission(ctx, tx, period)
		return err
	})
	if err != nil {
		t.Fatalf("relock: %v", err)
	}
	if relocked.Status != "locked" || relocked.EntryCount != 2 {
		t.Fatalf("relock did not pick the entries up again: %+v", relocked)
	}

	// A locked submission cannot be locked twice.
	err = f.runActor(t, f.treasurer, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.LockSubmission(ctx, tx, period)
		return err
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("relocking a locked submission must conflict, got %v", err)
	}
}

func TestVerifyGuardsAndDuplicateCallback(t *testing.T) {
	f := setupFixture(t, 2)
	sub := f.lockAndSubmit(t)
	svc := payments.New(nil)
	verifyWith := func(in payments.VerifyInput) (payments.VerifyResult, error) {
		var out payments.VerifyResult
		err := f.runActor(t, f.verifier, func(ctx context.Context, tx pgx.Tx) error {
			in.SubmissionID = sub.ID
			in.ActorID = f.verifier
			var err error
			out, err = svc.Verify(ctx, tx, in)
			return err
		})
		return out, err
	}

	// Every verified entry needs a source reference and evidence.
	if _, err := verifyWith(payments.VerifyInput{ReceivedUGX: 40000, Source: "mobile_money"}); !domain.IsValidation(err) {
		t.Fatalf("missing evidence must be refused, got %v", err)
	}
	// A cash path needs different people at each step.
	if _, err := verifyWith(payments.VerifyInput{
		ReceivedUGX: 40000, Source: "cash", EvidenceID: uuid.NewString(),
		CashHandoverBy: f.verifier, CashDepositBy: f.verifier,
	}); !domain.IsValidation(err) {
		t.Fatalf("a one-person cash path must be refused, got %v", err)
	}

	ref := "PR-" + uuid.NewString()
	first, err := verifyWith(payments.VerifyInput{
		ReceivedUGX: 40000, Source: "mobile_money", ProviderRef: ref, EvidenceID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// The same provider callback again returns the receipt it already made.
	second, err := verifyWith(payments.VerifyInput{
		ReceivedUGX: 40000, Source: "mobile_money", ProviderRef: ref, EvidenceID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("repeated callback: %v", err)
	}
	if second.ReceiptID != first.ReceiptID {
		t.Fatalf("a repeated callback must replay the receipt: %s vs %s", second.ReceiptID, first.ReceiptID)
	}
}

func TestVerifyRefusesUnreviewedSubmission(t *testing.T) {
	f := setupFixture(t, 1)
	svc := payments.New(nil)
	var locked payments.Submission
	err := f.runActor(t, f.treasurer, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		locked, err = svc.LockSubmission(ctx, tx, payments.LockInput{
			GroupID: f.tenant.GroupID, PeriodStart: time.Now().UTC().AddDate(0, 0, -30),
			PeriodEnd: time.Now().UTC(), ActorID: f.treasurer,
		})
		return err
	})
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	err = f.runActor(t, f.verifier, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Verify(ctx, tx, payments.VerifyInput{
			SubmissionID: locked.ID, ReceivedUGX: 20000, Source: "mobile_money",
			ProviderRef: "PR-" + uuid.NewString(), EvidenceID: uuid.NewString(), ActorID: f.verifier,
		})
		return err
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a locked submission must not be verified, got %v", err)
	}
}

func TestRetryPostingGuards(t *testing.T) {
	f := setupFixture(t, 1)
	svc := payments.New(nil)

	err := f.runActor(t, f.reviewer, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.RetryPosting(ctx, tx, uuid.NewString(), f.reviewer)
		return err
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown posting must be not found, got %v", err)
	}

	sub := f.lockAndSubmit(t)
	if _, err := f.verify(t, svc, sub, 20000, false, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
	var postingID string
	if err := f.owner.QueryRow(context.Background(),
		`SELECT id::text FROM pamoja.ledger_posting
		 WHERE group_id = $1::uuid AND status = 'confirmed' LIMIT 1`,
		f.tenant.GroupID).Scan(&postingID); err != nil {
		t.Fatalf("read posting: %v", err)
	}
	var confirmed bool
	err = f.runActor(t, f.reviewer, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		confirmed, err = svc.RetryPosting(ctx, tx, postingID, f.reviewer)
		return err
	})
	if err != nil {
		t.Fatalf("retry a confirmed posting: %v", err)
	}
	if !confirmed {
		t.Fatal("a confirmed posting must report success")
	}
}

func TestSummariseAndEntryIDs(t *testing.T) {
	f := setupFixture(t, 2)
	sub := f.lockAndSubmit(t)
	svc := payments.New(nil)
	if _, err := f.verify(t, svc, sub, 40000, false, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
	var summary payments.Summary
	err := f.runActor(t, f.treasurer, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		summary, err = svc.Summarise(ctx, tx, f.tenant.GroupID)
		return err
	})
	if err != nil {
		t.Fatalf("summarise: %v", err)
	}
	if summary.PostedUGX != 40000 || summary.SubmittedUGX != 0 {
		t.Fatalf("summary does not separate the states: %+v", summary)
	}
	if id := payments.NewEntryID(); id == "" {
		t.Fatal("NewEntryID returned an empty id")
	}
}
