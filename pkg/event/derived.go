// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"maps"
	"math"
	"slices"
	"time"
)

// The records the scorer derives from events, in the order it builds them
// (SM 1.3). All of them stay on the device except MinuteBucket and Rollup,
// which are uploaded and carry numbers, reason codes and event ids only.

// SlotMS is the length of a slot in milliseconds. The 15-second slot is
// the atomic unit of credit because it is the presence sample rate.
const SlotMS = 15_000

// Presence says how a slot's presence was decided (SM 2.1).
type Presence string

const (
	// PresentYes is input during the slot.
	PresentYes Presence = "yes"
	// PresentBridged is a short no-input run with input on both sides.
	PresentBridged Presence = "bridged"
	// PresentTail is just after the last input before a longer gap.
	PresentTail Presence = "tail"
	// PresentReturned is a longer silent run that ended in a substantive
	// act, of which only a bounded part can be credited.
	PresentReturned Presence = "returned"
	PresentNo       Presence = "no"
	// PresentUnknown means the sampler wasn't running, as in lookback.
	PresentUnknown Presence = "unknown"
)

var presences = enum[Presence]{values: []Presence{
	PresentYes, PresentBridged, PresentTail, PresentReturned, PresentNo, PresentUnknown,
}}

// Origin is the scorer's verdict on who or what started a turn (SM 2.2).
type Origin string

const (
	OriginScheduled    Origin = "scheduled"
	OriginSelfContinue Origin = "self_continue"
	OriginSubagent     Origin = "subagent"
	OriginHeadless     Origin = "headless"
	OriginCloud        Origin = "cloud"
	OriginHumanTyped   Origin = "human_typed"
	OriginHumanPasted  Origin = "human_pasted"
	OriginNoInput      Origin = "no_input"
)

var origins = enum[Origin]{values: []Origin{
	OriginScheduled, OriginSelfContinue, OriginSubagent, OriginHeadless,
	OriginCloud, OriginHumanTyped, OriginHumanPasted, OriginNoInput,
}}

// Human reports whether a person submitted the turn's prompt.
func (o Origin) Human() bool { return o == OriginHumanTyped || o == OriginHumanPasted }

// PromptClass sorts a human prompt (SM 2.2).
type PromptClass string

const (
	ClassSubstantive PromptClass = "substantive"
	ClassLowEffort   PromptClass = "low_effort"
	ClassAnswer      PromptClass = "answer"
	ClassErrorPaste  PromptClass = "error_paste"
)

var promptClasses = enum[PromptClass]{values: []PromptClass{
	ClassSubstantive, ClassLowEffort, ClassAnswer, ClassErrorPaste,
}}

// AnchorKind is the human act behind an anchor, one per row of SM 2.3.
type AnchorKind string

const (
	// AnchorSubstantivePrompt covers prompts of class substantive and
	// error_paste.
	AnchorSubstantivePrompt AnchorKind = "substantive_prompt"
	AnchorAnswer            AnchorKind = "answer"
	AnchorGoAhead           AnchorKind = "go_ahead"
	AnchorQueuedMessage     AnchorKind = "queued_message"
	AnchorPermissionAnswer  AnchorKind = "permission_answer"
	AnchorInterrupt         AnchorKind = "interrupt"
	AnchorTypedEdit         AnchorKind = "typed_edit"
	AnchorEditorNavigation  AnchorKind = "editor_navigation"
	AnchorTypedCommand      AnchorKind = "typed_command"
	AnchorHumanCommit       AnchorKind = "human_commit"
)

var anchorKinds = enum[AnchorKind]{values: []AnchorKind{
	AnchorSubstantivePrompt, AnchorAnswer, AnchorGoAhead, AnchorQueuedMessage,
	AnchorPermissionAnswer, AnchorInterrupt, AnchorTypedEdit,
	AnchorEditorNavigation, AnchorTypedCommand, AnchorHumanCommit,
}}

// Activity is what a credited second was spent on (SM 5.1): the server's
// six activity kinds plus supervising and lookup.
type Activity string

const (
	ActivityPrompting     Activity = "prompting"
	ActivityReadingOutput Activity = "reading_output"
	ActivitySupervising   Activity = "supervising"
	ActivityReviewing     Activity = "reviewing"
	ActivityTesting       Activity = "testing"
	ActivityDebugging     Activity = "debugging"
	ActivityHandEdit      Activity = "hand_edit"
	ActivityLookup        Activity = "lookup"
)

