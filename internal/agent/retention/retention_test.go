// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/docs"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/agent/vault"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

func TestDevice(t *testing.T) {
	p, err := Device()
	if err != nil {
		t.Fatal(err)
	}
	// These are DESIGN 4.4's device rows, which TestDesignPeriods holds
	// docs/retention.json to.
	cases := []struct {
		name      string
		got, want int
	}{
		{"prompt text is kept 90 days", p.Text, 90},
		{"file paths are kept 30 days", p.Paths, 30},
		{"ledger records and batch roots are kept 180 days", p.Ledger, 180},
		{"minute buckets are kept 180 days", p.Buckets, 180},
		{"held hook calls are kept 7 days at most", p.Held, 7},
		{"commit records are kept 180 days", p.Commits, 180},
		{"records of past uploads are kept 90 days", p.Uploads, 90},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %d days, want %d", c.got, c.want)
			}
		})
	}
	t.Run("presence, focus and sketches kept apart from the ledger's records is refused", func(t *testing.T) {
		data := bytes.Replace(docs.RetentionJSON, []byte(`"kept": "With the ledger records that hold them: {days}, or longer while a submission that uses them is inside its spot-check window ({window})",
      "days": 180`), []byte(`"kept": "{days}",
      "days": 90`), 1)
		if bytes.Equal(data, docs.RetentionJSON) {
			t.Fatal("docs/retention.json's device.activity entry isn't as this test expects")
		}
		if _, err := parse(data); err == nil {
			t.Error("parse took device.activity at 90 days and the ledger at 180")
		}
	})
}

func TestFrom(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		now  time.Time
		days int
		want string
	}{
		{"180 days back from midday", now, 180, "2026-05-27"},
		{"90 days back from midday", now, 90, "2026-08-25"},
		{"a day back is yesterday", now, 1, "2026-11-22"},
		{"the day counts in UTC, whatever the clock's zone", now.In(time.FixedZone("UTC+14", 14*3600)), 1, "2026-11-22"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := From(c.now, c.days); got != c.want {
				t.Errorf("From = %s, want %s", got, c.want)
			}
		})
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		days int
		want string
	}{
		{"with 1 day, yesterday's file goes at midnight UTC", time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), 1, "2026-11-23"},
		{"with 1 day, today's file stays until midnight UTC", time.Date(2026, 11, 22, 23, 59, 59, 0, time.UTC), 1, "2026-11-22"},
		{"90 days back from midday keeps the 90 days up to today", time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), 90, "2026-08-26"},
		{"a day's file goes as its first minute turns 90 days old", time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC), 90, "2026-08-27"},
		{"a day's file stays while its first minute is younger than 90 days", time.Date(2026, 11, 23, 23, 59, 59, 0, time.UTC), 90, "2026-08-26"},
		{"the day counts in UTC, whatever the clock's zone", time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC).In(time.FixedZone("UTC-8", -8*3600)), 1, "2026-11-23"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Within(c.now, c.days); got != c.want {
				t.Errorf("Within = %s, want %s", got, c.want)
			}
		})
	}
	// What the edge means for a text: kept no longer than its days, however
	// early or late in its day it was written, and not deleted as it's kept.
	t.Run("a text is kept no longer than the days the teen set, at the first look after its day turns that old", func(t *testing.T) {
		for _, days := range []int{1, 7, 30, 90} {
			for _, written := range []time.Time{time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 23, 59, 0, 0, time.UTC)} {
				day := written.Format(time.DateOnly)
				for _, after := range []time.Duration{time.Duration(days) * 24 * time.Hour, time.Duration(days)*24*time.Hour + time.Minute} {
					at := written.Add(after)
					if kept := day >= Within(at, days); kept {
						t.Errorf("with %d days, a text written at %s is still kept %v later, at %s", days, written.Format(time.RFC3339), after, at.Format(time.RFC3339))
					}
				}
				if day < Within(written, days) {
					t.Errorf("with %d days, a text written at %s goes at once", days, written.Format(time.RFC3339))
				}
			}
		}
	})
}

