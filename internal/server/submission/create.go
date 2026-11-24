// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// An account's caps (D-sb1-submissions, Q1): the submissions made with its
// own credentials, a web session's or its computers', withdrawn ones
// included, and the frozen windows across all of its submissions. The
// record stays as long as the account, so without them a loop of creates
// and withdrawals from one device token could fill the server's disk.
// They are the server's own limits, as the devlog caps are (#283).
const (
	MaxSubmissions = 1000
	MaxWindows     = 10_000
)

// MaxProjects is the most projects one create may name.
const MaxProjects = 32

// The refusals of a create and a withdrawal. Each stores nothing.
var (
	// ErrRequestConflict is a request id the account used for another
	// request.
	ErrRequestConflict = errors.New("submission: the request id was used for another request")
	// ErrProgramUnknown is a program [[programs]] doesn't list.
	ErrProgramUnknown = errors.New("submission: this server takes no submissions for the program")
	// ErrNotVerified is an account whose identity Hack Club Auth hasn't
	// verified (DESIGN 8.2).
	ErrNotVerified = errors.New("submission: the account isn't verified")
	// ErrProjectUnknown is a project id or key the account doesn't hold.
	ErrProjectUnknown = errors.New("submission: no such project")
	// ErrLimit is a create past MaxSubmissions or MaxWindows.
	ErrLimit = errors.New("submission: the account's limit is reached")
	// ErrWindowInvalid is a window that doesn't start before it ends, ends
	// after the freeze, or starts before the submission horizon.
	ErrWindowInvalid = errors.New("submission: the window is outside what a submission can freeze")
	// ErrNoDevlogWindows is a devlog-mode create whose cut list is empty.
	ErrNoDevlogWindows = errors.New("submission: there are no devlog windows to freeze")
	// ErrNotWithdrawable is a withdrawal once step 9's checks have run.
	ErrNotWithdrawable = errors.New("submission: the submission can't be withdrawn now")
)

// OverlapError is the overlap refusal (D-sb1-submissions, Q2): the
// account's submissions whose windows the new one's overlap on a shared
// key.
type OverlapError struct {
	Submissions []Ref
}

func (e *OverlapError) Error() string {
	return fmt.Sprintf("submission: the windows overlap %d of the account's submissions", len(e.Submissions))
}

// createInput is a teen's create, as the handler checked its form.
type createInput struct {
	Account string
	// CreatedBy is BySession or ByDevice, and Device the calling device,
	// "" for a session.
	CreatedBy, Device string
	// Program is the program's name, which the op looks up in
	// [[programs]] once it knows the request isn't a retry.
	Program   string
	RequestID string
	// RequestSHA256 is requestSum of the request.
	RequestSHA256 string
	// Projects are the project ids the teen picked, and ProjectKeys the
	// calling device's keys of them: exactly one is set, sorted.
	Projects, ProjectKeys []string
	// Window is the window W, or nil for devlog windows.
	Window *Window
}

// requestSum is the SHA-256, in lowercase hex, of a teen's create in its
// canonical form: the program, the projects by id or by the calling
// device's keys, sorted, and the window or the choice of devlog windows. A
// retry under the same request id has the same one (D-sb1-submissions,
// Q5). Keys are a device's own, so the device is part of a request that
// names them.
func requestSum(program, device string, projects, keys []string, w *Window) string {
	var b strings.Builder
	b.WriteString("pairtime-submission-request-v1\n")
	b.WriteString(program + "\n")
	if keys != nil {
		b.WriteString("project_keys " + device + "\n" + strings.Join(keys, ",") + "\n")
	} else {
		b.WriteString("projects\n" + strings.Join(projects, ",") + "\n")
	}
	if w == nil {
		b.WriteString(ModeDevlogWindows)
	} else {
		b.WriteString(ModeWindow + " " + strconv.FormatInt(w.Start, 10) + " " + strconv.FormatInt(w.End, 10))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// heldRequest returns the account's submission made under request id, or
// ErrNotFound.
func heldRequest(ctx context.Context, q Querier, account, requestID string) (string, string, error) {
	var id, sum string
	err := q.QueryRowContext(ctx, `SELECT id, request_sha256 FROM submissions WHERE user_id = $1 AND request_id = $2`, account, requestID).Scan(&id, &sum)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", ErrNotFound
	case err != nil:
		return "", "", fmt.Errorf("submission: reading %s's request: %w", account, err)
	}
	return id, sum, nil
}

// eligibility returns whether the account may submit, a verified
// identity, and whether its submissions carry the eligibility note
// (DESIGN 8.2), from its users row inside tx.
func eligibility(ctx context.Context, q Querier, account string) (verified, toConfirm bool, err error) {
	var status string
	var ysws sql.NullInt64
	err = q.QueryRowContext(ctx, `SELECT verification_status, ysws_eligible FROM users WHERE id = $1`, account).Scan(&status, &ysws)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, false, nil
	case err != nil:
		return false, false, fmt.Errorf("submission: reading %s's verification: %w", account, err)
	}
	verified = status == "verified"
	return verified, verified && (!ysws.Valid || ysws.Int64 != 1), nil
}

