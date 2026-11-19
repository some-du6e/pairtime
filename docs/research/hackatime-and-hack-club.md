# Hackatime, WakaTime, Lapse, Lookout and the Hack Club YSWS pipeline

Status: fact-check in progress (2026-11-19). The log at the end lists what has been checked so far.

Research for pairtime (AGPL-3.0), written 2026-11-19. Requirement IDs (R1-R11, A) refer to [docs/BOUNTY.md](../BOUNTY.md). Every claim cites a URL or a repo path at a pinned commit. "(unverified)" marks anything I could not confirm from a primary source.

## 0. Sources and versions seen

Code was read at these commits (HEAD of the default branch on 2026-11-19 unless noted). Paths in this doc are relative to the repo named in each section.

| Repo | License | Commit read | Date |
|---|---|---|---|
| [hackclub/hackatime](https://github.com/hackclub/hackatime) | MIT | `43bde10b02db` ("Upgrade to Ruby 4.0.10 (#1828)") | 2026-11-19 |
| [hackclub/hackatime-setup](https://github.com/hackclub/hackatime-setup) (installer) | AGPL-3.0 | `c83d1401b46a` | 2026-10-06 |
| [hackclub/hackatime-desktop](https://github.com/hackclub/hackatime-desktop) | MIT | `832a61069323` (app 1.7.5) | 2025-10-24 |
| [hackclub/stardance](https://github.com/hackclub/stardance) | none in API metadata | `b1d3e0eac1ae` | 2026-11-19 |
| [hackclub/lapse](https://github.com/hackclub/lapse) | MIT | `202d0209b03e` | 2026-11-17 |
| [hackclub/lookout](https://github.com/hackclub/lookout) | AGPL-3.0 | `be1b7b9230f2` | 2026-11-18 |
| [hackclub/data-warehouse](https://github.com/hackclub/data-warehouse) | none in API metadata | `4ff995dddcb7` | 2026-11-17 |
| [wakatime/wakatime-cli](https://github.com/wakatime/wakatime-cli) | BSD-3-Clause | `7ab26b5db6fe` (branch `develop`) | 2026-11-05 |
| [hackclub/ari](https://github.com/hackclub/ari) (ship review) | none in API metadata | `637233962a0f` | 2026-11-16 |
| [hackclub/ari-webhooks](https://github.com/hackclub/ari-webhooks) | none in API metadata | `01a47d67aa91` | 2026-11-10 |
| [hackclub/flavortown](https://github.com/hackclub/flavortown) | none in API metadata | `371026446d1b` | 2026-11-19 |
| Robert clients: hackclub/the-carnival, hackclub/pixl, hackclub/trmnt, hackclub/Alchemize | | default branch | 2026-11-19 |
| [YSWS Project Submission Guidelines](https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/) (GitBook, Markdown pages) | | "Last updated: March 2026" | fetched 2026-11-19 |

Licenses come from `gh api repos/OWNER/REPO` (`.license.spdx_id`) on 2026-11-19. Web pages were fetched on 2026-11-19. Telescreen's own source is not public as far as I could find.

## 1. Hackatime

### 1.1 Repos, stack, license, deployment

Hackatime is a Rails monolith at [hackclub/hackatime](https://github.com/hackclub/hackatime), described as "Free, open source, WakaTime-compatible coding time tracker". It is MIT-licensed (`LICENSE`: "Copyright (c) 2026 Hack Club"; `README.md`: "Go wild!"), so pairtime can copy its code into an AGPL-3.0 project as long as the MIT notice is kept. The reverse is not true: Hackatime cannot take AGPL code from pairtime without relicensing.

| Piece | What the repo shows |
|---|---|
| Language/runtime | Ruby 4.0.10 (`Dockerfile`, `ARG RUBY_VERSION=4.0.10`) |
| Framework | Rails `~> 8.1.2` (`Gemfile`), schema version `2026_11_17_120000` (`db/schema.rb`) |
| Database | PostgreSQL with `pg_trgm` and `pg_stat_statements` (`db/schema.rb`). A second Postgres database holds Solid Cache (`config/database.yml`, `production.cache`) |
| Jobs | GoodJob (Postgres-backed). Cron runs only in production (`config/initializers/good_job.rb`); the daily leaderboard rebuilds every minute, the weekly one every 7 minutes |
| Frontend | Inertia Rails 3.21 + Svelte, built with Vite and Bun 1.4 (`Gemfile`, `Dockerfile`, `ARCHITECTURE.md` section 1) |
| Auth | Session login via Hack Club Auth (HCA), Slack, or single-use email link; GitHub linking; Doorkeeper 5.8 OAuth provider with scopes `profile`, `read`, `admin` (`ARCHITECTURE.md` section 2) |
| Other gems | Flipper flags, PaperTrail audit history, blind_index, Sentry, rack-attack, geocoder + maxminddb, rack-mini-profiler (`Gemfile`); Skylight badge in `README.md` |
| Web server | Puma, `RAILS_MAX_THREADS` default 3 (`config/puma.rb`); production DB pool is web threads (default 16) + 8 GoodJob threads (`config/database.yml`) |
| Deploy | `Dockerfile` ("Use with Kamal or build'n'run by hand") plus `Dockerfile.production-worker` for job workers. `config/initializers/git_version.rb` notes that "Coolify overrides" `SOURCE_COMMIT`, so production runs under Coolify |
| Edge | Cloudflare in front. `config/initializers/rack_attack.rb` blocklists non-Cloudflare requests and ingestion reads `CF-Connecting-IP` and `CF-JA4`. The file sets `Rack::Attack.enabled = false`, so its throttles (for example 360 heartbeat POSTs per minute per IP) are defined but off at this commit |

`ARCHITECTURE.md` is the best single map of the code. Its rule for derived data matters for pairtime too: non-deleted `heartbeats` rows are the only source of truth, and durations, rollups, leaderboards, streaks and caches are all rebuilt from them.

Related repos: [hackclub/hackatime-setup](https://github.com/hackclub/hackatime-setup) is the Rust installer (AGPL-3.0, section 1.5). [hackclub/hackatime-desktop](https://github.com/hackclub/hackatime-desktop) is a Vue desktop app (MIT) whose last release, app 1.7.5, is from 2025-10-24.

### 1.2 Data model

Duration is never stored. Everything is derived from `heartbeats` at read time or in rollup jobs (`ARCHITECTURE.md` section 3).

`heartbeats` (`db/schema.rb`; `app/models/heartbeat.rb`):

| Column | Type | Notes |
|---|---|---|
| `id` | bigint | Tie-breaker in every ordering (`ORDER BY time, id`) |
| `user_id` | bigint, not null | |
| `time` | float, not null | Unix epoch seconds with fractions |
| `entity` | string | File path, app or URL/domain |
| `type` | string | `file`, `app`, `domain`, `url` (`inheritance_column = nil` so Rails STI ignores it) |
| `category` | string | Defaults to `coding`, or `browsing` when `type` is `domain`/`url` (`HeartbeatIngest#default_category`) |
| `project`, `branch`, `language` | string | `project` is the project identity; there is no projects table |
| `dependencies` | string[] | |
| `editor`, `operating_system`, `machine`, `user_agent` | string | Editor and OS are parsed from the user agent; `machine` prefers the `X-Machine-Name` header |
| `lines`, `lineno`, `cursorpos`, `line_additions`, `line_deletions`, `project_root_count` | integer | WakaTime fields |
| `is_write` | boolean | |
| `ai_model`, `ai_session`, `ai_subscription_plan` | string | AI fields from newer wakatime-cli |
| `ai_input_tokens`, `ai_output_tokens` | bigint | |
| `ai_prompt_length`, `ai_line_changes`, `human_line_changes` | integer | So yes, Hackatime stores AI and human line changes per heartbeat |
| `source_type` | integer enum | `direct_entry` 0, `wakapi_import` 1, `test_entry` 2 (entity `test.txt` is marked as a test) |
| `ip_address` | inet | From `CF-Connecting-IP` |
| `ja4_id` | int, FK `ja4s` | TLS client fingerprint from Cloudflare's `CF-JA4` header; `ja4s(fingerprint, name)` |
| `fields_hash` | text | MD5 of the JSON of `Heartbeat.indexed_attributes` plus any non-nil AI attributes. A partial unique index over non-deleted rows deduplicates resubmissions |
| `deleted_at` | datetime | Soft delete; default scope hides it |
| `ysws_program` | integer | Ignored column (`ignored_columns += %w[ysws_program] # unused`) |

`db/schema.rb` declares 30 indexes on this table at this commit, most of them partial (`deleted_at IS NULL`) composites led by `user_id` and `time`. PR #1745 counted 32 in production on 2026-10-04 (section 1.10).

Other tables that matter (`db/schema.rb`):

| Table | Key columns | Purpose |
|---|---|---|
| `users` | `trust_level` (int enum), `admin_level`, `username`, `slack_uid`, `hca_id`, `github_uid`, `github_username`, `timezone`, `allow_public_stats_lookup` (default true), `leaderboard_opt_out`, `leaderboard_shadowbanned` + `_reason`, `_expires_at`, `_by_id`, `country_code`, profile link fields | Identity, trust, visibility |
| `trust_level_audit_logs` | `user_id`, `changed_by_id`, `previous_trust_level`, `new_trust_level`, `reason`, `notes`, `original_reason`, `original_notes`, `edited_at`, `edited_by_id` | Every trust change, with edit history |
| `heartbeat_exclusions` | `user_id`, `kind` (`poison` 0, `project_deletion` 1), `project`, `starts_at`, `ends_at`, `reason`, `created_by_id`, `revoked_at`, `revoked_by_id` | Hides heartbeats from every read without deleting them. Check constraints: a poison needs `ends_at` and no project; a project deletion needs a project. One active poison per user |
| `project_repo_mappings` | `user_id`, `project_name`, `repo_url`, `repository_id`, `archived_at`, `public_shared_at` | Per-user project settings keyed by the heartbeat project string |
| `repositories` | `url` (unique), `host`, `owner`, `name`, `stars`, `languages`, `commit_count`, `last_commit_at`, `last_synced_at` | Shared GitHub metadata |
| `commits` | primary key `sha` | Imported commits for mapped repos |
| `dashboard_rollups` | per-user dimension rows | Precomputed all-time dashboard data |
| `leaderboards`, `leaderboard_entries` | `period_type`, `start_date`; entries have `total_seconds`, `discounted_ai_seconds`, `streak_count`, `rank` | Leaderboards |
| `api_keys`, `admin_api_keys`, `oauth_*` | | WakaTime API keys (UUIDv4), scoped admin keys, Doorkeeper |
| `goals`, `project_labels`, `sailors_logs*` | | Goals, labels, Slack notifications |
| `heartbeat_import_runs`, `heartbeat_import_sources`, `instance_import_sources` | | Imports from WakaTime/Wakapi dumps and other instances |
| `wakatime_mirrors` | `endpoint_url` (default `https://wakatime.com/api/v1`), `encrypted_api_key`, `last_synced_heartbeat_id` | Was for forwarding a user's heartbeats to WakaTime or another compatible server. No model or job uses it at this commit, and `after_create :mirror_to_wakatime` is commented out in `app/models/heartbeat.rb` |
| `versions` | PaperTrail | Change history |

`User.trust_level` (`app/models/user.rb`): `blue: 0 # unscored`, `red: 1 # convicted`, `green: 2 # trusted`, `yellow: 3 # suspected (invisible to user)`. `mask_trust_level` turns `yellow` into `blue` in every public answer, so a suspected user cannot tell they are suspected.

### 1.3 Ingestion endpoints

Routes are in `config/routes.rb`; the controller is `app/controllers/api/hackatime/v1/hackatime_controller.rb`. The comment on the namespace reads "Everything in this namespace conforms to wakatime.com's API." Clients set `api_url = https://hackatime.hackclub.com/api/hackatime/v1`, and wakatime-cli appends WakaTime's paths to it.

| Method and path | What it does |
|---|---|
| `POST /api/hackatime/v1/users/:id/heartbeats` | One heartbeat. Returns `202` with the stored row's attributes, or `422 {error, type}` |
| `POST /api/hackatime/v1/users/:id/heartbeats.bulk` | Array of heartbeats (`format == "bulk"`), at most `MAX_BULK_HEARTBEATS = 100`. Returns `201 {responses: [[body, status], ...]}` with one entry per input, so one bad item doesn't reject the batch |
| `GET /api/hackatime/v1/users/:id` | WakaTime-shaped user object (`plan: "free"`) |
| `GET /api/hackatime/v1/users/:id/summaries` | `start`, `end` (YYYY-MM-DD, at most 366 days), optional `project`, `timezone` |
| `GET /api/hackatime/v1/users/:id/statusbar/today` | `data.grand_total.{text,total_seconds}` plus daily goal progress. This is what editor status bars show |
| `GET /api/hackatime/v1/users/current/stats/last_7_days` | WakaTime-shaped stats (editors, languages, machines, projects, OSes) |

Accepted fields (`HEARTBEAT_KEYS`): `ai_input_tokens ai_line_changes ai_model ai_output_tokens ai_prompt_length ai_session ai_subscription_plan branch category created_at cursorpos editor entity human_line_changes is_write language line_additions line_deletions lineno lines machine operating_system project project_root_count time type user_agent plugin`, plus `dependencies[]`. Bodies can be a JSON array (`_json`), `text/plain` JSON, or wrapped in a `hackatime` key.

Authentication (`app/controllers/concerns/user_api_authentication.rb`) takes the API key as `Authorization: Bearer <key>`, as `Authorization: Basic base64(<key>)` (what wakatime-cli sends), or as an `?api_key=` query parameter. The heartbeat endpoint passes `allow_restricted: true`, so a convicted (`red`) user can still log heartbeats; only accounts pending deletion are refused.

What `HeartbeatIngest` (`app/services/heartbeat_ingest.rb`) does to each heartbeat:

- Time must fall in a sanity window of epoch seconds 1,000,000,000 to 2,000,000,000 (2001-09-09 to 2033-05-18). Millisecond, microsecond and nanosecond epochs are rescaled, and Windows FILETIME/WebKit epochs are shifted by 11,644,473,600 s. Anything else raises `InvalidHeartbeatTime`.
- Null bytes are stripped. The `<<LAST_PROJECT>>`, `<<LAST_LANGUAGE>>` and `<<LAST_BRANCH>>` placeholders that wakatime-cli sends are resolved from the user's previous heartbeats, except that `<<LAST_PROJECT>>` is stored as is (it's excluded from leaderboards).
- `category` defaults as described in 1.2. Editor, OS and `ai_model` are parsed from the user agent by `app/services/wakatime_user_agent_parser.rb` (641 lines of special cases), which knows AI agent products such as `claude-code`, `codex`, `cursor`, `github-copilot`, `gemini`, `opencode`, `kiro`, `cline`, `goose` and `qwen-code` (`AI_AGENT_PRODUCTS`).
- Request metadata is attached: IP (`CF-Connecting-IP` or remote IP), `X-Machine-Name`, user agent and JA4 fingerprint (`CF-JA4`).
- Rows are validated, deduplicated by `fields_hash`, and written with one `insert_all ... unique_by: :fields_hash`, so retries from the CLI's offline queue are idempotent. Inserts schedule a dashboard rollup refresh and an asynchronous attempt to map new project names to GitHub repositories.

### 1.4 From heartbeats to durations

The math lives in `app/models/concerns/heartbeatable.rb`, summarised in `ARCHITECTURE.md` section 3:

- `heartbeat_timeout_duration` defaults to `2.minutes`, and no caller overrides it. Ari's independent reimplementation states the same rule ("every gap is worth min(gap, 120s)", section 3.6).
- Order heartbeats by `(time, id)`. The first heartbeat contributes 0. Each later heartbeat contributes `LEAST(time - LAG(time), 120)`, so a gap longer than 2 minutes still adds 2 minutes, not 0.
- Nothing is added after the last heartbeat. A lone heartbeat is worth 0 seconds.
- `to_span` splits a session wherever a gap exceeds the timeout and caps the previous span's tail at the timeout. `GET /api/v1/users/:username/heartbeats/spans` exposes these spans.
- `duration_seconds_boundary_aware` pulls in the heartbeat just before the window so a window doesn't lose its opening gap.
- A streak day needs at least `STREAK_DAY_SECONDS = 15.minutes` of activity in the user's local day.

This is much stricter than WakaTime's default 15-minute keystroke timeout (section 2.4). With a 2-minute cap, an hour counts only if something creates a heartbeat at least every 2 minutes. The VS Code plugin creates one for the same file at most every `TIME_BETWEEN_HEARTBEATS_MS = 120000` ms, and only when an editor event fires (section 1.5), so steady typing in one file yields gaps of a little over 2 minutes, each capped at 120 s. Reading code, or reading a long agent response in a terminal, without any editor event loses everything past the first 2 minutes.

Per-project attribution has two code paths, and they disagree when someone switches projects:

| Path | Used for | Gap rule |
|---|---|---|
| `duration_seconds` on a relation grouped by `project` (`LAG(time) OVER (PARTITION BY project ORDER BY time, id)`) | Dashboard project breakdown (`DashboardData::Snapshots.project_grouped_durations`), project details (`project_details_snapshot`, behind `GET /api/v1/users/:username/projects/details`), and the per-project list in `GET /api/v1/users/:username/stats?features=projects` (`lib/wakatime_service.rb`, `@scope.group(:project).duration_seconds`) | Gaps are computed inside each project. Heartbeats A@0 s, B@30 s, A@60 s, B@90 s give A 60 s and B 60 s, 120 s in total for 90 s of wall time. Each switch can add up to 2 minutes of overlap |
| `attributed_durations_by(scope, field)` (`LAG` over the global order) | Language, editor, OS and category breakdowns, project stats branch and file lists | Each gap is credited to the heartbeat that ends it, so buckets sum to the total |
| `duration_seconds_boundary_aware` over the heartbeats of the selected projects | `GET /api/v1/users/:username/stats?test_param=true&total_seconds=true&filter_by_project[]=...`, which is how Stardance reads the hours for one Stardance project (section 3.3) | One global ordering across the selected projects, plus the heartbeat just before the window |

Programs have noticed this. Flavortown, a current program, keeps a `hackatime_time_loss_audits` table with `per_project_sum_seconds`, `ungrouped_total_seconds` and `difference_seconds` per project ([hackclub/flavortown](https://github.com/hackclub/flavortown) `app/models/hackatime_time_loss_audit.rb`). For R4's "5 different projects in an afternoon", the overlap is small per switch but systematic.

`project_details_snapshot` also applies no category filter, so its per-project totals include `ai coding` and `browsing` heartbeats at full weight. The `test_param=true` mode of the stats endpoint excludes `browsing`, `meeting` and `communicating`, and excludes `ai coding` only when the caller passes `no_ai_coding=true` (`app/controllers/api/v1/stats_controller.rb`). Stardance passes `no_ai_coding: false` (`app/services/hackatime_service.rb` in hackclub/stardance), so its Hackatime hours include agent-driven `ai coding` time at full weight. The leaderboard uses a different scope, `leaderboard_eligible`: category `coding` or `ai coding`, editor not a browser (`BROWSER_EDITORS`: arc, brave, chrome, ..., zen), project not `<<LAST_PROJECT>>`, valid timestamps. It then discounts AI time (`app/models/leaderboard_entry.rb`):

> AI coding earns 90% for the first three hours of a day. After that the rate decays exponentially, halving roughly every 1.4 hours, so a day of AI coding can never be worth more than 90% * (3h + 2h) = 4.5 hours.

The code is `credited = 0.9 * s` for `s <= 3h`, else `0.9 * (3h + 2h * (1 - exp(-(s - 3h) / 2h)))`, applied per user per day. `LeaderboardUpdateJob` stores the difference in `leaderboard_entries.discounted_ai_seconds`. The discount applies only to leaderboards. Project totals and the hours that programs read do not get it.

### 1.5 How editor plugins reach it

Hackatime has no plugins of its own. Users install WakaTime's editor plugins, which all shell out to wakatime-cli, and point them at Hackatime through `~/.wakatime.cfg`.

The installer, [hackclub/hackatime-setup](https://github.com/hackclub/hackatime-setup) (`src/main.rs`, release v1.5.27 of 2026-09-02, binaries for linux aarch64/x86_64, macOS aarch64/x86_64 and Windows x86_64), does four things:

1. Writes `$WAKATIME_HOME/.wakatime.cfg` (default `~/.wakatime.cfg`):
   ```ini
   [settings]
   api_url = https://hackatime.hackclub.com/api/hackatime/v1
   api_key = <UUIDv4 from the Hackatime setup page>
   heartbeat_rate_limit_seconds = 30
   ```
   An "advanced" mode can add `hide_branch_names = true` and a random `hostname`.
2. Seeds `[internal] ai_logs_last_parsed_at = <now>` in `~/.wakatime/wakatime-internal.cfg`. This stops wakatime-cli's AI sync from back-filling months of old agent transcripts as new `ai coding` time on first run.
3. Installs editor plugins for VS Code and its forks (Cursor, Trae, Windsurf, Antigravity), JetBrains IDEs, Zed and Xcode, plus [hackclub/terminal-wakatime](https://github.com/hackclub/terminal-wakatime) for bash, zsh and fish (`README.md`, `src/editor_plugins/`).
4. Sends a test heartbeat (`entity: "test.txt"`) to `{api_url}/users/current/heartbeats` with the key as a Bearer token. Hackatime stores that row as `source_type = test_entry`.

On the client side (wakatime-cli `7ab26b5db6fe`):

- The CLI posts to `{api_url}/users/current/heartbeats.bulk` (`pkg/api/heartbeat.go`) with HTTP Basic auth carrying the key, at most `SendLimit = 10` heartbeats per request (`pkg/offline/offline.go`).
- `heartbeat_rate_limit_seconds` (default `RateLimitDefaultSeconds = 120`) limits how often the CLI talks to the API, not how often heartbeats are created. Heartbeats inside the window go to the offline queue. Hackatime's installer sets it to 30 s, so a Hackatime dashboard lags real activity by about 30 s rather than 2 minutes.
- The offline queue is a bbolt database, `~/.wakatime/offline_heartbeats.bdb`, bucket `heartbeats`, with up to 3 requeue attempts per heartbeat (`maxRequeueAttempts`). `wakatime-cli --sync-offline-activity` flushes it. Because Hackatime deduplicates on `fields_hash`, replaying the queue is safe.
- A second server can receive the same heartbeats. wakatime-cli's `[api_urls]` section maps path regexes to `api_url|api_key` pairs, and "All matching patterns receive heartbeats, not just the first match", on top of the default `api_url` (`USAGE.md`, "Api URLs Section"; parsing in `pkg/params/params.go`). A line like `.* = https://pairtime.example/api/v1|<key>` sends every heartbeat to both Hackatime and pairtime. Pairtime can therefore receive WakaTime data without the user giving up Hackatime.
- The user agent is `wakatime/<cli version> (<os>-<kernel>-<arch>) <go version> <plugin>/<version>`, with an AI model token inserted for transcript-derived heartbeats (`pkg/heartbeat/heartbeat.go` `UserAgent`, `pkg/ai/ai.go` `aiModelUserAgentToken`). Hackatime gets editor, OS and `ai_model` from this string.

What creates a heartbeat is the editor plugin. In [wakatime/vscode-wakatime](https://github.com/wakatime/vscode-wakatime) (`src/wakatime.ts` and `src/constants.ts` at `ec6de79aaccf`, 2026-07-15), selection changes, text edits, tab switches, saves, debug sessions and tasks call `onEvent`. It sends a heartbeat when the file changed, on save (`is_write`), when debugging, compiling or the AI state flips, or when more than `TIME_BETWEEN_HEARTBEATS_MS = 120000` ms have passed since the last one. Time spent in a terminal, a browser or a desktop chat app produces nothing unless another plugin covers it.

### 1.6 Categories, AI coding, AI vs human lines

Hackatime accepts any category string. The values that occur are wakatime-cli's (`pkg/heartbeat/category.go`): `coding`, `ai coding`, `advising`, `browsing`, `building`, `code reviewing`, `communicating`, `debugging`, `designing`, `indexing`, `learning`, `manual testing`, `meeting`, `notes`, `planning`, `researching`, `running tests`, `supporting`, `translating`, `writing docs`, `writing tests`.

Where the categories come from:

- wakatime-cli's `WithCategory` step turns an explicit `coding` into an empty category, keeps any other explicit category, and for file heartbeats without one runs `DetectCategory`: `*_test.go`, paths containing `/test/`, `/tests/`, `/testdata/`, `/spec/` or `/specs/`, and `*.test.*`/`*.spec.*` names become `writing tests`; `.md` and `.mdx` become `writing docs`. Hackatime then defaults an empty category to `coding` (or `browsing` for `domain`/`url` types).
- The VS Code plugin sets `building` while a non-background, non-watch task runs and `debugging` during a debug session (`src/wakatime.ts`), and `ai coding` when its own heuristic thinks AI is inserting code (section 2.2).
- wakatime-cli's AI sync sets `ai coding` on transcript-derived heartbeats and relabels nearby editor heartbeats (section 2.2).

Which categories count where:

| Consumer | Categories counted |
|---|---|
| Leaderboards (`leaderboard_eligible`) | `coding` and `ai coding` only, with the AI discount from 1.4. `writing tests`, `writing docs`, `debugging` and `building` time is left out |
| `/api/v1/users/:username/stats?test_param=true` (Stardance) | Everything except `browsing`, `meeting`, `communicating`, and `ai coding` only if `no_ai_coding=true` |
| Dashboard "AI vs Human Coding" meter (`app/javascript/pages/Home/signedIn/AiCodingMeter.svelte`) | Compares `ai coding` seconds with `coding` seconds, labelled "Based on coding category duration" |
| Streaks | Everything except `browsing` |

AI and human line changes are stored (`heartbeats.ai_line_changes`, `heartbeats.human_line_changes`) and exported (`app/jobs/heartbeat_export_job.rb`), but the only code that reads them is the WakaTime-compatible daily summary, which sums `ai_line_changes` per `ai_model` (`lib/wakatime_service.rb`). No review or payout logic uses them. `ai_cached_input_tokens`, which wakatime-cli sends, is not in `HEARTBEAT_KEYS`, so Hackatime drops it.

### 1.7 Profiles, leaderboards, real-time, sign-in

- Sign-in: Hack Club Auth (HCA, the identity service programs also use), Slack OAuth, or a single-use email link. GitHub is linked as a secondary identity (`ARCHITECTURE.md` section 2; `app/controllers/sessions_controller.rb`).
- Public profiles: `GET /@:username` and `GET /@:username/project/:project_name`, plus OG images (`config/routes.rb`). Stats are public by default (`users.allow_public_stats_lookup` defaults to `true`); `ProfilesController#show` hides them when it is off, except to the owner. Per-project public sharing is opt-in through `project_repo_mappings.public_shared_at`.
- Git integration: projects are free-text names from heartbeats. `AttemptProjectRepoMappingJob` maps a name to a public GitHub repo in the user's namespace, or an org repo they have commits in, at most 50 GitHub requests per user per day. `PullRepoCommitsJob` imports commits. Private repos are never mapped, and the GitHub app asks only for Metadata (read) (`ARCHITECTURE.md` section 5). Background GitHub sync is behind the `github_sync` Flipper flag.
- Leaderboards: daily (rebuilt every minute) and weekly (every 7 minutes) by `LeaderboardUpdateJob`. Entry requires a linked `github_uid`, a trust level other than `red`, no opt-out, and more than 60 credited seconds. Shadowbans (`users.leaderboard_shadowbanned*`) hide a user without telling them.
- Real-time: there are no websockets. The dashboard is recomputed from heartbeats with short caches (1, 5 or 15 minutes per fragment) and a rollup refresh scheduled on every insert (`ARCHITECTURE.md` section 4). `GET /api/v1/currently_hacking` lists users with a heartbeat in the last 5 minutes (`app/jobs/cache/currently_hacking_job.rb`). Editors show today's total through `statusbar/today`.
- Other: daily goals, weekly summary emails, Slack status updates, embeddable badges (`GET /api/v1/badge/:user_id/*project`), data export, and imports from WakaTime, Wakapi and other instances. Mirroring out to WakaTime is not wired up (1.2).

### 1.8 The API YSWS programs call

There is no "submission" object in Hackatime. A program links the participant's Hackatime account, has them pick which Hackatime project names belong to the program's project, and reads seconds for those names over the program's date range. Stardance's client (`app/services/hackatime_service.rb` in hackclub/stardance at `b1d3e0eac1ae`) is the most complete current example:

| Call | Auth | Used for |
|---|---|---|
| `GET /api/v1/authenticated/me` | User's OAuth token (Doorkeeper) | Get the Hackatime user id after OAuth |
| `GET /api/v1/authenticated/api_keys` | OAuth token | Get or create the user's WakaTime API key, cached a week |
| `POST /api/admin/v1/user/get_user_by_email` | Program admin key (`HACKATIME_ADMIN_KEY`) | Find an account by email |
| `GET /api/v1/users/:uid/stats?features=projects&start_date=2026-05-31&test_param=true&no_ai_coding=false` | API key via `users/my/stats`, falling back to the public path | Per-project seconds, and `trust_factor.trust_value` (1 means `red`) |
| same, plus `total_seconds=true&filter_by_project[]=a&filter_by_project[]=b` | same | Total seconds for the set of Hackatime projects linked to one Stardance project (boundary-aware, section 1.4) |
| `GET /api/v1/users/:uid/trust_factor` | none | Masked trust level; works even for banned users with private stats |
| `GET /api/admin/v1/user/info_batch?ids=...` | admin key | Raw trust levels, `yellow` included, up to 2,000 ids |
| `GET /api/v1/users/:uid/heartbeats/spans?start_date&end_date&project[]=` | API key or public | Spans for reviewers |
| `GET /api/v1/my/heartbeats?start_time&end_time` | API key | Raw heartbeats (Stardance uses this to check that Lookout time arrived) |
| `POST /api/hackatime/v1/users/current/heartbeats.bulk` | user's API key | Pushes heartbeats on the user's behalf, in slices of 100 |

Two details matter for pairtime:

- Programs already write heartbeats into Hackatime for users. Stardance creates a Hackatime project by pushing one seed heartbeat (`type: "app"`, `editor: "Stardance"`, `language: "Stardance"`, `plugin: "stardance-project-seed"`). Its comment explains that "a 'project' is just a DISTINCT over the `project` column" and that the first heartbeat is worth 0 seconds, so the seed adds no time. It also forwards Lookout capture time as heartbeats (section 4.2). A tracker can feed hours into every Hackatime-integrated program this way without any program changing its code.
- Any OAuth app the user approves can get the user's full WakaTime API key. `GET /api/v1/authenticated/api_keys` runs only the default `doorkeeper_authorize!` with no scope check and returns the first key, creating one if needed (`app/controllers/api/v1/authenticated/api_keys_controller.rb`, `application_controller.rb`). With that key an app can read and write heartbeats. A pairtime add-on service could get the same access through ordinary Hackatime OAuth.
- Programs send a `RACK_ATTACK_BYPASS` header (`HACKATIME_BYPASS_KEYS`) to skip rate limits, which suggests the throttles have been switched on at some point, even though they are off at this commit.

### 1.9 Anti-fraud and trust

What Hackatime itself provides:

| Tool | Where | What it does |
|---|---|---|
| Trust levels | `users.trust_level`, `User#set_trust`, `trust_level_audit_logs` | `blue` unscored, `green` trusted, `yellow` suspected (shown to the user and to public APIs as `blue`), `red` convicted. Every change needs a reason (`POST /api/admin/v1/user/convict` refuses an empty `reason`) and is logged with the actor. Only superadmin and above can set `red`, and the actor must outrank the target |
| Effects of `red` | `api_access_restricted?`, `LeaderboardUpdateJob` | Off leaderboards; OAuth and read APIs refused. Heartbeat ingestion still works (`allow_restricted: true`) |
| Poison | `heartbeat_exclusions` kind `poison`, `POST/DELETE /api/admin/v1/poison/:hackatime_id` | Hides every heartbeat before a cutoff date from all reads without deleting them; revocable |
| Leaderboard shadowban | `users.leaderboard_shadowbanned*`, `/api/admin/v1/leaderboard_shadowbans` | Hidden from leaderboards with a reason and optional expiry; the user isn't told |
| Alt detection | `GET /api/admin/v1/alts/candidates`, `alts/shared_machines`, `heartbeats/ip_machine_pairs`, `user/get_users_by_ip`, `user/get_users_by_machine` | Finds accounts sharing a machine name and IP address within a lookback window (default 30 days) |
| Client fingerprints | `heartbeats.user_agent` (trigram index), `heartbeats.ja4_id`, `GET /api/admin/v1/heartbeats/by_user_agent_segment` | Search by user-agent fragment; JA4 TLS fingerprints from Cloudflare can expose a script posting heartbeats with a faked editor user agent |
| Quantized visualization | `GET /api/admin/v1/users/:id/visualization/quantized?year&month` | For each day, plots heartbeat `lineno` and `cursorpos` against time on a 400 x 100 grid, with daily totals. Scripted heartbeats show up as flat or regular patterns |
| Admin timeline | `/admin/timeline`, `app/services/timeline_service.rb` | Several users' days side by side: spans with entity, project, editor and language, plus GitHub commit markers |
| Raw data | `GET /api/admin/v1/user/heartbeats`, `user/heartbeat_values`, `user/stats`, `user/projects`, `user/trust_logs`, `user/info_batch` | Program and fraud tooling reads |
| Audit | PaperTrail `versions`, `admin_api_keys` with scopes and expiry jobs | Who changed what |

What the Fraud Department says publicly ([fraud.hackclub.com](https://fraud.hackclub.com/), fetched 2026-11-19):

- "The fraud department uses certain metrics to attempt to detect people unfairly gaining hours on Hackatime." The signals are kept secret, and the page admits "sometimes we misjudge Hackatime data."
- A flag blocks one project from one event; a ban restricts the account. "If you use AI to write or hold the conversation for you, we will turn down your appeal and ban you permanently."
- Evidence for an appeal is VS Code's local history folder (`%APPDATA%\Code\User\History` on Windows, `~/.config/Code/User/History` on Linux, `~/Library/Application Support/Code/User/History` on macOS), or for other editors "a timelapse of your screen while coding". It adds that "your Hackatime activity should line up with your previous activity."

Programs add their own checks on top of Hackatime. Stardance's integrity review (section 3.3) sets bit flags such as `cursor_strange`, `unknown_file`, `entropy_anomaly` and `neuralnet`, and keeps the signal definitions in a private secrets submodule (`app/helpers/admin/certification/integrity_helper.rb`: "the public repo never carries the signal names itself"). Two lessons follow for pairtime. Detection rules that are public get gamed, so the open-source client can't be where trust comes from. And whatever pairtime uploads has to be rich enough for reviewers to run their own checks on it.

### 1.10 Hosting, scale, cost

Public numbers beyond BOUNTY.md R11:

| Fact | Source |
|---|---|
| "41,247 users" and "2,805,220 hours tracked" | [hackatime.hackclub.com](https://hackatime.hackclub.com/) home page, fetched 2026-11-19 |
| "Heartbeats are the bulk of our Postgres: 755 GB for 426M rows, 592 GB of which is 32 indexes." Dashboard rollups "take about 17 worker-hours a day to refresh" | [hackatime PR #1745](https://github.com/hackclub/hackatime/pull/1745) "Move heartbeats to ClickHouse", a draft opened 2026-10-04 and still unmerged on 2026-11-19 |
| In ClickHouse, a `MergeTree` ordered by `(user_id, time, id)`, "All of production fits in 5.5 GiB". The copy drops 23.4M exact duplicates | same PR |
| Users with 1M+ heartbeats exist; counting their heartbeats takes about 300 ms | [hackatime PR #1337](https://github.com/hackclub/hackatime/pull/1337) |
| Production runs on Coolify behind Cloudflare | `config/initializers/git_version.rb`, `config/initializers/rack_attack.rb` |
| Hackatime's Postgres is deliberately overprovisioned (a maintainer in the bounty thread) | BOUNTY.md, themes |

Derived numbers for a load model (my arithmetic, approximate):

- 426M rows over 2.8M tracked hours is roughly 150 heartbeat rows per tracked hour, including duplicates and AI-transcript rows. The share coming from wakatime-cli's AI sync is unknown.
- At 109,759 hours per month (R11), that is about 16M heartbeats per month, roughly 6 per second on average. Peaks are higher because teen activity clusters in evenings and weekends.
- About 1.8 KB per heartbeat row in Postgres including indexes (755 GB / 426M), against about 14 bytes per row in ClickHouse (5.5 GiB / 403M). The storage cost of a heartbeat log is almost all indexes and row overhead, and a columnar store removes it.

So R11 is not tight for a heartbeat server. The costs that could break $500/month are screen-evidence storage (section 4.2) and heavy per-request computation, such as LLM calls per prompt or per review.

Two Hackatime issues show AI-time problems reaching support: [#1763](https://github.com/hackclub/hackatime/issues/1763) (2026-10-16) asks to delete about 1,045 `ai coding` rows in one hour that the AI sync attached to the wrong project from unrelated old folders, and [#1753](https://github.com/hackclub/hackatime/issues/1753) (2026-10-13) asks how to exclude "autonomous AI activity" synced from Codex transcripts. The CLI has a `--sync-ai-disabled` flag (mentioned in #1753).

## 2. WakaTime

### 2.1 Heartbeat schema and API

From [wakatime.com/developers](https://wakatime.com/developers) (fetched 2026-11-19). Base URL `https://api.wakatime.com/api/v1` (`BaseURL` in wakatime-cli `pkg/api/api.go`).

`POST /api/v1/users/current/heartbeats` and `POST /api/v1/users/current/heartbeats.bulk` (also `/users/:user/...`). Scope `write_heartbeats`. The docs say "The bulk endpoint accepts an array of heartbeats, limited to 25 per POST request" and "Editor and OS are detected from the User-Agent header." A single POST returns `202` with `data.{id, entity, type, time}`. The general rate limit is "fewer than 10 requests per second on average over any 5 minute period".

| Field | Type | Documented meaning |
|---|---|---|
| `entity` | string | "absolute file path or domain" |
| `type` | string | `file`, `app`, `url` or `domain` |
| `category` | string | Optional; "normally this is inferred automatically from type" |
| `time` | float | Unix epoch seconds with fractions |
| `project`, `branch`, `language` | string | Optional |
| `project_root_count` | int | Folders in the project root path, so the server can make paths relative |
| `dependencies` | string | Comma-separated (Hackatime takes an array) |
| `lines`, `lineno`, `cursorpos` | int | File length, cursor row and column |
| `is_write` | bool | Heartbeat came from a save |
| `ai_line_changes` | int | "lines added or removed by GenAI since last heartbeat in the current file" |
| `human_line_changes` | int | "lines added or removed by old-school typing since last heartbeat in the current file" |
| `ai_session` | string | AI session id |
| `ai_input_tokens`, `ai_output_tokens` | int | Tokens since the last heartbeat |
| `ai_prompt_length` | int | "number of user prompt characters typed to AI since the last heartbeat" |
| `ai_subscription_plan` | string | Plan of the AI tool |

wakatime-cli also sends `ai_cached_input_tokens` (`pkg/heartbeat/heartbeat.go`), which the public docs don't list. The model is not a heartbeat field; it goes in the user agent (section 2.2). Documented categories: `ai coding`, `browsing`, `building`, `code reviewing`, `coding`, `communicating`, `debugging`, `designing`, `indexing`, `learning`, `manual testing`, `notes`, `researching`, `running tests`, `writing docs`, `writing tests`. The CLI knows five more: `advising`, `meeting`, `planning`, `supporting`, `translating`.

Read endpoints a compatible server would mirror:

| Endpoint | Notes |
|---|---|
| `GET /users/current/durations?date=` | Heartbeats joined into spans; params `project`, `branches`, `timeout`, `writes_only`, `timezone`, `slice_by` (`category`, `dependencies`, `editor`, `entity`, `language`, `machine`, `os`, `project`; default `project`). Each duration carries AI aggregates, including `ai_model_costs` |
| `GET /users/current/summaries?start&end` or `range=` | Per-day totals. `grand_total` has `ai_additions`, `ai_deletions`, `human_additions`, `human_deletions`, `ai_model_line_changes`, `ai_model_costs`, `ai_model_breakdown[]` (`name`, `lines`, `input_tokens`, `output_tokens`, `cached_input_tokens`, `cost`), `ai_input_tokens`, `ai_output_tokens` and `ai_prompt_length_avg` (from the raw notes' fetch summary; not re-checked) |
| `GET /users/current/statusbar/today` | What editor plugins display |
| `GET /users/current/stats/:range` | Aggregates by editor, language, project and more |

Durations, from Alan Hamlett's reference code (gist [processHeartbeatsIntoDurations.js](https://gist.github.com/alanhamlett/0d0c496fe40d977dbdbf0abf0b34ec57), BSD-3-Clause, last active 2025-04-21):

- If the next heartbeat is less than `timeout` minutes away (default 15), the current heartbeat gets the full gap. Otherwise it gets 0, so an idle gap adds nothing at all, unlike Hackatime's 2-minute cap.
- The time before a project switch goes to the earlier heartbeat's project, the reverse of Hackatime's `attributed_durations_by`.
- Mini-durations merge only while the project (case-insensitive) stays the same. Entity, language and category don't split a duration.
- The last heartbeat adds 0 unless the next day's first heartbeat is within the timeout.

The FAQ adds that public profiles and leaderboards always use the default 15-minute timeout, and that "the timeout preference only affects time *between* heartbeats not surrounding" ([wakatime.com/faq](https://wakatime.com/faq), from the raw notes' fetch summary).

For pairtime, compatibility means accepting this heartbeat shape at `/api/v1/users/current/heartbeats(.bulk)` with Basic or Bearer key auth, answering `statusbar/today`, and tolerating unknown fields. Then every WakaTime plugin, Hackatime's installer and wakatime-cli's AI parsers work against pairtime by changing only `api_url`.

### 2.2 AI tracking in wakatime-cli

All of this is client-side, in [wakatime/wakatime-cli](https://github.com/wakatime/wakatime-cli) `pkg/ai` at `7ab26b5db6fe` (about 48,000 lines of Go including tests). The server only receives heartbeats with `category: "ai coding"` and AI fields.

How it works (`pkg/ai/ai.go`, `WithAISync`):

1. Every time an editor plugin runs wakatime-cli to send a heartbeat, a `WithAISync` step in the heartbeat pipeline takes a cross-process sync lock (`pkg/ai/sync_lock.go`; a second CLI process skips AI sync), reads the `ai_logs_last_parsed_at` watermark and per-parser checkpoints, and runs every transcript parser over records newer than the watermark.
2. Each parser reads one harness's local logs and turns records into heartbeats with category `ai coding`. There are about 50 parsers, listed in `parseAIHeartbeats`: Claude Code, Codex, Continue, Cody, Roo Code, OpenCode, GitHub Copilot Chat, Cursor, Windsurf, Qoder, Kiro, Cline and Cline CLI, Gemini, Qwen Code, Pi, Goose, Amp, Grok Build, Codebuff, CodeWhale, Crush, Cursor Agent, Devin, Droid, Forge, Hermes, IBM Bob, Kilo Code, Kimi, Kimi Code, LingTai TUI, Mistral Vibe, Mux, OMP, OpenClaw, Open Design, Quick Desktop, Warp, ZCode, Zed, Zerostack, DeepSeek, CodeBuddy Code, OpenClaude and Grok Bot. Some read JSONL transcripts with checkpoints; others read the harness's SQLite database with their own cursors (`sqliteConfig`).
3. The Claude Code parser (`pkg/ai/claude.go`) walks `<config dir>/projects/**/*.jsonl` for each Claude config directory. For each transcript line it emits:
   - an `app` heartbeat when the line is a real user prompt (not a side chain, and not only `<tag>...</tag>` wrappers such as slash-command markup), with `ai_prompt_length` set to the prompt's length;
   - a `file` heartbeat for each file a tool result touched, with `ai_line_changes` from the structured patch, `is_write: true`, and the file path as the entity;
   - token deltas (`ai_input_tokens`, `ai_output_tokens`, `ai_cached_input_tokens`) on the first heartbeat of the line, and `ai_session` from the transcript file name.
   Every heartbeat's `time` is the transcript record's timestamp, and the model goes into the user agent string, not a field (`userAgent()` builds a token from model and "complexity").
4. `replaceAppHeartbeats` rewrites each session's `app` heartbeats onto the file that session last edited, so prompts and edits land on one entity and project.
5. Then it merges the AI heartbeats with the editor heartbeats the plugin just sent:
   - An editor heartbeat on the same file within 5 seconds of an AI heartbeat (`sameEntityAIHeartbeatWithinWindow(h, entities, 5)`) is dropped as a duplicate, unless it comes after the first heartbeat with human line changes.
   - An editor heartbeat within 2 minutes of the AI activity window and with no `human_line_changes` is relabelled `ai coding`.
   - An editor heartbeat within 30 minutes of the AI window is also relabelled `ai coding` if it comes before the first human edit in the batch.
   - Everything else stays as the plugin sent it (`coding`, or `writing tests`/`writing docs` from `DetectCategory`).

What this means for counting:

- `ai coding` time is agent activity time. Transcript records are written while the agent works, so a 40-minute autonomous run with a tool call every minute becomes 40 minutes of heartbeats at 1-minute spacing, which Hackatime counts as 40 minutes. Nothing in the pipeline asks whether a person was present. This is the behaviour Zach's vision post objects to ("It just counts how active your AI agent is", BOUNTY.md).
- Human reading and reviewing near agent activity gets folded into `ai coding` (the 2- and 30-minute rules), so the split between AI and human time is not a measure of human effort either.
- The only per-prompt human signal that leaves the machine is `ai_prompt_length`, a character count. Prompt text stays local.

### 2.3 Desktop app and AI apps

WakaTime has two desktop trackers, both BSD-3-Clause: [wakatime/macos-wakatime](https://github.com/wakatime/macos-wakatime) (Swift, `8c70917f8469`, 2026-07-22) and [wakatime/desktop-wakatime](https://github.com/wakatime/desktop-wakatime) (TypeScript/Electron for Windows and Linux, `2d27faef3e63`, 2026-05-21). [wakatime.com/desktop](https://wakatime.com/desktop) calls the app "a catch-all if we don't have a plugin for your IDE or app."

The macOS app tracks only a fixed allow-list of bundle ids (`WakaTime/Watchers/MonitoredApp.swift`): Adobe apps, Arc, Brave, Canva, Chrome, Figma, Firefox, GitHub Desktop, iMessage, Inkscape, iTerm2, Linear, Miro, Notes, Notion, Postman, Rocket.Chat, Safari, Slack, TablePlus, Terminal, Warp, WeCom, WhatsApp, Xcode, Zed and Zoom. It reads window titles and browser URLs through the Accessibility API. Categories are fixed per app in `MonitoringManager.category(for:)` (designing, communicating, meeting, browsing, coding), and a browser URL matching a GitHub, GitLab or Bitbucket pull request becomes `code reviewing` (`category(from:)`).

No ChatGPT, Claude or Codex desktop app is on that list. WakaTime's AI tracking comes from wakatime-cli reading local transcripts and databases (section 2.2). [wakatime.com/llms.txt](https://wakatime.com/llms.txt) names Claude Code, the Claude desktop apps and Claude in editors, plus Codex, Continue, Cody, Roo Code, OpenCode, Copilot, Cursor, Windsurf, Qoder, Kiro, Cline, Gemini, Pi and Goose (from the raw notes' fetch summary). The same page says that capturing data on the developer's machine means "it knows whether code was AI generated or Human written".

### 2.4 Why people call it inaccurate

The bounty thread says WakaTime's AI tracking "is widely seen as poor" (BOUNTY.md). The public issue tracker shows errors in both directions:

| Issue | What it reports |
|---|---|
| [vscode-wakatime#440](https://github.com/wakatime/vscode-wakatime/issues/440) (2025-07-18, closed, 27 comments) | "it says I have spent 10 minutes AI coding when I haven't used AI at all today" |
| [vscode-wakatime#474](https://github.com/wakatime/vscode-wakatime/issues/474) (2026-03-03, closed) | A fully AI-written commit (`10 files changed +939 -11`) shows on the dashboard as "Human: +456 -6", "AI additions: 15" |
| [vscode-wakatime#495](https://github.com/wakatime/vscode-wakatime/issues/495) (2026-09-08, open) | "`ai coding` latches on and cannot clear while you type (30.2.1)". Over two weeks, 98.3% of time was labelled `ai coding` against an honest 86.1%, and 12 of 14 heartbeats with `human_line_changes > 0` were still labelled `ai coding`. The reporter traces it to the release path in `onChangeTextDocument` |
| #464, #476, #483, #484, #486, #482 (2025-11 to 2026-06) | Cursor's AI edits counted as human, Cursor time attributed to VS Code, Codex window time counted as Cursor editor time, AI activity barely tracked over Remote SSH |

Why this happens, from the code:

1. The editor plugin guesses from the shape of edits (section 1.5). A single change of more than 2 newlines or 50 characters (`Utils.isPossibleAICodeInsert` in `src/utils.ts`), repeated within `AI_RECENT_PASTES_TIME_MS = 500` ms, flips `isAICodeGenerating` on, but only if a known AI extension is installed (`hasAICapabilities`). Edits in an AI chat sidebar also flip it on. Turning it off takes more than one single-character insert or deletion (`isPossibleHumanCodeInsert`) inside a `AIdebounceMs = 1000` ms debounce, or a tab switch; other edit kinds reset the countdown (#495). Autocomplete, snippets, formatters, paste and multi-cursor edits all look like AI. AI edits applied through the file system (an agent writing files) don't show up as editor inserts at all.
2. Line counting uses `document.lineCount` deltas, and any jump above 50 lines within 60 s is zeroed "to prevent counting large copy/paste as human typed lines" (`updateLineNumbers` in `src/wakatime.ts`). Large AI diffs therefore vanish from both counts, which matches #474.
3. The CLI's AI sync relabels editor heartbeats within 2 minutes (or 30 minutes before the first human edit) of agent activity as `ai coding` (section 2.2). A person reviewing the agent's output gets counted as the agent.
4. `ai coding` time is the spacing of agent transcript records, so it measures how long the agent was busy, not how long a person was engaged.
5. For time itself, the 15-minute default timeout credits up to 15 minutes of silence between any two heartbeats. That over-counts idle time; Hackatime's 2-minute cap under-counts reading and thinking.

The underlying problem is that none of these signals separates the person from the agent. Edit-shape heuristics break whenever tools change how they insert text, and transcript timestamps measure the agent. Pairtime needs human-side evidence: input events, focus, prompt submissions and review actions, tied to the harness's own events (R2, R7).

## 3. The Hack Club YSWS pipeline

### 3.1 The Unified YSWS Projects DB

The Unified YSWS Projects DB is an Airtable base, `unified_ysws_projects_db` (`app3A5kJwYqxMLOgh`). Its main table is `approved_projects` (`tblzWWGUYHVH7Zyqf`). IDs come from [hackclub/data-warehouse](https://github.com/hackclub/data-warehouse) `orpheus_engine/defs/airtable/generated_ids.py` at `4ff995dddcb7`, and Stardance uses the same IDs (`app/services/certification/unified_ysws_service.rb`). The data warehouse is a Dagster project ("Owned by @24c02. Please talk to it before editing.") that copies the base into Postgres (`airtable_unified_ysws_projects_db` schema), backs it up daily as Parquet, and enriches rows: geocoding, archiving code and live URLs, GitHub star counts, and searches for mentions of the project online (`orpheus_engine/defs/unified_ysws_db/definitions.py`).

Programs never write to the unified base directly. Each program keeps its own submission table (Stardance's is "YSWS Project Submission", `app/services/certification/ysws_airtable.rb`), and Hack Club automation copies approved rows into `approved_projects`. Stardance's comment: "The unified base is fed by each program's own submission table, so the hours column can carry either name depending on which automation wrote the row." Once a row is in the unified base, Stardance's sync job won't rewrite it; "that record is corrected by hand" (`app/models/certification/integrity.rb`).

`approved_projects` fields that matter for time tracking:

| Field | Meaning |
|---|---|
| `Hours Spent` | Hours from the program |
| `Override Hours Spent` | Reviewer-adjusted hours; programs send it as `Optional - Override Hours Spent` |
| `Override Hours Spent Justification` | Free text explaining the hours (Stardance's template is in 3.3) |
| `YSWS–Weighted Project Contribution` | Airtable formula; hours / 10 (section 3.2) |
| `YSWS–Weighted Project Author Attribution`, `...Count`, `...Per Author` | Splits a project's weight across co-authors |
| `YSWS` (link), `YSWS–Name`, `YSWS–Default Hours Per Grant`, `YSWS–Current Owners` | The program the row belongs to |
| `Approved At`, `Age When Approved`, `Birthday` | Eligibility (spot checks look for under-19 at approval) |
| `Code URL`, `Playable URL`, `Screenshot`, `Description`, `GitHub Username` | The shipped project |
| `Email`, `First Name`, `Last Name`, address fields | Identity; ties spend to a person |
| `Override Duplicate Justification`, `Override Age Justification` | Exceptions |
| `Archive - *`, `Repo - *`, `YSWS Project Mentions - *`, `Geocoded - *` | Added by the warehouse |

Related tables in the same base: `ysws_programs` (`Weighted–Total`, `Weighted Projects This Month`, `Weighted Projects–2026`, `Average weight`, `Average Hours Per Grant`, `Cost Per Hour`, `Budget per hour`, `Start Date`, `End Date`, `Review Channel ID`, and more), `ysws_authors`, `ysws_spot_checks` and `ysws_spotchecks` (first, second and third review with `needs_nonzero_hours`, `needs_public_repo_url`, `needs_playable_url`, `is_duplicated`, `needs_under_19_birthday`, `ai_checked`, `ai_score`), `eliminated_projects` and `project_fines`. Spot checks mean Hack Club HQ re-reviews a sample of approved rows after the program has approved them.

None of the `approved_projects` fields names a time tracker. The unified DB holds an hour number and a justification string, and the program owns that number. The tracker is named one step earlier, in the program's submission table. Quality and Integrity's handbook defines structured justification fields, among them `Justification - Alternate Tracking Method` for "something other than Hackatime or Lapse", which an automation folds into the unified justification text (section 3.7). So a project counts toward the bounty when a program puts it in this table with hours that came from pairtime, and the evidence for that is the `Alternate Tracking Method` text inside `Override Hours Spent Justification` (see Open questions).

### 3.2 How a weighted project is calculated

"A weighted project is 10 hours of shipped work" ([readme.hackclub.com/nutshells](https://readme.hackclub.com/nutshells), fetched 2026-11-19). A Hack Club post from 2026-03-04 adds "A project with 20 hours counts as 2 weighted projects, 30 hours as 3 weighted projects" ([notes.deven.dev](https://notes.deven.dev/posts/the-plan-for-hack-club-world-dominance)). The warehouse code agrees: `bin/ysws_true_expense.py` says "weighted hours == SUM(weighted_project_contribution)*10", and the dbt model `orpheus_engine_dbt/models/hcb_ysws_true_spend_analytics/ysws_spend_by_program.sql` says "A weighted project is defined as 10 weighted hours." The formula itself is an Airtable formula field and isn't in any repo, so I couldn't check whether it uses override hours ahead of `Hours Spent`, rounds, or caps (unverified). The one capped weight in code is `weighted_referral_count = sum(min(user_hours, 10)) / 10` per user and program (`orpheus_engine/defs/ysws_programs_sync/definitions.py`), and that is a different metric.

Not every program tracks time at all. The warehouse runbook `runbooks/ysws-dau.md` (at `4ff995dddcb7`) says programs "whose only record is a submission form (repo URL + self-reported hours)" are common: "Most low-ceremony Airtable-form YSWS land here, and they can rank high by weighted projects." Its ground rule 7 says hours analytics must come "from a system Hack Club operated that recorded activity *live, while users were building*", and that a repo plus self-reported `hours_spent` "is NOT an activity system". I couldn't measure what share of weighted projects comes from self-reported programs without warehouse access. But for those programs any live tracker is an upgrade, and their bar is the handbook's "self-reported by user" example under `Alternate Tracking Method` (3.7).

So 1,000 weighted projects means about 10,000 approved hours in `approved_projects`, as Zach confirmed in the thread (BOUNTY.md). After review those hours are counted at Stardance's approval rate (27,600 of 123,400, about 22%). At that rate, getting 10,000 approved hours could take on the order of 45,000 submitted hours unless pairtime's hours are easier to approve. A tracker whose raw number is already close to what a reviewer would approve has a direct advantage here (R2, R9).

Money: the same post says programs receive "85 USD to the program budget (8.5 USD per hour spent building with good practice)" per weighted project, and online programs are "encouraged to spend 50 USD per weighted project (or 5.00 USD per hour)". The nutshells page gives "one hour of shipped work is $5 for most YSWSs". Every approved hour is money, which is why programs review so hard.

### 3.3 How programs reviewed and approved hours

#### Stardance (summer 2026)

Source: [hackclub/stardance](https://github.com/hackclub/stardance) at `b1d3e0eac1ae`. Stardance is a Rails app deployed on Coolify (`README.md`). Its admin "hour funnel" (`app/services/admin/hour_funnel.rb`) lists every stage an hour passes through, which makes it the clearest description of the pipeline:

1. Devlogged. A participant links Hackatime projects (`hackatime_keys`) to a Stardance project and posts devlogs. Each devlog claims the Hackatime time since the previous one.
2. Shipped, then ship certification (`Certification::Ship`): a reviewer checks the project works. States include approved, pending, returned to the builder, resubmitted, misfiled, withdrawn and rejected.
3. YSWS review (`Certification::Ysws`, the "GOI" review in comments): a reviewer approves, marks down or rejects each devlog's minutes (`devlog_reviews.approved_minutes` against `original_minutes`, each with a justification). A review under `MIN_APPROVED_MINUTES` is rejected; the funnel labels it "Review rejected (under 6 min)".
4. Integrity check (`Certification::Integrity`, software only; hardware skips it). Status `auto_passed`, `pending`, `manually_passed`, `deducted` (with `deduction_minutes`) or `banned`. Automatic checks fill `flags` (bits: `unknown_file`, `cursor_strange`, `neuralnet`, `no_hackatime_user`, `check_failed`, `entropy_anomaly`) and `fraud_detection_data` (JSON, signal names codenamed). Reviewers claim a check for 20 minutes (`CLAIM_TTL`). The review page draws a treemap of heartbeat share by file. A ban cascades to the project's other pending checks and rejects its YSWS queue. A deduction claws back the currency ("stardust") paid for those hours.
5. Airtable sync (`Certification::YswsAirtableSyncJob`) into Stardance's own "YSWS Project Submission" table, from which Hack Club's automation fills the Unified DB (section 3.1).
6. Payout cap: "Within 10h/devlog payout cap" (`Post::ShipEvent::MAX_PAYOUT_HOURS_PER_DEVLOG`).

Hours sent to Airtable are `(sum of approved devlog minutes - integrity deduction minutes) / 60`, rounded to 2 decimals, in `Optional - Override Hours Spent`. The justification text is generated, and it records the raw Hackatime figure next to the approved one:

```text
The user logged <original> on hackatime. (This was adjusted to <approved> after review.)
<commit activity sentence>
Further deducted by <h> hours by the fraud department for hour fraud. Reason: <...>
In this time they wrote <n> devlogs. Of which <a> were approved and <r> rejected.
This project was initially ship certified by <name>.
Following this it was YSWS reviewed by <name>
devlog <id>: <minutes> min <status>: "<reviewer note>"
...
Passed automatic heartbeat checks. | Hours deducted during manual review. | ...
The Stardance project can be found at https://stardance.hackclub.com/projects/<id>
The Full YSWS Review + devlogs are at https://stardance.hackclub.com/admin/certification/review/<id>
```

Other fields Stardance writes per submission (`build_airtable_fields`): `review_id`, `ship_event_id`, `ship_cert_id`, `user_slack_id`, `Email`, `First Name`, `Last Name`, `Birthday`, address fields, `project_name`, `ai_declaration`, `project_update_description`, `Code URL`, `Playable URL`, `readme_url`, `Description`, `Screenshot`, `reviewer`, `ship_certifier`, `reviewed_at`, `ship_certed_at`, `rejection_reason`, `rejected_at`, `ship_start`, `ship_end`, `report_status`, `integrity_id`, `integrity_status`, `integrity_flags`, `fraud_data`, `flagged_double_dipped`, `hackatime_uid` and `hackatime_keys`.

#### Earlier programs

| Program | Where hours came from | How they were checked | Source |
|---|---|---|---|
| High Seas (winter 2024-25, "the first event with Hackatime") | A "ship" listed Hackatime project names. `credited_hours` = the sum of those projects' Hackatime totals minus the previous ship's `total_hours`, with a 1-hour minimum ("Projects must be at least one hour") | No per-hour human review in this code; payouts came from peer voting. Zach: "most hours submitted were valid" | [hackclub/high-seas](https://github.com/hackclub/high-seas) `src/app/harbor/shipyard/ship-utils.ts` (default branch, 2026-11-19) |
| Summer of Making (summer 2025) | Each devlog pulls `GET https://hackatime.hackclub.com/api/v1/users/<slack_id>/stats?filter_by_project=<keys>&start_date=<previous devlog>&end_date=<this devlog>&features=projects&total_seconds=true&test_param=true` into `devlogs.duration_seconds` | Devlogs created after 2025-07-19 are capped at 36,000 s (10 h) (`capped_duration_seconds`). Ship certification by "Shipwrights" and an admin YSWS review (`app/controllers/admin/ship_certifications_controller.rb`, `app/controllers/admin/ysws_reviews_controller.rb`). Stardance inherited this design | [hackclub/summer-of-making](https://github.com/hackclub/summer-of-making) `app/models/devlog.rb` |
| Shipwrecked (Aug 2025, in-person, 130 attendees) | Per linked Hackatime project, `HackatimeProjectLink.rawHours` with an optional `hoursOverride` ("Override for the Hackatime reported hours"); effective hours prefer the override | A `Review` model with a "Justification for approved hours (from checklist)", plus shipped and viral approvals and project flags | [hackclub/shipwrecked](https://github.com/hackclub/shipwrecked) `prisma/schema.prisma`, `lib/hackatime-links.ts` |

Flavortown, running now, uses a third review stack. [hackclub/flavortown](https://github.com/hackclub/flavortown) at `371026446d1b` pulls finished reviews from `https://review.hackclub.com` (`YswsReviewService::BASE_URL` in `app/services/ysws_review_service.rb`). Each devlog there carries `origSecs` (Hackatime) and `approvedMins`, and the review is by "the Guardian of Integrity" (`app/jobs/ysws_review_sync_job.rb`, `build_justification`). The sync job also deduplicates across programs by Code URL. If the project is already in the Unified DB under another program, it submits only `new_hours - unified_hours`, and only when the increase is more than 0.5 h. So hours on a project that some other program already paid for count only as the increase, whichever tracker measured them.

Over three programs the pattern hardens. High Seas paid out raw Hackatime hours. Summer of Making put a reviewer and a 10-hour-per-devlog cap between Hackatime and the payout. Stardance added an integrity team with automated flags and deductions on top. Hackatime is never the final authority: every program stores a raw number and a reviewer-approved number, and the unified DB stores the approved one with a justification.

This is where BOUNTY.md R9's numbers come from: 123,400 Hackatime hours submitted to Stardance, 27,600 approved so far. Every hour went through a person who decided how much of the raw Hackatime figure to keep. The tracker supplies the starting number and the evidence; the program's reviewers decide the hours. A tracker that makes step 3 faster and more consistent is what program authors would value.

### 3.4 What a new tracker must provide

Program apps are mostly Rails apps cut from the same pattern (Summer of Making, then Stardance and Flavortown), each with a `HackatimeService` and often a `LapseService` or `LookoutService`. A program author will compare pairtime against what those services already do. From the code above, the list of what a tracker has to offer:

| Need | What programs use today | Evidence |
|---|---|---|
| Account linking | OAuth 2 (Doorkeeper) plus `GET /api/v1/authenticated/me`; an admin email lookup for users who signed up elsewhere | Stardance `HackatimeService.fetch_authenticated_user`, `lookup_user_id_by_email` |
| Project picker | A list of the user's tracker projects, linked to the program's project (`hackatime_keys`); Stardance's Lookout panel needed a token-scoped variant | Stardance `User::HackatimeProject`; Lapse `docs/lookout-desktop.md` |
| Hours query | Seconds for a set of project keys between two timestamps, deterministic and re-pullable, because each devlog re-pulls its own window | `stats?total_seconds=true&filter_by_project[]=...&start_date&end_date&test_param=true` in Summer of Making, Stardance and Flavortown |
| Trust signal | Per-user trust (`blue`/`green`/`yellow`/`red`), with a batch endpoint for up to 2,000 users; `red` blocks payouts | `trust_factor`, `user/info_batch`; Lapse gates browser recordings on it |
| Review evidence | Spans, raw heartbeats, a timeline, commit markers; for recordings: playback URL, snapshot timestamps, capture activity | Hackatime admin API (1.9), Lapse `detection.ts`, Lookout internal endpoints |
| Machine-readable review result | Something to put in `Optional - Override Hours Spent` and its justification, plus ids that let HQ trace the row back (`hackatime_uid`, `hackatime_keys`, `integrity_status`, `integrity_flags`, `fraud_data`) | Stardance `YswsAirtableSyncJob#build_airtable_fields` |
| Write path for programs | Pushing heartbeats on a user's behalf (project seeds, Lookout forwarding) | Stardance `HackatimeService.push_heartbeats`, `LookoutHeartbeatForwarder` |
| Program onboarding | Program API keys; Lookout asks programs to email lookout@hackclub.com first; Lapse registers OAuth apps through an admin | Lookout `README.md`; Lapse `API.md` |
| Privacy and deletion | Endpoints for Hack Club's privacy hub (`yeet.hackclub.com`): ES256 JWT, `iss: hackclub-privacy`, dry run first, single-use `jti` | Lapse `docs/privacy.md`; Hackatime `app/controllers/api/privacy_controller.rb` and `app/services/privacy/` |
| Fraud verdict | Robert scores each shipped project 1 to 10 from `kind: "hackatime"` plus project names; 1 to 4 is a final rejection | Section 3.5 |
| Reviewer cuts per evidence row | Ari settles hours row by row (Hackatime per person, commits, devlogs, clips, program time) and credits `ai coding` at one third | Section 3.6 |
| Handbook-shaped justification | Structured fields (`Hackatime Project Name(s) and Date Range(s)`, `Submitter Hackatime ID`, `Lapse Link(s)`, `Alternate Tracking Method`, ...) that an automation folds into the Unified DB | Section 3.7 |
| Evidence that survives spot-checks | Spot-checkers must be able to locate the evidence, or the project is removed and the program fined | Section 3.7 |
| Guidance from HQ | "We will work with Quality and Integrity to provide good guidance around how to use new time tracking tools" | BOUNTY.md, Zach in thread |

Two points follow. First, programs want a smaller, more defensible number with evidence attached, not a bigger one. A tracker that hands reviewers a pre-deflated figure, plus the reasons and the evidence, saves them the work that cut Stardance's 123,400 hours to 27,600. Second, the integration cost has to be close to zero. A program author already has a `HackatimeService`. A pairtime client that answers the same calls, or a pairtime figure that shows up inside Hackatime, gets adopted. One that needs a new review workflow probably doesn't, unless HQ requires it.

### 3.5 Telescreen and Robert: the shared fraud review desk

Telescreen (`https://telescreen.hackclub.com`) is Hack Club's shared review workbench. Its pages sit behind Hack Club Auth ("Sign in with your Hack Club account to continue", fetched 2026-11-19), and I found no public source repo (`gh search repos telescreen --owner hackclub` returns nothing), so everything below comes from the public program code that calls it. Its API docs are at `https://telescreen.hackclub.com/robert/docs`, also behind the login. GitHub code search for `telescreen.hackclub.com` (2026-11-19) finds references in 14 public Hack Club repos and one private one, among them hackclub/the-carnival, hackclub/pixl, hackclub/trmnt, hackclub/Alchemize, hackclub/savepoint, hackclub/halflife, hackclub/crossroads, hackclub/live, hackclub/shrink, hackclub/fellow-ysws and hackclub/stardance.

It replaced the older fraud desk. Stardance's `app/helpers/telescreen_helper.rb` rewrites old `joe.fraud.hackclub.com/profile/<id>` and `/case/<n>` links to Telescreen's `/subjects/<id>` and `/joe/cases/<n>`. Pixl's client says "Joe's old equivalent (lib/joe.ts) is retired" and "manual fraud triage is retired regardless of whether Robert is configured yet" (`apps/internal-dashboard/lib/robert.ts`).

Reviewer deep links (Stardance `telescreen_helper.rb`, savepoint `app/models/telescreen.rb`, halflife `src/lib/review-platform/telescreen.ts`):

| URL | Opens |
|---|---|
| `/subjects/<slack id or numeric Hackatime id>` | A person's subject page ("review cockpit") |
| `/workbench/hackatime/overview?u=<hackatime uid>&p=<key1,key2>` | Hackatime evidence for the listed project keys |
| `/workbench/hackatime?slack=<SLACK_ID>` | The same, looked up by Slack id |
| `/workbench/lapse?u=<hackatime uid>&t=<lapse id>` | One Lapse timelapse |
| `/joe/cases/<n>` | Cases migrated from Joe |

Every workbench is keyed on the Hackatime user id. Hackatime and Lapse are the evidence sources Telescreen knows about.

**Robert** is "Telescreen's review desk for YSWS submissions" (hackclub/trmnt `src/src/lib/server/robert.ts`). A program sends each shipped project to Robert, a fraud reviewer scores it, and the program records its own final decision back. The API, reconstructed from four clients: hackclub/the-carnival `src/lib/telescreen/client.ts` and `shared.ts`, hackclub/pixl `apps/internal-dashboard/lib/robert.ts`, hackclub/trmnt `src/src/lib/server/robert.ts`, and hackclub/Alchemize `src/lib/server/telescreenClient.ts`, all at their default branch on 2026-11-19:

| Method and path | Body | Returns |
|---|---|---|
| `POST /api/v1/ysws/projects` | `name`, `kind` (`"hackatime"` or `"hardware"`), `codeUrl`, `demoUrl?`, `submitter: {slackId?, hackatimeId?}`, `hackatimeProjects: string[]` (Alchemize caps it at 50 distinct names), `journal?: [{title, content, loggedAt, hours?}]` (hardware), `externalId` | `{id, state, created}`. Resending the same `externalId` returns the same project, so retries are idempotent |
| `GET /api/v1/ysws/projects/:id` | | `{id, externalId, state, trustScore, creditedSeconds, review: {trustScore, note, at}, outcome: {status, reason, at}, submittedAt}`. trmnt reads it wrapped as `{project: ...}`; the other clients read it bare |
| `GET /api/v1/ysws/projects?state=&limit=&offset=` | | `{total, projects[]}`, newest submission first |
| `POST /api/v1/ysws/projects/:id/outcome` | `{status: "approved" \| "rejected", reason?}` | Only while `awaiting_outcome`; `409` once an outcome is final |
| `DELETE /api/v1/ysws/projects/:id` | `{signedOffBy}` | Withdraws a submission; refused with `409` once a reviewer has scored it |
| `PATCH /api/v1/ysws/event` | `{webhookUrl}` | `{webhookUrl, secret}` |

- Auth is `Authorization: Bearer <key>`. "The API key belongs to exactly one Robert event, so no path names an event" (the-carnival). Alchemize treats a key that doesn't start with `ysws_` as missing, so event keys apparently carry that prefix.
- States: `awaiting_review`, then `awaiting_outcome` (scored above 4, waiting for the program), `rejected_fraud` (scored 4 or below, final) or `decided`.
- Score bands (the-carnival `shared.ts`): "1–4 Fraud: final rejection", "5–7 Doubtful: usually a deduction; the granting admin must acknowledge it", "8–10 Clean".
- `creditedSeconds` is Robert's own figure. Alchemize sends "the Hackatime project names Robert needs to credit time" and nothing else about hours, so Robert computes credited time from Hackatime itself.
- Webhooks: `X-Robert-Signature` is `sha256=` plus the hex HMAC-SHA256 of `<X-Robert-Timestamp>.<raw body>` under the event's secret, and receivers reject timestamps more than 300 s off (trmnt, Alchemize). Pixl notes that the webhook for an outcome decided in Robert's own UI "carries no review note (only outcome.reason)", so it also polls the list endpoint.
- Data minimisation is the norm: Alchemize sends "Never the owner's email, name, address, birthdate or Unified ID."

What this means for pairtime. Robert is where Fraud Squad's verdict now enters a program, and its only time-bearing input is `kind: "hackatime"` with Hackatime project names. A program that adopts pairtime still needs a fraud verdict from somewhere. Pairtime hours reach that review in one of two ways: as Hackatime projects (forwarding, Design implication 17), or through a new Robert `kind` with a pairtime evidence workbench, which only the Telescreen team can add. This is the most direct form of Zach's "We will work with Quality and Integrity to provide good guidance" (BOUNTY.md), and it belongs in the first conversation with HQ (Open questions).

### 3.6 Ari: the ship-review platform and its hours math

[hackclub/ari](https://github.com/hackclub/ari) (web app: SvelteKit, Prisma, Bun; no license in API metadata; commit `637233962a0f`, 2026-11-16) and [hackclub/ari-webhooks](https://github.com/hackclub/ari-webhooks) (Go ingestion and background service on the same database; `01a47d67aa91`, 2026-11-10) make up Hack Club's "ship-review platform". The README says "programs use them for verifying that a project submission follows the [YSWS Submission guidelines](https://hack.club/ysws-rules)". Robert (3.5) gives a fraud verdict. Ari is where a reviewer settles the hours, for the programs that use it: hackclub/3am-ysws, hackclub/hackxpansion, hackclub/disposition and hackclub/macondo have Ari client code (code search for `deflation_reason`, 2026-11-19).

It is open core. "There are private sections of the platform that are under a private repo, these contents are not to be shown to the public for containing fraud tooling or showing how certain flags work in specific" (ari `README.md`). One Dockerfile builds a public image, where `.dockerignore` keeps `private/` out, and a private image that gets `private/web` as a separate build context. Pairtime's AGPL server could use the same split for its detection rules (Design implication 15).

**Ingest.** Programs send signed ship webhooks to `POST /api/ingest/:program`, plus withdraw and status calls, and Ari sends signed outbound webhooks back with retries (ari-webhooks `README.md`). JSON keys accepted (`internal/ingest/validate.go`, `payload.go`): `external_id`, `maker` (`email`, `name`, `slack_id`, `hackatime_id`), `title`, `description`, `repo_url`, `demo_url`, `thumbnail_url`, `track` (`software` or `hardware`), `shipped_at`, `evidence[]`, `hackatime_projects[]`, `journals[]` (`at`/`date`, `seconds`/`minutes`/`hours`, `text`, `markdown`, `email`), `meta`, `is_update`, `update_message`, `collaborators[]`. A ship must carry at least one of Hackatime projects, journals or program hours (`hackatime_projects_or_journals_or_program_hours`). A program can also assert time directly with `program_seconds`, `program_minutes` or `program_hours` (bounded at 1,000 h), which the code calls "a program-asserted, evidence-free figure" (`parseProgramSeconds`).

**Time sources and cuts.** `src/lib/review/settlement.ts` knows four time sources: `hackatime`, `journals`, `lapse` and `program`. The reviewer settles a ship row by row. Rows are Hackatime time per person, per-commit `codingSeconds`, "Time after the last commit", each devlog entry, each timelapse clip (`lengthSeconds`), and program-added time. `clampSeconds` holds each settled row at or below its captured value, so a reviewer can only cut. Deductions decided outside the review (`deductionsSeconds`) are fixed. When tracked time is zero but commits exist, the ship settles from commits (`usesCommitFallback`). The review page also shows per-file hours (`ariw."submissionFileHours"`: `path`, `seconds`, `bytes`, `status`) and the maker's past approved projects with their `creditedSeconds` (`src/lib/server/review/loadExternal.ts`).

**Hackatime capture** (`internal/integrations/hackatime/spans.go`):

- Ari recomputes Hackatime's count itself from raw heartbeats: "sessions break on gaps > 120s; each session after the first carries the capped 120s re-entry gap. Sum of durations == Hackatime's official total." It cross-checks against `GET /api/v1/users/:id/projects/details` (the "oracle") and falls back to the public `heartbeats/spans` endpoint when the two disagree. A 403 there means the maker turned stats off, and Ari treats it as a privacy setting, not a failure.
- Ari credits `ai coding` time at one third. The constant is `aiCreditShare = 1.0 / 3`, commented "AI-assisted time is credited at one third", and the comment on `DiscountAiCoding` reads "an 8-hour AI session is still 8 hours of sitting there, it just is not credited like hand-written code." Ari stores the cut in `HoursBreakdown.aiDiscountedMinutes` (migration `00005`) and shows it to reviewers as `aiDiscountedSeconds`. Spans from the fallback endpoint carry no category, so Ari can't discount them.
- When a reviewer credits a Lapse clip on its own row, Ari drops the heartbeats Lapse synced for it (entity `<name> (<id>)`, type `timelapse` or category `timelapsing`) from the Hackatime figure, "so the same recording is not paid twice".
- ari-webhooks stores `internal/ingest.CurrentEvidenceVersion` with each submission. When "capture or hours math" changes, it re-ingests open submissions, and decided ones keep their settled numbers (migration `00012_integer_seconds.sql`). Design implication 8 asks pairtime for the same versioning.

Public flag kinds include `MARATHON_SESSION`, `HOURS_REUSE` and `IDLE_DEVLOG` (migration `00004_time_flag_checks.sql`) and a phantom-files flag (`00003`); the rest live in the private repo.

**The justification format is a Quality and Integrity standard.** `internal/outbound/justification.go`: "Override Hours Spent Justification (YSWS handbook, Quality and Integrity): the internal evidence record behind the hours a program credits. Field names and the comma-separated string shapes are the handbook's, not ours." The fields are:

| Field | Shape | Written by |
|---|---|---|
| `hackatime_projects` | `"<project> M/D/YYYY-M/D/YYYY, ..."` (UTC dates of the evidence window) | System |
| `hackatime_user_id` | Hackatime numeric id, "so a spot-checker can pull up the same heartbeats the reviewer read" | System |
| `lapse_links` | Comma-separated Lapse URLs | System |
| `technical_features` | Prose | Reviewer |
| `deflation_reason` | Prose | Reviewer |

Programs opt in per program (`Program.hoursJustification`); programs on "the legacy flow" get no block.

What this means for pairtime:

1. Three AI policies are already in production, and they disagree. Hackatime's leaderboard credits `ai coding` at 90% for three hours a day, then decays (1.4). Stardance counts it at full weight (`no_ai_coding=false`). Ari credits it at one third. Pairtime's credited time for AI-assisted work has to be easier to defend than Ari's flat third. A reviewer used to Ari will ask why pairtime credits more, and the answer has to be visible evidence of the human's part.
2. The handbook decides what evidence a spot-checker can follow. Today it names Hackatime and Lapse. For pairtime hours to be checkable after approval, the handbook needs a field such as `pairtime_links` (stable review URLs) that Quality and Integrity would have to add.
3. Ari already has the integration shape pairtime needs: a time source with rows a reviewer can cut. A `pairtime` source would list credited intervals (prompt-and-review cycles, test-and-fix loops) as rows, each with captured seconds and a link to evidence. Until Ari supports it, a program could pass pairtime's figure as `program_seconds`, but Ari treats that as evidence-free, which defeats the point.

### 3.7 Quality and Integrity's rules for hours (the YSWS handbook)

HQ's rules for what goes into the Unified DB are the [YSWS Project Submission Guidelines](https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/) (`https://hack.club/ysws-rules` redirects there). It is a GitBook maintained by `@lfd`, who calls it "a recompilation of the rules that have been in place ... not a cage", to be "revised and updated" when a program conflicts with a rule. I read the Markdown versions of its pages on 2026-11-19. Most pages say "Last updated: March 2026"; Project Exceptions says June 2026. Ari's code calls this "the YSWS handbook" (3.6).

**Hours policy** ([Override Hours Spent](https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/override-hours-spent.md)):

- The field "records the number of hours being approved", and the approved hours "should represent the *genuine effort* put into building the project."
- "Default to Deflation": "When a reviewer has doubts about the accuracy of the claimed hours, the correct response is to **deflate aggressively**. ... **When in doubt, deflate.** ... give them the benefit of the doubt on intent, but not on the number."
- Outright fraud ("heartbeats generated by a script rather than actual coding activity", plagiarism, "A commit history that was manufactured after the fact") is not deflated to zero. It never enters the database.
- On AI: "The key question is not 'how much of the code did AI write?' but rather 'how much genuine effort did the submitter invest?'" and "**AI slop is not eligible.** A project that was generated in a single prompt with no meaningful iteration, testing, or refinement is not a real project. If the submitter did not engage with the output (did not debug it, did not shape it, did not learn from it), then there is no genuine effort to reward. Reject these submissions outright." Programs "may define **stricter** AI usage criteria than the ones described here, but they may **not** be more relaxed."
- **The one-third rule.** "If the project is using Hackatime and some amount of time tracked is categorized as 'ai coding' by Wakatime, that time must be deflated **to at most ⅓ of its original amount** unless there is significant evidence proving that more time should be credited." The stated reason is that WakaTime "is incentivized to overtrack hours spent on AI coding due to business plan, so these projects are tracking the time that AI is working on a prompt as active coding time even though the user could be doing anything (and is likely not coding) while it's running." Worked example: 4 h of coding plus 3 h of `ai coding` gives "at most 5 hours of justifiable time". This is the policy that Ari's `aiCreditShare = 1.0 / 3` implements (3.6).
- Art and assets may count if part of the core project, but no more than 30% of approved hours. For an update, submit "only the hours tracked since the last update".

**Justification** ([Override Hours Spent Justification](https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/override-hours-spent-justification.md)). The standard: "someone who was not involved in the review should be able to read this justification, follow the links, and reach the same conclusion you did." The field is "**not public-facing**". Programs fill structured fields that an automation combines into the Unified DB's justification, or write one templated justification containing every element:

| Field | Include when | Notes from the handbook |
|---|---|---|
| Hackatime Project Name(s) and Date Range(s) | Using Hackatime | Comma-separated, e.g. `hackatime-project 7/20/2026-7/22/2026, hackatime-project-2 7/21/2026-7/23/2026`. "Can/should be automated" |
| Submitter Hackatime ID | Using Hackatime | Numeric id |
| Lapse Link(s) | Using Lapse | Comma-separated; for coding Lapses, how much was on-task and what deflation followed |
| Specific Technical Features | Always | Human-written; "realtime multiplayer, procedurally-generated worlds, cloud saving", not "React" |
| Deflation Justification | Hours were deflated | The deflated-to number and why |
| **Alternate Tracking Method** | "You are tracking time using something other than Hackatime or Lapse" | "a brief explanation of what method you used (ex. self-reported by user, base amount for project of this type, in-house YSWS-specific tracker) and why you are confident that the number of hours submitted ... are accurate according to your method." The explanation "can be automated"; subjective methods also need a human-written reason |
| Additional Justification | Suspicious characteristics, or extra links | e.g. "the project page on a custom YSWS review platform" |

These map one-to-one onto Airtable fields in program bases. The `wrangler` base (`appznGjUoC1o6jLZd`) table `ysws_project_submission` (`tblAv8vzzQClBi5xR`) has `Justification - Hackatime Project Name(s) + Date Range(s)`, `Justification - Submitter Hackatime ID`, `Justification - Lapse Links, comma-separated`, `Justification - Specific Technical Features`, `Justification - Deflation Justification`, `Justification - Alternate Tracking Method`, `Justification - Additional Justification`, `Automation - Unified Justification`, `Automation - Submit to Unified YSWS`, `Hours claimed`, `AI usage disclaimer` and `Linked lapse/lookout/hackatime links` (data-warehouse `orpheus_engine/defs/airtable/generated_ids.py` at `4ff995dddcb7`). The `snowglobe` base's `projects` table has Ari's versions: `ari_hackatime_justification`, `ari_hackatime_user_id`, `ari_lapse_links`, `ari_technical_features`, `ari_deflation_reason`, `ari_justification_extra`, `ari_approved_hours_paid`.

**Spot-checks** ([Spot-Checks](https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/spot-checks.md)). Verdicts are Accepted, "Needs Changes (fine issued)" and "Rejected (fine issued)". A project with either of the last two "is removed from the Unified Database". Grounds include "Submitted project does not match the reviewer justification (ex. hour mismatch ...)", a justification that "does not contain all required elements", and "Required evidence (ex. Hackatime projects) cannot be located using the information provided". The [shipped-project page](https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/what-makes-a-project-shipped.md) and the [required fields](https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/required-submission-fields.md) add that a Code URL must be public, with "multiple commits that reflect the development progress" ("a 20-hour project with only one commit does not follow the guidelines").

What this means for pairtime:

1. **There is already a legal path for a new tracker.** A program that tracks with pairtime fills "Alternate Tracking Method" with an automated explanation of pairtime's method and puts the pairtime review URL in "Additional Justification". No HQ change is needed to start. A dedicated field (like `Lapse Link(s)`) would come later, once Quality and Integrity trust the tool.
2. **The one-third rule is pairtime's opening.** Every Hackatime-tracked AI hour is capped at a third "unless there is significant evidence proving that more time should be credited." Pairtime's job is to be that evidence. It should report, for AI-assisted time, how much the human was present and engaged, so a reviewer can credit more than a third where the evidence supports it, and less where it doesn't. The fairness case runs both ways: a person who spent 3 of 3 hours steering and reviewing deserves more than 1 hour, and an agent left running overnight deserves none.
3. **"AI slop" has a definition pairtime can check:** a single prompt with "no meaningful iteration, testing, or refinement". The tracker can flag one-shot sessions (one prompt, no test runs, no manual edits, no follow-up prompts) before a reviewer sees them.
4. **Evidence links have to survive spot-checks.** If a spot-checker can't open the evidence, the project is removed and the program fined. Pairtime's review pages and uploaded evidence for submitted hours must stay reachable by HQ reviewers, signed in with HCA, for as long as spot-checks can happen. The handbook doesn't give that window (Open questions).
5. **Defaults favour under-counting.** "When in doubt, deflate" means pairtime should report a conservative credited number and show what it left out. The pitch to programs is fewer spot-check fines, not more hours.

## 4. Lapse and Lookout

### 4.1 Lapse

[hackclub/lapse](https://github.com/hackclub/lapse) (MIT, `LICENSE.md`; commit `202d0209b03e`) is "a place for Hack Clubbers to record and share timelapses", in beta. Its README calls it "a fancy WakaTime plugin" for timelapses. It's a pnpm TypeScript monorepo with a web client, a Fastify/oRPC server and a CPU-heavy ffmpeg `worker`, plus Postgres, Redis and S3, deployed with Docker on Coolify (`README.md`). Recording now goes through Lookout: the browser recorder is Lookout's Rust code compiled to WebAssembly (a git submodule), and the desktop path is the Lookout app (section 4.2).

What it records and sends:

- A recording is a sequence of snapshots. On publish, Lapse pushes every snapshot to the user's Hackatime account as a heartbeat (`syncTimelapseWithHackatime` in `apps/server/src/routers/timelapse.ts`): `entity: "<name> (<id>)"`, `category: "timelapsing"`, `type: "timelapse"`, `language: "Lapse"`, `user_agent: "wakatime/lapse (lapse) lapse/2.0.0 lapse/2.0.0"`, `project` set to the Hackatime project the user picked, in batches of 100.
- Lapse's own duration (`durationBySnapshots`) adds gaps only when they are 120 s or shorter and drops longer gaps entirely, while Hackatime caps them at 120 s. The same recording can therefore show slightly different totals in Lapse and in Hackatime.
- `timelapsing` is not `coding`, so this time never reaches Hackatime leaderboards (section 1.6), although program stats calls with `test_param=true` include it.
- Users that Hackatime has marked `red` may publish only recordings made with the Lookout desktop app, not browser recordings (`isRestrictedBrowserRecording` in `apps/server/src/lookoutPublish.ts`).

Encryption and privacy. The README says "All timelapses are encrypted before being published. That means that you (and *only* you!) can access them." The code is more modest:

- The browser's per-device 128-bit key (`apps/client/src/encryption.ts`) is now marked `@deprecated` ("Only used for legacy encrypted draft thumbnails. New timelapses use Lookout which handles encryption server-side").
- `packages/shared/src/encryption.ts` uses AES-CBC, though its doc comments say AES-128-GCM, so the ciphertext is not authenticated.
- The server decrypts drafts itself (`apps/server/src/encryption.ts`, PBKDF2 over a passkey with salts derived from a constant), and `docs/privacy.md` says queued encode jobs "carry" the draft's passkey.

So drafts are protected from casual access, but the server can read anything that gets published. Before publishing, the user can cut footage with an `editList` (`docs/custom-clients.md`). GDPR deletion and export go through Hack Club's privacy hub (`yeet.hackclub.com`, ES256 JWTs, dry run first), and heartbeats already sent to Hackatime are left for the hub's Hackatime step (`docs/privacy.md`).

Reviewer evidence: `packages/api/src/contracts/detection.ts` defines a lookup that returns, per timelapse, the recording (`duration`, `playbackUrl`, `snapshotTimestamps`, `hackatimeProject`), the owner's `hackatimeId`, provenance (`lookout` or `legacy`), and Lookout evidence (`trackingMode`, `trackedSeconds`, `activeSeconds`, `screenshotCount`, `captureRange`, `captureTimestamps`, `clientInfo`). This is the shape fraud reviewers already get from a timelapse tool.

Passive or not: Zach says Lapse "is not passive. You need to turn it on to start tracking" (BOUNTY.md). I found no "lookback" mode. GitHub code search for `lookback` in hackclub/lapse and hackclub/lookout returns nothing, and neither do issue and PR searches in either repo (2026-11-19); the only Hack Club hits are Hackatime's alt-detection lookback window. The term exists only in Zach's reply. [hackclub/elapsed](https://github.com/hackclub/elapsed) (MIT, .NET/Uno, last pushed 2026-07-01) is a native third-party client for the Lapse API, not a new recorder.

### 4.2 Lookout

[hackclub/lookout](https://github.com/hackclub/lookout) (AGPL-3.0, `LICENCE`; commit `be1b7b9230f2`; desktop release v0.4.2 of 2026-11-10 for Windows x64/arm64, macOS Intel/ARM, Linux deb/rpm/pacman) is "Simple time-tracking via periodic screenshots as a service, designed to be embedded into Hack Club programs", formerly "Collapse". It has a Rust capture engine (`lookout-session` for pure logic, `lookout-protocol` for the wire protocol, `lookout-core` for capture, hardware H.264 clips through VideoToolbox, Media Foundation or GStreamer, and redaction), a Tauri desktop shell, a React SDK, a hosted web recorder, and a TypeScript server with Postgres (drizzle) and Cloudflare R2 storage (`README.md`, `crates/lookout-core/README.md`, `docs/integration.md`).

How it works (`docs/integration.md`):

- There are no user accounts. A program's backend creates a session with its API key (`POST /api/internal/...`), passes the session token to the client (a `lookout://session?token=` deep link, the React SDK, or the hosted recorder), and later reads the results with the same token. Programs integrate by emailing lookout@hackclub.com first.
- The client records one roughly 60-second clip per minute holding about 6 frames taken 10 s apart, and falls back to one JPEG per minute. Clips are capped at 8 MB per minute server-side; a typical screen is about 1.1 MB per minute.
- `trackedSeconds` is decided by the server. In credit mode, "each capture that arrives within ±30s of the streak-anchored expected mark credits 60s; out-of-window captures reset the streak." The last capture before a pause credits the exact 0 to 60 s since the last mark. The server stamps its own time when the client asks for an upload URL and checks with `HeadObject` that the upload exists, so "clients can't fake credits".
- Sessions auto-pause after 10 minutes without uploads and auto-stop after 24 hours. Limits: 10 upload-URL and 20 confirm requests per minute, at most 720 confirmed captures (12 hours) and 4,320 upload-URL requests per session.
- Native clients also send interaction metadata with each capture (`activity`, `packages/server/API.md`): pointer position as fractions of the display, the frontmost app's id, whether the focused window is being recorded, window frames and display layout. "Window titles are never sent." It is "Internal only, like the JA4 fingerprint": never returned on token endpoints, because "echoing the data back would let a forger tune it". The docs call it "a signal for spotting lazy cheating (a pointer that never moves, coordinates repeating exactly, a recorded window that is never in front), not proof."
- Retention: "The original screenshot images are only retained for 7 days after a session stops", while timestamps, the compiled video and the thumbnail are kept. Cut footage is deleted as soon as the edited timelapse is ready.
- Hackatime: the program fetches `/timings` (one timestamp per credited minute) and pushes them to Hackatime with editor `Lookout`. Stardance's `LookoutHeartbeatForwarder` sends `type: "file"`, `entity: <session token>`, `language: "Lookout"`, `category: "coding"`, `editor: "Lookout"`, so Lookout time counts as `coding` everywhere, leaderboards included. Lapse uses `timelapsing` for the same kind of data, so already two conventions exist.

Rough cost check (my arithmetic, not from the docs): 1.1 MB per minute is about 66 MB per recorded hour. Recording all 109,759 monthly Hackatime hours (R11) would mean about 7.2 TB uploaded per month. R2 doesn't charge for egress, but storing and compiling that much is a real cost. Uploading only submitted hours, as R10 suggests, is what makes screen evidence affordable.

### 4.3 Lessons for pairtime

How reviewers use timelapses today, pulling together sections 3.5 to 3.7. The handbook asks for each coding Lapse "a short explanation/justification of how much time in the Lapse was on-task and what deflation was applied as a result". Ari lists each clip as its own row (`lengthSeconds`) that a reviewer can cut, and drops the Hackatime heartbeats Lapse synced for that clip so the same recording isn't paid twice. Telescreen opens a single timelapse at `/workbench/lapse?u=<hackatime uid>&t=<lapse id>`. So a recording is reviewed by watching for on-task time and cutting the rest by hand, which is slow at the scale of R9's 123,400 hours.

1. Timelapses are the evidence Hack Club already trusts. The fraud page asks for one in appeals, Stardance integrates Lookout, and reviewers have tools for it. A pairtime submission that can attach a Lookout-compatible capture for the claimed hours plugs into existing review habits (R9, R10, A).
2. Neither tool is passive. Lookout sessions are created by a program and Lapse has to be started. A local rolling capture that keeps evidence on disk and uploads only the minutes behind submitted hours is the "lookback" Zach describes, and it meets R4's three stories as long as capture is always on and cheap (R4, R6, R8).
3. Credit on a server-anchored grid works. A client can't invent minutes if the server issues the schedule and stamps arrival times. Pairtime's attestation pings can use the same idea even when the evidence itself stays local (R9).
4. Keep fraud signals server-side and unexplained. Lookout hides `activity` and JA4 from the token holder, Stardance keeps signal names in a private submodule, and the fraud page doesn't publish its patterns. Pairtime's client is AGPL and readable, so its trust has to come from server-side cross-checks, not client secrecy (R9).
5. Be exact about privacy. Lapse's "only you" claim doesn't match a server that decrypts at publish. Pairtime should state precisely what leaves the machine, when, and who can decrypt it (R6).
6. One heartbeat convention per source. Lapse sends `timelapsing` and Stardance's Lookout forwarding sends `coding`, and the two land in different Hackatime totals. If pairtime feeds Hackatime, it needs one documented category and editor value, agreed with Hackatime maintainers (R5, A).

## 5. Dark flow (fast.ai)

Source: Rachel Thomas, "Breaking the Spell of Vibe Coding", fast.ai, 2026-01-28 ([fast.ai/posts/2026-01-28-dark-flow](https://www.fast.ai/posts/2026-01-28-dark-flow/), fetched 2026-11-19). Zach's vision post links it to say that time in dark flow "shouldn't count" (BOUNTY.md).

The argument:

- Real flow (Csikszentmihalyi) needs a goal, rules, "clear clues as to how well one is performing", and challenge matched to skill.
- "Dark flow" is a term from gambling research ([PMC5846824](https://pmc.ncbi.nlm.nih.gov/articles/PMC5846824/)) for the absorption slot machines produce. It keeps the absorption and breaks the other conditions: luck drives outcomes while players credit their skill, and feedback is blurred by the "Loss Disguised as a Win" (you bet 20 cents, "win" 15 with celebration sounds, and are 5 cents down). Thomas also equates it with Csikszentmihalyi's "junk flow".
- Vibe coding fits that pattern: it "does not provide clear clues of how well one is performing", "the match between challenge level and skill level is murky", and it gives "a false sense of control" and "a misleading feeling of agency" (the user picks among options the model offers).
- Evidence cited: Armin Ronacher's "agent psychosis" post ("I spent two months excessively prompting the thing and wasting tokens"; tools he "felt really great about" that he never used), and METR's study, in which experienced developers "take 19% longer" with AI tools yet "still believed AI had sped them up by 20%" ([METR, 2025-07-10](https://metr.org/blog/2025-07-10-early-2025-ai-experienced-os-dev-study/): 16 developers, 246 real issues, mostly Cursor Pro with Claude 3.5/3.7 Sonnet). The METR page now says "These results are out of date" and points to a February 2026 follow-up.
- On measurement: in junk or dark flow "we lose our ability to accurately assess our productivity levels and the quality of our work", and "individuals are terrible judges of their own productivity."

The post names no detection signals and gives no advice to tool builders. What follows is my reading of how it should shape a struggle metric:

| The post says | So the metric should |
|---|---|
| Absorption and screen time aren't evidence of flow | Never count raw wall time, agent runtime, token volume or line counts as struggle. They are the "Loss Disguised as a Win" (R2, R9) |
| Real flow has clear feedback about performance | Credit the moments a person closes a feedback loop: reading or reviewing a diff, running the code or the tests, inspecting output, a fix after a failure, a manual edit to AI output (R2) |
| Dark flow is compulsive and repetitive (Ronacher's months of prompting); Zach adds "doing the same thing over and over again" | Detect re-roll loops: near-duplicate prompts in a short window, accept-then-revert cycles, regenerations with no edit or test in between. Count the first attempt and stop counting the repeats (R2, R9) |
| Agency is often illusory: the user picks among AI options | Weight prompts that change direction (new constraints, a rejected approach, a specific bug report) above "continue", "fix it" or "try again" (R2) |
| Self-assessment is unreliable (METR's 39-point gap between perceived and measured speed) | Don't use self-reported hours or self-ratings as the hour number. Show the user the measured split instead, which is also the self-improvement hook R3 asks for |
| Output that "felt great" was never used | Tie hours to a shipped artifact. The vision post's "The project must do what it says it does" (BOUNTY.md) makes the ship check part of the count (A) |

One caution: a metric that can be read off the client can be gamed. If pairtime publishes "a prompt with more than N characters within M minutes of a test run counts", people will script exactly that. The definitions can be public (R1 needs that), but the final number should come from server-side checks against evidence. That is how Hackatime and Stardance already work (section 1.9).

## 6. Strategy

The constraint that decides everything is A: 1,000 weighted projects means about 10,000 approved hours in `approved_projects` that programs tracked with pairtime. For scale, Stardance approved 27,600 hours (about 2,760 weighted projects) over a summer, so the bounty needs roughly a third of one Stardance's approved output. Whatever pairtime is, programs have to adopt it.

An approved hour passes through six layers today, and pairtime has to fit each one:

| Layer | What runs there now | What pairtime needs there |
|---|---|---|
| Tracker on the user's machine | WakaTime plugins and wakatime-cli pointed at Hackatime; Lapse and Lookout for screen evidence (1.5, 4) | A passive client people keep installed (R3, R4, R8) |
| Program app | A `HackatimeService` per program, often with a `LapseService` or `LookoutService` (3.4) | A Hackatime-shaped API so the same client code can read pairtime hours (A, R5) |
| Ship and hours review | In-app review (Stardance), `review.hackclub.com` (Flavortown) or Ari, whose time sources are `hackatime`, `journals`, `lapse` and `program` (3.3, 3.6) | A time source with rows a reviewer can cut, each linked to evidence (R9, R10) |
| Fraud desk | Telescreen and Robert, which take `kind: "hackatime"` or `"hardware"` and score 1 to 10 (3.5) | Either Hackatime projects Robert can read, or a new `kind` with a pairtime workbench (R9, R10) |
| Program Airtable | The handbook's justification fields, including `Alternate Tracking Method` (3.7) | An automated, handbook-shaped explanation and stable review links (A, R10) |
| Unified DB and spot-checks | `approved_projects`, HQ spot-checks with fines, removal if evidence can't be found (3.1, 3.7) | Evidence that stays reachable for spot-checkers (R10) |

| Option | What it means | For | Against |
|---|---|---|---|
| A. Hackatime-compatible server | Pairtime runs its own WakaTime-compatible ingest and answers Hackatime's program endpoints, so a program switches with a base-URL change | Every WakaTime plugin and wakatime-cli's AI parsers work through `[api_urls]` (1.5). Meets R5 with our own accounts, profiles and dashboard. Program code change is small | Re-implements profiles, leaderboards and GitHub mapping. Robert, Ari and Telescreen read Hackatime, not pairtime, so the review layers don't come along. Without human-side signals it's just Hackatime again |
| B. Extend Hackatime upstream | PRs to hackclub/hackatime (MIT): a struggle category, AI discounting in program stats, new heartbeat fields | Zero adoption cost: programs, Ari and Robert already read Hackatime, with 15,488 MAU (R11) | The bounty requires AGPL-3.0 (R1), and upstream contributions would be MIT. Maintainers decide scope and timing. HQ might not credit "pairtime" for hours that Hackatime reports |
| C. Lookout-style integration | A service programs embed: the program creates a session, pairtime evaluates it, the program reads the result | Clean trust model (server decides credit), already familiar from Lookout | Not passive (R4), weak on R3 (no reason to install it outside a program), and every program has to write integration code |
| D. Separate system that plugs into each layer | Pairtime has its own AGPL client and server (R5) and reaches programs through the Hackatime-shaped API, the handbook's `Alternate Tracking Method`, and later a time source in Ari and a `kind` in Robert | Works today with no HQ change (handbook path). Keeps pairtime's own evidence, profile and review page | The most moving parts, and it needs other teams (Ari, Telescreen, Quality and Integrity) for the strongest integrations |

My recommendation is D. Forwarding pairtime time into Hackatime as heartbeats, which I had in this plan before reading the handbook, is now a late and optional step. The handbook lists "heartbeats generated by a script rather than actual coding activity" as outright fraud (3.7), and Robert and Ari would read forwarded heartbeats as ordinary Hackatime time. A forwarded `coding` heartbeat would also slip past the one-third rule. Lookout's forwarding works because HQ sanctioned it. Pairtime's would need the same sanction first, or its users risk fraud flags.

Build order:

1. Ingest what Hackatime ingests. Expose `POST /api/v1/users/current/heartbeats(.bulk)` and `statusbar/today` in WakaTime's shape, and have the installer add pairtime under `[api_urls]` with `.* = https://<pairtime>/api/v1|<key>` while leaving Hackatime as the default. USAGE.md confirms that heartbeats then go to both. Users lose nothing and pairtime gets editor and AI-transcript data on day one (R4, R7).
2. Add pairtime's own human-side signals: harness hooks and the AGENTS.md `track` command (Karim's direction), local input and focus activity, review and test events. Compute credited struggle seconds per project server-side, with a reasons breakdown (R2, R9).
3. Give every submission a review page with a stable URL, plus a generated justification in the handbook's shape: the `Alternate Tracking Method` explanation, the review link for `Additional Justification`, and, when the user also runs Hackatime, `Hackatime Project Name(s) and Date Range(s)` and the Hackatime ID so reviewers can cross-check. This works with today's Airtable template and needs no HQ change (R9, R10, A).
4. Answer Hackatime's program calls with pairtime numbers: `GET /api/v1/users/:id/stats?features=projects&total_seconds=true&filter_by_project[]=...&start_date&end_date`, `trust_factor`, `authenticated/me`, with the same JSON shapes. A program's existing `HackatimeService` can then point a second instance at pairtime (R5, A).
5. Integrate with the review layer. Propose a `pairtime` time source to Ari (`src/lib/review/settlement.ts` already models sources and cuttable rows) and a `pairtime` kind or workbench to the Telescreen team. Until then, reviewers open pairtime's review page from the justification link (R9, R10, A).
6. Pilot. A pairtime-native YSWS program, which Zach allowed ("Yes!"), set up on the standard stack (Airtable template, Ari or in-app review, Robert for fraud), is the program whose review rules pairtime controls. An existing program is the second route: every program must already cut Hackatime `ai coding` time to a third "unless there is significant evidence proving that more time should be credited" (3.7), and pairtime is built to be that evidence. The third route is the low-ceremony Airtable-form programs that collect only self-reported hours (3.2). They have no tracker to replace, so pairtime's link plus a handbook-shaped `Alternate Tracking Method` text is a pure upgrade, and many small programs together can add up (A).
7. Only with explicit sanction from Hackatime's maintainers and HQ: forward credited time into Hackatime under one documented convention (an `editor` value like `pairtime`, a dedicated category, 60-second spacing like Lookout) (A).

The fastest credible path to 1,000 weighted projects is steps 1 to 3 plus step 6. The pitch to a program author is fewer spot-check fines and faster reviews: hours that arrive already deflated, with the reasons, in the handbook's own format. Building the whole of option A first would spend the time that adoption needs. Flavortown's sync shows one more constraint: hours on a project that another program already paid for count only as the increase (3.3), so a pilot should favour new projects.

## Design implications

Each item is tagged with the requirement IDs it serves.

1. **Ingest WakaTime heartbeats unchanged, as a second destination.** Implement `POST /api/v1/users/current/heartbeats` and `.bulk` (accept up to 100 per request, as Hackatime does; wakatime-cli sends at most 10), Basic, Bearer and `?api_key=` auth, every field in Hackatime's `HEARTBEAT_KEYS` plus `ai_cached_input_tokens`, and ignore unknown fields. Deduplicate with a hash over the canonical fields and a unique index, so offline-queue replays are idempotent. Answer `GET /api/v1/users/current/statusbar/today`. The installer adds a pairtime line under `[api_urls]` and leaves Hackatime as `api_url`. wakatime-cli's USAGE.md says heartbeats are "**always** sent to `api_url` ... **and also** to any matching `api_urls` patterns", so the user loses nothing (sections 1.3, 1.5, 2.1). (R4, R7, A)
2. **Treat WakaTime categories as untrusted input.** By the time a heartbeat arrives, wakatime-cli has already relabelled human editor heartbeats within 2 minutes (or up to 30 minutes) of agent activity as `ai coding`, and the VS Code plugin's edit-shape heuristic latches (section 2.4). Pairtime should store `category` as received but classify activity itself from raw signals, in a separate dimension (for example `activity_kind`: prompting, reading output, reviewing, editing by hand, testing, debugging, agent running, idle). Don't overload `category`. (R2, R9)
3. **Count human presence, not heartbeat gaps or agent runtime.** Hackatime's `LEAST(gap, 120 s)` sum and WakaTime's 15-minute timeout both measure the spacing of events. Transcript-derived `ai coding` heartbeats measure the agent, which is exactly why the handbook caps them at a third. Pairtime's unit should be an interval with human evidence in it: a prompt submission, hand edits, focus on and scrolling through the agent's output or a diff, a test run. Agent work with no human evidence counts zero. Watching an agent counts only up to a capped reading window after the last output the person actually looked at. (R2, R9)
4. **Answer the one-third rule with evidence, not a different constant.** The handbook caps Hackatime `ai coding` time at a third "unless there is significant evidence proving that more time should be credited", and Ari applies a flat `1.0 / 3` (sections 3.6, 3.7). For every AI-assisted window, pairtime should report three numbers: the agent's active time, the human-engaged seconds inside it, and the signals behind them. A reviewer can then credit above a third where the evidence supports it and below where it doesn't. Pairtime's value to a program is replacing the flat third with an evidenced figure. (R2, R9, A)
5. **Flag "AI slop" the way the handbook defines it.** "A project that was generated in a single prompt with no meaningful iteration, testing, or refinement" is rejected outright (section 3.7). Flag sessions and projects with one substantive prompt, no test runs, no hand edits and no follow-ups, and show the flag on the review page. (R2, R9)
6. **Detect dark flow and re-roll loops, counting the first attempt.** Signals: near-duplicate prompts within minutes, "continue", "try again" and "fix it" prompts with no read, edit or test between them, accept-then-revert cycles, and long agent runs with no review afterwards. Show the user the split, which doubles as the self-improvement view R3 asks for (section 5). (R2, R3, R9)
7. **One global timeline per user, then split by project.** Credit each second to exactly one project from a single time-ordered stream (Hackatime's `attributed_durations_by` approach), so per-project totals add up to the user's total. Hackatime's `PARTITION BY project` path can credit 120 s of wall time twice on every switch, and Flavortown audits the gap (section 1.4). This is R4's "5 different projects in an afternoon" story. (R4, R2)
8. **Deterministic, versioned hours.** Programs re-pull each devlog's own window, and spot-checkers compare the justification against the evidence later. The same query must return the same number forever. Store the credit-computation version with every submission, the way ari-webhooks stores `CurrentEvidenceVersion`, and re-run only open submissions when the math changes (sections 1.8, 3.6). (A, R10)
9. **Mirror the Hackatime program API with the same JSON shapes.** At minimum: `GET /api/v1/authenticated/me`, the stats call (`features=projects`, `total_seconds=true`, `filter_by_project` as an array or a comma string, `start_date`, `end_date`; accept `test_param`, `no_ai_coding` and `boundary_aware`), `GET /api/v1/users/:id/trust_factor`, `GET /api/admin/v1/user/info_batch?ids=` (up to 2,000), `POST /api/admin/v1/user/get_user_by_email`, and `GET /api/v1/users/:id/heartbeats/spans`. A program's existing `HackatimeService` can then point a second instance at pairtime. Unlike Hackatime, don't hand OAuth apps the user's full API key: issue scoped tokens instead (sections 1.8, 3.4). (A, R5)
10. **Add the submission object Hackatime lacks, and emit handbook-shaped justification.** A submission freezes project keys, a time window, credited seconds, the computation version, and hashes of the evidence behind it. It gets a stable review URL. Pairtime generates text for the handbook's `Alternate Tracking Method` field (how pairtime measures, automated) and `Additional Justification` (the review link), plus `Hackatime Project Name(s) and Date Range(s)` in the handbook's `name M/D/YYYY-M/D/YYYY` format when the user also runs Hackatime. Use `externalId`-style idempotency and HMAC-signed webhooks (`sha256=` over `<timestamp>.<body>`, 300 s tolerance) as Robert does, so program authors meet conventions they already know (sections 3.5, 3.7). (R9, R10, A)
11. **Report a conservative number with its reasons.** "When in doubt, deflate" is HQ policy, and spot-checks fine programs for hours the evidence doesn't support (section 3.7). Show reviewers raw logged time, credited struggle time, and the breakdown of what was discounted (agent-only time, re-roll loops, idle reading). Model the breakdown as rows a reviewer can cut, like Ari's settlement rows, never as one opaque score (sections 3.3, 3.6). (R9, R2)
12. **Two privacy tiers, with commitments made at the time.** Logged hours keep raw prompts, code and any screen frames on the machine in a rolling local buffer. Every minute or so the client sends the server a hash commitment and the server stamps its own time on it, the way Lookout anchors credit to server time (section 4.2). When the user submits, the client uploads evidence for the submitted window only, and the server checks it against the commitments. That gives a "lookback" mode in which evidence can't be fabricated afterwards. It meets R4 without asking anyone to press record, and R10's "different privacy guarantees for logged vs submitted hours". (R4, R6, R9, R10)
13. **Keep submitted evidence reachable for spot-checks.** A project whose "required evidence ... cannot be located using the information provided" is removed from the Unified DB and the program is fined (section 3.7). Review pages and submitted evidence must stay available to HQ reviewers signed in with Hack Club Auth for the whole spot-check window, while logged-only data never leaves the machine. Lookout keeps original screenshots for only 7 days after a session stops (the compiled timelapse and timestamps stay), which is too short if spot-checks come later and need the frames (section 4.2). (R10, R6, A)
14. **Reuse the evidence formats reviewers already know.** For screen evidence, match Lookout's capture shape (60 s clips of about 6 frames, a JPEG fallback, `trackedSeconds`, `activeSeconds`, `captureTimestamps`) and Lapse's detection lookup (`playbackUrl`, `snapshotTimestamps`, `trackingMode`) rather than inventing a new one (sections 4.1, 4.2). (R10, A)
15. **Keep fraud signals server-side, in a private component.** The client is AGPL and readable, so trust can't depend on client secrecy. Copy Hackatime's trust model (`blue`/`green`/`yellow`/`red`, `yellow` masked as `blue`, a reason required, an audit log with edit history) and its review tools (poison exclusions, alt detection by machine and IP, JA4 and user-agent search, quantized cursor plots, a multi-user timeline). Hide raw interaction metadata from token holders, as Lookout does. Ari's open core (public image without `private/`, private image built with a separate context) and Stardance's private submodule are the precedents for keeping detection rules out of the public tree. See Open questions on AGPL section 13 (sections 1.9, 3.6, 4.3). (R9, R1)
16. **Plug into Robert and Ari rather than replacing them.** Robert's only time-bearing input is `kind: "hackatime"` plus project names, and Ari's time sources are `hackatime`, `journals`, `lapse` and `program` (sections 3.5, 3.6). Design pairtime's review data so it maps onto those shapes: per-row captured seconds, a reviewer cut per row, and a deep link per row. Then a `pairtime` kind or source is a small change for those teams to accept. (A, R10)
17. **Don't forward synthetic heartbeats into Hackatime without sanction.** The handbook treats "heartbeats generated by a script" as outright fraud, and forwarded `coding` heartbeats would sidestep the one-third rule. If HQ and Hackatime's maintainers agree, forward at 60 s spacing or less (Hackatime credits at most 120 s per gap and 0 for a lone heartbeat), with one documented `editor` value and category, in a project key separate from the user's WakaTime project. Otherwise Hackatime merges both sources into one timeline and reports the union, not pairtime's number (sections 1.4, 1.8, 3.7, 4.2). (A, R9)
18. **Use Hack Club's identity and privacy plumbing, and minimise what programs get.** Sign in with Hack Club Auth so programs can match users without email lookups. Implement the privacy hub's deletion and export endpoints (ES256 JWT, `iss: hackclub-privacy`, dry run first, single-use `jti`), as Hackatime and Lapse do. Send programs and review desks Slack and Hackatime ids rather than names, emails or addresses, as Alchemize does with Robert (sections 1.7, 3.4, 3.5, 4.1). (R5, R6, A)
19. **Budget for evidence, not heartbeats.** All of Hackatime's 426M heartbeats fit in 5.5 GiB of ClickHouse. A columnar or lightly indexed event store is cheap at R11's scale. The costs that could break $500/month are screen evidence (about 66 MB per recorded hour at Lookout's rate, so upload only for submitted hours) and per-event server-side LLM calls, which pairtime should avoid (sections 1.10, 4.2). (R11, R8)
20. **Real-time without recomputing everything.** Hackatime has no websockets and recomputes the dashboard from heartbeats with 1 to 15 minute caches. R5 asks for a dashboard that "updates in real-time as time is logged", so push credited-time deltas to open dashboards over server-sent events, and keep the status-bar call cheap (section 1.7). (R5, R11)
21. **Pilot with one program before building everything.** Steps 1 to 3 of section 6, plus a pilot program (pairtime-native, or an existing one fighting the one-third rule), is the shortest path to measurable approved hours. Ship the submission, review page and handbook-shaped justification before profiles and leaderboards reach Hackatime parity. Favour new projects, because hours already paid by another program count only as the increase (sections 3.3, 6). (A)

## Open questions

For Karim or Hack Club staff.

1. **Attribution.** The handbook's `Alternate Tracking Method` field is where a program names a tracker other than Hackatime or Lapse. Will HQ count a project toward the bounty when that text names pairtime, or will HQ add a dedicated field (like `Lapse Link(s)`) for pairtime review links? (A)
2. **"Significant evidence" for AI time.** What would Quality and Integrity accept as "significant evidence proving that more time should be credited" than a third of `ai coding` time? Event timelines, prompt counts and lengths, diff-review and test timelines, screen evidence for submitted hours, or prompt text? This decides what pairtime uploads at submission. (R2, R6, R9, R10)
3. **Two trackers, one number.** If a user runs Hackatime and pairtime side by side, which number does a reviewer use, and does the one-third rule still apply to the Hackatime half? (R9, A)
4. **Spot-check window.** How long after approval can a spot-check happen? That sets how long pairtime must keep submitted evidence reachable. (R10, R6, R11)
5. **Weight formula.** Does `YSWS–Weighted Project Contribution` use `Override Hours Spent` ahead of `Hours Spent`, and does it round or cap? It's an Airtable formula and not in any repo. (A)
6. **Telescreen and Robert.** Would the Telescreen team add a `pairtime` kind or workbench to Robert, and can pairtime's builders see Telescreen's source or full API docs (both are behind HCA sign-in)? (A, R10)
7. **Ari.** Would Ari's maintainers accept a `pairtime` time source? Ari has no license in its repo metadata, so how contributions and reuse work is unclear. (A, R1)
8. **Hours delivered through Hackatime.** If pairtime computes the hours but a program reads them from Hackatime (forwarded heartbeats), do they count toward the bounty, and would HQ and Hackatime's maintainers sanction the forwarding so it isn't treated as scripted heartbeats? (A, R1, R9)
9. **Pilot program.** Which program would pilot? If pairtime-native, who funds the grants (programs receive about $8.50 per approved hour from HQ), and which review stack would it use: Ari, `review.hackclub.com`, or its own? (A)
10. **Lookback.** What retention window and evidence quality would make a lookback mode "sufficiently high quality" to count as passive? Nothing called lookback exists yet in Lapse or Lookout. (R4)
11. **AGPL and private fraud rules.** Can detection thresholds live in a separately deployed private component or in data while the server stays AGPL, as Ari and Stardance keep theirs out of public repos? This needs a decision from Karim, and legal advice if anyone doubts it. (R1, R9)
12. **Hosting.** Will pairtime run on Hack Club infrastructure for the pilot and the R11 cost check, or on Karim's domain? Who pays during the pilot? (R11, R5)
13. **Progress tracking.** Will HQ give read access to the Unified YSWS DB, or a count, so pairtime can track its progress toward 1,000 weighted projects? (A)

## Fact-check log

Adversarial fact-check, 2026-11-19. Each row is one claim a designer is likely to rely on, checked against a primary source opened during the fact-check (not the doc's own citation). "Confirmed" means the source says what the doc says. Rows are added as checks finish.

| # | Claim (section) | Source opened | Result and change |
|---|---|---|---|
