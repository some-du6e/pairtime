// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Version is f.v, the version of the feature map this package writes.
const Version = 1

// Class is a prompt's class in SM 2.2's sense, as the writer computed it
// when it wrote the record, whatever origin the prompt later turns out to
// have (DESIGN 3.6's "prompt_class as computed"). SM 2.2 and pkg/score own
// the rule; CC8 recomputes it for challenged turns.
type Class string

// The prompt classes of SM 2.2, in its order.
const (
	Substantive Class = "substantive"
	LowEffort   Class = "low_effort"
	Answer      Class = "answer"
	ErrorPaste  Class = "error_paste"
)

// Valid reports whether c is one of the four classes.
func (c Class) Valid() bool {
	switch c {
	case Substantive, LowEffort, Answer, ErrorPaste:
		return true
	}
	return false
}

// F is a record's feature map, the body member "f", at version 1 (spec
// 10.7). encoding/json writes it as the writer stores it: the members in
// the order of the table, absent where it says, and no insignificant
// whitespace.
type F struct {
	// V is the version, 1.
	V int `json:"v"`
	// MH is the prompt's sketch (spec 10.5), on prompt.submit and
	// cli.track records that commit their text.
	MH *Sketch `json:"mh,omitempty"`
	// TH is the template value (spec 10.6), on prompt.submit and
	// cli.track records that commit their text.
	TH *Value `json:"th,omitempty"`
	// ES is the error signature value (spec 10.6), on prompt.submit and
	// cli.track records that commit their text, when the text has a
	// signature.
	ES *Value `json:"es,omitempty"`
	// PC is the prompt class, on every prompt.submit, cli.track and
	// relay.message.
	PC Class `json:"pc,omitempty"`
	// H are the edit hunks (spec 10.6), in the order the patch lists
	// them, on agent.tool when the tool edited a file.
	H []Hunk `json:"h,omitempty"`
	// CH is the command value (spec 10.6), on agent.tool and
	// shell.command when there is a command.
	CH *Value `json:"ch,omitempty"`
}

// The kinds of features each record kind carries.
const (
	carriesNone = iota
	carriesPrompt
	carriesTool
	carriesShell
	carriesRelay
)

func carries(kind event.Kind) int {
	switch kind {
	case event.KindPromptSubmit, event.KindCLITrack:
		return carriesPrompt
	case event.KindAgentTool:
		return carriesTool
	case event.KindShellCommand:
		return carriesShell
	case event.KindRelayMessage:
		return carriesRelay
	}
	return carriesNone
}

// Carries reports whether the body of every record of kind k has f:
// prompt.submit, agent.tool, shell.command, cli.track and relay.message,
// even when f holds only v, and no other kind (spec 4.5, 10.7). A body of
// one of those kinds without f fails verification.
func Carries(k event.Kind) bool { return carries(k) != carriesNone }

// Prompt returns the features of a prompt.submit or cli.track record whose
// committed text is text and whose class is class: the sketch, the
// template value, the error signature value when text has a signature, and
// the class (spec 10.7). cli.track carries them because DESIGN 5.4 classes
// a track anchor like a prompt from its text. text should be valid UTF-8,
// as every committed text is.
func (k *Key) Prompt(text string, class Class) (F, error) {
	if !class.Valid() {
		return F{}, fmt.Errorf("features: %q isn't a prompt class", class)
	}
	mh := k.Sketch(text)
	th := k.Template(text)
	f := F{V: Version, MH: &mh, TH: &th, PC: class}
	if es, ok := k.ErrorSig(text); ok {
		f.ES = &es
	}
	return f, nil
}

// Textless returns the features of a prompt.submit whose text the device
// never had: one from the WakaTime bridge, a plugin's that came without
// its text, or one marked guest, whose text the agent drops (spec 10.1).
// It holds only v and the class, and the record commits nothing.
func Textless(class Class) (F, error) {
	if !class.Valid() {
		return F{}, fmt.Errorf("features: %q isn't a prompt class", class)
	}
	return F{V: Version, PC: class}, nil
}

