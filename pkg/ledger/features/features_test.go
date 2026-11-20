// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The expected values in the rule tables below come from
// tools/ledger_ref.py, the reference this package is checked against;
// testdata/ledger/vectors-content.json covers the same rules again.

func TestIsSpace(t *testing.T) {
	t.Run("IsSpace is unicode.IsSpace, the White_Space property, at every code point", func(t *testing.T) {
		n := 0
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if IsSpace(r) != unicode.IsSpace(r) {
				t.Errorf("U+%04X: IsSpace %v, unicode.IsSpace %v", r, IsSpace(r), unicode.IsSpace(r))
			}
			if IsSpace(r) {
				n++
			}
		}
		if n != 25 {
			t.Errorf("%d whitespace code points, want 25", n)
		}
	})
	cases := []struct {
		name  string
		r     rune
		space bool
	}{
		{"U+001C, which Python's isspace takes, isn't whitespace", 0x1c, false},
		{"U+001F isn't whitespace", 0x1f, false},
		{"a zero-width space isn't whitespace", 0x200b, false},
		{"a byte-order mark isn't whitespace", 0xfeff, false},
		{"a no-break space is whitespace", 0xa0, true},
		{"next line, U+0085, is whitespace", 0x85, true},
		{"an ideographic space is whitespace", 0x3000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if IsSpace(c.r) != c.space {
				t.Error("wrong")
			}
		})
	}
}

func TestTokens(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"the empty text has no tokens", "", nil},
		{"words and the whitespace between them alternate", "fix the bug", []string{"fix", " ", "the", " ", "bug"}},
		{"CRLF and the spaces around it are one whitespace token", "a \r\n b", []string{"a", " \r\n ", "b"}},
		{"whitespace at both ends makes tokens of its own", "\t x\U00003000", []string{"\t ", "x", "\U00003000"}},
		{"U+001C joins the words around it", "a\x1cb", []string{"a\x1cb"}},
		{"a no-break space separates words", "a\U000000a0b", []string{"a", "\U000000a0", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Tokens(c.text); !slices.Equal(got, c.want) {
				t.Errorf("Tokens(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
	t.Run("the tokens of 2,000 random texts concatenate to the text, alternate, and are never empty", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(1, 2))
		alphabet := []string{"a", "b", " ", "\t", "\r\n", "\U00003000", "\x1c", "\U00004e2d", "\U0001f64f", "\U000000a0"}
		for i := 0; i < 2000; i++ {
			var b strings.Builder
			for j, n := 0, rng.IntN(12); j < n; j++ {
				b.WriteString(alphabet[rng.IntN(len(alphabet))])
			}
			text := b.String()
			toks := Tokens(text)
			if strings.Join(toks, "") != text {
				t.Fatalf("Tokens(%q) = %q doesn't concatenate to the text", text, toks)
			}
			for j, tok := range toks {
				if tok == "" || j > 0 && isSpaceToken(tok) == isSpaceToken(toks[j-1]) {
					t.Fatalf("Tokens(%q) = %q: token %d is empty or of its neighbour's kind", text, toks, j)
				}
				if strings.IndexFunc(tok, func(r rune) bool { return IsSpace(r) != isSpaceToken(tok) }) >= 0 {
					t.Fatalf("Tokens(%q): token %q mixes kinds", text, tok)
				}
			}
		}
	})
}

func TestLower(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		want rune
	}{
		{"A lowercases to a", 'A', 'a'},
		{"a digit has no mapping", '7', '7'},
		{"U+0130, capital I with dot, lowercases to plain i", 0x130, 'i'},
		{"capital sigma lowercases to small sigma, never final sigma", 0x3a3, 0x3c3},
		{"sharp s has no simple uppercase-to-lowercase change", 0xdf, 0xdf},
		{"capital sharp s lowercases to sharp s", 0x1e9e, 0xdf},
		{"the Kelvin sign lowercases to ASCII k", 0x212a, 'k'},
		{"the title-case letter U+01C5 lowercases to U+01C6", 0x1c5, 0x1c6},
		{"Cherokee U+13A0 lowercases to U+AB70", 0x13a0, 0xab70},
		{"Deseret U+10400 lowercases to U+10428", 0x10400, 0x10428},
		{"U+A7CB, which Unicode 16.0 maps, stays in version 1", 0xa7cb, 0xa7cb},
		{"U+1C89, which Unicode 16.0 maps, stays in version 1", 0x1c89, 0x1c89},
		{"the last run's last code point lowercases", 0x1e921, 0x1e943},
		{"the code point after the last run has no mapping", 0x1e922, 0x1e922},
		{"a code point inside a stride-2 run's span but off its stride has no mapping", 0x101, 0x101},
		{"a negative rune passes through", -1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Lower(c.r); got != c.want {
				t.Errorf("Lower(U+%04X) = U+%04X, want U+%04X", c.r, got, c.want)
			}
		})
	}
	t.Run("the runs are in ascending order, and none reaches past the next one's first", func(t *testing.T) {
		total := 0
		for i, run := range lowerRuns {
			total += int(run.count)
			if i > 0 {
				prev := lowerRuns[i-1]
				if last := prev.first + (prev.count-1)*prev.stride; last >= run.first {
					t.Errorf("run %d ends at U+%04X, at or past run %d's first U+%04X", i-1, last, i, run.first)
				}
			}
		}
		if len(lowerRuns) != 182 || total != 1433 {
			t.Errorf("%d runs of %d mappings, want 182 of 1,433", len(lowerRuns), total)
		}
	})
}

