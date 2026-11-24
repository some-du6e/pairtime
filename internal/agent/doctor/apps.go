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
	// ServiceHome says T3 Code's background service sets CLAUDE_CONFIG_DIR
	// in its own environment, its systemd unit or launchd plist, to a Claude
	// home whose settings hold none of pairtime's hooks: the Claude Code its
	// server runs for an instance with no home of its own loads none of
	// them either (DESIGN 5.1).
	ServiceHome bool
	// Unread says T3 Code's settings didn't read, so doctor can't tell
	// whether one of its Claude instances has a home of its own.
	Unread bool
	// NoSQLite3 says doctor finds no sqlite3 tool on the PATH of the
	// terminal it runs in. The line uses it only while the agent can't
	// say, and hedges it, since the agent's PATH can differ.
	NoSQLite3 bool
}

// What the agent's reader of T3 Code's store says when it doesn't read
// (T3Code.Off, which is internal/adapters/t3code's Status.Off).
const (
	t3NoStore    = "no_store"
	t3NotRunning = "not_running"
	t3NoSQLite3  = "no_sqlite3"
	t3NoJSON     = "no_json"
	t3Schema     = "schema"
)

// t3Quiet is how long T3 Code can be on a computer with no session it
// started reaching pairtime before the line says so (DESIGN 5.1).
const t3Quiet = 24 * time.Hour

// t3Context is what the line says pairtime notes while T3 Code runs, in
// the words DESIGN 3.3's presence row and the privacy notice use
// (D-relayed-context): while T3 alone keeps the project context live, a
// sample a minute and no os.focus. The Claude Code T3 keeps running in a
// thread between its runs keeps no context live, since the process scan
// leaves out a harness process whose parent_class is agent_app, but its
// hooks do, and so does focus on T3's own window.
const t3Context = "While T3 Code runs and you aren't working in a project, pairtime notes once a minute whether you're at the computer, and nothing about which app is in front. " +
	"The Claude Code that T3 Code keeps open between your messages isn't work in a project, but a reply it's writing is, and so is T3 Code's own window in front, " +
	"until 10 minutes after the reply ends or you switch away"

