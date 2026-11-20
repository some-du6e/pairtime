# Lookback: two sessions in parallel

Noor, 15, runs Claude Code in two Windows Terminal tabs, one in `trivia-bot` and one in `portfolio`, and switches between them for twenty minutes. Pairtime wasn't installed yet, so lookback has only the two transcripts and no idea which tab was in front. SM 2.8's answer: "a second wanted by two parallel sessions goes to the one with the most recent human prompt". This case tests that rule, the 60 s cap on permission waits, and a pasted prompt whose lead counts only its typed part. It credits 885 s, 490 to `trivia-bot` and 395 to `portfolio`.

No SM section describes this case. Every number below comes from SM 2.8's rules and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for WP-FX5. Two transcripts, merged in time order by the log reader |
| Rules | SM 2.8, with SM 2.2 to 2.4, SM 3.3 within one session and the parameters of SM 8 |
| Date and zone | Sunday 2026-11-22, America/Chicago, UTC−06:00. 16:00 local is 22:00Z |
| Evidence | Level C, witness `backfilled`, presence `unknown` in every slot |
| Window | 16:00:00 to 16:20:00. The sessions start at 15:52:10 and 15:55:40, and A3 and B3 stop after the window, at 16:21:40 and 16:22:00. None of that is checked |
| Projects | `trivia-bot`, called A below, and `portfolio`, called B, one session each. B's session accepts edits without asking |
| Errata | [lookback](../../../../docs/errata/2026-11-19-sm-2.8-lookback.md), point 5 for 16:04, 16:08 and 16:11, and point 3 for the conflict this case leaves out |

## The two transcripts

| Turn | Prompt | Typed | Lead | Runs | Asks permission | Output |
|---|---|---|---|---|---|---|
| B1 | 16:01:00 | 90 | 60 s | to 16:03:30 | no | 45 words, a 16-line edit, 60 words |
| A1 | 16:02:00 | 60 | 40 s | to 16:05:00 | 16:03:00, answered 16:04:40 | 30 words, a 12-line edit, 120 words |
| A2 | 16:09:40 | 90 of 390, 300 pasted | 60 s | to 16:12:10 | no | 45 words, a 20-line edit, 75 words |
| B2 | 16:10:40 | 60 | 40 s | to 16:13:00 | 16:11:20, answered 16:11:35 | 60 words, a build with 4 lines shown, a 12-line edit, 45 words |
| A3 | 16:17:30 | 120 | 80 s | to 16:21:40 | no | 50 words, a 25-line edit, 90 words |
| B3 | 16:19:00 | 45 | 30 s | to 16:22:00 | no | 40 words, 70 words |

Every lead is typing time only, typed characters / 1.5. Every turn's output fills its bucket past 120 s, so each turn that a prompt follows within 600 s of its stop gets exactly 120 s of reading. B1's stop is followed by B2 430 s later, A1's by A2 280 s later, A2's by A3 320 s later and B2's by B3 360 s later. A3 and B3 have nothing after them.

