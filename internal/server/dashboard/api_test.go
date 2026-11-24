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
		// wantChallenge is the WWW-Authenticate header the answer must
		// carry, "" for none.
		wantChallenge string
	}{
		{name: "a signed-in owner gets their own view as JSON", account: "u1", wantCode: 200, wantBody: `"today":"2026-11-20"`, wantAsked: "u1"},
		{name: "the JSON keeps pending apart from final", account: "u1", wantCode: 200, wantBody: `"you":{"final_s":6240,"pending_s":540}`, wantAsked: "u1"},
		{name: "each project has its account-level id, its devices' keys and its name", account: "u1", wantCode: 200, wantBody: `{"id":"p_sample_chip8","keys":["p_sample_chip8"],"name":"chip8-emulator"}`, wantAsked: "u1"},
		{name: "the JSON says whether a repo on two computers shows as two projects", account: "u1", wantCode: 200, wantBody: `"projects_split_by_device":false`, wantAsked: "u1"},
		{name: "a request that isn't signed in gets 401 not_signed_in, with the session challenge, as GET /api/v1/users/me answers", wantCode: 401, wantBody: `{"error":"not_signed_in"}`, wantChallenge: `Session realm="pairtime"`},
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
			if got := rec.Header().Get("WWW-Authenticate"); got != tt.wantChallenge {
				t.Errorf("WWW-Authenticate = %q, want %q", got, tt.wantChallenge)
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

// TestAPIETag checks the snapshot's ETag, which the live dashboard's poll
// sends back as If-None-Match while its stream is quiet (WP-SV8).
func TestAPIETag(t *testing.T) {
	sample := demoView(t, demoNow)
	later := sample
	later.AsOf = demoNow.Add(time.Minute)
	changed := sample
	changed.Week.You.FinalS++
	tag := ETag(sample)
	tests := []struct {
		name string
		// v is the view the source answers, and account "" a request
		// that isn't signed in.
		v           View
		account     string
		ifNoneMatch []string
		wantCode    int
		// wantTag is the ETag the answer carries, "" for none.
		wantTag string
	}{
		{name: "a view comes with its weak ETag", v: sample, account: "u1", wantCode: 200, wantTag: tag},
		{name: "a poll whose If-None-Match names the view's ETag gets 304, with the ETag and no body", v: sample, account: "u1", ifNoneMatch: []string{tag}, wantCode: 304, wantTag: tag},
		{name: "the ETag leaves out when the view was read, so a later poll of the same numbers gets 304", v: later, account: "u1", ifNoneMatch: []string{tag}, wantCode: 304, wantTag: tag},
		{name: "a view whose numbers changed gets 200 with a new ETag", v: changed, account: "u1", ifNoneMatch: []string{tag}, wantCode: 200, wantTag: ETag(changed)},
		{name: "If-None-Match compares weakly, in a list or another header line", v: sample, account: "u1", ifNoneMatch: []string{`"other"`, `W/"x", ` + strings.TrimPrefix(tag, "W/")}, wantCode: 304, wantTag: tag},
		{name: "If-None-Match: * matches any view", v: sample, account: "u1", ifNoneMatch: []string{"*"}, wantCode: 304, wantTag: tag},
		{name: "a tag that isn't the view's gets the view", v: sample, account: "u1", ifNoneMatch: []string{`W/"0123"`}, wantCode: 200, wantTag: tag},
		{name: "a request that isn't signed in gets 401 whatever its If-None-Match", v: sample, ifNoneMatch: []string{"*"}, wantCode: 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asked string
			h := API(source{v: tt.v, asked: &asked},
				func(*http.Request) (string, error) {
					if tt.account == "" {
						return "", ErrNotSignedIn
					}
					return tt.account, nil
				},
				func() time.Time { return demoNow }, slog.New(slog.DiscardHandler))
			r := httptest.NewRequest(http.MethodGet, APIPath, nil)
			for _, v := range tt.ifNoneMatch {
				r.Header.Add("If-None-Match", v)
			}
			rec := httptest.NewRecorder()
			h(rec, r)
			if rec.Code != tt.wantCode {
				t.Fatalf("status %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Header().Get("ETag"); got != tt.wantTag {
				t.Errorf("ETag = %q, want %q", got, tt.wantTag)
			}
			if tt.wantTag != "" && !strings.HasPrefix(tt.wantTag, `W/"`) {
				t.Errorf("ETag %q isn't weak", tt.wantTag)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if tt.wantCode == 304 && rec.Body.Len() != 0 {
				t.Errorf("a 304 has a body: %q", rec.Body)
			}
		})
	}
}
