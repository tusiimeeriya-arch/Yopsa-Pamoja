package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const phase = "P6 — domain core and web client"

func (d Deps) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (d Deps) handleReadiness(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{}
	ready := true

	if d.Pool == nil {
		checks["database"] = "not configured"
		ready = false
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.Pool.Ping(ctx); err != nil {
			checks["database"] = "unreachable"
			ready = false
		} else {
			checks["database"] = "ok"
			var version int64
			err := d.Pool.QueryRow(ctx,
				`SELECT version FROM public.schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version)
			switch {
			case err != nil:
				checks["schema"] = "unversioned"
				ready = false
			default:
				checks["schema"] = "ok"
			}
		}
	}

	status := http.StatusOK
	label := "ok"
	if !ready {
		status = http.StatusServiceUnavailable
		label = "degraded"
	}
	writeJSON(w, status, map[string]any{"status": label, "checks": checks})
}

func (d Deps) handleMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service":     "pamoja-api",
		"version":     d.Version,
		"environment": string(d.Config.AppEnv),
		"phase":       phase,
		"time":        time.Now().UTC().Format(time.RFC3339),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent; log-free by design (no request data).
		return
	}
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
}
