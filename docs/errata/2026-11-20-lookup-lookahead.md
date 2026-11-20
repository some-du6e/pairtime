# Erratum: SM 2.5's lookups look ahead without a bound

Date 2026-11-20. Raised by WP-SC2's review fix-up. Status: waiting for a decision. It follows on from [the return-rule look-ahead erratum](2026-11-19-return-rule-lookahead.md), which asks how far ahead of a second a rule may look before the second's minute is final, and it was raised by the #52 review (#80).

## Input

- DESIGN 3.7 and 6.2 D1: a minute bucket stays `pending` for `PENDING_WINDOW_S` = 900 s after its minute ends, then it is `final`.
- SM 2.5: a run of browser or chat-app slots counts as `LOOKUP` for project `p` when it "starts within `LOOKUP_BRACKET_S` = 30 s after a credited slot of `p`, ends within 30 s before a credited slot of `p`, and no agent turn in `p` is running during it". The run is credited up to `LOOKUP_MAX_S` = 180 s.

## Expected

Every rule that can credit a second has decided it by the time the second's minute is final, as the return-rule erratum asks.

## What the rules give

SM puts no bound on how long the run is, so whether its first second is credited is decided only at its end. A two-hour video right after a credited slot is a lookup if the person comes back to the project within 30 s of its end, and its first 180 s are then `LOOKUP`, credited two hours after they happened. That reaches further than the pending window and further than the return rule's 1,200 s.

## What the scorer does

`pkg/score` holds `FinalBefore` at the start of such a run until its end bracket is final, so a Step per minute agrees with one Step over the same records. It holds only for runs with a credited second in the `LOOKUP_BRACKET_S` before them: once a run's start is final, that bracket is too, and a run with nothing there can't be a lookup whatever follows it. For a run that does have one, the hold has no bound, and WP-SC4's pending rule needs one.

## For Karim

Alongside the three rules the return-rule erratum offers:

1. Cap the run: a browser or chat-app run longer than some bound, for example `PENDING_WINDOW_S`, is no lookup. SM 8.3 already calls `LOOKUP_MAX_S` "a documentation lookup, not an evening of video".
2. Or keep the hold, and let a lookup's minutes be pending for as long as its run lasts.
3. Or let a lookup's credit land in a later revision of a final bucket, as that erratum's third rule would for the return rule.
