package domain

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/auth"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// Bindings returns the caller's role bindings with their scope types.
func (s *Service) Bindings(ctx context.Context, userID string) ([]rbac.Binding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT role, scope_type, scope_id::text FROM pamoja.auth_bindings($1::uuid)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	index := map[string]int{}
	bindings := make([]rbac.Binding, 0, 4)
	for rows.Next() {
		var role, scopeType string
		var scopeID *string
		if err := rows.Scan(&role, &scopeType, &scopeID); err != nil {
			return nil, err
		}
		key := role + "|" + scopeType
		i, ok := index[key]
		if !ok {
			i = len(bindings)
			index[key] = i
			bindings = append(bindings, rbac.Binding{Role: rbac.Role(role), Scope: rbac.ScopeType(scopeType)})
		}
		if scopeID != nil && *scopeID != "" {
			bindings[i].ScopeIDs = append(bindings[i].ScopeIDs, *scopeID)
		}
	}
	return bindings, rows.Err()
}

// Me returns the signed-in user's profile and role capabilities.
func (s *Service) Me(ctx context.Context, p auth.Principal, requestID string) (Me, error) {
	var me Me
	me.UserID = p.UserID.String()

	err := db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT full_name, phone_masked, is_staff, mfa_required FROM pamoja.app_user WHERE id = $1::uuid`,
			p.UserID.String()).Scan(&me.FullName, &me.PhoneMasked, &me.IsStaff, &me.MFARequired)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Me{}, ErrNotFound
		}
		return Me{}, err
	}

	bindings, err := s.Bindings(ctx, me.UserID)
	if err != nil {
		return Me{}, err
	}
	me.Roles = make([]RoleBinding, 0, len(bindings))
	me.Can = map[string]bool{}
	for _, b := range bindings {
		scopeID := ""
		if len(b.ScopeIDs) > 0 {
			scopeID = b.ScopeIDs[0]
		}
		me.Roles = append(me.Roles, RoleBinding{Role: string(b.Role), ScopeType: string(b.Scope), ScopeID: scopeID})
		for _, grant := range s.matrix.GrantsFor(b.Role) {
			for _, action := range grant.Actions {
				me.Can[string(grant.Module)+"."+string(action)] = true
			}
		}
	}
	return me, nil
}

// Groups returns the groups the caller may see, with their headline figures.
func (s *Service) Groups(ctx context.Context, p auth.Principal, requestID string) ([]Group, error) {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return nil, err
	}
	var groups []Group
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		all, err := loadGroups(ctx, tx)
		if err != nil {
			return err
		}
		for _, g := range all {
			if s.canViewGroup(bindings, p.UserID.String(), g) {
				groups = append(groups, g)
			}
		}
		return nil
	})
	if groups == nil {
		groups = []Group{}
	}
	return groups, err
}

// canViewGroup reports whether the caller may open a group screen at all. The
// RBAC matrix separates the modules on purpose, so a verifier who may confirm
// payments must still be able to open the group they belong to; the screen
// then shows only the sections that role may read.
func (s *Service) canViewGroup(bindings []rbac.Binding, userID string, g Group) bool {
	checks := []struct {
		module rbac.Module
		action rbac.Action
	}{
		{rbac.ModuleRegistration, rbac.ActionView},
		{rbac.ModuleMeetings, rbac.ActionView},
		{rbac.ModulePayments, rbac.ActionView},
		{rbac.ModuleLoans, rbac.ActionView},
		{rbac.ModuleTasks, rbac.ActionView},
	}
	for _, c := range checks {
		if s.allowed(bindings, userID, c.module, c.action, g.LegalEntityID, g.BranchID, g.ID, "") {
			return true
		}
	}
	return false
}

// Group returns one group and everything its detail screen shows.
func (s *Service) Group(ctx context.Context, p auth.Principal, requestID, groupID string) (GroupDetail, error) {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return GroupDetail{}, err
	}
	var detail GroupDetail
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		all, err := loadGroups(ctx, tx)
		if err != nil {
			return err
		}
		var found *Group
		for i := range all {
			if all[i].ID == groupID {
				found = &all[i]
				break
			}
		}
		if found == nil {
			return ErrNotFound
		}
		if !s.canViewGroup(bindings, p.UserID.String(), *found) {
			return ErrForbidden
		}
		detail.Group = *found
		detail.Members = []Member{}
		detail.Meetings = []Meeting{}
		detail.Contributions = []Contribution{}
		detail.Loans = []Loan{}
		detail.Tasks = []Task{}
		if s.allowed(bindings, p.UserID.String(), rbac.ModuleRegistration, rbac.ActionView, found.LegalEntityID, found.BranchID, found.ID, "") {
			if detail.Members, err = loadMembers(ctx, tx, groupID); err != nil {
				return err
			}
		}
		if s.allowed(bindings, p.UserID.String(), rbac.ModuleMeetings, rbac.ActionView, found.LegalEntityID, found.BranchID, found.ID, "") {
			if detail.Meetings, err = loadMeetings(ctx, tx, groupID, 12); err != nil {
				return err
			}
		}
		if s.allowed(bindings, p.UserID.String(), rbac.ModulePayments, rbac.ActionView, found.LegalEntityID, found.BranchID, found.ID, "") {
			if detail.Contributions, err = loadContributions(ctx, tx, []string{groupID}, 60); err != nil {
				return err
			}
		}
		if s.allowed(bindings, p.UserID.String(), rbac.ModuleLoans, rbac.ActionView, found.LegalEntityID, found.BranchID, found.ID, "") {
			if detail.Loans, err = loadLoans(ctx, tx, []string{groupID}); err != nil {
				return err
			}
		}
		if s.allowed(bindings, p.UserID.String(), rbac.ModuleTasks, rbac.ActionView, found.LegalEntityID, found.BranchID, found.ID, "") {
			if detail.Tasks, err = loadTasks(ctx, tx, []string{groupID}, 20); err != nil {
				return err
			}
		}
		return nil
	})
	return detail, err
}

// CreateGroup registers a new group in the caller's branch.
func (s *Service) CreateGroup(ctx context.Context, p auth.Principal, requestID string, in CreateGroupInput) (Group, error) {
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 3 {
		return Group{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if !contains([]string{"vsla", "investment_club", "association", "cooperative", "sacco"}, in.GroupType) {
		return Group{}, fmt.Errorf("%w: unknown group type", ErrInvalid)
	}
	if in.MeetingFrequency == "" {
		in.MeetingFrequency = "weekly"
	}
	if !contains([]string{"weekly", "fortnightly", "monthly"}, in.MeetingFrequency) {
		return Group{}, fmt.Errorf("%w: unknown meeting frequency", ErrInvalid)
	}
	if in.MeetingDay == "" {
		in.MeetingDay = "Sunday"
	}

	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return Group{}, err
	}

	var created Group
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		branchID := in.BranchID
		if branchID == "" {
			branchID = firstScopeID(bindings, rbac.ScopeBranch)
		}
		var legalEntityID, branchName string
		if branchID != "" {
			err := tx.QueryRow(ctx,
				`SELECT legal_entity_id::text, name FROM pamoja.branch WHERE id = $1::uuid`, branchID).
				Scan(&legalEntityID, &branchName)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: branch not found", ErrInvalid)
			}
			if err != nil {
				return err
			}
		} else {
			if err := tx.QueryRow(ctx,
				`SELECT id::text, legal_entity_id::text, name FROM pamoja.branch ORDER BY code LIMIT 1`).
				Scan(&branchID, &legalEntityID, &branchName); err != nil {
				return err
			}
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleRegistration, rbac.ActionCreate, legalEntityID, branchID, "", "") {
			return ErrForbidden
		}

		var next int
		if err := tx.QueryRow(ctx, `SELECT count(*) + 1 FROM pamoja.grp`).Scan(&next); err != nil {
			return err
		}
		code := fmt.Sprintf("YP-%04d", next)
		formed := cleanDate(in.FormedOn, time.Now())

		var id string
		var createdAt time.Time
		err := tx.QueryRow(ctx, `
			INSERT INTO pamoja.grp (branch_id, code, name, group_type, status, formed_on,
				meeting_day, meeting_frequency, village, sub_county, district, created_by)
			VALUES ($1::uuid, $2, $3, $4, 'pending_activation', $5::date, $6, $7, $8, $9, $10, $11::uuid)
			RETURNING id::text, created_at`,
			branchID, code, in.Name, in.GroupType, formed, in.MeetingDay, in.MeetingFrequency,
			strings.TrimSpace(in.Village), strings.TrimSpace(in.SubCounty), strings.TrimSpace(in.District),
			p.UserID.String()).Scan(&id, &createdAt)
		if err != nil {
			return err
		}
		if err := s.record(ctx, tx, requestID, p, "group.create", "group", id, map[string]any{
			"name": in.Name, "code": code, "type": in.GroupType, "branch": branchName,
		}); err != nil {
			return err
		}

		created = Group{
			ID: id, Code: code, Name: in.Name, GroupType: in.GroupType,
			Status: "pending_activation", FormedOn: formed, MeetingDay: in.MeetingDay,
			MeetingFrequency: in.MeetingFrequency, Village: strings.TrimSpace(in.Village),
			SubCounty: strings.TrimSpace(in.SubCounty), District: strings.TrimSpace(in.District),
			BranchID: branchID, BranchName: branchName, LegalEntityID: legalEntityID,
			CreatedAt: createdAt,
		}
		return nil
	})
	return created, err
}

// ActivateGroup moves a pending group to active after review (SD-06).
func (s *Service) ActivateGroup(ctx context.Context, p auth.Principal, requestID, groupID string) error {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		var legalEntityID, branchID, status string
		err := tx.QueryRow(ctx,
			`SELECT b.legal_entity_id::text, g.branch_id::text, g.status
			 FROM pamoja.grp g JOIN pamoja.branch b ON b.id = g.branch_id WHERE g.id = $1::uuid`,
			groupID).Scan(&legalEntityID, &branchID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleRegistration, rbac.ActionReviewActivate, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		if status != "pending_activation" && status != "draft" {
			return fmt.Errorf("%w: group is already %s", ErrConflict, status)
		}
		if _, err := tx.Exec(ctx, `UPDATE pamoja.grp SET status = 'active', updated_at = now() WHERE id = $1::uuid`, groupID); err != nil {
			return err
		}
		return s.record(ctx, tx, requestID, p, "group.activate", "group", groupID, map[string]any{"status": "active"})
	})
}

// AddMember adds one member to a group.
func (s *Service) AddMember(ctx context.Context, p auth.Principal, requestID, groupID string, in CreateMemberInput) (Member, error) {
	in.FullName = strings.TrimSpace(in.FullName)
	if len(in.FullName) < 3 {
		return Member{}, fmt.Errorf("%w: full name is required", ErrInvalid)
	}
	if in.Gender == "" {
		in.Gender = "female"
	}
	if !contains([]string{"female", "male", "other"}, in.Gender) {
		return Member{}, fmt.Errorf("%w: unknown gender", ErrInvalid)
	}
	if in.RoleInGroup == "" {
		in.RoleInGroup = "member"
	}
	if !contains([]string{"chairperson", "secretary", "treasurer", "member"}, in.RoleInGroup) {
		return Member{}, fmt.Errorf("%w: unknown group role", ErrInvalid)
	}

	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return Member{}, err
	}
	var member Member
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleRegistration, rbac.ActionCreate, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		var next int
		if err := tx.QueryRow(ctx, `SELECT count(*) + 1 FROM pamoja.group_member WHERE group_id = $1::uuid`, groupID).Scan(&next); err != nil {
			return err
		}
		memberNo := fmt.Sprintf("M-%03d", next)
		joined := cleanDate(in.JoinedOn, time.Now())
		phoneHash := ""
		if s.keys != nil {
			phoneHash = s.keys.LookupHash(auth.NormalizePhone(in.Phone))
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO pamoja.group_member (group_id, member_no, full_name, phone_masked, phone_hash, gender, role_in_group, joined_on)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::date)
			RETURNING id::text, created_at`,
			groupID, memberNo, in.FullName, maskPhone(in.Phone), phoneHash, in.Gender, in.RoleInGroup, joined).
			Scan(&member.ID, &member.CreatedAt)
		if err != nil {
			return err
		}
		member.MemberNo = memberNo
		member.FullName = in.FullName
		member.PhoneMasked = maskPhone(in.Phone)
		member.Gender = in.Gender
		member.RoleInGroup = in.RoleInGroup
		member.Status = "active"
		member.JoinedOn = joined
		return s.record(ctx, tx, requestID, p, "member.create", "group_member", member.ID, map[string]any{
			"group_id": groupID, "member_no": memberNo, "name": in.FullName, "role": in.RoleInGroup,
		})
	})
	return member, err
}

