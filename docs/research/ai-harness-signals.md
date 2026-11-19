# AI harness signals: what AI-assisted building leaves behind, and how to capture it

Status: draft in progress

Track: signals. Researched 2026-11-18/19 for pairtime. Every claim cites a source with the date or version seen; anything not confirmed against a primary source is marked "(unverified)".

## Contents

1. Scope, method, and how to read this doc
2. Key findings
3. Claude Code
4. Cursor
5. GitHub Copilot (VS Code chat and agent mode, Copilot CLI)
6. OpenAI Codex (CLI, IDE extension, cloud)
7. Gemini CLI
8. Windsurf (now Devin Desktop)
9. Other harnesses (Cline, Roo Code, Kilo Code, OpenCode, Aider, Zed, Antigravity, Kiro, Amp, Warp, JetBrains, Trae, Continue, Goose)
10. Browser chat and app builders
11. The AGENTS.md + CLI idea, assessed
12. Telling a human session from headless, scheduled, and agent-driven runs
13. Human-side signals outside the agent (editor, terminal, git, OS input, browser)
14. Signal scorecard: what each proves, how it is faked, privacy and cost
15. Matrix: harness by capture method
16. Recommended v1 set
17. Design implications
18. Open questions
19. Sources

## 1. Scope, method, and how to read this doc

(pending)

## 2. Key findings

(pending)

## 3. Claude Code

Docs read as raw Markdown from code.claude.com on 2026-11-19 (pages `hooks`, `monitoring-usage`, `sessions`, `claude-directory`, `headless`, `memory`, `goal`, `scheduled-tasks`, `statusline`, `skills`, `env-vars`). Latest version in the changelog that day: v2.1.328. Claude Code is closed source; transcript field names below come from the docs plus two public parsers (wakatime-cli `pkg/ai/claude.go` at commit `7ab26b5`, daaain/claude-code-log `models.py` at `763365c`).

### 3.1 Hooks

Config lives in JSON settings files under the `hooks` key: `~/.claude/settings.json` (user, all projects), `.claude/settings.json` (project, committed), `.claude/settings.local.json` (project, gitignored), managed policy settings, a plugin's `hooks/hooks.json`, or skill/subagent frontmatter. Hook entries from all levels merge rather than replace. Handler types: `command`, `http` (POST of the same JSON), `mcp_tool`, `prompt`, `agent`. A non-interactive installer only has to merge one block into `~/.claude/settings.json`; shipping as a plugin (`hooks/hooks.json`) is the cleaner, removable alternative.

