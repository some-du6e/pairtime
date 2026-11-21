// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// source is a Source with one answer, which records the account it was
// asked about.
type source struct {
	v     View
	err   error
	asked *string
}

func (s source) View(_ context.Context, account string, _ time.Time) (View, error) {
	*s.asked = account
	return s.v, s.err
}

func TestAPI(t *testing.T) {
	sample := demoView(t, demoNow)
	tests := []struct {
		name    string
		account string
		// sessionErr is the error reading the session gives, if any.
		sessionErr error
		err        error
		wantCode   int
		wantBody   string
		wantAsked  string
	}{
		{name: "a signed-in owner gets their own view as JSON", account: "u1", wantCode: 200, wantBody: `"today":"2026-11-20"`, wantAsked: "u1"},
		{name: "the JSON keeps pending apart from final", account: "u1", wantCode: 200, wantBody: `"you":{"final_s":6240,"pending_s":540}`, wantAsked: "u1"},
		{name: "each project has its account-level id, its devices' keys and its name", account: "u1", wantCode: 200, wantBody: `{"id":"p_sample_chip8","keys":["p_sample_chip8"],"name":"chip8-emulator"}`, wantAsked: "u1"},
		{name: "the JSON says whether a repo on two computers shows as two projects", account: "u1", wantCode: 200, wantBody: `"projects_split_by_device":false`, wantAsked: "u1"},
		{name: "a request that isn't signed in gets 401 not_signed_in, as GET /api/v1/users/me answers", wantCode: 401, wantBody: `{"error":"not_signed_in"}`},
		{name: "a session that can't be read is a 500, not a sign-in, and no view is read", sessionErr: errors.New("database is locked"), wantCode: 500, wantBody: `{"error":"dashboard_unavailable"}`},
		{name: "a view that doesn't load is a 500, never a view with wrong numbers", account: "u1", err: errors.New("disk on fire"), wantCode: 500, wantBody: `{"error":"dashboard_unavailable"}`, wantAsked: "u1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asked string
			h := API(source{v: sample, err: tt.err, asked: &asked},
				func(*http.Request) (string, error) {
					switch {
					case tt.sessionErr != nil:
						return "", tt.sessionErr
					case tt.account == "":
						return "", ErrNotSignedIn
					}
					return tt.account, nil
				},
				func() time.Time { return demoNow }, slog.New(slog.DiscardHandler))
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, APIPath, nil))
			if rec.Code != tt.wantCode {
				t.Fatalf("status %d, want %d", rec.Code, tt.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body %s doesn't contain %s", rec.Body.String(), tt.wantBody)
			}
			if asked != tt.wantAsked {
				t.Errorf("the source was asked about %q, want %q", asked, tt.wantAsked)
			}
			for h, want := range map[string]string{"Cache-Control": "no-store", "Content-Type": "application/json; charset=utf-8", "X-Content-Type-Options": "nosniff"} {
				if got := rec.Header().Get(h); got != want {
					t.Errorf("%s = %q, want %q", h, got, want)
				}
			}
		})
	}
	t.Run("the JSON reads back as the view it was made from", func(t *testing.T) {
		var asked string
		h := API(source{v: sample, asked: &asked}, func(*http.Request) (string, error) { return "u1", nil }, func() time.Time { return demoNow }, slog.New(slog.DiscardHandler))
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, APIPath, nil))
		var got View
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Week.You != sample.Week.You || got.Today != sample.Today || len(got.Days) != MonthDays || !got.Sample {
			t.Errorf("read back %+v", got.Week)
		}
	})
}
