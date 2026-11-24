// SPDX-License-Identifier: AGPL-3.0-or-later

package shadow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

func TestFrozen(t *testing.T) {
	tests := []struct {
		name string
		v    string
		want bool
	}{
		{name: "a 0.x version is still calibrating", v: "0.4.0"},
		{name: "the server's version today is still calibrating", v: "0.6.0"},
		{name: "the last 0.x is still calibrating", v: "0.99.9"},
		{name: "1.0.0, the freeze, ends it", v: "1.0.0", want: true},
		{name: "a later 1.x ends it", v: "1.2.3", want: true},
		{name: "a MAJOR of two digits ends it", v: "10.0.0", want: true},
		{name: "a MAJOR with a leading zero is still the MAJOR", v: "01.0.0", want: true},
		{name: "two parts aren't a version, so the flag keeps hiding", v: "1.0"},
		{name: "a prerelease of four dotted parts isn't a version the freeze tags, so the flag keeps hiding", v: "1.0.0-rc.1"},
		{name: "a prerelease in the third part isn't digits, so the flag keeps hiding", v: "1.0.0-rc"},
		{name: "build metadata in the third part isn't digits, so the flag keeps hiding", v: "1.0.0+build"},
		{name: "a v before the MAJOR isn't digits, so the flag keeps hiding", v: "v1.0.0"},
		{name: "a MAJOR of zeros is 0, so the flag keeps hiding", v: "00.6.0"},
		{name: "no version keeps hiding", v: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Frozen(tt.v); got != tt.want {
				t.Errorf("Frozen(%q) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

func TestHides(t *testing.T) {
	ctx := context.Background()
	since := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		on      bool
		scoring string
		want    bool
	}{
		{name: "an account without the flag shows its time", scoring: "0.6.0"},
		{name: "an account with the flag hides its time while the server scores below 1.0.0", on: true, scoring: "0.6.0", want: true},
		{name: "an account with the flag shows its time once the server scores at 1.0.0", on: true, scoring: "1.0.0"},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := storetest.Open(t, engine)
				ms, err := migrations.Embedded()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
					t.Fatal(err)
				}
				var flag any
				if tt.on {
					flag = since.UnixMilli()
				}
				if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms, calibrating_since_ms)
VALUES ('u1', 'sub-u1', 'verified', 0, 0, 0, $1)`, flag); err != nil {
					t.Fatal(err)
				}
				got, err := Hides(ctx, db.Read, "u1", tt.scoring)
				if err != nil || got != tt.want {
					t.Errorf("Hides = %v, %v; want %v", got, err, tt.want)
				}
				at, on, err := Flag(ctx, db.Read, "u1")
				if err != nil || on != tt.on || (on && !at.Equal(since)) {
					t.Errorf("Flag = %v, %v, %v; want %v since %v", at, on, err, tt.on, since)
				}
				if _, err := Hides(ctx, db.Read, "nobody", "0.6.0"); !errors.Is(err, rollup.ErrNoAccount) {
					t.Errorf("Hides of an account that isn't there = %v, want rollup.ErrNoAccount", err)
				}
			})
		}
	}
}

// TestClear checks that Clear turns every account's calibration flag off
// once the server scores at 1.0.0, counting them, and changes nothing
// before then (DESIGN 4.4).
func TestClear(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		scoring string
		// twice runs Clear a second time, whose count is checked.
		twice bool
		want  int64
		// on says whether the flags that were on stay on.
		on bool
	}{
		{name: "below 1.0.0 Clear changes nothing", scoring: "0.6.0", want: 0, on: true},
		{name: "at 1.0.0 Clear turns every flag off and counts them", scoring: "1.0.0", want: 2},
		{name: "a second Clear at 1.0.0 finds none left", scoring: "1.0.0", twice: true, want: 0},
		{name: "after 1.0.0 Clear turns them off too", scoring: "1.4.2", want: 2},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := storetest.Open(t, engine)
				ms, err := migrations.Embedded()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
					t.Fatal(err)
				}
				for _, a := range []struct {
					id   string
					flag any
				}{{"u1", int64(1)}, {"u2", nil}, {"u3", int64(2)}} {
					if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms, calibrating_since_ms)
VALUES ($1, $2, 'verified', 0, 0, 0, $3)`, a.id, "sub-"+a.id, a.flag); err != nil {
						t.Fatal(err)
					}
				}
				got, err := Clear(ctx, db.Write, tt.scoring)
				if tt.twice && err == nil {
					got, err = Clear(ctx, db.Write, tt.scoring)
				}
				if err != nil || got != tt.want {
					t.Errorf("Clear = %d, %v; want %d", got, err, tt.want)
				}
				for _, id := range []string{"u1", "u3"} {
					if _, on, err := Flag(ctx, db.Read, id); err != nil || on != tt.on {
						t.Errorf("%s's flag is on: %v (%v), want %v", id, on, err, tt.on)
					}
				}
				if _, on, err := Flag(ctx, db.Read, "u2"); err != nil || on {
					t.Errorf("u2's flag, never on, is on: %v (%v)", on, err)
				}
			})
		}
	}
}
