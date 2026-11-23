// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// CheckServerURL checks the base URL of a pairtime server and returns it
// in the form config.toml keeps: https and a host, or http on localhost or
// a loopback address for local runs and tests, with nothing after the host
// and port. A trailing "/" is dropped, and the scheme and host are lower
// case.
func CheckServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("isn't the address of a server, such as https://pairtime.example.org")
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("uses http, and only https is allowed, except on this computer")
		}
	default:
		return "", errors.New("must start with https://")
	}
	out := scheme + "://" + strings.ToLower(u.Host)
	// What goes in config.toml is quoted as a TOML string, so it holds
	// nothing that would need an escape there.
	if strings.ContainsFunc(out, func(r rune) bool {
		return !('a' <= r && r <= 'z' || '0' <= r && r <= '9' || strings.ContainsRune(":/.-_[]%", r))
	}) {
		return "", errors.New("has characters a server's address doesn't")
	}
	return out, nil
}

// ServerFromFile returns the [server] url of the config.toml at name, as
// CheckServerURL returns it, or "" when the file or the setting is missing
// or empty. A file that doesn't parse, or a url CheckServerURL refuses, is
// an error rather than "", so a typo can't send pairtime login to the
// default server instead of the one the teen meant.
func ServerFromFile(name string) (string, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var file fileConfig
	if _, err := toml.Decode(string(data), &file); err != nil {
		return "", fmt.Errorf("config.toml doesn't parse: %v", err)
	}
	if file.Server.URL == "" {
		return "", nil
	}
	u, err := CheckServerURL(file.Server.URL)
	if err != nil {
		return "", fmt.Errorf("config.toml's server url %q %v", file.Server.URL, err)
	}
	return u, nil
}

// SetOnline switches the config.toml at name to mode "online" with [server]
// url serverURL, which CheckServerURL must accept: what `pairtime login`
// does once it has linked the install (DESIGN 3.2). SetLocal switches it
// back to mode "local" and leaves [server] as it is: what `pairtime
// logout` does. A missing file is written from the default first.
//
// Both change only those values, so the teen's comments, a comment after
// the value included, order, line endings and other settings stay byte for
// byte, and a config.toml that is a symlink stays one: the file it points
// to is the one rewritten. They refuse to write a file they can't change
// that way, such as one that doesn't parse, or one where the edit would
// change any other setting; then the teen edits it by hand, and the error
// says what to write.
func SetOnline(name, serverURL string) error {
	u, err := CheckServerURL(serverURL)
	if err != nil {
		return fmt.Errorf("config: the server %q %v", serverURL, err)
	}
	return edit(name, Online, u)
}

// SetLocal switches the config.toml at name to mode "local".
func SetLocal(name string) error {
	return edit(name, Local, "")
}

// edit sets mode, and [server] url when serverURL isn't "", in the file.
func edit(name string, mode Mode, serverURL string) error {
	byHand := fmt.Sprintf("set mode = %q", string(mode))
	if serverURL != "" {
		byHand += fmt.Sprintf(" and, under [server], url = %q", serverURL)
	}
	return rewrite(name, byHand, func(text string) string { return patch(text, mode, serverURL) }, func(c *Config) {
		c.Mode = mode
		if serverURL != "" {
			c.Server.URL = serverURL
		}
	})
}

// ShowName turns on the name of the project key in the config.toml at
// name, with label, which projectname.ValidLabel must take: its
// [projects.<key>] table gets name = label and online = true, and is added
// at the end of the file when there is none. HideName turns it off: online
// = false, with the name kept, so turning it on again can show the same
// label; a project with no table has no name on, and HideName leaves the
// file as it is. pairtime projects show-name and hide-name, the local
// dashboard's Projects page and the uploader, when the owner hid a name on
// their dashboard, call them (D-owner-names).
//
// Both change only those lines, the way SetOnline does: comments, order,
// line endings and every other setting stay byte for byte, a symlinked
// config.toml stays one, a missing file is written from the default
// first, and a file they can't change that way, such as one that doesn't
// parse or one whose project is an inline table, is left alone with an
// error that says what to write.
func ShowName(name, key, label string) error {
	switch {
	case !IsProjectKey(key):
		return fmt.Errorf("config: %q isn't a project's key", key)
	case !projectname.ValidLabel(label):
		return fmt.Errorf("config: %q isn't a name pairtime can show", label)
	}
	byHand := fmt.Sprintf("set name = %s and online = true under [projects.%s]", tomlString(label), key)
	return rewrite(name, byHand, func(text string) string {
		return patchProject(text, key, []projectLine{{"name", tomlString(label)}, {"online", "true"}})
	}, func(c *Config) {
		c.Projects[key] = Project{Name: label, Online: true}
	})
}

