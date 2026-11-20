// SPDX-License-Identifier: AGPL-3.0-or-later

package params

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tool returns the path of a command an oracle test runs. CI must have it,
// so a missing tool fails the test there and skips it elsewhere.
func tool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err == nil {
		return path
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("%s isn't installed, and this oracle test must run in CI: %v", name, err)
	}
	t.Skipf("%s isn't installed; CI runs this test", name)
	return ""
}

// run runs a command with stdin as its input and returns its output.
func run(t *testing.T, stdin []byte, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(tool(t, name), args...)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, exit.Stderr)
		}
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return out
}

// jq runs jq -cSj . on stdin, or on the file named in args, and returns
// what it writes, as doc.go's hash recipe does. The -j leaves out the
// newline jq -cS ends with. It would also print a top-level string without
// its quotes, so every document given to jq is an object or an array.
func jq(t *testing.T, stdin []byte, args ...string) []byte {
	t.Helper()
	return run(t, stdin, "jq", append([]string{"-cSj", "."}, args...)...)
}

// TestCanonicalMatchesJq is the plan's oracle for the canonical form and
// the hash, run the way doc.go tells readers to run it: for every embedded
// table, Canonical is byte for byte what jq -cSj . writes for the file,
// and Hash is what sha256sum prints for those bytes.
func TestCanonicalMatchesJq(t *testing.T) {
	for _, tab := range Tables() {
		file := filepath.Join("tables", tab.Version()+".json")
		t.Run("jq -cSj and sha256sum agree with table "+tab.Version(), func(t *testing.T) {
			want := jq(t, nil, file)
			if got := tab.Canonical(); !bytes.Equal(got, want) {
				t.Errorf("Canonical() differs from jq -cSj . %s\n got: %s\nwant: %s", file, got, want)
			}
			out := string(run(t, want, "sha256sum"))
			sum, _, _ := strings.Cut(out, " ")
			if len(sum) != 64 {
				t.Fatalf("sha256sum printed %q", out)
			}
			if got := tab.Hash(); got != "sha256:"+sum {
				t.Errorf("Hash() = %s, but sha256sum of jq's output is %s", got, sum)
			}
		})
		t.Run("jq -cS writes table "+tab.Version()+" with a final newline the hash leaves out", func(t *testing.T) {
			out := run(t, nil, "jq", "-cS", ".", file)
			if want := append(tab.Canonical(), '\n'); !bytes.Equal(out, want) {
				t.Errorf("jq -cS . %s wrote %q, want the canonical bytes and a newline", file, out)
			}
		})
	}
}