func TestShorten(t *testing.T) {
	p := Periods{Text: 90, Paths: 30, Ledger: 180}
	cases := []struct {
		name               string
		text, paths        int
		wantText, wantPath int
	}{
		{"0 keeps DESIGN's periods", 0, 0, 90, 30},
		{"the teen's shorter periods are kept", 7, 3, 7, 3},
		{"a longer period than DESIGN's is DESIGN's", 365, 60, 90, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := p.Shorten(c.text, c.paths)
			if got.Text != c.wantText || got.Paths != c.wantPath || got.Ledger != 180 {
				t.Errorf("Shorten(%d, %d) = %+v", c.text, c.paths, got)
			}
		})
	}
}

// The device key the sweep's fixture writes under, and its id.
var (
	testKDev  = bytes.Repeat([]byte{7}, pkgledger.KeySize)
	testKeyID = "0123456789abcdef0123456789abcdef"
)

// fixture is a data directory whose ledger, vault and minute buckets hold
// a day of records each for days UTC days up to now: a presence sample, a
// prompt and an edit, sealed in one batch, and a bucket file.
type fixture struct {
	dir   string
	keys  *pkgledger.Keys
	clock time.Time
	w     *ledger.Writer
	v     *vault.Vault
	b     *store.Buckets
	// per day: the prompt's number and text, the edit's number and path,
	// and the day's first batch
	prompt, edit, batch map[string]uint64
	text, path          map[string]string
	// others are files no sweep of this package touches, by path.
	others map[string][]byte
}

func newFixture(t *testing.T, now time.Time, days int) *fixture {
	t.Helper()
	k, err := pkgledger.DeriveKeys(testKDev)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: t.TempDir(), keys: k, prompt: map[string]uint64{}, edit: map[string]uint64{}, batch: map[string]uint64{},
		text: map[string]string{}, path: map[string]string{}, others: map[string][]byte{}}
	f.open(t)
	mono := int64(1)
	envOf := func(at time.Time, session, project string, src event.Source, via event.Via, h event.Harness, hv string) event.Envelope {
		mono++
		return event.Envelope{EventID: event.NewEventID(at), T: at.UnixMilli(), Mono: mono * 1000, BootID: "b_test",
			Source: src, Via: via, Harness: h, HarnessVersion: hv, SessionKey: session, ProjectKey: project}
	}
	for back := days - 1; back >= 0; back-- {
		at := now.AddDate(0, 0, -back)
		f.clock = at
		day := at.Format(time.DateOnly)
		hook := func() event.Envelope {
			return envOf(at, "s_1", "p_web", event.SourceHook, event.ViaHook, event.HarnessClaudeCode, "2.1.327")
		}
		// A prompt seals at once on the writer's own goroutine, so the day's
		// records can take more than one batch: the day's first batch is
		// the next one sealed.
		f.batch[day] = f.w.Status().Batches + 1
		if _, err := f.w.Write(ledger.Input{Event: event.Event{Envelope: envOf(at, event.NoSession, "", event.SourceOS, event.ViaOS, event.HarnessNone, ""),
			Data: &event.OSPresence{IdleAnyS: 1}}}); err != nil {
			t.Fatal(err)
		}
		text := "fix the nav bar on " + day
		s, err := f.w.Write(ledger.Input{Event: event.Event{Envelope: hook(), Data: &event.PromptSubmit{TurnKey: "t_1",
			LenChars: len(text), TypedLenEst: len(text), OriginDeclared: event.DeclaredNone}}, Text: []byte(text)})
		if err != nil {
			t.Fatal(err)
		}
		f.prompt[day], f.text[day] = s, text
		path := "src/" + day + ".go"
		s, err = f.w.Write(ledger.Input{Event: event.Event{Envelope: hook(), Data: &event.AgentTool{ToolClass: event.ToolEdit,
			LinesAdded: 2, LinesRemoved: 1, TStart: at.UnixMilli(), TEnd: at.UnixMilli()}},
			Edits: []features.Edit{{Path: path, OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2}}})
		if err != nil {
			t.Fatal(err)
		}
		f.edit[day], f.path[day] = s, path
		if _, _, err := f.w.Seal(); err != nil {
			t.Fatalf("sealing %s: %v", day, err)
		}
		f.put(t, filepath.Join("buckets", day+".jsonl"), "{}\n")
	}
	// What the sweeps of this package leave alone: the day rollups and
	// coach records, which the teen deletes, and the stores that sweep
	// themselves, with files far older than any period.
	old := now.AddDate(0, 0, -400).Format(time.DateOnly)
	for name, data := range map[string]string{
		filepath.Join("days", old+".json"):        `{"day":"` + old + `"}`,
		filepath.Join("coach", "recaps.jsonl"):    "{}\n",
		filepath.Join("commits", old+".jsonl"):    "{}\n",
		"uploads.jsonl":                           "{}\n",
		"held.jsonl":                              "{}\n",
		"state.json":                              "{}",
		"config.toml":                             "mode = \"local\"\n",
		filepath.Join("buckets", "notes.txt"):     "not a day",
		filepath.Join("ledger", "old", "x", "y"):  "not a key's chain",
		filepath.Join("vault", "old", "notes.md"): "not a key's files",
	} {
		f.put(t, name, data)
		f.others[name] = []byte(data)
	}
	return f
}

