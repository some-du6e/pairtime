// SPDX-License-Identifier: AGPL-3.0-or-later

package jsonc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Style is how a file lays its JSON out, so that text an edit adds looks
// like the text around it.
type Style struct {
	// Newline is the file's line ending, "\n" or "\r\n".
	Newline string
	// Indent is one level of indentation, such as two spaces or a tab.
	Indent string
	// Compact means the file is one line, so added values are one line too.
	Compact bool
}

// StyleOf returns the layout of src, whose parsed root is root (nil for a
// source with no value): the line ending of its first line, the
// indentation of root's first member or element that starts a line, and
// whether root is a non-empty container written on one line. Without
// those clues it is "\n" and two spaces, the layout JSON.stringify(v,
// null, 2) writes.
func StyleOf(src []byte, root *Value) Style {
	st := Style{Newline: "\n", Indent: "  "}
	if i := bytes.IndexByte(src, '\n'); i > 0 && src[i-1] == '\r' {
		st.Newline = "\r\n"
	}
	if root == nil || (root.Kind != Object && root.Kind != Array) || len(root.Items) == 0 {
		return st
	}
	if bytes.IndexByte(src[root.Start:root.End], '\n') < 0 {
		st.Compact = true
		return st
	}
	base := indentAt(src, root.Start)
	for _, it := range root.Items {
		ind, own := ownLine(src, it.Start)
		if !own || !strings.HasPrefix(ind, base) {
			continue
		}
		if unit := ind[len(base):]; unit != "" {
			st.Indent = unit
			break
		}
	}
	return st
}

// Obj is a JSON object whose members are rendered in order.
type Obj []Member

// Member is one name and value of an Obj.
type Member struct {
	Key string
	Val any
}

// Render returns v as JSON text laid out in st: on one line if st is
// compact, and otherwise with one member or element per line, nested lines
// starting with indent plus st.Indent for each level, and the closing
// bracket at indent. v may be an Obj, a []any or []string, a string, a
// bool, nil, an int or a json.Number. Strings must be valid UTF-8.
func Render(v any, st Style, indent string) string {
	var b strings.Builder
	render(&b, v, st, indent)
	return b.String()
}

func render(b *strings.Builder, v any, st Style, indent string) {
	inner := indent + st.Indent
	open := func(i int) {
		if i > 0 {
			b.WriteByte(',')
			if st.Compact {
				b.WriteByte(' ')
			}
		}
		if !st.Compact {
			b.WriteString(st.Newline)
			b.WriteString(inner)
		}
	}
	shut := func(closer byte) {
		if !st.Compact {
			b.WriteString(st.Newline)
			b.WriteString(indent)
		}
		b.WriteByte(closer)
	}
	switch v := v.(type) {
	case Obj:
		if len(v) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, m := range v {
			open(i)
			writeString(b, m.Key)
			b.WriteString(": ")
			render(b, m.Val, st, inner)
		}
		shut('}')
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range v {
			open(i)
			render(b, e, st, inner)
		}
		shut(']')
	case []string:
		a := make([]any, len(v))
		for i, s := range v {
			a[i] = s
		}
		render(b, a, st, indent)
	case string:
		writeString(b, v)
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case nil:
		b.WriteString("null")
	case int:
		b.WriteString(strconv.Itoa(v))
	case json.Number:
		b.WriteString(string(v))
	default:
		panic(fmt.Sprintf("jsonc: can't render a %T", v))
	}
}

// writeString writes s as a JSON string. Unlike encoding/json it leaves <,
// > and & alone, so a path or a command line reads in the file as it is.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, "\x5cu%04x", r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// AddMember returns src with the member "key": val added after the last
// member of obj, which must be an object parsed from src.
func AddMember(src []byte, obj *Value, key string, val any, st Style) []byte {
	return insertLast(src, obj, func(indent string, st Style) string {
		var b strings.Builder
		writeString(&b, key)
		b.WriteString(": ")
		render(&b, val, st, indent)
		return b.String()
	}, st)
}

