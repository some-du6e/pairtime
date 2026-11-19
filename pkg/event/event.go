// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// Kind names an event kind (SM 1.2).
type Kind string

const (
	KindSessionStart    Kind = "session.start"
	KindSessionEnd      Kind = "session.end"
	KindPromptSubmit    Kind = "prompt.submit"
	KindAgentOutput     Kind = "agent.output"
	KindAgentTool       Kind = "agent.tool"
	KindAgentPermission Kind = "agent.permission"
	KindAgentInterrupt  Kind = "agent.interrupt"
	KindAgentQueue      Kind = "agent.queue"
	KindAgentStop       Kind = "agent.stop"
	KindAgentSubagent   Kind = "agent.subagent"
	KindEditorActivity  Kind = "editor.activity"
	KindShellCommand    Kind = "shell.command"
	KindGitChange       Kind = "git.change"
	KindOSPresence      Kind = "os.presence"
	KindOSFocus         Kind = "os.focus"
	KindOSPower         Kind = "os.power"
	KindOTelActiveTime  Kind = "otel.active_time"
	KindCLITrack        Kind = "cli.track"
)

// kindTable lists every kind in SM 1.2 order with its payload type. It is
// the one place a new kind has to be registered in Go.
var kindTable = []struct {
	kind  Kind
	alloc func() Payload
}{
	{KindSessionStart, func() Payload { return new(SessionStart) }},
	{KindSessionEnd, func() Payload { return new(SessionEnd) }},
	{KindPromptSubmit, func() Payload { return new(PromptSubmit) }},
	{KindAgentOutput, func() Payload { return new(AgentOutput) }},
	{KindAgentTool, func() Payload { return new(AgentTool) }},
	{KindAgentPermission, func() Payload { return new(AgentPermission) }},
	{KindAgentInterrupt, func() Payload { return new(AgentInterrupt) }},
	{KindAgentQueue, func() Payload { return new(AgentQueue) }},
	{KindAgentStop, func() Payload { return new(AgentStop) }},
	{KindAgentSubagent, func() Payload { return new(AgentSubagent) }},
	{KindEditorActivity, func() Payload { return new(EditorActivity) }},
	{KindShellCommand, func() Payload { return new(ShellCommand) }},
	{KindGitChange, func() Payload { return new(GitChange) }},
	{KindOSPresence, func() Payload { return new(OSPresence) }},
	{KindOSFocus, func() Payload { return new(OSFocus) }},
	{KindOSPower, func() Payload { return new(OSPower) }},
	{KindOTelActiveTime, func() Payload { return new(OTelActiveTime) }},
	{KindCLITrack, func() Payload { return new(CLITrack) }},
}

var kinds = enum[Kind]{name: "kind", values: func() []Kind {
	ks := make([]Kind, len(kindTable))
	for i, k := range kindTable {
		ks[i] = k.kind
	}
	return ks
}()}

// Kinds returns every event kind, in SM 1.2 order.
func Kinds() []Kind { return slices.Clone(kinds.values) }

// newPayload returns a new zero payload for k, or nil if k is unknown.
func newPayload(k Kind) Payload {
	for _, entry := range kindTable {
		if entry.kind == k {
			return entry.alloc()
		}
	}
	return nil
}

// Payload is the kind-specific part of an event, held under "data". Each
// kind in SM 1.2 has one payload type in this package, always used as a
// pointer, and no other type can implement Payload.
type Payload interface {
	// Kind returns the kind the payload belongs to.
	Kind() Kind
	// Validate reports every rule the payload breaks, or nil.
	Validate() error
	check(c *checker)
}

// NoSession is the session_key of events that belong to no harness
// session: OS and git events (SM 1.1).
const NoSession = "none"

