// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// RType is a record type, the header byte that says what a record holds
// (spec 4.2).
type RType uint8

// The record types that hold no event (spec 4.2). Codes 0x01 to 0x12 hold
// events, and RTypeOf gives each kind's code.
const (
	// RTypeCheckpoint is a scorer checkpoint: one project's serialized
	// score.ProjectState (DESIGN 3.6, 6.5).
	RTypeCheckpoint RType = 0x40
	// RTypeUploadFailure is the uploader's note of a failed upload
	// (DESIGN 3.6, 7.4).
	RTypeUploadFailure RType = 0x41
)

// eventRTypes is spec 4.2's table of event records: rtype 0x01 + i holds
// events of kind eventRTypes[i]. The 18 kinds took their codes in PR1's
// kindTable order, once. The table is frozen: a code never changes
// meaning, even if kindTable is reordered, and a kind added later takes
// the next free code from 0x13 up, appended here.
var eventRTypes = [...]event.Kind{
	event.KindSessionStart,    // 0x01
	event.KindSessionEnd,      // 0x02
	event.KindPromptSubmit,    // 0x03
	event.KindAgentOutput,     // 0x04
	event.KindAgentTool,       // 0x05
	event.KindAgentPermission, // 0x06
	event.KindAgentInterrupt,  // 0x07
	event.KindAgentQueue,      // 0x08
	event.KindAgentStop,       // 0x09
	event.KindAgentSubagent,   // 0x0a
	event.KindEditorActivity,  // 0x0b
	event.KindShellCommand,    // 0x0c
	event.KindGitChange,       // 0x0d
	event.KindOSPresence,      // 0x0e
	event.KindOSFocus,         // 0x0f
	event.KindOSPower,         // 0x10
	event.KindOTelActiveTime,  // 0x11
	event.KindCLITrack,        // 0x12
}

// RTypeOf returns the rtype of the records that hold events of kind k, or
// false for a kind the table doesn't list.
func RTypeOf(k event.Kind) (RType, bool) {
	for i, kind := range eventRTypes {
		if kind == k {
			return RType(i + 1), true
		}
	}
	return 0, false
}

// EventKind returns the kind of the events that records of type r hold,
// or false if r doesn't hold events.
func (r RType) EventKind() (event.Kind, bool) {
	if r >= 1 && int(r) <= len(eventRTypes) {
		return eventRTypes[r-1], true
	}
	return "", false
}

// Known reports whether r is in spec 4.2's table. A writer never emits
// any other code. A reader that meets one reports the record as unknown
// and can still check its batch, because roots and heads never read rtype.
func (r RType) Known() bool {
	_, ok := r.EventKind()
	return ok || r == RTypeCheckpoint || r == RTypeUploadFailure
}

// String returns the name of what r holds, such as "os.presence" or
// "checkpoint", or "rtype 0x13" for a code not in the table.
func (r RType) String() string {
	if k, ok := r.EventKind(); ok {
		return string(k)
	}
	switch r {
	case RTypeCheckpoint:
		return "checkpoint"
	case RTypeUploadFailure:
		return "upload_failure"
	}
	return fmt.Sprintf("rtype 0x%02x", uint8(r))
}

// osRecord reports whether r holds os.* events, whose records never have a
// project (spec 4.3).
func (r RType) osRecord() bool {
	k, _ := r.EventKind()
	return k == event.KindOSPresence || k == event.KindOSFocus || k == event.KindOSPower
}

// MaxKeyLen is the length limit of an opaque key. device_id, project_key
// and session_key are pkg/event opaque keys: 1 to MaxKeyLen ASCII
// characters, each a letter, a digit, '_' or '-' (spec section 1).
const MaxKeyLen = 128

