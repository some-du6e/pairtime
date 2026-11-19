# Raw notes: Signals that AI-assisted building leaves, and how to capture them

These are unverified notes from a research agent that was cancelled on 2026-11-19, before it wrote its doc. Work on pairtime was paused. The orchestrator extracted them from the agent transcript so the work isn't lost. Fetched-page summaries were written by the fetch tool and have not been fact-checked. Treat them as leads, not findings.

## Web searches

- "Cursor agent transcripts "agent-transcripts" folder location transcript_path jsonl"
  - [@tracebench/adapter-cursor](https://www.npmjs.com/package/@tracebench/adapter-cursor)
  - [scanner/cursor: agent-transcripts now uses .jsonl in nested <id>/ subdir, 0 sessions surfaced](https://github.com/subinium/agf/issues/35)
  - [cursor_theater.py](https://github.com/udah1/cursor-theater/blob/main/cursor_theater.py)
  - [cursor.rs - source](https://docs.rs/ctx-agent/latest/src/ctx/surface/cursor.rs.html)
  - [Cursor CLI changelog | Cursor Docs](https://cursor.com/docs/cli/changelog)
- "Cursor state.vscdb cursorDiskKV composerData bubbleId createdAt timingInfo parse chat history 2026"
  - [Reading Cursor's chat history out of SQLite — session-viz](https://session-viz.com/blog/reading-cursors-chat-history/)
  - [Cursor chat history: where it lives and what's in it | ranwhat](https://ranwhat.com/guides/cursor-chat-history)
  - [Where Cursor stores its history — session format](https://vshulcz.github.io/deja-vu/registry/cursor.html)
  - [cursor_desktop.rs - source](https://docs.rs/txcript/latest/src/txcript/harness/cursor_desktop.rs.html)
  - [farique/cursor-chat-explorer](https://github.com/farique/cursor-chat-explorer)
- "GitHub Copilot CLI hooks configuration userPromptSubmitted sessionStart preToolUse postToolUse docs.github.com"
  - [Using hooks with GitHub Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-hooks)
  - [Rolling out Copilot CLI hooks for your teams](https://docs.github.com/en/copilot/tutorials/copilot-cli-hooks)
  - [GitHub Copilot hooks reference](https://docs.github.com/en/copilot/reference/hooks-reference)
  - [https://docs.github.com/en/copilot/concepts/agents/hooks](https://docs.github.com/en/copilot/concepts/agents/hooks)
  - [docs/workshop/09-hooks.md](https://github.com/copilot-dev-days/copilot-cli-deep-dive-workshop/blob/main/docs/workshop/09-hooks.md)
- ""session-state" copilot cli "events.jsonl" "user.message" OR "session.start" event types parse"
  - [Where GitHub Copilot CLI Stores Sessions on macOS](https://jazzyalex.github.io/agent-sessions/guides/copilot-cli-local-history.html)
  - [docs/architecture.md](https://github.com/microsoft/mycopilotworks/blob/main/docs/architecture.md)
  - [Where Copilot CLI stores its history — session format](https://vshulcz.github.io/deja-vu/registry/copilot.html)
  - [CopilotJsonlParser in atim_parser::copilot_jsonl - Rust](https://docs.rs/atim-parser/latest/atim_parser/copilot_jsonl/struct.CopilotJsonlParser.html)
  - [harvest.py](https://github.com/microsoft/What-I-Did-Copilot/blob/main/harvest.py)

## Pages fetched, with the fetch tool's summary

### https://code.claude.com/docs/en/hooks

Asked: List every hook event name with when it fires, and the full JSON input payload fields for each (common fields like session_id, transcript_path, cwd, permission_mode, hook_event_name, plus event-specific ones like prompt, tool_name, tool_input, tool_response, source, reason, etc). Also: where hooks a

> > **Caveat:** The page content stops before its per-event **"Hook events"** section, so the full input schema for each event isn't in the provided text. Everything below comes from the visible part, and gaps are marked.
>
> ### 1. Hook events (33)
>
> "—" means the event has no matcher support. A `matcher` on those events is silently ignored.
>
> | Event | When it fires | `matcher` filters on |
> |---|---|---|
> | `SessionStart` | Session begins or resumes | how it started: `startup`, `resume`, `clear`, `compact`, `fork` |
> | `Setup` | `--init-only`, or `--init`/`--maintenance` in `-p` mode (CI/script prep) | `init`, `maintenance` |
> | `UserPromptSubmit` | Prompt submitted, before Claude processes it. Also fires on turns Claude Code starts itself | — |
> | `UserPromptExpansion` | User-typed command expands into a prompt (can block) | command/skill name |
> | `PreToolUse` | Before a tool call executes (can block) | tool name |
> | `PermissionRequest` | Tool call needs a permission decision | tool name |
> | `PermissionDenied` | Auto mode denies a tool call | tool name |
> | `PostToolUse` | Tool call succeeded | tool name |
> | `PostToolUseFailure` | Tool call failed | tool name |
> | `PostToolBatch` | Batch of parallel tool calls resolved, before next model call | — |
> | `Notification` | Claude Code sends a notification | `permission_prompt`, `idle_prompt`, `auth_success`, `auth_storage_failure`, `elicitation_dialog`, `elicitation_url_dialog`, `elicitation_complete`, `elicitation_response`, …

### https://code.claude.com/docs/en/monitoring-usage

Asked: List every OpenTelemetry metric name and event name with their attributes, exactly as written. In particular: claude_code.active_time.total and its attributes (is there a 'type' attribute splitting user vs cli time? what are the exact values and their definitions?). Also list env vars to enable (CLA

> # Claude Code OpenTelemetry Reference
>
> > **Note:** The fetched page was cut off partway through the `claude_code.tool_decision` event. Later sections, such as the remaining events, usage monitoring, and the SIEM example, aren't covered here.
>
> ## 1. Metrics
>
> Every metric carries the standard attributes (§3) plus the attributes listed below.
>
> | Metric | Description | Unit | Additional attributes |
> |---|---|---|---|
> | `claude_code.session.count` | Count of sessions started | none | `start_type`: `"fresh"`, `"resume"`, `"continue"`, `"agents_view"` |
> | `claude_code.lines_of_code.count` | Count of lines of code modified | none | `type`: `"added"`, `"removed"`; `model` |
> | `claude_code.pull_request.count` | Number of pull requests created | none | none |
> | `claude_code.commit.count` | Number of git commits created | none | none |
> | `claude_code.cost.usage` | Cost of the Claude Code session | USD | `model`, `query_source`, `speed`, `effort`, plus attribution attrs† |
> | `claude_code.token.usage` | Number of tokens used | tokens | `type`: `"input"`, `"output"`, `"cacheRead"`, `"cacheCreation"`; `model`, `query_source`, `speed`, `effort`, plus attribution attrs† |
> | `claude_code.code_edit_tool.decision` | Count of code editing tool permission decisions | none | `tool_name`, `decision`, `source`, `language` |
> | `claude_code.active_time.total` | Total active time | s | `type`: `"user"`, `"cli"` |
>
> † **Attribution attrs:** `agent.name`, `skill.name`, `plugin.name`, `marketplace.name`, …

### https://code.claude.com/docs/en/hooks

Asked: From this section of the hooks reference, list exact input fields for: UserPromptSubmit (prompt field name, any field saying where the prompt came from, e.g. whether Claude Code started the turn itself, any 'source'/'origin'/'turn_origin' field), UserPromptExpansion, PostToolUse (tool_response shape

> The excerpt starts partway through the page and is cut off inside the Notification section. **Stop, SubagentStop, SessionEnd, PreCompact, FileChanged, CwdChanged and TaskCreated have no input schemas in it.** What it does say about them is listed at the end.
>
> ## Prompt origin (human vs. automated)
>
> **No `source`, `origin` or `turn_origin` field is documented for UserPromptSubmit in this excerpt.** The docs say UserPromptSubmit fires for more than typed prompts:
>
> * a session-scoped scheduled task firing, including a `/loop` iteration
> * a background subagent reporting back to the session that started it
> * a message another session sends to your main conversation
> * Code Mode output, for `command`, `http` and `mcp_tool` hooks, when code can run in the background
>
> Even so, the documented input is only the common fields plus `prompt` (and `session_title`). If an origin field exists, it would be under "Common input fields," which comes earlier in the page and isn't in this excerpt.
>
> These are the closest signals, and none of them labels the prompt itself:
>
> * **UserPromptExpansion** fires when a *user-typed* command expands, such as typing `/skillname`. Its `command_source` (for example `"plugin"`) says where the command is defined, not who typed it.
> * **SessionStart `source: "fork"`** includes a conversation you move to the background, as well as `--fork-session`, `/fork` and `/branch`.
> * **SessionStart `agent_type`** is set when Claude Code starts with `claude --agent <name>`.
> * …

### https://code.claude.com/docs/en/hooks

Asked: List exact input fields for Notification (type field name, message, title), Stop (stop_hook_active, last_assistant_message, others), SubagentStop (agent_id, agent_type, agent_transcript_path...), SessionEnd (reason values), PreCompact (trigger, custom_instructions), FileChanged, CwdChanged, TaskCrea

> <persisted-output>
> E_CODE_DISABLE_PERMISSION_PROMPT_NOTIFY_HOOKS`](https://code.claude.com/docs/en/env-vars) to `1` to turn `permission_prompt` off in these sessions.
>
> Before v2.1.233, `permission_prompt` didn't fire in these sessions.
>
> Use separate matchers to run different handlers depending on the notification type. This configuration triggers a permission-specific alert script when Claude needs permission approval and a different notification when Claude has been idle:
>
> ```json theme={null}
> {
>   "hooks": {
>     "Notification": [
>       {
>         "matcher": "permission_prompt",
>         "hooks": [
>           {
>             "type": "command",
>             "command": "/path/to/permission-alert.sh"
>           }
>         ]
>       },
>       {
>         "matcher": "idle_prompt",
>         "hooks": [
>           {
>             "type": "command",
>             "command": "/path/to/idle-notification.sh"
>           }
>         ]
>       }
>     ]
>   }
> }
> ```
>
> #### Notification input
>
> In addition to the [common input fields](#common-input-fields), Notification hooks receive `message` with the notification text, an optional `title`, and `notification_type` indicating which type fired.
>
> ```json theme={null}
> {
>   "session_id": "abc123",
>   "transcript_path": "/Users/.../.claude/projects/.../00893aaf-19fa-41d2-8238-13269b9b3ca0.jsonl",
>   "cwd": "/Users/...",
>   "hook_event_name": "Notification",
>   "message": "Claude needs your permission",
>   "notification_type": "permission_prompt"
> }
> ```
>
> Notification hooks can't block or modify …

### https://code.claude.com/docs/en/memory

Asked: Does Claude Code read AGENTS.md files natively (automatically), and since which version? Quote the exact text about AGENTS.md. List all memory/instruction file locations (CLAUDE.md, CLAUDE.local.md, .claude/rules/*.md, ~/.claude/CLAUDE.md, managed policy paths per OS), how imports (@path) work, and 

> <persisted-output>
> > ## Documentation Index
> > Fetch the complete documentation index at: https://code.claude.com/docs/llms.txt
> > Use this file to discover all available pages before exploring further.
>
> # How Claude remembers your project
>
> > Give Claude persistent instructions with CLAUDE.md or AGENTS.md files, and let Claude accumulate learnings automatically with auto memory.
>
> Each Claude Code session begins with a fresh context window. Two mechanisms carry knowledge across sessions:
>
> * **CLAUDE.md files**: instructions you write to give Claude persistent context. Claude can also read a repository's [`AGENTS.md` files](#agents-md) in place of CLAUDE.md
> * **Auto memory**: notes Claude writes itself based on your corrections and preferences
>
> This page covers how to:
>
> * [Write and organize CLAUDE.md files](#claude-md-files)
> * [Use an existing AGENTS.md](#agents-md) as your project instructions
> * [Scope rules to specific file types](#organize-rules-with-claude/rules/) with `.claude/rules/`
> * [Configure auto memory](#auto-memory) so Claude takes notes automatically
> * [Troubleshoot](#troubleshoot-memory-issues) when instructions aren't being followed
>
> ## CLAUDE.md vs auto memory
>
> Claude Code has two complementary memory systems. Both are loaded at the start of every conversation. Claude treats them as context, not enforced configuration. To block an action regardless of what Claude decides, use a [PreToolUse hook](https://code.claude.com/docs/en/hooks-guide.md) instead. The more specific and …

### https://code.claude.com/docs/en/headless

Asked: Summarize: how to run Claude Code non-interactively (-p / --print), output formats (json, stream-json fields such as session_id, total_cost_usd, duration_ms, duration_api_ms, num_turns), --bare mode (what it skips: hooks, plugins, CLAUDE.md, MCP?), --no-session-persistence, whether -p sessions are s

> > ## Documentation Index
> > Fetch the complete documentation index at: https://code.claude.com/docs/llms.txt
> > Use this file to discover all available pages before exploring further.
>
> # Run Claude Code programmatically
>
> > Use the Agent SDK to run Claude Code programmatically from the CLI, Python, or TypeScript.
>
> The [Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview.md) gives you the same tools, agent loop, and context management that power Claude Code. It's available as a CLI for scripts and CI/CD, or as [Python](https://code.claude.com/docs/en/agent-sdk/python.md) and [TypeScript](https://code.claude.com/docs/en/agent-sdk/typescript.md) packages for full programmatic control.
>
> To run Claude Code in non-interactive mode, pass `-p` with your prompt and the [CLI options](https://code.claude.com/docs/en/cli-reference.md) you need:
>
> ```bash theme={null}
> claude -p "Find and fix the bug in auth.py" --allowedTools "Read,Edit,Bash"
> ```
>
> This page covers using the Agent SDK via the CLI (`claude -p`). For the Python and TypeScript SDK packages with structured outputs, tool approval callbacks, and native message objects, see the [full Agent SDK documentation](https://code.claude.com/docs/en/agent-sdk/overview.md).
>
> ## Basic usage
>
> Add the `-p` (or `--print`) flag to any `claude` command to run it non-interactively. Not every [CLI option](https://code.claude.com/docs/en/cli-reference.md) combines with `-p`. Claude Code rejects `--bg`, and rejects `--cloud` with a task …

### https://code.claude.com/docs/en/sessions

Asked: Where are Claude Code transcripts stored on each OS (exact path pattern, how the project directory name is encoded, subagent transcripts location), what format they are, how long they are kept (cleanupPeriodDays default and behavior), whether -p/SDK sessions are saved, background sessions, and anyth

> > ## Documentation Index
> > Fetch the complete documentation index at: https://code.claude.com/docs/llms.txt
> > Use this file to discover all available pages before exploring further.
>
> # Manage sessions
>
> > Name, resume, branch, and switch between Claude Code conversations. Covers `--continue`, `--resume`, `--from-pr`, the `/resume` picker, session naming, exporting transcripts, and where transcripts are stored.
>
> A [session](https://code.claude.com/docs/en/glossary.md#session) is a saved conversation tied to a project directory. Claude Code stores it locally as you work, so you can resume where you left off, branch to try a different approach, or switch between tasks.
>
> The [desktop app](https://code.claude.com/docs/en/desktop.md#work-in-parallel-with-sessions), [claude.ai/code](https://code.claude.com/docs/en/claude-code-on-the-web.md), and the [VS Code extension](https://code.claude.com/docs/en/vs-code.md#resume-past-conversations) each keep their own session list, and the desktop app can also [resume a CLI session](https://code.claude.com/docs/en/desktop.md#coming-from-the-cli). This page covers the CLI.
>
> ## Resume a session
>
> Sessions are saved continuously to [local transcript files](#export-and-locate-session-data) as you work, so you can return to one after exiting or running `/clear`. Use these entry points:
>
> | Command | What it does |
> | :- | :- |
> | `claude --continue` | Reopens the most recent session in the current directory |
> | `claude --resume` | Opens the [session …

### https://code.claude.com/docs/en/scheduled-tasks

Asked: Explain /loop and scheduled tasks: how a loop iteration is started (does it appear as a user prompt? does UserPromptSubmit fire?), self-paced wakeups, session-scoped vs durable cron, any /goal or autonomous continuation features, and any field in transcripts or hooks that marks a turn as started by 

> > ## Documentation Index
> > Fetch the complete documentation index at: https://code.claude.com/docs/llms.txt
> > Use this file to discover all available pages before exploring further.
>
> # Run prompts on a schedule
>
> > Use /loop and the cron scheduling tools to run prompts repeatedly, poll for status, or set one-time reminders within a Claude Code session.
>
> Session-scoped scheduled tasks let Claude re-run a prompt automatically on an interval within a Claude Code session. Use them to poll a deployment, babysit a PR, check back on a long-running build, or remind yourself to do something later in the session.
>
> > [!NOTE]
> >
> > These cases are covered on other pages:
> >
> > * **Reacting to events as they happen instead of polling**: see [Channels](https://code.claude.com/docs/en/channels.md). Your CI can push the failure into the session directly.
> > * **Keeping the session working turn after turn toward a condition rather than on an interval**: see [`/goal`](https://code.claude.com/docs/en/goal).
>
> Tasks are session-scoped. When you resume with `--resume` or `--continue`, Claude Code restores tasks that haven't [expired](#seven-day-expiry), except those listed under [Limitations](#limitations). For scheduling that survives independently of any session, create a [cloud scheduled task](https://code.claude.com/docs/en/routines.md), set up a [Desktop scheduled task](https://code.claude.com/docs/en/desktop-scheduled-tasks.md), or use [GitHub …

### https://code.claude.com/docs/en/goal

Asked: How does /goal work: what starts each continuation turn, does UserPromptSubmit or Stop fire per turn, what limits apply (turns, time, tokens), and how is a goal-driven turn marked in transcripts, hooks, or OTel (any field names)? Quote exact names.

> > ## Documentation Index
> > Fetch the complete documentation index at: https://code.claude.com/docs/llms.txt
> > Use this file to discover all available pages before exploring further.
>
> # Keep Claude working toward a goal
>
> > Set a completion condition with /goal and Claude keeps working until it's met, a model judges it impossible, or an error you have to fix clears the goal.
>
> The `/goal` command sets a completion condition and Claude keeps working toward it without you prompting each step. After each turn, a model checks whether the condition holds. If the model judges it not yet met, Claude starts another turn instead of returning control to you. The goal clears automatically once the condition is met, if the model judges the condition impossible to satisfy, or if a turn fails on [an error you have to fix](#errors-you-have-to-fix-clear-the-goal).
>
> Use a goal for substantial work with a verifiable end state:
>
> * Migrating a module to a new API until every call site compiles and tests pass
> * Implementing a design doc until all acceptance criteria hold
> * Splitting a large file into focused modules until each is under a size budget
> * Working through a labeled issue backlog until the queue is empty
>
> ## Compare ways to keep a session running
>
> Three approaches keep the current session running between prompts. Pick based on what should start the next turn:
>
> | Approach | Next turn starts when | Stops when |
> | :- | :- | :- |
> | `/goal` | The previous turn finishes, or, in an interactive …

### https://cursor.com/docs/agent/hooks

Asked: List every Cursor hook event name with when it fires, the JSON input fields (common fields like conversation_id, generation_id, model, hook_event_name, cursor_version, workspace_roots, user_email, transcript_path; and event-specific fields like prompt, attachments, command, file_path, edits, text, s

> **21 events:** 18 Agent (Agent Chat), 2 Tab, 1 app-lifecycle.
>
> ### Common input fields
> Every hook receives `conversation_id`, `generation_id`, `model`, `model_id` (optional), `model_params` (optional `{id, value}` array), `hook_event_name`, `cursor_version`, `workspace_roots`, `user_email` (string/null) and `transcript_path` (string/null).
>
> `workspaceOpen` is the exception. It omits `conversation_id`, `generation_id`, `model`, `session_id` and `transcript_path`.
>
> ### Events
>
> | Event | When it fires | Event-specific input | Output | Cloud agents |
> |---|---|---|---|---|
> | `sessionStart` | Composer chat created; non-blocking | `session_id` (equals `conversation_id`), `is_background_agent`, `composer_mode` (`agent`/`ask`/`edit`) | `env`, `additional_context` (`continue`/`user_message` are accepted but do nothing) | No |
> | `sessionEnd` | Composer chat closes; response only logged | `session_id`, `reason` (`completed`/`aborted`/`error`/`window_close`/`user_close`), `duration_ms`, `is_background_agent`, `final_status`, `error_message` | none | No |
> | `preToolUse` | Before any tool invocation (Shell, Read, Write, MCP, Task…) | `tool_name`, `tool_input`, `tool_use_id`, `cwd`, `agent_message` | `permission` (`allow`/`deny`; `ask` is accepted but has no effect), `user_message`, `agent_message`, `updated_input` | Yes |
> | `postToolUse` | Tool call succeeded | `tool_name`, `tool_input`, `tool_output` (JSON string), `tool_use_id`, `cwd`, `duration` (ms) | `updated_mcp_tool_output` (MCP …

### https://code.claude.com/docs/en/goal

Asked: How does /goal work: what starts each continuation turn, does UserPromptSubmit or Stop fire per turn, what limits apply (turns, time, tokens), and how is a goal-driven turn marked in transcripts, hooks, or OTel (any field names)? Quote exact names.

> > ## Documentation Index
> > Fetch the complete documentation index at: https://code.claude.com/docs/llms.txt
> > Use this file to discover all available pages before exploring further.
>
> # Keep Claude working toward a goal
>
> > Set a completion condition with /goal and Claude keeps working until it's met, a model judges it impossible, or an error you have to fix clears the goal.
>
> The `/goal` command sets a completion condition and Claude keeps working toward it without you prompting each step. After each turn, a model checks whether the condition holds. If the model judges it not yet met, Claude starts another turn instead of returning control to you. The goal clears automatically once the condition is met, if the model judges the condition impossible to satisfy, or if a turn fails on [an error you have to fix](#errors-you-have-to-fix-clear-the-goal).
>
> Use a goal for substantial work with a verifiable end state:
>
> * Migrating a module to a new API until every call site compiles and tests pass
> * Implementing a design doc until all acceptance criteria hold
> * Splitting a large file into focused modules until each is under a size budget
> * Working through a labeled issue backlog until the queue is empty
>
> ## Compare ways to keep a session running
>
> Three approaches keep the current session running between prompts. Pick based on what should start the next turn:
>
> | Approach | Next turn starts when | Stops when |
> | :- | :- | :- |
> | `/goal` | The previous turn finishes, or, in an interactive …

### https://cursor.com/docs/agent/hooks

Asked: List every Cursor hook event name with when it fires, the JSON input fields (common fields like conversation_id, generation_id, model, hook_event_name, cursor_version, workspace_roots, user_email, transcript_path; and event-specific fields like prompt, attachments, command, file_path, edits, text, s

> **21 events:** 18 Agent (Agent Chat), 2 Tab, 1 app-lifecycle.
>
> ### Common input fields
> Every hook receives `conversation_id`, `generation_id`, `model`, `model_id` (optional), `model_params` (optional `{id, value}` array), `hook_event_name`, `cursor_version`, `workspace_roots`, `user_email` (string/null) and `transcript_path` (string/null).
>
> `workspaceOpen` is the exception. It omits `conversation_id`, `generation_id`, `model`, `session_id` and `transcript_path`.
>
> ### Events
>
> | Event | When it fires | Event-specific input | Output | Cloud agents |
> |---|---|---|---|---|
> | `sessionStart` | Composer chat created; non-blocking | `session_id` (equals `conversation_id`), `is_background_agent`, `composer_mode` (`agent`/`ask`/`edit`) | `env`, `additional_context` (`continue`/`user_message` are accepted but do nothing) | No |
> | `sessionEnd` | Composer chat closes; response only logged | `session_id`, `reason` (`completed`/`aborted`/`error`/`window_close`/`user_close`), `duration_ms`, `is_background_agent`, `final_status`, `error_message` | none | No |
> | `preToolUse` | Before any tool invocation (Shell, Read, Write, MCP, Task…) | `tool_name`, `tool_input`, `tool_use_id`, `cwd`, `agent_message` | `permission` (`allow`/`deny`; `ask` is accepted but has no effect), `user_message`, `agent_message`, `updated_input` | Yes |
> | `postToolUse` | Tool call succeeded | `tool_name`, `tool_input`, `tool_output` (JSON string), `tool_use_id`, `cwd`, `duration` (ms) | `updated_mcp_tool_output` (MCP …

### https://cursor.com/docs/agent/third-party-hooks

Asked: How does Cursor load Claude Code hooks: which files (.claude/settings.json, ~/.claude/settings.json), which events map to which Cursor events, what payload translation happens, and how to enable/disable. Also any mention of where agent transcripts are stored (transcript_path location/format).

> This page mentions Claude Code only briefly. For the specifics you asked about, it points to a separate page: [Third Party Hooks](https://cursor.com/docs/reference/third-party-hooks.md).
>
> **What it says about Claude Code hooks**
> - Cursor "supports loading hooks from third-party tools like Claude Code."
> - **Loop limit:** `loop_limit` sets how many automatic follow-ups `stop`/`subagentStop` can trigger. It defaults to 5 for native hooks but "`null` for Claude Code hooks," which means no cap.
> - **Exit code 2:** Exiting with 2 stops the action, the same as a deny. The page says this "matches Claude Code behavior for compatibility."
> - **Env var:** Scripts always get `CLAUDE_PROJECT_DIR`, an "Alias for project dir (Claude compatibility)," alongside `CURSOR_PROJECT_DIR`.
>
> **What it doesn't cover**
> - Which Claude config files are read (`.claude/settings.json`, `~/.claude/settings.json`).
> - How Claude Code events map to Cursor events.
> - Payload translation. The only compatibility details are the ones above.
> - How to turn Claude Code hook loading on or off. The closest thing is the Hooks tab in **Customize** and the Hooks output channel. They show what's configured, what ran, and any errors.
>
> **Transcripts**
> - The shared input fields include `transcript_path`: "Path to the main conversation transcript file (null if transcripts disabled)." Scripts also get `CURSOR_TRANSCRIPT_PATH` when transcripts are on.
> - `subagentStop` adds `agent_transcript_path`, which points to the subagent's …

### https://cursor.com/docs/reference/third-party-hooks

Asked: Exactly which Claude Code config files Cursor reads hooks from, how Claude Code event names map to Cursor events (e.g. UserPromptSubmit -> beforeSubmitPrompt), what payload fields are translated, priority/precedence, and how to enable/disable (setting name). Quote exact names.

> **1. Claude Code files Cursor reads (highest priority first)**
>
> | Label | Path | Meaning |
> |---|---|---|
> | "Project local" | ".claude/settings.local.json" | Per-repo overrides that git ignores |
> | "Project" | ".claude/settings.json" | Shared and committed with the repo |
> | "User" | "~/.claude/settings.json" | Applies across all projects |
>
> **2. Event name mapping**
>
> | Claude Code | Cursor |
> |---|---|
> | "PreToolUse" | "preToolUse" |
> | "PostToolUse" | "postToolUse" |
> | "UserPromptSubmit" | "beforeSubmitPrompt" |
> | "Stop" | "stop" |
> | "SubagentStop" | "subagentStop" |
> | "SessionStart" | "sessionStart" |
> | "SessionEnd" | "sessionEnd" |
> | "PreCompact" | "preCompact" |
>
> - **Unsupported:** "Notification" and "PermissionRequest".
> - **Cursor's own format only:** "subagentStart", "loop_limit", and pushing hooks to teams or enterprises from the dashboard.
>
> **3. Translated fields**
>
> - **Response shape:** Cursor accepts either Claude's nested "hookSpecificOutput" or a flat object.
> - **PreToolUse:**
>   - "permissionDecision" → "permission"
>   - "permissionDecisionReason" → "user_message"
>   - "updatedInput" → "updated_input"
> - **Stop/SubagentStop:** "decision": "block" plus a "reason" (nested or flat) works like Cursor's "followup_message". The reason is sent as a follow-up automatically.
> - **Tool names:**
>   - "Bash" → "Shell" and "Edit" → "Write"
>   - "Read", "Write", "Grep", "Task", "WebFetch" and "WebSearch" stay the same
>   - "Glob" is unsupported
> - **Exit codes:**
>   - 0 means success, and …

### https://github.com/subinium/agf/issues/35

Asked: What does this issue say about where Cursor stores agent transcripts (exact paths, file format .jsonl vs .txt, nested <id>/ subdirectory), and what fields the jsonl lines contain? Include dates and Cursor versions mentioned.

> **Location and layout**
> - agf's scanner ("`src/scanner/cursor_agent.rs:22-31`") expected "`~/.cursor/projects/*/agent-transcripts/*.txt`". It searched exactly three levels deep ("`min_depth(3)`"/"`max_depth(3)`") and kept only .txt files.
> - The issue says cursor-agent, "at least as of 2026", writes .jsonl files one level lower (four deep). Each file sits in its own session-UUID folder: "`~/.cursor/projects/<project-encoded-cwd>/agent-transcripts/<session-uuid>/<session-uuid>.jsonl`"
> - The folder and the file share the same UUID. The proposed fix widens the search to "`min_depth(3).max_depth(4)`" or removes the depth limits. It treats the folder holding the .jsonl as the session ID and requires the level above it to be "`agent-transcripts`".
> - The author's example is "`~/.cursor/projects/Users-subinium-claude/agent-transcripts/`". It holds a folder named "`92e3bf27-512a-4e20-bc79-6e7d61b8a730`" with a .jsonl file of the same name inside. `find` located two .jsonl transcripts and no .txt ones, yet "`agf list --agent cursor-agent`" showed zero sessions.
>
> **.jsonl vs .txt**
> - Each line of the new files holds a single JSON object. The issue says this keeps the "same role/content shape as the older .txt format." It recommends still matching .txt because some setups could still create those files.
>
> **Fields in each line**
> - `"role"`: either `"user"` or `"assistant"`
> - `"message"` → `"content"`: an array of parts, each with a `"type"`
>   - `"text"` parts contain a `"text"` string
>   - …

### https://docs.rs/ctx-agent/latest/src/ctx/surface/cursor.rs.html

Asked: What paths and formats does this code read for Cursor (state.vscdb location per OS, cursorDiskKV keys like composerData:/bubbleId:, agent-transcripts jsonl), and what fields does it extract (timestamps such as createdAt, lastUpdatedAt, type 1/2 for user/assistant, text, etc.)? Quote the key names an

> **Bottom line:** This file reads only Cursor's JSONL agent transcripts. It contains no "state.vscdb", "cursorDiskKV", "composerData:", "bubbleId:", "createdAt", "lastUpdatedAt", or numeric type 1/2 roles. It also has no per-OS path logic. rusqlite appears in the crate's dependency list, but this file never opens a database. If the crate reads state.vscdb, that code is in another file.
>
> **Paths**
> - Documented layout: "`~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl`"
> - Discovery begins at a home directory passed in by the caller ("`home.join(".cursor").join("projects")`"). It enters each project's "agent-transcripts" folder and keeps only the file named after each session folder: "`dir.join(format!("{uuid}.jsonl"))`".
> - Anything under "`subagents/`" is ignored.
>
> **Line formats**
> - "`{ "role": "user"|"assistant", "message": { "content": [ ... ] } }`"
> - "`{ "type": "turn_ended", "status": "success"|"error", "error": "..." }`"
> - Lines that fail to parse are skipped, and unknown roles are ignored.
>
> **Extracted fields**
> - **Role:** taken from the string "role" ("user"/"assistant"). The only "type" values checked are "turn_ended", "text", and "tool_use".
> - **Text:** all "text" blocks under "message" → "content" are joined with newlines.
>   - For user turns, only the part between "`<user_query>`" and "`</user_query>`" is kept if those tags exist. Otherwise the entire message is used.
>   - Stored text is cut to 500 chars ("TEXT_PREFIX_CAP").
> - **Tool calls** …

### https://session-viz.com/blog/reading-cursors-chat-history/

Asked: Extract exact details: state.vscdb paths on Windows/macOS/Linux, tables (ItemTable, cursorDiskKV), key patterns (composerData:<id>, bubbleId:<composerId>:<bubbleId>, checkpointId, codeBlockDiff etc.), the JSON fields that carry timestamps (createdAt, lastUpdatedAt, timingInfo, clientStartTime etc.),

> **Post and version**
> - The post is titled "Reading Cursor's chat history out of SQLite" and is dated 15 August 2026.
> - It gives no Cursor version number. The only versioning hint is that tool names now carry `_vN` suffixes (`edit_file_v2`, `run_terminal_command_v2`).
> - The reader uses the `node:sqlite` module built into Node 22. That module is experimental and prints a warning the first time it is used.
>
> **state.vscdb location**
> - **macOS:** `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb`
> - **Linux:** `~/.config/Cursor/User/globalStorage/`. Flatpak installs use another path, which the post doesn't give.
> - **Windows:** the post only says it's beneath `%APPDATA%`. No full path is given.
> - **Per-workspace DBs:** these live in `workspaceStorage`. The author has 112, and 58 of them contain `composer.composerData`, referencing 385 of the 641 chats.
>
> **Size**
> - The file is 4,375,052,288 bytes (about 4.4 GB). It holds 641 non-empty conversations and 107,077 messages.
> - A full pass parses about 1,329 MB of JSON in 4–6 seconds with a warm cache.
>
> **Tables and keys**
> The post says there are three tables but names only `cursorDiskKV`, which has 274,832 rows. `ItemTable` isn't mentioned.
>
> | prefix | rows | key format stated |
> |---|---|---|
> | `agentKv` | 116,018 | — |
> | `bubbleId` | 115,295 | `bubbleId:<composerId>:<bubbleId>` |
> | `checkpointId` | 17,000 | — |
> | `codeBlockDiff` | 12,122 | — |
> | `messageRequestContext` | 3,910 | — |
> | `composerData` | 981 | …

### https://vshulcz.github.io/deja-vu/registry/cursor.html

Asked: Extract exact Cursor storage details: paths per OS (state.vscdb global and workspaceStorage, ~/.cursor/projects agent-transcripts, ~/.cursor/chats for cursor-agent CLI store.db), key patterns, timestamp fields, formats, retention, and dates/versions mentioned.

> Source: deja-vu's "Cursor session format" doc. Code-formatted text is copied verbatim from the page as identifiers and paths. Quotation marks mark exact wording.
>
> **1. Cursor IDE: `state.vscdb` (SQLite)**
> - The files are `<root>/globalStorage/state.vscdb` and `<root>/workspaceStorage/*/state.vscdb`. `<root>` depends on the OS:
>   - macOS: `~/Library/Application Support/Cursor/User`
>   - Windows: `%APPDATA%\Cursor\User`
>   - Linux and other systems: `~/.config/Cursor/User`. On Linux, `$XDG_CONFIG_HOME/Cursor/User` is used instead if it exists.
>   - `DEJA_CURSOR_ROOT` overrides the location for deja.
> - Chats live in the table `cursorDiskKV(key, value)`, which holds JSON values:
>   - Chat: `composerData:<id>`, with `composerId`, `name`, `createdAt`, `lastUpdatedAt`
>   - Message: `bubbleId:<composer-id>:<bubble-id>`, with `type`, `text` (fallback `rawText`), `timestamp`, `workspaceProjectDir`
> - `type: 1` means user, and any other number means assistant.
> - The project comes from the earliest bubble that has `workspaceProjectDir`.
> - All three timestamp fields are epoch milliseconds, e.g. `1784278800000`.
> - Reading the database needs the `sqlite3` binary.
> - IDE chats reopen only in the editor, because the CLI won't accept composer ids.
>
> **2. Cursor CLI: JSONL transcripts**
> - Path: `${CURSOR_CONFIG_DIR:-~/.cursor}/projects/<encoded-path>/agent-transcripts/**/*.jsonl`. `DEJA_CURSOR_CLI_ROOT` overrides it. The page gives no per-OS variants.
> - Subagents are written to …

### https://code.visualstudio.com/docs/copilot/customization/hooks

Asked: List all VS Code agent hook event names, when they fire, input JSON fields (sessionId, timestamp, cwd, prompt, transcript_path, tool_name, tool_input etc.), config file locations (.github/hooks/*.json, ~/.copilot/hooks, .claude/settings.json compatibility, settings like chat.hookFilesLocations / cha

> **Status and version**
> - "Agent hooks is in preview and might change before it becomes generally available." Each provider's implementation can have its own status and release timeline.
> - The page doesn't say which VS Code version introduced hooks. The page is dated 11/18/2026.
> - The legacy Local harness is deprecated and will be removed in February 2027.
>
> **Events (Copilot harness, camelCase)**
>
> | Event | When it fires / purpose |
> |---|---|
> | `sessionStart` | A session begins or resumes; can supply context |
> | `userPromptSubmitted` | The user submits a prompt; observe only, because command-hook output has no effect |
> | `preToolUse` | Before a tool call; can inspect it or return a permission decision |
> | `postToolUse` | After a tool succeeds; can validate or inject context, but can't reverse the action |
> | `preCompact` | Before compaction; can save information |
> | `subagentStart` / `subagentStop` | A subagent launches / completes |
> | `agentStop` | The agent is wrapping up a turn; can check whether it should really stop |
> | `sessionEnd` | The session ends; for cleanup |
>
> This list isn't exhaustive: "See the event reference for the full event list, timing, fields, and subagent coverage." That reference is GitHub's Copilot hooks reference.
>
> **Event aliases**
> - "Event names also select the input format."
> - The PascalCase `PreToolUse` alias sends snake_case fields and may use Claude-style tool names (`Read`, `Write`, `Edit`).
> - In the Local harness, an agent-scoped `Stop` is …

### https://docs.github.com/en/copilot/reference/hooks-reference

Asked: List every hook event with exact input JSON fields (e.g. timestamp, cwd, sessionId, source, initialPrompt, prompt, toolName, toolArgs, toolResult, reason, transcriptPath), output fields, config schema (version, hooks, type, bash, powershell, command, cwd, env, timeoutSec), file locations, and which 

> Unable to verify if domain docs.github.com is safe to fetch. This may be due to network restrictions or enterprise security policies blocking claude.ai.

### https://code.visualstudio.com/docs/agents/guides/monitoring-agents

Asked: How to enable OTel monitoring in VS Code for agents: settings names (e.g. github.copilot.chat.otel.enabled, exporterType, otlpEndpoint, captureContent, outfile), env vars, emitted event names (copilot_chat.session.start, user message events etc.) and metric names with attributes. Also: where VS Code

> *Setting, variable, event, and metric names below are exact. Descriptions are paraphrased.*
>
> ### Turning OTel on
> Any one of these activates it:
> - `github.copilot.chat.otel.enabled` → `true`
> - `github.copilot.chat.otel.dbSpanExporter.enabled` → `true`
> - `COPILOT_OTEL_ENABLED=true`
> - `OTEL_EXPORTER_OTLP_ENDPOINT` has any value
>
> To find the settings, open Settings and type `copilot otel`.
>
> ### Settings
> | Setting | Type, default | Purpose |
> |---|---|---|
> | `github.copilot.chat.otel.enabled` | boolean, `false` | Main switch |
> | `github.copilot.chat.otel.exporterType` | string, `"otlp-http"` | Choose `otlp-http` / `otlp-grpc` / `console` / `file` |
> | `github.copilot.chat.otel.otlpEndpoint` | string, `"http://localhost:4318"` | Collector URL |
> | `github.copilot.chat.otel.captureContent` | boolean, `false` | Records prompt and reply text, system prompt, tool definitions, and tool inputs/outputs |
> | `github.copilot.chat.otel.outfile` | string, `""` | JSONL target path (use with the `file` exporter) |
> | `github.copilot.chat.otel.captureIdentity` | boolean, `false` | Logs the signed-in account, OS username, and machine name (Local harness only) |
> | `github.copilot.chat.otel.maxAttributeSizeChars` | integer, `0` | Character limit for each captured-content attribute; `0` = unlimited |
> | `github.copilot.chat.otel.dbSpanExporter.enabled` | boolean, `false` | Stores spans in an on-device SQLite file and turns OTel on by itself |
>
> Example:
> ```json
> {
>   "github.copilot.chat.otel.enabled": …

### https://vshulcz.github.io/deja-vu/registry/copilot.html

Asked: Extract exact Copilot CLI and VS Code Copilot chat storage details: paths per OS (~/.copilot/session-state/<id>/events.jsonl, workspace.yaml, history-session-state, VS Code workspaceStorage chatSessions *.json/*.jsonl), event type names (session.start, user.message, assistant.message, tool.execution

> **Scope:** This page covers only Copilot CLI. VS Code Copilot Chat has its own page (`copilot-chat.html`), which isn't included here, so this text doesn't give its workspaceStorage/chatSessions layout.
>
> **Copilot CLI storage**
> - Location: `${COPILOT_HOME:-~/.copilot}/session-state/<sessionId>/events.jsonl`. No per-OS variants are given. `COPILOT_HOME` also relocates MCP settings, the skill and hook files. deja's read override is `DEJA_COPILOT_ROOT`, which targets `session-state`.
> - Format: "append-only JSONL, one event per line, each `{type, data, timestamp}`".
> - The folder name "is not the id and can differ from it." A folder may also persist after its `events.jsonl` is gone.
>
> **Event types and fields**
> - `session.start`: `sessionId` and `context.cwd` (the project name is derived from the cwd).
> - `user.message` / `assistant.message`: `data.content`, a bare string. Empty assistant turns that only ran tools used to be dropped. They now show up through the tool events.
> - `tool.execution_start`: `toolName` and `arguments`.
>   - `path` for file tools, `command` for `bash`, `old_str`/`new_str` for `edit`.
>   - `file_text` holds the entire new file for `create`. `view` is a read.
>   - Tool names are lower-case, unlike Claude Code's capitalised names, and the edit field is `old_str`, not `old_string`.
>   - GPT models never emit `edit`/`create`. Changes arrive as `apply_patch`, with the raw patch text as `arguments`.
> - `tool.execution_complete`: `data.result.content`. `success` is true …

### https://developers.openai.com/codex/hooks

Asked: List all Codex hook events, when they fire, the JSON input fields (session_id, turn_id, transcript_path, cwd, model, hook_event_name, prompt, tool_name, tool_input, tool_response, source, permission_mode etc.), output/decision fields, config locations (config.toml [hooks], hooks.json, project .codex

> REDIRECT DETECTED: The URL redirects to a location that was not fetched automatically.
>
>     Original URL: https://developers.openai.com/codex/hooks
>     Redirect URL (from the server's Location header — server-supplied, not verified): https://learn.chatgpt.com/docs/hooks
>     Status: 308 Permanent Redirect
>
>     To complete your request, I need to fetch content from the redirected URL. Please use WebFetch again with these parameters:
>     - url: "https://learn.chatgpt.com/docs/hooks"
>     - prompt: "List all Codex hook events, when they fire, the JSON input fields (session_id, turn_id, transcript_path, cwd, model, hook_event_name, prompt, tool_name, tool_input, tool_response, source, permission_mode etc.), output/decision fields, config locations (config.toml [hooks], hooks.json, project .codex/), feature flag (codex_hooks), whether hooks run for codex exec, IDE extension, app-server clients and cloud; Windows support; and the version where hooks became stable. Quote exact names."

### https://learn.chatgpt.com/docs/hooks

Asked: List all Codex hook events, when they fire, the JSON input fields (session_id, turn_id, transcript_path, cwd, model, hook_event_name, prompt, tool_name, tool_input, tool_response, source, permission_mode etc.), output/decision fields, config locations (config.toml [hooks], hooks.json, project .codex

> # Codex Hooks: Summary from the Docs Page
>
> ## Events (14)
>
> | Event | When it fires | `matcher` filters |
> |---|---|---|
> | `SessionStart` | A session begins. Also after a root session compacts, before the next model call. | `source`: `startup`, `resume`, `clear`, `compact`, `fork` |
> | `SessionEnd` | The main thread closes: an open chat is archived or deleted, Codex quits normally, or the chat sits idle 30 minutes with no connected client showing it. Never for subagents. | `reason` (only `other`) |
> | `SubagentStart` / `SubagentStop` | A subagent launches / finishes | `agent_type` |
> | `UserPromptSubmit` | Before a submitted prompt reaches the model | ignored |
> | `PreToolUse` | Before a local tool call: shell and `exec_command` (as `Bash`), `apply_patch`, MCP, other local function tools | `tool_name` plus aliases (`Edit`/`Write` for `apply_patch`, `Agent` for `spawn_agent`) |
> | `PermissionRequest` | Right before an approval prompt would appear (e.g., shell escalation, managed-network). Skipped when no approval is needed. | `tool_name` |
> | `PostToolUse` | After a supported tool returns output, including Bash commands with non-zero exits | `tool_name` |
> | `PreModelRequest` / `PostModelRequest` | Around every model request attempt, retries included | unsupported |
> | `PreCompact` / `PostCompact` | Before / after compaction | `trigger`: `manual`, `auto` |
> | `Stop` | When the turn is about to end | ignored |
> | `Interrupt` | The user interrupts a running main-thread turn | ignored …

### https://learn.chatgpt.com/docs/config-advanced

Asked: Extract the Codex OpenTelemetry ([otel]) configuration: keys (exporter, trace_exporter, metrics_exporter, log_user_prompt, environment, endpoint, protocol, headers), emitted event names (codex.conversation_starts, codex.api_request, codex.sse_event, codex.user_prompt, codex.tool_decision, codex.tool

> The server returned HTTP 404 Not Found.
>
> The response body was not retrieved. If this URL requires authentication, use an authenticated tool (e.g. `gh` for GitHub, or an MCP-provided fetch tool) instead of WebFetch.

