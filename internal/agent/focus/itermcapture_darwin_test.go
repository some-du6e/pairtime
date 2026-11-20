// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

// captureEnv names the directory TestCaptureITerm2 writes its capture to.
// Only platform.yml's iterm2-capture job, which runs by hand, sets it: the
// test opens iTerm2 and Terminal windows and kills and restarts iTerm2,
// which nobody wants on their own Mac, and no check on a hosted runner may
// rest on GUI apps doing that in time (JE must-fix 11). The check is
// TestITerm2Golden, which reads the file in ci.yml.
const captureEnv = "PAIRTIME_CAPTURE_ITERM2"

// stubEnv, set to a duration, makes this test binary sleep that long and
// exit. A copy of it named claude, started that way, is a harness stub:
// darwin.Processes names it claude, from its executable's path.
const stubEnv = "PAIRTIME_FOCUS_STUB_SLEEP"

func TestMain(m *testing.M) {
	if v := os.Getenv(stubEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		time.Sleep(d)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// iTermApp is where iTerm2's Homebrew cask installs it.
const iTermApp = "/Applications/iTerm.app"

// iTermDomain is iTerm2's preferences domain.
const iTermDomain = "com.googlecode.iterm2"

// TestCaptureITerm2 reads the process table TestITerm2Golden checks
// (decision E-6.3-two-tabs), from a real iTerm2 and Terminal on
// macos-latest, and writes it to captureEnv's directory as
// iterm2-macos-latest.json. It sets iTerm2's default profile to run a
// session script, which starts a harness stub as its child, and then:
//
//  1. starts iTerm2, whose first window runs the first stub under an
//     iTermServer, iTerm2's child;
//  2. kills iTerm2, which leaves that iTermServer and its stub running,
//     adopted by launchd, as after iTerm2 crashes;
//  3. starts iTerm2 again, whose new window runs the second stub, under
//     an iTermServer of the new iTerm2;
//  4. opens a Terminal window that runs the third stub.
//
// Then it reads the table and checks it as TestITerm2Golden does, after
// writing it, so a failed check leaves the table to look at.
func TestCaptureITerm2(t *testing.T) {
	out := os.Getenv(captureEnv)
	if out == "" {
		t.Skipf("%s isn't set: this test starts, kills and restarts iTerm2, so only platform.yml's iterm2-capture job runs it", captureEnv)
	}
	if _, err := os.Stat(iTermApp); err != nil {
		t.Fatalf("iTerm2 isn't installed: %v (the job installs it with brew install --cask iterm2)", err)
	}
	c := &capture{t: t, dir: t.TempDir(), out: out, me: darwin.SelfOwner()}
	if strings.ContainsAny(c.dir, "'\"\\ \n") {
		t.Fatalf("the temporary directory %q has characters the session scripts can't quote", c.dir)
	}
	t.Cleanup(c.stopAll)
	t.Cleanup(c.diagnose)
	c.writeStubs()
	c.defaults("import", iTermDomain, c.writePrefs())

	// 1. The first iTerm2 and its first session.
	c.open("-a", iTermApp)
	_, first := c.waitSlot(0, 90*time.Second)
	it1 := c.app("iTerm2")
	tab := c.table()
	server, ok := c.server(tab, first)
	if !ok {
		t.Fatalf("iTerm2 ran the first stub with no iTermServer in its chain: %s", c.chainOf(tab, first))
	}
	c.note("iTerm2 %d ran the first stub, %d, under %s %d, whose parent was %s: %s", it1.PID, first, server.Name, server.PID, c.nameOf(tab, server.PPID), c.namesOf(tab, first))

	// 2. Kill it. The iTermServer and the stub live on.
	c.kill(it1.PID)
	if !alive(first) || !alive(server.PID) {
		t.Fatalf("killing iTerm2 %d also ended the first stub or its iTermServer: %s", it1.PID, c.chainOf(c.table(), first))
	}
	tab = c.table()
	c.note("killing iTerm2 %d with SIGKILL left %s %d and the first stub running, and the server's parent is now %s", it1.PID, server.Name, server.PID, c.nameOf(tab, c.ppidOf(tab, server.PID)))

	// 3. A new iTerm2 and a session of its own, under an iTermServer that
	// is the new iTerm2's child. checkITermCapture fails if it isn't.
	it2, second := c.iTerm2Session()

	// 4. Terminal.
	c.open("-a", "Terminal", filepath.Join(c.dir, "terminal.command"))
	terminal := c.waitPID("terminal.pid", 90*time.Second)
	term := c.app("Terminal")

	procs, err := darwin.Processes()
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(procs, func(a, b platform.Proc) int { return a.PID - b.PID })
	g := itermCapture{
		Case:        "E-6.3-two-tabs: harness stubs in a session of iTerm2, under an iTermServer that doesn't descend from iTerm2, and in Terminal",
		Source:      "TestCaptureITerm2 (internal/agent/focus/itermcapture_darwin_test.go) in platform.yml's iterm2-capture job on macos-latest, run " + os.Getenv("GITHUB_RUN_ID"),
		Captured:    time.Now().UTC().Format(time.RFC3339),
		MacOS:       c.output("sw_vers", "-productVersion") + " (" + c.output("sw_vers", "-buildVersion") + ")",
		ITerm2:      c.output("plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", filepath.Join(iTermApp, "Contents", "Info.plist")),
		Notes:       c.notes,
		ITerm2PID:   it2.PID,
		TerminalPID: term.PID,
		Procs:       procs,
	}
	g.Stubs.UnderITerm2, g.Stubs.UnderITermServer, g.Stubs.InTerminal = second, first, terminal
	c.write(filepath.Join(out, itermCaptureFile), g)
	checkITermCapture(t, g)
}

// capture is TestCaptureITerm2's state.
type capture struct {
	t     *testing.T
	dir   string // the stubs, the scripts and the files they write
	out   string // where the capture goes, with diagnostics on a failure
	me    string // this user's Owner
	notes []string
}

// iTerm2Session starts iTerm2 and waits for a new session of it to start
// a stub, asking for a window in a directory if none opens at launch, as
// when iTerm2 restores the windows of the one before. It returns iTerm2's
// process and the stub's.
func (c *capture) iTerm2Session() (platform.Proc, int) {
	last := c.lastSlot()
	c.open("-a", iTermApp)
	slot, stub, found := c.trySlot(last, 30*time.Second)
	if !found {
		c.note("iTerm2 opened no new session at launch, so the test asked it for a window in a directory")
		c.open("-a", iTermApp, c.dir)
		slot, stub = c.waitSlot(last, 60*time.Second)
	}
	app := c.app("iTerm2")
	c.note("iTerm2 %d ran the stub in slot %d, %d: %s", app.PID, slot, stub, c.namesOf(c.table(), stub))
	return app, stub
}

// diagnose saves what explains a failed capture next to it, for the
// artifact: every process on the runner (ps -ax, whose comm column holds
// paths), iTerm2's preferences and, where the runner lets a shell take
// one, a screenshot, which shows a dialog that held an app up.
func (c *capture) diagnose() {
	if !c.t.Failed() {
		return
	}
	if ps, err := exec.Command("ps", "-axo", "pid,ppid,uid,comm").Output(); err == nil {
		os.WriteFile(filepath.Join(c.out, "ps.txt"), ps, 0o644)
	}
	if prefs, err := exec.Command("/usr/bin/defaults", "read", iTermDomain).CombinedOutput(); err == nil {
		os.WriteFile(filepath.Join(c.out, "iterm2-defaults.txt"), prefs, 0o644)
	}
	os.WriteFile(filepath.Join(c.out, "notes.txt"), []byte(strings.Join(c.notes, "\n")+"\n"), 0o644)
	exec.Command("/usr/sbin/screencapture", "-x", filepath.Join(c.out, "screen.png")).Run()
}

func (c *capture) note(format string, args ...any) {
	c.notes = append(c.notes, fmt.Sprintf(format, args...))
	c.t.Logf(format, args...)
}

// writeStubs writes the harness stub, a copy of this test binary named
// claude, and the two scripts that start it: session.sh, which iTerm2's
// default profile runs as its command and which claims the first free
// slot, and terminal.command, which Terminal runs in a new window.
func (c *capture) writeStubs() {
	exe, err := os.Executable()
	if err != nil {
		c.t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		c.t.Fatal(err)
	}
	c.writeFile(filepath.Join(c.dir, "claude"), b)
	session := `#!/bin/sh
# A harness session for TestCaptureITerm2: claim the first free slot, run
# the stub as this shell's child, record its process id and wait.
cd '` + c.dir + `' || exit 1
n=1
while ! (set -C; : > "slot$n.claim") 2>/dev/null; do
	n=$((n + 1))
	[ "$n" -le 50 ] || exit 1
done
` + stubEnv + `=1h ./claude &
echo $! > "slot$n.tmp" && mv "slot$n.tmp" "slot$n.pid"
wait
`
	c.writeFile(filepath.Join(c.dir, "session.sh"), []byte(session))
	terminal := `#!/bin/sh
cd '` + c.dir + `' || exit 1
` + stubEnv + `=1h ./claude &
echo $! > terminal.tmp && mv terminal.tmp terminal.pid
wait
`
	c.writeFile(filepath.Join(c.dir, "terminal.command"), []byte(terminal))
}

func (c *capture) writeFile(name string, b []byte) {
	if err := os.WriteFile(name, b, 0o755); err != nil {
		c.t.Fatal(err)
	}
}

// writePrefs writes the preferences TestCaptureITerm2 gives iTerm2 and
// returns their file: a default profile whose command is session.sh, and
// no update check, which would ask its question in a window on the second
// launch.
func (c *capture) writePrefs() string {
	var cmd bytes.Buffer
	if err := xml.EscapeText(&cmd, []byte(filepath.Join(c.dir, "session.sh"))); err != nil {
		c.t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Default Bookmark Guid</key>
	<string>pairtime-capture</string>
	<key>New Bookmarks</key>
	<array>
		<dict>
			<key>Guid</key>
			<string>pairtime-capture</string>
			<key>Name</key>
			<string>pairtime capture</string>
			<key>Custom Command</key>
			<string>Yes</string>
			<key>Command</key>
			<string>` + cmd.String() + `</string>
		</dict>
	</array>
	<key>RunJobsInServers</key>
	<true/>
	<key>SUEnableAutomaticChecks</key>
	<false/>
	<key>SUAutomaticallyUpdate</key>
	<false/>
	<key>SUHasLaunchedBefore</key>
	<true/>
	<key>PromptOnQuit</key>
	<false/>
	<key>NoSyncTipsDisabled</key>
	<true/>
</dict>
</plist>
`
	name := filepath.Join(c.dir, "iterm2.plist")
	if err := os.WriteFile(name, []byte(plist), 0o644); err != nil {
		c.t.Fatal(err)
	}
	return name
}

// defaults runs /usr/bin/defaults, which goes through cfprefsd, so iTerm2
// reads what it writes.
func (c *capture) defaults(args ...string) {
	if out, err := exec.Command("/usr/bin/defaults", args...).CombinedOutput(); err != nil {
		c.t.Fatalf("defaults %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// open runs /usr/bin/open, which asks Launch Services, in the runner's
// desktop session, to start or activate an app.
func (c *capture) open(args ...string) {
	if out, err := exec.Command("/usr/bin/open", args...).CombinedOutput(); err != nil {
		c.t.Fatalf("open %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (c *capture) output(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		c.t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// lastSlot is the highest slot a session has claimed so far.
func (c *capture) lastSlot() int {
	last := 0
	for n := 1; n <= 50; n++ {
		if _, err := os.Stat(filepath.Join(c.dir, fmt.Sprintf("slot%d.claim", n))); err == nil {
			last = n
		}
	}
	return last
}

// trySlot waits up to d for a session to record a stub in a slot after
// the slot after, and returns the slot and the stub's process id.
func (c *capture) trySlot(after int, d time.Duration) (slot, pid int, ok bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for n := after + 1; n <= 50; n++ {
			if pid, ok := c.readPID(fmt.Sprintf("slot%d.pid", n)); ok {
				return n, pid, true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return 0, 0, false
}

func (c *capture) waitSlot(after int, d time.Duration) (slot, pid int) {
	slot, pid, ok := c.trySlot(after, d)
	if !ok {
		c.t.Fatalf("no iTerm2 session started a stub within %v; the processes:\n%s", d, c.listing())
	}
	return slot, pid
}

func (c *capture) waitPID(name string, d time.Duration) int {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if pid, ok := c.readPID(name); ok {
			return pid
		}
		time.Sleep(200 * time.Millisecond)
	}
	c.t.Fatalf("no %s within %v; the processes:\n%s", name, d, c.listing())
	return 0
}

func (c *capture) readPID(name string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(c.dir, name))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil && pid > 0
}

func (c *capture) table() *platform.Table {
	procs, err := darwin.Processes()
	if err != nil {
		c.t.Fatal(err)
	}
	return platform.NewTable(platform.Darwin, procs)
}

// app returns this user's newest process named name, waiting up to 30 s
// for it.
func (c *capture) app(name string) platform.Proc {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var found platform.Proc
		for _, p := range c.table().Procs() {
			if p.Name == name && p.Owner == c.me && (found.PID == 0 || p.Start.After(found.Start)) {
				found = p
			}
		}
		if found.PID != 0 {
			return found
		}
		time.Sleep(200 * time.Millisecond)
	}
	c.t.Fatalf("no process named %s; the processes:\n%s", name, c.listing())
	return platform.Proc{}
}

// server returns the iTermServer in pid's chain.
func (c *capture) server(tab *platform.Table, pid int) (platform.Proc, bool) {
	for _, p := range tab.Chain(pid) {
		if strings.HasPrefix(appName(p), "itermserver") {
			return p, true
		}
	}
	return platform.Proc{}, false
}

func (c *capture) ppidOf(tab *platform.Table, pid int) int {
	p, _ := tab.Get(pid)
	return p.PPID
}

func (c *capture) nameOf(tab *platform.Table, pid int) string {
	if p, ok := tab.Get(pid); ok {
		return fmt.Sprintf("%s %d", p.Name, p.PID)
	}
	return fmt.Sprintf("process %d, gone", pid)
}

// namesOf is pid's chain by name, nearest first.
func (c *capture) namesOf(tab *platform.Table, pid int) string {
	var names []string
	for _, p := range tab.Chain(pid) {
		names = append(names, p.Name)
	}
	return strings.Join(names, " < ")
}

// chainOf is pid's chain in full, for a failure.
func (c *capture) chainOf(tab *platform.Table, pid int) string {
	var parts []string
	for _, p := range tab.Chain(pid) {
		parts = append(parts, fmt.Sprintf("%s %d (parent %d, %s)", p.Name, p.PID, p.PPID, p.Owner))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("process %d isn't running", pid)
	}
	return strings.Join(parts, " < ")
}

// listing is this user's processes and the chains of the stubs, for a
// failure.
func (c *capture) listing() string {
	tab := c.table()
	var b strings.Builder
	for _, p := range tab.Procs() {
		if p.Owner == c.me || p.Name == "login" {
			fmt.Fprintf(&b, "%d %d %s %s\n", p.PID, p.PPID, p.Owner, p.Name)
		}
	}
	return b.String()
}

func (c *capture) kill(pid int) {
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		c.t.Fatalf("killing %d: %v", pid, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(pid) {
		c.t.Fatalf("process %d is still running 15 s after SIGKILL", pid)
	}
}

// alive reports whether a process with this pid runs. A zombie counts, but
// launchd reaps orphans at once.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// stopAll ends what the test started: the stubs, iTerm2 with its servers,
// and Terminal.
func (c *capture) stopAll() {
	for _, p := range c.table().Procs() {
		if p.Owner != c.me {
			continue
		}
		switch n := appName(p); {
		case n == "claude", n == "iterm2", n == "terminal", strings.HasPrefix(n, "itermserver"):
			syscall.Kill(p.PID, syscall.SIGKILL)
		}
	}
}

// write writes g as TestITerm2Golden reads it, one process to a line.
// The notes keep their < as it is, not as \u003c.
func (c *capture) write(name string, g itermCapture) {
	procs := g.Procs
	g.Procs = nil
	var enc bytes.Buffer
	e := json.NewEncoder(&enc)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(g); err != nil {
		c.t.Fatal(err)
	}
	head := enc.Bytes()
	// head ends with `"procs": null` and the closing brace.
	cut := bytes.LastIndex(head, []byte(`"procs": null`))
	if cut < 0 {
		c.t.Fatalf("unexpected encoding: %s", head)
	}
	var b bytes.Buffer
	b.Write(head[:cut])
	b.WriteString("\"procs\": [\n")
	for i, p := range procs {
		line, err := json.Marshal(p)
		if err != nil {
			c.t.Fatal(err)
		}
		b.WriteString("    ")
		b.Write(line)
		if i < len(procs)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("  ]\n}\n")
	var check itermCapture
	if err := json.Unmarshal(b.Bytes(), &check); err != nil {
		c.t.Fatalf("the capture doesn't read back: %v", err)
	}
	if err := os.WriteFile(name, b.Bytes(), 0o644); err != nil {
		c.t.Fatal(err)
	}
}
