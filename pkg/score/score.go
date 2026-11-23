// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// Device holds what the scorer needs to know about the machine that
// recorded the events and that the events don't say themselves.
type Device struct {
	// TypingModule is set when input_keys comes from pairtime's opt-in
	// typing module, as on Windows and X11, rather than from the OS, as
	// macOS's key-down idle time does (DESIGN 6.2 D16). Anchors whose
	// keyboard evidence it supplies carry the typing_module label.
	TypingModule bool
	// VM is set on a machine labeled as a virtual machine. There the
	// typing module alone can't make an anchor level A, and every anchor
	// carries the vm label (DESIGN 6.2 D17).
	VM bool
}

// State is everything the scorer carries from one Step to the next. The
// zero State is not usable; start from NewState. A State is a value:
// Step returns a new one and never changes the one it was given.
type State struct {
	table  *params.Table
	device Device
	// recs holds every record Step has been given, without duplicates, in
	// record order (recordLess).
	recs []event.Event
	// high is the largest t of any record, 0 before the first.
	high int64
	// done is the FinalBefore of the last Step: everything derived for a
	// time before it has been emitted as final.
	done int64
}

// samplerRecord reports whether e is a record of the presence sampler's:
// an os.presence sample, or an os.power, which the sampler records with a
// sample after it (DESIGN 3.3). The agent stamps their t itself, from the
// device's clock, and they come every 15 to 60 s while a project context
// is live, so they are the scorer's clock (State.now), and they decide
// which slots are a sampling device's (presence.neverAt). An os.focus is
// neither, though the agent stamps it too: the focus component writes one
// only when focus changes, and where it can't see focus, as on a machine
// with no desktop, once each time the agent starts (SM 1.2, 2.1), so it
// says nothing about how far the present has come.
func samplerRecord(e event.Event) bool {
	switch e.Data.(type) {
	case *event.OSPresence, *event.OSPower:
		return true
	}
	return false
}

// stampedRecord reports whether the agent stamped e's t itself, as it
// happened, by a path whose time no other program gives: a hook's, its own
// watchers' (os) or an agent app reader's (app). A log reader's or the
// bridge's record carries the time a transcript or a heartbeat gave it,
// which can be skewed, and so do the other paths' records. One that isn't
// the sampler's, dated more than BUCKET_TTL_S after the sampler's latest
// record, shows the agent running without its sampler, which ends that
// record's reach once the newest record is rules.late past it (SM 2.1,
// sampledReaches): it no longer keeps the clock (State.now), and the slots
// after it are a never-sampling device's (presence.neverAt).
func stampedRecord(e event.Event) bool {
	switch e.Via {
	case event.ViaHook, event.ViaOS, event.ViaApp:
		return true
	}
	return false
}

// now is the time the scorer takes as the present, given the sampler's
// records and their reaches (sampledReaches): the largest t of any record,
// but no more than PENDING_WINDOW_S past a record of the presence
// sampler's (samplerRecord) that keeps the clock. A record dated further
// ahead, such as a bridge prompt whose transcript timestamp is hours off,
// so stays pending until the samples reach it, instead of making every
// real record after it come too late to be output. The sampler's latest
// record keeps the clock until its reach ends: another record the agent
// stamps itself (stampedRecord) is dated more than BUCKET_TTL_S after it,
// which shows the agent running without its sampler, and the newest record
// is rules.late past that one. An earlier record of the sampler's keeps
// the clock while its fate is open (reach.held): while a record that ends
// its reach waits like that, since a sample dated before that record can
// still arrive and keep the reach; and while the sampler's next record,
// more than BUCKET_TTL_S after it, is less than rules.late behind the
// newest record, since a stamped record dated inside that gap can still
// arrive and end the reach. Since settle is PENDING_WINDOW_S, nothing after
// a record that keeps the clock is final. With no record of the sampler's
// keeping it, as on a
// device whose agent never samples, now is high: nothing else keeps time
// there, and a minute is final once a record is dated PENDING_WINDOW_S
// past its end (SM 2.1).
func (s State) now(r rules, reaches []reach) int64 {
	for i, c := range reaches {
		if c.held || !c.ended && i == len(reaches)-1 {
			return min(s.high, c.t+r.pendingWindow)
		}
	}
	return s.high
}

// NewState returns the state of a scorer that has seen no records and
// scores with table t the records of the device d describes. t must hold
// every parameter the scorer reads: NewState panics on a table older than
// one of them, such as table 0.1.0, which predates INPUT_RELEASE_GRACE_MS,
// or table 0.2.0, which predates RELAY_MATCH_S and RELAY_SEEN_LAG_S
// (pkg/params, added). The agent scores with the newest table, and pkg/params
// checks that it holds them all.
func NewState(t *params.Table, d Device) State {
	if t == nil {
		panic("score: NewState needs a parameter table")
	}
	newRules(t)
	return State{table: t, device: d}
}

