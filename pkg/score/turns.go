// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Turn is a prompt and the agent's work on it, or a turn the agent went on
// to without a new prompt (SM 1.3, 2.2).
type Turn struct {
	SessionKey string        `json:"session_key"`
	TurnKey    string        `json:"turn_key"`
	Harness    event.Harness `json:"harness"`
	ProjectKey string        `json:"project_key,omitempty"`
	TSubmit    int64         `json:"t_submit"`
	// TStop is 0 while the turn is running.
	TStop int64 `json:"t_stop,omitempty"`
	// RunUntil is when the agent stopped working on the turn: at TStop, or
	// for a turn with no agent.stop at its last agent record, and in either
	// case no later than the session's next prompt. A turn with no agent
	// record after its prompt never runs, and its RunUntil is TSubmit. This
	// is the rule docs/errata/2026-11-19-turns-without-a-stop.md proposes;
	// it decides SUP against READ (SM 2.4) and agent_running_s (SM 5.1).
	RunUntil int64        `json:"run_until"`
	Origin   event.Origin `json:"origin"`
	// PromptClass is set for turns a person started.
	PromptClass event.PromptClass `json:"prompt_class,omitempty"`
	// Prompt is the event_id of the prompt.submit that started the turn,
	// and Via how it arrived. A turn a stop hook continued has neither.
	Prompt string    `json:"prompt,omitempty"`
	Via    event.Via `json:"via,omitempty"`
	// TypedChars is the typed length the lead's typing part is computed
	// from: typed_len_est, cut for a human_pasted prompt to what its input
	// slots could have typed (SM 2.2 rule 7).
	TypedChars int64 `json:"typed_chars,omitempty"`
	// WordsOut is the prose of the turn's agent.output records, and
	// DiffLines the lines its edit tools added and removed.
	WordsOut  int `json:"words_out"`
	DiffLines int `json:"diff_lines"`
}

// Event returns the turn as pkg/event's derived record. Re-roll chains are
// WP-SC6's, so every human turn is attempt 1 until then.
func (t Turn) Event() event.Turn {
	e := event.Turn{SessionKey: t.SessionKey, TurnKey: t.TurnKey, TSubmit: t.TSubmit, TStop: t.TStop,
		Origin: t.Origin, PromptClass: t.PromptClass, WordsOut: t.WordsOut, DiffLines: t.DiffLines}
	if t.Origin.Human() {
		e.Attempt = 1
	}
	return e
}

// decided is when the turn stops changing: when it stops.
func (t Turn) decided() int64 {
	if t.TStop == 0 {
		return never
	}
	return t.TStop
}

func turnCmp(a, b Turn) int {
	if c := cmp.Compare(a.TSubmit, b.TSubmit); c != 0 {
		return c
	}
	if c := strings.Compare(a.SessionKey, b.SessionKey); c != 0 {
		return c
	}
	return strings.Compare(a.TurnKey, b.TurnKey)
}

// session is what the records say about one harness session.
type session struct {
	key     string
	harness event.Harness
	project string
	// starts are its session.start records, in record order.
	starts []posRec[*event.SessionStart]
	// native is set when any of its records came through a hook or a log
	// reader, the code paths the harness itself feeds (DESIGN 5.2).
	native bool
	// bridged is set when any of its records came through the WakaTime
	// bridge, which reads them from an editor's wakatime-cli (DESIGN 5.7).
	bridged bool
	// nativePrompts are the times of its prompts that came that way.
	nativePrompts []int64
	// outputs and stops are its agent.output and agent.stop records, for
	// the answer class (SM 2.2).
	outputs []posRec[*event.AgentOutput]
	stops   []posRec[*event.AgentStop]
	// lastStop is the latest agent.stop seen so far while turns are
	// assembled, where the next continuation turn starts.
	lastStop int64
	turns    []*turnBuild
}

