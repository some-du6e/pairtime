// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// Env is the machine install plans for. Planning reads nothing else, so a
// test can plan for any OS in a temporary home.
type Env struct {
	// GOOS is the OS to plan for: "windows", "darwin" or "linux".
	GOOS string
	// Home is the teen's home directory, an absolute path on this machine.
	Home string
	// Getenv reads an environment variable. nil reads none.
	Getenv func(string) string
	// Exe is the absolute path of the pairtime binary that hooks and
	// autostart run: the per-user path of CP 7, where `pairtime install`
	// puts it.
	Exe string
}

// DefaultEnv returns this machine's Env, with exe as the binary's path.
func DefaultEnv(exe string) (Env, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Env{GOOS: runtime.GOOS, Home: h, Getenv: os.Getenv, Exe: exe}, nil
}

func (e Env) getenv(key string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(key)
}

// check says what makes e unusable for planning: a home directory that
// isn't absolute, or a binary path that isn't an absolute path on e.GOOS,
// holds a character no hook config or login item can carry, or names a
// binary that isn't called pairtime or pairtime.exe. Uninstall recognizes
// pairtime's hooks and login items by that name (isPairtimeBinary), so a
// hook written for another name would stay behind once its file changed.
func (e Env) check() error {
	if !filepath.IsAbs(e.Home) {
		return fmt.Errorf("the home directory %q isn't an absolute path", e.Home)
	}
	switch {
	case e.Exe == "":
		return errors.New("no path for the pairtime binary")
	case !utf8.ValidString(e.Exe):
		return fmt.Errorf("the binary's path %q isn't valid UTF-8", e.Exe)
	case strings.ContainsFunc(e.Exe, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return fmt.Errorf("the binary's path %q holds a control character", e.Exe)
	case !isAbs(e.GOOS, e.Exe):
		return fmt.Errorf("the binary's path %q isn't an absolute path on %s", e.Exe, e.GOOS)
	case !isPairtimeBinary(e.Exe):
		return fmt.Errorf("the binary %q isn't called pairtime or pairtime.exe, the name uninstall knows pairtime's hooks and login items by", e.Exe)
	}
	return nil
}

// isAbs reports whether p is an absolute path on goos, which needn't be
// the OS this runs on.
func isAbs(goos, p string) bool {
	if goos == "windows" {
		if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
			c := p[0] | 0x20
			return 'a' <= c && c <= 'z'
		}
		return strings.HasPrefix(p, `\\`)
	}
	return strings.HasPrefix(p, "/")
}

// Item is one thing install offers, which the teen can untick on the
// consent screen (DESIGN 5.9 step 2): the autostart entry, or the hooks of
// one harness.
type Item struct {
	// ID names the item, the same in every release, such as "autostart"
	// or "hooks/claude_code". The manifest records it.
	ID string
	// Summary says in one line what the item does.
	Summary string
	// Edits are the item's changes, in the order they are made.
	Edits []Edit
}

// Edit is one change to one target. It is applied to whatever the target
// holds when the plan is made, so it merges into a file rather than
// replacing it.
type Edit interface {
	// Target is what the edit changes.
	Target() Target
	// Kind names the edit's kind in the manifest, which picks the function
	// uninstall undoes it with.
	Kind() string
	// Apply returns cur with the edit made. Applied to its own result it
	// changes nothing, so a second install plans nothing.
	Apply(cur Content) (Content, error)
}

// An undoer undoes one kind of edit. It returns cur without pairtime's
// change, given base, what the target held before pairtime first touched
// it, and data, what the edit recorded for its undo (Recorder), nil if
// nothing. Uninstall uses it when the target changed since pairtime wrote
// it; otherwise it puts base back as it was.
type undoer func(cur, base Content, data json.RawMessage) (Content, error)

// undoers undo each kind of edit.
var undoers = map[string]undoer{
	kindOwned:       undoOwned,
	kindClaudeHooks: undoClaudeHooks,
}

// Recorder is an Edit that keeps, in the manifest, something its undo needs
// that neither the target nor what it held before tells: the kind of entry
// an autostart edit writes, so that uninstall can tell pairtime's entry from
// another program's that took its place, or the key that identifies the
// line WP-IN3 adds to ~/.wakatime.cfg (DESIGN 5.6), which the agent rewrites
// when its port changes, so that uninstall takes out that line and no
// other. Each install records what its edit returns, in place of what an
// earlier install of the same item recorded.
type Recorder interface {
	Edit
	// Record returns the data as JSON, or nil for none.
	Record() json.RawMessage
}

