// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/projects"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/install"
)

const privacyDeleteUsage = `Usage: pairtime privacy delete [--yes]

Deletes pairtime's device key and everything pairtime recorded on this
computer ("delete everything"): the ledger, the vault with the text of your
prompts, the minute buckets, your daily totals and recaps, the commit
records, the hook calls waiting to be read, the record of past uploads, and
this computer's sign-in to the pairtime server. Without the key, what
pairtime recorded before can't be opened or checked, so those hours can't
be submitted any more. Copies that a backup or a synced folder already
holds stay as they are, the key's file too when pairtime doctor says
pairtime keeps its key in a file.

It also takes out of config.toml, at {config}, the names you gave the
projects this computer knows of, and sets it back to local-only mode, so
pairtime uploads nothing until you sign in again. A table there for any
other project, as another computer sharing the file writes or an older
version of pairtime left behind, stays, and pairtime says how many it left.
pairtime itself and your other settings stay. So does install's record of
what it changed, with its copies of the files it edited, so that pairtime
uninstall can put them back. pairtime starts again from nothing, under a
new device key, and signed out: pairtime login links this computer again.
It sends nothing to the pairtime server: what the server already has, a
name your dashboard shows included, stays there until you hide that name
or remove this computer on your dashboard.

  --yes   go ahead without asking

Exit status:
  0  deleted, or there was nothing to delete
  1  something went wrong, or some of the device key had to stay where
     pairtime can't reach it, or a name you gave one of the projects this
     computer knows of had to stay in config.toml; the message says what,
     and what stays
  2  usage error
  3  you said no, so nothing changed
`

// wipeKeeps are the entries of the data directory that "delete everything"
// leaves, because they are pairtime itself and its settings, not what it
// recorded: config.toml, which is in the data directory on macOS and under
// PAIRTIME_HOME, and which loses only the names the teen gave their
// projects (config.ForgetNames), and config.lock, which every edit of it
// locks (internal/agent/config); install's record of what it put in, which
// uninstall reads, and its lock (internal/install); the binaries on
// Windows; the locks of the agent and of pairtime login, and the agent's
// socket.
var wipeKeeps = []string{"account", "agent.lock", "agent.sock", "bin", "config.lock", "config.toml", "install", "install.lock"}

