// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/pkg/event"
)

// stubEnv makes the test binary a stub harness (stubHarness), and stubExeEnv
// names the test binary the stub runs `pairtime hook` from.
const (
	stubEnv    = "PAIRTIME_TEST_STUB_HARNESS"
	stubExeEnv = "PAIRTIME_TEST_STUB_EXE"
)

func init() { helpers[stubEnv] = stubHarness }

// stubPayload is the event the stub harness hands its hook.
const stubPayload = `{"session_id":"stub-session","hook_event_name":"UserPromptSubmit","prompt":"hello"}`

// stubHarness stands in for a harness process. It runs from a copy of the
// test binary named claude, so the process table says Claude Code. In mode
// "hook" it first runs `pairtime hook` as its child, the way Claude Code
// runs its exec-form hooks, with an event on stdin. Then, in either mode,
// it prints "ready" and waits for stdin to close, so the test decides when
// it exits.
func stubHarness(mode string) int {
	if mode == "hook" {
		cmd := exec.Command(os.Getenv(stubExeEnv), "hook", "--harness", "claude_code", "--event", "UserPromptSubmit")
		cmd.Env = append(withoutEnv(os.Environ(), stubEnv), runEnv+"="+ownArgs)
		cmd.Stdin = strings.NewReader(stubPayload)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil || out.Len() > 0 {
			fmt.Printf("the hook ended with %v and printed %q\n", err, out.String())
			return 1
		}
	}
	fmt.Println("ready")
	io.Copy(io.Discard, os.Stdin)
	return 0
}

func withoutEnv(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// shortHome makes a PAIRTIME_HOME whose path leaves room for the socket's
// name: t.TempDir puts the test's name in the path, which on macOS can take
// it past sun_path's 104 bytes.
func shortHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestParseHook(t *testing.T) {
	tests := []struct {
		name                 string
		args                 []string
		harness, event, mapf string
		problem              string // substring; "" means none
	}{
		{name: "Claude Code's hooks pass its own event name", args: []string{"--harness", "claude_code", "--event", "UserPromptSubmit"},
			harness: "claude_code", event: "UserPromptSubmit"},
		{name: "the generic hook mode passes a generic name with --map", args: []string{"--harness", "zed", "--event", "prompt", "--map", "zed.toml"},
			harness: "zed", event: "prompt", mapf: "zed.toml"},
		{name: "with --map a native event name is a problem", args: []string{"--harness", "zed", "--event", "UserPromptSubmit", "--map", "zed.toml"},
			harness: "zed", event: "UserPromptSubmit", mapf: "zed.toml", problem: "generic"},
		{name: "flags may be written with =", args: []string{"--harness=codex", "--event=Stop"}, harness: "codex", event: "Stop"},
		{name: "an unknown flag is a problem, not an error", args: []string{"--harness", "claude_code", "--frobnicate"},
			harness: "claude_code", problem: "frobnicate"},
		{name: "a missing --harness is a problem", args: []string{"--event", "Stop"}, event: "Stop", problem: "no --harness"},
		{name: "a missing --event is a problem", args: []string{"--harness", "claude_code"}, harness: "claude_code", problem: "no --event"},
		{name: "arguments after the flags are a problem", args: []string{"--harness", "claude_code", "--event", "Stop", "extra"},
			harness: "claude_code", event: "Stop", problem: "no arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := parseHook(tt.args)
			if c.Harness != tt.harness || c.Event != tt.event || c.Map != tt.mapf {
				t.Errorf("harness %q, event %q, map %q; want %q, %q, %q", c.Harness, c.Event, c.Map, tt.harness, tt.event, tt.mapf)
			}
			switch {
			case tt.problem == "" && c.Problem != "":
				t.Errorf("problem %q, want none", c.Problem)
			case tt.problem != "" && !strings.Contains(c.Problem, tt.problem):
				t.Errorf("problem %q, want it to mention %q", c.Problem, tt.problem)
			}
		})
	}
}