// HideName turns off the name of the project key in the config.toml at
// name (ShowName).
func HideName(name, key string) error {
	if !IsProjectKey(key) {
		return fmt.Errorf("config: %q isn't a project's key", key)
	}
	byHand := fmt.Sprintf("set online = false under [projects.%s]", key)
	return rewrite(name, byHand, func(text string) string {
		return patchProject(text, key, []projectLine{{"online", "false"}})
	}, func(c *Config) {
		if p, ok := c.Projects[key]; ok {
			p.Online = false
			c.Projects[key] = p
		}
	})
}

// editMu serializes this process's edits of config.toml, as the
// uploader turning off a name hidden on the dashboard while the local API
// turns another on (rewrite).
var editMu sync.Mutex

// beforeReplace runs, when set, after rewrite has made its new text and
// before it reads the file again: a test's other writer.
var beforeReplace func(name string)

// LockName is the file whose lock every pairtime process holds while it
// changes config.toml (rewrite). It sits beside config.toml, in pairtime's
// config directory, and stays there.
const LockName = "config.lock"

// lockWait is how long an edit waits for another process's edit to end.
var lockWait = 5 * time.Second

// rewrite changes the config.toml at name with change, the text edit, and
// checks that the file then parses to what it parsed to before with apply
// done to it, and to nothing else. byHand says what the teen can write
// instead, when it refuses. It holds editMu, for this process's edits, and
// config.lock's lock, for every other pairtime process's, as `pairtime
// projects show-name` beside the running agent, from its read to its
// rename, so neither change undoes the other. It also reads the file
// again just before it replaces it, and starts over from the new text when
// it changed meanwhile, as an editor's save does, which takes no lock.
func rewrite(name, byHand string, change func(text string) string, apply func(*Config)) error {
	editMu.Lock()
	defer editMu.Unlock()
	lock := filepath.Join(filepath.Dir(name), LockName)
	if fi, err := os.Lstat(name); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(name)
		if err != nil {
			return fmt.Errorf("config: config.toml is a link pairtime can't follow (%v), so it didn't change it", err)
		}
		name = target
	}
	byHand += " in " + name + " by hand"
	release, err := home.LockFile(lock, lockWait)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No config directory, so no other process edits a config.toml in
		// it: the edit goes on, and changes nothing or fails as it would.
		release = func() error { return nil }
	case err != nil:
		return fmt.Errorf("config: pairtime couldn't lock config.toml to change it (%v), so it didn't; try again, or %s", err, byHand)
	}
	defer release()
	for try := 0; ; try++ {
		data, err := readEdited(name)
		if err != nil {
			return err
		}
		out, err := edited(string(data), byHand, change, apply)
		if err != nil || out == string(data) {
			return err
		}
		if beforeReplace != nil {
			beforeReplace(name)
		}
		now, err := readEdited(name)
		if err != nil {
			return err
		}
		if bytes.Equal(now, data) {
			return home.WriteFile(name, []byte(out))
		}
		if try == 2 {
			return fmt.Errorf("config: config.toml kept changing while pairtime changed it, so it didn't; try again, or %s", byHand)
		}
	}
}

// readEdited reads the config.toml at name for an edit: defaultFile when
// there is none.
func readEdited(name string) ([]byte, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte(defaultFile), nil
	}
	return data, err
}

