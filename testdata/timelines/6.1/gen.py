# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.1, three hours of deep flow with Claude Code. See README.md in
this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402

EDGES = "2026-11-19-sm-6.1-presence-edges"
SAME_ERROR = "2026-11-19-sm-6.1-same-error"
REPLAY = "2026-11-19-sm-6.1-hackatime-replay"

fx = tl.Fixture(
    "6.1",
    title="Three hours of deep flow with Claude Code",
    date="2026-11-21",
    timezone_name="Asia/Kolkata",
    utc_offset_min=330,
    os_name="macos",
    keys=True,
    device=tl.key("d", "6.1 rowan macbook"),
    boot_id=tl.key("b", "6.1 boot"),
    boot="08:02:37.000",
    seq0=52113,
    # His last input before the fixture starts, in an app that isn't a
    # project context, so the agent recorded nothing then.
    last_input="13:56:51.300",
    last_key="13:56:49.800",
)

at = fx.at
P = tl.key("p", "cs2go")
S = tl.key("s", "6.1 claude session")
CC = dict(harness="claude_code", version="2.1.328", session=S, project=P)
ITERM = tl.key("a", "6.1 iterm2")
CODE = tl.key("a", "6.1 vscode")
BROWSER = tl.key("a", "6.1 browser")
CMD_TEST = tl.key("c", "6.1 pytest")
ASSERTION = tl.key("e", "6.1 AssertionError in test_hitbox_follows_position")
FILES = [tl.key("f", f"6.1 file {i}") for i in range(12)]

CYCLES = 15
CYCLE_MS = 720_000
T0, END = at("14:00:00"), at("17:00:00")
TURN = [None] + [tl.key("t", f"6.1 turn {k}") for k in range(1, CYCLES + 1)]
# Cycle 1 is SM's table, whose minute 11 is a commit. SM's first variation
# makes minute 11 a commit in cycles 3, 6, 9, 12 and 15 as well, and a hand
# edit in the other nine. Rowan is away during cycle 6's minute 11, so that
# commit never happens, which leaves the five commits SM counts.
COMMIT_CYCLES = {1, 3, 6, 9, 12, 15}
DRINK = 6
LEFT, BACK = at("15:06:40"), at("15:12:40")
rand = tl.Rand("6.1")


def start(k):
    return T0 + (k - 1) * CYCLE_MS


def off(mss):
    """Milliseconds of an "M:SS.f" offset into a cycle."""
    m, s = mss.split(":")
    return int(m) * 60_000 + round(float(s) * 1000)


# Input. macOS reports key presses apart from other input (the key-down
# idle time, SM 1.2), so presses and pointer moves differ in the samples.
# No input falls on a slot boundary (README.md one level up).


def press(t):
    if t % tl.SLOT_MS == 0:
        t += 100
    fx.inputs.append((t, True))


def move(t):
    if t % tl.SLOT_MS == 0:
        t += 100
    fx.inputs.append((t, False))


def presses(a, b, lo, hi):
    """Key presses from a to b, lo to hi ms apart."""
    t = a
    while t <= b:
        press(t)
        t += rand.between(lo, hi)


def moves(a, b, lo, hi):
    t = a
    while t <= b:
        move(t)
        t += rand.between(lo, hi)


# Events.


def focus(t, app_class, app, project=None):
    data = {"app_class": app_class, "app_hash": app}
    if app == ITERM and project:
        data["hosted_sessions"] = [S]
    if project:
        data["focus_project"] = project
    return fx.emit(t, "os.focus", data, source="os", via="os", sample=True)


heartbeats = []  # (t, file, save), for the check of WakaTime's rule below


def heartbeat(t, act, file, lines=0, save=False):
    # WakaTime's VS Code plugin through the dual-send (DESIGN 5.6). The
    # bridge maps a heartbeat with changed lines or a save to an edit and
    # any other to navigation, both with edit_source unknown (DESIGN 5.7).
    # macOS skips the peer-process check, so these arrive via editor.
    heartbeats.append((t, file, save))
    return fx.emit(t, "editor.activity",
                   {"editor": "vscode", "act": act, "file_hash": file, "lines_changed": lines,
                    "edit_source": "unknown"},
                   source="editor", via="editor", project=P)


def open_file():
    """The file VS Code has open: the last heartbeat's."""
    return heartbeats[-1][1]


