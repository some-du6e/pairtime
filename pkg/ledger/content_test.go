// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
	"github.com/some-du6e/pairtime/pkg/params"
)

// TestSpecContentWorkedExample computes the worked example of spec section
// 10.10 and compares each value with the one the spec prints.
func TestSpecContentWorkedExample(t *testing.T) {
	k := testKeys(t)
	fk := k.Features()
	text := []byte("still broken, see src/nav.js:12\r\nTypeError: x is undefined")
	o, err := k.Open(2, text)
	if err != nil {
		t.Fatal(err)
	}
	if o.Len() != 15 {
		t.Fatalf("the text has %d tokens, the spec 15", o.Len())
	}
	c := o.Commitment()
	f, err := fk.Prompt(string(text), features.ErrorPaste)
	if err != nil {
		t.Fatal(err)
	}
	_, shingles := fk.Shingles(string(text))
	if len(shingles) == 0 {
		t.Fatal("no shingles")
	}
	first := features.MinHashValues(shingles[0].V)
	var h07 []byte
	for _, x := range first[:8] {
		h07 = append(h07, byte(x>>24), byte(x>>16), byte(x>>8), byte(x))
	}
	sig, _ := features.ErrorSignature(string(text))

	type value struct {
		name string
		got  []byte
		want string
	}
	values := []value{
		{"salt_1 is HMAC-SHA256(K_tok, u64(2) || u64(1))", o.salts[0][:], "d02c7820505e275a54f699ad69ec044071609006e70bf71dbae4f3f875601e19"},
		{"t_1 is LEAF(salt_1 || still)", o.leaves[0][:], "2a4d7636edabb51f88ef2c3478bc1f16d4e96fa02e952f433636f984291ed7ae"},
		{"salt_2 is the salt of the space after it", o.salts[1][:], "c91fb9511af649c2e9cbddc0747044e127d1814a4fe31ff95227d487e2dcd3a9"},
		{"t_2 is LEAF(salt_2 || space)", o.leaves[1][:], "cec8328945a0af051c0077ecc37741d14433e1f42b341474d1c5df5f63d85167"},
		{"salt_7 is the salt of src/nav.js:12", o.salts[6][:], "ed7631f22cb4e091fa3d2aee3c4f6b45c8acc20de88a03dfd56d024fcbaff1ab"},
		{"t_7 is LEAF(salt_7 || src/nav.js:12)", o.leaves[6][:], "7aa845239d74ce39e31dc06ff6a54e9ad62ebaa4b00e66abf3e2c43bf4aecfd2"},
		{"salt_15 is the salt of the last token", o.salts[14][:], "076e8750259d56bb41898ce7ce0227904efb2ad3bf108e84a2806d62c8527786"},
		{"t_15 is LEAF(salt_15 || undefined)", o.leaves[14][:], "30c6094dd1e912d894facc3d850c1b5263b8c0e9d7714d11051f7d1ec80f9a34"},
		{"C is ROOT of the 15 token leaves", c.R[:], "aa9bfca6f19928733e7a62d0bfad12c6251486f7085a01a297e110631d80ad8b"},
		{"v of the first shingle is HMAC-SHA256(K_feat, 0x06 || shingle)", shingles[0].V[:], "8bb3f386a2bdedcd87146997843e177a9fd5e405272bdf1005f3eb37b499a41e"},
		{"h_0 to h_7 are the first block of the first shingle's MinHash values", h07, "b93458ce0b69154c9209f1c2c69fb1fe7ec6269b350fc364b17f5127872b7caa"},
		{"mh is the sketch of the 14 shingles", f.MH.Bytes(), "1d5ee30902d5e7b729e6f3cc125f6c940886e4c31472a57b1054703f05eb147719253e22056919d00632db7c021bd14e1f289cf107f9ba753d7f54a211e444120cbb90a61a1185c40e787ce6027c216c1006520b1149b59616af640309ba329e0042c0ec0e20757518311269149f4710044c15e405a25c7c231a984b336a1cf2032e36dc39a29b9009c82d0407aae42f07aff42619511a6e07763482018d230a203fdc64178f72140552e3571fbd1dcc052e724a1d824f96055f296d0d4170950a3b7e05023bd77b07c0314a01718dce3da3d08d1b196d0f027ce1ea0422ad1f01b0a6b907432c591d5685f80382f553092e38d7350c6ae40d8155c9044f2069"},
		{"th is K16(0x02, the normalized text)", f.TH[:], "b30c980dcd8b3324712ae687fc0ed740"},
		{"es is K16(0x01, the error signature)", f.ES[:], "825d4a3f99907ce6760e3e1107856a15"},
	}
	for _, v := range values {
		t.Run("10.10 "+v.name, func(t *testing.T) {
			if got := hex.EncodeToString(v.got); got != v.want {
				t.Errorf("pkg/ledger computes %s, the spec prints %s", got, v.want)
			}
		})
	}

	t.Run("10.10 the 15 tokens are the spec's, and n is 15", func(t *testing.T) {
		want := []string{"still", " ", "broken,", " ", "see", " ", "src/nav.js:12", "\r\n", "TypeError:", " ", "x", " ", "is", " ", "undefined"}
		for j, tok := range o.tokens {
			if string(tok) != want[j] {
				t.Errorf("token %d is %q, the spec's %q", j+1, tok, want[j])
			}
		}
		if c.N != 15 {
			t.Errorf("n is %d", c.N)
		}
	})
	t.Run("10.10 a reveal that removes token 7 as known verifies ok, with t_7 as its leaf", func(t *testing.T) {
		items, err := o.Reveal([]Span{{First: 6, Count: 1, Type: SpanKnown}})
		if err != nil {
			t.Fatal(err)
		}
		if v, at := VerifyReveal(c, items); v != RevealOK {
			t.Fatalf("VerifyReveal gives %s at item %d", v, at)
		}
		if len(items) != 15 || !items[6].Removed || hex.EncodeToString(items[6].Leaves[0]) != "7aa845239d74ce39e31dc06ff6a54e9ad62ebaa4b00e66abf3e2c43bf4aecfd2" {
			t.Errorf("item 7 is %+v", items[6])
		}
	})
	t.Run("10.10 the text normalizes to <retry> see <path>, the second line gone as an error line", func(t *testing.T) {
		n := features.Normalize(string(text))
		if n.Text() != "<retry> see <path>" || !slices.Equal(n.RemovedLines, []int{1}) {
			t.Errorf("normalized %q, removed lines %v", n.Text(), n.RemovedLines)
		}
	})
	t.Run("10.10 the shingles are the 14 character 5-shingles the spec lists, in ascending order", func(t *testing.T) {
		want := []string{" <pat", " see ", "<path", "<retr", "> see", "e <pa", "ee <p",
			"etry>", "path>", "retry", "ry> s", "see <", "try> ", "y> se"}
		kind, got := features.Shingles(features.Normalize(string(text)).Words)
		if kind != features.Char5 || !slices.Equal(got, want) {
			t.Errorf("%s shingles %q", kind, got)
		}
	})
	t.Run("10.10 the error signature is TypeError: x is undefined", func(t *testing.T) {
		if sig != "TypeError: x is undefined" {
			t.Errorf("signature %q", sig)
		}
	})
	t.Run("10.10 the prompt.submit record's body ends with the f and c the spec prints", func(t *testing.T) {
		ev, err := sampleEvent(event.KindPromptSubmit)
		if err != nil {
			t.Fatal(err)
		}
		ev.Seq = 2
		rec, err := EventRecord(k, ev, Content{Text: text, Class: features.ErrorPaste})
		if err != nil {
			t.Fatal(err)
		}
		if want := `,"f":{"v":1,"mh":"1d5ee30902d5e7b729e6f3cc125f6c940886e4c31472a57b1054703f05eb147719253e22056919d00632db7c021bd14e1f289cf107f9ba753d7f54a211e444120cbb90a61a1185c40e787ce6027c216c1006520b1149b59616af640309ba329e0042c0ec0e20757518311269149f4710044c15e405a25c7c231a984b336a1cf2032e36dc39a29b9009c82d0407aae42f07aff42619511a6e07763482018d230a203fdc64178f72140552e3571fbd1dcc052e724a1d824f96055f296d0d4170950a3b7e05023bd77b07c0314a01718dce3da3d08d1b196d0f027ce1ea0422ad1f01b0a6b907432c591d5685f80382f553092e38d7350c6ae40d8155c9044f2069","th":"b30c980dcd8b3324712ae687fc0ed740","es":"825d4a3f99907ce6760e3e1107856a15","pc":"error_paste"},"c":{"n":15,"r":"aa9bfca6f19928733e7a62d0bfad12c6251486f7085a01a297e110631d80ad8b"}}`; !bytes.HasSuffix(rec.Body, []byte(want)) {
			t.Errorf("body\n%s\ndoesn't end with\n%s", rec.Body, want)
		}
	})
	t.Run("10.10 the agent.tool record that ran npm test and changed lines 10 to 12 of src/nav.js has the spec's f", func(t *testing.T) {
		ev, err := sampleEvent(event.KindAgentTool)
		if err != nil {
			t.Fatal(err)
		}
		ev.Seq = 3
		rec, err := EventRecord(k, ev, Content{Edits: []features.Edit{{Path: "src/nav.js", OldStart: 10, OldLines: 3, NewStart: 10, NewLines: 5}}, Command: "npm test"})
		if err != nil {
			t.Fatal(err)
		}
		if want := `,"f":{"v":1,"h":[{"p":"c8b26d53782c22f46eb418e7716dcc97","os":10,"ol":3,"ns":10,"nl":5}],"ch":"3235767396764538f6473785ffd467f8"}}`; !bytes.HasSuffix(rec.Body, []byte(want)) {
			t.Errorf("body\n%s\ndoesn't end with\n%s", rec.Body, want)
		}
	})
	t.Run("10.10 the draw: SHA-256(M), the seed, a score and the ten drawn turns", func(t *testing.T) {
		var r Hash
		for i := range r {
			r[i] = byte(0x20 + i)
		}
		m := []byte("stand-in metadata reveal")
		var pool []Turn
		for _, s := range []uint64{2, 9, 14, 23, 31, 40, 52, 61} {
			pool = append(pool, Turn{"d_example", s})
		}
		for _, s := range []uint64{7, 18, 26, 33} {
			pool = append(pool, Turn{"d_other", s})
		}
		mh := sha256.Sum256(m)
		seed := DrawSeed(r, m)
		if fromDigest := DrawSeedFromDigest(r, mh); fromDigest != seed {
			t.Errorf("DrawSeedFromDigest(r, SHA-256(M)) = %s, and DrawSeed(r, M) = %s", fromDigest, seed)
		}
		score, err := DrawScore(seed, Turn{"d_example", 2})
		if err != nil {
			t.Fatal(err)
		}
		drawn, err := Draw(seed, pool, drawTable(t))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, d := range drawn {
			names = append(names, fmt.Sprintf("%s %d", d.DeviceID, d.Seq))
		}
		cases := []struct{ name, got, want string }{
			{"SHA-256(M)", hex.EncodeToString(mh[:]), "2354fe7d5f87c7fa5cbff5be38836fdabd937b9623ccc4c9e3daef33db894f63"},
			{"seed", seed.String(), "2e64adb81e4c1a7352913b0c0e0732706dac00beffcbdfa062e07d8da236b391"},
			{"score of (d_example, 2)", score.String(), "670c31a9fde38cee626f952dda2e3db8697747bf3067bb25f0d924ed53efd2d1"},
			{"drawn, smallest first", strings.Join(names, ", "), "d_example 9, d_other 26, d_other 7, d_example 14, d_example 52, d_other 33, d_example 2, d_other 18, d_example 23, d_example 40"},
		}
		if k := ChallengeK(drawTable(t), len(pool)); k != 10 {
			t.Errorf("CHALLENGE_K for 12 is %d", k)
		}
		for _, c := range cases {
			if c.got != c.want {
				t.Errorf("%s: pkg/ledger gives %s, the spec %s", c.name, c.got, c.want)
			}
		}
	})
}

