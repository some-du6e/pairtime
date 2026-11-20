// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"reflect"
	"testing"
)

// The backslashes of \u escapes are written \x5c here, so that no editor
// or tool turns the escape into the character it stands for.

func TestJSONView(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{"text without a backslash comes back unchanged", `plain "text"`, `plain "text"`, false},
		{"an escaped quote keeps its quote", `a\"b`, `a "b`, true},
		{"an escaped line break becomes a space and a line break", `a\nb`, "a \nb", true},
		{"tab, return, backspace and form feed escapes become spaces", `a\tb\rc\bd\fe`, "a  b  c  d  e", true},
		{"an escaped slash keeps its slash", `a\/b`, "a /b", true},
		{"a run of backslashes counts as one escape", `a\\nb`, "a  \nb", true},
		{"a unicode escape of a symbol becomes spaces", "\x5cu003cmaya\x5cu003e", "      maya      ", true},
		{"a unicode escape of a letter fills its bytes with the letter", "jos\x5cu00e9@x.org", "josééé@x.org", true},
		{"a surrogate pair of a letter fills its twelve bytes", "\x5cud835\x5cudc00", "𝐀𝐀𝐀", true},
		{"a surrogate pair of a symbol becomes spaces", "\x5cud83d\x5cude00!", "            !", true},
		{"a lone surrogate becomes spaces", "a\x5cud800b", "a      b", true},
		{"a unicode escape without four hex digits keeps its letters", "a\x5cu12g", "a u12g", true},
		{"a backslash at the end becomes a space", "a\x5c", "a ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := jsonView(tt.in)
			if got != tt.want || changed != tt.changed {
				t.Errorf("jsonView(%q) = %q, %v; want %q, %v", tt.in, got, changed, tt.want, tt.changed)
			}
			if len(got) != len(tt.in) {
				t.Errorf("the view is %d bytes, the text %d", len(got), len(tt.in))
			}
		})
	}
}

func TestKeepEscapesWhole(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		start, end int
		want       [2]int
	}{
		{"a span that starts on the letter of an escape takes the backslash too", `"a\nbob"`, 3, 7, [2]int{2, 7}},
		{"a span that ends inside a unicode escape takes the rest of it", "\"x\x5cu00e9y\"", 1, 4, [2]int{1, 8}},
		{"a span on whole escapes stays as it is", `"a\nb"`, 2, 4, [2]int{2, 4}},
		{"the n after an escaped backslash is plain text", `"a\\nb"`, 4, 6, [2]int{4, 6}},
		{"a span between the halves of a surrogate pair takes both", "\"\x5cud835\x5cudc00\"", 7, 13, [2]int{1, 13}},
		{"a backslash outside quotes is plain text", `C:\Users\tom`, 9, 12, [2]int{9, 12}},
		{"a line break closes an unterminated string", "\"a\nC:\\tom", 6, 9, [2]int{6, 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := []Span{{Start: tt.start, End: tt.end, Type: TypeDeny}}
			keepEscapesWhole(tt.text, spans)
			if got := [2]int{spans[0].Start, spans[0].End}; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("span [%d, %d) of %q became %v, want %v", tt.start, tt.end, tt.text, got, tt.want)
			}
		})
	}
}
