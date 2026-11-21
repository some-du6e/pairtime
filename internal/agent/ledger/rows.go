// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"path/filepath"
	"time"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// Row is a sealed batch's row in batches.seg, without its records: what
// the uploader sends of a batch, {b, n, R_b, H_b, prev} (DESIGN 4.3), when
// the batch was sealed, and the server's receipt once batches.seg holds
// one (WP-AG5).
type Row struct {
	// BatchRow is the batch's b, first_seq, n, R_b and H_b, with its
	// receipt, which is nil until StoreReceipt keeps one.
	ledger.BatchRow
	// Prev is H_(b-1), zero(32) for batch 1.
	Prev ledger.Hash
	// SealedAt is when the batch was sealed.
	SealedAt time.Time
	// Online is set when the batch was sealed online, as its seal frame
	// records (Batch.Online).
	Online bool
}

// ReadRows reads which chain the ledger in dir holds and the rows of its
// sealed batches whose number is from or more, in order, each with its
// receipt when batches.seg holds one. A from of 0 reads from batch 1.
//
// It reads batches.seg alone, as it is, without Open's repairs and
// without the batches' records. A row that a crash lost, which the next
// Open rebuilds from its seal frame, is missing until then, and so are
// the rows after it. It stops quietly at a frame that isn't whole, such
// as one a Writer is still writing, so it can read a ledger a Writer has
// open.
func ReadRows(dir string, from uint64) (ChainInfo, []Row, error) {
	bf, err := readBatchesFile(filepath.Join(dir, batchesName))
	if err != nil {
		return ChainInfo{}, nil, err
	}
	info := ChainInfo{DeviceID: bf.chain.DeviceID, KeyID: bf.chain.KeyID, Created: time.UnixMilli(bf.chain.Created)}
	from = max(from, 1)
	if from > uint64(len(bf.rows)) {
		return info, nil, nil
	}
	rows := make([]Row, 0, uint64(len(bf.rows))-from+1)
	for _, rp := range bf.rows[from-1:] {
		r := Row{
			BatchRow: ledger.BatchRow{B: rp.B, FirstSeq: rp.FirstSeq, N: rp.N, Root: rp.Root, Head: rp.Head},
			Prev:     rp.Prev,
			SealedAt: time.UnixMilli(rp.SealedAt),
			Online:   rp.Online,
		}
		if rc, ok := bf.receipts[rp.B]; ok {
			r.Receipt = &rc
		}
		rows = append(rows, r)
	}
	return info, rows, nil
}

// RowOf returns the row of a batch the Writer sealed, as ReadRows would
// read it, given prev, the head of the batch before it: Batch doesn't
// carry it, and its holder knows it from that batch.
func RowOf(b Batch, prev ledger.Hash) Row {
	return Row{BatchRow: b.Row, Prev: prev, SealedAt: b.SealedAt, Online: b.Online}
}
