// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDrop(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, s *Buckets)
	}{
		{
			name: "the files of the days before from go, and those from it on stay",
			run: func(t *testing.T, s *Buckets) {
				for _, day := range []string{"2026-05-25", "2026-05-26", "2026-05-27", "2026-11-23"} {
					if err := os.WriteFile(s.path(day), []byte("{}\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				n, err := s.Drop("2026-05-27")
				if err != nil || n != 2 {
					t.Fatalf("Drop = %d, %v; want 2", n, err)
				}
				for day, want := range map[string]bool{"2026-05-25": false, "2026-05-26": false, "2026-05-27": true, "2026-11-23": true} {
					_, err := os.Stat(s.path(day))
					if got := err == nil; got != want || err != nil && !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("%s is there %v (%v), want %v", day, got, err, want)
					}
				}
			},
		},
		{
			name: "a day the store has read is forgotten and its file deleted",
			run: func(t *testing.T, s *Buckets) {
				if err := os.WriteFile(s.path("2026-05-01"), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				s.mu.Lock()
				_, err := s.day("2026-05-01")
				s.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				if n, err := s.Drop("2026-05-02"); err != nil || n != 1 {
					t.Fatalf("Drop = %d, %v; want 1", n, err)
				}
				s.mu.Lock()
				_, held := s.days["2026-05-01"]
				s.mu.Unlock()
				if held {
					t.Error("the store still holds the day it dropped")
				}
			},
		},
		{
			name: "a file that isn't a day's is left alone",
			run: func(t *testing.T, s *Buckets) {
				name := filepath.Join(s.Dir(), "notes.txt")
				if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Drop("2030-01-01"); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(name); err != nil {
					t.Errorf("notes.txt: %v", err)
				}
			},
		},
		{
			name: "a from that isn't a UTC day is refused",
			run: func(t *testing.T, s *Buckets) {
				if _, err := s.Drop("soon"); err == nil {
					t.Error("Drop took soon")
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := OpenBuckets(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			c.run(t, s)
		})
	}
}
