# Relayed: a day of agents talking to each other

Noor, 17, runs T3 Code on a Linux server at home. Claude Code agents work there all day in T3 threads and hand each other work through T3's MCP tools. pairtime runs on the server, which has no desktop, so pairtime samples no presence there. T3's server, `t3 serve`, starts every Claude Code session through the Agent SDK, with `entrypoint` `unknown`, `parent_class` `agent_app` and no TTY, and no window hosts any of them.

On this Monday the agents send 192 prompts over 49 sessions. T3 stores each of their messages with `createdBy` `agent`, which fails its own test for a message a person sent, so pairtime's T3 reader writes no relay record for any of them. Nothing witnesses those prompts, and SM 2.2 rule 4 makes every one `headless`. None is an anchor, and every second their turns run is `X.automated`. Noor sends one message herself, from T3's web client on her laptop, to a thread T3 runs on a provider pairtime has no hooks for. The reader records it but can't tie its run to a session, so the record pairs with nothing and opens nothing (SM 2.2, 2.3). The case credits 0 s.

No SM section describes this case. Every number below comes from SM 2.2 rules 4 and 9, 2.3, 5.1 and 5.2 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed. The server's hook records and the T3 reader's one record, in the order the agent wrote them |
| Rules | SM 2.2 rule 4 and rule 9's witness and pairing, 2.3's paragraph on a relayed anchor waiting for its pairing, 5.1's minute bucket, 5.2's codes for a slot whose presence is unknown, and SM 8's `RELAY_MATCH_S`, `RELAY_SEEN_LAG_S` and `COMPOSE_CPS` |
| Date and zone | Monday 2026-11-23, America/Chicago, UTC−06:00. 00:00 local is 06:00Z |
| Evidence | No second is credited, so none has a level. Witness `witnessed`: the server uploads live. No `os.presence`, `os.focus` or `os.power` record exists, so every slot's presence is `unknown` |
| Window | 00:00:00 to 24:00:00 |
| Sessions | 49. The planner's starts at 00:00:28 and lasts the day. Each hour's builder and reviewer start at :01:10 and :01:40. Every `session.start` has `start_kind` `startup`, `entrypoint` `unknown`, `parent_class` `agent_app`, no TTY and `permission_mode` `bypassPermissions` |
| Projects | `agent-desk`, the planner's. `tidepool` in even hours and `kelp-cli` in odd ones, the builders' and reviewers' |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision |

## The records

The day repeats one hour 24 times, so each hour's arithmetic is one short sum. Each hour the planner takes an item, hands it to a new builder thread and a new reviewer thread, and files the builder's report once the reviewer approves. Times are past the hour.

| Turn | Session | Its message | Characters | Prompt | Stop | Runs |
|---|---|---|---|---|---|---|
| P.1 | planner | the hour's item | 420 | :00:30 | :02:00 | 90 |
| A.1 | builder | the task, from the planner | 1,380 | :01:12 | :11:12 | 600 |
| B.1 | reviewer | the brief, from the planner | 640 | :01:42 | :05:42 | 240 |
| B.2 | reviewer | the change, ready for review, from the builder | 510 | :11:08 | :15:08 | 240 |
| A.2 | builder | review notes, from the reviewer | 780 | :15:04 | :21:04 | 360 |
| B.3 | reviewer | the fix, from the builder | 330 | :21:00 | :23:00 | 120 |
| A.3 | builder | the approval, from the reviewer | 120 | :22:56 | :23:56 | 60 |
| P.2 | planner | the report, from the builder | 450 | :23:52 | :25:22 | 90 |

Which agent hands the planner the hour's item changes nothing here. T3 stores that message with `createdBy` `agent`, like every other message in the day.

