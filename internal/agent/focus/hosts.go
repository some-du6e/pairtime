// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// maxHosted is how many session keys one os.focus may list: pkg/event caps
// every list in an event at 1,024.
const maxHosted = 1024

// window is what the process table says about the window in front.
type window struct {
	// proc is the process the window belongs to. For a console window that
	// Windows names a console helper for, it is the process the helper
	// serves (see windowOf).
	proc platform.Proc
	// class is the window's app class, from the shared app-class table.
	class event.AppClass
	// name is the app's identity, which app_hash keys: its executable's
	// name as the table spells it, lowercase and without ".exe".
	name string
	// hosts are the processes a harness session's parent chain has to
	// reach for the window to host it (SM 3.2 rule 1). It is empty for a
	// window that hosts no sessions.
	hosts map[platform.ID]bool
}

// consoleHelper reports whether p is a process that serves a console
// window for the programs that run in it, rather than one of those
// programs: conhost.exe, Windows' console host, or OpenConsole.exe, the
// copy Windows Terminal ships and starts as a child of its own for each
// tab's console (ConPTY). appclass.json leaves both out, because
// GetWindowThreadProcessId gives a classic console window to its first
// client, such as cmd.exe or claude.exe, and not to conhost (CP 3.3).
// Windows names a helper only for a console window with no client left,
// or for the hidden window a pseudo console keeps.
func consoleHelper(goos string, p platform.Proc) bool {
	if goos != platform.Windows {
		return false
	}
	switch appName(p) {
	case "conhost", "openconsole":
		return true
	}
	return false
}

// helpers names, by OS and app, the helper processes an app runs harness
// sessions under that need not descend from the app's own process, each as
// a prefix of the helper's name. iTerm2 can run a session's job in an
// iTermServer process, whose name carries iTerm2's version, so that the
// session outlives iTerm2: when iTerm2 quits or crashes, launchd adopts the
// server, and a later iTerm2 can restore the session from it (DESIGN 3.3;
// decision E-6.3-two-tabs). appclass.json's darwin terminal entry lists
// the same names as itermserver*.
var helpers = map[string]map[string]string{
	platform.Darwin: {"iterm2": "itermserver"},
}

// windowOf describes the window whose process is pid, or reports false
// when pid isn't in the table.
//
// The window hosts the harness sessions whose parent chain reaches its
// process, the process itself included: a console window that Windows
// gives to claude.exe hosts that claude's own session. A terminal that
// runs several windows or tabs in one process, as Windows Terminal does,
// counts as one window, which hosts the sessions of every one of them,
// because the OS can't see which tab is in front (decision 1 of WP-OS2;
// SM 3.2 rule 1 picks among them by look-ahead, decision E-6.3-two-tabs).
// Its helpers, the OpenConsole.exe that serves each tab's console, are its
// children, so a session under one of them is under the terminal too. On
// macOS every window of an app is one window, since macOS reports app
// activations (MacFocus), and iTerm2's window also hosts the sessions
// under the user's iTermServer processes, which may not descend from
// iTerm2 (helpers).
//
// When Windows names a console helper itself, the window is that helper's
// console: it belongs to the process the helper serves, its parent, which
// is the console's client for a classic console and the terminal for a
// pseudo console. Such a window is a terminal whatever its owner is.
//
// Only terminals, editors and agent desktop apps host sessions (SM 3.2
// rules 1 to 3). A browser, a chat app or anything else hosts none, even
// when a harness runs under its process (rules 4 and 5). That matters most
// for explorer.exe, which owns the desktop and the taskbar and is an
// ancestor of nearly every program the person starts: focus on the desktop
// is focus on no session.
func windowOf(t *platform.Table, pid int) (window, bool) {
	p, ok := t.Get(pid)
	if !ok {
		return window{}, false
	}
	goos := t.OS()
	w := window{proc: p, class: event.AppOther}
	if consoleHelper(goos, p) {
		w.class = event.AppTerminal
		// explorer only starts programs, so a console it would own is the
		// helper's alone.
		if owner, ok := t.Parent(p); ok && !givesConsole(goos, owner) {
			w.proc = owner
		}
	} else if e, ok := platform.Identify(goos, p); ok && e.AppClass != "" {
		w.class = e.AppClass
	}
	w.name = appName(w.proc)
	if hosting(w.class) {
		w.hosts = map[platform.ID]bool{p.ID(): true, w.proc.ID(): true}
		addHelpers(t, &w)
	}
	return w, true
}

