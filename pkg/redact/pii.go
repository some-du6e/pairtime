// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The personal-data step ports Presidio's pattern recognizers
// (presidio-analyzer 2.2.366, MIT; TP B4 step 3): their patterns, their
// checksums and other validation, and their scores. Presidio keeps any
// result with a score above zero. Here a result needs minScore, Presidio's
// floor for a result with a context word, so the very weak SSN shapes, any
// nine digits for one, need "ssn" or "social" nearby. Everything else
// either scores 0.5 or is validated to 1.
//
// Presidio runs on Python's re module, which has lookarounds and a
// Unicode \b. Go's regexp has neither, so each pattern below drops its
// leading \b or lookaround, and the code checks it at the start of the
// match instead (finditer). Digits are ASCII digits; Python's \d also
// takes other scripts' digits.
const (
	minScore      = 0.4  // LemmaContextAwareEnhancer.min_score_with_context_similarity
	contextFactor = 0.35 // LemmaContextAwareEnhancer.context_similarity_factor
)

func findPII(text string, emit func(start, end int, t Type)) {
	findCards(text, emit)
	findIBANs(text, emit)
	findSSNs(text, emit)
	findNINOs(text, emit)
	findAadhaars(text, emit)
	findEmails(text, emit)
}

// finditer works like Python's re.finditer over text for a pattern whose
// leading assertion Go can't express: at each byte offset where start
// holds, it tries re, which must begin with ^, on the rest of the text. A
// match moves the scan to its end, whether or not f keeps it, as in
// Python.
func finditer(text string, re *regexp.Regexp, start func(i int) bool, f func(m []int)) {
	for i := 0; i < len(text); {
		if !start(i) {
			i++
			continue
		}
		m := re.FindStringSubmatchIndex(text[i:])
		if m == nil {
			i++
			continue
		}
		for k := range m {
			if m[k] >= 0 {
				m[k] += i
			}
		}
		f(m)
		i = max(m[1], i+1)
	}
}

// isWord is Python's \w for a str pattern: letters, numbers and '_'.
func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// boundary is Python's \b at byte offset i of s.
func boundary(s string, i int) bool {
	before, after := false, false
	if i > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:i])
		before = isWord(r)
	}
	if i < len(s) {
		r, _ := utf8.DecodeRuneInString(s[i:])
		after = isWord(r)
	}
	return before != after
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

// digitStart is the leading \b of a pattern that begins with a digit, at
// a digit that doesn't start the fraction of a decimal number.
func digitStart(text string) func(int) bool {
	return func(i int) bool { return isDigit(text[i]) && boundary(text, i) && !fractionStart(text, i) }
}

// fractionStart reports whether the digit at text[i] comes right after a
// digit and a '.', where the fraction of a number such as
// 4.614999999999999 starts. Presidio's patterns need only a word edge,
// which a '.' makes, so they read the fraction of that OTLP asDouble value
// as fifteen digits that start like a card, and Luhn passes about one such
// run in ten (issue #104). So no card, SSN or Aadhaar number starts there.
// A match may still end right before a '.' and a digit: a number written as
// a float, such as 4111111111111111.0 from Python's json.dumps, is still a
// card, and so is a card number that ends a sentence. A fraction with no
// digit before its point, as minified JavaScript writes .5, or after a
// decimal comma, can still be taken for one. An exponent's digits are too
// few for any of these patterns.
func fractionStart(text string, i int) bool {
	return i >= 2 && text[i-1] == '.' && isDigit(text[i-2])
}

// strip removes every byte in chars from s, as Presidio's sanitize_value
// does with its replacement pairs.
func strip(s, chars string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(chars, r) {
			return -1
		}
		return r
	}, s)
}

// withContext is LemmaContextAwareEnhancer's boost for a result that has a
// context word nearby.
func withContext(score float64, found bool) float64 {
	if !found {
		return score
	}
	return min(max(score+contextFactor, minScore), 1)
}

// hasContext reports whether one of words appears in the ten words before
// offset i. Presidio looks at five lemmas before the match after dropping
// stop words and punctuation; ten plain words stands in for that.
func hasContext(text string, i int, words []string) bool {
	lo := max(0, i-256)
	for lo > 0 && !utf8.RuneStart(text[lo]) {
		lo--
	}
	fields := strings.FieldsFunc(strings.ToLower(text[lo:i]), func(r rune) bool { return !isWord(r) })
	if len(fields) > 10 {
		fields = fields[len(fields)-10:]
	}
	for _, f := range fields {
		for _, w := range words {
			if strings.Contains(f, w) {
				return true
			}
		}
	}
	return false
}

