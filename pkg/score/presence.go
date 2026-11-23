// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Slot is one 15-second slot of the timeline (SM 1.3, 2.1, 2.5), with its
// presence. Every slot from the one holding the first record on is
// derived, up to the last one the samples decide. On a device that never
// samples, such as a server with no desktop, whole minutes are derived
// instead, from the one holding the first record, or the earliest lead a
// relay.message can open if that is earlier, but no more than BUCKET_TTL_S
// before the agent's first record, to the one holding now (presence.live).
type Slot struct {
	// Start is the slot's start in Unix milliseconds, on :00, :15, :30 or
	// :45.
	Start int64 `json:"start"`
	// InputAny is set when an idle sample shows input during the slot.
	InputAny bool `json:"input_any"`
	// InputKeys is nil where the OS reports no keyboard-specific idle
	// time, else whether a sample shows a key press during the slot.
	InputKeys *bool `json:"input_keys"`
	// Present holds from Start until PresentUntil, and Then from there to
	// the slot's end. They differ in two kinds of slot. In the slot where a
	// tail ends: SM 2.1's tail runs PRESENCE_TAIL_S from the last input, to
	// the millisecond, so it can end inside a slot, and what follows in that
	// slot is no or returned. And in the slot whose input ends a silence
	// longer than PRESENCE_GAP_S, when a sample taken inside the slot dates
	// that input: the silence's no or returned holds until the second of
	// the input, and yes from there (presence.markStarts).
	Present      event.Presence `json:"present"`
	PresentUntil int64          `json:"present_until"`
	Then         event.Presence `json:"then"`
	// FocusClass, FocusProject and Hosted describe the window that held
	// focus longest in the slot, as its os.focus records give them (SM 2.5):
	// FocusProject is set when the client resolved the window to one
	// project. The project SM 3.2 resolves the slot to, which for a window
	// hosting sessions of several projects depends on their anchors and
	// reading buckets, is the ProjectKey of the slot's Credit.
	FocusClass   event.AppClass `json:"focus_class"`
	FocusProject string         `json:"focus_project,omitempty"`
	Hosted       []string       `json:"hosted_sessions,omitempty"`
	// Asleep is set when the slot overlaps a suspend or a gap between
	// samples longer than SLEEP_GAP_S; Locked when it overlaps a screen
	// lock. Either makes the slot a hard break.
	Asleep bool `json:"asleep"`
	Locked bool `json:"locked"`
}

// End returns the end of the slot.
func (s Slot) End() int64 { return s.Start + event.SlotMS }

// At returns the slot's presence at time t, which must be in the slot.
func (s Slot) At(t int64) event.Presence {
	if t < s.PresentUntil {
		return s.Present
	}
	return s.Then
}

// Event returns the slot as pkg/event's derived record. Its single present
// value is SM 2.1's for the whole slot: yes for a slot with input, even one
// whose first seconds still belong to the silence before it, and otherwise
// the one the slot starts with.
func (s Slot) Event() event.Slot {
	var keys *bool
	if s.InputKeys != nil {
		keys = ptr(*s.InputKeys)
	}
	present := s.Present
	if s.Then == event.PresentYes {
		present = event.PresentYes
	}
	return event.Slot{Start: s.Start, InputAny: s.InputAny, InputKeys: keys, Present: present,
		FocusClass: s.FocusClass, FocusProject: s.FocusProject, Asleep: s.Asleep, Locked: s.Locked}
}

func ptr[T any](v T) *T { return &v }

// present reports whether presence value p lets an anchor's evidence test
// pass: SM 2.1's yes, bridged and tail. A returned slot only qualifies
// credit under the return rule.
func present(p event.Presence) bool {
	return p == event.PresentYes || p == event.PresentBridged || p == event.PresentTail
}

// sample is one os.presence record in milliseconds: when it was taken and
// when the last input and the last key press before it came.
type sample struct {
	t       int64
	lastAny int64
	hasKeys bool
	lastKey int64
	locked  bool
	// atEvent is set for a sample taken at a focus change or a power
	// event: an os.focus or os.power record has the same time.
	atEvent bool
}

// span is the half-open interval [from, to) in Unix milliseconds.
type span struct{ from, to int64 }

// idleMaxS bounds an idle time: about 31,700 years, past any real one.
const idleMaxS = 1e12

