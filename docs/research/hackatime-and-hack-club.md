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

1. The editor plugin guesses from the shape of edits (section 1.5). A single change of more than 2 newlines or 50 characters, repeated within 500 ms, flips `isAICodeGenerating` on. Single-character inserts or deletions are needed to flip it off, and other edit kinds reset the countdown (#495). Autocomplete, snippets, formatters, paste and multi-cursor edits all look like AI. AI edits applied through the file system (an agent writing files) don't show up as editor inserts at all.
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

None of these fields name a time tracker. The unified DB holds only an hour number and a justification string, and the program owns that number. So a project counts toward the bounty when a program puts it in this table with hours that came from pairtime. Judging that will probably mean reading `Override Hours Spent Justification` or asking the program (see Open questions).

### 3.2 How a weighted project is calculated

"A weighted project is 10 hours of shipped work" ([readme.hackclub.com/nutshells](https://readme.hackclub.com/nutshells), fetched 2026-11-19). A Hack Club post from 2026-03-04 adds "A project with 20 hours counts as 2 weighted projects, 30 hours as 3 weighted projects" ([notes.deven.dev](https://notes.deven.dev/posts/the-plan-for-hack-club-world-dominance)). The warehouse code agrees: `bin/ysws_true_expense.py` says "weighted hours == SUM(weighted_project_contribution)*10". The formula itself is an Airtable formula field and isn't in any repo, so I couldn't check whether it uses override hours ahead of `Hours Spent`, rounds, or caps (unverified). The one capped weight in code is `weighted_referral_count = sum(min(user_hours, 10)) / 10` per user and program (`orpheus_engine/defs/ysws_programs_sync/definitions.py`), and that is a different metric.

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

This is where BOUNTY.md R9's numbers come from: 123,400 Hackatime hours submitted to Stardance, 27,600 approved so far. Every hour went through a person who decided how much of the raw Hackatime figure to keep. The tracker supplies the starting number and the evidence; the program's reviewers decide the hours. A tracker that makes step 3 faster and more consistent is what program authors would value.

### 3.4 What a new tracker must provide

(pending)

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

Passive or not: Zach says Lapse "is not passive. You need to turn it on to start tracking" (BOUNTY.md). I found no "lookback" mode: GitHub code search for `lookback` in hackclub/lapse and hackclub/lookout returns nothing (2026-11-19), so the term exists only in Zach's reply. [hackclub/elapsed](https://github.com/hackclub/elapsed) (MIT, .NET/Uno, last pushed 2026-07-01) is a native third-party client for the Lapse API, not a new recorder.

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

(pending)

## Design implications

(pending)

## Open questions

(pending)