// runPrivacyDelete is `pairtime privacy delete`, DESIGN 8.8's "delete
// everything" (docs/PLAN.md, WP-LG6): it wipes what pairtime recorded in
// the data directory and K_dev. Like uninstall, it stops the agent and
// holds the agent's lock while it deletes, so no agent writes a record in
// between, and it deletes the device key first: when the key can't go,
// nothing else does, since a data directory without its ledger and a key
// whose chain keystore.json records would keep the ledger from ever
// opening again (internal/agent/ledger's ErrKeyReuse). The agent starts
// again afterwards if it was running.
func runPrivacyDelete(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("privacy delete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		cfg := "pairtime's config folder"
		if dirs, err := home.Default(); err == nil {
			cfg = install.Printable(dirs.ConfigFile())
		}
		fmt.Fprint(flags.Output(), strings.ReplaceAll(privacyDeleteUsage, "{config}", cfg))
	}
	yes := flags.Bool("yes", false, "go ahead without asking")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime privacy delete: takes no arguments besides its flags, got %q\n", flags.Args())
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "pairtime privacy delete: %v\n", err)
		return 1
	}
	dirs, err := home.Default()
	if err != nil {
		return fail(err)
	}
	recorded, err := wipeEntries(dirs.Data)
	if err != nil {
		return fail(err)
	}
	_, kerr := keys.Inspect(dirs.Data)
	running := agentLocked(dirs)
	// The names the teen gave the projects this computer knows of, in
	// config.toml, go too, and any other project's stay, counted
	// (D-wipe-project-labels).
	ofMine, others, _ := config.ProjectTables(dirs.ConfigFile(), wipedKeys(dirs).has)
	if len(recorded) == 0 && kerr != nil && !running && ofMine == 0 {
		fmt.Fprintln(stdout, "pairtime has recorded nothing on this computer, so there is nothing to delete.")
		leftAlone(stdout, dirs, others)
		return 0
	}
	if !*yes {
		fmt.Fprintf(stdout, "This deletes pairtime's device key and everything pairtime recorded in %s: the text of your prompts it kept, your time, your daily totals and recaps, and this computer's sign-in. It also takes out of %s the names you gave the projects this computer knows of, and sets it back to local-only mode, so pairtime uploads nothing until you sign in again. A table there for any other project, as another computer sharing the file writes or an older version of pairtime left behind, stays, and pairtime says how many. The hours pairtime recorded so far can't be submitted any more. Copies a backup or a synced folder already holds stay as they are, the key's file too when pairtime doctor says pairtime keeps its key in a file. pairtime and your other settings stay, and so does install's record of what it changed, with its copies of the files it edited, so that pairtime uninstall can put them back. It sends nothing to the pairtime server, where a name your dashboard shows stays until you hide it there or remove this computer. pairtime starts again from nothing.\n\n", install.Printable(dirs.Data), install.Printable(dirs.ConfigFile()))
		if !askYes(bufio.NewScanner(installStdin), stdout, "Type y to delete everything, or n to stop without changing anything: ") {
			fmt.Fprintln(stdout, "Nothing changed.")
			return exitDeclined
		}
	}

	// The agent stops, and its lock is held until the end, so that no
	// agent records anything while the key and the data go. The hooks
	// stay, and one that finds the agent down starts another, which waits
	// for the lock and stops again (agent.ErrAlreadyRunning).
	var lock *home.Lock
	for try := 0; ; try++ {
		if err := stopAgent(dirs); err != nil {
			return fail(err)
		}
		if lock, err = home.Acquire(dirs.Data); err == nil {
			break
		}
		if !errors.Is(err, home.ErrLocked) || try == 2 {
			return fail(fmt.Errorf("pairtime's agent keeps starting again (%v); run pairtime privacy delete again", err))
		}
	}
	release := func() {
		if lock != nil {
			lock.Release()
			lock = nil
		}
	}
	defer release()
	// The keys of the projects this computer knows of, read with the agent
	// stopped and before anything goes, since the files they are in go.
	mine := wipedKeys(dirs)

	// The key first: without it, what is left can't be opened or checked
	// by anyone (crypto-shredding, DESIGN 3.5), and with it still there,
	// the data has to stay too.
	var keyStays error // why some of the key stays where pairtime can't reach it
	if err := keys.Destroy(keys.Options{Dir: dirs.Data, Stores: agentKeyStores}); err != nil {
		if _, ierr := keys.Inspect(dirs.Data); !errors.Is(ierr, fs.ErrNotExist) {
			release()
			return fail(fmt.Errorf("couldn't delete pairtime's device key (%v), so nothing was deleted; unlock your keychain or keyring, then run pairtime privacy delete again", err))
		}
		// keystore.json is gone, so trying again would find nothing: what is
		// left of the key is in a store this system doesn't have, or one a
		// damaged keystore.json can't name, which err says.
		keyStays = err
	}
	// config.toml next, once the key is gone and before the data goes, so
	// that a wipe that stops part of the way through has taken the names
	// out too, by the keys read above, which a second run couldn't read
	// again. The sign-in goes with the keystore and account.json, so the
	// mode goes back to local-only, as pairtime logout leaves it. The names
	// the teen gave the projects this computer knows of go: they are data
	// about the teen, and no project's key names them once K_dev is gone
	// (D-wipe-project-labels). The agent is stopped and the lock held, so no
	// copy of config.toml an agent read writes them back. Any other
	// project's tables stay, and leftAlone counts them.
	cfgPath := install.Printable(dirs.ConfigFile())
	mode := ""
	if cfg, _, err := config.Load(dirs.ConfigFile()); err == nil && cfg.Mode == config.Online {
		if err := config.SetLocal(dirs.ConfigFile()); err != nil {
			fmt.Fprintf(stdout, "Left alone: config.toml still says mode = \"online\" (%v). Nothing uploads without a sign-in, and pairtime login links this computer again.\n", err)
		} else {
			mode = " " + cfgPath + " is back in local-only mode, so pairtime uploads nothing until you sign in again."
		}
	} else if err == nil {
		mode = " " + cfgPath + " stays in local-only mode, so pairtime uploads nothing until you sign in again."
	}
	forgot, namesStay := forgetNames(dirs, mine)
	names := ""
	if namesStay == nil && forgot.Removed > 0 {
		names = ", and from " + cfgPath + " the names you gave the projects this computer knows of"
	}
	if err := clearData(dirs.Data, wipeKeeps, nil); err != nil {
		// Part of what was recorded stays, so nothing says it was all
		// deleted, and the agent isn't started again over it.
		if names != "" {
			fmt.Fprintf(stdout, "Took out of %s the names you gave the projects this computer knows of.%s\n", cfgPath, mode)
		} else if mode != "" {
			fmt.Fprintln(stdout, strings.TrimSpace(mode))
		}
		if keyStays != nil {
			fmt.Fprintf(stdout, "Left alone: %s\n", install.Printable(keyStays.Error()))
		}
		namesLeftAlone(stdout, namesStay)
		leftAlone(stdout, dirs, forgot.Left)
		release()
		return fail(fmt.Errorf("pairtime's device key is deleted, but some of what it recorded isn't: %w; close any program that has one of pairtime's files open, then run pairtime privacy delete again", err))
	}
	const rest = " Your other settings stay, and so does install's record of what it changed, with its copies of the files it edited, so that pairtime uninstall can put them back. It sent nothing to the pairtime server, where a name your dashboard shows stays until you hide it there or remove this computer."
	if keyStays != nil {
		fmt.Fprintf(stdout, "Deleted everything pairtime recorded in %s%s.%s%s\nLeft alone: %s\n", install.Printable(dirs.Data), names, mode, rest, install.Printable(keyStays.Error()))
	} else {
		fmt.Fprintf(stdout, "Deleted pairtime's device key and everything pairtime recorded in %s%s.%s%s\n", install.Printable(dirs.Data), names, mode, rest)
	}
	namesLeftAlone(stdout, namesStay)
	leftAlone(stdout, dirs, forgot.Left)
	release()
	code := 0
	if keyStays != nil || namesStay != nil {
		code = 1
	}

	if running {
		exe, err := os.Executable()
		if err == nil {
			_, err = startAgentFrom(exe, dirs)
		}
		if err != nil {
			fmt.Fprintf(stdout, "pairtime's agent starts again with your next prompt; starting it now failed: %v\n", err)
			return code
		}
		fmt.Fprintln(stdout, "pairtime's agent started again, under a new device key.")
	}
	return code
}

