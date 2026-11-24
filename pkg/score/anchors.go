// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Anchor is a human act that passed its evidence test and opens windows of
// credit around itself (SM 1.3, 2.3), with its evidence level and labels
// (DESIGN 6.4). Times are Unix milliseconds and lengths milliseconds.
//
// Its lead is [T-LeadMS, T): the think part first, which needs presence
// only, then the last TypingMS, the typing part, which needs key presses
// where the OS reports them (SM 2.3). A typed command also credits its own
// run, [T, T+WatchMS). Its trail is [TrailFrom, TrailFrom+TrailMS).
// Realizing the windows in present, focused seconds is WP-SC2's.
//
// An anchor that rests on an agent app's message (SM 2.2 rule 9) has its
// lead end at the app's stamp, SentAt, instead (leadEnd). A relayed
// anchor stands there, so its T is SentAt; a prompt that rules 6 and 7
// made typed at the stamp keeps its own T, a second or two later.
type Anchor struct {
	// ID is the event_id of the record behind the act.
	ID         string           `json:"anchor_id"`
	Kind       event.AnchorKind `json:"kind"`
	T          int64            `json:"t"`
	ProjectKey string           `json:"project_key"`
	// SessionKey and TurnKey are set for acts inside a harness session.
	SessionKey string `json:"session_key,omitempty"`
	TurnKey    string `json:"turn_key,omitempty"`
	// Origin is set for prompts and queued messages, and PromptClass for
	// those and `pairtime track` anchors, whose gate takes the place of an
	// origin (DESIGN 5.4).
	Origin      event.Origin      `json:"origin,omitempty"`
	PromptClass event.PromptClass `json:"prompt_class,omitempty"`
	Reason      event.Reason      `json:"reason"`
	LeadMS      int64             `json:"lead_ms"`
	TypingMS    int64             `json:"typing_ms,omitempty"`
	// TypingNeedsKeys is set when the typing part is realized only in
	// slots with key presses even where the OS reports none, as for a
	// `pairtime track` anchor (DESIGN 5.4).
	TypingNeedsKeys bool  `json:"typing_needs_keys,omitempty"`
	WatchMS         int64 `json:"watch_ms,omitempty"`
	TrailFrom       int64 `json:"trail_from"`
	TrailMS         int64 `json:"trail_ms"`
	// Via is how the record behind the act arrived; with presence and
	// keyboard evidence it sets the level.
	Via   event.Via      `json:"via"`
	Level event.Evidence `json:"level"`
	// Corroborated is set when a second code path saw the same act: a
	// hook and a transcript line, or a hook and an OTel event (DESIGN
	// 6.4).
	Corroborated bool          `json:"corroborated,omitempty"`
	Labels       []event.Label `json:"labels,omitempty"`
	// SentAt is the stamp of the agent app's message an anchor rests on
	// (SM 2.2 rule 9), when a relay.message witnessed its prompt: when the
	// app took the message from the person, and where the lead ends. A
	// relayed anchor's T is SentAt.
	SentAt int64 `json:"sent_at,omitempty"`
	// Prompt is the event_id of the prompt.submit a relayed anchor's
	// message paired with, whose turn it opened; the anchor's own ID is
	// the relay.message's. Relay is the event_id of the relay.message that
	// witnessed a prompt rules 6 and 7 made typed at its stamp.
	Prompt string `json:"prompt,omitempty"`
	Relay  string `json:"relay,omitempty"`
	// decided is when the anchor is decided: the latest t of the records it
	// goes by, and no earlier than T. A typed command's record is written
	// when the command ends, and a permission's when the request pairs with
	// the next tool event, both after T. The anchor is final, and can
	// change what was derived, from there (rules.reach).
	decided int64
	// thread is a relayed anchor's thread key, and paired when its message
	// paired: the later of its relay record's t and its prompt's (SM 2.4,
	// the reading a relayed anchor vouches for).
	thread string
	paired int64
}

// from returns the earliest time the anchor can credit, qualify a drain or
// end a returned run: rules.reach before it is decided. A pairing reaches
// back from the app's stamp, not from the later of its two records (SM
// 2.3), so an anchor that rests on one counts reach from SentAt, which is
// never later than when it is decided.
func (a *Anchor) from(r rules) int64 {
	if a.SentAt != 0 {
		return a.SentAt - r.reach
	}
	return a.decided - r.reach
}

