// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/deploy"
	"github.com/some-du6e/pairtime/internal/server/backup"
	serverkeys "github.com/some-du6e/pairtime/internal/server/keys"
	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
	"github.com/some-du6e/pairtime/internal/server/ops"
	"github.com/some-du6e/pairtime/internal/server/publisher"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// deployed is a server's surroundings, faked for doctor: the buckets,
// /readyz, the tunnel, HCA, GitHub's API, three push monitors, and the
// files of a deploy. Each field can be changed before doctor runs.
type deployed struct {
	s3       *objstoretest.Server
	monitors *monitor
	config   string
	dataDir  string
	witness  string
	environ  []string
	// readiness is what /readyz answers.
	readiness ops.Readiness
	// tunnel answers /healthz at base_url.
	tunnel func(w http.ResponseWriter)
	// commitAt is the witness repository's latest commit; zero means it
	// has none.
	commitAt time.Time
	github   string
	// keys are the keys in dataDir.
	keys *backup.Keys
	// diskSpace, when set, stands in for the data disk's free space.
	diskSpace func(string) (free, total uint64, ok bool, err error)
	// hcaToken, when set, is HCA's token endpoint's answer to the right
	// client id and secret; unset, it gives a token.
	hcaToken func(w http.ResponseWriter)
}

