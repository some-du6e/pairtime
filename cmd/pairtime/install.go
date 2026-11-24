// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent"
	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/install/consent"
	"github.com/some-du6e/pairtime/internal/version"
)

// exitDeclined is the exit status of install and uninstall when the teen
// says no, or gives no answer, so nothing changed.
const exitDeclined = 3

// offerWakaTime says install offers pairtime's line in ~/.wakatime.cfg
// (install.WakaTime; DESIGN 5.6), and uninstall looks for it where its
// manifest doesn't record it. It stays off until WP-AD17 turns it on with
// release.yml's gate, which publishes no release that edits the file
// without a green run of e2e-wakatime.yml against the real wakatime-cli
// (DESIGN 5.6, "Agent down"), by when the agent answers on the line's
// endpoint (WP-AD16): a line nothing answers holds back wakatime-cli's
// sends to Hackatime too (docs/errata/2026-11-23-wakatime-cli-2.28.4.md,
// item 4).
var offerWakaTime = false

const installUsage = `Usage: pairtime install [--yes] [--skip <item>]... [--calibration-volunteer] [--dry-run]

Sets pairtime up on this computer, without admin rights. It puts the
pairtime binary in your own programs folder, finds the AI tools you use,
and shows every change it would make, each with a diff, before it makes
any: starting pairtime when you log in, and the hooks that tell pairtime
when you prompt Claude Code. Untick what you don't want, then type y.
Running it again changes nothing that is set up already, and
pairtime uninstall takes everything back out.

  --yes        make every change without asking, for scripts
  --skip item  leave one change out: autostart, or hooks/claude_code
  --calibration-volunteer
               for calibration volunteers: also set shadow = true under
               [display] in config.toml, so that pairtime shows your time
               as "calibrating" until its scoring reaches version 1.0.0
  --dry-run    print the changes as diffs, and make none

Exit status:
  0  installed, or nothing needed changing
  1  something went wrong; what was done so far is undone by pairtime
     uninstall, and finished by running install again
  2  usage error
  3  you said no, so nothing changed
`

func init() {
	register(command{
		name:    "install",
		summary: "Set pairtime up: autostart, hooks, and the agent",
		run:     runInstall,
	})
}

// skipFlag collects --skip.
type skipFlag []string

func (s *skipFlag) String() string { return strings.Join(*s, ",") }

func (s *skipFlag) Set(v string) error {
	items := []string{install.ItemAutostart, install.ItemClaudeCode}
	if offerWakaTime {
		items = append(items, install.ItemWakaTime)
	}
	if !slices.Contains(items, v) {
		return fmt.Errorf("there is no item %q; the items are %s and %s", v, strings.Join(items[:len(items)-1], ", "), items[len(items)-1])
	}
	*s = append(*s, v)
	return nil
}

// madeFlag collects --made, which the install scripts pass (WP-RL2) and
// the usage leaves out: each folder a script made for the binary before it
// ran install, which is the binary's folder or a folder above it. install
// records them in setup.json beside the folders it made itself, so that
// uninstall takes them away once they are empty, and gives a data
// directory among them the permissions home gives the ones it makes
// (makeHome). Like everything install records, they count only in an
// install that gets past the consent screen, so the install command a
// script suggests running next, after a no (exit 3) or with --no-install,
// carries the same flags.
type madeFlag []string

func (m *madeFlag) String() string { return strings.Join(*m, ",") }

func (m *madeFlag) Set(v string) error {
	if !filepath.IsAbs(v) {
		return fmt.Errorf("%q isn't a full path", v)
	}
	*m = append(*m, filepath.Clean(v))
	return nil
}

// installStdin is where install and uninstall read the teen's answers.
// Tests replace it.
var installStdin io.Reader = os.Stdin