func TestSubstitute(t *testing.T) {
	cases := []struct {
		name, word, want string
	}{
		{"a path with a separator and a dot in its last part is a path", "src/main.go", "<path>"},
		{"a path that starts with ./ is a path", "./run.sh", "<path>"},
		{"a run with two separators is a path", "a/b/c", "<path>"},
		{"and/or isn't a path", "and/or", "and/or"},
		{"src/components isn't a path: one separator, no dot after it", "src/components", "src/components"},
		{"a path with a line number after it is one path", "src/nav.js:12", "<path>"},
		{"a Windows path is a path", "C:\\Users\\me\\x.txt", "<path>"},
		{"a home path is a path", "~/notes.txt", "<path>"},
		{"a URL runs to the end of the word", "(https://a.example/x),", "(<url>"},
		{"a URL scheme may hold + . -", "git+ssh://h/r", "<url>"},
		{"a backticked identifier is one id", "`handleSubmit`", "<id>"},
		{"double backticks leave the outer ones", "``x``", "`<id>`"},
		{"seven hex digits with a digit and a letter are hex", "3f9a2c1d", "<hex>"},
		{"0x and hex digits are hex", "0x7ffd", "<hex>"},
		{"0X counts like 0x", "0X7FFD", "<hex>"},
		{"a hex word with no digit stays", "deadbeef", "deadbeef"},
		{"0x alone is a number and a letter", "0x", "<n>x"},
		{"six hex digits aren't enough", "abc123", "abc<n>"},
		{"digit runs become <n>", "2026-11-19", "<n>-<n>-<n>"},
		{"a version keeps its dots", "v1.2.3", "v<n>.<n>.<n>"},
		{"an email address isn't a path", "teen@example.com", "teen@example.com"},
		{"non-ASCII letters pass through around a number", "\U000000e9t\U000000e94", "\U000000e9t\U000000e9<n>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := substitute(c.word); got != c.want {
				t.Errorf("substitute(%q) = %q, want %q", c.word, got, c.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name, text, want string
		removed          []int
	}{
		{"still broken becomes one retry, and a path its marker", "still broken, see src/nav.js:12", "<retry> see <path>", []int{}},
		{"retry phrases match without edge punctuation or apostrophes", "doesn\U00002019t work!! Still not working. fix it again? please retry", "<retry> <retry> <retry> <retry> please <retry>", []int{}},
		{"doesnt without an apostrophe is the same phrase", "it doesnt work", "it <retry>", []int{}},
		{"the longest phrase wins", "still not working", "<retry>", []int{}},
		{"try alone isn't a retry", "try harder", "try harder", []int{}},
		{"lowercasing is the simple mapping: U+0130 is i, a final capital sigma is a small sigma, and sharp s stays", "\U00000130STANBUL \U000003a3\U00000391\U000003a3 Stra\U000000dfe", "istanbul \U000003c3\U000003b1\U000003c3 stra\U000000dfe", []int{}},
		{"a fenced block goes with its fences", "look\n```js\nconst x = 1\n```\nwhy", "look why", []int{1, 2, 3}},
		{"a Go panic goes with its goroutine block, each frame and the file line under it", "panic: runtime error: index out of range [5] with length 3\n\ngoroutine 1 [running]:\nmain.main()\n\t/x/main.go:8 +0x1d\nhow do I solve this", "how do i solve this", []int{0, 1, 2, 3, 4}},
		{"after a panic, a blank line that no goroutine block follows stays", "panic: boom\n\nnot a goroutine\nstays", "not a goroutine stays", []int{0}},
		{"rustc's column-0 gutter line goes with its error, and the indented ones too", "error[E0308]: mismatched types\n --> src/main.rs:2:18\n  |\n2 |     let x: i32 = \"a\";\n  |            ^^^ expected `i32`\nwhy", "why", []int{0, 1, 2, 3, 4}},
		{"a gutter-like line after prose isn't error text", "see\n2 | x\nok", "see <n> x ok", []int{}},
		{"a JVM exception goes with its frames and its cause", "Exception in thread \"main\" java.lang.IllegalStateException: boom\n\tat Main.main(Main.java:3)\nCaused by: java.lang.NumberFormatException: x\n\t... 1 more\nhelp", "help", []int{0, 1, 2, 3}},
		{"a fence closes only with the same character, at least as long", "a\n~~~~\nb\n~~~\nc\n~~~~~\nd", "a d", []int{1, 2, 3, 4, 5}},
		{"an unclosed fence runs to the end", "a\n```\nb\nc", "a", []int{1, 2, 3}},
		{"a backtick fence whose line holds another backtick isn't a fence, and its backticks go as edge punctuation", "```js x = 1``` still here", "js x <n> still here", []int{}},
		{"four spaces of indent aren't a fence, and a word of nothing but punctuation goes", "    ```\nkept", "kept", []int{}},
		{"edge punctuation goes, so SM 6.5's 20:07 prompt is 20:02's text (spec 10.4 step 6)", "still overlapping on mobile, fix it!!", "still overlapping on mobile <retry>", []int{}},
		{"a placeholder at a word's edge keeps its angle brackets", "(see main.go:12) at 42. or <tag>", "see main.go:<n> at <n> or tag", []int{}},
		{"punctuation inside a word stays, and apostrophes go", "e.g. it's", "e.g its", []int{}},
		{"a pasted block goes with its wrapper lines", "<pasted_content id=\"1\">\nfoo\n</pasted_content>\nbar", "bar", []int{0, 1, 2}},
		{"a pasted block closes on Claude Code's closer, which repeats the opener's id (spec 10.4 P2)", "why\n\n<pasted_content id=\"1a2b\">\nfoo\n</pasted_content id=\"1a2b\">\nbar", "why bar", []int{2, 3, 4}},
		{"a traceback goes with its indented frames, then its exception line goes as an error", "x\nTraceback (most recent call last):\n  File \"a.py\", line 1\nValueError: bad\ny", "x y", []int{1, 2, 3}},
		{"an error line takes the indented lines after it", "TypeError: no\n    at f (a.js:1:2)\nwhy", "why", []int{0, 1}},
		{"CRLF splits lines like LF, and one CR goes", "a\r\nb\r\r\nc", "a b c", []int{}},
		{"prose with the word error isn't an error line", "the error is that Errors happen", "the error is that errors happen", []int{}},
		{"a whole-line error pattern must match the whole stripped line", "  FAILED tests/x.py::t  ", "", []int{0}},
		{"U+001C isn't whitespace, so it joins words", "fix\x1cit now", "fix\x1cit now", []int{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := Normalize(c.text)
			if n.Text() != c.want || !slices.Equal(n.RemovedLines, c.removed) && len(n.RemovedLines)+len(c.removed) > 0 {
				t.Errorf("Normalize(%q) gives %q removing lines %v, want %q removing %v", c.text, n.Text(), n.RemovedLines, c.want, c.removed)
			}
		})
	}
}

func TestErrorSignature(t *testing.T) {
	cases := []struct {
		name, text, want string
		ok               bool
	}{
		{"the first error line is the signature, substituted", "fix this\nTypeError: x is undefined at 0x7ffd", "TypeError: x is undefined at <hex>", true},
		{"a signature isn't lowercased", "KeyError: 'User_ID'", "KeyError: 'User_ID'", true},
		{"an error inside a fence still gives the signature", "it fails:\n```\nKeyError: 'user_id'\n```", "KeyError: 'user_id'", true},
		{"a traceback's signature is its exception line", "Traceback (most recent call last):\n  File \"x.py\", line 1\nNameError: name 'y' is not defined", "NameError: name 'y' is not defined", true},
		{"a Rust error gives its signature", "error[E0308]: mismatched types", "error[E<n>]: mismatched types", true},
		{"a compiler error's line and column become numbers", "main.c:3:5: error: 'x' undeclared", "main.c:<n>:<n>: error: 'x' undeclared", true},
		{"a Go panic gives its signature", "panic: runtime error: index out of range [5] with length 3", "panic: runtime error: index out of range [<n>] with length <n>", true},
		{"the JVM's thread prefix is part of the error line", "Exception in thread \"main\" java.lang.IllegalStateException: boom", "Exception in thread \"main\" java.lang.IllegalStateException: boom", true},
		{"an uncaught promise rejection is an error line", "Uncaught (in promise) TypeError: Failed to fetch", "Uncaught (in promise) TypeError: Failed to fetch", true},
		{"prose has no signature", "the error is that terror and Errors happen, error: none", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, ok := ErrorSignature(c.text); got != c.want || ok != c.ok {
				t.Errorf("ErrorSignature(%q) = %q, %v; want %q, %v", c.text, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestShingles(t *testing.T) {
	cases := []struct {
		name  string
		words []string
		kind  ShingleKind
		want  []string
	}{
		{"six words or more give word 3-shingles", []string{"make", "the", "button", "a", "bit", "bigger"}, Word3,
			[]string{"a bit bigger", "button a bit", "make the button", "the button a"}},
		{"under six words give character 5-shingles of the normalized text, in ascending order", []string{"a", "b", "c", "d", "e"}, Char5,
			[]string{" b c ", " c d ", "a b c", "b c d", "c d e"}},
		{"a repeated shingle counts once", []string{"a", "a", "a", "a", "a", "a", "a"}, Word3, []string{"a a a"}},
		{"a text of one to four code points is its own shingle", []string{"ok"}, Char5, []string{"ok"}},
		{"character shingles count code points, not bytes", []string{"\U00004fee\U0000590d\U00008fd9\U00004e2a"}, Char5,
			[]string{"\U00004fee\U0000590d\U00008fd9\U00004e2a"}},
		{"the empty text has no shingles", nil, Char5, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, got := Shingles(c.words)
			if kind != c.kind || !slices.Equal(got, c.want) {
				t.Errorf("Shingles(%q) = %s %q, want %s %q", c.words, kind, got, c.kind, c.want)
			}
		})
	}
}

func TestSketch(t *testing.T) {
	empty := EmptySketch()
	var a, b Sketch
	for i := range a {
		a[i] = uint32(i)
		b[i] = uint32(i)
	}
	for i := 0; i < 12; i++ {
		b[i] = 0xdead
	}
	c := b
	c[12] = 0xdead
	text, err := a.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	var back Sketch
	cases := []struct {
		name string
		ok   bool
	}{
		{"the empty set's sketch is 0xffffffff in all 64 places", strings.Count(empty.String(), "f") == 512},
		{"two empty sketches match everywhere", empty.Matches(EmptySketch()) == SketchSize},
		{"52 matches pass a threshold of 0.8, which is 4/5", a.Matches(b) == 52 && Similar(a, b, 4, 5)},
		{"51 matches don't pass 0.8", a.Matches(c) == 51 && !Similar(a, c, 4, 5)},
		{"a sketch's text form is 512 lowercase hex digits and reads back", len(text) == 512 && back.UnmarshalText(text) == nil && back == a},
		{"uppercase hex isn't a sketch", back.UnmarshalText([]byte(strings.ToUpper(string(text)))) != nil},
		{"255 bytes aren't a sketch", func() bool { _, err := SketchFrom(make([]byte, 255)); return err != nil }()},
		{"no shingles give the empty sketch", SketchOf(nil) == empty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Error("doesn't hold")
			}
		})
	}
}

func TestCheckPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		ok   bool
	}{
		{"a path relative to the project root is in form", "src/nav.js", true},
		{"a component may hold spaces and leading dots", "with space/..x/x..", true},
		{"an absolute path outside the project is in form", "/tmp/x", true},
		{"a Windows path with an upper-case drive and / is in form", "C:/Users/x", true},
		{"a lower-case drive isn't in form", "c:/x", false},
		{"a drive without / isn't in form", "C:x", false},
		{"a drive alone isn't in form", "C:/", false},
		{"a letter and : always start a Windows path", "x:/y", false},
		{"an empty component isn't in form", "a//b", false},
		{"a . component isn't in form", "a/./b", false},
		{"a .. component isn't in form", "a/../b", false},
		{"a trailing / isn't in form", "a/", false},
		{"the empty path isn't in form", "", false},
		{"/ alone isn't in form", "/", false},
		{"a backslash is never in form", "a\\b", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckPath(c.path)
			if (err == nil) != c.ok {
				t.Errorf("CheckPath(%q) = %v", c.path, err)
			}
			if err != nil && (!errors.Is(err, ErrPath) || c.path != "" && strings.Contains(err.Error(), c.path)) {
				t.Errorf("the error %q doesn't wrap ErrPath or quotes the path", err)
			}
		})
	}
}

func TestCommandText(t *testing.T) {
	cases := []struct {
		name, cmd, want string
	}{
		{"runs of whitespace collapse to one space, none at either end", "  npm  test\t-- --watch\n", "npm test -- --watch"},
		{"case and quotes stay", "git commit -m \"Fix It\"", "git commit -m \"Fix It\""},
		{"a command that is all whitespace is empty", " \t\U00003000 ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CommandText(c.cmd); got != c.want {
				t.Errorf("CommandText(%q) = %q, want %q", c.cmd, got, c.want)
			}
		})
	}
}

