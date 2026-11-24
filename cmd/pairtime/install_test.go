// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/install"
)

// hookModeEnv, when set, makes the test binary run pairtime with its hook
// command changed (runWithHookMode), in the process it starts and in every
// process that one starts in turn: the installed copy, the agent and the
// hook self-test.
const hookModeEnv = "PAIRTIME_TEST_HOOK"

// holdEnv, when set, makes the test binary wait until its stdin ends and
// exit, as a stand-in for a hook Claude Code runs, which keeps the binary
// in use meanwhile.
const holdEnv = "PAIRTIME_TEST_HOLD"

// keyringEnv names a folder in which the processes the cycle test starts
// keep device keys, as the OS's secret store would (testKeyring). Unlike
// the key file the other tests use, it is outside the data directory, so
// only keys.Destroy deletes a key from it. keyringFailEnv makes its
// deletes fail, as a locked keychain's do.
const (
	keyringEnv     = "PAIRTIME_TEST_KEYRING"
	keyringFailEnv = "PAIRTIME_TEST_KEYRING_FAIL"
)

// testKeyring is a keys.Store that keeps each secret in a file of its own
// in dir. It says it is the Secret Service, so keystore.json names a store
// outside the data directory.
type testKeyring struct {
	dir  string
	fail bool // Delete fails
}

func (k testKeyring) Kind() keys.Kind { return keys.KindSecretService }

func (k testKeyring) Get(name string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(k.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, keys.ErrNotFound
	}
	return b, err
}

func (k testKeyring) Set(name string, secret []byte) error {
	return os.WriteFile(filepath.Join(k.dir, name), secret, 0o600)
}

