package groups

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/platform/keyring"
	"github.com/yopsa/pamoja/backend/internal/platform/phone"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// Options configures a Service.
type Options struct {
	Pool   *pgxpool.Pool
	Matrix *rbac.Matrix
	Keys   keyring.Provider
	Now    func() time.Time
}

// Service implements the registration domain.
type Service struct {
	pool   *pgxpool.Pool
	matrix *rbac.Matrix
	keys   keyring.Provider
	now    func() time.Time
}

// NewService builds the registration service.
func NewService(o Options) (*Service, error) {
	if o.Pool == nil {
		return nil, errors.New("groups: database pool is required")
	}
	if o.Keys == nil {
		return nil, errors.New("groups: keyring is required")
	}
	matrix := o.Matrix
	if matrix == nil {
		loaded, err := rbac.Load()
		if err != nil {
			return nil, err
		}
		matrix = loaded
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: o.Pool, matrix: matrix, keys: o.Keys, now: now}, nil
}

func scopeIDs(actor rbac.Actor) []string {
	seen := map[string]bool{}
	var ids []string
	for _, binding := range actor.Bindings {
		for _, id := range binding.ScopeIDs {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) withActor(ctx context.Context, actor rbac.Actor, requestID string, fn db.TxFunc) error {
	return db.WithActor(ctx, s.pool,
		db.Actor{UserID: actor.UserID, Scopes: scopeIDs(actor)}, requestID, fn)
}

func (s *Service) authorize(actor rbac.Actor, action rbac.Action, path []rbac.ScopeRef) (rbac.Decision, error) {
	decision := s.matrix.Authorize(actor, rbac.ModuleRegistration, action, rbac.Resource{
		Module:    rbac.ModuleRegistration,
		ScopePath: path,
	})
	if !decision.Allowed {
		return decision, fmt.Errorf("%w: %s", ErrForbidden, decision.Reason)
	}
	return decision, nil
}

func branchPath(legalEntityID, branchID string) []rbac.ScopeRef {
	return []rbac.ScopeRef{
		{Type: rbac.ScopeLegalEntity, ID: legalEntityID},
		{Type: rbac.ScopeBranch, ID: branchID},
	}
}

func groupPath(g *Group) []rbac.ScopeRef {
	return []rbac.ScopeRef{
		{Type: rbac.ScopeLegalEntity, ID: g.LegalEntityID},
		{Type: rbac.ScopeBranch, ID: g.BranchID},
		{Type: rbac.ScopeGroup, ID: g.ID},
	}
}

// ListLegalEntities returns the legal entities the caller may see.
func (s *Service) ListLegalEntities(ctx context.Context, actor rbac.Actor, requestID string) ([]LegalEntity, error) {
	out := []LegalEntity{}
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id::text, code, name, status FROM pamoja.legal_entity ORDER BY name`)
		if err != nil {
			return mapDBError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var entity LegalEntity
			if err := rows.Scan(&entity.ID, &entity.Code, &entity.Name, &entity.Status); err != nil {
				return mapDBError(err)
			}
			if _, err := s.authorize(actor, rbac.ActionView,
				[]rbac.ScopeRef{{Type: rbac.ScopeLegalEntity, ID: entity.ID}}); err != nil {
				continue // RLS already scoped the row; policy is defence in depth.
			}
			out = append(out, entity)
		}
		return mapDBError(rows.Err())
	})
	return out, err
}

// ListBranches returns the branches of one legal entity.
func (s *Service) ListBranches(ctx context.Context, actor rbac.Actor, legalEntityID, requestID string) ([]Branch, error) {
	if _, err := uuid.Parse(legalEntityID); err != nil {
		return nil, fmt.Errorf("%w: legal_entity_id must be a UUID", ErrValidation)
	}
	out := []Branch{}
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.authorize(actor, rbac.ActionView,
			[]rbac.ScopeRef{{Type: rbac.ScopeLegalEntity, ID: legalEntityID}}); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT id::text, legal_entity_id::text, code, name, district, status
			 FROM pamoja.branch WHERE legal_entity_id = $1::uuid ORDER BY name`,
			legalEntityID)
		if err != nil {
			return mapDBError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var branch Branch
			if err := rows.Scan(&branch.ID, &branch.LegalEntityID, &branch.Code,
				&branch.Name, &branch.District, &branch.Status); err != nil {
				return mapDBError(err)
			}
			out = append(out, branch)
		}
		return mapDBError(rows.Err())
	})
	return out, err
}