// posRec is a record's payload with its index in the records and its time.
type posRec[T any] struct {
	pos int
	t   int64
	d   T
}

// startBefore returns the session's last session.start before the record
// at pos, or nil.
func (s *session) startBefore(pos int) *event.SessionStart {
	i := sort.Search(len(s.starts), func(i int) bool { return s.starts[i].pos >= pos })
	if i == 0 {
		return nil
	}
	return s.starts[i-1].d
}

// askedBefore reports whether the agent's last output before the record at
// pos asked a question, or its last turn stopped on one (SM 2.2, the
// answer class).
func (s *session) askedBefore(pos int) bool {
	i := sort.Search(len(s.outputs), func(i int) bool { return s.outputs[i].pos >= pos })
	if i > 0 && s.outputs[i-1].d.AsksQuestion {
		return true
	}
	j := sort.Search(len(s.stops), func(i int) bool { return s.stops[i].pos >= pos })
	return j > 0 && s.stops[j-1].d.StopReason == event.StopQuestion
}

// ide reports whether the session runs in an IDE, so that focus on the
// editor window counts as focus on it (SM 2.2 rule 6).
func (s *session) ide(pos int) bool {
	st := s.startBefore(pos)
	return st != nil && (st.Entrypoint == event.EntrypointIDE || st.ParentClass == event.ParentIDE)
}

// nativePromptNear reports whether a prompt that came through a hook or a
// log reader is within d of t.
func (s *session) nativePromptNear(t, d int64) bool {
	i := sort.Search(len(s.nativePrompts), func(i int) bool { return s.nativePrompts[i] >= t-d })
	return i < len(s.nativePrompts) && s.nativePrompts[i] <= t+d
}

// turnBuild is a turn while it is assembled.
type turnBuild struct {
	sess *session
	key  string
	// prompts are the indexes of the prompt.submit records with this
	// session and turn key: one per code path that saw the prompt.
	prompts []int
	// primary is the index of the prompt.submit the turn goes by, or -1
	// for a continuation.
	primary   int
	tSubmit   int64
	tStop     int64
	stopHook  bool
	words     int
	diffLines int
	// lastAgent is the time of the turn's last agent.output, agent.tool or
	// agent.stop, or 0.
	lastAgent int64
	turn      Turn
}

// running reports whether the agent was working on the turn at t.
func (tb *turnBuild) running(t int64) bool { return tb.turn.TSubmit <= t && t < tb.turn.RunUntil }

// viaRank orders the ways a record can arrive, most trusted first. When
// several records saw the same act, the first in this order is the one the
// act goes by.
func viaRank(v event.Via) int {
	switch v {
	case event.ViaHook:
		return 0
	case event.ViaLog:
		return 1
	case event.ViaOTel:
		return 2
	case event.ViaPlugin:
		return 8
	case event.ViaBridge:
		return 9
	case event.ViaBridgeUnverified:
		return 10
	case event.ViaTrack:
		return 11
	}
	return 3
}

// nativeVia reports whether v is a path the harness itself feeds: its
// hooks or its own logs (DESIGN 5.2).
func nativeVia(v event.Via) bool { return v == event.ViaHook || v == event.ViaLog }

// sideDoor reports whether v is one of the paths any process on the machine
// can post to (DESIGN 5.2). editor_unverified is level C too but isn't a
// labeled side door.
func sideDoor(v event.Via) bool {
	switch v {
	case event.ViaTrack, event.ViaPlugin, event.ViaBridge, event.ViaBridgeUnverified:
		return true
	}
	return false
}

// harnessRecord returns the session key of a record that belongs to a
// harness session, or "".
func harnessRecord(e event.Event) string {
	switch e.Data.(type) {
	case *event.SessionStart, *event.SessionEnd, *event.PromptSubmit, *event.AgentOutput, *event.AgentTool,
		*event.AgentPermission, *event.AgentInterrupt, *event.AgentQueue, *event.AgentStop, *event.AgentSubagent,
		*event.CLITrack:
		if e.SessionKey != event.NoSession {
			return e.SessionKey
		}
	}
	return ""
}

