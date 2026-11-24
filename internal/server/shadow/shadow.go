// SPDX-License-Identifier: AGPL-3.0-or-later

// Package shadow is the server's side of the scoring's shadow week
// (D-server-shadow; DESIGN 6.7 step 2; SM 7.5): the calibration flag an
// operator turns on for each volunteer's account with pairtimed
// calibrating. While it is on, and until the server scores at 1.0.0, every
// reply that shows the owner their own credited time hides what
// D-shadow-scope hides on the volunteer's computer and says "calibrating":
// the dashboard and its API (package dashboard), the build-days API
// (package buildday) and the live stream (package stream, through the
// dashboard's View). No tier-1 field carries the client's display.shadow,
// so the operator, who recruits the volunteers, sets the flag instead.
package shadow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
)

// Querier is what the flag is read with: the read pool, or a transaction.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Flag returns when the account's calibration flag went on, and false
// when it is off (users.calibrating_since_ms). An account that isn't there
// is rollup.ErrNoAccount.
func Flag(ctx context.Context, q Querier, account string) (time.Time, bool, error) {
	var since sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT calibrating_since_ms FROM users WHERE id = $1`, account).Scan(&since)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, fmt.Errorf("%w: %s", rollup.ErrNoAccount, account)
	case err != nil:
		return time.Time{}, false, fmt.Errorf("shadow: reading %s's calibration flag: %w", account, err)
	}
	if !since.Valid {
		return time.Time{}, false, nil
	}
	return time.UnixMilli(since.Int64), true, nil
}

// Hides reports whether the account's credited time is hidden from it now:
// its flag is on and scoring, the scoring version the server runs, is
// below 1.0.0. The freeze that tags 1.0.0 ends the calibration (SM 7.5;
// DESIGN 6.7 step 4), so from then on a flag left on hides nothing, as the
// client's display.shadow reads as off then.
func Hides(ctx context.Context, q Querier, account, scoring string) (bool, error) {
	if Frozen(scoring) {
		return false, nil
	}
	_, on, err := Flag(ctx, q, account)
	return on, err
}

// Frozen reports whether the scoring version v is 1.0.0 or later, by the
// client's rule (internal/agent/config's frozen). A scoring version is
// MAJOR.MINOR.PATCH (SM 7.6), so a MAJOR that isn't 0 decides, whatever its
// length. A v that isn't one isn't frozen, so a flag keeps hiding: a freeze
// nobody tagged must not show a volunteer their time early.
func Frozen(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return false
		}
	}
	return strings.TrimLeft(parts[0], "0") != ""
}
