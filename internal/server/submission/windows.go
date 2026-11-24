// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/internal/server/devlog"
	"github.com/some-du6e/pairtime/internal/server/rollup"
)

// Querier reads: the read pool, or a writer transaction.
type Querier = rollup.Querier

// Key is one device's key of a project, as rollup_projects names it.
type Key struct {
	Device, ProjectKey string
}

// devlogWindows returns the devlog windows a create in devlog mode freezes
// for the account's projects, whose devices' keys are keys (DESIGN 6.2
// D13): the windows between consecutive devlogs of the projects, by when
// the server received each, none starting before the latest end of any
// window a submission froze on any of keys that wasn't withdrawn
// (frozenEnd). It is the one place that says how several projects' devlogs
// make one list.
func devlogWindows(ctx context.Context, q Querier, account string, projects []string, keys []Key) ([]devlog.Window, error) {
	var times []int64
	for _, p := range projects {
		t, err := devlog.Times(ctx, q, account, p)
		if err != nil {
			return nil, err
		}
		times = append(times, t...)
	}
	slices.Sort(times)
	notBefore, err := frozenEnd(ctx, q, account, keys)
	if err != nil {
		return nil, err
	}
	return devlog.Windows(times, notBefore), nil
}

// frozenEnd returns the latest end of any window a submission of the
// account froze on any of keys and that wasn't withdrawn, or 0 when there
// is none: where D13's next devlog list may start (devlog.Windows).
func frozenEnd(ctx context.Context, q Querier, account string, keys []Key) (int64, error) {
	var latest int64
	for _, k := range keys {
		var end sql.NullInt64
		err := q.QueryRowContext(ctx, `SELECT MAX(w.end_ms) FROM submission_windows w
JOIN submissions s ON s.id = w.submission_id
JOIN submission_keys k ON k.submission_id = s.id
WHERE s.user_id = $1 AND s.frozen_at_ms IS NOT NULL AND s.state <> $2 AND k.device_id = $3 AND k.project_key = $4`,
			account, string(Withdrawn), k.Device, k.ProjectKey).Scan(&end)
		if err != nil {
			return 0, fmt.Errorf("submission: reading %s's frozen windows: %w", account, err)
		}
		latest = max(latest, end.Int64)
	}
	return latest, nil
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

// RevealedR returns the submission's r once its metadata reveal is
// stored, and false before then or for a submission the server doesn't
// hold. It is the one read of r, so no endpoint can show r before the
// reveal (PLAN WP-SB1's checks).
func RevealedR(ctx context.Context, q Querier, id string) ([]byte, bool, error) {
	var r []byte
	err := q.QueryRowContext(ctx, `SELECT r FROM submissions WHERE id = $1 AND metadata_revealed_at_ms IS NOT NULL AND r IS NOT NULL`, id).Scan(&r)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("submission: reading %s's r: %w", id, err)
	}
	return r, true, nil
}
