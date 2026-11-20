// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// EvidencePrefix is where the evidence store keeps sealed blobs in the
// main bucket, next to Litestream's replica (DESIGN 10.1).
const EvidencePrefix = "evidence/"

// MaxEvidenceSize bounds one sealed blob. DESIGN 10.1 expects about 1 MB
// of evidence per 10 submitted hours, so this leaves a wide margin and
// still keeps a damaged object from filling memory.
const MaxEvidenceSize = 64 << 20

// ErrDigestMismatch: a stored blob's bytes don't hash to its key, so the
// bucket's copy was changed or damaged after it was written.
var ErrDigestMismatch = errors.New("objstore: the blob's SHA-256 doesn't match its key")

// Evidence is the evidence store: sealed blobs, each written once under
// the hex SHA-256 of its bytes (DESIGN 7.6, 10.1, 10.3). The blobs are
// ciphertext, sealed by pkg/seal before they arrive, so the bucket holds
// no content anyone can read without the evidence key.
//
// Write-once puts and Get's digest check are the only protections. R2,
// the pilot's store, has no bucket versioning, so anyone with the
// bucket's key can delete a blob or overwrite it with a plain PUT, and
// no earlier version survives. Get then fails with ErrNotFound or
// ErrDigestMismatch, but nothing can bring the blob back, since
// Litestream and the nightly snapshot copy only the database. DESIGN
// 10.3's evidence row asks for versioning anyway, and
// docs/errata/2026-11-20-evidence-versioning.md puts that to Karim.
type Evidence struct {
	b *Bucket
}

// NewEvidence returns the evidence store in bucket b.
func NewEvidence(b *Bucket) *Evidence { return &Evidence{b: b} }

// EvidenceKey is the object key of the blob whose SHA-256 is sum.
func EvidenceKey(sum [sha256.Size]byte) string {
	return EvidencePrefix + hex.EncodeToString(sum[:])
}

// ParseEvidenceKey returns the SHA-256 an evidence key names.
func ParseEvidenceKey(key string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	h, ok := strings.CutPrefix(key, EvidencePrefix)
	if !ok || len(h) != 2*sha256.Size || strings.ToLower(h) != h {
		return sum, fmt.Errorf("objstore: %q isn't an evidence key, %s and 64 lowercase hex digits", key, EvidencePrefix)
	}
	if _, err := hex.Decode(sum[:], []byte(h)); err != nil {
		return sum, fmt.Errorf("objstore: %q isn't an evidence key, %s and 64 lowercase hex digits", key, EvidencePrefix)
	}
	return sum, nil
}

// Put stores blob under its SHA-256 and returns the sum. The put is
// write-once, so it never replaces a stored object: when the key exists,
// Put returns the sum and an error wrapping ErrExists. For a
// content-addressed blob that means the same bytes are already stored,
// so a caller that only needs the blob stored can treat it as success.
func (e *Evidence) Put(ctx context.Context, blob []byte) ([sha256.Size]byte, error) {
	sum := sha256.Sum256(blob)
	if len(blob) > MaxEvidenceSize {
		return sum, &Error{Op: "PutObject", Bucket: e.b.name, Key: EvidenceKey(sum),
			Err: fmt.Errorf("%w: a sealed blob is at most %d bytes, this one %d", ErrTooLarge, MaxEvidenceSize, len(blob))}
	}
	_, err := e.b.Put(ctx, EvidenceKey(sum), bytes.NewReader(blob), PutOptions{IfNoneMatch: true})
	return sum, err
}

// Get returns the blob whose SHA-256 is sum, after checking that its
// bytes hash to sum. A missing blob fails with ErrNotFound, a changed one
// with ErrDigestMismatch, and one over MaxEvidenceSize with ErrTooLarge.
func (e *Evidence) Get(ctx context.Context, sum [sha256.Size]byte) ([]byte, error) {
	key := EvidenceKey(sum)
	body, _, err := e.b.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	fail := func(err error) ([]byte, error) {
		return nil, &Error{Op: "GetObject", Bucket: e.b.name, Key: key, Err: err}
	}
	blob, err := io.ReadAll(io.LimitReader(body, MaxEvidenceSize+1))
	if err != nil {
		return fail(fmt.Errorf("reading the blob: %w", err))
	}
	if len(blob) > MaxEvidenceSize {
		return fail(fmt.Errorf("%w: the stored blob is over %d bytes", ErrTooLarge, MaxEvidenceSize))
	}
	if sha256.Sum256(blob) != sum {
		return fail(ErrDigestMismatch)
	}
	return blob, nil
}

// Has reports whether the blob whose SHA-256 is sum is stored.
func (e *Evidence) Has(ctx context.Context, sum [sha256.Size]byte) (bool, error) {
	_, err := e.b.Head(ctx, EvidenceKey(sum))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}