var activities = enum[Activity]{values: []Activity{
	ActivityPrompting, ActivityReadingOutput, ActivitySupervising, ActivityReviewing,
	ActivityTesting, ActivityDebugging, ActivityHandEdit, ActivityLookup,
}}

// Evidence is how strong the evidence behind credited seconds is (SM 5.1).
type Evidence string

const (
	// EvidenceA is witnessed, present by OS input, with anchors backed by
	// keyboard input.
	EvidenceA Evidence = "A"
	// EvidenceB is witnessed and present by OS input, without a
	// keyboard-specific signal.
	EvidenceB Evidence = "B"
	// EvidenceC is backfilled, or presence unknown (lookback).
	EvidenceC Evidence = "C"
)

var evidences = enum[Evidence]{values: []Evidence{EvidenceA, EvidenceB, EvidenceC}}

// BucketState is a minute bucket's state (SM 5.1).
type BucketState string

const (
	StateFinal BucketState = "final"
	// StateProvisional holds half-weight seconds waiting for a progress
	// event (SM 4.3).
	StateProvisional BucketState = "provisional"
	StateRestored    BucketState = "restored"
)

var bucketStates = enum[BucketState]{values: []BucketState{StateFinal, StateProvisional, StateRestored}}

// Witness says whether the server saw a minute's chain head in time.
type Witness string

const (
	// Witnessed means the chain head covering the minute reached the
	// server within 15 minutes.
	Witnessed  Witness = "witnessed"
	Backfilled Witness = "backfilled"
)

var witnesses = enum[Witness]{values: []Witness{Witnessed, Backfilled}}

// RollupLevel is the span a Rollup sums over (SM 5.3).
type RollupLevel string

const (
	LevelWorkSession RollupLevel = "work_session"
	LevelDay         RollupLevel = "day"
	LevelProject     RollupLevel = "project"
	LevelSubmission  RollupLevel = "submission"
)

var rollupLevels = enum[RollupLevel]{values: []RollupLevel{
	LevelWorkSession, LevelDay, LevelProject, LevelSubmission,
}}

// Slot is one 15-second presence slot (SM 1.3, 2.1, 2.5).
type Slot struct {
	// Start is the slot's start in Unix milliseconds, on a :00, :15, :30
	// or :45 second boundary.
	Start    int64 `json:"start"`
	InputAny bool  `json:"input_any"`
	// InputKeys is nil where the OS can't report keyboard input apart from
	// other input.
	InputKeys *bool    `json:"input_keys"`
	Present   Presence `json:"present"`
	// FocusClass and FocusProject describe the app that held focus longest
	// in the slot and the project it resolves to.
	FocusClass   AppClass `json:"focus_class"`
	FocusProject string   `json:"focus_project,omitempty"`
	Asleep       bool     `json:"asleep"`
	Locked       bool     `json:"locked"`
}

// Validate reports every rule s breaks, or nil.
func (s Slot) Validate() error {
	c := &checker{}
	c.slotStart("start", s.Start)
	if s.InputKeys != nil && *s.InputKeys && !s.InputAny {
		c.addf("input_keys", "is true but input_any is false; a key press is also input")
	}
	checkEnum(c, "present", presences, s.Present)
	if s.Present == PresentYes && !s.InputAny {
		c.addf("present", "is %q but the slot had no input", s.Present)
	}
	if (s.Asleep || s.Locked) && s.Present != PresentNo {
		c.addf("present", "must be %q in a slot that overlaps sleep or a lock, got %q", PresentNo, s.Present)
	}
	checkEnum(c, "focus_class", appClasses, s.FocusClass)
	c.optKey("focus_project", s.FocusProject)
	return c.err()
}

// Turn is one prompt and the agent's work on it (SM 1.3, 2.2).
type Turn struct {
	SessionKey string `json:"session_key"`
	TurnKey    string `json:"turn_key"`
	TSubmit    int64  `json:"t_submit"`
	// TStop is 0 while the turn is still running.
	TStop  int64  `json:"t_stop,omitempty"`
	Origin Origin `json:"origin"`
	// PromptClass is set for human turns only.
	PromptClass PromptClass `json:"prompt_class,omitempty"`
	// Attempt is the turn's position in a re-roll chain, from 1 (SM 4.2),
	// for human turns. It is 0 for turns no person started.
	Attempt   int `json:"attempt"`
	WordsOut  int `json:"words_out"`
	DiffLines int `json:"diff_lines"`
	// Unread is set when the output arrived while nobody was looking.
	Unread bool `json:"unread"`
}

