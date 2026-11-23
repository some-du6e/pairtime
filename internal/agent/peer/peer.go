// SPDX-License-Identifier: AGPL-3.0-or-later

// Package peer is the agent's peer-process check: which process is at the
// other end of a loopback connection to the agent, and whether that
// process may speak for what the connection carries (DESIGN 5.2, 5.7;
// WP-OS13). Any process of this user can reach the agent's server on
// 127.0.0.1 (internal/agent/localhttp), so two kinds of request get the
// check:
//
//   - WakaTime heartbeats (DESIGN 5.6, 5.7). Heartbeat passes when the
//     client is wakatime-cli and an editor in the app-class table is among
//     its ancestors, because that editor's plugin ran it. An editor
//     heartbeat that passes is via = editor, and the bridge records of one
//     that passes via = bridge. One that fails is editor_unverified, and
//     its bridge records bridge_unverified (WP-AD16).
//   - OTLP from Claude Code (DESIGN 5.1, 5.2). OTLP passes when the client
//     is a harness process that a hook's parent chain or the process scan
//     names (internal/agent/procs). A request that fails is refused and
//     counted (WP-AD4).
//
// Both fail closed: a connection whose client end the check can't trace
// to one process of this user fails, and on an OS without a lookup every
// connection fails. Each result carries a Reason, a code that says which
// rule failed and holds nothing about the processes.
//
// The client is found in the OS's own tables, while it waits for the
// agent's answer: GetExtendedTcpTable on Windows, /proc/net/tcp and
// /proc/<pid>/fd on Linux, and libproc's socket descriptors on macOS
// (LoopbackOwner in internal/platform/windows, linux and darwin). Each
// lookup reads the process table with it, and Chain is the client's parent
// chain from that read. A lookup costs a read of those tables, and a rule
// costs nothing. So a caller looks a connection up once, when its first
// request comes (Checker.Lookup), keeps that Result for the connection's
// life, and applies the rule to every request on it (Result.Heartbeat,
// Result.OTLP). The heartbeat rule gives a process the same answer every
// time. The OTLP rule doesn't, because named starts to report a process
// once a hook claims it or a scan reports it. A kept Result belongs to its
// connection: it goes when the connection closes, and is never kept by
// address alone, since a later connection can come from the same port.
// ConnAddrs gives a connection's two addresses from its net.Conn, as
// http.Server's ConnContext has it; in a handler, the request's RemoteAddr
// and its context's http.LocalAddrContextKey give the same two.
//
// The rules go by names, and not by the same ones:
//
//   - The heartbeat rule takes the client and its editor by their
//     executables' own names (Proc.Name), never by a title a process gives
//     itself (Proc.Alias: its comm, its process.title, a #! script's file
//     name). So it stops a script that posts to the agent's port, and one
//     that titles itself after an editor and runs the real wakatime-cli. A
//     copy of a program renamed wakatime-cli, run under a program renamed
//     like an editor, passes it. On Linux so does a process that made
//     itself undumpable, because the reader can't read its exe link and
//     names it by its comm (internal/platform/linux).
//   - The OTLP rule takes the client as the app-class table identifies it
//     (platform.HarnessOf), by its title where the table knows it, as the
//     process scan and the hook path do, because Claude Code installed
//     with npm runs as node titled claude. So a process titled like a
//     harness that the process scan reports passes OTLP, and so does one
//     that a hook's parent chain names. That is the residual WP-AD4 builds
//     on.
//
// What the check reads stays on the device: pids, executable names and
// parent chains are local (AGENTS.md), and only Reason and the via it
// decides may leave it.
package peer

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"strings"

	"github.com/some-du6e/pairtime/internal/platform"
)

// Reason says whether a check passed, and if not, which rule failed it.
type Reason string

