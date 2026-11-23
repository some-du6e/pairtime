// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/some-du6e/pairtime/internal/adapters"
)

// Where a T3 home came from (Store.From).
const (
	// FromEnv is T3CODE_HOME in the agent's own environment.
	FromEnv = "env"
	// FromService is the T3CODE_HOME T3's background service sets, in its
	// systemd unit on Linux or its launchd plist on macOS.
	FromService = "service"
	// FromDefault is <home>/.t3, T3's home when nothing names another.
	FromDefault = "default"
)

// Store is a place T3 Code's store may be: statev2.sqlite in the userdata
// folder of a T3 home (DESIGN 5.1).
type Store struct {
	// Path is the store, <T3 home>/userdata/statev2.sqlite, an absolute
	// path. It is local, like every path: no log line or doctor line names
	// it.
	Path string
	// From says where its T3 home came from: FromEnv, FromService or
	// FromDefault.
	From string
}

// The names T3's background service is installed under
// (apps/server/src/cloud/bootService.ts at T3 d8f3feff).
const (
	systemdUnit  = "t3code.service"
	launchdPlist = "com.t3tools.t3code.service.plist"
)

// Locate returns the places T3 Code's store may be, in the order the reader
// looks at them: the T3 home T3CODE_HOME names in env's environment, the one
// T3's background service sets in its systemd unit or launchd plist, and
// <home>/.t3. A home on a WSL distro's share, \\wsl$ or \\wsl.localhost, is
// left out: pairtime reads a WSL backend's store inside that distro, never
// over the share (DESIGN 5.1). Each store is listed once. Locate reads the
// service's unit file and nothing else; whether a store exists is the
// reader's to check on each read.
func Locate(env adapters.Env) []Store {
	var out []Store
	add := func(home, from string) {
		if home == "" || onWSLShare(home) {
			return
		}
		p := filepath.Join(home, "userdata", "statev2.sqlite")
		for _, s := range out {
			if s.Path == p {
				return
			}
		}
		out = append(out, Store{Path: p, From: from})
	}
	getenv := func(k string) string {
		if env.Getenv == nil {
			return ""
		}
		return env.Getenv(k)
	}
	add(resolveHome(getenv("T3CODE_HOME"), env.Home), FromEnv)
	if raw, ok := serviceHome(env, getenv); ok {
		add(resolveHome(raw, env.Home), FromService)
	}
	if env.Home != "" {
		add(filepath.Join(env.Home, ".t3"), FromDefault)
	}
	return out
}

