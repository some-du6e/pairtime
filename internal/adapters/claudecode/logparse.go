// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"strings"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/score"
)

// The transcript reader (WP-AD3) turns the lines of a Claude Code
// transcript into SM 1.2 records with source and via log. They hold only
// what the transcript states (SM 1.2, 2.8):
//
//   - A user entry with turnOrigin or promptSource starts a turn, keyed by
//     its promptId, which a hook's prompt_id and OTel's prompt.id share, so
//     CC1 lines the hook's prompt up with it (DESIGN 7.3). Its prompt.submit
//     has the text a hook would: the entry's, or "/name args" for a slash
//     command, whose markup the entry or the one before it holds.
//     turnOrigin scheduled declares cron (SM 2.2 rule 1), task_notification
//     or a <task-notification> opening subagent (rule 3), and the rest of
//     the values a capture has shown declare nothing; one no capture has
//     shown is a parse failure (E-cc-transcript-labels). promptSource
//     queued is the pickup of a message queued while the agent worked,
//     whose agent.queue the enqueue made: it starts its turn and is no
//     prompt (E-cc-queued-turn).
//   - A queue-operation enqueue while a turn runs is agent.queue in that
//     turn, unless a task notification queued itself.
//   - Each text block of an assistant entry is agent.output in its turn,
//     is_final for the turn's message whose stop_reason is end_turn, and
//     that message is also the turn's agent.stop, at its last entry, with
//     stop_hook_active once a /goal verdict found the goal unmet since the
//     turn's prompt (HS 3.5). "[Request interrupted by user ...]" is
//     agent.interrupt in its turn: Claude Code fires no Stop hook for one
//     (HS 3.1), and SM 1.3 ends a turn at either.
//   - Each tool use with its result is agent.tool, from its result as AD2
//     reads a PostToolUse hook's tool_response, which is the same object
//     (WP-AD1's capture). A call refused before it ran is none.
//   - A subagent's entries, isSidechain, give its tool calls alone, with no
//     visible output: the person reads none of a subagent's work (SM 2.4).
//   - session.start comes only for a session no hook reported, at the
//     first entry that states an entrypoint token and again wherever the
//     token changes, with parent_class unknown, has_tty false and the
//     token's entrypoint (E-2.8-lookback; E-cc-transcript-labels).
//   - No agent.permission: the transcript says who answered a permission
//     request, but dates no answer the scorer could anchor (SM 1.2;
//     docs/errata/2026-11-21-claude-code-transcript-permissions.md).
//
// For a session a hook reported, the hooks' records are the session's own
// for what they report, and a second record of it would count twice
// (pkg/score sums every agent.output and agent.tool of a turn). So there
// the reader writes no session.start and no agent.queue, writes the final
// message and stop of a turn only when no via hook Stop of the turn came,
// and a tool call only when no via hook PostToolUse of it came. A call or
// Stop that only the spool delivered is via plugin, which the scorer
// leaves out of a session a log reader covers (DESIGN 5.2), so the
// transcript's copy is the one that counts. Prompts and interrupts it
// always writes: copies of one prompt or one interrupt are one act to the
// scorer, and a prompt's log copy is CC1's.

// Bounds on what the reader remembers.
const (
	// maxLogFiles and maxLogSessions bound the transcript files and the
	// sessions it remembers: those it read from last.
	maxLogFiles    = 256
	maxLogSessions = 256
	// maxPendingTools bounds the tool uses of one file that wait for their
	// results.
	maxPendingTools = 64
	// maxStops bounds the turns of one session whose via hook Stop the
	// adapter remembers.
	maxStops = 16
)

// turnOrigins and promptSources are the values of turnOrigin and
// promptSource that WP-AD1's capture shows (E-cc-transcript-labels).
var (
	turnOrigins   = []string{"human", "sdk", "scheduled", "task_notification"}
	promptSources = []string{"typed", "system", "queued", "sdk"}
)

