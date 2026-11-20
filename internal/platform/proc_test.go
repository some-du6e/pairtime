// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// at is a start time, s seconds into a fixed day.
func at(s int) time.Time {
	return time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC).Add(time.Duration(s) * time.Second)
}

func pids(ps []Proc) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.PID
	}
	return out
}

func TestChain(t *testing.T) {
	tests := []struct {
		name  string
		procs []Proc
		pid   int
		want  []int
	}{
		{
			"a chain runs from the process up to the root",
			[]Proc{{PID: 1, Start: at(0)}, {PID: 10, PPID: 1, Start: at(1)}, {PID: 20, PPID: 10, Start: at(2)}},
			20, []int{20, 10, 1},
		},
		{
			"a chain stops at a parent that has exited",
			[]Proc{{PID: 20, PPID: 10, Start: at(2)}},
			20, []int{20},
		},
		{
			"a chain stops at a parent that started after the child, a reused pid",
			[]Proc{{PID: 10, PPID: 1, Start: at(50)}, {PID: 20, PPID: 10, Start: at(2)}},
			20, []int{20},
		},
		{
			"a parent that started in the same instant is still the parent",
			[]Proc{{PID: 10, Start: at(2)}, {PID: 20, PPID: 10, Start: at(2)}},
			20, []int{20, 10},
		},
		{
			"an unknown start time can't rule a parent out",
			[]Proc{{PID: 10, PPID: 1}, {PID: 20, PPID: 10, Start: at(2)}},
			20, []int{20, 10},
		},
		{
			"a process that is its own parent ends the chain",
			[]Proc{{PID: 4, PPID: 4}},
			4, []int{4},
		},
		{
			"a cycle in a damaged table ends the chain",
			[]Proc{{PID: 10, PPID: 20}, {PID: 20, PPID: 10}},
			10, []int{10, 20},
		},
		{
			"a pid that isn't in the table has no chain",
			[]Proc{{PID: 10}},
			99, []int{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pids(NewTable(Linux, tt.procs).Chain(tt.pid))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Chain(%d) = %v, want %v", tt.pid, got, tt.want)
			}
		})
	}
}

func TestChainIsCapped(t *testing.T) {
	t.Run("a chain deeper than MaxChain stops at MaxChain", func(t *testing.T) {
		var procs []Proc
		for pid := 1; pid <= 3*MaxChain; pid++ {
			procs = append(procs, Proc{PID: pid, PPID: pid + 1})
		}
		if got := len(NewTable(Linux, procs).Chain(1)); got != MaxChain {
			t.Errorf("a %d-deep chain came back %d long, want the cap %d", 3*MaxChain, got, MaxChain)
		}
	})
}

func TestDescendants(t *testing.T) {
	// A terminal with two shells, one running claude; a stale child whose
	// parent pid was reused by the terminal after it exited; an unrelated
	// process.
	procs := []Proc{
		{PID: 100, PPID: 1, Start: at(10), Name: "windowsterminal"},
		{PID: 110, PPID: 100, Start: at(11), Name: "bash"},
		{PID: 111, PPID: 110, Start: at(12), Name: "claude"},
		{PID: 120, PPID: 100, Start: at(13), Name: "pwsh"},
		{PID: 130, PPID: 100, Start: at(5), Name: "orphan"},
		{PID: 200, PPID: 1, Start: at(14), Name: "chrome"},
	}
	tests := []struct {
		name string
		pid  int
		want []int
	}{
		{"a window's descendants are every process under it", 100, []int{110, 111, 120}},
		{"a leaf has no descendants", 111, []int{}},
		{"a pid that isn't in the table has none", 999, []int{}},
	}
	tab := NewTable(Windows, procs)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pids(tab.Descendants(tt.pid)); !slices.Equal(got, tt.want) {
				t.Errorf("Descendants(%d) = %v, want %v", tt.pid, got, tt.want)
			}
		})
	}
}

func TestTTYText(t *testing.T) {
	tests := []struct {
		name string
		tty  TTY
		text string
	}{
		{"unknown reads as unknown", TTYUnknown, "unknown"},
		{"none reads as none", TTYNone, "none"},
		{"attached reads as attached", TTYAttached, "attached"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := tt.tty.MarshalText()
			if err != nil || string(b) != tt.text {
				t.Fatalf("MarshalText = %q, %v, want %q", b, err, tt.text)
			}
			var back TTY
			if err := back.UnmarshalText(b); err != nil || back != tt.tty {
				t.Errorf("UnmarshalText(%q) = %v, %v, want %v", b, back, err, tt.tty)
			}
		})
	}
	t.Run("an unknown word is an error", func(t *testing.T) {
		var p Proc
		if err := json.Unmarshal([]byte(`{"pid": 1, "tty": "maybe"}`), &p); err == nil {
			t.Error("tty \"maybe\" was accepted")
		}
	})
}