// runInstall is `pairtime install` (DESIGN 5.9 steps 1 to 4; docs/PLAN.md,
// WP-IN2). In order:
//
//  1. It finds the harnesses (install.Detect) and tests the hook command
//     the hooks would run (hookCheck), then plans each item it offers. An
//     item the teen leaves out with --skip isn't planned, and one whose
//     planner refuses its file, such as a settings.json that isn't JSON,
//     is left out with the planner's reason, as the hooks are when the
//     self-test fails.
//  2. It shows the consent screen, with each change as a diff, and the
//     teen unticks what they don't want (--yes for scripts).
//  3. It puts the binary at its per-user path, with pairtime's own
//     folders. It copies the binary beside its place and tests the hook
//     command of the copy, since that is what the hooks run, before the
//     copy goes in place.
//  4. It writes autostart and starts the agent, then merges the hooks, so
//     the first hook call finds the agent up and goes over its socket: a
//     call that finds it down is spooled, and a spooled call earns only a
//     side door's credit (DESIGN 5.2).
//
// With --calibration-volunteer, the screen also offers shadow = true under
// [display] in config.toml (planCalibration), so that a calibration
// volunteer's views show their time as "calibrating" until pairtime's
// scoring reaches 1.0.0, when config.Parse ends it (DESIGN 6.7 steps 2
// and 4; decision D-shadow-scope). Step 4 makes that change, through
// config's own edit, before the agent starts. Uninstall deletes config.toml
// with the rest of pairtime's data, and the line goes with it.
//
// Nothing changes before the teen's yes, so the binary goes in place after
// it, in step 3, not first as DESIGN 5.9 step 1 has it. An install script
// that put the binary in place itself names the folders it made with
// --made.
//
// When pairtime's hooks are in Claude Code's settings already, they run
// the binary at the per-user path. A binary whose hook command fails the
// self-test would then block every prompt once it went there, so install
// refuses to replace the binary at all (hooksWouldBreak), whether or not
// the teen leaves the hooks out.
//
// The agent starts in local-only mode, which it writes into config.toml
// at its first start (internal/agent/config). pairtime login links the
// computer and sets it online, and install leaves both as they are, so the
// consent screen and install's last line say which the computer is in
// (linkedServer). Once it runs, the agent reads the last 30 days of
// transcripts by itself, at low priority (lookback, internal/agent/tailers),
// and its status says how far it got; the progress bar and the welcome
// recap come with the dashboard's work packages, and the WakaTime line and
// Karim's AGENTS.md entry with theirs.
func runInstall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), installUsage) }
	yes := flags.Bool("yes", false, "make every change without asking")
	dryRun := flags.Bool("dry-run", false, "print the changes and make none")
	volunteer := flags.Bool("calibration-volunteer", false, `show your time as "calibrating" until scoring 1.0.0`)
	var skip skipFlag
	flags.Var(&skip, "skip", "leave one change out")
	var made madeFlag
	flags.Var(&made, "made", "a folder an install script made for the binary")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime install: takes no arguments besides its flags, got %q\n", flags.Args())
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "pairtime install: %v\n", err)
		return 1
	}

	env, dirs, err := setupEnv()
	if err != nil {
		return fail(err)
	}
	for _, d := range made {
		if !within(filepath.Dir(env.Exe), d) {
			fmt.Fprintf(stderr, "pairtime install: --made %s isn't %s or a folder above it\n", install.Printable(d), install.Printable(filepath.Dir(env.Exe)))
			return 2
		}
	}
	src, err := self()
	if err != nil {
		return fail(err)
	}
	same, err := sameBinary(src, env.Exe)
	if err != nil {
		return fail(err)
	}
	if !same {
		if err := checkReplaceable(env.Exe); err != nil {
			return fail(err)
		}
	}
	in := install.New(dirs)
	// Planning makes the data directory, for the installer's lock, so what
	// isn't there has to be known first: setup.json records it once install
	// changes something, and until then tidy takes it away again.
	missing := notThere(env, dirs)
	changed := false
	defer func() {
		if !changed {
			tidy(in, dirs, missing)
		}
	}()
	// Every plan takes the installer's lock and reads its manifest, so what
	// goes wrong here, another install that runs or a damaged manifest,
	// would go wrong for every item.
	probe, err := in.PlanInstall(nil)
	if err != nil {
		return fail(err)
	}
	probe.Close()

	// 1. What there is, and what install would change.
	harnesses := install.Detect(env)
	claudeFound := slices.ContainsFunc(harnesses, func(h install.Harness) bool { return h.Item == install.ItemClaudeCode && h.Found() })
	skipped := func(id string) bool { return slices.Contains(skip, id) }
	hooked := false
	if claudeFound && !same {
		if hooked, err = hasClaudeHooks(in, env); err != nil {
			return fail(err)
		}
	}
	var hook consent.HookTest
	if claudeFound && (!skipped(install.ItemClaudeCode) || hooked) {
		took, err := hookCheck(src)
		switch {
		case err == nil:
			hook = consent.HookTest{OK: true, Took: took}
		case hooked:
			return fail(hooksWouldBreak(err, env.Exe))
		default:
			hook = consent.HookTest{Why: hookWhy(err)}
		}
	}
	items := map[string]install.Item{}
	diffs := map[string]string{}
	shown := map[string]shownPlan{} // what each item's screen plan read and changes
	left := map[string]string{}     // the items install doesn't offer, with why
	offer := func(id string, mk func(install.Env) (install.Item, error)) error {
		it, err := mk(env)
		if err == nil {
			var p *install.Plan
			if p, err = in.PlanInstall([]install.Item{it}); err == nil {
				items[id], diffs[id], shown[id] = it, p.Diff(), shownOf(p)
				p.Close()
				return nil
			}
		}
		if errors.Is(err, install.ErrBusy) {
			return err
		}
		// A file the planner won't edit keeps out only its own item, which
		// the teen could have unticked anyway (DESIGN 5.9).
		left[id] = err.Error()
		return nil
	}
	if skipped(install.ItemAutostart) {
		left[install.ItemAutostart] = "you left it out with --skip"
	} else if err := offer(install.ItemAutostart, install.Autostart); err != nil {
		return fail(err)
	}
	if claudeFound {
		if skipped(install.ItemClaudeCode) {
			left[install.ItemClaudeCode] = "you left its hooks out with --skip"
		} else if hook.OK {
			if err := offer(install.ItemClaudeCode, install.ClaudeCode); err != nil {
				return fail(err)
			}
		}
	}
	wakaTimeNew := false
	// waka is what the WakaTime item is planned with, its key included, so
	// that step 5 can plan the same line on the port the agent listens on.
	var waka install.WakaTimeOptions
	if offerWakaTime {
		if skipped(install.ItemWakaTime) {
			left[install.ItemWakaTime] = "you left it out with --skip"
		} else {
			waka = install.WakaTimeOptions{Data: dirs.Data, Port: wakaTimePort(dirs), Now: time.Now()}
			_, there, err := install.WakaTimeConfig(env)
			wakaTimeNew = err == nil && !there
			if err := offer(install.ItemWakaTime, func(env install.Env) (install.Item, error) {
				key, err := install.WakaTimeKey(env, waka.Data)
				if err != nil {
					return install.Item{}, err
				}
				waka.Key = key
				return install.WakaTime(env, waka)
			}); err != nil {
				return fail(err)
			}
		}
	}
	if *volunteer {
		if diff, why := planCalibration(dirs.ConfigFile(), config.Calibrating()); why != "" {
			left[consent.ItemCalibration] = why
		} else {
			diffs[consent.ItemCalibration] = diff
		}
	}
	linked := linkedServer(dirs)
	unlinked := linked == "" && unlinkedOnline(dirs)
	screen := consent.New(consent.Input{
		GOOS:        env.GOOS,
		Binary:      env.Exe,
		Harnesses:   harnesses,
		Hook:        hook,
		Diffs:       diffs,
		Left:        left,
		Linked:      linked,
		Unlinked:    unlinked,
		WakaTimeNew: wakaTimeNew,
	})

	// 2. The teen's say.
	switch {
	case *dryRun:
		if !same {
			from := "/dev/null"
			if _, err := os.Stat(env.Exe); err == nil {
				from = install.Printable(env.Exe)
			}
			fmt.Fprintf(stdout, "Binary files %s and %s differ\n", from, install.Printable(env.Exe))
		}
		for _, id := range screen.Ticked() {
			io.WriteString(stdout, diffs[id])
		}
		leftOut(stderr, "pairtime install: ", hook, left, skip)
		return 0
	case *yes:
		leftOut(stdout, "", hook, left, skip)
	default:
		if !screen.Ask(installStdin, stdout) {
			fmt.Fprintln(stdout, "Nothing changed.")
			return exitDeclined
		}
	}
	chosen := screen.Ticked()

	// 3. The binary, at the path every hook and the login item run.
	changed = true
	if err := makeHome(env, dirs, missing, made); err != nil {
		return fail(fmt.Errorf("making pairtime's folders: %w; check that you may write in your home folder, then run pairtime install again", err))
	}
	restart := false
	if !same {
		// Copies an earlier install left: one it moved aside on Windows,
		// which something ran then (commitBinary), and one it staged and
		// was stopped before it put in place.
		for _, p := range strayBinaries(filepath.Dir(env.Exe), 10*time.Minute) {
			os.Remove(p)
		}
		staged, err := stageBinary(src, env.Exe)
		if err != nil {
			return fail(fmt.Errorf("copying pairtime to %s: %w; make sure the disk has room and you may write there, then run pairtime install again", install.Printable(filepath.Dir(env.Exe)), err))
		}
		defer os.Remove(staged) // still there only if it didn't go in place
		// The copy is what the hooks will run, so it is tested before it
		// goes where they run it: a folder whose programs can't run, or a
		// copy a virus scanner holds back, fails here.
		if hooked || slices.Contains(chosen, install.ItemClaudeCode) {
			if _, err := hookCheck(staged); err != nil {
				if hooked {
					return fail(hooksWouldBreak(err, env.Exe))
				}
				chosen = slices.DeleteFunc(chosen, func(id string) bool { return id == install.ItemClaudeCode })
				fmt.Fprintf(stdout, "Claude Code's hooks stay out: %s.\n", hookWhy(err))
			}
		}
		if st, err := queryAgent(dirs); err == nil {
			// The running agent may be this copy, and is an older pairtime
			// anyway.
			fmt.Fprintf(stdout, "Stopping pairtime's agent (process %d) to update it.\n", st.PID)
			if err := stopAgent(dirs); err != nil {
				return fail(err)
			}
			restart = true
		}
		if err := commitBinary(staged, env.Exe); err != nil {
			return fail(fmt.Errorf("putting pairtime in %s: %w; close what runs it, then run pairtime install again", install.Printable(env.Exe), err))
		}
		fmt.Fprintf(stdout, "Put pairtime in %s.\n", install.Printable(env.Exe))
	}

	// 4. Autostart, the agent, then the hooks.
	apply := func(id string) error {
		p, err := in.PlanInstall([]install.Item{items[id]})
		if err != nil {
			return err
		}
		if p.Diff() != diffs[id] {
			p.Close()
			return fmt.Errorf("what %s changes isn't what the screen showed any more, so pairtime didn't change it; run pairtime install again to see the new changes", id)
		}
		if err := p.Apply(); errors.Is(err, install.ErrStale) {
			return fmt.Errorf("something else changed what %s changes while install was making the change, so install stopped there; run pairtime install again to see the new changes", id)
		} else if err != nil {
			return err
		}
		return nil
	}
	if slices.Contains(chosen, install.ItemAutostart) {
		if err := apply(install.ItemAutostart); err != nil {
			return fail(err)
		}
		if diffs[install.ItemAutostart] != "" {
			fmt.Fprintln(stdout, "pairtime will start when you log in.")
		}
	}
	if slices.Contains(chosen, consent.ItemCalibration) {
		// config's own edit, under config.lock as every edit of config.toml
		// is, before the agent starts, so that the agent finds the file:
		// its first start writes the default only where there is none. It
		// changes the file as it is now, which is what the screen showed
		// unless something changed it since, and in a changed file it
		// changes the same line, which is what the teen said yes to.
		if err := config.SetShadow(dirs.ConfigFile(), true); err != nil {
			return fail(errors.New(calibrationWhy(err)))
		}
		if diffs[consent.ItemCalibration] != "" {
			fmt.Fprintln(stdout, `pairtime shows your time as "calibrating" until its scoring reaches version 1.0.0.`)
		}
	}
	st, err := queryAgent(dirs)
	if err != nil || restart || st.Version.Version != version.Get().Version {
		if err == nil {
			// An agent of another version runs, perhaps from another copy
			// of pairtime, or, after install stopped the agent to update
			// it (restart), one that started since, maybe from the old
			// binary: the one that runs from now on is this one.
			why := "which is another version"
			if st.Version.Version == version.Get().Version {
				why = "which started while install put this pairtime in place"
			}
			fmt.Fprintf(stdout, "Stopping pairtime's agent (process %d), %s.\n", st.PID, why)
			if err := stopAgent(dirs); err != nil {
				return fail(err)
			}
		}
		agentStarting(env.Exe, dirs)
		if st, err = startOwnAgent(env.Exe, dirs, stdout); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Started pairtime's agent, process %d.\n", st.PID)
	} else {
		fmt.Fprintf(stdout, "pairtime's agent is running, process %d.\n", st.PID)
	}
	if slices.Contains(chosen, install.ItemClaudeCode) {
		if err := apply(install.ItemClaudeCode); err != nil {
			return fail(err)
		}
		if diffs[install.ItemClaudeCode] != "" {
			fmt.Fprintf(stdout, "Added pairtime's hooks to %s.\n", install.Printable(install.ClaudeSettings(env)))
		}
	}
	// 5. ~/.wakatime.cfg, once the agent is up (DESIGN 5.9), naming the
	// port it listens on: one that found the screen's port taken, as by
	// another account's agent, listens on another.
	if slices.Contains(chosen, install.ItemWakaTime) {
		if pf, err := localhttp.ReadPortFile(dirs.PortFile()); err == nil && pf.Port != waka.Port {
			it, diff, err := wakaTimeReplan(env, in, waka, shown[install.ItemWakaTime], pf.Port)
			if err != nil {
				return fail(err)
			}
			items[install.ItemWakaTime], diffs[install.ItemWakaTime] = it, diff
		}
		if err := apply(install.ItemWakaTime); err != nil {
			return fail(err)
		}
		if diffs[install.ItemWakaTime] != "" {
			path, _, _ := install.WakaTimeConfig(env)
			if wakaTimeNew {
				fmt.Fprintf(stdout, "Made %s, so wakatime-cli sends your editor time to pairtime.\n", install.Printable(path))
			} else {
				fmt.Fprintf(stdout, "Added pairtime's line to %s, so wakatime-cli sends your editor time to pairtime too.\n", install.Printable(path))
			}
		}
	}

	fmt.Fprintln(stdout, setUpLine(linked, unlinked))
	if slices.Contains(chosen, install.ItemClaudeCode) {
		// Claude Code's file watcher picks up a change to the hooks in its
		// settings in sessions that are open, and a folder's trust dialog
		// comes before any of its hooks run (code.claude.com/docs/en/hooks).
		fmt.Fprintln(stdout, "Claude Code sessions that are open already start running the hooks once Claude Code notices the change, and a folder runs them only after you trust it.")
	}
	if !onPath(filepath.Dir(env.Exe)) {
		fmt.Fprintf(stdout, "%s isn't on your PATH, so type its full path, %s, or add the folder to your PATH.\n", install.Printable(filepath.Dir(env.Exe)), install.Printable(env.Exe))
	}
	return 0
}

