// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// BinaryPath returns the per-user path that `pairtime install` puts the
// binary at, and so the one that every hook and the login item run (CP 7):
// %LOCALAPPDATA%\pairtime\bin\pairtime.exe on Windows, and
// ~/.local/bin/pairtime on macOS and Linux. Neither needs admin rights.
func BinaryPath(env Env) (string, error) {
	switch env.GOOS {
	case "windows":
		local := env.getenv("LOCALAPPDATA")
		if !isAbs("windows", local) {
			return "", errors.New("LOCALAPPDATA isn't an absolute path, so pairtime has nowhere to put its binary")
		}
		return filepath.Join(local, "pairtime", "bin", "pairtime.exe"), nil
	case "darwin", "linux":
		if !filepath.IsAbs(env.Home) {
			return "", fmt.Errorf("the home directory %q isn't an absolute path", env.Home)
		}
		return filepath.Join(env.Home, ".local", "bin", "pairtime"), nil
	}
	return "", fmt.Errorf("pairtime has no per-user place for its binary on %s", env.GOOS)
}

// Harness is an AI harness that install looks for (DESIGN 5.1, 5.9 step
// 2). Only some of them have hooks that install can add so far; the
// consent screen names the others, so that a teen who uses one knows
// pairtime doesn't track it yet.
type Harness struct {
	// Name is the harness's name in `pairtime hook --harness`, such as
	// claude_code (DESIGN 3.4).
	Name string
	// Title is its name for people, such as "Claude Code".
	Title string
	// Where is what showed that it is on this machine: its folder, or its
	// program on the PATH. It is "" when install didn't find it.
	Where string
	// Item is the ID of the item that adds pairtime's hooks to it, or ""
	// while install can't add any.
	Item string
	// ReadsClaudeHooks says that it also runs the hooks in Claude Code's
	// user settings, as Cursor's agent does (DESIGN 5.1).
	ReadsClaudeHooks bool
}

// Found reports whether install found the harness on this machine.
func (h Harness) Found() bool { return h.Where != "" }

// Detect looks for each harness DESIGN 5.1 names that install knows the
// folders of, and returns them all, found or not, always in the same
// order. It only looks: it reads no file and runs nothing.
//
// Claude Code counts as found when its folder is there ($CLAUDE_CONFIG_DIR
// or ~/.claude), when ~/.claude.json is, or when a claude program is on
// the PATH: a teen who installed it but hasn't run it yet gets the hooks
// ready for the first session. The others count as found by their folder
// in the home directory, which each makes the first time it runs.
func Detect(env Env) []Harness {
	claude := Harness{Name: claudeHarness, Title: "Claude Code", Item: ItemClaudeCode}
	claude.Where = firstThere(filepath.Dir(ClaudeSettings(env)), filepath.Join(env.Home, ".claude.json"))
	if claude.Where == "" {
		claude.Where = lookPath(env, "claude")
	}
	codexHome := filepath.Join(env.Home, ".codex")
	if d := env.getenv("CODEX_HOME"); filepath.IsAbs(d) {
		codexHome = d
	}
	return []Harness{
		claude,
		{Name: "cursor", Title: "Cursor", Where: firstThere(filepath.Join(env.Home, ".cursor")), ReadsClaudeHooks: true},
		{Name: "copilot", Title: "GitHub Copilot CLI", Where: firstThere(filepath.Join(env.Home, ".copilot"))},
		{Name: "codex", Title: "OpenAI Codex", Where: firstThere(codexHome)},
		{Name: "gemini", Title: "Gemini CLI", Where: firstThere(filepath.Join(env.Home, ".gemini"))},
	}
}

// firstThere returns the first of paths that exists, or "". Only an
// absolute path counts, so a home directory that isn't one finds nothing.
func firstThere(paths ...string) string {
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// lookPath returns the program name on env's PATH, or "". On Windows it
// tries the extensions npm and the native installers give Claude Code's
// program; elsewhere the file has to be executable.
func lookPath(env Env, name string) string {
	path := env.getenv("PATH")
	if env.GOOS == "windows" && path == "" {
		path = env.getenv("Path")
	}
	names := []string{name}
	if env.GOOS == "windows" {
		names = []string{name + ".exe", name + ".cmd", name + ".bat"}
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue // a relative entry would find a program in the current folder
		}
		for _, n := range names {
			p := filepath.Join(dir, n)
			info, err := os.Stat(p)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if env.GOOS == "windows" || info.Mode()&0o111 != 0 {
				return p
			}
		}
	}
	return ""
}

// Printable returns s with the characters a terminal acts on written out,
// as Plan.Diff writes them, for a path or a name that install prints: a
// folder name could otherwise move the cursor or clear the consent screen.
func Printable(s string) string { return escapeControls(s) }
