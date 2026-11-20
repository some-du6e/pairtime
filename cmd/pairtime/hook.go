// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
)

const hookUsage = `Usage: pairtime hook --harness <name> --event <event> [--map <mapping.toml>]

Harnesses run this as a hook, and pairtime install sets those hooks up. It
reads the event the harness writes to stdin, up to 1 MiB, and hands it to
the pairtime agent within 100 ms, or leaves it in the agent's inbox when
the agent doesn't take it in time, and starts the agent when nothing
answers. It always exits 0 and prints nothing, so that it can never block
or slow down the harness, whatever goes wrong.

  --harness  the harness running the hook, such as claude_code
  --event    the harness's own name for the event, such as UserPromptSubmit;
             with --map, one of ` + "prompt, stop, tool, permission, session_start, session_end" + `
  --map      the mapping that reads the events of a harness pairtime has no
             adapter for (the generic hook mode)
`

func init() {
	register(command{
		name:    "hook",
		summary: "Hand a harness's hook event to the agent (harnesses run this)",
		run:     runHook,
	})
}

// hookStdin is where `pairtime hook` reads the harness's event, and
// hookStartAgent how it starts the agent. Tests replace both.
var (
	hookStdin      io.Reader = os.Stdin
	hookStartAgent           = startHookAgent
)

// errNotTheUsers means the hook runs as someone the data directory isn't
// for, such as root under `sudo claude` (mayWriteData, mayStartAgent).
var errNotTheUsers = errors.New("pairtime hook: this process doesn't run as the user whose data directory this is")

// prepareSpool runs just before a call is spooled (hookpath.Options.Prepare).
// It makes the data directory, for this user only, unless it exists: the
// first hook after an install, or after PAIRTIME_HOME moved, can run before
// any agent has made it. Left to the spool, it would be made with
// os.MkdirAll, whose mode Windows ignores, so the directory would take its
// parent's ACL and others could read the prompt in the spooled call
// (#46). home.Dirs.Create gives it a protected DACL there, and 0700
// elsewhere. A hook running as another user than the directory's makes and
// writes nothing.
func prepareSpool(d home.Dirs) error {
	if !mayWriteData(d) {
		return errNotTheUsers
	}
	return d.Create()
}

// ownsPath reports whether the user euid owns path or, while path doesn't
// exist, the nearest directory above it that does, as owner says. It is
// false when an owner can't be read.
func ownsPath(path string, euid int, owner func(string) (int, error)) bool {
	p := filepath.Clean(path)
	for {
		uid, err := owner(p)
		if err == nil {
			return uid == euid
		}
		parent := filepath.Dir(p)
		if !errors.Is(err, fs.ErrNotExist) || parent == p {
			return false
		}
		p = parent
	}
}

// startHookAgent starts the agent for a hook that found it down, in the
// data directory, which the spool lives in, so it doesn't keep the
// harness's project folder as its working directory. It starts none from
// a hook that runs as another user than the directory's, or on Windows
// with an elevated token, because that agent would run with root's or the
// administrator's rights on the user's files for the rest of the session
// (DESIGN 3.2). The login item, or the user's next hook, starts it then.
func startHookAgent(d home.Dirs) error {
	if !mayStartAgent(d) {
		return errNotTheUsers
	}
	dir := d.Data
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	_, err := startAgent(dir)
	return err
}

// runHook is `pairtime hook` (DESIGN 3.2; docs/PLAN.md, WP-AG3). Whatever
// happens it returns 0 and writes nothing, because a harness reads a hook's
// exit status and its output: Claude Code blocks the action on status 2,
// parses stdout as JSON, and adds the stdout of some events to the model's
// context (CP 6.2). Only a call with -h alone, which no harness makes,
// prints the usage.
func runHook(args []string, stdout, _ io.Writer) (code int) {
	defer func() {
		recover() // a bug here must not fail the harness's action
		code = 0
	}()
	if len(args) == 1 && slices.Contains([]string{"-h", "-help", "--help"}, args[0]) {
		fmt.Fprint(stdout, hookUsage)
		return 0
	}
	call := parseHook(args)
	call.Stdin = hookStdin
	dirs, err := home.Default()
	if err != nil {
		return 0 // no data directory, so no spool: the call is lost
	}
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		return 0
	}
	opts := hookpath.Options{Endpoint: ep, Prepare: func() error { return prepareSpool(dirs) }}
	if res := hookpath.Send(call, opts); res.StartAgent {
		hookStartAgent(dirs)
	}
	return 0
}

// parseHook reads the hook's arguments. Arguments it can't read become
// the call's Problem rather than an error, and the call is sent anyway,
// so the agent can count a hook that a harness's configuration got wrong.
func parseHook(args []string) hookpath.Call {
	var c hookpath.Call
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.StringVar(&c.Harness, "harness", "", "")
	flags.StringVar(&c.Event, "event", "", "")
	flags.StringVar(&c.Map, "map", "", "")
	var problems []string
	if err := flags.Parse(args); err != nil {
		problems = append(problems, err.Error())
	} else if flags.NArg() > 0 {
		problems = append(problems, fmt.Sprintf("takes no arguments besides its flags, got %d", flags.NArg()))
	}
	if c.Harness == "" {
		problems = append(problems, "no --harness")
	}
	if c.Event == "" {
		problems = append(problems, "no --event")
	} else if c.Map != "" && !hookpath.IsGeneric(c.Event) {
		problems = append(problems, "--event isn't one of the generic hook mode's events")
	}
	c.Problem = strings.Join(problems, "; ")
	return c
}
