# Erratum: DESIGN 4.4 has no rows for WP-SV12's build days, devlogs and badges, and deleting a devlog can re-window submitted time or bring it back

Date 2026-11-23. Raised by the plan for WP-SV12 part 1. Status: decided 2026-11-23 by the orchestrator for Karim (D-sv12-retention). Applied to DESIGN 3.10, 4.2, 4.3, 4.4, 6.2 D13, 7.5 step 2 and 7.7 item 9 and to PLAN in a docs PR of its own; docs/retention.json, the retention tests and the privacy notice follow in WP-SV12.

## Input

- Migration 0011 creates build_days, the weekly-goal and freeze state, devlogs, badges, the removed-item records and nullable users columns for the time-zone change. TestTables needs each table in docs/retention.json, and TestDesignPeriods ties each period to a DESIGN 4.4 row.
- A minute's received_at_ms lives only in minute_buckets and minute_bucket_revisions, which the minute-bucket job deletes at 180 days. rollups_daily keeps none, and a weekly streak passes 180 days at week 26 (182 days).
- D13 builds devlog windows from the devlogs that exist at submission time. The devlog API is POST, GET and DELETE, so a typo fix is a delete and a new publish.
- 8.3 and TE 7.2 make deleting anything published take effect at once (TP B2). A computer whose publish was stored but whose answer it lost sends the publish again.

## Expected

Every kind of data the server keeps has a 4.4 row and a finite period, a deletion stays a deletion, no submitted second is submitted again, and frozen numbers stay frozen.

## Decision

(a) A build day is stored as D10 decided it, for the account's lifetime, like the daily totals, because nothing can recompute it after 180 days.

(b) Devlogs and badges are tier-3 data. Each is kept until the teen deletes or removes it, or asks the controller to, which deletes the row at once from every page and read, a review page's story included, or until account deletion.

(c) Each devlog and badge carries a random id the client made. A delete or removal keeps only that id and its time, for 30 days, as project_name_hides does, so a publish sent again from any computer gets 410 and stores nothing. The client drops a publish unanswered for 30 days and tells the teen.

(d) A devlog's time is when the server received it, and D13's windows start no earlier than the end of the latest window frozen by a submission on the project that wasn't withdrawn. Worked case: devlogs at t1, t2 and t3, and a submission frozen on [t1, t2] and [t2, t3]. The teen deletes t3 to fix a typo and republishes it at t4. The next list is [t3, t4], not [t2, t4]. After a withdrawal, [t2, t4] is open again, and 7.5 step 5's sticky draw still applies.

(e) The freeze records devlog windows as start and end times, so the submitted windows and their numbers never move. 5.10's 'Frozen numbers' needs no edit, since programs re-pull by their own devlogs' dates (HC 3.3).

(f) A devlog names its project by the publishing device's id and key, resolved through rollup_projects, so WP-SV10's linking moves it with the project.

(g) Drafts stay on the device as coach records (3.5's coach/ row and 4.4's device row) and never reach the server.

(h) 8.9's eraser covers all four rows through WP-SV13's DeleteAccount calls, and the export holds them.

## Applied wording

DESIGN 4.4, four server rows after the row for HMACs of Hackatime names linked to a program:

| Where | Data | Kept |
|---|---|---|
| Server | Build days: each local day's credited seconds at levels A and B as D10 decides them, whether that made it a build day, and when that was decided; the weekly goal, each week's result, and the freezes earned, held and spent; the profile's time zone that sets the days' bounds, when it last changed and the zone it changed from, both replaced at the next change | Account lifetime |
| Server | Devlogs the teen published: the Markdown byte for byte as previewed, the `ai_assisted` label, the project it belongs to, when the server received it, and the random id the client gave it | Until the teen deletes it or asks the controller to, which deletes the row at once, so no page or read shows it again, a review page's story included (TE 7.2; TP B2); or account deletion. A submission keeps only its devlog windows' start and end times (7.5 step 2) |
| Server | Badges the teen chose to show, as codes from a fixed list, each with the random id the client gave it | Until the teen removes it or asks the controller to, which deletes its row at once (TE 7.2; TP B2); or account deletion |
| Server | Ids of devlogs the teen deleted and badges the teen removed, with the time of each removal, and no text, project or badge code | 30 days after the removal, so a computer that missed it can't publish the item again; or account deletion |

DESIGN 6.2 D13, the 'This design' cell's second sentence:

> A devlog's time is when the server received it, and windows start no earlier than the end of the latest window frozen by a submission on the project that wasn't withdrawn, so deleting or republishing a devlog never puts submitted time in a new window (7.5 step 2, 4.4).

DESIGN 7.5 step 2, after its first sentence:

> For a list of devlog windows, `W` is each window's start and end, so a devlog deleted or published later moves no frozen window (D13, 4.4).

DESIGN 7.7 item 9, at its end:

> A devlog the teen deleted is gone from it, as from every page (4.4).

DESIGN 4.3, the publishing row:

| From, to | When | Exactly what | Never in it |
|---|---|---|---|
| Client to `pairtimed`, publishing | Only when the teen clicks Publish, or deletes or removes something published | Devlog Markdown exactly as previewed, with its `ai_assisted` label and the key of the project the preview names; a project's display name and repo URL when made public; badge codes; a club join code. Each devlog and badge carries a random id the client made, and a delete or removal names only what it takes down | Any content the preview didn't show |

DESIGN 4.2, the What cell of the '3. Public' row:

> Profile aggregates, projects the teen made public, forge-verified commits of public repos, published devlogs, badges the teen chose

DESIGN 3.10, the Teen layer row:

| Component | Endpoints | Notes |
|---|---|---|
| Teen layer | `POST/GET/DELETE /api/v1/devlogs`, `POST/GET/DELETE /api/v1/badges`, `GET /api/v1/clubs/:id/board` | Devlog Markdown the teen published, `ai_assisted`; badges the teen chose, as codes; build days; club boards over 90 days, visible to signed-in members, opt-in (TE 3.8) |

## Not covered here

- A public project's display name and repo URL, and club memberships. They get 4.4 rows with the WPs that store them (WP-SV9 or WP-SV10, and the club-board WP).
- 7.5 step 1 refuses 'the same project and window to another program', while 9.2 and 9.7 say the server rejects overlapping submissions. This matters for explicit windows whatever devlogs do, so WP-SB1 raises it if the gap still stands.
- No WP yet builds the client's badge choice. Whichever does sends each badge with a random id, as WP-CO4 does for devlogs.

## For Karim

Nothing waits on you.
