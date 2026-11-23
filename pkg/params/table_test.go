// SPDX-License-Identifier: AGPL-3.0-or-later

package params

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// golden pins the hash of every published table, as computed outside Go
// with
//
//	jq -cSj . pkg/params/tables/<version>.json | sha256sum
//
// A published table never changes (DESIGN 6.6). New values make a new
// version, a new file and a new line here.
var golden = map[string]string{
	"0.1.0": "sha256:ccb35125e1fe7a2f9f136d1fd4e25da5efadac2b12872d34a753f11aef9621b0",
	"0.2.0": "sha256:2e531d6e1901be6fc0cd602817e7e8a28cdd8608e4c3da9330566ac68baae1b0",
	"0.3.0": "sha256:ce6ed56bb12f08dc6090eb729a2a8cf73fca4435dd49182e350698170f9326c8",
}

func TestEmbeddedTables(t *testing.T) {
	tables, err := embedded()
	if err != nil {
		t.Fatalf("the embedded tables don't load: %v", err)
	}
	for _, tab := range tables {
		t.Run("table "+tab.Version()+" hashes to its golden value", func(t *testing.T) {
			want, ok := golden[tab.Version()]
			if !ok {
				t.Fatalf("table %s has no golden hash; compute it with jq and sha256sum and add it", tab.Version())
			}
			if got := tab.Hash(); got != want {
				t.Errorf("table %s hashes to %s, want %s; a published table must not change", tab.Version(), got, want)
			}
		})
	}
	t.Run("every golden version is embedded", func(t *testing.T) {
		for version := range golden {
			if _, err := ByVersion(version); err != nil {
				t.Error(err)
			}
		}
	})
	t.Run("tables come oldest first", func(t *testing.T) {
		if !slices.IsSortedFunc(tables, func(a, b *Table) int { return compareVersions(a.Version(), b.Version()) }) {
			t.Errorf("tables aren't sorted by version")
		}
	})
	t.Run("hashes have the form minute buckets carry", func(t *testing.T) {
		form := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
		for _, tab := range tables {
			if !form.MatchString(tab.Hash()) {
				t.Errorf("table %s hash %q isn't \"sha256:\" and 64 lowercase hex digits", tab.Version(), tab.Hash())
			}
		}
	})
}

func TestByVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
		found   bool
	}{
		{"an embedded version is found", "0.1.0", true},
		{"the version that added INPUT_RELEASE_GRACE_MS is found", "0.2.0", true},
		{"the version that added RELAY_MATCH_S and RELAY_SEEN_LAG_S is found", "0.3.0", true},
		{"two parts aren't a version", "0.1", false},
		{"a v prefix isn't part of a version", "v0.1.0", false},
		{"a version nobody published isn't found", "9.9.9", false},
		{"the empty string isn't a version", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tab, err := ByVersion(c.version)
			if c.found {
				if err != nil || tab.Version() != c.version {
					t.Errorf("ByVersion(%q) = %v, %v", c.version, tab, err)
				}
				return
			}
			if tab != nil || !errors.Is(err, ErrUnknown) {
				t.Errorf("ByVersion(%q) = %v, %v; want nil and ErrUnknown", c.version, tab, err)
			}
		})
	}
}

func TestByHash(t *testing.T) {
	tab, err := ByVersion("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	digits := strings.TrimPrefix(tab.Hash(), "sha256:")
	cases := []struct {
		name  string
		hash  string
		found bool
	}{
		{"a table's own hash finds it", tab.Hash(), true},
		{"the hex digits alone don't", digits, false},
		{"uppercase hex doesn't", "sha256:" + strings.ToUpper(digits), false},
		{"the hash of other bytes doesn't", "sha256:" + strings.Repeat("0", 64), false},
		{"the empty string doesn't", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ByHash(c.hash)
			if c.found {
				if err != nil || got != tab {
					t.Errorf("ByHash(%q) = %v, %v; want table 0.1.0", c.hash, got, err)
				}
				return
			}
			if got != nil || !errors.Is(err, ErrUnknown) {
				t.Errorf("ByHash(%q) = %v, %v; want nil and ErrUnknown", c.hash, got, err)
			}
		})
	}
}

