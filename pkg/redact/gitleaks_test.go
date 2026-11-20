// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const corpusPath = "../../testdata/redact/gitleaks/corpus.jsonl.gz"

type corpusSample struct {
	Text    string   `json:"text"`
	Path    string   `json:"path"`
	Secrets []string `json:"secrets"`
}

type corpusCase struct {
	Rule string         `json:"rule"`
	TPs  []corpusSample `json:"tps"`
	FPs  []corpusSample `json:"fps"`
}

func readCorpus(t *testing.T) []corpusCase {
	t.Helper()
	f, err := os.Open(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(zr)
	var cases []corpusCase
	for {
		var c corpusCase
		err := dec.Decode(&c)
		if errors.Is(err, io.EOF) {
			return cases
		}
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
}

func mustRules(t *testing.T) *ruleset {
	t.Helper()
	rs, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func mustNew(t *testing.T, k Known) *Redactor {
	t.Helper()
	r, err := New(k)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// ruleSecrets is the secrets one rule finds in text on its own, in one
// pass, the way gitleaks' single-rule detector ran it.
func ruleSecrets(rs *ruleset, r *rule, text string) []string {
	got := []string{}
	r.find(text, strings.ToLower(text), rs.global, func(secret, _ [2]int) {
		got = append(got, text[secret[0]:secret[1]])
	})
	slices.Sort(got)
	return got
}

// TestFragmentSecretsRemoveTheToken covers the rules whose secret group is
// a piece of the token: the whole token must go.
func TestFragmentSecretsRemoveTheToken(t *testing.T) {
	uuid := "1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d"
	teams := "https://contoso.webhook" + ".office.com/webhookb2/" + uuid + "@" + uuid +
		"/IncomingWebhook/" + strings.Repeat("0f1e2d3c", 4) + "/" + uuid
	// Shaped like a base64-encoded JWT: "ZXlK" ("eyJ" encoded), then the
	// encoded "alg" header field jwt-base64 looks for, then 40 or more
	// base64 characters.
	jwt64 := "ZXlKaGJHY2lPaUpJVXpJMU5pSXNJblI1Y0NJNklrcFhWQ0o5LmV5SnBjM01pT2lKcWIyVWlMQTBLSUNKbGVIQWlPakV6TURBNE1Ua3pPREFzRFFvZ0ltaDBkSEE2THk5bGVHRnRjR3hsTG1OdmJTOXBjMTl5YjI5MElqcDBjblZsZlEuZEJqZnRKZVo0Q1ZQLW1COTJLMjd1aGJVSlUxcDFyX3dXMWdGV0ZPRWpYaw"
	tests := []struct {
		name string
		text string
		rule string
	}{
		{"a Teams webhook URL goes whole", "post to " + teams + " please", "microsoft-teams-webhook"},
		{"a base64-encoded JWT goes whole", "token: " + jwt64 + " ok", "jwt-base64"},
	}
	rs := mustRules(t)
	r := mustNew(t, Known{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ruleSecrets(rs, rs.byID[tt.rule], tt.text); len(got) == 0 {
				t.Fatalf("%s finds nothing in %q", tt.rule, tt.text)
			}
			out := Render(tt.text, r.Find(tt.text))
			if strings.Contains(out, uuid) || strings.Contains(out, "IncomingWebhook") || strings.Contains(out, "ZXlK") || strings.Contains(out, "LmV5") {
				t.Errorf("Render = %q, still holds part of the token", out)
			}
		})
	}
}

func TestGitleaksRulesCompile(t *testing.T) {
	rs := mustRules(t)
	if rs.tag != "v8.30.1" {
		t.Errorf("rules come from gitleaks %s, want v8.30.1", rs.tag)
	}
	// 222 rules, less pkcs12-file, which only names files.
	if len(rs.rules) != 221 {
		t.Errorf("loaded %d rules with a regex, want 221", len(rs.rules))
	}
	if len(rs.global) != 1 {
		t.Errorf("loaded %d global allowlists, want 1", len(rs.global))
	}
	if a, b := mustNew(t, Known{}), mustNew(t, testTeen); a.rules != rs || b.rules != rs {
		t.Error("a Redactor loaded its own copy of the rules")
	}
	for _, r := range rs.rules {
		t.Run(r.id, func(t *testing.T) {
			if _, err := regexp.Compile(r.pattern); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestGitleaksCorpus is the planted corpus of DESIGN 8.7: every true
// positive in gitleaks' own rule sources at the pinned tag, with the
// secrets gitleaks' detector found in it (testdata/redact/gitleaks). Each
// rule must find exactly what gitleaks found, the whole pipeline must leave
// none of it in the text, and no rule may fire on its own false positives.
func TestGitleaksCorpus(t *testing.T) {
	rs := mustRules(t)
	full := mustNew(t, Known{})
	cases := readCorpus(t)
	if len(cases) != 220 {
		t.Errorf("corpus has %d rule cases, want 220", len(cases))
	}
	covered := map[string]bool{}
	for _, c := range cases {
		r, ok := rs.byID[c.Rule]
		if !ok {
			if c.Rule != "pkcs12-file" { // flags a file by name; text has none
				t.Errorf("corpus rule %q isn't in the rules", c.Rule)
			}
			continue
		}
		covered[c.Rule] = true
		t.Run(c.Rule, func(t *testing.T) {
			if len(c.TPs) == 0 {
				t.Fatal("no true positives")
			}
			for i, tp := range c.TPs {
				want := slices.Clone(tp.Secrets)
				slices.Sort(want)
				if got := ruleSecrets(rs, r, tp.Text); !slices.Equal(got, want) {
					t.Errorf("true positive %d %q: the rule finds %q, gitleaks found %q", i, tp.Text, got, want)
				}
				out := Render(tp.Text, full.Find(tp.Text))
				for _, s := range tp.Secrets {
					if strings.Contains(out, s) {
						t.Errorf("true positive %d: %q is still in %q", i, s, out)
					}
				}
			}
			for i, fp := range c.FPs {
				if fp.Path != "" {
					// gitleaks kept it because of its path, and paths
					// don't limit rules here.
					continue
				}
				if got := ruleSecrets(rs, r, fp.Text); len(got) > 0 {
					t.Errorf("false positive %d %q: the rule finds %q, gitleaks found nothing", i, fp.Text, got)
				}
			}
		})
	}
	var missing []string
	for _, r := range rs.rules {
		if !covered[r.id] {
			missing = append(missing, r.id)
		}
	}
	slices.Sort(missing)
	// gitleaks ships these two without examples (dropbox.go doesn't call
	// Validate), so there's nothing to extract.
	if want := []string{"dropbox-long-lived-api-token", "dropbox-short-lived-api-token"}; !slices.Equal(missing, want) {
		t.Errorf("rules without examples: %q, want %q", missing, want)
	}
}

// The keys below are built at run time so that no file in the repo holds
// something a secret scanner would report.
func fakeKey(prefix, body string, n int, suffix string) string {
	return prefix + strings.Repeat(body, n/len(body)+1)[:n] + suffix
}

func TestSecretsInProse(t *testing.T) {
	anthropic := fakeKey("sk-ant-"+"api03-", "aB3_x9Kq-Z", 93, "AA")
	ghp := fakeKey("gh"+"p_", "Zx81Qw4LmN", 36, "")
	stripe := fakeKey("sk_"+"live_", "Qw81Zx4LmN", 24, "")
	hexKey := "a1b2c3d4e5f6" + "0718293a4b5c6d7e8f90"
	tests := []struct {
		name string
		text string
		key  string
		rule string
	}{
		{"a key at the end of a sentence", "my key is " + anthropic + ".", anthropic, "anthropic-api-key"},
		{"a key before a comma", "use " + stripe + ", it's the live one", stripe, "stripe-access-token"},
		{"a key in parentheses", "(token " + stripe + ")", stripe, "stripe-access-token"},
		{"a key in bold", "**" + anthropic + "**", anthropic, "anthropic-api-key"},
		{"a GitHub token mid-sentence", "the token " + ghp + " stopped working", ghp, "github-pat"},
		{"a key in a JSON string with an escaped newline", `{"text": "key ` + stripe + `\nthanks"}`, stripe, "stripe-access-token"},
		{"a key on a prompt's last line isn't hidden by an allowlisted line before it",
			"RUN --mount=type=secret,id=npmrc npm ci\nENV API_TOKEN=" + hexKey, hexKey, "generic-api-key"},
		{"a key in smart quotes", "my key is “" + stripe + "”", stripe, "stripe-access-token"},
		{"a key before a no-break space", stripe + "\xc2\xa0is mine", stripe, "stripe-access-token"},
		{"a key before an ellipsis", "try " + stripe + "… it fails", stripe, "stripe-access-token"},
		{"a key before a CJK full stop", "我的密钥是" + stripe + "。", stripe, "stripe-access-token"},
		{"a key between Chinese words with no space", "密钥是" + stripe + "请保密", stripe, "stripe-access-token"},
		{"a key before a fullwidth comma", stripe + "，谢谢", stripe, "stripe-access-token"},
		{"a key before a Devanagari danda", "मेरी कुंजी " + stripe + "।", stripe, "stripe-access-token"},
		{"a key in single smart quotes", "it's ‘" + stripe + "’", stripe, "stripe-access-token"},
		{"a key before a narrow no-break space", stripe + "\xe2\x80\xafis mine", stripe, "stripe-access-token"},
		{"a key before a thin space, one of the spaces from U+2000 to U+200A", stripe + "\xe2\x80\x89is mine", stripe, "stripe-access-token"},
		{"a key before an ideographic space", stripe + "\xe3\x80\x80是我的", stripe, "stripe-access-token"},
	}
	r := mustNew(t, Known{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := strings.Index(tt.text, tt.key)
			want := Span{Start: start, End: start + len(tt.key), Type: TypeSecret, Rule: tt.rule}
			spans := r.Find(tt.text)
			if !slices.Contains(spans, want) {
				t.Errorf("Find = %v, want it to contain %v", spans, want)
			}
			if out := Render(tt.text, spans); strings.Contains(out, tt.key) {
				t.Errorf("Render = %q, still holds the key", out)
			}
		})
	}
}

// TestSecretsInJSON covers keys in raw JSON captures, where a quoted value
// is written \"...\" and a line break \n, so the byte after a key is a
// backslash and the byte before one that starts a line is an n.
func TestSecretsInJSON(t *testing.T) {
	stripe := fakeKey("sk_"+"live_", "Qw81Zx4LmN", 24, "")
	hexKey := "a1b2c3d4e5f6" + "0718293a4b5c6d7e8f90"
	tests := []struct {
		name string
		text string
		key  string
		rule string
	}{
		{"a key quoted inside a JSON string", `{"input":{"content":"STRIPE_KEY=\"` + stripe + `\"\n"}}`, stripe, "stripe-access-token"},
		{"a key at the start of an embedded line", `{"text":"my key:\n` + stripe + `"}`, stripe, "stripe-access-token"},
		{"a key that ends an embedded sentence before the closing quote", `{"text":"it is ` + stripe + `.\""}`, stripe, "stripe-access-token"},
		{"a key that ends an embedded sentence before a line break", `{"text":"` + stripe + `.\nthanks"}`, stripe, "stripe-access-token"},
		{"an allowlisted embedded line doesn't hide a key on the next one",
			`{"content":"RUN --mount=type=secret,id=npmrc npm ci\nENV API_TOKEN=` + hexKey + `\n"}`, hexKey, "generic-api-key"},
		{"an allowlisted embedded line doesn't hide a key on the last one",
			`{"content":"RUN --mount=type=secret,id=npmrc npm ci\nENV API_TOKEN=` + hexKey + `"}`, hexKey, "generic-api-key"},
	}
	r := mustNew(t, Known{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := strings.Index(tt.text, tt.key)
			want := Span{Start: start, End: start + len(tt.key), Type: TypeSecret, Rule: tt.rule}
			spans := r.Find(tt.text)
			if !slices.Contains(spans, want) {
				t.Errorf("Find = %v, want it to contain %v", spans, want)
			}
			out := Render(tt.text, spans)
			if strings.Contains(out, tt.key) {
				t.Errorf("Render = %q, still holds the key", out)
			}
			if !json.Valid([]byte(out)) {
				t.Errorf("Render = %q, which isn't JSON any more", out)
			}
		})
	}
}

func TestProseView(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{"a period that ends a sentence becomes a space", "key abc.", "key abc ", true},
		{"a closing paren and a period both go", "(key abc).", "(key abc  ", true},
		{"punctuation inside a token stays", "a.b c,d", "a.b c,d", false},
		{"punctuation before a line break goes", "abc,\ndef", "abc \ndef", true},
		{"bold markers after a token go and before it stay", "**abc** x", "**abc   x", true},
		{"text without punctuation comes back unchanged", "plain words", "plain words", false},
		{"punctuation before a quote goes", `"key abc."`, `"key abc "`, true},
		{"punctuation before a backslash goes, as at the end of a JSON string", `key abc.\"`, `key abc \"`, true},
		{"punctuation before a semicolon goes", "abc.;", "abc ;", true},
		{"smart quotes become spaces, every byte of them", "“abc”", "   abc   ", true},
		{"an ellipsis becomes spaces", "abc… x", "abc    x", true},
		{"a CJK full stop becomes spaces", "abc。", "abc   ", true},
		{"a Devanagari danda becomes spaces", "abc।", "abc   ", true},
		{"a no-break space becomes plain spaces", "abc\xc2\xa0x", "abc  x", true},
		{"Chinese text right after a token becomes spaces", "abc请", "abc   ", true},
		{"punctuation before a no-break space goes", "abc.\xc2\xa0x", "abc   x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := proseView(tt.in)
			if got != tt.want || changed != tt.changed {
				t.Errorf("proseView(%q) = %q, %v; want %q, %v", tt.in, got, changed, tt.want, tt.changed)
			}
		})
	}
}

func TestShannonEntropy(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want float64
	}{
		{"empty text has none", "", 0},
		{"one repeated character has none", "aaaa", 0},
		{"two characters half each is one bit", "abab", 1},
		{"eight distinct characters are three bits", "abcdefgh", 3},
		{"runes are counted but bytes divide, as in gitleaks", "éé", 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shannonEntropy(tt.in); math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("shannonEntropy(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestGitleaksLine(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		start, end int
		want       string
	}{
		{"one line is the whole text", "a=secret", 2, 8, "a=secret"},
		{"a later line starts at the line break before it", "x\na=secret\ny", 4, 10, "\na=secret"},
		{"the first line has no break before it", "a=secret\ny", 2, 8, "a=secret"},
		{"the last line without a break starts at the break before it, like any other line", "x\na=secret", 4, 10, "\na=secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitleaksLine(tt.raw, tt.start, tt.end); got != tt.want {
				t.Errorf("gitleaksLine = %q, want %q", got, tt.want)
			}
		})
	}
}
