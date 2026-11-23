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
	"strconv"
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
	byHand := func(name, _ string) string {
		s := fmt.Sprintf("set mode = %q", string(mode))
		if serverURL != "" {
			s += fmt.Sprintf(" and, under [server], url = %q", serverURL)
		}
		return s + " in " + name + " by hand"
	}
	return rewrite(name, false, byHand, func(text string) string { return patch(text, mode, serverURL) }, func(c *Config) {
		c.Mode = mode
		if serverURL != "" {
			c.Server.URL = serverURL
		}
	})
}

// SetKeepText sets keep_prompt_text under [privacy] in the config.toml at
// name, the "keep prompt text" switch (DESIGN 4.4): what `pairtime privacy
// keep-text off` and `pairtime privacy keep-text on` do. A file with no
// [privacy] table, as an older pairtime wrote, gets one at its end, and a
// switch written as a dotted key at the top of the file is changed where
// it is; any other change, and a file it refuses, are as SetOnline says.
// Turning the switch off writes the file even when it says so already, so
// a running agent reads it again and tries again to delete the text it
// kept, as after a try that failed.
func SetKeepText(name string, keep bool) error {
	byHand := func(name, text string) string {
		var file fileConfig
		if _, err := toml.Decode(strings.TrimPrefix(text, bom), &file); err == nil && file.Privacy.KeepPromptText != nil {
			// Where the switch is, as in an inline table: adding it again
			// would set it twice.
			return fmt.Sprintf("change keep_prompt_text to %t where %s sets it, by hand", keep, name)
		}
		return fmt.Sprintf("set keep_prompt_text = %t under [privacy] in %s by hand, or, if the file has no [privacy] line, add the lines [privacy] and keep_prompt_text = %t at its end", keep, name, keep)
	}
	return rewrite(name, !keep, byHand, func(text string) string { return patchPrivacy(text, keep) }, func(c *Config) { c.Privacy.TextOff = !keep })
}

// SetShadow sets shadow under [display] in the config.toml at name to on,
// display.shadow (DESIGN 6.7 step 2): what pairtime install
// --calibration-volunteer does. A [display] table without the setting
// gets it just under its header, a file with no [display] table gets one
// at its end, and a display.shadow written as a dotted key at the top of
// the file is changed where it is. Any other change, a file that says so
// already, which it leaves alone, and a file it refuses are as SetOnline
// says. It edits what the file says, also after the scoring freeze, when
// Parse reads the setting as off.
func SetShadow(name string, on bool) error {
	change, apply := shadowEdit(on)
	return rewrite(name, false, shadowByHand(on), change, apply)
}

// PlanShadow returns what SetShadow(name, on) would do to the config.toml
// at name, and writes nothing: the file as it is, nil when there is none,
// and the file as SetShadow would write it, nil when it would write none.
// The two are the same when SetShadow would leave the file alone. It
// refuses what SetShadow refuses, with the same error. The file can still
// change before SetShadow runs, which then makes the same change to it.
func PlanShadow(name string, on bool) (now, then []byte, err error) {
	if name, err = editTarget(name); err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(name)
	text := string(data)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		data, text = nil, defaultFile
	case err != nil:
		return nil, nil, err
	}
	change, apply := shadowEdit(on)
	out, err := edited(text, shadowByHand(on)(name, text), change, apply)
	switch {
	case err != nil:
		return nil, nil, err
	case data == nil && out == text:
		// No file, and the default says so already: SetShadow writes none.
		return nil, nil, nil
	}
	return data, []byte(out), nil
}

// shadowEdit is SetShadow's change to a config.toml's text, and what it
// changes in the file's Config.
func shadowEdit(on bool) (change func(text string) string, apply func(*Config)) {
	return func(text string) string { return patchDisplay(text, on) }, func(c *Config) { c.Display.Shadow = on }
}

// shadowByHand says what to write by hand when SetShadow can't, for the
// file name, which holds text.
func shadowByHand(on bool) func(name, text string) string {
	return func(name, text string) string {
		body := strings.TrimPrefix(text, bom)
		switch md, err := toml.Decode(body, &fileConfig{}); {
		case displayVariant(body):
			// Parse reads [display] and shadow only as written in lower
			// case, and the decoder would put either spelling in.
			return fmt.Sprintf("write the table as [display] and the setting as shadow, in lower case, with shadow = %t, in %s by hand", on, name)
		case err == nil && md.IsDefined("display", "shadow"):
			// Where the setting is, as in an inline table: adding it again
			// would set it twice.
			return fmt.Sprintf("change shadow to %t where %s sets it, by hand", on, name)
		}
		return fmt.Sprintf("set shadow = %t under [display] in %s by hand, or, if the file has no [display] line, add the lines [display] and shadow = %t at its end", on, name, on)
	}
}

