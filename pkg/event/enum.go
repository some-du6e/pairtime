// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import "slices"

// enum is the closed set of values of one string type. name is the type's
// key under "$defs" in docs/schema/event.schema.json, or "" for the enums
// that only derived records use, which the event schema doesn't describe.
type enum[T ~string] struct {
	name   string
	values []T
}

func (e enum[T]) has(v T) bool { return slices.Contains(e.values, v) }

// Source is the kind of adapter or tool the record says produced it (SM
// 1.1). It is the record's own claim. [Via] is how the pairtime agent
// actually received it, and the two can differ: a plugin that posts
// events claiming source "hook" over HTTP has via "plugin".
type Source string

const (
	SourceHook   Source = "hook"
	SourceLog    Source = "log"
	SourceOTel   Source = "otel"
	SourceOS     Source = "os"
	SourceEditor Source = "editor"
	SourceShell  Source = "shell"
	SourceGit    Source = "git"
	// SourceCLI is the AGENTS.md fallback, `pairtime track`.
	SourceCLI Source = "cli"
	SourceMCP Source = "mcp"
)

var sources = enum[Source]{name: "source", values: []Source{
	SourceHook, SourceLog, SourceOTel, SourceOS, SourceEditor, SourceShell, SourceGit, SourceCLI, SourceMCP,
}}

// Via is how the pairtime agent received a record (DESIGN 4.1 E-2). The
// agent sets it from the path the record came in by and overwrites
// whatever a sender wrote, so a sender can't choose its own via. An
// anchor's evidence level depends on it (DESIGN 6.4), and only records
// with via hook, log, otel, editor or bridge count toward logged_s,
// ai_logged_s and hackatime_logged_s (DESIGN 6.2 D19). A track, plugin or
// bridge record whose entry a pairtime log reader also holds opens no
// anchor of its own. The log record's anchor counts instead, and the
// side-door record is a cross-check (DESIGN 5.2, 6.4).
type Via string

