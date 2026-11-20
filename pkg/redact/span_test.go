// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"reflect"
	"testing"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name string
		in   []Span
		want []Span
	}{
		{"nothing in gives nothing out", nil, nil},
		{"disjoint spans come out sorted",
			[]Span{{Start: 5, End: 7, Type: TypeEmail}, {Start: 0, End: 2, Type: TypeHost}},
			[]Span{{Start: 0, End: 2, Type: TypeHost}, {Start: 5, End: 7, Type: TypeEmail}}},
		{"spans that only touch stay apart",
			[]Span{{Start: 0, End: 2, Type: TypeHost}, {Start: 2, End: 4, Type: TypeEmail}},
			[]Span{{Start: 0, End: 2, Type: TypeHost}, {Start: 2, End: 4, Type: TypeEmail}}},
		{"an overlap takes the type of its longest member",
			[]Span{{Start: 6, End: 10, Type: TypeAccount}, {Start: 0, End: 10, Type: TypeHome}},
			[]Span{{Start: 0, End: 10, Type: TypeHome}}},
		{"a chain of overlaps becomes one span over all of them",
			[]Span{{Start: 0, End: 4, Type: TypeHost}, {Start: 3, End: 8, Type: TypeAccount}, {Start: 7, End: 12, Type: TypeEmail}},
			[]Span{{Start: 0, End: 12, Type: TypeAccount}}},
		{"a tie in length goes to the type earlier in pipeline order",
			[]Span{{Start: 0, End: 5, Type: TypeEmail}, {Start: 0, End: 5, Type: TypeGitEmail}},
			[]Span{{Start: 0, End: 5, Type: TypeGitEmail}}},
		{"a code block keeps its details when a secret inside it merges",
			[]Span{{Start: 10, End: 20, Type: TypeSecret, Rule: "github-pat"}, {Start: 0, End: 50, Type: TypeCode, Lines: 3, Lang: "go"}},
			[]Span{{Start: 0, End: 50, Type: TypeCode, Lines: 3, Lang: "go"}}},
		{"a secret that outgrows a known value keeps its rule",
			[]Span{{Start: 0, End: 4, Type: TypeDeny}, {Start: 2, End: 30, Type: TypeSecret, Rule: "jwt"}},
			[]Span{{Start: 0, End: 30, Type: TypeSecret, Rule: "jwt"}}},
		{"a vendor's rule wins a tie with generic-api-key, in either order",
			[]Span{{Start: 0, End: 9, Type: TypeSecret, Rule: "generic-api-key"}, {Start: 0, End: 9, Type: TypeSecret, Rule: "stripe-access-token"},
				{Start: 20, End: 29, Type: TypeSecret, Rule: "stripe-access-token"}, {Start: 20, End: 29, Type: TypeSecret, Rule: "generic-api-key"}},
			[]Span{{Start: 0, End: 9, Type: TypeSecret, Rule: "stripe-access-token"}, {Start: 20, End: 29, Type: TypeSecret, Rule: "stripe-access-token"}}},
		{"a tie between two vendors' rules goes to the id that sorts first",
			[]Span{{Start: 0, End: 9, Type: TypeSecret, Rule: "slack-bot-token"}, {Start: 0, End: 9, Type: TypeSecret, Rule: "aws-access-token"}},
			[]Span{{Start: 0, End: 9, Type: TypeSecret, Rule: "aws-access-token"}}},
		{"empty and inverted spans are dropped",
			[]Span{{Start: 3, End: 3, Type: TypeHost}, {Start: 5, End: 4, Type: TypeHost}, {Start: -1, End: 2, Type: TypeHost}},
			nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Merge(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Merge = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		spans []Span
		want  string
	}{
		{"no spans leave the text alone", "hello", nil, "hello"},
		{"a span becomes its marker", "mail maya@x.org now", []Span{{Start: 5, End: 15, Type: TypeEmail}}, "mail [EMAIL] now"},
		{"overlapping spans render once", "abcdef", []Span{{Start: 0, End: 4, Type: TypeHost}, {Start: 2, End: 6, Type: TypeSecret}}, "[HOST]"},
		{"a span at the very end", "key=abc", []Span{{Start: 4, End: 7, Type: TypeSecret}}, "key=[SECRET]"},
		{"a span past the end is cut at the end", "abc", []Span{{Start: 1, End: 9, Type: TypeDeny}}, "a[DENY]"},
		{"a span that starts past the end is ignored", "abc", []Span{{Start: 5, End: 9, Type: TypeDeny}}, "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Render(tt.text, tt.spans); got != tt.want {
				t.Errorf("Render = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPreview checks that the preview shows what a reveal carries: whole
// tokens, the five reveal types, and a code block's line count without its
// language.
func TestPreview(t *testing.T) {
	code := "see:\n```go\nx\n```\nok"
	tests := []struct {
		name  string
		text  string
		spans []Span
		want  string
	}{
		{"an email address takes its whole token and shows as pii", "write to email:teen@example.com, ok",
			[]Span{{Start: 15, End: 31, Type: TypeEmail}}, "write to [PII] ok"},
		{"every known value shows as known", "Maya Kowalski said hi",
			[]Span{{Start: 0, End: 13, Type: TypeGitName}}, "[KNOWN] said hi"},
		{"a secret shows as secret, without its rule", "key sk_test_x here",
			[]Span{{Start: 4, End: 13, Type: TypeSecret, Rule: "stripe-access-token"}}, "key [SECRET] here"},
		{"code shows its line count and never its language", code, CodeBlocks(code), "see:\n[CODE: 1 line]\nok"},
		{"an empty code block shows no count", "```\n```", CodeBlocks("```\n```"), "[CODE]"},
		{"a span the teen removed shows as teen", "my friend Sam", []Span{{Start: 10, End: 13, Type: TypeManual}}, "my friend [TEEN]"},
		{"a secret inside a code block shows once, as the code", "```\nkey\n```",
			[]Span{{Start: 0, End: 11, Type: TypeCode, Lines: 1}, {Start: 4, End: 7, Type: TypeSecret, Rule: "x"}}, "[CODE: 1 line]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Preview(tt.text, tt.spans); got != tt.want {
				t.Errorf("Preview = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarker(t *testing.T) {
	tests := []struct {
		name string
		span Span
		want string
	}{
		{"a type shows in capitals", Span{Type: TypeGitEmail}, "[GIT_EMAIL]"},
		{"a secret doesn't show its rule", Span{Type: TypeSecret, Rule: "github-pat"}, "[SECRET]"},
		{"code shows its language and line count", Span{Type: TypeCode, Lines: 12, Lang: "go"}, "[CODE go: 12 lines]"},
		{"one line of code is singular", Span{Type: TypeCode, Lines: 1}, "[CODE: 1 line]"},
		{"an empty block has zero lines", Span{Type: TypeCode}, "[CODE: 0 lines]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.span.Marker(); got != tt.want {
				t.Errorf("Marker = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTypeValid(t *testing.T) {
	tests := []struct {
		name string
		t    Type
		want bool
	}{
		{"every listed type is valid", TypeAadhaar, true},
		{"the teen's own removals are valid", TypeManual, true},
		{"an unknown name isn't", Type("name"), false},
		{"the empty type isn't", Type(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.Valid(); got != tt.want {
				t.Errorf("%q.Valid() = %v, want %v", tt.t, got, tt.want)
			}
		})
	}
	if len(typeOrder) != 18 {
		t.Errorf("typeOrder has %d types, want 18", len(typeOrder))
	}
}

func TestTokens(t *testing.T) {
	// The ledger spec's worked example (docs/spec/ledger-v1.md 10.10): 15
	// tokens, and token 7 is "src/nav.js:12" at bytes 18 to 31.
	spec := "still broken, see src/nav.js:12\r\nTypeError: x is undefined"
	tests := []struct {
		name  string
		text  string
		spans []Span
		want  []Span
	}{
		{"a span inside a token takes the whole token", spec,
			[]Span{{Start: 22, End: 28, Type: TypePrivate}},
			[]Span{{Start: 18, End: 31, Type: TypePrivate}}},
		{"an email address inside punctuation takes its token", "write to email:teen@example.com, ok",
			[]Span{{Start: 15, End: 31, Type: TypeEmail}},
			[]Span{{Start: 9, End: 32, Type: TypeEmail}}},
		{"a span already on token edges stays", "Maya Kowalski said",
			[]Span{{Start: 0, End: 13, Type: TypeGitName}},
			[]Span{{Start: 0, End: 13, Type: TypeGitName}}},
		{"two spans in one token become one", "key=abc:def",
			[]Span{{Start: 4, End: 7, Type: TypeSecret}, {Start: 8, End: 11, Type: TypeDeny}},
			[]Span{{Start: 0, End: 11, Type: TypeDeny}}},
		{"a span that ends inside whitespace takes the whole run", "a  b",
			[]Span{{Start: 0, End: 2, Type: TypeDeny}},
			[]Span{{Start: 0, End: 3, Type: TypeDeny}}},
		{"a no-break space separates tokens like any whitespace", "x y z",
			[]Span{{Start: 3, End: 4, Type: TypeDeny}},
			[]Span{{Start: 3, End: 4, Type: TypeDeny}}},
		{"spans outside the text are dropped", "abc",
			[]Span{{Start: 5, End: 9, Type: TypeDeny}, {Start: -2, End: 1, Type: TypeDeny}},
			nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Tokens(tt.text, tt.spans); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Tokens = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReveal(t *testing.T) {
	want := map[Type]string{
		TypeAccount: "known", TypeHome: "known", TypeHost: "known", TypeGitName: "known", TypeGitEmail: "known",
		TypeGitHub: "known", TypeHCA: "known", TypePrivate: "known", TypeDeny: "known",
		TypeSecret: "secret",
		TypeCard:   "pii", TypeIBAN: "pii", TypeUSSSN: "pii", TypeUKNINO: "pii", TypeAadhaar: "pii", TypeEmail: "pii",
		TypeCode:   "code",
		TypeManual: "teen",
	}
	for _, typ := range typeOrder {
		t.Run(string(typ), func(t *testing.T) {
			if got := typ.Reveal(); got != want[typ] {
				t.Errorf("%q.Reveal() = %q, want %q", typ, got, want[typ])
			}
		})
	}
	if len(want) != len(typeOrder) {
		t.Errorf("the table covers %d types, typeOrder has %d", len(want), len(typeOrder))
	}
	if got := Type("name").Reveal(); got != "" {
		t.Errorf(`an unknown type reveals as %q, want ""`, got)
	}
}
