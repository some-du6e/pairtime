// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Made-up project keys in the git adapter's form, and the sign-in of the
// computer in the tests.
const (
	chip8Key    = "g7c41e9a2d05b8f63e1a94c7d2b6f0e85"
	plantKey    = "g5be03d7f18a6c92e4b07d5a1c8f3e962"
	folderKey   = "dd2a8f61c94e07b35a8c1f6e29d4b7a03"
	namesServer = "https://pairtime.example.org"
	namesDevice = "d_example"
)

// serverCodes are the codes the server gave the keys of the computer in
// the tests, as its names answer gives them.
var serverCodes = map[string]string{chip8Key: "7c41e9", plantKey: "5be03d", folderKey: "d2a8f6"}

// projectsHome is a PAIRTIME_HOME with what pairtime projects reads.
type projectsHome struct {
	config string            // config.toml; "" for none
	linked bool              // account.json says the computer is linked
	shown  map[string]string // the names the server last confirmed
	codes  map[string]string // the codes the server last gave
	hidden map[string]uploader.HiddenName
	// folder adds a project of today's that the project map doesn't keep.
	folder bool
}

// write fills a home for a test on 2026-11-22: a project map with a repo
// and a bare repo, and today's rollup, where chip8-emulator has an hour of
// your time and ten minutes of Claude's.
func (p projectsHome) write(t *testing.T) projectsDir {
	t.Helper()
	h := newHome(t)
	onDay(t, "2026-11-22", time.UTC)
	dirs := h.dirs()
	m := git.ProjectMap{Repos: []git.RepoProject{
		{CommonDir: filepath.Join(h.dir, "code", "chip8-emulator", ".git"), Key: chip8Key, Kind: git.KindRepo},
		{CommonDir: filepath.Join(h.dir, "srv", "plant-sensor.git"), Key: plantKey, Kind: git.KindRepo},
	}}
	st, err := json.Marshal(map[string]any{"v": 1, "projects": m})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.StateFile(), st, 0o600); err != nil {
		t.Fatal(err)
	}
	start, end, err := store.DayBounds("2026-11-22", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	day := store.Day{Date: "2026-11-22", Zone: "UTC", Start: start, End: end, Projects: []store.DayProject{{Project: chip8Key, Tally: store.Tally{
		CreditedS: 3600, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 3600},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceA: 3600},
		AgentOnlyS:         600, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 600},
		Uncredited: map[event.Reason]float64{},
	}}}}
	if p.folder {
		day.Projects = append(day.Projects, store.DayProject{Project: folderKey, Tally: store.Tally{Uncredited: map[event.Reason]float64{}}})
	}
	if err := store.WriteDay(dirs.Days(), day); err != nil {
		t.Fatal(err)
	}
	if p.config != "" {
		if err := os.WriteFile(dirs.ConfigFile(), []byte(p.config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if p.linked {
		l := `{"v":1,"server":"` + namesServer + `","device_id":"` + namesDevice + `","public_key":"AAAA","linked_at":"2026-11-01T00:00:00Z"}`
		if err := os.WriteFile(account.File(dirs.Data), []byte(l), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if p.shown != nil || p.codes != nil || p.hidden != nil {
		n, err := json.Marshal(uploader.Names{V: 1, Server: namesServer, Device: namesDevice, Confirmed: p.shown, Codes: p.codes, AnsweredAt: 1, Hidden: p.hidden})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dirs.Data, uploader.NamesFileName), n, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return projectsDir{fakeHome: h, cfg: dirs.ConfigFile()}
}

// projectsDir is a fakeHome with its config.toml's path.
type projectsDir struct {
	fakeHome
	cfg string
}

// names reads the project tables of the home's config.toml.
func (h projectsDir) names(t *testing.T) map[string]config.Project {
	t.Helper()
	cfg, problems, err := config.Load(h.cfg)
	if err != nil || len(problems) != 0 {
		t.Fatalf("config.toml: %v %q", err, problems)
	}
	return cfg.Projects
}

// online is a config.toml that lets the computer upload.
const online = "mode = \"online\"\n"

// chip8Table is chip8-emulator's table in config.toml.
func chip8Table(label string, on bool) string {
	return "[projects." + chip8Key + "]\nname = \"" + label + "\"\nonline = " + strconv.FormatBool(on) + "\n"
}

func TestProjectsList(t *testing.T) {
	tests := []struct {
		name    string
		home    projectsHome
		want    []string // what stdout holds, each in order
		notWant []string
	}{
		{
			name: "each project shows its local name and the code the server gave it, its time today in pairtime today's words, and its name on the dashboard",
			home: projectsHome{config: online + chip8Table("chip8-emulator", true), linked: true, shown: map[string]string{chip8Key: "chip8-emulator"}, codes: serverCodes},
			want: []string{
				"chip8-emulator (Project 7c41e9)\n  Today: you 1h · Claude 10m\n  On your dashboard: shows the name \"chip8-emulator\"\n",
				"\nplant-sensor (Project 5be03d)\n  Today: you 0m\n  On your dashboard: Project 5be03d; show-name shows it as \"plant-sensor\"\n",
				"\npairtime projects show-name <code> shows a project's name on your own pairtime dashboard, where only you see it, and hide-name takes it off.\n",
			},
			notWant: []string{"local-only", "isn't linked", "listed by its key", plantKey},
		},
		{
			name: "a project the server gave no code yet is listed by its whole key, and a note says when it gets one",
			home: projectsHome{config: online, linked: true, codes: map[string]string{chip8Key: "7c41e9"}},
			want: []string{
				"chip8-emulator (Project 7c41e9)\n",
				"\nplant-sensor (" + plantKey + ")\n  Today: you 0m\n  On your dashboard: no code yet; show-name shows it as \"plant-sensor\"\n",
				"\nA project listed by its key has no code yet: your dashboard gives it one once the project's first minutes reach the server, and pairtime reads it back within a day, or when the agent next starts. show-name and hide-name take the key meanwhile.\n",
				"pairtime projects show-name <code> shows",
			},
		},
		{
			name: "a project with no local name and no code yet is listed by its whole key alone",
			home: projectsHome{config: online, linked: true, folder: true, codes: map[string]string{chip8Key: "7c41e9", plantKey: "5be03d"}},
			want: []string{"\n" + folderKey + "\n  Today: you 0m\n  On your dashboard: no code yet; it has no name on this computer, so show-name needs one\n"},
		},
		{
			name: "a computer that never read its codes lists every project by its key",
			home: projectsHome{config: online, linked: true},
			want: []string{"chip8-emulator (" + chip8Key + ")\n", "plant-sensor (" + plantKey + ")\n", "A project listed by its key has no code yet"},
		},
		{
			name: "a name turned off without a code says the dashboard calls the project by its code after the next upload",
			home: projectsHome{config: online + chip8Table("chip8-emulator", false), linked: true, shown: map[string]string{chip8Key: "chip8-emulator"}},
			want: []string{"  On your dashboard: shows its name until pairtime uploads the change, then its code\n"},
		},
		{
			name: "a name on that the server hasn't shown yet waits for the next upload",
			home: projectsHome{config: online + chip8Table("Chip-8", true), linked: true, shown: map[string]string{chip8Key: "chip8-emulator"}},
			want: []string{"  On your dashboard: shows the name \"Chip-8\" once pairtime uploads it\n"},
		},
		{
			name: "a name turned off that the server still shows says it goes with the next upload",
			home: projectsHome{config: online + chip8Table("chip8-emulator", false), linked: true, shown: map[string]string{chip8Key: "chip8-emulator"}, codes: serverCodes},
			want: []string{"  On your dashboard: shows its name until pairtime uploads the change, then Project 7c41e9\n"},
		},
		{
			name: "a name hidden on the dashboard was turned off here, and says when",
			home: projectsHome{config: online + chip8Table("chip8-emulator", false), linked: true, codes: serverCodes,
				hidden: map[string]uploader.HiddenName{chip8Key: {Label: "chip8-emulator", At: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC).UnixMilli()}}},
			want: []string{"  On your dashboard: Project 7c41e9, since you hid its name there on Saturday, November 21; pairtime turned it off here, and show-name shows it again\n"},
		},
		{
			name: "a project the map doesn't keep has no name here, and show-name needs one",
			home: projectsHome{config: online, linked: true, folder: true, codes: serverCodes},
			want: []string{"Project d2a8f6\n  Today: you 0m\n  On your dashboard: Project d2a8f6; it has no name on this computer, so show-name needs one\n"},
		},
		{
			name: "a computer that isn't linked says no name goes to the dashboard",
			home: projectsHome{config: chip8Table("chip8-emulator", true)},
			want: []string{"This computer isn't linked to a pairtime server, so no name goes to your dashboard until pairtime login links it.\n"},
		},
		{
			name: "a local-only computer says no name goes to the dashboard",
			home: projectsHome{config: "mode = \"local\"\n", linked: true},
			want: []string{"This computer is local-only, as config.toml says, so no name goes to your dashboard until it's online again.\n"},
		},
		{
			name: "a config.toml with an error turns every name off, and says so",
			home: projectsHome{config: online + chip8Table("chip8-emulator", true) + "[unclosed\n", linked: true, codes: serverCodes},
			want: []string{"On your dashboard: Project 7c41e9; show-name shows it as \"chip8-emulator\"\n", "config.toml has an error, so every name is off until it's fixed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.home.write(t)
			code, out, errOut := runCmd("projects")
			if code != 0 {
				t.Fatalf("pairtime projects = %d, %s", code, errOut)
			}
			at := 0
			for _, w := range tt.want {
				i := strings.Index(out[at:], w)
				if i < 0 {
					t.Fatalf("pairtime projects printed\n%s\nwant, after what came before, %q", out, w)
				}
				at += i + len(w)
			}
			for _, w := range tt.notWant {
				if strings.Contains(out, w) {
					t.Errorf("pairtime projects printed %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestProjectsNoneYet(t *testing.T) {
	newHome(t)
	onDay(t, "2026-11-22", time.UTC)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "with no project known the list says how one shows up", args: []string{"projects"}, want: "pairtime knows no projects on this computer yet. A project shows up here once you work on it with an AI agent.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := runCmd(tt.args...)
			if code != 0 || out != tt.want {
				t.Errorf("pairtime %s = %d, %q, %q; want 0 and %q", strings.Join(tt.args, " "), code, out, errOut, tt.want)
			}
		})
	}
}

func TestProjectsNames(t *testing.T) {
	tests := []struct {
		name     string
		home     projectsHome
		args     []string
		wantCode int
		wantOut  string // a substring of stdout
		wantErr  string // a substring of stderr
		want     map[string]config.Project
	}{
		{
			name:     "show-name with the server's code prints the local name before it saves it, says who sees it, and turns the name on in config.toml",
			home:     projectsHome{config: online, linked: true, codes: serverCodes},
			args:     []string{"projects", "show-name", "5be03d"},
			wantOut:  "Your pairtime dashboard will show Project 5be03d as \"plant-sensor\". Only you see it there, on your own dashboard while you're signed in, and nothing else about the project leaves this computer with it.\nSaved in config.toml. pairtime sends it with its next upload.\n",
			want:     map[string]config.Project{plantKey: {Name: "plant-sensor", Online: true}},
			wantCode: 0,
		},
		{
			name:    "show-name takes Project and the code as two words, and a label of several, cleaned",
			home:    projectsHome{config: online, linked: true, codes: serverCodes},
			args:    []string{"projects", "show-name", "Project", "5be03d", "My", "plant ", " sensor"},
			wantOut: "Your pairtime dashboard will show Project 5be03d as \"My plant sensor\".",
			want:    map[string]config.Project{plantKey: {Name: "My plant sensor", Online: true}},
		},
		{
			name:    "show-name takes Project and the code as one word",
			home:    projectsHome{config: online, linked: true, codes: serverCodes},
			args:    []string{"projects", "show-name", "Project 5be03d"},
			wantOut: "Your pairtime dashboard will show Project 5be03d as \"plant-sensor\".",
			want:    map[string]config.Project{plantKey: {Name: "plant-sensor", Online: true}},
		},
		{
			name:    "show-name takes the project's whole key, which names a project with a code too",
			home:    projectsHome{config: online, linked: true, codes: serverCodes},
			args:    []string{"projects", "show-name", plantKey},
			wantOut: "Your pairtime dashboard will show Project 5be03d as \"plant-sensor\".",
			want:    map[string]config.Project{plantKey: {Name: "plant-sensor", Online: true}},
		},
		{
			name:    "show-name takes the whole key of a project the server gave no code yet, and calls it by its key",
			home:    projectsHome{config: online, linked: true},
			args:    []string{"projects", "show-name", plantKey},
			wantOut: "Your pairtime dashboard will show the project " + plantKey + " as \"plant-sensor\".",
			want:    map[string]config.Project{plantKey: {Name: "plant-sensor", Online: true}},
		},
		{
			name:    "show-name without a label shows the label config.toml kept from before",
			home:    projectsHome{config: online + chip8Table("Chip-8", false), linked: true, codes: serverCodes},
			args:    []string{"projects", "show-name", "Project 7c41e9"},
			wantOut: "as \"Chip-8\"",
			want:    map[string]config.Project{chip8Key: {Name: "Chip-8", Online: true}},
		},
		{
			name:    "on a computer that isn't linked show-name saves the name, and says it goes once the computer is",
			home:    projectsHome{},
			args:    []string{"projects", "show-name", chip8Key},
			wantOut: "Saved in config.toml. This computer isn't linked to a pairtime server",
			want:    map[string]config.Project{chip8Key: {Name: "chip8-emulator", Online: true}},
		},
		{
			name:     "show-name refuses a code no project has, and points at the whole key",
			home:     projectsHome{config: online, linked: true, codes: serverCodes},
			args:     []string{"projects", "show-name", "abcdef"},
			wantCode: 1,
			wantErr:  "pairtime projects: no project on this computer has the code \"abcdef\"; pairtime knows only the codes the server gave it, so give the project's whole key, as pairtime projects lists it\n",
		},
		{
			name:     "show-name refuses a code the server didn't give, though the project's key starts with it",
			home:     projectsHome{config: online, linked: true},
			args:     []string{"projects", "show-name", "5be03d"},
			wantCode: 1,
			wantErr:  "no project on this computer has the code \"5be03d\"; pairtime knows only the codes the server gave it, so give the project's whole key",
		},
		{
			name:     "show-name refuses the dashboard's words for a code the server didn't give",
			home:     projectsHome{config: online, linked: true},
			args:     []string{"projects", "show-name", "Project", "5be03d"},
			wantCode: 1,
			wantErr:  "no project on this computer has the code \"Project 5be03d\"",
		},
		{
			name:     "while the computer isn't linked, no code it kept names a project",
			home:     projectsHome{config: online, codes: serverCodes},
			args:     []string{"projects", "show-name", "5be03d"},
			wantCode: 1,
			wantErr:  "no project on this computer has the code \"5be03d\"",
		},
		{
			name:     "show-name refuses a project with no name here when no label is given",
			home:     projectsHome{config: online, linked: true, folder: true, codes: serverCodes},
			args:     []string{"projects", "show-name", "d2a8f6"},
			wantCode: 1,
			wantErr:  "Project d2a8f6 has no name on this computer, so say what your dashboard shows: pairtime projects show-name d2a8f6 \"a name\"",
		},
		{
			name:     "a project with no name here and no code yet says to give its whole key and a name",
			home:     projectsHome{config: online, linked: true, folder: true},
			args:     []string{"projects", "show-name", folderKey},
			wantCode: 1,
			wantErr:  "the project " + folderKey + " has no name on this computer, so say what your dashboard shows: pairtime projects show-name " + folderKey + " \"a name\"",
		},
		{
			name:     "show-name refuses a label that cleans to nothing",
			home:     projectsHome{config: online, linked: true, codes: serverCodes},
			args:     []string{"projects", "show-name", "5be03d", " \t "},
			wantCode: 1,
			wantErr:  "that name is empty once its spaces and invisible characters are taken out",
		},
		{
			name:     "show-name refuses to change a config.toml that doesn't parse, and says what to write",
			home:     projectsHome{config: online + "[unclosed\n", linked: true, codes: serverCodes},
			args:     []string{"projects", "show-name", "5be03d"},
			wantCode: 1,
			wantErr:  "set name = \"plant-sensor\" and online = true under [projects." + plantKey + "]",
		},
		{
			name:    "hide-name turns a shown name off and keeps its label",
			home:    projectsHome{config: online + chip8Table("chip8-emulator", true), linked: true, shown: map[string]string{chip8Key: "chip8-emulator"}, codes: serverCodes},
			args:    []string{"projects", "hide-name", "7c41e9"},
			wantOut: "Turned off in config.toml. Your pairtime dashboard calls it Project 7c41e9 again once pairtime uploads the change.\n",
			want:    map[string]config.Project{chip8Key: {Name: "chip8-emulator"}},
		},
		{
			name:    "hide-name takes the whole key of a project without a code, and says the dashboard calls it by its code",
			home:    projectsHome{config: online + chip8Table("chip8-emulator", true), linked: true, shown: map[string]string{chip8Key: "chip8-emulator"}},
			args:    []string{"projects", "hide-name", chip8Key},
			wantOut: "Turned off in config.toml. Your pairtime dashboard calls it by its code again once pairtime uploads the change.\n",
			want:    map[string]config.Project{chip8Key: {Name: "chip8-emulator"}},
		},
		{
			name:     "hide-name refuses a code the server didn't give",
			home:     projectsHome{config: online + chip8Table("chip8-emulator", true), linked: true},
			args:     []string{"projects", "hide-name", "7c41e9"},
			wantCode: 1,
			wantErr:  "no project on this computer has the code \"7c41e9\"; pairtime knows only the codes the server gave it, so give the project's whole key",
		},
		{
			name:    "hide-name on a name that is off already changes nothing",
			home:    projectsHome{config: online, linked: true, codes: serverCodes},
			args:    []string{"projects", "hide-name", "7c41e9"},
			wantOut: "Its name was off already. Your pairtime dashboard calls it Project 7c41e9.\n",
		},
		{
			name:     "a subcommand pairtime projects doesn't have is a usage error",
			home:     projectsHome{config: online},
			args:     []string{"projects", "rename", "7c41e9"},
			wantCode: 2,
			wantErr:  "Usage: pairtime projects",
		},
		{
			name:     "show-name without a code is a usage error",
			home:     projectsHome{config: online},
			args:     []string{"projects", "show-name"},
			wantCode: 2,
			wantErr:  "Usage: pairtime projects",
		},
		{
			name:     "hide-name with more than a code is a usage error",
			home:     projectsHome{config: online},
			args:     []string{"projects", "hide-name", "7c41e9", "now"},
			wantCode: 2,
			wantErr:  "Usage: pairtime projects",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.home.write(t)
			code, out, errOut := runCmd(tt.args...)
			if code != tt.wantCode || !strings.Contains(out, tt.wantOut) || !strings.Contains(errOut, tt.wantErr) {
				t.Fatalf("pairtime %s = %d\nstdout %q\nstderr %q\nwant %d, stdout with %q, stderr with %q", strings.Join(tt.args, " "), code, out, errOut, tt.wantCode, tt.wantOut, tt.wantErr)
			}
			if tt.wantCode != 0 {
				return
			}
			if got := h.names(t); !equalProjects(got, tt.want) {
				t.Errorf("config.toml's projects are %+v, want %+v", got, tt.want)
			}
		})
	}
}

// equalProjects reports whether a and b hold the same projects; nil and
// empty are the same.
func equalProjects(a, b map[string]config.Project) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestTodayLocalNames(t *testing.T) {
	tests := []struct {
		name string
		home projectsHome
		want string
	}{
		{
			name: "pairtime today calls a repo's project by the name pairtime shows for it here, its folder's",
			home: projectsHome{config: online},
			want: "\nchip8-emulator: you 1h · Claude 10m\n",
		},
		{
			name: "a project the project map doesn't keep is still called by its key",
			home: projectsHome{config: online, folder: true},
			want: "\n" + folderKey + ": you 0m\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.home.write(t)
			code, out, errOut := runCmd("today")
			if code != 0 || !strings.Contains(out, tt.want) {
				t.Errorf("pairtime today = %d\n%s\n%s\nwant it to hold %q", code, out, errOut, tt.want)
			}
		})
	}
}

func TestUninstallKnowsNamesFile(t *testing.T) {
	tests := []struct {
		name, file string
	}{
		{name: "uninstall deletes the project names the server showed, through a linked data directory too", file: uploader.NamesFileName},
		{name: "and the temporary file of an atomic write of it", file: "." + uploader.NamesFileName + ".tmp-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !isPairtimeEntry(tt.file) {
				t.Errorf("%s isn't one of pairtimeEntries", tt.file)
			}
		})
	}
}