// patchDisplay rewrites the shadow line of [display], or the
// display.shadow line at the top of the file, and adds it where it is
// missing: just under the [display] header, beside the display. lines at
// the top that make the table without one, or with a [display] table at
// the end of the file. Parse reads the table and the setting only as
// written in lower case, so those are the ones it looks for, and a file
// that spells either in another case comes back as it is (displayVariant),
// which edited then refuses.
func patchDisplay(text string, on bool) string {
	if displayVariant(text) {
		return text
	}
	if cfg, _, _ := parseAsWritten([]byte(text)); cfg.Display.Shadow == on {
		// The file says so already, wherever it says it, such as in an
		// inline table, which a [display] header added after it would
		// define twice.
		return text
	}
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.SplitAfter(text, "\n")
	value := strconv.FormatBool(on)
	keyLine := "shadow = " + value
	table := ""
	done := false
	header, dotted := -1, -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "["):
			table = tableName(t)
			if table == "display" && header < 0 {
				header = i
			}
		case table == "display" && !done && isKey(t, "shadow"):
			lines[i] = setBool(line, value, keyLine)
			done = true
		case table == "" && isComment(t):
		case table == "" && len(keyParts(t)) > 1 && keyParts(t)[0] == "display":
			dotted = i
			if !done && slices.Equal(keyParts(t), []string{"display", "shadow"}) {
				lines[i] = setBool(line, value, "display."+keyLine)
				done = true
			}
		}
	}
	if !done && header < 0 && dotted >= 0 {
		// The table is made of dotted keys, and a header would make it twice.
		if !strings.HasSuffix(lines[dotted], "\n") {
			lines[dotted] += nl // the file's last line
		}
		lines = slices.Insert(lines, dotted+1, "display."+keyLine+nl)
		done = true
	}
	if !done && header >= 0 {
		if !strings.HasSuffix(lines[header], "\n") {
			lines[header] += nl // the file's last line
		}
		lines = slices.Insert(lines, header+1, keyLine+nl)
		done = true
	}
	out := strings.Join(lines, "")
	if !done {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += nl
		}
		if out != "" {
			out += nl
		}
		out += "[display]" + nl + keyLine + nl
	}
	return out
}

// displayVariant reports whether text spells the [display] table, or the
// shadow setting in it, in another upper and lower case than Parse reads,
// as a header, at the top of the file, or under [display]: [Display], a
// top-level Display.shadow, or Shadow under [display]. The decoder takes
// any case, and with two spellings it is chance which value wins.
func displayVariant(text string) bool {
	variant := func(got, want string) bool { return got != want && strings.EqualFold(got, want) }
	table := ""
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "["):
			table = tableName(t)
			if path, ok := tablePath(table); ok && variant(path[0], "display") {
				return true
			}
		case isComment(t):
		case table == "":
			parts := keyParts(t)
			if len(parts) > 0 && variant(parts[0], "display") {
				return true
			}
			if len(parts) == 2 && parts[0] == "display" && variant(parts[1], "shadow") {
				return true
			}
		case table == "display":
			if parts := keyParts(t); len(parts) > 0 && variant(parts[0], "shadow") {
				return true
			}
		}
	}
	return false
}

// keyParts returns the parts of the key the trimmed line t sets, without
// their quotes, such as display and shadow for display.shadow = true, or
// nil for a line that sets none.
func keyParts(t string) []string {
	key, _, ok := strings.Cut(t, "=")
	if !ok {
		return nil
	}
	var parts []string
	for _, p := range strings.Split(key, ".") {
		parts = append(parts, unquote(strings.TrimSpace(p)))
	}
	return parts
}

