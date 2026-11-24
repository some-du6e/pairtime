// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sync"
	"time"
	// A copy of the zone database for a host that has none. It is only a
	// fallback: time.LoadLocation reads $ZONEINFO and the system's zoneinfo
	// first, so on pairtimed's image, which ships Debian's tzdata, the
	// image's copy decides where an account's days start.
	_ "time/tzdata"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// Querier is what reads need: a *sql.DB, such as store.DB.Read, or a
// writer.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// minuteColumns are the rollup_minutes columns scanDevice reads, in its
// order.
const minuteColumns = `minute_ms, device_id, rev, state, slots, agent_only, day`

// scanDevice reads one of the account's rollup_minutes rows, selected as
// minuteColumns, and returns it with its minute.
func scanDevice(rows *sql.Rows, account string) (device, int64, error) {
	var d device
	var minuteMS int64
	var state, slots, agentOnly string
	if err := rows.Scan(&minuteMS, &d.id, &d.rev, &state, &slots, &agentOnly, &d.day); err != nil {
		return device{}, 0, fmt.Errorf("rollup: reading a minute: %w", err)
	}
	if err := json.Unmarshal([]byte(agentOnly), &d.agentOnly); err != nil {
		return device{}, 0, fmt.Errorf("rollup: minute %d of %s on %s has agent_only %q", minuteMS, account, d.id, agentOnly)
	}
	d.pending = state == "pending"
	var s []slot
	if err := json.Unmarshal([]byte(slots), &s); err != nil || len(s) > slotsPerMinute {
		return device{}, 0, fmt.Errorf("rollup: minute %d of %s on %s has slots %q", minuteMS, account, d.id, slots)
	}
	copy(d.slots[:], s)
	return d, minuteMS, nil
}

// loadMinute returns the account's device rows for one minute, in
// device_id order.
func loadMinute(ctx context.Context, q Querier, account string, minuteMS int64) ([]device, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+minuteColumns+` FROM rollup_minutes
WHERE user_id = $1 AND minute_ms = $2 ORDER BY device_id`, account, minuteMS)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading a minute: %w", err)
	}
	defer rows.Close()
	var out []device
	for rows.Next() {
		d, _, err := scanDevice(rows, account)
		if err != nil {
			return nil, err
		}
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
	agentOnly := []byte("{}")
	if len(d.agentOnly) > 0 {
		if agentOnly, err = json.Marshal(d.agentOnly); err != nil {
			return err
		}
	}
	state := "final"
	if d.pending {
		state = "pending"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rollup_minutes (user_id, minute_ms, device_id, rev, state, slots, agent_only, day)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (user_id, minute_ms, device_id) DO UPDATE SET rev = excluded.rev, state = excluded.state,
slots = excluded.slots, agent_only = excluded.agent_only, day = excluded.day`,
		account, minuteMS, d.id, d.rev, state, string(slots), string(agentOnly), d.day)
	if err != nil {
		return fmt.Errorf("rollup: storing a minute: %w", err)
	}
	return nil
}

// addDaily adds d to the account's rollups_daily rows, one additive upsert
// per row it changes. A row's credit and its day_cap_ms move together:
// capped moves credit past the day cap from one to the other.
func addDaily(ctx context.Context, tx writer.Tx, account string, d delta) error {
	for k, c := range d {
		if c == (columns{}) {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO rollups_daily (user_id, project_id, day, activity_kind,
final_a_ms, final_b_ms, final_c_ms, pending_a_ms, pending_b_ms, pending_c_ms, agent_only_final_ms, agent_only_pending_ms, day_cap_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (user_id, project_id, day, activity_kind) DO UPDATE SET
final_a_ms = rollups_daily.final_a_ms + excluded.final_a_ms,
final_b_ms = rollups_daily.final_b_ms + excluded.final_b_ms,
final_c_ms = rollups_daily.final_c_ms + excluded.final_c_ms,
pending_a_ms = rollups_daily.pending_a_ms + excluded.pending_a_ms,
pending_b_ms = rollups_daily.pending_b_ms + excluded.pending_b_ms,
pending_c_ms = rollups_daily.pending_c_ms + excluded.pending_c_ms,
agent_only_final_ms = rollups_daily.agent_only_final_ms + excluded.agent_only_final_ms,
agent_only_pending_ms = rollups_daily.agent_only_pending_ms + excluded.agent_only_pending_ms,
day_cap_ms = rollups_daily.day_cap_ms + excluded.day_cap_ms`,
			account, k.project, k.day, k.kind, c[0], c[1], c[2], c[3], c[4], c[5], c[6], c[7], c[8])
		if err != nil {
			return fmt.Errorf("rollup: adding to %s's %s: %w", account, k.day, err)
		}
	}
	return nil
}

