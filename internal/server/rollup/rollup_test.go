// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"context"
	"math"
	"math/rand/v2"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/event"
)

// base is a minute the tests count from: 2026-11-20 15:00 UTC.
var base = time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC).Unix()

// newDB returns a migrated database on engine with an account for each
// id, every one in UTC.
func newDB(t *testing.T, engine store.Engine, accounts ...string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	for _, id := range accounts {
		if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms)
VALUES ($1, $2, 'verified', 0, 0, 0)`, id, "sub-"+id); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// txDoer is a Doer that runs each op in a transaction of its own, as the
// writer does.
type txDoer struct {
	db  *store.DB
	ops *int
}

func (d txDoer) Do(ctx context.Context, _ int, op writer.Op) error {
	if d.ops != nil {
		*d.ops++
	}
	tx, err := d.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := op(ctx, tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// step is one thing a test does to the rollups: apply a bucket, set the
// account's time zone, link device keys to one project as project
// registration will (WP-SV10), or settle as of a time.
type step struct {
	bucket Bucket
	zone   string
	// link lists "device/key" pairs to give one project id.
	link []string
	// settle, when set, runs Settle as of this time.
	settle time.Time
}

// linked is a step that gives the "device/key" pairs one project id, as
// project registration (WP-SV10) will for the keys of one repo.
func linked(pairs ...string) step { return step{link: pairs} }

// settleAt is a step that runs the settle job as of t.
func settleAt(t time.Time) step { return step{settle: t} }

// run does each step in a transaction of its own, as ingest, the dashboard
// and the settle job do, and returns the first error.
func run(t *testing.T, db *store.DB, account string, steps []step) error {
	t.Helper()
	ctx := context.Background()
	for i, s := range steps {
		if !s.settle.IsZero() {
			if _, err := Settle(ctx, txDoer{db: db}, s.settle); err != nil {
				return err
			}
			continue
		}
		tx, err := db.Write.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case s.zone != "":
			err = SetZone(ctx, tx, account, s.zone)
		case s.link != nil:
			for _, pair := range s.link {
				device, key, _ := strings.Cut(pair, "/")
				if _, err = tx.ExecContext(ctx, `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id, last_minute_ms)
