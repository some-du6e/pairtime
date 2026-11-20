// SPDX-License-Identifier: AGPL-3.0-or-later

package jsonc

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// decodeStd decodes src with encoding/json, the oracle Parse is checked
// against.
func decodeStd(t *testing.T, src string) any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(src))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatalf("encoding/json can't decode %q: %v", src, err)
	}
	return v
}

func TestParseAgreesWithEncodingJSON(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"every kind of value parses as encoding/json decodes it", `{"s": "x", "n": -1.5e3, "z": 0, "e": 1E+2, "t": true, "f": false, "nil": null, "a": [1, [2], {}], "o": {"k": "v"}}`},
		{"short escapes decode as encoding/json decodes them", `["\"\\\/\b\f\n\r\t"]`},
		{"hex escapes decode, a surrogate pair as one character", "[\"\x5cu00e9\x5cu0041\", \"\x5cud83d\x5cude00\"]"},
		{"a surrogate without its pair decodes as U+FFFD", "[\"\x5cud83d\", \"\x5cud83dx\", \"\x5cude00\", \"\x5cud83d\x5cu0041\"]"},
		{"UTF-8 text is kept as it is", `{"é": "日本語", "emoji": "😀"}`},
		{"a repeated name keeps its last value", `{"a": 1, "b": 2, "a": 3}`},
		{"white space of every kind is skipped", "\t{ \"a\" :\r\n[ 1 ,2 ] }\n"},
		{"a value at the top can be a scalar", `"just a string"`},
		{"empty containers parse", `{"a": {}, "b": [], "c": [{}, []]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Parse([]byte(tt.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got, want := v.Interface(), decodeStd(t, tt.src); !reflect.DeepEqual(got, want) {
				t.Errorf("Parse gives %#v, encoding/json %#v", got, want)
			}
		})
	}
}

func TestParseAcceptsCommentsAndTrailingCommas(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		strict string // the same value without comments and trailing commas
	}{
		{"line and block comments are skipped", "// top\n{\n  /* a */ \"a\": 1, // one\n  \"b\": /* two */ 2\n}\n", `{"a": 1, "b": 2}`},
		{"a trailing comma ends an object or an array", "{\"a\": [1, 2,], \"b\": {\"c\": true,},}", `{"a": [1, 2], "b": {"c": true}}`},
		{"a line comment can end the file with no newline", "[1] // end", "[1]"},
		{"a block comment can span lines", "/* one\ntwo */ [1]", "[1]"},
		{"a byte order mark is skipped", "\xef\xbb\xbf{\"a\": 1}", `{"a": 1}`},
		{"comment marks inside a string are text", `{"u": "http://x/*y*/ //z"}`, `{"u": "http://x/*y*/ //z"}`},
		{"CRLF line endings end a line comment", "{\r\n  \"a\": 1 // one\r\n}\r\n", `{"a": 1}`},
		{"a carriage return alone ends a line comment, as JavaScript and VS Code read it", "// note\r{\"model\": \"x\"}", `{"model": "x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Parse([]byte(tt.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got, want := v.Interface(), decodeStd(t, tt.strict); !reflect.DeepEqual(got, want) {
				t.Errorf("Parse gives %#v, want %#v", got, want)
			}
		})
	}
}

