// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"context"
	"crypto/ecdh"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/seal"
)

// KindEvidence is key_history's kind for the keys of evidence.p256, which
// seal tier-2 content (DESIGN 7.6; docs/spec/seal-v1.md 7).
const KindEvidence = "evidence"

// Errors the evidence keys wrap, for errors.Is.
var (
	// ErrOtherEvidenceKey is what RecordEvidence returns, wrapped, when
	// data_dir's evidence key isn't the one the history holds as
	// current. pairtimed keys rotate-evidence (WP-SB9) switches evidence
	// keys, and until it exists nothing else may.
	ErrOtherEvidenceKey = errors.New("keys: the evidence key in data_dir isn't the current one")
	// ErrUnknownEvidenceKey: a blob is sealed to an evidence key the ring
	// doesn't hold.
	ErrUnknownEvidenceKey = errors.New("keys: the blob is sealed to an evidence key this server doesn't hold")
)

// EvidenceKey is one evidence key and when it is valid, in Unix ms.
type EvidenceKey struct {
	// ID is the key's id, seal.KeyID of its public key.
	ID string
	// Public is the P-256 public key.
	Public *ecdh.PublicKey
	// ValidFrom is the first millisecond the key is valid, and ValidTo
	// the last, both included, or Current while the key is current.
	ValidFrom, ValidTo int64
}

// loadEvidence reads the evidence keys, oldest first, and checks them as
// Load says.
func loadEvidence(ctx context.Context, q Querier) ([]EvidenceKey, error) {
	rows, err := loadKind(ctx, q, KindEvidence)
	if err != nil {
		return nil, err
	}
	var out []EvidenceKey
	for _, r := range rows {
		b, err := hex.DecodeString(r.public)
		if err != nil {
			return nil, fmt.Errorf("keys: the key history's public key for evidence key %s isn't that key", r.id)
		}
		pub, err := seal.ParsePublicKey(b)
		if err != nil || seal.KeyID(pub) != r.id {
			return nil, fmt.Errorf("keys: the key history's public key for evidence key %s isn't that key", r.id)
		}
		out = append(out, EvidenceKey{ID: r.id, Public: pub, ValidFrom: r.from, ValidTo: r.to})
	}
	return out, nil
}