func (k testKeyring) Delete(name string) error {
	if k.fail {
		return errors.New("the test keyring is locked")
	}
	err := os.Remove(filepath.Join(k.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return keys.ErrNotFound
	}
	return err
}

func init() {
	helpers[hookModeEnv] = runWithHookMode
	helpers[holdEnv] = func(string) int {
		io.Copy(io.Discard, os.Stdin)
		return 0
	}
}

// runWithHookMode runs pairtime with its own arguments after changing its
// hook command as mode says:
//
//   - real keeps the binary's own `pairtime hook` (WP-AG3);
//   - none takes the hook command away;
//   - broken makes it print an answer and exit 2, as a usage error would.
func runWithHookMode(mode string) int {
	if dir := os.Getenv(keyringEnv); dir != "" {
		agentKeyStores = []keys.Store{testKeyring{dir: dir, fail: os.Getenv(keyringFailEnv) != ""}}
	}
	switch mode {
	case "real":
	case "none":
		delete(commands, "hook")
	case "broken":
		commands["hook"] = command{name: "hook", summary: "test: broken", run: func(_ []string, stdout, _ io.Writer) int {
			fmt.Fprintln(stdout, `{"decision": "block"}`)
			return 2
		}}
	}
	return start(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

// testHome is a home directory of its own, with the temporary and runtime
// folders the OS would give its user, for install and uninstall to run in
// as they would on a teen's machine. Its path is short, because the agent's
// socket goes in it and macOS takes socket paths of 103 bytes at most.
type testHome struct {
	t    *testing.T
	root string    // holds home, tmp and run
	home string    // the home directory
	dirs home.Dirs // pairtime's directories in it, where home puts them
	bin  string    // the binary's per-user path in it
	env  []string  // the environment of every process the test starts
}

func newTestHome(t *testing.T, hookMode string) *testHome {
	t.Helper()
	root, err := os.MkdirTemp("", "pt")
	if err != nil {
		t.Fatal(err)
	}
	h := &testHome{t: t, root: root, home: filepath.Join(root, "home")}
	tmp, run := filepath.Join(root, "tmp"), filepath.Join(root, "run")
	for _, d := range []string{h.home, tmp, run, h.keyring()} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	drop := []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "TMPDIR", "TEMP", "TMP", "PATH", "XDG_DATA_HOME", "XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR", "XDG_STATE_HOME", "XDG_CACHE_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "WAKATIME_HOME", home.EnvHome, runEnv, hookModeEnv, holdEnv,
		keyringEnv, keyringFailEnv, wakatimeEnv, "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !slices.ContainsFunc(drop, func(d string) bool { return strings.EqualFold(d, k) }) {
			h.env = append(h.env, kv)
		}
	}
	switch runtime.GOOS {
	case "windows":
		local, roaming := filepath.Join(h.home, "AppData", "Local"), filepath.Join(h.home, "AppData", "Roaming")
		// Windows PowerShell, which deletes a binary that uninstall ran
		// from, keeps a startup cache here (psCache), as it does for any
		// user who runs it.
		for _, d := range []string{local, roaming, filepath.Join(local, psCacheDir)} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		sys := os.Getenv("SystemRoot")
		path := strings.Join([]string{filepath.Join(sys, "System32"), sys, filepath.Join(sys, "System32", "WindowsPowerShell", "v1.0")}, ";")
		// The temporary folder is the snapshot's too, so a folder the hook
		// self-test leaves in it fails the test, as it would stay in a
		// teen's profile, where %TEMP% is.
		h.env = append(h.env, "USERPROFILE="+h.home, "LOCALAPPDATA="+local, "APPDATA="+roaming, "TEMP="+tmp, "TMP="+tmp, "PATH="+path)
		data := filepath.Join(local, "pairtime")
		h.dirs = home.Dirs{Data: data, Config: filepath.Join(roaming, "pairtime"), Runtime: data}
		h.bin = filepath.Join(data, "bin", "pairtime.exe")
	case "darwin":
		h.env = append(h.env, "HOME="+h.home, "TMPDIR="+tmp, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
		data := filepath.Join(h.home, "Library", "Application Support", "pairtime")
		h.dirs = home.Dirs{Data: data, Config: data, Runtime: filepath.Join(tmp, "pairtime")}
		h.bin = filepath.Join(h.home, ".local", "bin", "pairtime")
	default:
		h.env = append(h.env, "HOME="+h.home, "TMPDIR="+tmp, "XDG_RUNTIME_DIR="+run, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
		h.dirs = home.Dirs{Data: filepath.Join(h.home, ".local", "share", "pairtime"), Config: filepath.Join(h.home, ".config", "pairtime"), Runtime: filepath.Join(run, "pairtime")}
		h.bin = filepath.Join(h.home, ".local", "bin", "pairtime")
	}
	h.env = append(h.env, hookModeEnv+"="+hookMode, keyringEnv+"="+h.keyring())
	t.Cleanup(h.cleanup)
	return h
}

// keyring is the folder the test's processes keep device keys in
// (testKeyring), in the snapshot, so a key that uninstall leaves fails the
// test.
func (h *testHome) keyring() string { return filepath.Join(h.root, "keyring") }

// waitForKey waits up to 30 s for the agent to have put a device key in
// the test's keyring, which it does in the background once it has started.
func (h *testHome) waitForKey() {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if entries, err := os.ReadDir(h.keyring()); err == nil && len(entries) > 0 {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatal("the agent put no device key in the keyring within 30 s of install")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// cleanup stops an agent the test left running, takes away a Run value it
// left on Windows, and deletes the folders.
func (h *testHome) cleanup() {
	if err := stopAgent(h.dirs); err != nil {
		h.t.Errorf("cleaning up: %v", err)
		if pf, err := readPID(h.dirs); err == nil && pf > 0 {
			if p, err := os.FindProcess(pf); err == nil {
				p.Kill()
			}
		}
	}
	if v, ok := runValue(); ok && strings.Contains(v, h.root) {
		deleteRunValue()
	}
	removeTemp(h.root)
}

// readPID returns the pid port.json names.
func readPID(dirs home.Dirs) (int, error) {
	data, err := os.ReadFile(dirs.PortFile())
	if err != nil {
		return 0, err
	}
	var pf struct {
		PID int `json:"pid"`
	}
	err = json.Unmarshal(data, &pf)
	return pf.PID, err
}

// path returns the path of rel, a slash-separated path in the home
// directory.
func (h *testHome) path(rel string) string { return filepath.Join(h.home, filepath.FromSlash(rel)) }

func (h *testHome) write(rel, data string) {
	h.t.Helper()
	p := h.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *testHome) read(rel string) string {
	h.t.Helper()
	data, err := os.ReadFile(h.path(rel))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		h.t.Fatal(err)
	}
	return string(data)
}

// run runs `pairtime <args>` from exe, the test binary when "", with stdin
// as its input, and returns its exit status and everything it printed.
func (h *testHome) run(exe, stdin string, args ...string) (int, string) {
	h.t.Helper()
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			h.t.Fatal(err)
		}
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = h.env
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	err := waitWithin(h.t, cmd, 3*time.Minute)
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		h.t.Fatalf("%s: %v", strings.Join(args, " "), err)
	}
	return cmd.ProcessState.ExitCode(), out.String()
}

// mustRun runs pairtime as run does and fails the test unless it exits
// with want.
func (h *testHome) mustRun(exe, stdin string, want int, args ...string) string {
	h.t.Helper()
	code, out := h.run(exe, stdin, args...)
	if code != want {
		h.t.Fatalf("pairtime %s exited with %d, want %d; it printed:\n%s", strings.Join(args, " "), code, want, out)
	}
	return out
}

// mode returns h with the processes it starts running pairtime with its
// hook command as mode says (runWithHookMode).
func (h *testHome) mode(mode string) *testHome { return h.withEnv(hookModeEnv, mode) }

// withEnv returns h with key set to value in the environment of the
// processes it starts.
func (h *testHome) withEnv(key, value string) *testHome {
	c := *h
	c.env = setEnv(slices.Clone(h.env), key, value)
	return &c
}

// copyBinary puts a copy of the test binary at path, with the bytes extra
// added at its end, which leave it a pairtime binary that runs as the test
// binary does: with extra it is another build of pairtime, as far as
// install can tell. It renames the copy into place, trying again for a
// while, since Windows may keep the file of a process that just ended in
// use a moment longer.
func (h *testHome) copyBinary(path, extra string) {
	h.t.Helper()
	exe, err := os.Executable()
	if err != nil {
		h.t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	tmp := path + ".copy"
	if err := os.WriteFile(tmp, append(data, extra...), 0o755); err != nil {
		h.t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := os.Rename(tmp, path)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// sum returns the SHA-256 of the file at path, in hex.
func (h *testHome) sum(path string) string {
	h.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// waitFor waits up to 30 s for path to be there, as keys/keystore.json is
// once the agent has opened its ledger, which it does in the background.
func (h *testHome) waitFor(path string) {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%s isn't there 30 s after install", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// hold starts exe as a process that runs until the function hold returns
// is called, as a hook Claude Code runs keeps the binary in use.
func (h *testHome) hold(exe string) func() {
	h.t.Helper()
	cmd := exec.Command(exe)
	cmd.Env = append(withoutEnv(h.env, hookModeEnv), holdEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		h.t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	return func() {
		stdin.Close()
		if err := waitWithin(h.t, cmd, time.Minute); err != nil {
			h.t.Errorf("the process that held %s: %v\n%s", exe, err, out.String())
		}
	}
}

// snapshot returns everything in the test's folders, with what each path
// is: a folder, a link and where it points, or a file's mode and SHA-256.
// On Windows it adds pairtime's Run value, which is the login item there.
func (h *testHome) snapshot() map[string]string {
	h.t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(h.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(h.root, p)
		if isPSCache(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if runtime.GOOS == "windows" {
			mode = 0 // Windows has ACLs, not modes
		}
		switch {
		case d.IsDir():
			m[rel] = fmt.Sprintf("folder %o", mode)
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			m[rel] = "link to " + target
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			m[rel] = fmt.Sprintf("file %o %x", mode, sha256.Sum256(data))
		}
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
	if v, ok := runValue(); ok {
		m[`HKCU\`+install.RunKey+`\`+install.RunValue] = v
	}
	return m
}

// psCacheDir is where in LOCALAPPDATA Windows PowerShell keeps the cache
// it writes at every start, StartupProfileData-NonInteractive among them.
// It is PowerShell's file, in every account that has run PowerShell, so
// the snapshots leave it out.
var psCacheDir = filepath.Join("Microsoft", "Windows", "PowerShell")

// isPSCache reports whether rel, a path in a test's folders, is one of
// PowerShell's startup caches.
func isPSCache(rel string) bool {
	return runtime.GOOS == "windows" && strings.HasPrefix(filepath.Base(rel), "StartupProfileData-") &&
		strings.HasSuffix(filepath.Dir(rel), psCacheDir)
}

// changes lists how after differs from before, one path a line.
func changes(before, after map[string]string) string {
	var lines []string
	for p, v := range after {
		if w, ok := before[p]; !ok {
			lines = append(lines, "added "+p+": "+v)
		} else if w != v {
			lines = append(lines, "changed "+p+": "+w+" became "+v)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			lines = append(lines, "gone "+p)
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// waitForTree waits up to a minute for the test's folders to be as they
// were in want, and returns how they still differ, "" for not at all. A
// binary that uninstall ran from on Windows is deleted by PowerShell once
// that uninstall has exited, a moment later.
func (h *testHome) waitForTree(want map[string]string) string {
	h.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		diff := changes(want, h.snapshot())
		if diff == "" || time.Now().After(deadline) {
			return diff
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// waitGone waits up to a minute for the file at path to be gone, as a
// binary that uninstall ran from on Windows is once that uninstall has
// exited.
func (h *testHome) waitGone(path string) {
	h.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%s is still there a minute after uninstall", path)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// hooked returns the commands of the hook handlers in Claude Code's
// settings that run the installed binary.
func (h *testHome) hooked() []string {
	h.t.Helper()
	data := h.read(".claude/settings.json")
	if data == "" {
		return nil
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(data), &s); err != nil {
		h.t.Fatalf("Claude Code's settings aren't JSON any more: %v\n%s", err, data)
	}
	var events []string
	for event, groups := range s.Hooks {
		for _, g := range groups {
			for _, hk := range g.Hooks {
				if hk.Command == h.bin && slices.Equal(hk.Args, []string{"hook", "--harness", "claude_code", "--event", event}) {
					events = append(events, event)
				}
			}
		}
	}
	slices.Sort(events)
	return events
}

// claudeEvents are the events pairtime hooks in Claude Code (DESIGN 5.1),
// sorted.
var claudeEvents = []string{"Notification", "PermissionRequest", "PostToolUse", "SessionEnd", "SessionStart",
	"Stop", "StopFailure", "SubagentStart", "SubagentStop", "UserPromptSubmit"}

// autostart returns the login item, "" when there is none.
func (h *testHome) autostart() string {
	h.t.Helper()
	switch runtime.GOOS {
	case "windows":
		v, _ := runValue()
		return v
	case "darwin":
		return h.read("Library/LaunchAgents/dev.pairtime.agent.plist")
	default:
		return h.read(".config/autostart/pairtime.desktop")
	}
}

// installed checks what install leaves: the binary at its per-user path,
// the login item, the agent answering, and pairtime's hooks in Claude
// Code's settings, all of them when hooks is true and none otherwise. It
// returns the agent's pid.
func (h *testHome) installed(hooks bool) int {
	h.t.Helper()
	if !isPairtimeBinary(h.bin) {
		h.t.Errorf("%s isn't a pairtime binary", h.bin)
	}
	if a := h.autostart(); !strings.Contains(a, h.bin) {
		h.t.Errorf("the login item doesn't run %s: %q", h.bin, a)
	}
	st, err := queryAgent(h.dirs)
	if err != nil {
		h.t.Fatalf("the agent doesn't answer: %v", err)
	}
	got := h.hooked()
	switch {
	case hooks && !slices.Equal(got, claudeEvents):
		h.t.Errorf("pairtime hooks %q in Claude Code, want %q", got, claudeEvents)
	case !hooks && len(got) > 0:
		h.t.Errorf("pairtime hooks %q in Claude Code, want none", got)
	}
	return st.PID
}

// uninstalled checks that the test's folders are as they were in before,
// and that the agent pid is gone.
func (h *testHome) uninstalled(before map[string]string, pid int) {
	h.t.Helper()
	if diff := h.waitForTree(before); diff != "" {
		h.t.Errorf("after uninstall the files differ from before install:\n%s", diff)
	}
	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			h.t.Errorf("the agent, process %d, is still running after uninstall", pid)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// gitAISettings is a Claude Code settings file in the shape git-ai and
// other tools leave it (HS 13.3), with CRLF line endings and a token in
// its env block, which install must keep byte for byte around its own
// hooks and uninstall must give back.
const gitAISettings = "{\r\n" +
	"  \"env\": {\r\n" +
	"    \"ANTHROPIC_AUTH_TOKEN\": \"sk-ant-test-token\"\r\n" +
	"  },\r\n" +
	"  \"hooks\": {\r\n" +
	"    \"PostToolUse\": [\r\n" +
	"      {\r\n" +
	"        \"hooks\": [\r\n" +
	"          {\r\n" +
	"            \"command\": \"/home/teen/.git-ai/bin/git-ai checkpoint claude --hook-input stdin\",\r\n" +
	"            \"type\": \"command\"\r\n" +
	"          }\r\n" +
	"        ],\r\n" +
	"        \"matcher\": \"*\"\r\n" +
	"      }\r\n" +
	"    ]\r\n" +
	"  }\r\n" +
	"}\r\n"

// TestInstallCycle runs the real install and uninstall commands in
// processes of their own, each case in a home directory of its own, and
// checks that uninstall leaves every file as it was before install, with
// no agent running (docs/PLAN.md, WP-IN2). The agent each install starts
// is a real one.
func TestInstallCycle(t *testing.T) {
	// The registry isn't in the test's home directory, and the test's
	// install would take over a login item of the account's own pairtime
	// and its uninstall delete it.
	if v, ok := runValue(); ok {
		t.Skipf(`this account has a pairtime login item, HKCU\%s\%s = %q, which the test would replace; it runs where there is none, as in CI`, install.RunKey, install.RunValue, v)
	}
	tests := []struct {
		name     string
		hookMode string
		// before sets up the home directory before the snapshot.
		before func(h *testHome)
		// steps runs the commands and checks what they did.
		steps func(t *testing.T, h *testHome, before map[string]string)
	}{
		{
			name:     "install, a second install, uninstall from the installed binary and a reinstall leave every file as it was",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				out := h.mustRun("", "", 0, "install", "--yes")
				pid := h.installed(true)
				settings, auto := h.read(".claude/settings.json"), h.autostart()
				if !strings.Contains(settings, "sk-ant-test-token") || !strings.Contains(settings, "git-ai checkpoint") || !strings.Contains(settings, "\r\n") {
					t.Errorf("install didn't keep the other tool's hooks, the token or the CRLF line endings:\n%s", settings)
				}
				if strings.Contains(out, "sk-ant-test-token") {
					t.Errorf("install printed the token:\n%s", out)
				}
				// The test's PATH doesn't have the binary's folder, on
				// Windows either.
				if !strings.Contains(out, "isn't on your PATH") {
					t.Errorf("install didn't say that %s isn't on the PATH:\n%s", filepath.Dir(h.bin), out)
				}

				out = h.mustRun("", "", 0, "install", "--yes")
				if h.read(".claude/settings.json") != settings || h.autostart() != auto {
					t.Errorf("a second install changed Claude Code's settings or the login item")
				}
				if !strings.Contains(out, fmt.Sprintf("agent is running, process %d", pid)) {
					t.Errorf("a second install didn't find the agent running as process %d:\n%s", pid, out)
				}

				h.mustRun(h.bin, "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)

				h.mustRun("", "", 0, "install", "--yes")
				pid = h.installed(true)
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "uninstall without install's records takes out the hooks, the login item, the binary and the data",
			hookMode: "real",
			before: func(h *testHome) {
				h.write(".claude/settings.json", gitAISettings)
				// The folders install makes above its own, which only its
				// records would let uninstall take away.
				for _, d := range []string{".local/bin", ".local/share", ".config/autostart", "Library/LaunchAgents", "Library/Application Support"} {
					if err := os.MkdirAll(h.path(d), 0o700); err != nil {
						t.Fatal(err)
					}
				}
			},
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				h.mustRun("", "", 0, "install", "--yes")
				pid := h.installed(true)
				if err := os.RemoveAll(filepath.Join(h.dirs.Data, "install")); err != nil {
					t.Fatal(err)
				}
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "uninstall deletes the device key and the records, keeps the daily totals and recaps when the teen says so, and a later uninstall deletes those",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				h.mustRun("", "", 0, "install", "--yes")
				pid := h.installed(true)
				h.waitForKey()
				// What the scorer and the coach will write (DESIGN 3.5),
				// which DESIGN 4.4 keeps until the teen deletes it.
				kept := map[string]string{"days/2026-11-20.json": `{"date":"2026-11-20"}` + "\n", "coach/recaps.jsonl": `{"recap":1}` + "\n"}
				for rel, body := range kept {
					p := filepath.Join(h.dirs.Data, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				out := h.mustRun("", "y\nn\n", 0, "uninstall")
				if !strings.Contains(out, "Your daily totals and recaps can stay") || !strings.Contains(out, "Your daily totals and recaps stay in") {
					t.Errorf("uninstall didn't ask about the daily totals and recaps, or didn't say where they stay:\n%s", out)
				}
				// What a backup or a synced folder holds, uninstall can't reach.
				if !strings.Contains(out, "Copies a backup or a synced folder already holds stay as they are") || strings.Contains(out, "nobody can bring") {
					t.Errorf("uninstall doesn't say backups keep their copies:\n%s", out)
				}
				h.waitGone(h.bin)
				for rel, body := range kept {
					if got, err := os.ReadFile(filepath.Join(h.dirs.Data, filepath.FromSlash(rel))); err != nil || string(got) != body {
						t.Errorf("uninstall didn't keep %s as it was: %q, %v", rel, got, err)
					}
				}
				// The device key, the ledger and the rest go (D-uninstall-key):
				// only the daily totals and the recaps stay.
				if left, err := os.ReadDir(h.keyring()); err != nil || len(left) > 0 {
					t.Errorf("uninstall left the device key in the keyring: %v, %v", left, err)
				}
				entries, err := os.ReadDir(h.dirs.Data)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range entries {
					if n := e.Name(); n != "days" && n != "coach" {
						t.Errorf("uninstall left %s in the data directory, which is neither the daily totals nor the recaps", n)
					}
				}
				for p := range h.snapshot() {
					full := filepath.Join(h.root, p)
					if _, ok := before[p]; !ok && !within(full, h.dirs.Data) && !within(full, h.dirs.Config) && !within(h.dirs.Data, full) && !within(h.dirs.Config, full) {
						t.Errorf("uninstall left %s, which isn't pairtime's data", p)
					}
				}
				if h.hooked() != nil || h.autostart() != "" {
					t.Errorf("uninstall left hooks %q or the login item %q", h.hooked(), h.autostart())
				}
				// Only the two folders stayed, without install's record of
				// the folders it made, so a later uninstall leaves those
				// above the data directory, empty.
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.waitGone(h.dirs.Data)
				for _, line := range strings.Split(changes(before, h.snapshot()), "\n") {
					p, what, _ := strings.Cut(strings.TrimPrefix(line, "added "), ": ")
					if line != "" && !(strings.HasPrefix(line, "added ") && strings.HasPrefix(what, "folder") && within(h.dirs.Data, filepath.Join(h.root, p))) {
						t.Errorf("after the later uninstall: %s", line)
					}
				}
				if processAlive(pid) {
					t.Errorf("the agent, process %d, is still running after uninstall", pid)
				}
			},
		},
		{
			name:     "a device key uninstall can't delete leaves pairtime and its data in place, so a second uninstall can finish",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				h.mustRun("", "", 0, "install", "--yes")
				pid := h.installed(true)
				h.waitForKey()
				out := h.withEnv(keyringFailEnv, "1").mustRun("", "", 1, "uninstall", "--yes", "--delete-data")
				if !strings.Contains(out, "couldn't delete pairtime's device key") || !strings.Contains(out, "run pairtime uninstall again") {
					t.Errorf("uninstall didn't say what to do about the key it couldn't delete:\n%s", out)
				}
				for _, p := range []string{h.bin, filepath.Join(h.dirs.Data, "keys", "keystore.json")} {
					if _, err := os.Stat(p); err != nil {
						t.Errorf("uninstall deleted %s though the device key stayed: %v", p, err)
					}
				}
				if left, err := os.ReadDir(h.keyring()); err != nil || len(left) == 0 {
					t.Errorf("the device key isn't in the keyring any more: %v, %v", left, err)
				}
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "install without a hook command leaves Claude Code's settings alone and sets up the rest",
			hookMode: "none",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				out := h.mustRun("", "", 0, "install", "--yes")
				if !strings.Contains(out, "no hook command") {
					t.Errorf("install didn't say why it added no hooks:\n%s", out)
				}
				pid := h.installed(false)
				if h.read(".claude/settings.json") != gitAISettings {
					t.Errorf("install changed Claude Code's settings")
				}
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "install with a hook command that fails its self-test leaves Claude Code's settings alone",
			hookMode: "broken",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				out := h.mustRun("", "", 0, "install", "--yes")
				if !strings.Contains(out, "failed its self-test") {
					t.Errorf("install didn't say why it added no hooks:\n%s", out)
				}
				pid := h.installed(false)
				if h.read(".claude/settings.json") != gitAISettings {
					t.Errorf("install changed Claude Code's settings")
				}
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "unticking the hooks on the consent screen installs only the login item",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				out := h.mustRun("", "2\ny\n", 0, "install")
				if !strings.Contains(out, "Saying no doesn't stop you from using pairtime.") || !strings.Contains(out, "[ ] 2. Add pairtime's hooks to Claude Code") {
					t.Errorf("the consent screen didn't show, or didn't untick the hooks:\n%s", out)
				}
				if strings.Contains(out, "sk-ant-test-token") {
					t.Errorf("the consent screen shows the token:\n%s", out)
				}
				pid := h.installed(false)
				h.mustRun("", "y\nn\n", 0, "uninstall")
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "an install whose hook command fails its self-test leaves alone the pairtime that Claude Code's hooks run",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				h.mustRun("", "", 0, "install", "--yes")
				pid := h.installed(true)
				// The installed copy becomes another build of pairtime, so
				// an install from the test binary would replace it.
				if err := stopAgent(h.dirs); err != nil {
					t.Fatal(err)
				}
				h.copyBinary(h.bin, "\npairtime test: another build\n")
				bin, settings := h.sum(h.bin), h.read(".claude/settings.json")
				out := h.mode("broken").mustRun("", "", 1, "install", "--yes")
				if !strings.Contains(out, "failed its self-test") || !strings.Contains(out, "block your prompts") {
					t.Errorf("install didn't say why it left the installed pairtime alone:\n%s", out)
				}
				if h.sum(h.bin) != bin || h.read(".claude/settings.json") != settings {
					t.Error("install replaced the pairtime that Claude Code's hooks run, or changed their settings")
				}
				out = h.mode("broken").mustRun("", "", 1, "install", "--yes", "--skip", "hooks/claude_code")
				if !strings.Contains(out, "block your prompts") || h.sum(h.bin) != bin {
					t.Errorf("leaving the hooks out let install replace the pairtime that the hooks already there run:\n%s", out)
				}
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "a Claude Code settings file that pairtime can't edit keeps its hooks out and the rest is set up, and --skip doesn't read it",
			hookMode: "real",
			before: func(h *testHome) {
				h.write(".claude/settings.json", gitAISettings)
				if err := os.Chmod(h.path(".claude/settings.json"), 0o400); err != nil {
					h.t.Fatal(err)
				}
			},
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				out := h.mustRun("", "", 0, "install", "--yes")
				if !strings.Contains(out, "is read-only") {
					t.Errorf("install didn't say why Claude Code's hooks stay out:\n%s", out)
				}
				pid := h.installed(false)
				if h.read(".claude/settings.json") != gitAISettings {
					t.Error("install changed Claude Code's settings")
				}
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)

				out = h.mustRun("", "", 0, "install", "--yes", "--skip", "hooks/claude_code")
				if strings.Contains(out, "read-only") || strings.Contains(out, "stay out") {
					t.Errorf("install planned the hooks it was told to leave out:\n%s", out)
				}
				pid = h.installed(false)
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "install leaves alone a file at the binary's path that isn't pairtime",
			hookMode: "real",
			before: func(h *testHome) {
				h.write(".claude/settings.json", gitAISettings)
				if err := os.MkdirAll(filepath.Dir(h.bin), 0o755); err != nil {
					h.t.Fatal(err)
				}
				if err := os.WriteFile(h.bin, []byte("#!/bin/sh\necho the teen's own pairtime script\n"), 0o755); err != nil {
					h.t.Fatal(err)
				}
			},
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				out := h.mustRun("", "", 1, "install", "--yes")
				if !strings.Contains(out, "isn't pairtime") || !strings.Contains(out, "move it somewhere else") {
					t.Errorf("install didn't say what to do about the file in its way:\n%s", out)
				}
				if diff := changes(before, h.snapshot()); diff != "" {
					t.Errorf("install changed files:\n%s", diff)
				}
				if runtime.GOOS == "windows" {
					return // the binary's folder is in pairtime's data directory there, not one other tools share
				}
				if out := h.mustRun("", "", 0, "uninstall", "--yes"); !strings.Contains(out, "nothing to take out") {
					t.Errorf("uninstall found something to take out:\n%s", out)
				}
				if diff := changes(before, h.snapshot()); diff != "" {
					t.Errorf("uninstall changed files:\n%s", diff)
				}
			},
		},
		{
			name:     "the folders an install script made for the binary, named with --made, go with uninstall",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				// What install.sh and install.ps1 do before they run
				// pairtime install: make the binary's folders and put the
				// binary there.
				made := missingDirs(filepath.Dir(h.bin))
				h.copyBinary(h.bin, "")
				h.mustRun(h.bin, "", 2, "install", "--yes", "--made", "relative")
				h.mustRun(h.bin, "", 2, "install", "--yes", "--made", h.path("elsewhere"))
				args := []string{"install", "--yes"}
				for _, d := range made {
					args = append(args, "--made", d)
				}
				h.mustRun(h.bin, "", 0, args...)
				pid := h.installed(true)
				// On Windows the script made pairtime's data directory too,
				// with its parent's entries, and install made it private.
				checkPrivateDir(t, h.dirs.Data)
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "on Windows, install moves aside a pairtime.exe that a process runs, and uninstall deletes both copies",
			hookMode: "real",
			before: func(h *testHome) {
				h.write(".claude/settings.json", gitAISettings)
				h.copyBinary(filepath.Join(h.root, "download", "pairtime.exe"), "\npairtime test: a newer build\n")
			},
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				if runtime.GOOS != "windows" {
					t.Skip("elsewhere a rename replaces a program that runs")
				}
				h.mustRun("", "", 0, "install", "--yes")
				h.installed(true)
				done := h.hold(h.bin)
				newer := filepath.Join(h.root, "download", "pairtime.exe")
				h.mustRun(newer, "", 0, "install", "--yes")
				if h.sum(h.bin) != h.sum(newer) {
					t.Error("install didn't put the newer pairtime in place")
				}
				if old := oldBinaries(filepath.Dir(h.bin)); len(old) != 1 {
					t.Errorf("beside the binary install left %q, want the one old copy that a process runs", old)
				}
				pid := h.installed(true)
				done()
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			name:     "saying no on the consent screen, or a dry run, changes nothing",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				h.mustRun("", "n\n", exitDeclined, "install")
				h.mustRun("", "", exitDeclined, "install")
				out := h.mustRun("", "", 0, "install", "--dry-run")
				if !strings.Contains(out, "+++ "+h.path(".claude/settings.json")) || !strings.Contains(out, `"UserPromptSubmit"`) {
					t.Errorf("the dry run didn't print the hooks' diff:\n%s", out)
				}
				if diff := changes(before, h.snapshot()); diff != "" {
					t.Errorf("saying no or a dry run changed files:\n%s", diff)
				}
				if agentLocked(h.dirs) {
					t.Error("an agent runs after saying no")
				}
				out = h.mustRun("", "", 0, "uninstall", "--yes")
				if !strings.Contains(out, "nothing to take out") {
					t.Errorf("uninstall with nothing installed said:\n%s", out)
				}
			},
		},
		{
			name:     "a calibration volunteer's install turns shadow on in config.toml and changes no other byte, a second one changes nothing, and uninstall takes the line out with config.toml",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				if !config.Calibrating() {
					t.Skip("pairtime's scoring has reached 1.0.0, so --calibration-volunteer offers nothing (TestPlanCalibration)")
				}
				name := h.dirs.ConfigFile()
				read := func() string {
					t.Helper()
					data, err := os.ReadFile(name)
					if err != nil {
						t.Fatal(err)
					}
					return string(data)
				}
				// A fresh install writes config.toml before the agent's first
				// start, which then finds it there and leaves it alone.
				out := h.mustRun("", "", 0, "install", "--yes", "--calibration-volunteer")
				pid := h.installed(true)
				if !strings.Contains(out, `pairtime shows your time as "calibrating" until its scoring reaches version 1.0.0.`) {
					t.Errorf("install didn't say your time shows as calibrating:\n%s", out)
				}
				if cfg, problems, err := config.Load(name); err != nil || !cfg.Display.Shadow || len(problems) > 0 {
					t.Errorf("after a volunteer's install, config.toml has shadow %v, problems %q, %v; want shadow on and no problems", cfg.Display.Shadow, problems, err)
				}
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)

				// On a computer pairtime is installed on, where the agent
				// wrote config.toml at its first start.
				h.mustRun("", "", 0, "install", "--yes")
				pid = h.installed(true)
				h.waitFor(name)
				written := read()
				out = h.mustRun("", "", 0, "install", "--dry-run", "--calibration-volunteer")
				if !strings.Contains(out, "\n-shadow = false\n+shadow = true\n") {
					t.Errorf("the dry run didn't print config.toml's change:\n%s", out)
				}
				if read() != written {
					t.Error("the dry run changed config.toml")
				}
				h.mustRun("", "", 0, "install", "--yes", "--calibration-volunteer")
				want := strings.Replace(written, "\nshadow = false\n", "\nshadow = true\n", 1)
				if got := read(); got != want || want == written {
					t.Errorf("after a volunteer's install config.toml is\n%s\nwant\n%s", got, want)
				}
				info, err := os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
				out = h.mustRun("", "", 0, "install", "--yes", "--calibration-volunteer")
				if after, err := os.Stat(name); err != nil || read() != want || !os.SameFile(info, after) {
					t.Errorf("a second volunteer's install changed config.toml or wrote it again (%v):\n%s", err, read())
				}
				if strings.Contains(out, `shows your time as "calibrating"`) {
					t.Errorf("a second volunteer's install said it changed something:\n%s", out)
				}
				h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				h.uninstalled(before, pid)
			},
		},
		{
			// On Linux the config folder is apart from the data folder, so
			// linking it moves only config.toml; elsewhere TestUninstallConfig
			// checks the same step.
			name:     "uninstall with pairtime's config folder a link to a folder another computer may share leaves config.toml there without this computer's names, says where, and exits 0",
			hookMode: "real",
			before: func(h *testHome) {
				if runtime.GOOS != "linux" {
					h.t.Skip("the config folder is apart from the data folder on Linux, where linking it moves only config.toml")
				}
				shared := h.path("dotfiles/pairtime")
				for _, d := range []string{shared, filepath.Dir(h.dirs.Config)} {
					if err := os.MkdirAll(d, 0o700); err != nil {
						h.t.Fatal(err)
					}
				}
				if err := os.Symlink(shared, h.dirs.Config); err != nil {
					h.t.Skipf("this system can't make a symlink here: %v", err)
				}
			},
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				h.mustRun("", "", 0, "install", "--yes")
				pid := h.installed(false)
				// A name the teen gave a project this computer knows of, which
				// project-names.json records, as only an online uploader writes
				// it, and another computer's table in the shared config.toml.
				names := `{"v":1,"server":"","device":"","confirmed":{"` + wipeProjectKey + `":"robotics-club"}}`
				if err := os.WriteFile(filepath.Join(h.dirs.Data, "project-names.json"), []byte(names), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := config.ShowName(h.dirs.ConfigFile(), wipeProjectKey, "robotics-club"); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(h.dirs.ConfigFile(), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("\n[projects." + farProjectKey + "]\nname = \"far-away\"\n")
				if cerr := f.Close(); err == nil {
					err = cerr
				}
				if err != nil {
					t.Fatal(err)
				}

				out := h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
				shared := h.path("dotfiles/pairtime")
				if !strings.Contains(out, "config.toml stays in "+shared+", without the names you gave the projects this computer knows of") || strings.Contains(out, "robotics-club") {
					t.Errorf("uninstall didn't say where config.toml stays, or printed a label:\n%s", out)
				}
				got, err := os.ReadFile(filepath.Join(shared, "config.toml"))
				if err != nil {
					t.Fatal(err)
				}
				// The default config.toml's comments show an example table, so
				// the tables are read as the agent reads them.
				cfg, _ := config.Parse(got)
				if _, mine := cfg.Projects[wipeProjectKey]; mine || strings.Contains(string(got), "robotics-club") || !strings.Contains(string(got), "[projects."+farProjectKey+"]\nname = \"far-away\"\n") {
					t.Errorf("the shared config.toml is\n%s\nwant this computer's table gone and the other computer's kept", got)
				}
				if _, err := os.Lstat(h.dirs.Config); err == nil {
					t.Errorf("the config folder's link %s is still there", h.dirs.Config)
				}
				// The shared folder as it was, with the link, so that the rest
				// can be compared with before install.
				for _, name := range []string{"config.toml", config.LockName} {
					os.Remove(filepath.Join(shared, name))
				}
				if err := os.Symlink(shared, h.dirs.Config); err != nil {
					t.Fatal(err)
				}
				h.uninstalled(before, pid)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHome(t, tt.hookMode)
			tt.before(h)
			tt.steps(t, h, h.snapshot())
		})
	}
}

// TestSetUpLine checks install's last word on where what pairtime records
// goes: local-only mode, as every install starts in, unless pairtime login
// linked the computer and config.toml says online, when config.toml lets
// its hours' numbers go to the server it's linked to.
func TestSetUpLine(t *testing.T) {
	linked := `{"v":1,"server":"https://pairtime.example","device_id":"d_testdevice00000000000000a","linked_at":"2026-11-22T10:00:00Z"}`
	cases := []struct {
		name, account, config, want string
	}{
		{"a computer no login linked runs local-only, whatever config.toml says", "", "mode = \"online\"\n", "It runs in local-only mode"},
		{"a linked computer whose config.toml says online names its server", linked, "mode = \"online\"\n",
			"This computer is linked to https://pairtime.example, and config.toml lets it upload your hours' numbers there; pairtime doctor says if anything holds them back."},
		{"a linked computer whose config.toml says local runs local-only", linked, "mode = \"local\"\n", "It runs in local-only mode"},
		{"a linked computer whose config.toml has an error runs local-only", linked, "mode = [\n", "It runs in local-only mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
			if tc.account != "" {
				if err := os.WriteFile(filepath.Join(dir, "account.json"), []byte(tc.account), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(dirs.ConfigFile(), []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := setUpLine(linkedServer(dirs)); !strings.Contains(got, tc.want) {
				t.Errorf("install says %q, want %q", got, tc.want)
			}
		})
	}
}