const (
	// ViaHook is a hook the harness ran, delivered over the socket or the
	// pipe: one of its native hooks, or `pairtime hook` and `pairtime
	// emit`, whose parent chain the agent walks from the connection's peer
	// to a host the adapter, mapping or plugin lists (DESIGN 3.4, 5.3). A
	// chain the sender recorded itself never makes a record ViaHook
	// (DESIGN 5.2).
	ViaHook Via = "hook"
	// ViaLog is one of pairtime's log readers: a transcript, rollout or
	// history file the harness wrote.
	ViaLog Via = "log"
	// ViaOTel is the agent's OTLP receiver.
	ViaOTel Via = "otel"
	// ViaOS is the agent's own presence, focus, power and process watchers.
	ViaOS Via = "os"
	// ViaGit is the agent's .git watcher.
	ViaGit Via = "git"
	// ViaShell is a shell integration.
	ViaShell Via = "shell"
	// ViaEditor is an editor heartbeat that passed the peer-process check:
	// the connection came from wakatime-cli, whose parent chain reaches an
	// editor (DESIGN 5.7). macOS skips the check, and for now its editor
	// heartbeats get ViaEditor (docs/errata/2026-11-19-bridge-unverified.md).
	ViaEditor Via = "editor"
	// ViaEditorUnverified is an editor heartbeat that failed the
	// peer-process check. Its anchors are level C and it never counts
	// toward logged_s (DESIGN 5.2, 6.4, D19).
	ViaEditorUnverified Via = "editor_unverified"
	// ViaBridge is a record the WakaTime bridge made from a
	// transcript-derived heartbeat that passed the peer-process check
	// (DESIGN 5.7). Its anchors are level C unless keyboard input in the
	// host window in the 10 s before them makes them level A (DESIGN 5.2,
	// 6.4).
	ViaBridge Via = "bridge"
	// ViaBridgeUnverified is a bridge record from a heartbeat that failed
	// the peer-process check, and for now every bridge record on macOS,
	// where the check doesn't run. Like ViaBridge, its anchors are level C
	// unless keyboard input in the host window in the 10 s before them
	// makes them level A, and like ViaEditorUnverified it never counts
	// toward logged_s. DESIGN 4.1 E-2 has no value for it;
	// docs/errata/2026-11-19-bridge-unverified.md records why it was added
	// and why macOS gets it.
	ViaBridgeUnverified Via = "bridge_unverified"
	// ViaTrack is `pairtime track`, called by an agent following the
	// AGENTS.md entry (DESIGN 5.4), over the socket or through the inbox
	// spool (DESIGN 5.2). Like ViaBridge, its anchors are level C
	// unless keyboard input in the host window in the 10 s before them
	// makes them level A, and it never counts toward logged_s.
	ViaTrack Via = "track"
	// ViaPlugin is a plugin posting over HTTP or writing to inbox/<id>/; a
	// hook, `pairtime emit` or generic hook mode call over the socket or
	// the pipe whose walked parent chain reaches no listed host; and every
	// such call that reached the agent through the inbox spool, whatever
	// parent chain it recorded (DESIGN 3.4, 5.2, 5.3). Like ViaBridge, its
	// anchors are level C unless keyboard input in the host window in the
	// 10 s before them makes them level A, and it never counts toward
	// logged_s.
	ViaPlugin Via = "plugin"
	// ViaApp is an agent app's reader: pairtime reading the app's own
	// store on the device, such as T3 Code's message store (DESIGN 3.4,
	// 5.1). It writes relay.message records, and no other kind arrives by
	// it (exclusiveVias). Like track and plugin records, its records never
	// count toward logged_s, ai_logged_s or hackatime_logged_s (DESIGN 6.2
	// D19). A relay record opens no anchor unless it pairs with a prompt,
	// and the anchor of a prompt it witnesses that scores as relayed is the
	// record itself, at level C whatever backs it (SM 2.2 rule 9, 2.3).
	ViaApp Via = "app"
)

var vias = enum[Via]{name: "via", values: []Via{
	ViaHook, ViaLog, ViaOTel, ViaOS, ViaGit, ViaShell, ViaEditor, ViaEditorUnverified,
	ViaBridge, ViaBridgeUnverified, ViaTrack, ViaPlugin, ViaApp,
}}

// Harness is the AI coding tool behind an event (SM 1.1).
type Harness string

const (
	HarnessClaudeCode Harness = "claude_code"
	HarnessCursor     Harness = "cursor"
	HarnessCopilot    Harness = "copilot"
	HarnessCodex      Harness = "codex"
	HarnessGemini     Harness = "gemini"
	HarnessDevin      Harness = "devin"
	HarnessCline      Harness = "cline"
	HarnessOpenCode   Harness = "opencode"
	// HarnessOther is an agent pairtime has no constant for yet, such as
	// one it only hears from through `pairtime track`. Whoever writes that
	// agent's adapter adds its constant here and in the schema.
	HarnessOther Harness = "other"
	// HarnessNone marks events no harness produced: OS and git events.
	HarnessNone Harness = "none"
)

var harnesses = enum[Harness]{name: "harness", values: []Harness{
	HarnessClaudeCode, HarnessCursor, HarnessCopilot, HarnessCodex, HarnessGemini,
	HarnessDevin, HarnessCline, HarnessOpenCode, HarnessOther, HarnessNone,
}}

// Harnesses returns every harness, HarnessNone last, in the schema's order.
func Harnesses() []Harness { return slices.Clone(harnesses.values) }

// StartKind says how a harness session began (session.start).
type StartKind string

const (
	StartStartup StartKind = "startup"
	StartResume  StartKind = "resume"
	StartClear   StartKind = "clear"
	StartCompact StartKind = "compact"
	StartFork    StartKind = "fork"
)

