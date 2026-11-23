// SPDX-License-Identifier: AGPL-3.0-or-later

package witness

import (
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/params"
)

// day0 is the start of the day the tests use, in Unix ms.
var day0 = time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC).UnixMilli()

// at is minute m of day0, in Unix ms.
func at(m float64) int64 { return day0 + int64(m*60_000) }

// presence is os.presence's rtype, the record most batches hold.
var presence, _ = ledger.RTypeOf(event.KindOSPresence)

// ev is an event whose wall clock reads minute t of day0 and whose boot's
// monotonic clock reads mono minutes.
func ev(t, mono float64, boot string) Record {
	return Record{Type: presence, Timed: true, T: at(t), Mono: int64(mono * 60_000), Boot: boot}
}

// note is an upload-failure note of minute t of day0.
func note(t float64) Record {
	return Record{Type: ledger.RTypeUploadFailure, Timed: true, T: at(t)}
}

// chain is a device's ledger and the server's witness rows of it, built
// one batch at a time.
type chain struct {
	batches []Batch
	recs    []Record
}

// batch adds the next batch, received at minute got of day0 and holding
// recs, and returns the index of its first record.
func (c *chain) batch(got float64, recs ...Record) int {
	b := uint64(len(c.batches) + 1)
	c.batches = append(c.batches, Batch{B: b, ReceivedAt: at(got)})
	first := len(c.recs)
	for _, r := range recs {
		c.add(b, r)
	}
	return first
}

// add puts r in batch b, and returns its index.
func (c *chain) add(b uint64, r Record) int {
	r.S, r.B = uint64(len(c.recs)+1), b
	c.recs = append(c.recs, r)
	return len(c.recs) - 1
}

// receive sets when the server received batches first to last: at minute
// got, plus step minutes for each batch after first.
func (c *chain) receive(first, last uint64, got, step float64) {
	for k := range c.batches {
		if b := c.batches[k].B; b >= first && b <= last {
			c.batches[k].ReceivedAt = at(got + float64(b-first)*step)
		}
	}
}

// drop removes the server's rows of batches first to last, as a chain that
// starts later or a local-only gap leaves them.
func (c *chain) drop(first, last uint64) {
	kept := c.batches[:0]
	for _, b := range c.batches {
		if b.B < first || b.B > last {
			kept = append(kept, b)
		}
	}
	c.batches = kept
}

// only keeps the records of the batches keep names, as a reveal does, and
// all of the server's rows.
func (c *chain) only(keep func(b uint64) bool) {
	kept := c.recs[:0]
	for _, r := range c.recs {
		if keep(r.B) {
			kept = append(kept, r)
		}
	}
	c.recs = kept
}

// find returns the index of the presence record whose wall clock reads
// minute m, or -1.
func (c *chain) find(m float64) int {
	for i, r := range c.recs {
		if r.Type == presence && r.T == at(m) {
			return i
		}
	}
	return -1
}

// receipt returns when the server received batch b, or 0.
func (c *chain) receipt(b uint64) int64 {
	for _, x := range c.batches {
		if x.B == b {
			return x.ReceivedAt
		}
	}
	return 0
}

