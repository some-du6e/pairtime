// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
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
		LeadS: 150, TrailS: 10, DiscountedBy: ReasonReroll, Via: ViaHook, Corroborated: true,
		Labels: []Label{LabelTypingModule}}
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

// handbookTotals is the submission in DESIGN 9.3's golden handbook text,
// which starts with SM 5.4's numbers: Hackatime logged 9.1 h, 7.4 h of it AI
// coding, so the handbook allows at most 4.2 h; pairtime credited 5.9 h,
// 4.8 h corroborated, 0.8 h witnessed, 0.2 h offline and 0.1 h backfilled;
// it credited 4.6 h (62%) of the AI-assisted time; 71% of credited time
// was level A. The level-C 0.1 h is the backfilled band, and here it is
// all backfilled time. Its Deflation Justification excludes agent-only
// runtime 2.1 h, re-roll loops 0.6 h and idle reading 0.5 h, which are
// the 3.2 h between Hackatime's 9.1 h and pairtime's 5.9 h.
func handbookTotals() SubmissionTotals {
	const h = 3600
	return SubmissionTotals{
		LoggedS: 9.1 * h, CreditedS: 21240,
		WitnessedS: 20160, OfflineS: 720, BackfilledS: 360,
		LevelAS: 15080, LevelBS: 5800, LevelCS: 360,
		BandCorroboratedS: 17280, BandWitnessedS: 2880, BandOfflineS: 720, BandBackfilledS: 360,
		AgentOnlyS: 2.1 * h, ExcludedLoopsS: 0.6 * h, IdleS: 0.5 * h,
		AILoggedS: 7.4 * h, CodingLoggedS: 1.7 * h, HandbookS: 1.7*h + 7.4*h/3,
		AICreditedS: 4.6 * h, HandCreditedS: 1.3 * h, AIRatio: ptr(4.6 / 7.4),
	}
}

func validSubmission() Rollup {
	tot := handbookTotals()
	r := validRollup()
	r.Level = LevelSubmission
	r.End = r.Start.Add(7 * 24 * time.Hour) // a week of work, so 5.9 h fits
	r.CreditedS = tot.CreditedS
	r.CreditedByReason = map[Reason]float64{ReasonPrompt: 9000, ReasonSupervise: 4000, ReasonRead: 3560, ReasonEdit: 3000, ReasonTest: 1680}
	r.CreditedByKind = map[Activity]float64{ActivityPrompting: 9000, ActivitySupervising: 4000, ActivityReadingOutput: 3560,
		ActivityHandEdit: 3000, ActivityTesting: 1680}
	r.CreditedByEvidence = map[Evidence]float64{EvidenceA: tot.LevelAS, EvidenceB: tot.LevelBS, EvidenceC: tot.LevelCS}
	r.Totals = &tot
	return r
}

