// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// testSlot is a slot boundary: 2026-11-19T00:41:45Z.
const testSlot = 1795046505000

func testParams() string { return "sha256:" + strings.Repeat("9c", 32) }

func validSlot() Slot {
	return Slot{Start: testSlot, InputAny: true, InputKeys: ptr(true), Present: PresentYes,
		FocusClass: AppTerminal, FocusProject: "p_9f2c"}
}

func validTurn() Turn {
	return Turn{SessionKey: "s_4be1f0", TurnKey: "t_01", TSubmit: testT, TStop: testT + 60000, Origin: OriginHumanTyped,
		PromptClass: ClassSubstantive, Attempt: 1, WordsOut: 160, DiffLines: 12, Unread: true}
}

func validAnchor() Anchor {
	return Anchor{AnchorID: testEventID, Kind: AnchorSubstantivePrompt, T: testT, ProjectKey: "p_9f2c",
		LeadS: 150, TrailS: 10, DiscountedBy: ReasonReroll}
}

func validCreditSlot() CreditSlot {
	return CreditSlot{Slot: testSlot, ProjectKey: "p_9f2c", CreditedS: 15, Weight: 1, Reason: ReasonPrompt,
		AnchorIDs: []string{testEventID}}
}

// validBucket is the minute in SM 5.1: ten seconds in a chat app, then
// reading, then a prompt.
func validBucket() MinuteBucket {
	return MinuteBucket{
		V:      "1.0.0",
		Params: testParams(),
		Minute: time.Date(2026, 11, 19, 16, 42, 0, 0, time.UTC),
		Device: "d_7f3a",
		Slots: []BucketSlot{
			{Project: "p_9f2c", Credited: 5, Why: ReasonRead, Kind: ActivityReadingOutput, Ev: EvidenceA},
			{Project: "p_9f2c", Credited: 15, Why: ReasonRead, Kind: ActivityReadingOutput, Ev: EvidenceA},
			{Project: "p_9f2c", Credited: 15, Why: ReasonPrompt, Kind: ActivityPrompting, Ev: EvidenceA},
			{Project: "p_9f2c", Credited: 15, Why: ReasonPrompt, Kind: ActivityPrompting, Ev: EvidenceA},
		},
		CreditedS:     50,
		Uncredited:    map[Reason]float64{ReasonOtherFocus: 10},
		AgentRunningS: 6,
		Anchors:       []string{testEventID, testEventID2},
		State:         StateFinal,
		Witness:       Witnessed,
	}
}

func validRollup() Rollup {
	return Rollup{
		V:                  "1.0.0",
		Params:             testParams(),
		Level:              LevelWorkSession,
		ProjectKey:         "p_9f2c",
		Start:              time.Date(2026, 11, 19, 16, 0, 0, 0, time.UTC),
		End:                time.Date(2026, 11, 19, 17, 0, 0, 0, time.UTC),
		CreditedS:          120,
		CreditedByReason:   map[Reason]float64{ReasonPrompt: 80, ReasonRead: 30, ReasonRead + ReturnedSuffix: 10},
		CreditedByKind:     map[Activity]float64{ActivityPrompting: 80, ActivityReadingOutput: 40},
		Uncredited:         map[Reason]float64{ReasonAgentOnly: 300, ReasonAbsent: 60},
		CreditedByEvidence: map[Evidence]float64{EvidenceA: 100, EvidenceB: 20},
		AgentRunningS:      400,
		AnchorCounts:       map[AnchorKind]int{AnchorSubstantivePrompt: 3, AnchorPermissionAnswer: 2},
	}
}

func validDayRollup() Rollup {
	r := validRollup()
	r.Level, r.ProjectKey = LevelDay, ""
	r.CreditedByProject = map[string]float64{"p_9f2c": 100, "p_77aa": 20}
	return r
}

// sm54Totals is the submission SM 5.4 describes: 5.9 h credited, 9.1 h
// Hackatime would log, 7.4 h of it AI coding, 4.6 h of that credited (62%),
// 97% of credited time witnessed.
func sm54Totals() SubmissionTotals {
	const h = 3600
	return SubmissionTotals{
		LoggedS: 9.1 * h, CreditedS: 5.9 * h, WitnessedS: 0.97 * 5.9 * h, BackfilledS: 0.03 * 5.9 * h,
		AgentOnlyS: 2 * h, ExcludedLoopsS: 0.3 * h, IdleS: h,
		AILoggedS: 7.4 * h, CodingLoggedS: 1.7 * h, HandbookS: 1.7*h + 7.4*h/3,
		AICreditedS: 4.6 * h, HandCreditedS: 1.3 * h, AIRatio: ptr(4.6 / 7.4),
	}
}

