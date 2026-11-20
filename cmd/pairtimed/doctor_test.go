// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/deploy"
	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
	"github.com/some-du6e/pairtime/internal/server/ops"
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
	if _, err := backup.Generate(backup.KeysDir(d.dataDir), rand.Reader); err != nil {
		t.Fatal(err)
	}

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
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":"%s/oauth/authorize","token_endpoint":"%s/oauth/token","jwks_uri":"%s/oauth/discovery/keys"}`, hcaURL, hcaURL, hcaURL, hcaURL)
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
		"PAIRTIME_EMAIL_KEY=" + strings.Repeat("ab", 32),
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
	d.s3.SetObject("pairtime-data", backup.KeyBackupPrefix+backup.KeyBackupName(time.Now()), []byte("armored"))
	root := filepath.Join(d.witness, "2026", "2026-11-19.json")
	writeFile(t, root, "{}\n", 0o644)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(root, old, old); err != nil {
		t.Fatal(err)
	}
	return d
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
	args := append([]string{"doctor", "-config", d.config, "-witness-dir", d.witness,
		"-litestream-config", filepath.Join(filepath.Dir(d.config), "litestream.yml"), "-timeout", "10s"}, extra...)
	code := run(context.Background(), args, e)
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
		// detail; every check not named must be ok.
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
		{name: "a repository with no commits and no roots yet is a skip that names WP-SV17",
			setup: func(t *testing.T, d *deployed) {
				d.commitAt = time.Time{}
				if err := os.RemoveAll(filepath.Join(d.witness, "2026")); err != nil {
					t.Fatal(err)
				}
			},
			want: map[string][2]string{"witness": {statusSkip, "WP-SV17"}}},
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
