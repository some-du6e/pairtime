# Teen layer API, version 1: devlogs, badges and build days

Status: the contract between `pairtimed` and the client for the teen layer's server side (WP-SV12 part 1; DESIGN 3.10, 4.3, 4.4, 6.2 D10 and D13; docs/errata/2026-11-23-build-day-lag.md and docs/errata/2026-11-23-build-days-devlogs-retention.md). The orchestrator decided its open points on 2026-11-23: the device token without a request signature, the 64 KiB limit, the badge codes, the summary's path and the week bounds; and in the review of its code (#283), the request rate, the caps on an account's devlogs and removal records, and one badge per code. Changing it needs both sides.

Every request carries the device token `pairtime login` keeps, as `Authorization: Bearer ptd_...` (docs/spec/device-auth-v1.md), and acts for the token's account. No request is signed with the device key: DESIGN 4.3's publishing row asks for none. The client sends a publish only when the teen clicks Publish, and a delete or a removal only when the teen takes something down (DESIGN 4.3).

## 1. Answers

Every answer is JSON with `Cache-Control: no-store`. A refusal is `{"error": "<code>", "detail": "<text>"}`, as ingest's are:

| Status | `error` | When |
|---|---|---|
| 401 | `token_missing` | No bearer token |
| 401 | `token_invalid` | The token fails its checksum, names no device, or its device was removed or its account marked ineligible |
| 400 | `body_shape` | The body isn't one JSON object with exactly the members the route takes, each of its type |
| 400 | `body_unreadable` | The body couldn't be read, as when the connection broke off; retry the same body |
| 400 | `json_invalid` | The body isn't valid UTF-8 |
| 400 | `id_invalid`, `project_invalid` | An id or project key isn't 1 to 128 letters, digits, `_` or `-` |
| 400 | `markdown_empty` | A devlog with no Markdown |
| 400 | `bad_markdown` | Markdown holding a C0 control character (U+0000 to U+001F) other than tab, line feed and carriage return, none of which Markdown needs. DEL (U+007F) and the C1 characters (U+0080 to U+009F) are taken |
| 400 | `code_unknown` | A badge code that isn't on the list in 4 |
| 413 | `markdown_too_large`, `body_too_large` | Markdown over 65,536 bytes, or a body larger than any devlog's |
| 409 | `limit_reached` | A publish that would take the account past 1,000 devlogs or 8 MiB of Markdown between them (2), or a delete or removal of an id the account doesn't hold while it keeps 1,000 removal records (5) |
| 409 | `already_shown` | A show of a code the account shows already, under another id (4) |
| 410 | `removed` | A publish or show of an id the teen deleted or removed in the last 30 days (5) |
| 429 | `rate_limited` | The device is sending faster than its rate, or another of its requests is still being answered; wait `Retry-After` seconds, then send it again |
| 503 | `unavailable` | The server couldn't read or write; every 503 carries `Retry-After: 5`, as ingest's do, and the request goes again after it |

Each device's requests to the six devlog and badge routes (2 to 5) count against a rate of their own, as ingest counts a device's uploads (SP 8.6): a burst of 30 requests, refilled one every 10 seconds, and one request in flight at a time. A publish or a show counts once its body is in, which has 60 seconds to arrive, as at ingest. The rate is kept apart from the device's upload rate, which only signed requests use up. `GET /api/v1/build-days` (6) isn't counted. The caps on an account's devlogs and removal records are the server's own (#283), so that one device token can't fill the server's disk.

## 2. `POST /api/v1/devlogs`

```json
{"id": "<key>", "project": "<key>", "markdown": "<text>", "ai_assisted": false}
```

| Member | Meaning |
|---|---|
| `id` | A random key the client made when the teen clicked Publish. A retry sends the same one |
| `project` | This device's key of the project the preview names, as ingest's slots name it |
| `markdown` | The devlog exactly as previewed, at most 65,536 bytes of UTF-8, with no C0 control character (U+0000 to U+001F) but tab, line feed and carriage return. DEL and the C1 characters are taken |
| `ai_assisted` | Whether the teen's agent wrote the draft (DESIGN 5.5) |

`201` answers with the devlog the server stored, in the form `GET` lists it. A publish of an id the account holds already answers `200` with the devlog stored then, and changes nothing, so a retry after a lost answer stores nothing new. A publish of an id in the removal records (5) answers `410` and stores nothing. An account holds at most 1,000 devlogs, with at most 8 MiB (8,388,608 bytes) of Markdown between them: a publish of a new id that would take it past either answers `409 limit_reached` and stores nothing. The devlog's time is when the server received it, set in the transaction that stores it, and it bounds D13's windows: deleting or republishing a devlog moves no frozen window (DESIGN 7.5 step 2).

## 3. `GET /api/v1/devlogs` and `DELETE /api/v1/devlogs/{id}`

`GET` answers `{"devlogs": [...]}`, every devlog of the account in the order the server received them:

```json
{"id": "<key>", "device": "<device_id>", "project_key": "<key>", "project": "<project id>",
 "markdown": "<text>", "ai_assisted": false, "received_at": "2026-11-20T10:00:00Z"}
```

`project` is the account-level project id the device's key has in `rollup_projects` at the read, as the dashboard counts the key's minutes (D-account-project-id). So when project registration (WP-SV10) links the keys of one repo, the devlog moves with the project.

`DELETE` deletes the devlog at once and answers `204`, whether or not the server held it, and keeps only its id and the time in the removal records (5). A delete of an id the server never held is what makes a publish of it sent later get `410`, so the client sends it even when the publish hasn't been answered (8). While the account keeps 1,000 removal records, a delete of an id the server doesn't hold answers `409 limit_reached` and keeps nothing. A delete of a devlog the server holds is never refused, and keeps its record however many the account keeps.

## 4. `POST`, `GET` and `DELETE /api/v1/badges`

`POST` takes `{"id": "<key>", "code": "<code>"}` and answers as a devlog's publish does: `201`, `200` for an id the account holds, `410` for a removed one. An account shows each code at most once, since DESIGN 4.4 keeps the badges the teen chose to show as codes: a show of a code the account shows already, under a new id, answers `409 already_shown`. `GET` answers `{"badges": [{"id", "code"}, ...]}`. `DELETE /api/v1/badges/{id}` removes the badge at once and answers `204`, as a devlog's delete does, with the same cap on ids the account doesn't hold.

The codes are DESIGN 3.8's moment kinds, none of which counts hours: `first_commit`, `first_green_test`, `first_test_written`, `rejected_agent_edit`, `interrupt`, `fixed_by_hand`, `long_fight`, `loop_caught`, `comeback`, `new_language`, `new_tool`. A later version may add codes.

## 5. Removal records

A devlog's delete or a badge's removal keeps the id and the time, with no text, project or code, for 30 days after the removal (`server.removed_items`). A publish or show of that id from any of the account's computers answers `410` meanwhile, so a computer that missed the removal can't publish the item again. A second delete keeps the first one's time, and answers `204` however many records the account keeps. While the account keeps 1,000 removal records, of any ids, a delete or removal of an id it doesn't hold answers `409 limit_reached` (3) and keeps nothing. A held item's delete or removal always keeps its record, past 1,000 too. The client drops a publish unanswered for 30 days and tells the teen (WP-CO4).

## 6. `GET /api/v1/build-days`

The account's build days as of the request, which the local Build days page mirrors (DESIGN 3.8):

```json
{"time_zone": "America/New_York", "today": "2026-11-20", "weekly_goal": 3,
 "build_days": 18, "window_days": 30, "weeks_in_a_row": 4,
 "freezes": {"held": 1, "max": 2, "earned": 3, "toward_next": 5, "every": 7},
 "days": [{"date": "2026-10-22", "state": "build_day", "a_ms": 912000, "b_ms": 0,
           "deadline": "2026-10-26T04:00:00Z", "decided_at": "2026-10-22T15:01:00Z"}, ...],
 "weeks": [{"start": "2026-10-19", "result": "met", "goal": 3, "build_days": 4}, ...]}
```

| Member | Meaning |
|---|---|
| `time_zone`, `today` | The profile's zone, which sets every day's bounds, and the date in it |
| `build_days`, `window_days` | Consistency: the build days among the last `CONSISTENCY_WINDOW_D` dates, today included |
| `weeks_in_a_row` | The weeks in a row met or covered by a freeze, the current week counting once it is met; a week not decided yet neither adds to it nor ends it |
| `days[].state` | `build_day`; `not_counted`, decided at the deadline, or past it with nothing to decide; or `open`. A day counts the moment the server holds `BUILD_DAY_S` of it, from minutes it labeled witnessed or offline, and an open one becomes `not_counted` at its `deadline`, its end plus `BUILD_DAY_LAG_S` (D10) |
| `days[].a_ms`, `b_ms` | A stored day's credit at levels A and B as D10 decided it: as of the moment it first reached `BUILD_DAY_S`, or else as of its deadline. An open day's credit so far by the same read, from the revisions the server labeled witnessed or offline, never the daily rollups' figure at the levels the devices claimed |
| `weeks[].result` | `met`, `frozen` (a held freeze covered it), `missed` or `open`. Weeks run Monday to Sunday by the days' local dates |

The weekly goal is `WEEKLY_GOAL_DEFAULT` until WP-SV12 part 2 adds its setter, and a week keeps the goal it was decided by.

## 7. On the server

Migration 0011 creates the tables. Their periods are DESIGN 4.4's rows, in docs/retention.json:

| Table | Kept |
|---|---|
| `build_days`, `build_weeks`, `build_goals` | while the account exists (`server.build_days`) |
| `devlogs` | until the teen deletes the devlog (`server.devlogs`) |
| `badges` | until the teen removes the badge (`server.badges`) |
| `removed_items` | 30 days after the removal (`server.removed_items`) |

`zone_changes` keeps every change of the profile's zone, its time and the zone it replaced, under `server.account` with `users`, so a minute takes its date in the zone in force at its own time. The eraser (WP-SV13) deletes an account's rows in these seven tables through `buildday.DeleteAccount` and `devlog.DeleteAccount`, inside its transaction, and the export counts them. Whichever of WP-SV12 and WP-SV13 lands second wires both calls in.

## 8. The client (WP-CO4)

What the client does with these routes, from the reviews of the decisions (#278) and of this API's code (#283):

- A publish gets a random id when the teen clicks Publish. The client sends it again with that id until the server answers, drops it with a note to the teen if 30 days pass unanswered, and shows the devlog as deleted on a `410`.
- Deleting a devlog sends `DELETE` by its id at once, and again until the server answers, with no 30-day limit, whether or not its publish was answered. It cancels the devlog's unanswered publish. So a devlog published and then deleted while the server is unreachable is never stored: after reconnecting, the server holds only its removal record, or, while the account keeps 1,000 removal records, nothing, and the delete stays queued (below).
- Each queued publish and delete keeps the server and device id `account.json` named when it was made, and goes only while `account.json` names both. `pairtime logout` drops the queued publishes and tells the teen, so a publish queued before a logout is never sent under the account signed in after it.
- A queued delete survives a logout and goes again once `account.json` names its server and device id again. Logout's note names each delete it keeps and says the devlog stays public until this computer signs in to that server again or the teen deletes it elsewhere. Each queued delete also keeps the account the token answer approved, and a sign-in to that server and device id that names another account drops it with the note.
- A sign-in to another server or device drops both, with the note, as `project-names.json` keeps its requests until such a sign-in (DESIGN 3.5).
- A `409` on a publish or a show is the server's answer, and final: the client drops the publish or show and tells the teen, naming the account's limits for `limit_reached` and the badge already shown for `already_shown`. A `409 limit_reached` on a delete isn't final, since the server kept no record of the id, and a publish of it still on its way could land: the client keeps the delete queued and sends it again as it does one unanswered. A held item's delete never gets it, and the account's records fall below 1,000 as each one's 30 days pass. A `429` or a `503` asks for the same request again after `Retry-After`.
- Badges follow the same rules.
