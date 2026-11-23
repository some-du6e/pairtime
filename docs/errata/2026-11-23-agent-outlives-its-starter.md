# Erratum: an agent that a hook starts on Linux stops with the session that started it

Date 2026-11-23. Raised by WP-HL1, from a Linux server with no graphical session that runs Claude Code sessions under T3 Code. Status: decided 2026-11-23 by the orchestrator for Karim, as the Decision section below records, and applied by the PR that adds this file, under `oracle-change`: DESIGN 3.2 gains the sentence that says what detached means, and the agent's scope asks for `OOMPolicy=continue`.

## Input

- DESIGN 3.2: `pairtime agent` is started by the login item "or a hook that finds it down", and the hook "starts the agent detached".
- `cmd/pairtime` before this change: `startAgent` (the hook, `agent --detach`, `dashboard`, `login`) and `startAgentFrom` (`install`, `privacy delete`) start `pairtime agent` with `Setsid` and the null device for stdio (`detach_unix.go`), and on Windows with `CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP` (`detach_windows.go`).
- pairtime installs no systemd user unit, and its XDG autostart entry runs only at a graphical login (`internal/install/autostart.go`).

## Expected

An agent that a hook starts keeps running when the harness session, terminal or app that ran the hook ends, until the user logs out or stops it.

## Computed

T3 Code runs each Claude Code session in a transient scope of the user's systemd manager, `app-t3code-agents-<id>.slice/t3code-claude-<id>.scope`, with `KillMode=control-group`, `OOMPolicy=stop` and `TimeoutStopSec=10s`. The agent that a hook started ran in one such scope, with `systemd --user` as its parent. `setsid` gives a process a session of its own, so a terminal's hangup misses it, but it leaves the process in its parent's cgroup. When T3 stops the scope at the session's end, systemd signals every process in it, the agent included, and the next hook starts another agent, in the next session's scope.

The same goes for any Linux agent that a hook starts inside a unit that stops when its session does: a terminal's or an app's scope that it stops when it closes, or an SSH session's scope on a host whose logind ends a session's processes at logout (`KillUserProcesses=yes`).

## The fix

**Linux.** When a systemd user manager runs for the user, the agent starts as

```
systemd-run --user --scope --quiet --collect --unit=pairtime-agent-<16 hex digits>.scope --description="pairtime agent" --property=CPUWeight=10 --property=IOWeight=10 --property=OOMPolicy=continue -- <binary> agent
```

and runs in a scope of its own under the manager's `app.slice`. For a scope, systemd-run asks the manager to move systemd-run's own process into the new unit, waits until the unit has started, and then executes the agent in that process. So the agent keeps everything it had before: the starter's whole environment, with the variables that decide its data directory and socket (`PAIRTIME_HOME`, the `XDG_*` folders, `HOME`), so nothing is passed by name; its working directory; its `Setsid` session and null stdio; and the process ID and exit status the starter sees, which `install` reads to tell an agent that found another running (exit status 3).

- A manager runs when `XDG_RUNTIME_DIR` is an absolute path, `$XDG_RUNTIME_DIR/systemd/private` is a socket that the process's effective user owns, and `systemd-run` is on PATH. Checking costs no process. A session bus alone doesn't count, since one runs without systemd too, and the owner check keeps root, holding another user's `XDG_RUNTIME_DIR` as `sudo -E` leaves it, out of that user's manager.
- Each start gets a random name. A fixed one would fail while the unit of an agent that is stopping is still loaded, or while another hook starts one at the same moment. The data directory's lock still lets one agent at a time run.
- The start counts once the process's cgroup path, read every 2 ms, ends in the unit's name, or once the process exits with status 3, which only the agent does. If systemd-run exits first, or takes more than 3 s, after which it is killed, the agent starts as it did before this change. So does every start without a user manager, and a binary whose path holds a `$`, because systemd-run(1) says a later release will expand `${NAME}` in a scope's command line by default.
- `--collect` unloads the unit when the agent ends, even after a failure. `--description` keeps the binary's path out of unit lists and the journal.
- `CPUWeight=10` and `IOWeight=10` give the scope a tenth of a unit's default share when the CPU or the disk is contended, the weight T3 Code gives its own agents' slice (`app-t3code.slice`, `CPUWeight=10` on the server that found this). In its starter's scope the agent shared that scope's share. In `app.slice` at the default weight it would take ten times what every Claude session of T3 Code gets together, and the lookback reader's idle priority ranks it only against the agent's own threads. A background tracker gives way, as the agent's EcoQoS on Windows and Background process type on macOS do (DESIGN 3.2). A numeric weight needs systemd 232. A manager that doesn't control a resource for the user's units leaves its weight unset, as systemd 255 there does `IOWeight`, and the start goes ahead.
- It needs systemd 236 (`--collect`), and 238 when the starter runs outside the manager's own cgroup tree, as an SSH session does, where the manager moves the process through PID 1's `AttachProcessesToUnit`. With older ones the start fails and falls back.