def another_file():
    """A file he opens, one of the eleven other than the one open."""
    others = [f for f in FILES if f != open_file()]
    return others[rand.below(len(others))]


def prompt(t, turn):
    return fx.emit(t, "prompt.submit",
                   {"turn_key": turn, "len_chars": 300, "pasted_chars": 0, "typed_len_est": 300,
                    "origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
                    "low_effort": False},
                   source="hook", via="hook", sample=True, **CC)


def output(t, turn, words, final=False):
    # Assistant text comes from the transcript tailer, as in 6.4.
    return fx.emit(t, "agent.output",
                   {"turn_key": turn, "words": words, "code_lines": 0, "is_final": final,
                    "asks_question": False, "offers_options": False},
                   source="log", via="log", **CC)


def tool(t_end, cls, *, t_start, files=None, added=0, removed=0, exit_code=None, error=None, visible=0):
    data = {"tool_class": cls}
    if files:
        data["files"] = files
    data["lines_added"] = added
    data["lines_removed"] = removed
    if cls == "test":
        data["cmd_hash"] = CMD_TEST
    if exit_code is not None:
        data["exit_code"] = exit_code
    if error:
        data["error_sig"] = error
    data["visible_output_lines"] = visible
    data["t_start"] = t_start
    data["t_end"] = t_end
    return fx.emit(t_end, "agent.tool", data, source="hook", via="hook", sample=True, **CC)


def permission(requested, answered):
    # Written at the answer, so the anchor's time is the event's time, as for
    # every other anchor; requested_at is the PermissionRequest hook's.
    return fx.emit(answered, "agent.permission",
                   {"requested_at": requested, "answered_at": answered, "decision": "accept",
                    "decided_by": "user"},
                   source="hook", via="hook", sample=True, **CC)


def stop(t, turn):
    return fx.emit(t, "agent.stop", {"turn_key": turn, "stop_reason": "end_turn", "stop_hook_active": False},
                   source="hook", via="hook", sample=True, **CC)


commits = []


def commit(t, ins, dels, files):
    n = len(commits)
    commits.append(t)
    return fx.emit(t, "git.change",
                   {"op": "commit", "sha": tl.git_sha(f"6.1 commit {n}"),
                    "parents": [tl.git_sha(f"6.1 commit {n - 1}" if n else "6.1 base")],
                    "files_changed": files, "insertions": ins, "deletions": dels, "actor": "human"},
                   source="git", via="git", project=P)


# Before the window: VS Code in front, then Claude Code started in iTerm,
# then back to VS Code a quarter of a minute before 14:00. These records set
# up the session and the editor's project and aren't checked.
fx.ticks("13:57:00", "17:00:45", tl.SLOT_MS)
move(at("13:56:58.900"))
focus(at("13:56:59.000"), "editor", CODE, P)
move(at("13:57:04.800"))
heartbeat(at("13:57:05.000"), "navigate", FILES[0])
presses(at("13:57:07.100"), at("13:57:57.000"), 2_500, 6_000)
press(at("13:57:59.900"))
focus(at("13:58:00.000"), "terminal", ITERM)
fx.typing("13:58:01.000", "13:58:04.600", 600)  # claude, Enter
fx.emit(at("13:58:05.500"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "default"},
        source="hook", via="hook", sample=True, **CC)
focus(at("13:58:05.500"), "terminal", ITERM, P)
move(at("13:58:41.200"))
move(at("13:59:20.700"))
press(at("13:59:44.900"))
focus(at("13:59:45.000"), "editor", CODE, P)
presses(at("13:59:46.300"), at("13:59:58.000"), 1_800, 4_200)

edits_since_commit = {"ins": 0, "dels": 0, "files": set()}
failing = []
green = []