VALUES ($1, $2, $3, $4, 0)`, account, device, key, "linked-"+string(rune('a'+i))); err != nil {
					break
				}
			}
		default:
			err = Apply(ctx, tx, nil, s.bucket)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return nil
}

// labels returns a label for each of the account's project ids: the
// "device/key" pairs behind it, joined by "+" in byte order. Project ids
// are random, so the tests name projects this way.
func labels(t *testing.T, db *store.DB, account string) map[string]string {
	t.Helper()
	keys, err := projectPairs(context.Background(), db, account)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(keys))
	for id, pairs := range keys {
		slices.Sort(pairs)
		out[id] = strings.Join(pairs, "+")
	}
	return out
}

// projectPairs returns the "device/key" pairs of each of the account's
// project ids.
func projectPairs(ctx context.Context, db *store.DB, account string) (map[string][]string, error) {
	r, err := db.Read.QueryContext(ctx, `SELECT project_id, device_id, project_key FROM rollup_projects WHERE user_id = $1`, account)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := make(map[string][]string)
	for r.Next() {
		var id, device, key string
		if err := r.Scan(&id, &device, &key); err != nil {
			return nil, err
		}
		out[id] = append(out[id], device+"/"+key)
	}
	return out, r.Err()
}

// rows returns the account's rollups_daily rows that hold any time, with
// each project id replaced by its label, by day, project and kind in byte
// order, whatever collation the engine sorts text by.
func rows(t *testing.T, db *store.DB, account string) []Day {
	t.Helper()
	all, err := Read(context.Background(), db.Read, account, "0000-01-01", "9999-12-31")
	if err != nil {
		t.Fatal(err)
	}
	label := labels(t, db, account)
	var out []Day
	for _, d := range all {
		if d.FinalMS != [3]int64{} || d.PendingMS != [3]int64{} || d.AgentOnlyFinalMS != 0 || d.AgentOnlyPendingMS != 0 {
			l, ok := label[d.ProjectID]
			if !ok {
				t.Errorf("rollups_daily has project id %q, which rollup_projects doesn't map", d.ProjectID)
			}
			d.ProjectID = l
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b Day) int {
		if c := strings.Compare(a.Day, b.Day); c != 0 {
			return c
		}
		if c := strings.Compare(a.ProjectID, b.ProjectID); c != 0 {
			return c
		}
		return strings.Compare(a.Kind, b.Kind)
	})
	return out
}

// concurrent counts the account's minutes, over all time, in which two of
// its devices credited the same slot.
func concurrent(t *testing.T, db *store.DB, account string) int {
	t.Helper()
	n, err := ConcurrentMinutes(context.Background(), db.Read, account, 0, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// bk is a bucket of account "u1", with agentOnly as its agent_only.
func bk(device string, minute int64, rev int, state string, agentOnly map[string]float64, slots ...Slot) step {
	b := Bucket{Account: "u1", Device: device, Minute: minute, Rev: rev, State: state, Slots: slots, AgentOnly: agentOnly}
	for _, s := range slots {
		b.CreditedS += s.CreditedS
	}
	return step{bucket: b}
}

func sl(project string, cr float64, kind, level string) Slot {
	return Slot{Project: project, CreditedS: cr, Kind: kind, Level: level}
}

// none is a slot with nothing credited and no project.
var none = Slot{}

// ao is an agent_only of one project.
func ao(project string, s float64) map[string]float64 { return map[string]float64{project: s} }

// final and pending are rollups_daily rows of credited time at level A
// (column 0), B (1) or C (2), and agent rows of the agent's time on its
// own. project is a project's label: its "device/key" pairs.
func final(project, day, kind string, level int, ms int64) Day {
	d := Day{ProjectID: project, Day: day, Kind: kind}
	d.FinalMS[level] = ms
	return d
}

func pending(project, day, kind string, level int, ms int64) Day {
	d := Day{ProjectID: project, Day: day, Kind: kind}
	d.PendingMS[level] = ms
	return d
}

func agent(project, day string, finalMS, pendingMS int64) Day {
	return Day{ProjectID: project, Day: day, AgentOnlyFinalMS: finalMS, AgentOnlyPendingMS: pendingMS}
}

func TestApply(t *testing.T) {
	const today = "2026-11-20"
	m0, m1 := base, base+60
	tests := []struct {
		name  string
		steps []step
		want  []Day
		// concurrent is how many minutes ConcurrentMinutes finds, as a
		// submission over all of them would.
		concurrent int
	}{
		{
			name:  "a first revision adds each credited slot to its project, local day, activity kind and level, and the agent's time on its own beside them",
			steps: []step{bk("d1", m0, 0, "final", ao("p1", 20), sl("p1", 15, "prompting", "A"), sl("p1", 15, "prompting", "A"), sl("p1", 10, "reading_output", "B"), none)},
			want:  []Day{agent("d1/p1", today, 20000, 0), final("d1/p1", today, "prompting", 0, 30000), final("d1/p1", today, "reading_output", 1, 10000)},
		},
		{
			name:  "half-weight seconds count to the millisecond",
			steps: []step{bk("d1", m0, 0, "provisional", nil, sl("p1", 7.5, "testing", "A"))},
			want:  []Day{final("d1/p1", today, "testing", 0, 7500)},
		},
		{
			name: "pending seconds stay apart from final ones, and the next revision moves them to final",
			steps: []step{
				bk("d1", m0, 0, "pending", ao("p1", 20), sl("p1", 10, "prompting", "A")),
				bk("d1", m1, 0, "pending", nil, sl("p1", 5, "prompting", "A")),
				bk("d1", m0, 1, "final", ao("p1", 20), sl("p1", 15, "prompting", "A")),
			},
			want: []Day{agent("d1/p1", today, 20000, 0), {ProjectID: "d1/p1", Day: today, Kind: "prompting", FinalMS: [3]int64{15000}, PendingMS: [3]int64{5000}}},
		},
		{
			name: "a revision no newer than the one applied changes nothing",
			steps: []step{
				bk("d1", m0, 1, "final", nil, sl("p1", 15, "prompting", "A")),
				bk("d1", m0, 0, "pending", nil, sl("p1", 5, "prompting", "A")),
				bk("d1", m0, 1, "final", nil, sl("p1", 10, "prompting", "A")),
			},
			want: []Day{final("d1/p1", today, "prompting", 0, 15000)},
		},
		{
			name: "two devices crediting one slot keep the one with more credited seconds, and a submission over the minute finds it concurrent (SM 3.4)",
			steps: []step{
				bk("d1", m0, 0, "final", nil, sl("p1", 10, "prompting", "A")),
				bk("d2", m0, 0, "final", nil, sl("p2", 15, "hand_edit", "B")),
			},
			want:       []Day{final("d2/p2", today, "hand_edit", 1, 15000)},
			concurrent: 1,
		},
		{
			name: "on a tie in credited seconds the keyboard-backed slot, level A, wins",
			steps: []step{
				bk("d1", m0, 0, "final", nil, sl("p1", 15, "prompting", "B")),
				bk("d2", m0, 0, "final", nil, sl("p2", 15, "prompting", "A")),
			},
			want:       []Day{final("d2/p2", today, "prompting", 0, 15000)},
			concurrent: 1,
		},
		{
			name: "on a full tie the lower device_id wins, whichever arrived first",
			steps: []step{
				bk("d2", m0, 0, "final", nil, sl("p2", 15, "prompting", "A")),
				bk("d1", m0, 0, "final", nil, sl("p1", 15, "prompting", "A")),
			},
			want:       []Day{final("d1/p1", today, "prompting", 0, 15000)},
			concurrent: 1,
		},
		{
			name: "when the winner's next revision credits nothing there, the other device's slot counts again and the minute isn't concurrent",
			steps: []step{
				bk("d1", m0, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
				bk("d2", m0, 0, "final", nil, sl("p2", 10, "reviewing", "A")),
				bk("d1", m0, 1, "final", nil, none),
			},
			want: []Day{final("d2/p2", today, "reviewing", 0, 10000)},
		},
		{
			name: "slots merge one by one, so two devices can each count in a different slot of one minute",
			steps: []step{
				bk("d1", m0, 0, "final", nil, sl("p1", 15, "prompting", "A"), none, none, none),
				bk("d2", m0, 0, "final", nil, none, sl("p2", 15, "testing", "B"), sl("p2", 15, "testing", "B"), none),
			},
			want: []Day{final("d1/p1", today, "prompting", 0, 15000), final("d2/p2", today, "testing", 1, 30000)},
		},
		{
			name: "each device's key is a project of its own, even one that matches another device's key, since keys are keyed per device (D-account-project-id)",
			steps: []step{
				bk("d1", m0, 0, "final", nil, sl("p1", 15, "prompting", "A"), none, none, none),
				bk("d2", m0, 0, "final", nil, none, sl("p1", 15, "prompting", "A"), none, none),
			},
			want: []Day{final("d1/p1", today, "prompting", 0, 15000), final("d2/p1", today, "prompting", 0, 15000)},
		},
		{
			name: "two devices' keys of one repo that registration links count as one project",
			steps: []step{
				linked("d1/g_laptop", "d2/g_desktop"),
				bk("d1", m0, 0, "final", nil, sl("g_laptop", 15, "prompting", "A"), none, none, none),
				bk("d2", m0, 0, "final", nil, none, sl("g_desktop", 15, "prompting", "A"), none, none),
			},
			want: []Day{final("d1/g_laptop+d2/g_desktop", today, "prompting", 0, 30000)},
		},
		{
			name: "the agent's time on its own counts once a minute per project across devices, from the device that reports the most there",
			steps: []step{
				linked("d1/g_laptop", "d2/g_desktop"),
				bk("d1", m0, 0, "final", ao("g_laptop", 40)),
				bk("d2", m0, 0, "pending", map[string]float64{"g_desktop": 20, "p2": 10}),
			},
			want: []Day{agent("d1/g_laptop+d2/g_desktop", today, 40000, 0), agent("d2/p2", today, 0, 10000)},
		},
		{
			name: "the agent's time on its own goes to each project agent_only names, several in one minute (D-bucket-agent-only)",
			steps: []step{
				bk("d1", m0, 0, "final", map[string]float64{"p1": 45, "p2": 15}, sl("p3", 15, "reviewing", "A")),
			},
			want: []Day{agent("d1/p1", today, 45000, 0), agent("d1/p2", today, 15000, 0), final("d1/p3", today, "reviewing", 0, 15000)},
		},
		{
			name: "another device's credit in a project covers that part of the minute, so the agent has less of it on its own there (SM 2.6)",
			steps: []step{
				linked("d1/g_laptop", "d2/g_desktop"),
				bk("d1", m0, 0, "final", ao("g_laptop", 60)),
				bk("d2", m0, 0, "final", nil, sl("g_desktop", 15, "prompting", "A"), sl("g_desktop", 15, "prompting", "A")),
			},
			want: []Day{agent("d1/g_laptop+d2/g_desktop", today, 30000, 0), final("d1/g_laptop+d2/g_desktop", today, "prompting", 0, 30000)},
		},
		{
			name: "until registration links them, a repo's keys on two computers are two projects, so one device's credit doesn't cover the other's agent time",
			steps: []step{
				bk("d1", m0, 0, "final", ao("g_laptop", 60)),
				bk("d2", m0, 0, "final", nil, sl("g_desktop", 15, "prompting", "A"), sl("g_desktop", 15, "prompting", "A")),
			},
			want: []Day{agent("d1/g_laptop", today, 60000, 0), final("d2/g_desktop", today, "prompting", 0, 30000)},
		},
		{
			name: "a device's two keys that registration links to one project give it the larger of their agent times, never the sum",
			steps: []step{
				linked("d1/g_clone1", "d1/g_clone2"),
				bk("d1", m0, 0, "final", map[string]float64{"g_clone1": 40, "g_clone2": 30}),
			},
			want: []Day{agent("d1/g_clone1+d1/g_clone2", today, 40000, 0)},
		},
		{
			name: "a minute counts toward the account's local day in its time zone",
			steps: []step{
				{zone: "Asia/Tokyo"},
				// 15:00 UTC on the 20th is midnight on the 21st in Tokyo.
				bk("d1", base, 0, "final", nil, sl("p1", 15, "prompting", "A")),
			},
			want: []Day{final("d1/p1", "2026-11-21", "prompting", 0, 15000)},
		},
		{
			// Chile starts daylight saving time at midnight: 2027-09-05
			// has no 00:00, so a day made from time.Date(..., 0, 0, ...)
			// would start at 23:00 of the day before.
			name: "in America/Santiago the last hour of 2027-09-04 counts on the 4th and the last hour of 2027-09-05 on the 5th, though the 5th has no midnight",
			steps: []step{
				{zone: "America/Santiago"},
				bk("d1", 1820115000, 0, "final", nil, sl("p1", 15, "prompting", "A")), // 2027-09-04 23:30 -04
				bk("d1", 1820116740, 0, "final", nil, sl("p1", 10, "prompting", "A")), // 2027-09-04 23:59 -04
				bk("d1", 1820116800, 0, "final", nil, sl("p1", 5, "prompting", "A")),  // 2027-09-05 01:00 -03
				bk("d1", 1820197800, 0, "final", nil, sl("p1", 1, "prompting", "A")),  // 2027-09-05 23:30 -03
				bk("d1", 1820199600, 0, "final", nil, sl("p1", 2, "prompting", "A")),  // 2027-09-06 00:00 -03
			},
			want: []Day{
				final("d1/p1", "2027-09-04", "prompting", 0, 25000),
				final("d1/p1", "2027-09-05", "prompting", 0, 6000),
				final("d1/p1", "2027-09-06", "prompting", 0, 2000),
			},
		},
		{
			name: "after the time zone changes, a minute's next revision moves its time to its day in the new zone",
			steps: []step{
				bk("d1", base+8*3600+30*60, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
				{zone: "Asia/Tokyo"},
				bk("d1", base+8*3600+30*60, 1, "final", nil, sl("p1", 15, "prompting", "A")),
			},
			want: []Day{final("d1/p1", "2026-11-21", "prompting", 0, 15000)},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := newDB(t, engine, "u1")
				if err := run(t, db, "u1", tt.steps); err != nil {
					t.Fatal(err)
				}
				if got := rows(t, db, "u1"); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("rollups_daily:\n got %+v\nwant %+v", got, tt.want)
				}
				if got := concurrent(t, db, "u1"); got != tt.concurrent {
					t.Errorf("concurrent minutes %d, want %d", got, tt.concurrent)
				}
			})
		}
	}
}

// TestApplyWritesOnlyRollups is D-no-ingest-flags' check: Apply writes the
// rollups' own tables and nothing else, so no fraud flag is stored for a
// minute two devices both credit while nobody has submitted it.
func TestApplyWritesOnlyRollups(t *testing.T) {
	t.Run("two devices crediting one slot leave rows in rollup_minutes, rollup_projects and rollups_daily only", func(t *testing.T) {
		db := newDB(t, store.SQLite, "u1")
		if err := run(t, db, "u1", []step{
			bk("d1", base, 0, "final", nil, sl("p1", 10, "prompting", "A")),
			bk("d2", base, 0, "final", nil, sl("p2", 15, "hand_edit", "B")),
		}); err != nil {
			t.Fatal(err)
		}
		r, err := db.Read.QueryContext(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		var tables []string
		for r.Next() {
			var n string
			if err := r.Scan(&n); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, n)
		}
		r.Close()
		var written []string
		for _, n := range tables {
			var rows int
			if err := db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+n).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			// newDB's account and the migrations' own records were there
			// before.
			if rows > 0 && n != "users" && n != "schema_migrations" {
				written = append(written, n)
			}
		}
		if want := []string{"rollup_minutes", "rollup_projects", "rollups_daily"}; !reflect.DeepEqual(written, want) {
			t.Errorf("Apply wrote rows to %v, want only %v", written, want)
		}
	})
}

func TestApplyRefuses(t *testing.T) {
	ok := Bucket{Account: "u1", Device: "d1", Minute: base, State: "final", Slots: []Slot{sl("p1", 15, "prompting", "A")}, CreditedS: 15}
	with := func(f func(*Bucket)) Bucket {
		b := ok
		b.Slots = slices.Clone(ok.Slots)
		f(&b)
		return b
	}
	tests := []struct {
		name string
		prev *Bucket
		next Bucket
		want string
	}{
		{name: "a level that isn't A, B or C", next: with(func(b *Bucket) { b.Slots[0].Level = "D" }), want: "evidence level"},
		{name: "credit that isn't a step of 0.5 seconds", next: with(func(b *Bucket) { b.Slots[0].CreditedS = 7.3 }), want: "steps of 0.5"},
		{name: "more than 15 seconds in a slot", next: with(func(b *Bucket) { b.Slots[0].CreditedS = 15.5 }), want: "steps of 0.5"},
		{name: "credit with no project", next: with(func(b *Bucket) { b.Slots[0].Project = "" }), want: "names no project"},
		{name: "credit with no activity kind", next: with(func(b *Bucket) { b.Slots[0].Kind = "" }), want: "activity kind"},
		{name: "a state pkg/event doesn't have", next: with(func(b *Bucket) { b.State = "maybe" }), want: "state"},
		{name: "five slots", next: with(func(b *Bucket) { b.Slots = append(b.Slots, none, none, none, none) }), want: "5 slots"},
		{name: "a minute that doesn't start a UTC minute", next: with(func(b *Bucket) { b.Minute += 30 }), want: "UTC minute"},
		{name: "more than a minute of agent time", next: with(func(b *Bucket) { b.AgentRunningS = 61 }), want: "agent_running_s"},
		{name: "agent_only for no project", next: with(func(b *Bucket) { b.AgentOnly = ao("", 10) }), want: "agent_only gives seconds to no project"},
		{name: "agent_only past a minute", next: with(func(b *Bucket) { b.AgentOnly = ao("p1", 61) }), want: "agent_only gives p1 61 seconds"},
		{name: "agent_only that isn't a step of 0.5 seconds", next: with(func(b *Bucket) { b.AgentOnly = ao("p1", 7.3) }), want: "agent_only gives p1 7.3 seconds"},
		{name: "an account that doesn't exist", next: with(func(b *Bucket) { b.Account = "u-nobody" }), want: "no such account"},
		{name: "a prev from another device", prev: &Bucket{Account: "u1", Device: "d2", Minute: base}, next: ok, want: "prev is"},
	}
	for _, tt := range tests {
		t.Run(tt.name+" is refused", func(t *testing.T) {
			db := newDB(t, store.SQLite, "u1")
			ctx := context.Background()
			tx, err := db.Write.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := Apply(ctx, tx, tt.prev, tt.next); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Apply = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// TestReplayConverges is WP-SV7's first check: revisions applied in any
// order leave the same rollups and concurrent minutes, because only each
// device's latest revision of each minute counts.
func TestReplayConverges(t *testing.T) {
	// The revisions: three devices, six minutes, three revisions each,
	// with credit, levels, states and agent time drawn from a fixed seed.
	// Registration links the devices' keys "p1" as one project, so the
	// agent's time merges across devices there, and p2 and p3 stay a
	// project per device.
	gen := rand.New(rand.NewPCG(7, 11))
	projects := []string{"p1", "p2", "p3"}
	kinds := []string{"prompting", "reading_output", "testing"}
	var revs []step
	for m := range int64(6) {
		for _, dev := range []string{"d1", "d2", "d3"} {
			for rev := range 3 {
				var slots []Slot
				for range slotsPerMinute {
					cr := float64(gen.IntN(31)) / 2
					if gen.IntN(3) == 0 {
						cr = 0
					}
					s := Slot{Project: projects[gen.IntN(3)]}
					if cr > 0 {
						s = sl(s.Project, cr, kinds[gen.IntN(3)], Levels[gen.IntN(3)])
					}
					slots = append(slots, s)
				}
				state := "pending"
				if rev == 2 {
					state = "final"
				}
				revs = append(revs, bk(dev, base+3540+m*60, rev, state, ao(projects[gen.IntN(3)], float64(gen.IntN(121))/2), slots...))
			}
		}
	}
	// A seventh minute in which two devices report agent time in the
	// linked project, so the replays always merge some.
	revs = append(revs,
		bk("d1", base+3540+6*60, 0, "final", ao("p1", 30)),
		bk("d2", base+3540+6*60, 0, "final", ao("p1", 20)))
	link := linked("d1/p1", "d2/p1", "d3/p1")
	type result struct {
		rows       []Day
		concurrent int
	}
	replay := func(t *testing.T, engine store.Engine, order []step) result {
		db := newDB(t, engine, "u1")
		if err := run(t, db, "u1", append([]step{link}, order...)); err != nil {
			t.Fatal(err)
		}
		return result{rows: rows(t, db, "u1"), concurrent: concurrent(t, db, "u1")}
	}
	for _, engine := range storetest.Engines() {
		want := replay(t, engine, revs)
		if len(want.rows) == 0 || want.concurrent == 0 || !slices.ContainsFunc(want.rows, func(d Day) bool { return d.ProjectID == "d1/p1+d2/p1+d3/p1" && d.Kind == "" }) {
			t.Fatalf("the revisions in order left rows %v and %d concurrent minutes; the test needs both, and agent time in the linked project", want.rows, want.concurrent)
		}
		for seed := range uint64(5) {
			t.Run(string(engine)+"/shuffle "+string(rune('a'+seed))+" leaves the rollups and concurrent minutes of applying the revisions in order", func(t *testing.T) {
				order := slices.Clone(revs)
				r := rand.New(rand.NewPCG(seed, 99))
				r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
				if got := replay(t, engine, order); !reflect.DeepEqual(got, want) {
					t.Errorf("shuffled:\n got %+v\nwant %+v", got, want)
				}
			})
		}
	}
}

func TestConcurrentMinutes(t *testing.T) {
	m := func(i int64) int64 { return base + 60*i }
	steps := []step{
		// m0 and m3: two devices credit slot 0. m1: they credit different
		// slots. m2: one device alone.
		bk("d1", m(0), 0, "final", nil, sl("p1", 15, "prompting", "A")),
		bk("d2", m(0), 0, "final", nil, sl("p2", 5, "prompting", "B")),
		bk("d1", m(1), 0, "final", nil, sl("p1", 15, "prompting", "A")),
		bk("d2", m(1), 0, "final", nil, none, sl("p2", 15, "prompting", "A")),
		bk("d1", m(2), 0, "final", nil, sl("p1", 15, "prompting", "A")),
		bk("d1", m(3), 0, "pending", nil, sl("p1", 15, "prompting", "A")),
		bk("d2", m(3), 0, "pending", nil, sl("p2", 15, "prompting", "A")),
	}
	tests := []struct {
		name     string
		from, to int64 // minutes
		want     int
	}{
		{name: "the window of all four minutes has the two in which two devices credited one slot", from: 0, to: 4, want: 2},
		{name: "two devices crediting different slots of a minute aren't concurrent", from: 1, to: 2, want: 0},
		{name: "a device alone isn't concurrent", from: 2, to: 3, want: 0},
		{name: "a window ends before its last minute", from: 0, to: 3, want: 1},
		{name: "pending minutes count as well as final ones", from: 3, to: 4, want: 1},
	}
	for _, engine := range storetest.Engines() {
		db := newDB(t, engine, "u1", "u2")
		if err := run(t, db, "u1", steps); err != nil {
			t.Fatal(err)
		}
		// Another account's devices, crediting the same slots, are its own.
		other := bk("d3", m(1), 0, "final", nil, sl("p1", 15, "prompting", "A"))
		other.bucket.Account = "u2"
		if err := run(t, db, "u2", []step{other}); err != nil {
			t.Fatal(err)
		}
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				got, err := ConcurrentMinutes(context.Background(), db.Read, "u1", m(tt.from)*1000, m(tt.to)*1000)
				if err != nil {
					t.Fatal(err)
				}
				if got != tt.want {
					t.Errorf("ConcurrentMinutes = %d, want %d", got, tt.want)
				}
			})
		}
	}
}

// TestSettle is D-server-settles-pending's check, on a fake clock: the
// server settles a minute its device left pending once PENDING_WINDOW_S
// has passed since the minute ended.
func TestSettle(t *testing.T) {
	const today = "2026-11-20"
	m0 := base
	// ended is when m0 ends; window is when its pending window closes.
	ended := time.Unix(m0+60, 0)
	window := ended.Add(PendingWindow())
	tests := []struct {
		name  string
		steps []step
		want  []Day
		// state is the device rows' states for m0, by device.
		state map[string]string
	}{
		{
			name: "a minute still pending when its pending window closes stays pending",
			steps: []step{
				bk("d1", m0, 0, "pending", ao("p1", 30), sl("p1", 15, "prompting", "A")),
				settleAt(window),
			},
			want:  []Day{agent("d1/p1", today, 0, 30000), pending("d1/p1", today, "prompting", 0, 15000)},
			state: map[string]string{"d1": "pending"},
		},
		{
			name: "a millisecond later the server settles it: its credit and its agent time move from pending to final",
			steps: []step{
				bk("d1", m0, 0, "pending", ao("p1", 30), sl("p1", 15, "prompting", "A"), sl("p1", 10, "testing", "B")),
				settleAt(window.Add(time.Millisecond)),
			},
			want:  []Day{agent("d1/p1", today, 30000, 0), final("d1/p1", today, "prompting", 0, 15000), final("d1/p1", today, "testing", 1, 10000)},
			state: map[string]string{"d1": "final"},
		},
		{
			name: "a later revision from the device still replaces a settled minute",
			steps: []step{
				bk("d1", m0, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
				settleAt(window.Add(time.Hour)),
				bk("d1", m0, 1, "final", nil, sl("p1", 5, "reviewing", "A")),
			},
			want:  []Day{final("d1/p1", today, "reviewing", 0, 5000)},
			state: map[string]string{"d1": "final"},
		},
		{
			name: "the pending revision arriving again after the server settled it changes nothing",
			steps: []step{
				bk("d1", m0, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
				settleAt(window.Add(time.Hour)),
				bk("d1", m0, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
			},
			want:  []Day{final("d1/p1", today, "prompting", 0, 15000)},
			state: map[string]string{"d1": "final"},
		},
		{
			name: "a newer revision a device sends as pending after the window settles again on the next run",
			steps: []step{
				bk("d1", m0, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
				settleAt(window.Add(time.Hour)),
				bk("d1", m0, 1, "pending", nil, sl("p1", 10, "prompting", "A")),
				settleAt(window.Add(time.Hour + time.Minute)),
			},
			want:  []Day{final("d1/p1", today, "prompting", 0, 10000)},
			state: map[string]string{"d1": "final"},
		},
		{
			name: "settling one device's pending minute keeps the merge: the slot it lost to the other device stays lost",
			steps: []step{
				bk("d1", m0, 0, "pending", nil, sl("p1", 10, "prompting", "A"), sl("p1", 15, "prompting", "A")),
				bk("d2", m0, 0, "final", nil, sl("p2", 15, "reviewing", "B")),
				settleAt(window.Add(time.Millisecond)),
			},
			want:  []Day{final("d1/p1", today, "prompting", 0, 15000), final("d2/p2", today, "reviewing", 1, 15000)},
			state: map[string]string{"d1": "final", "d2": "final"},
		},
		{
			name: "a settled minute stays on the local day it counted toward, though the zone changed since",
			steps: []step{
				bk("d1", m0, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
				{zone: "Asia/Tokyo"},
				settleAt(window.Add(time.Millisecond)),
			},
			want:  []Day{final("d1/p1", today, "prompting", 0, 15000)},
			state: map[string]string{"d1": "final"},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := newDB(t, engine, "u1")
				if err := run(t, db, "u1", tt.steps); err != nil {
					t.Fatal(err)
				}
				if got := rows(t, db, "u1"); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("rollups_daily:\n got %+v\nwant %+v", got, tt.want)
				}
				devs, err := loadMinute(context.Background(), db.Read, "u1", m0*1000)
				if err != nil {
					t.Fatal(err)
				}
				state := make(map[string]string)
				for _, d := range devs {
					state[d.id] = map[bool]string{true: "pending", false: "final"}[d.pending]
				}
				if !reflect.DeepEqual(state, tt.state) {
					t.Errorf("rollup_minutes states %v, want %v", state, tt.state)
				}
			})
		}
	}
}

func TestSettleBatches(t *testing.T) {
	// More minutes are due than one op takes, across two accounts.
	var steps []step
	for i := range int64(settleBatch + 30) {
		steps = append(steps, bk("d1", base+60*i, 0, "pending", nil, sl("p1", 15, "prompting", "A")))
	}
	db := newDB(t, store.SQLite, "u1", "u2")
	if err := run(t, db, "u1", steps); err != nil {
		t.Fatal(err)
	}
	other := bk("d9", base, 0, "pending", nil, sl("p1", 15, "prompting", "A"))
	other.bucket.Account = "u2"
	if err := run(t, db, "u2", []step{other}); err != nil {
		t.Fatal(err)
	}
	ops := 0
	last := time.Unix(base+60*(settleBatch+30), 0)
	n, err := Settle(context.Background(), txDoer{db: db, ops: &ops}, last.Add(PendingWindow()+time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Settle settles every minute that is due, in ops of at most settleBatch minutes", func(t *testing.T) {
		if want := settleBatch + 31; n != want || ops != 2 {
			t.Errorf("settled %d minutes in %d ops, want %d in 2", n, ops, want)
		}
	})
	t.Run("nothing stays pending, in either account", func(t *testing.T) {
		var left int
		if err := db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM rollup_minutes WHERE state = 'pending'`).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Errorf("%d rows still pending", left)
		}
	})
}

