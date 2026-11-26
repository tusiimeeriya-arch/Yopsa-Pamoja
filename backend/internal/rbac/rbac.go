// Package rbac implements the Pamoja authorisation model: scoped role grants
// with default deny, a separation-of-duties engine, and a machine-checked
// matrix (matrix.yaml) from which docs/RBAC.md is generated.
//
// Every handler must call Authorize before touching data, and must call
// CheckSeparation with the identities involved in the action. The route
// registry in internal/httpapi makes the first requirement testable.
package rbac

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed matrix.yaml
var matrixYAML []byte

// Role is a named set of permissions granted to a user within a scope.
type Role string

// Module is a functional area of the system.
type Module string

// Action is an operation a role may perform on a module.
type Action string

// ScopeType is the widest scope at which a grant applies.
type ScopeType string

// ContextField names an identity involved in an action, used by the
// separation-of-duties engine.
type ContextField string

// Scope types, widest first.
const (
	ScopePlatform    ScopeType = "platform"
	ScopeLegalEntity ScopeType = "legal_entity"
	ScopeBranch      ScopeType = "branch"
	ScopeGroup       ScopeType = "group"
	ScopeSelf        ScopeType = "self"
)

// Roles.
const (
	RoleMember         Role = "member"
	RoleSecretary      Role = "secretary"
	RoleTreasurer      Role = "treasurer"
	RoleChairperson    Role = "chairperson"
	RoleFieldOfficer   Role = "field_officer"
	RoleVerifier       Role = "verifier"
	RoleCreditApprover Role = "credit_approver"
	RoleManagement     Role = "management"
	RoleAuditor        Role = "auditor"
	RoleITAdmin        Role = "it_admin"
)

// Modules.
const (
	ModuleAuth           Module = "auth"
	ModuleRegistration   Module = "registration"
	ModuleMeetings       Module = "meetings"
	ModulePayments       Module = "payments"
	ModuleReconciliation Module = "reconciliation"
	ModuleLoans          Module = "loans"
	ModuleDashboard      Module = "dashboard"
	ModuleRanking        Module = "ranking"
	ModuleLearning       Module = "learning"
	ModuleComments       Module = "comments"
	ModuleDisputes       Module = "disputes"
	ModuleTasks          Module = "tasks"
	ModuleNotifications  Module = "notifications"
	ModuleReports        Module = "reports"
	ModuleAudit          Module = "audit"
	ModuleConfig         Module = "config"
	ModuleUsersRoles     Module = "users_roles"
	ModuleIntegrations   Module = "integrations"
)

// Actions.
const (
	ActionView             Action = "view"
	ActionCreate           Action = "create"
	ActionEditDraft        Action = "edit_draft"
	ActionCheckIn          Action = "check_in"
	ActionSubmit           Action = "submit"
	ActionLock             Action = "lock"
	ActionVerify           Action = "verify"
	ActionReturn           Action = "return"
	ActionApprove          Action = "approve"
	ActionDecline          Action = "decline"
	ActionDisburse         Action = "disburse"
	ActionPost             Action = "post"
	ActionCorrect          Action = "correct"
	ActionAmend            Action = "amend"
	ActionResolve          Action = "resolve"
	ActionModerate         Action = "moderate"
	ActionExport           Action = "export"
	ActionConfigure        Action = "configure"
	ActionGrantRole        Action = "grant_role"
	ActionRevokeRole       Action = "revoke_role"
	ActionReviewActivate   Action = "review_activate"
	ActionAppraise         Action = "appraise"
	ActionReviewOperations Action = "review_operations"
	ActionAcceptOffer      Action = "accept_offer"
	ActionClose            Action = "close"
	ActionRevoke           Action = "revoke"
	ActionStepUp           Action = "step_up"
)

// Context fields for separation-of-duties checks.
const (
	CtxRecorder       ContextField = "recorder"
	CtxSubmitter      ContextField = "submitter"
	CtxApprover       ContextField = "approver"
	CtxApplicant      ContextField = "applicant"
	CtxCreator        ContextField = "creator"
	CtxRequester      ContextField = "requester"
	CtxAppraiser      ContextField = "appraiser"
	CtxReviewer       ContextField = "reviewer"
	CtxSecondApprover ContextField = "second_approver"
)

// financialActions are the actions that move or authorise money. A role whose
// spec says may_handle_finance: false can never be granted one of them.
var financialActions = map[Action]bool{
	ActionVerify:           true,
	ActionApprove:          true,
	ActionDecline:          true,
	ActionDisburse:         true,
	ActionPost:             true,
	ActionCorrect:          true,
	ActionReviewOperations: true,
}