// msOf converts seconds, as PR1's float fields hold them, to whole
// milliseconds, rounding to the nearest. It goes through the decimal text,
// which strconv rounds exactly, so the result doesn't depend on float
// arithmetic. event.Validate asks only that an idle time be finite and not
// negative, so one at or past idleMaxS reads as idleMaxS, a long idle, and
// so does any text strconv can't read back: garbage must not pass for fresh
// input.
func msOf(sec float64) int64 {
	if !(sec < idleMaxS) {
		return idleMaxS * 1000
	}
	s := strconv.FormatFloat(sec, 'f', 3, 64)
	neg := strings.HasPrefix(s, "-")
	whole, frac, _ := strings.Cut(strings.TrimPrefix(s, "-"), ".")
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil {
		return idleMaxS * 1000
	}
	if neg {
		return -(w*1000 + f)
	}
	return w*1000 + f
}

// silentRun is a maximal run of slots with no input and no hard break
// (SM 2.1's no-input run).
type silentRun struct {
	i, j int // the run is slots [i, j)
	// left and right are set when the slot just before the run, and the
	// one just after it, have input.
	left, right bool
	// lastInput is when the last input before the run came, if left.
	lastInput int64
	// tailEnd is lastInput + PRESENCE_TAIL_S, if left.
	tailEnd int64
}

// presence is SM 2.1 over the records: the samples, the hard breaks and
// the slots they decide.
type presence struct {
	r       rules
	samples []sample
	// first is the start of slots[0].
	first int64
	slots []Slot
	// lastIn holds, for each slot, the latest input a sample shows in it.
	lastIn []int64
	runs   []silentRun
	// live is set on a device that never samples while its agent runs
	// (liveUnknown), whose every slot is SM 2.1's unknown: whole minutes
	// are derived, and SM 5.2's codes for a live slot whose presence is
	// unknown apply to its seconds.
	live bool
}

// liveUnknown reports whether recs come from a device with no presence
// sampler while its agent runs, such as a server with no desktop or a
// desktop that gives no idle time (SM 2.1): no record of the sampler's,
// os.presence or os.power (samplerRecord), and some record that came by a
// path only a running agent has, any but a log reader's. It returns the t
// of the first such record, where the agent's records start. The os.focus
// that such a device's agent writes each time it starts, saying focus is
// unknown, is no sample, so it doesn't make the device one that samples.
// Every record lookback backfills from transcripts comes by a log reader
// (SM 2.8), and the range of lookback's own slots is WP-SC7's, so such
// records alone aren't one.
func liveUnknown(recs []event.Event) (first int64, live bool) {
	for _, e := range recs {
		if samplerRecord(e) {
			return 0, false
		}
		if e.Via != event.ViaLog && !live {
			first, live = e.T, true
		}
	}
	return first, live
}

// earliestLead returns the earliest time a relayed lead can start for the
// relay.message records in recs, COMPOSE_MAX_S before the earliest stamp
// of those that can open one, and whether there is one. Those are the
// records of declared none written at most RELAY_SEEN_LAG_S after their
// stamps: any other pairs with nothing, or gives its prompt the app's
// declaration, and opens no lead (SM 2.2 rule 9, 2.3).
func earliestLead(r rules, recs []event.Event) (int64, bool) {
	from, ok := int64(math.MaxInt64), false
	for _, e := range recs {
		m, isRelay := e.Data.(*event.RelayMessage)
		if isRelay && m.Declared == event.RelayDeclaredNone && e.T-m.SentAt <= r.relaySeenLag {
			from, ok = min(from, m.SentAt-r.composeMax), true
		}
	}
	return from, ok
}