func table(t *testing.T) *params.Table {
	t.Helper()
	tab, err := params.ByVersion("0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func TestRecords(t *testing.T) {
	tests := []struct {
		name    string
		build   func(c *chain) int // the index of the record the case reads
		want    event.Witness
		refused bool
		// corrected, when set, is the corrected time the record must read.
		corrected int64
	}{
		{"a record whose batch reached the server within WITNESS_LAG_S is witnessed",
			func(c *chain) int { return c.batch(11, ev(10, 10, "b1")) }, event.Witnessed, false, 0},
		{"a record whose batch arrived just at the lag is still witnessed",
			func(c *chain) int { return c.batch(25, ev(10, 10, "b1")) }, event.Witnessed, false, 0},
		{"a record first seen after 15 minutes is backfilled",
			func(c *chain) int { return c.batch(25.5, ev(10, 10, "b1")) }, event.Backfilled, false, 0},
		{"a record lookback read is backfilled however soon its batch arrived",
			func(c *chain) int {
				r := ev(10, 10, "b1")
				r.Lookback = true
				return c.batch(10.5, r)
			}, event.Backfilled, false, 0},
		{"a record whose body wasn't revealed has no label",
			func(c *chain) int { return c.batch(10.5, Record{Type: presence}) }, "", false, 0},
		{"a record in a batch the server holds no row for has no label",
			func(c *chain) int {
				i := c.batch(10.5, ev(10, 10, "b1"))
				c.drop(1, 1)
				return i
			}, "", false, 0},
		{"a record dated more than five minutes after its batch arrived is refused",
			func(c *chain) int { return c.batch(10, ev(15.5, 15.5, "b1")) }, "", true, 0},
		{"a record dated up to five minutes ahead of its batch reads by the batch's receipt",
			func(c *chain) int { return c.batch(10, ev(14, 14, "b1")) }, event.Witnessed, false, at(10)},
		{"a note without a monotonic reading is labeled by its own time",
			func(c *chain) int { return c.batch(30, note(10)) }, event.Backfilled, false, at(10)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c chain
			i := tc.build(&c)
			res := Records(table(t), c.batches, c.recs)
			got := res.Readings[i]
			if got.Label != tc.want || got.Refused != tc.refused {
				t.Errorf("label %q, refused %v; want %q, %v (%+v)", got.Label, got.Refused, tc.want, tc.refused, got)
			}
			if tc.corrected != 0 && got.Corrected != tc.corrected {
				t.Errorf("corrected %d, want %d", got.Corrected, tc.corrected)
			}
			if got.Stretch != -1 || len(res.Stretches) != 0 {
				t.Errorf("stretch %d of %+v, want none", got.Stretch, res.Stretches)
			}
		})
	}
}

// TestClocks is PLAN's skewed clock, jumped clock and reboot: what the
// monotonic clock and the server's receipts correct, and what nothing can.
func TestClocks(t *testing.T) {
	// live adds a batch a minute from minute from to minute to of the true
	// clock, each holding one record and received half a minute after it.
	// wall maps the true minute to what the wall clock read, and mono to
	// what the boot's monotonic clock read.
	live := func(c *chain, from, to float64, boot string, wall, mono func(float64) float64) {
		for m := from; m <= to; m++ {
			c.batch(m+0.5, ev(wall(m), mono(m), boot))
		}
	}
	same := func(m float64) float64 { return m }
	type check struct {
		wall float64 // the record's wall minute
		want event.Witness
	}
	tests := []struct {
		name   string
		build  func(c *chain)
		checks []check
	}{
		{"a wall clock 20 minutes slow reads late, since a backdated record looks the same",
			func(c *chain) {
				live(c, 30, 40, "b1", func(m float64) float64 { return m - 20 }, same)
			}, []check{{15, event.Backfilled}, {20, event.Backfilled}}},
		{"a wall clock more than five minutes fast is refused",
			func(c *chain) {
				live(c, 30, 40, "b1", func(m float64) float64 { return m + 10 }, same)
			}, []check{{45, ""}, {50, ""}}},
		{"a clock set back an hour mid-boot doesn't make the records after it witnessed, and moves none before it",
			func(c *chain) {
				live(c, 60, 80, "b1", func(m float64) float64 {
					if m >= 70 {
						return m - 60
					}
					return m
				}, func(m float64) float64 { return m + 100 })
			}, []check{{65, event.Witnessed}, {69, event.Witnessed}, {15, event.Backfilled}, {20, event.Backfilled}}},
		{"a monotonic clock that stopped while the machine slept moves nothing",
			func(c *chain) {
				// The run clock stands still from minute 10 to minute 130.
				live(c, 1, 10, "run-a", same, same)
				live(c, 130, 140, "run-a", same, func(m float64) float64 { return m - 120 })
			}, []check{{5, event.Witnessed}, {10, event.Witnessed}, {130, event.Witnessed}, {140, event.Witnessed}}},
		{"a new boot's monotonic clock, which starts again near zero, doesn't hold the records of the boot before it",
			func(c *chain) {
				live(c, 1, 10, "boot-a", same, func(m float64) float64 { return m + 1000 })
				live(c, 20, 30, "boot-b", same, func(m float64) float64 { return m - 19 })
			}, []check{{5, event.Witnessed}, {10, event.Witnessed}, {20, event.Witnessed}, {30, event.Witnessed}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c chain
			tc.build(&c)
			res := Records(table(t), c.batches, c.recs)
			for _, ck := range tc.checks {
				i := c.find(ck.wall)
				if i < 0 {
					t.Fatalf("no record of wall minute %v", ck.wall)
				}
				got, r := res.Readings[i], c.recs[i]
				if got.Label != ck.want || got.Refused != (ck.want == "") {
					t.Errorf("the record of wall minute %v reads %q, refused %v; want %q (%+v)", ck.wall, got.Label, got.Refused, ck.want, got)
				}
				if got.Corrected > r.T || got.Corrected > c.receipt(r.B) {
					t.Errorf("the record of wall minute %v is corrected to %d, after its wall time %d or its batch's receipt", ck.wall, got.Corrected, r.T)
				}
			}
		})
	}
}

