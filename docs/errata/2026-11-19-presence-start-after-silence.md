# Erratum: where presence starts again after a long silence

Date 2026-11-19. Raised by WP-SC2. Status: decided 2026-11-20 by the orchestrator for Karim: after a no-input run that isn't bridged, presence starts again at the second holding the earliest key or pointer input that any sample reports in the slot that ends the run, and a tail ends PRESENCE_TAIL_S after the last input, timed to the millisecond (SM 2.1). 6.1 and 6.4 keep SM's rows, and 6.4's fixture moves its 23:37:12.7 pointer move to 23:37:15.5. It settles the presence-edges erratum's first question for both edges. The orchestrator's ruling the same day took this rule over the one in [that erratum](2026-11-19-sm-6.1-presence-edges.md)'s decision, whose focus at the act stands: an act's focus test reads the window in front at the act. None of it is on main yet: `presence.markStarts` still runs the fourth rule below, as #52 (bc01d415) merged it. One scorer PR under `oracle-change` applies the SM, DESIGN 3.3, fixture and `pkg/score` edits the Decision below lists, with the presence-edges decision's.

## Input

- SM 6.1's 15:12 row. Rowan comes back at 15:12:40 and clicks in VS Code. The agent samples at the focus change, and that sample has `idle_any_s` 0. SM credits `NAV` 20 and `X.absent` 40, so presence starts at the click, 10 s into its slot. `testdata/timelines/6.1` keeps the row.
- SM 6.4's 23:37 row. Jordan sits down at 23:37:00 after seven minutes away. The fixture puts his first move at 23:37:00.4, but no sample is taken then. The next one, at 23:37:15, reports the slot's last move, at 23:37:12.7. SM credits `P` 50 and `X.no_anchor` 10, so presence starts with the slot.
- The presence-edges erratum's proposal for measuring to the second: presence starts again at the first input after the silence. A sample with `idle_any_s` 0 pins that input, and where none does, "the slot's last reported input is the conservative choice".

## Expected

One rule for where presence starts after a silence longer than `PRESENCE_GAP_S` gives both rows.

## What the rules give

The samples can't tell the two returns apart. A sample reports when the last input before it came, never the first. In 6.1 an input at 15:12:35 followed by the click would give the same samples. In 6.4 the first move could be anywhere after 23:37:00.

| Rule | 6.1, 15:12 | 6.4, 23:37 | 6.4 credited |
|---|---|---|---|
| By slot, SM 2.1's "input during the slot" | `NAV` 30, `X.absent` 30 | `P` 50, `X.no_anchor` 10 | 810 s |
| The presence-edges proposal: at the pinned input, else at the slot's last reported input | `NAV` 20, `X.absent` 40 | `P` 48, `P~r` 2, `X.absent` 10 | 810 s |
| At the first input a sample taken inside the slot reports, else at the slot's start | `NAV` 20, `X.absent` 40 | `P` 50, `X.no_anchor` 10 | 810 s |
| At the input a focus or power sample inside the slot reports; a hook sample only bounds it; else at the slot's start | `NAV` 20, `X.absent` 40 | `P` 50, `X.no_anchor` 10 | 810 s |
| At the earliest input any sample reports in the slot (decided) | `NAV` 20, `X.absent` 40 | `P` 50, `X.no_anchor` 10 with the move at 23:37:15.5 | 810 s |

The 405 s silence before 23:37 is a returned run, ended by the substantive /goal prompt at 23:40:00 with no lock or focus change, so the prompt's think lead from 23:37:10 credits 2 s of it as `P~r`. 808 s was wrong.

The third and fourth reproduce both fixtures. By slot, 6.1 credits 10,105 s instead of 10,095.

They differ on SM 2.1's main case for the return rule, a silence "followed straight away by a typed prompt" (added after the #52 review, #80). The agent samples at the prompt's `UserPromptSubmit` hook, and that sample reports the Enter key, the last input before it, not the first key of the return. Take a prompt of 30 characters submitted at 460 s, typed from 451.5 s, after a silence from 105 s. The third rule starts presence at 459 s, so 450 to 458 s of the slot [450, 465) are `X.absent` though the person typed through them, and only 1 s of the lead's typing part counts. The fourth starts it at 450 s and credits 10 s of `P`. Without the hook sample both give 10 s, so the third rule pays less where the agent knows more.

## What the scorer does

It takes the fourth rule, as #52 merged it (`presence.markStarts` in `pkg/score/presence.go`), until the scorer PR for both presence decisions makes it take the decided rule. The rule reads only the input times that samples report, so it needs no sample's cause and gives the same start whether a periodic sample lands on the boundary or 50 ms early. More samples only move the start earlier. The fourth rule needs each sample's cause, which no record carries. It infers the cause from an os.focus or os.power record in the same millisecond, which the fixtures promise and the agent has kept for focus since #91. A client that dropped that sample would get the slot's start, and a return late in a slot would earn up to 14 s before any input. The decided rule's cost is the typed-prompt case. With the prompt's hook sample at 460 s, presence starts at 459 s and 450 to 458 s are `X.absent`. Without that sample, none of the typing in that slot counts. SM 2.1 states that cost once that PR lands.

