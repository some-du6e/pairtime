// SPDX-License-Identifier: AGPL-3.0-or-later

// Command provenance checks that every captured harness fixture says where it
// came from (DESIGN 5.1; docs/PLAN.md, WP-CI1). Each directory
// testdata/fixtures/<harness>/<version>/<os>/ needs a PROVENANCE.md that
// opens with front matter setting seven fields:
//
//	---
//	harness: claude_code
//	harness_version: 2.0.14
//	os: Windows 11 23H2, build 22631
//	captured_on: 2026-11-21
//	captured_by: Karim
//	synthetic: false
//	scrubbed_by: tools/capture/scrub.sh, every replacement read by eye
//	---
//
// Every field needs a value. synthetic is true or false; true marks a
// fixture written from documentation rather than captured from the real
// harness. captured_on is a date, YYYY-MM-DD. More fields may follow, and
// any Markdown may follow the front matter.
//
// Every file in the tree must sit inside one of those directories, where
// its PROVENANCE.md covers it. Only notes may sit above them: a README.md
// at any level, and the root's PROVENANCE.template.md (WP-AD0). So a
// capture saved without its <os> level is reported, and so is a symbolic
// link where a directory should be.
//
// Usage:
//
//	go run ./tools/ci/provenance [PATH...]
//
// A PATH that is a directory is checked as a fixtures tree; a PATH that is a
// file is checked as one PROVENANCE.md, which is how a template's example
// can be tested. PATH defaults to testdata/fixtures, and a fixtures tree
// that doesn't exist yet passes. The exit status is 1 when anything is
// wrong and 2 on bad usage.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileName is the provenance file each fixture directory needs.
const FileName = "PROVENANCE.md"

// fixtureDepth is how far below the fixtures root a <harness>/<version>/<os>
// directory sits.
const fixtureDepth = 3

// isNote reports whether a file above the fixture directories, at a
// slash-separated path relative to the tree, is one of the notes allowed
// there.
func isNote(p string) bool {
	return path.Base(p) == "README.md" || p == "PROVENANCE.template.md"
}

// required lists the front-matter fields every provenance file sets, in the
// order docs/PLAN.md gives them.
var required = []string{
	"harness",
	"harness_version",
	"os",
	"captured_on",
	"captured_by",
	"synthetic",
	"scrubbed_by",
}

// problem is one thing wrong with the fixtures, at a slash-separated path.
type problem struct {
	path string
	msg  string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintln(stderr, "usage: provenance [PATH...]")
			return 2
		}
	}
	if len(args) == 0 {
		args = []string{"testdata/fixtures"}
	}
	var problems []problem
	for _, p := range args {
		info, err := os.Stat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(stdout, "%s doesn't exist yet: no fixtures to check\n", p)
		case err != nil:
			fmt.Fprintln(stderr, err)
			return 2
		case info.IsDir():
			found, dirs, err := checkTree(os.DirFS(p))
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
			for _, pr := range found {
				problems = append(problems, problem{path: filepath.ToSlash(filepath.Join(p, pr.path)), msg: pr.msg})
			}
			fmt.Fprintf(stdout, "%s: %d fixture directories checked\n", p, dirs)
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
			for _, msg := range checkFile(data) {
				problems = append(problems, problem{path: filepath.ToSlash(p), msg: msg})
			}
		}
	}
	for _, pr := range problems {
		fmt.Fprintf(stdout, "::error file=%s::%s\n", pr.path, pr.msg)
	}
	if len(problems) > 0 {
		fmt.Fprintf(stdout, "%d provenance problems. Each testdata/fixtures/<harness>/<version>/<os>/ directory needs a %s whose front matter sets %s, and every fixture file goes in one of those directories (see tools/ci/provenance).\n",
			len(problems), FileName, strings.Join(required, ", "))
		return 1
	}
	return 0
}

// checkTree checks every fixture directory in a fixtures tree. It returns
// the problems found, with paths relative to the tree, and how many fixture
// directories it looked at.
func checkTree(fsys fs.FS) ([]problem, int, error) {
	var problems []problem
	dirs := 0
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if !d.IsDir() {
			// The walk skips each fixture directory's contents once it's
			// checked, so anything else reaching here sits above one.
			if !isNote(p) {
				problems = append(problems, problem{path: p, msg: "not inside a <harness>/<version>/<os>/ fixture directory, so no PROVENANCE.md covers it"})
			}
			return nil
		}
		depth := strings.Count(p, "/") + 1
		if depth < fixtureDepth {
			return nil
		}
		dirs++
		file := path.Join(p, FileName)
		data, err := fs.ReadFile(fsys, file)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			problems = append(problems, problem{path: p, msg: fmt.Sprintf("fixture directory has no %s", FileName)})
		case err != nil:
			return err
		default:
			for _, msg := range checkFile(data) {
				problems = append(problems, problem{path: file, msg: msg})
			}
		}
		// Directories further down belong to this fixture directory.
		return fs.SkipDir
	})
	if err != nil {
		return nil, 0, err
	}
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].path < problems[j].path })
	return problems, dirs, nil
}

// checkFile checks one provenance file and describes each problem with it.
func checkFile(data []byte) []string {
	fields, err := frontMatter(data)
	if err != nil {
		return []string{err.Error()}
	}
	var msgs []string
	for _, key := range required {
		value, ok := fields[key]
		switch {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("front matter has no %s field", key))
		case value == "":
			msgs = append(msgs, fmt.Sprintf("field %s is empty", key))
		}
	}
	if v, ok := fields["synthetic"]; ok && v != "" && v != "true" && v != "false" {
		msgs = append(msgs, fmt.Sprintf("synthetic is %q, want true or false", v))
	}
	if v, ok := fields["captured_on"]; ok && v != "" {
		if _, err := time.Parse(time.DateOnly, v); err != nil {
			msgs = append(msgs, fmt.Sprintf("captured_on is %q, want a date like 2026-11-21", v))
		}
	}
	return msgs
}

// frontMatter reads the key: value lines between the two --- lines that open
// a Markdown file. Blank lines and # comments inside are skipped, and a value
// in matching single or double quotes loses them.
func frontMatter(data []byte) (map[string]string, error) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return nil, errors.New("file must open with front matter: a --- line, the fields as key: value lines, and another --- line")
	}
	fields := make(map[string]string)
	for i, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			return fields, nil
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lineNo := i + 2
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("front matter line %d is %q, want key: value", lineNo, line)
		}
		if _, dup := fields[key]; dup {
			return nil, fmt.Errorf("front matter line %d sets %s a second time", lineNo, key)
		}
		fields[key] = unquote(strings.TrimSpace(value))
	}
	return nil, errors.New("front matter has no closing --- line")
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}
