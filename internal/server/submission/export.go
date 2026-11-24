// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"fmt"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// accountTables are the tables DeleteAccount deletes an account's
// submissions from, each by submission_id, before submissions itself, whose
// rows the others' foreign keys name. submission_flags has no foreign key,
// and goes with them.
var accountTables = []string{"submission_flags", "submission_events", "submission_windows", "submission_keys", "submission_projects"}

// DeleteAccount deletes every submission of account, with its projects,
// keys, windows, moves and flags, inside tx: what the eraser (WP-SV13)
// runs when the account is deleted (DESIGN 8.9). A repo_overlap flag of
// another account's submission keeps the id of a submission this deletes,
// which then names nothing.
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

// Exported is what this package keeps for an account that the privacy
// hub's export holds (DESIGN 8.9; WP-SV13): each of its submissions as the
// teen view shows it, with r only once its draw is stored, and each change
// of its state. Risk flags are reviewers' and stay out of it, as they stay
// out of every teen view.
type Exported struct {
	Submissions []ExportedSubmission `json:"submissions"`
}

// ExportedSubmission is one submission in the export: its teen view
// without the program's display name or the review URL, which are the
// server's and not the account's, and its moves.
type ExportedSubmission struct {
	TeenView
	Events []Event `json:"events"`
}

// Export reads every submission of account for the privacy hub's export,
// and writes nothing. DeleteAccount deletes the same rows. It reads r
// through RevealedR, so a submission whose draw isn't stored shows none.
func Export(ctx context.Context, q Querier, account string) (Exported, error) {
	out := Exported{Submissions: []ExportedSubmission{}}
	var after *Cursor
	for {
		page, more, err := List(ctx, q, Scope{Account: account}, 0, after, MaxPage)
		if err != nil {
			return Exported{}, err
		}
		for _, sub := range page {
			r, err := revealed(ctx, q, sub)
			if err != nil {
				return Exported{}, err
			}
			events, err := Events(ctx, q, sub.ID)
			if err != nil {
				return Exported{}, err
			}
			out.Submissions = append(out.Submissions, ExportedSubmission{TeenView: teen(sub, r, "", ""), Events: events})
		}
		if !more {
			return out, nil
		}
		last := page[len(page)-1]
		after = &Cursor{UpdatedAtMS: last.UpdatedAt.UnixMilli(), ID: last.ID}
	}
}
