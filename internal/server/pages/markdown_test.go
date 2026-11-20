// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    string
		wantErr string // substring; "" means render must succeed
	}{
		{name: "a heading gets an id from its text", src: "## When you submit hours", want: `<h2 id="when-you-submit-hours">When you submit hours</h2>` + "\n"},
		{name: "a repeated heading gets a numbered id", src: "## A\n\n## A", want: `<h2 id="a">A</h2>` + "\n" + `<h2 id="a-2">A</h2>` + "\n"},
		{name: "a heading needs a space after its #s", src: "##A", wantErr: "a heading is"},
		{name: "the lines of a paragraph join with spaces", src: "one\ntwo", want: "<p>one two</p>\n"},
		{name: "a blank line starts a new paragraph", src: "one\n\ntwo", want: "<p>one</p>\n<p>two</p>\n"},
		{name: "raw HTML is escaped, never passed through", src: `<script>alert("x")</script>`, want: "<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</p>\n"},
		{name: "strong and emphasis", src: "**a** and *b*", want: "<p><strong>a</strong> and <em>b</em></p>\n"},
		{name: "emphasis nests inside strong", src: "**a *b* c**", want: "<p><strong>a <em>b</em> c</strong></p>\n"},
		{name: "an unclosed strong is refused", src: "**a", wantErr: "isn't closed"},
		{name: "overlapping strong and emphasis are refused", src: "*a **b* c**", wantErr: "isn't closed"},
		{name: "a code span keeps its text as it is", src: "`a*b<c`", want: "<p><code>a*b&lt;c</code></p>\n"},
		{name: "a code span must end", src: "`a", wantErr: "doesn't end"},
		{name: "a backslash makes markup literal", src: `\*a\* \[b\] \| \\ \`, want: "<p>*a* [b] | \\ \\</p>\n"},
		{name: "an https link", src: "[the code](https://github.com/some-du6e/pairtime)", want: `<p><a href="https://github.com/some-du6e/pairtime">the code</a></p>` + "\n"},
		{name: "a mailto link", src: "[a@example.org](mailto:a@example.org)", want: `<p><a href="mailto:a@example.org">a@example.org</a></p>` + "\n"},
		{name: "a link to a heading on the page", src: "[below](#how-long-we-keep-things)", want: `<p><a href="#how-long-we-keep-things">below</a></p>` + "\n"},
		{name: "a link to a path on this server", src: "[notice](/privacy)", want: `<p><a href="/privacy">notice</a></p>` + "\n"},
		{name: "a javascript: link is refused", src: "[x](javascript:alert(1))", wantErr: "must be https:"},
		{name: "a protocol-relative link is refused", src: "[x](//evil.example)", wantErr: "must be https:"},
		{name: "an http link is refused", src: "[x](http://example.org)", wantErr: "must be https:"},
		{name: "a bracket that doesn't start a link is refused", src: "a [b] c", wantErr: "must start a [text](link)"},
		{name: "a list item continues on an indented line", src: "- a\n  b\n- c", want: "<ul>\n<li>a b</li>\n<li>c</li>\n</ul>\n"},
		{name: "an ordered list", src: "1. a\n2. b", want: "<ol>\n<li>a</li>\n<li>b</li>\n</ol>\n"},
		{name: "a paragraph ends where a list starts", src: "Before:\n- a", want: "<p>Before:</p>\n<ul>\n<li>a</li>\n</ul>\n"},
		{name: "an indented line outside a list is refused", src: "  a", wantErr: "indented line"},
		{name: "a quote holds paragraphs", src: "> a\n> b\n>\n> c", want: "<blockquote>\n<p>a b</p>\n<p>c</p>\n</blockquote>\n"},
		{
			name: "a table has a header and rows, and \\| stays in its cell",
			src:  "| A | B |\n|---|---|\n| 1 | 2 \\| 3 |",
			want: "<div class=\"table\"><table>\n<thead><tr><th>A</th><th>B</th></tr></thead>\n<tbody>\n<tr><td>1</td><td>2 | 3</td></tr>\n</tbody>\n</table></div>\n",
		},
		{name: "a table row with the wrong number of cells is refused", src: "| A | B |\n|---|---|\n| 1 |", wantErr: "has 1 cells"},
		{name: "a table needs its |---| row", src: "| A | B |\n| 1 | 2 |", wantErr: "second row"},
		{name: "a table row must end with |", src: "| A | B |\n|---|---|\n| 1 | 2", wantErr: "starts and ends with |"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(tt.src)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("render: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("render = %q, want an error containing %q", got, tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("render error = %q, want it to contain %q", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("render =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestEscape(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "markup characters come out as themselves", in: `a*b **c** [d](https://x.org) | e \ f`},
		{name: "a code span's backticks come out as themselves", in: "`x`"},
		{name: "plain text is unchanged", in: "Hack Club HQ, 212 Battery St"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := inline(escape(tt.in))
			if err != nil {
				t.Fatalf("inline(escape(%q)): %v", tt.in, err)
			}
			if want := htmlEscape(tt.in); got != want {
				t.Errorf("inline(escape(%q)) = %q, want %q", tt.in, got, want)
			}
		})
	}
}

// htmlEscape is html.EscapeString, kept apart so the test states what it
// expects without calling the code under test.
func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "'", "&#39;", "<", "&lt;", ">", "&gt;", `"`, "&#34;").Replace(s)
}
