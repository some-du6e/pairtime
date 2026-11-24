// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// ForgetNames takes every table under projects out of the config.toml at
// name: the label of each project the teen named for their own dashboard,
// which "delete everything" deletes, since no project's key names it once
// K_dev is gone (pairtime privacy delete; DESIGN 8.8;
// D-wipe-project-labels). That is each [projects.<key>] table, or any other
// table under projects, such as [projects] itself or an array of
// [[projects]], with every line under its header; and each dotted key or
// inline table under projects at the top of the file. A table or key goes
// with the comment lines right above it, with no blank line between, which
// often name the project, as toml_edit counts them with the table. Keys
// match however they are quoted or spaced, since ["projects".p_x] and
// [ projects . p_x ] name the same table. A projects key inside another
// table, such as [display], isn't the teen's projects, and stays.
//
// Every other line stays byte for byte, as SetOnline says. The result has
// to parse, with nothing under projects left at its top in any upper and
// lower case, as the decoder reads its keys (projectsLeft), and to what the
// file did with the projects gone and nothing else (edited), so a spelling
// this misses is a refusal, never a label left behind. That refuses a file
// that spells projects in another case, such as [Projects.x], which Parse
// doesn't read as projects but which can still hold a label, and one whose
// header escapes a letter of projects, as well as one with a multi-line
// string, a line of which could look like a table's. The error says what to
// delete by hand.
func ForgetNames(name string) error {
	byHand := func(name, _ string) string {
		return "delete every table under projects, in any upper and lower case, such as [projects.<key>], with every line under it, in " + name + " by hand"
	}
	why := "" // why the edit left the file as it is, though it holds projects
	err := rewrite(name, false, byHand, func(text string) string {
		why = ""
		if !projectsLeft(text) {
			return text // nothing under projects, whatever a string's lines look like
		}
		out := withoutProjects(text)
		switch {
		case strings.Contains(text, `"""`) || strings.Contains(text, "'''"):
			why = "has a multi-line string, a line of which could look like a table's"
			return text
		case projectsLeft(out):
			why = "holds projects pairtime can't take out line by line, such as a table spelled in another upper and lower case"
			return text
		}
		return out
	}, func(c *Config) { c.Projects = nil })
	switch {
	case why != "":
		return fmt.Errorf("config: config.toml %s, so pairtime didn't change it; %s", why, byHand(name, ""))
	case err != nil:
		return err
	}
	return nil
}

// HoldsNames reports whether the config.toml at name holds anything under
// projects at its top, in any upper and lower case, such as the names the
// teen gave their projects: what ForgetNames takes out, or refuses. A file
// that isn't there holds none, and one that doesn't parse is read line by
// line.
func HoldsNames(name string) bool {
	data, err := os.ReadFile(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false
	case err != nil:
		return true
	}
	text := strings.TrimPrefix(string(data), bom)
	if _, err := toml.Decode(text, &fileConfig{}); err == nil {
		return projectsLeft(text)
	}
	top := true
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "["):
			top = false
			if strings.HasPrefix(strings.ToLower(strings.TrimLeft(t, "[ ")), projectsTable) {
				return true
			}
		case top && !isComment(t):
			if parts := keyParts(t); len(parts) > 0 && strings.EqualFold(parts[0], projectsTable) {
				return true
			}
		}
	}
	return false
}

// projectsLeft reports whether text holds anything under projects at its
// top, in any upper and lower case, as the decoder reads its keys, or
// doesn't parse: what ForgetNames left must hold nothing of the kind.
func projectsLeft(text string) bool {
	md, err := toml.Decode(text, &fileConfig{})
	if err != nil {
		return true
	}
	for _, k := range md.Keys() {
		if len(k) > 0 && strings.EqualFold(k[0], projectsTable) {
			return true
		}
	}
	return false
}

// withoutProjects returns text without its tables under projects, with the
// lines under their headers, and without its dotted keys and inline tables
// under projects at the top of the file, each with the comment lines right
// above it (ForgetNames).
func withoutProjects(text string) string {
	lines := strings.SplitAfter(text, "\n")
	drop := make([]bool, len(lines))
	var starts []int // the first line of each table or key that goes
	top, in := true, false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "["):
			top, in = false, underProjects(t)
			if in {
				starts = append(starts, i)
			}
		case top:
			if parts := keyParts(t); !isComment(t) && len(parts) > 0 && parts[0] == projectsTable {
				drop[i] = true
				starts = append(starts, i)
			}
		}
		if in {
			drop[i] = true
		}
	}
	for _, s := range starts {
		for j := s - 1; j >= 0 && !drop[j] && strings.HasPrefix(strings.TrimSpace(lines[j]), "#"); j-- {
			drop[j] = true
		}
	}
	var b strings.Builder
	for i, line := range lines {
		if !drop[i] {
			b.WriteString(line)
		}
	}
	return b.String()
}

// underProjects reports whether the trimmed header line t opens a table
// under projects, projects itself, or an array of either, however its keys
// are quoted or spaced (tablePath).
func underProjects(t string) bool {
	inner := strings.TrimPrefix(t, "[")
	inner = strings.TrimPrefix(inner, "[") // an array of tables
	name, _, ok := strings.Cut(inner, "]")
	if !ok {
		return false
	}
	path, ok := tablePath(strings.TrimSpace(name))
	return ok && len(path) > 0 && path[0] == projectsTable
}
