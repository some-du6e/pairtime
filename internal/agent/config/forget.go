// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
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
// Every other line stays byte for byte, as SetOnline says, and the file has
// to parse to what it did, with the projects gone and nothing else
// (edited), so a spelling this misses is a refusal, never a label left
// behind. A file that spells projects in another upper and lower case, such
// as [Projects.x], which Parse doesn't read as projects but which can still
// hold a label, is refused, and so is one with a multi-line string. The
// error says what to delete by hand.
func ForgetNames(name string) error {
	byHand := func(name, _ string) string {
		return "delete every table under projects, in any upper and lower case, such as [projects.<key>], with every line under it, in " + name + " by hand"
	}
	variant := false
	err := rewrite(name, false, byHand, func(text string) string {
		if variant = projectsVariant(text); variant {
			return text
		}
		return withoutProjects(text)
	}, func(c *Config) { c.Projects = nil })
	switch {
	case variant:
		return fmt.Errorf("config: config.toml spells projects in another upper and lower case, which pairtime doesn't read as projects but which can still hold a project's name, so it didn't change it; %s", byHand(name, ""))
	case err != nil:
		return err
	}
	return nil
}

// projectsVariant reports whether text, which parses, spells projects at
// the top of the file in another upper and lower case than Parse reads, as
// a table, such as [Projects.x] or [PROJECTS], a dotted key, such as
// Projects.x.name, or an inline table. The decoder's keys name each one
// wherever the file writes it.
func projectsVariant(text string) bool {
	md, err := toml.Decode(text, &fileConfig{})
	if err != nil {
		return false // edited refuses a file that doesn't parse before it asks
	}
	for _, k := range md.Keys() {
		if len(k) > 0 && k[0] != projectsTable && strings.EqualFold(k[0], projectsTable) {
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
