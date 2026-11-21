// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

// The checks of what the CLI finds on the computer itself: the hooks each
// harness's settings hold and a test call of them, the device key's store
// and the binary's signature.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/coach/text"
)

// Hooks is what doctor found of pairtime's hooks in one harness.
type Hooks struct {
	// Harness is the harness's name in `pairtime hook --harness`, such as
	// claude_code, and Title its name for people, such as Claude Code.
	Harness, Title string
	// Found is set when the harness is on this computer (install.Detect).
	Found bool
	// Settings is the file its hooks are in.
	Settings string
	// ReadError says why the settings couldn't be read.
	ReadError string
	// Events are the events whose hooks run pairtime, Missing the ones
	// pairtime installs that are gone, Stale the ones whose hook runs with
	// other arguments than install writes, and Exes the binaries they run.
	Events, Missing, Stale, Exes []string
	// Disabled is set when the settings turn every hook off.
	Disabled bool
	// Trips are the times of warm test calls of the hook Exes[0] runs, from
	// start to exit, each handed to a listener of doctor's own so nothing
	// of it reaches the agent or the ledger. TripError says why a call that
	// ran the binary failed, and RigError why doctor couldn't start its
	// listener, when no call ran.
	Trips     []time.Duration
	TripError string
	RigError  string
}

// R8HookMS is R8's bound on a warm hook round trip, from start to exit,
// on an old laptop (DESIGN 3.12, 9.7).
const R8HookMS = 50

// Median is the middle of ds, the mean of the two middle ones for an even
// count, and 0 for none.
func Median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// hookChecks are DESIGN 5.9's hook round trip line for a harness, and the
// folder-trust hint for Claude Code once its hooks are in (WP-AG6). A
// harness that isn't on the computer and has none of pairtime's hooks gets
// no line.
func hookChecks(h Hooks) []Check {
	c := Check{Name: "hook." + h.Harness}
	settings := text.Printable(h.Settings)
	switch {
	case h.ReadError != "":
		c.Result, c.Text = Warn, fmt.Sprintf("%s's settings don't read, so doctor can't tell whether its hooks run pairtime: %s", h.Title, text.Printable(h.ReadError))
		return []Check{c}
	case len(h.Events) == 0 && !h.Found:
		return nil
	case len(h.Events) == 0:
		c.Result, c.Text = Warn, fmt.Sprintf("pairtime's hooks aren't in %s, so pairtime sees none of %s's sessions. Run pairtime install to add them", settings, h.Title)
		return []Check{c}
	case h.Disabled:
		c.Result, c.Text = Fail, fmt.Sprintf("%s has \"disableAllHooks\": true, so %s runs none of its hooks, pairtime's included. Take that line out to turn them back on", settings, h.Title)
	case h.RigError != "":
		c.Result, c.Text = Skip, "doctor couldn't start its test listener, so it didn't time the hook: "+text.Printable(h.RigError)
	case h.TripError != "":
		c.Result, c.Text = Fail, fmt.Sprintf("the hook %s runs, %s, failed a test call: %s. Run pairtime install to put a working one there", h.Title, text.Printable(firstOf(h.Exes)), text.Printable(h.TripError))
	default:
		took := Median(h.Trips)
		c.Result = OK
		c.Text = fmt.Sprintf("a test call took %s from start to exit, the median of %d", Millis(took), len(h.Trips))
		if took > R8HookMS*time.Millisecond {
			c.Result = Warn
			c.Text += fmt.Sprintf(", more than the %d ms pairtime aims for. pairtime doctor --report has the numbers to share", R8HookMS)
		}
	}
	if len(h.Missing) > 0 && c.Result != Fail {
		c.Result = Warn
		are := "are"
		if len(h.Missing) == 1 {
			are = "is"
		}
		c.Text += fmt.Sprintf("; %d of pairtime's hooks %s missing from %s (%s), so pairtime misses those events. Run pairtime install to put them back",
			len(h.Missing), are, settings, strings.Join(h.Missing, ", "))
	}
	if len(h.Stale) > 0 && c.Result != Fail {
		c.Result = Warn
		c.Text += fmt.Sprintf("; the hooks for %s don't run with the arguments install writes, so each of their calls counts as a parse failure. Run pairtime install to write them again",
			strings.Join(h.Stale, ", "))
	}
	if len(h.Exes) > 1 {
		if c.Result == OK {
			c.Result = Warn
		}
		c.Text += fmt.Sprintf("; the hooks run %d different pairtime binaries (%s), so run pairtime install to make them one", len(h.Exes), text.Printable(strings.Join(h.Exes, ", ")))
	}
	out := []Check{c}
	if h.Harness == "claude_code" {
		out = append(out, Check{Name: "trust." + h.Harness, Result: OK,
			Text: "Claude Code runs pairtime's hooks in a folder only once you accept its trust dialog there; claude -p and SDK runs don't ask, and --bare runs fire no hooks"})
	}
	return out
}

func firstOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// Keystore is where the device key is, from keystore.json alone
// (keys.Inspect), which asks no store for the key.
type Keystore struct {
	// Store names where K_dev is, such as "the macOS Keychain".
	Store string
	// Warning is keys.Status.Warning: what doctor says when K_dev is in the
	// key file, the fallback, and "" when it is in the OS's store.
	Warning string
	// Missing is set when the installation has no device key yet, and
	// Error says why keystore.json doesn't read.
	Missing bool
	Error   string
}

// keystoreCheck is the keystore's mode, with the fallback warning
// (DESIGN 3.6; CP 5.2).
func keystoreCheck(k Keystore) Check {
	c := Check{Name: "keystore"}
	switch {
	case k.Error != "":
		c.Result, c.Text = Fail, "keystore.json, which says where the device key is, doesn't read: "+text.Printable(k.Error)
	case k.Missing:
		c.Result, c.Text = Warn, "this computer has no device key yet, so pairtime records nothing; the agent makes one when it starts"
	case k.Warning != "":
		c.Result, c.Text = Warn, text.Printable(k.Warning)
	default:
		c.Result, c.Text = OK, "the device key is in "+text.Printable(k.Store)
	}
	return c
}

// Signing is what doctor knows of this binary: whether a release build
// stamped it, and what the OS's own check says of its signature (DESIGN
// 5.9). WP-RL5 signs releases; until then they are unsigned.
type Signing struct {
	// Release is set for a binary a release built, which stamped Version
	// and Commit in it; a build from a checkout is a development build.
	Release bool
	Version string
	Commit  string
	// Verdict is what the OS's check found, and State says it in words,
	// such as "not code-signed" or "signed, and Windows trusts the
	// signature".
	Verdict Verdict
	State   string
}

// Verdict is what an OS's signature check found.
type Verdict int

const (
	// Unchecked means doctor couldn't check, and State says why.
	Unchecked Verdict = iota
	// Unsigned is a binary with no signature.
	Unsigned
	// AdHoc is a macOS binary signed with no identity, as the Go linker
	// signs every Apple silicon binary, which Gatekeeper doesn't take as a
	// developer's signature.
	AdHoc
	// Signed is a signature doctor found and didn't verify.
	Signed
	// Trusted is a signature the OS checked and trusts.
	Trusted
	// Untrusted is a signature the OS checked and doesn't trust, as for a
	// certificate it doesn't know or that has expired.
	Untrusted
	// Broken is a signature the binary doesn't match: it changed after it
	// was signed.
	Broken
	// NoSigning is an OS with no code signing of programs, as Linux.
	NoSigning
)

// signingCheck is DESIGN 5.9's signing state of the binary. An unsigned
// release is ok: every release is until WP-RL5, and the line says what
// that means.
func signingCheck(s Signing) Check {
	c := Check{Name: "signing", Result: OK}
	what := "a development build, not a release"
	if s.Release {
		what = "release " + text.Printable(s.Version)
		if s.Commit != "" {
			what += " from commit " + text.Printable(shortCommit(s.Commit))
		}
	}
	c.Text = what + ", " + text.Printable(s.State)
	switch s.Verdict {
	case Unchecked:
		c.Result = Skip
	case Broken:
		c.Result = Fail
		c.Text += ", so it changed after it was signed. Download pairtime again and run pairtime install"
	case Untrusted:
		c.Result = Warn
		c.Text += ". Download pairtime again from its releases page and run pairtime install"
	case Unsigned, AdHoc:
		if s.Release {
			c.Text += ": pairtime's releases aren't code-signed yet, so the OS may warn you before it first runs"
		}
	}
	return c
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