// randomOpenings opens n random texts under fixed keys, each with random
// spans removed.
func randomOpenings(t *testing.T, seed uint64, n int) ([]*Opening, [][]Span) {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 7))
	k := testKeys(t)
	var opened []*Opening
	var spans [][]Span
	for i := 0; i < n; i++ {
		o, err := k.Open(1+rng.Uint64N(1<<32), []byte(randomText(rng)))
		if err != nil {
			t.Fatal(err)
		}
		opened = append(opened, o)
		spans = append(spans, randomSpans(rng, o.Len()))
	}
	return opened, spans
}

func TestRevealChangingAnyKeptTokenFails(t *testing.T) {
	openings, spans := randomOpenings(t, 1, 150)
	changes := []struct {
		name   string
		change func(tok []byte) []byte
	}{
		{"a byte added of the token's kind", func(tok []byte) []byte {
			if space, _ := tokenKind(tok); space {
				return append(bytes.Clone(tok), ' ')
			}
			return append(bytes.Clone(tok), 'x')
		}},
		{"the last byte dropped", func(tok []byte) []byte { return tok[:len(tok)-1] }},
		{"the first byte's low bit flipped", func(tok []byte) []byte {
			out := bytes.Clone(tok)
			out[0] ^= 0x01
			return out
		}},
		{"replaced by a token of the other kind", func(tok []byte) []byte {
			if space, _ := tokenKind(tok); space {
				return []byte("x")
			}
			return []byte(" ")
		}},
	}
	for _, ch := range changes {
		t.Run("a kept token with "+ch.name+" fails verification, at every kept position of 150 random reveals", func(t *testing.T) {
			tried := 0
			for i, o := range openings {
				items, err := o.Reveal(spans[i])
				if err != nil {
					t.Fatal(err)
				}
				c := o.Commitment()
				if v, at := VerifyReveal(c, items); v != RevealOK {
					t.Fatalf("reveal %d gets %s at item %d before any change", i, v, at)
				}
				for j := range items {
					if items[j].Removed {
						continue
					}
					m := cloneItems(items)
					m[j].Token = ch.change(m[j].Token)
					if bytes.Equal(m[j].Token, items[j].Token) {
						continue
					}
					tried++
					if v, _ := VerifyReveal(c, m); v == RevealOK {
						t.Fatalf("reveal %d still verifies with item %d changed from %q to %q", i, j, items[j].Token, m[j].Token)
					}
				}
			}
			if tried < 1000 {
				t.Errorf("only %d changes tried", tried)
			}
		})
	}
	t.Run("a kept token's salt with any one bit flipped fails verification", func(t *testing.T) {
		for i, o := range openings[:30] {
			items, err := o.Reveal(spans[i])
			if err != nil {
				t.Fatal(err)
			}
			for j := range items {
				if items[j].Removed {
					continue
				}
				for bit := 0; bit < 8*HashSize; bit += 37 {
					m := cloneItems(items)
					m[j].Salt[bit/8] ^= 1 << (bit % 8)
					if v, _ := VerifyReveal(o.Commitment(), m); v == RevealOK {
						t.Fatalf("reveal %d still verifies with bit %d of item %d's salt flipped", i, bit, j)
					}
				}
			}
		}
	})
}

