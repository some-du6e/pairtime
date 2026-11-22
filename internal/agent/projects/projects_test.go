// SPDX-License-Identifier: AGPL-3.0-or-later

package projects

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

// Made-up project keys in the git adapter's form, and the sign-in the
// names are confirmed for.
const (
	chip8Key  = "g7c41e9a2d05b8f63e1a94c7d2b6f0e85"
	plantKey  = "g5be03d7f18a6c92e4b07d5a1c8f3e962"
	folderKey = "dd2a8f61c94e07b35a8c1f6e29d4b7a03"
	server    = "https://pairtime.example.org"
	device    = "d_example"
)

func TestLocalName(t *testing.T) {
	tests := []struct {
		name, commonDir, want string
	}{
		{name: "a repo's main checkout is the folder that holds .git", commonDir: filepath.Join("/home", "sam", "code", "chip8-emulator", ".git"), want: "chip8-emulator"},
		{name: "a bare repo is its own folder, without .git", commonDir: filepath.Join("/srv", "git", "plant-sensor.git"), want: "plant-sensor"},
		{name: "a git directory kept apart is its own folder", commonDir: filepath.Join("/srv", "meta", "chip8-meta"), want: "chip8-meta"},
		{name: "a submodule is the folder git keeps its directory in", commonDir: filepath.Join("/home", "sam", "code", "chip8-emulator", ".git", "modules", "roms"), want: "roms"},
		{name: "a trailing separator changes nothing", commonDir: filepath.Join("/home", "sam", "chip8-emulator", ".git") + string(filepath.Separator), want: "chip8-emulator"},
		{name: "a folder's spaces stay, and a run of them becomes one", commonDir: filepath.Join("/home", "sam", "My  Chip-8", ".git"), want: "My Chip-8"},
		{name: "a folder's control characters become a space, so no name hides another", commonDir: filepath.Join("/home", "sam", "chip8\temu", ".git"), want: "chip8 emu"},
		{name: "a .git at the root of the disk has no folder to name it", commonDir: filepath.Join(string(filepath.Separator), ".git"), want: ""},
		{name: "no common directory has no name", commonDir: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocalName(tt.commonDir); got != tt.want {
				t.Errorf("LocalName(%q) = %q, want %q", tt.commonDir, got, tt.want)
			}
		})
	}
}

// gitRun runs git in dir with a setup of the test's own, so nothing from
// the machine's git config gets in.
func gitRun(t *testing.T, env []string, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitSetup is a home and a global git config of the test's own, as
// internal/adapters/git's tests use.
func gitSetup(t *testing.T) []string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[init]\n\tdefaultBranch = main\n[gc]\n\tauto = 0\n[core]\n\tautocrlf = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"GIT_CONFIG_GLOBAL=" + cfg, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Rowan Example", "GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Rowan Example", "GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_TERMINAL_PROMPT=0",
	}
}

