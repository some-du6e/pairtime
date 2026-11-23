// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import "slices"

// The payload types, one per kind, in SM 1.2 order. Fields tagged
// pairtime:"local" never leave the device; see Event.Shareable.

const (
	// MinHashSize is the number of values in a prompt's MinHash sketch.
	MinHashSize = 64
	// MaxLabelLen caps a cli.track label, in characters. The AGENTS.md
	// entry asks the agent for at most 400 characters of the user's
	// message, with personal information removed (DESIGN 5.4).
	MaxLabelLen = 400
)

// SessionStart is session.start: a harness session started, resumed, was
// cleared, compacted or forked.
type SessionStart struct {
	StartKind  StartKind  `json:"start_kind"`
	Entrypoint Entrypoint `json:"entrypoint"`
	// AutomationFlags are the harness's own automation tells as keys, such
	// as "background_agent" (HS 12.1).
	AutomationFlags []string    `json:"automation_flags,omitempty"`
	ParentClass     ParentClass `json:"parent_class"`
	HasTTY          bool        `json:"has_tty"`
	// PermissionMode is the harness's permission mode as it names it, such
	// as "acceptEdits" or "on-request".
	PermissionMode string `json:"permission_mode,omitempty"`
}

func (*SessionStart) Kind() Kind        { return KindSessionStart }
func (p *SessionStart) Validate() error { return validate(p.check) }

func (p *SessionStart) check(c *checker) {
	checkEnum(c, "start_kind", startKinds, p.StartKind)
	checkEnum(c, "entrypoint", entrypoints, p.Entrypoint)
	c.keys("automation_flags", p.AutomationFlags)
	checkEnum(c, "parent_class", parentClasses, p.ParentClass)
	c.optKey("permission_mode", p.PermissionMode)
}

// SessionEnd is session.end.
type SessionEnd struct {
	// Reason is the harness's own end reason, such as "logout".
	Reason string `json:"reason,omitempty"`
}

func (*SessionEnd) Kind() Kind        { return KindSessionEnd }
func (p *SessionEnd) Validate() error { return validate(p.check) }

func (p *SessionEnd) check(c *checker) {
	c.optKey("reason", p.Reason)
}

// PromptSubmit is prompt.submit: a prompt reached the agent. Whether a
// person typed it is decided later, from OS evidence (SM 2.2). This holds
// what the harness and the hook saw, and what an agent app's reader said
// about the run that delivered it: guest and relay (DESIGN 4.1 E-13).
type PromptSubmit struct {
	TurnKey string `json:"turn_key"`
	// LenChars counts the code points of the prompt as the harness delivered
	// it, pasted blocks expanded: the text the ledger commits, which CC8
	// checks it against (ledger-v1.md 10.1, 10.8). PastedChars counts code
	// points too.
	LenChars    int `json:"len_chars"`
	PastedChars int `json:"pasted_chars"`
	// TypedLenEst is LenChars - PastedChars.
	TypedLenEst int `json:"typed_len_est"`
	// MinHash is a MinHashSize-value sketch of the normalized prompt, for
	// re-roll detection (SM 4.2).
	MinHash      []uint32 `json:"minhash,omitempty" pairtime:"local"`
	TemplateHash string   `json:"template_hash,omitempty" pairtime:"local"`
	// ErrorSig is the hashed signature of an error pasted into the prompt
	// (SM 4.2).
	ErrorSig string `json:"error_sig,omitempty" pairtime:"local"`
	// SlashCommand is the command name only, such as "/loop", never its
	// arguments.
	SlashCommand       string         `json:"slash_command,omitempty"`
	OriginDeclared     DeclaredOrigin `json:"origin_declared"`
	MatchesPendingCron bool           `json:"matches_pending_cron"`
	AnswersQuestion    bool           `json:"answers_question"`
	LowEffort          bool           `json:"low_effort"`
	// Guest is set when an agent app's reader says a guest's message
	// started the prompt's run (SM 1.2, DESIGN 5.1): another person's
	// words, which the owner sent on. Such a prompt keeps no text, and SM
	// 2.2 rule 4 takes it whatever its session, even when an app message
	// witnesses it, so marking a prompt guest can only lose credit.
	Guest bool `json:"guest,omitempty"`
	// Relay is the event id of the relay.message the agent paired the
	// prompt with, when that record came first (SM 2.2 rule 9). The agent
	// writes a pairing on whichever of the two records arrives second, so a
	// prompt that came first has none, and its record's prompt names it
	// instead. The scorer reads only the link and checks its session and
	// times. Like event_id, it leaves the device only in tier-2 ledger
	// bodies (DESIGN 3.6).
	Relay string `json:"relay,omitempty"`
	// SealedText is the prompt text, encrypted on the device before the
	// event is written; base64 in JSON. Plain prompt text never goes in a
	// prompt.submit event. The one event field that holds it is
	// cli.track's label, which is local too.
	SealedText []byte `json:"text,omitempty" pairtime:"local"`
}

