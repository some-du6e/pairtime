// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"fmt"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// accountTables are the tables DeleteAccount deletes an account's
// submissions from, each by submission_id, before submissions itself,
// whose rows the others' foreign keys name. submission_flags has no
// foreign key, and goes with them.
var accountTables = []string{"submission_flags", "submission_events", "submission_windows", "submission_keys", "submission_projects"}

// DeleteAccount deletes every submission of account, with its projects,
// keys, windows, moves and flags, inside tx: what the eraser (WP-SV13)
// runs when the account is deleted (DESIGN 8.9), and the privacy hub's
// export counts these tables. A repo_overlap flag of another account's
// submission keeps the id of a submission this deletes, which then names
// nothing.
func DeleteAccount(ctx context.Context, tx writer.Tx, account string) error {
	for _, table := range accountTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE submission_id IN (SELECT id FROM submissions WHERE user_id = $1)`, account); err != nil {
			return fmt.Errorf("submission: deleting an account's %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM submissions WHERE user_id = $1`, account); err != nil {
		return fmt.Errorf("submission: deleting an account's submissions: %w", err)
	}
	return nil
}