// entrypointTokens are the entrypoint tokens HS 3.3 lists, which
// Claude Code's transcripts state per entry and OTel as app.entrypoint.
var entrypointTokens = []string{
	"cli", "sdk-cli", "sdk-ts", "sdk-py", "claude-vscode", "claude-desktop", "claude-desktop-3p", "claude-in-slack",
	"remote", "remote_desktop", "remote_mobile",
}

// tokenEntrypoint maps an entrypoint token to the entrypoint a log
// reader's session.start states: sdk for the SDK's, cloud for a session on
// a vendor VM, and unknown for the rest, cli and claude-vscode included,
// since only a walk of the process tree says ide or desktop
// (E-cc-transcript-labels). It reports false for a token HS 3.3 doesn't
// list.
func tokenEntrypoint(tok string) (event.Entrypoint, bool) {
	switch tok {
	case "sdk-cli", "sdk-ts", "sdk-py":
		return event.EntrypointSDK, true
	case "remote", "remote_desktop", "remote_mobile", "claude-in-slack":
		return event.EntrypointCloud, true
	}
	return event.EntrypointUnknown, contains(entrypointTokens, tok)
}

// txSession is what the reader remembers of one session's transcripts.
type txSession struct {
	id string
	// token is the entrypoint token of the last session.start the reader
	// wrote for the session, and started says it wrote one.
	token   string
	started bool
	// hooked says a hook call of the session reached the adapter, and
	// stays set when the adapter forgets the session's calls.
	hooked bool
	seen   uint64
}

// txFile is what the reader remembers of one transcript file.
type txFile struct {
	key string
	// at is where the last line the reader took ends.
	at int64
	// st is what the reader knows of the file after that line, and pend
	// the content it holds for records a later line completes.
	st   txState
	pend txPending
	// snap is st as it was at snapAt, the end of the last line after
	// which pend was empty: where a restart delivers the file from again,
	// to rebuild pend, which holds content and so is never saved
	// (LogResume).
	snap   txState
	snapAt int64
	seen   uint64
}

// txState is the part of what the reader knows of a file at a point in
// it that holds no content, so the adapter's saved memory may keep it.
type txState struct {
	// Session is the session id the file's entries name.
	Session string `json:"session,omitempty"`
	// Turn is the promptId of the turn the file's main conversation is
	// in, and Running says the agent works on it: it started and hasn't
	// stopped, been interrupted or been rewound.
	Turn    string `json:"turn,omitempty"`
	Running bool   `json:"running,omitempty"`
	// Blocked says a /goal verdict found the goal unmet since the turn's
	// prompt, so the turn's later stops are a stop hook's continuations.
	Blocked bool `json:"blocked,omitempty"`
	// Project is the project_key of the last entry that named a folder,
	// and Version the last harness version an entry stated, for the
	// records of entries that name neither.
	Project string `json:"project,omitempty"`
	Version string `json:"version,omitempty"`
}

// txPending is what the reader holds of a file in memory alone, since it
// is content: tool uses waiting for their results, a slash command's
// markup waiting for the entry that says it starts a turn, and a turn's
// last message waiting for the line that shows the turn stopped there.
type txPending struct {
	tools   []pendingTool
	command *pendingCommand
	final   *pendingFinal
}

func (p *txPending) empty() bool { return len(p.tools) == 0 && p.command == nil && p.final == nil }

// pendingTool is a tool use whose result hasn't come yet.
type pendingTool struct {
	id, name string
	// command and file are a Bash call's command line and an edit's path.
	command, file string
	t             int64
	sidechain     bool
}

// pendingCommand is a slash command's markup, and the prompt it stands
// for: "/name args".
type pendingCommand struct {
	promptID, text string
}

// pendingFinal is a turn's message whose stop_reason is end_turn, which
// is the turn's last unless a later entry of the turn shows otherwise.
type pendingFinal struct {
	msg, turn, session string
	env                event.Envelope
	text               string
	out                output
	// active says the stop is a stop hook's continuation, and continued
	// that a /goal verdict after the message found the goal unmet, so the
	// turn goes on.
	active, continued bool
}

