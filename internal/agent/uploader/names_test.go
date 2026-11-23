// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/state"
)

// Made-up project keys in the git adapter's form.
const (
	nameKey  = "g7c41e9a2d05b8f63e1a94c7d2b6f0e85"
	otherKey = "g5be03d7f18a6c92e4b07d5a1c8f3e962"
)

func TestNamesDiff(t *testing.T) {
	tests := []struct {
		name              string
		wanted, confirmed map[string]string
		acks              []string // the keys the server lists as hidden, whose names config.toml has off
		set               []nameItem
		del               []string
	}{
		{
			name:   "a name turned on that the server doesn't show is set",
			wanted: map[string]string{nameKey: "chip8-emulator"},
			set:    []nameItem{{Project: nameKey, Label: "chip8-emulator"}},
		},
		{
			name:      "a label changed in config.toml is set with the new label",
			wanted:    map[string]string{nameKey: "Chip-8 emulator"},
			confirmed: map[string]string{nameKey: "chip8-emulator"},
			set:       []nameItem{{Project: nameKey, Label: "Chip-8 emulator"}},
		},
		{
			name:      "a name the server shows that config.toml has off is deleted",
			confirmed: map[string]string{nameKey: "chip8-emulator"},
			del:       []string{nameKey},
		},
		{
			name:      "names the server shows as config.toml has them need nothing",
			wanted:    map[string]string{nameKey: "chip8-emulator"},
			confirmed: map[string]string{nameKey: "chip8-emulator"},
		},
		{
			name:   "a label that isn't clean and a key that isn't one never go",
			wanted: map[string]string{nameKey: " chip8", "chip 8": "chip8-emulator"},
		},
		{
			name:      "sets and deletes each come in key order",
			wanted:    map[string]string{nameKey: "b", otherKey: "a"},
			confirmed: map[string]string{"p_two": "x", "p_one": "y"},
			set:       []nameItem{{Project: otherKey, Label: "a"}, {Project: nameKey, Label: "b"}},
			del:       []string{"p_one", "p_two"},
		},
		{
			name: "a key the server lists as hidden goes as a delete, which tells the server the device turned the name off too",
			acks: []string{nameKey},
			del:  []string{nameKey},
		},
		{
			name:   "a hidden name the owner turned on again goes as a set, never as a delete too",
			wanted: map[string]string{nameKey: "chip8-emulator"},
			acks:   []string{nameKey},
			set:    []nameItem{{Project: nameKey, Label: "chip8-emulator"}},
		},
		{
			name:      "the deletes of shown and hidden names come together in key order, each once",
			confirmed: map[string]string{"p_two": "x"},
			acks:      []string{"p_two", "p_one"},
			del:       []string{"p_one", "p_two"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, del := namesDiff(tt.wanted, tt.confirmed, tt.acks)
			if !reflect.DeepEqual(set, tt.set) || !reflect.DeepEqual(del, tt.del) {
				t.Errorf("namesDiff = %v, %v; want %v, %v", set, del, tt.set, tt.del)
			}
		})
	}
}

func TestTakeNames(t *testing.T) {
	many := func(n int) ([]nameItem, []string) {
		var set []nameItem
		var del []string
		for i := range n {
			set = append(set, nameItem{Project: fmt.Sprintf("p_set%03d", i), Label: "x"})
			del = append(del, fmt.Sprintf("p_del%03d", i))
		}
		return set, del
	}
	tests := []struct {
		name          string
		sets, deletes int
		wantSet       int
		wantDel       int
	}{
		{name: "a request carries every change while they are 200 or fewer", sets: 100, deletes: 100, wantSet: 100, wantDel: 100},
		{name: "past 200 the deletes go first, and the names to show fill the rest", sets: 150, deletes: 150, wantSet: 50, wantDel: 150},
		{name: "past 200 deletes, a request carries 200 of them and nothing else", sets: 10, deletes: 250, wantSet: 0, wantDel: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, _ := many(tt.sets)
			_, del := many(tt.deletes)
			b := takeNames(set, del)
			if len(b.Set) != tt.wantSet || len(b.Delete) != tt.wantDel {
				t.Errorf("takeNames gave %d sets and %d deletes, want %d and %d", len(b.Set), len(b.Delete), tt.wantSet, tt.wantDel)
			}
		})
	}
}

func TestReadDue(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "an hour after the last answer no read is due", at: now.Add(-time.Hour), want: false},
		{name: "a day after the last answer a read is due", at: now.Add(-24 * time.Hour), want: true},
		{name: "an answer the clock is now before, as after it went back, makes a read due", at: now.Add(time.Hour), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readDue(tt.at.UnixMilli(), now); got != tt.want {
				t.Errorf("readDue = %v, want %v", got, tt.want)
			}
		})
	}
}

// projectTable is a config.toml table for key's name.
func projectTable(key, label string, online bool) string {
	return fmt.Sprintf("[projects.%s]\nname = %q\nonline = %v\n", key, label, online)
}

