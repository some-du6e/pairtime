// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Provenance is what the agent knows of where a hook call came from, by
// the rules in the package doc (DESIGN 3.4, 5.2).
type Provenance struct {
	// Via is the via of the call's events: event.ViaHook when the agent's
	// own walk reached a harness process, or a host of the harness the call
	// names, and event.ViaPlugin otherwise.
	Via event.Via
	// Harness is the harness whose process ran the hook: the one the walk
	// found, or, with no walk to go by, the one the sender's own chain
	// names, which is good for nothing but telling apart the copies of one
	// call that two harnesses make (DESIGN 5.1). It is "" when neither
	// names one, and may differ from what --harness said: Cursor runs
	// Claude Code's hooks too.
	Harness event.Harness
	// Walked says that the agent's own walk of the call's parent chain
	// counts: the agent read the chain itself, and the chain's first
	// process is the sender that recorded itself in the message. A walk
	// that doesn't count is as if there were none.
	Walked bool
	// Host is the process the hook ran under, the one that fired the hook:
	// the harness process, or the IDE process that hosts the harness. The
	// process scan is told about it (procs.Scanner.Claim), and the focus
	// component hosts the call's session in it. It is set only when Via is
	// hook.
	Host *platform.Proc
	// Lineage holds what SM 1.2 takes from the walk for the session: the
	// harness process's parent_class, entrypoint and has_tty. It is valid
	// only with Host.
	Lineage platform.Lineage
}

// Of says where the hook call d came from, on goos. d.Walk is the agent's
// own walk, which only the socket and the pipe give (ipc.Options.Walk), and
// named is the harness the call itself names, its --harness (Body.Harness).
//
// Only the walk can make the call's events via = hook, fill its session
// fields or name a process for the scan, and only when it counts: its
// first process has the pid and start time that the sender recorded first
// in its chain (DESIGN 3.4). The process the hook runs under, the nearest
// ancestor that isn't a shell or a wrapper (platform.HookHost), must then
// be a harness, or an IDE or agent desktop app that runs the hooks of the
// harness named, as Hosts lists them. A call through the spool has no walk
// and gets via = plugin, whatever chain its line records; that chain may
// name the harness and nothing more (DESIGN 5.2).
//
// The generic hook mode will also count the hosts its mapping lists
// (DESIGN 5.3), once mappings are read (internal/adapters/generic). Until
// then a --map call is hook only under a harness pairtime knows, or one
// of Hosts.
func Of(goos string, d inbox.Delivery, named event.Harness) Provenance {
	p := Provenance{Via: event.ViaPlugin}
	if d.Via == inbox.ViaInbox {
		p.Harness = recorded(goos, d.Chain, named)
		return p
	}
	if !counts(d) {
		return p
	}
	p.Walked = true
	i := platform.HookHost(goos, d.Walk)
	if i < 0 {
		return p
	}
	host := d.Walk[i]
	if h, ok := platform.HarnessOf(goos, host); ok {
		p.Via, p.Harness, p.Host = event.ViaHook, h, &host
		p.Lineage = platform.Classify(goos, d.Walk[i:])
		return p
	}
	if !hosts(goos, named, host) {
		return p
	}
	// The session runs inside the IDE, so the IDE is the ancestor that
	// decides its parent_class: Classify reads the chain from the process
	// below it.
	p.Via, p.Harness, p.Host = event.ViaHook, named, &host
	p.Lineage = platform.Classify(goos, d.Walk[i-1:])
	return p
}

// counts reports whether the agent's own walk of the call d counts: its
// first process is the one the sender recorded first in its chain, by pid
// and by start (platform.Proc.ID), so the walk is of the sender itself. A
// sender that doesn't wait for its ack can exit before the agent walks it,
// and Windows reuses pids soon, so without the check a walk could vouch for
// the parents of whatever process had the pid next. A sender that recorded
// no start time counts for nothing.
func counts(d inbox.Delivery) bool {
	if len(d.Walk) == 0 || len(d.Chain) == 0 {
		return false
	}
	want := d.Chain[0].ID()
	return want.Start != 0 && d.Walk[0].ID() == want
}

// Hosts lists, for each harness whose hooks an IDE or an agent desktop app
// runs itself, the processes that host its sessions there: Cursor's desktop
// app runs hook commands itself, through PowerShell on Windows (HS 4.1), VS
// Code runs those of the Copilot agent in it (HS 5), and Devin Desktop, once
// Windsurf, runs Devin Local's, which reads Claude Code's hooks too (HS 8).
// It stands in for DESIGN 3.4's Adapter.Hosts until the adapters exist
// (WP-AD2). Names are compared without case and without ".exe", and one
// ending in "*" matches every name that starts with the rest, as in the
// app-class table, whose role for the process must also be ide or desktop.
// No name is listed for two harnesses.
var Hosts = map[event.Harness][]string{
	event.HarnessCursor:  {"cursor", "cursor helper*"},
	event.HarnessCopilot: {"code", "code helper*"},
	event.HarnessDevin: {"devin-desktop", "devin desktop", "windsurf", "devin helper*", "devin desktop helper*",
		"windsurf helper*"},
}