func TestRevealSpansSurviveJSON(t *testing.T) {
	openings, spans := randomOpenings(t, 2, 200)
	for i, o := range openings {
		if len(spans[i]) == 0 {
			continue
		}
		t.Run(fmt.Sprintf("random reveal %d: span types, token counts and line counts survive a JSON round trip, and the reveal still verifies", i), func(t *testing.T) {
			items, err := o.Reveal(spans[i])
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			var back []RevealItem
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatal(err)
			}
			if v, at := VerifyReveal(o.Commitment(), back); v != RevealOK {
				t.Fatalf("the parsed reveal gets %s at item %d", v, at)
			}
			// Each span shows up once, in order, with its type, count and
			// lines; every other token is kept, as itself.
			var got []Span
			pos := 0
			for _, it := range back {
				if !it.Removed {
					if !bytes.Equal(it.Token, o.tokens[pos]) {
						t.Errorf("token %d comes back as %q, not %q", pos, it.Token, o.tokens[pos])
					}
					pos++
					continue
				}
				sp := Span{First: pos, Count: int(it.Count), Type: it.Type}
				if it.Lines != nil {
					sp.Lines = int(*it.Lines)
				}
				got = append(got, sp)
				pos += int(it.Count)
			}
			want := slices.Clone(spans[i])
			slices.SortFunc(want, func(a, b Span) int { return a.First - b.First })
			if !slices.Equal(got, want) {
				t.Errorf("the reveal's spans are %+v, want %+v", got, want)
			}
			again, err := json.Marshal(back)
			if err != nil || !bytes.Equal(again, data) {
				t.Errorf("writing the parsed reveal again gives other bytes: %v", err)
			}
		})
	}
}