// resolveHome is T3's resolveBaseDir for a T3CODE_HOME value raw: trimmed,
// with a leading ~ for home, and absolute (apps/server/src/os-jank.ts). T3
// resolves a relative value against its own working directory, which for
// its background service is the home folder (WorkingDirectory=%h), so a
// relative value is taken against home here. An empty value names none.
func resolveHome(raw, home string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return ""
	case raw == "~":
		raw = home
	case strings.HasPrefix(raw, "~/"), strings.HasPrefix(raw, `~\`):
		raw = filepath.Join(home, raw[2:])
	}
	if onWSLShare(raw) {
		return raw
	}
	if !filepath.IsAbs(raw) {
		if home == "" {
			return ""
		}
		raw = filepath.Join(home, raw)
	}
	return filepath.Clean(raw)
}

// onWSLShare reports whether p is on the share Windows serves a WSL
// distro's files from.
func onWSLShare(p string) bool {
	s := strings.ToLower(strings.ReplaceAll(p, "/", `\`))
	if rest, ok := strings.CutPrefix(s, `\\?\unc\`); ok {
		s = `\\` + rest
	}
	for _, share := range []string{`\\wsl$`, `\\wsl.localhost`} {
		if s == share || strings.HasPrefix(s, share+`\`) {
			return true
		}
	}
	return false
}

// serviceHome returns the T3CODE_HOME T3's background service sets, when
// its unit file is there and sets one. Windows' Task Scheduler task passes
// the home as a flag instead, which the reader doesn't read.
func serviceHome(env adapters.Env, getenv func(string) string) (string, bool) {
	if env.Home == "" {
		return "", false
	}
	switch env.GOOS {
	case "linux":
		dir := getenv("XDG_CONFIG_HOME")
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(env.Home, ".config")
		}
		data, ok := readSmall(filepath.Join(dir, "systemd", "user", systemdUnit))
		if !ok {
			return "", false
		}
		return unitHome(data, env.Home)
	case "darwin":
		data, ok := readSmall(filepath.Join(env.Home, "Library", "LaunchAgents", launchdPlist))
		if !ok {
			return "", false
		}
		return plistHome(data)
	}
	return "", false
}

// maxUnit bounds how much of a unit file Locate reads. T3's are under 2 KiB.
const maxUnit = 64 << 10

// readSmall reads a unit file of at most maxUnit bytes.
func readSmall(name string) ([]byte, bool) {
	f, err := os.Open(name)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUnit+1))
	if err != nil || len(data) > maxUnit {
		return nil, false
	}
	return data, true
}

// unitHome reads T3CODE_HOME from a systemd unit's Environment= lines, as
// systemd reads them: each line a list of assignments split at whitespace,
// with double and single quotes and backslash escapes, and specifiers, of
// which T3 writes only %% (bootService.ts quoteSystemdValue). %h is home;
// any other specifier makes the value unreadable. A later assignment wins.
func unitHome(data []byte, home string) (string, bool) {
	var val string
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		rest, ok := strings.CutPrefix(line, "Environment=")
		if !ok {
			continue
		}
		for _, word := range systemdWords(rest) {
			v, ok := strings.CutPrefix(word, "T3CODE_HOME=")
			if !ok {
				continue
			}
			v, ok = specifiers(v, home)
			if !ok {
				found, val = false, ""
				continue
			}
			found, val = true, v
		}
	}
	return val, found
}

// systemdWords splits an Environment= value into its assignments.
func systemdWords(s string) []string {
	var words []string
	var w strings.Builder
	in := false // inside a word
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			if in {
				words = append(words, w.String())
				w.Reset()
				in = false
			}
		case c == '\\' && i+1 < len(s):
			in = true
			i++
			w.WriteByte(s[i])
		case c == '"' || c == '\'':
			in = true
			for i++; i < len(s) && s[i] != c; i++ {
				if c == '"' && s[i] == '\\' && i+1 < len(s) {
					i++
				}
				w.WriteByte(s[i])
			}
		default:
			in = true
			w.WriteByte(c)
		}
	}
	if in {
		words = append(words, w.String())
	}
	return words
}

// specifiers expands a unit value's specifiers: %% and %h.
func specifiers(v, home string) (string, bool) {
	if !strings.Contains(v, "%") {
		return v, true
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '%' {
			b.WriteByte(v[i])
			continue
		}
		if i+1 >= len(v) {
			return "", false
		}
		i++
		switch v[i] {
		case '%':
			b.WriteByte('%')
		case 'h':
			b.WriteString(home)
		default:
			return "", false
		}
	}
	return b.String(), true
}

// plistHome reads T3CODE_HOME from a launchd plist: the string that follows
// the key T3CODE_HOME, which T3 writes in its EnvironmentVariables dict.
func plistHome(data []byte) (string, bool) {
	d := xml.NewDecoder(bytes.NewReader(data))
	var key string
	var text strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			text.Reset()
			if t.Name.Local != "string" && t.Name.Local != "key" {
				key = ""
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			switch t.Name.Local {
			case "key":
				key = text.String()
				continue
			case "string":
				if key == "T3CODE_HOME" {
					return text.String(), true
				}
			}
			key = ""
		}
	}
}

// exists reports whether name is there.
func exists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// running reports whether T3 holds the store at path open: its -wal and
// -shm files exist, as they do while T3 runs. A read-only open of a store
// without them would make both in T3's folder (DESIGN 5.1), so the reader
// reads only while they are there.
func running(path string) bool {
	return exists(path+"-wal") && exists(path+"-shm")
}
