// SPDX-License-Identifier: AGPL-3.0-or-later

// Package jsonc reads JSON that may hold comments and trailing commas, and
// edits it in place. An edit changes the bytes it has to and leaves every
// other byte as it was: comments, indentation, key order, line endings and
// a byte order mark. Harness settings files are written by people and by
// other tools, and pairtime's installer adds its entries to them without
// reformatting anything it doesn't own (DESIGN 5.1).
//
// Parse accepts JSON (RFC 8259) plus // and /* */ comments and a comma
// after the last member or element, which is what VS Code-style settings
// files allow. A // comment ends at a line feed or a carriage return, as
// VS Code's scanner ends it. Edits keep a strict JSON file strict: they add no comment,
// and a trailing comma only where the container already ends with one.
package jsonc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Kind is the type of a JSON value.
type Kind uint8

// The kinds of value.
const (
	Object Kind = iota + 1
	Array
	String
	Number
	Bool
	Null
)

// Value is one parsed value and where it sits in the source.
type Value struct {
	Kind Kind
	// Start is the offset of the value's first byte, and End the offset
	// just past its last one.
	Start, End int
	// Items are an object's members or an array's elements, in source
	// order.
	Items []Item
	// Text is a string's decoded value, a number as written, or "true",
	// "false" or "null".
	Text string
}

// Item is one member of an object or one element of an array.
type Item struct {
	// Start is the offset of a member's name or of an element.
	Start int
	// Key is a member's decoded name, and "" for an element.
	Key   string
	Value *Value
}

// ErrEmpty means the source holds no value: it is empty, or only white
// space and comments.
var ErrEmpty = errors.New("no JSON value")

// SyntaxError says where the source stops being JSON with comments.
type SyntaxError struct {
	Line, Column int // 1-based; the column counts bytes
	Msg          string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

// maxDepth bounds nesting, so a hostile file can't use up the stack.
const maxDepth = 512

// Parse parses src. A UTF-8 byte order mark at the start is skipped; a
// UTF-16 one is an error, because the edits write UTF-8.
func Parse(src []byte) (*Value, error) {
	if bytes.HasPrefix(src, []byte{0xFF, 0xFE}) || bytes.HasPrefix(src, []byte{0xFE, 0xFF}) {
		return nil, &SyntaxError{Line: 1, Column: 1, Msg: "the file is UTF-16, and only UTF-8 can be edited"}
	}
	p := &parser{src: src, pos: BOMLen(src)}
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.pos >= len(src) {
		return nil, ErrEmpty
	}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.pos < len(src) {
		return nil, p.errorf("unexpected %s after the end of the value", p.describe())
	}
	return v, nil
}

// BOMLen returns 3 if src starts with a UTF-8 byte order mark, and 0
// otherwise.
func BOMLen(src []byte) int {
	if bytes.HasPrefix(src, []byte{0xEF, 0xBB, 0xBF}) {
		return 3
	}
	return 0
}

// Comments returns where src's comments are: each span runs from a
// comment's first slash to just past its end, which for a // comment is the
// end of its line, without the line break. src must parse.
func Comments(src []byte) [][2]int {
	var spans [][2]int
	for i := BOMLen(src); i < len(src); {
		switch {
		case src[i] == '"':
			for i++; i < len(src) && src[i] != '"'; i++ {
				if src[i] == '\\' {
					i++
				}
			}
			i++
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			end := lineCommentEnd(src, i)
			spans = append(spans, [2]int{i, end})
			i = end
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			end := len(src)
			if n := bytes.Index(src[i+2:], []byte("*/")); n >= 0 {
				end = i + 2 + n + 2
			}
			spans = append(spans, [2]int{i, end})
			i = end
		default:
			i++
		}
	}
	return spans
}

// lineCommentEnd returns where the // comment that starts at i ends: at the
// first line break after it, "\n" or a carriage return, or at the end of
// src. A carriage return ends one on its own, as JavaScript and VS Code's
// JSONC scanner read it, so a file saved with old Mac line endings parses
// as other readers parse it; the "\r" of a CRLF ends it too.
func lineCommentEnd(src []byte, i int) int {
	if n := bytes.IndexAny(src[i:], "\r\n"); n >= 0 {
		return i + n
	}
	return len(src)
}

// Get returns the value of v's last member named key, which is the one
// JSON.parse and encoding/json keep, or nil when v isn't an object or has
// no such member.
func (v *Value) Get(key string) *Value {
	if i := v.Find(key); i >= 0 {
		return v.Items[i].Value
	}
	return nil
}

// Find returns the index of v's last member named key, or -1.
func (v *Value) Find(key string) int {
	if v == nil || v.Kind != Object {
		return -1
	}
	for i := len(v.Items) - 1; i >= 0; i-- {
		if v.Items[i].Key == key {
			return i
		}
	}
	return -1
}

// Interface returns v as encoding/json decodes it into an any with
// UseNumber: map[string]any, []any, string, json.Number, bool or nil. A
// repeated member name keeps its last value, as encoding/json does.
func (v *Value) Interface() any {
	switch v.Kind {
	case Object:
		m := make(map[string]any, len(v.Items))
		for _, it := range v.Items {
			m[it.Key] = it.Value.Interface()
		}
		return m
	case Array:
		a := make([]any, len(v.Items))
		for i, it := range v.Items {
			a[i] = it.Value.Interface()
		}
		return a
	case String:
		return v.Text
	case Number:
		return json.Number(v.Text)
	case Bool:
		return v.Text == "true"
	default:
		return nil
	}
}

type parser struct {
	src   []byte
	pos   int
	depth int
}

func (p *parser) errorf(format string, args ...any) error {
	return p.errorAt(p.pos, fmt.Sprintf(format, args...))
}

func (p *parser) errorAt(off int, msg string) error {
	line := 1 + bytes.Count(p.src[:off], []byte("\n"))
	col := off - (bytes.LastIndexByte(p.src[:off], '\n') + 1) + 1
	return &SyntaxError{Line: line, Column: col, Msg: msg}
}

// describe names the byte at the parser's position for an error message.
func (p *parser) describe() string {
	if p.pos >= len(p.src) {
		return "end of file"
	}
	r, _ := utf8.DecodeRune(p.src[p.pos:])
	return fmt.Sprintf("%q", r)
}

// skip moves past white space and comments.
func (p *parser) skip() error {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		case '/':
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '/' {
				p.pos = lineCommentEnd(p.src, p.pos)
				continue
			}
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '*' {
				n := bytes.Index(p.src[p.pos+2:], []byte("*/"))
				if n < 0 {
					return p.errorf("a /* comment that never ends")
				}
				p.pos += 2 + n + 2
				continue
			}
			return p.errorf("unexpected %s", p.describe())
		default:
			return nil
		}
	}
	return nil
}

