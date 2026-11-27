package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/engagement"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

type progressRequest struct {
	ItemCode string `json:"item_code"`
	MemberID string `json:"member_id"`
	Status   string `json:"status"`
}

func (d Deps) handleRecordProgress(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLearning, rbac.ActionComplete, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req progressRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		out, err := d.Engagement.RecordProgress(ctx, tx, engagement.RecordProgressInput{
			GroupID: groupID, ItemCode: req.ItemCode, MemberID: req.MemberID,
			Status: req.Status, ActorID: p.UserID.String(),
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "learning.progress.record", EntityType: "learning_progress", EntityID: out.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"item_code": out.ItemCode, "member_id": out.MemberID, "status": out.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, out, nil
	})
}

func (d Deps) handleListProgress(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLearning, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var out []engagement.Progress
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = d.Engagement.ListProgress(ctx, tx, groupID, r.URL.Query().Get("item_code"))
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"progress": out})
}

type commentRequest struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Body       string `json:"body"`
	Visibility string `json:"visibility"`
}

func (d Deps) handleAddComment(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleComments, rbac.ActionCreate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req commentRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		out, err := d.Engagement.AddComment(ctx, tx, engagement.AddCommentInput{
			GroupID: groupID, EntityType: req.EntityType, EntityID: req.EntityID,
			AuthorID: p.UserID.String(), Body: req.Body, Visibility: req.Visibility,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "comments.create", EntityType: "comment", EntityID: out.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"on": out.EntityType, "entity_id": out.EntityID, "visibility": out.Visibility},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, out, nil
	})
}

func (d Deps) handleListComments(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleComments, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	entityType := r.URL.Query().Get("entity_type")
	entityID := r.URL.Query().Get("entity_id")
	if entityType == "" || entityID == "" {
		d.writeDomainError(w, r, domain.Invalid("entity_id", "entity_type and entity_id are required"))
		return
	}
	var out []engagement.Comment
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = d.Engagement.ListComments(ctx, tx, groupID, entityType, entityID, false)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": out})
}

type moderateRequest struct {
	Hide bool `json:"hide"`
}

func (d Deps) handleModerateComment(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	commentID := idFrom(r, "commentID")
	groupID, err := d.commentGroup(r, commentID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleComments, rbac.ActionModerate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req moderateRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		if err := d.Engagement.ModerateComment(ctx, tx, commentID, p.UserID.String(), req.Hide); err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "comments.moderate", EntityType: "comment", EntityID: commentID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"hidden": req.Hide},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]any{"id": commentID, "hidden": req.Hide}, nil
	})
}

type disputeRequest struct {
	Kind        string `json:"kind"`
	SubjectRef  string `json:"subject_ref"`
	MemberID    string `json:"member_id"`
	Description string `json:"description"`
}

func (d Deps) handleRaiseDispute(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleDisputes, rbac.ActionCreate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req disputeRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		out, err := d.Engagement.RaiseDispute(ctx, tx, engagement.RaiseDisputeInput{
			GroupID: groupID, Kind: req.Kind, SubjectRef: req.SubjectRef,
			MemberID: req.MemberID, RaisedBy: p.UserID.String(), Description: req.Description,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "disputes.raise", EntityType: "dispute", EntityID: out.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"kind": out.Kind, "subject_ref": out.SubjectRef},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, out, nil
	})
}

type resolveDisputeRequest struct {
	Status string `json:"status"`
	Note   string `json:"resolution_note"`
}

func (d Deps) handleResolveDispute(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	disputeID := idFrom(r, "disputeID")
	groupID, err := d.disputeGroup(r, disputeID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleDisputes, rbac.ActionResolve, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req resolveDisputeRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		out, err := d.Engagement.ResolveDispute(ctx, tx, disputeID, p.UserID.String(), req.Status, req.Note)
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "disputes.resolve", EntityType: "dispute", EntityID: disputeID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"status": out.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, out, nil
	})
}

func (d Deps) handleListDisputes(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleDisputes, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var out []engagement.Dispute
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = d.Engagement.ListDisputes(ctx, tx, groupID)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disputes": out})
}

type taskRequest struct {
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	OwnerUserID string `json:"owner_user_id"`
	DueOn       string `json:"due_on"`
}

func (d Deps) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleTasks, rbac.ActionCreate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req taskRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		dueOn, err := time.Parse("2006-01-02", req.DueOn)
		if err != nil {
			return 0, nil, domain.Invalid("due_on", "must be YYYY-MM-DD")
		}
		out, err := d.Engagement.CreateTask(ctx, tx, engagement.CreateTaskInput{
			GroupID: groupID, Title: req.Title, Detail: req.Detail,
			OwnerUserID: req.OwnerUserID, DueOn: dueOn, CreatedBy: p.UserID.String(),
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "tasks.create", EntityType: "task", EntityID: out.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"owner_user_id": out.OwnerID, "due_on": out.DueOn},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, out, nil
	})
}

type closeTaskRequest struct {
	Status string `json:"status"`
}

func (d Deps) handleCloseTask(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	taskID := idFrom(r, "taskID")
	groupID, err := d.taskGroup(r, taskID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleTasks, rbac.ActionClose, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req closeTaskRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		out, err := d.Engagement.CloseTask(ctx, tx, taskID, p.UserID.String(), req.Status)
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "tasks.close", EntityType: "task", EntityID: taskID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"status": out.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, out, nil
	})
}

func (d Deps) handleListTasks(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleTasks, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var out []engagement.Task
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = d.Engagement.ListTasks(ctx, tx, groupID)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

func (d Deps) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if err := d.authorizeSelf(r.Context(), p, rbac.ModuleNotifications, rbac.ActionView); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var out []engagement.Notification
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = d.Engagement.ListNotifications(ctx, tx, p.UserID.String(), 50)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}

func (d Deps) handleReadNotification(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if err := d.authorizeSelf(r.Context(), p, rbac.ModuleNotifications, rbac.ActionView); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	notificationID := idFrom(r, "notificationID")
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return d.Engagement.MarkNotificationRead(ctx, tx, notificationID, p.UserID.String())
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": notificationID, "read": true})
}

// --- group lookups for record-scoped routes --------------------------------

func (d Deps) commentGroup(r *http.Request, id string) (string, error) {
	return d.scopeGroupOf(r, "pamoja.comment", id)
}

func (d Deps) disputeGroup(r *http.Request, id string) (string, error) {
	return d.scopeGroupOf(r, "pamoja.dispute", id)
}

func (d Deps) taskGroup(r *http.Request, id string) (string, error) {
	return d.scopeGroupOf(r, "pamoja.task", id)
}

// scopeGroupOf reads a record's group inside the caller's scope.
func (d Deps) scopeGroupOf(r *http.Request, table, id string) (string, error) {
	if id == "" {
		return "", domain.ErrNotFound
	}
	var groupID string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT group_id::text FROM `+table+` WHERE id = $1::uuid`, id).Scan(&groupID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return groupID, err
}