func buildPresence(r rules, recs []event.Event, now int64, fl *focusLine) *presence {
	p := &presence{r: r}
	if len(recs) == 0 {
		return p
	}
	// The agent samples at once at every hook, focus and power event
	// (DESIGN 3.3). markStarts needs to know which samples came with a
	// focus or power event.
	eventAt := map[int64]bool{}
	for _, e := range recs {
		switch e.Data.(type) {
		case *event.OSFocus, *event.OSPower:
			eventAt[e.T] = true
		}
	}
	var (
		locks, sleeps, gaps []span
		locked, asleep      bool
		lockFrom, sleepFrom int64
		haveSample          bool
		lastSample          int64
		lastIdle            int64 // the idle time the last sample showed
	)
	for _, e := range recs {
		switch d := e.Data.(type) {
		case *event.OSPresence:
			s := sample{t: e.T, lastAny: e.T - msOf(d.IdleAnyS), locked: d.Locked, atEvent: eventAt[e.T]}
			if d.IdleKeysS != nil {
				s.hasKeys, s.lastKey = true, e.T-msOf(*d.IdleKeysS)
			}
			if gap := e.T - lastSample; haveSample && gap > r.sleepGap && !(lastIdle >= r.contextIdle && gap <= r.slowGap) {
				// SM 2.1: gaps come from wall-clock sample times. The open
				// interval between the two samples is the gap. After
				// CONTEXT_IDLE_S without input the agent samples once a
				// minute (DESIGN 6.2 D14), and a gap that slow sampling
				// explains is no sleep.
				gaps = append(gaps, span{lastSample + 1, e.T})
			}
			// A sample after a suspend shows the machine awake again, even
			// if the resume never arrived. The one taken at the suspend
			// itself has the same time and doesn't.
			if asleep && e.T > sleepFrom {
				sleeps, asleep = append(sleeps, span{sleepFrom, e.T}), false
			}
			switch {
			case d.Locked && !locked:
				locked, lockFrom = true, e.T
			case !d.Locked && locked && e.T > lockFrom:
				locks, locked = append(locks, span{lockFrom, e.T}), false
			}
			p.samples = append(p.samples, s)
			haveSample, lastSample, lastIdle = true, e.T, e.T-s.lastAny
		case *event.OSPower:
			switch d.Op {
			case event.PowerSuspend:
				if !asleep {
					asleep, sleepFrom = true, e.T
				}
			case event.PowerResume:
				if asleep {
					sleeps, asleep = append(sleeps, span{sleepFrom, e.T}), false
				} else {
					// A resume with no suspend before it: the machine slept
					// since the last sample, or since before any record.
					from := int64(math.MinInt64)
					if haveSample {
						from = lastSample + 1
					}
					sleeps = append(sleeps, span{from, e.T})
				}
			case event.PowerLock:
				if !locked {
					locked, lockFrom = true, e.T
				}
			case event.PowerUnlock:
				if locked {
					locks, locked = append(locks, span{lockFrom, e.T}), false
				}
			}
		}
	}
	if asleep {
		sleeps = append(sleeps, span{sleepFrom, math.MaxInt64})
	}
	if locked {
		locks = append(locks, span{lockFrom, math.MaxInt64})
	}

	// Slots end with the slot holding now (Step), and the samples may end
	// them earlier. They start with the first record, but no more than
	// BUCKET_TTL_S before the sampler first ran; on a device whose agent
	// never samples, before the agent's first record (liveUnknown), which is
	// where its records start as a sampling device's start with its first
	// sample; and before now when there is neither, as for records a log
	// reader backfilled alone.
	// Every slot before the first sample is unknown and credits nothing (SM
	// 2.1), and what so old a record put in a bucket has expired by the time
	// the sampler sees anything. A record dated years back, which a
	// transcript or the bridge can carry, would otherwise derive a slot for
	// every 15 s since. Lookback (SM 2.8, WP-SC7) will need its own range.
	//
	// A device that never samples counts from its agent's first record, not
	// from now, so a Step after a silence longer than BUCKET_TTL_S still
	// derives the minutes before the silence that were pending, as one Step
	// over every record does, and a fresh State over the agent's window
	// (internal/agent/scorer) derives every minute from the window's start.
	ref := now
	if len(p.samples) > 0 {
		ref = p.samples[0].t
	}
	start, live := liveUnknown(recs)
	if live {
		ref = start
	}
	p.first = max(floorSlot(recs[0].T), floorSlot(ref-r.bucketTTL))
	last := floorSlot(now)
	// A device that never samples has nothing left to decide a slot once
	// its minute holds a record, so every second of each minute that does
	// is derived, the ones before its first record and after its last
	// included: relayed anchors credit them, and SM 5.2's codes for a live
	// slot whose presence is unknown fill the rest
	// (testdata/timelines/relayed). What it derives past now is pending.
	//
	// The app starts the run that takes a message, its session.start
	// included, only after the message's stamp, and the message's relayed
	// lead ends at the stamp, up to COMPOSE_MAX_S after it starts (SM 2.3).
	// So the slots start no later than the minute holding the earliest lead
	// a relay.message in recs can open, still within BUCKET_TTL_S before
	// the agent's first record: a state that holds nothing older, a fresh
	// one or the agent's window after a quiet night, then derives the whole
	// lead, as one holding every record does.
	if p.live = live; p.live {
		if from, ok := earliestLead(r, recs); ok {
			p.first = max(floorSlot(ref-r.bucketTTL), min(p.first, from))
		}
		p.first = floorMinute(p.first)
		last = floorMinute(now) + MinuteMS - event.SlotMS
	}
	n := int((last-p.first)/event.SlotMS) + 1
	asleepAt := p.mark(n, sleeps, gaps)
	lockedAt := p.mark(n, locks)

	// A slot is decided once a sample at or after its end exists, since
	// that sample's idle time covers the whole slot, or when it is a hard
	// break. The first undecided slot ends the list.
	firstSample, lastSampleT := int64(math.MaxInt64), int64(math.MinInt64)
	if len(p.samples) > 0 {
		firstSample, lastSampleT = p.samples[0].t, p.samples[len(p.samples)-1].t
	}
	for k := 0; k < n; k++ {
		a := p.first + int64(k)*event.SlotMS
		brk := asleepAt[k] || lockedAt[k]
		if !brk && lastSampleT < a+event.SlotMS && a+event.SlotMS > firstSample {
			n = k
			break
		}
	}
	p.slots = make([]Slot, n)
	inputAt := make([]bool, n)
	keyAt := make([]bool, n)
	lastIn := make([]int64, n)
	for _, s := range p.samples {
		if k, ok := p.index(s.lastAny); ok {
			inputAt[k] = true
			lastIn[k] = max(lastIn[k], s.lastAny)
		}
		if s.hasKeys {
			if k, ok := p.index(s.lastKey); ok {
				keyAt[k] = true
			}
		}
	}
	si := 0 // the first sample at or after the current slot's end
	for k := range p.slots {
		a := p.first + int64(k)*event.SlotMS
		b := a + event.SlotMS
		for si < len(p.samples) && p.samples[si].t < b {
			si++
		}
		s := &p.slots[k]
		s.Start, s.PresentUntil = a, b
		s.Asleep, s.Locked = asleepAt[k], lockedAt[k]
		hasKeys := false
		switch {
		case si < len(p.samples):
			hasKeys = p.samples[si].hasKeys
		case len(p.samples) > 0:
			hasKeys = p.samples[len(p.samples)-1].hasKeys
		}
		if hasKeys {
			s.InputKeys = ptr(keyAt[k])
		}
		s.InputAny = inputAt[k]
		f := fl.longest(a, b)
		s.FocusClass, s.FocusProject, s.Hosted = f.class, f.project, f.hosted
		switch {
		case s.Asleep || s.Locked:
			s.Present = event.PresentNo
		case b <= firstSample:
			// SM 2.1: the sampler wasn't running yet.
			s.Present = event.PresentUnknown
		case s.InputAny:
			s.Present = event.PresentYes
		default:
			s.Present = "" // a silent slot, decided below
		}
		s.Then = s.Present
	}
	p.lastIn = lastIn
	p.classifyRuns(lastIn)
	p.markStarts()
	return p
}