// t3Checks is DESIGN 5.9's line for T3 Code: whether the agent reads its
// store, with what it read today, or why it doesn't, and while T3 runs what
// pairtime notes then (t3Context); whether a Claude instance of T3's, or
// T3's background service, runs Claude Code with a home of its own,
// whether messages read today paired with no prompt, or whether no session
// T3 started has reached pairtime in a day while T3 runs, any of which
// means T3's sessions skip pairtime's hooks (DESIGN 5.1), unless the
// messages that paired with nothing were all read while no server the app
// table names ran, which means pairtime doesn't recognize T3's server; and,
// whatever else it says, how to install sqlite3 when the agent finds none,
// or, while the agent can't say, when doctor finds none on its own PATH.
// A computer where neither the agent nor doctor finds T3 Code gets no
// line. The line names no path, command line or argv: the reader's words
// name none, and doctor's own facts are counts.
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
		if t.Running {
			c.Text += ". " + t3Context
		}
	case t.Off == t3NotRunning:
		c.Result, c.Text = OK, "T3 Code isn't running, and pairtime reads its store only while it runs"
		if t.Witnessed > 0 || t.Paired > 0 || t.Aged > 0 || t.Ahead > 0 {
			c.Text += "; " + t3Today(t)
		}
	case t.Off == t3NoSQLite3:
		c.Result = Warn
		c.Text = "T3 Code is on this computer, but the agent finds no sqlite3 tool, so pairtime reads none of the messages you send through T3 Code, and they count for nothing. " +
			sqliteInstall(f.GOOS, f.Distro)
	case t.Off == t3NoJSON:
		c.Result = Warn
		c.Text = "pairtime doesn't read T3 Code's store, because the agent's sqlite3 has no JSON functions, so the messages you send through T3 Code count for nothing until it does. " +
			jsonInstall(f.GOOS)
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
	homes := f.T3.OwnHomes > 0 || f.T3.ServiceHome
	if f.T3.OwnHomes > 0 {
		c.Result = worse(c.Result, Warn)
		c.Text += "; " + ownHomes(f.T3.OwnHomes)
	}
	if f.T3.ServiceHome {
		c.Result = worse(c.Result, Warn)
		c.Text += "; T3 Code's background service sets CLAUDE_CONFIG_DIR, in its systemd unit or launchd plist, to a Claude home whose settings hold none of pairtime's hooks, " +
			"so the Claude Code it runs loads none of them, and the messages you send through T3 Code count for nothing. " +
			"Take CLAUDE_CONFIG_DIR out of the service's environment, or run pairtime install again with CLAUDE_CONFIG_DIR set to that home"
	}
	if !homes && f.T3.Unread {
		c.Text += "; doctor couldn't read T3 Code's settings, so it can't tell whether T3 Code gives Claude Code a home of its own, where pairtime's hooks don't run"
	}
	// Messages that paired with nothing, none of them read while the
	// agent's process scan found T3's server, come from a T3 whose server
	// the app table doesn't name, as one run from its source under node: its
	// sessions don't classify as agent_app, so the agent never asks about
	// their prompts, whatever the hooks do (DESIGN 3.3). The line names no
	// command line or path for it, since the scan reads none and doctor
	// --report can be shared.
	if t != nil && agentFinds {
		quiet := t.SessionAt == 0 || f.Now.Sub(time.UnixMilli(t.SessionAt)) > t3Quiet
		unpaired := t.Witnessed > 0 && t.Paired == 0
		switch {
		case unpaired && t.WithServer == 0:
			c.Result = worse(c.Result, Warn)
			c.Text += "; but none of the messages pairtime read from T3 Code today paired with a prompt Claude Code got, so they count for nothing. " +
				"pairtime doesn't recognize the T3 Code server that runs them, as happens with one run from T3 Code's source under node, " +
				"so it never asks about the prompts of the sessions that server starts. The t3 command T3 Code installs and its desktop app run a server pairtime recognizes"
		case homes:
			// A home of its own explains sessions that skip pairtime's
			// hooks, so the line doesn't say both.
		case unpaired:
			// Whatever session_at says: a claude typed into T3 Code's own
			// terminal is a session of T3's too, and would hide a day of
			// messages that never paired.
			c.Result = worse(c.Result, Warn)
			c.Text += "; but none of the messages pairtime read from T3 Code today paired with a prompt Claude Code got, so they count for nothing. " +
				"Most often the Claude Code that T3 Code runs for them isn't running pairtime's hooks, as with a CLAUDE_CONFIG_DIR that T3 Code's server got from its environment"
		case quiet && t.Running:
			c.Result = worse(c.Result, Warn)
			c.Text += "; no session T3 Code started has reached pairtime in the last day. If you used T3 Code since, the Claude Code it runs isn't running pairtime's hooks, " +
				"as with a CLAUDE_CONFIG_DIR that T3 Code's server got from its environment, and the messages you sent through it count for nothing"
		}
	}
	// Last, so the install command ends the line: a missing sqlite3 costs
	// T3 Code's messages their credit the moment T3 runs, whatever the
	// reader does now.
	switch {
	case t != nil && t.NoSQLite3 && t.Off != t3NoSQLite3:
		c.Result = worse(c.Result, Warn)
		c.Text += "; but the agent finds no sqlite3 tool, so pairtime can't read the messages you send through T3 Code, and they count for nothing until it can. " +
			sqliteInstall(f.GOOS, f.Distro)
	case t == nil && f.T3.NoSQLite3:
		// The agent can't say, so doctor's own lookup stands in, hedged.
		c.Result = worse(c.Result, Warn)
		c.Text += "; and doctor finds no sqlite3 tool on the PATH of the terminal it runs in. Without one, pairtime reads none of the messages you send through T3 Code, and they count for nothing. " +
			"The agent's PATH can differ from this terminal's, so this may not hold for the agent. " + sqliteInstall(f.GOOS, f.Distro)
	}
	return []Check{c}
}

// t3Today says what the reader did today: the messages a person sent
// through T3 Code that it recorded, how many of its records paired with a
// prompt Claude Code got, the messages its age test turned away, and those
// it turned away as stamped more than 2 s after it read them (SM 2.2).
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
	switch {
	case t.Ahead == 1:
		s += "; it turned away 1 message stamped more than 2 seconds after it read it, as happens when the computer's clock is set back"
	case t.Ahead > 1:
		s += "; it turned away " + strconv.Itoa(t.Ahead) + " messages stamped more than 2 seconds after it read them, as happens when the computer's clock is set back"
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

// jsonInstall says how to get a sqlite3 with JSON functions on goos, for
// a sqlite3 the reader's probe found without them (DESIGN 5.1): every one
// from 3.38 on has them built in, and an older one has them only when it
// was built with them. The agent looks for sqlite3 on its PATH, which on
// Linux and macOS a new login sets afresh, and on Windows winget changes a
// PATH only processes started after it see.
func jsonInstall(goos string) string {
	if goos == "windows" {
		return "Install a newer one with winget install SQLite.SQLite, then log out and back in, which starts the agent again so that it finds it"
	}
	return "Install a sqlite3 of version 3.38 or later, which has them built in, first on the PATH your login gives the agent, then log out and back in"
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