func newDeployed(t *testing.T) *deployed {
	t.Helper()
	d := &deployed{
		s3:       objstoretest.New(t, "pairtime-data", "pairtime-snapshots"),
		monitors: newMonitor(t),
		dataDir:  privateDir(t),
		witness:  t.TempDir(),
		readiness: ops.Readiness{Ready: true, Checks: []ops.CheckResult{
			{Name: "database", OK: true}, {Name: "object_storage.bucket", OK: true}, {Name: "replication", OK: true}}},
		tunnel:   func(w http.ResponseWriter) { w.Write([]byte("ok\n")) },
		commitAt: time.Now().Add(-time.Minute),
	}
	var err error
	if d.keys, err = backup.Generate(backup.KeysDir(d.dataDir), rand.Reader); err != nil {
		t.Fatal(err)
	}
	// The pilot's rules: R2's default one on the main bucket, which only
	// aborts unfinished uploads, and the 30-day expiry of snapshots.
	d.s3.SetLifecycle("pairtime-data", r2AbortRule)
	d.s3.SetLifecycle("pairtime-snapshots", snapshotRule(30))

	readyz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !d.readiness.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		fmt.Fprint(w, readinessJSON(d.readiness))
	}))
	t.Cleanup(readyz.Close)
	tunnel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" || !strings.HasPrefix(r.UserAgent(), "pairtimed-doctor/") {
			http.Error(w, "unexpected", http.StatusTeapot)
			return
		}
		d.tunnel(w)
	}))
	t.Cleanup(tunnel.Close)
	var hcaURL string
	hca := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/oauth/token" {
			// HCA's token endpoint, for the one client_credentials grant.
			if r.FormValue("grant_type") != "client_credentials" || r.FormValue("client_id") != "app-123" || r.FormValue("client_secret") != "hca-secret" || !strings.HasPrefix(r.UserAgent(), "pairtimed-doctor/") {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"error":"invalid_client"}`)
				return
			}
			if d.hcaToken != nil {
				d.hcaToken(w)
				return
			}
			fmt.Fprint(w, `{"access_token":"never-shown","token_type":"Bearer","expires_in":3600}`)
			return
		}
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":"%s/oauth/authorize","token_endpoint":"%s/oauth/token","jwks_uri":"%s/oauth/discovery/keys","grant_types_supported":["authorization_code","client_credentials"],"token_endpoint_auth_methods_supported":["client_secret_post"]}`, hcaURL, hcaURL, hcaURL, hcaURL)
	}))
	hca.Start()
	hcaURL = hca.URL
	t.Cleanup(hca.Close)
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/some-du6e/pairtime-witness/commits" || r.Header.Get("Authorization") != "" {
			http.NotFound(w, r)
			return
		}
		if d.commitAt.IsZero() {
			http.Error(w, `{"message":"Git Repository is empty."}`, http.StatusConflict)
			return
		}
		fmt.Fprintf(w, `[{"sha":"0123abcd","commit":{"committer":{"date":%q}}}]`, d.commitAt.UTC().Format(time.RFC3339))
	}))
	t.Cleanup(github.Close)
	d.github = github.URL

	dbPath := filepath.Join(d.dataDir, "pairtime.db")
	dir := t.TempDir()
	d.config = filepath.Join(dir, "pairtime.toml")
	writeFile(t, d.config, fmt.Sprintf(`base_url = %q
listen = "127.0.0.1:0"
ops_listen = %q
data_dir = %q
[hca]
client_id = "app-123"
issuer = %q
[object_storage]
endpoint = %q
bucket = "pairtime-data"
snapshot_bucket = "pairtime-snapshots"
`, tunnel.URL, strings.TrimPrefix(readyz.URL, "http://"), d.dataDir, hca.URL, d.s3.URL), 0o644)
	writeFile(t, filepath.Join(dir, witnessKey), "a deploy key\n", 0o600)
	writeFile(t, filepath.Join(dir, "litestream.yml"), strings.Replace(deploy.LitestreamYAML, "path: /data/pairtime.db", "path: "+dbPath, 1), 0o644)
	d.environ = []string{
		"PAIRTIME_S3_ACCESS_KEY_ID=" + objstoretest.AccessKeyID,
		"PAIRTIME_S3_SECRET_ACCESS_KEY=" + objstoretest.SecretAccessKey,
		"PAIRTIME_S3_ENDPOINT=" + d.s3.URL,
		"PAIRTIME_S3_REGION=auto",
		"PAIRTIME_S3_BUCKET=pairtime-data",
		"PAIRTIME_HCA_CLIENT_SECRET=hca-secret",
		"PAIRTIME_EMAIL_KEY=" + doctorEmailKey,
		"PAIRTIME_HEARTBEAT_URL=" + d.monitors.URL + "/pairtimed",
		"PAIRTIME_SNAPSHOT_HEARTBEAT_URL=" + d.monitors.URL + "/snapshot",
		"PAIRTIME_LITESTREAM_HEARTBEAT_URL=" + d.monitors.URL + "/litestream",
		"PAIRTIME_WITNESS_REPO=some-du6e/pairtime-witness",
	}

	// A migrated database, a snapshot and a key backup taken just now, and
	// a day root the repository already holds.
	createDatabase(t, dbPath)
	e, _, stderr := testEnv(d.environ...)
	if code := run(context.Background(), []string{"migrate", "-config", d.config}, e); code != 0 {
		t.Fatalf("migrate: %s", stderr)
	}
	snaps := objstore.NewSnapshots(mustBucket(t, d.s3, "pairtime-snapshots"))
	if _, err := snaps.Put(context.Background(), time.Now(), bytes.NewReader([]byte("age-encryption.org/v1\n"))); err != nil {
		t.Fatal(err)
	}
	d.s3.SetObject("pairtime-data", backup.KeyBackupPrefix+backup.KeyBackupName(time.Now(), d.keys.Public(), doctorEmailKey), []byte("armored"))
	root := filepath.Join(d.witness, "2026", "2026-11-19.json")
	writeFile(t, root, "{}\n", 0o644)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(root, old, old); err != nil {
		t.Fatal(err)
	}
	return d
}

// write runs op through a writer on the fake deploy's database, with the
// database's read pool, and closes both.
func (d *deployed) write(t *testing.T, op func(ctx context.Context, db *store.DB, w *writer.Writer) error) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Engine: store.SQLite, Path: filepath.Join(d.dataDir, "pairtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	defer w.Close()
	if err := op(ctx, db, w); err != nil {
		t.Fatal(err)
	}
}

// witnessRows stores a witness row of the fake deploy's server key at each
// time, as ingest does, and records the key's validity, as serve does.
func (d *deployed) witnessRows(t *testing.T, times ...time.Time) {
	t.Helper()
	pub := d.keys.Public()
	d.write(t, func(ctx context.Context, _ *store.DB, w *writer.Writer) error {
		return w.Do(ctx, len(times)+2, func(ctx context.Context, tx writer.Tx) error {
			for i, at := range times {
				if _, err := tx.ExecContext(ctx, `INSERT INTO witness_rows (device_id, b, account_id, n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id) VALUES ($1, $2, 'u1', 1, $3, $3, $3, $4, $5, $6)`,
					"d_doctor", i+1, strings.Repeat("ab", 32), at.UnixMilli(), strings.Repeat("01", 64), pub.ServerKeyID()); err != nil {
					return err
				}
			}
			return serverkeys.RecordServer(ctx, tx, pub.Server, time.Now().UnixMilli())
		})
	})
}

