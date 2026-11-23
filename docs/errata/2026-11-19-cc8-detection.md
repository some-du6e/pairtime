# Erratum: DESIGN 7.3's CC8 detection figure doesn't hold against a client that plans ahead

Date 2026-11-19. Raised by the ledger fix-up for issue #19, from the review of #12 (WP-LG3). Status: decided 2026-11-20 by the orchestrator for Karim (#15): accept the weaker guarantee. CC8 reveals no keyed value and keeps only the checks that need no key. DESIGN 7.3 restates what it catches, and binding the key is rejected.

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

## Decision

Decided 2026-11-20 for Karim in issue #15: accept the weaker guarantee and reveal no keyed value.

- CC8 checks only what needs no key (DESIGN 7.3, spec 10.8): `es` against every line a reveal shows whole; on a reveal that removed nothing, `len_chars`, `low_effort`, `es` and `pc` against the text; and equal `th`, `mh` and `es` between one submission's revealed turns of one chain whose normalized texts or error signatures are equal.
- DESIGN 7.3 keeps 0.65 only for those per-turn fields, on a turn that isn't withheld and whose reveal kept the text the check needs. Features a client keys itself are caught only through a same-text turn: under 0.07 for one loop of five identical prompts with three such turns in a pool of 100, and never for reworded repeats.
- The per-shingle reveal is dropped. A hidden value undid its redaction, and a shown one let the server probe every committed sketch for that phrase, while it caught only a client that falsified a sketch and then revealed true values.
- Binding the key is rejected. A verifiable random function such as RFC 9381's RSA-FDH-VRF would let the server check revealed values, but only on drawn turns whose reveals remove nothing. Withholding, removal by hand, reworded repeats and the never-revealed `agent.tool` and `shell.command` features would stay open, and shown values would keep the probe leak.
- DESIGN 7.12 accepts what remains, a modified client that forges its dark-flow inputs, as T9 residue.