func (*PromptSubmit) Kind() Kind        { return KindPromptSubmit }
func (p *PromptSubmit) Validate() error { return validate(p.check) }

func (p *PromptSubmit) check(c *checker) {
	c.key("turn_key", p.TurnKey)
	c.count("len_chars", p.LenChars)
	c.count("pasted_chars", p.PastedChars)
	if p.PastedChars > p.LenChars {
		c.addf("pasted_chars", "is %d, more than len_chars (%d)", p.PastedChars, p.LenChars)
	}
	if want := p.LenChars - p.PastedChars; p.TypedLenEst != want {
		c.addf("typed_len_est", "must be len_chars - pasted_chars = %d, got %d", want, p.TypedLenEst)
	}
	if n := len(p.MinHash); n != 0 && n != MinHashSize {
		c.addf("minhash", "must have %d values, got %d", MinHashSize, n)
	}
	c.optKey("template_hash", p.TemplateHash)
	c.optKey("error_sig", p.ErrorSig)
	if p.SlashCommand != "" && !isSlashCommand(p.SlashCommand) {
		c.addf("slash_command", "must be '/' then 1-64 letters, digits, '_', ':', '.' or '-', got %q", p.SlashCommand)
	}
	checkEnum(c, "origin_declared", declaredOrigins, p.OriginDeclared)
	c.optEventID("relay", p.Relay)
	// A guest's words never stay on the device (SM 1.2). The text isn't
	// echoed: it is sealed content.
	if p.Guest && len(p.SealedText) > 0 {
		c.addf("text", "is set, but a guest's prompt keeps no text (SM 1.2)")
	}
}

// AgentOutput is agent.output: output the agent showed in a turn.
type AgentOutput struct {
	TurnKey string `json:"turn_key"`
	// Words counts displayed assistant prose outside fenced code blocks
	// (SM 2.4's prose_words), not hidden thinking. Fenced blocks count in
	// CodeLines instead, so code isn't counted twice.
	Words int `json:"words"`
	// CodeLines counts lines inside fenced code blocks.
	CodeLines     int  `json:"code_lines"`
	IsFinal       bool `json:"is_final"`
	AsksQuestion  bool `json:"asks_question"`
	OffersOptions bool `json:"offers_options"`
}

func (*AgentOutput) Kind() Kind        { return KindAgentOutput }
func (p *AgentOutput) Validate() error { return validate(p.check) }

func (p *AgentOutput) check(c *checker) {
	c.key("turn_key", p.TurnKey)
	c.count("words", p.Words)
	c.count("code_lines", p.CodeLines)
}

