// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/privacy"
	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// TestTables holds every table the migrations create to one place: the
// eraser's account tables, the day leaves it keeps or deletes by day, or
// the tables that hold nothing of an account. A migration that adds a
// table fails it until the eraser deletes the table's rows of an account
// or the table is listed as holding none, in both OtherTables and
// retention.json's no_personal_data.
func TestTables(t *testing.T) {
	f := newFixture(t, store.SQLite)
	placed := make(map[string]string)
	for _, n := range privacy.AccountTables() {
		placed[n] = "an account table"
	}
	placed[privacy.LeafTable] = "the leaf table"
	for _, n := range privacy.OtherTables {
		placed[n] = "a table with nothing of an account"
	}
	have := f.tables(t)
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{name: "every table the migrations create is the eraser's, the leaf table, or holds nothing of an account", check: func(t *testing.T) {
			for _, n := range have {
				if placed[n] == "" {
					t.Errorf("%s is in no list: add it to the eraser's steps, or, if it holds nothing of an account, to both OtherTables and retention.json's no_personal_data", n)
				}
			}
		}},
		{name: "the tables the eraser leaves are exactly retention.json's no_personal_data, so no other table can skip it", check: func(t *testing.T) {
			p, err := retention.Default()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := slices.Sorted(slices.Values(privacy.OtherTables)), slices.Sorted(slices.Values(p.NoPersonalData)); !slices.Equal(got, want) {
				t.Errorf("OtherTables is %v, and no_personal_data %v: a table that holds an account's rows goes in the eraser's steps, and one kept after an erasure gets a list of its own, as LeafTable does", got, want)
			}
		}},
		{name: "submission_flags carries user_id, which the eraser deletes and counts an account's flags by, since a flag names its submission with no foreign key (D-sb1-flags-erasure)", check: func(t *testing.T) {
			rows, err := f.db.Read.QueryContext(context.Background(), `SELECT * FROM submission_flags WHERE 1 = 0`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(cols, "user_id") {
				t.Errorf("submission_flags has the columns %v, and no user_id", cols)
			}
		}},
		{name: "every table the lists name exists", check: func(t *testing.T) {
			for n, what := range placed {
				if !slices.Contains(have, n) {
					t.Errorf("%s is %s, and the migrations create no such table", n, what)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// world seeds the teen with two witness rows on the shared day, one on
// the day only the teen's batches are in and one on the open day, and the
// other account with one on the shared day, and publishes the shared and
// the alone day. It returns what the eraser must delete of the teen.
func world(t *testing.T, f *fixture) map[string]int64 {
	t.Helper()
	want := f.seed(t, teen, sharedDay, sharedDay, aloneDay, openDay)
	f.seed(t, other, sharedDay)
	f.publish(t, sharedDay, aloneDay)
	// The seed holds rows in every table the eraser claims, so a table
	// whose rows it couldn't find would show as one not deleted.
	seeded := slices.Sorted(maps.Keys(want))
	if claimed := slices.Sorted(slices.Values(privacy.AccountTables())); !slices.Equal(seeded, claimed) {
		t.Fatalf("the seed covers %v, and the eraser claims %v", seeded, claimed)
	}
	// The alone day's leaves go with its last witness row.
	want[privacy.LeafTable] = 1
	return want
}

// keptLeaves is what the eraser keeps of the teen: the leaf hashes of its
// two batches on the shared day.
func keptLeaves(t *testing.T, r privacy.Report) {
	t.Helper()
	if len(r.Retained) != 1 || r.Retained[0].Type != "leaf hashes in shared day trees" || r.Retained[0].Count != 2 ||
		r.Retained[0].Reason == "" || len(r.Retained[0].Reason) > 500 || len(r.Retained[0].Type) > 100 {
		t.Errorf("retained %+v, want the teen's 2 leaf hashes in the shared day's tree, alone, with a reason", r.Retained)
	}
}

// marks are the strings that name the teen in any row the teen has.
func marks(f *fixture, p person) []string {
	return []string{p.id, p.device, p.sub, p.slack, f.emails.Index(p.email)}
}

func TestErase(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, f *fixture)
	}{
		{name: "a delete erases every row of the account, build days, devlogs, badges, removed items and submissions among them, and reports each table's count", run: func(t *testing.T, f *fixture) {
			want := world(t, f)
			before := f.dump(t)
			r := report(t, f.ask(t, privacy.ActionDelete, subject([]string{teen.email}, nil, nil), false))
			if !r.Found || r.DryRun || !maps.Equal(r.Deleted, want) {
				t.Errorf("found %v, dry run %v, deleted %v; want found, not dry, deleted %v", r.Found, r.DryRun, r.Deleted, want)
			}
			keptLeaves(t, r)
			for _, table := range []string{"build_days", "build_weeks", "build_goals", "devlogs", "badges", "removed_items", "submissions", "submission_keys", "submission_windows", "submission_flags"} {
				if r.Deleted[table] == 0 {
					t.Errorf("the report deleted no %s", table)
				}
				for _, k := range r.Retained {
					if k.Type == table {
						t.Errorf("the report keeps %s", table)
					}
				}
			}
			after := f.dump(t, "privacy_token_uses")
			if left := mentions(after, marks(f, teen)...); len(left) > 0 {
				t.Errorf("rows that name the teen are left: %v", left)
			}
			if b, a := mentions(before, marks(f, other)...), mentions(after, marks(f, other)...); !reflect.DeepEqual(b, a) {
				t.Errorf("the other account's rows changed:\nbefore %v\nafter  %v", b, a)
			}
			if got := after["day_leaves"]; len(got) != 1 || !strings.HasPrefix(got[0], fmt.Sprint(day(sharedDay))+"|") {
				t.Errorf("the days that keep their leaves are %v, want the shared day's, %d, alone", got, day(sharedDay))
			}
			if !reflect.DeepEqual(before["day_heads"], after["day_heads"]) {
				t.Error("the day heads changed")
			}
		}},
		{name: "a dry run reports the counts the real run deletes and changes no row but its jti's", run: func(t *testing.T, f *fixture) {
			want := world(t, f)
			before := f.dump(t, "privacy_token_uses")
			r := report(t, f.ask(t, privacy.ActionDelete, subject([]string{teen.email}, nil, nil), true))
			if !r.Found || !r.DryRun || !maps.Equal(r.Deleted, want) {
				t.Errorf("found %v, dry run %v, deleted %v; want found, dry, deleted %v", r.Found, r.DryRun, r.Deleted, want)
			}
			keptLeaves(t, r)
			if after := f.dump(t, "privacy_token_uses"); !reflect.DeepEqual(before, after) {
				t.Error("the dry run changed rows")
			}
			if n := len(f.dump(t)["privacy_token_uses"]); n != 1 {
				t.Errorf("privacy_token_uses holds %d rows, want the dry run's jti", n)
			}
		}},
		{name: "a delete takes the account's submissions out of other accounts' repo_overlap flags, deletes a flag left naming none and counts it under submission_flags, and a dry run reports the same and changes nothing (D-sb1-flags-erasure)", run: func(t *testing.T, f *fixture) {
			want := world(t, f)
			now := f.now.UnixMilli()
			// One of the other account's submissions flags the teen's alone,
			// and a second one flags the teen's and a third account's.
			f.exec(t, `INSERT INTO submissions (id, user_id, program, takes_hours, request_id, request_sha256, created_by, device_id, mode, state, scoring_version, params, r, r_sha256, eligibility_to_confirm, created_at_ms, updated_at_ms, frozen_at_ms) VALUES ($1, $2, 'hard-mode', 1, 'req-2', 'sum', 'device', $3, 'window', 'frozen', '0.5.0', 'sha256:params', $4, 'rsum', 0, $5, $5, $5)`,
				"sub2-"+other.id, other.id, other.device, make([]byte, 32), now)
			for _, fl := range []struct{ on, names string }{
				{"sub-" + other.id, `["sub-` + teen.id + `"]`},
				{"sub2-" + other.id, `["sub-u-third","sub-` + teen.id + `"]`},
			} {
				f.exec(t, `INSERT INTO submission_flags (submission_id, user_id, code, detail, raised_at_ms) VALUES ($1, $2, 'repo_overlap', $3, $4)`, fl.on, other.id, `{"submissions":`+fl.names+`}`, now)
			}
			want["submission_flags"]++
			before := f.dump(t, "privacy_token_uses")
			if dry := report(t, f.ask(t, privacy.ActionDelete, subject([]string{teen.email}, nil, nil), true)); !maps.Equal(dry.Deleted, want) {
				t.Errorf("the dry run deleted %v, want %v", dry.Deleted, want)
			}
			if after := f.dump(t, "privacy_token_uses"); !reflect.DeepEqual(before, after) {
				t.Error("the dry run changed rows")
			}
			if r := report(t, f.ask(t, privacy.ActionDelete, subject([]string{teen.email}, nil, nil), false)); !maps.Equal(r.Deleted, want) {
				t.Errorf("deleted %v, want %v", r.Deleted, want)
			}
			var overlaps []string
			for _, l := range f.dump(t)["submission_flags"] {
				if strings.Contains(l, "|repo_overlap|") {
					overlaps = append(overlaps, l)
				}
			}
			if len(overlaps) != 1 || !strings.HasPrefix(overlaps[0], "sub2-"+other.id+"|"+other.id+`|repo_overlap|{"submissions":["sub-u-third"]}|`) {
				t.Errorf("the repo_overlap flags left are %q, want the other account's second, naming the third account's submission alone", overlaps)
			}
			if left := mentions(f.dump(t, "privacy_token_uses"), marks(f, teen)...); len(left) > 0 {
				t.Errorf("rows that name the teen are left: %v", left)
			}
		}},
		{name: "a flag on one of the account's submissions under another account's user_id, which the delete by user_id misses, fails the erasure, which then changes nothing (D-sb1-flags-erasure)", run: func(t *testing.T, f *fixture) {
			world(t, f)
			f.exec(t, `INSERT INTO submission_flags (submission_id, user_id, code, detail, raised_at_ms) VALUES ($1, $2, 'repo_overlap', '{"submissions":[]}', $3)`, "sub-"+teen.id, other.id, f.now.UnixMilli())
			before := f.dump(t, "privacy_token_uses")
			if rec := f.ask(t, privacy.ActionDelete, subject([]string{teen.email}, nil, nil), false); rec.Code != http.StatusInternalServerError {
				t.Errorf("the delete answered %d, want 500: %s", rec.Code, rec.Body.String())
			}
			if after := f.dump(t, "privacy_token_uses"); !reflect.DeepEqual(before, after) {
				t.Error("the failed erasure changed rows")
			}
		}},
		{name: "an export lists what is held, build days, devlogs, badges, removed items and submissions among it, and deletes nothing", run: func(t *testing.T, f *fixture) {
			want := world(t, f)
			before := f.dump(t, "privacy_token_uses")
			r := report(t, f.ask(t, privacy.ActionExport, subject([]string{teen.email}, nil, nil), false))
			if !r.Found || len(r.Deleted) != 0 {
				t.Errorf("found %v, deleted %v; want found and nothing deleted", r.Found, r.Deleted)
			}
			held := make(map[string]int64)
			for _, k := range r.Retained {
				held[k.Type] = k.Count
				if k.Reason != "held by pairtime; export is read-only" {
					t.Errorf("%s is held for %q", k.Type, k.Reason)
				}
			}
			delete(want, privacy.LeafTable)
			want["leaf hashes in day trees"] = 3
			if !maps.Equal(held, want) {
				t.Errorf("the export holds %v, want %v", held, want)
			}
			if after := f.dump(t, "privacy_token_uses"); !reflect.DeepEqual(before, after) {
				t.Error("the export changed rows")
			}
		}},
		{name: "a person pairtime holds nothing of is not found, and nothing changes", run: func(t *testing.T, f *fixture) {
			world(t, f)
			before := f.dump(t, "privacy_token_uses")
			r := report(t, f.ask(t, privacy.ActionDelete, subject([]string{"nobody@example.org"}, "U0NOBODY01", "ident!nobody"), false))
			if r.Found || len(r.Deleted) != 0 || len(r.Retained) != 0 || r.Deleted == nil || r.Retained == nil {
				t.Errorf("report %+v, want not found, with an empty deleted and an empty retained", r)
			}
			if after := f.dump(t, "privacy_token_uses"); !reflect.DeepEqual(before, after) {
				t.Error("rows changed")
			}
		}},
		{name: "an account is found by its Hack Club Auth identity alone, by its Slack id alone, and by an email in capitals", run: func(t *testing.T, f *fixture) {
			world(t, f)
			for _, s := range []map[string]any{subject(nil, nil, teen.sub), subject(nil, teen.slack, nil), subject([]string{"Teen@Example.ORG"}, nil, nil)} {
				if r := report(t, f.ask(t, privacy.ActionExport, s, false)); !r.Found {
					t.Errorf("subject %v isn't found", s)
				}
			}
		}},
		{name: "identifiers that lead to two accounts need a person, and nothing changes", run: func(t *testing.T, f *fixture) {
			world(t, f)
			before := f.dump(t, "privacy_token_uses")
			for _, action := range []string{privacy.ActionDelete, privacy.ActionExport} {
				conflict(t, f.ask(t, action, subject([]string{teen.email}, other.slack, nil), false))
			}
			if after := f.dump(t, "privacy_token_uses"); !reflect.DeepEqual(before, after) {
				t.Error("rows changed")
			}
		}},
		{name: "an identity that isn't the matched account's needs a person", run: func(t *testing.T, f *fixture) {
			world(t, f)
			conflict(t, f.ask(t, privacy.ActionDelete, subject([]string{teen.email}, nil, "ident!someone-else"), false))
		}},
		{name: "an account Hack Club Auth turned ineligible is found by its email alone, and erased", run: func(t *testing.T, f *fixture) {
			ctx := context.Background()
			acc, err := accounts.New(accounts.Options{DB: f.db, Writer: f.w, Emails: f.emails, MayCreate: func(string) bool { return true }, Now: func() time.Time { return f.now }})
			if err != nil {
				t.Fatal(err)
			}
			yes := true
			id := accounts.Identity{Sub: "ident!young1", SlackID: "U0YOUNG001", Email: "young@example.org", Status: accounts.Verified, YSWSEligible: &yes}
			if _, _, err := acc.SignIn(ctx, id, accounts.Login{}); err != nil {
				t.Fatal(err)
			}
			id.Status = accounts.Ineligible
			if _, _, err := acc.SignIn(ctx, id, accounts.Login{}); !errors.Is(err, accounts.ErrIneligible) {
				t.Fatalf("the ineligible sign-in = %v, want ErrIneligible", err)
			}
			r := report(t, f.ask(t, privacy.ActionDelete, subject([]string{"young@example.org"}, nil, nil), false))
			if !r.Found || r.Deleted["users"] != 1 {
				t.Errorf("found %v, deleted %v; want the ineligible account found by its email and its users row deleted", r.Found, r.Deleted)
			}
			if left := mentions(f.dump(t), "ident!young1", f.emails.Index("young@example.org")); len(left) > 0 {
				t.Errorf("rows that name the ineligible account are left: %v", left)
			}
		}},
		{name: "after an erasure, the next request finds nobody", run: func(t *testing.T, f *fixture) {
			world(t, f)
			report(t, f.ask(t, privacy.ActionDelete, subject([]string{teen.email}, nil, nil), false))
			if r := report(t, f.ask(t, privacy.ActionDelete, subject([]string{teen.email}, teen.slack, teen.sub), false)); r.Found || len(r.Deleted) != 0 {
				t.Errorf("report %+v, want not found", r)
			}
		}},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				tt.run(t, newFixture(t, engine))
			})
		}
	}
}

// conflict checks an answer is the 409 for an identity conflict.
func conflict(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	resp := rec.Result()
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict || body["error"] != "manual_review_required" || body["reason"] != "identity_conflict" {
		t.Errorf("answered %d %v, want 409 manual_review_required for identity_conflict", resp.StatusCode, body)
	}
}
