// SPDX-License-Identifier: AGPL-3.0-or-later

package peer

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
)

var (
	clientAddr = netip.MustParseAddrPort("127.0.0.1:54321")
	agentAddr  = netip.MustParseAddrPort("127.0.0.1:7247")
)

// line builds a parent chain from the root down: each process is the
// parent of the next, and the last is the client. A process with a pid set
// keeps it and its parent; the others get pids from 100.
func line(owner string, procs ...platform.Proc) []platform.Proc {
	out := make([]platform.Proc, len(procs))
	for i, p := range procs {
		if p.PID == 0 {
			p.PID = 100 + i
			if i > 0 {
				p.PPID = out[i-1].PID
			}
		}
		if p.Owner == "" {
			p.Owner = owner
		}
		out[i] = p
	}
	return out
}

// proc is a process whose executable is name.
func proc(name string) platform.Proc { return platform.Proc{Name: name} }

// last is the pid of the client in a line.
func last(procs []platform.Proc) int { return procs[len(procs)-1].PID }

const (
	me      = "uid:1000"
	session = "session:1"
)

func TestHeartbeat(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC)
	reused := []platform.Proc{
		{PID: 1, Name: "systemd", Owner: me, Start: t0},
		// pid 2 was the editor that started wakatime-cli; it has exited,
		// and a later process has its pid.
		{PID: 2, PPID: 1, Name: "code", Owner: me, Start: t0.Add(10 * time.Second)},
		{PID: 3, PPID: 2, Name: "wakatime-cli-linux-amd64", Owner: me, Start: t0.Add(5 * time.Second)},
	}
	tests := []struct {
		name   string
		goos   string
		self   string
		procs  []platform.Proc
		owner  int   // the pid the lookup gives; 0 means the line's last
		err    error // the lookup's error
		want   Reason
		editor int // the Editor index a pass gives
	}{
		{
			name:  "wakatime-cli that VS Code's extension host started passes",
			procs: line(me, proc("systemd"), proc("code"), proc("code"), proc("wakatime-cli-linux-amd64")),
			want:  Passed, editor: 1,
		},
		{
			name:  "wakatime-cli that a shell under VS Code started passes",
			procs: line(me, proc("systemd"), proc("code"), proc("bash"), proc("wakatime-cli-linux-amd64")),
			want:  Passed, editor: 2,
		},
		{
			name: "wakatime-cli that Vim started in a terminal passes, Debian's vim.basic going by its comm",
			procs: line(me, proc("systemd"), proc("gnome-terminal-server"), proc("bash"),
				platform.Proc{Name: "vim.basic", Alias: "vim"}, proc("wakatime-cli-linux-amd64")),
			want: Passed, editor: 1,
		},
		{
			name:  "wakatime-cli by the name a package manager installs it under passes",
			procs: line(me, proc("systemd"), proc("idea"), proc("wakatime-cli")),
			want:  Passed, editor: 1,
		},
		{
			name:  "on Windows, wakatime-cli-windows-amd64 that Code started passes",
			goos:  platform.Windows,
			self:  session,
			procs: line(session, proc("explorer"), proc("Code"), proc("Code"), proc("wakatime-cli-windows-amd64")),
			want:  Passed, editor: 1,
		},
		{
			name:  "on macOS, wakatime-cli that VS Code's plugin host started passes",
			goos:  platform.Darwin,
			procs: line(me, proc("launchd"), proc("Electron"), proc("Code Helper (Plugin)"), proc("wakatime-cli-darwin-arm64")),
			want:  Passed, editor: 1,
		},
		{
			name:  "a wakatime-cli that a script in a terminal started fails, since no editor started it",
			procs: line(me, proc("systemd"), proc("gnome-terminal-server"), proc("bash"), proc("python3"), proc("wakatime-cli-linux-amd64")),
			want:  NoEditor,
		},
		{
			name:  "a wakatime-cli that Claude Code started in a terminal fails, since no editor started it",
			procs: line(me, proc("systemd"), proc("kitty"), proc("zsh"), proc("claude"), proc("wakatime-cli-linux-amd64")),
			want:  NoEditor,
		},
		{
			name:  "on macOS, the Claude desktop app isn't an editor",
			goos:  platform.Darwin,
			procs: line(me, proc("launchd"), proc("claude-desktop"), proc("wakatime-cli-darwin-arm64")),
			want:  NoEditor,
		},
		{
			name:  "a wakatime-cli whose editor has exited fails, since its chain stops at the missing parent",
			procs: []platform.Proc{{PID: 1, Name: "systemd", Owner: me}, {PID: 5, PPID: 4, Name: "wakatime-cli-linux-amd64", Owner: me}},
			owner: 5,
			want:  NoEditor,
		},
		{
			name:  "an editor that started after wakatime-cli didn't start it, because its pid was reused",
			procs: reused,
			owner: 3,
			want:  NoEditor,
		},
		{
			name:  "curl posting straight to the port fails, since it isn't wakatime-cli",
			procs: line(me, proc("systemd"), proc("gnome-terminal-server"), proc("bash"), proc("curl")),
			want:  NotWakaTime,
		},
		{
			name:  "a script whose comm says wakatime-cli isn't wakatime-cli",
			procs: line(me, proc("systemd"), proc("code"), proc("bash"), platform.Proc{Name: "python3", Alias: "wakatime-cli"}),
			want:  NotWakaTime,
		},
		{
			name:  "an editor's own connection isn't wakatime-cli's",
			procs: line(me, proc("systemd"), proc("code")),
			want:  NotWakaTime,
		},
		{
			name: "a connection the lookup can't trace fails",
			err:  errors.New("no process this user can read holds it"),
			want: NoOwner,
		},
		{
			name:  "a client the process table lacks fails",
			procs: line(me, proc("systemd"), proc("code")),
			owner: 77,
			want:  NoOwner,
		},
		{
			name:  "a wakatime-cli the table gives another user fails, a backstop where the lookup sees only this user's processes",
			procs: line("uid:1001", proc("systemd"), proc("code"), proc("wakatime-cli-linux-amd64")),
			want:  OtherUser,
		},
		{
			name: "on Windows, a client whose session the reader couldn't read fails",
			goos: platform.Windows,
			self: session,
			procs: append(line(session, proc("explorer"), proc("Code")),
				platform.Proc{PID: 200, PPID: 101, Name: "wakatime-cli-windows-amd64"}),
			owner: 200,
			want:  OtherUser,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goos := tt.goos
			if goos == "" {
				goos = platform.Linux
			}
			self := tt.self
			if self == "" {
				self = me
			}
			owner := tt.owner
			if owner == 0 && len(tt.procs) > 0 {
				owner = last(tt.procs)
			}
			c := New(Options{OS: goos, Self: self, Owner: func(client, server netip.AddrPort) (int, []platform.Proc, error) {
				if client != clientAddr || server != agentAddr {
					t.Errorf("the lookup got %v, %v; want %v, %v", client, server, clientAddr, agentAddr)
				}
				if tt.err != nil {
					return 0, nil, tt.err
				}
				return owner, tt.procs, nil
			}})
			r := c.Heartbeat(clientAddr, agentAddr)
			if r.Reason != tt.want {
				t.Fatalf("Heartbeat = %s (%v), want %s", r.Reason, r.Err, tt.want)
			}
			if r.Pass() != (tt.want == Passed) {
				t.Errorf("Pass() = %v for %s", r.Pass(), r.Reason)
			}
			switch {
			case tt.want == Passed && r.Editor != tt.editor:
				t.Errorf("Editor = %d, want %d", r.Editor, tt.editor)
			case tt.want != Passed && r.Editor != -1:
				t.Errorf("Editor = %d on a check that failed, want -1", r.Editor)
			}
			switch tt.want {
			case NoOwner:
				if r.Err == nil || r.PID != 0 || r.Chain != nil {
					t.Errorf("a check with no owner gave PID %d, Chain %v, Err %v; want 0, nil and an error", r.PID, r.Chain, r.Err)
				}
			default:
				if r.PID != owner || len(r.Chain) == 0 || r.Chain[0].PID != owner {
					t.Errorf("PID %d, Chain %v; want both to name the client %d", r.PID, r.Chain, owner)
				}
			}
		})
	}
}