The planner sends the builder its task and the reviewer its brief while P.1 still runs, and every later message reaches its agent 4 s before the turn that sent it stops. T3 starts the builder's and the reviewer's sessions 2 s before their first prompts. So an agent runs without a break from :00:30 to :25:22, and then nothing runs until the next hour. The eight turns run 90 + 600 + 240 + 240 + 360 + 120 + 60 + 90 = 1,800 s between them, but they overlap for 240 + 48 + 5 × 4 = 308 s. B.1 runs inside A.1, P.1's last 48 s overlap A.1's first, and each of the five later messages starts its turn 4 s before its sender's turn ends. `agent_running_s` counts a second once, however many turns run in it (SM 5.1), so each hour has 1,800 − 308 = 1,492 s of agent time, in the 26 minutes :00 to :25.

Each turn ends with one output at its stop. The builder edits a file and runs the tests in A.1 and A.2, the reviewer runs the tests in B.2 and B.3, and P.2 edits the planner's notes. Whatever that output puts in a session's bucket (SM 2.4), no session reads any of it. Reading needs presence and a human anchor, or a relayed anchor in the session, and the day has neither.

| Message | Sent (`sent_at`) | Typed | Relay record | Its run | Pairs |
|---|---|---|---|---|---|
| M1, Noor's | 14:59:00 | 90 | 14:59:04, `declared` `none`, `session_key` `none`, project `agent-desk` | on a provider pairtime has no hooks for, in no session pairtime sees | no |

The record comes 4 s after the stamp, inside `RELAY_SEEN_LAG_S`, and M1 passes T3's own test for a message a person sent. But a message witnesses a prompt only through its run's session (SM 2.2), and M1's run reached none that pairtime sees, so the record names no prompt and no prompt names it. Had M1 paired, its lead would have been its typing time, 90 / 1.5 = 60 s, from 14:58:00 to its stamp. Hour 15's P.1 reaches the planner at 15:00:30, in M1's project, 90 s after the stamp and inside `RELAY_MATCH_S`. Nothing ties the two, since a pairing comes from the app's own structure, never from time or text.

## Credit, minute by minute

Every slot's presence is unknown, so an uncredited second is `X.automated` while a turn runs, since every turn here is headless, and `X.no_anchor` otherwise (SM 5.2). No session reads, so no second is `X.bucket_cap`. Hour 14 has two rows, so that M1's minutes have one of their own.

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 00:00-00:59 | 0 | none | Buckets in the 26 minutes 00:00 to 00:25. The planner's session starts at 00:00:28, and an agent runs from P.1 at 00:00:30 until P.2 stops at 00:25:22: `X.automated` 30 + 24 × 60 + 22 = 1,492. `X.no_anchor` is the 30 s before P.1 and the 38 s after P.2: 68. 00:26 to 00:59 have no credit and no agent, so no bucket |
| 01:00-01:59 | 0 | none | As 00:00-00:59: 26 bucket minutes, `X.automated` 1,492, `X.no_anchor` 68 |
| 02:00-02:59 | 0 | none | As 00:00-00:59 |
| 03:00-03:59 | 0 | none | As 00:00-00:59 |
| 04:00-04:59 | 0 | none | As 00:00-00:59 |
| 05:00-05:59 | 0 | none | As 00:00-00:59 |
| 06:00-06:59 | 0 | none | As 00:00-00:59 |
| 07:00-07:59 | 0 | none | As 00:00-00:59 |
| 08:00-08:59 | 0 | none | As 00:00-00:59 |
| 09:00-09:59 | 0 | none | As 00:00-00:59 |
| 10:00-10:59 | 0 | none | As 00:00-00:59 |
| 11:00-11:59 | 0 | none | As 00:00-00:59 |
| 12:00-12:59 | 0 | none | As 00:00-00:59 |
| 13:00-13:59 | 0 | none | As 00:00-00:59 |
| 14:00-14:57 | 0 | none | As 00:00-00:59, in 14:00 to 14:25: `X.automated` 1,492, `X.no_anchor` 68. 14:26 to 14:57 have no bucket |
| 14:58-14:59 | 0 | none | No credit and no agent, so no bucket. M1's lead would have filled 14:58, but M1 pairs with nothing, so it opens nothing |
| 15:00-15:59 | 0 | none | As 00:00-00:59. P.1 at 15:00:30 comes 90 s after M1's stamp, in M1's project, and is headless all the same: no record names it |
| 16:00-16:59 | 0 | none | As 00:00-00:59 |
| 17:00-17:59 | 0 | none | As 00:00-00:59 |
| 18:00-18:59 | 0 | none | As 00:00-00:59 |
| 19:00-19:59 | 0 | none | As 00:00-00:59 |
| 20:00-20:59 | 0 | none | As 00:00-00:59 |
| 21:00-21:59 | 0 | none | As 00:00-00:59 |
| 22:00-22:59 | 0 | none | As 00:00-00:59 |
| 23:00-23:59 | 0 | none | As 00:00-00:59. P.2 stops at 23:25:22, and nothing runs until the window ends at 24:00:00 |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 0 | 0 |
| `level_c_s` | 0 | 0 |
| `via_relay_s` | 0 | 0 |
| `agent_running_s` | 35,808 | 24 × (30 + 24 × 60 + 22) |
| `X.automated` | 35,808 | 24 × (30 + 24 × 60 + 22) |
| `X.no_anchor` | 1,632 | 24 × (30 + 38) |
| `headless_prompts` | 192 | 24 × (2 + 3 + 3) |
| `relayed_prompts` | 0 | 0 |
| `unpaired_relay_records` | 1 | 1 |

