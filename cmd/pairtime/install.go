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

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/install/consent"
	"github.com/some-du6e/pairtime/internal/version"
)

// exitDeclined is the exit status of install and uninstall when the teen
// says no, or gives no answer, so nothing changed.
const exitDeclined = 3

const installUsage = `Usage: pairtime install [--yes] [--skip <item>]... [--dry-run]

Sets pairtime up on this computer, without admin rights. It puts the
pairtime binary in your own programs folder, finds the AI tools you use,
and shows every change it would make, each with a diff, before it makes
any: starting pairtime when you log in, and the hooks that tell pairtime
when you prompt Claude Code. Untick what you don't want, then type y.
Running it again changes nothing that is set up already, and
pairtime uninstall takes everything back out.

  --yes        make every change without asking, for scripts
  --skip item  leave one change out: autostart, or hooks/claude_code
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
	if v != install.ItemAutostart && v != install.ItemClaudeCode {
		return fmt.Errorf("there is no item %q; the items are %s and %s", v, install.ItemAutostart, install.ItemClaudeCode)
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
// The agent runs in local-only mode, the only mode of this release, which
// it writes into config.toml at its first start (internal/agent/config).
// Once it runs, the agent reads the last 30 days of transcripts by itself,
// at low priority (lookback, internal/agent/tailers), and its status says
// how far it got; the progress bar and the welcome recap come with the
// dashboard's work packages, and the WakaTime line, Karim's AGENTS.md
// entry and sign-in with theirs.
func runInstall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), installUsage) }
	yes := flags.Bool("yes", false, "make every change without asking")
	dryRun := flags.Bool("dry-run", false, "print the changes and make none")
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
	left := map[string]string{} // the items install doesn't offer, with why
	offer := func(id string, mk func(install.Env) (install.Item, error)) error {
		it, err := mk(env)
		if err == nil {
			var p *install.Plan
			if p, err = in.PlanInstall([]install.Item{it}); err == nil {
				items[id], diffs[id] = it, p.Diff()
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
	screen := consent.New(consent.Input{
		GOOS:      env.GOOS,
		Binary:    env.Exe,
		Harnesses: harnesses,
		Hook:      hook,
		Diffs:     diffs,
		Left:      left,
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
	st, err := queryAgent(dirs)
	if err != nil || restart || st.Version.Version != version.Get().Version {
		if err == nil {
			// An agent of another version runs, perhaps from another copy
			// of pairtime: the one that runs from now on is this one.
			fmt.Fprintf(stdout, "Stopping pairtime's agent (process %d), which is another version.\n", st.PID)
			if err := stopAgent(dirs); err != nil {
				return fail(err)
			}
		}
		if st, err = startAgentFrom(env.Exe, dirs); err != nil {
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

	fmt.Fprintln(stdout, "pairtime is set up. It runs in local-only mode, so what it records stays on this computer.")
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
	for _, id := range []string{install.ItemAutostart, install.ItemClaudeCode} {
		why, ok := left[id]
		if !ok || slices.Contains(skip, id) {
			continue
		}
		if id == install.ItemAutostart {
			fmt.Fprintf(w, "%spairtime won't start when you log in: %s.\n", prefix, install.Printable(why))
		} else {
			fmt.Fprintf(w, "%sClaude Code's hooks stay out: %s.\n", prefix, install.Printable(why))
		}
	}
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
