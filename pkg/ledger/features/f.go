// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

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
	// cli.track.
	MH *Sketch `json:"mh,omitempty"`
	// TH is the template value (spec 10.6), on prompt.submit and
	// cli.track.
	TH *Value `json:"th,omitempty"`
	// ES is the error signature value (spec 10.6), on prompt.submit and
	// cli.track when the text has a signature.
	ES *Value `json:"es,omitempty"`
	// PC is the prompt class, on prompt.submit and cli.track.
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
)

func carries(kind event.Kind) int {
	switch kind {
	case event.KindPromptSubmit, event.KindCLITrack:
		return carriesPrompt
	case event.KindAgentTool:
		return carriesTool
	case event.KindShellCommand:
		return carriesShell
	}
	return carriesNone
}

// Carries reports whether a version 1 writer puts f in the body of records
// of kind k: prompt.submit, agent.tool, shell.command and cli.track, even
// when f holds only v, and no other kind (spec 10.7). A record of one of
// those kinds without f was written before features existed (WP-LG4),
// which isn't the same as a record that has none.
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
	// ErrUnknown is an f whose v isn't 1 or that holds a member spec
	// 10.7's table doesn't list. The record's features are unknown, and a
	// reader doesn't guess at them (spec 10.7).
	ErrUnknown = errors.New("features: f is of a version or holds a member version 1 doesn't define")
	// ErrKind is a version 1 f that doesn't fit its record's kind: a
	// member on a kind that doesn't carry it, a member that kind always
	// carries missing, or an f on a kind that carries none.
	ErrKind = errors.New("features: f doesn't fit its record's kind")
	// ErrMalformed is an f that isn't a feature map of any version, or a
	// version 1 f with a value that breaks spec 10.7.
	ErrMalformed = errors.New("features: f is malformed")
)

// members are the members of spec 10.7's table, version 1.
var members = map[string]bool{"v": true, "mh": true, "th": true, "es": true, "pc": true, "h": true, "ch": true}

// Parse reads raw, the "f" member of the body of a record of kind kind. It
// returns an error wrapping ErrUnknown for an f whose v isn't 1 or that
// holds a member version 1 doesn't list, which isn't a fault: the record's
// features are unknown, and the caller mustn't guess (spec 10.7). An f of
// version 1 that doesn't fit kind wraps ErrKind, and anything else that
// isn't a version 1 feature map wraps ErrMalformed.
func Parse(kind event.Kind, raw []byte) (F, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return F{}, fmt.Errorf("%w: not a JSON object", ErrMalformed)
	}
	rawV, ok := obj["v"]
	if !ok {
		return F{}, fmt.Errorf("%w: no v", ErrMalformed)
	}
	var v int
	if err := json.Unmarshal(rawV, &v); err != nil {
		return F{}, fmt.Errorf("%w: v isn't an integer", ErrMalformed)
	}
	if v != Version {
		return F{}, fmt.Errorf("%w: v is %d", ErrUnknown, v)
	}
	for name, val := range obj {
		if !members[name] {
			return F{}, fmt.Errorf("%w: member %q", ErrUnknown, name)
		}
		if bytes.Equal(bytes.TrimSpace(val), []byte("null")) {
			return F{}, fmt.Errorf("%w: %s is null", ErrMalformed, name)
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

// fits checks a version 1 f against the kind of its record (spec 10.7).
// obj is f's members as parsed, which tell a member that is present from
// one that is absent.
func (f F) fits(kind event.Kind, obj map[string]json.RawMessage) error {
	has := func(name string) bool { _, ok := obj[name]; return ok }
	switch carries(kind) {
	case carriesPrompt:
		for _, name := range []string{"mh", "th", "pc"} {
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