// wipeEntries returns the entries of the data directory dir that "delete
// everything" deletes: all but wipeKeeps, or when dir is a link, all of
// pairtime's own entries there but wipeKeeps (clearData).
func wipeEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	linked := linkedDir(dir)
	var out []string
	for _, e := range entries {
		name := e.Name()
		if slices.Contains(wipeKeeps, name) || name == "keys" && keysOnlyLock(filepath.Join(dir, name)) || linked && !isPairtimeEntry(name) {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// keysOnlyLock reports whether the keystore directory dir holds nothing but
// its lock, as Destroy leaves it.
func keysOnlyLock(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return !slices.ContainsFunc(entries, func(e fs.DirEntry) bool { return !strings.EqualFold(e.Name(), "agent.lock") })
}

// keySet is a set of project keys.
type keySet map[string]bool

// has reports whether key is in s.
func (s keySet) has(key string) bool { return s[key] }

// wipedKeys returns the keys of the projects this computer knows of, whose
// tables in config.toml "delete everything" takes out
// (D-wipe-project-labels): the git adapter's project map in state.json,
// with each key one of its repos had before, every day's rollup in days/,
// and from project-names.json its codes, its confirmed and sent names, the
// keys it deletes or lists as hidden, and its notes of a name hidden on the
// dashboard or dropped. A note of a name signed in elsewhere, or that
// couldn't be read, counts only when state.json or a rollup has its key
// too: SignedInElsewhere and loadNames write those for every name
// config.toml has on (cfg.NamesOn), which in a config.toml computers share
// holds another computer's keys. A file that doesn't read gives none. The
// tables of keys it doesn't give stay, and leftAlone counts them.
func wipedKeys(dirs home.Dirs) keySet {
	keys := keySet{}
	if m, err := projects.ReadMap(dirs.StateFile()); err == nil {
		for _, r := range m.Repos {
			keys[r.Key] = true
			for _, a := range r.Aliases {
				keys[a] = true
			}
		}
	}
	var dates []string
	if entries, err := os.ReadDir(dirs.Days()); err == nil {
		for _, e := range entries {
			date, ok := strings.CutSuffix(e.Name(), ".json")
			if _, err := time.Parse(time.DateOnly, date); ok && err == nil {
				dates = append(dates, date)
			}
		}
	}
	days, _ := projects.DayKeys(dirs.Days(), dates)
	for _, k := range days {
		keys[k] = true
	}
	seen := maps.Clone(keys) // what this computer recorded itself
	if n, err := uploader.ReadNames(dirs.Data); err == nil {
		for _, m := range []map[string]string{n.Confirmed, n.Codes, n.Sent} {
			for k := range m {
				keys[k] = true
			}
		}
		for _, k := range slices.Concat(n.Hides, n.Deleting) {
			keys[k] = true
		}
		for k, h := range n.Hidden {
			switch h.Reason {
			case "", uploader.NoteDropped:
				keys[k] = true
			case uploader.NoteSignedIn, uploader.NoteUnreadable:
				if seen[k] {
					keys[k] = true
				}
			}
		}
	}
	return keys
}

// forgetNames takes the names the teen gave the projects of mine, the keys
// this computer knows of, out of config.toml (config.ForgetNames), and
// with no key it knows of, only counts the tables of other projects there,
// so that a config.toml it can't read or change fails nothing.
func forgetNames(dirs home.Dirs, mine keySet) (config.Forgotten, error) {
	if len(mine) > 0 {
		return config.ForgetNames(dirs.ConfigFile(), mine.has)
	}
	var f config.Forgotten
	_, f.Left, _ = config.ProjectTables(dirs.ConfigFile(), mine.has)
	return f, nil
}

// namesLeftAlone prints namesLeft's line for err, from config.ForgetNames,
// when the names had to stay.
func namesLeftAlone(stdout io.Writer, err error) {
	if err != nil {
		fmt.Fprintf(stdout, "Left alone: %s.\n", install.Printable(namesLeft(err)))
	}
}

// namesLeft says that names of the projects this computer knows of had to
// stay in config.toml, and what to delete by hand, as err, from
// config.ForgetNames, says once. For a config.toml that doesn't parse it
// gives the line, never the decoder's message, which can quote the file.
func namesLeft(err error) string {
	why := err.Error()
	var u *config.UnparsedError
	if errors.As(err, &u) {
		at := ""
		if u.Line > 0 {
			at = fmt.Sprintf(" at line %d", u.Line)
		}
		why = fmt.Sprintf("config: config.toml doesn't parse%s, so pairtime didn't change it; %s", at, u.Hand)
	}
	return "the names you gave some of the projects this computer knows of (" + why + ")"
}

// leftAlone says how many tables under projects config.toml keeps of
// projects this computer doesn't know of, as another computer sharing the
// file writes or an older version of pairtime left behind, and where the
// file is, but never what they hold.
func leftAlone(stdout io.Writer, dirs home.Dirs, n int) {
	switch {
	case n == 1:
		fmt.Fprintf(stdout, "Left alone: 1 table under projects in %s, for a project this computer doesn't know of, as another computer sharing the file writes or an older version of pairtime left behind. If no other computer shares the file, you can delete it by hand.\n", install.Printable(dirs.ConfigFile()))
	case n > 1:
		fmt.Fprintf(stdout, "Left alone: %d tables under projects in %s, for projects this computer doesn't know of, as another computer sharing the file writes or an older version of pairtime left behind. If no other computer shares the file, you can delete them by hand.\n", n, install.Printable(dirs.ConfigFile()))
	}
}