func TestKeyedValues(t *testing.T) {
	var kf [KeySize]byte
	copy(kf[:], "a fixed K_feat for these tests..")
	k := NewKey(kf)
	es, _ := k.ErrorSig("TypeError: x")
	ch, _ := k.Command("TypeError: x")
	_, noCmd := k.Command(" \t ")
	_, noSig := k.ErrorSig("all good")
	t1, t2 := k.Template("The navbar STILL overlaps"), k.Template("the navbar still overlaps")
	js, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"an error signature and a command with the same text get different values", es != ch},
		{"a command that is all whitespace has no value", !noCmd},
		{"a text with no error line has no signature value", !noSig},
		{"two prompts that normalize alike have the same template", t1 == t2},
		{"a value's text form is 32 lowercase hex digits, an opaque key", len(es.String()) == 32 && strings.ToLower(es.String()) == es.String()},
		{"encoding/json writes a Key as {}", string(js) == "{}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Error("doesn't hold")
			}
		})
	}
}

func TestPromptAndToolFeatures(t *testing.T) {
	var kf [KeySize]byte
	k := NewKey(kf)
	write := func(f F, err error) string {
		if err != nil {
			return "error"
		}
		js, err := json.Marshal(f)
		if err != nil {
			return "error"
		}
		return string(js)
	}
	empty := EmptySketch()
	th := k.Template("")
	ch, _ := k.Command("npm test")
	cases := []struct {
		name, got, want string
	}{
		{"an empty prompt has the empty sketch, a template and its class", write(k.Prompt("", LowEffort)),
			`{"v":1,"mh":"` + empty.String() + `","th":"` + th.String() + `","pc":"low_effort"}`},
		{"a prompt with no class SM 2.2 knows is refused", write(k.Prompt("fix it", "great")), "error"},
		{"a read has an f that holds only v", write(k.Tool(nil, "")), `{"v":1}`},
		{"a tool that ran a command has ch", write(k.Tool(nil, " npm\ttest ")), `{"v":1,"ch":"` + ch.String() + `"}`},
		{"a hunk whose new range has lines but starts at 0 is refused", write(k.Tool([]Edit{{Path: "a.go", OldStart: 1, OldLines: 1, NewLines: 1}}, "")), "error"},
		{"a hunk number past 2^31 - 1 is refused", write(k.Tool([]Edit{{Path: "a.go", NewStart: 1 << 31}}, "")), "error"},
		{"a shell.command with no command has an f that holds only v", write(k.Shell(""), nil), `{"v":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %s, want %s", c.got, c.want)
			}
		})
	}
	t.Run("Carries names prompt.submit, agent.tool, shell.command and cli.track, and no other kind", func(t *testing.T) {
		for _, kind := range event.Kinds() {
			want := kind == event.KindPromptSubmit || kind == event.KindAgentTool || kind == event.KindShellCommand || kind == event.KindCLITrack
			if Carries(kind) != want {
				t.Errorf("Carries(%s) = %v", kind, !want)
			}
		}
	})
}

func TestShingleEntryJSON(t *testing.T) {
	v := strings.Repeat("ab", 32)
	cases := []struct {
		name string
		json string
		ok   bool
	}{
		{"an entry with its shingle is read", `{"v":"` + v + `","x":"203c706174"}`, true},
		{"an entry with an empty shingle is read, and VerifyShingleReveal refuses it", `{"v":"` + v + `","x":""}`, true},
		{"an entry without its shingle is refused: a hidden shingle sends nothing", `{"v":"` + v + `"}`, false},
		{"an entry with a member besides v and x is refused", `{"v":"` + v + `","x":"20","y":1}`, false},
		{"an entry whose v is spelled V is refused", `{"V":"` + v + `","x":"20"}`, false},
		{"an entry whose x is spelled X is refused", `{"v":"` + v + `","X":"20"}`, false},
		{"an entry with uppercase hex is refused", `{"v":"` + strings.ToUpper(v) + `","x":"20"}`, false},
		{"an entry with a null x is refused", `{"v":"` + v + `","x":null}`, false},
		{"an entry with no v is refused", `{"x":"20"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e ShingleEntry
			if err := json.Unmarshal([]byte(c.json), &e); (err == nil) != c.ok {
				t.Fatalf("Unmarshal = %v", err)
			}
		})
	}
	t.Run("an entry that shows an empty shingle is bad_entry", func(t *testing.T) {
		var e ShingleEntry
		if err := json.Unmarshal([]byte(`{"v":"`+v+`","x":""}`), &e); err != nil {
			t.Fatal(err)
		}
		if got, at, _ := VerifyShingleReveal(EmptySketch(), []ShingleEntry{e}, false); got != ShingleBadEntry || at != 0 {
			t.Errorf("verdict %s at %d", got, at)
		}
	})
	t.Run("a reveal of no entries matches the empty sketch", func(t *testing.T) {
		if got, _, unexplained := VerifyShingleReveal(EmptySketch(), nil, false); got != ShingleOK || unexplained != 0 {
			t.Errorf("verdict %s, %d places unexplained", got, unexplained)
		}
	})
	t.Run("an entry writes and reads back as v and x", func(t *testing.T) {
		in := ShingleEntry{V: bytes.Repeat([]byte{0xab}, 32), X: []byte(" <pat")}
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out ShingleEntry
		if err := json.Unmarshal(data, &out); err != nil || !bytes.Equal(out.V, in.V) || !bytes.Equal(out.X, in.X) {
			t.Errorf("%s reads back as %+v, %v", data, out, err)
		}
	})
}

