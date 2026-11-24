// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

var t0 = time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)

// proc is a Windows process that started mins minutes after t0.
func proc(pid, ppid int, name string, mins int) platform.Proc {
	return platform.Proc{PID: pid, PPID: ppid, Name: name, Start: t0.Add(time.Duration(mins) * time.Minute), Owner: "session:1"}
}

// desktop is a Windows desktop with every kind of window the hosting rule
// tells apart. Names are what windows.Processes gives: ExeName's, so the
// Claude desktop app is claude-desktop.
func desktop() []platform.Proc {
	return []platform.Proc{
		proc(4, 0, "System", -600),
		proc(4520, 4480, "explorer", -500),
		// Windows Terminal, one process for all its windows and tabs, with
		// an OpenConsole.exe per tab and a claude in each of two tabs.
		proc(6100, 4520, "WindowsTerminal", -60),
		proc(6144, 6100, "OpenConsole", -60),
		proc(6150, 6100, "OpenConsole", -50),
		proc(6200, 6100, "pwsh", -60),
		proc(6210, 6200, "claude", -30),
		proc(6300, 6100, "bash", -50),
		proc(6310, 6300, "claude", -20),
		// A classic console: cmd.exe from the Start menu, its conhost, and
		// a claude typed into it.
		proc(7000, 4520, "cmd", -40),
		proc(7001, 7000, "conhost", -40),
		proc(7010, 7000, "claude", -10),
		// claude.exe double-clicked in File Explorer, which owns the
		// console Windows made for it.
		proc(8000, 4520, "claude", -5),
		proc(8001, 8000, "conhost", -5),
		// A conhost whose console has no client left and whose parent is
		// explorer.
		proc(8100, 4520, "conhost", -4),
		// VS Code with Claude Code in its integrated terminal: the window
		// belongs to the main process, the shell to the pty host.
		proc(9000, 4520, "Code", -120),
		proc(9010, 9000, "Code", -120),
		proc(9011, 9010, "conhost", -100),
		proc(9020, 9010, "pwsh", -100),
		proc(9030, 9020, "claude", -90),
		// The Claude desktop app with a session in its Code tab.
		proc(9500, 4520, "claude-desktop", -200),
		proc(9510, 9500, "claude", -15),
		// A browser that started a harness (a web IDE's helper, say), and
		// an app the table doesn't know.
		proc(9600, 4520, "chrome", -300),
		proc(9610, 9600, "claude", -2),
		proc(9700, 4520, "notepad", -3),
	}
}

// keyed gives the sessions of the desktop's harness processes their keys.
func keyed(t *platform.Table, keys map[int]string) map[platform.ID]string {
	out := make(map[platform.ID]string)
	for pid, k := range keys {
		p, ok := t.Get(pid)
		if !ok {
			panic("no such pid in the test table")
		}
		out[p.ID()] = k
	}
	return out
}

var desktopSessions = map[int]string{
	6210: "s_tab1", 6310: "s_tab2", 7010: "s_cmd", 8000: "s_own",
	9030: "s_code", 9510: "s_desk", 9610: "s_web",
}