// toProjects turns the device's project keys in d, its slots' and its
// agent_only's, into the account-level project ids rollup_projects gives
// them (D-account-project-id). A key the device hasn't named before gets
// a new id. Two keys of the device that project registration (WP-SV10) has
// linked to one id may both name agent time; the larger counts, as it does
// across devices (merge).
func toProjects(ctx context.Context, tx writer.Tx, account string, d *device) error {
	ids := make(map[string]string)
	idOf := func(key string) (string, error) {
		if id, ok := ids[key]; ok {
			return id, nil
		}
		id, err := projectID(ctx, tx, account, d.id, key)
		if err != nil {
			return "", err
		}
		ids[key] = id
		return id, nil
	}
	for i := range d.slots {
		if d.slots[i].MS == 0 {
			continue
		}
		id, err := idOf(d.slots[i].Project)
		if err != nil {
			return err
		}
		d.slots[i].Project = id
	}
	if len(d.agentOnly) == 0 {
		return nil
	}
	agent := make(map[string]int64, len(d.agentOnly))
	for _, key := range slices.Sorted(maps.Keys(d.agentOnly)) {
		id, err := idOf(key)
		if err != nil {
			return err
		}
		agent[id] = max(agent[id], d.agentOnly[key])
	}
	d.agentOnly = agent
	return nil
}

// ProjectOf returns the account-level project id of the device's project
// key under account, inside tx, giving a key the device hasn't named before
// a new id, as Apply does (D-account-project-id). Package devlog calls it
// in the writer transaction that stores a devlog, so the devlog's key has a
// rollup_projects row before any read resolves it, and project
// registration (WP-SV10) moves it with the project's minutes.
func ProjectOf(ctx context.Context, tx writer.Tx, account, device, key string) (string, error) {
	return projectID(ctx, tx, account, device, key)
}

// projectID returns the project id of the device's project key. A key
// the device hasn't named before gets a new id.
func projectID(ctx context.Context, tx writer.Tx, account, device, key string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM rollup_projects
WHERE user_id = $1 AND device_id = $2 AND project_key = $3`, account, device, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = newProjectID()
		_, err = tx.ExecContext(ctx, `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id)
VALUES ($1, $2, $3, $4)`, account, device, key, id)
	}
	if err != nil {
		return "", fmt.Errorf("rollup: the project of %s's key on %s: %w", account, device, err)
	}
	return id, nil
}

// newProjectID returns a new project id, a random UUIDv4 (RFC 9562
// section 5.4). It carries no time, as the UUIDv7 ids of package store's
// rules do: the id lasts as long as the account, and any time in it, even
// a day, would tell whether a key first appeared early or late in the
// owner's own day.
func newProjectID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Projects returns, by project id, the project keys the account's devices
// name each of its projects by, ordered by device_id and then key in byte
// order (rollup_projects). Until project registration (WP-SV10) links two
// keys, each project has one.
func Projects(ctx context.Context, q Querier, account string) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_id, device_id, project_key FROM rollup_projects WHERE user_id = $1`, account)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading %s's projects: %w", account, err)
	}
	defer rows.Close()
	type mapping struct{ project, device, key string }
	var all []mapping
	for rows.Next() {
		var m mapping
		if err := rows.Scan(&m.project, &m.device, &m.key); err != nil {
			return nil, fmt.Errorf("rollup: reading %s's projects: %w", account, err)
		}
		all = append(all, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rollup: reading %s's projects: %w", account, err)
	}
	// Sorted here, so the order doesn't depend on the engine's collation.
	slices.SortFunc(all, func(a, b mapping) int {
		return cmp.Or(cmp.Compare(a.device, b.device), cmp.Compare(a.key, b.key))
	})
	out := make(map[string][]string)
	for _, m := range all {
		out[m.project] = append(out[m.project], m.key)
	}
	return out, nil
}