// ListGroupTypes returns the reference data for group forms. It carries no
// personal or financial data, so the HTTP layer exposes it without a session.
func (s *Service) ListGroupTypes(ctx context.Context) ([]GroupType, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT code, name, rules_version, description FROM pamoja.group_type ORDER BY name`)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()
	out := []GroupType{}
	for rows.Next() {
		var item GroupType
		if err := rows.Scan(&item.Code, &item.Name, &item.RulesVersion, &item.Description); err != nil {
			return nil, mapDBError(err)
		}
		out = append(out, item)
	}
	return out, mapDBError(rows.Err())
}

const groupColumns = `id::text, legal_entity_id::text, branch_id::text, type_code, name,
	registration_status, status, registration_evidence_id::text, formed_on::text,
	closed_on::text, created_by::text, reviewed_by::text, reviewed_at, review_note,
	version, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanGroup(row rowScanner) (*Group, error) {
	var g Group
	err := row.Scan(&g.ID, &g.LegalEntityID, &g.BranchID, &g.TypeCode, &g.Name,
		&g.RegistrationStatus, &g.Status, &g.RegistrationEvidenceID, &g.FormedOn,
		&g.ClosedOn, &g.CreatedBy, &g.ReviewedBy, &g.ReviewedAt, &g.ReviewNote,
		&g.Version, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, mapDBError(err)
	}
	return &g, nil
}

func (s *Service) loadGroup(ctx context.Context, tx pgx.Tx, groupID string) (*Group, error) {
	if _, err := uuid.Parse(groupID); err != nil {
		return nil, fmt.Errorf("%w: group id must be a UUID", ErrValidation)
	}
	return scanGroup(tx.QueryRow(ctx,
		`SELECT `+groupColumns+` FROM pamoja.groups WHERE id = $1::uuid`, groupID))
}

// CreateGroup registers a group in draft and records its first branch
// assignment.
func (s *Service) CreateGroup(ctx context.Context, actor rbac.Actor, in CreateGroupInput, requestID string) (*Group, error) {
	name, err := validateText("name", in.Name, 2, 200)
	if err != nil {
		return nil, err
	}
	formedOn, err := parseDate("formed_on", in.FormedOn, true, s.now())
	if err != nil {
		return nil, err
	}
	typeCode := strings.TrimSpace(in.TypeCode)
	if typeCode == "" {
		return nil, fmt.Errorf("%w: type_code is required", ErrValidation)
	}
	if _, err := uuid.Parse(in.BranchID); err != nil {
		return nil, fmt.Errorf("%w: branch_id must be a UUID", ErrValidation)
	}
	evidenceID := strings.TrimSpace(in.RegistrationEvidenceID)
	if evidenceID != "" {
		if _, err := uuid.Parse(evidenceID); err != nil {
			return nil, fmt.Errorf("%w: registration_evidence_id must be a UUID", ErrValidation)
		}
	}

	var created *Group
	err = s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		var legalEntityID string
		err := tx.QueryRow(ctx,
			`SELECT legal_entity_id::text FROM pamoja.branch
			 WHERE id = $1::uuid AND status = 'active'`, in.BranchID).Scan(&legalEntityID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: branch", ErrNotFound)
		}
		if err != nil {
			return mapDBError(err)
		}
		var typeExists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pamoja.group_type WHERE code = $1)`,
			typeCode).Scan(&typeExists); err != nil {
			return mapDBError(err)
		}
		if !typeExists {
			return fmt.Errorf("%w: unknown group type", ErrValidation)
		}

		decision, err := s.authorize(actor, rbac.ActionCreate,
			branchPath(legalEntityID, in.BranchID))
		if err != nil {
			return err
		}

		group, err := scanGroup(tx.QueryRow(ctx,
			`INSERT INTO pamoja.groups
			    (legal_entity_id, branch_id, type_code, name,
			     registration_evidence_id, formed_on, created_by)
			 VALUES ($1::uuid, $2::uuid, $3, $4, NULLIF($5, '')::uuid, $6::date, $7::uuid)
			 RETURNING `+groupColumns,
			legalEntityID, in.BranchID, typeCode, name, evidenceID,
			formedOn.Format("2006-01-02"), actor.UserID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.group_branch_history (group_id, branch_id, effective_from)
			 VALUES ($1::uuid, $2::uuid, $3::date)`,
			group.ID, in.BranchID, formedOn.Format("2006-01-02")); err != nil {
			return mapDBError(err)
		}

		if err := s.record(ctx, tx, requestID, actor.UserID, decision,
			"registration.group.create", "group", group.ID, in.BranchID,
			nil, map[string]any{
				"name":                group.Name,
				"registration_status": group.RegistrationStatus,
				"branch_id":           group.BranchID,
				"type_code":           group.TypeCode,
			}, ""); err != nil {
			return err
		}
		created = group
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// GetGroup returns one group the caller may see.
func (s *Service) GetGroup(ctx context.Context, actor rbac.Actor, groupID, requestID string) (*Group, error) {
	var out *Group
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if _, err := s.authorize(actor, rbac.ActionView, groupPath(group)); err != nil {
			return err
		}
		out = group
		return nil
	})
	return out, err
}