// leftOut writes, for --yes and --dry-run, which show no consent screen,
// each change that stays out for a reason the teen didn't give with --skip.
func leftOut(w io.Writer, prefix string, hook consent.HookTest, left map[string]string, skip []string) {
	if hook.Why != "" {
		fmt.Fprintf(w, "%sClaude Code's hooks stay out: %s.\n", prefix, hook.Why)
	}
	for _, id := range []string{install.ItemAutostart, consent.ItemCalibration, install.ItemClaudeCode, install.ItemWakaTime} {
		why, ok := left[id]
		if !ok || slices.Contains(skip, id) {
			continue
		}
		switch id {
		case install.ItemAutostart:
			fmt.Fprintf(w, "%spairtime won't start when you log in: %s.\n", prefix, install.Printable(why))
		case consent.ItemCalibration:
			fmt.Fprintf(w, "%sYour time won't show as \"calibrating\": %s.\n", prefix, install.Printable(why))
		case install.ItemWakaTime:
			fmt.Fprintf(w, "%spairtime won't get your editor time from WakaTime's plugins: %s.\n", prefix, install.Printable(why))
		default:
			fmt.Fprintf(w, "%sClaude Code's hooks stay out: %s.\n", prefix, install.Printable(why))
		}
	}
}

// wakaTimeItem returns the planner of the WakaTime item for pairtime's
// data directory in dirs, at now, with the port wakaTimePort picks.
func wakaTimeItem(dirs home.Dirs, now time.Time) func(install.Env) (install.Item, error) {
	return func(env install.Env) (install.Item, error) {
		return install.WakaTime(env, install.WakaTimeOptions{Data: dirs.Data, Port: wakaTimePort(dirs), Now: now})
	}
}

