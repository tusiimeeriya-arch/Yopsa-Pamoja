package engagement_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/engagement"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

type fixture struct {
	owner   *pgxpool.Pool
	app     *pgxpool.Pool
	tenant  testsupport.Tenant
	actor   string
	member  string
	service *engagement.Service
}

func setupFixture(t *testing.T) fixture {
	t.Helper()
	owner, app := testsupport.Pools(t)
	tenant := testsupport.CreateTenant(t, owner, "Engagement group")
	f := fixture{
		owner: owner, app: app, tenant: tenant, service: engagement.New(),
		actor: testsupport.CreateUser(t, owner, "Secretary"),
	}
	f.member = testsupport.CreateMember(t, owner, tenant, "Engaged Member", "")
	return f
}

func (f fixture) run(t *testing.T, fn func(context.Context, pgx.Tx) error) error {
	t.Helper()
	return db.WithActor(context.Background(), f.app,
		db.Actor{UserID: f.actor, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(), fn)
}

// T-ENG-03: progress upserts per member and completion is stamped by the
// database.
func TestLearningProgress(t *testing.T) {
	f := setupFixture(t)
	var first engagement.Progress
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		first, err = f.service.RecordProgress(ctx, tx, engagement.RecordProgressInput{
			GroupID: f.tenant.GroupID, ItemCode: "L-001", MemberID: f.member,
			Status: "completed", ActorID: f.actor,
		})
		return err
	})
	if err != nil {
		t.Fatalf("record progress: %v", err)
	}
	if first.Status != "completed" || first.CompletedAt == nil {
		t.Fatalf("completion not stamped: %+v", first)
	}

	var reopened engagement.Progress
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		reopened, err = f.service.RecordProgress(ctx, tx, engagement.RecordProgressInput{
			GroupID: f.tenant.GroupID, ItemCode: "L-001", MemberID: f.member,
			Status: "in_progress", ActorID: f.actor,
		})
		return err
	})
	if err != nil {
		t.Fatalf("reopen progress: %v", err)
	}
	if reopened.ID != first.ID || reopened.CompletedAt != nil {
		t.Fatalf("progress did not upsert: %+v", reopened)
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.RecordProgress(ctx, tx, engagement.RecordProgressInput{
			GroupID: f.tenant.GroupID, ItemCode: "L-999", MemberID: f.member,
			Status: "completed", ActorID: f.actor,
		})
		return err
	})
	if err == nil || !domain.IsValidation(err) {
		t.Fatalf("an unknown item must be rejected, got %v", err)
	}
}

// T-ENG-04: a hidden comment disappears from the member view and keeps its
// moderator.
func TestCommentModeration(t *testing.T) {
	f := setupFixture(t)
	var comment engagement.Comment
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		comment, err = f.service.AddComment(ctx, tx, engagement.AddCommentInput{
			GroupID: f.tenant.GroupID, EntityType: "meeting",
			EntityID: uuid.NewString(), AuthorID: f.actor, Body: "  Two members were missed.  ",
		})
		return err
	})
	if err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if comment.Body != "Two members were missed." || comment.Visibility != "group" {
		t.Fatalf("comment not normalised: %+v", comment)
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		return f.service.ModerateComment(ctx, tx, comment.ID, f.actor, true)
	})
	if err != nil {
		t.Fatalf("moderate: %v", err)
	}

	var visible []engagement.Comment
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		visible, err = f.service.ListComments(ctx, tx, f.tenant.GroupID, "meeting", comment.EntityID, false)
		return err
	})
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("a hidden comment must not be listed: %+v", visible)
	}

	var all []engagement.Comment
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		all, err = f.service.ListComments(ctx, tx, f.tenant.GroupID, "meeting", comment.EntityID, true)
		return err
	})
	if err != nil || len(all) != 1 || all[0].Status != "hidden" || all[0].ModeratedBy != f.actor {
		t.Fatalf("moderation evidence missing: %v (%+v)", err, all)
	}
}