func (p *parser) value() (*Value, error) {
	if p.pos >= len(p.src) {
		return nil, p.errorf("unexpected end of file")
	}
	switch c := p.src[p.pos]; {
	case c == '{':
		return p.container(Object, '}')
	case c == '[':
		return p.container(Array, ']')
	case c == '"':
		start := p.pos
		s, err := p.str()
		if err != nil {
			return nil, err
		}
		return &Value{Kind: String, Start: start, End: p.pos, Text: s}, nil
	case c == '-' || ('0' <= c && c <= '9'):
		return p.number()
	case c == 't':
		return p.literal("true", Bool)
	case c == 'f':
		return p.literal("false", Bool)
	case c == 'n':
		return p.literal("null", Null)
	default:
		return nil, p.errorf("unexpected %s", p.describe())
	}
}

func (p *parser) container(kind Kind, closer byte) (*Value, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return nil, p.errorf("nested more than %d deep", maxDepth)
	}
	v := &Value{Kind: kind, Start: p.pos}
	p.pos++ // the opening bracket
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos < len(p.src) && p.src[p.pos] == closer {
			p.pos++
			v.End = p.pos
			return v, nil
		}
		it := Item{Start: p.pos}
		if kind == Object {
			if p.pos >= len(p.src) || p.src[p.pos] != '"' {
				return nil, p.errorf("expected a member name in double quotes, found %s", p.describe())
			}
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			it.Key = key
			if err := p.skip(); err != nil {
				return nil, err
			}
			if p.pos >= len(p.src) || p.src[p.pos] != ':' {
				return nil, p.errorf("expected a colon after the member name, found %s", p.describe())
			}
			p.pos++
			if err := p.skip(); err != nil {
				return nil, err
			}
		}
		val, err := p.value()
		if err != nil {
			return nil, err
		}
		it.Value = val
		v.Items = append(v.Items, it)
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.pos < len(p.src) && p.src[p.pos] == closer {
			p.pos++
			v.End = p.pos
			return v, nil
		}
		return nil, p.errorf("expected a comma or %q, found %s", rune(closer), p.describe())
	}
}