var startKinds = enum[StartKind]{name: "start_kind", values: []StartKind{
	StartStartup, StartResume, StartClear, StartCompact, StartFork,
}}

// Entrypoint is how the harness was launched (session.start).
type Entrypoint string

const (
	EntrypointCLI     Entrypoint = "cli"
	EntrypointIDE     Entrypoint = "ide"
	EntrypointDesktop Entrypoint = "desktop"
	EntrypointSDK     Entrypoint = "sdk"
	EntrypointExec    Entrypoint = "exec"
	EntrypointMCP     Entrypoint = "mcp"
	EntrypointCloud   Entrypoint = "cloud"
	EntrypointUnknown Entrypoint = "unknown"
)

var entrypoints = enum[Entrypoint]{name: "entrypoint", values: []Entrypoint{
	EntrypointCLI, EntrypointIDE, EntrypointDesktop, EntrypointSDK,
	EntrypointExec, EntrypointMCP, EntrypointCloud, EntrypointUnknown,
}}

// ParentClass classes the nearest ancestor of a harness process that isn't
// a plain interactive shell (session.start, SM 2.2 rule 4).
type ParentClass string

const (
	ParentTerminalApp ParentClass = "terminal_app"
	ParentIDE         ParentClass = "ide"
	// ParentMultiplexer is tmux, screen and the like.
	ParentMultiplexer ParentClass = "multiplexer"
	// ParentRemote is sshd.
	ParentRemote ParentClass = "remote"
	// ParentScriptRuntime is python, a non-IDE node, or a shell running a
	// script file.
	ParentScriptRuntime ParentClass = "script_runtime"
	// ParentScheduler is cron, systemd, launchd or Task Scheduler.
	ParentScheduler ParentClass = "scheduler"
	// ParentAgent is another harness.
	ParentAgent ParentClass = "agent"
	// ParentAgentApp is an agent app's own server process, which runs the
	// harness for the app's clients, such as T3 Code's `t3 serve` (DESIGN
	// 5.1). SM 2.2 rule 4 takes every prompt of its sessions unless an app
	// message witnesses it (rule 9). The class comes from the process's
	// name, which a script can set, so it sorts honest setups and bounds
	// nothing on its own.
	ParentAgentApp ParentClass = "agent_app"
	ParentUnknown  ParentClass = "unknown"
)

var parentClasses = enum[ParentClass]{name: "parent_class", values: []ParentClass{
	ParentTerminalApp, ParentIDE, ParentMultiplexer, ParentRemote,
	ParentScriptRuntime, ParentScheduler, ParentAgent, ParentAgentApp, ParentUnknown,
}}

// DeclaredOrigin is what the harness itself says started a prompt
// (prompt.submit). The scorer's own verdict is [Origin].
type DeclaredOrigin string

const (
	DeclaredCron         DeclaredOrigin = "cron"
	DeclaredLoop         DeclaredOrigin = "loop"
	DeclaredGoal         DeclaredOrigin = "goal"
	DeclaredSubagent     DeclaredOrigin = "subagent"
	DeclaredCrossSession DeclaredOrigin = "cross_session"
	DeclaredFollowup     DeclaredOrigin = "followup"
	DeclaredNone         DeclaredOrigin = "none"
)

var declaredOrigins = enum[DeclaredOrigin]{name: "origin_declared", values: []DeclaredOrigin{
	DeclaredCron, DeclaredLoop, DeclaredGoal, DeclaredSubagent,
	DeclaredCrossSession, DeclaredFollowup, DeclaredNone,
}}

// ToolClass sorts an agent's tool call (agent.tool).
type ToolClass string

const (
	ToolEdit     ToolClass = "edit"
	ToolWrite    ToolClass = "write"
	ToolRead     ToolClass = "read"
	ToolBash     ToolClass = "bash"
	ToolTest     ToolClass = "test"
	ToolBuild    ToolClass = "build"
	ToolSearch   ToolClass = "search"
	ToolWeb      ToolClass = "web"
	ToolSubagent ToolClass = "subagent"
	ToolOther    ToolClass = "other"
)

