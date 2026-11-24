// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/witness"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/event"
)

// utc is a time on a UTC date, to the millisecond.
func utc(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

// The day most cases build: Friday 2026-11-20 in a UTC account, which ends
// at Saturday 00:00 and is decided 72 hours later, at Tuesday 00:00.
var (
	friday   = "2026-11-20"
	morning  = utc(2026, 11, 20, 10, 0)
	deadline = utc(2026, 11, 24, 0, 0)
)

// txDoer runs each op in a transaction of its own, as the writer does.
// before, when set, runs once before the next op, as another op the writer
// orders first, such as an upload whose batch commits just before.
type txDoer struct {
	db     *store.DB
	before func()
}

func (d *txDoer) Do(ctx context.Context, _ int, op writer.Op) error {
	if f := d.before; f != nil {
		d.before = nil
		f()
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

// world is a migrated database with account u1, a mini ingest that labels
// and stores revisions as ingest does, and the build-day job on a fake
// clock.
type world struct {
	t   *testing.T
	db  *store.DB
	d   *txDoer
	job *Job
	now time.Time
	// batches is the number of each device's last batch.
	batches map[string]int64
}

func newWorld(t *testing.T, engine store.Engine, zone string) *world {
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
	if zone == "" {
		zone = "UTC"
	}
	if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms, tz)
VALUES ('u1', 'sub-u1', 'verified', 0, 0, 0, $1)`, zone); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, db: db, d: &txDoer{db: db}, batches: map[string]int64{}}
	w.job = NewJob(w.d, db.Read, func() time.Time { return w.now })
	return w
}

// batch stores device's next batch root as received at at, as its witness
// row.
func (w *world) batch(device string, at time.Time) {
	w.t.Helper()
	w.batches[device]++
	if _, err := w.db.Write.ExecContext(context.Background(), `INSERT INTO witness_rows
(device_id, b, account_id, n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id)
VALUES ($1, $2, 'u1', 1, 'root', 'head', 'prev', $3, 'sig', 'key')`, device, w.batches[device], at.UnixMilli()); err != nil {
		w.t.Fatal(err)
	}
}

// store stores bucket m as ingest does at at, in one transaction: the
// server's own label from witness.Bucket, unless labeled is false, as a
// release from before migration 0009 stored none; the revision in
// minute_buckets and minute_bucket_revisions; and the rollups, through
// rollup.ApplyEvent.
func (w *world) store(at time.Time, m event.MinuteBucket, labeled bool) {
	w.t.Helper()
	ctx := context.Background()
	tx, err := w.db.Write.BeginTx(ctx, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	defer tx.Rollback()
	minuteMS := m.Minute.UnixMilli()
	var prev *event.MinuteBucket
	var cur *witness.Stored
	var stored string
	var revisions int64
	var label sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT b.bucket, b.revisions, r.server_witness FROM minute_buckets b
LEFT JOIN minute_bucket_revisions r ON r.device_id = b.device_id AND r.minute_ms = b.minute_ms AND r.rev = b.rev
WHERE b.device_id = $1 AND b.minute_ms = $2`, m.Device, minuteMS).Scan(&stored, &revisions, &label)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		w.t.Fatal(err)
	default:
		prev = new(event.MinuteBucket)
		if err := json.Unmarshal([]byte(stored), prev); err != nil {
			w.t.Fatal(err)
		}
		cur = &witness.Stored{Bucket: *prev, Label: event.Witness(label.String)}
	}
	var own any
	if labeled {
		l, err := witness.Bucket(ctx, tx, witness.Revision{Bucket: m, ReceivedAt: at.UnixMilli()}, cur)
		if err != nil {
			w.t.Fatal(err)
		}
		own = string(l)
	}
	data, err := json.Marshal(m)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO minute_buckets (device_id, minute_ms, account_id, rev, state, received_at_ms, bucket, revisions, server_witness)
VALUES ($1, $2, 'u1', $3, $4, $5, $6, $7, $8)
ON CONFLICT (device_id, minute_ms) DO UPDATE SET rev = excluded.rev, state = excluded.state,
received_at_ms = excluded.received_at_ms, bucket = excluded.bucket, revisions = excluded.revisions, server_witness = excluded.server_witness`,
		m.Device, minuteMS, int64(m.Rev), string(m.State), at.UnixMilli(), string(data), revisions+1, own); err != nil {
		w.t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO minute_bucket_revisions (device_id, minute_ms, rev, account_id, received_at_ms, bucket, server_witness)
VALUES ($1, $2, $3, 'u1', $4, $5, $6)`, m.Device, minuteMS, int64(m.Rev), at.UnixMilli(), string(data), own); err != nil {
		w.t.Fatal(err)
	}
	if err := rollup.ApplyEvent(ctx, tx, "u1", prev, m); err != nil {
		w.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		w.t.Fatal(err)
	}
}