def cycle(k):
    C = start(k)

    def c(mss):
        return C + off(mss)

    commit_cycle = k in COMMIT_CYCLES
    turn = TURN[k]

    # 0:00 to 2:00, VS Code. He moves through the explorer and quick open
    # with the keyboard; a file opens at 0:30 and another at 1:50, and
    # WakaTime sends a heartbeat when the open file changes, at most one in
    # two minutes otherwise, so those are the minutes' only heartbeats.
    if k == DRINK + 1:
        # Back from the drink: the first thing he does is click in VS Code.
        move(BACK)
        focus(BACK, "editor", CODE, P)
        heartbeat(BACK, "navigate", another_file())
        presses(BACK + 1_300, c("1:46.0"), 2_500, 6_000)
    else:
        if k > 1 and (k - 1) in COMMIT_CYCLES:
            press(C - 100)  # Cmd-Tab from iTerm
            focus(C, "editor", CODE, P)
        presses(c("0:01.2"), c("0:28.0"), 1_800, 4_200)
        press(c("0:29.6"))
        heartbeat(c("0:30"), "navigate", another_file())
        presses(c("0:32.4"), c("1:46.0"), 2_500, 6_000)
    press(c("1:47.2"))
    presses(c("1:47.8"), c("1:49.3"), 400, 600)
    press(c("1:49.6"))
    heartbeat(c("1:50"), "navigate", another_file())
    press(c("1:53.1"))
    press(c("1:56.4"))

    # 2:00 to 4:00, iTerm: types the 300-character prompt and sends it.
    press(c("1:59.9"))
    focus(c("2:00"), "terminal", ITERM, P)
    presses(c("2:00.8"), c("3:59.4"), 300, 500)
    press(c("3:59.8"))
    prompt(c("4:00"), turn)

    # 4:00 to 6:30, the agent's turn, which he watches.
    output(c("4:30"), turn, 45)
    move(c("4:33.4"))
    output(c("5:00"), turn, 45)
    move(c("5:02.6"))
    press(c("5:24.6"))  # picks Yes in the permission dialog
    permission(c("5:10"), c("5:25"))
    a1 = rand.between(16, 24)
    f1 = FILES[rand.below(len(FILES))]
    tool(c("5:30"), "edit", t_start=c("5:10"), files=[f1], added=a1, removed=30 - a1)
    if k == 9:
        failing.append(tool(c("5:50"), "test", t_start=c("5:32"), exit_code=1, error=ASSERTION, visible=4))
    move(c("5:41.2"))
    a2 = rand.between(16, 24)
    f2 = FILES[rand.below(len(FILES))]
    tool(c("6:00"), "edit", t_start=c("5:59.6"), files=[f2], added=a2, removed=30 - a2)
    if k in (9, 10):
        failing.append(tool(c("6:20"), "test", t_start=c("6:02"), exit_code=1, error=ASSERTION, visible=4))
    if k == 11:
        green.append(tool(c("6:20"), "test", t_start=c("6:02"), exit_code=0, visible=4))
    move(c("6:09.8"))
    output(c("6:30"), turn, 160, final=True)
    stop(c("6:30"), turn)
    edits_since_commit["ins"] += a1 + a2
    edits_since_commit["dels"] += 60 - a1 - a2
    edits_since_commit["files"].update([f1, f2])

    if k == DRINK:
        # Ten seconds after the stop he gets a drink.
        move(c("6:34.5"))
        move(LEFT)
        return

    # 6:30 to 9:00, reads the summary and scrolls back through both diffs.
    moves(c("6:33.0"), c("8:56.0"), 5_000, 9_000)
    move(c("8:59.9"))  # clicks the browser tab

    # 9:00 to 11:00, plays the change in the browser on localhost.
    focus(c("9:00"), "browser", BROWSER)
    presses(c("9:01.5"), c("10:58.5"), 700, 2_200)
    press(c("10:59.9"))  # Cmd-Tab

    # 11:00 to 12:00, a commit from iTerm or a small hand edit in VS Code,
    # finished at 11:40 either way.
    if commit_cycle:
        focus(c("11:00"), "terminal", ITERM, P)
        presses(c("11:01.0"), c("11:39.4"), 450, 750)  # git add -p, then git commit
        press(c("11:39.8"))
        commit(c("11:40"), edits_since_commit["ins"], edits_since_commit["dels"],
               len(edits_since_commit["files"]))
        edits_since_commit.update(ins=0, dels=0, files=set())
    else:
        # The edit is in the file open since 1:50, so VS Code changes no file.
        focus(c("11:00"), "editor", CODE, P)
        presses(c("11:02.0"), c("11:39.2"), 500, 900)
        # His first key opens a new line. It is an editor event 552 s after
        # the 1:50 heartbeat, so WakaTime sends a heartbeat with that one
        # changed line (HC 1.5) in the same millisecond, and the bridge makes
        # it an edit (DESIGN 5.7). The save carries the other two.
        heartbeat(c("11:02.0"), "edit", open_file(), lines=1)
        press(c("11:39.7"))  # Cmd-S
        # A number drawn and dropped. Other fixtures copy this one's harness
        # events by time and data, and the draws after this one set the
        # agent's files and line counts, so they keep their place.
        rand.below(len(FILES))
        hand = open_file()
        heartbeat(c("11:40"), "edit", hand, lines=2, save=True)
        edits_since_commit["ins"] += 2
        edits_since_commit["dels"] += 1
        edits_since_commit["files"].add(hand)
        press(c("11:44.6"))
        press(c("11:51.2"))
        press(c("11:57.8"))