// edited returns text with change made, once it checked that it parses
// to what text parsed to with apply done to it, and to nothing else.
func edited(text, byHand string, change func(text string) string, apply func(*Config)) (string, error) {
	if _, err := toml.Decode(text, &fileConfig{}); err != nil {
		return "", fmt.Errorf("config: config.toml doesn't parse, so pairtime didn't change it (%v); %s", err, byHand)
	}
	if strings.Contains(text, `"""`) || strings.Contains(text, `'''`) {
		// A multi-line string could hold a line that looks like a key.
		return "", fmt.Errorf("config: config.toml has a multi-line string, so pairtime didn't change it; %s", byHand)
	}
	before, _ := Parse([]byte(text))
	out := change(text)
	after, _ := Parse([]byte(out))
	want := before
	want.Projects = maps.Clone(before.Projects)
	if want.Projects == nil {
		want.Projects = make(map[string]Project)
	}
	apply(&want)
	if len(want.Projects) == 0 {
		want.Projects = nil
	}
	if !reflect.DeepEqual(after, want) {
		return "", fmt.Errorf("config: pairtime couldn't change config.toml without changing other settings, so it didn't; %s", byHand)
	}
	return out, nil
}

// patch rewrites the top-level mode line and the url line of [server],
// and adds them where they are missing: mode as the first line, url just
// under the [server] header, or a [server] table at the end of the file.
func patch(text string, mode Mode, serverURL string) string {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.SplitAfter(text, "\n")
	modeLine := `mode = "` + string(mode) + `"`
	urlLine := `url = "` + serverURL + `"`
	table := ""
	modeDone, urlDone := false, serverURL == ""
	serverHeader := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "["):
			table = tableName(t)
			if table == "server" && serverHeader < 0 {
				serverHeader = i
			}
		case table == "" && isKey(t, "mode"):
			lines[i] = setValue(line, string(mode), modeLine)
			modeDone = true
		case table == "server" && !urlDone && isKey(t, "url"):
			lines[i] = setValue(line, serverURL, urlLine)
			urlDone = true
		}
	}
	if !urlDone && serverHeader >= 0 {
		lines = append(lines[:serverHeader+1], append([]string{urlLine + nl}, lines[serverHeader+1:]...)...)
		urlDone = true
	}
	out := strings.Join(lines, "")
	if !modeDone {
		out = modeLine + nl + out
	}
	if !urlDone {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += nl
		}
		out += nl + "[server]" + nl + urlLine + nl
	}
	return out
}

// setValue rewrites the value of a key = "value" line, keeping its
// indentation, the blanks around "=", and whatever follows the value, such
// as a comment, and its line ending. When the old value isn't a string on
// one line, it writes whole instead, and edit's check catches anything
// that changes.
func setValue(line, value, whole string) string {
	return setRaw(line, `"`+value+`"`, whole)
}

// setRaw is setValue for a value already written as TOML, raw, such as a
// quoted string or true. The old value may be a string on one line, true
// or false; another one is written whole.
func setRaw(line, raw, whole string) string {
	body := strings.TrimRight(line, "\r\n")
	ending := line[len(body):]
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		return indentOf(line) + whole + ending
	}
	start := eq + 1
	for start < len(body) && (body[start] == ' ' || body[start] == '\t') {
		start++
	}
	end := valueEnd(body, start)
	if end < 0 {
		return indentOf(line) + whole + ending
	}
	return body[:start] + raw + body[end:] + ending
}

// valueEnd returns where the value that starts at start in a line's body
// ends, for a string on one line, true or false, or -1 for any other.
func valueEnd(body string, start int) int {
	if start >= len(body) {
		return -1
	}
	switch rest := body[start:]; {
	case rest[0] == '"':
		for j := start + 1; j < len(body); j++ {
			if body[j] == '\\' {
				j++
				continue
			}
			if body[j] == '"' {
				return j + 1
			}
		}
	case rest[0] == '\'':
		if j := strings.IndexByte(body[start+1:], '\''); j >= 0 {
			return start + 1 + j + 1
		}
	case strings.HasPrefix(rest, "true"), strings.HasPrefix(rest, "false"):
		end := start + 4
		if rest[0] == 'f' {
			end++
		}
		if end == len(body) || body[end] == ' ' || body[end] == '\t' || body[end] == '#' {
			return end
		}
	}
	return -1
}

