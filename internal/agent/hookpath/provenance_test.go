// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

func TestOf(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	proc := func(pid, ppid int, name string, ago time.Duration) platform.Proc {
		return platform.Proc{PID: pid, PPID: ppid, Name: name, PGID: pid, TTY: platform.TTYAttached, TPGID: pid, Start: at.Add(-ago), Owner: "uid:1000"}
	}
	// ide is a process of an IDE's, which has no terminal.
	ide := func(pid, ppid int, name string, ago time.Duration) platform.Proc {
		p := proc(pid, ppid, name, ago)
		p.TTY, p.TPGID = platform.TTYNone, 0
		return p
	}
	hook := proc(400, 300, "pairtime", 0)
	claude := proc(300, 200, "claude", time.Minute)
	cursor := proc(300, 200, "cursor-agent", time.Minute)
	bash := proc(200, 100, "bash", time.Hour)
	kitty := proc(100, 1, "kitty", 2*time.Hour)
	sh := proc(350, 300, "sh", 0)
	shHook := proc(400, 350, "pairtime", 0)
	// git has the pid the hook had, which Windows gave it after the hook
	// exited: it started later, under claude.
	git := proc(400, 300, "git", -time.Second)
	// self is the entry a sender records of itself first in its chain.
	self := func(p platform.Proc) []inbox.Proc {
		return []inbox.Proc{{PID: p.PID, Name: p.Name, Start: p.Start, Ticks: p.Ticks}}
	}
	// The chain a sender records, which only names processes.
	recorded := []inbox.Proc{{PID: 400, Name: "pairtime"}, {PID: 300, Name: "claude"}, {PID: 100, Name: "kitty"}}
	// On Linux the readers give each process its start in ticks after
	// boot as well, and Start moves with every step of the wall clock.
	ticked := func(p platform.Proc, ticks uint64, moved time.Duration) platform.Proc {
		p.Ticks, p.Start = ticks, p.Start.Add(moved)
		return p
	}

	// The Cursor desktop app runs its hooks through PowerShell on Windows,
	// and through sh from its plugin helper on macOS; VS Code runs the
	// Copilot agent's from its own process (HS 4.1, 5).
	winHook := ide(900, 800, "pairtime", 0)
	powershell := ide(800, 700, "powershell", 0)
	cursorApp := ide(700, 600, "Cursor", time.Hour)
	explorer := ide(600, 4, "explorer", 3*time.Hour)
	codeHook := ide(900, 710, "pairtime", 0)
	codeApp := ide(710, 600, "Code", time.Hour)
	macHook := ide(900, 820, "pairtime", 0)
	macSh := ide(820, 810, "sh", 0)
	helper := ide(810, 700, "Cursor Helper (Plugin)", time.Hour)
	macCursor := ide(700, 1, "Cursor", 2*time.Hour)
	launchd := ide(1, 0, "launchd", 4*time.Hour)

	tests := []struct {
		name        string
		goos        string        // "" is Linux
		named       event.Harness // the call's --harness
		d           inbox.Delivery
		via         event.Via
		harness     event.Harness
		walked      bool
		host        int // the claimed process's pid, 0 for none
		parentClass event.ParentClass
	}{
		{
			name: "a call over the socket from under a harness is hook, and claims the harness",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{hook, claude, bash, kitty}, Message: inbox.Message{Chain: self(hook)}},
			via:  event.ViaHook, harness: event.HarnessClaudeCode, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name: "a call over the pipe counts the same as one over the socket",
			d:    inbox.Delivery{Via: inbox.ViaPipe, PeerPID: 400, Walk: []platform.Proc{hook, claude, bash, kitty}, Message: inbox.Message{Chain: self(hook)}},
			via:  event.ViaHook, harness: event.HarnessClaudeCode, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name: "a hook run through a shell under a harness is hook too",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{shHook, sh, claude, bash, kitty}, Message: inbox.Message{Chain: self(shHook)}},
			via:  event.ViaHook, harness: event.HarnessClaudeCode, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name:  "the harness the walk finds wins over the one --harness names",
			named: event.HarnessClaudeCode,
			d:     inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{hook, cursor, bash, kitty}, Message: inbox.Message{Chain: self(hook)}},
			via:   event.ViaHook, harness: event.HarnessCursor, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name: "a call over the socket from a terminal, with no harness under it, is plugin",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{proc(400, 200, "pairtime", 0), bash, kitty}, Message: inbox.Message{Chain: self(proc(400, 200, "pairtime", 0))}},
			via:  event.ViaPlugin, walked: true,
		},
		{
			name: "a call over the socket that the agent couldn't walk is plugin",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Message: inbox.Message{Chain: self(hook)}},
			via:  event.ViaPlugin,
		},
		{
			name: "the rest of a sender's own chain over the socket counts for nothing",
			d: inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{proc(400, 200, "pairtime", 0), bash, kitty},
				Message: inbox.Message{Chain: append(self(proc(400, 200, "pairtime", 0)), recorded[1:]...)}},
			via: event.ViaPlugin, walked: true,
		},
		{
			name: "a socket sender whose pid has passed to a newer process under the harness gets no walked chain",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{git, claude, bash, kitty}, Message: inbox.Message{Chain: self(hook)}},
			via:  event.ViaPlugin,
		},
		{
			name: "a sender that recorded no start time has no walked chain",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{hook, claude, bash, kitty}, Message: inbox.Message{Chain: []inbox.Proc{{PID: 400, Name: "pairtime"}}}},
			via:  event.ViaPlugin,
		},
		{
			name: "a sender that recorded no chain at all has no walked chain",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{hook, claude, bash, kitty}},
			via:  event.ViaPlugin,
		},
		{
			name: "on Linux the walk is matched by start ticks, which no step of the wall clock moves",
			d: inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{ticked(hook, 81163, time.Hour), claude, bash, kitty},
				Message: inbox.Message{Chain: self(ticked(hook, 81163, 0))}},
			via: event.ViaHook, harness: event.HarnessClaudeCode, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name: "on Linux a process with other start ticks isn't the sender, whatever its start says",
			d: inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{ticked(hook, 81170, 0), claude, bash, kitty},
				Message: inbox.Message{Chain: self(ticked(hook, 81163, 0))}},
			via: event.ViaPlugin,
		},
		{
			name: "a hook the Cursor desktop app runs through PowerShell on Windows is hook, hosted by Cursor",
			goos: platform.Windows, named: event.HarnessCursor,
			d:   inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 900, Walk: []platform.Proc{winHook, powershell, cursorApp, explorer}, Message: inbox.Message{Chain: self(winHook)}},
			via: event.ViaHook, harness: event.HarnessCursor, walked: true, host: 700, parentClass: event.ParentIDE,
		},
		{
			name: "a hook Cursor runs through sh from its plugin helper on macOS is hook, hosted by the helper",
			goos: platform.Darwin, named: event.HarnessCursor,
			d:   inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 900, Walk: []platform.Proc{macHook, macSh, helper, macCursor, launchd}, Message: inbox.Message{Chain: self(macHook)}},
			via: event.ViaHook, harness: event.HarnessCursor, walked: true, host: 810, parentClass: event.ParentIDE,
		},
		{
			name: "a Copilot hook that VS Code runs is hook, hosted by Code",
			goos: platform.Windows, named: event.HarnessCopilot,
			d:   inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 900, Walk: []platform.Proc{codeHook, codeApp, explorer}, Message: inbox.Message{Chain: self(codeHook)}},
			via: event.ViaHook, harness: event.HarnessCopilot, walked: true, host: 710, parentClass: event.ParentIDE,
		},
		{
			name: "an IDE hosts only the harnesses listed for it: Cursor doesn't run Claude Code's hooks as its own",
			goos: platform.Windows, named: event.HarnessClaudeCode,
			d:   inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 900, Walk: []platform.Proc{winHook, powershell, cursorApp, explorer}, Message: inbox.Message{Chain: self(winHook)}},
			via: event.ViaPlugin, walked: true,
		},
		{
			name: "VS Code isn't a host of Cursor's hooks",
			goos: platform.Windows, named: event.HarnessCursor,
			d:   inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 900, Walk: []platform.Proc{codeHook, codeApp, explorer}, Message: inbox.Message{Chain: self(codeHook)}},
			via: event.ViaPlugin, walked: true,
		},
		{
			name: "a spooled call is plugin whatever chain it records, which only names its harness",
			d:    inbox.Delivery{Via: inbox.ViaInbox, Message: inbox.Message{Chain: recorded}},
			via:  event.ViaPlugin, harness: event.HarnessClaudeCode,
		},
		{
			name: "a spooled call with a walk on it is still plugin, and claims nothing",
			d:    inbox.Delivery{Via: inbox.ViaInbox, Walk: []platform.Proc{hook, claude, bash, kitty}, Message: inbox.Message{Chain: recorded}},
			via:  event.ViaPlugin, harness: event.HarnessClaudeCode,
		},
		{
			name: "a spooled call from the Cursor desktop app names Cursor through its recorded chain, and is plugin",
			goos: platform.Windows, named: event.HarnessCursor,
			d: inbox.Delivery{Via: inbox.ViaInbox, Message: inbox.Message{Chain: []inbox.Proc{
				{PID: 900, Name: "pairtime"}, {PID: 800, Name: "powershell"}, {PID: 700, Name: "Cursor"}}}},
			via: event.ViaPlugin, harness: event.HarnessCursor,
		},
		{
			name: "a spooled call with no chain names no harness",
			d:    inbox.Delivery{Via: inbox.ViaInbox},
			via:  event.ViaPlugin,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goos := tt.goos
			if goos == "" {
				goos = platform.Linux
			}
			p := Of(goos, tt.d, tt.named)
			if p.Via != tt.via || p.Harness != tt.harness || p.Walked != tt.walked {
				t.Errorf("via %q, harness %q, walked %v; want %q, %q, %v", p.Via, p.Harness, p.Walked, tt.via, tt.harness, tt.walked)
			}
			switch {
			case tt.host == 0 && p.Host != nil:
				t.Errorf("claims process %d, want none", p.Host.PID)
			case tt.host != 0 && (p.Host == nil || p.Host.PID != tt.host):
				t.Errorf("claims %+v, want process %d", p.Host, tt.host)
			case tt.host != 0 && p.Lineage.ParentClass != tt.parentClass:
				t.Errorf("parent_class %q, want %q", p.Lineage.ParentClass, tt.parentClass)
			}
		})
	}
}

