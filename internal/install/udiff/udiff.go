// SPDX-License-Identifier: AGPL-3.0-or-later

// Package udiff writes unified diffs, the format of `diff -u` and `git
// diff`, which is how `pairtime install --dry-run` and the consent screen
// show each planned change (DESIGN 5.9).
//
// Lines keep their own endings, so a change to a file with CRLF endings
// shows CRLF lines, and `git apply` or `patch` can apply the diff to the
// file it was made from.
//
// Memory goes with the lines from the first change to the last, not with
// the size of the files. The lines both sides start and end with are
// neither split nor diffed, but for the Context lines next to a change,
// and the edit script keeps each run of changed lines as one step. So a
// 4 MiB settings file that is mostly blank lines, with a short change in
// it, costs what the change does.
package udiff

import (
	"bytes"
	"fmt"
	"strings"
)

// Context is how many unchanged lines a hunk shows around each change.
const Context = 3

// Unified returns the diff that turns a into b, headed by fromName and
// toName, or "" when a and b are equal. Use "/dev/null" as the name of a
// side that doesn't exist.
func Unified(fromName, toName string, a, b []byte) string {
	if bytes.Equal(a, b) {
		return ""
	}
	// The window is what changed, with up to Context of the lines both
	// sides share on either side of it. The shared lines before it are the
	// same on both sides, and so are those after it.
	pre := sharedStart(a, b)
	suf := sharedEnd(a[pre:], b[pre:])
	lo := linesBack(a, pre, Context)
	hiA := linesOn(a, len(a)-suf, Context)
	hiB := len(b) - (len(a) - hiA)
	x, y := split(a[lo:hiA]), split(b[lo:hiB])
	first := bytes.Count(a[:lo], []byte("\n")) // the lines before the window
	var w strings.Builder
	fmt.Fprintf(&w, "--- %s\n+++ %s\n", fromName, toName)
	for _, h := range hunks(diff(x, y), x.n()) {
		writeHunk(&w, h, x, y, first)
	}
	return w.String()
}

// sharedStart returns the length of the whole lines a and b start with
// alike.
func sharedStart(a, b []byte) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return bytes.LastIndexByte(a[:n], '\n') + 1
}

// sharedEnd returns the length of the whole lines a and b end with alike.
// Both start at the start of a line.
func sharedEnd(a, b []byte) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	if (n == len(a) || a[len(a)-1-n] == '\n') && (n == len(b) || b[len(b)-1-n] == '\n') {
		return n // the shared bytes start a line on both sides
	}
	// The first line wholly inside the shared bytes starts after their
	// first "\n".
	i := bytes.IndexByte(a[len(a)-n:], '\n')
	if i < 0 {
		return 0
	}
	return n - i - 1
}

// linesBack returns the start of the line k lines before the line that
// starts at i, or 0 when there are fewer.
func linesBack(s []byte, i, k int) int {
	for ; k > 0 && i > 0; k-- {
		i = bytes.LastIndexByte(s[:i-1], '\n') + 1
	}
	return i
}

// linesOn returns the end of the k lines from the one that starts at i, or
// len(s) when there are fewer.
func linesOn(s []byte, i, k int) int {
	for ; k > 0 && i < len(s); k-- {
		n := bytes.IndexByte(s[i:], '\n')
		if n < 0 {
			return len(s)
		}
		i += n + 1
	}
	return i
}

// text is s split into lines, each with its "\n" but for a last line that
// has none: line i is s[at[i]:at[i+1]]. It holds one offset a line, not a
// copy of each.
type text struct {
	s  []byte
	at []int
}

func split(s []byte) text {
	at := make([]int, 1, bytes.Count(s, []byte("\n"))+2)
	for i := 0; i < len(s); {
		n := bytes.IndexByte(s[i:], '\n')
		if n < 0 {
			i = len(s)
		} else {
			i += n + 1
		}
		at = append(at, i)
	}
	return text{s: s, at: at}
}

// n returns the number of lines.
func (t text) n() int { return len(t.at) - 1 }

func (t text) line(i int) []byte { return t.s[t.at[i]:t.at[i+1]] }

// change is one run of changed lines between two kept ones: the del lines
// of x from line a on become the ins lines of y from line b on. diff -u
// writes the deletions first, then the insertions.
type change struct {
	a, b     int
	del, ins int
}

// diff returns a shortest edit script from x to y, as its changes in order.
func diff(x, y text) []change {
	nx, ny := x.n(), y.n()
	pre := 0
	for pre < nx && pre < ny && bytes.Equal(x.line(pre), y.line(pre)) {
		pre++
	}
	suf := 0
	for suf < nx-pre && suf < ny-pre && bytes.Equal(x.line(nx-1-suf), y.line(ny-1-suf)) {
		suf++
	}
	return middle(x, y, pre, nx-suf, pre, ny-suf)
}

// maxTrace bounds the memory Myers' algorithm keeps for its way back, in
// ints. Past it, the middle of the diff is written as one deletion of
// everything and one insertion, which is a correct diff, just not a short
// one.
const maxTrace = 1 << 21

// maxD is the most changes middle looks for: the trace keeps 2d+1 ints for
// each d, (d+1)² in all, which for d = maxD is past maxTrace for the first
// time. So middle's furthest points need only the diagonals up to it.
const maxD = 1448

