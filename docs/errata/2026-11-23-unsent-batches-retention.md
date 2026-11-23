# Erratum: a sweep that drops the rows of online batches the server hasn't stored stops a linked computer's uploads for good

Date 2026-11-23. Raised by the review of #206 (WP-LG6 part 2, the device's retention sweeps), issue #208. Status: decided 2026-11-23 by the orchestrator for Karim, in the ruling on #208: while account.json links the computer, the sweep keeps the rows of the online batches the server hasn't stored, until it stores them, and deletes their segments at 180 days all the same. DESIGN 3.5's `batches.seg` row and 4.4's ledger row say so, in the PR that adds this erratum. The Decision section below says what the sweep and the uploader do.

## Input

- DESIGN 4.4 keeps the device's "Ledger records and batch roots" for "180 days, or longer while a submission using them is inside its spot-check window", and DESIGN 3.5 says of `ledger/batches.seg`: "A batch's row goes when its batch's segment goes, and a base frame then names the first batch the file keeps" (docs/errata/2026-11-20-batches-retention.md).
- docs/spec/ingest-v1.md 2.7, batch roots, step 5: "with the stored head at batch `h`: `b = h + 1` needs `prev` equal to the stored `H_h` (`prev_mismatch`). `b` above `h + 1` is `chain_gap`, unless the device signed in again since the server stored its head and this is the first batch it stores under the new sign-in". 2.9: "A `chain_gap` root goes through once the missing batches are stored". 5.3: "Only a sign-in resets what the chain expects next: without one, a skipped batch is `chain_gap`".
- The server keeps a device's chain head on its device row, which outlives the witness rows that its retention deletes at 180 days (AGENTS.md, Retention), so a head stays the head however old it is.
- The uploader sends batch roots in order from the batch after the server's head. The agent stops the uploader before the ledger writer's Close seals the last batch, and a batch sealed between upload cycles waits for the next one, so a computer usually shuts down with a batch or two the server hasn't stored. One that can't reach the server, or is switched off for months, keeps them unsent for as long.

## Expected

A sweep that deletes the ledger's records and batch roots once they are 180 days old, and a computer that, once it reaches the server again, uploads what it recorded since.

## Computed

The sweep drops every batch whose first record is in a segment of a day before its `from`, whether or not the server stored its root. On a linked computer whose server holds the chain up to batch `h`, a sweep that drops batch `h + 1` leaves no root that can link: the root of the first kept batch is above `h + 1`, so ingest refuses it as `chain_gap`, and the batches in between can't be stored any more, since the device no longer has them. The uploads stop for good, the minutes of the kept batches included, which wait for receipts that never come. `pairtime logout` and `pairtime login` let the next root skip the gap, but the chain then goes on from the first batch sealed after the new link, so the online batches kept from before it are never sent either. #206 said the server would see the chain start at a later batch; that holds only for a device of which the server holds no batch.

ingest-v1 sets no age limit on a root: 2.7 checks a root's form, its head, a duplicate or a conflict, and how its `b` and `prev` link to the stored head (steps 1 to 5), and nothing about when it was sealed. A root kept past 180 days therefore links as `b = h + 1` whenever it is sent.

## Decision

Decided 2026-11-23 by the orchestrator for Karim (#208):

- While account.json links the computer, and no fork or `device_mismatch` has stopped its chain, the sweep keeps the rows of the batches from the uploader's first online batch the server hasn't stored on: its next root's batch, past the runs of batches sealed in local-only mode, which never go (`uploader.HeldFrom`). Those rows hold the batches' roots, seal times and chain fields. Their segments, which hold the records, go at 180 days all the same, so no record stays past its time. The rows go once the server has stored their roots, at the next sweep.
- `Writer.Sweep` takes that batch as a floor for its base. The readers pass over a kept row whose segment is gone: `ReadBatches` starts at the first batch whose first record the ledger still holds, as `FirstHeld` does, and Open reads on from the first segment after the newest row's end.
- Backstop: when the root that goes next is of a batch the ledger no longer keeps, as after a server restored from a backup that lost its last writes, the uploader stops and asks the teen, through `pairtime doctor`, to run `pairtime logout`, then `pairtime login`, as after a local-only stretch.

With that, a computer that comes back after a long time away sends its kept roots as `b = h + 1` and on, and the server links them; their minutes go once their receipts are in.

DESIGN 3.5's `batches.seg` row, in its Retention column, becomes:

> 180 days, or until every submission using them is past its spot-check window. A batch's row goes when its batch's segment goes, and a base frame then names the first batch the file keeps, or the next one to be sealed when it keeps none. While account.json links the computer, the rows of the online batches the server hasn't stored stay until it stores them, without their segments, which go at 180 days all the same (docs/errata/2026-11-23-unsent-batches-retention.md)

DESIGN 4.4's device row for ledger records and batch roots, in its Kept column, becomes:

> 180 days, or longer while a submission using them is inside its spot-check window; while the computer is linked, the batch roots of online batches the server hasn't stored stay, without their records, until it stores them

docs/retention.json's `device.ledger` entry says the same, for the privacy notice.

## Also in this PR

The same review asked for these, which change no rule above:

- A sweep that finds every batch older than its `from` drops them all, the newest one's row and segment included, and writes a base frame that names the batch to be sealed next, with no row. The writer then goes on from the dropped head, so an idle computer keeps nothing past its time.
- A `batches.seg` with a base frame has a chain frame of version 2. pairtime 0.1.0-alpha.5 and the versions before it read only version 1, and refuse any other at the chain frame, before they would read the base frame they don't know and cut it off as a torn tail.
