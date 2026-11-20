// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"
	// The zones come with pairtimed, so an account's days start at the
	// same moment on every host, whatever zoneinfo it has.
	_ "time/tzdata"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// Querier is what reads need: a *sql.DB, such as store.DB.Read, or a
// writer.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// loadMinute returns the account's device rows for one minute, in
// device_id order.
func loadMinute(ctx context.Context, q Querier, account string, minuteMS int64) ([]device, error) {
	rows, err := q.QueryContext(ctx, `SELECT device_id, rev, state, slots, agent_ms, day FROM rollup_minutes
WHERE user_id = $1 AND minute_ms = $2 ORDER BY device_id`, account, minuteMS)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading a minute: %w", err)
	}
	defer rows.Close()
	var out []device
	for rows.Next() {
		var d device
		var state, slots string
		if err := rows.Scan(&d.id, &d.rev, &state, &slots, &d.agentMS, &d.day); err != nil {
			return nil, fmt.Errorf("rollup: reading a minute: %w", err)
		}
		d.pending = state == "pending"
		var s []slot
		if err := json.Unmarshal([]byte(slots), &s); err != nil || len(s) > slotsPerMinute {
			return nil, fmt.Errorf("rollup: minute %d of %s on %s has slots %q", minuteMS, account, d.id, slots)
		}
		copy(d.slots[:], s)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rollup: reading a minute: %w", err)
	}
	return out, nil
}

// saveDevice stores d as the account's latest revision of the minute on
// d's device.
func saveDevice(ctx context.Context, tx writer.Tx, account string, minuteMS int64, d device) error {
	slots, err := json.Marshal(d.slots[:])
	if err != nil {
		return err
	}
	state := "final"
	if d.pending {
		state = "pending"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rollup_minutes (user_id, minute_ms, device_id, rev, state, slots, agent_ms, day)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (user_id, minute_ms, device_id) DO UPDATE SET rev = excluded.rev, state = excluded.state,
slots = excluded.slots, agent_ms = excluded.agent_ms, day = excluded.day`,
		account, minuteMS, d.id, d.rev, state, string(slots), d.agentMS, d.day)
	if err != nil {
		return fmt.Errorf("rollup: storing a minute: %w", err)
	}
	return nil
}

// moveMinute records that the account's minute now counts toward day.
func moveMinute(ctx context.Context, tx writer.Tx, account string, minuteMS int64, day string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE rollup_minutes SET day = $1 WHERE user_id = $2 AND minute_ms = $3`, day, account, minuteMS); err != nil {
		return fmt.Errorf("rollup: moving a minute to %s: %w", day, err)
	}
	return nil
}