// tier1Bucket is validBucket with every field set, slot labels and a
// revision included, so TestTier1Shape sees every key a bucket can write.
func tier1Bucket() MinuteBucket {
	m := validBucket()
	m.State, m.Rev = StatePending, 2
	m.Slots[0].Lbl = []Label{LabelViaTrack}
	m.Slots[1].Lbl = []Label{LabelViaPlugin}
	m.Slots[2].Lbl = []Label{LabelViaBridge}
	m.Slots[3].Lbl = []Label{LabelViaTrack, LabelViaBridge}
	return m
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
		{"not corroborated, no labels", func(a *Anchor) { a.Corroborated, a.Labels = false, nil }, ""},
		{"the id is an event id", func(a *Anchor) { a.AnchorID = "a_1" }, "anchor_id:"},
		{"unknown kind", func(a *Anchor) { a.Kind = "vibes" }, "kind:"},
		{"anchors belong to a project", func(a *Anchor) { a.ProjectKey = "" }, "project_key:"},
		{"a discount is a dark-flow code", func(a *Anchor) { a.DiscountedBy = ReasonPrompt }, "discounted_by: must be a dark-flow discount"},
		{"the no-progress rule discounts seconds, not anchors", func(a *Anchor) { a.DiscountedBy = ReasonNoProgress }, "discounted_by: must be a dark-flow discount"},
		{"an autopilot discount", func(a *Anchor) { a.DiscountedBy = ReasonAutopilot }, ""},
		{"lead isn't negative", func(a *Anchor) { a.LeadS = -1 }, "lead_s:"},
		{"an anchor says how its record arrived", func(a *Anchor) { a.Via = "" }, "via:"},
		{"a track anchor carries via_track", func(a *Anchor) { a.Via, a.Labels = ViaTrack, nil }, `labels: missing "via_track"; a track anchor always carries it`},
		{"a keyboard-backed track anchor keeps via_track", func(a *Anchor) {
			a.Via, a.Labels = ViaTrack, []Label{LabelViaTrack, LabelTypingModule}
		}, ""},
		{"a plugin anchor carries via_plugin", func(a *Anchor) { a.Via, a.Labels = ViaPlugin, []Label{LabelVM} }, `labels: missing "via_plugin"`},
		{"an unverified bridge anchor carries via_bridge", func(a *Anchor) { a.Via, a.Labels = ViaBridgeUnverified, []Label{LabelViaBridge} }, ""},
		{"a side-door label needs its side door", func(a *Anchor) { a.Labels = []Label{LabelViaPlugin} }, `labels: has "via_plugin", but the anchor arrived via "hook"`},
		{"an unverified editor anchor has no side-door label", func(a *Anchor) {
			a.Via, a.Labels = ViaEditorUnverified, []Label{LabelViaBridge}
		}, `labels: has "via_bridge", but the anchor arrived via "editor_unverified"`},
		{"labels are listed once, in order", func(a *Anchor) { a.Labels = []Label{LabelVM, LabelTypingModule} }, "labels: must list each label once"},
		{"editor_typing isn't a v1 label", func(a *Anchor) { a.Labels = []Label{"editor_typing"} }, `labels[0]: "editor_typing" is not one of`},
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
		{"credit without anchor ids, as uploaded", func(m *MinuteBucket) { m.Anchors = nil }, ""},
		{"anchor ids are event ids", func(m *MinuteBucket) { m.Anchors = []string{"e_01JDQ4"} }, "anchors[0]:"},
		{"agent time fits in the minute", func(m *MinuteBucket) { m.AgentRunningS = 61 }, "agent_running_s:"},
		{"pending, in a later revision", func(m *MinuteBucket) { m.State, m.Rev = StatePending, 3 }, ""},
		{"unknown state", func(m *MinuteBucket) { m.State = "done" }, "state:"},
		{"unknown witness", func(m *MinuteBucket) { m.Witness = "seen" }, "witness:"},
		{"offline credit keeps its level", func(m *MinuteBucket) { m.Witness = Offline }, ""},
		{"backfilled credit is level C", func(m *MinuteBucket) { m.Witness = Backfilled }, `slots[0].ev: must be "C" in a backfilled minute, got "A"`},
		{"backfilled at level C", func(m *MinuteBucket) {
			m.Witness = Backfilled
			for i := range m.Slots {
				m.Slots[i].Ev = EvidenceC
			}
		}, ""},
		{"side-door labels on a slot", func(m *MinuteBucket) { m.Slots[2].Lbl = []Label{LabelViaTrack, LabelViaBridge} }, ""},
		{"slot labels name side doors only", func(m *MinuteBucket) { m.Slots[0].Lbl = []Label{LabelTypingModule} }, `slots[0].lbl[0]: "typing_module" is not one of`},
		{"slot labels are listed once, in order", func(m *MinuteBucket) {
			m.Slots[0].Lbl = []Label{LabelViaBridge, LabelViaTrack}
		}, "slots[0].lbl: must list each label once"},
		{"a slot with no credit has no labels", func(m *MinuteBucket) {
			m.Slots[0] = BucketSlot{Lbl: []Label{LabelViaTrack}}
			m.CreditedS = 45
		}, "slots[0].lbl: a slot with no credit"},
	})
}