## Credit, minute by minute

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 16:00 | 60 | portfolio `P` 60 | B1's lead, 90 / 1.5 = 60 s, from 16:00:00 |
| 16:01 | 40 | trivia-bot `P` 40 | B1 runs from 16:01:00, and nothing credits 16:01:00 to 16:01:20, so `X.agent_only` 20. A1's lead, 60 / 1.5 = 40 s, from 16:01:20 |
| 16:02 | 0 | none | Both agents run. Lookback credits no reading while an agent runs. `X.agent_only` 60 |
| 16:03 | 30 | trivia-bot `PA` 20, portfolio `READ` 10 | `X.agent_only` 30 to B1's stop at 16:03:30. B reads from the stop. A1's permission waited 100 s, so lookback keeps the last 60 s before the answer, from 16:03:40. Both sessions want 16:03:40 on. A's last prompt, 16:02:00, is later than B's, 16:01:00, so A takes them |
| 16:04 | 60 | trivia-bot `PA` 50, portfolio `READ` 10 | A's PA runs to the answer at 16:04:40 and its 10 s trail to 16:04:50: 60 + 10 = 70, 20 in 16:03 and 50 here. Then B reads again until A1 stops at 16:05:00. B has read 10 + 10 = 20 |
| 16:05 | 60 | trivia-bot `READ` 60 | A1 stops at 16:05:00. Both sessions have output to read, and A's prompt is the more recent, so A reads first |
| 16:06 | 60 | trivia-bot `READ` 60 | A's 120 s end at 16:07:00 |
| 16:07 | 60 | portfolio `READ` 60 | B reads what is left of its 120: 120 − 20 = 100 s, to 16:08:40 |
| 16:08 | 60 | trivia-bot `P` 20, portfolio `READ` 40 | B's reading ends at 16:08:40: 10 + 10 + 60 + 40 = 120. A2's 390 characters include 300 pasted, so its lead is 90 / 1.5 = 60 s, from 16:08:40 |
| 16:09 | 40 | trivia-bot `P` 40 | A2 is sent at 16:09:40 and runs. `X.agent_only` 20 |
| 16:10 | 40 | portfolio `P` 40 | B2's lead, 60 / 1.5 = 40 s, from 16:10:00. Then both agents run: `X.agent_only` 20 |
| 16:11 | 25 | portfolio `PA` 25 | B2's build asks at 16:11:20, and the answer at 16:11:35 keeps its 15 s wait and its 10 s trail: 15 + 10 = 25. `X.agent_only` 20 + 15 = 35 |
| 16:12 | 50 | trivia-bot `READ` 50 | Both agents run to A2's stop at 16:12:10: `X.agent_only` 10. Then A reads, and B2 is still running, so B wants nothing |
| 16:13 | 60 | portfolio `READ` 60 | B2 stops at 16:13:00 and both sessions have output to read. B's last prompt, 16:10:40, is later than A's, 16:09:40, so this time B reads first |
| 16:14 | 60 | portfolio `READ` 60 | B's 120 s end at 16:15:00 |
| 16:15 | 60 | trivia-bot `READ` 60 | A reads what is left: 120 − 50 = 70 s, to 16:16:10 |
| 16:16 | 60 | trivia-bot `P` 50, trivia-bot `READ` 10 | A's reading ends at 16:16:10: 50 + 60 + 10 = 120. A3's lead, 120 / 1.5 = 80 s, from 16:16:10 |
| 16:17 | 30 | trivia-bot `P` 30 | A3 is sent at 16:17:30 and runs. `X.agent_only` 30 |
| 16:18 | 30 | portfolio `P` 30 | `X.agent_only` 30 while A3 runs. B3's lead, 45 / 1.5 = 30 s, from 16:18:30 |
| 16:19 | 0 | none | Both agents run. `X.agent_only` 60 |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 885 | 60 + 40 + 30 + 60 + 60 + 60 + 60 + 60 + 40 + 40 + 25 + 50 + 60 + 60 + 60 + 60 + 30 + 30 |
| `trivia-bot` | 490 | 180 + 70 + 240 |
| `portfolio` | 395 | 130 + 25 + 240 |
| `trivia_bot_p_s` | 180 | 40 + 60 + 80 |
| `trivia_bot_pa_s` | 70 | 60 + 10 |
| `trivia_bot_read_s` | 240 | 2 × 120 |
| `portfolio_p_s` | 130 | 60 + 40 + 30 |
| `portfolio_pa_s` | 25 | 15 + 10 |
| `portfolio_read_s` | 240 | 2 × 120 |
| `contested_s` | 310 | 70 + 120 + 120 |
| `agent_running_s` | 590 | 9 × 60 + 20 + 30 |
| `X.agent_only` | 315 | 20 + 60 + 30 + 20 + 20 + 35 + 10 + 30 + 30 + 60 |
| `agent_only_s` | 315 | 590 − (40 + 30 + 60 + 40 + 25 + 50 + 30) |

`contested_s` is the seconds both sessions wanted: A's permission window against B's reading from 16:03:40 to 16:04:50, then reading against reading from 16:05:00 to 16:07:00, which A takes, and from 16:13:00 to 16:15:00, which B takes. `agent_only_s` is the agents' running time less the seconds credited while one ran, in 16:01, 16:03, 16:04, 16:10, 16:11, 16:12 and 16:18.

## Readings the case relies on

- **No typing lead is contested.** The case keeps every typing lead clear of the other session's windows and reading, because SM 2.8 doesn't settle that conflict. "The most recent human prompt" looks back. A second in the lead of a prompt not yet sent would go to the other session if that session's last prompt is later, while SM 3.2 gives such a second to the prompt it leads. The erratum's point 3 asks which. gen.py checks that no contested second has a typing lead in it.
- **Permission against reading.** At 16:03:40 both readings agree: A's prompt is the more recent, and SM 3.3 rule 2 puts anchor windows before reading anyway.
- **A capped wait is its last 60 s.** A lead ends at its act (SM 2.3), so A1's 100 s wait keeps 16:03:40 to 16:04:40, not 16:03:00 to 16:04:00.
- **The trail stays.** As in the 6.1 case, each permission answer keeps its 10 s trail (the erratum's point 5). Without it the case credits 865 s.
- **Reading starts at the stop** and drains one second per second, as SM 2.4 drains it, whenever the session wins the second.
- **The pasted prompt.** A2 has `pasted_chars` 300. In lookback it is still `human_typed` (SM 2.8: "every remaining prompt is treated as `human_typed`"), and its lead uses `typed_len_est`, 90, not its 390 characters.
- **Two projects.** Each session has its own project, so a stop's reading is vouched for by the next prompt in that project, which is also that session's. Whether another session's prompt in the same project could vouch for it is not exercised.
