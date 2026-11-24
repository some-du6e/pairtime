// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// The risk flags the freeze raises (DESIGN 4.4's evidence-store row, 8.5).
// Reviewers see them, and no teen or program view does.
const (
	// FlagConcurrentDevices counts the minutes of the frozen windows in
	// which two of the account's devices credited one slot and at least
	// one of them credited it to one of P's projects (SM 3.4;
	// D-no-ingest-flags; D-sb1-submissions, Q8). Its detail is
	// {"minutes": n}.
	FlagConcurrentDevices = "concurrent_devices"
	// FlagRepoOverlap names the other accounts' submissions on the same
	// repo fingerprint whose windows overlap this one's (SP 10.2;
	// D-sb1-submissions, Q11). Its detail is {"submissions": [ids]}.
	FlagRepoOverlap = "repo_overlap"
)

// AccountKey is a (device id, project key) pair of an account.
type AccountKey struct {
	Account string
	Key
}

// Fingerprints finds the projects of other accounts that share a repo
// fingerprint, HMAC(pepper, root_sha), with a submission's projects (SP
// 10.2). WP-SV10 stores the fingerprints and implements it, and whichever
// of WP-SB1 and WP-SV10 lands second wires its version into pairtimed.
// Until then pairtimed wires NoFingerprints, which finds none.
type Fingerprints interface {
	// Sharing returns every (account, device id, project key) of an
	// account other than account whose project has a repo fingerprint that
	// the project of one of keys, account's pairs, has too, read through
	// q, the freeze's writer transaction.
	Sharing(ctx context.Context, q Querier, account string, keys []Key) ([]AccountKey, error)
}

// NoFingerprints finds no shared fingerprint: the Fingerprints pairtimed
// wires until WP-SV10 stores them.
type NoFingerprints struct{}

// Sharing finds none.
func (NoFingerprints) Sharing(context.Context, Querier, string, []Key) ([]AccountKey, error) {
	return nil, nil
}

// minuteMS is a minute in milliseconds.
const minuteMS = 60_000

