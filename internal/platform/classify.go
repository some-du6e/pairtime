// SPDX-License-Identifier: AGPL-3.0-or-later

// The parent-chain classifier: SM 1.2's parent_class and SM 2.2 rule 4,
// from a harness process's ancestors and the app-class table.

package platform

import (
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// LauncherWindow is how long before a harness a Node-style runtime that is
// its direct parent may have started and still count as the harness's own
// launcher, such as the node wrapper npm installs in front of codex.
const LauncherWindow = 3 * time.Second

// Lineage is what a harness process's ancestors say about how its session
// started.
type Lineage struct {
	ParentClass event.ParentClass
	// Entrypoint is EntrypointIDE or EntrypointDesktop when an IDE or an
	// agent desktop app started the session, else EntrypointUnknown. The
	// chain can't tell `claude` from `claude -p`, because pairtime doesn't
	// read command lines; hooks and OTel say that (SM 1.2).
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

// Classify classifies the session of the harness process chain[0] from its
// ancestors chain[1:], as Table.Chain returns them, read on goos.
//
// parent_class is the class of the nearest ancestor that isn't a plain
// interactive shell (SM 1.2). Walking up from the harness:
//
//   - Shells are skipped. A shell with POSIX job control that is running a
//     script, which shows as its child sharing its process group, marks the
//     session script_runtime unless an ancestor further up is a script
//     runtime, a scheduler or a harness, which say more. So `python farm.py`
//     spawning bash spawning claude is script_runtime, and so is
//     `bash farm.sh` typed into a terminal (SM 2.2 rule 4, SM 6.6). Windows
//     has no process groups, so there every shell counts as interactive.
//   - Wrappers (login, sudo, nohup) are skipped.
//   - The harness's own launcher is skipped: the same harness as its direct
//     parent, or a Node-style runtime that is its direct parent and started
//     at most LauncherWindow before it.
//   - A Node-style runtime whose parent is an IDE belongs to the IDE: SM 1.2
//     counts only "a non-IDE node" as script_runtime.
//   - Any other ancestor decides: a terminal is terminal_app, an editor ide,
//     tmux multiplexer, sshd remote, an interpreter script_runtime, a
//     scheduler scheduler, another harness agent, and an agent desktop app
//     or anything else unknown.
//
// HasTTY is the harness's controlling terminal where the OS reports one.
// Windows doesn't, so there it is set when a shell, a terminal, a
// multiplexer or sshd is anywhere among the ancestors.
func Classify(goos string, chain []Proc) Lineage {
	l := Lineage{ParentClass: event.ParentUnknown, Entrypoint: event.EntrypointUnknown, By: -1}
	if len(chain) == 0 {
		return l
	}
	h := chain[0]
	self, _ := HarnessOf(goos, h)
	role := RoleNone
walk:
	for i := 1; i < len(chain); i++ {
		a := chain[i]
		e, _ := Identify(goos, a)
		switch e.Role {
		case RoleShell:
			if e.JobControl && runsScript(chain[i-1], a) {
				l.ScriptShell = true
			}
			continue
		case RoleWrapper:
			continue
		case RoleHarness:
			if i == 1 && self != "" && e.Harness == self {
				continue
			}
		case RoleRuntime:
			if e.JSRuntime && i == 1 && launched(a, h) {
				continue
			}
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
// is among chain[0]'s ancestors. Where the OS doesn't say whether a
// process has a terminal (Windows), that is the sign it has a console: a
// console program inherits the console of whatever started it.
func consoleAbove(goos string, chain []Proc) bool {
	for _, a := range chain[1:] {
		e, _ := Identify(goos, a)
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

// launched reports whether runtime started at most LauncherWindow before
// harness, as a launcher that starts the harness at once does.
func launched(runtime, harness Proc) bool {
	if runtime.Start.IsZero() || harness.Start.IsZero() {
		return false
	}
	d := harness.Start.Sub(runtime.Start)
	return d >= 0 && d <= LauncherWindow
}
