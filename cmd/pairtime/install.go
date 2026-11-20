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

// installStdin is where install and uninstall read the teen's answers.
// Tests replace it.
var installStdin io.Reader = os.Stdin

// runInstall is `pairtime install` (DESIGN 5.9 steps 1 to 4; docs/PLAN.md,
// WP-IN2). In order:
//
//  1. It finds the harnesses (install.Detect) and tests the hook command
//     the hooks would run (hookCheck), then plans each item it offers.
//  2. It shows the consent screen, with each change as a diff, and the
//     teen unticks what they don't want (--yes for scripts).
//  3. It puts the binary at its per-user path, with pairtime's own
//     folders, and tests the hook command again there, since that copy is
//     what the hooks run.
//  4. It writes autostart and starts the agent, then merges the hooks, so
//     the first hook call finds the agent up and goes over its socket: a
//     call that finds it down is spooled, and a spooled call earns only a
//     side door's credit (DESIGN 5.2).
//
// The agent runs in local-only mode, the only mode of this release, which
// it writes into config.toml at its first start (internal/agent/config).
// Lookback over old transcripts arrives with WP-AD3, and the WakaTime
// line, Karim's AGENTS.md entry and sign-in with their work packages.
func runInstall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), installUsage) }
	yes := flags.Bool("yes", false, "make every change without asking")
	dryRun := flags.Bool("dry-run", false, "print the changes and make none")
	var skip skipFlag
	flags.Var(&skip, "skip", "leave one change out")
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
	src, err := self()
	if err != nil {
		return fail(err)
	}
	in := install.New(dirs)

	// 1. What there is, and what install would change.
	harnesses := install.Detect(env)
	claudeFound := slices.ContainsFunc(harnesses, func(h install.Harness) bool { return h.Item == install.ItemClaudeCode && h.Found() })
	var hook consent.HookTest
	if claudeFound {
		took, why := hookCheck(src)
		hook = consent.HookTest{OK: why == "", Took: took, Why: why}
	}
	items := map[string]install.Item{}
	diffs := map[string]string{}
	offer := func(it install.Item, err error) error {
		if err != nil {
			return err
		}
		p, err := in.PlanInstall([]install.Item{it})
		if err != nil {
			return err
		}
		defer p.Close()
		items[it.ID], diffs[it.ID] = it, p.Diff()
		return nil
	}
	if err := offer(install.Autostart(env)); err != nil {
		return fail(err)
	}
	if claudeFound && hook.OK {
		if err := offer(install.ClaudeCode(env)); err != nil {
			return fail(err)
		}
	}
	screen := consent.New(consent.Input{
		GOOS:      env.GOOS,
		Binary:    env.Exe,
		Harnesses: harnesses,
		Hook:      hook,
		Diffs:     diffs,
	})
	for _, id := range skip {
		screen.Untick(id)
	}

	// 2. The teen's say.
	same, err := sameBinary(src, env.Exe)
	if err != nil {
		return fail(err)
	}
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
		return 0
	case *yes:
		if claudeFound && !hook.OK {
			fmt.Fprintf(stdout, "Claude Code's hooks stay out: %s.\n", hook.Why)
		}
	default:
		if !screen.Ask(installStdin, stdout) {
			fmt.Fprintln(stdout, "Nothing changed.")
			return exitDeclined
		}
	}
	chosen := screen.Ticked()

	// 3. The binary, at the path every hook and the login item run.
	restart := false
	if !same {
		if st, err := queryAgent(dirs); err == nil {
			// The running agent may be this copy, which Windows won't let
			// anyone replace, and is an older pairtime anyway.
			fmt.Fprintf(stdout, "Stopping pairtime's agent (process %d) to update it.\n", st.PID)
			if err := stopAgent(dirs); err != nil {
				return fail(err)
			}
			restart = true
		}
	}
	if err := makeHome(env, dirs); err != nil {
		return fail(err)
	}
	if !same {
		if err := placeBinary(src, env.Exe); err != nil {
			return fail(fmt.Errorf("putting pairtime in %s: %w", env.Exe, err))
		}
		fmt.Fprintf(stdout, "Put pairtime in %s.\n", install.Printable(env.Exe))
	}
	if slices.Contains(chosen, install.ItemClaudeCode) {
		if _, why := hookCheck(env.Exe); why != "" {
			chosen = slices.DeleteFunc(chosen, func(id string) bool { return id == install.ItemClaudeCode })
			fmt.Fprintf(stdout, "Claude Code's hooks stay out: %s.\n", why)
		}
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
		return p.Apply()
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
	if claudeFound && slices.Contains(chosen, install.ItemClaudeCode) {
		fmt.Fprintln(stdout, "Claude Code runs the hooks in a folder once you trust that folder, and in sessions that start after now.")
	}
	if env.GOOS != "windows" && !onPath(filepath.Dir(env.Exe)) {
		fmt.Fprintf(stdout, "%s isn't on your PATH, so type its full path, %s, or add the folder to your PATH.\n", install.Printable(filepath.Dir(env.Exe)), install.Printable(env.Exe))
	}
	return 0
}

// makeHome makes pairtime's directories and the folder of its binary, and
// records in setup.json each folder that wasn't there, for uninstall to
// take away. The data and config directories are made by home first, so
// they are this user's only (DESIGN 3.5), and on Windows the binary's
// folder inside the data directory inherits that.
func makeHome(env install.Env, dirs home.Dirs) error {
	var made []string
	for _, d := range []string{dirs.Data, dirs.Config, filepath.Dir(env.Exe)} {
		made = addMade(made, missingDirs(d))
	}
	if err := dirs.Create(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(env.Exe), 0o755); err != nil {
		return err
	}
	rec, err := readSetup(dirs)
	if err != nil {
		return err
	}
	rec.Binary = env.Exe
	rec.Made = addMade(rec.Made, made)
	return writeSetup(dirs, rec)
}

// onPath reports whether dir is on this process's PATH.
func onPath(dir string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d != "" && filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}
