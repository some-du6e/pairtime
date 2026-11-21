# Erratum: DESIGN 4.4 doesn't say when a withdrawn or never-decided submission's spot-check window starts

Date 2026-11-20. Raised by the decision in `docs/errata/2026-11-20-evidence-versioning.md`, which gives WP-SV18 the evidence store's retention delete. Status: decided 2026-11-20 by the orchestrator for Karim (D-evidence-retention-wp): a withdrawal before any decision opens the spot-check window and deletes nothing, and a submission neither decided nor withdrawn opens it at its freeze (DESIGN 4.4).

## Input

DESIGN 4.4 keeps revealed evidence "until the spot-check window closes; 12 months after the decision until HQ sets a window". The witness rows and minute buckets a submission uses on the server, and its ledger records on the device, also stay until its window closes. A submission can be withdrawn (7.5 step 5), and nothing in WP-SB1's state machine ends one that is never decided.

## Expected

Every field has a finite retention period (4.4).

## What happens

A withdrawn or never-decided submission has no decision, so nothing starts its window. WP-SV14 (#34) has the withdraw code delete a submission's holds or set their decision time, and keeps an open submission's held rows while it stays open. Nothing says when the evidence of either one goes.

## Options

1. A withdrawal starts the window as a decision does, for evidence and for every hold, and an open submission keeps everything until it is decided or withdrawn.
2. A withdrawal releases everything at once. Its holds go, and its evidence is deleted.
3. Either of those, plus a limit on how long a submission may stay open before it counts as withdrawn.

## For Karim

Pick one rule for every row that uses the spot-check window. WP-SV18 needs it for evidence, and WP-SV14's holds should follow the same rule.
