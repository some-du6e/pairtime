# Relayed: messages sent while a turn runs

Lena's T3 Code server again, with Claude Code working on `budget-app`, and Lena on her laptop. This half hour is about messages she sends while the agent is busy. T3 Code handles such a message in one of two ways, and the case has both:

- **Queued.** With T3's follow-up setting on queue, the message gets a queued run of its own, and T3 hands it to the agent when the running turn ends, however long that takes. Its relayed lead still ends at its own stamp, and it pairs with the prompt its run delivers, as long as that prompt comes within `RELAY_MATCH_S` = 600 s of the stamp (SM 2.3, 8.1).
- **Steered.** T3's default pushes the message into the running turn. It starts no run of its own, so it pairs with nothing and earns nothing (SM 2.2).

M2 is queued 5 s after M1, so its lead is capped at those 5 s. M3 is steered. M5 is queued behind a turn that runs 12 minutes longer, so its prompt comes 721 s after its stamp, and M5 earns nothing. The case credits 186 s, all level C with `via_relay`.

No SM section describes this case. Every number below comes from SM 2.2 rule 9, 2.3, 2.4 and 5.2 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed. The server's hook records and the T3 reader's records, in the order the agent wrote them |
| Rules | SM 2.2 rule 9, 2.3's relayed row and the paragraph on waiting for the pairing, 2.4's relayed reading, 5.2's codes for a slot whose presence is unknown, and SM 8's `RELAY_MATCH_S`, `RELAY_SEEN_LAG_S`, `COMPOSE_CPS` and `UNKNOWN_BUCKET_MAX_S` |
| Date and zone | Tuesday 2026-11-24, America/Chicago, UTC−06:00. 20:00 local is 02:00Z on 2026-11-25 |
| Evidence | Level C, label `via_relay`, witness `witnessed`. No `os.presence` or `os.focus` record exists, so every slot's presence is `unknown` |
| Window | 20:00:00 to 20:30:00. The session started at 19:58:30 with `parent_class` `agent_app` and no TTY |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision |

## The records

| Message | Sent | Typed | How T3 handles it | Relay record | Its run's prompt | Pairs |
|---|---|---|---|---|---|---|
| M1 | 20:01:00 | 90 | at once | 20:01:03, names the prompt | 20:01:01, T1 to 20:09:00 | yes, 1 s after the stamp |
| M2 | 20:01:05 | 45 | queued behind T1 | 20:01:08, before its prompt | 20:09:01, which names the record; T2 to 20:12:00 | yes, 476 s after the stamp |
| M3 | 20:04:00 | 60 | steered into T1 | none: no run of its own | none | no |
| M4 | 20:14:00 | 90 | at once | 20:14:03, names the prompt | 20:14:01, T4 to 20:27:00 | yes, 1 s after the stamp |
| M5 | 20:15:00 | 60 | queued behind T4 | 20:15:03, before its prompt | 20:27:01; T5 to 20:29:00 | no: 721 s after the stamp |

Each relay record comes 3 s after its message's stamp, inside `RELAY_SEEN_LAG_S` = 60 s. Claude Code may or may not report a message T3 steers into a running turn; the case assumes it does, as an `agent.queue` at 20:04:01, the stricter reading. It has no input in its slot, so it opens no `Q` anchor, and with no run of its own it pairs with nothing.

T1 fills its bucket with 20 + 20 + 180 + 30 + 40 = 290 s, which the 120 s cap holds to 120. T2 fills 20 + 20 + 30 = 70 s. M2's prompt, M2 being relayed, resets the bucket to its base when T2 starts.

| Stop | Next relayed message sent after it | Gap | Next turn starts | Reads |
|---|---|---|---|---|
| T1, 20:09:00 | M4, 20:14:00 | 300 s | 20:09:01 | 1 s: from the stop until T2 starts. M2 was sent before the stop, so it vouches for nothing here |
| T2, 20:12:00 | M4, 20:14:00 | 120 s | 20:14:01 | 60 s, then M4's lead takes the next minute |
| T4, 20:27:00 | none | | 20:27:01 | nothing |
| T5, 20:29:00 | none | | | nothing |

## Credit, minute by minute

