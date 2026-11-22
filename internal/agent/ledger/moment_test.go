// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"testing"
	"time"

	osclock "github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestMoment checks how an event is held to the moment config.toml was
// last known to say local-only: by the monotonic clock it carries when the
// moment holds that clock too, so a step of the wall clock either way
// can't move it across the moment, and by its t otherwise; how moments
// combine; and how a seal frame's moment reads back.
func TestMoment(t *testing.T) {
	const boot = "b_os"
	m := moment{wall: 10_000, run: 5_000, os: 7_000, boot: boot}
	noRun := moment{wall: 10_000, os: 7_000, boot: boot}
	before := []struct {
		name string
		// at is the moment, m when zero.
		at   moment
		e    event.Envelope
		want bool
	}{
		{"an event of another boot whose t is before the moment was made before it",
			moment{}, event.Envelope{T: 9_999, BootID: "b_other", Mono: 1}, true},
		{"an event of another boot whose t is the moment's was made once config.toml said online",
			moment{}, event.Envelope{T: 10_000, BootID: "b_other", Mono: 1}, false},
		{"an event of this run whose mono is before the moment's was made before it, though a step of the wall clock put its t after",
			moment{}, event.Envelope{T: 20_000, BootID: runBoot, Mono: 4_999}, true},
		{"an event of this run whose mono is the moment's or later wasn't, though a step of the wall clock put its t before",
			moment{}, event.Envelope{T: 9_000, BootID: runBoot, Mono: 5_000}, false},
		{"a hook call of the moment's boot whose mono is before the moment's was made before it, though its t is after",
			moment{}, event.Envelope{T: 20_000, BootID: boot, Mono: 6_999}, true},
		{"a hook call of the moment's boot whose mono is the moment's or later wasn't, though its t is before",
			moment{}, event.Envelope{T: 9_000, BootID: boot, Mono: 7_000}, false},
		{"a hook call of another boot goes by its t alone",
			moment{}, event.Envelope{T: 20_000, BootID: "b_older", Mono: 1}, false},
		{"a hook call with no mono goes by its t alone",
			moment{}, event.Envelope{T: 9_000, BootID: boot}, true},
		{"an event of this run whose t is after a moment with no reading of this run's clock wasn't made before it, though its mono is small",
			noRun, event.Envelope{T: 20_000, BootID: runBoot, Mono: 1}, false},
		{"an event of this run whose t is before a moment with no reading of this run's clock was made before it",
			noRun, event.Envelope{T: 9_999, BootID: runBoot, Mono: 1}, true},
	}
	for _, c := range before {
		t.Run(c.name, func(t *testing.T) {
			at := c.at
			if at == (moment{}) {
				at = m
			}
			if got := at.before(c.e); got != c.want {
				t.Errorf("before(%+v) of %+v = %v, want %v", c.e, at, got, c.want)
			}
		})
	}

	raises := []struct {
		name    string
		from, n moment
		// cur is the boot the writer runs in.
		cur  string
		want moment
	}{
		{"each clock moves on to the later reading",
			moment{wall: 10, run: 20, os: 30, boot: boot}, moment{wall: 11, run: 19, os: 31, boot: boot}, boot,
			moment{wall: 11, run: 20, os: 31, boot: boot}},
		{"a reading of the boot the writer runs in replaces another boot's, as the newest, whatever the wall clock says",
			moment{wall: 10, os: 30, boot: boot}, moment{wall: 5, os: 2, boot: "b_new"}, "b_new",
			moment{wall: 10, os: 2, boot: "b_new"}},
		{"an older boot's reading, though later by the wall clock, doesn't replace one of the boot the writer runs in",
			moment{wall: 10, os: 30, boot: boot}, moment{wall: 20, os: 7, boot: "b_old"}, boot,
			moment{wall: 20, os: 30, boot: boot}},
		{"another boot's reading later by the wall clock replaces one of a boot the writer doesn't run in either",
			moment{wall: 10, os: 30, boot: "b_a"}, moment{wall: 20, os: 7, boot: "b_b"}, boot,
			moment{wall: 20, os: 7, boot: "b_b"}},
		{"another boot's reading earlier by the wall clock leaves the moment's, which is newer",
			moment{wall: 20, os: 30, boot: "b_a"}, moment{wall: 10, os: 7, boot: "b_b"}, boot,
			moment{wall: 20, os: 30, boot: "b_a"}},
		{"an earlier moment with no reading of a clock leaves the moment's",
			moment{wall: 10, run: 20, os: 30, boot: boot}, moment{wall: 5}, boot,
			moment{wall: 10, run: 20, os: 30, boot: boot}},
		{"a later moment with no reading of a clock drops the moment's, which is of an earlier time",
			moment{wall: 10, run: 20, os: 30, boot: boot}, moment{wall: 20}, boot,
			moment{wall: 20}},
		{"with no boot from the OS for the writer, a later moment's reading fills a moment that holds none",
			moment{wall: 10}, moment{wall: 20, os: 7, boot: boot}, "",
			moment{wall: 20, os: 7, boot: boot}},
	}
	for _, c := range raises {
		t.Run(c.name, func(t *testing.T) {
			got := c.from
			got.raise(c.n, c.cur)
			if got != c.want {
				t.Errorf("raise(%+v, %q) on %+v = %+v, want %+v", c.n, c.cur, c.from, got, c.want)
			}
		})
	}

	untils := []struct {
		name string
		sp   sealPayload
		want moment
	}{
		{"a seal frame's moment by the OS's clock comes back with its boot",
			sealPayload{LocalUntil: 10, LocalUntilOS: &untilOS{Mono: 7, Boot: boot}}, moment{wall: 10, os: 7, boot: boot}},
		{"one with no reading of the OS's clock comes back by the wall clock alone",
			sealPayload{LocalUntil: 10}, moment{wall: 10}},
		{"one whose reading has no mono comes back by the wall clock alone",
			sealPayload{LocalUntil: 10, LocalUntilOS: &untilOS{Boot: boot}}, moment{wall: 10}},
		{"one whose reading names no boot comes back by the wall clock alone",
			sealPayload{LocalUntil: 10, LocalUntilOS: &untilOS{Mono: 7}}, moment{wall: 10}},
	}
	for _, c := range untils {
		t.Run(c.name, func(t *testing.T) {
			if got := c.sp.until(); got != c.want {
				t.Errorf("until() of %+v = %+v, want %+v", c.sp, got, c.want)
			}
		})
	}

	wallOnly, now := time.UnixMilli(testT), time.Now()
	reads := []struct {
		name string
		at   time.Time
		s    osclock.Stamp
		want moment
	}{
		{"a moment takes the OS's reading with its boot",
			wallOnly, osclock.Stamp{Mono: 42, BootID: boot}, moment{wall: testT, os: 42, boot: boot}},
		{"a moment takes no reading of the OS's clock when the OS gave none",
			wallOnly, osclock.Stamp{}, moment{wall: testT}},
		{"a moment takes this run's clock from a time with Go's monotonic reading",
			now, osclock.Stamp{}, moment{wall: now.UnixMilli(), run: runMono(now)}},
		{"a moment takes none of this run's clock from a time with no monotonic reading, as one read back from disk",
			now.Round(0), osclock.Stamp{}, moment{wall: now.UnixMilli()}},
	}
	for _, c := range reads {
		t.Run(c.name, func(t *testing.T) {
			if got := momentAt(c.at, c.s); got != c.want {
				t.Errorf("momentAt(%v, %+v) = %+v, want %+v", c.at, c.s, got, c.want)
			}
		})
	}
}