// addHelpers adds to w's hosts the helper processes its app runs sessions
// under (helpers), wherever they sit in the table, as long as they belong
// to the user the app runs as. Another user's iTermServer, left by fast
// user switching, serves that user's iTerm2, not the one in front.
func addHelpers(t *platform.Table, w *window) {
	prefix := helpers[t.OS()][w.name]
	if prefix == "" || w.proc.Owner == "" {
		return
	}
	for _, q := range t.Procs() {
		if q.Owner == w.proc.Owner && strings.HasPrefix(appName(q), prefix) {
			w.hosts[q.ID()] = true
		}
	}
}

// hosting reports whether a window of class c can host harness sessions:
// terminals, editors and agent desktop apps (SM 3.2 rules 1 to 3).
func hosting(c event.AppClass) bool {
	return c == event.AppTerminal || c == event.AppEditor || c == event.AppAgentApp
}

// givesConsole reports whether p is the Windows shell, explorer, which the
// app-class table marks gives_console: it starts programs, and its own
// windows, the desktop and the taskbar among them, host nothing.
func givesConsole(goos string, p platform.Proc) bool {
	e, ok := platform.Identify(goos, p)
	return ok && e.GivesConsole
}

// appName is the identity of the app a process runs: its executable's
// name, lowercase and without ".exe", as the app-class table keys it.
func appName(p platform.Proc) string {
	n := strings.ToLower(strings.TrimSpace(p.Name))
	n, _ = strings.CutSuffix(n, ".exe")
	return n
}

// hostedSessions returns the keys of the harness sessions the window
// hosts: each process in sessions (by ID, with its session key) that is
// still in the table and whose parent chain, the process itself first,
// reaches one of the window's hosts. The keys come sorted, without
// repeats, and at most maxHosted of them.
func hostedSessions(t *platform.Table, w window, sessions map[platform.ID]string) []string {
	if len(w.hosts) == 0 {
		return nil
	}
	var keys []string
	for id, key := range sessions {
		if key == "" {
			continue
		}
		p, ok := t.Get(id.PID)
		// A PID now held by a newer process isn't the session's.
		if !ok || p.ID() != id {
			continue
		}
		for _, a := range t.Chain(p.PID) {
			if w.hosts[a.ID()] {
				keys = append(keys, key)
				break
			}
		}
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	if len(keys) > maxHosted {
		keys = keys[:maxHosted]
	}
	return keys
}

// focusProject resolves the window's project from the sessions it hosts
// (SM 3.2): a terminal, an editor or an agent desktop app goes to its
// sessions' project when they all have the same one, which is always so
// for one session. Several sessions in different projects, or one whose
// project isn't known, leave it empty, and the scorer picks among the
// sessions by SM 3.2 rule 1's look-ahead, or for an editor by its
// heartbeats (rule 2). Other apps have no project (rules 4 and 5).
//
// One process runs every window of an editor, as one runs every window of
// Windows Terminal, so the window in front may show a project that none
// of its sessions is in. Decision 1 counts such an app as one window that
// hosts all of them, for editors as for terminals, and SM 6.2 credits a
// prompt typed in Cursor because Cursor hosts the session, before any
// heartbeat says which project it shows (testdata/timelines/README.md,
// "Focus").
func focusProject(class event.AppClass, hosted []string, projectOf func(session string) string) string {
	if projectOf == nil || len(hosted) == 0 {
		return ""
	}
	switch class {
	case event.AppTerminal, event.AppEditor, event.AppAgentApp:
	default:
		return ""
	}
	project := ""
	for _, k := range hosted {
		p := projectOf(k)
		if p == "" || project != "" && p != project {
			return ""
		}
		project = p
	}
	return project
}
