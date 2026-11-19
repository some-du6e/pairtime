// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"maps"
	"math"
	"reflect"
	"slices"
	"time"
)

// The records the scorer derives from events, in the order it builds them
// (SM 1.3). All of them stay on the device except the minute bucket, which
// is the tier-1 upload. Its Shareable form carries numbers and codes only,
// no event ids (DESIGN 4.1 E-5, 4.3). Rollups are summed on both sides: the
// device keeps its own for local views (DESIGN 3.5), and the server sums
// the buckets it receives (DESIGN 3.10).

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

// Evidence is how strong the evidence behind credited seconds is: SM 5.1's
// levels with DESIGN 6.4's provenance rule, which also looks at the
// anchor's via.
type Evidence string

const (
	// EvidenceA is witnessed, or offline within DESIGN 6.2 D6's
	// conditions; present by OS input; with an anchor backed by keyboard
	// input (input_keys) that arrived via hook, log, shell, git or editor,
	// or a track, plugin or bridge anchor with keyboard input in the host
	// window in the 10 s before it.
	EvidenceA Evidence = "A"
	// EvidenceB is witnessed, or offline within D6's conditions; present by
	// OS input with no keyboard-specific signal (input_any only); with an
	// anchor that arrived via hook, log, shell, git or editor.
	EvidenceB Evidence = "B"
	// EvidenceC is everything else: backfilled and lookback minutes, track,
	// plugin and bridge anchors without keyboard corroboration,
	// editor_unverified anchors, and offline minutes beyond the cap or
	// without the forge push.
	EvidenceC Evidence = "C"
)

var evidences = enum[Evidence]{values: []Evidence{EvidenceA, EvidenceB, EvidenceC}}

// Label is a provenance label carried beside an evidence level. Labels
// never change a level (DESIGN 6.4). DESIGN 6.4's sixth label,
// editor_typing, isn't in v1 (DESIGN 5.11, 12 JB 3).
type Label string

const (
	// LabelViaTrack marks credit that rests on a `pairtime track` anchor,
	// whatever its level (DESIGN 5.4, 12 JB 3).
	LabelViaTrack Label = "via_track"
	// LabelViaPlugin marks credit that rests on a plugin anchor.
	LabelViaPlugin Label = "via_plugin"
	// LabelViaBridge marks credit that rests on a WakaTime bridge anchor,
	// verified or not.
	LabelViaBridge Label = "via_bridge"
	// LabelTypingModule marks an anchor whose keyboard evidence came from
	// the opt-in typing module (DESIGN 6.2 D16).
	LabelTypingModule Label = "typing_module"
	// LabelVM marks an anchor scored on a machine labeled as a VM, where the
	// typing module alone can't make it level A (DESIGN 6.2 D17).
	LabelVM Label = "vm"
)

var (
	// anchorLabels are the labels an anchor may carry.
	anchorLabels = enum[Label]{values: []Label{
		LabelViaTrack, LabelViaPlugin, LabelViaBridge, LabelTypingModule, LabelVM,
	}}
	// slotLabels are the side-door labels, the ones a minute bucket's slot
	// carries to tier 1 (DESIGN 4.1 E-6).
	slotLabels = enum[Label]{values: []Label{LabelViaTrack, LabelViaPlugin, LabelViaBridge}}
)

// sideDoorLabel returns the label an anchor that arrived via v carries, or
// "" when v isn't a labeled side door. editor_unverified anchors are level
// C but carry no label (DESIGN 6.4).
func sideDoorLabel(v Via) Label {
	switch v {
	case ViaTrack:
		return LabelViaTrack
	case ViaPlugin:
		return LabelViaPlugin
	case ViaBridge, ViaBridgeUnverified:
		return LabelViaBridge
	}
	return ""
}

// checkLabels checks a list of labels: each from set, at most once, and in
// set's order, so the same labels always encode the same way.
func checkLabels(c *checker, field string, set enum[Label], ls []Label) {
	last := -1
	for i, l := range ls {
		at := slices.Index(set.values, l)
		switch {
		case at < 0:
			c.addf(fieldIndex(field, i), "%q is not one of %q", l, set.values)
			return
		case at <= last:
			c.addf(field, "must list each label once, in the order %q", set.values)
			return
		}
		last = at
	}
}