// markStarts dates the end of every silence longer than PRESENCE_GAP_S,
// where the samples can (SM 2.1, docs/errata/2026-11-19-sm-6.1-presence-edges.md).
// SM 2.1 makes a slot with input present from its start, and the regular
// sample at the slot's end can't say more. But the agent also samples at
// hook, focus and power events (DESIGN 3.3). A sample taken inside the slot
// at a focus change or a power event dates the input it reports, which is
// the act that ended the silence: the silence lasts into the slot until
// that input, to the second. A sample taken at a hook reports the hook's
// own act, such as the Enter that submits a prompt, and the typing before
// it is the return too, so it only bounds the start: the silence lasts
// until the last sample inside the slot that still shows it, or the slot's
// start. SM 6.1's drink needs the first: Rowan clicks at 15:12:40, which a
// focus sample reports, and SM credits from there. SM 6.4's 23:37 row has
// no sample inside the slot, and SM credits it from its start.
// docs/errata/2026-11-19-presence-start-after-silence.md asks Karim to
// confirm the rule.
func (p *presence) markStarts() {
	for _, run := range p.runs {
		if !run.right || (run.left && int64(run.j-run.i)*event.SlotMS <= p.r.presenceGap) {
			continue
		}
		s := &p.slots[run.j]
		from := p.firstInputInside(s.Start, s.End())
		if from > s.Start {
			s.Present, s.PresentUntil, s.Then = event.PresentNo, from, event.PresentYes
		}
	}
}

