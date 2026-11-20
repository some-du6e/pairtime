// SPDX-License-Identifier: AGPL-3.0-or-later

package params

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math/big"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Rat is an exact rational number in lowest terms with a positive
// denominator. Rational parameters are read as a Rat so that integer-only
// code can use them: x times r is x * r.Num() / r.Den(), rounded however
// the caller's rule says, and x divided by r is x * r.Den() / r.Num().
type Rat struct {
	num, den int64
}

// Num returns r's numerator.
func (r Rat) Num() int64 { return r.num }

// Den returns r's denominator, which is always positive.
func (r Rat) Den() int64 { return r.den }

// String returns r as "num/den", such as "3/2".
func (r Rat) String() string {
	return strconv.FormatInt(r.num, 10) + "/" + strconv.FormatInt(r.den, 10)
}

// Table is one version of the parameter table. A Table never changes.
type Table struct {
	version   string
	canonical []byte
	hash      string
	values    map[string]value
}

// value is one parameter's value.
type value struct {
	kind kind
	// text is the value as the table writes it: "120", "1.5" or "false".
	text string
	// num holds integers, with den 1, and rationals.
	num  Rat
	flag bool
}

// Parse reads a parameter table: a JSON object with the table's "version",
// MAJOR.MINOR.PATCH, and its "params", which map each parameter's name to
// its value. Every parameter this package declares must be there with a
// value of its type, and nothing else may be. Parse reports every problem
// it finds at once, each naming its field by JSON path, as in
// "params.THINK_LEAD_S: missing".
func Parse(data []byte) (*Table, error) {
	doc, err := decode(data)
	if err != nil {
		return nil, err
	}
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("a table is a JSON object, got %s", describe(doc))
	}
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}
	for _, k := range slices.Sorted(maps.Keys(top)) {
		if k != "version" && k != "params" {
			add("%s: a table has only version and params", k)
		}
	}
	version, _ := top["version"].(string)
	if raw, ok := top["version"]; !ok {
		add("version: missing")
	} else if _, ok := parseVersion(version); !ok {
		add("version: must be MAJOR.MINOR.PATCH, such as \"0.1.0\", got %s", describe(raw))
	}
	params, ok := top["params"].(map[string]any)
	if raw, present := top["params"]; !present {
		add("params: missing")
	} else if !ok {
		add("params: must be an object of parameters, got %s", describe(raw))
	}
	values := make(map[string]value, len(all))
	if params != nil {
		for _, p := range all {
			raw, ok := params[p.String()]
			if !ok {
				add("params.%s: missing", p.String())
				continue
			}
			v, err := newValue(p.kind(), raw)
			if err != nil {
				add("params.%s: %v", p.String(), err)
				continue
			}
			values[p.String()] = v
		}
		for _, k := range slices.Sorted(maps.Keys(params)) {
			if !slices.ContainsFunc(all, func(p param) bool { return p.String() == k }) {
				add("params.%s: not a parameter", k)
			}
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	canonical := appendCanonical(nil, doc)
	sum := sha256.Sum256(canonical)
	return &Table{
		version:   version,
		canonical: canonical,
		hash:      "sha256:" + hex.EncodeToString(sum[:]),
		values:    values,
	}, nil
}

// newValue checks that raw, as decode returned it, is a value of kind k.
func newValue(k kind, raw any) (value, error) {
	if k == kindBool {
		b, ok := raw.(bool)
		if !ok {
			return value{}, fmt.Errorf("must be true or false, got %s", describe(raw))
		}
		return value{kind: k, text: strconv.FormatBool(b), flag: b}, nil
	}
	n, ok := raw.(json.Number)
	if !ok {
		return value{}, fmt.Errorf("must be a number, got %s", describe(raw))
	}
	text := string(n)
	if k == kindInt {
		if strings.Contains(text, ".") {
			return value{}, fmt.Errorf("must be an integer, got %s", text)
		}
		i, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return value{}, fmt.Errorf("must fit in 64 bits, got %s", text)
		}
		return value{kind: k, text: text, num: Rat{i, 1}}, nil
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok || !r.Num().IsInt64() || !r.Denom().IsInt64() {
		return value{}, fmt.Errorf("must be a decimal whose numerator and denominator in lowest terms fit in 64 bits, got %s", text)
	}
	return value{kind: k, text: text, num: Rat{r.Num().Int64(), r.Denom().Int64()}}, nil
}

// describe names a decoded JSON value for an error message.
func describe(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case string:
		return strconv.Quote(v)
	case json.Number:
		return string(v)
	case bool:
		return strconv.FormatBool(v)
	}
	return fmt.Sprintf("%T", v)
}

