# Lookback: SM 6.1 from its transcript alone

Rowan's three hours of flow (SM 6.1) as pairtime would score them if he installed it afterwards and lookback read the afternoon from his Claude Code transcript. The events are those of [`6.1`](../../6.1/) with every OS, editor and git event removed. SM 2.8 scores what is left. Lookback credits 5,055 s, against the 10,095 s the live fixture credits, all of it level C and backfilled.

| | |
|---|---|
| Source | `testdata/timelines/6.1/events.jsonl`, harness events only. gen.py reads that file, so this fixture follows any change to it |
| Rules | SM 2.8, with SM 2.2 to 2.4 and the parameters of SM 8 where 2.8 doesn't change them |
| Date and zone | Saturday 2026-11-21, Asia/Kolkata, UTC+05:30, as in 6.1 |
| Evidence | Level C, witness `backfilled`, presence `unknown` in every slot |
| Window | 14:00:00 to 17:00:00. The session starts at 13:58:05.5 and isn't checked |
| Errata | [lookback](../../../../docs/errata/2026-11-19-sm-2.8-lookback.md): point 5 for the permission trail; point 4 covers the session's fields |

## What the transcript holds

One `session.start`, then fifteen turns. Each has a 300-character prompt at C+4:00, two 45-word messages, a permission asked at C+5:10 and answered at C+5:25, two 30-line edits, the test runs of cycles 9 to 11, and a 160-word summary with the stop at C+6:30. C is the cycle's start, 14:00 plus 12 minutes for each cycle before it. All of them arrive `via` `log`, because no hook ran before pairtime was installed.

The live fixture's other records are gone. Without presence samples every slot is `unknown`. Without editor heartbeats there are no `NAV` or `E` anchors, without focus no browser lookup, and without the `.git` watcher no commits. The drink in cycle 6 leaves no record at all, so lookback can't see it.

## Credit, minute by minute

Cycle 1:

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 14:00 | 20 | `P` 20 | The 14:04:00 prompt has 300 typed characters. SM 2.8 keeps only its typing time, 300 / 1.5 = 200 s, so its lead starts at 14:00:40. No stop comes before it, so nothing covers 14:00:00 to 14:00:40, and those 40 s are `X.no_anchor` (SM 5.2) |
| 14:01 | 60 | `P` 60 | The same lead |
| 14:02 | 60 | `P` 60 | The same lead |
| 14:03 | 60 | `P` 60 | The same lead, to the submit at 14:04:00. 20 + 60 + 60 + 60 = 200 |
| 14:04 | 0 | none | The agent runs. Lookback credits no reading while it runs, so the live fixture's `SUP` is gone. `X.agent_only` 60 |
| 14:05 | 25 | `PA` 25 | Asked at 14:05:10 and answered at 14:05:25. The 15 s wait is under the 60 s cap, and the 10 s trail follows: 15 + 10 = 25, from 14:05:10 to 14:05:35. `X.agent_only` 10 + 25 = 35 |
| 14:06 | 30 | `READ` 30 | The agent stops at 14:06:30. The next prompt comes at 14:16:00, 570 s later, inside 600 s, so the turn's output counts as reading from the stop. `X.agent_only` 30 before it |
| 14:07 | 60 | `READ` 60 | The turn filled its bucket with 20 + 15 + 15 + 225 + 225 + 53 1/3 = 553 1/3 s, and lookback keeps 120 of it |
| 14:08 | 30 | `READ` 30 | 30 + 60 + 30 = 120, the cap, reached at 14:08:30. The output would last until the next prompt's lead and only the cap keeps it from being read, so the other 30 s are `X.bucket_cap` (SM 5.2) |
| 14:09-14:11 | 0 | none | Nothing is credited and no agent runs, so there is no bucket |