// logLine is one transcript line while Parse reads it.
type logLine struct {
	a    *Adapter
	raw  adapters.RawRecord
	f    *txFile
	perr *adapters.ParseError
	recs []adapters.Record
	// root is the project root of the line's folder, which edits' paths
	// are keyed relative to.
	root string
}

// parseLog is Parse for a transcript line the tailers delivered.
func (a *Adapter) parseLog(raw adapters.RawRecord) ([]adapters.Record, error) {
	perr := &adapters.ParseError{Event: "transcript"}
	ll := raw.Log
	if ll == nil || ll.File == "" {
		perr.Problem("a transcript line came without its file")
		return nil, perr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fileFor(ll.File)
	l := &logLine{a: a, raw: raw, f: f, perr: perr}
	switch {
	case ll.Idle:
		// No line, the tailers' word that the file hasn't grown for a
		// while: a turn's last message is its last, whatever comes later.
		if ll.Offset == f.at {
			l.resolveFinal()
		}
	default:
		if ll.Offset == 0 || ll.Offset != f.at {
			// The file's first line, or not the line after the last one
			// the reader took: what it remembers of the file doesn't lead
			// here.
			f.restart(ll.Offset)
		}
		if ll.TooLong {
			perr.Problem("a line longer than the tailers read")
		} else if e, ok := l.entryOf(); ok {
			l.take(e)
		}
		f.at = ll.End
	}
	if f.pend.empty() {
		f.snap, f.snapAt = f.st, f.at
	}
	if ll.Replay {
		// Its records went to the ledger before the agent's last stop, and
		// its problems were counted then.
		return nil, nil
	}
	return l.recs, perr.Err()
}

// entryOf returns the line's entry: as the tailers decoded it, or decoded
// here.
func (l *logLine) entryOf() (*entry, bool) {
	ll := l.raw.Log
	if ll.DecodeErr != nil {
		l.perr.Problem("%s", ll.DecodeErr.Error())
		return nil, false
	}
	if e, ok := ll.Entry.(*entry); ok && e != nil {
		return e, true
	}
	e, err := decodeEntry(l.raw.Payload)
	if err != nil {
		l.perr.Problem("%s", err.Error())
		return nil, false
	}
	return e, true
}

// take reads one entry.
func (l *logLine) take(e *entry) {
	f := l.f
	sid := e.SessionID
	if sid == "" {
		sid = f.st.Session
	} else {
		f.st.Session = sid
	}
	if e.Version != "" && event.IsVersion(e.Version) {
		f.st.Version = e.Version
	}
	if e.CWD != "" {
		f.st.Project, l.root = "", ""
		if l.a.opts.Project != nil {
			if p, ok := l.a.opts.Project(e.CWD); ok {
				f.st.Project, l.root = p.Key, p.Root
			}
		}
	}
	// A slash command's markup waits only for the next entry of its
	// prompt, which says whether it starts a turn.
	if cmd := f.pend.command; cmd != nil && !(e.Type == "user" && e.PromptID == cmd.promptID) {
		f.pend.command = nil
	}
	if fin := f.pend.final; fin != nil && finalEnds(e, fin) {
		l.resolveFinal()
	}
	l.sessionStart(e, sid)
	switch e.Type {
	case "user":
		l.user(e, sid)
	case "assistant":
		l.assistant(e, sid)
	case "attachment":
		l.attachment(e)
	case "queue-operation":
		l.queued(e, sid)
	case "system":
		// stop_hook_summary and turn_duration end a turn (finalEnds), and
		// the other subtypes hold nothing the reader reads.
	default:
		if !contains(otherTypes, e.Type) {
			l.perr.Problem("an entry of a type Claude Code 2.1.327 doesn't write: %s", safeName(e.Type))
		}
	}
}

// finalEnds reports whether entry e shows that the pending message is its
// turn's last: the summary Claude Code writes once the Stop hooks ran, the
// turn's duration, any user entry, or another message. A Stop hook comes
// after the message's entries are written and before that summary (WP-AD1's
// capture), so a via hook Stop of the turn has reached the adapter by then
// (resolveFinal).
func finalEnds(e *entry, fin *pendingFinal) bool {
	switch e.Type {
	case "system":
		return e.Subtype == "stop_hook_summary" || e.Subtype == "turn_duration"
	case "user":
		return true
	case "assistant":
		return e.Message == nil || e.Message.ID != fin.msg
	}
	return false
}

// env is the envelope of a record of the line that happened at t, in
// session sid.
func (l *logLine) env(t int64, sid string) event.Envelope {
	arr := l.raw.Arrival
	return event.Envelope{
		T: t, Mono: max(arr.Mono+(t-arr.T), 0), BootID: arr.BootID,
		Source: event.SourceLog, Via: arr.Via, Harness: harness, HarnessVersion: l.f.st.Version,
		SessionKey: l.a.keys.Session(sid), ProjectKey: l.f.st.Project,
	}
}

// add appends a record of payload p with content text.
func (l *logLine) add(env event.Envelope, p event.Payload, text []byte) *adapters.Record {
	l.recs = append(l.recs, adapters.Record{Event: event.Event{Envelope: env, Data: p}, Text: text})
	return &l.recs[len(l.recs)-1]
}

// hooked reports whether a hook call of session sid reached the adapter
// (HookReported), and remembers that it did.
func (a *Adapter) hooked(sid string) bool {
	ls := a.logSessionFor(sid)
	if a.sessions[sid] != nil {
		ls.hooked = true
	}
	return ls.hooked
}

// sessionStart writes the session.start a log reader writes for a session
// no hook reported: at the first entry that states an entrypoint token, and
// again where the token changes (E-cc-transcript-labels). A subagent's
// entries state the session's token too, and are no start of it.
func (l *logLine) sessionStart(e *entry, sid string) {
	if e.Entrypoint == nil || e.IsSidechain || sid == "" || e.t == 0 || l.a.hooked(sid) {
		return
	}
	ls := l.a.logSessionFor(sid)
	tok := *e.Entrypoint
	if ls.started && ls.token == tok {
		return
	}
	ep, known := tokenEntrypoint(tok)
	if !known {
		l.perr.Problem("an entrypoint token HS 3.3 doesn't list")
	}
	kind := event.StartStartup
	if ls.started {
		kind = event.StartResume
	}
	ls.started, ls.token = true, tok
	st := &event.SessionStart{StartKind: kind, Entrypoint: ep, ParentClass: event.ParentUnknown}
	if isKey(e.PermissionMode) {
		st.PermissionMode = e.PermissionMode
	}
	l.add(l.env(e.t, sid), st, nil)
}

// user reads a user entry: tool results, an interrupt, a prompt, or a
// slash command's markup.
func (l *logLine) user(e *entry, sid string) {
	m := e.Message
	if m == nil {
		l.perr.Problem("a user entry without its message")
		return
	}
	if e.PromptID != "" && !e.IsSidechain {
		l.f.st.Turn = e.PromptID
	}
	for _, p := range m.Content.Parts {
		if p.Type == "tool_result" {
			l.toolResult(e, sid, p)
		}
	}
	if e.IsSidechain {
		// A subagent's task, and what its tools gave it, which no person
		// and no hook sent (SM 2.2 rule 3).
		return
	}
	text := m.Content.text()
	switch {
	case isInterrupt(text):
		l.interrupt(e, sid)
	case e.TurnOrigin != nil || e.PromptSource != nil:
		l.prompt(e, sid, text)
	case e.PromptID != "":
		if cmd, ok := commandText(text); ok {
			// /goal's markup comes in an entry of its own, before the one
			// that starts the turn; /exit's in one that starts none.
			l.f.pend.command = &pendingCommand{promptID: e.PromptID, text: cmd}
		}
	}
}

// interruptTexts are the lines Claude Code writes as a user entry when the
// person stops a running turn.
var interruptTexts = []string{"[Request interrupted by user]", "[Request interrupted by user for tool use]"}

func isInterrupt(text string) bool { return contains(interruptTexts, strings.TrimSpace(text)) }

// interrupt writes the agent.interrupt of the entry's turn.
func (l *logLine) interrupt(e *entry, sid string) {
	turn := e.PromptID
	if turn == "" {
		turn = l.f.st.Turn
	}
	l.f.st.Running = false
	if turn == "" || sid == "" || e.t == 0 {
		return
	}
	l.add(l.env(e.t, sid), &event.AgentInterrupt{TurnKey: l.a.keys.Turn(sid, turn)}, nil)
}

// prompt reads an entry that starts a turn.
func (l *logLine) prompt(e *entry, sid, text string) {
	var src, origin string
	if e.PromptSource != nil {
		src = *e.PromptSource
		if !contains(promptSources, src) {
			l.perr.Problem("a promptSource no capture has shown")
		}
	}
	if e.TurnOrigin != nil {
		origin = *e.TurnOrigin
		if !contains(turnOrigins, origin) {
			l.perr.Problem("a turnOrigin no capture has shown")
		}
	}
	if e.PromptID == "" || sid == "" {
		l.perr.Problem("a prompt without its promptId or sessionId")
		return
	}
	f := l.f
	f.st.Turn, f.st.Running, f.st.Blocked = e.PromptID, true, false
	// A tool use of an earlier turn whose result never came won't get one.
	kept := f.pend.tools[:0]
	for _, pt := range f.pend.tools {
		if pt.sidechain {
			kept = append(kept, pt)
		}
	}
	clear(f.pend.tools[len(kept):])
	f.pend.tools = kept
	if src == "queued" {
		// The pickup of a message typed while the agent worked. Its
		// agent.queue went at the enqueue, by the hook or by queued below,
		// and the turn it starts is that message's (E-cc-queued-turn). A
		// later hook prompt with this turn's prompt_id is a message queued
		// in it, which no hook call of the turn names before its own calls.
		if s := l.a.sessions[sid]; s != nil && e.t > 0 {
			s.learn(e.PromptID, e.t)
		}
		f.pend.command = nil
		return
	}
	cmd := f.pend.command
	f.pend.command = nil
	switch {
	case cmd != nil && cmd.promptID == e.PromptID:
		text = cmd.text
	default:
		if c, ok := commandText(text); ok {
			text = c
		} else if strings.HasPrefix(text, "<local-command-stdout>") {
			l.perr.Problem("a turn's local command output came without its command")
			return
		}
	}
	if e.t == 0 {
		l.perr.Problem("a prompt without its timestamp")
		return
	}
	n := utf8.RuneCountInString(text)
	p := &event.PromptSubmit{
		TurnKey:        l.a.keys.Turn(sid, e.PromptID),
		LenChars:       n,
		TypedLenEst:    n,
		SlashCommand:   slashCommand(text),
		OriginDeclared: event.DeclaredNone,
		LowEffort:      score.LowEffort(text),
	}
	switch {
	case origin == "scheduled":
		// SM 2.2 rule 1: the session's scheduler fired it.
		p.OriginDeclared = event.DeclaredCron
	case origin == "task_notification", strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), taskNotification):
		// SM 2.2 rule 3: a background subagent's report.
		p.OriginDeclared = event.DeclaredSubagent
	}
	l.add(l.env(e.t, sid), p, []byte(text))
}

