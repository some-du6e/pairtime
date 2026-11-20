// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ledger is the agent's ledger writer (DESIGN 3.3, 3.5, 3.6;
// docs/PLAN.md, WP-LG5). It turns each event into a record with
// pkg/ledger, appends it to the device's ledger, seals the records into
// batches, chains the batches, and recovers what a crash leaves behind.
// docs/spec/ledger-v1.md fixes every byte that is hashed; the layout of the
// files around those bytes is this package's.
//
// # Files
//
// The ledger directory, home.Dirs.Ledger(), holds:
//
//   - YYYY-MM-DD.seg, one per UTC day the writer wrote in: the records of
//     that day, each a record frame holding its 33-byte header and its
//     body as stored, and after each batch's last record a seal frame
//     with the batch's b, first_seq, n, prev, R_b, H_b and sealed_at.
//   - batches.seg: a chain frame naming the device id and the device
//     key's id, then a row per batch, {b, first_seq, last_seq, n, prev,
//     R_b, H_b, sealed_at} and where its frames are, and the server's
//     receipts with their received_at (DESIGN 3.5; ledger-v1 7).
//   - old/<key id>/: the chain of a device key this installation no
//     longer writes with, kept for reading.
//
// A frame is u8(kind) || u32(len) || payload || u32(CRC-32C), big-endian.
//
// # Numbers
//
// Every record the device writes, checkpoints and upload-failure notes
// included, takes the next number from 1, with no gap or repeat, and an
// event's seq is the number of the record that holds it (ledger-v1 4.1).
// Only records in a sealed batch keep their numbers: a crash can cut
// records no batch holds yet, and after the restart the next record takes
// the number after the last one that survived. Nothing of a cut record
// left the device, so no nonce or salt anyone has seen is used twice. A new
// K_dev, as after a restore, starts a new device whose chain begins at 1
// (decision E-sm-1.1-seq-gaps, ledger-v1 3).
//
// # Durability
//
// A record is written with one write call and no flush. A seal writes its
// seal frame after the batch's records, flushes that segment once, and
// then appends the row to batches.seg without a flush: one fsync per seal.
// Open rebuilds a row a power cut lost from its seal frame, cuts a torn
// frame off the newest segment, and seals the records that survived
// without a batch. Batches leave the device, through Options.Sealed, only
// once they are on disk.
package ledger
