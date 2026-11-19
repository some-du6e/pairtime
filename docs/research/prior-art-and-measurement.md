# Prior art, measurement science, motivation, and server cost

Status: draft in progress

Track: priorart. Started 2026-11-19. Covers what already exists for tracking time and effort in AI-assisted coding, what research says separates engaged work from passive work, what makes tracking motivating for teens, and whether the server fits R11's $500/month budget.

Requirement IDs (R1-R11, A) refer to [docs/BOUNTY.md](../BOUNTY.md).

## Contents

1. Prior art: tools that track or analyze AI-assisted coding
2. Measurement science: productive struggle and how people program with AI
3. Signals that separate engaged from passive work
4. Motivation for teens
5. Server cost model for R11
6. Design implications
7. Open questions
8. Sources

## 1. Prior art

Everything below was checked against source code, official docs or the project's own site on 2026-11-19 unless marked otherwise. Repo metadata (license, last push) comes from the GitHub API on that date.

The short version: every existing tool measures one of three things. It measures **editor activity** (WakaTime, Hackatime, Wakapi, ActivityWatch), **AI output** (tokens, lines, commits, acceptance; ccusage, Copilot metrics, Cursor, Claude Code analytics, git-ai, Agent Trace), or **screen evidence** (Lapse, Lookout). None of them measures the human's contribution to an AI-assisted session. The tools that put AI work on a clock (WakaTime, Hackatime, vibe-log) all start the clock from agent transcript timestamps, which is the "counts how active your AI agent is" failure Zach describes in BOUNTY.md.

### 1.1 WakaTime: "ai coding" heartbeats from agent transcripts