func validSubmission() Rollup {
	tot := sm54Totals()
	r := validRollup()
	r.Level = LevelSubmission
	r.CreditedS = tot.CreditedS
	r.CreditedByReason = map[Reason]float64{ReasonPrompt: 9000, ReasonSupervise: 4000, ReasonRead: 3560, ReasonEdit: 3000, ReasonTest: 1680}
	r.CreditedByKind = map[Activity]float64{ActivityPrompting: 9000, ActivitySupervising: 4000, ActivityReadingOutput: 3560,
		ActivityHandEdit: 3000, ActivityTesting: 1680}
	r.CreditedByEvidence = map[Evidence]float64{EvidenceA: 20000, EvidenceB: 1240}
	r.Totals = &tot
	return r
}

type derivedCase[T any] struct {
	name   string
	mutate func(*T) // nil: the valid record as is
	want   string   // a substring of the error, or "" for a valid record
}

func runCases[T interface{ Validate() error }](t *testing.T, valid func() T, cases []derivedCase[T]) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := valid()
			if tc.mutate != nil {
				tc.mutate(&v)
			}
			err := v.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case tc.want != "" && err == nil:
				t.Errorf("Validate() = nil, want an error containing %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("Validate() = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestSlotValidate(t *testing.T) {
	runCases(t, validSlot, []derivedCase[Slot]{
		{"valid", nil, ""},
		{"no keyboard signal", func(s *Slot) { s.InputKeys = nil }, ""},
		{"locked and absent", func(s *Slot) { s.Locked, s.InputAny, s.InputKeys, s.Present = true, false, ptr(false), PresentNo }, ""},
		{"start on a slot boundary", func(s *Slot) { s.Start += 1000 }, "start: must fall on"},
		{"start in milliseconds", func(s *Slot) { s.Start /= 1000 }, "start: must be Unix milliseconds"},
		{"a key press is input", func(s *Slot) { s.InputAny = false }, "input_keys:"},
		{"present needs input", func(s *Slot) { s.InputAny, s.InputKeys = false, ptr(false) }, `present: is "yes" but the slot had no input`},
		{"sleep means absent", func(s *Slot) { s.Asleep = true }, `present: must be "no"`},
		{"unknown presence", func(s *Slot) { s.Present = "maybe" }, "present:"},
		{"focus class is required", func(s *Slot) { s.FocusClass = "" }, "focus_class:"},
		{"focus project isn't a path", func(s *Slot) { s.FocusProject = "/home/teen/game" }, "focus_project:"},
	})
}

func TestTurnValidate(t *testing.T) {
	runCases(t, validTurn, []derivedCase[Turn]{
		{"valid", nil, ""},
		{"still running", func(tu *Turn) { tu.TStop = 0 }, ""},
		{"automated", func(tu *Turn) { tu.Origin, tu.PromptClass, tu.Attempt = OriginScheduled, "", 0 }, ""},
		{"turns belong to a session", func(tu *Turn) { tu.SessionKey = NoSession }, "session_key:"},
		{"stop isn't before submit", func(tu *Turn) { tu.TStop = tu.TSubmit - 1 }, "t_stop:"},
		{"human turns have a class", func(tu *Turn) { tu.PromptClass = "" }, "prompt_class:"},
		{"human attempts count from 1", func(tu *Turn) { tu.Attempt = 0 }, "attempt: must be at least 1"},
		{"automated turns have no class", func(tu *Turn) { tu.Origin, tu.Attempt = OriginSelfContinue, 0 }, "prompt_class: only human turns"},
		{"automated turns have no attempt", func(tu *Turn) { tu.Origin, tu.PromptClass = OriginHeadless, "" }, "attempt: must be 0"},
		{"unknown origin", func(tu *Turn) { tu.Origin = "alien" }, "origin:"},
		{"word counts aren't negative", func(tu *Turn) { tu.WordsOut = -1 }, "words_out:"},
	})
}

func TestAnchorValidate(t *testing.T) {
	runCases(t, validAnchor, []derivedCase[Anchor]{
		{"valid", nil, ""},
		{"not discounted", func(a *Anchor) { a.DiscountedBy = "" }, ""},
		{"the id is an event id", func(a *Anchor) { a.AnchorID = "a_1" }, "anchor_id:"},
		{"unknown kind", func(a *Anchor) { a.Kind = "vibes" }, "kind:"},
		{"anchors belong to a project", func(a *Anchor) { a.ProjectKey = "" }, "project_key:"},
		{"a discount is an uncredited reason", func(a *Anchor) { a.DiscountedBy = ReasonPrompt }, "discounted_by:"},
		{"lead isn't negative", func(a *Anchor) { a.LeadS = -1 }, "lead_s:"},
	})
}

func TestCreditSlotValidate(t *testing.T) {
	runCases(t, validCreditSlot, []derivedCase[CreditSlot]{
		{"valid", nil, ""},
		{"half weight", func(s *CreditSlot) { s.CreditedS, s.Weight, s.Reason = 7.5, 0.5, ReasonRead }, ""},
		{"returned slot", func(s *CreditSlot) { s.Reason = ReasonRead + ReturnedSuffix }, ""},
		{"uncredited", func(s *CreditSlot) {
			s.CreditedS, s.Weight, s.Reason, s.ProjectKey, s.AnchorIDs = 0, 0, ReasonAbsent, "", nil
		}, ""},
		{"at most 15 seconds", func(s *CreditSlot) { s.CreditedS = 16 }, "credited_s: must be 0 to 15"},
		{"steps of half a second", func(s *CreditSlot) { s.CreditedS = 7.25 }, "credited_s: must be 0 to 15"},
		{"weight is 0, 0.5 or 1", func(s *CreditSlot) { s.Weight = 0.75 }, "weight:"},
		{"half weight credits at most 7.5", func(s *CreditSlot) { s.Weight = 0.5 }, "more than 15 seconds at weight 0.5"},
		{"credit needs a credit reason", func(s *CreditSlot) { s.Reason = ReasonOtherFocus }, "reason: credited seconds need a credit reason"},
		{"a credit reason needs credit", func(s *CreditSlot) { s.CreditedS = 0 }, "reason: is the credit reason"},
		{"unknown reason", func(s *CreditSlot) { s.Reason = "Z" }, `reason: "Z" is not a reason code`},
		{"credit belongs to a project", func(s *CreditSlot) { s.ProjectKey = "" }, "project_key: missing"},
		{"credit traces to an anchor", func(s *CreditSlot) { s.AnchorIDs = nil }, "anchor_ids: missing"},
		{"anchor ids are event ids", func(s *CreditSlot) { s.AnchorIDs = []string{"e_01JDQ4"} }, "anchor_ids[0]:"},
	})
}

func TestMinuteBucketValidate(t *testing.T) {
	runCases(t, validBucket, []derivedCase[MinuteBucket]{
		{"valid", nil, ""},
		{"nothing credited, agent running", func(m *MinuteBucket) {
			m.Slots = []BucketSlot{{}, {}, {}, {}}
			m.CreditedS, m.Anchors, m.Uncredited = 0, nil, map[Reason]float64{ReasonAgentOnly: 60}
		}, ""},
		{"four slots", func(m *MinuteBucket) { m.Slots = m.Slots[:3] }, "slots: must have 4 entries"},
		{"credited_s is the slots' sum", func(m *MinuteBucket) { m.CreditedS = 55 }, "slots[].cr: sums to 50"},
		{"kind follows the reason", func(m *MinuteBucket) { m.Slots[2].Kind = ActivityReadingOutput }, `slots[2].kind: must be "prompting"`},
		{"credit has a credit reason", func(m *MinuteBucket) { m.Slots[0].Why = ReasonOtherFocus }, "slots[0].why:"},
		{"no credit, no reason", func(m *MinuteBucket) {
			m.Slots[0] = BucketSlot{Why: ReasonRead}
			m.CreditedS = 45
		}, "slots[0].why: a slot with no credit"},
		{"evidence level", func(m *MinuteBucket) { m.Slots[1].Ev = "D" }, "slots[1].ev:"},
		{"credited slots have a project", func(m *MinuteBucket) { m.Slots[3].Project = "" }, "slots[3].p:"},
		{"uncredited reasons are X codes", func(m *MinuteBucket) { m.Uncredited = map[Reason]float64{ReasonRead: 10} }, `uncredited: has an invalid key "READ"`},
		{"a minute has 60 seconds", func(m *MinuteBucket) { m.Uncredited[ReasonOtherFocus] = 20 }, "add up to 70, more than a minute"},
		{"minute on the minute", func(m *MinuteBucket) { m.Minute = m.Minute.Add(30 * time.Second) }, "minute: must be the start of a UTC minute"},
		{"minute in UTC", func(m *MinuteBucket) { m.Minute = m.Minute.In(time.FixedZone("CET", 3600)) }, "minute: must be the start of a UTC minute"},
		{"params is a full hash", func(m *MinuteBucket) { m.Params = "sha256:9c1e04" }, "params:"},
		{"scoring version", func(m *MinuteBucket) { m.V = "1.0" }, "v:"},
		{"credit traces to anchors", func(m *MinuteBucket) { m.Anchors = nil }, "anchors: missing"},
		{"agent time fits in the minute", func(m *MinuteBucket) { m.AgentRunningS = 61 }, "agent_running_s:"},
		{"unknown state", func(m *MinuteBucket) { m.State = "done" }, "state:"},
		{"unknown witness", func(m *MinuteBucket) { m.Witness = "seen" }, "witness:"},
	})
}

// TestMinuteBucketSpecExample decodes the JSON example in SM 5.1, with its
// elided hash and event ids filled in, rejecting unknown fields. It fails
// if a field the spec names has no Go field.
func TestMinuteBucketSpecExample(t *testing.T) {
	example := strings.NewReplacer("PARAMS", testParams(), "ANCHOR1", testEventID, "ANCHOR2", testEventID2).Replace(`{
	  "v": "1.0.0",
	  "params": "PARAMS",
	  "minute": "2026-11-19T16:42:00Z",
	  "device": "d_7f3a",
	  "slots": [
	    {"p": "p_9f2c", "cr": 5,  "why": "READ", "kind": "reading_output", "ev": "A"},
	    {"p": "p_9f2c", "cr": 15, "why": "READ", "kind": "reading_output", "ev": "A"},
	    {"p": "p_9f2c", "cr": 15, "why": "P",    "kind": "prompting",      "ev": "A"},
	    {"p": "p_9f2c", "cr": 15, "why": "P",    "kind": "prompting",      "ev": "A"}
	  ],
	  "credited_s": 50,
	  "uncredited": {"X.other_focus": 10},
	  "agent_running_s": 6,
	  "anchors": ["ANCHOR1", "ANCHOR2"],
	  "state": "final",
	  "witness": "witnessed"
	}`)
	dec := json.NewDecoder(strings.NewReader(example))
	dec.DisallowUnknownFields()
	var got MinuteBucket
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decoding the SM 5.1 example: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("the SM 5.1 example doesn't validate: %v", err)
	}
	want := validBucket()
	if !got.Minute.Equal(want.Minute) {
		t.Errorf("minute = %v, want %v", got.Minute, want.Minute)
	}
	got.Minute = want.Minute
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded %+v\nwant    %+v", got, want)
	}
}

