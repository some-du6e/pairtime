# Erratum: the return rule can decide a second after its minute is final

Date 2026-11-19. Raised by WP-SC1. Status: waiting for Karim. No WP-SC1 output and no fixture depends on it. WP-SC4, which makes minute buckets final, needs the answer.

## Input

- DESIGN 3.7 and 6.2 D1: a minute bucket stays `pending` for `PENDING_WINDOW_S` = 900 s after its minute ends, "because a lead reaches back up to 420 s before its anchor and reading needs an anchor within 600 s". After that it is `final`.
- SM 2.1's return rule: a silent run of up to `RETURN_GAP_MAX_S` = 600 s is `returned` when "its first following anchor is substantive, in the same project and within 600 s of the run's end". Its reading part "is placed at the start of the run, right after the tail".

## Expected

Every rule that can credit a second has decided it by the time the second's minute is final.

## What the rules give

Take a 600 s silence after a long answer, from 10:00:30 to 10:10:30, and a typed prompt at 10:20:30, 600 s after the silence ends. The prompt qualifies the run, so the reading part of `RETURN_CREDIT` starts right after the 30 s tail, at 10:01:00. The minute 10:01 is final at 10:17:00, 900 s after it ends, and the prompt that decides its credit comes 210 s later. The rule reaches up to `RETURN_GAP_MAX_S` + `READ_HORIZON_S` = 1,200 s from the start of a run to its anchor.

So either the bucket is final without the return credit SM gives it, or a final bucket changes.

## What WP-SC1 does

WP-SC1 derives slots, anchors and the returned runs, not buckets. Its own outputs stay pending for `PENDING_WINDOW_S` + `RETURN_GAP_MAX_S` + `READ_HORIZON_S` = 2,100 s behind the latest record, so they are decided before they are final. `pkg/score`'s `rules.settle` holds that sum.

## A rule to decide it

1. Lengthen the pending window to the rule's reach. D1's 900 s keeps 300 s for late records beyond the 600 s reading horizon; the same margin beyond 1,200 s gives 1,500 s. Witnessing (DESIGN 7.4) is unaffected, since it is about when the batch reached the server, not when the bucket became final.
2. Or cap the return rule so it fits the window: its anchor must come within `PENDING_WINDOW_S` of the run's start.
3. Or let a returned run's credit land in a later revision of a final bucket, with `rev` raised, as the pending window already allows.

## For Karim

1. Pick one of the three, or another rule. The first changes D1's number, the second SM 2.1's rule, the third what `final` means.
