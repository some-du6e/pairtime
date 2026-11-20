# Check fixtures

These are the oracle for DESIGN 7.3's consistency checks, CC1 to CC11. Each scenario is a copy of one of SM's worked examples from [../timelines](../timelines/), edited the way the table's "Catches" column describes, with the inputs every check reads and the result every check must give. `pkg/check`'s tests run the checks on each scenario and compare: WP-TR1 for CC1 to CC5, WP-TR2 for CC6 to CC11. WP-FX6 wrote them before any check existed, from DESIGN 7.3, the ledger spec and SM, so no code under test produced a number here.

These files are oracle paths (docs/PLAN.md, ground rule 5). A PR that changes them together with code under `cmd/`, `internal/`, `pkg/`, `web/` or `extension/` needs Karim's `oracle-change` label. Where DESIGN doesn't decide a verdict, the fixtures take a reading and say so in an erratum for Karim (ground rule 6). Every scenario rests on at least one of the readings in [2026-11-19-check-readings](../../docs/errata/2026-11-19-check-readings.md), and that erratum lists which verdicts rest on which.

## What proves what

Each failing scenario fails one check. Every other check passes on the same data or isn't computable for a reason DESIGN gives, so a failure belongs to its check alone.

| Check | Passes in | Fails in | Not computable in |
|---|---|---|---|
| CC1 hook vs log | `4.2-clean`, the `6.3-*` scenarios | `4.2-cc1-no-transcript` | `6.2-clean`: Cursor has no v1 log reader |
| CC2 prompt vs OS | `4.2-clean`, `6.2-clean`, `6.3-clean` | `4.2-cc2-send-keys` | |
| CC3 edits vs git | `6.2-clean`, `6.3-clean` | `6.2-cc3-copied-lines`, `6.2-cc3-plugin-claim` | the `4.2-*` scenarios: no commit |
| CC4 commit clocks | `6.2-clean`, `6.3-clean` | `6.2-cc4-late`, `6.2-cc4-diffstat`, `6.3-cc4-pushed-earlier` | the `4.2-*` scenarios: no commit |
| CC5 one attention stream | `4.2-clean`, `6.2-clean`, `6.3-clean` | `4.2-cc5-second-device` | `4.2-cc2-send-keys`: nothing credited |
| CC6 live vs re-scored | every scenario but one | `6.3-cc6-moved-minute` | |
| CC7 chain integrity | every scenario but two | `4.2-cc7-dropped-record`, `4.2-cc7-dropped-batch` | |
| CC8 content-derived inputs | `4.2-clean`, `4.2-cc8-removed-spans` | `4.2-cc8-false-sketch`, `4.2-cc8-made-up-key`, `4.2-cc8-hidden-shingles`, `4.2-cc8-false-class`, `4.2-cc8-inflated-length` | `4.2-cc2-send-keys`: nothing drawn |
| CC9 session integrity | `4.2-clean`, `6.2-clean`, `6.3-clean` | `6.3-cc9-moved-turn`, `6.3-cc9-orphan-turn`, `4.2-cc9-parent-class` | |
| CC10 editor vs focus | `6.2-clean`, `6.3-clean` | `6.3-cc10-injected-heartbeats`: no editor has focus; `6.3-cc10-wrong-project`: the focused editor is on another project | the `4.2-*` scenarios: no editor |
| CC11 typing, module on | `4.2-clean` | `4.2-cc11-pasted` | the `6.2-*` and `6.3-*` scenarios and `4.2-cc8-inflated-length`: module off; `4.2-cc2-send-keys`: no human prompt |