// Relay returns the features of a relay.message record: v and the class of
// the app's own text, which a relayed anchor takes its class from (SM 2.2
// rule 9). The record commits nothing, so its f never has a sketch (spec
// 10.7).
func Relay(class Class) (F, error) {
	if !class.Valid() {
		return F{}, fmt.Errorf("features: %q isn't a prompt class", class)
	}
	return F{V: Version, PC: class}, nil
}

// HasText reports whether f, a prompt's features, was computed from its
// text: it has a sketch, which a prompt record carries exactly when it
// commits its text (spec 10.7). A Textless map has none.
func (f F) HasText() bool { return f.MH != nil }

// Tool returns the features of an agent.tool record: its edit hunks, in
// the order the patch lists them, and the value of the command it ran,
// cmd, which is "" for a tool that ran none (spec 10.7). A read has
// neither, and its f holds only v.
func (k *Key) Tool(edits []Edit, cmd string) (F, error) {
	f := F{V: Version}
	for _, e := range edits {
		h, err := k.Hunk(e)
		if err != nil {
			return F{}, err
		}
		f.H = append(f.H, h)
	}
	if ch, ok := k.Command(cmd); ok {
		f.CH = &ch
	}
	return f, nil
}

// Shell returns the features of a shell.command record: the value of its
// command line, cmd, which is "" when the source gave none (spec 10.7).
func (k *Key) Shell(cmd string) F {
	f := F{V: Version}
	if ch, ok := k.Command(cmd); ok {
		f.CH = &ch
	}
	return f
}

// Errors from Parse. Test for them with errors.Is.
var (
	// ErrUnknown is an f whose v is above Version: a later writer's, whose
	// features this reader doesn't know and mustn't guess at (spec 10.7). A
	// server is never older than the clients writing to it, so for a server
	// it is a fault, and it refuses or holds the record.
	ErrUnknown = errors.New("features: f is of a version newer than this reader's")
	// ErrKind is a version 1 f that doesn't fit its record's kind: a
	// member on a kind that doesn't carry it, a member that kind always
	// carries missing, or an f on a kind that carries none.
	ErrKind = errors.New("features: f doesn't fit its record's kind")
	// ErrMalformed is an f that isn't a feature map of any version: no v, a
	// v that isn't an integer or is below 1, a null member, a member
	// version 1 doesn't list, or a value that breaks spec 10.6 or 10.7.
	ErrMalformed = errors.New("features: f is malformed")
)

// members are the members of spec 10.7's table, version 1.
var members = map[string]bool{"v": true, "mh": true, "th": true, "es": true, "pc": true, "h": true, "ch": true}