for k in range(1, CYCLES + 1):
    cycle(k)

# After the window: a sixteenth cycle starts at 17:00, and its first
# heartbeat at 17:00:30 is the NAV whose lead credits 16:59:40 to 17:00:00.
press(at("16:59:59.900"))
focus(at("17:00:00.000"), "editor", CODE, P)
presses(at("17:00:01.200"), at("17:00:28.000"), 1_800, 4_200)
press(at("17:00:29.600"))
heartbeat(at("17:00:30.000"), "navigate", another_file())
press(at("17:00:33.100"))
press(at("17:00:37.500"))
press(at("17:00:42.200"))

events = fx.build()


# Checks of the timing choices against SM's rows (README.md).

def keys_in(a, b):
    return any(k and a <= t < b for t, k in fx.inputs)


def input_in(a, b):
    return any(a <= t < b for t, _ in fx.inputs)


tl.check(all(t % tl.SLOT_MS for t, _ in fx.inputs), "no input on a slot boundary")
# Presence: every silence in the window is at most PRESENCE_GAP_S = 120 s,
# so every slot is yes or bridged, except the drink: no input from his last
# touch at 15:06:40 to the click at 15:12:40.
times = sorted(t for t, _ in fx.inputs)
for a, b in zip(times, times[1:]):
    if b <= T0 or a >= END:
        continue
    if (a, b) == (LEFT, BACK):
        continue
    tl.check(b - a <= 120_000, f"silence from {fx.hhmmss(a)} to {fx.hhmmss(b)} is at most 120 s")
tl.check(LEFT in times and BACK in times and not input_in(LEFT + 1, BACK), "the drink: 15:06:40 to 15:12:40")
tl.check(BACK - LEFT == 360_000, "the silent run is 6 minutes long")

# WakaTime's VS Code plugin sends a heartbeat when the file changes, on a
# save, or on an editor event more than 120 s after the last heartbeat
# (HC 1.5), and only from the window in front (SM 3.2).
for (a, fa, _), (b, fb, save) in zip(heartbeats, heartbeats[1:]):
    tl.check(fb != fa or save or b - a > 120_000,
             f"the heartbeat at {fx.hhmmss(b)} changes file, saves or comes over 120 s after {fx.hhmmss(a)}")
for t, _, _ in heartbeats:
    front = [e for e in events if e["kind"] == "os.focus" and e["t"] <= t][-1]
    tl.check(front["data"]["app_hash"] == CODE, f"VS Code is in front at the heartbeat at {fx.hhmmss(t)}")

prompts_at = [start(k) + off("4:00") for k in range(1, CYCLES + 1)]
for k in range(1, CYCLES + 1):
    C = start(k)
    sub = C + off("4:00")
    # The typing part of the prompt's lead, from 1:50, needs key presses in
    # each of its slots; rule 6 needs input in the 10 s before the submit.
    for s in range(C + off("1:45"), sub, tl.SLOT_MS):
        tl.check(keys_in(s, s + tl.SLOT_MS), f"cycle {k}: key presses in slot {fx.hhmmss(s)}")
    tl.check(keys_in(sub - 10_000, sub), f"cycle {k}: key presses in the 10 s before the submit")
    # The lookup: the browser holds focus from 9:00 to 11:00, 120 s, under
    # LOOKUP_MAX_S = 180 s, after the agent stopped.
    if k != DRINK:
        f = [e for e in events if e["kind"] == "os.focus" and C + off("8:00") <= e["t"] <= C + off("11:00")]
        tl.check([(e["t"] - C, e["data"]["app_class"]) for e in f]
                 == [(off("9:00"), "browser"), (off("11:00"), "terminal" if k in COMMIT_CYCLES else "editor")],
                 f"cycle {k}: the browser run is 9:00 to 11:00")
    # Minute 11 of a hand-edit cycle is E 40 and NAV 20: from 11:00 the next
    # anchor is one of the edit's two heartbeats until the save at 11:40,
    # then the next cycle's first heartbeat, a navigation (SM 3.3 rule 3).
    if k != DRINK and k not in COMMIT_CYCLES:
        nxt = [(e["t"] - C, e["data"].get("act", e["kind"])) for e in events if C + off("11:00") <= e["t"] <= C + off("12:30")
               and e["kind"] in ("editor.activity", "prompt.submit", "agent.permission", "git.change")]
        tl.check(nxt == [(off("11:02"), "edit"), (off("11:40"), "edit"), (off("12:30"), "navigate")],
                 f"cycle {k}: the hand edit's heartbeats at 11:02 and 11:40, then a navigation at 12:30")

