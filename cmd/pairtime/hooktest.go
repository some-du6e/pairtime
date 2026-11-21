// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/install"
)

// The hooks install writes run `pairtime hook` (WP-AG3). A binary without
// that command, or with one that fails, would answer each of Claude Code's
// hooks with a usage error and exit status 2, which makes Claude Code block
// the prompt or the tool call the hook fired for (CP 6.2). So install adds
// hooks only after the command has passed hookCheck, against the binary the
// hooks will run, and it never puts a binary that fails it where hooks
// already in Claude Code's settings would run it (hooksWouldBreak).

// hookTestPayload is the event the self-test hands `pairtime hook`, shaped
// like Claude Code's SessionStart, with a session id no harness makes.
const hookTestPayload = `{"session_id":"pairtime-install-self-test","hook_event_name":"SessionStart","source":"startup"}`

// hookTestWait bounds one test call. A hook that is slower than this is
// broken: Claude Code waits for every hook, and pairtime's take
// milliseconds.
const hookTestWait = 10 * time.Second

// hookGrace is how long hookTest keeps its data directory locked after a
// call that didn't reach it. A hook that finds no agent starts one, and
// that agent must find the lock taken and exit, so that it never runs.
const hookGrace = 3 * time.Second

// errNoHook means the binary has no hook command at all.
var errNoHook = errors.New("this pairtime has no hook command")

// hookCheck tests the hook command of exe, which has the bytes of this
// binary, and returns how long the test call took, or why hooks may not
// run it.
func hookCheck(exe string) (time.Duration, error) {
	if _, ok := commands["hook"]; !ok {
		return 0, errNoHook
	}
	took, err := hookTest(exe)
	if err != nil {
		return 0, fmt.Errorf("pairtime hook failed its self-test (%v)", err)
	}
	return took, nil
}

// hookWhy says why Claude Code's hooks stay out when hookCheck returned
// err, in words that follow "found at <folder>; " on the consent screen.
func hookWhy(err error) string {
	if errors.Is(err, errNoHook) {
		return "this pairtime has no hook command yet, and Claude Code would block your prompts on the error each hook gave, so pairtime leaves its settings alone"
	}
	return err.Error() + ", so pairtime leaves Claude Code's settings alone"
}

// hooksWouldBreak is install's refusal to put a binary whose hook command
// failed the self-test, as err says, at exe, which the hooks pairtime put
// in Claude Code's settings run: from then on Claude Code would block
// every prompt.
func hooksWouldBreak(err error, exe string) error {
	return fmt.Errorf("%v, and the hooks in Claude Code's settings run the pairtime in %s, so putting this one there would block your prompts; install left that pairtime as it was, so keep using it, and report what the self-test said at https://github.com/some-du6e/pairtime/issues", err, install.Printable(filepath.Dir(exe)))
}

// hookTest runs exe's hook command as Claude Code runs its hooks, with
// hookTestPayload on stdin, and checks that it exits 0, prints nothing and
// hands the event over. It returns how long the call took, from start to
// exit.
//
// Nothing of the call reaches the teen's agent or ledger. The command runs
// with PAIRTIME_HOME set to a folder of its own, where this process listens
// as the agent would, through package ipc, and keeps the agent's lock, so
// that an agent the hook starts exits at once (exitAlreadyRunning) instead
// of making a device key for a throwaway folder.
func hookTest(exe string) (took time.Duration, err error) {
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		return 0, err
	}
	defer removeTemp(dir)
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	lock, err := home.Acquire(dir)
	if err != nil {
		return 0, err
	}
	got := make(chan inbox.Message, 8)
	reached := false
	defer func() {
		if !reached {
			time.Sleep(hookGrace)
		}
		lock.Release()
	}()
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		return 0, err
	}
	srv, err := ipc.Listen(ep, ipc.Options{})
	if err != nil {
		return 0, fmt.Errorf("listening for the test call: %w", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		srv.Serve(sinkFunc(func(d inbox.Delivery) error {
			select {
			case got <- d.Message:
			default:
			}
			return nil
		}))
	}()
	defer func() {
		srv.Close()
		<-served
	}()

	ctx, cancel := context.WithTimeout(context.Background(), hookTestWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "hook", "--harness", "claude_code", "--event", "SessionStart")
	cmd.Env = setEnv(os.Environ(), home.EnvHome, dir)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(hookTestPayload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()
	took = time.Since(start)
	// The agent acks a message once it has it, and the hook exits after
	// the ack, so a hook that exited 0 has handed its event over by now,
	// or spooled it. The wait is for one whose ack came too late.
	wait := time.Second
	if runErr != nil {
		wait = 0
	}
	select {
	case m := <-got:
		reached = true
		if err := checkHookMessage(m); err != nil {
			return 0, err
		}
	case <-time.After(wait):
	}
	switch {
	case ctx.Err() != nil:
		return 0, fmt.Errorf("it was still running after %v", hookTestWait)
	case runErr != nil:
		return 0, fmt.Errorf("it %v", exitText(runErr))
	case stdout.Len() > 0 || stderr.Len() > 0:
		return 0, errors.New("it printed something, which Claude Code would read as the hook's answer")
	case !reached:
		return 0, errors.New("the event didn't reach the agent")
	}
	return took, nil
}

// exitText says how a command that failed ended.
func exitText(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("exited with status %d", ee.ExitCode())
	}
	return "couldn't run: " + err.Error()
}

// checkHookMessage checks that m is the test call: a hook message for
// claude_code's SessionStart, with the payload the test wrote.
func checkHookMessage(m inbox.Message) error {
	var b struct {
		Harness string          `json:"harness"`
		Event   string          `json:"event"`
		Payload json.RawMessage `json:"payload"`
	}
	if m.Kind != "hook" || json.Unmarshal(m.Body, &b) != nil {
		return fmt.Errorf("the agent got a %q message that isn't a hook call", m.Kind)
	}
	if b.Harness != "claude_code" || b.Event != "SessionStart" {
		return fmt.Errorf("the agent got harness %q and event %q, not claude_code and SessionStart", b.Harness, b.Event)
	}
	var sent, recv any
	if json.Unmarshal([]byte(hookTestPayload), &sent) != nil || json.Unmarshal(b.Payload, &recv) != nil || !jsonEqual(sent, recv) {
		return errors.New("the agent didn't get the event the test wrote on stdin")
	}
	return nil
}

func jsonEqual(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// sinkFunc makes a function an inbox.Sink.
type sinkFunc func(inbox.Delivery) error

func (f sinkFunc) Deliver(d inbox.Delivery) error { return f(d) }

// setEnv returns env with key set to value, in place of any setting of it,
// whatever case Windows spells it in.
func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+value)
}

// removeTemp deletes dir, trying again for a few seconds: on Windows a
// process that just exited may hold a file in it a moment longer.
func removeTemp(dir string) {
	for i := 0; i < 20; i++ {
		if os.RemoveAll(dir) == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