// buildSessions collects what the records say about each session.
func (d *deriver) buildSessions() {
	d.sessions = map[string]*session{}
	for i, e := range d.recs {
		key := harnessRecord(e)
		if key == "" {
			continue
		}
		s := d.sessions[key]
		if s == nil {
			s = &session{key: key, harness: e.Harness}
			d.sessions[key] = s
		}
		if s.project == "" {
			s.project = e.ProjectKey
		}
		if nativeVia(e.Via) {
			s.native = true
		}
		if e.Via == event.ViaBridge || e.Via == event.ViaBridgeUnverified {
			s.bridged = true
		}
		switch p := e.Data.(type) {
		case *event.SessionStart:
			s.starts = append(s.starts, posRec[*event.SessionStart]{i, e.T, p})
		case *event.AgentOutput:
			s.outputs = append(s.outputs, posRec[*event.AgentOutput]{i, e.T, p})
		case *event.AgentStop:
			s.stops = append(s.stops, posRec[*event.AgentStop]{i, e.T, p})
		}
	}
}

// buildTurns assembles turns from prompt.submit, agent.output, agent.stop
// and agent.tool records (SM 1.3), then gives each its origin and class
// (SM 2.2).
func (d *deriver) buildTurns() {
	byKey := map[[2]string]*turnBuild{}
	var groups []*turnBuild
	for i, e := range d.recs {
		p, ok := e.Data.(*event.PromptSubmit)
		if !ok {
			continue
		}
		k := [2]string{e.SessionKey, p.TurnKey}
		if d.sessions[e.SessionKey] == nil {
			continue
		}
		tb := byKey[k]
		if tb == nil {
			tb = &turnBuild{sess: d.sessions[e.SessionKey], key: p.TurnKey}
			byKey[k] = tb
			groups = append(groups, tb)
		}
		tb.prompts = append(tb.prompts, i)
	}
	// The turn goes by its most trusted prompt record. A prompt that only
	// a side door saw is a cross-check, not a turn, when the harness's own
	// paths saw it too: for the bridge, whenever a hook or log reader
	// covers the session (DESIGN 5.7); for a plugin, when they hold a
	// prompt within TRACK_LOG_MATCH_S of it (DESIGN 5.2).
	for _, tb := range groups {
		tb.primary = tb.prompts[0]
		for _, i := range tb.prompts[1:] {
			if viaRank(d.recs[i].Via) < viaRank(d.recs[tb.primary].Via) {
				tb.primary = i
			}
		}
		if e := d.recs[tb.primary]; nativeVia(e.Via) {
			tb.sess.nativePrompts = append(tb.sess.nativePrompts, e.T)
		}
	}
	for _, s := range d.sessions {
		slices.Sort(s.nativePrompts)
	}
	for _, tb := range groups {
		e := d.recs[tb.primary]
		switch {
		case (e.Via == event.ViaBridge || e.Via == event.ViaBridgeUnverified) && tb.sess.native:
			continue
		case sideDoor(e.Via) && tb.sess.nativePromptNear(e.T, d.r.trackLogMatch):
			continue
		}
		tb.tSubmit = e.T
		tb.sess.turns = append(tb.sess.turns, tb)
	}
	for _, s := range d.sessions {
		slices.SortStableFunc(s.turns, func(a, b *turnBuild) int { return cmp.Compare(a.primary, b.primary) })
	}

	// Outputs and stops, in record order. A turn key no prompt carries is a
	// turn a stop hook continued: it starts where the session's previous
	// turn stopped (testdata/timelines/README.md, Turns).
	byKey = map[[2]string]*turnBuild{}
	for _, s := range d.sessions {
		for _, tb := range s.turns {
			byKey[[2]string{s.key, tb.key}] = tb
		}
	}
	for _, e := range d.recs {
		var key string
		switch p := e.Data.(type) {
		case *event.AgentOutput:
			key = p.TurnKey
		case *event.AgentStop:
			key = p.TurnKey
		default:
			continue
		}
		s := d.sessions[e.SessionKey]
		if s == nil || (s.native && sideDoor(e.Via)) {
			continue
		}
		tb := byKey[[2]string{s.key, key}]
		if tb == nil {
			tb = &turnBuild{sess: s, key: key, primary: -1, tSubmit: e.T}
			if s.lastStop > 0 && s.lastStop <= e.T {
				tb.tSubmit = s.lastStop
			}
			byKey[[2]string{s.key, key}] = tb
			s.turns = append(s.turns, tb)
		}
		switch p := e.Data.(type) {
		case *event.AgentOutput:
			tb.words += p.Words
		case *event.AgentStop:
			if tb.tStop == 0 {
				tb.tStop, tb.stopHook = e.T, p.StopHookActive
			}
			s.lastStop = max(s.lastStop, e.T)
		}
		tb.lastAgent = max(tb.lastAgent, e.T)
	}
	for _, s := range d.sessions {
		slices.SortStableFunc(s.turns, func(a, b *turnBuild) int { return cmp.Compare(a.tSubmit, b.tSubmit) })
	}
	// Tools carry no turn key; they go to the turn running in their session
	// at the time, and an edit tool's lines with them.
	for _, e := range d.recs {
		p, ok := e.Data.(*event.AgentTool)
		if !ok {
			continue
		}
		s := d.sessions[e.SessionKey]
		if s == nil || (s.native && sideDoor(e.Via)) {
			continue
		}
		if tb := s.runningAt(e.T); tb != nil {
			if isEdit(p) {
				tb.diffLines += p.LinesAdded + p.LinesRemoved
			}
			tb.lastAgent = max(tb.lastAgent, e.T)
		}
	}
	for _, s := range d.sessions {
		for k, tb := range s.turns {
			d.finishTurn(tb)
			tb.turn.RunUntil = tb.runUntil(s.turns[k+1:])
			d.turns = append(d.turns, tb)
		}
	}
	slices.SortFunc(d.turns, func(a, b *turnBuild) int { return turnCmp(a.turn, b.turn) })
}