Common input fields on every event: `session_id`, `prompt_id` (UUID, matches OTel `prompt.id`, absent until first user input), `transcript_path`, `cwd`, `scratchpad_dir` (v2.1.257+), `permission_mode` (`default`, `plan`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`), `effort.level`, `hook_event_name`, plus `agent_id`/`agent_type` inside subagents or `--agent` sessions. Only `SessionStart` may carry `model`. No hook payload carries a timestamp: the receiver stamps arrival time itself.

Events that matter for time tracking (35 exist in total):

| Event | Fires | Payload beyond common fields | Use for pairtime |
|---|---|---|---|
| `SessionStart` | new, `resume`, `clear`, `compact`, `fork` (matcher = `source`) | `source`, `model`, `agent_type`, `session_title`; on resume/fork also `seconds_since_last_response`, `context_tokens` (v2.1.251+) | session boundaries, project via `cwd` |
| `UserPromptSubmit` | each prompt, before the model sees it | `prompt` (full text, pasted blocks expanded), `session_title` | the human-turn heartbeat; prompt hash, length |
| `UserPromptExpansion` | a typed `/command` or skill expands | `command_source` and name | slash-command use |
| `PreToolUse` / `PostToolUse` / `PostToolUseFailure` / `PostToolBatch` | around each tool call | `tool_name`, `tool_input`, `tool_use_id`, `tool_response` | agent work spans, files edited, test runs |
| `PermissionRequest` | a tool call needs approval | tool fields | the human was asked to decide |
| `Notification` | matcher `permission_prompt` (after ~6 s with no typing), `idle_prompt` (~60 s after a response with no typing), `agent_needs_input`, `agent_completed`, and others | `message`, `title`, `notification_type` | "agent waiting on human" markers |
| `Stop` | main agent finished a turn (not on user interrupt) | `stop_hook_active`, `last_assistant_message`, `background_tasks[]`, `session_crons[]` (each with `schedule`, `recurring`, `prompt`) | end of agent span; list of scheduled prompts that will fire later |
| `StopFailure` | turn ended on API error | | |
| `SubagentStart` / `SubagentStop` | subagent lifecycle | `agent_id`, `agent_type`, `agent_transcript_path` | separate agent-in-agent time |
| `SessionEnd` | exit, `/clear`, `/resume` switch, logout | `reason` (`clear`, `resume`, `logout`, `prompt_input_exit`, `other`) | close session; default timeout only 1.5 s |
| `FileChanged`, `CwdChanged`, `PreCompact`/`PostCompact`, `PreModelSwitch`/`PostModelSwitch` | as named | | minor |

Behavior that decides the design:

- `UserPromptSubmit` is not proof of a typed prompt. The docs say it also fires for a session-scoped scheduled task (including each `/loop` iteration), a background subagent reporting back, a message another session sends, and Code Mode output. No field in the payload says which. (hooks reference, "UserPromptSubmit")
- `Stop` carries `session_crons[].prompt`. A tracker that remembers those prompts can flag a later `UserPromptSubmit` whose `prompt` matches as machine-started. `stop_hook_active: true` marks a turn that a Stop hook (and so `/goal`) continued.
- `UserPromptSubmit` blocks the model until the hook returns (default timeout 30 s), so pairtime's hook must return in milliseconds or use `"async": true`. In `-p` mode async hooks are killed at teardown, so a hook must write its record before returning or start a detached process.
- Hooks in settings files wait for the workspace-trust dialog in interactive sessions, but run unprompted in `-p` and SDK sessions.
- `--bare` (recommended for scripts, and documented as becoming the default for `-p` later) skips hooks, plugins, skills, MCP, auto memory and CLAUDE.md. A headless run in bare mode leaves no hook events and no instruction-file compliance at all. `disableAllHooks` and managed `allowManagedHooksOnly` also turn hooks off.
- Windows: shell-form hooks run in Git Bash, or PowerShell when Git Bash is missing. Exec form (`command` + `args`) needs a real `.exe`, so pairtime should ship a native binary and register it in exec form.

### 3.2 Local transcripts and history

| File | Path (all OSes, `~` = `%USERPROFILE%` on Windows) | Format | Retention |
|---|---|---|---|
| Session transcript | `~/.claude/projects/<project>/<session-id>.jsonl`; `<project>` = cwd with every non-alphanumeric char replaced by `-`, truncated at 200 chars plus a hash | JSONL, one entry per line | deleted after `cleanupPeriodDays`, default 30, minimum 1 |
| Subagent transcripts | `~/.claude/projects/<project>/<session-id>/subagents/` | same | with parent |
| Typed-prompt history | `~/.claude/history.jsonl` | JSONL: "prompts you've typed, with timestamp and project path" | kept until deleted (not swept, except under the HIPAA configuration) |
| Desktop app / Cowork transcripts | same tree | same | kept at any age unless `desktopSessionCleanupPeriodDays` is set |

`CLAUDE_CONFIG_DIR` moves the whole tree. `CLAUDE_CODE_SKIP_PROMPT_HISTORY=1` (any mode) and `--no-session-persistence` (with `-p`) suppress writes. Transcripts are not encrypted at rest, and the docs warn that secrets read by tools land in them. The docs also say the entry format "is internal to Claude Code and changes between versions".

Entry fields seen by public parsers: `type` (`user`, `assistant`, `system`, `summary`, `queue-operation`, `file-history-snapshot`, `progress`), `uuid`, `parentUuid`, `timestamp` (ISO 8601), `sessionId`, `version`, `cwd`, `gitBranch`, `userType` (`"external"`), `isSidechain`, `isMeta` (slash-command expansions), `agentId`, `promptId`, `requestId`, `teamName`, `message.{role, model, content[], usage}`, and `toolUseResult` with `filePath`, `oldString`, `newString`, `structuredPatch[]` (hunks with line counts). `queue-operation` entries carry `operation` = `enqueue`, `dequeue`, `remove` (a message the user typed while the agent was working) or `popAll`. claude-code-log's model also documents `CronCreate` writing durable tasks to `.claude/scheduled_tasks.json`.

Can a lookback reader rebuild sessions? Yes, for the last 30 days by default: every user turn and assistant/tool entry has a timestamp, and `toolUseResult.structuredPatch` gives per-edit line counts. `history.jsonl` goes further back but holds only typed prompts. I could not confirm from public sources whether a `/loop`, cron or cross-session prompt is written with a distinguishing field in the transcript (unverified). The likely tell is that such prompts are absent from `history.jsonl`, which records typed input for up-arrow recall; verify on a real install before relying on it.

### 3.3 Telemetry (OpenTelemetry)

Enabled with env vars, which can be set in the `env` block of `~/.claude/settings.json`: `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `OTEL_METRICS_EXPORTER=otlp`, `OTEL_LOGS_EXPORTER=otlp`, `OTEL_EXPORTER_OTLP_PROTOCOL` (`grpc`, `http/json`, `http/protobuf`; default `http/protobuf` from v2.1.323), `OTEL_EXPORTER_OTLP_ENDPOINT` (e.g. `http://127.0.0.1:4318`). Metric export interval defaults to 60 s, logs to 5 s. Content is redacted by default: `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_DETAILS` are off.

Claude Code ignores OTel exporter variables in a repo's `.claude/settings.json`, so a repo can't redirect telemetry. A repo can still turn a signal off by setting its exporter to `none`, unless managed settings, a `--settings` file, or the launching environment sets that variable. That is a skip vector to detect (OTel silent while hooks fire).

Metrics: `claude_code.session.count` (`start_type`: `fresh`, `resume`, `continue`, `agents_view`), `claude_code.lines_of_code.count` (`type` added/removed, `model`), `claude_code.pull_request.count`, `claude_code.commit.count`, `claude_code.cost.usage` and `claude_code.token.usage` (with `query_source`: `main`, `subagent`, `auxiliary`, ...), `claude_code.code_edit_tool.decision` (`decision` accept/reject; `source`: `config`, `hook`, `user_permanent`, `user_temporary`, `user_abort`, `user_reject`), and `claude_code.active_time.total` (unit s).

`claude_code.active_time.total` is the only first-party metric in any harness I found that splits human time from agent time. The docs: "Tracks actual time spent actively using Claude Code, excluding idle time ... incremented during user interactions, such as typing and reading responses, and during CLI processing", with attribute `type` = `"user"` for keyboard interactions and `"cli"` for tool execution and AI responses. The exact idle threshold and how "reading responses" is measured are not documented (unverified). `type="user"` is the closest existing measure of human engagement inside the harness, but it is computed in the client and could be inflated by synthetic keystrokes into the terminal.

Events (OTel logs): `claude_code.user_prompt` (`prompt_length`, `prompt` only if enabled, `command_name`, `command_source`, `message.uuid`; logged "including on turns Claude Code starts on its own"), `claude_code.assistant_response`, `claude_code.tool_result`, `claude_code.api_request`, `claude_code.api_error`, `claude_code.tool_decision`, `claude_code.permission_mode_changed`, `claude_code.subagent_completed`, `claude_code.compaction`, and others. Every event has `event.timestamp`, `event.sequence`, `prompt.id`, `session.id`.

Attributes that expose automation: `app.entrypoint` = `cli`, `sdk-cli`, `sdk-ts`, `sdk-py`, `claude-vscode`, `claude-desktop`, `remote`, ... (only when `OTEL_METRICS_INCLUDE_ENTRYPOINT=true`, added v2.1.152); `terminal.type` (`iTerm.app`, `vscode`, `cursor`, `tmux`, ...); `workflow.run_id` on agents spawned by a Workflow run; `agent_id`/`parent_agent_id` on subagent events (v2.1.310+); `query_source` on cost/token metrics.

Claude Code does not pass `OTEL_*` vars to hooks or Bash subprocesses.

### 3.4 Instruction files, skills, MCP

- AGENTS.md: read natively since v2.1.277, but by default only when no `CLAUDE.md`, `.claude/CLAUDE.md` or `CLAUDE.local.md` exists in the working directory or above. With a CLAUDE.md present, AGENTS.md is ignored unless imported (`@AGENTS.md`) or the user sets Project instructions to `claude-md-and-agents-md`. `~/.claude/CLAUDE.md` always loads alongside either. `AGENTS.local.md`, `AGENTS.override.md` and `.agents/` are not read.
- The docs say instruction files are "context, not enforced configuration" and point to hooks for anything that must happen.
- Skills: `~/.claude/skills/<name>/SKILL.md` or `.claude/skills/<name>/SKILL.md`, YAML frontmatter with `name`, `description`; Claude loads one when the description matches, or the user types `/name`. A skill can carry its own hooks in frontmatter.
- MCP: `claude mcp add`, `.mcp.json` (project), `~/.claude.json` (user).
- Plugins bundle hooks, skills, MCP servers and commands; install with `/plugin` or `claude plugin`.

### 3.5 Headless and autonomous runs

| Mode | What reveals it |
|---|---|
| `claude -p` / Agent SDK | OTel `app.entrypoint` = `sdk-cli`/`sdk-ts`/`sdk-py` (opt-in attribute). Hooks still run unless `--bare`. No workspace-trust dialog. Prompt not in `history.jsonl` (unverified). Output JSON has `session_id`, `total_cost_usd`, `num_turns`. |
| `claude --bare -p` | No hooks, no CLAUDE.md, no plugins. Visible only through the transcript (unless `--no-session-persistence`) and OTel if the launching env enables it. |
| `/loop`, `CronCreate`, `ScheduleWakeup` | `UserPromptSubmit` and `claude_code.user_prompt` fire with no origin marker. `Stop.session_crons[]` lists the pending prompts. Fires only while the session is idle; recurring tasks expire after 7 days. |
| `/goal` | A session-scoped prompt-based Stop hook: each continuation turn starts without a new user prompt, so no `UserPromptSubmit` fires for it (inferred from "Stop hook continues the turn"; verify). `stop_hook_active: true` on following Stops. Transcript records evaluator verdicts. |
| Background sessions / agent view | `SessionStart source=fork`, `Notification agent_needs_input`/`agent_completed`, `start_type=agents_view`. |
| Cloud sessions (claude.ai/code) | Don't read local settings, so no local hooks; OTel `app.entrypoint=remote`, `ccr.session.id`. |
| Another agent driving it (tmux send-keys, an MCP-controlled terminal) | Nothing in the payload. Only out-of-band evidence: OS input idle, `terminal.type`, typing cadence, prompt timing. |


## 4. Cursor

Docs read as raw Markdown from cursor.com/docs on 2026-11-19 (`hooks.md`, `reference/third-party-hooks.md`, `rules.md`, `cli/headless.md`, `cli/reference/output-format.md`, `cli/changelog.md` (latest entry v2026.11.17), `enterprise/opentelemetry-export.md`). Local storage details come from wakatime-cli `pkg/ai/cursor.go` and `cursor_agent.go` at `7ab26b5` and from the agf issue and session-viz post cited in the notes. Cursor is closed source.

### 4.1 Hooks

Config: `hooks.json` with `{"version": 1, "hooks": {"<event>": [{"command": "...", "matcher": "...", "timeout": N, "failClosed": false}]}}`. Locations, all merged: enterprise (`/Library/Application Support/Cursor/hooks.json`, `/etc/cursor/hooks.json`, `C:\ProgramData\Cursor\hooks.json`), team (dashboard, Enterprise only), project `<root>/.cursor/hooks.json`, user `~/.cursor/hooks.json`, then plugins. User hooks run with cwd `~/.cursor/`. On Windows the desktop app runs hook commands through PowerShell and pipes JSON to stdin. Exit 2 blocks; other non-zero exits fail open unless `failClosed`. A non-interactive install is one JSON merge into `~/.cursor/hooks.json`.

Cursor also loads Claude Code hooks from `.claude/settings.local.json`, `.claude/settings.json` and `~/.claude/settings.json`, mapping `UserPromptSubmit` to `beforeSubmitPrompt`, `PreToolUse`/`PostToolUse`, `Stop`, `SubagentStop`, `SessionStart`, `SessionEnd`, `PreCompact` (not `Notification` or `PermissionRequest`). So a pairtime hook installed for Claude Code also fires in Cursor; the receiver must dedupe by `hook_event_name` spelling and `cursor_version`.

Common input on every agent hook: `conversation_id` (stable across turns), `generation_id` (changes with every user message), `model`, `model_id`, `model_params[]`, `hook_event_name`, `cursor_version`, `workspace_roots[]`, `user_email`, `transcript_path` (null if transcripts are disabled). No timestamp field.

| Event | When | Event-specific input |
|---|---|---|
| `sessionStart` | composer conversation created (fire-and-forget) | `session_id` (= `conversation_id`), `is_background_agent`, `composer_mode` (`agent`, `ask`, `edit`) |
| `sessionEnd` | conversation ends | `reason` (`completed`, `aborted`, `error`, `window_close`, `user_close`), `duration_ms`, `is_background_agent`, `final_status` |
| `beforeSubmitPrompt` | "right after user hits send but before backend request" | `prompt`, `attachments[]` (`type` file/rule, `file_path`) |
| `afterAgentResponse` / `afterAgentThought` | assistant message / thinking block done | `text`; thought also `duration_ms` |
| `preToolUse` / `postToolUse` / `postToolUseFailure` | around every tool | `tool_name`, `tool_input`, `tool_use_id`, `cwd`; post adds `tool_output`, `duration` |
| `beforeShellExecution` / `afterShellExecution` | shell commands | command, output |
| `afterFileEdit` | the Agent edited a file | `file_path`, `edits[]` (`old_string`, `new_string`) |
| `afterTabFileEdit` | a Tab (inline completion) edit was applied | `file_path`, `edits[]` with `range`, `old_line`, `new_line` |
| `stop` | agent loop ends | `status` (`completed`, `aborted`, `error`), `loop_count`; output `followup_message` auto-submits a new user message |
| `subagentStart` / `subagentStop` | Task subagents | `agent_transcript_path` on stop |
| `preCompact` | before compaction | context-usage numbers |
| `workspaceOpen` | a workspace or folder opens (no session) | `workspace_roots` |

What this gives pairtime that other harnesses don't: Cursor's hooks separate the three kinds of code change in the editor. `afterFileEdit` is agent-written, `afterTabFileEdit` is Tab-accepted, and anything an editor extension sees that matches neither was typed or pasted by the human. `stop.followup_message` is how loop automation works in Cursor, and `loop_count` > 0 marks the auto-continued turns (default cap 5 per script, unlimited for Claude-format hooks). Whether `beforeSubmitPrompt` fires for a `followup_message` is not documented (unverified).

Cloud agents run project `.cursor/hooks.json` hooks (command type only) but never user hooks, and skip `sessionStart`/`sessionEnd`. On self-hosted workers `sessionStart` gets a `surface` field (`desktop`, `web`, `mobile`, `slack`, `linear`, `cli`, `sdk`, `api`, `automations`) (CLI changelog).

The Cursor CLI (binary `agent`, formerly `cursor-agent`) runs the same hooks, reads Claude Code `settings.json` hooks, and emits `afterAgentThought`/`afterAgentResponse` (CLI changelog entries "Hooks", "Hooks fire reliably").

### 4.2 Local transcripts and stores

| Store | Path | Format | Notes |
|---|---|---|---|
| Agent transcripts (CLI, and IDE when enabled) | `~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl`; subagents under `<parent>/subagents/`; older builds wrote `.txt` | JSONL lines `{"role":"user"|"assistant","message":{"content":[...]}}` and `{"type":"turn_ended","status":...}`; user text wrapped in `<user_query>` | No per-line timestamp in the shape public parsers document, so time comes from file mtime or from hooks. The CLI changelog says "headless transcripts write Claude Code-compatible JSONL" |
| IDE chat store | `state.vscdb` (SQLite) under `%APPDATA%\Cursor\User\globalStorage\` (Windows), `~/Library/Application Support/Cursor/User/globalStorage/` (macOS), `~/.config/Cursor/User/globalStorage/` (Linux); per-workspace copies in `workspaceStorage/*/state.vscdb` | table `cursorDiskKV(key, value)`; `composerData:<composerId>` (`createdAt`, `lastUpdatedAt`, `name`), `bubbleId:<composerId>:<bubbleId>` (`type` 1 = user, 2 = assistant; `text`; `createdAt` epoch ms; `tokenCount`; `toolFormerData.{name,status,result}` with tool names like `edit_file_v2`, `read_file_v2`, `run_terminal_command_v2`) | Can be several GB (one user's: 4.4 GB, 641 chats, 107k messages). wakatime only reads the newest N rows. Undocumented and changes between versions |
| AI code attribution | `~/.cursor/ai-tracking/ai-code-tracking.db` (SQLite) | table `ai_code_hashes` with millisecond `timestamp`; other tables hold tracked-file snapshots and commit history (per wakatime's comment) | Cursor's own local record of which code the AI wrote; source of its team "AI code tracking" analytics (unverified link) |

A lookback reader can rebuild IDE chat sessions with per-message timestamps from `state.vscdb`, with two costs: the file is large and locked by a running Cursor (open read-only with SQLite's WAL-aware reader), and the schema is undocumented. On a 10-year-old laptop, scanning a multi-GB SQLite file is the most expensive signal in this doc; read incrementally by rowid, at most every few minutes, and only when Cursor is not the foreground app.

### 4.3 Telemetry

No local OTel exporter for individual users. OpenTelemetry Export exists only on the Enterprise plan: an admin sets a public HTTPS OTLP/HTTP endpoint and Cursor's servers push metrics and logs from fixed source IPs. It doesn't help a teen on a personal plan, and it would route data through Cursor's cloud. Team analytics and the AI Code Tracking API are also admin-only.

### 4.4 Rules, AGENTS.md, skills, MCP

- AGENTS.md: supported in the project root and nested subdirectories, combined with parent files, more specific taking precedence (`rules.md`).
- Rules: `.cursor/rules/*.mdc` with frontmatter `description`, `globs`, `alwaysApply`; `alwaysApply: true` puts the rule in every chat. User Rules (settings, global) and Team Rules (dashboard) also exist.
- Skills: `SKILL.md` folders in `.agents/skills/`, `.cursor/skills/`, `~/.agents/skills/`, `~/.cursor/skills/`, and for compatibility `.claude/skills/`, `.codex/skills/`, `~/.claude/skills/`, `~/.codex/skills/` (`skills.md`). One pairtime skill in `~/.agents/skills/` or `~/.claude/skills/` reaches Cursor too.
- MCP: `mcp.json` (project `.cursor/mcp.json`, user `~/.cursor/mcp.json`; the per-scope paths are from general knowledge, the doc names only `mcp.json`).

### 4.5 Headless and autonomous runs

| Mode | Tell |
|---|---|
| `agent -p` (print mode), `--force`/`--yolo` | Output `{"type":"result","duration_ms",...,"session_id"}`; team admins can disable print mode. Hooks fire. No field in the hook payload marks print mode (unverified). |
| Background / cloud agents | `sessionStart.is_background_agent: true`; cloud runs only project hooks; `surface` on self-hosted workers |
| Stop-hook loops | `stop.loop_count`, and a `followup_message` the tracker itself can see if it is one of the hooks |
| Agent driving Cursor through the UI | Nothing in the payloads; needs OS-level evidence |


## 5. GitHub Copilot (VS Code chat and agent mode, Copilot CLI)

Sources, read 2026-11-19: GitHub docs via `docs.github.com/api/article/body?pathname=...` for `copilot/reference/hooks-reference`, `copilot-cli-reference/cli-command-reference`, `cli-config-dir-reference`, `enterprise-administrators/opentelemetry-reference`; VS Code docs from microsoft/vscode-docs at `2d06388` (2026-11-18): `docs/agent-customization/hooks.md`, `docs/agents/guides/monitoring-agents.md`, `docs/agent-customization/custom-instructions.md`; storage from wakatime-cli `copilot.go`, `copilot_cli.go`, `copilot_sqlite.go`.

One runtime, three front ends. VS Code's chat now runs a shared "Copilot harness" that VS Code, the GitHub Copilot app and Copilot CLI all use, with one hook format. A VS Code user can also pick **Claude** or **Codex** as the session target, which then run Claude Code's or Codex's own hooks. The older "Local" harness is deprecated (`chat.useHooks`, `chat.hookFilesLocations` only configure it).

### 5.1 Hooks

Locations, all combined: policy directory (machine-wide, can't be disabled), `.github/hooks/*.json` in the repo, user `~/.copilot/hooks/*.json` (`%USERPROFILE%\.copilot\hooks\` on Windows; `$COPILOT_HOME/hooks/`), inline `hooks` in `.github/copilot/settings.json`, `.github/copilot/settings.local.json`, `~/.copilot/settings.json`, the repo's `.claude/settings.json` and `.claude/settings.local.json`, and plugin `hooks.json`. Format: `{"version": 1, "hooks": {"<event>": [{"type": "command", "bash": "...", "powershell": "...", "command": "...", "cwd": "...", "env": {}, "timeoutSec": 30}]}}`; the CLI also accepts shell-free `exec` + `args`, and `http` and `prompt` hook types exist. A non-interactive install drops one file, `~/.copilot/hooks/pairtime.json`, which covers Copilot CLI, VS Code's Copilot harness and the Copilot app.

Events: `sessionStart`, `sessionEnd`, `userPromptSubmitted`, `userPromptTransformed`, `preToolUse`, `postToolUse`, `postToolUseFailure`, `permissionRequest`, `agentStop`, `subagentStart`, `subagentStop`, `errorOccurred`, `preCompact`, `notification`. The event name picks the payload style: camelCase (`sessionStart`) gives camelCase fields and a Unix-ms `timestamp`; PascalCase (`SessionStart`) gives VS Code-compatible snake_case fields with an ISO 8601 `timestamp`. Unlike Claude Code and Cursor, every Copilot payload carries a timestamp.

| Event | Input (camelCase) |
|---|---|
| `sessionStart` | `sessionId`, `timestamp`, `cwd`, `source` (`startup`, `resume`, `new`), `initialPrompt` |
| `userPromptSubmitted` | `sessionId`, `timestamp`, `cwd`, `prompt` (command-hook output is ignored, so it's observe-only) |
| `preToolUse` / `postToolUse` | `toolName`, `toolArgs`, results |
| `agentStop` | `sessionId`, `timestamp`, `cwd`, `transcriptPath`, `stopReason` (`end_turn`), `stop_hook_active` |
| `sessionEnd` | `reason` (`complete`, `error`, `abort`, `timeout`, `user_exit`) |
| `notification` (CLI only, async) | `message`, `title`, `notification_type`: `shell_completed`, `shell_detached_completed`, `agent_completed`, `agent_idle`, `permission_prompt`, `elicitation_dialog` |
| subagent events | add `agentId`, `agentType`, `parentAgentId` |

Copilot cloud agent (the coding agent that opens PRs) runs only `bash`/`command` entries from `.github/hooks/*.json` in its Linux sandbox. There `userPromptSubmitted` fires at most once per job and `notification` never fires.

### 5.2 Local transcripts and stores

| Surface | Path | Format |
|---|---|---|
| Copilot CLI sessions | `~/.copilot/session-state/<session-id>/events.jsonl` (`$COPILOT_HOME`; Windows `%USERPROFILE%\.copilot\`); the folder also holds plans, checkpoints, tracked files | append-only JSONL, each `{type, data, timestamp}`; types include `session.start` (`data.sessionId`, `data.context.cwd`), `session.model_change`, `user.message` (`data.content`), `assistant.message`, `tool.execution_start` (`toolName`, `arguments`), `tool.execution_complete`, `session.compaction_complete`, `session.shutdown` |
| Copilot CLI typed-command history | `~/.copilot/command-history-state/` | managed, last 50 commands by default (`commandHistoryMaxSize`, max 1000) |
| Copilot CLI cross-session index | `~/.copilot/session-store.db` (SQLite) | rebuilt by `/chronicle reindex`, which also syncs session data to the user's GitHub account |
| Copilot CLI logs | `~/.copilot/logs/` | session logs |
| VS Code chat sessions | `<Code user dir>/workspaceStorage/<hash>/chatSessions/*.json` or `.jsonl`; Code user dir = `%APPDATA%\Code\User` (Windows), `~/Library/Application Support/Code/User` (macOS), `~/.config/Code/User` (Linux) | JSON: `sessionId`, `creationDate`, `lastMessageDate`, `requests[]` with `requestId`, `timestamp` (ms), `message.text`, `response[]`, `modelState.completedAt`, `agent.extensionVersion` |
| VS Code OTel span DB | `.../globalStorage/github.copilot-chat/agent-traces.db` when `github.copilot.chat.otel.dbSpanExporter.enabled` is on | SQLite spans with `start_time_ms` |

No retention limit is documented for any of these. A lookback reader can rebuild Copilot CLI sessions with per-event timestamps, and VS Code chat requests with request and completion times.

### 5.3 Telemetry

VS Code Copilot Chat: enable with `github.copilot.chat.otel.enabled: true` (user settings.json) or `COPILOT_OTEL_ENABLED=true`; `github.copilot.chat.otel.exporterType` = `otlp-http` (default), `otlp-grpc`, `console`, `file`; `otlpEndpoint` default `http://localhost:4318`; `outfile` for JSON lines; content capture off by default (`captureContent`). Env vars override settings. Copilot CLI turns on with `COPILOT_OTEL_ENABLED=true`, `OTEL_EXPORTER_OTLP_ENDPOINT`, or `COPILOT_OTEL_FILE_EXPORTER_PATH`.

Metrics that matter: `copilot_chat.edit.acceptance.count` (accept/reject, with `copilot_chat.edit.source`), `copilot_chat.lines_of_code.count`, `copilot_chat.edit.survival.four_gram` and `.no_revert` (how much accepted AI code survives), `copilot_chat.user.action.count` (copy, insert, apply, followup), `copilot_chat.user.feedback.count`, `copilot_chat.session.count`, `copilot_chat.agent.invocation.duration`, `copilot_chat.agent.turn.count`, and for the CLI `github.copilot.code.lines_added`/`lines_removed`, `github.copilot.tool.call.count`. Events: `copilot_chat.session.start`, `copilot_chat.agent.turn`, `copilot_chat.edit.feedback`, `copilot_chat.edit.hunk.action`, `copilot_chat.inline.done`, `github.copilot.session.shutdown`, `github.copilot.session.abort` ("user cancelled").

No metric measures human time. The closest signals are the accept/reject events (a human decided) and `github.copilot.initiator`, documented only as "Request initiator" on chat spans. Copilot bills premium requests by initiator, so its values plausibly split user-initiated from agent-initiated requests, but the docs don't list them (unverified).

### 5.4 Instructions, skills, MCP

Copilot CLI merges all of these at once: `CLAUDE.md`, `GEMINI.md`, `AGENTS.md` (git root and cwd), `.github/instructions/**/*.instructions.md`, `.claude/rules/**/*.md`, `.github/copilot-instructions.md`, `~/.copilot/copilot-instructions.md`, `~/.copilot/instructions/**/*.instructions.md`, and `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`. `--no-custom-instructions` turns them all off. Subagents get none unless their agent file sets `include-custom-instructions: true`. VS Code's Copilot harness reads `.github/copilot-instructions.md` or `AGENTS.md`. Skills and MCP servers are supported in both (Agent Skills, `copilot mcp`, VS Code `mcp.json`).

### 5.5 Headless and autonomous runs

| Mode | Tell |
|---|---|
| `copilot -p "..."` | Programmatic run; `--allow-all`/`--yolo`/`COPILOT_ALLOW_ALL`; `--share` writes a Markdown transcript. Whether `events.jsonl` marks print mode is not documented (unverified). |
| Autopilot: `--autopilot`, `--mode autopilot`, `/autopilot`, `/goal [objective]`, `--plan --mode autopilot` | The agent continues until `task_complete`; `--max-autopilot-continues` (default 5) bounds continuations outside an objective; `--max-ai-credits` caps an objective. `agentStop.stop_hook_active` marks forced continuations. |
| Cloud agent | Hooks only from the repo; one `userPromptSubmitted` per job; `sessionEnd.reason` `complete`/`error`/`timeout` |
| Background agents | `notification` `agent_idle`, `agent_completed` |


## 6. OpenAI Codex (CLI, IDE extension, cloud)

Sources, read 2026-11-19: docs now live at learn.chatgpt.com (developers.openai.com/codex redirects there with 308; append `.md` for Markdown): `docs/hooks.md`, `docs/config-file/config-advanced.md`, `docs/config-file/config-reference.md`, `docs/agent-configuration/agents-md.md`, `docs/non-interactive-mode.md`. Source: openai/codex (Apache-2.0) at `d64451b` (2026-11-19), latest release `rust-v0.170.0` (2026-11-18): `codex-rs/protocol/src/protocol.rs`, `codex-rs/rollout/src/{list.rs,rollout_file_name.rs,compression.rs}`, `codex-rs/history/src/rollout_payload.rs`, `codex-rs/message-history/src/lib.rs`, `codex-rs/otel/src/metrics/tags.rs`. Because Codex is open source, its formats can be pinned to a commit, unlike Claude Code's or Cursor's.

### 6.1 Hooks

Locations, all combined: system `/etc/codex/hooks.json` or `%ProgramData%\OpenAI\Codex\hooks.json`; user `$CODEX_HOME/hooks.json` (default `~/.codex/hooks.json`, `%USERPROFILE%\.codex\hooks.json`); trusted project `.codex/hooks.json`; plugin `hooks/hooks.json`; or inline `[hooks]` in the same layer's `config.toml`. Managed hooks go in `requirements.toml`. Shape matches Claude Code's: `{"hooks": {"<Event>": [{"matcher": "...", "hooks": [{"type": "command", "command": "...", "timeout": 3, "async": true}]}]}}`. Hooks are on by default; `[features] hooks = false` turns them off (`codex_hooks` is the deprecated alias).

The install friction: "Before a non-managed hook can run, Codex requires you to review and trust the exact hook definition." Trust is pinned to the hook's hash, and new or changed hooks are skipped until the user approves them in `/hooks`. So pairtime can write `~/.codex/hooks.json` silently, but it won't fire until the user opens Codex and trusts it once, and again after every change to the hook definition. Keep the definition stable (a fixed `pairtime hook codex` command line) so updates to the binary don't invalidate trust. Managed hooks in `requirements.toml` are trusted by policy but need admin rights to install. `--dangerously-bypass-hook-trust` exists for one-off automation.

Common input: `session_id` (subagents get the parent's), `transcript_path`, `cwd`, `hook_event_name`, `model`; turn-scoped events add `turn_id`; most add `permission_mode`. No timestamp.

| Event | Notes for pairtime |
|---|---|
| `SessionStart` | `source`: `startup`, `resume`, `clear`, `compact`, `fork` |
| `UserPromptSubmit` | `turn_id`, `prompt` |
| `PreToolUse` / `PermissionRequest` / `PostToolUse` | shell and `exec_command` appear as `Bash`; `apply_patch` matches `Edit`/`Write` |
| `PreModelRequest` / `PostModelRequest` | every model request attempt; post has `status`, `token_usage` |
| `Stop` | `turn_id`, `stop_hook_active`, `last_assistant_message`; `decision: "block"` creates a new continuation prompt "that acts as a new user prompt" |
| `Interrupt` | the user interrupted an active main-thread turn: a direct, human-only action signal |
| `SubagentStart` / `SubagentStop` | `agent_id`, `agent_type`, `agent_transcript_path` |
| `PreCompact` / `PostCompact` | `trigger` manual/auto |
| `SessionEnd` | archive/delete, normal exit, or 30 min idle with no client showing it; `reason` is always `other` |

Cloud: Codex Cloud and Work Cloud don't run local hooks. Enterprise "cloud orchestration" supports only admin-configured global MCP hooks.

### 6.2 Local transcripts and history

| File | Path | Format | Retention |
|---|---|---|---|
| Rollouts (one per thread) | `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-YYYY-MM-DDThh-mm-ss-<uuid>.jsonl` | JSONL lines `{"timestamp": "...", "type": "<item>", "payload": {...}}` | Kept; files untouched for 7 days are compressed in the background to `.jsonl.zst` (zstd). Archived threads move to `archived_sessions/` |
| Typed-message history | `$CODEX_HOME/history.jsonl` | `{"session_id":"<uuid>","ts":<unix_seconds>,"text":"<message>"}` | `history.persistence` = `save-all` or `none` (config reference; `save-all` is the observed default, unverified); `history.max_bytes` trims oldest lines |
| Logs | `$CODEX_HOME/log/` (`codex-tui.log` when `log_dir` is set) | text | |

Rollout item types (`type`, snake_case): `session_meta`, `turn_context`, `response_item`, `event_msg`, `compacted`, `token_usage_record`, `inter_agent_communication`, `inter_agent_communication_metadata`, `dynamic_tools_updated`, `world_state`, `retained_context`, `security_risk_score`, `realtime_item`. `event_msg` payload types used by parsers: `user_message` (`message`, `images`), `agent_message`, `token_count`, `exec_command_end`, `patch_apply_end`, `task_started`, `task_complete`, `turn_aborted`.

`session_meta.payload` is the most useful line in any harness log for telling automation apart: `id`, `session_id`, `timestamp`, `cwd`, `originator`, `cli_version`, `source`, `thread_source`, `forked_from_id`, `parent_thread_id`, `agent_role`, `agent_nickname`, `creator_user_id`. Values:

- `source` (enum `SessionSource`): `cli`, `vscode` (the serde default), `exec`, `mcp`, `custom(<string>)`, `internal` (`memory_consolidation`, `guardian`), `subagent` (`review`, `compact`, `thread_spawn{parent_thread_id, depth, agent_role}`, `memory_consolidation`, `other`).
- `thread_source`: `user`, `subagent`, `guardian_review`, `memory_consolidation`, or a feature name.
- `originator` known values: `codex_cli_rs`, `codex-tui`, `codex-cli`, `codex_exec`, `codex_vscode`, `codex_desktop`, `codex-app-server`, `codex-app-server-sdk`, `codex_sdk_ts`, `codex_mcp_server`.

A lookback reader can rebuild Codex sessions fully: per-line timestamps, the session's origin, user messages, tool runs and patches. It needs a zstd decoder for files older than a week.

### 6.3 Telemetry

`[otel]` in `~/.codex/config.toml`: `exporter = "none"` (default), `{ otlp-http = { endpoint = "...", protocol = "binary", headers = {...} } }` (the doc example uses `binary`; a JSON option is unverified) or `{ otlp-grpc = {...} }`; `log_user_prompt = false` by default; `log_agent_responses`; `metrics_exporter`. Log events: `codex.conversation_starts`, `codex.api_request`, `codex.sse_event`, `codex.websocket_request`/`_event`, `codex.user_prompt` (length; content only if enabled), `codex.agent_response`, `codex.tool_decision` (approved/denied, and whether config or user decided), `codex.tool_result`, `codex.guardian_assessment`. Metrics include `turn.e2e_duration_ms`, `turn.tool.call`, `turn.token_usage`, `tool.call`, `approval.requested` (`approved`, `approved_with_amendment`, `approved_for_session`, `denied`, `abort`), `conversation.turn.count`, `task.user_shell` (user `!` shell actions), `task.undo`, `thread.started`, `multi_agent.spawn`. Every metric carries `originator`, `session_source`, `auth_mode`, `model`, `app.version`. No metric measures human time. Separate from OTel, Codex sends anonymous usage metrics to OpenAI unless `analytics` is disabled.

### 6.4 AGENTS.md, skills, MCP

Codex is where AGENTS.md started and reads it most completely: global `~/.codex/AGENTS.override.md` or `AGENTS.md`, then one file per directory from the git root down to cwd (`AGENTS.override.md`, `AGENTS.md`, then `project_doc_fallback_filenames`), concatenated root first, capped at `project_doc_max_bytes` (32 KiB default). Discovery runs once per run, at session start. Skills (`SKILL.md`) and MCP (`[mcp_servers.<id>]` in `config.toml`) are supported; custom prompts are deprecated in favor of skills.

### 6.5 Headless and autonomous runs

| Mode | Tell |
|---|---|
| `codex exec "..."` | `session_meta.source = "exec"`, `originator = "codex_exec"`; OTel tags `session_source=exec`; `--json` streams `thread.started`, `turn.started`, `turn.completed`, `turn.failed`, `item.*`. `--ephemeral` writes no rollout at all |
| Codex SDK (TypeScript/Python) | `originator = "codex_sdk_ts"`/`codex-app-server-sdk` |
| Codex as an MCP server driven by another agent | `source = "mcp"`, `originator = "codex_mcp_server"` |
| IDE extension / desktop app | `source = "vscode"`, `originator = "codex_vscode"`/`codex_desktop` |
| Subagents, reviews, guardian | `source.subagent`, `thread_source`, `parent_thread_id`, `inter_agent_communication` items |
| Stop-hook continuation | `Stop.stop_hook_active`; the continuation prompt is written like a user prompt (check whether it reaches `history.jsonl`; unverified) |
| Codex Cloud tasks | not on the local disk; visible only as PRs/commits and in the ChatGPT UI |


## 7. Gemini CLI

Sources: google-gemini/gemini-cli (Apache-2.0) at `615d6bc` (2026-11-18), release v0.69.0 (2026-11-19): `docs/hooks/index.md`, `docs/hooks/reference.md`, `docs/cli/telemetry.md`, `docs/cli/session-management.md`, `docs/cli/headless.md`, `docs/cli/gemini-md.md`, `docs/cli/checkpointing.md`, `docs/reference/configuration.md`; storage fields from wakatime-cli `gemini.go`.

### 7.1 Hooks

Defined in the `hooks` object of `settings.json`: project `.gemini/settings.json`, user `~/.gemini/settings.json`, system `/etc/gemini-cli/settings.json`, merged. `hooksConfig.enabled` defaults to `true`. Handler: `{"type": "command", "command": "...", "name": "...", "timeout": 60000}` (ms) inside `{"matcher": "...", "sequential": false, "hooks": [...]}`. Project hooks are fingerprinted: if a project hook's name or command changes, it counts as new and untrusted and the user is warned before it runs. User-level hooks don't need that step, so pairtime installs to `~/.gemini/settings.json`. Env vars `GEMINI_PROJECT_DIR` and `CLAUDE_PROJECT_DIR` (alias) are set.

Base input on every event: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `timestamp` (ISO 8601).

| Event | Fires | Extra input |
|---|---|---|
| `SessionStart` | startup, resume, `/clear` | `source`: `startup`, `resume`, `clear` |
| `BeforeAgent` | after the user submits a prompt, before planning | `prompt` |
| `AfterAgent` | once per turn after the final response | `prompt`, `prompt_response`, `stop_hook_active`; `decision: "deny"` + `reason` sends `reason` back as a new prompt (retry loop) |
| `BeforeModel` / `AfterModel` / `BeforeToolSelection` | around each model call | request/response |
| `BeforeTool` / `AfterTool` | around each tool | tool name, args, result |
| `Notification` | system alert | `notification_type` (`ToolPermission`), `message`, `details` |
| `PreCompress` | before history compression (async) | `trigger` auto/manual |
| `SessionEnd` | exit or clear (CLI does not wait for it) | `reason`: `exit`, `clear`, `logout`, `prompt_input_exit`, `other` |

### 7.2 Local transcripts

| File | Path | Format |
|---|---|---|
| Sessions | `~/.gemini/tmp/<project_hash>/chats/session-*.json`; subagent sessions under `chats/<parent-session>/` | JSON: `sessionId`, `startTime`, `lastUpdated`, `messages[]` with `id`, `type`, `timestamp`, `content`, `tokens{input,output,cached,thoughts,tool,total}`, `model`, `toolCalls[]{id,name,args,result,status,timestamp}`; edit results carry `diffStat.model_added_lines`/`model_removed_lines`. Update records use `$set` and `$rewindTo` |
| Prompt log | `~/.gemini/tmp/<project_hash>/logs.json` | JSON array of `{sessionId, type, message, timestamp}` (the typed prompts) |
| Project map | `~/.gemini/projects.json`, `~/.gemini/tmp/<hash>/.project_root` | maps hashes back to paths |
| Checkpoints | `~/.gemini/history/<project_hash>` (shadow git repo) and `~/.gemini/tmp/<project_hash>/checkpoints` | only when checkpointing is enabled |

Retention: `general.sessionRetention.enabled` defaults to `true`, `maxAge` `"30d"`, `minRetention` `"1d"`. `~` is `%USERPROFILE%` on Windows. A lookback reader can rebuild 30 days of sessions with per-message timestamps.

### 7.3 Telemetry

`telemetry` block in `settings.json` (or env): `enabled` (`GEMINI_TELEMETRY_ENABLED`, default `false`), `target` `local`/`gcp`, `otlpEndpoint` (default `http://localhost:4317`), `otlpProtocol` `grpc`/`http`, `outfile` (overrides the endpoint), `logPrompts` (default `true`, so pairtime must set it to `false` if it doesn't want prompt text in its own stream). Common attributes: `session.id`, `installation.id`, `active_approval_mode`, `user.email`.

Log events include `gemini_cli.config` (with `output_format`, `approval_mode`, and GitHub Actions fields `github_workflow_name`, `github_event_name`, which identify CI runs), `gemini_cli.user_prompt` (`prompt_length`, `prompt_id`, `prompt`), `gemini_cli.tool_call` (with the user's decision), `gemini_cli.edit_correction`, `gemini_cli.rewind`, `gemini_cli.conversation_finished`, `gemini_cli.agent.start`/`.finish`, `gemini_cli.hook_call`, `gemini_cli.ide_connection`. Metrics include `gemini_cli.session.count`, `gemini_cli.lines.changed`, `gemini_cli.agent.duration`, `gemini_cli.agent.turns`, `gemini_cli.tool.call.count`. Nothing measures human time.

The User-Agent carries a surface tag: `terminal`, `vscode` (Gemini Code Assist agent mode, `GeminiCLI-a2a-server`), `zed`, `xcode`, `jetbrains` (via ACP), or a custom `GEMINI_CLI_SURFACE`. Gemini CLI is the agent inside Gemini Code Assist and is commonly driven from Zed and JetBrains over ACP, so one set of hooks covers those surfaces too.

### 7.4 Context files, extensions, MCP

Gemini CLI reads `GEMINI.md` by default (global `~/.gemini/GEMINI.md`, then the project hierarchy and subdirectories). It reads `AGENTS.md` only if the user sets `context.fileName`, e.g. `["AGENTS.md", "GEMINI.md"]`. So an AGENTS.md-only integration does nothing here by default; pairtime would write to `~/.gemini/GEMINI.md` or set `context.fileName`. Extensions (`gemini extensions install`) bundle MCP servers, context files and commands; MCP servers go under `mcpServers` in `settings.json`.

### 7.5 Headless

Headless when stdin is not a TTY or with `-p/--prompt`; `--output-format json` or `stream-json` (`init`, `message`, `tool_use`, `tool_result`, `error`, `result`). Hooks still fire (`SessionStart` additional context is prepended to the prompt in non-interactive mode). The `gemini_cli.config` event records `output_format` and GitHub workflow fields. `--yolo`/approval mode shows in `active_approval_mode`.


## 8. Windsurf (now Devin Desktop)

Sources, read 2026-11-19: docs.windsurf.com now redirects (307) to docs.devin.ai. Pages: `desktop/cascade/hooks.md` (which now serves "Devin Local Customizations"), `desktop/cascade.md` ("Cascade (legacy)"), `desktop/changelog.md`, `cli/extensibility/hooks/overview.md`, `cli/extensibility/hooks/lifecycle-hooks.md`; storage from wakatime-cli `windsurf.go` and `devin.go`.

What changed: "Windsurf is now Devin Desktop" (changelog v3.0.12, 2026-06-02; latest stable seen v3.14.30825). The old Cascade agent is legacy: "Devin Desktop no longer starts new Cascade conversations and no longer opens old ones. New conversations start with Devin Local." Devin Local "shares its agent harness with Devin CLI", so it uses Devin CLI's hooks, rules and skills. The Windsurf JetBrains plugin still runs Cascade. Research or code that targets Cascade's old hook names (`pre_user_prompt`, `post_cascade_response`, `.windsurf/hooks.json`) is out of date for the editor; only `post_setup_worktree` in `.windsurf/hooks.json` is still documented on the legacy page.

### 8.1 Hooks (Devin Local and Devin CLI)

| Scope | Files |
|---|---|
| Project (cwd and ancestors up to repo root) | `.devin/hooks.v1.json` (the whole file is the hooks object), `.devin/config.json` / `.devin/config.local.json` (`"hooks"` key), `.claude/settings.json`, `.claude/settings.local.json` |
| User | `~/.config/devin/config.json` (`%APPDATA%\devin\config.json` on Windows), `~/.claude.json`, `~/.claude/settings.json`, `~/.claude/settings.local.json` |

Claude-format hooks under `.claude/` load by default (`read_config_from.claude`). Events: `PreToolUse`, `PostToolUse`, `PermissionRequest`, `UserPromptSubmit` (`prompt`), `Stop` (can block to continue), `PostCompaction`, `SessionStart` (`source` `startup`/`resume`), `SessionEnd` (`reason`; not fired in cloud sessions). Every payload has `session_id` and a per-turn `prompt_id`. Handler types: `command`, `prompt`. Exit 2 or `"decision": "block"` blocks. Hooks don't load in Restricted Mode (untrusted workspace).

So the same Claude-format block pairtime writes into `~/.claude/settings.json` fires in Claude Code, in Cursor, and in Devin Desktop/Devin CLI, provided the receiver normalizes the per-harness payload differences.

### 8.2 Local stores

| Store | Path | Notes |
|---|---|---|
| Devin CLI / Devin Local transcripts | `~/.local/share/devin/transcripts/*.json` (Linux/macOS per wakatime; Windows path unverified) | JSON with a `steps` array |
| Legacy Windsurf editor state | `state.vscdb` under `.../Windsurf/User/globalStorage/` (also `Windsurf - Next`), same `cursorDiskKV` `bubbleId:` layout as Cursor | Read by wakatime; whether Devin Desktop still writes it is unverified |
| Legacy Cascade memories, global rules and workflows | `~/.codeium/windsurf/` (`global_workflows/`, memories) | config, not activity |

### 8.3 Telemetry, rules, headless

- No user-configurable OTel export found. Team analytics and the Analytics API (lines suggested/accepted, "AI-written code percentage") are Teams/Enterprise features on the vendor's servers.
- Rules: `AGENTS.md`, `AGENTS.local.md` and `AGENT.md` are always-on rules at the workspace root and every directory down to the working one; `.devin/rules/*.md` with `trigger` frontmatter (`always_on`, `glob`, `model_decision`, `manual`); `.devin/global_rules.md`; user `~/.devin/rules/*.md`; legacy `.windsurf/rules/` and `.windsurfrules` are imported.
- Skills: `.devin/skills/`, `.agents/skills/`, `~/.config/devin/skills/`, `~/.agents/skills/`. MCP: `.devin/mcp_config.json`, `~/.config/devin/mcp_config.json`.
- Headless and cloud: Devin CLI runs non-interactively and hands off to Devin Cloud (`devin --cloud`, `/handoff`); cloud sessions don't fire `SessionEnd` and run on Cognition's VMs. No documented field separates them in local hooks (unverified).


## 9. Other harnesses

Shorter treatment. Storage paths come from wakatime-cli `pkg/ai/*.go` at `7ab26b5` unless another source is named; hook details from each project's docs or source, read 2026-11-19. "Claude-compatible" means the harness reads Claude Code's hook JSON shape or files.

| Harness | Hooks / plugin API | Prompt-submit event | Local log (lookback) | Instruction files | Notes |
|---|---|---|---|---|---|
| Cline (VS Code ext, CLI, SDK v4.1.26) | File hooks: executables named after the event in `~/Documents/Cline/Hooks/` or `.clinerules/hooks/`; must tick "Enable Hooks"; README says Windows not supported. SDK plugins: `beforeRun`, `afterRun`, `beforeModel`, `afterModel`, `beforeTool`, `afterTool`, `onEvent` | `UserPromptSubmit` (stdin JSON has `clineVersion`, `hookName`, `timestamp`, `taskId`, `workspaceRoots`, `userId`) | `<editor>/User/globalStorage/saoudrizwan.claude-dev/tasks/<id>/ui_messages.json` + `api_conversation_history.json` (for Code, Cursor, Windsurf, VSCodium); CLI `~/.cline/data/sessions/*.json` | `.clinerules/`, `.cline/rules/`, `AGENTS.md`, `~/.agents/AGENTS.md`, `.cursorrules`, `.windsurfrules` | Hooks are opt-in; Windows gap |
| Roo Code | none found | none | `globalStorage/rooveterinaryinc.roo-cline/tasks/<id>/` (same layout as Cline) | reads AGENTS.md (unverified) | GitHub repo `RooCodeInc/Roo-Code` is archived (last push 2026-05-15). Log reader only, low priority |
| Kilo Code (CLI + VS Code, OpenCode-based) | TS/JS plugins: events for sessions, messages, permissions, file changes; tool interception; loaded via `plugin` array in project or user config | via message/session events | SQLite `~/.local/share/kilo/kilo.db` (Windows `%USERPROFILE%\.local\share\kilo\kilo.db`); `kilo session list --format json`, `kilo export <id>` | `AGENTS.md` (root plus per-directory, loaded lazily when files there are read), `AGENT.md` | Older extension: `globalStorage/kilocode.kilo-code/tasks/` |
| OpenCode (MIT, anomalyco/opencode) | Server plugins in `.opencode/plugins/` or config `plugins`; `ctx.event.subscribe()` event stream (e.g. `session.idle`), hooks `ctx.session.hook("prompt")`, `ctx.tool.hook("execute.before")`; v1 names `chat.message`, `tool.execute.before` | `session.hook("prompt")` | SQLite `opencode*.db` in the data dir (`~/.local/share/opencode`, `~/Library/Application Support/opencode`, `%LOCALAPPDATA%\opencode`); legacy JSON under `storage/session/` and `storage/message/<session>/` | `AGENTS.md` (native), `CLAUDE.md` fallback (unverified) | A pairtime plugin is a small JS module; runs inside OpenCode's server |
| Aider (Apache-2.0) | none (only `--lint-cmd`, `--test-cmd`) | none | In the repo root: `.aider.input.history` (typed input, with timestamp header lines) and `.aider.chat.history.md`; optional `--analytics-log <file>` JSONL | `--read CONVENTIONS.md`; no native AGENTS.md (unverified) | Git attribution on by default: `--attribute-co-authored-by` (default True) adds a `Co-authored-by` trailer; `--attribute-author`/`--attribute-committer` append "(aider)". Repo activity slowed (last push 2026-05-22) |
| Zed agent | none for Zed Agent; external agents (Claude Agent, Codex, Gemini CLI, OpenCode, Copilot, Cursor) run over ACP with their own runtimes and so their own hooks | none for Zed Agent | SQLite `threads.db`: `~/Library/Application Support/Zed/threads/threads.db`, `%LOCALAPPDATA%\Zed\threads\threads.db`, `~/.local/share/zed/threads/threads.db` | `AGENTS.md` primary; first match of `.rules`, `.cursorrules`, ..., `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`; global `~/.config/zed/AGENTS.md` / `%APPDATA%\Zed\AGENTS.md` | Zed is a host for other agents; cover those |
| Google Antigravity (App, IDE, CLI) | `.agents/hooks.json` (workspace), `~/.gemini/config/hooks.json` (global), plugin `hooks.json`; events `SessionStart`, `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop`; payload `conversationId`, `workspacePaths`, `transcriptPath`, `artifactDirectoryPath`, `modelName`, `lastUserInput`, no timestamp | none; `PreInvocation` with `invocationNum` 0 is the nearest | `~/.gemini/antigravity{,-ide,-cli}/brain/<conversationId>/.system_generated/logs/transcript.jsonl`; SQLite `~/.gemini/antigravity*/conversations/*.db` and `implicit/*.db` | rules, skills, plugins (antigravity.google/docs) | Antigravity's docs include a Gemini CLI migration page (`docs/cli/gcli-migration`), so some Gemini CLI users will move here (inferred from the URL) |
| Kiro (IDE, CLI V3, Web) | `.kiro/hooks/<id>.json` (IDE and CLI); global `~/.kiro/hooks/` (CLI V3). Triggers: Prompt Submit, Agent Stop, Session Start/End, Pre/Post Tool Use, Permission Request, File Create/Save/Delete, Pre/Post Compaction, Pre/Post Task Execution, Manual | Prompt Submit (can't block in CLI) | IDE: `<Kiro user dir>/globalStorage/kiro.kiroagent/workspace-sessions/*/*.json`; CLI: `~/.kiro/sessions/**/messages.jsonl` + `session.json` (`KIRO_HOME`) | `.kiro/steering/`, `~/.kiro/steering/`, AGENTS.md supported on all surfaces | File Save trigger is an editor-side activity signal |
| Amp | TS/JS plugins in `.amp/plugins/` or `~/.config/amp/plugins/`: `amp.on('session.start' | 'agent.start' | 'tool.call' | 'tool.result' | 'agent.end')`; `agent.start` fires when the user submits a prompt and carries `changesWorkflow` (`ship`, `push_branch`) when a button started the turn | `agent.start` | Threads live on ampcode.com; local cache `~/.cache/amp/logs/threads`, `cli.log` | `AGENTS.md` (Amp's primary format) | Execute mode `amp -x` for scripts; `-ox` spawns cloud "orbs"; automations run on a schedule |
| Warp (terminal + agent) | none for Warp Agent | none | `warp.sqlite`: `~/Library/Application Support/dev.warp.Warp-Stable/`, `%LOCALAPPDATA%\warp\Warp\data\`, `~/.local/share/warp-terminal/` | `AGENTS.md` (or legacy `WARP.md`), root and cwd | Hosts third-party CLI agents (Claude Code, Codex, OpenCode) whose hooks still apply; Warp's terminal blocks are a command-timing source (unverified) |
| JetBrains Junie (CLI, EAP) | `hooks` in `~/.junie/config.json` (project `.junie/config.json` ignored unless passed with `--config-location`); extensions with Claude-layout `hooks/hooks.json`; events `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `PermissionRequest`, `StopFailure`, `SessionEnd`; Windows runs `cmd /c` | `UserPromptSubmit` (TUI only; batch and ACP hosts skip it) | not documented | `.junie/AGENTS.md`, else root `AGENTS.md` + `.junie/rules/*.md`, else legacy `.junie/guidelines.md`; global `~/.junie/AGENTS.md` | Hook doc dated 2026-11-18; IDE plugin hooks not documented |
| JetBrains AI Assistant (2026.3) | none documented | none | not documented | `AGENTS.md` for Junie, Codex, Copilot; `CLAUDE.md` for Claude Agent | Integrates Junie, Claude Agent, Codex, GitHub Copilot, plus ACP agents; the hosted agents' own hooks apply |
| Trae | Claude-style events `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop` in `hooks.json` (user `~/.trae/hooks.json`); payload includes `session_id` | `UserPromptSubmit` | not found | toggles "Include AGENTS.md in the context" and "Include CLAUDE.md in context" | Docs render client-side; field list extracted from page data, details unverified |
| Continue (Apache-2.0) | no lifecycle hooks; `data` destinations in `config.yaml` stream "development data" events to an HTTP endpoint (POST JSON) or a `file://` directory, with `level: noCode` to strip code and prompts | `chatInteraction` event | `~/.continue/dev_data/<schema>/{chatInteraction,tokensGenerated,toolUsage,editOutcome,autocomplete...}.jsonl` (on by default); `~/.continue/sessions/sessions.json` | `.continue/rules/` | A `data` entry pointing at `http://127.0.0.1:<port>` gives pairtime a push integration with no hook |
| Goose (Apache-2.0, aaif-goose/goose) | "Open Plugins" hooks: `~/.agents/plugins/<name>/hooks/hooks.json` or `<project>/.agents/plugins/<name>/`; events `SessionStart`, `SessionEnd`, `Stop`, `UserPromptSubmit`, `PreToolUse`, `PreToolUseResult`, `PostToolUse`, `PostToolUseFailure`, `BeforeReadFile`, `AfterFileEdit`, `BeforeShellExecution`, `AfterShellExecution`; payload `event`, `session_id`, `message` | `UserPromptSubmit` | SQLite `~/.local/share/goose/sessions/sessions.db` or `%APPDATA%\Block\goose\data\sessions\sessions.db` | `.goosehints`, `AGENTS.md` (unverified) | Recipes and scheduled runs exist |

Patterns across the long tail:

- A Claude Code-shaped hook (`UserPromptSubmit`/`PreToolUse`/`PostToolUse`/`Stop`, JSON on stdin, exit 2 blocks) is now the de facto standard: Copilot, Codex, Devin, Junie, Trae, Goose and Kiro CLI all use those names, and Cursor, Copilot, Devin and Junie (via plugin layout) read Claude files directly. pairtime can write one receiver that accepts all of these shapes.
- `~/.agents/` is becoming a cross-tool home: skills (Cursor, Devin, Copilot), `AGENTS.md` (Cline), plugins with hooks (Goose).
- Three harnesses (Zed, JetBrains AI Assistant, Warp) mostly host other agents over ACP or as terminal apps. Capturing the hosted agent covers them.


## 10. Browser chat and app builders

None of these run on the user's machine, so no hook, transcript or OTel stream exists locally. What remains is the browser, the vendors' data exports, and git.

### 10.1 Browser chat (ChatGPT, Claude.ai, Gemini)

| Signal | How | What it gives | Limits |
|---|---|---|---|
| Browser extension (Chrome MV3, Firefox WebExtension) | Content script on `chatgpt.com`, `claude.ai`, `gemini.google.com`; listen for the composer's submit (Enter key / send button) and for streamed response start/end via DOM mutation | Prompt-submit timestamps, prompt length, typing duration before submit, paste vs typed (from `paste` and `keydown` events), response-complete times, time the tab stays focused while the response is read | DOM selectors break when the sites change (monthly); needs host permissions that privacy-conscious users and school-managed Chromebooks may block; one extension per browser family |
| `chrome.idle` | `chrome.idle.onStateChanged`, `queryState(seconds)`; states `active`, `idle`, `locked`; default threshold 60 s; permission `idle` (developer.chrome.com, read 2026-11-19) | Machine-wide idle, from inside the browser | Coarse; the page documents no minimum interval |
| `tabs`/`windows` focus | `chrome.tabs.onActivated`, `chrome.windows.onFocusChanged` | Which chat tab is in front, and for how long | Same as WakaTime's browser extension does for domains |
| Vendor data exports | ChatGPT: Settings > Data controls > Export (a zip with `conversations.json`; messages carry `create_time` epoch seconds, per community parsers, unverified); Claude.ai: Settings > Privacy > Export data (`conversations.json` with `created_at` per message, unverified); Gemini: Google Takeout "Gemini Apps Activity" (unverified) | Full history with timestamps, after the fact | Manual, slow (email link), all-or-nothing privacy; useful for an audit on request, not for passive tracking |

Desktop chat apps (ChatGPT desktop, Claude desktop) are Electron or native apps with no plugin API; the OS window-focus watcher (section 13.4) is the only passive signal. Claude Desktop's Code tab runs Claude Code, so Claude Code's hooks and OTel apply there (`app.entrypoint=claude-desktop`).

### 10.2 App builders (Lovable, Bolt, v0, Replit Agent) and cloud agents

| Product | What reaches the user's git | Evidence seen |
|---|---|---|
| Lovable | Two-way GitHub sync; commits are authored by `gpt-engineer-app[bot]` (Lovable was formerly GPT Engineer) | GitHub commit search, 2026-11-19: commits such as "Add options chain table UI" with author `gpt-engineer-app[bot]` |
| Replit Agent | Commits carry trailers: `Replit-Commit-Author: Agent` (agent edits), `Replit-Commit-Author: Deployment` + `Replit-Commit-Deployment-Build-Id`, `Replit-Task-Id`, `Replit-Merge-Attempt` | GitHub commit search, 2026-11-19: 20 sampled commit messages (mostly squash merges) held 166 `Replit-Commit-Author: Agent` lines and 26 `Replit-Commit-Author: Deployment` lines |
| Bolt.new (StackBlitz) | GitHub export/sync from the web IDE | author pattern unverified (search rate-limited) |
| v0 (Vercel) | GitHub sync / PRs | author pattern unverified (search rate-limited) |
| Claude Code on the web, Codex Cloud, Copilot coding agent, Cursor/Devin cloud agents | PRs and commits from the vendor's VM; Copilot's coding agent commits as `Copilot` (unverified) | Section 3.5, 6.5, 5.1 |

What these signals prove: that an agent made the change, and roughly when. They say nothing about how long the human spent steering. For app builders, pairtime's only human-time evidence is the browser extension (prompt submits, typing, focus) plus OS focus time on the builder's tab. Commit cadence and the human-authored commits between bot commits are corroborating evidence, not time.


## 11. The AGENTS.md + CLI idea, assessed

The proposal: one instruction in AGENTS.md (or a skill) tells the agent, on every user message, to run `pairtime track "<PII-stripped copy of the prompt>"`. Estimated cost: about 30 input tokens and one tool call.

### 11.1 Does each harness read the file?

| Harness | Reads AGENTS.md by default? | Where the instruction must go to load reliably |
|---|---|---|
| Claude Code | Only when no `CLAUDE.md`/`.claude/CLAUDE.md`/`CLAUDE.local.md` exists in cwd or above (v2.1.277+) | `~/.claude/CLAUDE.md` (always loads) |
| Cursor | Yes, root and nested | `~/.cursor` User Rules or a project rule with `alwaysApply: true` |
| Copilot CLI / VS Code | Yes (CLI merges AGENTS.md, CLAUDE.md, GEMINI.md, copilot-instructions) | `~/.copilot/copilot-instructions.md`; subagents get nothing unless `include-custom-instructions: true` |
| Codex | Yes, natively, once at session start | `~/.codex/AGENTS.md` |
| Gemini CLI | No; reads `GEMINI.md` unless `context.fileName` includes `AGENTS.md` | `~/.gemini/GEMINI.md` |
| Devin Desktop / Devin CLI | Yes (`AGENTS.md`, `AGENTS.local.md`, `AGENT.md`) | `~/.devin/global_rules.md` |
| Cline, Kilo, OpenCode, Zed, Warp, Junie, JetBrains AI Assistant, Amp, Kiro, Trae | Yes (Trae and Kiro via a toggle or steering) | the tool's global rules file |
| Aider | No (`--read CONVENTIONS.md`); the model can't run shell commands on its own | not workable |
| Browser chat, app builders | No local shell | not workable |

### 11.2 Cost, recomputed

The "30 tokens" figure counts only the instruction. The real per-turn cost is larger:

1. The instruction sits in context on every request (about 30 to 60 tokens, usually cache-read).
2. The model writes the tool call, including the copied prompt: output tokens roughly equal to the prompt's length plus about 30. Output tokens are the expensive kind, and a 300-token prompt costs about 330 output tokens to copy.
3. A tool call ends the model's response, so the harness must send another request carrying the whole context (cache-read) to get the real answer. That's one extra API request per turn, unless the model batches `track` alongside its first real tool call in parallel. On subscription plans it consumes rate limit; on API keys it costs money roughly proportional to context size.
4. Latency: one more round trip, typically one to a few seconds per turn.
5. Permission prompts: Claude Code asks before an unlisted Bash command, so the installer has to add `permissions.allow: ["Bash(pairtime track:*)"]`. Codex runs model commands inside its sandbox, where writes outside the workspace and network access are off by default (`sandbox_workspace_write.network_access` is opt-in), so the CLI might not reach a localhost daemon or `~/.pairtime`. Copilot CLI and Claude Code also have sandboxes. Hooks run outside the model's sandbox, so none of this applies to them.

Asking for a short summary instead of a full copy cuts item 2, but makes the record lossy and more spoofable.

### 11.3 Reliability and spoofing

- Compliance is not guaranteed. Claude Code's own docs say instruction files are "context, not enforced configuration" and send users to hooks for anything that must happen. Per-turn rituals are what agents drop first: after compaction, in long tool loops, under `/goal` or autopilot, in subagents (Copilot subagents don't get instructions by default), and in `--bare` or `--no-custom-instructions` runs. I found no published compliance rate (unverified). pairtime can measure it: on harnesses that have both, count AGENTS.md calls per hook-observed prompt.
- The model writes the "stripped" prompt, so the record shows what the model chose to report. It can paraphrase, truncate, or invent.
- Anyone can run `pairtime track "..."` from a script, in a loop, with fabricated prompts. The CLI can't tell who called it except from weak hints: the parent process chain, harness env vars such as `CLAUDE_CODE_SESSION_ID`/`CLAUDECODE` (Claude Code sets both in tool subprocesses), and matching against a hook or log record.
- A repo's own AGENTS.md (prompt injection) can tell the agent to skip the call, or to call it with fake content. Deleting the line skips it for good.
- Privacy is no better than hooks: the raw prompt already goes to the model provider, and a hook receives the raw prompt locally, where pairtime can redact it deterministically (regex for emails, phone numbers, keys, paths) instead of trusting an LLM to do it.

### 11.4 Verdict

Where a hook or a readable log exists, it beats AGENTS.md + CLI on every axis: it is deterministic, costs zero tokens, runs outside the model's sandbox, can't be skipped by the model, gives exact timestamps (Copilot, Gemini, Cline) or receiver-stamped ones, and sees prompts the model never mentions. That covers the top six harnesses and most of the long tail (section 15).

Use AGENTS.md + CLI only for:

1. Agents with no hooks and no readable log: Zed Agent (it has `threads.db`, so even there a reader exists), Warp Agent, JetBrains AI Assistant chat, and any new harness before a native integration ships. This is R7's "plugin approach" fallback.
2. A consistency check where hooks exist: a missing or mismatched `track` call is not proof of fraud, but a `track` call with no matching hook event means the call came from outside the agent.
3. Optional self-declared context, such as "what I'm working on", which only the model or user can supply.

An MCP server variant (`pairtime` MCP with a `track` tool) has the same compliance problem, but the server process itself is a passive signal: harnesses start stdio MCP servers per session or per app launch, and the `initialize` request carries `clientInfo.name`/`version` (which harness) and `roots` (which project). Claude Code also passes `CLAUDE_CODE_SESSION_ID` to stdio MCP servers. That gives session boundaries and project attribution for any MCP-capable harness with zero tokens, as long as the model never has to call the tool.


## 12. Telling a human session from headless, scheduled, and agent-driven runs

### 12.1 Fields that reveal automation, per harness

| Harness | Headless / SDK | Scheduled or self-continuing | Agent driving agent |
|---|---|---|---|
| Claude Code | OTel `app.entrypoint` `sdk-cli`/`sdk-ts`/`sdk-py` (opt-in attribute); `--bare` leaves no hooks; prompt absent from `history.jsonl` (unverified) | `Stop.session_crons[].prompt` lists what will fire; `stop_hook_active`; `/goal` evaluator verdicts in transcript; `/loop` prompts fire only when idle | `CLAUDE_CODE_CHILD_SESSION=1` and `CLAUDECODE=1` in any process Claude Code spawns; OTel `agent_id`/`parent_agent_id`, `workflow.run_id`; `isSidechain` in transcripts |
| Codex | `session_meta.source=exec`, `originator=codex_exec`/`codex_sdk_ts`/`codex-app-server-sdk`; OTel `session_source` | `Stop` continuation prompts; `stop_hook_active` | `source=mcp` (Codex run as an MCP server by another agent), `source.subagent.thread_spawn.depth`, `inter_agent_communication` items |
| Copilot | `copilot -p`; cloud agent: one `userPromptSubmitted` per job | autopilot, `/goal`, `--max-autopilot-continues`; `stop_hook_active` | subagent `agentId`/`parentAgentId`; `github.copilot.initiator` (values unverified) |
| Cursor | `agent -p`, `--force` | `stop.loop_count` > 0 marks auto follow-ups | `sessionStart.is_background_agent`; `surface` (`sdk`, `api`, `automations`, ...) on self-hosted workers |
| Gemini CLI | non-TTY or `-p`; `gemini_cli.config.output_format`, `github_workflow_name` | `AfterAgent.stop_hook_active` retries | User-Agent surface tag (`vscode`, `zed`, `jetbrains` via ACP) |
| Devin, Junie, Kiro | Junie batch host and Kiro CLI Prompt Submit semantics differ; Junie batch never fires `UserPromptSubmit` | `Stop` block loops (Junie caps at 8) | ACP host fires a subset of events (Junie: only SessionStart, PreToolUse, SessionEnd) |

None of these flags survives a determined cheater: a script can drive the interactive TUI (tmux `send-keys`, a pty wrapper, an LLM-controlled terminal) and every field above reads "interactive human". The flags catch honest automation, which is most of it, and they let pairtime label agent time correctly instead of treating it as fraud.

### 12.2 Cross-harness heuristics for "a human typed this"

| Check | Data needed | Catches | Misses |
|---|---|---|---|
| OS keyboard activity in the N seconds before a prompt-submit event | OS input hook or idle counter (section 13.4) + hook timestamp | `-p` scripts, cron, cross-session messages, remote control, prompts sent while the machine was idle or locked | Auto-typers that inject OS-level input |
| Typing time vs prompt length | keystroke count/timing in the terminal or editor before submit; prompt length from the hook | Pasted or injected prompts (a 2,000-char prompt "typed" in 1 s) | Paste is legitimate (error logs); use as a weight, not a block |
| Injected-input flag | Windows low-level hook `LLKHF_INJECTED`; macOS `CGEventGetIntegerValueField(kCGEventSourceUnixProcessID)` / event source state; X11 XTEST device | `SendInput`/AutoHotkey/xdotool auto-typers | Hardware injectors (USB HID boards), VMs, remote desktop |
| Regularity of prompt timing | prompt timestamps | `/loop`-style fixed intervals, scripted cadence | Jittered scripts |
| Near-duplicate prompts | prompt hashes or MinHash of normalized text, kept locally | Repetitive re-prompting ("try again", "fix it"), the vision post's "doing the same thing over and over" | Paraphrased repetition |
| Process tree | parent chain of the agent process at session start | an agent spawned by another agent or by a Python/Node script; `tmux`/`expect` wrappers | an attacker who launches from a normal shell and drives it remotely |
| Foreground window and focus | OS focus watcher | prompts submitted while the terminal or editor wasn't focused | focus can be kept on purpose |

Signals combine well because faking all of them at once is expensive: keystrokes with human timing, focus on the right window, varied prompts, edits in the editor between turns, and commits that match.


## 13. Human-side signals outside the agent

(pending)

## 14. Signal scorecard

(pending)

## 15. Matrix: harness by capture method

(pending)

## 16. Recommended v1 set

(pending)

## 17. Design implications

(pending)

## 18. Open questions

(pending)

## 19. Sources

(pending)
