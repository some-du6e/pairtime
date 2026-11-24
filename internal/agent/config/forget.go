// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Forgotten is what ForgetNames found under projects in config.toml.
type Forgotten struct {
	// Removed is how many tables of mine's keys it took out.
	Removed int
	// Left is how many tables under projects it left, of keys mine doesn't
	// report: another computer's, when computers share the file, as a
	// roaming Windows profile or a symlinked dotfiles folder does. It never
	// says whose, nor what they hold.
	Left int
	// ByHand, when the edit refused, holds what to delete by hand for each
	// of mine's keys: the header of its table as the file writes it, up to
	// its closing bracket, or its key, such as projects.<key>, where no
	// header names it.
	ByHand []string
}

// ForgetNames takes out of the config.toml at name the tables under
// projects of the keys mine reports, the keys of the projects this computer
// tracked: the label of each one the teen named for their own dashboard,
// which "delete everything" deletes, since no project's key names it once
// K_dev is gone (pairtime privacy delete; DESIGN 8.8;
// D-wipe-project-labels). That is each such [projects.<key>] table, with
// the tables under it and every line under their headers; each such key's
// line in a [projects] table; and each dotted key under projects at the
// top of the file that names one. Each goes with the comment lines right
// above it, with no blank line between, which often name the project, as
// toml_edit counts them with the table. Keys match however a header quotes
// or spaces them, since ["projects".p_x] and [ projects . p_x ] name the
// same table.
//
// A table for any other key stays, since another computer sharing the
// file keeps its own projects' tables there, and so does a projects key
// inside another table, such as [display]. Forgotten.Left counts the
// tables it left.
//
// Every other line stays byte for byte, as SetOnline says. What the edit
// leaves has to parse, with no key of mine's left under projects in any
// upper and lower case, as the decoder reads its keys, and to what the file
// did with those projects gone and nothing else (edited). So a spelling it
// misses is a refusal, never a label of mine's left behind: a table that
// spells projects in another case, such as [Projects.x], which Parse
// doesn't read as projects but which can still hold a label; a header
// that escapes a letter; an inline table at projects; and a file with a
// multi-line string, a line of which could look like a table's. Then
// Forgotten.ByHand says what to delete by hand.
func ForgetNames(name string, mine func(key string) bool) (Forgotten, error) {
	byHand := func(name, _ string) string {
		return "delete the tables of this computer's projects under projects, with every line under them, in " + name + " by hand"
	}
	var (
		f   Forgotten
		why string // why the edit left the file as it is
	)
	err := rewrite(name, false, byHand, func(text string) string {
		f, why = Forgotten{}, ""
		keys, ok := projectKeys(text)
		if !ok {
			return text // edited refuses a file that doesn't parse before it asks
		}
		f.Left = keys.count(func(key string) bool { return !mine(key) })
		if keys.count(mine) == 0 {
			return text
		}
		out := withoutProjects(text, mine)
		after, ok := projectKeys(out)
		switch {
		case strings.Contains(text, `"""`) || strings.Contains(text, "'''"):
			why = "has a multi-line string, a line of which could look like a table's"
		case !ok || after.count(mine) > 0:
			why = "names this computer's projects in a way pairtime can't take out line by line, such as a table spelled in another upper and lower case"
		default:
			f.Removed = keys.count(mine)
			return out
		}
		f.ByHand = byHandOf(text, keys, mine)
		return text
	}, func(c *Config) {
		for key := range c.Projects {
			if mine(key) {
				delete(c.Projects, key)
			}
		}
	})
	switch {
	case why != "":
		return f, fmt.Errorf("config: config.toml %s, so pairtime didn't change it", why)
	case err != nil:
		return f, err
	}
	return f, nil
}

// ProjectTables reads the config.toml at name and returns how many tables
// under projects it holds of the keys mine reports, and of other keys,
// however it spells projects, as ForgetNames counts them. A file that isn't
// there holds none; the error is one that doesn't read or parse.
func ProjectTables(name string, mine func(key string) bool) (ofMine, others int, err error) {
	data, err := os.ReadFile(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return 0, 0, nil
	case err != nil:
		return 0, 0, err
	}
	keys, ok := projectKeys(strings.TrimPrefix(string(data), bom))
	if !ok {
		return 0, 0, errors.New("config: config.toml doesn't parse")
	}
	return keys.count(mine), keys.count(func(key string) bool { return !mine(key) }), nil
}

// tableKey is a table under projects as the decoder reads its keys: the top
// key as the file spells it, projects in some upper and lower case, and the
// project's key.
type tableKey struct{ top, key string }

// tableKeys are the tables under projects a file holds, in the order the
// decoder lists them.
type tableKeys []tableKey

// count returns how many of k are of keys that keep reports.
func (k tableKeys) count(keep func(key string) bool) int {
	n := 0
	for _, t := range k {
		if keep(t.key) {
			n++
		}
	}
	return n
}

// projectKeys returns the tables under projects in text, as the decoder
// reads its keys: each key under a top-level table whose name is projects
// in any upper and lower case, once. An array of [[projects]] tables names
// no project's key, and holds none. ok is false for text that doesn't
// parse.
func projectKeys(text string) (tableKeys, bool) {
	md, err := toml.Decode(text, &fileConfig{})
	if err != nil {
		return nil, false
	}
	var out tableKeys
	for _, k := range md.Keys() {
		if len(k) < 2 || !strings.EqualFold(k[0], projectsTable) || md.Type(k[0]) != "Hash" {
			continue
		}
		if t := (tableKey{k[0], k[1]}); !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out, true
}

// byHandOf returns what to delete by hand of the tables in keys of mine's
// keys, in text: the header of each, as the file writes it, up to its
// closing bracket, so that a comment after it never shows, or, for one no
// header names, its key.
func byHandOf(text string, keys tableKeys, mine func(key string) bool) []string {
	var out []string
	named := make(map[tableKey]bool)
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "[") || strings.HasPrefix(t, "[[") {
			continue
		}
		end := strings.IndexByte(t, ']')
		if end < 0 {
			continue
		}
		path, ok := tablePath(strings.TrimSpace(t[1:end]))
		if !ok || len(path) < 2 || !strings.EqualFold(path[0], projectsTable) || !mine(path[1]) {
			continue
		}
		named[tableKey{path[0], path[1]}] = true
		if h := t[:end+1]; !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	for _, k := range keys {
		if mine(k.key) && !named[k] {
			out = append(out, k.top+"."+k.key)
		}
	}
	return out
}

// withoutProjects returns text without what ForgetNames takes out for the
// keys mine reports: each one's tables, with the lines under their
// headers, its lines in a [projects] table, and its dotted keys under
// projects at the top of the file, each with the comment lines right above
// it.
func withoutProjects(text string, mine func(key string) bool) string {
	lines := strings.SplitAfter(text, "\n")
	drop := make([]bool, len(lines))
	var starts []int // the first line of each table or key that goes
	top, in, members := true, false, false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "["):
			top, in, members = false, false, false
			path, ok := tablePath(tableName(t))
			if ok && len(path) > 0 && path[0] == projectsTable {
				in = len(path) > 1 && mine(path[1])
				members = len(path) == 1
			}
			if in {
				starts = append(starts, i)
			}
		case (top || members) && !isComment(t):
			parts := keyParts(t)
			if top && len(parts) > 1 && parts[0] == projectsTable && mine(parts[1]) ||
				members && len(parts) > 0 && mine(parts[0]) {
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
