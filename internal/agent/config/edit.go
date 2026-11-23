// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/some-du6e/pairtime/internal/agent/home"
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
	byHand := func(name string) string {
		s := fmt.Sprintf("set mode = %q", string(mode))
		if serverURL != "" {
			s += fmt.Sprintf(" and, under [server], url = %q", serverURL)
		}
		return s + " in " + name + " by hand"
	}
	return rewrite(name, byHand, func(text string) string { return patch(text, mode, serverURL) }, func(c *Config) {
		c.Mode = mode
		if serverURL != "" {
			c.Server.URL = serverURL
		}
	})
}

// SetKeepText sets keep_prompt_text under [privacy] in the config.toml at
// name, the "keep prompt text" switch (DESIGN 4.4): what `pairtime privacy
// keep-text off` and `pairtime privacy keep-text on` do. A file with no
// [privacy] table, as an older pairtime wrote, gets one at its end; any
// other change, and a file it refuses, are as SetOnline says.
func SetKeepText(name string, keep bool) error {
	byHand := func(name string) string {
		return fmt.Sprintf("set keep_prompt_text = %t under [privacy] in %s by hand, or, if the file has no [privacy] line, add the lines [privacy] and keep_prompt_text = %t at its end", keep, name, keep)
	}
	return rewrite(name, byHand, func(text string) string { return patchPrivacy(text, keep) }, func(c *Config) { c.Privacy.TextOff = !keep })
}

// rewrite changes the config.toml at name with change, and writes it only
// when it then parses to what it did before with set's change and nothing
// else, as SetOnline says. byHand says what to write by hand in the file it
// names, for an error.
func rewrite(name string, byHand func(name string) string, change func(text string) string, set func(*Config)) error {
	if fi, err := os.Lstat(name); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(name)
		if err != nil {
			return fmt.Errorf("config: config.toml is a link pairtime can't follow (%v), so it didn't change it", err)
		}
		name = target
	}
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		data, err = []byte(defaultFile), nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	if _, err := toml.Decode(text, &fileConfig{}); err != nil {
		return fmt.Errorf("config: config.toml doesn't parse, so pairtime didn't change it (%v); %s", err, byHand(name))
	}
	if strings.Contains(text, `"""`) || strings.Contains(text, `'''`) {
		// A multi-line string could hold a line that looks like a key.
		return fmt.Errorf("config: config.toml has a multi-line string, so pairtime didn't change it; %s", byHand(name))
	}
	before, _ := Parse(data)
	out := change(text)
	after, _ := Parse([]byte(out))
	want := before
	set(&want)
	if !reflect.DeepEqual(after, want) {
		return fmt.Errorf("config: pairtime couldn't change config.toml without changing other settings, so it didn't; %s", byHand(name))
	}
	if out == text {
		return nil
	}
	return home.WriteFile(name, []byte(out))
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
	end := -1
	switch {
	case start < len(body) && body[start] == '"':
		for j := start + 1; j < len(body); j++ {
			if body[j] == '\\' {
				j++
				continue
			}
			if body[j] == '"' {
				end = j + 1
				break
			}
		}
	case start < len(body) && body[start] == '\'':
		if j := strings.IndexByte(body[start+1:], '\''); j >= 0 {
			end = start + 1 + j + 1
		}
	}
	if end < 0 {
		return indentOf(line) + whole + ending
	}
	return body[:start] + `"` + value + `"` + body[end:] + ending
}

// patchPrivacy rewrites the keep_prompt_text line of [privacy], and adds it
// where it is missing: just under the [privacy] header, or with a [privacy]
// table at the end of the file. It finds the table and the switch whatever
// their upper and lower case, as Parse reads them.
func patchPrivacy(text string, keep bool) string {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.SplitAfter(text, "\n")
	value := strconv.FormatBool(keep)
	keyLine := "keep_prompt_text = " + value
	table := ""
	done := false
	header := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "["):
			table = tableName(t)
			if strings.EqualFold(table, "privacy") && header < 0 {
				header = i
			}
		case strings.EqualFold(table, "privacy") && !done && isKeyFold(t, "keep_prompt_text"):
			lines[i] = setBool(line, value, keyLine)
			done = true
		}
	}
	if !done && header >= 0 {
		lines = append(lines[:header+1], append([]string{keyLine + nl}, lines[header+1:]...)...)
		done = true
	}
	out := strings.Join(lines, "")
	if !done {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += nl
		}
		out += nl + "[privacy]" + nl + keyLine + nl
	}
	return out
}

// setBool rewrites the value of a key = true or key = false line as
// setValue does a string's, and writes whole when the old value is
// anything else.
func setBool(line, value, whole string) string {
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
	for _, old := range []string{"true", "false"} {
		rest, ok := strings.CutPrefix(body[start:], old)
		if ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '#') {
			return body[:start] + value + rest + ending
		}
	}
	return indentOf(line) + whole + ending
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

// isKeyFold is isKey, whatever the upper and lower case of key in t.
func isKeyFold(t, key string) bool {
	if len(t) < len(key) || !strings.EqualFold(t[:len(key)], key) {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(t[len(key):], " \t"), "=")
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
