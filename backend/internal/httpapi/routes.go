package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/payments"
	"github.com/yopsa/pamoja/backend/internal/platform/config"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// Deps are the collaborators a handler may use. Every field is optional so the
// route registry can be inspected (and tested) without a running stack.
type Deps struct {
	Pool     *pgxpool.Pool
	Logger   *slog.Logger
	Config   config.Config
	Version  string
	Auth     AuthService
	Payments *payments.Service
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

		// P4 — payments, submissions, verification, posting and corrections.
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/payment-entries", Module: rbac.ModulePayments, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateEntry)},
		{Method: http.MethodPatch, Pattern: "/api/v1/payment-entries/{entryID}", Module: rbac.ModulePayments, Action: rbac.ActionEditDraft, Handler: d.requireAuth(d.handleUpdateEntry)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/payment-entries", Module: rbac.ModulePayments, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListEntries)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/submissions", Module: rbac.ModulePayments, Action: rbac.ActionLock, Handler: d.requireAuth(d.handleLockSubmission)},
		{Method: http.MethodPost, Pattern: "/api/v1/submissions/{submissionID}/submit", Module: rbac.ModulePayments, Action: rbac.ActionSubmit, Handler: d.requireAuth(d.handleSubmitSubmission)},
		{Method: http.MethodPost, Pattern: "/api/v1/submissions/{submissionID}/verify", Module: rbac.ModulePayments, Action: rbac.ActionVerify, Handler: d.requireAuth(d.handleVerifySubmission)},
		{Method: http.MethodPost, Pattern: "/api/v1/submissions/{submissionID}/return", Module: rbac.ModulePayments, Action: rbac.ActionReturn, Handler: d.requireAuth(d.handleReturnSubmission)},
		{Method: http.MethodPost, Pattern: "/api/v1/postings/{postingID}/retry", Module: rbac.ModuleReconciliation, Action: rbac.ActionPost, Handler: d.requireAuth(d.handleRetryPosting)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/payments/summary", Module: rbac.ModulePayments, Action: rbac.ActionView, Handler: d.requireAuth(d.handlePaymentsSummary)},
		{Method: http.MethodPost, Pattern: "/api/v1/corrections", Module: rbac.ModulePayments, Action: rbac.ActionCorrect, Handler: d.requireAuth(d.handleCreateCorrection)},
		{Method: http.MethodPost, Pattern: "/api/v1/corrections/{correctionID}/approve", Module: rbac.ModulePayments, Action: rbac.ActionCorrect, Handler: d.requireAuth(d.handleApproveCorrection)},
		{Method: http.MethodPost, Pattern: "/api/v1/corrections/{correctionID}/apply", Module: rbac.ModulePayments, Action: rbac.ActionCorrect, Handler: d.requireAuth(d.handleApplyCorrection)},
		{Method: http.MethodGet, Pattern: "/api/v1/finance-exceptions", Module: rbac.ModuleReconciliation, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListExceptions)},
		{Method: http.MethodPost, Pattern: "/api/v1/finance-exceptions/{exceptionID}/resolve", Module: rbac.ModuleReconciliation, Action: rbac.ActionPost, Handler: d.requireAuth(d.handleResolveException)},
	}
}
