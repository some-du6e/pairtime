// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/buildday"
	"github.com/some-du6e/pairtime/internal/server/devlog"
	"github.com/some-du6e/pairtime/internal/server/projectnames"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// Report is a request's answer (the hub's spec, "what to send back"): the
// request id and dry_run as the token had them, whether pairtimed holds
// anything of the person, the rows deleted by table, and what it keeps
// and why. It carries counts and reasons and nothing about the person.
type Report struct {
	RequestID string           `json:"request_id"`
	Found     bool             `json:"found"`
	DryRun    bool             `json:"dry_run"`
	Deleted   map[string]int64 `json:"deleted"`
	Retained  []Retained       `json:"retained"`
}

// Retained is one kind of row pairtimed keeps: a type of at most 100
// characters, how many, and a reason of at most 500 that a reviewer can
// check.
type Retained struct {
	Type   string `json:"type"`
	Count  int64  `json:"count"`
	Reason string `json:"reason"`
}

// newReport is c's report with nothing deleted or kept yet.
func newReport(c Claims) Report {
	return Report{RequestID: c.RequestID, DryRun: c.DryRun, Deleted: map[string]int64{}, Retained: []Retained{}}
}

// table is one table that holds an account's rows (DESIGN 4.4's server
// rows), and where selects them, with the account's id as $1.
type table struct {
	name, where string
}

// step deletes tables' rows: with del, the package that owns them, which
// the eraser checks left none; without it, a DELETE of the rows where
// selects.
type step struct {
	tables []table
	del    func(ctx context.Context, tx writer.Tx, account string) error
}

// steps are the eraser's deletes, in order: rows that point at users
// before users, and device_codes, which find an account's pending codes
// through its devices, before devices.
var steps = []step{
	{tables: []table{
		{"witness_rows", "account_id = $1"},
		{"ingest_replies", "account_id = $1"},
		{"minute_buckets", "account_id = $1"},
		{"minute_bucket_revisions", "account_id = $1"},
		{"commit_records", "account_id = $1"},
		{"device_labels", "account_id = $1"},
		{"request_origins", "account_id = $1"},
		{"rollup_projects", "user_id = $1"},
		{"rollup_minutes", "user_id = $1"},
		{"rollups_daily", "user_id = $1"},
	}},
	{tables: []table{{"project_names", "user_id = $1"}, {"project_name_hides", "user_id = $1"}}, del: projectnames.DeleteAccount},
	{tables: []table{{"build_days", "user_id = $1"}, {"build_weeks", "user_id = $1"}, {"build_goals", "user_id = $1"}, {"zone_changes", "user_id = $1"}}, del: buildday.DeleteAccount},
	{tables: []table{{"devlogs", "user_id = $1"}, {"badges", "user_id = $1"}, {"removed_items", "user_id = $1"}}, del: devlog.DeleteAccount},
	{tables: []table{
		{"sessions", "user_id = $1"},
		{"device_codes", "user_id = $1 OR device_id IN (SELECT device_id FROM devices WHERE user_id = $1)"},
		{"devices", "user_id = $1"},
		{"users", "id = $1"},
	}},
}

// AccountTables are the tables whose rows the eraser deletes and an
// export counts, in the order the eraser deletes them.
func AccountTables() []string {
	var out []string
	for _, s := range steps {
		for _, t := range s.tables {
			out = append(out, t.name)
		}
	}
	return out
}

// LeafTable keeps each published day's leaf hashes, the account's among
// them. The eraser deletes a day's row once no witness row of that day is
// left, and keeps it while one is, as DESIGN 8.9 says.
const LeafTable = "day_leaves"

// OtherTables are the tables that hold nothing of an account: the
// migrations' record, the operations probe, the server's keys and their
// history, the public day heads, and the jti the hub's tokens used. A
// test holds every table to one of these lists, so a table a later
// migration adds fails it until its rows are erased or it is listed
// here.
var OtherTables = []string{"schema_migrations", "ops_probe", "server_keys", "key_history", "day_heads", "privacy_token_uses"}

// leavesReason is why the eraser keeps an account's leaf hashes.
const leavesReason = "Each is a hash of one of the account's batches in the day tree of a day other accounts' batches share. Removing one would break the inclusion proofs of the day's other batches, so it stays until the last witness row of its day goes (DESIGN 8.9)."