// namesUploader makes an uploader over the rig's files that sends the
// names the config.toml at cfgPath turns on, and turns off there the ones
// hidden on the dashboard, as the agent's does, signed in when login says
// so.
func (r *rig) namesUploader(t *testing.T, cfgPath string, login func() (Credentials, bool)) *Uploader {
	t.Helper()
	u, err := New(Options{
		Ledger: r.w, LedgerDir: r.dirs.Ledger(), BucketDir: r.dirs.Buckets(), DataDir: r.dirs.Data, State: r.st,
		Login:  LoginFunc(login),
		Params: newestTable(), Client: r.client, Draw: func(int64) int64 { return 0 },
		Names: func() (map[string]string, bool) {
			cfg, _, err := config.Load(cfgPath)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, false
			}
			return cfg.NamesOn(), true
		},
		HideName: func(key string) error { return config.HideName(cfgPath, key) },
		Problem: func(msg string) {
			r.pmu.Lock()
			r.problem = msg
			r.pmu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	return u
}

// increasing reports whether every counter is above the one before it.
func increasing(counters []uint64) bool {
	for i := 1; i < len(counters); i++ {
		if counters[i] <= counters[i-1] {
			return false
		}
	}
	return true
}

func TestNames(t *testing.T) {
	type rigCheck func(t *testing.T, r *rig, cfgPath string)
	allFaults := func(fault string) map[int]string {
		m := map[int]string{}
		for i := 1; i <= 200; i++ {
			m[i] = fault
		}
		return m
	}
	bodies := func(want ...string) rigCheck {
		return func(t *testing.T, r *rig, _ string) {
			var got []string
			for _, b := range r.f.namesBodies {
				got = append(got, string(b))
			}
			if !slices.Equal(got, want) {
				t.Errorf("the names requests carried %q, want %q", got, want)
			}
		}
	}
	// quiet checks that the names requests told doctor nothing and kept
	// nothing, and waited on the names' own backoff, not the uploads'.
	quiet := func(t *testing.T, r *rig, _ string) {
		if p := r.doctor(); p != "" {
			t.Errorf("doctor heard %q, want nothing", p)
		}
		if r.u.namesBackoff.failing() == 0 || r.u.backoff.failing() != 0 {
			t.Errorf("the names' backoff counts %d failures and the uploads' %d; want the names' alone", r.u.namesBackoff.failing(), r.u.backoff.failing())
		}
		n, err := ReadNames(r.dirs.Data)
		if err != nil || n.Server != r.creds.Server || len(n.Confirmed) != 0 || len(n.Codes) != 0 {
			t.Errorf("project-names.json = %+v, %v; want this sign-in's, with nothing of the server's, since no answer was pairtimed's names", n, err)
		}
	}
	// missing checks that a 404, a server without the names endpoint, told
	// doctor nothing, kept nothing, and stops the names for a day, off the
	// backoffs, after posts requests.
	missing := func(posts int) rigCheck {
		return func(t *testing.T, r *rig, _ string) {
			if p := r.doctor(); p != "" {
				t.Errorf("doctor heard %q, want nothing", p)
			}
			if r.f.namesPosts != posts || r.u.namesBackoff.failing() != 0 || r.u.backoff.failing() != 0 {
				t.Errorf("%d names requests, the names' backoff counts %d failures and the uploads' %d; want %d requests and no failure", r.f.namesPosts, r.u.namesBackoff.failing(), r.u.backoff.failing(), posts)
			}
			if r.u.missingHash == "" || r.u.missingUntil.Before(time.Now().Add(23*time.Hour)) {
				t.Errorf("the 404 counts until %v, want a day from now", r.u.missingUntil)
			}
			n, err := ReadNames(r.dirs.Data)
			if err != nil || n.Server != r.creds.Server || len(n.Confirmed) != 0 || len(n.Codes) != 0 {
				t.Errorf("project-names.json = %+v, %v; want this sign-in's, with nothing of the server's, since no answer was pairtimed's names", n, err)
			}
		}
	}
	// hiddenStays checks that nameKey's name, which the owner hid on the
	// dashboard, ends hidden there and off here, with a note of the hide:
	// the server never shows it again, and the request before the hide's
	// delete was a read.
	hiddenStays := func(t *testing.T, r *rig, _ string) {
		if _, shown := r.f.names[nameKey]; shown {
			t.Errorf("the server shows the hidden name again: %v", r.f.names)
		}
		if got := r.f.namesBodies; len(got) < 2 || string(got[len(got)-2]) != `{}` {
			t.Errorf("the names requests carried %q, want a read before the last", got)
		}
		n, err := ReadNames(r.dirs.Data)
		if h, ok := n.Hidden[nameKey]; err != nil || !ok || h.Reason != "" || h.Pending || len(n.Sent) != 0 {
			t.Errorf("project-names.json = %+v, %v; want a note that the owner hid the name, and nothing left unsettled", n, err)
		}
	}
	// stillOn checks that nameKey's name is on in config.toml, with no note
	// and nothing left unsettled.
	stillOn := func(t *testing.T, r *rig, cfgPath string) {
		cfg, _, err := config.Load(cfgPath)
		if err != nil || !cfg.Projects[nameKey].Online {
			t.Errorf("config.toml has %+v, %v; want the name still on", cfg.Projects[nameKey], err)
		}
		n, err := ReadNames(r.dirs.Data)
		if _, noted := n.Hidden[nameKey]; err != nil || noted || len(n.Sent) != 0 {
			t.Errorf("project-names.json = %+v, %v; want no note, and nothing left unsettled", n, err)
		}
	}
	setChip8 := `{"set":[{"project":"` + nameKey + `","label":"chip8-emulator"}]}`
	// codes checks project-names.json's codes, and that its names are the
	// rig's server's.
	codes := func(want map[string]string) rigCheck {
		return func(t *testing.T, r *rig, _ string) {
			n, err := ReadNames(r.dirs.Data)
			if err != nil || !maps.Equal(n.Codes, want) || n.Server != r.creds.Server || n.AnsweredAt == 0 {
				t.Errorf("project-names.json = %+v, %v; want the codes %v from the rig's server", n, err, want)
			}
		}
	}
	tests := []struct {
		name    string
		config  string                // config.toml
		held    map[string]string     // the names the server holds at the start
		codes   map[string]string     // the codes the server gives the device's keys
		hidden  []string              // the keys the server lists as hidden on the dashboard at the start
		known   map[string]string     // project-names.json's names, as the server confirmed them
		notes   map[string]HiddenName // project-names.json's notes of names pairtime turned off
		server  string                // the server project-names.json is for, the rig's when ""
		faults  map[int]string        // the names requests' faults, by attempt
		then    map[int]string        // config.toml as the owner leaves it after an attempt, by its number
		hideAt  int                   // the attempt after which the owner presses Hide this name for nameKey
		dropAt  int                   // the attempt after which the server loses nameKey's name, with no hide
		rawFile string                // project-names.json as it is on disk at the start, when set
		own     []string              // the keys of the projects the computer has seen (Options.OwnKeys), every key when nil
		local   bool                  // the computer is local-only: no sign-in to upload with
		until   func(f *fakeIngest, cfgPath string) bool
		checks  []rigCheck
	}{
		{
			name:   "a name turned on in config.toml goes in a names request, and the server's answer is kept in project-names.json for its owner alone",
			config: projectTable(nameKey, "chip8-emulator", true),
			codes:  map[string]string{nameKey: "7c41e9"},
			until:  func(f *fakeIngest, _ string) bool { return f.names[nameKey] == "chip8-emulator" },
			checks: []rigCheck{
				bodies(`{}`, `{"set":[{"project":"`+nameKey+`","label":"chip8-emulator"}]}`),
				func(t *testing.T, r *rig, _ string) {
					raw, err := os.ReadFile(filepath.Join(r.dirs.Data, UploadsName))
					if err != nil {
						t.Fatal(err)
					}
					var lines []PastNames
					for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
						var e PastNames
						if json.Unmarshal([]byte(line), &e) == nil && e.Kind == KindNames {
							lines = append(lines, e)
						}
					}
					sum := sha256.Sum256(r.f.namesBodies[1])
					if len(lines) != 2 || lines[0].Set+lines[0].Delete != 0 || lines[1].Set != 1 || lines[1].Status != 200 || lines[1].Server != r.creds.Server || lines[1].BodySHA256 != hex.EncodeToString(sum[:]) {
						t.Errorf("uploads.jsonl's names lines are %+v, want the read and the set, with the set's body hash", lines)
					}
					if strings.Contains(string(raw), "chip8-emulator") || strings.Contains(string(raw), nameKey) {
						t.Errorf("uploads.jsonl holds the name or its key:\n%s", raw)
					}
				},
				func(t *testing.T, r *rig, _ string) {
					n, err := ReadNames(r.dirs.Data)
					if err != nil || !maps.Equal(n.Confirmed, map[string]string{nameKey: "chip8-emulator"}) || !maps.Equal(n.Codes, map[string]string{nameKey: "7c41e9"}) || n.AnsweredAt == 0 || n.Server != r.creds.Server || n.Device != r.creds.DeviceID {
						t.Errorf("project-names.json = %+v, %v", n, err)
					}
					fi, err := os.Stat(filepath.Join(r.dirs.Data, NamesFileName))
					if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
						t.Errorf("project-names.json is %v, %v; want it for its owner alone", fi.Mode(), err)
					}
				},
			},
		},
		{
			name:   "a name turned off goes as a delete, and the server drops it",
			config: projectTable(nameKey, "chip8-emulator", false),
			held:   map[string]string{nameKey: "chip8-emulator"},
			known:  map[string]string{nameKey: "chip8-emulator"},
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts > 0 && len(f.names) == 0 },
			checks: []rigCheck{bodies(`{}`, `{"delete":["`+nameKey+`"]}`)},
		},
		{
			name:   "a label changed in config.toml goes as a set with the new label",
			config: projectTable(nameKey, `Chip-8 "emulator" <v2>`, true),
			held:   map[string]string{nameKey: "chip8-emulator"},
			known:  map[string]string{nameKey: "chip8-emulator"},
			until:  func(f *fakeIngest, _ string) bool { return f.names[nameKey] != "chip8-emulator" },
			checks: []rigCheck{
				bodies(`{}`, `{"set":[{"project":"`+nameKey+`","label":"Chip-8 \"emulator\" <v2>"}]}`),
				func(t *testing.T, r *rig, _ string) {
					if got := r.f.names[nameKey]; got != `Chip-8 "emulator" <v2>` {
						t.Errorf("the server shows %q", got)
					}
				},
			},
		},
		{
			name:   "with nothing changed the start sends one read, {}, and then nothing more",
			config: projectTable(nameKey, "chip8-emulator", true),
			held:   map[string]string{nameKey: "chip8-emulator"},
			known:  map[string]string{nameKey: "chip8-emulator"},
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts == 1 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{bodies(`{}`)},
		},
		{
			name:   "a name the owner hid on the dashboard while the agent was off turns off in config.toml, with a note, isn't sent again, and a delete tells the server so",
			config: projectTable(nameKey, "chip8-emulator", true),
			hidden: []string{nameKey},
			known:  map[string]string{nameKey: "chip8-emulator"},
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts == 2 && len(f.namesHidden) == 0 && !cfg.Projects[nameKey].Online && f.head != nil && f.head.b == 3
			},
			checks: []rigCheck{
				bodies(`{}`, `{"delete":["`+nameKey+`"]}`),
				func(t *testing.T, r *rig, cfgPath string) {
					cfg, _, err := config.Load(cfgPath)
					if err != nil || cfg.Projects[nameKey] != (config.Project{Name: "chip8-emulator"}) {
						t.Errorf("config.toml has %+v, %v; want the name off with its label kept", cfg.Projects[nameKey], err)
					}
					n, err := ReadNames(r.dirs.Data)
					if h := n.Hidden[nameKey]; err != nil || h.Label != "chip8-emulator" || h.At == 0 || h.Pending || len(n.Confirmed) != 0 || len(n.Hides) != 0 {
						t.Errorf("project-names.json = %+v, %v; want a note of the hidden name, and nothing the server still lists", n, err)
					}
				},
			},
		},
		{
			name:   "a name the server no longer lists, as after the device's purge or a restore, goes off here with a note saying the server stopped showing it, and isn't sent again",
			config: projectTable(nameKey, "chip8-emulator", true),
			known:  map[string]string{nameKey: "chip8-emulator"},
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts == 1 && !cfg.Projects[nameKey].Online && f.head != nil && f.head.b == 3
			},
			checks: []rigCheck{
				bodies(`{}`),
				func(t *testing.T, r *rig, cfgPath string) {
					cfg, _, err := config.Load(cfgPath)
					if err != nil || cfg.Projects[nameKey] != (config.Project{Name: "chip8-emulator"}) {
						t.Errorf("config.toml has %+v, %v; want the name off with its label kept", cfg.Projects[nameKey], err)
					}
					n, err := ReadNames(r.dirs.Data)
					if h := n.Hidden[nameKey]; err != nil || h.Reason != NoteDropped || h.Label != "chip8-emulator" || h.At == 0 || len(n.Confirmed) != 0 {
						t.Errorf("project-names.json = %+v, %v; want a note that the server stopped showing the name", n, err)
					}
					if len(r.f.names) != 0 {
						t.Errorf("the server shows %v, want nothing sent again", r.f.names)
					}
				},
			},
		},
		{
			name:   "a name turned off after a set whose answer was lost goes as a delete at once, after a read finds it stored",
			config: projectTable(nameKey, "chip8-emulator", false),
			faults: map[int]string{2: "lost"},
			then: map[int]string{
				1: projectTable(nameKey, "chip8-emulator", true),
				2: projectTable(nameKey, "chip8-emulator", false),
			},
			until: func(f *fakeIngest, _ string) bool { return f.namesPosts >= 3 && len(f.names) == 0 },
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					var got []string
					for _, b := range r.f.namesBodies {
						got = append(got, string(b))
					}
					// The transport may send the lost request again itself,
					// which the server refuses as counter_stale.
					want := []string{`{}`, `{"delete":["` + nameKey + `"]}`}
					if len(got) < 4 || got[0] != `{}` || got[1] != `{"set":[{"project":"`+nameKey+`","label":"chip8-emulator"}]}` || !slices.Equal(got[len(got)-2:], want) {
						t.Errorf("the names requests carried %q, want a read, the set, and then a read and the delete", got)
					}
				},
			},
		},
		{
			name:   "a set whose answer was lost and that the owner hid on the dashboard before the retry stays hidden: the retry is a read, never the set again",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{2: "lost"},
			hideAt: 2,
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts >= 4 && len(f.namesHidden) == 0 && !cfg.Projects[nameKey].Online
			},
			checks: []rigCheck{
				func(t *testing.T, r *rig, cfgPath string) {
					var got []string
					for _, b := range r.f.namesBodies {
						got = append(got, string(b))
					}
					// The transport may send the lost request again itself,
					// which the server refuses as counter_stale.
					want := []string{`{}`, `{"delete":["` + nameKey + `"]}`}
					if len(got) < 4 || got[0] != `{}` || got[1] != `{"set":[{"project":"`+nameKey+`","label":"chip8-emulator"}]}` || !slices.Equal(got[len(got)-2:], want) {
						t.Errorf("the names requests carried %q, want a read, the set, then a read and the hide's delete", got)
					}
					if _, shown := r.f.names[nameKey]; shown {
						t.Errorf("the server shows the hidden name again: %v", r.f.names)
					}
					n, err := ReadNames(r.dirs.Data)
					if h, ok := n.Hidden[nameKey]; err != nil || !ok || h.Reason != "" || h.Pending {
						t.Errorf("project-names.json = %+v, %v; want a note that the owner hid the name", n, err)
					}
				},
			},
		},
		{
			name:   "after a 404, a change to the names reads first, and once the endpoint answers, a set whose answer was lost and that the owner hid before the retry stays hidden",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{1: "404", 3: "lost"},
			then:   map[int]string{1: projectTable(nameKey, "Chip-8", true)},
			hideAt: 3,
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts >= 5 && len(f.namesHidden) == 0 && !cfg.Projects[nameKey].Online
			},
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					if _, shown := r.f.names[nameKey]; shown {
						t.Errorf("the server shows the hidden name again: %v", r.f.names)
					}
					n, err := ReadNames(r.dirs.Data)
					if h, ok := n.Hidden[nameKey]; err != nil || !ok || h.Reason != "" {
						t.Errorf("project-names.json = %+v, %v; want a note that the owner hid the name", n, err)
					}
					if got := string(r.f.namesBodies[len(r.f.namesBodies)-2]); got != `{}` {
						t.Errorf("the request before the hide's delete carried %s, want a read", got)
					}
				},
				func(t *testing.T, r *rig, _ string) {
					if got := string(r.f.namesBodies[0]); got != `{}` {
						t.Errorf("the first request the endpoint answered, after the 404 and the change, carried %s, want a read", got)
					}
				},
			},
		},
		{
			name:   "a 200 whose body doesn't read leaves the outcome unknown, so the next request is a read",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{2: "garbled"},
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts >= 3 },
			checks: []rigCheck{bodies(`{}`, `{"set":[{"project":"`+nameKey+`","label":"chip8-emulator"}]}`, `{}`)},
		},
		{
			name:   "a set whose answer was lost and whose name the server no longer has, with no hide, goes off as dropped and isn't sent again",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{2: "lost"},
			dropAt: 2,
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts >= 3 && !cfg.Projects[nameKey].Online
			},
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					if _, shown := r.f.names[nameKey]; shown {
						t.Errorf("the server shows the name again: %v", r.f.names)
					}
					if got := string(r.f.namesBodies[len(r.f.namesBodies)-1]); got != `{}` {
						t.Errorf("the last request carried %s, want the read that found the name gone", got)
					}
					n, err := ReadNames(r.dirs.Data)
					if h := n.Hidden[nameKey]; err != nil || h.Reason != NoteDropped || h.Label != "chip8-emulator" || len(n.Sent) != 0 {
						t.Errorf("project-names.json = %+v, %v; want a dropped note, and nothing still waiting", n, err)
					}
				},
			},
		},
		{
			name:   "a delete whose answer was lost leaves no note: the name went as the owner asked",
			config: projectTable(nameKey, "chip8-emulator", false),
			held:   map[string]string{nameKey: "chip8-emulator"},
			known:  map[string]string{nameKey: "chip8-emulator"},
			faults: map[int]string{2: "lost"},
			until: func(f *fakeIngest, _ string) bool {
				// The read after the lost delete, which settles it.
				return len(f.namesBodies) >= 3 && string(f.namesBodies[len(f.namesBodies)-1]) == `{}` && len(f.names) == 0
			},
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					n, err := ReadNames(r.dirs.Data)
					if err != nil || len(n.Hidden) != 0 || len(n.Deleting) != 0 || len(n.Confirmed) != 0 {
						t.Errorf("project-names.json = %+v, %v; want no note and nothing waiting", n, err)
					}
				},
			},
		},
		{
			name:    "a project-names.json that doesn't read counts as another sign-in's: every name on goes off with a note, and the file is this sign-in's",
			config:  projectTable(nameKey, "chip8-emulator", true),
			rawFile: `{"v":1,"server":`,
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts == 1 && !cfg.Projects[nameKey].Online && f.head != nil && f.head.b == 3
			},
			checks: []rigCheck{
				bodies(`{}`),
				func(t *testing.T, r *rig, _ string) {
					n, err := ReadNames(r.dirs.Data)
					if h := n.Hidden[nameKey]; err != nil || h.Reason != NoteUnreadable || h.Label != "chip8-emulator" || n.Server != r.creds.Server {
						t.Errorf("project-names.json = %+v, %v; want this sign-in's, with a note of the name turned off", n, err)
					}
					if len(r.f.names) != 0 {
						t.Errorf("the server shows %v, want no name sent", r.f.names)
					}
				},
			},
		},
		{
			name:    "a name the server shows that pairtime turned off for a project-names.json that didn't read keeps its note while its delete goes",
			config:  projectTable(nameKey, "chip8-emulator", true),
			held:    map[string]string{nameKey: "chip8-emulator"},
			rawFile: `{"v":1,"server":`,
			until: func(f *fakeIngest, _ string) bool {
				return len(f.names) == 0 && len(f.namesBodies) >= 2 && string(f.namesBodies[len(f.namesBodies)-1]) == `{"delete":["`+nameKey+`"]}`
			},
			checks: []rigCheck{
				bodies(`{}`, `{"delete":["`+nameKey+`"]}`),
				func(t *testing.T, r *rig, _ string) {
					n, err := ReadNames(r.dirs.Data)
					if h := n.Hidden[nameKey]; err != nil || h.Reason != NoteUnreadable || len(n.Confirmed) != 0 {
						t.Errorf("project-names.json = %+v, %v; want the note of why the name went off, and no name shown", n, err)
					}
				},
			},
		},
		{
			name:   "no names request goes before the server holds a batch from the link, even with a name on, so pairtime logout after a wrong approval still frees the device's id",
			config: projectTable(nameKey, "chip8-emulator", true),
			until:  func(f *fakeIngest, _ string) bool { return f.names[nameKey] == "chip8-emulator" },
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					if r.f.namesEarly != 0 || r.f.posts == 0 {
						t.Errorf("%d names requests came before the server held a batch, after %d uploads; want none", r.f.namesEarly, r.f.posts)
					}
				},
			},
		},
		{
			name:   "a hidden name config.toml can't be changed for isn't sent again, nor its delete, and doctor says what to write",
			config: "[projects]\n" + nameKey + " = { name = \"chip8-emulator\", online = true }\n",
			hidden: []string{nameKey},
			known:  map[string]string{nameKey: "chip8-emulator"},
			until: func(f *fakeIngest, _ string) bool {
				return f.namesPosts > 0 && f.head != nil && f.head.b == 3
			},
			checks: []rigCheck{
				bodies(`{}`),
				func(t *testing.T, r *rig, _ string) {
					n, err := ReadNames(r.dirs.Data)
					if err != nil || !n.Hidden[nameKey].Pending {
						t.Errorf("project-names.json = %+v, %v; want a pending note", n, err)
					}
					if p := r.doctor(); !strings.Contains(p, "set online = false under [projects.<key>] in config.toml for "+nameKey) {
						t.Errorf("doctor heard %q", p)
					}
				},
			},
		},
		{
			name:   "a device with no name on and none shown sends one read, {}, at the start, and keeps the codes it gives",
			config: "mode = \"online\"\n",
			codes:  map[string]string{nameKey: "7c41e9", otherKey: "5be03d"},
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts == 1 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{
				bodies(`{}`),
				codes(map[string]string{nameKey: "7c41e9", otherKey: "5be03d"}),
				func(t *testing.T, r *rig, _ string) {
					if n, err := ReadNames(r.dirs.Data); err != nil || len(n.Confirmed) != 0 {
						t.Errorf("project-names.json = %+v, %v; want no name", n, err)
					}
				},
			},
		},
		{
			name:   "the names a sign-in to another server confirmed count for nothing here, and the start reads this one's",
			config: projectTable(nameKey, "chip8-emulator", false),
			known:  map[string]string{nameKey: "chip8-emulator"},
			server: "https://other.example.org",
			codes:  map[string]string{otherKey: "5be03d"},
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts == 1 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{
				bodies(`{}`),
				codes(map[string]string{otherKey: "5be03d"}),
				func(t *testing.T, r *rig, _ string) {
					if n, err := ReadNames(r.dirs.Data); err != nil || len(n.Confirmed) != 0 || n.Hidden[nameKey].Reason != NoteSignedIn {
						t.Errorf("project-names.json = %+v, %v; want none of the other server's names, and a note of the one it showed", n, err)
					}
				},
			},
		},
		{
			name:   "a name on when this computer signs in to another server goes off here with a note, and never goes to that server",
			config: projectTable(nameKey, "chip8-emulator", true),
			known:  map[string]string{nameKey: "chip8-emulator"},
			server: "https://other.example.org",
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts == 1 && !cfg.Projects[nameKey].Online && f.head != nil && f.head.b == 3
			},
			checks: []rigCheck{
				bodies(`{}`),
				func(t *testing.T, r *rig, cfgPath string) {
					cfg, _, err := config.Load(cfgPath)
					if err != nil || cfg.Projects[nameKey] != (config.Project{Name: "chip8-emulator"}) {
						t.Errorf("config.toml has %+v, %v; want the name off with its label kept", cfg.Projects[nameKey], err)
					}
					n, err := ReadNames(r.dirs.Data)
					if h := n.Hidden[nameKey]; err != nil || h.Reason != NoteSignedIn || h.Label != "chip8-emulator" || n.Server != r.creds.Server {
						t.Errorf("project-names.json = %+v, %v; want this sign-in's file with a note of the name turned off", n, err)
					}
					if len(r.f.names) != 0 {
						t.Errorf("the new server shows %v, want no name sent there", r.f.names)
					}
				},
			},
		},
		{
			name:   "a 503 and another computer's higher counters are retried, and every request, names and uploads, raises the one counter",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{1: "503", 2: "stale"},
			until: func(f *fakeIngest, _ string) bool {
				return f.names[nameKey] == "chip8-emulator" && f.head != nil && f.head.b == 3
			},
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					if r.f.namesPosts != 4 || !increasing(r.f.counters) {
						t.Errorf("%d names requests; the counters went %v, want four, a read refused twice, then the read and the set, and every counter above the one before", r.f.namesPosts, r.f.counters)
					}
					var st saved
					if _, err := r.st.Get(StateKey, &st); err != nil || st.Req < slices.Max(r.f.counters) {
						t.Errorf("state.json reserves counters up to %d, below the %d used", st.Req, slices.Max(r.f.counters))
					}
				},
			},
		},
		{
			name:   "a refusal sending again doesn't fix holds the names for an hour, and the uploads go on",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: allFaults("label_invalid"),
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts > 0 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					if r.f.namesPosts != 1 || len(r.f.names) != 0 {
						t.Errorf("%d names requests, the server shows %v; want one, and no name", r.f.namesPosts, r.f.names)
					}
					if p := r.doctor(); !strings.Contains(p, "refused this computer's project names as label_invalid") {
						t.Errorf("doctor heard %q", p)
					}
				},
			},
		},
		{
			name:   "a set whose answer was lost, hidden before the read after it got a 404, waits for the endpoint's answer and never goes again unread, and a change to another name reads first",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{2: "lost", 3: "404"},
			hideAt: 2,
			then:   map[int]string{3: projectTable(nameKey, "chip8-emulator", true) + projectTable(otherKey, "plant-sensor", true)},
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts >= 5 && len(f.namesHidden) == 0 && !cfg.Projects[nameKey].Online && f.names[otherKey] == "plant-sensor"
			},
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					if _, shown := r.f.names[nameKey]; shown {
						t.Errorf("the server shows the hidden name again: %v", r.f.names)
					}
					sets := 0
					for _, b := range r.f.namesBodies {
						if strings.Contains(string(b), `{"project":"`+nameKey+`","label"`) {
							sets++
						}
					}
					if got := r.f.namesBodies; sets != 1 || len(got) < 3 || string(got[len(got)-2]) != `{}` {
						t.Errorf("the names requests carried %q, want the hidden name's set once, and a read before the last", got)
					}
					n, err := ReadNames(r.dirs.Data)
					if h, ok := n.Hidden[nameKey]; err != nil || !ok || h.Reason != "" || len(n.Sent) != 0 {
						t.Errorf("project-names.json = %+v, %v; want a note that the owner hid the name, and nothing left unsettled", n, err)
					}
				},
			},
		},
		{
			name:   "a set the server refused, whose name the owner hid on the dashboard meanwhile, goes again only after a read, so turning another name on doesn't undo the hide",
			config: projectTable(nameKey, "Chip-8", true),
			held:   map[string]string{nameKey: "chip8-emulator"},
			known:  map[string]string{nameKey: "chip8-emulator"},
			faults: map[int]string{2: "label_invalid"},
			hideAt: 2,
			then:   map[int]string{2: projectTable(nameKey, "Chip-8", true) + projectTable(otherKey, "plant-sensor", true)},
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.names[otherKey] == "plant-sensor" && len(f.namesHidden) == 0 && !cfg.Projects[nameKey].Online
			},
			checks: []rigCheck{hiddenStays},
		},
		{
			name:   "a set that got a 404, whose name the owner hid on the dashboard meanwhile, goes again only after a read, so turning another name on doesn't undo the hide",
			config: projectTable(nameKey, "Chip-8", true),
			held:   map[string]string{nameKey: "chip8-emulator"},
			known:  map[string]string{nameKey: "chip8-emulator"},
			faults: map[int]string{2: "404"},
			hideAt: 2,
			then:   map[int]string{2: projectTable(nameKey, "Chip-8", true) + projectTable(otherKey, "plant-sensor", true)},
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.names[otherKey] == "plant-sensor" && len(f.namesHidden) == 0 && !cfg.Projects[nameKey].Online
			},
			checks: []rigCheck{hiddenStays},
		},
		{
			name:   "a set the server stored, answered by a proxy's 502 that the transport doesn't send again, leaves the outcome unknown, so the hide made meanwhile stays",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{2: "stored-502"},
			hideAt: 2,
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts >= 4 && len(f.namesHidden) == 0 && !cfg.Projects[nameKey].Online
			},
			checks: []rigCheck{hiddenStays, bodies(`{}`, setChip8, `{}`, `{"delete":["`+nameKey+`"]}`)},
		},
		{
			name:   "a name turned on again after an old hide, whose set's answer was lost and which the owner then hid again, goes off again rather than out",
			config: projectTable(nameKey, "chip8-emulator", true),
			notes:  map[string]HiddenName{nameKey: {Label: "chip8-emulator", At: 1}},
			faults: map[int]string{2: "lost"},
			hideAt: 2,
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts >= 4 && len(f.namesHidden) == 0 && !cfg.Projects[nameKey].Online
			},
			checks: []rigCheck{hiddenStays},
		},
		{
			name:   "a name config.toml has in a form pairtime can't change, whose set's answer was lost and which the owner hid meanwhile, stays out under the label config.toml has, and isn't sent again",
			config: "[projects]\n" + nameKey + " = { name = \"chip8-emulator\", online = true }\n",
			faults: map[int]string{2: "lost"},
			hideAt: 2,
			until: func(f *fakeIngest, _ string) bool {
				return f.namesPosts >= 3 && string(f.namesBodies[len(f.namesBodies)-1]) == `{}` && f.head != nil && f.head.b == 3
			},
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					if _, shown := r.f.names[nameKey]; shown || !r.f.namesHidden[nameKey] {
						t.Errorf("the server shows %v and hides %v, want the name hidden still", r.f.names, r.f.namesHidden)
					}
					n, err := ReadNames(r.dirs.Data)
					if h := n.Hidden[nameKey]; err != nil || !h.Pending || h.Label != "chip8-emulator" {
						t.Errorf("project-names.json = %+v, %v; want a pending note with config.toml's label", n, err)
					}
					if p := r.doctor(); !strings.Contains(p, "set online = false under [projects.<key>] in config.toml for "+nameKey) {
						t.Errorf("doctor heard %q", p)
					}
				},
			},
		},
		{
			name:   "a set the server answered with a 429 stored nothing: it goes again after a read, and the name stays on",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{2: "429"},
			until:  func(f *fakeIngest, _ string) bool { return f.names[nameKey] == "chip8-emulator" },
			checks: []rigCheck{bodies(`{}`, setChip8, `{}`, setChip8), stillOn},
		},
		{
			name:   "a set the server refused stored nothing: the name stays on with no note, and nothing more goes while config.toml asks for the same",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{2: "label_invalid"},
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts >= 2 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{bodies(`{}`, setChip8), stillOn},
		},
		{
			name:   "an answer from the endpoint, even one that doesn't read, ends a 404's day, so what config.toml asked for at the 404 goes once it asks for it again",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: map[int]string{1: "404", 2: "garbled"},
			then:   map[int]string{1: projectTable(nameKey, "Chip-8", true), 2: projectTable(nameKey, "chip8-emulator", true)},
			until:  func(f *fakeIngest, _ string) bool { return f.names[nameKey] == "chip8-emulator" },
			checks: []rigCheck{bodies(`{}`, `{}`, setChip8)},
		},
		{
			name:   "an answer that isn't pairtimed's counts as a network error, is never acted on, tells doctor nothing, and holds back no upload",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: allFaults("html"),
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts > 0 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{quiet},
		},
		{
			name:   "a 404 to the read at the start, from a server without the names endpoint, tells doctor nothing, holds back no upload, and waits a day to read again",
			config: "mode = \"online\"\n",
			codes:  map[string]string{nameKey: "7c41e9"},
			faults: allFaults("404"),
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts > 0 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{missing(1)},
		},
		{
			name:   "a 404 to the read at the start with a name turned on is as quiet, and the name waits a day for the endpoint's answer, since the device doesn't know what the server holds",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: allFaults("404"),
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts > 0 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{missing(1), bodies()},
		},
		{
			name:   "a name config.toml turns on for a project this computer never worked in, as in a config.toml copied from another computer, never goes, and doctor says which",
			config: projectTable(otherKey, "copied", true),
			own:    []string{nameKey},
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts == 1 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{
				bodies(`{}`),
				func(t *testing.T, r *rig, _ string) {
					if p := r.doctor(); !strings.Contains(p, "config.toml turns on the names of projects this computer hasn't worked in, as when it was copied from another computer, so pairtime doesn't send them: "+otherKey) {
						t.Errorf("doctor heard %q", p)
					}
				},
			},
		},
		{
			name:   "a name for a project this computer worked in goes before the server gives the project a code",
			config: projectTable(nameKey, "chip8-emulator", true),
			own:    []string{nameKey},
			until:  func(f *fakeIngest, _ string) bool { return f.names[nameKey] == "chip8-emulator" },
			checks: []rigCheck{bodies(`{}`, `{"set":[{"project":"`+nameKey+`","label":"chip8-emulator"}]}`)},
		},
		{
			name:   "nothing goes while the computer is local-only",
			config: "mode = \"local\"\n" + projectTable(nameKey, "chip8-emulator", true),
			local:  true,
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					if r.f.namesPosts != 0 || r.f.posts != 0 || r.f.gets != 0 {
						t.Errorf("%d names requests, %d uploads and %d state reads went from a local-only computer", r.f.namesPosts, r.f.posts, r.f.gets)
					}
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, 3, nil, -time.Minute)
			cfgPath := filepath.Join(r.dirs.Config, "config.toml")
			if err := os.WriteFile(cfgPath, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			maps.Copy(r.f.names, tt.held)
			maps.Copy(r.f.codes, tt.codes)
			maps.Copy(r.f.namesFaults, tt.faults)
			for _, key := range tt.hidden {
				r.f.namesHidden[key] = true
			}
			r.f.namesThen = map[int]func(){}
			// step adds f to what runs after the attempt, after the steps
			// already there.
			step := func(attempt int, f func()) {
				if prev := r.f.namesThen[attempt]; prev != nil {
					r.f.namesThen[attempt] = func() { prev(); f() }
					return
				}
				r.f.namesThen[attempt] = f
			}
			if tt.hideAt > 0 {
				// Hide this name, on the fake server, under its lock.
				step(tt.hideAt, func() {
					if _, ok := r.f.names[nameKey]; ok {
						delete(r.f.names, nameKey)
						r.f.namesHidden[nameKey] = true
					}
				})
			}
			if tt.dropAt > 0 {
				// The server loses the name and keeps no hide, as after the
				// hide's 30 days or for a removed device.
				step(tt.dropAt, func() { delete(r.f.names, nameKey) })
			}
			if tt.rawFile != "" {
				if err := os.WriteFile(filepath.Join(r.dirs.Data, NamesFileName), []byte(tt.rawFile), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for attempt, text := range tt.then {
				step(attempt, func() {
					if err := os.WriteFile(cfgPath, []byte(text), 0o600); err != nil {
						t.Error(err)
					}
				})
			}
			if tt.known != nil || tt.notes != nil {
				server := tt.server
				if server == "" {
					server = r.creds.Server
				}
				n := Names{V: namesVersion, Server: server, Device: r.creds.DeviceID, Confirmed: tt.known, Hidden: tt.notes, AnsweredAt: time.Now().Add(-time.Hour).UnixMilli()}
				data, err := json.Marshal(n)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(r.dirs.Data, NamesFileName), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			r.u.Close()
			r.u = r.namesUploader(t, cfgPath, func() (Credentials, bool) { return r.creds, !tt.local })
			if tt.own != nil {
				r.u.opts.OwnKeys = func() map[string]bool {
					own := map[string]bool{}
					for _, key := range tt.own {
						own[key] = true
					}
					return own
				}
			}
			if tt.until == nil {
				r.u.Start()
				for range 5 {
					r.u.Wake()
					time.Sleep(50 * time.Millisecond)
				}
				r.u.Close()
			} else {
				r.run(t, "the names to settle", func(f *fakeIngest) bool { return tt.until(f, cfgPath) })
			}
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			for _, check := range tt.checks {
				check(t, r, cfgPath)
			}
			if r.f.namesEarly != 0 {
				t.Errorf("%d names requests came before the server held a batch from the link, want none", r.f.namesEarly)
			}
		})
	}
}

