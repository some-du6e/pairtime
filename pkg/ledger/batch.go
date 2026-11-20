// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// chainTag is the first byte of a batch chain link's SHA-256 input (spec
// 2, 6).
const chainTag = 0x03

// receiptContext starts every receipt message (spec 2, 7).
const receiptContext = "pairtime-receipt-v1"

// SignatureSize is the size of an Ed25519 signature.
const SignatureSize = ed25519.SignatureSize

// BatchHead returns H_b = SHA-256(0x03 || prev || root || u64(n) ||
// u64(b)), where prev is H_(b-1), zero(32) for batch 1 (spec 6). The
// server recomputes it from the uploaded prev, R_b, n and b, and refuses a
// batch whose head differs. BatchHead hashes whatever it is given; Seal
// and Chain.Verify are what refuse n = 0 and b = 0.
func BatchHead(prev, root Hash, n, b uint64) Hash {
	var in [1 + 2*HashSize + 8 + 8]byte
	in[0] = chainTag
	copy(in[1:], prev[:])
	copy(in[1+HashSize:], root[:])
	binary.BigEndian.PutUint64(in[1+2*HashSize:], n)
	binary.BigEndian.PutUint64(in[1+2*HashSize+8:], b)
	return sha256.Sum256(in[:])
}

// BatchRow is a sealed batch: what the device keeps for it in
// ledger/batches.seg, apart from that file's layout and its sealed_at,
// which no hash covers (WP-LG5).
type BatchRow struct {
	// B is the batch number, from 1 with no gaps.
	B uint64
	// FirstSeq is the s of the batch's first record. Its last record is
	// FirstSeq + N - 1.
	FirstSeq uint64
	// N is n_b, the number of the batch's records, at least 1.
	N uint64
	// Root is R_b, the MTH of the records' entries.
	Root Hash
	// Head is H_b.
	Head Hash
	// Receipt is the server's signed receipt, nil until it arrives.
	Receipt *Receipt
}

// Seal seals entries into batch b, chained to prev, the head of batch b -
// 1, or zero(32) for batch 1 (spec 6). The entries are those of the
// records written since batch b - 1, in sequence order. Seal refuses b =
// 0, no entries, sequence numbers that don't rise by 1, a batch 1 that
// doesn't start at s = 1 or whose prev isn't zero(32), and a header that
// Header.Check refuses. It returns the row without a receipt.
func Seal(prev Hash, b uint64, entries []Entry) (BatchRow, error) {
	if b == 0 {
		return BatchRow{}, errors.New("ledger: batch numbers start at 1")
	}
	if len(entries) == 0 {
		return BatchRow{}, errors.New("ledger: an empty batch is never sealed")
	}
	first := entries[0].Header().Seq()
	if b == 1 && (first != 1 || prev != zeroHash) {
		return BatchRow{}, errors.New("ledger: batch 1 starts at s = 1, after H_0 = zero(32)")
	}
	for i := range entries {
		h := entries[i].Header()
		if s, want := h.Seq(), first+uint64(i); s != want {
			return BatchRow{}, fmt.Errorf("ledger: batch %d has record %d where record %d belongs", b, s, want)
		}
		if err := h.Check(); err != nil {
			return BatchRow{}, fmt.Errorf("ledger: batch %d, record %d: %w", b, h.Seq(), err)
		}
	}
	n := uint64(len(entries))
	root := BatchRoot(entries)
	return BatchRow{B: b, FirstSeq: first, N: n, Root: root, Head: BatchHead(prev, root, n, b)}, nil
}

// Receipt is the server's signed receipt for a batch it stored (spec 7).
// It binds the server to ReceivedAt: the server can't later deny or
// re-date a head the device holds a receipt for, and the device can't
// claim the batch arrived earlier.
type Receipt struct {
	// ReceivedAt is when the server stored the batch, in Unix ms by its
	// clock.
	ReceivedAt uint64
	// Signature is Ed25519(server key, M_b).
	Signature [SignatureSize]byte
}