// frozenPair is a pair of P with the project it maps to.
type frozenPair struct {
	Key
	project string
}

// resolve returns P's project ids and every (device id, project key) pair
// they map to through rollup_projects now (D-sb1-submissions, Q5 and
// Q17): for project ids, the account's projects; for the calling device's
// keys, the projects those keys map to. Either way it is every pair of
// those projects, on every computer of the account, so a key stands for
// its project. An id or key the account doesn't hold gets
// ErrProjectUnknown.
func resolve(ctx context.Context, q Querier, account, device string, projects, keys []string) ([]string, []frozenPair, error) {
	rows, err := q.QueryContext(ctx, `SELECT device_id, project_key, project_id FROM rollup_projects WHERE user_id = $1`, account)
	if err != nil {
		return nil, nil, fmt.Errorf("submission: reading %s's projects: %w", account, err)
	}
	var all []frozenPair
	for rows.Next() {
		var p frozenPair
		if err := rows.Scan(&p.Device, &p.ProjectKey, &p.project); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("submission: reading %s's projects: %w", account, err)
		}
		all = append(all, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("submission: reading %s's projects: %w", account, err)
	}
	ids := make(map[string]bool)
	if keys != nil {
		for _, k := range keys {
			i := slices.IndexFunc(all, func(p frozenPair) bool { return p.Device == device && p.ProjectKey == k })
			if i < 0 {
				return nil, nil, ErrProjectUnknown
			}
			ids[all[i].project] = true
		}
	} else {
		for _, id := range projects {
			if !slices.ContainsFunc(all, func(p frozenPair) bool { return p.project == id }) {
				return nil, nil, ErrProjectUnknown
			}
			ids[id] = true
		}
	}
	var pairs []frozenPair
	for _, p := range all {
		if ids[p.project] {
			pairs = append(pairs, p)
		}
	}
	slices.SortFunc(pairs, func(a, b frozenPair) int { return compareKeys(a.Key, b.Key) })
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	slices.Sort(out)
	return out, pairs, nil
}

func keysOf(pairs []frozenPair) []Key {
	out := make([]Key, len(pairs))
	for i, p := range pairs {
		out[i] = p.Key
	}
	return out
}

