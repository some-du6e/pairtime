// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

var (
	focusClasses = []event.AppClass{
		event.AppTerminal, event.AppEditor, event.AppAgentApp, event.AppBrowser,
		event.AppChatApp, event.AppOther,
	}
	harnessValues = []event.Harness{
		event.HarnessClaudeCode, event.HarnessCursor, event.HarnessCopilot, event.HarnessCodex,
		event.HarnessGemini, event.HarnessDevin, event.HarnessCline, event.HarnessOpenCode,
		event.HarnessOther,
	}
	citeRE = regexp.MustCompile(`\b(HS|CP|SM) [0-9]+(\.[0-9]+)*`)
)

// checkEntry returns what is wrong with one appclass.json entry.
func checkEntry(e Entry) []string {
	var bad []string
	if len(e.Names) == 0 {
		bad = append(bad, "no names")
	}
	for _, n := range e.Names {
		stem, pattern := strings.CutSuffix(n, "*")
		switch {
		case n == "" || stem == "":
			bad = append(bad, fmt.Sprintf("name %q is empty", n))
		case n != strings.TrimSpace(n):
			bad = append(bad, fmt.Sprintf("name %q has surrounding space", n))
		case n != strings.ToLower(n):
			bad = append(bad, fmt.Sprintf("name %q isn't lowercase", n))
		case strings.HasSuffix(stem, ".exe"):
			bad = append(bad, fmt.Sprintf("name %q keeps .exe", n))
		case strings.ContainsAny(n, `/\`):
			bad = append(bad, fmt.Sprintf("name %q is a path", n))
		case strings.Contains(stem, "*"):
			bad = append(bad, fmt.Sprintf("name %q has a * before its end", n))
		case pattern && len(stem) < 3:
			bad = append(bad, fmt.Sprintf("pattern %q is too short to be specific", n))
		}
	}
	if slices.ContainsFunc(e.OS, func(s string) bool { return !slices.Contains(oses, s) }) {
		bad = append(bad, fmt.Sprintf("os %v names an OS other than %v", e.OS, oses))
	}
	if len(e.OS) != len(slices.Compact(slices.Sorted(slices.Values(e.OS)))) {
		bad = append(bad, fmt.Sprintf("os %v repeats an OS", e.OS))
	}
	if !slices.Contains(focusClasses, e.AppClass) {
		bad = append(bad, fmt.Sprintf("app_class %q isn't one of %v", e.AppClass, focusClasses))
	}
	if !slices.Contains(roles, e.Role) {
		bad = append(bad, fmt.Sprintf("role %q isn't one of %v", e.Role, roles))
	}
	switch {
	case e.Role == RoleHarness && !slices.Contains(harnessValues, e.Harness):
		bad = append(bad, fmt.Sprintf("a harness entry needs a harness from %v, got %q", harnessValues, e.Harness))
	case e.Role != RoleHarness && e.Harness != "":
		bad = append(bad, "harness is set on an entry whose role isn't harness")
	}
	if e.JobControl && e.Role != RoleShell {
		bad = append(bad, "job_control is set on an entry that isn't a shell")
	}
	if e.JSRuntime && e.Role != RoleRuntime {
		bad = append(bad, "js_runtime is set on an entry that isn't a runtime")
	}
	if e.NodeLauncher && e.Role != RoleHarness {
		bad = append(bad, "node_launcher is set on an entry whose role isn't harness")
	}
	if e.GivesConsole && !slices.Equal(e.OS, []string{Windows}) {
		bad = append(bad, "gives_console is set on an entry that isn't for Windows alone")
	}
	if !citeRE.MatchString(e.Cite) {
		bad = append(bad, fmt.Sprintf("cite %q names no HS, CP or SM section", e.Cite))
	}
	return bad
}

func TestAppClassEntriesAreWellFormed(t *testing.T) {
	entries := Entries()
	if len(entries) == 0 {
		t.Fatal("appclass.json has no entries")
	}
	for i, e := range entries {
		t.Run(fmt.Sprintf("entry %d (%s) is well formed", i, strings.Join(e.Names, ", ")), func(t *testing.T) {
			for _, b := range checkEntry(e) {
				t.Error(b)
			}
		})
	}
}

// TestAppClassNamesAreUnambiguous checks that on each OS a name belongs to
// one entry, and that no pattern matches another entry's name or pattern,
// so Lookup's answer never depends on the order of the file.
func TestAppClassNamesAreUnambiguous(t *testing.T) {
	for _, goos := range oses {
		t.Run(goos+" maps each name to one entry", func(t *testing.T) {
			owner := map[string]int{}
			for i, e := range Entries() {
				if len(e.OS) > 0 && !slices.Contains(e.OS, goos) {
					continue
				}
				for _, n := range e.Names {
					if j, dup := owner[n]; dup {
						t.Errorf("%q is in entries %d and %d", n, j, i)
					}
					owner[n] = i
				}
			}
			for p, i := range owner {
				stem, ok := strings.CutSuffix(p, "*")
				if !ok {
					continue
				}
				for n, j := range owner {
					if j != i && strings.HasPrefix(strings.TrimSuffix(n, "*"), stem) {
						t.Errorf("pattern %q of entry %d also matches %q of entry %d", p, i, n, j)
					}
				}
			}
		})
	}
}

func TestAppClassRejectsBadEntries(t *testing.T) {
	good := Entry{Names: []string{"tool"}, AppClass: event.AppOther, Role: RoleNone, Cite: "HS 12.2"}
	tests := []struct {
		name   string
		change func(*Entry)
		want   string
	}{
		{"a well-formed entry passes", func(*Entry) {}, ""},
		{"an entry needs a name", func(e *Entry) { e.Names = nil }, "no names"},
		{"names are lowercase", func(e *Entry) { e.Names = []string{"Tool"} }, "isn't lowercase"},
		{"names drop .exe", func(e *Entry) { e.Names = []string{"tool.exe"} }, "keeps .exe"},
		{"names aren't paths", func(e *Entry) { e.Names = []string{"bin/tool"} }, "is a path"},
		{"a * only ends a name", func(e *Entry) { e.Names = []string{"to*ol"} }, "before its end"},
		{"a pattern is specific", func(e *Entry) { e.Names = []string{"t*"} }, "too short"},
		{"os values are GOOS names", func(e *Entry) { e.OS = []string{"macos"} }, "other than"},
		{"app_class is a focus class", func(e *Entry) { e.AppClass = "none" }, "app_class"},
		{"role is known", func(e *Entry) { e.Role = "daemon" }, "role"},
		{"a harness entry names its harness", func(e *Entry) { e.Role = RoleHarness }, "needs a harness"},
		{"only harness entries name a harness", func(e *Entry) { e.Harness = event.HarnessCodex }, "isn't harness"},
		{"job_control is for shells", func(e *Entry) { e.JobControl = true }, "isn't a shell"},
		{"js_runtime is for runtimes", func(e *Entry) { e.JSRuntime = true }, "isn't a runtime"},
		{"node_launcher is for harnesses", func(e *Entry) { e.NodeLauncher = true }, "node_launcher is set"},
		{"gives_console is for Windows entries", func(e *Entry) { e.GivesConsole = true }, "gives_console is set"},
		{"gives_console on a Windows entry passes", func(e *Entry) { e.GivesConsole, e.OS = true, []string{Windows} }, ""},
		{"an entry cites HS, CP or SM", func(e *Entry) { e.Cite = "common knowledge" }, "names no HS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := good
			e.Names = slices.Clone(good.Names)
			tt.change(&e)
			got := strings.Join(checkEntry(e), "; ")
			if tt.want == "" && got != "" || tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("checkEntry = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	tests := []struct {
		name      string
		goos, exe string
		ok        bool
		role      Role
		class     event.AppClass
	}{
		{"case doesn't matter", Windows, "WindowsTerminal", true, RoleTerminal, event.AppTerminal},
		{"a .exe suffix is dropped", Windows, "Code.exe", true, RoleIDE, event.AppEditor},
		{"an entry holds only on its OS", Linux, "windowsterminal", false, "", ""},
		{"an entry without os holds everywhere", Darwin, "bash", true, RoleShell, event.AppTerminal},
		{"a pattern matches the rest", Linux, "python3.12", true, RoleRuntime, event.AppOther},
		{"a pattern matches a macOS helper", Darwin, "Code Helper (Plugin)", true, RoleIDE, event.AppEditor},
		{"the same name can differ by OS", Darwin, "launchd", true, RoleScheduler, event.AppOther},
		{"login is a wrapper on macOS", Darwin, "login", true, RoleWrapper, event.AppOther},
		{"login is a text console's terminal on Linux", Linux, "login", true, RoleTerminal, event.AppTerminal},
		{"the bare name agent isn't the Cursor CLI", Windows, "Agent.exe", false, "", ""},
		{"conhost isn't listed, since it never owns a console window or starts a process", Windows, "conhost.exe", false, "", ""},
		{"claude is Claude Code", Linux, "claude", true, RoleHarness, event.AppTerminal},
		{"the desktop app is not Claude Code", Darwin, "claude-desktop", true, RoleDesktop, event.AppAgentApp},
		{"an unknown name isn't found", Linux, "gen_prompt.py", false, "", ""},
		{"an empty name isn't found", Linux, "", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := Lookup(tt.goos, tt.exe)
			if ok != tt.ok || e.Role != tt.role || e.AppClass != tt.class {
				t.Errorf("Lookup(%q, %q) = %v %q %q, want %v %q %q", tt.goos, tt.exe, ok, e.Role, e.AppClass, tt.ok, tt.role, tt.class)
			}
		})
	}
}

func TestAppClassOf(t *testing.T) {
	tests := []struct {
		name      string
		goos, exe string
		want      event.AppClass
	}{
		{"a terminal is terminal", Darwin, "iTerm2", event.AppTerminal},
		{"an editor is editor", Windows, "Cursor.exe", event.AppEditor},
		{"a browser is browser", Windows, "msedge.exe", event.AppBrowser},
		{"ChatGPT is a chat app", Darwin, "ChatGPT", event.AppChatApp},
		{"an unknown app is other", Windows, "steam.exe", event.AppOther},
		{"a PowerShell console window, which powershell.exe owns, is a terminal", Windows, "powershell.exe", event.AppTerminal},
		{"a cmd console window is a terminal", Windows, "cmd.exe", event.AppTerminal},
		{"the console window of a claude.exe started from Win+R is a terminal", Windows, "claude.exe", event.AppTerminal},
		{"the console window of Git Bash's bash.exe is a terminal", Windows, "bash.exe", event.AppTerminal},
		{"File Explorer is other", Windows, "explorer.exe", event.AppOther},
		{"Neovim is an editor", Linux, "nvim", event.AppEditor},
		{"Helix is an editor", Darwin, "hx", event.AppEditor},
		{"Devin Desktop is an editor", Linux, "devin-desktop", event.AppEditor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AppClassOf(tt.goos, tt.exe); got != tt.want {
				t.Errorf("AppClassOf(%q, %q) = %q, want %q", tt.goos, tt.exe, got, tt.want)
			}
		})
	}
}

func TestExeName(t *testing.T) {
	tests := []struct {
		name       string
		goos, path string
		want       string
	}{
		{"the base name names the executable", Linux, "/usr/bin/bash", "bash"},
		{"Windows drops .exe and reads backslashes", Windows, `C:\Program Files\Git\usr\bin\bash.exe`, "bash"},
		{"Windows .exe is dropped in any case", Windows, `C:\Windows\System32\CMD.EXE`, "CMD"},
		{"a deleted image keeps its name", Linux, "/opt/tool/bin/tool (deleted)", "tool"},
		{"Claude Code's versioned image is claude on Linux", Linux, "/home/u/.local/share/claude/versions/2.1.328", "claude"},
		{"Claude Code's versioned image is claude on macOS", Darwin, "/Users/u/.local/share/claude/versions/2.1.328", "claude"},
		{"a version number elsewhere stays as it is", Linux, "/opt/app/versions/2.1.328", "2.1.328"},
		{"Claude.app is the desktop app", Darwin, "/Applications/Claude.app/Contents/MacOS/Claude", "claude-desktop"},
		{"a CLI inside Claude.app is not the desktop app", Darwin, "/Applications/Claude.app/Contents/Resources/bin/claude", "claude"},
		{"Codex.app is the desktop app", Darwin, "/Applications/Codex.app/Contents/MacOS/Codex", "codex-desktop"},
		{"claude.exe under AnthropicClaude is the desktop app", Windows, `C:\Users\u\AppData\Local\AnthropicClaude\app-0.14.10\claude.exe`, "claude-desktop"},
		{"its launcher is the desktop app too", Windows, `C:\Users\u\AppData\Local\AnthropicClaude\claude.exe`, "claude-desktop"},
		{"claude.exe elsewhere is Claude Code", Windows, `C:\Users\u\.local\bin\claude.exe`, "claude"},
		{"a bare name is its own base name", Linux, "bash", "bash"},
		{"the Cursor CLI's versioned image is cursor-agent", Linux, "/home/u/.local/share/cursor-agent/versions/2026.11.19-f005e9d/cursor-agent", "cursor-agent"},
		{"a node the Cursor CLI bundles is cursor-agent", Darwin, "/Users/u/.local/share/cursor-agent/versions/2026.11.19-f005e9d/node", "cursor-agent"},
		{"the Cursor CLI's node is cursor-agent on Windows too", Windows, `C:\Users\u\AppData\Local\cursor-agent\versions\2026.11.19-f005e9d\node.exe`, "cursor-agent"},
		{"another program in the Cursor CLI's version directory keeps its name", Linux, "/home/u/.local/share/cursor-agent/versions/2026.11.19-f005e9d/rg", "rg"},
		{"Battle.net's Agent.exe stays Agent", Windows, `C:\ProgramData\Battle.net\Agent\Agent.9124\Agent.exe`, "Agent"},
		{"a node elsewhere stays node", Windows, `C:\Program Files\nodejs\node.exe`, "node"},
		{"the Devin CLI in Devin Desktop's resources is devin", Windows, `C:\Users\u\AppData\Local\Programs\Devin\resources\app\extensions\windsurf\devin\bin\devin.exe`, "devin"},
		{"a devin.exe outside a bin directory is taken for Devin Desktop", Windows, `C:\Users\u\AppData\Local\Programs\Devin\Devin.exe`, "devin-desktop"},
		{"a bare devin.exe, before its path is read, is devin", Windows, "devin.exe", "devin"},
		{"a macOS app's main executable called Devin is Devin Desktop", Darwin, "/Applications/Devin.app/Contents/MacOS/Devin", "devin-desktop"},
		{"the Devin CLI inside the app bundle is devin", Darwin, "/Applications/Devin.app/Contents/Resources/app/extensions/windsurf/devin/bin/devin", "devin"},
		{"the Devin CLI on Linux is devin", Linux, "/home/u/.local/bin/devin", "devin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExeName(tt.goos, tt.path); got != tt.want {
				t.Errorf("ExeName(%q, %q) = %q, want %q", tt.goos, tt.path, got, tt.want)
			}
		})
	}
}

func TestNeedsPath(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		want bool
	}{
		{"claude may be Claude Code or the desktop app", "claude.exe", true},
		{"devin may be the CLI or Devin Desktop", "Devin.exe", true},
		{"node may be the Cursor CLI's", "node.exe", true},
		{"agent may be the Cursor CLI's", "agent.exe", true},
		{"a version number may be Claude Code", "2.1.328", true},
		{"bash is bash wherever it is", "bash.exe", false},
		{"codex is codex wherever it is", "codex.exe", false},
		{"cursor-agent needs no path", "cursor-agent.exe", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsPath(tt.exe); got != tt.want {
				t.Errorf("NeedsPath(%q) = %v, want %v", tt.exe, got, tt.want)
			}
		})
	}
}

func TestAliasOf(t *testing.T) {
	tests := []struct {
		name             string
		goos, path, comm string
		want             string
	}{
		{"a comm that repeats the file is no alias", Linux, "/usr/bin/bash", "bash", ""},
		{"a comm cut to 15 bytes is no alias", Linux, "/usr/libexec/gnome-terminal-server", "gnome-terminal-", ""},
		{"a comm cut to 16 bytes is no alias", Darwin, "/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper (Plugin).app/Contents/MacOS/Code Helper (Plugin)", "Code Helper (Plu", ""},
		{"a Node app's title is an alias", Linux, "/usr/bin/node", "claude", "claude"},
		{"a #! script's name is an alias", Linux, "/usr/bin/python3.12", "terminator", "terminator"},
		{"the desktop app's own file name is no alias", Darwin, "/Applications/Claude.app/Contents/MacOS/Claude", "Claude", ""},
		{"a version-named image's file name is no alias", Linux, "/home/u/.local/share/claude/versions/2.1.328", "2.1.328", ""},
		{"an empty comm is no alias", Linux, "/usr/bin/bash", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := ExeName(tt.goos, tt.path)
			if got := AliasOf(tt.goos, tt.path, name, tt.comm); got != tt.want {
				t.Errorf("AliasOf(%q, %q, %q, %q) = %q, want %q", tt.goos, tt.path, name, tt.comm, got, tt.want)
			}
		})
	}
}
