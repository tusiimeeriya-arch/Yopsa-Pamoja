// Package engagement holds the P7 collaboration records: learning progress,
// comments, disputes, follow-up tasks and in-app notifications. The rules that
// need no database live here as pure functions.
package engagement

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
)

// Service writes and reads the engagement records inside the caller's
// transaction, so row-level security decides what is visible.
type Service struct {
	Now func() time.Time
}

// New returns an engagement service.
func New() *Service { return &Service{Now: time.Now} }

// ---------------------------------------------------------------------------
// Pure rules
// ---------------------------------------------------------------------------

// MaxCommentLength bounds one comment.
const MaxCommentLength = 2000

// ValidateCommentBody rejects empty and oversized comments.
func ValidateCommentBody(body string) error {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return domain.Invalid("body", "is required")
	}
	if len(body) > MaxCommentLength {
		return domain.Invalid("body", "is longer than 2000 characters")
	}
	return nil
}

// CanTransitionDispute reports the allowed dispute status changes.
func CanTransitionDispute(from, to string) bool {
	switch from {
	case "open":
		return to == "investigating" || to == "resolved" || to == "rejected"
	case "investigating":
		return to == "resolved" || to == "rejected"
	default:
		return false
	}
}

// RequiresResolutionNote reports whether closing a dispute needs a note.
func RequiresResolutionNote(to string) bool { return to == "resolved" || to == "rejected" }

// ---------------------------------------------------------------------------
// Learning
// ---------------------------------------------------------------------------

// Progress is one member's state on one learning item.
type Progress struct {
	ID          string     `json:"id"`
	ItemCode    string     `json:"item_code"`
	MemberID    string     `json:"member_id"`
	MemberName  string     `json:"member_name,omitempty"`
	Status      string     `json:"status"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// RecordProgressInput records or updates one member's learning progress.
type RecordProgressInput struct {
	GroupID  string
	ItemCode string
	MemberID string
	Status   string
	ActorID  string
}

// RecordProgress upserts a member's progress. Completing stamps the time.
func (s *Service) RecordProgress(ctx context.Context, tx pgx.Tx, in RecordProgressInput) (Progress, error) {
	switch in.Status {
	case "assigned", "in_progress", "completed":
	default:
		return Progress{}, domain.Invalid("status", "must be assigned, in_progress or completed")
	}
	if err := domain.Required("item_code", in.ItemCode); err != nil {
		return Progress{}, err
	}
	if err := domain.Required("member_id", in.MemberID); err != nil {
		return Progress{}, err
	}
	var out Progress
	err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.learning_progress
		   (legal_entity_id, branch_id, group_id, item_id, member_id, status, recorded_by)
		 SELECT g.legal_entity_id, g.branch_id, g.id, i.id, $2::uuid, $3, $4::uuid
		 FROM pamoja.groups g, pamoja.learning_item i
		 WHERE g.id = $1::uuid AND i.code = $5 AND i.status = 'published'
		 ON CONFLICT (item_id, member_id)
		 DO UPDATE SET status = EXCLUDED.status, recorded_by = EXCLUDED.recorded_by
		 RETURNING id::text, status, completed_at`,
		in.GroupID, in.MemberID, in.Status, in.ActorID, in.ItemCode).
		Scan(&out.ID, &out.Status, &out.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, domain.Invalid("item_code", "is not a published learning item")
	}
	if err != nil {
		return Progress{}, err
	}
	out.ItemCode = in.ItemCode
	out.MemberID = in.MemberID
	return out, nil
}

