# Raw notes: Hackatime, WakaTime, Lapse, and the Hack Club YSWS pipeline

These are unverified notes from a research agent that was cancelled on 2026-11-19, before it wrote its doc. Work on pairtime was paused. The orchestrator extracted them from the agent transcript so the work isn't lost. Fetched-page summaries were written by the fetch tool and have not been fact-checked. Treat them as leads, not findings.

## GitHub repos the agent read

- [hackclub/data-warehouse](https://github.com/hackclub/data-warehouse) at `4ff995dddcb7`
- [hackclub/elapsed](https://github.com/hackclub/elapsed)
- [hackclub/fellow-ysws](https://github.com/hackclub/fellow-ysws)
- [hackclub/hackatime](https://github.com/hackclub/hackatime) at `f26a2787f83b`
- [hackclub/hackatime-desktop](https://github.com/hackclub/hackatime-desktop)
- [hackclub/hackatime-setup](https://github.com/hackclub/hackatime-setup)
- [hackclub/lapse](https://github.com/hackclub/lapse) at `202d0209b03e`
- [hackclub/lookout](https://github.com/hackclub/lookout) at `be1b7b9230f2`
- [hackclub/slacker-news](https://github.com/hackclub/slacker-news)
- [hackclub/stardance](https://github.com/hackclub/stardance) at `b1d3e0eac1ae`
- [hackclub/the-carnival](https://github.com/hackclub/the-carnival)
- [wakatime/desktop-wakatime](https://github.com/wakatime/desktop-wakatime)
- [wakatime/macos-wakatime](https://github.com/wakatime/macos-wakatime)
- [wakatime/vscode-wakatime](https://github.com/wakatime/vscode-wakatime)
- [wakatime/wakatime-cli](https://github.com/wakatime/wakatime-cli)

## Files the agent pulled from those repos

- `ht`: `.env.example`, `AGENTS.md`, `ARCHITECTURE.md`, `CLAUDE.md`, `DEVELOPMENT.md`, `Dockerfile`, `Dockerfile.production-worker`, `LICENSE`, `README.md`, `app/controllers/admin/timeline_controller.rb`, `app/controllers/api/admin/v1/admin_controller.rb`, `app/controllers/api/admin/v1/heartbeats_controller.rb`, `app/controllers/api/admin/v1/poisons_controller.rb`, `app/controllers/api/admin/v1/timeline_controller.rb`, `app/controllers/api/hackatime/v1/hackatime_controller.rb`, `app/controllers/api/privacy_controller.rb`, `app/controllers/api/summary_controller.rb`, `app/controllers/api/v1/authenticated/heartbeats_controller.rb`, `app/controllers/api/v1/authenticated/hours_controller.rb`, `app/controllers/api/v1/authenticated/me_controller.rb`, `app/controllers/api/v1/authenticated/projects_controller.rb`, `app/controllers/api/v1/currently_hacking_controller.rb`, `app/controllers/api/v1/leaderboard_controller.rb`, `app/controllers/api/v1/stats_controller.rb`, `app/controllers/api/v1/users_controller.rb`, `app/controllers/concerns/api/admin/v1/user_utilities.rb`, `app/controllers/profiles_controller.rb`, `app/controllers/users_controller.rb`, `app/javascript/pages/Home/signedIn/AiCodingMeter.svelte`, `app/jobs/heartbeat_import_job.rb`, `app/jobs/leaderboard_update_job.rb`, `app/models/concerns/heartbeatable.rb`, `app/models/currently_hacking.rb`, `app/models/heartbeat.rb`, `app/models/heartbeat_exclusion.rb`, `app/models/ja4.rb`, `app/models/leaderboard.rb`, `app/models/leaderboard_entry.rb`, `app/models/trust_level_audit_log.rb`, `app/models/user.rb`, `app/services/dashboard_data/snapshots.rb`, `app/services/dashboard_stats.rb`, `app/services/heartbeat_import_service.rb`, `app/services/heartbeat_ingest.rb`, `app/services/leaderboard_cache.rb`, `app/services/leaderboard_service.rb`, `app/services/privacy/eraser.rb`, `app/services/privacy/token.rb`, `app/services/profile_stats_service.rb`, `app/services/project_stats_query.rb`, `app/services/project_stats_service.rb`, `app/services/timeline_service.rb`, `app/services/wakatime_user_agent_parser.rb`, `config/database.yml`, `config/environments/production.rb`, `config/initializers/good_job.rb`, `config/initializers/rack_attack.rb`, `config/puma.rb`, `config/routes.rb`, `db/migrate/20260609195005_create_ja4s_and_add_ja4_to_heartbeats.rb` …
- `stardance`: `AI_POLICY.md`, `README.md`, `app/controllers/admin/certification/integrity/deductions_controller.rb`, `app/controllers/admin/certification/ysws_controller.rb`, `app/controllers/api/v1/certification_decisions_controller.rb`, `app/controllers/api/v1/fraud_reports_controller.rb`, `app/controllers/projects/lookout_sessions_controller.rb`, `app/helpers/admin/certification/integrity_helper.rb`, `app/jobs/certification/airtable_submission.rb`, `app/jobs/certification/ysws_airtable_sync_job.rb`, `app/models/certification/integrity.rb`, `app/models/lookout_session.rb`, `app/models/user/hackatime_project.rb`, `app/models/user/hackatime_sync.rb`, `app/services/admin/hour_funnel.rb`, `app/services/certification/unified_ysws_service.rb`, `app/services/certification/ysws_airtable.rb`, `app/services/hackatime_service.rb`, `app/services/lapse_service.rb`, `app/services/lookout_heartbeat_forwarder.rb`, `app/services/lookout_push_status.rb`, `app/services/lookout_service.rb`
- `lapse`: `AGENTS.md`, `API.md`, `README.md`, `apps/client/src/encryption.ts`, `apps/server/README.md`, `apps/server/src/encryption.ts`, `apps/server/src/hackatime.ts`, `apps/server/src/lookoutPublish.ts`, `apps/server/src/routers/draftTimelapse.ts`, `apps/server/src/routers/hackatime.ts`, `apps/server/src/routers/timelapse.ts`, `apps/server/src/upload.ts`, `docs/authentication.md`, `docs/custom-clients.md`, `docs/lookout-desktop.md`, `docs/privacy.md`, `packages/api/src/contracts/detection.ts`, `packages/api/src/contracts/draftTimelapse.ts`, `packages/api/src/contracts/timelapse.ts`, `packages/shared/src/encryption.ts`
- `lookout`: `README.md`, `clients/native/README.md`, `crates/lookout-core/README.md`, `crates/lookout-core/src/capture_loop.rs`, `docs/integration.md`, `packages/server/API.md`, `packages/server/drizzle/0007_credit_mode.sql`, `packages/server/drizzle/0034_capture_activity.sql`
- `wakacli`: `cmd/heartbeat/ai_sync.go`, `cmd/heartbeat/heartbeat.go`, `cmd/offlinesync/offlinesync.go`, `cmd/root.go`, `pkg/ai/ai.go`, `pkg/ai/checkpoints.go`, `pkg/ai/claude.go`, `pkg/ai/codex.go`, `pkg/ai/copilot.go`, `pkg/ai/cursor.go`, `pkg/ai/generic_provider.go`, `pkg/ai/sync_lock.go`, `pkg/ai/transcript_clock.go`, `pkg/api/api.go`, `pkg/api/heartbeat.go`, `pkg/filestats/filestats.go`, `pkg/heartbeat/category.go`, `pkg/heartbeat/heartbeat.go`, `pkg/heartbeat/sanitize.go`, `pkg/offline/offline.go`, `pkg/params/params.go`
- `dw`: `bin/ysws_true_expense.py`, `orpheus_engine/defs/airtable/config.py`, `orpheus_engine/defs/airtable/generated_ids.py`, `orpheus_engine/defs/unified_ysws_db/__init__.py`, `orpheus_engine/defs/unified_ysws_db/definitions.py`, `orpheus_engine/defs/ysws_programs_sync/definitions.py`

## Web searches

- "WakaTime "AI coding" category how detect AI vs human line changes"
  - [AI vs Human classification issue in VSCode · Issue #474 · wakatime/vscode-wakatime](https://github.com/wakatime/vscode-wakatime/issues/474)
  - [CHANGELOG.md](https://github.com/wakatime/vscode-wakatime/blob/master/CHANGELOG.md)
  - [src/wakatime.ts](https://github.com/wakatime/vscode-wakatime/blob/master/src/wakatime.ts)
  - [Wrongfully assuming AI code is being used](https://github.com/wakatime/vscode-wakatime/issues/440)
  - [Features and Usage | wakatime/vscode-wakatime | DeepWiki](https://deepwiki.com/wakatime/vscode-wakatime/1.2-features-and-usage)
- "WakaTime desktop app track AI apps ChatGPT Claude desktop macos-wakatime"
  - [WakaTime Desktop](https://wakatime.com/desktop)
  - [Mac - WakaTime - Open source Mac system tray app for automatic time tracking and metrics generated f](https://wakatime.com/mac)
  - [Automatic App Time Tracking - WakaTime - See where your workday goes with WakaTime Desktop.](https://wakatime.com/time-tracking)
  - [macOS-wakatime](https://github.com/wakatime/macos-wakatime)
  - [https://wakatime.com/llms.txt](https://wakatime.com/llms.txt)
- "alanhamlett gist wakatime durations algorithm heartbeats joined timeout"
  - [Turn an array of heartbeats into durations by combining heartbeats that are within Keystroke Timeout](https://gist.github.com/alanhamlett/0d0c496fe40d977dbdbf0abf0b34ec57)
  - [Configurable heartbeat offset / AFK timeout · Issue #156 · muety/wakapi](https://github.com/muety/wakapi/issues/156)
  - [WakaTime API Docs](https://wakatime.com/developers)
  - [Alan Hamlett\n         \n         \n          alanhamlett](https://gist.github.com/alanhamlett)
  - [FAQ](https://wakatime.com/faq)
- "Hack Club "Fraud Squad" Hackatime review tool github"
  - [Fraud Department — Hack Club](https://fraud.hackclub.com/)
  - [hackclub/nephthys](https://github.com/hackclub/nephthys)
  - [https://flavortown.hackclub.com/projects/7626](https://flavortown.hackclub.com/projects/7626)
  - [README // You ship, We ship](https://readme.hackclub.com/ysws)
  - [Help – Hack Club](https://help.hackclub.com/)

## Pages fetched, with the fetch tool's summary

### https://wakatime.com/developers

Asked: Extract exactly: (1) the heartbeat object schema for POST /api/v1/users/current/heartbeats and heartbeats.bulk, every field name with its type and description, including any AI fields (ai_line_changes, human_line_changes, ai_model, ai_prompt_length, ai_input_tokens, etc.) and the list of allowed cat

> ## 1. Heartbeat object: `POST /api/v1/users/current/heartbeats` and `/heartbeats.bulk`
>
> - **Scope:** `write_heartbeats`.
> - **Bulk:** takes a JSON array of up to 25 items per call.
> - **Editor/OS:** inferred from the User-Agent header. The docs suggest using wakatime-cli rather than calling this endpoint directly.
> - **Response:** `202` with `id`, `entity`, `type` and `time`.
>
> | Field | Type | Description (verbatim) |
> |---|---|---|
> | `entity` | string | "entity heartbeat is logging time against, such as an absolute file path or domain" |
> | `type` | string | "type of entity; can be file, app, url, or domain" |
> | `category` | string | "category for this activity (optional); normally this is inferred automatically from type" |
> | `time` | float | "UNIX epoch timestamp; numbers after decimal point are fractions of a second" |
> | `project` | string | "project name (optional)" |
> | `project_root_count` | integer | "count of the number of folders in the project root path (optional)". Example: root `/Users/user/projects/wakatime` with entity `…/models/user.py` gives `5`, leaving `models/user.py` |
> | `branch` | string | "branch name (optional)" |
> | `language` | string | "language name (optional)" |
> | `dependencies` | string | "comma separated list of dependencies detected from entity file (optional)" |
> | `lines` | integer | "total number of lines in the entity (when entity type is file)" |
> | `ai_line_changes` | integer | "number of lines added or removed by GenAI since last heartbeat in …

### https://wakatime.com/developers

Asked: Extract: (1) Summaries endpoint path, params and response fields, especially any AI fields (ai_additions, ai_deletions, human_additions, etc.) and categories; (2) Stats endpoint path, ranges and response fields, including AI fields; (3) Status bar endpoint path and fields; (4) any mention of 'ai cod

> ## 1) Summaries
>
> - **Endpoints:** `GET /api/v1/users/:user/summaries` and `GET /api/v1/users/current/summaries`. Scope: `read_summaries`.
> - **Description:** "A user's coding activity for the given time range as an array of summaries segmented by day." Summaries are read-only. They merge heartbeats that fall within 15 minutes of each other, and that gap follows the account's keystroke-timeout setting.
> - **Required params:** `start` and `end` (Date).
> - **Optional params:** `project`, `branches` (comma-separated), `timeout`, `writes_only`, `timezone`.
> - **`range` param:** can replace start/end. Values are `Today`, `Yesterday`, `Last 7 Days`, `Last 7 Days from Yesterday`, `Last 14 Days`, `Last 30 Days`, `This Week`, `Last Week`, `This Month`, `Last Month`.
>
> **Per-day `grand_total`**
> - Time fields: `digital`, `hours`, `minutes`, `text`, `total_seconds`.
> - AI line counts:
>   - `ai_additions`: "number of lines added by GenAI for this day"
>   - `human_additions`: "number of lines added by old-school typing for this day"
>   - `ai_deletions` and `human_deletions` are the matching removal counts.
> - AI models and cost:
>   - `ai_model_line_changes`: model → lines
>   - `ai_model_costs`: model → "estimated USD cost"
>   - `ai_model_breakdown[]`: `name`, `lines`, `input_tokens`, `output_tokens`, `cached_input_tokens`, `cost`
>   - `ai_model_total_cost`, `ai_input_tokens`, `ai_output_tokens`
> - AI prompts and sessions:
>   - `ai_prompt_length_avg`: "mean character length across all user prompts sent to …

### https://wakatime.com/desktop

Asked: What does WakaTime Desktop track? List exactly: which AI apps it tracks (ChatGPT, Claude, Copilot etc.), how it categorizes them (AI coding vs other), what data it records (window titles, app names, URLs, screenshots?), OS support, whether it's open source and its license, and any statements about A

> This is a short download page, so it doesn't answer most of your questions.
>
> **What it tracks**
> - Page title: "Open source system tray app for automatic time tracking and metrics generated from your computer usage."
> - Body: "The desktop app can track the time you spend in any app."
> - Positioning: "It’s a catch-all if we don’t have a plugin for your IDE or app."
>
> **AI apps (ChatGPT, Claude, Copilot, etc.):** The page names none of them, and it lists no specific applications at all.
>
> **Categorization (AI coding vs. other):** The page doesn't describe any categories.
>
> **Data recorded:** It mentions only time per app and unspecified "metrics." It says nothing about window titles, app names, URLs or screenshots.
>
> **OS support:** "Mac OS", "Windows", "Linux"
>
> **Open source / license:** The title says "Open source," but the page gives no license. Its footer links to WakaTime's GitHub, where you could check.
>
> **AI coding detection:** The page says nothing about it. The only line about coding is "Combine with IDE plugins for more detailed code stats." That suggests richer programming data comes from editor plugins rather than the desktop tool alone.
>
> To learn how it handles AI apps, what data it collects and what license it uses, try the FAQ, Privacy or GitHub links in the page footer.

### https://wakatime.com/llms.txt

Asked: Extract everything about AI coding tracking: how WakaTime detects AI coding vs human coding, which AI tools are supported, the desktop app, the "AI Coding" category, AI line changes, and any links to blog posts or FAQ pages about AI. Also extract the default keystroke timeout and how durations are c

> This page is a promotional overview written for AI assistants, so it covers features rather than technical internals. Direct quotes stay within the 125-character cap, and everything else is paraphrased.
>
> **How AI and human code are told apart**
> - Data is gathered on each developer's own computer through editor plugins and the desktop app. Because of this, the page claims "it knows whether code was AI generated or Human written".
> - It argues that you can't determine AI authorship once code is in a repository. Capturing at the source lets WakaTime see AI output plus "how much the dev manually edited the code before committing".
> - It contrasts itself with tools that only connect to cloud APIs, which can't see human rework, and with tools that cover just one assistant.
>
> **AI tools named** (presented as current highlights, possibly not a complete list)
> - Claude: the Claude Code command line, Claude's desktop applications, and Claude running in VS Code or other editor extensions
> - Codex, Continue, Cody, Roo Code, OpenCode, GitHub Copilot, Cursor, Windsurf, Qoder, Kiro, Cline, Gemini, Pi, Goose
>
> **Desktop app**
> - https://wakatime.com/desktop is promoted for capturing work done outside the editor.
> - The passive, privacy-focused tracker can run inside the editor or as this standalone app. The page also names it as one of two data sources for AI attribution.
>
> **Line-level AI metrics** (the page defines no specific field for AI line changes)
> - **Adoption:** lines written by AI versus …

### https://wakatime.com/faq

Asked: Extract verbatim: (1) how WakaTime calculates coding time from heartbeats, the keystroke timeout default and allowed values, what happens to the time after the last heartbeat; (2) when plugins send heartbeats (every 2 minutes, on file change, on save); (3) anything on AI coding, AI vs human detectio

> Exact wording is in quotation marks. Everything else is paraphrased.
>
> **1) How coding time is calculated and the keystroke timeout**
> - "Heartbeats are joined into durations based on your timeout preference, then displayed on your dashboard."
> - **Definition:** the timeout is "the maximum time allowed between durations when calculating your total coding activity for a day." The FAQ compares it to a granularity knob. It doesn't change how much you actually worked; "it just combines durations with more or less accuracy."
> - **Default:** "The default keystroke timeout is 15 minutes."
>   - Your public profile, leaderboard ranking and public badges always use this default.
>   - Your own setting applies to your personal dashboard, goals, insights, invoices, embedded charts and private project badges.
>   - Team dashboards can have a different timeout, which explains mismatched totals.
> - **Allowed values:** the page doesn't list them. Its example only uses 10, 15 and 30 minutes. With 2 min of typing, a 13 min pause and 1 more min of typing:
>   - 10 min timeout: 3 min
>   - 15 min timeout: 16 min
>   - 30 min timeout: still 16 min
> - **Time after the last heartbeat:** not stated directly.
>   - The closest line is "the timeout preference only affects time *between* heartbeats not surrounding."
>   - This suggests the timeout fills gaps but adds nothing after the final heartbeat (my inference). In the example, the final 1 min burst counts as 1 min at every setting.
>   - Once you're idle longer than …

### https://fraud.hackclub.com/

Asked: What is the Hack Club Fraud Department / Fraud Squad? What do they review, what tools do they use (Hackatime, trust levels, Lapse, timelapses), what counts as fraud (time inflation, AI use without declaration, botting), what are the consequences, and how do they work with YSWS programs? Quote verbat

> **Hack Club Fraud Department / "Fraud Squad FAQ"**
>
> **What it is**
> It's Hack Club's team for catching people who cheat to get rewards. The page's premise is that "Hack Club is a community built on trust", yet "some people attempt to abuse that trust to get free stuff." The reviewers describe themselves as peers: "We're all teens here."
>
> **What they review and the tools they use**
> - **Event projects:** the page says "the fraud team reviewed your project".
> - **Hackatime:** the team "uses certain metrics to attempt to detect people unfairly gaining hours on Hackatime."
> - **Secrecy:** "We don't share the specific patterns we look for, because that would only help people who abuse the system."
> - **Appeal evidence from VS Code or VS Code-based editors (such as Cursor):** zip and send the editor's local history directory.
>   - Windows: "`C:\Users\<YourUsername>\AppData\Roaming\Code\User\History`"
>   - Linux: "`~/.config/Code/User/History`"
>   - macOS: "`~/Library/Application Support/Code/User/History`"
> - **Appeal evidence from other editors:** "we would appreciate a timelapse of your screen while coding." The page adds that "your Hackatime activity should line up with your previous activity."
> - **Not mentioned:** trust levels and Lapse never appear. Only a generic screen recording is referenced.
>
> **What counts as fraud**
> - **Time inflation:** this is the only offense defined, described as "dishonestly sending data to Hackatime" or trying to "game the system."
> - **AI:** the only rule …

### https://readme.hackclub.com/ysws

Asked: Extract everything about the YSWS (You Ship, We Ship) pipeline: the Unified YSWS Projects DB (what system, Airtable base?), required fields (hours, hours override, justification, review status, weighting), how 'weighted projects' are calculated, how programs review and approve hours, how Hackatime h

> **Bottom line:** This page (https://readme.hackclub.com/ysws) is aimed at participants and doesn't answer most of your questions.
>
> **Not covered on the page**
> - **Unified YSWS Projects DB or Airtable:** The page names no database, Airtable base or backend system.
> - **Fields (hours, hours override, justification, review status, weighting):** None are listed.
> - **Weighted projects:** No formula is given. The closest hint is that Flavortown's MacBook Neo "could be bought for 108 hours if the project(s) that were shipped were high quality." That suggests quality affects the payout.
> - **AI usage declaration:** The page doesn't mention AI.
>
> **Review and approval**
> The page describes how projects are reviewed. It doesn't explain how hour totals are approved or adjusted.
> 1. "Submitted projects are given a quick review by the Shipwright team to make sure that it works"
> 2. Next, the project is "handed off to the organizer of the YSWS event" to check it against that event's rules.
> 3. "The Fraud Squad does one last final check before the project is approved for prizes."
> - Open-theme events such as Flavortown (https://flavortown.hackclub.com/) use peer judging instead. "other participants vote on the quality of a shipped project," and "The project creator then gets points based on the quality of their project as determined by the voters."
>
> **Time tracking and verification**
> - **Hackatime** (https://hackatime.hackclub.com/): "Hackatime will automatically detect when you start coding and …

### https://telescreen.hackclub.com/robert/docs

Asked: Extract the full API documentation: what Robert/Telescreen is, the submission kinds supported (hackatime, lapse, lookout, others?), every endpoint with request/response fields, states, creditedSeconds and trustScore meaning, webhooks and their payloads, and how reviewers determine credited time. Quo

> The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed.

