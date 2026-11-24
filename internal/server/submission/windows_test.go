// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestNewR checks the freeze's r: 32 fresh bytes each time, and the
// commitment the lowercase hex of their SHA-256 (DESIGN 7.5 step 2).
func TestNewR(t *testing.T) {
	r1, c1, err := newR()
	if err != nil {
		t.Fatal(err)
	}
	r2, _, err := newR()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "r is 32 bytes", ok: len(r1) == 32},
		{name: "the commitment is the lowercase hex of r's SHA-256", ok: c1 == func() string { s := sha256.Sum256(r1); return hex.EncodeToString(s[:]) }()},
		{name: "two freezes draw two different r", ok: !bytes.Equal(r1, r2)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("r %x, commitment %s", r1, c1)
			}
		})
	}
}

// TestOverlaps checks the overlap of two lists of windows the refusal and
// the flag read (D-sb1-submissions, Q2): two windows overlap when each
// starts before the other ends.
func TestOverlaps(t *testing.T) {
	tests := []struct {
		name string
		a, b []Window
		want bool
	}{
		{name: "a window inside another overlaps it", a: []Window{{10, 20}}, b: []Window{{0, 30}}, want: true},
		{name: "windows that share a stretch overlap", a: []Window{{10, 20}}, b: []Window{{15, 25}}, want: true},
		{name: "windows that only touch at an end don't overlap", a: []Window{{10, 20}}, b: []Window{{20, 30}, {0, 10}}, want: false},
		{name: "a later window of a list can overlap", a: []Window{{0, 10}, {10, 20}, {40, 50}}, b: []Window{{25, 35}, {45, 46}}, want: true},
		{name: "lists that interleave without sharing a stretch don't overlap", a: []Window{{0, 10}, {20, 30}}, b: []Window{{10, 20}, {30, 40}}, want: false},
		{name: "an empty list overlaps nothing", a: nil, b: []Window{{0, 10}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overlaps(tt.a, tt.b); got != tt.want {
				t.Errorf("overlaps(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := overlaps(tt.b, tt.a); got != tt.want {
				t.Errorf("overlaps(%v, %v) = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

// TestRequestSum checks the canonical form a request id's idempotency
// compares (D-sb1-submissions, Q5): the program, the projects or the
// calling device's keys, and the window or the choice of devlog windows.
func TestRequestSum(t *testing.T) {
	w := &Window{Start: 1000, End: 2000}
	base := requestSum("hard-mode", "", []string{"p-app", "p-web"}, nil, w)
	tests := []struct {
		name string
		sum  string
		same bool
	}{
		{name: "the same request has the same sum", sum: requestSum("hard-mode", "", []string{"p-app", "p-web"}, nil, &Window{Start: 1000, End: 2000}), same: true},
		{name: "a session's and a computer's request for the same projects by id are the same", sum: requestSum("hard-mode", "d1", []string{"p-app", "p-web"}, nil, w), same: true},
		{name: "another program differs", sum: requestSum("stardance", "", []string{"p-app", "p-web"}, nil, w)},
		{name: "other projects differ", sum: requestSum("hard-mode", "", []string{"p-app"}, nil, w)},
		{name: "keys differ from ids with the same text", sum: requestSum("hard-mode", "d1", nil, []string{"p-app", "p-web"}, w)},
		{name: "another window differs", sum: requestSum("hard-mode", "", []string{"p-app", "p-web"}, nil, &Window{Start: 1000, End: 2001})},
		{name: "devlog windows differ from a window", sum: requestSum("hard-mode", "", []string{"p-app", "p-web"}, nil, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.sum == base) != tt.same {
				t.Errorf("sum %s against %s, want same %v", tt.sum, base, tt.same)
			}
		})
	}
	t.Run("one computer's keys differ from another's of the same text", func(t *testing.T) {
		if requestSum("hard-mode", "d1", nil, []string{"k_app"}, w) == requestSum("hard-mode", "d2", nil, []string{"k_app"}, w) {
			t.Error("two computers' keys k_app made one request")
		}
	})
}