## Readings the case relies on

- **An agent's message gets no relay record.** T3 stores what an agent sends through its MCP tools with `createdBy` `agent`. That fails T3's own test for a message a person sent, so it witnesses nothing (SM 2.2), and the reader writes no record for it, as in [t3-away](../t3-away/). SM 2.2 also has the reader record a message another thread sent, which T3 marks `senderThreadId`, with `declared` `cross_session`. Read that way, each prompt here would pair with such a record and be a cross-session `subagent` by rule 3. The rows would stay as they are, 0 s and the same `X.automated` seconds, but rule 4 would take none of the prompts, and `headless_prompts` would be 0.
- **Rule 4 stands in live slots.** Every slot's presence is unknown, but these records are live, not backfilled, so SM 2.8's lookback rules don't reach them (2.8's last paragraph). No session has a TTY or an IDE or desktop-app host, so rule 4 makes every prompt `headless`. Its exception for `parent_class` `agent_app` is for a prompt an app message witnesses, and nothing witnesses these.
- **Rule 4 comes before rule 8.** SM 2.1's `unknown` row says a live prompt that no app message witnesses "falls to one of rules 1 to 5, such as rule 4 in a session with no TTY, or to rule 8". 2.2's rules go in order and the first match wins, so rule 4 labels these prompts before rule 8 is reached. Their turns are automated, so their seconds are `X.automated`, not `X.agent_only` (SM 5.2).
- **Overlapping turns count once.** `agent_running_s` counts the seconds in which any turn runs, in any project (SM 5.1), so each hour's 1,800 s of turns are 1,492 s of agent time. P.1, in `agent-desk`, overlaps A.1 and B.1, in the hour's other project.
- **An unpaired record opens nothing.** M1 never pairs, so once `RELAY_MATCH_S` passes at 15:09:00, the minute its lead would have filled has nothing credited (SM 2.3). A relay record isn't an agent event (SM 5.4), so with no credit and no agent running, 14:58 and 14:59 have no bucket (SM 5.1).
- **The record's envelope.** With no session to name, M1's record has `session_key` `none` and `harness` `none`. Its `project_key` is the thread's folder's, `agent-desk`.
- **No flag.** `relay_without_prompt` is for a `pairtime relay` call that pairs with nothing (SM 8.7), not for a T3 record.