// concurrentMinutes counts the minutes the windows ws touch in which two
// of the account's devices credited one slot and at least one of them
// credited it to one of projects, P's project ids (D-sb1-submissions, Q8).
// A minute that two windows touch counts once, and a slot that only other
// projects competed for is never counted, since fraud checks run only on
// the hours a teen submits (DESIGN 8.5).
func concurrentMinutes(ctx context.Context, q Querier, account string, ws []Window, projects []string) (int, error) {
	// The minutes a window touches run from the one it starts in to the
	// last one that starts before it ends. Spans that share a minute are
	// merged, so no minute is read twice.
	type span struct{ from, to int64 }
	spans := make([]span, 0, len(ws))
	for _, w := range ws {
		spans = append(spans, span{from: w.Start - w.Start%minuteMS, to: w.End})
	}
	slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.from, b.from) })
	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && s.from < merged[n-1].to {
			merged[n-1].to = max(merged[n-1].to, s.to)
			continue
		}
		merged = append(merged, s)
	}
	total := 0
	for _, s := range merged {
		n, err := rollup.ConcurrentMinutes(ctx, q, account, s.from, s.to, projects)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// repoOverlap returns, in order, the ids of the submissions of other
// accounts that were frozen and not withdrawn, went to a program that
// takes hours, hold a pair whose repo fingerprint one of keys' projects
// shares, and have a window that overlaps one of ws (D-sb1-submissions,
// Q11).
func repoOverlap(ctx context.Context, tx writer.Tx, fp Fingerprints, account string, keys []Key, ws []Window) ([]string, error) {
	shared, err := fp.Sharing(ctx, tx, account, keys)
	if err != nil {
		return nil, fmt.Errorf("submission: reading the repo fingerprints of %s's keys: %w", account, err)
	}
	byAccount := make(map[string][]Key)
	for _, ak := range shared {
		if ak.Account != account {
			byAccount[ak.Account] = append(byAccount[ak.Account], ak.Key)
		}
	}
	var out []string
	for other, ks := range byAccount {
		refs, err := overlapping(ctx, tx, other, ks, ws)
		if err != nil {
			return nil, err
		}
		for _, r := range refs {
			out = append(out, r.ID)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// raiseFlags raises the freeze's flags on the new submission id, inside
// its writer transaction at nowMS: concurrent_devices when the frozen
// windows hold such a minute, and repo_overlap on it and on each other
// submission it names that is still undecided, each naming the other, so
// two freezes in one batch flag each other. A submission to a program
// that takes no hours gets no flags, and the caller doesn't call it.
func raiseFlags(ctx context.Context, tx writer.Tx, fp Fingerprints, id, account string, projects []string, keys []Key, ws []Window, nowMS int64) error {
	n, err := concurrentMinutes(ctx, tx, account, ws, projects)
	if err != nil {
		return err
	}
	if n > 0 {
		if err := setFlag(ctx, tx, id, FlagConcurrentDevices, map[string]int{"minutes": n}, nowMS); err != nil {
			return err
		}
	}
	others, err := repoOverlap(ctx, tx, fp, account, keys, ws)
	if err != nil || len(others) == 0 {
		return err
	}
	if err := setFlag(ctx, tx, id, FlagRepoOverlap, overlapDetail{Submissions: others}, nowMS); err != nil {
		return err
	}
	for _, other := range others {
		var decided sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT decided_at_ms FROM submissions WHERE id = $1`, other).Scan(&decided); err != nil {
			return fmt.Errorf("submission: reading %s: %w", other, err)
		}
		if decided.Valid {
			continue
		}
		var d overlapDetail
		if f, ok, err := flagOf(ctx, tx, other, FlagRepoOverlap); err != nil {
			return err
		} else if ok {
			if err := json.Unmarshal([]byte(f.Detail), &d); err != nil {
				return fmt.Errorf("submission: %s's repo_overlap flag holds %q: %w", other, f.Detail, err)
			}
		}
		if slices.Contains(d.Submissions, id) {
			continue
		}
		d.Submissions = append(d.Submissions, id)
		slices.Sort(d.Submissions)
		if err := setFlag(ctx, tx, other, FlagRepoOverlap, d, nowMS); err != nil {
			return err
		}
	}
	return nil
}

// overlapDetail is a repo_overlap flag's detail.
type overlapDetail struct {
	Submissions []string `json:"submissions"`
}

// setFlag stores submission id's flag code with its detail as JSON, raised
// at nowMS, in place of the one it held.
func setFlag(ctx context.Context, tx writer.Tx, id, code string, detail any, nowMS int64) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO submission_flags (submission_id, code, detail, raised_at_ms) VALUES ($1, $2, $3, $4)
ON CONFLICT (submission_id, code) DO UPDATE SET detail = excluded.detail, raised_at_ms = excluded.raised_at_ms`, id, code, string(b), nowMS); err != nil {
		return fmt.Errorf("submission: raising %s on %s: %w", code, id, err)
	}
	return nil
}

// Flag is a risk flag of a submission, for reviewers (WP-RV1, WP-RV2).
type Flag struct {
	Code string
	// Detail is the flag's detail, as JSON.
	Detail   string
	RaisedAt time.Time
}

// flagOf returns submission id's flag code, and whether it has one.
func flagOf(ctx context.Context, q Querier, id, code string) (Flag, bool, error) {
	f := Flag{Code: code}
	var raised int64
	err := q.QueryRowContext(ctx, `SELECT detail, raised_at_ms FROM submission_flags WHERE submission_id = $1 AND code = $2`, id, code).Scan(&f.Detail, &raised)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Flag{}, false, nil
	case err != nil:
		return Flag{}, false, fmt.Errorf("submission: reading %s's %s flag: %w", id, code, err)
	}
	f.RaisedAt = time.UnixMilli(raised).UTC()
	return f, true, nil
}

// Flags returns submission id's risk flags, by code: what reviewers see,
// and no teen or program view shows.
func Flags(ctx context.Context, q Querier, id string) ([]Flag, error) {
	rows, err := q.QueryContext(ctx, `SELECT code, detail, raised_at_ms FROM submission_flags WHERE submission_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("submission: reading %s's flags: %w", id, err)
	}
	defer rows.Close()
	var out []Flag
	for rows.Next() {
		var f Flag
		var raised int64
		if err := rows.Scan(&f.Code, &f.Detail, &raised); err != nil {
			return nil, fmt.Errorf("submission: reading %s's flags: %w", id, err)
		}
		f.RaisedAt = time.UnixMilli(raised).UTC()
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("submission: reading %s's flags: %w", id, err)
	}
	slices.SortFunc(out, func(a, b Flag) int { return cmp.Compare(a.Code, b.Code) })
	return out, nil
}
