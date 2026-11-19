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
	app     string // app_hash; local, so absent from shareable records
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
// under it, isn't one.
func (f *focusRec) sameWindow(g *focusRec) bool {
	return f.class == g.class && f.project == g.project && f.app == g.app
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
				project: d.FocusProject, hosted: d.HostedSessions, app: d.AppHash})
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

// longest returns what held focus longest in [a, b), the earliest on a
// tie (SM 2.5).
func (fl *focusLine) longest(a, b int64) *focusRec {
	best, bestLen := fl.at(a), int64(-1)
	cur, from := best, a
	i := sort.Search(len(fl.recs), func(i int) bool { return fl.recs[i].t > a })
	for {
		to := b
		if i < len(fl.recs) && fl.recs[i].t < b {
			to = fl.recs[i].t
		}
		if n := to - from; n > bestLen {
			best, bestLen = cur, n
		}
		if to == b {
			return best
		}
		cur, from = fl.recs[i], to
		i++
	}
}