var toolClasses = enum[ToolClass]{name: "tool_class", values: []ToolClass{
	ToolEdit, ToolWrite, ToolRead, ToolBash, ToolTest,
	ToolBuild, ToolSearch, ToolWeb, ToolSubagent, ToolOther,
}}

// Decision is the answer to a permission request (agent.permission).
type Decision string

const (
	DecisionAccept Decision = "accept"
	DecisionReject Decision = "reject"
	DecisionEdit   Decision = "edit"
	DecisionCancel Decision = "cancel"
)

var decisions = enum[Decision]{name: "decision", values: []Decision{
	DecisionAccept, DecisionReject, DecisionEdit, DecisionCancel,
}}

// DecidedBy says who answered a permission request (agent.permission).
type DecidedBy string

const (
	DecidedByUser   DecidedBy = "user"
	DecidedByConfig DecidedBy = "config"
	DecidedByHook   DecidedBy = "hook"
)

var decidedBys = enum[DecidedBy]{name: "decided_by", values: []DecidedBy{
	DecidedByUser, DecidedByConfig, DecidedByHook,
}}

// StopReason says why an agent turn stopped (agent.stop).
type StopReason string

const (
	StopEndTurn     StopReason = "end_turn"
	StopQuestion    StopReason = "question"
	StopInterrupted StopReason = "interrupted"
	StopError       StopReason = "error"
	StopLimit       StopReason = "limit"
)

var stopReasons = enum[StopReason]{name: "stop_reason", values: []StopReason{
	StopEndTurn, StopQuestion, StopInterrupted, StopError, StopLimit,
}}

// SubagentPhase is the start or stop of a subagent (agent.subagent).
type SubagentPhase string

const (
	SubagentStart SubagentPhase = "start"
	SubagentStop  SubagentPhase = "stop"
)

var subagentPhases = enum[SubagentPhase]{name: "subagent_phase", values: []SubagentPhase{
	SubagentStart, SubagentStop,
}}

// EditorAct is what happened in an editor (editor.activity).
type EditorAct string

const (
	ActEdit     EditorAct = "edit"
	ActNavigate EditorAct = "navigate"
	ActSave     EditorAct = "save"
	ActOpen     EditorAct = "open"
	ActDebug    EditorAct = "debug"
	ActTask     EditorAct = "task"
)

var editorActs = enum[EditorAct]{name: "editor_act", values: []EditorAct{
	ActEdit, ActNavigate, ActSave, ActOpen, ActDebug, ActTask,
}}

// EditSource says who or what made an editor change (editor.activity).
type EditSource string

const (
	EditSourceHuman EditSource = "human"
	// EditSourceTab is an accepted tab completion.
	EditSourceTab     EditSource = "tab"
	EditSourceAgent   EditSource = "agent"
	EditSourcePaste   EditSource = "paste"
	EditSourceUnknown EditSource = "unknown"
)

var editSources = enum[EditSource]{name: "edit_source", values: []EditSource{
	EditSourceHuman, EditSourceTab, EditSourceAgent, EditSourcePaste, EditSourceUnknown,
}}

// CmdClass sorts a shell command (shell.command).
type CmdClass string

const (
	CmdTest    CmdClass = "test"
	CmdBuild   CmdClass = "build"
	CmdRun     CmdClass = "run"
	CmdGit     CmdClass = "git"
	CmdInstall CmdClass = "install"
	CmdOther   CmdClass = "other"
)

var cmdClasses = enum[CmdClass]{name: "cmd_class", values: []CmdClass{
	CmdTest, CmdBuild, CmdRun, CmdGit, CmdInstall, CmdOther,
}}

// GitOp is the git operation behind a git.change event.
type GitOp string

