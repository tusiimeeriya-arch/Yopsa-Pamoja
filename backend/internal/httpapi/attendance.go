package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/attendance"
	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/domain"
	platformdb "github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// decodeStrict rejects unknown fields so a typo cannot silently change a
// financial or workflow request.
func decodeStrict(body []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.Invalid("body", "is not valid JSON for this request")
	}
	return nil
}

type meetingResponse struct {
	ID           string     `json:"id"`
	GroupID      string     `json:"group_id"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	Location     string     `json:"location"`
	Status       string     `json:"status"`
	OpenedAt     *time.Time `json:"opened_at,omitempty"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
	Version      int        `json:"version"`
}

type createMeetingRequest struct {
	ScheduledFor time.Time `json:"scheduled_for"`
	Location     string    `json:"location"`
}

func (d Deps) handleCreateMeeting(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleMeetings, rbac.ActionCreate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req createMeetingRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		if req.ScheduledFor.IsZero() {
			return 0, nil, domain.Invalid("scheduled_for", "is required")
		}
		var legalEntity, branch string
		if err := tx.QueryRow(ctx,
			`SELECT legal_entity_id::text, branch_id::text FROM pamoja.groups WHERE id = $1::uuid`,
			groupID).Scan(&legalEntity, &branch); err != nil {
			return 0, nil, err
		}
		var out meetingResponse
		err := tx.QueryRow(ctx,
			`INSERT INTO pamoja.meeting
			   (legal_entity_id, branch_id, group_id, scheduled_for, location, created_by)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid)
			 RETURNING id, group_id, scheduled_for, location, status, opened_at, closed_at, version`,
			legalEntity, branch, groupID, req.ScheduledFor, req.Location, p.UserID).
			Scan(&out.ID, &out.GroupID, &out.ScheduledFor, &out.Location, &out.Status, &out.OpenedAt, &out.ClosedAt, &out.Version)
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "meetings.create", EntityType: "meeting", EntityID: out.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"scheduled_for": req.ScheduledFor, "location": req.Location},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, out, nil
	})
}

type meetingTransitionRequest struct {
	Reason string `json:"reason"`
}

func (d Deps) handleOpenMeeting(w http.ResponseWriter, r *http.Request) {
	d.meetingTransition(w, r, rbac.ActionEditDraft, "open")
}

func (d Deps) handleConfirmRegister(w http.ResponseWriter, r *http.Request) {
	d.meetingTransition(w, r, rbac.ActionEditDraft, "register_confirmed")
}

func (d Deps) handleCloseMeeting(w http.ResponseWriter, r *http.Request) {
	d.meetingTransition(w, r, rbac.ActionClose, "closed")
}

func (d Deps) meetingTransition(w http.ResponseWriter, r *http.Request, action rbac.Action, to string) {
	p, _ := principalFrom(r.Context())
	meetingID := idFrom(r, "meetingID")
	groupID, err := d.meetingGroup(r.Context(), p, meetingID, r)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleMeetings, action, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var out meetingResponse
		now := "now()"
		column := map[string]string{
			"open":               "opened_by = $2::uuid, opened_at = " + now,
			"register_confirmed": "register_confirmed_by = $2::uuid, register_confirmed_at = " + now,
			"closed":             "closed_by = $2::uuid, closed_at = " + now,
		}[to]
		query := fmt.Sprintf(
			`UPDATE pamoja.meeting SET status = $1, %s, version = version + 1
			 WHERE id = $3::uuid
			 RETURNING id, group_id, scheduled_for, location, status, opened_at, closed_at, version`, column)
		if err := tx.QueryRow(ctx, query, to, p.UserID, meetingID).
			Scan(&out.ID, &out.GroupID, &out.ScheduledFor, &out.Location, &out.Status, &out.OpenedAt, &out.ClosedAt, &out.Version); err != nil {
			return 0, nil, err
		}
		if to == "register_confirmed" {
			if _, err := tx.Exec(ctx,
				`UPDATE pamoja.meeting_attendance
				 SET confirmed_by = $2::uuid, confirmed_at = now()
				 WHERE meeting_id = $1::uuid AND confirmed_at IS NULL`,
				meetingID, p.UserID); err != nil {
				return 0, nil, err
			}
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "meetings." + to, EntityType: "meeting", EntityID: meetingID,
			ScopeType: "group", ScopeID: groupID, After: map[string]any{"status": to},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, out, nil
	})
}

type checkInRequest struct {
	MemberID           string `json:"member_id"`
	Status             string `json:"status"`
	Channel            string `json:"channel"`
	ConfirmationMethod string `json:"confirmation_method"`
}

