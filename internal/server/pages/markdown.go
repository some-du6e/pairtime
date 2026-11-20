// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// render turns the Markdown the pages are written in into HTML. It knows a
// small subset and is strict about it: anything else is an error rather
// than a guess, so a template mistake fails pairtimed's start and CI, not
// a reader. Raw HTML is always escaped, never passed through.
//
// Blocks, separated by blank lines:
//
//   - headings, "# " to "#### ", each with an id made from its text;
//   - paragraphs, whose lines join with spaces;
//   - "- " and "1. " lists, one level deep, where a line indented by two
//     spaces continues the item above it;
//   - "> " quotes, holding paragraphs;
//   - tables: a "| a | b |" header, a "|---|---|" row, then rows with as
//     many cells as the header.
//
// Inline: **strong**, *emphasis*, `code`, and [text](link) where the link
// is https:, mailto:, a path starting with "/" or a "#heading". A
// backslash before ASCII punctuation stands for the character itself.
func render(src string) (string, error) {
	r := &renderer{ids: make(map[string]int)}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); {
		n, err := r.block(lines[i:])
		if err != nil {
			return "", fmt.Errorf("markdown line %d: %w", i+1, err)
		}
		if n < 1 {
			// A block that used no line would loop here for ever.
			return "", fmt.Errorf("markdown line %d: the renderer can't read this line", i+1)
		}
		i += n
	}
	return r.b.String(), nil
}

var (
	headingLine   = regexp.MustCompile(`^(#{1,4}) (\S.*)$`)
	orderedItem   = regexp.MustCompile(`^[0-9]{1,3}\. `)
	separatorCell = regexp.MustCompile(`^:?-{3,}:?$`)
	allowedLink   = regexp.MustCompile(`^(https://[^\s()<>"'\\]+|mailto:[^\s()<>"'\\]+|/([^/\s()<>"'\\][^\s()<>"'\\]*)?|#[a-z0-9-]+)$`)
)

type renderer struct {
	b strings.Builder
	// ids counts heading ids, so a repeated one gets a number.
	ids map[string]int
}

// block renders the block that starts at lines[0] and returns how many
// lines it used. A line of nothing but whitespace, a no-break space or a
// stray \r included, is blank, as it is to paragraph.
func (r *renderer) block(lines []string) (int, error) {
	line := strings.TrimRight(lines[0], " \t")
	switch {
	case strings.TrimSpace(line) == "":
		return 1, nil
	case strings.HasPrefix(line, "#"):
		return 1, r.heading(line)
	case strings.HasPrefix(line, "|"):
		return r.table(lines)
	case strings.HasPrefix(line, "- "):
		return r.list(lines, "ul", func(l string) (string, bool) { return strings.CutPrefix(l, "- ") })
	case orderedItem.MatchString(line):
		return r.list(lines, "ol", func(l string) (string, bool) {
			loc := orderedItem.FindStringIndex(l)
			if loc == nil {
				return "", false
			}
			return l[loc[1]:], true
		})
	case strings.HasPrefix(line, ">"):
		return r.quote(lines)
	case line[0] == ' ' || line[0] == '\t':
		return 0, errors.New("an indented line must continue a list item")
	default:
		return r.paragraph(lines)
	}
}

// startsBlock reports whether line starts a block other than a paragraph.
func startsBlock(line string) bool {
	return strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") || strings.HasPrefix(line, "- ") ||
		orderedItem.MatchString(line) || strings.HasPrefix(line, ">")
}

func (r *renderer) paragraph(lines []string) (int, error) {
	var parts []string
	n := 0
	for n < len(lines) {
		l := strings.TrimSpace(lines[n])
		if l == "" || (n > 0 && startsBlock(l)) {
			break
		}
		parts = append(parts, l)
		n++
	}
	text, err := inline(strings.Join(parts, " "))
	if err != nil {
		return 0, err
	}
	r.b.WriteString("<p>" + text + "</p>\n")
	return n, nil
}

