// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package rehearsal

// TestDesktopRehearsal runs in rehearsal.yml, inside Xvfb with openbox and
// a private session bus (tools/ci/desktop_session.sh), with
// navikt/mock-oauth2-server standing in for Hack Club Auth as it does for
// internal/server/auth's TestMockOAuth2Server. Its oracles are what a teen
// can see: the commands' exit codes and words, the server's pages and
// JSON, the agent's local API, and the files left in the home folder.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"maps"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

const (
	// oidcEnv names the base URL of a running navikt/mock-oauth2-server.
	// rehearsal.yml starts it and sets this, as ci.yml does for
	// TestMockOAuth2Server.
	oidcEnv = "PAIRTIME_TEST_OIDC_URL"
	// outEnv names a folder for what a person debugging a failed run
	// reads: each command's output, pairtimed's log, the agent's status,
	// both sides' numbers, the scorer's buckets and the ledger's events.
	// rehearsal.yml uploads it as the rehearsal artifact. Without it they
	// go in the test's temporary folder.
	outEnv = "PAIRTIME_REHEARSAL_OUT"
	// claudePlanEnv, set to a plan file, makes the test binary the
	// stand-in claude (runClaude), which writes what it did to the file
	// claudeLogEnv names.
	claudePlanEnv = "PAIRTIME_REHEARSAL_CLAUDE_PLAN"
	claudeLogEnv  = "PAIRTIME_REHEARSAL_CLAUDE_LOG"

	// clientID is the mock's client whose sign-ins are a verified,
	// eligible account (internal/server/auth/testdata/mock-oauth2-server.json).
	clientID = "pairtime-verified"
	// buildVersion is what both binaries report, in release.yml's form, so
	// the agent and the server see a release build rather than a dev one.
	buildVersion = "v0.1.0-rehearsal"
	versionPkg   = "github.com/some-du6e/pairtime/internal/version"

	// captureDir holds WP-AD1's capture of Claude Code 2.1.327 on Linux,
	// whose hook calls the stand-in claude replays. tools/ci's
	// TestRehearsalPaths reads it, to check that rehearsal.yml runs on a
	// change to the capture.
	captureDir = "testdata/fixtures/claude_code/2.1.327/linux/cli/hooks"
	// The capture's paths, which the replay moves to the rehearsal's own.
	captureProject    = "/home/capture/pairtime-capture/projects/cli"
	captureClaudeHome = "/home/capture/pairtime-capture/claude-home"

	// typingDelay is the time between two keys as the person types a
	// prompt, in milliseconds: about 11 characters a second, under SM
	// 2.2's TYPING_MAX_CPS of 15.
	typingDelay = "90"
)

// The test binary is also the stand-in claude, started under the name
// claude in an xterm, the way a person runs Claude Code in a terminal.
func init() {
	if plan := os.Getenv(claudePlanEnv); plan != "" {
		os.Exit(runClaude(plan, os.Getenv(claudeLogEnv)))
	}
}

// TestDesktopRehearsal does what a teen does in their first hour with
// pairtime, with binaries built the way release.yml builds them, and
// checks what they would see at each step. pairtimed runs on SQLite and
// signs people in through the mock; a browser with a cookie jar signs a
// new account in; `pairtime install --yes` sets up an empty home folder;
// `pairtime login` is approved on the server's /device pages; the person
// types three prompts into a Claude Code session in an xterm, moving the
// mouse while they read, so the X server sees real XTEST input; the
// scorer credits the time, the uploader sends it, and /dashboard and its
// API must show exactly what the client shows; then `pairtime uninstall
// --yes --delete-data` must leave nothing behind.
//
// Each step is a subtest. A step that needs what an earlier one failed to
// get is skipped, so one run reports every step that works.
func TestDesktopRehearsal(t *testing.T) {
	oidc := os.Getenv(oidcEnv)
	if oidc == "" {
		t.Skipf("%s isn't set; rehearsal.yml starts navikt/mock-oauth2-server and sets it", oidcEnv)
	}
	r := newRehearsal(t, oidc)
	steps := []struct {
		name  string
		needs []string
		run   func(t *testing.T)
	}{
		{
			name: "pairtimed signs a new account in through Hack Club Auth, and its dashboard says to link the app",
			run:  r.stepServer,
		},
		{
			name: "pairtime install --yes in a home folder holding only the teen's project puts pairtime in place, starts the agent and adds Claude Code's hooks",
			run:  r.stepInstall,
		},
		{
			name:  "pairtime login opens /device without the code, and links the computer once the signed-in account approves the code there",
			needs: []string{"signed_in", "installed"},
			run:   r.stepLogin,
		},
		{
			name:  "the agent reads the X server's idle time and the window in front, and Claude Code's SessionStart hook reaches it",
			needs: []string{"installed"},
			run:   r.stepDesktop,
		},
		{
			name:  "three prompts typed into the Claude Code session reach the ledger through pairtime hook as the person's own hooked turns",
			needs: []string{"desktop"},
			run:   r.stepSession,
		},
		{
			name:  "the scorer credits the person's time, each typed prompt counting as typed, and pairtime today and the agent's API agree on it",
			needs: []string{"session"},
			run:   r.stepCredit,
		},
		{
			name:  "the uploader's minutes reach pairtimed, whose /dashboard and dashboard API show exactly what the client shows",
			needs: []string{"credited", "linked"},
			run:   r.stepUpload,
		},
		{
			name:  "pairtime uninstall --yes --delete-data leaves only the teen's project in the home folder, and nothing running from it",
			needs: []string{"installed"},
			run:   r.stepUninstall,
		},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			for _, n := range s.needs {
				if !r.ok[n] {
					t.Skipf("needs %q, which an earlier step didn't get to", n)
				}
			}
			s.run(t)
		})
	}
}

// rehearsal is one run's folders, processes and what the steps found.
type rehearsal struct {
	t    *testing.T // the whole test, for cleanups that outlive a step
	root string     // the module
	base string     // the test's temporary folder

	bin     string // the built pairtime and pairtimed
	tools   string // the stand-in claude and xdg-open, on PATH
	home    string // the teen's home folder, empty at the start
	runtime string // XDG_RUNTIME_DIR, where the agent's socket goes
	tmp     string // TMPDIR for every pairtime process
	project string // the teen's git repository, where Claude Code runs
	srv     string // pairtimed's config and data
	out     string // diagnostics (outEnv)

	pairtime, pairtimed string // the built binaries
	pt                  string // the pairtime that install put in place

	oidc      string // the mock's base URL
	server    string // pairtimed's base URL
	srvCmd    *exec.Cmd
	srvExited chan struct{}
	srvLog    syncBuffer
	browser   *http.Client

	plan                claudePlan
	planFile, claudeLog string
	claudeWin, plainWin string
	projectSnap         string

	agentPID     int
	sessionStart time.Time
	firstPrompt  time.Time

	ok          map[string]bool // what the steps got to
	collected   bool
	uninstalled bool
}

func newRehearsal(t *testing.T, oidc string) *rehearsal {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module isn't two folders up from the test: %v", err)
	}
	base := t.TempDir()
	r := &rehearsal{t: t, root: root, base: base, oidc: strings.TrimSuffix(oidc, "/"), ok: map[string]bool{}}
	for _, d := range []struct {
		dir  *string
		name string
	}{{&r.bin, "bin"}, {&r.tools, "tools"}, {&r.home, "home"}, {&r.runtime, "run"}, {&r.tmp, "tmp"}, {&r.srv, "server"}} {
		*d.dir = filepath.Join(base, d.name)
		if err := os.Mkdir(*d.dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r.project = filepath.Join(r.home, "projects", "cli")
	r.planFile = filepath.Join(base, "claude-plan.json")
	r.claudeLog = filepath.Join(base, "claude.log")
	r.out = os.Getenv(outEnv)
	if r.out == "" {
		r.out = filepath.Join(base, "out")
	}
	if err := os.MkdirAll(r.out, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.cleanup)
	r.build(t)
	r.makeTools(t)
	r.makeProject(t)
	return r
}

// build builds both binaries as release.yml does, with a release's
// version stamped in.
func (r *rehearsal) build(t *testing.T) {
	commit := os.Getenv("GITHUB_SHA")
	if commit == "" {
		commit = strings.Repeat("0", 40)
	}
	ldflags := fmt.Sprintf("-s -w -X %[1]s.Version=%[2]s -X %[1]s.Commit=%[3]s -X %[1]s.Date=%[4]s",
		versionPkg, buildVersion, commit, time.Now().UTC().Format(time.RFC3339))
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", r.bin+string(filepath.Separator), "./cmd/pairtime", "./cmd/pairtimed")
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building pairtime and pairtimed: %v\n%s", err, out)
	}
	r.pairtime = filepath.Join(r.bin, "pairtime")
	r.pairtimed = filepath.Join(r.bin, "pairtimed")
}

