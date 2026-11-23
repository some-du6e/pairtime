# Relayed: a message that pairs too late to vouch for the reading before it

Tessa, 17, runs T3 Code on a small Linux server at home, where Claude Code works on her `trail-log` project, and she drives it from T3's web client on her laptop. pairtime runs on the server. The server has no desktop, so pairtime samples no presence there, and Claude Code, which T3's server `t3 serve` starts through the Agent SDK, has no TTY. She keeps T3's follow-up setting on queue, so a message she sends while a turn runs gets a run of its own and waits for that turn to end.

M1 pairs at once, and its turn T1 stops at 19:06:00 with its bucket at the 120 s cap. Four minutes later an agent in her `docs` thread sends this thread a message through T3's MCP tools. T3 stores it with `createdBy` `agent`, so pairtime's T3 reader writes no record for it, rule 4 takes its prompt, and its turn A1 runs, automated, for 15 minutes 40 seconds. At 19:15:50, 590 s after T1's stop, Tessa sends M2, and T3 queues it behind A1. When A1 stops, T3 hands M2's run to the agent. The prompt comes 591 s after M2's stamp, inside `RELAY_MATCH_S` = 600 s, so M2 pairs, earns its own lead and starts T2. But it pairs 1,181 s after T1's stop, past `PENDING_WINDOW_S` − 60 s = 840 s, so it vouches for no reading of T1's output, and the 120 s T1 banked earn nothing. M3, sent 180 s after T2's stop and paired at once, does vouch for T2's reading. The case credits 340 s, all level C with `via_relay`. Without the 840 s rule it would credit 460 s, and 120 s of that would sit in minutes that were already final when M2 paired.

No SM section describes this case. Every number below comes from SM 2.2 rules 4 and 9, 2.3, 2.4 and 5.2 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, SM 8's parameters and DESIGN 6.2 D1's `PENDING_WINDOW_S`, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed. The server's hook records and the T3 reader's records, in the order the agent wrote them |
| Rules | SM 2.2 rules 4 and 9, 2.3's relayed row and the paragraph on waiting for the pairing, 2.4's relayed reading and the deadline for the pairing that vouches for it, 5.2's codes for a slot whose presence is unknown, SM 8's `RELAY_MATCH_S`, `RELAY_SEEN_LAG_S`, `COMPOSE_CPS` and `UNKNOWN_BUCKET_MAX_S`, and DESIGN 6.2 D1's `PENDING_WINDOW_S` |
| Date and zone | Wednesday 2026-11-25, America/Denver, UTC−07:00. 19:00 local is 02:00Z on 2026-11-26 |
| Evidence | Level C, label `via_relay`, witness `witnessed`. No `os.presence`, `os.focus` or `os.power` record exists, so every slot's presence is `unknown` |
| Window | 19:00:00 to 19:42:00. The session started at 18:57:20 with `entrypoint` `unknown`, `parent_class` `agent_app`, which is T3's server, and no TTY |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision |

## The records

| Message | Sent (`sent_at`) | Typed | Relay record | Its run's prompt | Pairs | Turn | Output |
|---|---|---|---|---|---|---|---|
| M1 | 19:01:30 | 135 | 19:01:33, names the prompt | 19:01:31 | 1 s after the stamp | T1, to 19:06:00 | 60 words, an 8-line edit, 60 words |
| the agent's, from `docs` | none | 420 | none: `createdBy` `agent` | 19:10:00 | nothing to pair | A1, to 19:25:40 | 90 words, a 16-line edit, an 8-line edit, 60 words |
| M2 | 19:15:50 | 105 | 19:15:53, before its prompt | 19:25:41, names the record | 591 s after the stamp | T2, to 19:34:00 | 60 words, a 6-line edit, 105 words |
| M3 | 19:37:00 | 90 | 19:37:03, names the prompt | 19:37:01 | 1 s after the stamp | M3's turn, to 19:40:30 | 45 words, 30 words |

M3's turn gets no T name, since T3 would read as T3 Code. Each relay record comes 3 s after its message's stamp, inside `RELAY_SEEN_LAG_S` = 60 s, and each says `client` `web`. The `docs` thread runs on a provider pairtime has no hooks for, so this device records nothing of the agent that sent the message, only the prompt the message delivers here. With the agent idle, T3 hands M1 and M3 over a second after their stamps, while M2 waits behind A1. Each relayed lead is its typing time, characters / 1.5, ending at its stamp: 90 s, 70 s and 60 s. No earlier message caps one. M1 is the thread's first, M2 comes 860 s after M1, and M3 1,270 s after M2.

