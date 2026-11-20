# Erratum: DESIGN 7.3 doesn't say how each check reaches its verdict

Date 2026-11-19. Raised by WP-FX6. Status: waiting for Karim. Until Karim decides, `testdata/checks` uses the readings below, and every scenario's `expected.json` names this erratum. The list under each reading names the verdicts that rest on it. A verdict not listed follows from DESIGN's text alone.

## Input

DESIGN 7.3: "Each check reports pass, fail or not computable, with counts. Not computable, such as CC1 on a harness without a log reader, never counts as a failure. Failures change review priority and the band, never the credited number, except CC6 and CC7, which hold minutes." Then the table, one row per check, with what each compares and what it catches. DESIGN 7.7 shows a count as "prompts with keyboard input before them: 212 of 214".

## Expected

Every result in a check fixture follows from DESIGN 7.3, the ledger spec and SM, so that `pkg/check` (WP-TR1, WP-TR2) can be tested against it.

## What DESIGN leaves open, and what the fixtures do

1. **A check with nothing to compare.** A submission with no commit gives CC3 and CC4 nothing to check, and one with no editor act gives CC10 nothing. DESIGN doesn't say whether that is a pass or not computable.
   The fixtures say not computable, with a reason. A pass would put "CC3 passed" on a submission that has no commits at all, which is evidence of nothing. The verdicts that rest on it: CC3, CC4 and CC10 in all nine `4.2-*` scenarios, and CC5, CC8 and CC11 in `4.2-cc2-send-keys`, where no prompt opened an anchor.

2. **Who CC2 counts.** CC2 compares "the share of human-origin prompts with OS input in the 10 s before submit and focus on the host window". SM 2.2 rule 6 gives a prompt a human origin only when it had that input and that focus, so read literally the share is always 100%, and DESIGN 7.7's "212 of 214" can't happen. The prompts it can catch, the ones a pty or `tmux send-keys` driver sends, get origin `no_input` under rule 8.
   The fixtures count every prompt that SM 2.2 rules 1 to 5 leave unlabeled: the ones that end up `human_typed`, `human_pasted` or `no_input`. The count is how many had the input and the focus. Every CC2 verdict rests on it.

3. **Shares without a threshold.** DESIGN states CC2 as a share and CC11's injected part as one ("injected share"), and gives no share at which either fails. It doesn't give the number of key-downs that is "too low" for a typed length in CC11 either.
   No verdict rests on these. The CC2 scenarios have all prompts with input or none (`4.2-cc2-send-keys` has 0 of 5), and the CC11 scenarios have 66 key-downs for 60 typed characters or 3 for 60, with no injected key. Any threshold strictly between those passes and fails the same scenarios. The generator refuses a CC2 share strictly between none and all, so a new scenario can't come to rest on a threshold without someone noticing.

4. **One failing item.** The other checks are stated per item: each hook prompt (CC1), each commit (CC3, CC4), each slot (CC5), each minute (CC6), each batch (CC7), each challenged turn (CC8), each session (CC9), each editor anchor (CC10), each prompt's key count (CC11). DESIGN doesn't say whether one failing item fails the check.
   The fixtures fail a check on one failing item. The "Catches" column needs that reading: a script that invents one prompt, injects four heartbeats or drops one record must be caught. The verdicts that rest on it: CC1 in `4.2-cc1-no-transcript` (1 of 5 prompts), CC5 in `4.2-cc5-second-device` (19 of 75 slots), CC6 in `6.3-cc6-moved-minute` (1 of 30 minutes), CC7 in `4.2-cc7-dropped-record` (1 of 28 batches), CC8 in `4.2-cc8-false-sketch` and `4.2-cc8-made-up-key` (1 of 5 turns), CC9 in `6.3-cc9-moved-turn` (1 of 3 sessions), CC10 in `6.3-cc10-injected-heartbeats` (4 of 14 acts) and CC11 in `4.2-cc11-pasted` (1 of 5 prompts).

5. **Some items not computable.** DESIGN's example is a whole check that isn't computable. A submission can mix a harness with a log reader and one without, as 6.3 mixes Claude Code and Codex with Cursor.
   The fixtures compute what they can. A check fails when a computed item fails, passes when it computed at least one item and none failed, and is not computable when it computed none. The items it couldn't compute are counted. The verdicts that rest on it: CC1 passes in the four `6.3-*` scenarios, with Cursor's two prompts counted as not computable.

6. **Which minutes a broken chain holds.** CC7 says "Affected minutes are held", and DESIGN 6.2 D7 holds minutes "whose chain doesn't verify".
   The fixtures hold the minutes in which the failing batch's revealed records fall. The verifier checks each batch against the head the server stored before it, so one bad batch doesn't fail every later one. And since the writer seals at every minute, a batch never spans one. The verdict that rests on it: `4.2-cc7-dropped-record` holds 19:33 alone, 60 s.

7. **The scope of CC5.** "No slot credited twice across devices or projects" could mean the submitted projects or every project of the account.
   The fixtures take every device and every project of the account in the window, which is what "across projects" and SM 3.4's "one human-minute per wall-clock minute per account across all devices" say. The verdict that rests on it: CC5 fails in `4.2-cc5-second-device`, where the second device credits `remind-bot`, a project the submission doesn't include. Read the other way, that scenario passes CC5.

8. **CC1's normalized length and CC3's recorded lines.** CC1 compares "normalized `len_chars`" and doesn't say what is normalized. CC3 compares "lines changed by agent edit tools plus hand-edit deltas in a commit interval" and doesn't say whether an editor heartbeat that reports an agent's edit (`edit_source` `agent`, as Cursor's heartbeat in 6.2 does) counts as a hand-edit delta, or where the interval starts when the parent commit is older than the records.
   No verdict rests on these. Every transcript copy has its hook's exact `len_chars`. CC3 counts the agent's tool lines and every editor heartbeat except `edit_source` `agent`, since the commit before it in the project. The fixtures' verdicts are the same with agent-sourced heartbeats counted: 6.2's commit leaves 1 or 0 lines unexplained, and `6.2-cc3-copied-lines` leaves 300 or 260.

## For Karim

1. Confirm readings 1, 2, 4, 5, 6 and 7, or pick another for each. A different choice changes the verdicts listed under it, in a PR with the `oracle-change` label.
2. Give CC2 and CC11 thresholds for `pkg/check`, which WP-TR1 and WP-TR2 need. A proposal:
   - CC2 fails at SM 8.7's starting point for `no_input_prompts`: at least 2 prompts, and at least a quarter of the prompts it counts. That would make CC2 and the tier-2 flag agree on public data. The deployed flag can still use its private threshold.
   - CC11 fails a prompt whose lead window has fewer non-injected key-downs than its typed characters, since every typed character needs a key press. It also fails a prompt with any injected key-down in its lead, because DESIGN 7.12 says injected keys "don't count as typing". A teen who uses a text expander would trip the second rule. A share such as 10% would spare them.
3. Say whether CC1 should allow any difference in `len_chars`, for example for line endings a transcript writes differently from the hook. The fixtures don't need it.
