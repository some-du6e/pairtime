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
	// systemd unit or a drop-in beside it on Linux, or its launchd plist on
	// macOS.
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
// T3's background service sets in its systemd unit, or a drop-in beside
// it, or its launchd plist, and <home>/.t3. A home on a WSL distro's share,
// \\wsl$ or \\wsl.localhost, is left out: pairtime reads a WSL backend's
// store inside that distro, never over the share (DESIGN 5.1). Each store
// is listed once. Locate reads the service's unit files or plist and
// nothing else; whether a store exists is the reader's to check on each
// read.
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
// its unit file is there and it or a drop-in beside it sets one
// (unitFiles), or its launchd plist does. Windows' Task Scheduler task
// passes the home as a flag instead, which the reader doesn't read.
func serviceHome(env adapters.Env, getenv func(string) string) (string, bool) {
	if env.Home == "" {
		return "", false
	}
	switch env.GOOS {
	case "linux":
		data, ok := unitFiles(env.Home, getenv)
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

// systemdUnitPath is where T3's background service's systemd unit is:
// under XDG_CONFIG_HOME, or ~/.config while that names no absolute folder.
func systemdUnitPath(home string, getenv func(string) string) string {
	dir := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", systemdUnit)
}

// maxDropIns bounds how many drop-ins of T3's unit unitFiles reads.
const maxDropIns = 32

// unitFiles returns T3's systemd unit and the drop-ins beside it, which is
// where systemctl --user edit puts its lines, as one text in the order
// systemd reads them, or false when there is no unit. A drop-in's
// assignments come after the unit's, and systemd reads the drop-ins in the
// order of their names, so a later one wins. Drop-ins in systemd's other
// folders, such as /etc/systemd/user, aren't read.
func unitFiles(home string, getenv func(string) string) ([]byte, bool) {
	unit := systemdUnitPath(home, getenv)
	data, ok := readSmall(unit)
	if !ok {
		return nil, false
	}
	if entries, err := os.ReadDir(unit + ".d"); err == nil {
		n := 0
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") || n == maxDropIns {
				continue
			}
			n++
			if d, ok := readSmall(filepath.Join(unit+".d", e.Name())); ok {
				data = append(append(data, '\n'), d...)
			}
		}
	}
	return data, true
}

// ServiceClaudeHome returns the Claude home T3 Code's background service
// sets with CLAUDE_CONFIG_DIR in its own environment, resolved as T3
// resolves a home (resolveHome), when it sets one: in its systemd unit or
// the drop-ins beside it, in the order systemd reads them, which is where
// systemctl --user edit puts an Environment= line, on Linux, and in its
// launchd plist on macOS. T3's own unit and plist set none, but one a
// person edited can, and then every Claude Code that T3's server runs for
// an instance with no home of its own reads its settings, pairtime's hooks
// among them, from that home (DESIGN 5.1, 5.9). Doctor checks that home as
// it checks an instance's (ClaudeHomes). Windows' Task Scheduler task sets
// no environment. It reads those files alone, and returns a path that,
// like every path, is local.
func ServiceClaudeHome(env adapters.Env) (string, bool) {
	if env.Home == "" {
		return "", false
	}
	getenv := func(k string) string {
		if env.Getenv == nil {
			return ""
		}
		return env.Getenv(k)
	}
	var raw string
	found := false
	switch env.GOOS {
	case "linux":
		data, ok := unitFiles(env.Home, getenv)
		if !ok {
			return "", false
		}
		raw, found = unitVar(data, env.Home, claudeHomeVar)
	case "darwin":
		data, ok := readSmall(filepath.Join(env.Home, "Library", "LaunchAgents", launchdPlist))
		if !ok {
			return "", false
		}
		raw, found = plistVar(data, claudeHomeVar)
	}
	if !found {
		return "", false
	}
	h := resolveHome(raw, env.Home)
	return h, h != ""
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

// unitHome reads T3CODE_HOME from a systemd unit's Environment= lines
// (unitVar).
func unitHome(data []byte, home string) (string, bool) { return unitVar(data, home, "T3CODE_HOME") }

// unitVar reads the variable name from a systemd unit's Environment= and
// UnsetEnvironment= lines, as systemd reads them: each line a list of
// assignments split at whitespace, with double and single quotes and
// backslash escapes, and specifiers, of which T3 writes only %%
// (bootService.ts quoteSystemdValue). %h is home; any other specifier
// makes the value unreadable. A later assignment wins, and an empty
// Environment= drops every assignment before it. UnsetEnvironment= takes
// the variable out, by its name or by one assignment of it, wherever its
// line stands, since systemd applies it last, and an empty one drops the
// names listed before it. An EnvironmentFile= isn't read, so a variable
// only such a file sets isn't found.
func unitVar(data []byte, home, name string) (string, bool) {
	var val string
	found := false
	var unset []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Environment":
			if strings.TrimSpace(rest) == "" {
				found, val = false, ""
				continue
			}
			for _, word := range systemdWords(rest) {
				v, ok := strings.CutPrefix(word, name+"=")
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
		case "UnsetEnvironment":
			if strings.TrimSpace(rest) == "" {
				unset = nil
				continue
			}
			for _, word := range systemdWords(rest) {
				if word == name || strings.HasPrefix(word, name+"=") {
					unset = append(unset, word)
				}
			}
		}
	}
	for _, u := range unset {
		if u == name {
			return "", false
		}
		if v, ok := specifiers(strings.TrimPrefix(u, name+"="), home); found && ok && v == val {
			return "", false
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

// plistHome reads T3CODE_HOME from a launchd plist (plistVar).
func plistHome(data []byte) (string, bool) { return plistVar(data, "T3CODE_HOME") }

// plistVar reads the variable name from a launchd plist: the string that
// follows the key name, which T3 writes in its EnvironmentVariables dict.
// It reads the XML form T3 writes; a plist that defaults write or plutil
// turned into the binary form doesn't parse, so it gives nothing.
func plistVar(data []byte, name string) (string, bool) {
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
				if key == name {
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
// reads only while they are there. Whether T3 runs takes its server's
// process as well (Status.Running), since a T3 killed while it held the
// store leaves both files behind.
func running(path string) bool {
	return exists(path+"-wal") && exists(path+"-shm")
}

// walFiles is what a stat of a store's -wal and -shm files says: their
// sizes and modification times, Unix ns. T3 writes the -wal for every
// change it commits, so while both say the same T3 has written nothing.
type walFiles struct {
	walSize, walTime int64
	shmSize, shmTime int64
}

// walOf stats the -wal and -shm files of the store at path.
func walOf(path string) (walFiles, bool) {
	wal, err := os.Stat(path + "-wal")
	if err != nil {
		return walFiles{}, false
	}
	shm, err := os.Stat(path + "-shm")
	if err != nil {
		return walFiles{}, false
	}
	return walFiles{walSize: wal.Size(), walTime: wal.ModTime().UnixNano(), shmSize: shm.Size(), shmTime: shm.ModTime().UnixNano()}, true
}