// ListGroups lists groups in one branch or legal entity.
func (s *Service) ListGroups(ctx context.Context, actor rbac.Actor, branchID, legalEntityID, requestID string) ([]Group, error) {
	if (branchID == "") == (legalEntityID == "") {
		return nil, fmt.Errorf("%w: supply exactly one of branch_id or legal_entity_id", ErrValidation)
	}
	out := []Group{}
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		var query string
		var args []any
		if branchID != "" {
			if _, err := uuid.Parse(branchID); err != nil {
				return fmt.Errorf("%w: branch_id must be a UUID", ErrValidation)
			}
			var entityID string
			err := tx.QueryRow(ctx,
				`SELECT legal_entity_id::text FROM pamoja.branch WHERE id = $1::uuid`,
				branchID).Scan(&entityID)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: branch", ErrNotFound)
			}
			if err != nil {
				return mapDBError(err)
			}
			if _, err := s.authorize(actor, rbac.ActionView,
				branchPath(entityID, branchID)); err != nil {
				return err
			}
			query = `SELECT ` + groupColumns + ` FROM pamoja.groups
			         WHERE branch_id = $1::uuid ORDER BY name`
			args = []any{branchID}
		} else {
			if _, err := uuid.Parse(legalEntityID); err != nil {
				return fmt.Errorf("%w: legal_entity_id must be a UUID", ErrValidation)
			}
			if _, err := s.authorize(actor, rbac.ActionView,
				[]rbac.ScopeRef{{Type: rbac.ScopeLegalEntity, ID: legalEntityID}}); err != nil {
				return err
			}
			query = `SELECT ` + groupColumns + ` FROM pamoja.groups
			         WHERE legal_entity_id = $1::uuid ORDER BY name`
			args = []any{legalEntityID}
		}
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return mapDBError(err)
		}
		defer rows.Close()
		for rows.Next() {
			group, err := scanGroup(rows)
			if err != nil {
				return err
			}
			out = append(out, *group)
		}
		return mapDBError(rows.Err())
	})
	return out, err
}

