// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
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

func TestServe(t *testing.T) {
	configPath, dbPath := writeConfig(t)
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
		{name: "the ops listener answers /healthz", url: private + "/healthz", wantCode: 200, wantBody: `^ok\n$`},
		{name: "/readyz is ready once its probe row commits through the writer", url: private + "/readyz", wantCode: 200, wantBody: `"ready":true.*"name":"database","ok":true`},
		{name: "/readyz refuses POST", method: http.MethodPost, url: private + "/readyz", wantCode: 405},
		{name: "/metrics counts the probe's transaction", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_writer_commits_total [1-9]`},
		{name: "/metrics has the writer queue depth", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_writer_queue_depth 0$`},
		{name: "/metrics has the ingest rate", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_ingest_batches_total 0$`},
		{name: "/metrics has ingest batch latency", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_ingest_batch_duration_seconds_count 0$`},
		{name: "/metrics has open streams", url: private + "/metrics", wantCode: 200, wantBody: `(?m)^pairtime_stream_open_streams 0$`},
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
	tests := []struct {
		name       string
		extra      string // appended to writeConfig's pairtime.toml
		wantCode   int
		wantStderr string // substring
	}{
		{name: "a rollback runs on a newer release's pairtime.toml, warning about the keys it ignores", extra: "config_version = 2\nfrom_a_newer_release = true\n", wantStderr: "ignores the keys it doesn't know: from_a_newer_release"},
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