Cycles 2 to 14 repeat cycle 1, each 12 minutes later. Each stop is followed by the next prompt 570 s later, cycle 6's included. Only the codes of the first minute differ. From cycle 2 on, its first 40 s lie between the stop before them and the prompt, so the cap cuts them, as it cuts 14:08's last 30 s. Cycle 15 has no prompt after it:

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 16:48 | 20 | `P` 20 | The 16:52:00 prompt's lead, 200 s from 16:48:40. The cap cuts the 40 s before it, as in cycles 2 to 14 |
| 16:49 | 60 | `P` 60 | The same lead |
| 16:50 | 60 | `P` 60 | The same lead |
| 16:51 | 60 | `P` 60 | The same lead, to the submit at 16:52:00 |
| 16:52 | 0 | none | The agent runs. `X.agent_only` 60 |
| 16:53 | 25 | `PA` 25 | 15 s wait + 10 s trail, from 16:53:10. `X.agent_only` 35 |
| 16:54 | 0 | none | The agent stops at 16:54:30. The transcript ends there, so no prompt follows and the turn's output is never read as far as the rules can tell. `X.agent_only` 30, then `X.no_anchor` 30 (SM 5.2) |
| 16:55-16:59 | 0 | none | No bucket |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `first_cycle_credited_s` | 345 | 20 + 60 + 60 + 60 + 25 + 30 + 60 + 30 |
| `last_cycle_credited_s` | 225 | 20 + 60 + 60 + 60 + 25 |
| `credited_s` | 5,055 | 14 × 345 + 225 |
| `P` | 3,000 | 15 × 200 |
| `PA` | 375 | 15 × 25 |
| `READ` | 1,680 | 14 × 120 |
| `agent_running_s` | 2,250 | 15 × 150 |
| `agent_only_s` | 1,875 | 15 × (150 − 25) |
| `X.agent_only` | 1,875 | 15 × 125 |
| `X.bucket_cap` | 980 | 14 × (30 + 40) |
| `X.no_anchor` | 70 | 40 + 30 |

All 5,055 s are AI-assisted (`P`, `PA`, `READ`). There is no hand work, because the hand work is in the records lookback doesn't have.

| | Live 6.1 | Lookback |
|---|---|---|
| Credited | 10,095 | 5,055 |
| `P` | 1,950 | 3,000 |
| `SUP` and `READ` | 3,640 | 1,680 |
| `PA` | 375 | 375 |
| `NAV`, `E`, `G`, `LOOKUP` | 4,130 | 0 |
| Agent-only | 375 | 1,875 |

`P` goes up. In the live fixture the heartbeat at C+1:50 is the next anchor for every second before it, so its lead takes those seconds from the prompt's (SM 3.3 rule 3), and the prompt keeps 130 s a cycle. Lookback has no heartbeat, so the prompt keeps its whole 200 s of typing time.

## Readings the rows rely on

- **Reading starts at the stop.** SM 2.8 credits reading "between a stop and a human prompt that follows within 600 s", and here the next prompt follows each stop 570 s later. Nothing else wants those seconds, so the bucket drains from the stop, one second per second, as SM 2.4 drains it, and the 120 s end at C+8:30.
- **The permission's trail stays.** SM 2.8 changes the permission answer's lead, its wait, to at most 60 s, and says nothing about its 10 s trail, so the trail is SM 2.3's. Without the trail each cycle would credit 15 s of `PA`, not 25, and the afternoon 4,905 s. That is the erratum's point 5.
- **Codes.** Uncredited seconds follow SM 5.2's order for unknown presence (SM 2.8). The 980 s between a stop and the next prompt, where only the 120 s cap stops the reading, are `X.bucket_cap`: 30 s after each of cycles 1 to 14's reading and the first 40 s of cycles 2 to 15. Seconds in an agent turn that nothing credits are `X.agent_only`, since a person started every turn. The other 70 s, cycle 1's first 40 and the 30 after cycle 15's stop, lie outside every window and reading span, so they are `X.no_anchor`.
- **The envelope.** Each record keeps the live record's time, session, project, turn and payload, with `source` and `via` set to `log`. `seq` starts at 1, because lookback runs at the agent's first start. `mono` and `boot_id` are the live fixture's: a transcript has no monotonic reading, and no lookback rule reads one.
- **`session.start`** keeps the live fields, `entrypoint` `cli`, `parent_class` `terminal_app` and a TTY, so SM 2.2 rule 4 sees an interactive session. A transcript may not record all of them. The erratum's point 4 asks what lookback does when it can't.
- **The same error.** Lookback has neither the 15:47:40 commit nor the 15:59:02 hand edit that reset the `AssertionError`'s count in the live fixture, so the count is 3 at cycle 11's prompt, 16:04:00, and the error is cleared only by the passing run at 16:06:20. That prompt has 300 typed characters and no pasted error, so it is `substantive`, which SM 4.2's fix-it rule never discounts, and cycle 11 keeps its 345 s.

Under DESIGN 6.2 D5, a program that opts in to level C would count at most min(credited, ai_logged / 3) of this afternoon, and only inside forge-verified commit intervals. The agent events replay to `ai_logged_s` 3,930, so that cap is 1,310 s (`expected.json`'s notes).
