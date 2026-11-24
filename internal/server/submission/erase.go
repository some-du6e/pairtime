// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// partTables are the tables that hold an account's submissions' parts by
// submission_id. Their foreign keys name submissions, so DeleteAccount
// deletes from them before submissions itself.
var partTables = []string{"submission_events", "submission_windows", "submission_keys", "submission_projects"}

// DeleteAccount deletes every submission of account, with its projects,
// keys, windows, moves and flags, inside tx: what the eraser (WP-SV13)
// runs when the account is deleted (DESIGN 8.9), and the privacy hub's
// export counts these tables. It first takes the account's submission ids
// out of the other accounts' repo_overlap flags, and deletes a flag that
// then names none, since an id that points at one of the account's
// submissions is data about the account (D-sb1-flags-erasure).
func DeleteAccount(ctx context.Context, tx writer.Tx, account string) error {
	ids, err := IDs(ctx, tx, account)
	if err != nil {
		return err
	}
	if err := unname(ctx, tx, account, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM submission_flags WHERE user_id = $1`, account); err != nil {
		return fmt.Errorf("submission: deleting an account's submission_flags: %w", err)
	}
	for _, table := range partTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE submission_id IN (SELECT id FROM submissions WHERE user_id = $1)`, account); err != nil {
			return fmt.Errorf("submission: deleting an account's %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM submissions WHERE user_id = $1`, account); err != nil {
		return fmt.Errorf("submission: deleting an account's submissions: %w", err)
	}
	return nil
}

// IDs returns the ids of account's submissions, in order.
func IDs(ctx context.Context, q Querier, account string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM submissions WHERE user_id = $1 ORDER BY id`, account)
	if err != nil {
		return nil, fmt.Errorf("submission: reading %s's submissions: %w", account, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("submission: reading %s's submissions: %w", account, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("submission: reading %s's submissions: %w", account, err)
	}
	return out, nil
}

// FlagsNaming counts the repo_overlap flags, of any account, whose detail
// names one of ids. Once DeleteAccount has run for the account ids were
// of, none does, which the eraser checks (D-sb1-flags-erasure).
func FlagsNaming(ctx context.Context, q Querier, ids []string) (int, error) {
	flags, err := overlapFlags(ctx, q, "", ids)
	return len(flags), err
}

// overlapFlag is a repo_overlap flag: the submission it is on, and the
// submissions its detail names.
type overlapFlag struct {
	on    string
	names []string
}

// overlapFlags returns the repo_overlap flags whose detail names one of
// ids, other than account's own, read through q.
func overlapFlags(ctx context.Context, q Querier, account string, ids []string) ([]overlapFlag, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	named := make(map[string]bool, len(ids))
	for _, id := range ids {
		named[id] = true
	}
	rows, err := q.QueryContext(ctx, `SELECT submission_id, user_id, detail FROM submission_flags WHERE code = $1`, FlagRepoOverlap)
	if err != nil {
		return nil, fmt.Errorf("submission: reading the repo_overlap flags: %w", err)
	}
	defer rows.Close()
	var out []overlapFlag
	for rows.Next() {
		var on, owner, detail string
		if err := rows.Scan(&on, &owner, &detail); err != nil {
			return nil, fmt.Errorf("submission: reading the repo_overlap flags: %w", err)
		}
		if owner == account {
			continue
		}
		var d overlapDetail
		if err := json.Unmarshal([]byte(detail), &d); err != nil {
			return nil, fmt.Errorf("submission: %s's repo_overlap flag holds %q: %w", on, detail, err)
		}
		if slices.ContainsFunc(d.Submissions, func(s string) bool { return named[s] }) {
			out = append(out, overlapFlag{on: on, names: d.Submissions})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("submission: reading the repo_overlap flags: %w", err)
	}
	return out, nil
}

// unname takes ids, account's submissions, out of every other account's
// repo_overlap flag that names one, inside tx, and deletes a flag that
// then names none.
func unname(ctx context.Context, tx writer.Tx, account string, ids []string) error {
	flags, err := overlapFlags(ctx, tx, account, ids)
	if err != nil {
		return err
	}
	for _, f := range flags {
		keep := slices.DeleteFunc(slices.Clone(f.names), func(s string) bool { return slices.Contains(ids, s) })
		if len(keep) == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM submission_flags WHERE submission_id = $1 AND code = $2`, f.on, FlagRepoOverlap); err != nil {
				return fmt.Errorf("submission: deleting %s's repo_overlap flag: %w", f.on, err)
			}
			continue
		}
		b, err := json.Marshal(overlapDetail{Submissions: keep})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE submission_flags SET detail = $3 WHERE submission_id = $1 AND code = $2`, f.on, FlagRepoOverlap, string(b)); err != nil {
			return fmt.Errorf("submission: updating %s's repo_overlap flag: %w", f.on, err)
		}
	}
	return nil
}
