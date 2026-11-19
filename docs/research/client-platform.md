# Client platform: runtime, background service, watchers, packaging, storage, IPC

Status: draft in progress

Track: the client that runs on a teen's machine (R4 passive, R6 privacy, R7 every harness, R8 Windows/macOS/Linux on old hardware). Researched 2026-11-19. Every claim that isn't common knowledge carries a source; anything not confirmed is marked (unverified).

Target machine used throughout: a ~2016 Windows 10 laptop, 2 cores, 4 GB RAM, spinning hard drive, often on battery.

## Contents

1. Language and runtime
2. Running in the background on each OS
3. Watching for activity cheaply
4. Installing, updating and code signing
5. Local storage and UI
6. Hooks calling the client: cold start and IPC
7. Recommended client stack
8. Design implications
9. Open questions

## 1. Language and runtime

What the target machine imposes: about 4 GB of RAM, much of it already taken by Windows 10, a browser, an editor and an agent; a spinning disk, so every page fault and file open is slow; two cores, often a low-power Celeron or Pentium; and a battery. The background agent has to idle at roughly 0% CPU with a small resident set, and the hook binary that harnesses run on every prompt has to start in milliseconds. One codebase also has to ship to Windows (x64, possibly x86 and arm64), macOS (Intel and Apple Silicon) and Linux.

### 1.1 What comparable tools ship (measured from release assets, 2026-11-19)

Sizes are the uncompressed sizes inside each release zip, read from the zip central directory over HTTP range requests (script in `/tmp/pairtime-scratch/client/scripts/zipls.py`, deleted afterwards).

| Tool | Language | Release | Artifact | Size |
|---|---|---|---|---|
| wakatime-cli | Go | v2.28.4 (2026-11-05) | `wakatime-cli-windows-amd64.exe` | 22.9 MB (8.6 MB zipped) |
| wakatime-cli | Go | v2.28.4 | `wakatime-cli-windows-386.exe` | 21.8 MB |
| wakatime-cli | Go | v2.28.4 | `wakatime-cli-darwin-arm64` | 21.4 MB |
| ActivityWatch, classic | Python (PyInstaller, Qt) plus Rust server | v0.14.3 (2026-11-15) | `activitywatch-v0.14.3-windows-x86_64.zip` | 268 MB in 1,543 files (110 MB zipped; setup.exe 84 MB) |
| of which | Rust | | `aw-server-rust.exe` (embeds the web UI) | 34.2 MB |
| of which | Python (PyInstaller) | | `aw-qt.exe`, `aw-watcher-afk.exe`, `aw-watcher-window.exe`, `aw-watcher-input.exe` | 3.6 to 3.8 MB each, plus the shared Python and Qt runtime in the other files |
| ActivityWatch, Tauri build | Rust (Tauri) plus Python watchers | v0.14.3 | `activitywatch-tauri-v0.14.3-windows-x86_64.zip` | 133 MB in 291 files; `aw-tauri.exe` 49.0 MB |

wakatime-cli is the right comparison for pairtime's CLI: a Go binary that editor plugins spawn once per heartbeat, with no resident process. Its 22.9 MB comes from about 48,000 lines of transcript parsers plus dependencies (see hackatime-and-hack-club.md 2.2), so a focused pairtime binary should come in well under that.

### 1.2 Baseline memory per runtime