// RecordEvidence makes the history hold pub, the evidence key in
// data_dir, as the current evidence key, inside tx at now, Unix ms.
//
// A history without an evidence key records pub as current from now: the
// first start that publishes it, since nothing was sealed to it before
// (seal-v1 7). A key the history holds as current changes nothing. Any
// other key is refused with ErrOtherEvidenceKey, a retired one because
// pairtimed never serves with a retired evidence key again (DESIGN 10.2),
// and a new one because WP-SB9's rotation is what switches evidence keys.
// Until it exists, a different evidence.p256 means the key the stored
// blobs are sealed to has gone from data_dir.
func RecordEvidence(ctx context.Context, tx writer.Tx, pub *ecdh.PublicKey, now int64) error {
	if pub == nil || pub.Curve() != ecdh.P256() {
		return errors.New("keys: an evidence key is a P-256 key")
	}
	id := seal.KeyID(pub)
	have, err := loadEvidence(ctx, tx)
	if err != nil {
		return err
	}
	for _, k := range have {
		switch {
		case k.ID != id:
		case !k.Public.Equal(pub):
			return fmt.Errorf("keys: the key history holds another public key under evidence key %s's id", id)
		case k.ValidTo != Current:
			return fmt.Errorf("%w: evidence key %s stopped being valid at %s, and pairtimed never serves with a retired evidence key (DESIGN 10.2). Restore the current keys from their backup (deploy/runbook.md, Restoring the keys)",
				ErrOtherEvidenceKey, id, msTime(k.ValidTo))
		default:
			return nil
		}
	}
	for _, k := range have {
		if k.ValidTo == Current {
			return fmt.Errorf("%w: the database records evidence key %s as current, and data_dir holds evidence key %s. Restore evidence.p256 from the key backup (deploy/runbook.md, Restoring the keys): the stored blobs are sealed to %s, and pairtimed keys rotate-evidence (WP-SB9) is what switches evidence keys",
				ErrOtherEvidenceKey, k.ID, id, k.ID)
		}
		if k.ValidTo >= now {
			return fmt.Errorf("keys: evidence key %s would be valid from %s, and evidence key %s was valid until %s, so two keys would be valid at once. Check the server's clock", id, msTime(now), k.ID, msTime(k.ValidTo))
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO key_history (kind, key_id, public_key, valid_from_ms, valid_to_ms, recorded_at_ms) VALUES ($1, $2, $3, $4, NULL, $5)`,
		KindEvidence, id, hex.EncodeToString(pub.Bytes()), now, now); err != nil {
		return fmt.Errorf("keys: recording evidence key %s: %w", id, err)
	}
	return nil
}

// Ring is the evidence keys pairtimed holds (docs/spec/seal-v1.md 7): the
// current one, which clients seal uploads to and the keys document lists,
// and any retired one whose private half is still on disk, because the
// blobs stored before a rotation stay sealed to it until WP-SB9's pass
// re-seals them. It opens each blob with the key its key id names.
//
// A nil *Ring holds no key.
type Ring struct {
	// current is the current key's id, or "" when the ring holds none.
	current string
	keys    map[string]ringKey
}

// ringKey is one key of a Ring, with its private half.
type ringKey struct {
	EvidenceKey
	private *ecdh.PrivateKey
}

// NewRing returns the ring of privs. Each must be an evidence key h
// holds, which says whether it is current and when it is valid, and one
// h doesn't hold is refused. The ring holds no current key when none of
// privs is h's current evidence key, as on a server whose data_dir has no
// keys: the keys document then lists no evidence key, and IsCurrent is
// false for every id, so no upload is taken that the server couldn't
// open. WP-SB4 answers such an upload with a 503 of its own, since a 409
// evidence_key_retired would have no key to carry (seal-v1 7).
func NewRing(h *History, privs ...*ecdh.PrivateKey) (*Ring, error) {
	r := &Ring{keys: make(map[string]ringKey, len(privs))}
	for _, p := range privs {
		if p == nil || p.Curve() != ecdh.P256() {
			return nil, errors.New("keys: an evidence key is a P-256 key")
		}
		pub := p.PublicKey()
		id := seal.KeyID(pub)
		var (
			k  EvidenceKey
			ok bool
		)
		for _, e := range h.Evidence {
			if e.ID == id && e.Public.Equal(pub) {
				k, ok = e, true
			}
		}
		if !ok {
			return nil, fmt.Errorf("keys: the key history doesn't hold evidence key %s, so nothing says whether it is current", id)
		}
		r.keys[id] = ringKey{EvidenceKey: k, private: p}
		if k.ValidTo == Current {
			r.current = id
		}
	}
	return r, nil
}

// Current returns the current evidence key, and false when the ring holds
// none.
func (r *Ring) Current() (EvidenceKey, bool) {
	if r == nil || r.current == "" {
		return EvidenceKey{}, false
	}
	return r.keys[r.current].EvidenceKey, true
}

// IsCurrent reports whether id is the current evidence key's id. WP-SB4's
// upload check asks it of the id an upload's header names
// (seal.BlobKeyID), and answers 409 evidence_key_retired for any other
// (DESIGN 7.6). The id is only the sealer's claim, so a true answer says
// the upload names the current key, not that it is sealed to it: only
// Open under the upload's submission and part shows that, which the
// server does before it stores the upload (seal-v1 7).
func (r *Ring) IsCurrent(id string) bool {
	return r != nil && r.current != "" && id == r.current
}

// Open opens blob, sealed under c, with the key its key id names, the
// current key or a retired one. A blob sealed to a key the ring doesn't
// hold fails with ErrUnknownEvidenceKey, and the rest as seal.Open says.
// For a stored blob sealed to a key that can still come back, that is a
// server fault: a key file is missing from data_dir, and its backup
// brings it back. A blob sealed to a key the history has switched away
// from, whose private half is gone, is unavailable instead (seal-v1 7;
// DESIGN 7.5 step 8).
func (r *Ring) Open(blob []byte, c seal.Context) ([]byte, error) {
	k, err := r.keyFor(blob)
	if err != nil {
		return nil, err
	}
	return seal.Open(k.private, c, blob)
}

// Reseal opens blob as Open does and seals its plaintext to to under the
// same c, with seal.Reseal: to a viewer's ephemeral key (WP-SB7), or to
// the current evidence key after a rotation (WP-SB9).
func (r *Ring) Reseal(blob []byte, c seal.Context, to *ecdh.PublicKey) ([]byte, error) {
	k, err := r.keyFor(blob)
	if err != nil {
		return nil, err
	}
	return seal.Reseal(k.private, to, c, blob)
}

// keyFor returns the ring's key that blob names.
func (r *Ring) keyFor(blob []byte) (ringKey, error) {
	id, err := seal.BlobKeyID(blob)
	if err != nil {
		return ringKey{}, err
	}
	if r != nil {
		if k, ok := r.keys[id]; ok {
			return k, nil
		}
	}
	return ringKey{}, fmt.Errorf("%w: evidence key %s", ErrUnknownEvidenceKey, id)
}
