// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// TestTyped holds the reader to SM 2.2 rule 9's measure of a message: the
// app's own text without its machine strings. The texts are invented.
func TestTyped(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"a message with none of T3's machine strings is what was typed", "make the cache key include the locale",
			"make the cache key include the locale"},
		{"the Ultrathink: line T3's web client puts first is not typing", "Ultrathink:\nmake the cache key include the locale",
			"make the cache key include the locale"},
		{"an Ultrathink: line is T3's whatever its case and trailing space", "ultrathink:  \r\nrename it", "rename it"},
		{"Ultrathink: with words after it on its line was typed", "Ultrathink: rename it", "Ultrathink: rename it"},
		{"a message of an Ultrathink: line alone typed nothing", "Ultrathink:", ""},
		{"T3's placeholder for attachments with no text typed nothing", attachmentOnly, ""},
		{"T3's placeholder after an Ultrathink: line typed nothing", "Ultrathink:\n" + attachmentOnly, ""},
		{"the placeholder inside other words is the person's", "see " + attachmentOnly, "see " + attachmentOnly},
		{"a context reference's token is not typing", "compare [main.go](t3-context://v1/file/file_main-go) with the old one",
			"compare  with the old one"},
		{"an image reference's token, with its !, is not typing", "![screenshot](t3-context://v1/image/img_1) what broke?",
			"what broke?"},
		{"a reference of a kind T3 doesn't write is the person's", "[x](t3-context://v1/Bad/id)", "[x](t3-context://v1/Bad/id)"},
		{"a reference with more parts than T3 writes is the person's", "[x](t3-context://v1/file/a/b)", "[x](t3-context://v1/file/a/b)"},
		{"a quote of an earlier answer counts as the person's comment on it",
			"[Assistant quote](t3-citation://v1/env/thread/msg?text=the+agent%27s+words&start=0&end=5&prefix=&suffix=&comment=too+slow) why?",
			"too slow why?"},
		{"a quote with no comment typed nothing", "[Assistant quote](t3-citation://v1/env/thread/msg?text=words&start=0&end=5&prefix=&suffix=)",
			""},
		{"the context block T3 appends for the provider is not typing",
			"fix [a](t3-context://v1/file/f_a) now\n\n<t3_context version=\"1\">\n<context kind=\"file\" id=\"f_a\">\nname: a\n</context>\n</t3_context>",
			"fix  now"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := typed(tc.text); got != tc.want {
				t.Errorf("typed(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestMeasure holds the reader's measurement to SM 2.2: the length of what
// was typed, the go-ahead test on it, and the class the record's f.pc
// carries.
func TestMeasure(t *testing.T) {
	table, err := params.ByVersion("0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("a", maxTextChars)
	cases := []struct {
		name      string
		read      string
		total     int
		lenChars  int
		lowEffort bool
		class     event.PromptClass
	}{
		{"a go-ahead is low effort", "yes", 3, 3, true, event.ClassLowEffort},
		{"a go-ahead after T3's Ultrathink: line is still one", "Ultrathink:\nkeep going", 22, 10, true, event.ClassLowEffort},
		{"twenty typed characters are substantive", "rename the variables", 20, 20, false, event.ClassSubstantive},
		{"a short message off the go-ahead list is low effort by its length", "and the tests", 13, 13, false, event.ClassLowEffort},
		{"length counts characters, not bytes", "ünïcödé wörds hërë", 18, 18, false, event.ClassLowEffort},
		{"a message of attachments alone has nothing typed", attachmentOnly, 121, 0, true, event.ClassLowEffort},
		{"a text longer than the query reads counts the rest by length, and is no go-ahead", long, maxTextChars + 7,
			maxTextChars + 7, false, event.ClassSubstantive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := measure(tc.read, tc.total)
			if m.lenChars != tc.lenChars || m.lowEffort != tc.lowEffort {
				t.Errorf("measure gave %d characters, low effort %v; want %d, %v", m.lenChars, m.lowEffort, tc.lenChars, tc.lowEffort)
			}
			if got := m.class(table); got != tc.class {
				t.Errorf("class %s, want %s", got, tc.class)
			}
		})
	}
	t.Run("the attachment placeholder is T3's 121 characters", func(t *testing.T) {
		if n := len([]rune(attachmentOnly)); n != 121 {
			t.Errorf("it has %d", n)
		}
	})
}
