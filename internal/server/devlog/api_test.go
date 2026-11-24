// SPDX-License-Identifier: AGPL-3.0-or-later

package devlog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// fakeDevices authenticates the tokens it maps, as *device.Store does, and
// fails on ptd_down, as a device store whose database is down does.
type fakeDevices map[string]device.Device

func (f fakeDevices) Authenticate(_ context.Context, token string) (device.Device, error) {
	d, ok := f[token]
	switch {
	case token == "ptd_down":
		return device.Device{}, errors.New("device store: the database is locked")
	case !ok:
		return device.Device{}, device.ErrUnknownToken
	case d.Revoked:
		return d, device.ErrRevoked
	}
	return d, nil
}

// call is one request of a TestAPI case and what it must get.
type call struct {
	method, path, token string
	body                string
	wantCode            int
	// want is a substring of the response body.
	want string
	// markdown, when set, is the Markdown the response's devlog, or its
	// list's only devlog, must hold byte for byte.
	markdown string
	// wait moves the clock on before the call.
	wait time.Duration
	// retryAfter, when set, is the Retry-After the response must carry.
	retryAfter string
	// broken makes the body's read fail after it, as when the connection
	// breaks off.
	broken bool
}

// failingWriter is a writer whose every op fails, as one whose database
// takes no writes.
type failingWriter struct{}

func (failingWriter) Do(context.Context, int, writer.Op) error {
	return errors.New("writer: the database is locked")
}

// devlogBody is a publish's JSON body.
func devlogBody(id, markdown string) string {
	b, _ := json.Marshal(map[string]any{"id": id, "project": "p_one", "markdown": markdown, "ai_assisted": false})
	return string(b)
}

