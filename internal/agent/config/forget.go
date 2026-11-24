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
	// Removed is how many of mine's keys it took out under projects, once
	// the file is written.
	Removed int
	// Left is how many tables under projects it left, of keys mine doesn't
	// report: another computer's, when computers share the file, as a
	// roaming Windows profile or a symlinked dotfiles folder does, or one an
	// older release's "delete everything" left. It never says whose, nor
	// what they hold.
	Left int
	// ByHand, when the edit didn't take mine's keys out, holds what to
	// delete by hand for each of them, as the file writes it (byHandOf).
	ByHand []string
}

// ForgetNames takes out of the config.toml at name, or the file it links
// to, what is under projects for the keys mine reports, the keys of the
// projects this computer knows of: the label of each project the teen
// named for their own dashboard, which "delete everything" deletes, since
// no project's key names it once K_dev is gone, and which uninstall takes
// out of a config.toml it doesn't delete (pairtime privacy delete, pairtime
// uninstall; DESIGN 8.8; D-wipe-project-labels). That is each such key's
// [projects.<key>] table, with the tables and arrays of tables under it and
// every line up to the next header; each line of a [projects] table that
// names such a key, a dotted key or an inline table; and each dotted key
// under projects at the top of the file that names one. A value that goes
// on over lines goes whole. Each goes with the comment lines right above
// it, with no blank line between, which often name the project, as
// toml_edit counts them with the table; and the comment lines right above
// the header that ends a table that goes stay with that header. Keys match
// however a header quotes or spaces them, since ["projects".p_x] and
// [ projects . p_x ] name the same table.
//
// A table or entry for any other key stays, since another computer sharing
// the file keeps its own projects' tables there, and so do an array of
// [[projects]] tables and a projects key inside another table, such as
// [display]. Forgotten.Left counts the tables it left.
//
// Every other line stays byte for byte, as SetOnline says. What the edit
// leaves has to parse, with no key of mine's left under projects in any
// upper and lower case, as the decoder reads its keys, and to what the file
// did with those projects gone and nothing else (edited). So a spelling it
// misses is a refusal, never a label of mine's left behind: a table or entry
// that spells projects in another case, such as [Projects.x], which Parse
// doesn't read as projects but which can still hold a label; a header that
// escapes a letter; an inline table at projects; and a file with a
// multi-line string, a line of which could look like a table's. Every error
// it returns, a refusal or another, ends by saying once what to delete by
// hand, which Forgotten.ByHand lists, read from the file as it is when the
// edit stopped before reading it.
func ForgetNames(name string, mine func(key string) bool) (Forgotten, error) {
	var (
		f        Forgotten
		why      string // why the edit left the file as it is
		removed  int
		lastWhat []string // what to delete by hand, as the last byHand call found it
		lastHand string   // and its sentence, which rewrite's errors end with
	)
	byHand := func(file, text string) string {
		lastWhat, lastHand = handFor(file, text, mine)
		return lastHand
	}
	err := rewrite(name, false, byHand, func(text string) string {
		f, why, removed = Forgotten{}, "", 0
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
			removed = keys.count(mine)
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
	if why == "" && err == nil {
		f.Removed = removed
		return f, nil
	}
	if lastHand == "" {
		// The edit stopped before it read the file, at a link it can't
		// follow or a read that failed.
		lastWhat, lastHand = handFor(name, "", mine)
	}
	if f.ByHand == nil {
		f.ByHand = lastWhat
	}
	if f.Left == 0 {
		if _, others, err := ProjectTables(name, mine); err == nil {
			f.Left = others
		}
	}
	if why != "" {
		return f, fmt.Errorf("config: config.toml %s, so pairtime didn't change it; %s", why, lastHand)
	}
	if !strings.HasSuffix(err.Error(), lastHand) {
		err = fmt.Errorf("%w; %s", err, lastHand)
	}
	return f, err
}

// handFor returns what to delete by hand of mine's keys in file, which
// holds text, or, with text "", what file holds now (byHandOf), and the
// sentence that says to. A file it can't read is told to lose the tables
// of this computer's projects, unnamed.
func handFor(file, text string, mine func(key string) bool) (what []string, hand string) {
	if text == "" {
		if data, err := os.ReadFile(file); err == nil {
			text = string(data)
		}
	}
	text = strings.TrimPrefix(text, bom)
	keys, _ := projectKeys(text)
	what = byHandOf(text, keys, mine)
	list := "the tables of this computer's projects under projects"
	if len(what) > 0 {
		list = strings.Join(what, ", ")
	}
	return what, "delete " + list + " in " + file + " by hand"
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
// in any upper and lower case, once. An array of tables at projects,
// [[projects]] or projects = [{...}], names no project's key, and holds
// none. The decoder gives a type only to a table a header or an inline
// table makes, so projects has none when only [projects.<key>] headers or
// dotted keys make it, and the types that tell are the arrays'. ok is
// false for text that doesn't parse.
func projectKeys(text string) (tableKeys, bool) {
	md, err := toml.Decode(text, &fileConfig{})
	if err != nil {
		return nil, false
	}
	var out tableKeys
	for _, k := range md.Keys() {
		if len(k) < 2 || !strings.EqualFold(k[0], projectsTable) {
			continue
		}
		if typ := md.Type(k[0]); typ == "ArrayHash" || typ == "Array" {
			continue
		}
		if t := (tableKey{k[0], k[1]}); !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out, true
}

// byHandOf returns what to delete by hand in text for mine's keys under
// projects, in the file's order, each once, as the file writes it: each
// table or array-of-tables header of such a key, up to its closing
// bracket, and each entry with no header, by its key path up to its =,
// with where it is, under the [projects] header it is in, as written, or
// at the top of the file. None holds a value or a comment, so none holds a
// label. projects matches in any upper and lower case, as the decoder
// reads it. A key of keys that no line names, as in an inline table at
// projects, is named by its path as the decoder reads it.
func byHandOf(text string, keys tableKeys, mine func(key string) bool) []string {
	var out []string
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	named := make(map[string]bool) // mine's keys a line names
	top, members, depth := true, "", 0
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if depth > 0 {
			depth = max(0, openAfter(t, depth))
			continue
		}
		switch {
		case strings.HasPrefix(t, "["):
			top, members = false, ""
			path, array, ok := headerPath(t)
			end := headerEnd(t)
			if !ok || end < 0 || len(path) == 0 || !strings.EqualFold(path[0], projectsTable) {
				continue
			}
			switch {
			case len(path) == 1 && !array:
				members = t[:end]
			case len(path) > 1 && mine(path[1]):
				named[path[1]] = true
				add(t[:end])
			}
		case !isComment(t):
			key, _, _ := strings.Cut(t, "=")
			key = strings.TrimSpace(key)
			switch parts := keyParts(t); {
			case top && len(parts) > 1 && strings.EqualFold(parts[0], projectsTable) && mine(parts[1]):
				named[parts[1]] = true
				add(key + " at the top of the file")
			case members != "" && len(parts) > 0 && mine(parts[0]):
				named[parts[0]] = true
				add(key + " under " + members)
			}
			depth = max(0, openAfter(t, 0))
		}
	}
	for _, k := range keys {
		if mine(k.key) && !named[k.key] {
			add(k.top + "." + k.key)
		}
	}
	return out
}

// withoutProjects returns text without what ForgetNames takes out for the
// keys mine reports: each one's tables and arrays of tables under
// projects, with every line up to the next header, its entries in a
// [projects] table, and its dotted keys under projects at the top of the
// file, each with every line of a value that goes on over lines. The
// comment lines right above each go with it, and the comment lines right
// above the header that ends a table that goes stay with that header, as
// toml_edit counts them with the header below them.
func withoutProjects(text string, mine func(key string) bool) string {
	lines := strings.SplitAfter(text, "\n")
	drop := make([]bool, len(lines))
	// starts are the first lines of the tables and entries that go, and ends
	// the headers that end a table that goes and stay.
	var starts, ends []int
	top, in, members := true, false, false
	// depth is the brackets and braces a value left open, and entry says the
	// value is that of an entry that goes.
	depth, entry := 0, false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if depth > 0 {
			// A value that goes on from a line before: never a header or a key.
			if in || entry {
				drop[i] = true
			}
			if depth = max(0, openAfter(t, depth)); depth == 0 {
				entry = false
			}
			continue
		}
		switch {
		case strings.HasPrefix(t, "["):
			was := in
			top, in, members = false, false, false
			if path, array, ok := headerPath(t); ok && len(path) > 0 && path[0] == projectsTable {
				in = len(path) > 1 && mine(path[1])
				members = len(path) == 1 && !array
			}
			switch {
			case in:
				starts = append(starts, i)
			case was:
				ends = append(ends, i)
			}
		case !isComment(t):
			if parts := keyParts(t); top && len(parts) > 1 && parts[0] == projectsTable && mine(parts[1]) ||
				members && len(parts) > 0 && mine(parts[0]) {
				drop[i] = true
				starts = append(starts, i)
				entry = true
			}
			if depth = max(0, openAfter(t, 0)); depth == 0 {
				entry = false
			}
		}
		if in {
			drop[i] = true
		}
	}
	for _, e := range ends {
		for j := e - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "#"); j-- {
			drop[j] = false
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

// headerPath returns the keys a [table] or [[array of tables]] header line
// t names, as tablePath splits them, and whether it is an array's.
func headerPath(t string) (path []string, array, ok bool) {
	name := strings.TrimPrefix(t, "[")
	if array = strings.HasPrefix(name, "["); array {
		name = name[1:]
	}
	name, _, found := strings.Cut(name, "]")
	if !found {
		return nil, array, false
	}
	path, ok = tablePath(strings.TrimSpace(name))
	return path, array, ok
}

// headerEnd returns where the header line t ends, just past its closing
// bracket, or its two for an array of tables, so that what follows, such as
// a comment, can be left out; -1 for a line with none.
func headerEnd(t string) int {
	closing := "]"
	if strings.HasPrefix(t, "[[") {
		closing = "]]"
	}
	i := strings.Index(t, closing)
	if i < 0 {
		return -1
	}
	return i + len(closing)
}

// openAfter returns how many brackets and braces are open at the end of the
// trimmed line t, outside its strings and its comment, when depth were open
// before it.
func openAfter(t string, depth int) int {
	for i := 0; i < len(t); i++ {
		switch c := t[i]; c {
		case '#':
			return depth
		case '"', '\'':
			for i++; i < len(t) && t[i] != c; i++ {
				if c == '"' && t[i] == '\\' {
					i++
				}
			}
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
	}
	return depth
}
