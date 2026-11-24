// SPDX-License-Identifier: AGPL-3.0-or-later

// Package devlog keeps the devlogs and badges a teen publishes (WP-SV12;
// DESIGN 3.10, 4.3, 4.4, 6.2 D13;
// docs/errata/2026-11-23-build-days-devlogs-retention.md;
// docs/spec/teen-layer-v1.md).
//
// A devlog is Markdown exactly as the client previewed it, stored byte for
// byte, with its ai_assisted label, the random id the client made when the
// teen clicked Publish, and the time the server received it, which bounds
// D13's windows (Windows). It names its project by the publishing device's
// id and project key, as project_names does. A publish gives a key with no
// rollup_projects row yet a project id, as the rollups do, and reads
// resolve the key through rollup_projects, so project registration
// (WP-SV10) moves a devlog with the project's minutes. A badge is a code
// from Codes, with its own random id.
//
// A publish of an id the account holds is answered with what the server
// stored, so a retry stores nothing new. A delete of a devlog or a removal
// of a badge deletes its row at once, in the writer transaction that keeps
// only the id and the time in removed_items, whether or not the server held
// the item. A publish of an id in removed_items is refused with ErrRemoved
// (410) and stores nothing, so a computer that missed the removal can't
// publish the item again. The retention job server.removed_items deletes a
// removal's record 30 days after it, and the client drops a publish
// unanswered for 30 days (WP-CO4). The eraser (WP-SV13) deletes all of an
// account's rows through DeleteAccount.
//
// So that one device token can't fill the disk (#283), an account holds at
// most MaxDevlogs devlogs, with MaxMarkdownTotal bytes of Markdown between
// them, and one badge per code. A publish past a devlog cap gets ErrLimit,
// a show of a code the account shows already under another id gets
// ErrShown, and a delete or removal of an id the account doesn't hold gets
// ErrLimit while the account keeps MaxRemovals removal records. Each
// stores nothing. The delete or removal of a held item is never refused,
// and always keeps its record, past MaxRemovals too.
package devlog

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// MaxMarkdown is the most bytes of Markdown a devlog holds (orchestrator,
// 2026-11-23).
const MaxMarkdown = 64 << 10

// maxKeyLen is the longest id or project key a request may name.
const maxKeyLen = 128

// An account's caps (#283): its devlogs, the Markdown they hold between
// them in bytes, and the removal records past which it keeps none of an id
// it doesn't hold. They are the server's own limits, not SM parameters.
const (
	MaxDevlogs       = 1000
	MaxMarkdownTotal = 8 << 20
	MaxRemovals      = 1000
)

// Codes are the badges a teen can choose to show: the moment kinds of
// DESIGN 3.8, none of which counts hours (orchestrator, 2026-11-23). A
// later work package may add codes. TestCodesAreDesign38 pins this list to
// DESIGN 3.8's Moments row.
var Codes = []string{
	"first_commit", "first_green_test", "first_test_written", "rejected_agent_edit", "interrupt",
	"fixed_by_hand", "long_fight", "loop_caught", "comeback", "new_language", "new_tool",
}

// hasControl reports whether s holds a C0 control character other than
// tab, line feed and carriage return. Markdown needs none of them, and
// JSON writes each as six bytes (#293).
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return true
		}
	}
	return false
}

