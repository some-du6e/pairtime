// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// p is a chain entry: a name, a process group and a start second. The
// classifier reads only the chain's order, so PIDs are left to mk.
type p struct {
	name, alias string
	pgid        int
	start       int
	tty         TTY
	tpgid       int
}

// mk turns entries, harness first, into a chain with made-up PIDs.
func mk(entries ...p) []Proc {
	chain := make([]Proc, len(entries))
	for i, e := range entries {
		pr := Proc{PID: 1000 - i, PPID: 999 - i, Name: e.name, Alias: e.alias, PGID: e.pgid, TTY: e.tty, TPGID: e.tpgid}
		if e.start != 0 {
			pr.Start = at(e.start)
		}
		chain[i] = pr
	}
	return chain
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		chain   []Proc
		class   event.ParentClass
		entry   event.Entrypoint
		tty     bool
		by      string // the name of the ancestor that decided, "" for none
		script  bool
		comment string
	}{
		{
			name:  "an interactive shell is skipped and the terminal decides",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 300, tty: TTYAttached}, p{name: "zsh", pgid: 200}, p{name: "gnome-terminal-server", pgid: 100}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "gnome-terminal-server",
		},
		{
			name:  "a shell whose child shares its process group runs a script, so the session is script_runtime",
			goos:  Darwin,
			chain: mk(p{name: "claude", pgid: 400, tty: TTYAttached}, p{name: "bash", pgid: 400}, p{name: "zsh", pgid: 300}, p{name: "login", pgid: 200}, p{name: "Terminal", pgid: 100}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "Terminal", script: true,
		},
		{
			name:  "a script run in an IDE's terminal is script_runtime, not an IDE session",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 400, tty: TTYAttached}, p{name: "bash", pgid: 400}, p{name: "bash", pgid: 300}, p{name: "code", pgid: 100}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "code", script: true,
		},
		{
			name:  "python spawning bash spawning claude is script_runtime, the shell in between skipped",
			goos:  Windows,
			chain: mk(p{name: "claude"}, p{name: "bash"}, p{name: "python"}, p{name: "windowsterminal"}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "python",
		},
		{
			name:  "a shell alone is skipped, not taken for a script, on Windows where there are no process groups",
			goos:  Windows,
			chain: mk(p{name: "claude"}, p{name: "bash"}, p{name: "bash"}, p{name: "windowsterminal"}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "windowsterminal",
		},
		{
			name:  "a shell without job control can't show a script, so it counts as interactive",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 200, tty: TTYAttached}, p{name: "pwsh", pgid: 200}, p{name: "kitty", pgid: 100}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "kitty",
		},
		{
			name:  "an IDE extension's harness is an IDE session with no terminal",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 100, tty: TTYNone}, p{name: "code", pgid: 100}),
			class: event.ParentIDE, entry: event.EntrypointIDE, tty: false, by: "code",
		},
		{
			name:  "on Windows a harness straight under the IDE has no terminal",
			goos:  Windows,
			chain: mk(p{name: "claude"}, p{name: "code"}, p{name: "explorer"}),
			class: event.ParentIDE, entry: event.EntrypointIDE, tty: false, by: "code",
		},
		{
			name:  "on Windows a shell in the chain means a console, so has_tty",
			goos:  Windows,
			chain: mk(p{name: "claude"}, p{name: "pwsh"}, p{name: "code"}),
			class: event.ParentIDE, entry: event.EntrypointIDE, tty: true, by: "code",
		},
		{
			name:  "a harness started from another harness's tool shell is agent",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 200, tty: TTYAttached}, p{name: "bash", pgid: 200}, p{name: "claude", pgid: 200}, p{name: "zsh", pgid: 100}),
			class: event.ParentAgent, entry: event.EntrypointUnknown, tty: true, by: "claude", script: true,
		},
		{
			name:  "a native harness right above the same native harness is another session, so agent",
			goos:  Windows,
			chain: mk(p{name: "codex"}, p{name: "codex"}, p{name: "pwsh"}, p{name: "windowsterminal"}),
			class: event.ParentAgent, entry: event.EntrypointUnknown, tty: true, by: "codex",
		},
		{
			name:  "npm's node wrapper, titled as the harness, is its launcher and is skipped",
			goos:  Linux,
			chain: mk(p{name: "codex", pgid: 300, tty: TTYAttached}, p{name: "node", alias: "codex", pgid: 300}, p{name: "bash", pgid: 200}, p{name: "kitty", pgid: 100}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "kitty",
		},
		{
			name:  "a node that started the harness at once is its npm launcher and is skipped",
			goos:  Windows,
			chain: mk(p{name: "codex", start: 101}, p{name: "node", start: 100}, p{name: "cmd", start: 100}, p{name: "pwsh", start: 50}, p{name: "windowsterminal", start: 10}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "windowsterminal",
		},
		{
			name:  "a node that starts claude at once is a script, because Claude Code has no Node launcher",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 300, start: 101, tty: TTYAttached}, p{name: "node", pgid: 300, start: 100}, p{name: "bash", pgid: 200, start: 50}, p{name: "gnome-terminal-server", pgid: 100, start: 10}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "node",
		},
		{
			name:  "on Windows too a node just above claude is a script",
			goos:  Windows,
			chain: mk(p{name: "claude", start: 101}, p{name: "node", start: 100}, p{name: "pwsh", start: 50}, p{name: "windowsterminal", start: 10}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "node",
		},
		{
			name:  "a node that ran for a while before starting the harness is a script",
			goos:  Windows,
			chain: mk(p{name: "codex", start: 160}, p{name: "node", start: 100}, p{name: "pwsh", start: 50}, p{name: "windowsterminal", start: 10}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "node",
		},
		{
			name:  "a node whose parent is an IDE belongs to the IDE",
			goos:  Darwin,
			chain: mk(p{name: "claude", pgid: 100, start: 500, tty: TTYNone}, p{name: "node", pgid: 100, start: 100}, p{name: "zed", pgid: 100, start: 10}),
			class: event.ParentIDE, entry: event.EntrypointIDE, tty: false, by: "zed",
		},
		{
			name:  "tmux is a multiplexer",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 300, tty: TTYAttached}, p{name: "bash", pgid: 200}, p{name: "tmux", alias: "tmux: server", pgid: 100}),
			class: event.ParentMultiplexer, entry: event.EntrypointUnknown, tty: true, by: "tmux",
		},
		{
			name:  "sshd is remote",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 300, tty: TTYAttached}, p{name: "bash", pgid: 200}, p{name: "sshd-session", pgid: 100}, p{name: "sshd", pgid: 50}),
			class: event.ParentRemote, entry: event.EntrypointUnknown, tty: true, by: "sshd-session",
		},
		{
			name:  "cron running a script is scheduler, which says more than script_runtime",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 200, tty: TTYNone}, p{name: "sh", pgid: 200}, p{name: "cron", pgid: 100}),
			class: event.ParentScheduler, entry: event.EntrypointUnknown, tty: false, by: "cron", script: true,
		},
		{
			name:  "a LaunchAgent is scheduler",
			goos:  Darwin,
			chain: mk(p{name: "claude", pgid: 200, tty: TTYNone}, p{name: "launchd", pgid: 1}),
			class: event.ParentScheduler, entry: event.EntrypointUnknown, tty: false, by: "launchd",
		},
		{
			name:  "on Windows a harness explorer started has a console window of its own",
			goos:  Windows,
			chain: mk(p{name: "claude"}, p{name: "explorer"}),
			class: event.ParentUnknown, entry: event.EntrypointUnknown, tty: true, by: "explorer",
		},
		{
			name:  "Task Scheduler is scheduler and gives no console",
			goos:  Windows,
			chain: mk(p{name: "claude"}, p{name: "svchost"}, p{name: "services"}),
			class: event.ParentScheduler, entry: event.EntrypointUnknown, tty: false, by: "svchost",
		},
		{
			name:  "an agent desktop app gives entrypoint desktop and no automation class",
			goos:  Darwin,
			chain: mk(p{name: "claude", pgid: 100, tty: TTYNone}, p{name: "claude-desktop", pgid: 100}),
			class: event.ParentUnknown, entry: event.EntrypointDesktop, tty: false, by: "claude-desktop",
		},
		{
			name:  "login under macOS Terminal is a wrapper and is skipped",
			goos:  Darwin,
			chain: mk(p{name: "claude", pgid: 300, tty: TTYAttached}, p{name: "zsh", pgid: 200}, p{name: "login", pgid: 200}, p{name: "iTerm2", pgid: 100}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "iTerm2",
		},
		{
			name:  "login on a Linux text console is the terminal, not systemd's scheduler",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 300, tty: TTYAttached, tpgid: 300}, p{name: "bash", pgid: 200}, p{name: "login", pgid: 100}, p{name: "systemd", pgid: 1}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "login",
		},
		{
			name:  "a script's timeout puts claude in a group of its own, but the script holds the terminal, so it is still a script",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 500, tty: TTYAttached, tpgid: 400}, p{name: "timeout", pgid: 500}, p{name: "bash", pgid: 400}, p{name: "bash", pgid: 300}, p{name: "gnome-terminal-server", pgid: 100}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "gnome-terminal-server", script: true,
		},
		{
			name:  "the same script run in the background is a script too",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 500, tty: TTYAttached, tpgid: 300}, p{name: "timeout", pgid: 500}, p{name: "bash", pgid: 400}, p{name: "bash", pgid: 300}, p{name: "kitty", pgid: 100}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "kitty", script: true,
		},
		{
			name:  "on macOS a script's timeout is caught the same way",
			goos:  Darwin,
			chain: mk(p{name: "claude", pgid: 800, tty: TTYAttached, tpgid: 700}, p{name: "timeout", pgid: 800}, p{name: "bash", pgid: 700}, p{name: "zsh", pgid: 641}, p{name: "login", pgid: 640}, p{name: "Terminal", pgid: 612}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: true, by: "Terminal", script: true,
		},
		{
			name:  "timeout typed into an interactive shell leaves claude in the foreground, so it isn't a script",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 400, tty: TTYAttached, tpgid: 400}, p{name: "timeout", pgid: 400}, p{name: "bash", pgid: 300}, p{name: "kitty", pgid: 100}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "kitty",
		},
		{
			name:  "a harness sent to the background from the terminal's own shell isn't a script",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 400, tty: TTYAttached, tpgid: 300}, p{name: "bash", pgid: 300}, p{name: "kitty", pgid: 100}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "kitty",
		},
		{
			name:  "a harness started from Neovim's terminal is an editor session",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 500, tty: TTYAttached, tpgid: 500}, p{name: "zsh", pgid: 400}, p{name: "nvim", pgid: 300}, p{name: "zsh", pgid: 200}, p{name: "kitty", pgid: 100}),
			class: event.ParentIDE, entry: event.EntrypointIDE, tty: true, by: "nvim",
		},
		{
			name:  "a terminal written in Python is a terminal by its alias",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 300, tty: TTYAttached}, p{name: "bash", pgid: 200}, p{name: "python3.12", alias: "terminator", pgid: 100}),
			class: event.ParentTerminalApp, entry: event.EntrypointUnknown, tty: true, by: "python3.12",
		},
		{
			name:  "an unknown ancestor is unknown",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 300, tty: TTYAttached}, p{name: "zsh", pgid: 200}, p{name: "mystery-launcher", pgid: 100}),
			class: event.ParentUnknown, entry: event.EntrypointUnknown, tty: true, by: "mystery-launcher",
		},
		{
			name:  "a harness with no ancestors is unknown, decided by none",
			goos:  Linux,
			chain: mk(p{name: "claude", tty: TTYNone}),
			class: event.ParentUnknown, entry: event.EntrypointUnknown, tty: false, by: "",
		},
		{
			name:  "a chain of only shells running a script is script_runtime",
			goos:  Linux,
			chain: mk(p{name: "claude", pgid: 100, tty: TTYNone}, p{name: "bash", pgid: 100}),
			class: event.ParentScriptRuntime, entry: event.EntrypointUnknown, tty: false, by: "", script: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := Classify(tt.goos, tt.chain)
			by := ""
			if l.By >= 0 {
				by = tt.chain[l.By].Name
			}
			if l.ParentClass != tt.class || l.Entrypoint != tt.entry || l.HasTTY != tt.tty || by != tt.by || l.ScriptShell != tt.script {
				t.Errorf("Classify = %s, entrypoint %s, has_tty %v, by %q, script shell %v; want %s, %s, %v, %q, %v",
					l.ParentClass, l.Entrypoint, l.HasTTY, by, l.ScriptShell, tt.class, tt.entry, tt.tty, tt.by, tt.script)
			}
		})
	}
	t.Run("an empty chain is unknown", func(t *testing.T) {
		if l := Classify(Linux, nil); l.ParentClass != event.ParentUnknown || l.By != -1 {
			t.Errorf("Classify(nil) = %+v", l)
		}
	})
}

