// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"regexp"
)

// ownRules are pairtime's own secret rules, for secrets beginners paste
// that gitleaks v8.30.1 has no rule for. Each removes its first group, and
// its id never names a gitleaks rule (TestOwnRuleIDs).
var ownRules = []struct {
	id string
	re *regexp.Regexp
}{
	// The password of a URL such as postgres://app:PASSWORD@localhost:5432.
	// gitleaks only finds one when a keyword such as "password" comes
	// before it, and the email pattern misses it when the host has no dot
	// or ends in a number. The user may be empty, as in redis://:pw@cache,
	// and the password may hold an @: it runs to the last one before the
	// host, so p@ss doesn't leave ss behind.
	{"url-password", regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/:@'"]*:([^\s/'"]+)@`)},
	// The credential of an Authorization header, as written outside a curl
	// command, where gitleaks' curl-auth-header doesn't look: in HTTP,
	// "Authorization: Bearer ...", or in JSON, quoted around the colon.
	{"authorization-header", regexp.MustCompile(`(?i)\bauthorization[ \t]*["']?[ \t]*[:=][ \t]*["']?[ \t]*(?:basic|bearer|token)[ \t]+([\w.~+/=-]+)`)},
}

// ownSecrets calls emit for every match of ownRules in text.
func ownSecrets(text string, emit func(start, end int, rule string)) {
	for _, o := range ownRules {
		for _, m := range o.re.FindAllStringSubmatchIndex(text, -1) {
			if m[2] >= 0 && m[2] < m[3] {
				emit(m[2], m[3], o.id)
			}
		}
	}
}
