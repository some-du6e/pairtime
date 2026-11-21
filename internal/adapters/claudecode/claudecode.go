// SPDX-License-Identifier: AGPL-3.0-or-later

// Package claudecode is Claude Code's adapter, part 1: its hooks (DESIGN
// 5.1; docs/PLAN.md, WP-AD2). The transcript reader and the lookback
// reader are part 2 (WP-AD3), and the OTLP receiver is WP-AD4's.
//
// Each hook call `pairtime hook --harness claude_code --event <e>` hands
// the agent becomes SM 1.2 events:
//
//   - SessionStart is session.start, its start_kind from source. Its
//     entrypoint, parent_class and has_tty come from the agent's walk of
//     the hook's parent chain alone, so one without a walk that counts, a
//     spooled one included, has entrypoint and parent_class unknown and
//     has_tty false (D-spool-via). A walked call of a session that has no
//     session.start with via hook yet, because its SessionStart came
//     through the spool, without a counted walk or not at all, first gets
//     one from the walk, as the process scan builds one (DESIGN 5.2).
//   - UserPromptSubmit is prompt.submit, its turn_key from prompt_id,
//     which the transcript's promptId and OTel's prompt.id share, so the
//     three agree turn by turn (DESIGN 7.3, CC1). The text goes in the
//     record's content, never in the event. A prompt that matches, by
//     prefix, a scheduled prompt that the session's last Stop listed in
//     session_crons has matches_pending_cron (SM 2.2 rule 1), and a
//     background subagent's report, which Claude Code sends as a
//     <task-notification> prompt, is declared subagent (SM 2.2 rule 3).
//     A message typed while a turn runs is queued: Claude Code runs the
//     hook as it queues it, with the running turn's prompt_id (WP-AD1's
//     step 21), so a prompt whose prompt_id an earlier call of the session
//     carried is agent.queue in that turn, SM 2.3's Q anchor. A scheduled
//     prompt or a task notification never is: Claude Code runs its hook
//     when it takes it up, with a prompt_id of its own (E-cc-queued-turn).
//   - Stop is agent.output for last_assistant_message, with is_final, and
//     agent.stop with stop_hook_active (SM 2.2 rule 2) and the session's
//     scheduled prompts as pending_crons. StopFailure is agent.stop alone,
//     with stop_reason error, or limit for a rate limit or the output cap:
//     its last_assistant_message is the API error's text, not Claude's.
//   - PostToolUse for Edit, Write and Bash is agent.tool, with the lines
//     structuredPatch adds and removes, the edited file's key, and for a
//     command its class, its output's visible lines and error signature.
//     PostToolUseFailure, for the same tools, is agent.tool too: a call
//     that started and failed, such as a test run that exited non-zero,
//     with the exit code and the output its error text gives.
//   - SubagentStart and SubagentStop are agent.subagent.
//   - SessionEnd is session.end with its reason.
//   - PermissionRequest and Notification make no event. A hook alone can't
//     say who answered a permission request: a PermissionRequest hook or a
//     config rule can answer for the person. So the adapter holds each
//     request, and the PostToolUse that follows it, until Claude Code's
//     own OTel claude_code.tool_decision says who decided (Decide; WP-AD4;
//     E-6.1-permission). Notifications only mark that Claude waits, which
//     presence already sees from the hook itself.
//
// Envelopes get source hook and the via, times and walk the agent worked
// out (adapters.HookRecord): via hook only for a call over the socket or
// the pipe whose walked chain reaches claude, and plugin for every other,
// spooled calls included (D-spool-via). A call that another harness's
// process ran, as when Devin or Cursor's desktop app runs these hooks, is
// that harness's to record (DESIGN 5.1), so Parse returns
// adapters.ErrOtherHarness for it.
//
// Fields Claude Code 2.1.327 doesn't send, and events the adapter doesn't
// read, are parse failures, which never stop the call's other events from
// being made (DESIGN 5.3).
package claudecode

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The hook events pairtime installs in Claude Code (DESIGN 5.1), and
// PostToolUseFailure, which the adapter reads too
// (docs/errata/2026-11-20-claude-code-tool-failure.md).
const (
	EventSessionStart       = "SessionStart"
	EventUserPromptSubmit   = "UserPromptSubmit"
	EventStop               = "Stop"
	EventStopFailure        = "StopFailure"
	EventNotification       = "Notification"
	EventPermissionRequest  = "PermissionRequest"
	EventPostToolUse        = "PostToolUse"
	EventPostToolUseFailure = "PostToolUseFailure"
	EventSubagentStart      = "SubagentStart"
	EventSubagentStop       = "SubagentStop"
	EventSessionEnd         = "SessionEnd"
)