// AgentTool is agent.tool: one tool call by the agent.
type AgentTool struct {
	ToolClass ToolClass `json:"tool_class"`
	// Files are the paths the tool touched, each as an opaque key keyed
	// per device: an HMAC under a device key, never a plain hash a reader
	// could recompute from a guessed path (DESIGN 4.1 E-10, 5.7).
	// editor.activity's file_hash takes the same form, so an agent's edit
	// and a person's edit of one file compare equal (DESIGN 5.7).
	Files        []string `json:"files,omitempty"`
	LinesAdded   int      `json:"lines_added"`
	LinesRemoved int      `json:"lines_removed"`
	// CmdHash is a hash of the normalized command, for tools that run one.
	CmdHash string `json:"cmd_hash,omitempty" pairtime:"local"`
	// ExitCode is nil for tools that don't run a process. It is 64-bit
	// because Windows exit codes are unsigned 32-bit values.
	ExitCode *int64 `json:"exit_code,omitempty"`
	// ErrorSig is the signature of an error in the tool's output, as an
	// opaque key: HMAC(K_feat, signature) truncated to 16 bytes, so
	// revealing it in tier 2 leaks nothing a guess can test (DESIGN 3.6,
	// 4.1 E-10).
	ErrorSig           string `json:"error_sig,omitempty"`
	VisibleOutputLines int    `json:"visible_output_lines"`
	TStart             int64  `json:"t_start"`
	TEnd               int64  `json:"t_end"`
}

func (*AgentTool) Kind() Kind        { return KindAgentTool }
func (p *AgentTool) Validate() error { return validate(p.check) }

func (p *AgentTool) check(c *checker) {
	checkEnum(c, "tool_class", toolClasses, p.ToolClass)
	c.keys("files", p.Files)
	c.count("lines_added", p.LinesAdded)
	c.count("lines_removed", p.LinesRemoved)
	c.optKey("cmd_hash", p.CmdHash)
	c.optKey("error_sig", p.ErrorSig)
	c.count("visible_output_lines", p.VisibleOutputLines)
	c.span("t_start", p.TStart, "t_end", p.TEnd)
}

// AgentPermission is agent.permission: a permission request and its answer.
type AgentPermission struct {
	RequestedAt int64     `json:"requested_at"`
	AnsweredAt  int64     `json:"answered_at"`
	Decision    Decision  `json:"decision"`
	DecidedBy   DecidedBy `json:"decided_by"`
}

func (*AgentPermission) Kind() Kind        { return KindAgentPermission }
func (p *AgentPermission) Validate() error { return validate(p.check) }

func (p *AgentPermission) check(c *checker) {
	c.span("requested_at", p.RequestedAt, "answered_at", p.AnsweredAt)
	checkEnum(c, "decision", decisions, p.Decision)
	checkEnum(c, "decided_by", decidedBys, p.DecidedBy)
}

// AgentInterrupt is agent.interrupt: the person stopped a running turn.
type AgentInterrupt struct {
	TurnKey string `json:"turn_key"`
}

func (*AgentInterrupt) Kind() Kind        { return KindAgentInterrupt }
func (p *AgentInterrupt) Validate() error { return validate(p.check) }

func (p *AgentInterrupt) check(c *checker) {
	c.key("turn_key", p.TurnKey)
}

// AgentQueue is agent.queue: a message typed while the agent was working.
type AgentQueue struct {
	TurnKey  string `json:"turn_key"`
	LenChars int    `json:"len_chars"`
}

func (*AgentQueue) Kind() Kind        { return KindAgentQueue }
func (p *AgentQueue) Validate() error { return validate(p.check) }

func (p *AgentQueue) check(c *checker) {
	c.key("turn_key", p.TurnKey)
	c.count("len_chars", p.LenChars)
}

