// SPDX-License-Identifier: AGPL-3.0-or-later

// Package udiff writes unified diffs, the format of `diff -u` and `git
// diff`, which is how `pairtime install --dry-run` and the consent screen
// show each planned change (DESIGN 5.9).
//
// Lines keep their own endings, so a change to a file with CRLF endings
// shows CRLF lines, and `git apply` or `patch` can apply the diff to the
// file it was made from.
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
	x, y := Lines(a), Lines(b)
	ops := diff(x, y)
	var w strings.Builder
	fmt.Fprintf(&w, "--- %s\n+++ %s\n", fromName, toName)
	for _, h := range hunks(ops) {
		writeHunk(&w, ops[h[0]:h[1]], x, y)
	}
	return w.String()
}

// Lines splits s after each "\n". The last line has no "\n" when s doesn't
// end with one.
func Lines(s []byte) []string {
	var lines []string
	for len(s) > 0 {
		n := bytes.IndexByte(s, '\n') + 1
		if n == 0 {
			n = len(s)
		}
		lines = append(lines, string(s[:n]))
		s = s[n:]
	}
	return lines
}

// op is one step of an edit script: keep x[a] (which equals y[b]), delete
// x[a], or insert y[b]. a and b are also where the step starts in x and y.
type op struct {
	kind byte // ' ', '-' or '+'
	a, b int
}

// diff returns a shortest edit script from x to y, with each run of
// changes written as its deletions and then its insertions.
func diff(x, y []string) []op {
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	var ops []op
	for i := 0; i < pre; i++ {
		ops = append(ops, op{' ', i, i})
	}
	for _, o := range middle(x[pre:len(x)-suf], y[pre:len(y)-suf]) {
		ops = append(ops, op{o.kind, o.a + pre, o.b + pre})
	}
	for i := 0; i < suf; i++ {
		ops = append(ops, op{' ', len(x) - suf + i, len(y) - suf + i})
	}
	return deletionsFirst(ops)
}

// maxTrace bounds the memory Myers' algorithm keeps for its way back, in
// ints. Past it, the middle of the diff is written as one deletion of
// everything and one insertion, which is a correct diff, just not a short
// one.
const maxTrace = 1 << 21

// middle is Myers' O(ND) algorithm ("An O(ND) Difference Algorithm and Its
// Variations", 1986): trace[d] keeps, for each diagonal k from -d to d,
// how far in x the furthest path with d changes on it got.
func middle(x, y []string) []op {
	n, m := len(x), len(y)
	if n == 0 || m == 0 {
		return replaceAll(n, m)
	}
	limit := n + m
	off := limit + 1
	v := make([]int, 2*limit+3)
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
			for i < n && j < m && x[i] == y[j] {
				i++
				j++
			}
			v[off+k] = i
			if i >= n && j >= m {
				return backtrack(trace, n, m)
			}
		}
		used += 2*d + 1
		if used > maxTrace {
			return replaceAll(n, m)
		}
		trace = append(trace, append([]int(nil), v[off-d:off+d+1]...))
	}
	return replaceAll(n, m)
}

// backtrack walks from the end of both sequences back to their start,
// using trace[d-1] to find the step that led to each path with d changes.
func backtrack(trace [][]int, n, m int) []op {
	var rev []op
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
		for i > pi && j > pj {
			i--
			j--
			rev = append(rev, op{' ', i, j})
		}
		if i == pi {
			j--
			rev = append(rev, op{'+', i, j})
		} else {
			i--
			rev = append(rev, op{'-', i, j})
		}
	}
	for i > 0 && j > 0 {
		i--
		j--
		rev = append(rev, op{' ', i, j})
	}
	ops := make([]op, len(rev))
	for k, o := range rev {
		ops[len(rev)-1-k] = o
	}
	return ops
}

// replaceAll deletes n lines and inserts m.
func replaceAll(n, m int) []op {
	ops := make([]op, 0, n+m)
	for i := 0; i < n; i++ {
		ops = append(ops, op{'-', i, 0})
	}
	for j := 0; j < m; j++ {
		ops = append(ops, op{'+', n, j})
	}
	return ops
}

// deletionsFirst reorders each run of changes between two kept lines so
// its deletions come before its insertions, as diff -u writes them, and
// sets each step's a and b to where it starts in x and y.
func deletionsFirst(ops []op) []op {
	out := make([]op, 0, len(ops))
	a, b := 0, 0
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			out = append(out, op{' ', a, b})
			a++
			b++
			i++
			continue
		}
		j := i
		dels, ins := 0, 0
		for ; j < len(ops) && ops[j].kind != ' '; j++ {
			if ops[j].kind == '-' {
				dels++
			} else {
				ins++
			}
		}
		for k := 0; k < dels; k++ {
			out = append(out, op{'-', a, b})
			a++
		}
		for k := 0; k < ins; k++ {
			out = append(out, op{'+', a, b})
			b++
		}
		i = j
	}
	return out
}

// hunks groups ops into hunks, as [start, end) ranges of ops: each change
// with Context kept lines on either side, and two changes in one hunk when
// at most 2*Context kept lines lie between them.
func hunks(ops []op) [][2]int {
	var hs [][2]int
	i := 0
	for {
		for i < len(ops) && ops[i].kind == ' ' {
			i++
		}
		if i == len(ops) {
			return hs
		}
		start := max(i-Context, 0)
		j := i
		for {
			for j < len(ops) && ops[j].kind != ' ' {
				j++
			}
			k := j
			for k < len(ops) && ops[k].kind == ' ' {
				k++
			}
			if k < len(ops) && k-j <= 2*Context {
				j = k
				continue
			}
			break
		}
		end := min(j+Context, len(ops))
		hs = append(hs, [2]int{start, end})
		i = end
	}
}

func writeHunk(w *strings.Builder, ops []op, x, y []string) {
	na, nb := 0, 0
	for _, o := range ops {
		if o.kind != '+' {
			na++
		}
		if o.kind != '-' {
			nb++
		}
	}
	fmt.Fprintf(w, "@@ -%s +%s @@\n", rangeOf(ops[0].a, na), rangeOf(ops[0].b, nb))
	for _, o := range ops {
		line := ""
		switch o.kind {
		case ' ', '-':
			line = x[o.a]
		case '+':
			line = y[o.b]
		}
		w.WriteByte(o.kind)
		w.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
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
