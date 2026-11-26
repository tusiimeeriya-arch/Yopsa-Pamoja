package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/yopsa/pamoja/backend/internal/groups"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// GroupsService is the registration surface the HTTP layer needs. The concrete
// implementation is *groups.Service; the interface keeps handler tests free of
// a database.
type GroupsService interface {
	ListLegalEntities(ctx context.Context, actor rbac.Actor, requestID string) ([]groups.LegalEntity, error)
	ListBranches(ctx context.Context, actor rbac.Actor, legalEntityID, requestID string) ([]groups.Branch, error)
	ListGroupTypes(ctx context.Context) ([]groups.GroupType, error)
	CreateGroup(ctx context.Context, actor rbac.Actor, in groups.CreateGroupInput, requestID string) (*groups.Group, error)
	GetGroup(ctx context.Context, actor rbac.Actor, groupID, requestID string) (*groups.Group, error)
	ListGroups(ctx context.Context, actor rbac.Actor, branchID, legalEntityID, requestID string) ([]groups.Group, error)
	UpdateGroup(ctx context.Context, actor rbac.Actor, groupID string, in groups.UpdateGroupInput, requestID string) (*groups.Group, error)
	SubmitGroup(ctx context.Context, actor rbac.Actor, groupID string, version int, requestID string) (*groups.Group, error)
	ReviewGroup(ctx context.Context, actor rbac.Actor, groupID string, in groups.ReviewInput, requestID string) (*groups.Group, error)
	ListMemberships(ctx context.Context, actor rbac.Actor, groupID, requestID string) ([]groups.Membership, error)
	AddMembership(ctx context.Context, actor rbac.Actor, groupID string, in groups.AddMembershipInput, requestID string) (*groups.Membership, error)
	EndMembership(ctx context.Context, actor rbac.Actor, groupID, membershipID string, in groups.EndMembershipInput, requestID string) (*groups.Membership, error)
	ListAuthorities(ctx context.Context, actor rbac.Actor, groupID, requestID string) ([]groups.Authority, error)
	AppointAuthority(ctx context.Context, actor rbac.Actor, groupID string, in groups.AppointAuthorityInput, requestID string) (*groups.Authority, error)
	EndAuthority(ctx context.Context, actor rbac.Actor, groupID, authorityID string, in groups.EndAuthorityInput, requestID string) (*groups.Authority, error)
	RecordConsent(ctx context.Context, actor rbac.Actor, memberID string, in groups.RecordConsentInput, requestID string) (*groups.Consent, error)
}

func (d Deps) groupsReady(w http.ResponseWriter, r *http.Request) bool {
	if d.Groups == nil {
		writeError(w, r, http.StatusServiceUnavailable, "registration_unavailable")
		return false
	}
	return true
}

func actorFrom(r *http.Request) (rbac.Actor, bool) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		return rbac.Actor{}, false
	}
	return rbac.Actor{UserID: principal.UserID.String(), Bindings: principal.Bindings}, true
}

// handleGroupsError maps domain errors to HTTP without leaking row-level
// detail. An unexpected error is logged with the correlation id and answered
// generically.
func (d Deps) handleGroupsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, groups.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden")
	case errors.Is(err, groups.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found")
	case errors.Is(err, groups.ErrConflict):
		writeError(w, r, http.StatusConflict, "conflict")
	case errors.Is(err, groups.ErrValidation):
		detail := strings.TrimPrefix(err.Error(), groups.ErrValidation.Error()+": ")
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":      "invalid_request",
			"detail":     detail,
			"request_id": RequestIDFrom(r.Context()),
		})
	default:
		d.logger().Error("registration request failed",
			"error", err.Error(),
			"request_id", RequestIDFrom(r.Context()))
		writeError(w, r, http.StatusInternalServerError, "internal_error")
	}
}

func (d Deps) handleListLegalEntities(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := d.Groups.ListLegalEntities(r.Context(), actor, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"legal_entities": items})
}

func (d Deps) handleListBranches(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := d.Groups.ListBranches(r.Context(), actor,
		chi.URLParam(r, "legalEntityID"), RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": items})
}

// handleListGroupTypes is public reference data: the labels a registration form
// offers before a session exists. It contains no personal or financial data.
func (d Deps) handleListGroupTypes(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	items, err := d.Groups.ListGroupTypes(r.Context())
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group_types": items})
}

func (d Deps) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in groups.CreateGroupInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	group, err := d.Groups.CreateGroup(r.Context(), actor, in, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"group": group})
}

func (d Deps) handleListGroups(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	query := r.URL.Query()
	items, err := d.Groups.ListGroups(r.Context(), actor,
		query.Get("branch_id"), query.Get("legal_entity_id"), RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": items})
}

func (d Deps) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	group, err := d.Groups.GetGroup(r.Context(), actor,
		chi.URLParam(r, "groupID"), RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group})
}

func (d Deps) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in groups.UpdateGroupInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	group, err := d.Groups.UpdateGroup(r.Context(), actor,
		chi.URLParam(r, "groupID"), in, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group})
}

type submitGroupRequest struct {
	Version int `json:"version"`
}

func (d Deps) handleSubmitGroup(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in submitGroupRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	group, err := d.Groups.SubmitGroup(r.Context(), actor,
		chi.URLParam(r, "groupID"), in.Version, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group})
}

func (d Deps) handleReviewGroup(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in groups.ReviewInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	group, err := d.Groups.ReviewGroup(r.Context(), actor,
		chi.URLParam(r, "groupID"), in, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group})
}

func (d Deps) handleListMemberships(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := d.Groups.ListMemberships(r.Context(), actor,
		chi.URLParam(r, "groupID"), RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memberships": items})
}

func (d Deps) handleAddMembership(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in groups.AddMembershipInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	membership, err := d.Groups.AddMembership(r.Context(), actor,
		chi.URLParam(r, "groupID"), in, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"membership": membership})
}

func (d Deps) handleEndMembership(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in groups.EndMembershipInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	membership, err := d.Groups.EndMembership(r.Context(), actor,
		chi.URLParam(r, "groupID"), chi.URLParam(r, "membershipID"),
		in, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"membership": membership})
}

func (d Deps) handleListAuthorities(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := d.Groups.ListAuthorities(r.Context(), actor,
		chi.URLParam(r, "groupID"), RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authorities": items})
}

func (d Deps) handleAppointAuthority(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in groups.AppointAuthorityInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	authority, err := d.Groups.AppointAuthority(r.Context(), actor,
		chi.URLParam(r, "groupID"), in, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"authority": authority})
}

func (d Deps) handleEndAuthority(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in groups.EndAuthorityInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	authority, err := d.Groups.EndAuthority(r.Context(), actor,
		chi.URLParam(r, "groupID"), chi.URLParam(r, "authorityID"),
		in, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authority": authority})
}

func (d Deps) handleRecordConsent(w http.ResponseWriter, r *http.Request) {
	if !d.groupsReady(w, r) {
		return
	}
	actor, ok := actorFrom(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in groups.RecordConsentInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	consent, err := d.Groups.RecordConsent(r.Context(), actor,
		chi.URLParam(r, "memberID"), in, RequestIDFrom(r.Context()))
	if err != nil {
		d.handleGroupsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"consent": consent})
}