// TestAPI checks the devlog and badge routes over HTTP, for the device
// token of the account's own computers (docs/spec/teen-layer-v1.md).
func TestAPI(t *testing.T) {
	big := strings.Repeat("x", MaxMarkdown)
	tests := []struct {
		name  string
		calls []call
		// burst, when set, is each device's rate: burst requests, refilled
		// one every ingest.DefaultRateEvery.
		burst int
		// seed, when set, stores rows before the calls.
		seed func(t *testing.T, db *store.DB)
		// broken, when set, is what fails: "writer", every write, or
		// "read", the read pool.
		broken string
	}{
		{name: "a publish without a device token is refused with token_missing", calls: []call{
			{method: "POST", path: DevlogsPath, body: devlogBody("dl_1", "hi"), wantCode: 401, want: `"error":"token_missing"`},
		}},
		{name: "a removed device's token is refused with token_invalid", calls: []call{
			{method: "GET", path: DevlogsPath, token: "ptd_removed", wantCode: 401, want: `"error":"token_invalid"`},
		}},
		{name: "a published devlog answers 201 and reads back byte for byte", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", tricky), wantCode: 201, markdown: tricky},
			{method: "GET", path: DevlogsPath, token: "ptd_desktop", wantCode: 200, markdown: tricky},
		}},
		{name: "a retry of a publish answers 200 with what the server stored", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "first"), wantCode: 201},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "second"), wantCode: 200, markdown: "first"},
		}},
		{name: "a delete answers 204, the devlog is gone from every read, and its publish sent again from another computer gets 410", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "hi"), wantCode: 201},
			{method: "DELETE", path: DevlogsPath + "/dl_1", token: "ptd_desktop", wantCode: 204},
			{method: "GET", path: DevlogsPath, token: "ptd_laptop", wantCode: 200, want: `{"devlogs":[]}`},
			{method: "POST", path: DevlogsPath, token: "ptd_desktop", body: devlogBody("dl_1", "hi"), wantCode: 410, want: `"error":"removed"`},
		}},
		{name: "a delete of an id the server never held answers 204, and a publish of it after gets 410", calls: []call{
			{method: "DELETE", path: DevlogsPath + "/dl_never", token: "ptd_laptop", wantCode: 204},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_never", "hi"), wantCode: 410},
		}},
		{name: "Markdown of 64 KiB is taken", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", big), wantCode: 201},
		}},
		{name: "Markdown over 64 KiB is refused with 413", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", big+"x"), wantCode: 413, want: `"error":"markdown_too_large"`},
		}},
		{name: "a body larger than any devlog's is refused with 413", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: strings.Repeat(" ", maxBody) + devlogBody("dl_1", "hi"), wantCode: 413, want: `"error":"body_too_large"`},
		}},
		{name: "a body that isn't valid UTF-8 is refused with json_invalid", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: `{"id":"dl_1","project":"p_one","markdown":"` + "\xff" + `","ai_assisted":false}`, wantCode: 400, want: `"error":"json_invalid"`},
		}},
		{name: "a member the API doesn't know is refused", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: `{"id":"dl_1","project":"p_one","markdown":"hi","ai_assisted":false,"window":1}`, wantCode: 400, want: `"error":"body_shape"`},
		}},
		{name: "a publish without ai_assisted is refused", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: `{"id":"dl_1","project":"p_one","markdown":"hi"}`, wantCode: 400, want: `"error":"body_shape"`},
		}},
		{name: "an id that isn't a key is refused", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl/1", "hi"), wantCode: 400, want: `"error":"id_invalid"`},
		}},
		{name: "a devlog with no Markdown is refused", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", ""), wantCode: 400, want: `"error":"markdown_empty"`},
		}},
		{name: "a badge code that isn't on the list is refused", calls: []call{
			{method: "POST", path: BadgesPath, token: "ptd_laptop", body: `{"id":"bd_1","code":"ten_hours"}`, wantCode: 400, want: `"error":"code_unknown"`},
		}},
		{name: "Markdown holding a C0 control character other than tab, LF and CR is refused with bad_markdown", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "a\x00b"), wantCode: 400, want: `"error":"bad_markdown"`},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "a\x01b"), wantCode: 400, want: `"error":"bad_markdown"`},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "colors \x1b[31mred"), wantCode: 400, want: `"error":"bad_markdown"`},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "a\x1f"), wantCode: 400, want: `"error":"bad_markdown"`},
			{method: "GET", path: DevlogsPath, token: "ptd_laptop", wantCode: 200, want: `{"devlogs":[]}`},
		}},
		{name: "Markdown holding a backspace (U+0008) is refused with bad_markdown", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "a\x08b"), wantCode: 400, want: `"error":"bad_markdown"`},
		}},
		{name: "Markdown holding a vertical tab (U+000B) is refused with bad_markdown", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "a\x0bb"), wantCode: 400, want: `"error":"bad_markdown"`},
		}},
		{name: "Markdown holding a form feed (U+000C) is refused with bad_markdown", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "a\x0cb"), wantCode: 400, want: `"error":"bad_markdown"`},
		}},
		{name: "Markdown with tabs, line feeds, carriage returns and DEL is taken byte for byte", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "a\tb\nc\r\nd\x7f"), wantCode: 201, markdown: "a\tb\nc\r\nd\x7f"},
		}},
		{name: "Markdown holding a C1 character (U+0085) is taken and read back byte for byte", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "a\u0085b"), wantCode: 201, markdown: "a\u0085b"},
			{method: "GET", path: DevlogsPath, token: "ptd_desktop", wantCode: 200, markdown: "a\u0085b"},
		}},
		{name: "every route answers 503 with Retry-After: 5 when the device store fails", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_down", body: devlogBody("dl_1", "hi"), wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "GET", path: DevlogsPath, token: "ptd_down", wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "DELETE", path: DevlogsPath + "/dl_1", token: "ptd_down", wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "POST", path: BadgesPath, token: "ptd_down", body: `{"id":"bd_1","code":"comeback"}`, wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "GET", path: BadgesPath, token: "ptd_down", wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "DELETE", path: BadgesPath + "/bd_1", token: "ptd_down", wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
		}},
		{name: "a publish, a show, a delete and a removal answer 503 with Retry-After: 5 when the writer fails", broken: "writer", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "hi"), wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "POST", path: BadgesPath, token: "ptd_laptop", body: `{"id":"bd_1","code":"comeback"}`, wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "DELETE", path: DevlogsPath + "/dl_1", token: "ptd_laptop", wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "DELETE", path: BadgesPath + "/bd_1", token: "ptd_laptop", wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
		}},
		{name: "the devlogs and badges lists answer 503 with Retry-After: 5 when the read pool fails", broken: "read", calls: []call{
			{method: "GET", path: DevlogsPath, token: "ptd_laptop", wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
			{method: "GET", path: BadgesPath, token: "ptd_laptop", wantCode: 503, want: `"error":"unavailable"`, retryAfter: "5"},
		}},
		{name: "the devlogs list keeps <, > and & one byte each", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", tricky), wantCode: 201},
			{method: "GET", path: DevlogsPath, token: "ptd_laptop", wantCode: 200, want: `Fixed the ` + "`nil`" + ` map <b>at last</b> & \"quoted\" it`},
		}},
		{name: "a body whose read breaks off is refused with body_unreadable", calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: `{"id":"dl_1",`, broken: true, wantCode: 400, want: `"error":"body_unreadable"`},
		}},
		{name: "a device past its rate gets 429 rate_limited with Retry-After on each of the six routes, and its next request once the bucket refills", burst: 1, calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "hi"), wantCode: 201},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_2", "hi"), wantCode: 429, want: `"error":"rate_limited"`, retryAfter: "10"},
			{method: "GET", path: DevlogsPath, token: "ptd_laptop", wantCode: 429, want: `"error":"rate_limited"`, retryAfter: "10"},
			{method: "DELETE", path: DevlogsPath + "/dl_1", token: "ptd_laptop", wantCode: 429, want: `"error":"rate_limited"`, retryAfter: "10"},
			{method: "POST", path: BadgesPath, token: "ptd_laptop", body: `{"id":"bd_1","code":"comeback"}`, wantCode: 429, want: `"error":"rate_limited"`, retryAfter: "10"},
			{method: "GET", path: BadgesPath, token: "ptd_laptop", wantCode: 429, want: `"error":"rate_limited"`, retryAfter: "10"},
			{method: "DELETE", path: BadgesPath + "/bd_1", token: "ptd_laptop", wantCode: 429, want: `"error":"rate_limited"`, retryAfter: "10"},
			{method: "GET", path: DevlogsPath, token: "ptd_laptop", wait: 4 * time.Second, wantCode: 429, retryAfter: "6"},
			{method: "GET", path: DevlogsPath, token: "ptd_laptop", wait: 7 * time.Second, wantCode: 200, markdown: "hi"},
		}},
		{name: "each device of the account has a rate of its own", burst: 1, calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "hi"), wantCode: 201},
			{method: "GET", path: DevlogsPath, token: "ptd_desktop", wantCode: 200, markdown: "hi"},
			{method: "GET", path: DevlogsPath, token: "ptd_laptop", wantCode: 429, retryAfter: "10"},
		}},
		{name: "a publish that would take the account past its caps is refused with 409 limit_reached, and a held id's retry still answers 200", seed: func(t *testing.T, db *store.DB) {
			seedDevlogs(t, db, "u1", MaxDevlogs, 10)
		}, calls: []call{
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_new", "hi"), wantCode: 409, want: `"error":"limit_reached"`},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_seed_0", "hi"), wantCode: 200, want: `"id":"dl_seed_0"`},
		}},
		{name: "a new id of a code the account shows is refused with 409 already_shown, and the same id again answers 200", calls: []call{
			{method: "POST", path: BadgesPath, token: "ptd_laptop", body: `{"id":"bd_1","code":"comeback"}`, wantCode: 201},
			{method: "POST", path: BadgesPath, token: "ptd_desktop", body: `{"id":"bd_2","code":"comeback"}`, wantCode: 409, want: `"error":"already_shown"`},
			{method: "POST", path: BadgesPath, token: "ptd_desktop", body: `{"id":"bd_1","code":"comeback"}`, wantCode: 200, want: `"id":"bd_1"`},
			{method: "GET", path: BadgesPath, token: "ptd_laptop", wantCode: 200, want: `{"badges":[{"id":"bd_1","code":"comeback"}]}`},
		}},
		{name: "past 1,000 removal records a delete of a never-held id is refused with 409 limit_reached, and a held devlog's delete still answers 204", seed: func(t *testing.T, db *store.DB) {
			seedRemovals(t, db, "u1", MaxRemovals)
		}, calls: []call{
			{method: "DELETE", path: DevlogsPath + "/dl_never", token: "ptd_laptop", wantCode: 409, want: `"error":"limit_reached"`},
			{method: "DELETE", path: BadgesPath + "/bd_never", token: "ptd_laptop", wantCode: 409, want: `"error":"limit_reached"`},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "hi"), wantCode: 201},
			{method: "DELETE", path: DevlogsPath + "/dl_1", token: "ptd_laptop", wantCode: 204},
			{method: "POST", path: DevlogsPath, token: "ptd_laptop", body: devlogBody("dl_1", "hi"), wantCode: 410},
			{method: "DELETE", path: DevlogsPath + "/rm_seed_3", token: "ptd_laptop", wantCode: 204},
		}},
		{name: "a badge shows and lists, and after its removal its show sent again gets 410", calls: []call{
			{method: "POST", path: BadgesPath, token: "ptd_laptop", body: `{"id":"bd_1","code":"loop_caught"}`, wantCode: 201, want: `"code":"loop_caught"`},
			{method: "GET", path: BadgesPath, token: "ptd_desktop", wantCode: 200, want: `{"badges":[{"id":"bd_1","code":"loop_caught"}]}`},
			{method: "DELETE", path: BadgesPath + "/bd_1", token: "ptd_desktop", wantCode: 204},
			{method: "GET", path: BadgesPath, token: "ptd_laptop", wantCode: 200, want: `{"badges":[]}`},
			{method: "POST", path: BadgesPath, token: "ptd_laptop", body: `{"id":"bd_1","code":"loop_caught"}`, wantCode: 410, want: `"error":"removed"`},
		}},
	}
	devices := fakeDevices{
		"ptd_laptop":  {ID: "d1", Account: "u1"},
		"ptd_desktop": {ID: "d2", Account: "u1"},
		"ptd_removed": {ID: "d3", Account: "u1", Revoked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newDB(t, store.SQLite)
			if tt.seed != nil {
				tt.seed(t, db)
			}
			w := writer.Start(db.Write, writer.Options{})
			defer w.Close()
			now := at(10, 0)
			o := Options{
				Devices: devices, Writer: w, Read: db.Read,
				Now:    func() time.Time { return now },
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			switch tt.broken {
			case "writer":
				o.Writer = failingWriter{}
			case "read":
				if err := db.Read.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if tt.burst > 0 {
				o.Rate = ingest.NewLimiter(tt.burst, ingest.DefaultRateEvery)
			}
			mux := http.NewServeMux()
			New(o).Register(mux)
			for i, c := range tt.calls {
				now = now.Add(c.wait)
				var body io.Reader = strings.NewReader(c.body)
				if c.broken {
					body = io.MultiReader(body, iotest.ErrReader(errors.New("connection reset by peer")))
				}
				r := httptest.NewRequest(c.method, c.path, body)
				if c.token != "" {
					r.Header.Set("Authorization", "Bearer "+c.token)
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, r)
				got := rec.Body.String()
				if rec.Code != c.wantCode || !strings.Contains(got, c.want) {
					t.Fatalf("call %d, %s %s = %d %.300s; want %d with %s", i, c.method, c.path, rec.Code, got, c.wantCode, c.want)
				}
				if ra := rec.Header().Get("Retry-After"); ra != c.retryAfter {
					t.Errorf("call %d: Retry-After is %q, want %q", i, ra, c.retryAfter)
				}
				if c.markdown == "" {
					continue
				}
				var one Devlog
				var all struct{ Devlogs []Devlog }
				if c.method == "GET" {
					if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil || len(all.Devlogs) != 1 {
						t.Fatalf("call %d: %s doesn't hold one devlog (%v)", i, got, err)
					}
					one = all.Devlogs[0]
				} else if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
					t.Fatalf("call %d: %s isn't a devlog (%v)", i, got, err)
				}
				if one.Markdown != c.markdown || one.Device != "d1" || one.ProjectKey != "p_one" || !one.ReceivedAt.Equal(at(10, 0)) {
					t.Errorf("call %d: the devlog is %+v, want the Markdown byte for byte, from d1's p_one, received at 10:00", i, one)
				}
			}
		})
	}
}