The dated start is for crediting seconds. SM 2.3's evidence tests that read presence, a permission answer's at `answered_at` and an interrupt's in its slot, read SM 2.1's value for the slot, as the slot record does: a slot with input is present from its start. Otherwise an interrupt early in the slot that ends a silence, before the correction whose hook sample dates the input, would open no anchor, and the prompt after it would end the silence as a returned run, which SM 2.1 denies when the first act is an interrupt.

## Decision

Decided 2026-11-20 by the orchestrator for Karim. The decision on [the presence-edges erratum](2026-11-19-sm-6.1-presence-edges.md) gave another rule the same day: presence starts with the slot that ends the silence, at the second of a sample there that still shows no input, or at the click or key press that switched windows when the sample taken at that switch is the first in the slot to report input. The orchestrator's ruling of 2026-11-20 takes the rule below, with 6.4's 23:37:12.7 move re-timed to 23:37:15.5, and keeps that decision's focus at the act. Its question 2 stays open.

Presence ends PRESENCE_TAIL_S after the last input, timed to the millisecond. In the slot whose input ends a no-input run that isn't bridged, because the run is longer than PRESENCE_GAP_S or has no input before it, presence starts again at the second holding the earliest key or pointer input that any sample reports in that slot. Run lengths stay in whole slots, and every other slot with input counts from its start, including the first slot after a hard break. Permission answers and interrupts pass their presence test anywhere in a yes slot, as #52's presentFor already does, so an act early in the return slot keeps its anchor. This replaces #52's newest rule, under which only focus or power samples date a return, and it keeps every SM row: 6.1's focus sample dates 15:12:40, and moving 6.4's 23:37:12.7 pointer move to 23:37:15.5 lets the regular sample date 23:37:00. SM 2.1 states the cost, up to 14 s of typing before the first reported input. The teen version is 'after a break, you count as back from the first input pairtime saw.'

No SM worked number and no expected value changes. The edits:

- SM 2.1, after the hard breaks and before the return rule: a paragraph on the edges inside a slot, which states the rule above and its cost. In short, after a break you count as back from the first input pairtime saw.
- SM 1.3's slot paragraph: the two edges of presence, where a tail ends and where a return starts (2.1), are applied to the second inside a slot. The slot record keeps one `present` value, `yes` for a slot with input and no hard break and otherwise the value the slot starts with.
- SM 2.3: the permission answer's evidence is presence at `answered_at` (a `yes` slot counts from its start, 2.1), and the interrupt's presence at its time (a `yes` slot counts from its start, 2.1). From the presence-edges decision, the first paragraph gains: "An evidence test that names focus reads the window in front at the act, the last `os.focus` record before the act's record, as rule 6 of 2.2 reads focus at submit." The typed edit and editor navigation rows test the editor in front at the act.
- SM 2.4: `b` drains where the person is present, with 2.1's edges inside a slot.
- SM 2.5, from the presence-edges decision: "An act's evidence test doesn't read a slot's focus: a test that names focus reads the window in front at the act (2.3)."
- SM 2.7: `present(τ)` in place of `present(slot(τ))` in all four places.
- SM 6.1, the drink: "The click brings VS Code to the front, and the sample the client takes at that focus change reports it, so presence starts again at 15:12:40 (2.1)."
- DESIGN 3.3, the presence row: "A sample can't date the first input that ends a pause, so the scorer starts presence again at the earliest input any sample reports in that slot (SM 2.1)."
- The 6.4 re-time: `testdata/timelines/6.4/gen.py` moves the 23:37:12.7 pointer move to 23:37:15.5. Only one record changes, the os.presence at t 1795214235000 (23:37:15, seq 88248), whose idle_any_s goes from 2.3 to 14.6. expected.json, anchors.json and prompts.json stay byte for byte.
- `testdata/timelines/6.1`: the 15:07 and 15:12 rows stop naming the presence-edges erratum, which the 15:08 to 15:11 row keeps for its question 2. The README gives the start at the click, and from the presence-edges decision its Errata row names that erratum for the 240 s of `X.absent` in 15:08 to 15:11, which no bucket holds, and its click bullet reads the click's focus at the act. `testdata/timelines/README.md` gains a Returns bullet.
- The code: `presence.markStarts` starts presence at the earliest input any sample reports in the slot, from every input a sample reports, a key press included. `atEvent`, `eventAt` and `firstInputInside` go, `presentFor` stays, and `doc.go` and the tests follow.

The decision put all of this in #52, which merged as bc01d415 with the fourth rule instead. A scoring rule changes in SM and in code in the same PR (AGENTS.md), so one scorer PR carries it under `oracle-change`, with the presence-edges decision.