// HostedBy returns the harness whose hooks p, an IDE or agent desktop
// process, runs as their host by Hosts, or "" when Hosts lists p for none.
// Of counts such a process only for the harness the call names. This names
// it for the copies of a call that another harness's host made, as when
// Cursor's desktop app runs Claude Code's hooks (HS 4.1), which that
// harness's adapter records (DESIGN 5.1).
func HostedBy(goos string, p platform.Proc) event.Harness {
	hs := make([]event.Harness, 0, len(Hosts))
	for h := range Hosts {
		hs = append(hs, h)
	}
	slices.Sort(hs)
	for _, h := range hs {
		if hosts(goos, h, p) {
			return h
		}
	}
	return ""
}

// hosts reports whether p, the process a hook runs under, is an IDE or
// agent desktop process that Hosts lists for harness h.
func hosts(goos string, h event.Harness, p platform.Proc) bool {
	names := Hosts[h]
	if len(names) == 0 {
		return false
	}
	if e, ok := platform.Identify(goos, p); !ok || (e.Role != platform.RoleIDE && e.Role != platform.RoleDesktop) {
		return false
	}
	for _, n := range []string{p.Alias, p.Name} {
		n = strings.ToLower(strings.TrimSpace(n))
		n = strings.TrimSuffix(n, ".exe")
		if n == "" {
			continue
		}
		for _, want := range names {
			if prefix, ok := strings.CutSuffix(want, "*"); ok && strings.HasPrefix(n, prefix) || n == want {
				return true
			}
		}
	}
	return false
}

// recorded returns the harness that a sender's own chain names as the one
// its hook ran under, going by names alone, as the chain carries nothing
// else that Of could use: a harness process, or a host of the harness the
// call named (Hosts). It is "" when the chain names neither.
func recorded(goos string, chain []inbox.Proc, named event.Harness) event.Harness {
	if len(chain) == 0 {
		return ""
	}
	procs := make([]platform.Proc, len(chain))
	for i, c := range chain {
		procs[i] = platform.Proc{PID: c.PID, Name: c.Name, Start: c.Start, Ticks: c.Ticks}
	}
	i := platform.HookHost(goos, procs)
	if i < 0 {
		return ""
	}
	if h, ok := platform.HarnessOf(goos, procs[i]); ok {
		return h
	}
	if hosts(goos, named, procs[i]) {
		return named
	}
	return ""
}

// Times returns when the hook call d was made, the stamp its events take as
// their t, mono and boot_id (SM 1). That is the hook's own stamp, the
// message's Sent, Mono and BootID, whichever way the call came, because a
// drain can read a spooled call hours after it was made. But no call is
// timed later than the agent took it in (DESIGN 5.2). Times gives the
// agent's own stamp from then, d.Received, instead, when the hook's is
// later than it by the wall clock, or by the monotonic clock within one
// boot, and when the hook gave no monotonic reading, without which its
// time can't be placed within a boot (DESIGN 7.4). Any process of this
// user can append to the spool, and one under a harness can send over the
// socket, with whatever stamp it likes. A delivery without the agent's
// stamp, which neither the socket nor a drain makes, keeps the hook's.
func Times(d inbox.Delivery) clock.Stamp {
	sent := clock.Stamp{Wall: d.Sent, Mono: d.Mono, BootID: d.BootID}
	r := d.Received
	switch {
	case r.Wall.IsZero():
		return sent
	case !sent.HasMono(), sent.Wall.After(r.Wall), sent.BootID == r.BootID && sent.Mono > r.Mono:
		return r
	}
	return sent
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

// SessionSecretLabel is the HKDF label of K_session, the secret session
// keys are keyed with. Like the git adapter's project secret and focus's
// K_app, it is derived from the device key K_dev under a "pairtime/v1/"
// label of its own, which no other key uses (docs/spec/ledger-v1.md
// section 3).
const SessionSecretLabel = "pairtime/v1/session"

// SessionSecretLen is the length of K_session, in bytes.
const SessionSecretLen = 32

// SessionSecret derives K_session from K_dev the way ledger-v1.md section 3
// derives the ledger's keys: HKDF-SHA256 with RFC 5869's default salt of
// 32 zero bytes, info SessionSecretLabel, and 32 bytes of output.
func SessionSecret(kDev []byte) ([]byte, error) {
	if len(kDev) != 32 {
		return nil, errors.New("K_dev must be 32 bytes")
	}
	return hkdf.Key(sha256.New, kDev, nil, SessionSecretLabel, SessionSecretLen)
}

// sessionDomain is hashed first, so a session key can't equal another
// keyed value made under the same secret.
const sessionDomain = "pairtime/v1/session/id\x00"

// SessionKey is the session_key of the harness session whose own id is id
// (SessionID): "s" and the first 16 bytes of HMAC-SHA256(K_session,
// sessionDomain || id), in hex. SM 1 has a session_key as the harness's
// session id salted per device, and an HMAC under a secret derived from
// K_dev is that salt, which a reader without the device's key can't
// recompute from a guess of the id (AGENTS.md). The key depends on the id
// alone, so the copies of one session's calls that two harnesses make
// (DESIGN 5.1) have one key. The events of a hook call's session carry it
// (WP-AD2), and the focus component lists it in hosted_sessions (WP-OS2).
// It is "" when id is.
func SessionKey(secret []byte, id string) string {
	if id == "" {
		return ""
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(sessionDomain))
	m.Write([]byte(id))
	return "s" + hex.EncodeToString(m.Sum(nil)[:16])
}