func TestRevealRefuses(t *testing.T) {
	k := testKeys(t)
	o, err := k.Open(9, []byte("fix the login bug"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		spans []Span
	}{
		{"spans that overlap are refused", []Span{{First: 0, Count: 2, Type: SpanTeen}, {First: 1, Count: 1, Type: SpanTeen}}},
		{"a span past the end is refused", []Span{{First: 6, Count: 2, Type: SpanTeen}}},
		{"a span before the start is refused", []Span{{First: -1, Count: 1, Type: SpanTeen}}},
		{"a span of no tokens is refused", []Span{{First: 2, Count: 0, Type: SpanTeen}}},
		{"a span type outside the table is refused", []Span{{First: 2, Count: 1, Type: "email"}}},
		{"a line count on a span that isn't code is refused", []Span{{First: 2, Count: 1, Type: SpanPII, Lines: 3}}},
		{"a negative line count is refused", []Span{{First: 2, Count: 1, Type: SpanCode, Lines: -1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := o.Reveal(c.spans); err == nil {
				t.Error("revealed")
			}
		})
	}
	t.Run("adjacent spans of two types stay two items, and verify", func(t *testing.T) {
		items, err := o.Reveal([]Span{{First: 2, Count: 1, Type: SpanKnown}, {First: 0, Count: 2, Type: SpanSecret}})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2+4 || items[0].Type != SpanSecret || items[1].Type != SpanKnown {
			t.Errorf("items %+v", items)
		}
		if v, _ := VerifyReveal(o.Commitment(), items); v != RevealOK {
			t.Errorf("verdict %s", v)
		}
	})
	t.Run("a text that isn't valid UTF-8 can't be opened", func(t *testing.T) {
		if _, err := k.Open(9, []byte{'a', 0xc0, 0xaf}); err == nil {
			t.Error("opened an overlong '/'")
		}
	})
}

func TestDrawRules(t *testing.T) {
	var seed Hash
	copy(seed[:], "a fixed seed for the draw tests")
	rng := rand.New(rand.NewPCG(3, 3))
	pool := make([]Turn, 300)
	for i := range pool {
		pool[i] = Turn{DeviceID: pick(rng, []string{"d_a", "d_b", "x"}), Seq: uint64(1 + i)}
	}
	tb := drawTable(t)
	drawn, err := Draw(seed, pool, tb)
	if err != nil {
		t.Fatal(err)
	}
	shuffled := slices.Clone(pool)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	again, err := Draw(seed, shuffled, tb)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"the draw doesn't depend on the pool's order", slices.Equal(drawn, again)},
		{"a pool of 300 draws 30, the cap", len(drawn) == 30},
		{"CHALLENGE_K is 10 up to a pool of 100 and 11 from 101", ChallengeK(tb, 100) == 10 && ChallengeK(tb, 101) == 11},
		{"CHALLENGE_K is 29 at 290 and 30 from 291", ChallengeK(tb, 290) == 29 && ChallengeK(tb, 291) == 30 && ChallengeK(tb, 5000) == 30},
		{"a pool smaller than CHALLENGE_K is drawn whole", len(mustDraw(t, seed, pool[:4])) == 4},
		{"an empty pool draws nothing", len(mustDraw(t, seed, nil)) == 0},
		{"the drawn turns are the smallest scores, smallest first", smallestFirst(t, seed, pool, drawn)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Error("doesn't hold")
			}
		})
	}
	drawErr := func(pool []Turn) error {
		_, err := Draw(seed, pool, tb)
		return err
	}
	commitsErr := func(commits [][]byte) error {
		_, err := DrawCommits(seed, commits)
		return err
	}
	refusals := []struct {
		name string
		err  error
	}{
		{"a pool that lists a turn twice is refused", drawErr([]Turn{{"d_a", 1}, {"d_a", 1}})},
		{"a pool turn whose device_id isn't an opaque key is refused", drawErr([]Turn{{"teen@example.com", 1}})},
		{"a pool turn with s = 0 is refused", drawErr([]Turn{{"d_a", 0}})},
		{"a commit id of 21 bytes is refused", commitsErr([][]byte{make([]byte, 21)})},
		{"a commit list that holds a commit twice is refused", commitsErr([][]byte{make([]byte, 20), make([]byte, 20)})},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			if c.err == nil {
				t.Error("drawn")
			}
		})
	}
	t.Run("one commit is the whole interview list; of three, the two smallest scores", func(t *testing.T) {
		one := [][]byte{bytes.Repeat([]byte{1}, 20)}
		if got, err := DrawCommits(seed, one); err != nil || len(got) != 1 {
			t.Errorf("%v, %v", got, err)
		}
		three := [][]byte{bytes.Repeat([]byte{1}, 20), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 20)}
		got, err := DrawCommits(seed, three)
		if err != nil || len(got) != InterviewCommits {
			t.Fatalf("%v, %v", got, err)
		}
		var scores []Hash
		for _, c := range three {
			s, _ := CommitScore(seed, c)
			scores = append(scores, s)
		}
		for _, g := range got {
			s, _ := CommitScore(seed, g)
			larger := 0
			for _, o := range scores {
				if bytes.Compare(o[:], s[:]) > 0 {
					larger++
				}
			}
			if larger < 1 {
				t.Errorf("drew the commit with the largest score")
			}
		}
	})
}

