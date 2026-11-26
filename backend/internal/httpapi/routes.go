package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/platform/config"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// Deps are the collaborators a handler may use. Every field is optional so the
// route registry can be inspected (and tested) without a running stack.
type Deps struct {
	Pool    *pgxpool.Pool
	Logger  *slog.Logger
	Config  config.Config
	Version string
	Auth    AuthService
	Groups  GroupsService
}

// Route is one entry of the HTTP surface. Routes are declared as data so that
// tests can prove every non-public route names the RBAC module and action it
// requires; a handler cannot be registered without that declaration.
type Route struct {
	Method  string
	Pattern string
	Module  rbac.Module
	Action  rbac.Action
	Public  bool // unauthenticated: liveness, readiness, landing page, login, refresh.
	Handler http.HandlerFunc
}

// Routes returns the complete HTTP surface for this build.
//
// Authentication endpoints are the only public state-changing routes: they
// mint the session that every other route requires. Group types are public
// reference data. Every other domain route declares its RBAC module and
// action here.
func Routes(d Deps) []Route {
	return []Route{
		{Method: http.MethodGet, Pattern: "/", Public: true, Handler: d.handleLanding},
		{Method: http.MethodGet, Pattern: "/healthz", Public: true, Handler: d.handleLiveness},
		{Method: http.MethodGet, Pattern: "/readyz", Public: true, Handler: d.handleReadiness},
		{Method: http.MethodGet, Pattern: "/api/v1/meta", Public: true, Handler: d.handleMeta},

		{Method: http.MethodPost, Pattern: "/api/v1/auth/login", Public: true, Handler: d.handleLogin},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/refresh", Public: true, Handler: d.handleRefresh},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/logout", Module: rbac.ModuleAuth, Action: rbac.ActionRevoke, Handler: d.requireAuth(d.handleLogout)},
		{Method: http.MethodGet, Pattern: "/api/v1/auth/sessions", Module: rbac.ModuleAuth, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListSessions)},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/sessions/{sessionID}/revoke", Module: rbac.ModuleAuth, Action: rbac.ActionRevoke, Handler: d.requireAuth(d.handleRevokeSession)},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/step-up", Module: rbac.ModuleAuth, Action: rbac.ActionStepUp, Handler: d.requireAuth(d.handleStepUp)},

		// P2 registration: identity, organisation and authority.
		{Method: http.MethodGet, Pattern: "/api/v1/group-types", Public: true, Handler: d.handleListGroupTypes},
		{Method: http.MethodGet, Pattern: "/api/v1/legal-entities", Module: rbac.ModuleRegistration, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListLegalEntities)},
		{Method: http.MethodGet, Pattern: "/api/v1/legal-entities/{legalEntityID}/branches", Module: rbac.ModuleRegistration, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListBranches)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups", Module: rbac.ModuleRegistration, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateGroup)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups", Module: rbac.ModuleRegistration, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListGroups)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}", Module: rbac.ModuleRegistration, Action: rbac.ActionView, Handler: d.requireAuth(d.handleGetGroup)},
		{Method: http.MethodPatch, Pattern: "/api/v1/groups/{groupID}", Module: rbac.ModuleRegistration, Action: rbac.ActionEditDraft, Handler: d.requireAuth(d.handleUpdateGroup)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/submit", Module: rbac.ModuleRegistration, Action: rbac.ActionSubmit, Handler: d.requireAuth(d.handleSubmitGroup)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/review", Module: rbac.ModuleRegistration, Action: rbac.ActionReviewActivate, Handler: d.requireAuth(d.handleReviewGroup)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/members", Module: rbac.ModuleRegistration, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListMemberships)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/members", Module: rbac.ModuleRegistration, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleAddMembership)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/members/{membershipID}/end", Module: rbac.ModuleRegistration, Action: rbac.ActionEditDraft, Handler: d.requireAuth(d.handleEndMembership)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/authorities", Module: rbac.ModuleRegistration, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListAuthorities)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/authorities", Module: rbac.ModuleRegistration, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleAppointAuthority)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/authorities/{authorityID}/end", Module: rbac.ModuleRegistration, Action: rbac.ActionEditDraft, Handler: d.requireAuth(d.handleEndAuthority)},
		{Method: http.MethodPost, Pattern: "/api/v1/members/{memberID}/consents", Module: rbac.ModuleRegistration, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleRecordConsent)},
	}
}
