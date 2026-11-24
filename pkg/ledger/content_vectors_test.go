// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unicode"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// The part 2 sections of spec 11.3's vectors format. vectors-content.json
// is read with these types, and TestReferenceCheckContent writes them for
// tools/ledger_ref.py check.

type contentVectorsFile struct {
	Format         string             `json:"format"`
	Spec           string             `json:"spec,omitempty"`
	Generator      string             `json:"generator,omitempty"`
	Note           string             `json:"note,omitempty"`
	Lowercase      *lowercaseVec      `json:"lowercase,omitempty"`
	Commitments    []commitmentVec    `json:"commitments,omitempty"`
	Reveals        []revealVec        `json:"reveals,omitempty"`
	Normalizations []normalizationVec `json:"normalizations,omitempty"`
	Features       []featureVec       `json:"features,omitempty"`
	Diffs          []diffVec          `json:"diffs,omitempty"`
	Jaccard        []jaccardVec       `json:"jaccard,omitempty"`
	Draws          []drawVec          `json:"draws,omitempty"`
	ChallengeK     []challengeKVec    `json:"challenge_k,omitempty"`
}

type lowercaseVec struct {
	Note         string        `json:"note,omitempty"`
	Unicode      string        `json:"unicode"`
	Runs         []lowerRunVec `json:"runs"`
	LaterUnicode string        `json:"later_unicode,omitempty"`
	Later        []laterVec    `json:"later,omitempty"`
}

type lowerRunVec struct {
	First  rune `json:"first"`
	Count  rune `json:"count"`
	Delta  rune `json:"delta"`
	Stride rune `json:"stride"`
}

type laterVec struct {
	Code  rune `json:"code"`
	Lower rune `json:"lower"`
}

type commitmentVec struct {
	Note   string     `json:"note,omitempty"`
	KDev   hexBytes   `json:"k_dev"`
	Seq    uint64     `json:"seq"`
	RType  uint8      `json:"rtype"`
	Raw    hexBytes   `json:"raw,omitempty"`
	Text   hexBytes   `json:"text"`
	N      uint64     `json:"n"`
	Root   hexBytes   `json:"root"`
	Tokens []tokenVec `json:"tokens,omitempty"`
}

type tokenVec struct {
	Token hexBytes `json:"token"`
	Salt  hexBytes `json:"salt"`
	Leaf  hexBytes `json:"leaf"`
}

type revealVec struct {
	Note    string        `json:"note,omitempty"`
	N       uint64        `json:"n"`
	Root    hexBytes      `json:"root"`
	Items   []RevealItem  `json:"items"`
	Verdict RevealVerdict `json:"verdict"`
}

type normalizationVec struct {
	Note         string     `json:"note,omitempty"`
	Text         hexBytes   `json:"text"`
	RemovedLines []int      `json:"removed_lines"`
	Normalized   hexBytes   `json:"normalized"`
	ShingleKind  string     `json:"shingle_kind"`
	Shingles     []hexBytes `json:"shingles"`
	Signature    *hexBytes  `json:"signature"`
}

// featureVec is one features object. Which members it has depends on its
// rtype, so the optional ones are pointers: present, even when empty, or
// absent.
type featureVec struct {
	Note          string          `json:"note,omitempty"`
	KDev          hexBytes        `json:"k_dev"`
	RType         uint8           `json:"rtype"`
	Text          *hexBytes       `json:"text,omitempty"`
	PromptClass   *features.Class `json:"prompt_class,omitempty"`
	ShingleValues *[]hexBytes     `json:"shingle_values,omitempty"`
	Hunks         *[]hunkVec      `json:"hunks,omitempty"`
	Command       *hexBytes       `json:"command,omitempty"`
	F             json.RawMessage `json:"f"`
}

type hunkVec struct {
	Path     string `json:"path"`
	OldStart int    `json:"old_start"`
	OldLines int    `json:"old_lines"`
	NewStart int    `json:"new_start"`
	NewLines int    `json:"new_lines"`
}

