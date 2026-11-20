# Erratum: where presence starts again after a long silence

Date 2026-11-19. Raised by WP-SC2. Status: waiting for Karim. It follows on from [the presence-edges erratum](2026-11-19-sm-6.1-presence-edges.md), which asks whether presence ends and starts to the second. Until Karim decides, `pkg/score` uses the rule below, and both fixtures keep SM's rows.

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
| The presence-edges proposal: at the pinned input, else at the slot's last reported input | `NAV` 20, `X.absent` 40 | `P` 48, `X.absent` 12 | 808 s |
| At the first input a sample taken inside the slot reports, else at the slot's start | `NAV` 20, `X.absent` 40 | `P` 50, `X.no_anchor` 10 | 810 s |
| At the input a focus or power sample inside the slot reports; a hook sample only bounds it; else at the slot's start | `NAV` 20, `X.absent` 40 | `P` 50, `X.no_anchor` 10 | 810 s |

The last two reproduce both fixtures. By slot, 6.1 credits 10,105 s instead of 10,095.

They differ on SM 2.1's main case for the return rule, a silence "followed straight away by a typed prompt" (added after the #52 review, #80). The agent samples at the prompt's `UserPromptSubmit` hook, and that sample reports the Enter key, the last input before it, not the first key of the return. Take a prompt of 30 characters submitted at 460 s, typed from 451.5 s, after a silence from 105 s. The third rule starts presence at 459 s, so 450 to 458 s of the slot [450, 465) are `X.absent` though the person typed through them, and only 1 s of the lead's typing part counts. The fourth starts it at 450 s and credits 10 s of `P`. Without the hook sample both give 10 s, so the third rule pays less where the agent knows more.

## What the scorer does

It takes the fourth rule. The regular sample at a slot's end stands for the whole slot, as SM 2.1 has it. A sample the agent takes inside the slot at a focus change or a power event (DESIGN 3.3) dates the input it reports, because that input is the act that ended the silence. So after a silence longer than `PRESENCE_GAP_S`, the silence runs into the slot that ends it, up to the second of that input. A sample taken at a hook reports the hook's own act, which typing or reading may have preceded, so it only bounds the start: the silence runs up to the last inside sample that still shows none, or to the slot's start. With no inside sample at all, presence starts with the slot. A silence the return rule credits includes those seconds. `presence.markStarts` in `pkg/score/presence.go` is the code.

The dated start is for crediting seconds. SM 2.3's evidence tests that read presence, a permission answer's at `answered_at` and an interrupt's in its slot, read SM 2.1's value for the slot, as the slot record does: a slot with input is present from its start. Otherwise an interrupt early in the slot that ends a silence, before the correction whose hook sample dates the input, would open no anchor, and the prompt after it would end the silence as a returned run, which SM 2.1 denies when the first act is an interrupt.

The rule is generous where the samples are coarse and strict where they are fine. Someone who comes back late in a slot with no event sample in it gets the whole slot, up to 14 s more than the conservative choice. Someone whose return comes with a focus change gets credit from that second.

## For Karim

1. Pick where presence starts after a long silence:
   - The scorer's rule. 6.1 and 6.4 stay as they are, and SM 2.1 gains a sentence on samples taken inside a slot at focus and power events.
   - The third rule, which dates a return from any inside sample. 6.1 and 6.4 stay as they are too, but a typed prompt's own hook sample cuts its typing in the slot that ends the silence.
   - The conservative choice. 6.4's 23:37 row becomes `P` 48 and `X.absent` 12 and its total 808 s, in an `oracle-change` PR that also changes SM 6.4's table.
   - By slot. 6.1's 15:12 row and totals change as the presence-edges erratum's table shows.
2. Whichever you pick also settles that erratum's first question for the start of presence. Its end, the tail, is measured to the millisecond in both fixtures already.