func TestOTLP(t *testing.T) {
	claude := line(me, proc("systemd"), proc("kitty"), proc("zsh"), platform.Proc{Name: "claude", Ticks: 4242})
	tests := []struct {
		name  string
		self  string
		procs []platform.Proc
		err   error
		// names is what named answers; nil passes a nil named.
		names *bool
		want  Reason
	}{
		{name: "a harness process that a hook or the scan named passes", procs: claude, names: ptr(true), want: Passed},
		{
			name:  "a Node app that titled itself claude passes, as the app-class table goes by its title",
			procs: line(me, proc("systemd"), proc("kitty"), proc("zsh"), platform.Proc{Name: "node", Alias: "claude"}),
			names: ptr(true), want: Passed,
		},
		{name: "a harness process that nothing named fails", procs: claude, names: ptr(false), want: NotNamed},
		{name: "a nil named names nothing", procs: claude, want: NotNamed},
		{
			name:  "a process that isn't a harness fails, even when named",
			procs: line(me, proc("systemd"), proc("kitty"), proc("zsh"), proc("python3")),
			names: ptr(true), want: NotHarness,
		},
		{
			name:  "wakatime-cli isn't a harness",
			procs: line(me, proc("systemd"), proc("code"), proc("wakatime-cli-linux-amd64")),
			names: ptr(true), want: NotHarness,
		},
		{
			name:  "a harness process the table gives another user fails",
			procs: line("uid:1001", proc("systemd"), proc("kitty"), proc("zsh"), proc("claude")),
			names: ptr(true), want: OtherUser,
		},
		{name: "a connection the lookup can't trace fails", err: errors.New("not established"), names: ptr(true), want: NoOwner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(Options{OS: platform.Linux, Self: me, Owner: func(netip.AddrPort, netip.AddrPort) (int, []platform.Proc, error) {
				if tt.err != nil {
					return 0, nil, tt.err
				}
				return last(tt.procs), tt.procs, nil
			}})
			var asked []platform.ID
			var fn func(platform.ID) bool
			if tt.names != nil {
				fn = func(id platform.ID) bool {
					asked = append(asked, id)
					return *tt.names
				}
			}
			r := c.OTLP(clientAddr, agentAddr, fn)
			if r.Reason != tt.want {
				t.Fatalf("OTLP = %s (%v), want %s", r.Reason, r.Err, tt.want)
			}
			if r.Pass() != (tt.want == Passed) {
				t.Errorf("Pass() = %v for %s", r.Pass(), r.Reason)
			}
			// named is asked about the client, by the ID the process scan and
			// the hook path key it by, and only once the client is a harness.
			if tt.want == Passed || tt.want == NotNamed && fn != nil {
				client := tt.procs[len(tt.procs)-1]
				if len(asked) != 1 || asked[0] != client.ID() {
					t.Errorf("named was asked about %v, want once about %v", asked, client.ID())
				}
			} else if len(asked) != 0 {
				t.Errorf("named was asked about %v, want not at all", asked)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestNoSelf(t *testing.T) {
	looked := false
	c := &Checker{goos: platform.Linux, owner: func(netip.AddrPort, netip.AddrPort) (int, []platform.Proc, error) {
		looked = true
		procs := line(me, proc("systemd"), proc("code"), proc("wakatime-cli-linux-amd64"))
		return last(procs), procs, nil
	}}
	tests := []struct {
		name  string
		check func() Result
	}{
		{"a heartbeat fails when the OS won't say who this user is", func() Result { return c.Heartbeat(clientAddr, agentAddr) }},
		{"OTLP fails when the OS won't say who this user is", func() Result {
			return c.OTLP(clientAddr, agentAddr, func(platform.ID) bool { return true })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			looked = false
			r := tt.check()
			if r.Reason != NoOwner || !errors.Is(r.Err, errNoSelf) {
				t.Errorf("= %s, %v; want %s, %v", r.Reason, r.Err, NoOwner, errNoSelf)
			}
			if looked {
				t.Error("the check looked the connection up without knowing who this user is")
			}
		})
	}
}

// TestKeptLookup applies rules, in turn, to one Result a lookup gave, as a
// caller does to each request on a connection it looked up once.
func TestKeptLookup(t *testing.T) {
	claude := line(me, proc("systemd"), proc("kitty"), proc("zsh"), proc("claude"))
	never := func(platform.ID) bool { return false }
	always := func(platform.ID) bool { return true }
	heartbeat := func(r Result) Result { return r.Heartbeat() }
	otlp := func(named func(platform.ID) bool) func(Result) Result {
		return func(r Result) Result { return r.OTLP(named) }
	}
	tests := []struct {
		name  string
		procs []platform.Proc
		err   error
		// lookup is the Reason Lookup gives, and want the Reason each of
		// rules gives, applied in turn to the Result before it.
		lookup Reason
		rules  []func(Result) Result
		want   []Reason
	}{
		{
			name:   "a lookup alone finds the client and neither passes nor fails a rule",
			procs:  claude,
			lookup: Found,
		},
		{
			name:   "a kept lookup gives the OTLP rule's answer anew once a hook or a scan names the client",
			procs:  claude,
			lookup: Found,
			rules:  []func(Result) Result{otlp(never), otlp(always)},
			want:   []Reason{NotNamed, Passed},
		},
		{
			name:   "a heartbeat's result gives the OTLP rule its own answer, and the OTLP rule's the heartbeat rule its own",
			procs:  claude,
			lookup: Found,
			rules:  []func(Result) Result{heartbeat, otlp(always), heartbeat},
			want:   []Reason{NotWakaTime, Passed, NotWakaTime},
		},
		{
			name:   "a lookup that found no owner stays no_owner under either rule",
			err:    errors.New("not established"),
			lookup: NoOwner,
			rules:  []func(Result) Result{heartbeat, otlp(always)},
			want:   []Reason{NoOwner, NoOwner},
		},
		{
			name:   "another user's client stays other_user under either rule",
			procs:  line("uid:1001", proc("systemd"), proc("code"), proc("wakatime-cli-linux-amd64")),
			lookup: OtherUser,
			rules:  []func(Result) Result{heartbeat, otlp(always)},
			want:   []Reason{OtherUser, OtherUser},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookups := 0
			c := New(Options{OS: platform.Linux, Self: me, Owner: func(netip.AddrPort, netip.AddrPort) (int, []platform.Proc, error) {
				lookups++
				if tt.err != nil {
					return 0, nil, tt.err
				}
				return last(tt.procs), tt.procs, nil
			}})
			r := c.Lookup(clientAddr, agentAddr)
			if r.Reason != tt.lookup || r.Pass() {
				t.Fatalf("Lookup = %s, Pass %v; want %s, false", r.Reason, r.Pass(), tt.lookup)
			}
			for i, rule := range tt.rules {
				r = rule(r)
				if r.Reason != tt.want[i] {
					t.Errorf("rule %d gave %s, want %s", i, r.Reason, tt.want[i])
				}
			}
			if lookups != 1 {
				t.Errorf("the connection was looked up %d times, want once", lookups)
			}
		})
	}
}

func TestZeroResult(t *testing.T) {
	tests := []struct {
		name string
		r    Result
	}{
		{"a zero Result passes the heartbeat rule never", Result{}.Heartbeat()},
		{"a zero Result passes the OTLP rule never, however named", Result{}.OTLP(func(platform.ID) bool { return true })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.r.Pass() {
				t.Errorf("= %s, a pass", tt.r.Reason)
			}
		})
	}
}

func TestWakaTimeCLI(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		want bool
	}{
		{"the Linux release asset is wakatime-cli", "wakatime-cli-linux-amd64", true},
		{"the macOS arm64 release asset is wakatime-cli", "wakatime-cli-darwin-arm64", true},
		{"the Windows release asset is wakatime-cli, whatever its case", "WakaTime-CLI-windows-386", true},
		{"a name that keeps its .exe is wakatime-cli", "wakatime-cli-windows-arm64.exe", true},
		{"Homebrew's wakatime-cli is wakatime-cli", "wakatime-cli", true},
		{"the old Python CLI's name isn't", "wakatime", false},
		{"a name that only starts with wakatime-cli isn't", "wakatime-client", false},
		{"curl isn't", "curl", false},
		{"no name isn't", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wakaTimeCLI(tt.exe); got != tt.want {
				t.Errorf("wakaTimeCLI(%q) = %v, want %v", tt.exe, got, tt.want)
			}
		})
	}
}