// diffVec is one hunk derivation of spec 10.6: a patch hunk with its
// lines, or a file's text before and after one edit, and the zero-context
// hunks it gives, or Malformed for a patch hunk that gives none.
type diffVec struct {
	Note      string     `json:"note,omitempty"`
	Patch     *patchVec  `json:"patch,omitempty"`
	Before    *hexBytes  `json:"before,omitempty"`
	After     *hexBytes  `json:"after,omitempty"`
	Hunks     *[]numsVec `json:"hunks,omitempty"`
	Malformed bool       `json:"malformed,omitempty"`
}

type patchVec struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Lines    []hexBytes `json:"lines"`
}

type numsVec struct {
	OldStart int `json:"old_start"`
	OldLines int `json:"old_lines"`
	NewStart int `json:"new_start"`
	NewLines int `json:"new_lines"`
}

type jaccardVec struct {
	Note    string   `json:"note,omitempty"`
	A       hexBytes `json:"a"`
	B       hexBytes `json:"b"`
	Matches int      `json:"matches"`
}

type drawVec struct {
	Note         string       `json:"note,omitempty"`
	R            hexBytes     `json:"r"`
	Metadata     hexBytes     `json:"metadata"`
	Seed         hexBytes     `json:"seed"`
	Pool         []poolVec    `json:"pool"`
	Drawn        []turnVec    `json:"drawn"`
	Commits      *[]commitVec `json:"commits,omitempty"`
	DrawnCommits *[]commitRef `json:"drawn_commits,omitempty"`
}

type poolVec struct {
	DeviceID string   `json:"device_id"`
	Seq      uint64   `json:"seq"`
	Score    hexBytes `json:"score"`
}

type turnVec struct {
	DeviceID string `json:"device_id"`
	Seq      uint64 `json:"seq"`
}

type commitVec struct {
	Commit hexBytes `json:"commit"`
	Score  hexBytes `json:"score"`
}

type commitRef struct {
	Commit hexBytes `json:"commit"`
}

type challengeKVec struct {
	Note  string `json:"note,omitempty"`
	Pool  int    `json:"pool"`
	K     int    `json:"k"`
	Drawn int    `json:"drawn"`
}

// readContentVectors reads testdata/ledger/vectors-content.json, refusing
// any member the format doesn't define.
func readContentVectors(t *testing.T) contentVectorsFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ledger", "vectors-content.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f contentVectorsFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("vectors-content.json: %v", err)
	}
	if f.Format != vectorsFormat {
		t.Fatalf("vectors-content.json: format is %q, want %q", f.Format, vectorsFormat)
	}
	return f
}

func TestContentVectorsHaveWhatSpec11Lists(t *testing.T) {
	f := readContentVectors(t)
	if f.Lowercase == nil {
		t.Fatal("the vectors have no lowercase table")
	}
	verdicts := map[RevealVerdict]int{}
	for _, r := range f.Reveals {
		verdicts[r.Verdict]++
	}
	// The counts come from testdata/ledger/README.md and PR #12. If they
	// stop matching, a vectors file with less in it would pass the tests
	// below without notice.
	cases := []struct {
		name      string
		got, want int
	}{
		{"the lowercase table has 182 runs", len(f.Lowercase.Runs), 182},
		{"the lowercase table lists 55 later mappings", len(f.Lowercase.Later), 55},
		{"the vectors hold 22 commitments", len(f.Commitments), 22},
		{"the vectors hold 58 reveals", len(f.Reveals), 58},
		{"the vectors hold 54 normalizations", len(f.Normalizations), 54},
		{"the vectors hold 21 feature maps", len(f.Features), 21},
		{"the vectors hold 17 hunk derivations", len(f.Diffs), 17},
		{"the vectors hold 6 Jaccard pairs", len(f.Jaccard), 6},
		{"the vectors hold 5 draws", len(f.Draws), 5},
		{"the vectors hold 18 CHALLENGE_K sizes", len(f.ChallengeK), 18},
	}
	for _, v := range []RevealVerdict{RevealOK, RevealBadItem, RevealCount, RevealAlternation, RevealRoot} {
		cases = append(cases, struct {
			name      string
			got, want int
		}{fmt.Sprintf("some reveal has the verdict %s", v), min(verdicts[v], 1), 1})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %d, want %d", c.got, c.want)
			}
		})
	}
}