func (r *renderer) heading(line string) error {
	m := headingLine.FindStringSubmatch(line)
	if m == nil {
		return errors.New("a heading is one to four #s, a space and its text")
	}
	text, err := inline(m[2])
	if err != nil {
		return err
	}
	id := slug(m[2])
	if id == "" {
		return errors.New("a heading needs a letter or digit for its id")
	}
	r.ids[id]++
	if n := r.ids[id]; n > 1 {
		id += "-" + strconv.Itoa(n)
	}
	level := strconv.Itoa(len(m[1]))
	r.b.WriteString("<h" + level + ` id="` + id + `">` + text + "</h" + level + ">\n")
	return nil
}

// slug makes a heading's id: its ASCII letters and digits, lowercased,
// with a hyphen where spaces or hyphens were.
func slug(s string) string {
	var b strings.Builder
	gap := false
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			gap = false
			b.WriteRune(c)
		case c == ' ' || c == '-':
			gap = true
		}
	}
	return b.String()
}

func (r *renderer) list(lines []string, tag string, item func(string) (string, bool)) (int, error) {
	var items [][]string
	n := 0
	for n < len(lines) {
		l := strings.TrimRight(lines[n], " \t")
		if rest, ok := item(l); ok {
			items = append(items, []string{strings.TrimSpace(rest)})
		} else if len(items) > 0 && (strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "\t")) {
			last := len(items) - 1
			items[last] = append(items[last], strings.TrimSpace(l))
		} else {
			break
		}
		n++
	}
	var b strings.Builder
	b.WriteString("<" + tag + ">\n")
	for _, it := range items {
		text, err := inline(strings.Join(it, " "))
		if err != nil {
			return 0, err
		}
		b.WriteString("<li>" + text + "</li>\n")
	}
	b.WriteString("</" + tag + ">\n")
	r.b.WriteString(b.String())
	return n, nil
}

func (r *renderer) quote(lines []string) (int, error) {
	var paras, cur []string
	flush := func() {
		if len(cur) > 0 {
			paras = append(paras, strings.Join(cur, " "))
			cur = nil
		}
	}
	n := 0
	for n < len(lines) {
		rest, ok := strings.CutPrefix(strings.TrimRight(lines[n], " \t"), ">")
		if !ok {
			break
		}
		if rest = strings.TrimSpace(rest); rest == "" {
			flush()
		} else {
			cur = append(cur, rest)
		}
		n++
	}
	flush()
	var b strings.Builder
	b.WriteString("<blockquote>\n")
	for _, p := range paras {
		text, err := inline(p)
		if err != nil {
			return 0, err
		}
		b.WriteString("<p>" + text + "</p>\n")
	}
	b.WriteString("</blockquote>\n")
	r.b.WriteString(b.String())
	return n, nil
}