const (
	// Passed is a check that passed.
	Passed Reason = "passed"
	// Found is a lookup that found the client, a process of this user's,
	// before a rule has decided on it (Checker.Lookup). It doesn't pass.
	Found Reason = "found"
	// NoOwner is a connection whose client end the lookup couldn't trace
	// to one process of this user: it isn't an established loopback
	// connection, no process this user can see holds it, or several do.
	// The Linux and macOS lookups see only this user's processes, so there
	// another user's connection is NoOwner too. Result.Err says which.
	NoOwner Reason = "no_owner"
	// OtherUser is a client the process table gives another owner: on
	// Windows a process in another session, or one whose session the
	// table couldn't read. On Linux and macOS, where the lookups find only
	// this user's processes, it is a backstop.
	OtherUser Reason = "other_user"
	// NotWakaTime is a heartbeat whose client isn't wakatime-cli.
	NotWakaTime Reason = "not_wakatime_cli"
	// NoEditor is a heartbeat from a wakatime-cli that no editor in the
	// app-class table started.
	NoEditor Reason = "no_editor"
	// NotHarness is OTLP whose client isn't a harness process.
	NotHarness Reason = "not_harness"
	// NotNamed is OTLP from a harness process that no hook's parent chain
	// and no process scan has named.
	NotNamed Reason = "not_named"
)

// Result is a connection's client as one lookup found it, and the reason
// the last rule applied to it gave.
type Result struct {
	Reason Reason
	// PID is the client's pid, 0 when the lookup found none.
	PID int
	// Chain is the client and its ancestors, nearest first, as the process
	// table read with the lookup gives them, so Chain[0] is the client. It
	// is nil when the lookup found no client.
	Chain []platform.Proc
	// Editor is the index in Chain of the editor that a passing heartbeat
	// rule found, and -1 otherwise.
	Editor int
	// Err says why the lookup found no client, when Reason is NoOwner.
	Err error
	// goos is the GOOS whose names Chain carries.
	goos string
}

// Pass reports whether the check passed.
func (r Result) Pass() bool { return r.Reason == Passed }

// found reports whether the lookup behind r found a client of this user's,
// so that a rule can decide on it.
func (r Result) found() bool {
	return r.Reason != NoOwner && r.Reason != OtherUser && len(r.Chain) > 0
}

// Options configure a Checker.
type Options struct {
	// Owner returns the pid of the one process that holds the client end of
	// the loopback TCP connection from client to server, and the process
	// table read while it held it. Nil means this OS's LoopbackOwner.
	Owner func(client, server netip.AddrPort) (int, []platform.Proc, error)
	// OS is the GOOS whose names Owner's process table carries. Empty means
	// runtime.GOOS.
	OS string
	// Self is the Proc.Owner of this user's processes, as the process scan
	// takes it (procs.Options.Self). Empty means this process's own, as
	// this OS's reader names it.
	Self string
}

// Checker runs the checks. It is safe for concurrent use.
type Checker struct {
	owner func(client, server netip.AddrPort) (int, []platform.Proc, error)
	goos  string
	self  string
}

// New returns a Checker.
func New(opts Options) *Checker {
	c := &Checker{owner: opts.Owner, goos: cmp.Or(opts.OS, runtime.GOOS), self: opts.Self}
	if c.owner == nil {
		c.owner = ownerOS
	}
	if c.self == "" {
		c.self = selfOS()
	}
	return c
}

// errNoSelf means the OS wouldn't say whose processes are this user's, so
// no client can be shown to be.
var errNoSelf = errors.New("this OS won't say whose processes are this user's")

// Lookup finds the client of the connection from client to server: the
// process that holds the client end, with its parent chain. client is the
// connection's remote address as the agent's server sees it, and server
// its local address (ConnAddrs). The Result's Reason is Found when the
// client is a process of this user's, and NoOwner or OtherUser when it
// isn't or can't be shown to be. Either rule, applied to the Result,
// decides what passes.
func (c *Checker) Lookup(client, server netip.AddrPort) Result {
	r := Result{Reason: NoOwner, Editor: -1, goos: c.goos}
	if c.self == "" {
		r.Err = errNoSelf
		return r
	}
	pid, procs, err := c.owner(client, server)
	if err != nil {
		r.Err = err
		return r
	}
	chain := platform.NewTable(c.goos, procs).Chain(pid)
	if len(chain) == 0 {
		r.Err = fmt.Errorf("process %d holds the connection from %v to %v but isn't in the process table", pid, client, server)
		return r
	}
	r.PID, r.Chain = pid, chain
	if chain[0].Owner != c.self {
		r.Reason = OtherUser
		return r
	}
	r.Reason = Found
	return r
}