// UpdateGroup edits a draft or returned group.
func (s *Service) UpdateGroup(ctx context.Context, actor rbac.Actor, groupID string, in UpdateGroupInput, requestID string) (*Group, error) {
	var updated *Group
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		decision, err := s.authorize(actor, rbac.ActionEditDraft, groupPath(group))
		if err != nil {
			return err
		}
		if group.RegistrationStatus != "draft" && group.RegistrationStatus != "returned" {
			return fmt.Errorf("%w: only a draft or returned group can be edited", ErrConflict)
		}
		if in.Version != group.Version {
			return fmt.Errorf("%w: group was changed by someone else", ErrConflict)
		}

		name := group.Name
		if in.Name != nil {
			name, err = validateText("name", *in.Name, 2, 200)
			if err != nil {
				return err
			}
		}
		typeCode := group.TypeCode
		if in.TypeCode != nil {
			typeCode = strings.TrimSpace(*in.TypeCode)
			var typeExists bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM pamoja.group_type WHERE code = $1)`,
				typeCode).Scan(&typeExists); err != nil {
				return mapDBError(err)
			}
			if !typeExists {
				return fmt.Errorf("%w: unknown group type", ErrValidation)
			}
		}
		formedOn := ""
		if group.FormedOn != nil {
			formedOn = *group.FormedOn
		}
		if in.FormedOn != nil {
			parsed, err := parseDate("formed_on", *in.FormedOn, true, s.now())
			if err != nil {
				return err
			}
			formedOn = parsed.Format("2006-01-02")
		}
		if formedOn == "" {
			return fmt.Errorf("%w: formed_on is required", ErrValidation)
		}
		evidenceID := ""
		if group.RegistrationEvidenceID != nil {
			evidenceID = *group.RegistrationEvidenceID
		}
		if in.RegistrationEvidenceID != nil {
			evidenceID = strings.TrimSpace(*in.RegistrationEvidenceID)
			if evidenceID != "" {
				if _, err := uuid.Parse(evidenceID); err != nil {
					return fmt.Errorf("%w: registration_evidence_id must be a UUID", ErrValidation)
				}
			}
		}

		updatedGroup, err := scanGroup(tx.QueryRow(ctx,
			`UPDATE pamoja.groups
			 SET name = $2, type_code = $3, formed_on = $4::date,
			     registration_evidence_id = NULLIF($5, '')::uuid,
			     version = version + 1, updated_at = now()
			 WHERE id = $1::uuid AND version = $6
			 RETURNING `+groupColumns,
			groupID, name, typeCode, formedOn, evidenceID, group.Version))
		if err != nil {
			return err
		}

		if err := s.record(ctx, tx, requestID, actor.UserID, decision,
			"registration.group.update", "group", group.ID,
			group.BranchID,
			groupSnapshot(group), groupSnapshot(updatedGroup), ""); err != nil {
			return err
		}
		updated = updatedGroup
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// SubmitGroup sends a complete draft for review.
func (s *Service) SubmitGroup(ctx context.Context, actor rbac.Actor, groupID string, version int, requestID string) (*Group, error) {
	var submitted *Group
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		decision, err := s.authorize(actor, rbac.ActionSubmit, groupPath(group))
		if err != nil {
			return err
		}
		if group.RegistrationStatus != "draft" && group.RegistrationStatus != "returned" {
			return fmt.Errorf("%w: only a draft or returned group can be submitted", ErrConflict)
		}
		if version != group.Version {
			return fmt.Errorf("%w: group was changed by someone else", ErrConflict)
		}
		if group.FormedOn == nil || group.RegistrationEvidenceID == nil {
			return fmt.Errorf("%w: formed_on and registration evidence are required before submission", ErrValidation)
		}

		var memberCount int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM pamoja.membership
			 WHERE group_id = $1::uuid AND status = 'active'`, groupID).Scan(&memberCount); err != nil {
			return mapDBError(err)
		}
		if memberCount == 0 {
			return fmt.Errorf("%w: a group needs at least one active member before submission", ErrValidation)
		}

		var missingConsent int
		if err := tx.QueryRow(ctx,
			`SELECT count(*)
			 FROM pamoja.membership m
			 WHERE m.group_id = $1::uuid AND m.status = 'active'
			   AND NOT EXISTS (
			       SELECT 1 FROM pamoja.consent_record c
			       WHERE c.member_id = m.member_id
			         AND c.purpose = 'membership'
			         AND c.granted)`, groupID).Scan(&missingConsent); err != nil {
			return mapDBError(err)
		}
		if missingConsent > 0 {
			return fmt.Errorf("%w: every active member must have a granted membership consent", ErrValidation)
		}

		submittedGroup, err := scanGroup(tx.QueryRow(ctx,
			`UPDATE pamoja.groups
			 SET registration_status = 'submitted', version = version + 1, updated_at = now()
			 WHERE id = $1::uuid AND version = $2
			 RETURNING `+groupColumns, groupID, group.Version))
		if err != nil {
			return err
		}

		if err := s.record(ctx, tx, requestID, actor.UserID, decision,
			"registration.group.submit", "group", group.ID,
			group.BranchID,
			groupSnapshot(group), groupSnapshot(submittedGroup), ""); err != nil {
			return err
		}
		submitted = submittedGroup
		return nil
	})
	if err != nil {
		return nil, err
	}
	return submitted, nil
}

// ReviewGroup activates, returns or rejects a submitted group. SD-06 is
// checked here and again by the database trigger.
func (s *Service) ReviewGroup(ctx context.Context, actor rbac.Actor, groupID string, in ReviewInput, requestID string) (*Group, error) {
	decision := strings.TrimSpace(in.Decision)
	switch decision {
	case "activate", "return", "reject":
	default:
		return nil, fmt.Errorf("%w: decision must be activate, return or reject", ErrValidation)
	}
	note := strings.TrimSpace(in.Note)
	if decision != "activate" && note == "" {
		return nil, fmt.Errorf("%w: a reason is required when returning or rejecting", ErrValidation)
	}
	if len(note) > 1000 {
		return nil, fmt.Errorf("%w: note may not exceed 1000 characters", ErrValidation)
	}

	var reviewed *Group
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		authDecision, err := s.authorize(actor, rbac.ActionReviewActivate, groupPath(group))
		if err != nil {
			return err
		}
		if group.RegistrationStatus != "submitted" && group.RegistrationStatus != "under_review" {
			return fmt.Errorf("%w: only a submitted group can be reviewed", ErrConflict)
		}
		if in.Version != group.Version {
			return fmt.Errorf("%w: group was changed by someone else", ErrConflict)
		}
		if err := s.matrix.CheckSeparation(actor.UserID, rbac.ModuleRegistration,
			rbac.ActionReviewActivate, map[rbac.ContextField][]string{
				rbac.CtxCreator: {group.CreatedBy},
			}); err != nil {
			return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
		}

		version := group.Version
		if group.RegistrationStatus == "submitted" {
			var underReview int
			err := tx.QueryRow(ctx,
				`UPDATE pamoja.groups
				 SET registration_status = 'under_review', version = version + 1, updated_at = now()
				 WHERE id = $1::uuid AND version = $2 AND registration_status = 'submitted'
				 RETURNING version`, groupID, version).Scan(&underReview)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: group was changed by someone else", ErrConflict)
			}
			if err != nil {
				return mapDBError(err)
			}
			version = underReview
		}

		status := "active"
		operational := "active"
		switch decision {
		case "return":
			status = "returned"
			operational = "pending"
		case "reject":
			status = "rejected"
			operational = "pending"
		}

		reviewedGroup, err := scanGroup(tx.QueryRow(ctx,
			`UPDATE pamoja.groups
			 SET registration_status = $2, status = $3, reviewed_by = $4::uuid,
			     reviewed_at = now(), review_note = $5, version = version + 1,
			     updated_at = now()
			 WHERE id = $1::uuid AND version = $6
			 RETURNING `+groupColumns,
			groupID, status, operational, actor.UserID, note, version))
		if err != nil {
			return err
		}

		if err := s.record(ctx, tx, requestID, actor.UserID, authDecision,
			"registration.group.review", "group", group.ID,
			group.BranchID,
			groupSnapshot(group), groupSnapshot(reviewedGroup), note); err != nil {
			return err
		}
		reviewed = reviewedGroup
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reviewed, nil
}