func TestMinuteBucketShareable(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, orig, share MinuteBucket)
	}{
		{"anchor ids stay on the device", func(t *testing.T, orig, share MinuteBucket) {
			if share.Anchors != nil {
				t.Errorf("Shareable kept anchors %q", share.Anchors)
			}
		}},
		{"the original keeps its anchor ids", func(t *testing.T, orig, share MinuteBucket) {
			if want := tier1Bucket().Anchors; !reflect.DeepEqual(orig.Anchors, want) {
				t.Errorf("Shareable changed the original's anchors to %q", orig.Anchors)
			}
		}},
		{"every other field is kept", func(t *testing.T, orig, share MinuteBucket) {
			share.Anchors = orig.Anchors
			if !reflect.DeepEqual(share, orig) {
				t.Errorf("Shareable changed more than the local fields\n got: %+v\nwant: %+v", share, orig)
			}
		}},
		{"a credited bucket validates without its anchor ids", func(t *testing.T, orig, share MinuteBucket) {
			if err := share.Validate(); err != nil {
				t.Errorf("the shareable bucket doesn't validate: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := tier1Bucket()
			tt.check(t, orig, orig.Shareable())
		})
	}
}

// objectKeys returns the keys of the JSON object b, in order.
func objectKeys(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("%s isn't a JSON object", b)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("reading a key of %s: %v", b, err)
		}
		keys = append(keys, tok.(string))
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			t.Fatalf("reading the value of %q in %s: %v", keys[len(keys)-1], b, err)
		}
	}
	return keys
}