// isEdit reports whether a tool call changed files, so that its lines are
// the turn's diff (SM 2.4).
func isEdit(p *event.AgentTool) bool {
	return p.ToolClass == event.ToolEdit || p.ToolClass == event.ToolWrite
}

// runUntil is the end of the turn's run (Turn.RunUntil). later are the
// session's turns after it, in submit order.
func (tb *turnBuild) runUntil(later []*turnBuild) int64 {
	end := tb.tStop
	if end == 0 {
		end = tb.lastAgent
	}
	for _, n := range later {
		if n.primary >= 0 && n.tSubmit > tb.tSubmit {
			end = min(end, n.tSubmit)
			break
		}
	}
	return max(end, tb.tSubmit)
}

// runningAt returns the turn of s that was running at t, or nil.
func (s *session) runningAt(t int64) *turnBuild {
	var found *turnBuild
	for _, tb := range s.turns {
		if tb.tSubmit > t {
			break
		}
		if tb.tStop == 0 || t < tb.tStop {
			found = tb
		}
	}
	return found
}

// finishTurn gives a turn its origin, class and typed length.
func (d *deriver) finishTurn(tb *turnBuild) {
	tb.turn = Turn{SessionKey: tb.sess.key, TurnKey: tb.key, Harness: tb.sess.harness, ProjectKey: tb.sess.project,
		TSubmit: tb.tSubmit, TStop: tb.tStop, WordsOut: tb.words, DiffLines: tb.diffLines}
	if tb.primary < 0 {
		// SM 2.2 rule 2: the turn went on without a new prompt.
		tb.turn.Origin = event.OriginSelfContinue
		return
	}
	e := d.recs[tb.primary]
	tb.turn.Prompt, tb.turn.Via = e.EventID, e.Via
	if e.ProjectKey != "" {
		tb.turn.ProjectKey = e.ProjectKey
	}
	tb.turn.Origin, tb.turn.PromptClass, tb.turn.TypedChars = d.origin(tb, e)
}

