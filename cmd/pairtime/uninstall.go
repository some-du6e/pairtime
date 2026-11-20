// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
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
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/install"
)

const uninstallUsage = `Usage: pairtime uninstall [--yes] [--delete-data | --keep-data] [--dry-run]

Takes out everything pairtime install put in: pairtime's hooks, the login
item and the pairtime binary, and stops the agent. A file pairtime changed
gets back what it held before, byte for byte, unless something else changed
it since; then only pairtime's own entries come out. It works even when
pairtime's record of what it installed is gone.

Your pairtime data, the time you tracked and the key that seals it, stays
unless you say it should go. uninstall asks; --delete-data and --keep-data
answer for scripts.

  --yes          go ahead without asking; the data stays unless --delete-data
  --delete-data  delete your pairtime data too
  --keep-data    keep your pairtime data
  --dry-run      print what would come out, as diffs, and take out nothing

Exit status:
  0  uninstalled, or there was nothing to take out
  1  something went wrong, or something had to stay; the message says what
  2  usage error
  3  you said no, so nothing changed
`

func init() {
	register(command{
		name:    "uninstall",
		summary: "Take out everything install put in, and stop the agent",
		run:     runUninstall,
	})
}

// runUninstall is `pairtime uninstall` (docs/PLAN.md, WP-IN2), which
// reverses every step of install. The hooks come out first, so no harness
// starts the agent again once it has stopped, then the login item, then
// the agent stops, and while uninstall holds the agent's lock, so that no
// agent can start, the binary goes, and the data when the teen asks.
//
// It doesn't need install's records. The installer plans the undo of the
// items install offers on this machine as well as of what its manifest
// records (install.Installer.PlanUninstall), the binary is found at its
// per-user path and recognized by its build info, and pairtime's own
// directories are where home puts them. Without setup.json only the empty
// folders install made above those stay behind.
func runUninstall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), uninstallUsage) }
	yes := flags.Bool("yes", false, "go ahead without asking")
	deleteData := flags.Bool("delete-data", false, "delete your pairtime data too")
	keepData := flags.Bool("keep-data", false, "keep your pairtime data")
	dryRun := flags.Bool("dry-run", false, "print what would come out")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime uninstall: takes no arguments besides its flags, got %q\n", flags.Args())
		return 2
	}
	if *deleteData && *keepData {
		fmt.Fprintln(stderr, "pairtime uninstall: --delete-data and --keep-data can't go together")
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "pairtime uninstall: %v\n", err)
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
	rec, err := readSetup(dirs)
	if err != nil {
		fmt.Fprintf(stderr, "pairtime uninstall: %v; going on without it\n", err)
		rec = setupRecord{V: 1}
	}

	// What there is to take out.
	var items []install.Item
	for _, it := range []func(install.Env) (install.Item, error){install.Autostart, install.ClaudeCode} {
		if item, err := it(env); err == nil {
			items = append(items, item)
		}
	}
	in := install.New(dirs)
	// Planning makes the data directory, for the installer's lock, so what
	// is there has to be known first, and tidy takes away again what
	// uninstall made of it.
	missing := notThere(env, dirs)
	data := existing(dirs.Data, dirs.Config)
	running := agentLocked(dirs)
	defer tidy(in, dirs, missing)
	p, err := in.PlanUninstall(items...)
	if err != nil {
		return fail(err)
	}
	defer p.Close()
	var binaries []string
	for _, b := range []string{env.Exe, rec.Binary} {
		if b != "" && !slices.Contains(binaries, b) && isPairtimeBinary(b) {
			binaries = append(binaries, b)
		}
	}
	if p.Empty() && len(p.Problems) == 0 && len(binaries) == 0 && len(data) == 0 && !running {
		fmt.Fprintln(stdout, "pairtime isn't installed here, so there is nothing to take out.")
		return 0
	}

	if *dryRun {
		io.WriteString(stdout, p.Diff())
		for _, b := range binaries {
			fmt.Fprintf(stdout, "Binary files %s and /dev/null differ\n", install.Printable(b))
		}
		return 0
	}

	// The teen's say.
	answers := bufio.NewScanner(installStdin)
	if !*yes {
		showUninstall(stdout, p, binaries, running)
		if !askYes(answers, stdout, "Type y to take it all out, or n to stop without changing anything: ") {
			fmt.Fprintln(stdout, "Nothing changed.")
			return exitDeclined
		}
	}
	purge := *deleteData
	if len(data) > 0 && !*deleteData && !*keepData && !*yes {
		fmt.Fprintf(stdout, "\nYour pairtime data is in %s: the time you tracked, your recaps, and the key that seals what pairtime recorded. Once it's deleted, nobody can bring it back.\n", install.Printable(strings.Join(data, " and ")))
		purge = askYes(answers, stdout, "Type y to delete it too, or n to keep it: ")
	}

	// The hooks and the login item.
	problems := p.Problems
	if err := p.Apply(); err != nil {
		return fail(err)
	}
	if !p.Empty() {
		fmt.Fprintln(stdout, "Took out pairtime's hooks and its login item.")
	}

	// The agent, then the binary and the data, with the agent's lock held
	// so that a hook already on its way can't start an agent in between.
	// One that started before the hooks came out is stopped too.
	var lock *home.Lock
	for try := 0; ; try++ {
		if err := stopAgent(dirs); err != nil {
			return fail(err)
		}
		info, err := os.Stat(dirs.Data)
		if err != nil || !info.IsDir() {
			break
		}
		if lock, err = home.Acquire(dirs.Data); err == nil {
			break
		}
		if !errors.Is(err, home.ErrLocked) || try == 2 {
			return fail(fmt.Errorf("pairtime's agent keeps starting again (%v); run pairtime uninstall again", err))
		}
	}
	if running {
		fmt.Fprintln(stdout, "Stopped pairtime's agent.")
	}
	release := func() {
		if lock != nil {
			lock.Release()
			lock = nil
		}
	}
	defer release()

	// Folders install made, and pairtime's own, that go once they are
	// empty, innermost last here: removeEmptyDirs takes the innermost
	// first.
	after := slices.Clone(rec.Made)
	if dirs.Runtime != dirs.Data {
		after = addMade(after, []string{dirs.Runtime})
	}
	if purge {
		after = addMade(after, data)
	}
	later := ""
	for _, b := range binaries {
		if within(b, dirs.Data) {
			// Windows keeps the binary in a folder of the data directory,
			// which is pairtime's own. ~/.local/bin is the teen's, and goes
			// only when setup.json says install made it.
			after = addMade(after, []string{filepath.Dir(b)})
		}
		isSelf, err := sameBinaryFile(src, b)
		if err != nil {
			return fail(err)
		}
		if isSelf && !canRemoveRunning {
			later = b
			continue
		}
		if err := removeBinary(b); err != nil {
			return fail(fmt.Errorf("deleting %s: %w", b, err))
		}
	}
	if len(binaries) > 0 {
		fmt.Fprintf(stdout, "Deleted pairtime from %s.\n", install.Printable(strings.Join(binaries, " and ")))
	}

	if purge {
		if err := keys.Destroy(keys.Options{Dir: dirs.Data}); err != nil {
			release()
			return fail(fmt.Errorf("deleting pairtime's device key: %w; your data stays where it is, so a later uninstall can try again", err))
		}
		release()
		for _, d := range data {
			if err := os.RemoveAll(d); err != nil && !(later != "" && within(later, d)) {
				return fail(fmt.Errorf("deleting %s: %w", d, err))
			}
		}
		fmt.Fprintf(stdout, "Deleted your pairtime data from %s.\n", install.Printable(strings.Join(data, " and ")))
	} else {
		release()
	}
	if dirs.Runtime != dirs.Data {
		// The socket's folder holds nothing once the agent has stopped,
		// but a socket a crashed agent left behind.
		os.Remove(filepath.Join(dirs.Runtime, "agent.sock"))
	}
	kept := removeEmptyDirs(after)
	if later != "" {
		if err := removeAfterExit(later, kept); err != nil {
			fmt.Fprintf(stdout, "Windows doesn't let a program delete itself while it runs, so delete %s yourself once this has finished.\n", install.Printable(later))
		} else {
			fmt.Fprintf(stdout, "pairtime deletes %s once this has finished, because Windows doesn't let a program delete itself while it runs.\n", install.Printable(later))
		}
	}
	if !purge {
		rec.Binary = ""
		rec.Made = slices.DeleteFunc(rec.Made, func(d string) bool { return !slices.Contains(kept, d) })
		if err := writeSetup(dirs, rec); err != nil && len(data) > 0 {
			fmt.Fprintf(stderr, "pairtime uninstall: updating %s: %v\n", setupPath(dirs), err)
		}
		if len(data) > 0 {
			fmt.Fprintf(stdout, "Your pairtime data stays in %s. Delete it when you no longer want it.\n", install.Printable(strings.Join(data, " and ")))
		}
	}
	for _, problem := range problems {
		fmt.Fprintf(stdout, "Left alone: %s\n", install.Printable(problem))
	}
	if len(problems) > 0 {
		return 1
	}
	fmt.Fprintln(stdout, "pairtime is uninstalled.")
	return 0
}

