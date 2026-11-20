// SPDX-License-Identifier: AGPL-3.0-or-later

package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cursor struct {
	File   string `json:"file"`
	Offset int64  `json:"offset"`
}

func TestStore(t *testing.T) {
	tests := []struct {
		name        string
		before      string // state.json before Open; "" for no file
		run         func(t *testing.T, name string, s *Store)
		wantProblem bool
		wantBad     string // what state.json.bad holds afterwards; "" for no such file
	}{
		{
			name: "a missing file opens an empty store",
			run: func(t *testing.T, _ string, s *Store) {
				var c cursor
				if ok, err := s.Get("tailers", &c); ok || err != nil {
					t.Errorf("Get on an empty store = %v, %v; want false, nil", ok, err)
				}
			},
		},
		{
			name: "a value that was Put and Saved is there after Open",
			run: func(t *testing.T, name string, s *Store) {
				want := cursor{File: "a.jsonl", Offset: 42}
				if err := s.Put("tailers", want); err != nil {
					t.Fatal(err)
				}
				if err := s.Save(); err != nil {
					t.Fatal(err)
				}
				again, problem, err := Open(name)
				if err != nil || problem != "" {
					t.Fatalf("Open after Save: %v, %q", err, problem)
				}
				var got cursor
				if ok, err := again.Get("tailers", &got); !ok || err != nil || got != want {
					t.Errorf("Get after Open = %+v, %v, %v; want %+v", got, ok, err, want)
				}
			},
		},
		{
			name: "Save writes the format version",
			run: func(t *testing.T, name string, s *Store) {
				if err := s.Save(); err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(b), `"v":1`) {
					t.Errorf("state.json = %s, want it to hold \"v\":1", b)
				}
			},
		},
		{
			name:        "a file that isn't JSON is moved to state.json.bad and the store opens empty",
			before:      "{not json",
			run:         func(*testing.T, string, *Store) {},
			wantProblem: true,
			wantBad:     "{not json",
		},
		{
			name:        "a file of another format version is moved aside",
			before:      `{"v":2,"tailers":{}}`,
			run:         func(*testing.T, string, *Store) {},
			wantProblem: true,
			wantBad:     `{"v":2,"tailers":{}}`,
		},
		{
			name: "the key v is kept for the format version",
			run: func(t *testing.T, _ string, s *Store) {
				if err := s.Put("v", 7); err == nil {
					t.Error(`Put("v") succeeded, want an error`)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "state.json")
			if tt.before != "" {
				if err := os.WriteFile(name, []byte(tt.before), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			s, problem, err := Open(name)
			if err != nil {
				t.Fatal(err)
			}
			if (problem != "") != tt.wantProblem {
				t.Errorf("Open's problem = %q, want one: %v", problem, tt.wantProblem)
			}
			tt.run(t, name, s)
			bad, err := os.ReadFile(name + ".bad")
			switch {
			case tt.wantBad == "" && err == nil:
				t.Errorf("state.json.bad exists, holding %q", bad)
			case tt.wantBad != "" && string(bad) != tt.wantBad:
				t.Errorf("state.json.bad holds %q (%v), want %q", bad, err, tt.wantBad)
			}
		})
	}
}