// BucketState is a minute bucket's state (SM 5.1, DESIGN 6.2 D1).
type BucketState string

const (
	// StatePending is a bucket within PENDING_WINDOW_S (900 s) of the end
	// of its minute. Late events re-score it, and each change uploads a
	// new revision with a higher rev (DESIGN 3.7). After the window it is
	// final, provisional or restored.
	StatePending BucketState = "pending"
	StateFinal   BucketState = "final"
	// StateProvisional holds half-weight seconds waiting for a progress
	// event (SM 4.3).
	StateProvisional BucketState = "provisional"
	StateRestored    BucketState = "restored"
)

var bucketStates = enum[BucketState]{values: []BucketState{StatePending, StateFinal, StateProvisional, StateRestored}}

// Witness is the label a minute gets from when the server received the
// ledger batch holding its records (DESIGN 7.4).
type Witness string

const (
	// Witnessed means the batch reached the server within WITNESS_LAG_S
	// (900 s).
	Witnessed Witness = "witnessed"
	// Offline means late, but sealed on a continuous chain while the
	// ledger held upload-failure notes, then flushed in one catch-up
	// (DESIGN 4.1 E-3). Its credit keeps its computed level only up to
	// OFFLINE_CAP_S per submission and with a forge push within
	// OFFLINE_PUSH_S of reconnecting (DESIGN 6.2 D6), which a submission
	// decides.
	Offline Witness = "offline"
	// Backfilled is everything else that arrived late, and all lookback.
	// Its credit is level C.
	Backfilled Witness = "backfilled"
)

var witnesses = enum[Witness]{values: []Witness{Witnessed, Offline, Backfilled}}

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
	// Unread is set when the person replied too fast to have read the
	// final message: words_final >= 100 and the time from agent.stop to
	// the next prompt is under 0.25 * 60 * words_final / 238 seconds
	// (SM 4.2's autopilot test). It is about reply speed, not presence;
	// output nobody was there to see is SM 2.1's unread_prose_words.
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
	// DiscountedBy is the dark-flow discount applied to this anchor, if
	// any: X.reroll, X.rewrite_loop, X.same_error or X.autopilot (SM 4.2).
	DiscountedBy Reason `json:"discounted_by,omitempty"`
	// Via is how the agent received the record behind the act. With
	// presence and keyboard evidence it sets the anchor's level (DESIGN
	// 4.1 E-7, 6.4).
	Via Via `json:"via"`
	// Corroborated is set when a second record, written by a different
	// code path, saw the same act: a hook and a transcript line, a hook and
	// an OTel event, or an editor heartbeat that passed the peer-process
	// check plus a commit whose diffstat covers the file. It is one of the
	// conditions of DESIGN 6.4's corroborated band.
	Corroborated bool `json:"corroborated"`
	// Labels ride along with the anchor's level and never change it
	// (DESIGN 6.4). A track, plugin or bridge anchor always carries its
	// via_* label; no other anchor carries one.
	Labels []Label `json:"labels,omitempty"`
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
	if a.DiscountedBy != "" && !darkFlowReasons.has(a.DiscountedBy) {
		c.addf("discounted_by", "must be a dark-flow discount, one of %q, got %q", darkFlowReasons.values, a.DiscountedBy)
	}
	checkEnum(c, "via", vias, a.Via)
	checkLabels(c, "labels", anchorLabels, a.Labels)
	want := sideDoorLabel(a.Via)
	if want != "" && !slices.Contains(a.Labels, want) {
		c.addf("labels", "missing %q; a %s anchor always carries it", want, a.Via)
	}
	for _, l := range a.Labels {
		if slotLabels.has(l) && l != want {
			c.addf("labels", "has %q, but the anchor arrived via %q", l, a.Via)
		}
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
// which anything was credited or an agent was running (SM 5.1). Its
// Shareable form is the tier-1 upload, with exactly the fields DESIGN 4.3
// lists.
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
	// They stay on the device (DESIGN 4.1 E-5, 6.2 D9): an event id is a
	// UUIDv7, so it would give away the millisecond of every act, and
	// tier 1 doesn't need them. The server recomputes them from a
	// submission's reveal.
	Anchors []string    `json:"anchors,omitempty" pairtime:"local"`
	State   BucketState `json:"state"`
	Witness Witness     `json:"witness"`
	// Rev numbers the bucket's revisions, from 0. Each re-score inside the
	// pending window that changes the bucket raises it by one, and a higher
	// rev supersedes a lower one for the same device and minute (DESIGN
	// 3.7, 6.2 D1).
	Rev uint32 `json:"rev"`
}