// showUninstall shows what uninstall takes out.
func showUninstall(w io.Writer, p *install.Plan, binaries []string, running bool) {
	fmt.Fprintln(w, "pairtime uninstall")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This takes out everything pairtime install put in.")
	if !p.Empty() {
		fmt.Fprintln(w, "\nThese files and settings get back what they held before pairtime:")
		fmt.Fprintln(w)
		for _, line := range strings.SplitAfter(strings.TrimSuffix(p.Diff(), "\n"), "\n") {
			fmt.Fprintf(w, "    %s\n", strings.TrimSuffix(line, "\n"))
		}
	}
	for _, problem := range p.Problems {
		fmt.Fprintf(w, "\nThis has to stay: %s\n", install.Printable(problem))
	}
	if len(binaries) > 0 || running {
		fmt.Fprintln(w)
	}
	if running {
		fmt.Fprintln(w, "pairtime's agent stops.")
	}
	for _, b := range binaries {
		fmt.Fprintf(w, "The pairtime program in %s is deleted.\n", install.Printable(b))
	}
	fmt.Fprintln(w)
}

// askYes asks question until the answer is yes or no, and reports whether
// it was yes. The end of input is no.
func askYes(answers *bufio.Scanner, w io.Writer, question string) bool {
	for {
		fmt.Fprint(w, question)
		if !answers.Scan() {
			fmt.Fprintln(w)
			return false
		}
		switch strings.ToLower(strings.TrimSpace(answers.Text())) {
		case "y", "yes":
			return true
		case "n", "no", "q", "quit":
			return false
		}
	}
}

// existing returns those of dirs that are there, each once.
func existing(dirs ...string) []string {
	var out []string
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// sameBinaryFile reports whether a and b are the same file.
func sameBinaryFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}