// shownPlan is what the screen's plan of an item read of each target it
// edits, and which of them it changes.
type shownPlan struct {
	read    map[install.Target]install.Content
	changed map[install.Target]bool
}

// shownOf returns what p, a plan of one item, read and changes.
func shownOf(p *install.Plan) shownPlan {
	s := shownPlan{read: p.Read(), changed: map[install.Target]bool{}}
	for _, c := range p.Changes {
		s.changed[c.Target] = true
	}
	return s
}

// wakaTimeReplan plans the WakaTime item again with o on port, the one the
// agent listens on, instead of o.Port, the screen's, and returns it with
// its diff, which install's apply then holds it to. It takes the new plan
// only when it edits the targets shown, the screen's plan, edits, and each
// target that either plan changes still holds what the screen's plan read.
// The item's edits depend only on what they read, the key, the port and
// o.Now, so the new plan is then the screen's with the agent's port in
// place of the screen's: also when it changes nothing, as when pairtime's
// line names the agent's port already, whatever its spacing, and when it
// moves a line the screen left as it was. A target neither plan changes
// can have changed since, as wakatime-internal.cfg does whenever
// wakatime-cli sends, which install's apply doesn't hold either. Anything
// else that changed since the screen, such as a config the teen edited,
// made or deleted in between, refuses it.
func wakaTimeReplan(env install.Env, in *install.Installer, o install.WakaTimeOptions, shown shownPlan, port int) (install.Item, string, error) {
	o.Port = port
	it, err := install.WakaTime(env, o)
	if err != nil {
		return install.Item{}, "", err
	}
	p, err := in.PlanInstall([]install.Item{it})
	if err != nil {
		return install.Item{}, "", err
	}
	defer p.Close()
	again := shownOf(p)
	changed := fmt.Errorf("what %s changes isn't what the screen showed any more, so pairtime didn't change it; run pairtime install again to see the new changes", install.ItemWakaTime)
	if len(again.read) != len(shown.read) {
		return install.Item{}, "", changed
	}
	for t, c := range again.read {
		s, ok := shown.read[t]
		if !ok || (shown.changed[t] || again.changed[t]) && !s.Same(c) {
			return install.Item{}, "", changed
		}
	}
	return it, p.Diff(), nil
}

