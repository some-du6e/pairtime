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
