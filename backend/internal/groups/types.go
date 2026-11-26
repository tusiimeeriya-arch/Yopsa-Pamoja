// Package groups implements the registration domain from ERD §1: legal
// entities, branches, group types, groups, members, memberships, consent and
// authority records.
//
// Authorisation is enforced here, not in the HTTP layer: every method takes
// the caller's rbac.Actor, resolves the resource's scope path and calls
// rbac.Authorize before reading or writing. PostgreSQL row-level security then
// narrows the rows to the same scopes, so a mistake in one layer cannot leak
// another branch's data.
package groups

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Domain errors. The HTTP layer maps these to status codes without exposing
// why an authorisation check failed.
var (
	ErrForbidden  = errors.New("groups: forbidden")
	ErrNotFound   = errors.New("groups: not found")
	ErrConflict   = errors.New("groups: conflict")
	ErrValidation = errors.New("groups: validation")
)

// ConsentNoticeVersion is the data-protection notice version members accept
// when they join. Bump it when docs/DATA_PROTECTION.md changes materially;
// consent records keep the version they were captured against.
const ConsentNoticeVersion = "2026-11"

// LegalEntity is one Yopsa legal entity.
type LegalEntity struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Branch is one branch of a legal entity.
type Branch struct {
	ID            string `json:"id"`
	LegalEntityID string `json:"legal_entity_id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	District      string `json:"district"`
	Status        string `json:"status"`
}

// GroupType is reference data describing a form of group.
type GroupType struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	RulesVersion string `json:"rules_version"`
	Description  string `json:"description"`
}

// Group is one VSLA, club, association, cooperative or SACCO.
type Group struct {
	ID                     string     `json:"id"`
	LegalEntityID          string     `json:"legal_entity_id"`
	BranchID               string     `json:"branch_id"`
	TypeCode               string     `json:"type_code"`
	Name                   string     `json:"name"`
	RegistrationStatus     string     `json:"registration_status"`
	Status                 string     `json:"status"`
	RegistrationEvidenceID *string    `json:"registration_evidence_id,omitempty"`
	FormedOn               *string    `json:"formed_on,omitempty"`
	ClosedOn               *string    `json:"closed_on,omitempty"`
	CreatedBy              string     `json:"created_by"`
	ReviewedBy             *string    `json:"reviewed_by,omitempty"`
	ReviewedAt             *time.Time `json:"reviewed_at,omitempty"`
	ReviewNote             string     `json:"review_note,omitempty"`
	Version                int        `json:"version"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// Membership is a member's active or past participation in a group. The
// member's phone number is deliberately never returned.
type Membership struct {
	ID        string  `json:"id"`
	MemberID  string  `json:"member_id"`
	GroupID   string  `json:"group_id"`
	MemberNo  string  `json:"member_no"`
	FullName  string  `json:"full_name"`
	Gender    string  `json:"gender"`
	JoinedOn  string  `json:"joined_on"`
	LeftOn    *string `json:"left_on,omitempty"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
}

// Authority is a member's appointment to a group office.
type Authority struct {
	ID                   string  `json:"id"`
	GroupID              string  `json:"group_id"`
	MemberID             string  `json:"member_id"`
	MemberName           string  `json:"member_name"`
	Office               string  `json:"office"`
	EffectiveFrom        string  `json:"effective_from"`
	EffectiveTo          *string `json:"effective_to,omitempty"`
	ResolutionEvidenceID *string `json:"resolution_evidence_id,omitempty"`
	RecordedBy           string  `json:"recorded_by"`
}

// Consent is one member's recorded decision for one purpose and notice
// version.
type Consent struct {
	ID            string    `json:"id"`
	MemberID      string    `json:"member_id"`
	Purpose       string    `json:"purpose"`
	Granted       bool      `json:"granted"`
	NoticeVersion string    `json:"notice_version"`
	CapturedAt    time.Time `json:"captured_at"`
	CapturedBy    string    `json:"captured_by"`
}

// CreateGroupInput registers a group in draft.
type CreateGroupInput struct {
	BranchID               string `json:"branch_id"`
	TypeCode               string `json:"type_code"`
	Name                   string `json:"name"`
	FormedOn               string `json:"formed_on"`
	RegistrationEvidenceID string `json:"registration_evidence_id"`
}

// UpdateGroupInput edits a draft or returned group. Nil fields are unchanged.
type UpdateGroupInput struct {
	Name                   *string `json:"name"`
	TypeCode               *string `json:"type_code"`
	FormedOn               *string `json:"formed_on"`
	RegistrationEvidenceID *string `json:"registration_evidence_id"`
	Version                int     `json:"version"`
}

// ReviewInput records a reviewer's decision on a submitted group.
type ReviewInput struct {
	Decision string `json:"decision"` // activate | return | reject
	Note     string `json:"note"`
	Version  int    `json:"version"`
}

// AddMembershipInput adds a member to a group, creating the member record when
// the phone number has not been seen before.
type AddMembershipInput struct {
	FullName    string `json:"full_name"`
	Phone       string `json:"phone"`
	Gender      string `json:"gender"`
	DateOfBirth string `json:"date_of_birth"`
	MemberNo    string `json:"member_no"`
	JoinedOn    string `json:"joined_on"`
}

// EndMembershipInput end-dates a membership.
type EndMembershipInput struct {
	LeftOn string `json:"left_on"`
}

// AppointAuthorityInput appoints a member to a group office.
type AppointAuthorityInput struct {
	MemberID             string `json:"member_id"`
	Office               string `json:"office"`
	EffectiveFrom        string `json:"effective_from"`
	ResolutionEvidenceID string `json:"resolution_evidence_id"`
}

// EndAuthorityInput end-dates an appointment.
type EndAuthorityInput struct {
	EffectiveTo string `json:"effective_to"`
}

// RecordConsentInput records a member's consent decision.
type RecordConsentInput struct {
	Purpose       string `json:"purpose"`
	Granted       bool   `json:"granted"`
	NoticeVersion string `json:"notice_version"`
}

// validateText trims and bounds a human name or label.
func validateText(field, value string, minLen, maxLen int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < minLen || len(trimmed) > maxLen {
		return "", fmt.Errorf("%w: %s must be %d-%d characters", ErrValidation, field, minLen, maxLen)
	}
	return trimmed, nil
}

// parseDate accepts the API's YYYY-MM-DD form and rejects a future business
// date where that would be nonsensical (a group cannot have formed next year).
func parseDate(field, raw string, required bool, now time.Time) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return nil, fmt.Errorf("%w: %s is required", ErrValidation, field)
		}
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be YYYY-MM-DD", ErrValidation, field)
	}
	// Business dates are Ugandan dates; the fixed offset avoids depending on
	// tzdata in the runtime image (Uganda has no daylight saving).
	eastAfrica := time.FixedZone("EAT", 3*60*60)
	local := now.In(eastAfrica)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	if parsed.After(today) {
		return nil, fmt.Errorf("%w: %s may not be in the future", ErrValidation, field)
	}
	return &parsed, nil
}

func validOffice(office string) bool {
	switch office {
	case "chairperson", "secretary", "treasurer":
		return true
	}
	return false
}

func validConsentPurpose(purpose string) bool {
	switch purpose {
	case "membership", "data_processing", "photos", "notifications":
		return true
	}
	return false
}

func validGender(gender string) bool {
	switch gender {
	case "female", "male", "other", "undisclosed":
		return true
	}
	return false
}
