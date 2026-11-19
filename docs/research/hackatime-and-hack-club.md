# Hackatime, WakaTime, Lapse, Lookout and the Hack Club YSWS pipeline

Status: draft in progress

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

Licenses come from `gh api repos/OWNER/REPO` (`.license.spdx_id`) on 2026-11-19. Web pages were fetched on 2026-11-19.

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

There are 28 indexes on this table, most of them partial (`deleted_at IS NULL`) composites led by `user_id` and `time`.

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
| `wakatime_mirrors` | `endpoint_url` (default `https://wakatime.com/api/v1`), `encrypted_api_key`, `last_synced_heartbeat_id` | Forwards a user's heartbeats to WakaTime or another compatible server |
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

- `heartbeat_timeout_duration` defaults to `2.minutes`, and no caller overrides it.
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
- Other: daily goals, weekly summary emails, Slack status updates, embeddable badges (`GET /api/v1/badge/:user_id/*project`), data export, imports from WakaTime and Wakapi, and mirroring to WakaTime.

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
- Programs send a `RACK_ATTACK_BYPASS` header (`HACKATIME_BYPASS_KEYS`) to skip rate limits, which suggests the throttles have been switched on at some point, even though they are off at this commit.

### 1.9 Anti-fraud and trust

(pending)

### 1.10 Hosting, scale, cost

(pending)

## 2. WakaTime

### 2.1 Heartbeat schema and API

(pending)

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

(pending)

### 2.4 Why people call it inaccurate

(pending)

## 3. The Hack Club YSWS pipeline

### 3.1 The Unified YSWS Projects DB

(pending)

### 3.2 How a weighted project is calculated

(pending)

### 3.3 How programs reviewed and approved hours

(pending)

### 3.4 What a new tracker must provide

(pending)

## 4. Lapse and Lookout

### 4.1 Lapse

(pending)

### 4.2 Lookout

(pending)

### 4.3 Lessons for pairtime

(pending)

## 5. Dark flow (fast.ai)

(pending)

## 6. Strategy

(pending)

## Design implications

(pending)

## Open questions

(pending)
