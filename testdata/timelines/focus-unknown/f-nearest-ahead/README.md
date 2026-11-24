# Focus unknown (f): the nearer prompt lies ahead

Lee, 16, runs Claude Code in two terminals on Ubuntu's GNOME on Wayland, without pairtime's extension: one session for `game` and one for `notes`. pairtime can't see which terminal is in front, so every slot's focus is unknown and both projects' windows and buckets compete for every second (SM 2.5, 3.3 rule 1). Buckets of the two projects go to "the project whose human anchor qualifying the drain (2.4) is nearest the second, the earlier anchor on a tie, then the lower `project_key`" (SM 2.5), and under D-split-slots a slot of unknown focus keeps the project of its first credited second. From 14:04:45 to 14:06:45 both buckets still hold output, and game's next prompt, B1 at 14:09:01, is nearer than notes's last one, A1 at 14:00:00, so the eight slots go to game, whose anchor lies ahead. A rule by the most recent human prompt would give them to notes. 860 of the 900 s are credited, 350 to `notes` and 510 to `game`, all level C with `focus_unknown`.

| | |
|---|---|
| Source | WP-FX8's (f), for follow-up triage item 90 (docs/PLAN.md, WP-SC8). Invented |
| Rules | SM 2.5's focus-unknown paragraph and D-split-slots, with SM 2.2 to 2.4, 3.3 and 5.2 and the parameters of SM 8 |
| Date and zone | Saturday 2026-11-28, Europe/Berlin, UTC+01:00. 13:55 local is 12:55Z |
| OS and evidence | Linux, GNOME on Wayland without pairtime's extension. `idle_keys_s` is null in every sample. Every credited second is level C with `focus_unknown`. Witnessed |
| Window | 13:55:00 to 14:10:00. Records from 13:50:00 set up the two sessions, and samples run to 14:10:15 |
| Projects | `game` and `notes`, one Claude Code session each, in two terminals |
| Errata | [split slots](../../../../docs/errata/2026-11-24-sm-2.5-split-slots.md), decided 2026-11-24 as D-split-slots, for 14:00 and 14:04 |

## The events

| Turn | Prompt | Typed | Lead | Runs | Output |
|---|---|---|---|---|---|
| B0, `game` | 13:55:00 | 60 | 120 + 40 = 160 s, from 13:52:20 | to 13:56:30 | 30 words at 13:55:30, a 60-line edit at 13:56:00, a 60-word summary at the stop |
| A1, `notes` | 14:00:00 | 45 | 120 + 30 = 150 s, from 13:57:30 | to 14:03:00 | 45 words at 14:01:00, a 40-line edit at 14:02:00, a 60-word summary at the stop |
| B1, `game` | 14:09:01 | 24 | 120 + 16 = 136 s, from 14:06:45 | to 14:09:50 | 30 words at 14:09:20, a 24-line edit at 14:09:30, a 30-word summary at the stop |

Both sessions start before the window, `game`'s at 13:50:00 and `notes`'s at 13:50:40, with `permission_mode` `acceptEdits`, so nothing asks permission. The first SessionStart hook makes a project context live, and the agent records its one `os.focus`, `app_class` `unknown`. Lee is at the keyboard throughout: she types each prompt, scrolls every 5 to 9 s while she reads, and no silence is longer than 12 s. A1 and B1 are 541 s apart, an odd number of seconds, so their midpoint, 14:04:30.5, is the middle of a second (below). Their typed lengths put both leads' starts, 13:57:30 and 14:06:45, on a slot's first second, so no lead starts inside a slot another project holds.

Every prompt passes SM 2.2 rule 6 on unknown focus: `human_typed`, `substantive`, level C with `focus_unknown`. Every fill is a whole number of seconds: words / 3 for prose, lines × 7.5 for an edit.

