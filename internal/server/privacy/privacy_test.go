// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/privacy"
	"github.com/some-du6e/pairtime/internal/server/store"
)

// errorOf decodes an error answer's body.
func errorOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the answer %q isn't JSON: %v", rec.Body.String(), err)
	}
	return body
}

func TestEndpoints(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, f *fixture)
	}{
		{name: "a request without a bearer token is a 401 missing_token", run: func(t *testing.T, f *fixture) {
			rec := f.call(t, privacy.DeletePath, "")
			if rec.Code != http.StatusUnauthorized || errorOf(t, rec)["error"] != "missing_token" {
				t.Errorf("answered %d %s", rec.Code, rec.Body.String())
			}
		}},
		{name: "an Authorization header of another scheme is a 401 missing_token", run: func(t *testing.T, f *fixture) {
			req := httptest.NewRequest(http.MethodPost, privacy.DeletePath, nil)
			req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
			rec := httptest.NewRecorder()
			f.mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || errorOf(t, rec)["error"] != "missing_token" {
				t.Errorf("answered %d %s", rec.Code, rec.Body.String())
			}
		}},
		{name: "the scheme's name is case-insensitive", run: func(t *testing.T, f *fixture) {
			req := httptest.NewRequest(http.MethodPost, privacy.ExportPath, nil)
			req.Header.Set("Authorization", "bearer "+f.hub.token(t, f.hub.claims(f.now, privacy.ActionExport, subject(nil, nil, nil))))
			rec := httptest.NewRecorder()
			f.mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("answered %d %s", rec.Code, rec.Body.String())
			}
		}},
		{name: "a token used once is a 409 replayed the second time", run: func(t *testing.T, f *fixture) {
			token := f.hub.token(t, f.hub.claims(f.now, privacy.ActionExport, subject(nil, nil, nil)))
			if rec := f.call(t, privacy.ExportPath, token); rec.Code != http.StatusOK {
				t.Fatalf("the first call answered %d %s", rec.Code, rec.Body.String())
			}
			rec := f.call(t, privacy.ExportPath, token)
			if rec.Code != http.StatusConflict || errorOf(t, rec)["error"] != "replayed" {
				t.Errorf("the replay answered %d %s, want 409 replayed", rec.Code, rec.Body.String())
			}
		}},
		{name: "a dry run's token is a 409 replayed when the hub's real run reuses it", run: func(t *testing.T, f *fixture) {
			c := f.hub.claims(f.now, privacy.ActionDelete, subject([]string{teen.email}, nil, nil))
			c["dry_run"] = true
			token := f.hub.token(t, c)
			if rec := f.call(t, privacy.DeletePath, token); rec.Code != http.StatusOK {
				t.Fatalf("the dry run answered %d %s", rec.Code, rec.Body.String())
			}
			if rec := f.call(t, privacy.DeletePath, token); rec.Code != http.StatusConflict {
				t.Errorf("reusing the dry run's token answered %d %s, want 409", rec.Code, rec.Body.String())
			}
		}},
		{name: "a delete token sent to export is a 400 action_mismatch, and records no jti", run: func(t *testing.T, f *fixture) {
			rec := f.call(t, privacy.ExportPath, f.hub.token(t, f.hub.claims(f.now, privacy.ActionDelete, subject(nil, nil, nil))))
			if rec.Code != http.StatusBadRequest || errorOf(t, rec)["error"] != "action_mismatch" {
				t.Errorf("answered %d %s", rec.Code, rec.Body.String())
			}
			if n := len(f.dump(t)["privacy_token_uses"]); n != 0 {
				t.Errorf("privacy_token_uses holds %d rows after a refused token", n)
			}
		}},
		{name: "every answer is JSON that no cache may keep", run: func(t *testing.T, f *fixture) {
			for _, rec := range []*httptest.ResponseRecorder{
				f.call(t, privacy.DeletePath, ""),
				f.ask(t, privacy.ActionExport, subject(nil, nil, nil), false),
			} {
				if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "application/json" {
					t.Errorf("answered %d with Cache-Control %q and Content-Type %q", rec.Code, rec.Header().Get("Cache-Control"), rec.Header().Get("Content-Type"))
				}
			}
		}},
		{name: "the answer echoes the token's request_id and dry_run", run: func(t *testing.T, f *fixture) {
			c := f.hub.claims(f.now, privacy.ActionDelete, subject(nil, nil, nil))
			c["dry_run"] = true
			r := report(t, f.call(t, privacy.DeletePath, f.hub.token(t, c)))
			if r.RequestID != c["request_id"] || !r.DryRun {
				t.Errorf("report %+v, want request_id %v and dry_run true", r, c["request_id"])
			}
		}},
		{name: "the log names the request_id and the outcome, and nothing of the person", run: func(t *testing.T, f *fixture) {
			world(t, f)
			c := f.hub.claims(f.now, privacy.ActionDelete, subject([]string{teen.email}, teen.slack, teen.sub))
			report(t, f.call(t, privacy.DeletePath, f.hub.token(t, c)))
			f.call(t, privacy.DeletePath, "not-a-token")
			logs := f.logs.String()
			if !strings.Contains(logs, c["request_id"].(string)) || !strings.Contains(logs, "outcome=deleted") || !strings.Contains(logs, "outcome=malformed_token") {
				t.Errorf("the log lacks the request_id or an outcome:\n%s", logs)
			}
			for _, m := range []string{teen.email, teen.slack, teen.sub, teen.id, teen.device, f.emails.Index(teen.email), c["jti"].(string)} {
				if strings.Contains(logs, m) {
					t.Errorf("the log names %q:\n%s", m, logs)
				}
			}
		}},
		{name: "a GET is refused, since the hub only POSTs", run: func(t *testing.T, f *fixture) {
			rec := httptest.NewRecorder()
			f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, privacy.DeletePath, nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("GET answered %d", rec.Code)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, newFixture(t, store.SQLite))
		})
	}
}

func TestNew(t *testing.T) {
	f := newFixture(t, store.SQLite)
	full := privacy.Options{Verifier: &privacy.Verifier{Audience: audience, Keys: f.hub}, DB: f.db, Writer: f.w, Emails: f.emails, Logger: slog.Default()}
	tests := []struct {
		name string
		edit func(o *privacy.Options)
	}{
		{name: "a service without a verifier is refused", edit: func(o *privacy.Options) { o.Verifier = nil }},
		{name: "a service without a database is refused", edit: func(o *privacy.Options) { o.DB = nil }},
		{name: "a service without a writer is refused", edit: func(o *privacy.Options) { o.Writer = nil }},
		{name: "a service without an email cipher, which finds accounts by email, is refused", edit: func(o *privacy.Options) { o.Emails = nil }},
		{name: "a service without a logger is refused", edit: func(o *privacy.Options) { o.Logger = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := full
			tt.edit(&o)
			if _, err := privacy.New(o); err == nil {
				t.Error("New took options without it")
			}
		})
	}
}
