// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// TestReferenceCheckContent writes 1,000 random texts with random span
// removals, as commitments and reveals, together with their
// normalizations, feature maps and CC8 reveals and a set of random draws,
// in the vectors format, and has tools/ledger_ref.py, which shares no code
// with this package, recompute every value and verdict in them (WP-LG4).
// It needs python3 and fails without it.
func TestReferenceCheckContent(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is missing, and this test runs tools/ledger_ref.py with it: %v", err)
	}
	doc := randomContent(t)
	texts := 0
	for _, c := range doc.Commitments {
		if !strings.HasPrefix(c.Note, "long") {
			texts++
		}
	}
	if texts != 1000 {
		t.Fatalf("wrote %d random texts, want 1,000", texts)
	}

	// Small slices of the same file, each with one value changed, to show
	// the check really reads every section this test writes.
	small := func() contentVectorsFile {
		s := contentVectorsFile{Format: vectorsFormat,
			Commitments: doc.Commitments[:5], Reveals: doc.Reveals[:5], Normalizations: doc.Normalizations[:5],
			Features: doc.Features[:5], ShingleReveals: doc.ShingleReveals[:5], Draws: doc.Draws[:3]}
		return deepCopyContent(t, s)
	}
	rootChanged := small()
	rootChanged.Commitments[1].Root[0] ^= 0x01
	verdictChanged := small()
	verdictChanged.Reveals[2].Verdict = RevealRoot
	normalizedChanged := small()
	normalizedChanged.Normalizations[3].Normalized = append(normalizedChanged.Normalizations[3].Normalized, 'x')
	fChanged := small()
	fChanged.Features[0].F = json.RawMessage(strings.Replace(string(fChanged.Features[0].F), `"pc":"`, `"pc":"x`, 1))
	drawChanged := small()
	for i := range drawChanged.Draws {
		if d := drawChanged.Draws[i].Drawn; len(d) >= 2 {
			d[0], d[1] = d[1], d[0]
			break
		}
	}

	cases := []struct {
		name string
		doc  contentVectorsFile
		want int
	}{
		{"1,000 random texts with random removals, and their features, CC8 reveals and draws, pass tools/ledger_ref.py check", doc, 0},
		{"tools/ledger_ref.py check fails the same commitments with one root byte changed", rootChanged, 1},
		{"tools/ledger_ref.py check fails an ok reveal written down as failing on its root", verdictChanged, 1},
		{"tools/ledger_ref.py check fails a normalized text with a byte added", normalizedChanged, 1},
		{"tools/ledger_ref.py check fails a feature map whose prompt class was changed", fChanged, 1},
		{"tools/ledger_ref.py check fails a draw with two drawn turns swapped", drawChanged, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "content.json")
			data, err := json.Marshal(c.doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(python, "-I", filepath.Join("..", "..", "tools", "ledger_ref.py"), "check", path)
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("running tools/ledger_ref.py: %v", err)
				}
				code = exit.ExitCode()
			}
			if code != c.want {
				t.Fatalf("tools/ledger_ref.py check exited %d, want %d:\n%s", code, c.want, out)
			}
			lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
			t.Logf("%d bytes checked: %s", len(data), lines[len(lines)-1])
		})
	}
}