// firstInputInside returns when the samples taken inside (a, b) let the
// slot [a, b) start to count as present: at the second holding the input
// that the first of them to report one reports, when a focus change or a
// power event took it; else at the last of them that still shows no input
// in the slot; else at a.
func (p *presence) firstInputInside(a, b int64) int64 {
	from := a
	i := sort.Search(len(p.samples), func(i int) bool { return p.samples[i].t > a })
	for ; i < len(p.samples) && p.samples[i].t < b; i++ {
		s := p.samples[i]
		if s.lastAny >= a {
			if s.atEvent {
				return max(a, floorSecond(s.lastAny))
			}
			return from
		}
		from = s.t
	}
	return from
}

// floorSecond returns the start of the second holding t.
func floorSecond(t int64) int64 {
	q := t / 1000
	if t%1000 < 0 {
		q--
	}
	return q * 1000
}

// unsampled reports whether t falls before the presence sampler ran: there
// is no sample, or none until after the slot holding t ends. Such a slot is
// SM 2.1's unknown.
func (p *presence) unsampled(t int64) bool {
	return len(p.samples) == 0 || floorSlot(t)+event.SlotMS <= p.samples[0].t
}

// mark returns, for each of the n slots, whether it overlaps any of the
// spans.
func (p *presence) mark(n int, lists ...[]span) []bool {
	at := make([]bool, n)
	end := p.first + int64(n)*event.SlotMS
	for _, spans := range lists {
		for _, sp := range spans {
			from, to := max(sp.from, p.first), min(sp.to, end)
			if from >= to {
				continue
			}
			for k := (floorSlot(from) - p.first) / event.SlotMS; k < int64(n); k++ {
				if p.first+k*event.SlotMS >= to {
					break
				}
				at[k] = true
			}
		}
	}
	return at
}

// index returns the index of the slot holding t, if it is derived.
func (p *presence) index(t int64) (int, bool) {
	if t < p.first {
		return 0, false
	}
	k := int((t - p.first) / event.SlotMS)
	return k, k < len(p.slots)
}

// slotAt returns the slot holding t, or nil if it isn't derived.
func (p *presence) slotAt(t int64) *Slot {
	if k, ok := p.index(t); ok {
		return &p.slots[k]
	}
	return nil
}

// presentFor reports whether the person was present at t for an act's
// evidence test, such as a permission answer's or an interrupt's (SM 2.3).
// SM 2.1 makes presence a value per slot, so a slot with input passes from
// its start, as Slot.Event has it, even where markStarts dates the end of a
// silence inside it: that dating is for crediting seconds, and the act
// itself is the person's return. Elsewhere it is the presence at t, so a
// tail still ends to the millisecond.
func (p *presence) presentFor(t int64) bool {
	s := p.slotAt(t)
	if s == nil {
		return false
	}
	return s.Then == event.PresentYes || present(s.At(t))
}

