// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/score"
)

// call is one hook call while Parse reads it.
type call struct {
	a   *Adapter
	raw adapters.RawRecord
	f   fields
	// pid is the payload's prompt_id, "" when it has none.
	pid  string
	s    *session
	env  event.Envelope
	proj adapters.Project
	perr *adapters.ParseError
	recs []adapters.Record
}

// Parse turns one Claude Code hook call into records (the package doc
// says which). The records carry no event_id, seq or device_id, which the
// agent sets when it writes them.
func (a *Adapter) Parse(raw adapters.RawRecord) ([]adapters.Record, error) {
	perr := &adapters.ParseError{Event: safeName(raw.Event)}
	switch {
	case raw.Source != adapters.SourceHook:
		perr.Problem("Claude Code's adapter reads hooks only, not %s records", safeName(string(raw.Source)))
		return nil, perr
	case raw.Arrival.Harness != "" && raw.Arrival.Harness != harness:
		return nil, adapters.ErrOtherHarness
	case raw.Incomplete:
		perr.Problem("the payload didn't reach the agent whole")
		return nil, perr
	}
	f, ok := decode(raw.Payload, perr)
	if !ok {
		return nil, perr
	}
	name := raw.Event
	if hen, ok := f.str("hook_event_name"); ok && hen != name {
		// The payload's own name wins: it is what Claude Code ran.
		perr.Problem("--event says %s, the payload's hook_event_name %s", safeName(name), safeName(hen))
		name = hen
	}
	perr.Event = safeName(name)
	if _, ok := own[name]; !ok {
		perr.Problem("pairtime doesn't install a hook for this event")
		return nil, perr
	}
	if u := f.unknown(name); len(u) > 0 {
		perr.Problem("fields Claude Code 2.1.327 doesn't send: %s", quoteNames(u))
	}
	sid, _ := f.str("session_id")
	if sid == "" {
		perr.Problem("no session_id")
		return nil, perr
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	c := &call{a: a, raw: raw, f: f, s: a.sessionFor(sid), perr: perr}
	if cwd, ok := f.str("cwd"); ok && cwd != "" && a.opts.Project != nil {
		if p, ok := a.opts.Project(cwd); ok {
			c.proj = p
		}
	}
	c.env = event.Envelope{
		T: raw.Arrival.T, Mono: raw.Arrival.Mono, BootID: raw.Arrival.BootID,
		Source: event.SourceHook, Via: raw.Arrival.Via, Harness: harness,
		SessionKey: a.keys.Session(sid), ProjectKey: c.proj.Key,
	}
	if w := raw.Arrival.Walk; w != nil && raw.Arrival.Via == event.ViaHook && !c.s.hookStart && name != EventSessionStart {
		// D-spool-via: a walked call of a session with no session.start
		// with via hook gets one from the walk first, as the process scan
		// builds one, so SM 2.2 rule 4 reads the session's parent chain.
		c.add(&event.SessionStart{StartKind: event.StartStartup, Entrypoint: entrypointOf(*w),
			ParentClass: parentOf(*w), HasTTY: w.HasTTY}, nil)
		c.s.hookStart = true
	}
	c.pid, _ = f.str("prompt_id")
	if c.pid != "" && name != EventUserPromptSubmit {
		// Every call names the turn it is in, and a turn that took up a
		// queued message names its own only in calls like this one, so a
		// message queued while it runs is its agent.queue (E-cc-queued-turn).
		c.s.note(c.pid, c.env.T)
	}
	switch name {
	case EventSessionStart:
		c.sessionStart()
	case EventUserPromptSubmit:
		c.prompt()
	case EventStop:
		c.stop(false)
	case EventStopFailure:
		c.stop(true)
	case EventPostToolUse, EventPostToolUseFailure:
		c.tool(name == EventPostToolUseFailure)
	case EventPermissionRequest:
		c.permissionRequest()
	case EventSubagentStart, EventSubagentStop:
		c.subagent(name == EventSubagentStart)
	case EventSessionEnd:
		c.sessionEnd()
	case EventNotification:
		// No SM 1.2 kind: presence reads the hook call itself.
	}
	return c.recs, perr.Err()
}

// add appends a record of payload p with content text.
func (c *call) add(p event.Payload, text []byte) *adapters.Record {
	c.recs = append(c.recs, adapters.Record{Event: event.Event{Envelope: c.env, Data: p}, Text: text})
	return &c.recs[len(c.recs)-1]
}

// turnKey returns the turn_key of Claude Code's turn id.
func (c *call) turnKey(id string) string { return c.a.keys.Turn(c.s.id, id) }

// entrypointOf is the entrypoint a walk found: ide or desktop from its
// host, sdk or exec once the command-line follow-up reads print flags
// (E-procs-cmdlines), else unknown.
func entrypointOf(l platform.Lineage) event.Entrypoint {
	switch l.Entrypoint {
	case event.EntrypointIDE, event.EntrypointDesktop, event.EntrypointSDK, event.EntrypointExec:
		return l.Entrypoint
	}
	return event.EntrypointUnknown
}

func parentOf(l platform.Lineage) event.ParentClass {
	if l.ParentClass == "" {
		return event.ParentUnknown
	}
	return l.ParentClass
}

// startKinds maps SessionStart's source to start_kind (HS 3.1).
var startKinds = map[string]event.StartKind{
	"startup": event.StartStartup,
	"resume":  event.StartResume,
	"clear":   event.StartClear,
	"compact": event.StartCompact,
	"fork":    event.StartFork,
}

func (c *call) sessionStart() {
	src, _ := c.f.str("source")
	kind, ok := startKinds[src]
	if !ok {
		c.perr.Problem("source isn't startup, resume, clear, compact or fork")
		kind = event.StartStartup
	}
	st := &event.SessionStart{StartKind: kind, Entrypoint: event.EntrypointUnknown, ParentClass: event.ParentUnknown}
	if w := c.raw.Arrival.Walk; w != nil && c.raw.Arrival.Via == event.ViaHook {
		st.Entrypoint, st.ParentClass, st.HasTTY = entrypointOf(*w), parentOf(*w), w.HasTTY
		c.s.hookStart = true
	}
	if pm, ok := c.f.str("permission_mode"); ok {
		if isKey(pm) {
			st.PermissionMode = pm
		} else {
			c.perr.Problem("permission_mode isn't a mode's name")
		}
	}
	c.add(st, nil)
}

func (c *call) sessionEnd() {
	end := &event.SessionEnd{}
	if r, ok := c.f.str("reason"); ok {
		if isKey(r) {
			end.Reason = r
		} else {
			c.perr.Problem("reason isn't a reason's name")
		}
	}
	c.add(end, nil)
}

// taskNotification opens the prompt Claude Code sends the main agent when
// a background subagent finishes (WP-AD1's step 10; HS 3.1).
const taskNotification = "<task-notification>"

func (c *call) prompt() {
	text, ok := c.f.str("prompt")
	if !ok {
		c.perr.Problem("no prompt")
		return
	}
	if !utf8.ValidString(text) {
		// encoding/json has already replaced bad bytes; this can't happen
		// but would break the ledger's commitment.
		text = strings.ToValidUTF8(text, "\uFFFD")
	}
	n := utf8.RuneCountInString(text)
	// SM 2.2 rules 1 and 3 read a queued message's text as they read a
	// prompt's (E-cc-queued-turn).
	cron := c.s.matchesCron(text)
	// Machine text from a background subagent, which no person typed.
	notification := strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), taskNotification)
	if c.pid != "" && c.s.namedBefore(c.pid, c.env.T) {
		// A message typed while the agent works: Claude Code runs the hook
		// as it queues the message, with the running turn's prompt_id, and
		// the turn that takes the message up later has a prompt_id no hook
		// names before that turn's own calls (WP-AD1's step 21). So a
		// prompt whose prompt_id an earlier call of the session carried is
		// that turn's agent.queue, SM 2.3's Q anchor, and never a second
		// prompt of the turn, which the scorer would take for another copy
		// of the first (E-cc-queued-turn). Earlier goes by the calls' own
		// times, so a prompt that reaches the adapter through the spool
		// after a later call of its turn still starts it. Only a person's
		// message is queued: Claude Code runs a scheduled prompt's or a
		// task notification's hook as it takes it up, with a prompt_id of
		// its own. A message queued in a turn that took up another before
		// any call of that turn named its prompt_id is still a
		// prompt.submit, until WP-AD3 learns the turn's key from the
		// transcript's promptSource queued entry.
		if cron || notification {
			c.perr.Problem("a scheduled prompt or a task notification came with a running turn's prompt_id, which Claude Code 2.1.327 doesn't send")
			return
		}
		c.add(&event.AgentQueue{TurnKey: c.turnKey(c.pid), LenChars: n}, nil)
		return
	}
	id := c.pid
	if id == "" {
		id = c.s.newTurn()
	}
	c.s.start(id, c.env.T)
	p := &event.PromptSubmit{
		TurnKey:            c.turnKey(id),
		LenChars:           n,
		TypedLenEst:        n,
		SlashCommand:       slashCommand(text),
		OriginDeclared:     event.DeclaredNone,
		MatchesPendingCron: cron,
		LowEffort:          score.LowEffort(text),
	}
	if notification {
		// SM 2.2 rule 3.
		p.OriginDeclared = event.DeclaredSubagent
	}
	c.add(p, []byte(text))
}

