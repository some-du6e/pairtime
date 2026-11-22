// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"maps"
	"slices"

	"github.com/some-du6e/pairtime/pkg/projectname"
)

// Project is a project's table in config.toml, [projects.<key>] under the
// project's key: whether the project's name shows on its owner's own
// pairtime dashboard, and the label it shows there (DESIGN 3.5, 3.9;
// D-owner-names). Nobody else sees the name, and nothing else about the
// project goes with it (docs/spec/project-names-v1.md). A name is off
// unless its table says online = true and has a name, so a typo keeps it
// off.
type Project struct {
	// Name is the label, name in the file: what the owner's dashboard
	// shows in place of the project's short code, in the form
	// projectname.ValidLabel takes. It stays in the file while the name is
	// off, so turning the name on again can show it again.
	Name string
	// Online is set while the name shows on the owner's dashboard, online
	// = true in the file. It is never set without a Name.
	Online bool
}

// projectsTable is the top-level key of the project tables.
const projectsTable = "projects"

// maxProjectKey is the longest project key: an opaque key is 1 to 128
// characters (ingest-v1 1).
const maxProjectKey = 128

// IsProjectKey reports whether s has the form of a project's key: 1 to
// 128 characters, each one of A-Z a-z 0-9 _ -, the opaque key of ingest-v1
// 1, which is also a bare TOML key. The git adapter's keys are a letter
// and 32 hex digits.
func IsProjectKey(s string) bool {
	if s == "" || len(s) > maxProjectKey {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// NamesOn returns the names cfg turns on, each project's label by its key:
// what the uploader keeps the owner's dashboard showing.
func (cfg Config) NamesOn() map[string]string {
	out := make(map[string]string)
	for key, p := range cfg.Projects {
		if p.Online && p.Name != "" {
			out[key] = p.Name
		}
	}
	return out
}

// parseProjects reads config.toml's [projects] tables, which v is, as the
// TOML decoder gives an untyped value. Each rule that fails turns only its
// project's name off, so the rest of the file stays in force: a key that
// isn't a project's key, a table that isn't one, a name that isn't a label
// pairtime can show, a value of the wrong type, and a setting the table
// doesn't have, which may be a misspelled online = false. Every problem is
// reported. It returns nil when no project has a table.
func parseProjects(v any) (map[string]Project, []string) {
	if v == nil {
		return nil, nil
	}
	tables, ok := v.(map[string]any)
	if !ok {
		return nil, []string{"config.toml: projects isn't a table of projects, so no project's name shows on your dashboard"}
	}
	var problems []string
	out := make(map[string]Project)
	for _, key := range slices.Sorted(maps.Keys(tables)) {
		if !IsProjectKey(key) {
			problems = append(problems, fmt.Sprintf("config.toml: projects.%q isn't a project's key, so it was ignored", key))
			continue
		}
		table, ok := tables[key].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("config.toml: projects.%s isn't a table, so the project's name stays off", key))
			continue
		}
		p, ps := parseProject(key, table)
		problems = append(problems, ps...)
		out[key] = p
	}
	if len(out) == 0 {
		return nil, problems
	}
	return out, problems
}

// parseProject reads one project's table.
func parseProject(key string, table map[string]any) (Project, []string) {
	var (
		p        Project
		problems []string
		online   bool
		fine     = true
	)
	for _, k := range slices.Sorted(maps.Keys(table)) {
		switch k {
		case "name":
			s, isString := table[k].(string)
			switch {
			case !isString:
				fine = false
				problems = append(problems, fmt.Sprintf("config.toml: projects.%s.name isn't text, so the project's name stays off", key))
			case !projectname.ValidLabel(s):
				fine = false
				problems = append(problems, fmt.Sprintf("config.toml: projects.%s.name %q isn't a name pairtime can show: it must be 1 to %d characters, with no space at either end, no two spaces in a row and no control or invisible characters; so the project's name stays off", key, s, projectname.MaxLabel))
			default:
				p.Name = s
			}
		case "online":
			b, isBool := table[k].(bool)
			if !isBool {
				fine = false
				problems = append(problems, fmt.Sprintf("config.toml: projects.%s.online isn't true or false, so the project's name stays off", key))
				continue
			}
			online = b
		default:
			fine = false
			problems = append(problems, fmt.Sprintf("config.toml: the unknown setting %q was ignored, so the project's name stays off until it's fixed", projectsTable+"."+key+"."+k))
		}
	}
	switch {
	case online && fine && p.Name == "":
		problems = append(problems, fmt.Sprintf("config.toml: projects.%s says online = true but has no name, so the project's name stays off", key))
	case online && fine:
		p.Online = true
	}
	return p, problems
}
