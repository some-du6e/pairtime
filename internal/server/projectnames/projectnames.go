// SPDX-License-Identifier: AGPL-3.0-or-later

// Package projectnames keeps the project names owners turned on, one
// project at a time on their computer, so their own dashboard shows a name
// in place of a project's short code (D-owner-names; DESIGN 4.3, 4.4;
// docs/spec/project-names-v1.md). A name is the label the owner approved
// for one of a device's project keys, in the table project_names, and a
// hide is a name the owner hid on the dashboard, without its label, in
// project_name_hides (migration 0008).
//
// Ingest stores and deletes a device's names from its signed requests
// (Apply), and answers each with the device's names and its hides
// (OfDevice). The owner's dashboard reads the account's names (OfAccount),
// and only it: no public page, program read or export ever does. A name
// goes when the device sends a delete; when the owner presses "Hide this
// name" on the dashboard (Hide), which deletes the label at once and keeps
// a hide until the device turns the name off too, so the device can tell
// a name its owner hid from one the server no longer holds; when the
// device is removed and 90 days pass (package device marks the rows, and
// the retention job server.project_names deletes them); and when the
// account is deleted (DeleteAccount). A hide goes with the device's delete
// of its key, 30 days after the hide (the retention job
// server.project_name_hides), or with the account.
package projectnames

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// MaxPerDevice is how many names and hides together the server holds for
// one device (project-names-v1 1.4).
const MaxPerDevice = 1000

// Name is one project's name: the device's project key, and the label.
type Name struct {
	Project string `json:"project"`
	Label   string `json:"label"`
}

// Querier reads: the read pool, or a writer transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Apply deletes the names and hides of the keys in del and stores the
// names in set, for device under account, at nowMS, inside tx:
// project-names-v1 1.6. A key already named gets its new label, and its
// row stays the device's; a key hidden on the dashboard loses its hide,
// since the owner turned the name on again. It returns how many names and
// hides the device has afterwards, which the caller holds to MaxPerDevice
// before it commits. The caller checked every key and label
// (project-names-v1 1.5).
func Apply(ctx context.Context, tx writer.Tx, account, device string, set []Name, del []string, nowMS int64) (int, error) {
	for _, key := range del {
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_names WHERE device_id = $1 AND project_key = $2`, device, key); err != nil {
			return 0, fmt.Errorf("projectnames: deleting a name: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_name_hides WHERE device_id = $1 AND project_key = $2 AND user_id = $3`, device, key, account); err != nil {
			return 0, fmt.Errorf("projectnames: deleting a hide: %w", err)
		}
	}
	for _, n := range set {
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_name_hides WHERE device_id = $1 AND project_key = $2 AND user_id = $3`, device, n.Project, account); err != nil {
			return 0, fmt.Errorf("projectnames: deleting a hide: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms, device_removed_at_ms)
VALUES ($1, $2, $3, $4, $5, NULL)
ON CONFLICT (device_id, project_key) DO UPDATE SET label = excluded.label, set_at_ms = excluded.set_at_ms
WHERE project_names.user_id = excluded.user_id`,
			device, n.Project, account, n.Label, nowMS); err != nil {
			return 0, fmt.Errorf("projectnames: storing a name: %w", err)
		}
	}
	var names, hides int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_names WHERE device_id = $1`, device).Scan(&names); err != nil {
		return 0, fmt.Errorf("projectnames: counting a device's names: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_name_hides WHERE device_id = $1`, device).Scan(&hides); err != nil {
		return 0, fmt.Errorf("projectnames: counting a device's hides: %w", err)
	}
	return names + hides, nil
}

// Foreign reports whether project_names or project_name_hides holds a row
// of device under an account other than account, which ingest's other
// tables already refuse a request for (D-device-account-binding). A
// device's names and hides all share its account: Apply never changes a
// row's account, and Hide keeps the name's.
func Foreign(ctx context.Context, q Querier, device, account string) (bool, error) {
	for _, table := range []string{"project_names", "project_name_hides"} {
		var owner string
		err := q.QueryRowContext(ctx, `SELECT user_id FROM `+table+` WHERE device_id = $1 AND user_id <> $2 LIMIT 1`, device, account).Scan(&owner)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return false, fmt.Errorf("projectnames: reading a device's %s: %w", table, err)
		default:
			return true, nil
		}
	}
	return false, nil
}

