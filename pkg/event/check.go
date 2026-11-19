// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// Unix-millisecond times must fall in [minUnixMS, maxUnixMS). The bounds
	// catch unit mistakes, not clock skew: a time in seconds or microseconds
	// lands far outside them.
	minUnixMS = 1_577_836_800_000 // 2020-01-01T00:00:00Z
	maxUnixMS = 4_102_444_800_000 // 2100-01-01T00:00:00Z

	maxKeyLen = 128
	// maxList caps every list in an event, so one malformed line can't make
	// the reader allocate without limit.
	maxList = 1024
	// sumTolerance absorbs float rounding when parts must add up to a
	// total. Credited seconds are multiples of 0.5, so honest sums are exact.
	sumTolerance = 1e-6
)

// checker collects every rule a record breaks. Each error names the field
// by its JSON path, prefixed with prefix.
type checker struct {
	prefix string
	errs   []error
}

func validate(check func(*checker)) error {
	c := &checker{}
	check(c)
	return c.err()
}

func (c *checker) err() error { return errors.Join(c.errs...) }

func (c *checker) addf(field, format string, args ...any) {
	c.errs = append(c.errs, fmt.Errorf("%s%s: %s", c.prefix, field, fmt.Sprintf(format, args...)))
}

// fieldIndex names one element of a list field, such as "files[2]".
func fieldIndex(field string, i int) string { return fmt.Sprintf("%s[%d]", field, i) }

// within runs check with field added to the path prefix.
func (c *checker) within(field string, check func(*checker)) {
	saved := c.prefix
	c.prefix = saved + field + "."
	check(c)
	c.prefix = saved
}

// isKey reports whether s is an opaque key: 1 to 128 ASCII letters, digits,
// '_' or '-'. A path, a command line with arguments or an email address
// can't pass, since each needs a separator, a space, a dot or '@'. A bare
// name such as "Makefile" or "pytest" does pass, so isKey checks shape,
// not privacy. What keeps a key private is that it is keyed per device: an
// HMAC under a device secret, or a random id.
func isKey(s string) bool {
	if len(s) == 0 || len(s) > maxKeyLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

func (c *checker) key(field, v string) {
	if !isKey(v) {
		c.addf(field, "must be 1-%d letters, digits, '_' or '-', got %q", maxKeyLen, v)
	}
}

func (c *checker) optKey(field, v string) {
	if v != "" {
		c.key(field, v)
	}
}

// keys checks a list of keys and reports at most its first bad entry.
func (c *checker) keys(field string, vs []string) {
	if len(vs) > maxList {
		c.addf(field, "has %d entries, more than %d", len(vs), maxList)
		return
	}
	for i, v := range vs {
		if !isKey(v) {
			c.key(fieldIndex(field, i), v)
			return
		}
	}
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if b := s[i]; !('0' <= b && b <= '9' || 'a' <= b && b <= 'f') {
			return false
		}
	}
	return true
}

// isUUIDv7 reports whether s is a UUIDv7 (RFC 9562) in its canonical
// lowercase text form.
func isUUIDv7(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if !isHex(s[i:i+1], 1) {
				return false
			}
		}
	}
	return s[14] == '7' && strings.IndexByte("89ab", s[19]) >= 0
}

func (c *checker) eventID(field, v string) {
	if !isUUIDv7(v) {
		c.addf(field, "must be a lowercase UUIDv7, got %q", v)
	}
}

func (c *checker) eventIDs(field string, vs []string) {
	if len(vs) > maxList {
		c.addf(field, "has %d entries, more than %d", len(vs), maxList)
		return
	}
	for i, v := range vs {
		if !isUUIDv7(v) {
			c.eventID(fieldIndex(field, i), v)
			return
		}
	}
}

// isEnvName reports whether s is an environment variable's name: a letter
// or '_', then up to maxEnvNameLen-1 letters, digits or '_'. "NAME=value"
// fails, so a value can't pass as a name.
func isEnvName(s string) bool {
	if len(s) == 0 || len(s) > maxEnvNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || b == '_' || i > 0 && '0' <= b && b <= '9') {
			return false
		}
	}
	return true
}

// isGitID reports whether s is a git object id: 40 lowercase hex digits,
// or 64 in a SHA-256 repository.
func isGitID(s string) bool { return isHex(s, 40) || isHex(s, 64) }

