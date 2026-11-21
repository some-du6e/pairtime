# Erratum: SM 2.2 rules 6 and 7 lose typing that the release of the submit key hides

Date 2026-11-21. Raised by WP-E2E's CI rehearsal of a real install (TestDesktopRehearsal). Status: decided 2026-11-21 by the orchestrator for Karim (D-input-release-grace): For SM 2.2 rules 6 and 7's input test only, input counts in the KEYS_PRE_S before submit or up to INPUT_RELEASE_GRACE_MS = 250 ms after it (calibration range 150 to 250 ms, TP A4). Nothing else widens. DESIGN 7.3's CC2 uses the same rule-6/7 test. The typing module, when it ships, counts key-downs only. Presence windows, the typing part of a lead, other anchors' input tests and focus at submit stay as they are. SM 2.2 and 8.1, DESIGN 7.3's CC2 row, `pkg/params` and `pkg/score` take the change together, in the PR that adds this file.

## Input

- SM 2.2 rule 6: "Input in the `KEYS_PRE_S` = 10 s before submit (`input_keys` where available, else `input_any`)", with focus at submit on the window hosting the session and a plausible typed length. Rule 7 is "As rule 6" with the plausibility test failed or pasted characters. Rule 8, `no_input`, is everything else, and it opens no anchor.
- SM 1.1: the hook binary stamps `t` when it runs. DESIGN 3.3: the agent takes a presence sample at once at every hook event. An idle sample says only when the last input before it came.
- The rehearsal ran under Xvfb, where only `input_any` exists, with xdotool typing into the terminal. xdotool releases each key at once. It typed 53 characters between 04:13:53.7 and 04:13:58.4 UTC and pressed Return. The hook stamped the prompt `t` = 04:13:58.497. The hook's sample, at 58.501, saw its last input at 58.500. The sample before it, at 04:13:45.007, saw its last input at 44.421.

## What the rule gave

`pkg/score` counted a sample's input only when it fell in [`t` − 10 s, `t`], here [48.497, 58.497]. The 45.007 sample saw nothing after 44.421, and the hook's sample dated its last input 3 ms after `t`. No sample showed input in the window, so the prompt was rule 8, `no_input`, and opened no anchor. A person typed it in the window hosting the session, so rule 6 should have held: `human_typed`, with a 155 s lead (120 s to think and 35 s to type 53 characters).

## Why

The hook starts after the press of the key that sends the prompt, and that key's release comes a key press later. Across Dhakal et al.'s 168,960 typists a key press lasts 116 ms on average, with a standard deviation of 24 ms between typists' means (TP A4). When the release lands between `t` and the hook's sample, the sample's last input is the release, after `t`, and no sample shows the typing before it. Nothing tells the release apart from the typing.

