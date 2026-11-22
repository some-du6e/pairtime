// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"slices"
	"strconv"
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
//     turn, unless a task notification queued itself (rule 3).
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
//   - session.start comes only for a session no via-hook call reported,
//     at the first entry that states an entrypoint token and again
//     wherever the token changes, with parent_class unknown, has_tty false
//     and the token's entrypoint (E-2.8-lookback; E-cc-transcript-labels).
//     Claude Code can write a session's first entries before its first
//     hook call reaches the agent, so for an entry the reader reads as it
//     is written the record waits startGrace for a via-hook call, whose
//     walk gives the session its start instead. A call the spool delivered
//     cancels nothing.
//   - No agent.permission: the transcript says who answered a permission
//     request, but dates no answer of a command (SM 1.2;
//     docs/errata/2026-11-21-claude-code-transcript-permissions.md).
//
// A session's own hooks report its stops and its Edit, Write and Bash calls
// from the first session.start with via hook on (session.nativeAt), and a
// second record of one would count it twice: pkg/score sums every
// agent.output and agent.tool of a turn. So from then on the reader writes
// no final output, no agent.stop and no Edit, Write or Bash call that
// succeeded, whatever it remembers of the hook calls, which a kill can lose,
// and no other call that a via hook PostToolUse reported. A spooled hook
// call is via plugin, which the scorer leaves out of a session a log reader
// covers (DESIGN 5.2), so for a session whose hooks only the spool
// delivered the transcript's copies are the ones that count, and it writes
// them. Prompts, interrupts and queued messages it always writes: the
// scorer takes the copies of one such act for one act, and a prompt's log
// copy is CC1's.

// Bounds on what the reader remembers.
const (
	// maxLogFiles and maxLogSessions bound the transcript files and the
	// sessions it remembers: those it read from last, for files those it
	// would forget last (older), and for sessions those a remembered file
	// names first. There is room for more sessions than the files can name,
	// so none a file names is the one forgotten (logSessionFor).
	maxLogFiles    = 256
	maxLogSessions = maxLogFiles + 64
	// recentFiles is how many of the files it read from last it keeps
	// over the rest: lookback hands a history over a batch at a time, and
	// other files' lines come between its batches.
	recentFiles = 16
	// maxPendingTools bounds the tool uses of one file that wait for their
	// results.
	maxPendingTools = 64
)

// startGrace is how long a session.start waits for a via-hook call of its
// session before the reader writes it, a call that gives the session the
// walk's start instead: Claude Code can write a session's first entries
// before its first hook call reaches the agent, as while the agent starts,
// with room for a slow one. A call the spool delivered cancels nothing
// (sessionStart).
const startGrace = 90_000

// maxToolWait is how long after its use a tool use waits for its result
// once the file is idle: a session killed while a tool ran writes none,
// and a tool use that waits holds the file's memory back (LogResume).
// Claude Code's own limit on a command is 10 minutes; a subagent can run
// longer.
const maxToolWait = 6 * 60 * 60 * 1000

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

// hookedTools are the tools whose calls pairtime's PostToolUse hook
// reports, as its matcher names them (toolMatcher; DESIGN 5.1).
var hookedTools = []string{"Edit", "Write", "Bash"}

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
	// nativeAt is when the session's hooks came via hook from
	// (session.nativeAt), 0 for never: a call the spool delivered doesn't
	// count. It stays when the adapter forgets the session's calls.
	nativeAt int64
	seen     uint64
}