## Credit, minute by minute

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 13:55 | 30 | game SUP 30 | B0 is sent at 13:55:00 and resets game's bucket to 20 s: SUP to 13:55:20. Its 30 words add 30 / 3 = 10 s at 13:55:30: SUP to 13:55:40. notes has no output yet. While B0 runs with nothing to read, SM 5.2's turn step reads every project's running turns, since focus is unknown: `X.agent_only` 10 + 20 = 30 |
| 13:56 | 60 | game SUP 30, game READ 30 | game's 60-line edit lands at 13:56:00 and adds 60 × 7.5 = 450 s. B0 stops at 13:56:30, and its summary adds 60 / 3 = 20 s. Only game has output: SUP while B0 runs, then READ |
| 13:57 | 60 | game READ 30, notes P 30 | game reads to 13:57:30. There A1's lead begins, on a slot's first second, and beats game's bucket (SM 3.3 rule 2) |
| 13:58 | 60 | notes P 60 | A1's lead |
| 13:59 | 60 | notes P 60 | A1's lead; its typing part, 45 / 1.5 = 30 s, from 13:59:30 |
| 14:00 | 50 | notes SUP 20, game READ 30 | A1 is sent at 14:00:00 and resets notes's bucket to 20 s. game's bucket holds 500 − 120 = 380 s. Both buckets have output, and A1, 0 to 19 s back, is nearer than B0, 300 to 319 s back, or B1, 541 to 522 s ahead, so notes drains its 20 s, to 14:00:20. The slot from 14:00:15 is notes's by its first second (D-split-slots), so from 14:00:20 game's bucket can't take its seconds, and A1 runs with nothing of notes's to read: `X.agent_only` 10. From 14:00:30, a new slot, only game's bucket has output: READ |
| 14:01 | 60 | notes SUP 15, game READ 45 | A1's 45 words add 45 / 3 = 15 s at 14:01:00. A1 is nearer, so notes drains them, to 14:01:15. From there only game's bucket has output again: READ. game has drained 30 + 45 = 75 s since 14:00:00 |
| 14:02 | 60 | notes SUP 60 | notes's 40-line edit lands at 14:02:00 and adds 40 × 7.5 = 300 s. A1, 120 to 179 s back, is nearer than B0, 420 to 479 s back, or B1, 421 to 362 s ahead |
| 14:03 | 60 | notes READ 60 | A1 stops at 14:03:00, and its summary adds 60 / 3 = 20 s: READ |
| 14:04 | 60 | notes READ 45, game READ 15 | At 14:04:30, the first second of its slot, A1 is 270 s back and B1 271 s ahead, so notes reads, and the slot is notes's (D-split-slots), though from 14:04:31 B1 is the nearer. At 14:04:45, a new slot, B1 is 256 s ahead and A1 285 s back, so game reads. Its bucket holds 380 − 75 = 305 s, and notes's 20 + 15 + 300 + 20 − (20 + 15 + 60 + 105) = 155 |
| 14:05 | 60 | game READ 60 | At each slot's first second B1 ahead is nearer than A1 behind: 241, 226, 211 and 196 s against 300, 315, 330 and 345 |
| 14:06 | 60 | game READ 45, game P 15 | game reads to 14:06:45, 120 s since 14:04:45, which leaves 305 − 120 = 185 s in its bucket. There B1's lead begins, on a slot's first second, and beats notes's bucket (SM 3.3 rule 2) |
| 14:07 | 60 | game P 60 | B1's lead |
| 14:08 | 60 | game P 60 | B1's lead; its typing part, 24 / 1.5 = 16 s, from 14:08:45 |
| 14:09 | 60 | game P 1, game SUP 49, game READ 10 | B1 is sent at 14:09:01 and resets game's bucket to 20 s, so the 185 s it held are never read. 30 words add 10 s at 14:09:20, and a 24-line edit adds 24 × 7.5 = 180 s at 14:09:30. B1, 0 to 58 s back, is nearer than A1, 541 to 599 s back: SUP while B1 runs, to 14:09:50, then READ |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 860 | 30 + 50 + 13 × 60 |
| `notes` | 350 | 30 + 60 + 60 + 20 + 15 + 60 + 60 + 45 |
| `game` | 510 | 30 + 30 + 30 + 30 + 30 + 45 + 15 + 60 + 45 + 15 + 60 + 60 + 1 + 49 + 10 |
| `P` | 286 | 150 + 136 |
| `SUP` | 204 | 30 + 30 + 20 + 15 + 60 + 49 |
| `READ` | 370 | 30 + 30 + 30 + 45 + 60 + 45 + 15 + 60 + 45 + 10 |
| `X.agent_only` | 40 | 30 + 10 |
| `agent_only_s` | 40 | (90 − 60) + (180 − 95 − 75) |
| `agent_running_s.p_fca05b11bce0` | 180 | 180 |
| `agent_only_s.p_fca05b11bce0` | 85 | 180 − (20 + 15 + 60) |
| `agent_running_s.p_3815ea4d3197` | 139 | 90 + 49 |
| `agent_only_s.p_3815ea4d3197` | 30 | 90 − (20 + 10 + 30) |
| `contested_buckets_s` | 379 | 20 + 15 + 165 + 120 + 59 |
| `level_c_s` | 860 | 860 |
| `focus_unknown_s` | 860 | 860 |
| `level_b_s` | 0 | 0 |
| `ai_credited_s` | 860 | 286 + 204 + 370 |