// Credit cards: CreditCardRecognizer, "All Credit Cards (weak)", 0.3, then
// a Luhn check that sets the score to 1 or drops the result. The first
// group also takes 22 to 27, which Presidio's pattern leaves out:
// Mastercard has issued 2221 to 2720 since 2017, and Mir uses 2200 to
// 2204. Luhn still drops nine in ten other numbers that start that way.
var cardRE = lazy(`^((4\d{3})|(5[0-5]\d{2})|(6\d{3})|(1\d{3})|(3\d{3})|(2[2-7]\d{2}))[- ]?(\d{3,4})[- ]?(\d{3,4})[- ]?(\d{3,5})\b`)

func findCards(text string, emit func(int, int, Type)) {
	finditer(text, cardRE.get(), func(i int) bool {
		// \b(?!1\d{12}(?!\d)): not a 13-digit number that starts with 1,
		// which is what a timestamp in milliseconds looks like.
		return digitStart(text)(i) && !thirteenFromOne(text, i)
	}, func(m []int) {
		if luhn(strip(text[m[0]:m[1]], "- ")) {
			emit(m[0], m[1], TypeCard)
		}
	})
}

func thirteenFromOne(s string, i int) bool {
	if i+13 > len(s) || s[i] != '1' {
		return false
	}
	for j := i + 1; j < i+13; j++ {
		if !isDigit(s[j]) {
			return false
		}
	}
	if i+13 == len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[i+13:])
	return !unicode.IsDigit(r)
}