func TestVectorLowercase(t *testing.T) {
	lc := readContentVectors(t).Lowercase
	table := map[rune]rune{}
	for _, r := range lc.Runs {
		for i := rune(0); i < r.Count; i++ {
			table[r.First+i*r.Stride] = r.First + i*r.Stride + r.Delta
		}
	}
	later := map[rune]rune{}
	for _, l := range lc.Later {
		later[l.Code] = l.Lower
	}
	t.Run("the vectors pin Unicode 15.0.0, and list later mappings up to 17.0.0", func(t *testing.T) {
		if lc.Unicode != features.LowerUnicode || lc.LaterUnicode != "17.0.0" {
			t.Errorf("unicode %q, later_unicode %q", lc.Unicode, lc.LaterUnicode)
		}
	})
	t.Run("Lower is the vectors' table at every code point, and the identity elsewhere", func(t *testing.T) {
		if len(table) != 1433 {
			t.Errorf("the runs hold %d mappings, want 1,433", len(table))
		}
		bad := 0
		for r := rune(0); r <= unicode.MaxRune; r++ {
			want, ok := table[r]
			if !ok {
				want = r
			}
			if got := features.Lower(r); got != want && bad < 10 {
				bad++
				t.Errorf("Lower(U+%04X) = U+%04X, the vectors map it to U+%04X", r, got, want)
			}
		}
	})
	t.Run("version 1 applies none of the mappings Unicode added after 15.0.0", func(t *testing.T) {
		for code := range later {
			if got := features.Lower(code); got != code {
				t.Errorf("Lower(U+%04X) = U+%04X; f.v = 1 keeps it", code, got)
			}
		}
	})
	// package unicode's tables come from another source, the Go team's
	// generator over UnicodeData.txt, in whatever version this toolchain
	// has. They must agree with the pinned table everywhere apart from the
	// later mappings, which a toolchain may or may not have.
	t.Run("unicode.ToLower agrees with the pinned table apart from the later mappings", func(t *testing.T) {
		differ := 0
		for r := rune(0); r <= unicode.MaxRune; r++ {
			got, pinned := unicode.ToLower(r), features.Lower(r)
			if got == pinned {
				continue
			}
			differ++
			if l, ok := later[r]; !ok || got != l {
				t.Errorf("unicode %s maps U+%04X to U+%04X, the pinned 15.0.0 table to U+%04X, and the vectors list no such later mapping", unicode.Version, r, got, pinned)
			}
		}
		t.Logf("unicode %s differs from the pinned table at %d of the %d later code points", unicode.Version, differ, len(later))
	})
}

func TestVectorCommitments(t *testing.T) {
	for i, cv := range readContentVectors(t).Commitments {
		t.Run(fmt.Sprintf("commitments[%d] %s: n, C and every token, salt and leaf reproduce", i, cv.Note), func(t *testing.T) {
			checkCommitmentVector(t, cv)
		})
	}
}

// checkCommitmentVector recomputes one commitment of the vectors.
func checkCommitmentVector(t *testing.T, cv commitmentVec) {
	t.Helper()
	k, err := DeriveKeys(cv.KDev)
	if err != nil {
		t.Fatal(err)
	}
	if kind, ok := RType(cv.RType).EventKind(); !ok || !CommitsText(kind) {
		t.Fatalf("rtype 0x%02x commits no text", cv.RType)
	}
	if cv.Raw != nil {
		sameBytes(t, "text, the cut of raw", CommittedMessage(cv.Raw), cv.Text)
	} else if RType(cv.RType) == 0x04 && len(cv.Text) > MessageMax {
		t.Errorf("an agent message of %d bytes", len(cv.Text))
	}
	o, err := k.Open(cv.Seq, cv.Text)
	if err != nil {
		t.Fatal(err)
	}
	c := o.Commitment()
	if c.N != cv.N {
		t.Errorf("n: pkg/ledger counts %d tokens, the vectors %d", c.N, cv.N)
	}
	sameBytes(t, "root", c.R[:], cv.Root)
	if cv.Tokens == nil {
		return
	}
	if len(cv.Tokens) != o.Len() {
		t.Fatalf("the vectors list %d tokens, pkg/ledger finds %d", len(cv.Tokens), o.Len())
	}
	for j, tv := range cv.Tokens {
		sameBytes(t, fmt.Sprintf("tokens[%d].token", j), o.tokens[j], tv.Token)
		sameBytes(t, fmt.Sprintf("tokens[%d].salt", j), o.salts[j][:], tv.Salt)
		sameBytes(t, fmt.Sprintf("tokens[%d].leaf", j), o.leaves[j][:], tv.Leaf)
		salt := k.TokenSalt(cv.Seq, uint64(j+1))
		sameBytes(t, fmt.Sprintf("tokens[%d].salt from TokenSalt", j), salt[:], tv.Salt)
	}
}