// TestReanchoring checks the corrected time against the server's receipts,
// carried back along a boot's monotonic clock.
func TestReanchoring(t *testing.T) {
	tests := []struct {
		name      string
		build     func(c *chain) int
		want      event.Witness
		corrected int64
	}{
		{"a wall clock up to five minutes fast reads by the server's receipts, so its lag isn't shortened",
			func(c *chain) int {
				i := c.batch(10.5, ev(13, 10, "b1"))
				c.batch(11.5, ev(14, 11, "b1"))
				return i
			}, event.Witnessed, at(10.5)},
		{"a record held back and dated fresh reads late when a later record of its boot reached the server on time",
			func(c *chain) int {
				// Made at minute 10, two minutes by the monotonic clock
				// before a record the server had at 12.5, then sent at 130
				// dated 125.
				c.batch(12.5, ev(12, 112, "b1"))
				return c.batch(130, ev(125, 110, "b1"))
			}, event.Backfilled, at(10.5)},
		{"a record of another boot bounds nothing",
			func(c *chain) int {
				c.batch(12.5, ev(12, 112, "b2"))
				return c.batch(130, ev(125, 110, "b1"))
			}, event.Witnessed, at(125)},
		{"a record without a monotonic reading neither bounds others nor is bounded",
			func(c *chain) int {
				c.batch(12.5, ev(12, 0, "b1"))
				return c.batch(130, ev(125, 110, "b1"))
			}, event.Witnessed, at(125)},
		{"records whose monotonic readings are equal bound nothing, since a clock stopped in sleep reads the same on both sides of it",
			func(c *chain) int {
				c.batch(12.5, ev(12, 110, "b1"))
				return c.batch(130, ev(125, 110, "b1"))
			}, event.Witnessed, at(125)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c chain
			i := tc.build(&c)
			got := Records(table(t), c.batches, c.recs).Readings[i]
			if got.Label != tc.want || got.Corrected != tc.corrected {
				t.Errorf("label %q corrected %d; want %q and %d", got.Label, got.Corrected, tc.want, tc.corrected)
			}
		})
	}
}

// outage is PLAN's two-hour outage: a batch a minute for four hours, each
// holding a presence record from half a minute before its seal; the
// server down from minute 60 to minute 180, with the uploader's tries
// failing every four minutes of it when notes is set; and every batch held
// back sent in one catch-up at minute 180.5. A note goes in the first
// batch sealed after its failure.
func outage(notes bool) *chain {
	var c chain
	for m := 1.0; m <= 240; m++ {
		recs := []Record{ev(m-0.5, m+100, "run-a")}
		if f := m - 0.8; notes && f >= 60 && f <= 180 && int(f-60)%4 == 0 {
			recs = append(recs, note(f))
		}
		got := m + 0.05
		if m >= 60 && m <= 180 {
			got = 180.5
		}
		c.batch(got, recs...)
	}
	return &c
}

// presences returns a presence record of each minute from from to to,
// with no monotonic reading.
func presences(from, to float64) []Record {
	var out []Record
	for m := from; m <= to; m++ {
		out = append(out, Record{Type: presence, Timed: true, T: at(m)})
	}
	return out
}

