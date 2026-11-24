// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
)

// Querier reads: the read pool, or a writer transaction.
type Querier = rollup.Querier

// Key is one device's key of a project, as rollup_projects names it.
type Key struct {
	Device, ProjectKey string
}

// Project is one of a submission's projects P, by its account-level
// project id, with each (device id, project key) pair it mapped to at the
// freeze (D-sb1-submissions, Q17).
type Project struct {
	ID   string
	Keys []Key
}

// Window is a frozen window, from Start to End in Unix ms.
type Window struct {
	Start, End int64
}

// The modes of a submission: one window W, or a list of devlog windows
// (DESIGN 6.2 D13).
const (
	ModeWindow        = "window"
	ModeDevlogWindows = "devlog_windows"
)

// The credentials a submission can be made with: a web session or one of
// the account's computers. WP-PG6 adds a program's.
const (
	BySession = "session"
	ByDevice  = "device"
)

// Program is one of the programs this instance takes submissions for, as
// pairtime.toml's [[programs]] lists it (DESIGN 10.2).
type Program struct {
	// Name is how program_reviewer:<program> and its submissions name it.
	Name string
	// DisplayName is the name teens see.
	DisplayName string
	// TakesHours says whether its submissions claim hours. Only a program
	// whose submissions claim none, which today means calibration's
	// (DESIGN 6.7), has it false.
	TakesHours bool
}

// Submission is a submission as the server holds it, without r.
type Submission struct {
	ID string
	// Account is the users id of the account it belongs to.
	Account string
	// Program is the program's name, and TakesHours what [[programs]] said
	// of it at the create.
	Program    string
	TakesHours bool
	// ExternalID is the program's id for a program-made submission
	// (WP-PG6), and RequestID the random id a teen's client made, with
	// RequestSHA256 the hash of the request it made it for. One of the two
	// ids is set.
	ExternalID, RequestID, RequestSHA256 string
	// CreatedBy is BySession or ByDevice, and Device the creating device's
	// id, "" for a session.
	CreatedBy, Device string
	// Mode is ModeWindow or ModeDevlogWindows.
	Mode  string
	State State
	// Projects are P, by project id, each with its frozen keys.
	Projects []Project
	// Windows are the frozen windows, in order.
	Windows []Window
	// ScoringVersion and Params are what the freeze froze: the scoring
	// version and its parameter table's hash. RSHA256 is the lowercase hex
	// of SHA-256(r). All three are "" before the freeze.
	ScoringVersion, Params, RSHA256 string
	// EligibilityToConfirm is the eligibility note (DESIGN 8.2), as the
	// freeze found the account.
	EligibilityToConfirm bool
	// The times: when it was made and last changed, and when it last
	// reached each state a later step reads, zero while it hasn't.
	CreatedAt, UpdatedAt                                               time.Time
	FrozenAt, MetadataRevealedAt, ChallengedAt, DecidedAt, WithdrawnAt time.Time
	RevokedAt                                                          time.Time
}

// Keys returns every frozen (device id, project key) pair of the
// submission, in order.
func (s Submission) Keys() []Key {
	var out []Key
	for _, p := range s.Projects {
		out = append(out, p.Keys...)
	}
	slices.SortFunc(out, compareKeys)
	return out
}

// ProjectIDs returns P's project ids, in order.
func (s Submission) ProjectIDs() []string {
	out := make([]string, len(s.Projects))
	for i, p := range s.Projects {
		out[i] = p.ID
	}
	return out
}

func compareKeys(a, b Key) int {
	return cmp.Or(cmp.Compare(a.Device, b.Device), cmp.Compare(a.ProjectKey, b.ProjectKey))
}