// commandText returns the prompt a slash command's markup stands for,
// "/name args" as the person typed it, which is the prompt a hook gets:
// <command-name>/goal</command-name> with <command-args>...</command-args>.
func commandText(text string) (string, bool) {
	if !strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), "<command-") {
		return "", false
	}
	name, ok := between(text, "<command-name>", "</command-name>")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return "", false
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	if args, _ := between(text, "<command-args>", "</command-args>"); args != "" {
		return name + " " + args, true
	}
	return name, true
}

// between returns what s holds between the first open and the close after
// it.
func between(s, open, close string) (string, bool) {
	i := strings.Index(s, open)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// assistant reads an assistant entry: one block of a message, its text or
// a tool use.
func (l *logLine) assistant(e *entry, sid string) {
	m := e.Message
	if m == nil {
		l.perr.Problem("an assistant entry without its message")
		return
	}
	f := l.f
	final := m.StopReason == "end_turn" || m.StopReason == "stop_sequence"
	main := !e.IsSidechain && f.st.Turn != "" && sid != "" && e.t > 0
	for _, p := range m.Content.Parts {
		switch p.Type {
		case "tool_use":
			if p.ID != "" {
				l.pendTool(pendingTool{id: p.ID, name: p.Name, command: p.Input.Command, file: p.Input.FilePath,
					t: e.t, sidechain: e.IsSidechain})
			}
		case "text":
			if !main || p.Text == "" {
				continue
			}
			if final {
				l.pendFinal(e, sid, m.ID, p.Text)
			} else {
				o := outputOf(p.Text)
				l.add(l.env(e.t, sid), &event.AgentOutput{TurnKey: l.a.keys.Turn(sid, f.st.Turn), Words: o.words,
					CodeLines: o.codeLines, AsksQuestion: o.asks, OffersOptions: o.offers}, nil)
			}
		}
	}
	if final && main && (f.pend.final == nil || f.pend.final.msg != m.ID) {
		// The turn stops at this message, which has shown no text yet: a
		// text block of it may follow.
		l.pendFinal(e, sid, m.ID, "")
	}
}

// pendFinal holds the end_turn message's text block until a later line
// shows it is the turn's last. An earlier text block of the same message
// isn't, and is written as output that isn't final.
func (l *logLine) pendFinal(e *entry, sid, msg, text string) {
	f := l.f
	if old := f.pend.final; old != nil && old.msg == msg && old.text != "" && text != "" {
		o := old.out
		l.add(old.env, &event.AgentOutput{TurnKey: l.a.keys.Turn(old.session, old.turn), Words: o.words,
			CodeLines: o.codeLines, AsksQuestion: o.asks, OffersOptions: o.offers}, nil)
	}
	fin := &pendingFinal{msg: msg, turn: f.st.Turn, session: sid, env: l.env(e.t, sid), text: text, active: f.st.Blocked}
	if text != "" {
		fin.out = outputOf(text)
	}
	f.pend.final = fin
}

// resolveFinal writes the pending message as its turn's final output and
// stop, at the message's last entry, unless the turn's own Stop hook came
// via hook and reported both.
func (l *logLine) resolveFinal() {
	f := l.f
	fin := f.pend.final
	if fin == nil {
		return
	}
	f.pend.final = nil
	if !fin.continued {
		f.st.Running = false
	}
	if s := l.a.sessions[fin.session]; s != nil && s.stoppedNative(fin.turn) {
		return
	}
	key := l.a.keys.Turn(fin.session, fin.turn)
	reason := event.StopEndTurn
	if fin.text != "" {
		o := fin.out
		l.add(fin.env, &event.AgentOutput{TurnKey: key, Words: o.words, CodeLines: o.codeLines, IsFinal: true,
			AsksQuestion: o.asks, OffersOptions: o.offers}, []byte(fin.text))
		if o.asks {
			reason = event.StopQuestion
		}
	}
	l.add(fin.env, &event.AgentStop{TurnKey: key, StopReason: reason, StopHookActive: fin.active}, nil)
}

// attachment reads a /goal verdict: one that finds the goal unmet makes
// the Stop hook continue the turn (SM 2.2 rule 2; HS 3.5). The verdict
// /goal itself writes, with sentinel set, judges nothing yet.
func (l *logLine) attachment(e *entry) {
	at := e.Attachment
	if at == nil || at.Type != "goal_status" || at.Sentinel || at.Met == nil || *at.Met {
		return
	}
	f := l.f
	f.st.Blocked, f.st.Running = true, true
	if fin := f.pend.final; fin != nil {
		fin.continued = true
	}
}

// queued reads a queue-operation. An enqueue while a turn runs is a
// message the person typed for the agent to take up later, SM 2.3's Q
// anchor, which SM 1.2 fills from it (E-cc-queued-turn). Scheduled
// prompts and task notifications are queued too, while no turn runs, and a
// task notification can be queued during one: rule 3 makes it a
// subagent's. A hooked session's queued messages are its hooks' records.
func (l *logLine) queued(e *entry, sid string) {
	switch e.Operation {
	case "enqueue":
	case "dequeue", "remove", "popAll":
		return
	default:
		l.perr.Problem("a queue-operation Claude Code 2.1.327 doesn't write")
		return
	}
	f := l.f
	text := string(e.Content)
	if sid == "" || e.t == 0 || !f.st.Running || f.st.Turn == "" || l.a.hooked(sid) ||
		strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), taskNotification) {
		return
	}
	l.add(l.env(e.t, sid), &event.AgentQueue{TurnKey: l.a.keys.Turn(sid, f.st.Turn), LenChars: utf8.RuneCountInString(text)}, nil)
}