// upload stores each bucket as ingest does at at, with the client's
// reading the bucket says.
func (w *world) upload(at time.Time, ms []event.MinuteBucket) {
	w.t.Helper()
	for _, m := range ms {
		w.store(at, m, true)
	}
}

// live uploads ms as a device online does: a batch received at at, then
// the buckets, read witnessed, at at.
func (w *world) live(at time.Time, ms []event.MinuteBucket) {
	w.t.Helper()
	w.batch(ms[0].Device, at)
	w.upload(at, read(ms, event.Witnessed))
}

// online uploads each of ms as a device online does, the minute after it
// ends: a batch received then, and the bucket, read witnessed.
func (w *world) online(ms []event.MinuteBucket) {
	w.t.Helper()
	for _, m := range ms {
		at := m.Minute.Add(time.Minute)
		w.batch(m.Device, at)
		w.store(at, read([]event.MinuteBucket{m}, event.Witnessed)[0], true)
	}
}

// catchUp uploads ms as a device back after an outage does: its next
// batch, received at at, then the buckets, read offline, at at.
func (w *world) catchUp(at time.Time, ms []event.MinuteBucket) {
	w.t.Helper()
	w.batch(ms[0].Device, at)
	w.upload(at, read(ms, event.Offline))
}

// read returns ms with the client's reading r.
func read(ms []event.MinuteBucket, r event.Witness) []event.MinuteBucket {
	out := make([]event.MinuteBucket, len(ms))
	for i, m := range ms {
		m.Witness = r
		out[i] = m
	}
	return out
}