T1 fills its bucket with 20 + 20 + 60 + 20 = 120 s, the cap exactly. A1 is automated, so its prompt resets nothing, and its 30 + 120 + 60 + 20 = 230 s go into the same capped bucket, which T1's unread 120 s already fill. M2's prompt is relayed, so it resets the bucket to its 20 s base when T2 starts, and T2 fills 20 + 20 + 45 + 35 = 120 s. M3's prompt resets it again, and its turn fills 20 + 15 + 10 = 45 s.

| Stop | Next relayed message sent after it | Sent | Paired | Next turn starts | Reads |
|---|---|---|---|---|---|
| T1, 19:06:00 | M2, 19:15:50 | 590 s after the stop | 19:25:41, 1,181 s after the stop | A1, 19:10:00 | nothing, since M2 pairs past 840 s. Without that rule, 120 s from the stop |
| A1, 19:25:40 | M3, 19:37:00 | 680 s after the stop | 19:37:03 | T2, 19:25:41 | nothing. M3 comes more than 600 s after the stop, and M2 was sent before it |
| T2, 19:34:00 | M3, 19:37:00 | 180 s after the stop | 19:37:03, 183 s after the stop | M3's turn, 19:37:01 | 120 s from the stop, then M3's lead |
| M3's turn, 19:40:30 | none | | | | nothing |

## Credit, minute by minute

Every slot's presence is unknown, so an uncredited second is `X.agent_only` while a person's turn runs, `X.automated` while only automated turns do, and `X.no_anchor` otherwise (SM 5.2). No second is `X.bucket_cap`. The one reading drains a bucket that T2 filled to exactly the 120 s cap.

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 19:00 | 60 | `P` 60 | M1's lead, 135 / 1.5 = 90 s, from 19:00:00 to its stamp at 19:01:30. It is the thread's first message, so no earlier one caps the lead |
| 19:01 | 30 | `P` 30 | The lead's last 30 s. T3 hands M1 to the agent at 19:01:31, so 19:01:30 is in neither the lead nor a turn: `X.no_anchor` 1. T1 runs from 19:01:31: `X.agent_only` 29 |
| 19:02-19:05 | 0 | none | T1 runs, and a relayed session reads only after a stop: `X.agent_only` 4 × 60 |
| 19:06-19:07 | 0 | none | T1 stops at 19:06:00 with 120 s banked. M2 is sent 590 s after the stop but pairs 1,181 s after it, past 840 s, so nothing reads them. No credit and no agent, so no bucket. Without the 840 s rule M2 would vouch, and these two minutes would credit `READ` 60 + 60, though both are final before M2 pairs |
| 19:08-19:09 | 0 | none | No credit and no agent. Even without the rule the bucket would be empty from 19:08:00, and the reading would end at 19:10:00, when A1 starts |
| 19:10 | 0 | none | The agent's message reaches the agent at 19:10:00 with no relay record. Rule 4 makes it `headless`, and A1 runs, automated: `X.automated` 60 |
| 19:11-19:13 | 0 | none | A1 runs: `X.automated` 3 × 60 |
| 19:14 | 20 | `P` 20 | A1 runs: `X.automated` 40. M2's lead, 105 / 1.5 = 70 s, starts at 19:14:40, under A1's run |
| 19:15 | 50 | `P` 50 | The lead runs to M2's stamp at 19:15:50. T3 queues M2 behind A1, and the reader records it at 19:15:53. A1 runs on: `X.automated` 10 |
| 19:16-19:24 | 0 | none | A1 runs: `X.automated` 9 × 60. M2's record waits for its pairing |
| 19:25 | 0 | none | A1 runs until 19:25:40: `X.automated` 40. 19:25:40 is in no turn: `X.no_anchor` 1. M2's prompt comes at 19:25:41, 591 s after M2's stamp and inside `RELAY_MATCH_S`, and names M2's record. So M2 pairs, its lead stands, and T2 runs: `X.agent_only` 19 |
| 19:26-19:33 | 0 | none | T2 runs: `X.agent_only` 8 × 60 |
| 19:34 | 60 | `READ` 60 | T2 stops at 19:34:00 with 120 s banked. M3 is sent 180 s later and pairs 183 s after the stop, so the session reads from the stop |
| 19:35 | 60 | `READ` 60 | The same reading. Its 120 s end at 19:36:00 |
| 19:36 | 60 | `P` 60 | M3's lead, 90 / 1.5 = 60 s, from 19:36:00 to its stamp |
| 19:37 | 0 | none | 19:37:00 is in neither the lead nor a turn: `X.no_anchor` 1. M3's turn runs from 19:37:01: `X.agent_only` 59 |
| 19:38-19:39 | 0 | none | M3's turn runs: `X.agent_only` 2 × 60 |
| 19:40 | 0 | none | M3's turn runs until 19:40:30: `X.agent_only` 30. No relayed message follows its stop, so nothing reads its 45 s: `X.no_anchor` 30 |
| 19:41 | 0 | none | No credit and no agent, so no bucket |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 340 | 60 + 30 + 20 + 50 + 60 + 60 + 60 |
| `P` | 220 | 60 + 30 + 20 + 50 + 60 |
| `READ` | 120 | 60 + 60 |
| `level_c_s` | 340 | 220 + 120 |
| `via_relay_s` | 340 | 220 + 120 |
| `agent_running_s` | 1,917 | 29 + 4 × 60 + 60 + 3 × 60 + 60 + 60 + 9 × 60 + 59 + 8 × 60 + 59 + 2 × 60 + 30 |
| `X.agent_only` | 977 | 29 + 4 × 60 + 19 + 8 × 60 + 59 + 2 × 60 + 30 |
| `X.automated` | 870 | 60 + 3 × 60 + 40 + 10 + 9 × 60 + 40 |
| `X.no_anchor` | 33 | 1 + 1 + 1 + 30 |
| `relayed_prompts` | 3 | 1 + 1 + 1 |
| `headless_prompts` | 1 | 1 |