xdotool hit this on every prompt. A person hits it with a fast tap. Windows makes it likelier in two ways. The hook process takes about 24 ms from its creation to `main` before it stamps `t` ([the hook-gate erratum](2026-11-20-hook-p95-gate.md)'s trace), so `t` comes later after the press and the release lands nearer `t`. And Windows counts idle time in GetTickCount's steps of about 16 ms, so a reading can date the press or the release a step after it came (`internal/agent/presence`'s `inputSlop`). The hook's own work from the stamp to the agent's sample takes about 2 ms there, close to the 4 ms the rehearsal saw on Linux.

Today the race reaches only machines without `input_keys`: Windows and X11 until the typing module ships, and all of Wayland. Rule 6 reads key presses where the OS reports them, and macOS's key-down idle time doesn't count a release, so the Return press before `t` shows there.

## Decision

D-input-release-grace, as the Status line says. Rules 6 and 7 count input in [`t` − `KEYS_PRE_S`, `t` + `INPUT_RELEASE_GRACE_MS`]. The value covers the release of a long key press. The decision gave its source as press-to-release times of about 50 to 150 ms plus the hook's start-up after the press. The start-up shortens the release's lead over `t` rather than lengthening it, because `t` is stamped after the press, so the release comes less than one key press after `t`. 250 ms is more than twice Dhakal's mean press, and it also covers a Windows reading a tick late, so the value stands. SM 8.1 gives the range as 150 to 250 ms. Shorter misses the release of a long press, and longer catches hardly any more releases while letting later input vouch for the prompt.

For a script that drives the pty, as `tmux send-keys` does (TP A3 T3), the grace adds a quarter of a second to the 10 s in which a real key press would have to come.

## What changes

- SM 2.2: rule 6's input test gains the grace, rule 7 says it takes it too, and a paragraph after the table says why and what doesn't take it. SM 8.1 gains the `INPUT_RELEASE_GRACE_MS` row.
- `pkg/params`: table 0.2.0 is table 0.1.0 plus `INPUT_RELEASE_GRACE_MS` = 250, hash `sha256:2e531d6e1901be6fc0cd602817e7e8a28cdd8608e4c3da9330566ac68baae1b0`. Table 0.1.0 keeps its parameters and its hash. This is the first parameter added after 0.1.0, so a table now holds the parameters of its version, and `Parse` refuses `INPUT_RELEASE_GRACE_MS` in a table older than 0.2.0.
- `pkg/score`: rules 6 and 7 read input in [`t` − 10 s, `t` + 250 ms]. Scoring version 0.2.0, since this changes a rule and scoring versions stay 0.x until the freeze (DESIGN 10.2). PLAN has WP-SC6's new detectors take 0.2.0 the same way, so they now take 0.3.0. `NewState` refuses table 0.1.0, which lacks a parameter the scorer reads.
- Tests: the rehearsal's exact times give `human_typed`, and with the grace set to 0 they give `no_input`, as the rehearsal found. Input 250 ms after submit counts and 251 ms doesn't. Rule 7 takes the grace, and where the OS reports key presses the Return press shows without it. Focus at submit, a typed command's evidence test and rule 4's test before `session.start` don't take it.
- The goldens change in `v` and `params` with the new version and table.
- DESIGN 7.3's CC2 row takes the grace, as the decision says.
- testdata/checks/README.md scores live buckets at the table the scorer runs with, 0.2.0 since this change, because `NewState` refuses table 0.1.0, which lacks the new parameter. None of the scenarios' 229 prompts changes with the grace.

## What stays

- Presence, the typing part of a lead (SM 2.3), the plausibility test's input slots, rule 4's test, the other anchors' evidence tests and focus at submit read input as before.
- An anchor's evidence level reads the `KEYS_PRE_S` before the prompt, the evidence window `pkg/score` gives a prompt for DESIGN 6.4. No source of key presses records a release today, so no level changes. The typing module counts key-downs only, as macOS does (decided with D-input-release-grace). If it counted releases, a prompt whose only key evidence was a release after `t` would pass rule 6 and stay level B.
- The samples still hide typing in two cases the decision doesn't cover. Input more than `INPUT_RELEASE_GRACE_MS` after `t` that lands before the first sample after `t`, such as a mouse move, hides it the same way. That can happen only when that sample comes more than 250 ms after `t`, as for a prompt with no hook sample: one a log reader found, or a hook call the agent spooled. And when `t` falls a few milliseconds before a slot's end and the release lands in the next slot, no sample shows input in the slot holding `t`, so the plausibility test can count one input slot fewer.
- DESIGN 7.3's CC2 re-checks rule 6 with the same input test, so it takes the grace too: OS input in the 10 s before submit or the `INPUT_RELEASE_GRACE_MS` after it. Otherwise the check would fail prompts the scorer credits, such as the rehearsal's. `pkg/check`, which will implement CC2, doesn't exist yet. `testdata/checks` reads each prompt's input from the hook's sample at the prompt's own millisecond, and none of its 229 prompts changes with the grace.
