// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/state"
)

// signIn is the rig's sign-in, which a test changes while the uploader
// runs, as pairtime logout and login do beside the running agent.
type signIn struct {
	mu sync.Mutex
	c  Credentials
}

func (s *signIn) get() (Credentials, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c, true
}

func (s *signIn) set(c Credentials) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c = c
}

func TestNamesSignIns(t *testing.T) {
	inline := func(key, label string) string {
		return "[projects]\n" + key + " = { name = \"" + label + "\", online = true }\n"
	}
	putNames := func(t *testing.T, r *rig, raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(r.dirs.Data, NamesFileName), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// onlyReads checks that every names request after the first run's
	// posts was a read, so no name went.
	onlyReads := func(t *testing.T, r *rig, posts int) {
		t.Helper()
		for _, b := range r.f.namesBodies[posts:] {
			if string(b) != `{}` {
				t.Errorf("after the change the names requests carried %q, want reads alone", r.f.namesBodies[posts:])
				return
			}
		}
	}
	tests := []struct {
		name string
		// config is config.toml for the first run, which ends once first
		// holds.
		config string
		first  func(f *fakeIngest) bool
		// between changes the files, the fake server and the sign-in, under
		// the fake's lock, before the second run. restart says a new agent
		// makes it, as after a restart; else the same uploader goes on.
		between func(t *testing.T, r *rig, cfgPath string, s *signIn)
		restart bool
		// The second run ends once a names request went after the first
		// run's, and a few wakes later.
		check func(t *testing.T, r *rig, posts int)
	}{
		{
			name:   "an agent that starts after its link's first batch, with a project-names.json that doesn't read, keeps a name config.toml has in a form pairtime can't change out of what goes, and doctor names it",
			config: projectTable(nameKey, "chip8-emulator", true),
			first: func(f *fakeIngest) bool {
				return f.names[nameKey] == "chip8-emulator" && f.head != nil && f.head.b == 3
			},
			between: func(t *testing.T, r *rig, cfgPath string, _ *signIn) {
				// The owner hid it on the dashboard; config.toml has it in
				// an inline table, which pairtime doesn't change.
				delete(r.f.names, nameKey)
				r.f.namesHidden[nameKey] = true
				if err := os.WriteFile(cfgPath, []byte(inline(nameKey, "chip8-emulator")), 0o600); err != nil {
					t.Fatal(err)
				}
				putNames(t, r, []byte(`{"v":1,"server":`))
			},
			restart: true,
			check: func(t *testing.T, r *rig, posts int) {
				onlyReads(t, r, posts)
				if _, shown := r.f.names[nameKey]; shown || !r.f.namesHidden[nameKey] {
					t.Errorf("the server shows %v and hides %v, want the name hidden still", r.f.names, r.f.namesHidden)
				}
				n, err := ReadNames(r.dirs.Data)
				if h := n.Hidden[nameKey]; err != nil || !h.Pending || h.Reason != NoteUnreadable {
					t.Errorf("project-names.json = %+v, %v; want a pending note that the file didn't read", n, err)
				}
				if p := r.doctor(); !strings.Contains(p, "set online = false under [projects.<key>] in config.toml for "+nameKey) {
					t.Errorf("doctor heard %q", p)
				}
			},
		},
		{
			name:   "a new sign-in to the same server, as the same device, reads project-names.json again, so a name pairtime login turned off with a note it couldn't write into config.toml isn't sent",
			config: "",
			first:  func(f *fakeIngest) bool { return f.namesPosts >= 1 && f.head != nil && f.head.b == 3 },
			between: func(t *testing.T, r *rig, cfgPath string, s *signIn) {
				// As pairtime logout and login do: a new token, a name turned
				// on meanwhile in an inline table, and the file login wrote,
				// whose note for it is pending.
				c := r.creds
				c.Token = "pt_dev_relinked"
				s.set(c)
				r.f.token = c.Token
				if err := os.WriteFile(cfgPath, []byte(inline(otherKey, "plant-sensor")), 0o600); err != nil {
					t.Fatal(err)
				}
				n := Names{V: namesVersion, Server: c.Server, Device: c.DeviceID, Hidden: map[string]HiddenName{
					otherKey: {Label: "plant-sensor", At: time.Now().UnixMilli(), Reason: NoteSignedIn, Pending: true},
				}}
				raw, err := json.Marshal(n)
				if err != nil {
					t.Fatal(err)
				}
				putNames(t, r, raw)
			},
			check: func(t *testing.T, r *rig, posts int) {
				onlyReads(t, r, posts)
				if _, shown := r.f.names[otherKey]; shown {
					t.Errorf("the server shows %v, want the name another sign-in's note keeps out", r.f.names)
				}
				n, err := ReadNames(r.dirs.Data)
				if h := n.Hidden[otherKey]; err != nil || !h.Pending || h.Reason != NoteSignedIn {
					t.Errorf("project-names.json = %+v, %v; want login's pending note kept", n, err)
				}
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
			s := &signIn{c: r.creds}
			r.u.Close()
			r.u = r.namesUploader(t, cfgPath, s.get)
			r.u.Start()
			waitFor(t, "the first run", func() bool {
				r.u.Wake()
				r.f.mu.Lock()
				defer r.f.mu.Unlock()
				return tt.first(r.f)
			})
			if tt.restart {
				r.u.Close()
			} else {
				// Let the cycle that met first end before the files change.
				time.Sleep(300 * time.Millisecond)
			}
			r.f.mu.Lock()
			posts := r.f.namesPosts
			tt.between(t, r, cfgPath, s)
			r.f.mu.Unlock()
			if tt.restart {
				st, _, err := state.Open(r.dirs.StateFile())
				if err != nil {
					t.Fatal(err)
				}
				r.st = st
				r.u = r.namesUploader(t, cfgPath, s.get)
				r.u.Start()
			}
			waitFor(t, "a names request after the change", func() bool {
				r.u.Wake()
				r.f.mu.Lock()
				defer r.f.mu.Unlock()
				return r.f.namesPosts > posts
			})
			for range 3 {
				r.u.Wake()
				time.Sleep(100 * time.Millisecond)
			}
			r.u.Close()
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			tt.check(t, r, posts)
		})
	}
}

func TestNoteSending(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	body := namesBody{Set: []nameItem{{Project: nameKey, Label: "chip8-emulator"}}, Delete: []string{otherKey}}
	tests := []struct {
		name    string
		dataDir string
		wantErr bool
	}{
		{
			name:    "a set's names and a delete's keys are written down in project-names.json before the request goes",
			dataDir: t.TempDir(),
		},
		{
			name:    "a request whose names can't be written down doesn't go, and leaves nothing noted, so no lost answer can bring a hidden name back",
			dataDir: filepath.Join(file, "data"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Uploader{opts: Options{DataDir: tt.dataDir}, names: &Names{V: namesVersion}, log: slog.New(slog.DiscardHandler)}
			err := u.noteSending(body)
			switch {
			case tt.wantErr && (err == nil || len(u.names.Sent)+len(u.names.Deleting) != 0):
				t.Errorf("noteSending = %v, with %+v noted; want an error and nothing noted", err, u.names)
			case !tt.wantErr && err != nil:
				t.Fatal(err)
			case !tt.wantErr:
				n, err := ReadNames(tt.dataDir)
				if err != nil || n.Sent[nameKey] != "chip8-emulator" || len(n.Deleting) != 1 || n.Deleting[0] != otherKey {
					t.Errorf("project-names.json = %+v, %v; want the set's name and the delete's key", n, err)
				}
			}
		})
	}
}