// origin applies SM 2.2's origin rules to a turn's prompt, first match
// wins. Rules 1 to 5 read what the harness declares; 6 to 8 read OS
// evidence. For a person's prompt it also returns the prompt's class and
// the typed length its lead is computed from.
func (d *deriver) origin(tb *turnBuild, e event.Event) (event.Origin, event.PromptClass, int64) {
	p := e.Data.(*event.PromptSubmit)
	st := tb.sess.startBefore(tb.primary)
	switch {
	case p.MatchesPendingCron || p.OriginDeclared == event.DeclaredCron || p.OriginDeclared == event.DeclaredLoop:
		return event.OriginScheduled, "", 0
	case p.OriginDeclared == event.DeclaredGoal || p.OriginDeclared == event.DeclaredFollowup || tb.stopHook:
		return event.OriginSelfContinue, "", 0
	case p.OriginDeclared == event.DeclaredSubagent || p.OriginDeclared == event.DeclaredCrossSession:
		// SM 2.2 names no rule for a message another session sent; like a
		// subagent's, it is one agent prompting another (TP DI 26).
		return event.OriginSubagent, "", 0
	case st != nil && d.headless(st, tb.sess):
		return event.OriginHeadless, "", 0
	case st != nil && st.Entrypoint == event.EntrypointCloud:
		return event.OriginCloud, "", 0
	}
	// Rules 6 to 8: input in the KEYS_PRE_S before the submit, focus on a
	// window hosting the session, and a typed length the input could
	// have produced.
	if !d.pres.inputBefore(e.T, d.r.keysPre) || !d.hostedBy(d.focus.before(tb.primary), tb.sess, tb.primary) {
		return event.OriginNoInput, "", 0
	}
	class := d.classify(tb.sess, tb.primary, p)
	typed := int64(p.TypedLenEst)
	think, typing := d.leadParts(class, e.Via, typed)
	active := d.pres.inputSlots(e.T-think-typing, e.T) * event.SlotMS / 1000
	most := d.r.typingMaxCPS * active
	if typed <= most && p.PastedChars == 0 {
		return event.OriginHumanTyped, class, typed
	}
	return event.OriginHumanPasted, class, min(typed, most)
}

// headless is SM 2.2 rule 4.
func (d *deriver) headless(st *event.SessionStart, s *session) bool {
	automated := false
	switch {
	case st.Entrypoint == event.EntrypointSDK || st.Entrypoint == event.EntrypointExec:
		automated = true
	case st.ParentClass == event.ParentScriptRuntime || st.ParentClass == event.ParentScheduler ||
		st.ParentClass == event.ParentAgent:
		automated = true
	case !st.HasTTY && st.Entrypoint != event.EntrypointIDE && st.Entrypoint != event.EntrypointDesktop &&
		st.ParentClass != event.ParentIDE:
		// Neither a TTY nor an IDE or desktop-app host.
		automated = true
	}
	if !automated {
		return false
	}
	// A person typing `claude -p "..."` by hand: key presses in the
	// KEYS_PRE_S before session.start, from an interactive shell. Where the
	// OS reports no key presses, headless prompts never count.
	switch st.ParentClass {
	case event.ParentTerminalApp, event.ParentMultiplexer, event.ParentRemote:
		for _, sr := range s.starts {
			if sr.d == st && d.pres.keysAt(sr.t) && d.pres.inputIn(sr.t-d.r.keysPre, sr.t, true) {
				return false
			}
		}
	}
	return true
}

