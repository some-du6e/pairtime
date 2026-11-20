# Erratum: where the uncredited-codes rule puts the cap codes, `X.no_input` and a no-input turn's run

Date 2026-11-20. Raised by WP-SC2's review fix-up. Status: waiting for a decision. It follows on from [the 6.6 uncredited-codes erratum](2026-11-19-sm-6.6-uncredited-codes.md), whose rule `pkg/score` takes, and it was raised by the #52 review (#80).

## Input

- The uncredited-codes erratum's rule gives a second that nothing credits the first of five codes that applies: `X.absent`, `X.other_focus`, `X.ambiguous`, the running turn's code (`X.automated` if automation started the turn, `X.agent_only` if a person did), then `X.no_anchor`. "Discount codes (SM 4.2) and `X.no_input` stay where SM puts them, on the seconds a discounted or no-input turn's windows would have credited."
- SM 5.2 also lists `X.lookup_cap` and `X.bucket_cap`, "Caps (2.4, 2.5, 5.3)". SM 2.5 puts `X.lookup_cap` on a lookup run's seconds past `LOOKUP_MAX_S`. SM 2.4 caps the bucket at `BUCKET_MAX_S`, evicting the oldest output, but names no seconds for `X.bucket_cap`.
- SM 2.2 rule 8 makes a prompt with no input before it origin `no_input`, which opens no anchor. Its turn still runs.

## Expected

Every uncredited second gets one code, and the rule says which.

## What the rules give

The rule places neither cap code. It places `X.no_input` on the seconds the lead of a no-input prompt would have covered had a person typed it, but not against the five codes. And its fourth code covers a turn that automation or a person started, so it names no code for the seconds a no-input turn runs, whose origin is neither.

## What the scorer does

It takes the rule, with these additions, in this order:

1. `X.absent`.
2. `X.lookup_cap`, on a present second of a lookup run past `LOOKUP_MAX_S`.
3. `X.other_focus`, then `X.ambiguous`.
4. `X.no_input`, on a second the lead of a no-input prompt of the focused project would have covered.
5. `X.bucket_cap`, on a present, focused second within `READ_HORIZON_S` of a human act whose bucket is empty only because `BUCKET_MAX_S` evicted output that hasn't yet expired. The scorer keeps what the cap evicted as a shadow of the bucket, which resets and expires as the bucket does, and drains a second of it on each such second.
6. While a turn of the focused project runs: `X.agent_only` if a person started it, `X.automated` if automation did, and `X.agent_only` for a no-input turn, SM 5.2's code for "an agent turn ran and nothing credited the second". When turns of several kinds run, the first of these wins.
7. `X.no_anchor`.

No row that `pkg/score`'s tests check depends on these. 6.6's uncredited seconds aren't checked until the uncredited-codes erratum is decided, and no other timeline the scorer runs on has a lookup past its cap, a bucket past its cap or a no-input prompt. The lookback timelines use `X.bucket_cap` under [the lookback erratum](2026-11-19-sm-2.8-lookback.md)'s own reading, for WP-SC7. `creditor.uncredited` in `pkg/score/credit.go` is the code, and each choice is one line there.

## For Karim

1. Confirm where the two cap codes go, or move them. `X.bucket_cap` before the running turn's code says a person read past what the cap let them bank, which tells a reviewer more than `X.agent_only` would.
2. Confirm `X.agent_only` for the run of a no-input turn, or give it `X.no_input` too. `X.no_input` there would mark the whole run of a scripted prompt, not only where its lead would have been.
3. Whatever is chosen belongs in SM 5.2 with the uncredited-codes rule.