func TestComments(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want [][2]int
	}{
		{"a line comment ends before its line feed", "{} // a\n", [][2]int{{3, 7}}},
		{"a line comment ends before the carriage return of a CRLF", "{} // a\r\n", [][2]int{{3, 7}}},
		{"a line comment ends at a carriage return alone", "// a\r{}", [][2]int{{0, 4}}},
		{"a line comment at the end of the file ends there", "{} // a", [][2]int{{3, 7}}},
		{"a block comment ends after its star and slash", "{/* a */}", [][2]int{{1, 8}}},
		{"comment marks inside a string aren't a comment", `{"u": "http://x/*y*/"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.src)); err != nil {
				t.Fatalf("Parse(%q): %v", tt.src, err)
			}
			if got := Comments([]byte(tt.src)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Comments(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// jsonOK is true where encoding/json accepts the input, so it
		// can't serve as the oracle.
		jsonOK bool
	}{
		{name: "a member without a value is an error", src: `{"a":}`},
		{name: "two commas in a row are an error", src: `[1,,2]`},
		{name: "a comma alone in an array is an error", src: `[,]`},
		{name: "a member without a colon is an error", src: `{"a" 1}`},
		{name: "a name without quotes is an error", src: `{a: 1}`},
		{name: "values without a comma between them are an error", src: `[1 2]`},
		{name: "a control character inside a string is an error", src: "\"a\x01\""},
		{name: "a string that never ends is an error", src: `"abc`},
		{name: "a block comment that never ends is an error", src: `[1] /* never`},
		{name: "a slash that starts no comment is an error", src: `[1] /x`},
		{name: "a number with a leading zero is an error", src: `01`},
		{name: "a number ending in a point is an error", src: `1.`},
		{name: "a minus sign alone is an error", src: `-`},
		{name: "a number starting with a point is an error", src: `.5`},
		{name: "a cut-off literal is an error", src: `tru`},
		{name: "two top-level values are an error", src: `{}{}`},
		{name: "an extra closing bracket is an error", src: `[1]]`},
		{name: "an unknown escape is an error", src: `"\x"`},
		{name: "a hex escape with too few digits is an error", src: "\"\x5cu12\""},
		// encoding/json doesn't check UTF-8 and decodes the byte as U+FFFD.
		{name: "a string that isn't UTF-8 is an error", src: "\"\xff\"", jsonOK: true},
		{name: "UTF-16 is refused, since edits write UTF-8", src: "\xff\xfe{\x00}\x00"},
		{name: "nesting deeper than 512 is an error", src: strings.Repeat("[", 600) + strings.Repeat("]", 600), jsonOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.src))
			var serr *SyntaxError
			if !errors.As(err, &serr) {
				t.Fatalf("Parse(%q) = %v, want a *SyntaxError", tt.src, err)
			}
			if !tt.jsonOK && json.Valid([]byte(tt.src)) {
				t.Errorf("encoding/json accepts %q, so the case doesn't test what it says", tt.src)
			}
		})
	}
}

func TestParseFindsNoValueInBlankSources(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"an empty source holds no value", ""},
		{"white space holds no value", " \n\t\r\n"},
		{"a line comment holds no value", "// only a comment\n"},
		{"comments hold no value", "/* a */ // b"},
		{"a byte order mark alone holds no value", "\xef\xbb\xbf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.src)); !errors.Is(err, ErrEmpty) {
				t.Errorf("Parse(%q) = %v, want ErrEmpty", tt.src, err)
			}
		})
	}
}

func TestSyntaxErrorSaysWhere(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		line, col int
	}{
		{"the error is at the first byte that isn't JSON", "{\n  \"a\": tru\n}", 2, 8},
		{"a string that never ends is reported where it starts", "[\n \"abc", 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.src))
			var serr *SyntaxError
			if !errors.As(err, &serr) {
				t.Fatalf("Parse = %v, want a *SyntaxError", err)
			}
			if serr.Line != tt.line || serr.Column != tt.col {
				t.Errorf("the error is at line %d, column %d, want line %d, column %d: %v", serr.Line, serr.Column, tt.line, tt.col, err)
			}
		})
	}
}

func TestValuesKnowWhereTheyAre(t *testing.T) {
	src := `{"a": [1, "x"], "b": {}, "a": null}`
	v, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"the root spans the whole value", src[v.Start:v.End], src},
		{"a member starts at its name", src[v.Items[0].Start:v.Items[0].Value.End], `"a": [1, "x"]`},
		{"an array spans its brackets", src[v.Items[0].Value.Start:v.Items[0].Value.End], `[1, "x"]`},
		{"an element spans its own text", src[v.Items[0].Value.Items[1].Start:v.Items[0].Value.Items[1].Value.End], `"x"`},
		{"Get returns the last member of a repeated name", src[v.Get("a").Start:v.Get("a").End], `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
	if v.Find("missing") != -1 || v.Items[0].Value.Find("a") != -1 {
		t.Error("Find found a member that isn't there, or a member of an array")
	}
	var none *Value
	if none.Get("a") != nil {
		t.Error("Get on a nil value returned a value")
	}
}