// heldReason is an export's reason for every row it lists.
const heldReason = "held by pairtime; export is read-only"

// querier reads, as *sql.Tx and writer.Tx do.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// identityConflict is the 409 for a subject whose identifiers lead to
// more than one account, or to one whose Hack Club Auth identity isn't
// the subject's: it may be someone else's account, so a person decides.
var identityConflict = &Error{Status: http.StatusConflict, Code: "manual_review_required", Reason: "identity_conflict"}

// match returns the id of the account s names, or "" when none matches.
// An account matches by its Hack Club Auth identity, its Slack id or the
// keyed index of its email, each of which pairtimed only ever takes from
// Hack Club Auth's sign-in. Identifiers that lead to two accounts, or to
// one whose identity isn't s.IdentityID, are an identity conflict.
func match(ctx context.Context, q querier, emails *accounts.EmailCipher, s Subject) (string, error) {
	found := make(map[string]string)
	find := func(query string, arg any) error {
		rows, err := q.QueryContext(ctx, query, arg)
		if err != nil {
			return fmt.Errorf("privacy: finding the subject's account: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, sub string
			if err := rows.Scan(&id, &sub); err != nil {
				return fmt.Errorf("privacy: finding the subject's account: %w", err)
			}
			found[id] = sub
		}
		return rows.Err()
	}
	if s.IdentityID != "" {
		if err := find(`SELECT id, hca_sub FROM users WHERE hca_sub = $1`, s.IdentityID); err != nil {
			return "", err
		}
	}
	if s.SlackID != "" {
		if err := find(`SELECT id, hca_sub FROM users WHERE slack_id = $1`, s.SlackID); err != nil {
			return "", err
		}
	}
	for _, e := range s.Emails {
		if err := find(`SELECT id, hca_sub FROM users WHERE email_index = $1`, emails.Index(e)); err != nil {
			return "", err
		}
	}
	if len(found) > 1 {
		return "", identityConflict
	}
	for id, sub := range found {
		if s.IdentityID != "" && sub != s.IdentityID {
			return "", identityConflict
		}
		return id, nil
	}
	return "", nil
}

// count returns how many rows of t are the account's.
func count(ctx context.Context, q querier, t table, account string) (int64, error) {
	var n int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.name+` WHERE `+t.where, account).Scan(&n); err != nil {
		return 0, fmt.Errorf("privacy: counting the account's %s: %w", t.name, err)
	}
	return n, nil
}

// leafDays sorts the published days of the account's witness rows: keep
// counts the account's leaf hashes in days other accounts' rows share,
// and gone lists the days no other row is left on.
func leafDays(ctx context.Context, q querier, account string) (keep int64, gone []int64, err error) {
	rows, err := q.QueryContext(ctx, `SELECT received_at_ms / $2, COUNT(*) FROM witness_rows WHERE account_id = $1 GROUP BY 1`, account, int64(ledger.DayMS))
	if err != nil {
		return 0, nil, fmt.Errorf("privacy: reading the account's witness days: %w", err)
	}
	type day struct{ d, mine int64 }
	var days []day
	for rows.Next() {
		var d day
		if err := rows.Scan(&d.d, &d.mine); err != nil {
			rows.Close()
			return 0, nil, fmt.Errorf("privacy: reading the account's witness days: %w", err)
		}
		days = append(days, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("privacy: reading the account's witness days: %w", err)
	}
	for _, d := range days {
		var published, others int64
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM day_leaves WHERE day = $1`, d.d).Scan(&published); err != nil {
			return 0, nil, fmt.Errorf("privacy: reading day %d's leaves: %w", d.d, err)
		}
		if published == 0 {
			// Not published yet, or its leaves already went: the day's
			// tree will hold only the rows still there when it's built.
			continue
		}
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM witness_rows WHERE received_at_ms >= $1 AND received_at_ms < $2 AND account_id <> $3`,
			d.d*ledger.DayMS, (d.d+1)*ledger.DayMS, account).Scan(&others); err != nil {
			return 0, nil, fmt.Errorf("privacy: counting day %d's other witness rows: %w", d.d, err)
		}
		if others > 0 {
			keep += d.mine
		} else {
			gone = append(gone, d.d)
		}
	}
	return keep, gone, nil
}