// pendTool holds a tool use until its result comes, dropping the oldest
// past maxPendingTools.
func (l *logLine) pendTool(pt pendingTool) {
	f := l.f
	f.pend.tools = append(f.pend.tools, pt)
	if len(f.pend.tools) > maxPendingTools {
		f.pend.tools = f.pend.tools[1:]
	}
}

// takeTool returns the tool use id names and stops holding it.
func (f *txFile) takeTool(id string) (pendingTool, bool) {
	for i, pt := range f.pend.tools {
		if pt.id == id {
			f.pend.tools = append(f.pend.tools[:i], f.pend.tools[i+1:]...)
			return pt, true
		}
	}
	return pendingTool{}, false
}

// toolResult writes the agent.tool of a tool use whose result entry e
// holds: from e's toolUseResult, as AD2 reads a PostToolUse hook's
// tool_response (tool), with the call's span from its use to its result.
func (l *logLine) toolResult(e *entry, sid string, p msgPart) {
	pt, ok := l.f.takeTool(p.ToolUseID)
	if !ok || sid == "" || e.t == 0 {
		return
	}
	if e.ToolDenialKind != "" || e.ToolUseResult.Text == "User rejected tool use" {
		// Refused before it ran, so no tool ran.
		return
	}
	if l.a.hooked(sid) {
		if s := l.a.sessions[sid]; s != nil && s.postedNative(p.ToolUseID) {
			return
		}
	}
	t := &event.AgentTool{ToolClass: event.ToolOther, TStart: e.t, TEnd: e.t}
	if pt.t > 0 && pt.t <= e.t {
		t.TStart = pt.t
	}
	kit := toolKit{feat: l.a.opts.Features, root: l.root, perr: l.perr}
	resp := e.ToolUseResult.Resp
	var r *recordContent
	switch pt.name {
	case "Edit", "MultiEdit", "NotebookEdit":
		t.ToolClass = event.ToolEdit
		if !p.IsError {
			r = kit.edit(t, toolInput{FilePath: pt.file}, resp)
		}
	case "Write":
		t.ToolClass = event.ToolWrite
		if !p.IsError {
			r = kit.edit(t, toolInput{FilePath: pt.file}, resp)
		}
	case "Bash":
		if p.IsError {
			// A command that ran and failed: its result is the error's
			// text, which says its exit code first, as PostToolUseFailure's
			// error does (docs/errata/2026-11-20-claude-code-tool-failure.md).
			resp = toolResponse{}
			if code, out, ok := exitLine(string(p.Content)); ok {
				t.ExitCode, resp.Stdout = &code, out
			}
		}
		r = kit.bash(t, toolInput{Command: pt.command}, resp)
	case "Read":
		t.ToolClass = event.ToolRead
	case "Grep", "Glob":
		t.ToolClass = event.ToolSearch
	case "WebFetch", "WebSearch":
		t.ToolClass = event.ToolWeb
	case "Agent", "Task":
		t.ToolClass = event.ToolSubagent
	}
	if pt.sidechain {
		// A subagent's tool output never reaches the conversation the
		// person reads (SM 2.4), as for a hook call with agent_id.
		t.VisibleOutputLines = 0
	}
	rec := l.add(l.env(e.t, sid), t, nil)
	if r != nil {
		rec.Edits, rec.Command = r.edits, r.command
	}
	if rec.Command != "" {
		// For the git watcher, which tells an agent's commit from the
		// person's by the command that made it (NoteAgentCommand).
		rec.Dir, rec.Call = e.CWD, p.ToolUseID
	}
}