func TestRunSettle(t *testing.T) {
	t.Run("the settle job settles at once by the clock it is given, and stops when its context ends", func(t *testing.T) {
		db := newDB(t, store.SQLite, "u1")
		if err := run(t, db, "u1", []step{bk("d1", base, 0, "pending", nil, sl("p1", 15, "prompting", "A"))}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clock := func() time.Time { return time.Unix(base+60, 0).Add(PendingWindow() + time.Second) }
		var reports []int
		done := make(chan struct{})
		go func() {
			defer close(done)
			RunSettle(ctx, txDoer{db: db}, time.Hour, clock, func(n int, err error) {
				if err != nil {
					t.Error(err)
				}
				reports = append(reports, n)
				cancel()
			})
		}()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("RunSettle didn't stop after its context ended")
		}
		if !reflect.DeepEqual(reports, []int{1}) {
			t.Errorf("reports %v, want one run that settled 1 minute", reports)
		}
		if got := rows(t, db, "u1"); !reflect.DeepEqual(got, []Day{final("d1/p1", "2026-11-20", "prompting", 0, 15000)}) {
			t.Errorf("rollups_daily %+v, want the minute final", got)
		}
	})
}

func TestPendingWindow(t *testing.T) {
	t.Run("the pending window is PENDING_WINDOW_S, 900 seconds (DESIGN 3.7)", func(t *testing.T) {
		if got := PendingWindow(); got != 900*time.Second {
			t.Errorf("PendingWindow = %v, want 15m", got)
		}
	})
}