// Heartbeat looks the connection up and applies the heartbeat rule
// (Result.Heartbeat).
func (c *Checker) Heartbeat(client, server netip.AddrPort) Result {
	return c.Lookup(client, server).Heartbeat()
}

// OTLP looks the connection up and applies the OTLP rule (Result.OTLP).
func (c *Checker) OTLP(client, server netip.AddrPort, named func(platform.ID) bool) Result {
	return c.Lookup(client, server).OTLP(named)
}

// Heartbeat applies the rule for WakaTime heartbeats to the client r's
// lookup found: it passes when the client's executable is wakatime-cli and
// one of its ancestors runs an editor's executable, a name the app-class
// table gives the role ide (DESIGN 5.7). Both go by Proc.Name alone, never
// by the title a process gave itself (Proc.Alias), so a shell or a script
// that titles itself code or vim is no editor. A wakatime-cli that a
// script, a terminal or a harness started without an editor above it
// fails, as does one whose editor has exited, since its chain then stops
// short of the editor. A Result whose lookup found no client of this
// user's comes back as it is.
func (r Result) Heartbeat() Result {
	if !r.found() {
		return r
	}
	r.Editor = -1
	if !wakaTimeCLI(r.Chain[0].Name) {
		r.Reason = NotWakaTime
		return r
	}
	for i, a := range r.Chain[1:] {
		if e, ok := platform.Lookup(r.goos, a.Name); ok && e.Role == platform.RoleIDE {
			r.Reason, r.Editor = Passed, i+1
			return r
		}
	}
	r.Reason = NoEditor
	return r
}

// OTLP applies the rule for OTLP to the client r's lookup found: it passes
// when the client is a harness process by the app-class table
// (platform.HarnessOf) and named reports that a hook's parent chain
// claimed that process or the process scan reported it (DESIGN 5.2), which
// the agent knows by the process's ID (procs.Scanner.Claimed and
// Sessions). HarnessOf goes by the process's title where the table knows
// it, as the scan and the hook path do, because Claude Code installed with
// npm runs as node titled claude, so a process titled like a harness that
// the scan reports passes. named is asked anew each time the rule is
// applied, and a nil named names nothing. A Result whose lookup found no
// client of this user's comes back as it is.
func (r Result) OTLP(named func(platform.ID) bool) Result {
	if !r.found() {
		return r
	}
	r.Editor = -1
	if _, ok := platform.HarnessOf(r.goos, r.Chain[0]); !ok {
		r.Reason = NotHarness
		return r
	}
	if named == nil || !named(r.Chain[0].ID()) {
		r.Reason = NotNamed
		return r
	}
	r.Reason = Passed
	return r
}

// wakaTimeCLI reports whether name, a process's executable name, is
// wakatime-cli's: wakatime-cli-<os>-<arch>, the release asset that the
// editor plugins download and run (CP 1.1, 4.2), or wakatime-cli, as a
// package manager installs it. Only the executable's own name counts, not
// a name the process gave itself, because wakatime-cli is a Go binary,
// never a script.
func wakaTimeCLI(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".exe")
	return n == "wakatime-cli" || strings.HasPrefix(n, "wakatime-cli-")
}

// ConnAddrs returns the client and server addresses that the checks take,
// from a TCP connection the agent's server accepted: its remote address is
// the client's, and its local address the server's.
func ConnAddrs(conn net.Conn) (client, server netip.AddrPort, err error) {
	remote, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return client, server, fmt.Errorf("peer: a %T connection isn't TCP", conn)
	}
	local, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return client, server, fmt.Errorf("peer: a %T connection isn't TCP", conn)
	}
	return unmap(remote.AddrPort()), unmap(local.AddrPort()), nil
}

func unmap(ap netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}