func TestRollupValidate(t *testing.T) {
	runCases(t, validRollup, []derivedCase[Rollup]{
		{"valid work session", nil, ""},
		{"valid day", func(r *Rollup) { *r = validDayRollup() }, ""},
		{"valid submission", func(r *Rollup) { *r = validSubmission() }, ""},
		{"unknown level", func(r *Rollup) { r.Level = "week" }, "level:"},
		{"a day spans projects", func(r *Rollup) { *r = validDayRollup(); r.ProjectKey = "p_9f2c" }, "project_key: a day rollup spans projects"},
		{"other levels have a project", func(r *Rollup) { r.ProjectKey = "" }, "project_key:"},
		{"end isn't before start", func(r *Rollup) { r.End = r.Start.Add(-time.Hour) }, "end: is before start"},
		{"by reason sums to credited", func(r *Rollup) { r.CreditedByReason[ReasonPrompt] = 10 }, "credited_by_reason: sums to 50"},
		{"by reason uses credit reasons", func(r *Rollup) { r.CreditedByReason = map[Reason]float64{ReasonAbsent: 120} }, "credited_by_reason: has an invalid key"},
		{"by kind sums to credited", func(r *Rollup) { r.CreditedByKind[ActivityPrompting] = 1 }, "credited_by_kind: sums to 41"},
		{"by kind uses activities", func(r *Rollup) { r.CreditedByKind["napping"] = 0 }, "credited_by_kind: has an invalid key"},
		{"uncredited uses X codes", func(r *Rollup) { r.Uncredited[ReasonPrompt] = 1 }, "uncredited: has an invalid key"},
		{"by evidence sums to credited", func(r *Rollup) { r.CreditedByEvidence[EvidenceC] = 5 }, "credited_by_evidence: sums to 125"},
		{"by project is for days", func(r *Rollup) { r.CreditedByProject = map[string]float64{"p_9f2c": 120} }, "credited_by_project: is for day rollups only"},
		{"by project sums to credited", func(r *Rollup) { *r = validDayRollup(); r.CreditedByProject["p_77aa"] = 0 }, "credited_by_project: sums to 100"},
		{"anchor counts aren't negative", func(r *Rollup) { r.AnchorCounts[AnchorInterrupt] = -1 }, "anchor_counts: has a negative count"},
		{"submissions carry totals", func(r *Rollup) { *r = validSubmission(); r.Totals = nil }, "totals: missing"},
		{"totals are for submissions", func(r *Rollup) { tot := sm54Totals(); r.Totals = &tot }, "totals: is for submission rollups only"},
		{"totals are checked", func(r *Rollup) { *r = validSubmission(); r.Totals.HandbookS = 18000 }, "totals.handbook_s: is 18000"},
	})
}

