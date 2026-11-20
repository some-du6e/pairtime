// SPDX-License-Identifier: AGPL-3.0-or-later

package jsonc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// at returns the value at path, a list of member names, under the root of
// src.
func at(t *testing.T, src string, path ...string) *Value {
	t.Helper()
	v, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	for _, k := range path {
		if v = v.Get(k); v == nil {
			t.Fatalf("no %q in %q", k, src)
		}
	}
	return v
}

var lf2 = Style{Newline: "\n", Indent: "  "}

func TestAddMember(t *testing.T) {
	tests := []struct {
		name string
		src  string
		path []string // the object, under the root
		key  string
		val  any
		st   Style
		want string
	}{
		{name: "the member goes on a line of its own after the last one, indented like it",
			src: "{\n  \"a\": 1\n}\n", key: "b", val: true, st: lf2,
			want: "{\n  \"a\": 1,\n  \"b\": true\n}\n"},
		{name: "a comment after the last member stays at the end of its line",
			src: "{\n  \"a\": 1 // one\n}", key: "b", val: true, st: lf2,
			want: "{\n  \"a\": 1, // one\n  \"b\": true\n}"},
		{name: "a CRLF file gets CRLF line endings",
			src: "{\r\n  \"a\": 1\r\n}\r\n", key: "b", val: true, st: Style{Newline: "\r\n", Indent: "  "},
			want: "{\r\n  \"a\": 1,\r\n  \"b\": true\r\n}\r\n"},
		{name: "an empty object gets the member on a line of its own",
			src: "{}", key: "b", val: true, st: lf2,
			want: "{\n  \"b\": true\n}"},
		{name: "an empty object inside another is indented one level more",
			src: "{\n  \"x\": {}\n}", path: []string{"x"}, key: "b", val: true, st: lf2,
			want: "{\n  \"x\": {\n    \"b\": true\n  }\n}"},
		{name: "a comment inside an empty object stays, after the member",
			src: "{\n  // none yet\n}", key: "b", val: true, st: lf2,
			want: "{\n  \"b\": true\n  // none yet\n}"},
		{name: "an object on one line gets the member on that line",
			src: `{"a": 1}`, key: "b", val: true, st: Style{Newline: "\n", Indent: "  ", Compact: true},
			want: `{"a": 1, "b": true}`},
		{name: "after a trailing comma the member gets one too",
			src: "{\n  \"a\": 1,\n}", key: "b", val: true, st: lf2,
			want: "{\n  \"a\": 1,\n  \"b\": true,\n}"},
		{name: "a closing bracket on the last member's line stays on the new member's line",
			src: "{\n  \"a\": 1}", key: "b", val: true, st: lf2,
			want: "{\n  \"a\": 1,\n  \"b\": true}"},
		{name: "a nested value is laid out with the file's indent",
			src: "{\n\t\"a\": 1\n}", key: "b", val: Obj{{Key: "c", Val: []any{1, "x"}}, {Key: "d", Val: Obj{}}},
			st:   Style{Newline: "\n", Indent: "\t"},
			want: "{\n\t\"a\": 1,\n\t\"b\": {\n\t\t\"c\": [\n\t\t\t1,\n\t\t\t\"x\"\n\t\t],\n\t\t\"d\": {}\n\t}\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := at(t, tt.src, tt.path...)
			got := string(AddMember([]byte(tt.src), obj, tt.key, tt.val, tt.st))
			if got != tt.want {
				t.Fatalf("AddMember gives\n%q\nwant\n%q", got, tt.want)
			}
			checkRemoveUndoes(t, tt.src, got, tt.path)
		})
	}
}