// isComment reports whether the trimmed line t is a comment or empty.
func isComment(t string) bool { return t == "" || strings.HasPrefix(t, "#") }

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
	byHand := func(name, _ string) string {
		return fmt.Sprintf("set name = %s and online = true under [projects.%s] in %s by hand", tomlString(label), key, name)
	}
	return rewrite(name, false, byHand, func(text string) string {
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
	byHand := func(name, _ string) string {
		return fmt.Sprintf("set online = false under [projects.%s] in %s by hand", key, name)
	}
	return rewrite(name, false, byHand, func(text string) string {
		return patchProject(text, key, []projectLine{{"online", "false"}})
	}, func(c *Config) {
		if p, ok := c.Projects[key]; ok {
			p.Online = false
			c.Projects[key] = p
		}
	})
}

// bom is the byte order mark some editors start a UTF-8 file with, which
// TOML allows.
const bom = "\ufeff"

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

// rewrite changes the config.toml at name with change, and writes it only
// when it then parses to what it did before with apply's change and
// nothing else, as SetOnline says, and when that changed it, or, as best
// it can, when always is set. change gets the text without its byte order
// mark, which the file keeps. byHand says what to write by hand in the
// file it names, which holds text, for an error.
//
// It holds editMu, for this process's edits, and config.lock's lock, for
// every other pairtime process's, as `pairtime projects show-name` beside
// the running agent, from its read to its rename, so neither change undoes
// the other. It also reads the file again just before it replaces it, and
// starts over from the new text when it changed meanwhile, as an editor's
// save does, which takes no lock.
func rewrite(name string, always bool, byHand func(name, text string) string, change func(text string) string, apply func(*Config)) error {
	editMu.Lock()
	defer editMu.Unlock()
	lock := filepath.Join(filepath.Dir(name), LockName)
	target, err := editTarget(name)
	if err != nil {
		return err
	}
	name = target
	release, lockErr := home.LockFile(lock, lockWait)
	if errors.Is(lockErr, home.ErrBusy) {
		return fmt.Errorf("config: pairtime couldn't lock config.toml to change it (%v), so it didn't; try again, or %s", lockErr, byHand(name, ""))
	}
	if lockErr != nil {
		// The lock file can't be made, as in a config directory that
		// doesn't exist or can't be written, where no other process changes
		// config.toml either: an edit that changes nothing goes on, and one
		// that would change it fails.
		release = func() error { return nil }
	}
	defer release()
	for try := 0; ; try++ {
		data, err := readEdited(name)
		if err != nil {
			return err
		}
		hand := byHand(name, string(data))
		out, err := edited(string(data), hand, change, apply)
		if err != nil {
			return err
		}
		same := out == string(data)
		if same && !always {
			return nil
		}
		if lockErr != nil && !same {
			return fmt.Errorf("config: pairtime couldn't lock config.toml to change it (%v), so it didn't; %s", lockErr, hand)
		}
		if beforeReplace != nil {
			beforeReplace(name)
		}
		now, err := readEdited(name)
		if err != nil {
			return err
		}
		if bytes.Equal(now, data) {
			err := home.WriteFile(name, []byte(out))
			if same {
				// The file says it already, so a write that fails, as in a
				// folder that can't be written, changes nothing that
				// matters: a running agent reads it again at its next start.
				return nil
			}
			return err
		}
		if try == 2 {
			return fmt.Errorf("config: config.toml kept changing while pairtime changed it, so it didn't; try again, or %s", hand)
		}
	}
}

// editTarget returns the file an edit of the config.toml at name changes:
// the file a symlink there points to, so the link stays a link, or name
// itself. A link that points nowhere, or that can't be followed, is an
// error, since writing a new file in its place would undo the link.
func editTarget(name string) (string, error) {
	fi, err := os.Lstat(name)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		return name, nil
	}
	target, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", fmt.Errorf("config: config.toml is a link pairtime can't follow (%v), so it didn't change it", err)
	}
	return target, nil
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

// UnparsedError is the error an edit returns for a config.toml that
// doesn't parse. Its Error quotes the decoder's message, which can hold
// what the teen wrote in the file, such as a key or a value it couldn't
// read. A screen the teen may share, as pairtime install's consent screen,
// shows only Line and Hand instead.
type UnparsedError struct {
	// Line is the line the file stops parsing at, or the line of a value
	// of the wrong type, or 0 when the decoder names no line.
	Line int
	// Hand says what to write in the file by hand instead.
	Hand string
	err  error
}