type attendanceResponse struct {
	ID          string    `json:"id"`
	MeetingID   string    `json:"meeting_id"`
	MemberID    string    `json:"member_id"`
	Status      string    `json:"status"`
	Channel     string    `json:"channel"`
	RecordedAt  time.Time `json:"recorded_at"`
	AmendmentOf string    `json:"amendment_of_id,omitempty"`
}

func (d Deps) handleCheckIn(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	meetingID := idFrom(r, "meetingID")
	groupID, err := d.meetingGroup(r.Context(), p, meetingID, r)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}

	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req checkInRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		if req.Status == "" {
			req.Status = attendance.StatusPresent
		}
		if req.Channel == "" {
			req.Channel = "self"
		}

		var selfMemberID string
		_ = tx.QueryRow(ctx, `SELECT id::text FROM pamoja.member WHERE user_id = $1::uuid`, p.UserID).Scan(&selfMemberID)
		self := req.MemberID == "" || req.MemberID == selfMemberID
		if self {
			if err := d.authorizeSelf(ctx, p, rbac.ModuleMeetings, rbac.ActionCheckIn); err != nil {
				return 0, nil, err
			}
			if selfMemberID == "" {
				return 0, nil, domain.ErrForbidden
			}
			req.MemberID = selfMemberID
			req.Channel = "self"
		} else {
			if err := d.authorizeGroup(ctx, p, rbac.ModuleMeetings, rbac.ActionCheckIn, groupID, RequestIDFrom(r.Context())); err != nil {
				return 0, nil, err
			}
			if req.Channel == "self" {
				return 0, nil, domain.Invalid("channel", "assisted check-in must name its channel")
			}
		}

		var memberGroup string
		if err := tx.QueryRow(ctx,
			`SELECT group_id::text FROM pamoja.membership
			 WHERE member_id = $1::uuid AND group_id = $2::uuid AND status = 'active'`,
			req.MemberID, groupID).Scan(&memberGroup); err != nil {
			return 0, nil, domain.ErrForbidden
		}

		var out attendanceResponse
		err := tx.QueryRow(ctx,
			`INSERT INTO pamoja.meeting_attendance
			   (legal_entity_id, branch_id, group_id, meeting_id, member_id, status,
			    checkin_channel, recorded_by)
			 SELECT m.legal_entity_id, m.branch_id, m.group_id, m.id, $2::uuid, $3, $4, $5::uuid
			 FROM pamoja.meeting m WHERE m.id = $1::uuid
			 RETURNING id, meeting_id, member_id, status, checkin_channel, recorded_at,
			           COALESCE(amendment_of_id::text, '')`,
			meetingID, req.MemberID, req.Status, req.Channel, p.UserID).
			Scan(&out.ID, &out.MeetingID, &out.MemberID, &out.Status, &out.Channel, &out.RecordedAt, &out.AmendmentOf)
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "meetings.check_in", EntityType: "meeting_attendance", EntityID: out.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"member_id": req.MemberID, "status": req.Status, "channel": req.Channel},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, out, nil
	})
}

type closeMeetingRequest struct {
	Minutes     string `json:"minutes"`
	Resolutions []struct {
		Body         string `json:"body"`
		VotesFor     int    `json:"votes_for"`
		VotesAgainst int    `json:"votes_against"`
		Abstentions  int    `json:"abstentions"`
	} `json:"resolutions"`
	ActionItems []struct {
		OwnerUserID string `json:"owner_user_id"`
		Description string `json:"description"`
		DueOn       string `json:"due_on"`
	} `json:"action_items"`
}

