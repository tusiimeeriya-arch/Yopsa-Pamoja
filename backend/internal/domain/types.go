// Package domain implements the Yopsa Pamoja product core: groups, membership,
// meetings, savings entries, group borrowing applications, follow-up tasks,
// the management dashboard, reports and the audit view.
//
// Every method takes the authenticated principal and enforces the RBAC matrix
// before touching data; queries run inside db.WithActor so row-level security
// always has an actor. Money is whole UGX in int64 (ADR-0009).
package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/auth"
	"github.com/yopsa/pamoja/backend/internal/platform/keyring"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// Errors the HTTP layer maps to responses.
var (
	ErrForbidden = errors.New("domain: forbidden")
	ErrNotFound  = errors.New("domain: not found")
	ErrInvalid   = errors.New("domain: invalid request")
	ErrConflict  = errors.New("domain: conflict")
)

// Service is the domain core.
type Service struct {
	pool   *pgxpool.Pool
	matrix *rbac.Matrix
	audit  *audit.Recorder
	keys   keyring.Provider
}

// New builds a Service.
func New(pool *pgxpool.Pool, matrix *rbac.Matrix, recorder *audit.Recorder, keys keyring.Provider) *Service {
	return &Service{pool: pool, matrix: matrix, audit: recorder, keys: keys}
}

// RoleBinding is one role a user holds at one scope.
type RoleBinding struct {
	Role      string `json:"role"`
	ScopeType string `json:"scope_type"`
	ScopeID   string `json:"scope_id,omitempty"`
}

// Me is the signed-in user's profile and role capability summary.
type Me struct {
	UserID      string          `json:"user_id"`
	FullName    string          `json:"full_name"`
	PhoneMasked string          `json:"phone_masked"`
	IsStaff     bool            `json:"is_staff"`
	MFARequired bool            `json:"mfa_required"`
	Roles       []RoleBinding   `json:"roles"`
	Can         map[string]bool `json:"can"`
}