// wakaTimePort returns the port pairtime's line in ~/.wakatime.cfg names:
// the agent's, as the agent picks it (localhttp.Candidates), config.toml's
// port first, then the one its last run listened on, then the default.
// Install plans the line again on the port the agent listens on once it
// is up (step 5), and an agent that moves to another port later rewrites
// the line (DESIGN 5.6, "The port").
func wakaTimePort(dirs home.Dirs) int {
	configured, last := 0, 0
	if cfg, _, err := config.Load(dirs.ConfigFile()); err == nil {
		configured = cfg.HTTP.Port
	}
	if pf, err := localhttp.ReadPortFile(dirs.PortFile()); err == nil {
		last = pf.Port
	}
	return localhttp.Candidates(configured, last)[0]
}

// planCalibration plans what --calibration-volunteer changes in the
// config.toml at name: the diff the consent screen and --dry-run show, ""
// when the file says shadow = true already, with what the teen wrote in it
// masked (config.Masked), or else why install leaves the change out:
// pairtime's scoring is frozen, as calibrating false says, or config's
// edit refuses the file, as one that doesn't parse (calibrationWhy).
func planCalibration(name string, calibrating bool) (diff, why string) {
	if !calibrating {
		return "", "pairtime's scoring has reached version 1.0.0, so its rules are frozen and your time shows as they count it"
	}
	before, after, err := config.PlanShadow(name, true)
	if err != nil {
		return "", calibrationWhy(err)
	}
	return install.FileDiff(name, config.Masked(before), config.Masked(after)), ""
}

