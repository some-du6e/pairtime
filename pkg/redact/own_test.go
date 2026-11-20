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

// TestKubernetesSecretValues covers pasted Secret manifests: every value of
// their data and stringData blocks goes, and nothing outside them does.
func TestKubernetesSecretValues(t *testing.T) {
	b1 := "UyFCXCpk" + "JHpEc2I9"
	b2 := "cG9zdGdyZXM6Ly9hZG1p" + "bjpodW50ZXIyQGRiLmV4YW1wbGUuY29tOjU0MzIvYXBw"
	pw := "hunter2" + "hunter2"
	tests := []struct {
		name string
		text string
		gone []string
		kept []string
	}{
		{"every data value goes, not just the first, whatever its key",
			"apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\ntype: Opaque\ndata:\n  password: " + b1 + "\n  DATABASE_URL: " + b2 + "\n",
			[]string{b1, b2}, []string{"DATABASE_URL:", "name: db", "type: Opaque"}},
		{"stringData values go, and every line of a multi-line value",
			"kind: Secret\nstringData:\n  api-key: " + pw + "\n  config.yaml: |\n    db:\n      url: x\n  after: y\n",
			[]string{pw, "db:", "url: x", ": y"}, []string{"api-key:", "config.yaml:", "after:"}},
		{"a manifest in a raw JSON string goes too",
			`{"content":"apiVersion: v1\nkind: Secret\ndata:\n  password: ` + b1 + `\n  DATABASE_URL: ` + b2 + `\n"}`,
			[]string{b1, b2}, []string{"DATABASE_URL:"}},
		{"a manifest as kubectl prints it, with kind after data, goes too",
			"apiVersion: v1\ndata:\n  password: " + b1 + "\nkind: Secret\nmetadata:\n  name: x\n",
			[]string{b1}, []string{"name: x"}},
		{"a ConfigMap keeps its data", "kind: ConfigMap\ndata:\n  LOG_LEVEL: debug\n", nil, []string{"LOG_LEVEL: debug"}},
		{"the next document isn't the Secret's",
			"kind: Secret\ndata:\n  a: " + b1 + "\n---\nkind: ConfigMap\ndata:\n  b: plainvalue\n",
			[]string{b1}, []string{"b: plainvalue"}},
		// gitleaks' own span takes the first entry's key with its value.
		{"CRLF lines and a quoted kind still make a Secret", "kind: \"Secret\"\r\ndata:\r\n  a: " + b1 + "\r\n  b: " + b2 + "\r\n",
			[]string{b1, b2}, []string{"  b: [SECRET]\r\n"}},
	}
	r := mustNew(t, Known{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := Render(tt.text, r.Find(tt.text))
			for _, g := range tt.gone {
				if strings.Contains(out, g) {
					t.Errorf("Render = %q, still holds %q", out, g)
				}
			}
			for _, k := range tt.kept {
				if !strings.Contains(out, k) {
					t.Errorf("Render = %q, lost %q", out, k)
				}
			}
		})
	}
}
