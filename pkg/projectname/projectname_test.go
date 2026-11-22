// SPDX-License-Identifier: AGPL-3.0-or-later

package projectname

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

func TestShortCodes(t *testing.T) {
	// many is 5000 made-up keys in the git adapter's form, and two that
	// share their first six hex digits, so the set has a clash whatever
	// the random ones do.
	gen := rand.New(rand.NewPCG(7, 11))
	many := make([]string, 5000, 5002)
	for i := range many {
		many[i] = fmt.Sprintf("g%016x%016x", gen.Uint64(), gen.Uint64())
	}
	many = append(many, "g0a1b2c3d4e5f60718293a4b5c6d7e8f9", "g0a1b2c4d4e5f60718293a4b5c6d7e8f9")
	tests := []struct {
		name  string
		keys  []string
		want  map[string]string
		check func(*testing.T, map[string]string)
	}{
		{
			name: "a repo's key is called by the six hex digits after its kind prefix",
			keys: []string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85"},
			want: map[string]string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85": "7c41e9"},
		},
		{
			name: "a folder's key leaves out its d prefix too, though d is a hex digit",
			keys: []string{"dd2a8f61c94e07b35a8c1f6e29d4b7a03"},
			want: map[string]string{"dd2a8f61c94e07b35a8c1f6e29d4b7a03": "d2a8f6"},
		},
		{
			name: "two keys whose codes would be the same both get longer ones until they differ, and no other key's code changes",
			keys: []string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85", "g7c41e9b2d05b8f63e1a94c7d2b6f0e85", "g5be03d7f18a6c92e4b07d5a1c8f3e962"},
			want: map[string]string{
				"g7c41e9a2d05b8f63e1a94c7d2b6f0e85": "7c41e9a",
				"g7c41e9b2d05b8f63e1a94c7d2b6f0e85": "7c41e9b",
				"g5be03d7f18a6c92e4b07d5a1c8f3e962": "5be03d",
			},
		},
		{
			name: "codes grow a character at a time, each only as far as its own clash needs",
			keys: []string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85", "g7c41e9a3d05b8f63e1a94c7d2b6f0e85", "g7c41e9b2d05b8f63e1a94c7d2b6f0e85"},
			want: map[string]string{
				"g7c41e9a2d05b8f63e1a94c7d2b6f0e85": "7c41e9a2",
				"g7c41e9a3d05b8f63e1a94c7d2b6f0e85": "7c41e9a3",
				"g7c41e9b2d05b8f63e1a94c7d2b6f0e85": "7c41e9b",
			},
		},
		{
			name: "keys that differ only in their kind prefix keep it, so their codes still differ",
			keys: []string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85", "d7c41e9a2d05b8f63e1a94c7d2b6f0e85"},
			want: map[string]string{
				"g7c41e9a2d05b8f63e1a94c7d2b6f0e85": "g7c41e9a2d05b8f63e1a94c7d2b6f0e85",
				"d7c41e9a2d05b8f63e1a94c7d2b6f0e85": "d7c41e9a2d05b8f63e1a94c7d2b6f0e85",
			},
		},
		{
			name: "a code that would equal a key two others fell back to is the whole key too, so no two projects share one",
			keys: []string{"g" + strings.Repeat("d", 32), "d" + strings.Repeat("d", 32), strings.Repeat("d", 34)},
			want: map[string]string{
				"g" + strings.Repeat("d", 32): "g" + strings.Repeat("d", 32),
				"d" + strings.Repeat("d", 32): "d" + strings.Repeat("d", 32),
				strings.Repeat("d", 34):       strings.Repeat("d", 34),
			},
		},
		{
			name: "a key of another form is called by its own first characters, and a short one by all of it",
			keys: []string{"p_0123456789abcdef", "p1", "G7C41E9A2D05B8F63E1A94C7D2B6F0E85"},
			want: map[string]string{"p_0123456789abcdef": "p_0123", "p1": "p1", "G7C41E9A2D05B8F63E1A94C7D2B6F0E85": "G7C41E"},
		},
		{
			name: "a key listed twice gets one code",
			keys: []string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85", "g7c41e9a2d05b8f63e1a94c7d2b6f0e85"},
			want: map[string]string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85": "7c41e9"},
		},
		{
			name: "among 5002 keys every code is unique and none is the start of another, and a code is longer than six only where another key shares its first six",
			keys: many,
			check: func(t *testing.T, codes map[string]string) {
				if len(codes) != len(many) {
					t.Fatalf("%d codes for %d keys", len(codes), len(many))
				}
				first := make(map[string]int)
				for _, k := range many {
					first[k[1:1+CodeLen]]++
				}
				seen := make(map[string]bool)
				longer := 0
				for k, c := range codes {
					if seen[c] {
						t.Errorf("the code %s is given twice", c)
					}
					seen[c] = true
					if !strings.HasPrefix(k[1:], c) {
						t.Errorf("key %s has the code %s, which isn't its start", k, c)
					}
					if shared := first[k[1:1+CodeLen]] > 1; (len(c) > CodeLen) != shared {
						t.Errorf("key %s has the code %s, though %d keys share its first %d digits", k, c, first[k[1:1+CodeLen]], CodeLen)
					}
					if len(c) > CodeLen {
						longer++
					}
				}
				for c := range seen {
					for n := CodeLen; n < len(c); n++ {
						if seen[c[:n]] {
							t.Errorf("the code %s starts with another code, %s", c, c[:n])
						}
					}
				}
				if longer == 0 {
					t.Error("no two of the keys share their first six digits, so the case checks no clash")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShortCodes(tt.keys)
			if tt.check != nil {
				tt.check(t, got)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ShortCodes(%q) = %v, want %v", tt.keys, got, tt.want)
			}
		})
	}
}

func TestCodeName(t *testing.T) {
	tests := []struct{ name, code, want string }{
		{name: "a project without a name is called by its code, as a name rather than a hash", code: "7c41e9", want: "Project 7c41e9"},
		{name: "a code lengthened on a clash is called the same way", code: "7c41e9a", want: "Project 7c41e9a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeName(tt.code); got != tt.want {
				t.Errorf("CodeName(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestCleanLabel(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{name: "a plain name stays as it is", in: "chip8-emulator", want: "chip8-emulator"},
		{name: "runs of spaces, tabs and line breaks become one space, and the ends are trimmed", in: "  chip8 \t\n emulator \r\n", want: "chip8 emulator"},
		{name: "control characters count as a gap", in: "chip8\x00\x1bemulator", want: "chip8 emulator"},
		{name: "a bidirectional override is dropped, so it can't reorder what follows", in: "chip8\u202eemulator", want: "chip8emulator"},
		{name: "zero-width spaces and other invisible formatting are dropped", in: "chip\u200b8\u2060", want: "chip8"},
		{name: "the zero-width joiner and non-joiner stay, for scripts and emoji that need them", in: "a\u200db\u200cc", want: "a\u200db\u200cc"},
		{name: "line and paragraph separators count as a gap", in: "a\u2028b\u2029c", want: "a b c"},
		{name: "at most 64 characters stay, counted as code points", in: strings.Repeat("é", 70), want: strings.Repeat("é", 64)},
		{name: "a cut that ends in a gap leaves no space at the end", in: strings.Repeat("a", 63) + " b", want: strings.Repeat("a", 63)},
		{name: "nothing but spaces cleans to nothing", in: " \t ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanLabel(tt.in); got != tt.want {
				t.Errorf("CleanLabel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidLabel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "a clean name is valid", in: "chip8-emulator", want: true},
		{name: "64 characters is valid", in: strings.Repeat("é", 64), want: true},
		{name: "an empty label isn't", in: "", want: false},
		{name: "65 characters isn't, though cleaning would cut it", in: strings.Repeat("a", 65), want: false},
		{name: "a label with a space at its end isn't, though cleaning would trim it", in: "chip8 ", want: false},
		{name: "a label with two spaces in a row isn't", in: "chip8  emulator", want: false},
		{name: "a label with a bidirectional override isn't", in: "chip8\u202e", want: false},
		{name: "a label that isn't valid UTF-8 isn't", in: "chip8\xff", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidLabel(tt.in); got != tt.want {
				t.Errorf("ValidLabel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
