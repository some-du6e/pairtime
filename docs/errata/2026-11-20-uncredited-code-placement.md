# Erratum: where the uncredited-codes rule puts the cap codes, `X.no_input` and a no-input turn's run

Date 2026-11-20. Raised by WP-SC2's review fix-up. Status: decided 2026-11-23 by the orchestrator for Karim, with the 6.6 uncredited codes (E-6.6-codes). SM 5.2's order puts the codes for withheld credit, the two cap codes among them, ahead of every other code and `X.no_input` on a no-input turn's run, as `pkg/score` has done in sampled slots since #105, and a live slot whose presence is unknown takes the same turn step. It follows on from [the 6.6 uncredited-codes erratum](2026-11-19-sm-6.6-uncredited-codes.md), whose rule `pkg/score` takes, and it was raised by the #52 review (#80). SM 5.2's rows and order, SM 5.4's `X.automated` sentence and the 6.6 erratum's sentence that leaves this erratum the cap codes and live slots came in #271 (WP-ERR1, part 7). SM 5.2's paragraph on live slots whose presence is unknown, the relayed-prompts erratum's reading 3, the relayed fixtures' comments and README, and `pkg/score`'s shared turn step come in one PR under `oracle-change` after #222, and that PR takes `pkg/score/VERSION`'s next MINOR.

## Input

- The uncredited-codes erratum's rule gives a second that nothing credits the first of five codes that applies: `X.absent`, `X.other_focus`, `X.ambiguous`, the running turn's code (`X.automated` if automation started the turn, `X.agent_only` if a person did), then `X.no_anchor`. "Discount codes (SM 4.2) and `X.no_input` stay where SM puts them, on the seconds a discounted or no-input turn's windows would have credited."
- SM 5.2 also lists `X.lookup_cap` and `X.bucket_cap`, "Caps (2.4, 2.5, 5.3)". SM 2.5 puts `X.lookup_cap` on a lookup run's seconds past `LOOKUP_MAX_S`. SM 2.4 caps the bucket at `BUCKET_MAX_S`, evicting the oldest output, but names no seconds for `X.bucket_cap`.
- SM 2.2 rule 8 makes a prompt with no input before it origin `no_input`, which opens no anchor. Its turn still runs.

## Expected

Every uncredited second gets one code, and the rule says which.

## What the rules give

The rule places neither cap code. It places `X.no_input` on the seconds the lead of a no-input prompt would have covered had a person typed it, but not against the five codes. And its fourth code covers a turn that automation or a person started, so it names no code for the seconds a no-input turn runs, whose origin is neither.

## What the scorer does

This was #52's code. #105 replaced it with the order in the Decision below.

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

## Decision

Decided 2026-11-23 by the orchestrator for Karim, with the 6.6 uncredited codes (E-6.6-codes), whose order SM 5.2 gives.

1. The cap codes come first, with the other codes for credit a rule withheld, and only on seconds that rule would have credited: a loop's discount code where SM 4.2 counts the second as part of what the loop cost; `X.lookup_cap` on a qualifying lookup run's present seconds past `LOOKUP_MAX_S`; `X.bucket_cap` on a second that would have drained output `BUCKET_MAX_S` evicted (SM 2.4), or output `UNKNOWN_BUCKET_MAX_S` cut; and `X.day_cap` past `DAY_CAP_S`. `X.noprog` is the uncredited half of a half-weighted second. Outside relayed credit, a drain or a lookup needs presence and focus, so ranking these codes ahead of `X.absent`, `X.other_focus` and `X.ambiguous` changes a second only where presence or focus is unknown or the credit is relayed, and there the cap is why the second isn't credited. `X.bucket_cap` also comes before a running turn's code, as point 1 of For Karim proposed.
2. `X.no_input` goes on a no-input turn's run and nowhere else. While a turn of the slot's focus project runs, the code is `X.agent_only` if a person started one of the running turns (a relayed turn counts), else `X.automated` if automation started one, else `X.no_input`. So a scripted prompt's whole run is marked, apart from a person's turns, as point 2 of For Karim proposed. The seconds a no-input prompt's lead would have covered take whatever code applies, most often `X.no_anchor`. SM 2.2 classes only a person's prompt, so that lead has no length, and placing it would recode seconds up to 420 s before the prompt.
3. A live slot whose presence is unknown takes the same turn step, over the turns of every project. A `no_input` turn's run there is `X.no_input`, not the `X.agent_only` that D-relayed's reading 3 took from step 6 above, and an automated turn running beside it makes the second `X.automated`. That holds in every live slot SM 2.1 calls `unknown`: a server's, a desktop's that gives no idle time (KDE, wlroots), and a desktop's slots before its first sample or after its sampler stops (WP-HL1). A prompt there with no input on record before it and no app message behind it is `no_input` (SM 2.2 rule 8; SM 3.4 says the same of a remote box), so its run is `X.no_input`, which says what pairtime saw, even when a person typed it out of pairtime's sight. Only relayed anchors credit such slots, so no credit moves, and the run still counts in `agent_only_s`, the agent's time in pairtime's views.
4. SM 5.2's order and its paragraph on live slots whose presence is unknown, once the PR that applies this decision edits it, say all of this, as point 3 of For Karim asked.

The last paragraph of What the scorer does no longer holds. relayed/t3-unattended's golden carries `X.bucket_cap` ahead of C6's `X.automated` in 16:50, and relayed/t3-away's carries it ahead of `X.no_anchor`. `pkg/score`'s tests pin a no-input turn's run in sampled slots, and the PR that applies this decision pins it in live slots whose presence is unknown. No credited second, fixture row or SM worked number moves, and that PR changes the goldens only in `v`, since it raises the scoring version's MINOR.