const (
	GitCommit   GitOp = "commit"
	GitAmend    GitOp = "amend"
	GitRebase   GitOp = "rebase"
	GitReset    GitOp = "reset"
	GitCheckout GitOp = "checkout"
	GitMerge    GitOp = "merge"
	GitRevert   GitOp = "revert"
	GitPush     GitOp = "push"
	GitStash    GitOp = "stash"
)

var gitOps = enum[GitOp]{name: "git_op", values: []GitOp{
	GitCommit, GitAmend, GitRebase, GitReset, GitCheckout, GitMerge, GitRevert, GitPush, GitStash,
}}

// Actor says who ran a git operation (git.change). It is ActorAgent when an
// agent's bash tool ran the same git command within 2 seconds.
type Actor string

const (
	ActorHuman   Actor = "human"
	ActorAgent   Actor = "agent"
	ActorUnknown Actor = "unknown"
)

var actors = enum[Actor]{name: "actor", values: []Actor{
	ActorHuman, ActorAgent, ActorUnknown,
}}

// Actors returns every actor, in the schema's order.
func Actors() []Actor { return slices.Clone(actors.values) }

// AppClass sorts the app that has focus (os.focus, and a slot's
// focus_class).
type AppClass string

const (
	AppTerminal AppClass = "terminal"
	AppEditor   AppClass = "editor"
	// AppAgentApp is a desktop agent app, such as Claude Desktop's Code tab.
	AppAgentApp AppClass = "agent_app"
	AppBrowser  AppClass = "browser"
	AppChatApp  AppClass = "chat_app"
	AppOther    AppClass = "other"
	AppNone     AppClass = "none"
	// AppUnknown is focus the client can't see: it can't tell which window
	// is in front (SM 2.5). An os.focus record with it names no app, hosts
	// no session and resolves to no project, and a slot it holds longest
	// has no focus_project.
	AppUnknown AppClass = "unknown"
)

var appClasses = enum[AppClass]{name: "app_class", values: []AppClass{
	AppTerminal, AppEditor, AppAgentApp, AppBrowser, AppChatApp, AppOther, AppNone, AppUnknown,
}}

// PowerOp is a suspend, resume, lock or unlock (os.power).
type PowerOp string

const (
	PowerSuspend PowerOp = "suspend"
	PowerResume  PowerOp = "resume"
	PowerLock    PowerOp = "lock"
	PowerUnlock  PowerOp = "unlock"
)

var powerOps = enum[PowerOp]{name: "power_op", values: []PowerOp{
	PowerSuspend, PowerResume, PowerLock, PowerUnlock,
}}

// ActiveTimeType is the type attribute of Claude Code's
// claude_code.active_time.total metric (otel.active_time).
type ActiveTimeType string

const (
	ActiveTimeUser ActiveTimeType = "user"
	ActiveTimeCLI  ActiveTimeType = "cli"
)

var activeTimeTypes = enum[ActiveTimeType]{name: "active_time_type", values: []ActiveTimeType{
	ActiveTimeUser, ActiveTimeCLI,
}}

// LogMatch says whether a pairtime log reader holds the user entry that a
// `pairtime track` call copies (cli.track; DESIGN 5.2, 5.4).
type LogMatch string

const (
	// LogMatched means a log reader holds the matching user entry from the
	// same session within TRACK_LOG_MATCH_S (10 s). The log record's own
	// anchor counts, and the track call is only a cross-check.
	LogMatched LogMatch = "matched"
	// LogUnmatched means a log reader covers the session but held no
	// matching entry when the agent recorded the call. The scorer matches
	// again inside the pending window, so a line the tailer reads late
	// still matches (DESIGN 5.4).
	LogUnmatched LogMatch = "unmatched"
	// LogNoReader means no pairtime log reader covers the caller's session.
	LogNoReader LogMatch = "no_reader"
)