// AppendElement returns src with val added after the last element of arr,
// which must be an array parsed from src.
func AppendElement(src []byte, arr *Value, val any, st Style) []byte {
	return insertLast(src, arr, func(indent string, st Style) string {
		return Render(val, st, indent)
	}, st)
}

// insertLast adds the text item returns as c's last item. A container
// written over several lines gets the item on a line of its own, indented
// like the item before it; one written on one line gets it on that line.
// The comma that separates it goes right after the item before, so a
// comment at the end of that line stays at the end of that line. When the
// item before has a trailing comma, the new one gets one too.
func insertLast(src []byte, c *Value, item func(indent string, st Style) string, st Style) []byte {
	closeAt := c.End - 1
	if len(c.Items) == 0 {
		if st.Compact {
			one := st
			one.Compact = true
			return splice(src, c.Start+1, c.Start+1, item("", one))
		}
		indent := indentAt(src, c.Start)
		multi := st
		multi.Compact = false
		text := st.Newline + indent + st.Indent + item(indent+st.Indent, multi)
		if isBlank(src[c.Start+1 : closeAt]) {
			// {} and a bracket pair with only white space between them both
			// become the brackets on their own lines around the item.
			return splice(src, c.Start+1, closeAt, text+st.Newline+indent)
		}
		// Comments inside: keep them, after the item.
		return splice(src, c.Start+1, c.Start+1, text)
	}
	last := c.Items[len(c.Items)-1]
	end := last.Value.End
	comma := -1
	if j := skipBlank(src, end); j < closeAt && src[j] == ',' {
		comma = j // a trailing comma already separates the new item
	}
	if bytes.IndexByte(src[c.Start:c.End], '\n') < 0 {
		one := st
		one.Compact = true
		if comma >= 0 {
			return splice(src, comma+1, comma+1, " "+item("", one)+",")
		}
		return splice(src, end, end, ", "+item("", one))
	}
	indent, own := ownLine(src, last.Start)
	if !own {
		indent = indentAt(src, c.Start) + st.Indent
	}
	multi := st
	multi.Compact = false
	text := st.Newline + indent + item(indent, multi)
	if comma >= 0 {
		at := lineEnd(src, comma+1, closeAt)
		return splice(src, at, at, text+",")
	}
	at := lineEnd(src, end, closeAt)
	if at == end {
		return splice(src, end, end, ","+text)
	}
	out := splice(src, at, at, text) // the later offset first, so end stays right
	return splice(out, end, end, ",")
}

// Remove returns src without item i of c, which must be an object or array
// parsed from src. It takes the item's separator and the line break before
// it too, so removing an item that AddMember or AppendElement added gives
// back the bytes from before. An object or array left with nothing but
// white space between its brackets becomes {} or [].
func Remove(src []byte, c *Value, i int) []byte {
	closeAt := c.End - 1
	it := c.Items[i]
	start, end := it.Start, it.Value.End
	trailing := -1 // the comma after the item, if there is one
	if j := skipBlank(src, end); j < closeAt && src[j] == ',' {
		trailing = j
	}
	lb, own := lineBreakBefore(src, start)
	switch {
	case len(c.Items) == 1:
		cut := end
		if trailing >= 0 {
			cut = trailing + 1
		}
		rest := append(append([]byte{}, src[c.Start+1:start]...), src[cut:closeAt]...)
		if isBlank(rest) {
			return splice(src, c.Start+1, closeAt, "")
		}
		if own {
			start = lb
		}
		return splice(src, start, cut, "")
	case i == len(c.Items)-1:
		prev := c.Items[i-1]
		sep := skipBlank(src, prev.Value.End) // the comma between prev and the item
		if trailing >= 0 {
			// The file writes trailing commas: the one after prev stays,
			// and is prev's trailing comma again.
			if own && sep < lb {
				return splice(src, lb, trailing+1, "")
			}
			return splice(src, sep+1, trailing+1, "")
		}
		if own && sep < lb {
			out := splice(src, lb, end, "")
			return splice(out, sep, sep+1, "")
		}
		return splice(src, prev.Value.End, end, "")
	default:
		next := c.Items[i+1]
		if own && trailing >= 0 {
			return splice(src, lb, trailing+1, "")
		}
		return splice(src, start, next.Start, "")
	}
}