// Params returns the table the state scores with.
func (s State) Params() *params.Table { return s.table }

// Device returns the device the state scores for.
func (s State) Device() Device { return s.device }

// High returns the largest t, in Unix milliseconds, of any record the
// state holds, or 0 if it holds none.
func (s State) High() int64 { return s.high }

// Len returns the number of records the state holds.
func (s State) Len() int { return len(s.recs) }

// Output is what one Step derived.
type Output struct {
	// FinalBefore is the time, in Unix milliseconds on a slot boundary,
	// before which nothing derived will change again. It never goes back.
	FinalBefore int64
	// Final holds what became final in this Step: every derived record
	// whose time is before FinalBefore and wasn't final after the previous
	// Step. Over a state's life each derived record is in exactly one
	// Step's Final, so the Finals of successive Steps, followed by the last
	// Step's Pending, are what one Step over all the records gives.
	Final Derived
	// Pending is the current view of every derived record that may still
	// change: late records can still arrive for it (DESIGN 3.7), or a rule
	// looks further ahead than the records reach.
	Pending Derived
}

// Derived holds the records the scorer derives from events (SM 1.3), as
// far as WP-SC1 and WP-SC2 build them: presence slots, turns, anchors, the
// silent runs the return rule credits, the fate of each `pairtime track`
// call and each agent app's relay.message, review flags, what each turn put
// in its reading bucket, and every slot's credit. Each list is in time
// order, ties broken by ids, so equal inputs give equal lists.
type Derived struct {
	Slots   []Slot        `json:"slots"`
	Turns   []Turn        `json:"turns"`
	Anchors []Anchor      `json:"anchors"`
	Returns []Return      `json:"returns"`
	Tracks  []TrackCall   `json:"tracks"`
	Relays  []RelayRecord `json:"relays"`
	Flags   []Flag        `json:"flags"`
	Fills   []Fill        `json:"fills"`
	Credits []Credit      `json:"credits"`
}

// Step scores records on top of state s and returns the new state and what
// it derived. Records may come in any order and may repeat: Step keeps
// them in record order, by t, then seq, then event_id, and drops a record
// whose event_id it already holds. It reads no clock and does no I/O;
// "now" is the largest t it has seen, but no more than PENDING_WINDOW_S
// past a sample or power record, which the presence sampler writes, that
// keeps the clock (State.now). Every record must pass event.Validate, as
// every record in the ledger does.
//
// A record whose t is before the state's FinalBefore still counts toward
// what is pending, but what is final stays final.
func Step(s State, records []event.Event) (State, Output) {
	if s.table == nil {
		panic("score: Step on a State not made by NewState")
	}
	next := State{table: s.table, device: s.device, high: s.high, done: s.done}
	next.recs = merge(s.recs, records)
	for _, e := range records {
		if e.Data != nil {
			next.high = max(next.high, e.T)
		}
	}
	r := newRules(s.table)
	reaches := sampledReaches(r, next.recs, next.high)
	now := next.now(r, reaches)
	all, lookups, relays := derive(r, s.device, next.recs, now, reaches)
	fb := s.done
	if len(next.recs) > 0 {
		to := floorSlot(now - r.settle)
		// A browser run's credit waits for the slot after it: a lookup
		// needs credit within LOOKUP_BRACKET_S on both sides (SM 2.5). So
		// nothing from a run whose end bracket isn't final yet is final.
		// derive lists only the runs with credit before them: once the run's
		// start is final, so is the bracket before it, and a run with none
		// there is no lookup whatever follows. SM 2.5 sets no bound on a
		// run's length, so the hold has none either
		// (docs/errata/2026-11-20-lookup-lookahead.md).
		for _, sp := range lookups {
			if sp.from < to && sp.to+r.lookupBracket > to {
				to = min(to, sp.from)
			}
		}
		// An agent app's message that hasn't paired yet holds the minutes a
		// lead it could open can touch until it pairs or RELAY_MATCH_S after
		// its stamp passes (SM 2.3, deriver.relayHolds). A relayed lead
		// starts at most COMPOSE_MAX_S before the stamp, and RELAY_MATCH_S +
		// COMPOSE_MAX_S is PENDING_WINDOW_S in table 0.3.0 (SM 8.1), so the
		// hold binds only for the think part of a prompt that rules 6 and 7
		// make typed at the stamp, and for a later table that widens either.
		for _, h := range relays {
			to = min(to, floorSlot(h))
		}
		fb = max(fb, to)
	}
	next.done = fb
	out := Output{FinalBefore: fb}
	out.Final, out.Pending = all.split(s.done, fb)
	return next, out
}