// hunkMembers are the members of a hunk (spec 10.6).
var hunkMembers = []string{"p", "os", "ol", "ns", "nl"}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// Parse reads raw, the "f" member of the body of a record of kind kind. It
// returns an error wrapping ErrUnknown for an f whose v is above Version:
// the record's features are unknown, and the caller mustn't guess at them
// (spec 10.7). An f of version 1 that doesn't fit kind wraps ErrKind, and
// anything else that isn't a version 1 feature map wraps ErrMalformed: no
// v, a v that isn't an integer or is below 1, a null member, a member
// version 1 doesn't list, or a hunk that isn't exactly spec 10.6's five
// members. Member names match exactly, never in another letter case, and
// the verdict doesn't depend on the order of the members.
func Parse(kind event.Kind, raw []byte) (F, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return F{}, fmt.Errorf("%w: not a JSON object", ErrMalformed)
	}
	rawV, ok := obj["v"]
	if !ok {
		return F{}, fmt.Errorf("%w: no v", ErrMalformed)
	}
	var v int64
	if isNull(rawV) || json.Unmarshal(rawV, &v) != nil {
		return F{}, fmt.Errorf("%w: v isn't an integer", ErrMalformed)
	}
	switch {
	case v < 1:
		return F{}, fmt.Errorf("%w: v is %d, and versions start at 1", ErrMalformed, v)
	case v > Version:
		return F{}, fmt.Errorf("%w: v is %d", ErrUnknown, v)
	}
	names := slices.Sorted(maps.Keys(obj))
	for _, name := range names {
		if !members[name] {
			return F{}, fmt.Errorf("%w: version 1 has no member %q", ErrMalformed, name)
		}
	}
	for _, name := range names {
		if isNull(obj[name]) {
			return F{}, fmt.Errorf("%w: %s is null", ErrMalformed, name)
		}
	}
	if h, ok := obj["h"]; ok {
		if err := checkHunks(h); err != nil {
			return F{}, err
		}
	}
	var f F
	if err := json.Unmarshal(raw, &f); err != nil {
		return F{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if err := f.fits(kind, obj); err != nil {
		return F{}, err
	}
	return f, nil
}

// checkHunks refuses an h that isn't a list of objects with exactly the
// members p, os, ol, ns and nl, none of them null (spec 10.6), so that a
// null element, a missing member or a member in another case can't become a
// hunk at line 0 of path value zero.
func checkHunks(raw json.RawMessage) error {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return fmt.Errorf("%w: h isn't a list", ErrMalformed)
	}
	for i, e := range elems {
		var obj map[string]json.RawMessage
		if isNull(e) || json.Unmarshal(e, &obj) != nil {
			return fmt.Errorf("%w: hunk %d isn't an object", ErrMalformed, i)
		}
		for _, name := range hunkMembers {
			if v, ok := obj[name]; !ok || isNull(v) {
				return fmt.Errorf("%w: hunk %d has no %s", ErrMalformed, i, name)
			}
		}
		if len(obj) != len(hunkMembers) {
			return fmt.Errorf("%w: hunk %d has a member besides p, os, ol, ns and nl", ErrMalformed, i)
		}
	}
	return nil
}

// fits checks a version 1 f against the kind of its record (spec 10.7).
// obj is f's members as parsed, which tell a member that is present from
// one that is absent.
func (f F) fits(kind event.Kind, obj map[string]json.RawMessage) error {
	has := func(name string) bool { _, ok := obj[name]; return ok }
	switch carries(kind) {
	case carriesPrompt:
		// With its text, a prompt's f has mh, th and pc, and es when the
		// text has a signature; without it, pc alone (spec 10.7).
		need := []string{"pc"}
		if has("mh") || has("th") || has("es") {
			need = []string{"mh", "th", "pc"}
		}
		for _, name := range need {
			if !has(name) {
				return fmt.Errorf("%w: %s f has no %s", ErrKind, kind, name)
			}
		}
		for _, name := range []string{"h", "ch"} {
			if has(name) {
				return fmt.Errorf("%w: %s f has %s", ErrKind, kind, name)
			}
		}
		if !f.PC.Valid() {
			return fmt.Errorf("%w: %q isn't a prompt class", ErrMalformed, f.PC)
		}
	case carriesRelay:
		// An app's message commits no text, so its f is v and pc alone
		// (spec 10.7).
		if !has("pc") {
			return fmt.Errorf("%w: %s f has no pc", ErrKind, kind)
		}
		for _, name := range []string{"mh", "th", "es", "h", "ch"} {
			if has(name) {
				return fmt.Errorf("%w: %s f has %s", ErrKind, kind, name)
			}
		}
		if !f.PC.Valid() {
			return fmt.Errorf("%w: %q isn't a prompt class", ErrMalformed, f.PC)
		}
	case carriesTool, carriesShell:
		names := []string{"mh", "th", "es", "pc"}
		if carries(kind) == carriesShell {
			names = append(names, "h")
		}
		for _, name := range names {
			if has(name) {
				return fmt.Errorf("%w: %s f has %s", ErrKind, kind, name)
			}
		}
		if has("h") && len(f.H) == 0 {
			return fmt.Errorf("%w: h is an empty list; a tool that edited nothing has no h", ErrMalformed)
		}
		for _, h := range f.H {
			if err := h.check(); err != nil {
				return fmt.Errorf("%w: %v", ErrMalformed, err)
			}
		}
	default:
		return fmt.Errorf("%w: %s records carry no f", ErrKind, kind)
	}
	return nil
}
