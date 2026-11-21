// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// running is the facts of a running agent, with no harness found.
func running() Facts {
	return Facts{
		Reach: Reached, Version: "v0.1.0", AskTook: 2 * time.Millisecond, Adapters: []string{"claude_code"},
		Agent: &Status{
			Version: Version{Version: "v0.1.0"}, UptimeS: 3600, Mode: "local",
			IPC:     IPC{Via: "socket"},
			Signals: Signals{Idle: Signal{Via: "GetLastInputInfo"}, Focus: Signal{Via: "SetWinEventHook"}},
			Adapters: Adapters{Harnesses: map[string]HarnessCounts{
				"claude_code": {Calls: 10, Records: 40},
			}},
		},
		Keystore: Keystore{Store: "Windows DPAPI"},
		Signing:  Signing{Release: true, Version: "v0.1.0", Commit: "0123456789abcdef0123", Verdict: Unsigned, State: "not code-signed"},
	}
}

// byName returns the check named name, and whether there is one.
func byName(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestChecks(t *testing.T) {
	claude := func(edit func(*Hooks)) func(*Facts) {
		return func(f *Facts) {
			h := Hooks{Harness: "claude_code", Title: "Claude Code", Found: true, Settings: "/home/teen/.claude/settings.json",
				Events: []string{"SessionStart", "UserPromptSubmit"}, Exes: []string{"/home/teen/.local/bin/pairtime"},
				Trips: []time.Duration{9 * time.Millisecond, 30 * time.Millisecond, 12 * time.Millisecond}}
			edit(&h)
			f.Hooks = []Hooks{h}
		}
	}
	tests := []struct {
		name    string
		edit    func(*Facts)
		check   string
		want    Result
		wantHas []string
		absent  bool
	}{
		{name: "a running agent of this version is ok, with what its mode uploads and how fast it answered",
			edit: func(*Facts) {}, check: "agent", want: OK,
			wantHas: []string{"running pairtime v0.1.0 for 1h, in local mode, which uploads nothing; it answered in 2 ms"}},
		{name: "a signed-in agent says it uploads your hours' numbers",
			edit: func(f *Facts) { f.Agent.Mode = "online" }, check: "agent", want: OK, wantHas: []string{"signed in, so it uploads your hours' numbers"}},
		{name: "an agent on the named pipe says hooks reach it there",
			edit: func(f *Facts) { f.Agent.IPC.Via = "pipe" }, check: "agent", want: OK, wantHas: []string{"named pipe"}},
		{name: "an agent of another version warns, and says how to move it to this one",
			edit: func(f *Facts) { f.Agent.Version.Version = "v0.0.9" }, check: "agent", want: Warn,
			wantHas: []string{"This pairtime is v0.1.0", "run pairtime install with this one"}},
		{name: "no agent fails and says how to start one",
			edit: func(f *Facts) { f.Reach, f.Agent = Down, nil }, check: "agent", want: Fail,
			wantHas: []string{"isn't running", "pairtime agent --detach"}},
		{name: "with no agent the idle line is skipped, never read from the terminal",
			edit: func(f *Facts) { f.Reach, f.Agent = Down, nil }, check: "idle", want: Skip,
			wantHas: []string{"not from the terminal it runs in"}},
		{name: "with no agent the spool line is skipped",
			edit: func(f *Facts) { f.Reach, f.Agent = Down, nil }, check: "spool", want: Skip},
		{name: "idle time with a source is idle detection working, from it",
			edit: func(*Facts) {}, check: "idle", want: OK, wantHas: []string{"idle detection working, from GetLastInputInfo"}},
		{name: "no idle source is presence unknown on this desktop",
			edit: func(f *Facts) { f.Agent.Signals.Idle = Signal{Unknown: "no session bus"} }, check: "idle", want: Warn,
			wantHas: []string{"presence unknown on this desktop: no session bus"}},
		{name: "no focus source is focus unknown on this desktop, without the level-C clause WP-SC8 adds",
			edit: func(f *Facts) {
				f.Agent.Signals.Focus = Signal{Unknown: "Wayland gives other programs no focus signal"}
			}, check: "focus", want: Warn,
			wantHas: []string{"focus unknown on this desktop: Wayland gives other programs no focus signal"}},
		{name: "the macOS loop off the main thread warns that locks and app switches may not arrive",
			edit: func(f *Facts) { f.Agent.Signals.Loop = &Loop{} }, check: "macos-loop", want: Warn,
			wantHas: []string{"may not arrive"}},
		{name: "the macOS loop on the main thread is ok",
			edit: func(f *Facts) { f.Agent.Signals.Loop = &Loop{MainThread: true} }, check: "macos-loop", want: OK},
		{name: "a desktop with no macOS loop has no line for it",
			edit: func(*Facts) {}, check: "macos-loop", absent: true},
		{name: "a hook's line is the median of its warm test calls",
			edit: claude(func(*Hooks) {}), check: "hook.claude_code", want: OK,
			wantHas: []string{"a test call took 12 ms from start to exit, the median of 3"}},
		{name: "a hook slower than R8's 50 ms warns and points to the report",
			edit:  claude(func(h *Hooks) { h.Trips = []time.Duration{80 * time.Millisecond} }),
			check: "hook.claude_code", want: Warn, wantHas: []string{"80 ms", "more than the 50 ms", "pairtime doctor --report"}},
		{name: "a test call that fails fails, with the binary and why, and says to install again",
			edit:  claude(func(h *Hooks) { h.Trips, h.TripError = nil, "it exited with status 2" }),
			check: "hook.claude_code", want: Fail, wantHas: []string{"/home/teen/.local/bin/pairtime", "it exited with status 2", "pairtime install"}},
		{name: "disableAllHooks fails and says which line to take out",
			edit:  claude(func(h *Hooks) { h.Disabled = true }),
			check: "hook.claude_code", want: Fail, wantHas: []string{`"disableAllHooks": true`, "Take that line out"}},
		{name: "a missing hook warns and names it",
			edit:  claude(func(h *Hooks) { h.Missing = []string{"Stop"} }),
			check: "hook.claude_code", want: Warn, wantHas: []string{"1 of pairtime's hooks is missing", "(Stop)", "pairtime install"}},
		{name: "hooks that run two binaries warn",
			edit:  claude(func(h *Hooks) { h.Exes = append(h.Exes, "/opt/pairtime") }),
			check: "hook.claude_code", want: Warn, wantHas: []string{"2 different pairtime binaries"}},
		{name: "a harness on the computer without pairtime's hooks warns to install them",
			edit:  claude(func(h *Hooks) { h.Events, h.Exes, h.Trips = nil, nil, nil }),
			check: "hook.claude_code", want: Warn, wantHas: []string{"pairtime's hooks aren't in /home/teen/.claude/settings.json", "pairtime install"}},
		{name: "a harness that isn't on the computer and has no hooks has no line",
			edit:  claude(func(h *Hooks) { h.Found, h.Events, h.Exes, h.Trips = false, nil, nil, nil }),
			check: "hook.claude_code", absent: true},
		{name: "Claude Code's hooks bring the folder-trust hint",
			edit: claude(func(*Hooks) {}), check: "trust.claude_code", want: OK,
			wantHas: []string{"only once you accept its trust dialog there", "--bare runs fire no hooks"}},
		{name: "without pairtime's hooks there is no trust hint",
			edit:  claude(func(h *Hooks) { h.Events, h.Exes, h.Trips = nil, nil, nil }),
			check: "trust.claude_code", absent: true},
		{name: "settings that don't read warn that doctor can't tell",
			edit:  claude(func(h *Hooks) { h.ReadError = "settings.json isn't valid JSON" }),
			check: "hook.claude_code", want: Warn, wantHas: []string{"can't tell", "isn't valid JSON"}},
		{name: "an adapter's parse failures warn, with the last one",
			edit: func(f *Facts) {
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Calls: 5, ParseFailures: 2, LastFailure: "Stop: unknown field"}
			},
			check: "adapter.claude_code", want: Warn, wantHas: []string{"2 calls didn't fully parse", "Stop: unknown field"}},
		{name: "an unsigned release is ok, and says releases aren't signed yet",
			edit: func(*Facts) {}, check: "signing", want: OK,
			wantHas: []string{"release v0.1.0 from commit 0123456789ab, not code-signed", "aren't code-signed yet"}},
		{name: "a development build says so",
			edit: func(f *Facts) {
				f.Signing = Signing{Verdict: NoSigning, State: "not code-signed, as Linux programs aren't"}
			}, check: "signing", want: OK,
			wantHas: []string{"a development build, not a release, not code-signed, as Linux programs aren't"}},
		{name: "a binary that doesn't match its signature fails and says to download it again",
			edit: func(f *Facts) {
				f.Signing.Verdict, f.Signing.State = Broken, "signed, but it doesn't match its signature"
			}, check: "signing", want: Fail,
			wantHas: []string{"changed after it was signed", "Download pairtime again"}},
		{name: "a signature Windows doesn't trust warns",
			edit: func(f *Facts) {
				f.Signing.Verdict, f.Signing.State = Untrusted, "signed, but Windows doesn't trust the signature"
			}, check: "signing", want: Warn},
		{name: "a trusted signature is ok",
			edit: func(f *Facts) {
				f.Signing.Verdict, f.Signing.State = Trusted, "signed, and Windows trusts the signature"
			}, check: "signing", want: OK,
			wantHas: []string{"Windows trusts the signature"}},
		{name: "the device key in the OS's store is ok, naming the store",
			edit: func(*Facts) {}, check: "keystore", want: OK, wantHas: []string{"the device key is in Windows DPAPI"}},
		{name: "the device key in the key file warns with the keystore's own warning",
			edit: func(f *Facts) {
				f.Keystore = Keystore{Store: "the key file", Warning: "pairtime keeps its device key in a file that only your account has permission to read"}
			},
			check: "keystore", want: Warn, wantHas: []string{"pairtime keeps its device key in a file"}},
		{name: "no device key yet warns that nothing is recorded",
			edit: func(f *Facts) { f.Keystore = Keystore{Missing: true} }, check: "keystore", want: Warn, wantHas: []string{"no device key yet"}},
		{name: "a keystore.json that doesn't read fails",
			edit: func(f *Facts) { f.Keystore = Keystore{Error: "keys: damaged"} }, check: "keystore", want: Fail},
		{name: "the keystore's problem isn't printed twice, since the keystore line gives it",
			edit: func(f *Facts) { f.Agent.Problems = map[string]string{"keystore": "the warning"} }, check: "problem.keystore", absent: true},
		{name: "no spooled call in the week is ok",
			edit: func(*Facts) {}, check: "spool", want: OK, wantHas: []string{"none only through the spool"}},
		{name: "spooled calls warn with how many were prompts, why they count for less and what keeps them off the spool",
			edit: func(f *Facts) { f.Agent.IPC.Spooled = Spooled{Calls: 12, Prompts: 3} }, check: "spool", want: Warn,
			wantHas: []string{"12 hook calls of the last 7 days reached the agent only through the spool, 3 of them prompts", "credits less", "logging in starts it"}},
		{name: "one spooled prompt reads as one",
			edit: func(f *Facts) { f.Agent.IPC.Spooled = Spooled{Calls: 1, Prompts: 1} }, check: "spool", want: Warn,
			wantHas: []string{"1 hook call of the last 7 days reached the agent only through the spool, and it was a prompt"}},
		{name: "spooled calls an older version recorded say their kind isn't known",
			edit: func(f *Facts) { f.Agent.IPC.Spooled = Spooled{Calls: 4, Prompts: 1, Unknown: 2} }, check: "spool", want: Warn,
			wantHas: []string{"1 of them a prompt, and 2 from a pairtime too old to record whether they were"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := running()
			tt.edit(&f)
			c, ok := byName(Checks(f), tt.check)
			if tt.absent {
				if ok {
					t.Errorf("there is a %s line: %+v", tt.check, c)
				}
				return
			}
			if !ok {
				t.Fatalf("there is no %s line in %+v", tt.check, Checks(f))
			}
			if c.Result != tt.want {
				t.Errorf("%s is %s %q, want %s", tt.check, c.Result, c.Text, tt.want)
			}
			for _, w := range tt.wantHas {
				if !strings.Contains(c.Text, w) {
					t.Errorf("%s says %q, want it to say %q", tt.check, c.Text, w)
				}
			}
		})
	}
}

