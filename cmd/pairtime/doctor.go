// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/doctor"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/version"
)

const doctorUsage = `Usage: pairtime doctor [--json | --report]

Checks that pairtime works on this computer and prints one line per
check: ok, warn, FAIL or skip, what was checked, and what doctor found.
When something is wrong, the line says the one thing to do about it. The
idle and focus lines say what the running agent reads, not what this
terminal can reach. doctor sends nothing anywhere.

  --json    Print the checks as JSON, as described in
            docs/schema/cli-doctor.schema.json.
  --report  Print the footprint report instead: how much memory, CPU and
            disk time pairtime takes on this computer, with nothing in it
            that names you or this computer, so you can choose to share
            it.

Exit status:
  0  no check failed
  1  a check failed, or the report couldn't be made
  2  usage error
`

func init() {
	register(command{
		name:    "doctor",
		summary: "Check that pairtime works here, one line per check",
		run:     runDoctor,
	})
}

// doctorJSON is `pairtime doctor --json`, which
// docs/schema/cli-doctor.schema.json describes.
type doctorJSON struct {
	V       int            `json:"v"`
	Checks  []doctor.Check `json:"checks"`
	Summary doctor.Summary `json:"summary"`
}

// doctorWait bounds how long doctor waits for the agent's status. The
// agent answers from what it holds, so a slower answer means it's stuck.
// Tests shorten it.
var doctorWait = 3 * time.Second

// doctorHookCalls is how many warm test calls the hook line times, after
// one that warms the OS's caches.
const doctorHookCalls = 3

// What doctor reads of the computer, which tests replace: the hooks a
// harness's settings hold, the round trips of test calls of the hook a
// harness runs, and the signature of this binary.
var (
	doctorHooks      = readDoctorHooks
	doctorRoundTrips = hookRoundTrips
	doctorSignature  = doctor.OSSignature
)

func runDoctor(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), doctorUsage) }
	asJSON := flags.Bool("json", false, "print JSON")
	report := flags.Bool("report", false, "print the footprint report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime doctor: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	if *asJSON && *report {
		fmt.Fprintln(stderr, "pairtime doctor: --json and --report don't go together")
		return 2
	}
	dirs, err := home.Default()
	if err != nil {
		fmt.Fprintf(stderr, "pairtime doctor: can't find pairtime's folders: %v\n", err)
		return 1
	}
	facts := gatherFacts(dirs)
	checks := doctor.Checks(facts)
	if *asJSON {
		if err := writeJSON(stdout, doctorJSON{V: jsonVersion, Checks: checks, Summary: doctor.Summarize(checks)}); err != nil {
			fmt.Fprintf(stderr, "pairtime doctor: %v\n", err)
			return 1
		}
	} else if err := doctor.Write(stdout, checks); err != nil {
		fmt.Fprintf(stderr, "pairtime doctor: %v\n", err)
		return 1
	}
	if doctor.Summarize(checks).Fail > 0 {
		return 1
	}
	return 0
}

// gatherFacts finds out what doctor's checks need: the running agent's
// status, over its socket, and what this computer holds.
func gatherFacts(dirs home.Dirs) doctor.Facts {
	f := doctor.Facts{Version: version.Get().Version, AskWait: doctorWait, Now: viewNow()}
	for _, h := range adapters.Default.Harnesses() {
		f.Adapters = append(f.Adapters, string(h))
	}
	f.Hooks = doctorHooks()
	for i, h := range f.Hooks {
		if len(h.Events) > 0 && !h.Disabled && h.ReadError == "" {
			trips, err := doctorRoundTrips(h.Exes[0], h.Harness, doctorHookCalls)
			if err != nil {
				f.Hooks[i].TripError = err.Error()
			}
			f.Hooks[i].Trips = trips
		}
	}
	f.Keystore = inspectKeystore(dirs)
	f.Signing = binarySigning()
	start := time.Now()
	st, err := askStatus(dirs)
	f.AskTook = time.Since(start)
	switch {
	case err == nil:
		f.Reach, f.Agent = doctor.Reached, &st
	case errors.Is(err, errAgentDown):
		f.Reach = doctor.Down
	case errors.Is(err, errAgentOld):
		f.Reach = doctor.Old
	case errors.Is(err, errAgentSlow):
		f.Reach = doctor.Slow
	default:
		f.Reach, f.AgentError = doctor.Unreached, err.Error()
	}
	return f
}

