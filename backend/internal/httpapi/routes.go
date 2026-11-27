package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/domain"
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
	Domain  *domain.Service
}

// Route is one entry of the HTTP surface. Routes are declared as data so that
// tests can prove every non-public route names the RBAC module and action it
// requires; a handler cannot be registered without that declaration.
type Route struct {
	Method  string
	Pattern string
	Module  rbac.Module
	Action  rbac.Action
	Public  bool // unauthenticated: the client shell, assets, probes, login, refresh.
	Handler http.HandlerFunc
}

// Routes returns the complete HTTP surface for this build.
func Routes(d Deps) []Route {
	return []Route{
		{Method: http.MethodGet, Pattern: "/", Public: true, Handler: d.handleLanding},
		{Method: http.MethodGet, Pattern: "/assets/*", Public: true, Handler: d.handleAsset},
		{Method: http.MethodGet, Pattern: "/favicon.svg", Public: true, Handler: d.handleFavicon},
		{Method: http.MethodGet, Pattern: "/healthz", Public: true, Handler: d.handleLiveness},
		{Method: http.MethodGet, Pattern: "/readyz", Public: true, Handler: d.handleReadiness},
		{Method: http.MethodGet, Pattern: "/api/v1/meta", Public: true, Handler: d.handleMeta},

		{Method: http.MethodPost, Pattern: "/api/v1/auth/login", Public: true, Handler: d.handleLogin},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/refresh", Public: true, Handler: d.handleRefresh},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/logout", Module: rbac.ModuleAuth, Action: rbac.ActionRevoke, Handler: d.requireAuth(d.handleLogout)},
		{Method: http.MethodGet, Pattern: "/api/v1/auth/sessions", Module: rbac.ModuleAuth, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListSessions)},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/sessions/{sessionID}/revoke", Module: rbac.ModuleAuth, Action: rbac.ActionRevoke, Handler: d.requireAuth(d.handleRevokeSession)},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/step-up", Module: rbac.ModuleAuth, Action: rbac.ActionStepUp, Handler: d.requireAuth(d.handleStepUp)},

		{Method: http.MethodGet, Pattern: "/api/v1/me", Module: rbac.ModuleAuth, Action: rbac.ActionView, Handler: d.requireAuth(d.handleMe)},
		{Method: http.MethodGet, Pattern: "/api/v1/dashboard", Module: rbac.ModuleDashboard, Action: rbac.ActionView, Handler: d.requireAuth(d.handleDashboard)},

		{Method: http.MethodGet, Pattern: "/api/v1/groups", Module: rbac.ModuleRegistration, Action: rbac.ActionView, Handler: d.requireAuth(d.handleGroups)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups", Module: rbac.ModuleRegistration, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateGroup)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}", Module: rbac.ModuleRegistration, Action: rbac.ActionView, Handler: d.requireAuth(d.handleGroup)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/activate", Module: rbac.ModuleRegistration, Action: rbac.ActionReviewActivate, Handler: d.requireAuth(d.handleActivateGroup)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/members", Module: rbac.ModuleRegistration, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateMember)},
		{Method: http.MethodPatch, Pattern: "/api/v1/groups/{groupID}/members/{memberID}", Module: rbac.ModuleRegistration, Action: rbac.ActionEditDraft, Handler: d.requireAuth(d.handleUpdateMember)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/meetings", Module: rbac.ModuleMeetings, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateMeeting)},
		{Method: http.MethodPost, Pattern: "/api/v1/meetings/{meetingID}/attendance", Module: rbac.ModuleMeetings, Action: rbac.ActionEditDraft, Handler: d.requireAuth(d.handleSetAttendance)},
		{Method: http.MethodPost, Pattern: "/api/v1/meetings/{meetingID}/close", Module: rbac.ModuleMeetings, Action: rbac.ActionClose, Handler: d.requireAuth(d.handleCloseMeeting)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/contributions", Module: rbac.ModulePayments, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateContribution)},
		{Method: http.MethodPost, Pattern: "/api/v1/contributions/{contributionID}/verify", Module: rbac.ModulePayments, Action: rbac.ActionVerify, Handler: d.requireAuth(d.handleVerifyContribution)},
		{Method: http.MethodPost, Pattern: "/api/v1/contributions/{contributionID}/return", Module: rbac.ModulePayments, Action: rbac.ActionReturn, Handler: d.requireAuth(d.handleReturnContribution)},

		{Method: http.MethodGet, Pattern: "/api/v1/loans", Module: rbac.ModuleLoans, Action: rbac.ActionView, Handler: d.requireAuth(d.handleLoans)},
		{Method: http.MethodPost, Pattern: "/api/v1/loans", Module: rbac.ModuleLoans, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateLoan)},
		{Method: http.MethodPost, Pattern: "/api/v1/loans/{loanID}/appraise", Module: rbac.ModuleLoans, Action: rbac.ActionAppraise, Handler: d.requireAuth(d.handleAppraiseLoan)},
		{Method: http.MethodPost, Pattern: "/api/v1/loans/{loanID}/approve", Module: rbac.ModuleLoans, Action: rbac.ActionApprove, Handler: d.requireAuth(d.handleApproveLoan)},
		{Method: http.MethodPost, Pattern: "/api/v1/loans/{loanID}/decline", Module: rbac.ModuleLoans, Action: rbac.ActionDecline, Handler: d.requireAuth(d.handleDeclineLoan)},
		{Method: http.MethodPost, Pattern: "/api/v1/loans/{loanID}/disburse", Module: rbac.ModuleLoans, Action: rbac.ActionDisburse, Handler: d.requireAuth(d.handleDisburseLoan)},

		{Method: http.MethodGet, Pattern: "/api/v1/tasks", Module: rbac.ModuleTasks, Action: rbac.ActionView, Handler: d.requireAuth(d.handleTasks)},
		{Method: http.MethodPost, Pattern: "/api/v1/tasks", Module: rbac.ModuleTasks, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateTask)},
		{Method: http.MethodPost, Pattern: "/api/v1/tasks/{taskID}/close", Module: rbac.ModuleTasks, Action: rbac.ActionClose, Handler: d.requireAuth(d.handleCloseTask)},

		{Method: http.MethodGet, Pattern: "/api/v1/audit", Module: rbac.ModuleAudit, Action: rbac.ActionView, Handler: d.requireAuth(d.handleAudit)},
		{Method: http.MethodGet, Pattern: "/api/v1/users", Module: rbac.ModuleUsersRoles, Action: rbac.ActionView, Handler: d.requireAuth(d.handleUsers)},
		{Method: http.MethodGet, Pattern: "/api/v1/reports/portfolio.csv", Module: rbac.ModuleReports, Action: rbac.ActionExport, Handler: d.requireAuth(d.handlePortfolioCSV)},
	}
}