func isKey(s string) bool {
	if len(s) == 0 || len(s) > MaxKeyLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// checkKey refuses s unless it is an opaque key. The error leaves s out: a
// string that fails is likely a path or other content, which stays out of
// error text.
func checkKey(field, s string) error {
	if !isKey(s) {
		return fmt.Errorf("ledger: %s isn't an opaque key of 1 to %d letters, digits, '_' or '-' (got %d bytes)", field, MaxKeyLen, len(s))
	}
	return nil
}

// Sizes of a header's project and session digests (spec 4.3).
const (
	ProjectDigestSize = 16
	SessionDigestSize = 8
)

// ProjectDigest returns a header's project field for projectKey: the first
// 16 bytes of the SHA-256 of its ASCII bytes, or zero(16) when projectKey
// is "", no project (spec 4.3).
func ProjectDigest(projectKey string) ([ProjectDigestSize]byte, error) {
	var d [ProjectDigestSize]byte
	if projectKey == "" {
		return d, nil
	}
	if err := checkKey("project_key", projectKey); err != nil {
		return d, err
	}
	h := sha256.Sum256([]byte(projectKey))
	copy(d[:], h[:])
	return d, nil
}

// SessionDigest returns a header's session field for sessionKey: the first
// 8 bytes of the SHA-256 of its ASCII bytes, or zero(8) for
// event.NoSession, the session "none" (spec 4.3).
func SessionDigest(sessionKey string) ([SessionDigestSize]byte, error) {
	var d [SessionDigestSize]byte
	if sessionKey == event.NoSession {
		return d, nil
	}
	if err := checkKey("session_key", sessionKey); err != nil {
		return d, err
	}
	h := sha256.Sum256([]byte(sessionKey))
	copy(d[:], h[:])
	return d, nil
}

// HeaderSize is the size of a record header (spec 4.3).
const HeaderSize = 8 + 1 + ProjectDigestSize + SessionDigestSize

// Offsets of a header's fields.
const (
	offRType   = 8
	offProject = offRType + 1
	offSession = offProject + ProjectDigestSize
)

// Header is a record's header, u64(s) || u8(rtype) || project || session
// (spec 4.3). A revealed batch shows the header of every record in it,
// and the accessors read the fields back from the bytes.
type Header [HeaderSize]byte

// NewHeader returns the header of record s, of type rtype, for projectKey
// ("" for no project) and sessionKey (event.NoSession for none). It
// refuses what Check refuses, and a key that isn't an opaque key.
func NewHeader(s uint64, rtype RType, projectKey, sessionKey string) (Header, error) {
	var h Header
	project, err := ProjectDigest(projectKey)
	if err != nil {
		return h, err
	}
	session, err := SessionDigest(sessionKey)
	if err != nil {
		return h, err
	}
	binary.BigEndian.PutUint64(h[:offRType], s)
	h[offRType] = byte(rtype)
	copy(h[offProject:offSession], project[:])
	copy(h[offSession:], session[:])
	if err := h.Check(); err != nil {
		return Header{}, err
	}
	return h, nil
}

// Seq returns the header's sequence number s.
func (h Header) Seq() uint64 { return binary.BigEndian.Uint64(h[:offRType]) }

// RType returns the header's record type.
func (h Header) RType() RType { return RType(h[offRType]) }

// Project returns the header's project digest, zero(16) for no project.
func (h Header) Project() [ProjectDigestSize]byte {
	var d [ProjectDigestSize]byte
	copy(d[:], h[offProject:offSession])
	return d
}

// Session returns the header's session digest, zero(8) for none.
func (h Header) Session() [SessionDigestSize]byte {
	var d [SessionDigestSize]byte
	copy(d[:], h[offSession:])
	return d
}

func (h Header) hasProject() bool { return h.Project() != [ProjectDigestSize]byte{} }
func (h Header) hasSession() bool { return h.Session() != [SessionDigestSize]byte{} }

// Check reports whether h follows spec 4.3: s is at least 1, the rtype is
// in the table, an os.* header has the zero project, a checkpoint has a
// project and no session, and an upload-failure note has neither. An rtype
// not in the table gives an error wrapping ErrUnknownRType, and every other
// broken rule one wrapping ErrHeader.
func (h Header) Check() error {
	r := h.RType()
	switch {
	case h.Seq() == 0:
		return fmt.Errorf("%w: sequence numbers start at 1", ErrHeader)
	case !r.Known():
		return fmt.Errorf("%w: %s", ErrUnknownRType, r)
	case r.osRecord() && h.hasProject():
		return fmt.Errorf("%w: %s records have no project", ErrHeader, r)
	case r == RTypeCheckpoint && (!h.hasProject() || h.hasSession()):
		return fmt.Errorf("%w: a checkpoint has a project and no session", ErrHeader)
	case r == RTypeUploadFailure && (h.hasProject() || h.hasSession()):
		return fmt.Errorf("%w: an upload-failure note has no project and no session", ErrHeader)
	}
	return nil
}

// EntrySize is the size of an entry (spec 4.6).
const EntrySize = HeaderSize + HashSize

// Entry is a record's entry, hdr || SHA-256(body) (spec 4.6), the data of
// the record's leaf in its batch tree. The body's hash stands in for the
// body until the body is revealed.
type Entry [EntrySize]byte

// NewEntry returns the entry of the record with header h and stored body.
func NewEntry(h Header, body []byte) Entry {
	var e Entry
	copy(e[:HeaderSize], h[:])
	d := sha256.Sum256(body)
	copy(e[HeaderSize:], d[:])
	return e
}

// Header returns the entry's header.
func (e Entry) Header() Header {
	var h Header
	copy(h[:], e[:HeaderSize])
	return h
}

// BodyHash returns the SHA-256 of the record's stored body.
func (e Entry) BodyHash() Hash {
	var d Hash
	copy(d[:], e[HeaderSize:])
	return d
}

// Record is a record as the writer stores it: its header and its body's
// bytes. Every hash covers the body as stored (spec 4.5).
type Record struct {
	Header Header
	Body   []byte
}

// Entry returns r's entry.
func (r Record) Entry() Entry { return NewEntry(r.Header, r.Body) }

// bodyPrefix is how every body starts: the "n" member, whose value is the
// nonce in hex (spec 4.5).
var bodyPrefix = []byte(`{"n":"`)

// Body is a record body (spec 4.5). The writer stores what Encode returns,
// which is encoding/json's output for Body: one object, no insignificant
// whitespace, the members in field order, "f" and "c" absent where the
// record has none.
type Body struct {
	// N is the record's nonce, written as 64 lowercase hex digits.
	N Hash `json:"n"`
	// E is the record. For an event it is Event.Shareable() as
	// Event.MarshalJSON writes it; for a checkpoint, the project's
	// serialized score.ProjectState (WP-SC5); for an upload-failure note,
	// the note (WP-AG5). It holds no text (DESIGN 3.6).
	E json.RawMessage `json:"e"`
	// F is the record's keyed scorer features, versioned by f.v (spec
	// 10.7): on every prompt.submit, agent.tool, shell.command and
	// cli.track record from version 1's writers on, and on no other. It
	// is kept as stored, because a reader that meets a version or a member
	// it doesn't know reports the features as unknown and doesn't guess;
	// Features reads it.
	F json.RawMessage `json:"f,omitempty"`
	// C is the record's content commitment (spec 10.2), on prompt.submit,
	// a final agent.output and cli.track from version 1's writers on.
	C *Commitment `json:"c,omitempty"`
}

// Encode returns b's bytes as the writer stores them. encoding/json
// compacts E and F on the way, so a record given with spaces or newlines
// is stored without them.
func (b Body) Encode() ([]byte, error) {
	if isNull(b.E) {
		return nil, fmt.Errorf("%w: no record", ErrBody)
	}
	if b.F != nil && isNull(b.F) {
		return nil, fmt.Errorf("%w: f is null", ErrBody)
	}
	out, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBody, err)
	}
	return out, nil
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// storedBody is a body as ParseBody first reads it, with "f" and "c" kept
// raw so that a null in either is refused rather than read as absent.
type storedBody struct {
	N Hash            `json:"n"`
	E json.RawMessage `json:"e"`
	F json.RawMessage `json:"f"`
	C json.RawMessage `json:"c"`
}