// erase deletes every row of the account c's subject names, inside tx,
// and reports it (DESIGN 8.9): each table's rows, through the package
// that owns them where one does, and the leaves of each published day
// that no witness row is left on. It keeps the account's leaf hashes in
// days other accounts' batches share, and says so. pairtimed holds no
// fraud verdicts yet, so no minimal fraud record is kept, and the
// evidence store's blobs (WP-SB4) and backup segments aren't written yet.
func erase(ctx context.Context, tx writer.Tx, emails *accounts.EmailCipher, c Claims) (Report, error) {
	r := newReport(c)
	account, err := match(ctx, tx, emails, c.Subject)
	if err != nil || account == "" {
		return r, err
	}
	r.Found = true
	keep, gone, err := leafDays(ctx, tx, account)
	if err != nil {
		return r, err
	}
	for _, d := range gone {
		res, err := tx.ExecContext(ctx, `DELETE FROM day_leaves WHERE day = $1`, d)
		if err != nil {
			return r, fmt.Errorf("privacy: deleting day %d's leaves: %w", d, err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			r.Deleted[LeafTable] += n
		}
	}
	for _, s := range steps {
		if s.del == nil {
			for _, t := range s.tables {
				res, err := tx.ExecContext(ctx, `DELETE FROM `+t.name+` WHERE `+t.where, account)
				if err != nil {
					return r, fmt.Errorf("privacy: deleting the account's %s: %w", t.name, err)
				}
				n, err := res.RowsAffected()
				if err != nil {
					return r, fmt.Errorf("privacy: deleting the account's %s: %w", t.name, err)
				}
				if n > 0 {
					r.Deleted[t.name] += n
				}
			}
			continue
		}
		before := make([]int64, len(s.tables))
		for i, t := range s.tables {
			if before[i], err = count(ctx, tx, t, account); err != nil {
				return r, err
			}
		}
		if err := s.del(ctx, tx, account); err != nil {
			return r, err
		}
		for i, t := range s.tables {
			left, err := count(ctx, tx, t, account)
			if err != nil {
				return r, err
			}
			if left != 0 {
				return r, fmt.Errorf("privacy: deleting the account left %d rows of %s", left, t.name)
			}
			if before[i] > 0 {
				r.Deleted[t.name] += before[i]
			}
		}
	}
	if keep > 0 {
		r.Retained = append(r.Retained, Retained{Type: "leaf hashes in shared day trees", Count: keep, Reason: leavesReason})
	}
	return r, nil
}

// export reports what pairtimed holds of the account c's subject names,
// as erase would find it, and changes nothing: each table's rows, and the
// account's leaf hashes in published day trees, all as kept.
func export(ctx context.Context, q querier, emails *accounts.EmailCipher, c Claims) (Report, error) {
	r := newReport(c)
	account, err := match(ctx, q, emails, c.Subject)
	if err != nil || account == "" {
		return r, err
	}
	r.Found = true
	for _, s := range steps {
		for _, t := range s.tables {
			n, err := count(ctx, q, t, account)
			if err != nil {
				return r, err
			}
			if n > 0 {
				r.Retained = append(r.Retained, Retained{Type: t.name, Count: n, Reason: heldReason})
			}
		}
	}
	var leaves int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM witness_rows w WHERE w.account_id = $1 AND EXISTS (SELECT 1 FROM day_leaves d WHERE d.day = w.received_at_ms / $2)`,
		account, int64(ledger.DayMS)).Scan(&leaves); err != nil {
		return r, fmt.Errorf("privacy: counting the account's leaf hashes: %w", err)
	}
	if leaves > 0 {
		r.Retained = append(r.Retained, Retained{Type: "leaf hashes in day trees", Count: leaves, Reason: heldReason})
	}
	return r, nil
}

// errDryRun rolls a dry run's erasure back: the writer undoes the op
// that returns it, and no other op of its batch.
var errDryRun = errors.New("privacy: a dry run changes nothing")

// rowsOf is how many rows a report counts, the size the writer gives an
// erasure's batch.
func rowsOf(r Report) int {
	n := 0
	for _, k := range r.Retained {
		n += int(k.Count)
	}
	return n
}