// txFile is what the reader remembers of one transcript file.
type txFile struct {
	key string
	// at is where the last line the reader took ends.
	at int64
	// st is what the reader knows of the file after that line, and pend
	// what it holds for records a later line completes.
	st   txState
	pend txPending
	// snap is st as it was at snapAt, the end of the last line after
	// which pend was empty: where a restart delivers the file from again,
	// to rebuild pend, which holds content and so is never saved
	// (LogResume).
	snap   txState
	snapAt int64
	seen   uint64
	// newest is the time of the newest entry the reader took from the
	// file, in Unix ms, which ranks it for eviction (older).
	newest int64
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
	// records of entries that name neither. Ignored says that folder is out
	// of pairtime (LogIgnored), which keeps the records of every line and
	// idle marker of the file out until an entry names one that counts.
	Project string `json:"project,omitempty"`
	Version string `json:"version,omitempty"`
	Ignored bool   `json:"ignored,omitempty"`
	// Queued counts the messages queued while a turn ran, each of which an
	// agent.queue recorded, that no entry has picked up yet: a version
	// without the labels marks no pickup (E-cc-queued-turn).
	Queued int `json:"queued,omitempty"`
}

// txPending is what the reader holds of a file in memory alone: tool uses
// waiting for their results, a slash command's markup waiting for the
// entry that says it starts a turn, a turn's last message waiting for the
// line that shows the turn stopped there, all of which are content, and a
// session.start waiting for startGrace to pass.
type txPending struct {
	tools   []pendingTool
	command *pendingCommand
	final   *pendingFinal
	// starts are the session.starts that wait, in the order their entries
	// came: one more for each change of the token.
	starts []*pendingStart
}

func (p *txPending) empty() bool {
	return len(p.tools) == 0 && p.command == nil && p.final == nil && len(p.starts) == 0
}

// maxPendingStarts bounds the session.starts of one file that wait: a
// token that changes more often than that within startGrace waits for the
// next entry that states it.
const maxPendingStarts = 4

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
	// at is where the line holding its text starts, and part the text's
	// place in its message's content, which name its records
	// (adapters.Record.LogKey).
	at   int64
	part int
	env  event.Envelope
	text string
	out  output
	// active says the stop is a stop hook's continuation, and continued
	// that a /goal verdict after the message found the goal unmet, so the
	// turn goes on.
	active, continued bool
}

