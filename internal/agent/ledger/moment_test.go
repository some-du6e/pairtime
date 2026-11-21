// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestMoment checks how an event is held to the moment config.toml was
// last known to say local-only: by the monotonic clock it carries when the
// moment holds that clock too, so a step of the wall clock either way
// can't move it across the moment, and by its t otherwise; and how
// moments combine.
func TestMoment(t *testing.T) {
	const boot = "b_os"
	m := moment{wall: 10_000, run: 5_000, os: 7_000, boot: boot}
	before := []struct {
		name string
		e    event.Envelope
		want bool
	}{
		{"an event of another boot whose t is before the moment was made before it",
			event.Envelope{T: 9_999, BootID: "b_other", Mono: 1}, true},
		{"an event of another boot whose t is the moment's was made once config.toml said online",
			event.Envelope{T: 10_000, BootID: "b_other", Mono: 1}, false},
		{"an event of this run whose mono is before the moment's was made before it, though a step of the wall clock put its t after",
			event.Envelope{T: 20_000, BootID: runBoot, Mono: 4_999}, true},
		{"an event of this run whose mono is the moment's or later wasn't, though a step of the wall clock put its t before",
			event.Envelope{T: 9_000, BootID: runBoot, Mono: 5_000}, false},
		{"a hook call of the moment's boot whose mono is before the moment's was made before it, though its t is after",
			event.Envelope{T: 20_000, BootID: boot, Mono: 6_999}, true},
		{"a hook call of the moment's boot whose mono is the moment's or later wasn't, though its t is before",
			event.Envelope{T: 9_000, BootID: boot, Mono: 7_000}, false},
		{"a hook call of another boot goes by its t alone",
			event.Envelope{T: 20_000, BootID: "b_older", Mono: 1}, false},
		{"a hook call with no mono goes by its t alone",
			event.Envelope{T: 9_000, BootID: boot}, true},
	}
	for _, c := range before {
		t.Run(c.name, func(t *testing.T) {
			if got := m.before(c.e); got != c.want {
				t.Errorf("before(%+v) = %v, want %v", c.e, got, c.want)
			}
		})
	}
	noRun := moment{wall: 10_000, os: 7_000, boot: boot}
	t.Run("an event of this run goes by its t alone when the moment has no reading of this run's clock", func(t *testing.T) {
		if noRun.before(event.Envelope{T: 20_000, BootID: runBoot, Mono: 1}) || !noRun.before(event.Envelope{T: 9_999, BootID: runBoot, Mono: 1}) {
			t.Error("an event of this run was held to the run's clock, which the moment has no reading of")
		}
	})

	raises := []struct {
		name    string
		from, n moment
		want    moment
	}{
		{"each clock moves on to the later reading",
			moment{wall: 10, run: 20, os: 30, boot: boot}, moment{wall: 11, run: 19, os: 31, boot: boot},
			moment{wall: 11, run: 20, os: 31, boot: boot}},
		{"a reading of the OS's clock in another boot replaces the moment's, as the newer",
			moment{wall: 10, os: 30, boot: boot}, moment{wall: 5, os: 2, boot: "b_new"},
			moment{wall: 10, os: 2, boot: "b_new"}},
		{"a moment with no reading of the OS's clock leaves the moment's",
			moment{wall: 10, os: 30, boot: boot}, moment{wall: 20},
			moment{wall: 20, os: 30, boot: boot}},
	}
	for _, c := range raises {
		t.Run(c.name, func(t *testing.T) {
			got := c.from
			got.raise(c.n)
			if got != c.want {
				t.Errorf("raise(%+v) on %+v = %+v, want %+v", c.n, c.from, got, c.want)
			}
		})
	}

	at := time.UnixMilli(testT)
	reads := []struct {
		name string
		s    clock.Stamp
		want moment
	}{
		{"a moment takes the OS's reading with its boot",
			clock.Stamp{Mono: 42, BootID: boot}, moment{wall: testT, run: runMono(at), os: 42, boot: boot}},
		{"a moment takes no reading of the OS's clock when the OS gave none",
			clock.Stamp{}, moment{wall: testT, run: runMono(at)}},
	}
	for _, c := range reads {
		t.Run(c.name, func(t *testing.T) {
			if got := momentAt(at, c.s); got != c.want {
				t.Errorf("momentAt(%v, %+v) = %+v, want %+v", at, c.s, got, c.want)
			}
		})
	}
}