var scopeRank = map[ScopeType]int{
	ScopeSelf:        0,
	ScopeGroup:       1,
	ScopeBranch:      2,
	ScopeLegalEntity: 3,
	ScopePlatform:    4,
}

// roleOrder is the presentation order for documentation.
var roleOrder = []Role{
	RoleMember, RoleSecretary, RoleTreasurer, RoleChairperson, RoleFieldOfficer,
	RoleVerifier, RoleCreditApprover, RoleManagement, RoleAuditor, RoleITAdmin,
}

// RoleSpec describes a role.
type RoleSpec struct {
	Description      string `yaml:"description"`
	MayHandleFinance bool   `yaml:"may_handle_finance"`
}

// ModuleSpec describes a module.
type ModuleSpec struct {
	Description string `yaml:"description"`
}

// Grant is one role's permission on one module at one scope.
type Grant struct {
	Role    Role      `yaml:"role"`
	Module  Module    `yaml:"module"`
	Actions []Action  `yaml:"actions"`
	Scope   ScopeType `yaml:"scope"`
}

// SoDRule is one separation-of-duties constraint.
type SoDRule struct {
	ID                   string         `yaml:"id"`
	Module               Module         `yaml:"module"`
	Action               Action         `yaml:"action"`
	Description          string         `yaml:"description"`
	MustDifferFrom       []ContextField `yaml:"must_differ_from"`
	RequiresSecondPerson bool           `yaml:"requires_second_person"`
}

// Matrix is the complete authorisation policy.
type Matrix struct {
	Version    int                   `yaml:"version"`
	ScopeTypes []ScopeType           `yaml:"scope_types"`
	Actions    []Action              `yaml:"actions"`
	Roles      map[Role]RoleSpec     `yaml:"roles"`
	Modules    map[Module]ModuleSpec `yaml:"modules"`
	Grants     []Grant               `yaml:"grants"`
	SoD        []SoDRule             `yaml:"sod"`
}

