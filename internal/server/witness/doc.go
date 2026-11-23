// SPDX-License-Identifier: AGPL-3.0-or-later

// Package witness gives pairtimed's own witness labels (WP-SV6; DESIGN
// 7.4, 6.2 D6): witnessed, offline or backfilled, from when the server
// received what a device sent, on the server's clock. The client sends a
// reading of its own with every minute bucket, which ingest stores as
// sent (ingest-v1 7 item 13). This package never trusts it to raise a
// label, only to lower one.
//
// # Records
//
// Records labels a device's ledger records, as a submission reveals them
// (WP-SB4; DESIGN 7.5 step 3). Each record gets one label from its batch's
// received_at minus its corrected time.
//
// The corrected time is the record's own wall time, re-anchored from the
// monotonic clock within one boot (SP 8.4). A record was made before every
// record of its boot whose monotonic clock read later, by the monotonic
// time between them, and each of those existed when its batch reached the
// server. So the server's clock, carried back along the boot's monotonic
// clock, bounds when a record can have been made, whatever the device's
// wall clock said. The corrected time is the earliest of the record's wall
// time and those bounds. That needs every mono to be a reading of the
// boot's clock taken at its record's own time, or none: one derived from
// the wall clock could run ahead of the boot's clock and pull every
// earlier record of the boot back with it.
//
// A record's credit lands at its wall time, which the client scored and a
// submission re-scores, so no correction may make a record read later, and
// fresher, than its wall time says. The corrected time is never after it.
// The other direction is safe. A wall clock that runs up to FutureMS fast,
// or jumped ahead in the boot, reads by the server's clock instead, as
// does a record held back and sent late when a later record of its boot
// reached the server on time. A monotonic clock that stopped while the
// machine slept only makes a bound looser, never wrong, and two equal
// readings bound nothing, since such a clock can read the same on both
// sides of a sleep. A wall clock set back proves nothing about the records
// before it and moves none of them. What no correction can fix is a wall
// clock that runs slow: a minute it dates 20 minutes early reads 20
// minutes late, because a backdated record looks the same.
//
// A record dated more than FutureMS after its batch reached the server is
// refused and gets no label (SP 8.4). The check reads the wall time, where
// the record's credit lands. The corrected time can't run ahead of the
// receipt, so checking it would let any later record of the boot pull a
// record dated ahead back under the limit. So on a device whose clock runs
// more than FutureMS fast, every record sent live is refused.
//
// Then each record is witnessed when its lag is at most WITNESS_LAG_S. It
// is offline when it is late but its batch waited out an outage: the batch
// closed within the lag after the record, an upload-failure note falls
// within the lag of the batch's close, before or after it, in a batch of
// the same catch-up or a later one, the batch arrived within the lag after
// the device got through again, and the batch before that reconnect, the
// catch-up's anchor, holds. An anchor holds when it has a record that
// reads witnessed or offline, or when it came itself in an earlier
// catch-up whose anchor holds, by the run rules a catch-up's records use:
// its run began after a silence, and it arrived within the lag after the
// run's first batch. So a backlog that reached the server in several
// catch-ups ran on unbroken from a batch with a record that reads
// witnessed. A batch closes no earlier than its latest record and no
// earlier than the batch before it, since a chain seals its batches in
// order. So every batch of a catch-up closed no earlier than the time of
// the witnessed record its chain of anchors goes back to, and that time is
// at most the lag before that record's batch reached the server, whatever
// records a caller leaves out: a batch of backdated records sent after the
// device got batches of the present through can't read offline. Which
// records inside that bound read offline can still depend on what a caller
// passes. A record no rule reveals, dated late in a batch of a first
// catch-up, raises the close of every batch after it, so leaving it out
// lets records of a second catch-up read offline that read backfilled with
// it. Everything else late is backfilled, and so is every record the
// caller marks as lookback's.
//
// The notes are the client's own claim (DESIGN 7.4), so the server can't
// check them beyond their place in the chain. It reads their times from
// their bodies, which DESIGN 6.5's rule (g) reveals with the presence,
// focus and power records of each catch-up's chain of anchors.
//
// D6 applies OFFLINE_CAP_S and OFFLINE_PUSH_S at submission, in WP-SB4,
// not here. What it needs from here is which records are offline and the
// stretch each belongs to: one per catch-up, with the time the device got
// through again, from which OFFLINE_PUSH_S counts.
//
// # Buckets
//
// Bucket labels each minute-bucket revision ingest stores. Ingest sees
// batch roots and buckets, never records, so it reads the bucket itself:
// the minute, on the device's clock, against when the revision arrived.
// A revision that arrived within the lag of its minute's end is witnessed,
// once the server holds a batch of the device received after the minute,
// less FutureMS for a fast clock: the client sends a minute only after
// such a batch has a receipt. A later revision with the same credit as the
// one it replaces keeps that one's label, since it adds nothing the server
// didn't already hold: a pending minute turning final is the common case.
// A late revision the client reads as offline is offline when the server
// saw the device cut off and catch up around it: the device's first batch
// received after the minute ended came more than the lag after it, the
// revision came within the lag after that batch, and the batch before
// that one is on the chain, received before the minute ended. Every other
// late revision is backfilled. The label is never better than the client's
// reading, which knows what the server can't, such as which minutes are
// lookback's.
//
// Without records that reading can be wrong either way. A late revision
// whose new credit rests on records the server had on time, as a restore of
// half-weighted seconds does (SM 4.3), reads backfilled here, and so does a
// minute whose bucket waited out an outage that began after its batch got
// through, or that ended before the first of two catch-ups and whose batch
// went in the second, since the device's first batch received after the
// minute is then the first catch-up's. And a client that writes a minute's
// records into batches it sends late, while it sends the minute and other
// batches on time, gets witnessed here and late labels for the records. A
// submission labels the records themselves, and its labels are the ones
// that count.
package witness
