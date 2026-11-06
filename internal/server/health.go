package server

import (
	"net/http"
)

// health answers whether caddy-view itself is alive: it can read its database. It does not ask Caddy, which has its own
// status, and it says nothing else, since it needs no login.
func (a *api) health(w http.ResponseWriter, r *http.Request) {
	if a.opts.Database != nil {
		if _, err := a.opts.Database.Info(r.Context()); err != nil {
			a.log.Error("health check: the database cannot be read", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