// run runs the job once at at.
func (w *world) run(at time.Time) Result {
	w.t.Helper()
	w.now = at
	r, err := w.job.Once(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

// row is a build_days row.
type row struct {
	a, b    int64
	counted bool
	decided time.Time
}

// day returns the account's build_days row of day, and whether there is
// one.
func (w *world) day(day string) (row, bool) {
	w.t.Helper()
	var r row
	var counted, decided int64
	err := w.db.Read.QueryRowContext(context.Background(), `SELECT a_ms, b_ms, counted, decided_at_ms FROM build_days WHERE user_id = 'u1' AND day = $1`, day).
		Scan(&r.a, &r.b, &counted, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, false
	}
	if err != nil {
		w.t.Fatal(err)
	}
	r.counted, r.decided = counted == 1, time.UnixMilli(decided).UTC()
	return r, true
}

// summary reads the account's summary at at.
func (w *world) summary(at time.Time) Summary {
	w.t.Helper()
	s, err := Read(context.Background(), w.db.Read, "u1", at)
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

// rolled returns rollups_daily's final plus pending A and B of day.
func (w *world) rolled(day string) rollup.Credit {
	w.t.Helper()
	rows, err := rollup.Read(context.Background(), w.db.Read, "u1", day, day)
	if err != nil {
		w.t.Fatal(err)
	}
	var c rollup.Credit
	for _, r := range rows {
		c.AMS += r.FinalMS[0] + r.PendingMS[0]
		c.BMS += r.FinalMS[1] + r.PendingMS[1]
	}
	return c
}

// slot credits s seconds at level ev to project p, with labels.
func slot(p string, s float64, ev event.Evidence, lbl ...event.Label) event.BucketSlot {
	return event.BucketSlot{Project: p, Credited: s, Why: event.ReasonPrompt, Kind: event.ActivityPrompting, Ev: ev, Lbl: lbl}
}

// minute is device's final revision 0 of the minute that starts at at,
// with slots, which it fills to four with empty ones.
func minute(device string, at time.Time, slots ...event.BucketSlot) event.MinuteBucket {
	var total float64
	for _, s := range slots {
		total += s.Credited
	}
	for len(slots) < 4 {
		slots = append(slots, event.BucketSlot{})
	}
	return event.MinuteBucket{
		V: "0.3.0", Params: "sha256:0", Minute: at.UTC(), Device: device,
		Slots: slots, CreditedS: total, State: event.StateFinal, Witness: event.Witnessed,
	}
}

// minutes is n of device's minutes from at on, each with the four slots.
func minutes(device string, at time.Time, n int, slots ...event.BucketSlot) []event.MinuteBucket {
	var out []event.MinuteBucket
	for i := range n {
		out = append(out, minute(device, at.Add(time.Duration(i)*time.Minute), slots...))
	}
	return out
}

// full is n of device's minutes from at on, each crediting 60 seconds at
// level ev, 15 in each slot, with labels.
func full(device string, at time.Time, n int, ev event.Evidence, lbl ...event.Label) []event.MinuteBucket {
	s := slot("p_one", 15, ev, lbl...)
	return minutes(device, at, n, s, s, s, s)
}

// revised is ms as revision rev, in state st, with slots in place of
// theirs.
func revised(ms []event.MinuteBucket, rev uint32, st event.BucketState, slots ...event.BucketSlot) []event.MinuteBucket {
	out := make([]event.MinuteBucket, len(ms))
	for i, m := range ms {
		n := minute(m.Device, m.Minute, slots...)
		n.Rev, n.State = rev, st
		out[i] = n
	}
	return out
}

// withState is ms in state st.
func withState(ms []event.MinuteBucket, st event.BucketState) []event.MinuteBucket {
	out := make([]event.MinuteBucket, len(ms))
	for i, m := range ms {
		m.State = st
		out[i] = m
	}
	return out
}

func cat(parts ...[]event.MinuteBucket) []event.MinuteBucket {
	var out []event.MinuteBucket
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// TestBuildDays checks DESIGN 6.2 D10 as D-sv12-lag words it, PLAN
// WP-SV12's CI checks, with a fake clock, over revisions the server labels
// itself (package witness), in a UTC account unless a case says otherwise.
func TestBuildDays(t *testing.T) {
	type want struct {
		// stored says the day has a build_days row, and counted that it is
		// a build day.
		stored, counted bool
		// a and b, when set, are its stored milliseconds.
		a, b int64
	}
	tests := []struct {
		name string
		zone string
		do   func(w *world)
		day  string
		want want
		// check, when set, checks more after do.
		check func(t *testing.T, w *world)
	}{
		{
			name: "a day whose 900 seconds at level A reach the server in an offline catch-up 70 hours after it ends counts",
			do: func(w *world) {
				w.batch("d1", utc(2026, 11, 20, 9, 0))
				w.catchUp(utc(2026, 11, 23, 22, 0), full("d1", morning, 15, event.EvidenceA))
				w.run(utc(2026, 11, 23, 22, 1))
				w.run(deadline)
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "a day whose 900 seconds reach the server in an offline catch-up 73 hours after it ends doesn't count",
			do: func(w *world) {
				w.batch("d1", utc(2026, 11, 20, 9, 0))
				w.run(deadline)
				w.catchUp(utc(2026, 11, 24, 1, 0), full("d1", morning, 15, event.EvidenceA))
				w.run(utc(2026, 11, 24, 1, 1))
			},
			want: want{stored: true, counted: false, a: 0},
		},
		{
			name: "a revision received at the deadline, 2026-11-24T00:00:00.000Z, counts",
			do: func(w *world) {
				w.batch("d1", utc(2026, 11, 20, 9, 0))
				w.catchUp(deadline, full("d1", morning, 15, event.EvidenceA))
				w.run(deadline)
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "a revision received a millisecond after the deadline doesn't count",
			do: func(w *world) {
				w.batch("d1", utc(2026, 11, 20, 9, 0))
				w.catchUp(deadline.Add(time.Millisecond), full("d1", morning, 15, event.EvidenceA))
				w.run(deadline.Add(time.Millisecond))
			},
			want: want{stored: true, counted: false},
		},
		{
			name: "a minute at 00:05 that reaches the server in an offline catch-up at 2026-11-23T23:00Z, 94 h 55 min after it began, counts",
			do: func(w *world) {
				w.batch("d1", utc(2026, 11, 20, 0, 0))
				w.catchUp(utc(2026, 11, 23, 23, 0), full("d1", utc(2026, 11, 20, 0, 5), 15, event.EvidenceA))
				w.run(utc(2026, 11, 23, 23, 1))
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "a day counts at the job's first run after its 900 seconds are final, before its deadline",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 15), full("d1", morning, 15, event.EvidenceA))
				w.run(utc(2026, 11, 20, 10, 16))
			},
			want: want{stored: true, counted: true, a: 900_000},
			check: func(t *testing.T, w *world) {
				r, _ := w.day(friday)
				if !r.decided.Equal(utc(2026, 11, 20, 10, 16)) {
					t.Errorf("decided at %v, want the run at 10:16", r.decided)
				}
				// Its credit didn't change after it first reached 900 s, so
				// it stores rollups_daily's final plus pending A and B.
				if got := w.rolled(friday); got.AMS != r.a || got.BMS != r.b {
					t.Errorf("stored A %d and B %d, rollups_daily's final plus pending %+v", r.a, r.b, got)
				}
			},
		},
		{
			name: "an offline catch-up 56 hours after the day ends, a school sync on Monday at 08:00, counts",
			do: func(w *world) {
				w.batch("d1", utc(2026, 11, 20, 9, 0))
				w.catchUp(utc(2026, 11, 23, 8, 0), full("d1", morning, 15, event.EvidenceA))
				w.run(utc(2026, 11, 23, 8, 1))
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "a pending revision counts once its minute ended PENDING_WINDOW_S before the read, and not a millisecond before",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 15), withState(full("d1", morning, 15, event.EvidenceA), event.StatePending))
				// The last minute, 10:14, ends at 10:15 and is 900 s old at
				// 10:30; at 10:29:59.999 only 14 minutes count, 840 s.
				w.run(utc(2026, 11, 20, 10, 30).Add(-time.Millisecond))
				if _, ok := w.day(friday); ok {
					w.t.Errorf("the day counted at 10:29:59.999, before its last pending minute was 900 s old")
				}
				w.run(utc(2026, 11, 20, 10, 30))
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "a day that counted stays counted when a later backfilled revision lowers its minutes to 0, while the owner's rollups move",
			do: func(w *world) {
				ms := full("d1", morning, 15, event.EvidenceA)
				w.live(utc(2026, 11, 20, 10, 15), ms)
				w.run(utc(2026, 11, 20, 10, 16))
				w.batch("d1", utc(2026, 11, 22, 10, 0))
				w.upload(utc(2026, 11, 22, 10, 0), revised(ms, 1, event.StateFinal))
				w.run(utc(2026, 11, 22, 10, 1))
				w.run(deadline)
			},
			want: want{stored: true, counted: true, a: 900_000},
			check: func(t *testing.T, w *world) {
				if got := w.rolled(friday); got.Total() != 0 {
					t.Errorf("rollups_daily holds %+v, want the backfilled revision's 0", got)
				}
			},
		},
		{
			name: "a day that counted stays counted when a second device's offline catch-up later wins its slots at level C",
			do: func(w *world) {
				w.batch("d2", utc(2026, 11, 20, 9, 0))
				half := slot("p_one", 7.5, event.EvidenceA)
				w.online(minutes("d1", morning, 30, half, half, half, half))
				w.run(utc(2026, 11, 20, 10, 31))
				w.catchUp(utc(2026, 11, 22, 10, 0), full("d2", morning, 30, event.EvidenceC))
				w.run(utc(2026, 11, 22, 10, 1))
				w.run(deadline)
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "a day whose credit reached 900 seconds before a second device's catch-up took its slots at level C counts when the job first runs only after its deadline",
			do: func(w *world) {
				w.batch("d2", utc(2026, 11, 20, 9, 0))
				half := slot("p_one", 7.5, event.EvidenceA)
				w.online(minutes("d1", morning, 30, half, half, half, half))
				w.catchUp(utc(2026, 11, 21, 10, 0), full("d2", morning, 30, event.EvidenceC))
				// The job's first run comes an hour after the deadline, as
				// after an outage or on the first run after migration 0011.
				w.run(deadline.Add(time.Hour))
			},
			want: want{stored: true, counted: true, a: 900_000},
			check: func(t *testing.T, w *world) {
				if got := w.rolled(friday); got.Total() != 0 {
					t.Errorf("rollups_daily holds %+v, want d2's level C winning every slot", got)
				}
			},
		},
		{
			name: "a day whose credit reached 900 seconds before a second device's catch-up took its slots at level C counts when the job first runs after the catch-up but before the deadline",
			do: func(w *world) {
				w.batch("d2", utc(2026, 11, 20, 9, 0))
				half := slot("p_one", 7.5, event.EvidenceA)
				w.online(minutes("d1", morning, 30, half, half, half, half))
				w.catchUp(utc(2026, 11, 21, 10, 0), full("d2", morning, 30, event.EvidenceC))
				w.run(utc(2026, 11, 22, 12, 0))
			},
			want: want{stored: true, counted: true, a: 900_000},
			check: func(t *testing.T, w *world) {
				if r, _ := w.day(friday); !r.decided.Equal(utc(2026, 11, 22, 12, 0)) {
					t.Errorf("decided at %v, want the first run, before the deadline", r.decided)
				}
			},
		},
		{
			name: "a day whose first minutes arrive 8 days late is decided not to count at the next run, and its week is decided at its deadline",
			do: func(w *world) {
				for d := 9; d <= 11; d++ {
					buildOn(w, 2026, 11, d)
				}
				// The week of 2026-11-09 is decided, so the next one is 11-16.
				w.run(utc(2026, 11, 19, 0, 0))
				w.live(utc(2026, 11, 24, 12, 0), full("d1", utc(2026, 11, 16, 10, 0), 15, event.EvidenceA))
				w.run(utc(2026, 11, 24, 12, 1))
				// The week of 2026-11-16 ends at 11-23T00:00Z.
				w.run(utc(2026, 11, 26, 0, 0))
			},
			day:  "2026-11-16",
			want: want{stored: true, counted: false},
			check: func(t *testing.T, w *world) {
				got, err := Export(context.Background(), w.db.Read, "u1")
				if err != nil {
					t.Fatal(err)
				}
				if got.Goal == nil || got.Goal.NextWeek != "2026-11-23" {
					t.Errorf("goal %+v, want the week of 2026-11-16 decided and 2026-11-23 next", got.Goal)
				}
			},
		},
		{
			name: "a catch-up whose batch is still open when a run reads, and has no later revision of its day, is stored by the next run before the deadline",
			do: func(w *world) {
				// The first run, so the runs after it aren't first runs.
				w.run(utc(2026, 11, 20, 9, 0))
				w.batch("d1", utc(2026, 11, 20, 9, 30))
				// Ingest stamps the catch-up at 09:00 on the 21st, and its
				// batch commits after the run at 09:00:30 has read.
				w.run(utc(2026, 11, 21, 9, 0).Add(30 * time.Second))
				w.catchUp(utc(2026, 11, 21, 9, 0), full("d1", morning, 15, event.EvidenceA))
				w.run(utc(2026, 11, 21, 9, 1).Add(30 * time.Second))
			},
			want: want{stored: true, counted: true, a: 900_000},
			check: func(t *testing.T, w *world) {
				if r, _ := w.day(friday); !r.decided.Equal(utc(2026, 11, 21, 9, 1).Add(30 * time.Second)) {
					t.Errorf("decided at %v, want the run after the batch committed", r.decided)
				}
			},
		},
		{
			name: "a credit that passes 900 seconds and falls back between two runs, neither the first, is stored by the second, with its A and B as of the first reach",
			do: func(w *world) {
				w.run(utc(2026, 11, 20, 9, 0))
				w.batch("d2", utc(2026, 11, 20, 9, 0))
				half := slot("p_one", 7.5, event.EvidenceA)
				w.online(minutes("d1", morning, 20, half, half, half, half))
				w.run(utc(2026, 11, 20, 10, 25))
				if _, ok := w.day(friday); ok {
					w.t.Errorf("the day was stored at 10:25 with 600 seconds")
				}
				// 900 s at 10:30, then d2's level C wins every slot at 10:40.
				w.online(minutes("d1", morning.Add(20*time.Minute), 10, half, half, half, half))
				w.catchUp(utc(2026, 11, 20, 10, 40), full("d2", morning, 30, event.EvidenceC))
				w.run(utc(2026, 11, 20, 10, 45))
			},
			want: want{stored: true, counted: true, a: 900_000},
			check: func(t *testing.T, w *world) {
				if got := w.rolled(friday); got.Total() != 0 {
					t.Errorf("rollups_daily holds %+v, want d2's level C winning every slot", got)
				}
			},
		},
		{
			name: "a run 30 hours after the deadline decides a day an offline catch-up at 73 hours raised from 600 to 1,200 seconds not to count",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 10), full("d1", morning, 10, event.EvidenceA))
				w.catchUp(utc(2026, 11, 24, 1, 0), full("d1", utc(2026, 11, 20, 10, 10), 10, event.EvidenceA))
				w.run(deadline.Add(30 * time.Hour))
			},
			want: want{stored: true, counted: false, a: 600_000},
			check: func(t *testing.T, w *world) {
				if got := w.rolled(friday); got.AMS != 1_200_000 {
					t.Errorf("rollups_daily holds %+v, want 1,200 s at level A", got)
				}
			},
		},
		{
			name: "a run on time decides the day the late catch-up raised from 600 to 1,200 seconds not to count, as the late run does",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 10), full("d1", morning, 10, event.EvidenceA))
				w.run(deadline)
				w.catchUp(utc(2026, 11, 24, 1, 0), full("d1", utc(2026, 11, 20, 10, 10), 10, event.EvidenceA))
				w.run(deadline.Add(30 * time.Hour))
			},
			want: want{stored: true, counted: false, a: 600_000},
		},
		{
			name: "a revision received 10 ms before the deadline whose writer batch is still open when the job's first run after the deadline starts, counts",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 10), full("d1", morning, 10, event.EvidenceA))
				// The run's look at the read pool finds 600 s; the catch-up's
				// batch commits after it, before the run's writer op.
				w.d.before = func() {
					w.catchUp(deadline.Add(-10*time.Millisecond), full("d1", utc(2026, 11, 20, 10, 10), 5, event.EvidenceA))
				}
				w.run(deadline.Add(time.Second))
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "Saturday 2026-11-21's minutes sent on Monday by a device that uploaded batches through Saturday are labeled backfilled and count nothing, though the device sent them as witnessed at level A",
			do: func(w *world) {
				for _, h := range []int{8, 12, 23} {
					w.batch("d1", utc(2026, 11, 21, h, 0))
				}
				w.live(utc(2026, 11, 23, 8, 0), full("d1", utc(2026, 11, 21, 10, 0), 15, event.EvidenceA))
				w.run(utc(2026, 11, 23, 8, 1))
				w.run(utc(2026, 11, 25, 0, 0))
			},
			day:  "2026-11-21",
			want: want{stored: true, counted: false},
		},
		{
			name: "three days of lookback uploaded as live by a device whose chain began after them are labeled backfilled and count nothing",
			do: func(w *world) {
				var ms []event.MinuteBucket
				for d := 18; d <= 20; d++ {
					ms = append(ms, full("d1", utc(2026, 11, d, 10, 0), 15, event.EvidenceA)...)
				}
				w.live(utc(2026, 11, 23, 10, 0), ms)
				w.run(utc(2026, 11, 23, 10, 1))
				w.run(deadline)
			},
			want: want{stored: true, counted: false},
			check: func(t *testing.T, w *world) {
				for _, d := range []string{"2026-11-18", "2026-11-19"} {
					if r, ok := w.day(d); !ok || r.counted || r.a != 0 {
						t.Errorf("%s: row %+v (stored %v), want a day decided not to count, with nothing", d, r, ok)
					}
				}
			},
		},
		{
			name: "a restore the server labeled backfilled keeps the minute's earlier witnessed credit",
			do: func(w *world) {
				half := slot("p_one", 7.5, event.EvidenceA)
				ms := withState(minutes("d1", morning, 30, half, half, half, half), event.StateProvisional)
				w.online(ms)
				w.batch("d1", utc(2026, 11, 22, 10, 0))
				s := slot("p_one", 15, event.EvidenceA)
				w.upload(utc(2026, 11, 22, 10, 0), revised(ms, 1, event.StateRestored, s, s, s, s))
				w.run(utc(2026, 11, 22, 10, 1))
			},
			want: want{stored: true, counted: true, a: 900_000},
			check: func(t *testing.T, w *world) {
				if got := w.rolled(friday); got.AMS != 1_800_000 {
					t.Errorf("rollups_daily holds %+v, want the restore's 1,800 s", got)
				}
			},
		},
		{
			name: "a revision with no server label, stored before migration 0009, goes by its bucket's own witness",
			do: func(w *world) {
				for _, m := range full("d1", morning, 15, event.EvidenceA) {
					w.store(utc(2026, 11, 20, 10, 15), m, false)
				}
				w.run(utc(2026, 11, 20, 10, 16))
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "a revision with no server label whose bucket reads backfilled counts nothing",
			do: func(w *world) {
				for _, m := range read(full("d1", morning, 15, event.EvidenceA), event.Backfilled) {
					w.store(utc(2026, 11, 20, 10, 15), m, false)
				}
				w.run(deadline)
			},
			want: want{stored: true, counted: false},
		},
		{
			name: "450 seconds at level A and 450 at level B count",
			do: func(w *world) {
				a, b := slot("p_one", 15, event.EvidenceA), slot("p_one", 15, event.EvidenceB)
				w.live(utc(2026, 11, 20, 10, 15), minutes("d1", morning, 15, a, a, b, b))
				w.run(utc(2026, 11, 20, 10, 16))
			},
			want: want{stored: true, counted: true, a: 450_000, b: 450_000},
		},
		{
			name: "899.5 seconds at level A don't count",
			do: func(w *world) {
				s := slot("p_one", 15, event.EvidenceA)
				ms := cat(minutes("d1", morning, 14, s, s, s, s), []event.MinuteBucket{minute("d1", morning.Add(14*time.Minute), s, s, s, slot("p_one", 14.5, event.EvidenceA))})
				w.live(utc(2026, 11, 20, 10, 15), ms)
				w.run(utc(2026, 11, 20, 10, 16))
				w.run(deadline)
			},
			want: want{stored: true, counted: false, a: 899_500},
		},
		{
			name: "two devices crediting the same slots at level A count them once",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 10), full("d1", morning, 10, event.EvidenceA))
				w.live(utc(2026, 11, 20, 10, 10), full("d2", morning, 10, event.EvidenceA))
				w.run(deadline)
			},
			want: want{stored: true, counted: false, a: 600_000},
		},
		{
			name: "a slot another device wins with more seconds at level C counts nothing",
			do: func(w *world) {
				s := slot("p_one", 12.5, event.EvidenceA)
				w.online(minutes("d1", morning, 20, s, s, s, s))
				w.online(full("d2", morning, 8, event.EvidenceC))
				w.run(deadline)
			},
			// d1's 1,000 s lose 8 minutes of 50 s to d2's level C.
			want: want{stored: true, counted: false, a: 600_000},
		},
		{
			name: "450 seconds in each of two projects count",
			do: func(w *world) {
				p1, p2 := slot("p_one", 15, event.EvidenceA), slot("p_two", 15, event.EvidenceA)
				w.live(utc(2026, 11, 20, 10, 15), minutes("d1", morning, 15, p1, p1, p2, p2))
				w.run(utc(2026, 11, 20, 10, 16))
			},
			want: want{stored: true, counted: true, a: 900_000},
		},
		{
			name: "level C never counts: a lookback day",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 15), full("d1", morning, 15, event.EvidenceC))
				w.run(deadline)
			},
			want: want{stored: true, counted: false},
		},
		{
			name: "level C never counts: relayed minutes (via_relay)",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 15), full("d1", morning, 15, event.EvidenceC, event.LabelViaRelay))
				w.run(deadline)
			},
			want: want{stored: true, counted: false},
		},
		{
			name: "level C never counts: focus unknown (focus_unknown)",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 15), full("d1", morning, 15, event.EvidenceC, event.LabelFocusUnknown))
				w.run(deadline)
			},
			want: want{stored: true, counted: false},
		},
		{
			name: "level C never counts: an uncorroborated side door (via_track)",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 15), full("d1", morning, 15, event.EvidenceC, event.LabelViaTrack))
				w.run(deadline)
			},
			want: want{stored: true, counted: false},
		},
		{
			// 2026-11-02T04:30Z is 23:30 EST on the 1st, in the day's 25th
			// hour.
			name: "in America/New_York 2026-11-01 runs 25 hours and is decided at 2026-11-05T05:00Z",
			zone: "America/New_York",
			do: func(w *world) {
				w.live(utc(2026, 11, 2, 4, 40), full("d1", utc(2026, 11, 2, 4, 30), 10, event.EvidenceA))
				w.run(utc(2026, 11, 5, 5, 0).Add(-time.Millisecond))
				if _, ok := w.day("2026-11-01"); ok {
					w.t.Errorf("2026-11-01 was decided before 2026-11-05T05:00Z")
				}
				w.run(utc(2026, 11, 5, 5, 0))
			},
			day:  "2026-11-01",
			want: want{stored: true, counted: false, a: 600_000},
		},
		{
			name: "after a change from UTC to America/Los_Angeles at 2026-11-20T12:00Z, a minute at 05:00Z first uploaded after it counts toward 2026-11-20, and the day is decided at 2026-11-24T08:00Z",
			do: func(w *world) {
				w.batch("d1", utc(2026, 11, 20, 4, 0))
				w.setZone("America/Los_Angeles", utc(2026, 11, 20, 12, 0))
				w.catchUp(utc(2026, 11, 20, 13, 0), full("d1", utc(2026, 11, 20, 5, 0), 10, event.EvidenceA))
				w.run(deadline)
				if _, ok := w.day(friday); ok {
					w.t.Errorf("2026-11-20 was decided at 2026-11-24T00:00Z, before its end in Los Angeles plus 72 hours")
				}
				w.run(utc(2026, 11, 24, 8, 0).Add(-time.Millisecond))
				if _, ok := w.day(friday); ok {
					w.t.Errorf("2026-11-20 was decided before 2026-11-24T08:00Z")
				}
				w.run(utc(2026, 11, 24, 8, 0))
			},
			want: want{stored: true, counted: false, a: 600_000},
		},
		{
			name: "after a change from UTC to Asia/Tokyo at 2026-11-20T12:00Z, 2026-11-20 is still decided at 2026-11-24T00:00Z",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 10), full("d1", morning, 10, event.EvidenceA))
				w.setZone("Asia/Tokyo", utc(2026, 11, 20, 12, 0))
				w.run(deadline.Add(-time.Millisecond))
				if _, ok := w.day(friday); ok {
					w.t.Errorf("2026-11-20 was decided before 2026-11-24T00:00Z")
				}
				w.run(deadline)
			},
			want: want{stored: true, counted: false, a: 600_000},
		},
		{
			name: "a minute counted before a zone change keeps its day when it is revised after it",
			do: func(w *world) {
				ms := full("d1", utc(2026, 11, 20, 5, 0), 1, event.EvidenceA)
				w.live(utc(2026, 11, 20, 5, 1), ms)
				w.setZone("America/Los_Angeles", utc(2026, 11, 20, 12, 0))
				w.live(utc(2026, 11, 20, 12, 30), revised(ms, 1, event.StateFinal, slot("p_one", 10, event.EvidenceA)))
			},
			check: func(t *testing.T, w *world) {
				if got := w.rolled(friday); got.AMS != 10_000 {
					t.Errorf("2026-11-20 holds %+v, want the revised minute's 10 s", got)
				}
				if got := w.rolled("2026-11-19"); got.Total() != 0 {
					t.Errorf("2026-11-19 holds %+v, want nothing", got)
				}
			},
		},
		{
			name: "a day decided before a zone change keeps its result",
			do: func(w *world) {
				w.live(utc(2026, 11, 16, 10, 10), full("d1", utc(2026, 11, 16, 10, 0), 10, event.EvidenceA))
				w.run(utc(2026, 11, 20, 0, 0))
				before, _ := w.day("2026-11-16")
				w.setZone("America/Los_Angeles", utc(2026, 11, 20, 12, 0))
				w.run(utc(2026, 11, 21, 0, 0))
				if after, _ := w.day("2026-11-16"); after != before {
					w.t.Errorf("2026-11-16 became %+v after the change, was %+v", after, before)
				}
			},
			day:  "2026-11-16",
			want: want{stored: true, counted: false, a: 600_000},
		},
		{
			name: "a day decided at its deadline whose revisions the server all labeled witnessed or offline, none received after it, stores rollups_daily's final plus pending A and B",
			do: func(w *world) {
				a, b := slot("p_one", 15, event.EvidenceA), slot("p_two", 10, event.EvidenceB)
				w.live(utc(2026, 11, 20, 10, 10), cat(
					minutes("d1", morning, 5, a, a, b),
					withState(minutes("d1", morning.Add(5*time.Minute), 3, b, a), event.StatePending),
					full("d1", morning.Add(8*time.Minute), 1, event.EvidenceC),
				))
				w.catchUp(utc(2026, 11, 22, 9, 0), minutes("d1", morning.Add(20*time.Minute), 4, b, b))
				w.run(deadline)
			},
			want: want{stored: true, counted: false, a: 195_000, b: 160_000},
			check: func(t *testing.T, w *world) {
				r, _ := w.day(friday)
				if got := w.rolled(friday); got.AMS != r.a || got.BMS != r.b {
					t.Errorf("stored A %d and B %d, rollups_daily's final plus pending %+v", r.a, r.b, got)
				}
			},
		},
		{
			name: "a decided day keeps its result after the minute-bucket job deletes its minutes",
			do: func(w *world) {
				w.live(utc(2026, 11, 20, 10, 15), full("d1", morning, 15, event.EvidenceA))
				w.run(utc(2026, 11, 20, 10, 16))
				w.sweepMinutes(utc(2027, 6, 1, 0, 0))
				w.run(utc(2027, 6, 1, 0, 1))
				if s := w.summary(utc(2026, 11, 21, 12, 0)); s.Days[len(s.Days)-2].State != BuildDay {
					w.t.Errorf("the summary shows 2026-11-20 as %q, want a build day", s.Days[len(s.Days)-2].State)
				}
			},
			want: want{stored: true, counted: true, a: 900_000},
			check: func(t *testing.T, w *world) {
				var n int
				if err := w.db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM rollup_minutes`).Scan(&n); err != nil || n != 0 {
					t.Errorf("rollup_minutes holds %d rows (%v), want the job to have deleted them", n, err)
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				w := newWorld(t, engine, tt.zone)
				tt.do(w)
				day := tt.day
				if day == "" {
					day = friday
				}
				if tt.want != (want{}) {
					r, ok := w.day(day)
					switch {
					case ok != tt.want.stored:
						t.Errorf("%s stored %v, want %v", day, ok, tt.want.stored)
					case ok && r.counted != tt.want.counted:
						t.Errorf("%s counted %v (A %d, B %d), want %v", day, r.counted, r.a, r.b, tt.want.counted)
					case ok && (r.a != tt.want.a || r.b != tt.want.b):
						t.Errorf("%s stored A %d and B %d, want %d and %d", day, r.a, r.b, tt.want.a, tt.want.b)
					}
				}
				if tt.check != nil {
					tt.check(t, w)
				}
			})
		}
	}
}

// setZone sets the account's zone at at, as the dashboard's form does.
func (w *world) setZone(zone string, at time.Time) {
	w.t.Helper()
	if err := w.d.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		return rollup.SetZone(ctx, tx, "u1", zone, at)
	}); err != nil {
		w.t.Fatal(err)
	}
}

// sweepMinutes runs the retention jobs of server.minute_buckets at at, the
// ones that delete minute buckets, their revisions and rollup_minutes 180
// days after the minute.
func (w *world) sweepMinutes(at time.Time) {
	w.t.Helper()
	p, err := retention.Default()
	if err != nil {
		w.t.Fatal(err)
	}
	on, _ := p.Jobs()
	var jobs []retention.Job
	for _, j := range on {
		if j.Entry == "server.minute_buckets" {
			jobs = append(jobs, j)
		}
	}
	for _, r := range retention.Sweep(context.Background(), w.d, jobs, at, 0) {
		if r.Err != nil {
			w.t.Fatal(r.Err)
		}
	}
}