func TestShingleReveal(t *testing.T) {
	k := NewKey([KeySize]byte{1, 2, 3})
	all := func(text string) []string {
		_, xs := Shingles(Normalize(text).Words)
		return xs
	}
	cases := []struct {
		name    string
		text    string
		removed []int
		shown   []string
	}{
		{"with nothing removed, every shingle is shown", "fix it for bob", nil, all("fix it for bob")},
		{"spec 10.8's example: with bob removed, the shingles touching it send nothing", "fix it for bob", []int{6},
			[]string{" for ", "<retr", "> for", "etry>", "retry", "ry> f", "try> ", "y> fo"}},
		{"a <retry> comes from every token it replaced, so removing one of them hides it", "fix it for bob", []int{2},
			[]string{" for ", "for b", "or bo", "r bob"}},
		{"a removed whitespace token makes no word, and nothing touches it", "fix it for bob", []int{5}, all("fix it for bob")},
		{"a removed token on a line that step 2 removes touches nothing",
			"fix the login page\nTypeError: bob is undefined", []int{10}, all("fix the login page\nTypeError: bob is undefined")},
		{"a word shingle is hidden when every run of three words that makes it holds a removed word",
			"please make the header sticky and ask bob about the footer", []int{14},
			[]string{"about the footer", "header sticky and", "make the header", "please make the", "sticky and ask", "the header sticky"}},
		{"everything removed: nothing is shown", "fix it for bob", []int{0, 1, 2, 3, 4, 5, 6}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, entries := k.ShingleReveal(c.text, c.removed)
			wantKind, _ := Shingles(Normalize(c.text).Words)
			if kind != wantKind {
				t.Errorf("kind %s, want %s", kind, wantKind)
			}
			var got []string
			for i, e := range entries {
				if v := k.ShingleValue(kind, string(e.X)); !bytes.Equal(e.V, v[:]) {
					t.Errorf("entry %d of %q has another value", i, e.X)
				}
				if i > 0 && bytes.Compare(entries[i-1].V, e.V) >= 0 {
					t.Errorf("entry %d isn't above the one before", i)
				}
				got = append(got, string(e.X))
			}
			slices.Sort(got)
			if !slices.Equal(got, c.shown) {
				t.Errorf("shows %q, want %q", got, c.shown)
			}
			removed := len(c.removed) > 0
			if v, at, _ := VerifyShingleReveal(k.Sketch(c.text), entries, removed); v != ShingleOK {
				t.Errorf("the client's own reveal gets %s at %d", v, at)
			}
		})
	}
}
