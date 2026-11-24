// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/internal/server/devlog"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// devlogWindows returns the devlog windows a create in devlog mode freezes
// for the account's projects P, whose frozen pairs are keys (DESIGN 6.2
// D13; D-sb1-submissions, Q11 and Q12), and the bound they start from: the
// windows between consecutive devlogs of any of the projects, merged and
// by when the server received each (devlog.Windows), none starting before
// notBefore, the latest end of any window frozen by a submission on one
// of keys that wasn't withdrawn and went to a program that takes hours
// (frozenEnd), or 0 when there is none. The submission horizon, horizonMS,
// then cuts the list: a window that ends at or before it is left out, and
// one that starts before it starts at it. So with no frozen window before
// it, a list starts at its first devlog, never at the horizon, unless the
// window from that devlog to the next crosses it. It is the one place that
// says how several projects' devlogs make one list.
func devlogWindows(ctx context.Context, q Querier, account string, projects []string, keys []Key, horizonMS int64) ([]Window, int64, error) {
	var times []int64
	for _, p := range projects {
		t, err := devlog.Times(ctx, q, account, p)
		if err != nil {
			return nil, 0, err
		}
		times = append(times, t...)
	}
	slices.Sort(times)
	notBefore, err := frozenEnd(ctx, q, account, keys)
	if err != nil {
		return nil, 0, err
	}
	var out []Window
	for _, w := range devlog.Windows(times, notBefore) {
		if w.End <= horizonMS {
			continue
		}
		out = append(out, Window{Start: max(w.Start, horizonMS), End: w.End})
	}
	return out, notBefore, nil
}

// frozenEnd returns the latest end of any window frozen by a submission of
// the account that holds one of keys, wasn't withdrawn and went to a
// program that takes hours, or 0 when there is none: where D13's next
// devlog list may start (devlog.Windows). A decided or revoked submission
// still counts, and a withdrawn one frees its windows.
func frozenEnd(ctx context.Context, q Querier, account string, keys []Key) (int64, error) {
	var latest int64
	for _, k := range keys {
		var end sql.NullInt64
		err := q.QueryRowContext(ctx, `SELECT MAX(w.end_ms) FROM submission_windows w
JOIN submissions s ON s.id = w.submission_id
JOIN submission_keys k ON k.submission_id = s.id
WHERE k.user_id = $1 AND k.device_id = $2 AND k.project_key = $3
AND s.frozen_at_ms IS NOT NULL AND s.withdrawn_at_ms IS NULL AND s.takes_hours = 1`,
			account, k.Device, k.ProjectKey).Scan(&end)
		if err != nil {
			return 0, fmt.Errorf("submission: reading %s's frozen windows: %w", account, err)
		}
		latest = max(latest, end.Int64)
	}
	return latest, nil
}

// Ref names a submission and its program, as the overlap refusal names
// each submission it found.
type Ref struct {
	ID, Program string
}

// overlapping returns the account's other submissions that the overlap
// refusal finds for a new submission whose frozen pairs are keys and whose
// windows are ws (D-sb1-submissions, Q2): each one frozen and not
// withdrawn, in any state from frozen through decided, or revoked, that
// went to a program that takes hours, whichever program, holds one of
// keys, and has a window that overlaps one of ws. A withdrawal frees a
// submission's windows, and a decision or a revocation doesn't.
func overlapping(ctx context.Context, q Querier, account string, keys []Key, ws []Window) ([]Ref, error) {
	candidates, err := holding(ctx, q, account, keys)
	if err != nil {
		return nil, err
	}
	var out []Ref
	for _, c := range candidates {
		theirs, err := windowsOf(ctx, q, c.ID)
		if err != nil {
			return nil, err
		}
		if overlaps(ws, theirs) {
			out = append(out, c)
		}
	}
	return out, nil
}

