// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Submission says what a submission covers (SM 5.3, DESIGN 7.5): a window
// and a set of projects.
type Submission struct {
	// From and To bound the window [From, To), in Unix milliseconds. A slot
	// counts when its start is in it.
	From, To int64
	// Projects are the submitted projects' keys.
	Projects []string
	// Witness gives each minute's witness label, as for Revisions. Nil
	// labels every minute witnessed.
	Witness func(minute int64) event.Witness
	// HeldS is the seconds the server holds because its re-score doesn't
	// reproduce them or their chain doesn't verify (DESIGN 6.2 D7). The
	// device can't know them, so its own totals leave this 0.
	HeldS float64
}

// hackatimeGapMS is Hackatime's cap on the gap between two heartbeats, the
// min(gap, 120 s) of SM 5.3's logged_s (HC 1.4). It is Hackatime's rule,
// not a pairtime parameter.
const hackatimeGapMS = 120_000

// hackatimeDecided picks the logged_s and ai_logged_s rule. While it is
// false, Totals follows main's SM: logged_s is min(gap, 120 s) over one
// time-ordered series of the window's counted records (SM 5.3), and
// ai_logged_s the same rule over the agent events alone (SM 5.4), which
// gives SM 6.1's window logged_s 7,712, ai_logged_s 3,930 and handbook_s
// 5,092, as 6.1's expected.json notes. Set, it follows E-6.1-hackatime
// (docs/errata/2026-11-19-sm-6.1-hackatime-replay.md, decided 2026-11-20):
// each gap goes to the record that ends it, an editor heartbeat within
// echoMS of an agent edit of its file counts as the agent's, and the
// window's first record counts its gap from the last counted record before
// the window, which gives 6.1 7,832, 4,050 and 5,132. That decision puts its
// oracle PR first, so the rule waits for SM's text (D-sc4-sm-first, #132).
//
// TODO(P08): the decisions PR that lands E-6.1-hackatime's SM 5.3, 5.4 and
// 6.1 text sets this true, which runs the test cases that pin the decided
// rule and skips the ones that pin main's, and then drops the switch.
const hackatimeDecided = false

// echoMS is how close an editor heartbeat must be to an agent edit of the
// same file to count as part of it under E-6.1-hackatime, as wakatime-cli
// treats such a heartbeat as a duplicate of the agent's (HC 2.2).
const echoMS = 5_000

