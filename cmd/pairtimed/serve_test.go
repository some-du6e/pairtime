// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/ops"
	"github.com/some-du6e/pairtime/internal/server/pages"
	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/internal/version"
)

// writeConfig writes a pairtime.toml for a test server that listens on
// free loopback ports and keeps its data in a temporary directory. It
// returns the file's path and the database's.
func writeConfig(t *testing.T) (configPath, dbPath string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "pairtime.toml")
	text := `base_url = "http://localhost:8080"
listen = "127.0.0.1:0"
ops_listen = "127.0.0.1:0"
data_dir = "data"
`
	if err := os.WriteFile(configPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, filepath.Join(dir, "data", "pairtime.db")
}

// addPrograms lists two programs in configPath's [[programs]]: one that
// takes hours, with takes_hours left out, and calibration's, which takes
// none.
func addPrograms(t *testing.T, configPath string) {
	t.Helper()
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("config_version = 2\n[[programs]]\nname = \"hard-mode\"\ndisplay_name = \"Hard Mode\"\n[[programs]]\nname = \"calibration\"\ndisplay_name = \"Calibration\"\ntakes_hours = false\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartupRefusesOldSQLite(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		wantStderr string
	}{
		{name: "3.51.2, the last release with the WAL-reset bug, is refused", version: "3.51.2", wantStderr: "older than 3.51.3"},
		{name: "3.50.7 is refused too: there is one floor, backports or not", version: "3.50.7", wantStderr: "older than 3.51.3"},
		{name: "a version that can't be read is refused", version: "3.51", wantStderr: "can't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, dbPath := writeConfig(t)
			e, _, stderr := testEnv()
			e.storeOptions = func(o *store.Options) { o.SQLiteVersion = tt.version }
			e.listening = func(public, ops string) {
				t.Errorf("serve listened on %s and %s with SQLite %s", public, ops, tt.version)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if code := run(ctx, []string{"serve", "-config", configPath}, e); code != 1 {
				t.Errorf("serve exit code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
			if _, err := os.Stat(dbPath); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the database file exists after the refusal (stat: %v); the check must come before the file is touched", err)
			}
		})
	}
	t.Run("3.51.3 is accepted", func(t *testing.T) {
		configPath, dbPath := writeConfig(t)
		createDatabase(t, dbPath)
		e, _, stderr := testEnv()
		e.storeOptions = func(o *store.Options) { o.SQLiteVersion = "3.51.3" }
		if code := run(context.Background(), []string{"migrate", "-config", configPath}, e); code != 0 {
			t.Errorf("migrate exit code = %d, want 0; stderr:\n%s", code, stderr.String())
		}
	})
}

// TestStartupRefusesScoringVersion checks D-sb1-submissions' start-up
// check (Q10): a freeze records the scoring version the server runs, so
// pairtimed doesn't start on a scoring_version newer than its built-in
// scorer's, or, before 1.0.0, on one with another MINOR, and stops before
// it touches the database.
func TestStartupRefusesScoringVersion(t *testing.T) {
	var major, minor, patch int
	if _, err := fmt.Sscanf(pages.BuiltinScoringVersion, "%d.%d.%d", &major, &minor, &patch); err != nil {
		t.Fatalf("the built-in scoring version %q: %v", pages.BuiltinScoringVersion, err)
	}
	tests := []struct {
		name, version, wantStderr string
	}{
		{name: "a PATCH newer than the built-in scorer's is refused", version: fmt.Sprintf("%d.%d.%d", major, minor, patch+1), wantStderr: "newer than this release's built-in scorer"},
		{name: "a later MAJOR is refused", version: fmt.Sprintf("%d.0.0", major+1), wantStderr: "newer than this release's built-in scorer"},
	}
	if major == 0 && minor > 0 {
		tests = append(tests, struct{ name, version, wantStderr string }{
			name: "before 1.0.0, an earlier MINOR is refused, since it may score by another rule", version: fmt.Sprintf("0.%d.0", minor-1), wantStderr: "another MINOR",
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, dbPath := writeConfig(t)
			f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString("scoring_version = \"" + tt.version + "\"\n"); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			e, _, stderr := testEnv()
			e.listening = func(public, ops string) {
				t.Errorf("serve listened on %s and %s with scoring_version %s", public, ops, tt.version)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if code := run(ctx, []string{"serve", "-config", configPath}, e); code != 1 {
				t.Errorf("serve exit code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
			if _, err := os.Stat(dbPath); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the database file exists after the refusal (stat: %v); the check must come before the database opens", err)
			}
		})
	}
}

func TestServe(t *testing.T) {
	configPath, dbPath := writeConfig(t)
	// The programs route lists only the program that takes hours, and serve
	// wires [[programs]] into the submissions API.
	addPrograms(t, configPath)
	e, _, stderr := testEnv()
	type addrs struct{ public, ops string }
	up := make(chan addrs, 1)
	e.listening = func(public, ops string) { up <- addrs{public, ops} }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan int, 1)
	go func() { exited <- run(ctx, []string{"serve", "-config", configPath}, e) }()
	var a addrs
	select {
	case a = <-up:
	case code := <-exited:
		t.Fatalf("serve exited with %d before listening; stderr:\n%s", code, stderr.String())
	case <-time.After(60 * time.Second):
		t.Fatalf("serve didn't start within a minute; stderr:\n%s", stderr.String())
	}
	public, private := "http://"+a.public, "http://"+a.ops

	requests := []struct {
		name     string
		method   string
		url      string
		wantCode int
		wantBody string // a regular expression the body must match
	}{
		{name: "the public listener answers /healthz", url: public + "/healthz", wantCode: 200, wantBody: `^ok\n$`},
		{name: "the public listener hides /readyz", url: public + "/readyz", wantCode: 404},
		{name: "the public listener hides /metrics", url: public + "/metrics", wantCode: 404},
		{name: "without hca.client_id the sign-in page says sign-in isn't set up", url: public + "/auth/hca/login", wantCode: 503, wantBody: `<code>not_configured</code>`},
		{name: "without a session /api/v1/users/me answers 401", url: public + "/api/v1/users/me", wantCode: 401, wantBody: `"error":"not_signed_in"`},
		{name: "the ops listener doesn't serve sign-in", url: private + "/auth/hca/login", wantCode: 404},
		{name: "the ops listener answers /healthz", url: private + "/healthz", wantCode: 200, wantBody: `^ok\n$`},
		{name: "/readyz is ready once its probe row commits through the writer", url: private + "/readyz", wantCode: 200, wantBody: `"ready":true.*"name":"database","ok":true`},
		{name: "/readyz refuses POST", method: http.MethodPost, url: private + "/readyz", wantCode: 405},
		{name: "/metrics counts the probe's transaction", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_writer_commits_total [1-9]`},
		// The retention sweep at start runs its jobs through the writer, so
		// the queue may still hold one of their deletes.
		{name: "/metrics has the writer queue depth", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_writer_queue_depth [0-9]+$`},
		{name: "/metrics has the ingest rate", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_ingest_batches_total 0$`},
		{name: "/metrics has ingest batch latency", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_ingest_batch_duration_seconds_count 0$`},
		{name: "/metrics has open streams", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_stream_open_streams 0$`},
		{name: "/metrics counts rows the retention jobs deleted", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_retention_deleted_rows_total 0$`},
		{name: "/metrics counts the pending minutes the settle job settled, none on a new database", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_rollup_settled_minutes_total 0$`},
		{name: "/metrics counts the days the build-day job stored, none on a new database", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_build_days_decided_total 0$`},
		{name: "/metrics counts the open live-dashboard streams, none on a new server (DESIGN 10.4)", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_streams_open 0$`},
		{name: "the build-day summary needs a device token", url: public + "/api/v1/build-days", wantCode: 401, wantBody: `"error":"token_missing"`},
		{name: "devlogs need a device token", url: public + "/api/v1/devlogs", wantCode: 401, wantBody: `"error":"token_missing"`},
		{name: "the programs route lists pairtime.toml's programs that take hours, and not calibration's", url: public + "/api/v1/programs", wantCode: 200, wantBody: `^\{"programs":\[\{"name":"hard-mode","display_name":"Hard Mode"\}\]\}\n$`},
		{name: "a create with no credential answers 401 not_signed_in", method: http.MethodPost, url: public + "/api/v1/submissions", wantCode: 401, wantBody: `"error":"not_signed_in"`},
		{name: "the ops listener doesn't serve the submissions API", url: private + "/api/v1/submissions", wantCode: 404},
		{name: "badges need a device token", method: http.MethodPost, url: public + "/api/v1/badges", wantCode: 401, wantBody: `"error":"token_missing"`},
		{name: "a devlog's delete needs a device token", method: http.MethodDelete, url: public + "/api/v1/devlogs/d1", wantCode: 401, wantBody: `"error":"token_missing"`},
		{name: "the ops listener doesn't serve devlogs", url: private + "/api/v1/devlogs", wantCode: 404},
		{name: "the public listener serves the privacy notice", url: public + "/privacy", wantCode: 200, wantBody: `<h1 id="pairtime-privacy-notice">`},
		{name: "the privacy notice marks the controller as not named yet", url: public + "/privacy", wantCode: 200, wantBody: `This notice isn&#39;t final\.`},
		{name: "the public listener serves the evidence definition for the built-in scoring version", url: public + "/docs/evidence", wantCode: 200, wantBody: `This server runs scoring ` + regexp.QuoteMeta(pages.BuiltinScoringVersion) + `\.`},
		{name: "the ops listener doesn't serve the pages", url: private + "/privacy", wantCode: 404},
		{name: "the public listener serves the home page at /, so a sign-in without return_to never ends on a 404", url: public + "/", wantCode: 200, wantBody: `<h1 id="pairtime-counts-the-time-you-spend-building-with-ai">`},
		{name: "without hca.client_id the home page says sign-in isn't set up, and offers none", url: public + "/", wantCode: 200, wantBody: `(?s)<header>\n<a class="name" href="/">pairtime</a>\n</header>.*Sign-in isn&#39;t set up on this server yet\.`},
		{name: "/ links to the privacy notice", url: public + "/", wantCode: 200, wantBody: `href="/privacy"`},
		{name: "/ links to the sample dashboard", url: public + "/", wantCode: 200, wantBody: `<a href="/demo">See a sample dashboard</a>`},
		{name: "the public listener serves the sample dashboard at /demo, labelled as sample data", url: public + "/demo", wantCode: 200, wantBody: `<strong class="tag">Sample data</strong>`},
		{name: "without hca.client_id the sample dashboard says sign-in isn't set up yet", url: public + "/demo", wantCode: 200, wantBody: `Sign-in isn&#39;t set up on this server yet\.`},
		{name: "the ops listener doesn't serve the sample dashboard", url: private + "/demo", wantCode: 404},
		{name: "a path nothing serves is still a 404, not the home page", url: public + "/nothing-here", wantCode: 404},
		{name: "the public listener answers GET /api/v1/time without a token", url: public + "/api/v1/time", wantCode: 200, wantBody: `^\{"server_time":[0-9]+\}$`},
		{name: "uploads get ingest_off while the server key isn't there", method: http.MethodPost, url: public + "/api/v1/ingest/batches", wantCode: 503, wantBody: `"error":"ingest_off"`},
		{name: "the ops listener doesn't serve ingest", url: private + "/api/v1/time", wantCode: 404},
		{name: "the ops listener doesn't serve the home page", url: private + "/", wantCode: 404},
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, tt := range requests {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req, err := http.NewRequest(method, tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantCode {
				t.Errorf("%s %s = %d, want %d; body:\n%s", method, tt.url, resp.StatusCode, tt.wantCode, body)
			}
			if tt.wantBody != "" && !regexp.MustCompile(tt.wantBody).Match(body) {
				t.Errorf("%s %s body doesn't match %s:\n%s", method, tt.url, tt.wantBody, body)
			}
		})
	}

	notReady := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"ready":false}`, http.StatusServiceUnavailable)
	}))
	defer notReady.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	checks := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "healthcheck passes on a ready server", args: []string{"-url", private + "/readyz"}, wantCode: 0, wantStdout: "healthy"},
		{name: "healthcheck passes on /healthz while the process is up", args: []string{"-url", private + "/healthz"}, wantCode: 0, wantStdout: "healthy"},
		{name: "healthcheck fails on a 503", args: []string{"-url", notReady.URL + "/readyz"}, wantCode: 1, wantStderr: "503"},
		{name: "healthcheck fails when nothing listens", args: []string{"-url", closedURL + "/readyz"}, wantCode: 1, wantStderr: "unhealthy"},
	}
	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			he, stdout, stderr := testEnv()
			code := run(context.Background(), append([]string{"healthcheck"}, tt.args...), he)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) || !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stdout = %q, stderr = %q; want them to contain %q and %q", stdout.String(), stderr.String(), tt.wantStdout, tt.wantStderr)
			}
		})
	}

	cancel()
	select {
	case code := <-exited:
		if code != 0 {
			t.Errorf("serve exit code after cancel = %d, want 0; stderr:\n%s", code, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("serve didn't stop within a minute of its context ending")
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("database file mode = %04o, want 0600", perm)
		}
	}
}

// createDatabase creates an empty database at path, as serve does before
// it migrates, for the commands that open only a database that exists.
func createDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Engine: store.SQLite, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrate(t *testing.T) {
	configPath, dbPath := writeConfig(t)
	// Each step runs on what the step before it left.
	steps := []struct {
		name       string
		args       []string
		before     func(t *testing.T)
		wantCode   int
		wantStdout string // substring
		wantStderr string // substring
	}{
		{name: "status on a host without a database creates none, so Litestream can still restore one", args: []string{"-status"}, wantCode: 1, wantStderr: "no database at"},
		{name: "migrate on a host without a database creates none either; serve creates it", wantCode: 1, wantStderr: "no database at"},
		{name: "on a database without migrations, status shows every migration pending", args: []string{"-status"}, before: func(t *testing.T) { createDatabase(t, dbPath) }, wantStdout: "0001_ops_probe  pending"},
		{name: "migrate applies every migration to a database without them", wantStdout: "applied 0001_ops_probe"},
		{name: "a second migrate has nothing to do", wantStdout: "up to date"},
		{name: "after migrating, status shows each one applied", args: []string{"-status"}, wantStdout: "0001_ops_probe  applied 20"},
	}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			if tt.before != nil {
				tt.before(t)
			}
			e, stdout, stderr := testEnv()
			args := append([]string{"migrate", "-config", configPath}, tt.args...)
			if code := run(context.Background(), args, e); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
			if tt.wantCode != 0 {
				if _, err := os.Stat(filepath.Dir(dbPath)); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("the refused command left %s behind (stat: %v)", filepath.Dir(dbPath), err)
				}
			}
		})
	}
}

func TestConfigVersion(t *testing.T) {
	// newer is the config_version of a file a newer release wrote.
	newer := fmt.Sprintf("config_version = %d\n", config.ConfigVersion+1)
	tests := []struct {
		name       string
		extra      string // appended to writeConfig's pairtime.toml
		wantCode   int
		wantStderr string // substring
	}{
		{name: "a rollback runs on a newer release's pairtime.toml, warning about the keys it ignores", extra: newer + "profile_default = \"ask\"\nfrom_a_newer_release = true\n", wantStderr: "ignores the keys it doesn't know: from_a_newer_release"},
		{name: "a newer release's pairtime.toml that leaves out profile_default is refused while keys are ignored, since one may be it misspelt", extra: newer + "profile_defualt = \"ask\"\n", wantCode: 1, wantStderr: "may be a misspelt or misplaced profile_default"},
		{name: "a pairtime.toml for this release still refuses a misspelt key", extra: "signup_open = true\n", wantCode: 1, wantStderr: "unknown keys: signup_open"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, dbPath := writeConfig(t)
			f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(tt.extra); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			createDatabase(t, dbPath)
			e, _, stderr := testEnv()
			if code := run(context.Background(), []string{"migrate", "-config", configPath}, e); code != tt.wantCode {
				t.Errorf("migrate exit code = %d, want %d; stderr:\n%s", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestDialAddr(t *testing.T) {
	tests := []struct {
		name, listen, want string
	}{
		{name: "an empty host dials loopback", listen: ":9091", want: "127.0.0.1:9091"},
		{name: "0.0.0.0 dials loopback", listen: "0.0.0.0:9091", want: "127.0.0.1:9091"},
		{name: ":: dials the IPv6 loopback", listen: "[::]:9091", want: "[::1]:9091"},
		{name: "a named host is kept", listen: "127.0.0.1:9091", want: "127.0.0.1:9091"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dialAddr(tt.listen); got != tt.want {
				t.Errorf("dialAddr(%q) = %q, want %q", tt.listen, got, tt.want)
			}
		})
	}
}

func TestRetentionReport(t *testing.T) {
	timeout := errors.New("retention: server.ip_ja4/request_origins: writer: the batch timed out")
	tests := []struct {
		name        string
		result      retention.Result
		stopping    bool // the sweep is being stopped
		wantDeleted uint64
		wantFailed  uint64
		wantLog     []string // substrings of the one log line; nil means no line
	}{
		{
			name:        "a job's deleted rows are counted and logged",
			result:      retention.Result{ID: "server.ip_ja4/request_origins", Deleted: 3},
			wantDeleted: 3,
			wantLog:     []string{"level=INFO", "job=server.ip_ja4/request_origins", "rows=3"},
		},
		{
			name:   "a job that deleted nothing logs nothing",
			result: retention.Result{ID: "server.ip_ja4/request_origins"},
		},
		{
			name:        "rows deleted before a job failed are counted, and the failure's log says how many",
			result:      retention.Result{ID: "server.ip_ja4/request_origins", Deleted: 1500, Err: timeout},
			wantDeleted: 1500,
			wantFailed:  1,
			wantLog:     []string{"level=ERROR", "job=server.ip_ja4/request_origins", "rows=1500", "the batch timed out"},
		},
		{
			name:       "a job that fails before deleting anything counts the failure",
			result:     retention.Result{ID: "server.ip_ja4/request_origins", Err: timeout},
			wantFailed: 1,
			wantLog:    []string{"level=ERROR", "rows=0"},
		},
		{
			name:        "stopping the sweep isn't a failure, but the rows it deleted first still count",
			result:      retention.Result{ID: "server.ip_ja4/request_origins", Deleted: 500, Err: context.Canceled},
			stopping:    true,
			wantDeleted: 500,
			wantLog:     []string{"level=INFO", "rows=500"},
		},
		{
			name:     "stopping the sweep before it deleted anything logs nothing",
			result:   retention.Result{ID: "server.ip_ja4/request_origins", Err: context.Canceled},
			stopping: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			deleted, failed := new(ops.Counter), new(ops.Counter)
			retentionReport(slog.New(slog.NewTextHandler(&out, nil)), deleted, failed)(tt.result, tt.stopping)
			if got := deleted.Value(); got != tt.wantDeleted {
				t.Errorf("deleted rows counted = %d, want %d", got, tt.wantDeleted)
			}
			if got := failed.Value(); got != tt.wantFailed {
				t.Errorf("failures counted = %d, want %d", got, tt.wantFailed)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			switch {
			case tt.wantLog == nil && out.Len() > 0:
				t.Errorf("logged %q, want nothing", out.String())
			case tt.wantLog != nil && len(lines) != 1:
				t.Errorf("logged %q, want one line", out.String())
			case tt.wantLog != nil:
				for _, w := range tt.wantLog {
					if !strings.Contains(lines[0], w) {
						t.Errorf("logged %q, want it to contain %q", lines[0], w)
					}
				}
			}
		})
	}
}

func TestSourceCommit(t *testing.T) {
	commit := "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
	tests := []struct {
		name string
		info version.Info
		want string
	}{
		{name: "a clean build's pages link its commit", info: version.Info{Commit: commit}, want: commit},
		{name: "a build with local changes links no commit, since none holds what it runs", info: version.Info{Commit: commit, Modified: true}},
		{name: "a build that doesn't know its commit links none", info: version.Info{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceCommit(tt.info); got != tt.want {
				t.Errorf("sourceCommit(%+v) = %q, want %q", tt.info, got, tt.want)
			}
		})
	}
}

// startServe runs pairtimed serve on the file at configPath with the
// environment given, and returns the URLs of its public and ops listeners.
// The server stops when the test ends, and must stop cleanly.
func startServe(t *testing.T, configPath string, environ ...string) (public, private string) {
	t.Helper()
	e, _, stderr := testEnv(environ...)
	type addrs struct{ public, ops string }
	up := make(chan addrs, 1)
	e.listening = func(public, ops string) { up <- addrs{public, ops} }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var code int
	go func() {
		code = run(ctx, []string{"serve", "-config", configPath}, e)
		close(done)
	}()
	listening := false
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if listening && code != 0 {
				t.Errorf("serve exit code after cancel = %d, want 0; stderr:\n%s", code, stderr.String())
			}
		case <-time.After(60 * time.Second):
			t.Error("serve didn't stop within a minute of its context ending")
		}
	})
	select {
	case a := <-up:
		listening = true
		return "http://" + a.public, "http://" + a.ops
	case <-done:
		t.Fatalf("serve exited with %d before listening; stderr:\n%s", code, stderr.String())
	case <-time.After(60 * time.Second):
		t.Fatalf("serve didn't start within a minute; stderr:\n%s", stderr.String())
	}
	return "", ""
}

// setBaseURL changes the base_url of writeConfig's file at configPath.
func setBaseURL(t *testing.T, configPath, baseURL string) {
	t.Helper()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	const old = `base_url = "http://localhost:8080"`
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s doesn't say %s", configPath, old)
	}
	text := strings.Replace(string(data), old, `base_url = "`+baseURL+`"`, 1)
	if err := os.WriteFile(configPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestServeHSTS checks that with an https base_url every response from the
// public listener carries Strict-Transport-Security, whichever handler
// writes it, and that nothing from the ops listener does. With an http
// base_url, which config allows only on localhost, no response carries it,
// since the header must not be sent over plain http (RFC 6797 7.2).
func TestServeHSTS(t *testing.T) {
	bases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "with an https base_url", baseURL: "https://pairtime.example.org", want: "max-age=31536000"},
		{name: "with an http base_url on localhost", baseURL: "http://localhost:8080"},
	}
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, base := range bases {
		t.Run(base.name, func(t *testing.T) {
			configPath, _ := writeConfig(t)
			setBaseURL(t, configPath, base.baseURL)
			public, private := startServe(t, configPath)
			requests := []struct {
				name     string
				method   string
				url      string
				wantCode int
				want     string
			}{
				{name: "the public health check", url: public + "/healthz", wantCode: 200, want: base.want},
				{name: "the home page", url: public + "/", wantCode: 200, want: base.want},
				{name: "the privacy notice", url: public + "/privacy", wantCode: 200, want: base.want},
				{name: "a 404", url: public + "/nothing-here", wantCode: 404, want: base.want},
				{name: "sign-in's error page", url: public + "/auth/hca/login", wantCode: 503, want: base.want},
				{name: "sign-out's redirect", method: http.MethodPost, url: public + "/auth/logout", wantCode: 303, want: base.want},
				{name: "a JSON answer", url: public + "/api/v1/users/me", wantCode: 401, want: base.want},
				{name: "the ops listener's health check", url: private + "/healthz", wantCode: 200},
				{name: "the ops listener's metrics", url: private + "/metrics", wantCode: 200},
			}
			for _, tt := range requests {
				name := tt.name + " carries no Strict-Transport-Security"
				if tt.want != "" {
					name = tt.name + " carries Strict-Transport-Security: " + tt.want
				}
				t.Run(name, func(t *testing.T) {
					method := tt.method
					if method == "" {
						method = http.MethodGet
					}
					req, err := http.NewRequest(method, tt.url, nil)
					if err != nil {
						t.Fatal(err)
					}
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()
					if resp.StatusCode != tt.wantCode {
						t.Errorf("%s %s = %d, want %d", method, tt.url, resp.StatusCode, tt.wantCode)
					}
					if got := resp.Header.Values("Strict-Transport-Security"); len(got) > 1 || resp.Header.Get("Strict-Transport-Security") != tt.want {
						t.Errorf("%s %s has Strict-Transport-Security %q, want %q", method, tt.url, got, tt.want)
					}
				})
			}
		})
	}
}

// TestServeRunning checks that the privacy notice's first lines follow
// what serve runs (#119): ingest takes uploads once the server key is
// there, so from then on the notice no longer says this server doesn't
// receive them (WP-SV4), and the submissions API always runs, so it never
// says this server doesn't take submitted hours, while the evidence store
// waits for WP-SB4 (D-sb1-submissions, Q16).
func TestServeRunning(t *testing.T) {
	tests := []struct {
		name string
		keys bool
		// programs lists two programs in [[programs]].
		programs bool
		want     string
		notWant  string
	}{
		{name: "without the server key ingest is off, so the notice says this server doesn't receive uploads yet",
			want: "This server doesn&#39;t yet let anyone sign in, receive uploads from the pairtime program, take hours you submit to programs, keep an evidence store, or publish day roots."},
		{name: "with the server key ingest takes uploads, so the notice no longer lists them",
			keys:    true,
			want:    "This server doesn&#39;t yet let anyone sign in, take hours you submit to programs, keep an evidence store, or publish day roots.",
			notWant: "receive uploads from"},
		{name: "with [[programs]] listing none, the submissions API refuses every create, so the notice says this server doesn't take submitted hours yet",
			want: "take hours you submit to programs"},
		{name: "with [[programs]] listing a program, the submissions API takes hours, so the notice no longer says it doesn't",
			programs: true,
			want:     "keep an evidence store",
			notWant:  "take hours you submit to programs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, dbPath := writeConfig(t)
			if tt.programs {
				addPrograms(t, configPath)
			}
			if tt.keys {
				if _, err := backup.Generate(backup.KeysDir(filepath.Dir(dbPath)), rand.Reader); err != nil {
					t.Fatal(err)
				}
			}
			public, _ := startServe(t, configPath)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(public + "/privacy")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /privacy = %d, %v", resp.StatusCode, err)
			}
			if !strings.Contains(string(body), tt.want) {
				t.Errorf("the notice doesn't say %q", tt.want)
			}
			if tt.notWant != "" && strings.Contains(string(body), tt.notWant) {
				t.Errorf("the notice still says %q", tt.notWant)
			}
		})
	}
}

// TestServeHeaders checks the headers a browser and Cloudflare read on
// every kind of answer from the public listener: each says how to cache
// it, the error answers of ServeMux and sign-in included, and none can be
// sniffed into another type (#119).
func TestServeHeaders(t *testing.T) {
	configPath, _ := writeConfig(t)
	public, private := startServe(t, configPath)
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	nosniff := "nosniff"
	tests := []struct {
		name     string
		method   string
		url      string
		wantCode int
		want     map[string]string
	}{
		{name: "the public health check is never stored or sniffed", url: public + "/healthz", wantCode: 200,
			want: map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": nosniff}},
		{name: "a 404 for a path nothing serves, such as /favicon.ico, is never stored, so the edge can't keep it",
			url: public + "/favicon.ico", wantCode: 404,
			want: map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": nosniff, "Content-Security-Policy": "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"}},
		{name: "a 405 for a method a page doesn't take is never stored either", method: http.MethodPost, url: public + "/privacy", wantCode: 405,
			want: map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": nosniff}},
		{name: "the privacy notice keeps its own caching", url: public + "/privacy", wantCode: 200,
			want: map[string]string{"Cache-Control": "no-cache", "X-Content-Type-Options": nosniff, "Referrer-Policy": "no-referrer"}},
		{name: "the home page keeps its own referrer policy", url: public + "/", wantCode: 200,
			want: map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": nosniff, "Referrer-Policy": "same-origin"}},
		{name: "a 401 names how a request authenticates (RFC 9110 15.5.2)", url: public + "/api/v1/users/me", wantCode: 401,
			want: map[string]string{"WWW-Authenticate": `Session realm="pairtime"`, "Cache-Control": "no-store", "X-Content-Type-Options": nosniff}},
		{name: "the dashboard API's 401 names the same scheme, as GET /api/v1/users/me answers", url: public + "/api/v1/users/me/dashboard", wantCode: 401,
			want: map[string]string{"WWW-Authenticate": `Session realm="pairtime"`, "Cache-Control": "no-store", "X-Content-Type-Options": nosniff}},
		{name: "the live stream answers a signed-out user:me with the same 401, which stops EventSource (WP-SV8)", url: public + "/api/v1/stream?topics=user:me", wantCode: 401,
			want: map[string]string{"WWW-Authenticate": `Session realm="pairtime"`, "Cache-Control": "no-store", "X-Content-Type-Options": nosniff}},
		{name: "sign-in's error page is never stored", url: public + "/auth/hca/login", wantCode: 503,
			want: map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": nosniff}},
		{name: "the keys document is JSON that a cache must check with the server before each reuse (DESIGN 7.6)", url: public + "/.well-known/pairtime-keys.json", wantCode: 200,
			want: map[string]string{"Cache-Control": "no-cache", "Content-Type": "application/json", "X-Content-Type-Options": nosniff}},
		{name: "the ops listener's health check isn't sniffed either", url: private + "/healthz", wantCode: 200,
			want: map[string]string{"X-Content-Type-Options": nosniff}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req, err := http.NewRequest(method, tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tt.wantCode {
				t.Errorf("%s %s = %d, want %d", method, tt.url, resp.StatusCode, tt.wantCode)
			}
			for k, v := range tt.want {
				if got := resp.Header.Values(k); len(got) != 1 || got[0] != v {
					t.Errorf("%s %s has %s %q, want %q", method, tt.url, k, got, v)
				}
			}
		})
	}
}

// doerFunc is a rollup.Doer that is a function.
type doerFunc func(ctx context.Context, rows int, op writer.Op) error

func (f doerFunc) Do(ctx context.Context, rows int, op writer.Op) error { return f(ctx, rows, op) }

// TestCommitThenTell checks when a time zone change or a hidden name tells
// the live stream: only once its op has committed, whether or not its
// request is still there, since the writer answers a request that went
// with its error before the op has committed.
func TestCommitThenTell(t *testing.T) {
	tests := []struct {
		name string
		// opErr is what the op returns. gone ends the request's context
		// before the op runs.
		opErr    error
		gone     bool
		wantErr  bool
		wantTold bool
	}{
		{name: "an op that committed tells its account", wantTold: true},
		{name: "an op that failed tells nobody", opErr: errors.New("too soon"), wantErr: true},
		{name: "a request that went before its op committed doesn't stop the op, and its account is told once the op commits", gone: true, wantTold: true},
		{name: "a request that went before its op failed tells nobody", gone: true, opErr: errors.New("too soon"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var told []string
			// The writer answers with its context's error once that
			// context ends, before the op has committed, as Writer.Do does.
			w := doerFunc(func(dctx context.Context, _ int, op writer.Op) error {
				if tt.gone {
					cancel()
				}
				if err := dctx.Err(); err != nil {
					return err
				}
				err := op(dctx, nil)
				if len(told) != 0 {
					t.Error("changed heard the account before the op committed")
				}
				return err
			})
			err := commitThenTell(ctx, w, "a1", func(account string) { told = append(told, account) }, func(context.Context, writer.Tx) error {
				return tt.opErr
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("commitThenTell = %v, want an error: %v", err, tt.wantErr)
			}
			if want := tt.wantTold; (len(told) == 1 && told[0] == "a1") != want || len(told) > 1 {
				t.Errorf("changed heard %q, want a1 once: %v", told, want)
			}
		})
	}
}