// classifyRuns gives every silent slot its presence (SM 2.1): bridged for
// a short run with input on both sides; otherwise tail for the
// PRESENCE_TAIL_S after the last input, and no for the rest. The return
// rule, which needs anchors, later turns some of the no into returned.
//
// A run's length is its slots' length, from the end of the last input slot
// to the start of the next: samples tell when the last input before a
// silence came, but not when the first input after it did.
func (p *presence) classifyRuns(lastIn []int64) {
	isInput := func(k int) bool { return p.slots[k].Present == event.PresentYes }
	for i := 0; i < len(p.slots); {
		if p.slots[i].Present != "" {
			i++
			continue
		}
		j := i
		for j < len(p.slots) && p.slots[j].Present == "" {
			j++
		}
		run := silentRun{i: i, j: j, left: i > 0 && isInput(i-1), right: j < len(p.slots) && isInput(j)}
		if run.left {
			run.lastInput = lastIn[i-1]
			run.tailEnd = run.lastInput + p.r.presenceTail
		}
		length := int64(j-i) * event.SlotMS
		for k := i; k < j; k++ {
			s := &p.slots[k]
			switch {
			case run.left && run.right && length <= p.r.presenceGap:
				s.Present = event.PresentBridged
			case run.left && s.Start < run.tailEnd:
				s.Present = event.PresentTail
				s.PresentUntil = min(s.End(), run.tailEnd)
			default:
				s.Present = event.PresentNo
			}
			s.Then = s.Present
			if s.PresentUntil < s.End() {
				s.Then = event.PresentNo
			}
		}
		p.runs = append(p.runs, run)
		i = j
	}
}

// inputIn reports whether a sample shows input, or a key press if keys is
// set, at a time in [from, to]. Each sample says when the last input before
// it came, so only samples from from on can show one, and the first sample
// at or after to is the last that can.
func (p *presence) inputIn(from, to int64, keys bool) bool {
	i := sort.Search(len(p.samples), func(i int) bool { return p.samples[i].t >= from })
	for ; i < len(p.samples); i++ {
		s := p.samples[i]
		if !keys {
			if s.lastAny >= from && s.lastAny <= to {
				return true
			}
		} else if s.hasKeys && s.lastKey >= from && s.lastKey <= to {
			return true
		}
		if s.t >= to {
			break
		}
	}
	return false
}

// inputWhere is inputIn with a condition on each input's time, such as
// which window had focus then.
func (p *presence) inputWhere(from, to int64, keys bool, ok func(t int64) bool) bool {
	i := sort.Search(len(p.samples), func(i int) bool { return p.samples[i].t >= from })
	for ; i < len(p.samples); i++ {
		s := p.samples[i]
		l, has := s.lastAny, true
		if keys {
			l, has = s.lastKey, s.hasKeys
		}
		if has && l >= from && l <= to && ok(l) {
			return true
		}
		if s.t >= to {
			break
		}
	}
	return false
}

// keysAt reports whether the OS reports keyboard input apart from other
// input around time t: whether the first sample at or after t, or else the
// last one, has idle_keys_s.
func (p *presence) keysAt(t int64) bool {
	if len(p.samples) == 0 {
		return false
	}
	i := sort.Search(len(p.samples), func(i int) bool { return p.samples[i].t >= t })
	if i == len(p.samples) {
		i--
	}
	return p.samples[i].hasKeys
}

// inputBefore reports whether input came in the window before t: key
// presses where the OS reports them, else any input, as a typed command's
// evidence test reads it (SM 2.3).
func (p *presence) inputBefore(t, window int64) bool {
	return p.inputAround(t, window, 0)
}

// inputAround reports whether input came in the before milliseconds up to
// t or the after milliseconds past it: key presses where the OS reports
// them, else any input. SM 2.2 rules 6 and 7 read a prompt's input this
// way, with after INPUT_RELEASE_GRACE_MS, because the key that sends the
// prompt can be released after the hook stamps t. A sample only dates the
// last input before it, so the release, landing between t and the hook's
// sample, hides the typing before it.
func (p *presence) inputAround(t, before, after int64) bool {
	return p.inputIn(t-before, t+after, p.keysAt(t))
}

// inputSlots counts the slots overlapping [from, to) with input: key
// presses where the OS reports them, else any input.
func (p *presence) inputSlots(from, to int64) int64 {
	keys := p.keysAt(to)
	var n int64
	for t := floorSlot(from); t < to; t += event.SlotMS {
		s := p.slotAt(t)
		if s == nil {
			continue
		}
		if keys && s.InputKeys != nil {
			if *s.InputKeys {
				n++
			}
		} else if s.InputAny {
			n++
		}
	}
	return n
}

// slotInput reports whether the slot holding t had input, or a key press
// where the OS reports them and keys is set.
func (p *presence) slotInput(t int64, keys bool) bool {
	s := p.slotAt(t)
	if s == nil {
		return false
	}
	if keys && s.InputKeys != nil {
		return *s.InputKeys
	}
	return s.InputAny
}
