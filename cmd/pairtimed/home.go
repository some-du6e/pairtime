// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/auth"
)

// homePage is the page at / exactly: where a sign-in without return_to
// lands, which until now was a 404. It says whether the browser is signed
// in, and links to the account, the privacy notice and the evidence page.
// The live dashboard replaces it (WP-SV8).
const homePage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>pairtime</title>
</head>
<body>
<h1>pairtime</h1>
<p>%s</p>
<ul>
<li><a href="/privacy">Privacy notice</a></li>
<li><a href="/docs/evidence">What counts as evidence</a></li>
<li><a href="https://github.com/some-du6e/pairtime">Source code</a></li>
</ul>
</body>
</html>
`

// The page's one line, by state.
const (
	homeSignedIn  = `You're signed in with Hack Club Auth. <a href="` + auth.MePath + `">Your account</a>.`
	homeSignedOut = `<a href="` + auth.LoginPath + `">Sign in with Hack Club Auth</a>.`
	homeNoSignIn  = `Sign-in isn't set up on this server yet.`
)

// home serves homePage.
func home(signIn *auth.Service, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		line := homeNoSignIn
		if signIn.Enabled() {
			line = homeSignedOut
			_, err := signIn.Session(r)
			switch {
			case err == nil:
				line = homeSignedIn
			case !errors.Is(err, accounts.ErrNoSession):
				logger.Error("reading a session for the home page", "err", err)
			}
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		// The page differs by session, so nothing may keep a copy.
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Method == http.MethodHead {
			return
		}
		// The lines are constants, so there is nothing to escape.
		w.Write([]byte(strings.Replace(homePage, "%s", line, 1)))
	}
}
