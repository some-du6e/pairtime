// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/privacy"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// audience is the aud the tests' server is registered under.
const audience = "pairtime-test"

// hub stands in for Hack Club's privacy hub: a P-256 key that signs
// tokens, published under kid.
type hub struct {
	key *ecdsa.PrivateKey
	kid string
	// made counts the tokens made, so each gets a jti of its own.
	made int
}

func newHub(t *testing.T) *hub {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &hub{key: k, kid: "test-2026-11"}
}

// brokenKid is a kid whose key set can't be fetched, as when the hub's
// key set answers 502.
const brokenKid = "broken-keys"

// Key gives the hub's key for its kid, as its key set does.
func (h *hub) Key(_ context.Context, kid string) (*ecdsa.PublicKey, error) {
	switch kid {
	case h.kid:
		return &h.key.PublicKey, nil
	case brokenKid:
		return nil, &privacy.Error{Status: http.StatusServiceUnavailable, Code: "jwks_unavailable", Err: errors.New("the hub's keys answered 502")}
	}
	return nil, &privacy.Error{Status: http.StatusUnauthorized, Code: "unknown_key"}
}

// header is the header the hub signs with.
func (h *hub) header() map[string]any {
	return map[string]any{"alg": "ES256", "typ": "JWT", "kid": h.kid}
}

// claims are a valid token's claims for action at now about subject,
// with a jti and a request_id of their own.
func (h *hub) claims(now time.Time, action string, subject map[string]any) map[string]any {
	h.made++
	return map[string]any{
		"iss":        privacy.Issuer,
		"aud":        audience,
		"iat":        now.Unix(),
		"exp":        now.Add(5 * time.Minute).Unix(),
		"jti":        fmt.Sprintf("01jti-%d-%s", h.made, action),
		"request_id": fmt.Sprintf("01req-%d", h.made),
		"action":     action,
		"dry_run":    false,
		"subject":    subject,
	}
}

// token signs claims under the hub's header.
func (h *hub) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	return sign(t, h.key, h.header(), claims)
}

// sign returns the compact JWS of header and claims, signed with key:
// ES256, the signature as R and S, 32 bytes each (RFC 7518 3.4).
func sign(t *testing.T, key *ecdsa.PrivateKey, header, claims any) string {
	t.Helper()
	return signRaw(t, key, marshal(t, header), marshal(t, claims))
}

