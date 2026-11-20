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
	// reach for the window to host it (SM 3.2 rule 1).
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
	switch strings.ToLower(p.Name) {
	case "conhost", "openconsole":
		return true
	}
	return false
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
// children, so a session under one of them is under the terminal too.
//
// When Windows names a console helper itself, the window is that helper's
// console: it belongs to the process the helper serves, its parent, which
// is the console's client for a classic console and the terminal for a
// pseudo console. Such a window is a terminal whatever its owner is.
func windowOf(t *platform.Table, pid int) (window, bool) {
	p, ok := t.Get(pid)
	if !ok {
		return window{}, false
	}
	goos := t.OS()
	w := window{proc: p, hosts: map[platform.ID]bool{p.ID(): true}}
	if consoleHelper(goos, p) {
		w.class = event.AppTerminal
		if owner, ok := t.Parent(p); ok {
			w.proc = owner
			w.hosts[owner.ID()] = true
		}
	} else {
		w.class = event.AppOther
		if e, ok := platform.Identify(goos, p); ok && e.AppClass != "" {
			w.class = e.AppClass
		}
	}
	w.name = appName(w.proc)
	return w, true
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
// (SM 3.2): a terminal or an agent desktop app goes to its sessions'
// project when they all have the same one, which is always so for one
// session. Several sessions in different projects, or one whose project
// isn't known, leave it empty, and the scorer picks among the sessions by
// SM 3.2 rule 1's look-ahead. An editor's project comes from its own
// heartbeats instead (SM 3.2 rule 2): its process runs every window of
// the editor, and one of them may be on a project none of its sessions
// is in. Other apps have no project (rules 4 and 5).
func focusProject(class event.AppClass, hosted []string, projectOf func(session string) string) string {
	if projectOf == nil || len(hosted) == 0 {
		return ""
	}
	switch class {
	case event.AppTerminal, event.AppAgentApp:
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