// AgentStop is agent.stop: a turn ended.
type AgentStop struct {
	TurnKey    string     `json:"turn_key"`
	StopReason StopReason `json:"stop_reason"`
	// StopHookActive is set when a stop hook made the turn continue, as
	// Claude Code's /goal does (SM 2.2 rule 2).
	StopHookActive bool `json:"stop_hook_active"`
	// PendingCrons are the session's scheduled prompts at this stop. A
	// later prompt matching one is labelled scheduled (SM 2.2 rule 1).
	// They hold digests of prompt text, so they stay on the device;
	// prompt.submit's matches_pending_cron carries the result instead.
	PendingCrons []PendingCron `json:"pending_crons,omitempty" pairtime:"local"`
}

// PendingCron is one scheduled prompt waiting in a session.
type PendingCron struct {
	// PrefixHash is a hash of the scheduled prompt's first 1,000 characters.
	PrefixHash string `json:"prefix_hash"`
	// Schedule is the schedule as the harness reports it, such as
	// "*/20 * * * *".
	Schedule string `json:"schedule"`
}

func (*AgentStop) Kind() Kind        { return KindAgentStop }
func (p *AgentStop) Validate() error { return validate(p.check) }

func (p *AgentStop) check(c *checker) {
	c.key("turn_key", p.TurnKey)
	checkEnum(c, "stop_reason", stopReasons, p.StopReason)
	if len(p.PendingCrons) > maxList {
		c.addf("pending_crons", "has %d entries, more than %d", len(p.PendingCrons), maxList)
		return
	}
	for i, pc := range p.PendingCrons {
		c.within(fieldIndex("pending_crons", i), func(c *checker) {
			c.key("prefix_hash", pc.PrefixHash)
			if pc.Schedule == "" {
				c.addf("schedule", "missing")
			}
			c.text("schedule", pc.Schedule, 64)
		})
	}
}

// AgentSubagent is agent.subagent: a subagent started or stopped.
type AgentSubagent struct {
	Phase    SubagentPhase `json:"phase"`
	AgentKey string        `json:"agent_key"`
}

func (*AgentSubagent) Kind() Kind        { return KindAgentSubagent }
func (p *AgentSubagent) Validate() error { return validate(p.check) }

func (p *AgentSubagent) check(c *checker) {
	checkEnum(c, "phase", subagentPhases, p.Phase)
	c.key("agent_key", p.AgentKey)
}

// EditorActivity is editor.activity: an edit, save, navigation or other act
// in an editor, usually from a WakaTime heartbeat.
type EditorActivity struct {
	// Editor names the editor as a key, such as "vscode" or "neovim".
	Editor string    `json:"editor"`
	Act    EditorAct `json:"act"`
	// FileHash is the file's path as an opaque key keyed per device: an
	// HMAC under a device key, in the same form as agent.tool's files,
	// never a plain hash a reader could recompute from a guessed path
	// (DESIGN 4.1 E-10, 5.7).
	FileHash     string     `json:"file_hash,omitempty"`
	LinesChanged int        `json:"lines_changed"`
	EditSource   EditSource `json:"edit_source"`
}

func (*EditorActivity) Kind() Kind        { return KindEditorActivity }
func (p *EditorActivity) Validate() error { return validate(p.check) }

func (p *EditorActivity) check(c *checker) {
	c.key("editor", p.Editor)
	checkEnum(c, "act", editorActs, p.Act)
	c.optKey("file_hash", p.FileHash)
	c.count("lines_changed", p.LinesChanged)
	checkEnum(c, "edit_source", editSources, p.EditSource)
}

// ShellCommand is shell.command: a command run in a shell.
type ShellCommand struct {
	CmdClass CmdClass `json:"cmd_class"`
	CmdHash  string   `json:"cmd_hash,omitempty" pairtime:"local"`
	// ExitCode is nil when the source doesn't record one, as zsh and fish
	// history files don't.
	ExitCode *int64 `json:"exit_code,omitempty"`
	TStart   int64  `json:"t_start"`
	// TEnd equals TStart when the source doesn't record how long the
	// command ran.
	TEnd        int64 `json:"t_end"`
	Interactive bool  `json:"interactive"`
	// ErrorSig is the signature of an error in the command's output, as an
	// opaque key in agent.tool's form: HMAC(K_feat, signature) truncated to
	// 16 bytes (DESIGN 3.6, 4.1 E-10).
	ErrorSig string `json:"error_sig,omitempty"`
}