func TestPresenceKind(t *testing.T) {
	tests := []struct {
		name string
		b    Body
		want event.Kind
	}{
		{"Claude Code's UserPromptSubmit starts a turn as a prompt", Body{Harness: "claude_code", Event: "UserPromptSubmit"}, event.KindPromptSubmit},
		{"Claude Code's Stop ends the turn", Body{Harness: "claude_code", Event: "Stop"}, event.KindAgentStop},
		{"Claude Code's StopFailure ends the turn too", Body{Harness: "claude_code", Event: "StopFailure"}, event.KindAgentStop},
		{"Claude Code's PostToolUse is a tool call", Body{Harness: "claude_code", Event: "PostToolUse"}, event.KindAgentTool},
		{"Claude Code's PermissionRequest is a permission request", Body{Harness: "claude_code", Event: "PermissionRequest"}, event.KindAgentPermission},
		{"Claude Code's SessionEnd ends the session", Body{Harness: "claude_code", Event: "SessionEnd"}, event.KindSessionEnd},
		{"Claude Code's Notification only shows activity", Body{Harness: "claude_code", Event: "Notification"}, ""},
		{"Cursor's beforeSubmitPrompt is a prompt", Body{Harness: "cursor", Event: "beforeSubmitPrompt"}, event.KindPromptSubmit},
		{"a generic event name is read only with --map", Body{Harness: "zed", Event: "prompt"}, ""},
		{"with --map the generic names count", Body{Harness: "zed", Event: "prompt", Map: "zed.toml"}, event.KindPromptSubmit},
		{"with --map a native name doesn't", Body{Harness: "claude_code", Event: "UserPromptSubmit", Map: "zed.toml"}, ""},
		{"an unknown harness only shows activity", Body{Harness: "warp", Event: "UserPromptSubmit"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PresenceKind(tt.b); got != tt.want {
				t.Errorf("PresenceKind = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenericNames(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"prompt", true}, {"stop", true}, {"tool", true}, {"permission", true}, {"session_start", true}, {"session_end", true},
		{"UserPromptSubmit", false}, {"", false},
	}
	for _, tt := range tests {
		t.Run("DESIGN 5.3 lists "+tt.name+" as a generic event: "+map[bool]string{true: "yes", false: "no"}[tt.want], func(t *testing.T) {
			if got := IsGeneric(tt.name); got != tt.want {
				t.Errorf("IsGeneric(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestSessionID(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"Claude Code's session_id", `{"session_id":"8f2c","hook_event_name":"Stop"}`, "8f2c"},
		{"Copilot's camelCase sessionId", `{"sessionId":"c-1","timestamp":1795046500000}`, "c-1"},
		{"Cursor's conversation_id", `{"conversation_id":"conv-9","generation_id":"g"}`, "conv-9"},
		{"session_id wins over conversation_id", `{"conversation_id":"conv-9","session_id":"s-2"}`, "s-2"},
		{"a session_id that isn't a string is passed over", `{"session_id":7,"conversation_id":"conv-9"}`, "conv-9"},
		{"a payload without one names none", `{"prompt":"hi"}`, ""},
		{"a payload that isn't an object names none", `["session_id"]`, ""},
		{"no payload names none", ``, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionID(Body{Payload: json.RawMessage(tt.payload)}); got != tt.want {
				t.Errorf("SessionID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTimes(t *testing.T) {
	sent := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	hookStamp := clock.Stamp{Wall: sent, Mono: 5000, BootID: "b-1"}
	call := func(s clock.Stamp) inbox.Message { return inbox.Message{Sent: s.Wall, Mono: s.Mono, BootID: s.BootID} }
	tests := []struct {
		name string
		d    inbox.Delivery
		want clock.Stamp
	}{
		{
			name: "a spooled call keeps the hook's own times, not the drain's",
			d:    inbox.Delivery{Via: inbox.ViaInbox, Message: call(hookStamp), Received: clock.Stamp{Wall: sent.Add(3 * time.Hour), Mono: 5000 + 3*3600000, BootID: "b-1"}},
			want: hookStamp,
		},
		{
			name: "a call over the socket keeps the hook's own times too",
			d:    inbox.Delivery{Via: inbox.ViaSocket, Message: call(hookStamp), Received: clock.Stamp{Wall: sent.Add(time.Millisecond), Mono: 5001, BootID: "b-1"}},
			want: hookStamp,
		},
		{
			name: "a spooled line stamped after its drain gets the drain's stamps",
			d:    inbox.Delivery{Via: inbox.ViaInbox, Message: call(clock.Stamp{Wall: sent.Add(time.Hour), Mono: 5000, BootID: "b-0"}), Received: clock.Stamp{Wall: sent, Mono: 7000, BootID: "b-1"}},
			want: clock.Stamp{Wall: sent, Mono: 7000, BootID: "b-1"},
		},
		{
			name: "a socket frame stamped ahead of its arrival gets the agent's stamp from then",
			d:    inbox.Delivery{Via: inbox.ViaSocket, Message: call(clock.Stamp{Wall: sent.Add(2 * time.Second), Mono: 5000, BootID: "b-1"}), Received: clock.Stamp{Wall: sent, Mono: 7000, BootID: "b-1"}},
			want: clock.Stamp{Wall: sent, Mono: 7000, BootID: "b-1"},
		},
		{
			name: "a monotonic reading ahead of the agent's in the same boot gets the agent's stamp",
			d:    inbox.Delivery{Via: inbox.ViaSocket, Message: call(clock.Stamp{Wall: sent, Mono: 9000, BootID: "b-1"}), Received: clock.Stamp{Wall: sent.Add(time.Millisecond), Mono: 7000, BootID: "b-1"}},
			want: clock.Stamp{Wall: sent.Add(time.Millisecond), Mono: 7000, BootID: "b-1"},
		},
		{
			name: "a monotonic reading from another boot isn't held to the agent's",
			d:    inbox.Delivery{Via: inbox.ViaInbox, Message: call(clock.Stamp{Wall: sent, Mono: 90000000, BootID: "b-0"}), Received: clock.Stamp{Wall: sent.Add(time.Hour), Mono: 7000, BootID: "b-1"}},
			want: clock.Stamp{Wall: sent, Mono: 90000000, BootID: "b-0"},
		},
		{
			name: "a call that gave no monotonic reading gets the agent's stamp",
			d:    inbox.Delivery{Via: inbox.ViaInbox, Message: call(clock.Stamp{Wall: sent}), Received: clock.Stamp{Wall: sent.Add(time.Minute), Mono: 7000, BootID: "b-1"}},
			want: clock.Stamp{Wall: sent.Add(time.Minute), Mono: 7000, BootID: "b-1"},
		},
		{
			name: "a delivery the agent didn't stamp keeps the hook's times",
			d:    inbox.Delivery{Via: inbox.ViaSocket, Message: call(hookStamp)},
			want: hookStamp,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Times(tt.d); !got.Wall.Equal(tt.want.Wall) || got.Mono != tt.want.Mono || got.BootID != tt.want.BootID {
				t.Errorf("Times = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSessionKey(t *testing.T) {
	kDev := make([]byte, 32)
	for i := range kDev {
		kDev[i] = byte(i)
	}
	secret, err := SessionSecret(kDev)
	if err != nil {
		t.Fatal(err)
	}
	// The vectors come from Python's hmac and hashlib, which compute HKDF
	// and the key from their definitions, without this code.
	const wantSecret = "855b9afd265e093c942665364b4fe5313bf2864c775c60f244ec0d0eb9442a70"
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"K_session is HKDF-SHA256 of K_dev with the label pairtime/v1/session", func(t *testing.T) {
			if got := hex.EncodeToString(secret); got != wantSecret {
				t.Errorf("K_session = %s, want %s", got, wantSecret)
			}
		}},
		{"a Claude Code session id gives the key Python computes", func(t *testing.T) {
			if got := SessionKey(secret, "8f2c3e9a-1b7d-4c0e-9a51-2f6d8e4b7c10"); got != "sd77b3e28506bc6b7a5ea2f3555cc7cb6" {
				t.Errorf("SessionKey = %s", got)
			}
		}},
		{"a Cursor conversation id gives the key Python computes", func(t *testing.T) {
			if got := SessionKey(secret, "conv-9"); got != "s14923cc0b10a4b25eafba9eb2883bcc9" {
				t.Errorf("SessionKey = %s", got)
			}
		}},
		{"a session with no id has no key", func(t *testing.T) {
			if got := SessionKey(secret, ""); got != "" {
				t.Errorf("SessionKey = %q, want none", got)
			}
		}},
		{"another device's key gives another session key", func(t *testing.T) {
			other := append([]byte(nil), kDev...)
			other[0] ^= 1
			s2, err := SessionSecret(other)
			if err != nil {
				t.Fatal(err)
			}
			if SessionKey(s2, "conv-9") == SessionKey(secret, "conv-9") {
				t.Error("two devices give one session the same key")
			}
		}},
		{"a session key is one an os.focus record takes", func(t *testing.T) {
			f := event.OSFocus{AppClass: event.AppTerminal, HostedSessions: []string{SessionKey(secret, "conv-9")}}
			if err := f.Validate(); err != nil {
				t.Errorf("os.focus refuses the key: %v", err)
			}
		}},
		{"K_dev must be 32 bytes", func(t *testing.T) {
			if _, err := SessionSecret(kDev[:31]); err == nil {
				t.Error("SessionSecret took a 31-byte K_dev")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}