// Totals returns the submission's totals, SM 5.3's and SM 5.4's, from what
// the scorer derived over the window, d, and the records it derived them
// from, which give logged_s. Records before the window count toward no
// total under main's SM 5.3 (E-6.1-hackatime would let the last one open
// logged_s's series, see hackatimeDecided), but they open the harness
// sessions whose spans idle_s reads.
//
//   - credited_s is the submitted projects' credit in the window. The
//     uncommitted cap is WP-SC6's, so it isn't applied yet. It splits by
//     witness label, by evidence level (C for every second of a backfilled
//     minute, DESIGN 6.4) and by band. The bands are the device's view: the
//     corroborated band holds witnessed level-A seconds whose every anchor
//     is corroborated (DESIGN 6.4), the witnessed band the other witnessed
//     seconds at A or B, the offline band offline seconds at A or B, and
//     the backfilled band every level-C second. The server decides the
//     bands again at submission, with the checks only it runs (CC6, CC7,
//     CC9, CC10).
//   - logged_s is Hackatime's min(gap, 120 s) between each two consecutive
//     records of one series: the submitted projects' editor heartbeats and
//     agent events (prompt submits, tool calls and stops) in the window, in
//     t order (SM 5.3), ties by seq, then event_id. Only records via
//     hook, log, otel, editor or bridge count; track, plugin,
//     editor_unverified and bridge_unverified records never do (DESIGN 6.2
//     D19). ai_logged_s is the same rule over the agent events alone (SM
//     5.4). hackatimeDecided switches both to E-6.1-hackatime's rule once
//     SM states it.
//   - ai_credited_s is credit under P, ANS, GO, Q, PA, INT, SUP and READ,
//     and any credited second inside a turn of its project (SM 5.4).
//   - agent_only_s, excluded_loops_s and idle_s count the submitted
//     projects' agent-only time, the dark-flow discounts of slots whose
//     seconds go to them, and the X.absent seconds of slots that start
//     while one of their harness sessions is open, from its first record
//     to its session.end or, without one, its last record.
func (s Submission) Totals(d Derived, records []event.Event) event.SubmissionTotals {
	in := func(p string) bool { return slices.Contains(s.Projects, p) }
	witness := s.Witness
	if witness == nil {
		witness = func(int64) event.Witness { return event.Witnessed }
	}
	corroborated := make(map[string]bool, len(d.Anchors))
	for _, a := range d.Anchors {
		corroborated[a.ID] = a.Corroborated
	}
	open := sessionSpans(records, in)
	var (
		credited, witnessed, offline, backfilled int64
		levelA, levelB, levelC                   int64
		bandCorroborated, bandWitnessed, bandOff int64
		viaTrack, ai                             int64
		agentOnly, loops, idle                   int64
	)
	for _, m := range Minutes(d) {
		w := witness(m.Start)
		for _, sl := range m.Slots {
			if sl.Slot < s.From || sl.Slot >= s.To {
				continue
			}
			for p, n := range sl.AgentOnly {
				if in(p) {
					agentOnly += n
				}
			}
			if in(sl.ProjectKey) {
				for _, r := range []event.Reason{event.ReasonReroll, event.ReasonRewriteLoop, event.ReasonSameError,
					event.ReasonAutopilot} {
					loops += sl.Uncredited[r]
				}
			}
			if n := sl.Uncredited[event.ReasonAbsent]; n > 0 && covered(open, sl.Slot) {
				idle += n
			}
			for _, e := range sl.Credited {
				if !in(e.ProjectKey) || e.Halves == 0 {
					continue
				}
				h := e.Halves
				credited += h
				switch w {
				case event.Offline:
					offline += h
				case event.Backfilled:
					backfilled += h
				default:
					witnessed += h
				}
				level := e.Level
				if w == event.Backfilled {
					level = event.EvidenceC
				}
				switch level {
				case event.EvidenceA:
					levelA += h
				case event.EvidenceB:
					levelB += h
				default:
					levelC += h
				}
				switch {
				case level == event.EvidenceC:
					// The backfilled band is every level-C second.
				case w == event.Offline:
					bandOff += h
				case level == event.EvidenceA && allCorroborated(e.AnchorIDs, corroborated):
					bandCorroborated += h
				default:
					bandWitnessed += h
				}
				if slices.Contains(e.Labels, event.LabelViaTrack) {
					viaTrack += h
				}
				if e.InTurn || slices.Contains(aiCreditReasons, e.Reason.Base()) {
					ai += h
				}
			}
		}
	}
	logged, aiLogged := s.logged(records, in)
	out := event.SubmissionTotals{
		LoggedS:           msSeconds(logged),
		CreditedS:         float64(credited) / 2,
		WitnessedS:        float64(witnessed) / 2,
		OfflineS:          float64(offline) / 2,
		BackfilledS:       float64(backfilled) / 2,
		HeldS:             s.HeldS,
		LevelAS:           float64(levelA) / 2,
		LevelBS:           float64(levelB) / 2,
		LevelCS:           float64(levelC) / 2,
		BandCorroboratedS: float64(bandCorroborated) / 2,
		BandWitnessedS:    float64(bandWitnessed) / 2,
		BandOfflineS:      float64(bandOff) / 2,
		BandBackfilledS:   float64(levelC) / 2,
		ViaTrackS:         float64(viaTrack) / 2,
		AgentOnlyS:        float64(agentOnly),
		ExcludedLoopsS:    float64(loops),
		IdleS:             float64(idle),
		AILoggedS:         msSeconds(aiLogged),
		CodingLoggedS:     msSeconds(logged - aiLogged),
		AICreditedS:       float64(ai) / 2,
		HandCreditedS:     float64(credited-ai) / 2,
	}
	out.HandbookS = out.CodingLoggedS + out.AILoggedS/3
	if aiLogged > 0 {
		r := out.AICreditedS / out.AILoggedS
		out.AIRatio = &r
	}
	return out
}

// LevelCCap is SM 5.4's ceiling for a submission's level-C seconds:
// min(level_c_s, ai_logged_s / 3), so lookback and side-door credit never
// beat the handbook's third. DESIGN 6.2 D5 lets a program take them only if
// it opts in to level C, and only inside commit intervals whose closing
// commit is forge-verified, so the cap bounds what any program could
// approve.
func LevelCCap(t event.SubmissionTotals) float64 { return min(t.LevelCS, t.AILoggedS/3) }

// aiCreditReasons are SM 5.4's AI-assisted credit reasons. Any other credited
// second counts as AI-assisted too when a turn of its project was running.
var aiCreditReasons = []event.Reason{event.ReasonPrompt, event.ReasonAnswer, event.ReasonGoAhead, event.ReasonQueued,
	event.ReasonPermission, event.ReasonInterrupt, event.ReasonSupervise, event.ReasonRead}

// msSeconds converts milliseconds to seconds.
func msSeconds(ms int64) float64 { return float64(ms) / 1000 }

// allCorroborated reports whether every anchor named is corroborated, and
// at least one is named.
func allCorroborated(ids []string, corroborated map[string]bool) bool {
	for _, id := range ids {
		if !corroborated[id] {
			return false
		}
	}
	return len(ids) > 0
}

