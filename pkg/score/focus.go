// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"sort"
	"strings"

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
	// set is hosted as a set (hostedSet), so two records host the same
	// sessions exactly when their sets are equal.
	set string
}

// hostedSet returns a list of session keys as one string: sorted, without
// repeats, and joined by spaces, which no key holds (pkg/event's key
// shape). Two lists give the same string exactly when they hold the same
// keys, and comparing two costs their length, so a record listing many
// sessions can't make longest quadratic in them.
func hostedSet(keys []string) string {
	s := slices.Clone(keys)
	slices.Sort(s)
	return strings.Join(slices.Compact(s), " ")
}

// noFocus is what holds the foreground before the first os.focus record.
var noFocus = &focusRec{pos: -1, class: event.AppNone}

// unknown reports whether the client couldn't tell which window was in
// front: an os.focus with app_class unknown (SM 2.5). The foreground before
// the first os.focus record is no unknown one, so that time still credits
// nothing.
func (f *focusRec) unknown() bool { return f.class == event.AppUnknown }

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
				project: d.FocusProject, hosted: d.HostedSessions, set: hostedSet(d.HostedSessions)})
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
//
// Windows and hosted sets are looked up by key, so each piece costs the
// length of its record's set (hostedSet) and not the number of windows and
// sets seen before it: the server re-scores os.focus records a client
// wrote (DESIGN 6.5), and pkg/event lets one list up to 1024 sessions.
func (fl *focusLine) longest(a, b int64) *focusRec {
	type piece struct {
		first *focusRec
		total int64
	}
	type window struct {
		piece
		sets  []*piece
		bySet map[string]*piece
	}
	// windowKey is what sameWindow compares, with noFocus apart.
	type windowKey struct {
		none    bool
		class   event.AppClass
		project string
	}
	var wins []*window
	byKey := map[windowKey]*window{}
	add := func(f *focusRec, n int64) {
		k := windowKey{f == noFocus, f.class, f.project}
		w := byKey[k]
		if w == nil {
			w = &window{piece: piece{first: f}, bySet: map[string]*piece{}}
			byKey[k] = w
			wins = append(wins, w)
		}
		w.total += n
		hs := w.bySet[f.set]
		if hs == nil {
			hs = &piece{first: f}
			w.bySet[f.set] = hs
			w.sets = append(w.sets, hs)
		}
		hs.total += n
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