// merge returns old plus the new records with data whose event_id old
// doesn't hold, in record order. old is not modified.
func merge(old, add []event.Event) []event.Event {
	seen := make(map[string]bool, len(old)+len(add))
	for _, e := range old {
		seen[e.EventID] = true
	}
	out := make([]event.Event, len(old), len(old)+len(add))
	copy(out, old)
	sorted := true
	for _, e := range add {
		if e.Data == nil || seen[e.EventID] {
			continue
		}
		seen[e.EventID] = true
		if n := len(out); n > 0 && recordLess(e, out[n-1]) {
			sorted = false
		}
		out = append(out, e)
	}
	if !sorted {
		slices.SortStableFunc(out, recordCmp)
	}
	return out
}

// recordCmp orders records by t, then seq, then event_id. seq rises with
// every record the agent writes (ledger-v1 4.1), so events of the same
// millisecond keep the order the agent wrote them in.
func recordCmp(a, b event.Event) int {
	if c := cmp.Compare(a.T, b.T); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Seq, b.Seq); c != 0 {
		return c
	}
	return strings.Compare(a.EventID, b.EventID)
}

func recordLess(a, b event.Event) bool { return recordCmp(a, b) < 0 }

// floorSlot returns the start of the slot holding t.
func floorSlot(t int64) int64 {
	q := t / event.SlotMS
	if t%event.SlotMS < 0 {
		q--
	}
	return q * event.SlotMS
}

// never is the decision time of a record that stays pending until more
// records arrive, such as a turn that hasn't stopped.
const never = math.MaxInt64

// split divides d into what became final in [from, to) and what is still
// pending at or after to. What is before from was final already.
func (d Derived) split(from, to int64) (final, pending Derived) {
	final.Slots, pending.Slots = splitBy(d.Slots, from, to, func(s Slot) int64 { return s.Start })
	final.Turns, pending.Turns = splitBy(d.Turns, from, to, Turn.decided)
	final.Anchors, pending.Anchors = splitBy(d.Anchors, from, to, func(a Anchor) int64 { return a.decided })
	final.Returns, pending.Returns = splitBy(d.Returns, from, to, func(r Return) int64 { return r.RunStart })
	final.Tracks, pending.Tracks = splitBy(d.Tracks, from, to, func(c TrackCall) int64 { return c.T })
	final.Relays, pending.Relays = splitBy(d.Relays, from, to, func(r RelayRecord) int64 { return r.decided })
	final.Flags, pending.Flags = splitBy(d.Flags, from, to, func(f Flag) int64 { return f.T })
	final.Fills, pending.Fills = splitBy(d.Fills, from, to, func(f Fill) int64 { return f.decided })
	final.Credits, pending.Credits = splitBy(d.Credits, from, to, func(c Credit) int64 { return c.Slot })
	return final, pending
}

func splitBy[T any](xs []T, from, to int64, at func(T) int64) (final, pending []T) {
	for _, x := range xs {
		switch t := at(x); {
		case t < from:
		case t < to:
			final = append(final, x)
		default:
			pending = append(pending, x)
		}
	}
	return final, pending
}

// Append adds o's records to d, keeping each list in order. It is how a
// caller that steps the scorer often rebuilds the whole picture: the
// Finals of every Step, then the last Pending.
func (d *Derived) Append(o Derived) {
	d.Slots = append(d.Slots, o.Slots...)
	d.Turns = append(d.Turns, o.Turns...)
	d.Anchors = append(d.Anchors, o.Anchors...)
	d.Returns = append(d.Returns, o.Returns...)
	d.Tracks = append(d.Tracks, o.Tracks...)
	d.Relays = append(d.Relays, o.Relays...)
	d.Flags = append(d.Flags, o.Flags...)
	d.Fills = append(d.Fills, o.Fills...)
	d.Credits = append(d.Credits, o.Credits...)
	slices.SortStableFunc(d.Slots, func(a, b Slot) int { return cmp.Compare(a.Start, b.Start) })
	slices.SortStableFunc(d.Turns, turnCmp)
	slices.SortStableFunc(d.Anchors, anchorCmp)
	slices.SortStableFunc(d.Returns, func(a, b Return) int { return cmp.Compare(a.RunStart, b.RunStart) })
	slices.SortStableFunc(d.Tracks, trackCmp)
	slices.SortStableFunc(d.Relays, relayCmp)
	slices.SortStableFunc(d.Flags, flagCmp)
	slices.SortStableFunc(d.Fills, fillCmp)
	slices.SortStableFunc(d.Credits, func(a, b Credit) int { return cmp.Compare(a.Slot, b.Slot) })
}