# Level A: every editor, permission and commit anchor has key presses in
# its slot. A prompt's evidence is the 10 s before it, checked above.
anchor_events = [e for e in events if e["kind"] in ("editor.activity", "agent.permission", "git.change")]
for e in anchor_events:
    s = e["t"] - e["t"] % tl.SLOT_MS
    tl.check(keys_in(s, s + tl.SLOT_MS), f"key presses in the slot of the {e['kind']} at {fx.hhmmss(e['t'])}")

tl.check(len(prompts_at) == 15 and sum(e["kind"] == "agent.permission" for e in events) == 15,
         "fifteen typed prompts and fifteen permission decisions")
tl.check([fx.hhmmss(t) for t in commits] == ["14:11:40", "14:35:40", "15:47:40", "16:23:40", "16:59:40"],
         "five commits")
tl.check(len(failing) == 3 and len(green) == 1, "three failing test runs, then a green one")
tl.check(all(e[2]["session_key"] == S for e in failing + green), "the agent runs the tests")

# The reading bucket, per turn: reset to its 20 s base at the submit, no
# drain while the permission answer's window covers the second (14:05:10 to
# 14:05:35), and then, second by second, exactly as SM's rows say: empty
# from 4:20 to 4:30 and from 4:45 to 5:00 (X.agent_only), never empty while
# SM credits SUP or READ.
fills = [(e["t"], e["kind"], e["data"]) for e in events if e["kind"] in ("agent.output", "agent.tool")]
for k in range(1, CYCLES + 1):
    C = start(k)
    bucket = tl.Bucket()
    bucket.reset(C + off("4:00"), tl.Bucket.BASE_S)
    todo = [(t, bucket.amount(kind, d)) for t, kind, d in fills if C + off("4:00") <= t <= C + off("6:30")]
    end = LEFT + 30_000 if k == DRINK else C + off("9:00")
    for tau in range(C + off("4:00"), end, 1000):
        while todo and todo[0][0] <= tau:
            bucket.fill(*todo.pop(0))
        if C + off("5:10") <= tau < C + off("5:35"):
            continue
        idle = C + off("4:20") <= tau < C + off("4:30") or C + off("4:45") <= tau < C + off("5:00")
        got = bucket.drain(1)
        tl.check(got == (0 if idle else 1), f"cycle {k}: the bucket at {fx.hhmmss(tau)}")
    if k == 1:
        tl.check(int(bucket.total()) == 303, f"the bucket holds 303 s at 14:09:00, not {float(bucket.total())}")

# Hackatime's replay of the window (SM 5.3, 5.4): every editor heartbeat and
# agent event for logged_s, the agent events alone for ai_logged_s.
AI_KINDS = ("prompt.submit", "agent.tool", "agent.stop")
in_window = [e for e in events if T0 <= e["t"] < END]
ai_times = [e["t"] for e in in_window if e["kind"] in AI_KINDS]
all_times = sorted(ai_times + [e["t"] for e in in_window if e["kind"] == "editor.activity"])
ai_logged = tl.hackatime_replay(ai_times)
logged = tl.hackatime_replay(all_times)
handbook = logged - ai_logged + ai_logged / 3
tl.check(ai_logged == 4050 - 120, f"ai_logged_s replays to {ai_logged}")
tl.check(logged == 7712, f"logged_s replays to {logged}")
# Hackatime's boundary-aware path, which Stardance reads and DESIGN D15
# follows, also takes the last record before the window (HC 1.4): the
# 13:57:05 heartbeat, 205 s before the 14:00:30 one. No agent event comes
# before the window, so only logged_s would change.
before = [e["t"] for e in events if e["t"] < T0 and (e["kind"] in AI_KINDS or e["kind"] == "editor.activity")]
logged_boundary = tl.hackatime_replay(before[-1:] + all_times)
tl.check(fx.hhmmss(before[-1]) == "13:57:05" and logged_boundary == logged + 120,
         f"the boundary-aware logged_s is {logged_boundary}")

