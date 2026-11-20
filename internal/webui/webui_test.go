// SPDX-License-Identifier: AGPL-3.0-or-later

package webui

import (
	"fmt"
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
			name: "the built dashboard's CSP is exactly vite.config.ts's policy, which allows its own origin only",
			check: func(t *testing.T) {
				if kind != "local" {
					t.Skip("the placeholder loads nothing and has no CSP")
				}
				m := cspTag.FindStringSubmatch(page)
				if m == nil {
					t.Fatal("index.html has no Content-Security-Policy <meta>; vite.config.ts adds it to builds")
				}
				csp := html.UnescapeString(m[1])
				for _, p := range cspProblems(csp) {
					t.Errorf("the CSP isn't vite.config.ts's: %s (the policy: %s)", p, csp)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// cspWant is contentSecurityPolicy in web/vite.config.ts: all nine
// directives, in its order. Changing the policy means changing this too.
var cspWant = []struct{ name, sources string }{
	{"default-src", "'none'"},
	{"script-src", "'self'"},
	{"style-src", "'self'"},
	{"img-src", "'self'"},
	{"font-src", "'self'"},
	{"connect-src", "'self'"},
	{"manifest-src", "'self'"},
	{"base-uri", "'none'"},
	{"form-action", "'self'"},
}

// cspProblems lists every way csp differs from cspWant: a directive that is
// missing, has other sources, isn't in cspWant, or is given twice. Each
// problem starts with the directive's name. Names are case-insensitive, and
// a browser obeys only the first of two directives with the same name, so
// a second one counts as a problem even when it matches.
func cspProblems(csp string) []string {
	var problems, names []string
	policy := map[string]string{}
	for _, d := range strings.Split(csp, ";") {
		fields := strings.Fields(d)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if _, twice := policy[name]; twice {
			problems = append(problems, name+" is given twice")
			continue
		}
		names = append(names, name)
		policy[name] = strings.Join(fields[1:], " ")
	}
	wanted := map[string]bool{}
	for _, w := range cspWant {
		wanted[w.name] = true
		got, ok := policy[w.name]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s is missing; want %s %s", w.name, w.name, w.sources))
		case got != w.sources:
			problems = append(problems, fmt.Sprintf("%s is %q; want %q", w.name, got, w.sources))
		}
	}
	for _, name := range names {
		if !wanted[name] {
			problems = append(problems, fmt.Sprintf("%s %s isn't in the policy", name, policy[name]))
		}
	}
	return problems
}

// TestCSPProblems shows the CSP case in TestFS can fail, on policies a
// build could write.
func TestCSPProblems(t *testing.T) {
	const policy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; " +
		"connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'"
	var every []string
	for _, w := range cspWant {
		every = append(every, w.name)
	}
	tests := []struct {
		name string
		csp  string
		// The directive each problem names, in order.
		want []string
	}{
		{
			name: "vite.config.ts's policy passes",
			csp:  policy,
		},
		{
			name: "a trailing semicolon and extra spaces change nothing",
			csp:  strings.ReplaceAll(policy, "; ", " ;  ") + ";",
		},
		{
			name: "a directive that also allows another host fails",
			csp:  strings.Replace(policy, "font-src 'self'", "font-src 'self' https://fonts.example", 1),
			want: []string{"font-src"},
		},
		{
			name: "a directive the policy lacks fails, even one that only adds hosts",
			csp:  policy + "; frame-src https://example.com",
			want: []string{"frame-src"},
		},
		{
			name: "a missing directive fails",
			csp:  strings.Replace(policy, "; manifest-src 'self'", "", 1),
			want: []string{"manifest-src"},
		},
		{
			name: "a directive given twice fails, whatever its case",
			csp:  policy + "; SCRIPT-SRC 'self'",
			want: []string{"script-src"},
		},
		{
			name: "an empty policy misses every directive",
			csp:  "",
			want: every,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cspProblems(tt.csp)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d problems, want %d: %q", len(got), len(tt.want), got)
			}
			for i, p := range got {
				if !strings.HasPrefix(p, tt.want[i]+" ") {
					t.Errorf("problem %d is %q, want one about %s", i, p, tt.want[i])
				}
			}
		})
	}
}
