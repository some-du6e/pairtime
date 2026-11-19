# AI harness signals: what AI-assisted building leaves behind, and how to capture it

Status: fact-checked 2026-11-19

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

Scope: what AI-assisted building leaves behind on a teen's machine, and how pairtime can capture it passively, privately, cheaply and on old Windows laptops. Covers the harnesses in the task brief (top six in depth), browser chat and app builders, and human-side signals outside the agent.

Method:

- Docs were fetched as raw Markdown wherever the vendor serves it (code.claude.com, cursor.com, learn.chatgpt.com, docs.devin.ai, kiro.dev, ampcode.com, GitHub docs via `docs.github.com/api/article/body`) and grepped directly, rather than relying on summaries. A few client-rendered pages (Antigravity, Junie, JetBrains AI Assistant, Trae) came through WebFetch summaries or raw page data and are marked where that limits confidence.
- Open-source harnesses (Codex, Gemini CLI, Cline, OpenCode, Kilo, Goose, Continue, Aider, Zed) were checked against their repositories, pinned to the commit shown in section 19.
- Local storage paths for closed-source tools come from wakatime-cli's readers (BSD-3-Clause), which are maintained against current releases.
- Nothing was read from agent transcripts or configs on this machine, and nothing was installed or run. No claim here comes from a live test; section 17 lists what needs one.

Reading conventions: field and event names are verbatim. "(unverified)" marks anything not confirmed in a primary source. Versions and dates are the ones seen on 2026-11-19; this field moves monthly, so re-check before building against a specific field.


## 2. Key findings