// twoCatchUps is the outage of outage(true) on a network that let the
// device through at minute 180.5 for batches 60 to 120 only, then dropped
// it again, its uploader failing every four minutes, until minute 200.5,
// when the rest went.
func twoCatchUps() *chain {
	c := outage(true)
	c.receive(121, 200, 200.5, 0)
	for f := 184.2; f < 200; f += 4 {
		c.add(uint64(f)+1, note(f))
	}
	return c
}

func TestOutage(t *testing.T) {
	tests := []struct {
		name   string
		notes  bool
		minute float64 // the record's wall minute
		want   event.Witness
	}{
		{"a minute an hour before the outage is witnessed", true, 0.5, event.Witnessed},
		{"the minute just before the outage, whose batch waited through it, is offline", true, 59.5, event.Offline},
		{"a minute in the outage's first hour is offline", true, 90.5, event.Offline},
		{"a minute in its second hour is offline", true, 150.5, event.Offline},
		{"a minute whose batch reached the server within the lag, even in the catch-up, is witnessed", true, 170.5, event.Witnessed},
		{"a minute after the server came back is witnessed", true, 200.5, event.Witnessed},
		{"the same outage without notes is backfilled", false, 90.5, event.Backfilled},
		{"the minute before it, without notes, is backfilled too", false, 59.5, event.Backfilled},
		{"without notes, a minute whose batch arrived within the lag is still witnessed", false, 170.5, event.Witnessed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := outage(tc.notes)
			res := Records(table(t), c.batches, c.recs)
			i := c.find(tc.minute)
			if i < 0 {
				t.Fatalf("no record of minute %v", tc.minute)
			}
			got := res.Readings[i]
			if got.Label != tc.want {
				t.Errorf("reads %q, want %q (%+v)", got.Label, tc.want, got)
			}
			if (got.Stretch >= 0) != (tc.want == event.Offline) {
				t.Errorf("stretch %d for a %q record", got.Stretch, got.Label)
			}
		})
	}
	t.Run("the outage is one stretch, from the reconnect, over the batches that waited", func(t *testing.T) {
		c := outage(true)
		res := Records(table(t), c.batches, c.recs)
		// The last batch late by more than the lag is the one sealed at
		// minute 165, whose record of minute 164.5 arrived 16 minutes on.
		want := Stretch{Reconnect: at(180.5), First: 60, Last: 165, From: at(59.5), To: at(164.5)}
		if len(res.Stretches) != 1 || res.Stretches[0] != want {
			t.Errorf("stretches %+v, want %+v", res.Stretches, want)
		}
	})
	t.Run("the same outage without notes has no stretch", func(t *testing.T) {
		c := outage(false)
		if res := Records(table(t), c.batches, c.recs); len(res.Stretches) != 0 {
			t.Errorf("stretches %+v", res.Stretches)
		}
	})
}

