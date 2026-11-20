// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"fmt"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// tl builds a small synthetic timeline for the rule tests: one device, and
// presence samples computed from the input a test declares, so a case says
// what the person did and the samples follow.

// base is a UTC minute boundary.
const base = int64(1_795_046_400_000)

// at returns the time s seconds after base.
func at(s int64) int64 { return base + s*1000 }

// atMS returns the time ms milliseconds after base.
func atMS(ms int64) int64 { return base + ms }

type tl struct {
	n      int
	evs    []event.Event
	inputs []int64 // any input, sorted
	keys   []int64 // key presses, sorted
	// keyCounter says the OS reports key presses apart from other input,
	// as macOS does and the typing module does elsewhere.
	keyCounter bool
	// locked is set between a lock and an unlock, so samples say so.
	locked bool
}

func newTL(keyCounter bool) *tl { return &tl{keyCounter: keyCounter} }

// mouse declares pointer input at the given times.
func (b *tl) mouse(ts ...int64) {
	b.inputs = append(b.inputs, ts...)
	slices.Sort(b.inputs)
}

// press declares key presses at the given times; they are input too.
func (b *tl) press(ts ...int64) {
	b.mouse(ts...)
	b.keys = append(b.keys, ts...)
	slices.Sort(b.keys)
}

// every returns from, from+step, ..., up to to.
func every(from, to, step int64) []int64 {
	var ts []int64
	for t := from; t <= to; t += step {
		ts = append(ts, t)
	}
	return ts
}

// emit adds an event. Records gets them in time order, keeping the order
// they were emitted in within a millisecond, and numbers them.
func (b *tl) emit(t int64, env event.Envelope, p event.Payload) event.Event {
	b.n++
	env.EventID = fmt.Sprintf("019a0000-0000-7000-8000-%012x", b.n)
	env.DeviceID, env.BootID, env.T = "d_test", "b_test", t
	e := event.Event{Envelope: env, Data: p}
	b.evs = append(b.evs, e)
	return e
}

func (b *tl) records() []event.Event {
	out := slices.Clone(b.evs)
	slices.SortStableFunc(out, func(x, y event.Event) int {
		switch {
		case x.T < y.T:
			return -1
		case x.T > y.T:
			return 1
		}
		return 0
	})
	for i := range out {
		out[i].Seq = uint64(i + 1)
	}
	return out
}

func (b *tl) os(t int64, p event.Payload) event.Event {
	return b.emit(t, event.Envelope{Source: event.SourceOS, Via: event.ViaOS, Harness: event.HarnessNone,
		SessionKey: event.NoSession}, p)
}

// sample takes an idle sample at t from the declared input.
func (b *tl) sample(t int64) {
	idle := func(ts []int64) float64 {
		last := t - 3_600_000
		for _, x := range ts {
			if x <= t {
				last = x
			}
		}
		return float64(t-last) / 1000
	}
	d := &event.OSPresence{IdleAnyS: idle(b.inputs), Locked: b.locked}
	if b.keyCounter {
		k := idle(b.keys)
		d.IdleKeysS = &k
	}
	b.os(t, d)
}

// samples takes a sample on every slot boundary in [from, to].
func (b *tl) samples(from, to int64) {
	for t := floorSlot(from + event.SlotMS - 1); t <= to; t += event.SlotMS {
		b.sample(t)
	}
}

func (b *tl) focus(t int64, class event.AppClass, project string, hosted ...string) {
	b.os(t, &event.OSFocus{AppClass: class, AppHash: "a_" + string(class), FocusProject: project, HostedSessions: hosted})
}

func (b *tl) power(t int64, op event.PowerOp) {
	switch op {
	case event.PowerLock:
		b.locked = true
	case event.PowerUnlock:
		b.locked = false
	}
	b.os(t, &event.OSPower{Op: op})
}

// harness adds a record of session sess that arrived via v.
func (b *tl) harness(t int64, v event.Via, sess, project string, p event.Payload) event.Event {
	src := event.SourceHook
	switch v {
	case event.ViaLog, event.ViaBridge, event.ViaBridgeUnverified:
		src = event.SourceLog
	case event.ViaTrack:
		src = event.SourceCLI
	}
	return b.emit(t, event.Envelope{Source: src, Via: v, Harness: event.HarnessClaudeCode, SessionKey: sess,
		ProjectKey: project}, p)
}

func (b *tl) start(t int64, sess, project string, entry event.Entrypoint, parent event.ParentClass, tty bool) {
	b.harness(t, event.ViaHook, sess, project, &event.SessionStart{StartKind: event.StartStartup, Entrypoint: entry,
		ParentClass: parent, HasTTY: tty})
}

// prompt adds a typed prompt of n characters with turn key turn.
func (b *tl) prompt(t int64, v event.Via, sess, project, turn string, n int) event.Event {
	return b.harness(t, v, sess, project, &event.PromptSubmit{TurnKey: turn, LenChars: n, TypedLenEst: n,
		OriginDeclared: event.DeclaredNone})
}

func (b *tl) editor(t int64, v event.Via, project string, act event.EditorAct, src event.EditSource) event.Event {
	return b.emit(t, event.Envelope{Source: event.SourceEditor, Via: v, Harness: event.HarnessNone,
		SessionKey: event.NoSession, ProjectKey: project},
		&event.EditorActivity{Editor: "vscode", Act: act, FileHash: "f_1", LinesChanged: 1, EditSource: src})
}

// terminalSession is the usual start of a case: a terminal in focus at
// base hosting session s_1 of project p_1, which a person started from an
// interactive shell.
func terminalSession(b *tl) {
	b.focus(at(0), event.AppTerminal, "p_1", "s_1")
	b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
}

// run scores the timeline in one Step and returns everything derived.
func (b *tl) run(t *testing.T, d Device) Derived {
	t.Helper()
	return scoreAll(t, d, b.records())
}

func anchorByID(d Derived, id string) (Anchor, bool) {
	i := slices.IndexFunc(d.Anchors, func(a Anchor) bool { return a.ID == id })
	if i < 0 {
		return Anchor{}, false
	}
	return d.Anchors[i], true
}

func turnByKey(d Derived, key string) (Turn, bool) {
	i := slices.IndexFunc(d.Turns, func(tu Turn) bool { return tu.TurnKey == key })
	if i < 0 {
		return Turn{}, false
	}
	return d.Turns[i], true
}

func slotAt(d Derived, t int64) (Slot, bool) {
	i := slices.IndexFunc(d.Slots, func(s Slot) bool { return s.Start == t })
	if i < 0 {
		return Slot{}, false
	}
	return d.Slots[i], true
}
