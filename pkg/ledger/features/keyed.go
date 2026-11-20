// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// KeySize is the size of K_feat.
const KeySize = 32

// The tag bytes of spec 10.6's 16-byte keyed values, one per kind, so a
// command and an error signature with the same text get different values.
const (
	tagError    = 0x01
	tagTemplate = 0x02
	tagCommand  = 0x03
	tagPath     = 0x04
)

// Key holds K_feat, the device key that keys every feature value (spec 3).
// Only the device holds it, so no one else can compute a feature value,
// and nothing may log it. Its field is unexported, so encoding/json writes
// Key as {}, and Key formats as a placeholder under every fmt verb. Hold it
// by pointer, as NewKey returns it.
type Key struct {
	k [KeySize]byte
}

// NewKey returns the Key for K_feat. Package ledger's Keys.Features is how
// a device gets it from K_dev.
func NewKey(kFeat [KeySize]byte) *Key { return &Key{k: kFeat} }

// Format writes a placeholder whatever the verb, so printing or logging a
// Key never shows key material.
func (Key) Format(f fmt.State, _ rune) { io.WriteString(f, "features.Key{redacted}") }

func (k *Key) mac(tag byte, x string) [32]byte {
	m := hmac.New(sha256.New, k.k[:])
	m.Write([]byte{tag})
	io.WriteString(m, x)
	var out [32]byte
	m.Sum(out[:0])
	return out
}

// ShingleValue returns v(x) = HMAC-SHA256(K_feat, kind || x), the
// per-shingle value of shingle x, whose UTF-8 bytes are hashed (spec
// 10.5). It panics for a kind other than Word3 and Char5.
func (k *Key) ShingleValue(kind ShingleKind, x string) ShingleValue {
	if kind != Word3 && kind != Char5 {
		panic(fmt.Sprintf("features: %s isn't a shingle kind", kind))
	}
	return k.mac(byte(kind), x)
}

// Shingle is one shingle of a prompt with its per-shingle value.
type Shingle struct {
	X string
	V ShingleValue
}

// Shingles returns the kind and the shingles of text's normalized words,
// in ascending order of their UTF-8 bytes, each with its value.
func (k *Key) Shingles(text string) (ShingleKind, []Shingle) {
	kind, xs := Shingles(Normalize(text).Words)
	out := make([]Shingle, len(xs))
	for i, x := range xs {
		out[i] = Shingle{X: x, V: k.ShingleValue(kind, x)}
	}
	return kind, out
}

// Sketch returns the keyed MinHash of text: the sketch of its shingles'
// values (spec 10.5).
func (k *Key) Sketch(text string) Sketch {
	_, shingles := k.Shingles(text)
	vs := make([]ShingleValue, len(shingles))
	for i, s := range shingles {
		vs[i] = s.V
	}
	return SketchOf(vs)
}

// Value is a 16-byte keyed value, K16(tag, x) = HMAC-SHA256(K_feat, u8(tag)
// || x)[0:16] (spec 10.6). Its text form, which JSON uses, is 32 lowercase
// hex digits, which also makes it a valid pkg/event opaque key.
type Value [16]byte

func (k *Key) keyed16(tag byte, x string) Value {
	h := k.mac(tag, x)
	var v Value
	copy(v[:], h[:16])
	return v
}

// String returns v as 32 lowercase hex digits.
func (v Value) String() string { return hex.EncodeToString(v[:]) }

// MarshalText returns v as 32 lowercase hex digits.
func (v Value) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, v[:]), nil }

// UnmarshalText reads v from exactly 32 lowercase hex digits, the one form
// MarshalText writes.
func (v *Value) UnmarshalText(text []byte) error {
	b, err := decodeHex(text, len(v))
	if err != nil {
		return err
	}
	copy(v[:], b)
	return nil
}

// ErrorSig returns es, the keyed value of text's error signature (spec
// 10.6), and false when text has no signature. It is the one way pairtime
// keys an error signature: a prompt's goes in f.es, and an agent.tool's,
// taken from the tool's output, or a shell.command's, taken from the
// command's output, goes in the event's error_sig as Value.String. Using
// one function for all three lets the fix-it rule count a pasted error and
// the agent's test failure as one error (SM 4.2).
func (k *Key) ErrorSig(text string) (Value, bool) {
	sig, ok := ErrorSignature(text)
	if !ok {
		return Value{}, false
	}
	return k.keyed16(tagError, sig), true
}

