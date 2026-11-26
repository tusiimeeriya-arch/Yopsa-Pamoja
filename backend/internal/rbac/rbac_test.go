package rbac

import (
	"os"
	"path/filepath"
	"testing"
)

func mustLoad(t *testing.T) *Matrix {
	t.Helper()
	m, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func TestEmbeddedMatrixIsValid(t *testing.T) {
	m := mustLoad(t)
	if m.Version != 1 {
		t.Errorf("version = %d, want 1", m.Version)
	}
	if len(m.Roles) != 10 {
		t.Errorf("roles = %d, want 10", len(m.Roles))
	}
	if len(m.SoD) < 10 {
		t.Errorf("separation rules = %d, want at least 10", len(m.SoD))
	}
}

func TestNoFinancialRightsForNonFinancialRoles(t *testing.T) {
	m := mustLoad(t)
	for _, role := range []Role{RoleITAdmin, RoleAuditor, RoleManagement, RoleFieldOfficer, RoleChairperson, RoleSecretary, RoleMember} {
		for _, g := range m.GrantsFor(role) {
			for _, a := range g.Actions {
				if financialActions[a] {
					t.Errorf("role %s must not be granted financial action %s on %s", role, a, g.Module)
				}
			}
		}
	}
}

func TestAuditorIsReadOnly(t *testing.T) {
	m := mustLoad(t)
	for _, g := range m.GrantsFor(RoleAuditor) {
		// Every role manages its own sessions and step-up; that is self-service
		// authentication, not a business action on a record.
		if g.Module == ModuleAuth {
			continue
		}
		for _, a := range g.Actions {
			if a != ActionView && a != ActionExport {
				t.Errorf("auditor granted non-read action %s on %s", a, g.Module)
			}
		}
	}
}

func TestAuthorize(t *testing.T) {
	m := mustLoad(t)

	groupPath := []ScopeRef{
		{Type: ScopeLegalEntity, ID: "e1"},
		{Type: ScopeBranch, ID: "b1"},
		{Type: ScopeGroup, ID: "g1"},
	}

	treasurer := Actor{UserID: "u-treasurer", Bindings: []Binding{
		{Role: RoleTreasurer, Scope: ScopeGroup, ScopeIDs: []string{"g1"}},
	}}
	verifier := Actor{UserID: "u-verifier", Bindings: []Binding{
		{Role: RoleVerifier, Scope: ScopeBranch, ScopeIDs: []string{"b1"}},
	}}
	officerElsewhere := Actor{UserID: "u-officer", Bindings: []Binding{
		{Role: RoleFieldOfficer, Scope: ScopeBranch, ScopeIDs: []string{"b2"}},
	}}
	member := Actor{UserID: "u-member", Bindings: []Binding{
		{Role: RoleMember, Scope: ScopeSelf},
	}}
	itAdmin := Actor{UserID: "u-it", Bindings: []Binding{
		{Role: RoleITAdmin, Scope: ScopeLegalEntity, ScopeIDs: []string{"e1"}},
	}}
	auditor := Actor{UserID: "u-auditor", Bindings: []Binding{
		{Role: RoleAuditor, Scope: ScopeLegalEntity, ScopeIDs: []string{"e1"}},
	}}
	management := Actor{UserID: "u-management", Bindings: []Binding{
		{Role: RoleManagement, Scope: ScopeLegalEntity, ScopeIDs: []string{"e1"}},
	}}

	groupRes := Resource{Module: ModulePayments, ScopePath: groupPath}

	cases := []struct {
		name   string
		actor  Actor
		mod    Module
		action Action
		res    Resource
		want   bool
	}{
		{"treasurer records entry in own group", treasurer, ModulePayments, ActionCreate, groupRes, true},
		{"treasurer locks submission", treasurer, ModulePayments, ActionLock, groupRes, true},
		{"treasurer cannot verify own submission", treasurer, ModulePayments, ActionVerify, groupRes, false},
		{"verifier verifies at branch", verifier, ModulePayments, ActionVerify, groupRes, true},
		{"verifier cannot record entries", verifier, ModulePayments, ActionCreate, groupRes, false},
		{"verifier confirms disbursement", verifier, ModuleLoans, ActionDisburse, groupRes, true},
		{"verifier cannot approve loan", verifier, ModuleLoans, ActionApprove, groupRes, false},
		{"officer of another branch denied", officerElsewhere, ModulePayments, ActionView, groupRes, false},
		{"member views own payment", member, ModulePayments, ActionView,
			Resource{Module: ModulePayments, ScopePath: groupPath, OwnerUserID: "u-member"}, true},
		{"member cannot view another member", member, ModulePayments, ActionView,
			Resource{Module: ModulePayments, ScopePath: groupPath, OwnerUserID: "u-other"}, false},
		{"member cannot create payment entry", member, ModulePayments, ActionCreate,
			Resource{Module: ModulePayments, ScopePath: groupPath, OwnerUserID: "u-member"}, false},
		{"IT admin cannot verify", itAdmin, ModulePayments, ActionVerify, groupRes, false},
		{"IT admin cannot approve", itAdmin, ModuleLoans, ActionApprove, groupRes, false},
		{"IT admin can grant a role", itAdmin, ModuleUsersRoles, ActionGrantRole,
			Resource{Module: ModuleUsersRoles, ScopePath: []ScopeRef{{Type: ScopeLegalEntity, ID: "e1"}}}, true},
		{"auditor can view payments", auditor, ModulePayments, ActionView, groupRes, true},
		{"auditor cannot correct", auditor, ModulePayments, ActionCorrect, groupRes, false},
		{"management reads dashboard", management, ModuleDashboard, ActionView, groupRes, true},
		{"management cannot verify", management, ModulePayments, ActionVerify, groupRes, false},
		{"management configures ranking", management, ModuleRanking, ActionConfigure,
			Resource{Module: ModuleRanking, ScopePath: []ScopeRef{{Type: ScopeLegalEntity, ID: "e1"}}}, true},
		{"unauthenticated denied", Actor{}, ModulePayments, ActionView, groupRes, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := m.Authorize(tc.actor, tc.mod, tc.action, tc.res)
			if got.Allowed != tc.want {
				t.Fatalf("Allowed = %v, want %v (reason: %s)", got.Allowed, tc.want, got.Reason)
			}
			if got.Reason == "" {
				t.Fatal("decision must carry a reason for the audit log")
			}
		})
	}
}

