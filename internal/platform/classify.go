// SPDX-License-Identifier: AGPL-3.0-or-later

// The parent-chain classifier: SM 1.2's parent_class and SM 2.2 rule 4,
// from a harness process's ancestors and the app-class table.

package platform

import (
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// LauncherWindow is how long before a NodeLauncher harness a Node-style
// runtime that is its direct parent may have started and still count as
// the harness's own launcher, such as the node wrapper npm installs in
// front of codex.
const LauncherWindow = 3 * time.Second

// Lineage is what a harness process's ancestors say about how its session
// started.
type Lineage struct {
	ParentClass event.ParentClass
	// Entrypoint is EntrypointIDE or EntrypointDesktop when an IDE or an
	// agent desktop app started the session, else EntrypointUnknown. The
	// chain can't tell `claude` from `claude -p`, because pairtime doesn't
	// read command lines, and no hook payload says it either (HS 3.1). For
	// Claude Code only OTel's app.entrypoint does, an attribute sent only
	// when OTEL_METRICS_INCLUDE_ENTRYPOINT is set (HS 3.3, 3.5).
	Entrypoint event.Entrypoint
	HasTTY     bool
	// By is the chain index of the ancestor that decided ParentClass, or
	// -1 when the chain ran out first.
	By int
	// ScriptShell is set when a shell running a script, rather than a plain
	// interactive shell, sat between the harness and By.
	ScriptShell bool
}

// Identify returns the table entry that describes p on goos: its Alias's
// when the table knows the Alias, else its Name's. Alias goes first so that
// a terminal written in Python (terminator) is a terminal and a Node app
// that names itself, such as a harness, is that app.
func Identify(goos string, p Proc) (Entry, bool) {
	if p.Alias != "" {
		if e, ok := Lookup(goos, p.Alias); ok {
			return e, true
		}
	}
	return Lookup(goos, p.Name)
}

// HarnessOf reports the harness p runs, if p is a harness process.
func HarnessOf(goos string, p Proc) (event.Harness, bool) {
	e, ok := Identify(goos, p)
	if !ok || e.Role != RoleHarness {
		return "", false
	}
	return e.Harness, true
}

// HookHost returns the chain index of the process a hook command runs
// under: the nearest ancestor of chain[0] that isn't a shell or a wrapper,
// because harnesses run shell-form hooks through sh -c, Git Bash or
// PowerShell (HS 3.1). It returns -1 when there is none.
func HookHost(goos string, chain []Proc) int {
	for i := 1; i < len(chain); i++ {
		e, _ := Identify(goos, chain[i])
		if e.Role != RoleShell && e.Role != RoleWrapper {
			return i
		}
	}
	return -1
}

// LauncherOf reports whether parent is child's own launcher rather than
// what started child's session: a Node-style runtime directly above the
// harness that either runs that harness itself, as npm's wrapper does under
// a title naming it (Linux's comm), or, for a NodeLauncher harness, started
// at most LauncherWindow before it, as the plain node of the same wrapper
// does on macOS and Windows. The classifier skips a launcher, and the scan
// reports the child rather than the launcher, because the child is what a
// hook claims. A native harness above the same native harness is no
// launcher: it is another session that started this one (agent).
func LauncherOf(goos string, parent, child Proc) bool {
	if rt, _ := Lookup(goos, parent.Name); !rt.JSRuntime {
		return false
	}
	h, ok := HarnessOf(goos, child)
	if !ok {
		return false
	}
	if ph, ok := HarnessOf(goos, parent); ok {
		return ph == h
	}
	e, _ := Identify(goos, child)
	return e.NodeLauncher && launched(parent, child)
}

// Classify classifies the session of the harness process chain[0] from its
// ancestors chain[1:], as Table.Chain returns them, read on goos.
//
// parent_class is the class of the nearest ancestor that isn't a plain
// interactive shell (SM 1.2). Walking up from the harness:
//
//   - The harness's own launcher, its direct parent when LauncherOf says
//     so, is skipped.
//   - Shells are skipped. A shell with POSIX job control that is running a
//     script marks the session script_runtime unless an ancestor further up
//     is a script runtime, a scheduler or a harness, which say more. So
//     `python farm.py` spawning bash spawning claude is script_runtime, and
//     so is `bash farm.sh` typed into a terminal (SM 2.2 rule 4, SM 6.6).
//     Such a shell shows in one of two ways. Its child on the chain shares
//     its process group, since only an interactive shell gives each command
//     a group of its own. Or the harness sits on a terminal but outside
//     the terminal's foreground group, where no person types into it, and
//     the shell was started by another shell, so it isn't the shell the
//     terminal gave the person: a script's command can leave the script's
//     group, as `timeout 900 claude -p` does, because GNU timeout makes a
//     group of its own unless given --foreground. Windows has no process
//     groups, so there every shell counts as interactive.
//   - Wrappers (sudo, nohup, timeout; login on macOS) are skipped.
//   - A Node-style runtime whose parent is an IDE belongs to the IDE: SM 1.2
//     counts only "a non-IDE node" as script_runtime.
//   - Any other ancestor decides: a terminal (or login on a Linux text
//     console) is terminal_app, an editor ide, tmux multiplexer, sshd
//     remote, an interpreter script_runtime, a scheduler scheduler, another
//     harness agent, and an agent desktop app or anything else unknown.
//
// HasTTY is the harness's controlling terminal where the OS reports one.
// Windows doesn't, so there it is set when a shell, a terminal, a
// multiplexer or sshd is anywhere among the ancestors, or when explorer
// started the harness directly and so gave it a console window of its own.
func Classify(goos string, chain []Proc) Lineage {
	l := Lineage{ParentClass: event.ParentUnknown, Entrypoint: event.EntrypointUnknown, By: -1}
	if len(chain) == 0 {
		return l
	}
	h := chain[0]
	role := RoleNone
walk:
	for i := 1; i < len(chain); i++ {
		a := chain[i]
		if i == 1 && LauncherOf(goos, a, h) {
			continue
		}
		e, _ := Identify(goos, a)
		switch e.Role {
		case RoleShell:
			if e.JobControl && (runsScript(chain[i-1], a) || offForeground(h) && shellAbove(goos, chain, i)) {
				l.ScriptShell = true
			}
			continue
		case RoleWrapper:
			continue
		case RoleRuntime:
			if e.JSRuntime && i+1 < len(chain) {
				if up, _ := Identify(goos, chain[i+1]); up.Role == RoleIDE {
					l.By, role = i+1, RoleIDE
					break walk
				}
			}
		}
		l.By, role = i, e.Role
		break
	}

	switch role {
	case RoleTerminal:
		l.ParentClass = event.ParentTerminalApp
	case RoleIDE:
		l.ParentClass, l.Entrypoint = event.ParentIDE, event.EntrypointIDE
	case RoleDesktop:
		l.Entrypoint = event.EntrypointDesktop
	case RoleMultiplexer:
		l.ParentClass = event.ParentMultiplexer
	case RoleRemote:
		l.ParentClass = event.ParentRemote
	case RoleRuntime:
		l.ParentClass = event.ParentScriptRuntime
	case RoleScheduler:
		l.ParentClass = event.ParentScheduler
	case RoleHarness:
		l.ParentClass = event.ParentAgent
	}
	if l.ScriptShell {
		switch l.ParentClass {
		case event.ParentScriptRuntime, event.ParentScheduler, event.ParentAgent:
		default:
			// A script started the session, not the IDE or the desktop app
			// further up.
			l.ParentClass, l.Entrypoint = event.ParentScriptRuntime, event.EntrypointUnknown
		}
	}

	switch h.TTY {
	case TTYAttached:
		l.HasTTY = true
	case TTYNone:
		l.HasTTY = false
	default:
		l.HasTTY = consoleAbove(goos, chain)
	}
	return l
}

// consoleAbove reports whether a shell, a terminal, a multiplexer or sshd
// is among chain[0]'s ancestors, or whether its direct parent is explorer.
// Where the OS doesn't say whether a process has a terminal (Windows), that
// is the sign it has a console: a console program inherits the console of
// whatever started it, and gets a new one when explorer starts it. Only the
// direct parent counts for explorer, because an IDE's chain ends at
// explorer too, and a harness the IDE starts has no console.
func consoleAbove(goos string, chain []Proc) bool {
	for i, a := range chain[1:] {
		e, _ := Identify(goos, a)
		if i == 0 && e.GivesConsole {
			return true
		}
		switch e.Role {
		case RoleShell, RoleTerminal, RoleMultiplexer, RoleRemote:
			return true
		}
	}
	return false
}

// runsScript reports whether shell, a POSIX shell with job control, is
// running a script rather than taking commands from a person. An
// interactive shell starts each command in a process group of its own; a
// shell running a script, or a -c string, doesn't, so its children stay
// in its group.
func runsScript(child, shell Proc) bool {
	return shell.PGID > 0 && child.PGID == shell.PGID
}

// offForeground reports whether the harness has a controlling terminal but
// isn't in the terminal's foreground process group, the one that reads
// what is typed there.
func offForeground(h Proc) bool {
	return h.TTY == TTYAttached && h.TPGID > 0 && h.PGID > 0 && h.PGID != h.TPGID
}

// shellAbove reports whether the nearest ancestor of chain[i] that isn't a
// wrapper is a shell, so that chain[i] is a shell another shell started
// rather than the one a terminal, a login, sshd or a multiplexer started
// for a person.
func shellAbove(goos string, chain []Proc, i int) bool {
	for _, a := range chain[i+1:] {
		e, _ := Identify(goos, a)
		if e.Role == RoleWrapper {
			continue
		}
		return e.Role == RoleShell
	}
	return false
}

// launched reports whether runtime started at most LauncherWindow before
// harness, as a launcher that starts the harness at once does.
func launched(runtime, harness Proc) bool {
	if runtime.Start.IsZero() || harness.Start.IsZero() {
		return false
	}
	d := harness.Start.Sub(runtime.Start)
	return d >= 0 && d <= LauncherWindow
}