var logMatches = enum[LogMatch]{name: "log_match", values: []LogMatch{
	LogMatched, LogUnmatched, LogNoReader,
}}

// EnvHint is the name of a harness environment variable that `pairtime
// track` looks for in its caller's environment (cli.track's env_hints,
// DESIGN 4.1 E-8). The list is closed, so a variable's value can't pass as
// a name, and env_hints can carry only these names off the device. A
// collector that looks for another variable adds its constant here and in
// the schema, as with Harness.
type EnvHint string

const (
	// EnvClaudeCode is CLAUDECODE. Claude Code sets it in the processes it
	// spawns, but IDE extensions also set it in their integrated
	// terminals, where a person types, so on its own it doesn't show that
	// Claude Code made the call (HS 11.3, 12.1).
	EnvClaudeCode EnvHint = "CLAUDECODE"
	// EnvClaudeCodeChildSession is CLAUDE_CODE_CHILD_SESSION, which Claude
	// Code sets in the processes its Bash, PowerShell and Monitor tools,
	// hooks and status line spawn, but not in stdio MCP servers (HS 12.1).
	EnvClaudeCodeChildSession EnvHint = "CLAUDE_CODE_CHILD_SESSION"
	// EnvClaudeCodeSessionID is CLAUDE_CODE_SESSION_ID, which Claude Code
	// sets in its tool, hook and stdio MCP subprocesses (HS 11.3).
	EnvClaudeCodeSessionID EnvHint = "CLAUDE_CODE_SESSION_ID"
)

var envHints = enum[EnvHint]{name: "env_hint", values: []EnvHint{
	EnvClaudeCode, EnvClaudeCodeChildSession, EnvClaudeCodeSessionID,
}}

// RelayApp is the agent app whose own store a relay.message came from (SM
// 1.2). An app's reader adds its constant here and in the schema.
type RelayApp string

const (
	// RelayAppT3Code is T3 Code (pingdotgg/t3code), read from its message
	// store (DESIGN 5.1).
	RelayAppT3Code RelayApp = "t3_code"
)

var relayApps = enum[RelayApp]{name: "relay_app", values: []RelayApp{RelayAppT3Code}}

// RelayClient is the app's own word for where a relayed message was typed
// (relay.message): for T3 Code, the message's creationSource (SM 1.2,
// 2.2).
type RelayClient string

const (
	// RelayClientWeb is the app's web client, which can run on this
	// computer or any other, so it says nothing about which device typed.
	RelayClientWeb RelayClient = "web"
	// RelayClientMobile is the app's phone app. A message typed there skips
	// SM 2.2 rules 6 and 7, since no input on this device typed it.
	RelayClientMobile RelayClient = "mobile"
)

var relayClients = enum[RelayClient]{name: "relay_client", values: []RelayClient{
	RelayClientWeb, RelayClientMobile,
}}

// RelayDeclared is what the app says sent a relayed message
// (relay.message, SM 1.2, 2.2). The scorer's verdict on the prompt it pairs
// with is that prompt's [Origin].
type RelayDeclared string

const (
	// RelayDeclaredNone is a message the app says a person sent. Only such a
	// record witnesses a prompt (SM 2.2 rule 9).
	RelayDeclaredNone RelayDeclared = "none"
	// RelayDeclaredCron is the app's scheduler or a webhook. The prompt it
	// pairs with is scheduled (SM 2.2 rule 1).
	RelayDeclaredCron RelayDeclared = "cron"
	// RelayDeclaredCrossSession is a message from another thread. The prompt
	// it pairs with is a cross-session subagent's (SM 2.2 rule 3).
	RelayDeclaredCrossSession RelayDeclared = "cross_session"
)

var relayDeclareds = enum[RelayDeclared]{name: "relay_declared", values: []RelayDeclared{
	RelayDeclaredNone, RelayDeclaredCron, RelayDeclaredCrossSession,
}}