// addDaily adds d to the account's rollups_daily rows, one additive upsert
// per row it changes.
func addDaily(ctx context.Context, tx writer.Tx, account string, d delta) error {
	for k, c := range d {
		if c == (columns{}) {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO rollups_daily (user_id, project, day, activity_kind,
final_a_ms, final_b_ms, final_c_ms, pending_a_ms, pending_b_ms, pending_c_ms, agent_final_ms, agent_pending_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (user_id, project, day, activity_kind) DO UPDATE SET
final_a_ms = rollups_daily.final_a_ms + excluded.final_a_ms,
final_b_ms = rollups_daily.final_b_ms + excluded.final_b_ms,
final_c_ms = rollups_daily.final_c_ms + excluded.final_c_ms,
pending_a_ms = rollups_daily.pending_a_ms + excluded.pending_a_ms,
pending_b_ms = rollups_daily.pending_b_ms + excluded.pending_b_ms,
pending_c_ms = rollups_daily.pending_c_ms + excluded.pending_c_ms,
agent_final_ms = rollups_daily.agent_final_ms + excluded.agent_final_ms,
agent_pending_ms = rollups_daily.agent_pending_ms + excluded.agent_pending_ms`,
			account, k.project, k.day, k.kind, c[0], c[1], c[2], c[3], c[4], c[5], c[6], c[7])
		if err != nil {
			return fmt.Errorf("rollup: adding to %s's %s: %w", account, k.day, err)
		}
	}
	return nil
}

// setFlag raises or clears flag on the account's minute.
func setFlag(ctx context.Context, tx writer.Tx, account string, minuteMS int64, flag string, on bool) error {
	var err error
	if on {
		_, err = tx.ExecContext(ctx, `INSERT INTO rollup_flags (user_id, minute_ms, flag) VALUES ($1, $2, $3)
ON CONFLICT (user_id, minute_ms, flag) DO NOTHING`, account, minuteMS, flag)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM rollup_flags WHERE user_id = $1 AND minute_ms = $2 AND flag = $3`, account, minuteMS, flag)
	}
	if err != nil {
		return fmt.Errorf("rollup: setting %s: %w", flag, err)
	}
	return nil
}

// zones caches the zones accounts name, since ingest applies up to 2,000
// buckets a request.
var zones sync.Map // string to *time.Location

// zoneName is what an IANA zone name looks like, such as "UTC",
// "America/Argentina/Buenos_Aires" or "Etc/GMT+5".
var zoneName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$`)

// LoadZone returns the zone an IANA name names. It refuses "Local", which
// is the server's own and no account's, and any name the zone database
// doesn't have.
func LoadZone(name string) (*time.Location, error) {
	if v, ok := zones.Load(name); ok {
		return v.(*time.Location), nil
	}
	if name == "Local" || len(name) > 64 || !zoneName.MatchString(name) {
		return nil, fmt.Errorf("rollup: %q isn't a time zone name", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("rollup: %q isn't a time zone pairtime knows: %w", name, err)
	}
	zones.Store(name, loc)
	return loc, nil
}

// ErrNoAccount is what Zone returns for an account that doesn't exist.
var ErrNoAccount = errors.New("rollup: no such account")

// zoneOf returns the account's zone and its name. A name the zone database
// doesn't have, which SetZone never stores, counts as UTC, so an upload
// never fails for it.
func zoneOf(ctx context.Context, q Querier, account string) (*time.Location, string, error) {
	var name string
	err := q.QueryRowContext(ctx, `SELECT tz FROM users WHERE id = $1`, account).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("%w: %s", ErrNoAccount, account)
	}
	if err != nil {
		return nil, "", fmt.Errorf("rollup: reading %s's time zone: %w", account, err)
	}
	loc, err := LoadZone(name)
	if err != nil {
		return time.UTC, "UTC", nil
	}
	return loc, name, nil
}

// Zone returns the account's time zone and its IANA name.
func Zone(ctx context.Context, q Querier, account string) (*time.Location, string, error) {
	return zoneOf(ctx, q, account)
}

// SetZone sets the account's time zone, an IANA name, through tx. The
// minutes counted after it count toward days in the new zone.
func SetZone(ctx context.Context, tx writer.Tx, account, name string) error {
	if _, err := LoadZone(name); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET tz = $1 WHERE id = $2`, name, account)
	if err != nil {
		return fmt.Errorf("rollup: setting %s's time zone: %w", account, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%w: %s", ErrNoAccount, account)
	}
	return nil
}

// Day is one rollups_daily row: an account's time in one project, local
// day and activity kind, in milliseconds. The row whose Kind is "" holds
// the agent's running time in that project and day.
type Day struct {
	Project, Day, Kind string
	// FinalMS and PendingMS are credited time by evidence level, in
	// Levels order.
	FinalMS, PendingMS [3]int64
	// AgentFinalMS and AgentPendingMS are the agent's running time.
	AgentFinalMS, AgentPendingMS int64
}

// Read returns the account's rollups_daily rows for the days from to to,
// both YYYY-MM-DD and included.
func Read(ctx context.Context, q Querier, account, from, to string) ([]Day, error) {
	rows, err := q.QueryContext(ctx, `SELECT project, day, activity_kind,
final_a_ms, final_b_ms, final_c_ms, pending_a_ms, pending_b_ms, pending_c_ms, agent_final_ms, agent_pending_ms
FROM rollups_daily WHERE user_id = $1 AND day >= $2 AND day <= $3
ORDER BY day, project, activity_kind`, account, from, to)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading %s's days: %w", account, err)
	}
	defer rows.Close()
	var out []Day
	for rows.Next() {
		var d Day
		if err := rows.Scan(&d.Project, &d.Day, &d.Kind,
			&d.FinalMS[0], &d.FinalMS[1], &d.FinalMS[2], &d.PendingMS[0], &d.PendingMS[1], &d.PendingMS[2],
			&d.AgentFinalMS, &d.AgentPendingMS); err != nil {
			return nil, fmt.Errorf("rollup: reading %s's days: %w", account, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rollup: reading %s's days: %w", account, err)
	}
	return out, nil
}

// ConcurrentMinutes counts the account's minutes from fromMS to before
// toMS in which two of its devices credited the same slot.
func ConcurrentMinutes(ctx context.Context, q Querier, account string, fromMS, toMS int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM rollup_flags WHERE user_id = $1 AND flag = $2 AND minute_ms >= $3 AND minute_ms < $4`,
		account, FlagConcurrentDevices, fromMS, toMS).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("rollup: counting %s's flagged minutes: %w", account, err)
	}
	return n, nil
}