// TestChallengeKTables runs ChallengeK and Draw under tables other than
// 0.1.0, so K has to come from the table passed in, its share has to be
// exact (spec 10.9), and a K that no draw can take is refused.
func TestChallengeKTables(t *testing.T) {
	small := challengeTable(t, "1", "0.15", "7")
	third := challengeTable(t, "10", "0.3333333333333333", "30")
	thirdWide := challengeTable(t, "1", "0.3333333333333333", "1000")
	tenth := challengeTable(t, "10", "0.100000000000000001", "30")
	tenthWide := challengeTable(t, "1", "0.100000000000000001", "1000")
	huge := challengeTable(t, "1", "4611686018427387904", "30")
	cases := []struct {
		name      string
		got, want int64
	}{
		{"under MIN 1, FRAC 0.15 and MAX 7, a pool of 0 draws CHALLENGE_K_MIN", ChallengeK(small, 0), 1},
		{"under MIN 1, FRAC 0.15 and MAX 7, 6 turns give a share of 0.9, which rounds up to 1", ChallengeK(small, 6), 1},
		{"under MIN 1, FRAC 0.15 and MAX 7, 7 turns give a share of 1.05, which rounds up to 2", ChallengeK(small, 7), 2},
		{"under MIN 1, FRAC 0.15 and MAX 7, 40 turns give exactly 6", ChallengeK(small, 40), 6},
		{"under MIN 1, FRAC 0.15 and MAX 7, 41 turns give 6.15, which rounds up to 7", ChallengeK(small, 41), 7},
		{"under MIN 1, FRAC 0.15 and MAX 7, 1000 turns are capped at 7", ChallengeK(small, 1000), 7},
		{"a FRAC of 0.3333333333333333 gives the cap of 30 at 2765 turns, where num*P + den - 1 passes 2^63", ChallengeK(third, 2765), 30},
		{"a FRAC of 0.3333333333333333 gives exactly 922 at 2765 turns under a cap of 1000", ChallengeK(thirdWide, 2765), 922},
		{"a FRAC of 0.100000000000000001 gives 11 at 100 turns, since 10.0000000000000001 rounds up", ChallengeK(tenth, 100), 11},
		{"a FRAC of 0.100000000000000001 gives the cap of 30 at 300 turns", ChallengeK(tenth, 300), 30},
		{"a FRAC of 0.100000000000000001 gives exactly 31 at 300 turns under a cap of 1000", ChallengeK(tenthWide, 300), 31},
		{"a share past 2^64 is the cap", ChallengeK(huge, math.MaxInt32), 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("CHALLENGE_K = %d, want %d", c.got, c.want)
			}
		})
	}
	var seed Hash
	copy(seed[:], "a fixed seed for the table tests")
	pool := make([]Turn, 20)
	for i := range pool {
		pool[i] = Turn{DeviceID: "d_a", Seq: uint64(1 + i)}
	}
	draws := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"a pool of 20 under MIN 1, FRAC 0.15 and MAX 7 draws 3 turns, the first 3 of table 0.1.0's ranking", func(t *testing.T) {
			got, want := mustDrawUnder(t, seed, pool, small), mustDraw(t, seed, pool)
			if len(got) != 3 || !slices.Equal(got, want[:3]) {
				t.Errorf("drew %v, want the first 3 of %v", got, want)
			}
		}},
		{"a K past 32 bits is cut to the pool before it becomes an int, so a pool of 20 is drawn whole", func(t *testing.T) {
			if got := mustDrawUnder(t, seed, pool, challengeTable(t, "4294967301", "0.1", "4294967301")); len(got) != len(pool) {
				t.Errorf("drew %d turns, want %d", len(got), len(pool))
			}
		}},
		{"a negative CHALLENGE_K_MAX is refused with an error, not a panic", func(t *testing.T) {
			if _, err := Draw(seed, pool, challengeTable(t, "1", "0.1", "-1")); err == nil {
				t.Error("Draw took a table whose CHALLENGE_K is -1")
			}
		}},
		{"a CHALLENGE_K of 0, for an empty pool under a CHALLENGE_K_MIN of 0, is refused", func(t *testing.T) {
			if _, err := Draw(seed, nil, challengeTable(t, "0", "0.1", "30")); err == nil {
				t.Error("Draw took a table whose CHALLENGE_K is 0")
			}
		}},
	}
	for _, c := range draws {
		t.Run(c.name, c.check)
	}
}