// UpdateMember changes a member's group role or status.
func (s *Service) UpdateMember(ctx context.Context, p auth.Principal, requestID, groupID, memberID string, in UpdateMemberInput) error {
	if in.RoleInGroup != "" && !contains([]string{"chairperson", "secretary", "treasurer", "member"}, in.RoleInGroup) {
		return fmt.Errorf("%w: unknown group role", ErrInvalid)
	}
	if in.Status != "" && !contains([]string{"active", "inactive", "exited"}, in.Status) {
		return fmt.Errorf("%w: unknown member status", ErrInvalid)
	}
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleRegistration, rbac.ActionEditDraft, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		tag, err := tx.Exec(ctx, `
			UPDATE pamoja.group_member
			SET role_in_group = COALESCE(NULLIF($3, ''), role_in_group),
			    status = COALESCE(NULLIF($4, ''), status)
			WHERE id = $1::uuid AND group_id = $2::uuid`,
			memberID, groupID, in.RoleInGroup, in.Status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.record(ctx, tx, requestID, p, "member.update", "group_member", memberID, map[string]any{
			"group_id": groupID, "role": in.RoleInGroup, "status": in.Status,
		})
	})
}

// CreateMeeting opens a meeting with the active members' register.
func (s *Service) CreateMeeting(ctx context.Context, p auth.Principal, requestID, groupID string, in CreateMeetingInput) (Meeting, error) {
	if in.MeetingType == "" {
		in.MeetingType = "regular"
	}
	if !contains([]string{"regular", "special", "annual"}, in.MeetingType) {
		return Meeting{}, fmt.Errorf("%w: unknown meeting type", ErrInvalid)
	}
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return Meeting{}, err
	}
	var meeting Meeting
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleMeetings, rbac.ActionCreate, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		var next int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(meeting_no), 0) + 1 FROM pamoja.meeting WHERE group_id = $1::uuid`, groupID).Scan(&next); err != nil {
			return err
		}
		heldOn := cleanDate(in.HeldOn, time.Now())
		var id string
		err = tx.QueryRow(ctx, `
			INSERT INTO pamoja.meeting (group_id, meeting_no, held_on, meeting_type, location, agenda, created_by)
			VALUES ($1::uuid, $2, $3::date, $4, $5, $6, $7::uuid)
			RETURNING id::text`,
			groupID, next, heldOn, in.MeetingType, strings.TrimSpace(in.Location), strings.TrimSpace(in.Agenda), p.UserID.String()).
			Scan(&id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO pamoja.attendance (meeting_id, member_id, status)
			SELECT $1::uuid, m.id, 'present'
			FROM pamoja.group_member m
			WHERE m.group_id = $2::uuid AND m.status = 'active'
			ON CONFLICT (meeting_id, member_id) DO NOTHING`, id, groupID); err != nil {
			return err
		}
		if err := s.record(ctx, tx, requestID, p, "meeting.create", "meeting", id, map[string]any{
			"group_id": groupID, "meeting_no": next, "held_on": heldOn, "type": in.MeetingType,
		}); err != nil {
			return err
		}
		meetings, err := loadMeetings(ctx, tx, groupID, 1)
		if err != nil {
			return err
		}
		if len(meetings) > 0 {
			meeting = meetings[0]
		}
		return nil
	})
	return meeting, err
}