// DeviceProjects returns the project id of each project key device uploaded
// under account, by key: the ones the names endpoint gives the device the
// codes of (projectname.ProjectCodes over Projects).
func DeviceProjects(ctx context.Context, q Querier, account, device string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_key, project_id FROM rollup_projects WHERE user_id = $1 AND device_id = $2`, account, device)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading a device's projects: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var key, id string
		if err := rows.Scan(&key, &id); err != nil {
			return nil, fmt.Errorf("rollup: reading a device's projects: %w", err)
		}
		out[key] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rollup: reading a device's projects: %w", err)
	}
	return out, nil
}

// zones caches the zones accounts name, since ingest applies up to 2,000
// buckets a request.
var zones sync.Map // string to *time.Location

// zoneName is what an IANA zone name looks like, such as "UTC",
// "America/Argentina/Buenos_Aires" or "Etc/GMT+5".
var zoneName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$`)

// ErrBadZone is what LoadZone and SetZone return, wrapped, for a name that
// isn't a time zone pairtime knows.
var ErrBadZone = errors.New("not a time zone pairtime knows")

// LoadZone returns the zone an IANA name names. It refuses "Local", which
// is the server's own and no account's, and any name the zone database
// doesn't have.
func LoadZone(name string) (*time.Location, error) {
	if v, ok := zones.Load(name); ok {
		return v.(*time.Location), nil
	}
	if name == "Local" || len(name) > 64 || !zoneName.MatchString(name) {
		return nil, fmt.Errorf("rollup: %q: %w", name, ErrBadZone)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("rollup: %q: %w (%v)", name, ErrBadZone, err)
	}
	zones.Store(name, loc)
	return loc, nil
}

// ErrNoAccount is what Zone returns for an account that doesn't exist.
var ErrNoAccount = errors.New("rollup: no such account")

// History is an account's time zone and every change of it (users.tz and
// zone_changes; migration 0011).
type History struct {
	// Zone is the zone the account's days are counted in now, users.tz,
	// UTC until the owner sets one, and Name its IANA name. A name the zone
	// database doesn't have, which SetZone never stores, counts as UTC, so
	// an upload never fails for it.
	Zone *time.Location
	Name string
	// Changes are the account's zone changes, oldest first.
	Changes []Change
}

// Change is one change of an account's zone: when it was made, in Unix
// ms, and the zone it replaced.
type Change struct {
	AtMS   int64
	Before *time.Location
}

// ZoneHistory returns the account's History.
func ZoneHistory(ctx context.Context, q Querier, account string) (History, error) {
	var name string
	err := q.QueryRowContext(ctx, `SELECT tz FROM users WHERE id = $1`, account).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return History{}, fmt.Errorf("%w: %s", ErrNoAccount, account)
	}
	if err != nil {
		return History{}, fmt.Errorf("rollup: reading %s's time zone: %w", account, err)
	}
	h := History{Zone: time.UTC, Name: "UTC"}
	if loc, err := LoadZone(name); err == nil {
		h.Zone, h.Name = loc, name
	}
	rows, err := q.QueryContext(ctx, `SELECT changed_at_ms, tz_before FROM zone_changes WHERE user_id = $1`, account)
	if err != nil {
		return History{}, fmt.Errorf("rollup: reading %s's time zone changes: %w", account, err)
	}
	defer rows.Close()
	for rows.Next() {
		var c Change
		var before string
		if err := rows.Scan(&c.AtMS, &before); err != nil {
			return History{}, fmt.Errorf("rollup: reading %s's time zone changes: %w", account, err)
		}
		c.Before = time.UTC
		if loc, err := LoadZone(before); err == nil {
			c.Before = loc
		}
		h.Changes = append(h.Changes, c)
	}
	if err := rows.Err(); err != nil {
		return History{}, fmt.Errorf("rollup: reading %s's time zone changes: %w", account, err)
	}
	slices.SortFunc(h.Changes, func(a, b Change) int { return cmp.Compare(a.AtMS, b.AtMS) })
	return h, nil
}

