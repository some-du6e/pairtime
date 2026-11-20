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

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/install"
)

// hookModeEnv, when set, makes the test binary run pairtime with its hook
// command changed (runWithHookMode), in the process it starts and in every
// process that one starts in turn: the installed copy, the agent and the
// hook self-test.
const hookModeEnv = "PAIRTIME_TEST_HOOK"

func init() { helpers[hookModeEnv] = runWithHookMode }

// runWithHookMode runs pairtime with its own arguments after changing its
// hook command as mode says:
//
//   - real keeps the binary's own, or a stand-in that hands the event to
//     the agent the way `pairtime hook` does (fakeHook) when the binary has
//     none, until WP-AG3's lands;
//   - none takes the hook command away;
//   - broken makes it print an answer and exit 2, as a usage error would.
func runWithHookMode(mode string) int {
	switch mode {
	case "real":
		if _, ok := commands["hook"]; !ok {
			commands["hook"] = command{name: "hook", summary: "test stand-in", run: fakeHook}
		}
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

// fakeHook hands the event on stdin to the agent as a hook message, the
// way `pairtime hook` does, and spools it when the agent doesn't take it.
func fakeHook(args []string, _, _ io.Writer) int {
	var harness, event string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--harness":
			harness = args[i+1]
		case "--event":
			event = args[i+1]
		}
	}
	stdin, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	b := map[string]any{"harness": harness, "event": event, "bytes": len(stdin)}
	if json.Valid(stdin) {
		b["payload"] = json.RawMessage(stdin)
	}
	body, err := json.Marshal(b)
	if err != nil {
		return 0
	}
	dirs, err := home.Default()
	if err != nil {
		return 0
	}
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		return 0
	}
	ipc.Send(ep, inbox.New("hook", body), ipc.HookTimeout)
	return 0
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
	for _, d := range []string{h.home, tmp, run} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	drop := []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "TMPDIR", "PATH", "XDG_DATA_HOME", "XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR", "XDG_STATE_HOME", "XDG_CACHE_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", home.EnvHome, runEnv, hookModeEnv}
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
		h.env = append(h.env, "USERPROFILE="+h.home, "LOCALAPPDATA="+local, "APPDATA="+roaming, "PATH="+path)
		data := filepath.Join(local, "pairtime")
		h.dirs = home.Dirs{Data: data, Config: filepath.Join(roaming, "pairtime"), Runtime: data}
		h.bin = filepath.Join(data, "bin", "pairtime.exe")
	case "darwin":
		h.env = append(h.env, "HOME="+h.home, "TMPDIR="+tmp, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
		data := filepath.Join(h.home, "Library", "Application Support", "pairtime")
		h.dirs = home.Dirs{Data: data, Config: data, Runtime: filepath.Join(tmp, "pairtime")}
		h.bin = filepath.Join(h.home, ".local", "bin", "pairtime")
	default:
		h.env = append(h.env, "HOME="+h.home, "XDG_RUNTIME_DIR="+run, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
		h.dirs = home.Dirs{Data: filepath.Join(h.home, ".local", "share", "pairtime"), Config: filepath.Join(h.home, ".config", "pairtime"), Runtime: filepath.Join(run, "pairtime")}
		h.bin = filepath.Join(h.home, ".local", "bin", "pairtime")
	}
	h.env = append(h.env, hookModeEnv+"="+hookMode)
	t.Cleanup(h.cleanup)
	return h
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
			name:     "uninstall keeps the data unless asked, and a later uninstall deletes it",
			hookMode: "real",
			before:   func(h *testHome) { h.write(".claude/settings.json", gitAISettings) },
			steps: func(t *testing.T, h *testHome, before map[string]string) {
				h.mustRun("", "", 0, "install", "--yes")
				pid := h.installed(true)
				out := h.mustRun("", "", 0, "uninstall", "--yes")
				if !strings.Contains(out, "data stays in") {
					t.Errorf("uninstall didn't say where the data stays:\n%s", out)
				}
				h.waitGone(h.bin)
				for p := range h.snapshot() {
					full := filepath.Join(h.root, p)
					if _, ok := before[p]; !ok && !within(full, h.dirs.Data) && !within(full, h.dirs.Config) && !within(h.dirs.Data, full) && !within(h.dirs.Config, full) {
						t.Errorf("uninstall left %s, which isn't pairtime's data", p)
					}
				}
				if h.hooked() != nil || h.autostart() != "" {
					t.Errorf("uninstall left hooks %q or the login item %q", h.hooked(), h.autostart())
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHome(t, tt.hookMode)
			tt.before(h)
			tt.steps(t, h, h.snapshot())
		})
	}
}