// OfDevice returns device's names, and the keys of those the owner hid on
// the dashboard that the device hasn't turned off since, each in key
// order, an empty list for none.
func OfDevice(ctx context.Context, q Querier, device string) (names []Name, hidden []string, err error) {
	names = []Name{}
	if err := each(ctx, q, `SELECT project_key, label FROM project_names WHERE device_id = $1`, []any{device}, func(rows *sql.Rows) error {
		var n Name
		if err := rows.Scan(&n.Project, &n.Label); err != nil {
			return err
		}
		names = append(names, n)
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("projectnames: reading a device's names: %w", err)
	}
	hidden = []string{}
	if err := each(ctx, q, `SELECT project_key FROM project_name_hides WHERE device_id = $1`, []any{device}, func(rows *sql.Rows) error {
		var key string
		if err := rows.Scan(&key); err != nil {
			return err
		}
		hidden = append(hidden, key)
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("projectnames: reading a device's hides: %w", err)
	}
	// Key order is bytes, whatever the database's collation.
	slices.SortFunc(names, func(a, b Name) int { return strings.Compare(a.Project, b.Project) })
	slices.Sort(hidden)
	return names, hidden, nil
}

// each runs query with args and calls scan on each row.
func each(ctx context.Context, q Querier, query string, args []any, scan func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// OfAccount returns the label each of account's projects is called by on
// its own dashboard, by project id (D-account-project-id): the name of the
// first of its device keys, in device and key order, that has one. A name
// counts only for the project its own device's key maps to in
// rollup_projects, the rows Hide and the device's delete remove, so a name
// a device stored for a key it never uploaded minutes of shows nowhere.
func OfAccount(ctx context.Context, q Querier, account string) (map[string]string, error) {
	type named struct{ project, device, key, label string }
	var all []named
	if err := each(ctx, q, `SELECT rp.project_id, n.device_id, n.project_key, n.label FROM project_names n
JOIN rollup_projects rp ON rp.user_id = n.user_id AND rp.device_id = n.device_id AND rp.project_key = n.project_key
WHERE n.user_id = $1`, []any{account}, func(rows *sql.Rows) error {
		var n named
		if err := rows.Scan(&n.project, &n.device, &n.key, &n.label); err != nil {
			return err
		}
		all = append(all, n)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("projectnames: reading an account's names: %w", err)
	}
	// The order rollup.Projects gives a project's keys in, whatever the
	// database's collation.
	slices.SortFunc(all, func(a, b named) int {
		return cmp.Or(strings.Compare(a.device, b.device), strings.Compare(a.key, b.key))
	})
	labels := make(map[string]string)
	for _, n := range all {
		if _, ok := labels[n.project]; !ok {
			labels[n.project] = n.label
		}
	}
	return labels, nil
}

// ofProject is the condition that the project_names row the alias t
// names is one of project's device keys for account ($1 and $2), by
// rollup_projects.
func ofProject(t string) string {
	return `EXISTS (SELECT 1 FROM rollup_projects rp WHERE rp.user_id = $1 AND rp.project_id = $2
AND rp.device_id = ` + t + `.device_id AND rp.project_key = ` + t + `.project_key)`
}

// Hide deletes the name on every device key of project, an account-level
// project id (D-account-project-id), for account, inside tx: the
// dashboard's "Hide this name" button. The label goes at once. A linked
// device's name leaves a hide, with nowMS, which its next names answer
// lists until the device turns the name off too, 30 days at most; a
// removed device's leaves none, since it can't ask until it is linked
// again. It changes nothing of another account's. The website can hide a
// name, never set or change one.
func Hide(ctx context.Context, tx writer.Tx, account, project string, nowMS int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_name_hides (device_id, project_key, user_id, hidden_at_ms)
SELECT n.device_id, n.project_key, n.user_id, CAST($3 AS BIGINT) FROM project_names n
WHERE n.user_id = $1 AND n.device_removed_at_ms IS NULL AND `+ofProject("n")+`
ON CONFLICT (device_id, project_key) DO UPDATE SET hidden_at_ms = excluded.hidden_at_ms`, account, project, nowMS); err != nil {
		return fmt.Errorf("projectnames: keeping a hide: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_names WHERE user_id = $1 AND `+ofProject("project_names"), account, project); err != nil {
		return fmt.Errorf("projectnames: hiding a name: %w", err)
	}
	return nil
}

// DeleteAccount deletes every name and hide of account, inside tx: what
// the eraser (WP-SV13) runs when the account is deleted.
func DeleteAccount(ctx context.Context, tx writer.Tx, account string) error {
	for _, table := range []string{"project_names", "project_name_hides"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id = $1`, account); err != nil {
			return fmt.Errorf("projectnames: deleting an account's %s: %w", table, err)
		}
	}
	return nil
}
