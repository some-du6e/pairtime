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

// kindRule says what may produce events of a kind (SM 1.2) and how the
// agent may receive them (DESIGN 4.1 E-2).
type kindRule struct {
	// sources, when set, are the only sources the kind comes from.
	sources []Source
	// vias, when set, are the only ways the agent receives the kind.
	vias []Via
	// noSession kinds come from no harness: harness and session_key are
	// both "none".
	noSession bool
	// inSession kinds belong to a harness session: neither harness nor
	// session_key may be "none".
	inSession bool
}

var (
	fromOS      = kindRule{sources: []Source{SourceOS}, vias: []Via{ViaOS}, noSession: true}
	fromHarness = kindRule{inSession: true}
	fromAnyone  = kindRule{}
)

// kindTable lists every kind in SM 1.2 order with its payload type and
// rule. It is the one place a new kind has to be registered in Go. The
// ledger numbers record types in this order (DESIGN 3.6), so a new kind
// goes at the end.
var kindTable = []struct {
	kind  Kind
	alloc func() Payload
	rule  kindRule
}{
	{KindSessionStart, func() Payload { return new(SessionStart) }, fromHarness},
	{KindSessionEnd, func() Payload { return new(SessionEnd) }, fromHarness},
	{KindPromptSubmit, func() Payload { return new(PromptSubmit) }, fromHarness},
	{KindAgentOutput, func() Payload { return new(AgentOutput) }, fromHarness},
	{KindAgentTool, func() Payload { return new(AgentTool) }, fromHarness},
	{KindAgentPermission, func() Payload { return new(AgentPermission) }, fromHarness},
	{KindAgentInterrupt, func() Payload { return new(AgentInterrupt) }, fromHarness},
	{KindAgentQueue, func() Payload { return new(AgentQueue) }, fromHarness},
	{KindAgentStop, func() Payload { return new(AgentStop) }, fromHarness},
	{KindAgentSubagent, func() Payload { return new(AgentSubagent) }, fromHarness},
	{KindEditorActivity, func() Payload { return new(EditorActivity) }, fromAnyone},
	{KindShellCommand, func() Payload { return new(ShellCommand) }, fromAnyone},
	{KindGitChange, func() Payload { return new(GitChange) }, kindRule{sources: []Source{SourceGit}, vias: []Via{ViaGit}, noSession: true}},
	{KindOSPresence, func() Payload { return new(OSPresence) }, fromOS},
	{KindOSFocus, func() Payload { return new(OSFocus) }, fromOS},
	{KindOSPower, func() Payload { return new(OSPower) }, fromOS},
	{KindOTelActiveTime, func() Payload { return new(OTelActiveTime) }, kindRule{sources: []Source{SourceOTel}, vias: []Via{ViaOTel}}},
	{KindCLITrack, func() Payload { return new(CLITrack) }, kindRule{sources: []Source{SourceCLI, SourceMCP}, vias: []Via{ViaTrack}}},
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

// ownPayload reports whether p is the payload type this package defines
// for p's kind, and not, say, a struct that embeds it.
func ownPayload(p Payload) bool {
	want := newPayload(p.Kind())
	return want != nil && reflect.TypeOf(p) == reflect.TypeOf(want)
}

// ruleFor returns the rule for k. An unknown kind gets the rule with no
// restrictions; Validate never reaches it, since only known kinds decode.
func ruleFor(k Kind) kindRule {
	for _, entry := range kindTable {
		if entry.kind == k {
			return entry.rule
		}
	}
	return fromAnyone
}

// Payload is the kind-specific part of an event, held under "data". Each
// kind in SM 1.2 has one payload type in this package, always used as a
// pointer. A type in another package can still satisfy Payload by
// embedding one, so Validate rejects any type but the kind's own, and
// Shareable refuses to copy one.
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

// Envelope holds the fields every event carries: SM 1.1's, without
// chain_hash and with via (DESIGN 4.1 E-1, E-2). The ledger's Merkle
// batches and batch chain replace the per-event hash chain (DESIGN 3.6),
// because a linear chain would force revealing every event in a window.
type Envelope struct {
	// EventID is a UUIDv7 in canonical lowercase text form; see NewEventID.
	EventID string `json:"event_id"`
	// DeviceID is a random per-install id, never derived from hardware.
	DeviceID string `json:"device_id"`
	// Seq is the s of the device's ledger record that holds the event
	// (docs/spec/ledger-v1.md 4.1). One sequence numbers every ledger
	// record from 1 with no gaps, scorer checkpoints and upload-failure
	// notes included, so the events' own seq values skip the numbers
	// those records hold. The server uses it for exactly-once ingest. The
	// agent assigns it when it writes the event, so whatever an adapter or
	// plugin sends is overwritten.
	Seq uint64 `json:"seq"`
	// T is the client's wall time, in Unix milliseconds UTC, when the
	// adapter saw the event.
	T int64 `json:"t"`
	// Mono is a monotonic clock reading in milliseconds taken at the same
	// moment, so the server can re-anchor T within one boot.
	Mono int64 `json:"mono"`
	// BootID is Linux's boot_id, or an id generated per boot elsewhere.
	BootID string `json:"boot_id"`
	// Source is what the record claims produced it.
	Source Source `json:"source"`
	// Via is how the agent received it, set by the agent; see [Via].
	Via     Via     `json:"via"`
	Harness Harness `json:"harness"`
	// HarnessVersion is the harness's version as it reports it, when that
	// is a version in IsVersion's form. An adapter whose harness reports
	// none, or reports something else, leaves it empty (D-version-grammar).
	HarnessVersion string `json:"harness_version,omitempty"`
	// SessionKey is the harness session id, salted per device, or
	// NoSession for OS and git events.
	SessionKey string `json:"session_key"`
	// ProjectKey is the client's id for the project, resolved from the
	// working directory, the repo's root commits or a hashed folder path.
	// It is empty when no project applies, as for most OS events.
	ProjectKey string `json:"project_key,omitempty"`
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
// docs/schema/event.schema.json states the same rules for each field and
// kind, but not the ones that compare two fields, such as typed_len_est =
// len_chars - pasted_chars or t_end not before t_start; only Validate
// checks those. The schema's "integer" also accepts 1e3 and 12.0, which
// encoding/json refuses for an int field, so integers must be written as
// integer literals.
func (e Event) Validate() error {
	c := &checker{}
	checkEnvelope(c, e.Envelope)
	if isNil(e.Data) {
		c.addf("data", "missing")
		return c.err()
	}
	if !ownPayload(e.Data) {
		c.addf("data", "%T is not the payload type for %s", e.Data, e.Data.Kind())
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
	checkEnum(c, "via", vias, e.Via)
	checkEnum(c, "harness", harnesses, e.Harness)
	if e.HarnessVersion != "" && !IsVersion(e.HarnessVersion) {
		c.addf("harness_version", "must be a version such as 2.1.327, or absent (D-version-grammar)")
	}
	c.key("session_key", e.SessionKey)
	c.optKey("project_key", e.ProjectKey)
}

// checkKindRules checks the envelope against the kind's rule: OS and git
// events have no harness or session, a few kinds come from one source
// and arrive one way only, and harness events belong to a session.
func checkKindRules(c *checker, e Envelope, k Kind) {
	r := ruleFor(k)
	if r.sources != nil && !slices.Contains(r.sources, e.Source) {
		c.addf("source", "%s events come from %q, got %q", k, r.sources, e.Source)
	}
	if r.vias != nil && !slices.Contains(r.vias, e.Via) {
		c.addf("via", "%s events arrive via %q, got %q", k, r.vias, e.Via)
	}
	if r.noSession {
		if e.Harness != HarnessNone {
			c.addf("harness", "must be %q for %s events, got %q", HarnessNone, k, e.Harness)
		}
		if e.SessionKey != NoSession {
			c.addf("session_key", "must be %q for %s events, got %q", NoSession, k, e.SessionKey)
		}
	}
	if r.inSession {
		if e.Harness == HarnessNone {
			c.addf("harness", "%s events come from a harness, got %q", k, e.Harness)
		}
		if e.SessionKey == NoSession {
			c.addf("session_key", "%s events belong to a harness session, got %q", k, e.SessionKey)
		}
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
	if !ownPayload(e.Data) {
		// clearLocal can't see fields a foreign type adds. Fail closed:
		// with no data, MarshalJSON refuses to encode the event.
		e.Data = nil
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