// Envelope holds the fields every event carries (SM 1.1).
type Envelope struct {
	// EventID is a UUIDv7 in canonical lowercase text form; see NewEventID.
	EventID string `json:"event_id"`
	// DeviceID is a random per-install id, never derived from hardware.
	DeviceID string `json:"device_id"`
	// Seq is the per-device sequence number, gapless from 1. The server
	// uses it for exactly-once ingest.
	Seq uint64 `json:"seq"`
	// T is the client's wall time, in Unix milliseconds UTC, when the
	// adapter saw the event.
	T int64 `json:"t"`
	// Mono is a monotonic clock reading in milliseconds taken at the same
	// moment, so the server can re-anchor T within one boot.
	Mono int64 `json:"mono"`
	// BootID is Linux's boot_id, or an id generated per boot elsewhere.
	BootID  string  `json:"boot_id"`
	Source  Source  `json:"source"`
	Harness Harness `json:"harness"`
	// HarnessVersion is the harness's version as it reports it.
	HarnessVersion string `json:"harness_version,omitempty"`
	// SessionKey is the harness session id, salted per device, or
	// NoSession for OS and git events.
	SessionKey string `json:"session_key"`
	// ProjectKey is the client's id for the project, resolved from the
	// working directory, the repo's root commits or a hashed folder path.
	// It is empty when no project applies, as for most OS events.
	ProjectKey string `json:"project_key,omitempty"`
	// ChainHash links this event to the previous one in the device's hash
	// chain (TP A3, SP 8.5).
	ChainHash Hash `json:"chain_hash"`
}

// Event is one normalized event: the envelope plus a kind-specific
// payload. Its kind is always Data.Kind(), so the two can't disagree.
type Event struct {
	Envelope
	Data Payload
}

// Kind returns the event's kind, or "" if it has no payload.
func (e Event) Kind() Kind {
	if isNil(e.Data) {
		return ""
	}
	return e.Data.Kind()
}