// Validate reports every rule t breaks, or nil.
func (t Turn) Validate() error {
	c := &checker{}
	c.key("session_key", t.SessionKey)
	if t.SessionKey == NoSession {
		c.addf("session_key", "a turn belongs to a harness session, got %q", t.SessionKey)
	}
	c.key("turn_key", t.TurnKey)
	c.unixMS("t_submit", t.TSubmit)
	if t.TStop != 0 {
		c.span("t_submit", t.TSubmit, "t_stop", t.TStop)
	}
	checkEnum(c, "origin", origins, t.Origin)
	if t.Origin.Human() {
		checkEnum(c, "prompt_class", promptClasses, t.PromptClass)
		if t.Attempt < 1 {
			c.addf("attempt", "must be at least 1 for a human turn, got %d", t.Attempt)
		}
	} else {
		if t.PromptClass != "" {
			c.addf("prompt_class", "only human turns have one, got %q for origin %q", t.PromptClass, t.Origin)
		}
		if t.Attempt != 0 {
			c.addf("attempt", "must be 0 for a turn no person started, got %d", t.Attempt)
		}
	}
	c.count("words_out", t.WordsOut)
	c.count("diff_lines", t.DiffLines)
	return c.err()
}

// Anchor is a human act that passed its evidence test and opens a window
// of credit around itself (SM 1.3, 2.3).
type Anchor struct {
	// AnchorID is the event_id of the human act.
	AnchorID   string     `json:"anchor_id"`
	Kind       AnchorKind `json:"kind"`
	T          int64      `json:"t"`
	ProjectKey string     `json:"project_key"`
	LeadS      float64    `json:"lead_s"`
	TrailS     float64    `json:"trail_s"`
	// DiscountedBy is the uncredited reason, such as X.reroll, under which
	// a dark-flow or progress rule discounted this anchor (SM 4.2, 4.3).
	DiscountedBy Reason `json:"discounted_by,omitempty"`
}

// Validate reports every rule a breaks, or nil.
func (a Anchor) Validate() error {
	c := &checker{}
	c.eventID("anchor_id", a.AnchorID)
	checkEnum(c, "kind", anchorKinds, a.Kind)
	c.unixMS("t", a.T)
	c.key("project_key", a.ProjectKey)
	c.seconds("lead_s", a.LeadS)
	c.seconds("trail_s", a.TrailS)
	if a.DiscountedBy != "" && !a.DiscountedBy.Uncredited() {
		c.addf("discounted_by", "must be an uncredited reason (X.*), got %q", a.DiscountedBy)
	}
	return c.err()
}

// CreditSlot is the credit one slot earned for one project under one
// reason (SM 1.3).
type CreditSlot struct {
	// Slot is the slot's start, in Unix milliseconds.
	Slot       int64  `json:"slot"`
	ProjectKey string `json:"project_key,omitempty"`
	// CreditedS is the seconds credited after weighting: 0 to 15, in
	// steps of 0.5.
	CreditedS float64 `json:"credited_s"`
	// Weight is 1, 0.5 (the no-progress rule) or 0.
	Weight    float64  `json:"weight"`
	Reason    Reason   `json:"reason"`
	AnchorIDs []string `json:"anchor_ids,omitempty"`
}

// Validate reports every rule s breaks, or nil.
func (s CreditSlot) Validate() error {
	c := &checker{}
	c.slotStart("slot", s.Slot)
	c.optKey("project_key", s.ProjectKey)
	c.slotSeconds("credited_s", s.CreditedS)
	if s.Weight != 0 && s.Weight != 0.5 && s.Weight != 1 {
		c.addf("weight", "must be 0, 0.5 or 1, got %v", s.Weight)
	} else if s.CreditedS > 15*s.Weight {
		c.addf("credited_s", "is %v, more than 15 seconds at weight %v", s.CreditedS, s.Weight)
	}
	credited := s.CreditedS > 0
	switch {
	case !s.Reason.Valid():
		c.addf("reason", "%q is not a reason code", s.Reason)
	case credited && !s.Reason.Credited():
		c.addf("reason", "credited seconds need a credit reason, got %q", s.Reason)
	case !credited && s.Reason.Credited():
		c.addf("reason", "is the credit reason %q, but nothing was credited", s.Reason)
	}
	if credited && s.ProjectKey == "" {
		c.addf("project_key", "missing; credited seconds belong to a project")
	}
	if credited && len(s.AnchorIDs) == 0 {
		c.addf("anchor_ids", "missing; every credited second traces back to an anchor")
	}
	c.eventIDs("anchor_ids", s.AnchorIDs)
	return c.err()
}

