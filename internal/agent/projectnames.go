// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/projects"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

// The dashboard's Projects page lists what `pairtime projects` lists, from
// GET /v1/projects, and turns a project's name on or off on its owner's
// own dashboard with PUT /v1/projects/{key}/name, which edits config.toml
// the way `pairtime projects show-name` and `hide-name` do (D-owner-names).
// Everything in the answers stays on this computer; only the uploader
// sends a name, once config.toml has it on.

// projectsReply is GET /v1/projects.
type projectsReply struct {
	// Date is today, the last of the Days days whose projects are listed.
	Date string `json:"date"`
	// Linked says `pairtime login` linked this computer, and Online that
	// config.toml also lets it upload, so the names it turns on go to the
	// server.
	Linked bool `json:"linked"`
	Online bool `json:"online"`
	// Projects are in the order pairtime projects lists them.
	Projects []projectReply `json:"projects"`
}

// projectReply is one project of GET /v1/projects, and the answer to PUT
// /v1/projects/{key}/name.
type projectReply struct {
	Project   string         `json:"project"`
	Code      string         `json:"code"`
	LocalName string         `json:"local_name"`
	State     projects.State `json:"state"`
	Label     string         `json:"label"`
	HiddenAt  *time.Time     `json:"hidden_at,omitempty"`
}

// replyOf is p as the local API gives it.
func replyOf(p projects.Project) projectReply {
	r := projectReply{Project: p.Key, Code: p.Code, LocalName: p.LocalName, State: p.State, Label: p.Label}
	if p.State == projects.Hidden && !p.HiddenAt.IsZero() {
		at := p.HiddenAt.UTC()
		r.HiddenAt = &at
	}
	return r
}

// projectList reads the list as it stands: the project map in memory,
// config.toml as uploads read it, from the file, so an answer after a PUT
// has its change, the last Days days' rollups, and the uploader's
// project-names.json.
func (a *agent) projectList() (projectsReply, []projects.Project) {
	var m git.ProjectMap
	if a.state != nil {
		if _, err := a.state.Get(projectsKey, &m); err != nil {
			a.log.Warn("the project map in state.json doesn't read, so GET /v1/projects lists no project of its own", "err", err)
			m = git.ProjectMap{}
		}
	}
	cfg, broken := UploadConfig(a.dirs)
	today := time.Now().In(a.zone()).Format(time.DateOnly)
	keys, err := projects.DayKeys(a.dirs.Days(), projects.Dates(today, projects.Days))
	if err != nil {
		a.log.Warn("a day's rollup doesn't read, so GET /v1/projects may lack a project", "err", err)
	}
	names, err := uploader.ReadNames(a.dirs.Data)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		a.log.Warn("project-names.json doesn't read", "err", err)
	}
	in := projects.Input{Map: m, Days: keys, Config: cfg, Names: names}
	reply := projectsReply{Date: today, Projects: []projectReply{}}
	if l, err := account.Load(a.dirs.Data); err == nil {
		in.Server, in.Device, reply.Linked = l.Server, l.DeviceID, true
	}
	reply.Online = reply.Linked && broken == "" && cfg.Mode == config.Online
	list := projects.List(in)
	for _, p := range list {
		reply.Projects = append(reply.Projects, replyOf(p))
	}
	return reply, list
}

// serveProjects answers GET /v1/projects.
func (a *agent) serveProjects(w http.ResponseWriter, r *http.Request) {
	reply, _ := a.projectList()
	writeJSON(w, http.StatusOK, reply)
}

// nameChoice is PUT /v1/projects/{key}/name's body: whether the project's
// name shows on the owner's dashboard, and with what label. Without a
// label, turning it on shows the project's own (projects.Label).
type nameChoice struct {
	Online *bool   `json:"online"`
	Label  *string `json:"label"`
}

// maxNameChoice bounds PUT /v1/projects/{key}/name's body.
const maxNameChoice = 4096

// serveProjectName answers PUT /v1/projects/{key}/name: it turns the
// project's name on, with the label cleaned as the server cleans a
// display name, or off, in config.toml, and answers with the project as
// GET /v1/projects lists it then. The uploader wakes, and sends it.
func (a *agent) serveProjectName(w http.ResponseWriter, r *http.Request) {
	var choice nameChoice
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNameChoice))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&choice); err != nil || choice.Online == nil || dec.More() {
		http.Error(w, `the body must be {"online": true or false}, with a "label" to show when it is true`, http.StatusBadRequest)
		return
	}
	key := r.PathValue("key")
	_, list := a.projectList()
	var p projects.Project
	found := false
	for _, q := range list {
		if q.Key == key {
			p, found = q, true
		}
	}
	if !found {
		http.Error(w, "no project on this computer has that key", http.StatusNotFound)
		return
	}
	if *choice.Online {
		label, err := projects.Label(p, deref(choice.Label), choice.Label != nil)
		switch {
		case errors.Is(err, projects.ErrNoLabel):
			http.Error(w, "the project has no name on this computer, so turning it on needs a label", http.StatusUnprocessableEntity)
			return
		case err != nil:
			http.Error(w, "the label is empty once its spaces and invisible characters are taken out", http.StatusUnprocessableEntity)
			return
		}
		err = projects.Show(a.dirs.ConfigFile(), p, label)
		if err != nil {
			http.Error(w, fmt.Sprintf("pairtime couldn't change config.toml: %v", err), http.StatusConflict)
			return
		}
	} else if err := projects.Hide(a.dirs.ConfigFile(), p); err != nil {
		http.Error(w, fmt.Sprintf("pairtime couldn't change config.toml: %v", err), http.StatusConflict)
		return
	}
	if u := a.upload.Load(); u != nil {
		u.Wake()
	}
	_, list = a.projectList()
	for _, q := range list {
		if q.Key == key {
			writeJSON(w, http.StatusOK, replyOf(q))
			return
		}
	}
	writeJSON(w, http.StatusOK, replyOf(p))
}

// localNames is the name pairtime shows on this computer for each project
// of the git adapter's project map in memory (projects.LocalNames), for
// the answers about days and sessions, whose rollups carry no name.
func (a *agent) localNames() map[string]string {
	var m git.ProjectMap
	if a.state != nil {
		if _, err := a.state.Get(projectsKey, &m); err != nil {
			return nil
		}
	}
	return projects.LocalNames(m)
}

// named is name, or the local name of the project key when it has none.
func named(name string, local map[string]string, key string) string {
	if name != "" {
		return name
	}
	return local[key]
}

// deref is *s, or "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