// TestTier1Shape pins what a minute bucket uploads: the JSON keys of its
// shareable form, and of each of its four slots, are DESIGN 4.3's tier-1
// list, in order. A new field fails here until it is local, or Karim adds
// it to DESIGN 4.3 (PLAN ground rule 10).
func TestTier1Shape(t *testing.T) {
	m := tier1Bucket()
	for _, v := range append([]reflect.Value{reflect.ValueOf(m)}, sliceValues(m.Slots)...) {
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).IsZero() {
				t.Fatalf("tier1Bucket leaves %s.%s unset; set every field so this test sees its key", v.Type().Name(), v.Type().Field(i).Name)
			}
		}
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("tier1Bucket doesn't validate: %v", err)
	}
	b, err := json.Marshal(m.Shareable())
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Slots []json.RawMessage `json:"slots"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	bucketKeys := []string{"v", "params", "minute", "device", "slots", "credited_s", "uncredited", "agent_running_s", "state", "witness", "rev"}
	slotKeys := []string{"p", "cr", "why", "kind", "ev", "lbl"}
	type keysCase struct {
		name string
		json []byte
		want []string
	}
	tests := []keysCase{{"a shareable bucket has DESIGN 4.3's fields and no anchor ids", b, bucketKeys}}
	for i, raw := range wire.Slots {
		tests = append(tests, keysCase{fmt.Sprintf("slot %d has DESIGN 4.3's slot fields", i), raw, slotKeys})
	}
	if len(wire.Slots) != 4 {
		t.Errorf("the shareable bucket has %d slots, DESIGN 4.3 says 4", len(wire.Slots))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := objectKeys(t, tt.json); !slices.Equal(got, tt.want) {
				t.Errorf("keys = %q, want %q\njson: %s", got, tt.want, tt.json)
			}
		})
	}
}

// sliceValues returns each element of s as a reflect.Value.
func sliceValues[T any](s []T) []reflect.Value {
	vs := make([]reflect.Value, len(s))
	for i := range s {
		vs[i] = reflect.ValueOf(s[i])
	}
	return vs
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
		{"a submission may cover several projects", func(r *Rollup) {
			*r = validSubmission()
			r.ProjectKey, r.CreditedByProject = "", map[string]float64{"p_9f2c": 15000, "p_77aa": 6240}
		}, ""},
		{"a submission's projects share its credit", func(r *Rollup) {
			*r = validSubmission()
			r.ProjectKey, r.CreditedByProject = "", map[string]float64{"p_9f2c": 15000, "p_77aa": 6000}
		}, "credited_by_project: sums to 21000"},
		{"a keyed submission's breakdown may name its own project", func(r *Rollup) {
			*r = validSubmission()
			r.CreditedByProject = map[string]float64{"p_9f2c": 21240}
		}, ""},
		{"a keyed submission's breakdown names no other project", func(r *Rollup) {
			*r = validSubmission()
			r.CreditedByProject = map[string]float64{"p_77aa": 21240}
		}, `credited_by_project: names project "p_77aa", but the submission covers only its project_key "p_9f2c"`},
		{"a keyed submission doesn't share its credit with another project", func(r *Rollup) {
			*r = validSubmission()
			r.CreditedByProject = map[string]float64{"p_9f2c": 15000, "p_77aa": 6240}
		}, `credited_by_project: names project "p_77aa"`},
		{"a submission's project is a key", func(r *Rollup) { *r = validSubmission(); r.ProjectKey = "/home/teen/game" }, "project_key:"},
		{"unknown level", func(r *Rollup) { r.Level = "week" }, "level:"},
		{"a day spans projects", func(r *Rollup) { *r = validDayRollup(); r.ProjectKey = "p_9f2c" }, "project_key: a day rollup spans projects"},
		{"other levels have a project", func(r *Rollup) { r.ProjectKey = "" }, "project_key:"},
		{"end isn't before start", func(r *Rollup) { r.End = r.Start.Add(-time.Hour) }, "end: is before start"},
		{"by reason sums to credited", func(r *Rollup) { r.CreditedByReason[ReasonPrompt] = 10 }, "credited_by_reason: sums to 50"},
		{"by reason uses credit reasons", func(r *Rollup) { r.CreditedByReason = map[Reason]float64{ReasonAbsent: 120} }, "credited_by_reason: has an invalid key"},
		{"by kind sums to credited", func(r *Rollup) { r.CreditedByKind[ActivityPrompting] = 1 }, "credited_by_kind: sums to 41"},
		{"by kind uses activities", func(r *Rollup) { r.CreditedByKind["napping"] = 0 }, "credited_by_kind: has an invalid key"},
		{"by kind matches by reason", func(r *Rollup) {
			r.CreditedByKind = map[Activity]float64{ActivityPrompting: 40, ActivityReadingOutput: 80}
		}, "but credited_by_reason puts 80 s there"},
		{"credit fits the window", func(r *Rollup) { r.End = r.Start.Add(time.Minute) }, "credited_s: is 120 s, more than the 60 s from start to end"},
		{"totals stay within the rollup", func(r *Rollup) { *r = validSubmission(); r.Totals.CreditedS += 100 }, "totals.credited_s: is 21340 s, more than the rollup's 21240 s"},
		{"uncredited uses X codes", func(r *Rollup) { r.Uncredited[ReasonPrompt] = 1 }, "uncredited: has an invalid key"},
		{"by evidence sums to credited", func(r *Rollup) { r.CreditedByEvidence[EvidenceC] = 5 }, "credited_by_evidence: sums to 125"},
		{"by project is for days and submissions", func(r *Rollup) { r.CreditedByProject = map[string]float64{"p_9f2c": 120} }, "credited_by_project: is for day and submission rollups only"},
		{"by project sums to credited", func(r *Rollup) { *r = validDayRollup(); r.CreditedByProject["p_77aa"] = 0 }, "credited_by_project: sums to 100"},
		{"anchor counts aren't negative", func(r *Rollup) { r.AnchorCounts[AnchorInterrupt] = -1 }, "anchor_counts: has a negative count"},
		{"submissions carry totals", func(r *Rollup) { *r = validSubmission(); r.Totals = nil }, "totals: missing"},
		{"totals are for submissions", func(r *Rollup) { tot := handbookTotals(); r.Totals = &tot }, "totals: is for submission rollups only"},
		{"totals are checked", func(r *Rollup) { *r = validSubmission(); r.Totals.HandbookS = 18000 }, "totals.handbook_s: is 18000"},
	})
}

func TestSubmissionTotalsValidate(t *testing.T) {
	runCases(t, handbookTotals, []derivedCase[SubmissionTotals]{
		{"the DESIGN 9.3 example", nil, ""},
		{"values rounded for display", func(s *SubmissionTotals) { s.HandbookS = math.Round(s.HandbookS*10) / 10; s.AIRatio = ptr(0.62) }, ""},
		{"no AI time, no ratio", func(s *SubmissionTotals) {
			s.AILoggedS, s.CodingLoggedS, s.HandbookS, s.AIRatio = 0, s.LoggedS, s.LoggedS, nil
			s.AICreditedS, s.HandCreditedS = 0, s.CreditedS
		}, ""},
		{"witness labels split credited", func(s *SubmissionTotals) { s.WitnessedS = 20000 }, "witnessed_s: is 20000, but credited_s - offline_s - backfilled_s = 20160"},
		{"levels split credited", func(s *SubmissionTotals) { s.LevelAS = 15000 }, "level_a_s: is 15000, but credited_s - level_b_s - level_c_s = 15080"},
		{"bands split credited", func(s *SubmissionTotals) { s.BandCorroboratedS = 17000 }, "band_corroborated_s: is 17000"},
		{"the backfilled band is level C", func(s *SubmissionTotals) {
			s.BandBackfilledS, s.BandWitnessedS = 720, 2520
		}, "band_backfilled_s: is 720, but level_c_s = 360"},
		{"the offline band holds offline seconds", func(s *SubmissionTotals) {
			s.BandOfflineS, s.BandWitnessedS = 1080, 2520
		}, "band_offline_s: is 1080, more than offline_s = 720"},
		{"the witnessed bands hold witnessed seconds", func(s *SubmissionTotals) {
			s.WitnessedS, s.OfflineS = 19800, 1080
		}, "band_witnessed_s: is 2880, more than witnessed_s - band_corroborated_s = 2520"},
		{"held seconds sit outside credited", func(s *SubmissionTotals) { s.HeldS = 600 }, ""},
		{"a track loop's level-C credit", func(s *SubmissionTotals) { s.ViaTrackS = 360 }, ""},
		{"track seconds fit in credited", func(s *SubmissionTotals) { s.ViaTrackS = 30000 }, "via_track_s: is 30000, more than credited_s = 21240"},
		{"no negative held time", func(s *SubmissionTotals) { s.HeldS = -1 }, "held_s:"},
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
	// DESIGN 9.3's handbook text rounds these: "Hackatime logged 9.1 h, 7.4 h
	// of it as AI coding", "allows at most 4.2 h", "credited 5.9 h: 4.8 h
	// corroborated, 0.8 h witnessed, 0.2 h offline, 0.1 h backfilled",
	// "credited 4.6 h (62%)" and "71% of credited time had keyboard-level
	// evidence (level A)". Its Deflation Justification reads "deflated from
	// Hackatime's 9.1 h to pairtime's 5.9 h: agent-only runtime 2.1 h,
	// re-roll loops 0.6 h, idle reading 0.5 h excluded".
	tot := handbookTotals()
	hours := func(s float64) float64 { return math.Round(s/360) / 10 }
	percent := func(part, whole float64) float64 { return math.Round(100 * part / whole) }
	for _, tt := range []struct {
		name      string
		got, want float64
	}{
		{"Hackatime logged 9.1 h", hours(tot.LoggedS), 9.1},
		{"7.4 h of it AI coding", hours(tot.AILoggedS), 7.4},
		{"the handbook allows 4.2 h", hours(tot.HandbookS), 4.2},
		{"pairtime credited 5.9 h", hours(tot.CreditedS), 5.9},
		{"4.8 h corroborated", hours(tot.BandCorroboratedS), 4.8},
		{"0.8 h witnessed", hours(tot.BandWitnessedS), 0.8},
		{"0.2 h offline", hours(tot.BandOfflineS), 0.2},
		{"0.1 h backfilled", hours(tot.BandBackfilledS), 0.1},
		{"4.6 h of AI time credited", hours(tot.AICreditedS), 4.6},
		{"62% of AI time credited", math.Round(*tot.AIRatio * 100), 62},
		{"71% of credited time at level A", percent(tot.LevelAS, tot.CreditedS), 71},
		{"agent-only runtime 2.1 h excluded", hours(tot.AgentOnlyS), 2.1},
		{"re-roll loops 0.6 h excluded", hours(tot.ExcludedLoopsS), 0.6},
		{"idle reading 0.5 h excluded", hours(tot.IdleS), 0.5},
		{"deflated by 3.2 h from 9.1 h to 5.9 h", hours(tot.LoggedS - tot.CreditedS), 3.2},
		{"the three exclusions are the whole 3.2 h", hours(tot.AgentOnlyS + tot.ExcludedLoopsS + tot.IdleS), 3.2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, DESIGN 9.3 says %v", tt.got, tt.want)
			}
		})
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
