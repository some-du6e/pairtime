# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.3, the five-project afternoon, from 13:54:45 to 14:31:00.

One timeline, two ways of holding A and B: two iTerm windows (the 6.3
fixture) or two tabs of one iTerm window (6.3-tabs, SM 6.3's second-to-last
paragraph). The input, the harness events and the editor and git events
are the same in both; only os.focus differs, because the OS sees a switch
between windows and not a switch between tabs. build() returns everything
a gen.py writes. README.md in each directory describes the choices.
"""

import os
import sys
from fractions import Fraction

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402

TABS_ERRATUM = "2026-11-19-sm-6.3-two-tabs"

THINK_LEAD_S, COMPOSE_CPS, EDIT_LEAD_S, LOW_LEAD_S, COMMIT_LEAD_S = 120, Fraction(3, 2), 120, 20, 60
ATTRIB_STICKY_S, READ_HORIZON_S = 120, 600


def typing_s(n):
    """The typing part of a lead for n typed characters, n / COMPOSE_CPS
    rounded to the nearest second, halves up (SM 1.3): 61 characters give
    41 s in 6.4 and 35 give 23 s in 6.5."""
    return Fraction(int(Fraction(n) / COMPOSE_CPS + Fraction(1, 2)))


def build(tabs):
    name = "6.3-tabs" if tabs else "6.3"
    fx = tl.Fixture(
        name,
        title="The five-project afternoon, A and B in two tabs of one iTerm window" if tabs
        else "The five-project afternoon",
        date="2026-11-28",
        timezone_name="America/Los_Angeles",
        utc_offset_min=-480,
        os_name="macos",
        keys=True,
        device=tl.key("d", "6.3 sam macbook"),
        boot_id=tl.key("b", "6.3 boot"),
        boot="08:12:47.000",
        seq0=52174,
        # He was in another app before the fixture starts.
        last_input="13:54:41.200",
        last_key="13:54:36.900",
    )
    at = fx.at

    A, B, C, D, E = (tl.key("p", n) for n in ("portfolio", "discord-bot", "homework-helper", "plant-sensor", "rusty-cli"))
    S_A = tl.key("s", "6.3 claude session")
    S_B = tl.key("s", "6.3 codex session")
    S_E = tl.key("s", "6.3 cursor conversation")
    T = {k: tl.key("t", f"6.3 turn {k}") for k in ("B0", "A1", "B1", "E1", "E2", "A2", "B2")}
    ITERM, CURSOR_APP, VSCODE = (tl.key("a", n) for n in ("6.3 iTerm2", "6.3 Cursor", "6.3 Code"))
    FA = [tl.key("f", f"6.3 portfolio file {i}") for i in range(1, 6)]
    FB = [tl.key("f", f"6.3 discord-bot file {i}") for i in range(1, 4)]
    # E's one file: src/parse.rs, its unit tests included.
    F_E = tl.key("f", "6.3 rusty-cli src/parse.rs")
    F_D = tl.key("f", "6.3 plant-sensor sketch.ino")
    CMD_A_BUILD, CMD_B_TEST, CMD_E_TEST = (tl.key("c", n) for n in ("6.3 npm run build", "6.3 npm test", "6.3 cargo test"))
    ERR_E = tl.key("e", "6.3 cargo test failure")
    CC = dict(harness="claude_code", version="2.1.328", session=S_A, project=A)
    CODEX = dict(harness="codex", version="0.170.0", session=S_B, project=B)
    CURSOR = dict(harness="cursor", version="2.1.50", session=S_E, project=E)

    # Input. macOS reports key presses apart from other input, so the two
    # differ here. In 6.3-tabs a click on a tab or Cmd-` between tabs is
    # the same input as the switch between windows.
    fx.pointer("13:54:44.600")                       # clicks iTerm window 2 (tab 2)
    fx.typing("13:54:46.000", "13:54:48.400", 600)   # "codex", Enter
    fx.typing("13:54:55.000", "13:55:39.800", 700)   # B's earlier prompt, 64 characters
    fx.pointer_every("13:55:47.300", "13:57:57.300", 10_000)  # watches B's agent
    fx.pointer_every("13:58:06.100", "13:58:56.100", 10_000)  # reads B's 13:58 answer
    fx.pointer("13:58:59.800")                       # clicks window 1 (tab 1)
    fx.typing("13:59:01.000", "13:59:03.800", 700)   # "claude", Enter
    fx.typing("13:59:30.400", "14:01:29.800", 600)   # A's 180-character prompt
    fx.pointer("14:01:38.500", "14:01:51.200")       # watches A's agent start
    fx.pointer("14:02:00.100")                       # clicks window 2 (tab 2)
    fx.pointer("14:02:08.300", "14:02:19.600", "14:02:31.000", "14:02:42.700")  # rereads B's answer
    fx.typing("14:02:50.300", "14:04:29.800", 650)   # B's 150-character prompt
    fx.pointer("14:04:38.900", "14:04:52.400")       # watches B's agent start
    fx.pointer("14:05:00.200")                       # clicks Cursor
    fx.pointer_every("14:05:07.400", "14:05:57.400", 10_000)  # scrolls through the code
    fx.typing("14:05:59.900", "14:08:39.900", 700)   # hand edits; saves at 14:07:30 and 14:08:40
    fx.keys_at("14:08:41.000")                       # opens a new agent chat
    fx.typing("14:08:42.000", "14:08:59.800", 400)   # the 50-character request
    fx.pointer_every("14:09:06.200", "14:10:56.200", 11_000)  # watches cargo test, reads the failure
    fx.typing("14:10:59.900", "14:12:29.900", 700)   # fixes the test; saves at 14:12:30
    fx.keys_at("14:12:33.500")                       # pages back up the test output
    fx.pointer("14:12:41.000")                       # rereads it
    fx.typing("14:12:45.000", "14:12:59.800", 800)   # "run the tests again"
    fx.pointer_every("14:13:05.600", "14:13:38.600", 11_000)  # watches the tests
    fx.typing("14:13:44.000", "14:13:58.600", 700)   # the commit message
    fx.pointer("14:13:59.700")                       # Commit
    fx.keys_at("14:14:00.300")                       # Cmd-Tab to iTerm
    fx.pointer_every("14:14:06.800", "14:18:56.800", 9_000)  # reads A's summary and diff
    fx.typing("14:19:00.400", "14:20:29.800", 650)   # the 135-character follow-up
    fx.keys_at("14:20:30.400")                       # Cmd-` to window 2 (tab 2)
    fx.pointer_every("14:20:37.100", "14:23:57.100", 10_000)  # reads B's answer, then thinks
    fx.typing("14:24:00.400", "14:24:59.800", 600)   # the 90-character reply
    fx.pointer("14:25:00.400")                       # clicks VS Code
    fx.typing("14:25:00.700", "14:30:59.900", 800)   # hand coding the Arduino sketch
    # No input on a slot boundary (testdata/timelines/README.md): the
    # regular runs above land on a few; move those by 50 ms.
    fx.inputs = [(t + 50 if t % tl.SLOT_MS == 0 else t, k) for t, k in fx.inputs]
    fx.ticks("13:54:45", "14:31:00", tl.SLOT_MS)

    def focus(t, app_class, app, hosted=None, project=None):
        data = {"app_class": app_class, "app_hash": app}
        if hosted:
            data["hosted_sessions"] = sorted(hosted)
        if project:
            data["focus_project"] = project
        fx.emit(at(t), "os.focus", data, source="os", via="os", sample=True)

    def term(t, *hosted):
        """iTerm in front. In 6.3 each window hosts one session and resolves
        to its project; in 6.3-tabs the one window hosts every session
        started in it, and with more than one it resolves to none."""
        hosted = [s for s in hosted if s]
        project = {S_A: A, S_B: B}[hosted[0]] if len(hosted) == 1 else None
        focus(t, "terminal", ITERM, hosted, project)

    def session_start(t, h, entrypoint, parent, tty, mode=None):
        data = {"start_kind": "startup", "entrypoint": entrypoint, "parent_class": parent, "has_tty": tty}
        if mode:
            data["permission_mode"] = mode
        fx.emit(at(t), "session.start", data, source="hook", via="hook", sample=True, **h)

    def prompt(t, h, turn, length, low_effort=False):
        return fx.emit(at(t), "prompt.submit",
                       {"turn_key": T[turn], "len_chars": length, "pasted_chars": 0, "typed_len_est": length,
                        "origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
                        "low_effort": low_effort},
                       source="hook", via="hook", sample=True, **h)

    def output(t, h, turn, words, final=False, via="log"):
        fx.emit(at(t), "agent.output",
                {"turn_key": T[turn], "words": words, "code_lines": 0, "is_final": final, "asks_question": False,
                 "offers_options": False},
                source=via, via=via, sample=via == "hook", **h)

    def tool(t_end, h, cls, *, start_ms=1_000, files=None, added=0, removed=0, cmd=None, exit_code=None,
             error=None, visible=0):
        data = {"tool_class": cls}
        if files:
            data["files"] = files
        data["lines_added"] = added
        data["lines_removed"] = removed
        if cmd:
            data["cmd_hash"] = cmd
        if exit_code is not None:
            data["exit_code"] = exit_code
        if error:
            data["error_sig"] = error
        data["visible_output_lines"] = visible
        data["t_start"] = at(t_end) - start_ms
        data["t_end"] = at(t_end)
        fx.emit(at(t_end), "agent.tool", data, source="hook", via="hook", sample=True, **h)

    def stop(t, h, turn):
        fx.emit(at(t), "agent.stop", {"turn_key": T[turn], "stop_reason": "end_turn", "stop_hook_active": False},
                source="hook", via="hook", sample=True, **h)

    def heartbeat(t, editor, act, f, lines, project):
        return fx.emit(at(t), "editor.activity",
                       {"editor": editor, "act": act, "file_hash": f, "lines_changed": lines, "edit_source": "human"},
                       source="editor", via="editor", project=project)

    # 13:54:45, window 2: he starts Codex for B and asks it something; the
    # answer arrives at 13:58:00. This is "B's answer from 13:58".
    term("13:54:45.000")
    session_start("13:54:49.000", CODEX, "cli", "terminal_app", True, "on-request")
    term("13:54:49.000", S_B)
    prompt("13:55:40.000", CODEX, "B0", 64)
    output("13:55:51.000", CODEX, "B0", 40)
    tool("13:56:20.000", CODEX, "read", start_ms=600, files=[FB[0]])
    tool("13:56:58.000", CODEX, "edit", start_ms=400, files=[FB[0]], added=12, removed=6)
    tool("13:57:31.000", CODEX, "test", start_ms=14_000, cmd=CMD_B_TEST, exit_code=0, visible=4)
    output("13:57:59.600", CODEX, "B0", 160, final=True)
    stop("13:58:00.000", CODEX, "B0")

    # 13:59, window 1: he starts Claude Code for A and types its prompt. In
    # auto mode, as in 6.4: A's agent edits and builds while he is in other
    # windows, and SM's rows have no permission answer (README.md).
    if tabs:
        # Tab 1 of the same window: the OS sees nothing until the new
        # session changes what the window hosts.
        session_start("13:59:05.000", CC, "cli", "terminal_app", True, "auto")
        term("13:59:05.000", S_A, S_B)
    else:
        term("13:59:00.000")
        session_start("13:59:05.000", CC, "cli", "terminal_app", True, "auto")
        term("13:59:05.000", S_A)

    # 14:01:30, A's prompt. Its agent works until 14:09:00: 300 changed
    # lines, which alone would fill the bucket by 2,250 s.
    a1 = prompt("14:01:30.000", CC, "A1", 180)
    output("14:01:38.000", CC, "A1", 90)
    tool("14:01:47.000", CC, "read", start_ms=600, files=[FA[0]])
    tool("14:01:56.000", CC, "read", start_ms=600, files=[FA[1]])
    if not tabs:
        term("14:02:00.200", S_B)
    tool("14:02:31.000", CC, "edit", start_ms=400, files=[FA[0]], added=48, removed=16)
    tool("14:03:20.000", CC, "edit", start_ms=400, files=[FA[1]], added=40, removed=12)
    output("14:03:52.000", CC, "A1", 35)
    tool("14:04:40.000", CC, "edit", start_ms=400, files=[FA[2]], added=36, removed=12)

    # 14:04:30, B's prompt. Its agent stops at 14:07:00; the output waits.
    b1 = prompt("14:04:30.000", CODEX, "B1", 150)
    output("14:04:37.000", CODEX, "B1", 45)
    focus("14:05:00.300", "editor", CURSOR_APP, None, E)
    tool("14:05:12.000", CODEX, "edit", start_ms=400, files=[FB[1]], added=14, removed=6)
    tool("14:05:35.000", CC, "build", start_ms=21_000, cmd=CMD_A_BUILD, exit_code=0, visible=4)
    tool("14:06:01.000", CODEX, "test", start_ms=15_000, cmd=CMD_B_TEST, exit_code=0, visible=4)
    tool("14:06:25.000", CC, "edit", start_ms=400, files=[FA[3]], added=55, removed=15)
    output("14:06:58.000", CODEX, "B1", 210, final=True)
    stop("14:07:00.000", CODEX, "B1")
    output("14:07:18.000", CC, "A1", 40)
    tool("14:07:50.000", CC, "edit", start_ms=400, files=[FA[4]], added=50, removed=16)
    tool("14:08:30.000", CC, "build", start_ms=20_000, cmd=CMD_A_BUILD, exit_code=0, visible=4)
    output("14:08:56.000", CC, "A1", 210, final=True)

    # 14:05 to 14:14, E in Cursor: hand edits (WakaTime heartbeats on his
    # first keystroke and on two saves), then two agent turns and a commit.
    # The failing test is in the file he is editing, so no file switch
    # sends a heartbeat of its own (README.md).
    e_edits = [heartbeat("14:06:00.000", "cursor", "edit", F_E, 4, E),
               heartbeat("14:07:30.000", "cursor", "save", F_E, 9, E),
               heartbeat("14:08:40.000", "cursor", "save", F_E, 6, E)]
    session_start("14:08:41.500", CURSOR, "ide", "ide", False)
    focus("14:08:41.500", "editor", CURSOR_APP, [S_E], E)
    stop("14:09:00.000", CC, "A1")
    e1 = prompt("14:09:00.000", CURSOR, "E1", 50)
    tool("14:09:30.000", CURSOR, "test", start_ms=26_000, cmd=CMD_E_TEST, exit_code=101, error=ERR_E, visible=4)
    output("14:09:35.000", CURSOR, "E1", 40, final=True, via="hook")
    stop("14:09:36.000", CURSOR, "E1")
    e_edits += [heartbeat("14:11:00.000", "cursor", "edit", F_E, 3, E),
                heartbeat("14:12:30.000", "cursor", "save", F_E, 7, E)]
    e2 = prompt("14:13:00.000", CURSOR, "E2", 19, low_effort=True)
    tool("14:13:40.000", CURSOR, "test", start_ms=36_000, cmd=CMD_E_TEST, exit_code=0, visible=4)
    output("14:13:43.000", CURSOR, "E2", 12, final=True, via="hook")
    stop("14:13:44.000", CURSOR, "E2")
    commit = fx.emit(at("14:14:00.000"), "git.change",
                     {"op": "commit", "sha": tl.git_sha("6.3 rusty-cli commit"),
                      "parents": [tl.git_sha("6.3 rusty-cli parent")], "files_changed": 1, "insertions": 21,
                      "deletions": 8, "actor": "human"},
                     source="git", via="git", project=E)
    if tabs:
        term("14:14:00.400", S_A, S_B)
    else:
        term("14:14:00.400", S_A)

    # 14:20:30, A's follow-up; its agent runs until 14:23:00. Then B's
    # reply at 14:25:00; its agent runs until 14:26:00.
    a2 = prompt("14:20:30.000", CC, "A2", 135)
    if not tabs:
        term("14:20:30.500", S_B)
    output("14:20:36.000", CC, "A2", 90)
    tool("14:21:10.000", CC, "edit", start_ms=400, files=[FA[2]], added=18, removed=6)
    tool("14:21:55.000", CC, "edit", start_ms=400, files=[FA[3]], added=12, removed=4)
    tool("14:22:40.000", CC, "build", start_ms=20_000, cmd=CMD_A_BUILD, exit_code=0, visible=4)
    output("14:22:58.000", CC, "A2", 120, final=True)
    stop("14:23:00.000", CC, "A2")
    b2 = prompt("14:25:00.000", CODEX, "B2", 90)
    focus("14:25:00.500", "editor", VSCODE, None, D)
    d_edits = [heartbeat("14:25:00.800", "vscode", "edit", F_D, 2, D)]
    output("14:25:08.000", CODEX, "B2", 30)
    tool("14:25:31.000", CODEX, "edit", start_ms=400, files=[FB[2]], added=7, removed=3)
    output("14:25:58.000", CODEX, "B2", 80, final=True)
    stop("14:26:00.000", CODEX, "B2")
    # He saves every 70 to 75 s while he codes. In one file WakaTime sends a
    # heartbeat on a save and otherwise none until two minutes after the
    # last (HC 1.5), so each of these is a save. The last comes after the
    # window, so its lead covers 14:29.
    for t in ("14:26:10.000", "14:27:20.000", "14:28:35.000", "14:29:45.000", "14:30:55.000"):
        d_edits.append(heartbeat(t, "vscode", "save", F_D, 3, D))

    events = fx.build()
    # emit returns the raw row; build keeps its data dict, so that finds the
    # built event.
    built = {id(e["data"]): e for e in events}
    a1, b1, e1, e2, a2, b2, commit = (built[id(r[4])] for r in (a1, b1, e1, e2, a2, b2, commit))
    e_edits = [built[id(r[4])] for r in e_edits]
    d_edits = [built[id(r[4])] for r in d_edits]

    # Checks of the timing choices against SM's rows (README.md).
    def slot_inputs(s):
        return [k for t, k in fx.inputs if s <= t < s + tl.SLOT_MS]

    window = (at("14:00:00"), at("14:30:00"))
    tl.check(all(t % tl.SLOT_MS for t, _ in fx.inputs), "no input on a slot boundary")
    for s in range(window[0], window[1], tl.SLOT_MS):
        tl.check(slot_inputs(s), f"input in slot {fx.hhmmss(s)}, so every slot is present")

    def lead(ps):
        typing = typing_s(ps["data"]["typed_len_est"])
        return THINK_LEAD_S + typing, typing

    prompts = {"A1": (a1, 240, "13:57:30"), "B1": (b1, 220, "14:00:50"), "A2": (a2, 210, "14:17:00"),
               "B2": (b2, 180, "14:22:00")}
    for turn, (ps, want, start) in prompts.items():
        total, typing = lead(ps)
        tl.check(total == want and ps["t"] - total * 1000 == at(start), f"{turn}: lead {total} s from {start}")
        first = ps["t"] - int(typing * 1000)
        for s in range(first - first % tl.SLOT_MS, ps["t"], tl.SLOT_MS):
            tl.check(any(slot_inputs(s)), f"{turn}: key presses in slot {fx.hhmmss(s)} of the typing part")
        tl.check(ps["t"] - fx.last_input_before(ps["t"] - 1, keys_only=True) <= 10_000,
                 f"{turn}: keyboard input in the 10 s before submit")
    tl.check(b1["t"] - int(lead(b1)[1] * 1000) == at("14:02:50"), "B's typing part starts at 14:02:50")
    total, typing = lead(e1)
    tl.check(e1["t"] - typing * 1000 < at("14:08:40"), "E1's typing part covers 14:08:40 to 14:09:00")
    for s in range(at("14:08:30"), at("14:09:00"), tl.SLOT_MS):
        tl.check(any(slot_inputs(s)), f"E1: key presses in slot {fx.hhmmss(s)}")
    for ev in e_edits + d_edits + [commit]:
        s = ev["t"] - ev["t"] % tl.SLOT_MS
        tl.check(any(slot_inputs(s)), f"key presses in the slot of the {ev['kind']} at {fx.hhmmss(ev['t'])}")
    tl.check(e2["t"] - LOW_LEAD_S * 1000 == at("14:12:40"), "the go-ahead's 20 s lead starts at 14:12:40")
    tl.check(commit["t"] - COMMIT_LEAD_S * 1000 == at("14:13:00"), "the commit's lead starts at 14:13:00")
    gaps = [b["t"] - a["t"] for a, b in zip(d_edits, d_edits[1:])]
    tl.check(all(60_000 <= g <= 80_000 for g in gaps), f"D's heartbeats 60 to 80 s apart: {gaps}")
    tl.check(d_edits[-1]["t"] > window[1] and d_edits[-2]["t"] < window[1], "the last heartbeat's lead covers 14:29")
    for a, b in zip(d_edits, d_edits[1:]):
        tl.check(b["t"] - a["t"] <= EDIT_LEAD_S * 1000, "D's leads leave no gap")

    # The reading buckets (SM 2.4), checked with timeline.Bucket: A's never
    # runs dry from 14:01:30 to 14:02:00 or from 14:14:00 to 14:17:00, and is
    # at its 600 s cap at 14:14; B's never runs dry from 14:04:30 to 14:05:00
    # or from 14:20:30 to 14:22:00, and holds 255 s at 14:20:30; E's holds at
    # least 10 s at 14:12:30.
    def bucket_run(session, drains, until):
        bucket = tl.Bucket()
        fills = []
        for e in events:
            if e["session_key"] != session:
                continue
            if e["kind"] == "prompt.submit":
                fills.append((e["t"], "reset"))
            elif e["kind"] in ("agent.output", "agent.tool"):
                fills.append((e["t"], bucket.amount(e["kind"], e["data"])))
        i = 0
        snapshots = {}
        tau = fills[0][0]
        drains = [(at(a), at(b)) for a, b in drains]
        while tau <= at(until):
            while i < len(fills) and fills[i][0] <= tau:
                t, amount = fills[i]
                if amount == "reset":
                    bucket.reset(t, tl.Bucket.BASE_S)
                else:
                    bucket.fill(t, amount)
                i += 1
            bucket.expire(tau)
            snapshots[tau] = bucket.total()
            if any(a <= tau < b for a, b in drains):
                tl.check(bucket.drain(1) == 1, f"{fx.name}: the bucket has something to read at {fx.hhmmss(tau)}")
            tau += 1000
        return snapshots

    a_bucket = bucket_run(S_A, [("14:01:30", "14:02:00"), ("14:14:00", "14:17:00")], "14:17:00")
    tl.check(a_bucket[at("14:14:00")] == 600, f"A's bucket is at its cap at 14:14: {a_bucket[at('14:14:00')]}")
    diff_lines = sum(e["data"]["lines_added"] + e["data"]["lines_removed"] for e in events
                     if e["kind"] == "agent.tool" and e["session_key"] == S_A and a1["t"] < e["t"] <= at("14:09:00"))
    tl.check(diff_lines == 300 and diff_lines * 60 // 8 == 2250, f"A's agent changed {diff_lines} lines")
    b_bucket = bucket_run(S_B, [("14:04:30", "14:05:00"), ("14:20:30", "14:22:00")], "14:22:00")
    tl.check(b_bucket[at("14:20:30")] == 255, f"255 s left in B's bucket at 14:20:30: {b_bucket[at('14:20:30')]}")
    e_bucket = bucket_run(S_E, [("14:12:30", "14:12:40")], "14:12:40")
    tl.check(e_bucket[at("14:12:30")] >= 10, "E's bucket holds the 10 s of READ at 14:12:30")
    tl.check(a2["t"] - READ_HORIZON_S * 1000 <= at("14:14:00"), "A's reading is within 600 s of the follow-up")
    tl.check(b2["t"] - READ_HORIZON_S * 1000 <= at("14:20:30"), "B's reading is within 600 s of the reply")

    # Agent time. A's and B's agents run while he works elsewhere; the
    # seconds of a run that its own project doesn't credit are that
    # project's agent-only time (SM 2.6, 6.3).
    runs = {A: [(a1["t"], at("14:09:00")), (a2["t"], at("14:23:00"))],
            B: [(b1["t"], at("14:07:00")), (b2["t"], at("14:26:00"))],
            E: [(e1["t"], at("14:09:36")), (e2["t"], at("14:13:44"))]}
    watched = {A: [(at("14:01:30"), at("14:02:00"))], B: [(at("14:04:30"), at("14:05:00"))],
               E: [(at("14:09:00"), at("14:09:36")), (at("14:13:00"), at("14:13:44"))]}
    if tabs:
        # SM's paragraph moves 14:01:30-14:02:00 from A to B: A's agent runs
        # with nothing of A credited.
        watched[A] = []

    def overlap(spans, a, b):
        return sum(max(0, min(y, b) - max(x, a)) for x, y in spans)

    def agent_seconds(minute):
        """Seconds of the minute in which any agent runs, counting each
        second once."""
        a = at(minute)
        b = a + tl.MINUTE_MS
        cuts = sorted({a, b} | {x for v in runs.values() for s in v for x in s if a < x < b})
        n = 0
        for x, y in zip(cuts, cuts[1:]):
            if any(s <= x and y <= e for v in runs.values() for s, e in v):
                n += y - x
        return n // 1000

    agent_only = {p: (overlap(runs[p], 0, 1 << 62) - overlap(watched[p], 0, 1 << 62)) // 1000 for p in runs}
    agent_time = {p: overlap(runs[p], 0, 1 << 62) // 1000 for p in runs}
    tl.check(agent_only[A] == (600 if tabs else 570) and agent_only[B] == 180 and agent_only[E] == 0,
             f"agent-only time {agent_only}")

    if tabs:
        tl.check(not any(e["kind"] == "os.focus" and window[0] <= e["t"] < window[1] and e["data"]["app_class"] == "terminal"
                         and e["data"].get("focus_project") for e in events), "iTerm never resolves to one project")
        sm_rules = attribute_tabs(fx, events, ITERM, {S_A: A, S_B: B})
        tl.check(sm_rules == "14:00:00-14:01:30 A, 14:01:30-14:05:00 B, 14:14:00-14:17:00 ambiguous, "
                 "14:17:00-14:22:00 A, 14:22:00-14:25:00 B", f"SM 3.2 in the tab window gives {sm_rules}")
    else:
        sm_rules = None

    exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 6.3", evidence="A", witness="witnessed",
                      projects={A: "portfolio", B: "discord-bot", C: "homework-helper", D: "plant-sensor",
                                E: "rusty-cli"},
                      start="14:00:00", end="14:30:00")
    rows = [
        ("14:00", {A: {"P": 60}}, "14:00"),
        ("14:01", {A: {"P": 30}, B: {"P": 30}} if tabs else {A: {"P": 30, "SUP": 30}}, "14:01"),
        ("14:02", {B: {"P": 60}}, "14:02"),
        ("14:03", {B: {"P": 60}}, "14:03"),
        ("14:04", {B: {"P": 30, "SUP": 30}}, "14:04"),
        ("14:05", {E: {"E": 60}}, "14:05"),
        ("14:06", {E: {"E": 60}}, "14:06"),
        ("14:07", {E: {"E": 60}}, "14:07"),
        ("14:08", {E: {"E": 40, "P": 20}}, "14:08"),
        ("14:09", {E: {"E": 60}}, "14:09"),
        ("14:10", {E: {"E": 60}}, "14:10"),
        ("14:11", {E: {"E": 60}}, "14:11"),
        ("14:12", {E: {"E": 30, "READ": 10, "GO": 20}}, "14:12"),
        ("14:13", {E: {"G": 60}}, "14:13"),
        ("14:14", {A: {"READ": 60}}, "14:14"),
        ("14:15", {A: {"READ": 60}}, "14:15"),
        ("14:16", {A: {"READ": 60}}, "14:16"),
        ("14:17", {A: {"P": 60}}, "14:17"),
        ("14:18", {A: {"P": 60}}, "14:18"),
        ("14:19", {A: {"P": 60}}, "14:19"),
        ("14:20", {A: {"P": 30}, B: {"READ": 30}}, "14:20"),
        ("14:21", {B: {"READ": 60}}, "14:21"),
        ("14:22", {B: {"P": 60}}, "14:22"),
        ("14:23", {B: {"P": 60}}, "14:23"),
        ("14:24", {B: {"P": 60}}, "14:24"),
        ("14:25", {D: {"E": 60}}, "14:25"),
    ]
    tab_erratum_rows = {"14:14", "14:15", "14:16", "14:20", "14:21"}
    derived_agent = {"14:09", "14:13"}
    for minute, credit, sm_row in rows:
        kw = {}
        if minute in derived_agent:
            kw["derived"] = ["agent_running_s"]
            kw["note"] = "E's Cursor turns aren't timed in SM; agent_running_s follows from the fixture's stops."
        if tabs and minute == "14:01":
            sm_row = "14:01, moved by the paragraph on two tabs"
        if tabs and minute in tab_erratum_rows:
            kw["erratum"] = TABS_ERRATUM
        exp.row(minute, credit=credit, uncredited={}, agent=agent_seconds(minute), sm=sm_row, **kw)
    exp.row("14:26", "14:30", credit={D: {"E": 240}}, uncredited={}, agent=0, sm="14:26-14:29")

    by_project = {A: 480, B: 480, E: 540, D: 300} if tabs else {A: 510, B: 450, E: 540, D: 300}
    extra = {"credited_s": 1800, "credited_by_project": by_project, "wall_s": 1800}
    for p in (A, B, E):
        extra[f"agent_running_s.{p}"] = agent_time[p]
    for p in (A, B, E):
        extra[f"agent_only_s.{p}"] = agent_only[p]
    stated = ["credited_s", "wall_s"]
    if not tabs:
        stated += [f"agent_only_s.{A}", f"agent_only_s.{B}"]
    notes = [
        "credited_by_project is SM's: " + ("A 480, B 480, E 540, D 300 for two tabs, A 510, B 450 with two windows"
                                           if tabs else "A 510, B 450, E 540, D 300") + ". Every second goes to one "
        "project, so they add up to the 1,800 s total.",
        "agent_running_s counts a second once however many agents run in it. agent_running_s.<project> is that "
        "project's agent time and agent_only_s.<project> the part of it the same project doesn't credit, the "
        "chess clock of SM 2.6: " + (
            "A's agent runs 600 s and none of it is credited to A once 14:01:30-14:02:00 moves to B."
            if tabs else "A's agent runs 600 s, 30 of them watched, B's 210 s, 30 watched. They sum to SM's 750 s, "
            "more than agent_running_s because A's and B's runs overlap from 14:04:30 to 14:07:00."),
        "A's second turn stops at 14:23:00 and B's at 14:26:00. SM doesn't time them; its 570 s and 180 s of "
        "unwatched agent time do.",
        "E's Cursor turns (14:09:00-14:09:36, 14:13:00-14:13:44) fall inside E's credited seconds, so E has no "
        "agent-only time.",
        "C (homework-helper) has no records here: SM 6.3 says Sam works on it after 15:00.",
    ]
    if tabs:
        notes.append("The rows are SM's: the paragraph on two tabs moves 30 s and says the total doesn't change. "
                     "Under SM 3.2's own rule 14:14:00-14:17:00 is X.ambiguous and 14:20:30-14:22:00 goes to A; "
                     f"see the erratum. SM 3.2 applied by gen.py gives {sm_rules}.")
    expected = exp.doc(extra_totals=extra, stated=stated, approx={}, errata=[TABS_ERRATUM] if tabs else [],
                       notes=notes)

    def anc(ev, kind, lead_s, lead_from, sm, origin=None, cls=None):
        return tl.anchor(fx, ev, kind=kind, origin=origin, prompt_class=cls, lead_s=lead_s, trail_s=0,
                         lead_from=lead_from, level="A", sm=sm)

    def p_lead(ps):
        total, _ = lead(ps)
        return int(total) if total.denominator == 1 else float(total)

    sub = dict(origin="human_typed", cls="substantive")
    b0 = fx.event_at("prompt.submit", at("13:55:40"))
    anchor_list = [
        anc(b0, "substantive_prompt", p_lead(b0), "params",
            "14:02: the prompt behind B's answer from 13:58, sent at 13:55:40 before the window (64 characters, "
            "lead 120 + 64 / 1.5, rounded)", **sub),
        anc(a1, "substantive_prompt", 240, "sm", "14:01: sends at 14:01:30 (lead 240 s)", **sub),
        anc(b1, "substantive_prompt", 220, "sm", "14:04: sends at 14:04:30 (lead 220 s)", **sub),
        anc(e_edits[0], "typed_edit", EDIT_LEAD_S, "params", "14:05: heartbeat at 14:06:00"),
        anc(e_edits[1], "typed_edit", EDIT_LEAD_S, "params", "14:05: heartbeat at 14:07:30"),
        anc(e_edits[2], "typed_edit", EDIT_LEAD_S, "params", "14:08: last edit at 14:08:40"),
        anc(e1, "substantive_prompt", p_lead(e1), "params",
            "14:08: asks Cursor's agent to run the tests (50 characters), sent 14:09:00; lead 120 + 50 / 1.5, "
            "rounded", **sub),
        anc(e_edits[3], "typed_edit", EDIT_LEAD_S, "params", "14:11: edit at 14:11:00"),
        anc(e_edits[4], "typed_edit", EDIT_LEAD_S, "params", "14:11: edit at 14:12:30"),
        anc(e2, "go_ahead", LOW_LEAD_S, "sm", "14:12: \"run the tests again\" (a go-ahead, lead 20 s)",
            origin="human_typed", cls="low_effort"),
        anc(commit, "human_commit", COMMIT_LEAD_S, "params", "14:13: commits at 14:14:00"),
        anc(a2, "substantive_prompt", 210, "sm", "14:17: the follow-up he sends at 14:20:30 (135 characters, "
            "lead 210 s)", **sub),
        anc(b2, "substantive_prompt", 180, "sm", "14:22: his reply (90 characters, sent 14:25:00, lead 180 s)",
            **sub),
    ] + [anc(ev, "typed_edit", EDIT_LEAD_S, "params", "14:25-14:29: hand coding with heartbeats every 60-80 s")
         for ev in d_edits]
    turns = [
        tl.turn(fx, at("13:55:40"), at("13:58:00"), session=S_B, turn_key=T["B0"], origin="human_typed",
                prompt_class="substantive", sm="14:02: B's answer from 13:58; before the window"),
        tl.turn(fx, a1["t"], at("14:09:00"), session=S_A, turn_key=T["A1"], origin="human_typed",
                prompt_class="substantive", sm="14:01 and 14:09"),
        tl.turn(fx, b1["t"], at("14:07:00"), session=S_B, turn_key=T["B1"], origin="human_typed",
                prompt_class="substantive", sm="14:04 and 14:07"),
        tl.turn(fx, e1["t"], at("14:09:36"), session=S_E, turn_key=T["E1"], origin="human_typed",
                prompt_class="substantive", sm="14:08 and 14:09"),
        tl.turn(fx, e2["t"], at("14:13:44"), session=S_E, turn_key=T["E2"], origin="human_typed",
                prompt_class="low_effort", sm="14:12 and 14:13"),
        tl.turn(fx, a2["t"], at("14:23:00"), session=S_A, turn_key=T["A2"], origin="human_typed",
                prompt_class="substantive", sm="14:20"),
        tl.turn(fx, b2["t"], at("14:26:00"), session=S_B, turn_key=T["B2"], origin="human_typed",
                prompt_class="substantive", sm="14:24"),
    ]
    anchors = {"fixture": name, "anchors": anchor_list, "turns": turns}

    def pr(turn, ps, text, source):
        return {"turn_key": T[turn], "t": ps["t"], "local": fx.hhmmss(ps["t"]), "text": text,
                "len_chars": ps["data"]["len_chars"], "source": source}

    prompts_doc = {"fixture": name, "prompts": [
        pr("B0", b0, None, "not in SM; the turn whose 13:58 answer he rereads at 14:02"),
        pr("A1", a1, None, "SM 6.3's 14:00 row gives the length, 180 characters, not the text"),
        pr("B1", b1, None, "SM 6.3's 14:03 row gives the length, 150 characters, not the text"),
        pr("E1", e1, None, "SM 6.3's 14:08 row describes the request (run the tests without fixing anything, "
                           "50 characters) without quoting it"),
        pr("E2", e2, "run the tests again", "quoted in SM 6.3's 14:12 row"),
        pr("A2", a2, None, "SM 6.3's 14:17 row gives the length, 135 characters, not the text"),
        pr("B2", b2, None, "SM 6.3's 14:22 row gives the length, 90 characters, not the text"),
    ]}
    return fx, expected, anchors, prompts_doc


def attribute_tabs(fx, events, app, project_of):
    """SM 3.2 rule 1 for the one iTerm window of 6.3-tabs, slot by slot: the
    session whose next human anchor in the window comes soonest, if the slot
    is inside that anchor's lead; else the session with the most recent
    human anchor within ATTRIB_STICKY_S; else ambiguous. Returns the
    window's slots in the half hour grouped into runs, as "HH:MM:SS-HH:MM:SS
    project" strings. Only prompts are human anchors in this window."""
    at = fx.at
    anchors = []
    for e in events:
        if e["kind"] == "prompt.submit" and e["session_key"] in project_of:
            lead_ms = int((THINK_LEAD_S + typing_s(e["data"]["typed_len_est"])) * 1000)
            anchors.append((e["t"], e["session_key"], lead_ms))
    focus = []
    for e in events:
        if e["kind"] == "os.focus":
            focus.append((e["t"], e["data"].get("app_hash") == app))

    def in_window(s):
        """Whether iTerm held focus longest in the slot."""
        held, cur, t0 = 0, False, s
        for t, is_app in focus:
            if t <= s:
                cur = is_app
        for t, is_app in focus:
            if s < t < s + tl.SLOT_MS:
                held += (t - t0) if cur else 0
                cur, t0 = is_app, t
        held += (s + tl.SLOT_MS - t0) if cur else 0
        return held * 2 > tl.SLOT_MS

    runs = []
    for s in range(at("14:00:00"), at("14:30:00"), tl.SLOT_MS):
        if not in_window(s):
            continue
        ahead = [a for a in anchors if a[0] > s]
        got = "ambiguous"
        if ahead:
            t, sess, lead_ms = min(ahead)
            if t - lead_ms <= s:
                got = project_of[sess]
        if got == "ambiguous":
            recent = [a for a in anchors if a[0] <= s and s - a[0] < ATTRIB_STICKY_S * 1000]
            if recent:
                got = project_of[max(recent)[1]]
        if runs and runs[-1][2] == got and runs[-1][1] == s:
            runs[-1][1] = s + tl.SLOT_MS
        else:
            runs.append([s, s + tl.SLOT_MS, got])
    names = {v: k for k, v in zip("AB", project_of.values())}
    return ", ".join(f"{fx.hhmmss(a)}-{fx.hhmmss(b)} {names.get(p, p)}" for a, b, p in runs)