// publish runs the day-root publisher at now, as serve does, with the
// files going to dir.
func (d *deployed) publish(t *testing.T, dir string, now time.Time) {
	t.Helper()
	pub := d.keys.Public()
	d.write(t, func(ctx context.Context, db *store.DB, w *writer.Writer) error {
		h, err := serverkeys.Load(ctx, db.Read)
		if err != nil {
			return err
		}
		_, err = publisher.Publish(ctx, publisher.Options{Writer: w, Read: db.Read, History: h,
			Keys: map[string]ed25519.PrivateKey{pub.ServerKeyID(): d.keys.Server}, Dir: dir}, now)
		return err
	})
}

// doctorEmailKey is the fake deploy's PAIRTIME_EMAIL_KEY.
var doctorEmailKey = strings.Repeat("ab", 32)

// setKeyBackups replaces the fake deploy's key backups with ones made at
// each time ago of each named key set, and returns their names.
func (d *deployed) setKeyBackups(t *testing.T, names ...string) {
	t.Helper()
	b := mustBucket(t, d.s3, "pairtime-data")
	for _, k := range d.s3.Keys("pairtime-data") {
		if strings.HasPrefix(k, backup.KeyBackupPrefix) {
			if err := b.Delete(context.Background(), k); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, n := range names {
		d.s3.SetObject("pairtime-data", backup.KeyBackupPrefix+n, []byte("armored"))
	}
}

// otherKeys are a key set other than the fake deploy's.
func otherKeys(t *testing.T) backup.Public {
	t.Helper()
	keys, err := backup.Generate(filepath.Join(privateDir(t), "keys"), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return keys.Public()
}

// r2AbortRule is the lifecycle rule R2 gives every new bucket: it aborts
// multipart uploads that never finished, and deletes no object.
const r2AbortRule = `<Rule><ID>Default Multipart Abort Rule</ID><Filter><Prefix></Prefix></Filter><Status>Enabled</Status><AbortIncompleteMultipartUpload><DaysAfterInitiation>7</DaysAfterInitiation></AbortIncompleteMultipartUpload></Rule>`

// snapshotRule is a lifecycle rule that deletes the snapshots after days.
func snapshotRule(days int) string {
	return fmt.Sprintf(`<Rule><ID>expire snapshots</ID><Filter><Prefix>snapshots/</Prefix></Filter><Status>Enabled</Status><Expiration><Days>%d</Days></Expiration></Rule>`, days)
}

func readinessJSON(r ops.Readiness) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"ready":%v,"checks":[`, r.Ready)
	for i, c := range r.Checks {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"name":%q,"ok":%v,"error":%q,"ms":1}`, c.Name, c.OK, c.Error)
	}
	b.WriteString("]}")
	return b.String()
}

func mustBucket(t *testing.T, srv *objstoretest.Server, name string) *objstore.Bucket {
	t.Helper()
	c, err := objstore.New(objstore.Config{Endpoint: srv.URL, Region: objstoretest.Region, Credentials: objstoretest.Credentials})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Bucket(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// doctorLines runs doctor and returns each check's status and detail by
// name, and the exit code.
func (d *deployed) run(t *testing.T, extra ...string) (map[string][2]string, int) {
	t.Helper()
	e, stdout, _ := testEnv(d.environ...)
	e.githubURL = d.github
	e.doctorWriteGap = time.Millisecond
	e.diskSpace = d.diskSpace
	args := append([]string{"doctor", "-config", d.config, "-witness-dir", d.witness,
		"-litestream-config", filepath.Join(filepath.Dir(d.config), "litestream.yml"), "-timeout", "10s"}, extra...)
	code := run(context.Background(), args, e)
	if strings.Contains(stdout.String(), "never-shown") || strings.Contains(stdout.String(), "hca-secret") {
		t.Error("doctor printed HCA's token or the client secret")
	}
	lines := make(map[string][2]string)
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] != "doctor:" {
			lines[f[1]] = [2]string{f[0], strings.Join(f[2:], " ")}
		}
	}
	if len(lines) == 0 {
		t.Fatalf("doctor printed no checks:\n%s", stdout)
	}
	return lines, code
}

func TestDoctor(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, d *deployed)
		args  []string
		// want maps a check to the status it must have and a part of its
		// detail. Every check not named must be ok, or, in a case that
		// passes -only or -skip, must not fail.
		want      map[string][2]string
		wantCode  int
		wantPings map[string]int
	}{
		{name: "a healthy deploy passes every check, and each push monitor gets one ping with doctor's User-Agent",
			wantPings: map[string]int{"/pairtimed": 1, "/snapshot": 1, "/litestream": 1}},
		{name: "a bucket with versioning on fails, since a purge would leave old versions",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetVersioning("pairtime-data", "Enabled") },
			want:  map[string][2]string{"bucket.pairtime-data.versioning": {statusFail, "versioning is Enabled"}}, wantCode: 1},
		{name: "a bucket with versioning suspended fails too, since it keeps the versions it made",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetVersioning("pairtime-snapshots", "Suspended") },
			want:  map[string][2]string{"bucket.pairtime-snapshots.versioning": {statusFail, "versioning is Suspended"}}, wantCode: 1},
		{name: "a store without GetBucketVersioning passes, as R2 does",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetVersioning("pairtime-data", objstoretest.NotImplemented) },
			want:  map[string][2]string{"bucket.pairtime-data.versioning": {statusOK, "doesn't implement GetBucketVersioning"}}},
		{name: "a key that may not ask about versioning is a warning naming -skip versioning, not a failure",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetVersioning("pairtime-data", objstoretest.AccessDenied) },
			want:  map[string][2]string{"bucket.pairtime-data.versioning": {statusWarn, "-skip versioning"}}},
		{name: "any other versioning answer doctor can't decide, such as a store error, is a warning too (D-evidence-versioning)",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetVersioning("pairtime-snapshots", objstoretest.InternalError) },
			want:  map[string][2]string{"bucket.pairtime-snapshots.versioning": {statusWarn, "InternalError"}}},
		{name: "a lifecycle rule that deletes objects in the main bucket fails, since it would delete the replica, the evidence and the key backups",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetLifecycle("pairtime-data", r2AbortRule+snapshotRule(30)) },
			want:  map[string][2]string{"bucket.pairtime-data.lifecycle": {statusFail, `rule "expire snapshots" deletes objects`}}, wantCode: 1},
		{name: "a main bucket without a lifecycle configuration passes",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetLifecycle("pairtime-data", "") },
			want:  map[string][2]string{"bucket.pairtime-data.lifecycle": {statusOK, "no lifecycle rules"}}},
		{name: "a snapshot bucket without a rule that deletes snapshots fails, since they would outlive the privacy notice's 30 days",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetLifecycle("pairtime-snapshots", r2AbortRule) },
			want:  map[string][2]string{"bucket.pairtime-snapshots.lifecycle": {statusFail, "within 30 days"}}, wantCode: 1},
		{name: "a snapshot rule that keeps them 60 days fails",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetLifecycle("pairtime-snapshots", snapshotRule(60)) },
			want:  map[string][2]string{"bucket.pairtime-snapshots.lifecycle": {statusFail, "within 30 days"}}, wantCode: 1},
		{name: "a disabled snapshot rule fails, since it deletes nothing",
			setup: func(_ *testing.T, d *deployed) {
				d.s3.SetLifecycle("pairtime-snapshots", strings.Replace(snapshotRule(30), "Enabled", "Disabled", 1))
			},
			want: map[string][2]string{"bucket.pairtime-snapshots.lifecycle": {statusFail, "no enabled rule"}}, wantCode: 1},
		{name: "a snapshot rule narrowed to tagged objects fails, since snapshots carry no tags",
			setup: func(_ *testing.T, d *deployed) {
				d.s3.SetLifecycle("pairtime-snapshots", `<Rule><ID>tagged</ID><Filter><And><Prefix>snapshots/</Prefix><Tag><Key>k</Key><Value>v</Value></Tag></And></Filter><Status>Enabled</Status><Expiration><Days>30</Days></Expiration></Rule>`)
			},
			want: map[string][2]string{"bucket.pairtime-snapshots.lifecycle": {statusFail, "no enabled rule"}}, wantCode: 1},
		{name: "a 30-day rule over the whole snapshot bucket passes",
			setup: func(_ *testing.T, d *deployed) {
				d.s3.SetLifecycle("pairtime-snapshots", strings.Replace(snapshotRule(30), "<Prefix>snapshots/</Prefix>", "<Prefix></Prefix>", 1))
			},
			want: map[string][2]string{"bucket.pairtime-snapshots.lifecycle": {statusOK, "after 30 days"}}},
		{name: "a store without lifecycle rules fails the snapshot bucket, which then keeps every snapshot, and passes the main bucket",
			setup: func(_ *testing.T, d *deployed) {
				d.s3.SetLifecycle("pairtime-data", objstoretest.NotImplemented)
				d.s3.SetLifecycle("pairtime-snapshots", objstoretest.NotImplemented)
			},
			want: map[string][2]string{"bucket.pairtime-data.lifecycle": {statusOK, "doesn't implement lifecycle rules"},
				"bucket.pairtime-snapshots.lifecycle": {statusFail, "doesn't implement lifecycle rules"}}, wantCode: 1},
		{name: "a key that may not read lifecycle rules is a warning naming -skip lifecycle, not a failure",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetLifecycle("pairtime-snapshots", objstoretest.AccessDenied) },
			want:  map[string][2]string{"bucket.pairtime-snapshots.lifecycle": {statusWarn, "-skip lifecycle"}}},
		{name: "a lifecycle answer doctor can't decide, such as a store error, is a warning",
			setup: func(_ *testing.T, d *deployed) { d.s3.SetLifecycle("pairtime-data", objstoretest.InternalError) },
			want:  map[string][2]string{"bucket.pairtime-data.lifecycle": {statusWarn, "InternalError"}}},
		{name: "a client secret HCA refuses fails the hca check, and the token never shows",
			setup: func(_ *testing.T, d *deployed) { d.environ = append(d.environ, "PAIRTIME_HCA_CLIENT_SECRET=wrong") },
			want:  map[string][2]string{"hca": {statusFail, "refused the client id or secret"}}, wantCode: 1},
		{name: "HCA's 400 invalid_scope to a grant that names no scope passes the hca check, since HCA checks the client id and secret first",
			setup: func(_ *testing.T, d *deployed) {
				d.hcaToken = func(w http.ResponseWriter) {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":"invalid_scope","error_description":"The requested scope is invalid, unknown, or malformed."}`)
				}
			},
			want: map[string][2]string{"hca": {statusOK, "took the client id and secret, and refused the grant's scope (invalid_scope)"}}},
		{name: "a 400 unauthorized_client passes the hca check too, since the client authenticated before the grant was refused",
			setup: func(_ *testing.T, d *deployed) {
				d.hcaToken = func(w http.ResponseWriter) {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":"unauthorized_client"}`)
				}
			},
			want: map[string][2]string{"hca": {statusOK, "may not use client_credentials"}}},
		{name: "any other answer from the token endpoint fails the hca check, naming it",
			setup: func(_ *testing.T, d *deployed) {
				d.hcaToken = func(w http.ResponseWriter) {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":"invalid_request"}`)
				}
			},
			want: map[string][2]string{"hca": {statusFail, "answered 400 invalid_request"}}, wantCode: 1},
		{name: "a store that takes a second write-once put fails",
			setup: func(_ *testing.T, d *deployed) { d.s3.WriteOnceIgnored("pairtime-snapshots") },
			want:  map[string][2]string{"bucket.pairtime-snapshots.write_once": {statusFail, "took a second If-None-Match"}}, wantCode: 1},
		{name: "a Cloudflare challenge at the tunnel is named as Cloudflare's, not the server's",
			setup: func(_ *testing.T, d *deployed) {
				d.tunnel = func(w http.ResponseWriter) {
					w.Header().Set("Cf-Mitigated", "challenge")
					w.WriteHeader(http.StatusForbidden)
				}
			},
			want: map[string][2]string{"tunnel": {statusFail, "Bot Fight Mode"}}, wantCode: 1},
		{name: "when the replica lags, doctor pings neither pairtimed's nor Litestream's monitor",
			setup: func(_ *testing.T, d *deployed) {
				d.readiness = ops.Readiness{Ready: false, Checks: []ops.CheckResult{{Name: "database", OK: true}, {Name: "replication", OK: false, Error: "behind"}}}
			},
			want: map[string][2]string{"readyz": {statusFail, "replication: behind"},
				"push.pairtimed": {statusSkip, "sends no ping"}, "push.litestream": {statusSkip, "sends no ping"}},
			wantCode: 1, wantPings: map[string]int{"/pairtimed": 0, "/snapshot": 1, "/litestream": 0}},
		{name: "with object storage on, a /readyz without a replication check fails, but pairtimed's monitor still gets its ping, as the heartbeat sends it",
			setup: func(_ *testing.T, d *deployed) {
				d.readiness = ops.Readiness{Ready: true, Checks: []ops.CheckResult{{Name: "database", OK: true}}}
			},
			want: map[string][2]string{"readyz": {statusFail, "without -litestream-replica"},
				"push.litestream": {statusSkip, "no replication check"}},
			wantCode: 1, wantPings: map[string]int{"/pairtimed": 1, "/litestream": 0}},
		{name: "with object storage off, a /readyz without a replication check is a skip with a note, and pairtimed's monitor gets its ping, as the heartbeat sends it",
			setup: func(t *testing.T, d *deployed) {
				d.readiness = ops.Readiness{Ready: true, Checks: []ops.CheckResult{{Name: "database", OK: true}}}
				text, err := os.ReadFile(d.config)
				if err != nil {
					t.Fatal(err)
				}
				off := string(text[:strings.Index(string(text), "[object_storage]")])
				writeFile(t, d.config, off, 0o644)
			},
			want: map[string][2]string{"readyz": {statusSkip, "object storage is off"},
				"push.pairtimed":  {statusOK, "pinged pairtimed's push monitor"},
				"push.litestream": {statusSkip, "no replication check"},
				"push.snapshot":   {statusSkip, "sends no ping"},
				// What object storage off costs a deployment, which these
				// checks already said.
				"object_storage": {statusFail, "is off"},
				"litestream.yml": {statusFail, "isn't object_storage.endpoint"}},
			wantCode: 1, wantPings: map[string]int{"/pairtimed": 1, "/litestream": 0, "/snapshot": 0}},
		{name: "one monitor shared by two parts fails",
			setup: func(_ *testing.T, d *deployed) {
				d.environ = append(d.environ, "PAIRTIME_LITESTREAM_HEARTBEAT_URL="+d.monitors.URL+"/pairtimed")
			},
			want: map[string][2]string{"push.pairtimed": {statusFail, "same URL"}, "push.snapshot": {statusFail, "same URL"}, "push.litestream": {statusFail, "same URL"}}, wantCode: 1},
		{name: "a litestream.yml that replicates another database fails",
			setup: func(t *testing.T, d *deployed) {
				writeFile(t, filepath.Join(filepath.Dir(d.config), "litestream.yml"), deploy.LitestreamYAML, 0o644)
			},
			want: map[string][2]string{"litestream.yml": {statusFail, "doesn't replicate database.path"}}, wantCode: 1},
		{name: "a bucket in secrets.env other than pairtime.toml's fails, since Litestream would replicate elsewhere",
			setup: func(_ *testing.T, d *deployed) { d.environ = append(d.environ, "PAIRTIME_S3_BUCKET=pairtime-other") },
			want:  map[string][2]string{"litestream.yml": {statusFail, "PAIRTIME_S3_BUCKET"}}, wantCode: 1},
		{name: "no snapshot yet fails, says how to take one, and leaves the snapshot's monitor alone",
			setup: func(t *testing.T, d *deployed) {
				b := mustBucket(t, d.s3, "pairtime-snapshots")
				for _, k := range d.s3.Keys("pairtime-snapshots") {
					if err := b.Delete(context.Background(), k); err != nil {
						t.Fatal(err)
					}
				}
			},
			want:     map[string][2]string{"snapshot": {statusFail, "snapshot take"}, "push.snapshot": {statusSkip, "sends no ping"}},
			wantCode: 1, wantPings: map[string]int{"/snapshot": 0}},
		{name: "a root the sidecar hasn't pushed after 15 minutes fails the witness check",
			setup: func(t *testing.T, d *deployed) {
				d.commitAt = time.Now().Add(-3 * time.Hour)
			},
			want: map[string][2]string{"witness": {statusFail, "isn't pushing"}}, wantCode: 1},
		{name: "a server with no witness rows has no day root to push yet, which passes, though the repository has no commits",
			setup: func(t *testing.T, d *deployed) {
				d.commitAt = time.Time{}
				if err := os.RemoveAll(filepath.Join(d.witness, "2026")); err != nil {
					t.Fatal(err)
				}
			},
			want: map[string][2]string{"witness": {statusOK, "no witness rows yet"}}},
		{name: "witness rows of a day that is due, with no day head stored, fail the witness check: the publisher isn't publishing",
			setup: func(t *testing.T, d *deployed) { d.witnessRows(t, time.Now().Add(-72*time.Hour)) },
			want:  map[string][2]string{"witness": {statusFail, "are due and have no day head"}}, wantCode: 1},
		{name: "witness rows of today alone have no day head due yet, which passes and says when the first is due",
			setup: func(t *testing.T, d *deployed) {
				d.witnessRows(t, time.Now())
				if err := os.RemoveAll(filepath.Join(d.witness, "2026")); err != nil {
					t.Fatal(err)
				}
			},
			want: map[string][2]string{"witness": {statusOK, "the first day root"}}},
		{name: "the newest day head with no file in the witness directory fails, since the sidecar can't push it",
			setup: func(t *testing.T, d *deployed) {
				d.witnessRows(t, time.Now().Add(-72*time.Hour))
				d.publish(t, t.TempDir(), time.Now())
			},
			want: map[string][2]string{"witness": {statusFail, "has no file at"}}, wantCode: 1},
		{name: "a day root's file that doesn't hold its stored head fails, since a published root never changes",
			setup: func(t *testing.T, d *deployed) {
				now := time.Now()
				d.witnessRows(t, now.Add(-72*time.Hour))
				d.publish(t, d.witness, now)
				last, _ := publisher.LastDue(now)
				writeFile(t, filepath.Join(d.witness, filepath.FromSlash(publisher.FileName(last))), "{}\n", 0o644)
			},
			want: map[string][2]string{"witness": {statusFail, "doesn't hold the stored head"}}, wantCode: 1},
		{name: "day roots the publisher wrote and the sidecar pushed pass, naming the newest day head",
			setup: func(t *testing.T, d *deployed) {
				d.witnessRows(t, time.Now().Add(-72*time.Hour))
				d.publish(t, d.witness, time.Now())
				old := time.Now().Add(-time.Hour)
				if err := filepath.WalkDir(d.witness, func(p string, e fs.DirEntry, err error) error {
					if err != nil || e.IsDir() {
						return err
					}
					return os.Chtimes(p, old, old)
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: map[string][2]string{"witness": {statusOK, "the newest day head"}}},
		{name: "a root pairtimed is still writing, under a dot name, isn't a root the sidecar failed to push, since the sidecar never publishes one",
			setup: func(t *testing.T, d *deployed) {
				d.commitAt = time.Now().Add(-30 * time.Minute)
				tmp := filepath.Join(d.witness, "2026", ".2026-11-20.json.tmp")
				writeFile(t, tmp, "{\"da", 0o644)
				written := time.Now().Add(-20 * time.Minute)
				if err := os.Chtimes(tmp, written, written); err != nil {
					t.Fatal(err)
				}
			},
			want: map[string][2]string{"witness": {statusOK, "2026-11-19.json, the newest root"}}},
		{name: "a key backup from before backups named their keys is stale, so it fails, though the object reads",
			setup: func(t *testing.T, d *deployed) {
				d.setKeyBackups(t, "pairtime-keys-"+time.Now().UTC().Format("20060102T150405Z")+".tar.age")
			},
			want: map[string][2]string{"keys_backup": {statusFail, "is stale"}}, wantCode: 1},
		{name: "a newest key backup of other keys fails, though an older one holds these",
			setup: func(t *testing.T, d *deployed) {
				d.setKeyBackups(t,
					backup.KeyBackupName(time.Now().Add(-time.Hour), d.keys.Public(), doctorEmailKey),
					backup.KeyBackupName(time.Now(), otherKeys(t), doctorEmailKey))
			},
			want: map[string][2]string{"keys_backup": {statusFail, "holds server key"}}, wantCode: 1},
		{name: "a key backup without the email key fails while PAIRTIME_EMAIL_KEY is set",
			setup: func(t *testing.T, d *deployed) {
				d.setKeyBackups(t, backup.KeyBackupName(time.Now(), d.keys.Public(), ""))
			},
			want: map[string][2]string{"keys_backup": {statusFail, "no email key, and PAIRTIME_EMAIL_KEY is set"}}, wantCode: 1},
		{name: "the newest key backup decides, so an older stale one beside a current one passes",
			setup: func(t *testing.T, d *deployed) {
				d.setKeyBackups(t, "pairtime-keys-20261101T000000Z.tar.age", backup.KeyBackupName(time.Now(), d.keys.Public(), doctorEmailKey))
			},
			want: map[string][2]string{"keys_backup": {statusOK, "holds these keys and the email key"}}},
		{name: "no key backup at all fails",
			setup: func(t *testing.T, d *deployed) { d.setKeyBackups(t) },
			want:  map[string][2]string{"keys_backup": {statusFail, "no key backup"}}, wantCode: 1},
		{name: "a snapshot bucket that refuses a multipart upload fails, since a nightly snapshot past 64 MiB takes one",
			setup: func(t *testing.T, d *deployed) {
				d.s3.FailMatching(5, func(r *http.Request) bool { return r.Method == http.MethodPost && r.URL.Query().Has("uploads") }, http.StatusForbidden, "AccessDenied", false)
			},
			want: map[string][2]string{"bucket.pairtime-snapshots.multipart": {statusFail, "multipart upload"}}, wantCode: 1},
		{name: "a disk with less room than the database is a warning, since the next snapshot won't fit",
			setup: func(t *testing.T, d *deployed) {
				d.diskSpace = func(string) (uint64, uint64, bool, error) { return 1 << 20, 20 << 30, true, nil }
			},
			want: map[string][2]string{"disk": {statusWarn, "too little for the next nightly snapshot"}}},
		{name: "a database past a third of its disk is a warning to grow it",
			setup: func(t *testing.T, d *deployed) {
				// A disk of 1 KiB, a third of which any SQLite file passes.
				d.diskSpace = func(string) (uint64, uint64, bool, error) { return 1 << 30, 1 << 10, true, nil }
			},
			want: map[string][2]string{"disk": {statusWarn, "past a third of the disk"}}},
		{name: "a missing deploy key fails the witness check",
			setup: func(t *testing.T, d *deployed) {
				if err := os.Remove(filepath.Join(filepath.Dir(d.config), witnessKey)); err != nil {
					t.Fatal(err)
				}
			},
			want: map[string][2]string{"witness": {statusFail, "no deploy key"}}, wantCode: 1},
		{name: "-only runs the named checks and leaves the rest out",
			args: []string{"-only", "config,keys"},
			want: map[string][2]string{"tunnel": {statusSkip, "-only"}, "readyz": {statusSkip, "-only"}, "push.pairtimed": {statusSkip, "-only"},
				"bucket.pairtime-data.write_once": {statusSkip, "-only"}},
			wantPings: map[string]int{"/pairtimed": 0}},
		{name: "-skip takes a check's last part, so write_once leaves out both buckets' writes",
			args: []string{"-skip", "write_once"},
			want: map[string][2]string{"bucket.pairtime-data.write_once": {statusSkip, "-skip"}, "bucket.pairtime-snapshots.write_once": {statusSkip, "-skip"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeployed(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}
			lines, code := d.run(t, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code %d, want %d; checks: %v", code, tt.wantCode, lines)
			}
			for name, got := range lines {
				want, named := tt.want[name]
				switch {
				case named && (got[0] != want[0] || !strings.Contains(got[1], want[1])):
					t.Errorf("%s: %s %q, want %s and %q", name, got[0], got[1], want[0], want[1])
				case !named && len(tt.args) == 0 && got[0] != statusOK:
					// A check that stopped checking says skip or warn,
					// and exits 0 like ok.
					t.Errorf("%s: %s %q, want ok", name, got[0], got[1])
				case !named && got[0] == statusFail:
					t.Errorf("%s failed: %s", name, got[1])
				}
			}
			for name := range tt.want {
				if _, ok := lines[name]; !ok {
					t.Errorf("doctor printed no %s check", name)
				}
			}
			for path, want := range tt.wantPings {
				if got := d.monitors.count(path); got != want {
					t.Errorf("%s got %d pings, want %d", path, got, want)
				}
			}
			for _, a := range d.monitors.agents {
				if !strings.HasPrefix(a, "pairtimed-doctor/") {
					t.Errorf("a ping's User-Agent is %q, not doctor's", a)
				}
			}
		})
	}
}
