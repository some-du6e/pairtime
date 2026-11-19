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

(Entries 1.8 onward in progress.)

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