// rawParams returns table 0.1.0's parameters as raw JSON values.
func rawParams(t *testing.T) map[string]string {
	t.Helper()
	tab, err := ByVersion("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	return rawOf(tab)
}

// rawOf returns a table's parameters as raw JSON values.
func rawOf(tab *Table) map[string]string {
	raw := make(map[string]string)
	for _, name := range tab.Names() {
		raw[name], _ = tab.Text(name)
	}
	return raw
}

// paramsJSON writes a params object from raw JSON values, in reverse
// order and with whitespace, so it is never in canonical form.
func paramsJSON(raw map[string]string) string {
	names := slices.Sorted(maps.Keys(raw))
	slices.Reverse(names)
	var b strings.Builder
	b.WriteString("{")
	for i, name := range names {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n  %q: %s", name, raw[name])
	}
	b.WriteString("\n}")
	return b.String()
}

// doc writes a JSON object from pairs of keys and raw JSON values, in the
// order given.
func doc(pairs ...string) []byte {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q: %s", pairs[i], pairs[i+1])
	}
	b.WriteString("}")
	return []byte(b.String())
}

func TestParseRejects(t *testing.T) {
	base := rawParams(t)
	// with returns table 0.1.0's params with one value replaced, or
	// removed when raw is empty.
	with := func(name, raw string) string {
		m := maps.Clone(base)
		if raw == "" {
			delete(m, name)
		} else {
			m[name] = raw
		}
		return paramsJSON(m)
	}
	ps := paramsJSON(base)
	v := `"0.1.0"`
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"a table is an object", []byte(`[1]`), "a table is a JSON object, got an array"},
		{"a table has a version", doc("params", ps), "version: missing"},
		{"a version is a string", doc("version", `1`, "params", ps), "version: must be MAJOR.MINOR.PATCH"},
		{"a version has three parts", doc("version", `"0.1"`, "params", ps), "version: must be MAJOR.MINOR.PATCH"},
		{"a version has no leading zeros", doc("version", `"0.01.0"`, "params", ps), "version: must be MAJOR.MINOR.PATCH"},
		{"a version has no v prefix", doc("version", `"v0.1.0"`, "params", ps), "version: must be MAJOR.MINOR.PATCH"},
		{"a version has no pre-release part", doc("version", `"0.1.0-rc.1"`, "params", ps), "version: must be MAJOR.MINOR.PATCH"},
		{"a table has params", doc("version", v), "params: missing"},
		{"params is an object", doc("version", v, "params", `[]`), "params: must be an object of parameters, got an array"},
		{"a table has no other fields", doc("version", v, "params", ps, "notes", `"hi"`), "notes: a table has only version and params"},
		{"every parameter is there", doc("version", v, "params", with("THINK_LEAD_S", "")), "params.THINK_LEAD_S: missing"},
		{"a misspelled parameter is refused", doc("version", v, "params", with("THINK_LAED_S", "120")), "params.THINK_LAED_S: not a parameter"},
		{"a parameter a later version added is refused in an older table", doc("version", v, "params", with("INPUT_RELEASE_GRACE_MS", "250")), "params.INPUT_RELEASE_GRACE_MS: not a parameter before version 0.2.0, which added it"},
		{"the version that added a parameter has it", doc("version", `"0.2.0"`, "params", ps), "params.INPUT_RELEASE_GRACE_MS: missing"},
		{"a version after the one that added a parameter has it too", doc("version", `"0.10.0"`, "params", ps), "params.INPUT_RELEASE_GRACE_MS: missing"},
		{"a relay parameter is refused in a table older than 0.3.0, which added it", doc("version", `"0.2.0"`, "params", with("RELAY_MATCH_S", "600")),
			"params.RELAY_MATCH_S: not a parameter before version 0.3.0, which added it"},
		{"table 0.3.0 has RELAY_MATCH_S", doc("version", `"0.3.0"`, "params", ps), "params.RELAY_MATCH_S: missing"},
		{"table 0.3.0 has RELAY_SEEN_LAG_S", doc("version", `"0.3.0"`, "params", ps), "params.RELAY_SEEN_LAG_S: missing"},
		{"the replaced STREAK_DAY_S is refused", doc("version", v, "params", with("STREAK_DAY_S", "900")), "params.STREAK_DAY_S: not a parameter"},
		{"SM 8.7's tier-2 flags aren't parameters", doc("version", v, "params", with("regular_timing", "0.1")), "params.regular_timing: not a parameter"},
		{"an integer parameter has no fraction", doc("version", v, "params", with("THINK_LEAD_S", "120.5")), "params.THINK_LEAD_S: must be an integer, got 120.5"},
		{"an integer parameter fits in 64 bits", doc("version", v, "params", with("THINK_LEAD_S", "9223372036854775808")), "params.THINK_LEAD_S: must fit in 64 bits"},
		{"an integer parameter isn't a string", doc("version", v, "params", with("THINK_LEAD_S", `"120"`)), `params.THINK_LEAD_S: must be a number, got "120"`},
		{"an integer parameter isn't a flag", doc("version", v, "params", with("THINK_LEAD_S", "true")), "params.THINK_LEAD_S: must be a number, got true"},
		{"an integer parameter isn't null", doc("version", v, "params", with("THINK_LEAD_S", "null")), "params.THINK_LEAD_S: must be a number, got null"},
		{"a parameter isn't an object", doc("version", v, "params", with("THINK_LEAD_S", `{"s":120}`)), "params.THINK_LEAD_S: must be a number, got an object"},
		{"a rational parameter is a number", doc("version", v, "params", with("COMPOSE_CPS", `"1.5"`)), `params.COMPOSE_CPS: must be a number, got "1.5"`},
		{"a rational parameter's terms fit in 64 bits", doc("version", v, "params", with("COMPOSE_CPS", "1.0000000000000000001")), "params.COMPOSE_CPS: must be a decimal whose numerator and denominator"},
		{"a flag is true or false", doc("version", v, "params", with("COMPOSE_NEEDS_KEYS", "0")), "params.COMPOSE_NEEDS_KEYS: must be true or false, got 0"},
		{"numbers in a table are plain decimals", doc("version", v, "params", with("COMPOSE_CPS", "1.50")), "plain decimal"},
		{"numbers in a table have no exponent", doc("version", v, "params", with("THINK_LEAD_S", "1.2e2")), "plain decimal"},
		{"a rational parameter below 0.000001 is refused, as jq writes it with an exponent", doc("version", v, "params", with("AUDIT_BASE_P", "0.0000001")), "plain decimal"},
		{"a megabyte-long rational is refused before big.Rat parses it", doc("version", v, "params", with("COMPOSE_CPS", "1."+strings.Repeat("3", 1<<20))), "characters long"},
		{"a table nests at most maxDepth levels deep", doc("version", v, "params", with("THINK_LEAD_S", strings.Repeat("[", maxDepth)+strings.Repeat("]", maxDepth))), "nested more than"},
		{"a parameter appears once", doc("version", v, "params", strings.Replace(ps, "{", `{"THINK_LEAD_S": 120,`, 1)), `key "THINK_LEAD_S" appears twice`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tab, err := Parse(c.in)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Parse = %v, %v; want an error containing %q\ninput: %s", tab, err, c.want, c.in)
			}
		})
	}
}

