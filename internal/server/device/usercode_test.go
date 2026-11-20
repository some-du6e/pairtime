// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeUserCode(t *testing.T) {
	tests := []struct {
		name  string
		typed string
		want  string // "" means refused
	}{
		{name: "a code as shown is accepted", typed: "WDJB-MJHT", want: "WDJBMJHT"},
		{name: "lower case is accepted", typed: "wdjb-mjht", want: "WDJBMJHT"},
		{name: "a code without its dash is accepted", typed: "WDJBMJHT", want: "WDJBMJHT"},
		{name: "spaces anywhere are ignored", typed: " WDJB MJHT ", want: "WDJBMJHT"},
		{name: "a vowel isn't in the alphabet, so the code is refused rather than guessed at", typed: "WDJB-MJHA", want: ""},
		{name: "a digit is refused", typed: "WDJB-MJH0", want: ""},
		{name: "seven characters are refused", typed: "WDJB-MJH", want: ""},
		{name: "nine characters are refused", typed: "WDJB-MJHTB", want: ""},
		{name: "a character outside ASCII is refused", typed: "WDJB-MJHＴ", want: ""},
		{name: "a very long input is refused before it is read", typed: strings.Repeat("B", 65), want: ""},
		{name: "an empty input is refused", typed: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NormalizeUserCode(tt.typed)
			if ok != (tt.want != "") || got != tt.want {
				t.Errorf("NormalizeUserCode(%q) = %q, %v; want %q", tt.typed, got, ok, tt.want)
			}
		})
	}
}

func TestNewUserCode(t *testing.T) {
	seen := make(map[rune]bool)
	tests := []struct {
		name string
		ok   func(code string) bool
	}{
		{name: "a new code is 8 characters of the base-20 alphabet", ok: func(code string) bool {
			for _, r := range code {
				seen[r] = true
			}
			return len(code) == UserCodeLen && strings.Trim(code, UserCodeAlphabet) == ""
		}},
		{name: "a new code normalizes to itself, so the page finds what the terminal shows", ok: func(code string) bool {
			got, ok := NormalizeUserCode(FormatUserCode(code))
			return ok && got == code
		}},
		{name: "a new code shows as XXXX-XXXX", ok: func(code string) bool {
			f := FormatUserCode(code)
			return len(f) == 9 && f[4] == '-' && f[:4]+f[5:] == code
		}},
	}
	codes := make([]string, 300)
	for i := range codes {
		codes[i] = newUserCode()
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, c := range codes {
				if !tt.ok(c) {
					t.Fatalf("code %q", c)
				}
			}
		})
	}
	t.Run("every letter of the alphabet turns up", func(t *testing.T) {
		for _, r := range UserCodeAlphabet {
			if !seen[r] {
				t.Errorf("%c never appears in %d codes", r, len(codes))
			}
		}
	})
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		takes    int // wrong codes taken first
		refunds  int // right codes among them, given back
		after    time.Duration
		wantTake bool
	}{
		{name: "an account with no wrong codes may try", wantTake: true},
		{name: "an account with 9 wrong codes may try once more", takes: 9, wantTake: true},
		{name: "an account with 10 wrong codes may not try", takes: 10, wantTake: false},
		{name: "a right code given back doesn't count", takes: 10, refunds: 1, wantTake: true},
		{name: "an account with 10 wrong codes may not try a minute before its window ends", takes: 10, after: wrongCodeWindow - time.Minute, wantTake: false},
		{name: "an account may try again once its window has ended", takes: 10, after: wrongCodeWindow, wantTake: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := now
			l := newLimiter(maxWrongCodes, wrongCodeWindow, func() time.Time { return at })
			for range tt.takes {
				l.take("a1")
			}
			for range tt.refunds {
				l.refund("a1")
			}
			at = at.Add(tt.after)
			if got := l.take("a1"); got != tt.wantTake {
				t.Errorf("take = %v, want %v", got, tt.wantTake)
			}
			if !l.take("a2") {
				t.Error("another account's wrong codes counted against a2")
			}
		})
	}
	t.Run("tries sent at once get at most 10 through", func(t *testing.T) {
		l := newLimiter(maxWrongCodes, wrongCodeWindow, func() time.Time { return now })
		var wg sync.WaitGroup
		var passed atomic.Int64
		for range 100 {
			wg.Go(func() {
				if l.take("a1") {
					passed.Add(1)
				}
			})
		}
		wg.Wait()
		if n := passed.Load(); n != maxWrongCodes {
			t.Errorf("%d of 100 tries at once got through, want %d", n, maxWrongCodes)
		}
	})
	t.Run("a full limiter drops ended windows first, and stays bounded", func(t *testing.T) {
		at := now
		l := newLimiter(maxWrongCodes, wrongCodeWindow, func() time.Time { return at })
		for i := range maxTallies {
			l.take("a" + strconv.Itoa(i))
		}
		at = at.Add(wrongCodeWindow)
		l.take("new")
		if len(l.tries) != 1 {
			t.Errorf("%d tallies after every old window ended, want 1", len(l.tries))
		}
		for i := range maxTallies + 100 {
			l.take("b" + strconv.Itoa(i))
		}
		if len(l.tries) > maxTallies {
			t.Errorf("%d tallies, more than %d", len(l.tries), maxTallies)
		}
	})
}