func TestHostedSessions(t *testing.T) {
	tab := platform.NewTable(platform.Windows, desktop())
	sessions := keyed(tab, desktopSessions)
	tests := []struct {
		name   string
		pid    int
		class  event.AppClass
		app    string
		hosted []string
	}{
		{"a Windows Terminal hosts the sessions of all its windows and tabs (decision 1)", 6100, event.AppTerminal, "windowsterminal", []string{"s_tab1", "s_tab2"}},
		{"a window Windows gives to a terminal's OpenConsole helper is the terminal's, with all its sessions", 6144, event.AppTerminal, "windowsterminal", []string{"s_tab1", "s_tab2"}},
		{"a classic console window belongs to its first client and hosts the sessions under it", 7000, event.AppTerminal, "cmd", []string{"s_cmd"}},
		{"a console window Windows names conhost for is the console of the client conhost serves", 7001, event.AppTerminal, "cmd", []string{"s_cmd"}},
		{"a console window given to claude.exe hosts that claude's own session", 8000, event.AppTerminal, "claude", []string{"s_own"}},
		{"the conhost of a console claude.exe owns is that console", 8001, event.AppTerminal, "claude", []string{"s_own"}},
		{"a conhost under explorer is a terminal that hosts nothing, never everything explorer started", 8100, event.AppTerminal, "conhost", nil},
		{"an editor's window hosts the sessions in its integrated terminal", 9000, event.AppEditor, "code", []string{"s_code"}},
		{"an agent desktop app hosts the sessions of its Code tab (SM 3.2 rule 3)", 9500, event.AppAgentApp, "claude-desktop", []string{"s_desk"}},
		{"a browser hosts no session, even one its process started (SM 3.2 rule 4)", 9600, event.AppBrowser, "chrome", nil},
		{"an app the table doesn't know is other and hosts nothing", 9700, event.AppOther, "notepad", nil},
		{"the desktop and the taskbar are explorer's and host nothing, though explorer started every session", 4520, event.AppOther, "explorer", nil},
		{"a shell inside a terminal is a terminal that hosts the sessions under it", 6300, event.AppTerminal, "bash", []string{"s_tab2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, ok := windowOf(tab, tt.pid)
			if !ok {
				t.Fatalf("pid %d isn't in the table", tt.pid)
			}
			if w.class != tt.class || w.name != tt.app {
				t.Errorf("class %s, app %q; want %s, %q", w.class, w.name, tt.class, tt.app)
			}
			if got := hostedSessions(tab, w, sessions); !slices.Equal(got, tt.hosted) {
				t.Errorf("hosted_sessions %v, want %v", got, tt.hosted)
			}
		})
	}

	t.Run("a pid that isn't in the table is no window", func(t *testing.T) {
		if _, ok := windowOf(tab, 31337); ok {
			t.Error("windowOf found a window for a pid that isn't in the table")
		}
	})
	t.Run("a session whose pid went to a newer process isn't hosted", func(t *testing.T) {
		stale := map[platform.ID]string{{PID: 6210, Start: t0.Add(-45 * time.Minute).UnixNano()}: "s_old"}
		w, _ := windowOf(tab, 6100)
		if got := hostedSessions(tab, w, stale); got != nil {
			t.Errorf("hosted_sessions %v, want none", got)
		}
	})
	t.Run("a session whose process has exited isn't hosted", func(t *testing.T) {
		gone := map[platform.ID]string{{PID: 6999, Start: t0.UnixNano()}: "s_gone"}
		w, _ := windowOf(tab, 6100)
		if got := hostedSessions(tab, w, gone); got != nil {
			t.Errorf("hosted_sessions %v, want none", got)
		}
	})
	t.Run("two processes with one session key are listed once", func(t *testing.T) {
		twice := keyed(tab, map[int]string{6210: "s_same", 6310: "s_same"})
		w, _ := windowOf(tab, 6100)
		if got := hostedSessions(tab, w, twice); !slices.Equal(got, []string{"s_same"}) {
			t.Errorf("hosted_sessions %v, want [s_same]", got)
		}
	})
}

// TestGoldenHosting reads the SM 6.6 process tables WP-OS6 wrote from SM
// and checks SM 3.2's hosting rule against them: the Windows Terminal in
// front, or a window Windows gives to its OpenConsole helper, hosts the
// claude it runs, and Chrome, also in those tables, hosts nothing.
func TestGoldenHosting(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "platform", "testdata", "procs", "sm66-*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no SM 6.6 tables in internal/platform/testdata/procs (%v)", err)
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// The fields of platform's GoldenFile this test reads.
		var g struct {
			Tables []struct {
				OS         string          `json:"os"`
				HarnessPID int             `json:"harness_pid"`
				Procs      []platform.Proc `json:"procs"`
			} `json:"tables"`
		}
		if err := json.Unmarshal(data, &g); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, gt := range g.Tables {
			if gt.OS != platform.Windows {
				continue
			}
			tab := platform.NewTable(gt.OS, gt.Procs)
			h, ok := tab.Get(gt.HarnessPID)
			if !ok {
				t.Fatalf("%s: harness pid %d isn't in the table", name, gt.HarnessPID)
			}
			sessions := map[platform.ID]string{h.ID(): "s_casey"}
			for _, p := range tab.Procs() {
				var want []string
				switch appName(p) {
				case "windowsterminal", "openconsole":
					want = []string{"s_casey"}
				case "chrome":
				default:
					continue
				}
				t.Run(filepath.Base(name)+": "+p.Name+" in front hosts "+map[bool]string{true: "the claude it runs", false: "nothing"}[want != nil], func(t *testing.T) {
					w, _ := windowOf(tab, p.PID)
					if got := hostedSessions(tab, w, sessions); !slices.Equal(got, want) {
						t.Errorf("hosted_sessions %v, want %v", got, want)
					}
				})
			}
		}
	}
}