func deepCopyContent(t *testing.T, v contentVectorsFile) contentVectorsFile {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out contentVectorsFile
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Words, separators and whole lines that random texts are made of. They
// reach every rule of spec 10.4: retry phrases with their punctuation,
// URLs, identifiers, paths and things that aren't, hex runs and numbers,
// letters whose lowercase Unicode 15.0.0 and later versions disagree on,
// letters that lowercase into ASCII, code points that look like whitespace
// and aren't, and every fence, paste and error-line pattern.
var (
	sampleWords = []string{
		"fix", "the", "navbar", "on", "mobile,", "mobile!!", "it", "It", "still", "STILL", "Still", "broken", "broken,",
		"not", "working", "working.", "try", "Try", "again", "again?", "Again!", "retry", "doesnt", "doesn't",
		"doesn\U00002019t", "work", "work!!", "please", "why", "does", "\U00002018again\U00002019", "\U0000201cretry\U0000201d",
		"(again)", "'fix'", "it.", "src/app/main.go", "./run.sh", "../x/y", "~/notes.txt", `~\notes.txt`,
		`C:\Users\me\x.txt`, "c:/tmp", "and/or", "src/components", "a/b/c", "e.g./i.e.", "$HOME/.config/app",
		"localhost:8080/api", "/", `\`, "a/.b", "x/y.z", "foo/bar.", "@scope/pkg", "100%/x", "https://example.com/x?y=1",
		"(see", "https://a.example/x),", "<ftp://b.example>,", "HTTP://C.EXAMPLE/Y", "git+ssh://h/r", "xhttps://a/b",
		"a://", "://x", "1http://x", "`useEffect`", "`fooBar`", "``x``", "`two", "words`", "`a`b`c`", "`", "``",
		"3f9a2c1d", "deadbeef", "facade1", "abc123", "abc1234", "0x1F", "0X7FFD", "0x", "0xg1", "1234567", "a1b2c3d",
		"v1.2.3", "2026-11-19", "42", "1.", "007", "x86_64", "sha256:3f9a2c1d4e",
		"\U000000c9COLE", "stra\U000000dfe", "\U00000130stanbul", "\U000003a3\U00000391\U000003a3",
		"\U00000415\U00000449\U00000451", "\U00000440\U00000430\U00000437", "\U0000212a", "\U00002126", "\U0000212b",
		"\U000013a0\U000013f0", "\U00001c90", "\U00010400", "\U0001e900", "\U000001c5", "\U00001e9e", "\U000003f4",
		"\U0000fb00", "\U00001c89", "\U0000a7cb", "\U0000a7dc", "\U00010d50", "\U00016ea0", "\U0001f64f",
		"\U00004fee\U0000590d\U00008fd9\U00004e2a\U00009519\U00008bef", "na\U000000efve", "e\U00000301", "\U00002014",
		"zero\U0000200bwidth", "\U0000feffbom", "x\x1cy", "soft\U000000adhyphen", "TypeError:", "Error", "Exception:",
		"panic:", "FAILED", "---", "FAIL:", "error[E0308]:", "main.c:3:5:", "error:", "fatal", "Uncaught",
		"ReferenceError:", "x", "is", "undefined", "sk-test-51Hx", "teen@example.com", "```", "~~~",
		"<pasted_content>", "</pasted_content>",
	}
	// sampleSpaces separate words within a line. Some aren't whitespace
	// in spec 10.2's sense, so they join words instead.
	sampleSpaces = []string{
		" ", " ", " ", " ", "  ", "\t", " \t ", "\r", "\x0b", "\x0c", "\U00000085", "\U000000a0", "\U00001680",
		"\U00002000", "\U0000200a", "\U00002028", "\U00002029", "\U0000202f", "\U0000205f", "\U00003000",
		"\x1c", "\U0000200b",
	}
	sampleLines = []string{
		"```", "```python", "```js const x = 1```", "~~~~", "~~~", "    ```", "   ```", "``` ", "````", "~~~~~ ",
		"```\t", "<pasted_content>", `<pasted_content id="1">`, `  <pasted_content id="2" lines="3">  `,
		"</pasted_content>", " </pasted_content> ", "<pasted_content id>", `<pasted_content id="1a2b">`,
		`</pasted_content id="1a2b">`, `</pasted_content id="ffff"> `, "</pasted_content id>", "Traceback (most recent call last):",
		`  File "app.py", line 3, in <module>`, "    main()", "\tat x", " at App (App.jsx:12:5)",
		"ValueError: invalid literal for int() with base 10: 'abc'",
		"TypeError: Cannot read properties of undefined (reading 'map')", "Uncaught ReferenceError: foo is not defined",
		"My.Custom.Exception: boom", "KeyError: 'user_id'", "Error", "SomethingError", "error: none", "Errors happen",
		"error[E0308]: mismatched types", " --> src/main.rs:4:5", "main.c:3:5: error: 'x' undeclared",
		"a.go:1:2: fatal error: x.h: no such file", "FAILED tests/test_api.py::test_login - AssertionError: 401 != 200",
		"FAILED ", "--- FAIL: TestParse (0.00s)", "    parse_test.go:14: got 3, want 4",
		"panic: runtime error: index out of range [5] with length 3", "panic: ", "goroutine 1 [running]:",
		"\U00003000TypeError: x\U00003000", "\U000000a0FAILED x 0x7ffd",
	}
	lineBreaks = []string{"\n", "\n", "\r\n", "\r\n\r\n", "\n\n"}
)

// randomText returns a random text of a few lines, each a whole sample
// line or random words, sometimes with whitespace at either end.
func randomText(rng *rand.Rand) string {
	var b strings.Builder
	if rng.IntN(5) == 0 {
		b.WriteString(pick(rng, sampleSpaces))
	}
	lines := 1 + rng.IntN(6)
	for i := 0; i < lines; i++ {
		if i > 0 {
			b.WriteString(pick(rng, lineBreaks))
		}
		if rng.IntN(3) == 0 {
			b.WriteString(pick(rng, sampleLines))
			continue
		}
		for j, n := 0, rng.IntN(11); j < n; j++ {
			if j > 0 {
				b.WriteString(pick(rng, sampleSpaces))
			}
			b.WriteString(pick(rng, sampleWords))
		}
	}
	if rng.IntN(5) == 0 {
		b.WriteString(pick(rng, append(sampleSpaces, lineBreaks...)))
	}
	return b.String()
}

// longMessage returns an agent message of more than 16 KiB whose cut falls
// next to or inside a multi-byte character.
func longMessage(rng *rand.Rand) []byte {
	var b strings.Builder
	for b.Len() < MessageMax-40 {
		b.WriteString(randomText(rng) + "\n")
	}
	for b.Len() <= MessageMax+8 {
		b.WriteString(pick(rng, []string{"a", "\U000000e9", "\U00004e2d", "\U0001f64f", " "}))
	}
	return []byte(b.String())
}

// randomSpans returns up to three disjoint spans over n tokens, of random
// types, code spans with or without a line count.
func randomSpans(rng *rand.Rand, n int) []Span {
	var spans []Span
	for j := 0; j < n && len(spans) < 3; {
		j += rng.IntN(4)
		if j >= n {
			break
		}
		sp := Span{First: j, Count: 1 + rng.IntN(min(3, n-j)), Type: pick(rng, []SpanType{SpanKnown, SpanSecret, SpanPII, SpanCode, SpanTeen})}
		if sp.Type == SpanCode && rng.IntN(4) > 0 {
			sp.Lines = 1 + rng.IntN(9)
		}
		spans = append(spans, sp)
		j += sp.Count + rng.IntN(2)
	}
	return spans
}

// cloneItems returns a deep copy of a reveal.
func cloneItems(items []RevealItem) []RevealItem {
	out := make([]RevealItem, len(items))
	for i, it := range items {
		out[i] = it
		out[i].Salt = bytes.Clone(it.Salt)
		out[i].Token = bytes.Clone(it.Token)
		out[i].Leaves = make([][]byte, len(it.Leaves))
		for j, h := range it.Leaves {
			out[i].Leaves[j] = bytes.Clone(h)
		}
		if it.Lines != nil {
			l := *it.Lines
			out[i].Lines = &l
		}
	}
	return out
}

// mutateReveal returns a copy of items with one random change, or false
// when items has nothing that change applies to.
func mutateReveal(rng *rand.Rand, items []RevealItem) ([]RevealItem, bool) {
	m := cloneItems(items)
	var kept, spans []int
	for i, it := range m {
		if it.Removed {
			spans = append(spans, i)
		} else {
			kept = append(kept, i)
		}
	}
	switch rng.IntN(10) {
	case 0: // a kept token changed, keeping its kind
		if len(kept) == 0 {
			return nil, false
		}
		it := &m[pick(rng, kept)]
		if space, _ := tokenKind(it.Token); space {
			it.Token = append(it.Token, ' ')
		} else {
			it.Token = append(it.Token, 'x')
		}
	case 1: // a salt bit flipped
		if len(kept) == 0 {
			return nil, false
		}
		m[pick(rng, kept)].Salt[rng.IntN(HashSize)] ^= 1 << rng.IntN(8)
	case 2: // the last item dropped
		if len(m) == 0 {
			return nil, false
		}
		m = m[:len(m)-1]
	case 3: // a span that claims one more token than it has leaves
		if len(spans) == 0 {
			return nil, false
		}
		m[pick(rng, spans)].Count++
	case 4: // a span type that isn't in the table
		if len(spans) == 0 {
			return nil, false
		}
		m[pick(rng, spans)].Type = "email"
	case 5: // a line count on a span that isn't code, or of 0 lines on one that is
		if len(spans) == 0 {
			return nil, false
		}
		it := &m[pick(rng, spans)]
		lines := uint64(rng.IntN(3))
		if it.Type == SpanCode {
			lines = 0
		}
		it.Lines = &lines
	case 6: // two neighbouring kept tokens swapped
		if len(kept) < 2 {
			return nil, false
		}
		i := rng.IntN(len(kept) - 1)
		m[kept[i]], m[kept[i+1]] = m[kept[i+1]], m[kept[i]]
	case 7: // a leaf cut to 31 bytes
		if len(spans) == 0 {
			return nil, false
		}
		it := &m[pick(rng, spans)]
		j := rng.IntN(len(it.Leaves))
		it.Leaves[j] = it.Leaves[j][:HashSize-1]
	case 8: // a kept token emptied
		if len(kept) == 0 {
			return nil, false
		}
		m[pick(rng, kept)].Token = nil
	default: // a kept token of both kinds at once
		if len(kept) == 0 {
			return nil, false
		}
		it := &m[pick(rng, kept)]
		it.Token = append(it.Token, " x"...)
	}
	return m, true
}

var sampleComponents = []string{"src", "web", "app", "main.go", "nav.js", ".github", "x.y.z", "Makefile", "with space",
	"\U000000fcn\U000000efcode", "a-b_c", "..x", "x..", "~", "$HOME"}

// randomEdit returns an edit hunk in spec 10.6's form: a path relative to
// the project, absolute, or a Windows path, and numbers that follow the
// hunk header's rules.
func randomEdit(rng *rand.Rand) features.Edit {
	parts := make([]string, 1+rng.IntN(4))
	for i := range parts {
		parts[i] = pick(rng, sampleComponents)
	}
	path := strings.Join(parts, "/")
	switch rng.IntN(5) {
	case 0:
		path = "/" + path
	case 1:
		path = string(rune('A'+rng.IntN(26))) + ":/" + path
	}
	side := func() (start, lines int) {
		lines = rng.IntN(4) * rng.IntN(20)
		start = rng.IntN(5000)
		if lines > 0 {
			start++
		}
		return start, lines
	}
	e := features.Edit{Path: path}
	e.OldStart, e.OldLines = side()
	e.NewStart, e.NewLines = side()
	if rng.IntN(50) == 0 {
		e.OldStart, e.NewStart = 1<<31-1, 1<<31-1
	}
	return e
}

var sampleCommandWords = []string{"npm", "test", "--", "--watch", "pytest", "-k", "test_login", "go", "./...", "ls", "-la",
	"git", "commit", "-m", `"fix it"`, "C:\\x", "\U000000e9"}

// randomCommand returns a command line with random whitespace in and
// around it, or "" now and then.
func randomCommand(rng *rand.Rand) string {
	if rng.IntN(6) == 0 {
		return pick(rng, []string{"", " ", "\t\n"})
	}
	var b strings.Builder
	for i, n := 0, 1+rng.IntN(5); i < n; i++ {
		b.WriteString(pick(rng, []string{"", " ", "  ", "\t", "\n", "\U00003000"}))
		b.WriteString(pick(rng, sampleCommandWords))
	}
	b.WriteString(pick(rng, []string{"", " ", "\n"}))
	return b.String()
}

// randomContent writes the file TestReferenceCheckContent checks, checking
// each value with this package's own verifiers on the way. The seed is
// fixed, so a failure reproduces.
func randomContent(t *testing.T) contentVectorsFile {
	t.Helper()
	rng := rand.New(rand.NewPCG(20261119, 4))
	kDev := make([]byte, KeySize)
	for i := range kDev {
		kDev[i] = byte(rng.Uint32())
	}
	k, err := DeriveKeys(kDev)
	if err != nil {
		t.Fatal(err)
	}
	fk := k.Features()
	doc := contentVectorsFile{Format: vectorsFormat, Note: "Written by pkg/ledger's TestReferenceCheckContent from a fixed seed."}
	classes := []features.Class{features.Substantive, features.LowEffort, features.Answer, features.ErrorPaste}
	var sketches []hexBytes

	commit := func(note string, rtype RType, raw []byte, withTokens bool) *Opening {
		text := raw
		if rtype == 0x04 {
			text = CommittedMessage(raw)
		}
		seq := 1 + rng.Uint64N(1<<40)
		o, err := k.Open(seq, text)
		if err != nil {
			t.Fatal(err)
		}
		c := o.Commitment()
		cv := commitmentVec{Note: note, KDev: kDev, Seq: seq, RType: uint8(rtype), Text: text, N: c.N, Root: hashBytes(c.R)}
		if len(raw) > MessageMax {
			cv.Raw = raw
		}
		if withTokens {
			cv.Tokens = []tokenVec{}
			for j := range o.tokens {
				cv.Tokens = append(cv.Tokens, tokenVec{Token: o.tokens[j], Salt: hashBytes(o.salts[j]), Leaf: hashBytes(o.leaves[j])})
			}
		}
		doc.Commitments = append(doc.Commitments, cv)
		return o
	}

	for i := 0; i < 1000; i++ {
		text := randomText(rng)
		rtype := pick(rng, []RType{0x03, 0x04, 0x12})
		o := commit("random text", rtype, []byte(text), i%10 == 0)
		c := o.Commitment()
		spans := randomSpans(rng, o.Len())
		items, err := o.Reveal(spans)
		if err != nil {
			t.Fatal(err)
		}
		if v, at := VerifyReveal(c, items); v != RevealOK {
			t.Fatalf("text %d: pkg/ledger's own reveal gets %s at item %d", i, v, at)
		}
		doc.Reveals = append(doc.Reveals, revealVec{N: c.N, Root: hashBytes(c.R), Items: items, Verdict: RevealOK})
		if i%3 == 0 {
			if m, ok := mutateReveal(rng, items); ok {
				v, _ := VerifyReveal(c, m)
				if v == RevealOK {
					t.Fatalf("text %d: a changed reveal still verifies", i)
				}
				doc.Reveals = append(doc.Reveals, revealVec{Note: "changed", N: c.N, Root: hashBytes(c.R), Items: m, Verdict: v})
			}
		}

		norm := features.Normalize(text)
		kind, shingles := features.Shingles(norm.Words)
		nv := normalizationVec{Text: []byte(text), RemovedLines: norm.RemovedLines, Normalized: []byte(norm.Text()),
			ShingleKind: kind.String(), Shingles: []hexBytes{}}
		for _, x := range shingles {
			nv.Shingles = append(nv.Shingles, []byte(x))
		}
		if sig, ok := features.ErrorSignature(text); ok {
			s := hexBytes(sig)
			nv.Signature = &s
		}
		doc.Normalizations = append(doc.Normalizations, nv)

		class := pick(rng, classes)
		f, err := fk.Prompt(text, class)
		if err != nil {
			t.Fatal(err)
		}
		fJSON, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		_, valued := fk.Shingles(text)
		values := []hexBytes{}
		for _, s := range valued {
			values = append(values, bytes.Clone(s.V[:]))
		}
		textHex := hexBytes(text)
		doc.Features = append(doc.Features, featureVec{KDev: kDev, RType: uint8(pick(rng, []RType{0x03, 0x12})),
			Text: &textHex, PromptClass: &class, ShingleValues: &values, F: fJSON})
		sketches = append(sketches, f.MH.Bytes())

		if i%3 == 1 {
			hide := func(string) bool { return rng.IntN(3) == 0 }
			entries := fk.ShingleReveal(text, hide)
			v, at := features.VerifyShingleReveal(*f.MH, entries)
			if v != features.ShingleOK {
				t.Fatalf("text %d: pkg/ledger's own CC8 reveal gets %s at entry %d", i, v, at)
			}
			doc.ShingleReveals = append(doc.ShingleReveals, shingleRevealVec{MH: f.MH.Bytes(), Entries: entries, Verdict: v})
			if len(entries) > 0 {
				// One entry dropped: a sketch error unless its value set
				// no minimum. Whatever pkg/ledger says, the reference
				// must say too.
				j := rng.IntN(len(entries))
				dropped := append(append([]features.ShingleEntry{}, entries[:j]...), entries[j+1:]...)
				v, _ := features.VerifyShingleReveal(*f.MH, dropped)
				doc.ShingleReveals = append(doc.ShingleReveals, shingleRevealVec{Note: "one entry dropped", MH: f.MH.Bytes(), Entries: dropped, Verdict: v})
			}
		}
	}
	for i := 0; i < 8; i++ {
		commit("long agent message", 0x04, longMessage(rng), false)
	}
	for i := 0; i < 300; i++ {
		a, b := pick(rng, sketches), pick(rng, sketches)
		sa, _ := features.SketchFrom(a)
		sb, _ := features.SketchFrom(b)
		doc.Jaccard = append(doc.Jaccard, jaccardVec{A: a, B: b, Matches: sa.Matches(sb)})
	}

	for i := 0; i < 200; i++ {
		cmd := randomCommand(rng)
		var cmdHex *hexBytes
		if cmd != "" || rng.IntN(2) == 0 {
			h := hexBytes(cmd)
			cmdHex = &h
		}
		if i%4 == 3 {
			raw, err := json.Marshal(fk.Shell(cmd))
			if err != nil {
				t.Fatal(err)
			}
			doc.Features = append(doc.Features, featureVec{KDev: kDev, RType: 0x0c, Command: cmdHex, F: raw})
			continue
		}
		var edits []features.Edit
		hunks := []hunkVec{}
		for j, n := 0, rng.IntN(4); j < n; j++ {
			e := randomEdit(rng)
			edits = append(edits, e)
			hunks = append(hunks, hunkVec{Path: e.Path, OldStart: e.OldStart, OldLines: e.OldLines, NewStart: e.NewStart, NewLines: e.NewLines})
		}
		tf, err := fk.Tool(edits, cmd)
		if err != nil {
			t.Fatalf("edits %+v: %v", edits, err)
		}
		raw, err := json.Marshal(tf)
		if err != nil {
			t.Fatal(err)
		}
		doc.Features = append(doc.Features, featureVec{KDev: kDev, RType: 0x05, Hunks: &hunks, Command: cmdHex, F: raw})
	}

	for i, size := range []int{0, 1, 2, 5, 9, 10, 11, 37, 99, 100, 101, 110, 111, 150, 289, 290, 291, 300, 350, 12} {
		var r Hash
		for j := range r {
			r[j] = byte(rng.Uint32())
		}
		metadata := []byte(randomText(rng))
		seed := DrawSeed(r, metadata)
		devices := []string{randKey(rng, 1+rng.IntN(MaxKeyLen)), "d_" + randHex(rng, 4), randKey(rng, MaxKeyLen)}
		dv := drawVec{R: hashBytes(r), Metadata: metadata, Seed: hashBytes(seed), Pool: []poolVec{}, Drawn: []turnVec{}}
		var pool []Turn
		for j := 0; j < size; j++ {
			tn := Turn{DeviceID: devices[j%len(devices)], Seq: 1 + uint64(j)*97 + rng.Uint64N(97)}
			s, err := DrawScore(seed, tn)
			if err != nil {
				t.Fatal(err)
			}
			pool = append(pool, tn)
			dv.Pool = append(dv.Pool, poolVec{DeviceID: tn.DeviceID, Seq: tn.Seq, Score: hashBytes(s)})
		}
		drawn, err := Draw(seed, pool)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range drawn {
			dv.Drawn = append(dv.Drawn, turnVec{DeviceID: d.DeviceID, Seq: d.Seq})
		}
		if i%2 == 0 {
			commits := [][]byte{}
			cvs := []commitVec{}
			for j, n := 0, rng.IntN(7); j < n; j++ {
				c := make([]byte, pick(rng, []int{20, 32}))
				for x := range c {
					c[x] = byte(rng.Uint32())
				}
				s, err := CommitScore(seed, c)
				if err != nil {
					t.Fatal(err)
				}
				commits = append(commits, c)
				cvs = append(cvs, commitVec{Commit: c, Score: hashBytes(s)})
			}
			got, err := DrawCommits(seed, commits)
			if err != nil {
				t.Fatal(err)
			}
			refs := []commitRef{}
			for _, c := range got {
				refs = append(refs, commitRef{Commit: c})
			}
			dv.Commits, dv.DrawnCommits = &cvs, &refs
		}
		doc.Draws = append(doc.Draws, dv)
	}
	for p := 0; p <= 400; p++ {
		doc.ChallengeK = append(doc.ChallengeK, challengeKVec{Pool: p, K: ChallengeK(p), Drawn: min(ChallengeK(p), p)})
	}
	return doc
}