// T-ENG-05: a dispute cannot be closed without a note, and closing is final.
func TestDisputeResolution(t *testing.T) {
	f := setupFixture(t)
	var dispute engagement.Dispute
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		dispute, err = f.service.RaiseDispute(ctx, tx, engagement.RaiseDisputeInput{
			GroupID: f.tenant.GroupID, Kind: "attendance", MemberID: f.member,
			RaisedBy: f.actor, Description: "Marked absent while present at the meeting",
		})
		return err
	})
	if err != nil {
		t.Fatalf("raise dispute: %v", err)
	}
	if dispute.Status != "open" || dispute.DueOn == "" {
		t.Fatalf("unexpected dispute: %+v", dispute)
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.ResolveDispute(ctx, tx, dispute.ID, f.actor, "resolved", "")
		return err
	})
	if err == nil || !domain.IsValidation(err) {
		t.Fatalf("closing without a note must fail, got %v", err)
	}

	var resolved engagement.Dispute
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resolved, err = f.service.ResolveDispute(ctx, tx, dispute.ID, f.actor, "resolved",
			"Register amended with the member's signature")
		return err
	})
	if err != nil {
		t.Fatalf("resolve dispute: %v", err)
	}
	if resolved.Status != "resolved" || resolved.ResolvedBy != f.actor || resolved.ResolutionNote == "" {
		t.Fatalf("resolution evidence missing: %+v", resolved)
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.ResolveDispute(ctx, tx, dispute.ID, f.actor, "rejected", "Second try")
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("a closed dispute must not reopen, got %v", err)
	}
}

// T-ENG-06: closing a task is one-way and records who closed it.
func TestTaskLifecycle(t *testing.T) {
	f := setupFixture(t)
	var task engagement.Task
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		task, err = f.service.CreateTask(ctx, tx, engagement.CreateTaskInput{
			GroupID: f.tenant.GroupID, Title: "Collect the missing register signatures",
			OwnerUserID: f.actor, DueOn: time.Now().UTC().AddDate(0, 0, 7), CreatedBy: f.actor,
		})
		return err
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != "open" {
		t.Fatalf("unexpected task: %+v", task)
	}

	var closed engagement.Task
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		closed, err = f.service.CloseTask(ctx, tx, task.ID, f.actor, "done")
		return err
	})
	if err != nil {
		t.Fatalf("close task: %v", err)
	}
	if closed.Status != "done" || closed.ClosedBy != f.actor {
		t.Fatalf("close evidence missing: %+v", closed)
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.CloseTask(ctx, tx, task.ID, f.actor, "cancelled")
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("a closed task must not change again, got %v", err)
	}
}

// T-ENG-07: a user sees and reads only their own notifications.
func TestNotificationsAreOwnerOnly(t *testing.T) {
	f := setupFixture(t)
	var note engagement.Notification
	err := f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		note, err = f.service.Notify(ctx, tx, f.actor, "task", "A task is due",
			"Collect the missing signatures", "task", "")
		return err
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}

	var list []engagement.Notification
	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		list, err = f.service.ListNotifications(ctx, tx, f.actor, 50)
		return err
	})
	if err != nil || len(list) != 1 || list[0].ID != note.ID {
		t.Fatalf("notification not listed: %v (%+v)", err, list)
	}

	other := testsupport.CreateUser(t, f.owner, "Other User")
	var foreign string
	if err := f.owner.QueryRow(context.Background(),
		`INSERT INTO pamoja.notification (user_id, kind, title) VALUES ($1::uuid, 'task', 'Not yours')
		 RETURNING id::text`, other).Scan(&foreign); err != nil {
		t.Fatalf("foreign notification: %v", err)
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		return f.service.MarkNotificationRead(ctx, tx, foreign, f.actor)
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another user's notification must not be readable, got %v", err)
	}

	err = f.run(t, func(ctx context.Context, tx pgx.Tx) error {
		return f.service.MarkNotificationRead(ctx, tx, note.ID, f.actor)
	})
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	var readAt *time.Time
	if err := f.owner.QueryRow(context.Background(),
		`SELECT read_at FROM pamoja.notification WHERE id = $1::uuid`, note.ID).Scan(&readAt); err != nil {
		t.Fatalf("read notification: %v", err)
	}
	if readAt == nil {
		t.Fatal("read_at not stamped")
	}
}