func mustDraw(t *testing.T, seed Hash, pool []Turn) []Turn {
	t.Helper()
	return mustDrawUnder(t, seed, pool, drawTable(t))
}

func mustDrawUnder(t *testing.T, seed Hash, pool []Turn, tb *params.Table) []Turn {
	t.Helper()
	d, err := Draw(seed, pool, tb)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// drawTable is parameter table 0.1.0, which the draw tests and vectors run
// under: its CHALLENGE_K_* values give min(30, max(10, ceil(p / 10))).
func drawTable(t testing.TB) *params.Table {
	t.Helper()
	tb, err := params.ByVersion("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

// challengeTable is table 0.1.0 with its CHALLENGE_K_MIN, CHALLENGE_K_FRAC
// and CHALLENGE_K_MAX set to lo, frac and hi, written as JSON writes them,
// under a version of its own, as params.Parse reads a bundle's table.
func challengeTable(t testing.TB, lo, frac, hi string) *params.Table {
	t.Helper()
	var doc struct {
		Version string                     `json:"version"`
		Params  map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(drawTable(t).Canonical(), &doc); err != nil {
		t.Fatal(err)
	}
	doc.Version = "0.1.9"
	doc.Params[params.CHALLENGE_K_MIN.String()] = json.RawMessage(lo)
	doc.Params[params.CHALLENGE_K_FRAC.String()] = json.RawMessage(frac)
	doc.Params[params.CHALLENGE_K_MAX.String()] = json.RawMessage(hi)
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	tb, err := params.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

// smallestFirst reports whether drawn are the pool's turns of smallest
// score, in rising order of score.
func smallestFirst(t *testing.T, seed Hash, pool, drawn []Turn) bool {
	t.Helper()
	scores := make([][]byte, len(pool))
	for i, p := range pool {
		s, err := DrawScore(seed, p)
		if err != nil {
			t.Fatal(err)
		}
		scores[i] = bytes.Clone(s[:])
	}
	slices.SortFunc(scores, bytes.Compare)
	for i, d := range drawn {
		s, _ := DrawScore(seed, d)
		if !bytes.Equal(s[:], scores[i]) {
			return false
		}
	}
	return true
}

func TestContentSecretsAreNeverPrinted(t *testing.T) {
	k := testKeys(t)
	o, err := k.Open(4, []byte("my key is sk-test-51Hx"))
	if err != nil {
		t.Fatal(err)
	}
	feat := k.FeatKey()
	secrets := []string{"sk-test-51Hx", hex.EncodeToString(o.salts[0][:]), hex.EncodeToString(feat[:])}
	var cases []struct{ name, out string }
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%q"} {
		cases = append(cases,
			struct{ name, out string }{"*Opening formats as a placeholder under " + verb, fmt.Sprintf(verb, o)},
			struct{ name, out string }{"Opening formats as a placeholder under " + verb, fmt.Sprintf(verb, *o)},
			struct{ name, out string }{"*features.Key formats as a placeholder under " + verb, fmt.Sprintf(verb, k.Features())},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.out != "ledger.Opening{redacted}" && c.out != "features.Key{redacted}" {
				t.Errorf("printed %q", c.out)
			}
			for _, s := range secrets {
				if strings.Contains(strings.ToLower(c.out), strings.ToLower(s)) {
					t.Errorf("output %q shows a secret", c.out)
				}
			}
		})
	}
}
