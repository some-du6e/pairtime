// SPDX-License-Identifier: AGPL-3.0-or-later

// Package projectname is what pairtime calls an owner's projects, on the
// server and on the computer alike, so the two agree: the short code of a
// project without a name, and the label an owner shows on their own
// dashboard (D-owner-names, docs/spec/project-names-v1.md).
//
// The server never learns a project's name. A project's key is an opaque
// HMAC (DESIGN 4.3), and the dashboard calls the project by a short code
// cut from it, "Project 7c41e9", which `pairtime projects` shows too. An
// owner can turn a project's name on, one project at a time, on the
// computer: then a label they saw and approved, cleaned as the sign-in's
// display name is, shows on their own dashboard in the code's place.
package projectname

import (
	"slices"
	"strings"
	"unicode"
)

// MaxLabel is the most characters, Unicode code points, a label keeps:
// the display name's 64 (D-display-name).
const MaxLabel = 64

// CleanLabel makes s fit to show as a project's name, the way the server
// cleans a sign-in's display name (D-display-name; internal/server/auth's
// cleanName): each run of spaces, line breaks and control characters
// becomes one space, the ends are trimmed, and formatting characters are
// dropped, such as the bidirectional controls that could reorder or hide
// the text after the name, and the invisible ones. The zero-width joiner
// and non-joiner stay, because scripts that join letters and emoji
// sequences need them. At most MaxLabel characters stay. The client cleans
// a label before the owner sees it, so the one it sends is the one they
// approved.
func CleanLabel(s string) string {
	var out []rune
	gap := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r) || unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp):
			gap = len(out) > 0
			continue
		case unicode.Is(unicode.Cf, r) && r != '\u200c' && r != '\u200d':
			continue
		}
		if gap {
			out = append(out, ' ')
			gap = false
		}
		out = append(out, r)
		if len(out) >= MaxLabel {
			break
		}
	}
	return strings.TrimRight(string(out[:min(len(out), MaxLabel)]), " ")
}

// ValidLabel reports whether s is a label the server takes: valid UTF-8,
// not empty, and already clean, so that cleaning it changes nothing
// (docs/spec/project-names-v1.md 1.3). The server refuses a label
// cleaning would change rather than store one the owner didn't see.
func ValidLabel(s string) bool {
	return s != "" && CleanLabel(s) == s
}

// CodeLen is how many characters a project's short code has, unless
// another of the account's projects shares them.
const CodeLen = 6

// ShortCodes gives each of keys, the device keys an account's projects
// are called by, the short code the owner's dashboard and `pairtime
// projects` call its project by, since the server never learns a
// project's name unless the owner shows it. A code is the first CodeLen characters of the key after
// its kind prefix: the git adapter's keys start with a letter that says
// what kind of identity the key came from (g for a repo's root commits, d
// for a folder), and 32 hex digits, so "g7c41e9…" has the code "7c41e9".
// A key of any other form gives its own first characters. Two keys whose
// codes would be the same both get longer ones, a character at a time,
// until they differ, and no other key's code changes; keys that differ
// only in their kind prefix keep it, and so does any key whose code would
// still be another's. So every key's code is unique among keys. Keys are
// opaque HMACs under a device secret (DESIGN 4.3), so a code says nothing
// about its project either.
func ShortCodes(keys []string) map[string]string {
	codes := make(map[string]string, len(keys))
	var assign func(group []string, n int)
	assign = func(group []string, n int) {
		byCode := make(map[string][]string, len(group))
		for _, k := range group {
			body := codeBody(k)
			c := body[:min(n, len(body))]
			byCode[c] = append(byCode[c], k)
		}
		for c, ks := range byCode {
			whole := true
			for _, k := range ks {
				whole = whole && len(codeBody(k)) <= n
			}
			switch {
			case len(ks) == 1:
				codes[ks[0]] = c
			case whole:
				// The same body under two kind prefixes: only the
				// prefix tells them apart.
				for _, k := range ks {
					codes[k] = k
				}
			default:
				assign(ks, n+1)
			}
		}
	}
	assign(slices.Compact(slices.Sorted(slices.Values(keys))), CodeLen)
	// A key that fell back to itself can equal the code cut from a longer
	// key of another form, as "d" and 32 d's does the first 33 characters
	// of 34 d's. Every key whose code another key also has gets its full
	// key instead, until no code repeats. Keys are distinct, so that ends.
	for {
		owners := make(map[string][]string, len(codes))
		for k, c := range codes {
			owners[c] = append(owners[c], k)
		}
		shared := false
		for _, ks := range owners {
			if len(ks) < 2 {
				continue
			}
			for _, k := range ks {
				if codes[k] != k {
					codes[k], shared = k, true
				}
			}
		}
		if !shared {
			return codes
		}
	}
}

// codeBody is what a key's short code is cut from: one of the git
// adapter's keys without its kind prefix, or any other key whole.
func codeBody(key string) string {
	if len(key) != 33 || key[0] < 'a' || key[0] > 'z' {
		return key
	}
	for i := 1; i < len(key); i++ {
		if c := key[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return key
		}
	}
	return key[1:]
}

// CodeName is what the owner's dashboard calls a project with the short
// code code, when the owner shows no name for it: "Project 7c41e9".
func CodeName(code string) string { return "Project " + code }
