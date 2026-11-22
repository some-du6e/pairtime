# Relayed: T3 Code messages nobody typed

Mateo, 16, runs T3 Code on a Linux box at home, where Claude Code works on his `bus-times` web app. On Saturday afternoon he drives it from T3's web client on a laptop at the library. pairtime runs on the box. The box has no desktop, so pairtime samples no presence there, and every Claude Code session T3's server `t3 serve` starts has no TTY.

T3 stores every message in this case as a user-role message with `createdBy` `user`. Mateo typed two of them. The rest come in three kinds:

- **Scheduled.** A task Mateo made in T3's UI fires every 10 minutes into the checks thread, and a webhook task fires twice into the deploys thread. Both carry `scheduledTaskId`, so the T3 reader records them with `declared` `cron`, and rule 1 makes their prompts `scheduled`.
- **Unwitnessed.** T3's web composer sends a `/compact` marked `idleCompaction` just before Mateo's first message. Ines, a friend Mateo let into the review thread as a guest, writes a message that he sends on, marked `context.guest`. Neither mark lets a message witness a prompt, so the reader writes nothing for them, and rule 4 makes their prompts `headless`.
- **Cross-session.** An agent in a thread of the T3 environment on Mateo's laptop sends the review thread a message marked `linkedSender`. The reader records it with `declared` `cross_session`, and rule 3 makes its prompt a cross-session `subagent`.

Mateo's two messages are relayed (SM 2.2 rule 9). The case credits 270 s, all from those two, at level C with `via_relay`. Nothing else earns a second.

No SM section describes this case. Every number below comes from SM 2.2 rules 1, 3, 4 and 9, 2.3, 2.4 and 5.2 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed. The box's hook records and the T3 reader's records, in the order the agent wrote them. Every message text is invented, and only its length reaches the records |
| Rules | SM 2.2 rules 1, 3, 4 and 9 and the relayed bullets under them, 2.3's relayed row, 2.4's relayed reading, 5.1's minute bucket, 5.2's codes for a slot whose presence is unknown, and SM 8's `RELAY_MATCH_S`, `RELAY_SEEN_LAG_S`, `COMPOSE_CPS` and `UNKNOWN_BUCKET_MAX_S`. DESIGN 3.3 and 5.1 for when and what the T3 reader reads |
| Date and zone | Saturday 2026-11-28, America/New_York, UTC−05:00. 16:00 local is 21:00Z |
| Evidence | Level C, label `via_relay`, witness `witnessed`. No `os.presence` or `os.focus` record exists, so every slot's presence is `unknown` |
| Window | 16:00:00 to 18:00:00. One project, four T3 threads, each with its own Claude Code session. T3's server starts every session with `entrypoint` `unknown`, `parent_class` `agent_app` and no TTY |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision |

## The threads

| Thread | Session started | What writes to it |
|---|---|---|
| checks | 16:00:00, for the task's first firing | The task: every 10 minutes from 16:00, the same 150-character prompt |
| deploys | 16:27:00, for the webhook's first firing | The webhook task: when Mateo's CI posts a finished preview deploy, T3 renders the request into a prompt |
| main | 13:02:14 | Mateo himself. The thread's earlier turns ended before 14:00 and aren't in the case. Nothing in the window reads their output, and M1 comes hours after their messages, so they cap nothing |
| review | 17:14:00, for Ines's message | Ines's message, and the laptop thread's |

## The records

| Message | Sent (`sent_at`) | Characters | T3's marks | Relay record | Its run's prompt | Turn | Origin |
|---|---|---|---|---|---|---|---|
| C1 to C12, the task | 16:00:00, then every 10 minutes to 17:50:00 | 150 | `scheduledTaskId` | 3 s after each stamp, `cron`, names the prompt | 1 s after each stamp | 50 s, from the prompt | `scheduled`, rule 1 |
| W1, the webhook | 16:27:00 | 228 | `scheduledTaskId` | 16:27:03, `cron`, names the prompt | 16:27:01 | to 16:28:31 | `scheduled`, rule 1 |
| K, the `/compact` | 16:44:00 | 8 | `idleCompaction` `onReturn` | none | 16:44:01 | to 16:44:40 | `headless`, rule 4 |
| M1, Mateo's | 16:44:00 | 135 | none | 16:44:03, `none`, before its prompt | 16:44:41, which names the record | to 16:48:15 | `relayed`, rule 9 |
| M2, Mateo's | 16:55:00 | 90 | none | 16:55:03, `none`, names the prompt | 16:55:01 | to 16:57:30 | `relayed`, rule 9 |
| G, Ines's | 17:14:00 | 133 | `context.guest` | none: the reader's query never selects it | 17:14:01 | to 17:14:56 | `headless`, rule 4 |
| L, the laptop thread's | 17:25:00 | 174 | `linkedSender` | 17:25:03, `cross_session`, names the prompt | 17:25:01 | to 17:26:31 | `subagent`, rule 3 |
| W2, the webhook | 17:30:20 | 231 | `scheduledTaskId` | 17:30:23, `cron`, names the prompt | 17:30:21 | to 17:31:51 | `scheduled`, rule 1 |