// Version returns the table's version, such as "0.1.0".
func (t *Table) Version() string { return t.version }

// Hash returns the SHA-256 of the table's canonical JSON as "sha256:" and
// 64 lowercase hex digits, the form a minute bucket's params field takes
// (SM 7.6).
func (t *Table) Hash() string { return t.hash }

// Canonical returns the table's canonical JSON, the bytes Hash covers.
func (t *Table) Canonical() []byte { return slices.Clone(t.canonical) }

// Names returns the name of every parameter in the table, sorted.
func (t *Table) Names() []string { return slices.Sorted(maps.Keys(t.values)) }

// Text returns a parameter's value as the table writes it, such as "120",
// "1.5" or "false", and whether the table has the parameter. Names and Text
// are for code that shows a whole table. Rules read values with Int, Rat
// and Bool, whose names the static check can see.
func (t *Table) Text(name string) (string, bool) {
	v, ok := t.values[name]
	return v.text, ok
}

// Int returns an integer parameter. Every table has every IntParam this
// package declares, and no other package can make one up, so Int panics
// only for the zero IntParam, which names no parameter. Each call is a map
// lookup; a rule that reads a parameter in a loop should read it once
// outside.
func (t *Table) Int(p IntParam) int64 { return t.lookup(p.intName, kindInt).num.num }

// Rat returns a rational parameter. Like Int, it panics only for the zero
// RatParam.
func (t *Table) Rat(p RatParam) Rat { return t.lookup(p.ratName, kindRat).num }

// Bool returns a flag. Like Int, it panics only for the zero BoolParam.
func (t *Table) Bool(p BoolParam) bool { return t.lookup(p.flagName, kindBool).flag }

func (t *Table) lookup(name string, k kind) value {
	v, ok := t.values[name]
	if !ok || v.kind != k {
		panic(fmt.Sprintf("params: table %s has no %s parameter %q", t.version, k, name))
	}
	return v
}

// parseVersion splits a MAJOR.MINOR.PATCH version into its three numbers,
// each written in decimal without leading zeros, as in Semantic Versioning.
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		// Nine digits keep Atoi in range where int has 32 bits.
		if !isDigits(p) || len(p) > 9 || (len(p) > 1 && p[0] == '0') {
			return v, false
		}
		v[i], _ = strconv.Atoi(p)
	}
	return v, true
}

// compareVersions orders two valid versions numerically, so 0.9.0 comes
// before 0.10.0.
func compareVersions(a, b string) int {
	va, _ := parseVersion(a)
	vb, _ := parseVersion(b)
	return slices.Compare(va[:], vb[:])
}

// ErrUnknown is the error ByVersion and ByHash wrap when no embedded table
// matches.
var ErrUnknown = errors.New("params: no such table")

//go:embed tables/*.json
var tableFiles embed.FS

// embedded parses the embedded tables once. A table's hash covers its
// version, and no two files can hold the same version, so the hashes
// differ too.
var embedded = sync.OnceValues(func() ([]*Table, error) {
	files, err := fs.Glob(tableFiles, "tables/*.json")
	if err != nil {
		return nil, err
	}
	var tables []*Table
	for _, file := range files {
		data, err := tableFiles.ReadFile(file)
		if err != nil {
			return nil, err
		}
		t, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("params: %s: %w", file, err)
		}
		if path.Base(file) != t.version+".json" {
			return nil, fmt.Errorf("params: %s holds version %s", file, t.version)
		}
		tables = append(tables, t)
	}
	if len(tables) == 0 {
		return nil, errors.New("params: no tables are embedded")
	}
	slices.SortFunc(tables, func(a, b *Table) int { return compareVersions(a.version, b.version) })
	return tables, nil
})

// Tables returns every embedded table, oldest version first. It panics if
// an embedded table doesn't parse, which this package's tests rule out for
// any build whose CI passed.
func Tables() []*Table {
	tables, err := embedded()
	if err != nil {
		panic(err)
	}
	return slices.Clone(tables)
}

// ByVersion returns the embedded table with the given version, such as
// "0.1.0".
func ByVersion(version string) (*Table, error) {
	for _, t := range Tables() {
		if t.version == version {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%w with version %q", ErrUnknown, version)
}

// ByHash returns the embedded table whose Hash is h, which has the form
// "sha256:" and 64 lowercase hex digits.
func ByHash(h string) (*Table, error) {
	for _, t := range Tables() {
		if t.hash == h {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%w with hash %q", ErrUnknown, h)
}
