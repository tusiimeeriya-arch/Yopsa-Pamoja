package payments_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/ledger"
	"github.com/yopsa/pamoja/backend/internal/payments"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

type fixture struct {
	owner     *pgxpool.Pool
	app       *pgxpool.Pool
	tenant    testsupport.Tenant
	treasurer string
	verifier  string
	reviewer  string
	entries   []payments.Entry
}

func setupFixture(t *testing.T, count int) fixture {
	t.Helper()
	owner, app := testsupport.Pools(t)
	tenant := testsupport.CreateTenant(t, owner, "Payments group")
	f := fixture{
		owner: owner, app: app, tenant: tenant,
		treasurer: testsupport.CreateUser(t, owner, "Treasurer"),
		verifier:  testsupport.CreateUser(t, owner, "Verifier"),
		reviewer:  testsupport.CreateUser(t, owner, "Reviewer"),
	}
	member := testsupport.CreateMember(t, owner, tenant, "Paying Member", "")
	svc := payments.New(ledger.NewSandbox())
	err := db.WithActor(context.Background(), app, db.Actor{UserID: f.treasurer, Scopes: []string{tenant.GroupID}}, "fixture",
		func(ctx context.Context, tx pgx.Tx) error {
			for i := 0; i < count; i++ {
				e, err := svc.CreateEntry(ctx, tx, payments.CreateEntryInput{
					GroupID: tenant.GroupID, MemberID: member, Category: "savings",
					AmountUGX: 20000, PaidOn: time.Now().UTC().AddDate(0, 0, -1),
					Channel: "mobile_money", SourceRef: "MM-" + uuid.NewString(),
					EvidenceID: uuid.NewString(), RecordedBy: f.treasurer,
					IdempotencyKey: uuid.NewString(),
				})
				if err != nil {
					return err
				}
				f.entries = append(f.entries, e)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("create entries: %v", err)
	}
	return f
}

func (f fixture) lockAndSubmit(t *testing.T) payments.Submission {
	t.Helper()
	svc := payments.New(ledger.NewSandbox())
	var sub payments.Submission
	err := db.WithActor(context.Background(), f.app, db.Actor{UserID: f.treasurer, Scopes: []string{f.tenant.GroupID}}, "lock",
		func(ctx context.Context, tx pgx.Tx) error {
			var err error
			sub, err = svc.LockSubmission(ctx, tx, payments.LockInput{
				GroupID: f.tenant.GroupID, PeriodStart: time.Now().UTC().AddDate(0, 0, -30),
				PeriodEnd: time.Now().UTC(), ActorID: f.treasurer,
			})
			if err != nil {
				return err
			}
			sub, err = svc.Submit(ctx, tx, sub.ID, f.treasurer)
			return err
		})
	if err != nil {
		t.Fatalf("lock/submit: %v", err)
	}
	return sub
}

func (f fixture) verify(t *testing.T, svc *payments.Service, sub payments.Submission, received int64, partial bool, allocations []payments.Allocation) (payments.VerifyResult, error) {
	t.Helper()
	var result payments.VerifyResult
	err := db.WithActor(context.Background(), f.app, db.Actor{UserID: f.verifier, Scopes: []string{f.tenant.GroupID}}, "verify",
		func(ctx context.Context, tx pgx.Tx) error {
			var err error
			result, err = svc.Verify(ctx, tx, payments.VerifyInput{
				SubmissionID: sub.ID, ReceivedUGX: received, Source: "mobile_money",
				ProviderRef: "PR-" + uuid.NewString(), EvidenceID: uuid.NewString(),
				Partial: partial, Allocations: allocations, ActorID: f.verifier,
			})
			return err
		})
	return result, err
}

// Full verification writes one receipt, allocations and postings, and reports
// submitted / verified / posted separately.
func TestFullVerificationAndSummary(t *testing.T) {
	f := setupFixture(t, 3)
	sub := f.lockAndSubmit(t)
	if sub.DeclaredTotal != 60000 || sub.EntryCount != 3 {
		t.Fatalf("unexpected lock: %+v", sub)
	}
	svc := payments.New(ledger.NewSandbox())
	result, err := f.verify(t, svc, sub, 60000, false, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Submission.Status != "posted" || result.PostingPending {
		t.Fatalf("submission not posted: %+v", result.Submission)
	}

	var receipts, allocations, postings int
	if err := f.owner.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM pamoja.receipt WHERE submission_id = $1::uuid),
		        (SELECT count(*) FROM pamoja.allocation a JOIN pamoja.receipt r ON r.id = a.receipt_id WHERE r.submission_id = $1::uuid),
		        (SELECT count(*) FROM pamoja.ledger_posting p JOIN pamoja.allocation a ON a.id = p.allocation_id
		           JOIN pamoja.receipt r ON r.id = a.receipt_id WHERE r.submission_id = $1::uuid AND p.status = 'confirmed')`,
		sub.ID).Scan(&receipts, &allocations, &postings); err != nil {
		t.Fatalf("counts: %v", err)
	}
	if receipts != 1 || allocations != 3 || postings != 3 {
		t.Fatalf("three facts not separate: receipts=%d allocations=%d postings=%d", receipts, allocations, postings)
	}

	var summary payments.Summary
	if err := db.WithActor(context.Background(), f.app, db.Actor{UserID: f.verifier, Scopes: []string{f.tenant.GroupID}}, "summary",
		func(ctx context.Context, tx pgx.Tx) error {
			var err error
			summary, err = svc.Summarise(ctx, tx, f.tenant.GroupID)
			return err
		}); err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.PostedUGX != 60000 || summary.SubmittedUGX != 0 {
		t.Fatalf("summary does not separate values: %+v", summary)
	}
}

// T-PAY-01: a 20,000 shortage blocks full verification and is never spread.
func TestShortageNeverSpread(t *testing.T) {
	f := setupFixture(t, 10)
	sub := f.lockAndSubmit(t)
	svc := payments.New(ledger.NewSandbox())

	if _, err := f.verify(t, svc, sub, 180000, false, nil); err == nil {
		t.Fatal("full verification accepted a 20,000 shortage")
	}

	allocs := make([]payments.Allocation, 0, 9)
	for _, e := range f.entries[:9] {
		allocs = append(allocs, payments.Allocation{EntryID: e.ID, AmountUGX: 20000})
	}
	result, err := f.verify(t, svc, sub, 180000, true, allocs)
	if err != nil {
		t.Fatalf("partial verification: %v", err)
	}
	if result.Difference != 20000 || !result.Partial {
		t.Fatalf("unexpected partial result: %+v", result)
	}
	var allocationCount int
	if err := f.owner.QueryRow(context.Background(),
		`SELECT count(*) FROM pamoja.allocation WHERE group_id = $1::uuid`, f.tenant.GroupID).Scan(&allocationCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if allocationCount != 9 {
		t.Fatalf("shortage was spread: %d allocations", allocationCount)
	}
}

// T-PAY-04: the database rejects an allocation above confirmed funds even if
// the service is bypassed.
func TestAllocationCapEnforcedByDatabase(t *testing.T) {
	f := setupFixture(t, 0)
	ctx := context.Background()
	member := testsupport.CreateMember(t, f.owner, f.tenant, "Cap Member", "")
	var receiptID, entryID string
	if err := f.owner.QueryRow(ctx,
		`INSERT INTO pamoja.receipt
		   (legal_entity_id, branch_id, group_id, amount_ugx, source, provider_ref, evidence_id,
		    status, recorded_by, confirmed_by, received_on)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 10000, 'mobile_money', $4, $5::uuid,
		         'confirmed', $6::uuid, $6::uuid, current_date)
		 RETURNING id::text`,
		f.tenant.LegalEntityID, f.tenant.BranchID, f.tenant.GroupID,
		"CAP-"+uuid.NewString(), uuid.NewString(), f.verifier).Scan(&receiptID); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if err := f.owner.QueryRow(ctx,
		`INSERT INTO pamoja.payment_entry
		   (legal_entity_id, branch_id, group_id, member_id, category, amount_ugx, paid_on,
		    channel, source_ref, evidence_id, status, recorded_by, idempotency_key)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'savings', 20000, current_date,
		         'mobile_money', 'MM-CAP', $5::uuid, 'submitted', $6::uuid, $7)
		 RETURNING id::text`,
		f.tenant.LegalEntityID, f.tenant.BranchID, f.tenant.GroupID, member,
		uuid.NewString(), f.treasurer, uuid.NewString()).Scan(&entryID); err != nil {
		t.Fatalf("entry: %v", err)
	}
	_, err := f.owner.Exec(ctx,
		`INSERT INTO pamoja.allocation
		   (legal_entity_id, branch_id, group_id, receipt_id, entry_id, amount_ugx, created_by)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 10001, $6::uuid)`,
		f.tenant.LegalEntityID, f.tenant.BranchID, f.tenant.GroupID, receiptID, entryID, f.verifier)
	if err == nil {
		t.Fatal("database accepted an allocation above the receipt")
	}
}

// T-PAY-05: a locked entry cannot be edited, even by a direct database update.
func TestLockedEntryIsImmutable(t *testing.T) {
	f := setupFixture(t, 1)
	f.lockAndSubmit(t)
	_, err := f.owner.Exec(context.Background(),
		`UPDATE pamoja.payment_entry SET amount_ugx = 99999 WHERE id = $1::uuid`, f.entries[0].ID)
	if err == nil {
		t.Fatal("database allowed a locked entry to be edited")
	}
}

// T-PAY-06: the recorder cannot verify their own submission.
func TestSelfVerificationRejected(t *testing.T) {
	f := setupFixture(t, 1)
	sub := f.lockAndSubmit(t)
	svc := payments.New(ledger.NewSandbox())
	var err error
	_ = db.WithActor(context.Background(), f.app, db.Actor{UserID: f.treasurer, Scopes: []string{f.tenant.GroupID}}, "self-verify",
		func(ctx context.Context, tx pgx.Tx) error {
			_, err = svc.Verify(ctx, tx, payments.VerifyInput{
				SubmissionID: sub.ID, ReceivedUGX: 20000, Source: "mobile_money",
				ProviderRef: "PR-" + uuid.NewString(), EvidenceID: uuid.NewString(),
				ActorID: f.treasurer,
			})
			return nil
		})
	if !errors.Is(err, domain.ErrSeparationOfDuties) {
		t.Fatalf("got %v, want separation-of-duties error", err)
	}
}

// T-PAY-08: posting failure yields Posting pending plus an exception with an
// owner and deadline; retry posts it.
func TestPostingFailureBecomesExceptionAndRetries(t *testing.T) {
	f := setupFixture(t, 1)
	sub := f.lockAndSubmit(t)
	sandbox := ledger.NewSandbox()
	sandbox.FailRefs["SBX-"+f.entries[0].ID] = true
	svc := payments.New(sandbox)

	result, err := f.verify(t, svc, sub, 20000, false, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.PostingPending || result.Submission.Status != "posting_pending" || result.Exceptions != 1 {
		t.Fatalf("posting failure did not produce pending + exception: %+v", result)
	}
	var owner string
	var due time.Time
	if err := f.owner.QueryRow(context.Background(),
		`SELECT owner_user_id::text, due_on FROM pamoja.finance_exception WHERE submission_id = $1::uuid`,
		sub.ID).Scan(&owner, &due); err != nil {
		t.Fatalf("exception: %v", err)
	}
	if owner == "" || due.IsZero() {
		t.Fatal("exception has no owner or deadline")
	}

	sandbox.FailRefs = map[string]bool{}
	var postingID string
	if err := f.owner.QueryRow(context.Background(),
		`SELECT p.id::text FROM pamoja.ledger_posting p
		 JOIN pamoja.allocation a ON a.id = p.allocation_id
		 JOIN pamoja.payment_entry e ON e.id = a.entry_id
		 WHERE e.id = $1::uuid`, f.entries[0].ID).Scan(&postingID); err != nil {
		t.Fatalf("posting id: %v", err)
	}
	var ok bool
	if err := db.WithActor(context.Background(), f.app, db.Actor{UserID: f.verifier, Scopes: []string{f.tenant.GroupID}}, "retry",
		func(ctx context.Context, tx pgx.Tx) error {
			var err error
			ok, err = svc.RetryPosting(ctx, tx, postingID, f.verifier)
			return err
		}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !ok {
		t.Fatal("retry did not confirm the posting")
	}
	var status string
	if err := f.owner.QueryRow(context.Background(),
		`SELECT status FROM pamoja.group_submission WHERE id = $1::uuid`, sub.ID).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "posted" {
		t.Fatalf("submission status after retry = %s, want posted", status)
	}
}

// T-PAY-09: concurrent verification attempts — exactly one succeeds.
func TestConcurrentVerificationExactlyOneSucceeds(t *testing.T) {
	f := setupFixture(t, 1)
	sub := f.lockAndSubmit(t)
	svc := payments.New(ledger.NewSandbox())

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = db.WithActor(context.Background(), f.app,
				db.Actor{UserID: f.verifier, Scopes: []string{f.tenant.GroupID}}, "concurrent",
				func(ctx context.Context, tx pgx.Tx) error {
					_, err := svc.Verify(ctx, tx, payments.VerifyInput{
						SubmissionID: sub.ID, ReceivedUGX: 20000, Source: "mobile_money",
						ProviderRef: "PR-" + uuid.NewString(), EvidenceID: uuid.NewString(),
						ActorID: f.verifier,
					})
					return err
				})
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent verification successes = %d, want 1 (errors: %v)", successes, results)
	}
}