## Readings the case relies on

- **A message that pairs more than 840 s after a stop vouches for none of its reading** (SM 2.4, the erratum's decision 3). M2 is the thread's next relayed message after T1's stop, sent 590 s after it, inside the 600 s that 2.4 asks for. But it pairs only when its prompt comes at 19:25:41, 1,181 s after the stop, so T1's 120 s go unread. Without the 840 s rule M2 would vouch. The session would read from 19:06:00 until A1 starts at 19:10:00, 240 s that hold all 120 s of the bucket, so 19:06 and 19:07 would credit `READ` 60 each. The case would credit 340 + 120 = 460 s, with `READ` 120 + 120 = 240.
- **D1's finality is why the rule exists** (DESIGN 6.2 D1, SM 2.3). 19:06, the minute of T1's stop, ends at 19:07:00 and is final 900 s later, at 19:22:00. 19:07 is final at 19:23:00. M2 pairs at 19:25:41, 221 s after 19:06 became final, and a pairing changes nothing in a final minute. At 19:22:00 the scorer can't know whether M2 will pair at all, since its prompt may come until 19:25:50, `RELAY_MATCH_S` after its stamp. The deadline settles the question at 19:20:00, 840 s after the stop, while both minutes are still pending. A message may be sent up to 600 s after a stop and pair up to `RELAY_MATCH_S` after that, 1,200 s after the stop. A stop's minute is final more than 900 s after the stop, wherever in the minute the stop falls, so 840 s always comes first, with more than a minute to spare.
- **A pairing too late to vouch still earns the message's own lead** (SM 2.3 and 8.1, the erratum's decision 3). M2's prompt comes 591 s after its stamp, inside `RELAY_MATCH_S`, so its 70 s lead stands at the stamp, 19:14:40 to 19:15:50. Its minutes, 19:14 and 19:15, end at 19:15:00 and 19:16:00 and stay pending until 19:30:00 and 19:31:00, so the pairing at 19:25:41 lands while both are open. Any pairing inside `RELAY_MATCH_S` does. A lead starts at most `COMPOSE_MAX_S` = 300 s before its stamp, so its first minute is final more than 600 s after the stamp.
- **A message sent before a stop doesn't vouch for it** (SM 2.4, the erratum's reading 5). M2 was sent at 19:15:50, before A1's stop at 19:25:40, so it says nothing about reading A1's output. The thread's next message after that stop is M3, 680 s later and past 600 s, and T2 starts a second after the stop anyway. A1's output went into the same capped bucket (the erratum's reading 4), nobody reads it, and T2's prompt resets the bucket at 19:25:41.
- **An agent's message has no record** (SM 2.2, the erratum's decision 1). T3 stores it with `createdBy` `agent`, and the reader records only messages T3 stores with `createdBy` `user`. Nothing witnesses its prompt, so rule 4 takes it, as it takes every unwitnessed prompt of a session T3's server started. A1's running seconds are `X.automated` (SM 5.2), apart from the 70 s that M2's lead credits under it. A relayed lead ignores what the slot records (SM 2.3).
- **A pairing in time vouches** (SM 2.4). M3 is sent 180 s after T2's stop and pairs 183 s after it, so the session reads T2's 120 s from the stop, 19:34:00 to 19:36:00, and M3's lead takes the next minute. 19:34 is final at 19:50:00, long after M3 pairs.
- **The lead ends at the stamp, not at the prompt** (SM 2.3). 19:01:30 and 19:37:00 lie between a lead and the turn its prompt starts, in no window.
- **Two link directions** (SM 1.2 and 2.2). M1's and M3's prompts came before their records, so the records name them. M2's record came almost ten minutes before its prompt, so the prompt names the record, in `relay` after `low_effort`.