// ListMemberships returns the members of one group.
func (s *Service) ListMemberships(ctx context.Context, actor rbac.Actor, groupID, requestID string) ([]Membership, error) {
	out := []Membership{}
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if _, err := s.authorize(actor, rbac.ActionView, groupPath(group)); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT ms.id::text, ms.member_id::text, ms.group_id::text, ms.member_no,
			        me.full_name, me.gender, ms.joined_on::text, ms.left_on::text,
			        ms.status, ms.created_at::text
			 FROM pamoja.membership ms
			 JOIN pamoja.member me ON me.id = ms.member_id
			 WHERE ms.group_id = $1::uuid
			 ORDER BY me.full_name`, groupID)
		if err != nil {
			return mapDBError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var item Membership
			if err := rows.Scan(&item.ID, &item.MemberID, &item.GroupID, &item.MemberNo,
				&item.FullName, &item.Gender, &item.JoinedOn, &item.LeftOn,
				&item.Status, &item.CreatedAt); err != nil {
				return mapDBError(err)
			}
			out = append(out, item)
		}
		return mapDBError(rows.Err())
	})
	return out, err
}

// AddMembership adds a member to a group, creating the member when the phone
// hash is new.
func (s *Service) AddMembership(ctx context.Context, actor rbac.Actor, groupID string, in AddMembershipInput, requestID string) (*Membership, error) {
	fullName, err := validateText("full_name", in.FullName, 2, 200)
	if err != nil {
		return nil, err
	}
	phone := phone.Normalize(in.Phone)
	if len(phone) < 9 {
		return nil, fmt.Errorf("%w: phone must contain at least 9 digits", ErrValidation)
	}
	gender := strings.TrimSpace(in.Gender)
	if gender == "" {
		gender = "undisclosed"
	}
	if !validGender(gender) {
		return nil, fmt.Errorf("%w: gender must be female, male, other or undisclosed", ErrValidation)
	}
	memberNo, err := validateText("member_no", in.MemberNo, 1, 32)
	if err != nil {
		return nil, err
	}
	joinedOn, err := parseDate("joined_on", in.JoinedOn, true, s.now())
	if err != nil {
		return nil, err
	}
	var dateOfBirth *time.Time
	if strings.TrimSpace(in.DateOfBirth) != "" {
		dateOfBirth, err = parseDate("date_of_birth", in.DateOfBirth, false, s.now())
		if err != nil {
			return nil, err
		}
	}

	phoneHash := s.keys.LookupHash(phone)
	var sealed []byte
	if sealed, err = s.keys.Encrypt([]byte(phone)); err != nil {
		return nil, fmt.Errorf("groups: encrypt phone: %w", err)
	}

	var added *Membership
	err = s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		decision, err := s.authorize(actor, rbac.ActionCreate, groupPath(group))
		if err != nil {
			return err
		}
		if group.RegistrationStatus == "rejected" || group.RegistrationStatus == "closed" {
			return fmt.Errorf("%w: a rejected or closed group cannot take new members", ErrConflict)
		}

		var memberID string
		err = tx.QueryRow(ctx,
			`SELECT id::text FROM pamoja.member_by_phone_hash($1)`, phoneHash).Scan(&memberID)
		if errors.Is(err, pgx.ErrNoRows) {
			memberID = uuid.NewString()
			var dob any
			if dateOfBirth != nil {
				dob = dateOfBirth.Format("2006-01-02")
			}
			// The id is generated here so the insert needs no RETURNING: the new
			// member is not yet visible under the member read policy.
			if _, err := tx.Exec(ctx,
				`INSERT INTO pamoja.member
				    (id, full_name, phone_enc, phone_hash, gender, date_of_birth)
				 VALUES ($1::uuid, $2, $3, $4, $5, NULLIF($6, '')::date)`,
				memberID, fullName, sealed, phoneHash, gender, dob); err != nil {
				return mapDBError(err)
			}
		} else if err != nil {
			return mapDBError(err)
		}

		// The id is generated here and the row is read back in a second
		// statement: INSERT ... RETURNING would have to satisfy the membership
		// SELECT policy before the row it just created is visible to it.
		membershipID := uuid.NewString()
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.membership
			    (id, member_id, group_id, member_no, joined_on)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::date)`,
			membershipID, memberID, groupID, memberNo,
			joinedOn.Format("2006-01-02")); err != nil {
			return mapDBError(err)
		}
		var item Membership
		err = tx.QueryRow(ctx,
			`SELECT ms.id::text, ms.member_id::text, ms.group_id::text, ms.member_no,
			        me.full_name, me.gender, ms.joined_on::text, ms.left_on::text,
			        ms.status, ms.created_at::text
			 FROM pamoja.membership ms
			 JOIN pamoja.member me ON me.id = ms.member_id
			 WHERE ms.id = $1::uuid`, membershipID).
			Scan(&item.ID, &item.MemberID, &item.GroupID, &item.MemberNo,
				&item.FullName, &item.Gender, &item.JoinedOn, &item.LeftOn,
				&item.Status, &item.CreatedAt)
		if err != nil {
			return mapDBError(err)
		}

		if err := s.record(ctx, tx, requestID, actor.UserID, decision,
			"registration.membership.create", "membership", item.ID,
			group.BranchID, nil,
			map[string]any{
				"group_id":  groupID,
				"member_no": memberNo,
				"joined_on": joinedOn.Format("2006-01-02"),
			}, ""); err != nil {
			return err
		}
		added = &item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// EndMembership end-dates a membership; the row is never deleted.