// Group is one VSLA, investment club, association, cooperative or SACCO.
type Group struct {
	ID               string    `json:"id"`
	Code             string    `json:"code"`
	Name             string    `json:"name"`
	GroupType        string    `json:"group_type"`
	Status           string    `json:"status"`
	FormedOn         string    `json:"formed_on"`
	MeetingDay       string    `json:"meeting_day"`
	MeetingFrequency string    `json:"meeting_frequency"`
	Village          string    `json:"village"`
	SubCounty        string    `json:"sub_county"`
	District         string    `json:"district"`
	CycleStart       string    `json:"cycle_start,omitempty"`
	CycleEnd         string    `json:"cycle_end,omitempty"`
	BranchID         string    `json:"branch_id"`
	BranchName       string    `json:"branch_name"`
	LegalEntityID    string    `json:"legal_entity_id"`
	Members          int       `json:"members"`
	SavingsUGX       int64     `json:"savings_ugx"`
	OutstandingUGX   int64     `json:"outstanding_ugx"`
	ActiveLoans      int       `json:"active_loans"`
	AttendanceRate   int       `json:"attendance_rate"`
	LastMeeting      string    `json:"last_meeting,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// Member is one group member.
type Member struct {
	ID          string    `json:"id"`
	MemberNo    string    `json:"member_no"`
	FullName    string    `json:"full_name"`
	PhoneMasked string    `json:"phone_masked"`
	Gender      string    `json:"gender"`
	RoleInGroup string    `json:"role_in_group"`
	Status      string    `json:"status"`
	JoinedOn    string    `json:"joined_on"`
	SavingsUGX  int64     `json:"savings_ugx"`
	CreatedAt   time.Time `json:"created_at"`
}

// Attendance is one member's row in a meeting register.
type Attendance struct {
	MemberID   string `json:"member_id"`
	MemberName string `json:"member_name"`
	Status     string `json:"status"`
	Note       string `json:"note,omitempty"`
}

// Meeting is one meeting, its register and its verified collection total.
type Meeting struct {
	ID               string       `json:"id"`
	MeetingNo        int          `json:"meeting_no"`
	HeldOn           string       `json:"held_on"`
	MeetingType      string       `json:"meeting_type"`
	Status           string       `json:"status"`
	Location         string       `json:"location"`
	Agenda           string       `json:"agenda"`
	Minutes          string       `json:"minutes"`
	Present          int          `json:"present"`
	Absent           int          `json:"absent"`
	ContributionsUGX int64        `json:"contributions_ugx"`
	Attendance       []Attendance `json:"attendance,omitempty"`
}

// Contribution is one payment entry.
type Contribution struct {
	ID             string    `json:"id"`
	GroupID        string    `json:"group_id"`
	GroupName      string    `json:"group_name"`
	MemberID       string    `json:"member_id"`
	MemberName     string    `json:"member_name"`
	MeetingID      string    `json:"meeting_id,omitempty"`
	Kind           string    `json:"kind"`
	AmountUGX      int64     `json:"amount_ugx"`
	Method         string    `json:"method"`
	Reference      string    `json:"reference"`
	Status         string    `json:"status"`
	Note           string    `json:"note"`
	OccurredOn     string    `json:"occurred_on"`
	RecordedByName string    `json:"recorded_by_name"`
	VerifiedByName string    `json:"verified_by_name,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Loan is one group borrowing application.
type Loan struct {
	ID                string    `json:"id"`
	GroupID           string    `json:"group_id"`
	GroupName         string    `json:"group_name"`
	ApplicantMemberID string    `json:"applicant_member_id,omitempty"`
	ApplicantName     string    `json:"applicant_name,omitempty"`
	AmountUGX         int64     `json:"amount_ugx"`
	Purpose           string    `json:"purpose"`
	TermMonths        int       `json:"term_months"`
	Status            string    `json:"status"`
	AppraisalNote     string    `json:"appraisal_note"`
	DecisionNote      string    `json:"decision_note"`
	AppraisedByName   string    `json:"appraised_by_name,omitempty"`
	ApprovedByName    string    `json:"approved_by_name,omitempty"`
	SubmittedOn       string    `json:"submitted_on,omitempty"`
	DecidedOn         string    `json:"decided_on,omitempty"`
	DisbursedOn       string    `json:"disbursed_on,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// Task is one follow-up task.
type Task struct {
	ID           string    `json:"id"`
	GroupID      string    `json:"group_id,omitempty"`
	GroupName    string    `json:"group_name,omitempty"`
	Title        string    `json:"title"`
	Detail       string    `json:"detail"`
	AssigneeName string    `json:"assignee_name,omitempty"`
	DueOn        string    `json:"due_on,omitempty"`
	Status       string    `json:"status"`
	Priority     string    `json:"priority"`
	CreatedAt    time.Time `json:"created_at"`
}

// GroupDetail is everything one group screen shows.
type GroupDetail struct {
	Group         Group          `json:"group"`
	Members       []Member       `json:"members"`
	Meetings      []Meeting      `json:"meetings"`
	Contributions []Contribution `json:"contributions"`
	Loans         []Loan         `json:"loans"`
	Tasks         []Task         `json:"tasks"`
}

// MonthPoint is one month of verified savings.
type MonthPoint struct {
	Month     string `json:"month"`
	Label     string `json:"label"`
	AmountUGX int64  `json:"amount_ugx"`
}

// StatusCount is a loan count by status.
type StatusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// Attention is one item that needs someone's action.
type Attention struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	GroupID string `json:"group_id,omitempty"`
	Link    string `json:"link,omitempty"`
}

// Totals are the dashboard headline figures.
type Totals struct {
	Groups               int   `json:"groups"`
	Members              int   `json:"members"`
	SavingsUGX           int64 `json:"savings_ugx"`
	OutstandingUGX       int64 `json:"outstanding_ugx"`
	ActiveLoans          int   `json:"active_loans"`
	AttendanceRate       int   `json:"attendance_rate"`
	PendingVerifications int   `json:"pending_verifications"`
	PendingLoans         int   `json:"pending_loans"`
	OpenTasks            int   `json:"open_tasks"`
}

// Dashboard is the management overview.
type Dashboard struct {
	Totals              Totals         `json:"totals"`
	MonthlySavings      []MonthPoint   `json:"monthly_savings"`
	Groups              []Group        `json:"groups"`
	Attention           []Attention    `json:"attention"`
	LoansByStatus       []StatusCount  `json:"loans_by_status"`
	RecentContributions []Contribution `json:"recent_contributions"`
}

// User is one row of the staff directory.
type User struct {
	ID          string    `json:"id"`
	FullName    string    `json:"full_name"`
	PhoneMasked string    `json:"phone_masked"`
	Status      string    `json:"status"`
	IsStaff     bool      `json:"is_staff"`
	MFARequired bool      `json:"mfa_required"`
	Roles       string    `json:"roles"`
	CreatedAt   time.Time `json:"created_at"`
}

// AuditEntry is one row of the append-only audit log.
type AuditEntry struct {
	Seq        int64     `json:"seq"`
	OccurredAt time.Time `json:"occurred_at"`
	ActorName  string    `json:"actor_name"`
	ActorRole  string    `json:"actor_role"`
	Action     string    `json:"action"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id,omitempty"`
	Reason     string    `json:"reason"`
	Channel    string    `json:"channel"`
	ScopeType  string    `json:"scope_type"`
}

// AuditPage is one page of the audit log plus chain integrity.
type AuditPage struct {
	Entries     []AuditEntry `json:"entries"`
	Total       int64        `json:"total"`
	Verified    bool         `json:"verified"`
	Checked     int64        `json:"checked"`
	FirstBadSeq int64        `json:"first_bad_seq,omitempty"`
	Reason      string       `json:"reason,omitempty"`
}

// CreateGroupInput is the body of POST /groups.
type CreateGroupInput struct {
	Name             string `json:"name"`
	GroupType        string `json:"group_type"`
	BranchID         string `json:"branch_id"`
	Village          string `json:"village"`
	SubCounty        string `json:"sub_county"`
	District         string `json:"district"`
	MeetingDay       string `json:"meeting_day"`
	MeetingFrequency string `json:"meeting_frequency"`
	FormedOn         string `json:"formed_on"`
}

// CreateMemberInput is the body of POST /groups/{id}/members.
type CreateMemberInput struct {
	FullName    string `json:"full_name"`
	Phone       string `json:"phone"`
	Gender      string `json:"gender"`
	RoleInGroup string `json:"role_in_group"`
	JoinedOn    string `json:"joined_on"`
}

// UpdateMemberInput is the body of PATCH /groups/{id}/members/{memberID}.
type UpdateMemberInput struct {
	RoleInGroup string `json:"role_in_group"`
	Status      string `json:"status"`
}

// CreateMeetingInput is the body of POST /groups/{id}/meetings.
type CreateMeetingInput struct {
	HeldOn      string `json:"held_on"`
	MeetingType string `json:"meeting_type"`
	Location    string `json:"location"`
	Agenda      string `json:"agenda"`
}

// AttendanceEntry is one row of an attendance update.
type AttendanceEntry struct {
	MemberID string `json:"member_id"`
	Status   string `json:"status"`
	Note     string `json:"note"`
}

// AttendanceInput is the body of POST /meetings/{id}/attendance.
type AttendanceInput struct {
	Entries []AttendanceEntry `json:"entries"`
}

// CloseMeetingInput is the body of POST /meetings/{id}/close.
type CloseMeetingInput struct {
	Minutes string `json:"minutes"`
}

// CreateContributionInput is the body of POST /groups/{id}/contributions.
type CreateContributionInput struct {
	MemberID   string `json:"member_id"`
	MeetingID  string `json:"meeting_id"`
	Kind       string `json:"kind"`
	AmountUGX  int64  `json:"amount_ugx"`
	Method     string `json:"method"`
	Reference  string `json:"reference"`
	Note       string `json:"note"`
	OccurredOn string `json:"occurred_on"`
}

// ReturnContributionInput is the body of POST /contributions/{id}/return.
type ReturnContributionInput struct {
	Note string `json:"note"`
}

// CreateLoanInput is the body of POST /loans.
type CreateLoanInput struct {
	GroupID           string `json:"group_id"`
	ApplicantMemberID string `json:"applicant_member_id"`
	AmountUGX         int64  `json:"amount_ugx"`
	Purpose           string `json:"purpose"`
	TermMonths        int    `json:"term_months"`
}

// NoteInput is a body carrying one note (appraise, approve, decline).
type NoteInput struct {
	Note string `json:"note"`
}

// CreateTaskInput is the body of POST /tasks.
type CreateTaskInput struct {
	GroupID  string `json:"group_id"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	DueOn    string `json:"due_on"`
	Priority string `json:"priority"`
}

// helpers -----------------------------------------------------------------

func path(legalEntity, branch, group string) []rbac.ScopeRef {
	out := make([]rbac.ScopeRef, 0, 3)
	if legalEntity != "" {
		out = append(out, rbac.ScopeRef{Type: rbac.ScopeLegalEntity, ID: legalEntity})
	}
	if branch != "" {
		out = append(out, rbac.ScopeRef{Type: rbac.ScopeBranch, ID: branch})
	}
	if group != "" {
		out = append(out, rbac.ScopeRef{Type: rbac.ScopeGroup, ID: group})
	}
	return out
}

func (s *Service) allowed(bindings []rbac.Binding, userID string, mod rbac.Module, action rbac.Action, legalEntity, branch, group, owner string) bool {
	decision := s.matrix.Authorize(
		rbac.Actor{UserID: userID, Bindings: bindings},
		mod,
		action,
		rbac.Resource{Module: mod, ScopePath: path(legalEntity, branch, group), OwnerUserID: owner},
	)
	return decision.Allowed
}

// maskPhone keeps the country code and the last three digits only.
func maskPhone(raw string) string {
	normalized := auth.NormalizePhone(raw)
	if len(normalized) < 4 {
		return ""
	}
	last := normalized[len(normalized)-3:]
	if strings.HasPrefix(normalized, "+256") {
		return "+256 ••• " + last
	}
	return "••• ••• " + last
}

func cleanDate(value string, fallback time.Time) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback.Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fallback.Format("2006-01-02")
	}
	return value
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
