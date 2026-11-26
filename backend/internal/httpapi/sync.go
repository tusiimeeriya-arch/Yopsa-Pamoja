package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/offline"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

func (d Deps) handleSyncBatch(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionCreate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req offline.SubmitInput
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		for _, op := range req.Operations {
			if op.Kind == offline.KindAttendanceCheckIn {
				if err := d.authorizeGroup(ctx, p, rbac.ModuleMeetings, rbac.ActionCheckIn, groupID, RequestIDFrom(ctx)); err != nil {
					return 0, nil, err
				}
				break
			}
		}
		req.GroupID = groupID
		req.ActorID = p.UserID.String()
		result, err := d.Offline.Submit(ctx, tx, req)
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "offline.sync.submit", EntityType: "sync_batch", EntityID: result.BatchID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"client_id": req.ClientID, "applied": result.Applied,
				"conflicts": result.Conflicts, "skew_ms": result.SkewMS, "status": result.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, result, nil
	})
}

// syncBatchView is the stored batch as the API returns it.
type syncBatchView struct {
	ID             string    `json:"id"`
	ClientID       string    `json:"client_id"`
	ClientTime     time.Time `json:"client_time"`
	ServerTime     time.Time `json:"server_time"`
	SkewMS         int64     `json:"skew_ms"`
	OperationCount int       `json:"operation_count"`
	AppliedCount   int       `json:"applied_count"`
	ConflictCount  int       `json:"conflict_count"`
	Status         string    `json:"status"`
}

func (d Deps) handleSyncBatchStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	batchID := idFrom(r, "batchID")
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var batch syncBatchView
	var operations []map[string]any
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT id::text, client_id, client_time, server_time, skew_ms,
			        operation_count, applied_count, conflict_count, status
			 FROM pamoja.sync_batch WHERE id = $1::uuid AND group_id = $2::uuid`,
			batchID, groupID).
			Scan(&batch.ID, &batch.ClientID, &batch.ClientTime, &batch.ServerTime,
				&batch.SkewMS, &batch.OperationCount, &batch.AppliedCount,
				&batch.ConflictCount, &batch.Status); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT seq, kind, status, conflict_reason, result
			 FROM pamoja.sync_operation WHERE batch_id = $1::uuid ORDER BY seq`, batchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		operations = []map[string]any{}
		for rows.Next() {
			var seq int
			var kind, status, reason string
			var result []byte
			if err := rows.Scan(&seq, &kind, &status, &reason, &result); err != nil {
				return err
			}
			operations = append(operations, map[string]any{
				"seq": seq, "kind": kind, "status": status,
				"conflict_reason": reason, "result": string(result),
			})
		}
		return rows.Err()
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batch": batch, "operations": operations})
}