func TestVectorReveals(t *testing.T) {
	for i, rv := range readContentVectors(t).Reveals {
		t.Run(fmt.Sprintf("reveals[%d] %s: VerifyReveal gives %s", i, rv.Note, rv.Verdict), func(t *testing.T) {
			root, err := HashOf(rv.Root)
			if err != nil {
				t.Fatal(err)
			}
			if got, at := VerifyReveal(Commitment{N: rv.N, R: root}, rv.Items); got != rv.Verdict {
				t.Errorf("VerifyReveal gives %s (item %d), the vectors %s", got, at, rv.Verdict)
			}
		})
	}
}

func TestVectorNormalizations(t *testing.T) {
	for i, nv := range readContentVectors(t).Normalizations {
		t.Run(fmt.Sprintf("normalizations[%d] %s: removed lines, words, shingles and signature reproduce", i, nv.Note), func(t *testing.T) {
			checkNormalizationVector(t, nv)
		})
	}
}

// checkNormalizationVector recomputes one normalization of the vectors.
func checkNormalizationVector(t *testing.T, nv normalizationVec) {
	t.Helper()
	norm := features.Normalize(string(nv.Text))
	if got := norm.RemovedLines; !slices.Equal(got, nv.RemovedLines) && len(got)+len(nv.RemovedLines) > 0 {
		t.Errorf("removed_lines: pkg/ledger removes %v, the vectors %v", got, nv.RemovedLines)
	}
	if got := norm.Text(); got != string(nv.Normalized) {
		t.Errorf("normalized: pkg/ledger gives %q, the vectors %q", got, nv.Normalized)
	}
	kind, shingles := features.Shingles(norm.Words)
	if kind.String() != nv.ShingleKind {
		t.Errorf("shingle_kind: pkg/ledger cuts %s, the vectors %s", kind, nv.ShingleKind)
	}
	want := make([]string, len(nv.Shingles))
	for j, x := range nv.Shingles {
		want[j] = string(x)
	}
	if !slices.Equal(shingles, want) && len(shingles)+len(want) > 0 {
		t.Errorf("shingles: pkg/ledger gives %q, the vectors %q", shingles, want)
	}
	sig, ok := features.ErrorSignature(string(nv.Text))
	switch {
	case ok != (nv.Signature != nil):
		t.Errorf("signature: pkg/ledger finds one %v, the vectors %v", ok, nv.Signature != nil)
	case ok && sig != string(*nv.Signature):
		t.Errorf("signature: pkg/ledger gives %q, the vectors %q", sig, *nv.Signature)
	}
}

func TestVectorFeatures(t *testing.T) {
	for i, fv := range readContentVectors(t).Features {
		t.Run(fmt.Sprintf("features[%d] %s: f reproduces byte for byte, and parses back", i, fv.Note), func(t *testing.T) {
			checkFeatureVector(t, fv)
		})
	}
}