Each relay record comes 3 s after its message's stamp, inside `RELAY_SEEN_LAG_S` = 60 s. Every prompt but M1's comes 1 s after its stamp. T3's composer sends the `/compact` and then M1, both stamped 16:44:00, and T3 runs M1 only once the compaction's run has ended. So M1's prompt comes 41 s after its stamp, inside `RELAY_MATCH_S` = 600 s. The `/compact`'s prompt at 16:44:01 comes from a session T3's server started, so the reader reads T3's store at once (DESIGN 3.3). It skips the `/compact`'s row, which carries `idleCompaction`, and writes M1's record at 16:44:03. That is before M1's prompt, so the prompt carries the link (SM 2.2). G's 133 characters include the prefix T3 puts on a guest's message the owner sends on, "Message from guest Ines, sent by the owner:".

M1's lead is 135 / 1.5 = 90 s and M2's 90 / 1.5 = 60 s, each ending at its stamp. M1 is the main thread's first message in the case, and M2 comes 660 s after it, so the cap at the seconds since the thread's previous message binds neither. M1's turn fills 20 + 15 + 60 + 45 + 25 = 165 s: the 20 s base at pickup, 45 words, an 8-line edit, a 6-line edit and 75 words. The 120 s cap holds that to 120. M2's turn fills 20 + 10 + 30 + 20 = 80 s.

| Stop | Next relayed message sent after it | Gap | Next turn in the session | Reads |
|---|---|---|---|---|
| K, 16:44:40 | M2, 16:55:00 | 620 s | M1, 16:44:41 | nothing: more than 600 s. M1 was sent before the stop, so it vouches for nothing here |
| M1, 16:48:15 | M2, 16:55:00 | 405 s | M2, 16:55:01 | 120 s from the stop, and then the cap holds back the other 45 s |
| M2, 16:57:30 | none in the window | | | nothing |
| every stop in checks, deploys and review | none: no relayed message comes in those threads | | | nothing |

## Credit, minute by minute

