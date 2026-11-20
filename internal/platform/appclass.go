// SPDX-License-Identifier: AGPL-3.0-or-later

// The app-class table, appclass.json: what pairtime knows about an
// executable, for the Windows, macOS and Linux code alike. Focus (SM 3.2)
// reads an entry's app class; the parent-chain classifier in classify.go
// reads its role.

package platform

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Role is an executable's part in a process's parent chain (SM 1.2,
// parent_class; SM 2.2 rule 4).
type Role string

const (
	// RoleNone is an executable that says nothing about how a session
	// started: a browser, a chat app, anything not in the table.
	RoleNone Role = "none"
	// RoleShell is a command shell. A plain interactive one is skipped; one
	// running a script makes the session script_runtime (SM 1.2).
	RoleShell Role = "shell"
	// RoleWrapper only runs another program (login, sudo, nohup) and is
	// always skipped.
	RoleWrapper Role = "wrapper"
	// RoleTerminal is a terminal emulator: parent_class terminal_app.
	RoleTerminal Role = "terminal"
	// RoleIDE is an editor or IDE: parent_class ide, entrypoint ide.
	RoleIDE Role = "ide"
	// RoleDesktop is an agent desktop app that hosts harness sessions
	// (SM 3.2): entrypoint desktop.
	RoleDesktop Role = "desktop"
	// RoleMultiplexer is tmux, screen and the like.
	RoleMultiplexer Role = "multiplexer"
	// RoleRemote is a remote login server such as sshd.
	RoleRemote Role = "remote"
	// RoleRuntime is a script interpreter: parent_class script_runtime.
	RoleRuntime Role = "runtime"
	// RoleScheduler starts programs on a schedule or as services: cron,
	// systemd, launchd, Task Scheduler.
	RoleScheduler Role = "scheduler"
	// RoleHarness is an AI coding harness: parent_class agent when it is an
	// ancestor, and a session the procs scanner reports when it fires no
	// hooks (DESIGN 3.3).
	RoleHarness Role = "harness"
)

var roles = []Role{
	RoleNone, RoleShell, RoleWrapper, RoleTerminal, RoleIDE, RoleDesktop,
	RoleMultiplexer, RoleRemote, RoleRuntime, RoleScheduler, RoleHarness,
}

// The operating systems the client ships for, as runtime.GOOS names them.
const (
	Windows = "windows"
	Darwin  = "darwin"
	Linux   = "linux"
)

var oses = []string{Windows, Darwin, Linux}

// Entry is one row of appclass.json.
type Entry struct {
	// Names are executable base names: lowercase, without ".exe". A name
	// ending in "*" matches every name that starts with the rest.
	Names []string `json:"names"`
	// OS lists the operating systems the entry holds on. Empty means all.
	OS []string `json:"os,omitempty"`
	// AppClass is the os.focus class of a window the executable owns.
	AppClass event.AppClass `json:"app_class"`
	Role     Role           `json:"role"`
	// Harness is the harness a RoleHarness executable runs.
	Harness event.Harness `json:"harness,omitempty"`
	// JobControl is set on shells that, when interactive, start each
	// command in a process group of its own (POSIX job control). For those
	// shells a child in the shell's own group means the shell is running a
	// script, not taking commands from a person.
	JobControl bool `json:"job_control,omitempty"`
	// JSRuntime marks Node-style runtimes. One that is a NodeLauncher
	// harness's direct parent and started just before it is the harness's
	// own launcher (an npm wrapper), and one whose parent is an IDE belongs
	// to the IDE.
	JSRuntime bool `json:"js_runtime,omitempty"`
	// NodeLauncher marks a harness whose npm package starts it through a
	// Node wrapper that runs the native harness as its child, as codex's
	// does. Only for these is a Node-style runtime just above the harness
	// taken for its launcher (LauncherOf): Claude Code's npm install runs
	// no Node at all (CP 4.1), so a node above claude is a script.
	NodeLauncher bool `json:"node_launcher,omitempty"`
	// GivesConsole marks the Windows shell, explorer: a console program it
	// starts gets a console window of its own, so a harness it starts
	// directly (from the address bar, Win+R, a shortcut or a double-click)
	// has a terminal.
	GivesConsole bool `json:"gives_console,omitempty"`
	// Cite says where the class comes from: an HS, CP or SM section.
	Cite string `json:"cite"`
}

type appFile struct {
	About   string  `json:"about"`
	Entries []Entry `json:"entries"`
}

//go:embed appclass.json
var appclassJSON []byte

// apps indexes appclass.json by OS.
type apps struct {
	entries []Entry
	exact   map[string]map[string]int // os, name: index into entries
	prefix  map[string][]prefixRule   // os
}

type prefixRule struct {
	prefix string
	entry  int
}

var loadApps = sync.OnceValues(func() (*apps, error) { return parseApps(appclassJSON) })

func parseApps(data []byte) (*apps, error) {
	var f appFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("appclass.json: %w", err)
	}
	a := &apps{
		entries: f.Entries,
		exact:   make(map[string]map[string]int),
		prefix:  make(map[string][]prefixRule),
	}
	for _, goos := range oses {
		a.exact[goos] = make(map[string]int)
	}
	for i, e := range f.Entries {
		for _, goos := range e.oses() {
			for _, n := range e.Names {
				if p, ok := strings.CutSuffix(n, "*"); ok {
					a.prefix[goos] = append(a.prefix[goos], prefixRule{p, i})
				} else {
					a.exact[goos][n] = i
				}
			}
		}
	}
	return a, nil
}