// str reads the string at the parser's position, which holds its opening
// quote, and returns it decoded.
func (p *parser) str() (string, error) {
	start := p.pos
	p.pos++
	var b []byte
	for {
		if p.pos >= len(p.src) {
			return "", p.errorAt(start, "a string that never ends")
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			return string(b), nil
		case c == '\\':
			if p.pos+1 >= len(p.src) {
				return "", p.errorAt(start, "a string that never ends")
			}
			e := p.src[p.pos+1]
			p.pos += 2
			switch e {
			case '"', '\\', '/':
				b = append(b, e)
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'u':
				r, err := p.hex4()
				if err != nil {
					return "", err
				}
				if utf16.IsSurrogate(r) {
					r = p.lowSurrogate(r)
				}
				b = utf8.AppendRune(b, r)
			default:
				return "", p.errorAt(p.pos-2, fmt.Sprintf("a backslash before %q, which isn't an escape", rune(e)))
			}
		case c < 0x20:
			return "", p.errorf("a control character inside a string")
		default:
			r, size := utf8.DecodeRune(p.src[p.pos:])
			if r == utf8.RuneError && size == 1 {
				return "", p.errorf("a string that isn't valid UTF-8")
			}
			b = append(b, p.src[p.pos:p.pos+size]...)
			p.pos += size
		}
	}
}

// lowSurrogate pairs the surrogate hi, just read from an escape, with an
// escaped low surrogate right after it. A surrogate without its pair
// decodes as U+FFFD, as encoding/json decodes it.
func (p *parser) lowSurrogate(hi rune) rune {
	save := p.pos
	if p.pos+6 <= len(p.src) && p.src[p.pos] == '\\' && p.src[p.pos+1] == 'u' {
		p.pos += 2
		if lo, err := p.hex4(); err == nil {
			if r := utf16.DecodeRune(hi, lo); r != unicode.ReplacementChar {
				return r
			}
		}
	}
	p.pos = save
	return unicode.ReplacementChar
}

// hex4 reads the four hex digits of an escape.
func (p *parser) hex4() (rune, error) {
	if p.pos+4 > len(p.src) {
		return 0, p.errorf("an escape that needs four hex digits")
	}
	var r rune
	for _, c := range p.src[p.pos : p.pos+4] {
		var d byte
		switch {
		case '0' <= c && c <= '9':
			d = c - '0'
		case 'a' <= c && c <= 'f':
			d = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, p.errorf("an escape that needs four hex digits")
		}
		r = r<<4 | rune(d)
	}
	p.pos += 4
	return r, nil
}

func (p *parser) number() (*Value, error) {
	start := p.pos
	digits := func() int {
		n := 0
		for p.pos < len(p.src) && '0' <= p.src[p.pos] && p.src[p.pos] <= '9' {
			p.pos++
			n++
		}
		return n
	}
	if p.src[p.pos] == '-' {
		p.pos++
	}
	if p.pos < len(p.src) && p.src[p.pos] == '0' {
		p.pos++
	} else if digits() == 0 {
		return nil, p.errorAt(start, "a number without digits")
	}
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		p.pos++
		if digits() == 0 {
			return nil, p.errorAt(start, "a number with no digits after its point")
		}
	}
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
			p.pos++
		}
		if digits() == 0 {
			return nil, p.errorAt(start, "a number with no digits in its exponent")
		}
	}
	return &Value{Kind: Number, Start: start, End: p.pos, Text: string(p.src[start:p.pos])}, nil
}

func (p *parser) literal(word string, kind Kind) (*Value, error) {
	if !bytes.HasPrefix(p.src[p.pos:], []byte(word)) {
		return nil, p.errorf("unexpected %s", p.describe())
	}
	v := &Value{Kind: kind, Start: p.pos, End: p.pos + len(word), Text: word}
	p.pos += len(word)
	return v, nil
}