// SetAttendance replaces the register rows supplied for a draft meeting.
func (s *Service) SetAttendance(ctx context.Context, p auth.Principal, requestID, meetingID string, in AttendanceInput) error {
	if len(in.Entries) == 0 {
		return fmt.Errorf("%w: no attendance entries", ErrInvalid)
	}
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		var groupID, status string
		err := tx.QueryRow(ctx, `SELECT group_id::text, status FROM pamoja.meeting WHERE id = $1::uuid`, meetingID).
			Scan(&groupID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleMeetings, rbac.ActionEditDraft, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		if status != "draft" {
			return fmt.Errorf("%w: a closed register cannot be edited", ErrConflict)
		}
		for _, entry := range in.Entries {
			if !contains([]string{"present", "absent", "excused", "late"}, entry.Status) {
				return fmt.Errorf("%w: unknown attendance status", ErrInvalid)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO pamoja.attendance (meeting_id, member_id, status, note)
				VALUES ($1::uuid, $2::uuid, $3, $4)
				ON CONFLICT (meeting_id, member_id)
				DO UPDATE SET status = EXCLUDED.status, note = EXCLUDED.note`,
				meetingID, entry.MemberID, entry.Status, strings.TrimSpace(entry.Note)); err != nil {
				return err
			}
		}
		return s.record(ctx, tx, requestID, p, "meeting.attendance", "meeting", meetingID, map[string]any{
			"rows": len(in.Entries),
		})
	})
}

// CloseMeeting locks the register and stores the minutes.
func (s *Service) CloseMeeting(ctx context.Context, p auth.Principal, requestID, meetingID string, in CloseMeetingInput) error {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		var groupID, status string
		err := tx.QueryRow(ctx, `SELECT group_id::text, status FROM pamoja.meeting WHERE id = $1::uuid`, meetingID).
			Scan(&groupID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleMeetings, rbac.ActionClose, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		if status == "closed" {
			return fmt.Errorf("%w: meeting is already closed", ErrConflict)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE pamoja.meeting
			SET status = 'closed', minutes = $2, closed_at = now()
			WHERE id = $1::uuid`, meetingID, strings.TrimSpace(in.Minutes)); err != nil {
			return err
		}
		return s.record(ctx, tx, requestID, p, "meeting.close", "meeting", meetingID, map[string]any{"status": "closed"})
	})
}

// CreateContribution records one payment entry as submitted.
func (s *Service) CreateContribution(ctx context.Context, p auth.Principal, requestID, groupID string, in CreateContributionInput) (Contribution, error) {
	if in.AmountUGX <= 0 {
		return Contribution{}, fmt.Errorf("%w: amount must be greater than zero", ErrInvalid)
	}
	if in.Kind == "" {
		in.Kind = "savings"
	}
	if !contains([]string{"savings", "shares", "social_fund", "loan_repayment", "fine"}, in.Kind) {
		return Contribution{}, fmt.Errorf("%w: unknown payment kind", ErrInvalid)
	}
	if in.Method == "" {
		in.Method = "cash"
	}
	if !contains([]string{"cash", "mobile_money", "bank"}, in.Method) {
		return Contribution{}, fmt.Errorf("%w: unknown payment method", ErrInvalid)
	}
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return Contribution{}, err
	}
	var created Contribution
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModulePayments, rbac.ActionCreate, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		var exists int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM pamoja.group_member WHERE id = $1::uuid AND group_id = $2::uuid`,
			in.MemberID, groupID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("%w: member does not belong to this group", ErrInvalid)
		}
		occurred := cleanDate(in.OccurredOn, time.Now())
		var meetingID *string
		if strings.TrimSpace(in.MeetingID) != "" {
			meetingID = &in.MeetingID
		}
		var id string
		err = tx.QueryRow(ctx, `
			INSERT INTO pamoja.contribution (group_id, member_id, meeting_id, kind, amount_ugx, method, reference, status, note, recorded_by, occurred_on)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, 'submitted', $8, $9::uuid, $10::date)
			RETURNING id::text`,
			groupID, in.MemberID, meetingID, in.Kind, in.AmountUGX, in.Method,
			strings.TrimSpace(in.Reference), strings.TrimSpace(in.Note), p.UserID.String(), occurred).Scan(&id)
		if err != nil {
			return err
		}
		if err := s.record(ctx, tx, requestID, p, "payment.create", "contribution", id, map[string]any{
			"group_id": groupID, "kind": in.Kind, "amount_ugx": in.AmountUGX, "method": in.Method,
		}); err != nil {
			return err
		}
		rows, err := loadContributionsByID(ctx, tx, id)
		if err != nil {
			return err
		}
		created = rows
		return nil
	})
	return created, err
}

// VerifyContribution confirms a submitted entry (SD-01, SD-02).
func (s *Service) VerifyContribution(ctx context.Context, p auth.Principal, requestID, contributionID string) error {
	return s.decideContribution(ctx, p, requestID, contributionID, rbac.ActionVerify, "")
}

// ReturnContribution sends a submitted entry back to the recorder.
func (s *Service) ReturnContribution(ctx context.Context, p auth.Principal, requestID, contributionID string, in ReturnContributionInput) error {
	if strings.TrimSpace(in.Note) == "" {
		return fmt.Errorf("%w: a reason is required to return an entry", ErrInvalid)
	}
	return s.decideContribution(ctx, p, requestID, contributionID, rbac.ActionReturn, in.Note)
}

func (s *Service) decideContribution(ctx context.Context, p auth.Principal, requestID, contributionID string, action rbac.Action, note string) error {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		var groupID, status, recordedBy string
		err := tx.QueryRow(ctx,
			`SELECT group_id::text, status, COALESCE(recorded_by::text, '') FROM pamoja.contribution WHERE id = $1::uuid`,
			contributionID).Scan(&groupID, &status, &recordedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModulePayments, action, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		if status != "submitted" {
			return fmt.Errorf("%w: only a submitted entry can be %s", ErrConflict, action)
		}
		if err := s.matrix.CheckSeparation(p.UserID.String(), rbac.ModulePayments, action, map[rbac.ContextField][]string{
			rbac.CtxRecorder:  {recordedBy},
			rbac.CtxSubmitter: {recordedBy},
		}); err != nil {
			return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
		}
		if action == rbac.ActionVerify {
			_, err = tx.Exec(ctx, `
				UPDATE pamoja.contribution
				SET status = 'verified', verified_by = $2::uuid, verified_at = now()
				WHERE id = $1::uuid`, contributionID, p.UserID.String())
		} else {
			_, err = tx.Exec(ctx, `
				UPDATE pamoja.contribution
				SET status = 'returned', note = $2, verified_by = $3::uuid, verified_at = now()
				WHERE id = $1::uuid`, contributionID, note, p.UserID.String())
		}
		if err != nil {
			return err
		}
		return s.record(ctx, tx, requestID, p, "payment."+string(action), "contribution", contributionID, map[string]any{
			"group_id": groupID, "note": note,
		})
	})
}

// Loans returns the borrowing applications the caller may see.
func (s *Service) Loans(ctx context.Context, p auth.Principal, requestID string) ([]Loan, error) {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return nil, err
	}
	var loans []Loan
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		all, err := loadGroups(ctx, tx)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(all))
		for _, g := range all {
			if s.allowed(bindings, p.UserID.String(), rbac.ModuleLoans, rbac.ActionView, g.LegalEntityID, g.BranchID, g.ID, "") {
				ids = append(ids, g.ID)
			}
		}
		loans, err = loadLoans(ctx, tx, ids)
		return err
	})
	if loans == nil {
		loans = []Loan{}
	}
	return loans, err
}

// CreateLoan submits a borrowing application for a group.
func (s *Service) CreateLoan(ctx context.Context, p auth.Principal, requestID string, in CreateLoanInput) (Loan, error) {
	if in.AmountUGX <= 0 {
		return Loan{}, fmt.Errorf("%w: amount must be greater than zero", ErrInvalid)
	}
	if strings.TrimSpace(in.Purpose) == "" {
		return Loan{}, fmt.Errorf("%w: purpose is required", ErrInvalid)
	}
	if in.TermMonths == 0 {
		in.TermMonths = 6
	}
	if in.TermMonths < 1 || in.TermMonths > 36 {
		return Loan{}, fmt.Errorf("%w: term must be between 1 and 36 months", ErrInvalid)
	}
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return Loan{}, err
	}
	var created Loan
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		legalEntityID, branchID, err := groupScope(ctx, tx, in.GroupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleLoans, rbac.ActionCreate, legalEntityID, branchID, in.GroupID, "") {
			return ErrForbidden
		}
		var applicant *string
		if strings.TrimSpace(in.ApplicantMemberID) != "" {
			var exists int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM pamoja.group_member WHERE id = $1::uuid AND group_id = $2::uuid`,
				in.ApplicantMemberID, in.GroupID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return fmt.Errorf("%w: applicant does not belong to this group", ErrInvalid)
			}
			applicant = &in.ApplicantMemberID
		}
		var id string
		err = tx.QueryRow(ctx, `
			INSERT INTO pamoja.loan_application (group_id, applicant_member_id, amount_ugx, purpose, term_months, status, submitted_on, created_by)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'submitted', current_date, $6::uuid)
			RETURNING id::text`,
			in.GroupID, applicant, in.AmountUGX, strings.TrimSpace(in.Purpose), in.TermMonths, p.UserID.String()).Scan(&id)
		if err != nil {
			return err
		}
		if err := s.record(ctx, tx, requestID, p, "loan.create", "loan_application", id, map[string]any{
			"group_id": in.GroupID, "amount_ugx": in.AmountUGX, "term_months": in.TermMonths,
		}); err != nil {
			return err
		}
		created, err = loadLoanByID(ctx, tx, id)
		return err
	})
	return created, err
}

