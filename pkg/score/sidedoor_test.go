// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The side-door cases in testdata/timelines/sidedoor are WP-FX4's oracle for
// DESIGN 5.2, 5.4, 6.2 D2, D3 and D19, and 6.4. Each has an SM fixture's
// files, so its anchors and turns are checked as the SM ones are, and a
// sidedoor.json saying, record by record, what opens an anchor at what level
// and what becomes of each `pairtime track` call.

// sideDoorFile is the part of sidedoor.json WP-SC1 checks.
type sideDoorFile struct {
	Case    string `json:"case"`
	Records []struct {
		EventID string        `json:"event_id"`
		Local   string        `json:"local"`
		Kind    event.Kind    `json:"kind"`
		Anchor  bool          `json:"anchor"`
		Level   string        `json:"level"`
		Labels  []event.Label `json:"labels"`
		Track   string        `json:"track"`
		Flags   []string      `json:"flags"`
		Why     string        `json:"why"`
	} `json:"records"`
}

// sideDoorCases loads every case under testdata/timelines/sidedoor.
func sideDoorCases(t *testing.T) []*timelines.Fixture {
	t.Helper()
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "sidedoor"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("testdata/timelines/sidedoor doesn't exist yet (WP-FX4)")
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []*timelines.Fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "sidedoor", e.Name(), timelines.EventsFile)); err != nil {
			continue
		}
		f, err := timelines.Load(root, "sidedoor/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		t.Fatal("no side-door cases")
	}
	return out
}

func readSideDoor(t *testing.T, f *timelines.Fixture) sideDoorFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.Dir, "sidedoor.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s sideDoorFile
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("%s/sidedoor.json: %v", f.Name, err)
	}
	return s
}

var trackOutcomes = map[string]TrackOutcome{
	"anchor":         TrackAnchor,
	"matched":        TrackMatched,
	"without_prompt": TrackWithoutPrompt,
}

func TestSideDoorCases(t *testing.T) {
	for _, f := range sideDoorCases(t) {
		dev := fixtureDevice(f)
		got := scoreAll(t, dev, f.Events)
		checkAnchors(t, f, got)
		checkTurns(t, f, got)
		sd := readSideDoor(t, f)
		for _, rec := range sd.Records {
			name := f.Name + " " + rec.Local + " " + string(rec.Kind) + ": opens what the side-door rules say, at their level and with their labels"
			t.Run(name, func(t *testing.T) {
				a, ok := anchorByID(got, rec.EventID)
				if ok != rec.Anchor {
					t.Fatalf("opens an anchor: %v, want %v (%s)", ok, rec.Anchor, rec.Why)
				}
				if ok && (string(a.Level) != rec.Level || !slices.Equal(a.Labels, rec.Labels)) {
					t.Errorf("level %s labels %q, want %s and %q (%s)", a.Level, a.Labels, rec.Level, rec.Labels, rec.Why)
				}
				var flags []string
				for _, fl := range got.Flags {
					if fl.EventID == rec.EventID {
						flags = append(flags, fl.Name)
					}
				}
				if !slices.Equal(flags, rec.Flags) {
					t.Errorf("flags %q, want %q", flags, rec.Flags)
				}
				if rec.Kind != event.KindCLITrack {
					return
				}
				c, ok := trackOf(got, rec.EventID)
				if want, known := trackOutcomes[rec.Track]; !ok || !known || c.Outcome != want {
					t.Errorf("track call %+v, want outcome %q (%s)", c, rec.Track, rec.Why)
				}
			})
		}
		t.Run(f.Name+": a Step per minute gives what one Step gives", func(t *testing.T) {
			if !bytes.Equal(marshal(t, scoreByMinute(t, dev, f.Events)), marshal(t, got)) {
				t.Error("the Finals of a Step per minute and the last Pending differ from one Step's output")
			}
		})
	}
}

// TestSideDoorLevels is the WP-SC1 check on WP-FX4's expected levels: cases
// (a), (c), (d) and (e) open only level-C anchors and (b) only level-A
// ones; (f) is track_matched and opens nothing; (g) opens nothing and raises
// track_without_prompt.
func TestSideDoorLevels(t *testing.T) {
	byCase := map[string]Derived{}
	tracks := map[string][]TrackCall{}
	for _, f := range sideDoorCases(t) {
		sd := readSideDoor(t, f)
		d := scoreAll(t, fixtureDevice(f), f.Events)
		byCase[sd.Case] = d
		tracks[sd.Case] = d.Tracks
	}
	for _, c := range []struct {
		name  string
		cases []string
		level event.Evidence
	}{
		{"track, plugin, editor and bridge side doors without key presses open only level-C anchors", []string{"a", "c", "d", "e"}, event.EvidenceC},
		{"track calls with key presses in the host window open only level-A anchors", []string{"b"}, event.EvidenceA},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range c.cases {
				d, ok := byCase[k]
				if !ok {
					t.Fatalf("no case %s", k)
				}
				if len(d.Anchors) == 0 {
					t.Errorf("case %s opens no anchor", k)
				}
				for _, a := range d.Anchors {
					if a.Level != c.level {
						t.Errorf("case %s: anchor %s is level %s", k, a.ID, a.Level)
					}
				}
			}
		})
	}
	for _, c := range []struct {
		name    string
		key     string
		outcome TrackOutcome
		flag    bool
	}{
		{"case f's call is track_matched and opens nothing", "f", TrackMatched, false},
		{"case g's call opens nothing and raises track_without_prompt", "g", TrackWithoutPrompt, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := byCase[c.key]
			if len(tracks[c.key]) != 1 || tracks[c.key][0].Outcome != c.outcome {
				t.Fatalf("track calls %+v, want one %s", tracks[c.key], c.outcome)
			}
			call := tracks[c.key][0]
			if _, ok := anchorByID(d, call.ID); ok {
				t.Error("the call opened an anchor")
			}
			if flagged(d, FlagTrackWithoutPrompt, call.ID) != c.flag {
				t.Errorf("flags %+v; track_without_prompt should be %v", d.Flags, c.flag)
			}
		})
	}
}