`TestAgentOutlivesScope`, which ci.yml runs as the runner user under that user's manager (systemd 259 on ubuntu-26.04), starts the real agent from inside a scope with `agent --detach` and with a hook. It checks that the agent runs in a scope of its own with the starter's environment and working directory, still answers once the starter's scope has stopped, and leaves no unit behind once it stops. Its control case starts the agent as before and sees it stop with the scope.

**macOS.** No change. launchd kills what is left of a job's process group when the job exits, unless the job sets `AbandonProcessGroup` (launchd.plist(5)), and a closing terminal hangs up its session. A `Setsid` agent has a session and a process group of its own and no controlling terminal, so neither reaches it. macOS has nothing like a cgroup that ends a whole tree of processes, and once the hook exits, the agent's parent is launchd, so an app that kills its own descendants doesn't find it either.

**Windows.** The agent now also starts with `CREATE_BREAKAWAY_FROM_JOB`, so it leaves the job object of the process that starts it when the job allows breakaway (`JOB_OBJECT_LIMIT_BREAKAWAY_OK`), and isn't ended with the job's other processes when the job's last handle closes under `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. A job that allows no breakaway fails such a start, and the agent then starts again without the flag, in the job, as before. Outside any job the flag does nothing. Neither Windows Terminal's source nor VS Code's terminals (node-pty) put a job around a shell. Closing a tab signals the processes attached to its console, and the agent has a console of its own. The job libuv puts a Node program's children in lets their children out silently (`JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK`), so an agent that a hook started was already outside it. `TestAgentStartLeavesJob` checks these cases on Windows in platform.yml, with a control that starts the agent without the flag.

## Limits

- An agent in a unit of the user manager stops when the manager does: at the user's last logout, unless lingering is on, as every user unit does. A `Setsid` agent left in a session's scope could outlive a logout where logind leaves a session's processes running (`KillUserProcesses=no`).
- Presence: an agent started from an SSH or text-console session used to be in that logind session, and `internal/platform/linux/logind.go` followed that session's lock state, which never locks. In a unit of the user manager the agent is in no session. It now follows the session its environment names, `XDG_SESSION_ID`, which `systemd-run --scope` passes on, while that session lasts and isn't closing. Without one, or once it ends, it follows the user's graphical session and, with none, reports lock state as unknown, as an agent from XDG autostart under systemd, or from T3 Code, already did. So the locked desktop of a user who works over SSH stops counting their time only once that SSH session has ended.
- Under systemd 253 and later, a scope takes the manager's `DefaultOOMPolicy=`, `stop` by default, so the kernel's OOM kill of any process in the agent's scope, such as a git it runs, would stop the agent too, as an OOM kill of any process of a T3 session did in its scope. The scope asks for `OOMPolicy=continue`. systemd before 253 refuses that property for a scope, and its systemd-run exits at once, so a start that exits before the agent is in its unit is made once more without it, which costs a fork on such a system alone, before the agent starts as it did before. There the OOM policy is the scope's default, which before 253 doesn't stop a scope.
- A hook that starts the agent waits until the agent is in its scope, 14 to 19 ms on the server that found this, and a user manager that hangs holds it for up to 3 s, after the hook has already left its call in the inbox. `pairtime hook`'s help says so.
- On Windows, a job that allows no breakaway, such as one an app makes with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` alone, still ends the agent with it. Leaving such a job takes a process that isn't the starter's child, such as one WMI's `Win32_Process.Create` or the Task Scheduler starts, which is too heavy for a hook.
- A hook-started agent on macOS isn't a launchd job, so DESIGN 3.2's "leaves `ProcessType` at `Background`" holds only for the agent the LaunchAgent starts: one a hook starts runs at its parent's QoS. That was so before this change, which leaves macOS as it was.

## Decision

Decided 2026-11-23 by the orchestrator for Karim, on #250:

1. DESIGN 3.2 takes the sentence below, after its table, under `oracle-change`.
2. The agent's scope asks for `OOMPolicy=continue`, and a start that systemd before 253 refuses for it is made again without it before the agent starts as before.
3. The other limits above stand: the agent stops at the user's last logout without linger, and the hook's next start falls back once the manager is gone; an agent outside any session follows the session `XDG_SESSION_ID` names; a Windows job that allows no breakaway still ends the agent; and a hook-started agent on macOS runs at its parent's QoS.

The sentence DESIGN 3.2 gains:

> Detached means a process that the hook's session, terminal and job can't end: on Linux, a transient scope of the user's systemd manager when one runs; elsewhere, a session (Unix) or a process group with no console window (Windows) of its own, outside the caller's job when the job allows breakaway.