// TestCanonicalForm checks the canonical form on documents that exercise
// each rule, and that jq writes the same bytes for each of them. A case
// with a JSON escape spells the escape's backslash \x5c, so the source
// holds the escape and not the character it stands for.
func TestCanonicalForm(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"keys sort bytewise, so capitals and _ come before lowercase",
			`{"b":1,"a":2,"B":3,"_":4,"a_b":5,"ab":6,"A1":7}`,
			`{"A1":7,"B":3,"_":4,"a":2,"a_b":5,"ab":6,"b":1}`,
		},
		{
			"whitespace goes",
			"{ \"a\" : [ 1 , 2 ] ,\n\t\"b\" : { } }",
			`{"a":[1,2],"b":{}}`,
		},
		{
			"nested objects sort too",
			`{"z":{"y":1,"x":{"w":true,"v":null}}}`,
			`{"z":{"x":{"v":null,"w":true},"y":1}}`,
		},
		{
			"arrays keep their order",
			`[3,1,2]`,
			`[3,1,2]`,
		},
		{
			"numbers keep their text, even past float64 precision",
			`[0,-1,1.5,0.25,0.08,86400,-0.5,9007199254740993]`,
			`[0,-1,1.5,0.25,0.08,86400,-0.5,9007199254740993]`,
		},
		{
			"numbers at least 0.000001 from 0 stay plain, as jq writes them",
			`[0.000001,-0.000001,0.0000012,1.0000001,-0.0000012]`,
			`[0.000001,-0.000001,0.0000012,1.0000001,-0.0000012]`,
		},
		{
			"a number may have maxNumberLen characters",
			"[" + strings.Repeat("1", maxNumberLen) + "]",
			"[" + strings.Repeat("1", maxNumberLen) + "]",
		},
		{
			"strings escape only quote and backslash",
			`["a\"b","c\\d","A/","\/"]`,
			`["a\"b","c\\d","A/","/"]`,
		},
		{
			"escapes are decoded, so an escaped key sorts as its character and both come out plain",
			"{\"\x5cu0062\":\"\x5cu0041\",\"a\":2}",
			`{"a":2,"b":"A"}`,
		},
		{
			"true, false and null stay",
			`[true,false,null]`,
			`[true,false,null]`,
		},
		{
			"empty objects and arrays stay",
			`{"a":[],"b":{}}`,
			`{"a":[],"b":{}}`,
		},
		{
			"nesting may go maxDepth levels deep",
			strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth),
			strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth),
		},
		{
			"a table puts params before version",
			`{"version":"0.1.0","params":{"B_S":1,"A_S":0.5}}`,
			`{"params":{"A_S":0.5,"B_S":1},"version":"0.1.0"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := decode([]byte(c.in))
			if err != nil {
				t.Fatalf("decode(%q): %v", c.in, err)
			}
			if got := string(appendCanonical(nil, v)); got != c.want {
				t.Errorf("canonical form of %q\n got: %s\nwant: %s", c.in, got, c.want)
			}
			if got := string(jq(t, []byte(c.in))); got != c.want {
				t.Errorf("jq -cSj . of %q\n got: %s\nwant: %s", c.in, got, c.want)
			}
		})
	}
}

// TestCanonicalRejects checks that decode refuses every document whose
// canonical form would be ambiguous or might differ from jq's. An empty
// want accepts any error, for malformed JSON whose message is the JSON
// decoder's own. JSON escapes are spelled as in TestCanonicalForm.
func TestCanonicalRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"a key can't appear twice", `{"a":1,"a":2}`, `key "a" appears twice`},
		{"a key can't appear twice under two spellings", "{\"\x5cu0061\":1,\"a\":2}", `key "a" appears twice`},
		{"a key can't appear twice in a nested object", `{"a":{"b":1,"b":1}}`, `key "b" appears twice`},
		{"numbers have no exponent", `[1e2]`, "plain decimal"},
		{"numbers have no capital exponent", `[1E2]`, "plain decimal"},
		{"fractions don't end in zero", `[1.50]`, "plain decimal"},
		{"whole numbers have no fraction", `[2.0]`, "plain decimal"},
		{"there is no negative zero", `[-0]`, "plain decimal"},
		{"a number closer to 0 than 0.000001 is refused, as jq writes it with an exponent", `[0.0000001]`, "plain decimal"},
		{"a negative number closer to 0 than -0.000001 is refused too", `[-0.0000005]`, "plain decimal"},
		{"a number has at most maxNumberLen characters", "[" + strings.Repeat("1", maxNumberLen+1) + "]", "characters long"},
		{"strings are ASCII", `["é"]`, "printable ASCII"},
		{"escaped non-ASCII counts too", "[\"\x5cu00e9\"]", "printable ASCII"},
		{"strings have no control characters", `["a\nb"]`, "printable ASCII"},
		{"strings have no raw DEL", "[\"\x7f\"]", "printable ASCII"},
		{"strings have no escaped DEL", "[\"\x5cu007f\"]", "printable ASCII"},
		{"keys are ASCII", `{"é":1}`, "printable ASCII"},
		{"nesting goes at most maxDepth levels deep", strings.Repeat("[", maxDepth+1) + strings.Repeat("]", maxDepth+1), "nested more than"},
		{"a million open brackets are refused before they exhaust the stack", strings.Repeat("[", 1<<20), "nested more than"},
		{"one document only", `{} {}`, "more than one JSON document"},
		{"there is a document", ``, "no JSON document"},
		{"whitespace isn't a document", " \n", "no JSON document"},
		{"a truncated document is an error", `{"a":1`, ""},
		{"malformed JSON is an error", `{"a" 1}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decode([]byte(c.in))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("decode(%q) = %v, want an error containing %q", c.in, err, c.want)
			}
		})
	}
}

// TestPlainDecimal checks the number rule on its own, including forms JSON
// itself allows.
func TestPlainDecimal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"zero is plain", "0", true},
		{"an integer is plain", "86400", true},
		{"a negative integer is plain", "-5", true},
		{"a decimal is plain", "1.5", true},
		{"a decimal below one is plain", "0.08", true},
		{"a negative decimal is plain", "-0.5", true},
		{"a millionth is plain", "0.000001", true},
		{"a first nonzero digit sixth after the point is plain", "0.0000012", true},
		{"six zeros after a nonzero integer part are plain", "1.0000001", true},
		{"a number closer to 0 than a millionth isn't, as jq writes it with an exponent", "0.0000001", false},
		{"a negative number closer to 0 than a millionth isn't either", "-0.0000005", false},
		{"negative zero isn't", "-0", false},
		{"a trailing zero isn't", "0.250", false},
		{"a zero fraction isn't", "1.0", false},
		{"an exponent isn't", "1e3", false},
		{"a signed exponent isn't", "1.5E-3", false},
		{"a leading zero isn't", "01", false},
		{"a bare point isn't", "1.", false},
		{"a missing integer part isn't", ".5", false},
		{"an empty string isn't", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := plainDecimal(c.in); got != c.want {
				t.Errorf("plainDecimal(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