func TestHarnessOf(t *testing.T) {
	tests := []struct {
		name string
		goos string
		proc Proc
		want event.Harness
		ok   bool
	}{
		{"claude is Claude Code", Linux, Proc{Name: "claude"}, event.HarnessClaudeCode, true},
		{"claude.exe is Claude Code", Windows, Proc{Name: "claude.exe"}, event.HarnessClaudeCode, true},
		{"a Node app titled claude is Claude Code", Linux, Proc{Name: "node", Alias: "claude"}, event.HarnessClaudeCode, true},
		{"codex is Codex", Darwin, Proc{Name: "codex"}, event.HarnessCodex, true},
		{"the Cursor CLI is Cursor", Linux, Proc{Name: "cursor-agent"}, event.HarnessCursor, true},
		{"the Cursor CLI started through its agent link is Cursor", Linux, Proc{Name: "cursor-agent", Alias: "agent"}, event.HarnessCursor, true},
		{"Battle.net's Agent.exe isn't a harness", Windows, Proc{Name: "Agent.exe"}, "", false},
		{"the Devin CLI is Devin", Linux, Proc{Name: "devin"}, event.HarnessDevin, true},
		{"Devin Desktop isn't a harness process", Windows, Proc{Name: "devin-desktop"}, "", false},
		{"the Claude desktop app isn't a harness process", Windows, Proc{Name: "claude-desktop"}, "", false},
		{"a shell isn't a harness", Linux, Proc{Name: "bash"}, "", false},
		{"node alone isn't a harness", Linux, Proc{Name: "node"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := HarnessOf(tt.goos, tt.proc)
			if got != tt.want || ok != tt.ok {
				t.Errorf("HarnessOf(%+v) = %q, %v; want %q, %v", tt.proc, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestLauncherOf(t *testing.T) {
	tests := []struct {
		name          string
		goos          string
		parent, child Proc
		want          bool
	}{
		{"npm's wrapper titled codex is codex's launcher", Linux, Proc{Name: "node", Alias: "codex"}, Proc{Name: "codex"}, true},
		{"a plain node that started codex at once is its launcher", Windows, Proc{Name: "node", Start: at(100)}, Proc{Name: "codex", Start: at(101)}, true},
		{"a plain node that started codex a minute later is a script", Darwin, Proc{Name: "node", Start: at(100)}, Proc{Name: "codex", Start: at(160)}, false},
		{"a plain node whose start is unknown can't be told for a launcher", Windows, Proc{Name: "node"}, Proc{Name: "codex", Start: at(101)}, false},
		{"a plain node that started claude at once is a script, since claude has no Node launcher", Windows, Proc{Name: "node", Start: at(100)}, Proc{Name: "claude", Start: at(101)}, false},
		{"a native codex above codex is another session", Linux, Proc{Name: "codex", Start: at(100)}, Proc{Name: "codex", Start: at(101)}, false},
		{"a node titled as another harness isn't this one's launcher", Linux, Proc{Name: "node", Alias: "gemini"}, Proc{Name: "codex"}, false},
		{"a node above a program that isn't a harness is no launcher", Linux, Proc{Name: "node", Alias: "codex"}, Proc{Name: "bash"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LauncherOf(tt.goos, tt.parent, tt.child); got != tt.want {
				t.Errorf("LauncherOf(%+v, %+v) = %v, want %v", tt.parent, tt.child, got, tt.want)
			}
		})
	}
}

func TestHookHost(t *testing.T) {
	tests := []struct {
		name  string
		goos  string
		chain []Proc
		want  int
	}{
		{"an exec-form hook's host is its parent", Linux, mk(p{name: "pairtime"}, p{name: "claude"}, p{name: "zsh"}), 1},
		{"a shell-form hook's sh -c is skipped", Linux, mk(p{name: "pairtime"}, p{name: "sh"}, p{name: "claude"}), 2},
		{"Git Bash's launcher and its shell are skipped", Windows, mk(p{name: "pairtime"}, p{name: "bash"}, p{name: "bash"}, p{name: "claude"}), 3},
		{"a hook under only shells has no host", Linux, mk(p{name: "pairtime"}, p{name: "bash"}), -1},
		{"a hook with no parent has no host", Linux, mk(p{name: "pairtime"}), -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HookHost(tt.goos, tt.chain); got != tt.want {
				t.Errorf("HookHost = %d, want %d", got, tt.want)
			}
		})
	}
}
