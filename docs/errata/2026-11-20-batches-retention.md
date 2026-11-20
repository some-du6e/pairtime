# Erratum: the ledger writer's files can't drop batches older than DESIGN 4.4's 180 days

Date 2026-11-20. Raised by the fix-up for issue #99, from the spec review of #94 (WP-LG5). Status: waiting for a decision. Nothing changes in this PR; the question is which format change WP-LG6's retention sweep gets.

## Input

- DESIGN 3.5's table keeps `ledger/YYYY-MM-DD.seg` for 180 days, and `ledger/batches.seg` "180 days, or until every submission using them is past its spot-check window".
- DESIGN 4.4 keeps the device's "Ledger records and batch roots" for "180 days, or longer while a submission using them is inside its spot-check window".
- WP-LG6 builds "retention sweeps for every device row of DESIGN 4.4", touching `internal/agent/vault/` and `internal/agent/retention/` only.
- The writer (#94, `internal/agent/ledger`): `batches.seg` is one file, a chain frame and then a row per batch from `b = 1` with `first_seq = 1`, appended forever. `readBatchesFile` refuses rows that don't start there, and `ReadBatches(dir, 1, ...)` needs batch 1's segment.

## Expected

Once a segment is more than 180 days old and no submission uses its batches, the sweep deletes it and the rows of its batches, and the writer and its readers go on with the batches that are left.

## What the writer does

- Delete the oldest segment, and `ReadBatches` from batch 1 fails with `ErrDamaged` ("the segment of ..., where batch 1 is, isn't there"). Open still works, because it reads only the newest batches, but WP-AG4's rebuild from the ledger doesn't.
- Drop the oldest rows from `batches.seg`, and Open returns `ErrDamaged`, so the agent records nothing.
- Leave both, and `batches.seg` grows by a row of about 350 bytes per batch, about 170 KB for a day of a batch a minute, which every Open and `ReadChain` decodes in full.

No API tells a caller which batch is the first one left, either.

## Proposal

A base frame, which the writer can add without breaking a ledger that has none:

1. `batches.seg` may hold a base frame right after its chain frame: `{b, first_seq, prev}` of the first batch it keeps. With none, the base is batch 1 with `first_seq = 1` and `prev = zero(32)`, as today.
2. `readBatchesFile` accepts rows from the base on, with the base's `first_seq` and `prev`. Open checks that the first kept row's segment exists.
3. The sweep writes a new `batches.seg` by atomic rename, holding the chain frame, a base frame, and the rows and receipts from the first kept batch on, and only then deletes the segments wholly before that batch's first record. A crash leaves the old file or the new one, and segments the new one doesn't need, which the next sweep deletes. An open Writer appends rows and receipts through its own handle on `batches.seg`, so the sweep is a Writer method, or runs only while no Writer is open. As a method it holds `sealMu`, which every append to `batches.seg` takes, from before it reads the old file until the Writer's handle is on the new one. It closes the old handle before the rename, because Windows can't rename over an open file, and opens the new file after it. Otherwise rows and receipts appended meanwhile would go to the old file, which the rename unlinks.
4. `ReadBatches` starts at the larger of `from` and the base, and `ChainInfo` gets the base's batch number, so the scorer's rebuild and the uploader know where the device's kept chain starts.
5. A kept chain verifies from its base with `prev` taken as given, as ledger-v1 9 step 1 takes "a run that starts later". The server already holds the earlier heads, so nothing about witnessing changes.

The extension needs no spec change, because ledger-v1 4 leaves the files' layout to the writer. It does change what DESIGN 3.5's table promises about `batches.seg`, since a row then goes when its batch's segment goes.

## For Karim

Decide whether WP-LG6's sweep takes this format, or another. It isn't urgent for the format's sake, because a `batches.seg` without a base frame keeps meaning "from batch 1", but the sweep can't run before it lands, and WP-LG6's Touches line needs `internal/agent/ledger/` added.
