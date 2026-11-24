// SPDX-License-Identifier: AGPL-3.0-or-later

package pagestest

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// TestCheck holds Check to D-server-ui's test 1: it passes a page with its
// own stylesheet, the server's own scripts by their hashes and inline
// <svg>s that fetch nothing, and fails a Vite page, 'unsafe-inline', a CDN,
// a script that isn't one of the server's files and an <svg> that fetches.
func TestCheck(t *testing.T) {
	js := []byte("// SPDX-License-Identifier: AGPL-3.0-or-later\n'use strict';\n")
	hashOf := func(b string) string {
		sum := sha256.Sum256([]byte(b))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	css := hashOf("p { color: #1c1c1e; }")
	csp := func(scripts, formAction string) string {
		s := "default-src 'none'; style-src " + css
		if scripts != "" {
			s += "; script-src " + scripts
		}
		return s + "; base-uri 'none'; form-action " + formAction + "; frame-ancestors 'none'"
	}
	page := func(body string) string {
		return "<!doctype html>\n<html lang=\"en\">\n<head>\n<style>p { color: #1c1c1e; }</style>\n</head>\n<body>\n" + body + "\n</body>\n</html>\n"
	}
	ours := Options{Scripts: map[string][]byte{"live.js": js}}
	tests := []struct {
		name string
		body string
		csp  string
		o    Options
		// want is part of a problem Check must find, "" for none.
		want string
	}{
		{name: "a page with nothing but its stylesheet passes", body: page("<p>Hello</p>"), csp: csp("", "'none'")},
		{name: "a page with one of the server's scripts, named by its hash, passes", body: page("<script>" + string(js) + "</script>"), csp: csp(hashOf(string(js)), "'self'"), o: ours},
		{name: "a script that fetches may have connect-src 'self'", body: page("<script>" + string(js) + "</script>"), csp: csp(hashOf(string(js))+"; connect-src 'self'", "'self'"), o: ours},
		{name: "an inline <svg> that fetches nothing passes", body: page(`<svg viewBox="0 0 10 10" role="img"><rect class="bar" width="5" height="5"></rect></svg>`), csp: csp("", "'none'")},
		{name: "a form that posts to this server passes", body: page(`<form method="post" action="/dashboard/zone"><button type="submit">Save</button></form>`), csp: csp("", "'self'")},
		{name: "a form to the one other origin the options name passes", body: page(`<form method="post" action="https://auth.example/oauth/consent"></form>`), csp: csp("", "'self' https://auth.example"), o: Options{FormAction: "https://auth.example"}},
		{name: "a Vite page, which needs script-src 'self' for its assets, fails", body: page(`<script type="module" crossorigin src="/assets/index-a1b2c3.js"></script>`), csp: csp("'self'", "'none'"), want: "isn't one a page may send"},
		{name: "a Vite page's module script fails for its attributes", body: page(`<script type="module" crossorigin src="/assets/index-a1b2c3.js"></script>`), csp: csp("'self'", "'none'"), want: "a <script> has"},
		{name: "'unsafe-inline' fails", body: page("<script>" + string(js) + "</script>"), csp: csp("'unsafe-inline'", "'self'"), o: ours, want: "isn't one a page may send"},
		{name: "a script from a CDN fails", body: page(`<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.min.js"></script>`), csp: csp("", "'none'"), want: "a <script> has"},
		{name: "a stylesheet or font from a CDN fails", body: page(`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">`), csp: csp("", "'none'"), want: "makes a browser fetch something"},
		{name: "a CDN in the CSP fails", body: page("<p>Hello</p>"), csp: csp("https://cdn.jsdelivr.net", "'none'"), want: "isn't one a page may send"},
		{name: "a script that isn't one of the server's files fails, though the CSP names its hash", body: page("<script>alert(1)</script>"), csp: csp(hashOf("alert(1)"), "'none'"), o: ours, want: "isn't one of internal/server/pages' .js files"},
		{name: "a script the CSP doesn't name fails", body: page("<script>" + string(js) + "</script>"), csp: csp("", "'none'"), o: ours, want: "doesn't name an inline script's hash"},
		{name: "a hash in the CSP that no script on the page has fails", body: page("<p>Hello</p>"), csp: csp(hashOf(string(js)), "'none'"), o: ours, want: "which no script on the page has"},
		{name: "an <svg> with a <use href> fails", body: page(`<svg viewBox="0 0 10 10"><use href="#bar"></use></svg>`), csp: csp("", "'none'"), want: "can make a browser fetch something"},
		{name: "an <svg> that loads an image fails", body: page(`<svg viewBox="0 0 10 10"><image href="https://example.org/a.png"></image></svg>`), csp: csp("", "'none'"), want: "can make a browser fetch something"},
		{name: "an <svg> with a style of its own fails", body: page(`<svg viewBox="0 0 10 10"><style>rect{fill:url(#g)}</style></svg>`), csp: csp("", "'none'"), want: "can make a browser fetch something"},
		{name: "an <svg> that never closes fails", body: page(`<svg viewBox="0 0 10 10"><rect width="5" height="5">`), csp: csp("", "'none'"), want: "makes a browser fetch something"},
		{name: "an inline event handler fails", body: page(`<button type="button" onclick="go()">Go</button>`), csp: csp("", "'none'"), want: "inline event handler"},
		{name: "a style attribute fails", body: page(`<p style="width: 40%">Bar</p>`), csp: csp("", "'none'"), want: "style attribute"},
		{name: "a form that posts to another origin fails", body: page(`<form method="post" action="https://elsewhere.example/collect"></form>`), csp: csp("", "'self'"), want: "isn't a path on this server"},
		{name: "a form to a protocol-relative URL fails", body: page(`<form method="post" action="//elsewhere.example/collect"></form>`), csp: csp("", "'self'"), want: "isn't a path on this server"},
		{name: "a form-action with another origin fails without the options naming it", body: page("<p>Hello</p>"), csp: csp("", "'self' https://auth.example"), want: "isn't one a page may send"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check(tt.body, tt.csp, tt.o)
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("Check found %q, want nothing", got)
				}
				return
			}
			for _, p := range got {
				if strings.Contains(p, tt.want) {
					return
				}
			}
			t.Errorf("Check found %q, want a problem with %q", got, tt.want)
		})
	}
}