- **Repo**: [wakatime/wakatime-cli](https://github.com/wakatime/wakatime-cli), BSD-3-Clause, commit `7ab26b5` (2026-11-05). Go.
- **What it measures**: time per project, file, language, branch, plus a category per heartbeat. Categories in `pkg/heartbeat/category.go`: `coding`, `ai coding`, `building`, `code reviewing`, `debugging`, `manual testing`, `running tests`, `writing tests`, `writing docs`, `researching`, `learning`, `planning`, `designing`, `browsing`, `communicating`, `meeting`, `notes`, `indexing`, `translating`, `advising`, `supporting`.
- **AI fields on the heartbeat** (`pkg/heartbeat/heartbeat.go`): `ai_line_changes`, `human_line_changes`, `ai_prompt_length`, `ai_input_tokens`, `ai_output_tokens`, `ai_cached_input_tokens`, `ai_session`, `ai_subscription_plan`. The summaries API adds `ai_additions`, `human_additions`, `ai_deletions`, `human_deletions`, per-model line and cost breakdowns, and `ai_prompt_length_avg` (from the sibling notes on [wakatime.com/developers](https://wakatime.com/developers); not re-fetched).
- **How**: `pkg/ai/` has a parser for each of about 45 harnesses (files such as `claude.go`, `codex.go`, `cursor.go`, `copilot.go`, `copilot_cli.go`, `gemini.go`, `opencode.go`, `windsurf.go`, `cline.go`, `roo.go`, `goose.go`, `amp.go`, `pi.go`, `kiro.go`, `zed.go`, `warp.go`, plus `generic_json.go` and `generic_sqlite.go`). Each parser reads the harness's local transcript or SQLite store after a checkpoint and emits heartbeats:
  - `claude.go` `claudeAppHeartbeat`: one `app`-type heartbeat per user prompt, with `ai_prompt_length` set, stamped with the transcript line's `timestamp`.
  - `claude.go` `claudeFileHeartbeat`: one `file`-type heartbeat per tool result that touched a file, with `ai_line_changes` and `is_write` (`create`/`update`/`delete`), stamped with the transcript time.
  - `transcript_clock.go`: when a transcript line has no timestamp, it reuses the previous line's time or the file mtime, and stores fallback stamps under `<wakatime resources>/ai-timestamps/<sha256(path)>.json` so re-syncs don't move old records.
  - `ai.go`: human editor heartbeats within 2 minutes of AI heartbeats (with no human line changes), or within 30 minutes and before the first human edit, are relabeled `ai coding`. Heartbeats on the same entity and session less than 60 s apart are merged and their counts summed.
  - The server then joins heartbeats into durations with the keystroke timeout (default 15 minutes; [FAQ](https://wakatime.com/faq)).
- **Borrow**: the parser catalogue (BSD-3 is compatible with AGPL-3.0, so code can be ported with attribution). The checkpoint-per-parser design (`checkpoints.go`) for incremental, idempotent transcript reading. The `human_line_changes` vs `ai_line_changes` split. The idea of relabeling editor time near agent activity instead of double-counting it.
- **Falls short**: a heartbeat fires for every agent file edit, so a 3-hour unattended agent run that edits a file every few minutes yields 3 hours of `ai coding` once the 15-minute timeout joins them. There is no notion of the human reading, reviewing, testing or thinking. Prompt *length* is recorded but not prompt intent or repetition. The BOUNTY.md thread notes WakaTime's AI tracking "is widely seen as poor."

### 1.2 Hackatime: WakaTime-compatible server with an AI discount

- **Repo**: [hackclub/hackatime](https://github.com/hackclub/hackatime), MIT, commit `43bde10` (2026-11-19). Rails + Postgres. The sibling doc `hackatime-and-hack-club.md` covers it in depth; this entry is only what matters for prior art.
- **What it measures**: WakaTime heartbeats (it accepts the WakaTime API), with a much shorter join: `app/models/concerns/heartbeatable.rb` sums `LEAST(time - LAG(time), heartbeat_timeout_duration)` per user, where `heartbeat_timeout_duration` defaults to **2 minutes**, not WakaTime's 15. Only categories `coding` and `ai coding` count toward totals (`where(category: [ "coding", "ai coding" ])`).
- **AI handling**: `app/javascript/pages/Home/signedIn/AiCodingMeter.svelte` shows an "AI vs Human Coding" split from `stats["ai coding"]` vs `stats.coding`. `app/models/leaderboard_entry.rb` discounts AI time on the leaderboard: "AI coding earns 90% for the first three hours of a day. After that the rate decays exponentially, halving roughly every 1.4 hours, so a day of AI coding can never be worth more than 90% * (3h + 2h) = 4.5 hours." Constants: `AI_CREDIT_RATE = 0.9`, `AI_FULL_RATE_SECONDS = 3h`, `AI_DECAY_SECONDS = 2h`.
- **Borrow**: the 2-minute gap cap (it keeps idle tails short and is what Hack Club reviewers already calibrate against). The daily diminishing-returns curve as a fallback cap for any signal pairtime can't verify.
- **Falls short**: the discount is a flat haircut on agent-driven time. It can't tell a student reviewing every diff from one who walked away, so it under-credits the first and over-credits the second. Stardance's 123,400 submitted vs 27,600 approved hours (BOUNTY.md R9) is the result.

### 1.3 git-ai: line attribution in git notes

- **Repo**: [git-ai-project/git-ai](https://github.com/git-ai-project/git-ai), Apache-2.0, v1.7.6 (2026-11-05), about 3,000 stars. Rust.
- **What it measures**: which lines of each commit were written by which agent session, model and prompt, and which by a known human. Not time.
- **How**: agents call `git-ai checkpoint` "whenever they write code or modify files with bash scripts" (README). git-ai installs and maintains the agent hooks itself (`git ai install-hooks`, re-checked daily) and does not use git hooks or wrap git. On commit it writes an authorship log as a git note under `refs/notes/ai` ([spec v3.0.0](https://github.com/git-ai-project/git-ai/blob/main/specs/git_ai_standard_v3.0.0.md)). The log is an attestation section (file path, then two-space-indented `<key> <line ranges>`; keys `s_<session>::t_<turn>` for AI sessions, `h_<hash>` for known humans), a `---` divider, and a JSON metadata section with `schema_version: "authorship/3.0.0"`, `base_commit_sha`, `sessions` (each with `agent_id {tool, id, model}` and `human_author`), `humans`, and legacy `prompts` (`total_additions`, `total_deletions`, `accepted_lines`, `overriden_lines`). Supported agents per README: Claude Code, Codex, Cursor, Copilot, OpenCode, Pi, Windsurf, Droid, Amp, Gemini, Continue, Junie, Rovo Dev, Firebender. Prompts are "scanned and redacted, and saved outside of Git."
- **Borrow**: this is the right git-integration layer for R5 and R10. A reviewer can see, per commit, which lines a session wrote and which the human wrote or rewrote (`overriden_lines` is a direct measure of hand-fixing). pairtime can read `refs/notes/ai` if present rather than reimplementing attribution, and can write notes in the same format so reviewers' existing tools work. "Agents report exactly which lines they wrote" (no AI-text detection) matches the BOUNTY.md thread's point that detectors like Pangram are unreliable.
- **Falls short**: no time dimension at all. Lines are a poor proxy for struggle: a one-shot can produce 2,000 attributed lines in two minutes.

### 1.4 Agent Trace (Cursor's attribution RFC)

- **Spec**: [agent-trace.dev](https://agent-trace.dev/), version `0.1.0`, status RFC, dated January 2026, license CC BY 4.0. The page links to `github.com/cursor/agent-trace`, which returned HTTP 404 on 2026-11-19 (moved or made private; the site itself still serves). Credited partners: Amp, Amplitude, Cline, Cloudflare, Cognition, git-ai, Jules, OpenCode, Tapes, Vercel. InfoQ covered it on 2026-02-04 ([article](https://www.infoq.com/news/2026/02/agent-trace-cursor/)).
- **Schema** (JSON Schema 2020-12, `$id` `https://agent-trace.dev/schemas/v1/trace-record.json`, MIME `application/vnd.agent-trace.record+json`): top-level required `version`, `id` (UUID), `timestamp` (RFC 3339), `files`; optional `vcs {type: git|jj|hg|svn, revision}`, `tool {name, version}`, `metadata` (free-form, vendor keys namespaced). `files[] {path, conversations[]}`; `conversations[] {ranges[], url?, contributor?, related[]?}`; `ranges[] {start_line, end_line, content_hash?, contributor?}`; `contributor {type: human|ai|mixed|unknown, model_id?}`. Storage is "implementation-defined" (files, git notes, database).
- **Borrow**: the four-way contributor type (`mixed` is exactly "the human reworked AI code"), and `content_hash` so attribution survives moves. Emitting Agent Trace records alongside git-ai notes costs little and makes pairtime's evidence readable by third-party tools.
- **Falls short**: the only time field is the record `timestamp`. No duration or effort. The spec's own `version` regex (`^[0-9]+\.[0-9]+$`) rejects its `0.1.0` examples, per the fetched page, which says something about its maturity.

### 1.5 vibe-log: session duration from message gaps, LLM-scored prompts

- **Repo**: [vibe-log/vibe-log-cli](https://github.com/vibe-log/vibe-log-cli), MIT, last push 2026-04-19 (quiet since). TypeScript. Reads Claude Code, Codex and Cursor sessions (`src/lib/readers/claude.ts`, `codex.ts`, `cursor.ts`).
- **What it measures**: per-session duration, languages, edited files, model switches, plan-mode exits (`ExitPlanMode` tool uses), and a 0-100 prompt-quality score.
- **How**: `calculateDuration` in `src/lib/readers/claude.ts` sums the gaps between consecutive messages, user *and* assistant, counting only gaps of at most `MAX_IDLE_GAP = 15 * 60` seconds, capped at `MAX_SESSION_DURATION = 8 * 60 * 60`. `src/lib/prompt-analyzer.ts` sends each prompt with 2-3 turns of context to a local Claude (`executeClaude`, default model `haiku`) and asks for a score on "clarity, context, success criteria, examples if needed", a diagnosis of at most 20 words, and an emoji tag (vague, missing context, lacking specificity). Its rubric explicitly scores short direct answers to the agent's questions ("Yes", "PostgreSQL") as 80-100. `src/lib/message-sanitizer.ts` strips content before cloud upload. It also generates standups and "personality" coaching.
- **Borrow**: the prompt rubric, especially "a terse answer to the agent's question is a good prompt", which prevents a length-based score from punishing real steering. Plan-mode exits as an engagement signal. Running the LLM judge locally on the user's own subscription so prompts never leave the machine (R6).
- **Falls short**: duration counts assistant messages, so agent runtime counts as the human's time, which is the same failure as WakaTime. LLM-scored prompts are gameable if the score feeds credit directly.

### 1.6 ccusage: tokens and cost from local logs

- **Repo**: [ccusage/ccusage](https://github.com/ccusage/ccusage) (moved from `ryoppippi/ccusage`), MIT (the LICENSE file is MIT; GitHub reports `NOASSERTION`), v20.0.51 (2026-11-14), about 19,500 stars. TypeScript plus a `rust/` tree.
- **What it measures**: token usage and estimated cost by day, week, month, session, and Claude Code's 5-hour billing window (`ccusage blocks`), for 20 sources: Claude Code, Codex, OpenCode, Amp, Droid, Codebuff, Cline, Hermes Agent, pi-agent, Goose, OpenClaw, Kilo, Kimi, Qwen, Copilot CLI, Gemini CLI, Antigravity, Grok Build CLI, ZCode, DeepSeek Harness (README).
- **Borrow**: its per-source readers are the most widely used open map of where each CLI keeps its logs. A "tokens per human-minute" ratio is a useful dark-flow signal (lots of spend, little steering), and ccusage's math gives the numerator.
- **Falls short**: tokens measure the model's work. Nothing about the human.

### 1.7 SpecStory: local-first transcript capture

- **Repo**: [specstoryai/getspecstory](https://github.com/specstoryai/getspecstory), Apache-2.0 for the repo and CLI; the Cursor and VS Code Copilot extensions are listed as "Closed" source in its README. Last push 2026-11-18.
- **What it does**: saves every AI conversation as Markdown under `.specstory/history/` in the project. CLI providers (open source, `specstory-cli/pkg/providers/`): Claude Code, Codex CLI, Copilot CLI, Cursor CLI, Cursor Cloud Agents (via `CURSOR_API_KEY`), Gemini CLI, Droid, plus others listed in the README. `specstory run <agent>` wraps the agent. Optional sync to `cloud.specstory.com`. `/lore` mines history into agent skills.
- **Borrow**: the in-repo, human-readable transcript as the audit artifact. If a student chooses to submit evidence for R10, a redacted Markdown transcript a reviewer can read is the format reviewers will want. The provider package layout is a model for pairtime's adapter plugins (R7).
- **Falls short**: it records conversations, not time, and wrapping the agent (`specstory run`) is an extra step that R4 forbids. Committing transcripts to the repo is the "security risk" the BOUNTY.md thread warns about.

### 1.8 Wakapi: self-hosted WakaTime-compatible server

- **Repo**: [muety/wakapi](https://github.com/muety/wakapi), MIT, v2.18.2 (2026-10-14), about 4,500 stars. Go.
- **What it measures**: WakaTime heartbeats, so the same thing as 1.1, without the AI-specific parsing (which lives in wakatime-cli).
- **How**: one Go binary. `db.dialect` defaults to `sqlite3` (also `postgres`, `mysql`). Daily summaries are precomputed by cron (`app.aggregation_time`, default `0 15 2 * * *`). `app.heartbeat_max_age` defaults to `4320h` (180 days) for accepting old heartbeats, and `app.data_retention_months` defaults to `-1` (keep forever). `config.default.yml` and README.
- **Borrow**: single binary plus SQLite as the default deployment. Nightly pre-aggregation into summary rows so dashboards never scan raw heartbeats (R11). WakaTime API compatibility, so the existing WakaTime editor plugins can feed pairtime's editor signal for free (R7).
- **Falls short**: inherits WakaTime's activity model.

### 1.9 ActivityWatch: local, privacy-first activity tracking

- **Repo**: [ActivityWatch/activitywatch](https://github.com/ActivityWatch/activitywatch), MPL-2.0, about 19,700 stars, last push 2026-11-15. Python and Rust.
- **What it measures**: active window (app and title), AFK state, browser tab, editor file, per user, all local. No server, no accounts.
- **How**: watchers send *heartbeats* to a local server on `localhost:5600`. A heartbeat merges with the previous event when the data is identical and it arrives within `pulsetime` seconds, so steady state costs one row update instead of one row per poll. The AFK watcher ([aw-watcher-afk](https://github.com/ActivityWatch/aw-watcher-afk), MPL-2.0) polls input every `poll_time = 5` s and marks AFK after `timeout = 180` s without input, sending heartbeats with `pulsetime = timeout + poll_time` (`aw_watcher_afk/config.py`, `afk.py`).
- **Borrow**: the heartbeat-with-pulsetime merge, which is the cheapest correct way to store "still active" on a low-spec laptop (R8). The idea that the AFK signal is *presence*, not content: seconds since last input, never keystrokes (R6). Everything stays local by default.
- **Falls short**: no projects, no server, no AI awareness, and window titles are sensitive (they include file names and page titles), which is more than pairtime should upload.

### 1.10 Lapse and Lookout: Hack Club's timelapse evidence

- **Lapse**: [hackclub/lapse](https://github.com/hackclub/lapse), MIT, commit `202d020` (2026-11-17). "A place for Hack Clubbers to record and share timelapses", which integrates with Hackatime. "All timelapses are encrypted before being published", so only the author can view them until they choose to share. Records through Lookout. Docker services `client`, `server`, `worker` (ffmpeg encoding; "CPU-bound - it's a good idea to put it on a beefy server"), Postgres and Redis. Zach: "currently Lapse is not passive. You need to turn it on to start tracking" (BOUNTY.md).
- **Lookout**: [hackclub/lookout](https://github.com/hackclub/lookout), **AGPL-3.0**, commit `be1b7b9` (2026-11-18), formerly "Collapse". "Simple time-tracking via periodic screenshots as a service, designed to be embedded into Hack Club programs." The client records a short clip every minute (a frame every 10 s, about 6 frames per minute) and uploads it as one capture, or one screenshot per minute if it can't encode video. "The server tracks the number and timing of captures received to validate time." Sessions auto-pause after 10 minutes of inactivity and auto-stop after 24 hours. No users or accounts; a session token issued by the program controls everything. Migration `packages/server/drizzle/0034_capture_activity.sql` adds per-capture `activity` JSON: pointer position as a fraction of the display, the frontmost app and captured-window focus, sampled at every clip frame (marked internal only).
- **Borrow**: Lookout is already AGPL-3.0 and already accepted by Hack Club programs, so pairtime can treat a Lookout session as an optional, user-initiated *evidence attachment* for submitted hours (R10) instead of building its own screen recorder. The per-capture interaction metadata (pointer, frontmost app, focus) is a good model for low-cost presence evidence. The "time is validated by the count and timing of captures the server received" rule is the same idea as server-witnessed timestamps (see grit, 1.14).
- **Falls short**: not passive (R4). Screen capture is the most privacy-invasive option (R6), heavy on a 10-year-old laptop (R8), and expensive to host (video encoding and storage, R11). A timelapse shows a screen, not whether the human was thinking: an agent's terminal scrolling looks busy.

### 1.11 Vendor analytics: Claude Code, Cursor, GitHub Copilot

These are enterprise dashboards. None is available to a teenager on a personal plan, but their field choices show what the vendors think is measurable, and Claude Code's local OTel export works on any plan.

| | Claude Code | Cursor | GitHub Copilot |
|---|---|---|---|
| Source | [OTel export](https://code.claude.com/docs/en/monitoring-usage) (any plan) and [Analytics Admin API](https://platform.claude.com/docs/en/manage-claude/claude-code-analytics-api) (`GET /v1/organizations/usage_report/claude_code`, daily, Console orgs only, up to 1 h delay) | [AI Code Tracking API](https://cursor.com/docs/account/teams/ai-code-tracking-api) (`GET /analytics/ai-code/commits`, `/changes`, `/commits/:commitHash`; Enterprise only, alpha) | [Copilot usage metrics REST API](https://docs.github.com/en/rest/copilot/copilot-usage-metrics?apiVersion=2026-03-10) (`/orgs/{org}/copilot/metrics/reports/users-1-day?day=`, `users-28-day/latest`, etc.) |
| Time | `claude_code.active_time.total` (s), attribute `type`: `"user"` for keyboard interactions, `"cli"` for tool execution and AI responses. "Excluding idle time." | none | none |
| Human input | `claude_code.user_prompt` event: `prompt_length`, `prompt` (redacted unless `OTEL_LOG_USER_PROMPTS=1`), `prompt.id`, `command_name`. Fires "including on turns Claude Code starts on its own." | none | `user_initiated_interaction_count` |
| Review decisions | `claude_code.code_edit_tool.decision` metric; API `tool_actions.{edit,multi_edit,write,notebook_edit}_tool.{accepted,rejected}`; span `claude_code.tool.blocked_on_user` | none | `code_acceptance_activity_count` |
| Lines | `claude_code.lines_of_code.count` (`type` added/removed); API `lines_of_code.added/removed` | `tabLinesAdded`, `composerLinesAdded`, `nonAiLinesAdded` (= `max(0, total - AI)`, a remainder, not detected) per commit; `changes[]` with `source` `TAB`/`COMPOSER` | `loc_suggested_to_add_sum`, `loc_added_sum`, `loc_deleted_sum` (agent edits count without an accept step) |
| Other | `session.count`, `commit.count`, `pull_request.count`, `cost.usage`, `token.usage`; export intervals default 60 s (metrics) and 5 s (logs) | `commitSource` `ide`/`cli`/`cloud`; blame ranges linked to conversation summaries | `used_agent`, `used_chat`, `used_cli`, and similar flags; LoC "may not match line counts in an editor diff view" |

- **Borrow**: Claude Code's `active_time` split is the vendor's own admission that agent time and human time are different quantities, and its `type="user"` counter is a ready-made human-time signal for one harness (R2, R7). `tool.blocked_on_user` spans measure human review latency directly. Accept/reject counts per edit are a verification signal. `prompt.id` correlation lets pairtime group everything that followed one human prompt.
- **Falls short**: `user_prompt` also fires for agent-initiated turns, so prompt counts alone overstate human input. Cursor's "non-AI lines" are a remainder, not a measurement. Copilot counts agent edits as added lines with no human acceptance. All three are org-level products; the personal-plan path is local OTel or transcripts.

### 1.12 DX, LinearB, Jellyfish: enterprise AI-impact platforms

- **DX** ([AI Measurement Framework](https://getdx.com/blog/ai-measurement-framework-guide/), updated 2026-05-20): three dimensions. Utilization ("AI tool usage (DAUs/WAUs)", "Percentage of PRs that are AI-assisted", "Percentage of committed code that is AI-generated", "Tasks assigned to agents"); Impact ("AI-driven time savings (developer hours/week)", "Developer satisfaction", "Human-equivalent hours (HEH) of work completed by agents", plus Core 4 measures); Cost ("AI spend", "Net time gain per developer"). Headline from its Q1 2026 report: "developers save an average of 3.9 hours per week with AI coding tools with daily users saving 4.72 hours per week" (no sample size given). Warns that "code generation volume" metrics are "particularly susceptible to gaming" and should not be used "for individual performance evaluation". The page doesn't say how time savings are collected; DX is known for developer surveys (unverified from this page).
- **LinearB** ([AI Insights FAQ](https://linearb.helpdocs.io/article/ww1ciql9uw-linear-b-ai-insights-faq), via the earlier agent's fetch; not re-fetched): four signals: commit co-author trailers, AI-written PR comments, PRs opened by known agents, vendor APIs (Copilot, Claude, Codex). It recommends an `AGENTS.md` rule so Codex appends `Co-authored-by: Codex <codex@openai.com>`, and states it does not store source code for detection.
- **Jellyfish** ([AI Impact](https://jellyfish.co/platform/jellyfish-ai-impact/), via the earlier agent's fetch; not re-fetched): "derives AI signals directly from Git, planning systems, and workflow data"; measures "adoption, usage, spend, and outcomes (speed, quality, productivity)".
- **Borrow**: LinearB's AGENTS.md co-author trailer trick is the cheapest possible universal attribution hook, and it matches Karim's AGENTS.md direction (R7). DX's distinction between utilization (did they use AI) and impact (did it help) maps onto pairtime's split between logged agent activity and credited human struggle.
- **Falls short**: all measure teams and outcomes over weeks, for managers. None measures an individual's effort per hour, and the time-savings numbers are surveys, which are useless for fraud review (R9, R10).

### 1.13 Hack Club community and bounty entries

Searched GitHub on 2026-11-19 for repos created after 2026-11-10 matching "productive struggle", "ai time tracking", "ai time tracker", "time tracking claude code", "struggle time" and "ai coding time tracker". The bounty was posted 2026-11-18 23:38 UTC, so anything here is less than a day old.

| Repo | License | Created | What it does | Notes |
|---|---|---|---|---|
| [N1k0s1/grit](https://github.com/N1k0s1/grit) | AGPL-3.0 | 2026-11-19 00:24 UTC | Bounty entry. Credits "only human-steered time" from transcripts plus an OS idle signal. Go, SQLite, SSE. | The closest prior art to pairtime; see 1.14. |
| [polmarza/mod-crono](https://github.com/polmarza/mod-crono) | none stated | 2026-11-17 | Claude Code mod: a "chess clock" (reloj de ajedrez) for human vs Claude time per project. Your clock stops when you send, Claude's runs until it finishes; yours restarts when you type in the prompt box (terminal) or press Start. Auto-pauses after 10 min with no activity. | Not passive (manual Start in the desktop app). The chess-clock framing is a good UI metaphor. |
| [Abdoul-sudo/worklog](https://github.com/Abdoul-sudo/worklog) | MIT | 2026-11-12 | Claude Code mod: time, tokens and API price per project, with a yearly heatmap. | Agent time, not human time (from description; not read). |
| [leue21/timeweft](https://github.com/leue21/timeweft) | MIT | 2026-11-16 | Time tracking for parallel agents per git branch, Jira timesheets. | Billing-oriented (description only). |
| [szmichalczyk/focus-app](https://github.com/szmichalczyk/focus-app) | none stated | 2026-11-16 | macOS local tracker: apps, windows, tabs and Claude Code sessions next to logged time. | macOS only (description only). |

Older Hack Club tools relevant here: [hackclub/elapsed](https://github.com/hackclub/elapsed) (MIT, created 2026-03-10, last push 2026-07-01: "new timelapse tracking tool and media sharing platform"), plus Lapse and Lookout (1.10). The sibling notes list `hackclub/ari`, `hackclub/breadcrumb` and `hackclub/sunbeam-review` as read by the earlier agent; their purpose wasn't recorded and I didn't re-check them.

### 1.14 grit in detail (closest competitor)

[N1k0s1/grit](https://github.com/N1k0s1/grit), AGPL-3.0, Go 1.26, pure Go (`CGO_ENABLED=0`), commit `e7cf94e` (2026-11-19 04:36 UTC). Read from README and `internal/score/score.go`, `internal/score/anomaly.go`, `internal/activity/`.

- **Collection**: reads Claude Code transcripts (`~/.claude/projects/*/*.jsonl`) and Codex rollouts (`~/.codex/sessions/**/rollout-*.jsonl`) passively. Other harnesses pipe hook JSON into `grit hook -harness <name> -kind prompt|stop`, appended to `~/.grit/events.jsonl`.
- **Presence signal**: `grit watch` asks the OS every 15 s for seconds since last input (Windows `GetLastInputInfo` + `GetTickCount`; macOS `CGEventSourceSecondsSinceLastEventType` via purego, `ioreg` `HIDIdleTime` fallback; Linux X11 or GNOME Mutter `org.gnome.Mutter.IdleMonitor` over D-Bus). It stores one bit per minute in a 180-byte bitmap per UTC day under `~/.grit/activity`, plus a second bitmap of minutes the sampler was running, so "no input" and "no data" differ.
- **Scoring rules** (`score.Default`): `IdleTimeout` 10 min (a reply within this gap after the agent finishes counts as engaged think time); typing estimate at `ComposeCPS` 3 chars/s clamped to 20 s-4 min; `LowEffortCap` 90 s of think credit for bare go-aheads (a fixed list: "continue", "yes", "ok", "lgtm", "try again", "more", and so on, or any prompt under 4 characters); agent run counts up to `AgentCap` 8 min only if the human came back within `ReturnWindow` 2 min, otherwise only the first `Unsupervised` 1 min; `ReviewTail` 2 min after the last turn. With activity data covering at least `MinCoverage` 50% of the session ("v2"), only minutes with input inside those windows count. Parallel sessions are deduplicated so five agents count as one person.
- **Prompt tags**: `low-effort`, `debugging` (regex for error, bug, crash, traceback, "doesn't work", why, stuck, still, and similar), `specific` (200 or more characters).
- **Session flags** (never change credit): `autopilot` (at least 4 prompts and half or more low-effort), `mostly-agent` (agent time more than 4x credited), `long-idle` (wall time over 1 h and credited under 20% of it), `regular-timing` (6 or more prompts with coefficient of variation of spacing under 0.1), `repeated-prompts` (same prompt or same template with numbers changed, 3 or more times), `no-input-prompts` (prompts arriving with no keyboard or mouse input), `no-output` (1 h or more with no file touched or commit).
- **Attestation**: `grit sync` sends numbers only (start, end, credited seconds, counts, rule strings). Each turn adds a link in a hash chain over (prompt time, kind, sha256 of redacted prompt), salted per session. The server records when it first saw each link. On `grit submit`, the server recomputes the chain: turns seen within 15 min are **witnessed**, later ones **backfilled**, mismatches fail. Trust tier per session: high (witnessed and activity-scored), medium (one), low (neither).
- **Server**: `gritd`, one Go binary with SQLite, public profiles, projects, git links, leaderboard, live updates over SSE, and a review queue where a reviewer reads prompts and approves, deflates or rejects. README estimates 150,000-250,000 sessions/month and about 1 GB/month of new data, "well under $50", not yet load-tested.
- **Self-reported gaps**: scripted prompts plus a mouse jiggler are caught only by timing/repetition flags and reviewers; hand edits are judged per minute; witnessing needs `grit watch` running; Windows and Linux idle paths compile in CI but hadn't been run on real machines.
- **What this means for pairtime**: grit already covers the core thesis, at least on paper. pairtime's differentiation has to come from (a) accuracy validated against reviewer judgments (R2, R9), not hand-tuned windows; (b) broader harness coverage via an adapter model plus AGENTS.md fallback (R7); (c) the motivation layer, which grit barely has (R3); and (d) adoption by programs (acceptance criterion A). The bounty goes to the first entry to reach 1,000 weighted projects, so program adoption is the real race, not features.

### 1.15 Summary: what to borrow

| Tool | Measures | License | Take | Leave |
|---|---|---|---|---|
| WakaTime CLI | editor + agent heartbeats | BSD-3 | ~45 transcript parsers, checkpoints, AI vs human line split | agent-edit heartbeats as time |
| Hackatime | heartbeats, 2-min gap cap, AI discount | MIT | 2-min cap; daily diminishing-returns curve as a last-resort cap | flat haircut on AI time |
| Wakapi | heartbeats | MIT | single binary + SQLite; nightly summaries; WakaTime API compat | |
| ActivityWatch | window, AFK, local | MPL-2.0 | pulsetime heartbeat merge; AFK = seconds since input | uploading window titles |
| Lookout / Lapse | screen captures | AGPL-3.0 / MIT | opt-in evidence attachment for submitted hours | as the default signal |
| git-ai | line authorship in `refs/notes/ai` | Apache-2.0 | read and write its notes; `overriden_lines` | |
| Agent Trace | line attribution record | CC BY 4.0 | `mixed` contributor type; `content_hash` | |
| vibe-log | message-gap duration, LLM prompt score | MIT | prompt rubric ("a terse answer to a question is a good prompt") | counting assistant messages as time |
| ccusage | tokens, cost | MIT | log locations; tokens per human-minute | |
| SpecStory | transcripts as Markdown | Apache-2.0 (CLI) | readable transcript as the evidence format | wrapping the agent; committing transcripts |
| Claude Code OTel | `active_time{type=user\|cli}`, prompts, decisions | docs | the user/cli split; `tool.blocked_on_user` | trusting `user_prompt` as always human |
| Cursor / Copilot APIs | AI lines per commit | proprietary | | Enterprise only |
| DX / LinearB / Jellyfish | team adoption, surveys | proprietary | co-author trailer via AGENTS.md | survey-based time |
| grit | human-steered time from transcripts + idle bitmap | AGPL-3.0 | explainable per-minute rules; witnessed hash chain; flags that don't change credit | hand-tuned constants with no validation set |

## 2. Measurement science

(pending)

## 3. Signals that separate engaged from passive work

(pending)

## 4. Motivation for teens

(pending)

## 5. Server cost model for R11

(pending)

## 6. Design implications

(pending)

## 7. Open questions

(pending)

## 8. Sources

(pending)
