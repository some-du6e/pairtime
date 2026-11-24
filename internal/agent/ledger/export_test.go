// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"slices"
	"testing"
	"time"
)

// TestExportedStretches checks the local-only stretches the ledger gives a
// reader outside the writer, such as the uploader's commit records
// (D-ag5-D1). ChainInfo has the newest list batches.seg holds, from the
// newest row whose seal frame recorded one or from the base frame a sweep
// wrote, and each sealed Batch has the list its own frame recorded, nil for
// none. ChainInfo also says when the rows lack a stretch the writer has, as
// an older version's rows do, until a seal frame records the list Open
// rebuilt (restoreStretches).
func TestExportedStretches(t *testing.T) {
	// sameList reports whether a and b are the same list. A nil list is the
	// same only as nil, since nil says a frame recorded none.
	sameList := func(a, b []Stretch) bool { return (a == nil) == (b == nil) && slices.Equal(a, b) }
	// twoStretches gives a rig whose writer saw two local-only stretches, as
	// TestStretchesKept's does, then closed. The install's stretch runs
	// from the beginning of time to 10 s. Batch 1 records it going on, and
	// batch 2, sealed online, records its end. Batches 3 and 4 are online
	// with nothing changed, and record no list. The second stretch begins at
	// 30 s, the writer's last online read, and ends at 50 s. Batch 5
	// records it going on and batch 6 its end. Batch 7 is online with
	// nothing changed, and records no list.
	twoStretches := func(t *testing.T) *lateRig {
		r := newLateRig(t)
		r.note(false)
		r.write(r.presence(1000))
		r.take()
		r.at(10_000)
		r.note(true)
		r.pause()
		for _, ms := range []int64{20_000, 25_000, 30_000} {
			r.at(ms)
			r.write(r.presence(ms))
			r.seal()
		}
		r.at(40_000)
		r.note(false)
		r.write(r.presence(41_000))
		r.take()
		r.at(50_000)
		r.note(true)
		r.pause()
		for _, ms := range []int64{60_000, 65_000} {
			r.at(ms)
			r.write(r.presence(ms))
			r.seal()
		}
		if err := r.w.Close(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	cases := []struct {
		name string
		test func(t *testing.T)
	}{
		{"ReadRows gives the list the newest recording row or the base frame holds, and a sealed Batch gives the list its frame recorded", func(t *testing.T) {
			r := twoStretches(t)
			info, rows, err := ReadRows(r.dir, 0)
			if err != nil {
				t.Fatal(err)
			}
			chain, err := ReadChain(r.dir)
			if err != nil {
				t.Fatal(err)
			}
			read := readAll(t, r.dir)
			// A sweep that drops every row, batch 6's with the rest, keeps the
			// writer's list in its base frame, folded into one stretch from the
			// beginning of time to the sweep's day (Sweep).
			r.open()
			day := dayOf(time.UnixMilli(testT + 2*sweepDayMS))
			if dropped, _, err := r.w.Sweep(day, 0); err != nil || dropped != len(r.batches) {
				t.Fatalf("Sweep dropped %d batches, %v; want %d", dropped, err, len(r.batches))
			}
			if err := r.w.Close(); err != nil {
				t.Fatal(err)
			}
			swept, _, err := ReadRows(r.dir, 0)
			if err != nil {
				t.Fatal(err)
			}
			sweptChain, err := ReadChain(r.dir)
			if err != nil {
				t.Fatal(err)
			}
			install := Stretch{To: testT + 10_000}
			// framed is the list each batch's seal frame recorded, nil for none.
			framed := [][]Stretch{
				{{Open: true}},
				{install},
				nil,
				nil,
				{install, {From: testT + 30_000, Open: true}},
				{install, {From: testT + 30_000, To: testT + 50_000}},
				nil,
			}
			newest, folded := framed[5], []Stretch{{To: cutOf(day)}}
			listsOf := func(bs []Batch) [][]Stretch {
				var out [][]Stretch
				for _, b := range bs {
					out = append(out, b.Stretches)
				}
				return out
			}
			var rowLists [][]Stretch
			for _, row := range rows {
				rowLists = append(rowLists, row.Stretches)
			}
			checks := []struct {
				name      string
				got, want [][]Stretch
			}{
				{"ReadRows' ChainInfo has the list of the newest row whose seal frame recorded one, though a later row's recorded none",
					[][]Stretch{info.Stretches}, [][]Stretch{newest}},
				{"ReadChain has the same list", [][]Stretch{chain.Stretches}, [][]Stretch{newest}},
				{"each batch Sealed delivered has the list its own seal frame recorded, nil where the frame recorded none",
					listsOf(r.batches), framed},
				{"ReadBatches reads each batch with the list its seal frame recorded, nil where the frame recorded none",
					listsOf(read), framed},
				{"RowOf gives a row its batch's list, nil for a batch whose frame recorded none",
					[][]Stretch{RowOf(r.batches[5], r.batches[4].Row.Head).Stretches, RowOf(r.batches[6], r.batches[5].Row.Head).Stretches},
					[][]Stretch{newest, nil}},
				{"ReadRows gives no row a list of its own, as batches.seg keeps only the newest once read",
					rowLists, make([][]Stretch, len(framed))},
				{"after a sweep that drops every row, ReadRows' ChainInfo has the list the base frame keeps, one stretch from the beginning of time to the sweep's day",
					[][]Stretch{swept.Stretches}, [][]Stretch{folded}},
				{"and ReadChain has that list too", [][]Stretch{sweptChain.Stretches}, [][]Stretch{folded}},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !slices.EqualFunc(ch.got, ch.want, sameList) {
						t.Errorf("got %+v, want %+v", ch.got, ch.want)
					}
				})
			}
		}},
		{"ChainInfo says the rows lack stretches for an older version's rows with no list and for a rise of local_until after the newest list's row, and stops saying so once a seal records the list", func(t *testing.T) {
			// alwaysOnline gives a rig whose config.toml said online from the
			// chain's start, its writer then closed. None of its three batches
			// saw a stretch, so no seal frame records a list and every
			// local_until is 0.
			alwaysOnline := func(t *testing.T) *lateRig {
				r := newLateRig(t)
				for _, ms := range []int64{10_000, 20_000, 30_000} {
					r.at(ms)
					r.write(r.presence(ms))
					r.seal()
				}
				if err := r.w.Close(); err != nil {
					t.Fatal(err)
				}
				return r
			}
			// untilMoved gives a rig as TestUntilMovesRecordStretches' does, its
			// writer then closed with config.toml still local-only. The
			// install's stretch ends at 10 s, and batch 2 is sealed online.
			// config.toml says local-only again at 30 s, and batch 3 records
			// the stretch going on. The agent then saw a stretch end at 25 s,
			// which that stretch holds, so it ends nothing, but it moves
			// local_until past the list's newest end. Batch 4 records the list
			// with that moment, and batch 5, with nothing changed, records
			// none.
			untilMoved := func(t *testing.T) *lateRig {
				r := newLateRig(t)
				r.note(false)
				r.write(r.presence(1000))
				r.take()
				r.at(10_000)
				r.note(true)
				r.pause()
				r.at(20_000)
				r.write(r.presence(20_000))
				r.seal()
				r.at(30_000)
				r.note(false)
				r.write(r.presence(31_000))
				r.take()
				r.w.NoteSeen(Seen{Ended: time.UnixMilli(testT + 25_000)})
				for _, ms := range []int64{40_000, 45_000} {
					r.at(ms)
					r.write(r.presence(ms))
					r.seal()
				}
				if err := r.w.Close(); err != nil {
					t.Fatal(err)
				}
				return r
			}
			ledgers := []struct {
				name string
				// build leaves a ledger in the rig's dir as the case says, the
				// writer closed.
				build func(t *testing.T) *lateRig
				// behind is whether ChainInfo says the rows lack stretches.
				behind bool
			}{
				{"an older version's rows, whose seal frames record no list, lack the stretches each rise of their local_until ends",
					func(t *testing.T) *lateRig {
						r := twoStretches(t)
						dropStretches(t, r.dir, 1)
						return r
					}, true},
				{"rows an older version sealed after the newest list's row, as after a downgrade, lack the stretch their rise of local_until past the list ends",
					func(t *testing.T) *lateRig {
						// Batch 2's row keeps its list, and batch 6's local_until
						// rises to 50 s.
						r := twoStretches(t)
						dropStretches(t, r.dir, 3)
						return r
					}, true},
				{"so do rows an older version sealed after a list whose last stretch goes on, as after a downgrade and a sign-in there",
					func(t *testing.T) *lateRig {
						// Batch 5's row keeps its list, whose second stretch goes
						// on, and batch 6's local_until rises to 50 s.
						r := twoStretches(t)
						dropStretches(t, r.dir, 6)
						return r
					}, true},
				{"so do rows after a sweep's base frame whose local_until rose past the list and moment the frame keeps, as only an older version's do",
					func(t *testing.T) *lateRig {
						// A sweep drops every row, and its base frame keeps the
						// list, folded at its day, with the writer's moment.
						r := twoStretches(t)
						r.open()
						day := dayOf(time.UnixMilli(testT + 2*sweepDayMS))
						if dropped, _, err := r.w.Sweep(day, 0); err != nil || dropped != len(r.batches) {
							t.Fatalf("Sweep dropped %d batches, %v; want %d", dropped, err, len(r.batches))
						}
						// A third stretch, noted at 3 days and ended 10 s later.
						// The rows of its two batches then lose their lists.
						r.at(3 * sweepDayMS)
						r.note(false)
						r.write(r.presence(3*sweepDayMS + 1000))
						third := r.take()
						r.at(3*sweepDayMS + 10_000)
						r.note(true)
						r.pause()
						r.at(3*sweepDayMS + 20_000)
						r.write(r.presence(3*sweepDayMS + 20_000))
						r.seal()
						if err := r.w.Close(); err != nil {
							t.Fatal(err)
						}
						dropStretches(t, r.dir, third.Row.B)
						return r
					}, true},
				{"the rows of a ledger online since it began lack none, as their local_until never rose, though none records a list",
					alwaysOnline, false},
				{"this version's rows lack none, though the newest records no list", twoStretches, false},
				{"nor do this version's rows whose local_until rose past the list's newest end with the list's own row, as a stretch end the agent saw that ends nothing moves it",
					untilMoved, false},
			}
			for _, c := range ledgers {
				t.Run(c.name, func(t *testing.T) {
					r := c.build(t)
					chainBefore, err := ReadChain(r.dir)
					if err != nil {
						t.Fatal(err)
					}
					before, rows, err := ReadRows(r.dir, 0)
					if err != nil {
						t.Fatal(err)
					}
					// Open rebuilds what the rows lack, and the next seal frame
					// records the list.
					at := r.clk.now().UnixMilli() - testT + 10_000
					r.at(at)
					r.open()
					r.write(r.presence(at + 1000))
					b := r.seal()
					if err := r.w.Close(); err != nil {
						t.Fatal(err)
					}
					chainAfter, err := ReadChain(r.dir)
					if err != nil {
						t.Fatal(err)
					}
					after, _, err := ReadRows(r.dir, 0)
					if err != nil {
						t.Fatal(err)
					}
					// want is the newest list a seal frame recorded, b's when its
					// frame recorded one and the one ChainInfo had before when it
					// didn't.
					want := before.Stretches
					if b.Stretches != nil {
						want = b.Stretches
					}
					// ends is whether b's list ends at the newest row's
					// local_until, so it holds the stretch that rise ended.
					last := rows[len(rows)-1].LocalUntil.UnixMilli()
					n := len(b.Stretches)
					ends := n > 0 && !b.Stretches[n-1].Open && b.Stretches[n-1].To == last
					checks := []struct {
						name string
						ok   bool
					}{
						{"ReadChain says whether the rows lack stretches", chainBefore.StretchesBehind == c.behind},
						{"ReadRows says the same", before.StretchesBehind == c.behind},
						{"where the rows lack them, the first seal after Open records the list Open rebuilt, which ends at the newest row's local_until",
							!c.behind || ends},
						{"after Open and that seal, ReadChain and ReadRows say the rows lack none", !chainAfter.StretchesBehind && !after.StretchesBehind},
						{"and ChainInfo has the newest list a seal frame recorded", sameList(chainAfter.Stretches, want) && sameList(after.Stretches, want)},
					}
					for _, ch := range checks {
						t.Run(ch.name, func(t *testing.T) {
							if !ch.ok {
								t.Errorf("no: behind %v by ReadChain and %v by ReadRows, want %v; the batch after Open recorded %+v; then behind %v and %v, with the list %+v, want %+v; the newest row's local_until %d",
									chainBefore.StretchesBehind, before.StretchesBehind, c.behind, b.Stretches,
									chainAfter.StretchesBehind, after.StretchesBehind, after.Stretches, want, last)
							}
						})
					}
				})
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.test)
	}
}

// TestExportedStretchHolds checks Stretch.Holds, which judges a time by the
// wall clock alone, as Write judges a record with no monotonic reading. A
// stretch holds the times from From to To, both edges included. From 0 is
// the beginning of time, and an open stretch has no end.
func TestExportedStretchHolds(t *testing.T) {
	from, to := testT+10_000, testT+20_000
	cases := []struct {
		name string
		s    Stretch
		at   int64
		want bool
	}{
		{"a time between the edges is held", Stretch{From: from, To: to}, testT + 15_000, true},
		{"so is From's own millisecond", Stretch{From: from, To: to}, from, true},
		{"and To's own", Stretch{From: from, To: to}, to, true},
		{"the millisecond before From is not, as config.toml said online then", Stretch{From: from, To: to}, from - 1, false},
		{"nor is the one after To", Stretch{From: from, To: to}, to + 1, false},
		{"a stretch whose From is 0 holds every time before To, from the beginning of time", Stretch{To: to}, 1, true},
		{"and none after To", Stretch{To: to}, to + 1, false},
		{"an open stretch holds From's own millisecond", Stretch{From: from, Open: true}, from, true},
		{"and every time after it", Stretch{From: from, Open: true}, 1 << 50, true},
		{"but not the millisecond before From", Stretch{From: from, Open: true}, from - 1, false},
		{"an open stretch whose From is 0 holds every time from the beginning of time on", Stretch{Open: true}, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.s.Holds(c.at); got != c.want {
				t.Errorf("%+v.Holds(%d) = %v, want %v", c.s, c.at, got, c.want)
			}
		})
	}
}