// ListProgress returns a group's progress for one item, or for every item.
func (s *Service) ListProgress(ctx context.Context, tx pgx.Tx, groupID, itemCode string) ([]Progress, error) {
	rows, err := tx.Query(ctx,
		`SELECT p.id::text, i.code, p.member_id::text, mem.full_name, p.status, p.completed_at
		 FROM pamoja.learning_progress p
		 JOIN pamoja.learning_item i ON i.id = p.item_id
		 JOIN pamoja.member mem ON mem.id = p.member_id
		 WHERE p.group_id = $1::uuid AND ($2 = '' OR i.code = $2)
		 ORDER BY i.code, mem.full_name`, groupID, itemCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Progress{}
	for rows.Next() {
		var p Progress
		if err := rows.Scan(&p.ID, &p.ItemCode, &p.MemberID, &p.MemberName, &p.Status, &p.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Comments
// ---------------------------------------------------------------------------

// Comment is one group-visible or restricted note on a record.
type Comment struct {
	ID          string    `json:"id"`
	EntityType  string    `json:"entity_type"`
	EntityID    string    `json:"entity_id"`
	AuthorID    string    `json:"author_id"`
	Body        string    `json:"body"`
	Visibility  string    `json:"visibility"`
	Status      string    `json:"status"`
	ModeratedBy string    `json:"moderated_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// AddCommentInput is a new comment on a record in the group.
type AddCommentInput struct {
	GroupID    string
	EntityType string
	EntityID   string
	AuthorID   string
	Body       string
	Visibility string
}

var commentEntityTypes = map[string]bool{
	"meeting": true, "payment_entry": true, "loan_application": true, "dispute": true, "task": true,
}

// AddComment records a comment.
func (s *Service) AddComment(ctx context.Context, tx pgx.Tx, in AddCommentInput) (Comment, error) {
	if err := ValidateCommentBody(in.Body); err != nil {
		return Comment{}, err
	}
	if !commentEntityTypes[in.EntityType] {
		return Comment{}, domain.Invalid("entity_type", "is not a commentable record type")
	}
	if err := domain.Required("entity_id", in.EntityID); err != nil {
		return Comment{}, err
	}
	if in.Visibility == "" {
		in.Visibility = "group"
	}
	if in.Visibility != "group" && in.Visibility != "restricted" {
		return Comment{}, domain.Invalid("visibility", "must be group or restricted")
	}
	var out Comment
	err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.comment
		   (legal_entity_id, branch_id, group_id, entity_type, entity_id,
		    author_user_id, body, visibility)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2, $3::uuid, $4::uuid, $5, $6
		 FROM pamoja.groups g WHERE g.id = $1::uuid
		 RETURNING id::text, entity_type, entity_id::text, author_user_id::text, body,
		           visibility, status, created_at`,
		in.GroupID, in.EntityType, in.EntityID, in.AuthorID, strings.TrimSpace(in.Body), in.Visibility).
		Scan(&out.ID, &out.EntityType, &out.EntityID, &out.AuthorID, &out.Body,
			&out.Visibility, &out.Status, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, domain.ErrNotFound
	}
	return out, err
}

// ModerateComment hides or restores a comment. Hiding records who did it.
func (s *Service) ModerateComment(ctx context.Context, tx pgx.Tx, commentID, actorID string, hide bool) error {
	status := "visible"
	if hide {
		status = "hidden"
	}
	tag, err := tx.Exec(ctx,
		`UPDATE pamoja.comment
		 SET status = $2,
		     moderated_by = CASE WHEN $2 = 'hidden' THEN $3::uuid ELSE NULL END,
		     moderated_at = CASE WHEN $2 = 'hidden' THEN now() ELSE NULL END
		 WHERE id = $1::uuid`, commentID, status, actorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListComments returns a record's comments, newest last. Hidden comments are
// only included when the caller moderates.
func (s *Service) ListComments(ctx context.Context, tx pgx.Tx, groupID, entityType, entityID string, includeHidden bool) ([]Comment, error) {
	rows, err := tx.Query(ctx,
		`SELECT id::text, entity_type, entity_id::text, author_user_id::text, body,
		        visibility, status, COALESCE(moderated_by::text, ''), created_at
		 FROM pamoja.comment
		 WHERE group_id = $1::uuid AND entity_type = $2 AND entity_id = $3::uuid
		   AND ($4 OR status = 'visible')
		 ORDER BY created_at`, groupID, entityType, entityID, includeHidden)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.AuthorID, &c.Body,
			&c.Visibility, &c.Status, &c.ModeratedBy, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Disputes
// ---------------------------------------------------------------------------

// Dispute is a contested attendance or payment record.
type Dispute struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	SubjectRef     string    `json:"subject_ref,omitempty"`
	MemberID       string    `json:"member_id,omitempty"`
	RaisedBy       string    `json:"raised_by"`
	Description    string    `json:"description"`
	Status         string    `json:"status"`
	ResolutionNote string    `json:"resolution_note,omitempty"`
	ResolvedBy     string    `json:"resolved_by,omitempty"`
	DueOn          string    `json:"due_on"`
	CreatedAt      time.Time `json:"created_at"`
}

// RaiseDisputeInput is a new dispute.
type RaiseDisputeInput struct {
	GroupID     string
	Kind        string
	SubjectRef  string
	MemberID    string
	RaisedBy    string
	Description string
}

// RaiseDispute records an open dispute.
func (s *Service) RaiseDispute(ctx context.Context, tx pgx.Tx, in RaiseDisputeInput) (Dispute, error) {
	switch in.Kind {
	case "attendance", "payment", "loan":
	default:
		return Dispute{}, domain.Invalid("kind", "must be attendance, payment or loan")
	}
	if err := domain.Required("description", in.Description); err != nil {
		return Dispute{}, err
	}
	var out Dispute
	var created time.Time
	var due time.Time
	err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.dispute
		   (legal_entity_id, branch_id, group_id, kind, subject_ref, member_id, raised_by, description)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2, NULLIF($3, '')::uuid,
		        NULLIF($4, '')::uuid, $5::uuid, $6
		 FROM pamoja.groups g WHERE g.id = $1::uuid
		 RETURNING id::text, kind, COALESCE(subject_ref::text, ''), COALESCE(member_id::text, ''),
		           raised_by::text, description, status, due_on, created_at`,
		in.GroupID, in.Kind, in.SubjectRef, in.MemberID, in.RaisedBy, in.Description).
		Scan(&out.ID, &out.Kind, &out.SubjectRef, &out.MemberID, &out.RaisedBy,
			&out.Description, &out.Status, &due, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dispute{}, domain.ErrNotFound
	}
	if err != nil {
		return Dispute{}, err
	}
	out.DueOn = due.Format("2006-01-02")
	out.CreatedAt = created
	return out, nil
}

// ResolveDispute closes a dispute with a note. The database re-checks both.
func (s *Service) ResolveDispute(ctx context.Context, tx pgx.Tx, disputeID, actorID, status, note string) (Dispute, error) {
	if !RequiresResolutionNote(status) {
		return Dispute{}, domain.Invalid("status", "must be resolved or rejected")
	}
	if err := domain.Required("resolution_note", note); err != nil {
		return Dispute{}, err
	}
	var current string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM pamoja.dispute WHERE id = $1::uuid FOR UPDATE`, disputeID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Dispute{}, domain.ErrNotFound
		}
		return Dispute{}, err
	}
	if !CanTransitionDispute(current, status) {
		return Dispute{}, domain.ErrInvalidTransition
	}
	var out Dispute
	var due, created time.Time
	err := tx.QueryRow(ctx,
		`UPDATE pamoja.dispute
		 SET status = $2, resolution_note = $3, resolved_by = $4::uuid
		 WHERE id = $1::uuid
		 RETURNING id::text, kind, COALESCE(subject_ref::text, ''), COALESCE(member_id::text, ''),
		           raised_by::text, description, status, resolution_note,
		           COALESCE(resolved_by::text, ''), due_on, created_at`,
		disputeID, status, note, actorID).
		Scan(&out.ID, &out.Kind, &out.SubjectRef, &out.MemberID, &out.RaisedBy, &out.Description,
			&out.Status, &out.ResolutionNote, &out.ResolvedBy, &due, &created)
	if err != nil {
		return Dispute{}, err
	}
	out.DueOn = due.Format("2006-01-02")
	out.CreatedAt = created
	return out, nil
}

// ListDisputes returns a group's disputes, newest first.
func (s *Service) ListDisputes(ctx context.Context, tx pgx.Tx, groupID string) ([]Dispute, error) {
	rows, err := tx.Query(ctx,
		`SELECT id::text, kind, COALESCE(subject_ref::text, ''), COALESCE(member_id::text, ''),
		        raised_by::text, description, status, resolution_note,
		        COALESCE(resolved_by::text, ''), due_on, created_at
		 FROM pamoja.dispute WHERE group_id = $1::uuid
		 ORDER BY created_at DESC LIMIT 200`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dispute{}
	for rows.Next() {
		var d Dispute
		var due, created time.Time
		if err := rows.Scan(&d.ID, &d.Kind, &d.SubjectRef, &d.MemberID, &d.RaisedBy,
			&d.Description, &d.Status, &d.ResolutionNote, &d.ResolvedBy, &due, &created); err != nil {
			return nil, err
		}
		d.DueOn = due.Format("2006-01-02")
		d.CreatedAt = created
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Tasks
// ---------------------------------------------------------------------------

// Task is a follow-up with an owner and a deadline.
type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail,omitempty"`
	OwnerID   string    `json:"owner_user_id"`
	DueOn     string    `json:"due_on"`
	Status    string    `json:"status"`
	ClosedBy  string    `json:"closed_by,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateTaskInput is a new follow-up task.
type CreateTaskInput struct {
	GroupID     string
	Title       string
	Detail      string
	OwnerUserID string
	DueOn       time.Time
	CreatedBy   string
}

// CreateTask records an open task.
func (s *Service) CreateTask(ctx context.Context, tx pgx.Tx, in CreateTaskInput) (Task, error) {
	if err := domain.Required("title", in.Title); err != nil {
		return Task{}, err
	}
	if err := domain.Required("owner_user_id", in.OwnerUserID); err != nil {
		return Task{}, err
	}
	if in.DueOn.IsZero() {
		return Task{}, domain.Invalid("due_on", "is required")
	}
	var out Task
	var due, created time.Time
	err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.task
		   (legal_entity_id, branch_id, group_id, title, detail, owner_user_id, due_on, created_by)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2, $3, $4::uuid, $5::date, $6::uuid
		 FROM pamoja.groups g WHERE g.id = $1::uuid
		 RETURNING id::text, title, detail, owner_user_id::text, due_on, status, created_by::text, created_at`,
		in.GroupID, in.Title, in.Detail, in.OwnerUserID, in.DueOn.Format("2006-01-02"), in.CreatedBy).
		Scan(&out.ID, &out.Title, &out.Detail, &out.OwnerID, &due, &out.Status, &out.CreatedBy, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, domain.ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	out.DueOn = due.Format("2006-01-02")
	out.CreatedAt = created
	return out, nil
}

// CloseTask marks a task done or cancelled.
func (s *Service) CloseTask(ctx context.Context, tx pgx.Tx, taskID, actorID, status string) (Task, error) {
	if status != "done" && status != "cancelled" {
		return Task{}, domain.Invalid("status", "must be done or cancelled")
	}
	var out Task
	var due, created time.Time
	err := tx.QueryRow(ctx,
		`UPDATE pamoja.task
		 SET status = $2, closed_by = $3::uuid, closed_at = now()
		 WHERE id = $1::uuid AND status = 'open'
		 RETURNING id::text, title, detail, owner_user_id::text, due_on, status,
		           COALESCE(closed_by::text, ''), created_by::text, created_at`,
		taskID, status, actorID).
		Scan(&out.ID, &out.Title, &out.Detail, &out.OwnerID, &due, &out.Status,
			&out.ClosedBy, &out.CreatedBy, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, domain.ErrInvalidTransition
	}
	if err != nil {
		return Task{}, err
	}
	out.DueOn = due.Format("2006-01-02")
	out.CreatedAt = created
	return out, nil
}

// ListTasks returns a group's tasks, open first.
func (s *Service) ListTasks(ctx context.Context, tx pgx.Tx, groupID string) ([]Task, error) {
	rows, err := tx.Query(ctx,
		`SELECT id::text, title, detail, owner_user_id::text, due_on, status,
		        COALESCE(closed_by::text, ''), created_by::text, created_at
		 FROM pamoja.task WHERE group_id = $1::uuid
		 ORDER BY (status = 'open') DESC, due_on LIMIT 200`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		var due, created time.Time
		if err := rows.Scan(&t.ID, &t.Title, &t.Detail, &t.OwnerID, &due, &t.Status,
			&t.ClosedBy, &t.CreatedBy, &created); err != nil {
			return nil, err
		}
		t.DueOn = due.Format("2006-01-02")
		t.CreatedAt = created
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Notifications
// ---------------------------------------------------------------------------

// Notification is an in-app message for one user.
type Notification struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Body       string     `json:"body,omitempty"`
	EntityType string     `json:"entity_type,omitempty"`
	EntityID   string     `json:"entity_id,omitempty"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Notify records an in-app notification for the acting user. Row-level
// security only lets a user write their own notifications; delivering to
// another user needs the worker's definer path.
func (s *Service) Notify(ctx context.Context, tx pgx.Tx, userID, kind, title, body, entityType, entityID string) (Notification, error) {
	if err := domain.Required("title", title); err != nil {
		return Notification{}, err
	}
	var out Notification
	err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.notification (user_id, kind, title, body, entity_type, entity_id)
		 VALUES ($1::uuid, $2, $3, $4, $5, NULLIF($6, '')::uuid)
		 RETURNING id::text, kind, title, body, entity_type, COALESCE(entity_id::text, ''), read_at, created_at`,
		userID, kind, title, body, entityType, entityID).
		Scan(&out.ID, &out.Kind, &out.Title, &out.Body, &out.EntityType, &out.EntityID, &out.ReadAt, &out.CreatedAt)
	return out, err
}

// ListNotifications returns the caller's newest notifications.
func (s *Service) ListNotifications(ctx context.Context, tx pgx.Tx, userID string, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx,
		`SELECT id::text, kind, title, body, entity_type, COALESCE(entity_id::text, ''), read_at, created_at
		 FROM pamoja.notification WHERE user_id = $1::uuid
		 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.EntityType, &n.EntityID, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkNotificationRead stamps the read time on one of the caller's own
// notifications.
func (s *Service) MarkNotificationRead(ctx context.Context, tx pgx.Tx, notificationID, userID string) error {
	tag, err := tx.Exec(ctx,
		`UPDATE pamoja.notification SET read_at = COALESCE(read_at, now())
		 WHERE id = $1::uuid AND user_id = $2::uuid`, notificationID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