// MinuteBucket is the scorer's output for one device and UTC minute in
// which anything was credited or an agent was running. It is the tier-1
// upload (SM 5.1).
type MinuteBucket struct {
	// V is the scoring_version that produced the record.
	V string `json:"v"`
	// Params is "sha256:" and the SHA-256 of the parameter table (SM 8).
	Params string    `json:"params"`
	Minute time.Time `json:"minute"`
	Device string    `json:"device"`
	// Slots holds the minute's four slots, in order.
	Slots     []BucketSlot `json:"slots"`
	CreditedS float64      `json:"credited_s"`
	// Uncredited holds the seconds that were looked at and not credited,
	// by uncredited reason.
	Uncredited map[Reason]float64 `json:"uncredited,omitempty"`
	// AgentRunningS is the seconds an agent turn was running, in any
	// project.
	AgentRunningS float64 `json:"agent_running_s"`
	// Anchors are the event ids whose windows covered credited seconds.
	Anchors []string    `json:"anchors,omitempty"`
	State   BucketState `json:"state"`
	Witness Witness     `json:"witness"`
}

// BucketSlot is one slot of a MinuteBucket, with the short field names of
// the upload format.
type BucketSlot struct {
	Project string `json:"p,omitempty"`
	// Credited is the slot's credited seconds, 0 to 15 in steps of 0.5.
	Credited float64 `json:"cr"`
	// Why is the dominant credit reason, and Kind the activity it counts as.
	Why  Reason   `json:"why,omitempty"`
	Kind Activity `json:"kind,omitempty"`
	Ev   Evidence `json:"ev,omitempty"`
}

// Validate reports every rule m breaks, or nil.
func (m MinuteBucket) Validate() error {
	c := &checker{}
	c.scoringVersion("v", m.V)
	c.paramsHash("params", m.Params)
	c.instant("minute", m.Minute)
	if _, offset := m.Minute.Zone(); offset != 0 || m.Minute.Second() != 0 || m.Minute.Nanosecond() != 0 {
		c.addf("minute", "must be the start of a UTC minute, got %s", m.Minute.Format(time.RFC3339Nano))
	}
	c.key("device", m.Device)
	if len(m.Slots) != 4 {
		c.addf("slots", "must have 4 entries, got %d", len(m.Slots))
	}
	slotSum := 0.0
	for i, s := range m.Slots {
		c.within(fieldIndex("slots", i), s.check)
		slotSum += s.Credited
	}
	c.sumsTo("slots[].cr", slotSum, "credited_s", m.CreditedS)
	uncredited := breakdown(c, "uncredited", m.Uncredited, Reason.Uncredited)
	if total := m.CreditedS + uncredited; total > 60+sumTolerance {
		c.addf("uncredited", "credited and uncredited seconds add up to %v, more than a minute", total)
	}
	c.seconds("agent_running_s", m.AgentRunningS)
	if m.AgentRunningS > 60 {
		c.addf("agent_running_s", "is %v, more than a minute", m.AgentRunningS)
	}
	c.eventIDs("anchors", m.Anchors)
	if m.CreditedS > 0 && len(m.Anchors) == 0 {
		c.addf("anchors", "missing; every credited second traces back to an anchor")
	}
	checkEnum(c, "state", bucketStates, m.State)
	checkEnum(c, "witness", witnesses, m.Witness)
	return c.err()
}