// submissionColumns are the submissions columns scanSubmission reads, in
// its order. r is never one of them: only RevealedR and DrawR read it.
const submissionColumns = `id, user_id, program, takes_hours, COALESCE(external_id, ''), COALESCE(request_id, ''), request_sha256,
created_by, COALESCE(device_id, ''), mode, state, COALESCE(scoring_version, ''), COALESCE(params, ''), COALESCE(r_sha256, ''),
COALESCE(eligibility_to_confirm, 0), created_at_ms, updated_at_ms,
frozen_at_ms, metadata_revealed_at_ms, challenged_at_ms, decided_at_ms, withdrawn_at_ms, revoked_at_ms`

type scanner interface{ Scan(dest ...any) error }

// timeOf turns a nullable time column into a time, zero when it is NULL.
func timeOf(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64).UTC()
}

func scanSubmission(s scanner) (Submission, error) {
	var sub Submission
	var state string
	var takesHours, eligibility, created, updated int64
	var frozen, revealed, challenged, decided, withdrawn, revoked sql.NullInt64
	if err := s.Scan(&sub.ID, &sub.Account, &sub.Program, &takesHours, &sub.ExternalID, &sub.RequestID, &sub.RequestSHA256,
		&sub.CreatedBy, &sub.Device, &sub.Mode, &state, &sub.ScoringVersion, &sub.Params, &sub.RSHA256,
		&eligibility, &created, &updated,
		&frozen, &revealed, &challenged, &decided, &withdrawn, &revoked); err != nil {
		return Submission{}, err
	}
	sub.State = State(state)
	sub.TakesHours, sub.EligibilityToConfirm = takesHours == 1, eligibility == 1
	sub.CreatedAt, sub.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	sub.FrozenAt, sub.MetadataRevealedAt, sub.ChallengedAt = timeOf(frozen), timeOf(revealed), timeOf(challenged)
	sub.DecidedAt, sub.WithdrawnAt, sub.RevokedAt = timeOf(decided), timeOf(withdrawn), timeOf(revoked)
	return sub, nil
}