// Shareable returns a copy of m without its local fields: the form that
// leaves the device (DESIGN 4.3). m itself is not modified.
func (m MinuteBucket) Shareable() MinuteBucket {
	clearLocal(reflect.ValueOf(&m).Elem())
	return m
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
	// Lbl lists the side-door labels of the anchors behind the slot's
	// credited seconds, so programs and reviewers see which minutes rest
	// on a side door (DESIGN 4.1 E-6): via_track, via_plugin and
	// via_bridge, each at most once and in that order.
	Lbl []Label `json:"lbl,omitempty"`
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
		// Backfilled credit is level C (DESIGN 6.4). Witnessed and offline
		// credit may be any level: track, plugin and bridge anchors are C
		// when witnessed, and offline minutes past the cap become C only
		// when a submission applies it.
		if m.Witness == Backfilled && s.Credited > 0 && s.Ev != EvidenceC {
			c.addf(fieldIndex("slots", i)+".ev", "must be %q in a backfilled minute, got %q", EvidenceC, s.Ev)
		}
	}
	c.seconds("credited_s", m.CreditedS)
	c.sumsTo("slots[].cr", slotSum, "credited_s", m.CreditedS)
	uncredited := breakdown(c, "uncredited", m.Uncredited, Reason.Uncredited)
	if total := m.CreditedS + uncredited; total > 60+sumTolerance {
		c.addf("uncredited", "credited and uncredited seconds add up to %v, more than a minute", total)
	}
	c.seconds("agent_running_s", m.AgentRunningS)
	if m.AgentRunningS > 60 {
		c.addf("agent_running_s", "is %v, more than a minute", m.AgentRunningS)
	}
	// Anchors may be absent even with credit: Shareable clears them. Each
	// credited CreditSlot still names its anchors on the device.
	c.eventIDs("anchors", m.Anchors)
	checkEnum(c, "state", bucketStates, m.State)
	checkEnum(c, "witness", witnesses, m.Witness)
	return c.err()
}