func TestAppendElement(t *testing.T) {
	tests := []struct {
		name string
		src  string
		path []string // the array, under the root
		val  any
		st   Style
		want string
	}{
		{name: "the element goes on a line of its own after the last one",
			src: "[\n  1\n]", val: 2, st: lf2,
			want: "[\n  1,\n  2\n]"},
		{name: "an empty array gets the element on a line of its own",
			src: "{\n  \"l\": []\n}", path: []string{"l"}, val: Obj{{Key: "a", Val: 1}}, st: lf2,
			want: "{\n  \"l\": [\n    {\n      \"a\": 1\n    }\n  ]\n}"},
		{name: "an array on one line gets the element on that line, laid out on one line",
			src: "{\n  \"l\": [1]\n}", path: []string{"l"}, val: Obj{{Key: "a", Val: []string{"x", "y"}}}, st: lf2,
			want: "{\n  \"l\": [1, {\"a\": [\"x\", \"y\"]}]\n}"},
		{name: "after a trailing comma on one line the element gets one too",
			src: "[1, 2,]", val: 3, st: Style{Newline: "\n", Indent: "  ", Compact: true},
			want: "[1, 2, 3,]"},
		{name: "after a trailing comma on its own line the element gets one too",
			src: "[\n\t1,\n]", val: 2, st: Style{Newline: "\n", Indent: "\t"},
			want: "[\n\t1,\n\t2,\n]"},
		{name: "a block comment after the last element stays on its line",
			src: "[\n  1 /* one */\n]", val: 2, st: lf2,
			want: "[\n  1, /* one */\n  2\n]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arr := at(t, tt.src, tt.path...)
			got := string(AppendElement([]byte(tt.src), arr, tt.val, tt.st))
			if got != tt.want {
				t.Fatalf("AppendElement gives\n%q\nwant\n%q", got, tt.want)
			}
			checkRemoveUndoes(t, tt.src, got, tt.path)
		})
	}
}

// checkRemoveUndoes checks that got, src with one item added at the end
// of the container at path, is still JSON with comments, is strict JSON
// when src is, and gives back src exactly when the item is removed.
func checkRemoveUndoes(t *testing.T, src, got string, path []string) {
	t.Helper()
	if json.Valid([]byte(src)) && !json.Valid([]byte(got)) {
		t.Errorf("src is strict JSON and the result isn't: %q", got)
	}
	c := at(t, got, path...)
	back := string(Remove([]byte(got), c, len(c.Items)-1))
	if back != src {
		t.Errorf("removing the added item gives\n%q\nwant the source\n%q", back, src)
	}
}