// open opens the vault, the ledger writer on the fixture's clock and the
// bucket store, and closes them when the test ends.
func (f *fixture) open(t *testing.T) {
	t.Helper()
	v, err := vault.Open(vault.Options{Dir: filepath.Join(f.dir, "vault"), Keys: f.keys, KeyID: testKeyID, KeepText: true})
	if err != nil {
		t.Fatal(err)
	}
	w, err := ledger.Open(ledger.Options{Dir: filepath.Join(f.dir, "ledger"), Keys: f.keys, KeyID: testKeyID, Vault: v,
		Now: func() time.Time { return f.clock }})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.OpenBuckets(filepath.Join(f.dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	f.w, f.v, f.b = w, v, b
	t.Cleanup(func() { f.close() })
}

func (f *fixture) close() {
	f.w.Close()
	f.v.Close()
	f.b.Close()
}

func (f *fixture) put(t *testing.T, name, data string) {
	t.Helper()
	p := filepath.Join(f.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) stores() Stores { return Stores{Vault: f.v, Ledger: f.w, Buckets: f.b} }

// days lists the UTC days of the files named YYYY-MM-DD<suffix> in dir.
func days(t *testing.T, dir, suffix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if day, ok := strings.CutSuffix(e.Name(), suffix); ok && len(day) == len(time.DateOnly) && e.Type().IsRegular() {
			out = append(out, day)
		}
	}
	slices.Sort(out)
	return out
}

// TestSweep runs the sweeps under a fake clock over 200 days of records and
// checks, row by row of DESIGN 4.4, that they delete exactly what is past
// its period and nothing else (docs/PLAN.md, WP-LG6).
func TestSweep(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	device, err := Device()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		periods Periods
	}{
		{"with DESIGN 4.4's periods", device},
		{"with the teen's shorter periods for text and paths", device.Shorten(7, 3)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, now, 200)
			all := slices.Sorted(maps.Keys(f.prompt))
			got, failed := Sweep(now, c.periods, f.stores())
			if failed != nil {
				t.Fatal(failed)
			}
			textFrom, pathsFrom := Within(now, c.periods.Text), Within(now, c.periods.Paths)
			ledgerFrom, bucketsFrom := From(now, c.periods.Ledger), From(now, c.periods.Buckets)
			t.Run("the vault keeps the texts of the days of the text's period, and none older", func(t *testing.T) {
				for _, day := range all {
					s := f.prompt[day]
					cm, err := f.keys.Commit(s, []byte(f.text[day]))
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.v.Text(s, cm)
					if kept := err == nil; kept != (day >= textFrom) {
						t.Errorf("%s: the text is kept %v (%v), want %v", day, kept, err, day >= textFrom)
					}
				}
			})
			t.Run("the vault keeps the paths of the days of the paths' period, and none older", func(t *testing.T) {
				fk := f.keys.Features()
				for _, day := range all {
					key, err := fk.Path(f.path[day])
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.v.Paths(f.edit[day], []features.Hunk{{P: key}})
					if kept := err == nil; kept != (day >= pathsFrom) {
						t.Errorf("%s: the paths are kept %v (%v), want %v", day, kept, err, day >= pathsFrom)
					}
				}
			})
			t.Run("the ledger keeps the batches of the ledger's period, from a base, and the segments they are in", func(t *testing.T) {
				info, rows, err := ledger.ReadRows(filepath.Join(f.dir, "ledger"), 0)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) == 0 {
					t.Fatal("the ledger keeps no row")
				}
				if info.Base != f.batch[ledgerFrom] || rows[0].B != info.Base {
					t.Errorf("the ledger keeps rows from batch %d (base %d), want %d", rows[0].B, info.Base, f.batch[ledgerFrom])
				}
				var want []string
				for _, d := range all {
					if d >= ledgerFrom {
						want = append(want, d)
					}
				}
				if segs := days(t, filepath.Join(f.dir, "ledger"), ".seg"); !slices.Equal(segs, want) {
					t.Errorf("segments %v, want the %d from %s", segs, len(want), ledgerFrom)
				}
				if got.Batches != int(f.batch[ledgerFrom]-1) {
					t.Errorf("Sweep says %d batches went, want %d", got.Batches, f.batch[ledgerFrom]-1)
				}
			})
			t.Run("the chain the ledger keeps verifies from its base, the head before it taken as given", func(t *testing.T) {
				info, rows, err := ledger.ReadRows(filepath.Join(f.dir, "ledger"), 0)
				if err != nil {
					t.Fatal(err)
				}
				chain := pkgledger.Chain{DeviceID: info.DeviceID, Prev: rows[0].Prev}
				err = ledger.ReadBatches(filepath.Join(f.dir, "ledger"), 0, func(b ledger.Batch) error {
					chain.Batches = append(chain.Batches, pkgledger.BatchOf(b.Row, b.Records))
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(chain.Batches) != len(rows) {
					t.Errorf("ReadBatches gives %d batches, ReadRows %d rows", len(chain.Batches), len(rows))
				}
				if err := chain.Verify(pkgledger.Options{Keys: f.keys}); err != nil {
					t.Errorf("pkg/ledger refuses the kept chain: %v", err)
				}
			})
			t.Run("the minute buckets of the buckets' period stay, and older ones go", func(t *testing.T) {
				var want []string
				for _, d := range all {
					if d >= bucketsFrom {
						want = append(want, d)
					}
				}
				if got := days(t, filepath.Join(f.dir, "buckets"), ".jsonl"); !slices.Equal(got, want) {
					t.Errorf("bucket days %v, want the %d from %s", got, len(want), bucketsFrom)
				}
			})
			t.Run("nothing else in the data directory changes", func(t *testing.T) {
				for name, want := range f.others {
					if got, err := os.ReadFile(filepath.Join(f.dir, name)); err != nil || !bytes.Equal(got, want) {
						t.Errorf("%s: %q, %v", name, got, err)
					}
				}
			})
			t.Run("a second sweep the same day deletes nothing more", func(t *testing.T) {
				again, failed := Sweep(now, c.periods, f.stores())
				if failed != nil || again != (Swept{}) {
					t.Errorf("the second sweep: %+v, %v", again, failed)
				}
			})
			t.Run("the ledger goes on after the sweep, and opens again from its base", func(t *testing.T) {
				f.clock = now.Add(time.Hour)
				text := "one more prompt"
				s, err := f.w.Write(ledger.Input{Event: event.Event{Envelope: event.Envelope{EventID: event.NewEventID(f.clock),
					T: f.clock.UnixMilli(), Mono: 1 << 40, BootID: "b_test", Source: event.SourceHook, Via: event.ViaHook,
					Harness: event.HarnessClaudeCode, HarnessVersion: "2.1.327", SessionKey: "s_1", ProjectKey: "p_web"},
					Data: &event.PromptSubmit{TurnKey: "t_2", LenChars: len(text), TypedLenEst: len(text), OriginDeclared: event.DeclaredNone}},
					Text: []byte(text)})
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := f.w.Seal(); err != nil {
					t.Fatalf("sealing after the sweep: %v", err)
				}
				f.close()
				f.open(t)
				if next := f.w.Status().Next; next != s+1 {
					t.Errorf("the reopened writer's next record is %d, want %d", next, s+1)
				}
				cm, err := f.keys.Commit(s, []byte(text))
				if err != nil {
					t.Fatal(err)
				}
				if got, err := f.v.Text(s, cm); err != nil || string(got) != text {
					t.Errorf("the vault after the sweep: %q, %v", got, err)
				}
			})
		})
	}
}