func (e *UnparsedError) Error() string {
	return fmt.Sprintf("config: config.toml doesn't parse, so pairtime didn't change it (%v); %s", e.err, e.Hand)
}

// Unwrap returns the decoder's error.
func (e *UnparsedError) Unwrap() error { return e.err }

// edited returns text with change made to it without its byte order mark,
// which it keeps, once it checked that the result parses to what text
// parsed to with apply done to it, and to nothing else. hand says what to
// write by hand instead, for an error.
func edited(text, hand string, change func(text string) string, apply func(*Config)) (string, error) {
	if _, err := toml.Decode(text, &fileConfig{}); err != nil {
		u := &UnparsedError{Hand: hand, err: err}
		var pe toml.ParseError
		if errors.As(err, &pe) {
			u.Line = pe.Position.Line
		} else {
			// A value of the wrong type fails as the decoder fills
			// fileConfig, with a plain error that starts "toml: line N"
			// when the decoder knows the key's line. That prefix holds
			// nothing from the file.
			fmt.Sscanf(err.Error(), "toml: line %d", &u.Line)
		}
		return "", u
	}
	before, _, _ := parseAsWritten([]byte(text))
	body, found := strings.CutPrefix(text, bom)
	out := change(body)
	if out != body && (strings.Contains(text, `"""`) || strings.Contains(text, `'''`)) {
		// A multi-line string could hold a line that looks like a key, so
		// such a file is changed only when the change rewrites nothing, as
		// in one that says so already: then nothing in it is mis-edited.
		return "", fmt.Errorf("config: config.toml has a multi-line string, so pairtime didn't change it; %s", hand)
	}
	if found {
		out = bom + out
	}
	after, _, _ := parseAsWritten([]byte(out))
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
		return "", fmt.Errorf("config: pairtime couldn't change config.toml without changing other settings, so it didn't; %s", hand)
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
		if !strings.HasSuffix(lines[serverHeader], "\n") {
			lines[serverHeader] += nl // the file's last line
		}
		lines = slices.Insert(lines, serverHeader+1, urlLine+nl)
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

// patchPrivacy rewrites the keep_prompt_text line of [privacy], or the
// privacy.keep_prompt_text line at the top of the file, and adds it where
// it is missing: just under the [privacy] header, beside the privacy.
// lines at the top that make the table without one, or with a [privacy]
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
	header, dotted := -1, -1
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
		case table == "" && inPrivacyDotted(t):
			dotted = i
			if !done && dottedKey(t, "keep_prompt_text") {
				lines[i] = setBool(line, value, "privacy."+keyLine)
				done = true
			}
		}
	}
	if !done && header < 0 && dotted >= 0 {
		// The table is made of dotted keys, and a header would make it twice.
		if !strings.HasSuffix(lines[dotted], "\n") {
			lines[dotted] += nl // the file's last line
		}
		lines = slices.Insert(lines, dotted+1, "privacy."+keyLine+nl)
		done = true
	}
	if !done && header >= 0 {
		if !strings.HasSuffix(lines[header], "\n") {
			lines[header] += nl // the file's last line
		}
		lines = slices.Insert(lines, header+1, keyLine+nl)
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

// inPrivacyDotted reports whether the trimmed line t, at the top of the
// file, sets a key of [privacy] as a dotted key, such as
// privacy.keep_prompt_text = false.
func inPrivacyDotted(t string) bool {
	key, _, ok := strings.Cut(t, "=")
	if !ok {
		return false
	}
	first, _, ok := strings.Cut(key, ".")
	return ok && strings.EqualFold(unquote(strings.TrimSpace(first)), "privacy")
}

// dottedKey reports whether the trimmed line t sets privacy.<key>, with
// key as its whole second part, whatever their upper and lower case.
func dottedKey(t, key string) bool {
	k, _, _ := strings.Cut(t, "=")
	parts := strings.Split(k, ".")
	return len(parts) == 2 && strings.EqualFold(unquote(strings.TrimSpace(parts[0])), "privacy") &&
		strings.EqualFold(unquote(strings.TrimSpace(parts[1])), key)
}

// unquote takes the quotes off a quoted key part.
func unquote(part string) string {
	if len(part) >= 2 && (part[0] == '"' && part[len(part)-1] == '"' || part[0] == '\'' && part[len(part)-1] == '\'') {
		return part[1 : len(part)-1]
	}
	return part
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