// checkFeatureVector recomputes one features object of the vectors. The
// reference writes f's members in the order of spec 10.7's table, so its
// compacted bytes are what encoding/json writes for F.
func checkFeatureVector(t *testing.T, fv featureVec) {
	t.Helper()
	k, err := DeriveKeys(fv.KDev)
	if err != nil {
		t.Fatal(err)
	}
	fk := k.Features()
	kind, _ := RType(fv.RType).EventKind()
	var f features.F
	switch kind {
	case event.KindPromptSubmit, event.KindCLITrack:
		if fv.PromptClass == nil || (fv.Text == nil) != (fv.ShingleValues == nil) {
			t.Fatal("a prompt's features object lacks prompt_class, or has only one of text and shingle_values")
		}
		if fv.Text == nil {
			// A prompt whose text the device never had (spec 10.1).
			if kind != event.KindPromptSubmit {
				t.Fatal("a track label always has its text")
			}
			if f, err = features.Textless(*fv.PromptClass); err != nil {
				t.Fatal(err)
			}
			break
		}
		if f, err = fk.Prompt(string(*fv.Text), *fv.PromptClass); err != nil {
			t.Fatal(err)
		}
		_, shingles := fk.Shingles(string(*fv.Text))
		if len(shingles) != len(*fv.ShingleValues) {
			t.Fatalf("pkg/ledger has %d shingles, the vectors %d values", len(shingles), len(*fv.ShingleValues))
		}
		for j, s := range shingles {
			sameBytes(t, fmt.Sprintf("shingle_values[%d]", j), s.V[:], (*fv.ShingleValues)[j])
		}
	case event.KindAgentTool:
		if fv.Hunks == nil {
			t.Fatal("an agent.tool features object lacks hunks")
		}
		var edits []features.Edit
		for _, h := range *fv.Hunks {
			edits = append(edits, features.Edit{Path: h.Path, OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines})
		}
		cmd := ""
		if fv.Command != nil {
			cmd = string(*fv.Command)
		}
		if f, err = fk.Tool(edits, cmd); err != nil {
			t.Fatal(err)
		}
	case event.KindShellCommand:
		cmd := ""
		if fv.Command != nil {
			cmd = string(*fv.Command)
		}
		f = fk.Shell(cmd)
	case event.KindRelayMessage:
		// An app's message commits nothing: its f is v and the class of the
		// app's own text (spec 10.7).
		if fv.PromptClass == nil || fv.Text != nil || fv.ShingleValues != nil {
			t.Fatal("an app message's features object has prompt_class, and no text or shingle_values")
		}
		if f, err = features.Relay(*fv.PromptClass); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("rtype 0x%02x carries no features", fv.RType)
	}
	got, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := json.Compact(&want, fv.F); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Errorf("f: pkg/ledger writes\n%s\nthe vectors have\n%s", got, want.Bytes())
	}
	back, err := features.Parse(kind, fv.F)
	if err != nil {
		t.Fatalf("features.Parse refuses the vectors' f: %v", err)
	}
	if again, err := json.Marshal(back); err != nil || !bytes.Equal(again, got) {
		t.Errorf("parsing and writing f again gives %s, %v", again, err)
	}
}

func TestVectorDiffs(t *testing.T) {
	for i, dv := range readContentVectors(t).Diffs {
		t.Run(fmt.Sprintf("diffs[%d] %s: the zero-context hunks reproduce", i, dv.Note), func(t *testing.T) {
			var got []features.Edit
			malformed := false
			if dv.Patch != nil {
				lines := make([]string, len(dv.Patch.Lines))
				for j, l := range dv.Patch.Lines {
					lines[j] = string(l)
				}
				var err error
				got, err = features.EditsFromPatch("f.go", dv.Patch.OldStart, dv.Patch.OldLines, dv.Patch.NewStart, dv.Patch.NewLines, lines)
				if err != nil {
					if !errors.Is(err, features.ErrPatch) {
						t.Fatalf("EditsFromPatch's error %v doesn't wrap ErrPatch", err)
					}
					malformed = true
				}
			} else {
				if dv.Before == nil || dv.After == nil {
					t.Fatal("a diff without a patch has no before and after")
				}
				if e, ok := features.EditFromTexts("f.go", string(*dv.Before), string(*dv.After)); ok {
					got = []features.Edit{e}
				}
			}
			if malformed != dv.Malformed {
				t.Fatalf("pkg/ledger finds the patch malformed %v, the vectors %v", malformed, dv.Malformed)
			}
			if malformed {
				return
			}
			var want []features.Edit
			for _, h := range *dv.Hunks {
				want = append(want, features.Edit{Path: "f.go", OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines})
			}
			if !slices.Equal(got, want) {
				t.Errorf("pkg/ledger gives %v, the vectors %v", got, want)
			}
		})
	}
}

