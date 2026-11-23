// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

// The line for T3 Code, the agent app whose message store the agent reads
// (DESIGN 5.1, 5.9).

import (
	"strconv"
	"strings"
	"time"
)

// T3Facts is what doctor found of T3 Code on this computer itself, where T3
// keeps its store and its settings (DESIGN 5.1). It names no path.
type T3Facts struct {
	// Found says a T3 Code store is where T3 keeps it.
	Found bool
	// OwnHomes counts the Claude instances T3 Code runs Claude Code in with
	// a Claude home of their own, CLAUDE_CONFIG_DIR, whose settings hold
	// none of pairtime's hooks, so their sessions never reach pairtime.
	OwnHomes int
	// Unread says T3 Code's settings didn't read, so doctor can't tell
	// whether one of its Claude instances has a home of its own.
	Unread bool
}

// What the agent's reader of T3 Code's store says when it doesn't read
// (T3Code.Off, which is internal/adapters/t3code's Status.Off).
const (
	t3NoStore    = "no_store"
	t3NotRunning = "not_running"
	t3NoSQLite3  = "no_sqlite3"
	t3Schema     = "schema"
)

// t3Quiet is how long T3 Code can be on a computer with no session it
// started reaching pairtime before the line says so (DESIGN 5.1).
const t3Quiet = 24 * time.Hour

// t3Checks is DESIGN 5.9's line for T3 Code: whether the agent reads its
// store, with what it read today, or why it doesn't; and whether a Claude
// instance of T3's runs Claude Code with a home of its own, or no session
// T3 started has reached pairtime in a day, either of which means T3's
// sessions skip pairtime's hooks (DESIGN 5.1). A computer where neither
// the agent nor doctor finds T3 Code gets no line. The line names no path:
// the reader's words name none, and doctor's own facts are counts.
func t3Checks(f Facts) []Check {
	var t *T3Code
	if f.Reach == Reached && f.Agent != nil {
		t = f.Agent.Apps.T3Code
	}
	agentFinds := t != nil && t.Off != t3NoStore
	if !agentFinds && !f.T3.Found {
		return nil
	}
	c := Check{Name: "app.t3_code"}
	switch {
	case f.Reach != Reached || f.Agent == nil:
		c.Result, c.Text = Skip, "T3 Code is on this computer; "+notAsked(f)
	case t == nil:
		c.Result, c.Text = Skip, "T3 Code is on this computer, but the agent hasn't read its store yet. It starts once its ledger is open, and a problem line says what keeps it"
	case !agentFinds:
		c.Result, c.Text = Warn, "doctor finds T3 Code's store from this terminal, but the agent finds none where it looks, so pairtime reads none of the messages you send through T3 Code. "+
			"If you set T3CODE_HOME in this terminal, set it where your login sets the environment too, then log out and back in"
	case t.Reading:
		c.Result, c.Text = OK, "pairtime reads T3 Code's store; "+t3Today(t)
		if t.Newer {
			c.Text += ". T3 Code's schema is newer than the one this pairtime was tested with, and pairtime reads it anyway"
		}
	case t.Off == t3NotRunning:
		c.Result, c.Text = OK, "T3 Code isn't running, and pairtime reads its store only while it runs"
		if t.Witnessed > 0 || t.Paired > 0 || t.Aged > 0 {
			c.Text += "; " + t3Today(t)
		}
	case t.Off == t3NoSQLite3:
		c.Result = Warn
		c.Text = "T3 Code is on this computer, but the agent finds no sqlite3 tool, so pairtime reads none of the messages you send through T3 Code, and they count for nothing. " +
			sqliteInstall(f.GOOS, f.Distro)
	case t.Off == t3Schema:
		c.Result = Warn
		why := "its schema isn't one pairtime reads"
		if strings.TrimSpace(t.Problem) != "" {
			why = reason(t.Problem)
		}
		c.Text = "pairtime doesn't read T3 Code's store, because " + why + ". The messages you send through T3 Code count for nothing until it can. " + reportIt
	default:
		c.Result = Warn
		c.Text = "the agent's last read of T3 Code's store failed: " + reason(t.Problem) + ". It tries again every 10 seconds"
	}
	if f.T3.OwnHomes > 0 {
		c.Result = worse(c.Result, Warn)
		c.Text += "; " + ownHomes(f.T3.OwnHomes)
	} else if f.T3.Unread {
		c.Text += "; doctor couldn't read T3 Code's settings, so it can't tell whether T3 Code gives Claude Code a home of its own, where pairtime's hooks don't run"
	}
	if t != nil && agentFinds && f.T3.OwnHomes == 0 {
		quiet := t.SessionAt == 0 || f.Now.Sub(time.UnixMilli(t.SessionAt)) > t3Quiet
		switch {
		case !quiet:
		case t.Witnessed > 0 && t.Paired == 0:
			c.Result = worse(c.Result, Warn)
			c.Text += "; but no session T3 Code started has reached pairtime in the last day, so the messages you sent through T3 Code today count for nothing. " +
				"The Claude Code that T3 Code runs isn't running pairtime's hooks"
		case t.Off != t3NotRunning:
			c.Result = worse(c.Result, Warn)
			c.Text += "; no session T3 Code started has reached pairtime in the last day. If you used T3 Code since, the Claude Code it runs isn't running pairtime's hooks, " +
				"as with a CLAUDE_CONFIG_DIR that T3 Code's server got from its environment, and the messages you sent through it count for nothing"
		}
	}
	return []Check{c}
}