func TestSubmissionTotalsValidate(t *testing.T) {
	runCases(t, sm54Totals, []derivedCase[SubmissionTotals]{
		{"the SM 5.4 example", nil, ""},
		{"values rounded for display", func(s *SubmissionTotals) { s.HandbookS = math.Round(s.HandbookS*10) / 10; s.AIRatio = ptr(0.62) }, ""},
		{"no AI time, no ratio", func(s *SubmissionTotals) {
			s.AILoggedS, s.CodingLoggedS, s.HandbookS, s.AIRatio = 0, s.LoggedS, s.LoggedS, nil
			s.AICreditedS, s.HandCreditedS = 0, s.CreditedS
		}, ""},
		{"witnessed + backfilled = credited", func(s *SubmissionTotals) { s.WitnessedS = 20000 }, "witnessed_s: is 20000"},
		{"coding = logged - AI", func(s *SubmissionTotals) { s.CodingLoggedS = 7000 }, "coding_logged_s: is 7000"},
		{"handbook = coding + AI/3", func(s *SubmissionTotals) { s.HandbookS = 4.2 * 3600 }, "handbook_s: is 15120"},
		{"hand = credited - AI", func(s *SubmissionTotals) { s.HandCreditedS = 5000 }, "hand_credited_s: is 5000"},
		{"ratio is AI credited over AI logged", func(s *SubmissionTotals) { s.AIRatio = ptr(0.9) }, "ai_ratio: is 0.9"},
		{"ratio is required with AI time", func(s *SubmissionTotals) { s.AIRatio = nil }, "ai_ratio: missing"},
		{"ratio is null without AI time", func(s *SubmissionTotals) {
			s.AILoggedS, s.CodingLoggedS, s.HandbookS = 0, s.LoggedS, s.LoggedS
			s.AICreditedS, s.HandCreditedS = 0, s.CreditedS
		}, "ai_ratio: must be null"},
		{"no negative totals", func(s *SubmissionTotals) { s.IdleS = -1 }, "idle_s:"},
	})
	// SM 5.4 rounds these for its handbook text: "allows at most 4.2 h" and
	// "credited 4.6 h (62%)".
	tot := sm54Totals()
	if got := math.Round(tot.HandbookS/360) / 10; got != 4.2 {
		t.Errorf("handbook hours = %v, SM 5.4 says 4.2", got)
	}
	if got := math.Round(*tot.AIRatio * 100); got != 62 {
		t.Errorf("ai_ratio = %v%%, SM 5.4 says 62%%", got)
	}
}

func TestDerivedJSONRoundTrip(t *testing.T) {
	records := []interface{ Validate() error }{
		validSlot(), validTurn(), validAnchor(), validCreditSlot(), validBucket(),
		validRollup(), validDayRollup(), validSubmission(),
	}
	for _, rec := range records {
		t.Run(reflect.TypeOf(rec).Name(), func(t *testing.T) {
			b, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			p := reflect.New(reflect.TypeOf(rec))
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.DisallowUnknownFields()
			if err := dec.Decode(p.Interface()); err != nil {
				t.Fatalf("decoding %s: %v", b, err)
			}
			got := p.Elem().Interface().(interface{ Validate() error })
			if err := got.Validate(); err != nil {
				t.Errorf("the decoded record doesn't validate: %v", err)
			}
			again, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(again, b) {
				t.Errorf("the JSON changed on a round trip\n first: %s\nsecond: %s", b, again)
			}
		})
	}
}