// ReceiptMessage returns M_b, the bytes a receipt signs (spec 7):
//
//	"pairtime-receipt-v1" || u8(len(device_id)) || device_id || u64(b) || H_b || u64(received_at)
//
// device_id must be an opaque key, and b at least 1.
func ReceiptMessage(deviceID string, b uint64, head Hash, receivedAt uint64) ([]byte, error) {
	if b == 0 {
		return nil, errors.New("ledger: batch numbers start at 1")
	}
	m := make([]byte, 0, len(receiptContext)+1+len(deviceID)+8+HashSize+8)
	m, err := appendDevice(append(m, receiptContext...), deviceID)
	if err != nil {
		return nil, err
	}
	m = binary.BigEndian.AppendUint64(m, b)
	m = append(m, head[:]...)
	return binary.BigEndian.AppendUint64(m, receivedAt), nil
}

// appendDevice appends u8(len(device_id)) || device_id. An opaque key is
// at most 128 bytes, so its length fits the byte before it (spec 7, 8).
func appendDevice(dst []byte, deviceID string) ([]byte, error) {
	if err := checkKey("device_id", deviceID); err != nil {
		return nil, err
	}
	dst = append(dst, byte(len(deviceID)))
	return append(dst, deviceID...), nil
}

// SignReceipt returns the receipt for batch b of deviceID with head,
// stored at receivedAt, signed with the server's key (spec 7).
func SignReceipt(key ed25519.PrivateKey, deviceID string, b uint64, head Hash, receivedAt uint64) (Receipt, error) {
	m, err := ReceiptMessage(deviceID, b, head, receivedAt)
	if err != nil {
		return Receipt{}, err
	}
	sig, err := sign(key, m)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{ReceivedAt: receivedAt, Signature: sig}, nil
}

// VerifyReceipt checks that r is a receipt for batch b of deviceID with
// head, signed under pub, which must be the server key that was valid at
// r.ReceivedAt (spec 7; DESIGN 10.2). Errors wrap ErrReceipt.
func VerifyReceipt(pub ed25519.PublicKey, deviceID string, b uint64, head Hash, r Receipt) error {
	m, err := ReceiptMessage(deviceID, b, head, r.ReceivedAt)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReceipt, err)
	}
	if !verify(pub, m, r.Signature) {
		return ErrReceipt
	}
	return nil
}

// sign returns Ed25519(key, msg) as RFC 8032 section 5.1 defines it,
// PureEdDSA with no context, which is what crypto/ed25519.Sign computes
// (spec section 1). Receipts and day heads are signed through it.
//
// It refuses a key whose public half, bytes 32 to 63, isn't the one its
// seed gives. crypto/ed25519 doesn't check that half and hashes it as A
// into k = H(R || A || M), so such a key would sign receipts and day heads
// that verify under no key at all.
func sign(key ed25519.PrivateKey, msg []byte) ([SignatureSize]byte, error) {
	var sig [SignatureSize]byte
	if len(key) != ed25519.PrivateKeySize {
		return sig, fmt.Errorf("ledger: an Ed25519 private key is %d bytes, got %d", ed25519.PrivateKeySize, len(key))
	}
	if !bytes.Equal(key[ed25519.SeedSize:], ed25519.NewKeyFromSeed(key.Seed())[ed25519.SeedSize:]) {
		return sig, errors.New("ledger: the Ed25519 private key's public half isn't the one its seed gives")
	}
	copy(sig[:], ed25519.Sign(key, msg))
	return sig, nil
}

// verify reports whether sig is pub's Ed25519 signature of msg. A key that
// isn't 32 bytes, nil included, verifies nothing.
func verify(pub ed25519.PublicKey, msg []byte, sig [SignatureSize]byte) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, msg, sig[:])
}