# The rows: cycle 1 is SM's table; the other fourteen cycles have its shape
# with SM's three variations.
SHAPE = [
    ({"NAV": 60}, {}, 0),
    ({"NAV": 50, "P": 10}, {}, 0),
    ({"P": 60}, {}, 0),
    ({"P": 60}, {}, 0),
    ({"SUP": 35}, {"X.agent_only": 25}, 60),
    ({"PA": 25, "SUP": 35}, {}, 60),
    ({"SUP": 30, "READ": 30}, {}, 30),
    ({"READ": 60}, {}, 0),
    ({"READ": 60}, {}, 0),
    ({"LOOKUP": 60}, {}, 0),
    ({"LOOKUP": 60}, {}, 0),
]
DERIVED = ["credited_s", "credit", "uncredited"]
SAME_SHAPE = "the other fourteen cycles are the same shape"
DRINK_SM = "the drink: the tail credits reading until 15:07:10, then X.absent to 15:12:40, 290 s in cycle 6 and 40 s in cycle 7"

exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 6.1", evidence="A", witness="witnessed",
                  projects={P: "cs2go"}, start="14:00:00", end="17:00:00")
for k in range(1, CYCLES + 1):
    C = start(k)
    for m in range(12):
        hhmm = fx.hhmm(C + m * 60_000)
        cycle1 = f"14:{m:02d}"
        sm = cycle1 if k == 1 else f"{cycle1}; {SAME_SHAPE}"
        derived = None if k == 1 else DERIVED
        note = None
        if k == DRINK and m == 7:
            exp.row(hhmm, credit={P: {"READ": 10}}, uncredited={"X.absent": 50}, sm=DRINK_SM, derived=DERIVED,
                    erratum=EDGES, note="His last input is at 15:06:40, so the tail ends at 15:07:10.")
            continue
        if k == DRINK and m == 8:
            exp.row(hhmm, fx.hhmm(C + 12 * 60_000), bucket=False, codes=["X.absent"], sm=DRINK_SM,
                    derived=["bucket"], erratum=EDGES,
                    note="Nothing is credited and no agent runs, so these minutes have no bucket (SM 5.1), "
                         "and their 240 s of X.absent are in no bucket either. Cycle 6's commit, which SM's "
                         "first variation puts at 15:11:40, never happens: he is away.")
            continue
        if k == DRINK and m > 8:
            continue
        if k == DRINK + 1 and m == 0:
            exp.row(hhmm, credit={P: {"NAV": 20}}, uncredited={"X.absent": 40}, sm=DRINK_SM, derived=DERIVED,
                    erratum=EDGES,
                    note="Back at 15:12:40 with a click in VS Code, a NAV anchor that doesn't qualify for the "
                         "return rule. The lead of the 15:13:50 heartbeat credits 15:12:40 to 15:13:00.")
            continue
        if m < 11:
            credit, unc, agent = SHAPE[m]
            if k == 1 and m == 8:
                note = "SM: the bucket still holds 303 s at the end of the minute (303 1/3 s: 553 1/3 filled, 250 drained)."
            if k == 9 and m == 5:
                note = ("The agent's test run fails at 15:41:50 with the AssertionError. Its 4 lines add 30 s to "
                        "a bucket that never runs dry here, so the row keeps cycle 1's shape.")
            if k == 9 and m == 6:
                note = "A second failing run at 15:42:20, the same AssertionError."
            if k == 10 and m == 6:
                note = "A third failing run at 15:54:20, the same AssertionError, after the agent tried his hypothesis."
            if k == 11 and m == 6:
                note = "The test run at 16:06:20 passes: a run_green, after his own edit, saved at 15:59:40."
            exp.row(hhmm, credit={P: credit}, uncredited=unc, agent=agent, sm=sm, derived=derived, note=note)
            continue
        if k in COMMIT_CYCLES:
            exp.row(hhmm, credit={P: {"G": 40, "NAV": 20}}, uncredited={},
                    sm="14:11" if k == 1 else "14:11; minute 11 is a commit in cycles 3, 6, 9, 12 and 15",
                    derived=derived)
        else:
            exp.row(hhmm, credit={P: {"E": 40, "NAV": 20}}, uncredited={},
                    sm="minute 11 of the other cycles is a small hand edit in VS Code, which credits the same "
                       "60 s as E and NAV",
                    derived=DERIVED,
                    note="The hand edit's heartbeats are at his first keystroke, 14:23:02, and at the save, 14:23:40, "
                         "where cycle 1's commit is." if k == 2 else
                    ("His own edit fixes the failing test." if k == 10 else None))

