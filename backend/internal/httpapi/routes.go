package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/engagement"
	"github.com/yopsa/pamoja/backend/internal/loans"
	"github.com/yopsa/pamoja/backend/internal/offline"
	"github.com/yopsa/pamoja/backend/internal/payments"
	"github.com/yopsa/pamoja/backend/internal/platform/config"
	"github.com/yopsa/pamoja/backend/internal/rbac"
	"github.com/yopsa/pamoja/backend/internal/reporting"
)

// Deps are the collaborators a handler may use. Every field is optional so the
// route registry can be inspected (and tested) without a running stack.
type Deps struct {
	Pool       *pgxpool.Pool
	Logger     *slog.Logger
	Config     config.Config
	Version    string
	Auth       AuthService
	Payments   *payments.Service
	Loans      *loans.Service
	Reports    *reporting.Service
	Engagement *engagement.Service
	Offline    *offline.Service
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

		// P5 — borrowing: application, appraisal, operations review, approval,
		// offer, acceptance, disbursement and repayment.
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/loan-applications", Module: rbac.ModuleLoans, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateLoanApplication)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/loan-applications", Module: rbac.ModuleLoans, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListLoanApplications)},
		{Method: http.MethodGet, Pattern: "/api/v1/loan-applications/{applicationID}", Module: rbac.ModuleLoans, Action: rbac.ActionView, Handler: d.requireAuth(d.handleGetLoanApplication)},
		{Method: http.MethodPost, Pattern: "/api/v1/loan-applications/{applicationID}/appraise", Module: rbac.ModuleLoans, Action: rbac.ActionAppraise, Handler: d.requireAuth(d.handleAppraiseLoan)},
		{Method: http.MethodPost, Pattern: "/api/v1/loan-applications/{applicationID}/review", Module: rbac.ModuleLoans, Action: rbac.ActionSubmit, Handler: d.requireAuth(d.handleSendLoanToReview)},
		{Method: http.MethodPost, Pattern: "/api/v1/loan-applications/{applicationID}/decision", Module: rbac.ModuleLoans, Action: rbac.ActionApprove, Handler: d.requireAuth(d.handleDecideLoan)},
		{Method: http.MethodPost, Pattern: "/api/v1/loan-applications/{applicationID}/offer", Module: rbac.ModuleLoans, Action: rbac.ActionApprove, Handler: d.requireAuth(d.handleIssueLoanOffer)},
		{Method: http.MethodPost, Pattern: "/api/v1/loan-applications/{applicationID}/offer/accept", Module: rbac.ModuleLoans, Action: rbac.ActionAcceptOffer, Handler: d.requireAuth(d.handleAcceptLoanOffer)},
		{Method: http.MethodPost, Pattern: "/api/v1/loan-applications/{applicationID}/disburse", Module: rbac.ModuleLoans, Action: rbac.ActionDisburse, Handler: d.requireAuth(d.handleDisburseLoan)},
		{Method: http.MethodPost, Pattern: "/api/v1/loan-applications/{applicationID}/material-change", Module: rbac.ModuleLoans, Action: rbac.ActionApprove, Handler: d.requireAuth(d.handleLoanMaterialChange)},
		{Method: http.MethodPost, Pattern: "/api/v1/loan-applications/{applicationID}/repayments", Module: rbac.ModuleLoans, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleRecordLoanRepayment)},

		// P6 — dashboard cards and drill-downs, audited exports, ranking.
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/dashboard", Module: rbac.ModuleDashboard, Action: rbac.ActionView, Handler: d.requireAuth(d.handleDashboard)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/dashboard/{card}", Module: rbac.ModuleDashboard, Action: rbac.ActionView, Handler: d.requireAuth(d.handleDashboardCard)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/reports/exports", Module: rbac.ModuleReports, Action: rbac.ActionExport, Handler: d.requireAuth(d.handleCreateExport)},
		{Method: http.MethodGet, Pattern: "/api/v1/reports/exports/{exportID}", Module: rbac.ModuleReports, Action: rbac.ActionView, Handler: d.requireAuth(d.handleDownloadExport)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/ranking", Module: rbac.ModuleRanking, Action: rbac.ActionView, Handler: d.requireAuth(d.handleGroupRanking)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/ranking/snapshots", Module: rbac.ModuleRanking, Action: rbac.ActionConfigure, Handler: d.requireAuth(d.handleCreateRankingSnapshot)},

		// P7 — learning progress, comments, disputes, tasks, notifications.
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/learning/progress", Module: rbac.ModuleLearning, Action: rbac.ActionComplete, Handler: d.requireAuth(d.handleRecordProgress)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/learning/progress", Module: rbac.ModuleLearning, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListProgress)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/comments", Module: rbac.ModuleComments, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleAddComment)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/comments", Module: rbac.ModuleComments, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListComments)},
		{Method: http.MethodPost, Pattern: "/api/v1/comments/{commentID}/moderate", Module: rbac.ModuleComments, Action: rbac.ActionModerate, Handler: d.requireAuth(d.handleModerateComment)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/disputes", Module: rbac.ModuleDisputes, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleRaiseDispute)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/disputes", Module: rbac.ModuleDisputes, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListDisputes)},
		{Method: http.MethodPost, Pattern: "/api/v1/disputes/{disputeID}/resolve", Module: rbac.ModuleDisputes, Action: rbac.ActionResolve, Handler: d.requireAuth(d.handleResolveDispute)},
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/tasks", Module: rbac.ModuleTasks, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleCreateTask)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/tasks", Module: rbac.ModuleTasks, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListTasks)},
		{Method: http.MethodPost, Pattern: "/api/v1/tasks/{taskID}/close", Module: rbac.ModuleTasks, Action: rbac.ActionClose, Handler: d.requireAuth(d.handleCloseTask)},
		{Method: http.MethodGet, Pattern: "/api/v1/notifications", Module: rbac.ModuleNotifications, Action: rbac.ActionView, Handler: d.requireAuth(d.handleListNotifications)},
		{Method: http.MethodPost, Pattern: "/api/v1/notifications/{notificationID}/read", Module: rbac.ModuleNotifications, Action: rbac.ActionView, Handler: d.requireAuth(d.handleReadNotification)},

		// P8 — offline outbox replay.
		{Method: http.MethodPost, Pattern: "/api/v1/groups/{groupID}/sync/batches", Module: rbac.ModulePayments, Action: rbac.ActionCreate, Handler: d.requireAuth(d.handleSyncBatch)},
		{Method: http.MethodGet, Pattern: "/api/v1/groups/{groupID}/sync/batches/{batchID}", Module: rbac.ModulePayments, Action: rbac.ActionView, Handler: d.requireAuth(d.handleSyncBatchStatus)},
	}
}
