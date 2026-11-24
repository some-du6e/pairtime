// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/devlog"
	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// t0 is when the tests' clock starts: 2026-11-24 12:00 UTC.
var t0 = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// day is a day.
const day = 24 * time.Hour

// The test accounts. u1 and u2 are verified, and u2's eligibility is still
// to confirm, since Hack Club Auth didn't say it is eligible; u3 isn't
// verified.
const (
	u1 = "u1"
	u2 = "u2"
	u3 = "u3"
)

// newDB returns a migrated database on engine with the test accounts and
// their projects. u1's project p-app has a key on each of its computers,
// k_app on d1 and k_app_desk on d2, as WP-SV10's linking would leave them;
// p-web is d1's k_web and p-tool d2's k_tool. u2's p-other is d3's k_app,
// and u3's p-pending d4's k_x.
func newDB(t *testing.T, engine store.Engine) *store.DB {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct {
		id, status string
		ysws       any
	}{{u1, "verified", int64(1)}, {u2, "verified", nil}, {u3, "pending", nil}} {
		if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, slack_id, verification_status, ysws_eligible, created_at_ms, updated_at_ms, signed_in_at_ms)
VALUES ($1, $2, $3, $4, $5, 0, 0, 0)`, u.id, "sub-"+u.id, "U-"+u.id, u.status, u.ysws); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []struct{ account, device, key, project string }{
		{u1, "d1", "k_app", "p-app"}, {u1, "d2", "k_app_desk", "p-app"}, {u1, "d1", "k_web", "p-web"}, {u1, "d2", "k_tool", "p-tool"},
		{u2, "d3", "k_app", "p-other"}, {u3, "d4", "k_x", "p-pending"},
	} {
		if _, err := db.Write.ExecContext(ctx, `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id) VALUES ($1, $2, $3, $4)`,
			p.account, p.device, p.key, p.project); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// in runs op in a transaction of its own, as the writer does, and returns
// its error.
func in(t *testing.T, db *store.DB, op writer.Op) error {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := op(ctx, tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return nil
}

// clock is the tests' clock, safe for the writer's goroutine to read.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// keyOf is device id's key in the tests.
func keyOf(id string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("pairtime submissions test device " + id))
	return ed25519.NewKeyFromSeed(seed[:])
}

// fakeDevices authenticates the tokens it maps, as *device.Store does,
// and fails on ptd_down, as a device store whose database is down does.
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

// devices are the tests' computers: u1's laptop d1 and desktop d2, u2's
// d3, u3's d4, a device of u1's with no key on record, and a removed one.
var devices = fakeDevices{
	"ptd_laptop":  {ID: "d1", Account: u1, PublicKey: keyOf("d1").Public().(ed25519.PublicKey)},
	"ptd_desktop": {ID: "d2", Account: u1, PublicKey: keyOf("d2").Public().(ed25519.PublicKey)},
	"ptd_other":   {ID: "d3", Account: u2, PublicKey: keyOf("d3").Public().(ed25519.PublicKey)},
	"ptd_pending": {ID: "d4", Account: u3, PublicKey: keyOf("d4").Public().(ed25519.PublicKey)},
	"ptd_keyless": {ID: "d5", Account: u1},
	"ptd_removed": {ID: "d6", Account: u1, Revoked: true},
}

// fakeSessions finds a session by the value of the "sess" cookie, as
// *auth.Service does by the session cookie.
type fakeSessions map[string]*accounts.Session

func (f fakeSessions) Session(r *http.Request) (*accounts.Session, error) {
	c, err := r.Cookie("sess")
	if err != nil {
		return nil, accounts.ErrNoSession
	}
	if c.Value == "down" {
		return nil, errors.New("accounts: the database is locked")
	}
	s, ok := f[c.Value]
	if !ok {
		return nil, accounts.ErrNoSession
	}
	return s, nil
}

// sessionsAt returns the tests' web sessions, by cookie value, for a clock
// at now: u1's with a fresh login a minute old, one 11 minutes old, and an
// ordinary sign-in; u2's and u3's with fresh logins.
func sessionsAt(now time.Time) fakeSessions {
	s := func(account string, fresh time.Duration) *accounts.Session {
		sess := &accounts.Session{User: accounts.User{ID: account}, CreatedAt: now.Add(-day), AuthTime: now.Add(-time.Minute)}
		if fresh > 0 {
			sess.FreshLoginAt = now.Add(-fresh)
		}
		return sess
	}
	return fakeSessions{
		"fresh":   s(u1, time.Minute),
		"edge":    s(u1, FreshLogin),
		"stale":   s(u1, FreshLogin+time.Minute),
		"plain":   s(u1, 0),
		"fresh2":  s(u2, time.Minute),
		"pending": s(u3, time.Minute),
	}
}

// programs are the tests' [[programs]]: two that take hours and
// calibration's, which takes none.
var programs = []Program{
	{Name: "hard-mode", DisplayName: "Hard Mode", TakesHours: true},
	{Name: "stardance", DisplayName: "Stardance", TakesHours: true},
	{Name: "calibration", DisplayName: "Calibration", TakesHours: false},
}

// fakeFingerprints gives each (account, device, key) the repo
// fingerprints it lists, as WP-SV10's store will.
type fakeFingerprints map[AccountKey][]string

func (f fakeFingerprints) Sharing(_ context.Context, _ Querier, account string, keys []Key) ([]AccountKey, error) {
	mine := make(map[string]bool)
	for _, k := range keys {
		for _, fp := range f[AccountKey{Account: account, Key: k}] {
			mine[fp] = true
		}
	}
	var out []AccountKey
	for ak, fps := range f {
		if ak.Account != account && slices.ContainsFunc(fps, func(fp string) bool { return mine[fp] }) {
			out = append(out, ak)
		}
	}
	slices.SortFunc(out, func(a, b AccountKey) int { return cmp.Or(cmp.Compare(a.Account, b.Account), compareKeys(a.Key, b.Key)) })
	return out, nil
}

// harness is a submissions API on a fresh database, with the tests'
// devices, sessions and programs, a clock at t0, and every write through a
// writer.
type harness struct {
	t     *testing.T
	db    *store.DB
	w     *writer.Writer
	clock *clock
	svc   *Service
	mux   *http.ServeMux
}

// newHarness returns a harness on engine. Each edit changes the options
// before the service starts. A program credential is the header
// X-Test-Program, naming the program, or "bad" for one that doesn't check
// out.
func newHarness(t *testing.T, engine store.Engine, edits ...func(*Options)) *harness {
	t.Helper()
	db := newDB(t, engine)
	w := writer.Start(db.Write, writer.Options{})
	t.Cleanup(w.Close)
	c := &clock{now: t0}
	scoring, paramsHash, err := Scoring("", "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	o := Options{
		Devices:    devices,
		Sessions:   sessionsAt(t0),
		SameOrigin: func(r *http.Request) bool { return r.Header.Get("Sec-Fetch-Site") != "cross-site" },
		Program: func(r *http.Request) (string, error) {
			switch p := r.Header.Get("X-Test-Program"); p {
			case "bad":
				return "", errors.New("program credential: unknown client")
			default:
				return p, nil
			}
		},
		Programs:       programs,
		Writer:         w,
		Read:           db.Read,
		Now:            c.Now,
		BaseURL:        "https://pairtime.example.org",
		ScoringVersion: scoring,
		Params:         paramsHash,
		Rate:           ingest.NewLimiter(10000, time.Millisecond),
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, edit := range edits {
		edit(&o)
	}
	svc, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.Register(mux)
	return &harness{t: t, db: db, w: w, clock: c, svc: svc, mux: mux}
}

// request builds a request from as: a device's token, such as ptd_laptop,
// which signs it with the device's key; "sess:<name>", a web session from
// this server's own pages; "prog:<name>", a program's credential; or "",
// no credential.
func (h *harness) request(as, method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	switch {
	case strings.HasPrefix(as, "ptd_"):
		dev := devices[as]
		r.Header.Set("Authorization", "Bearer "+as)
		sign(r, dev.ID, keyOf(dev.ID), h.clock.Now(), body)
	case strings.HasPrefix(as, "sess:"):
		r.AddCookie(&http.Cookie{Name: "sess", Value: strings.TrimPrefix(as, "sess:")})
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	case strings.HasPrefix(as, "prog:"):
		r.Header.Set("X-Test-Program", strings.TrimPrefix(as, "prog:"))
	}
	return r
}

// sign sets the headers a device's request carries, signed with key at now
// (docs/spec/submissions-v1.md 2).
func sign(r *http.Request, deviceID string, key ed25519.PrivateKey, now time.Time, body string) {
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	sum := sha256.Sum256([]byte(body))
	bodySHA := hex.EncodeToString(sum[:])
	r.Header.Set(ingest.HeaderDevice, deviceID)
	r.Header.Set(ingest.HeaderTime, ts)
	r.Header.Set(ingest.HeaderBodySHA256, bodySHA)
	sig := ed25519.Sign(key, Message(deviceID, r.Method, r.RequestURI, ts, bodySHA))
	r.Header.Set(ingest.HeaderSignature, "ed25519="+base64.StdEncoding.EncodeToString(sig))
}

// serve answers r.
func (h *harness) serve(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, r)
	return rec
}

// call sends a request from as and returns the answer.
func (h *harness) call(as, method, target, body string) *httptest.ResponseRecorder {
	return h.serve(h.request(as, method, target, body))
}

// view decodes a teen view, or fails the test.
func view(t *testing.T, rec *httptest.ResponseRecorder) TeenView {
	t.Helper()
	var v TeenView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%s isn't a submission: %v", rec.Body.String(), err)
	}
	return v
}

// refusalBody decodes a refusal, or fails the test.
func refusalBody(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error == "" {
		t.Fatalf("%d %s isn't a refusal: %v", rec.Code, rec.Body.String(), err)
	}
	return e
}

// want checks that rec has status code and, for a refusal, the error
// code, and fails the test otherwise.
func want(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body.String(), status, code)
	}
	if code != "" {
		if e := refusalBody(t, rec); e.Error != code {
			t.Fatalf("got %s (%s), want %s", e.Error, e.Detail, code)
		}
	}
}

// ts writes t as the API reads it.
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// createBody is a create's JSON: program and request id, projects by id
// or, with byKey, by the calling device's keys, and the window from start
// to end, or devlog windows when both are zero.
func createBody(program, requestID string, byKey bool, projects []string, start, end time.Time) string {
	b := map[string]any{"program": program, "request_id": requestID}
	if byKey {
		b["project_keys"] = projects
	} else {
		b["projects"] = projects
	}
	if start.IsZero() && end.IsZero() {
		b["devlog_windows"] = true
	} else {
		b["window"] = map[string]string{"start": ts(start), "end": ts(end)}
	}
	data, _ := json.Marshal(b)
	return string(data)
}

// window is a create of p-app by the laptop's key to program over [start,
// end], with request id id.
func window(program, id string, start, end time.Time) string {
	return createBody(program, id, true, []string{"k_app"}, start, end)
}

// devlogs is a devlog-mode create of projects by id.
func devlogs(program, id string, projects ...string) string {
	return createBody(program, id, false, projects, time.Time{}, time.Time{})
}

// create sends a create from as and returns the submission it answered
// with, failing the test unless the status is status.
func (h *harness) create(as, body string, status int) TeenView {
	h.t.Helper()
	rec := h.call(as, "POST", SubmissionsPath, body)
	want(h.t, rec, status, "")
	return view(h.t, rec)
}

// move moves submission id through states, one Move each, at the clock's
// time, through the writer.
func (h *harness) move(id string, states ...State) {
	h.t.Helper()
	err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		for _, s := range states {
			if err := Move(ctx, tx, id, s, h.clock.Now().UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// publish publishes a devlog of the account's device on key with id, as
// the server received it at.
func (h *harness) publish(account, dev, key, id string, at time.Time) {
	h.t.Helper()
	err := h.w.Do(context.Background(), 2, func(ctx context.Context, tx writer.Tx) error {
		_, _, err := devlog.Publish(ctx, tx, account, dev, devlog.Devlog{ID: id, ProjectKey: key, Markdown: "# " + id, AIAssisted: false}, at.UnixMilli())
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// unpublish deletes the account's devlog id at at.
func (h *harness) unpublish(account, id string, at time.Time) {
	h.t.Helper()
	err := h.w.Do(context.Background(), 2, func(ctx context.Context, tx writer.Tx) error {
		return devlog.Delete(ctx, tx, account, id, at.UnixMilli())
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// minute stores the account's device's revision of the minute at, as
// rollup_minutes keeps it, crediting each of slots' projects 15 s at level
// A; "" credits nothing.
func (h *harness) minute(account, dev string, at time.Time, slots ...string) {
	h.t.Helper()
	type slot struct {
		P  string `json:"p,omitempty"`
		MS int64  `json:"ms,omitempty"`
		K  string `json:"k,omitempty"`
		EV string `json:"ev,omitempty"`
	}
	var s []slot
	for _, p := range slots {
		if p == "" {
			s = append(s, slot{})
			continue
		}
		s = append(s, slot{P: p, MS: 15000, K: "prompting", EV: "A"})
	}
	data, err := json.Marshal(s)
	if err != nil {
		h.t.Fatal(err)
	}
	err = h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO rollup_minutes (user_id, minute_ms, device_id, rev, state, slots, agent_only, day)
VALUES ($1, $2, $3, 0, 'final', $4, '{}', $5)`, account, at.UnixMilli(), dev, string(data), at.UTC().Format(time.DateOnly))
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// rows counts the rows of every table of the package, to show that a
// refused request stored nothing.
func (h *harness) rows() string {
	h.t.Helper()
	var out []string
	for _, table := range []string{"submissions", "submission_projects", "submission_keys", "submission_windows", "submission_events", "submission_flags"} {
		var n int
		if err := h.db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %d", table, n))
	}
	return strings.Join(out, ", ")
}

// windowsOfView returns v's windows as [start, end] pairs.
func windowsOfView(v TeenView) [][2]time.Time {
	out := make([][2]time.Time, len(v.Windows))
	for i, w := range v.Windows {
		out[i] = [2]time.Time{w.Start, w.End}
	}
	return out
}

// spans writes windows as start..end, for messages.
func spans(ws [][2]time.Time) string {
	var parts []string
	for _, w := range ws {
		parts = append(parts, w[0].Format(time.RFC3339)+".."+w[1].Format(time.RFC3339))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