// ParseBody decodes a stored body. It refuses a body that doesn't start
// with the "n" member, a member that Body doesn't have, a missing or null
// record, a null "f" or "c", a "c" that isn't {"n": count, "r": root}, and
// anything after the object. It doesn't read "f", which Features does.
// Errors wrap ErrBody.
func ParseBody(data []byte) (Body, error) {
	if !bytes.HasPrefix(data, bodyPrefix) {
		return Body{}, fmt.Errorf("%w: doesn't start with %s", ErrBody, bodyPrefix)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var sb storedBody
	if err := dec.Decode(&sb); err != nil {
		return Body{}, fmt.Errorf("%w: %v", ErrBody, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Body{}, fmt.Errorf("%w: data after the object", ErrBody)
	}
	if isNull(sb.E) {
		return Body{}, fmt.Errorf("%w: no record", ErrBody)
	}
	b := Body{N: sb.N, E: sb.E, F: sb.F}
	if sb.F != nil && isNull(sb.F) {
		return Body{}, fmt.Errorf("%w: f is null", ErrBody)
	}
	if sb.C != nil {
		c, err := parseCommitment(sb.C)
		if err != nil {
			return Body{}, err
		}
		b.C = &c
	}
	return b, nil
}

// parseCommitment reads a body's "c" member, which must be exactly
// {"n": count, "r": root}.
func parseCommitment(raw json.RawMessage) (Commitment, error) {
	if isNull(raw) {
		return Commitment{}, fmt.Errorf("%w: c is null", ErrBody)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c struct {
		N *uint64 `json:"n"`
		R *Hash   `json:"r"`
	}
	if err := dec.Decode(&c); err != nil {
		return Commitment{}, fmt.Errorf("%w: c: %v", ErrBody, err)
	}
	if c.N == nil || c.R == nil {
		return Commitment{}, fmt.Errorf("%w: c is {\"n\": count, \"r\": root}", ErrBody)
	}
	return Commitment{N: *c.N, R: *c.R}, nil
}

// Features reads the body's feature map as the features of a record of
// type r (spec 10.7). present is false for a body without "f": a record of
// a kind that carries none, or one written before features existed. An f
// whose version or members the reader doesn't know gives an error wrapping
// features.ErrUnknown, which isn't a fault in the record: its features are
// unknown, and the caller mustn't guess at them.
func (b Body) Features(r RType) (f features.F, present bool, err error) {
	if len(b.F) == 0 {
		return features.F{}, false, nil
	}
	kind, _ := r.EventKind()
	f, err = features.Parse(kind, b.F)
	return f, true, err
}

// Event decodes the event that b, the body of an event record, holds.
func (b Body) Event() (event.Event, error) {
	var ev event.Event
	if err := json.Unmarshal(b.E, &ev); err != nil {
		return event.Event{}, err
	}
	return ev, nil
}

// Content is what spec section 10 adds to an event record's body: the text
// the record commits and the inputs of its keyed features. The text never
// enters the body; only its commitment and keyed values do. Each kind takes
// these fields and no others:
//
//   - prompt.submit: Text, the prompt as the harness delivered it, pasted
//     blocks expanded, and Class.
//   - agent.output: when IsFinal is set, Text, the turn's final agent
//     message, which EventRecord cuts to its first MessageMax bytes;
//     otherwise nothing.
//   - agent.tool: Edits, in the order the patch lists them, and Command,
//     the command line the tool ran.
//   - shell.command: Command, the command line as run.
//   - cli.track: Text, the label after pkg/redact (DESIGN 5.4), and Class.
//
// Every other kind takes none. Text must be valid UTF-8 (spec 10.1).
type Content struct {
	Text    []byte
	Class   features.Class
	Edits   []features.Edit
	Command string
}

// EventRecord returns the record that holds ev, whose sequence number is
// ev.Seq, with c, the content spec section 10 adds for ev's kind. The body
// holds ev.Shareable(), so none of ev's local fields reach the ledger
// (spec 4.5); its "f" holds the features of version 1, on every kind that
// carries them, even when f holds only v (spec 10.7); and its "c" commits
// the text of a prompt, a final agent message or a track label (spec
// 10.1, 10.2).
//
// EventRecord refuses an event that fails Validate, an os.* event with a
// project_key, which pkg/event doesn't refuse yet (spec 4.3, decision 6),
// content ev's kind doesn't take, text that isn't valid UTF-8, a prompt
// with no class, and an empty text for a prompt whose len_chars, or a
// final message whose words or code_lines, say it isn't empty.
func EventRecord(k *Keys, ev event.Event, c Content) (Record, error) {
	kind := ev.Kind()
	rtype, ok := RTypeOf(kind)
	if !ok {
		return Record{}, fmt.Errorf("ledger: no rtype for kind %q", kind)
	}
	if ev.Validate() != nil {
		// Validate's errors can quote a field's value, which may be
		// content, so this one doesn't carry them. Call Validate for them.
		return Record{}, fmt.Errorf("ledger: %s event %d fails event.Validate", kind, ev.Seq)
	}
	if rtype.osRecord() && ev.ProjectKey != "" {
		return Record{}, fmt.Errorf("ledger: %s events have no project_key", kind)
	}
	h, err := NewHeader(ev.Seq, rtype, ev.ProjectKey, ev.SessionKey)
	if err != nil {
		return Record{}, err
	}
	e, err := ev.Shareable().MarshalJSON()
	if err != nil {
		return Record{}, fmt.Errorf("ledger: %s event %d: %w", kind, ev.Seq, err)
	}
	b := Body{N: k.Nonce(h.Seq()), E: e}
	if b.F, b.C, err = k.content(ev, c); err != nil {
		return Record{}, fmt.Errorf("ledger: %s event %d: %w", kind, ev.Seq, err)
	}
	body, err := b.Encode()
	if err != nil {
		return Record{}, fmt.Errorf("ledger: %s record %d: %w", h.RType(), h.Seq(), err)
	}
	return Record{Header: h, Body: body}, nil
}

// content returns the "f" and "c" members of the body of the record that
// holds ev, a valid event, from c (spec 10.1, 10.7). The errors say which
// field is wrong and never quote the text.
func (k *Keys) content(ev event.Event, c Content) (json.RawMessage, *Commitment, error) {
	kind := ev.Kind()
	takes := func(text, class, edits, command bool) error {
		switch {
		case !text && len(c.Text) > 0:
			return fmt.Errorf("%s records commit no text", kind)
		case !class && c.Class != "":
			return fmt.Errorf("%s records have no prompt class", kind)
		case !edits && len(c.Edits) > 0:
			return fmt.Errorf("%s records have no edit hunks", kind)
		case !command && c.Command != "":
			return fmt.Errorf("%s records have no command", kind)
		}
		return nil
	}
	fk := k.Features()
	var f features.F
	var commit *Commitment
	switch p := ev.Data.(type) {
	case *event.PromptSubmit:
		if err := takes(true, true, false, false); err != nil {
			return nil, nil, err
		}
		if len(c.Text) == 0 && p.LenChars > 0 {
			return nil, nil, fmt.Errorf("the prompt's len_chars is %d, and its content has no text", p.LenChars)
		}
		cm, err := k.Commit(ev.Seq, c.Text)
		if err != nil {
			return nil, nil, err
		}
		commit = &cm
		if f, err = fk.Prompt(string(c.Text), c.Class); err != nil {
			return nil, nil, err
		}
	case *event.CLITrack:
		if err := takes(true, true, false, false); err != nil {
			return nil, nil, err
		}
		cm, err := k.Commit(ev.Seq, c.Text)
		if err != nil {
			return nil, nil, err
		}
		commit = &cm
		if f, err = fk.Prompt(string(c.Text), c.Class); err != nil {
			return nil, nil, err
		}
	case *event.AgentOutput:
		if !p.IsFinal {
			if err := takes(false, false, false, false); err != nil {
				return nil, nil, fmt.Errorf("%w; only a turn's final message does", err)
			}
			return nil, nil, nil
		}
		if err := takes(true, false, false, false); err != nil {
			return nil, nil, err
		}
		if len(c.Text) == 0 && (p.Words > 0 || p.CodeLines > 0) {
			return nil, nil, fmt.Errorf("the message has %d words and %d code lines, and its content has no text", p.Words, p.CodeLines)
		}
		cm, err := k.Commit(ev.Seq, CommittedMessage(c.Text))
		if err != nil {
			return nil, nil, err
		}
		return nil, &cm, nil
	case *event.AgentTool:
		if err := takes(false, false, true, true); err != nil {
			return nil, nil, err
		}
		var err error
		if f, err = fk.Tool(c.Edits, c.Command); err != nil {
			return nil, nil, err
		}
	case *event.ShellCommand:
		if err := takes(false, false, false, true); err != nil {
			return nil, nil, err
		}
		f = fk.Shell(c.Command)
	default:
		return nil, nil, takes(false, false, false, false)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, nil, err
	}
	return raw, commit, nil
}

// CheckpointRecord returns checkpoint record s of projectKey's scorer
// state, which is state: the project's serialized score.ProjectState
// (WP-SC5), keyed values only (DESIGN 6.5).
func CheckpointRecord(k *Keys, s uint64, projectKey string, state json.RawMessage) (Record, error) {
	h, err := NewHeader(s, RTypeCheckpoint, projectKey, event.NoSession)
	if err != nil {
		return Record{}, err
	}
	return newRecord(k, h, state)
}

// UploadFailureRecord returns upload-failure note s, which holds note
// (WP-AG5).
func UploadFailureRecord(k *Keys, s uint64, note json.RawMessage) (Record, error) {
	h, err := NewHeader(s, RTypeUploadFailure, "", event.NoSession)
	if err != nil {
		return Record{}, err
	}
	return newRecord(k, h, note)
}

func newRecord(k *Keys, h Header, e json.RawMessage) (Record, error) {
	body, err := Body{N: k.Nonce(h.Seq()), E: e}.Encode()
	if err != nil {
		return Record{}, fmt.Errorf("ledger: %s record %d: %w", h.RType(), h.Seq(), err)
	}
	return Record{Header: h, Body: body}, nil
}

// CheckBody reports whether body, the stored body of the record with
// header h, agrees with h (spec 4.5). It must parse, and an event record's
// body must hold an event of h's kind whose seq is h's s and whose
// project_key and session_key have h's digests. "f" may appear only on the
// kinds that carry features and "c" only on the kinds that commit a text,
// an agent.output's only when it is the turn's final message (spec 10.1,
// 10.7). A version 1 "f" must fit its kind; an "f" of a version or with a
// member version 1 doesn't define passes, since its features are unknown,
// not wrong. A checkpoint's or an upload-failure note's body only has to
// parse: how a score.ProjectState names its project is WP-SC5's to define.
// Errors wrap ErrBody, ErrBodyHeader or ErrUnknownRType.
//
// CheckBody doesn't check the nonce, which only the device can compute;
// Keys.CheckNonce does. Nor does it open "c": a reveal does (spec 10.3).
func CheckBody(h Header, body []byte) error {
	b, err := ParseBody(body)
	if err != nil {
		return err
	}
	r := h.RType()
	kind, isEvent := r.EventKind()
	if !isEvent {
		if !r.Known() {
			return fmt.Errorf("%w: %s", ErrUnknownRType, r)
		}
		if b.F != nil || b.C != nil {
			return fmt.Errorf("%w: %s records carry no f and no c", ErrBodyHeader, r)
		}
		return nil
	}
	ev, err := b.Event()
	if err != nil {
		return fmt.Errorf("%w: the header says %s, and the body holds no event: %v", ErrBodyHeader, kind, err)
	}
	if ev.Kind() != kind {
		return fmt.Errorf("%w: the header says %s, and the body holds %s", ErrBodyHeader, kind, ev.Kind())
	}
	if ev.Seq != h.Seq() {
		return fmt.Errorf("%w: the header says s = %d, and the event's seq is %d", ErrBodyHeader, h.Seq(), ev.Seq)
	}
	if d, err := ProjectDigest(ev.ProjectKey); err != nil || d != h.Project() {
		return fmt.Errorf("%w: the event's project_key doesn't have the header's project digest", ErrBodyHeader)
	}
	if d, err := SessionDigest(ev.SessionKey); err != nil || d != h.Session() {
		return fmt.Errorf("%w: the event's session_key doesn't have the header's session digest", ErrBodyHeader)
	}
	if b.F != nil {
		if !features.Carries(kind) {
			return fmt.Errorf("%w: %s records carry no f", ErrBodyHeader, kind)
		}
		switch _, err := features.Parse(kind, b.F); {
		case err == nil, errors.Is(err, features.ErrUnknown):
		case errors.Is(err, features.ErrKind):
			return fmt.Errorf("%w: %v", ErrBodyHeader, err)
		default:
			return fmt.Errorf("%w: %v", ErrBody, err)
		}
	}
	if b.C != nil {
		if !CommitsText(kind) {
			return fmt.Errorf("%w: %s records commit no text", ErrBodyHeader, kind)
		}
		if out, ok := ev.Data.(*event.AgentOutput); ok && !out.IsFinal {
			return fmt.Errorf("%w: only a turn's final agent message commits its text", ErrBodyHeader)
		}
	}
	return nil
}