func (e Entry) oses() []string {
	if len(e.OS) == 0 {
		return oses
	}
	return e.OS
}

// Entries returns every row of appclass.json, in file order.
func Entries() []Entry {
	a := mustApps()
	return append([]Entry(nil), a.entries...)
}

func mustApps() *apps {
	a, err := loadApps()
	if err != nil {
		// The file is embedded and appclass_test.go parses it, so this is a
		// broken build, not a runtime condition.
		panic(err)
	}
	return a
}

// Lookup finds the entry for an executable on goos. name is matched
// without regard to case, with any ".exe" suffix removed. An exact name
// wins over a pattern, and a longer pattern over a shorter one.
func Lookup(goos, name string) (Entry, bool) {
	a := mustApps()
	n := normalizeName(name)
	if n == "" {
		return Entry{}, false
	}
	if i, ok := a.exact[goos][n]; ok {
		return a.entries[i], true
	}
	best, bestLen := -1, -1
	for _, r := range a.prefix[goos] {
		if strings.HasPrefix(n, r.prefix) && len(r.prefix) > bestLen {
			best, bestLen = r.entry, len(r.prefix)
		}
	}
	if best < 0 {
		return Entry{}, false
	}
	return a.entries[best], true
}

// AppClassOf is the os.focus class of a window owned by the executable
// name on goos: AppOther when the table doesn't know it.
func AppClassOf(goos, name string) event.AppClass {
	if e, ok := Lookup(goos, name); ok {
		return e.AppClass
	}
	return event.AppOther
}

func normalizeName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n, _ = strings.CutSuffix(n, ".exe")
	return n
}

// ExeName turns the full path of a process's executable image into the
// name the table is keyed by: the base name without ".exe", except for
// these install layouts, whose base name doesn't name the program.
//
//   - Claude Code's native install runs from claude/versions/<version>
//     through a claude symlink (CP 4.1), so on Linux and macOS the image's
//     own name is a version number. Such an image is named "claude".
//   - The Cursor CLI's install script puts it at
//     cursor-agent/versions/<version>/cursor-agent and links both agent
//     and cursor-agent in ~/.local/bin to it (HS 4.1; cursor.com/install,
//     read 2026-11-19). An image in such a version directory called
//     cursor-agent, agent or node, a runtime the CLI may bundle, is named
//     "cursor-agent". The bare name agent names nothing, because other
//     programs use it: Battle.net's updater is Agent.exe.
//   - The Claude desktop app's executable is also called claude.exe on
//     Windows (under AnthropicClaude) and Claude on macOS (Claude.app). It
//     hosts sessions rather than being one (SM 3.2), so it is named
//     "claude-desktop". Likewise Codex.app is "codex-desktop".
//   - The Devin CLI is devin, and Devin Desktop carries its own copy in
//     resources/app/extensions/windsurf/devin/bin (HS 8; docs.devin.ai's
//     air-gapped install page, read 2026-11-19). The docs don't name Devin
//     Desktop's own executable, which may be Devin too, so the main
//     executable of a macOS app bundle called devin, and on Windows a
//     devin.exe outside a bin directory, are named "devin-desktop". A
//     missed CLI session costs less than every Electron process of the
//     editor reported as one.
//
// Linux marks an image deleted since it started, as after an update, with
// " (deleted)"; that is dropped.
func ExeName(goos, path string) string {
	p, dir, base := splitPath(goos, path)
	lower := strings.ToLower(p)
	parent, grand, great := lastThree(strings.ToLower(dir))
	lbase := strings.ToLower(base)
	switch {
	case goos == Darwin && strings.HasSuffix(lower, "/claude.app/contents/macos/claude"):
		return "claude-desktop"
	case goos == Darwin && strings.HasSuffix(lower, "/codex.app/contents/macos/codex"):
		return "codex-desktop"
	case goos == Darwin && strings.HasSuffix(lower, ".app/contents/macos/devin"):
		return "devin-desktop"
	case goos == Windows && lbase == "claude" &&
		(parent == "anthropicclaude" || grand == "anthropicclaude" && strings.HasPrefix(parent, "app-")):
		return "claude-desktop"
	case goos == Windows && lbase == "devin" && dir != "" && parent != "bin":
		return "devin-desktop"
	case isVersion(base) && parent == "versions" && grand == "claude":
		return "claude"
	case grand == "versions" && great == "cursor-agent" && (lbase == "cursor-agent" || lbase == "agent" || lbase == "node"):
		return "cursor-agent"
	}
	return base
}

