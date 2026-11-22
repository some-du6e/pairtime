// SPDX-License-Identifier: AGPL-3.0-or-later

// Package projectnames keeps the project names owners turned on, one
// project at a time on their computer, so their own dashboard shows a name
// in place of a project's short code (D-owner-names; DESIGN 4.3, 4.4;
// docs/spec/project-names-v1.md). A name is the label the owner approved
// for one of a device's project keys, in the table project_names
// (migration 0008).
//
// Ingest stores and deletes a device's names from its signed requests
// (Apply), and answers each with the device's names (OfDevice). The
// owner's dashboard reads the account's (OfAccount), and only it: no
// public page, program read or export ever does. A name goes when the
// device sends a delete, when the owner presses "Hide this name" on the
// dashboard (Hide), when the device is removed and 90 days pass (package
// device marks the rows, and the retention job server.project_names
// deletes them), and when the account is deleted (DeleteAccount).
package projectnames

import (
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

// Apply deletes the names of the keys in del and stores the names in set,
// for device under account, at nowMS, inside tx: project-names-v1 1.6. A
// key already named gets its new label, and its row stays the device's. It
// returns how many names the device has afterwards, which the caller holds
// to MaxPerDevice before it commits. The caller checked every key and
// label (project-names-v1 1.5).
func Apply(ctx context.Context, tx writer.Tx, account, device string, set []Name, del []string, nowMS int64) (int, error) {
	for _, key := range del {
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_names WHERE device_id = $1 AND project_key = $2`, device, key); err != nil {
			return 0, fmt.Errorf("projectnames: deleting a name: %w", err)
		}
	}
	for _, n := range set {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms, device_removed_at_ms)
VALUES ($1, $2, $3, $4, $5, NULL)
ON CONFLICT (device_id, project_key) DO UPDATE SET label = excluded.label, set_at_ms = excluded.set_at_ms
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

// OfDevice returns device's names in key order, an empty list for none.
func OfDevice(ctx context.Context, q Querier, device string) ([]Name, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_key, label FROM project_names WHERE device_id = $1 ORDER BY project_key`, device)
	if err != nil {
		return nil, fmt.Errorf("projectnames: reading a device's names: %w", err)
	}
	defer rows.Close()
	names := []Name{}
	for rows.Next() {
		var n Name
		if err := rows.Scan(&n.Project, &n.Label); err != nil {
			return nil, fmt.Errorf("projectnames: reading a device's names: %w", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projectnames: reading a device's names: %w", err)
	}
	// SQL's order is the database's collation; key order is bytes.
	slices.SortFunc(names, func(a, b Name) int { return strings.Compare(a.Project, b.Project) })
	return names, nil
}

// OfAccount returns the labels of account's names by project key, for its
// own dashboard. A project key is an HMAC under its device's secret, so two
// devices never share one.
func OfAccount(ctx context.Context, q Querier, account string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_key, label FROM project_names WHERE user_id = $1`, account)
	if err != nil {
		return nil, fmt.Errorf("projectnames: reading an account's names: %w", err)
	}
	defer rows.Close()
	labels := make(map[string]string)
	for rows.Next() {
		var key, label string
		if err := rows.Scan(&key, &label); err != nil {
			return nil, fmt.Errorf("projectnames: reading an account's names: %w", err)
		}
		labels[key] = label
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projectnames: reading an account's names: %w", err)
	}
	return labels, nil
}

// Hide deletes the name of every device key of project, an account-level
// project id (D-account-project-id), for account, inside tx: the
// dashboard's "Hide this name" button. It deletes nothing of another
// account's. The website can hide a name, never set or change one.
func Hide(ctx context.Context, tx writer.Tx, account, project string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_names WHERE user_id = $1 AND EXISTS (
SELECT 1 FROM rollup_projects rp WHERE rp.user_id = $1 AND rp.project_id = $2
AND rp.device_id = project_names.device_id AND rp.project_key = project_names.project_key)`, account, project); err != nil {
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