func (*ShellCommand) Kind() Kind        { return KindShellCommand }
func (p *ShellCommand) Validate() error { return validate(p.check) }

func (p *ShellCommand) check(c *checker) {
	checkEnum(c, "cmd_class", cmdClasses, p.CmdClass)
	c.optKey("cmd_hash", p.CmdHash)
	c.span("t_start", p.TStart, "t_end", p.TEnd)
	c.optKey("error_sig", p.ErrorSig)
}

// GitChange is git.change: a commit, checkout, push or other change seen in
// a repo's .git directory.
type GitChange struct {
	Op GitOp `json:"op"`
	// SHA is the commit the operation produced or moved HEAD to.
	SHA          string   `json:"sha"`
	Parents      []string `json:"parents,omitempty"`
	FilesChanged int      `json:"files_changed"`
	Insertions   int      `json:"insertions"`
	Deletions    int      `json:"deletions"`
	Actor        Actor    `json:"actor"`
	// AgentTrailer names the agent a commit trailer credits, as a key such
	// as "claude" or "aider" (HS 13.3). Never the raw trailer, which can
	// carry a person's name and email address.
	AgentTrailer string `json:"agent_trailer,omitempty"`
}

func (*GitChange) Kind() Kind        { return KindGitChange }
func (p *GitChange) Validate() error { return validate(p.check) }

func (p *GitChange) check(c *checker) {
	checkEnum(c, "op", gitOps, p.Op)
	if !isGitID(p.SHA) {
		c.addf("sha", "must be 40 or 64 lowercase hex digits, got %q", p.SHA)
	}
	if len(p.Parents) > maxList {
		c.addf("parents", "has %d entries, more than %d", len(p.Parents), maxList)
	} else {
		for i, sha := range p.Parents {
			if !isGitID(sha) {
				c.addf(fieldIndex("parents", i), "must be 40 or 64 lowercase hex digits, got %q", sha)
				break
			}
		}
	}
	c.count("files_changed", p.FilesChanged)
	c.count("insertions", p.Insertions)
	c.count("deletions", p.Deletions)
	checkEnum(c, "actor", actors, p.Actor)
	c.optKey("agent_trailer", p.AgentTrailer)
}

// OSPresence is os.presence: one idle-time sample, taken every 15 seconds.
type OSPresence struct {
	// IdleAnyS is the seconds since the last keyboard or mouse input.
	IdleAnyS float64 `json:"idle_any_s"`
	// IdleKeysS is the seconds since the last key press, or nil where the
	// OS can't tell keys from other input. It is never less than IdleAnyS,
	// give or take the second between the two readings.
	IdleKeysS *float64 `json:"idle_keys_s"`
	Locked    bool     `json:"locked"`
}

func (*OSPresence) Kind() Kind        { return KindOSPresence }
func (p *OSPresence) Validate() error { return validate(p.check) }

func (p *OSPresence) check(c *checker) {
	c.seconds("idle_any_s", p.IdleAnyS)
	if p.IdleKeysS != nil {
		c.seconds("idle_keys_s", *p.IdleKeysS)
		if *p.IdleKeysS < p.IdleAnyS-1 {
			c.addf("idle_keys_s", "is %v, less than idle_any_s (%v); a key press is also input", *p.IdleKeysS, p.IdleAnyS)
		}
	}
}