// splitPath cleans an executable's path and splits it into its directory
// and its base name without ".exe".
func splitPath(goos, path string) (p, dir, base string) {
	p = path
	if goos == Windows {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	if goos == Linux {
		p = strings.TrimSuffix(p, " (deleted)")
	}
	p = strings.TrimRight(p, "/")
	base = p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		dir, base = p[:i], p[i+1:]
	}
	if goos == Windows && len(base) > 4 && strings.EqualFold(base[len(base)-4:], ".exe") {
		base = base[:len(base)-4]
	}
	return p, dir, base
}

// commMin is the shortest name Linux (15 bytes) or macOS (16) cuts a
// process's comm to.
const commMin = 15

// AliasOf returns what a reader stores as Proc.Alias for a process whose
// executable is at path, named name by ExeName, which goes by comm (Linux
// comm, macOS p_comm). It is empty when comm says nothing more: when it
// is name, or the file's own base name whole or cut short by the OS. So a
// Node app that set its title, or a script run through a #! line, keeps
// its own name, while Claude.app's "Claude" doesn't override the name
// "claude-desktop" that ExeName gave it.
func AliasOf(goos, path, name, comm string) string {
	if comm == "" || strings.EqualFold(comm, name) {
		return ""
	}
	_, _, base := splitPath(goos, path)
	if strings.EqualFold(comm, base) {
		return ""
	}
	if len(comm) >= commMin && len(base) > len(comm) && strings.EqualFold(base[:len(comm)], comm) {
		return ""
	}
	return comm
}

// lastThree returns the last three components of a slash path, last
// first, with "" for those it doesn't have.
func lastThree(dir string) (last, second, third string) {
	parts := [3]string{}
	for k := range parts {
		if dir == "" {
			break
		}
		i := strings.LastIndexByte(dir, '/')
		parts[k] = dir[i+1:]
		if i < 0 {
			break
		}
		dir = dir[:i]
	}
	return parts[0], parts[1], parts[2]
}

// isVersion reports whether s looks like 2.1.328: digits and dots, starting
// and ending with a digit, with at least one dot.
func isVersion(s string) bool {
	if s == "" || !isDigit(s[0]) || !isDigit(s[len(s)-1]) {
		return false
	}
	dots := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '.':
			if s[i-1] == '.' {
				return false
			}
			dots++
		case !isDigit(s[i]):
			return false
		}
	}
	return dots > 0
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// NeedsPath reports whether ExeName may name an executable whose base name
// is name differently once it has the full path: claude (Claude Code or the
// Claude desktop app), devin (the Devin CLI or Devin Desktop), agent and
// node (the Cursor CLI's image or another program's), and a version number.
// A reader that gets base names cheaply, as Windows' process snapshot gives
// them, reads the full path of these alone.
func NeedsPath(name string) bool {
	switch n := normalizeName(name); n {
	case "claude", "devin", "agent", "node":
		return true
	default:
		return isVersion(n)
	}
}