// slashCommand returns the command a prompt starts with, such as "/loop",
// without its arguments, or "" when it doesn't start with one.
func slashCommand(text string) string {
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	name := text
	if i := strings.IndexFunc(text, isSpaceRune); i >= 0 {
		name = text[:i]
	}
	if len(name) < 2 || len(name) > 65 {
		return ""
	}
	for i := 1; i < len(name); i++ {
		b := name[i]
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || strings.IndexByte("_:.-", b) >= 0) {
			return ""
		}
	}
	return name
}

func isSpaceRune(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

// isKey reports whether s has pkg/event's opaque key form.
func isKey(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

// cron is one entry of Stop's session_crons (HS 3.1).
type cron struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`
	Recurring bool   `json:"recurring"`
	Prompt    string `json:"prompt"`
}

// stop maps Stop, or StopFailure when failed. StopFailure's
// last_assistant_message is the API error's text as the conversation shows
// it, such as "API Error: Rate limit reached", not Claude's reply, so it
// makes no agent.output and isn't committed as the turn's final message
// (SM 1.2; the hooks reference).
func (c *call) stop(failed bool) {
	key := c.turnKey(c.s.turnOf(c.pid))
	reason := event.StopEndTurn
	if msg, ok := c.f.str("last_assistant_message"); ok && msg != "" && !failed {
		o := outputOf(msg)
		c.add(&event.AgentOutput{TurnKey: key, Words: o.words, CodeLines: o.codeLines, IsFinal: true,
			AsksQuestion: o.asks, OffersOptions: o.offers}, []byte(msg))
		if o.asks {
			reason = event.StopQuestion
		}
	}
	st := &event.AgentStop{TurnKey: key, StopReason: reason, StopHookActive: c.f.boolean("stop_hook_active")}
	if failed {
		st.StopReason = event.StopError
		if e, ok := c.f.str("error"); ok && (e == "rate_limit" || e == "max_output_tokens") {
			st.StopReason = event.StopLimit
		}
	} else {
		var crons []cron
		c.f.list("session_crons", &crons)
		c.s.crons = c.s.crons[:0]
		for _, cr := range crons {
			sc := scheduledOf(cr.Prompt)
			c.s.crons = append(c.s.crons, sc)
			prefix := sc.prefix
			if cr.Schedule == "" || utf8.RuneCountInString(cr.Schedule) > 64 || strings.ContainsFunc(cr.Schedule, isControl) {
				c.perr.Problem("a scheduled prompt's schedule isn't one")
				continue
			}
			st.PendingCrons = append(st.PendingCrons, event.PendingCron{
				PrefixHash: c.a.keys.Cron(c.s.id, prefix), Schedule: cr.Schedule})
		}
	}
	c.add(st, nil)
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f || 0x80 <= r && r < 0xa0 }

// cronMax is how much of a scheduled prompt Stop's session_crons keeps
// (HS 3.1), and SM 2.2 rule 1 compares.
const cronMax = 1000

// scheduled is a scheduled prompt as rule 1 compares it.
type scheduled struct {
	// prefix is the prompt as Stop listed it, without the marker of a cut,
	// and at most cronMax code points.
	prefix string
	// cut says Stop cut the prompt.
	cut bool
}

// cutMarker opens the marker Claude Code puts after a scheduled prompt it
// cut at 1,000 characters: "… [+N chars]" (HS 3.1).
const cutMarker = "\u2026 [+"

// scheduledOf reads a scheduled prompt as Stop's session_crons lists it.
func scheduledOf(p string) scheduled {
	var sc scheduled
	if i := strings.LastIndex(p, cutMarker); i >= 0 && strings.HasSuffix(p, " chars]") {
		digits := p[i+len(cutMarker) : len(p)-len(" chars]")]
		if digits != "" && strings.Trim(digits, "0123456789") == "" {
			// A cut can split a UTF-16 surrogate pair, which decodes to
			// U+FFFD, and no prompt has that half at that place.
			p, sc.cut = strings.TrimRight(p[:i], "\uFFFD"), true
		}
	}
	sc.prefix = firstRunes(p, cronMax)
	return sc
}

// firstRunes returns s's first n code points.
func firstRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

// matchesCron applies SM 2.2 rule 1: the prompt's first 1,000 characters
// are a scheduled prompt's, as the session's last Stop listed it. A
// scheduled prompt Stop cut matches every prompt that starts with what it
// kept, since Claude Code counts the 1,000 characters in UTF-16 units and
// may keep fewer code points; one it didn't cut matches only itself.
func (s *session) matchesCron(text string) bool {
	head := firstRunes(text, cronMax)
	return slices.ContainsFunc(s.crons, func(sc scheduled) bool {
		if sc.prefix == "" {
			return false
		}
		if sc.cut {
			return strings.HasPrefix(text, sc.prefix)
		}
		return head == sc.prefix
	})
}

func (c *call) subagent(start bool) {
	id, _ := c.f.str("agent_id")
	if id == "" {
		c.perr.Problem("no agent_id")
		return
	}
	phase := event.SubagentStop
	if start {
		phase = event.SubagentStart
	}
	c.add(&event.AgentSubagent{Phase: phase, AgentKey: c.a.keys.Agent(c.s.id, id)}, nil)
}
