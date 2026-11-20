// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"sort"

	"github.com/some-du6e/pairtime/pkg/event"
)

// focusRec is one os.focus record: what held the foreground from its time
// until the next one.
type focusRec struct {
	pos     int // index in the records
	t       int64
	class   event.AppClass
	project string
	hosted  []string
}

// noFocus is what holds the foreground before the first os.focus record.
var noFocus = &focusRec{pos: -1, class: event.AppNone}

// hosts reports whether the window hosts harness session key (SM 3.2).
func (f *focusRec) hosts(key string) bool {
	return key != "" && key != event.NoSession && slices.Contains(f.hosted, key)
}

// sameWindow reports whether g is the same foreground window as f, so
// that moving from f to g is no focus change. A new record for the same
// window, which the agent writes when a harness session starts or ends
// under it, isn't one. It compares only what a ledger record shares, the
// app class and the project: app_hash is local, so the server's exact
// re-score (DESIGN 6.5) never sees it, and two apps of one class on one
// project count as one window on both sides.
func (f *focusRec) sameWindow(g *focusRec) bool {
	return f.class == g.class && f.project == g.project
}

// focusLine is the foreground over time, from os.focus records.
type focusLine struct {
	recs []*focusRec
}

func buildFocus(recs []event.Event) *focusLine {
	fl := &focusLine{}
	for i, e := range recs {
		if d, ok := e.Data.(*event.OSFocus); ok {
			fl.recs = append(fl.recs, &focusRec{pos: i, t: e.T, class: d.AppClass,
				project: d.FocusProject, hosted: d.HostedSessions})
		}
	}
	return fl
}

// before returns what had focus just before the record at pos: the last
// os.focus record before it in record order.
func (fl *focusLine) before(pos int) *focusRec {
	i := sort.Search(len(fl.recs), func(i int) bool { return fl.recs[i].pos >= pos })
	if i == 0 {
		return noFocus
	}
	return fl.recs[i-1]
}

// at returns what had focus at time t: the last os.focus record at or
// before t.
func (fl *focusLine) at(t int64) *focusRec {
	i := sort.Search(len(fl.recs), func(i int) bool { return fl.recs[i].t > t })
	if i == 0 {
		return noFocus
	}
	return fl.recs[i-1]
}

// changed reports whether the foreground moved to another window at a
// time in (from, to].
func (fl *focusLine) changed(from, to int64) bool {
	prev := fl.at(from)
	i := sort.Search(len(fl.recs), func(i int) bool { return fl.recs[i].t > from })
	for ; i < len(fl.recs) && fl.recs[i].t <= to; i++ {
		if !prev.sameWindow(fl.recs[i]) {
			return true
		}
		prev = fl.recs[i]
	}
	return false
}

// longest returns what held focus longest in [a, b) (SM 2.5): the window
// whose pieces of focus in it add up to the most time, the one to have
// focus first on a tie. A window's focus comes in pieces when another
// window took it in between, or when the agent wrote it again because a
// session started or ended under it. Of the winning window's hosted sets
// it takes the one whose pieces add up to the most time, again the first
// on a tie (E-6.6-codes), and returns that set's first piece, whose hosted
// sessions are the slot's. Focus before the first os.focus record, noFocus,
// is a window of its own.
func (fl *focusLine) longest(a, b int64) *focusRec {
	type hostedSet struct {
		first *focusRec
		total int64
	}
	type window struct {
		first *focusRec
		total int64
		sets  []*hostedSet
	}
	var wins []*window
	add := func(f *focusRec, n int64) {
		var w *window
		for _, x := range wins {
			if (x.first == noFocus) == (f == noFocus) && x.first.sameWindow(f) {
				w = x
				break
			}
		}
		if w == nil {
			w = &window{first: f}
			wins = append(wins, w)
		}
		w.total += n
		for _, hs := range w.sets {
			if sameSet(hs.first.hosted, f.hosted) {
				hs.total += n
				return
			}
		}
		w.sets = append(w.sets, &hostedSet{first: f, total: n})
	}
	cur, from := fl.at(a), a
	i := sort.Search(len(fl.recs), func(i int) bool { return fl.recs[i].t > a })
	for {
		to := b
		if i < len(fl.recs) && fl.recs[i].t < b {
			to = fl.recs[i].t
		}
		add(cur, to-from)
		if to == b {
			break
		}
		cur, from = fl.recs[i], to
		i++
	}
	win := wins[0]
	for _, w := range wins[1:] {
		if w.total > win.total {
			win = w
		}
	}
	best := win.sets[0]
	for _, hs := range win.sets[1:] {
		if hs.total > best.total {
			best = hs
		}
	}
	return best.first
}

// sameSet reports whether two lists of session keys hold the same keys, in
// whatever order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, k := range a {
		if !slices.Contains(b, k) {
			return false
		}
	}
	for _, k := range b {
		if !slices.Contains(a, k) {
			return false
		}
	}
	return true
}
