package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/auth"
	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// principal is the authenticated caller type used across handlers.
type principal = auth.Principal

// requestActor is the actor a transaction runs as. Scopes are the scope ids in
// the caller's role bindings; RLS reads them from the transaction settings.
func requestActor(p principal) db.Actor {
	return db.Actor{UserID: p.UserID.String(), Scopes: p.Scopes}
}

// bindings returns the caller's role bindings, including which scope each role
// was granted at. The definer function is executable by the API role and reads
// only the caller's own grants.
func (d Deps) bindings(ctx context.Context, p principal) ([]rbac.Binding, error) {
	if d.Pool == nil {
		return nil, errors.New("httpapi: database not configured")
	}
	rows, err := d.Pool.Query(ctx,
		`SELECT role, legal_entity_id::text, branch_id::text, group_id::text
		 FROM pamoja.auth_roles($1::uuid)`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rbac.Binding
	for rows.Next() {
		var role string
		var legalEntity, branch, group *string
		if err := rows.Scan(&role, &legalEntity, &branch, &group); err != nil {
			return nil, err
		}
		binding := rbac.Binding{Role: rbac.Role(role)}
		switch {
		case group != nil && *group != "":
			binding.Scope = rbac.ScopeGroup
			binding.ScopeIDs = []string{*group}
		case branch != nil && *branch != "":
			binding.Scope = rbac.ScopeBranch
			binding.ScopeIDs = []string{*branch}
		case legalEntity != nil && *legalEntity != "":
			binding.Scope = rbac.ScopeLegalEntity
			binding.ScopeIDs = []string{*legalEntity}
		default:
			binding.Scope = rbac.ScopeSelf
		}
		out = append(out, binding)
	}
	return out, rows.Err()
}

// matrix loads the embedded authorisation policy once per process.
func (d Deps) matrix() (*rbac.Matrix, error) {
	m, err := rbac.Load()
	if err != nil {
		return nil, err
	}
	return m, nil
}

// authorizeGroup checks a group-scoped action. The group's branch and legal
// entity are read inside the caller's RLS scope so a caller cannot probe a
// group outside their bindings; the policy then checks the binding reaches it.
func (d Deps) authorizeGroup(ctx context.Context, p principal, mod rbac.Module, action rbac.Action, groupID, requestID string) error {
	var legalEntity, branch string
	err := db.WithActor(ctx, d.Pool, requestActor(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT legal_entity_id::text, branch_id::text FROM pamoja.groups WHERE id = $1::uuid`,
			groupID).Scan(&legalEntity, &branch)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}

	m, err := d.matrix()
	if err != nil {
		return err
	}
	bindings, err := d.bindings(ctx, p)
	if err != nil {
		return err
	}
	decision := m.Authorize(rbac.Actor{UserID: p.UserID.String(), Bindings: bindings}, mod, action,
		rbac.Resource{Module: mod, ScopePath: []rbac.ScopeRef{
			{Type: rbac.ScopeLegalEntity, ID: legalEntity},
			{Type: rbac.ScopeBranch, ID: branch},
			{Type: rbac.ScopeGroup, ID: groupID},
		}})
	if !decision.Allowed {
		return domain.ErrForbidden
	}
	return nil
}

// authorizeSelf checks a self-scoped action (member records).
func (d Deps) authorizeSelf(ctx context.Context, p principal, mod rbac.Module, action rbac.Action) error {
	m, err := d.matrix()
	if err != nil {
		return err
	}
	bindings, err := d.bindings(ctx, p)
	if err != nil {
		return err
	}
	decision := m.Authorize(rbac.Actor{UserID: p.UserID.String(), Bindings: bindings}, mod, action,
		rbac.Resource{Module: mod, OwnerUserID: p.UserID.String()})
	if !decision.Allowed {
		return domain.ErrForbidden
	}
	return nil
}

// checkSeparation applies the SoD rules for an action.
func (d Deps) checkSeparation(p principal, mod rbac.Module, action rbac.Action, involved map[rbac.ContextField][]string) error {
	m, err := d.matrix()
	if err != nil {
		return err
	}
	if err := m.CheckSeparation(p.UserID.String(), mod, action, involved); err != nil {
		return domain.ErrSeparationOfDuties
	}
	return nil
}

// stepUpFresh enforces a fresh MFA step-up for the financial actions that
// require it. The caller's session carries the time.
func (d Deps) stepUpFresh(p principal) bool {
	if p.StepUpAt == nil {
		return false
	}
	return time.Since(*p.StepUpAt) <= d.Config.Auth.StepUpWindow
}

// idempotent runs a state-changing handler inside one transaction with the
// Idempotency-Key contract: same key + same request replays the stored
// response, same key + different request is 409, and a missing key is 400.
//
// The response is written only after the transaction commits.
func (d Deps) idempotent(w http.ResponseWriter, r *http.Request, route string,
	fn func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error)) {
	p, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 200 {
		writeError(w, r, http.StatusBadRequest, "idempotency_key_required")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	hash := domain.HashRequest(r.Method, route, body)

	var status int
	var response any
	var replay []byte
	err = db.WithActor(r.Context(), d.Pool, requestActor(p), RequestIDFrom(r.Context()),
		func(ctx context.Context, tx pgx.Tx) error {
			storedStatus, storedBody, err := beginIdempotency(ctx, tx, p.UserID.String(), key, route, hash)
			if err != nil {
				return err
			}
			if storedBody != nil {
				status = storedStatus
				replay = storedBody
				return nil
			}
			status, response, err = fn(ctx, tx, body)
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				return err
			}
			return finishIdempotency(ctx, tx, p.UserID.String(), key, status, encoded)
		})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if replay != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(replay)
		return
	}
	writeJSON(w, status, response)
}

// writeDomainError maps service errors to generic responses. Detail stays in
// the audit log and server logs, never in the caller's body.
func (d Deps) writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrSeparationOfDuties):
		writeError(w, r, http.StatusForbidden, "forbidden")
	case errors.Is(err, domain.ErrStepUpRequired):
		writeError(w, r, http.StatusForbidden, "step_up_required")
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		writeError(w, r, http.StatusNotFound, "not_found")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		writeError(w, r, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrInvalidTransition),
		errors.Is(err, domain.ErrMaterialChange):
		writeError(w, r, http.StatusConflict, "conflict")
	case domain.IsValidation(err), errors.Is(err, domain.ErrValidation):
		writeError(w, r, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, domain.ErrPostingFailed):
		writeError(w, r, http.StatusAccepted, "posting_pending")
	default:
		d.logger().Error("request failed",
			"error", err.Error(),
			"path", r.URL.Path,
			"request_id", RequestIDFrom(r.Context()))
		writeError(w, r, http.StatusInternalServerError, "internal_error")
	}
}

func groupIDFrom(r *http.Request) string         { return chi.URLParam(r, "groupID") }
func idFrom(r *http.Request, name string) string { return chi.URLParam(r, name) }