// TestEarlierThan checks which clock orders two moments: this run's when
// both hold it, the OS's of one boot, and the wall clock otherwise.
func TestEarlierThan(t *testing.T) {
	cases := []struct {
		name string
		m, n moment
		want bool
	}{
		{"this run's clock orders two moments that hold it, whatever the wall clock says", moment{wall: 9, run: 1}, moment{wall: 1, run: 2}, true},
		{"the OS's clock of one boot orders two moments that hold it, whatever the wall clock says", moment{wall: 9, os: 1, boot: "b"}, moment{wall: 1, os: 2, boot: "b"}, true},
		{"readings of two boots don't order two moments, the wall clock does", moment{wall: 9, os: 1, boot: "b"}, moment{wall: 1, os: 2, boot: "c"}, false},
		{"the wall clock orders two moments with no clock in common", moment{wall: 1}, moment{wall: 2, os: 5, boot: "b"}, true},
		{"a moment at the same time isn't earlier", moment{wall: 2}, moment{wall: 2}, false},
		{"no moment is earlier than none", moment{wall: 2, run: 1}, moment{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.m.earlierThan(c.n); got != c.want {
				t.Errorf("earlierThan = %v, want %v", got, c.want)
			}
		})
	}
}

// TestEarlier checks the bounds a moment an earlier run recorded takes at
// Open (Writer.earlier): every event of this run, and every hook call of
// this boot when the moment was recorded in another boot, came after it.
func TestEarlier(t *testing.T) {
	const cur = "b_cur"
	cases := []struct {
		name string
		// boot is the boot the writer runs in.
		boot     string
		in, want moment
	}{
		{"a moment of this boot holds this run's events from the run's start, and keeps its reading of the boot",
			cur, moment{wall: 10, os: 7, boot: cur}, moment{wall: 10, run: 1, os: 7, boot: cur}},
		{"a moment of another boot holds this boot's hook calls from the boot's start too",
			cur, moment{wall: 10, os: 7, boot: "b_old"}, moment{wall: 10, run: 1, os: 1, boot: cur}},
		{"a moment by the wall clock alone says nothing of its boot, so it holds this boot's hook calls by their t",
			cur, moment{wall: 10}, moment{wall: 10, run: 1}},
		{"with no boot from the OS, a moment keeps another boot's reading",
			"", moment{wall: 10, os: 7, boot: "b_old"}, moment{wall: 10, run: 1, os: 7, boot: "b_old"}},
		{"no moment stays none", cur, moment{}, moment{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &Writer{boot: c.boot}
			if got := w.earlier(c.in); got != c.want {
				t.Errorf("earlier(%+v) in boot %q = %+v, want %+v", c.in, c.boot, got, c.want)
			}
		})
	}
}
