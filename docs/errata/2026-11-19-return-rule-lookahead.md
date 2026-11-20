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

## SM 2.5's lookups look ahead without a bound

Added by the WP-SC2 review fix-up (#80), 2026-11-20.

A run of browser or chat-app slots is a lookup for project `p` only if a credited slot of `p` starts within `LOOKUP_BRACKET_S` after the run ends (SM 2.5). SM puts no bound on how long the run is, so whether its first second is credited is decided only at its end. A two-hour video right after a credited slot is a lookup if the person comes back to the project within 30 s, and its first 180 s are then `LOOKUP`, credited two hours after they happened. That reaches further than both the pending window and the return rule's 1,200 s.

`pkg/score` holds `FinalBefore` at the start of such a run until its end bracket is final, so every Step agrees with one Step over the same records. It holds only for runs with a credited second in the `LOOKUP_BRACKET_S` before them: once a run's start is final, that bracket is too, and a run with nothing there can't be a lookup. The hold still has no bound for a run that does have one, and WP-SC4's pending rule needs one.

For Karim, alongside the three choices above:

1. Cap the run: a browser or chat-app run longer than some bound, for example `PENDING_WINDOW_S`, is no lookup. SM 8.3 already calls `LOOKUP_MAX_S` "a documentation lookup, not an evening of video".
2. Or keep the hold, and let a lookup's minutes be pending for as long as its run lasts.
3. Or let a lookup's credit land in a later revision of a final bucket, as choice 3 above would for the return rule.
