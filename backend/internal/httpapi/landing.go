package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/yopsa/pamoja/backend/internal/webui"
)

// handleLanding serves the Yopsa Pamoja web client shell. Every screen is
// rendered by the client from the JSON API below; the API keeps enforcing
// every rule, so the browser is never trusted.
func (d Deps) handleLanding(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(webui.Index())
}

// handleAsset serves one file of the embedded client.
func (d Deps) handleAsset(w http.ResponseWriter, r *http.Request) {
	body, contentType, ok := webui.Asset(chi.URLParam(r, "*"))
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// handleFavicon serves the brand mark.
func (d Deps) handleFavicon(w http.ResponseWriter, r *http.Request) {
	body, contentType, ok := webui.Asset("favicon.svg")
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}