func TestVectorJaccard(t *testing.T) {
	for i, jv := range readContentVectors(t).Jaccard {
		t.Run(fmt.Sprintf("jaccard[%d] %s: %d of 64 places match", i, jv.Note, jv.Matches), func(t *testing.T) {
			a, err := features.SketchFrom(jv.A)
			if err != nil {
				t.Fatal(err)
			}
			b, err := features.SketchFrom(jv.B)
			if err != nil {
				t.Fatal(err)
			}
			if m := a.Matches(b); m != jv.Matches {
				t.Errorf("pkg/ledger counts %d matches, the vectors %d", m, jv.Matches)
			}
			if a.Matches(b) != b.Matches(a) {
				t.Error("Matches isn't symmetric")
			}
		})
	}
}

func TestVectorDraws(t *testing.T) {
	for i, dv := range readContentVectors(t).Draws {
		t.Run(fmt.Sprintf("draws[%d] %s: the seed, every score, the drawn turns and the interview commits reproduce", i, dv.Note), func(t *testing.T) {
			checkDrawVector(t, dv)
		})
	}
}

// checkDrawVector recomputes one draw of the vectors.
func checkDrawVector(t *testing.T, dv drawVec) {
	t.Helper()
	r, err := HashOf(dv.R)
	if err != nil {
		t.Fatal(err)
	}
	seed := DrawSeed(r, dv.Metadata)
	sameBytes(t, "seed", seed[:], dv.Seed)
	if s := DrawSeedFromDigest(r, sha256.Sum256(dv.Metadata)); s != seed {
		t.Errorf("DrawSeedFromDigest gives %s from SHA-256(M), and DrawSeed %s from M", s, seed)
	}
	pool := make([]Turn, len(dv.Pool))
	for j, p := range dv.Pool {
		pool[j] = Turn{DeviceID: p.DeviceID, Seq: p.Seq}
		s, err := DrawScore(seed, pool[j])
		if err != nil {
			t.Fatal(err)
		}
		sameBytes(t, fmt.Sprintf("pool[%d].score", j), s[:], p.Score)
	}
	drawn, err := Draw(seed, pool, drawTable(t))
	if err != nil {
		t.Fatal(err)
	}
	want := make([]Turn, len(dv.Drawn))
	for j, d := range dv.Drawn {
		want[j] = Turn{DeviceID: d.DeviceID, Seq: d.Seq}
	}
	if !slices.Equal(drawn, want) {
		t.Errorf("drawn: pkg/ledger draws %v, the vectors %v", drawn, want)
	}
	if dv.Commits == nil {
		return
	}
	commits := make([][]byte, len(*dv.Commits))
	for j, c := range *dv.Commits {
		commits[j] = c.Commit
		s, err := CommitScore(seed, c.Commit)
		if err != nil {
			t.Fatal(err)
		}
		sameBytes(t, fmt.Sprintf("commits[%d].score", j), s[:], c.Score)
	}
	got, err := DrawCommits(seed, commits)
	if err != nil {
		t.Fatal(err)
	}
	if dv.DrawnCommits == nil || len(got) != len(*dv.DrawnCommits) {
		t.Fatalf("pkg/ledger draws %d interview commits, the vectors list another number", len(got))
	}
	for j, c := range *dv.DrawnCommits {
		sameBytes(t, fmt.Sprintf("drawn_commits[%d]", j), got[j], c.Commit)
	}
}

func TestVectorChallengeK(t *testing.T) {
	for _, cv := range readContentVectors(t).ChallengeK {
		t.Run(fmt.Sprintf("a pool of %d draws %d of CHALLENGE_K %d", cv.Pool, cv.Drawn, cv.K), func(t *testing.T) {
			if k := ChallengeK(drawTable(t), cv.Pool); k != int64(cv.K) || min(k, int64(cv.Pool)) != int64(cv.Drawn) {
				t.Errorf("ChallengeK(%d) = %d", cv.Pool, k)
			}
		})
	}
}