// toolMatcher is the matcher PostToolUse and PostToolUseFailure are
// installed under.
const toolMatcher = "Edit|Write|Bash"

// harness is the harness this adapter is for.
const harness = event.HarnessClaudeCode

func init() {
	adapters.Register(harness, func(o adapters.Options) adapters.Adapter { return New(o) })
}

//go:embed fixtures
var fixtures embed.FS

// Adapter is Claude Code's adapter. Its methods are safe for concurrent
// use.
type Adapter struct {
	opts adapters.Options
	keys *adapters.Keys

	mu       sync.Mutex
	sessions map[string]*session
	clock    uint64
}

// New returns an adapter that keys ids and resolves projects with opts. It
// panics when opts.Keys is nil, since every id the adapter records is keyed
// under the device's K_session and no other key may stand in for it.
func New(opts adapters.Options) *Adapter {
	if opts.Keys == nil {
		panic("claudecode: New needs Options.Keys, the device's K_session")
	}
	return &Adapter{opts: opts, keys: opts.Keys, sessions: map[string]*session{}}
}

// Name is claude_code.
func (*Adapter) Name() event.Harness { return harness }

// Detect says whether Claude Code's folder exists: $CLAUDE_CONFIG_DIR when
// that is an absolute path, else ~/.claude (HS 3.2). Hooks don't say which
// version runs; the transcripts and OTel do (WP-AD3, WP-AD4).
func (*Adapter) Detect(env adapters.Env) adapters.Detection {
	settings := install.ClaudeSettings(env)
	d := adapters.Detection{ConfigPaths: []string{settings}}
	if fi, err := os.Stat(filepath.Dir(settings)); err == nil && fi.IsDir() {
		d.Installed = true
	}
	return d
}

// Plan returns the install item that adds pairtime's hooks to Claude
// Code's user settings (internal/install).
func (*Adapter) Plan(env adapters.Env) ([]adapters.ConfigEdit, error) {
	item, err := install.ClaudeCode(env)
	if err != nil {
		return nil, err
	}
	return []adapters.ConfigEdit{item}, nil
}

// Events lists the hook events Parse reads, in DESIGN 5.1's order, with
// PostToolUseFailure after PostToolUse.
var Events = []string{
	EventSessionStart, EventUserPromptSubmit, EventStop, EventStopFailure, EventNotification,
	EventPermissionRequest, EventPostToolUse, EventPostToolUseFailure, EventSubagentStart, EventSubagentStop,
	EventSessionEnd,
}

// Sources lists the hook events Parse reads. The transcripts are WP-AD3's.
func (*Adapter) Sources() []adapters.Source {
	out := make([]adapters.Source, len(Events))
	for i, e := range Events {
		out[i] = adapters.Source{Kind: adapters.SourceHook, Event: e}
		if e == EventPostToolUse || e == EventPostToolUseFailure {
			out[i].Matcher = toolMatcher
		}
	}
	return out
}

// Hosts returns the executables the platform's app-class table lists as
// Claude Code's own processes: the CLI, which the VS Code extension and
// the Desktop app's Code tab start too (HS 3.5; CP 4.1).
func (*Adapter) Hosts() []adapters.ProcessMatch {
	var m adapters.ProcessMatch
	for _, e := range platform.Entries() {
		if e.Role == platform.RoleHarness && e.Harness == harness {
			m.Names = append(m.Names, e.Names...)
		}
	}
	return []adapters.ProcessMatch{m}
}

// Fixtures holds the expected records of each capture this adapter
// replays, by version, OS and run: <version>/<os>/<run>/expected.jsonl,
// beside testdata/fixtures/claude_code/<version>/<os>/<run> (Conform).
func (*Adapter) Fixtures() fs.FS {
	sub, err := fs.Sub(fixtures, "fixtures")
	if err != nil {
		panic(err) // the embedded directory is there, or this doesn't build
	}
	return sub
}

// Bounds on what the adapter remembers. Claude Code runs a few sessions at
// once, and a session a few turns at a time.
const (
	maxSessions = 256
	maxTurns    = 64
	maxHeld     = 64
)

