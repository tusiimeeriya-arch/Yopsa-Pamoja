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
}

// Route is one entry of the HTTP surface. Routes are declared as data so that
// tests can prove every non-public route names the RBAC module and action it
// requires; a handler cannot be registered without that declaration.
type Route struct {
	Method  string
	Pattern string
	Module  rbac.Module
	Action  rbac.Action
	Public  bool // unauthenticated: liveness, readiness, landing page, version.
	Handler http.HandlerFunc
}

// Routes returns the complete HTTP surface for this build.
//
// P0 exposes only unauthenticated platform endpoints. Authentication, the
// Authorize pipeline and domain routes arrive in P1/P2 and must declare their
// module and action here.
func Routes(d Deps) []Route {
	return []Route{
		{Method: http.MethodGet, Pattern: "/", Public: true, Handler: d.handleLanding},
		{Method: http.MethodGet, Pattern: "/healthz", Public: true, Handler: d.handleLiveness},
		{Method: http.MethodGet, Pattern: "/readyz", Public: true, Handler: d.handleReadiness},
		{Method: http.MethodGet, Pattern: "/api/v1/meta", Public: true, Handler: d.handleMeta},
	}
}