// Template returns th, the keyed value of text's template, which is its
// normalized text (spec 10.6). Two prompts have the same th exactly when
// their normalized texts are identical. The empty text has a template too.
func (k *Key) Template(text string) Value {
	return k.keyed16(tagTemplate, Normalize(text).Text())
}

// Command returns ch, the keyed value of a command line (spec 10.6), with
// its whitespace collapsed as CommandText does, and false for a command
// that is empty or all whitespace, which has none.
func (k *Key) Command(cmd string) (Value, bool) {
	c := CommandText(cmd)
	if c == "" {
		return Value{}, false
	}
	return k.keyed16(tagCommand, c), true
}

// Path returns p, the keyed value of a file's path (spec 10.6). It refuses
// a path that isn't in the form CheckPath accepts.
func (k *Key) Path(path string) (Value, error) {
	if err := CheckPath(path); err != nil {
		return Value{}, err
	}
	return k.keyed16(tagPath, path), nil
}

// ErrPath is a file path that isn't in spec 10.6's form.
var ErrPath = errors.New("features: path isn't in spec 10.6's form")

// CheckPath reports whether path is in spec 10.6's form: relative to the
// root of its record's project, with '/' between components and never
// '\', no empty, '.' or '..' component and no trailing '/'; or, for a file
// outside the project, absolute from '/', or on Windows from an upper-case
// drive letter, ':' and '/'. A path that starts with a letter and ':' is
// always such a Windows path. The error leaves the path out: a path is
// content, which stays out of error text.
func CheckPath(path string) error {
	body := path
	switch {
	case len(path) >= 2 && isAlpha(path[0]) && path[1] == ':':
		body = ""
		if 'A' <= path[0] && path[0] <= 'Z' && len(path) >= 3 && path[2] == '/' {
			body = path[3:]
		}
	case strings.HasPrefix(path, "/"):
		body = path[1:]
	}
	if strings.Contains(path, `\`) || slices.ContainsFunc(strings.Split(body, "/"), func(part string) bool {
		return part == "" || part == "." || part == ".."
	}) {
		return fmt.Errorf("%w (%d bytes)", ErrPath, len(path))
	}
	return nil
}

// Edit is one edit hunk as an edit tool's patch reports it (Claude Code's
// structuredPatch, Codex's patch_apply_end, Cursor's afterFileEdit): the
// file's path, in the form CheckPath accepts, and the four numbers of its
// unified diff header, @@ -OldStart,OldLines +NewStart,NewLines @@.
type Edit struct {
	Path                                   string
	OldStart, OldLines, NewStart, NewLines int
}

// Hunk is an edit hunk as f carries it: the path's keyed value and the
// four numbers of the unified diff header (spec 10.6).
type Hunk struct {
	P  Value `json:"p"`
	OS int   `json:"os"`
	OL int   `json:"ol"`
	NS int   `json:"ns"`
	NL int   `json:"nl"`
}

// maxLine bounds each number of a hunk: from 0 to 2^31 - 1.
const maxLine = 1<<31 - 1

// check reports whether h's numbers follow spec 10.6: each from 0 to 2^31
// - 1, and, as in GNU diff, a range with lines starting at 1 or later.
func (h Hunk) check() error {
	for _, n := range [...]int{h.OS, h.OL, h.NS, h.NL} {
		if n < 0 || n > maxLine {
			return fmt.Errorf("features: a hunk number is %d, outside 0 to 2^31 - 1", n)
		}
	}
	if h.OL > 0 && h.OS == 0 || h.NL > 0 && h.NS == 0 {
		return errors.New("features: a hunk range with lines starts at line 1 or later")
	}
	return nil
}

// Hunk returns e as f carries it, keying its path. It refuses a path that
// CheckPath refuses and numbers that break spec 10.6.
func (k *Key) Hunk(e Edit) (Hunk, error) {
	h := Hunk{OS: e.OldStart, OL: e.OldLines, NS: e.NewStart, NL: e.NewLines}
	if err := h.check(); err != nil {
		return Hunk{}, err
	}
	p, err := k.Path(e.Path)
	if err != nil {
		return Hunk{}, err
	}
	h.P = p
	return h, nil
}