// projectLine is a setting of a project's table and its value, written as
// TOML.
type projectLine struct{ key, raw string }

// patchProject sets each of lines in the [projects.<key>] table: it
// rewrites a setting's line where the table has it, and adds the others
// just under the table's header. A file with no such table gets one at its
// end, unless every line turns something off, which a project without a
// table has off already.
func patchProject(text, key string, lines []projectLine) string {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	all := strings.SplitAfter(text, "\n")
	header := -1
	done := make([]bool, len(lines))
	in := false
	for i, line := range all {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			path, ok := tablePath(tableName(t))
			in = ok && header < 0 && slices.Equal(path, []string{projectsTable, key})
			if in {
				header = i
			}
			continue
		}
		if !in {
			continue
		}
		for j, l := range lines {
			if !done[j] && isKey(t, l.key) {
				all[i] = setRaw(line, l.raw, l.key+" = "+l.raw)
				done[j] = true
			}
		}
	}
	var add []string
	for j, l := range lines {
		if !done[j] {
			add = append(add, l.key+" = "+l.raw+nl)
		}
	}
	if header >= 0 {
		if len(add) == 0 {
			return strings.Join(all, "")
		}
		if !strings.HasSuffix(all[header], "\n") {
			all[header] += nl
		}
		all = append(all[:header+1], append(add, all[header+1:]...)...)
		return strings.Join(all, "")
	}
	turnsOn := false
	for _, l := range lines {
		turnsOn = turnsOn || l.raw != "false"
	}
	if !turnsOn {
		return text
	}
	out := text
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += nl
	}
	if out != "" {
		out += nl
	}
	out += "[" + projectsTable + "." + key + "]" + nl
	for _, l := range lines {
		out += l.key + " = " + l.raw + nl
	}
	return out
}

// tablePath splits a table's name, as tableName returns it, into its keys:
// bare ones, and quoted ones without escapes, with blanks around the dots.
// It reports false for a name it can't split that way, which then matches
// no table pairtime looks for.
func tablePath(name string) ([]string, bool) {
	var path []string
	rest := strings.TrimSpace(name)
	for {
		var part string
		switch {
		case rest == "":
			return nil, false
		case rest[0] == '"' || rest[0] == '\'':
			end := strings.IndexByte(rest[1:], rest[0])
			if end < 0 {
				return nil, false
			}
			part = rest[1 : 1+end]
			if rest[0] == '"' && strings.ContainsRune(part, '\\') {
				return nil, false
			}
			rest = rest[2+end:]
		default:
			end := strings.IndexAny(rest, ". \t")
			if end < 0 {
				end = len(rest)
			}
			part, rest = rest[:end], rest[end:]
		}
		path = append(path, part)
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return path, true
		}
		if rest[0] != '.' {
			return nil, false
		}
		rest = strings.TrimLeft(rest[1:], " \t")
	}
}

// tomlString returns s as a TOML basic string: in double quotes, with the
// quote, the backslash and the control characters TOML doesn't take in one
// escaped. A label has no control characters (projectname.CleanLabel), so
// they are escaped only to be safe. Every apostrophe is escaped too, so
// pairtime never writes three apostrophes in a row itself, which rewrite
// takes for the start of a multi-line string and refuses to edit after.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\'' || r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "%cu%04X", '\\', r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tableName returns the name a [table] header line opens, as written, or
// "[[...]]" for an array of tables, which never matches a name.
func tableName(header string) string {
	if strings.HasPrefix(header, "[[") {
		return "[[...]]"
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(header, "["), "]")
	return strings.TrimSpace(name)
}

// isKey reports whether the trimmed line t sets key.
func isKey(t, key string) bool {
	rest, ok := strings.CutPrefix(t, key)
	return ok && strings.HasPrefix(strings.TrimLeft(rest, " \t"), "=")
}

// indentOf is a line's leading spaces and tabs.
func indentOf(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// endingOf is a line's line ending: "\r\n", "\n", or "" for the last line
// of a file that doesn't end with one.
func endingOf(line string) string {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(line, "\n"):
		return "\n"
	}
	return ""
}