// TestOfflineConditions checks DESIGN 7.4's conditions for offline one at
// a time: a late record that misses one is backfilled.
func TestOfflineConditions(t *testing.T) {
	tests := []struct {
		name  string
		build func() (*chain, int)
		want  event.Witness
		// reconnect is the reconnect of an offline record's stretch, and
		// stretches how many stretches there are.
		reconnect int64
		stretches int
	}{
		{"a failure only at the end of a stretch of sending nothing doesn't make it offline",
			func() (*chain, int) {
				c := outage(false)
				// One failure, just before the device came back.
				c.add(181, note(179.8))
				return c, c.find(90.5)
			}, event.Backfilled, 0, 0},
		{"an outage that began before the server held any batch of the device is backfilled",
			func() (*chain, int) {
				c := outage(true)
				// The chain the server holds starts at batch 60, as after a
				// first sign-in.
				c.drop(1, 59)
				return c, c.find(90.5)
			}, event.Backfilled, 0, 0},
		{"an outage right after a local-only gap is backfilled",
			func() (*chain, int) {
				c := outage(true)
				c.drop(40, 59)
				return c, c.find(90.5)
			}, event.Backfilled, 0, 0},
		{"a record written into a batch long after it happened is backfilled, notes or not",
			func() (*chain, int) {
				c := outage(true)
				// A record of minute 30 that the agent only wrote at minute 99.
				// The outage around it is a stretch still.
				return c, c.add(100, Record{Type: presence, Timed: true, T: at(30.25)})
			}, event.Backfilled, 0, 1},
		{"a backlog that reached the server in two catch-ups makes a stretch of each, from its own reconnect",
			func() (*chain, int) {
				c := twoCatchUps()
				return c, c.find(125.5)
			}, event.Offline, at(200.5), 2},
		{"a batch of the catch-up that came within the lag after the reconnect is offline",
			func() (*chain, int) {
				c := outage(true)
				// After batch 120 the backlog went one batch every 30 s.
				c.receive(121, 180, 181, 0.5)
				c.drop(181, 240)
				return c, c.find(139.5)
			}, event.Offline, at(180.5), 1},
		{"records dated long before the batch before theirs closed read backfilled, whatever notes their batch holds",
			func() (*chain, int) {
				// The device got a batch through every minute to minute 120.
				// Then, after 30 minutes of silence, a batch of records dated
				// 10 to 24 and a note of 24.5, as if they had waited all along.
				var c chain
				for m := 1.0; m <= 120; m++ {
					c.batch(m, ev(m-0.5, m+100, "run-a"))
				}
				var recs []Record
				for m := 10.0; m <= 24; m++ {
					recs = append(recs, Record{Type: presence, Timed: true, T: at(m)})
				}
				return &c, c.batch(150, append(recs, note(24.5))...)
			}, event.Backfilled, 0, 0},
		{"a catch-up whose live batch before it comes without its records reads backfilled",
			func() (*chain, int) {
				// As above, but the caller gives the records of batch 121
				// alone, and the rows of batches 120 and 121, as a reveal of
				// the window the records claim would hold.
				var c chain
				for m := 1.0; m <= 120; m++ {
					c.batch(m, ev(m-0.5, m+100, "run-a"))
				}
				var recs []Record
				for m := 10.0; m <= 24; m++ {
					recs = append(recs, Record{Type: presence, Timed: true, T: at(m)})
				}
				first := c.batch(150, append(recs, note(24.5))...)
				c.recs = c.recs[first:]
				c.drop(1, 119)
				return &c, 0
			}, event.Backfilled, 0, 0},
		{"a catch-up whose batch before it came live and reads only backfilled doesn't wait out an outage",
			func() (*chain, int) {
				// Batch 120 holds only a record of minute 100, 20 minutes
				// late, so nothing shows it closed in time. The catch-up at
				// minute 150 holds records of 121 to 135 and a note of 135.5,
				// which pass every other condition.
				var c chain
				for m := 1.0; m <= 119; m++ {
					c.batch(m, ev(m-0.5, m+100, "run-a"))
				}
				c.batch(120, Record{Type: presence, Timed: true, T: at(100)})
				var recs []Record
				for m := 121.0; m <= 135; m++ {
					recs = append(recs, Record{Type: presence, Timed: true, T: at(m)})
				}
				return &c, c.batch(150, append(recs, note(135.5))...)
			}, event.Backfilled, 0, 0},
		{"a backlog that reached the server in two catch-ups reads offline from what rule (g) reveals of a minute in the second",
			func() (*chain, int) {
				c := twoCatchUps()
				// A submission of minutes 170 to 180 reveals batches 171 to
				// 181, their notes, and the presence of batch 120 before the
				// second catch-up and of batch 59 before the first. No
				// revealed note is near batch 120's close, so its own record
				// reads backfilled, and the second catch-up is the one
				// stretch.
				c.only(func(b uint64) bool { return b >= 171 && b <= 181 || b == 120 || b == 59 })
				return c, c.find(170.5)
			}, event.Offline, at(200.5), 1},
		{"the same backlog reads backfilled throughout when the first catch-up's anchor reads backfilled",
			func() (*chain, int) {
				c := twoCatchUps()
				c.only(func(b uint64) bool { return b >= 171 && b <= 181 || b == 120 || b == 59 })
				// Batch 59's record is a late one, so nothing anchors the
				// first catch-up, and so nothing anchors the second.
				c.recs[c.find(58.5)].T = at(30)
				return c, c.find(170.5)
			}, event.Backfilled, 0, 0},
		{"a backlog that reached the server in three catch-ups reads offline from what rule (g) reveals",
			func() (*chain, int) {
				c := outage(true)
				c.receive(60, 100, 180.5, 0)
				c.receive(101, 140, 200.5, 0)
				c.receive(141, 200, 220.5, 0)
				c.drop(201, 240)
				// A submission of minutes 150 to 155 reveals batches 151 to
				// 156 and the anchors before each catch-up: 140, 100 and 59.
				// Batch 140's record reads offline too, by the note of 152.2
				// in batch 153, so there are two stretches.
				c.only(func(b uint64) bool { return b >= 151 && b <= 156 || b == 140 || b == 100 || b == 59 })
				return c, c.find(150.5)
			}, event.Offline, at(220.5), 2},
		{"a batch of the present that arrives after a silence and reveals none of its records counts as a catch-up batch, so the silence before it can read offline",
			func() (*chain, int) {
				// Batches 1 to 59 went live. Batch 60 went at minute 180.5,
				// after two hours of silence, with one record of minute 180
				// that no reveal rule opens, so recs leaves it out. Batch 61
				// went at minute 300 with records of minutes 60 to 70 and a
				// note of 70.5. With batch 60's record passed, they would read
				// backfilled.
				var c chain
				for m := 1.0; m <= 59; m++ {
					c.batch(m+0.05, ev(m-0.5, m+100, "run-a"))
				}
				c.batch(180.5)
				return &c, c.batch(300, append(presences(60, 70), note(70.5))...)
			}, event.Offline, at(300), 1},
		{"a batch that reveals none of its records and came within the lag after the batch before it resumed no run, so it anchors nothing",
			func() (*chain, int) {
				// As above, but batch 60 went live, at minute 60.05.
				var c chain
				for m := 1.0; m <= 59; m++ {
					c.batch(m+0.05, ev(m-0.5, m+100, "run-a"))
				}
				c.batch(60.05)
				return &c, c.batch(300, append(presences(60, 70), note(70.5))...)
			}, event.Backfilled, 0, 0},
		{"a backlog that reached the server in two catch-ups reads offline from the rows Records asks for, though the batch before the second holds no presence, focus or power record",
			func() (*chain, int) {
				c := twoCatchUps()
				// A submission of minutes 170 to 180 reveals batches 171 to
				// 181 and batch 59's presence. Batch 120 holds no presence,
				// focus or power record, so rule (g) opens nothing of it. The
				// rows are exactly the contract's: every row from batch 59,
				// the one before the chain's first catch-up, on.
				c.only(func(b uint64) bool { return b >= 171 && b <= 181 || b == 59 })
				c.drop(1, 58)
				return c, c.find(170.5)
			}, event.Offline, at(200.5), 1},
		{"a record dated while the server took in an earlier catch-up can read offline in a later one",
			func() (*chain, int) {
				// Batches 60 to 100 went one every 15 s from minute 180.5,
				// so the server had batch 80 at 185.5. Then the device dropped
				// again, and the rest went at minute 215. The record of 185.5
				// waited for the second catch-up.
				c := outage(true)
				c.receive(60, 100, 180.5, 0.25)
				c.receive(101, 200, 215, 0)
				c.drop(201, 240)
				return c, c.find(185.5)
			}, event.Offline, at(215), 2},
		{"the bound on what reads offline counts from the witnessed record's corrected time, not its date",
			func() (*chain, int) {
				// Batch 59's record is dated 61.5, on a clock 2.45 minutes
				// fast, so it reads by its batch's receipt, 59.05. A record
				// dated 45, 16.5 minutes before that date but 14.05 before
				// the corrected time, went at minute 300 with a note of 46.
				var c chain
				for m := 1.0; m <= 58; m++ {
					c.batch(m+0.05, ev(m-0.5, m+100, "run-a"))
				}
				c.batch(59.05, Record{Type: presence, Timed: true, T: at(61.5)})
				return &c, c.batch(300, Record{Type: presence, Timed: true, T: at(45)}, note(46))
			}, event.Offline, at(300), 1},
		{"the last minutes of an outage that ends asleep are offline by the failures just before them",
			func() (*chain, int) {
				// The uploader failed every four minutes from minute 60 to
				// 88.2, then waited, and the lid closed at 95 before its next
				// try. Woken at minute 600, it caught up at once.
				var c chain
				for m := 1.0; m <= 95; m++ {
					recs := []Record{ev(m-0.5, m+100, "run-a")}
					if f := m - 0.8; f >= 60 && f <= 89 && int(f-60)%4 == 0 {
						recs = append(recs, note(f))
					}
					got := m + 0.05
					if m >= 60 {
						got = 600.5
					}
					c.batch(got, recs...)
				}
				return &c, c.find(94.5)
			}, event.Offline, at(600.5), 1},
		{"a failure from an outage the device had caught up from doesn't make a later stretch offline",
			func() (*chain, int) {
				// Failures from minute 60 to 76.2 and a catch-up at 80.5, then
				// live until minute 90, then batches held without a failure
				// until minute 200.
				var c chain
				for m := 1.0; m <= 120; m++ {
					recs := []Record{ev(m-0.5, m+100, "run-a")}
					if f := m - 0.8; f >= 60 && f <= 77 && int(f-60)%4 == 0 {
						recs = append(recs, note(f))
					}
					got := m + 0.05
					switch {
					case m >= 60 && m <= 80:
						got = 80.5
					case m >= 91:
						got = 200
					}
					c.batch(got, recs...)
				}
				return &c, c.find(90.5)
			}, event.Backfilled, 0, 1},
		{"a batch that came more than the lag after the device got through again isn't part of the catch-up",
			func() (*chain, int) {
				c := outage(true)
				c.receive(121, 180, 181, 0.5)
				c.drop(181, 240)
				// Batch 160 went at minute 200.5, 20 minutes after the
				// reconnect.
				return c, c.find(159.5)
			}, event.Backfilled, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, i := tc.build()
			if i < 0 {
				t.Fatal("the case names no record")
			}
			res := Records(table(t), c.batches, c.recs)
			got := res.Readings[i]
			if got.Label != tc.want {
				t.Fatalf("reads %q, want %q (%+v)", got.Label, tc.want, got)
			}
			if tc.reconnect != 0 && (got.Stretch < 0 || res.Stretches[got.Stretch].Reconnect != tc.reconnect) {
				t.Errorf("stretch %d of %+v, want one that reconnects at %d", got.Stretch, res.Stretches, tc.reconnect)
			}
			if len(res.Stretches) != tc.stretches {
				t.Errorf("%d stretches, want %d: %+v", len(res.Stretches), tc.stretches, res.Stretches)
			}
		})
	}
}