// TestSweepFloor checks that the ledger keeps the rows from the floor
// Stores gives, as for the uploads of a linked computer, and deletes the
// segments by its period all the same.
func TestSweepFloor(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	device, err := Device()
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, now, 200)
	floorDay := now.AddDate(0, 0, -190).Format(time.DateOnly)
	s := f.stores()
	s.Floor = f.batch[floorDay]
	if _, failed := Sweep(now, device, s); failed != nil {
		t.Fatal(failed)
	}
	ledgerFrom := From(now, device.Ledger)
	cases := []struct {
		name string
		ok   func() (bool, string)
	}{
		{"the rows from the floor's batch stay, older than the ledger's period", func() (bool, string) {
			info, rows, err := ledger.ReadRows(filepath.Join(f.dir, "ledger"), 0)
			return err == nil && info.Base == s.Floor && len(rows) > 0 && rows[0].B == s.Floor, fmt.Sprintf("base %d, %v", info.Base, err)
		}},
		{"the segments go by the ledger's period, the floor's batches' too", func() (bool, string) {
			segs := days(t, filepath.Join(f.dir, "ledger"), ".seg")
			return len(segs) > 0 && segs[0] == ledgerFrom, fmt.Sprintf("segments from %v, want from %s", segs[:min(1, len(segs))], ledgerFrom)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if ok, why := c.ok(); !ok {
				t.Error(why)
			}
		})
	}
}