`p_fca05b11bce0` is `notes` and `p_3815ea4d3197` is `game`.

- **`agent_only_s`** is SM 5.3's count across every project: "seconds in agent turns that nothing credited". B0 ran 90 s, and game was credited 60 of them. A1 ran 180 s, notes was credited 95 of them and game 75, while A1 ran: 30 to 14:01:00 and 45 to 14:02:00. B1 ran 49 s, all credited.
- **`agent_running_s.<p>` and `agent_only_s.<p>`** are D-bucket-agent-only's per-project numbers, as the 6.3 fixture writes them: the seconds a turn of the project ran, and those of them not credited to that project. A1 ran 180 s, and notes was credited 95, so 85. B0 ran 90 s with game credited 60, and B1 49 with game credited all of them, so 30.
- **`contested_buckets_s`** is the seconds both buckets wanted. notes takes 14:00:00 to 14:00:20, 14:01:00 to 14:01:15 and 14:02:00 to 14:04:45, 20 + 15 + 165 = 200, and game 14:04:45 to 14:06:45 and 14:09:01 to 14:10:00, 120 + 59 = 179. The seconds where one project's lead beat the other's bucket, 13:57:30 to 14:00:00 and 14:06:45 to 14:09:01, count in neither.

## The slots the case is about

Both buckets hold output, with no lead over the seconds, from 14:02:00, when notes's edit lands, to 14:06:45, when B1's lead begins. At the first second of each slot, the two readings pick these projects. No lead decides the slots before 14:06:45, since A1's ended at 14:00:00; the last row is where B1's lead takes over.

| Slot | A1 back | B1 ahead | Nearest qualifying anchor (SM 2.5) | Most recent human prompt | notes holds | game holds |
|---|---|---|---|---|---|---|
| 14:04:00 | 240 | 301 | A1: notes READ 15 | A1: notes READ 15 | 200 | 305 |
| 14:04:15 | 255 | 286 | A1: notes READ 15 | A1: notes READ 15 | 185 | 305 |
| 14:04:30 | 270 | 271 | A1: notes READ 15 | A1: notes READ 15 | 170 | 305 |
| 14:04:45 | 285 | 256 | B1: game READ 15 | A1: notes READ 15 | 155 | 305 |
| 14:05:00 | 300 | 241 | B1: game READ 15 | A1: notes READ 15 | 155 | 290 |
| 14:05:15 | 315 | 226 | B1: game READ 15 | A1: notes READ 15 | 155 | 275 |
| 14:05:30 | 330 | 211 | B1: game READ 15 | A1: notes READ 15 | 155 | 260 |
| 14:05:45 | 345 | 196 | B1: game READ 15 | A1: notes READ 15 | 155 | 245 |
| 14:06:00 | 360 | 181 | B1: game READ 15 | A1: notes READ 15 | 155 | 230 |
| 14:06:15 | 375 | 166 | B1: game READ 15 | A1: notes READ 15 | 155 | 215 |
| 14:06:30 | 390 | 151 | B1: game READ 15 | A1: notes READ 15 | 155 | 200 |
| 14:06:45 | 405 | 136 | B1's lead: game P 15 | B1's lead: game P 15 | 155 | 185 |