// isSlashCommand reports whether s looks like "/loop" or "/project:review".
func isSlashCommand(s string) bool {
	if len(s) < 2 || len(s) > 65 || s[0] != '/' {
		return false
	}
	for i := 1; i < len(s); i++ {
		b := s[i]
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || strings.IndexByte("_:.-", b) >= 0) {
			return false
		}
	}
	return true
}

// isSemver reports whether s is MAJOR.MINOR.PATCH, digits only.
func isSemver(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 9 {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}

// text checks free-form text: valid UTF-8, at most maxRunes characters
// (what JSON Schema's maxLength counts), and no control characters.
func (c *checker) text(field, v string, maxRunes int) {
	if !utf8.ValidString(v) {
		c.addf(field, "must be valid UTF-8")
		return
	}
	if n := utf8.RuneCountInString(v); n > maxRunes {
		c.addf(field, "has %d characters, more than %d", n, maxRunes)
	}
	if strings.IndexFunc(v, unicode.IsControl) >= 0 {
		c.addf(field, "must not contain control characters")
	}
}

func (c *checker) unixMS(field string, v int64) {
	if v < minUnixMS || v >= maxUnixMS {
		c.addf(field, "must be Unix milliseconds between 2020 and 2100, got %d", v)
	}
}

// span checks two Unix-millisecond times and that end isn't before start.
func (c *checker) span(startField string, start int64, endField string, end int64) {
	c.unixMS(startField, start)
	c.unixMS(endField, end)
	if end < start {
		c.addf(endField, "is %d ms before %s", start-end, startField)
	}
}

// slotStart checks a Unix-millisecond time on a 15-second slot boundary.
func (c *checker) slotStart(field string, v int64) {
	c.unixMS(field, v)
	if v%SlotMS != 0 {
		c.addf(field, "must fall on a :00, :15, :30 or :45 second boundary, got %d", v)
	}
}

func (c *checker) instant(field string, t time.Time) {
	if ms := t.UnixMilli(); ms < minUnixMS || ms >= maxUnixMS {
		c.addf(field, "must be between 2020 and 2100, got %s", t.Format(time.RFC3339Nano))
	}
}

func (c *checker) count(field string, v int) {
	if v < 0 {
		c.addf(field, "must not be negative, got %d", v)
	}
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (c *checker) seconds(field string, v float64) {
	if !finite(v) || v < 0 {
		c.addf(field, "must be a finite number of seconds >= 0, got %v", v)
	}
}

// slotSeconds checks credited seconds within one slot: 0 to 15, in steps
// of 0.5, since a half-weight second credits half a second (SM 5.1).
func (c *checker) slotSeconds(field string, v float64) {
	if !finite(v) || v < 0 || v > SlotMS/1000 || v*2 != math.Trunc(v*2) {
		c.addf(field, "must be 0 to 15 seconds in steps of 0.5, got %v", v)
	}
}

func (c *checker) scoringVersion(field, v string) {
	if !isSemver(v) {
		c.addf(field, "must be a scoring version like 1.0.0, got %q", v)
	}
}

func (c *checker) paramsHash(field, v string) {
	if !strings.HasPrefix(v, "sha256:") || !isHex(v[len("sha256:"):], 64) {
		c.addf(field, "must be \"sha256:\" and 64 lowercase hex digits, got %q", v)
	}
}

// breakdown checks a map of seconds: every key passes keyOK and every value
// is a finite number >= 0. It returns the values' sum. Keys are visited in
// sorted order so errors come out the same way every time.
func breakdown[K ~string](c *checker, field string, m map[K]float64, keyOK func(K) bool) float64 {
	sum := 0.0
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !keyOK(k) {
			c.addf(field, "has an invalid key %q", k)
		}
		c.seconds(fmt.Sprintf("%s[%s]", field, k), m[k])
		sum += m[k]
	}
	return sum
}

// sumsTo reports a breakdown whose parts don't add up to its total.
func (c *checker) sumsTo(field string, sum float64, totalField string, total float64) {
	if math.Abs(sum-total) > sumTolerance {
		c.addf(field, "sums to %v, but %s is %v", sum, totalField, total)
	}
}

func checkEnum[T ~string](c *checker, field string, e enum[T], v T) {
	if !e.has(v) {
		c.addf(field, "%q is not one of %q", v, e.values)
	}
}

func checkOptEnum[T ~string](c *checker, field string, e enum[T], v T) {
	if v != "" {
		checkEnum(c, field, e, v)
	}
}