// hostedBy reports whether window f hosts session s (SM 2.2 rule 6): its
// hosted_sessions hold the session, or it is the editor, in the session's
// project, of a session that runs in an IDE. A session the bridge reports
// runs in one: its records come from the editor's wakatime-cli (DESIGN
// 5.7), and no hook says more about its process.
func (d *deriver) hostedBy(f *focusRec, s *session, pos int) bool {
	if f.hosts(s.key) {
		return true
	}
	return (s.ide(pos) || s.bridged) && f.class == event.AppEditor && (f.project == "" || f.project == s.project)
}

// classify gives a person's prompt its class (SM 2.2). The prompt's text
// is local, so the class reads what the agent derived from it when it
// recorded the prompt: low_effort, from LowEffort below, and error_sig.
func (d *deriver) classify(s *session, pos int, p *event.PromptSubmit) event.PromptClass {
	typed := int64(p.TypedLenEst)
	switch {
	case p.AnswersQuestion || s.askedBefore(pos):
		return event.ClassAnswer
	case p.LowEffort:
		return event.ClassLowEffort
	case p.ErrorSig != "" && typed < d.r.errorPasteMax:
		// The pasted error is in pasted_chars, so typed_len_est is what
		// was typed around it.
		return event.ClassErrorPaste
	case typed >= d.r.substantive:
		return event.ClassSubstantive
	}
	return event.ClassLowEffort
}

// leadParts returns the think and typing parts of a prompt's lead (SM 2.3):
// THINK_LEAD_S, ANSWER_LEAD_S or LOW_LEAD_S by class, then the typing time.
// A go-ahead's lead needs presence only and has no typing part. A bridge
// prompt gets the typing part only (DESIGN 6.2 D4).
func (d *deriver) leadParts(class event.PromptClass, via event.Via, typed int64) (think, typing int64) {
	if via == event.ViaBridge || via == event.ViaBridgeUnverified {
		return 0, d.r.typingMS(typed)
	}
	switch class {
	case event.ClassAnswer:
		return d.r.answerLead, d.r.typingMS(typed)
	case event.ClassLowEffort:
		return d.r.lowLead, 0
	}
	return d.r.thinkLead, d.r.typingMS(typed)
}

// goAheads is SM 2.2's go-ahead list, in SM's words and order. A pick such
// as "1", "2" or "option 2" is on it too; see LowEffort.
var goAheads = []string{
	"yes", "y", "ok", "okay", "sure", "continue", "go", "go on", "go ahead", "do it", "proceed",
	"next", "keep going", "lgtm", "looks good", "sounds good", "thanks", "try again", "again",
	"retry", "fix it", "more",
}

// normalized is the go-ahead list as normalize leaves it.
var normalizedGoAheads = func() map[string]bool {
	m := make(map[string]bool, len(goAheads))
	for _, g := range goAheads {
		m[normalize(g)] = true
	}
	return m
}()

// normalize lowercases text and strips its punctuation and whitespace (SM
// 2.2).
func normalize(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsPunct(r) || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LowEffort reports whether a prompt's text is a go-ahead by SM 2.2's text
// test: normalized, it is under 4 characters, on the go-ahead list, or a
// pick ("1", "option 2"). The agent applies it to the text when it records
// a prompt and stores the result in prompt.submit's low_effort, since the
// text itself never leaves the device. Whether the agent's last output
// asked a question, which makes the prompt an answer instead, the scorer
// decides from the records.
func LowEffort(text string) bool {
	n := normalize(text)
	if utf8.RuneCountInString(n) < 4 || normalizedGoAheads[n] {
		return true
	}
	return isPick(n)
}

// isPick reports whether normalized text picks an option: digits, or
// "option" and digits.
func isPick(n string) bool {
	n = strings.TrimPrefix(n, "option")
	if n == "" {
		return false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
