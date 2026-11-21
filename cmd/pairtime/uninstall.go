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
	"time"

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

It deletes pairtime's device key too, and with it the ledger and the rest
of what pairtime recorded, as pairtime's privacy notice says. Your daily
totals and recaps can stay until you delete them: uninstall asks, and
--delete-data and --keep-data answer for scripts.

  --yes          go ahead without asking; your daily totals and recaps
                 stay unless --delete-data
  --delete-data  delete your daily totals and recaps too
  --keep-data    keep your daily totals and recaps
  --dry-run      print what would come out, and take out nothing

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
// the agent stops. While uninstall holds the agent's lock, so that no
// agent can start and make a new key, pairtime's device key goes, then
// the data, then the binary.
//
// K_dev goes on every uninstall, as DESIGN 4.4 ("until uninstall or
// rotation") and the privacy notice say, and so do the ledger, the minute
// buckets, the vault and the rest of pairtime's data. Only the day rollups
// and coach records can stay, which 4.4 keeps until the teen deletes them
// (D-uninstall-key, issue #124). The key goes before anything else is
// deleted, so that when it can't go, pairtime is still there to try again
// with. Nothing writes coach/ yet. When WP-CO does, a moment that quotes a
// prompt, which DESIGN 3.5 keeps 30 days, has to be gone before coach/ is
// kept, or kept in the vault under K_dev, since no agent is left to cut it.
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
	deleteData := flags.Bool("delete-data", false, "delete your daily totals and recaps too")
	keepData := flags.Bool("keep-data", false, "keep your daily totals and recaps")
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
	keepable := existing(dirs.Days(), dirs.Coach())
	_, kerr := keys.Inspect(dirs.Data)
	hadKey := kerr == nil
	running := agentLocked(dirs)
	defer tidy(in, dirs, missing)
	p, err := in.PlanUninstall(items...)
	if err != nil {
		return fail(err)
	}
	defer p.Close()
	// Worked out before anything is deleted, since a file that is gone
	// can't be compared any more.
	binaries := pairtimeBinaries(src, slices.Concat([]string{env.Exe, rec.Binary}, strayBinaries(filepath.Dir(env.Exe), 0)))
	if p.Empty() && len(p.Problems) == 0 && len(binaries) == 0 && len(data) == 0 && !running {
		fmt.Fprintln(stdout, "pairtime isn't installed here, so there is nothing to take out.")
		return 0
	}

	if *dryRun {
		io.WriteString(stdout, p.Diff())
		for _, b := range binaries {
			fmt.Fprintf(stdout, "Binary files %s and /dev/null differ\n", install.Printable(b.path))
		}
		if len(data) > 0 {
			fmt.Fprintln(stdout, dataFate(hadKey, data, keepable, *deleteData, "would be deleted"))
		}
		return 0
	}

	// The teen's say.
	answers := bufio.NewScanner(installStdin)
	if !*yes {
		fate := ""
		if len(data) > 0 {
			fate = dataFate(hadKey, data, keepable, *deleteData, "is deleted")
		}
		showUninstall(stdout, p, binaries, running, fate)
		if !askYes(answers, stdout, "Type y to take it all out, or n to stop without changing anything: ") {
			fmt.Fprintln(stdout, "Nothing changed.")
			return exitDeclined
		}
	}
	purge := *deleteData
	if len(keepable) > 0 && !*deleteData && !*keepData && !*yes {
		fmt.Fprintf(stdout, "\nYour daily totals and recaps can stay, in %s, until you delete them. Once they're deleted, nobody can bring them back.\n", joinPaths(keepable))
		purge = askYes(answers, stdout, "Type y to delete them too, or n to keep them: ")
	}
	// What stays in the data directory unless it goes too: the day rollups
	// and coach records, also when the agent made them after the question.
	var keep []string
	if !purge {
		keep = []string{filepath.Base(dirs.Days()), filepath.Base(dirs.Coach())}
	}

	// The hooks and the login item.
	problems := slices.Clone(p.Problems)
	if err := p.Apply(); errors.Is(err, install.ErrStale) {
		return fail(errors.New("something else changed a file while uninstall was putting it back, so uninstall stopped there; run pairtime uninstall again"))
	} else if err != nil {
		return fail(err)
	}
	if !p.Empty() {
		fmt.Fprintln(stdout, "Took out pairtime's hooks and its login item.")
	}

	// The agent, then the key, the data and the binary, with the agent's
	// lock held, so that a hook already on its way can't start an agent in
	// between. One that started before the hooks came out is stopped too.
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

	// The device key, before any binary, so that pairtime is still there
	// when the key can't go: a keychain that is locked, or a Secret Service
	// that no session bus leads to, over SSH. The agent opens its keystore
	// in the background, so it may have made the key since the screen.
	if _, err := keys.Inspect(dirs.Data); err == nil {
		hadKey = true
	}
	keyDeleted := hadKey
	if err := keys.Destroy(keys.Options{Dir: dirs.Data, Stores: agentKeyStores}); err != nil {
		keyDeleted = false
		if _, ierr := keys.Inspect(dirs.Data); !errors.Is(ierr, fs.ErrNotExist) {
			release()
			return fail(fmt.Errorf("couldn't delete pairtime's device key (%v), so pairtime and its data stay where they are; unlock your keychain or keyring, then run pairtime uninstall again", err))
		}
		// keystore.json is gone, so trying again would find nothing: what
		// is left is a key in a store this system doesn't have, such as a
		// keyring of the OS the data directory came from, which err names.
		problems = append(problems, err.Error())
	}

	// The binary this process runs from, which Windows deletes only once
	// this process has exited (removeAfterExit).
	var later, paths []string
	for _, b := range binaries {
		if b.self && !canRemoveRunning {
			later = append(later, b.path)
		}
		paths = append(paths, b.path)
	}

	// The data: everything but what the teen keeps. A linked data directory
	// loses what pairtime put where it points, and then the link, unless
	// something is kept there. On Windows the binaries are in the data
	// directory, and what is left of them goes with the binaries below.
	for _, d := range data {
		var keepHere []string
		if samePath(d, dirs.Data) {
			keepHere = keep
		}
		if err := clearData(d, keepHere, paths); err != nil {
			release()
			return fail(fmt.Errorf("deleting your pairtime data: %w; close any program that has one of pairtime's files open, then run pairtime uninstall again", err))
		}
	}
	var stayed []string // the day rollups and coach records that stay
	if !purge {
		stayed = existing(dirs.Days(), dirs.Coach())
	}
	if len(data) > 0 {
		key := ""
		if keyDeleted {
			key = "pairtime's device key and "
		}
		if len(stayed) > 0 {
			// Only they stay, so install's record of the folders it made
			// goes too, and a folder it made above the data directory
			// stays once that is deleted.
			fmt.Fprintf(stdout, "Deleted %severything else pairtime kept in %s. Your daily totals and recaps stay in %s; delete %s when you no longer want them.\n", key, joinPaths(data), joinPaths(stayed), install.Printable(dirs.Data))
		} else {
			fmt.Fprintf(stdout, "Deleted %syour pairtime data from %s.\n", key, joinPaths(data))
		}
	}

	// The binaries.
	var deleted []string
	for _, b := range binaries {
		if b.self && !canRemoveRunning {
			continue
		}
		if err := removeBinary(b.path); err != nil {
			if !canRemoveRunning {
				// Something still runs it, and it goes once that has ended.
				later = append(later, b.path)
				continue
			}
			release()
			return fail(fmt.Errorf("deleting %s: %w; make sure you may change the folder it's in, then run pairtime uninstall again", install.Printable(b.path), err))
		}
		deleted = append(deleted, b.path)
	}
	if len(deleted) > 0 {
		fmt.Fprintf(stdout, "Deleted pairtime from %s.\n", joinPaths(deleted))
	}

	// The lock files go last, while uninstall holds the agent's
	// (removeAgentLock), and the install lock's only if no other install
	// holds it (RemoveLockFile).
	removeAgentLock(dirs.Data, lock)
	lock = nil
	in.RemoveLockFile()
	if dirs.Runtime != dirs.Data {
		// The socket's folder holds nothing once the agent has stopped,
		// but a socket a crashed agent left behind.
		os.Remove(filepath.Join(dirs.Runtime, "agent.sock"))
	}

	// Folders install made, and pairtime's own, that go once they are
	// empty, innermost last here: removeEmptyDirs takes the innermost
	// first. A linked data directory's link goes after them, so that its
	// folders are found through it.
	after := slices.Clone(rec.Made)
	if dirs.Runtime != dirs.Data {
		after = addMade(after, []string{dirs.Runtime})
	}
	after = addMade(after, data)
	for _, b := range binaries {
		if within(b.path, dirs.Data) {
			// Windows keeps the binary in a folder of the data directory,
			// which is pairtime's own. ~/.local/bin is the teen's, and goes
			// only when setup.json says install, or the install script,
			// made it.
			after = addMade(after, []string{filepath.Dir(b.path)})
		}
	}
	kept := removeEmptyDirs(after)
	// A link that stood in for a data directory goes now, but not what it
	// points to, unless something stays behind it. When the binary this
	// process runs from is behind it, PowerShell deletes the link once it
	// has deleted the binary and its folder through it.
	var links []string
	for _, d := range data {
		switch {
		case !linkedDir(d) || (len(stayed) > 0 && samePath(d, dirs.Data)):
		case slices.ContainsFunc(later, func(f string) bool { return within(f, d) }):
			links = append(links, d)
		default:
			os.Remove(d)
		}
	}
	stays := false // a binary that nothing will delete
	if len(later) > 0 {
		if err := removeAfterExit(later, realDirs(kept), links); err != nil {
			fmt.Fprintf(stdout, "Windows doesn't let anyone delete a program while it runs, so delete %s yourself once this has finished.\n", joinPaths(later))
			stays = true
		} else {
			fmt.Fprintf(stdout, "pairtime deletes %s once this has finished, because Windows doesn't let anyone delete a program while it runs.\n", joinPaths(later))
		}
	}
	for _, problem := range problems {
		fmt.Fprintf(stdout, "Left alone: %s\n", install.Printable(problem))
	}
	if len(problems) > 0 || stays {
		return 1
	}
	fmt.Fprintln(stdout, "pairtime is uninstalled.")
	return 0
}

