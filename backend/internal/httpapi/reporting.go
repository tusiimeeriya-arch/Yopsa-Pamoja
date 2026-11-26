package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/rbac"
	"github.com/yopsa/pamoja/backend/internal/reporting"
)

func (d Deps) handleDashboard(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleDashboard, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var out reporting.Dashboard
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = d.Reports.Dashboard(ctx, tx, groupID)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (d Deps) handleDashboardCard(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	card := idFrom(r, "card")
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleDashboard, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var rows []map[string]any
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		rows, err = d.Reports.DrillDown(ctx, tx, groupID, card, 100)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"card": card, "rows": rows})
}

type createExportRequest struct {
	Kind   string         `json:"kind"`
	Params map[string]any `json:"params"`
}

func (d Deps) handleCreateExport(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleReports, rbac.ActionExport, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req createExportRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		export, _, err := d.Reports.Generate(ctx, tx, groupID, req.Kind, req.Params, p.UserID.String())
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "reports.export", EntityType: "report_export", EntityID: export.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"kind": export.Kind, "row_count": export.RowCount,
				"sha256": export.SHA256},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, export, nil
	})
}

func (d Deps) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	exportID := idFrom(r, "exportID")

	// Authorise against the export's own group before reading its content.
	var groupID string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT group_id::text FROM pamoja.report_export WHERE id = $1::uuid`, exportID).Scan(&groupID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		d.writeDomainError(w, r, domain.ErrNotFound)
		return
	}
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleReports, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}

	var export reporting.Export
	var content string
	err = d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		export, content, err = d.Reports.GetExport(ctx, tx, exportID)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+export.Kind+`-`+export.ID+`.csv"`)
	w.Header().Set("X-Content-SHA256", export.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(content))
}

func (d Deps) handleGroupRanking(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleRanking, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var snapshot reporting.Snapshot
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		snapshot, err = d.Reports.LatestSnapshot(ctx, tx, groupID)
		return err
	})
	if errors.Is(err, reporting.ErrNoSnapshot) {
		writeJSON(w, http.StatusOK, map[string]any{"group_id": groupID, "snapshot": nil})
		return
	}
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group_id": groupID, "snapshot": snapshot})
}

func (d Deps) handleCreateRankingSnapshot(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleRanking, rbac.ActionConfigure, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, _ []byte) (int, any, error) {
		snapshot, err := d.Reports.Snapshot(ctx, tx, groupID, p.UserID.String())
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "ranking.snapshot.create", EntityType: "ranking_snapshot", EntityID: snapshot.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"score": snapshot.Score, "period_end": snapshot.PeriodEnd,
				"rank_in_branch": snapshot.RankInBranch},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, snapshot, nil
	})
}