// luhn is CreditCardRecognizer.__luhn_checksum.
func luhn(digits string) bool {
	sum := 0
	for i := 0; i < len(digits); i++ {
		d := int(digits[len(digits)-1-i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return len(digits) > 0 && sum%10 == 0
}

// IBANs: IbanRecognizer. Its pattern is
//
//	(?<![A-Z0-9])([A-Z]{2}[0-9]{2}(?:[ -]?[A-Z0-9]{4}){2,6})((?:[ -]?[A-Z0-9]{4})?)((?:[ -]?[A-Z0-9]{1,3})?)(?![A-Z0-9])
//
// and it validates the longest of the three group ends first, keeping the
// first that passes the mod-97 check and the country's format. Here every
// place the run could end, with up to 34 letters and digits and one space
// or dash between any two of them, is a candidate, tried longest first.
// That finds everything Presidio's grouping finds, and a few groupings it
// doesn't.
func findIBANs(text string, emit func(int, int, Type)) {
	for i := 0; i+4 <= len(text); i++ {
		if i > 0 && (isUpper(text[i-1]) || isDigit(text[i-1])) {
			continue
		}
		if !isUpper(text[i]) || !isUpper(text[i+1]) || !isDigit(text[i+2]) || !isDigit(text[i+3]) {
			continue
		}
		if end := ibanAt(text, i); end > 0 {
			emit(i, end, TypeIBAN)
			i = end - 1
		}
	}
}

func ibanAt(s string, i int) int {
	upperOrDigit := func(j int) bool { return j < len(s) && (isUpper(s[j]) || isDigit(s[j])) }
	var ends []int
	n := 0
	for j := i; j < len(s) && n < 34; {
		switch {
		case upperOrDigit(j):
			n++
			j++
			if n >= 15 && !upperOrDigit(j) {
				ends = append(ends, j)
			}
		case (s[j] == ' ' || s[j] == '-') && n >= 4 && upperOrDigit(j+1):
			j++
		default:
			j = len(s)
		}
	}
	for k := len(ends) - 1; k >= 0; k-- {
		if validIBAN(strip(s[i:ends[k]], " -")) {
			return ends[k]
		}
	}
	return 0
}

// validIBAN is IbanRecognizer.validate_result on a value without
// separators: the check digits must be the ones ISO 13616 computes, and
// the value must start with its country's format. Like Presidio, the
// format is only anchored at the start, which its table relies on: the
// entries for Mauritius, Palestine, Portugal and Qatar are shorter than
// those countries' IBANs.
func validIBAN(v string) bool {
	if len(v) < 5 {
		return false
	}
	format, ok := ibanFormats[v[:2]]
	if !ok {
		return false
	}
	// __generate_iban_check_digits: 98 - (number mod 97), where the
	// number is the value with "00" for its check digits, moved to the end,
	// letters counted from A = 10.
	rearranged := v[4:] + v[:2] + "00"
	rem := 0
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		switch {
		case isDigit(c):
			rem = (rem*10 + int(c-'0')) % 97
		case isUpper(c):
			rem = (rem*100 + int(c-'A') + 10) % 97
		default:
			return false
		}
	}
	if fmt.Sprintf("%02d", 98-rem) != v[2:4] {
		return false
	}
	re, err := regexp.Compile("^(?:" + format + ")")
	return err == nil && re.MatchString(v)
}

// US Social Security numbers: UsSsnRecognizer's five patterns, each run on
// its own as Presidio does, and its invalidate_result.
var ssnPatterns = []struct {
	re    *lazyRE
	score float64
}{
	{lazy(`^([0-9]{5})-([0-9]{4})\b`), 0.05},
	{lazy(`^([0-9]{3})-([0-9]{6})\b`), 0.05},
	{lazy(`^(([0-9]{3})-([0-9]{2})-([0-9]{4}))\b`), 0.05},
	{lazy(`^[0-9]{9}\b`), 0.05},
	{lazy(`^([0-9]{3})[- .]([0-9]{2})[- .]([0-9]{4})\b`), 0.5},
}

var ssnContext = []string{"social", "security", "ssn", "ssns", "ssid"}

func findSSNs(text string, emit func(int, int, Type)) {
	for _, p := range ssnPatterns {
		finditer(text, p.re.get(), digitStart(text), func(m []int) {
			if ssnInvalid(text[m[0]:m[1]]) {
				return
			}
			if withContext(p.score, hasContext(text, m[0], ssnContext)) >= minScore {
				emit(m[0], m[1], TypeUSSSN)
			}
		})
	}
}

// ssnInvalid is UsSsnRecognizer.invalidate_result.
func ssnInvalid(v string) bool {
	delims := map[rune]bool{}
	for _, c := range v {
		if c == '.' || c == '-' || c == ' ' {
			delims[c] = true
		}
	}
	if len(delims) > 1 {
		return true
	}
	d := strip(v, ".- ")
	if d == "" || strings.Count(d, d[:1]) == len(d) {
		return true
	}
	if len(d) >= 5 && d[3:5] == "00" || len(d) > 5 && d[5:] == "0000" {
		return true
	}
	if len(d) >= 3 && (d[:3] == "000" || d[:3] == "666") {
		return true
	}
	switch d {
	case "123456789", "987654320", "078051120":
		return true
	}
	return false
}

// UK National Insurance numbers: UkNinoRecognizer, "NINO (medium)", 0.5,
// with re.IGNORECASE as every Presidio pattern recognizer has.
var ninoRE = lazy(`(?i)^([a-ceghj-pr-tw-z][a-ceghj-npr-tw-z]) ?([0-9]{2}) ?([0-9]{2}) ?([0-9]{2}) ?([a-d])\b`)

func findNINOs(text string, emit func(int, int, Type)) {
	finditer(text, ninoRE.get(), func(i int) bool {
		if i+2 > len(text) || !boundary(text, i) {
			return false
		}
		// (?!bg|gb|nk|kn|nt|tn|zz), ignoring case.
		switch strings.ToLower(text[i : i+2]) {
		case "bg", "gb", "nk", "kn", "nt", "tn", "zz":
			return false
		}
		return true
	}, func(m []int) {
		emit(m[0], m[1], TypeUKNINO)
	})
}

// Aadhaar numbers: InAadhaarRecognizer's two patterns, 0.01, and its
// validate_result: twelve digits, the first at least 2, a valid Verhoeff
// check digit, and not a palindrome.
var aadhaarPatterns = []*lazyRE{
	lazy(`^[0-9]{12}\b`),
	lazy(`^[0-9]{4}[- :][0-9]{4}[- :][0-9]{4}\b`),
}

func findAadhaars(text string, emit func(int, int, Type)) {
	for _, re := range aadhaarPatterns {
		finditer(text, re.get(), digitStart(text), func(m []int) {
			if validAadhaar(strip(text[m[0]:m[1]], "- :")) {
				emit(m[0], m[1], TypeAadhaar)
			}
		})
	}
}

func validAadhaar(v string) bool {
	if len(v) != 12 || v[0] < '2' || v[0] > '9' {
		return false
	}
	for i := 0; i < len(v); i++ {
		if !isDigit(v[i]) {
			return false
		}
	}
	palindrome := true
	for i := 0; i < len(v)/2; i++ {
		if v[i] != v[len(v)-1-i] {
			palindrome = false
		}
	}
	return verhoeff(v) && !palindrome
}

// The Verhoeff tables, as in InAadhaarRecognizer._is_verhoeff_number: the
// multiplication table of the dihedral group D5 and the position
// permutation.
var (
	verhoeffD = [10][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 2, 3, 4, 0, 6, 7, 8, 9, 5},
		{2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
		{3, 4, 0, 1, 2, 8, 9, 5, 6, 7},
		{4, 0, 1, 2, 3, 9, 5, 6, 7, 8},
		{5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
		{6, 5, 9, 8, 7, 1, 0, 4, 3, 2},
		{7, 6, 5, 9, 8, 2, 1, 0, 4, 3},
		{8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
		{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	}
	verhoeffP = [8][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
		{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
		{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
		{9, 4, 5, 3, 1, 2, 6, 8, 7, 0},
		{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
		{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
		{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
	}
)

// verhoeff reports whether the decimal digits in v end in a valid Verhoeff
// check digit.
func verhoeff(v string) bool {
	c := 0
	for i := 0; i < len(v); i++ {
		d := v[len(v)-1-i]
		if !isDigit(d) {
			return false
		}
		c = verhoeffD[c][verhoeffP[i%8][d-'0']]
	}
	return len(v) > 0 && c == 0
}

// Email addresses: EmailRecognizer, "Email (Medium)", 0.5, with Python's
// \w spelled out, then validate_result, which keeps a match whose domain
// ends in a public suffix. Without the public suffix list, the last label
// has to be a TLD-shaped word that isn't a file extension.
const (
	emailChar = `!#$%&'*+\-/=?^_` + "`" + `{|}~\p{L}\p{N}`
	emailWord = `[\p{L}\p{N}_]`
)

var emailRE = lazy(`(?i)^((([` + emailChar + `])|([` + emailChar + `][` + emailChar + `.]*[` + emailChar + `]))@` +
	emailWord + `+(?:-+` + emailWord + `+)*(?:\.` + emailWord + `+(?:-+` + emailWord + `+)*)+)`)

// maxLocal bounds how far before an @ a match may start. RFC 5321 allows
// 64 bytes of local part; this is generous, and it keeps a long run of
// word characters from making the scan quadratic.
const maxLocal = 256

// findEmails gives the matches finditer would, but only tries the starts
// that can reach an @: those in the run of local-part characters right
// before it. The run stops at a backslash escape, and the start right after
// one counts as a word edge, so in a raw capture "hi\nmaya@example.com"
// gives maya@example.com, not nmaya@example.com, whose removal would leave
// a broken \ escape behind.
func findEmails(text string, emit func(int, int, Type)) {
	for from := 0; from < len(text); {
		k := strings.IndexByte(text[from:], '@')
		if k < 0 {
			return
		}
		at := from + k
		s := at
		escaped := false
		for s > from && at-s < maxLocal {
			if escapedBefore(text, s) {
				escaped = true
				break
			}
			r, size := utf8.DecodeLastRuneInString(text[from:s])
			if !isLocal(r) {
				break
			}
			s -= size
		}
		next := at + 1
		for i := s; i < at; {
			if boundary(text, i) || i == s && escaped {
				if m := emailRE.get().FindStringIndex(text[i:]); m != nil {
					start, end := i, i+m[1]
					// The pattern's trailing \b.
					if (end == len(text) || boundary(text, end)) && emailDomainOK(text[start:end]) {
						emit(start, end, TypeEmail)
					}
					next = end
					break
				}
			}
			_, size := utf8.DecodeRuneInString(text[i:])
			i += size
		}
		from = next
	}
}

// isLocal reports whether r can be part of the pattern's local part.
func isLocal(r rune) bool {
	return isWord(r) || r == '.' || strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r)
}

// notTLDs are file extensions that aren't top-level domains, so a name
// like "logo@2x.png" isn't an email address.
var notTLDs = map[string]bool{
	"bmp": true, "cjs": true, "css": true, "csv": true, "gif": true, "gz": true,
	"heic": true, "htm": true, "html": true, "ico": true, "jpeg": true, "jpg": true,
	"js": true, "json": true, "jsx": true, "lock": true, "log": true, "map": true,
	"mjs": true, "mov": true, "mp3": true, "mp4": true, "png": true, "scss": true,
	"svg": true, "tar": true, "tgz": true, "tif": true, "tiff": true, "toml": true,
	"ts": true, "tsx": true, "txt": true, "wasm": true, "wav": true, "webm": true,
	"webp": true, "yaml": true, "yml": true,
}

func emailDomainOK(addr string) bool {
	tld := strings.ToLower(addr[strings.LastIndexByte(addr, '.')+1:])
	if notTLDs[tld] {
		return false
	}
	if strings.HasPrefix(tld, "xn--") {
		return len(tld) > 4
	}
	if utf8.RuneCountInString(tld) < 2 {
		return false
	}
	for _, r := range tld {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