// fileFor returns what the reader remembers of file key, making it, and
// marks it read from now. Past maxLogFiles it forgets the file read from
// least recently.
func (a *Adapter) fileFor(key string) *txFile {
	a.clock++
	f := a.files[key]
	if f == nil {
		if len(a.files) >= maxLogFiles {
			var oldest *txFile
			for _, o := range a.files {
				if oldest == nil || o.seen < oldest.seen {
					oldest = o
				}
			}
			delete(a.files, oldest.key)
		}
		f = &txFile{key: key}
		a.files[key] = f
	}
	f.seen = a.clock
	return f
}

// restart starts what the reader knows of a file afresh at offset at.
func (f *txFile) restart(at int64) {
	f.at, f.st, f.pend, f.snap, f.snapAt = at, txState{}, txPending{}, txState{}, at
}

// logSessionFor returns what the reader remembers of session sid, making
// it. Past maxLogSessions it forgets the session read from least recently.
func (a *Adapter) logSessionFor(sid string) *txSession {
	a.clock++
	s := a.logSessions[sid]
	if s == nil {
		if len(a.logSessions) >= maxLogSessions {
			var oldest *txSession
			for _, o := range a.logSessions {
				if oldest == nil || o.seen < oldest.seen {
					oldest = o
				}
			}
			delete(a.logSessions, oldest.id)
		}
		s = &txSession{id: sid}
		a.logSessions[sid] = s
	}
	s.seen = a.clock
	return s
}

