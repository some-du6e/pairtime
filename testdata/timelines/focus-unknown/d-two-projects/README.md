# Focus unknown (d): two projects whose buckets compete

Ana, 17, runs Claude Code in two terminals on Fedora's GNOME on Wayland, without pairtime's extension: one session for `site` and one for `api`. pairtime can't see which terminal is in front, so every slot's focus is unknown and both projects' windows and buckets compete for every second (SM 2.5, 3.3 rule 1). SM 2.5 settles each contested second: leads of the two projects by SM 3.3 rule 3, and buckets of the two projects by "the project whose human anchor qualifying the drain (2.4) is nearest the second, the earlier anchor on a tie, then the lower `project_key`". 836 of the 900 s are credited, 351 to `site` and 485 to `api`, all level C with `focus_unknown`.

| | |
|---|---|
| Source | WP-FX8's (d) (docs/PLAN.md). Invented |
| Rules | SM 2.5's focus-unknown paragraph, with SM 2.2 to 2.4, 3.3 and 5.2 and the parameters of SM 8 |
| Date and zone | Friday 2026-11-27, America/Sao_Paulo, UTC−03:00. 16:00 local is 19:00Z |
| OS and evidence | Linux, GNOME on Wayland without pairtime's extension. `idle_keys_s` is null in every sample. Every credited second is level C with `focus_unknown`. Witnessed |
| Window | 16:00:00 to 16:15:00. Records from 15:57:00 set up the two sessions, and samples run to 16:15:15 |
| Projects | `site` and `api`, one Claude Code session each, in two terminals |
| Errata | [split slots](../../../../docs/errata/2026-11-24-sm-2.5-split-slots.md), for the two slots that credit both projects, in 16:03 and 16:08 |

## The events

| Turn | Prompt | Typed | Lead | Runs | Output |
|---|---|---|---|---|---|
| B1, `api` | 16:02:00 | 120 | 120 + 80 = 200 s, from 15:58:40 | to 16:05:30 | 60 words at 16:02:40, a 24-line edit at 16:03:50, a 120-word summary at the stop |
| A1, `site` | 16:03:00 | 60 | 120 + 40 = 160 s, from 16:00:20 | to 16:04:40 | 30 words at 16:03:20, a 30-line edit at 16:03:40, a 135-word summary at the stop |
| B2, `api` | 16:13:01 | 90 | 120 + 60 = 180 s, from 16:10:01 | to 16:14:31 | 45 words at 16:13:41, a 60-word summary at the stop |

Both sessions start before the window, `site`'s at 15:57:00 and `api`'s at 15:57:40, with `permission_mode` `acceptEdits`, so nothing asks permission. The first SessionStart hook makes a project context live, and the agent records its one `os.focus`, `app_class` `unknown`. Ana is at the keyboard throughout: she types each prompt, scrolls every 5 to 9 s while she reads, and no silence is longer than 13 s. B2 is sent one second past the minute, so that it is 601 s after A1, which keeps their midpoint off a whole second (below).

Every prompt passes SM 2.2 rule 6 on unknown focus: `human_typed`, `substantive`, level C with `focus_unknown`. Every fill is a whole number of seconds: words / 3 for prose, lines × 7.5 for an edit.

