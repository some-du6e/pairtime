// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"slices"
	"strings"
	"testing"
)

// TestOwnRules covers pairtime's own secret rules: the password of a
// connection URL and the credential of a bare Authorization header, which
// no gitleaks rule finds. The values are built at run time, so no file in
// the repo looks like a leaked password.
func TestOwnRules(t *testing.T) {
	pw := "Tr0ub4" + "dor3x"
	tok := "8f14e45fceea167a" + "5a36dedd4bea2543"
	b64 := "bWF5YTpo" + "dW50ZXIy"
	tests := []struct {
		name   string
		text   string
		secret string // empty for a near miss, where neither rule may fire
		rule   string
	}{
		{"a URL password before a host and port goes", "DATABASE_URL=postgres://postgres:" + pw + "@localhost:5432/app", pw, "url-password"},
		{"a URL password before a numeric host goes", "redis://default:" + pw + "@127.0.0.1:6379", pw, "url-password"},
		{"a URL password before a bare host goes", "mysql://root:" + pw + "@db/app", pw, "url-password"},
		{"a URL password with no user goes", "redis://:" + pw + "@cache:6379", pw, "url-password"},
		{"a URL password in a JSON string goes", `{"url":"postgres://app:` + pw + `@db:5432/app"}`, pw, "url-password"},
		{"a URL password in an escaped JSON string goes", `{"cmd":"psql \"postgres://app:` + pw + `@db:5432/app\""}`, pw, "url-password"},
		{"a URL password with an @ in it goes whole", "postgres://app:p@ss" + pw + "@db/x", "p@ss" + pw, "url-password"},
		{"a bearer token in an Authorization header goes", "Authorization: Bearer " + tok, tok, "authorization-header"},
		{"basic credentials go, whatever the case", "authorization: basic " + b64, b64, "authorization-header"},
		{"a header in a JSON object goes", `{"headers":{"Authorization":"Bearer ` + tok + `"}}`, tok, "authorization-header"},
		{"a header in an escaped JSON string goes", `{"cmd":"curl -H \"Authorization: Bearer ` + tok + `\" x"}`, tok, "authorization-header"},
		{"a proxy header with the token scheme goes", "Proxy-Authorization: token " + tok, tok, "authorization-header"},
		{"URLs without a password keep everything",
			"https://github.com:443/org/repo and ssh://git@github.com/org/repo and git+ssh://git@github.com:org/repo", "", ""},
		{"an Authorization header without a scheme is left to gitleaks", "Authorization: required for every endpoint", "", ""},
	}
	r := mustNew(t, Known{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := r.Find(tt.text)
			if tt.secret == "" {
				for _, sp := range spans {
					if sp.Rule == "url-password" || sp.Rule == "authorization-header" {
						t.Errorf("Find = %v, want no span from pairtime's rules", spans)
					}
				}
				return
			}
			start := strings.Index(tt.text, tt.secret)
			want := Span{Start: start, End: start + len(tt.secret), Type: TypeSecret, Rule: tt.rule}
			if !slices.Contains(spans, want) {
				t.Errorf("Find = %v, want it to contain %v", spans, want)
			}
			if out := Render(tt.text, spans); strings.Contains(out, tt.secret) {
				t.Errorf("Render = %q, still holds the secret", out)
			}
		})
	}
}

func TestOwnRuleIDs(t *testing.T) {
	rs := mustRules(t)
	for _, o := range ownRules {
		t.Run(o.id+" is no gitleaks rule's id", func(t *testing.T) {
			if rs.byID[o.id] != nil {
				t.Errorf("gitleaks has a rule %q too", o.id)
			}
		})
	}
}