// makeTools puts on PATH a copy of the test binary named claude, which is
// the stand-in Claude Code, and an xdg-open that writes down what it was
// asked to open instead of starting a browser that would take the focus.
func (r *rehearsal) makeTools(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.tools, "claude"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	opener := "#!/bin/sh\n# The rehearsal's browser: it writes down the page pairtime login opens.\nprintf '%s\\n' \"$*\" >> '" + r.openedFile() + "'\n"
	if err := os.WriteFile(filepath.Join(r.tools, "xdg-open"), []byte(opener), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (r *rehearsal) openedFile() string { return filepath.Join(r.base, "xdg-open.args") }

// makeProject makes the teen's repository: the capture's small Python
// project, committed once.
func (r *rehearsal) makeProject(t *testing.T) {
	if err := os.MkdirAll(r.project, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"README.md":         "# cli\n\nCounts the words in a text.\n",
		"wordcount.py":      "\"\"\"Word counts.\"\"\"\n\n\ndef count_words(text):\n    return len(text.split(\" \"))\n",
		"test_wordcount.py": "import unittest\n\nfrom wordcount import count_words\n\n\nclass TestCountWords(unittest.TestCase):\n    def test_single_space(self):\n        self.assertEqual(count_words(\"one two three\"), 3)\n\n    def test_double_space(self):\n        self.assertEqual(count_words(\"one  two three\"), 3)\n\n\nif __name__ == \"__main__\":\n    unittest.main()\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(r.project, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "Count words"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = r.project
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=Teen", "GIT_AUTHOR_EMAIL=teen@example.org", "GIT_COMMITTER_NAME=Teen", "GIT_COMMITTER_EMAIL=teen@example.org")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	snap, err := snapshot(r.project)
	if err != nil {
		t.Fatal(err)
	}
	r.projectSnap = snap
}

// env is the environment of every pairtime process and of the windows:
// the teen's home folder, runtime and temporary folders, the tools on
// PATH, and the test's desktop.
func (r *rehearsal) env(extra ...string) []string {
	env := []string{
		"HOME=" + r.home,
		"XDG_RUNTIME_DIR=" + r.runtime,
		"TMPDIR=" + r.tmp,
		"PATH=" + r.tools + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TZ=UTC",
		"LANG=C.UTF-8",
	}
	for _, k := range []string{"DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "USER", "LOGNAME", "SHELL"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return append(env, extra...)
}

// dataDir is the agent's data folder in the teen's home (CP 7).
func (r *rehearsal) dataDir() string { return filepath.Join(r.home, ".local", "share", "pairtime") }

// result is what a command did.
type result struct {
	stdout, stderr string
	code           int
	err            error // set when it couldn't run, or ran out of time
}

// cmd runs a command as the teen, from their home folder.
func (r *rehearsal) cmd(timeout time.Duration, name string, args ...string) result {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = r.env()
	cmd.Dir = r.home
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		res.err = fmt.Errorf("it didn't finish within %v", timeout)
	case errors.As(err, &ee):
		res.code = ee.ExitCode()
	case err != nil:
		res.err = err
	}
	return res
}

// run is cmd for a step, which saves the output as label and fails when
// the command couldn't run.
func (r *rehearsal) run(t *testing.T, label string, timeout time.Duration, name string, args ...string) result {
	t.Helper()
	res := r.cmd(timeout, name, args...)
	r.save(label+".txt", fmt.Sprintf("$ %s %s\nexit %d\n\nstdout:\n%s\nstderr:\n%s", filepath.Base(name), strings.Join(args, " "), res.code, res.stdout, res.stderr))
	if res.err != nil {
		t.Fatalf("%s %s: %v\nstdout:\n%s\nstderr:\n%s", filepath.Base(name), strings.Join(args, " "), res.err, res.stdout, res.stderr)
	}
	return res
}

// save writes a diagnostics file.
func (r *rehearsal) save(name, content string) {
	p := filepath.Join(r.out, name)
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		os.WriteFile(p, []byte(content), 0o644)
	}
}

// Step 1: the server, and a teen signed in to it.

func (r *rehearsal) stepServer(t *testing.T) {
	r.startServer(t)
	r.signIn(t)
	d := r.get(t, r.server+"/dashboard")
	r.save("dashboard-before.html", d.body)
	if d.status != http.StatusOK || !strings.Contains(d.body, "Nothing counted yet") || !strings.Contains(d.body, "pairtime login") {
		t.Errorf("/dashboard of the new account answered %d, without the empty state that says to run pairtime login:\n%s", d.status, clip(d.body))
	}
}

// startServer makes pairtimed's keys and config with pairtimed init, then
// points sign-in at the mock, names the controller the privacy notice
// needs before sign-ups open (DESIGN 8.6), opens them, and starts
// pairtimed serve on SQLite. The rehearsal's server has no buckets, so
// init gets -no-object-storage: without PAIRTIME_S3_ENDPOINT it refuses
// to write a pairtime.toml the deploy bundle's Litestream couldn't run
// (#123).
func (r *rehearsal) startServer(t *testing.T) {
	// Both at once, so the two can't be one port (#313).
	ports := freePorts(t, 2)
	port, opsPort := ports[0], ports[1]
	r.server = fmt.Sprintf("http://127.0.0.1:%d", port)
	data := filepath.Join(r.srv, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(r.srv, "pairtime.toml")
	env := append(os.Environ(),
		"PAIRTIME_HCA_CLIENT_SECRET=mock-oauth2-server-takes-any-secret",
		"PAIRTIME_EMAIL_KEY="+strings.Repeat("5a", 32))
	initCmd := exec.Command(r.pairtimed, "init", "-base-url", r.server, "-hca-client-id", clientID, "-data-dir", data, "-config-out", cfg, "-no-object-storage")
	initCmd.Env = env
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("pairtimed init: %v\n%s", err, out)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	conf := string(b)
	for _, e := range []struct{ old, new string }{
		{`signups_open = false`, `signups_open = true`},
		{"\nname = \"\"\n", "\nname = \"pairtime rehearsal\"\n"},
		{"\ncontact = \"\"\n", "\ncontact = \"rehearsal@example.org\"\n"},
		{`listen = ":8080"`, fmt.Sprintf(`listen = "127.0.0.1:%d"`, port)},
		{`ops_listen = "127.0.0.1:9091"`, fmt.Sprintf(`ops_listen = "127.0.0.1:%d"`, opsPort)},
		{`client_id = "` + clientID + `"`, `client_id = "` + clientID + `"` + "\n" + `issuer = "` + r.oidc + `/hca"`},
	} {
		if n := strings.Count(conf, e.old); n != 1 {
			t.Fatalf("pairtimed init's pairtime.toml has %s %d times, want once:\n%s", e.old, n, conf)
		}
		conf = strings.Replace(conf, e.old, e.new, 1)
	}
	if err := os.WriteFile(cfg, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	r.save("pairtime.toml", conf)

	cmd := exec.Command(r.pairtimed, "serve", "-config", cfg)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = &r.srvLog, &r.srvLog
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.srvCmd = cmd
	r.srvExited = make(chan struct{})
	go func() {
		cmd.Wait()
		close(r.srvExited)
	}()
	deadline := time.Now().Add(30 * time.Second)
	// A server that takes the connection and never answers mustn't hold
	// the loop past its deadline.
	probe := &http.Client{Timeout: 2 * time.Second}
	for {
		resp, err := probe.Get(r.server + "/api/v1/time")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case <-r.srvExited:
			t.Fatalf("pairtimed serve exited:\n%s", r.srvLog.String())
		case <-time.After(200 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("pairtimed serve didn't answer GET /api/v1/time within 30 s:\n%s", r.srvLog.String())
		}
	}
	r.ok["server"] = true
}

// signIn signs the browser in through Hack Club Auth: the login page
// sends it to the mock, and the mock back to the callback, which signs it
// in and sends it on to /dashboard.
func (r *rehearsal) signIn(t *testing.T) {
	r.browser = &http.Client{
		Jar:     &plainJar{byHost: map[string]map[string]*http.Cookie{}},
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	p := r.get(t, r.server+"/auth/hca/login?return_to="+url.QueryEscape("/dashboard"))
	for i := 0; p.status == http.StatusFound && i < 4; i++ {
		p = r.get(t, p.loc)
	}
	if p.status != http.StatusSeeOther || p.loc != r.server+"/dashboard" {
		t.Fatalf("signing in through Hack Club Auth ended at %s with %d, Location %q:\n%s\npairtimed's log:\n%s", p.url, p.status, p.loc, clip(p.body), r.srvLog.String())
	}
	me := r.get(t, r.server+"/api/v1/users/me")
	r.save("users-me.json", me.body)
	if me.status != http.StatusOK {
		t.Fatalf("GET /api/v1/users/me after signing in answered %d:\n%s", me.status, clip(me.body))
	}
	r.ok["signed_in"] = true
}

// Step 2: pairtime install.

func (r *rehearsal) stepInstall(t *testing.T) {
	res := r.run(t, "install", 3*time.Minute, r.pairtime, "install", "--yes")
	if res.code != 0 {
		t.Fatalf("pairtime install --yes exited %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
	r.pt = filepath.Join(r.home, ".local", "bin", "pairtime")
	if fi, err := os.Stat(r.pt); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("pairtime install didn't put an executable pairtime at %s (%v)\nstdout:\n%s", r.pt, err, res.stdout)
	}
	st, raw, err := r.waitStatus(30*time.Second, func(map[string]any) bool { return true })
	if err != nil {
		t.Fatalf("the agent pairtime install started doesn't answer on its local API: %v\ninstall said:\n%s", err, res.stdout)
	}
	r.agentPID = int(num(st, "pid"))
	r.ok["installed"] = true
	r.save("status-installed.json", string(raw))
	if mode := str(st, "mode"); mode != "local" {
		t.Errorf("a new install's agent is in mode %q, want local", mode)
	}

	settings := filepath.Join(r.home, ".claude", "settings.json")
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("pairtime install --yes didn't write Claude Code's settings.json: %v\ninstall said:\n%s", err, res.stdout)
	}
	r.save("claude-settings.json", string(b))
	hooks, err := parseHooks(b)
	if err != nil {
		t.Fatalf("%s: %v", settings, err)
	}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"} {
		found := false
		for _, g := range hooks[ev] {
			for _, h := range g.Hooks {
				found = found || h.Command == r.pt && slices.Contains(h.Args, "hook")
			}
		}
		if !found {
			t.Errorf("settings.json has no %s hook that runs %s hook:\n%s", ev, r.pt, b)
		}
	}
}

// Step 3: pairtime login, approved on /device.

var (
	// userCode finds the code pairtime login prints (RFC 8628 6.1's
	// base-20 alphabet, two groups of four).
	userCode      = regexp.MustCompile(`\b[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}\b`)
	csrfField     = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	userCodeField = regexp.MustCompile(`name="user_code" value="([^"]+)"`)
	onlineMode    = regexp.MustCompile(`(?m)^\s*mode\s*=\s*"online"`)
)

func (r *rehearsal) stepLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.pt, "login", "--server", r.server)
	cmd.Env = r.env()
	cmd.Dir = r.home
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr syncBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var out strings.Builder
	drain := func() {
		for l := range lines {
			out.WriteString(l + "\n")
		}
	}
	code := ""
	timeout := time.After(time.Minute)
read:
	for code == "" {
		select {
		case l, ok := <-lines:
			if !ok {
				break read
			}
			out.WriteString(l + "\n")
			code = userCode.FindString(l)
		case <-timeout:
			break read
		}
	}
	if code == "" {
		cancel()
		drain()
		cmd.Wait()
		r.save("login.txt", out.String()+"\nstderr:\n"+stderr.String())
		t.Fatalf("pairtime login printed no code within a minute\nstdout:\n%s\nstderr:\n%s", out.String(), stderr.String())
	}

	// On Linux pairtime login opens /device without the code, since other
	// accounts can read xdg-open's command line (device-auth-v1).
	opened := strings.TrimSpace(waitFile(r.openedFile(), 10*time.Second))
	if want := r.server + "/device"; opened != want {
		t.Errorf("pairtime login had the browser open %q, want %q, with no code in it", opened, want)
	}
	r.approve(t, code)

	drain()
	err = cmd.Wait()
	r.save("login.txt", out.String()+"\nstderr:\n"+stderr.String())
	if err != nil {
		t.Fatalf("pairtime login: %v\nstdout:\n%s\nstderr:\n%s\npairtimed's log:\n%s", err, out.String(), stderr.String(), r.srvLog.String())
	}
	if !strings.Contains(out.String(), "Linked.") {
		t.Errorf("pairtime login exited 0 without saying Linked.:\n%s", out.String())
	}
	r.ok["linked"] = true

	var linked struct {
		DeviceID string `json:"device_id"`
		Server   string `json:"server"`
	}
	if b, err := os.ReadFile(filepath.Join(r.dataDir(), "account.json")); err != nil || json.Unmarshal(b, &linked) != nil || linked.DeviceID == "" || linked.Server != r.server {
		t.Errorf("account.json after pairtime login (%v) doesn't name this server and a device:\n%s", err, b)
	}
	// The server's devices page lists the computer, with the build it
	// runs (WP-SV3).
	devices := r.get(t, r.server+"/account/devices")
	r.save("account-devices.html", devices.body)
	if devices.status != http.StatusOK || linked.DeviceID == "" || !strings.Contains(devices.body, "<code>"+linked.DeviceID+"</code>") || !strings.Contains(devices.body, "pairtime "+buildVersion) {
		t.Errorf("/account/devices answered %d without device %s running pairtime %s:\n%s", devices.status, linked.DeviceID, buildVersion, clip(devices.body))
	}
	cfg, err := os.ReadFile(filepath.Join(r.home, ".config", "pairtime", "config.toml"))
	r.save("config.toml", string(cfg))
	if err != nil || !onlineMode.Match(cfg) || !strings.Contains(string(cfg), r.server) {
		t.Errorf("config.toml after pairtime login (%v) doesn't say mode online with this server:\n%s", err, cfg)
	}
	_, raw, err := r.waitStatus(90*time.Second, func(st map[string]any) bool { return str(st, "mode") == "online" })
	r.save("status-linked.json", string(raw))
	if err != nil {
		t.Errorf("the agent didn't go online within 90 s of pairtime login (%v):\n%s", err, raw)
	}
}

// approve does what the teen does in the browser pairtime login opened:
// types the code on /device, checks it, and chooses Link it.
func (r *rehearsal) approve(t *testing.T, code string) {
	p := r.get(t, r.server+"/device")
	m := csrfField.FindStringSubmatch(p.body)
	if p.status != http.StatusOK || m == nil {
		t.Fatalf("GET /device for the signed-in account answered %d with no form:\n%s", p.status, clip(p.body))
	}
	p = r.post(t, r.server+"/device", url.Values{"csrf": {m[1]}, "user_code": {code}})
	for i := 0; (p.status == http.StatusFound || p.status == http.StatusSeeOther) && i < 2; i++ {
		p = r.get(t, p.loc)
	}
	r.save("device-confirm.html", p.body)
	i := strings.Index(p.body, `action="/device/decision"`)
	if p.status != http.StatusOK || i < 0 || !strings.Contains(p.body, code) {
		t.Fatalf("entering code %s on /device answered %d without a page to link it:\n%s", code, p.status, clip(p.body))
	}
	form := p.body[i:]
	m, u := csrfField.FindStringSubmatch(form), userCodeField.FindStringSubmatch(form)
	if m == nil || u == nil {
		t.Fatalf("the page for code %s has no decision form:\n%s", code, clip(p.body))
	}
	p = r.post(t, r.server+"/device/decision", url.Values{"csrf": {m[1]}, "user_code": {u[1]}, "decision": {"approve"}})
	r.save("device-linked.html", p.body)
	if p.status != http.StatusOK || !strings.Contains(p.body, "<h1>Linked</h1>") {
		t.Fatalf("choosing Link it answered %d:\n%s", p.status, clip(p.body))
	}
}

// Step 4: the desktop, and the Claude Code session starting in it.

func (r *rehearsal) stepDesktop(t *testing.T) {
	for _, tool := range []string{"xterm", "xdotool"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s isn't installed; rehearsal.yml installs it", tool)
		}
	}
	r.plainWin = r.startWindow(t, "PairtimePlain", nil, "xterm", "-class", "PairtimePlain", "-e", "sleep", "3600")
	r.plan = r.writePlan(t)
	r.sessionStart = time.Now()
	r.claudeWin = r.startWindow(t, "PairtimeClaude", []string{claudePlanEnv + "=" + r.planFile, claudeLogEnv + "=" + r.claudeLog},
		"xterm", "-class", "PairtimeClaude", "-e", filepath.Join(r.tools, "claude"))
	// Claude Code runs SessionStart as it starts.
	r.waitCalls(t, 0, time.Minute)
	r.ok["claude"] = true
	// Away and back, as alt-tab does, a few seconds after the session's
	// folder resolved to its project, so an os.focus names the session the
	// window now hosts.
	time.Sleep(5 * time.Second)
	r.activate(t, r.plainWin)
	time.Sleep(time.Second)
	r.activate(t, r.claudeWin)
	time.Sleep(time.Second)
	st, raw, err := r.waitStatus(30*time.Second, func(st map[string]any) bool {
		return str(st, "signals", "idle", "via") != "" && str(st, "signals", "focus", "via") != ""
	})
	r.save("status-desktop.json", string(raw))
	if err != nil {
		t.Fatalf("the agent doesn't read idle time and focus from the desktop (%v):\n%s", err, raw)
	}
	if via := str(st, "signals", "idle", "via"); !strings.Contains(via, "MIT-SCREEN-SAVER") {
		t.Errorf("the agent reads idle time through %q, want the X server's MIT-SCREEN-SAVER", via)
	}
	if via := str(st, "signals", "focus", "via"); !strings.Contains(via, "_NET_ACTIVE_WINDOW") {
		t.Errorf("the agent reads focus through %q, want the window manager's _NET_ACTIVE_WINDOW", via)
	}
	if !t.Failed() {
		r.ok["desktop"] = true
	}
}

// startWindow starts an X client in the test's display as the teen, from
// the project folder, and returns its window, which xdotool finds by the
// class it was started with. The window lasts until the test ends.
func (r *rehearsal) startWindow(t *testing.T, class string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = r.env(env...)
	cmd.Dir = r.project
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	out, err := exec.Command("timeout", "30", "xdotool", "search", "--sync", "--limit", "1", "--class", class).Output()
	if err != nil {
		t.Fatalf("finding the %s window: %v", class, err)
	}
	return strings.TrimSpace(string(out))
}

// activate brings win to the front and waits until the window manager
// says it is: openbox can answer windowactivate before it manages a new
// window (tools/ci/desktop_session.sh).
func (r *rehearsal) activate(t *testing.T, win string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		exec.Command("xdotool", "windowactivate", "--sync", win).Run()
		out, err := exec.Command("xdotool", "getactivewindow").Output()
		if err == nil && strings.TrimSpace(string(out)) == win {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %s isn't in front 30 s after xdotool windowactivate; the active window is %q (%v)", win, strings.TrimSpace(string(out)), err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// xdotool runs xdotool with args.
func (r *rehearsal) xdotool(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("xdotool", args...).CombinedOutput(); err != nil {
		t.Fatalf("xdotool %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// Step 5: the session.

// person is the teen at the keyboard between prompts: they move the mouse
// a little every few seconds while they think and read, so every
// 15-second slot has input (SM 2.1), until they leave.
type person struct {
	stop, done chan struct{}
	once       sync.Once
}

func startPerson() *person {
	p := &person{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		tick := time.NewTicker(4 * time.Second)
		defer tick.Stop()
		dx := 4
		for {
			select {
			case <-p.stop:
				return
			case <-tick.C:
				exec.Command("xdotool", "mousemove_relative", "--", strconv.Itoa(dx), "0").Run()
				dx = -dx
			}
		}
	}()
	return p
}

func (p *person) leave() {
	p.once.Do(func() { close(p.stop) })
	<-p.done
}

// typePrompt types text into the Claude Code window, Return after each
// line, through XTEST as a keyboard would. xdotool releases Return a few
// milliseconds after the press, so the presence sample the hook takes
// often finds only that release, dated after the prompt. SM 2.2 rule 6
// counts input up to INPUT_RELEASE_GRACE_MS after the submit (#151), so
// the quick tap still shows the typing; stepCredit checks that every
// prompt counted as typed.
func (r *rehearsal) typePrompt(t *testing.T, text string) {
	t.Helper()
	r.activate(t, r.claudeWin)
	for _, line := range strings.Split(text, "\n") {
		r.xdotool(t, "type", "--delay", typingDelay, "--", line)
		r.xdotool(t, "key", "Return")
	}
}

func (r *rehearsal) stepSession(t *testing.T) {
	p := startPerson()
	defer p.leave()
	// The teen looks over the code before asking anything.
	time.Sleep(45 * time.Second)
	reading := []time.Duration{25 * time.Second, 20 * time.Second, 30 * time.Second}
	for k := 1; k <= 3; k++ {
		r.waitClaude(t, 30*time.Second, fmt.Sprintf("claude waiting for prompt %d", k), func(log []claudeEvent) bool {
			return hasClaude(log, k, "waiting")
		})
		r.typePrompt(t, r.plan.Steps[k].Prompt)
		if k == 1 {
			r.firstPrompt = time.Now()
		}
		r.waitCalls(t, k, 3*time.Minute)
		time.Sleep(reading[k-1])
	}
	p.leave()
	r.ok["session"] = true

	// The writer seals a batch at once after a prompt or a stop, and the
	// tool records of the last turn with its stop, so the poll waits for
	// every record checkLedger needs.
	var evs []event.Event
	deadline := time.Now().Add(90 * time.Second)
	for {
		var err error
		evs, err = r.ledgerEvents()
		if err == nil && recordedAll(evs) {
			break
		}
		if time.Now().After(deadline) {
			r.saveLedger(evs)
			t.Fatalf("90 s after the session the ledger holds %s, want at least %s (%v); the agent's status:\n%s",
				recordCounts(evs, false), recordCounts(evs, true), err, r.statusText())
		}
		time.Sleep(2 * time.Second)
	}
	r.saveLedger(evs)
	r.checkLedger(t, evs)
}

// checkLedger checks the session's records: every Claude Code record is
// via hook from this one session and project, and focus and presence saw
// the person.
func (r *rehearsal) checkLedger(t *testing.T, evs []event.Event) {
	harness := map[event.Kind]int{}
	var session, project string
	var focusHosts, focusProject bool
	var typedSample bool
	for _, ev := range evs {
		m := eventMap(ev)
		switch k := ev.Kind(); k {
		case event.KindSessionStart, event.KindPromptSubmit, event.KindAgentOutput, event.KindAgentTool, event.KindAgentStop:
			harness[k]++
			if v, h := str(m, "via"), str(m, "harness"); v != string(event.ViaHook) || h != string(event.HarnessClaudeCode) {
				t.Errorf("a %s record is via %q from %q, want via hook from claude_code", k, v, h)
			}
			s, p := str(m, "session_key"), str(m, "project_key")
			switch {
			case s == "":
				t.Errorf("a %s record has no session_key", k)
			case session == "":
				session = s
			case s != session:
				t.Errorf("a %s record is in session %q, want %q like the session's first record", k, s, session)
			}
			switch {
			case p == "" && k == event.KindSessionStart:
				t.Logf("the session.start record has no project_key, which it gets once the session's folder resolves")
			case p == "":
				t.Errorf("a %s record has no project_key, so its time can't count toward the project", k)
			case project == "":
				project = p
			case p != project:
				t.Errorf("a %s record is in project %q, want %q like the session's others", k, p, project)
			}
		}
	}
	for _, ev := range evs {
		m := eventMap(ev)
		switch ev.Kind() {
		case event.KindOSFocus:
			hosted, _ := at(m, "data", "hosted_sessions").([]any)
			if str(m, "data", "app_class") == string(event.AppTerminal) && slices.Contains(hosted, any(session)) {
				focusHosts = true
				focusProject = focusProject || str(m, "data", "focus_project") == project
			}
		case event.KindOSPresence:
			if time.UnixMilli(int64(num(m, "t"))).After(r.firstPrompt.Add(-30*time.Second)) && num(m, "data", "idle_any_s") < 1 {
				typedSample = true
			}
		}
	}
	for _, w := range sessionRecords {
		if harness[w.kind] < w.min {
			t.Errorf("the ledger holds %d %s records from Claude Code's hooks, want at least %d", harness[w.kind], w.kind, w.min)
		}
	}
	if !focusHosts {
		t.Errorf("no os.focus record has the terminal hosting the session %q, so SM 2.2 rule 6 can't call a prompt typed", session)
	}
	if !focusProject {
		t.Logf("no os.focus record names the session's project %q as focus_project; the scorer resolves it from the hosted session instead", project)
	}
	if !typedSample {
		t.Errorf("no os.presence record around the prompts has idle_any_s under 1 s, so the agent didn't see the typing")
	}
}

// Step 6: the client's own numbers.

func (r *rehearsal) stepCredit(t *testing.T) {
	deadline := time.Now().Add(4 * time.Minute)
	for {
		today, raw, err := r.today()
		if err == nil && today.total() > 0 {
			r.save("today-credited.json", raw)
			break
		}
		if time.Now().After(deadline) {
			r.save("today-credited.json", raw)
			t.Fatalf("pairtime today --json shows no time of yours 4 minutes after the session (%v):\n%s\nthe agent's status:\n%s", err, raw, r.statusText())
		}
		time.Sleep(10 * time.Second)
	}
	r.ok["credited"] = true
	r.checkPromptsTyped(t)
	// The CLI reads the agent's day rollups, and the local API serves
	// them; both must say the same (WP-AG4).
	var last string
	for range 5 {
		today, raw, err := r.today()
		api, _, err2 := r.clientDays([]string{today.Date})
		if err == nil && err2 == nil && sameCredit(byDay{today.Date: today.credit()}, api) {
			t.Logf("pairtime today: %s", raw)
			return
		}
		last = fmt.Sprintf("pairtime today --json (%v):\n%s\nGET /v1/days/%s (%v):\n%v", err, raw, today.Date, err2, api)
		time.Sleep(3 * time.Second)
	}
	t.Errorf("pairtime today and the agent's GET /v1/days disagree:\n%s", last)
}

// checkPromptsTyped checks that each of the three prompts the person
// typed counted as typed: SM 2.2 rule 6 made it the human act whose
// window credits the seconds before it (SM 2.3), so its event is an
// anchor of a credited minute. A prompt the scorer took for one that
// arrived without typing (X.no_input) anchors nothing. The scorer can
// settle the last prompt's minutes a batch or two later, so this waits.
func (r *rehearsal) checkPromptsTyped(t *testing.T) {
	t.Helper()
	evs, err := r.ledgerEvents()
	if err != nil {
		t.Errorf("reading the ledger back: %v", err)
		return
	}
	var prompts []string
	for _, ev := range evs {
		if ev.Kind() == event.KindPromptSubmit {
			prompts = append(prompts, ev.EventID)
		}
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		anchors := map[string]bool{}
		for _, date := range r.dates() {
			latest, _ := r.latestBuckets(date)
			for _, bk := range latest {
				if !slices.ContainsFunc(bk.Slots, func(s slotRev) bool { return s.CR > 0 }) {
					continue // a minute with no credit names no anchor that counted
				}
				for _, a := range bk.Anchors {
					anchors[a] = true
				}
			}
		}
		var untyped []int
		for i, id := range prompts {
			if !anchors[id] {
				untyped = append(untyped, i+1)
			}
		}
		if len(prompts) == 3 && len(untyped) == 0 {
			return
		}
		if time.Now().After(deadline) {
			_, raw, _ := r.agentGet("/v1/days/" + time.Now().UTC().Format(time.DateOnly))
			t.Errorf("of the %d prompts the person typed, prompts %v anchor no credited minute, so they counted as arriving without typing (SM 2.2 rule 6); the day:\n%s", len(prompts), untyped, raw)
			return
		}
		time.Sleep(10 * time.Second)
	}
}

// Step 7: the server's numbers.

func (r *rehearsal) stepUpload(t *testing.T) {
	dates := r.dates()
	deadline := time.Now().Add(20 * time.Minute)
	var firstSeen time.Time
	var g uploadReading
	for {
		g = r.readUpload(dates)
		if g.serr == nil && firstSeen.IsZero() && g.server.total() > 0 {
			firstSeen = time.Now()
			t.Logf("pairtimed's dashboard first showed the person's time %v after the first prompt (its empty state says within a few minutes of a prompt)", firstSeen.Sub(r.firstPrompt).Round(time.Second))
		}
		if g.agrees(dates) {
			break
		}
		if time.Now().After(deadline) {
			text := g.String()
			r.save("compare.txt", text)
			out := r.cmd(time.Minute, r.pt, "privacy", "show")
			r.save("privacy-show.txt", out.stdout+"\nstderr:\n"+out.stderr)
			t.Fatalf("20 minutes after the session pairtimed's dashboard still doesn't show what the client does:\n%s\nthe agent's status:\n%s\npairtime privacy show says:\n%s", text, r.statusText(), out.stderr)
		}
		time.Sleep(10 * time.Second)
	}
	r.ok["uploaded"] = true
	b, _ := json.MarshalIndent(g.view, "", "  ")
	r.save("server-dashboard.json", string(b))
	t.Logf("client and server agree:\n%v", g.client)
	if len(dates) == 1 {
		t.Logf("the client's uploaded slots and the server's week both split it %v by level and %v by kind, and the agent ran on its own %.1f s on both", g.srvLevels, g.srvKinds, g.srvAgent)
	} else {
		t.Logf("the session spans %v, so the splits by level and kind, which the server gives for the week, aren't compared", dates)
	}

	// The teen's own view says the same as the server, for a day the
	// session's credit is on.
	today, raw, err := r.today()
	switch {
	case err != nil:
		t.Errorf("pairtime today --json: %v\n%s", err, raw)
	case !slices.Contains(dates, today.Date):
		t.Logf("pairtime today now shows %s, a day the session didn't touch, so it isn't compared", today.Date)
	case len(dates) == 1 && today.total() <= 0:
		t.Errorf("pairtime today --json shows none of your time on %s, the session's only day:\n%s", today.Date, raw)
	case !sameCredit(byDay{today.Date: today.credit()}, byDay{today.Date: g.server[today.Date]}):
		t.Errorf("pairtime today --json for %s doesn't match the server's %v:\n%s", today.Date, g.server[today.Date], raw)
	}

	// So does the page: it names the project and gives the day's total
	// as it writes durations, in whole minutes rounded down.
	d := r.get(t, r.server+"/dashboard")
	r.save("dashboard-after.html", d.body)
	if d.status != http.StatusOK || strings.Contains(d.body, "Nothing counted yet") {
		t.Errorf("/dashboard answered %d, or still says nothing is counted:\n%s", d.status, clip(d.body))
	}
	for _, ps := range g.server {
		for key := range ps {
			if short := key[:min(len(key), 12)]; !strings.Contains(d.body, short) {
				t.Errorf("/dashboard doesn't name the project %s… the client credits", short)
			}
		}
	}
	if srvToday := str(g.view, "today"); slices.Contains(dates, srvToday) {
		total := 0.0
		for _, c := range g.server[srvToday] {
			total += c.S
		}
		if want := "<strong>" + html.EscapeString(pageDuration(total)) + "</strong> of your time"; !strings.Contains(d.body, want) {
			t.Errorf("/dashboard doesn't say %q, for the server's %.1f s today", want, total)
		}
	}

	// The client's own views split each second exactly, so a slot whose
	// seconds had two reasons or two levels reads differently there than
	// on the server's dashboard, which counts its split per slot
	// (D-kind-split-by-slot). Same total, other split.
	if len(dates) == 1 && err == nil {
		if levels, kinds, _ := today.splits(); !sameSplit(levels, g.srvLevels) || !sameSplit(kinds, g.srvKinds) {
			t.Logf("pairtime today splits the same time %v by level and %v by kind; pairtimed's dashboard, from one reason and one level a slot, %v and %v", levels, kinds, g.srvLevels, g.srvKinds)
		}
	}
}

// uploadReading is one poll of both sides. stepUpload waits for a poll
// whose readings all agree and checks the rest against that poll alone,
// since the scorer may write another revision of a pending minute
// between two reads.
type uploadReading struct {
	client, server byDay
	clientAgent    map[string]float64
	view           map[string]any
	cerr, serr     error
	// The client's uploaded slots and the server's week, by evidence level
	// (DESIGN 6.4) and by kind (SM 5.1), each slot under its one reason
	// and its one level, and the server's day of the agent's time on its
	// own (D-bucket-agent-only). Read only when the session is on one day,
	// as the server's split is the week's, and the account is new.
	slotLevels, slotKinds, srvLevels, srvKinds map[string]float64
	srvAgent                                   float64
	splitErr                                   error
}

func (r *rehearsal) readUpload(dates []string) uploadReading {
	var g uploadReading
	g.client, g.clientAgent, g.cerr = r.clientDays(dates)
	g.server, g.view, g.serr = r.serverDays(dates)
	if len(dates) == 1 && g.serr == nil {
		g.slotLevels, g.slotKinds, g.splitErr = r.slotSplits(dates[0])
		g.srvLevels, g.srvKinds, g.srvAgent = serverSplits(g.view, dates[0])
	}
	return g
}

// agrees reports whether the poll shows the client's credit on the server,
// per day and project, and, on one day, the uploaded slots' splits and the
// agent's time on its own as well.
func (g uploadReading) agrees(dates []string) bool {
	if g.cerr != nil || g.serr != nil || g.client.total() <= 0 || !sameCredit(g.client, g.server) {
		return false
	}
	if len(dates) != 1 {
		return true
	}
	return g.splitErr == nil && sameSplit(g.slotLevels, g.srvLevels) && sameSplit(g.slotKinds, g.srvKinds) &&
		math.Abs(g.clientAgent[dates[0]]-g.srvAgent) <= 0.01
}

func (g uploadReading) String() string {
	return fmt.Sprintf("client (GET /v1/days, %v):\n%v  the agent on its own: %v\nserver (GET /api/v1/users/me/dashboard, %v):\n%v  the agent on its own: %.1f s\nthe client's uploaded slots (%v): by level %v, by kind %v\nthe server's week: by level %v, by kind %v\n",
		g.cerr, g.client, g.clientAgent, g.serr, g.server, g.srvAgent, g.splitErr, g.slotLevels, g.slotKinds, g.srvLevels, g.srvKinds)
}

// pageDuration writes seconds as /dashboard does: hours and minutes,
// rounded down to the minute, and "<1m" for more than nothing but under a
// minute.
func pageDuration(s float64) string {
	m := int64(s / 60)
	switch {
	case m == 0 && s > 0:
		return "<1m"
	case m < 60:
		return strconv.FormatInt(m, 10) + "m"
	case m%60 == 0:
		return strconv.FormatInt(m/60, 10) + "h"
	}
	return strconv.FormatInt(m/60, 10) + "h " + strconv.FormatInt(m%60, 10) + "m"
}

// slotSplits is the client's minute buckets of date by evidence level and
// by kind of work, slot by slot, from the latest revision of each minute:
// what the uploader sends (SM 5.1).
func (r *rehearsal) slotSplits(date string) (levels, kinds map[string]float64, err error) {
	latest, err := r.latestBuckets(date)
	if err != nil {
		return nil, nil, err
	}
	levels, kinds = map[string]float64{}, map[string]float64{}
	for _, bk := range latest {
		for _, s := range bk.Slots {
			if s.CR > 0 {
				levels[s.EV] += s.CR
				kinds[s.Kind] += s.CR
			}
		}
	}
	return levels, kinds, nil
}

// bucketRev is a line of the agent's minute buckets, the parts read here.
type bucketRev struct {
	Minute string    `json:"minute"`
	Rev    int       `json:"rev"`
	Slots  []slotRev `json:"slots"`
	// Anchors are the human acts whose windows the minute's credit came
	// from, by event id: local, never uploaded (DESIGN 4.3).
	Anchors []string `json:"anchors"`
}

// slotRev is one 15-second slot of a minute bucket: its credit, and the
// kind and level of its one reason (SM 5.1).
type slotRev struct {
	CR   float64 `json:"cr"`
	Kind string  `json:"kind"`
	EV   string  `json:"ev"`
}

// latestBuckets is the latest revision of each minute in the agent's
// buckets/<date>.jsonl, by minute.
func (r *rehearsal) latestBuckets(date string) (map[string]bucketRev, error) {
	b, err := os.ReadFile(filepath.Join(r.dataDir(), "buckets", date+".jsonl"))
	if err != nil {
		return nil, err
	}
	latest := map[string]bucketRev{}
	for _, line := range strings.Split(string(b), "\n") {
		var bk bucketRev
		if json.Unmarshal([]byte(line), &bk) != nil || bk.Minute == "" {
			continue // a blank line, or a torn last one the agent is still writing
		}
		if prev, ok := latest[bk.Minute]; !ok || bk.Rev >= prev.Rev {
			latest[bk.Minute] = bk
		}
	}
	return latest, nil
}

// serverSplits is the server's week by evidence level and by kind of
// work, and the day's agent time on its own, pending minutes included.
func serverSplits(view map[string]any, date string) (levels, kinds map[string]float64, agent float64) {
	levels, kinds = map[string]float64{}, map[string]float64{}
	sum := func(into map[string]float64, list any, name string) {
		items, _ := list.([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			if s := num(m, "final_s") + num(m, "pending_s"); s > 0 {
				into[str(m, name)] += s
			}
		}
	}
	sum(levels, at(view, "week", "levels"), "level")
	sum(kinds, at(view, "week", "kinds"), "kind")
	days, _ := view["days"].([]any)
	for _, d := range days {
		if dm, _ := d.(map[string]any); str(dm, "date") == date {
			agent = num(dm, "agent", "final_s") + num(dm, "agent", "pending_s")
		}
	}
	return levels, kinds, agent
}

// sameSplit reports whether two splits give the same seconds to the same
// names.
func sameSplit(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, s := range a {
		if math.Abs(s-b[k]) > 0.01 {
			return false
		}
	}
	return true
}

// Step 8: pairtime uninstall.

func (r *rehearsal) stepUninstall(t *testing.T) {
	r.collect(t)
	last := len(r.plan.Steps) - 1
	if r.ok["claude"] && r.claudeAt(last, "waiting") {
		// The teen quits Claude Code first, which runs SessionEnd. Its own
		// subtest, so the uninstall below runs whatever it finds.
		t.Run("Claude Code's /exit runs its SessionEnd hook, which exits 0", func(t *testing.T) {
			r.typePrompt(t, r.plan.Steps[last].Prompt)
			r.waitCalls(t, last, time.Minute)
			r.waitClaude(t, 10*time.Second, "claude exiting", func(log []claudeEvent) bool { return hasClaude(log, last+1, "done") })
		})
	}
	res := r.run(t, "uninstall", 3*time.Minute, r.pt, "uninstall", "--yes", "--delete-data")
	if res.code != 0 {
		t.Fatalf("pairtime uninstall --yes --delete-data exited %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
	r.uninstalled = true
	if r.agentPID > 0 && !waitGone(r.agentPID, 20*time.Second) {
		t.Errorf("the agent, process %d, still runs 20 s after pairtime uninstall", r.agentPID)
	}
	for _, c := range []struct {
		dir, keep, what string
	}{
		{r.home, r.project, "the home folder"},
		{r.runtime, "", "XDG_RUNTIME_DIR"},
		{r.tmp, "", "TMPDIR"},
	} {
		left, err := leftIn(c.dir, c.keep)
		if err != nil {
			t.Errorf("listing %s: %v", c.what, err)
		}
		if len(left) > 0 {
			t.Errorf("pairtime uninstall --yes --delete-data left these in %s:\n%s", c.what, strings.Join(left, "\n"))
		}
	}
	if procs := runningFrom(r.home); len(procs) > 0 {
		t.Errorf("processes still run from the home folder after pairtime uninstall:\n%s", strings.Join(procs, "\n"))
	}
	if snap, err := snapshot(r.project); err != nil || snap != r.projectSnap {
		t.Errorf("the teen's project isn't as it was (%v):\nbefore:\n%s\nafter:\n%s", err, r.projectSnap, snap)
	}
}

// collect saves what a person debugging the run needs, before uninstall
// deletes it.
func (r *rehearsal) collect(t *testing.T) {
	if r.collected {
		return
	}
	r.collected = true
	if _, raw, err := r.agentGet("/v1/status"); err == nil {
		r.save("status.json", string(raw))
	}
	if r.pt != "" {
		res := r.cmd(time.Minute, r.pt, "today", "--json")
		r.save("today.json", res.stdout+res.stderr)
		res = r.cmd(time.Minute, r.pt, "privacy", "show")
		r.save("privacy-show.txt", res.stdout+"\nstderr:\n"+res.stderr)
	}
	for _, d := range r.dates() {
		if _, raw, err := r.agentGet("/v1/days/" + d); err == nil {
			r.save("days-"+d+".json", string(raw))
		}
	}
	if r.browser != nil {
		if raw, err := r.fetch(r.server + "/api/v1/users/me/dashboard"); err == nil {
			r.save("server-dashboard-final.json", raw)
		}
	}
	for _, sub := range []string{"buckets", "days"} {
		files, _ := filepath.Glob(filepath.Join(r.dataDir(), sub, "*"))
		for _, f := range files {
			if b, err := os.ReadFile(f); err == nil {
				r.save(filepath.Join(sub, filepath.Base(f)), string(b))
			}
		}
	}
	if evs, err := r.ledgerEvents(); err == nil {
		r.saveLedger(evs)
	}
	for _, f := range []string{r.claudeLog, r.openedFile(), r.planFile} {
		if b, err := os.ReadFile(f); err == nil {
			r.save(filepath.Base(f), string(b))
		}
	}
	t.Logf("diagnostics are in %s", r.out)
}

// cleanup stops what is still running once the test ends: the agent when
// uninstall didn't, and pairtimed.
func (r *rehearsal) cleanup() {
	r.collect(r.t)
	if !r.uninstalled {
		if pid := r.portPID(); pid > 0 {
			r.agentPID = pid
		}
		if r.agentPID > 0 {
			syscall.Kill(r.agentPID, syscall.SIGTERM)
			if !waitGone(r.agentPID, 10*time.Second) {
				syscall.Kill(r.agentPID, syscall.SIGKILL)
				waitGone(r.agentPID, 5*time.Second)
			}
		}
	}
	if r.srvCmd != nil {
		r.srvCmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-r.srvExited:
		case <-time.After(15 * time.Second):
			r.srvCmd.Process.Kill()
			<-r.srvExited
		}
	}
	r.save("pairtimed.log", r.srvLog.String())
}

// The browser.

// plainJar is the browser's cookie jar. It keeps each host's cookies by
// name and sends them all back to it, Secure ones included: pairtimed
// serves plain http on 127.0.0.1 here, which browsers and Go 1.27's
// cookiejar count as secure but Go 1.26's doesn't.
type plainJar struct {
	mu     sync.Mutex
	byHost map[string]map[string]*http.Cookie
}

func (j *plainJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	h := u.Hostname()
	if j.byHost[h] == nil {
		j.byHost[h] = map[string]*http.Cookie{}
	}
	for _, c := range cookies {
		if c.MaxAge < 0 || !c.Expires.IsZero() && c.Expires.Before(time.Now()) {
			delete(j.byHost[h], c.Name)
			continue
		}
		j.byHost[h][c.Name] = &http.Cookie{Name: c.Name, Value: c.Value}
	}
}

func (j *plainJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []*http.Cookie
	for _, c := range j.byHost[u.Hostname()] {
		out = append(out, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	slices.SortFunc(out, func(a, b *http.Cookie) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// page is a response the browser got, its Location made absolute.
type page struct {
	url, loc, body string
	status         int
}

func (r *rehearsal) do(t *testing.T, req *http.Request) page {
	t.Helper()
	resp, err := r.browser.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	p := page{url: req.URL.String(), status: resp.StatusCode, body: string(b)}
	if loc := resp.Header.Get("Location"); loc != "" {
		if u, err := req.URL.Parse(loc); err == nil {
			p.loc = u.String()
		} else {
			p.loc = loc
		}
	}
	return p
}

func (r *rehearsal) get(t *testing.T, u string) page {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r.do(t, req)
}

// post sends a form from one of the server's own pages, so with its
// Origin, as a browser does.
func (r *rehearsal) post(t *testing.T, u string, form url.Values) page {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", r.server)
	return r.do(t, req)
}

// fetch is a GET for polling, which reports a failure rather than ending
// the step.
func (r *rehearsal) fetch(u string) (string, error) {
	resp, err := r.browser.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return string(b), fmt.Errorf("GET %s answered %d: %s", u, resp.StatusCode, clip(string(b)))
	}
	return string(b), nil
}

// The agent's local API.

// portFile is port.json: the agent's port, bearer token and process.
type portFile struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func (r *rehearsal) readPortFile() (portFile, error) {
	var pf portFile
	b, err := os.ReadFile(filepath.Join(r.dataDir(), "port.json"))
	if err != nil {
		return pf, err
	}
	err = json.Unmarshal(b, &pf)
	return pf, err
}

func (r *rehearsal) portPID() int {
	pf, _ := r.readPortFile()
	return pf.PID
}

// agentGet asks the running agent's local API with its bearer token.
func (r *rehearsal) agentGet(path string) (map[string]any, []byte, error) {
	pf, err := r.readPortFile()
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", pf.Port, path), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pf.Token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, raw, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, raw, fmt.Errorf("GET %s answered %d", path, resp.StatusCode)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, raw, err
	}
	return m, raw, nil
}

// waitStatus asks for GET /v1/status until ok says yes, for at most
// timeout.
func (r *rehearsal) waitStatus(timeout time.Duration, ok func(map[string]any) bool) (map[string]any, []byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		st, raw, err := r.agentGet("/v1/status")
		if err == nil && ok(st) {
			return st, raw, nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("not within %v", timeout)
			}
			return st, raw, err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// statusText is the agent's status for a failure message.
func (r *rehearsal) statusText() string {
	_, raw, err := r.agentGet("/v1/status")
	if err != nil {
		return fmt.Sprintf("(%v) %s", err, raw)
	}
	return string(raw)
}

// The numbers on both sides.

// credit is one project's credited seconds on a day, pending minutes
// included, and the part of them in pending minutes.
type credit struct {
	S, PendingS float64
}

// byDay is credit by date, then by project key. Only projects with
// credit are in it.
type byDay map[string]map[string]credit

func (d byDay) total() float64 {
	s := 0.0
	for _, ps := range d {
		for _, c := range ps {
			s += c.S
		}
	}
	return s
}

func (d byDay) String() string {
	var b strings.Builder
	for _, date := range slices.Sorted(maps.Keys(d)) {
		for _, key := range slices.Sorted(maps.Keys(d[date])) {
			c := d[date][key]
			fmt.Fprintf(&b, "  %s %s: %.1f s, %.1f s of it pending\n", date, key, c.S, c.PendingS)
		}
	}
	if b.Len() == 0 {
		return "  nothing\n"
	}
	return b.String()
}

// sameCredit reports whether a and b credit the same seconds to the same
// projects on the same days. Pending and final may differ: each side
// settles a minute on its own clock.
func sameCredit(a, b byDay) bool {
	dates := map[string]bool{}
	for d := range a {
		dates[d] = true
	}
	for d := range b {
		dates[d] = true
	}
	for d := range dates {
		pa, pb := a[d], b[d]
		if len(pa) != len(pb) {
			return false
		}
		for k, ca := range pa {
			cb, ok := pb[k]
			if !ok || math.Abs(ca.S-cb.S) > 0.01 {
				return false
			}
		}
	}
	return true
}

// dates are the UTC days the session touched.
func (r *rehearsal) dates() []string {
	var out []string
	for _, at := range []time.Time{r.sessionStart, time.Now()} {
		if at.IsZero() {
			continue
		}
		if d := at.UTC().Format(time.DateOnly); !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// clientDays is the agent's GET /v1/days for each date: the credit, and
// the agent's time on its own over every project (SM 5.3's agent_only_s).
func (r *rehearsal) clientDays(dates []string) (byDay, map[string]float64, error) {
	out, agent := byDay{}, map[string]float64{}
	for _, date := range dates {
		m, _, err := r.agentGet("/v1/days/" + date)
		if err != nil {
			return out, agent, err
		}
		out[date] = map[string]credit{}
		projects, _ := m["projects"].([]any)
		for _, p := range projects {
			pm, _ := p.(map[string]any)
			agent[date] += num(pm, "agent_only_s")
			if c := (credit{S: num(pm, "credited_s"), PendingS: num(pm, "pending_s")}); c.S > 0 {
				out[date][str(pm, "project")] = c
			}
		}
	}
	return out, agent, nil
}

// serverDays is the signed-in account's GET /api/v1/users/me/dashboard,
// for each date, with each project named by its device key.
func (r *rehearsal) serverDays(dates []string) (byDay, map[string]any, error) {
	raw, err := r.fetch(r.server + "/api/v1/users/me/dashboard")
	if err != nil {
		return nil, nil, err
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		return nil, nil, err
	}
	keyOf := map[string]string{}
	projects, _ := view["projects"].([]any)
	for _, p := range projects {
		pm, _ := p.(map[string]any)
		keys, _ := pm["keys"].([]any)
		if len(keys) != 1 {
			return nil, view, fmt.Errorf("project %s has keys %v, want the one key of the one device", str(pm, "id"), keys)
		}
		keyOf[str(pm, "id")], _ = keys[0].(string)
	}
	out := byDay{}
	for _, date := range dates {
		out[date] = map[string]credit{}
	}
	days, _ := view["days"].([]any)
	for _, d := range days {
		dm, _ := d.(map[string]any)
		ps, ok := out[str(dm, "date")]
		if !ok {
			continue
		}
		entries, _ := dm["projects"].([]any)
		for _, e := range entries {
			em, _ := e.(map[string]any)
			final, pending := num(em, "you", "final_s"), num(em, "you", "pending_s")
			if final+pending > 0 {
				ps[keyOf[str(em, "project")]] = credit{S: final + pending, PendingS: pending}
			}
		}
	}
	return out, view, nil
}

// todayJSON is what pairtime today --json prints, the parts compared
// here (docs/schema/cli-today.schema.json).
type todayJSON struct {
	Date        string `json:"date"`
	Calibrating bool   `json:"calibrating"`
	Projects    []struct {
		Project string `json:"project"`
		You     *struct {
			S        float64 `json:"s"`
			PendingS float64 `json:"pending_s"`
			Kinds    []struct {
				Kind string  `json:"kind"`
				S    float64 `json:"s"`
			} `json:"kinds"`
			Levels []struct {
				Level string  `json:"level"`
				S     float64 `json:"s"`
			} `json:"levels"`
		} `json:"you"`
		Agents []struct {
			Harness string  `json:"harness"`
			S       float64 `json:"s"`
		} `json:"agents"`
	} `json:"projects"`
}

// splits is the day's credit by evidence level and by kind of work, and
// the agents' time on their own, over every project.
func (d todayJSON) splits() (levels, kinds map[string]float64, agent float64) {
	levels, kinds = map[string]float64{}, map[string]float64{}
	for _, p := range d.Projects {
		for _, a := range p.Agents {
			agent += a.S
		}
		if p.You == nil {
			continue
		}
		for _, l := range p.You.Levels {
			if l.S > 0 {
				levels[l.Level] += l.S
			}
		}
		for _, k := range p.You.Kinds {
			if k.S > 0 {
				kinds[k.Kind] += k.S
			}
		}
	}
	return levels, kinds, agent
}

func (d todayJSON) credit() map[string]credit {
	out := map[string]credit{}
	for _, p := range d.Projects {
		if p.You != nil && p.You.S > 0 {
			out[p.Project] = credit{S: p.You.S, PendingS: p.You.PendingS}
		}
	}
	return out
}

func (d todayJSON) total() float64 { return byDay{d.Date: d.credit()}.total() }

func (r *rehearsal) today() (todayJSON, string, error) {
	var d todayJSON
	res := r.cmd(time.Minute, r.pt, "today", "--json")
	if res.err != nil || res.code != 0 {
		return d, res.stdout + res.stderr, fmt.Errorf("pairtime today --json exited %d (%v)", res.code, res.err)
	}
	err := json.Unmarshal([]byte(res.stdout), &d)
	return d, res.stdout, err
}

// The ledger.

// ledgerEvents reads the events in the ledger's sealed batches. Other
// records, such as the uploader's failure notes, are skipped.
func (r *rehearsal) ledgerEvents() ([]event.Event, error) {
	var evs []event.Event
	err := ledger.ReadBatches(filepath.Join(r.dataDir(), "ledger"), 1, func(b ledger.Batch) error {
		for _, rec := range b.Records {
			body, err := pkgledger.ParseBody(rec.Body)
			if err != nil {
				continue
			}
			ev, err := body.Event()
			if err != nil {
				continue
			}
			evs = append(evs, ev)
		}
		return nil
	})
	return evs, err
}

func (r *rehearsal) saveLedger(evs []event.Event) {
	var b strings.Builder
	for _, ev := range evs {
		j, err := json.Marshal(ev)
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", ev.Kind(), err)
			continue
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	r.save("ledger-events.jsonl", b.String())
}

// sessionRecords are the least the session's hook calls leave in the
// ledger, by kind: the capture's calls 000001 to 000014 make one session
// start, and three turns with their outputs, tool calls and stops.
var sessionRecords = []struct {
	kind event.Kind
	min  int
}{{event.KindSessionStart, 1}, {event.KindPromptSubmit, 3}, {event.KindAgentOutput, 3}, {event.KindAgentTool, 3}, {event.KindAgentStop, 3}}

// recordedAll reports whether evs hold every record sessionRecords asks
// for.
func recordedAll(evs []event.Event) bool {
	for _, w := range sessionRecords {
		if countKind(evs, w.kind) < w.min {
			return false
		}
	}
	return true
}

// recordCounts writes how many records of each of sessionRecords' kinds
// evs hold, or with want set, how many they must.
func recordCounts(evs []event.Event, want bool) string {
	var parts []string
	for _, w := range sessionRecords {
		n := countKind(evs, w.kind)
		if want {
			n = w.min
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, w.kind))
	}
	return strings.Join(parts, ", ")
}

func countKind(evs []event.Event, k event.Kind) int {
	n := 0
	for _, ev := range evs {
		if ev.Kind() == k {
			n++
		}
	}
	return n
}

// eventMap is an event as its JSON reads.
func eventMap(ev event.Event) map[string]any {
	var m map[string]any
	if b, err := json.Marshal(ev); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

// The stand-in claude.

// claudePlan is what the stand-in claude does, a step at a time.
type claudePlan struct {
	Steps []claudeStep `json:"steps"`
}

type claudeStep struct {
	// Prompt, when set, is what the person types at the terminal before
	// the step's calls go, its lines each ended by Return.
	Prompt string       `json:"prompt,omitempty"`
	Calls  []claudeCall `json:"calls"`
}

// claudeCall is one hook call: the event, its payload as Claude Code's
// hooks get it on stdin, and how long after the step's previous call, or
// its prompt, Claude Code makes it.
type claudeCall struct {
	Event   string          `json:"event"`
	After   time.Duration   `json:"after"`
	Payload json.RawMessage `json:"payload"`
}

// claudeEvent is a line of the stand-in's log.
type claudeEvent struct {
	Step  int    `json:"step"`
	What  string `json:"what"` // ready, waiting, typed, call, done or error
	Event string `json:"event,omitempty"`
	Hooks int    `json:"hooks,omitempty"`
	Exit  []int  `json:"exit,omitempty"`
	Out   string `json:"out,omitempty"`
	Err   string `json:"err,omitempty"`
	AtMS  int64  `json:"at_ms"`
}

// writePlan makes the session from the capture's first fourteen calls, a
// session of three prompts (an answer, a pasted spec, an edit and a test
// run), ending with /exit and the capture's SessionEnd, all moved to the
// rehearsal's home folder and project. Claude Code's own pauses between
// calls of a turn are kept, up to 12 s each.
func (r *rehearsal) writePlan(t *testing.T) claudePlan {
	t.Helper()
	dir := filepath.Join(r.root, captureDir)
	type captured struct {
		event, prompt, promptID, session, transcript string
		at                                           int64
		payload                                      []byte
	}
	read := func(n string) captured {
		stdin, err := os.ReadFile(filepath.Join(dir, n, "stdin"))
		if err != nil {
			t.Fatal(err)
		}
		stamp, err := os.ReadFile(filepath.Join(dir, n, "time"))
		if err != nil {
			t.Fatal(err)
		}
		var c captured
		for _, line := range strings.Split(string(stamp), "\n") {
			if v, ok := strings.CutPrefix(line, "unix_ns="); ok {
				c.at, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			}
		}
		c.payload = r.moved(stdin)
		var p struct {
			Event      string `json:"hook_event_name"`
			Prompt     string `json:"prompt"`
			PromptID   string `json:"prompt_id"`
			Session    string `json:"session_id"`
			Transcript string `json:"transcript_path"`
		}
		if err := json.Unmarshal(c.payload, &p); err != nil {
			t.Fatalf("capture call %s: %v", n, err)
		}
		c.event, c.prompt, c.promptID, c.session, c.transcript = p.Event, p.Prompt, p.PromptID, p.Session, p.Transcript
		return c
	}
	turn := func(names ...string) claudeStep {
		var s claudeStep
		var prev int64
		for i, n := range names {
			c := read(n)
			var after time.Duration
			if i > 0 {
				after = max(0, min(time.Duration(c.at-prev), 12*time.Second))
			}
			prev = c.at
			if c.event == "UserPromptSubmit" && s.Prompt == "" {
				s.Prompt = c.prompt
			}
			s.Calls = append(s.Calls, claudeCall{Event: c.event, After: after, Payload: c.payload})
		}
		return s
	}
	plan := claudePlan{Steps: []claudeStep{
		turn("000001"),
		turn("000002", "000003", "000004"),
		turn("000005", "000006"),
		turn("000007", "000008", "000009", "000010", "000011", "000012", "000013", "000014"),
	}}
	for k := 1; k <= 3; k++ {
		if plan.Steps[k].Prompt == "" {
			t.Fatalf("step %d of the plan has no prompt", k)
		}
	}
	// /exit: the capture's SessionEnd of its third session, as this one's.
	first, last := read("000001"), read("000014")
	var end map[string]any
	if err := json.Unmarshal(read("000085").payload, &end); err != nil {
		t.Fatal(err)
	}
	if end["hook_event_name"] != "SessionEnd" {
		t.Fatalf("capture call 000085 is %v, not SessionEnd", end["hook_event_name"])
	}
	end["session_id"], end["transcript_path"], end["prompt_id"] = first.session, first.transcript, last.promptID
	b, err := json.Marshal(end)
	if err != nil {
		t.Fatal(err)
	}
	plan.Steps = append(plan.Steps, claudeStep{Prompt: "/exit", Calls: []claudeCall{{Event: "SessionEnd", Payload: b}}})
	b, err = json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.planFile, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return plan
}

// moved is a captured payload with the capture's paths replaced by the
// rehearsal's: the project, Claude Code's home under the teen's, and the
// folder Claude Code names after the project for its transcripts.
func (r *rehearsal) moved(payload []byte) []byte {
	s := string(payload)
	s = strings.ReplaceAll(s, strings.ReplaceAll(captureProject, "/", "-"), strings.ReplaceAll(r.project, "/", "-"))
	s = strings.ReplaceAll(s, captureClaudeHome, filepath.Join(r.home, ".claude"))
	s = strings.ReplaceAll(s, captureProject, r.project)
	return []byte(s)
}

// readClaudeLog reads the stand-in's log.
func (r *rehearsal) readClaudeLog() []claudeEvent {
	b, _ := os.ReadFile(r.claudeLog)
	var out []claudeEvent
	for _, line := range strings.Split(string(b), "\n") {
		var e claudeEvent
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// hasClaude reports whether log has a line for step that says what.
func hasClaude(log []claudeEvent, step int, what string) bool {
	return slices.ContainsFunc(log, func(e claudeEvent) bool { return e.Step == step && e.What == what })
}

// claudeAt reports whether the stand-in's last log line is step's what.
func (r *rehearsal) claudeAt(step int, what string) bool {
	log := r.readClaudeLog()
	return len(log) > 0 && log[len(log)-1].Step == step && log[len(log)-1].What == what
}

// waitClaude waits until ok says yes to the stand-in's log, and ends the
// step if the stand-in reports an error first.
func (r *rehearsal) waitClaude(t *testing.T, timeout time.Duration, what string, ok func([]claudeEvent) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		log := r.readClaudeLog()
		for _, e := range log {
			if e.What == "error" {
				t.Fatalf("the stand-in claude, at step %d: %s", e.Step, e.Err)
			}
		}
		if ok(log) {
			return
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(r.claudeLog)
			t.Fatalf("no %s within %v; claude's log:\n%s", what, timeout, b)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitCalls waits until the stand-in has made every call of step, and
// checks that each ran pairtime's hook and that every hook exited 0, as
// Claude Code needs it to.
func (r *rehearsal) waitCalls(t *testing.T, step int, timeout time.Duration) {
	t.Helper()
	want := len(r.plan.Steps[step].Calls)
	var calls []claudeEvent
	r.waitClaude(t, timeout, fmt.Sprintf("the %d hook calls of step %d", want, step), func(log []claudeEvent) bool {
		calls = calls[:0]
		for _, e := range log {
			if e.Step == step && e.What == "call" {
				calls = append(calls, e)
			}
		}
		return len(calls) == want
	})
	for _, c := range calls {
		if c.Hooks == 0 || slices.ContainsFunc(c.Exit, func(code int) bool { return code != 0 }) {
			t.Errorf("Claude Code's %s ran %d hooks, which exited %v:\n%s", c.Event, c.Hooks, c.Exit, c.Out)
		}
	}
}

// runClaude is the stand-in claude: it reads Claude Code's settings.json
// in the home folder, as Claude Code does at start, and makes each step's
// hook calls the way Claude Code does, the hook's own command with the
// payload on stdin, once the person has typed the step's prompt. It ends
// after the last step, as claude does after /exit.
func runClaude(planFile, logFile string) int {
	logw, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer logw.Close()
	emit := func(e claudeEvent) {
		e.AtMS = time.Now().UnixMilli()
		b, _ := json.Marshal(e)
		logw.Write(append(b, '\n'))
	}
	fail := func(step int, err error) int {
		emit(claudeEvent{Step: step, What: "error", Err: err.Error()})
		fmt.Println("claude:", err)
		return 1
	}
	b, err := os.ReadFile(planFile)
	if err != nil {
		return fail(-1, err)
	}
	var plan claudePlan
	if err := json.Unmarshal(b, &plan); err != nil {
		return fail(-1, err)
	}
	settings, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"))
	if err != nil {
		return fail(-1, err)
	}
	hooks, err := parseHooks(settings)
	if err != nil {
		return fail(-1, err)
	}
	emit(claudeEvent{Step: -1, What: "ready"})
	fmt.Println("Claude Code (pairtime's rehearsal stand-in)")
	in := bufio.NewReader(os.Stdin)
	for i, s := range plan.Steps {
		if s.Prompt != "" {
			emit(claudeEvent{Step: i, What: "waiting"})
			fmt.Print("> ")
			want := strings.Split(s.Prompt, "\n")
			var got []string
			for len(got) < len(want) {
				line, err := in.ReadString('\n')
				if err != nil {
					return fail(i, fmt.Errorf("reading the prompt: %w", err))
				}
				got = append(got, strings.TrimRight(line, "\r\n"))
			}
			if typed := strings.Join(got, "\n"); typed != s.Prompt {
				return fail(i, fmt.Errorf("the terminal got %q, want %q", typed, s.Prompt))
			}
			emit(claudeEvent{Step: i, What: "typed"})
		}
		for _, c := range s.Calls {
			time.Sleep(c.After)
			ran, exits, out := runHooks(hooks, c)
			emit(claudeEvent{Step: i, What: "call", Event: c.Event, Hooks: ran, Exit: exits, Out: out})
			if c.Event == "Stop" {
				var p struct {
					Message string `json:"last_assistant_message"`
				}
				json.Unmarshal(c.Payload, &p)
				fmt.Printf("\n%s\n\n", p.Message)
			}
		}
	}
	emit(claudeEvent{Step: len(plan.Steps), What: "done"})
	return 0
}

// hookGroup is one matcher's hooks in Claude Code's settings.json.
type hookGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Timeout float64  `json:"timeout"`
	} `json:"hooks"`
}

func parseHooks(settings []byte) (map[string][]hookGroup, error) {
	var s struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, err
	}
	return s.Hooks, nil
}

// runHooks runs the hooks settings.json gives call's event whose matcher
// takes it: a tool event's tool name, or SessionStart's source.
func runHooks(hooks map[string][]hookGroup, c claudeCall) (ran int, exits []int, out string) {
	var p struct {
		Cwd    string `json:"cwd"`
		Tool   string `json:"tool_name"`
		Source string `json:"source"`
	}
	json.Unmarshal(c.Payload, &p)
	target := p.Tool
	if c.Event == "SessionStart" {
		target = p.Source
	}
	var outs []string
	for _, g := range hooks[c.Event] {
		if g.Matcher != "" && g.Matcher != "*" {
			if ok, err := regexp.MatchString("^(?:"+g.Matcher+")$", target); err != nil || !ok {
				continue
			}
		}
		for _, h := range g.Hooks {
			if h.Type != "command" {
				continue
			}
			code, o := runHook(h.Command, h.Args, h.Timeout, p.Cwd, c.Payload)
			ran++
			exits = append(exits, code)
			if code != 0 || o != "" {
				outs = append(outs, o)
			}
		}
	}
	return ran, exits, strings.Join(outs, "\n")
}

// runHook runs one hook as Claude Code does: in the project's folder, the
// command with its args, or through sh when it has none, with the
// payload on stdin and Claude Code's own variables added.
func runHook(command string, args []string, timeoutS float64, dir string, payload []byte) (int, string) {
	timeout := 60 * time.Second
	if timeoutS > 0 {
		timeout = time.Duration(timeoutS * float64(time.Second))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if len(args) > 0 {
		cmd = exec.CommandContext(ctx, command, args...)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
	}
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, claudePlanEnv+"=") && !strings.HasPrefix(kv, claudeLogEnv+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "CLAUDECODE=1", "CLAUDE_PROJECT_DIR="+dir)
	cmd.Stdin = bytes.NewReader(payload)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return ee.ExitCode(), out.String()
	case err != nil:
		return -1, out.String() + err.Error()
	}
	return 0, out.String()
}

// Files and processes.

// waitFile returns name's contents once it has some, or "" after timeout.
func waitFile(name string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		if b, err := os.ReadFile(name); err == nil && len(b) > 0 {
			return string(b)
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitGone reports whether process pid has gone within timeout.
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil && strings.Contains(string(stat), ") Z ") {
			return true // exited, and only its parent's wait is left
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// leftIn lists what is in dir, its subfolders' contents included, apart
// from keep, what it holds, and the folders on the way to it.
func leftIn(dir, keep string) ([]string, error) {
	var left []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if keep != "" && p == keep {
			return filepath.SkipDir
		}
		if keep != "" && d.IsDir() && strings.HasPrefix(keep, p+string(filepath.Separator)) {
			return nil // a folder keep is in: look inside, but it isn't left over itself
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			rel += "/"
		}
		left = append(left, rel)
		return nil
	})
	return left, err
}

// runningFrom lists the processes whose executable is in dir.
func runningFrom(dir string) []string {
	var out []string
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err == nil && strings.HasPrefix(exe, dir+string(filepath.Separator)) {
			out = append(out, fmt.Sprintf("%d %s", pid, exe))
		}
	}
	return out
}

// snapshot lists a repository's files with a hash of each, leaving out
// .git/index, which a git command that only reads may still refresh.
func snapshot(dir string) (string, error) {
	var b strings.Builder
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() || rel == filepath.Join(".git", "index") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s %s\n", hex.EncodeToString(sum[:8]), rel)
		return nil
	})
	return b.String(), err
}

// JSON as maps.

// at returns the value at path in m, or nil.
func at(m map[string]any, path ...string) any {
	var v any = m
	for _, k := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

func str(m map[string]any, path ...string) string {
	s, _ := at(m, path...).(string)
	return s
}

func num(m map[string]any, path ...string) float64 {
	f, _ := at(m, path...).(float64)
	return f
}

// clip shortens a page for a failure message.
func clip(s string) string {
	const limit = 4000
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// syncBuffer collects a process's output for a failing test to print.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
