// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// APIPath is where a signed-in owner reads their dashboard as JSON
// (DESIGN 3.10).
const APIPath = "/api/v1/users/me/dashboard"

// ErrNotSignedIn is what an account func returns for a request that isn't
// signed in. Any other error means the session couldn't be read, which the
// dashboard answers with a 500, never with sign-in.
var ErrNotSignedIn = errors.New("dashboard: not signed in")

// sessionChallenge is the WWW-Authenticate challenge of the 401 (RFC 9110
// 15.5.2), the one GET /api/v1/users/me sends: a request authenticates
// with the session cookie Hack Club Auth sign-in sets.
const sessionChallenge = `Session realm="pairtime"`

// API serves GET /api/v1/users/me/dashboard: the View of the account a
// request is signed in to, as JSON. account returns that account's users
// id. A request that isn't signed in, ErrNotSignedIn, gets a 401 with the
// code not_signed_in, as GET /api/v1/users/me answers. A session that
// can't be read gets a 500 with dashboard_unavailable instead, so a client
// isn't told to sign in again because the database failed. The view is
// the owner's own and changes by the minute, so nothing may store it.
//
// It is also the snapshot the live dashboard polls while its stream is
// quiet (SP 6.4; WP-SV8): each answer has the View's ETag, and a request
// whose If-None-Match names it gets 304 with no body.
func API(src Source, account func(*http.Request) (string, error), now func() time.Time, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id, err := account(r)
		switch {
		case errors.Is(err, ErrNotSignedIn):
			w.Header().Set("WWW-Authenticate", sessionChallenge)
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
		tag := ETag(v)
		w.Header().Set("ETag", tag)
		if noneMatch(r.Header.Values("If-None-Match"), tag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// ETag returns v's entity tag. It is weak, and leaves AsOf out: two Views
// that differ only in when they were read are the same dashboard, so a
// poll a minute after the last finds it unchanged (RFC 9110 8.8.1, 8.8.3).
func ETag(v View) string {
	v.AsOf = time.Time{}
	b, err := json.Marshal(v)
	if err != nil {
		// A View always encodes; a tag that matches nothing still works.
		return `W/"unencodable"`
	}
	sum := sha256.Sum256(b)
	return `W/"` + hex.EncodeToString(sum[:16]) + `"`
}

// noneMatch reports whether If-None-Match, as values, names tag or is *,
// comparing tags weakly, as RFC 9110 13.1.2 asks of it.
func noneMatch(values []string, tag string) bool {
	opaque := strings.TrimPrefix(tag, "W/")
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t == "*" || strings.TrimPrefix(t, "W/") == opaque {
				return true
			}
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