The buckets' levels are the nearest-anchor reading's, at each slot's first second. Read by the most recent human prompt instead, notes would drain 15 s a slot from 14:04:45 and game none, so the eight slots from 14:04:45 to 14:06:45 would be notes READ 120, and notes would get 470 s and game 390, not 350 and 510. Every other second is the same under both readings, since everywhere else the nearer anchor is also the most recent one, or only one bucket has output.

## Readings the case relies on

- **The nearest anchor can lie ahead.** SM 2.4 qualifies a drain by a human anchor "within `READ_HORIZON_S` = 600 s ... before or after", and SM 2.5 takes the nearest qualifying one. From 14:04:45, B1, sent five minutes later, is nearer than A1, so game's bucket drains while notes's still holds 155 s. A rule by the most recent human prompt, as SM 2.8's lookback has, would drain notes's instead (above). This is the case (d)'s README says it lacks: a slot starts while both buckets hold output after the two anchors' midpoint.
- **Nearest, either way it is measured.** SM 2.5 doesn't say whether "nearest the second" measures from the second's start or its middle. A1 and B1 are 541 s apart, an odd number of seconds, so the two readings agree on every second: measured from the start, 14:04:30 is 270 s from A1 and 271 from B1; measured from the middle it is 270.5 from both, and "the earlier anchor on a tie" gives it to A1 too. SM 1.3 places a second in a span by its start, so the start is SM's own reading. Measured from the second's end, 14:04:31, B1 would be nearer and game would take the slot from 14:04:30, 15 s. gen.py checks both readings wherever the nearest anchor picks a slot's project, which under D-split-slots is at a slot's first credited second.
- **One project to a slot (D-split-slots).** The project whose candidate credits a slot's first credited second takes the slot. So notes keeps the slot from 14:00:15 after its bucket runs dry at 14:00:20, and the slot from 14:04:30 though B1 is nearer from 14:04:31. Read second by second, as SM 2.5 alone says, 14:00:20 to 14:00:30 would be game READ 10 and 14:04:31 to 14:04:45 game READ 14, so notes would get 336 s and game 534.
- **game's bucket outlasts notes's empty seconds.** Before notes's edit lands, its bucket is empty from 14:00:20 to 14:01:00 and from 14:01:15 to 14:02:00, and game's drains 30 + 45 = 75 s then. It still holds 305 s at 14:04:45, so both buckets have output in every slot the case is about.
- **An edit fills the bucket when it lands.** An `agent.tool` record fills at its `t`, which is its `t_end`, not its `t_start` (SM 6.1's "lands at 14:05:30", 6.2's "until the agent's 40-line edit lands at 07:41:50"). Filled at `t_start`, game's 60-line edit would land at 13:55:51.5 and drain from 13:55:52, so 13:55 would credit 8 s more game SUP and 8 s less `X.agent_only`, and game would get 518 s. notes's would land at 14:01:52.2, inside game's slot from 14:01:45, so it would move no credit, and `contested_buckets_s` would be 386, since both buckets would want 14:01:53 to 14:02:00. No slot the case is about would change.
- **SUP or READ is the drained session's.** A session's bucket drains as SUP while its own turn runs and READ after its own stop (SM 2.4), whatever the other project's turn is doing.
- **Leads before buckets.** Where a project's lead holds a second, it beats the other project's bucket (SM 3.3 rule 2): A1's from 13:57:30, B1's from 14:06:45.
- **No tie on `project_key`.** No two anchors share a time, so neither SM 2.5's last tie-break nor SM 3.3 rule 6 is reached. One session per project, so SM 3.3 rule 4's FIFO across sessions isn't either.