# Totals SM states, checked against the sums of the rows by Expected.doc.
rows_by_reason = exp.totals()["credited_by_reason"]
tl.check(sum(rows_by_reason.get(r, 0) for r in ("P", "PA", "SUP", "READ")) == 5965, "AI-assisted 5,965 s")
tl.check(sum(rows_by_reason.get(r, 0) for r in ("NAV", "E", "G", "LOOKUP")) == 4130, "hand work 4,130 s")
first = sum(sum(sum(v.values()) for v in r["credit"].values()) for r in exp.rows[:12])
tl.check(first == 695, f"cycle 1 credits {first} s")
tl.check(10095 + 375 + 330 == 10800, "credited, agent-only and absent seconds fill the wall time")

expected = exp.doc(
    extra_totals={
        "wall_s": 10800,
        "credited_s": 10095,
        "ai_credited_s": 5965,
        "hand_credited_s": 4130,
        "agent_only_s": 375,
        "absent_s": 330,
        "first_cycle_credited_s": 695,
    },
    stated=["wall_s", "credited_s", "ai_credited_s", "hand_credited_s", "agent_only_s", "absent_s",
            "first_cycle_credited_s"],
    approx={
        "logged_s": {"about": 8300, "sm": "Hackatime-equivalent logged_s: about 8,300"},
        "ai_logged_s": {"about": 4050,
                        "sm": "of which ai_logged_s: about 4,050, each run plus the 120 s gap credit before "
                              "each prompt, 270 s a cycle"},
        "handbook_s": {"about": 5600, "sm": "handbook_s: about 5,600, 1 h 33 min"},
        "ai_ratio": {"about": 1.47, "sm": "ai_ratio is 5,965 / 4,050 = 1.47"},
    },
    errata=[EDGES, SAME_ERROR, REPLAY],
    notes=[
        "ai_credited_s is P + PA + SUP + READ and hand_credited_s is NAV + E + G + LOOKUP, as SM's table "
        "splits them; no credited second inside an agent turn has a hand-work reason (SM 5.4).",
        "absent_s is SM's X.absent total, the drink's 330 s. The rows' X.absent sums to 90 (50 in 15:07, 40 "
        "in 15:12): the other 240 s fall in 15:08 to 15:11, which have no bucket. See the presence-edges "
        "erratum.",
        "agent_only_s is the 25 s a cycle that SM's 14:04 row calls X.agent_only. agent_running_s, 150 s a "
        "turn from submit to stop, follows from SM's times.",
        "Commits: cycle 1's at 14:11:40 and cycles 3, 9, 12 and 15's, five in all. Cycle 6's would be at "
        "15:11:40, during the drink.",
        "Cycles 9 and 10: the agent's test run fails at 15:41:50, 15:42:20 and 15:54:20 with one "
        "AssertionError signature, and passes at 16:06:20, after his hand edit, saved at 15:59:40. Nothing is "
        "discounted. See the same-error erratum for why SM's reason doesn't apply.",
        f"This fixture's events replay to logged_s {logged:g}, ai_logged_s {ai_logged:g} and handbook_s "
        f"{handbook:g} under min(gap, 120 s) over the window; SM says about 8,300, 4,050 and 5,600. The "
        "first prompt has no agent event within 120 s before it in the window, which is the 120 s off SM's "
        "ai_logged_s, and VS Code sends only the heartbeats SM names, plus one at the first keystroke of each "
        "hand edit. Hackatime's boundary-aware path would also count the 13:57:05 heartbeat before the window, "
        f"for logged_s {logged_boundary:g}. See the Hackatime-replay erratum.",
    ],
)

