// SPDX-License-Identifier: AGPL-3.0-or-later

// Package projects is the list `pairtime projects` and the local
// dashboard's Projects page show: every project this computer knows, with
// the name pairtime shows for it here, the short code its owner's
// dashboard calls it by, and whether its name shows on that dashboard
// (D-owner-names; docs/spec/project-names-v1.md). Both read it the same
// way, through List, and turn a name on and off through Label, Show and
// Hide.
package projects

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// Days is how many days of rollups, today's included, List takes the
// projects of beside the project map: the last 30 days, the span of the
// dashboard's Projects page.
const Days = 30

// mapKey is the git adapter's project map's key in state.json, which the
// agent's git host writes (internal/agent/git.go).
const mapKey = "projects"

// State says whether a project's name shows on its owner's own dashboard.
type State string

const (
	// Off is a name config.toml has off, which the dashboard doesn't show:
	// it calls the project by its code.
	Off State = "off"
	// On is a name config.toml has on, with the label the server's last
	// answer showed.
	On State = "on"
	// Sending is a name config.toml has on that the server hasn't shown
	// with its label yet: it goes with the uploader's next request, once
	// the computer is linked and online.
	Sending State = "sending"
	// Removing is a name config.toml has off that the server's last
	// answer still showed: the delete goes with the next request.
	Removing State = "removing"
	// Hidden is a name the owner hid on their dashboard, which pairtime
	// turned off in config.toml, so it stays hidden until they turn it on
	// again here.
	Hidden State = "hidden"
)

// Project is one project of the list.
type Project struct {
	// Key is the project's key.
	Key string
	// Code is its short code, projectname.ShortCodes over the keys this
	// computer knows: the dashboard calls the project "Project <code>"
	// while its name is off. The dashboard's code can be a character
	// longer, when another of the owner's computers has a key that starts
	// the same way.
	Code string
	// LocalName is the name pairtime shows for the project on this
	// computer (LocalName), "" when it has none.
	LocalName string
	// State says whether its name shows on the dashboard.
	State State
	// Label is the label the name shows, or goes with, while it is on
	// (On, Sending), and the label turning it on shows otherwise (Label):
	// the one config.toml kept, or the local name; "" when there is none.
	Label string
	// HiddenAt is when pairtime found the name hidden on the dashboard,
	// for Hidden.
	HiddenAt time.Time
}

// Title is what the list calls p: its local name, or "Project <code>".
func (p Project) Title() string {
	if p.LocalName != "" {
		return p.LocalName
	}
	return projectname.CodeName(p.Code)
}

// LocalName is the name pairtime shows on this computer for the repo whose
// common git directory is commonDir, as the git adapter's project map
// keeps it: the folder of the main checkout, which holds .git, for a repo
// with worktrees too, since its worktrees share that directory, or else
// the common directory's own folder name without ".git", as for a bare
// repo. It is that folder's name only, never a path, cleaned with
// projectname.CleanLabel, and "" when there is none.
func LocalName(commonDir string) string {
	if commonDir == "" {
		return ""
	}
	dir := filepath.Clean(commonDir)
	base := filepath.Base(dir)
	if base == ".git" {
		base = filepath.Base(filepath.Dir(dir))
	} else {
		base = strings.TrimSuffix(base, ".git")
	}
	if base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return ""
	}
	return projectname.CleanLabel(base)
}

// LocalNames returns the local name of every project the map holds, by its
// key and by each key it had before (RepoProject.Aliases). Several repos
// can share a key, as a fresh clone shares the original's: the first by
// common directory names it.
func LocalNames(m git.ProjectMap) map[string]string {
	repos := slices.Clone(m.Repos)
	slices.SortFunc(repos, func(a, b git.RepoProject) int { return cmp.Compare(a.CommonDir, b.CommonDir) })
	out := make(map[string]string)
	for _, r := range repos {
		name := LocalName(r.CommonDir)
		if name == "" {
			continue
		}
		for _, key := range append([]string{r.Key}, r.Aliases...) {
			if _, ok := out[key]; !ok && key != "" {
				out[key] = name
			}
		}
	}
	return out
}

// ReadMap reads the git adapter's project map from the state.json at
// name without changing the file, as a command beside the running agent
// must: a missing file, or one without a map, is an empty map.
func ReadMap(name string) (git.ProjectMap, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return git.ProjectMap{}, nil
	}
	if err != nil {
		return git.ProjectMap{}, err
	}
	var st map[string]json.RawMessage
	if err := json.Unmarshal(data, &st); err != nil {
		return git.ProjectMap{}, fmt.Errorf("state.json doesn't read: %w", err)
	}
	var m git.ProjectMap
	if raw, ok := st[mapKey]; ok {
		if err := json.Unmarshal(raw, &m); err != nil {
			return git.ProjectMap{}, fmt.Errorf("the project map in state.json doesn't read: %w", err)
		}
	}
	return m, nil
}

// Dates returns the n local days up to today, YYYY-MM-DD, today first.
func Dates(today string, n int) []string {
	t, err := time.Parse(time.DateOnly, today)
	if err != nil {
		return nil
	}
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, t.AddDate(0, 0, -i).Format(time.DateOnly))
	}
	return out
}

// DayKeys returns the keys of the projects in the rollups of dates in the
// days directory dir. A day with no rollup has none; the error is the
// first rollup that doesn't read, whose day is then left out.
func DayKeys(dir string, dates []string) ([]string, error) {
	seen := make(map[string]bool)
	var keys []string
	var first error
	for _, date := range dates {
		d, err := store.ReadDay(dir, date)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		for _, p := range d.Projects {
			if !seen[p.Project] {
				seen[p.Project] = true
				keys = append(keys, p.Project)
			}
		}
	}
	return keys, first
}