func TestProjects(t *testing.T) {
	uuidv7 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	db := newDB(t, store.SQLite, "u1")
	if err := run(t, db, "u1", []step{
		bk("d2", base+60, 0, "final", ao("g_b", 10), sl("g_a", 15, "prompting", "A")),
		bk("d1", base, 0, "final", nil, sl("g_a", 15, "prompting", "A")),
		bk("d2", base, 0, "final", nil, sl("g_a", 15, "prompting", "A")),
		linked("d1/g_z", "d3/g_y"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := Projects(context.Background(), db.Read, "u1")
	if err != nil {
		t.Fatal(err)
	}
	byKeys := make(map[string]string)
	for id, keys := range got {
		byKeys[strings.Join(keys, "+")] = id
	}
	t.Run("each device's key has an id of its own, a UUIDv7, though two devices name the same key", func(t *testing.T) {
		if len(got) != 4 {
			t.Fatalf("Projects = %v, want 4 projects", got)
		}
		for id, keys := range got {
			if len(keys) == 1 && !uuidv7.MatchString(id) {
				t.Errorf("project %s of %v isn't a UUIDv7", id, keys)
			}
		}
	})
	t.Run("a linked project lists its keys in device order", func(t *testing.T) {
		if _, ok := byKeys["g_z+g_y"]; !ok {
			t.Errorf("Projects = %v, want a project with keys g_z, g_y", got)
		}
	})
	t.Run("a key keeps its id and remembers the latest minute that named it", func(t *testing.T) {
		var n int
		var last int64
		if err := db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*), MAX(last_minute_ms) FROM rollup_projects WHERE device_id = 'd2' AND project_key = 'g_a'`).Scan(&n, &last); err != nil {
			t.Fatal(err)
		}
		if n != 1 || last != (base+60)*1000 {
			t.Errorf("%d rows for d2's g_a, the latest at %d, want 1 at %d", n, last, (base+60)*1000)
		}
	})
}

func TestFromEvent(t *testing.T) {
	m := event.MinuteBucket{
		Minute: time.Unix(base, 0).UTC(),
		Device: "d1",
		Slots: []event.BucketSlot{
			{Project: "p1", Credited: 7.5, Why: "P", Kind: "prompting", Ev: "A", Lbl: []event.Label{"via_track"}},
			{},
		},
		CreditedS:     7.5,
		Uncredited:    map[event.Reason]float64{"X.absent": 30},
		AgentRunningS: 12,
		State:         "pending",
		Rev:           3,
	}
	want := Bucket{
		Account: "u1", Device: "d1", Minute: base, Rev: 3, State: "pending",
		Slots:     []Slot{{Project: "p1", CreditedS: 7.5, Why: "P", Kind: "prompting", Level: "A", Labels: []string{"via_track"}}, {}},
		CreditedS: 7.5, AgentRunningS: 12, Uncredited: map[string]float64{"X.absent": 30},
	}
	tests := []struct {
		name      string
		agentOnly map[string]float64
		want      map[string]float64
	}{
		{name: "a pkg/event minute bucket keeps every field it carries, as a Bucket of the account's"},
		{name: "the agent_only ingest holds beside the bucket becomes the Bucket's (D-bucket-agent-only)", agentOnly: map[string]float64{"p1": 4.5, "p2": 6}, want: map[string]float64{"p1": 4.5, "p2": 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := want
			w.AgentOnly = tt.want
			if got := FromEvent("u1", m, tt.agentOnly); !reflect.DeepEqual(got, w) {
				t.Errorf("FromEvent:\n got %+v\nwant %+v", got, w)
			}
		})
	}
}

func TestLocalDay(t *testing.T) {
	santiago, err := LoadZone("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		minute int64
		zone   *time.Location
		want   string
	}{
		{name: "a UTC minute is on its UTC date", minute: base, zone: time.UTC, want: "2026-11-20"},
		{name: "a minute before Santiago's skipped midnight is on the 4th", minute: 1820116740, zone: santiago, want: "2027-09-04"},
		{name: "the first minute after Santiago's skipped midnight is on the 5th", minute: 1820116800, zone: santiago, want: "2027-09-05"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocalDay(tt.minute, tt.zone); got != tt.want {
				t.Errorf("LocalDay = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLoadZone(t *testing.T) {
	tests := []struct {
		name, zone string
		ok         bool
	}{
		{name: "an IANA name loads", zone: "Europe/London", ok: true},
		{name: "a three-part name loads", zone: "America/Argentina/Buenos_Aires", ok: true},
		{name: "UTC loads", zone: "UTC", ok: true},
		{name: "Local, the server's own zone, is refused", zone: "Local"},
		{name: "an empty name is refused", zone: ""},
		{name: "a name the zone database doesn't have is refused", zone: "Mars/Olympus_Mons"},
		{name: "a path is refused", zone: "../../etc/passwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadZone(tt.zone); (err == nil) != tt.ok {
				t.Errorf("LoadZone(%q) = %v, want ok %v", tt.zone, err, tt.ok)
			}
		})
	}
}