// At returns the zone in force at the instant ms, in Unix ms: the zone the
// first change after ms replaced, and Zone when no change came after it. So
// an instant before the first recorded change takes the zone that change
// replaced.
func (h History) At(ms int64) *time.Location {
	for _, c := range h.Changes {
		if ms < c.AtMS {
			return c.Before
		}
	}
	return h.Zone
}

// after returns the zone change i put in force.
func (h History) after(i int) *time.Location {
	if i+1 < len(h.Changes) {
		return h.Changes[i+1].Before
	}
	return h.Zone
}

// zoneOf returns the zone in force for the account at the minute that
// starts at minuteMS, which Apply counts the minute's first revision in.
func zoneOf(ctx context.Context, q Querier, account string, minuteMS int64) (*time.Location, error) {
	h, err := ZoneHistory(ctx, q, account)
	if err != nil {
		return nil, err
	}
	return h.At(minuteMS), nil
}

// Zone returns the account's time zone, users.tz, and its IANA name.
func Zone(ctx context.Context, q Querier, account string) (*time.Location, string, error) {
	h, err := ZoneHistory(ctx, q, account)
	if err != nil {
		return nil, "", err
	}
	return h.Zone, h.Name, nil
}

// ZoneChangeEvery is how long after one change of an account's time zone
// the next may come (DESIGN 6.2 D10).
const ZoneChangeEvery = 30 * 24 * time.Hour

// ErrZoneTooSoon is what a *ZoneTooSoonError from SetZone wraps: a change
// less than ZoneChangeEvery after the last.
var ErrZoneTooSoon = errors.New("rollup: the time zone changed less than 30 days ago")

// ZoneTooSoonError is SetZone's refusal of a change that comes too soon. It
// says when the account's zone last changed, and when the next change may
// come, both in the account's zone.
type ZoneTooSoonError struct {
	Changed, Next time.Time
}

func (e *ZoneTooSoonError) Error() string {
	return fmt.Sprintf("%v: it changed at %s, and the next change may come from %s",
		ErrZoneTooSoon, e.Changed.Format(time.RFC3339), e.Next.Format(time.RFC3339))
}

func (e *ZoneTooSoonError) Unwrap() error { return ErrZoneTooSoon }

