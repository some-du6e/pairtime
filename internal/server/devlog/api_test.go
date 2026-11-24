// SPDX-License-Identifier: AGPL-3.0-or-later

package devlog

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// fakeDevices authenticates the tokens it maps, as *device.Store does.
type fakeDevices map[string]device.Device

func (f fakeDevices) Authenticate(_ context.Context, token string) (device.Device, error) {
	d, ok := f[token]
	switch {
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
			w := writer.Start(db.Write, writer.Options{})
			defer w.Close()
			mux := http.NewServeMux()
			New(Options{
				Devices: devices, Writer: w, Read: db.Read,
				Now:    func() time.Time { return at(10, 0) },
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}).Register(mux)
			for i, c := range tt.calls {
				r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
				if c.token != "" {
					r.Header.Set("Authorization", "Bearer "+c.token)
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, r)
				body := rec.Body.String()
				if rec.Code != c.wantCode || !strings.Contains(body, c.want) {
					t.Fatalf("call %d, %s %s = %d %.300s; want %d with %s", i, c.method, c.path, rec.Code, body, c.wantCode, c.want)
				}
				if c.markdown == "" {
					continue
				}
				var one Devlog
				var all struct{ Devlogs []Devlog }
				if c.method == "GET" {
					if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil || len(all.Devlogs) != 1 {
						t.Fatalf("call %d: %s doesn't hold one devlog (%v)", i, body, err)
					}
					one = all.Devlogs[0]
				} else if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
					t.Fatalf("call %d: %s isn't a devlog (%v)", i, body, err)
				}
				if one.Markdown != c.markdown || one.Device != "d1" || one.ProjectKey != "p_one" || !one.ReceivedAt.Equal(at(10, 0)) {
					t.Errorf("call %d: the devlog is %+v, want the Markdown byte for byte, from d1's p_one, received at 10:00", i, one)
				}
			}
		})
	}
}