Every slot's presence is unknown, so an uncredited second is `X.bucket_cap` where only the 120 s cap kept the main session from reading, `X.agent_only` while a person's turn runs, `X.automated` while only automated turns do, and `X.no_anchor` otherwise (SM 5.2). No automated turn runs during Mateo's turns, so the running seconds left uncredited are `X.agent_only` in his turns and `X.automated` in all the others.

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 16:00 | 0 | none | The task fires at 16:00:00, and T3 starts the checks session for it. C1's prompt reaches the agent at 16:00:01 and its `cron` record follows at 16:00:03, so rule 1 makes it `scheduled`. C1 runs until 16:00:51: `X.no_anchor` 1 + 9, `X.automated` 50 |
| 16:01-16:09 | 0 | none | No credit and no agent, so no bucket |
| 16:10 | 0 | none | C2, as C1: `X.no_anchor` 10, `X.automated` 50 |
| 16:11-16:19 | 0 | none | No credit and no agent |
| 16:20 | 0 | none | C3: `X.no_anchor` 10, `X.automated` 50 |
| 16:21-16:26 | 0 | none | No credit and no agent |
| 16:27 | 0 | none | The webhook fires at 16:27:00. W1's prompt, `scheduled` by its `cron` record, runs from 16:27:01: `X.no_anchor` 1, `X.automated` 59 |
| 16:28 | 0 | none | W1 runs until 16:28:31: `X.automated` 31, `X.no_anchor` 29 |
| 16:29 | 0 | none | No credit and no agent |
| 16:30 | 0 | none | C4: `X.no_anchor` 10, `X.automated` 50 |
| 16:31-16:39 | 0 | none | No credit and no agent |
| 16:40 | 0 | none | C5: `X.no_anchor` 10, `X.automated` 50 |
| 16:41 | 0 | none | No credit and no agent |
| 16:42 | 30 | `P` 30 | `X.no_anchor` 30, then M1's lead, 135 / 1.5 = 90 s, from 16:42:30 |
| 16:43 | 60 | `P` 60 | M1's lead runs to its stamp at 16:44:00 |
| 16:44 | 0 | none | 16:44:00 is in neither the lead nor a turn: `X.no_anchor` 1. The `/compact`'s prompt has no relay record, so rule 4 makes it `headless`, and its turn runs from 16:44:01 to 16:44:40: `X.automated` 39. 16:44:40 falls between the turns: `X.no_anchor` 1. M1's turn runs from 16:44:41: `X.agent_only` 19 |
| 16:45-16:47 | 0 | none | M1's turn runs, and a relayed session reads only after a stop: `X.agent_only` 3 × 60 |
| 16:48 | 45 | `READ` 45 | `X.agent_only` 15 until M1's turn stops at 16:48:15. M2 comes 405 s after the stop, so the session reads from it |
| 16:49 | 60 | `READ` 60 | The same reading |
| 16:50 | 15 | `READ` 15 | The 120 s end at 16:50:15: 45 + 60 + 15. The 600 s cap would have read the other 45 s of the 165, so they are `X.bucket_cap` 45. C6 runs in the checks session from 16:50:01 to 16:50:51, but `X.bucket_cap` comes before `X.automated` |
| 16:51-16:53 | 0 | none | The main session's reading span runs until M2 is sent, but nothing is left to read and no agent runs, so no bucket |
| 16:54 | 60 | `P` 60 | M2's lead, 90 / 1.5 = 60 s, from 16:54:00 to its stamp. M1 came 660 s earlier, so the cap at the seconds since the thread's previous message doesn't bind |
| 16:55 | 0 | none | 16:55:00 is in neither the lead nor a turn: `X.no_anchor` 1. M2's turn runs from 16:55:01: `X.agent_only` 59 |
| 16:56 | 0 | none | M2's turn runs: `X.agent_only` 60 |
| 16:57 | 0 | none | M2's turn runs until 16:57:30: `X.agent_only` 30. No relayed message follows it in the window, so nothing reads its 80 s: `X.no_anchor` 30 |
| 16:58-16:59 | 0 | none | No credit and no agent |
| 17:00 | 0 | none | C7: `X.no_anchor` 10, `X.automated` 50 |
| 17:01-17:09 | 0 | none | No credit and no agent |
| 17:10 | 0 | none | C8: `X.no_anchor` 10, `X.automated` 50 |
| 17:11-17:13 | 0 | none | No credit and no agent |
| 17:14 | 0 | none | Ines's message reaches the agent at 17:14:01 with no relay record, so rule 4 makes it `headless`. Its turn runs until 17:14:56: `X.no_anchor` 1 + 4, `X.automated` 55 |
| 17:15-17:19 | 0 | none | No credit and no agent |
| 17:20 | 0 | none | C9: `X.no_anchor` 10, `X.automated` 50 |
| 17:21-17:24 | 0 | none | No credit and no agent |
| 17:25 | 0 | none | The laptop thread's message reaches the agent at 17:25:01, and its `cross_session` record makes it a cross-session `subagent` (rule 3): `X.no_anchor` 1, `X.automated` 59 |
| 17:26 | 0 | none | Its turn runs until 17:26:31: `X.automated` 31, `X.no_anchor` 29 |
| 17:27-17:29 | 0 | none | No credit and no agent |
| 17:30 | 0 | none | C10 runs from 17:30:01 to 17:30:51 and W2 from 17:30:21, so an agent runs from 17:30:01 to the end of the minute, each second once: `X.no_anchor` 1, `X.automated` 59 |
| 17:31 | 0 | none | W2 runs until 17:31:51: `X.automated` 51, `X.no_anchor` 9 |
| 17:32-17:39 | 0 | none | No credit and no agent |
| 17:40 | 0 | none | C11: `X.no_anchor` 10, `X.automated` 50 |
| 17:41-17:49 | 0 | none | No credit and no agent |
| 17:50 | 0 | none | C12: `X.no_anchor` 10, `X.automated` 50 |
| 17:51-17:59 | 0 | none | No credit and no agent |