// spoolOf returns the messages in the spool of the data directory ptHome.
func spoolOf(t *testing.T, ptHome string) []inbox.Message {
	t.Helper()
	f, err := os.Open(filepath.Join(ptHome, "inbox", inbox.SpoolName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []inbox.Message
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, inbox.MaxMessage+2)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		m, err := inbox.Decode(sc.Bytes())
		if err != nil {
			t.Fatalf("a spool line isn't a message: %v", err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestHook runs `pairtime hook` in this process with the agent stopped, so
// each call ends in the spool, and checks DESIGN 3.2's contract: exit 0,
// nothing printed, one spool line per call, stdin cut at 1 MiB.
func TestHook(t *testing.T) {
	args := []string{"hook", "--harness", "claude_code", "--event", "UserPromptSubmit"}
	tests := []struct {
		name      string
		args      []string
		stdin     string
		check     func(t *testing.T, b hookpath.Body)
		wantStart bool   // the agent is to be started
		wantOut   string // substring of stdout; "" means stdout must be empty
	}{
		{
			name: "with the agent stopped one call appends one line, starts the agent, exits 0 and prints nothing",
			args: args, stdin: stubPayload, wantStart: true,
			check: func(t *testing.T, b hookpath.Body) {
				if string(b.Payload) != stubPayload || b.Cut || b.Problem != "" {
					t.Errorf("the body is %+v, want the event whole", b)
				}
			},
		},
		{
			name: "2 MiB of stdin is cut at 1 MiB and the call still exits 0 and prints nothing",
			args: args, stdin: `{"prompt":"` + strings.Repeat("a", 2<<20) + `"}`, wantStart: true,
			check: func(t *testing.T, b hookpath.Body) {
				if !b.Cut || b.Bytes != hookpath.MaxStdin || len(b.Text) != hookpath.MaxStdin || b.Payload != nil {
					t.Errorf("cut %v, %d bytes, text of %d, payload of %d; want stdin cut at %d", b.Cut, b.Bytes, len(b.Text), len(b.Payload), hookpath.MaxStdin)
				}
			},
		},
		{
			name: "malformed stdin exits 0, prints nothing, and the call is still kept",
			args: args, stdin: `{"session_id": "s1", "prompt": `, wantStart: true,
			check: func(t *testing.T, b hookpath.Body) {
				if b.Text != `{"session_id": "s1", "prompt": ` || b.Payload != nil || b.Cut {
					t.Errorf("the body is %+v, want the text as it came", b)
				}
			},
		},
		{
			name: "arguments a harness got wrong still exit 0 and print nothing, and the call says what was wrong",
			args: []string{"hook", "--harness", "claude_code", "--nope"}, stdin: `{}`, wantStart: true,
			check: func(t *testing.T, b hookpath.Body) {
				if !strings.Contains(b.Problem, "nope") {
					t.Errorf("the body's problem is %q, want it to name the bad flag", b.Problem)
				}
			},
		},
		{
			name: "-h alone prints the usage, for a person, and spools nothing",
			args: []string{"hook", "-h"}, wantOut: "Usage: pairtime hook",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ptHome := shortHome(t)
			t.Setenv(home.EnvHome, ptHome)
			oldStdin, oldStart := hookStdin, hookStartAgent
			t.Cleanup(func() { hookStdin, hookStartAgent = oldStdin, oldStart })
			hookStdin = strings.NewReader(tt.stdin)
			started := 0
			hookStartAgent = func(home.Dirs) error { started++; return nil }

			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != 0 {
				t.Errorf("exit status %d, want 0", code)
			}
			check(t, "stdout", stdout.String(), tt.wantOut)
			check(t, "stderr", stderr.String(), "")
			if got := started > 0; got != tt.wantStart {
				t.Errorf("the agent was started %d times, want a start: %v", started, tt.wantStart)
			}
			got := spoolOf(t, ptHome)
			if tt.check == nil {
				if len(got) != 0 {
					t.Errorf("the spool has %d messages, want none", len(got))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("the spool has %d messages, want 1", len(got))
			}
			if got[0].Kind != hookpath.Kind || got[0].Sent.IsZero() {
				t.Errorf("the spooled message is %+v, want a stamped hook call", got[0])
			}
			b, err := hookpath.DecodeBody(got[0])
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, b)
		})
	}
}

// journalEntry is a line of the agent's journal, received/*.jsonl.
type journalEntry struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"`
	Via      inbox.Via     `json:"via"`
	Bytes    int           `json:"body_bytes"`
	Walk     int           `json:"walk"`
	EventVia event.Via     `json:"event_via"`
	Harness  event.Harness `json:"harness"`
}

func journalOf(t *testing.T, ptHome string) []journalEntry {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(ptHome, "received", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []journalEntry
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			var e journalEntry
			if strings.TrimSpace(line) == "" || json.Unmarshal([]byte(line), &e) != nil {
				continue
			}
			out = append(out, e)
		}
	}
	return out
}

// awaitJournal waits up to 30 s for the journal to hold n hook calls that
// came via, and returns them.
func awaitJournal(t *testing.T, ptHome string, via inbox.Via, n int) []journalEntry {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var got []journalEntry
		for _, e := range journalOf(t, ptHome) {
			if e.Kind == hookpath.Kind && e.Via == via {
				got = append(got, e)
			}
		}
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("after 30 s the journal holds %d hook calls via %s, want %d: %+v", len(got), via, n, journalOf(t, ptHome))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// claimedPIDs returns the pids of the processes that state.json's process
// scan says hooks claimed.
func claimedPIDs(t *testing.T, ptHome string) []int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ptHome, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Procs struct {
			Claimed []struct {
				PID int `json:"pid"`
			} `json:"claimed"`
		} `json:"procs"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, c := range st.Procs.Claimed {
		pids = append(pids, c.PID)
	}
	return pids
}

// copyStub copies the test binary to claude, or claude.exe, in a directory
// of its own, for stubHarness to run from. A copy, not a link: macOS may
// name a hard-linked executable by its other path.
func copyStub(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(t.TempDir(), name)
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

// stub is a running stub harness.
type stub struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

// startStub starts the stub harness at path in mode and waits until it is
// ready. The test's cleanup ends it.
func startStub(t *testing.T, path, ptHome, mode string) *stub {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-test.run=^$")
	cmd.Env = append(os.Environ(), stubEnv+"="+mode, stubExeEnv+"="+exe, home.EnvHome+"="+ptHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &stub{cmd: cmd, stdin: stdin}
	t.Cleanup(s.stop)
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("the stub harness said %q (%v), want ready; stderr:\n%s", line, err, stderr.String())
	}
	go io.Copy(io.Discard, stdout)
	return s
}

func (s *stub) stop() {
	s.stdin.Close()
	done := make(chan struct{})
	go func() { s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		s.cmd.Process.Kill()
		<-done
	}
}

// runHookProcess runs `pairtime hook` as a process of its own, with stdin
// on its stdin, and returns its exit status and what it printed.
func runHookProcess(t *testing.T, ptHome, stdin string) (code int, stdout, stderr string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "hook", "--harness", "claude_code", "--event", "UserPromptSubmit")
	cmd.Env = append(os.Environ(), runEnv+"="+ownArgs, home.EnvHome+"="+ptHome)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	err = waitWithin(t, cmd, time.Minute)
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

// agentPid returns the pid of the agent that port.json in ptHome names,
// once it has written one.
func agentPid(t *testing.T, ptHome string) int {
	t.Helper()
	waitForAgent(t, ptHome)
	data, err := os.ReadFile(filepath.Join(ptHome, "port.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pf struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &pf); err != nil || pf.PID <= 0 {
		t.Fatalf("port.json is %s (%v), want the agent's pid", data, err)
	}
	return pf.PID
}

// TestHookProvenance checks D-spool-via with real processes: an agent, a
// stub harness named like Claude Code, and `pairtime hook`. A call made
// while the agent is stopped, and a spool line a test process appends with
// the stub harness's chain copied in, are both drained with no walk of
// their own, as plugin calls, and claim nothing from the process scan. The
// same call made over the socket from under the stub carries the agent's
// own walk, is a hook call, and claims the stub's process.
func TestHookProvenance(t *testing.T) {
	ptHome := shortHome(t)
	path := copyStub(t)

	// A stub harness that stays up, and a spool line that copies its chain,
	// as any process of this user could write one.
	waiting := startStub(t, path, ptHome, "wait")
	chain, err := hookpath.ReadChain(waiting.cmd.Process.Pid)
	if err != nil || len(chain) < 2 {
		t.Fatalf("reading the stub harness's chain: %v, %+v", err, chain)
	}
	forged := inbox.New(hookpath.Kind, json.RawMessage(`{"harness":"claude_code","event":"UserPromptSubmit","bytes":0}`))
	forged.Chain = []inbox.Proc{{PID: os.Getpid(), Name: "pairtime"}}
	for _, p := range chain {
		forged.Chain = append(forged.Chain, inbox.Proc{PID: p.PID, Name: p.Name, Start: p.Start.UTC()})
	}
	if err := inbox.Append(filepath.Join(ptHome, "inbox"), forged); err != nil {
		t.Fatal(err)
	}

	top := t // the agent the first step starts runs until the whole test ends
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "with the agent stopped a hook exits 0, prints nothing, and starts the agent",
			run: func(t *testing.T) {
				code, stdout, stderr := runHookProcess(t, ptHome, stubPayload)
				if code != 0 || stdout != "" || stderr != "" {
					t.Errorf("exit status %d, stdout %q, stderr %q; want 0 and nothing printed", code, stdout, stderr)
				}
				stopAtCleanup(top, agentPid(t, ptHome))
			},
		},
		{
			name: "both spooled calls are drained with no walk, as plugin calls, the copied chain naming only the harness",
			run: func(t *testing.T) {
				got := awaitJournal(t, ptHome, inbox.ViaInbox, 2)
				if len(got) != 2 {
					t.Fatalf("the journal holds %d spooled hook calls, want 2: %+v", len(got), got)
				}
				for _, e := range got {
					if e.Walk != 0 || e.EventVia != event.ViaPlugin {
						t.Errorf("spooled call %+v, want no walk and via plugin", e)
					}
					want := event.Harness("")
					if e.ID == forged.ID {
						want = event.HarnessClaudeCode
					}
					if e.Harness != want {
						t.Errorf("spooled call %s names harness %q, want %q", e.ID, e.Harness, want)
					}
				}
			},
		},
		{
			name: "spooled calls claim nothing from the process scan",
			run: func(t *testing.T) {
				if pids := claimedPIDs(t, ptHome); len(pids) != 0 {
					t.Errorf("the scan has claims for pids %v, want none; the stub harness is %d", pids, waiting.cmd.Process.Pid)
				}
			},
		},
		{
			name: "the same call over the socket from under the stub harness carries the agent's own walk, as a hook call",
			run: func(t *testing.T) {
				hooked := startStub(t, path, ptHome, "hook")
				got := awaitJournal(t, ptHome, inbox.ViaSocket, 1)
				e := got[0]
				if e.Walk < 2 || e.EventVia != event.ViaHook || e.Harness != event.HarnessClaudeCode {
					t.Errorf("the call over the socket is %+v, want a walk, via hook and claude_code", e)
				}
				pids := claimedPIDs(t, ptHome)
				if len(pids) != 1 || pids[0] != hooked.cmd.Process.Pid {
					t.Errorf("the scan has claims for pids %v, want only the stub harness's, %d", pids, hooked.cmd.Process.Pid)
				}
			},
		},
		{
			name: "malformed stdin over the socket exits 0 and prints nothing",
			run: func(t *testing.T) {
				code, stdout, stderr := runHookProcess(t, ptHome, `{"session_id": `)
				if code != 0 || stdout != "" || stderr != "" {
					t.Errorf("exit status %d, stdout %q, stderr %q; want 0 and nothing printed", code, stdout, stderr)
				}
				awaitJournal(t, ptHome, inbox.ViaSocket, 2)
			},
		},
		{
			name: "2 MiB of stdin over the socket is cut at 1 MiB, exits 0 and prints nothing",
			run: func(t *testing.T) {
				code, stdout, stderr := runHookProcess(t, ptHome, `{"prompt":"`+strings.Repeat("a", 2<<20)+`"}`)
				if code != 0 || stdout != "" || stderr != "" {
					t.Errorf("exit status %d, stdout %q, stderr %q; want 0 and nothing printed", code, stdout, stderr)
				}
				got := awaitJournal(t, ptHome, inbox.ViaSocket, 3)
				big := got[len(got)-1]
				if big.Bytes < hookpath.MaxStdin || big.Bytes > hookpath.MaxStdin+4<<10 {
					t.Errorf("the call's body is %d bytes, want 1 MiB of stdin and its envelope", big.Bytes)
				}
				if big.EventVia != event.ViaPlugin {
					t.Errorf("a hook this test ran, under no harness, is via %q, want plugin", big.EventVia)
				}
			},
		},
	}
	for _, step := range steps {
		if !t.Run(step.name, step.run) {
			break // each step builds on the one before
		}
	}
}
