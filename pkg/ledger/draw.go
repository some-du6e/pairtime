// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/pkg/params"
)

// The challenge draw of DESIGN 7.5 step 4, as spec 10.9 gives it to the
// byte. The server committed to r before it saw the metadata reveal, and
// the client sent the reveal before it learned r, so neither side can steer
// the draw, and an auditor recomputes it from the bundle.

// DrawSeed returns seed = SHA-256(r || SHA-256(M)), where r is the
// server's 32 random bytes from the freeze and metadata is M, the metadata
// reveal as the client sent it, before sealing (spec 10.9).
func DrawSeed(r Hash, metadata []byte) Hash {
	return DrawSeedFromDigest(r, sha256.Sum256(metadata))
}

// DrawSeedFromDigest returns seed = SHA-256(r || m), where m is SHA-256(M),
// the digest of the metadata reveal. A bundle carries r and SHA-256(M),
// not M as the client sent it (spec 10.9; DESIGN 7.8), so an auditor
// recomputes the seed from those.
func DrawSeedFromDigest(r, m Hash) Hash {
	return sum(r[:], m[:])
}

// Turn names one record in a draw's pool: a human-origin prompt or track
// call, by its device and sequence number.
type Turn struct {
	DeviceID string
	Seq      uint64
}

// DrawScore returns a turn's score, HMAC-SHA256(seed, u8(len(device_id))
// || device_id || u64(s)) (spec 10.9). It refuses a device_id that isn't
// an opaque key and s = 0.
func DrawScore(seed Hash, t Turn) (Hash, error) {
	if t.Seq == 0 {
		return Hash{}, errors.New("ledger: sequence numbers start at 1")
	}
	msg, err := appendDevice(nil, t.DeviceID)
	if err != nil {
		return Hash{}, err
	}
	return score(seed, binary.BigEndian.AppendUint64(msg, t.Seq)), nil
}

// CommitScore returns a commit's score, HMAC-SHA256(seed, 0x00 || commit
// id), where the id is the commit's raw hash: 20 bytes for SHA-1 and 32
// for SHA-256 (spec 10.9). The 0x00 can never start a turn's input, which
// starts with a length from 1 to 128.
func CommitScore(seed Hash, commit []byte) (Hash, error) {
	if len(commit) != 20 && len(commit) != 32 {
		return Hash{}, fmt.Errorf("ledger: a commit id is 20 or 32 bytes, got %d", len(commit))
	}
	return score(seed, append([]byte{0x00}, commit...)), nil
}

func score(seed Hash, msg []byte) Hash {
	m := hmac.New(sha256.New, seed[:])
	m.Write(msg)
	var h Hash
	m.Sum(h[:0])
	return h
}

// ChallengeK returns CHALLENGE_K for a pool of p turns under parameter
// table t: min(CHALLENGE_K_MAX, max(CHALLENGE_K_MIN, ceil(CHALLENGE_K_FRAC
// × p))), with the share rounded up in integer arithmetic (DESIGN 6.3).
// The caller passes the table frozen with r (DESIGN 7.5 step 4, spec 10.9).
// Under table 0.1.0, K is 10 for pools of up to 100, 11 for 101 to 110,
// and 30 from 291 on.
func ChallengeK(t *params.Table, p int) int {
	frac := t.Rat(params.CHALLENGE_K_FRAC)
	share := (frac.Num()*int64(p) + frac.Den() - 1) / frac.Den()
	return int(min(t.Int(params.CHALLENGE_K_MAX), max(t.Int(params.CHALLENGE_K_MIN), share)))
}

// Draw returns the drawn turns of pool, smallest score first: the pool
// ranked by score, comparing scores as 32-byte strings, cut to the first
// min(CHALLENGE_K, P), with CHALLENGE_K from parameter table t (spec
// 10.9). The caller passes the table frozen with r (DESIGN 7.5 step 4).
// It refuses a pool that lists a turn twice, or a turn DrawScore refuses.
// Which turns the pool holds is WP-SB5's to decide under DESIGN 7.5 step 4.
func Draw(seed Hash, pool []Turn, t *params.Table) ([]Turn, error) {
	type scored struct {
		t Turn
		s Hash
	}
	seen := make(map[Turn]bool, len(pool))
	ranked := make([]scored, len(pool))
	for i, tn := range pool {
		if seen[tn] {
			return nil, fmt.Errorf("ledger: the pool lists record %d of a device twice", tn.Seq)
		}
		seen[tn] = true
		s, err := DrawScore(seed, tn)
		if err != nil {
			return nil, err
		}
		ranked[i] = scored{tn, s}
	}
	// Two items tie only through an HMAC collision; the device id and s
	// still give one order.
	slices.SortFunc(ranked, func(a, b scored) int {
		return cmp.Or(bytes.Compare(a.s[:], b.s[:]), cmp.Compare(a.t.DeviceID, b.t.DeviceID), cmp.Compare(a.t.Seq, b.t.Seq))
	})
	out := make([]Turn, min(ChallengeK(t, len(pool)), len(pool)))
	for i := range out {
		out[i] = ranked[i].t
	}
	return out, nil
}

// InterviewCommits is the number of commits a draw puts on the interview
// list (DESIGN 7.5 step 4).
const InterviewCommits = 2

// DrawCommits returns the interview commits: the InterviewCommits commits
// with the smallest scores, smallest first, or every commit when there are
// fewer (spec 10.9). It refuses a list that holds a commit twice, or a
// commit CommitScore refuses. Which commits are eligible is WP-SB5's to
// decide.
func DrawCommits(seed Hash, commits [][]byte) ([][]byte, error) {
	type scored struct {
		c []byte
		s Hash
	}
	seen := make(map[string]bool, len(commits))
	ranked := make([]scored, len(commits))
	for i, c := range commits {
		if seen[string(c)] {
			return nil, errors.New("ledger: the commit list holds a commit twice")
		}
		seen[string(c)] = true
		s, err := CommitScore(seed, c)
		if err != nil {
			return nil, err
		}
		ranked[i] = scored{c, s}
	}
	slices.SortFunc(ranked, func(a, b scored) int {
		return cmp.Or(bytes.Compare(a.s[:], b.s[:]), bytes.Compare(a.c, b.c))
	})
	out := make([][]byte, min(InterviewCommits, len(commits)))
	for i := range out {
		out[i] = bytes.Clone(ranked[i].c)
	}
	return out, nil
}
