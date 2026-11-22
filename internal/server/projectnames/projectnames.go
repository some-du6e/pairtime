// SPDX-License-Identifier: AGPL-3.0-or-later

// Package projectnames keeps the project names owners turned on, one
// project at a time on their computer, so their own dashboard shows a name
// in place of a project's short code (D-owner-names; DESIGN 4.3, 4.4;
// docs/spec/project-names-v1.md). A name is the label the owner approved
// for one of a device's project keys, in the table project_names
// (migration 0008).
//
// Ingest stores and deletes a device's names from its signed requests
// (Apply), and answers each with the device's names and the ones the owner
// hid on the dashboard (OfDevice). The owner's dashboard reads the
// account's (OfAccount), and only it: no public page, program read or
// export ever does. A name goes when the device sends a delete, when the
// owner presses "Hide this name" on the dashboard (Hide), when the device
// is removed and 90 days pass (package device marks the rows, and the
// retention job server.project_names deletes them), and when the account
// is deleted (DeleteAccount). A hidden name's label goes at once, and its
// row stays without it until the device's delete says the device turned
// the name off too, so that the device can tell a name its owner hid from
// one the server no longer holds.
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

// MaxPerDevice is how many names the server holds for one device
// (project-names-v1 1.4).
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

// Apply deletes the names of the keys in del, hidden ones too, and stores
// the names in set, for device under account, at nowMS, inside tx:
// project-names-v1 1.6. A key already named, or hidden on the dashboard,
// gets its new label, and its row stays the device's. It returns how many
// rows the device has afterwards, hidden names' included, which the caller
// holds to MaxPerDevice before it commits. The caller checked every key
// and label (project-names-v1 1.5).
func Apply(ctx context.Context, tx writer.Tx, account, device string, set []Name, del []string, nowMS int64) (int, error) {
	for _, key := range del {
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_names WHERE device_id = $1 AND project_key = $2`, device, key); err != nil {
			return 0, fmt.Errorf("projectnames: deleting a name: %w", err)
		}
	}
	for _, n := range set {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms, hidden_at_ms, device_removed_at_ms)
VALUES ($1, $2, $3, $4, $5, NULL, NULL)
ON CONFLICT (device_id, project_key) DO UPDATE SET label = excluded.label, set_at_ms = excluded.set_at_ms, hidden_at_ms = NULL
WHERE project_names.user_id = excluded.user_id`,
			device, n.Project, account, n.Label, nowMS); err != nil {
			return 0, fmt.Errorf("projectnames: storing a name: %w", err)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_names WHERE device_id = $1`, device).Scan(&count); err != nil {
		return 0, fmt.Errorf("projectnames: counting a device's names: %w", err)
	}
	return count, nil
}

// Foreign reports whether project_names holds a row of device under an
// account other than account, which ingest's other tables already refuse
// a request for (D-device-account-binding). A device's names all share
// its account: Apply never changes a row's account.
func Foreign(ctx context.Context, q Querier, device, account string) (bool, error) {
	var owner string
	err := q.QueryRowContext(ctx, `SELECT user_id FROM project_names WHERE device_id = $1 AND user_id <> $2 LIMIT 1`, device, account).Scan(&owner)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("projectnames: reading a device's names: %w", err)
	}
	return true, nil
}

// OfDevice returns device's names, and the keys of those the owner hid on
// the dashboard that the device hasn't turned off since, each in key
// order, an empty list for none.
func OfDevice(ctx context.Context, q Querier, device string) (names []Name, hidden []string, err error) {
	rows, err := q.QueryContext(ctx, `SELECT project_key, label FROM project_names WHERE device_id = $1`, device)
	if err != nil {
		return nil, nil, fmt.Errorf("projectnames: reading a device's names: %w", err)
	}
	defer rows.Close()
	names, hidden = []Name{}, []string{}
	for rows.Next() {
		var key string
		var label sql.NullString
		if err := rows.Scan(&key, &label); err != nil {
			return nil, nil, fmt.Errorf("projectnames: reading a device's names: %w", err)
		}
		if label.Valid {
			names = append(names, Name{Project: key, Label: label.String})
		} else {
			hidden = append(hidden, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("projectnames: reading a device's names: %w", err)
	}
	// Key order is bytes, whatever the database's collation.
	slices.SortFunc(names, func(a, b Name) int { return strings.Compare(a.Project, b.Project) })
	slices.Sort(hidden)
	return names, hidden, nil
}

// OfAccount returns the label each of account's projects is called by on
// its own dashboard, by project id (D-account-project-id): the name of the
// first of its device keys, in device and key order, that has one. A name
// counts only for the project its own device's key maps to in
// rollup_projects, the rows Hide and the device's delete remove, so a name
// a device stored for a key it never uploaded minutes of shows nowhere.
func OfAccount(ctx context.Context, q Querier, account string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT rp.project_id, n.device_id, n.project_key, n.label FROM project_names n
JOIN rollup_projects rp ON rp.user_id = n.user_id AND rp.device_id = n.device_id AND rp.project_key = n.project_key
WHERE n.user_id = $1 AND n.label IS NOT NULL`, account)
	if err != nil {
		return nil, fmt.Errorf("projectnames: reading an account's names: %w", err)
	}
	defer rows.Close()
	type named struct{ project, device, key, label string }
	var all []named
	for rows.Next() {
		var n named
		if err := rows.Scan(&n.project, &n.device, &n.key, &n.label); err != nil {
			return nil, fmt.Errorf("projectnames: reading an account's names: %w", err)
		}
		all = append(all, n)
	}
	if err := rows.Err(); err != nil {
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

// Hide deletes the label of every name on a device key of project, an
// account-level project id (D-account-project-id), for account, at nowMS,
// inside tx: the dashboard's "Hide this name" button. Each row stays
// without its label, as hidden, until its device's delete (OfDevice). It
// changes nothing of another account's. The website can hide a name, never
// set or change one.
func Hide(ctx context.Context, tx writer.Tx, account, project string, nowMS int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE project_names SET label = NULL, hidden_at_ms = $3 WHERE user_id = $1 AND label IS NOT NULL AND EXISTS (
SELECT 1 FROM rollup_projects rp WHERE rp.user_id = $1 AND rp.project_id = $2
AND rp.device_id = project_names.device_id AND rp.project_key = project_names.project_key)`, account, project, nowMS); err != nil {
		return fmt.Errorf("projectnames: hiding a name: %w", err)
	}
	return nil
}

// DeleteAccount deletes every name of account, inside tx: what the eraser
// (WP-SV13) runs when the account is deleted.
func DeleteAccount(ctx context.Context, tx writer.Tx, account string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_names WHERE user_id = $1`, account); err != nil {
		return fmt.Errorf("projectnames: deleting an account's names: %w", err)
	}
	return nil
}