func TestNamesCodes(t *testing.T) {
	tests := []struct {
		name   string
		config string              // config.toml
		starts []map[string]string // the codes the server gives at each start of the agent, in order
		want   map[string]string   // project-names.json's codes after the last
	}{
		{
			name:   "the codes the read at the start gets are kept, on a device with no name on too",
			config: "mode = \"online\"\n",
			starts: []map[string]string{{nameKey: "7c41e9", otherKey: "5be03d"}},
			want:   map[string]string{nameKey: "7c41e9", otherKey: "5be03d"},
		},
		{
			name:   "the next answer's codes replace the ones kept: a code that got longer, and one the server no longer gives",
			config: "mode = \"online\"\n",
			starts: []map[string]string{{nameKey: "7c41e9", otherKey: "5be03d"}, {nameKey: "7c41e9a"}},
			want:   map[string]string{nameKey: "7c41e9a"},
		},
		{
			name:   "an answer with no code leaves none kept",
			config: "mode = \"online\"\n",
			starts: []map[string]string{{nameKey: "7c41e9"}, {}},
			want:   nil,
		},
		{
			name:   "a code that isn't one, and a code of what isn't a key, aren't kept",
			config: "mode = \"online\"\n",
			starts: []map[string]string{{nameKey: "7C41E9", otherKey: "5be03d", "not a key": "abcdef", "p_long": strings.Repeat("a", 34)}},
			want:   map[string]string{otherKey: "5be03d"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, 3, nil, -time.Minute)
			cfgPath := filepath.Join(r.dirs.Config, "config.toml")
			if err := os.WriteFile(cfgPath, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			for i, codes := range tt.starts {
				// Each start is another agent's, over the same files.
				r.u.Close()
				st, _, err := state.Open(r.dirs.StateFile())
				if err != nil {
					t.Fatal(err)
				}
				r.st = st
				r.f.mu.Lock()
				r.f.codes = maps.Clone(codes)
				r.f.mu.Unlock()
				r.u = r.namesUploader(t, cfgPath, func() (Credentials, bool) { return r.creds, true })
				r.run(t, "the start's names request", func(f *fakeIngest) bool {
					return f.namesPosts == i+1 && f.head != nil && f.head.b == 3
				})
			}
			n, err := ReadNames(r.dirs.Data)
			if err != nil || !maps.Equal(n.Codes, tt.want) {
				t.Errorf("project-names.json = %+v, %v; want the codes %v", n, err, tt.want)
			}
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			if r.f.namesPosts != len(tt.starts) {
				t.Errorf("%d names requests, want one at each of the %d starts", r.f.namesPosts, len(tt.starts))
			}
		})
	}
}