// Load parses and validates the embedded matrix.
func Load() (*Matrix, error) {
	var m Matrix
	if err := yaml.Unmarshal(matrixYAML, &m); err != nil {
		return nil, fmt.Errorf("rbac: parse matrix: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate proves the matrix is internally consistent. It is run on load and by
// tests, so an incoherent policy cannot ship.
func (m *Matrix) Validate() error {
	if m.Version < 1 {
		return fmt.Errorf("rbac: matrix version must be >= 1")
	}
	scopeOK := map[ScopeType]bool{}
	for _, s := range m.ScopeTypes {
		if _, ok := scopeRank[s]; !ok {
			return fmt.Errorf("rbac: unknown scope type %q", s)
		}
		scopeOK[s] = true
	}
	for _, required := range []ScopeType{ScopePlatform, ScopeLegalEntity, ScopeBranch, ScopeGroup, ScopeSelf} {
		if !scopeOK[required] {
			return fmt.Errorf("rbac: scope_types must include %q", required)
		}
	}

	actionOK := map[Action]bool{}
	for _, a := range m.Actions {
		actionOK[a] = true
	}
	if len(actionOK) == 0 {
		return fmt.Errorf("rbac: no actions declared")
	}
	if len(m.Roles) == 0 {
		return fmt.Errorf("rbac: no roles declared")
	}
	if len(m.Modules) == 0 {
		return fmt.Errorf("rbac: no modules declared")
	}

	seen := map[string]bool{}
	viewed := map[Module]bool{}
	for i, g := range m.Grants {
		spec, ok := m.Roles[g.Role]
		if !ok {
			return fmt.Errorf("rbac: grant %d references unknown role %q", i, g.Role)
		}
		if _, ok := m.Modules[g.Module]; !ok {
			return fmt.Errorf("rbac: grant %d references unknown module %q", i, g.Module)
		}
		if !scopeOK[g.Scope] {
			return fmt.Errorf("rbac: grant %d (%s/%s) uses unknown scope %q", i, g.Role, g.Module, g.Scope)
		}
		if len(g.Actions) == 0 {
			return fmt.Errorf("rbac: grant %d (%s/%s) has no actions", i, g.Role, g.Module)
		}
		for _, a := range g.Actions {
			if !actionOK[a] {
				return fmt.Errorf("rbac: grant %d (%s/%s) uses unknown action %q", i, g.Role, g.Module, a)
			}
			key := string(g.Role) + "|" + string(g.Module) + "|" + string(a)
			if seen[key] {
				return fmt.Errorf("rbac: duplicate grant %s/%s/%s", g.Role, g.Module, a)
			}
			seen[key] = true
			if a == ActionView {
				viewed[g.Module] = true
			}
			if financialActions[a] && !spec.MayHandleFinance {
				return fmt.Errorf("rbac: role %q may not handle finance but is granted %q on %q", g.Role, a, g.Module)
			}
		}
		if g.Role == RoleMember && g.Scope != ScopeSelf {
			return fmt.Errorf("rbac: member grants must be self-scoped, found %q on %q", g.Scope, g.Module)
		}
	}

	for mod := range m.Modules {
		if !viewed[mod] {
			return fmt.Errorf("rbac: module %q has no role that can view it", mod)
		}
	}

	for _, rule := range m.SoD {
		if !actionOK[rule.Action] {
			return fmt.Errorf("rbac: SoD %s uses unknown action %q", rule.ID, rule.Action)
		}
		if _, ok := m.Modules[rule.Module]; !ok {
			return fmt.Errorf("rbac: SoD %s uses unknown module %q", rule.ID, rule.Module)
		}
		if len(rule.MustDifferFrom) == 0 && !rule.RequiresSecondPerson {
			return fmt.Errorf("rbac: SoD %s constrains nothing", rule.ID)
		}
	}
	return nil
}

// Binding is one role a user holds at one scope. ScopeIDs lists the entity,
// branch or group ids the binding covers; it is empty for the self scope.
type Binding struct {
	Role     Role
	Scope    ScopeType
	ScopeIDs []string
}

// Actor is the authenticated principal performing an action.
type Actor struct {
	UserID   string
	Bindings []Binding
}

// ScopeRef is one level of a resource's scope path, outermost first.
type ScopeRef struct {
	Type ScopeType
	ID   string
}

// Resource is the thing being acted on.
type Resource struct {
	Module      Module
	ScopePath   []ScopeRef
	OwnerUserID string // set when the resource belongs to a member
}

// Decision is the outcome of an authorisation check. Reason is written to the
// audit log and returned to the caller, so it must never contain data the
// caller may not see.
type Decision struct {
	Allowed bool
	Reason  string
	Role    Role
}

// Authorize answers whether the actor may perform action on module for the
// resource. Default deny: anything not granted is refused.
func (m *Matrix) Authorize(actor Actor, mod Module, action Action, res Resource) Decision {
	if actor.UserID == "" {
		return Decision{Reason: "denied: unauthenticated actor"}
	}
	for _, b := range actor.Bindings {
		for _, g := range m.Grants {
			if g.Role != b.Role || g.Module != mod || !containsAction(g.Actions, action) {
				continue
			}
			if g.Scope == ScopeSelf {
				if res.OwnerUserID != "" && res.OwnerUserID == actor.UserID {
					return Decision{Allowed: true, Role: b.Role,
						Reason: fmt.Sprintf("allowed: %s may %s own %s records", b.Role, action, mod)}
				}
				continue
			}
			if !bindingCovers(b, g.Scope, res.ScopePath) {
				continue
			}
			return Decision{Allowed: true, Role: b.Role,
				Reason: fmt.Sprintf("allowed: %s may %s %s at %s scope", b.Role, action, mod, g.Scope)}
		}
	}
	return Decision{Reason: fmt.Sprintf("denied: no grant for %s on %s at this scope", action, mod)}
}

// bindingCovers reports whether the binding reaches the granted scope and the
// resource path contains the binding's scope id.
func bindingCovers(b Binding, grantScope ScopeType, path []ScopeRef) bool {
	if scopeRank[b.Scope] < scopeRank[grantScope] {
		return false
	}
	if b.Scope == ScopePlatform {
		return true
	}
	if b.Scope == ScopeSelf {
		return false
	}
	for _, ref := range path {
		if ref.Type != b.Scope {
			continue
		}
		for _, id := range b.ScopeIDs {
			if id == ref.ID {
				return true
			}
		}
	}
	return false
}

// CheckSeparation enforces every separation-of-duties rule that applies to the
// action. ctx maps each involved identity field to the user ids that filled it
// (a submission has several recorders, a loan one approver, and so on).
func (m *Matrix) CheckSeparation(actorID string, mod Module, action Action, ctx map[ContextField][]string) error {
	for _, rule := range m.SoD {
		if rule.Module != mod || rule.Action != action {
			continue
		}
		for _, field := range rule.MustDifferFrom {
			for _, id := range ctx[field] {
				if id != "" && id == actorID {
					return fmt.Errorf("%s: %s", rule.ID, rule.Description)
				}
			}
		}
		if rule.RequiresSecondPerson {
			second := ctx[CtxSecondApprover]
			if len(second) == 0 || second[0] == "" {
				return fmt.Errorf("%s: %s", rule.ID, rule.Description)
			}
			if second[0] == actorID {
				return fmt.Errorf("%s: %s", rule.ID, rule.Description)
			}
		}
	}
	return nil
}

// HasGrant reports whether any role may perform the action on the module. The
// route-coverage test uses it to prove no endpoint is unreachable by policy.
func (m *Matrix) HasGrant(mod Module, action Action) bool {
	for _, g := range m.Grants {
		if g.Module == mod && containsAction(g.Actions, action) {
			return true
		}
	}
	return false
}

// GrantsFor returns the grants held by a role, in matrix order.
func (m *Matrix) GrantsFor(role Role) []Grant {
	var out []Grant
	for _, g := range m.Grants {
		if g.Role == role {
			out = append(out, g)
		}
	}
	return out
}

// GenerateMarkdown renders docs/RBAC.md. Output is deterministic so a test can
// compare it with the committed file and fail on drift.
func (m *Matrix) GenerateMarkdown() string {
	var b strings.Builder

	b.WriteString("# RBAC matrix\n\n")
	b.WriteString("> Generated from `backend/internal/rbac/matrix.yaml` by `make rbac-doc`.\n")
	b.WriteString("> Do not edit this file by hand; edit the matrix and regenerate.\n")
	b.WriteString("> A test fails when this document and the matrix drift apart.\n\n")
	fmt.Fprintf(&b, "Matrix version: **%d**\n\n", m.Version)

	b.WriteString("## Model\n\n")
	b.WriteString("A user holds one or more **role bindings**, each scoped to a legal entity, branch or group, ")
	b.WriteString("with `effective_from` / `effective_to` dates and an approving second person. ")
	b.WriteString("Roles are never global: a grant only applies inside the scopes the binding covers.\n\n")
	b.WriteString("Authorisation is **default deny**. `Authorize(actor, module, action, resource)` must be called by ")
	b.WriteString("every handler before data is read or written, and `CheckSeparation` must be called with the ")
	b.WriteString("identities involved in the action. PostgreSQL row-level security then restricts the rows the ")
	b.WriteString("transaction can see, using the same actor and scope set.\n\n")

	b.WriteString("### Scopes (widest first)\n\n")
	b.WriteString("| Scope | Meaning |\n|---|---|\n")
	b.WriteString("| `platform` | The whole installation. Reserved for operational tooling; no business role is granted it. |\n")
	b.WriteString("| `legal_entity` | One Yopsa legal entity. |\n")
	b.WriteString("| `branch` | One branch of a legal entity. |\n")
	b.WriteString("| `group` | One group (VSLA, club, association, cooperative, SACCO). |\n")
	b.WriteString("| `self` | The member's own records. |\n\n")

	b.WriteString("## Roles\n\n")
	b.WriteString("| Role | Description | May handle finance |\n|---|---|---|\n")
	for _, role := range m.orderedRoles() {
		spec := m.Roles[role]
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", role, spec.Description, yesNo(spec.MayHandleFinance))
	}
	b.WriteString("\n")

	b.WriteString("## Action vocabulary\n\n")
	b.WriteString("| Action | Meaning |\n|---|---|\n")
	for _, a := range m.Actions {
		fmt.Fprintf(&b, "| `%s` | %s |\n", a, actionMeaning(a))
	}
	b.WriteString("\n")

	b.WriteString("## Coverage summary\n\n")
	b.WriteString("✓ = the role has at least one grant on the module. Actions and scope are in the detail below.\n\n")
	modules := m.sortedModules()
	b.WriteString("| Role |")
	for _, mod := range modules {
		b.WriteString(" " + string(mod) + " |")
	}
	b.WriteString("\n|---|")
	for range modules {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, role := range m.orderedRoles() {
		b.WriteString("| `" + string(role) + "` |")
		for _, mod := range modules {
			if m.roleHasModule(role, mod) {
				b.WriteString(" ✓ |")
			} else {
				b.WriteString(" — |")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("## Grants by role\n\n")
	for _, role := range m.orderedRoles() {
		grants := m.GrantsFor(role)
		fmt.Fprintf(&b, "### `%s`\n\n", role)
		if len(grants) == 0 {
			b.WriteString("No grants. This role can do nothing.\n\n")
			continue
		}
		b.WriteString("| Module | Actions | Scope |\n|---|---|---|\n")
		for _, g := range grants {
			fmt.Fprintf(&b, "| `%s` | %s | `%s` |\n", g.Module, actionList(g.Actions), g.Scope)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Separation of duties\n\n")
	b.WriteString("Each rule is enforced in the service layer and, where the data model allows it, by a PostgreSQL ")
	b.WriteString("trigger, so a direct database edit cannot bypass it. A violation is a 403 with the rule id recorded ")
	b.WriteString("in the audit log.\n\n")
	b.WriteString("| Rule | Module / action | Constraint |\n|---|---|---|\n")
	for _, rule := range m.SoD {
		constraint := rule.Description
		if rule.RequiresSecondPerson {
			constraint += " A second person must approve."
		}
		fmt.Fprintf(&b, "| `%s` | `%s.%s` | %s |\n", rule.ID, rule.Module, rule.Action, constraint)
	}
	b.WriteString("\n")

	b.WriteString("## How this is proven\n\n")
	b.WriteString("1. `TestEmbeddedMatrixIsValid` — the policy parses and is internally consistent.\n")
	b.WriteString("2. `TestAuthorize` — allowed and denied cases for every role family, including scope containment.\n")
	b.WriteString("3. `TestSeparationOfDuties` — each rule blocks the actor it names.\n")
	b.WriteString("4. `TestDocMatchesMatrix` — this document matches the matrix.\n")
	b.WriteString("5. Route coverage — every non-public HTTP route declares a module and action, and at least one ")
	b.WriteString("role can perform it.\n")

	return b.String()
}

func (m *Matrix) orderedRoles() []Role {
	seen := map[Role]bool{}
	var out []Role
	for _, r := range roleOrder {
		if _, ok := m.Roles[r]; ok {
			out = append(out, r)
			seen[r] = true
		}
	}
	var rest []Role
	for r := range m.Roles {
		if !seen[r] {
			rest = append(rest, r)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i] < rest[j] })
	return append(out, rest...)
}

func (m *Matrix) sortedModules() []Module {
	out := make([]Module, 0, len(m.Modules))
	for mod := range m.Modules {
		out = append(out, mod)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (m *Matrix) roleHasModule(role Role, mod Module) bool {
	for _, g := range m.Grants {
		if g.Role == role && g.Module == mod {
			return true
		}
	}
	return false
}

func containsAction(list []Action, want Action) bool {
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}

func actionList(list []Action) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = "`" + string(a) + "`"
	}
	return strings.Join(parts, ", ")
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func actionMeaning(a Action) string {
	switch a {
	case ActionView:
		return "Read records within scope."
	case ActionCreate:
		return "Create a draft record."
	case ActionEditDraft:
		return "Edit a record while it is still a draft."
	case ActionCheckIn:
		return "Record a member's attendance at an open register, self or assisted."
	case ActionSubmit:
		return "Submit a record for review; it becomes immutable except by correction."
	case ActionLock:
		return "Lock a group submission so its entries can no longer change."
	case ActionVerify:
		return "Independently verify a submission or receipt."
	case ActionReturn:
		return "Return a submission for correction, with a reason."
	case ActionApprove:
		return "Approve a borrowing application within the approver's authority."
	case ActionDecline:
		return "Decline a borrowing application with a reason."
	case ActionDisburse:
		return "Confirm that a loan was actually disbursed."
	case ActionPost:
		return "Confirm posting to the core ledger."
	case ActionCorrect:
		return "Authorise a correction, reversal or adjustment."
	case ActionAmend:
		return "Amend a closed register or minutes through the controlled path."
	case ActionResolve:
		return "Resolve a dispute with a retained resolution record."
	case ActionModerate:
		return "Moderate a comment, retaining the reason and moderator."
	case ActionExport:
		return "Export data or a report; the export is audited."
	case ActionConfigure:
		return "Change a versioned policy value."
	case ActionGrantRole:
		return "Grant a role at a scope, subject to a second administrator's approval."
	case ActionRevokeRole:
		return "Revoke a role at a scope, subject to a second administrator's approval."
	case ActionReviewActivate:
		return "Review a submitted group and activate it."
	case ActionAppraise:
		return "Record an officer appraisal for a borrowing application."
	case ActionReviewOperations:
		return "Perform the mandatory operations review of a borrowing application."
	case ActionAcceptOffer:
		return "Accept a loan offer on behalf of the group."
	case ActionClose:
		return "Close a meeting, task or group through its controlled transition."
	}
	return ""
}
