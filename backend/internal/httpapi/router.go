// Package httpapi wires the HTTP surface of the Pamoja API. Routes are declared
// as data (see routes.go) so that authorisation coverage can be tested.
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/yopsa/pamoja/backend/internal/platform/config"
)

// New builds the API router.
func New(d Deps) http.Handler {
	log := d.Logger
	if log == nil {
		log = newDiscardLogger()
	}

	r := chi.NewRouter()
	r.Use(requestID)
	// Client IP is deliberately not derived from forwarding headers here: chi's
	// RealIP trusted client-supplied X-Forwarded-For / True-Client-IP values.
	// When audit logging needs the caller's IP, add middleware.ClientIPFromXFF
	// with the deployment's trusted proxy CIDRs (or ClientIPFromXFFTrustedProxies
	// with its proxy count) and read it with middleware.GetClientIP.
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(securityHeaders(d.Config.AppEnv == config.EnvProduction))
	r.Use(maxBody(d.Config.MaxBodyBytes))
	r.Use(accessLog(log))

	for _, route := range Routes(d) {
		r.Method(route.Method, route.Pattern, route.Handler)
		// Probes and proxies often use HEAD; Go discards the body itself.
		if route.Method == http.MethodGet {
			r.Method(http.MethodHead, route.Pattern, route.Handler)
		}
	}

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error":      "not_found",
			"request_id": RequestIDFrom(r.Context()),
		})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error":      "method_not_allowed",
			"request_id": RequestIDFrom(r.Context()),
		})
	})

	return r
}
