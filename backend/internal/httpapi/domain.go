package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yopsa/pamoja/backend/internal/auth"
	"github.com/yopsa/pamoja/backend/internal/domain"
)

func (d Deps) principalOf(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return auth.Principal{}, false
	}
	return p, true
}

func (d Deps) service(w http.ResponseWriter, r *http.Request) (*domain.Service, bool) {
	if d.Domain == nil {
		writeError(w, r, http.StatusServiceUnavailable, "domain_unavailable")
		return nil, false
	}
	return d.Domain, true
}

func writeMessage(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"error":      code,
		"message":    message,
		"request_id": RequestIDFrom(r.Context()),
	})
}

func (d Deps) domainError(w http.ResponseWriter, r *http.Request, err error) {
	message := strings.TrimPrefix(err.Error(), "domain: ")
	switch {
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found")
	case errors.Is(err, domain.ErrConflict):
		writeMessage(w, r, http.StatusConflict, "conflict", message)
	case errors.Is(err, domain.ErrInvalid):
		writeMessage(w, r, http.StatusBadRequest, "invalid_request", message)
	default:
		d.logger().Error("domain request failed",
			"error", err.Error(),
			"request_id", RequestIDFrom(r.Context()))
		writeError(w, r, http.StatusInternalServerError, "internal_error")
	}
}

func (d Deps) handleMe(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	me, err := svc.Me(r.Context(), p, RequestIDFrom(r.Context()))
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, me)
}

func (d Deps) handleDashboard(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	body, err := svc.Dashboard(r.Context(), p, RequestIDFrom(r.Context()))
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (d Deps) handleGroups(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	groups, err := svc.Groups(r.Context(), p, RequestIDFrom(r.Context()))
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (d Deps) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.CreateGroupInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	group, err := svc.CreateGroup(r.Context(), p, RequestIDFrom(r.Context()), in)
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, group)
}

func (d Deps) handleGroup(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	detail, err := svc.Group(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "groupID"))
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (d Deps) handleActivateGroup(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	if err := svc.ActivateGroup(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "groupID")); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
}

func (d Deps) handleCreateMember(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.CreateMemberInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	member, err := svc.AddMember(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "groupID"), in)
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, member)
}

func (d Deps) handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.UpdateMemberInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	err := svc.UpdateMember(r.Context(), p, RequestIDFrom(r.Context()),
		chi.URLParam(r, "groupID"), chi.URLParam(r, "memberID"), in)
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (d Deps) handleCreateMeeting(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.CreateMeetingInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	meeting, err := svc.CreateMeeting(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "groupID"), in)
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, meeting)
}

func (d Deps) handleSetAttendance(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.AttendanceInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := svc.SetAttendance(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "meetingID"), in); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (d Deps) handleCloseMeeting(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.CloseMeetingInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := svc.CloseMeeting(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "meetingID"), in); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

func (d Deps) handleCreateContribution(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.CreateContributionInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	entry, err := svc.CreateContribution(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "groupID"), in)
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (d Deps) handleVerifyContribution(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	if err := svc.VerifyContribution(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "contributionID")); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "verified"})
}

func (d Deps) handleReturnContribution(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.ReturnContributionInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := svc.ReturnContribution(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "contributionID"), in); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "returned"})
}

func (d Deps) handleLoans(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	loans, err := svc.Loans(r.Context(), p, RequestIDFrom(r.Context()))
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"loans": loans})
}

func (d Deps) handleCreateLoan(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.CreateLoanInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	loan, err := svc.CreateLoan(r.Context(), p, RequestIDFrom(r.Context()), in)
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, loan)
}

func (d Deps) handleAppraiseLoan(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.NoteInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := svc.AppraiseLoan(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "loanID"), in); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "appraised"})
}

func (d Deps) handleApproveLoan(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.NoteInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := svc.ApproveLoan(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "loanID"), in); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (d Deps) handleDeclineLoan(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.NoteInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := svc.DeclineLoan(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "loanID"), in); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "declined"})
}

func (d Deps) handleDisburseLoan(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	if err := svc.DisburseLoan(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "loanID")); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disbursed"})
}

func (d Deps) handleTasks(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	tasks, err := svc.Tasks(r.Context(), p, RequestIDFrom(r.Context()))
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (d Deps) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	var in domain.CreateTaskInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request")
		return
	}
	task, err := svc.CreateTask(r.Context(), p, RequestIDFrom(r.Context()), in)
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (d Deps) handleCloseTask(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	if err := svc.CloseTask(r.Context(), p, RequestIDFrom(r.Context()), chi.URLParam(r, "taskID")); err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "done"})
}

func (d Deps) handleAudit(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	page, err := svc.Audit(r.Context(), p, RequestIDFrom(r.Context()), limit, offset)
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (d Deps) handleUsers(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	users, err := svc.Users(r.Context(), p, RequestIDFrom(r.Context()))
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (d Deps) handlePortfolioCSV(w http.ResponseWriter, r *http.Request) {
	p, ok := d.principalOf(w, r)
	if !ok {
		return
	}
	svc, ok := d.service(w, r)
	if !ok {
		return
	}
	body, err := svc.PortfolioCSV(r.Context(), p, RequestIDFrom(r.Context()))
	if err != nil {
		d.domainError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="yopsa-portfolio-`+time.Now().Format("2006-01-02")+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
