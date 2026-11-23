# Erratum: the device keeps presence, focus, typing counts and MinHash sketches only inside ledger records, which DESIGN 4.4 keeps 180 days, not 90

Date 2026-11-23. Raised by WP-LG6 (#204), which builds the device's retention sweeps. Status: decided 2026-11-23 by the orchestrator for Karim: on the device they are kept with the ledger records that hold them, 180 days, or longer while a submission using them is inside its spot-check window. DESIGN 4.4's row and docs/retention.json's `device.activity` entry say so, in the docs PR that adds this erratum.

## Input

- DESIGN 4.4, device rows: "Presence and focus slots, opt-in typing aggregates, MinHash sketches | 90 days", and "Ledger records and batch roots | 180 days, or longer while a submission using them is inside its spot-check window".
- DESIGN 3.6: a record's body is `{n, e, f, c}`, and "Body bytes are stored verbatim. Verification hashes the stored bytes". Each body's hash is in its entry, every entry is under its batch's root `R_b`, and the server holds each batch's head and gives a signed receipt for it.
- DESIGN 3.6: "The header is revealed for every record in a revealed batch, so the server can demand full bodies of every presence, power and focus record".
- DESIGN 4.4 and 3.5: the ledger is kept 180 days because that is the submission horizon (TP B8).
- On the device, the 90-day row's data exists only inside ledger record bodies. Presence samples and their opt-in typing counts are `os.presence` records' `e`. Focus slots are `os.focus` records' `e`. Each prompt's MinHash sketch is its record's `f.mh` (ledger-v1 10.7). No other store holds them: the scorer's `buckets/` and `days/` hold its output, and `state.json` holds no samples.

## Expected

A sweep that deletes what DESIGN 4.4 lists, and nothing a submission still needs.

## Computed

Nothing can delete these at 90 days without deleting the ledger records that hold them, or rewriting those records' bodies:

- A rewritten body no longer matches the hash in its entry, so its batch no longer verifies against the root and head the server witnessed.
- A deleted record takes from the server the body it can demand for any batch a submission reveals. Under the 180-day submission horizon, that can be a batch from 90 to 180 days ago.

The 90-day row is TP B8's, written for a local database with a field per kind of data. The ledger design (DESIGN 3.6) put those fields in record bodies instead.

## Decision

On the device, presence and focus slots, opt-in typing aggregates and MinHash sketches are kept with the ledger records that hold them: 180 days, or longer while a submission using them is inside its spot-check window. WP-LG6's sweep deletes them when it deletes their ledger segment, and never sooner.

DESIGN 4.4's row becomes:

| Where | Data | Kept |
|---|---|---|
| Device | Presence and focus slots, opt-in typing aggregates, MinHash sketches, which the device keeps only inside ledger records | With the ledger records that hold them: 180 days, or longer while a submission using them is inside its spot-check window |

`docs/retention.json`'s `device.activity` entry takes the same period, so `/privacy` and the dashboard's Privacy page never promise a deletion at 90 days that doesn't happen. `TestDesignPeriods` holds its days to the new row.