`4.2-cc8-removed-spans` passes every check although its client lied. It is the limit ledger-v1.md 10.8 states and [issue #15](https://github.com/some-du6e/pairtime/issues/15) asks Karim to decide on. Its `known_limit` says so.

## The scenarios

| Scenario | Base | What changes |
|---|---|---|
| `4.2-clean` | [4.2-alternation](../timelines/4.2-alternation/) | Nothing the checks object to: the base with the transcript's copy of each prompt, the typing module's counts, and each prompt's features and commitment in its ledger body |
| `4.2-cc1-no-transcript` | 4.2-alternation | B1's turn came from a script calling `pairtime hook`, so the transcript has nothing of it |
| `4.2-cc2-send-keys` | 4.2-alternation | Claude Code runs in tmux and a script types all five prompts with `tmux send-keys`: no key press reaches the OS |
| `4.2-cc5-second-device` | 4.2-alternation and [6.2](../timelines/6.2/) | A second device on the account runs 6.2's five minutes at the same time |
| `4.2-cc7-dropped-record` | 4.2-alternation | The reveal leaves out one record of a witnessed batch |
| `4.2-cc7-dropped-batch` | 4.2-alternation | The reveal leaves out the last batch of the re-score range, A3's stop and its sample |
| `4.2-cc8-false-sketch` | 4.2-alternation | A3's committed sketch is another request's, so the re-roll rule misses it; the client reveals A3's true values |
| `4.2-cc8-made-up-key` | 4.2-alternation | A3's sketch and values are under a key of the client's own; A1 and A2 show the same shingles with other values |
| `4.2-cc8-hidden-shingles` | 4.2-alternation | As the made-up key, with no `x` on any of A3's values and nothing removed |
| `4.2-cc8-false-class` | 4.2-alternation | B2 is the go-ahead `looks good`, committed with class substantive |
| `4.2-cc8-inflated-length` | 4.2-alternation | B2's `len_chars` and `typed_len_est` say 450 for its 60 characters; the typing module is off |
| `4.2-cc8-removed-spans` | 4.2-alternation | As the made-up key, with three words of A3 removed by hand so no shingle of A3 shows its text |
| `4.2-cc9-parent-class` | 4.2-alternation | A second process files a startup under the live session's key and names a multiplexer parent; the first named Terminal |
| `4.2-cc11-pasted` | 4.2-alternation | B2 was pasted with Cmd-V and the paste wasn't marked: three key-downs for 60 typed characters |
| `6.2-clean` | 6.2 | Nothing the checks object to: the base with its commit's server clocks and forge lookup |
| `6.2-cc3-copied-lines` | 6.2 | The commit adds 299 lines copied in outside anything pairtime watches |
| `6.2-cc3-plugin-claim` | 6.2 | As `6.2-cc3-copied-lines`, plus a heartbeat posted through the plugin inbox that claims the copied lines as a 300-line paste |
| `6.2-cc4-late` | 6.2 | pairtimed first sees the commit 18 minutes after its `observed_at` |
| `6.2-cc4-diffstat` | 6.2 | The forge's diffstat for the commit isn't the one the laptop reported |
| `6.3-clean` | [6.3](../timelines/6.3/) | Nothing the checks object to: the base with the log readers' copies of A's and B's prompts and E's commit clocks |
| `6.3-cc4-pushed-earlier` | 6.3 | GitHub's push of E's commit is five minutes older than the laptop's `observed_at` |
| `6.3-cc6-moved-minute` | 6.3 | The live bucket of 14:18 credits B with A's minute |
| `6.3-cc9-moved-turn` | 6.3 | A's second turn is labeled with project D |
| `6.3-cc9-orphan-turn` | 6.3 | A's second turn's agent records move to a session made up for D, without its prompt |
| `6.3-cc10-injected-heartbeats` | 6.3 | A script posts D's heartbeats while iTerm has focus and Sam types A's prompt |
| `6.3-cc10-wrong-project` | 6.3 | A script posts D's heartbeats while Cursor, on E, has focus |

Each scenario's `expected.json` tells its story, lists its edits and notes what its results rely on.

## A scenario directory

| File | What it holds | In |
|---|---|---|
| `expected.json` | The scenario, its submission, and every check's result and counts | every scenario |
| `events.jsonl` | Every device's events, in arrival order | every scenario |
| `ledger.json` | The client's metadata reveal, and the rows the server stored for each batch | every scenario |
| `commits.json` | What the server holds for each commit: its clocks and the forge's answer | the `6.2-*` and `6.3-*` scenarios |
| `challenge.json` | The draw, and each drawn prompt's text reveal and shingle reveal | the `4.2-*` scenarios |
| `typing.json` | The typing module's per-slot key-down counts | the `4.2-*` scenarios but `4.2-cc8-inflated-length`, whose Mac has the module off |

### `expected.json`

- `scenario`, `title`, `story`, and `base`, the SM fixture it copies.
- `edits` says what changed from the base, in order.
- `devices` lists each device: its `os`, whether it has `input_keys`, the typing module and a VM label, and whether it `reveals`. In every scenario one device submits and reveals. The second device in `4.2-cc5-second-device` reveals nothing; the server has only its live buckets.
- `submission` is the projects `P` and the window `W` (DESIGN 7.5). The checks look at records in `W`. Records before it set up state, as in a timeline fixture.
- `live` says what the server received live. See [The live buckets](#the-live-buckets).
- `checks` holds one result per check: `result` (`pass`, `fail` or `not_computable`), `counts`, `items` naming what failed or couldn't be computed, `reason` for a check that isn't computable, and `basis` where a result rests on reasoning instead of counts: CC6 always, and CC5 where the scorer's output isn't in SM's rows.
- `fails` lists the checks that fail. It has one entry or none.
- `not_exercised` holds the checks a scenario has no inputs for, with the reason. Only CC8 is ever here: SM 6.2 and 6.3 don't quote enough prompt text to commit and reveal.
- `known_limit`, `errata` and `notes`.

A count a scenario states is part of the oracle. A count it leaves out isn't asserted. Where a scenario's edit changes what the scorer credits, CC5 states no `slots_credited`, because that number needs a scorer.

### `events.jsonl`

The format of [../timelines/README.md](../timelines/README.md#eventsjsonl): `pkg/event`'s own encoding, one event per line, in arrival order. A scenario with two devices interleaves their events by `t`, and each device's `seq` is gapless on its own. Keys, devices and times are the base fixture's. Event ids are UUIDv7s with the event's time and bits from a hash of the base, the device and `seq`, so two scenarios with the same records write the same bytes. The prompt texts the scenarios use are in the base fixture's `prompts.json`.

`events.jsonl` holds what each device recorded, and `ledger.json` what the client revealed. They hold the same records in every scenario but three: `4.2-cc7-dropped-record` and `4.2-cc7-dropped-batch` leave records out of the reveal, and the second device of `4.2-cc5-second-device` reveals nothing, so its records are in `events.jsonl` only.

### `ledger.json`

`devices[]`, one per revealing device:

- `rescore_range` is DESIGN 6.5's re-score range: `start` and `end` in Unix ms, from W's start minus 900 s, since no scenario has a checkpoint, to W's end plus `REVEAL_POST_S` = 900 s, and `first_b` and `last_b`, the first and last batch the server stored with records in it. DESIGN 7.5 step 3 has the client reveal every one of those. How the server finds them from its own rows is WP-SB4's; in every scenario here they are all of `witnessed[]`.
- `batches[]` is the client's metadata reveal (DESIGN 7.5 step 3): each batch's `b` and its records, each with `seq`, `rtype` (ledger-v1.md 4.2), `project_key` (`""` for none, as in the ledger vectors), `session_key` and `body`, the stored body bytes as text. Every body is ASCII.
- `witnessed[]` is what the server stored when each batch arrived (ledger-v1.md 6): `b`, `n`, `prev`, `root`, `head` and `received_at`.

Every record of the revealing device in `events.jsonl` is in a batch the server stored, a row of `witnessed[]`. `batches[]` reveals all of them except the record and the batch the two `4.2-cc7-*` scenarios leave out. The writer seals a batch at every UTC minute, and at once after a hook's `prompt.submit` or `agent.stop` and after a lock or suspend, together with the records of the same millisecond, such as the presence sample the agent takes then (DESIGN 3.3). So no batch spans a minute, and a suspend's batch holds its own sample, uploaded before the laptop sleeps. Each batch reached the server 0.25 to 1.5 s after it was sealed and never before the batch before it, because the server stores batch `b` only after `b - 1` (ledger-v1.md 6). So every record is witnessed, and `received_at` rises with `b`. Batch numbers start at the device's first `seq` divided by 3, an arbitrary choice, and the first batch's `prev` stands for the head the server stored before it: the SHA-256 of `stand-in head of batch <b - 1> of <device_id>`.

Bodies follow ledger-v1.md 4.5: `{"n": nonce, "e": the event after Event.Shareable}`. In the `4.2-*` scenarios each `prompt.submit` body also carries `f` and `c`, its features and commitment (ledger-v1.md 10.7 and 10.2). Other bodies carry no `f`, as a writer from before features would write them. The device keys are test keys, published here and used nowhere else: `K_dev` is the SHA-256 of `pairtime check fixtures: K_dev of <device_id>`, and `K_leaf`, `K_tok` and `K_feat` come from it as ledger-v1.md 3 says. A check never needs them. Only the device can compute a nonce, and only the generator needs `K_feat`.

### `commits.json`

`commits[]`: `sha`, `project_key`, `observed_at` (the laptop's, which is its `git.change`'s `t`), `server_first_seen_at` by the server's clock, `seen_via` (`device` for the laptop's commit record, `forge_push` for a GitHub App push), and `forge`: `host`, `app_installed`, `push_time` on App-installed repos, `found_at`, `sha_found`, and the forge's `files_changed`, `insertions` and `deletions` for the SHA (DESIGN 5.8).

### `challenge.json`

`device_id`, the `pool` of `seq`s (DESIGN 7.5 step 4: human prompts in `P` and `W` that opened a credited anchor), `k`, and `drawn[]`. Every pool here is smaller than `CHALLENGE_K` = 10, so the draw takes all of it, and the seed doesn't matter. Each drawn prompt has its `seq`, its `turn_key`, `text`, its redacted reveal as ledger-v1.md 10.3 gives it, and `cc8`, the per-shingle values of 10.8, ascending, each with its shingle's text unless the shingle touches a removed span. The committed `f` and `c` are in the prompt's body in `ledger.json`. The scenarios have no agent text, so the reveal of the agent message each prompt answered isn't here. CC8 doesn't read it.

### `typing.json`

`device_id`, `module` (`on`), `vm`, and `slots[]`: `{start, keys, injected}` for each 15-second slot with a key-down, where `keys` counts every key-down and `injected` the ones the module saw injected. It is a stand-in. `pkg/event` has no field for the typing module's counts yet (DESIGN 7.2 rank 5 ships it in M5), and the work package that adds them should adopt this shape or change these files with it. The counts agree with the presence samples: a slot has key-downs exactly when its samples show a key press.

### The live buckets

CC5 and CC6 read the minute buckets the server received live, which a scenario doesn't list. Each device's live buckets are its own re-score: `pkg/score` over its events at the 0.1.0 parameter table. It takes content-derived features from the bodies' `f` where they exist, as the server does (DESIGN 6.2 D18), and from the base fixture's `prompts.json` otherwise. Then `live.mutations` apply. The one mutation, `move_project`, gives every credited slot of one minute the `to` project instead of `from`, and states the seconds that minute holds.

## How each check is read

`checks.py` computes every result here from the scenario's own files, and `gen.py` states every result by hand. The generator writes nothing unless the two agree. CC5 and CC6 are the exception until `pkg/score` exists: they compare live buckets and a re-score that no code here computes, so `gen.py` states them, from its slot tables for CC5 and from the scenario's mutations for CC6, and each says what it rests on in `basis` where its counts don't. These are the rules the checks follow, with the readings the erratum lists.

- **CC1.** For each session of `P` whose harness has a v1 log reader (DESIGN 5.1: Claude Code, Codex, Copilot, Gemini CLI, Devin and Cline, not Cursor), each hook `prompt.submit` in `W` needs a `via` `log` `prompt.submit` with the same session and `turn_key`, at most 5 s from it and with the same `len_chars`. The matched pairs keep their order, and every log entry has its hook record. A hook prompt of a session with no reader is counted as not computable.
- **CC2.** The prompts of `P` in `W` that SM 2.2 rules 1 to 5 leave unlabeled, one per turn, the hook's record where there is one. Each needs input in the 10 s before submit, read from the sample taken at the hook (`idle_keys_s` where the machine has `input_keys`, else `idle_any_s`, at most 10 s), and focus at submit on a window whose `hosted_sessions` holds the session, or on the editor for an IDE session. The sample and the focus are the prompt's own device's: another device's records say nothing about this one's keyboard or windows, and in `4.2-cc5-second-device` the laptop's focus at 19:36:00 shares B1's millisecond.
- **CC3.** For each commit of `P` in `W`, the changed lines are its insertions plus deletions. The recorded lines are the lines added and removed by `agent.tool` edits and writes, plus `lines_changed` of every `editor.activity` except `edit_source` `agent`, in the commit's project since the commit before it there, from records whose `via` DESIGN 6.2 D19 trusts: `hook`, `log`, `otel`, `editor`, and `bridge`, which is a heartbeat that passed the peer-process check. Records via `plugin`, `track`, `bridge_unverified` or `editor_unverified` are claims any process on the machine can make (DESIGN 5.2), so their lines explain nothing; each item lists them as `ignored_lines`. A commit is flagged `unexplained_lines` when its unexplained lines, changed minus recorded, are more than half its lines and more than 200.
- **CC4.** For each commit, `observed_at` < `server_first_seen_at` < `observed_at` + 15 minutes. On an App-installed repo, `push_time` ≥ `observed_at` − 60 s. The forge found the SHA with the record's files, insertions and deletions.
- **CC5.** Every device's live buckets in `W`, in every project. A slot is credited twice when two devices, or two projects of one device, credit it. The slot tables `gen.py` states hold each device's credited seconds, not its projects, because SM 3.1 gives each slot of a device one project; so in these scenarios only two devices can credit a slot twice, and the two-project case waits for `pkg/score`. `merge_dropped_s` is what SM 3.4's merge drops, which is WP-SV7's job; CC5 changes no credit.
- **CC6.** Each device's live buckets against its re-score, minute by minute and slot by slot, in the minutes of W that CC7 doesn't hold: a held minute's records didn't verify, so there is nothing to reproduce it from. A minute that differs is held (DESIGN 6.2 D7).
- **CC7.** For each device, every batch the server stored between `rescore_range`'s `first_b` and `last_b` is revealed (`missing_batches`), and batch numbers and `seq` rise by 1 with no gap. Each revealed batch's entries give the `n`, `R_b` and `H_b` the server stored, with `H_b` computed from the head the server stored before it, and each body agrees with its header (ledger-v1.md 4.5, 9). A batch that is missing or fails holds every minute its records could fall in: from the minute of the last verified record before it to the minute of the first verified record after it, or of the batch's `received_at` when no verified record follows. Its own revealed records can't narrow that, because they are what failed to verify. `held_minutes` counts the held minutes in W, and each item gives the span as `between`.
- **CC8.** For each drawn prompt (ledger-v1.md 10.8), the shingle reveal is well formed and its values give the body's `f.mh` (`bad_entry`, `sketch`). When nothing was removed, the shown shingles are exactly the text's and every value shows one (`shingles`); the body's `len_chars` is the revealed text's length in code points and its `typed_len_est` is no more than that (`lengths`); and the class SM 2.2 gives the text, with the typed length bounded by the text's, is `f.pc` (`class`). Across the device's reveals, a shingle, a kind and a text, that shows two values fails every reveal showing one of them (`conflict`, with `conflicts_with` naming the reveals that show another value), and `shingle_conflicts` counts such shingles, so neither depends on the order of the reveals. The kind, word or character shingles, follows from the number of words the text reveal covers, removed ones included.
- **CC9.** For each harness session of `P` with records in `W`, every record names one project. Every turn an `agent.output`, `agent.interrupt`, `agent.queue` or `agent.stop` names has a `prompt.submit` in the session, or ends with a stop hook's continuation. Every `session.start` of the session has one `parent_class`.
- **CC10.** For each `editor.activity` of `P` in `W` of an anchor's kind (SM 2.3: an edit or save with `edit_source` `human`, `tab` or `unknown`, or a navigate, open, debug or task), the last `os.focus` of the same device at or before it is an editor whose `focus_project` is the record's project. Both halves count: `6.3-cc10-injected-heartbeats` fails the first, `6.3-cc10-wrong-project` the second alone.
- **CC11.** Only with the typing module on. For each human prompt with typed characters and a typing part in its lead (`substantive`, `error_paste` or `answer`), count the key-downs, minus the injected ones, in the slots its lead window overlaps whose focus, the last `os.focus` at or before the slot's end, is the window hosting the prompt's session (the editor, for an IDE session), leaving out slots an earlier prompt already counted. The count is compared with the characters the lead's typing part credits, `min(typed_len_est, 450)`, since SM 2.3 credits at most `COMPOSE_MAX_S` = 300 s of typing at 1.5 characters a second. DESIGN says no more than "too low", so the threshold `pkg/check` will use is pending Karim's decision (erratum, For Karim item 2). No verdict here rests on it: each prompt's count is at least its credited characters or under a quarter of them, which the erratum's proposal, a quarter, and every stricter reading up to all of them agree on, and the generator refuses a count in between. A prompt with an injected key-down in its lead fails too; no scenario has one.

A check fails when an item it computed fails. It passes when it computed an item and none failed. It is not computable when it had nothing to compute, with a `reason`.

## What a check's test does with a scenario

1. Load `events.jsonl` and split it by device. Score each device with `pkg/score` and apply `live.mutations`: those are the live buckets.
2. Build what the server holds from `ledger.json`, `commits.json`, `challenge.json` and `typing.json`, where a scenario has them.
3. Run every check in `checks` on the submission, and compare `result` and every stated count. Skip the checks in `not_exercised`.

## Generating

```sh
python3 -I testdata/checks/gen.py            # rewrite the checked-in files
python3 -I testdata/checks/gen.py --out /tmp/x
```

`gen.py` describes every scenario. `checks.py` holds what they share: loading a base fixture, the edits, the ledger, the keyed content values, and the reading of each check. Both use `../timelines/timeline.py` unchanged. Presence edits work from the base's own samples: each sample says when the last input before it was, those times give every sample back exactly, and an edit removes or adds input times and recomputes the samples. `checks.py` refuses a prompt text that ledger-v1.md 10.4 would change, so its normalization never has to guess. The texts it commits are plain lowercase words, which every step of 10.4 leaves as they are, and 4.2's come from its `prompts.json`. Every slot table CC5 reads must add up to the base's rows, minute by minute, so a base that changes can't leave a stale table behind. `gen.py` first runs `checks.selftest`, which covers what no scenario reaches.

The output is deterministic. `internal/timelines/checks_test.go` checks that every directory here but `__pycache__` has an `expected.json`; every event against `Event.Validate`, the JSON Schema and its own encoding; each device's stream; the shape of every `expected.json`, including a `basis` for CC6 and for a CC5 without slot counts; that the witnessed rows chain and arrive in order and that `rescore_range` is DESIGN 6.5's; that the reveal covers the range and each revealed batch's root and head reproduce the witnessed ones, except the batches CC7 fails; each body's `e` against `Event.Shareable` of its event, byte for byte; and that `gen.py` writes every file byte for byte.
