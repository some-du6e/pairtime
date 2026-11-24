// SPDX-License-Identifier: AGPL-3.0-or-later

// Package submission is pairtimed's submissions API (WP-SB1; DESIGN 7.5
// steps 1 and 2, SP 10.2): a submission of a set of projects P and a
// window W or a list of devlog windows (D13), frozen with the scoring
// version, the parameter hash and a fresh 32-byte r whose SHA-256 alone
// it answers with, then moved through its states by the later work
// packages: the metadata reveal (WP-SB4), the challenge and the content
// reveal (WP-SB5), the checks (WP-TR2, WP-TR3) and review (WP-RV4,
// WP-RV5). Every state change goes through Move.
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

// transitions are the legal moves out of each state. A submission moves
// one step at a time down the chain, a teen can withdraw it in any state
// before a decision, a decision can be made again, and a decided
// submission can be revoked. Withdrawn and revoked are final. Who makes
// each move:
//
//   - created to frozen: the freeze (WP-SB1); a teen's create freezes at once;
//   - frozen to metadata_revealed: the stored metadata reveal (WP-SB4);
//   - metadata_revealed to challenged, and on to content_revealed: the
//     draw and the content reveal (WP-SB5), straight through on an empty
//     draw;
//   - content_revealed to checked: step 9's checks (WP-TR2, WP-TR3);
//   - checked to in_review, in_review to decided, and decided to decided:
//     assignment, the outcome and a re-decision (WP-RV4);
//   - decided to revoked: a revocation (WP-RV5);
//   - any state before decided to withdrawn: the teen (WP-SB1).
var transitions = map[State][]State{
	Created:          {Frozen, Withdrawn},
	Frozen:           {MetadataRevealed, Withdrawn},
	MetadataRevealed: {Challenged, Withdrawn},
	Challenged:       {ContentRevealed, Withdrawn},
	ContentRevealed:  {Checked, Withdrawn},
	Checked:          {InReview, Withdrawn},
	InReview:         {Decided, Withdrawn},
	Decided:          {Decided, Revoked},
}

// Legal reports whether a submission may move from one state to another.
func Legal(from, to State) bool { return slices.Contains(transitions[from], to) }

// timeColumns are the submissions columns that record when a submission
// last reached a state. Its spot-check window opens at the freeze, the
// latest decision or a withdrawal (DESIGN 4.4), so those times stay.
var timeColumns = map[State]string{
	Frozen:           "frozen_at_ms",
	MetadataRevealed: "metadata_revealed_at_ms",
	Decided:          "decided_at_ms",
	Withdrawn:        "withdrawn_at_ms",
	Revoked:          "revoked_at_ms",
}

// ErrNotFound is what Move returns for a submission the server doesn't
// hold.
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO submission_events (submission_id, seq, from_state, to_state, at_ms)
SELECT $1, COALESCE(MAX(seq), 0) + 1, $2, $3, $4 FROM submission_events WHERE submission_id = $1`, id, string(from), string(to), atMS); err != nil {
		return fmt.Errorf("submission: noting %s's move to %s: %w", id, to, err)
	}
	return nil
}