func TestRemove(t *testing.T) {
	tests := []struct {
		name string
		src  string
		path []string
		i    int
		want string
	}{
		{name: "the first of several elements goes with its comma and line",
			src: "[\n  1,\n  2\n]", i: 0, want: "[\n  2\n]"},
		{name: "an element in the middle goes with its comma and line",
			src: "[\n  1,\n  2,\n  3\n]", i: 1, want: "[\n  1,\n  3\n]"},
		{name: "the first element on one line goes with its comma",
			src: "[1, 2, 3]", i: 0, want: "[2, 3]"},
		{name: "an element in the middle of one line goes with its comma",
			src: "[1, 2, 3]", i: 1, want: "[1, 3]"},
		{name: "the last element on one line takes the comma before it",
			src: "[1, 2, 3]", i: 2, want: "[1, 2]"},
		{name: "a comment line before the last element stays",
			src: "[\n  1,\n  // two\n  2\n]", i: 1, want: "[\n  1\n  // two\n]"},
		{name: "the only element leaves an empty array",
			src: "{\n  \"l\": [\n    1\n  ]\n}", path: []string{"l"}, i: 0, want: "{\n  \"l\": []\n}"},
		{name: "a member in the middle of an object goes with its line",
			src: "{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": 3\n}", i: 1, want: "{\n  \"a\": 1,\n  \"c\": 3\n}"},
		{name: "CRLF lines go with their CR",
			src: "[\r\n  1,\r\n  2\r\n]", i: 1, want: "[\r\n  1\r\n]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := at(t, tt.src, tt.path...)
			if got := string(Remove([]byte(tt.src), c, tt.i)); got != tt.want {
				t.Errorf("Remove gives\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestRenderStringsAsEncodingJSON(t *testing.T) {
	tests := []struct {
		name string
		s    string
	}{
		{"quotes and backslashes are escaped", `say "hi" \ bye`},
		{"a Windows path keeps its backslashes, escaped", `C:\Users\teen\AppData\Local\pairtime\bin\pairtime.exe`},
		{"line breaks and tabs use short escapes", "a\nb\rc\td"},
		{"backspace and form feed use short escapes", "\b\f"},
		{"other control characters use hex escapes", "\x01\x1f"},
		{"<, > and & are left alone, unlike encoding/json's default", "<a href='x'>&amp;</a>"},
		{"UTF-8 text is left alone", "日本語 é 😀"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			enc := json.NewEncoder(&b)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(tt.s); err != nil {
				t.Fatal(err)
			}
			want := strings.TrimSuffix(b.String(), "\n")
			if got := Render(tt.s, lf2, ""); got != want {
				t.Errorf("Render gives %s, encoding/json %s", got, want)
			}
		})
	}
}

func TestRenderLayout(t *testing.T) {
	v := Obj{
		{Key: "hooks", Val: Obj{{Key: "Stop", Val: []any{Obj{
			{Key: "matcher", Val: "Edit|Write"},
			{Key: "hooks", Val: []any{Obj{{Key: "type", Val: "command"}, {Key: "args", Val: []string{"hook", "--event", "Stop"}}}}},
		}}}}},
		{Key: "empty", Val: []any{}},
		{Key: "none", Val: nil},
		{Key: "n", Val: 3},
	}
	one := Render(v, Style{Newline: "\n", Indent: "  ", Compact: true}, "")
	if !json.Valid([]byte(one)) {
		t.Fatalf("the one-line rendering isn't JSON: %s", one)
	}
	tests := []struct {
		name   string
		st     Style
		indent string
		prefix string // json.Indent's prefix
		unit   string // json.Indent's indent
	}{
		{"two spaces lay out as json.Indent does", lf2, "", "", "  "},
		{"tabs lay out as json.Indent does", Style{Newline: "\n", Indent: "\t"}, "", "", "\t"},
		{"a value inside indented text starts its lines with that text", lf2, "    ", "    ", "  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var want bytes.Buffer
			if err := json.Indent(&want, []byte(one), tt.prefix, tt.unit); err != nil {
				t.Fatal(err)
			}
			if got := Render(v, tt.st, tt.indent); got != want.String() {
				t.Errorf("Render gives\n%s\njson.Indent\n%s", got, want.String())
			}
		})
	}
	crlf := Render(v, Style{Newline: "\r\n", Indent: "  "}, "")
	if lf := Render(v, lf2, ""); crlf != strings.ReplaceAll(lf, "\n", "\r\n") {
		t.Errorf("the CRLF rendering isn't the LF one with CRLF line endings:\n%q", crlf)
	}
	if want := `{"hooks": {"Stop": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "args": ["hook", "--event", "Stop"]}]}]}, "empty": [], "none": null, "n": 3}`; one != want {
		t.Errorf("the one-line rendering is\n%s\nwant\n%s", one, want)
	}
}

func TestStyleOf(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want Style
	}{
		{"two spaces and LF, as Claude Code writes", "{\n  \"a\": 1\n}\n", Style{Newline: "\n", Indent: "  "}},
		{"four spaces and CRLF", "{\r\n    \"a\": 1\r\n}\r\n", Style{Newline: "\r\n", Indent: "    "}},
		{"tabs after a byte order mark", "\xef\xbb\xbf{\n\t\"a\": 1\n}", Style{Newline: "\n", Indent: "\t"}},
		{"the first member that starts a line decides the indent", "{ \"a\": 1,\n   \"b\": 2\n}", Style{Newline: "\n", Indent: "   "}},
		{"a root on one line is compact", `{"a": {"b": 1}}`, Style{Newline: "\n", Indent: "  ", Compact: true}},
		{"an empty root gives the defaults", "{}", Style{Newline: "\n", Indent: "  "}},
		{"a comment line first doesn't change the indent", "// x\r\n{\r\n  \"a\": 1\r\n}", Style{Newline: "\r\n", Indent: "  "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := Parse([]byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			if got := StyleOf([]byte(tt.src), root); got != tt.want {
				t.Errorf("StyleOf = %+v, want %+v", got, tt.want)
			}
		})
	}
}