// TestParseReportsEveryProblem checks that Parse lists all the problems in
// a table at once, so one fix-and-push round can fix them all.
func TestParseReportsEveryProblem(t *testing.T) {
	m := rawParams(t)
	delete(m, "THINK_LEAD_S")
	m["COMPOSE_CPS"] = "true"
	_, err := Parse(doc("version", `"0.1"`, "params", paramsJSON(m)))
	cases := []struct {
		name string
		want string
	}{
		{"the bad version is reported", "version: must be MAJOR.MINOR.PATCH"},
		{"the missing parameter is reported", "params.THINK_LEAD_S: missing"},
		{"the wrong type is reported", "params.COMPOSE_CPS: must be a number, got true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Parse error = %v, want it to contain %q", err, c.want)
			}
		})
	}
	t.Run("without a valid version, a parameter some versions lack isn't reported missing", func(t *testing.T) {
		for _, name := range []string{"INPUT_RELEASE_GRACE_MS", "RELAY_MATCH_S", "RELAY_SEEN_LAG_S"} {
			if err == nil || strings.Contains(err.Error(), name) {
				t.Errorf("Parse error = %v, want one that doesn't name %s", err, name)
			}
		}
	})
}

// bump returns a different value of the same type as text: the flag
// flipped, or the number plus one.
func bump(text string) string {
	switch text {
	case "true":
		return "false"
	case "false":
		return "true"
	}
	whole, frac, dot := strings.Cut(text, ".")
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		panic(err)
	}
	s := strconv.FormatInt(n+1, 10)
	if dot {
		s += "." + frac
	}
	return s
}