## Totals

`agent_running_s` adds up the turns. The task's twelve run 50 s each, W1 90, the compaction 39, M1 214, M2 149, Ines's 55, the laptop thread's 90 and W2 90, and C10 and W2 share 30 s. `X.automated` is the automated turns' seconds less C6's 50, which the reading and the cap take.

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 270 | 30 + 60 + 45 + 60 + 15 + 60 |
| `P` | 150 | 90 + 60 |
| `READ` | 120 | 45 + 60 + 15 |
| `level_c_s` | 270 | 150 + 120 |
| `via_relay_s` | 270 | 150 + 120 |
| `agent_running_s` | 1,297 | 12 × 50 + 90 + 39 + 214 + 149 + 55 + 90 + 90 − 30 |
| `X.agent_only` | 363 | 214 + 149 |
| `X.automated` | 884 | 11 × 50 + 90 + 39 + 55 + 90 + 90 − 30 |
| `X.bucket_cap` | 45 | 165 − 120 |
| `X.no_anchor` | 238 | 10 × 10 + 1 + 29 + 30 + 1 + 1 + 1 + 30 + 1 + 4 + 1 + 29 + 1 + 9 |
| `relayed_prompts` | 2 | 1 + 1 |
| `scheduled_prompts` | 14 | 12 + 2 |
| `headless_prompts` | 2 | 1 + 1 |
| `cross_session_prompts` | 1 | 1 |

## Readings the case relies on

- **`createdBy` `user` decides nothing.** Every message here has it. The rest of T3's record decides: `scheduledTaskId` on the task's and the webhook's firings, `linkedSender` on the laptop thread's message, `idleCompaction` on the `/compact`, `context.guest` on Ines's message (SM 2.2, "What witnesses a prompt").
- **The app's declarations work as the harness's do.** The hook's own record of each firing has `origin_declared` `none` and no pending cron, since Claude Code can't know that T3's scheduler fired it. The relay record's `cron` is what brings in rule 1, and `cross_session` what brings in rule 3 (SM 2.2, "The app's own declarations").
- **No record, no witness.** The reader turns the `/compact`'s row away for its `idleCompaction`, and its query's guest test never selects Ines's message (DESIGN 5.1). Rule 4 takes both prompts, as it takes any prompt of a session T3's server starts that nothing witnesses.
- **Pairing follows T3's runs, not the clock.** The `/compact`'s prompt at 16:44:01 is the main session's first after M1's stamp, but it belongs to the compaction's run. M1 pairs with its own run's prompt at 16:44:41 (SM 2.2, "Pairing"). That is why the case assumes Claude Code fires `UserPromptSubmit` for the `/compact`: it is the stricter reading. Whether it does is unverified, and the case credits the same either way. The case records the compaction as a prompt and a stop and nothing else.
- **M1 is `P`.** SM 2.3 gives a relayed anchor `P`, `ANS` or `GO` by class, never `Q`. T3 holds M1 until the compaction's run ends, but M1 reaches the agent as its own run's prompt, not as a message the harness queued (SM 1.2's `agent.queue`). So it takes its class's code, as [t3-queued](../t3-queued/)'s M2 does.
- **A message sent before a stop doesn't vouch for its reading.** M1 was sent before the compaction's stop, so only M2 could open reading after that stop, and M2 comes 620 s later (SM 2.4).
- **Reading is per session.** The checks, deploys and review sessions fill their buckets with every turn's output, but no relayed message comes in their threads. Mateo's messages vouch only for the main session's reading, since a relayed anchor is no human anchor for another session's reading (SM 2.3). C6 neither ends nor pauses the main session's reading, which ends only when the thread's next relayed message is sent or the main session's next turn starts (SM 2.4).
- **The cap's code comes first.** In 16:50, C6 runs while the 120 s cap keeps 45 s of M1's output unread. SM 5.2 puts `X.bucket_cap` before `X.automated`.
- **No bucket without credit or an agent.** From 16:51 to 16:53 the main session's reading span is still open, but its bucket is empty and no agent runs, so those minutes have no bucket (SM 5.1).
- **Two turns, one clock.** C10 and W2 run together from 17:30:21 to 17:30:51, and `agent_running_s` counts each of those seconds once, as any second an agent turn runs in any project (SM 5.1).
- **Two link directions.** M1's record came before its prompt, so the prompt carries `relay`. Every other record came after its prompt and names it in `prompt`.
