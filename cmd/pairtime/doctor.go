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
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/doctor"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/coach/text"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/version"
	"github.com/some-du6e/pairtime/pkg/redact"
)

const doctorUsage = `Usage: pairtime doctor [--json | --report]

Checks that pairtime works on this computer and prints one line per
check: ok, warn, FAIL or skip, what was checked, and what doctor found.
When something is wrong, the line says the one thing to do about it. The
idle and focus lines say what the running agent reads, not what this
terminal can reach. doctor sends nothing anywhere.

  --json    Print the checks as JSON, as described in
            docs/schema/cli-doctor.schema.json.
  --report  Print the footprint report instead: the memory and CPU
            pairtime's agent takes, how fast its hook answers, and what
            this computer is, with nothing in it that names you or the
            computer, so you can choose to share it. It watches the agent
            at rest for a minute first.

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
// harness runs, pairtime's start alone, and the signature of this binary.
var (
	doctorHooks      = readDoctorHooks
	doctorRoundTrips = hookRoundTrips
	doctorStartup    = startupTrips
	doctorSignature  = doctor.OSSignature
)

// doctorSettle is how long doctor waits to ask again when nothing answers
// on the agent's socket but an agent holds the data folder: one that has
// just started listens a moment later. Tests shorten it.
var doctorSettle = time.Second

// doctorReportSpan is how long --report watches the agent at rest, as
// footprint.yml watches an idle agent, and doctorReportCalls how many warm
// calls it times. Tests shorten the span.
var doctorReportSpan = time.Minute

const doctorReportCalls = 20

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
	if *report {
		return runReport(dirs, stdout, stderr)
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
	f := doctor.Facts{Version: version.Get().Version, AskWait: doctorWait}
	for _, h := range adapters.Default.Harnesses() {
		f.Adapters = append(f.Adapters, string(h))
	}
	f.Hooks = doctorHooks()
	for i, h := range f.Hooks {
		if len(h.Events) > 0 && len(h.Exes) > 0 && !h.Disabled && h.ReadError == "" {
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
	if errors.Is(err, errAgentDown) && agentLocked(dirs) {
		// An agent holds the data folder: it may have just started.
		time.Sleep(doctorSettle)
		start = time.Now()
		st, err = askStatus(dirs)
		f.AskTook = time.Since(start)
		if errors.Is(err, errAgentDown) {
			f.Reach = doctor.Deaf
			return f
		}
	}
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

// runReport prints the footprint report (DESIGN 5.9; TE WP7.9): it watches
// the agent for doctorReportSpan between two status questions, times warm
// test calls of each harness's hook and of pairtime's start alone, and
// prints what it found, checked against every name and path that could
// identify the teen or this computer.
func runReport(dirs home.Dirs, stdout, stderr io.Writer) int {
	v := version.Get()
	r := doctor.Report{Version: v.Version, Platform: v.Platform, GoVersion: v.GoVersion,
		OS: doctor.OSBuild(), CPUs: runtime.NumCPU(), Disk: doctor.DiskKind(dirs.Data)}
	first, err := askStatus(dirs)
	if err != nil {
		r.NoAgent = noAgentWords(err)
		if errors.Is(err, errAgentDown) && agentLocked(dirs) {
			r.NoAgent = "running, but answering nothing on its socket"
		}
	} else {
		fmt.Fprintf(stderr, "Watching pairtime's agent at rest for %s, then timing its hook. Leave this computer be until it's done.\n", text.Duration(doctorReportSpan.Seconds()))
		start := time.Now()
		time.Sleep(doctorReportSpan)
		second, err := askStatus(dirs)
		span := time.Since(start)
		if err != nil {
			second, span = first, 0
		}
		r.Agent = agentFootprint(first, second, span)
	}
	exe := ""
	for _, h := range doctorHooks() {
		if len(h.Events) == 0 || len(h.Exes) == 0 || h.Disabled || h.ReadError != "" {
			continue
		}
		trips, err := doctorRoundTrips(h.Exes[0], h.Harness, doctorReportCalls)
		r.Hooks = append(r.Hooks, doctor.HookTimes{Harness: h.Harness, Times: doctor.TimesOf(trips), Failed: err != nil})
		if exe == "" {
			exe = h.Exes[0]
		}
	}
	if exe == "" {
		exe, _ = os.Executable()
	}
	if exe != "" {
		if trips, err := doctorStartup(exe, doctorReportCalls); err == nil {
			r.Startup = doctor.TimesOf(trips)
		}
	}
	out, err := scrubReport(r.Text(), dirs)
	if err != nil {
		fmt.Fprintf(stderr, "pairtime doctor: couldn't check the report for names and paths, so it isn't printed: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, out)
	return 0
}

// agentFootprint is the agent's numbers from its status before and after
// span. Over the span they count only when both answers came from the
// same run of the agent.
func agentFootprint(first, second doctor.Status, span time.Duration) *doctor.AgentFootprint {
	a := &doctor.AgentFootprint{Version: second.Version.Version, UptimeS: second.UptimeS,
		RSSBytes: second.Footprint.RSSBytes, PeakRSSBytes: second.Footprint.PeakRSSBytes,
		CPUS: second.Footprint.CPUS, Wakeups: second.Wakeups}
	if span <= 0 || first.PID != second.PID || !first.Started.Equal(second.Started) {
		return a
	}
	a.SpanS = span.Seconds()
	a.SpanCPUS = max(second.Footprint.CPUS-first.Footprint.CPUS, 0)
	if second.Wakeups >= first.Wakeups {
		a.SpanWakeups = second.Wakeups - first.Wakeups
	}
	a.Busy = second.IPC.Stored != first.IPC.Stored || hookCalls(second) != hookCalls(first)
	return a
}

// hookCalls counts the hook calls the agent's adapters took.
func hookCalls(s doctor.Status) uint64 {
	n := s.Adapters.Other.Calls
	for _, h := range s.Adapters.Harnesses {
		n += h.Calls
	}
	return n
}

// noAgentWords says why the agent's numbers aren't in the report, without
// an error's text, which can name a path.
func noAgentWords(err error) string {
	switch {
	case errors.Is(err, errAgentDown):
		return "not running"
	case errors.Is(err, errAgentOld):
		return "an older pairtime that can't say"
	case errors.Is(err, errAgentSlow):
		return "didn't answer in time"
	}
	return "couldn't be asked"
}

// startupTrips times n warm runs of `exe hook -h`, after one that warms the
// OS's caches: what starting pairtime costs before the hook does anything,
// as footprint.yml times it.
func startupTrips(exe string, n int) ([]time.Duration, error) {
	var trips []time.Duration
	for i := 0; i <= n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), hookTestWait)
		cmd := exec.CommandContext(ctx, exe, "hook", "-h")
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		start := time.Now()
		err := cmd.Run()
		took := time.Since(start)
		cancel()
		if err != nil {
			return nil, err
		}
		if i > 0 {
			trips = append(trips, took)
		}
	}
	return trips, nil
}

// scrubReport replaces in report everything pkg/redact finds that could
// name the teen or this computer: what the OS says of the user and the
// host, git's name and email, the environment's names and paths, and
// pairtime's own folders. The report holds none of them by construction;
// this is the check that it stays so.
func scrubReport(report string, dirs home.Dirs) (string, error) {
	k := machineKnown()
	for _, name := range []string{"USER", "USERNAME", "LOGNAME"} {
		k.Accounts = appendSet(k.Accounts, os.Getenv(name))
	}
	for _, name := range []string{"HOSTNAME", "COMPUTERNAME"} {
		k.Hosts = appendSet(k.Hosts, os.Getenv(name))
	}
	for _, name := range []string{"HOME", "USERPROFILE"} {
		k.Homes = appendSet(k.Homes, os.Getenv(name))
	}
	for _, name := range []string{"LOCALAPPDATA", "APPDATA", "TMPDIR", "TEMP", "TMP", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR"} {
		k.Private = appendSet(k.Private, os.Getenv(name))
	}
	for _, dir := range []string{dirs.Data, dirs.Config, dirs.Runtime} {
		k.Private = appendSet(k.Private, dir)
	}
	rd, err := redact.New(k)
	if err != nil {
		return "", err
	}
	return redact.Render(report, rd.Find(report)), nil
}

// appendSet appends v to list unless it is empty or there already.
func appendSet(list []string, v string) []string {
	if v == "" || slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}
