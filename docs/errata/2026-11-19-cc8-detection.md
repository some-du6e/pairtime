# Erratum: DESIGN 7.3's CC8 detection figure doesn't hold against a client that plans ahead

Date 2026-11-19. Raised by the ledger fix-up for issue #19, from the review of #12 (WP-LG3). Status: waiting for Karim, who decides CC8's role in issue #15. Until then DESIGN 7.3 keeps its figure, and `docs/spec/ledger-v1.md` 10.8 says what CC8 can and can't check.

## Input

A modified client that commits each prompt's MinHash sketch `mh` under a key it makes up for that prompt, instead of under the device's `K_feat`, and reveals per-shingle values under the same made-up key when the prompt is drawn (spec 10.5, 10.8). Its aim is to hide re-rolls from the server's re-score (DESIGN 6.2 D18): each falsified sketch looks unlike the prompt before it.

## Expected

DESIGN 7.3's CC8 row: "a 10-turn sample catches one that falsifies 10% of prompts with probability 0.65". That is `1 - 0.9^10`, so it assumes each falsified turn that is drawn fails CC8 on its own.

## Computed

A drawn turn whose values come from the made-up key reproduces its committed sketch exactly, so it passes spec 10.8's `sketch` check. The server can't check a value against `K_feat`, which only the device holds. CC8 catches the client only through its checks across reveals (spec 10.8): when two revealed turns share a shingle and show different values for it, or two revealed prompts with equal normalized texts or equal error signatures carry different `th` or `es`. So the chance of catching it depends on how often two drawn turns share a shingle, not on 10% of turns failing one by one, and a span removed by hand over the shared words hides even that. Against a client that falsifies sketches and then reveals the true values, which don't reproduce the falsified sketch, the figure holds.

The same holds for `es`. A client that keeps honest sketches but writes a fresh made-up `es` on each pasted error keeps its pastes out of SM 4.2's count of that error and makes each paste a new error signature, a change event that ends re-roll, rewrite and autopilot counts. A paste never resets the error's count, which only the person's own change events and the error's clearing do (SM 4.2). CC8 sees it only when two revealed prompts share a signature.

## Reasoning

DESIGN 7.3's figure is a design number, which ground rule 6 (PLAN.md) says goes to Karim when the spec disagrees with it. The spec can't close the gap by itself: either the server learns something that binds a client's values to its key, or CC8's guarantee is restated. Issue #15 lists the three options:

1. **Accept it.** Restate CC8's guarantee in DESIGN 7.3 and lower or drop the figure.
2. **Bind the key.** Commit to the MinHash key, or an input to its derivation, early and verifiably, so that the server can recompute the shingle values of revealed turns.
3. **Drop CC8's dark-flow role.** Rely on other checks, and say so.

## For Karim

Pick one of the three in issue #15. If it is the second, the ledger spec needs a new committed value. No device has witnessed data yet, so version 1 can still take it, but only until the first device uploads; WP-TR2 waits for that spec change.
