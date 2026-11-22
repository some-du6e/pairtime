// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/projectnames"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// namesOf decodes a names request's 200 answer.
func namesOf(t *testing.T, code int, body []byte) []projectnames.Name {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", code, body)
	}
	var a struct {
		Names []projectnames.Name `json:"names"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatal(err)
	}
	return a.Names
}

// TestNames checks POST /api/v1/projects/names (project-names-v1): what a
// device's names request stores and answers, the one counter it shares
// with uploads, and each refusal, after which nothing it sent stays.
func TestNames(t *testing.T) {
	set := func(pairs ...string) map[string]any {
		var items []any
		for i := 0; i < len(pairs); i += 2 {
			items = append(items, map[string]any{"project": pairs[i], "label": pairs[i+1]})
		}
		return map[string]any{"set": items}
	}
	engines(t, func(t *testing.T, engine store.Engine) {
		tests := []struct {
			name string
			run  func(t *testing.T, h *harness, d *testDevice)
		}{
			{name: "a set is stored, and the answer lists every name of the device in key order", run: func(t *testing.T, h *harness, d *testDevice) {
				rec := h.send(t, upload{dev: d, names: true, body: set("k2", "portfolio", "k1", "chip8-emulator")})
				if got, want := namesOf(t, rec.Code, rec.Body.Bytes()), []projectnames.Name{{Project: "k1", Label: "chip8-emulator"}, {Project: "k2", Label: "portfolio"}}; !reflect.DeepEqual(got, want) {
					t.Errorf("names %v, want %v", got, want)
				}
			}},
			{name: "an empty object is a read: it changes nothing and lists the names", run: func(t *testing.T, h *harness, d *testDevice) {
				h.send(t, upload{dev: d, names: true, body: set("k1", "chip8")})
				rec := h.send(t, upload{dev: d, names: true, json: []byte(`{}`)})
				if got := namesOf(t, rec.Code, rec.Body.Bytes()); !reflect.DeepEqual(got, []projectnames.Name{{Project: "k1", Label: "chip8"}}) {
					t.Errorf("names %v", got)
				}
			}},
			{name: "a delete drops the name at once, and the answer lists none, as [] rather than null", run: func(t *testing.T, h *harness, d *testDevice) {
				h.send(t, upload{dev: d, names: true, body: set("k1", "chip8")})
				rec := h.send(t, upload{dev: d, names: true, json: []byte(`{"delete":["k1"],"set":null}`)})
				if !strings.Contains(rec.Body.String(), `"names":[]`) || len(namesOf(t, rec.Code, rec.Body.Bytes())) != 0 {
					t.Errorf("answer %s, want no names", rec.Body)
				}
			}},
			{name: "a names request takes the device's one counter, so an upload at or below it is counter_stale", run: func(t *testing.T, h *harness, d *testDevice) {
				h.send(t, upload{dev: d, names: true, req: 7, body: set("k1", "chip8")})
				if s, c := refusalOf(t, h.send(t, upload{dev: d, req: 7, body: map[string]any{}})); s != http.StatusConflict || c != CodeCounterStale {
					t.Errorf("an upload at the names request's counter got %d %s, want 409 counter_stale", s, c)
				}
				if st := h.state(t, d); st.Req != 7 {
					t.Errorf("the state's req is %d, want 7", st.Req)
				}
			}},
			{name: "a names request at or below the counter an upload moved is counter_stale, and stores nothing", run: func(t *testing.T, h *harness, d *testDevice) {
				h.ok(t, upload{dev: d, req: 9, body: map[string]any{}})
				if s, c := refusalOf(t, h.send(t, upload{dev: d, names: true, req: 9, body: set("k1", "chip8")})); s != http.StatusConflict || c != CodeCounterStale {
					t.Errorf("got %d %s, want 409 counter_stale", s, c)
				}
				if got := namesOf(t, http.StatusOK, h.send(t, upload{dev: d, names: true, req: 10, json: []byte(`{}`)}).Body.Bytes()); len(got) != 0 {
					t.Errorf("names %v, want none", got)
				}
			}},
			{name: "an upload's signature never passes for a names request", run: func(t *testing.T, h *harness, d *testDevice) {
				r := h.request(t, upload{dev: d, body: set("k1", "chip8")})
				r.URL.Path = NamesPath
				if s, c := refusalOf(t, h.serve(t, r)); s != http.StatusUnauthorized || c != CodeSignatureInvalid {
					t.Errorf("got %d %s, want 401 signature_invalid", s, c)
				}
			}},
			{name: "a names signature never passes for an upload", run: func(t *testing.T, h *harness, d *testDevice) {
				r := h.request(t, upload{dev: d, names: true, body: map[string]any{}})
				r.URL.Path = BatchesPath
				if s, c := refusalOf(t, h.serve(t, r)); s != http.StatusUnauthorized || c != CodeSignatureInvalid {
					t.Errorf("got %d %s, want 401 signature_invalid", s, c)
				}
			}},
			{name: "the answer gives each key the device uploaded the dashboard's code, worked out over all the account's projects", run: func(t *testing.T, h *harness, d *testDevice) {
				// The device's key and another device's key of the same
				// account start alike, so both codes are a digit longer.
				h.exec(t, `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id) VALUES ($1, $2, 'g7c41e9a2d05b8f63e1a94c7d2b6f0e85', 'P1')`, d.Account, d.ID)
				h.exec(t, `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id) VALUES ($1, 'd_desktop', 'g7c41e9b6c18a2f05d3e7b94a1c6d0f28', 'P2')`, d.Account)
				rec := h.send(t, upload{dev: d, names: true, json: []byte(`{}`)})
				var a struct {
					Codes map[string]string `json:"codes"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil || !reflect.DeepEqual(a.Codes, map[string]string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85": "7c41e9a"}) {
					t.Errorf("codes %v (%v), want this device's key alone, with its lengthened code: %s", a.Codes, err, rec.Body)
				}
			}},
			{name: "a device the server has no minutes from gets no codes, as {} rather than null", run: func(t *testing.T, h *harness, d *testDevice) {
				rec := h.send(t, upload{dev: d, names: true, json: []byte(`{}`)})
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"codes":{}`) {
					t.Errorf("answer %d %s, want codes {}", rec.Code, rec.Body)
				}
			}},
			{name: "a device with names under another account is refused with device_mismatch", run: func(t *testing.T, h *harness, d *testDevice) {
				other := h.device("d_other", false)
				h.exec(t, `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms) VALUES ($1, 'k9', $2, 'theirs', 0)`, d.ID, other.Account)
				if s, c := refusalOf(t, h.send(t, upload{dev: d, names: true, body: set("k1", "chip8")})); s != http.StatusForbidden || c != CodeDeviceMismatch {
					t.Errorf("got %d %s, want 403 device_mismatch", s, c)
				}
			}},
			{name: "a set that would leave the device more than 1000 names is too_many_names, and nothing of it stays", run: func(t *testing.T, h *harness, d *testDevice) {
				if err := h.writer.Do(context.Background(), projectnames.MaxPerDevice, func(ctx context.Context, tx writer.Tx) error {
					for i := range projectnames.MaxPerDevice {
						if _, err := tx.ExecContext(ctx, `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms) VALUES ($1, $2, $3, 'x', 0)`,
							d.ID, fmt.Sprintf("k%04d", i), d.Account); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if s, c := refusalOf(t, h.send(t, upload{dev: d, names: true, body: set("k_new", "one more")})); s != http.StatusRequestEntityTooLarge || c != CodeTooManyNames {
					t.Errorf("got %d %s, want 413 too_many_names", s, c)
				}
				var n int
				if err := h.db.Read.QueryRow(`SELECT COUNT(*) FROM project_names WHERE device_id = $1`, d.ID).Scan(&n); err != nil || n != projectnames.MaxPerDevice {
					t.Errorf("%d names, %v; want %d", n, err, projectnames.MaxPerDevice)
				}
				if st := h.state(t, d); st.Req != 0 {
					t.Errorf("the refused request moved the counter to %d", st.Req)
				}
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				h := newHarness(t, engine)
				tt.run(t, h, h.device("d_names", false))
			})
		}
	})
}

// TestNamesRefusals checks project-names-v1 1.5's refusals of a body, each
// of which stores nothing and moves no counter.
func TestNamesRefusals(t *testing.T) {
	many := make([]string, MaxNameItems+1)
	for i := range many {
		many[i] = fmt.Sprintf("%q", fmt.Sprintf("k%d", i))
	}
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "a member that isn't set or delete is body_shape", body: `{"names":[]}`, status: 422, code: CodeBodyShape},
		{name: "a member twice is body_shape", body: `{"set":[],"set":[]}`, status: 422, code: CodeBodyShape},
		{name: "a set item with a member besides project and label is body_shape", body: `{"set":[{"project":"k1","label":"a","path":"/home"}]}`, status: 422, code: CodeBodyShape},
		{name: "a set item without its label is body_shape", body: `{"set":[{"project":"k1"}]}`, status: 422, code: CodeBodyShape},
		{name: "a label that isn't a string is body_shape", body: `{"set":[{"project":"k1","label":7}]}`, status: 422, code: CodeBodyShape},
		{name: "a delete entry that isn't a string is body_shape", body: `{"delete":[7]}`, status: 422, code: CodeBodyShape},
		{name: "a project that isn't a key is project_invalid", body: `{"set":[{"project":"src/chip8","label":"chip8"}]}`, status: 422, code: CodeProjectInvalid},
		{name: "a key in set and in delete is project_repeated", body: `{"set":[{"project":"k1","label":"chip8"}],"delete":["k1"]}`, status: 422, code: CodeProjectRepeated},
		{name: "an empty label is label_invalid", body: `{"set":[{"project":"k1","label":""}]}`, status: 422, code: CodeLabelInvalid},
		{name: "a label cleaning would trim is label_invalid, so the server never stores one the owner didn't see", body: `{"set":[{"project":"k1","label":" chip8"}]}`, status: 422, code: CodeLabelInvalid},
		{name: "a label with a bidirectional override is label_invalid", body: `{"set":[{"project":"k1","label":"chip8‮"}]}`, status: 422, code: CodeLabelInvalid},
		{name: "a label of 65 characters is label_invalid", body: `{"set":[{"project":"k1","label":"` + strings.Repeat("a", 65) + `"}]}`, status: 422, code: CodeLabelInvalid},
		{name: "more than 200 items is too_many_names", body: `{"delete":[` + strings.Join(many, ",") + `]}`, status: 413, code: CodeTooManyNames},
		{name: "a body that isn't an object is body_shape", body: `[]`, status: 422, code: CodeBodyShape},
	}
	engines(t, func(t *testing.T, engine store.Engine) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				h := newHarness(t, engine)
				d := h.device("d_refused", false)
				if s, c := refusalOf(t, h.send(t, upload{dev: d, names: true, json: []byte(tt.body)})); s != tt.status || c != tt.code {
					t.Errorf("got %d %s, want %d %s", s, c, tt.status, tt.code)
				}
				if st := h.state(t, d); st.Req != 0 {
					t.Errorf("the refused request moved the counter to %d", st.Req)
				}
				var n int
				if err := h.db.Read.QueryRow(`SELECT COUNT(*) FROM project_names`).Scan(&n); err != nil || n != 0 {
					t.Errorf("%d names, %v; want none", n, err)
				}
			})
		}
	})
}