// Input is what List lists from.
type Input struct {
	// Map is the git adapter's project map.
	Map git.ProjectMap
	// Days are the keys of the projects in the last Days days' rollups.
	Days []string
	// Config is config.toml, whose [projects.<key>] tables turn names on.
	Config config.Config
	// Names is the uploader's project-names.json, and Server and Device
	// the sign-in uploads go with now, "" while there is none: the names
	// the server confirmed to another sign-in count for nothing.
	Names          uploader.Names
	Server, Device string
}

// List returns every project this computer knows: those of the project
// map, of the last Days days' rollups, of config.toml's project tables and
// of the names the server shows. They are in the order of their titles,
// then of their keys, each with its code, its local name, and its name's
// state.
func List(in Input) []Project {
	local := LocalNames(in.Map)
	var confirmed map[string]string
	if in.Server != "" && in.Names.Server == in.Server && in.Names.Device == in.Device {
		confirmed = in.Names.Confirmed
	}
	listed := make(map[string]bool)
	for _, r := range in.Map.Repos {
		listed[r.Key] = true
	}
	for _, key := range in.Days {
		listed[key] = true
	}
	for key := range in.Config.Projects {
		listed[key] = true
	}
	for key := range confirmed {
		listed[key] = true
	}
	maps.DeleteFunc(listed, func(key string, _ bool) bool { return !config.IsProjectKey(key) })
	// The codes come from every key this computer knows, old ones too,
	// which the server may hold minutes under, as the dashboard's come
	// from every key it holds.
	known := slices.Collect(maps.Keys(listed))
	for _, r := range in.Map.Repos {
		for _, a := range r.Aliases {
			if config.IsProjectKey(a) {
				known = append(known, a)
			}
		}
	}
	codes := projectname.ShortCodes(known)
	out := make([]Project, 0, len(listed))
	for key := range listed {
		p := Project{Key: key, Code: codes[key], LocalName: local[key]}
		p.State, p.Label, p.HiddenAt = state(key, local[key], in.Config.Projects[key], confirmed, in.Names.Hidden)
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Project) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Title()), strings.ToLower(b.Title())), cmp.Compare(a.Key, b.Key))
	})
	return out
}

// state works out a project's name's state and its label (Project.Label).
func state(key, local string, cfg config.Project, confirmed map[string]string, hidden map[string]uploader.HiddenName) (State, string, time.Time) {
	shown, isShown := confirmed[key]
	h, wasHidden := hidden[key]
	off := cfg.Name
	if off == "" {
		off = local
	}
	switch {
	case wasHidden && h.Pending:
		// config.toml still says on, but pairtime sends it no more.
		return Hidden, cmp.Or(cfg.Name, h.Label), time.UnixMilli(h.At).UTC()
	case cfg.Online && isShown && shown == cfg.Name:
		return On, cfg.Name, time.Time{}
	case cfg.Online:
		return Sending, cfg.Name, time.Time{}
	case isShown:
		return Removing, off, time.Time{}
	case wasHidden:
		return Hidden, off, time.UnixMilli(h.At).UTC()
	}
	return Off, off, time.Time{}
}

// Find returns the project of list that arg names: its short code, as
// `pairtime projects` and the dashboard show it, "Project <code>", or its
// whole key, matched exactly or else in either case when only one
// matches.
func Find(list []Project, arg string) (Project, bool) {
	a := strings.TrimSpace(arg)
	if len(a) > len("Project ") && strings.EqualFold(a[:len("Project ")], "Project ") {
		a = strings.TrimSpace(a[len("Project "):])
	}
	if a == "" {
		return Project{}, false
	}
	for _, p := range list {
		if p.Code == a || p.Key == a {
			return p, true
		}
	}
	var found []Project
	for _, p := range list {
		if strings.EqualFold(p.Code, a) || strings.EqualFold(p.Key, a) {
			found = append(found, p)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return Project{}, false
}

// The labels Label refuses.
var (
	// ErrNoLabel is no label given for a project with none to show: no
	// local name, and no label kept in config.toml.
	ErrNoLabel = errors.New("the project has no name on this computer, so it needs a label")
	// ErrEmptyLabel is a label that is empty once cleaned.
	ErrEmptyLabel = errors.New("the label is empty once its spaces and invisible characters are taken out")
)

// Label returns the label turning p's name on shows: label, cleaned with
// projectname.CleanLabel as the server cleans a display name, or, when
// label isn't given, p.Label, the one config.toml kept or the local name.
// The caller shows it to the owner before it saves it, since what the
// dashboard shows is the label they saw (D-owner-names).
func Label(p Project, label string, given bool) (string, error) {
	if !given {
		if p.Label == "" {
			return "", ErrNoLabel
		}
		label = p.Label
	}
	clean := projectname.CleanLabel(label)
	if clean == "" {
		return "", ErrEmptyLabel
	}
	return clean, nil
}

// Show turns p's name on with label, which Label returned, in the
// config.toml at configFile. The running agent's config watch picks the
// change up, and its uploader sends it.
func Show(configFile string, p Project, label string) error {
	return config.ShowName(configFile, p.Key, label)
}

// Hide turns p's name off in the config.toml at configFile.
func Hide(configFile string, p Project) error {
	return config.HideName(configFile, p.Key)
}
