// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import "strings"

// ownRules are pairtime's own secret rules, for secrets beginners paste
// that gitleaks v8.30.1 has no rule for. Each runs only on text that holds
// its keyword, ignoring case, and removes its first group, and its id
// never names a gitleaks rule (TestOwnRuleIDs).
var ownRules = []struct {
	id, keyword string
	re          *lazyRE
}{
	// The password of a URL such as postgres://app:PASSWORD@localhost:5432.
	// gitleaks only finds one when a keyword such as "password" comes
	// before it, and the email pattern misses it when the host has no dot
	// or ends in a number. The user may be empty, as in redis://:pw@cache,
	// and the password may hold an @: it runs to the last one before the
	// host, so p@ss doesn't leave ss behind.
	{"url-password", "://", lazy(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/:@'"]*:([^\s/'"]+)@`)},
	// The credential of an Authorization header, as written outside a curl
	// command, where gitleaks' curl-auth-header doesn't look: in HTTP,
	// "Authorization: Bearer ...", or in JSON, quoted around the colon.
	{"authorization-header", "authorization", lazy(`(?i)\bauthorization[ \t]*["']?[ \t]*[:=][ \t]*["']?[ \t]*(?:basic|bearer|token)[ \t]+([\w.~+/=-]+)`)},
}

// ownSecrets calls emit for every match of ownRules in text, whose lower
// case is lower.
func ownSecrets(text, lower string, emit func(start, end int, rule string)) {
	for _, o := range ownRules {
		if !strings.Contains(lower, o.keyword) {
			continue
		}
		for _, m := range o.re.get().FindAllStringSubmatchIndex(text, -1) {
			if m[2] >= 0 && m[2] < m[3] {
				emit(m[2], m[3], o.id)
			}
		}
	}
}

var (
	secretKind = lazy(`(?im)^[ \t]*kind:[ \t]*["']?secret["']?[ \t]*(?:#[^\n]*)?\r?$`)
	dataKey    = lazy(`^[ \t]*(?:data|stringData):[ \t]*`)
	entryKey   = lazy(`^(?:[\w.-]+|"[^"]*"|'[^']*')[ \t]*:[ \t]*`)
)

// kubernetesSecretValues calls emit, with gitleaks' kubernetes-secret-yaml
// as the rule, for the value of every entry in the data: and stringData:
// blocks of each Kubernetes Secret in text: a YAML document with a
// kind: Secret line. gitleaks' rule matches a single entry, so it takes
// the first value of a pasted manifest out and leaves the rest, and it
// can't see stringData, which holds values in plain text. Keys stay, so a
// reader can still tell what the manifest held. A line in a block that
// isn't an entry, such as the content of a multi-line value, goes whole.
//
// It reads YAML's own colons, so it runs over text and its JSON view but
// not over a prose view, which blanks a colon before a space.
func kubernetesSecretValues(text string, emit func(start, end int, rule string)) {
	const rule = "kubernetes-secret-yaml"
	if (!strings.Contains(text, "ecret") && !strings.Contains(text, "ECRET")) || !secretKind.get().MatchString(text) {
		return
	}
	// Each document runs from one --- line to the next.
	for _, doc := range yamlDocuments(text) {
		if !secretKind.get().MatchString(text[doc[0]:doc[1]]) {
			continue
		}
		block, entries := -1, -1 // indents of the open data key and of its entries
		for at := doc[0]; at < doc[1]; {
			end := strings.IndexByte(text[at:doc[1]], '\n')
			next := doc[1]
			if end < 0 {
				end = doc[1]
			} else {
				end += at
				next = end + 1
			}
			line := strings.TrimRight(text[at:end], " \t\r")
			content := strings.TrimLeft(line, " \t")
			indent := len(line) - len(content)
			switch {
			case content == "" || strings.HasPrefix(content, "#"):
			case block >= 0 && indent > block:
				if entries < 0 {
					entries = indent
				}
				value := 0 // where the value starts in content
				if m := entryKey.get().FindStringIndex(content); m != nil && indent == entries {
					value = m[1]
				}
				if value < len(content) {
					emit(at+indent+value, at+len(line), rule)
				}
			default:
				block, entries = -1, -1
				if m := dataKey.get().FindStringIndex(line); m != nil {
					if rest := line[m[1]:]; rest != "" && !strings.HasPrefix(rest, "#") {
						// A value on the key's own line, such as {a: b}.
						emit(at+m[1], at+len(line), rule)
					} else {
						block = indent
					}
				}
			}
			at = next
		}
	}
}

// yamlDocuments returns the [start, end) of each YAML document in text,
// split at lines that are ---.
func yamlDocuments(text string) [][2]int {
	var docs [][2]int
	start := 0
	for at := 0; at < len(text); {
		end := strings.IndexByte(text[at:], '\n')
		next := len(text)
		if end < 0 {
			end = len(text)
		} else {
			end += at
			next = end + 1
		}
		if strings.TrimRight(text[at:end], " \t\r") == "---" {
			docs = append(docs, [2]int{start, at})
			start = next
		}
		at = next
	}
	return append(docs, [2]int{start, len(text)})
}