func TestKeptCodes(t *testing.T) {
	tests := []struct {
		name  string
		codes map[string]string
		want  map[string]string
	}{
		{name: "a code of 0-9 a-z _ - is kept by its key", codes: map[string]string{nameKey: "7c41e9", "p_bot": "p_bot"}, want: map[string]string{nameKey: "7c41e9", "p_bot": "p_bot"}},
		{name: "a git adapter's whole key, 33 characters, is a code", codes: map[string]string{nameKey: nameKey}, want: map[string]string{nameKey: nameKey}},
		{name: "a code of 34 characters isn't one", codes: map[string]string{nameKey: strings.Repeat("a", 34)}},
		{name: "a code with a capital letter isn't one", codes: map[string]string{nameKey: "7C41e9"}},
		{name: "a code with a space or a control character isn't one", codes: map[string]string{nameKey: "7c41 e9", otherKey: "5be03d\u202e"}},
		{name: "an empty code isn't one", codes: map[string]string{nameKey: ""}},
		{name: "a code of what isn't a key isn't kept", codes: map[string]string{"g7c41e9/a": "7c41e9"}},
		{name: "none kept is nil", codes: map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keptCodes(tt.codes)
			if !maps.Equal(got, tt.want) || (tt.want == nil) != (got == nil) {
				t.Errorf("keptCodes(%q) = %q, want %q", tt.codes, got, tt.want)
			}
		})
	}
}