// dataFate says in one sentence what uninstall does with pairtime's data
// directories data, given that a device key is there (hadKey), keepable,
// the day rollups and coach records that are there, and whether those go
// too (purge). done is the verb, such as "is deleted".
func dataFate(hadKey bool, data, keepable []string, purge bool, done string) string {
	what := "Everything pairtime recorded in " + joinPaths(data)
	if hadKey {
		what = "pairtime's device key, and with it everything pairtime recorded in " + joinPaths(data) + ","
	}
	if len(keepable) == 0 || purge {
		return what + " " + done + "."
	}
	return what + " " + done + ", but for your daily totals and recaps in " + joinPaths(keepable) + ", which can stay."
}

// showUninstall shows what uninstall takes out.
func showUninstall(w io.Writer, p *install.Plan, binaries []binary, running bool, fate string) {
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
	if len(binaries) > 0 || running || fate != "" {
		fmt.Fprintln(w)
	}
	if running {
		fmt.Fprintln(w, "pairtime's agent stops.")
	}
	for _, b := range binaries {
		fmt.Fprintf(w, "The pairtime program in %s is deleted.\n", install.Printable(b.path))
	}
	if fate != "" {
		fmt.Fprintln(w, fate)
	}
	fmt.Fprintln(w)
}

// askYes asks question until the answer is yes or no, and reports whether
// it was yes. The end of input is no, and so is any answer to a question
// that couldn't be written, since the teen didn't see it.
func askYes(answers *bufio.Scanner, w io.Writer, question string) bool {
	for {
		if _, err := fmt.Fprint(w, question); err != nil {
			return false
		}
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

// joinPaths writes paths for a sentence, with the characters a terminal
// acts on written out.
func joinPaths(paths []string) string { return install.Printable(strings.Join(paths, " and ")) }

// binary is a pairtime binary uninstall deletes.
type binary struct {
	path string
	info fs.FileInfo
	// self says that this process runs from it.
	self bool
}

// pairtimeBinaries returns those of paths that are pairtime binaries, each
// file once, however its path is spelled: setup.json may name the binary
// through another spelling of the home directory, such as /home/teen and
// /var/home/teen on Fedora Atomic. It marks the one this process runs
// from, src, which isn't one of them when it is gone.
func pairtimeBinaries(src string, paths []string) []binary {
	srcInfo, _ := os.Stat(src)
	var out []binary
	for _, p := range paths {
		if p == "" || !isPairtimeBinary(p) {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || slices.ContainsFunc(out, func(b binary) bool { return os.SameFile(b.info, info) }) {
			continue
		}
		out = append(out, binary{path: p, info: info, self: srcInfo != nil && os.SameFile(srcInfo, info)})
	}
	return out
}

// pairtimeEntries are the names of what pairtime keeps in its data and
// config directories (DESIGN 3.5; home.Dirs and the packages that write
// there).
var pairtimeEntries = []string{"account", "account.json", "agent.lock", "agent.sock", "bin", "buckets", "coach", "commits", "config.toml", "days",
	"held.jsonl", "held.jsonl.pending", "held.jsonl.replay", "inbox", "install", "install.lock", "keys", "ledger", "outbox.json",
	"pipe.json", "port.json", "received", "state.json", "state.json.bad", "uploads.jsonl", "vault"}

// isPairtimeEntry reports whether name is one of pairtimeEntries, or the
// temporary file of an atomic write of one of them (home.WriteFile).
func isPairtimeEntry(name string) bool {
	return slices.Contains(pairtimeEntries, name) || slices.ContainsFunc(pairtimeEntries, func(e string) bool {
		return strings.HasPrefix(name, "."+e+".tmp-")
	})
}

// clearData deletes what dir, one of pairtime's directories, holds, but the
// entries named in keep and the two lock files, which go once uninstall is
// done with them (removeAgentLock, install.Installer.RemoveLockFile). A
// link in dir goes, not what it points to.
//
// When dir is itself a link or a junction, as when the teen moved
// pairtime's data to a bigger drive, it deletes only pairtime's entries
// (pairtimeEntries) in the folder the link points to, which could hold
// anything, and the link goes later. binaries are pairtime's binaries, which
// on Windows are in the data directory: one that can't be deleted yet, as
// the one this process runs from can't, goes with the binaries later, so a
// folder that holds nothing else afterwards isn't an error.
func clearData(dir string, keep, binaries []string) error {
	linked := linkedDir(dir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if slices.Contains(keep, name) || name == "agent.lock" || name == "install.lock" || (linked && !isPairtimeEntry(name)) {
			continue
		}
		if err := removeTree(filepath.Join(dir, name), binaries); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removeTree deletes path and what it holds. What is left of binaries, and
// the folders that hold them, is no error. Where a running program's file
// can't be deleted, on Windows, it tries again for a few seconds, as
// removeBinary does: an agent that just stopped, or a virus scanner, may
// keep a file in use a moment longer.
func removeTree(path string, binaries []string) error {
	for i := 0; ; i++ {
		err := os.RemoveAll(path)
		if err == nil || onlyHolds(path, binaries) {
			return nil
		}
		if canRemoveRunning || i == 49 {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// onlyHolds reports whether all that is left at path is some of files and
// the folders that hold them.
func onlyHolds(path string, files []string) bool {
	if len(files) == 0 {
		return false
	}
	only := true
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
		case slices.ContainsFunc(files, func(f string) bool { return samePath(p, f) }):
			return nil
		case d.IsDir() && slices.ContainsFunc(files, func(f string) bool { return within(f, p) }):
			return nil
		}
		only = false
		return filepath.SkipAll
	})
	return only
}

// linkedDir reports whether dir is a link or a junction to a folder, not a
// folder itself.
func linkedDir(dir string) bool {
	info, err := os.Lstat(dir)
	return err == nil && info.Mode().Type() != fs.ModeDir
}

// realDirs returns those of dirs that are folders, not links or junctions
// standing in for one, which removeAfterExit's Directory.Delete would
// delete whatever they point to.
func realDirs(dirs []string) []string {
	return slices.DeleteFunc(slices.Clone(dirs), func(d string) bool {
		info, err := os.Lstat(d)
		return err != nil || info.Mode().Type() != fs.ModeDir
	})
}
