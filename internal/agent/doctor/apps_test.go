// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TestT3Line checks DESIGN 5.9's line for T3 Code (DESIGN 5.1): whether
// the agent reads its store, with what it read today, or why it doesn't,
// with the command that installs sqlite3 here; a Claude instance of T3's
// with a home of its own; and no session T3 started reaching pairtime in
// a day. It names no path.
func TestT3Line(t *testing.T) {
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	// t3 is the facts of an agent whose reader reads T3 Code's store,
	// today's three messages paired, a session of T3's heard of an hour
	// ago, changed by edit.
	t3 := func(edit func(*T3Code, *Facts)) func(*Facts) {
		return func(f *Facts) {
			c := T3Code{Reading: true, Witnessed: 3, Paired: 3, SessionAt: now.Add(-time.Hour).UnixMilli()}
			f.Now = now
			edit(&c, f)
			f.Agent.Apps.T3Code = &c
		}
	}
	off := func(why, problem string) func(*T3Code, *Facts) {
		return func(c *T3Code, _ *Facts) { c.Reading, c.Off, c.Problem = false, why, problem }
	}
	noSQLite := func(goos string, distro ...string) func(*Facts) {
		return t3(func(c *T3Code, f *Facts) {
			off("no_sqlite3", "")(c, f)
			f.GOOS, f.Distro = goos, distro
		})
	}
	tests := []struct {
		name    string
		edit    func(*Facts)
		want    Result
		wantHas []string
		wantNot []string
		absent  bool
	}{
		{name: "a computer where neither the agent nor doctor finds T3 Code has no line",
			edit: func(*Facts) {}, absent: true},
		{name: "an agent that finds no store, with doctor finding none either, has no line",
			edit: t3(func(c *T3Code, f *Facts) { off("no_store", "")(c, f) }), absent: true},
		{name: "a store the agent reads is ok, with today's messages and the records paired with prompts",
			edit: t3(func(*T3Code, *Facts) {}), want: OK,
			wantHas: []string{"pairtime reads T3 Code's store; today it read 3 messages you sent through T3 Code, and paired 3 records with prompts Claude Code got"}},
		{name: "a day with no messages reads as none, with nothing paired",
			edit: t3(func(c *T3Code, _ *Facts) { c.Witnessed, c.Paired = 0, 0 }), want: OK,
			wantHas: []string{"today it read no messages you sent through T3 Code"}, wantNot: []string{"paired"}},
		{name: "the messages the age test turned away are counted",
			edit: t3(func(c *T3Code, _ *Facts) { c.Aged = 2 }), want: OK,
			wantHas: []string{"it turned away 2 messages it first read too long after they were sent"}},
		{name: "one message the age test turned away reads as one",
			edit: t3(func(c *T3Code, _ *Facts) { c.Aged = 1 }), want: OK,
			wantHas: []string{"it turned away 1 message it first read too long after it was sent"}},
		{name: "the messages turned away as stamped more than 2 s after the read that decided them are counted (SM 2.2)",
			edit: t3(func(c *T3Code, _ *Facts) { c.Ahead = 2 }), want: OK,
			wantHas: []string{"it turned away 2 messages stamped more than 2 seconds after it read them, as happens when the computer's clock is set back"}},
		{name: "one message turned away as stamped after its read reads as one",
			edit: t3(func(c *T3Code, _ *Facts) { c.Ahead = 1 }), want: OK,
			wantHas: []string{"it turned away 1 message stamped more than 2 seconds after it read it"}},
		{name: "T3 Code that isn't running still says what the reader turned away earlier today, though it read nothing",
			edit: t3(func(c *T3Code, f *Facts) {
				off("not_running", "")(c, f)
				c.Witnessed, c.Paired, c.Ahead = 0, 0, 1
			}), want: OK,
			wantHas: []string{"T3 Code isn't running, and pairtime reads its store only while it runs; today it read no messages",
				"it turned away 1 message stamped more than 2 seconds after it read it"}},
		{name: "a schema newer than the one pairtime was tested with is read, and the line says so (DESIGN 5.1)",
			edit: t3(func(c *T3Code, _ *Facts) { c.Newer = true }), want: OK,
			wantHas: []string{"T3 Code's schema is newer than the one this pairtime was tested with, and pairtime reads it anyway"}},
		{name: "T3 Code that isn't running is ok, with what the reader read of it earlier today",
			edit: t3(func(c *T3Code, f *Facts) { off("not_running", "")(c, f) }), want: OK,
			wantHas: []string{"T3 Code isn't running, and pairtime reads its store only while it runs; today it read 3 messages"}},
		{name: "no sqlite3 on Ubuntu warns that T3 Code's messages count for nothing, with apt's command",
			edit: noSQLite("linux", "ubuntu", "debian"), want: Warn,
			wantHas: []string{"the agent finds no sqlite3 tool, so pairtime reads none of the messages you send through T3 Code, and they count for nothing", "sudo apt install sqlite3"}},
		{name: "no sqlite3 on Fedora names its sqlite package",
			edit: noSQLite("linux", "fedora"), want: Warn, wantHas: []string{"sudo dnf install sqlite"}, wantNot: []string{"apt"}},
		{name: "no sqlite3 on Windows names winget and a new login, which starts the agent with the new PATH",
			edit: noSQLite("windows"), want: Warn, wantHas: []string{"winget install SQLite.SQLite", "log out and back in"}},
		{name: "no sqlite3 on macOS, which ships it, says to report a line that a new login doesn't clear",
			edit: noSQLite("darwin"), want: Warn, wantHas: []string{"macOS ships it", "Report this line"}},
		{name: "a reader off for want of sqlite3 names the install command once",
			edit: t3(func(c *T3Code, f *Facts) {
				off("no_sqlite3", "")(c, f)
				c.NoSQLite3 = true
				f.GOOS, f.Distro = "linux", []string{"debian"}
			}), want: Warn, wantHas: []string{"sudo apt install sqlite3"}, wantNot: []string{"; but the agent finds no sqlite3 tool"}},
		{name: "a missing sqlite3 while T3 Code isn't running warns with the install command, whatever else the line says (F2)",
			edit: t3(func(c *T3Code, f *Facts) {
				off("not_running", "")(c, f)
				c.NoSQLite3 = true
				f.GOOS, f.Distro = "linux", []string{"ubuntu", "debian"}
			}), want: Warn,
			wantHas: []string{"T3 Code isn't running, and pairtime reads its store only while it runs",
				"but the agent finds no sqlite3 tool, so pairtime can't read the messages you send through T3 Code, and they count for nothing until it can. Install it with: sudo apt install sqlite3"}},
		{name: "a missing sqlite3 warns with the install command when doctor finds a store the agent doesn't, too",
			edit: t3(func(c *T3Code, f *Facts) {
				off("no_store", "")(c, f)
				c.NoSQLite3 = true
				f.T3 = T3Facts{Found: true}
				f.GOOS, f.Distro = "linux", []string{"fedora"}
			}), want: Warn, wantHas: []string{"T3CODE_HOME", "the agent finds no sqlite3 tool", "sudo dnf install sqlite"}},
		{name: "with neither the agent nor doctor finding T3 Code, a missing sqlite3 says nothing",
			edit: t3(func(c *T3Code, f *Facts) {
				off("no_store", "")(c, f)
				c.NoSQLite3 = true
			}), absent: true},
		{name: "a sqlite3 with no JSON functions warns that T3 Code's messages count for nothing, with how to get one that has them",
			edit: t3(func(c *T3Code, f *Facts) {
				off("no_json", "this sqlite3 has no JSON functions")(c, f)
				f.GOOS = "linux"
			}), want: Warn,
			wantHas: []string{"pairtime doesn't read T3 Code's store, because the agent's sqlite3 has no JSON functions, so the messages you send through T3 Code count for nothing until it does",
				"Install a sqlite3 of version 3.38 or later"},
			wantNot: []string{"It tries again every 10 seconds"}},
		{name: "on Windows a sqlite3 with no JSON functions names winget and a new login",
			edit: t3(func(c *T3Code, f *Facts) {
				off("no_json", "this sqlite3 has no JSON functions")(c, f)
				f.GOOS = "windows"
			}), want: Warn, wantHas: []string{"winget install SQLite.SQLite", "log out and back in"}},
		{name: "a schema the probe refuses warns with the reader's reason and where to report it",
			edit: t3(off("schema", "T3 Code's table orchestration_v2_projection_messages has no column run_id")), want: Warn,
			wantHas: []string{"pairtime doesn't read T3 Code's store, because T3 Code's table orchestration_v2_projection_messages has no column run_id. The messages you send through T3 Code count for nothing until it can", "Report this line"}},
		{name: "a refused schema the reader gives no reason for still reads as a sentence",
			edit: t3(off("schema", "")), want: Warn, wantHas: []string{"because its schema isn't one pairtime reads."}},
		{name: "a failed read warns with the reader's words",
			edit: t3(off("failed", "T3 Code's store was busy")), want: Warn,
			wantHas: []string{"the agent's last read of T3 Code's store failed: T3 Code's store was busy. It tries again every 10 seconds"}},
		{name: "a Claude instance of T3's with a home of its own warns, whatever the reader does, and says how to count it (DESIGN 5.1)",
			edit: t3(func(_ *T3Code, f *Facts) { f.T3 = T3Facts{Found: true, OwnHomes: 1} }), want: Warn,
			wantHas: []string{"one of T3 Code's Claude instances runs Claude Code with a Claude home of its own, set by CLAUDE_CONFIG_DIR, which loads none of pairtime's hooks",
				"Clear that instance's CLAUDE_CONFIG_DIR path in T3 Code's provider settings, or run pairtime install again with CLAUDE_CONFIG_DIR set to it"}},
		{name: "two such instances read as two",
			edit: t3(func(_ *T3Code, f *Facts) { f.T3 = T3Facts{Found: true, OwnHomes: 2} }), want: Warn,
			wantHas: []string{"2 of T3 Code's Claude instances run Claude Code with Claude homes of their own"}},
		{name: "T3 Code's settings that don't read say doctor can't tell, and warn of nothing",
			edit: t3(func(_ *T3Code, f *Facts) { f.T3 = T3Facts{Found: true, Unread: true} }), want: OK,
			wantHas: []string{"doctor couldn't read T3 Code's settings, so it can't tell whether T3 Code gives Claude Code a home of its own"}},
		{name: "a CLAUDE_CONFIG_DIR T3 Code's background service sets, to a home without pairtime's hooks, warns and says how to count it (Q13)",
			edit: t3(func(_ *T3Code, f *Facts) { f.T3 = T3Facts{Found: true, ServiceHome: true} }), want: Warn,
			wantHas: []string{"T3 Code's background service sets CLAUDE_CONFIG_DIR, in its systemd unit or launchd plist, to a Claude home whose settings hold none of pairtime's hooks",
				"Take CLAUDE_CONFIG_DIR out of the service's environment, or run pairtime install again with CLAUDE_CONFIG_DIR set to that home"},
			wantNot: []string{"doctor couldn't read"}},
		{name: "messages today with no session of T3's reaching pairtime in a day warn that they count for nothing",
			edit: t3(func(c *T3Code, _ *Facts) { c.Paired, c.SessionAt = 0, now.Add(-25*time.Hour).UnixMilli() }), want: Warn,
			wantHas: []string{"but none of the messages pairtime read from T3 Code today paired with a prompt Claude Code got, so they count for nothing",
				"isn't running pairtime's hooks"}},
		{name: "messages today that paired with no prompt warn whatever session_at says, since a claude typed into T3 Code's terminal is a session of T3's too (Q13)",
			edit: t3(func(c *T3Code, _ *Facts) { c.Paired = 0 }), want: Warn,
			wantHas: []string{"but none of the messages pairtime read from T3 Code today paired with a prompt Claude Code got"},
			wantNot: []string{"last day"}},
		{name: "a running T3 Code none of whose sessions ever reached pairtime warns (DESIGN 5.1)",
			edit: t3(func(c *T3Code, _ *Facts) { c.Witnessed, c.Paired, c.SessionAt, c.Running = 0, 0, 0, true }), want: Warn,
			wantHas: []string{"no session T3 Code started has reached pairtime in the last day. If you used T3 Code since, the Claude Code it runs isn't running pairtime's hooks"}},
		{name: "a store the agent reads with no T3 Code server running, as one a killed T3 Code left open, says nothing of quiet sessions (Q15)",
			edit: t3(func(c *T3Code, _ *Facts) { c.Witnessed, c.Paired, c.SessionAt, c.Running = 0, 0, 0, false }), want: OK,
			wantNot: []string{"last day"}},
		{name: "a session of T3's in the last day warns of nothing",
			edit: t3(func(c *T3Code, _ *Facts) { c.Running = true }), want: OK, wantNot: []string{"last day", "none of the messages"}},
		{name: "a T3 Code that isn't running and sent nothing today says nothing of its quiet sessions",
			edit: t3(func(c *T3Code, f *Facts) {
				off("not_running", "")(c, f)
				c.Witnessed, c.Paired, c.SessionAt = 0, 0, 0
			}), want: OK, wantNot: []string{"last day"}},
		{name: "a home of its own explains quiet sessions and messages that paired with nothing, so the line doesn't say both",
			edit: t3(func(c *T3Code, f *Facts) {
				c.Paired, c.SessionAt, c.Running = 0, 0, true
				f.T3 = T3Facts{Found: true, OwnHomes: 1}
			}), want: Warn, wantHas: []string{"home of its own"}, wantNot: []string{"last day", "none of the messages"}},
		{name: "so does a home T3 Code's background service sets",
			edit: t3(func(c *T3Code, f *Facts) {
				c.Paired, c.SessionAt, c.Running = 0, 0, true
				f.T3 = T3Facts{Found: true, ServiceHome: true}
			}), want: Warn, wantHas: []string{"T3 Code's background service sets CLAUDE_CONFIG_DIR"}, wantNot: []string{"last day", "none of the messages"}},
		{name: "with no agent, a T3 Code doctor finds is skipped, never read from the terminal",
			edit: func(f *Facts) { f.Reach, f.Agent, f.T3 = Down, nil, T3Facts{Found: true} }, want: Skip,
			wantHas: []string{"T3 Code is on this computer; the agent isn't running"}},
		{name: "with no agent, a home of its own that doctor finds still warns",
			edit: func(f *Facts) { f.Reach, f.Agent, f.T3 = Down, nil, T3Facts{Found: true, OwnHomes: 1} }, want: Warn,
			wantHas: []string{"home of its own"}},
		{name: "an agent that hasn't read T3 Code's store yet is a skip",
			edit: func(f *Facts) { f.T3 = T3Facts{Found: true} }, want: Skip, wantHas: []string{"the agent hasn't read its store yet"}},
		{name: "a store doctor finds and the agent doesn't warns, and names T3CODE_HOME",
			edit: t3(func(c *T3Code, f *Facts) {
				off("no_store", "")(c, f)
				f.T3 = T3Facts{Found: true}
			}), want: Warn, wantHas: []string{"the agent finds none where it looks", "T3CODE_HOME"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := running()
			tt.edit(&f)
			c, ok := byName(Checks(f), "app.t3_code")
			if tt.absent {
				if ok {
					t.Errorf("there is a T3 Code line: %+v", c)
				}
				return
			}
			if !ok {
				t.Fatalf("there is no T3 Code line in %+v", Checks(f))
			}
			if c.Result != tt.want {
				t.Errorf("app.t3_code is %s %q, want %s", c.Result, c.Text, tt.want)
			}
			for _, w := range tt.wantHas {
				if !strings.Contains(c.Text, w) {
					t.Errorf("app.t3_code says %q, want it to say %q", c.Text, w)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(c.Text, w) {
					t.Errorf("app.t3_code says %q, which it mustn't", c.Text)
				}
			}
		})
	}
}

// TestSQLiteInstall checks the command DESIGN 5.1 has doctor give for
// sqlite3 on each OS, and on Linux for each family of distributions, by
// os-release's ID and ID_LIKE.
func TestSQLiteInstall(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		distro []string
		want   string
	}{
		{"Debian has apt's sqlite3", "linux", []string{"debian"}, "sudo apt install sqlite3"},
		{"Linux Mint is like Ubuntu, so apt", "linux", []string{"linuxmint", "ubuntu", "debian"}, "sudo apt install sqlite3"},
		{"Fedora has dnf's sqlite", "linux", []string{"fedora"}, "sudo dnf install sqlite"},
		{"Rocky Linux is like Fedora, so dnf", "linux", []string{"rocky", "rhel", "centos", "fedora"}, "sudo dnf install sqlite"},
		{"Manjaro is like Arch, so pacman", "linux", []string{"manjaro", "arch"}, "sudo pacman -S sqlite"},
		{"openSUSE has zypper's sqlite3", "linux", []string{"opensuse-tumbleweed", "opensuse", "suse"}, "sudo zypper install sqlite3"},
		{"a distribution doctor doesn't know gets the package's names", "linux", []string{"void"}, "Install your distribution's sqlite3 package"},
		{"a Linux whose os-release doctor couldn't read gets the package's names too", "linux", nil, "Install your distribution's sqlite3 package"},
		{"Windows has winget, and a new login", "windows", nil, "winget install SQLite.SQLite, then log out and back in"},
		{"macOS ships it", "darwin", nil, "macOS ships it"},
		{"another OS gets the tool's name", "freebsd", nil, "Install the sqlite3 command-line tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sqliteInstall(tt.goos, tt.distro); !strings.Contains(got, tt.want) {
				t.Errorf("sqliteInstall(%q, %q) = %q, want it to say %q", tt.goos, tt.distro, got, tt.want)
			}
		})
	}
}