// Target is one thing install changes: a file, or a value under a key of
// HKEY_CURRENT_USER.
type Target struct {
	// Path is a file's absolute path.
	Path string `json:"path,omitempty"`
	// Key is a registry key under HKEY_CURRENT_USER, such as
	// Software\Microsoft\Windows\CurrentVersion\Run, and Name is a value's
	// name under it.
	Key  string `json:"key,omitempty"`
	Name string `json:"name,omitempty"`
}

// File returns the target for the file at path.
func File(path string) Target { return Target{Path: path} }

// RegistryValue returns the target for the value name under key in
// HKEY_CURRENT_USER.
func RegistryValue(key, name string) Target { return Target{Key: key, Name: name} }

func (t Target) isRegistry() bool { return t.Key != "" }

// String names the target in diffs and messages.
func (t Target) String() string {
	if t.isRegistry() {
		return `HKCU\` + t.Key + `\` + t.Name
	}
	return t.Path
}

// Content is what a target holds.
type Content struct {
	// Exists is false for a file or a registry value that isn't there.
	Exists bool
	// Data is a file's bytes, or a registry value's string as UTF-8.
	Data []byte
	// Mode is a file's permission bits. A rewritten file keeps its mode,
	// and a new one gets 0600.
	Mode fs.FileMode
	// Type is a registry value's type, such as RegSZ, and 0 for a file.
	Type uint32

	// link is true when the file's path is a symbolic link, which reading
	// followed. Only read sets it, so that Apply can check that a path it
	// writes or deletes is still what the plan found there.
	link bool
}

// Same reports whether c and d hold the same thing. A file's mode doesn't
// count, and nor does whether its path is a link (unchanged).
func (c Content) Same(d Content) bool {
	if c.Exists != d.Exists {
		return false
	}
	return !c.Exists || (bytes.Equal(c.Data, d.Data) && c.Type == d.Type)
}

// unchanged reports whether c, what a target holds when Apply reads it
// again, is still d, what it held when the plan read it: the same thing,
// and for a file the same mode and a path that is a link or not as it
// was. Apply writes the file with the mode the plan read, so a mode
// changed in between, such as settings.json made private, would otherwise
// be undone; and it deletes a file pairtime created by its path, which
// would delete a link the teen put there since, and leave pairtime's hooks
// in the file it points to. Same, which tells whether an edit changes
// anything, ignores both.
func (c Content) unchanged(d Content) bool {
	return c.Same(d) && c.Mode == d.Mode && c.link == d.link
}

// digest identifies c in the manifest: "" for nothing there, and otherwise
// the SHA-256 of the data, with the type of a registry value.
func (c Content) digest() string {
	if !c.Exists {
		return ""
	}
	h := sha256.New()
	if c.Type != 0 {
		fmt.Fprintf(h, "registry value of type %d\n", c.Type)
	}
	h.Write(c.Data)
	return hex.EncodeToString(h.Sum(nil))
}

// text is c as a diff shows it: a file's bytes, or a registry value on a
// line of its own.
func (c Content) text() []byte {
	switch {
	case !c.Exists:
		return nil
	case c.Type == 0:
		return c.Data
	case c.Type == RegSZ:
		return []byte(string(c.Data) + "\n")
	default:
		return []byte(fmt.Sprintf("%s (registry type %d)\n", c.Data, c.Type))
	}
}

// Installer plans and makes changes. It keeps what uninstall needs in Dir:
// manifest.json, which records every target pairtime wrote and what it
// wrote there, and backups/, a copy of what each target held before.
type Installer struct {
	// Dir is pairtime's install directory, inside its data directory.
	Dir string
	// Registry is HKEY_CURRENT_USER on Windows and nil elsewhere.
	Registry Registry
}

// New returns the installer for pairtime's directories dirs, keeping its
// files in an install directory in the data directory (DESIGN 3.5).
func New(dirs home.Dirs) *Installer {
	return &Installer{Dir: filepath.Join(dirs.Data, "install"), Registry: systemRegistry()}
}

func (in *Installer) manifestPath() string { return filepath.Join(in.Dir, "manifest.json") }

func (in *Installer) backupsDir() string { return filepath.Join(in.Dir, "backups") }