// AppraiseLoan records the field officer's appraisal.
func (s *Service) AppraiseLoan(ctx context.Context, p auth.Principal, requestID, loanID string, in NoteInput) error {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		groupID, status, err := loanState(ctx, tx, loanID)
		if err != nil {
			return err
		}
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleLoans, rbac.ActionAppraise, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		if status != "submitted" {
			return fmt.Errorf("%w: only a submitted application can be appraised", ErrConflict)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE pamoja.loan_application
			SET status = 'appraised', appraised_by = $2::uuid, appraisal_note = $3, updated_at = now()
			WHERE id = $1::uuid`, loanID, p.UserID.String(), strings.TrimSpace(in.Note)); err != nil {
			return err
		}
		return s.record(ctx, tx, requestID, p, "loan.appraise", "loan_application", loanID, map[string]any{"status": "appraised"})
	})
}

// ApproveLoan approves an appraised application (SD-03).
func (s *Service) ApproveLoan(ctx context.Context, p auth.Principal, requestID, loanID string, in NoteInput) error {
	return s.decideLoan(ctx, p, requestID, loanID, "approved", in.Note)
}

// DeclineLoan declines an appraised application.
func (s *Service) DeclineLoan(ctx context.Context, p auth.Principal, requestID, loanID string, in NoteInput) error {
	return s.decideLoan(ctx, p, requestID, loanID, "declined", in.Note)
}

func (s *Service) decideLoan(ctx context.Context, p auth.Principal, requestID, loanID, decision, note string) error {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	action := rbac.ActionApprove
	if decision == "declined" {
		action = rbac.ActionDecline
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		var groupID, status, appraisedBy string
		err := tx.QueryRow(ctx, `
			SELECT group_id::text, status, COALESCE(appraised_by::text, '')
			FROM pamoja.loan_application WHERE id = $1::uuid`, loanID).
			Scan(&groupID, &status, &appraisedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleLoans, action, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		if status != "appraised" && status != "operations_reviewed" {
			return fmt.Errorf("%w: only an appraised application can be %s", ErrConflict, decision)
		}
		if err := s.matrix.CheckSeparation(p.UserID.String(), rbac.ModuleLoans, action, map[rbac.ContextField][]string{
			rbac.CtxAppraiser: {appraisedBy},
		}); err != nil {
			return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
		}
		if _, err := tx.Exec(ctx, `
			UPDATE pamoja.loan_application
			SET status = $2, approved_by = $3::uuid, decision_note = $4, decided_on = current_date, updated_at = now()
			WHERE id = $1::uuid`, loanID, decision, p.UserID.String(), strings.TrimSpace(note)); err != nil {
			return err
		}
		return s.record(ctx, tx, requestID, p, "loan."+decision, "loan_application", loanID, map[string]any{"status": decision})
	})
}

// DisburseLoan confirms a disbursement (SD-04).
func (s *Service) DisburseLoan(ctx context.Context, p auth.Principal, requestID, loanID string) error {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		var groupID, status, approvedBy string
		err := tx.QueryRow(ctx, `
			SELECT group_id::text, status, COALESCE(approved_by::text, '')
			FROM pamoja.loan_application WHERE id = $1::uuid`, loanID).
			Scan(&groupID, &status, &approvedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		legalEntityID, branchID, err := groupScope(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleLoans, rbac.ActionDisburse, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		if status != "approved" {
			return fmt.Errorf("%w: only an approved application can be disbursed", ErrConflict)
		}
		if err := s.matrix.CheckSeparation(p.UserID.String(), rbac.ModuleLoans, rbac.ActionDisburse, map[rbac.ContextField][]string{
			rbac.CtxApprover: {approvedBy},
		}); err != nil {
			return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
		}
		if _, err := tx.Exec(ctx, `
			UPDATE pamoja.loan_application
			SET status = 'disbursed', disbursed_on = current_date, updated_at = now()
			WHERE id = $1::uuid`, loanID); err != nil {
			return err
		}
		return s.record(ctx, tx, requestID, p, "loan.disburse", "loan_application", loanID, map[string]any{"status": "disbursed"})
	})
}

// Tasks returns the follow-up tasks the caller may see.
func (s *Service) Tasks(ctx context.Context, p auth.Principal, requestID string) ([]Task, error) {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return nil, err
	}
	var tasks []Task
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		all, err := loadGroups(ctx, tx)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(all))
		for _, g := range all {
			if s.allowed(bindings, p.UserID.String(), rbac.ModuleTasks, rbac.ActionView, g.LegalEntityID, g.BranchID, g.ID, "") {
				ids = append(ids, g.ID)
			}
		}
		tasks, err = loadTasks(ctx, tx, ids, 100)
		return err
	})
	if tasks == nil {
		tasks = []Task{}
	}
	return tasks, err
}

// CreateTask records a follow-up task owned by the caller.
func (s *Service) CreateTask(ctx context.Context, p auth.Principal, requestID string, in CreateTaskInput) (Task, error) {
	in.Title = strings.TrimSpace(in.Title)
	if len(in.Title) < 3 {
		return Task{}, fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	if !contains([]string{"low", "normal", "high"}, in.Priority) {
		return Task{}, fmt.Errorf("%w: unknown priority", ErrInvalid)
	}
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return Task{}, err
	}
	var created Task
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		legalEntityID, branchID := actorScope(bindings)
		if in.GroupID != "" {
			le, br, err := groupScope(ctx, tx, in.GroupID)
			if err != nil {
				return err
			}
			legalEntityID, branchID = le, br
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleTasks, rbac.ActionCreate, legalEntityID, branchID, in.GroupID, "") {
			return ErrForbidden
		}
		var groupID *string
		if in.GroupID != "" {
			groupID = &in.GroupID
		}
		var due *string
		if strings.TrimSpace(in.DueOn) != "" {
			due = &in.DueOn
		}
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO pamoja.task (group_id, title, detail, assignee_user_id, due_on, priority, created_by)
			VALUES ($1::uuid, $2, $3, $4::uuid, $5::date, $6, $4::uuid)
			RETURNING id::text`,
			groupID, in.Title, strings.TrimSpace(in.Detail), p.UserID.String(), due, in.Priority).Scan(&id)
		if err != nil {
			return err
		}
		if err := s.record(ctx, tx, requestID, p, "task.create", "task", id, map[string]any{
			"title": in.Title, "priority": in.Priority,
		}); err != nil {
			return err
		}
		created, err = loadTaskByID(ctx, tx, id)
		return err
	})
	return created, err
}

