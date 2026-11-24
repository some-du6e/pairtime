// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// PruneAfter is how long past its token's last valid moment, exp plus
// Leeway, a jti's row stays at least. Requests check their tokens against
// clocks read at different moments, and a row pruned by one request's
// clock mustn't reopen a token another request has just checked, so the
// rows outlive the tokens by far more than a request takes. A row goes
// with the next request's Take after that, which may be much later: the
// hub calls only when someone asks Hack Club for a deletion.
const PruneAfter = time.Hour

// JTIKey is the key privacy_token_uses records a jti under: its hex
// SHA-256. The hub's jti starts with the request_id and names the action,
// and the hash keeps neither (0015). Verify has held the jti to 1 to
// MaxIDSize bytes of printable ASCII before anything hashes or stores it.
func JTIKey(jti string) string {
	sum := sha256.Sum256([]byte(jti))
	return hex.EncodeToString(sum[:])
}

// Take records c's jti as used, and refuses one already recorded with a
// 409 replayed, even when its first use failed (D-sv13-jti). The record
// commits, through w, before the request acts, in a transaction of its
// own, so a dry run's jti stays when its erasure rolls back, and a
// restart inside the token's lifetime still refuses the replay. The hub
// sends every call a new token, retries included (its spec, "the
// token"), so no honest request is refused.
//
// The same transaction deletes the rows of tokens whose exp passed more
// than Leeway and PruneAfter before now, the time Verify checked c at.
func Take(ctx context.Context, w *writer.Writer, c Claims, now time.Time) error {
	var replayed bool
	err := w.Do(ctx, 2, func(ctx context.Context, tx writer.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM privacy_token_uses WHERE exp_ms < $1`, now.Add(-Leeway-PruneAfter).UnixMilli()); err != nil {
			return fmt.Errorf("privacy: deleting the jti of tokens past their exp: %w", err)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO privacy_token_uses (jti_sha256, exp_ms) VALUES ($1, $2) ON CONFLICT (jti_sha256) DO NOTHING`, JTIKey(c.JTI), c.Expires.UnixMilli())
		if err != nil {
			return fmt.Errorf("privacy: recording a token's jti: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("privacy: recording a token's jti: %w", err)
		}
		replayed = n == 0
		return nil
	})
	if err != nil {
		return err
	}
	if replayed {
		return &Error{Status: http.StatusConflict, Code: "replayed"}
	}
	return nil
}