// holding returns the account's submissions that hold one of keys, were
// frozen and not withdrawn, and went to a program that takes hours, in id
// order.
func holding(ctx context.Context, q Querier, account string, keys []Key) ([]Ref, error) {
	seen := make(map[string]string)
	for _, k := range keys {
		rows, err := q.QueryContext(ctx, `SELECT s.id, s.program FROM submissions s
JOIN submission_keys k ON k.submission_id = s.id
WHERE k.user_id = $1 AND k.device_id = $2 AND k.project_key = $3 AND s.user_id = $1
AND s.frozen_at_ms IS NOT NULL AND s.withdrawn_at_ms IS NULL AND s.takes_hours = 1`, account, k.Device, k.ProjectKey)
		if err != nil {
			return nil, fmt.Errorf("submission: reading %s's frozen keys: %w", account, err)
		}
		for rows.Next() {
			var id, program string
			if err := rows.Scan(&id, &program); err != nil {
				rows.Close()
				return nil, fmt.Errorf("submission: reading %s's frozen keys: %w", account, err)
			}
			seen[id] = program
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("submission: reading %s's frozen keys: %w", account, err)
		}
	}
	out := make([]Ref, 0, len(seen))
	for id, program := range seen {
		out = append(out, Ref{ID: id, Program: program})
	}
	slices.SortFunc(out, func(a, b Ref) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// overlaps reports whether a window of a overlaps a window of b: each
// starts before the other ends, so windows that only touch at an end
// don't. Each list is in order, and its windows overlap none of each
// other, as every submission's are.
func overlaps(a, b []Window) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i].End <= b[j].Start:
			i++
		case b[j].End <= a[i].Start:
			j++
		default:
			return true
		}
	}
	return false
}

// newR returns a fresh 32-byte r for a freeze and the lowercase hex of
// its SHA-256, the commitment the freeze answers with (DESIGN 7.5 step 2).
func newR() ([]byte, string, error) {
	r := make([]byte, 32)
	if _, err := rand.Read(r); err != nil {
		return nil, "", fmt.Errorf("submission: drawing r: %w", err)
	}
	sum := sha256.Sum256(r)
	return r, hex.EncodeToString(sum[:]), nil
}

// ErrNotRevealed is what RevealedR returns before a submission's draw is
// stored, and DrawR for a submission that isn't metadata_revealed.
var ErrNotRevealed = errors.New("submission: r isn't revealed")

// RevealedR returns the submission's r once its draw is stored, which sets
// challenged_at_ms in the transaction that moves it to challenged, and
// ErrNotRevealed before then, for a submission the server doesn't hold,
// and for one withdrawn before its draw, which never shows r
// (D-sb1-submissions, Q9). It is the read of r behind the teen view, and
// WP-SB8's bundle reads r through it too, so nothing can show r before the
// draw: a client that read r earlier could work out the draw and withdraw
// before it is recorded.
func RevealedR(ctx context.Context, q Querier, id string) ([]byte, error) {
	var r []byte
	err := q.QueryRowContext(ctx, `SELECT r FROM submissions WHERE id = $1 AND challenged_at_ms IS NOT NULL AND r IS NOT NULL`, id).Scan(&r)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotRevealed
	case err != nil:
		return nil, fmt.Errorf("submission: reading %s's r: %w", id, err)
	}
	return r, nil
}

// DrawR returns the submission's r for the seed of its draw, inside the
// writer transaction that stores the draw and moves the submission to
// challenged (WP-SB5; D-sb1-submissions, Q9). It returns r only while the
// submission is metadata_revealed, ErrNotRevealed in any other state, and
// ErrNotFound for a submission the server doesn't hold. No handler, log
// line or error calls it.
func DrawR(ctx context.Context, tx writer.Tx, id string) ([]byte, error) {
	var state string
	var r []byte
	err := tx.QueryRowContext(ctx, `SELECT state, r FROM submissions WHERE id = $1`, id).Scan(&state, &r)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("submission: reading %s for its draw: %w", id, err)
	case State(state) != MetadataRevealed || len(r) == 0:
		return nil, ErrNotRevealed
	}
	return r, nil
}