// calibrationWhy says why config's edit refused config.toml, for the
// consent screen and the lines install prints with --yes and --dry-run:
// err's words without "config: ", but for a file that doesn't parse only
// the line it breaks at and what to write by hand, since the decoder's
// message can quote what the teen wrote there, and teens share that
// screen when they ask for help.
func calibrationWhy(err error) string {
	var u *config.UnparsedError
	if !errors.As(err, &u) {
		return strings.TrimPrefix(err.Error(), "config: ")
	}
	at := ""
	if u.Line > 0 {
		at = fmt.Sprintf(" at line %d", u.Line)
	}
	return "config.toml doesn't parse" + at + ", so pairtime leaves it alone; " + u.Hand
}

// hasClaudeHooks reports whether Claude Code's settings hold hooks of
// pairtime's, as uninstall would find them: those install's manifest
// records and any it recognizes as pairtime's. They run the binary at the
// per-user path, or, from an older pairtime, one somewhere else. A
// settings file uninstall couldn't take them out of counts as holding
// them, since it may.
func hasClaudeHooks(in *install.Installer, env install.Env) (bool, error) {
	it, err := install.ClaudeCode(env)
	if err != nil {
		return false, err
	}
	p, err := in.PlanUninstall(it)
	if err != nil {
		return false, err
	}
	defer p.Close()
	for _, c := range p.Changes {
		if slices.Contains(c.Items, install.ItemClaudeCode) {
			return true, nil
		}
	}
	settings := install.ClaudeSettings(env)
	return slices.ContainsFunc(p.Problems, func(problem string) bool { return strings.Contains(problem, settings) }), nil
}

