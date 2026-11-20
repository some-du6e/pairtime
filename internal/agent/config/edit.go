// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"reflect"
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

// SetOnline switches the config.toml at name to mode "online" with [server]
// url serverURL, which CheckServerURL must accept: what `pairtime login`
// does once it has linked the install (DESIGN 3.2). SetLocal switches it
// back to mode "local" and leaves [server] as it is: what `pairtime
// logout` does. A missing file is written from the default first.
//
// Both change only those lines, so the teen's comments, order, line
// endings and other settings stay byte for byte. They refuse to write a
// file they can't change that way, such as one that doesn't parse, or one
// where the edit would change any other setting; then the teen edits it by
// hand, and the error says what to write.
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
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		data, err = []byte(defaultFile), nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	byHand := fmt.Sprintf("set mode = %q", string(mode))
	if serverURL != "" {
		byHand += fmt.Sprintf(" and, under [server], url = %q", serverURL)
	}
	byHand += " in " + name + " by hand"
	if _, err := toml.Decode(text, &fileConfig{}); err != nil {
		return fmt.Errorf("config: config.toml doesn't parse, so pairtime didn't change it (%v); %s", err, byHand)
	}
	if strings.Contains(text, `"""`) || strings.Contains(text, `'''`) {
		// A multi-line string could hold a line that looks like a key.
		return fmt.Errorf("config: config.toml has a multi-line string, so pairtime didn't change it; %s", byHand)
	}
	before, _ := Parse(data)
	out := patch(text, mode, serverURL)
	after, _ := Parse([]byte(out))
	want := before
	want.Mode = mode
	if serverURL != "" {
		want.Server.URL = serverURL
	}
	if !reflect.DeepEqual(after, want) {
		return fmt.Errorf("config: pairtime couldn't change config.toml without changing other settings, so it didn't; %s", byHand)
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
			lines[i] = indentOf(line) + modeLine + endingOf(line)
			modeDone = true
		case table == "server" && !urlDone && isKey(t, "url"):
			lines[i] = indentOf(line) + urlLine + endingOf(line)
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