// TestAgentAppWindow checks SM 3.2 rule 3 for T3 Code's desktop app, which
// runs its server as its own executable, a child of the window's process
// (internal/platform/testdata/procs/sm32-t3-desktop.json): its window is an
// agent app's and hosts every session that server drives, so SM 2.2 rule
// 6, tested at a message's stamp, sees a message typed into it here. A t3
// serve started on its own owns no window, so a console showing its log
// hosts nothing.
func TestAgentAppWindow(t *testing.T) {
	tab := platform.NewTable(platform.Windows, []platform.Proc{
		proc(4, 0, "System", -600),
		proc(4520, 4480, "explorer", -500),
		// The desktop app's window, its server, a session the server
		// started for a message, and one typed into T3 Code's own terminal.
		proc(9800, 4520, "T3 Code (Beta)", -100),
		proc(9810, 9800, "T3 Code (Beta)", -100),
		proc(9820, 9810, "claude", -30),
		proc(9830, 9810, "pwsh", -20),
		proc(9840, 9830, "claude", -10),
		// A t3 serve explorer started, which Windows gives a console of its
		// own, and a session it started.
		proc(9900, 4520, "t3", -200),
		proc(9910, 9900, "claude", -5),
	})
	sessions := keyed(tab, map[int]string{9820: "s_t3", 9840: "s_t3term", 9910: "s_serve"})
	tests := []struct {
		name   string
		pid    int
		class  event.AppClass
		app    string
		hosted []string
	}{
		{"T3 Code's desktop window is an agent app's and hosts every session its own server drives (SM 3.2 rule 3)",
			9800, event.AppAgentApp, "t3 code (beta)", []string{"s_t3", "s_t3term"}},
		{"a console of t3 serve's own is other, so it hosts nothing, though its process started the session",
			9900, event.AppOther, "t3", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, ok := windowOf(tab, tt.pid)
			if !ok {
				t.Fatalf("pid %d isn't in the table", tt.pid)
			}
			if w.class != tt.class || w.name != tt.app {
				t.Errorf("class %s, app %q; want %s, %q", w.class, w.name, tt.class, tt.app)
			}
			if got := hostedSessions(tab, w, sessions); !slices.Equal(got, tt.hosted) {
				t.Errorf("hosted_sessions %v, want %v", got, tt.hosted)
			}
		})
	}
}

func TestFocusProject(t *testing.T) {
	projects := map[string]string{"s_a": "p_a", "s_a2": "p_a", "s_b": "p_b"}
	projectOf := func(s string) string { return projects[s] }
	tests := []struct {
		name   string
		class  event.AppClass
		hosted []string
		want   string
	}{
		{"one hosted session gives its project (SM 3.2 rule 1)", event.AppTerminal, []string{"s_a"}, "p_a"},
		{"sessions all in one project give that project", event.AppTerminal, []string{"s_a", "s_a2"}, "p_a"},
		{"sessions in two projects leave the slot to the scorer's look-ahead", event.AppTerminal, []string{"s_a", "s_b"}, ""},
		{"a session whose project isn't known leaves the project empty", event.AppTerminal, []string{"s_a", "s_new"}, ""},
		{"an agent desktop app gives the project of the session it hosts (SM 3.2 rule 3)", event.AppAgentApp, []string{"s_b"}, "p_b"},
		{"an editor gives the project of the session it hosts, as Cursor does in SM 6.2", event.AppEditor, []string{"s_a"}, "p_a"},
		{"an editor's sessions in two projects leave the project to its heartbeats (SM 3.2 rule 2)", event.AppEditor, []string{"s_a", "s_b"}, ""},
		{"a browser has no project, whatever runs under it (SM 3.2 rule 4)", event.AppBrowser, []string{"s_a"}, ""},
		{"a window that hosts nothing has no project", event.AppTerminal, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := focusProject(tt.class, tt.hosted, projectOf); got != tt.want {
				t.Errorf("focus_project %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("without a way to look projects up the project stays empty", func(t *testing.T) {
		if got := focusProject(event.AppTerminal, []string{"s_a"}, nil); got != "" {
			t.Errorf("focus_project %q, want none", got)
		}
	})
}

func TestAppHash(t *testing.T) {
	kDev := make([]byte, 32)
	for i := range kDev {
		kDev[i] = byte(i)
	}
	secret, err := AppSecret(kDev)
	if err != nil {
		t.Fatal(err)
	}
	other, err := AppSecret(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	// The reference values come from a Python HKDF and HMAC written from
	// ledger-v1.md section 3, which also gives the git adapter's published
	// project-secret vector for K_dev 00..1f.
	tests := []struct {
		name, got, want string
	}{
		{"K_app of K_dev 00..1f is HKDF-SHA256 under pairtime/v1/app", hex.EncodeToString(secret), "97ac792fc1a5978b991698cbd340e91238634f8dacda8a2cd6387abc3a76fddb"},
		{"app_hash of Windows Terminal under that K_app is the reference value", appHash(secret, platform.Windows, "windowsterminal"), "af96f58c5388218fcd6f6cd72bb8e793f"},
		{"app_hash of Chrome under that K_app is the reference value", appHash(secret, platform.Windows, "chrome"), "a18ecfcc1abf15c0fce0859c46e557fa6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %s, want %s", tt.got, tt.want)
			}
		})
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"app_hash isn't the plain hash of the app's name", appHash(secret, platform.Windows, "windowsterminal") != "a7c42472c9a264bb8589c1f0b9b8d4e1f"},
		{"another device's K_app gives another app_hash", appHash(secret, platform.Windows, "chrome") != appHash(other, platform.Windows, "chrome")},
		{"the same name on another OS gives another app_hash", appHash(secret, platform.Windows, "code") != appHash(secret, platform.Linux, "code")},
		{"app_hash is an opaque key", validKey(appHash(secret, platform.Windows, "code"))},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Error("it isn't")
			}
		})
	}
	t.Run("a K_dev of another length is refused", func(t *testing.T) {
		if _, err := AppSecret(kDev[:16]); err == nil {
			t.Error("AppSecret took a 16-byte K_dev")
		}
	})
}