Peak RSS of a program that starts one async task, sleeps 10 s and exits. Intel i7-13700K, Debian 12, late 2024. Source: [hez2010/async-runtimes-benchmarks-2024](https://github.com/hez2010/async-runtimes-benchmarks-2024) `RESULT.md` at `a5586c58a8` ("Minimum Footprint" chart data).

| Runtime (version) | Memory, 1 task | Memory, 10k tasks |
|---|---|---|
| Rust, tokio (1.82) | 4.7 MB | 9.0 MB |
| Rust, futures | 3.0 MB | 4.8 MB |
| Go (1.23.3) | 3.6 MB | 34.3 MB |
| C#, NativeAOT (.NET 9.0.100) | 3.6 MB | 7.6 MB |
| C#, JIT (.NET 9.0.100) | 24.4 MB | 28.1 MB |
| Java, GraalVM native-image | 8.3 MB | 22.8 MB |
| Python (3.13.0) | 19.5 MB | 33.3 MB |
| Node.js (23.2.0) | 41.9 MB | 64.8 MB |

A tracker daemon runs a handful of goroutines or tasks, not thousands, so the one-task column applies: Go, Rust and NativeAOT all start at 3 to 5 MB, the JIT and interpreter runtimes at 20 to 42 MB. These are Linux numbers. Windows working sets for the same programs run higher, because system DLLs (kernel32, user32 and others) map into every process (unverified; measure on the target).

### 1.3 Idle CPU and wakeups

Idle CPU on a laptop is mostly a question of how often the process wakes, since each wakeup keeps the CPU out of deep C-states.

- **Go.** The runtime's monitor thread (`sysmon`, `src/runtime/proc.go` on master, Go 1.27.4 current) backs off from 20 µs to 10 ms sleeps, and once every P is idle it sleeps until the next timer or for `forcegcperiod / 2` (60 s, `forcegcperiod = 2 * 60 * 1e9`), whichever is sooner. An idle Go daemon whose own work is a 15 s ticker therefore wakes about once per 15 s, plus at least once every 60 s for the forced-GC check. On Windows, Go used to call `timeBeginPeriod(1)`, which raised the system timer rate to 1 kHz while the process ran and cost battery ([golang/go#8687](https://github.com/golang/go/issues/8687)). Current runtimes use `CreateWaitableTimerExW(..., CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, ...)` when available, and `osRelax` is then a no-op ("there's no need to adjust the global clock frequency"); winmm's `timeBeginPeriod` is loaded only as a fallback on Windows versions without high-resolution timers (`src/runtime/os_windows.go`, master). High-resolution waitable timers exist from Windows 10 1803 onward (unverified; Go issue #8687 discussion).
- **Rust.** No runtime threads beyond what the program creates. A tokio runtime with no pending timers parks its workers indefinitely.
- **NativeAOT.** Ships the .NET GC and a finalizer thread; the GC does not run periodically when nothing allocates (unverified).
- **Node, Bun, Python.** One event loop, no periodic wakeups of their own when idle, but 20 to 40 MB more resident memory (1.2), and Python's GIL and per-watcher processes multiply that in ActivityWatch's design (one process per watcher).

The bigger idle cost is the program's own polling, not the runtime. ActivityWatch's window watcher polls once per second by default (`poll_time = 1.0` in `aw_watcher_window/config.py`, master `76b3f6f580`, 2026-11-10). How it fetched the foreground app mattered more than the language. Its Windows watcher once used WMI for the process name, which kept WMI Provider Host at 5 to 10% CPU ([activitywatch#654](https://github.com/ActivityWatch/activitywatch/issues/654), 2021) and up to 30% on one Windows 11 machine ([#829](https://github.com/ActivityWatch/activitywatch/issues/829), 2022), until [aw-watcher-window#83](https://github.com/ActivityWatch/aw-watcher-window/pull/83) moved it to `GetWindowThreadProcessId` plus `OpenProcess` and `GetModuleFileNameEx` (the current `windows.py`). Its macOS AppleScript path kept the System Events process at about 15% CPU on a 2015 MacBook Pro, rising with the number of open windows ([#472](https://github.com/ActivityWatch/activitywatch/issues/472), 2020); the default is now a Swift helper (`strategy_macos = "swift"`).

### 1.4 Language comparison

Binary sizes are for the runtime alone, which is the floor for any single-executable build: a Node SEA copies `node.exe` and appends the app blob, `bun build --compile` embeds the Bun runtime, and `deno compile` embeds `denort`. Startup times are hello-world numbers from Linux benchmarks. Read them as ratios, since Windows adds 10 to 30 ms per process spawn on top (1.5 and section 6).

| | Go | Rust | Zig | C# NativeAOT | Node SEA / Bun / Deno | Python (PyInstaller) |
|---|---|---|---|---|---|---|
| Current version seen | 1.27.4 | stable, about 1.93 (platform page) | 0.17.1 (2026-11-02), pre-1.0 | .NET 10 (docs dated 2026-11-03) | Node 26.13.1 LTS (2026-11-10), Bun 1.4.4 (2026-10-22), Deno 2.9.8 (2026-10-21) | 3.14 |
| Runtime floor, Windows x64 | about 2 MB for hello world ("a couple of megabytes", Go FAQ); wakatime-cli is 22.9 MB with everything in it | under 1 MB is typical for a stripped CLI (unverified for our feature set) | similar to Rust | 1 to 3 MB for hello world (unverified) | `node.exe` 100.1 MB; `bun.exe` 115.3 MB; `denort.exe` 80.4 MB (measured above) | 3.6 to 3.8 MB per PyInstaller exe in ActivityWatch, plus the unpacked runtime |
| RSS at one task (Linux, 1.2) | 3.6 MB | 3.0 to 4.7 MB | not measured; no runtime, so near Rust | 3.6 MB | Node 41.9 MB (Bun, Deno not measured) | 19.5 MB |
| Hello-world startup | 0.41 ms on an i5-2400S, 4.10 ms on a Raspberry Pi 3 ([bdrung/startup-time](https://github.com/bdrung/startup-time), 2018) | 0.51 ms / 4.42 ms (same source) | about C's 0.26 ms (unverified) | low ms (unverified) | Node 41.70 ms, Deno 14.53 ms ([chocolateboy/startup-time](https://github.com/chocolateboy/startup-time) sample, undated, no versions) | Python 3 25.84 ms / 197.79 ms (bdrung); 22.46 ms (chocolateboy). PyInstaller one-file unpacks its bundle into a fresh `_MEIxxxxxx` temp folder on each start and deletes it on exit, and the docs say it "is a little slower to start up than the one-folder bundle" ([PyInstaller docs](https://pyinstaller.org/en/stable/operating-mode.html)) |
| Windows 32-bit (x86) | yes, `windows/386`, `GO386=sse2` default | yes, `i686-pc-windows-msvc` tier 1 (Windows 10+, Pentium 4) | yes | yes from .NET 9 (`win-x86`) | Node: no `win-x86` build of 26.x (404 on nodejs.org/dist); Bun and Deno: x64 and arm64 only | yes |
| Oldest Windows | Windows 10 / Server 2016 (Go 1.21+) | Windows 10 / Server 2016; Windows 7 only via tier-3 `x86_64-win7-windows-msvc` with no official builds | Windows 10 (unverified) | Windows 10 (unverified) | Windows 10 | Windows 10 for 3.13+ (unverified) |
| Oldest macOS | macOS 13 Ventura for Go 1.27; macOS 12 Monterey for Go 1.25 and 1.26 ([wiki/MinimumRequirements](https://go.dev/wiki/MinimumRequirements)) | 10.12 Sierra (`x86_64-apple-darwin`, tier 2 since Rust 1.90); 11.0 Big Sur (`aarch64-apple-darwin`, tier 1) ([platform support](https://doc.rust-lang.org/rustc/platform-support.html)) | (unverified) | macOS x64 and arm64; minimum (unverified) | (unverified) | (unverified) |
| Oldest Linux | kernel 3.2 (Go 1.24+); static with `CGO_ENABLED=0`, so no glibc floor | kernel 3.2 and glibc 2.17 for `-gnu`; static musl 1.2.5 for `-musl` | static | needs the same or newer glibc than the build host (docs: a binary built on Ubuntu 20.04 won't run on 18.04) | glibc floor of the runtime build | needs the build host's glibc or older (unverified) |
| x86 CPU baseline | `GOAMD64=v1`: "all 64-bit x86 processors" | x86-64 baseline (v1) | x86-64 baseline | x86-64-v2 or lower (unverified) | Bun ships separate `-baseline` builds for CPUs without AVX2 (same 115.3 MB on Windows) | any |
| Cross-compile from one Linux runner | all targets, pure Go (`CGO_ENABLED=0`), seconds | Windows via `-gnu` or `cargo-xwin`; macOS needs the Apple SDK, so in practice a macOS runner | all targets including macOS for code that doesn't link Apple frameworks | no: "Native AOT does not support cross-OS compilation" (Microsoft Learn, cross-compile page); build each OS on its own runner | each runtime publishes all targets; SEA injection works cross-platform (unverified for Bun's `--target`) | no; build each OS on its own runner |
| OS API access without C | `golang.org/x/sys/windows` for Win32; macOS frameworks need cgo or `purego` (grit uses `purego` for `CGEventSourceSecondsSinceLastEventType`, prior-art-and-measurement.md 1.14) | `windows-sys`/`windows` crates; `objc2` family for Apple frameworks | manual bindings | P/Invoke, but no built-in COM on NativeAOT | N-API addons or FFI (Bun `bun:ffi`); native addons rule out single-file builds unless embedded | `pywin32`, `pyobjc` (heavy) |
| Antivirus false positives | the Go FAQ: "a common occurrence, especially on Windows machines, and is almost always a false positive" | rarer; no comparable FAQ entry (unverified) | (unverified) | (unverified) | rare for the runtime itself; the appended blob is opaque (unverified) | common for PyInstaller bootloaders (unverified) |
| Maturity and contributor pool | stable, large; wakatime-cli, grit, Tailscale, Syncthing are Go | stable, large; ActivityWatch server, Tauri | language and standard library still change between 0.x releases | stable; smaller open-source desktop-agent ecosystem | stable; largest contributor pool, and harness hooks often already run Node | stable; ActivityWatch watchers |

Notes on rows that decide the choice:

- **Footprint rules out the JS runtimes and Python for the resident agent.** A 100 MB executable and a 40 MB baseline RSS per process is 10 times the native options, on a machine where 4 GB is shared with a browser and an agent. Bun and Deno also have no 32-bit Windows build, and Node 26 has none either.
- **Hook cold start rules them out for the hook path.** A Node process costs about 42 ms before it does anything on a fast Linux box; Windows adds its own process-creation cost and Defender's scan of a 100 MB image on first run (section 6).
- **Go, Rust and NativeAOT all meet the footprint bar.** The tiebreakers are cross-compilation, OS API access, and the people who will maintain it. NativeAOT can't cross-compile between operating systems, so it needs three native build jobs and a Windows-hosted toolchain with the Visual Studio C++ workload. Zig is attractive for size but changes incompatibly between releases and lacks mature libraries for SQLite, tray icons and macOS frameworks.

### 1.5 Cross-compilation and CI cost

GitHub Actions is "free for standard GitHub-hosted runners in public repositories" ([billing docs](https://docs.github.com/en/actions/concepts/billing-and-usage), read 2026-11-19), so pairtime pays nothing for CI on any OS. The standard runners available to public repos ([runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners), read 2026-11-19):

| OS and architecture | Labels | CPU / RAM |
|---|---|---|
| Linux x64 | `ubuntu-latest` (24.04), `ubuntu-22.04`, `ubuntu-26.04`; `ubuntu-slim` (1 CPU container, 15-minute job limit) | 4 / 16 GB |
| Linux arm64 | `ubuntu-24.04-arm`, `ubuntu-22.04-arm`, `ubuntu-26.04-arm` | 4 / 16 GB |
| Windows x64 | `windows-latest` (2025), `windows-2025-vs2026`, `windows-2022` | 4 / 16 GB |
| Windows arm64 | `windows-11-arm`, `windows-11-vs2026-arm` | 4 / 16 GB |
| macOS Intel | `macos-15-intel`, `macos-26-intel` | 4 / 14 GB |
| macOS arm64 (M1) | `macos-latest` (26), `macos-15`, `macos-26` | 3 / 7 GB |

The Intel macOS runners are ending. GitHub introduced `macos-15-intel` on 2025-09-19 as "the last available x86_64 image from Actions", available "until August 2027", after which "the x86_64 architecture will not be supported on GitHub Actions" ([runner-images#13045](https://github.com/actions/runner-images/issues/13045)). A `macos-26-intel` label has appeared since (runner page above), but the end date stands. After August 2027, Intel Mac builds must be cross-compiled from Apple Silicon, which Go and Rust both do natively (`GOARCH=amd64`; `--target x86_64-apple-darwin`), and tested on real hardware or not at all.

What each language costs in CI:

- **Go, pure.** One `ubuntu-latest` job builds every target (`GOOS`/`GOARCH` matrix) in well under a minute for a project of this size. Test jobs still need each OS to exercise platform code. GoReleaser (section 4) does builds, archives, checksums, signing and package manifests from one config.
- **Go with cgo** (for example `mattn/go-sqlite3` or a cgo tray library on macOS). Needs a C cross-toolchain per target, or a native runner per OS. This is the main reason to stay cgo-free.
- **Rust.** Native runners per OS are the practical route (macOS can't be linked from Linux without the Apple SDK). Release builds take minutes rather than seconds; free on public repos either way.
- **NativeAOT.** Three native runners, by design.

**Old Macs and the Go toolchain.** Macs that top out at macOS 12 Monterey (roughly models from 2015 to 2016; Ventura needs 2017 or later, unverified) can't run binaries from Go 1.27 or later. Go 1.26 still supports them and gets security fixes until February 2027 ([golang/go#75836](https://github.com/golang/go/issues/75836), which also cites about 0.8% of opt-in Go telemetry hosts on macOS 12 in October 2025). If those Macs matter, ship the macOS build from Go 1.26 until then, or accept dropping them. Rust targets macOS 10.12 and later.

(sections 1.6 onward in progress)

## 2. Running in the background on each OS

The agent has to run in the user's own interactive session, because the signals it needs belong to that session. On Windows, `GetLastInputInfo` "provides session-specific user input information for only the session that invoked the function" ([Microsoft Learn](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getlastinputinfo), Remarks), and a service runs in session 0. On macOS, idle time and frontmost-app notifications need the user's GUI (Aqua) session. On Linux, X11 and Wayland idle protocols need `DISPLAY` or `WAYLAND_DISPLAY`. That rules out system services and daemons on all three OSes, and all the per-user mechanisms below need no admin rights.

### 2.1 Mechanisms compared

| OS | Mechanism | Admin? | Starts | Survives reboot / OS update | Restart on crash | User can see and disable it | Notes |
|---|---|---|---|---|---|---|---|
| Windows | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` value | no | at each logon of that user | yes / yes | no | Task Manager, Startup apps tab; Settings, Apps, Startup | Command line "no longer than 260 characters"; order among entries "indeterminate"; "the system may choose to delay the execution of programs in the Run key and in the Startup group"; ignored in Safe Mode ([Run and RunOnce keys](https://learn.microsoft.com/en-us/windows/win32/setupapi/run-and-runonce-registry-keys), 2024-07-19) |
| Windows | Shortcut in `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup` (`shell:startup`) | no | at logon | yes / yes | no | same Startup apps list | Same delay rule as the Run key. Easier for a user to find and delete by hand; a `.lnk` needs a COM call (`IShellLink`) or a PowerShell one-liner to create |
| Windows | Task Scheduler task with a logon trigger | `ONLOGON` fires for "any user"; "Only Administrators can schedule tasks, regardless of the value of the /ru parameter" ([schtasks create](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/schtasks-create)). Per-user logon triggers from a standard account: (unverified) | at logon, optional `/delay` | yes / yes | yes, via task restart settings | Task Scheduler library (few users look there) | Defaults are wrong for a laptop agent: `DisallowStartIfOnBatteries` "default setting ... is True" ([schema](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskschedulerschema-disallowstartifonbatteries-settingstype-element)); `StopIfGoingOnBatteries` and a 72-hour `ExecutionTimeLimit` are also default on (unverified). A task XML must set all three |
| Windows | Windows service | yes (install) | at boot, session 0 | yes | yes (SCM recovery) | services.msc | Can't read the user's idle time or foreground window (session 0). Useful only as a privileged helper, which pairtime doesn't need |
| macOS | `~/Library/LaunchAgents/<label>.plist`, loaded with `launchctl bootstrap gui/$(id -u) <plist>` | no | at login (`RunAtLoad`) | yes / yes | `KeepAlive` with `Crashed` or `SuccessfulExit` keys; respawns are throttled to once per 10 s by default (`ThrottleInterval`) | System Settings, General, Login Items & Extensions; macOS 13+ registers items with the Background Task Management framework, and `sfltool dumpbtm` lists them ([Apple deployment guide](https://support.apple.com/guide/deployment/manage-login-items-background-tasks-mac-depdca572563/web)) | Per-user agents "provided by the user" live in `~/Library/LaunchAgents` ([launchd.plist(5)](https://keith.github.io/xcode-man-pages/launchd.plist.5.html)). An unset `ProcessType` means "light resource limits ... throttling its CPU usage and I/O bandwidth"; `Background` is stricter; `Interactive` has none. `LowPriorityIO` is available. A user-visible "background item added" notice appears on install (unverified wording) |
| macOS | `SMAppService` (macOS 13+) agent inside an app bundle | no | at login | yes | yes (launchd) | Login Items list, with the app's name and icon | "uses the SMAppService object to control helper executables that live inside an app's main bundle" (same guide). Needs a signed `.app` bundle, so only worth it if pairtime ships a menu-bar app. macOS 26+: "if background tasks are started by an app and remain active after a user quits the app, the user is prompted" |
| Linux | systemd user unit in `~/.config/systemd/user/pairtime.service`, `systemctl --user enable --now` | no | `WantedBy=default.target` starts it with the user manager (first login); X11/Wayland-only services should use `PartOf=graphical-session.target` so they stop with the session ([systemd.special(7)](https://www.freedesktop.org/software/systemd/man/latest/systemd.special.html), systemd 262) | yes / yes | `Restart=on-failure` | `systemctl --user status`; invisible to non-technical users | The service doesn't inherit `DISPLAY`/`WAYLAND_DISPLAY` unless the desktop imports them into the user manager (GNOME and KDE do; minimal window managers may not) (unverified per desktop). Without lingering, the user manager starts at first login |
| Linux | XDG autostart `~/.config/autostart/pairtime.desktop` | no | "automatically launched during startup of the user's desktop environment after the user has logged in" ([autostart spec](http://specifications.freedesktop.org/autostart/latest/) 0.5) | yes / yes | no | Desktop "Startup Applications" settings; `Hidden=true` disables | Works on non-systemd distributions and inherits the session environment. On systemd desktops, `systemd-xdg-autostart-generator` turns these entries into units under `xdg-desktop-autostart.target` (since v246). `TryExec` skips the entry if the binary is gone |

Recommendation for pairtime: the HKCU Run key on Windows (one registry write, no COM, no admin, shows up where users expect startup apps), a LaunchAgent plist on macOS, and on Linux an XDG autostart entry by default, with a systemd user unit as an opt-in for people who want restart-on-crash. Each is written by `pairtime install` and removed by `pairtime uninstall`. The agent also needs a single-instance lock (a named mutex on Windows, an `flock` on the socket's lock file elsewhere), because Run-key starts, a user double-clicking the binary, and a hook starting the agent on demand will all race.

### 2.2 Sleep, standby and battery

| Situation | What happens to the agent | What pairtime must do |
|---|---|---|
| Windows S3 sleep or hibernate (most 2016 laptops) | All processes freeze; on resume, timers that were due fire at once | Treat any gap between consecutive samples longer than twice the sample interval as "machine asleep", never as idle-but-present. Use wall-clock timestamps for records, and detect suspend explicitly (below) |
| Windows Modern Standby (connected standby, newer laptops) | The Desktop Activity Moderator puts processes "created in an interactive session" in a job object "subject to suspension": "all their threads suspended ... app state (process memory) is maintained". It's like S3 to the app, with "inconsistencies in uptime / runtime vs. wall clock time" ([DAM, Microsoft Learn](https://learn.microsoft.com/en-us/windows/win32/w8cookbook/desktop-activity-moderator)) | Register with `PowerRegisterSuspendResumeNotification(DEVICE_NOTIFY_CALLBACK, ...)` (no window needed) or `RegisterSuspendResumeNotification` with an HWND, and handle `WM_POWERBROADCAST` `PBT_APMSUSPEND` / `PBT_APMRESUME` (same page). Flush the event store on suspend |
| Windows on battery | The Run-key agent keeps running. Windows schedules windowless background processes by heuristics; a process can opt into EcoQoS with `SetProcessInformation(ProcessPowerThrottling)` and `PROCESS_POWER_THROTTLING_EXECUTION_SPEED`, which "always selects most efficient CPU frequency and schedules to efficient cores" (Eco level: Windows 11; Low/Utility levels from 1709 and 22H2) ([Quality of Service](https://learn.microsoft.com/en-us/windows/win32/procthread/quality-of-service), 2025-07-14) | Opt into EcoQoS at startup. It costs nothing on a 2-core laptop with no efficiency cores and helps on newer ones. Avoid Task Scheduler, whose defaults stop tasks on battery (2.1) |
| macOS sleep | Processes freeze. A process without AppKit gets sleep and wake through IOKit: `IORegisterForSystemPower`, messages `kIOMessageSystemWillSleep` (must acknowledge with `IOAllowPowerChange`, or "sleep will be delayed by 30 seconds") and `kIOMessageSystemHasPoweredOn`. AppKit apps can observe `NSWorkspaceWillSleepNotification` / `NSWorkspaceDidWakeNotification` on NSWorkspace's own notification center ([QA1340](https://developer.apple.com/library/archive/qa/qa1340/_index.html), 2014) | Use the IOKit route from the agent, acknowledge promptly, flush on will-sleep |
| macOS on battery, App Nap | App Nap targets apps with UI that are hidden; a launchd agent's throttling comes from `ProcessType` (2.1) | Leave `ProcessType` unset or `Background`; pairtime's work is tiny |
| Linux sleep | systemd-logind emits `PrepareForSleep(true)` before suspend and `PrepareForSleep(false)` after resume on `org.freedesktop.login1.Manager`; a client can take a `delay` inhibitor lock to flush first (unverified this session; logind D-Bus docs) | Subscribe over D-Bus where logind exists; otherwise rely on the sample-gap rule |
| Clock behavior across sleep | Monotonic clocks differ by OS: Linux `CLOCK_MONOTONIC` stops during suspend; Windows interrupt time keeps counting (unverified; check `QueryInterruptTime` vs `QueryUnbiasedInterruptTime` docs) | Never compute an "active duration" as a monotonic difference across a possible suspend. Store wall-clock UTC per sample, and split intervals at suspend and resume events |

The sample-gap rule is the safety net on every OS: if the agent samples idle state every 15 s and two samples are 10 minutes apart, the machine was asleep or the agent was frozen, and that span counts as nothing.

## 3. Watching for activity cheaply

The agent's inputs, in rough order of cost: idle-time samples (a syscall), foreground-app changes (an event or a cheap query), harness hook events (pushed to us, section 6), harness transcript and log files (file notifications plus reads), and the user's repos (the expensive one). The API names per OS are in ai-harness-signals.md 13.4; this section is about what each costs and how to keep it near zero.

### 3.1 File-change notification APIs

| | Windows `ReadDirectoryChangesW` | macOS FSEvents | macOS kqueue | Linux inotify |
|---|---|---|---|---|
| Recursive | yes, one handle per tree (`bWatchSubtree = TRUE`) | yes, a stream covers "a large tree of files" | no, one fd per watched file or directory | no: "to monitor subdirectories under a directory, additional watches must be created" ([inotify(7)](https://man7.org/linux/man-pages/man7/inotify.7.html), man-pages 6.19) |
| Granularity | per file, with action codes | per directory by default ("tells you *only* that something in the directory has changed"); per file with `kFSEventStreamCreateFlagFileEvents` (10.7+) ([FSEvents guide](https://developer.apple.com/library/archive/documentation/Darwin/Conceptual/FSEvents_ProgGuide/TechnologyOverview/TechnologyOverview.html), 2012) | per fd | per file, with event masks |
| Overflow | the kernel buffer is fixed when first allocated; on overflow the call "will still return true, but the entire contents of the buffer are discarded" and `lpBytesReturned` is 0, or it fails with `ERROR_NOTIFY_ENUM_DIR`; either way "compute the changes by enumerating the directory or subtree" ([Microsoft Learn](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-readdirectorychangesw)) | coalesced; history is kept in a per-volume database | n/a | "Events in excess of this limit are dropped, but an IN_Q_OVERFLOW event is always generated" (`max_queued_events`) |
| Limits | buffer over 64 KB fails with `ERROR_INVALID_PARAMETER` on network shares | none that matter | `kern.maxfilesperproc` | `max_user_watches` defaults to 1% of low memory divided by `INOTIFY_WATCH_COST` (`sizeof(struct inotify_inode_mark) + 2 * sizeof(struct inode)`), clamped to [8192, 1048576] (`fs/notify/inotify/inotify_user.c`, Linux master); each watch also pins the watched inode. The limit is per real user ID, so it is shared with VS Code's own watcher |
| Replay after downtime | no (use the NTFS change journal, which needs admin, or rescan) | yes: the database records changes "even while your application was not running" | no | no |
| Gotchas | `FILE_NOTIFY_CHANGE_SIZE` and `_LAST_WRITE`: "detects a change ... only when the file is written to the disk. For operating systems that use extensive caching, detection occurs only when the cache is sufficiently flushed", so appends to a file another process holds open can arrive late. Directory handle needs `FILE_LIST_DIRECTORY` and `FILE_FLAG_BACKUP_SEMANTICS` | Spotlight and antivirus generate attribute noise (fsnotify README) | fd exhaustion on big trees | no events for NFS/SMB/FUSE, `/proc`, `/sys`; none for `mmap` writes |

Library support, checked 2026-11-19:

- **Go, [fsnotify](https://github.com/fsnotify/fsnotify) v1.10.1 (2026-05-04, BSD-3-Clause).** Backends: inotify, kqueue (BSD and macOS), `ReadDirectoryChangesW`, FEN. FSEvents is listed as "Needs support in x/sys/unix". "Are subdirectories watched? No" (recursive watching is still issue #18). On macOS, "kqueue requires opening a file descriptor for every file that's being watched". No polling fallback for network filesystems (issue #9). Fine for pairtime's narrow watch set (3.2), wrong for watching a whole repo on macOS.
- **Rust, [notify](https://github.com/notify-rs/notify) 9.0.0-rc.9 (2026-11-15; 8.x stable).** inotify on Linux, FSEvents by default on macOS (kqueue optional), `ReadDirectoryChangesW` on Windows, and a polling backend everywhere. Comes with debouncers.

### 3.2 What pairtime should watch

**Harness transcript and log directories, not repos.** These trees are small: one directory per project for Claude Code, date-nested folders for Codex, one folder per session for Copilot CLI (paths in ai-harness-signals.md). The tailer should:

1. Watch each harness root non-recursively plus only the subdirectories that are active (today's Codex date folder; Copilot session folders touched in the last day). That keeps the inotify count in the tens and the macOS kqueue fd count in the tens.
2. Keep a cursor per file: (volume and file ID or inode, size, byte offset, hash of the last complete line). On a change event, `stat` the file, read from the offset to EOF, and buffer any partial last line until the next newline. If the file shrank or its ID changed, it was truncated or replaced, so restart from 0 and dedupe by line hash.
3. Back up the notifications with a slow poll of only the files that are "hot" (written in the last 10 minutes): one `stat` every 5 to 10 s while a session is active, backing off to 60 s, then stopping. This covers Windows' delayed size notifications, network homes, WSL paths (`\\wsl.localhost\...`, where change notification is unreliable; unverified), and editors or harnesses that write through `mmap`.
4. On startup, do one catch-up scan (lookback) from the saved cursors, which is how wakatime-cli reads transcripts too (hackatime-and-hack-club.md 2.2). On macOS, FSEvents can replay changes since the last event ID; elsewhere a scan of the few harness roots by modification time is cheap.
5. For SQLite-backed harness state (Cursor's `state.vscdb`, which can be gigabytes per ai-harness-signals.md), watch the `-wal` file, open the database read-only, and query incrementally by rowid, rate-limited to once per 30 to 60 s. On a spinning disk this is the most expensive read in the whole client.

**Repos: watch `.git`, not the working tree.** A non-recursive watch on `<repo>/.git` sees `HEAD`, `index` and `ORIG_HEAD` change, plus `.git/logs/HEAD` appends, which covers commits, checkouts, resets, rebases and staging for every repo pairtime knows about (worktrees and submodules point `.git` to elsewhere, so resolve with `git rev-parse --git-dir` once). Working-tree edits come from the editor extension and from harness hooks (`PostToolUse` paths), which already say who made the edit. A recursive working-tree watch costs the most on exactly the hardware we care about: a JavaScript project's `node_modules` can hold tens of thousands of directories, each an inotify watch pinning an inode on Linux, and every `npm install` floods the queue. If pairtime ever needs it (say, to see hand edits made in an editor without an extension), use it only on Windows and macOS, where one handle covers the tree, and never on Linux by default.

### 3.3 Idle, input and focus: cost per sample

| Signal | Call | Cost | Sample rate |
|---|---|---|---|
| Idle time, Windows | `GetLastInputInfo` + `GetTickCount` | one user32 call; no permission. The tick "is not guaranteed to be incremental" (it can go backwards, for example after `SendInput`), so clamp negative idle to 0 | every 5 to 15 s. ActivityWatch's AFK watcher polls every 5 s with a 180 s timeout (`aw_watcher_afk/config.py`: `timeout = 180`, `poll_time = 5`); grit samples every 15 s |
| Idle time, macOS | `CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState, kCGAnyInputEventType)` | one CoreGraphics call; no permission | same |
| Idle time, Linux | X11 `XScreenSaverQueryInfo`; GNOME `org.gnome.Mutter.IdleMonitor` (event-driven watches); `ext-idle-notify-v1` on wlroots, KDE and others | a D-Bus or X round trip | same, or event-driven where the watch API exists |
| Foreground app, Windows | `SetWinEventHook(EVENT_SYSTEM_FOREGROUND, ..., WINEVENT_OUTOFCONTEXT)` on a thread with a message loop, then `GetWindowThreadProcessId` → `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` → `QueryFullProcessImageNameW` | event-driven: zero cost between focus changes. Never WMI: ActivityWatch's WMI lookups kept WMI Provider Host at 5 to 30% CPU (1.3). `PROCESS_QUERY_LIMITED_INFORMATION` also works on elevated processes, where `PROCESS_QUERY_INFORMATION` (which aw-watcher-window uses) does not (unverified) | on event; window titles only if the user opts in |
| Foreground app, macOS | `NSWorkspace` `didActivateApplicationNotification`, or polling `NSWorkspace.frontmostApplication` | no permission for the app name; titles need Accessibility or Screen Recording. Never AppleScript or System Events polling (15% CPU in [#472](https://github.com/ActivityWatch/activitywatch/issues/472)) | on event, or a 2 to 5 s poll if the run loop is awkward from Go |
| Foreground app, Linux | X11: `PropertyNotify` on the root window's `_NET_ACTIVE_WINDOW`; Wayland: no standard protocol | event-driven on X11; on GNOME Wayland it needs a Shell extension | on event |
| Keystroke and click counts | Windows Raw Input (`RIDEV_INPUTSINK`) or low-level hooks; macOS `CGEventTap` (Input Monitoring permission) | hooks add latency to every keystroke system-wide if the hook thread stalls; low-level hooks are "silently removed" after a timeout on Windows 7+ (13.4). A program that installs a global keyboard hook and uploads data looks like a keylogger to antivirus heuristics and to privacy-minded users | leave out of v1; idle time plus harness and editor events carry the presence signal. If added later, count keys without recording them, opt-in only |

On a 2-core laptop, the whole sampling loop (idle sample every 15 s, foreground on event, hot-file `stat` every 10 s during a session) should wake the CPU about 10 times a minute and use well under 0.1% CPU (estimate; measure on target hardware before quoting it).

## 4. Installing, updating and code signing

### 4.1 Precedent: how Claude Code ships (read 2026-11-19)

Claude Code is the harness most of pairtime's users will already have installed, and its install story is the bar ([setup docs](https://code.claude.com/docs/en/setup.md)):

- **Native installer, no admin:** `curl -fsSL https://claude.ai/install.sh | bash` (macOS, Linux, WSL); `irm https://claude.ai/install.ps1 | iex` (PowerShell); `curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd` (CMD). Installs `~/.local/bin/claude` as a symlink into `~/.local/share/claude/versions/` (Windows: `%USERPROFILE%\.local\bin\claude.exe`). Native installs auto-update in the background, keep the two newest versions, and take effect on next start.
- **Package managers:** Homebrew casks `claude-code` (stable) and `claude-code@latest`; `winget install Anthropic.ClaudeCode`; signed apt, dnf and apk repositories. None of these auto-update unless `CLAUDE_CODE_PACKAGE_MANAGER_AUTO_UPDATE=1`, and "On WinGet the upgrade may fail while Claude Code is running because Windows locks the executable".
- **npm:** `npm install -g @anthropic-ai/claude-code@latest` (Node 22+) pulls "a per-platform optional dependency such as `@anthropic-ai/claude-code-darwin-arm64`, and a postinstall step links it into place. The installed `claude` binary does not itself invoke Node." Platforms: `darwin-arm64`, `darwin-x64`, `linux-x64`, `linux-arm64`, `linux-x64-musl`, `linux-arm64-musl`, `win32-x64`, `win32-arm64`.
- **Integrity:** each release publishes a GPG-signed `manifest.json` with SHA-256 per platform; macOS binaries are signed by "Anthropic PBC" and notarized; Windows binaries are Authenticode-signed; Linux binaries rely on the signed manifest or repository keys.
- **Requirements:** "macOS 13.0+", "Windows 10 1809+", "4 GB+ RAM, x64 or ARM64 processor". No 32-bit Windows, no macOS 12.

That last line bounds pairtime's own requirements for its Claude Code users: a machine that can't run the harness doesn't need pairtime's harness integration. 32-bit Windows and macOS 12 matter only for other harnesses and for editor-only tracking (unverified which popular harnesses still support them).

### 4.2 Channels compared

| Channel | Admin? | Reaches | Updates | Signing needed to avoid warnings | Effort for pairtime |
|---|---|---|---|---|---|
| `curl ... \| sh` and `irm ... \| iex` script | no | everyone with a terminal; the same command works in docs, the website and Slack | pairtime's own self-update | macOS: files fetched by `curl` aren't quarantined (quarantine is opted into per app via `LSFileQuarantineEnabled`, which browsers and mail clients set; unverified on the exact default), so Gatekeeper doesn't block an unsigned CLI. Windows: SmartScreen keys off the Mark of the Web, which browser downloads carry; PowerShell's `irm` doesn't add it (unverified). Smart App Control checks every executable regardless (below) | low; host the scripts on GitHub Pages or the pairtime domain |
| GitHub Releases zip, downloaded in a browser | no | people who don't use terminals | self-update | Windows: unsigned gets "Windows protected your PC" and needs "Run anyway"; macOS: unsigned or un-notarized needs System Settings > Privacy & Security > Open Anyway (below) | lowest |
| winget (`microsoft/winget-pkgs` PR) | no for a portable package | Windows 10 1809+ and 11 with App Installer (unverified for LTSC and school-managed images) | `winget upgrade`; not automatic | none required by policy, but every submission is scanned by "several antivirus programs", and code flagged as potentially unwanted is rejected with `Binary-Validation-Error`; it must install and uninstall "for both administrators and non-administrators", silently, from "the publisher's release location" ([submission docs](https://learn.microsoft.com/en-us/windows/package-manager/package/repository)) | GoReleaser generates the manifest and can open the PR; first submission gets manual review |
| Scoop bucket | no (installs under `~\scoop`) | developer-leaning Windows users | `scoop update` | none | trivial: pairtime's own bucket repo, GoReleaser generates it |
| Homebrew tap (`brew install pairtime/tap/pairtime`) | no | most macOS developers, Linuxbrew | `brew upgrade` | a tap formula installing a CLI binary isn't quarantined (unverified). homebrew-cask disables casks "that fail Gatekeeper checks in September 2026", and `--no-quarantine` is deprecated ([Homebrew 5.0.0](https://brew.sh/2025/11/12/homebrew-5.0.0/); [brew#20755](https://github.com/Homebrew/brew/issues/20755)), so a menu-bar `.app` cask needs signing and notarization | low with GoReleaser; homebrew-core needs notability first |
| npm (`npm i -g pairtime`, `npx pairtime`) | no | anyone with Node; Karim's examples use `npx` | npm; or self-update | same as the script channel | moderate: per-platform packages with `os`/`cpu` fields, as Claude Code does. `npx` is fine for a one-time `npx pairtime install`; it is not acceptable inside a hook, because every hook call would start Node and npm before pairtime runs (section 6) |
| VS Code Marketplace and Open VSX extension | no | VS Code, Cursor, Windsurf, VSCodium users, who are most of the target audience | marketplace auto-update | the bundled binary is unpacked by the editor, not downloaded by a browser (SmartScreen exposure unverified) | moderate. Platform-specific VSIX packages: `vsce package --target win32-x64` and so on for `win32-x64`, `win32-arm64`, `linux-x64`, `linux-arm64`, `linux-armhf`, `alpine-x64`, `alpine-arm64`, `darwin-x64`, `darwin-arm64`, `web`; VS Code 1.61+ picks the matching package ([publishing docs](https://code.visualstudio.com/api/working-with-extensions/publishing-extension)). Open VSX support for targets (unverified) |
| Microsoft Store (MSIX) | no | Windows 10/11 including S mode | Store | none: "Microsoft re-signs the package after certification"; Store apps "are never subject to SmartScreen download warnings"; developer registration is free ([code signing options](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options), 2026-10-29) | high: MSIX packaging and Store review, and MSIX file-system virtualization may get in the way of reading other apps' files under `%USERPROFILE%` (unverified). The only route into Windows S mode |

How WakaTime does it: the VS Code plugin downloads wakatime-cli itself from `https://github.com/wakatime/wakatime-cli/releases/latest/download/wakatime-cli-<os>-<arch>.zip` into its resources folder, checks the GitHub API for a newer tag at most every 4 hours (`cli_version_last_accessed`), and pins an old tag for macOS kernels below 17 (`src/dependencies.ts`, vscode-wakatime, pushed 2026-07-15). It verifies nothing beyond TLS, and even has a `no_ssl_verify` setting. pairtime should copy the "the extension brings the binary" idea but verify a signature.

### 4.3 Windows: SmartScreen, Smart App Control, and what signing costs

From Microsoft's [SmartScreen reputation](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation) page (2026-05-04):

- SmartScreen weighs "Publisher reputation" and "File hash reputation". Unsigned: "Windows protected your PC", and the user "must choose 'Run anyway'". Enterprise policy can remove that option.
- "When a file is not signed, SmartScreen reputation must build for each new version of your files, starting with zero reputation." Signed files can inherit reputation from the certificate; reputation "can take several weeks and hundreds of clean installs from a wide audience".
- "EV certificates no longer bypass SmartScreen." The bypass "was removed in 2024".
- "On Windows 11 devices, the Smart App Control feature may supersede SmartScreen ... Smart App Control will block execution of unsigned files unless the file has a positive reputation. Smart App Control signature checks apply to all executable files, not just those downloaded from the Internet." So on a Windows 11 machine with Smart App Control on, an unsigned pairtime binary may not run however it was installed.

Signing options for a Windows binary distributed outside the Store ([code signing options](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options), 2026-10-29):

| Option | Cost | Who can get it | Notes |
|---|---|---|---|
| Azure Artifact Signing (formerly Trusted Signing) | about $9.99/month (Basic, 5,000 signatures) | "Organizations: USA, Canada, EU, UK. Individuals: USA and Canada only" | no hardware token; works from GitHub Actions; reputation still builds over time. Billing starts at account creation and isn't prorated ([pricing FAQ](https://azure.microsoft.com/en-us/pricing/details/artifact-signing/)) |
| OV certificate from a CA | $150 to $300/year | worldwide | private key must be on an HSM or token (CA/Browser Forum, June 2023), so CI signing needs a cloud HSM |
| EV certificate | $400+/year | worldwide | no SmartScreen advantage since 2024 |
| SignPath Foundation | free for open source | OSS projects that pass review ([signpath.org](https://signpath.org/); eligibility terms not on the landing page) | Microsoft's own page lists it as the open-source route; the certificate is the Foundation's, which "vouch[es] for" binaries "built from your open source repository"; keys stay in SignPath's HSM, signing runs from CI |
| Microsoft Store MSIX | free | anyone | covered above |

Hack Club is a US nonprofit, so as an organization it qualifies for Artifact Signing (open question 9). SignPath is the free route if signing under the project's own name doesn't matter.

### 4.4 macOS: Gatekeeper and notarization

- Since macOS 15 Sequoia, "users will no longer be able to Control-click to override Gatekeeper when opening software that isn't signed correctly or notarized. They'll need to visit System Settings > Privacy & Security" ([Apple Developer News](https://developer.apple.com/news/?id=saqachfa), 2024-08-06). Apple's current user steps: try to open it, then System Settings > Privacy & Security > Open Anyway, confirm, and the app "is now saved as an exception" ([support 102445](https://support.apple.com/en-us/102445), 2026-05-27).
- Apple Silicon won't run native arm64 code without a valid signature (ad-hoc is enough); Homebrew's issue cites Apple's Rosetta page for this ([brew#20755](https://github.com/Homebrew/brew/issues/20755)). Go's and Rust's macOS linkers ad-hoc sign arm64 binaries automatically (unverified for Go cross-links from Linux; grit and wakatime-cli ship darwin-arm64 builds made this way).
- Developer ID signing and notarization need an Apple Developer Program membership (US$99/year; fee waivers exist for some nonprofits, unverified for Developer ID-only use). Both can run on a Linux CI runner: GoReleaser's `notarize.macos` uses [anchore/quill](https://github.com/anchore/quill) ("works on any operating system"; inputs: `.p12` certificate and password, App Store Connect `issuer_id`, `key_id` and `.p8` key; binaries only, not app bundles) ([GoReleaser docs](https://goreleaser.com/customization/notarize/)), and [rcodesign](https://gregoryszorc.com/docs/apple-codesign/main/) can "sign, notarize, and release Apple software from non-Apple operating systems", including stapling for bundles, pkgs and DMGs.
- In practice: a CLI installed by `curl | sh`, a Homebrew formula or an editor extension avoids Gatekeeper even unsigned (unverified for the extension case); a downloadable `.app` or a Homebrew cask needs Developer ID plus notarization.

### 4.5 Linux

No signing requirement. Ship static binaries in tarballs, `.deb`/`.rpm`/`.apk` via nFPM (GoReleaser), and a signed checksum file. Avoid Snap and Flatpak for the agent: their sandboxes are designed to block reading `~/.claude`, `~/.codex` and other apps' files, which is the agent's whole job (unverified per-sandbox details; both allow broad `home` access only with extra permissions).

### 4.6 Self-update

| Step | Detail |
|---|---|
| Check | at most once a day, plus on agent start; fetch a small signed manifest (version, per-platform URL, SHA-256, minimum supported version) from GitHub Releases |
| Verify | the manifest signature with an Ed25519 public key compiled into the binary (minisign format or plain Ed25519; GPG as Claude Code does works too but needs gpg tooling), then the SHA-256 of the download. Platform signatures (Authenticode, notarization) are checked by the OS, not by the updater |
| Swap | macOS and Linux: write `pairtime.new` beside the binary, `rename(2)` over the old one (atomic), restart the agent. Windows: a running `.exe` can't be overwritten or deleted, but it can be renamed, so rename `pairtime.exe` to `pairtime.old.exe`, move the new file in, restart, and delete the `.old` on the next start (unverified that Defender or indexing never holds the handle) |
| Respect package managers | if the binary lives under a winget, Scoop or Homebrew prefix, don't self-update; tell the user the upgrade command (Claude Code's rule, 4.1) |
| Hooks during the swap | hooks call a stable path (`~/.local/bin/pairtime` or `%LOCALAPPDATA%\pairtime\pairtime.exe`), so a version directory plus launcher symlink (Claude Code's layout) avoids a window where the path is missing. On Windows, where symlinks need Developer Mode, keep a fixed path and do the rename dance |

## 5. Local storage and UI

### 5.1 The local event store

pairtime writes little: an idle sample every 15 s, a few hook events per prompt, transcript-derived turn records, focus changes. That's on the order of 10,000 small rows per active day (estimate). The store's real jobs are durability across crashes and sleep, cheap range queries for the local dashboard, and an append-only shape that later supports the audit chain (R10).

Recommended layout:

| File | Format | Why |
|---|---|---|
| `spool/<yyyy-mm-dd>.jsonl` | append-only JSON Lines, one record per line, written by hooks (section 6) and by the agent | the hook process can append a line with a single `O_APPEND` write and exit without talking to anything; crash-safe; human-readable for the user and a reviewer; trivial to hash-chain |
| `pairtime.db` | SQLite in WAL mode | the agent's index: sessions, minutes, projects, derived credit, upload state. Rebuildable from the spool plus harness logs, so `synchronous=NORMAL` is acceptable |
| `config.toml` | TOML | user settings, project mappings, privacy rules |

SQLite settings: `journal_mode=WAL`, `synchronous=NORMAL`. SQLite's docs: "WAL mode is safe from corruption with synchronous=NORMAL", "A transaction committed in WAL mode with synchronous=NORMAL might roll back following a power loss or system crash", and "Transactions are durable across application crashes regardless of the synchronous setting or journal mode"; in WAL+NORMAL "no sync operations occur during most transactions". Auto-checkpoint defaults to 1,000 pages ([PRAGMA docs](https://www.sqlite.org/pragma.html)). On a spinning disk, fsync is the expensive part (tens of milliseconds each, estimate), so batch writes: the agent commits once every 15 to 60 s or on suspend, not per event. The spool is where per-event durability lives.

Go drivers (benchmark on an i7-1165G7, NVMe, Go 1.26.0, run 2026-03-23, [cvilsmeier/go-sqlite-bench](https://github.com/cvilsmeier/go-sqlite-bench), Unlicense): inserting 1 million rows in one transaction took 1,480 ms with `mattn/go-sqlite3` (cgo, MIT), 2,419 ms with `modernc.org/sqlite` (C transpiled to Go, no cgo, BSD-3-Clause) and 2,719 ms with `ncruces/go-sqlite3` (SQLite compiled to WASM and run by wazero, no cgo, MIT). The author's summary: "SQLite without CGO is possible nowadays." At pairtime's write rate, all three are far faster than needed, so the cgo-free drivers win on cross-compilation (1.5). `modernc.org/sqlite` adds several megabytes to the binary (unverified size). Rust's `rusqlite` with the `bundled` feature compiles SQLite's C in the build.

Alternatives considered: bbolt (what wakatime-cli uses for its offline queue, `~/.wakatime/offline_heartbeats.bdb`; hackatime-and-hack-club.md) is a fine key-value queue but has no SQL for dashboard queries; plain JSONL alone makes "minutes per project this week" a full scan; DuckDB is far too large for the footprint target.

### 5.2 Encryption at rest

| Approach | Protects against | Doesn't protect against | Cost |
|---|---|---|---|
| Rely on full-disk encryption (BitLocker, FileVault, LUKS) | stolen laptop | anything running as the user | none; but Windows 10 Home has no BitLocker, only "Device Encryption" on some hardware (unverified for 2016 laptops) |
| Encrypt sensitive fields only (raw prompt text, transcript excerpts) with XChaCha20-Poly1305 (`golang.org/x/crypto`), key in the OS credential store | other local users, backups and cloud sync of the home folder, casual reading of the files, accidental upload | malware or a script running as the same user, which can ask the credential store for the key | small; one key lookup at agent start |
| SQLCipher for the whole DB | same as above, for every field | same as above | needs cgo in Go, reintroducing the cross-compile cost |

Key storage without cgo: [zalando/go-keyring](https://github.com/zalando/go-keyring) (MIT, pushed 2026-11-17) uses Windows Credential Manager, the `/usr/bin/security` binary on macOS ("It should be available by default"), and the Secret Service D-Bus API on Linux. Secret Service is missing on minimal window managers and headless boxes, so fall back to a key file with mode `0600` and say so in the UI. On Windows, DPAPI (`CryptProtectData`, per-user) is an alternative with no prompt.

Recommendation: store only what's needed (hashes, lengths, timestamps and derived features by default; raw prompt text only if the user turns on local history), and encrypt that raw text per field. Be honest in the privacy page that this guards against other users and accidental sharing, not against malware on the same account (R6).

### 5.3 UI: tray, menu bar, or a local web dashboard

| Option | Resident cost | Cost when open | Cross-platform effort | Notes |
|---|---|---|---|---|
| Local web dashboard served by the agent at `http://127.0.0.1:<port>`, opened in the user's own browser | none beyond the agent (embedded static files) | one browser tab | one codebase, can share components with the server dashboard (R5) | needs Host-header checks and a token (6.4). Chrome's Local Network Access gates requests from public sites to loopback behind a `loopback-network` permission prompt ([MDN](https://developer.mozilla.org/en-US/docs/Web/Security/Defenses/Local_network_access)); that helps, but the agent must not depend on it |
| Tray (Windows) / menu bar (macOS) icon | a few MB in the agent | a native menu | Windows `Shell_NotifyIcon`, macOS `NSStatusItem`, Linux StatusNotifierItem over D-Bus. [fyne-io/systray](https://github.com/fyne-io/systray) (Apache-2.0) covers all three, uses D-Bus SNI on Linux without GTK, but "requires cgo" and on macOS needs an app bundle | GNOME shows no tray icons without an AppIndicator extension (Ubuntu ships one; unverified for Fedora) |
| Tauri 2 or Wails window | none if the window process exits when closed | an empty Tauri 2 window: 108.0 MB private working set, 387.6 MB working set, 7 processes; an empty Electron 45 window: 103.3 MB and 331.9 MB, 4 processes (Windows 11, medians of 5 runs, [lleg.dev](https://lleg.dev/posts/electron-vs-tauri-empty-window-memory/), 2026-11-08). WebView2 processes exit about 15 s after the last window closes ([tauri#16528](https://github.com/tauri-apps/tauri/issues/16528), withdrawn report) | Tauri: Rust; Wails: Go. Both use WebView2 on Windows, WKWebView on macOS, WebKitGTK on Linux | WebView2 is "included as part of the Windows 11 operating system", and "The vast majority of Windows 10 devices have the WebView2 Runtime installed already", but "A small number" don't; the bootstrapper is about 2 MB and the Fixed Version runtime "over 250 MB" ([WebView2 distribution](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution)) |
| Electron | 100+ MB per window (above) | | | WakaTime's Windows/Linux tray app is Electron ([desktop-wakatime](https://github.com/wakatime/desktop-wakatime), TypeScript); its macOS app is native Swift |
| Native toolkit per OS (Win32, AppKit, GTK) | smallest | smallest | three UIs to maintain | not worth it for a dashboard |

On a 4 GB machine, a webview window costs about 100 MB whichever framework draws it, so the rule is: nothing with a webview stays resident. The local dashboard in the user's existing browser costs nothing extra while closed, and a browser is already open on almost every teen's laptop. A tray or menu-bar icon is worth adding for R3 (today's time at a glance, pause tracking, open dashboard), but as a thin layer: on Windows and Linux it can live in the agent; on macOS a tray needs cgo and an app bundle, so ship it as a small separate menu-bar helper or defer it.

## 6. Hooks calling the client: cold start and IPC

(pending)

## 7. Recommended client stack

(pending)

## 8. Design implications

(pending)

## 9. Open questions

(pending)