// middle is Myers' O(ND) algorithm ("An O(ND) Difference Algorithm and Its
// Variations", 1986) on x's lines x0 to x1 and y's lines y0 to y1: trace[d]
// keeps, for each diagonal k from -d to d, how far in x the furthest path
// with d changes on it got.
func middle(x, y text, x0, x1, y0, y1 int) []change {
	n, m := x1-x0, y1-y0
	if n == 0 || m == 0 {
		return replaceAll(x0, y0, n, m)
	}
	limit := n + m
	reach := min(limit, maxD)
	off := reach + 1
	v := make([]int, 2*reach+3)
	var trace [][]int
	used := 0
	for d := 0; d <= limit; d++ {
		for k := -d; k <= d; k += 2 {
			var i int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				i = v[off+k+1] // down from diagonal k+1: an insertion
			} else {
				i = v[off+k-1] + 1 // right from diagonal k-1: a deletion
			}
			j := i - k
			for i < n && j < m && bytes.Equal(x.line(x0+i), y.line(y0+j)) {
				i++
				j++
			}
			v[off+k] = i
			if i >= n && j >= m {
				cs := backtrack(trace, n, m)
				for c := range cs {
					cs[c].a += x0
					cs[c].b += y0
				}
				return cs
			}
		}
		used += 2*d + 1
		if used > maxTrace {
			return replaceAll(x0, y0, n, m)
		}
		trace = append(trace, append([]int(nil), v[off-d:off+d+1]...))
	}
	return replaceAll(x0, y0, n, m)
}

// backtrack walks from the end of both sequences back to their start,
// using trace[d-1] to find the step that led to each path with d changes,
// and returns the changes in order. Steps with no kept line between them
// make one change.
func backtrack(trace [][]int, n, m int) []change {
	var rev []change
	open := false // whether rev's last change can still grow
	i, j := n, m
	for d := len(trace); d > 0; d-- {
		prev := trace[d-1]
		at := func(k int) int { return prev[k+d-1] }
		k := i - j
		var pk int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		pi := at(pk)
		pj := pi - pk
		if s := min(i-pi, j-pj); s > 0 {
			// Kept lines, back to the step: the change after them is done.
			i -= s
			j -= s
			open = false
		}
		if !open {
			rev = append(rev, change{})
			open = true
		}
		c := &rev[len(rev)-1]
		if i == pi {
			j--
			c.ins++
		} else {
			i--
			c.del++
		}
		c.a, c.b = i, j
	}
	for l, r := 0, len(rev)-1; l < r; l, r = l+1, r-1 {
		rev[l], rev[r] = rev[r], rev[l]
	}
	return rev
}

// replaceAll deletes x's n lines from x0 on and inserts y's m lines from y0
// on.
func replaceAll(x0, y0, n, m int) []change {
	if n == 0 && m == 0 {
		return nil
	}
	return []change{{a: x0, b: y0, del: n, ins: m}}
}

// hunk is what one hunk shows: x's lines a0 to a1 and y's lines b0 to b1,
// which hold changes and the kept lines around them.
type hunk struct {
	a0, a1, b0, b1 int
	changes        []change
}

// hunks groups changes into hunks: each change with Context kept lines on
// either side, and two changes in one hunk when at most 2*Context kept
// lines lie between them. nx is the number of x's lines.
func hunks(cs []change, nx int) []hunk {
	var hs []hunk
	for i := 0; i < len(cs); {
		j := i + 1
		for j < len(cs) && cs[j].a-(cs[j-1].a+cs[j-1].del) <= 2*Context {
			j++
		}
		first, last := cs[i], cs[j-1]
		a0 := max(first.a-Context, 0)
		a1 := min(last.a+last.del+Context, nx)
		hs = append(hs, hunk{
			a0: a0, a1: a1,
			// The lines before the first change and after the last are kept,
			// so as many of them are on each side.
			b0:      first.b - (first.a - a0),
			b1:      last.b + last.ins + (a1 - (last.a + last.del)),
			changes: cs[i:j],
		})
		i = j
	}
	return hs
}

// writeHunk writes h, whose lines are numbered from first on, the number
// of lines before x and y.
func writeHunk(w *strings.Builder, h hunk, x, y text, first int) {
	fmt.Fprintf(w, "@@ -%s +%s @@\n", rangeOf(first+h.a0, h.a1-h.a0), rangeOf(first+h.b0, h.b1-h.b0))
	at := h.a0
	for _, c := range h.changes {
		writeLines(w, ' ', x, at, c.a)
		writeLines(w, '-', x, c.a, c.a+c.del)
		writeLines(w, '+', y, c.b, c.b+c.ins)
		at = c.a + c.del
	}
	writeLines(w, ' ', x, at, h.a1)
}

// writeLines writes t's lines from to to, each after kind.
func writeLines(w *strings.Builder, kind byte, t text, from, to int) {
	for i := from; i < to; i++ {
		line := t.line(i)
		w.WriteByte(kind)
		w.Write(line)
		if !bytes.HasSuffix(line, []byte("\n")) {
			w.WriteString("\n\\ No newline at end of file\n")
		}
	}
}

// rangeOf writes a hunk's range as diff -u does: "start,count", just
// "start" when the count is 1, and the line before the hunk when the count
// is 0. start is 0-based.
func rangeOf(start, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", start)
	case 1:
		return fmt.Sprintf("%d", start+1)
	default:
		return fmt.Sprintf("%d,%d", start+1, count)
	}
}
