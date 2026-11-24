// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// Take records c's jti as used, and refuses one already recorded with a
// 409 replayed, even when its first use failed (D-sv13-jti). The record
// commits, through w, before the request acts, in a transaction of its
// own, so a dry run's jti stays when its erasure rolls back, and a
// restart inside the token's lifetime still refuses the replay. The hub
// sends every call a new token, retries included (its spec, "the
// token"), so no honest request is refused.
//
// The same transaction deletes the rows of tokens that can no longer
// verify, those whose exp passed more than Leeway before now. now must be
// the time Verify checked c at: a token that verified then has an exp no
// earlier than that, so its own row, if any, stays.
func Take(ctx context.Context, w *writer.Writer, c Claims, now time.Time) error {
	var replayed bool
	err := w.Do(ctx, 2, func(ctx context.Context, tx writer.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM privacy_token_uses WHERE exp_ms < $1`, now.Add(-Leeway).UnixMilli()); err != nil {
			return fmt.Errorf("privacy: deleting the jti of tokens past their exp: %w", err)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO privacy_token_uses (jti, exp_ms) VALUES ($1, $2) ON CONFLICT (jti) DO NOTHING`, c.JTI, c.Expires.UnixMilli())
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