func (d Deps) handleCloseMeetingWithRecord(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	meetingID := idFrom(r, "meetingID")
	groupID, err := d.meetingGroup(r.Context(), p, meetingID, r)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleMeetings, rbac.ActionClose, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req closeMeetingRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		if req.Minutes == "" {
			return 0, nil, domain.Invalid("minutes", "are required before closing")
		}
		for _, item := range req.ActionItems {
			if item.OwnerUserID == "" || item.DueOn == "" || item.Description == "" {
				return 0, nil, domain.Invalid("action_items", "every item needs an owner, a description and a deadline")
			}
		}

		var legalEntity, branch string
		if err := tx.QueryRow(ctx,
			`SELECT legal_entity_id::text, branch_id::text FROM pamoja.groups WHERE id = $1::uuid`,
			groupID).Scan(&legalEntity, &branch); err != nil {
			return 0, nil, err
		}
		var minutesID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO pamoja.meeting_minutes
			   (legal_entity_id, branch_id, group_id, meeting_id, body, confirmed_by, confirmed_at, created_by)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, now(), $6::uuid)
			 RETURNING id`,
			legalEntity, branch, groupID, meetingID, req.Minutes, p.UserID).Scan(&minutesID); err != nil {
			return 0, nil, err
		}
		for _, res := range req.Resolutions {
			if _, err := tx.Exec(ctx,
				`INSERT INTO pamoja.meeting_resolution
				   (legal_entity_id, branch_id, group_id, meeting_id, body, votes_for, votes_against, abstentions, recorded_by)
				 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9::uuid)`,
				legalEntity, branch, groupID, meetingID, res.Body, res.VotesFor, res.VotesAgainst, res.Abstentions, p.UserID); err != nil {
				return 0, nil, err
			}
		}
		for _, item := range req.ActionItems {
			due, err := time.Parse("2006-01-02", item.DueOn)
			if err != nil {
				return 0, nil, domain.Invalid("due_on", "must be YYYY-MM-DD")
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO pamoja.action_item
				   (legal_entity_id, branch_id, group_id, meeting_id, owner_user_id, description, due_on)
				 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7)`,
				legalEntity, branch, groupID, meetingID, item.OwnerUserID, item.Description, due); err != nil {
				return 0, nil, err
			}
		}

		var out meetingResponse
		if err := tx.QueryRow(ctx,
			`UPDATE pamoja.meeting
			 SET status = 'closed', closed_by = $2::uuid, closed_at = now(), version = version + 1
			 WHERE id = $1::uuid
			 RETURNING id, group_id, scheduled_for, location, status, opened_at, closed_at, version`,
			meetingID, p.UserID).
			Scan(&out.ID, &out.GroupID, &out.ScheduledFor, &out.Location, &out.Status, &out.OpenedAt, &out.ClosedAt, &out.Version); err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "meetings.close", EntityType: "meeting", EntityID: meetingID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"minutes_id": minutesID, "action_items": len(req.ActionItems)},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, out, nil
	})
}

type amendAttendanceRequest struct {
	AttendanceID string `json:"attendance_id"`
	NewStatus    string `json:"new_status"`
	Reason       string `json:"reason"`
	ReviewerID   string `json:"reviewer_id"`
}

func (d Deps) handleAmendAttendance(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	meetingID := idFrom(r, "meetingID")
	groupID, err := d.meetingGroup(r.Context(), p, meetingID, r)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleMeetings, rbac.ActionAmend, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req amendAttendanceRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		var originalRecorder, originalMember string
		if err := tx.QueryRow(ctx,
			`SELECT recorded_by::text, member_id::text FROM pamoja.meeting_attendance
			 WHERE id = $1::uuid AND meeting_id = $2::uuid`,
			req.AttendanceID, meetingID).Scan(&originalRecorder, &originalMember); err != nil {
			return 0, nil, domain.ErrNotFound
		}
		if err := attendance.ValidateAmendment(attendance.Amendment{
			OriginalID: req.AttendanceID, NewStatus: req.NewStatus, Reason: req.Reason,
			ReviewerID: req.ReviewerID, RecorderID: originalRecorder, At: time.Now(),
		}); err != nil {
			return 0, nil, domain.Invalid("amendment", err.Error())
		}
		if err := d.checkSeparation(p, rbac.ModuleMeetings, rbac.ActionAmend,
			map[rbac.ContextField][]string{rbac.CtxReviewer: {req.ReviewerID}}); err != nil {
			return 0, nil, err
		}

		var out attendanceResponse
		err := tx.QueryRow(ctx,
			`INSERT INTO pamoja.meeting_attendance
			   (legal_entity_id, branch_id, group_id, meeting_id, member_id, status,
			    checkin_channel, recorded_by, amendment_of_id, amendment_reason, reviewer_id)
			 SELECT m.legal_entity_id, m.branch_id, m.group_id, m.id, $2::uuid, $3,
			        'assisted', $4::uuid, $5::uuid, $6, $7::uuid
			 FROM pamoja.meeting m WHERE m.id = $1::uuid
			 RETURNING id, meeting_id, member_id, status, checkin_channel, recorded_at,
			           COALESCE(amendment_of_id::text, '')`,
			meetingID, originalMember, req.NewStatus, p.UserID, req.AttendanceID, req.Reason, req.ReviewerID).
			Scan(&out.ID, &out.MeetingID, &out.MemberID, &out.Status, &out.Channel, &out.RecordedAt, &out.AmendmentOf)
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "meetings.amend_attendance", EntityType: "meeting_attendance", EntityID: out.ID,
			ScopeType: "group", ScopeID: groupID, Reason: req.Reason,
			Before: map[string]any{"attendance_id": req.AttendanceID},
			After:  map[string]any{"status": req.NewStatus, "reviewer_id": req.ReviewerID},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, out, nil
	})
}

type memberAttendance struct {
	MemberID string   `json:"member_id"`
	FullName string   `json:"full_name"`
	Expected int      `json:"expected"`
	Attended int      `json:"attended"`
	Percent  *float64 `json:"percent"`
	Label    string   `json:"label"`
}

type attendanceStatsResponse struct {
	Group   memberAttendance   `json:"group"`
	Members []memberAttendance `json:"members"`
	From    string             `json:"from"`
	To      string             `json:"to"`
}