// CloseTask marks a task done.
func (s *Service) CloseTask(ctx context.Context, p auth.Principal, requestID, taskID string) error {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return err
	}
	return db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		var groupID, status string
		err := tx.QueryRow(ctx, `SELECT COALESCE(group_id::text, ''), status FROM pamoja.task WHERE id = $1::uuid`, taskID).
			Scan(&groupID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		legalEntityID, branchID := actorScope(bindings)
		if groupID != "" {
			le, br, err := groupScope(ctx, tx, groupID)
			if err != nil {
				return err
			}
			legalEntityID, branchID = le, br
		}
		if !s.allowed(bindings, p.UserID.String(), rbac.ModuleTasks, rbac.ActionClose, legalEntityID, branchID, groupID, "") {
			return ErrForbidden
		}
		if status != "open" {
			return fmt.Errorf("%w: task is already %s", ErrConflict, status)
		}
		if _, err := tx.Exec(ctx, `UPDATE pamoja.task SET status = 'done', closed_at = now() WHERE id = $1::uuid`, taskID); err != nil {
			return err
		}
		return s.record(ctx, tx, requestID, p, "task.close", "task", taskID, map[string]any{"status": "done"})
	})
}

// Dashboard returns the management overview for the caller's scope.
func (s *Service) Dashboard(ctx context.Context, p auth.Principal, requestID string) (Dashboard, error) {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return Dashboard{}, err
	}
	out := Dashboard{MonthlySavings: []MonthPoint{}, Groups: []Group{}, Attention: []Attention{}, LoansByStatus: []StatusCount{}, RecentContributions: []Contribution{}}

	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		all, err := loadGroups(ctx, tx)
		if err != nil {
			return err
		}
		var visible []Group
		for _, g := range all {
			if s.allowed(bindings, p.UserID.String(), rbac.ModuleDashboard, rbac.ActionView, g.LegalEntityID, g.BranchID, g.ID, "") {
				visible = append(visible, g)
			}
		}
		out.Groups = visible
		ids := groupIDs(visible)
		rateSum := 0
		for _, g := range visible {
			out.Totals.Groups++
			out.Totals.Members += g.Members
			out.Totals.SavingsUGX += g.SavingsUGX
			out.Totals.OutstandingUGX += g.OutstandingUGX
			out.Totals.ActiveLoans += g.ActiveLoans
			rateSum += g.AttendanceRate
		}
		if len(visible) > 0 {
			out.Totals.AttendanceRate = rateSum / len(visible)
		}
		if len(ids) == 0 {
			return nil
		}

		list := strings.Join(ids, ",")
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM pamoja.contribution
			WHERE group_id = ANY(string_to_array($1, ',')::uuid[]) AND status = 'submitted'`, list).
			Scan(&out.Totals.PendingVerifications); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM pamoja.loan_application
			WHERE group_id = ANY(string_to_array($1, ',')::uuid[]) AND status IN ('submitted', 'appraised')`, list).
			Scan(&out.Totals.PendingLoans); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM pamoja.task
			WHERE (group_id IS NULL OR group_id = ANY(string_to_array($1, ',')::uuid[])) AND status = 'open'`, list).
			Scan(&out.Totals.OpenTasks); err != nil {
			return err
		}

		out.MonthlySavings, err = loadMonthlySavings(ctx, tx, list)
		if err != nil {
			return err
		}
		out.LoansByStatus, err = loadLoanStatusCounts(ctx, tx, list)
		if err != nil {
			return err
		}
		out.RecentContributions, err = loadContributions(ctx, tx, ids, 6)
		if err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT c.group_id::text, g.name, m.full_name, c.amount_ugx, c.kind, c.occurred_on::text
			FROM pamoja.contribution c
			JOIN pamoja.grp g ON g.id = c.group_id
			JOIN pamoja.group_member m ON m.id = c.member_id
			WHERE c.group_id = ANY(string_to_array($1, ',')::uuid[]) AND c.status = 'submitted'
			ORDER BY c.created_at DESC LIMIT 4`, list)
		if err != nil {
			return err
		}
		for rows.Next() {
			var groupID, groupName, memberName, kind, occurred string
			var amount int64
			if err := rows.Scan(&groupID, &groupName, &memberName, &amount, &kind, &occurred); err != nil {
				rows.Close()
				return err
			}
			out.Attention = append(out.Attention, Attention{
				Kind:    "verification",
				Title:   fmt.Sprintf("Verify %s from %s", kindLabel(kind), memberName),
				Detail:  fmt.Sprintf("%s · %s · %s", groupName, formatUGX(amount), prettyDate(occurred)),
				GroupID: groupID,
				Link:    "/groups/" + groupID,
			})
		}
		rows.Close()

		loanRows, err := tx.Query(ctx, `
			SELECT l.id::text, l.group_id::text, g.name, l.amount_ugx, l.status
			FROM pamoja.loan_application l
			JOIN pamoja.grp g ON g.id = l.group_id
			WHERE l.group_id = ANY(string_to_array($1, ',')::uuid[]) AND l.status IN ('submitted', 'appraised')
			ORDER BY l.created_at DESC LIMIT 4`, list)
		if err != nil {
			return err
		}
		defer loanRows.Close()
		for loanRows.Next() {
			var id, groupID, groupName, status string
			var amount int64
			if err := loanRows.Scan(&id, &groupID, &groupName, &amount, &status); err != nil {
				return err
			}
			verb := "Appraise"
			if status == "appraised" {
				verb = "Decide on"
			}
			out.Attention = append(out.Attention, Attention{
				Kind:    "loan",
				Title:   fmt.Sprintf("%s %s loan", verb, groupName),
				Detail:  fmt.Sprintf("%s · %s", formatUGX(amount), statusLabel(status)),
				GroupID: groupID,
				Link:    "/loans",
			})
		}
		return loanRows.Err()
	})
	return out, err
}