// session is what the adapter remembers of one Claude Code session.
type session struct {
	// id is Claude Code's session_id. It stays in memory.
	id string
	// hookStart says a session.start with via hook was made for the
	// session (D-spool-via).
	hookStart bool
	// started are the prompt_ids the session's calls named, oldest first:
	// each prompt's that started a turn, and the turn of any other call,
	// such as one that took up a queued message.
	started []string
	// named holds, for each id this adapter added to started, the earliest
	// time, in Unix milliseconds, at which a call named it. Calls can reach
	// Parse in another order than they were made, a spooled one after a
	// later one over the socket, so whether a call is earlier than another
	// goes by the times they carry: the stamps hookpath.Times gives, which
	// are the hooks' own unless a hook had no monotonic reading or stamped
	// itself later than the agent took it in. A call in the same
	// millisecond counts as earlier, so calls whose stamps tie, as the
	// agent's own stamps of one spool drain can, keep the order they
	// arrived in. An id of started with no time, such as one loaded from
	// saved memory, counts as named before any call.
	named map[string]int64
	// turn is the turn the session's latest call named: its prompt_id, or
	// one the adapter made up when Claude Code sent none.
	turn string
	// unnamed counts the turns the adapter made a turn id for.
	unnamed int
	// crons are the scheduled prompts the session's last Stop listed (SM
	// 2.2 rule 1).
	crons []scheduled
	// requests are permission requests no report has answered, and posts
	// the PostToolUse calls that may answer one, oldest first (Decide).
	requests []*request
	posts    []post
	seen     uint64
}

// sessionFor returns the session with Claude Code's id, making it, and
// marks it as heard from now. It forgets the session heard from least
// recently once it holds maxSessions.
func (a *Adapter) sessionFor(id string) *session {
	a.clock++
	s := a.sessions[id]
	if s == nil {
		if len(a.sessions) >= maxSessions {
			var oldest *session
			for _, o := range a.sessions {
				if oldest == nil || o.seen < oldest.seen {
					oldest = o
				}
			}
			delete(a.sessions, oldest.id)
		}
		s = &session{id: id}
		a.sessions[id] = s
	}
	s.seen = a.clock
	return s
}

// HookReported reports whether a hook call of Claude Code's session id has
// reached the adapter, over the socket, the pipe or the spool. A log reader
// writes session.start only for a session no hook reported, so that its
// placeholder, with parent_class unknown and has_tty false, never stands in
// for a hooked session's own (E-2.8-lookback; SM 1.2). That holds for a
// session whose hooks only the spool delivered too: its records are side
// doors, and a log reader's session.start would be the one SM 2.2 rule 4
// read, making it headless.
func (a *Adapter) HookReported(sessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[sessionID] != nil
}

// startedTurn reports whether a call of the session already named
// prompt_id id.
func (s *session) startedTurn(id string) bool {
	for _, t := range s.started {
		if t == id {
			return true
		}
	}
	return false
}

// namedBefore reports whether a call of the session made no later than t
// named prompt_id id, or saved memory the adapter loaded holds it.
func (s *session) namedBefore(id string, t int64) bool {
	if !s.startedTurn(id) {
		return false
	}
	first, ok := s.named[id]
	return !ok || first <= t
}

// start records that a prompt made at t started turn id.
func (s *session) start(id string, t int64) { s.note(id, t) }

// note records that a call of the session made at t named turn id, which
// is then the session's latest. A turn that took up a queued message has
// no prompt of its own, so its first other call, a PostToolUse,
// PermissionRequest, Notification, SubagentStart, SubagentStop, Stop or
// StopFailure, is where the adapter learns its id (E-cc-queued-turn).
func (s *session) note(id string, t int64) {
	if !s.startedTurn(id) {
		s.started = append(s.started, id)
		if len(s.started) > maxTurns {
			delete(s.named, s.started[0])
			s.started = s.started[1:]
		}
		if s.named == nil {
			s.named = map[string]int64{}
		}
		s.named[id] = t
	} else if first, ok := s.named[id]; ok && t < first {
		s.named[id] = t
	}
	s.turn = id
}

// turnOf returns the turn a call names: its prompt_id, else the session's
// latest turn, else one made up for the session's first. Claude Code
// 2.1.327 sends prompt_id on every call after the first prompt; the rest is
// for a version that doesn't.
func (s *session) turnOf(promptID string) string {
	switch {
	case promptID != "":
		return promptID
	case s.turn != "":
		return s.turn
	}
	return s.newTurn()
}

// newTurn makes up a turn id, for a prompt that came without a prompt_id.
// It can't look like a prompt_id, which is a UUID.
func (s *session) newTurn() string {
	s.unnamed++
	return "pairtime-turn-" + strconv.Itoa(s.unnamed)
}