// failing is a store whose sweep fails, and records that it ran.
type failing struct{ ran bool }

func (f *failing) Sweep(string, string) (int, error) {
	f.ran = true
	return 0, errors.New("the disk is full")
}
func (f *failing) Drop(string) (int, error) { f.ran = true; return 0, errors.New("access denied") }

// TestSweepFailures checks that a store whose sweep fails is named with its
// error, and that the others are swept all the same.
func TestSweepFailures(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	device, err := Device()
	if err != nil {
		t.Fatal(err)
	}
	vaultStore, bucketStore := &failing{}, &failing{}
	var pruned time.Time
	_, failed := Sweep(now, device, Stores{Vault: vaultStore, Buckets: bucketStore, Uploads: func(at time.Time) error { pruned = at; return nil }})
	cases := []struct {
		name string
		ok   bool
	}{
		{"the vault's failure is named with its error", failed[StoreVault] != nil && strings.Contains(failed[StoreVault].Error(), "the disk is full")},
		{"the bucket store's failure is named with its error", failed[StoreBuckets] != nil && strings.Contains(failed[StoreBuckets].Error(), "access denied")},
		{"a store that didn't fail isn't named", len(failed) == 2 && failed[StoreUploads] == nil},
		{"every store is swept, after a failure too", vaultStore.ran && bucketStore.ran && pruned.Equal(now)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("it isn't: %v", failed)
			}
		})
	}
}