Every slot's presence is unknown, so an uncredited second is `X.agent_only` while a person's turn runs, `X.automated` while only automated turns do, and `X.no_anchor` otherwise (SM 5.2). Nothing here is cut by the 120 s cap.

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 20:00 | 60 | `P` 60 | M1's lead, 90 / 1.5 = 60 s, from 20:00:00 to its stamp |
| 20:01 | 5 | `P` 5 | M2's lead: 45 / 1.5 = 30 s, capped at the 5 s since M1, so 20:01:00 to 20:01:05. T1 runs from 20:01:01, and the 55 s after the lead are `X.agent_only` |
| 20:02-20:03 | 0 | none | T1 runs: `X.agent_only` 2 × 60 |
| 20:04 | 0 | none | M3 is steered into T1 and earns nothing. `X.agent_only` 60 |
| 20:05-20:08 | 0 | none | T1 runs: `X.agent_only` 4 × 60 |
| 20:09 | 1 | `READ` 1 | T1 stops at 20:09:00, and M4 follows 300 s later, so the session reads until T2 starts at 20:09:01: 1 s. T2 runs: `X.agent_only` 59 |
| 20:10-20:11 | 0 | none | T2 runs: `X.agent_only` 2 × 60 |
| 20:12 | 60 | `READ` 60 | T2 stops at 20:12:00 and M4 follows 120 s later. Of T2's 70 s, 60 are read here |
| 20:13 | 60 | `P` 60 | M4's lead, 90 / 1.5 = 60 s, from 20:13:00. It beats the reading, whose last 10 s go unread |
| 20:14 | 0 | none | 20:14:00 is in neither a window nor a turn: `X.no_anchor` 1. T4 runs from 20:14:01: `X.agent_only` 59. M5's lead would have run from 20:14:20, but M5 never pairs |
| 20:15-20:26 | 0 | none | T4 runs: `X.agent_only` 12 × 60 |
| 20:27 | 0 | none | T4 stops at 20:27:00 with no relayed message after it: `X.no_anchor` 1. M5's prompt comes at 20:27:01, 721 s after M5's stamp, so nothing pairs with it, rule 4 takes it, and T5 runs automated: `X.automated` 59 |
| 20:28 | 0 | none | T5 runs: `X.automated` 60 |
| 20:29 | 0 | none | T5 stops at 20:29:00. No credit and no agent, so no bucket |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 186 | 60 + 5 + 1 + 60 + 60 |
| `P` | 125 | 60 + 5 + 60 |
| `READ` | 61 | 1 + 60 |
| `level_c_s` | 186 | 125 + 61 |
| `via_relay_s` | 186 | 125 + 61 |
| `agent_running_s` | 1,556 | 59 + 2 × 60 + 60 + 4 × 60 + 59 + 2 × 60 + 59 + 12 × 60 + 59 + 60 |
| `X.agent_only` | 1,433 | 55 + 2 × 60 + 60 + 4 × 60 + 59 + 2 × 60 + 59 + 12 × 60 |
| `X.automated` | 119 | 59 + 60 |
| `X.no_anchor` | 2 | 1 + 1 |
| `relayed_prompts` | 3 | 1 + 1 + 1 |
| `headless_prompts` | 1 | 1 |
| `unpaired_relay_records` | 1 | 1 |

## Readings the case relies on

- **A queued message credits at its own stamp.** M2's lead sits at 20:01:00 to 20:01:05, under T1's run, though its prompt comes at 20:09:01. The minutes it touches stay open until the pairing, which comes well before 20:01 would be final.
- **The lead cap is the time since the thread's previous message**, M1's stamp, so a burst of two messages 5 s apart claims at most 5 s of typing for the second.
- **A message sent before a stop doesn't vouch for its reading.** M2 was sent at 20:01:05, so it says nothing about whether Lena read T1's output after 20:09:00. M4 does, 300 s after the stop, but T2 starts a second after the stop and reading ends there.
- **A steered message has no run of its own**, so it pairs with nothing (SM 2.2). The `agent.queue` Claude Code may write for it opens no anchor on a machine with no input.
- **Queued past `RELAY_MATCH_S`.** M5's relay record waits for a pairing that never comes, so it opens nothing, and its prompt has no witness. That is SM 2.3's stated limit: a message queued longer than `RELAY_MATCH_S` earns nothing.
- **Two link directions.** M1's and M4's prompts came before their relay records, so the records name them. M2's record came first, so its prompt names the record (SM 2.2).
