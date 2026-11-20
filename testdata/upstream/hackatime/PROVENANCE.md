---
method: copied
captured_on: 2026-11-19
captured_by: WP-PG0
source: https://github.com/hackclub/hackatime/tree/43bde10b02dbd2371fb4f802e724a1a875ac0dfc MIT
---

# Hackatime at 43bde10b02db

hackclub/hackatime is MIT-licensed, so this is the one directory that quotes upstream code. `LICENSE` is Hackatime's notice, byte for byte (git blob `2b5d105834bb`). Commit `43bde10b02db` ("Upgrade to Ruby 4.0.10 (#1828)", 2026-11-19) is the one HC 0 read. Hackatime's `main` had moved on to `3250f9bbf544` by the time this was captured, and nothing here comes from the newer commit.

## What's here

| File | What it is | Whose words |
|---|---|---|
| `LICENSE` | Hackatime's MIT notice | Hackatime's |
| `excerpts/` | Line ranges from twelve files, each block under a marker that names its range | Hackatime's, verbatim |
| `endpoints.json` | The eight reads programs call: route, action, credentials, parameters and the forms they take, statuses | Ours, citing lines |
| `responses/` | A JSON Schema and an example for each response body | Ours |
| `durations.sql` | Hackatime's duration SQL composed into four standalone PostgreSQL functions | Hackatime's fragments, our assembly |
| `durations.json` | Heartbeat sets with the seconds each function should return, worked out by hand | Ours |

## Files quoted

Fetched with `gh api -H "Accept: application/vnd.github.raw" "repos/hackclub/hackatime/contents/<path>?ref=43bde10b02dbd2371fb4f802e724a1a875ac0dfc"` on 2026-11-19. The blob ids are `git hash-object` of what came back, the same ids the contents API reports. `excerpts/` cut the ranges with `sed -n`, so each block is the upstream text unchanged.

| Upstream file | Git blob | Lines quoted |
|---|---|---|
| `config/routes.rb` | `0a6026c1fa0a` | 245-247, 255-257, 269-284, 286-291, 300 |
| `app/models/concerns/heartbeatable.rb` | `d033bd276ea3` | 25-41, 44-46, 86-134, 227-248, 250-311, 331-339 |
| `app/controllers/api/v1/stats_controller.rb` | `5c62ea1bc306` | 2, 5-6, 32-113, 115-126, 128-138, 168-184, 205-215, 226-234 |
| `app/controllers/api/v1/authenticated/me_controller.rb` | `6809c76a7f08` | 9-27 |
| `app/controllers/api/v1/authenticated/api_keys_controller.rb` | `72455719676d` | 5-13 |
| `app/controllers/api/v1/authenticated/application_controller.rb` | `46ba56e89f66` | 16-26 |
| `app/controllers/api/v1/my/heartbeats_controller.rb` | `179ac223e626` | 26-38, 42-50 |
| `app/controllers/concerns/render_helpers.rb` | `ec322217afdc` | 9-29 |
| `app/controllers/concerns/api/admin/v1/user_utilities.rb` | `29b7c654b68c` | 32-37, 284-297 |
| `app/models/user.rb` | `b492014cc09a` | 48-59, 95-112 |
| `lib/wakatime_service.rb` | `69d87c81dd6e` | 7-32, 34-38, 72-110, 119-137, 275-292 |
| `app/helpers/application_helper.rb` | `cf630cc550ba` | 40-56 |

Read and cited by line, not quoted: `app/controllers/application_controller.rb` (`3ca603904e49`, lines 14-19 and 45-47, the per-user time zone and the session user), `config/application.rb` (`f1ff96912a80`, line 37), `app/models/heartbeat.rb` (`5bcd4412a8e8`, lines 11-12, the default scopes), and the lines of `me_controller.rb` and `user_utilities.rb` that `endpoints.json` cites outside the quoted ranges. A code search at Hackatime's `main` on 2026-11-19 found `config.time_zone` set nowhere else and no `Time.zone_default`, which is what `endpoints.json`'s `time_zone` note rests on.

## What was left out

- The heartbeat ingest responses under `/api/hackatime/v1`. WP-AD16 adds those shapes to this directory.
- Each controller's class-level filter declarations. The excerpts quote method bodies, and `endpoints.json` says which credential each action takes, which is what a client needs. pairtime issues its own scopes (DESIGN 3.10).
- Admin, moderation and fraud tooling, apart from the two admin reads Stardance calls.
- In `durations.sql`, the default scope's `heartbeat_exclusions` predicate. It hides heartbeats Hackatime staff exclude, and a client's own copy of its heartbeats has none.

## How the downstream WPs use it

- WP-PG3, WP-PG4 and WP-PG5 validate pairtime's answers against `responses/*.schema.json`, using `endpoints.json` for which schema goes with which call and status, and `identifier_lookup` for `:uid` resolution.
- WP-PG11 loads `durations.sql` into Postgres 18, runs every case in `durations.json`, and checks `pkg/wakacompat` against the same sets. Nobody ran the SQL or the vectors in WP-PG0, since this machine builds and runs nothing. If Postgres disagrees with a vector, that is an erratum for Karim (PLAN ground rule 6), and the quoted Ruby decides which side is wrong.
- `durations.sql` makes two textual changes to what ActiveRecord sends, an `IN` list as `= ANY` and the boundary heartbeat as a subquery. Its header explains why neither changes a result.
