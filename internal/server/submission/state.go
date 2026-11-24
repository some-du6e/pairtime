// SPDX-License-Identifier: AGPL-3.0-or-later

// Package submission is pairtimed's submissions API (WP-SB1; DESIGN 7.5
// steps 1 and 2, SP 10.2; D-sb1-submissions; docs/spec/submissions-v1.md):
// a submission of a set of projects P and a window W or a list of devlog
// windows (D13), frozen with each (device id, project key) pair P maps to,
// the scoring version, the parameter hash and a fresh 32-byte r whose
// SHA-256 alone it answers with, then moved through its states by the
// later work packages: the metadata reveal (WP-SB4), the draw and the
// content reveal (WP-SB5), the checks (WP-TR3) and review (WP-RV4,
// WP-RV5). Every state change goes through Move.
//
// A teen makes a submission with a web session that has a fresh login, or
// from one of the account's computers, with the device token and a request
// signed by the device key (signed.go). The create freezes at once, in one
// writer op that checks, in order, the account's request id, the account's
// verification, the caps on what one account holds, the windows, and the
// overlap with the account's other frozen submissions on a shared key
// (Create). A refused create stores nothing and draws no r.
//
// The record stays as long as the account (DESIGN 4.4's submissions row):
// a withdrawal deletes nothing, and only the eraser, through DeleteAccount,
// deletes it. The freeze's risk flags are evidence-store data
// (evidence.revealed), deleted once the spot-check window closes.
package submission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// State is a submission's state (DESIGN 7.5, PLAN WP-SB1).
type State string

// The states, in the order a submission moves through them, then the two
// it can end in early.
const (
	Created          State = "created"
	Frozen           State = "frozen"
	MetadataRevealed State = "metadata_revealed"
	Challenged       State = "challenged"
	ContentRevealed  State = "content_revealed"
	Checked          State = "checked"
	InReview         State = "in_review"
	Decided          State = "decided"
	Withdrawn        State = "withdrawn"
	Revoked          State = "revoked"
)

// States is every state, in the order above.
var States = []State{Created, Frozen, MetadataRevealed, Challenged, ContentRevealed, Checked, InReview, Decided, Withdrawn, Revoked}

// transitions are the legal moves out of each state: one linear chain,
// with no step skipped (D-sb1-submissions, Q15). The account can withdraw
// a submission until step 9's checks have run, and never after, so no one
// can re-roll an audit or a reviewer by submitting the same time again
// (DESIGN 7.5 step 5). A decision can be made again, and a decided
// submission can be revoked. Withdrawn and revoked are final. Who makes
// each move:
//
//   - created to frozen: the freeze (WP-SB1), at once for a teen's create,
//     and WP-PG6's confirmation of a program-made submission;
//   - frozen to metadata_revealed: WP-SB4, in the writer transaction that
//     marks the metadata reveal's index row stored;
//   - metadata_revealed to challenged: WP-SB5, in the writer transaction
//     that stores the draw, which is when r can first be read (RevealedR);
//   - challenged to content_revealed: WP-SB5, once every drawn item is
//     revealed, withheld or expired, in the same op as the draw when the
//     pool is empty;
//   - content_revealed to checked: WP-TR3, in the transaction that stores
//     the risk score and the audit draw. A submission to a program that
//     takes no hours, calibration's, stays at content_revealed;
//   - checked to in_review, in_review to decided, and decided to decided:
//     assignment, the outcome and a re-decision (WP-RV4);
//   - decided to revoked: a revocation (WP-RV5);
//   - created through content_revealed to withdrawn: the account (WP-SB1).
var transitions = map[State][]State{
	Created:          {Frozen, Withdrawn},
	Frozen:           {MetadataRevealed, Withdrawn},
	MetadataRevealed: {Challenged, Withdrawn},
	Challenged:       {ContentRevealed, Withdrawn},
	ContentRevealed:  {Checked, Withdrawn},
	Checked:          {InReview},
	InReview:         {Decided},
	Decided:          {Decided, Revoked},
}

// Legal reports whether a submission may move from one state to another.
func Legal(from, to State) bool { return slices.Contains(transitions[from], to) }

// timeColumns are the submissions columns that record when a submission
// last reached a state. Its spot-check window opens at the freeze, the
// latest decision or a withdrawal (DESIGN 4.4), r can be read from the
// draw on (challenged_at_ms), and submission_events keeps the time of
// every move.
var timeColumns = map[State]string{
	Frozen:           "frozen_at_ms",
	MetadataRevealed: "metadata_revealed_at_ms",
	Challenged:       "challenged_at_ms",
	Decided:          "decided_at_ms",
	Withdrawn:        "withdrawn_at_ms",
	Revoked:          "revoked_at_ms",
}

// ErrNotFound is what Move and the reads return for a submission the
// server doesn't hold.
var ErrNotFound = errors.New("submission: no such submission")

// ErrIllegal is what Move returns, wrapped, for a move transitions doesn't
// allow. It changes nothing.
var ErrIllegal = errors.New("submission: that move isn't legal")

// Move moves the submission id to the state to at atMS, inside tx: it
// sets the state and updated_at_ms, the time column of the state if it
// has one, and adds the move to submission_events. A move transitions
// doesn't allow gets ErrIllegal and changes nothing.
func Move(ctx context.Context, tx writer.Tx, id string, to State, atMS int64) error {
	var from State
	err := tx.QueryRowContext(ctx, `SELECT state FROM submissions WHERE id = $1`, id).Scan(&from)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("submission: reading %s: %w", id, err)
	case !Legal(from, to):
		return fmt.Errorf("%w: %s to %s", ErrIllegal, from, to)
	}
	set := "state = $2, updated_at_ms = $3"
	if col := timeColumns[to]; col != "" {
		set += ", " + col + " = $3"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE submissions SET `+set+` WHERE id = $1`, id, string(to), atMS); err != nil {
		return fmt.Errorf("submission: moving %s to %s: %w", id, to, err)
	}
	// The writer runs one op at a time, so no other move of the submission
	// can take the next seq between the read and the insert.
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM submission_events WHERE submission_id = $1`, id).Scan(&seq); err != nil {
		return fmt.Errorf("submission: reading %s's moves: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO submission_events (submission_id, seq, from_state, to_state, at_ms)
VALUES ($1, $2, $3, $4, $5)`, id, seq+1, string(from), string(to), atMS); err != nil {
		return fmt.Errorf("submission: noting %s's move to %s: %w", id, to, err)
	}
	return nil
}

// Touch marks the submission id changed at atMS, inside tx, without a
// move: what a later work package calls when it changes what a program
// sees, such as WP-SB4's totals, so GET /api/v1/submissions?updated_since=
// lists it again.
func Touch(ctx context.Context, tx writer.Tx, id string, atMS int64) error {
	res, err := tx.ExecContext(ctx, `UPDATE submissions SET updated_at_ms = $2 WHERE id = $1`, id, atMS)
	if err != nil {
		return fmt.Errorf("submission: touching %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("submission: touching %s: %w", id, err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