func (r *renderer) table(lines []string) (int, error) {
	var rows [][]string
	n := 0
	for n < len(lines) {
		l := strings.TrimSpace(lines[n])
		if !strings.HasPrefix(l, "|") {
			break
		}
		cells, err := splitRow(l)
		if err != nil {
			return 0, fmt.Errorf("table row %d: %w", n+1, err)
		}
		rows = append(rows, cells)
		n++
	}
	if len(rows) < 2 {
		return 0, errors.New("a table needs a header row and a |---| row")
	}
	for _, c := range rows[1] {
		if !separatorCell.MatchString(c) {
			return 0, errors.New("a table's second row must be its |---| row")
		}
	}
	width := len(rows[0])
	var b strings.Builder
	b.WriteString("<div class=\"table\"><table>\n<thead><tr>")
	for _, c := range rows[0] {
		text, err := inline(c)
		if err != nil {
			return 0, err
		}
		b.WriteString("<th>" + text + "</th>")
	}
	b.WriteString("</tr></thead>\n<tbody>\n")
	for i, row := range rows {
		if len(row) != width {
			return 0, fmt.Errorf("table row %d has %d cells, and the header has %d", i+1, len(row), width)
		}
		if i < 2 {
			continue
		}
		b.WriteString("<tr>")
		for _, c := range row {
			text, err := inline(c)
			if err != nil {
				return 0, err
			}
			b.WriteString("<td>" + text + "</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody>\n</table></div>\n")
	r.b.WriteString(b.String())
	return n, nil
}

// splitRow splits "| a | b |" into its trimmed cells. A backslash keeps the
// character after it in the cell, so "\|" reaches inline, which turns it
// into a "|".
func splitRow(line string) ([]string, error) {
	if len(line) < 2 || !strings.HasSuffix(line, "|") || strings.HasSuffix(line, `\|`) {
		return nil, errors.New("a table row starts and ends with |")
	}
	inner := line[1 : len(line)-1]
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(inner); i++ {
		switch c := inner[i]; {
		case c == '\\' && i+1 < len(inner):
			cur.WriteByte(c)
			cur.WriteByte(inner[i+1])
			i++
		case c == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, strings.TrimSpace(cur.String())), nil
}

// inline renders the inline markup of s.
func inline(s string) (string, error) {
	var b strings.Builder
	var open []string // the open "strong" and "em" tags, innermost last
	toggle := func(tag string) {
		if len(open) > 0 && open[len(open)-1] == tag {
			open = open[:len(open)-1]
			b.WriteString("</" + tag + ">")
		} else {
			open = append(open, tag)
			b.WriteString("<" + tag + ">")
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && isPunct(s[i+1]):
			b.WriteString(html.EscapeString(s[i+1 : i+2]))
			i += 2
		case c == '`':
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				return "", errors.New("a ` starts a code span that doesn't end")
			}
			b.WriteString("<code>" + html.EscapeString(s[i+1:i+1+end]) + "</code>")
			i += end + 2
		case strings.HasPrefix(s[i:], "**"):
			toggle("strong")
			i += 2
		case c == '*':
			toggle("em")
			i++
		case c == '[':
			text, href, n, err := link(s[i:])
			if err != nil {
				return "", err
			}
			label, err := inline(text)
			if err != nil {
				return "", err
			}
			b.WriteString(`<a href="` + html.EscapeString(href) + `">` + label + "</a>")
			i += n
		default:
			j := i + 1
			for j < len(s) && strings.IndexByte("\\`*[", s[j]) < 0 {
				j++
			}
			b.WriteString(html.EscapeString(s[i:j]))
			i = j
		}
	}
	if len(open) > 0 {
		return "", fmt.Errorf("a <%s> isn't closed, or ** and * overlap", open[len(open)-1])
	}
	return b.String(), nil
}

// link parses the [text](href) at the start of s, and returns its parts
// and its length in bytes.
func link(s string) (text, href string, n int, err error) {
	end := -1
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == ']' {
			end = i
			break
		}
	}
	if end < 0 || end+1 >= len(s) || s[end+1] != '(' {
		return "", "", 0, errors.New(`a [ must start a [text](link); write \[ for a bracket`)
	}
	stop := strings.IndexByte(s[end+2:], ')')
	if stop < 0 {
		return "", "", 0, errors.New("a link's ( has no )")
	}
	href = s[end+2 : end+2+stop]
	if !allowedLink.MatchString(href) {
		return "", "", 0, fmt.Errorf("link %q must be https:, mailto:, a path starting with / or a #heading", href)
	}
	return s[1:end], href, end + 3 + stop, nil
}

// isPunct reports whether c is ASCII punctuation, which a backslash makes
// literal.
func isPunct(c byte) bool {
	return strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", c) >= 0
}

// escape makes s plain text inside Markdown: every character render reads
// as markup comes out as itself, and line breaks become spaces.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '`', '*', '[', ']', '|':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