func (d Deps) handleAttendanceStats(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleMeetings, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	from, to, err := dateRange(r)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}

	var resp attendanceStatsResponse
	resp.From, resp.To = from.Format("2006-01-02"), to.Format("2006-01-02")
	err = d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id::text, scheduled_for, status FROM pamoja.meeting
			 WHERE group_id = $1::uuid AND scheduled_for >= $2 AND scheduled_for < $3
			 ORDER BY scheduled_for`, groupID, from, to.AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		var meetings []attendance.Meeting
		for rows.Next() {
			var m attendance.Meeting
			if err := rows.Scan(&m.ID, &m.ScheduledFor, &m.Status); err != nil {
				rows.Close()
				return err
			}
			meetings = append(meetings, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		memberRows, err := tx.Query(ctx,
			`SELECT ms.member_id::text, m.full_name, ms.joined_on, ms.left_on
			 FROM pamoja.membership ms JOIN pamoja.member m ON m.id = ms.member_id
			 WHERE ms.group_id = $1::uuid AND ms.status IN ('active', 'left')
			 ORDER BY m.full_name`, groupID)
		if err != nil {
			return err
		}
		var members []attendance.Membership
		names := map[string]string{}
		for memberRows.Next() {
			var ms attendance.Membership
			var joined time.Time
			var left *time.Time
			var name string
			if err := memberRows.Scan(&ms.MemberID, &name, &joined, &left); err != nil {
				memberRows.Close()
				return err
			}
			ms.JoinedOn = &joined
			ms.LeftOn = left
			names[ms.MemberID] = name
			members = append(members, ms)
		}
		memberRows.Close()
		if err := memberRows.Err(); err != nil {
			return err
		}

		recordRows, err := tx.Query(ctx,
			`SELECT meeting_id::text, member_id::text, status, recorded_at,
			        COALESCE(amendment_of_id::text, '')
			 FROM pamoja.meeting_attendance WHERE group_id = $1::uuid`, groupID)
		if err != nil {
			return err
		}
		var records []attendance.Record
		index := 0
		for recordRows.Next() {
			var rec attendance.Record
			if err := recordRows.Scan(&rec.MeetingID, &rec.MemberID, &rec.Status, &rec.RecordedAt, &rec.AmendmentOfID); err != nil {
				recordRows.Close()
				return err
			}
			rec.AmendmentIndex = index
			index++
			records = append(records, rec)
		}
		recordRows.Close()
		if err := recordRows.Err(); err != nil {
			return err
		}

		policy := attendance.DefaultPolicy()
		groupResult := attendance.Group(meetings, members, records, policy)
		resp.Group = memberAttendance{Expected: groupResult.Expected, Attended: groupResult.Attended,
			Percent: groupResult.Percent, Label: groupResult.Label}
		resp.Members = make([]memberAttendance, 0, len(members))
		for _, ms := range members {
			got := attendance.Individual(meetings, ms, records, policy)
			resp.Members = append(resp.Members, memberAttendance{
				MemberID: ms.MemberID, FullName: names[ms.MemberID],
				Expected: got.Expected, Attended: got.Attended,
				Percent: got.Percent, Label: got.Label,
			})
		}
		return nil
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (d Deps) meetingGroup(ctx context.Context, p principal, meetingID string, r *http.Request) (string, error) {
	var groupID string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT group_id::text FROM pamoja.meeting WHERE id = $1::uuid`, meetingID).Scan(&groupID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return groupID, err
}

func (d Deps) withActor(r *http.Request, fn func(context.Context, pgx.Tx) error) error {
	p, ok := principalFrom(r.Context())
	if !ok {
		return domain.ErrForbidden
	}
	return d.withActorP(r.Context(), p, fn)
}

func (d Deps) withActorP(ctx context.Context, p principal, fn func(context.Context, pgx.Tx) error) error {
	return platformdb.WithActor(ctx, d.Pool, requestActor(p), RequestIDFrom(ctx), fn)
}

func appendAudit(ctx context.Context, tx pgx.Tx, r *http.Request, p principal, e audit.Entry) error {
	e.ActorUserID = p.UserID.String()
	e.Channel = "self"
	e.IP = clientIP(r)
	_, err := audit.New(tx).Append(ctx, RequestIDFrom(r.Context()), e)
	return err
}

func dateRange(r *http.Request) (time.Time, time.Time, error) {
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -90)
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return time.Time{}, time.Time{}, domain.Invalid("to", "must be YYYY-MM-DD")
		}
		to = parsed
	}
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return time.Time{}, time.Time{}, domain.Invalid("from", "must be YYYY-MM-DD")
		}
		from = parsed
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, domain.Invalid("from", "must not be after to")
	}
	return from, to, nil
}
