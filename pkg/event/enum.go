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

// Source is the kind of adapter that produced an event (SM 1.1).
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
	ParentAgent   ParentClass = "agent"
	ParentUnknown ParentClass = "unknown"
)

var parentClasses = enum[ParentClass]{name: "parent_class", values: []ParentClass{
	ParentTerminalApp, ParentIDE, ParentMultiplexer, ParentRemote,
	ParentScriptRuntime, ParentScheduler, ParentAgent, ParentUnknown,
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
)

var appClasses = enum[AppClass]{name: "app_class", values: []AppClass{
	AppTerminal, AppEditor, AppAgentApp, AppBrowser, AppChatApp, AppOther, AppNone,
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
