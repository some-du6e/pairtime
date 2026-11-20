// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// fakePresence answers like the frameworks would. A missing entry in cg
// means CoreGraphics isn't loaded.
type fakePresence struct {
	cg       map[uint32]float64
	hidNS    int64
	hidOK    bool
	locked   bool
	lockedOK bool
	calls    []string // "keys", "any", "hid", "session", in call order
}

func (f *fakePresence) cgIdle(t uint32) (float64, bool) {
	if t == cgKeyDown {
		f.calls = append(f.calls, "keys")
	} else {
		f.calls = append(f.calls, "any")
	}
	v, ok := f.cg[t]
	return v, ok
}

func (f *fakePresence) hidIdle() (int64, bool) {
	f.calls = append(f.calls, "hid")
	return f.hidNS, f.hidOK
}

func (f *fakePresence) session() (bool, bool) {
	f.calls = append(f.calls, "session")
	return f.locked, f.lockedOK
}

func ptr(v float64) *float64 { return &v }

func TestSamplePresence(t *testing.T) {
	tests := []struct {
		name    string
		src     fakePresence
		want    event.OSPresence
		wantErr error
	}{
		{
			name: "any-input and key-down idle both come from CoreGraphics",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: 2.5, cgKeyDown: 40}, lockedOK: true},
			want: event.OSPresence{IdleAnyS: 2.5, IdleKeysS: ptr(40)},
		},
		{
			name: "key-down idle a little below any-input idle, from the gap between the calls, is raised to it",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: 12.25, cgKeyDown: 12.2}, lockedOK: true},
			want: event.OSPresence{IdleAnyS: 12.25, IdleKeysS: ptr(12.25)},
		},
		{
			name: "idle times are rounded to the millisecond",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: 1.23456, cgKeyDown: 7.0004}, lockedOK: true},
			want: event.OSPresence{IdleAnyS: 1.235, IdleKeysS: ptr(7)},
		},
		{
			name: "without CoreGraphics, any-input idle comes from HIDIdleTime and key-down idle is null",
			src:  fakePresence{hidNS: 3_500_000_000, hidOK: true, lockedOK: true},
			want: event.OSPresence{IdleAnyS: 3.5},
		},
		{
			name: "a negative CoreGraphics idle time falls back to HIDIdleTime and drops key-down idle",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: -1, cgKeyDown: 5}, hidNS: 9_000_000_000, hidOK: true},
			want: event.OSPresence{IdleAnyS: 9},
		},
		{
			name: "a NaN CoreGraphics idle time falls back to HIDIdleTime",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: math.NaN(), cgKeyDown: math.NaN()}, hidNS: 1_000_000, hidOK: true},
			want: event.OSPresence{IdleAnyS: 0.001},
		},
		{
			name: "an infinite key-down idle time is null, not a number",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: 4, cgKeyDown: math.Inf(1)}},
			want: event.OSPresence{IdleAnyS: 4},
		},
		{
			name: "a key-down idle time longer than any uptime is kept as the OS gave it",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: 4, cgKeyDown: 1.8e19}},
			want: event.OSPresence{IdleAnyS: 4, IdleKeysS: ptr(1.8e19)},
		},
		{
			name: "a locked screen is reported",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: 30, cgKeyDown: 30}, locked: true, lockedOK: true},
			want: event.OSPresence{IdleAnyS: 30, IdleKeysS: ptr(30), Locked: true},
		},
		{
			name: "no session dictionary reports unlocked and keeps the idle times",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: 30, cgKeyDown: 31}, locked: true},
			want: event.OSPresence{IdleAnyS: 30, IdleKeysS: ptr(31)},
		},
		{
			name:    "with no idle time from either framework the sample fails instead of guessing",
			src:     fakePresence{cg: map[uint32]float64{cgAnyInput: -5}, hidNS: -1, hidOK: true},
			wantErr: ErrNoIdle,
		},
		{
			name:    "with neither framework loaded the sample fails",
			src:     fakePresence{},
			wantErr: ErrNoIdle,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.src
			got, err := samplePresence(&src)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.IdleAnyS != tt.want.IdleAnyS || got.Locked != tt.want.Locked {
				t.Errorf("got idle_any_s %v locked %v, want %v and %v", got.IdleAnyS, got.Locked, tt.want.IdleAnyS, tt.want.Locked)
			}
			switch {
			case (got.IdleKeysS == nil) != (tt.want.IdleKeysS == nil):
				t.Errorf("idle_keys_s = %v, want %v", fmtPtr(got.IdleKeysS), fmtPtr(tt.want.IdleKeysS))
			case got.IdleKeysS != nil && *got.IdleKeysS != *tt.want.IdleKeysS:
				t.Errorf("idle_keys_s = %v, want %v", *got.IdleKeysS, *tt.want.IdleKeysS)
			}
			// The event model is the oracle for what a sample may hold.
			if err := got.Validate(); err != nil {
				t.Errorf("the sample breaks pkg/event's rules: %v", err)
			}
		})
	}
}

func fmtPtr(p *float64) any {
	if p == nil {
		return "null"
	}
	return *p
}

// TestSampleOrder checks the call order that keeps a key press between two
// calls from making idle_keys_s smaller than idle_any_s.
func TestSampleOrder(t *testing.T) {
	tests := []struct {
		name string
		src  fakePresence
		want []string
	}{
		{
			name: "key-down idle is read before any-input idle, and HIDIdleTime only when CoreGraphics fails",
			src:  fakePresence{cg: map[uint32]float64{cgAnyInput: 1, cgKeyDown: 1}},
			want: []string{"keys", "any", "session"},
		},
		{
			name: "HIDIdleTime is read when CoreGraphics gives no any-input time",
			src:  fakePresence{hidOK: true},
			want: []string{"keys", "any", "hid", "session"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.src
			if _, err := samplePresence(&src); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(src.calls, tt.want) {
				t.Errorf("calls = %q, want %q", src.calls, tt.want)
			}
		})
	}
}