func TestLocalNamesOfRepos(t *testing.T) {
	env := gitSetup(t)
	root := t.TempDir()
	// A repo with a commit, a worktree beside it in a folder of its own,
	// and a bare clone with a worktree of its own.
	main := filepath.Join(root, "chip8-emulator")
	worktree := filepath.Join(root, "chip8-wt-sound")
	bare := filepath.Join(root, "plant-sensor.git")
	checkout := filepath.Join(root, "plant-checkout")
	if err := os.Mkdir(main, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, env, main, "init", "-q")
	gitRun(t, env, main, "commit", "-q", "--allow-empty", "-m", "first")
	gitRun(t, env, main, "worktree", "add", "-q", "-b", "sound", worktree)
	gitRun(t, env, root, "clone", "-q", "--bare", main, bare)
	gitRun(t, env, bare, "worktree", "add", "-q", "-b", "plant-work", checkout)

	tests := []struct {
		name string
		dirs []string // the folders sessions worked in, resolved in this order
		want string
		not  string
	}{
		{name: "a repo's main checkout is named by its folder", dirs: []string{main}, want: "chip8-emulator"},
		{name: "a worktree is named by the main checkout's folder, never its own", dirs: []string{worktree}, want: "chip8-emulator", not: "chip8-wt-sound"},
		{name: "a repo seen from its worktree and its main checkout has one name", dirs: []string{worktree, main}, want: "chip8-emulator", not: "chip8-wt-sound"},
		{name: "a bare repo's worktree is named by the bare repo's folder, without .git", dirs: []string{checkout}, want: "plant-sensor", not: "plant-checkout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := git.NewResolver(git.ResolverOptions{Git: &git.Runner{Env: env}, Secret: bytes.Repeat([]byte{7}, git.SecretLen)}, git.ProjectMap{})
			if err != nil {
				t.Fatal(err)
			}
			var key string
			for _, dir := range tt.dirs {
				loc, err := res.Resolve(context.Background(), dir)
				if err != nil {
					t.Fatal(err)
				}
				if key != "" && loc.Project != key {
					t.Fatalf("%s resolved to another project, %s, than %s", dir, loc.Project, key)
				}
				key = loc.Project
			}
			names := LocalNames(res.Map())
			if got := names[key]; got != tt.want {
				t.Errorf("the project's local name is %q, want %q (names %v)", got, tt.want, names)
			}
			for k, n := range names {
				if tt.not != "" && n == tt.not {
					t.Errorf("%s is named after the folder %q", k, n)
				}
				if strings.ContainsAny(n, `/\`) {
					t.Errorf("%s's name %q is a path", k, n)
				}
			}
		})
	}
}

func TestLocalNames(t *testing.T) {
	m := git.ProjectMap{Repos: []git.RepoProject{
		{CommonDir: filepath.Join("/b", "chip8-copy", ".git"), Key: chip8Key, Kind: git.KindRepo},
		{CommonDir: filepath.Join("/a", "chip8-emulator", ".git"), Key: chip8Key, Kind: git.KindRepo, Aliases: []string{folderKey}},
		{CommonDir: filepath.Join(string(filepath.Separator), ".git"), Key: plantKey, Kind: git.KindRepo},
	}}
	want := map[string]string{chip8Key: "chip8-emulator", folderKey: "chip8-emulator"}
	tests := []struct {
		name string
		key  string
	}{
		{name: "of repos that share a key, as a clone shares its original's, the first by common directory names it", key: chip8Key},
		{name: "a key the project had before its first commit has its name too", key: folderKey},
		{name: "a repo whose folder has no name gives its key none", key: plantKey},
	}
	got := LocalNames(m)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got[tt.key] != want[tt.key] {
				t.Errorf("LocalNames()[%s] = %q, want %q", tt.key, got[tt.key], want[tt.key])
			}
		})
	}
}

func TestReadMap(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		file    string // "" means none
		want    git.ProjectMap
		wantErr bool
	}{
		{name: "a missing state.json is an empty map", want: git.ProjectMap{}},
		{name: "a state.json without a map is an empty map", file: `{"v":1,"agent":{}}`, want: git.ProjectMap{}},
		{
			name: "the map is read from its key, the agent's git host's",
			file: `{"v":1,"projects":{"repos":[{"common_dir":"/a/chip8-emulator/.git","key":"` + chip8Key + `","kind":"repo"}]}}`,
			want: git.ProjectMap{Repos: []git.RepoProject{{CommonDir: "/a/chip8-emulator/.git", Key: chip8Key, Kind: git.KindRepo}}},
		},
		{name: "a state.json that isn't JSON is an error", file: `{"v":1,`, wantErr: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(dir, "state"+string(rune('a'+i))+".json")
			if tt.file != "" {
				if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ReadMap(name)
			if (err != nil) != tt.wantErr || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReadMap = %+v, %v; want %+v, an error %v", got, err, tt.want, tt.wantErr)
			}
			if tt.file != "" {
				after, _ := os.ReadFile(name)
				if string(after) != tt.file {
					t.Error("ReadMap changed state.json")
				}
			}
		})
	}
}

func TestDayKeys(t *testing.T) {
	dir := t.TempDir()
	put := func(date string, keys ...string) {
		d := store.Day{Date: date, Zone: "UTC"}
		for _, k := range keys {
			d.Projects = append(d.Projects, store.DayProject{Project: k})
		}
		if err := store.WriteDay(dir, d); err != nil {
			t.Fatal(err)
		}
	}
	put("2026-11-22", chip8Key)
	put("2026-11-20", plantKey, chip8Key)
	put("2026-10-01", folderKey)
	if err := os.WriteFile(store.DayFile(dir, "2026-11-21"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := DayKeys(dir, Dates("2026-11-22", Days))
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "the projects of the last 30 days come once each, in the order the days give them", ok: slices.Equal(keys, []string{chip8Key, plantKey})},
		{name: "a day older than 30 days adds none", ok: !slices.Contains(keys, folderKey)},
		{name: "a rollup that doesn't read is the error, and the other days still count", ok: err != nil && strings.Contains(err.Error(), "2026-11-21")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("DayKeys = %v, %v", keys, err)
			}
		})
	}
}

func TestDates(t *testing.T) {
	tests := []struct {
		name  string
		today string
		n     int
		want  []string
	}{
		{name: "the days run back from today, across a month's end", today: "2026-12-01", n: 3, want: []string{"2026-12-01", "2026-11-30", "2026-11-29"}},
		{name: "a today that isn't a date has none", today: "today", n: 3, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Dates(tt.today, tt.n); !slices.Equal(got, tt.want) {
				t.Errorf("Dates = %v, want %v", got, tt.want)
			}
		})
	}
}

// project returns the project key of list, and false when it isn't there.
func project(list []Project, key string) (Project, bool) {
	for _, p := range list {
		if p.Key == key {
			return p, true
		}
	}
	return Project{}, false
}

func TestList(t *testing.T) {
	repos := git.ProjectMap{Repos: []git.RepoProject{{CommonDir: filepath.Join("/a", "chip8-emulator", ".git"), Key: chip8Key, Kind: git.KindRepo}}}
	on := func(label string) config.Config {
		return config.Config{Projects: map[string]config.Project{chip8Key: {Name: label, Online: true}}}
	}
	off := func(label string) config.Config {
		return config.Config{Projects: map[string]config.Project{chip8Key: {Name: label}}}
	}
	shows := func(names map[string]string) uploader.Names {
		return uploader.Names{V: 1, Server: server, Device: device, Confirmed: names}
	}
	hiddenAt := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		in        Input
		want      Project
		wantFound bool
	}{
		{
			name:      "a project of the map is listed with its local name and code, its name off, and the local name as the label it would show",
			in:        Input{Map: repos},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Off, Label: "chip8-emulator"},
			wantFound: true,
		},
		{
			name:      "a name off keeps the label config.toml kept as the one it would show",
			in:        Input{Map: repos, Config: off("Chip-8")},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Off, Label: "Chip-8"},
			wantFound: true,
		},
		{
			name:      "a name on that the server shows with its label is on",
			in:        Input{Map: repos, Config: on("Chip-8"), Names: shows(map[string]string{chip8Key: "Chip-8"}), Server: server, Device: device},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: On, Label: "Chip-8"},
			wantFound: true,
		},
		{
			name:      "a name on that the server shows with another label is still being sent",
			in:        Input{Map: repos, Config: on("Chip-8"), Names: shows(map[string]string{chip8Key: "chip8-emulator"}), Server: server, Device: device},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Sending, Label: "Chip-8"},
			wantFound: true,
		},
		{
			name:      "a name on while the computer isn't linked waits to be sent",
			in:        Input{Map: repos, Config: on("Chip-8"), Names: shows(map[string]string{chip8Key: "Chip-8"})},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Sending, Label: "Chip-8"},
			wantFound: true,
		},
		{
			name:      "names another sign-in confirmed count for nothing",
			in:        Input{Map: repos, Config: on("Chip-8"), Names: shows(map[string]string{chip8Key: "Chip-8"}), Server: "https://other.example.org", Device: device},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Sending, Label: "Chip-8"},
			wantFound: true,
		},
		{
			name:      "a name off that the server still shows is being removed",
			in:        Input{Map: repos, Config: off("Chip-8"), Names: shows(map[string]string{chip8Key: "Chip-8"}), Server: server, Device: device},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Removing, Label: "Chip-8"},
			wantFound: true,
		},
		{
			name: "a name hidden on the dashboard is hidden, with when, and the label it showed",
			in: Input{Map: repos, Config: off("Chip-8"), Server: server, Device: device,
				Names: uploader.Names{V: 1, Server: server, Device: device, Hidden: map[string]uploader.HiddenName{chip8Key: {Label: "Chip-8", At: hiddenAt.UnixMilli()}}}},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Hidden, Label: "Chip-8", HiddenAt: hiddenAt},
			wantFound: true,
		},
		{
			name: "a hidden name turned on again is being sent",
			in: Input{Map: repos, Config: on("Chip-8"), Server: server, Device: device,
				Names: uploader.Names{V: 1, Server: server, Device: device, Hidden: map[string]uploader.HiddenName{chip8Key: {Label: "Chip-8", At: hiddenAt.UnixMilli()}}}},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Sending, Label: "Chip-8"},
			wantFound: true,
		},
		{
			name: "a hidden name config.toml couldn't be changed for is hidden, though config.toml still has it on",
			in: Input{Map: repos, Config: on("Chip-8"), Server: server, Device: device,
				Names: uploader.Names{V: 1, Server: server, Device: device, Hidden: map[string]uploader.HiddenName{chip8Key: {Label: "Chip-8", At: hiddenAt.UnixMilli(), Pending: true}}}},
			want:      Project{Key: chip8Key, Code: "7c41e9", LocalName: "chip8-emulator", State: Hidden, Label: "Chip-8", HiddenAt: hiddenAt},
			wantFound: true,
		},
		{
			name:      "a project of the last 30 days that the map doesn't keep, as a plain folder, is listed without a local name or a label",
			in:        Input{Days: []string{folderKey}},
			want:      Project{Key: folderKey, Code: "d2a8f6", State: Off},
			wantFound: true,
		},
		{
			name:      "a project config.toml names is listed, so its name can be turned off",
			in:        Input{Config: config.Config{Projects: map[string]config.Project{plantKey: {Name: "plant-sensor", Online: true}}}},
			want:      Project{Key: plantKey, Code: "5be03d", State: Sending, Label: "plant-sensor"},
			wantFound: true,
		},
		{
			name:      "a project only the server's names have is listed, so its name can go",
			in:        Input{Names: shows(map[string]string{plantKey: "plant-sensor"}), Server: server, Device: device},
			want:      Project{Key: plantKey, Code: "5be03d", State: Removing},
			wantFound: true,
		},
		{
			name: "a code is long enough to tell keys apart, old keys of the map's projects included",
			in: Input{Map: git.ProjectMap{Repos: []git.RepoProject{{CommonDir: filepath.Join("/a", "chip8-emulator", ".git"), Key: chip8Key, Kind: git.KindRepo,
				Aliases: []string{"g7c41e9b2d05b8f63e1a94c7d2b6f0e85"}}}}},
			want:      Project{Key: chip8Key, Code: "7c41e9a", LocalName: "chip8-emulator", State: Off, Label: "chip8-emulator"},
			wantFound: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := project(List(tt.in), tt.want.Key)
			if !got.HiddenAt.Equal(tt.want.HiddenAt) {
				t.Errorf("hidden at %v, want %v", got.HiddenAt, tt.want.HiddenAt)
			}
			got.HiddenAt, tt.want.HiddenAt = time.Time{}, time.Time{}
			if found != tt.wantFound || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("List gave %+v (found %v), want %+v", got, found, tt.want)
			}
		})
	}
}

func TestListOrder(t *testing.T) {
	m := git.ProjectMap{Repos: []git.RepoProject{
		{CommonDir: filepath.Join("/a", "zebra", ".git"), Key: chip8Key},
		{CommonDir: filepath.Join("/a", "Apple", ".git"), Key: plantKey},
	}}
	list := List(Input{Map: m, Days: []string{folderKey, "p_bad key"}})
	var titles []string
	for _, p := range list {
		titles = append(titles, p.Title())
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "projects come in the order of their titles, in either case, a code's title among them", ok: slices.Equal(titles, []string{"Apple", "Project d2a8f6", "zebra"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("titles %q", titles)
			}
		})
	}
}

func TestFind(t *testing.T) {
	list := []Project{{Key: chip8Key, Code: "7c41e9"}, {Key: "p_Plant", Code: "p_Plan"}, {Key: "p_plant", Code: "p_plan"}}
	tests := []struct {
		name, arg string
		want      string // the key found, "" for none
	}{
		{name: "the short code finds its project", arg: "7c41e9", want: chip8Key},
		{name: "the dashboard's words, Project and the code, find it too", arg: "Project 7c41e9", want: chip8Key},
		{name: "so do they in lower case", arg: "project 7c41e9", want: chip8Key},
		{name: "the whole key finds it", arg: chip8Key, want: chip8Key},
		{name: "a code in other case finds its project when only one matches", arg: "7C41E9", want: chip8Key},
		{name: "a code in other case that two projects match finds none", arg: "P_PLAN", want: ""},
		{name: "an exact code wins over another case", arg: "p_plan", want: "p_plant"},
		{name: "a code no project has finds none", arg: "abcdef", want: ""},
		{name: "nothing finds nothing", arg: "Project ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := Find(list, tt.arg)
			if ok != (tt.want != "") || p.Key != tt.want {
				t.Errorf("Find(%q) = %s, %v; want %q", tt.arg, p.Key, ok, tt.want)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	named := Project{Key: chip8Key, LocalName: "chip8-emulator", Label: "chip8-emulator"}
	unnamed := Project{Key: folderKey}
	tests := []struct {
		name    string
		p       Project
		label   string
		given   bool
		want    string
		wantErr error
	}{
		{name: "without a label, the project's own shows", p: named, want: "chip8-emulator"},
		{name: "a label given wins over the project's own", p: named, label: "Chip-8 emulator", given: true, want: "Chip-8 emulator"},
		{name: "a label is cleaned the way the server cleans a display name before anyone sees it", p: named, label: "  Chip-8 \t emulator" + string(rune(0x202e)) + " ", given: true, want: "Chip-8 emulator"},
		{name: "without a label, a project with no name of its own is refused", p: unnamed, wantErr: ErrNoLabel},
		{name: "a label that cleans to nothing is refused", p: unnamed, label: " " + string(rune(0x200b)) + " ", given: true, wantErr: ErrEmptyLabel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Label(tt.p, tt.label, tt.given)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("Label = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