1. **All six top harnesses now have lifecycle hooks with a prompt-submit event**, and they have converged on Claude Code's shape (`UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, JSON on stdin, exit 2 blocks). Cursor, Devin Desktop and Copilot (repo level) even read Claude Code's own settings files. One receiver binary can cover most of the market, provided the shared Claude-format entries are `command` hooks: Cursor's loader lists only `command`/`prompt` types and Claude Code's `SessionStart` accepts no HTTP hooks (fact-check note; sections 3.1, 4.1). (sections 3 to 9)
2. **Windsurf is now Devin Desktop** (2026-06-02, v3.0.12). The Cascade agent is legacy and no longer starts new conversations; its replacement, Devin Local, uses Devin CLI's hooks and reads `~/.claude/settings.json` hooks by default. Research or code built on Cascade's `pre_user_prompt` hooks is out of date. (section 8)
3. **Only Claude Code measures human time itself**: the OTel metric `claude_code.active_time.total` has `type="user"` (keyboard interactions, reading responses) and `type="cli"` (tool execution, AI responses). No other harness exports anything comparable. (section 3.3)
4. **A prompt-submit hook doesn't prove a human typed the prompt.** Claude Code fires `UserPromptSubmit` for `/loop` iterations, background subagents and cross-session messages, with no origin field. Automation flags exist elsewhere (Codex `session_meta.source`/`originator`, Claude `app.entrypoint`, Cursor `loop_count`, Copilot autopilot), but anyone driving the interactive TUI defeats them all. Human evidence has to come from outside the agent. (sections 3.1, 12)
5. **Lookback is feasible for most harnesses**: Claude Code transcripts (30 days by default; `history.jsonl` of typed prompts kept indefinitely), Codex rollouts (no documented expiry; an off-by-default feature can zstd-compress files older than 7 days), Copilot CLI `events.jsonl` and VS Code `chatSessions`, Gemini CLI chats (30 days), Cursor's `state.vscdb` (heavy, multi-GB). That satisfies the organizer's "lookback mode counts as passive" path. (sections 3 to 7)
6. **Codex is open source and its rollout header is the best automation tell found**: `source` (`cli`, `vscode`, `exec`, `mcp`, `subagent`, ...), `originator` (`codex_exec`, `codex_sdk_ts`, ...; overridable with the `CODEX_INTERNAL_ORIGINATOR_OVERRIDE` env var, so honest-automation evidence only), `thread_source`. Its hooks, however, need a one-time user "trust" in `/hooks`, hash-pinned, before they run. (section 6)
7. **AGENTS.md + CLI is a fallback, not the core.** It costs more than 30 tokens (the prompt copy is output tokens, plus one extra model request per turn), the model can skip or falsify it, sandboxes can block it, Claude Code ignores AGENTS.md whenever a CLAUDE.md exists, and Gemini CLI doesn't read it by default. Wherever a hook or log exists, those are strictly better. (section 11)
8. **Cursor labels AI edits directly** (`afterTabFileEdit` for Tab, `afterFileEdit` for Agent). Stable VS Code can't: its change events don't carry an edit source (a proposed API does, but Marketplace extensions can't use it), and VS Code removed its own "AI vs typed characters" gauge on 2026-11-12 because agent users saw 95 to 100% AI. (sections 4.1, 13.1)
9. **OS-level presence is cheap everywhere except GNOME Wayland focus**: `GetLastInputInfo`, `CGEventSourceSecondsSinceLastEventType`, `XScreenSaverQueryInfo`, `ext-idle-notify-v1` (KWin, Sway, Hyprland, not Mutter). Windows flags injected keystrokes (`LLKHF_INJECTED`), but low-level keyboard hooks look like keyloggers to antivirus and are removed silently if slow. (section 13.4)
10. **wakatime-cli already parses 46 agents' logs** (46 parser IDs in `pkg/ai/ai.go` at v2.28.4, some covering several sources such as Copilot chat and CLI; corrected in fact-check from "about 60"; BSD-3, reusable as a map of paths and fields), but it converts agent activity into "ai coding" time, which is exactly the metric the bounty's vision post rejects. (section 13.1)
11. **Git gives attribution, not time.** Claude Code and Aider add `Co-authored-by` trailers by default, Replit adds `Replit-Commit-Author: Agent`, Lovable commits as `gpt-engineer-app[bot]`; all are removable, and commit dates are client-set. git-ai's `refs/notes/ai` standard adds line-level AI and "known human" attestations. (section 13.3)
12. **The design that holds up**: hooks plus log readers plus a local OTLP receiver for the agent side, OS idle/focus and editor typing for the human side, git as the join key, cross-source consistency checks and a hash-chained local spool for trust. Everything except aggregates stays on the device. (sections 16, 17)


## 3. Claude Code

Docs read as raw Markdown from code.claude.com on 2026-11-19 (pages `hooks`, `monitoring-usage`, `sessions`, `claude-directory`, `headless`, `memory`, `goal`, `scheduled-tasks`, `statusline`, `skills`, `env-vars`). Latest version in the changelog that day: v2.1.328. Claude Code is closed source; transcript field names below come from the docs plus two public parsers (wakatime-cli `pkg/ai/claude.go` at commit `7ab26b5`, daaain/claude-code-log `models.py` at `763365c`).

### 3.1 Hooks

Config lives in JSON settings files under the `hooks` key: `~/.claude/settings.json` (user, all projects), `.claude/settings.json` (project, committed), `.claude/settings.local.json` (project, gitignored), managed policy settings, a plugin's `hooks/hooks.json`, or skill/subagent frontmatter. Hook entries from all levels merge rather than replace. Handler types: `command`, `http` (POST of the same JSON), `mcp_tool`, `prompt`, `agent`. Not every event takes every type: `SessionStart` accepts only `command` and `mcp_tool`, and `Setup` only `command`, so even an HTTP-first design needs a command hook for session start (added in fact-check; hooks reference, "Prompt-based hooks", read 2026-11-19). A non-interactive installer only has to merge one block into `~/.claude/settings.json`; shipping as a plugin (`hooks/hooks.json`) is the cleaner, removable alternative.

Common input fields on every event: `session_id`, `prompt_id` (UUID, matches OTel `prompt.id`, absent until first user input), `transcript_path`, `cwd`, `scratchpad_dir` (v2.1.257+), `permission_mode` (`default`, `plan`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`; not sent on every event), `effort.level` (only on events with a tool-use context, such as `PreToolUse`, `PostToolUse`, `Stop`), `hook_event_name`, plus `agent_id`/`agent_type` inside subagents or `--agent` sessions. Only `SessionStart` may carry `model`, and not always; `PreModelSwitch`/`PostModelSwitch` carry `from_model`/`to_model`. No hook payload carries a timestamp: the receiver stamps arrival time itself. (Fact-checked against the hooks reference "Common input fields" table, 2026-11-19.)

Events that matter for time tracking (33 exist in total):

| Event | Fires | Payload beyond common fields | Use for pairtime |
|---|---|---|---|
| `SessionStart` | new, `resume`, `clear`, `compact`, `fork` (matcher = `source`) | `source`, `model`, `agent_type`, `session_title`; on resume/fork also `seconds_since_last_response`, `context_tokens` (v2.1.251+) | session boundaries, project via `cwd` |
| `UserPromptSubmit` | each prompt, before the model sees it | `prompt` (full text, pasted blocks expanded), `session_title` | the human-turn heartbeat; prompt hash, length |
| `UserPromptExpansion` | a typed `/command` or skill expands | `command_source` and name | slash-command use |
| `PreToolUse` / `PostToolUse` / `PostToolUseFailure` / `PostToolBatch` | around each tool call | `tool_name`, `tool_input`, `tool_use_id`, `tool_response` | agent work spans, files edited, test runs |
| `PermissionRequest` | a tool call needs approval | tool fields | the human was asked to decide |
| `Notification` | matcher `permission_prompt` (after ~6 s with no typing), `idle_prompt` (~60 s after a response with no typing), `agent_needs_input`, `agent_completed`, and others | `message`, `title`, `notification_type` | "agent waiting on human" markers |
| `Stop` | main agent finished a turn (not on user interrupt) | `stop_hook_active`, `last_assistant_message`, `background_tasks[]` (`id`, `type` such as `shell`/`subagent`/`monitor`/`workflow`, `status`, ...), `session_crons[]` (each with `id`, `schedule`, `recurring`, `prompt`; `prompt` capped at 1000 chars with a `… [+N chars]` marker) | end of agent span; list of scheduled prompts that will fire later. Stop-hook continuations are capped at 8 in a row (`CLAUDE_CODE_STOP_HOOK_BLOCK_CAP`) |
| `StopFailure` | turn ended on API error | | |
| `SubagentStart` / `SubagentStop` | subagent lifecycle | `agent_id`, `agent_type`, `agent_transcript_path` | separate agent-in-agent time |
| `SessionEnd` | exit, `/clear`, `/resume` switch, logout | `reason` (`clear`, `resume`, `logout`, `prompt_input_exit`, `other`) | close session; all SessionEnd hooks share a 1.5 s budget, raised to match a longer per-hook `timeout` up to 60 s |
| `FileChanged`, `CwdChanged`, `PreCompact`/`PostCompact`, `PreModelSwitch`/`PostModelSwitch` | as named | | minor |

Behavior that decides the design:

- `UserPromptSubmit` is not proof of a typed prompt. The docs say it also fires for a session-scoped scheduled task (including each `/loop` iteration), a background subagent reporting back, and a message another session sends. No field in the payload says which. (hooks reference, "UserPromptSubmit", 2026-11-19.) The same page still lists Code Mode script output as a fourth source, but the v2.1.328 changelog (2026-11-19) says "Changed Code Mode to no longer run UserPromptSubmit hooks ... on scripts' output" (fact-check correction: the docs page lags the release).
- `Stop` carries `session_crons[].prompt`. A tracker that remembers those prompts can flag a later `UserPromptSubmit` whose `prompt` matches as machine-started. Match on a prefix: the Stop copy is truncated at 1000 characters, the `UserPromptSubmit` copy is not. `stop_hook_active: true` marks a turn that a Stop hook (and so `/goal`) continued.
- `UserPromptSubmit` blocks the model until the hook returns (default timeout 30 s), so pairtime's hook must return in milliseconds or use `"async": true`. In `-p` mode async hooks are killed at teardown, so a hook must write its record before returning or start a detached process.
- Hooks in settings files wait for the workspace-trust dialog in interactive sessions, "including your own `~/.claude/settings.json`", so pairtime's user-level hooks only start firing in a new folder once the user accepts that dialog; they run unprompted in `-p` and SDK sessions (hooks reference, "Workspace trust", 2026-11-19).
- `--bare` (recommended for scripts, and documented as becoming the default for `-p` later) skips hooks, plugins, skills, MCP, auto memory and CLAUDE.md. A headless run in bare mode leaves no hook events and no instruction-file compliance at all. `disableAllHooks` and managed `allowManagedHooksOnly` also turn hooks off.
- Windows: shell-form hooks run in Git Bash, or PowerShell when Git Bash is missing. Exec form (`command` + `args`) needs a real `.exe`, so pairtime should ship a native binary and register it in exec form.

### 3.2 Local transcripts and history

| File | Path (all OSes, `~` = `%USERPROFILE%` on Windows) | Format | Retention |
|---|---|---|---|
| Session transcript | `~/.claude/projects/<project>/<session-id>.jsonl`; `<project>` = cwd with every non-alphanumeric char replaced by `-`, truncated at 200 chars plus a hash | JSONL, one entry per line | deleted after `cleanupPeriodDays`, default 30, minimum 1 |
| Subagent transcripts | `~/.claude/projects/<project>/<session-id>/subagents/` | same | with parent |
| Typed-prompt history | `~/.claude/history.jsonl` | JSONL: "prompts you've typed, with timestamp and project path" | kept until deleted (not swept, except under the HIPAA configuration) |
| Desktop app / Cowork transcripts | same tree | same | kept at any age unless `desktopSessionCleanupPeriodDays` is set |

`CLAUDE_CONFIG_DIR` moves the whole tree, and with it set, `CLAUDE_CODE_PROJECT_DIR_NAME` replaces the derived `<project>` name, so a reader should take the project from each entry's `cwd` rather than decode the directory name (added in fact-check; `sessions.md`, `env-vars.md`, 2026-11-19). `CLAUDE_CODE_SKIP_PROMPT_HISTORY=1` (any mode) and `--no-session-persistence` (with `-p`) suppress writes. A nested interactive `claude` started from inside another session (`CLAUDE_CODE_CHILD_SESSION` inherited) is left out of `--resume`, up-arrow history and `claude agents` by default, while a nested `claude -p` still persists its transcript (`env-vars.md`, v2.1.172+). Transcripts are not encrypted at rest, and the docs warn that secrets read by tools land in them. The docs also say the entry format "is internal to Claude Code and changes between versions".

Entry fields seen by public parsers: `type` (`user`, `assistant`, `system`, `summary`, `queue-operation`, `file-history-snapshot`, `progress`), `uuid`, `parentUuid`, `timestamp` (ISO 8601), `sessionId`, `version`, `cwd`, `gitBranch`, `userType` (`"external"`), `isSidechain`, `isMeta` (slash-command expansions), `agentId`, `promptId`, `requestId`, `teamName`, `message.{role, model, content[], usage}`, and `toolUseResult` with `filePath`, `oldString`, `newString`, `structuredPatch[]` (hunks with line counts). `queue-operation` entries carry `operation` = `enqueue`, `dequeue`, `remove` (a message the user typed while the agent was working) or `popAll`. claude-code-log's model also documents `CronCreate` writing durable tasks to `.claude/scheduled_tasks.json`.

Can a lookback reader rebuild sessions? Yes, for the last 30 days by default: every user turn and assistant/tool entry has a timestamp, and `toolUseResult.structuredPatch` gives per-edit line counts. `history.jsonl` goes further back but holds only typed prompts. I could not confirm from public sources whether a `/loop`, cron or cross-session prompt is written with a distinguishing field in the transcript (unverified). The likely tell is that such prompts are absent from `history.jsonl`, which records typed input for up-arrow recall; verify on a real install before relying on it.

### 3.3 Telemetry (OpenTelemetry)

Enabled with env vars, which can be set in the `env` block of `~/.claude/settings.json`: `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `OTEL_METRICS_EXPORTER=otlp`, `OTEL_LOGS_EXPORTER=otlp`, `OTEL_EXPORTER_OTLP_PROTOCOL` (`grpc`, `http/json`, `http/protobuf`; default `http/protobuf` from v2.1.323), `OTEL_EXPORTER_OTLP_ENDPOINT` (e.g. `http://127.0.0.1:4318`). Metric export interval defaults to 60 s, logs to 5 s. Content is redacted by default: `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_DETAILS` are off.

Claude Code ignores OTel exporter variables in a repo's `.claude/settings.json`, so a repo can't redirect telemetry. A repo can still turn a signal off by setting its exporter to `none`, unless managed settings, a `--settings` file, or the launching environment sets that variable. That is a skip vector to detect (OTel silent while hooks fire).

Metrics: `claude_code.session.count` (`start_type`: `fresh`, `resume`, `continue`, `agents_view`), `claude_code.lines_of_code.count` (`type` added/removed, `model`), `claude_code.pull_request.count`, `claude_code.commit.count`, `claude_code.cost.usage` and `claude_code.token.usage` (with `query_source`: `main`, `subagent`, `auxiliary`, ...), `claude_code.code_edit_tool.decision` (`decision` accept/reject; `source`: `config`, `hook`, `user_permanent`, `user_temporary`, `user_abort`, `user_reject`), and `claude_code.active_time.total` (unit s).

`claude_code.active_time.total` is the only first-party metric in any harness I found that splits human time from agent time. The docs: "Tracks actual time spent actively using Claude Code, excluding idle time ... incremented during user interactions, such as typing and reading responses, and during CLI processing", with attribute `type` = `"user"` for keyboard interactions and `"cli"` for tool execution and AI responses. The exact idle threshold and how "reading responses" is measured are not documented (unverified). `type="user"` is the closest existing measure of human engagement inside the harness, but it is computed in the client and could be inflated by synthetic keystrokes into the terminal.

Events (OTel logs): `claude_code.user_prompt` (`prompt_length`, `prompt` only if enabled, `command_name`, `command_source`, `message.uuid`; logged "including on turns Claude Code starts on its own"), `claude_code.assistant_response`, `claude_code.tool_result`, `claude_code.api_request`, `claude_code.api_error`, `claude_code.tool_decision`, `claude_code.permission_mode_changed`, `claude_code.subagent_completed`, `claude_code.compaction`, and others. Every event has `event.timestamp`, `event.sequence`, `prompt.id`, `session.id`.

Privacy on ingest (added in fact-check): every metric and event carries standard attributes that include `user.email` ("always included when available"), `user.account_uuid`/`user.account_id` (default on), `organization.id` and an anonymous `user.id`. pairtime's receiver must drop these before anything leaves the spool. `OTEL_METRICS_INCLUDE_REPOSITORY=true` (v2.1.269+) adds `vcs.repository.url.full`, `vcs.owner.name`, `vcs.repository.name` from the `origin` remote, which gives OTel records a project key (`monitoring-usage.md`, "Standard attributes", 2026-11-19).

Attributes that expose automation: `app.entrypoint` = `cli`, `sdk-cli`, `sdk-ts`, `sdk-py`, `claude-vscode`, `claude-desktop`, `claude-desktop-3p`, `claude-in-slack`, and for cloud sessions `remote`, `remote_desktop`, `remote_mobile` (only when `OTEL_METRICS_INCLUDE_ENTRYPOINT=true`; the changelog entry adding it is under v2.1.152); `terminal.type` (`iTerm.app`, `vscode`, `cursor`, `tmux`, ...); `workflow.run_id` on agents spawned by a Workflow run; `agent_id`/`parent_agent_id` on subagent events (v2.1.310+); `query_source` on cost/token metrics.

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

Cursor also loads Claude Code hooks from `.claude/settings.local.json`, `.claude/settings.json` and `~/.claude/settings.json`, mapping `UserPromptSubmit` to `beforeSubmitPrompt`, `PreToolUse`/`PostToolUse`, `Stop`, `SubagentStop`, `SessionStart`, `SessionEnd`, `PreCompact` (not `Notification` or `PermissionRequest`; `SubagentStart` isn't in the mapping either). So a pairtime hook installed for Claude Code also fires in Cursor; the receiver must dedupe by `hook_event_name` spelling and `cursor_version`. Fact-check additions (`reference/third-party-hooks.md`, 2026-11-19): loading depends on **Include Third-Party Plugins, Skills, and Other Configs** (Settings > Agents > Third-Party Imports, on by default), so a user can switch it off; the page lists only `command` and `prompt` hook types as supported, so a Claude-format `http` hook should be assumed not to fire in Cursor; and it doesn't document which field names Cursor sends to a Claude-format hook (unverified: test both spellings).

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
| Agent transcripts (CLI, and IDE when enabled) | `~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl`; subagents under `<parent>/subagents/`; older builds wrote `.txt` | JSONL lines `{"role": "user", "message": {"content": [...]}}` (role `user` or `assistant`) and `{"type":"turn_ended","status":...}`; user text wrapped in `<user_query>` | No per-line timestamp in the shape public parsers document, so time comes from file mtime or from hooks. The CLI changelog says "headless transcripts write Claude Code-compatible JSONL" |
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

Copilot cloud agent (the coding agent that opens PRs) runs only `bash`/`command` entries from `.github/hooks/*.json` in its Linux sandbox. There `userPromptSubmitted` fires at most once per job and `notification` never fires. Fact-check additions (hooks reference via `docs.github.com/api/article/body`, 2026-11-19): `agentStop` `decision: "block"` is capped at 8 consecutive continuations ("Runaway guard"); in autopilot a `preToolUse` `"ask"` is denied because no prompt is shown; the cloud sandbox exposes the job prompt as `COPILOT_AGENT_PROMPT`.

### 5.2 Local transcripts and stores

| Surface | Path | Format |
|---|---|---|
| Copilot CLI sessions | `~/.copilot/session-state/<session-id>/events.jsonl` (`$COPILOT_HOME`; Windows `%USERPROFILE%\.copilot\`); the folder also holds plans, checkpoints, tracked files | append-only JSONL, each `{type, data, timestamp}`; types include `session.start` (`data.sessionId`, `data.context.cwd`), `session.model_change`, `user.message` (`data.content`), `assistant.message`, `tool.execution_start` (`toolName`, `arguments`), `tool.execution_complete`, `session.compaction_complete`, `session.shutdown` (GitHub's docs confirm the folder layout and name `user.message` and `session.context_changed` as session event types; the other type names are confirmed only in wakatime-cli's parser, `copilot_cli.go` lines 334-350 at `7ab26b5`) |
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
| Rollouts (one per thread) | `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-YYYY-MM-DDThh-mm-ss-<uuid>.jsonl` | JSONL lines `{"timestamp": "...", "type": "<item>", "payload": {...}}` | Kept. A background worker can compress files untouched for 7 days to `.jsonl.zst` (zstd level 3), but only when the `local_thread_store_compression` feature is on, and at `d64451b` that feature is `Stage::UnderDevelopment`, `default_enabled: false` (`codex-rs/features/src/lib.rs`, `core/src/thread_store_builder.rs`; corrected in fact-check). Archived threads move to `archived_sessions/`. When a fork's rollout id differs from the thread id the name is `rollout-<ts>-<thread_id>_<rollout_id>.jsonl` (`rollout_file_name.rs`) |
| Typed-message history | `$CODEX_HOME/history.jsonl` | `{"session_id":"<uuid>","ts":<unix_seconds>,"text":"<message>"}` | `history.persistence` = `save-all` (the `#[default]` in `codex-rs/config/src/types.rs` at `d64451b`, confirmed in fact-check) or `none`; `history.max_bytes` trims oldest lines |
| Logs | `$CODEX_HOME/log/` (`codex-tui.log` when `log_dir` is set) | text | |

Rollout item types (`type`, snake_case): `session_meta`, `turn_context`, `response_item`, `event_msg`, `compacted`, `token_usage_record`, `inter_agent_communication`, `inter_agent_communication_metadata`, `dynamic_tools_updated`, `world_state`, `retained_context`, `security_risk_score`, `realtime_item`. `event_msg` payload types used by parsers: `user_message` (`message`, `images`), `agent_message`, `token_count`, `exec_command_end`, `patch_apply_end`, `task_started`, `task_complete`, `turn_aborted`.

`session_meta.payload` is the most useful line in any harness log for telling automation apart: `id`, `session_id`, `timestamp`, `cwd`, `originator`, `cli_version`, `source`, `thread_source`, `forked_from_id`, `parent_thread_id`, `agent_role`, `agent_nickname`, `creator_user_id`. Values:

- `source` (enum `SessionSource`): `cli`, `vscode` (the serde default), `exec`, `mcp`, `custom(<string>)`, `internal` (`memory_consolidation`, `guardian`), `subagent` (`review`, `compact`, `thread_spawn{parent_thread_id, depth, agent_path, agent_nickname, agent_role}`, `memory_consolidation`, `other`), and `unknown` for unrecognized values. Because `vscode` is the serde default, a rollout with no `source` field reads as `vscode`; don't treat that as evidence of the IDE (`codex-rs/protocol/src/protocol.rs` at `d64451b`, checked in fact-check). `SessionMeta` also carries `creator_account_id`, `runtime_workspace_roots`, `agent_path`, `model_provider` and the full `base_instructions` text.
- `thread_source`: `user`, `subagent`, `guardian_review`, `memory_consolidation`, or a feature name.
- `originator` known values: `codex_cli_rs`, `codex-tui`, `codex-cli`, `codex_exec`, `codex_vscode`, `codex_desktop`, `codex-app-server`, `codex-app-server-sdk`, `codex_sdk_ts`, `codex_mcp_server` (the OTel allowlist in `codex-rs/otel/src/metrics/tags.rs` also has `none`; anything else is tagged `other`). `originator` is client-asserted and trivially spoofed: `CODEX_INTERNAL_ORIGINATOR_OVERRIDE` in the environment wins over the built-in value (`get_originator_value` in `codex-rs/login/src/auth/default_client.rs`), and the TypeScript SDK uses exactly that variable to label itself `codex_sdk_ts` (`sdk/typescript/src/exec.ts`). Added in fact-check.

A lookback reader can rebuild Codex sessions fully: per-line timestamps, the session's origin, user messages, tool runs and patches. Ship a zstd decoder anyway, for users who turn compression on or a release that makes it default.

### 6.3 Telemetry

`[otel]` in `~/.codex/config.toml`: `exporter = "none"` (default), `{ otlp-http = { endpoint = "...", protocol = "binary", headers = {...} } }` (the doc example uses `binary`; a JSON option is unverified) or `{ otlp-grpc = {...} }`; `log_user_prompt = false` by default; `log_agent_responses`; `metrics_exporter`. Log events: `codex.conversation_starts`, `codex.api_request`, `codex.sse_event`, `codex.websocket_request`/`_event`, `codex.user_prompt` (length; content only if enabled), `codex.agent_response`, `codex.tool_decision` (approved/denied, and whether config or user decided), `codex.tool_result`, `codex.guardian_assessment`. Metrics include `turn.e2e_duration_ms`, `turn.tool.call`, `turn.token_usage`, `tool.call`, `approval.requested` (`approved`, `approved_with_amendment`, `approved_for_session`, `denied`, `abort`), `conversation.turn.count`, `task.user_shell` (user `!` shell actions), `task.undo`, `thread.started`, `multi_agent.spawn`. Wire names carry a `codex.` prefix that the docs' catalog omits ("Metric names below omit the `codex.` prefix"), so a receiver matches `codex.turn.e2e_duration_ms`, `codex.thread.started` and so on (`config-advanced.md` and `codex-rs/otel/src/metrics/names.rs`, corrected in fact-check). The docs list `auth_mode`, `model`, `app.version` as the default context fields on every metric; the source also defines `originator`, `session_source` and `service_name` tags (`tags.rs`). No metric measures human time. Separate from OTel, Codex sends anonymous usage metrics to OpenAI unless `analytics` is disabled.

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
| `/goal` (added in fact-check) | Codex has its own `/goal` (feature `goals`, `Stage::Stable`, on by default at `d64451b`; crate `codex-rs/ext/goal`). Continuation turns are injected as an internal context fragment with source `goal`, not as a user message (`ext/goal/src/steering.rs`), so they should not fire `UserPromptSubmit` (inferred; test). The rollout gets `ThreadGoalUpdated` events (serialized name unverified) and OTel gets `codex.goal.created`, `.resumed`, `.completed`, `.budget_limited`, `.usage_limited`, `.blocked`, `.token_count`, `.duration_s` |
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

`telemetry` block in `settings.json` (or env): `enabled` (`GEMINI_TELEMETRY_ENABLED`, default `false`), `target` `local`/`gcp`, `otlpEndpoint` (default `http://localhost:4317`), `otlpProtocol` `grpc` (default) or `http`, so an OTLP/HTTP-only receiver must set `otlpProtocol: "http"` and its own endpoint (default confirmed in `docs/cli/telemetry.md` at `615d6bc` during fact-check), `outfile` (overrides the endpoint), `logPrompts` (default `true`, so pairtime must set it to `false` if it doesn't want prompt text in its own stream). Common attributes: `session.id`, `installation.id`, `active_approval_mode`, `user.email`.

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
| Cline (VS Code ext, CLI, SDK v4.1.26) | File hooks: executables named after the event in `~/Documents/Cline/Hooks/` or `.clinerules/hooks/`; must tick "Enable Hooks"; README says Windows not supported. SDK plugins: `beforeRun`, `afterRun`, `beforeModel`, `afterModel`, `beforeTool`, `afterTool`, `onEvent` | `UserPromptSubmit` (stdin JSON has `clineVersion`, `hookName`, `timestamp`, `taskId`, `workspaceRoots`, `userId`). Full file-hook set: `TaskStart`, `TaskResume`, `TaskCancel` (a human action), `TaskComplete`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PreCompact` (`.clinerules/hooks/README.md`, last changed 2026-09-21, checked in fact-check) | `<editor>/User/globalStorage/saoudrizwan.claude-dev/tasks/<id>/ui_messages.json` + `api_conversation_history.json` (for Code, Cursor, Windsurf, VSCodium); CLI `~/.cline/data/sessions/*.json` | `.clinerules/`, `.cline/rules/`, `AGENTS.md`, `~/.agents/AGENTS.md`, `.cursorrules`, `.windsurfrules` | Hooks are opt-in; Windows gap |
| Roo Code | none found | none | `globalStorage/rooveterinaryinc.roo-cline/tasks/<id>/` (same layout as Cline) | reads AGENTS.md (unverified) | GitHub repo `RooCodeInc/Roo-Code` is archived (last push 2026-05-15). Log reader only, low priority |
| Kilo Code (CLI + VS Code, OpenCode-based) | TS/JS plugins: events for sessions, messages, permissions, file changes; tool interception; loaded via `plugin` array in project or user config | via message/session events | SQLite `~/.local/share/kilo/kilo.db` (Windows `%USERPROFILE%\.local\share\kilo\kilo.db`); `kilo session list --format json`, `kilo export <id>` | `AGENTS.md` (root plus per-directory, loaded lazily when files there are read), `AGENT.md` | Older extension: `globalStorage/kilocode.kilo-code/tasks/` |
| OpenCode (MIT, anomalyco/opencode) | Server plugins in `.opencode/plugins/` or config `plugins`; `ctx.event.subscribe()` event stream (e.g. `session.idle`), hooks `ctx.session.hook("prompt")`, `ctx.tool.hook("execute.before")`; v1 names `chat.message`, `tool.execute.before` | `session.hook("prompt")` | SQLite `opencode*.db` in the data dir (`~/.local/share/opencode`, `~/Library/Application Support/opencode`, `%LOCALAPPDATA%\opencode`); legacy JSON under `storage/session/` and `storage/message/<session>/` | `AGENTS.md` (native), `CLAUDE.md` fallback (unverified) | A pairtime plugin is a small JS module; runs inside OpenCode's server |
| Aider (Apache-2.0) | none (only `--lint-cmd`, `--test-cmd`) | none | In the repo root: `.aider.input.history` (typed input, with timestamp header lines) and `.aider.chat.history.md`; optional `--analytics-log <file>` JSONL | `--read CONVENTIONS.md`; no native AGENTS.md (unverified) | Git attribution on by default: `--attribute-co-authored-by` (default True) adds `Co-authored-by: aider (<model>) <aider@aider.chat>`, and while it is on, author and committer names are not modified unless `--attribute-author`/`--attribute-committer` are set explicitly (then they get " (aider)" appended) (`options.md` and `aider/repo.py` docstring on main, corrected in fact-check). Repo activity slowed (last push 2026-05-22) |
| Zed agent | none for Zed Agent; external agents (Claude Agent, Codex, Gemini CLI, OpenCode, Copilot, Cursor) run over ACP with their own runtimes and so their own hooks | none for Zed Agent | SQLite `threads.db`: `~/Library/Application Support/Zed/threads/threads.db`, `%LOCALAPPDATA%\Zed\threads\threads.db`, `~/.local/share/zed/threads/threads.db` | `AGENTS.md` primary; first match of `.rules`, `.cursorrules`, ..., `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`; global `~/.config/zed/AGENTS.md` / `%APPDATA%\Zed\AGENTS.md` | Zed is a host for other agents; cover those |
| Google Antigravity (App, IDE, CLI) | `.agents/hooks.json` (workspace), `~/.gemini/config/hooks.json` (global), plugin `hooks.json`; events `SessionStart`, `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop`; payload `conversationId`, `workspacePaths`, `transcriptPath`, `artifactDirectoryPath`, `modelName`, `lastUserInput`, no timestamp | none; `PreInvocation` with `invocationNum` 0 is the nearest | `~/.gemini/antigravity{,-ide,-cli}/brain/<conversationId>/.system_generated/logs/transcript.jsonl`; SQLite `~/.gemini/antigravity*/conversations/*.db` and `implicit/*.db` | rules, skills, plugins (antigravity.google/docs) | Antigravity's docs include a Gemini CLI migration page (`docs/cli/gcli-migration`), so some Gemini CLI users will move here (inferred from the URL) |
| Kiro (IDE, CLI V3, Web) | `.kiro/hooks/<id>.json` (IDE and CLI); global `~/.kiro/hooks/` (CLI V3). Triggers: Prompt Submit, Agent Stop, Session Start/End, Pre/Post Tool Use, Permission Request, File Create/Save/Delete, Pre/Post Compaction, Pre/Post Task Execution, Manual | Prompt Submit (can't block in CLI) | IDE: `<Kiro user dir>/globalStorage/kiro.kiroagent/workspace-sessions/*/*.json`; CLI: `~/.kiro/sessions/**/messages.jsonl` + `session.json` (`KIRO_HOME`) | `.kiro/steering/`, `~/.kiro/steering/`, AGENTS.md supported on all surfaces | File Save trigger is an editor-side activity signal |
| Amp | TS/JS plugins in `.amp/plugins/` or `~/.config/amp/plugins/`: `amp.on(<event>, ...)` for `session.start`, `agent.start`, `tool.call`, `tool.result`, `agent.end`; `agent.start` fires when the user submits a prompt and carries `changesWorkflow` (`ship`, `push_branch`) when a button started the turn | `agent.start` | Threads live on ampcode.com; local cache `~/.cache/amp/logs/threads`, `cli.log` | `AGENTS.md` (Amp's primary format) | Execute mode `amp -x` for scripts; `-ox` spawns cloud "orbs"; automations run on a schedule |
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
| Cursor | Yes, root and nested | User Rules (global, in Cursor settings) or a project rule with `alwaysApply: true` |
| Copilot CLI / VS Code | Yes (CLI merges AGENTS.md, CLAUDE.md, GEMINI.md, copilot-instructions) | `~/.copilot/copilot-instructions.md`; subagents get nothing unless `include-custom-instructions: true` |
| Codex | Yes, natively, once at session start | `~/.codex/AGENTS.md`, unless `~/.codex/AGENTS.override.md` exists: at the global level "Codex uses only the first non-empty file", so the override silently replaces pairtime's line (`agents-md.md`, added in fact-check) |
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
5. Permission prompts: Claude Code asks before an unlisted Bash command, so the installer has to add `permissions.allow: ["Bash(pairtime track *)"]` (the docs' current form; the older `Bash(pairtime track:*)` is still accepted as "an equivalent way to write a trailing wildcard", `permissions.md`, checked in fact-check). Codex runs model commands inside its sandbox, where writes outside the workspace and network access are off by default ("the default `workspace-write` sandbox mode keeps network access turned off unless you enable it", `agent-approvals-security.md`, confirmed in fact-check; key `sandbox_workspace_write.network_access`), so the CLI might not reach a localhost daemon or `~/.pairtime`. Copilot CLI and Claude Code also have sandboxes. Hooks run outside the model's sandbox, so none of this applies to them.

Asking for a short summary instead of a full copy cuts item 2, but makes the record lossy and more spoofable.

### 11.3 Reliability and spoofing

- Compliance is not guaranteed. Claude Code's own docs say instruction files are "context, not enforced configuration" and send users to hooks for anything that must happen. Per-turn rituals are what agents drop first: after compaction, in long tool loops, under `/goal` or autopilot, in subagents (Copilot subagents don't get instructions by default), and in `--bare` or `--no-custom-instructions` runs. I found no published compliance rate (unverified). pairtime can measure it: on harnesses that have both, count AGENTS.md calls per hook-observed prompt.
- The model writes the "stripped" prompt, so the record shows what the model chose to report. It can paraphrase, truncate, or invent.
- Anyone can run `pairtime track "..."` from a script, in a loop, with fabricated prompts. The CLI can't tell who called it except from weak hints: the parent process chain, harness env vars such as `CLAUDE_CODE_SESSION_ID` (set in Claude Code's tool, hook and stdio MCP subprocesses) and `CLAUDE_CODE_CHILD_SESSION` (tool and hook subprocesses only; `CLAUDECODE` is not enough, since IDE extensions set it in integrated terminals too), and matching against a hook or log record.
- A repo's own AGENTS.md (prompt injection) can tell the agent to skip the call, or to call it with fake content. Deleting the line skips it for good.
- Privacy is no better than hooks: the raw prompt already goes to the model provider, and a hook receives the raw prompt locally, where pairtime can redact it deterministically (regex for emails, phone numbers, keys, paths) instead of trusting an LLM to do it.

### 11.4 Verdict

Where a hook or a readable log exists, it beats AGENTS.md + CLI on every axis: it is deterministic, costs zero tokens, runs outside the model's sandbox, can't be skipped by the model, gives exact timestamps (Copilot, Gemini, Cline) or receiver-stamped ones, and sees prompts the model never mentions. That covers the top six harnesses and most of the long tail (section 15).

Use AGENTS.md + CLI only for:

1. Agents with no hooks and no readable log: Zed Agent (it has `threads.db`, so even there a reader exists), Warp Agent, JetBrains AI Assistant chat, and any new harness before a native integration ships. This is R7's "plugin approach" fallback.
2. A consistency check where hooks exist: a missing or mismatched `track` call is not proof of fraud, but a `track` call with no matching hook event means the call came from outside the agent.
3. Optional self-declared context, such as "what I'm working on", which only the model or user can supply.

An MCP server variant (`pairtime` MCP with a `track` tool) has the same compliance problem, but the server process itself is a weak passive signal: harnesses start stdio MCP servers per session or per app launch, and under MCP 2025-11-25 and earlier the `initialize` request carries `clientInfo` (`name`, `version`, optional `title`), which names the harness. Corrected in fact-check: `initialize` does not carry the roots, only a `capabilities.roots` flag; the server must send a `roots/list` request to learn the project folders (`docs/specification/2025-11-25/basic/lifecycle.mdx` and `client/roots.mdx`). The current spec revision, 2026-07-28, removes the `initialize` handshake entirely (clients "SHOULD identify themselves on each request" via `_meta` `io.modelcontextprotocol/clientInfo`) and deprecates Roots in favor of passing paths as tool parameters (`2026-07-28/changelog.mdx`, SEP-2575 and SEP-2577). So harness identity survives the spec change but project attribution via roots does not; fall back to the server process's cwd or environment (unverified per harness). Claude Code passes `CLAUDE_CODE_SESSION_ID` to stdio MCP servers, but the server "retains the ID it was spawned with" across `/clear`, and Claude Code describes stdio servers as "long-lived" processes that "outlive the session that spawned them" (`env-vars.md`), so server lifetime is not a reliable session boundary. Net: harness identity and a coarse "a session ran here" signal with zero tokens, as long as the model never has to call the tool.


## 12. Telling a human session from headless, scheduled, and agent-driven runs

### 12.1 Fields that reveal automation, per harness

| Harness | Headless / SDK | Scheduled or self-continuing | Agent driving agent |
|---|---|---|---|
| Claude Code | OTel `app.entrypoint` `sdk-cli`/`sdk-ts`/`sdk-py` (opt-in attribute); `--bare` leaves no hooks; prompt absent from `history.jsonl` (unverified) | `Stop.session_crons[].prompt` lists what will fire; `stop_hook_active`; `/goal` evaluator verdicts in transcript; `/loop` prompts fire only when idle | `CLAUDE_CODE_CHILD_SESSION=1` in processes Claude Code itself spawns from Bash/PowerShell/Monitor tools, hooks and status line (not stdio MCP servers); `CLAUDECODE=1` is weaker, because IDE extensions also set it in their integrated terminals, where a human types; a nested interactive `claude` is left out of `history.jsonl`; OTel `agent_id`/`parent_agent_id`, `workflow.run_id`; `isSidechain` in transcripts (env-var details corrected in fact-check against `env-vars.md`) |
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

These are the signals that show the human, rather than the agent, doing something. They matter most because every agent-side signal in sections 3 to 12 can be produced by a script.

### 13.1 Editor activity

WakaTime-style plugins send a heartbeat when the user saves, switches files, or keeps editing the same file after 2 minutes (WakaTime's plugin guide, not re-read today), and the server joins heartbeats closer than the keystroke timeout (default 15 minutes) into durations (WakaTime FAQ, per `notes/hackatime.md`). wakatime-cli v2.28.4 (released 2026-11-05, commit `7ab26b5`) also reads 46 agents' logs (section 3.2 onward) and relabels editor heartbeats as category `ai coding` when they fall within 2 minutes of AI activity and show no human line changes, or fall within 30 minutes of AI activity and before the first human edit (`pkg/ai/ai.go` lines 270-292 at `7ab26b5`; count and 30-minute rule corrected in fact-check). That measures when the agent was busy, which the vision post rejects as the metric.

VS Code extension API (stable `vscode.d.ts`, main branch, last changed in commit `9e9e76c` on 2026-10-30; enums re-checked in fact-check):

| API | Gives | Tells AI from human? |
|---|---|---|
| `workspace.onDidChangeTextDocument` | `contentChanges[]` (range, text) per edit; `reason` is only `Undo = 1` or `Redo = 2` | No. Typing, paste, inline-completion accept, and agent edits all arrive as plain changes |
| `window.onDidChangeTextEditorSelection` | `kind`: `Keyboard = 1` ("typing in the editor"), `Mouse = 2`, `Command = 3` | Partly: a change followed by a `Keyboard` selection move looks like typing; agent edits to unopened files have no editor at all |
| `window.onDidChangeWindowState` | `focused`, and `active` ("interacted with recently") | Shows the human is at this window |
| `workspace.onDidSaveTextDocument`, `onDidOpenTextDocument` | saves, opens | Agent tools also save files |
| `window.onDidStartTerminalShellExecution` / `onDidEndTerminalShellExecution` | `commandLine.value`, `cwd`, `exitCode` (needs shell integration) | Commands in the integrated terminal; agents usually run their own subprocesses instead |
| Proposed `textDocumentChangeReason` | `detailedReason.source`, e.g. `inline-completion`, `chat-edit`, `extension`, plus `metadata` | Yes, but proposed APIs can't be used by extensions published to the Marketplace (VS Code "Using Proposed API" page, not re-read today) |

VS Code itself can tell them apart: v1.103 (July 2025) shipped an experimental `editor.aiStats.enabled` that showed "the percentage of characters that was inserted by AI versus inserted by typing". VS Code issue #347841, "Remove the AI usage statistics status bar item" (closed 2026-11-12), removed it because "users who work with agents see an AI rate of 95% to 100% nearly all the time"; the same issue says "Edit source tracking and edit telemetry (`telemetry.editStats.*`) are not affected" (checked via GitHub API in fact-check). So the core keeps an edit-source signal internally, and a stable extension can only approximate it: classify a change as typed when it is small (one or a few characters), follows a keystroke-driven selection change, and coincides with OS keyboard activity; classify larger multi-line inserts with no matching keystrokes as paste, completion, or agent. Cursor's `afterTabFileEdit`/`afterFileEdit` hooks label the AI edits directly (section 4.1).

JetBrains: the IntelliJ Platform has `DocumentListener`, `CaretListener` and `TypedHandlerDelegate` (a typed-character callback distinct from programmatic edits), which allows a stronger "typed" signal than VS Code's (platform API, general knowledge; verify before relying on it). Zed has no third-party extension API for buffer events (unverified).

### 13.2 Terminal activity

| Source | Format | Notes |
|---|---|---|
| bash `~/.bash_history` | one command per line; `#<epoch>` lines only if `HISTTIMEFORMAT` is set | written at shell exit by default |
| zsh `~/.zsh_history` | `: <epoch>:<duration>;<cmd>` with `EXTENDED_HISTORY` | common on macOS |
| fish `~/.local/share/fish/fish_history` | `- cmd: ...` / `when: <epoch>` | timestamps by default |
| PowerShell (PSReadLine) | `%APPDATA%\Microsoft\Windows\PowerShell\PSReadLine\ConsoleHost_history.txt` | no timestamps; Windows default shell |
| Atuin | SQLite with timestamp, duration, exit code, cwd | opt-in tool |
| Shell integration escapes | OSC 133 (FinalTerm; Windows Terminal, iTerm2, WezTerm, kitty) and OSC 633 (VS Code) mark prompt start, command start, command end with exit code | a terminal or shell plugin can timestamp each command |
| Warp | `warp.sqlite` (section 9) | per-block command history (unverified schema) |

What it proves: commands in an interactive shell's history were typed or pasted into that shell, because agent tools run non-interactive subprocesses that don't write history. Running tests, reading logs and fixing by hand all show up here. A tmux `send-keys` driver writes to history too, so this is corroboration, not proof. Reading history files is lookback-friendly but coarse (no timestamps in bash or PowerShell by default); a shell hook (`PROMPT_COMMAND`, zsh `preexec`/`precmd`, PowerShell `PSReadLine` `AddToHistoryHandler`) gives exact per-command times and is cheap, but means editing the user's shell profile.

### 13.3 Git

| Signal | Detail |
|---|---|
| Commits | author and committer name, email, dates. Both dates are set by the client (`GIT_AUTHOR_DATE`, `GIT_COMMITTER_DATE`, `--date`), so they prove nothing about when work happened unless a server saw the push (GitHub push events, reflog on the server) |
| Diffs | size, files, churn; cheap to compute locally at commit time |
| Agent trailers | Claude Code adds `Co-Authored-By: <model name> <noreply@anthropic.com>` (for example `Claude Sonnet 5`; `Claude` or `Claude Code` when it can't name the model) and a PR line by default (`attribution`, `attribution.commit`, `attribution.pr`, `attribution.sessionUrl`; `false` hides all, v2.1.281+; `settings-reference.md`, 2026-11-19). Aider adds `Co-authored-by: aider (<model>) <aider@aider.chat>` by default (`--attribute-co-authored-by`); match on the `noreply@anthropic.com` and `aider@aider.chat` addresses rather than names. Replit adds `Replit-Commit-Author: Agent` / `Deployment`. Lovable commits as `gpt-engineer-app[bot]`. All are optional or removable, so absence proves nothing |
| git notes | `refs/notes/*`; travel only if pushed explicitly (`git push origin refs/notes/*`) |
| git-ai (Apache-2.0, ~3k stars, pushed 2026-11-17) | Records which agent, model and prompt produced each line, in git notes under `refs/notes/ai` (standard v3.0.0: "MUST be stored under the `refs/notes/ai` namespace"). Attestations cover AI sessions (`s_` keys) and "known human" authors (`h_` keys, "as observed by an IDE extension"). Prompts are "scanned and redacted, and saved outside of Git". Supports Claude Code, Codex, Cursor, Copilot and others through their hooks; no git hooks or git wrapper. It installs those agent hooks itself and "checks/updates them daily" (`git ai install-hooks`), and syncs notes on `git push`/`git fetch`, so pairtime's installer must merge alongside git-ai's entries in the same config files rather than overwrite them (README on main, checked in fact-check; repo Apache-2.0, 2,997 stars, pushed 2026-11-17) |
| Agent Trace | A Cursor-led attribution spec at github.com/cursor/agent-trace with partners including Amp, Cline, Cognition, git-ai, OpenCode, Vercel (per `notes/priorart.md`, unverified here) |

Git answers "what shipped and which lines an agent claims", which R5 (git integration) and R10 (audit) need. It does not measure time. Its best use is as the join key: pairtime's hours should attach to commits, and a reviewer should be able to ask "which hours produced this diff".

### 13.4 OS input and window focus

| Platform | Idle time | Keystroke/mouse events | Focused window | Synthetic-input tell |
|---|---|---|---|---|
| Windows 7 to 11 | `GetLastInputInfo` (tick of last input in this session; ActivityWatch's `aw-watcher-afk/windows.py` uses it) | `SetWindowsHookEx(WH_KEYBOARD_LL / WH_MOUSE_LL)`, or Raw Input (`RIDEV_INPUTSINK`), which Microsoft recommends over low-level hooks | `SetWinEventHook(EVENT_SYSTEM_FOREGROUND)` (event-driven), `GetForegroundWindow`, `GetWindowThreadProcessId`, `QueryFullProcessImageNameW`, `GetWindowTextW` | `KBDLLHOOKSTRUCT.flags` `LLKHF_INJECTED` for `SendInput`/`keybd_event` input. Low-level hooks must answer within `LowLevelHooksTimeout` (max 1000 ms from Win10 1709); on Windows 7+ a slow hook "is silently removed" (Microsoft Learn, `LowLevelKeyboardProc`, 2025-07-14), so the hook thread must only enqueue |
| macOS | `CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState, kCGAnyInputEventType)` (ActivityWatch `macos.py`), no permission needed | `CGEventTap` needs Input Monitoring permission | `NSWorkspace` `didActivateApplicationNotification` (app); window titles need Accessibility, or Screen Recording for `CGWindowListCopyWindowInfo` names | event fields `kCGEventSourceUnixProcessID` / source state ID can identify posted events (unverified; test) |
| Linux X11 | `XScreenSaverQueryInfo` (MIT-SCREEN-SAVER) | XInput2 raw events (ActivityWatch's `unix.py` uses pynput listeners) | `_NET_ACTIVE_WINDOW` on the root window, `_NET_WM_NAME`, `_NET_WM_PID` | XTEST input arrives from the "Virtual core XTEST" device (XI2 `sourceid`) |
| Linux Wayland | `ext-idle-notify-v1`: `get_idle_notification` and, since v2, `get_input_idle_notification` (input only, ignores inhibitors); supported by KWin 6.7, Sway 1.11, Hyprland 0.52.1, COSMIC, niri and others, not Mutter (GNOME) or Weston (wayland.app, 2026-11-19). GNOME: `org.gnome.Mutter.IdleMonitor.GetIdletime` over D-Bus, plus event-driven `AddIdleWatch`/`AddUserActiveWatch` with a `WatchFired` signal (`data/dbus-interfaces/org.gnome.Mutter.IdleMonitor.xml` in GNOME/mutter main, confirmed in fact-check) | none for unprivileged clients by design; reading `/dev/input` needs the `input` group | no standard protocol; GNOME needs a Shell extension, KDE a KWin script, wlroots `wlr-foreign-toplevel-management` | n/a |

What these prove: a person (or a device pretending to be one) is producing input on this machine, and which app has focus. Idle time is the minimum viable signal for "the human is present" and is enough to drop time when nobody is at the machine. Keystroke counts are a stronger signal but need consent (and on macOS an Input Monitoring prompt). pairtime should count keystrokes and their timing only, never record which keys, and attribute them to the focused app.

ActivityWatch's watchers are the reference design (MPL-2.0; Python, one process per watcher). Its RAM use on old laptops is a reason to reimplement the same calls in one native binary rather than embed it (memory figures unverified).

### 13.5 Browser extensions

Covered in section 10.1: content scripts on the chat sites for prompt-submit timing and typing-vs-paste, `chrome.idle` for presence, tab focus for attribution. The same extension can see app builders (Lovable, Bolt, v0, Replit) because they are web apps.


## 14. Signal scorecard

CPU and RAM figures are estimates for a 2016-era Windows 10 laptop (dual-core i3/i5, 4 GB RAM, often an HDD), assuming pairtime is one native daemon plus a tiny native hook binary. They are not measurements; benchmark before quoting them.

| Signal | What it proves about the human | How it is faked | Privacy cost | Platforms | Cost on an old laptop |
|---|---|---|---|---|---|
| Prompt-submit hook (`UserPromptSubmit`, `beforeSubmitPrompt`, `userPromptSubmitted`, `BeforeAgent`) | A prompt entered the agent at time T, with this text and length. Not that a human typed it: Claude Code fires it for `/loop`, background agents and cross-session messages too | Script drives the TUI or SDK; LLM writes prompts; loop features | Raw prompt reaches pairtime locally; keep a hash, length and local-only text | All top harnesses, Win/Mac/Linux (Cline file hooks: not Windows) | Command hook = one process spawn per event: about 10 to 30 ms and under 5 MB for a native exe, 200+ ms and 30+ MB for Node/Python. HTTP hooks (Claude Code; Copilot with `COPILOT_HOOK_ALLOW_LOCALHOST=1`) avoid the spawn, but not for Claude Code `SessionStart` (command or `mcp_tool` only) or for Claude-format hooks run by Cursor (fact-check note) |
| Tool and stop hooks (`PreToolUse`, `PostToolUse`, `Stop`, `afterFileEdit`) | When the agent worked, on which files, with what result. Agent time, not human time | Same as above | File paths, commands, diffs | Same | Fires many times per turn; register only the events needed, use matchers, or read the transcript instead |
| Human-decision hooks (`PermissionRequest`, Claude `Notification permission_prompt`/`idle_prompt`, Codex `Interrupt`, accept/reject OTel events) | A decision point was presented, and when it was answered; latency between prompt and answer is human reaction time | Auto-approve modes (`--yolo`, `bypassPermissions`, `--force`) remove the prompts; a script can answer them | Low | Most | Rare events; negligible |
| Transcript/log lookback reader | Complete session history with per-message timestamps (Claude Code, Codex, Copilot, Gemini, Cursor DB) | Replayed or synthesized log files; edited timestamps; logs written by a script that never called a model | Highest: full prompts, code, tool output, secrets the agent read. Must stay local | All OSes; formats undocumented and versioned except Codex (open source) | JSONL tail via file watcher (`ReadDirectoryChangesW`/FSEvents/inotify): negligible. Cursor `state.vscdb` (can be GBs): heavy I/O on HDD, read incrementally by rowid and rate-limit. Codex `.jsonl.zst` decode only on backfill |
| Claude Code OTel `claude_code.active_time.total{type=user}` | Client-measured seconds of user keyboard interaction and reading inside Claude Code | Synthetic keystrokes into the terminal; the metric is computed client-side | Low (no content by default) | Claude Code only, all OSes | Local OTLP/HTTP receiver: one batch per 60 s (metrics) and per 5 s (logs); negligible |
| Other OTel (Copilot edit accept/reject, survival; Codex `approval.requested`, `task.user_shell`; Gemini tool decisions) | Human decisions on AI output; how much accepted AI code survives | As above | Low | Copilot (VS Code, CLI), Codex, Gemini CLI | Negligible |
| AGENTS.md + `pairtime track` | That the model followed an instruction | Trivially: run the CLI from a script; prompt injection; deleted line | Model-chosen text | Any harness with a shell tool | One extra model request per turn (tokens, latency) |
| MCP server presence | Harness X started an MCP session (`clientInfo`); project only via a `roots/list` round trip on older spec versions (Roots deprecated in MCP 2026-07-28) or the process cwd | Start the MCP client yourself | Low | Every MCP-capable harness | One idle process per harness session, a few MB |
| Editor typing (small keystroke-driven edits) | The human typed code in this file at time T | Auto-typers (`SendInput`, xdotool) replaying code; injected edits via extension | File names, edit sizes; never store text | VS Code family, JetBrains; Zed limited | Event-driven; negligible if the extension debounces |
| AI-edit labels (Cursor `afterTabFileEdit`/`afterFileEdit`, VS Code proposed API, git-ai) | Which edits came from AI | Disable the hook; edit outside the editor | Low | Cursor; VS Code Insiders only; git-ai where installed | Negligible |
| OS idle time | Someone touched keyboard or mouse in the last N seconds | Mouse jigglers, auto-typers, any injected input | Very low | Win `GetLastInputInfo`, mac `CGEventSourceSecondsSinceLastEventType`, X11 `XScreenSaverQueryInfo`, Wayland `ext-idle-notify-v1` (not GNOME, which needs Mutter's D-Bus IdleMonitor) | 1 Hz poll: negligible; no AV risk |
| OS keystroke/mouse counts and timing | Human-like input cadence in the focused app; injected input is flagged on Windows (`LLKHF_INJECTED`) and X11 (XTEST device) | Hardware HID injectors (USB boards), VMs, remote desktop | Medium: even counts reveal activity patterns; never log key identities | Windows LL hook or Raw Input; macOS event tap (Input Monitoring prompt); X11 XI2; Wayland not possible unprivileged | Negligible CPU, but low-level keyboard hooks in unsigned binaries look like keyloggers to antivirus heuristics; make it opt-in and code-sign |
| Window focus | Which app, and which project window, has the user's attention | Leave the window focused while a script runs | Window titles leak document and site names; store app + project, hash titles | Win `SetWinEventHook(EVENT_SYSTEM_FOREGROUND)`, mac `NSWorkspace` (titles need Accessibility), X11 `_NET_ACTIVE_WINDOW`; Wayland needs per-compositor helpers | Event-driven on Windows and macOS: negligible |
| Shell history / shell hook | Commands typed or pasted into an interactive shell (tests, git, running the app) | tmux `send-keys`, scripted shells | Commands can contain secrets; store command name and timing only by default | bash, zsh, fish, PowerShell (no timestamps), Atuin | Shell hook: microseconds per command; history parse: negligible |
| Git commits, diffs, trailers, notes | What shipped; which commits agents claim; links hours to output | Client-side dates are forgeable; trailers are removable | Code stays local; send hashes and stats | All | Negligible at commit time |
| Browser extension on chat and builder sites | Prompt-submit timing, typing vs paste, tab focus for web-only work | Same as other UI automation; DOM-level scripts | Prompt text visible to the extension; keep local | Chrome/Edge/Firefox; managed Chromebooks may block | Content script: low; one extension per browser |
| Vendor data exports | Full web-chat history after the fact | Exports can be edited before upload | Everything | ChatGPT, Claude.ai, Google Takeout | Manual |

The pattern: everything an agent writes about itself is forgeable by a script, and everything the OS sees is forgeable with injected input. They fail differently, though. A cheater has to fake both, consistently, at once: a believable transcript, keystroke cadence matching the prompt lengths, focus on the right window, editor edits between turns, and commits that match the diffs. pairtime's trust comes from cross-checking independent sources, not from any single one.


## 15. Matrix: harness by capture method

Cell = coverage / build effort. Coverage: **Y** works for passive time tracking, **P** partial (caveat in note), **N** not available, **-** not applicable. Effort: S (days), M (one to two weeks), L (more, or ongoing breakage). "Editor extension" means a pairtime VS Code-family or JetBrains extension that sees the human's typing alongside the agent.

| Harness | Hook | Log reader (lookback) | OTel receiver | AGENTS.md + CLI | MCP server | Editor extension |
|---|---|---|---|---|---|---|
| Claude Code (CLI, VS Code ext, Desktop Code tab) | Y / S (command, or HTTP except on `SessionStart`; plugin packaging; fires only after the folder's trust dialog) | Y / M (`~/.claude/projects`, `history.jsonl`; undocumented format) | Y / S (only harness with human-time metric) | P / S (only via `~/.claude/CLAUDE.md`) | P / S | P (VS Code ext host) |
| Cursor (IDE, `agent` CLI) | Y / S (`~/.cursor/hooks.json`; Tab vs Agent edit hooks) | P / L (multi-GB `state.vscdb`; JSONL transcripts lack timestamps) | N (Enterprise cloud only) | P / S | P / S | Y / M (VS Code fork; Open VSX) |
| GitHub Copilot (VS Code, CLI, app) | Y / S (`~/.copilot/hooks/*.json`, timestamps included) | Y / M (`events.jsonl`, `chatSessions/`) | Y / S (edit accept/reject, survival) | P / S | P / S | Y / M |
| OpenAI Codex (CLI, IDE ext, desktop) | P / S (works, but each hook needs one-time user trust in `/hooks`) | Y / S (open-source format; `source`/`originator` fields) | P / S (no human-time metric) | Y / S (native `~/.codex/AGENTS.md`) | P / S | P (IDE ext) |
| Gemini CLI | Y / S (`~/.gemini/settings.json`, timestamps included) | Y / S (`~/.gemini/tmp/*/chats`, `logs.json`) | P / S (`logPrompts` defaults on) | P / S (needs `GEMINI.md` or `context.fileName`) | P / S | - |
| Windsurf → Devin Desktop / Devin CLI | Y / S (reads `~/.claude/settings.json` hooks; or `~/.config/devin/config.json`) | P / M (`~/.local/share/devin/transcripts`; Windows path unknown) | N | P / S | P / S | Y / M (VS Code fork) |
| Cline | P / S (opt-in; Windows unsupported for file hooks) | Y / S (`tasks/<id>/*.json`) | P (enterprise OTel docs exist; unverified) | P / S | P / S | (it is an extension) |
| Kilo Code, OpenCode | Y / M (JS/TS plugin) | Y / M (SQLite) | N | Y / S | P / S | - |
| Zed agent | N | Y / M (`threads.db`) | N | Y / S (only in-agent path) | P / S | N (no buffer-event API found) |
| Antigravity | P / S (no prompt event) | P / M (`brain/.../transcript.jsonl`, per-conversation SQLite) | N | P / S | P / S | P (VS Code-based IDE, unverified) |
| Kiro | Y / S (`.kiro/hooks`, `~/.kiro/hooks` for CLI) | P / M | N | Y / S | P / S | P |
| Amp | Y / M (JS plugin) | P (threads are server-side) | N | Y / S | P / S | - |
| Warp agent | N | P / M (`warp.sqlite`) | N | Y / S | P / S | - |
| JetBrains Junie CLI / AI Assistant | P / S (Junie CLI hooks; IDE undocumented) | N | N | Y / S | P / S | Y / L (JetBrains plugin) |
| Trae | P / S (Claude-style; details unverified) | N | N | P / S | P / S | P |
| Continue | P / S (`data` destination POST) | Y / S (`~/.continue/dev_data`) | N | P / S | P / S | (it is an extension) |
| Goose | Y / S (`~/.agents/plugins/*/hooks/hooks.json`) | Y / S (`sessions.db`) | N | P / S | Y / S | - |
| Aider | N | Y / S (`.aider.input.history` in repo) | N | N | N | - |
| ChatGPT / Claude.ai / Gemini web | - | P (manual exports) | - | N | - | browser extension Y / M |
| Lovable, Bolt, v0, Replit | - | via git only | - | N | - | browser extension P / M |

Two columns that apply to every row: OS idle + focus watcher (Y / M, all platforms except per-compositor Wayland focus) and git integration (Y / S).


## 16. Recommended v1 set

Ordered by value per unit of work.

1. **One local daemon** (native, single binary per OS) that owns: a spool on disk, a localhost receiver (HTTP for hooks, OTLP/HTTP for telemetry), the OS idle + focus watcher, and the log readers. Budget: idle CPU near zero, under 30 MB RAM.
2. **A tiny native hook binary** (`pairtime hook <harness>`) registered in exec form. It reads stdin, stamps the arrival time, appends one line to the spool (or POSTs to the daemon), and exits in milliseconds. Fail-open always: a dead daemon must never block the agent.
3. **Hooks for the top six, installed by `pairtime install`:**
   - Claude Code: `~/.claude/settings.json` (or a plugin) for `SessionStart`, `UserPromptSubmit`, `Stop`, `StopFailure`, `Notification`, `PermissionRequest`, `PostToolUse` (matcher `Edit|Write|Bash`), `SubagentStart`/`Stop`, `SessionEnd`. Register them as exec-form `command` hooks: that is the one type every consumer of this file runs (Claude Code `SessionStart` rejects `http`; Cursor's loader lists only `command`/`prompt`). Cursor (when its third-party import setting is on, the default) and Devin Desktop read the same file at user level; dedupe by harness. In interactive Claude Code these hooks start firing in a new folder only after the user accepts its trust dialog. (Fact-check additions.)
   - Cursor: `~/.cursor/hooks.json` adds `beforeSubmitPrompt`, `afterTabFileEdit`, `afterFileEdit`, `afterAgentResponse`, `stop`, `sessionStart`/`End`.
   - Copilot: `~/.copilot/hooks/pairtime.json` (camelCase events, for the timestamps).
   - Codex: `~/.codex/hooks.json`, plus a first-run notice telling the user to trust it in `/hooks`; until trusted, fall back to the rollout reader.
   - Gemini CLI: `~/.gemini/settings.json` hooks; set `telemetry.logPrompts=false` if enabling OTel.
4. **Claude Code OTel** via the `env` block of `~/.claude/settings.json` pointing at the daemon (`OTEL_EXPORTER_OTLP_PROTOCOL=http/json` or `http/protobuf`, `OTEL_METRICS_INCLUDE_ENTRYPOINT=true`, `OTEL_METRICS_INCLUDE_REPOSITORY=true` for a project key, prompt logging off). Drop `user.email`, `user.account_uuid`/`user.account_id` and `organization.id` at ingest; Claude Code attaches them by default (fact-check addition). Store `active_time{type=user}` and `{type=cli}` per session as a reference measure to calibrate pairtime's own estimate against.
5. **Lookback readers** for Claude Code, Codex, Copilot CLI, VS Code Copilot chat and Gemini CLI. They backfill when hooks weren't installed yet (R4's "lookback" path) and give a second, independent record to compare hooks against. Cursor's DB reader is v1.5: high value but heavy and fragile. The Codex reader should accept `.jsonl.zst` even though compression is off by default today (fact-check correction, section 6.2).
6. **OS signals:** idle time and foreground app on all three OSes (Windows `GetLastInputInfo` + `SetWinEventHook`; macOS `CGEventSourceSecondsSinceLastEventType` + `NSWorkspace`; X11 and `ext-idle-notify-v1`). Keystroke counts are opt-in.
7. **Git:** per-commit stats at commit time (via the daemon watching `.git` or a `post-commit` hook), agent trailers, and reading git-ai notes when present. Hours attach to commits.
8. **AGENTS.md + CLI** only as the fallback integration for harnesses with nothing else, shipped as a documented "plugin" for R7.

Deferred to v2: VS Code and JetBrains extensions for typed-vs-AI edits, the browser extension for web chat and app builders, OpenCode/Kilo/Amp plugins, Cursor DB reader, shell-hook integration.


## 17. Design implications

Each recommendation is tagged with the requirements it serves.

1. **Hooks first, log readers second, AGENTS.md last.** Hooks are deterministic, cost no tokens, run outside the model's sandbox and can't be skipped by the model; logs give lookback and an independent second record; AGENTS.md + CLI is only the fallback for harnesses with neither. [R4, R7, R8]
2. **Hours come from human-side evidence; agent events only frame them.** Credit a minute when the session is live (agent events in the last N minutes) and there is human evidence in that minute: OS input with the terminal, editor or chat tab of that project focused, a prompt submit, a permission answer, an interrupt, a typed edit, a shell command. Agent-only spans, where the agent runs and the human gives no input, count as agent time: shown to the user, never submitted as struggle. This is the concrete form of "count the human, not the agent's runtime", and it means `/loop`, `/goal`, autopilot and `-p` runs earn nothing on their own. [R2, R9]
3. **Reading and reviewing need an explicit, capped rule.** After a `Stop`/`agentStop`/`AfterAgent`, give credit for focus-plus-input time on the agent's window up to a cap scaled by response length (reading speed), and for diff review in the editor. Watching a spinner with no input earns nothing. Calibrate the caps against Claude Code's `active_time{type=user}`, which measures "typing and reading responses" client-side. [R2, R3]
4. **Label automated turns at ingest** with every flag in section 12.1 (`app.entrypoint`, Codex `source`/`originator`/`thread_source`, `Stop.session_crons` prompt matches, `stop_hook_active`, Cursor `loop_count`/`is_background_agent`, Copilot autopilot, Gemini `output_format`, Codex `/goal`, nested-agent env and process tree), and show them in the UI. Weigh them as self-reported: Codex `originator` is overridable with `CODEX_INTERNAL_ORIGINATOR_OVERRIDE`, and `CLAUDECODE=1` also appears in IDE terminals where a human types, so prefer `CLAUDE_CODE_CHILD_SESSION` (fact-check correction). Most automation is honest, and labeling it beats treating it as fraud. [R2, R9, R3]
5. **Detect dark flow and repetition locally.** Keep MinHash signatures of normalized prompts on the device; down-weight runs of near-duplicate prompts with no edits, tests or reading between them, and show the user when it happens, which is useful feedback in its own right. [R2, R3, R9]
6. **Normalize everything into one event schema** (`session`, `turn`, `prompt`, `tool`, `edit{source: human|tab|agent|unknown}`, `decision`, `stop`, `input_activity`, `focus`, `commit`), each event tagged with its source (hook, log, otel, os, git, cli) and harness. The "plugin approach" in R7 is this schema plus a documented spool format, so a new harness needs only an adapter. [R7]
7. **Treat every undocumented format as unstable.** Pin parsers to versions, keep fixture tests per harness version, and record parse failures locally. Prefer documented surfaces (hooks, OTel, Codex's open-source rollout format) over Claude Code's and Cursor's internal formats, which their own docs say change between releases. wakatime-cli's BSD-3 readers are a vetted starting point for paths and fields. [R7, R8]
8. **Cross-source consistency is the anti-fraud core.** For each claimed hour, check that hooks, logs, OTel, OS input, editor edits and git agree: prompts with no preceding keystrokes, transcripts with no hook events, hook events with no OS input, typing speed that's physically impossible, and commits whose diffs don't match recorded edits all lower trust. Report a per-hour trust score instead of a binary verdict. [R9, R10]
9. **Make local records tamper-evident.** Hash-chain the spool (each record carries the previous record's hash) and upload the chain head every few minutes, so a replayed or backdated log can't later match what the server saw. The cost is one hash per upload. [R9, R10, R11]
10. **Two privacy tiers.** Logged data stays local: raw prompts, transcripts, file names, window titles. The server gets per-minute aggregates per project plus hashes. Only at submission does pairtime build a redacted evidence pack (timeline, aggregates, prompt hashes or user-approved excerpts, commit links) that reviewers can check against the anchored hashes. [R6, R10]
11. **Redact on the device, deterministically.** Hooks hand pairtime the raw prompt; strip emails, phone numbers, keys, tokens and home paths with local rules before anything is stored beyond the spool. Don't rely on the model to strip PII. [R6]
12. **Build for the 10-year-old Windows laptop first.** One native binary per OS; hooks in exec form by default, with HTTP to localhost only as an optimization where the event and every consumer of the file accept it (Claude Code except `SessionStart`/`Setup`; Copilot needs `COPILOT_HOOK_ALLOW_LOCALHOST=1`; not Claude-format hooks run by Cursor); no Node or Python runtime; event-driven file and focus watching; no low-level keyboard hook by default (antivirus flags it, and it fails silently past 1 s); code-sign the binaries. [R8, R6]
13. **Install touches user-level config only**, merged idempotently, removable by `pairtime uninstall`: never commit hooks, AGENTS.md or CLAUDE.md into users' repos. Handle Codex's one-time hook trust and Cline's "Enable Hooks" checkbox with clear prompts. Merge next to other tools' entries rather than replacing them: git-ai, for one, rewrites its own agent hooks daily in the same files. [R4, R6, R7]
14. **Use lookback to show value on day one.** Backfill from Claude Code (30 days), Gemini CLI (30 days), Codex and Copilot (no documented expiry) on install, but mark backfilled hours as lower-trust for submission because they lack OS-side evidence. [R3, R4, R9]
15. **Attach hours to commits.** Per-commit stats and agent trailers or git-ai notes let reviewers ask "which hours produced this diff", which is what YSWS review needs. [R5, R10]
16. **Upload only aggregates.** Per-minute buckets per project are a few KB per user per day, which keeps hosting far below R11's $500/month even at 15,488 MAU. [R11, R5]
17. **Lead with self-improvement views:** human vs agent time, time spent waiting on the agent, prompts per hour, repeated-prompt streaks, review time, interruptions. Claude Code's `active_time` user/cli split is a ready-made example of what to show. [R3]


## 18. Open questions

For Karim or Hack Club staff:

1. Does reading the agent's output count as productive struggle, and how much? The vision post counts "actively solving problems"; is watching a long streamed answer with no input credited, capped, or zero?
2. Which OS-level signals are acceptable under R6: idle time and foreground app only, or also keystroke counts and window titles? Would Hack Club's privacy reviewers accept an opt-in keystroke-cadence signal for higher-trust submissions?
3. Will YSWS reviewers and Fraud Squad accept a hash-anchored evidence pack without screen recording, or do high-value submissions still need a Lapse-style timelapse?
4. Can Hackatime share aggregate counts of AI user-agent tokens (wakatime-cli tags heartbeats per harness) so pairtime's harness priority matches what teens actually use?
5. Is it acceptable for the installer to edit global agent configs (`~/.claude/settings.json`, `~/.cursor/hooks.json`, `~/.codex/hooks.json`, ...) with one consent screen?
6. Does a one-time "trust this hook" step in Codex, or ticking "Enable Hooks" in Cline, still count as passive under R4?
7. Should submitted data show agent hours alongside human hours, so program authors can see how a project was built, or human hours only?
8. Are web-only builders (Lovable, Bolt, v0, Replit) and web chat in scope for v1, given they need a browser extension? Do programs accept hours from those tools at all?
9. Are school-managed devices (Chromebooks, locked-down Windows) in scope? They usually block extensions and global installs.
10. Should pairtime also emit WakaTime-compatible heartbeats into Hackatime so existing program pipelines keep working, and how should AI-assisted human time be categorized there?
11. What labeled ground truth exists, such as Stardance's hand-reviewed hours, that pairtime could calibrate against for R2?
12. (Added in fact-check.) May pairtime switch on vendor settings that are off by default, such as Claude Code's `OTEL_METRICS_INCLUDE_REPOSITORY` and `OTEL_METRICS_INCLUDE_ENTRYPOINT`, Gemini CLI telemetry, or Codex's history persistence if a user disabled it, or should it only read what the user already has on? Each one widens what the tool collects, which is an R6 call as much as an engineering one.


## 19. Sources

All read 2026-11-19 unless noted. Docs fetched as raw Markdown where the site offers it; GitHub files pinned to the commit shown.

Claude Code (Anthropic), code.claude.com/docs/en/*.md, changelog latest v2.1.328:
1. `hooks` — events, common input fields, UserPromptSubmit sources, Stop `session_crons`, async and HTTP hooks, workspace trust, Windows exec form.
2. `monitoring-usage` — OTel metrics and events, `active_time.total` `type` user/cli, `app.entrypoint`, repo-settings restrictions.
3. `sessions` and `claude-directory` — transcript paths, `cleanupPeriodDays` (30), `history.jsonl` retention, scratchpad layout.
4. `headless` — `-p`, `--bare`, structured output.
5. `memory` — AGENTS.md rules (v2.1.277+, only without CLAUDE.md).
6. `goal`, `scheduled-tasks` — `/goal` as a prompt-based Stop hook, `/loop`, 7-day expiry.
7. `env-vars` — `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_SKIP_PROMPT_HISTORY`.
8. `settings-reference` — `attribution` commit/PR trailers.
9. `skills`, `mcp`, `statusline`, `changelog`.

Cursor, cursor.com/docs/*.md:
10. `hooks.md` — events, common fields, Tab vs Agent edit hooks, cloud-agent coverage, Windows via PowerShell.
11. `reference/third-party-hooks.md` — Claude Code hook loading and mapping.
12. `rules.md`, `skills.md`, `mcp.md`.
13. `cli/headless.md`, `cli/reference/output-format.md`, `cli/changelog.md` (v2026.11.17).
14. `enterprise/opentelemetry-export.md`, `enterprise/privacy-and-data-governance.md`.

GitHub Copilot and VS Code:
15. docs.github.com (via `/api/article/body`): `copilot/reference/hooks-reference`, `copilot-cli-reference/cli-command-reference`, `cli-config-dir-reference`, `enterprise-administrators/opentelemetry-reference`.
16. microsoft/vscode-docs @ `2d06388` (2026-11-18): `docs/agent-customization/hooks.md`, `docs/agents/guides/monitoring-agents.md`, `docs/agent-customization/custom-instructions.md`, `release-notes/v1_103.md` (AI statistics).
17. microsoft/vscode main: `src/vscode-dts/vscode.d.ts`, `vscode.proposed.textDocumentChangeReason.d.ts`; issue #347841 (AI stats removal, closed 2026-11-12).

OpenAI Codex:
18. learn.chatgpt.com/docs: `hooks.md`, `config-file/config-advanced.md`, `config-file/config-reference.md`, `agent-configuration/agents-md.md`, `non-interactive-mode.md`.
19. openai/codex (Apache-2.0) @ `d64451b`, release rust-v0.170.0: `codex-rs/protocol/src/protocol.rs` (SessionSource, SessionMeta), `codex-rs/rollout/src/{lib.rs,list.rs,rollout_file_name.rs,compression.rs}`, `codex-rs/history/src/rollout_payload.rs`, `codex-rs/message-history/src/lib.rs`, `codex-rs/otel/src/metrics/tags.rs`.

Gemini CLI:
20. google-gemini/gemini-cli (Apache-2.0) @ `615d6bc`, v0.69.0: `docs/hooks/{index,reference}.md`, `docs/cli/{telemetry,session-management,headless,gemini-md,checkpointing}.md`, `docs/reference/configuration.md`.

Windsurf / Devin Desktop:
21. docs.devin.ai: `desktop/cascade/hooks.md` (now "Devin Local Customizations"), `desktop/cascade.md` (legacy Cascade), `desktop/changelog.md` (v3.0.12, 2026-06-02, rename), `cli/extensibility/hooks/overview.md`, `cli/extensibility/hooks/lifecycle-hooks.md`.

Other harnesses:
22. cline/cline main: `.clinerules/hooks/README.md`, `docs/sdk/plugins.mdx`, `docs/customization/cline-rules.mdx`; release v4.1.26.
23. RooCodeInc/Roo-Code — repository archived (GitHub API).
24. Kilo-Org/kilocode main: `packages/kilo-docs/pages/{automate/extending/plugins,code-with-ai/agents/session-history,customize/agents-md}.md`.
25. anomalyco/opencode dev: `services/site/src/docs/content/build/plugins/{index,server,migrate-v1}.mdx`.
26. Aider-AI/aider main: `aider/website/docs/config/options.md`, `docs/more/analytics.md`.
27. zed-industries/zed main: `docs/src/ai/{rules,instructions,external-agents,agent-panel}.md`.
28. antigravity.google/docs/hooks (WebFetch summary) and `llms.txt`.
29. kiro.dev/docs: `hooks.md`, `hooks/types.md`, `ide/whats-new-v1/hooks.md`, `steering.md`.
30. ampcode.com: `llms.txt`, `docs/markdown/customize/plugins`.
31. docs.warp.dev `_llms-txt/agents.txt`.
32. junie.jetbrains.com/docs/junie-cli-hooks.html and `guidelines-and-memory.html` (both dated 2026-11-18); jetbrains.com/help/ai-assistant/agents.html (2026.3, dated 2026-10-07).
33. docs.trae.ai/ide/hook-configuration-reference (raw page data; client-rendered).
34. continuedev/continue main: `docs/reference.mdx` (`data` destinations), `docs/customize/deep-dives/development-data.mdx`.
35. aaif-goose/goose main: `documentation/docs/guides/context-engineering/hooks.md`.

Prior art and parsers:
36. wakatime/wakatime-cli (BSD-3-Clause) @ `7ab26b5`, v2.28.4: `pkg/ai/*.go` (per-harness log paths and fields; "ai coding" relabeling in `ai.go`; `transcript_clock.go`).
37. daaain/claude-code-log @ `763365c`: `claude_code_log/models.py` (transcript entry fields, queue operations).
38. FailproofAI/failproofai: `lib/log-entries.ts` (entry types).
39. git-ai-project/git-ai (Apache-2.0): `README.md`, `specs/git_ai_standard_v3.0.0.md` (`refs/notes/ai`, human attestations).
40. ActivityWatch/aw-watcher-afk (MPL-2.0): `aw_watcher_afk/{windows,macos,unix}.py`; licenses of activitywatch, aw-watcher-window, browser-wakatime via GitHub API.
41. GitHub commit search (2026-11-19): `Replit-Commit-Author` trailers; `gpt-engineer-app[bot]` commits.

Platform APIs:
42. learn.microsoft.com, `LowLevelKeyboardProc` (ms.date 2025-07-14): timeout and silent-removal rules, Raw Input recommendation.
43. wayland.app, `ext-idle-notify-v1`: interfaces, v2 input-idle notification, compositor support table.
44. developer.chrome.com, `chrome.idle` API reference.
45. Not re-read today, cited from prior knowledge: VS Code "Using Proposed API" page (proposed APIs barred from Marketplace extensions); WakaTime plugin-creation guide (heartbeat rules).

Internal notes:
46. `docs/research/notes/signals.md`, `notes/hackatime.md`, `notes/priorart.md` (unverified leads; used only where re-verified above, or marked).

Opened during the fact-check (2026-11-19), in addition to re-reading items 1-5, 6, 7, 10, 11, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 26, 36, 39, 40, 42, 43 above:
47. code.claude.com/docs/en `changelog.md` (v2.1.328 Code Mode change; v2.1.152 entrypoint; v2.1.277 and v2.1.281 AGENTS.md), `settings-reference.md` (`attribution.commit` default trailer), `permissions.md` (Bash rule wildcards), `sessions.md`, `claude-directory.md`.
48. openai/codex @ `d64451b`: `codex-rs/features/src/lib.rs` (`local_thread_store_compression` off, `hooks` and `goals` on), `codex-rs/core/src/thread_store_builder.rs`, `codex-rs/rollout/src/{compression.rs,rollout_file_name.rs,lib.rs}`, `codex-rs/login/src/auth/default_client.rs` (`CODEX_INTERNAL_ORIGINATOR_OVERRIDE`), `sdk/typescript/src/exec.ts`, `codex-rs/config/src/types.rs` (`HistoryPersistence`), `codex-rs/otel/src/metrics/{names,tags}.rs`, `codex-rs/ext/goal/src/{lib,steering,events}.rs`; learn.chatgpt.com/docs `agent-approvals-security.md`, `config-file/config-reference.md`, `agent-configuration/agents-md.md`.
49. docs.github.com `copilot/reference/enterprise-administrators/opentelemetry-reference`; GitHub API for microsoft/vscode issue #347841 and `vscode.d.ts` history (`9e9e76c`).
50. modelcontextprotocol/modelcontextprotocol main: `docs/specification/2025-11-25/basic/lifecycle.mdx`, `docs/specification/2026-07-28/{changelog.mdx,client/roots.mdx}`.
51. GNOME/mutter main `data/dbus-interfaces/org.gnome.Mutter.IdleMonitor.xml`; wayland-protocols main `staging/ext-idle-notify/ext-idle-notify-v1.xml`; MicrosoftDocs/win32 `desktop-src/winmsg/lowlevelkeyboardproc.md`.
52. Aider-AI/aider main `aider/repo.py` (attribution docstring, last change `f730853`) and `aider/website/docs/git.md`; cline/cline main `.clinerules/hooks/README.md` (last change `40d93ba`, 2026-09-21); git-ai-project/git-ai main `README.md`; GitHub API repo metadata for every license and archive claim in section 9.


## Fact-check log

Adversarial fact-check, 2026-11-19. Each row is a claim a designer would lean on, checked against a primary source opened during the fact-check (not the doc's own citation). Verdicts: **confirmed**, **corrected** (fixed in place), **partly** (fixed in place), **unverified** (marked in place).

Summary: 34 claims checked; 26 confirmed (many with additions written in place), 4 partly right and fixed in place, 4 corrected outright. The corrections that change the design: Codex rollouts are not zstd-compressed by default (#15); MCP `initialize` never carried roots and the current spec drops `initialize` and deprecates Roots (#25); `CLAUDECODE=1` doesn't prove a Claude-spawned process (#6); wakatime-cli parses 46 agents, not 60 (#13); Codex metric names carry a `codex.` prefix on the wire (#17). The additions that change the design: Claude Code `SessionStart` takes no HTTP hooks and Cursor runs only `command`/`prompt` Claude-format hooks, so pairtime's shared hooks must be exec-form commands (#1, #9); even user-level Claude Code hooks wait for the folder trust dialog (#2); Codex `originator` is spoofable by env var (#16); Codex has its own `/goal` (#17); Claude Code OTel carries the user's email by default (#3).

Still unverified after the fact-check (marked in place): field spelling Cursor sends to Claude-format hooks; whether `/goal`-style continuations (Claude Code or Codex) fire prompt-submit hooks; whether `/loop` or cross-session prompts reach Claude Code's `history.jsonl`; Cursor `state.vscdb` and `ai-code-tracking.db` schemas, Devin transcript paths on Windows, and Copilot `events.jsonl` type names beyond `user.message` (known only from wakatime-cli's parsers, not vendor docs); the Marketplace ban on proposed VS Code APIs; Antigravity, Kiro, Amp, Junie, Trae and Warp details in section 9 (not re-checked: lower priority than the top six); every CPU/RAM estimate in section 14.


| # | Claim (section) | Source opened | Verdict and change |
|---|---|---|---|
| 1 | Claude Code hook common input fields; no timestamp; 33 events; handler types (3.1) | code.claude.com/docs/en/hooks.md, raw Markdown, 2026-11-19 | **partly**. Fields and "no timestamp" confirmed; 33 event headings counted. Added: `permission_mode` and `effort` are not on every event; `SessionStart` accepts only `command`/`mcp_tool` hooks and `Setup` only `command` (so HTTP-only won't cover session start); SessionEnd budget 1.5 s, raisable to 60 s |
| 2 | `UserPromptSubmit` also fires for `/loop`, background subagents, cross-session messages, Code Mode, with no origin field; `Stop.session_crons` (3.1) | same page, "UserPromptSubmit" and "Stop input" | **confirmed**, with additions: `session_crons[]` entries also carry `id`, and `prompt` is truncated at 1000 chars (match on prefix); Stop-hook continuations capped at 8 in a row; workspace trust holds back even `~/.claude/settings.json` hooks in interactive sessions until the folder is trusted |
| 3 | Claude Code OTel: `active_time.total` `type` user/cli; protocol default `http/protobuf` from v2.1.323; 60 s / 5 s intervals; `app.entrypoint` opt-in (v2.1.152); repo settings can't redirect telemetry (3.3) | code.claude.com/docs/en/monitoring-usage.md and changelog.md, 2026-11-19 | **confirmed** (definition quoted verbatim; versions found in changelog). Added: standard attributes carry `user.email`, account UUIDs and `organization.id` by default (receiver must strip them); `OTEL_METRICS_INCLUDE_REPOSITORY` (v2.1.269+) gives a repo key; fuller `app.entrypoint` value list. Also fixed 3.1: v2.1.328 stopped running `UserPromptSubmit` on Code Mode output, which the hooks page hasn't caught up with |
| 4 | Claude Code reads AGENTS.md only without a CLAUDE.md (v2.1.277+); `~/.claude/CLAUDE.md` always loads; `AGENTS.local.md`/`.override`/`.agents/` not read; "context, not enforced configuration" (3.4, 11) | code.claude.com/docs/en/memory.md; changelog v2.1.277 | **confirmed**. Note: AGENTS.md is read by a built-in plugin (`cc-plugin-agents-md@builtin`) that a user can disable, and before v2.1.281 it was skipped on Bedrock or with telemetry disabled |
| 5 | Transcript path and `<project>` encoding, 30-day `cleanupPeriodDays` (min 1), `history.jsonl` not swept except HIPAA, format "internal ... changes between versions", not encrypted (3.2) | code.claude.com/docs/en/sessions.md, claude-directory.md | **confirmed**. Added `CLAUDE_CODE_PROJECT_DIR_NAME` (with `CLAUDE_CONFIG_DIR`) as a reason not to decode directory names |
| 6 | `CLAUDECODE=1` and `CLAUDE_CODE_CHILD_SESSION=1` mark any process Claude Code spawns; `CLAUDE_CODE_SESSION_ID` reaches stdio MCP servers (12.1, 11.3, 11.4) | code.claude.com/docs/en/env-vars.md | **corrected**. `CLAUDECODE` is also set by IDE extensions in integrated terminals, so it can't separate a human's terminal from an agent's subprocess; `CLAUDE_CODE_CHILD_SESSION` is the reliable marker but is not set for stdio MCP servers. `CLAUDE_CODE_SESSION_ID` to MCP servers confirmed. Added: nested interactive sessions are excluded from up-arrow history (v2.1.172+) |
| 7 | `/goal` is a session-scoped prompt-based Stop hook; `/loop`/cron prompts fire only while idle; recurring tasks expire after 7 days (3.5) | code.claude.com/docs/en/goal.md, scheduled-tasks.md | **confirmed** ("`/goal` is a wrapper around a session-scoped prompt-based Stop hook"; "Tasks only fire while Claude Code is running and idle"; "Recurring tasks automatically expire 7 days after creation"). "No `UserPromptSubmit` for continuation turns" stays an inference to test |
| 8 | Cursor hook config, locations, common fields (no timestamp), `beforeSubmitPrompt`, `afterFileEdit` vs `afterTabFileEdit`, `stop.loop_count`/`followup_message`, `loop_limit` 5 for Cursor hooks and unlimited for Claude-format hooks, PowerShell on Windows, cloud agents skip user hooks and `sessionStart`/`sessionEnd` (4.1) | cursor.com/docs/hooks.md, 2026-11-19 | **confirmed** field by field |
| 9 | Cursor runs Claude Code hooks from `.claude/settings*.json` and `~/.claude/settings.json` with the listed mapping (4.1, 2, 8) | cursor.com/docs/reference/third-party-hooks.md | **partly**. Mapping confirmed. Added: gated by a user setting (on by default); only `command`/`prompt` types listed as supported, so Claude-format `http` hooks likely don't fire in Cursor; payload field spelling for Claude-format hooks undocumented (marked unverified) |
| 10 | Cursor OTel export is Enterprise-only and pushed from Cursor's servers; CLI `surface` on self-hosted workers; headless transcripts are Claude Code-compatible JSONL (4.3, 4.1, 4.2) | cursor.com/docs/enterprise/opentelemetry-export.md, cli/changelog.md (latest v2026.11.17) | **confirmed** (OTLP/HTTP protobuf to a public HTTPS endpoint from fixed source IPs) |
| 11 | Copilot hook locations (policy, `.github/hooks/*.json`, `~/.copilot/hooks/`, repo `.claude/settings*.json`, plugins), 14 events, camelCase vs PascalCase payloads with ms vs ISO timestamps, `userPromptSubmitted` output dropped, `COPILOT_HOOK_ALLOW_LOCALHOST=1`, cloud-agent behavior (5.1) | docs.github.com/api/article/body?pathname=/en/copilot/reference/hooks-reference, 2026-11-19 | **confirmed**. Added the 8-continuation runaway guard, autopilot denying `"ask"`, and `COPILOT_AGENT_PROMPT` in cloud jobs |
| 12 | Copilot CLI stores `session-state/<id>/events.jsonl`, `command-history-state/` (50 default, max 1000), `session-store.db` rebuilt and synced by `/chronicle reindex`, `logs/` (5.2) | GitHub `cli-config-dir-reference`; wakatime-cli `pkg/ai/copilot_cli.go` @ `7ab26b5` | **confirmed**. Event type names other than `user.message` exist only in wakatime's parser, now said in place |
| 13 | wakatime-cli v2.28.4 "about 60"/"60+" agent logs; "ai coding" relabel within 2 minutes when no human line changes (2, 13.1) | GitHub API releases (v2.28.4, 2026-11-05); `pkg/ai/ai.go` @ `7ab26b5` | **corrected**: 46 parser IDs, not 60; relabel rule has a second clause (within 30 minutes of AI activity and before the first human edit) |
| 14 | Codex hooks: locations, trust pinned to the hook's hash and reviewed in `/hooks`, `--dangerously-bypass-hook-trust`, `[features] hooks = false` (alias `codex_hooks`), common fields, `UserPromptSubmit`, `Stop` continuation "acts as a new user prompt", `Interrupt`, `SessionEnd` after 30 min idle with `reason` always `other` (6.1) | learn.chatgpt.com/docs/hooks.md (developers.openai.com/codex/hooks returns 308 there), 2026-11-19; `codex-rs/features/src/lib.rs` @ `d64451b` (`hooks` Stable, default on) | **confirmed**. Useful extra: user and system hooks load even in untrusted projects; the same command in two files can run twice |
| 15 | Codex rollouts "compressed to `.jsonl.zst` after 7 days" (2, 6.2, 14) | `codex-rs/rollout/src/compression.rs`, `core/src/thread_store_builder.rs`, `features/src/lib.rs` @ `d64451b` | **corrected**. The 7-day zstd worker exists (`MIN_ROLLOUT_AGE` 7 days, level 3) but only runs when `local_thread_store_compression` is on, which is `UnderDevelopment` and off by default. Also added the `rollout-<ts>-<thread>_<rollout>.jsonl` fork name |
| 16 | Codex `SessionSource`/`ThreadSource`/`SessionMeta` values; `originator` list; `history.jsonl` shape and `save-all` default (6.2, 6.5, 12.1) | `codex-rs/protocol/src/protocol.rs`, `login/src/auth/default_client.rs`, `sdk/typescript/src/exec.ts`, `otel/src/metrics/tags.rs`, `message-history/src/lib.rs`, `config/src/types.rs` @ `d64451b` | **confirmed**, `save-all` default now sourced (was "unverified"). Added: `unknown` variant and missing `source` defaulting to `vscode`; `originator` can be set by any process via `CODEX_INTERNAL_ORIGINATOR_OVERRIDE` |
| 17 | Codex OTel metric names `turn.e2e_duration_ms`, `approval.requested`, `task.user_shell`, ...; "every metric carries `originator`, `session_source`, ..." (6.3) | learn.chatgpt.com/docs/config-file/config-advanced.md; `otel/src/metrics/names.rs`, `tags.rs` | **partly**. Names exist in the docs' catalog, but on the wire they carry a `codex.` prefix. Docs list only `auth_mode`, `model`, `app.version` as default context; `originator`/`session_source` are source-defined tags. Added Codex `/goal` (feature `goals`, on by default) to 6.5 with its `codex.goal.*` metrics |
| 18 | Gemini CLI hooks: `settings.json` `hooks`, `command` type only, 60000 ms default timeout, base input with ISO 8601 `timestamp`, `BeforeAgent`/`AfterAgent` (`stop_hook_active`, deny = retry with `reason` as new prompt), `SessionEnd` not awaited, project hooks fingerprinted, `hooksConfig.enabled` default true (7.1) | google-gemini/gemini-cli @ `615d6bc` (2026-11-18; v0.69.0 released 2026-11-19): `docs/hooks/reference.md`, `docs/hooks/index.md`, `docs/reference/configuration.md` | **confirmed** |
| 19 | Gemini sessions under `~/.gemini/tmp/<project_hash>/chats/`; `sessionRetention` enabled, `maxAge` 30d, `minRetention` 1d (7.2) | `docs/cli/session-management.md`, `docs/reference/configuration.md` @ `615d6bc` | **confirmed** (`maxCount` default is undefined) |
| 20 | Gemini reads `GEMINI.md` by default and AGENTS.md only via `context.fileName`; telemetry off by default, `logPrompts` default true (7.3, 7.4, 11.1) | `docs/cli/gemini-md.md`, `docs/cli/telemetry.md` @ `615d6bc` | **confirmed**. Added: `otlpProtocol` defaults to `grpc` (port 4317), which matters for an HTTP-only receiver |
| 21 | "Windsurf is now Devin Desktop" (v3.0.12, 2026-06-02); Cascade legacy; Devin Local shares Devin CLI's harness; hooks from `.devin/hooks.v1.json`, `~/.config/devin/config.json` (`%APPDATA%\devin\config.json`), and `.claude/` plus `~/.claude/settings.json` by default (`read_config_from.claude`); `session_id`/`prompt_id`; `SessionEnd` not fired in cloud; no hooks in Restricted Mode (2, 8) | docs.devin.ai `desktop/changelog.md` (latest v3.14.30825), `desktop/cascade.md`, `desktop/cascade/hooks.md` (now "Devin Local Customizations"), `cli/extensibility/hooks/{overview,lifecycle-hooks}.md`, 2026-11-19; docs.windsurf.com returns 308 to docs.devin.ai | **confirmed** verbatim |
| 22 | VS Code: `TextDocumentChangeReason` only `Undo = 1`/`Redo = 2`; `TextEditorSelectionChangeKind` `Keyboard = 1`, `Mouse = 2`, `Command = 3`; `WindowState.active`; proposed `detailedReason.source` (`inline-completion`, `chat-edit`, `extension`); issue #347841 removed the AI-rate gauge on 2026-11-12 (8, 13.1) | microsoft/vscode main `src/vscode-dts/vscode.d.ts` (last change `9e9e76c`, 2026-10-30) and `vscode.proposed.textDocumentChangeReason.d.ts`; GitHub API for issue #347841 | **confirmed**, quote matches the issue body. Added that edit-source tracking stays in core. The Marketplace ban on proposed APIs is still from the doc's prior knowledge (not re-checked) |
| 23 | `LowLevelKeyboardProc`: must answer within `LowLevelHooksTimeout`, max 1000 ms from Windows 10 1709, "silently removed" on Windows 7+, Raw Input recommended (13.4) | MicrosoftDocs/win32 `desktop-src/winmsg/lowlevelkeyboardproc.md` (ms.date 07/14/2025) | **confirmed** verbatim |
| 24 | Wayland `ext-idle-notify-v1` v2 adds `get_input_idle_notification`; supported by KWin 6.7, Sway 1.11, Hyprland 0.52.1, COSMIC, niri, not Mutter or Weston; GNOME fallback via Mutter D-Bus (13.4) | wayland-protocols `staging/ext-idle-notify/ext-idle-notify-v1.xml` (main); wayland.app support table; GNOME/mutter `org.gnome.Mutter.IdleMonitor.xml` | **confirmed** (16 supporting compositors; Mutter 51 and Weston 15 marked unsupported). Replaced "general knowledge" for the GNOME D-Bus API with the interface file, adding its event-driven watches |
| 25 | "the `initialize` request carries `clientInfo.name`/`version` (which harness) and `roots` (which project)"; MCP server lifetime gives session boundaries (11.4, 14) | modelcontextprotocol/modelcontextprotocol main: `docs/specification/2025-11-25/basic/lifecycle.mdx`, `client/roots.mdx`, `2026-07-28/changelog.mdx`; code.claude.com `env-vars.md` | **corrected**. `initialize` has `clientInfo` and only a `roots` capability flag; roots need a `roots/list` request. Spec 2026-07-28 drops `initialize` (clientInfo moves to per-request `_meta`, SHOULD) and deprecates Roots. Claude Code's MCP servers keep their spawn-time session ID and outlive sessions, so they don't mark session boundaries |
| 26 | Aider: `--attribute-co-authored-by` default True; `--attribute-author`/`--attribute-committer` append "(aider)"; Apache-2.0, last push 2026-05-22. Claude Code adds a `Co-Authored-By` trailer by default (9, 13.3, 2) | Aider-AI/aider main `aider/website/docs/config/options.md`, `docs/git.md`, `aider/repo.py` (last change `f730853`); GitHub API; code.claude.com `settings-reference.md` | **partly**. With the trailer on (the default), Aider leaves author/committer names alone unless those flags are set explicitly. Added the exact trailer strings for both tools (`<aider@aider.chat>`, `<noreply@anthropic.com>`) so the git reader can match on addresses |
| 27 | git-ai: Apache-2.0, ~3k stars, pushed 2026-11-17; standard v3.0.0 "MUST be stored under the `refs/notes/ai` namespace"; `s_` AI and `h_` "known human" keys "as observed by an IDE extension"; prompts "scanned and redacted, and saved outside of Git"; no git hooks or wrapper (13.3) | GitHub API for git-ai-project/git-ai; `specs/git_ai_standard_v3.0.0.md` and `README.md` on main | **confirmed** verbatim. Added: git-ai manages agent hooks itself (daily check), so installers must coexist |
| 28 | Long-tail repos and licenses: OpenCode MIT at `anomalyco/opencode`, Goose Apache-2.0 at `aaif-goose/goose`, Roo Code archived (last push 2026-05-15), Cline v4.1.26 with file hooks unsupported on Windows, Kilo, Continue, ActivityWatch MPL-2.0, wakatime-cli BSD-3-Clause (9, 13.4, 19) | GitHub API repo metadata (`sst/opencode` and `block/goose` resolve to the renamed repos); cline/cline `.clinerules/hooks/README.md` | **confirmed**. Kilo is MIT, Continue Apache-2.0, Zed has no single SPDX license (NOASSERTION). Added Cline's full hook event list |
| 29 | Installer must allow `Bash(pairtime track:*)` in Claude Code (11.2) | code.claude.com/docs/en/permissions.md | **confirmed with update**: `:*` still works, but the documented form is `Bash(pairtime track *)` |
| 30 | Codex sandbox blocks network and writes outside the workspace by default (11.2) | learn.chatgpt.com/docs/agent-approvals-security.md, config-reference.md | **confirmed** |
| 31 | Copilot OTel: VS Code settings `github.copilot.chat.otel.{enabled,exporterType,otlpEndpoint,outfile,captureContent,dbSpanExporter.enabled}`, defaults `otlp-http` and `http://localhost:4318`; `copilot_chat.*` metrics and events; CLI `COPILOT_OTEL_ENABLED`, `COPILOT_OTEL_FILE_EXPORTER_PATH`, `github.copilot.code.lines_added`, `github.copilot.session.abort`, `github.copilot.initiator` "Request initiator" with no listed values (5.3) | microsoft/vscode-docs @ `2d06388` `docs/agents/guides/monitoring-agents.md`; docs.github.com `copilot/reference/enterprise-administrators/opentelemetry-reference` | **confirmed**. Extra: the Copilot CLI runtime only speaks OTLP/HTTP even when gRPC is configured |
| 32 | Copilot CLI merges `CLAUDE.md`, `GEMINI.md`, `AGENTS.md` (git root and cwd), `.github/copilot-instructions.md`, `~/.copilot/copilot-instructions.md`, `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`; `--no-custom-instructions` disables them (5.4, 11.1) | docs.github.com `copilot-cli-reference/cli-command-reference` | **confirmed** |
| 33 | Codex AGENTS.md chain: global `AGENTS.override.md` or `AGENTS.md`, root-to-cwd walk, `project_doc_max_bytes` 32 KiB, discovery once per run (6.4, 11.1) | learn.chatgpt.com/docs/agent-configuration/agents-md.md | **confirmed**. Added to 11.1: a global `AGENTS.override.md` makes Codex skip `~/.codex/AGENTS.md` |
| 34 | ActivityWatch idle calls: `GetLastInputInfo` (`windows.py`), `CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState, kCGAnyInputEventType)` (`macos.py`) (13.4) | ActivityWatch/aw-watcher-afk master `aw_watcher_afk/{windows,macos}.py` | **confirmed** |