func (s *Service) EndMembership(ctx context.Context, actor rbac.Actor, groupID, membershipID string, in EndMembershipInput, requestID string) (*Membership, error) {
	leftOn, err := parseDate("left_on", in.LeftOn, true, s.now())
	if err != nil {
		return nil, err
	}
	var ended *Membership
	err = s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		decision, err := s.authorize(actor, rbac.ActionEditDraft, groupPath(group))
		if err != nil {
			return err
		}
		var item Membership
		err = tx.QueryRow(ctx,
			`UPDATE pamoja.membership ms
			 SET left_on = $3::date, status = 'left', updated_at = now()
			 FROM pamoja.member me
			 WHERE ms.id = $1::uuid AND ms.group_id = $2::uuid
			   AND ms.left_on IS NULL AND me.id = ms.member_id
			 RETURNING ms.id::text, ms.member_id::text, ms.group_id::text, ms.member_no,
			           me.full_name, me.gender, ms.joined_on::text, ms.left_on::text,
			           ms.status, ms.created_at::text`,
			membershipID, groupID, leftOn.Format("2006-01-02")).
			Scan(&item.ID, &item.MemberID, &item.GroupID, &item.MemberNo,
				&item.FullName, &item.Gender, &item.JoinedOn, &item.LeftOn,
				&item.Status, &item.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: active membership", ErrNotFound)
		}
		if err != nil {
			return mapDBError(err)
		}
		if err := s.record(ctx, tx, requestID, actor.UserID, decision,
			"registration.membership.end", "membership", item.ID,
			group.BranchID, nil,
			map[string]any{"left_on": leftOn.Format("2006-01-02")}, ""); err != nil {
			return err
		}
		ended = &item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ended, nil
}