// TestOfflineCost holds offline's note checks to the size of the input:
// many notes from just before the device's last live receipt, then a
// catch-up of as many batches of one record each, every one of which
// looks for a note in a window the old notes fill. A check that scanned
// the window for each record, or built it again for each batch, reads a
// note per note per record, which the bound fails.
func TestOfflineCost(t *testing.T) {
	const n = 20_000
	var c chain
	recs := []Record{ev(100, 200, "run-a")}
	for k := range n {
		recs = append(recs, note(86+14*float64(k)/n))
	}
	c.batch(100.05, recs...)
	first := len(c.recs)
	for k := range n {
		c.batch(120, Record{Type: presence, Timed: true, T: at(100) + int64(k)})
	}
	res, reads := records(table(t), c.batches, c.recs)
	batches := len(c.batches)
	tests := []struct {
		name string
		ok   bool
	}{
		{"the notes' checks read a note at most six times the notes plus three times the batches", reads <= 6*n+3*batches},
		{"the catch-up's records read backfilled, since every note in their window is from before the catch-up",
			res.Readings[first].Label == event.Backfilled && res.Readings[len(res.Readings)-1].Label == event.Backfilled},
		{"the notes before the silence read witnessed", res.Readings[1].Label == event.Witnessed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: %d reads for %d notes and %d batches", reads, n, batches)
			}
		})
	}
}
