// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"context"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
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

// step is one thing a test does to the rollups: apply a bucket, or set
// the account's time zone.
type step struct {
	bucket Bucket
	zone   string
}

// run does each step in a transaction of its own, as ingest and the
// dashboard do, and returns Apply's first error.
func run(t *testing.T, db *store.DB, account string, steps []step) error {
	t.Helper()
	ctx := context.Background()
	for _, s := range steps {
		tx, err := db.Write.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if s.zone != "" {
			err = SetZone(ctx, tx, account, s.zone)
		} else {
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

// rows returns the account's rollups_daily rows that hold any time, by
// day, project and kind in byte order, whatever collation the engine
// sorts text by.
func rows(t *testing.T, db *store.DB, account string) []Day {
	t.Helper()
	all, err := Read(context.Background(), db.Read, account, "0000-01-01", "9999-12-31")
	if err != nil {
		t.Fatal(err)
	}
	var out []Day
	for _, d := range all {
		if d.FinalMS != [3]int64{} || d.PendingMS != [3]int64{} || d.AgentOnlyFinalMS != 0 || d.AgentOnlyPendingMS != 0 {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b Day) int {
		if c := strings.Compare(a.Day, b.Day); c != 0 {
			return c
		}
		if c := strings.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		return strings.Compare(a.Kind, b.Kind)
	})
	return out
}

// flagged returns the minutes, in Unix seconds, flagged concurrent_devices.
func flagged(t *testing.T, db *store.DB, account string) []int64 {
	t.Helper()
	r, err := db.Read.QueryContext(context.Background(), `SELECT minute_ms FROM rollup_flags WHERE user_id = $1 AND flag = $2 ORDER BY minute_ms`, account, FlagConcurrentDevices)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out []int64
	for r.Next() {
		var ms int64
		if err := r.Scan(&ms); err != nil {
			t.Fatal(err)
		}
		out = append(out, ms/1000)
	}
	return out
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
// own.
func final(project, day, kind string, level int, ms int64) Day {
	d := Day{Project: project, Day: day, Kind: kind}
	d.FinalMS[level] = ms
	return d
}

func pending(project, day, kind string, level int, ms int64) Day {
	d := Day{Project: project, Day: day, Kind: kind}
	d.PendingMS[level] = ms
	return d
}

func agent(project, day string, finalMS, pendingMS int64) Day {
	return Day{Project: project, Day: day, AgentOnlyFinalMS: finalMS, AgentOnlyPendingMS: pendingMS}
}

func TestApply(t *testing.T) {
	const today = "2026-11-20"
	m0, m1 := base, base+60
	tests := []struct {
		name    string
		steps   []step
		want    []Day
		flagged []int64
	}{
		{
			name:  "a first revision adds each credited slot to its project, local day, activity kind and level, and the agent's time on its own beside them",
			steps: []step{bk("d1", m0, 0, "final", ao("p1", 20), sl("p1", 15, "prompting", "A"), sl("p1", 15, "prompting", "A"), sl("p1", 10, "reading_output", "B"), none)},
			want:  []Day{agent("p1", today, 20000, 0), final("p1", today, "prompting", 0, 30000), final("p1", today, "reading_output", 1, 10000)},
		},
		{
			name:  "half-weight seconds count to the millisecond",
			steps: []step{bk("d1", m0, 0, "provisional", nil, sl("p1", 7.5, "testing", "A"))},
			want:  []Day{final("p1", today, "testing", 0, 7500)},
		},
		{
			name: "pending seconds stay apart from final ones, and the next revision moves them to final",
			steps: []step{
				bk("d1", m0, 0, "pending", ao("p1", 20), sl("p1", 10, "prompting", "A")),
				bk("d1", m1, 0, "pending", nil, sl("p1", 5, "prompting", "A")),
				bk("d1", m0, 1, "final", ao("p1", 20), sl("p1", 15, "prompting", "A")),
			},
			want: []Day{agent("p1", today, 20000, 0), {Project: "p1", Day: today, Kind: "prompting", FinalMS: [3]int64{15000}, PendingMS: [3]int64{5000}}},
		},
		{
			name: "a revision no newer than the one applied changes nothing",
			steps: []step{
				bk("d1", m0, 1, "final", nil, sl("p1", 15, "prompting", "A")),
				bk("d1", m0, 0, "pending", nil, sl("p1", 5, "prompting", "A")),
				bk("d1", m0, 1, "final", nil, sl("p1", 10, "prompting", "A")),
			},
			want: []Day{final("p1", today, "prompting", 0, 15000)},
		},
		{
			name: "two devices crediting one slot keep the one with more credited seconds and raise concurrent_devices (SM 3.4)",
			steps: []step{
				bk("d1", m0, 0, "final", nil, sl("p1", 10, "prompting", "A")),
				bk("d2", m0, 0, "final", nil, sl("p2", 15, "hand_edit", "B")),
			},
			want:    []Day{final("p2", today, "hand_edit", 1, 15000)},
			flagged: []int64{m0},
		},
		{
			name: "on a tie in credited seconds the keyboard-backed slot, level A, wins",
			steps: []step{
				bk("d1", m0, 0, "final", nil, sl("p1", 15, "prompting", "B")),
				bk("d2", m0, 0, "final", nil, sl("p2", 15, "prompting", "A")),
			},
			want:    []Day{final("p2", today, "prompting", 0, 15000)},
			flagged: []int64{m0},
		},
		{
			name: "on a full tie the lower device_id wins, whichever arrived first",
			steps: []step{
				bk("d2", m0, 0, "final", nil, sl("p2", 15, "prompting", "A")),
				bk("d1", m0, 0, "final", nil, sl("p1", 15, "prompting", "A")),
			},
			want:    []Day{final("p1", today, "prompting", 0, 15000)},
			flagged: []int64{m0},
		},
		{
			name: "when the winner's next revision credits nothing there, the other device's slot counts again and the flag clears",
			steps: []step{
				bk("d1", m0, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
				bk("d2", m0, 0, "final", nil, sl("p2", 10, "reviewing", "A")),
				bk("d1", m0, 1, "final", nil, none),
			},
			want: []Day{final("p2", today, "reviewing", 0, 10000)},
		},
		{
			name: "slots merge one by one, so two devices can each count in a different slot of one minute",
			steps: []step{
				bk("d1", m0, 0, "final", nil, sl("p1", 15, "prompting", "A"), none, none, none),
				bk("d2", m0, 0, "final", nil, none, sl("p2", 15, "testing", "B"), sl("p2", 15, "testing", "B"), none),
			},
			want: []Day{final("p1", today, "prompting", 0, 15000), final("p2", today, "testing", 1, 30000)},
		},
		{
			name: "the agent's time on its own counts once a minute per project across devices, from the device that reports the most there",
			steps: []step{
				bk("d1", m0, 0, "final", ao("p1", 40)),
				bk("d2", m0, 0, "pending", map[string]float64{"p1": 20, "p2": 10}),
			},
			want: []Day{agent("p1", today, 40000, 0), agent("p2", today, 0, 10000)},
		},
		{
			name: "the agent's time on its own goes to each project agent_only names, several in one minute (D-bucket-agent-only)",
			steps: []step{
				bk("d1", m0, 0, "final", map[string]float64{"p1": 45, "p2": 15}, sl("p3", 15, "reviewing", "A")),
			},
			want: []Day{agent("p1", today, 45000, 0), agent("p2", today, 15000, 0), final("p3", today, "reviewing", 0, 15000)},
		},
		{
			name: "another device's credit in a project covers that part of the minute, so the agent has less of it on its own there (SM 2.6)",
			steps: []step{
				bk("d1", m0, 0, "final", ao("p1", 60)),
				bk("d2", m0, 0, "final", nil, sl("p1", 15, "prompting", "A"), sl("p1", 15, "prompting", "A")),
			},
			want: []Day{agent("p1", today, 30000, 0), final("p1", today, "prompting", 0, 30000)},
		},
		{
			name: "a minute counts toward the account's local day in its time zone",
			steps: []step{
				{zone: "Asia/Tokyo"},
				// 15:00 UTC on the 20th is midnight on the 21st in Tokyo.
				bk("d1", base, 0, "final", nil, sl("p1", 15, "prompting", "A")),
			},
			want: []Day{final("p1", "2026-11-21", "prompting", 0, 15000)},
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
				final("p1", "2027-09-04", "prompting", 0, 25000),
				final("p1", "2027-09-05", "prompting", 0, 6000),
				final("p1", "2027-09-06", "prompting", 0, 2000),
			},
		},
		{
			name: "after the time zone changes, a minute's next revision moves its time to its day in the new zone",
			steps: []step{
				bk("d1", base+8*3600+30*60, 0, "pending", nil, sl("p1", 15, "prompting", "A")),
				{zone: "Asia/Tokyo"},
				bk("d1", base+8*3600+30*60, 1, "final", nil, sl("p1", 15, "prompting", "A")),
			},
			want: []Day{final("p1", "2026-11-21", "prompting", 0, 15000)},
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
				if got := flagged(t, db, "u1"); !reflect.DeepEqual(got, tt.flagged) {
					t.Errorf("concurrent_devices minutes %v, want %v", got, tt.flagged)
				}
			})
		}
	}
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
// order leave the same rollups and flags, because only each device's
// latest revision of each minute counts.
func TestReplayConverges(t *testing.T) {
	// The revisions: three devices, six minutes, three revisions each,
	// with credit, levels, states and agent time drawn from a fixed seed.
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
	type result struct {
		rows    []Day
		flagged []int64
	}
	replay := func(t *testing.T, engine store.Engine, order []step) result {
		db := newDB(t, engine, "u1")
		if err := run(t, db, "u1", order); err != nil {
			t.Fatal(err)
		}
		return result{rows: rows(t, db, "u1"), flagged: flagged(t, db, "u1")}
	}
	for _, engine := range storetest.Engines() {
		want := replay(t, engine, revs)
		if len(want.rows) == 0 || len(want.flagged) == 0 {
			t.Fatalf("the revisions in order left rows %v and flags %v; the test needs both", want.rows, want.flagged)
		}
		for seed := range uint64(5) {
			t.Run(string(engine)+"/shuffle "+string(rune('a'+seed))+" leaves the rollups and flags of applying the revisions in order", func(t *testing.T) {
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
	t.Run("a pkg/event minute bucket keeps every field it carries, as a Bucket of the account's", func(t *testing.T) {
		if got := FromEvent("u1", m); !reflect.DeepEqual(got, want) {
			t.Errorf("FromEvent:\n got %+v\nwant %+v", got, want)
		}
	})
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
