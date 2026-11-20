// SPDX-License-Identifier: AGPL-3.0-or-later

package webui

import (
	"html"
	"io/fs"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var (
	// The UI an index.html is: "placeholder" for the checked-in one, or the
	// Vite entry it was built from (web/src/<name>/index.html).
	uiMeta = regexp.MustCompile(`<meta name="pairtime-ui" content="([a-z]+)"`)
	// What a page loads: a script's src or a link's href. An <a href> is
	// only a link, so it isn't matched.
	loads  = regexp.MustCompile(`<(?:script|link)\b[^>]*?\s(?:src|href)="([^"]*)"`)
	cspTag = regexp.MustCompile(`<meta http-equiv="Content-Security-Policy" content="([^"]*)"`)
)

// These hold for the placeholder, which ci.yml embeds, and for the real
// dashboard, which web.yml builds into dist/ before it runs them again.
func TestFS(t *testing.T) {
	index, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(index)
	kind := ""
	if m := uiMeta.FindStringSubmatch(page); m != nil {
		kind = m[1]
	}
	// Attribute values as the browser reads them: Vite writes ' as &#39;.
	var loaded []string
	for _, m := range loads.FindAllStringSubmatch(page, -1) {
		loaded = append(loaded, html.UnescapeString(m[1]))
	}

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "index.html is at the root of the UI",
			check: func(t *testing.T) {
				if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(page)), "<!doctype html>") {
					t.Error("index.html doesn't start with <!doctype html>")
				}
			},
		},
		{
			name: "the embedded UI is the placeholder or the local dashboard, not another entry point",
			check: func(t *testing.T) {
				if kind != "placeholder" && kind != "local" {
					t.Errorf(`<meta name="pairtime-ui"> says %q; want "placeholder", or "local" from vite build --mode agent`, kind)
				}
			},
		},
		{
			name: "every script and stylesheet index.html loads is embedded",
			check: func(t *testing.T) {
				for _, ref := range loaded {
					u, err := url.Parse(ref)
					if err != nil || u.Scheme != "" || u.Host != "" {
						continue // the next case reports these
					}
					name := strings.TrimPrefix(u.Path, "/")
					if _, err := fs.Stat(FS(), name); err != nil {
						t.Errorf("index.html loads %s, which isn't embedded: %v", ref, err)
					}
				}
			},
		},
		{
			name: "index.html loads nothing from another host",
			check: func(t *testing.T) {
				for _, ref := range loaded {
					u, err := url.Parse(ref)
					if err != nil || u.Scheme != "" || u.Host != "" || strings.HasPrefix(ref, "//") {
						t.Errorf("index.html loads %q; everything must come from the agent itself", ref)
					}
				}
			},
		},
		{
			name: "the built dashboard's CSP lets it load and connect to its own origin only",
			check: func(t *testing.T) {
				if kind != "local" {
					t.Skip("the placeholder loads nothing and has no CSP")
				}
				m := cspTag.FindStringSubmatch(page)
				if m == nil {
					t.Fatal("index.html has no Content-Security-Policy <meta>; vite.config.ts adds it to builds")
				}
				csp := html.UnescapeString(m[1])
				policy := map[string]string{}
				for _, d := range strings.Split(csp, ";") {
					name, sources, _ := strings.Cut(strings.TrimSpace(d), " ")
					policy[name] = strings.TrimSpace(sources)
				}
				want := map[string]string{
					"default-src": "'none'",
					"script-src":  "'self'",
					"style-src":   "'self'",
					"connect-src": "'self'",
					"img-src":     "'self'",
					"base-uri":    "'none'",
				}
				for name, sources := range want {
					if policy[name] != sources {
						t.Errorf("CSP %s is %q, want %q (the policy: %s)", name, policy[name], sources, csp)
					}
				}
				for _, bad := range []string{"'unsafe-inline'", "'unsafe-eval'", "http:", "https:", "*", "data:"} {
					if strings.Contains(csp, bad) {
						t.Errorf("the CSP allows %s: %s", bad, csp)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}
