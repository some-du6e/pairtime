// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

func TestOf(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	proc := func(pid, ppid int, name string, ago time.Duration) platform.Proc {
		return platform.Proc{PID: pid, PPID: ppid, Name: name, PGID: pid, TTY: platform.TTYAttached, TPGID: pid, Start: at.Add(-ago), Owner: "uid:1000"}
	}
	hook := proc(400, 300, "pairtime", 0)
	claude := proc(300, 200, "claude", time.Minute)
	cursor := proc(300, 200, "cursor-agent", time.Minute)
	bash := proc(200, 100, "bash", time.Hour)
	kitty := proc(100, 1, "kitty", 2*time.Hour)
	sh := proc(350, 300, "sh", 0)
	shHook := proc(400, 350, "pairtime", 0)
	// The chain a sender records, which only names processes.
	recorded := []inbox.Proc{{PID: 400, Name: "pairtime"}, {PID: 300, Name: "claude"}, {PID: 100, Name: "kitty"}}

	tests := []struct {
		name        string
		d           inbox.Delivery
		via         event.Via
		harness     event.Harness
		walked      bool
		host        int // the claimed process's pid, 0 for none
		parentClass event.ParentClass
	}{
		{
			name: "a call over the socket from under a harness is hook, and claims the harness",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{hook, claude, bash, kitty}},
			via:  event.ViaHook, harness: event.HarnessClaudeCode, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name: "a call over the pipe counts the same as one over the socket",
			d:    inbox.Delivery{Via: inbox.ViaPipe, PeerPID: 400, Walk: []platform.Proc{hook, claude, bash, kitty}},
			via:  event.ViaHook, harness: event.HarnessClaudeCode, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name: "a hook run through a shell under a harness is hook too",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{shHook, sh, claude, bash, kitty}},
			via:  event.ViaHook, harness: event.HarnessClaudeCode, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name: "the harness the walk finds wins over the one --harness names",
			d: inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{hook, cursor, bash, kitty},
				Message: inbox.Message{Kind: Kind, Body: json.RawMessage(`{"harness":"claude_code","event":"Stop","bytes":0}`)}},
			via: event.ViaHook, harness: event.HarnessCursor, walked: true, host: 300, parentClass: event.ParentTerminalApp,
		},
		{
			name: "a call over the socket from a terminal, with no harness under it, is plugin",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{proc(400, 200, "pairtime", 0), bash, kitty}},
			via:  event.ViaPlugin, walked: true,
		},
		{
			name: "a call over the socket that the agent couldn't walk is plugin",
			d:    inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400},
			via:  event.ViaPlugin,
		},
		{
			name: "a sender's own chain over the socket counts for nothing",
			d: inbox.Delivery{Via: inbox.ViaSocket, PeerPID: 400, Walk: []platform.Proc{proc(400, 200, "pairtime", 0), bash, kitty},
				Message: inbox.Message{Chain: recorded}},
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
			name: "a spooled call with no chain names no harness",
			d:    inbox.Delivery{Via: inbox.ViaInbox},
			via:  event.ViaPlugin,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Of(platform.Linux, tt.d)
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
	tests := []struct {
		name string
		d    inbox.Delivery
	}{
		{"a spooled call keeps the hook's own times, not the drain's", inbox.Delivery{Via: inbox.ViaInbox, Received: sent.Add(3 * time.Hour),
			Message: inbox.Message{Sent: sent, Mono: 5000, BootID: "b-1"}}},
		{"a call over the socket keeps the hook's own times too", inbox.Delivery{Via: inbox.ViaSocket, Received: sent.Add(time.Millisecond),
			Message: inbox.Message{Sent: sent, Mono: 5000, BootID: "b-1"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at, mono, boot := Times(tt.d.Message)
			if !at.Equal(sent) || mono != 5000 || boot != "b-1" {
				t.Errorf("Times = %v, %d, %q, want %v, 5000, b-1", at, mono, boot, sent)
			}
		})
	}
}
