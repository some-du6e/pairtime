// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, del := namesDiff(tt.wanted, tt.confirmed)
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
	tests := []struct {
		name   string
		config string            // config.toml
		held   map[string]string // the names the server holds at the start
		known  map[string]string // project-names.json's names, as the server confirmed them
		server string            // the server project-names.json is for, the rig's when ""
		faults map[int]string    // the names requests' faults, by attempt
		local  bool              // the computer is local-only: no sign-in to upload with
		until  func(f *fakeIngest, cfgPath string) bool
		checks []rigCheck
	}{
		{
			name:   "a name turned on in config.toml goes in a names request, and the server's answer is kept in project-names.json for its owner alone",
			config: projectTable(nameKey, "chip8-emulator", true),
			until:  func(f *fakeIngest, _ string) bool { return f.names[nameKey] == "chip8-emulator" },
			checks: []rigCheck{
				bodies(`{"set":[{"project":"` + nameKey + `","label":"chip8-emulator"}]}`),
				func(t *testing.T, r *rig, _ string) {
					n, err := ReadNames(r.dirs.Data)
					if err != nil || !maps.Equal(n.Confirmed, map[string]string{nameKey: "chip8-emulator"}) || n.AnsweredAt == 0 || n.Server != r.creds.Server || n.Device != r.creds.DeviceID {
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
			checks: []rigCheck{bodies(`{"delete":["` + nameKey + `"]}`)},
		},
		{
			name:   "a label changed in config.toml goes as a set with the new label",
			config: projectTable(nameKey, `Chip-8 "emulator" <v2>`, true),
			held:   map[string]string{nameKey: "chip8-emulator"},
			known:  map[string]string{nameKey: "chip8-emulator"},
			until:  func(f *fakeIngest, _ string) bool { return f.names[nameKey] != "chip8-emulator" },
			checks: []rigCheck{
				bodies(`{"set":[{"project":"` + nameKey + `","label":"Chip-8 \"emulator\" <v2>"}]}`),
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
			name:   "a name the owner hid on the dashboard while the agent was off turns off in config.toml, with a note, and isn't sent again",
			config: projectTable(nameKey, "chip8-emulator", true),
			known:  map[string]string{nameKey: "chip8-emulator"},
			until: func(f *fakeIngest, cfgPath string) bool {
				cfg, _, _ := config.Load(cfgPath)
				return f.namesPosts > 0 && !cfg.Projects[nameKey].Online && f.head != nil && f.head.b == 3
			},
			checks: []rigCheck{
				bodies(`{}`),
				func(t *testing.T, r *rig, cfgPath string) {
					cfg, _, err := config.Load(cfgPath)
					if err != nil || cfg.Projects[nameKey] != (config.Project{Name: "chip8-emulator"}) {
						t.Errorf("config.toml has %+v, %v; want the name off with its label kept", cfg.Projects[nameKey], err)
					}
					n, err := ReadNames(r.dirs.Data)
					if h := n.Hidden[nameKey]; err != nil || h.Label != "chip8-emulator" || h.At == 0 || h.Pending || len(n.Confirmed) != 0 {
						t.Errorf("project-names.json = %+v, %v; want a note of the hidden name", n, err)
					}
				},
			},
		},
		{
			name:   "a hidden name config.toml can't be changed for isn't sent again, and doctor says what to write",
			config: "[projects]\n" + nameKey + " = { name = \"chip8-emulator\", online = true }\n",
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
			name:   "with no name on and none shown, no names request goes",
			config: "mode = \"online\"\n",
			until:  func(f *fakeIngest, _ string) bool { return f.head != nil && f.head.b == 3 },
			checks: []rigCheck{bodies()},
		},
		{
			name:   "the names a sign-in to another server confirmed count for nothing here",
			config: projectTable(nameKey, "chip8-emulator", false),
			known:  map[string]string{nameKey: "chip8-emulator"},
			server: "https://other.example.org",
			until:  func(f *fakeIngest, _ string) bool { return f.head != nil && f.head.b == 3 },
			checks: []rigCheck{bodies()},
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
					if r.f.namesPosts != 3 || !increasing(r.f.counters) {
						t.Errorf("%d names requests; the counters went %v, want three requests and every counter above the one before", r.f.namesPosts, r.f.counters)
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
			name:   "an answer that isn't pairtimed's counts as a network error, is never acted on, and holds back no upload",
			config: projectTable(nameKey, "chip8-emulator", true),
			faults: allFaults("html"),
			until:  func(f *fakeIngest, _ string) bool { return f.namesPosts > 0 && f.head != nil && f.head.b == 3 },
			checks: []rigCheck{
				func(t *testing.T, r *rig, _ string) {
					n, err := ReadNames(r.dirs.Data)
					if !errors.Is(err, fs.ErrNotExist) || len(n.Confirmed) != 0 {
						t.Errorf("project-names.json = %+v, %v; want none, since no answer was pairtimed's", n, err)
					}
				},
			},
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
			maps.Copy(r.f.namesFaults, tt.faults)
			if tt.known != nil {
				server := tt.server
				if server == "" {
					server = r.creds.Server
				}
				n := Names{V: namesVersion, Server: server, Device: r.creds.DeviceID, Confirmed: tt.known, AnsweredAt: time.Now().Add(-time.Hour).UnixMilli()}
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
		})
	}
}
