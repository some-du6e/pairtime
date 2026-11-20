// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"encoding/json"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Provenance is what the agent knows of where a hook call came from, by
// the rule in the package doc (D-spool-via).
type Provenance struct {
	// Via is the via of the call's events: event.ViaHook when the agent's
	// own walk reached a harness process, and event.ViaPlugin otherwise.
	Via event.Via
	// Harness is the harness whose process ran the hook: the one the walk
	// found, or, with no walk to go by, the one the sender's own chain
	// names, which is good for nothing but telling apart the copies of one
	// call that two harnesses make (DESIGN 5.1). It is "" when neither
	// names one, and may differ from what --harness said: Cursor runs
	// Claude Code's hooks too.
	Harness event.Harness
	// Walked says that the agent read the call's parent chain itself.
	Walked bool
	// Host is the harness process the walk found, the one that fired the
	// hook, which the process scan is told about (procs.Scanner.Claim). It
	// is set only when Via is hook.
	Host *platform.Proc
	// Lineage holds what SM 1.2 takes from the walk for the session: the
	// harness process's parent_class, entrypoint and has_tty. It is valid
	// only with Host.
	Lineage platform.Lineage
}

// Of says where the hook call d came from, on goos. d.Walk is the agent's
// own walk, which only the socket and the pipe give (ipc.Options.Walk).
//
// Only the walk can make the call's events via = hook, fill its session
// fields or name a process for the scan, and only when the process the
// hook runs under, the nearest ancestor that isn't a shell or a wrapper
// (platform.HookHost), is a harness. A call through the spool has no walk
// and gets via = plugin, whatever chain its line records; that chain may
// name the harness and nothing more.
//
// The generic hook mode will also count the hosts its mapping lists
// (DESIGN 5.3), once mappings are read (internal/adapters/generic). Until
// then a --map call is hook only under a harness pairtime knows.
func Of(goos string, d inbox.Delivery) Provenance {
	p := Provenance{Via: event.ViaPlugin}
	if d.Via == inbox.ViaInbox {
		p.Harness = named(goos, d.Chain)
		return p
	}
	if len(d.Walk) == 0 {
		return p
	}
	p.Walked = true
	i := platform.HookHost(goos, d.Walk)
	if i < 0 {
		return p
	}
	h, ok := platform.HarnessOf(goos, d.Walk[i])
	if !ok {
		return p
	}
	host := d.Walk[i]
	p.Via, p.Harness, p.Host = event.ViaHook, h, &host
	p.Lineage = platform.Classify(goos, d.Walk[i:])
	return p
}

// named returns the harness that a sender's own chain names as the process
// its hook ran under, going by names alone, as the chain carries nothing
// else that Of could use, or "".
func named(goos string, chain []inbox.Proc) event.Harness {
	if len(chain) == 0 {
		return ""
	}
	procs := make([]platform.Proc, len(chain))
	for i, c := range chain {
		procs[i] = platform.Proc{PID: c.PID, Name: c.Name, Start: c.Start}
	}
	i := platform.HookHost(goos, procs)
	if i < 0 {
		return ""
	}
	h, _ := platform.HarnessOf(goos, procs[i])
	return h
}

// Times returns when a hook call was made, which its events take as their
// t, mono and boot_id: the hook's own stamp, whichever way the call came.
// For a spooled call nothing else would do, because the agent drains the
// spool when it can, which may be hours later (Delivery.Received). Mono is 0
// and bootID "" when the sender's OS gave neither.
func Times(m inbox.Message) (t time.Time, mono int64, bootID string) {
	return m.Sent, m.Mono, m.BootID
}

// Generic lists the event names of the generic hook mode, which --map calls
// use (DESIGN 5.3), each with the kind that presence takes it as.
var Generic = []struct {
	Name string
	Kind event.Kind
}{
	{"prompt", event.KindPromptSubmit},
	{"stop", event.KindAgentStop},
	{"tool", event.KindAgentTool},
	{"permission", event.KindAgentPermission},
	{"session_start", event.KindSessionStart},
	{"session_end", event.KindSessionEnd},
}

// IsGeneric reports whether name is one of the generic hook mode's events.
func IsGeneric(name string) bool {
	for _, g := range Generic {
		if g.Name == name {
			return true
		}
	}
	return false
}

// native maps each harness's own event names, as the hooks pairtime
// installs pass them, to the kind that presence takes each as: DESIGN
// 5.1's hooks for Claude Code and Cursor. It is only what presence needs
// to start and end turns (presence.Sampler.Hook); the adapters, which
// read every field of every payload (WP-AD2), own the full mapping.
// Events not listed still count as activity in their session.
var native = map[event.Harness]map[string]event.Kind{
	event.HarnessClaudeCode: {
		"SessionStart":      event.KindSessionStart,
		"UserPromptSubmit":  event.KindPromptSubmit,
		"Stop":              event.KindAgentStop,
		"StopFailure":       event.KindAgentStop, // the turn ended on an API error (HS 3.1)
		"PermissionRequest": event.KindAgentPermission,
		"PostToolUse":       event.KindAgentTool,
		"SubagentStart":     event.KindAgentSubagent,
		"SubagentStop":      event.KindAgentSubagent,
		"SessionEnd":        event.KindSessionEnd,
	},
	event.HarnessCursor: {
		"sessionStart":       event.KindSessionStart,
		"beforeSubmitPrompt": event.KindPromptSubmit,
		"afterFileEdit":      event.KindAgentTool,
		"afterAgentResponse": event.KindAgentOutput,
		"stop":               event.KindAgentStop,
		"sessionEnd":         event.KindSessionEnd,
	},
}

// PresenceKind returns the kind presence takes the call b as, or "" for an
// event that only shows the session is active.
func PresenceKind(b Body) event.Kind {
	if b.Map != "" {
		for _, g := range Generic {
			if g.Name == b.Event {
				return g.Kind
			}
		}
		return ""
	}
	return native[event.Harness(b.Harness)][b.Event]
}

// SessionID returns the harness's own session id from a hook's payload, or
// "" when it names none: session_id, which Claude Code's hooks carry (HS
// 3.1), as do Claude-style hooks elsewhere; sessionId, the camelCase that
// Copilot's camelCase events use (HS 5.1); or conversation_id, which every
// Cursor hook carries (HS 4.1). It is the harness's id, not a key: whoever
// keeps it beyond the call keys it per device first (AGENTS.md).
func SessionID(b Body) string {
	if p := trimSpace(b.Payload); len(p) == 0 || p[0] != '{' {
		return ""
	}
	var ids struct {
		Snake        string `json:"session_id"`
		Camel        string `json:"sessionId"`
		Conversation string `json:"conversation_id"`
	}
	// A field of another type is an error, but leaves the others filled.
	_ = json.Unmarshal(b.Payload, &ids)
	switch {
	case ids.Snake != "":
		return ids.Snake
	case ids.Camel != "":
		return ids.Camel
	}
	return ids.Conversation
}