func (s BucketSlot) check(c *checker) {
	c.slotSeconds("cr", s.Credited)
	checkLabels(c, "lbl", slotLabels, s.Lbl)
	if s.Credited == 0 && len(s.Lbl) > 0 {
		c.addf("lbl", "a slot with no credit rests on no anchor, so it has no labels, got %q", s.Lbl)
	}
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
	// ProjectKey is empty at the day level, which spans projects, and at
	// the submission level when the submission covers several projects
	// (DESIGN 7.5). CreditedByProject can then split the credit.
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
	// CreditedByProject is for the day and submission levels only. When
	// present it sums to CreditedS. A submission with a ProjectKey may
	// name only that project in it.
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
	switch r.Level {
	case LevelDay:
		if r.ProjectKey != "" {
			c.addf("project_key", "a day rollup spans projects, got %q", r.ProjectKey)
		}
	case LevelSubmission:
		// A submission covers a set of projects (DESIGN 7.5): one key, or
		// none when it covers several. A keyed submission covers that one
		// project, so its breakdown can't put credit anywhere else.
		c.optKey("project_key", r.ProjectKey)
		if isKey(r.ProjectKey) {
			for _, k := range slices.Sorted(maps.Keys(r.CreditedByProject)) {
				if k != r.ProjectKey {
					c.addf("credited_by_project", "names project %q, but the submission covers only its project_key %q", k, r.ProjectKey)
				}
			}
		}
	default:
		c.key("project_key", r.ProjectKey)
	}
	c.instant("start", r.Start)
	c.instant("end", r.End)
	if r.End.Before(r.Start) {
		c.addf("end", "is before start")
	}
	c.seconds("credited_s", r.CreditedS)
	// One timeline per person: a second of wall time earns at most a
	// second of credit (SM 3.1, SM 5.1).
	if window := r.End.Sub(r.Start).Seconds(); !r.End.Before(r.Start) && r.CreditedS > window+sumTolerance {
		c.addf("credited_s", "is %v s, more than the %v s from start to end", r.CreditedS, window)
	}
	c.sumsTo("credited_by_reason", breakdown(c, "credited_by_reason", r.CreditedByReason, Reason.Credited), "credited_s", r.CreditedS)
	c.sumsTo("credited_by_kind", breakdown(c, "credited_by_kind", r.CreditedByKind, activities.has), "credited_s", r.CreditedS)
	checkKindsMatchReasons(c, r.CreditedByReason, r.CreditedByKind)
	breakdown(c, "uncredited", r.Uncredited, Reason.Uncredited)
	if len(r.CreditedByEvidence) > 0 {
		c.sumsTo("credited_by_evidence", breakdown(c, "credited_by_evidence", r.CreditedByEvidence, evidences.has), "credited_s", r.CreditedS)
	}
	if len(r.CreditedByProject) > 0 {
		if r.Level != LevelDay && r.Level != LevelSubmission {
			c.addf("credited_by_project", "is for day and submission rollups only, got level %q", r.Level)
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
		// The uncommitted cap (SM 4.3) only removes credit.
		if r.Totals.CreditedS > r.CreditedS+totalsTolerance {
			c.addf("totals.credited_s", "is %v s, more than the rollup's %v s", r.Totals.CreditedS, r.CreditedS)
		}
	}
	return c.err()
}

// checkKindsMatchReasons checks credited_by_kind against credited_by_reason
// activity by activity. Each credit reason counts as exactly one activity,
// so agreeing on the total isn't enough.
func checkKindsMatchReasons(c *checker, byReason map[Reason]float64, byKind map[Activity]float64) {
	fromReasons := map[Activity]float64{}
	for reason, s := range byReason {
		if a := reason.Activity(); a != "" {
			fromReasons[a] += s
		}
	}
	acts := slices.Collect(maps.Keys(fromReasons))
	for a := range byKind {
		if _, ok := fromReasons[a]; !ok {
			acts = append(acts, a)
		}
	}
	slices.Sort(acts)
	for _, a := range acts {
		if !activities.has(a) {
			continue // breakdown already reported the key
		}
		if math.Abs(fromReasons[a]-byKind[a]) > sumTolerance {
			c.addf("credited_by_kind", "has %v s of %s, but credited_by_reason puts %v s there", byKind[a], a, fromReasons[a])
		}
	}
}

// SubmissionTotals are the totals of a submission (SM 5.3), the numbers
// that answer the handbook's one-third rule (SM 5.4), and the splits the
// review page and the handbook text state (DESIGN 4.1 E-9, 7.7, 9.3).
type SubmissionTotals struct {
	// LoggedS is what Hackatime would log for the same activity: all
	// heartbeats and agent events as one series, summed as min(gap, 120 s).
	// Only records with via hook, log, otel, editor or bridge count; track,
	// plugin and unverified records never do (DESIGN 6.2 D19).
	LoggedS float64 `json:"logged_s"`
	// CreditedS is the credited seconds after the uncommitted cap (SM 4.3).
	// Held seconds aren't in it. Each credited second has one witness
	// label, one evidence level and one band, so each of the three splits
	// below sums to CreditedS.
	CreditedS float64 `json:"credited_s"`
	// WitnessedS, OfflineS and BackfilledS split CreditedS by witness label
	// (SM 5.3, DESIGN 7.4).
	WitnessedS  float64 `json:"witnessed_s"`
	OfflineS    float64 `json:"offline_s"`
	BackfilledS float64 `json:"backfilled_s"`
	// HeldS is the seconds the server holds because its re-score doesn't
	// reproduce them or their chain doesn't verify (DESIGN 6.2 D7). They
	// are outside CreditedS, the levels and the bands until a reviewer
	// clears them.
	HeldS float64 `json:"held_s"`
	// LevelAS, LevelBS and LevelCS split CreditedS by evidence level
	// (DESIGN 6.4).
	LevelAS float64 `json:"level_a_s"`
	LevelBS float64 `json:"level_b_s"`
	LevelCS float64 `json:"level_c_s"`
	// The four bands split CreditedS again (DESIGN 6.4, 7.7). The
	// corroborated and witnessed bands are witnessed seconds at level A or
	// B, the offline band is offline seconds at level A or B, and the
	// backfilled band is every level-C second, which reviewers see with the
	// handbook-third cap applied.
	BandCorroboratedS float64 `json:"band_corroborated_s"`
	BandWitnessedS    float64 `json:"band_witnessed_s"`
	BandOfflineS      float64 `json:"band_offline_s"`
	BandBackfilledS   float64 `json:"band_backfilled_s"`
	// ViaTrackS is the credited seconds that rest on `pairtime track`
	// anchors, the ones labeled via_track (DESIGN 7.7).
	ViaTrackS  float64 `json:"via_track_s"`
	AgentOnlyS float64 `json:"agent_only_s"`
	// ExcludedLoopsS is X.reroll + X.rewrite_loop + X.same_error + X.autopilot.
	ExcludedLoopsS float64 `json:"excluded_loops_s"`
	// IdleS is X.absent seconds while an agent session was open.
	IdleS float64 `json:"idle_s"`
	// AILoggedS is the LoggedS rule, via filter included, applied to agent
	// events only: what Hackatime would call "ai coding".
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
		{"offline_s", t.OfflineS}, {"backfilled_s", t.BackfilledS}, {"held_s", t.HeldS},
		{"level_a_s", t.LevelAS}, {"level_b_s", t.LevelBS}, {"level_c_s", t.LevelCS},
		{"band_corroborated_s", t.BandCorroboratedS}, {"band_witnessed_s", t.BandWitnessedS},
		{"band_offline_s", t.BandOfflineS}, {"band_backfilled_s", t.BandBackfilledS},
		{"via_track_s", t.ViaTrackS}, {"agent_only_s", t.AgentOnlyS},
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
	atMost := func(field string, got float64, rule string, limit float64) {
		if got > limit+totalsTolerance {
			c.addf(field, "is %v, more than %s = %v", got, rule, limit)
		}
	}
	identity("witnessed_s", t.WitnessedS, "credited_s - offline_s - backfilled_s", t.CreditedS-t.OfflineS-t.BackfilledS)
	identity("level_a_s", t.LevelAS, "credited_s - level_b_s - level_c_s", t.CreditedS-t.LevelBS-t.LevelCS)
	identity("band_corroborated_s", t.BandCorroboratedS, "credited_s - band_witnessed_s - band_offline_s - band_backfilled_s",
		t.CreditedS-t.BandWitnessedS-t.BandOfflineS-t.BandBackfilledS)
	// DESIGN 6.4: the backfilled band is exactly the level-C seconds; the
	// offline band holds offline seconds and the corroborated and
	// witnessed bands witnessed ones.
	identity("band_backfilled_s", t.BandBackfilledS, "level_c_s", t.LevelCS)
	atMost("band_offline_s", t.BandOfflineS, "offline_s", t.OfflineS)
	atMost("band_witnessed_s", t.BandWitnessedS, "witnessed_s - band_corroborated_s", t.WitnessedS-t.BandCorroboratedS)
	atMost("via_track_s", t.ViaTrackS, "credited_s", t.CreditedS)
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