func TestWrite(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   string
	}{
		{
			name: "each check is a line, with its result, its name in a column as wide as the longest and its text, then the counts",
			checks: []Check{
				{Name: "agent", Result: OK, Text: "running"},
				{Name: "hook.claude_code", Result: Fail, Text: "failed"},
				{Name: "idle", Result: Warn, Text: "unknown"},
				{Name: "focus", Result: Skip, Text: "skipped"},
			},
			want: "ok    agent             running\n" +
				"FAIL  hook.claude_code  failed\n" +
				"warn  idle              unknown\n" +
				"skip  focus             skipped\n" +
				"doctor: 1 ok, 1 warning, 1 failed, 1 skipped\n",
		},
		{
			name:   "with no checks there is only the count",
			checks: nil,
			want:   "doctor: 0 ok, 0 warnings, 0 failed, 0 skipped\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := Write(&b, tt.checks); err != nil {
				t.Fatal(err)
			}
			if b.String() != tt.want {
				t.Errorf("Write printed\n%s\nwant\n%s", b.String(), tt.want)
			}
		})
	}
}

func TestMedian(t *testing.T) {
	ms := time.Millisecond
	tests := []struct {
		name string
		in   []time.Duration
		want time.Duration
	}{
		{"no calls have no median", nil, 0},
		{"an odd count's median is its middle call", []time.Duration{9 * ms, 1 * ms, 5 * ms}, 5 * ms},
		{"an even count's median is the mean of its two middle calls", []time.Duration{4 * ms, 1 * ms, 9 * ms, 6 * ms}, 5 * ms},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Median(tt.in); got != tt.want {
				t.Errorf("Median(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestMillis(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"under a millisecond reads as under 1 ms", 400 * time.Microsecond, "under 1 ms"},
		{"a time rounds to the nearest millisecond", 23600 * time.Microsecond, "24 ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Millis(tt.in); got != tt.want {
				t.Errorf("Millis(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