// askStatus asks the agent for its status over the socket
// (ipc.AskStatus), which checks that the agent runs as this user before
// it writes anything, so doctor sends the bearer token nowhere (#120).
func askStatus(dirs home.Dirs) (doctor.Status, error) {
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		return doctor.Status{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), doctorWait)
	defer cancel()
	ans, err := ipc.Ask(ctx, ep, ipc.AskStatus)
	if err != nil {
		return doctor.Status{}, askFailure(ctx, err)
	}
	var st doctor.Status
	if err := json.Unmarshal(ans, &st); err != nil {
		return doctor.Status{}, fmt.Errorf("the agent's status didn't parse: %w", err)
	}
	return st, nil
}

// readDoctorHooks reads which of pairtime's hooks each harness's settings
// hold: Claude Code's, the one harness install adds hooks to so far
// (DESIGN 5.1). Cursor runs those too, without their arguments, and
// pairtime ignores those calls (install.ClaudeCode).
func readDoctorHooks() []doctor.Hooks {
	env, err := install.DefaultEnv("")
	if err != nil {
		return []doctor.Hooks{{Harness: "claude_code", Title: "Claude Code", ReadError: err.Error()}}
	}
	var out []doctor.Hooks
	for _, h := range install.Detect(env) {
		if h.Item != install.ItemClaudeCode {
			continue
		}
		d := doctor.Hooks{Harness: h.Name, Title: h.Title, Found: h.Found()}
		ch, err := install.ReadClaudeHooks(env)
		d.Settings = ch.Path
		if err != nil {
			d.ReadError = err.Error()
		}
		d.Events, d.Missing, d.Exes, d.Disabled = ch.Events, ch.Missing, ch.Exes, ch.Disabled
		out = append(out, d)
	}
	return out
}

// hookRoundTrips times n warm test calls of exe's hook command for
// harness, after one that warms the OS's caches, the way a harness runs
// its hooks: exec form, an event on stdin, from start to exit. Each goes
// to a hookRig, which walks the caller's parent chain as the agent does,
// so nothing of it reaches the agent, the journal or the ledger.
func hookRoundTrips(exe, harness string, n int) ([]time.Duration, error) {
	r, err := newHookRig(func(pid int) []platform.Proc {
		chain, _ := hookpath.ReadChain(pid)
		return chain
	})
	if err != nil {
		return nil, err
	}
	defer r.close()
	var trips []time.Duration
	for i := 0; i <= n; i++ {
		payload := fmt.Sprintf(`{"session_id":"pairtime-doctor-self-test","hook_event_name":"SessionStart","source":"startup","call":%d}`, i)
		took, err := r.call(exe, harness, "SessionStart", payload)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			trips = append(trips, took)
		}
	}
	return trips, nil
}

// inspectKeystore reads where the device key is from keystore.json alone,
// asking no store for the key, so it brings up no keyring prompt.
func inspectKeystore(dirs home.Dirs) doctor.Keystore {
	st, err := keys.Inspect(dirs.Data)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return doctor.Keystore{Missing: true}
	case err != nil:
		return doctor.Keystore{Error: err.Error()}
	}
	return doctor.Keystore{Store: st.Store.Description(), Warning: st.Warning()}
}

// binarySigning is what this binary is and how the OS sees its signature.
func binarySigning() doctor.Signing {
	v := version.Get()
	s := doctor.Signing{Release: releaseBuild(), Version: v.Version, Commit: v.Commit}
	exe, err := os.Executable()
	if err != nil {
		s.State = "its signature couldn't be checked: " + err.Error()
		return s
	}
	s.Verdict, s.State = doctorSignature(exe)
	return s
}

// releaseBuild reports whether a release build stamped this binary:
// release.yml sets its version to the tag and its commit with the linker
// (internal/version). ci.yml's v0.0.0-ci and a build from a checkout don't
// count.
func releaseBuild() bool {
	return strings.HasPrefix(version.Version, "v") && version.Version != "v0.0.0-ci" && version.Commit != ""
}
