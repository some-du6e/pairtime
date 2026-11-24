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

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
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

pairtime itself and your settings in config.toml stay. So does install's
record of what it changed, with its copies of the files it edited, so that
pairtime uninstall can put them back. pairtime starts again from nothing,
under a new device key, and signed out: pairtime login links this computer
again. What the pairtime server already has stays there until you remove
this computer on your dashboard.

  --yes   go ahead without asking

Exit status:
  0  deleted, or there was nothing to delete
  1  something went wrong, or some of the device key had to stay where
     pairtime can't reach it; the message says what, and what stays
  2  usage error
  3  you said no, so nothing changed
`

// wipeKeeps are the entries of the data directory that "delete everything"
// leaves, because they are pairtime itself and its settings, not what it
// recorded: config.toml, which is in the data directory on macOS and under
// PAIRTIME_HOME, and config.lock, which every edit of it locks
// (internal/agent/config); install's record of what it put in, which
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
	flags.Usage = func() { fmt.Fprint(flags.Output(), privacyDeleteUsage) }
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
	if len(recorded) == 0 && kerr != nil && !running {
		fmt.Fprintln(stdout, "pairtime has recorded nothing on this computer, so there is nothing to delete.")
		return 0
	}
	if !*yes {
		fmt.Fprintf(stdout, "This deletes pairtime's device key and everything pairtime recorded in %s: the text of your prompts it kept, your time, your daily totals and recaps, and this computer's sign-in. The hours pairtime recorded so far can't be submitted any more. Copies a backup or a synced folder already holds stay as they are, the key's file too when pairtime doctor says pairtime keeps its key in a file. pairtime and your settings stay, and it starts again from nothing.\n\n", install.Printable(dirs.Data))
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
	if err := clearData(dirs.Data, wipeKeeps, nil); err != nil {
		release()
		return fail(fmt.Errorf("pairtime's device key is deleted, but some of what it recorded isn't: %w; close any program that has one of pairtime's files open, then run pairtime privacy delete again", err))
	}
	// The sign-in went with the keystore and account.json, so config.toml
	// goes back to local-only, as pairtime logout leaves it.
	if cfg, _, err := config.Load(dirs.ConfigFile()); err == nil && cfg.Mode == config.Online {
		if err := config.SetLocal(dirs.ConfigFile()); err != nil {
			fmt.Fprintf(stdout, "Left alone: config.toml still says mode = \"online\" (%v). Nothing uploads without a sign-in, and pairtime login links this computer again.\n", err)
		}
	}
	if keyStays != nil {
		fmt.Fprintf(stdout, "Deleted everything pairtime recorded in %s. Your settings in config.toml stay.\nLeft alone: %s\n", install.Printable(dirs.Data), install.Printable(keyStays.Error()))
	} else {
		fmt.Fprintf(stdout, "Deleted pairtime's device key and everything pairtime recorded in %s. Your settings in config.toml stay.\n", install.Printable(dirs.Data))
	}
	release()
	code := 0
	if keyStays != nil {
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