func (s BucketSlot) check(c *checker) {
	c.slotSeconds("cr", s.Credited)
	if s.Credited > 0 {
		c.key("p", s.Project)
		if !s.Why.Credited() {
			c.addf("why", "must be a credit reason, got %q", s.Why)
		} else if want := s.Why.Activity(); s.Kind != want {
			c.addf("kind", "must be %q for reason %q, got %q", want, s.Why, s.Kind)
		}
		checkEnum(c, "ev", evidences, s.Ev)
		return
	}
	c.optKey("p", s.Project)
	if s.Why != "" || s.Kind != "" {
		c.addf("why", "a slot with no credit has no reason or kind, got %q and %q", s.Why, s.Kind)
	}
	checkOptEnum(c, "ev", evidences, s.Ev)
}

// Rollup sums minute buckets over a work session, a day, a project or a
// submission (SM 5.3). Credited and uncredited seconds roll up separately,
// by reason code and by activity kind, so the "what was left out"
// breakdown survives every level.
type Rollup struct {
	V      string      `json:"v"`
	Params string      `json:"params"`
	Level  RollupLevel `json:"level"`
	// ProjectKey is empty at the day level, which spans projects.
	ProjectKey string    `json:"project_key,omitempty"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	CreditedS  float64   `json:"credited_s"`
	// CreditedByReason and CreditedByKind each sum to CreditedS.
	CreditedByReason map[Reason]float64   `json:"credited_by_reason,omitempty"`
	CreditedByKind   map[Activity]float64 `json:"credited_by_kind,omitempty"`
	Uncredited       map[Reason]float64   `json:"uncredited,omitempty"`
	// CreditedByEvidence, when present, sums to CreditedS.
	CreditedByEvidence map[Evidence]float64 `json:"credited_by_evidence,omitempty"`
	// CreditedByProject is for the day level only. When present it sums to
	// CreditedS.
	CreditedByProject map[string]float64 `json:"credited_by_project,omitempty"`
	AgentRunningS     float64            `json:"agent_running_s"`
	AnchorCounts      map[AnchorKind]int `json:"anchor_counts,omitempty"`
	// Totals is set at the submission level, and only there.
	Totals *SubmissionTotals `json:"totals,omitempty"`
}

// Validate reports every rule r breaks, or nil.
func (r Rollup) Validate() error {
	c := &checker{}
	c.scoringVersion("v", r.V)
	c.paramsHash("params", r.Params)
	checkEnum(c, "level", rollupLevels, r.Level)
	if r.Level == LevelDay {
		if r.ProjectKey != "" {
			c.addf("project_key", "a day rollup spans projects, got %q", r.ProjectKey)
		}
	} else {
		c.key("project_key", r.ProjectKey)
	}
	c.instant("start", r.Start)
	c.instant("end", r.End)
	if r.End.Before(r.Start) {
		c.addf("end", "is before start")
	}
	c.seconds("credited_s", r.CreditedS)
	c.sumsTo("credited_by_reason", breakdown(c, "credited_by_reason", r.CreditedByReason, Reason.Credited), "credited_s", r.CreditedS)
	c.sumsTo("credited_by_kind", breakdown(c, "credited_by_kind", r.CreditedByKind, activities.has), "credited_s", r.CreditedS)
	breakdown(c, "uncredited", r.Uncredited, Reason.Uncredited)
	if len(r.CreditedByEvidence) > 0 {
		c.sumsTo("credited_by_evidence", breakdown(c, "credited_by_evidence", r.CreditedByEvidence, evidences.has), "credited_s", r.CreditedS)
	}
	if len(r.CreditedByProject) > 0 {
		if r.Level != LevelDay {
			c.addf("credited_by_project", "is for day rollups only, got level %q", r.Level)
		}
		c.sumsTo("credited_by_project", breakdown(c, "credited_by_project", r.CreditedByProject, isKey), "credited_s", r.CreditedS)
	}
	c.seconds("agent_running_s", r.AgentRunningS)
	for _, k := range slices.Sorted(maps.Keys(r.AnchorCounts)) {
		if !anchorKinds.has(k) {
			c.addf("anchor_counts", "has an invalid key %q", k)
		}
		if n := r.AnchorCounts[k]; n < 0 {
			c.addf("anchor_counts", "has a negative count for %q: %d", k, n)
		}
	}
	switch {
	case r.Level == LevelSubmission && r.Totals == nil:
		c.addf("totals", "missing; a submission rollup carries its totals")
	case r.Level != LevelSubmission && r.Totals != nil:
		c.addf("totals", "is for submission rollups only, got level %q", r.Level)
	case r.Totals != nil:
		c.within("totals", r.Totals.check)
	}
	return c.err()
}

// SubmissionTotals are the totals of a submission (SM 5.3) and the numbers
// that answer the handbook's one-third rule (SM 5.4).
type SubmissionTotals struct {
	// LoggedS is what Hackatime would log for the same activity: all
	// heartbeats and agent events as one series, summed as min(gap, 120 s).
	LoggedS float64 `json:"logged_s"`
	// CreditedS is the credited seconds after the uncommitted cap (SM 4.3).
	CreditedS   float64 `json:"credited_s"`
	WitnessedS  float64 `json:"witnessed_s"`
	BackfilledS float64 `json:"backfilled_s"`
	AgentOnlyS  float64 `json:"agent_only_s"`
	// ExcludedLoopsS is X.reroll + X.rewrite_loop + X.same_error + X.autopilot.
	ExcludedLoopsS float64 `json:"excluded_loops_s"`
	// IdleS is X.absent seconds while an agent session was open.
	IdleS float64 `json:"idle_s"`
	// AILoggedS is the LoggedS rule applied to agent events only: what
	// Hackatime would call "ai coding".
	AILoggedS     float64 `json:"ai_logged_s"`
	CodingLoggedS float64 `json:"coding_logged_s"`
	// HandbookS is CodingLoggedS + AILoggedS/3, the most a reviewer could
	// approve from Hackatime data under the handbook.
	HandbookS     float64 `json:"handbook_s"`
	AICreditedS   float64 `json:"ai_credited_s"`
	HandCreditedS float64 `json:"hand_credited_s"`
	// AIRatio is AICreditedS / AILoggedS, or nil when AILoggedS is 0. Above
	// 1/3 means pairtime credits more than the handbook's third.
	AIRatio *float64 `json:"ai_ratio"`
}

// Totals may be rounded for display, so their identities hold to within
// totalsTolerance seconds, and AIRatio to within ratioTolerance.
const (
	totalsTolerance = 0.5
	ratioTolerance  = 0.005
)

// Validate reports every rule t breaks, or nil.
func (t SubmissionTotals) Validate() error { return validate(t.check) }

func (t SubmissionTotals) check(c *checker) {
	for _, f := range []struct {
		name string
		v    float64
	}{
		{"logged_s", t.LoggedS}, {"credited_s", t.CreditedS}, {"witnessed_s", t.WitnessedS},
		{"backfilled_s", t.BackfilledS}, {"agent_only_s", t.AgentOnlyS},
		{"excluded_loops_s", t.ExcludedLoopsS}, {"idle_s", t.IdleS}, {"ai_logged_s", t.AILoggedS},
		{"coding_logged_s", t.CodingLoggedS}, {"handbook_s", t.HandbookS},
		{"ai_credited_s", t.AICreditedS}, {"hand_credited_s", t.HandCreditedS},
	} {
		c.seconds(f.name, f.v)
	}
	identity := func(field string, got float64, rule string, want float64) {
		if math.Abs(got-want) > totalsTolerance {
			c.addf(field, "is %v, but %s = %v", got, rule, want)
		}
	}
	identity("witnessed_s", t.WitnessedS, "credited_s - backfilled_s", t.CreditedS-t.BackfilledS)
	identity("coding_logged_s", t.CodingLoggedS, "logged_s - ai_logged_s", t.LoggedS-t.AILoggedS)
	identity("handbook_s", t.HandbookS, "coding_logged_s + ai_logged_s / 3", t.CodingLoggedS+t.AILoggedS/3)
	identity("hand_credited_s", t.HandCreditedS, "credited_s - ai_credited_s", t.CreditedS-t.AICreditedS)
	switch {
	case t.AILoggedS > 0 && t.AIRatio == nil:
		c.addf("ai_ratio", "missing; it is ai_credited_s / ai_logged_s whenever ai_logged_s > 0")
	case t.AILoggedS > 0:
		if want := t.AICreditedS / t.AILoggedS; !finite(*t.AIRatio) || math.Abs(*t.AIRatio-want) > ratioTolerance {
			c.addf("ai_ratio", "is %v, but ai_credited_s / ai_logged_s = %v", *t.AIRatio, want)
		}
	case t.AIRatio != nil:
		c.addf("ai_ratio", "must be null when ai_logged_s is 0, got %v", *t.AIRatio)
	}
}
