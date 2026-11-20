// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestCodeBlockGoldens renders each testdata/redact/code/<name>.md with its
// code blocks collapsed and wants <name>.golden. The goldens were written by
// hand from CommonMark's fence rules, not produced by this code.
func TestCodeBlockGoldens(t *testing.T) {
	inputs, err := filepath.Glob("../../testdata/redact/code/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 16 {
		t.Fatalf("found %d golden inputs, want 16", len(inputs))
	}
	for _, in := range inputs {
		name := strings.TrimSuffix(filepath.Base(in), ".md")
		t.Run(name, func(t *testing.T) {
			text, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(in, ".md") + ".golden")
			if err != nil {
				t.Fatal(err)
			}
			if got := Render(string(text), CodeBlocks(string(text))); got != string(want) {
				t.Errorf("got\n%q\nwant\n%q", got, want)
			}
		})
	}
}

func TestCodeBlocks(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []Span
	}{
		{"a span starts at the fence, not its indentation, and ends at the closing run",
			"  ```c\n  x;\n  ```  \nok",
			[]Span{{Start: 2, End: 17, Type: TypeCode, Lines: 1, Lang: "c"}}},
		{"CRLF line endings count lines the same way",
			"```\r\nx\r\n```\r\nok",
			[]Span{{Start: 0, End: 11, Type: TypeCode, Lines: 1}}},
		{"a tab before a closing fence is four columns, too deep to close it",
			"```\nx\n\t```\n",
			[]Span{{Start: 0, End: 10, Type: TypeCode, Lines: 2}}},
		{"a language name keeps + and #",
			"```C++\nint x;\n```",
			[]Span{{Start: 0, End: 17, Type: TypeCode, Lines: 1, Lang: "c++"}}},
		{"no fences, no spans", "just text\n", nil},
		{"a fence after a list marker starts the block at the fence",
			"1. ```sh\n   npm i\n   ```\nok",
			[]Span{{Start: 3, End: 24, Type: TypeCode, Lines: 1, Lang: "sh"}}},
		{"a block in a quote closes at a quoted fence",
			"> ```\n> x\n> ```\nok",
			[]Span{{Start: 2, End: 15, Type: TypeCode, Lines: 1}}},
		{"a list marker inside a quote counts too",
			"> - ```js\n>   y\n> ```",
			[]Span{{Start: 4, End: 21, Type: TypeCode, Lines: 1, Lang: "js"}}},
		{"a quoted fence is content in a block outside a quote",
			"```\n> ```\nx\n```",
			[]Span{{Start: 0, End: 15, Type: TypeCode, Lines: 2}}},
		{"a bullet needs a blank after it, so bold before backticks opens nothing", "**```** marks a fence", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeBlocks(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CodeBlocks(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
