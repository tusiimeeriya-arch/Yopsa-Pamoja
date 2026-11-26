package reporting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
)

// Export is the audit record of one generated file.
type Export struct {
	ID          string    `json:"id"`
	GroupID     string    `json:"group_id"`
	Kind        string    `json:"kind"`
	RowCount    int       `json:"row_count"`
	SHA256      string    `json:"sha256"`
	GeneratedBy string    `json:"generated_by"`
	GeneratedAt time.Time `json:"generated_at"`
}

// exportQueries are the audited report kinds. Every query is group-scoped.
var exportQueries = map[string]string{
	"members": `
		SELECT mem.full_name, ms.member_no, ms.status, ms.joined_on::text, ms.left_on::text
		FROM pamoja.membership ms
		JOIN pamoja.member mem ON mem.id = ms.member_id
		WHERE ms.group_id = $1::uuid
		ORDER BY mem.full_name LIMIT ` + "5000",
	"attendance": `
		SELECT m.scheduled_for::date::text AS meeting_date, mem.full_name, ma.status,
		       ma.checkin_channel, ma.confirmed_at::text
		FROM pamoja.meeting_attendance ma
		JOIN pamoja.meeting m ON m.id = ma.meeting_id
		JOIN pamoja.member mem ON mem.id = ma.member_id
		WHERE ma.group_id = $1::uuid
		ORDER BY m.scheduled_for DESC, mem.full_name LIMIT ` + "5000",
	"payments": `
		SELECT pe.paid_on::text, mem.full_name, pe.category, pe.amount_ugx, pe.channel,
		       pe.status, pe.recorded_via
		FROM pamoja.payment_entry pe
		JOIN pamoja.member mem ON mem.id = pe.member_id
		WHERE pe.group_id = $1::uuid
		ORDER BY pe.paid_on DESC LIMIT ` + "5000",
	"loans": `
		SELECT a.id::text AS application_id, p.code AS product, a.loan_kind, a.status,
		       a.requested_amount_ugx, a.term_months, a.created_at::date::text
		FROM pamoja.loan_application a
		JOIN pamoja.loan_product p ON p.id = a.product_id
		WHERE a.group_id = $1::uuid
		ORDER BY a.created_at DESC LIMIT ` + "5000",
	"arrears": `
		SELECT a.id::text AS application_id, i.due_on::text, i.amount_ugx, i.paid_ugx,
		       (i.amount_ugx - i.paid_ugx) AS overdue_ugx
		FROM pamoja.loan_installment i
		JOIN pamoja.loan_application a ON a.id = i.application_id
		WHERE a.group_id = $1::uuid AND a.status = 'disbursed'
		  AND i.status <> 'paid' AND i.due_on < current_date
		ORDER BY i.due_on LIMIT ` + "5000",
}

// Generate builds a CSV from the ledger, hashes it and records the export. The
// stored content is what the caller downloads, so the hash always matches.
func (s *Service) Generate(ctx context.Context, tx pgx.Tx, groupID, kind string, params map[string]any, actorID string) (Export, string, error) {
	query, ok := exportQueries[kind]
	if !ok {
		return Export{}, "", domain.Invalid("kind", "is not a known report kind")
	}
	if params == nil {
		params = map[string]any{}
	}
	encodedParams, err := json.Marshal(params)
	if err != nil {
		return Export{}, "", domain.Invalid("params", "must be a JSON object")
	}

	rows, err := tx.Query(ctx, query, groupID)
	if err != nil {
		return Export{}, "", err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	header := make([]string, len(fields))
	for i, f := range fields {
		header[i] = string(f.Name)
	}
	if err := writer.Write(header); err != nil {
		return Export{}, "", err
	}

	count := 0
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return Export{}, "", err
		}
		record := make([]string, len(values))
		for i, v := range values {
			record[i] = csvValue(v)
		}
		if err := writer.Write(record); err != nil {
			return Export{}, "", err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return Export{}, "", err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return Export{}, "", err
	}

	content := buf.String()
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])

	var out Export
	err = tx.QueryRow(ctx,
		`INSERT INTO pamoja.report_export
		   (legal_entity_id, branch_id, group_id, kind, params, row_count,
		    content_sha256, content, generated_by)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2, $3::jsonb, $4, $5, $6, $7::uuid
		 FROM pamoja.groups g WHERE g.id = $1::uuid
		 RETURNING id::text, group_id::text, kind, row_count, content_sha256, generated_by::text, generated_at`,
		groupID, kind, string(encodedParams), count, digest, content, actorID).
		Scan(&out.ID, &out.GroupID, &out.Kind, &out.RowCount, &out.SHA256, &out.GeneratedBy, &out.GeneratedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Export{}, "", domain.ErrNotFound
		}
		return Export{}, "", err
	}
	return out, content, nil
}

// GetExport returns a stored export and its exact content.
func (s *Service) GetExport(ctx context.Context, tx pgx.Tx, exportID string) (Export, string, error) {
	var out Export
	var content string
	err := tx.QueryRow(ctx,
		`SELECT id::text, group_id::text, kind, row_count, content_sha256,
		        generated_by::text, generated_at, content
		 FROM pamoja.report_export WHERE id = $1::uuid`, exportID).
		Scan(&out.ID, &out.GroupID, &out.Kind, &out.RowCount, &out.SHA256,
			&out.GeneratedBy, &out.GeneratedAt, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return Export{}, "", domain.ErrNotFound
	}
	if err != nil {
		return Export{}, "", err
	}
	return out, content, nil
}

// csvValue renders a database value for a CSV cell without inventing data.
func csvValue(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case time.Time:
		return value.UTC().Format(time.RFC3339)
	case []byte:
		return string(value)
	default:
		return fmt.Sprint(value)
	}
}
