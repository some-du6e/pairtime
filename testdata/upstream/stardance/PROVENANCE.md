---
method: described
captured_on: 2026-11-19
captured_by: WP-PG0
source: https://github.com/hackclub/stardance/tree/b1d3e0eac1aef07c7a137111339babe05586607e none
---

# Stardance's HackatimeService at b1d3e0eac1ae

hackclub/stardance has no license in its repository metadata (`gh api repos/hackclub/stardance --jq .license` is null on 2026-11-19), so nothing here is Stardance's code or prose. `requests.json` describes, in our words, every HTTP request `app/services/hackatime_service.rb` makes: method, path, query parameters with the form each value takes, body, credential, the response fields Stardance reads, and what it does when a call fails. Each entry cites the lines it describes. Stardance's `main` had moved on to `8606eb06ace8` by capture time; everything here is at the pin.

WP-PG3, WP-PG4 and WP-PG5 replay these requests against pairtime and check the answers with `../hackatime/responses/`. WP-PG5's Ruby client is written from this list.

## Files read

Fetched with `gh api -H "Accept: application/vnd.github.raw" "repos/hackclub/stardance/contents/<path>?ref=b1d3e0eac1aef07c7a137111339babe05586607e"` on 2026-11-19. Blob ids are `git hash-object` of what came back.

| File | Git blob | Used for |
|---|---|---|
| `app/services/hackatime_service.rb` | `288907c6b8f8` | every request, its connection and its error handling |
| `app/models/project.rb` | `2f1f436841a5` | which windows `fetch_total_seconds_for_projects` is asked for (lines 663, 673-689) |
| `app/models/post/devlog.rb` | `301e56f3db88` | the per-name devlog breakdown (lines 214-217) |
| `app/models/project/hackatime_devlog_resync.rb` | `0a10a4a34225` | re-pulling a devlog's window (lines 73-87) |
| `app/models/user/hackatime_sync.rb` | `5338d98dced4` | the project list and trust level calls (lines 48, 87) |
| `app/models/user/hackatime_project.rb` | `5e3b17abd2e9` | the two project names Stardance ignores (line 38) |
| `app/models/streak_activity.rb` | `3230290d9d84` | the spans call and its date forms (lines 95-101) |
| `app/jobs/project/ensure_hackatime_projects_job.rb` | `143641f96611` | project seeding (lines 21, 67, 70, 95-96) |
| `app/services/lookout_heartbeat_forwarder.rb` | `fbc2a710db1a` | the heartbeats Lookout forwarding sends (lines 34, 46, 61-76) |
| `app/services/lookout_push_status.rb` | `4fd63c9ea27a` | the raw heartbeats call (lines 56-61) |

## How it was described

- A parameter's `form` says what the value is and where it comes from. Where a caller decides the form, such as ISO 8601 times for devlog windows against dates for the streak sync, the caller is cited.
- Headers are listed when a server answering these calls has to read them: the credential, the content type, the cache directive and the user agent.
- `reads` lists the response fields Stardance uses, so a compatible server knows which fields it can't drop.
- The example bodies use made-up values in the shape the code builds.