// count runs query, which selects one COUNT, and returns it.
func count(ctx context.Context, q Querier, query string, args ...any) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// create runs a teen's create and freeze inside the writer transaction tx
// (DESIGN 7.5 steps 1 and 2; D-sb1-submissions). In order: a request id
// the account used before answers with the submission it made then, in
// whatever state it is in now, when the request is the same, even when
// [[programs]] no longer lists its program, and gets ErrRequestConflict
// when it isn't; a program the list doesn't hold gets ErrProgramUnknown;
// an account that isn't verified gets ErrNotVerified; one at MaxSubmissions gets ErrLimit; P resolves to its
// pairs, or ErrProjectUnknown; the windows are checked, or made from the
// devlogs, or ErrWindowInvalid or ErrNoDevlogWindows; windows past
// MaxWindows get ErrLimit; and, for a program that takes hours, windows
// that overlap the account's other frozen submissions on a shared key get
// an *OverlapError. Each refusal stores nothing and draws no r. Then it
// stores the submission with P, its pairs and its windows, the scoring
// version, the parameter hash, a fresh r and its SHA-256 and the
// eligibility note, moves it to frozen, raises its flags, and calls
// OnFreeze. It returns the submission and whether this call made it.
func (s *Service) create(ctx context.Context, tx writer.Tx, in createInput) (Submission, bool, error) {
	if id, sum, err := heldRequest(ctx, tx, in.Account, in.RequestID); err == nil {
		if sum != in.RequestSHA256 {
			return Submission{}, false, ErrRequestConflict
		}
		sub, err := Get(ctx, tx, id)
		return sub, false, err
	} else if !errors.Is(err, ErrNotFound) {
		return Submission{}, false, err
	}
	program, ok := s.programs[in.Program]
	if !ok {
		return Submission{}, false, ErrProgramUnknown
	}
	verified, toConfirm, err := eligibility(ctx, tx, in.Account)
	if err != nil {
		return Submission{}, false, err
	}
	if !verified {
		return Submission{}, false, ErrNotVerified
	}
	made, err := count(ctx, tx, `SELECT COUNT(*) FROM submissions WHERE user_id = $1 AND created_by IN ('session', 'device')`, in.Account)
	if err != nil {
		return Submission{}, false, fmt.Errorf("submission: counting %s's submissions: %w", in.Account, err)
	}
	if made >= MaxSubmissions {
		return Submission{}, false, ErrLimit
	}
	projects, pairs, err := resolve(ctx, tx, in.Account, in.Device, in.Projects, in.ProjectKeys)
	if err != nil {
		return Submission{}, false, err
	}
	keys := keysOf(pairs)
	now := s.o.Now().UnixMilli()
	horizon := now - s.horizon.Milliseconds()
	mode := ModeWindow
	var ws []Window
	if in.Window != nil {
		w := *in.Window
		if w.Start >= w.End || w.End > now || w.Start < horizon {
			return Submission{}, false, ErrWindowInvalid
		}
		ws = []Window{w}
	} else {
		mode = ModeDevlogWindows
		if ws, _, err = devlogWindows(ctx, tx, in.Account, projects, keys, horizon); err != nil {
			return Submission{}, false, err
		}
		if len(ws) == 0 {
			return Submission{}, false, ErrNoDevlogWindows
		}
	}
	frozen, err := count(ctx, tx, `SELECT COUNT(*) FROM submission_windows w JOIN submissions s ON s.id = w.submission_id WHERE s.user_id = $1`, in.Account)
	if err != nil {
		return Submission{}, false, fmt.Errorf("submission: counting %s's windows: %w", in.Account, err)
	}
	if frozen+int64(len(ws)) > MaxWindows {
		return Submission{}, false, ErrLimit
	}
	if program.TakesHours {
		refs, err := overlapping(ctx, tx, in.Account, keys, ws)
		if err != nil {
			return Submission{}, false, err
		}
		if len(refs) > 0 {
			return Submission{}, false, &OverlapError{Submissions: refs}
		}
	}

	id := newID(time.UnixMilli(now))
	r, rSum, err := newR()
	if err != nil {
		return Submission{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO submissions (id, user_id, program, takes_hours, request_id, request_sha256,
created_by, device_id, mode, state, scoring_version, params, r, r_sha256, eligibility_to_confirm, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16)`,
		id, in.Account, program.Name, bit(program.TakesHours), in.RequestID, in.RequestSHA256,
		in.CreatedBy, nullString(in.Device), mode, string(Created), s.scoringVersion, s.params, r, rSum, bit(toConfirm), now); err != nil {
		return Submission{}, false, fmt.Errorf("submission: storing a submission: %w", err)
	}
	for _, p := range projects {
		if _, err := tx.ExecContext(ctx, `INSERT INTO submission_projects (submission_id, project_id) VALUES ($1, $2)`, id, p); err != nil {
			return Submission{}, false, fmt.Errorf("submission: storing a submission's projects: %w", err)
		}
	}
	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO submission_keys (submission_id, user_id, device_id, project_key, project_id)
VALUES ($1, $2, $3, $4, $5)`, id, in.Account, p.Device, p.ProjectKey, p.project); err != nil {
			return Submission{}, false, fmt.Errorf("submission: storing a submission's keys: %w", err)
		}
	}
	for _, w := range ws {
		if _, err := tx.ExecContext(ctx, `INSERT INTO submission_windows (submission_id, start_ms, end_ms) VALUES ($1, $2, $3)`, id, w.Start, w.End); err != nil {
			return Submission{}, false, fmt.Errorf("submission: storing a submission's windows: %w", err)
		}
	}
	if err := Move(ctx, tx, id, Frozen, now); err != nil {
		return Submission{}, false, err
	}
	if program.TakesHours {
		if err := raiseFlags(ctx, tx, s.fingerprints, id, in.Account, projects, keys, ws, now); err != nil {
			return Submission{}, false, err
		}
	}
	sub, err := Get(ctx, tx, id)
	if err != nil {
		return Submission{}, false, err
	}
	if s.o.OnFreeze != nil {
		if err := s.o.OnFreeze(ctx, tx, sub); err != nil {
			return Submission{}, false, err
		}
	}
	return sub, true, nil
}

// withdraw withdraws the account's submission id inside tx at the
// server's clock (D-sb1-submissions, Q7): from created through
// content_revealed it moves it to withdrawn, which records withdrawn_at_ms,
// deletes nothing and frees its windows, and calls OnWithdraw; a
// submission already withdrawn comes back unchanged; one from checked on
// gets ErrNotWithdrawable; and another account's, or one the server
// doesn't hold, gets ErrNotFound.
func (s *Service) withdraw(ctx context.Context, tx writer.Tx, account, id string) (Submission, error) {
	sub, err := Get(ctx, tx, id)
	switch {
	case errors.Is(err, ErrNotFound) || err == nil && sub.Account != account:
		return Submission{}, ErrNotFound
	case err != nil:
		return Submission{}, err
	case sub.State == Withdrawn:
		return sub, nil
	case !Legal(sub.State, Withdrawn):
		return Submission{}, ErrNotWithdrawable
	}
	if err := Move(ctx, tx, id, Withdrawn, s.o.Now().UnixMilli()); err != nil {
		return Submission{}, err
	}
	if sub, err = Get(ctx, tx, id); err != nil {
		return Submission{}, err
	}
	if s.o.OnWithdraw != nil {
		if err := s.o.OnWithdraw(ctx, tx, sub); err != nil {
			return Submission{}, err
		}
	}
	return sub, nil
}

// sortedUnique reports whether list, once sorted, holds no value twice,
// and returns it sorted.
func sortedUnique(list []string) ([]string, bool) {
	out := slices.Clone(list)
	slices.Sort(out)
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			return nil, false
		}
	}
	return out, true
}
