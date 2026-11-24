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
		wantNot []string
		absent  bool
	}{
		{name: "a running agent of this version is ok, with what its mode uploads and how fast it answered",
			edit: func(*Facts) {}, check: "agent", want: OK,
			wantHas: []string{"running pairtime v0.1.0 for 1h, in local mode, which uploads nothing; it answered in 2 ms"}},
		{name: "a signed-in agent says it uploads your hours' numbers",
			edit: func(f *Facts) { f.Agent.Mode = "online" }, check: "agent", want: OK, wantHas: []string{"signed in, so it uploads your hours' numbers"}},
		{name: "a signed-in agent whose uploader reports a problem names problem.uploader rather than saying it uploads",
			edit: func(f *Facts) {
				f.Agent.Mode = "online"
				f.Agent.Problems = map[string]string{"uploader": "pairtime has stopped uploading; to upload again, run pairtime logout, then pairtime login"}
			}, check: "agent", want: OK,
			wantHas: []string{"signed in, but its uploader reports a problem, which problem.uploader names"},
			wantNot: []string{"so it uploads"}},
		{name: "a signed-in agent whose sign-in doesn't read says it uploads nothing, and names problem.account",
			edit: func(f *Facts) {
				f.Agent.Mode = "online"
				f.Agent.Problems = map[string]string{"account": "pairtime uploads nothing, because this computer is linked to a server but its sign-in doesn't read: the keyring is locked",
					"uploader": "the server's receipt for batch 3 doesn't check out, so it isn't kept"}
			}, check: "agent", want: OK,
			wantHas: []string{"in online mode, but it uploads nothing (problem.account says why)"},
			wantNot: []string{"so it uploads", "problem.uploader"}},
		{name: "an online agent whose ledger hasn't opened names problem.ledger rather than saying it uploads, since no uploader runs",
			edit: func(f *Facts) {
				f.Agent.Mode = "online"
				f.Agent.Problems = map[string]string{"ledger": "the agent records nothing in the ledger until it opens, and tries again in 30s: the keyring is locked"}
			}, check: "agent", want: OK,
			wantHas: []string{"in online mode, but its ledger reports a problem, which problem.ledger names"},
			wantNot: []string{"so it uploads", "signed in"}},
		{name: "a signed-in agent with other problems only still says it uploads",
			edit: func(f *Facts) {
				f.Agent.Mode = "online"
				f.Agent.Problems = map[string]string{"focus": "focus detection stopped"}
			}, check: "agent", want: OK, wantHas: []string{"signed in, so it uploads your hours' numbers"}},
		{name: "an agent on the named pipe says hooks reach it there",
			edit: func(f *Facts) { f.Agent.IPC.Via = "pipe" }, check: "agent", want: OK, wantHas: []string{"named pipe"}},
		{name: "an agent of another version warns, and says how to move it to this one",
			edit: func(f *Facts) { f.Agent.Version.Version = "v0.0.9" }, check: "agent", want: Warn,
			wantHas: []string{"This pairtime is v0.1.0", "run pairtime install with this one"}},
		{name: "no agent fails and says how to start one",
			edit: func(f *Facts) { f.Reach, f.Agent = Down, nil }, check: "agent", want: Fail,
			wantHas: []string{"isn't running", "pairtime agent --detach"}},
		{name: "an agent that holds its folder but answers nothing on its socket fails, and says how to restart it",
			edit: func(f *Facts) { f.Reach, f.Agent = Deaf, nil }, check: "agent", want: Fail,
			wantHas: []string{"nothing answers on its socket", "every hook call goes to the spool", "Log out and back in"}},
		{name: "with no agent the idle line is skipped, never read from the terminal",
			edit: func(f *Facts) { f.Reach, f.Agent = Down, nil }, check: "idle", want: Skip,
			wantHas: []string{"not from the terminal it runs in"}},
		{name: "with no agent the spool line is skipped",
			edit: func(f *Facts) { f.Reach, f.Agent = Down, nil }, check: "spool", want: Skip},
		{name: "idle time with a source is idle detection working, from it",
			edit: func(*Facts) {}, check: "idle", want: OK, wantHas: []string{"idle detection working, from GetLastInputInfo"}},
		{name: "no idle source with no desktop session is presence unknown on this desktop, a failure, since pairtime counts none of that time, with a new login to fix it",
			edit: func(f *Facts) {
				f.Agent.Signals.Idle, f.Agent.Signals.Session = Signal{Unknown: "no graphical session: DISPLAY and WAYLAND_DISPLAY are unset"}, "none"
			}, check: "idle", want: Fail,
			wantHas: []string{"presence unknown on this desktop: no graphical session: DISPLAY and WAYLAND_DISPLAY are unset. pairtime counts none of your time here yet; logging out and back in"}},
		{name: "a macOS or Windows source that didn't start fails with a new login to fix it",
			edit: func(f *Facts) { f.Agent.Signals.Focus = Signal{Unknown: "the macOS notification loop didn't start"} }, check: "focus", want: Fail,
			wantHas: []string{"logging out and back in starts the agent in your desktop's session"}},
		{name: "on X11, focus unknown because the window manager doesn't keep _NET_ACTIVE_WINDOW names a window manager that does",
			edit: func(f *Facts) {
				f.Agent.Signals.Focus, f.Agent.Signals.Session = Signal{Unknown: "the window manager doesn't keep _NET_ACTIVE_WINDOW"}, "x11"
			}, check: "focus", want: Fail,
			wantHas: []string{"a window manager that keeps _NET_ACTIVE_WINDOW, as most do, lets it see the window in front"},
			wantNot: []string{"logging out"}},
		{name: "on X11, presence unknown names the X server's MIT-SCREEN-SAVER extension",
			edit: func(f *Facts) {
				f.Agent.Signals.Idle, f.Agent.Signals.Session = Signal{Unknown: "the X server has no MIT-SCREEN-SAVER extension"}, "x11"
			}, check: "idle", want: Fail,
			wantHas: []string{"an X server with the MIT-SCREEN-SAVER extension, as most have, lets it see when you're at the keyboard"},
			wantNot: []string{"logging out"}},
		{name: "on X11, focus unknown because the agent couldn't connect to the X display at its start names the X connection, not a window manager",
			edit: func(f *Facts) {
				f.Agent.Signals.Focus, f.Agent.Signals.Session = Signal{Unknown: "can't connect to the X display: dial unix /tmp/.X11-unix/X0: connect: no such file or directory"}, "x11"
			}, check: "focus", want: Fail,
			wantHas: []string{"the agent couldn't reach the X server, as with a DISPLAY or an X authority that isn't your desktop's; logging out and back in starts the agent in your desktop's session, where it can see the window in front"},
			wantNot: []string{"_NET_ACTIVE_WINDOW"}},
		{name: "on X11, presence unknown because the X server refused the connection names the X connection, not an extension",
			edit: func(f *Facts) {
				f.Agent.Signals.Idle, f.Agent.Signals.Session = Signal{Unknown: "the X server refused the connection: Authorization required"}, "x11"
			}, check: "idle", want: Fail,
			wantHas: []string{"the agent couldn't reach the X server", "where it can see when you're at the keyboard"},
			wantNot: []string{"MIT-SCREEN-SAVER"}},
		{name: "on X11, presence unknown because the connection's setup failed names the X connection",
			edit: func(f *Facts) {
				f.Agent.Signals.Idle, f.Agent.Signals.Session = Signal{Unknown: "X connection setup: unexpected EOF"}, "x11"
			}, check: "idle", want: Fail, wantHas: []string{"the agent couldn't reach the X server"}, wantNot: []string{"MIT-SCREEN-SAVER"}},
		{name: "on X11, focus unknown because DISPLAY names a screen the X display hasn't names the X connection",
			edit: func(f *Facts) {
				f.Agent.Signals.Focus, f.Agent.Signals.Session = Signal{Unknown: "the X display has no screen 1"}, "x11"
			}, check: "focus", want: Fail, wantHas: []string{"the agent couldn't reach the X server"}, wantNot: []string{"_NET_ACTIVE_WINDOW"}},
		{name: "on X11, presence unknown because the X server wants more authentication names the X connection",
			edit: func(f *Facts) {
				f.Agent.Signals.Idle, f.Agent.Signals.Session = Signal{Unknown: "the X server wants more authentication: Kerberos"}, "x11"
			}, check: "idle", want: Fail, wantHas: []string{"the agent couldn't reach the X server"}, wantNot: []string{"MIT-SCREEN-SAVER"}},
		{name: "on X11, focus unknown because DISPLAY can't be read names the X connection",
			edit: func(f *Facts) {
				f.Agent.Signals.Focus, f.Agent.Signals.Session = Signal{Unknown: "DISPLAY has no display number"}, "x11"
			}, check: "focus", want: Fail, wantHas: []string{"the agent couldn't reach the X server"}, wantNot: []string{"_NET_ACTIVE_WINDOW"}},
		{name: "on X11, focus unknown while the agent reconnects to the X server says to run doctor again",
			edit: func(f *Facts) {
				f.Agent.Signals.Focus, f.Agent.Signals.Session = Signal{Unknown: "lost the X connection; reconnecting"}, "x11"
			}, check: "focus", want: Fail,
			wantHas: []string{"the agent is reconnecting to the X server, so run pairtime doctor again in a minute"}},
		{name: "no focus source on Wayland is focus unknown on this desktop, a failure that names the X11 session to fix it, without the level-C clause WP-SC8 adds",
			edit: func(f *Facts) {
				f.Agent.Signals.Focus, f.Agent.Signals.Session = Signal{Unknown: "Wayland gives other programs no focus signal"}, "wayland"
			}, check: "focus", want: Fail,
			wantHas: []string{"focus unknown on this desktop: Wayland gives other programs no focus signal. pairtime counts none of your time here yet; an X11 session, where your login screen offers one, lets it see the window in front"},
			wantNot: []string{"level C"}},
		{name: "the macOS loop off the main thread warns that locks and app switches may not arrive, and says to share that line alone",
			edit: func(f *Facts) { f.Agent.Signals.Loop = &Loop{} }, check: "macos-loop", want: Warn,
			wantHas: []string{"may not arrive", "Report this line, and pairtime doctor --report if it helps", "not the rest of doctor's output, which names your folders"}},
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
		{name: "a test listener doctor couldn't start is a skip, which blames neither the hook nor its binary",
			edit: claude(func(h *Hooks) {
				h.Trips, h.RigError = nil, "doctor's test listener: mkdir /tmp/pt1: read-only file system"
			}),
			check: "hook.claude_code", want: Skip, wantHas: []string{"doctor couldn't start its test listener, so it didn't time the hook"},
			wantNot: []string{"failed a test call", "pairtime install"}},
		{name: "the home folder in a line reads as ~, so a line someone shares names fewer of their folders",
			edit: func(f *Facts) {
				f.Homes = []string{"/home/teen"}
				f.Agent.Problems = map[string]string{"config": "couldn't read /home/teen/.config/pairtime/config.toml"}
			},
			check: "problem.config", want: Warn, wantHas: []string{"couldn't read ~/.config/pairtime/config.toml"}, wantNot: []string{"/home/teen"}},
		{name: "a Windows home folder reads as ~ in any case and in its 8.3 short form, as TEMP and the test listener's errors spell it",
			edit: func(f *Facts) {
				f.Homes = []string{`C:\Users\karimeltaib`, `C:\Users\KARIME~1`}
				f.Agent.Problems = map[string]string{"ipc": `mkdir C:\Users\KARIME~1\AppData\Local\Temp\pt1: denied; c:\users\KARIMELTAIB\x is gone`}
			},
			check: "problem.ipc", want: Warn, wantHas: []string{`mkdir ~\AppData\Local\Temp\pt1: denied; ~\x is gone`},
			wantNot: []string{"KARIME", "karime"}},
		{name: "a hook that runs without install's arguments warns that its calls count as parse failures",
			edit:  claude(func(h *Hooks) { h.Stale = []string{"UserPromptSubmit"} }),
			check: "hook.claude_code", want: Warn, wantHas: []string{"the hooks for UserPromptSubmit don't run with the arguments install writes", "parse failure", "pairtime install"}},
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
		{name: "records of lines a replay read again, which the ledger had already, are counted, and aren't a warning",
			edit: func(f *Facts) {
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Calls: 5, Lines: 30, Records: 28, Repeats: 2}
			},
			check: "adapter.claude_code", want: OK,
			wantHas: []string{"5 calls and 30 lines of its logs made 28 records, and left out 2 records the ledger already had, of lines it read again and calls it parsed again, with no parse failures"}},
		{name: "records left out as repeats are counted when no call or fresh line came, as when only an idle marker's output came again",
			edit: func(f *Facts) {
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Repeats: 1}
			},
			check: "adapter.claude_code", want: OK,
			wantHas: []string{"found; since the agent started, 0 calls made 0 records, and left out 1 record the ledger already had, of lines it read again and calls it parsed again, with no parse failures"},
			wantNot: []string{"no hook call has come in"}},
		{name: "records left out as repeats stay with the counts, before a parse failure and the request to report it",
			edit: func(f *Facts) {
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Calls: 5, Lines: 30, Records: 28, Repeats: 2, ParseFailures: 1, LastFailure: "Stop: unknown field"}
			},
			check: "adapter.claude_code", want: Warn,
			wantHas: []string{"5 calls and 30 lines of its logs made 28 records, and left out 2 records the ledger already had, of lines it read again and calls it parsed again, but 1 call didn't fully parse; the last failure: Stop: unknown field. pairtime may count less of this harness until it reads them. Report this line"}},
		{name: "an adapter's parse failures warn, with the last failure and where to report them",
			edit: func(f *Facts) {
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Calls: 5, ParseFailures: 2, LastFailure: "Stop: unknown field"}
			},
			check: "adapter.claude_code", want: Warn, wantHas: []string{"but 2 calls didn't fully parse; the last failure: Stop: unknown field", "Report this line, and pairtime doctor --report if it helps", "which names your folders"}},
		{name: "a record the ledger refused warns, and isn't called a parse failure",
			edit: func(f *Facts) {
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Calls: 5, Records: 4, WriteFailures: 1, LastFailure: "the ledger refused a prompt.submit record: closed"}
			},
			check: "adapter.claude_code", want: Warn, wantHas: []string{"but the ledger refused 1 record; the last failure: the ledger refused a prompt.submit record"},
			wantNot: []string{"parse", "Report this line"}},
		{name: "records the ledger dropped as made while config.toml said local-only are counted, and aren't a warning, since they were never to upload",
			edit: func(f *Facts) {
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Calls: 5, Records: 4, LateLocal: 2}
			},
			check: "adapter.claude_code", want: OK,
			wantHas: []string{"5 calls made 4 records, with no parse failures; 2 records made while config.toml said local-only came only after pairtime had uploaded again, so the ledger dropped them"},
			wantNot: []string{"refused"}},
		{name: "one such record is it",
			edit: func(f *Facts) {
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Calls: 5, Records: 4, LateLocal: 1}
			},
			check: "adapter.claude_code", want: OK,
			wantHas: []string{"1 record made while config.toml said local-only came only after pairtime had uploaded again, so the ledger dropped it,",
				"as nothing recorded in local-only mode may upload (a log line read again, or a hook call parsed again, after a restart counts again)"}},
		{name: "the records the ledger dropped over the chain's life are a line of their own, which points to the adapter lines for each harness's since the start",
			edit: func(f *Facts) {
				f.Agent.Ledger = &Ledger{LateLocal: 12}
				f.Agent.Adapters.Harnesses["claude_code"] = HarnessCounts{Calls: 5, Records: 4, LateLocal: 2}
			},
			check: "ledger", want: OK,
			wantHas: []string{"has dropped 12 records made while config.toml said local-only that came only after pairtime had uploaded again",
				"may upload (a log line read again, or a hook call parsed again, after a restart counts again); the adapter lines count each harness's since the agent started"}},
		{name: "drops no adapter made since the start, as presence's or the git watcher's, don't point to the adapter lines",
			edit: func(f *Facts) { f.Agent.Ledger = &Ledger{LateLocal: 1} }, check: "ledger", want: OK,
			wantHas: []string{"has dropped 1 record made while"}, wantNot: []string{"adapter lines"}},
		{name: "a ledger that has dropped nothing has no line",
			edit: func(f *Facts) { f.Agent.Ledger = &Ledger{} }, check: "ledger", absent: true},
		{name: "nor has one that isn't open",
			edit: func(*Facts) {}, check: "ledger", absent: true},
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
		{name: "a signature doctor couldn't check is skipped, never ok",
			edit: func(f *Facts) {
				f.Signing.Verdict, f.Signing.State = Unchecked, "its signature couldn't be checked: it isn't a 64-bit Mach-O binary"
			}, check: "signing", want: Skip,
			wantHas: []string{"couldn't be checked"}},
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
		{name: "a spool the agent stopped, as its ledger can't open, warns that hooks spool nothing, and why",
			edit: func(f *Facts) { f.SpoolStopped = true }, check: "spool", want: Warn,
			wantHas: []string{"hooks spool nothing", "the ledger can't open until you run pairtime restore", "is lost"}},
		{name: "so does it with no agent to ask, as doctor reads it from the data folder",
			edit: func(f *Facts) { f.Reach, f.Agent, f.SpoolStopped = Down, nil, true }, check: "spool", want: Warn,
			wantHas: []string{"hooks spool nothing"}},
		{name: "spooled calls warn with how many were prompts, why they count for less and what keeps them off the spool",
			edit: func(f *Facts) { f.Agent.IPC.Spooled = Spooled{Calls: 12, Prompts: 3} }, check: "spool", want: Warn,
			wantHas: []string{"12 hook calls of the last 7 days reached the agent only through the spool, 3 of them prompts", "credits less", "logging in starts it"}},
		{name: "one spooled prompt reads as one",
			edit: func(f *Facts) { f.Agent.IPC.Spooled = Spooled{Calls: 1, Prompts: 1} }, check: "spool", want: Warn,
			wantHas: []string{"1 hook call of the last 7 days reached the agent only through the spool, and it was a prompt"}},
		{name: "spooled calls an older version recorded count apart from those whose kind is known",
			edit: func(f *Facts) { f.Agent.IPC.Spooled = Spooled{Calls: 4, Prompts: 1, Unknown: 2} }, check: "spool", want: Warn,
			wantHas: []string{"4 hook calls", "1 of the 2 it can tell was a prompt, and 2 from a pairtime too old to record whether they were"}},
		{name: "where the data folder's access list decides who may open its files, as on Windows, the private files line is skipped",
			edit: func(f *Facts) { f.Modes = false }, check: "private files", want: Skip},
		{name: "where file modes decide and nothing is open to other accounts, the private files line is ok",
			edit: func(f *Facts) { f.Modes = true }, check: "private files", want: OK},
		{name: "a held file open to other accounts warns, naming it and the chmod that closes it",
			edit: func(f *Facts) {
				f.Modes = true
				f.Exposed = []Exposed{{Path: "/srv/pairtime/held.jsonl", Fix: "chmod 600 /srv/pairtime/held.jsonl"}}
			}, check: "private files", want: Warn,
			wantHas: []string{"/srv/pairtime/held.jsonl is open to other accounts on this computer", "could read the prompts", "Run: chmod 600 /srv/pairtime/held.jsonl"}},
		{name: "a data folder and a spool file open to other accounts both warn, each with its chmod",
			edit: func(f *Facts) {
				f.Modes = true
				f.Exposed = []Exposed{
					{Path: "/srv/pairtime", Fix: "chmod go-w /srv/pairtime"},
					{Path: "/srv/pairtime/inbox/spool.jsonl", Fix: "chmod 600 /srv/pairtime/inbox/spool.jsonl"},
				}
			}, check: "private files", want: Warn,
			wantHas: []string{"/srv/pairtime, /srv/pairtime/inbox/spool.jsonl are open", "Run: chmod go-w /srv/pairtime; chmod 600 /srv/pairtime/inbox/spool.jsonl"}},
		{name: "when no spooled call's kind is known, the line counts no prompts at all",
			edit: func(f *Facts) { f.Agent.IPC.Spooled = Spooled{Calls: 3, Unknown: 3} }, check: "spool", want: Warn,
			wantHas: []string{"3 hook calls of the last 7 days reached the agent only through the spool, all from a pairtime too old to record whether they were prompts"},
			wantNot: []string{"0 of"}},
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
			for _, w := range tt.wantNot {
				if strings.Contains(c.Text, w) {
					t.Errorf("%s says %q, which it mustn't", tt.check, c.Text)
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

func TestTilde(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		homes []string
		want  string
	}{
		{"a home folder reads as ~ in both its slashes' spellings", `couldn't read /home/teen/x and \home\teen\y`, []string{"/home/teen"}, `couldn't read ~/x and ~\y`},
		{"a home folder too short to name anyone is left as it is", "couldn't read /a/x", []string{"/a"}, "couldn't read /a/x"},
		{"a home folder that isn't UTF-8, as a Linux path can be, is passed over with no panic, and the text stays as it was",
			"couldn't read /home/jos\xe9/x", []string{"/home/jos\xe9"}, "couldn't read /home/jos\xe9/x"},
		{"a home that isn't UTF-8 doesn't keep the next spelling from reading as ~", "couldn't read /home/teen/x", []string{"/home/jos\xe9", "/home/teen"}, "couldn't read ~/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tilde(tt.text, tt.homes); got != tt.want {
				t.Errorf("tilde(%q, %q) = %q, want %q", tt.text, tt.homes, got, tt.want)
			}
		})
	}
}