// signRaw is sign for a header and claims given as their exact bytes.
func signRaw(t *testing.T, key *ecdsa.PrivateKey, header, claims []byte) string {
	t.Helper()
	in := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(in))
	der, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	var rs struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &rs); err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	rs.R.FillBytes(sig[:32])
	rs.S.FillBytes(sig[32:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// part is v as JSON in base64url without padding.
func part(t *testing.T, v any) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString(marshal(t, v))
}

// marshal is v as JSON.
func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// subject is a token's subject claim.
func subject(emails []string, slack, identity any) map[string]any {
	if emails == nil {
		emails = []string{}
	}
	return map[string]any{"emails": emails, "slack_id": slack, "identity_id": identity}
}

// lockedBuffer collects the service's log lines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// fixture is a migrated database, its writer, and the endpoints on a mux,
// on a clock the test sets.
type fixture struct {
	engine store.Engine
	db     *store.DB
	w      *writer.Writer
	emails *accounts.EmailCipher
	hub    *hub
	mux    *http.ServeMux
	logs   *lockedBuffer
	now    time.Time
}

func newFixture(t *testing.T, engine store.Engine) *fixture {
	t.Helper()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(context.Background(), db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	emails, err := accounts.NewEmailCipher(bytes.Repeat([]byte{7}, accounts.EmailKeySize))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{engine: engine, db: db, w: w, emails: emails, hub: newHub(t), mux: http.NewServeMux(), logs: &lockedBuffer{},
		now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)}
	svc, err := privacy.New(privacy.Options{
		Verifier: &privacy.Verifier{Audience: audience, Keys: f.hub},
		DB:       db,
		Writer:   w,
		Emails:   emails,
		Logger:   slog.New(slog.NewTextHandler(f.logs, nil)),
		Now:      func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Register(f.mux)
	return f
}

// call POSTs to path with token as its bearer token, or with no
// Authorization header when token is "", and returns the answer.
func (f *fixture) call(t *testing.T, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// ask makes a token for action about subj, dry or not, and calls its
// endpoint with it.
func (f *fixture) ask(t *testing.T, action string, subj map[string]any, dry bool) *httptest.ResponseRecorder {
	t.Helper()
	c := f.hub.claims(f.now, action, subj)
	c["dry_run"] = dry
	path := privacy.DeletePath
	if action == privacy.ActionExport {
		path = privacy.ExportPath
	}
	return f.call(t, path, f.hub.token(t, c))
}

// report decodes a 200's body, refusing a member Report lacks.
func report(t *testing.T, rec *httptest.ResponseRecorder) privacy.Report {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d %s, want 200", rec.Code, rec.Body.String())
	}
	var r privacy.Report
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("the report doesn't decode: %v\n%s", err, rec.Body.String())
	}
	return r
}

// exec runs one statement through the writer.
func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := f.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// person is an account the tests seed: its ids, and the device its rows
// came from.
type person struct {
	id, sub, slack, email, device string
}

var (
	teen  = person{id: "u-teen", sub: "ident!teen1", slack: "U0TEEN0001", email: "teen@example.org", device: "d-teen-1"}
	other = person{id: "u-other", sub: "ident!other1", slack: "U0OTHER001", email: "other@example.org", device: "d-other-1"}
)

// The days the tests' witness rows fall on: one whose tree both accounts'
// batches share, one that holds only the teen's, both published, and one
// not published yet.
var (
	sharedDay = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	aloneDay  = time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	openDay   = time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
)

// day is t's UTC day number, as day_leaves keys it.
func day(t time.Time) int64 { return t.UnixMilli() / 86_400_000 }

// seed stores rows of p in every table that holds an account's, one
// witness row on each of witness, and returns how many rows it stored by
// table.
func (f *fixture) seed(t *testing.T, p person, witness ...time.Time) map[string]int64 {
	t.Helper()
	at := f.now.Add(-time.Hour).UnixMilli()
	rows := []struct {
		table, q string
		args     []any
	}{
		// The account is a calibration volunteer's, so its row holds the
		// flag too (D-server-shadow), which goes with the row.
		{"users", `INSERT INTO users (id, hca_sub, slack_id, email_ct, email_index, verification_status, ysws_eligible, created_at_ms, updated_at_ms, signed_in_at_ms, calibrating_since_ms) VALUES ($1, $2, $3, $4, $5, 'verified', 1, $6, $6, $6, $6)`,
			[]any{p.id, p.sub, p.slack, []byte("sealed " + p.id), f.emails.Index(p.email), at}},
		{"sessions", `INSERT INTO sessions (id_hash, user_id, created_at_ms, expires_at_ms) VALUES ($1, $2, $3, $4)`, []any{"s-" + p.id, p.id, at, at + 86_400_000}},
		{"devices", `INSERT INTO devices (device_id, user_id, link_generation) VALUES ($1, $2, 1)`, []any{p.device, p.id}},
		{"device_codes", `INSERT INTO device_codes (device_code_hash, user_code_hash, run_id, device_id, os_family, client_build, public_key, status, user_id, created_at_ms, expires_at_ms, interval_s) VALUES ($1, $2, 'run', $3, 'linux', 'b1', $4, 'approved', $5, $6, $7, 5)`,
			[]any{"dc-a-" + p.id, "uc-a-" + p.id, p.device, []byte{1, 2, 3}, p.id, at, at + 600_000}},
		// A pending code: no account yet, found through the device.
		{"device_codes", `INSERT INTO device_codes (device_code_hash, user_code_hash, run_id, device_id, os_family, client_build, public_key, status, user_id, created_at_ms, expires_at_ms, interval_s) VALUES ($1, $2, 'run', $3, 'linux', 'b1', $4, 'pending', NULL, $5, $6, 5)`,
			[]any{"dc-p-" + p.id, "uc-p-" + p.id, p.device, []byte{4, 5, 6}, at, at + 600_000}},
		{"ingest_replies", `INSERT INTO ingest_replies (device_id, account_id, idempotency_key, body_sha256, response, received_at_ms) VALUES ($1, $2, 'key', 'sum', '{}', $3)`, []any{p.device, p.id, at}},
		{"minute_buckets", `INSERT INTO minute_buckets (device_id, minute_ms, account_id, rev, state, received_at_ms, bucket, revisions) VALUES ($1, $2, $3, 1, 'final', $2, '{}', 1)`, []any{p.device, at, p.id}},
		{"minute_bucket_revisions", `INSERT INTO minute_bucket_revisions (device_id, minute_ms, rev, account_id, received_at_ms, bucket) VALUES ($1, $2, 1, $3, $2, '{}')`, []any{p.device, at, p.id}},
		{"commit_records", `INSERT INTO commit_records (device_id, sha, account_id, project, body, first_seen_at_ms, updated_at_ms) VALUES ($1, 'abc123', $2, 'p1', '{}', $3, $3)`, []any{p.device, p.id, at}},
		{"device_labels", `INSERT INTO device_labels (device_id, account_id, labels, received_at_ms) VALUES ($1, $2, '{}', $3)`, []any{p.device, p.id, at}},
		{"request_origins", `INSERT INTO request_origins (id, device_id, account_id, ip, seen_at_ms) VALUES ($1, $2, $3, '192.0.2.1', $4)`, []any{"o-" + p.id, p.device, p.id, at}},
		{"rollup_projects", `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id) VALUES ($1, $2, 'pk1', 'p1')`, []any{p.id, p.device}},
		{"rollup_minutes", `INSERT INTO rollup_minutes (user_id, minute_ms, device_id, rev, state, slots, agent_only, day) VALUES ($1, $2, $3, 1, 'final', '[]', '[]', '2026-11-24')`, []any{p.id, at, p.device}},
		{"rollups_daily", `INSERT INTO rollups_daily (user_id, project_id, day, activity_kind) VALUES ($1, 'p1', '2026-11-24', 'coding')`, []any{p.id}},
		{"project_names", `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms) VALUES ($1, 'pk1', $2, 'my project', $3)`, []any{p.device, p.id, at}},
		{"project_name_hides", `INSERT INTO project_name_hides (device_id, project_key, user_id, hidden_at_ms) VALUES ($1, 'pk2', $2, $3)`, []any{p.device, p.id, at}},
		{"build_days", `INSERT INTO build_days (user_id, day, a_ms, b_ms, counted, decided_at_ms) VALUES ($1, '2026-11-23', 3600000, 0, 1, $2)`, []any{p.id, at}},
		{"build_goals", `INSERT INTO build_goals (user_id, goal, freeze_days, freezes_held, freezes_earned) VALUES ($1, 3, 0, 0, 0)`, []any{p.id}},
		{"build_weeks", `INSERT INTO build_weeks (user_id, week, goal, build_days, result, decided_at_ms) VALUES ($1, '2026-W47', 3, 3, 'met', $2)`, []any{p.id, at}},
		{"devlogs", `INSERT INTO devlogs (user_id, id, device_id, project_key, received_at_ms, markdown, ai_assisted) VALUES ($1, 'dl1', $2, 'pk1', $3, $4, 0)`, []any{p.id, p.device, at, []byte("# what I built")}},
		{"badges", `INSERT INTO badges (user_id, id, code) VALUES ($1, 'b1', 'first-build')`, []any{p.id}},
		{"removed_items", `INSERT INTO removed_items (user_id, id, removed_at_ms) VALUES ($1, 'dl0', $2)`, []any{p.id, at}},
		{"zone_changes", `INSERT INTO zone_changes (user_id, changed_at_ms, tz_before) VALUES ($1, $2, 'UTC')`, []any{p.id, at}},
	}
	for i, d := range witness {
		rows = append(rows, struct {
			table, q string
			args     []any
		}{"witness_rows", `INSERT INTO witness_rows (device_id, b, account_id, n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id) VALUES ($1, $2, $3, 1, 'root', 'head', 'prev', $4, 'sig', 'kid')`,
			[]any{p.device, int64(i + 1), p.id, d.Add(time.Duration(i+1) * time.Hour).UnixMilli()}})
	}
	counts := make(map[string]int64)
	for _, r := range rows {
		f.exec(t, r.q, r.args...)
		counts[r.table]++
	}
	return counts
}

// publish stores the day heads and leaves of days, as the publisher does
// once a day is over.
func (f *fixture) publish(t *testing.T, days ...time.Time) {
	t.Helper()
	for _, d := range days {
		f.exec(t, `INSERT INTO day_leaves (day, leaves) VALUES ($1, $2)`, day(d), make([]byte, 32))
		f.exec(t, `INSERT INTO day_heads (day, n, root, prev, head, signature, key_id, written_at_ms) VALUES ($1, 1, 'root', 'prev', 'head', 'sig', 'kid', $2)`, day(d), d.Add(24*time.Hour).UnixMilli())
	}
}

// tables lists the database's tables.
func (f *fixture) tables(t *testing.T) []string {
	t.Helper()
	q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`
	if f.engine == store.Postgres {
		q = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`
	}
	rows, err := f.db.Read.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// dump returns every row of every table but skip, each as text and in
// order, so two dumps are equal only when no row changed.
func (f *fixture) dump(t *testing.T, skip ...string) map[string][]string {
	t.Helper()
	out := make(map[string][]string)
	for _, table := range f.tables(t) {
		if contains(skip, table) {
			continue
		}
		rows, err := f.db.Read.QueryContext(context.Background(), `SELECT * FROM `+table)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			cells := make([]string, len(vals))
			for i, v := range vals {
				switch v := v.(type) {
				case nil:
					cells[i] = "NULL"
				case []byte:
					cells[i] = hex.EncodeToString(v)
				default:
					cells[i] = fmt.Sprint(v)
				}
			}
			lines = append(lines, strings.Join(cells, "|"))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(lines)
		out[table] = lines
	}
	return out
}

// mentions returns the rows of d that hold any of marks.
func mentions(d map[string][]string, marks ...string) map[string][]string {
	out := make(map[string][]string)
	for table, lines := range d {
		for _, l := range lines {
			for _, m := range marks {
				if strings.Contains(l, m) {
					out[table] = append(out[table], l)
					break
				}
			}
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