// leadEnd returns where the anchor's lead ends: at the app's stamp for an
// anchor that rests on an agent app's message, else at T (SM 2.2, 2.3).
func (a *Anchor) leadEnd() int64 {
	if a.SentAt != 0 {
		return a.SentAt
	}
	return a.T
}

// relayed reports whether the anchor is a relayed message's (SM 2.2 rule
// 9). It credits its own lead and its own session's reading, and is no
// human anchor for anything else: not for another session's reading or
// its level, not for a returned run, not for a lookup's brackets (SM
// 2.3).
func (a *Anchor) relayed() bool { return a.Origin == event.OriginRelayed }

// Event returns the anchor as pkg/event's derived record.
func (a Anchor) Event() event.Anchor {
	return event.Anchor{AnchorID: a.ID, Kind: a.Kind, T: a.T, ProjectKey: a.ProjectKey,
		LeadS: float64(a.LeadMS) / 1000, TrailS: float64(a.TrailMS) / 1000, Via: a.Via,
		Corroborated: a.Corroborated, Labels: slices.Clone(a.Labels)}
}

func anchorCmp(a, b Anchor) int {
	if c := cmp.Compare(a.T, b.T); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// add keeps an anchor that belongs to a project. One with no project can't
// be credited: its windows are realized only on its project (SM 2.5).
// decided is the latest t of the records behind the anchor.
func (d *deriver) add(a Anchor, decided int64) bool {
	if a.ProjectKey == "" {
		return false
	}
	a.decided = max(a.T, decided)
	d.anchors = append(d.anchors, a)
	return true
}

// latest returns the latest t of the records at the given indexes.
func (d *deriver) latest(pos []int) int64 {
	var t int64
	for _, i := range pos {
		t = max(t, d.recs[i].T)
	}
	return t
}

// promptKind returns the anchor kind and reason of a person's prompt of
// class c (SM 2.3).
func promptKind(c event.PromptClass) (event.AnchorKind, event.Reason) {
	switch c {
	case event.ClassAnswer:
		return event.AnchorAnswer, event.ReasonAnswer
	case event.ClassLowEffort:
		return event.AnchorGoAhead, event.ReasonGoAhead
	}
	return event.AnchorSubstantivePrompt, event.ReasonPrompt
}

// promptAnchors makes an anchor of every prompt a person submitted: SM 2.2
// origins human_typed and human_pasted, and relayed (relayedAnchor). A
// prompt with no input before it opens nothing (SM 2.2 rule 8); while its
// turn runs and no other turn of its project does, the project's
// uncredited seconds are X.no_input (SM 5.2, credit.go).
//
// A prompt an agent app's message witnesses and rules 6 and 7 made typed at
// the app's stamp is anchored as any typed prompt, at its usual level, at
// its own t (SM 2.2): its lead and its evidence window end at the stamp,
// and it is decided once the relay record is in too.
func (d *deriver) promptAnchors() {
	for _, tb := range d.turns {
		t := tb.turn
		if tb.primary < 0 {
			continue
		}
		if t.Origin == event.OriginRelayed {
			d.relayedAnchor(tb)
			continue
		}
		if !t.Origin.Human() {
			continue
		}
		e := d.recs[tb.primary]
		think, typing := d.leadParts(t.PromptClass, e.Via, t.TypedChars)
		a := Anchor{ID: e.EventID, T: e.T, ProjectKey: t.ProjectKey, SessionKey: t.SessionKey, TurnKey: t.TurnKey,
			Origin: t.Origin, PromptClass: t.PromptClass, LeadMS: think + typing, TypingMS: typing,
			TrailFrom: e.T, Via: e.Via, Corroborated: d.corroborated(tb.prompts)}
		a.Kind, a.Reason = promptKind(t.PromptClass)
		end, decided := e.T, d.latest(tb.prompts)
		if w := tb.witness; w != nil {
			a.SentAt, a.Relay = w.d.SentAt, d.recs[w.pos].EventID
			end, decided = w.d.SentAt, max(decided, d.recs[w.pos].T)
		}
		d.level(&a, end-d.r.keysPre, end, d.sessionHost(tb.sess, tb.primary))
		d.add(a, decided)
	}
}

// relayedAnchor makes the anchor of a relayed prompt (SM 2.2 rule 9, 2.3):
// the relay.message that witnessed it, standing at the app's stamp. Its
// lead is typing time only, whatever its class: the typed length origin
// took from the app's own text, over COMPOSE_CPS and at most
// COMPOSE_MAX_S, cut to the time since the thread's previous relay.message
// of declared none, paired or not, and ending at the stamp. It has no think
// part and no trail, and its class gives its kind and reason: P, ANS or GO.
// It is level C with via_relay, and vm on a VM, whatever backs it, and is
// never corroborated (DESIGN 6.4). It is decided once both its records are
// in, but reaches back from the stamp (Anchor.from).
//
// Realizing its lead, in a slot of any presence but one the machine slept
// through (Slot.slept) and with no focus or keyboard test, after every
// other candidate (SM 2.3, 2.7), is the relayed stage of the credit pass,
// which windows leaves it to.
func (d *deriver) relayedAnchor(tb *turnBuild) {
	t, w := tb.turn, tb.witness
	re, pe := d.recs[w.pos], d.recs[w.prompt]
	sent := w.d.SentAt
	typing := d.r.typingMS(t.TypedChars)
	if prev, ok := d.prevMessage(w); ok {
		typing = min(typing, sent-prev)
	}
	a := Anchor{ID: re.EventID, T: sent, ProjectKey: t.ProjectKey, SessionKey: t.SessionKey, TurnKey: t.TurnKey,
		Origin: t.Origin, PromptClass: t.PromptClass, LeadMS: typing, TypingMS: typing, TrailFrom: sent, Via: re.Via,
		Level: event.EvidenceC, SentAt: sent, Prompt: pe.EventID, thread: w.d.ThreadKey, paired: max(re.T, pe.T)}
	a.Kind, a.Reason = promptKind(t.PromptClass)
	if d.dev.VM {
		a.Labels = append(a.Labels, event.LabelVM)
	}
	a.Labels = append(a.Labels, event.LabelViaRelay)
	d.add(a, max(re.T, d.latest(tb.prompts)))
}

// corroborated reports whether the records at the given indexes, all of one
// act, came through a hook and also through a log reader or OTel (DESIGN
// 6.4).
func (d *deriver) corroborated(pos []int) bool {
	var hook, other bool
	for _, i := range pos {
		switch d.recs[i].Via {
		case event.ViaHook:
			hook = true
		case event.ViaLog, event.ViaOTel:
			other = true
		}
	}
	return hook && other
}

// sessionHost returns a test of whether, at a time, the window in focus
// hosted session s (SM 2.2 rule 6), for the record at pos.
func (d *deriver) sessionHost(s *session, pos int) func(int64) bool {
	return func(t int64) bool { return d.hostedBy(d.focus.at(t), s, pos) }
}

// projectHost returns a test of whether, at a time, the window in focus was
// on project p, which for an editor with no resolved project is its own
// heartbeat's (SM 3.2).
func (d *deriver) projectHost(p string, editor bool) func(int64) bool {
	return func(t int64) bool {
		f := d.focus.at(t)
		if editor && f.class != event.AppEditor {
			return false
		}
		return f.project == p || (editor && f.project == "")
	}
}

// slotWindow returns the first and last millisecond of the slot holding t,
// the evidence window of acts whose test is "input in its slot".
func slotWindow(t int64) (int64, int64) {
	a := floorSlot(t)
	return a, a + event.SlotMS - 1
}

// actKey identifies one act that several records may report.
type actKey struct {
	kind     event.Kind
	sess     string
	key      string
	from, to int64
	// chars is a queued message's len_chars, which its copies share, and n
	// tells apart two queued messages of one turn, length and millisecond
	// that one code path reported.
	chars, n int
}

// eventAnchors makes anchors of the human acts other than prompts (SM 2.3):
// queued messages, permission answers, interrupts, typed edits, editor
// navigation, typed commands and human commits. The first three open
// nothing in a session an agent app's server started (harnessAnchor).
func (d *deriver) eventAnchors() {
	// A harness act reported by several code paths is one act. It goes by
	// its most trusted record, the first in record order on a tie.
	groups := map[actKey][]int{}
	var order []actKey
	// perms holds each session's permission groups. The PermissionRequest
	// hook stamps arrival times and Claude's tool.blocked_on_user span its
	// own clock (SM 1.2), so two reports of one answer differ by a few
	// milliseconds. Records whose request and answer times are both within
	// PA_MIN_LATENCY_MS of a group's are that answer: two answers can't be
	// that close, since a person takes at least that long over each (SM
	// 2.3).
	perms := map[string][]actKey{}
	// queues holds each session's queued-message groups. A hook and a log
	// reader report a queued message apart, the hook as the person queues
	// it and the transcript's enqueue a few milliseconds later: a log
	// reader's record and another path's, of the same turn and len_chars
	// within TRACK_LOG_MATCH_S, are that message, paired one to one, oldest
	// first (E-cc-queued-turn). So two messages one hook reported stay two.
	queues := map[string][]actKey{}
	for i, e := range d.recs {
		var k actKey
		switch p := e.Data.(type) {
		case *event.AgentQueue:
			k = actKey{kind: event.KindAgentQueue, sess: e.SessionKey, key: p.TurnKey, from: e.T, chars: p.LenChars}
		case *event.AgentPermission:
			k = actKey{kind: event.KindAgentPermission, sess: e.SessionKey, from: p.RequestedAt, to: p.AnsweredAt}
		case *event.AgentInterrupt:
			k = actKey{kind: event.KindAgentInterrupt, sess: e.SessionKey, key: p.TurnKey}
		case *event.EditorActivity, *event.ShellCommand, *event.GitChange:
			d.otherAnchor(i, e)
			continue
		default:
			continue
		}
		s := d.sessions[e.SessionKey]
		if s == nil || (s.native && (e.Via == event.ViaBridge || e.Via == event.ViaBridgeUnverified)) {
			// The bridge is a cross-check wherever the harness's own
			// paths cover the session (DESIGN 5.7).
			continue
		}
		if k.kind == event.KindAgentPermission {
			for _, g := range perms[k.sess] {
				if absDiff(g.from, k.from) < d.r.paMinLatencyMS && absDiff(g.to, k.to) < d.r.paMinLatencyMS {
					k = g
					break
				}
			}
		}
		if k.kind == event.KindAgentQueue {
			matched := false
			for _, g := range queues[k.sess] {
				// One to one: a group of one copy takes the message's other,
				// when one of the two is a log reader's.
				if g.key == k.key && g.chars == k.chars && absDiff(g.from, k.from) <= d.r.trackLogMatch &&
					len(groups[g]) == 1 && (e.Via == event.ViaLog) != (d.recs[groups[g][0]].Via == event.ViaLog) {
					k, matched = g, true
					break
				}
			}
			for !matched && groups[k] != nil {
				k.n++ // a message of the same moment the same path reported
			}
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
			switch k.kind {
			case event.KindAgentPermission:
				perms[k.sess] = append(perms[k.sess], k)
			case event.KindAgentQueue:
				queues[k.sess] = append(queues[k.sess], k)
			}
		}
		groups[k] = append(groups[k], i)
	}
	for _, k := range order {
		pos := groups[k]
		best := pos[0]
		for _, i := range pos[1:] {
			if viaRank(d.recs[i].Via) < viaRank(d.recs[best].Via) {
				best = i
			}
		}
		d.harnessAnchor(best, pos)
	}
}

// absDiff returns |a - b|.
func absDiff(a, b int64) int64 {
	if a < b {
		return b - a
	}
	return a - b
}

// harnessAnchor makes the anchor of a harness act, if it passes its
// evidence test (SM 2.3). best is the record it goes by and pos all of its
// records.
//
// A session whose last session.start before the act names an agent app's
// own server, parent_class agent_app, opens none of these anchors (SM 2.2,
// What isn't a message; SM 2.3). An approval or a stop given in the app's
// interface reaches the harness without a message, and so does a message
// the app steers into the running turn, which Claude Code can report as a
// queued one. The app's window hosts every session its server drives, so
// input there would otherwise make any of these a person's act at this
// computer, whoever gave it and wherever. A permission answered too fast
// is still flagged there, since a script answered it either way.
func (d *deriver) harnessAnchor(best int, pos []int) {
	e := d.recs[best]
	s := d.sessions[e.SessionKey]
	st := s.startBefore(best)
	app := st != nil && st.ParentClass == event.ParentAgentApp
	host := d.sessionHost(s, best)
	project := e.ProjectKey
	if project == "" {
		project = s.project
	}
	a := Anchor{ID: e.EventID, T: e.T, ProjectKey: project, SessionKey: s.key, TrailFrom: e.T, Via: e.Via,
		Corroborated: d.corroborated(pos)}
	from, to := slotWindow(e.T)
	switch p := e.Data.(type) {
	case *event.AgentQueue:
		// Input in its slot and focus on the host window; then as a prompt
		// of its class. Its record carries a length, not the derived
		// low-effort test, so a short message is a go-ahead.
		if app || !d.pres.slotInput(e.T, false) || !d.hostedBy(d.focus.before(best), s, best) {
			return
		}
		typed := count(p.LenChars)
		class := event.ClassLowEffort
		switch {
		case s.askedBefore(best):
			class = event.ClassAnswer
		case typed >= d.r.substantive:
			class = event.ClassSubstantive
		}
		// SM 2.2 rule 7, as for a prompt of its class (SM 2.3): more than
		// the lead's input slots could have typed at TYPING_MAX_CPS is
		// pasted, and the typing part is computed from what they could.
		// agent.queue carries no pasted_chars, so this is its only bound.
		think, typing := d.leadParts(class, e.Via, typed)
		a.Origin = event.OriginHumanTyped
		active := d.pres.inputSlots(e.T-think-typing, e.T) * event.SlotMS / 1000
		if most := d.r.typingMaxCPS * active; typed > most {
			a.Origin = event.OriginHumanPasted
			_, typing = d.leadParts(class, e.Via, most)
		}
		a.Kind, a.Reason, a.TurnKey = event.AnchorQueuedMessage, event.ReasonQueued, p.TurnKey
		a.PromptClass = class
		a.LeadMS, a.TypingMS = think+typing, typing
	case *event.AgentPermission:
		// decided_by user, an answer no faster than PA_MIN_LATENCY_MS, and
		// presence when it came. The lead is the wait, up to PA_MAX_S.
		if p.DecidedBy != event.DecidedByUser {
			return
		}
		wait := p.AnsweredAt - p.RequestedAt
		if wait < d.r.paMinLatencyMS {
			d.flag(FlagPermissionTooFast, e)
			return
		}
		if app || !d.pres.presentFor(p.AnsweredAt) {
			return
		}
		a.Kind, a.Reason = event.AnchorPermissionAnswer, event.ReasonPermission
		a.T, a.TrailFrom = p.AnsweredAt, p.AnsweredAt
		a.LeadMS, a.TrailMS = min(wait, d.r.paMax), d.r.paTrail
		from, to = slotWindow(p.AnsweredAt)
	case *event.AgentInterrupt:
		if app || !d.pres.presentFor(e.T) {
			return
		}
		a.Kind, a.Reason, a.TurnKey = event.AnchorInterrupt, event.ReasonInterrupt, p.TurnKey
		a.LeadMS, a.TrailMS = d.r.intLead, d.r.intTrail
	default:
		return
	}
	d.level(&a, from, to, host)
	d.add(a, d.latest(pos))
}

// otherAnchor makes the anchor of an editor, shell or git act, if it
// passes its evidence test (SM 2.3).
func (d *deriver) otherAnchor(i int, e event.Event) {
	if e.ProjectKey == "" {
		return
	}
	a := Anchor{ID: e.EventID, T: e.T, ProjectKey: e.ProjectKey, TrailFrom: e.T, Via: e.Via}
	from, to := slotWindow(e.T)
	host := d.projectHost(e.ProjectKey, false)
	switch p := e.Data.(type) {
	case *event.EditorActivity:
		f := d.focus.before(i)
		host = d.projectHost(e.ProjectKey, true)
		switch p.Act {
		case event.ActEdit, event.ActSave:
			// Typed by the person (human, tab or unknown, never the agent
			// or a paste), input in its slot, key presses where the OS
			// reports them, and the editor in focus on the same project:
			// the project of SM 3.2 rule 2's heartbeat when there is one,
			// else the one the focus record names (D-editor-heartbeat-first,
			// D-rule2-sidedoor).
			switch p.EditSource {
			case event.EditSourceHuman, event.EditSourceTab, event.EditSourceUnknown:
			default:
				return
			}
			if !d.pres.slotInput(e.T, d.pres.keysAt(e.T)) || f.class != event.AppEditor {
				return
			}
			if on := d.editorProject(f, i); on != "" && on != e.ProjectKey {
				return
			}
			a.Kind, a.Reason = event.AnchorTypedEdit, event.ReasonEdit
		case event.ActNavigate, event.ActOpen, event.ActDebug, event.ActTask:
			if !d.pres.slotInput(e.T, false) || f.class != event.AppEditor {
				return
			}
			a.Kind, a.Reason = event.AnchorEditorNavigation, event.ReasonNavigate
		default:
			return
		}
		a.LeadMS = d.r.editLead
	case *event.ShellCommand:
		// Typed in an interactive shell, with input just before it. The
		// anchor is when it started; it credits its lead, its run up to
		// CMD_WATCH_MAX_S, and a trail after it ends.
		if !p.Interactive || !d.pres.inputBefore(p.TStart, d.r.keysPre) {
			return
		}
		// A build is TEST, as SM 5.2 and SM 2.3's activity column have it;
		// SM 2.3's reason column names only tests (doc.go's readings).
		a.Kind, a.Reason = event.AnchorTypedCommand, event.ReasonCommand
		if p.CmdClass == event.CmdTest || p.CmdClass == event.CmdBuild {
			a.Reason = event.ReasonTest
		}
		a.T, a.TrailFrom = p.TStart, p.TEnd
		a.LeadMS, a.WatchMS, a.TrailMS = d.r.cmdLead, min(p.TEnd-p.TStart, d.r.cmdWatchMax), d.r.cmdTrail
		from, to = p.TStart-d.r.keysPre, p.TStart
	case *event.GitChange:
		if p.Op != event.GitCommit || p.Actor != event.ActorHuman || !d.pres.slotInput(e.T, false) {
			return
		}
		a.Kind, a.Reason, a.LeadMS = event.AnchorHumanCommit, event.ReasonCommit, d.r.commitLead
	default:
		return
	}
	d.level(&a, from, to, host)
	d.add(a, e.T)
}

// levelVia reports whether an anchor that arrived via v can be level A or
// B: the harness's own hooks and logs, the shell integration, the .git
// watcher, and editor heartbeats that passed the peer-process check
// (DESIGN 6.4).
func levelVia(v event.Via) bool {
	switch v {
	case event.ViaHook, event.ViaLog, event.ViaShell, event.ViaGit, event.ViaEditor:
		return true
	}
	return false
}

// sideDoorLabel returns the label an anchor that arrived via v carries, or
// "" (DESIGN 4.1 E-6, 6.4).
func sideDoorLabel(v event.Via) event.Label {
	switch v {
	case event.ViaTrack:
		return event.LabelViaTrack
	case event.ViaPlugin:
		return event.LabelViaPlugin
	case event.ViaBridge, event.ViaBridgeUnverified:
		return event.LabelViaBridge
	}
	return ""
}

// level sets an anchor's evidence level and labels (DESIGN 6.4). from and
// to bound its evidence window: the KEYS_PRE_S before a prompt or command,
// else its slot. A prompt's window leaves out the INPUT_RELEASE_GRACE_MS
// after it, which only rules 6 and 7's input test reads. host says whether
// the window in focus at a time is the one the act happened in.
//
//   - A: a hook, log, shell, git or editor anchor with key presses in its
//     evidence window, or a side-door anchor with key presses in the host
//     window in the KEYS_PRE_S before it.
//   - B: a hook, log, shell, git or editor anchor whose evidence is any
//     input, without key presses: on a machine whose OS reports no key
//     presses apart from other input, or where the input wasn't a key.
//   - C: everything else: side doors without key presses, editor
//     heartbeats that failed the peer-process check, and acts before the
//     presence sampler ran (SM 2.8).
//
// On a VM, key presses from the typing module don't make an anchor level A
// (DESIGN 6.2 D17). Witnessing, which can make a minute level C later, is
// decided when buckets are uploaded (WP-SC4).
func (d *deriver) level(a *Anchor, from, to int64, host func(int64) bool) {
	keys := false
	switch {
	case d.pres.unsampled(a.T):
		a.Level = event.EvidenceC
	case sideDoor(a.Via):
		keys = d.pres.keysAt(a.T) && d.pres.inputWhere(a.T-d.r.keysPre, a.T, true, host)
		a.Level = event.EvidenceC
		if keys && !d.vmTyping() {
			a.Level = event.EvidenceA
		}
	case levelVia(a.Via):
		keys = d.pres.keysAt(to) && d.pres.inputIn(from, to, true)
		a.Level = event.EvidenceB
		if keys && !d.vmTyping() {
			a.Level = event.EvidenceA
		}
	default:
		a.Level = event.EvidenceC
	}
	a.Labels = nil
	if l := sideDoorLabel(a.Via); l != "" {
		a.Labels = append(a.Labels, l)
	}
	if keys && d.dev.TypingModule {
		a.Labels = append(a.Labels, event.LabelTypingModule)
	}
	if d.dev.VM {
		a.Labels = append(a.Labels, event.LabelVM)
	}
}

// vmTyping reports whether key presses come from the typing module on a VM,
// where they can't make an anchor level A (DESIGN 6.2 D17).
func (d *deriver) vmTyping() bool { return d.dev.VM && d.dev.TypingModule }
