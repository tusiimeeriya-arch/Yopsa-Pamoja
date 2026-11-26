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