// Audit returns one page of the audit log with chain integrity.
func (s *Service) Audit(ctx context.Context, p auth.Principal, requestID string, limit, offset int) (AuditPage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return AuditPage{}, err
	}
	legalEntityID, branchID := actorScope(bindings)
	if !s.allowed(bindings, p.UserID.String(), rbac.ModuleAudit, rbac.ActionView, legalEntityID, branchID, "", "") {
		return AuditPage{}, ErrForbidden
	}
	page := AuditPage{Entries: []AuditEntry{}}
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT seq, occurred_at, actor_name, actor_role, action, entity_type, COALESCE(entity_id::text, ''), reason, channel, scope_type FROM pamoja.domain_audit_list($1, $2)`, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e AuditEntry
			if err := rows.Scan(&e.Seq, &e.OccurredAt, &e.ActorName, &e.ActorRole, &e.Action, &e.EntityType, &e.EntityID, &e.Reason, &e.Channel, &e.ScopeType); err != nil {
				return err
			}
			page.Entries = append(page.Entries, e)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT pamoja.domain_audit_count()`).Scan(&page.Total); err != nil {
			return err
		}
		var firstBad *int64
		var reason *string
		if err := tx.QueryRow(ctx, `SELECT checked, first_bad_seq, reason FROM pamoja.audit_verify(1)`).
			Scan(&page.Checked, &firstBad, &reason); err != nil {
			return err
		}
		page.Verified = firstBad == nil
		if firstBad != nil {
			page.FirstBadSeq = *firstBad
		}
		if reason != nil {
			page.Reason = *reason
		}
		return nil
	})
	return page, err
}

// Users returns the staff directory.
func (s *Service) Users(ctx context.Context, p auth.Principal, requestID string) ([]User, error) {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return nil, err
	}
	legalEntityID, branchID := actorScope(bindings)
	if !s.allowed(bindings, p.UserID.String(), rbac.ModuleUsersRoles, rbac.ActionView, legalEntityID, branchID, "", "") {
		return nil, ErrForbidden
	}
	users := []User{}
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text, full_name, phone_masked, status, is_staff, mfa_required, created_at, roles FROM pamoja.domain_list_users()`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u User
			if err := rows.Scan(&u.ID, &u.FullName, &u.PhoneMasked, &u.Status, &u.IsStaff, &u.MFARequired, &u.CreatedAt, &u.Roles); err != nil {
				return err
			}
			users = append(users, u)
		}
		return rows.Err()
	})
	return users, err
}

// PortfolioCSV exports the visible groups as a spreadsheet-ready report.
func (s *Service) PortfolioCSV(ctx context.Context, p auth.Principal, requestID string) ([]byte, error) {
	bindings, err := s.Bindings(ctx, p.UserID.String())
	if err != nil {
		return nil, err
	}
	legalEntityID, branchID := actorScope(bindings)
	if !s.allowed(bindings, p.UserID.String(), rbac.ModuleReports, rbac.ActionExport, legalEntityID, branchID, "", "") {
		return nil, ErrForbidden
	}
	var groups []Group
	err = db.WithActor(ctx, s.pool, actorOf(p), requestID, func(ctx context.Context, tx pgx.Tx) error {
		all, err := loadGroups(ctx, tx)
		if err != nil {
			return err
		}
		for _, g := range all {
			if s.allowed(bindings, p.UserID.String(), rbac.ModuleReports, rbac.ActionView, g.LegalEntityID, g.BranchID, g.ID, "") {
				groups = append(groups, g)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	_ = writer.Write([]string{"code", "name", "type", "branch", "district", "status", "members", "savings_ugx", "outstanding_loans_ugx", "active_loans", "attendance_rate", "last_meeting"})
	for _, g := range groups {
		_ = writer.Write([]string{
			g.Code, g.Name, g.GroupType, g.BranchName, g.District, g.Status,
			fmt.Sprint(g.Members), fmt.Sprint(g.SavingsUGX), fmt.Sprint(g.OutstandingUGX),
			fmt.Sprint(g.ActiveLoans), fmt.Sprint(g.AttendanceRate), g.LastMeeting,
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// queries -----------------------------------------------------------------

const groupSelect = `
SELECT g.id::text, g.code, g.name, g.group_type, g.status, g.formed_on::text,
       g.meeting_day, g.meeting_frequency, g.village, g.sub_county, g.district,
       COALESCE(g.cycle_start::text, ''), COALESCE(g.cycle_end::text, ''),
       b.id::text, b.name, b.legal_entity_id::text,
       (SELECT count(*) FROM pamoja.group_member m WHERE m.group_id = g.id AND m.status = 'active'),
       COALESCE((SELECT sum(c.amount_ugx) FROM pamoja.contribution c
                 WHERE c.group_id = g.id AND c.status = 'verified' AND c.kind IN ('savings', 'shares')), 0),
       COALESCE((SELECT sum(l.amount_ugx) FROM pamoja.loan_application l
                 WHERE l.group_id = g.id AND l.status IN ('approved', 'disbursed')), 0),
       (SELECT count(*) FROM pamoja.loan_application l
        WHERE l.group_id = g.id AND l.status IN ('approved', 'disbursed')),
       COALESCE((SELECT round(100.0 * count(*) FILTER (WHERE a.status IN ('present', 'late')) / NULLIF(count(*), 0))::int
                 FROM pamoja.attendance a JOIN pamoja.meeting mt ON mt.id = a.meeting_id
                 WHERE mt.group_id = g.id), 0),
       COALESCE((SELECT max(mt.held_on)::text FROM pamoja.meeting mt WHERE mt.group_id = g.id), ''),
       g.created_at