// t3Today says what the reader did today: the messages a person sent
// through T3 Code that it recorded, how many of its records paired with a
// prompt Claude Code got, and the messages its age test turned away.
func t3Today(t *T3Code) string {
	s := "today it read no messages you sent through T3 Code"
	if t.Witnessed > 0 {
		s = "today it read " + count(uint64(t.Witnessed), "message", "messages") + " you sent through T3 Code"
	}
	if t.Witnessed > 0 || t.Paired > 0 {
		s += ", and paired " + count(uint64(max(t.Paired, 0)), "record", "records") + " with prompts Claude Code got"
	}
	switch {
	case t.Aged == 1:
		s += "; it turned away 1 message it first read too long after it was sent, as happens while pairtime's agent isn't running"
	case t.Aged > 1:
		s += "; it turned away " + strconv.Itoa(t.Aged) + " messages it first read too long after they were sent, as happens while pairtime's agent isn't running"
	}
	return s
}

// ownHomes says that n of T3 Code's Claude instances run Claude Code with
// a Claude home of their own, and what to do about it.
func ownHomes(n int) string {
	if n == 1 {
		return "one of T3 Code's Claude instances runs Claude Code with a Claude home of its own, set by CLAUDE_CONFIG_DIR, which loads none of pairtime's hooks, " +
			"so the messages you send to it count for nothing. Clear that instance's CLAUDE_CONFIG_DIR path in T3 Code's provider settings, " +
			"or run pairtime install again with CLAUDE_CONFIG_DIR set to it"
	}
	return strconv.Itoa(n) + " of T3 Code's Claude instances run Claude Code with Claude homes of their own, set by CLAUDE_CONFIG_DIR, which load none of pairtime's hooks, " +
		"so the messages you send to them count for nothing. Clear their CLAUDE_CONFIG_DIR paths in T3 Code's provider settings, " +
		"or run pairtime install again with CLAUDE_CONFIG_DIR set to each"
}

// sqliteInstall says how to get the sqlite3 tool on goos, for a Linux
// distribution whose os-release words are distro (DESIGN 5.1): Debian and
// Ubuntu don't install it by default, Fedora has it as sqlite, Windows has
// none, and macOS ships it. The agent looks for sqlite3 again at each
// read, so on Linux it finds a new one at once; on Windows winget puts it
// on a PATH that only processes started after it see, so the agent has to
// start again.
func sqliteInstall(goos string, distro []string) string {
	switch goos {
	case "windows":
		return "Install it with winget install SQLite.SQLite, then log out and back in, which starts the agent again so that it finds it"
	case "darwin":
		return "macOS ships it, so something keeps it from the agent. If logging out and back in doesn't clear this line: " + reportIt
	case "linux":
		for _, d := range distro {
			switch d {
			case "debian", "ubuntu":
				return "Install it with: sudo apt install sqlite3"
			case "fedora":
				return "Install it with: sudo dnf install sqlite"
			case "arch":
				return "Install it with: sudo pacman -S sqlite"
			case "suse", "opensuse":
				return "Install it with: sudo zypper install sqlite3"
			}
		}
		return "Install your distribution's sqlite3 package, which Debian and Ubuntu call sqlite3 and Fedora sqlite"
	}
	return "Install the sqlite3 command-line tool"
}

// DistroIDs returns the words of os-release's ID and then its ID_LIKE,
// lowercase, such as [linuxmint ubuntu debian]: the Linux distribution and
// the ones it derives from, which say how to install a package there. b is
// the file's content, /etc/os-release or /usr/lib/os-release.
func DistroIDs(b []byte) []string {
	var id, like string
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		} else {
			v = strings.Trim(v, `'`)
		}
		switch k {
		case "ID":
			id = v
		case "ID_LIKE":
			like = v
		}
	}
	return strings.Fields(strings.ToLower(id + " " + like))
}

// worse returns the worse of two results: fail, then warn, then ok and
// skip alike.
func worse(a, b Result) Result {
	rank := func(r Result) int {
		switch r {
		case Fail:
			return 2
		case Warn:
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}