// OSFocus is os.focus: the foreground app changed, or the client can no
// longer tell which window is in front. Then AppClass is AppUnknown and the
// record has no other field; it holds until the next window report (SM
// 2.5).
type OSFocus struct {
	AppClass AppClass `json:"app_class"`
	// AppHash is a per-device hash of the focused app's identity.
	AppHash string `json:"app_hash,omitempty" pairtime:"local"`
	// HostedSessions are the session keys of harness processes running
	// under the focused window's process.
	HostedSessions []string `json:"hosted_sessions,omitempty"`
	// FocusProject is the project the focused window resolves to, if any.
	FocusProject string `json:"focus_project,omitempty"`
}

func (*OSFocus) Kind() Kind        { return KindOSFocus }
func (p *OSFocus) Validate() error { return validate(p.check) }

func (p *OSFocus) check(c *checker) {
	checkEnum(c, "app_class", appClasses, p.AppClass)
	c.optKey("app_hash", p.AppHash)
	c.keys("hosted_sessions", p.HostedSessions)
	c.optKey("focus_project", p.FocusProject)
	if p.AppClass != AppUnknown {
		return
	}
	// An unknown record knows no window, so nothing can describe one. The
	// values aren't echoed: app_hash is local.
	if p.AppHash != "" {
		c.addf("app_hash", "is set, but an %q record can't tell which app is in front, so it has none", AppUnknown)
	}
	if len(p.HostedSessions) > 0 {
		c.addf("hosted_sessions", "is set, but an %q record can't tell which window is in front, so it hosts no session", AppUnknown)
	}
	if p.FocusProject != "" {
		c.addf("focus_project", "is set, but an %q record can't tell which window is in front, so no project resolves from it", AppUnknown)
	}
}

// OSPower is os.power: the machine suspended, resumed, locked or unlocked.
type OSPower struct {
	Op PowerOp `json:"op"`
}

func (*OSPower) Kind() Kind        { return KindOSPower }
func (p *OSPower) Validate() error { return validate(p.check) }

func (p *OSPower) check(c *checker) {
	checkEnum(c, "op", powerOps, p.Op)
}

// OTelActiveTime is otel.active_time: Claude Code's own active-time metric.
// It calibrates the scorer and is never credit (SM 1.2).
type OTelActiveTime struct {
	Type    ActiveTimeType `json:"type"`
	Seconds float64        `json:"seconds"`
}

func (*OTelActiveTime) Kind() Kind        { return KindOTelActiveTime }
func (p *OTelActiveTime) Validate() error { return validate(p.check) }

func (p *OTelActiveTime) check(c *checker) {
	checkEnum(c, "type", activeTimeTypes, p.Type)
	c.seconds("seconds", p.Seconds)
}

// CLITrack is cli.track: a coding agent ran `pairtime track`, following the
// AGENTS.md entry (DESIGN 5.4). It is the model's own claim. It opens an
// anchor only under DESIGN 5.4's four conditions, at level C unless
// keyboard input in the host window in the 10 s before the call makes it
// level A (DESIGN 5.4, 6.2 D3), and it never counts toward logged_s or
// ai_logged_s (D19). When the caller's
// process chain reaches a harness session, the agent fills harness and
// session_key from it (DESIGN 4.1 E-8).
type CLITrack struct {
	// Label is the text the model passed, on one line: the user's message
	// with personal information removed, at most MaxLabelLen characters.
	// It is prompt text, so it is local. The agent redacts it again with
	// pkg/redact, keeps it in the vault and commits it in the ledger
	// (DESIGN 5.4).
	Label string `json:"label,omitempty" pairtime:"local"`
	// TextLen is the number of characters the model passed. Unlike the
	// label it is shareable, so the length the anchor was classed by
	// (DESIGN 5.4) survives Shareable.
	TextLen int `json:"text_len"`
	// EnvHints are the harness variables set in the caller's environment,
	// by name and never by value (DESIGN 4.1 E-8): each one of the closed
	// list of EnvHint names, at most once.
	EnvHints []EnvHint `json:"env_hints,omitempty"`
	// LogMatch says whether a pairtime log reader holds the user entry the
	// call copies (DESIGN 5.2, 5.4).
	LogMatch LogMatch `json:"log_match"`
}