// pendingStart is a session.start that waits for a hook of its session.
type pendingStart struct {
	session string
	at      int64 // where the entry that stated the token starts
	env     event.Envelope
	start   event.SessionStart
	token   string
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
			l.resolveStart()
			l.settle()
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
			f.newest = max(f.newest, e.t)
		}
		if !ll.Replay {
			l.resolveStart()
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

// settle drops, once the file is idle, what no later line will resolve: a
// slash command's markup whose turn didn't start, as /exit's, and tool uses
// that have waited longer than maxToolWait.
func (l *logLine) settle() {
	f := l.f
	f.pend.command = nil
	kept := f.pend.tools[:0]
	for _, pt := range f.pend.tools {
		if pt.t > 0 && l.raw.Arrival.T-pt.t < maxToolWait {
			kept = append(kept, pt)
		}
	}
	clear(f.pend.tools[len(kept):])
	f.pend.tools = kept
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

// key names the record of this line that kind says (adapters.Record.LogKey).
func (l *logLine) key(kind string) string { return logKey(l.raw.Log.Offset, kind) }

func logKey(at int64, kind string) string { return strconv.FormatInt(at, 10) + "." + kind }

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
		f.st.Project, f.st.Ignored, l.root = "", false, ""
		if l.a.opts.Project != nil {
			p, ok := l.a.opts.Project(e.CWD)
			switch {
			case ok:
				f.st.Project, l.root = p.Key, p.Root
			case p.Ignored:
				f.st.Ignored = true
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
// turn's duration, any user entry, or another message.
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

// add appends a record of payload p with content text, named key.
func (l *logLine) add(key string, env event.Envelope, p event.Payload, text []byte) *adapters.Record {
	l.recs = append(l.recs, adapters.Record{Event: event.Event{Envelope: env, Data: p}, Text: text, LogKey: key})
	return &l.recs[len(l.recs)-1]
}

// logSession returns what the reader remembers of session sid, with what
// the adapter's hook memory says of it now: since when its hooks come via
// hook.
func (a *Adapter) logSession(sid string) *txSession {
	ls := a.logSessionFor(sid)
	if s := a.sessions[sid]; s != nil && s.hookStart && (ls.nativeAt == 0 || s.nativeAt < ls.nativeAt) {
		// 0 is a session native from its start, as an older build's memory
		// says, so it can stand for one only when ls has none.
		ls.nativeAt = max(s.nativeAt, 1)
	}
	return ls
}

// hookedAt reports whether the session's via-hook calls, as s remembers
// the first of them, describe a moment t: one no more than startGrace
// before that call, as a session's first entries can be. An entry older
// than that, as lookback reads from a session's history before its hooks
// came, keeps the reader's session.start: the hook's own sorts after it,
// so SM 2.2 rules 4 and 5 can't read that one for it.
func (s *txSession) hookedAt(t int64) bool { return s.nativeAt > 0 && t >= s.nativeAt-startGrace }

// noteNative copies the native cutoff of session s, which a hook call just
// set, into the reader's memory of the session, so the reader keeps it
// when the hook calls' memory of the session is gone, as after 256 others,
// before the session's transcript is read. a.mu is held.
func (a *Adapter) noteNative(s *session) {
	ls := a.logSessionFor(s.id)
	if ls.nativeAt == 0 || s.nativeAt < ls.nativeAt {
		ls.nativeAt = max(s.nativeAt, 1)
	}
}

// native reports whether session sid's hooks came via hook at t, when they
// report its stops and tool calls themselves.
func (a *Adapter) native(sid string, t int64) bool {
	ls := a.logSession(sid)
	return ls.nativeAt > 0 && t >= ls.nativeAt
}

// sessionStart has a session no hook reported via hook get the
// session.start a log reader writes: at the first entry that states an
// entrypoint token, and again where the token changes
// (E-cc-transcript-labels). A session whose hooks only the spool delivered
// gets it too, since pkg/score reads no side door's start in a session a
// log reader covers, so its own would leave SM 2.2 rules 4 and 5 nothing
// to read. So does a hooked session's history from before its hooks came,
// as lookback reads it (hookedAt). A subagent's entries state the
// session's token too, and are no start of it. It waits in pend until
// startGrace after the entry, unless the entry is that old already
// (resolveStart).
func (l *logLine) sessionStart(e *entry, sid string) {
	if e.Entrypoint == nil || e.IsSidechain || sid == "" || e.t == 0 {
		return
	}
	ls := l.a.logSession(sid)
	if ls.hookedAt(e.t) {
		return
	}
	// The session's token as of this entry: the last waiting start's, or
	// the last one written.
	tok := *e.Entrypoint
	started, last := ls.started, ls.token
	for _, ps := range l.f.pend.starts {
		if ps.session == sid {
			started, last = true, ps.token
		}
	}
	if started && last == tok || len(l.f.pend.starts) >= maxPendingStarts {
		return
	}
	ep, known := tokenEntrypoint(tok)
	if !known {
		l.perr.Problem("an entrypoint token HS 3.3 doesn't list")
	}
	kind := event.StartStartup
	if started {
		kind = event.StartResume
	}
	ps := &pendingStart{session: sid, at: l.raw.Log.Offset, env: l.env(e.t, sid), token: tok,
		start: event.SessionStart{StartKind: kind, Entrypoint: ep, ParentClass: event.ParentUnknown}}
	if isKey(e.PermissionMode) {
		ps.start.PermissionMode = e.PermissionMode
	}
	l.f.pend.starts = append(l.f.pend.starts, ps)
	if !l.raw.Log.Replay {
		// In a replay it waits for the lines after it: the time it went by
		// is today's, which would write it there, where its record goes.
		l.resolveStart()
	}
}

// resolveStart writes the pending session.start once startGrace has
// passed since its entry with no via-hook call of its session, and drops it
// when one came that the entry is no older than (hookedAt). One written after its entry's line is dated a millisecond
// before the entry, so it sorts before the entry's other records (pkg/score
// orders records by time, then by when they were written).
func (l *logLine) resolveStart() {
	for len(l.f.pend.starts) > 0 {
		ps := l.f.pend.starts[0]
		ls := l.a.logSession(ps.session)
		switch {
		case ls.hookedAt(ps.env.T):
		case l.raw.Arrival.T-ps.env.T >= startGrace:
			ls.started, ls.token = true, ps.token
			env := ps.env
			if l.raw.Log.Offset != ps.at || l.raw.Log.Idle {
				env.T--
				env.Mono = max(env.Mono-1, 0)
			}
			st := ps.start
			l.add(logKey(ps.at, "start"), env, &st, nil)
		default:
			// It waits, and the ones after it, in their order.
			return
		}
		l.f.pend.starts = l.f.pend.starts[1:]
	}
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
	for i, p := range m.Content.Parts {
		if p.Type == "tool_result" {
			l.toolResult(e, sid, i, p)
		}
	}
	if e.IsSidechain {
		// A subagent's task, and what its tools gave it, which no person
		// and no hook sent (SM 2.2 rule 3).
		return
	}
	if e.IsCompactSummary {
		// The summary a compaction puts in place of the conversation,
		// with the running turn's promptId, which no person sent.
		return
	}
	text := m.Content.text()
	switch {
	case e.TurnOrigin != nil || e.PromptSource != nil:
		// A prompt, whatever its text says: Claude Code labels no
		// interrupt entry.
		l.prompt(e, sid, text, false)
	case isInterrupt(text):
		l.interrupt(e, sid)
	case e.PromptID != "":
		if cmd, ok := commandText(text); ok {
			// /goal's markup comes in an entry of its own, before the one
			// that starts the turn; /exit's in one that starts none.
			l.f.pend.command = &pendingCommand{promptID: e.PromptID, text: cmd}
		} else if cmd := l.f.pend.command; cmd != nil && cmd.promptID == e.PromptID {
			// The command's own output, which started no turn: /goal's
			// output is the entry that starts its turn.
			l.f.pend.command = nil
		} else if strings.TrimSpace(text) != "" && !e.IsMeta && !e.IsVisibleInTranscriptOnly && !opensWith(text, notPrompts) {
			// A prompt of a version that writes neither label, or of one
			// that renamed them: it still counts, declaring nothing, and the
			// failure says the format moved (SM Risks; HS 17.7).
			l.perr.Problem("a user entry with neither turnOrigin nor promptSource")
			l.prompt(e, sid, text, true)
		}
	}
}

// notPrompts open the user entries without labels that Claude Code writes
// for what no person sent the agent as a prompt: a local command's output,
// bash mode's command and its output, a memory note, a teammate's message
// and an MCP server's update.
var notPrompts = []string{"<local-command-", "<bash-input", "<bash-stdout", "<bash-stderr", "<user-memory-input",
	"<teammate-message", "<mcp-resource-update", "<mcp-polling-update"}

// opensWith reports whether text, past its leading white space, opens with
// one of marks.
func opensWith(text string, marks []string) bool {
	text = strings.TrimLeft(text, " \t\r\n")
	for _, m := range marks {
		if strings.HasPrefix(text, m) {
			return true
		}
	}
	return false
}

// isTaskNotification reports whether text is the report Claude Code sends
// the main agent when a background subagent finishes (SM 2.2 rule 3).
func isTaskNotification(text string) bool { return opensWith(text, []string{taskNotification}) }

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
	l.add(l.key("interrupt"), l.env(e.t, sid), &event.AgentInterrupt{TurnKey: l.a.keys.Turn(sid, turn)}, nil)
}

// prompt reads an entry that starts a turn.
func (l *logLine) prompt(e *entry, sid, text string, unlabeled bool) {
	var src, origin string
	if e.TurnOrigin != nil {
		origin = *e.TurnOrigin
		if !contains(turnOrigins, origin) {
			l.perr.Problem("a turnOrigin no capture has shown")
		}
	}
	declared := event.DeclaredNone
	switch {
	case origin == "scheduled":
		// SM 2.2 rule 1: the session's scheduler fired it.
		declared = event.DeclaredCron
	case origin == "task_notification", isTaskNotification(text):
		// SM 2.2 rule 3: a background subagent's report.
		declared = event.DeclaredSubagent
	}
	if unlabeled && l.f.st.Queued > 0 && declared == event.DeclaredNone {
		// The pickup of a message queued while the turn before ran, which
		// its agent.queue already recorded. A task notification is none:
		// what was queued decides (E-cc-queued-turn).
		src = "queued"
	}
	if e.PromptSource != nil {
		src = *e.PromptSource
		if !contains(promptSources, src) {
			l.perr.Problem("a promptSource no capture has shown")
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
		f.st.Queued = max(f.st.Queued-1, 0)
		return
	}
	if declared == event.DeclaredNone {
		// A prompt of its own that rules 1 to 3 leave unlabeled ends the
		// wait of any message queued before it (E-cc-queued-turn).
		f.st.Queued = 0
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
		OriginDeclared: declared,
		LowEffort:      score.LowEffort(text),
	}
	l.add(l.key("prompt"), l.env(e.t, sid), p, []byte(text))
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
	for i, p := range m.Content.Parts {
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
				l.pendFinal(e, sid, m.ID, p.Text, i)
			} else {
				o := outputOf(p.Text)
				l.add(l.key("output"+strconv.Itoa(i)), l.env(e.t, sid), &event.AgentOutput{TurnKey: l.a.keys.Turn(sid, f.st.Turn),
					Words: o.words, CodeLines: o.codeLines, AsksQuestion: o.asks, OffersOptions: o.offers}, nil)
			}
		}
	}
	if final && main && (f.pend.final == nil || f.pend.final.msg != m.ID) {
		// The turn stops at this message, which has shown no text yet: a
		// text block of it may follow.
		l.pendFinal(e, sid, m.ID, "", len(m.Content.Parts))
	}
}

// pendFinal holds the end_turn message's text block until a later line
// shows it is the turn's last. An earlier text block of the same message
// isn't, and is written as output that isn't final.
func (l *logLine) pendFinal(e *entry, sid, msg, text string, part int) {
	f := l.f
	if old := f.pend.final; old != nil && old.msg == msg && old.text != "" && text != "" {
		o := old.out
		l.add(logKey(old.at, "output"+strconv.Itoa(old.part)), old.env, &event.AgentOutput{TurnKey: l.a.keys.Turn(old.session, old.turn),
			Words: o.words, CodeLines: o.codeLines, AsksQuestion: o.asks, OffersOptions: o.offers}, nil)
	}
	fin := &pendingFinal{msg: msg, turn: f.st.Turn, session: sid, at: l.raw.Log.Offset, part: part, env: l.env(e.t, sid), text: text,
		active: f.st.Blocked}
	if text != "" {
		fin.out = outputOf(text)
	}
	f.pend.final = fin
}

// resolveFinal writes the pending message as its turn's final output and
// stop, at the message's last entry, unless the session's hooks came via
// hook by then, whose Stop reports both.
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
	if l.a.native(fin.session, fin.env.T) || l.a.hookStopped(fin.session, fin.turn) {
		// The hooks report the turn's end: from the session's first
		// session.start with via hook on, or at a Stop that came via hook,
		// as for a session that turned native at that Stop, whose final
		// message is stamped before it.
		return
	}
	key := l.a.keys.Turn(fin.session, fin.turn)
	reason := event.StopEndTurn
	if fin.text != "" {
		o := fin.out
		l.add(logKey(fin.at, "final"), fin.env, &event.AgentOutput{TurnKey: key, Words: o.words, CodeLines: o.codeLines,
			IsFinal: true, AsksQuestion: o.asks, OffersOptions: o.offers}, []byte(fin.text))
		if o.asks {
			reason = event.StopQuestion
		}
	}
	l.add(logKey(fin.at, "stop"), fin.env, &event.AgentStop{TurnKey: key, StopReason: reason, StopHookActive: fin.active}, nil)
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
// subagent's. In a hooked session the hook reported the message too, as it
// was queued, and pkg/score takes the two for one corroborated act.
func (l *logLine) queued(e *entry, sid string) {
	switch e.Operation {
	case "enqueue":
	case "dequeue":
		// Taken up: its pickup is the next prompt entry.
		return
	case "remove":
		// Out of the queue without a pickup: a message that made an
		// agent.queue waits no longer. A task notification made none.
		if !isTaskNotification(string(e.Content)) {
			l.f.st.Queued = max(l.f.st.Queued-1, 0)
		}
		return
	case "popAll":
		// Back in the input box, to be sent again as a prompt of its own.
		l.f.st.Queued = 0
		return
	default:
		l.perr.Problem("a queue-operation Claude Code 2.1.327 doesn't write")
		return
	}
	f := l.f
	text := string(e.Content)
	if sid == "" || e.t == 0 || !f.st.Running || f.st.Turn == "" || isTaskNotification(text) {
		return
	}
	l.add(l.key("queue"), l.env(e.t, sid), &event.AgentQueue{TurnKey: l.a.keys.Turn(sid, f.st.Turn),
		LenChars: utf8.RuneCountInString(text)}, nil)
	f.st.Queued++
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
// holds, as its i-th part: from e's toolUseResult, as AD2 reads a
// PostToolUse hook's tool_response (tool), with the call's span from its
// use to its result. A call the person approved waited for them in between,
// which the transcript doesn't date, so its span starts at its result.
func (l *logLine) toolResult(e *entry, sid string, i int, p msgPart) {
	pt, ok := l.f.takeTool(p.ToolUseID)
	if !ok || sid == "" || e.t == 0 {
		return
	}
	if e.ToolDenialKind != "" || e.ToolUseResult.Text == "User rejected tool use" {
		// Refused before it ran, so no tool ran.
		return
	}
	if l.a.native(sid, e.t) && !p.IsError && contains(hookedTools, pt.name) {
		// The session's PostToolUse hook reports the call.
		return
	}
	if s := l.a.sessions[sid]; s != nil && s.postedNative(p.ToolUseID) {
		return
	}
	t := &event.AgentTool{ToolClass: event.ToolOther, TStart: e.t, TEnd: e.t}
	if pt.t > 0 && pt.t <= e.t && !strings.HasPrefix(e.PermissionDecision.Source, "user_") {
		t.TStart = pt.t
	}
	kit := toolKit{feat: l.a.opts.Features, root: l.root, perr: l.perr}
	resp := e.ToolUseResult.Resp
	var r *recordContent
	switch pt.name {
	case "Edit", "MultiEdit", "NotebookEdit":
		t.ToolClass = event.ToolEdit
		if !p.IsError {
			l.badPatch(e)
			r = kit.edit(t, toolInput{FilePath: pt.file}, resp)
		}
	case "Write":
		t.ToolClass = event.ToolWrite
		if !p.IsError {
			l.badPatch(e)
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
	rec := l.add(l.key("tool"+strconv.Itoa(i)), l.env(e.t, sid), t, nil)
	if r != nil {
		rec.Edits, rec.Command = r.edits, r.command
	}
	if rec.Command != "" {
		// For the git watcher, which tells an agent's commit from the
		// person's by the command that made it (NoteAgentCommand).
		rec.Dir, rec.Call = e.CWD, p.ToolUseID
	}
}

// badPatch counts an edit's structuredPatch that isn't a list of hunks,
// whose lines the reader can't count.
func (l *logLine) badPatch(e *entry) {
	if e.ToolUseResult.BadPatch {
		l.perr.Problem("an edit's structuredPatch isn't a list of hunks")
	}
}

// fileFor returns what the reader remembers of file key, making it, and
// marks it read from now. Past maxLogFiles it forgets a file (older).
func (a *Adapter) fileFor(key string) *txFile {
	a.clock++
	f := a.files[key]
	if f == nil {
		if len(a.files) >= maxLogFiles {
			recent := recentSeen(a.files)
			var oldest *txFile
			for _, o := range a.files {
				if oldest == nil || older(o, oldest, recent) {
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

// older reports whether the reader's memory, when full, forgets file o
// before p: a file that holds nothing back first, then one that isn't
// among the recentFiles it read from last, those whose seen is recent or
// later (recentSeen), so the history lookback reads now stays, then the
// one whose newest entry is oldest, so the hundreds of histories lookback
// reads go before a session being written now, and then the one read from
// least recently. A file forgotten while it is written to again is read
// again from its start.
func older(o, p *txFile, recent uint64) bool {
	if o.pend.empty() != p.pend.empty() {
		return o.pend.empty()
	}
	if or, pr := o.seen >= recent, p.seen >= recent; or != pr {
		return pr
	}
	if o.newest != p.newest {
		return o.newest < p.newest
	}
	return o.seen < p.seen
}

// recentSeen returns the seen of the recentFiles-th of files read from
// last, or 0 when there are no more files than that.
func recentSeen(files map[string]*txFile) uint64 {
	if len(files) <= recentFiles {
		return 0
	}
	seen := make([]uint64, 0, len(files))
	for _, f := range files {
		seen = append(seen, f.seen)
	}
	slices.Sort(seen)
	return seen[len(seen)-recentFiles]
}

// restart starts what the reader knows of a file afresh at offset at.
func (f *txFile) restart(at int64) {
	f.at, f.st, f.pend, f.snap, f.snapAt = at, txState{}, txPending{}, txState{}, at
}

// logSessionFor returns what the reader remembers of session sid, making
// it. Past maxLogSessions it forgets the session read from least recently
// of those no remembered file names, so a session that went quiet while
// lookback read hundreds of others keeps what it knows, as its file does
// (older), or of all of them when every one is named.
func (a *Adapter) logSessionFor(sid string) *txSession {
	a.clock++
	s := a.logSessions[sid]
	if s == nil {
		if len(a.logSessions) >= maxLogSessions {
			named := a.namedSessions()
			var oldest, oldestNamed *txSession
			for _, o := range a.logSessions {
				switch {
				case named[o.id]:
					if oldestNamed == nil || o.seen < oldestNamed.seen {
						oldestNamed = o
					}
				case oldest == nil || o.seen < oldest.seen:
					oldest = o
				}
			}
			if oldest == nil {
				oldest = oldestNamed
			}
			delete(a.logSessions, oldest.id)
		}
		s = &txSession{id: sid}
		a.logSessions[sid] = s
	}
	s.seen = a.clock
	return s
}

// namedSessions returns the sessions the files the reader remembers name.
// a.mu is held.
func (a *Adapter) namedSessions() map[string]bool {
	named := make(map[string]bool, len(a.files))
	for _, f := range a.files {
		if f.st.Session != "" {
			named[f.st.Session] = true
		}
	}
	return named
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

// LogIgnored reports whether the last entry of file that named a folder
// named one out of pairtime (adapters.LogReader).
func (a *Adapter) LogIgnored(file string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.files[file]
	return f != nil && f.st.Ignored
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

// learn records that turn id began at t, which a transcript's queued
// pickup says and no hook call of the turn's says before the turn's own
// calls (E-cc-queued-turn). Unlike note it leaves the session's latest
// turn as it was.
func (s *session) learn(id string, t int64) {
	latest := s.turn
	s.note(id, t)
	s.turn = latest
}