// ListAuthorities returns the appointments of one group.
func (s *Service) ListAuthorities(ctx context.Context, actor rbac.Actor, groupID, requestID string) ([]Authority, error) {
	out := []Authority{}
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if _, err := s.authorize(actor, rbac.ActionView, groupPath(group)); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT a.id::text, a.group_id::text, a.member_id::text, me.full_name,
			        a.office, a.effective_from::text, a.effective_to::text,
			        a.resolution_evidence_id::text, a.recorded_by::text
			 FROM pamoja.authority_record a
			 JOIN pamoja.member me ON me.id = a.member_id
			 WHERE a.group_id = $1::uuid
			 ORDER BY a.office, a.effective_from DESC`, groupID)
		if err != nil {
			return mapDBError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var item Authority
			if err := rows.Scan(&item.ID, &item.GroupID, &item.MemberID, &item.MemberName,
				&item.Office, &item.EffectiveFrom, &item.EffectiveTo,
				&item.ResolutionEvidenceID, &item.RecordedBy); err != nil {
				return mapDBError(err)
			}
			out = append(out, item)
		}
		return mapDBError(rows.Err())
	})
	return out, err
}

// AppointAuthority appoints an active member to a group office.
func (s *Service) AppointAuthority(ctx context.Context, actor rbac.Actor, groupID string, in AppointAuthorityInput, requestID string) (*Authority, error) {
	office := strings.TrimSpace(in.Office)
	if !validOffice(office) {
		return nil, fmt.Errorf("%w: office must be chairperson, secretary or treasurer", ErrValidation)
	}
	effectiveFrom, err := parseDate("effective_from", in.EffectiveFrom, true, s.now())
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(in.MemberID); err != nil {
		return nil, fmt.Errorf("%w: member_id must be a UUID", ErrValidation)
	}
	evidenceID := strings.TrimSpace(in.ResolutionEvidenceID)
	if evidenceID != "" {
		if _, err := uuid.Parse(evidenceID); err != nil {
			return nil, fmt.Errorf("%w: resolution_evidence_id must be a UUID", ErrValidation)
		}
	}

	var appointed *Authority
	err = s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		decision, err := s.authorize(actor, rbac.ActionCreate, groupPath(group))
		if err != nil {
			return err
		}

		var joinedOn string
		err = tx.QueryRow(ctx,
			`SELECT joined_on::text FROM pamoja.membership
			 WHERE group_id = $1::uuid AND member_id = $2::uuid
			   AND left_on IS NULL`, groupID, in.MemberID).Scan(&joinedOn)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: the appointee must be an active member of the group", ErrValidation)
		}
		if err != nil {
			return mapDBError(err)
		}
		if effectiveFrom.Format("2006-01-02") < joinedOn {
			return fmt.Errorf("%w: effective_from may not precede the member's joined_on", ErrValidation)
		}

		var item Authority
		err = tx.QueryRow(ctx,
			`WITH inserted AS (
			    INSERT INTO pamoja.authority_record
			        (group_id, member_id, office, effective_from,
			         resolution_evidence_id, recorded_by)
			    VALUES ($1::uuid, $2::uuid, $3, $4::date, NULLIF($5, '')::uuid, $6::uuid)
			    RETURNING id, group_id, member_id, office, effective_from,
			              effective_to, resolution_evidence_id, recorded_by
			 )
			 SELECT i.id::text, i.group_id::text, i.member_id::text, me.full_name,
			        i.office, i.effective_from::text, i.effective_to::text,
			        i.resolution_evidence_id::text, i.recorded_by::text
			 FROM inserted i JOIN pamoja.member me ON me.id = i.member_id`,
			groupID, in.MemberID, office, effectiveFrom.Format("2006-01-02"),
			evidenceID, actor.UserID).
			Scan(&item.ID, &item.GroupID, &item.MemberID, &item.MemberName,
				&item.Office, &item.EffectiveFrom, &item.EffectiveTo,
				&item.ResolutionEvidenceID, &item.RecordedBy)
		if err != nil {
			return mapDBError(err)
		}

		if err := s.record(ctx, tx, requestID, actor.UserID, decision,
			"registration.authority.appoint", "authority_record", item.ID,
			group.BranchID, nil,
			map[string]any{
				"group_id":       groupID,
				"member_id":      in.MemberID,
				"office":         office,
				"effective_from": item.EffectiveFrom,
			}, ""); err != nil {
			return err
		}
		appointed = &item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return appointed, nil
}

// EndAuthority end-dates an appointment.
func (s *Service) EndAuthority(ctx context.Context, actor rbac.Actor, groupID, authorityID string, in EndAuthorityInput, requestID string) (*Authority, error) {
	effectiveTo, err := parseDate("effective_to", in.EffectiveTo, true, s.now())
	if err != nil {
		return nil, err
	}
	var ended *Authority
	err = s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		decision, err := s.authorize(actor, rbac.ActionEditDraft, groupPath(group))
		if err != nil {
			return err
		}
		var item Authority
		err = tx.QueryRow(ctx,
			`UPDATE pamoja.authority_record a
			 SET effective_to = $3::date
			 WHERE a.id = $1::uuid AND a.group_id = $2::uuid
			   AND a.effective_to IS NULL
			 RETURNING a.id::text, a.group_id::text, a.member_id::text,
			           (SELECT full_name FROM pamoja.member WHERE id = a.member_id),
			           a.office, a.effective_from::text, a.effective_to::text,
			           a.resolution_evidence_id::text, a.recorded_by::text`,
			authorityID, groupID, effectiveTo.Format("2006-01-02")).
			Scan(&item.ID, &item.GroupID, &item.MemberID, &item.MemberName,
				&item.Office, &item.EffectiveFrom, &item.EffectiveTo,
				&item.ResolutionEvidenceID, &item.RecordedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: active authority record", ErrNotFound)
		}
		if err != nil {
			return mapDBError(err)
		}
		if err := s.record(ctx, tx, requestID, actor.UserID, decision,
			"registration.authority.end", "authority_record", item.ID,
			group.BranchID, nil,
			map[string]any{"effective_to": item.EffectiveTo}, ""); err != nil {
			return err
		}
		ended = &item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ended, nil
}

// RecordConsent records a member's decision for one purpose and notice
// version. Repeating the same decision updates the capture time rather than
// creating a duplicate row.
func (s *Service) RecordConsent(ctx context.Context, actor rbac.Actor, memberID string, in RecordConsentInput, requestID string) (*Consent, error) {
	purpose := strings.TrimSpace(in.Purpose)
	if !validConsentPurpose(purpose) {
		return nil, fmt.Errorf("%w: unknown consent purpose", ErrValidation)
	}
	noticeVersion := strings.TrimSpace(in.NoticeVersion)
	if noticeVersion == "" {
		noticeVersion = ConsentNoticeVersion
	}
	if len(noticeVersion) > 32 {
		return nil, fmt.Errorf("%w: notice_version may not exceed 32 characters", ErrValidation)
	}

	var recorded *Consent
	err := s.withActor(ctx, actor, requestID, func(ctx context.Context, tx pgx.Tx) error {
		var groupID string
		err := tx.QueryRow(ctx,
			`SELECT group_id::text FROM pamoja.membership
			 WHERE member_id = $1::uuid AND left_on IS NULL
			 ORDER BY joined_on DESC LIMIT 1`, memberID).Scan(&groupID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: the member has no active membership", ErrValidation)
		}
		if err != nil {
			return mapDBError(err)
		}
		group, err := s.loadGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		decision, err := s.authorize(actor, rbac.ActionCreate, groupPath(group))
		if err != nil {
			return err
		}

		var item Consent
		err = tx.QueryRow(ctx,
			`INSERT INTO pamoja.consent_record
			    (member_id, purpose, granted, notice_version, captured_by)
			 VALUES ($1::uuid, $2, $3, $4, $5::uuid)
			 ON CONFLICT (member_id, purpose, notice_version)
			 DO UPDATE SET granted = EXCLUDED.granted, captured_at = now(),
			               captured_by = EXCLUDED.captured_by
			 RETURNING id::text, member_id::text, purpose, granted, notice_version,
			           captured_at, captured_by::text`,
			memberID, purpose, in.Granted, noticeVersion, actor.UserID).
			Scan(&item.ID, &item.MemberID, &item.Purpose, &item.Granted,
				&item.NoticeVersion, &item.CapturedAt, &item.CapturedBy)
		if err != nil {
			return mapDBError(err)
		}

		if err := s.record(ctx, tx, requestID, actor.UserID, decision,
			"registration.consent.record", "consent_record", item.ID,
			group.BranchID, nil,
			map[string]any{
				"member_id":      memberID,
				"purpose":        purpose,
				"granted":        in.Granted,
				"notice_version": noticeVersion,
			}, ""); err != nil {
			return err
		}
		recorded = &item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return recorded, nil
}

func groupSnapshot(g *Group) map[string]any {
	snapshot := map[string]any{
		"name":                g.Name,
		"type_code":           g.TypeCode,
		"registration_status": g.RegistrationStatus,
		"status":              g.Status,
		"version":             g.Version,
	}
	if g.FormedOn != nil {
		snapshot["formed_on"] = *g.FormedOn
	}
	return snapshot
}

func (s *Service) record(ctx context.Context, tx pgx.Tx, requestID, actorID string,
	decision rbac.Decision, action, entityType, entityID, branchID string,
	before, after map[string]any, reason string) error {
	_, err := audit.New(tx).Append(ctx, requestID, audit.Entry{
		ActorUserID: actorID,
		ActorRole:   string(decision.Role),
		ScopeType:   string(rbac.ScopeBranch),
		ScopeID:     branchID,
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		Before:      before,
		After:       after,
		Reason:      reason,
		Channel:     "api",
	})
	return err
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: duplicate record", ErrConflict)
		case "23503": // foreign_key_violation
			return fmt.Errorf("%w: referenced record does not exist", ErrValidation)
		case "23514": // check_violation, including the lifecycle trigger
			return fmt.Errorf("%w: %s", ErrValidation, pgErr.Message)
		}
	}
	return err
}
