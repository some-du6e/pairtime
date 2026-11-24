// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pagestest holds a page pairtimed serves to the rules every
// server page keeps (D-server-ui; PLAN ground rule 12): a CSP of one
// shape, inline scripts that are the server's own hand-written files,
// named by their hashes, and nothing that makes a browser fetch anything.
// pages' tests check its pages with Check, and the tests of every other
// package that serves a page, such as device's, with Page.
package pagestest

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Options are what a page may have beyond its stylesheet.
type Options struct {
	// Scripts are the files a page may carry inline, by name: the .js
	// files embedded in internal/server/pages (pages.Scripts). A page's
	// inline script must be one of them, byte for byte.
	Scripts map[string][]byte
	// FormAction is the one origin besides this server that a form may
	// post to, such as "https://auth.example": PG6's consent page names
	// its request's redirect URI's (D-server-ui point 3). "" for none.
	FormAction string
}

const (
	hash = `'sha256-[A-Za-z0-9+/]+={0,2}'`
	// shape is the CSP a page sends, but for its form-action: its own
	// stylesheet by its hash, and, on a page with scripts, each script
	// by its hash, with connect-src 'self' when one fetches.
	shapeStart = `^default-src 'none'; style-src ` + hash + `(; script-src( ` + hash + `)+(; connect-src 'self')?)?; base-uri 'none'; form-action `
	shapeEnd   = `; frame-ancestors 'none'$`
)

var (
	// strict is the CSP's shape for a page whose forms post only here.
	strict = regexp.MustCompile(shapeStart + `'(none|self)'` + shapeEnd)
	// scriptSrc finds the hashes a CSP's script-src names.
	scriptSrc = regexp.MustCompile(`; script-src ((?:'sha256-[A-Za-z0-9+/]+={0,2}' ?)+)`)
	// script finds each <script> element, its attributes and its text.
	script = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	// svg finds each inline <svg>, and svgFetch what in one could make a
	// browser fetch something or run a script.
	svg      = regexp.MustCompile(`(?is)<svg\b.*?</svg\s*>`)
	svgFetch = regexp.MustCompile(`(?i)\bhref\s*=|\bsrc\s*=|<use\b|<image\b|<feimage\b|<foreignobject\b|<script\b|<style\b|url\(`)
	// fetches finds markup that makes a browser fetch something while it
	// loads the page: a script, a stylesheet or font through <link>, an
	// image, a frame, media, an <svg> left after the clean ones went, a
	// <base> that moves relative links, a meta refresh, and CSS that loads
	// a URL.
	fetches = regexp.MustCompile(`(?i)<(script|link|img|image|iframe|frame|object|embed|audio|video|source|track|picture|svg|base)\b|\b(src|srcset|poster)\s*=|@import|url\(|http-equiv`)
	// handler finds an inline event handler, and style a style attribute.
	handler = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	style   = regexp.MustCompile(`(?i)\sstyle\s*=`)
	// formAction finds where each form on a page posts.
	formAction = regexp.MustCompile(`(?i)<form\b[^>]*\baction="([^"]*)"`)
)

// Check returns what is wrong with a page, given its HTML and its
// Content-Security-Policy header, one sentence a problem, or nothing.
func Check(body, csp string, o Options) []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	shape := strict
	if o.FormAction != "" {
		shape = regexp.MustCompile(shapeStart + `'self' ` + regexp.QuoteMeta(o.FormAction) + shapeEnd)
	}
	if !shape.MatchString(csp) {
		add("the CSP %q isn't one a page may send: its own stylesheet and scripts by their hashes, connect-src 'self' at most, and forms to this server", csp)
	}

	named := map[string]bool{}
	if m := scriptSrc.FindStringSubmatch(csp); m != nil {
		for _, h := range strings.Fields(m[1]) {
			named[strings.Trim(h, "'")] = true
		}
	}
	files := make(map[string]bool, len(o.Scripts))
	for _, b := range o.Scripts {
		files[string(b)] = true
	}
	carried := map[string]bool{}
	for _, m := range script.FindAllStringSubmatch(body, -1) {
		attrs, text := m[1], m[2]
		if strings.TrimSpace(attrs) != "" {
			add("a <script> has %q: a page's script is inline, with no src, type or other attribute", strings.TrimSpace(attrs))
		}
		sum := sha256.Sum256([]byte(text))
		h := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
		carried[h] = true
		if !named[h] {
			add("the CSP's script-src doesn't name an inline script's hash, %s", h)
		}
		if !files[text] {
			add("an inline script of %d bytes isn't one of internal/server/pages' .js files, byte for byte", len(text))
		}
	}
	for h := range named {
		if !carried[h] {
			add("the CSP's script-src names %s, which no script on the page has", h)
		}
	}

	// What a browser would fetch is checked with the scripts, which the
	// CSP allows by their hashes, and the clean inline <svg>s taken out.
	rest := script.ReplaceAllString(body, "")
	for _, s := range svg.FindAllString(rest, -1) {
		if m := svgFetch.FindString(s); m != "" {
			add("an inline <svg> has %q, which can make a browser fetch something", m)
		}
	}
	rest = svg.ReplaceAllString(rest, "")
	if m := fetches.FindString(rest); m != "" {
		add("the page has %q, which makes a browser fetch something", m)
	}
	if m := handler.FindString(rest); m != "" {
		add("the page has an inline event handler, %q", strings.TrimSpace(m))
	}
	if m := style.FindString(rest); m != "" {
		add("the page has a style attribute, %q", strings.TrimSpace(m))
	}
	for _, m := range formAction.FindAllStringSubmatch(rest, -1) {
		to := m[1]
		here := strings.HasPrefix(to, "/") && !strings.HasPrefix(to, "//")
		if !here && (o.FormAction == "" || !strings.HasPrefix(to, o.FormAction+"/")) {
			add("a form posts to %q, which isn't a path on this server", to)
		}
	}
	return problems
}

// Page reports each problem Check finds with a page as an error of t.
func Page(t testing.TB, body, csp string, o Options) {
	t.Helper()
	for _, p := range Check(body, csp, o) {
		t.Error(p)
	}
}
