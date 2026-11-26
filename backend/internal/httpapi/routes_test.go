package httpapi

import (
	"testing"

	"github.com/yopsa/pamoja/backend/internal/rbac"
)

// TestEveryRouteDeclaresAuthorization is the guard that keeps authorisation
// from being forgotten as routes are added: a non-public route without a
// module and action, or whose action no role can perform, fails the build.
func TestEveryRouteDeclaresAuthorization(t *testing.T) {
	matrix, err := rbac.Load()
	if err != nil {
		t.Fatalf("rbac.Load: %v", err)
	}

	seen := map[string]bool{}
	for _, route := range Routes(Deps{}) {
		key := route.Method + " " + route.Pattern
		if seen[key] {
			t.Fatalf("duplicate route %s", key)
		}
		seen[key] = true

		if route.Handler == nil {
			t.Fatalf("route %s has no handler", key)
		}
		if route.Public {
			continue
		}
		if route.Module == "" || route.Action == "" {
			t.Fatalf("route %s is not public and must declare a module and action", key)
		}
		if !matrix.HasGrant(route.Module, route.Action) {
			t.Fatalf("route %s requires %s.%s but no role is granted it", key, route.Module, route.Action)
		}
	}
}

func TestPublicRoutesAreOnlyPlatformEndpoints(t *testing.T) {
	allowed := map[string]bool{
		"GET /":                     true,
		"GET /healthz":              true,
		"GET /readyz":               true,
		"GET /api/v1/meta":          true,
		"GET /api/v1/group-types":   true,
		"POST /api/v1/auth/login":   true,
		"POST /api/v1/auth/refresh": true,
	}
	for _, route := range Routes(Deps{}) {
		key := route.Method + " " + route.Pattern
		if route.Public && !allowed[key] {
			t.Errorf("route %s is public but not in the reviewed public allow-list", key)
		}
	}
}

// TestRouteRoleScopeMatrix is the generated route × role × scope suite
// (T-RBAC-05): for every route, every role and every binding scope, the
// authorisation engine must allow exactly the combinations the matrix grants.
func TestRouteRoleScopeMatrix(t *testing.T) {
	matrix, err := rbac.Load()
	if err != nil {
		t.Fatalf("rbac.Load: %v", err)
	}
	ranks := map[rbac.ScopeType]int{
		rbac.ScopeSelf: 0, rbac.ScopeGroup: 1, rbac.ScopeBranch: 2,
		rbac.ScopeLegalEntity: 3, rbac.ScopePlatform: 4,
	}
	ids := map[rbac.ScopeType][]string{
		rbac.ScopeSelf: nil, rbac.ScopePlatform: nil,
		rbac.ScopeLegalEntity: {"entity"}, rbac.ScopeBranch: {"branch"},
		rbac.ScopeGroup: {"group"},
	}
	path := []rbac.ScopeRef{
		{Type: rbac.ScopeLegalEntity, ID: "entity"},
		{Type: rbac.ScopeBranch, ID: "branch"},
		{Type: rbac.ScopeGroup, ID: "group"},
	}

	for _, route := range Routes(Deps{}) {
		if route.Public {
			continue
		}
		for role := range matrix.Roles {
			for _, scope := range matrix.ScopeTypes {
				actor := rbac.Actor{
					UserID: "user-1",
					Bindings: []rbac.Binding{{
						Role: role, Scope: scope, ScopeIDs: ids[scope],
					}},
				}
				resource := rbac.Resource{Module: route.Module, ScopePath: path}
				if scope == rbac.ScopeSelf {
					resource.OwnerUserID = actor.UserID
				}
				got := matrix.Authorize(actor, route.Module, route.Action, resource).Allowed

				want := false
				for _, grant := range matrix.GrantsFor(role) {
					if grant.Module != route.Module || !hasAction(grant.Actions, route.Action) {
						continue
					}
					if grant.Scope == rbac.ScopeSelf {
						want = want || scope == rbac.ScopeSelf
						continue
					}
					if grant.Scope == rbac.ScopePlatform {
						want = want || scope == rbac.ScopePlatform
						continue
					}
					// A binding at or above the grant's scope reaches the
					// resource, which carries ids for every scope level.
					want = want || (scope != rbac.ScopeSelf && ranks[scope] >= ranks[grant.Scope])
				}
				if got != want {
					t.Errorf("%s %s: role=%s scope=%s allowed=%v want=%v",
						route.Method, route.Pattern, role, scope, got, want)
				}
			}
		}
	}
}

func hasAction(actions []rbac.Action, target rbac.Action) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}