// isKey reports whether s is an id or a project key: 1 to 128 letters,
// digits, _ or -, the shape AGENTS.md's opaque keys have.
func isKey(s string) bool {
	if len(s) == 0 || len(s) > maxKeyLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Devlog is a devlog as the server holds it.
type Devlog struct {
	ID string `json:"id"`
	// Device is the device that published it, and ProjectKey that device's
	// key of its project.
	Device     string `json:"device"`
	ProjectKey string `json:"project_key"`
	// Project is the account-level project id the key has in
	// rollup_projects when it is read (D-account-project-id).
	Project string `json:"project"`
	// Markdown is the text, byte for byte as the client previewed it.
	Markdown   string `json:"markdown"`
	AIAssisted bool   `json:"ai_assisted"`
	// ReceivedAt is when the server received it.
	ReceivedAt time.Time `json:"received_at"`
}

// Badge is a badge the teen chose to show.
type Badge struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

// Querier reads: the read pool, or a writer transaction.
type Querier = rollup.Querier

// ErrRemoved is what Publish and Show return for an id in removed_items:
// the teen deleted or removed it in the last 30 days, and the server
// stores it nothing again.
var ErrRemoved = errors.New("devlog: the id was removed")

// ErrLimit is what Publish returns for a devlog that would take the account
// past MaxDevlogs devlogs or MaxMarkdownTotal bytes of Markdown, and what
// Delete and Remove return for an id the account doesn't hold while it
// keeps MaxRemovals removal records. Nothing is stored.
var ErrLimit = errors.New("devlog: the account's limit is reached")

// ErrShown is what Show returns for a new id of a code the account shows
// already. An account shows each code at most once, since DESIGN 4.4 keeps
// "badges the teen chose to show, as codes from a fixed list".
var ErrShown = errors.New("devlog: the account shows the badge already")

// isRemoved reports whether removed_items holds the account's id.
func isRemoved(ctx context.Context, q Querier, account, id string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM removed_items WHERE user_id = $1 AND id = $2`, account, id).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("devlog: reading a removal: %w", err)
	}
	return true, nil
}

// remove keeps the removal of the account's id at nowMS in removed_items,
// held saying whether the account held the item until this transaction
// deleted it. A second removal of the id keeps the first one's time. The
// removal of an id the account didn't hold gets ErrLimit and keeps nothing
// while the account keeps MaxRemovals records. A held item's is always
// kept.
func remove(ctx context.Context, tx writer.Tx, account, id string, nowMS int64, held bool) error {
	if !held {
		gone, err := isRemoved(ctx, tx, account, id)
		if err != nil || gone {
			return err
		}
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM removed_items WHERE user_id = $1`, account).Scan(&n); err != nil {
			return fmt.Errorf("devlog: counting removals: %w", err)
		}
		if n >= MaxRemovals {
			return ErrLimit
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO removed_items (user_id, id, removed_at_ms) VALUES ($1, $2, $3)
ON CONFLICT (user_id, id) DO NOTHING`, account, id, nowMS); err != nil {
		return fmt.Errorf("devlog: keeping a removal: %w", err)
	}
	return nil
}

// devlogColumns are the columns scanDevlog reads, in its order, from
// devlogs d joined to rollup_projects p.
const devlogColumns = `d.id, d.device_id, d.project_key, COALESCE(p.project_id, ''), d.markdown, d.ai_assisted, d.received_at_ms`

// devlogFrom is devlogs d with the rollup_projects row p its key has.
const devlogFrom = `devlogs d LEFT JOIN rollup_projects p
ON p.user_id = d.user_id AND p.device_id = d.device_id AND p.project_key = d.project_key`

type scanner interface{ Scan(dest ...any) error }

func scanDevlog(s scanner) (Devlog, error) {
	var d Devlog
	var md []byte
	var ai, at int64
	if err := s.Scan(&d.ID, &d.Device, &d.ProjectKey, &d.Project, &md, &ai, &at); err != nil {
		return Devlog{}, err
	}
	d.Markdown, d.AIAssisted, d.ReceivedAt = string(md), ai == 1, time.UnixMilli(at).UTC()
	return d, nil
}

// Publish stores the devlog d that device published for account, inside
// tx, at nowMS, the time the server received it: d's ID, ProjectKey,
// Markdown and AIAssisted, which the caller checked. It gives the key a
// project id in rollup_projects if it has none. It returns the devlog the
// server holds and whether this call stored it: for an id the account
// holds already, the devlog stored then, with nothing changed. An id in
// removed_items gets ErrRemoved, and a devlog that would take the account
// past MaxDevlogs or MaxMarkdownTotal gets ErrLimit. Both store nothing.
func Publish(ctx context.Context, tx writer.Tx, account, device string, d Devlog, nowMS int64) (Devlog, bool, error) {
	if gone, err := isRemoved(ctx, tx, account, d.ID); err != nil || gone {
		if gone {
			err = ErrRemoved
		}
		return Devlog{}, false, err
	}
	held, err := scanDevlog(tx.QueryRowContext(ctx, `SELECT `+devlogColumns+` FROM `+devlogFrom+`
WHERE d.user_id = $1 AND d.id = $2`, account, d.ID))
	switch {
	case err == nil:
		return held, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Devlog{}, false, fmt.Errorf("devlog: reading a devlog: %w", err)
	}
	// LENGTH counts a BYTEA's or a BLOB's bytes on both engines.
	var n, size int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(markdown)), 0) FROM devlogs WHERE user_id = $1`, account).Scan(&n, &size); err != nil {
		return Devlog{}, false, fmt.Errorf("devlog: counting devlogs: %w", err)
	}
	if n >= MaxDevlogs || size+int64(len(d.Markdown)) > MaxMarkdownTotal {
		return Devlog{}, false, ErrLimit
	}
	project, err := rollup.ProjectOf(ctx, tx, account, device, d.ProjectKey)
	if err != nil {
		return Devlog{}, false, err
	}
	var ai int64
	if d.AIAssisted {
		ai = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO devlogs (user_id, id, device_id, project_key, received_at_ms, markdown, ai_assisted)
VALUES ($1, $2, $3, $4, $5, $6, $7)`, account, d.ID, device, d.ProjectKey, nowMS, []byte(d.Markdown), ai); err != nil {
		return Devlog{}, false, fmt.Errorf("devlog: storing a devlog: %w", err)
	}
	d.Device, d.Project, d.ReceivedAt = device, project, time.UnixMilli(nowMS).UTC()
	return d, true, nil
}

// List returns the account's devlogs in the order the server received
// them, each with the project its key has in rollup_projects now.
func List(ctx context.Context, q Querier, account string) ([]Devlog, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+devlogColumns+` FROM `+devlogFrom+` WHERE d.user_id = $1`, account)
	if err != nil {
		return nil, fmt.Errorf("devlog: reading %s's devlogs: %w", account, err)
	}
	defer rows.Close()
	var out []Devlog
	for rows.Next() {
		d, err := scanDevlog(rows)
		if err != nil {
			return nil, fmt.Errorf("devlog: reading %s's devlogs: %w", account, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("devlog: reading %s's devlogs: %w", account, err)
	}
	// Sorted here, so ties don't depend on the engine's collation.
	slices.SortFunc(out, func(a, b Devlog) int {
		return cmp.Or(a.ReceivedAt.Compare(b.ReceivedAt), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// Delete deletes the account's devlog id at once, inside tx, at nowMS, and
// keeps only the id and the time in removed_items, whether or not the
// account held the devlog. For an id the account didn't hold, it gets
// ErrLimit and keeps nothing while the account keeps MaxRemovals records.
func Delete(ctx context.Context, tx writer.Tx, account, id string, nowMS int64) error {
	held, err := deleteRow(ctx, tx, "devlogs", account, id)
	if err != nil {
		return fmt.Errorf("devlog: deleting a devlog: %w", err)
	}
	return remove(ctx, tx, account, id, nowMS, held)
}

// deleteRow deletes the account's row id from table, and reports whether
// there was one.
func deleteRow(ctx context.Context, tx writer.Tx, table, account, id string) (bool, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id = $1 AND id = $2`, account, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Show stores the badge b the account chose to show, inside tx: b's ID
// and Code, which the caller checked against Codes. It returns the badge
// the server holds and whether this call stored it, as Publish does,
// ErrRemoved for an id in removed_items, and ErrShown for a new id of a
// code the account shows already.
func Show(ctx context.Context, tx writer.Tx, account string, b Badge) (Badge, bool, error) {
	if gone, err := isRemoved(ctx, tx, account, b.ID); err != nil || gone {
		if gone {
			err = ErrRemoved
		}
		return Badge{}, false, err
	}
	var held Badge
	err := tx.QueryRowContext(ctx, `SELECT id, code FROM badges WHERE user_id = $1 AND id = $2`, account, b.ID).Scan(&held.ID, &held.Code)
	switch {
	case err == nil:
		return held, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Badge{}, false, fmt.Errorf("devlog: reading a badge: %w", err)
	}
	var other string
	err = tx.QueryRowContext(ctx, `SELECT id FROM badges WHERE user_id = $1 AND code = $2`, account, b.Code).Scan(&other)
	switch {
	case err == nil:
		return Badge{}, false, ErrShown
	case !errors.Is(err, sql.ErrNoRows):
		return Badge{}, false, fmt.Errorf("devlog: reading a badge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO badges (user_id, id, code) VALUES ($1, $2, $3)`, account, b.ID, b.Code); err != nil {
		return Badge{}, false, fmt.Errorf("devlog: storing a badge: %w", err)
	}
	return b, true, nil
}

// Badges returns the badges the account chose to show, by code and id.
func Badges(ctx context.Context, q Querier, account string) ([]Badge, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, code FROM badges WHERE user_id = $1`, account)
	if err != nil {
		return nil, fmt.Errorf("devlog: reading %s's badges: %w", account, err)
	}
	defer rows.Close()
	var out []Badge
	for rows.Next() {
		var b Badge
		if err := rows.Scan(&b.ID, &b.Code); err != nil {
			return nil, fmt.Errorf("devlog: reading %s's badges: %w", account, err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("devlog: reading %s's badges: %w", account, err)
	}
	slices.SortFunc(out, func(a, b Badge) int { return cmp.Or(cmp.Compare(a.Code, b.Code), cmp.Compare(a.ID, b.ID)) })
	return out, nil
}

// Remove removes the account's badge id at once, inside tx, at nowMS, and
// keeps only the id and the time in removed_items, as Delete does for a
// devlog, with the same cap on the ids the account didn't hold.
func Remove(ctx context.Context, tx writer.Tx, account, id string, nowMS int64) error {
	held, err := deleteRow(ctx, tx, "badges", account, id)
	if err != nil {
		return fmt.Errorf("devlog: removing a badge: %w", err)
	}
	return remove(ctx, tx, account, id, nowMS, held)
}

// DeleteAccount deletes every devlog, badge and removal record of account,
// inside tx: what the eraser (WP-SV13) runs when the account is deleted.
func DeleteAccount(ctx context.Context, tx writer.Tx, account string) error {
	for _, table := range []string{"devlogs", "badges", "removed_items"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id = $1`, account); err != nil {
			return fmt.Errorf("devlog: deleting an account's %s: %w", table, err)
		}
	}
	return nil
}

// Times returns the times the server received the account's devlogs on
// project, an account-level project id, in order: the times D13's windows
// run between (Windows). A devlog is on the project its key's
// rollup_projects row names when it is read.
func Times(ctx context.Context, q Querier, account, project string) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.received_at_ms FROM devlogs d
JOIN rollup_projects p ON p.user_id = d.user_id AND p.device_id = d.device_id AND p.project_key = d.project_key
WHERE d.user_id = $1 AND p.project_id = $2`, account, project)
	if err != nil {
		return nil, fmt.Errorf("devlog: reading %s's devlog times: %w", account, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return nil, fmt.Errorf("devlog: reading %s's devlog times: %w", account, err)
		}
		out = append(out, at)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("devlog: reading %s's devlog times: %w", account, err)
	}
	slices.Sort(out)
	return out, nil
}

// Window is a devlog window, from Start to End in Unix ms (DESIGN 6.2 D13).
type Window struct {
	Start, End int64
}

// Windows returns D13's devlog windows over times, the receipt times of a
// project's devlogs in order (Times): the time between each two
// consecutive devlogs, none starting before notBefore, the latest end of
// any window frozen by a submission on the project that wasn't withdrawn,
// or 0 when there is none (#278). The first window after notBefore
// starts at it. So deleting or republishing a devlog moves no frozen window
// and puts no submitted time in a new one (DESIGN 7.5 step 2, 4.4). WP-SB1
// builds a submission's list of devlog windows with it. A project with no
// devlogs after notBefore has no window here: D13 gives it one window,
// whose bounds are the submission's, so the caller adds it.
func Windows(times []int64, notBefore int64) []Window {
	var points []int64
	if notBefore > 0 {
		points = append(points, notBefore)
	}
	for _, t := range times {
		if t > notBefore && (len(points) == 0 || t > points[len(points)-1]) {
			points = append(points, t)
		}
	}
	var out []Window
	for i := 1; i < len(points); i++ {
		out = append(out, Window{Start: points[i-1], End: points[i]})
	}
	return out
}

// Exported is every row this package keeps for an account, which the
// privacy hub's export holds (DESIGN 8.9; WP-SV13): its devlogs, its badges
// and the ids of what it deleted or removed in the last 30 days.
type Exported struct {
	Devlogs []Devlog  `json:"devlogs"`
	Badges  []Badge   `json:"badges"`
	Removed []Removal `json:"removed"`
}

// Removal is a removed_items row: an id the teen deleted or removed, and
// when.
type Removal struct {
	ID        string    `json:"id"`
	RemovedAt time.Time `json:"removed_at"`
}

// Export reads every row this package keeps for account, for the privacy
// hub's export, and writes nothing. DeleteAccount deletes the same rows.
func Export(ctx context.Context, q Querier, account string) (Exported, error) {
	devlogs, err := List(ctx, q, account)
	if err != nil {
		return Exported{}, err
	}
	badges, err := Badges(ctx, q, account)
	if err != nil {
		return Exported{}, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id, removed_at_ms FROM removed_items WHERE user_id = $1`, account)
	if err != nil {
		return Exported{}, fmt.Errorf("devlog: exporting %s's removals: %w", account, err)
	}
	defer rows.Close()
	var removed []Removal
	for rows.Next() {
		var r Removal
		var at int64
		if err := rows.Scan(&r.ID, &at); err != nil {
			return Exported{}, fmt.Errorf("devlog: exporting %s's removals: %w", account, err)
		}
		r.RemovedAt = time.UnixMilli(at).UTC()
		removed = append(removed, r)
	}
	if err := rows.Err(); err != nil {
		return Exported{}, fmt.Errorf("devlog: exporting %s's removals: %w", account, err)
	}
	slices.SortFunc(removed, func(a, b Removal) int { return cmp.Or(a.RemovedAt.Compare(b.RemovedAt), cmp.Compare(a.ID, b.ID)) })
	return Exported{Devlogs: devlogs, Badges: badges, Removed: removed}, nil
}