// Get returns the submission id with its projects, keys and windows, or
// ErrNotFound. It never reads r.
func Get(ctx context.Context, q Querier, id string) (Submission, error) {
	sub, err := scanSubmission(q.QueryRowContext(ctx, `SELECT `+submissionColumns+` FROM submissions WHERE id = $1`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Submission{}, ErrNotFound
	case err != nil:
		return Submission{}, fmt.Errorf("submission: reading %s: %w", id, err)
	}
	if err := loadParts(ctx, q, &sub); err != nil {
		return Submission{}, err
	}
	return sub, nil
}

// loadParts reads sub's projects, frozen keys and windows.
func loadParts(ctx context.Context, q Querier, sub *Submission) error {
	ids, err := textColumn(ctx, q, `SELECT project_id FROM submission_projects WHERE submission_id = $1`, sub.ID)
	if err != nil {
		return fmt.Errorf("submission: reading %s's projects: %w", sub.ID, err)
	}
	slices.Sort(ids)
	keys := make(map[string][]Key)
	rows, err := q.QueryContext(ctx, `SELECT device_id, project_key, project_id FROM submission_keys WHERE submission_id = $1`, sub.ID)
	if err != nil {
		return fmt.Errorf("submission: reading %s's keys: %w", sub.ID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var k Key
		var project string
		if err := rows.Scan(&k.Device, &k.ProjectKey, &project); err != nil {
			return fmt.Errorf("submission: reading %s's keys: %w", sub.ID, err)
		}
		keys[project] = append(keys[project], k)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("submission: reading %s's keys: %w", sub.ID, err)
	}
	sub.Projects = make([]Project, len(ids))
	for i, id := range ids {
		ks := keys[id]
		slices.SortFunc(ks, compareKeys)
		sub.Projects[i] = Project{ID: id, Keys: ks}
	}
	sub.Windows, err = windowsOf(ctx, q, sub.ID)
	return err
}

// windowsOf returns the frozen windows of submission id, in order.
func windowsOf(ctx context.Context, q Querier, id string) ([]Window, error) {
	rows, err := q.QueryContext(ctx, `SELECT start_ms, end_ms FROM submission_windows WHERE submission_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("submission: reading %s's windows: %w", id, err)
	}
	defer rows.Close()
	var out []Window
	for rows.Next() {
		var w Window
		if err := rows.Scan(&w.Start, &w.End); err != nil {
			return nil, fmt.Errorf("submission: reading %s's windows: %w", id, err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("submission: reading %s's windows: %w", id, err)
	}
	slices.SortFunc(out, func(a, b Window) int { return cmp.Compare(a.Start, b.Start) })
	return out, nil
}

// textColumn runs query, which selects one TEXT column, and returns its
// values.
func textColumn(ctx context.Context, q Querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Scope says whose submissions List lists: one account's, or one
// program's, which never includes a submission to a program that takes no
// hours.
type Scope struct {
	Account, Program string
}

// Cursor is where a page of List ends: the last submission's update time
// and id.
type Cursor struct {
	UpdatedAtMS int64
	ID          string
}

// List returns at most limit of scope's submissions that changed at or
// after sinceMS, ordered by update time and then id, after the cursor
// when after is set, with whether there are more.
func List(ctx context.Context, q Querier, scope Scope, sinceMS int64, after *Cursor, limit int) ([]Submission, bool, error) {
	where, arg := "user_id = $1", scope.Account
	if scope.Program != "" {
		where, arg = "program = $1 AND takes_hours = 1", scope.Program
	}
	c := Cursor{UpdatedAtMS: -1}
	if after != nil {
		c = *after
	}
	rows, err := q.QueryContext(ctx, `SELECT `+submissionColumns+` FROM submissions
WHERE `+where+` AND updated_at_ms >= $2 AND (updated_at_ms > $3 OR (updated_at_ms = $3 AND id > $4))
ORDER BY updated_at_ms, id LIMIT $5`, arg, sinceMS, c.UpdatedAtMS, c.ID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("submission: listing submissions: %w", err)
	}
	var out []Submission
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			rows.Close()
			return nil, false, fmt.Errorf("submission: listing submissions: %w", err)
		}
		out = append(out, sub)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, fmt.Errorf("submission: listing submissions: %w", err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	for i := range out {
		if err := loadParts(ctx, q, &out[i]); err != nil {
			return nil, false, err
		}
	}
	return out, more, nil
}

// Event is one change of a submission's state.
type Event struct {
	From State     `json:"from"`
	To   State     `json:"to"`
	At   time.Time `json:"at"`
}

// Events returns every change of submission id's state, in order.
func Events(ctx context.Context, q Querier, id string) ([]Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT seq, from_state, to_state, at_ms FROM submission_events WHERE submission_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("submission: reading %s's moves: %w", id, err)
	}
	defer rows.Close()
	type event struct {
		seq int64
		Event
	}
	var all []event
	for rows.Next() {
		var e event
		var from, to string
		var atMS int64
		if err := rows.Scan(&e.seq, &from, &to, &atMS); err != nil {
			return nil, fmt.Errorf("submission: reading %s's moves: %w", id, err)
		}
		e.From, e.To, e.At = State(from), State(to), time.UnixMilli(atMS).UTC()
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("submission: reading %s's moves: %w", id, err)
	}
	slices.SortFunc(all, func(a, b event) int { return cmp.Compare(a.seq, b.seq) })
	out := make([]Event, len(all))
	for i, e := range all {
		out[i] = e.Event
	}
	return out, nil
}

// newID returns a UUIDv7 for t (RFC 9562 section 5.7), the id form package
// store's portable SQL rules ask for.
func newID(t time.Time) string {
	var b [16]byte
	rand.Read(b[:])
	ms := uint64(t.UnixMilli())
	for i := range 6 {
		b[i] = byte(ms >> (40 - 8*i))
	}
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// nullString is s as a column value, NULL when it is "".
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// bit is b as a 1 or 0 column value.
func bit(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
