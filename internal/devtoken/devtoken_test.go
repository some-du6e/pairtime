// SPDX-License-Identifier: AGPL-3.0-or-later

package devtoken

import (
	"errors"
	"strings"
	"testing"
)

// The vectors' checksums were computed with Python's zlib.crc32, not with
// this package, so they check the format against an independent CRC-32.
func TestCheck(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  error
	}{
		{name: "a token of 32 zeros with its checksum passes", token: "ptd_" + strings.Repeat("0", 32) + "0Hhedz"},
		{name: "a checksum whose CRC-32 is small is padded with leading zeros", token: "ptd_" + strings.Repeat("aZ09", 8) + "009kMJ"},
		{name: "the shortest random part, 30 characters, passes", token: "ptd_" + strings.Repeat("x", 30) + "3QlOpq"},
		{name: "the longest random part, 64 characters, passes", token: "ptd_" + strings.Repeat("Q", 64) + "45kTaJ"},
		{name: "one mistyped character fails the checksum", token: "ptd_" + strings.Repeat("0", 31) + "1" + "0Hhedz", want: ErrChecksum},
		{name: "a changed checksum digit fails", token: "ptd_" + strings.Repeat("0", 32) + "0Hhedy", want: ErrChecksum},
		{name: "another prefix is malformed", token: "ptx_" + strings.Repeat("0", 32) + "0Hhedz", want: ErrMalformed},
		{name: "a token cut short is malformed", token: "ptd_" + strings.Repeat("0", 29) + "0Hhedz", want: ErrMalformed},
		{name: "a random part longer than 64 is malformed", token: "ptd_" + strings.Repeat("Q", 65) + "45kTaJ", want: ErrMalformed},
		{name: "a character outside base62 is malformed", token: "ptd_" + strings.Repeat("0", 31) + "-" + "0Hhedz", want: ErrMalformed},
		{name: "an empty string is malformed", token: "", want: ErrMalformed},
		{name: "the prefix alone is malformed", token: "ptd_", want: ErrMalformed},
		{name: "a token with a space at the end is malformed", token: "ptd_" + strings.Repeat("0", 32) + "0Hhedz ", want: ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Check(tt.token); !errors.Is(err, tt.want) {
				t.Errorf("Check(%q) = %v, want %v", tt.token, err, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	const n = 200
	seen := make(map[string]bool, n)
	tokens := make([]string, n)
	for i := range tokens {
		tokens[i] = New()
	}
	tests := []struct {
		name string
		ok   func(tok string) bool
	}{
		{name: "a new token passes Check", ok: func(tok string) bool { return Check(tok) == nil }},
		{name: "a new token is ptd_ and 38 base62 characters", ok: func(tok string) bool {
			return len(tok) == Len && Len == 42 && strings.HasPrefix(tok, Prefix) && strings.Trim(tok[len(Prefix):], base62) == ""
		}},
		{name: "no two new tokens are the same", ok: func(tok string) bool {
			dup := seen[tok]
			seen[tok] = true
			return !dup
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, tok := range tokens {
				if !tt.ok(tok) {
					t.Fatalf("token %q", tok)
				}
			}
		})
	}
	t.Run("every base62 digit turns up in the random part", func(t *testing.T) {
		var all strings.Builder
		for _, tok := range tokens {
			all.WriteString(tok[len(Prefix) : len(tok)-ChecksumLen])
		}
		for _, c := range base62 {
			if !strings.ContainsRune(all.String(), c) {
				t.Errorf("%c never appears in %d tokens", c, n)
			}
		}
	})
}