// SetZone sets the account's time zone, an IANA name, through tx, as of
// now. A change counts from now: a minute before it counts toward its date
// in the zone before, one from it on in the new zone, and every minute keeps
// the day it first counted toward (D-sv12-lag). It records the change's
// time and the zone it replaced in zone_changes, which keeps every change,
// so a minute takes the zone in force at its own time however many changes
// came after it (#278). A name equal to the account's zone changes nothing
// and isn't refused. A change less than ZoneChangeEvery after the last gets
// a *ZoneTooSoonError, which wraps ErrZoneTooSoon, and a name that isn't a
// time zone gets ErrBadZone, wrapped.
func SetZone(ctx context.Context, tx writer.Tx, account, name string, now time.Time) error {
	if _, err := LoadZone(name); err != nil {
		return err
	}
	var cur string
	err := tx.QueryRowContext(ctx, `SELECT tz FROM users WHERE id = $1`, account).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %s", ErrNoAccount, account)
	case err != nil:
		return fmt.Errorf("rollup: reading %s's time zone: %w", account, err)
	case name == cur:
		return nil
	}
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(changed_at_ms) FROM zone_changes WHERE user_id = $1`, account).Scan(&last); err != nil {
		return fmt.Errorf("rollup: reading %s's time zone changes: %w", account, err)
	}
	nowMS := now.UnixMilli()
	if last.Valid && nowMS-last.Int64 < ZoneChangeEvery.Milliseconds() {
		loc, err := LoadZone(cur)
		if err != nil {
			loc = time.UTC
		}
		at := time.UnixMilli(last.Int64)
		return &ZoneTooSoonError{Changed: at.In(loc), Next: at.Add(ZoneChangeEvery).In(loc)}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO zone_changes (user_id, changed_at_ms, tz_before) VALUES ($1, $2, $3)`, account, nowMS, cur); err != nil {
		return fmt.Errorf("rollup: recording %s's time zone change: %w", account, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET tz = $1 WHERE id = $2`, name, account); err != nil {
		return fmt.Errorf("rollup: setting %s's time zone: %w", account, err)
	}
	return nil
}

// Day is one rollups_daily row: an account's time in one project, local
// day and activity kind, in milliseconds. The row whose Kind is "" holds
// the agent's time on its own in that project and day.
type Day struct {
	// ProjectID is the account-level project id (D-account-project-id);
	// Projects gives the device keys behind it.
	ProjectID, Day, Kind string
	// FinalMS and PendingMS are credited time by evidence level, in
	// Levels order.
	FinalMS, PendingMS [3]int64
	// AgentOnlyFinalMS and AgentOnlyPendingMS are the agent's time on its
	// own (SM 2.6, D-bucket-agent-only).
	AgentOnlyFinalMS, AgentOnlyPendingMS int64
	// DayCapMS is the credit the day cap held back in the project and
	// kind that day, SM 5.3's X.day_cap, which FinalMS and PendingMS leave
	// out. On a day cut under DAY_CAP_S, it is the credit past the cap in
	// time order. A day the cap couldn't cut again, because some of its
	// minutes were gone, keeps its rows: once it is over the cap, or holds
	// anything back, a change holds back the credit it adds, so its
	// FinalMS and PendingMS can stay past DAY_CAP_S (see the package doc).
	DayCapMS int64
}

// Read returns the account's rollups_daily rows for the days from to to,
// both YYYY-MM-DD and included.
func Read(ctx context.Context, q Querier, account, from, to string) ([]Day, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_id, day, activity_kind,
final_a_ms, final_b_ms, final_c_ms, pending_a_ms, pending_b_ms, pending_c_ms, agent_only_final_ms, agent_only_pending_ms, day_cap_ms
FROM rollups_daily WHERE user_id = $1 AND day >= $2 AND day <= $3
ORDER BY day, project_id, activity_kind`, account, from, to)
	if err != nil {
		return nil, fmt.Errorf("rollup: reading %s's days: %w", account, err)
	}
	defer rows.Close()
	var out []Day
	for rows.Next() {
		var d Day
		if err := rows.Scan(&d.ProjectID, &d.Day, &d.Kind,
			&d.FinalMS[0], &d.FinalMS[1], &d.FinalMS[2], &d.PendingMS[0], &d.PendingMS[1], &d.PendingMS[2],
			&d.AgentOnlyFinalMS, &d.AgentOnlyPendingMS, &d.DayCapMS); err != nil {
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
// toMS in which two of its devices credited the same slot: SM 3.4's
// concurrent_devices. Ingest stores no such flag, since no fraud check
// runs on hours nobody submitted (D-no-ingest-flags; DESIGN 4.2, 8.5): the
// submission freeze (WP-SB1) calls this over the submitted window. It
// reads the per-device minutes rollup_minutes keeps for 180 days, the
// furthest back a submission reaches, and merges each minute as Apply
// does.
func ConcurrentMinutes(ctx context.Context, q Querier, account string, fromMS, toMS int64) (int, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+minuteColumns+` FROM rollup_minutes
WHERE user_id = $1 AND minute_ms >= $2 AND minute_ms < $3 ORDER BY minute_ms, device_id`, account, fromMS, toMS)
	if err != nil {
		return 0, fmt.Errorf("rollup: counting %s's concurrent minutes: %w", account, err)
	}
	defer rows.Close()
	n := 0
	var devs []device
	minute := int64(-1)
	count := func() {
		if len(devs) > 1 && merge(devs).concurrent {
			n++
		}
		devs = devs[:0]
	}
	for rows.Next() {
		d, m, err := scanDevice(rows, account)
		if err != nil {
			return 0, err
		}
		if m != minute {
			count()
			minute = m
		}
		devs = append(devs, d)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("rollup: counting %s's concurrent minutes: %w", account, err)
	}
	count()
	return n, nil
}