// loggedVia reports whether a record that arrived via v counts toward
// logged_s and ai_logged_s (DESIGN 6.2 D19).
func loggedVia(v event.Via) bool {
	switch v {
	case event.ViaHook, event.ViaLog, event.ViaOTel, event.ViaEditor, event.ViaBridge:
		return true
	}
	return false
}

// agentEvent reports whether e is one of SM 5.4's agent events: a prompt
// submit, a tool call or a stop.
func agentEvent(e event.Event) bool {
	switch e.Data.(type) {
	case *event.PromptSubmit, *event.AgentTool, *event.AgentStop:
		return true
	}
	return false
}

// logged returns logged_s and ai_logged_s in milliseconds (SM 5.3, 5.4).
func (s Submission) logged(records []event.Event, in func(string) bool) (logged, ai int64) {
	series := loggedSeries(records, in)
	if !hackatimeDecided {
		return s.replay(series, nil), s.replay(series, agentEvent)
	}
	return s.loggedDecided(series)
}

// loggedSeries returns the records that count toward logged_s, the
// submitted projects' editor heartbeats and agent events through a logged
// via (D19), in record order, each once.
func loggedSeries(records []event.Event, in func(string) bool) []event.Event {
	var series []event.Event
	for _, e := range records {
		if !loggedVia(e.Via) || !in(e.ProjectKey) {
			continue
		}
		if _, ok := e.Data.(*event.EditorActivity); ok || agentEvent(e) {
			series = append(series, e)
		}
	}
	slices.SortStableFunc(series, recordCmp)
	return slices.CompactFunc(series, func(a, b event.Event) bool { return a.EventID == b.EventID })
}

// replay is SM 5.3's rule over the records of series inside the window that
// keep accepts, or all of them for nil: min(gap, 120 s) between each two
// consecutive ones. The window's first such record adds nothing, since SM
// 5.3 sums the window's records.
func (s Submission) replay(series []event.Event, keep func(event.Event) bool) int64 {
	var total int64
	prev := int64(-1)
	for _, e := range series {
		if e.T >= s.To {
			break
		}
		if e.T < s.From || keep != nil && !keep(e) {
			continue
		}
		if prev >= 0 {
			total += min(e.T-prev, hackatimeGapMS)
		}
		prev = e.T
	}
	return total
}

// loggedDecided is E-6.1-hackatime's rule over series, which waits for its
// SM text (hackatimeDecided): one series whose first record in the window
// counts its gap from the last counted record before the window, each gap
// going to the record that ends it, and to ai_logged_s when that is an agent
// event or an editor heartbeat within echoMS of a counted agent edit of the
// same file.
func (s Submission) loggedDecided(series []event.Event) (logged, ai int64) {
	// The agent edits each heartbeat may echo.
	var edits []event.Event
	for _, e := range series {
		if p, ok := e.Data.(*event.AgentTool); ok && isEdit(p) {
			edits = append(edits, e)
		}
	}
	echo := func(e event.Event) bool {
		hb, ok := e.Data.(*event.EditorActivity)
		if !ok || hb.FileHash == "" {
			return false
		}
		for _, x := range edits {
			if x.T >= e.T-echoMS && x.T <= e.T+echoMS && slices.Contains(x.Data.(*event.AgentTool).Files, hb.FileHash) {
				return true
			}
		}
		return false
	}
	prev := int64(-1)
	for _, e := range series {
		if e.T >= s.To {
			break
		}
		if e.T >= s.From && prev >= 0 {
			gap := min(e.T-prev, hackatimeGapMS)
			logged += gap
			if agentEvent(e) || echo(e) {
				ai += gap
			}
		}
		prev = e.T
	}
	return logged, ai
}

// sessionSpans returns, for each harness session of a submitted project,
// the span it is open: from its first record to its session.end, or
// without one to its last record.
func sessionSpans(records []event.Event, in func(string) bool) []span {
	type open struct{ first, last, end int64 }
	by := map[string]*open{}
	var keys []string
	for _, e := range records {
		key := harnessRecord(e)
		if key == "" || !in(e.ProjectKey) {
			continue
		}
		o := by[key]
		if o == nil {
			o = &open{first: e.T, last: e.T}
			by[key] = o
			keys = append(keys, key)
		}
		o.first, o.last = min(o.first, e.T), max(o.last, e.T)
		if _, ok := e.Data.(*event.SessionEnd); ok {
			o.end = max(o.end, e.T)
		}
	}
	var out []span
	for _, k := range keys {
		o := by[k]
		end := o.last
		if o.end > 0 {
			end = o.end
		}
		out = append(out, span{o.first, end})
	}
	return out
}

// covered reports whether t falls in one of spans, each [from, to].
func covered(spans []span, t int64) bool {
	for _, sp := range spans {
		if t >= sp.from && t <= sp.to {
			return true
		}
	}
	return false
}