## Credit, minute by minute

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 16:00 | 60 | api P 60 | B1's lead covers the minute, and A1's lead wants 16:00:20 on as well. Focus is unknown, so both projects' leads survive SM 3.3 rule 1, and rule 3 gives each second to the lead of the next anchor at or after it: B1, at 16:02:00 |
| 16:01 | 60 | api P 60 | Rule 3 again, to 16:02:00 |
| 16:02 | 60 | site P 60 | B1 is sent at 16:02:00, and its turn resets api's bucket to 20 s; 60 words add 20 more at 16:02:40. A1's lead beats that bucket (SM 3.3 rule 2) |
| 16:03 | 60 | site SUP 50, api SUP 10 | A1 is sent at 16:03:00 and resets site's bucket to 20 s. Both buckets have output, and the nearest qualifying anchor decides (SM 2.5): A1, 0 to 60 s back, against B1, 60 to 120 s back. site drains its 20 s and the 10 s that 30 words add at 16:03:20, to 16:03:30. site's bucket is then empty until its edit at 16:03:40, so api's drains uncontested, 10 of its 40 s. The edit adds 30 × 7.5 = 225 s to site's, and A1 is still the nearer anchor: 20 + 10 + 20 |
| 16:04 | 60 | site SUP 40, site READ 20 | site drains. A1 stops at 16:04:40, so SUP becomes READ, and its summary adds 135 / 3 = 45 s. api's edit at 16:03:50 added 24 × 7.5 = 180 s, so api's bucket holds 30 + 180 = 210 s, unread |
| 16:05 | 60 | site READ 60 | B1 stops at 16:05:30, and its summary adds 120 / 3 = 40 s: api's holds 250. A1 is still nearer than B1 or B2 |
| 16:06 | 60 | site READ 60 | A1 is 180 to 240 s back, B1 240 to 300 s back and B2 421 to 362 s ahead |
| 16:07 | 60 | site READ 60 | A1 is nearer through 16:08:00 |
| 16:08 | 60 | site READ 1, api READ 59 | At 16:08:00 A1 is 300 s back and B2 301 s ahead, so site reads. At 16:08:01 B2 is 300 s ahead and A1 301 s back, so api reads. site has drained 20 + 10 + 261 = 291 of its 300 s and keeps 9 |
| 16:09 | 60 | api READ 60 | B2 is the nearer anchor |
| 16:10 | 60 | api P 59, api READ 1 | api reads to 16:10:01, 59 + 60 + 1 = 120 of its 250 s. Then B2's lead beats both buckets (SM 3.3 rule 2) |
| 16:11 | 60 | api P 60 | B2's lead |
| 16:12 | 60 | api P 60 | B2's lead; its typing part from 16:12:01 |
| 16:13 | 36 | api P 1, api SUP 35 | B2 is sent at 16:13:01 and resets api's bucket to 20 s, so the 130 s it held are never read: SUP to 16:13:21. site's 9 s don't qualify, since A1 is now more than 600 s back. Nothing is left to read while B2 runs, from 16:13:21 until 45 words add 15 s at 16:13:41: SUP to 16:13:56. Then nothing again. Focus is unknown, so SM 5.2's turn step reads every project's running turns: X.agent_only 20 + 4 = 24 |
| 16:14 | 20 | api READ 20 | B2 runs to 16:14:31 with nothing to read: X.agent_only 31. Its summary adds 20 s: READ to 16:14:51. Then no turn runs, and nothing either project could read qualifies: X.no_anchor 9 |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 836 | 13 × 60 + 36 + 20 |
| `site` | 351 | 60 + 50 + 40 + 20 + 60 + 60 + 60 + 1 |
| `api` | 485 | 60 + 60 + 10 + 59 + 60 + 59 + 1 + 60 + 60 + 1 + 35 + 20 |
| `P` | 360 | 120 + 60 + 180 |
| `SUP` | 135 | 50 + 40 + 10 + 35 |
| `READ` | 341 | 20 + 60 + 60 + 60 + 1 + 59 + 60 + 1 + 20 |
| `X.agent_only` | 55 | 24 + 31 |
| `X.no_anchor` | 9 | 9 |
| `agent_running_s` | 300 | 210 + 90 |
| `agent_only_s` | 55 | 300 − (210 + 35) |
| `contested_leads_s` | 100 | 120 − 20 |
| `contested_buckets_s` | 411 | 30 + 261 + 120 |
| `level_c_s` | 836 | 836 |
| `focus_unknown_s` | 836 | 836 |
| `level_b_s` | 0 | 0 |
| `ai_credited_s` | 836 | 360 + 135 + 341 |

`contested_leads_s` is the seconds both projects' leads wanted, 16:00:20 to 16:02:00, all B1's. `contested_buckets_s` is the seconds both buckets wanted: 16:03:00 to 16:03:30 and 16:03:40 to 16:08:01 for `site`, 30 + 261 = 291, and 16:08:01 to 16:10:01 for `api`, 120. The seconds where one project's lead beat the other's bucket, 16:02:00 to 16:03:00 and from 16:10:01, aren't counted in either. `agent_running_s` is B1's 210 s, which A1's 100 lie inside, and B2's 90.

## Readings the case relies on

- **Nearest, either way it is measured.** SM 2.5 doesn't say whether "nearest the second" measures from the second's start or its middle. B2 is 601 s after A1, an odd number of seconds, so the two readings agree on every second: measured from the start, 16:08:00 is 300 s from A1 and 301 from B2; measured from the middle it is 300.5 from both, and "the earlier anchor on a tie" gives it to A1 too. gen.py checks both readings on every contested second. A gap of an even number of seconds would put the two readings one second apart at the midpoint.
- **The nearest anchor can lie ahead.** SM 2.4 qualifies a drain by a human anchor "within `READ_HORIZON_S` = 600 s ... before or after", and SM 2.5 takes the nearest qualifying one, so from 16:08:01 B2, sent five minutes later, wins the reading for `api`. SM 2.8's lookback rule, "the one with the most recent human prompt", would leave the seconds with `site` until its bucket ran dry at 16:08:10, and 16:08 would credit `site` 10 and `api` 50. The case tells the two rules apart in those 9 s.
- **No tie on `project_key`.** No two anchors share a time, so neither SM 2.5's last tie-break nor SM 3.3 rule 6 is reached. One session per project, so SM 3.3 rule 4's FIFO across sessions isn't either.
- **Leads before buckets.** Where a project's lead holds a second, it beats the other project's bucket as it would its own (SM 3.3 rule 2): A1's lead from 16:02:00, B2's from 16:10:01.
- **Two slots credit two projects.** The slot from 16:03:30 credits `api` 10 s and `site` 5, and the slot from 16:08:00 `site` 1 and `api` 14. SM 5.1's minute bucket gives a slot one project, so it can't record them as they are, and SM doesn't say how it should. The case follows SM 2.5 as written, second by second, and `slots.json` gives each slot's credit by project rather than a `p`. The [split-slots erratum](../../../../docs/errata/2026-11-24-sm-2.5-split-slots.md) asks for a rule. If it carries over the ambiguous-anchors decision, where the project of a slot's first credited second takes the whole slot, `api` keeps the slot from 16:03:30 and `site` the slot from 16:08:00: 16:03 becomes `site` SUP 45 and `api` SUP 15, 16:08 `site` READ 15 and `api` READ 45, and `site` gets 360 s and `api` 476. Rows 16:03 and 16:08 name the erratum.
- **What the buckets leave.** B2's reset drops the 130 s `api`'s bucket still held (SM 2.4). `site`'s last 9 s stay in its bucket and are never read: from 16:13:01 no anchor of `site` is within 600 s.