FROM pamoja.grp g
JOIN pamoja.branch b ON b.id = g.branch_id
ORDER BY g.name`

func loadGroups(ctx context.Context, tx pgx.Tx) ([]Group, error) {
	rows, err := tx.Query(ctx, groupSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := []Group{}
	for rows.Next() {
		var g Group
		var members, activeLoans int64
		var rate int32
		if err := rows.Scan(
			&g.ID, &g.Code, &g.Name, &g.GroupType, &g.Status, &g.FormedOn,
			&g.MeetingDay, &g.MeetingFrequency, &g.Village, &g.SubCounty, &g.District,
			&g.CycleStart, &g.CycleEnd, &g.BranchID, &g.BranchName, &g.LegalEntityID,
			&members, &g.SavingsUGX, &g.OutstandingUGX, &activeLoans, &rate,
			&g.LastMeeting, &g.CreatedAt,
		); err != nil {
			return nil, err
		}
		g.Members = int(members)
		g.ActiveLoans = int(activeLoans)
		g.AttendanceRate = int(rate)
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func loadMembers(ctx context.Context, tx pgx.Tx, groupID string) ([]Member, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.id::text, m.member_no, m.full_name, m.phone_masked, m.gender,
		       m.role_in_group, m.status, m.joined_on::text,
		       COALESCE((SELECT sum(c.amount_ugx) FROM pamoja.contribution c
		                 WHERE c.member_id = m.id AND c.status = 'verified' AND c.kind IN ('savings', 'shares')), 0),
		       m.created_at
		FROM pamoja.group_member m
		WHERE m.group_id = $1::uuid
		ORDER BY m.member_no`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.MemberNo, &m.FullName, &m.PhoneMasked, &m.Gender,
			&m.RoleInGroup, &m.Status, &m.JoinedOn, &m.SavingsUGX, &m.CreatedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func loadMeetings(ctx context.Context, tx pgx.Tx, groupID string, limit int) ([]Meeting, error) {
	rows, err := tx.Query(ctx, `
		SELECT mt.id::text, mt.meeting_no, mt.held_on::text, mt.meeting_type, mt.status,
		       mt.location, mt.agenda, mt.minutes,
		       (SELECT count(*) FROM pamoja.attendance a WHERE a.meeting_id = mt.id AND a.status IN ('present', 'late')),
		       (SELECT count(*) FROM pamoja.attendance a WHERE a.meeting_id = mt.id AND a.status IN ('absent', 'excused')),
		       COALESCE((SELECT sum(c.amount_ugx) FROM pamoja.contribution c WHERE c.meeting_id = mt.id AND c.status = 'verified'), 0)
		FROM pamoja.meeting mt
		WHERE mt.group_id = $1::uuid
		ORDER BY mt.held_on DESC, mt.meeting_no DESC
		LIMIT $2`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	meetings := []Meeting{}
	ids := []string{}
	for rows.Next() {
		var m Meeting
		var present, absent int64
		if err := rows.Scan(&m.ID, &m.MeetingNo, &m.HeldOn, &m.MeetingType, &m.Status,
			&m.Location, &m.Agenda, &m.Minutes, &present, &absent, &m.ContributionsUGX); err != nil {
			return nil, err
		}
		m.Present = int(present)
		m.Absent = int(absent)
		meetings = append(meetings, m)
		ids = append(ids, m.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return meetings, nil
	}

	attRows, err := tx.Query(ctx, `
		SELECT a.meeting_id::text, a.member_id::text, m.full_name, a.status, a.note
		FROM pamoja.attendance a
		JOIN pamoja.group_member m ON m.id = a.member_id
		WHERE a.meeting_id = ANY(string_to_array($1, ',')::uuid[])
		ORDER BY m.member_no`, strings.Join(ids, ","))
	if err != nil {
		return nil, err
	}
	defer attRows.Close()
	byMeeting := map[string][]Attendance{}
	for attRows.Next() {
		var meetingID string
		var a Attendance
		if err := attRows.Scan(&meetingID, &a.MemberID, &a.MemberName, &a.Status, &a.Note); err != nil {
			return nil, err
		}
		byMeeting[meetingID] = append(byMeeting[meetingID], a)
	}
	for i := range meetings {
		meetings[i].Attendance = byMeeting[meetings[i].ID]
	}
	return meetings, attRows.Err()
}

func loadContributions(ctx context.Context, tx pgx.Tx, groupIDs []string, limit int) ([]Contribution, error) {
	if len(groupIDs) == 0 {
		return []Contribution{}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT c.id::text, c.group_id::text, g.name, c.member_id::text, m.full_name,
		       COALESCE(c.meeting_id::text, ''), c.kind, c.amount_ugx, c.method, c.reference,
		       c.status, c.note, c.occurred_on::text,
		       COALESCE(pamoja.domain_user_name(c.recorded_by), ''),
		       COALESCE(pamoja.domain_user_name(c.verified_by), ''),
		       c.created_at
		FROM pamoja.contribution c
		JOIN pamoja.grp g ON g.id = c.group_id
		JOIN pamoja.group_member m ON m.id = c.member_id
		WHERE c.group_id = ANY(string_to_array($1, ',')::uuid[])
		ORDER BY c.occurred_on DESC, c.created_at DESC
		LIMIT $2`, strings.Join(groupIDs, ","), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Contribution{}
	for rows.Next() {
		var c Contribution
		if err := rows.Scan(&c.ID, &c.GroupID, &c.GroupName, &c.MemberID, &c.MemberName,
			&c.MeetingID, &c.Kind, &c.AmountUGX, &c.Method, &c.Reference, &c.Status,
			&c.Note, &c.OccurredOn, &c.RecordedByName, &c.VerifiedByName, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadContributionsByID(ctx context.Context, tx pgx.Tx, id string) (Contribution, error) {
	var c Contribution
	err := tx.QueryRow(ctx, `
		SELECT c.id::text, c.group_id::text, g.name, c.member_id::text, m.full_name,
		       COALESCE(c.meeting_id::text, ''), c.kind, c.amount_ugx, c.method, c.reference,
		       c.status, c.note, c.occurred_on::text,
		       COALESCE(pamoja.domain_user_name(c.recorded_by), ''),
		       COALESCE(pamoja.domain_user_name(c.verified_by), ''),
		       c.created_at
		FROM pamoja.contribution c
		JOIN pamoja.grp g ON g.id = c.group_id
		JOIN pamoja.group_member m ON m.id = c.member_id
		WHERE c.id = $1::uuid`, id).
		Scan(&c.ID, &c.GroupID, &c.GroupName, &c.MemberID, &c.MemberName, &c.MeetingID,
			&c.Kind, &c.AmountUGX, &c.Method, &c.Reference, &c.Status, &c.Note,
			&c.OccurredOn, &c.RecordedByName, &c.VerifiedByName, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Contribution{}, ErrNotFound
	}
	return c, err
}

const loanSelect = `
SELECT l.id::text, l.group_id::text, g.name,
       COALESCE(l.applicant_member_id::text, ''), COALESCE(m.full_name, ''),
       l.amount_ugx, l.purpose, l.term_months, l.status, l.appraisal_note, l.decision_note,
       COALESCE(pamoja.domain_user_name(l.appraised_by), ''),
       COALESCE(pamoja.domain_user_name(l.approved_by), ''),
       COALESCE(l.submitted_on::text, ''), COALESCE(l.decided_on::text, ''),
       COALESCE(l.disbursed_on::text, ''), l.created_at
FROM pamoja.loan_application l
JOIN pamoja.grp g ON g.id = l.group_id
LEFT JOIN pamoja.group_member m ON m.id = l.applicant_member_id`

func loadLoans(ctx context.Context, tx pgx.Tx, groupIDs []string) ([]Loan, error) {
	if len(groupIDs) == 0 {
		return []Loan{}, nil
	}
	rows, err := tx.Query(ctx, loanSelect+`
		WHERE l.group_id = ANY(string_to_array($1, ',')::uuid[])
		ORDER BY l.created_at DESC`, strings.Join(groupIDs, ","))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Loan{}
	for rows.Next() {
		l, err := scanLoan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func loadLoanByID(ctx context.Context, tx pgx.Tx, id string) (Loan, error) {
	row := tx.QueryRow(ctx, loanSelect+` WHERE l.id = $1::uuid`, id)
	l, err := scanLoan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Loan{}, ErrNotFound
	}
	return l, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLoan(row rowScanner) (Loan, error) {
	var l Loan
	err := row.Scan(&l.ID, &l.GroupID, &l.GroupName, &l.ApplicantMemberID, &l.ApplicantName,
		&l.AmountUGX, &l.Purpose, &l.TermMonths, &l.Status, &l.AppraisalNote, &l.DecisionNote,
		&l.AppraisedByName, &l.ApprovedByName, &l.SubmittedOn, &l.DecidedOn, &l.DisbursedOn, &l.CreatedAt)
	return l, err
}

const taskSelect = `
SELECT t.id::text, COALESCE(t.group_id::text, ''), COALESCE(g.name, ''),
       t.title, t.detail, COALESCE(pamoja.domain_user_name(t.assignee_user_id), ''),
       COALESCE(t.due_on::text, ''), t.status, t.priority, t.created_at
FROM pamoja.task t
LEFT JOIN pamoja.grp g ON g.id = t.group_id`

func loadTasks(ctx context.Context, tx pgx.Tx, groupIDs []string, limit int) ([]Task, error) {
	var rows pgx.Rows
	var err error
	if len(groupIDs) == 0 {
		rows, err = tx.Query(ctx, taskSelect+`
			WHERE t.group_id IS NULL
			ORDER BY (t.status = 'open') DESC, t.due_on NULLS LAST, t.created_at DESC
			LIMIT $1`, limit)
	} else {
		rows, err = tx.Query(ctx, taskSelect+`
			WHERE (t.group_id IS NULL OR t.group_id = ANY(string_to_array($1, ',')::uuid[]))
			ORDER BY (t.status = 'open') DESC, t.due_on NULLS LAST, t.created_at DESC
			LIMIT $2`, strings.Join(groupIDs, ","), limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Task{}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.GroupID, &t.GroupName, &t.Title, &t.Detail,
			&t.AssigneeName, &t.DueOn, &t.Status, &t.Priority, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func loadTaskByID(ctx context.Context, tx pgx.Tx, id string) (Task, error) {
	var t Task
	err := tx.QueryRow(ctx, taskSelect+` WHERE t.id = $1::uuid`, id).
		Scan(&t.ID, &t.GroupID, &t.GroupName, &t.Title, &t.Detail, &t.AssigneeName,
			&t.DueOn, &t.Status, &t.Priority, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, err
}

func loadMonthlySavings(ctx context.Context, tx pgx.Tx, groupIDs string) ([]MonthPoint, error) {
	rows, err := tx.Query(ctx, `
		SELECT to_char(date_trunc('month', occurred_on), 'YYYY-MM') AS month,
		       sum(amount_ugx)
		FROM pamoja.contribution
		WHERE group_id = ANY(string_to_array($1, ',')::uuid[])
		  AND status = 'verified' AND kind IN ('savings', 'shares')
		  AND occurred_on >= date_trunc('month', current_date) - interval '5 months'
		GROUP BY 1 ORDER BY 1`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	amounts := map[string]int64{}
	for rows.Next() {
		var month string
		var amount int64
		if err := rows.Scan(&month, &amount); err != nil {
			return nil, err
		}
		amounts[month] = amount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	points := make([]MonthPoint, 0, 6)
	now := time.Now()
	for i := 5; i >= 0; i-- {
		m := now.AddDate(0, -i, 0)
		key := m.Format("2006-01")
		points = append(points, MonthPoint{Month: key, Label: m.Format("Jan"), AmountUGX: amounts[key]})
	}
	return points, nil
}

func loadLoanStatusCounts(ctx context.Context, tx pgx.Tx, groupIDs string) ([]StatusCount, error) {
	rows, err := tx.Query(ctx, `
		SELECT status, count(*)
		FROM pamoja.loan_application
		WHERE group_id = ANY(string_to_array($1, ',')::uuid[])
		GROUP BY status ORDER BY status`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StatusCount{}
	for rows.Next() {
		var s StatusCount
		var count int64
		if err := rows.Scan(&s.Status, &count); err != nil {
			return nil, err
		}
		s.Count = int(count)
		out = append(out, s)
	}
	return out, rows.Err()
}

// small helpers -----------------------------------------------------------

func actorOf(p auth.Principal) db.Actor {
	return db.Actor{UserID: p.UserID.String(), Scopes: p.Scopes}
}

func groupIDs(groups []Group) []string {
	ids := make([]string, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	return ids
}

func groupScope(ctx context.Context, tx pgx.Tx, groupID string) (legalEntityID, branchID string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT b.legal_entity_id::text, g.branch_id::text
		FROM pamoja.grp g JOIN pamoja.branch b ON b.id = g.branch_id
		WHERE g.id = $1::uuid`, groupID).Scan(&legalEntityID, &branchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return legalEntityID, branchID, err
}

func loanState(ctx context.Context, tx pgx.Tx, loanID string) (groupID, status string, err error) {
	err = tx.QueryRow(ctx, `SELECT group_id::text, status FROM pamoja.loan_application WHERE id = $1::uuid`, loanID).
		Scan(&groupID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return groupID, status, err
}

func actorScope(bindings []rbac.Binding) (legalEntityID, branchID string) {
	for _, b := range bindings {
		if b.Scope == rbac.ScopeLegalEntity && legalEntityID == "" && len(b.ScopeIDs) > 0 {
			legalEntityID = b.ScopeIDs[0]
		}
		if b.Scope == rbac.ScopeBranch && branchID == "" && len(b.ScopeIDs) > 0 {
			branchID = b.ScopeIDs[0]
		}
	}
	return legalEntityID, branchID
}

func firstScopeID(bindings []rbac.Binding, scope rbac.ScopeType) string {
	for _, b := range bindings {
		if b.Scope == scope && len(b.ScopeIDs) > 0 {
			return b.ScopeIDs[0]
		}
	}
	return ""
}

func (s *Service) record(ctx context.Context, tx pgx.Tx, requestID string, p auth.Principal, action, entityType, entityID string, after map[string]any) error {
	if s.audit == nil {
		return nil
	}
	_, err := s.audit.Append(ctx, requestID, audit.Entry{
		ActorUserID: p.UserID.String(),
		ActorRole:   strings.Join(p.Roles, ","),
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		After:       after,
		Channel:     "web",
	})
	return err
}

func kindLabel(kind string) string {
	switch kind {
	case "savings":
		return "savings"
	case "shares":
		return "share purchase"
	case "social_fund":
		return "social fund"
	case "loan_repayment":
		return "loan repayment"
	case "fine":
		return "fine"
	default:
		return kind
	}
}

func statusLabel(status string) string {
	switch status {
	case "submitted":
		return "awaiting appraisal"
	case "appraised":
		return "awaiting decision"
	case "approved":
		return "approved, awaiting disbursement"
	case "declined":
		return "declined"
	case "disbursed":
		return "disbursed"
	default:
		return status
	}
}

func prettyDate(iso string) string {
	if t, err := time.Parse("2006-01-02", iso); err == nil {
		return t.Format("2 Jan 2006")
	}
	return iso
}

func formatUGX(amount int64) string {
	// Whole shillings with thousands separators; no decimals.
	negative := amount < 0
	if negative {
		amount = -amount
	}
	digits := fmt.Sprint(amount)
	var parts []string
	for len(digits) > 3 {
		parts = append([]string{digits[len(digits)-3:]}, parts...)
		digits = digits[:len(digits)-3]
	}
	parts = append([]string{digits}, parts...)
	out := strings.Join(parts, ",")
	if negative {
		out = "-" + out
	}
	return "UGX " + out
}