func TestDistroIDs(t *testing.T) {
	tests := []struct {
		name, osRelease string
		want            []string
	}{
		{"ID comes first, then the words of ID_LIKE", "NAME=\"Linux Mint\"\nID=linuxmint\nID_LIKE=\"ubuntu debian\"\n", []string{"linuxmint", "ubuntu", "debian"}},
		{"an ID without ID_LIKE is one word", "ID=fedora\nVERSION_ID=41\n", []string{"fedora"}},
		{"single quotes are taken off as double quotes are", "ID='arch'\n", []string{"arch"}},
		{"case doesn't matter", "ID=Ubuntu\n", []string{"ubuntu"}},
		{"comments, blank lines and other keys are passed over", "# ID=debian\n\nPRETTY_NAME=\"x\"\nID=void\n", []string{"void"}},
		{"a file with no ID gives nothing", "NAME=\"Something\"\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DistroIDs([]byte(tt.osRelease)); !slices.Equal(got, tt.want) {
				t.Errorf("DistroIDs = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWorse(t *testing.T) {
	tests := []struct {
		name string
		a, b Result
		want Result
	}{
		{"a warning is worse than ok", OK, Warn, Warn},
		{"a warning is worse than a skip", Skip, Warn, Warn},
		{"a failure stays a failure", Fail, Warn, Fail},
		{"ok and skip rank alike, so the first stays", Skip, OK, Skip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := worse(tt.a, tt.b); got != tt.want {
				t.Errorf("worse(%s, %s) = %s, want %s", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