func isNil(p Payload) bool {
	if p == nil {
		return true
	}
	v := reflect.ValueOf(p)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// wireEvent is an Event as it appears in JSON.
type wireEvent struct {
	Envelope
	Kind Kind            `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// MarshalJSON writes the envelope fields, then "kind", then the payload
// under "data". It doesn't validate.
func (e Event) MarshalJSON() ([]byte, error) {
	if isNil(e.Data) {
		return nil, errors.New("event: can't marshal an event with no data")
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wireEvent{Envelope: e.Envelope, Kind: e.Data.Kind(), Data: data})
}

// UnmarshalJSON reads an event and decodes "data" into the payload type
// "kind" names. Unknown fields are ignored, as encoding/json does
// everywhere; the schema, which forbids them, is the stricter contract.
// It doesn't validate.
func (e *Event) UnmarshalJSON(b []byte) error {
	var w wireEvent
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	p := newPayload(w.Kind)
	if p == nil {
		return fmt.Errorf("event: unknown kind %q", w.Kind)
	}
	if len(w.Data) == 0 || string(w.Data) == "null" {
		return fmt.Errorf("event: %s event has no data", w.Kind)
	}
	if err := json.Unmarshal(w.Data, p); err != nil {
		return fmt.Errorf("event: %s data: %w", w.Kind, err)
	}
	e.Envelope = w.Envelope
	e.Data = p
	return nil
}

// Validate reports every rule e breaks (SM 1.1, 1.2), or nil. Errors name
// fields by JSON path, such as "data.typed_len_est". The schema in
// docs/schema/event.schema.json states the same rules.
func (e Event) Validate() error {
	c := &checker{}
	checkEnvelope(c, e.Envelope)
	if isNil(e.Data) {
		c.addf("data", "missing")
		return c.err()
	}
	checkKindRules(c, e.Envelope, e.Data.Kind())
	c.within("data", e.Data.check)
	return c.err()
}

func checkEnvelope(c *checker, e Envelope) {
	c.eventID("event_id", e.EventID)
	c.key("device_id", e.DeviceID)
	if e.Seq == 0 {
		c.addf("seq", "must be at least 1")
	}
	c.unixMS("t", e.T)
	if e.Mono < 0 {
		c.addf("mono", "must not be negative, got %d", e.Mono)
	}
	c.key("boot_id", e.BootID)
	checkEnum(c, "source", sources, e.Source)
	checkEnum(c, "harness", harnesses, e.Harness)
	c.text("harness_version", e.HarnessVersion, 64)
	c.key("session_key", e.SessionKey)
	c.optKey("project_key", e.ProjectKey)
}

// checkKindRules checks the envelope against what SM 1.2 says produces
// each kind: OS and git events have no harness or session, a few kinds
// come from one source only, and harness events belong to a session.
func checkKindRules(c *checker, e Envelope, k Kind) {
	switch k {
	case KindOSPresence, KindOSFocus, KindOSPower:
		mustSource(c, k, e.Source, SourceOS)
		noHarness(c, k, e)
	case KindGitChange:
		mustSource(c, k, e.Source, SourceGit)
		noHarness(c, k, e)
	case KindOTelActiveTime:
		mustSource(c, k, e.Source, SourceOTel)
	case KindCLITrack:
		mustSource(c, k, e.Source, SourceCLI, SourceMCP)
	case KindSessionStart, KindSessionEnd, KindPromptSubmit, KindAgentOutput, KindAgentTool,
		KindAgentPermission, KindAgentInterrupt, KindAgentQueue, KindAgentStop, KindAgentSubagent:
		if e.Harness == HarnessNone {
			c.addf("harness", "%s events come from a harness, got %q", k, e.Harness)
		}
		if e.SessionKey == NoSession {
			c.addf("session_key", "%s events belong to a harness session, got %q", k, e.SessionKey)
		}
	}
}

func mustSource(c *checker, k Kind, got Source, allowed ...Source) {
	if !slices.Contains(allowed, got) {
		c.addf("source", "%s events come from %q, got %q", k, allowed, got)
	}
}

func noHarness(c *checker, k Kind, e Envelope) {
	if e.Harness != HarnessNone {
		c.addf("harness", "must be %q for %s events, got %q", HarnessNone, k, e.Harness)
	}
	if e.SessionKey != NoSession {
		c.addf("session_key", "must be %q for %s events, got %q", NoSession, k, e.SessionKey)
	}
}

// Shareable returns a copy of e with every local field cleared. Local
// fields hold prompt text, sketches and hashes of it, command hashes and
// app hashes; SM 1.2 says they never leave the device, not even in the
// evidence for submitted hours. Call Shareable on anything about to leave
// the device. e itself is not modified.
func (e Event) Shareable() Event {
	if isNil(e.Data) {
		return e
	}
	src := reflect.ValueOf(e.Data).Elem()
	dst := reflect.New(src.Type())
	dst.Elem().Set(src)
	clearLocal(dst.Elem())
	e.Data = dst.Interface().(Payload)
	return e
}

// clearLocal zeroes the fields of struct v tagged pairtime:"local".
func clearLocal(v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Tag.Get("pairtime") == "local" {
			v.Field(i).SetZero()
		}
	}
}

// Hash is a SHA-256 digest. JSON and text forms are 64 lowercase hex
// digits.
type Hash [32]byte

func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// MarshalText implements encoding.TextMarshaler.
func (h Hash) MarshalText() ([]byte, error) {
	b := make([]byte, hex.EncodedLen(len(h)))
	hex.Encode(b, h[:])
	return b, nil
}

// UnmarshalText implements encoding.TextUnmarshaler. It accepts only the
// canonical form, 64 lowercase hex digits.
func (h *Hash) UnmarshalText(b []byte) error {
	if !isHex(string(b), 2*len(h)) {
		return fmt.Errorf("event: a hash must be %d lowercase hex digits, got %q", 2*len(h), b)
	}
	_, err := hex.Decode(h[:], b)
	return err
}

// NewEventID returns a new UUIDv7 (RFC 9562) whose timestamp is t: 48 bits
// of Unix milliseconds, then 74 random bits.
func NewEventID(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (40 - 8*i))
	}
	// crypto/rand.Read never returns an error as of Go 1.24.
	_, _ = rand.Read(b[6:])
	b[6] = b[6]&0x0f | 0x70 // version 7
	b[8] = b[8]&0x3f | 0x80 // variant 10
	var s [36]byte
	hex.Encode(s[0:8], b[0:4])
	s[8] = '-'
	hex.Encode(s[9:13], b[4:6])
	s[13] = '-'
	hex.Encode(s[14:18], b[6:8])
	s[18] = '-'
	hex.Encode(s[19:23], b[8:10])
	s[23] = '-'
	hex.Encode(s[24:36], b[10:16])
	return string(s[:])
}
