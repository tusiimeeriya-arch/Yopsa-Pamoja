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
// mint the session that every other route requires. Domain routes arrive in
// P2 and must declare their module and action here.
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

		// P3 — meetings, register, minutes and controlled amendments.
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/meetings", Module: rbac.ModuleMeetings, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateMeeting)},
		{Method: http.MethodPost, Pattern: "/api/v1/meetings/{meetingID}/open", Module: rbac.ModuleMeetings, Action: rbac.ActionEditDraft, Handler: d.requireAuth(d.handleOpenMeeting)},
		{Method: http.MethodPost, Pattern: "/api/v1/meetings/{meetingID}/check-in", Module: rbac.ModuleMeetings, Action: rbac.ActionCheckIn, Handler: d.requireAuth(d.handleCheckIn)},
		{Method: http.MethodPost, Pattern: "/api/v1/meetings/{meetingID}/confirm", Module: rbac.ModuleMeetings, Action: rbac.ActionEditDraft, Handler: d.requireAuth(d.handleConfirmRegister)},
		{Method: http.MethodPost, Pattern: "/api/v1/meetings/{meetingID}/close", Module: rbac.ModuleMeetings, Action: rbac.ActionClose, Handler: d.requireAuth(d.handleCloseMeetingWithRecord)},
		{Method: http.MethodPost, Pattern: "/api/v1/meetings/{meetingID}/amend-attendance", Module: rbac.ModuleMeetings, Action: rbac.ActionAmend, Handler: d.requireAuth(d.handleAmendAttendance)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/attendance", Module: rbac.ModuleMeetings, Action: rbac.ActionView, Handler: d.requireAuth(d.handleAttendanceStats)},
	}
}
