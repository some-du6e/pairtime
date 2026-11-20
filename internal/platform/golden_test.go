// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// GoldenFile is one file in testdata/procs: a scenario from SM, as process
// tables on one or more OSes, each with the classification the rules give
// it. The scanner tests in internal/agent/procs read the same files.
type GoldenFile struct {
	Case   string        `json:"case"`
	Source string        `json:"source"`
	Bare   bool          `json:"bare,omitempty"`
	Tables []GoldenTable `json:"tables"`
}

// GoldenTable is one process table and what classifying its harness gives.
type GoldenTable struct {
	OS         string `json:"os"`
	HarnessPID int    `json:"harness_pid"`
	Want       struct {
		Harness     event.Harness     `json:"harness"`
		ParentClass event.ParentClass `json:"parent_class"`
		Entrypoint  event.Entrypoint  `json:"entrypoint"`
		HasTTY      bool              `json:"has_tty"`
		DecidedBy   string            `json:"decided_by"`
		ScriptShell bool              `json:"script_shell"`
	} `json:"want"`
	// SMParentClass is what SM says the process tree shows. Where it
	// differs from Want, Erratum names the file in docs/errata that says why.
	SMParentClass event.ParentClass `json:"sm_parent_class"`
	Erratum       string            `json:"erratum,omitempty"`
	Procs         []Proc            `json:"procs"`
}

func TestGoldenTables(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "procs", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden tables in testdata/procs (%v)", err)
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var g GoldenFile
		if err := json.Unmarshal(data, &g); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, tab := range g.Tables {
			t.Run(filepath.Base(name)+" on "+tab.OS+" classifies as the rules say", func(t *testing.T) {
				chain := NewTable(tab.OS, tab.Procs).Chain(tab.HarnessPID)
				if len(chain) == 0 {
					t.Fatalf("harness pid %d isn't in the table", tab.HarnessPID)
				}
				h, _ := HarnessOf(tab.OS, chain[0])
				l := Classify(tab.OS, chain)
				by := ""
				if l.By >= 0 {
					by = chain[l.By].Name
				}
				w := tab.Want
				if h != w.Harness || l.ParentClass != w.ParentClass || l.Entrypoint != w.Entrypoint ||
					l.HasTTY != w.HasTTY || by != w.DecidedBy || l.ScriptShell != w.ScriptShell {
					t.Errorf("got harness %q, %s, entrypoint %s, has_tty %v, decided by %q, script shell %v; want %q, %s, %s, %v, %q, %v",
						h, l.ParentClass, l.Entrypoint, l.HasTTY, by, l.ScriptShell,
						w.Harness, w.ParentClass, w.Entrypoint, w.HasTTY, w.DecidedBy, w.ScriptShell)
				}
			})
			t.Run(filepath.Base(name)+" on "+tab.OS+" differs from SM only with an erratum", func(t *testing.T) {
				if tab.SMParentClass == "" {
					t.Fatal("sm_parent_class is missing")
				}
				switch {
				case tab.Want.ParentClass == tab.SMParentClass && tab.Erratum != "":
					t.Errorf("the table agrees with SM but names erratum %s", tab.Erratum)
				case tab.Want.ParentClass != tab.SMParentClass && tab.Erratum == "":
					t.Errorf("want %s differs from SM's %s and no erratum says why", tab.Want.ParentClass, tab.SMParentClass)
				case tab.Erratum != "":
					if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(tab.Erratum))); err != nil {
						t.Errorf("erratum %s: %v", tab.Erratum, err)
					}
				}
			})
		}
	}
}