func (*CLITrack) Kind() Kind        { return KindCLITrack }
func (p *CLITrack) Validate() error { return validate(p.check) }

func (p *CLITrack) check(c *checker) {
	c.text("label", p.Label, MaxLabelLen)
	c.count("text_len", p.TextLen)
	if len(p.EnvHints) > maxList {
		c.addf("env_hints", "has %d entries, more than %d", len(p.EnvHints), maxList)
	} else {
		for i, name := range p.EnvHints {
			// The message leaves out the entry: one that isn't a listed
			// name may be a variable's value, such as a token.
			if !envHints.has(name) {
				c.addf(fieldIndex("env_hints", i), "must be one of the harness variable names %q, never a value", envHints.values)
				break
			}
			if slices.Contains(p.EnvHints[:i], name) {
				c.addf(fieldIndex("env_hints", i), "repeats an earlier name")
				break
			}
		}
	}
	checkEnum(c, "log_match", logMatches, p.LogMatch)
}

// RelayMessage is relay.message: a message an agent app's own store says a
// person sent, which the app's reader read on the device (SM 1.2, DESIGN
// 5.1). It is the app's claim. It witnesses the prompt.submit it pairs
// with (SM 2.2 rule 9), it opens no anchor unless it pairs, and it never
// counts toward logged_s (DESIGN 6.2 D19). It holds no text and none of the
// app's own ids, names or folders: the thread is an opaque key keyed per
// device, and the envelope's session_key is the harness session the app
// bound the message's run to, or none when the reader didn't know it.
// Nothing in it is local, so all of it is shareable.
type RelayMessage struct {
	App RelayApp `json:"app"`
	// Client is the app's word for where the message was typed: for T3
	// Code, its creationSource. A message from the app's phone app skips SM
	// 2.2 rules 6 and 7.
	Client RelayClient `json:"client"`
	// SentAt is the app's own stamp of the message, in Unix milliseconds:
	// when the app took it from the person. A relayed anchor stands there,
	// and its lead ends there (SM 2.3).
	SentAt int64 `json:"sent_at"`
	// ThreadKey is the app's thread as an opaque key keyed per device: an
	// HMAC under a device key, never the app's own id, which isKey would
	// pass (DESIGN 4.1 E-12). A relayed lead is capped at the seconds since
	// the thread's previous message (SM 2.3).
	ThreadKey string `json:"thread_key"`
	// Declared is what the app says sent the message: none for a person,
	// or its scheduler or another thread (SM 2.2).
	Declared RelayDeclared `json:"declared"`
	// LenChars is the typed length of the app's own text with the app's
	// machine strings removed (SM 2.2): an upper bound, since the app keeps
	// no paste flag.
	LenChars int `json:"len_chars"`
	// LowEffort is SM 2.2's go-ahead test on the app's own text.
	LowEffort bool `json:"low_effort"`
	// Prompt is the event id of the prompt.submit the agent paired the
	// message with, when that prompt came first; otherwise the prompt's
	// relay names this record (SM 2.2 rule 9). Like relay, it leaves the
	// device only in tier-2 ledger bodies.
	Prompt string `json:"prompt,omitempty"`
}

func (*RelayMessage) Kind() Kind        { return KindRelayMessage }
func (p *RelayMessage) Validate() error { return validate(p.check) }

func (p *RelayMessage) check(c *checker) {
	checkEnum(c, "app", relayApps, p.App)
	checkEnum(c, "client", relayClients, p.Client)
	c.unixMS("sent_at", p.SentAt)
	c.key("thread_key", p.ThreadKey)
	checkEnum(c, "declared", relayDeclareds, p.Declared)
	c.count("len_chars", p.LenChars)
	c.optEventID("prompt", p.Prompt)
}