// splice returns src with src[from:to] replaced by text, in a new slice.
func splice(src []byte, from, to int, text string) []byte {
	out := make([]byte, 0, len(src)-(to-from)+len(text))
	out = append(out, src[:from]...)
	out = append(out, text...)
	return append(out, src[to:]...)
}

// isBlank reports whether b holds only spaces, tabs and line breaks.
func isBlank(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}

// IsBlank reports whether src holds no value and no comment: only a byte
// order mark, spaces, tabs and line breaks.
func IsBlank(src []byte) bool {
	return isBlank(src[BOMLen(src):])
}

// skipBlank returns the offset of the first byte at or after i that isn't
// white space or inside a comment. src must parse.
func skipBlank(src []byte, i int) int {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\n', '\r':
			i++
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				n := bytes.IndexByte(src[i:], '\n')
				if n < 0 {
					return len(src)
				}
				i += n
			} else if i+1 < len(src) && src[i+1] == '*' {
				n := bytes.Index(src[i+2:], []byte("*/"))
				if n < 0 {
					return len(src)
				}
				i += 2 + n + 2
			} else {
				return i
			}
		default:
			return i
		}
	}
	return i
}

// lineEnd returns the offset of the line break that ends i's line, when
// only spaces, tabs and comments come between i and it, and i otherwise.
// It looks no further than limit.
func lineEnd(src []byte, i, limit int) int {
	j := i
	for j < limit {
		switch src[j] {
		case ' ', '\t':
			j++
		case '\n':
			return j
		case '\r':
			if j+1 < len(src) && src[j+1] == '\n' {
				return j
			}
			return i
		case '/':
			if j+1 < limit && src[j+1] == '/' {
				n := bytes.IndexByte(src[j:], '\n')
				if n < 0 {
					return i
				}
				n += j
				if src[n-1] == '\r' {
					return n - 1
				}
				return n
			}
			if j+1 < limit && src[j+1] == '*' {
				n := bytes.Index(src[j+2:], []byte("*/"))
				if n < 0 {
					return i
				}
				e := j + 2 + n + 2
				if bytes.IndexByte(src[j:e], '\n') >= 0 {
					return i
				}
				j = e
				continue
			}
			return i
		default:
			return i
		}
	}
	return i
}

// lineStart returns the offset of the first byte of i's line.
func lineStart(src []byte, i int) int {
	return bytes.LastIndexByte(src[:i], '\n') + 1
}

// indentAt returns the spaces and tabs that start i's line.
func indentAt(src []byte, i int) string {
	s := lineStart(src, i)
	e := s
	for e < len(src) && (src[e] == ' ' || src[e] == '\t') {
		e++
	}
	return string(src[s:e])
}

// ownLine reports whether only spaces and tabs come before offset i on its
// line, and returns them.
func ownLine(src []byte, i int) (string, bool) {
	s := lineStart(src, i)
	for _, c := range src[s:i] {
		if c != ' ' && c != '\t' {
			return "", false
		}
	}
	return string(src[s:i]), true
}

// lineBreakBefore returns the offset of the line break, "\n" or "\r\n",
// that ends the line before i's, when only spaces and tabs come before i on
// its line.
func lineBreakBefore(src []byte, i int) (int, bool) {
	s := lineStart(src, i)
	if s == 0 {
		return 0, false
	}
	if _, own := ownLine(src, i); !own {
		return 0, false
	}
	lb := s - 1
	if lb > 0 && src[lb-1] == '\r' {
		lb--
	}
	return lb, true
}
