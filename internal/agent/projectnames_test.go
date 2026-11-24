// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

// Made-up project keys in the git adapter's form, and a sign-in.
const (
	chip8Key    = "g7c41e9a2d05b8f63e1a94c7d2b6f0e85"
	folderKey   = "dd2a8f61c94e07b35a8c1f6e29d4b7a03"
	namesServer = "https://pairtime.example.org"
	namesDevice = "d_example"
)

// send makes a request of the agent with the bearer token, and returns
// the answer's status and body.
func send(t *testing.T, a *running, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", a.port.Port, path), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.port.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

// projectsDir fills a data directory with a project map holding
// chip8-emulator's repo, and today's rollup with a plain folder's
// project, which the map doesn't keep, and, when linked, the names and
// codes the server last gave, and the notes of names hidden on the
// dashboard.
func projectsDir(t *testing.T, dirs home.Dirs, linked bool, shown, codes map[string]string, hidden map[string]uploader.HiddenName) {
	t.Helper()
	m := git.ProjectMap{Repos: []git.RepoProject{{CommonDir: filepath.Join(dirs.Data, "code", "chip8-emulator", ".git"), Key: chip8Key, Kind: git.KindRepo}}}
	st, err := json.Marshal(map[string]any{"v": 1, projectsKey: m})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.StateFile(), st, 0o600); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format(time.DateOnly)
	start, end, err := store.DayBounds(today, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteDay(dirs.Days(), store.Day{Date: today, Zone: "Local", Start: start.UTC(), End: end.UTC(),
		Projects: []store.DayProject{{Project: chip8Key}, {Project: folderKey}}}); err != nil {
		t.Fatal(err)
	}
	if !linked {
		return
	}
	l := `{"v":1,"server":"` + namesServer + `","device_id":"` + namesDevice + `","public_key":"AAAA","linked_at":"2026-11-01T00:00:00Z"}`
	if err := os.WriteFile(account.File(dirs.Data), []byte(l), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := json.Marshal(uploader.Names{V: 1, Server: namesServer, Device: namesDevice, Confirmed: shown, Codes: codes, AnsweredAt: 1, Hidden: hidden})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Data, uploader.NamesFileName), n, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectsAPI(t *testing.T) {
	type step struct {
		method, path, body string
		code               int
		// check reads the answer's body and config.toml's project tables.
		check func(t *testing.T, body string, names map[string]config.Project)
	}
	entry := func(t *testing.T, body string) projectReply {
		t.Helper()
		var r projectReply
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("%v in %s", err, body)
		}
		return r
	}
	name := "/v1/projects/" + chip8Key + "/name"
	tests := []struct {
		name   string
		config string
		linked bool
		shown  map[string]string
		codes  map[string]string // the codes the server last gave
		hidden map[string]uploader.HiddenName
		// rawNames is project-names.json's bytes before the steps, when set.
		rawNames string
		steps    []step
		// file checks project-names.json after the steps, when set.
		file func(t *testing.T, n uploader.Names, err error)
	}{
		{
			name:     "GET /v1/projects says a name being sent is sent when a set of it went and no answer came for it, and not when none went",
			config:   "mode = \"online\"\n[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nonline = true\n[projects." + folderKey + "]\nname = \"scratch\"\nonline = true\n",
			linked:   true,
			rawNames: `{"v":1,"server":"` + namesServer + `","device":"` + namesDevice + `","answered_at":1,"sent":{"` + chip8Key + `":"chip8-emulator"}}`,
			steps: []step{{method: "GET", path: "/v1/projects", code: 200, check: func(t *testing.T, body string, _ map[string]config.Project) {
				var reply projectsReply
				if err := json.Unmarshal([]byte(body), &reply); err != nil {
					t.Fatal(err)
				}
				got := make(map[string]projectReply)
				for _, p := range reply.Projects {
					got[p.Project] = p
				}
				if c, f := got[chip8Key], got[folderKey]; c.State != "sending" || !c.Sent || f.State != "sending" || f.Sent {
					t.Errorf("GET /v1/projects = %s; want both names sending, and only chip8-emulator's sent", body)
				}
			}}},
		},
		{
			name: "GET /v1/projects lists a repo by its local name, with its name off and that name to show, and a plain folder with neither, and no code before a server gives one",
			steps: []step{{method: "GET", path: "/v1/projects", code: 200, check: func(t *testing.T, body string, _ map[string]config.Project) {
				var r projectsReply
				if err := json.Unmarshal([]byte(body), &r); err != nil {
					t.Fatal(err)
				}
				want := []projectReply{
					{Project: chip8Key, LocalName: "chip8-emulator", State: "off", Label: "chip8-emulator"},
					{Project: folderKey, State: "off"},
				}
				if r.Date != time.Now().Format(time.DateOnly) || r.Linked || r.Online || len(r.Projects) != 2 || r.Projects[0] != want[0] || r.Projects[1] != want[1] {
					t.Errorf("GET /v1/projects = %s", body)
				}
				if !strings.Contains(body, `"code":""`) {
					t.Errorf("GET /v1/projects = %s; want each project's code, empty while it has none", body)
				}
			}}},
		},
		{
			name:   "a linked computer's projects have the codes the server gave them, and one it gave none has an empty code",
			config: "mode = \"online\"\n",
			linked: true,
			codes:  map[string]string{chip8Key: "7c41e9a"},
			steps: []step{{method: "GET", path: "/v1/projects", code: 200, check: func(t *testing.T, body string, _ map[string]config.Project) {
				var r projectsReply
				if err := json.Unmarshal([]byte(body), &r); err != nil {
					t.Fatal(err)
				}
				want := []projectReply{
					{Project: chip8Key, Code: "7c41e9a", LocalName: "chip8-emulator", State: "off", Label: "chip8-emulator"},
					{Project: folderKey, State: "off"},
				}
				if len(r.Projects) != 2 || r.Projects[0] != want[0] || r.Projects[1] != want[1] {
					t.Errorf("GET /v1/projects = %s", body)
				}
			}}},
		},
		{
			name:   "turning a name on answers the project with the code the server gave it",
			config: "mode = \"online\"\n",
			linked: true,
			codes:  map[string]string{chip8Key: "7c41e9"},
			steps: []step{{method: "PUT", path: "/v1/projects/" + chip8Key + "/name", body: `{"online":true}`, code: 200, check: func(t *testing.T, body string, _ map[string]config.Project) {
				var r projectReply
				if err := json.Unmarshal([]byte(body), &r); err != nil {
					t.Fatal(err)
				}
				if r != (projectReply{Project: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: "sending", Label: "chip8-emulator"}) {
					t.Errorf("PUT answered %s", body)
				}
			}}},
		},
		{
			name:   "a linked, online computer whose name the server shows says so",
			config: "mode = \"online\"\n[projects." + chip8Key + "]\nname = \"Chip-8\"\nonline = true\n",
			linked: true,
			shown:  map[string]string{chip8Key: "Chip-8"},
			steps: []step{{method: "GET", path: "/v1/projects", code: 200, check: func(t *testing.T, body string, _ map[string]config.Project) {
				var r projectsReply
				if err := json.Unmarshal([]byte(body), &r); err != nil {
					t.Fatal(err)
				}
				if !r.Linked || !r.Online || len(r.Projects) == 0 || r.Projects[0].State != "on" || r.Projects[0].Label != "Chip-8" {
					t.Errorf("GET /v1/projects = %s", body)
				}
			}}},
		},
		{
			name: "turning a name on writes config.toml with the project's own name, and answers the project, waiting to be sent",
			steps: []step{{method: "PUT", path: name, body: `{"online":true}`, code: 200, check: func(t *testing.T, body string, names map[string]config.Project) {
				if r := entry(t, body); r.State != "sending" || r.Label != "chip8-emulator" {
					t.Errorf("PUT answered %s", body)
				}
				if names[chip8Key] != (config.Project{Name: "chip8-emulator", Online: true}) {
					t.Errorf("config.toml has %+v", names)
				}
			}}},
		},
		{
			name:     "the switch on a computer whose project-names.json names another sign-in first does what login does: that sign-in's names go off with a note, the file names this one, and the name switched on goes",
			config:   "mode = \"online\"\n[projects." + folderKey + "]\nname = \"scratch\"\nonline = true\n",
			linked:   true,
			rawNames: `{"v":1,"server":"https://old.example.org","device":"d_old","confirmed":{"` + folderKey + `":"scratch"}}`,
			steps: []step{{method: "PUT", path: name, body: `{"online":true}`, code: 200, check: func(t *testing.T, body string, names map[string]config.Project) {
				if r := entry(t, body); r.State != "sending" {
					t.Errorf("PUT answered %s", body)
				}
				if names[chip8Key] != (config.Project{Name: "chip8-emulator", Online: true}) || names[folderKey] != (config.Project{Name: "scratch"}) {
					t.Errorf("config.toml has %+v, want chip8-emulator on and the old sign-in's name off", names)
				}
			}}},
			file: func(t *testing.T, n uploader.Names, err error) {
				if h := n.Hidden[folderKey]; err != nil || n.Server != namesServer || n.Device != namesDevice || h.Reason != uploader.NoteSignedIn || h.Pending {
					t.Errorf("project-names.json = %+v, %v; want this sign-in's, with a note of the old sign-in's name", n, err)
				}
			},
		},
		{
			name:     "the switch on a linked computer whose project-names.json names no sign-in writes this one into it, and turns nothing else off",
			config:   "mode = \"online\"\n[projects." + folderKey + "]\nname = \"scratch\"\nonline = true\n",
			linked:   true,
			rawNames: `{"v":1}`,
			steps: []step{{method: "PUT", path: name, body: `{"online":true}`, code: 200, check: func(t *testing.T, _ string, names map[string]config.Project) {
				if names[chip8Key] != (config.Project{Name: "chip8-emulator", Online: true}) || names[folderKey] != (config.Project{Name: "scratch", Online: true}) {
					t.Errorf("config.toml has %+v, want both names on", names)
				}
			}}},
			file: func(t *testing.T, n uploader.Names, err error) {
				if err != nil || n.Server != namesServer || n.Device != namesDevice || len(n.Hidden) != 0 {
					t.Errorf("project-names.json = %+v, %v; want this sign-in's, with no note", n, err)
				}
			},
		},
		{
			name: "a label given is cleaned as the server cleans a display name before it is saved",
			steps: []step{{method: "PUT", path: name, body: `{"online":true,"label":"  My \t chip  "}`, code: 200, check: func(t *testing.T, body string, names map[string]config.Project) {
				if r := entry(t, body); r.Label != "My chip" || names[chip8Key].Name != "My chip" {
					t.Errorf("PUT answered %s, and config.toml has %+v", body, names)
				}
			}}},
		},
		{
			name:   "turning a name off keeps its label in config.toml, so turning it on again shows it",
			config: "[projects." + chip8Key + "]\nname = \"Chip-8\"\nonline = true\n",
			steps: []step{
				{method: "PUT", path: name, body: `{"online":false}`, code: 200, check: func(t *testing.T, body string, names map[string]config.Project) {
					if r := entry(t, body); r.State != "off" || r.Label != "Chip-8" || names[chip8Key] != (config.Project{Name: "Chip-8"}) {
						t.Errorf("PUT answered %s, and config.toml has %+v", body, names)
					}
				}},
				{method: "PUT", path: name, body: `{"online":true}`, code: 200, check: func(t *testing.T, body string, names map[string]config.Project) {
					if names[chip8Key] != (config.Project{Name: "Chip-8", Online: true}) {
						t.Errorf("config.toml has %+v", names)
					}
				}},
			},
		},
		{
			name:  "a key no project has is 404",
			steps: []step{{method: "PUT", path: "/v1/projects/g0000000000000000000000000000000/name", body: `{"online":true}`, code: 404}},
		},
		{
			name: "a project with no name here can't be turned on without a label, and can with one",
			steps: []step{
				{method: "PUT", path: "/v1/projects/" + folderKey + "/name", body: `{"online":true}`, code: 422},
				{method: "PUT", path: "/v1/projects/" + folderKey + "/name", body: `{"online":true,"label":"scratch"}`, code: 200, check: func(t *testing.T, _ string, names map[string]config.Project) {
					if names[folderKey] != (config.Project{Name: "scratch", Online: true}) {
						t.Errorf("config.toml has %+v", names)
					}
				}},
			},
		},
		{
			name:  "a label that cleans to nothing is 422",
			steps: []step{{method: "PUT", path: name, body: `{"online":true,"label":" \t "}`, code: 422}},
		},
		{
			name: "a body that isn't a choice is 400",
			steps: []step{
				{method: "PUT", path: name, body: `{"label":"x"}`, code: 400},
				{method: "PUT", path: name, body: `{"online":"yes"}`, code: 400},
				{method: "PUT", path: name, body: `{"online":true,"public":true}`, code: 400},
			},
		},
		{
			name:   "turning on a name hidden on the dashboard that pairtime couldn't turn off in config.toml is 409, which says what to write first",
			config: "mode = \"online\"\n[projects." + chip8Key + "]\nname = \"Chip-8\"\nonline = true\n",
			linked: true,
			codes:  map[string]string{chip8Key: "7c41e9"},
			hidden: map[string]uploader.HiddenName{chip8Key: {Label: "Chip-8", At: 1, Pending: true}},
			steps: []step{{method: "PUT", path: name, body: `{"online":true}`, code: 409, check: func(t *testing.T, body string, names map[string]config.Project) {
				if !strings.Contains(body, "write it there by hand as a table of its own, [projects.<key>] with its name and online = false, and once pairtime projects stops saying pairtime couldn't turn it off, turn it on again") {
					t.Errorf("PUT answered %s", body)
				}
				if names[chip8Key] != (config.Project{Name: "Chip-8", Online: true}) {
					t.Errorf("config.toml has %+v", names)
				}
			}}},
		},
		{
			name:   "a config.toml pairtime can't change is 409, and stays as it was",
			config: "mode = \"local\"\n[unclosed\n",
			steps:  []step{{method: "PUT", path: name, body: `{"online":true}`, code: 409}},
		},
		{
			name:  "the name route takes no GET",
			steps: []step{{method: "GET", path: name, code: 405}},
		},
		{
			name: "a day's answer names a repo's project by its local name, and leaves a plain folder's unnamed",
			steps: []step{{method: "GET", path: "/v1/days/today", code: 200, check: func(t *testing.T, body string, _ map[string]config.Project) {
				var d struct {
					Projects []struct {
						Project string `json:"project"`
						Name    string `json:"name"`
					} `json:"projects"`
				}
				if err := json.Unmarshal([]byte(body), &d); err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for _, p := range d.Projects {
					got[p.Project] = p.Name
				}
				if len(got) != 2 || got[chip8Key] != "chip8-emulator" || got[folderKey] != "" {
					t.Errorf("GET /v1/days/today named %v", got)
				}
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfgFile, dataDir string
			a := startWith(t, tt.config, nil, func(dirs home.Dirs) {
				projectsDir(t, dirs, tt.linked, tt.shown, tt.codes, tt.hidden)
				if tt.rawNames != "" {
					if err := os.WriteFile(filepath.Join(dirs.Data, uploader.NamesFileName), []byte(tt.rawNames), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				cfgFile, dataDir = dirs.ConfigFile(), dirs.Data
			})
			before, _ := os.ReadFile(cfgFile)
			for _, s := range tt.steps {
				code, body := send(t, a, s.method, s.path, s.body)
				if code != s.code {
					t.Fatalf("%s %s %s = %d %s, want %d", s.method, s.path, s.body, code, body, s.code)
				}
				if s.check != nil {
					cfg, _, _ := config.Load(cfgFile)
					s.check(t, body, cfg.Projects)
				}
			}
			if tt.config != "" && strings.Contains(tt.config, "[unclosed") {
				if after, _ := os.ReadFile(cfgFile); string(after) != string(before) {
					t.Errorf("config.toml changed to %q", after)
				}
			}
			if tt.file != nil {
				n, err := uploader.ReadNames(dataDir)
				tt.file(t, n, err)
			}
		})
	}
}
