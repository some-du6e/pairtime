// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// APIPath is where a signed-in owner reads their dashboard as JSON
// (DESIGN 3.10).
const APIPath = "/api/v1/users/me/dashboard"

// ErrNotSignedIn is what an account func returns for a request that isn't
// signed in. Any other error means the session couldn't be read, which the
// dashboard answers with a 500, never with sign-in.
var ErrNotSignedIn = errors.New("dashboard: not signed in")

// API serves GET /api/v1/users/me/dashboard: the View of the account a
// request is signed in to, as JSON. account returns that account's users
// id. A request that isn't signed in, ErrNotSignedIn, gets a 401 with the
// code not_signed_in, as GET /api/v1/users/me answers. A session that
// can't be read gets a 500 with dashboard_unavailable instead, so a client
// isn't told to sign in again because the database failed. The view is
// the owner's own and changes by the minute, so nothing may store it.
func API(src Source, account func(*http.Request) (string, error), now func() time.Time, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id, err := account(r)
		switch {
		case errors.Is(err, ErrNotSignedIn):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not_signed_in"})
			return
		case err != nil:
			logger.Error("reading a session for the dashboard", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "dashboard_unavailable"})
			return
		}
		v, err := src.View(r.Context(), id, now())
		if err != nil {
			logger.Error("reading a dashboard", "user", id, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "dashboard_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
