// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const presidioDir = "../../testdata/redact/presidio"

// TestPresidioFiles checks the copied Presidio files against SHA256SUMS,
// so the oracle below is upstream's and nobody edited it.
func TestPresidioFiles(t *testing.T) {
	sums, err := os.ReadFile(filepath.Join(presidioDir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != 8 {
		t.Fatalf("SHA256SUMS lists %d files, want 8", len(lines))
	}
	for _, line := range lines {
		want, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("bad SHA256SUMS line %q", line)
		}
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(presidioDir, name))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(b)
			if got := hex.EncodeToString(sum[:]); got != want {
				t.Errorf("SHA-256 is %s, want %s", got, want)
			}
		})
	}
}

// pyParser reads the Python literals in Presidio's test files: strings,
// numbers, booleans and tuples or lists of them, with comments.
type pyParser struct {
	s string
	i int
}

func (p *pyParser) space() {
	for p.i < len(p.s) {
		switch c := p.s[p.i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.i++
		case c == '#':
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

func (p *pyParser) value() (any, error) {
	p.space()
	if p.i >= len(p.s) {
		return nil, fmt.Errorf("unexpected end")
	}
	switch c := p.s[p.i]; {
	case c == '(' || c == '[':
		end := byte(')')
		if c == '[' {
			end = ']'
		}
		p.i++
		items := []any{}
		for {
			p.space()
			if p.i < len(p.s) && p.s[p.i] == end {
				p.i++
				return items, nil
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			items = append(items, v)
			p.space()
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.i < len(p.s) && p.s[p.i] == end {
				p.i++
				return items, nil
			}
			return nil, fmt.Errorf("offset %d: want , or %c", p.i, end)
		}
	case c == '"' || c == '\'':
		var b strings.Builder
		for p.i < len(p.s) && (p.s[p.i] == '"' || p.s[p.i] == '\'') {
			q := p.s[p.i]
			j := p.i + 1
			for j < len(p.s) && p.s[j] != q {
				if p.s[j] == '\\' {
					return nil, fmt.Errorf("offset %d: escapes aren't supported", j)
				}
				b.WriteByte(p.s[j])
				j++
			}
			if j >= len(p.s) {
				return nil, fmt.Errorf("unterminated string")
			}
			p.i = j + 1
			p.space() // "a" "b" is one string in Python
		}
		return b.String(), nil
	case c == '-' || c >= '0' && c <= '9':
		j := p.i + 1
		for j < len(p.s) && (p.s[j] >= '0' && p.s[j] <= '9' || p.s[j] == '.') {
			j++
		}
		tok := p.s[p.i:j]
		p.i = j
		if strings.Contains(tok, ".") {
			return strconv.ParseFloat(tok, 64)
		}
		return strconv.Atoi(tok)
	default:
		j := p.i
		for j < len(p.s) && (p.s[j] == '_' || p.s[j] >= 'a' && p.s[j] <= 'z' || p.s[j] >= 'A' && p.s[j] <= 'Z') {
			j++
		}
		name := p.s[p.i:j]
		p.i = j
		switch name {
		case "True":
			return true, nil
		case "False":
			return false, nil
		}
		return nil, fmt.Errorf("offset %d: unsupported %q", p.i, name)
	}
}

// presidioList parses the Python list that follows marker in a test file.
func presidioList(t *testing.T, file, marker string) []any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(presidioDir, file))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("%s: no %q", file, marker)
	}
	j := strings.IndexByte(s[i:], '[')
	p := &pyParser{s: s, i: i + j}
	v, err := p.value()
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return v.([]any)
}

// pySpans turns ((s, e), ...) into spans of type t.
func pySpans(t *testing.T, v any, typ Type) []Span {
	t.Helper()
	spans := []Span{}
	for _, p := range v.([]any) {
		se := p.([]any)
		spans = append(spans, Span{Start: se[0].(int), End: se[1].(int), Type: typ})
	}
	return spans
}

func collect(find func(string, func(int, int, Type)), text string) []Span {
	spans := []Span{}
	find(text, func(s, e int, typ Type) { spans = append(spans, Span{Start: s, End: e, Type: typ}) })
	return sortSpans(spans)
}

// TestPresidioCases runs each recognizer on the cases in Presidio's own
// tests and wants the spans those tests expect. The one deliberate
// difference is the score floor: a US SSN case Presidio expects only at a
// very weak score (below minScore) must not be redacted here, since none of
// those texts has a context word.
func TestPresidioCases(t *testing.T) {
	type pcase struct {
		text string
		want []Span
	}
	marker := "@pytest.mark.parametrize("
	files := []struct {
		name  string
		file  string
		find  func(string, func(int, int, Type))
		typ   Type
		count int // cases in the file, so a parser slip can't drop some
		cases func(t *testing.T, row []any, typ Type) pcase
	}{
		{"credit card", "test_credit_card_recognizer.py.txt", findCards, TypeCard, 19,
			func(t *testing.T, row []any, typ Type) pcase {
				return pcase{row[0].(string), pySpans(t, row[3], typ)}
			}},
		{"iban", "test_iban_recognizer.py.txt", findIBANs, TypeIBAN, 330,
			func(t *testing.T, row []any, typ Type) pcase {
				return pcase{row[0].(string), pySpans(t, row[2], typ)}
			}},
		{"email", "test_email_recognizer.py.txt", findEmails, TypeEmail, 8,
			func(t *testing.T, row []any, typ Type) pcase {
				return pcase{row[0].(string), pySpans(t, row[2], typ)}
			}},
		{"uk nino", "test_uk_nino_recognizer.py.txt", findNINOs, TypeUKNINO, 16,
			func(t *testing.T, row []any, typ Type) pcase {
				return pcase{row[0].(string), pySpans(t, row[2], typ)}
			}},
		{"in aadhaar", "test_in_aadhaar_recognizer.py.txt", findAadhaars, TypeAadhaar, 13,
			func(t *testing.T, row []any, typ Type) pcase {
				want := []Span{}
				if row[1].(int) > 0 {
					se := row[2].([]any)
					want = append(want, Span{Start: se[0].(int), End: se[1].(int), Type: typ})
				}
				return pcase{row[0].(string), want}
			}},
		{"us ssn", "test_us_ssn_recognizer.py.txt", findSSNs, TypeUSSSN, 28,
			func(t *testing.T, row []any, typ Type) pcase {
				want := []Span{}
				spans := pySpans(t, row[2], typ)
				for i, r := range row[3].([]any) {
					var lo float64
					switch v := r.([]any)[0].(type) {
					case float64:
						lo = v
					case int:
						lo = float64(v)
					}
					if lo >= minScore {
						want = append(want, spans[i])
					}
				}
				return pcase{row[0].(string), want}
			}},
	}
	for _, f := range files {
		t.Run(f.name, func(t *testing.T) {
			rows := presidioList(t, f.file, marker)
			if len(rows) != f.count {
				t.Fatalf("parsed %d cases, want %d", len(rows), f.count)
			}
			for _, r := range rows {
				c := f.cases(t, r.([]any), f.typ)
				t.Run(c.text, func(t *testing.T) {
					if got := collect(f.find, c.text); !reflect.DeepEqual(got, c.want) {
						t.Errorf("got %v, want %v", got, c.want)
					}
				})
			}
		})
	}
}

func TestVerhoeff(t *testing.T) {
	// Presidio's verhoeff_test_set, plus the worked example from Verhoeff's
	// algorithm as Wikipedia gives it: 236 has check digit 3.
	rows := presidioList(t, "test_in_aadhaar_recognizer.py.txt", "verhoeff_test_set = ")
	if len(rows) != 3 {
		t.Fatalf("parsed %d Presidio cases, want 3", len(rows))
	}
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"236 with check digit 3 is valid", "2363", true},
		{"236 with any other check digit is invalid", "2360", false},
		{"one transposed pair breaks the check", "3263", false},
	}
	for _, r := range rows {
		row := r.([]any)
		n := strconv.Itoa(row[0].(int))
		tests = append(tests, struct {
			name string
			in   string
			want bool
		}{"Presidio " + n, n, row[1].(bool)})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verhoeff(tt.in); got != tt.want {
				t.Errorf("verhoeff(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestIBANFormatsMatchPresidio evaluates every entry of Presidio's
// regex_per_country and wants the same regex in ibanFormats.
func TestIBANFormatsMatchPresidio(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(presidioDir, "iban_patterns.py.txt"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := map[string]string{}
	got := map[string]string{}
	inTable := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "  #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case strings.HasPrefix(line, "regex_per_country = {"):
			inTable = true
		case inTable && line == "}":
			inTable = false
		case inTable && strings.HasPrefix(line, `"`):
			cc, expr, ok := strings.Cut(strings.TrimSuffix(line, ","), ": ")
			if !ok {
				t.Fatalf("bad table line %q", line)
			}
			var re strings.Builder
			for _, part := range strings.Split(expr, " + ") {
				if strings.HasPrefix(part, `"`) {
					re.WriteString(strings.Trim(part, `"`))
				} else if v, ok := blocks[part]; ok {
					re.WriteString(v)
				} else {
					t.Fatalf("unknown block %q in %q", part, line)
				}
			}
			got[strings.Trim(cc, `"`)] = re.String()
		case !inTable && strings.Contains(line, " = \""):
			name, val, _ := strings.Cut(line, " = ")
			blocks[name] = strings.Trim(val, `"`)
		}
	}
	if len(got) != 76 || len(ibanFormats) != 76 {
		t.Fatalf("Presidio's table has %d countries and ibanFormats %d, want 76 each", len(got), len(ibanFormats))
	}
	for cc, want := range got {
		t.Run(cc+" has Presidio's format", func(t *testing.T) {
			if have, ok := ibanFormats[cc]; !ok || have != want {
				t.Errorf("ibanFormats[%q] = %q, Presidio has %q", cc, have, want)
			}
		})
	}
}

// TestChecksumNearMisses are pairtime's own cases: a valid number of each
// checksummed kind is redacted, and the same number with its check digit
// changed is not. The valid numbers are published test values.
func TestChecksumNearMisses(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []Span
	}{
		{"Visa test card with a valid Luhn digit is redacted", "card 4111 1111 1111 1111 ok",
			[]Span{{Start: 5, End: 24, Type: TypeCard}}},
		{"the same card with the last digit off by one is kept", "card 4111 1111 1111 1112 ok", []Span{}},
		{"Amex test card in 4-6-5 groups is redacted", "amex 3782 822463 10005",
			[]Span{{Start: 5, End: 22, Type: TypeCard}}},
		{"a 13-digit millisecond timestamp is never a card", "at 1795046500000 ms", []Span{}},
		{"Mastercard 2-series test card with a valid Luhn digit is redacted", "my card 2223 0031 2200 3222 got declined",
			[]Span{{Start: 8, End: 27, Type: TypeCard}}},
		{"the same 2-series card with the last digit off by one is kept", "my card 2223 0031 2200 3223 got declined", []Span{}},
		{"German IBAN from the registry is redacted", "pay DE89 3704 0044 0532 0130 00 now",
			[]Span{{Start: 4, End: 31, Type: TypeIBAN}}},
		{"the same IBAN with a wrong check digit is kept", "pay DE88 3704 0044 0532 0130 00 now", []Span{}},
		{"an IBAN followed by a number keeps the number", "IBAN GB82WEST12345698765432 2 days",
			[]Span{{Start: 5, End: 27, Type: TypeIBAN}}},
		{"Aadhaar with a valid Verhoeff digit is redacted", "aadhaar 2345 6789 0124",
			[]Span{{Start: 8, End: 22, Type: TypeAadhaar}}},
		{"the same Aadhaar with the check digit changed is kept", "aadhaar 2345 6789 0125", []Span{}},
		{"an Aadhaar can't start with 0 or 1", "id 1234 5678 9012", []Span{}},
		{"SSN with matching dashes is redacted", "ssn 219-09-9999", []Span{{Start: 4, End: 15, Type: TypeUSSSN}}},
		{"SSN with area 666 is never issued", "ssn 666-09-9999", []Span{}},
		{"SSN with group 00 is never issued", "ssn 219-00-9999", []Span{}},
		{"SSN with mixed separators is kept", "219-09.9999", []Span{}},
		{"nine bare digits need a context word", "file is 134217728 bytes", []Span{}},
		{"nine bare digits after 'social security' are redacted", "my social security number is 219099999",
			[]Span{{Start: 29, End: 38, Type: TypeUSSSN}}},
		{"NINO with a valid prefix and suffix is redacted", "NI AB 12 34 56 C", []Span{{Start: 3, End: 16, Type: TypeUKNINO}}},
		{"NINO with the excluded prefix GB is kept", "NI GB 12 34 56 C", []Span{}},
		{"NINO with suffix E is kept", "NI AB 12 34 56 E", []Span{}},
		{"email address is redacted", "mail maya.k+tag@school.example.org today",
			[]Span{{Start: 5, End: 34, Type: TypeEmail}}},
		{"retina image name is not an email", "use logo@2x.png here", []Span{}},
		{"npm version spec is not an email", "install react@18.3.1", []Span{}},
		{"an email address after an escaped line break starts after the escape", `hi\nbob@westfield.edu`,
			[]Span{{Start: 4, End: 21, Type: TypeEmail}}},
		{"an email address after a unicode escape starts after the escape", "\x5cu003cbob@westfield.edu\x5cu003e",
			[]Span{{Start: 6, End: 23, Type: TypeEmail}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collect(findPII, tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