# The acts SM names, in time order. Outside cycle 1, sm names the cycle-1
# row the act copies.
anchors = []
turns = []
prompt_entries = []
for k in range(1, CYCLES + 1):
    C = start(k)

    def shaped(text):
        return text if k == 1 else f"{text}; {SAME_SHAPE}"

    if k == DRINK + 1:
        click = fx.event_at("editor.activity", BACK)
        anchors.append(tl.anchor(fx, click, kind="editor_navigation", lead_s=120, trail_s=0, lead_from="params",
                                 level="A", sm="the drink: back at 15:12:40, the first thing he does is click "
                                               "in VS Code; its first anchor is NAV"))
    else:
        nav = fx.event_at("editor.activity", C + off("0:30"))
        anchors.append(tl.anchor(fx, nav, kind="editor_navigation", lead_s=120, trail_s=0, lead_from="params",
                                 level="A",
                                 sm=shaped("14:00: heartbeats at 14:00:30 and 14:01:50 become NAV anchors")))
    nav = fx.event_at("editor.activity", C + off("1:50"))
    anchors.append(tl.anchor(fx, nav, kind="editor_navigation", lead_s=120, trail_s=0, lead_from="params",
                             level="A", sm=shaped("14:01: from 14:01:50 the lead of his next prompt takes over")))
    sub = fx.event_at("prompt.submit", C + off("4:00"))
    anchors.append(tl.anchor(fx, sub, kind="substantive_prompt", origin="human_typed", prompt_class="substantive",
                             lead_s=320, trail_s=0, lead_from="sm", level="A",
                             sm=shaped("14:03: sends at 14:04:00; its lead is 120 + 300 / 1.5 = 320 s")))
    pa = fx.event_at("agent.permission", C + off("5:25"))
    anchors.append(tl.anchor(fx, pa, kind="permission_answer", lead_s=15, trail_s=10, lead_from="params",
                             level="A", sm=shaped("14:05: permission request at 14:05:10, accepted at 14:05:25; "
                                                  "the wait and the 10 s trail are PA 25")))
    if k == DRINK:
        pass
    elif k in COMMIT_CYCLES:
        g = fx.event_at("git.change", C + off("11:40"))
        anchors.append(tl.anchor(fx, g, kind="human_commit", lead_s=60, trail_s=0, lead_from="sm", level="A",
                                 sm="14:11: commit at 14:11:40 (G, lead 60 s)" if k == 1 else
                                 "14:11; minute 11 is a commit in cycles 3, 6, 9, 12 and 15"))
    else:
        for mss, part in (("11:02", "his first keystroke"), ("11:40", "the save")):
            e = fx.event_at("editor.activity", C + off(mss))
            anchors.append(tl.anchor(fx, e, kind="typed_edit", lead_s=120, trail_s=0, lead_from="params", level="A",
                                     sm="minute 11 of the other cycles is a small hand edit in VS Code, crediting "
                                        f"E and NAV; the heartbeat at {part}"))
    turns.append(tl.turn(fx, C + off("4:00"), C + off("6:30"), session=S, turn_key=TURN[k], origin="human_typed",
                         prompt_class="substantive",
                         sm=shaped("14:04 to 14:06: the agent starts at 14:04:00 and stops at 14:06:30")))
    source = "SM 6.1 gives each cycle's prompt 300 characters and quotes none"
    if k == 10:
        source = ("SM 6.1 quotes the hypothesis Rowan answers the failing test with, \"the hitbox updates before "
                  "the position in tick(), swap them\", as part of a 300-character prompt, not the prompt itself")
    prompt_entries.append({"turn_key": TURN[k], "t": C + off("4:00"), "local": fx.hhmmss(C + off("4:00")),
                           "text": None, "len_chars": 300, "source": source})
last = fx.event_at("editor.activity", at("17:00:30"))
anchors.append(tl.anchor(fx, last, kind="editor_navigation", lead_s=120, trail_s=0, lead_from="params", level="A",
                         sm="14:11: then the lead of the next cycle's first NAV, which for cycle 15 comes after "
                            "the window"))

anchor_doc = {"fixture": "6.1", "anchors": anchors, "turns": turns}
prompt_doc = {"fixture": "6.1", "prompts": prompt_entries}

fx.write(tl.out_dir(HERE), expected, anchor_doc, prompt_doc)