// TestHashChangesWithEveryValue is the plan's check that changing any one
// value changes the hash, run for every parameter of every table, plus the
// version.
func TestHashChangesWithEveryValue(t *testing.T) {
	for _, tab := range Tables() {
		base := rawOf(tab)
		for _, name := range tab.Names() {
			changed := bump(base[name])
			t.Run(fmt.Sprintf("table %s: %s from %s to %s changes the hash", tab.Version(), name, base[name], changed), func(t *testing.T) {
				m := maps.Clone(base)
				m[name] = changed
				got, err := Parse(doc("version", strconv.Quote(tab.Version()), "params", paramsJSON(m)))
				if err != nil {
					t.Fatal(err)
				}
				if got.Hash() == tab.Hash() {
					t.Errorf("%s = %s hashes the same as %s", name, changed, base[name])
				}
				if v, _ := got.Text(name); v != changed {
					t.Errorf("%s = %s after the change, want %s", name, v, changed)
				}
			})
		}
	}
	tab, err := ByVersion("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	base := rawParams(t)
	t.Run("the version is in the hash too", func(t *testing.T) {
		got, err := Parse(doc("version", `"0.1.1"`, "params", paramsJSON(base)))
		if err != nil {
			t.Fatal(err)
		}
		if got.Hash() == tab.Hash() {
			t.Errorf("version 0.1.1 hashes the same as 0.1.0")
		}
	})
}

// TestLayoutDoesntChangeHash checks that only values count: key order,
// whitespace and field order in the file don't change the hash.
func TestLayoutDoesntChangeHash(t *testing.T) {
	tab, err := ByVersion("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	ps := paramsJSON(rawParams(t))
	cases := []struct {
		name string
		in   []byte
	}{
		{"keys in reverse order with whitespace", doc("version", `"0.1.0"`, "params", ps)},
		{"params before version", doc("params", ps, "version", `"0.1.0"`)},
		{"the canonical bytes themselves", tab.Canonical()},
	}
	for _, c := range cases {
		t.Run(c.name+" hash the same", func(t *testing.T) {
			got, err := Parse(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Hash() != tab.Hash() || string(got.Canonical()) != string(tab.Canonical()) {
				t.Errorf("hash %s, want %s", got.Hash(), tab.Hash())
			}
			for _, name := range tab.Names() {
				want, _ := tab.Text(name)
				if v, _ := got.Text(name); v != want {
					t.Errorf("%s = %s, want %s", name, v, want)
				}
			}
		})
	}
}

// TestMadeUpNamesPanic checks that reading a parameter no table declares,
// reading one as the wrong type, or reading the zero value panics instead
// of returning zero. Only this package can make the first two kinds of
// name; other packages can only make the zero value.
func TestMadeUpNamesPanic(t *testing.T) {
	tab, err := ByVersion("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		read func()
	}{
		{"Int of a name no table has panics", func() { tab.Int(IntParam{"NOT_A_PARAM_S"}) }},
		{"Int of a parameter a later table added panics", func() { tab.Int(INPUT_RELEASE_GRACE_MS) }},
		{"Int of a relay parameter, which table 0.3.0 added, panics in an older table", func() { tab.Int(RELAY_SEEN_LAG_S) }},
		{"Int of a rational parameter panics", func() { tab.Int(IntParam{COMPOSE_CPS.ratName}) }},
		{"Rat of an integer parameter panics", func() { tab.Rat(RatParam{THINK_LEAD_S.intName}) }},
		{"Bool of an integer parameter panics", func() { tab.Bool(BoolParam{THINK_LEAD_S.intName}) }},
		{"Int of the zero IntParam panics", func() { tab.Int(IntParam{}) }},
		{"Rat of the zero RatParam panics", func() { tab.Rat(RatParam{}) }},
		{"Bool of the zero BoolParam panics", func() { tab.Bool(BoolParam{}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("the read didn't panic")
				}
			}()
			c.read()
		})
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"three numbers are a version", "0.1.0", true},
		{"numbers can have several digits", "10.20.300", true},
		{"two parts aren't", "0.1", false},
		{"four parts aren't", "0.1.0.0", false},
		{"leading zeros aren't allowed", "00.1.0", false},
		{"pre-release parts aren't allowed", "0.1.0-rc.1", false},
		{"a v prefix isn't allowed", "v0.1.0", false},
		{"an empty part isn't allowed", "0..0", false},
		{"spaces aren't allowed", " 0.1.0", false},
		{"ten digits are too many for 32-bit int", "1234567890.0.0", false},
		{"the empty string isn't one", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := parseVersion(c.in); ok != c.ok {
				t.Errorf("parseVersion(%q) ok = %v, want %v", c.in, ok, c.ok)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"minor versions compare as numbers, not text", "0.9.0", "0.10.0", -1},
		{"major outranks minor", "1.0.0", "0.99.99", 1},
		{"patch counts last", "0.1.1", "0.1.0", 1},
		{"a version equals itself", "0.1.0", "0.1.0", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compareVersions(c.a, c.b); got != c.want {
				t.Errorf("compareVersions(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
			}
		})
	}
}