func TestBindingNarrowerThanGrantIsDenied(t *testing.T) {
	m := mustLoad(t)
	// A verifier bound to one group cannot act on a branch-wide grant: the
	// policy grant is branch scope, the binding is only group scope.
	actor := Actor{UserID: "u", Bindings: []Binding{
		{Role: RoleVerifier, Scope: ScopeGroup, ScopeIDs: []string{"g1"}},
	}}
	res := Resource{Module: ModulePayments, ScopePath: []ScopeRef{
		{Type: ScopeLegalEntity, ID: "e1"},
		{Type: ScopeBranch, ID: "b1"},
		{Type: ScopeGroup, ID: "g1"},
	}}
	if d := m.Authorize(actor, ModulePayments, ActionVerify, res); d.Allowed {
		t.Fatalf("expected denial for group-scoped verifier, got %s", d.Reason)
	}
}

func TestSeparationOfDuties(t *testing.T) {
	m := mustLoad(t)

	cases := []struct {
		name    string
		actor   string
		mod     Module
		action  Action
		ctx     map[ContextField][]string
		wantErr bool
	}{
		{
			name: "verifier recorded an entry", actor: "u1", mod: ModulePayments, action: ActionVerify,
			ctx:     map[ContextField][]string{CtxRecorder: {"u1", "u2"}},
			wantErr: true,
		},
		{
			name: "verifier is independent", actor: "u9", mod: ModulePayments, action: ActionVerify,
			ctx: map[ContextField][]string{CtxRecorder: {"u1", "u2"}},
		},
		{
			name: "approver is the applicant", actor: "u1", mod: ModuleLoans, action: ActionApprove,
			ctx:     map[ContextField][]string{CtxApplicant: {"u1"}, CtxAppraiser: {"u3"}},
			wantErr: true,
		},
		{
			name: "approver appraised the loan", actor: "u3", mod: ModuleLoans, action: ActionApprove,
			ctx:     map[ContextField][]string{CtxApplicant: {"u1"}, CtxAppraiser: {"u3"}},
			wantErr: true,
		},
		{
			name: "disburser is the approver", actor: "u4", mod: ModuleLoans, action: ActionDisburse,
			ctx:     map[ContextField][]string{CtxApprover: {"u4"}},
			wantErr: true,
		},
		{
			name: "role grant without a second person", actor: "admin1", mod: ModuleUsersRoles, action: ActionGrantRole,
			ctx:     map[ContextField][]string{},
			wantErr: true,
		},
		{
			name: "role grant self-approved", actor: "admin1", mod: ModuleUsersRoles, action: ActionGrantRole,
			ctx:     map[ContextField][]string{CtxSecondApprover: {"admin1"}},
			wantErr: true,
		},
		{
			name: "role grant with second admin", actor: "admin1", mod: ModuleUsersRoles, action: ActionGrantRole,
			ctx: map[ContextField][]string{CtxSecondApprover: {"admin2"}},
		},
		{
			name: "register amended and self-reviewed", actor: "sec1", mod: ModuleMeetings, action: ActionAmend,
			ctx:     map[ContextField][]string{CtxReviewer: {"sec1"}},
			wantErr: true,
		},
		{
			name: "correction approved by its requester", actor: "u5", mod: ModulePayments, action: ActionCorrect,
			ctx:     map[ContextField][]string{CtxRequester: {"u5"}, CtxRecorder: {"u1"}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := m.CheckSeparation(tc.actor, tc.mod, tc.action, tc.ctx)
			if tc.wantErr && err == nil {
				t.Fatal("expected separation-of-duties violation")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected violation: %v", err)
			}
		})
	}
}

func TestDocMatchesMatrix(t *testing.T) {
	m := mustLoad(t)
	want := m.GenerateMarkdown()

	path := filepath.Join("..", "..", "..", "docs", "RBAC.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `make rbac-doc`)", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s is out of date with matrix.yaml; run `make rbac-doc`", path)
	}
}