func TestReadNamesCodes(t *testing.T) {
	tests := []struct {
		name string
		file string
		want map[string]string
	}{
		{
			name: "the codes the uploader kept are read by their keys",
			file: `{"v":1,"server":"https://pairtime.example.org","device":"d_example","codes":{"` + nameKey + `":"7c41e9"}}`,
			want: map[string]string{nameKey: "7c41e9"},
		},
		{
			name: "a code the file holds that isn't one counts for nothing, so nothing else shows as a code",
			file: `{"v":1,"server":"https://pairtime.example.org","device":"d_example","codes":{"` + nameKey + `":"7c41e9\u001b[31m","` + otherKey + `":"5be03d"}}`,
			want: map[string]string{otherKey: "5be03d"},
		},
		{
			name: "a file from before codes has none",
			file: `{"v":1,"server":"https://pairtime.example.org","device":"d_example","confirmed":{"` + nameKey + `":"chip8-emulator"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, NamesFileName), []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			n, err := ReadNames(dir)
			if err != nil || !maps.Equal(n.Codes, tt.want) {
				t.Errorf("ReadNames = %+v, %v; want the codes %v", n, err, tt.want)
			}
		})
	}
}

func TestSignedInElsewhere(t *testing.T) {
	const (
		server = "https://pairtime.example.org"
		device = "d_example"
	)
	at := time.Date(2026, 11, 22, 21, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		file       *Names            // project-names.json, none when nil
		raw        string            // project-names.json's bytes, when set, for one that doesn't read
		on         map[string]string // the names config.toml has on
		stuck      string            // a key hide can't turn off
		wantOff    map[string]string
		wantReason string
		wantFile   *Names // project-names.json after it, unchanged when nil
	}{
		{
			name:       "a file of another server turns every name on, and every name that server showed, off with a note, and is this sign-in's from then on",
			file:       &Names{V: namesVersion, Server: "https://other.example.org", Device: device, Confirmed: map[string]string{nameKey: "chip8-emulator"}, Codes: map[string]string{nameKey: "7c41e9"}},
			on:         map[string]string{otherKey: "plant-sensor"},
			wantOff:    map[string]string{nameKey: "chip8-emulator", otherKey: "plant-sensor"},
			wantReason: NoteSignedIn,
			wantFile: &Names{V: namesVersion, Server: server, Device: device, Hidden: map[string]HiddenName{
				nameKey:  {Label: "chip8-emulator", At: at.UnixMilli(), Reason: NoteSignedIn},
				otherKey: {Label: "plant-sensor", At: at.UnixMilli(), Reason: NoteSignedIn},
			}},
		},
		{
			name:       "a file of another device on the same server does the same",
			file:       &Names{V: namesVersion, Server: server, Device: "d_other", Confirmed: map[string]string{nameKey: "chip8-emulator"}},
			wantOff:    map[string]string{nameKey: "chip8-emulator"},
			wantReason: NoteSignedIn,
			wantFile: &Names{V: namesVersion, Server: server, Device: device, Hidden: map[string]HiddenName{
				nameKey: {Label: "chip8-emulator", At: at.UnixMilli(), Reason: NoteSignedIn},
			}},
		},
		{
			name: "a name config.toml can't turn off gets a pending note, so the uploader never sends it",
			file: &Names{V: namesVersion, Server: "https://other.example.org", Device: device},
			on:   map[string]string{nameKey: "chip8-emulator"}, stuck: nameKey,
			wantOff: map[string]string{nameKey: "chip8-emulator"}, wantReason: NoteSignedIn,
			wantFile: &Names{V: namesVersion, Server: server, Device: device, Hidden: map[string]HiddenName{
				nameKey: {Label: "chip8-emulator", At: at.UnixMilli(), Reason: NoteSignedIn, Pending: true},
			}},
		},
		{
			name: "a file of this sign-in changes nothing",
			file: &Names{V: namesVersion, Server: server, Device: device, Confirmed: map[string]string{nameKey: "chip8-emulator"}},
			on:   map[string]string{nameKey: "chip8-emulator"},
		},
		{
			name:     "with no file, as before a computer's first link, nothing goes off: the names turned on before it go, and the file names this sign-in from now on",
			on:       map[string]string{nameKey: "chip8-emulator"},
			wantFile: &Names{V: namesVersion, Server: server, Device: device},
		},
		{
			name:     "a file that names no sign-in gets this one, its notes kept",
			file:     &Names{V: namesVersion, Hidden: map[string]HiddenName{nameKey: {Label: "chip8-emulator", At: 1}}},
			on:       map[string]string{otherKey: "plant-sensor"},
			wantFile: &Names{V: namesVersion, Server: server, Device: device, Hidden: map[string]HiddenName{nameKey: {Label: "chip8-emulator", At: 1}}},
		},
		{
			name:       "a file that doesn't read counts as another sign-in's, since pairtime can't tell: every name on goes off",
			raw:        `{"v":1,"server":`,
			on:         map[string]string{nameKey: "chip8-emulator"},
			wantOff:    map[string]string{nameKey: "chip8-emulator"},
			wantReason: NoteUnreadable,
			wantFile: &Names{V: namesVersion, Server: server, Device: device, Hidden: map[string]HiddenName{
				nameKey: {Label: "chip8-emulator", At: at.UnixMilli(), Reason: NoteUnreadable},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			name := filepath.Join(dir, NamesFileName)
			if tt.file != nil {
				data, err := json.Marshal(tt.file)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.raw != "" {
				if err := os.WriteFile(name, []byte(tt.raw), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(name)
			var hid []string
			hide := func(key string) error {
				if key == tt.stuck {
					return errors.New("config.toml has the project as an inline table")
				}
				hid = append(hid, key)
				return nil
			}
			e, err := SignedInElsewhere(dir, server, device, tt.on, hide, at)
			if err != nil {
				t.Fatal(err)
			}
			var wantStuck []string
			if tt.stuck != "" {
				wantStuck = []string{tt.stuck}
			}
			if !maps.Equal(e.Off, tt.wantOff) || e.Reason != tt.wantReason || !slices.Equal(e.Stuck, wantStuck) {
				t.Errorf("turned off %v, %v stuck, for %q; want %v, %v stuck, for %q", e.Off, e.Stuck, e.Reason, tt.wantOff, wantStuck, tt.wantReason)
			}
			var want []string
			for key := range tt.wantOff {
				if key != tt.stuck {
					want = append(want, key)
				}
			}
			slices.Sort(want)
			if !slices.Equal(hid, want) {
				t.Errorf("hid %v in config.toml, want %v", hid, want)
			}
			after, _ := os.ReadFile(name)
			if tt.wantFile == nil {
				if string(after) != string(before) {
					t.Errorf("project-names.json changed to %s", after)
				}
				return
			}
			got, err := ReadNames(dir)
			if err != nil || !reflect.DeepEqual(got, *tt.wantFile) {
				t.Errorf("project-names.json = %+v, %v; want %+v", got, err, *tt.wantFile)
			}
		})
	}
}

func TestNextNames(t *testing.T) {
	c := Credentials{Server: "https://pairtime.example.org", DeviceID: "d_example"}
	mine := func(confirmed map[string]string) *Names {
		return &Names{V: namesVersion, Server: c.Server, Device: c.DeviceID, Confirmed: confirmed, Codes: map[string]string{nameKey: "7c41e9"}}
	}
	tests := []struct {
		name    string
		file    *Names
		raw     string // the file's bytes, for one that doesn't read
		wanted  map[string]string
		own     map[string]bool
		linking bool
		want    string // the body, "" for no request
	}{
		{
			name:   "a name turned on that the server doesn't show is the next request, its body as the uploader encodes it",
			file:   mine(nil),
			wanted: map[string]string{nameKey: "chip8-emulator"},
			want:   `{"set":[{"project":"` + nameKey + `","label":"chip8-emulator"}]}`,
		},
		{
			name: "a name the server shows that config.toml has off is a delete",
			file: mine(map[string]string{nameKey: "chip8-emulator"}),
			want: `{"delete":["` + nameKey + `"]}`,
		},
		{
			name:   "names the server shows as config.toml has them need no request, and a read never shows",
			file:   mine(map[string]string{nameKey: "chip8-emulator"}),
			wanted: map[string]string{nameKey: "chip8-emulator"},
		},
		{
			name:    "nothing goes while the link's first batch isn't stored",
			file:    mine(nil),
			wanted:  map[string]string{nameKey: "chip8-emulator"},
			linking: true,
		},
		{
			name:   "a file of another sign-in sends nothing, since its names all go off when the agent loads it",
			file:   &Names{V: namesVersion, Server: "https://other.example.org", Device: c.DeviceID},
			wanted: map[string]string{nameKey: "chip8-emulator"},
		},
		{
			name:   "a file that doesn't read sends nothing, since the agent counts it as another sign-in's and turns its names off",
			raw:    `{"v":1,"server":`,
			wanted: map[string]string{nameKey: "chip8-emulator"},
		},
		{
			name:   "a project this computer never worked in, with no code from the server, isn't in it",
			file:   &Names{V: namesVersion, Server: c.Server, Device: c.DeviceID},
			wanted: map[string]string{otherKey: "copied"},
			own:    map[string]bool{nameKey: true},
		},
		{
			name:   "a hidden name pairtime couldn't turn off stays out while its label is the one it had",
			file:   &Names{V: namesVersion, Server: c.Server, Device: c.DeviceID, Hidden: map[string]HiddenName{nameKey: {Label: "chip8-emulator", At: 1, Pending: true}}},
			wanted: map[string]string{nameKey: "chip8-emulator"},
		},
		{
			name:   "with no file, as before a first link, a name turned on goes",
			wanted: map[string]string{nameKey: "chip8-emulator"},
			want:   `{"set":[{"project":"` + nameKey + `","label":"chip8-emulator"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != nil {
				data, err := json.Marshal(tt.file)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, NamesFileName), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.raw != "" {
				if err := os.WriteFile(filepath.Join(dir, NamesFileName), []byte(tt.raw), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			pv, err := NextNames(dir, c, tt.wanted, tt.own, tt.linking)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if pv != nil {
				got = string(pv.JSON)
			}
			if got != tt.want {
				t.Errorf("NextNames = %q, want %q", got, tt.want)
			}
		})
	}
}