// agentStarting runs just before install starts its agent at step 5.
// Tests set it to start an agent of another version there first, as a hook
// does that finds no agent while install puts the new binary in place,
// from the old binary that it still runs (#330).
var agentStarting = func(exe string, dirs home.Dirs) {}

// ownAgentTries is how many times install starts its agent before it gives
// up on an agent of another version taking its place (startOwnAgent).
const ownAgentTries = 3

// startOwnAgent starts exe's agent (startAgentFrom) and returns its status
// once an agent of this pairtime's version answers. An agent of another
// version can answer instead: one that a hook started from the old binary
// while install stopped the agent and put the new binary in place, which
// held the lock when install's own start came, and which startAgentFrom
// then returns. That agent is stopped and exe's started again, and so on,
// ownAgentTries times at most, saying so each time.
func startOwnAgent(exe string, dirs home.Dirs, stdout io.Writer) (agentStatus, error) {
	want := version.Get().Version
	for try := 1; ; try++ {
		st, err := startAgentFrom(exe, dirs)
		if err != nil {
			return agentStatus{}, err
		}
		if st.Version.Version == want {
			return st, nil
		}
		if try == ownAgentTries {
			return agentStatus{}, fmt.Errorf("the agent that answers is pairtime %s, process %d, not this pairtime's %s, after install stopped it %d times; stop it (it's the process called pairtime), then run pairtime install again",
				install.Printable(st.Version.Version), st.PID, want, ownAgentTries-1)
		}
		fmt.Fprintf(stdout, "Stopping pairtime's agent (process %d), which is pairtime %s, not this pairtime's %s, as a hook may have started it from the old pairtime while install put this one in place.\n",
			st.PID, install.Printable(st.Version.Version), want)
		if err := stopAgent(dirs); err != nil {
			return agentStatus{}, err
		}
	}
}