// LogPlace returns the folder a transcript line ran in and its session id
// (adapters.LogReader).
func (a *Adapter) LogPlace(raw adapters.RawRecord) (dir, session string) {
	if raw.Source != adapters.SourceLog || raw.Log == nil || raw.Log.Idle || raw.Log.TooLong || raw.Log.DecodeErr != nil {
		return "", ""
	}
	e, ok := raw.Log.Entry.(*entry)
	if !ok || e == nil {
		var err error
		if e, err = decodeEntry(raw.Payload); err != nil {
			return "", ""
		}
	}
	return e.CWD, e.SessionID
}

// LogResume returns where in file the adapter's memory of it starts
// (adapters.LogReader).
func (a *Adapter) LogResume(file string) (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.files[file]
	if f == nil {
		return 0, false
	}
	return f.snapAt, true
}

// ForgetLog drops what the reader remembers of file (adapters.LogReader).
func (a *Adapter) ForgetLog(file string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.files, file)
}

// stopNative records that turn id's Stop came via hook, keeping the
// maxStops turns named last.
func (s *session) stopNative(id string) {
	if contains(s.stops, id) {
		return
	}
	s.stops = append(s.stops, id)
	if len(s.stops) > maxStops {
		s.stops = s.stops[1:]
	}
}

// stoppedNative reports whether a via hook Stop of turn id came: a
// transcript's copy of the turn's final message and stop would count them
// twice (logLine.resolveFinal).
func (s *session) stoppedNative(id string) bool { return contains(s.stops, id) }

// learn records that turn id began at t, which a transcript's queued
// pickup says and no hook call of the turn's says before the turn's own
// calls (E-cc-queued-turn). Unlike note it leaves the session's latest
// turn as it was.
func (s *session) learn(id string, t int64) {
	latest := s.turn
	s.note(id, t)
	s.turn = latest
}