// linkedServer is the server pairtime login linked this computer to,
// while config.toml says online and reads (agent.UploadConfig), and ""
// otherwise: where config.toml lets its hours' numbers go, as the consent
// screen and setUpLine say. Install reads neither the sign-in nor the
// uploader's state, so neither promises an upload.
func linkedServer(dirs home.Dirs) string {
	l, err := account.Load(dirs.Data)
	if err != nil {
		return ""
	}
	if cfg, broken := agent.UploadConfig(dirs); broken != "" || cfg.Mode != config.Online {
		return ""
	}
	return l.Server
}

// unlinkedOnline reports whether config.toml reads and says online while no
// pairtime login linked this computer (agent.Linked). pairtime then uploads
// nothing, as the agent's problem.account says, and what it records before
// a login stays on this computer, since nothing sealed before the link
// uploads.
func unlinkedOnline(dirs home.Dirs) bool {
	cfg, broken := agent.UploadConfig(dirs)
	return broken == "" && cfg.Mode == config.Online && !agent.Linked(dirs)
}

// setUpLine is install's last word on where what pairtime records goes:
// it stays on this computer in local-only mode, as every install starts
// in; on a computer linked to server (linkedServer), config.toml lets its
// hours' numbers go there, while doctor says what holds them back; and on
// one whose config.toml says online though no login linked it (unlinked,
// unlinkedOnline), nothing goes until a login does.
func setUpLine(server string, unlinked bool) string {
	switch {
	case server != "":
		return fmt.Sprintf("pairtime is set up. This computer is linked to %s, and config.toml lets it upload your hours' numbers there; pairtime doctor says if anything holds them back.",
			install.Printable(server))
	case unlinked:
		return "pairtime is set up. " + consent.UnlinkedLine
	}
	return "pairtime is set up. It runs in local-only mode, so what it records stays on this computer."
}

// makeHome makes pairtime's directories and the folder of its binary, and
// records in setup.json, for uninstall to take away, each folder of made,
// which weren't there when install began (notThere), and of scriptMade,
// which an install script made for the binary before it ran install
// (--made). home makes the data and config directories, so they are this
// user's only (DESIGN 3.5), and on Windows the binary's folder inside the
// data directory inherits that. A data directory a script made took its
// parent's permissions instead, and gets home's here (protectDir).
func makeHome(env install.Env, dirs home.Dirs, made, scriptMade []string) error {
	if err := dirs.Create(); err != nil {
		return err
	}
	if slices.ContainsFunc(scriptMade, func(d string) bool { return samePath(d, dirs.Data) }) {
		if err := protectDir(dirs.Data); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(env.Exe), 0o755); err != nil {
		return err
	}
	rec, err := readSetup(dirs)
	if err != nil {
		return err
	}
	rec.Binary = env.Exe
	rec.Made = addMade(addMade(rec.Made, made), scriptMade)
	return writeSetup(dirs, rec)
}

// onPath reports whether dir is on this process's PATH, comparing paths as
// the OS does: in any case on Windows.
func onPath(dir string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d != "" && samePath(d, dir) {
			return true
		}
	}
	return false
}
