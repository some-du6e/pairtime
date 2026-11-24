# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.5, an hour of dark-flow re-rolling. See README.md in this
directory. Besides the four data files it writes hunks.json, the edit
hunks agent.tool has no field for yet (README.md, "The missing field")."""

import json
import os
import sys
from fractions import Fraction

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
sys.path.insert(0, HERE)

import neardup  # noqa: E402
import timeline as tl  # noqa: E402

THINK_LEAD_S, COMPOSE_CPS, LOW_LEAD_S, EDIT_LEAD_S, COMMIT_LEAD_S = 120, Fraction(3, 2), 20, 120, 60

fx = tl.Fixture(
    "6.5",
    title="An hour of dark-flow re-rolling",
    date="2026-11-25",
    timezone_name="Asia/Manila",
    utc_offset_min=480,
    os_name="windows",
    keys=False,
    device=tl.key("d", "6.5 alex laptop"),
    boot_id=tl.key("b", "6.5 boot"),
    boot="16:41:09.000",
    seq0=23817,
    # He was in the browser before the fixture starts.
    last_input="19:57:21.600",
)
at = fx.at
P = tl.key("p", "pantry-app")
S = tl.key("s", "6.5 claude session")
NAVBAR, HERO, FOOTER = (tl.key("f", f"6.5 src/{n}.css") for n in ("Navbar", "Hero", "Footer"))
TERMINAL, BROWSER, CODE = (tl.key("a", n) for n in ("6.5 WindowsTerminal.exe", "6.5 chrome.exe", "6.5 Code.exe"))
CC = dict(harness="claude_code", version="2.1.328", session=S, project=P)
TURN = [None] + [tl.key("t", f"6.5 turn {i}") for i in range(1, 23)]

LOOP_TEXT = "still overlapping on mobile. fix it"
INVENTED = "invented: SM says the prompts from 20:09 to 20:37 are mostly in new words and quotes three"
REPEAT = "invented: one of the prompts in that range whose wording repeats, here T2's text again"
QUOTED = "quoted in SM 6.5's 20:08 to 20:37 row"
# (turn, sent, text, source).
PROMPTS = [
    (1, "20:01:00", "the navbar overlaps the hero on mobile, can you fix the css so the hero starts below the navbar",
     "quoted in SM 6.5's 20:00 row"),
    (2, "20:03:00", LOOP_TEXT, "quoted in SM 6.5's 20:02 row"),
    (3, "20:05:00", LOOP_TEXT, "SM 6.5's 20:05 row: the identical prompt"),
    (4, "20:07:00", "still overlapping on mobile, fix it!!", "quoted in SM 6.5's 20:07 row"),
    (5, "20:09:00", "the navbar STILL covers the hero", QUOTED),
    (6, "20:11:00", "can you make the hero start under the nav bar on phones", INVENTED),
    (7, "20:13:00", LOOP_TEXT, REPEAT),
    (8, "20:15:00", "pls fix the overlap", QUOTED),
    (9, "20:17:00", "the top of the hero is hidden behind the navbar", INVENTED),
    (10, "20:19:00", "it's not working, try again", QUOTED),
    (11, "20:21:00", "why is the hero still under the navbar on my phone", INVENTED),
    (12, "20:23:00", LOOP_TEXT, REPEAT),
    (13, "20:25:00", "push the hero down so the navbar doesn't cover it", INVENTED),
    (14, "20:27:00", "nope, same problem on mobile", INVENTED),
    (15, "20:29:00", "make sure nothing is behind the navbar on small screens", INVENTED),
    (16, "20:31:00", "still overlapping on mobile, fix it", REPEAT),
    (17, "20:33:00", "the hero text is cut off by the navbar again", INVENTED),
    (18, "20:35:00", "try a different way to stop the overlap", INVENTED),
    (19, "20:37:00", "the navbar is on top of the hero, fix the spacing", INVENTED),
    (20, "20:43:00", "now the hero is right on mobile but there's a big gap on desktop. only apply the margin under 768px",
     "quoted in SM 6.5's 20:42 row"),
    (21, "20:49:00", "the gap is gone. give the footer the same breakpoint so they line up",
     "invented: one of the two turns in SM 6.5's 20:43 to 20:59 row"),
    (22, "21:00:30", "next thing: the pantry cards overflow on small phones, fix that",
     "invented: the next request, sent after the window; its lead covers 20:58 to 21:00"),
]
SENT = {k: at(t) for k, t, _, _ in PROMPTS}
TEXT = {k: text for k, _, text, _ in PROMPTS}
LEN = {k: len(text) for k, _, text, _ in PROMPTS}
LOOP = range(3, 20)  # T3 to T19, the discounted turns

# Each turn's agent run: (stop, [(time, what, ...)]). T1 stops at 20:02:00
# and T19 at 20:38:00 (SM); T2 stops at 20:04:00, so its last 20 s are SUP
# and the 20:04:00 browser check is a lookup with no agent running. T3 to T18
# run 75 s, into the next minute. Every turn rewrites Navbar.css in one
# edit: lines 10-21 in T1, 12-19 after it (SM 6.5).
RUNS = {}
RUNS[1] = (at("20:02:00"), [("20:01:12", "read", NAVBAR), ("20:01:35", "edit", NAVBAR, 12, 12, (10, 12, 10, 12)),
                            ("20:01:58", "say", 110)])
RUNS[2] = (at("20:04:00"), [("20:03:12", "read", NAVBAR), ("20:03:40", "edit", NAVBAR, 8, 8, (12, 8, 12, 8)),
                            ("20:03:59", "say", 110)])
for k in range(3, 19):
    t = SENT[k]
    RUNS[k] = (t + 75_000, [(fx.hhmmss(t + 10_000), "read", NAVBAR),
                            (fx.hhmmss(t + 40_000), "edit", NAVBAR, 8, 8, (12, 8, 12, 8)),
                            (fx.hhmmss(t + 73_000), "say", 110)])
RUNS[19] = (at("20:38:00"), [("20:37:10", "read", NAVBAR), ("20:37:40", "edit", NAVBAR, 8, 8, (12, 8, 12, 8)),
                             ("20:37:58", "say", 110)])
# The two turns after his fix add new lines.
RUNS[20] = (at("20:45:00"), [("20:43:30", "read", HERO), ("20:44:00", "edit", HERO, 12, 0, (40, 0, 41, 12)),
                             ("20:44:58", "say", 96)])
RUNS[21] = (at("20:51:00"), [("20:49:35", "read", FOOTER), ("20:50:00", "edit", FOOTER, 18, 0, (22, 0, 23, 18)),
                             ("20:50:58", "say", 180)])

# Input. Windows without the key counter reports any input only, so key
# presses and pointer moves differ only in this script.
fx.pointer("19:57:34.800")                         # clicks the terminal
fx.typing("19:57:38.000", "19:57:41.200", 600)     # "claude", Enter
fx.pointer_every("19:57:50.300", "19:59:50.300", 10_000)  # scrolls back through the CSS
fx.typing("19:59:56.400", "20:00:59.600", 650)     # types T1
fx.pointer_every("20:01:06.200", "20:01:56.200", 10_000)  # watches T1
fx.pointer_every("20:02:03.100", "20:02:23.100", 10_000)  # reads its answer
fx.pointer("20:02:29.900", "20:02:36.500", "20:02:44.200", "20:02:49.900")  # the phone view, and back
fx.typing("20:02:50.400", "20:02:59.600", 280)     # types T2
fx.pointer_every("20:03:05.400", "20:03:55.400", 10_000)  # watches T2
fx.pointer("20:04:00.200", "20:04:08.600", "20:04:19.300", "20:04:30.200")  # the phone view, and back
fx.pointer("20:04:33.000", "20:04:41.700")         # reads T2's answer
fx.typing("20:04:45.000", "20:04:59.600", 420)     # types T3


def loop_input(k):
    """A loop cycle after T_k: he watches the agent, checks the phone view
    from 80 s to 95 s after the prompt, and types the next prompt in the
    last 20 s."""
    t = SENT[k]
    fx.pointer_every(fx.hhmmss(t + 5_300), fx.hhmmss(t + 75_300), 10_000)
    fx.pointer(fx.hhmmss(t + 79_900), fx.hhmmss(t + 86_400), fx.hhmmss(t + 94_900))
    n = LEN[k + 1]
    fx.typing(fx.hhmmss(t + 100_000), fx.hhmmss(t + 119_600), 19_600 // n)


for k in range(3, 19):
    loop_input(k)
fx.pointer_every("20:37:05.300", "20:37:55.300", 10_000)  # watches T19
fx.pointer("20:38:00.200", "20:38:09.700", "20:38:21.400", "20:38:30.200")  # the phone view, and back
fx.pointer("20:38:41.600", "20:38:58.300", "20:39:07.900")  # sits in the terminal
fx.pointer("20:39:15.100")                         # clicks VS Code
fx.keys_at("20:39:17.000", "20:39:17.600", "20:39:18.200", "20:39:19.600")  # Ctrl+P, Hero.css
fx.pointer_every("20:39:26.500", "20:40:46.500", 10_000)  # reads Hero.css, scrolling
fx.typing("20:40:52.600", "20:40:59.900", 300)     # types the margin-top, saves
fx.pointer("20:41:04.300", "20:41:09.900")         # waits for the reload, clicks the browser
fx.pointer("20:41:18.400", "20:41:29.000", "20:41:36.600", "20:41:39.900")  # the phone view: fixed
fx.typing("20:41:42.000", "20:42:59.600", 780)     # types T20
fx.pointer_every("20:43:04.600", "20:44:54.600", 10_000)  # watches T20
fx.pointer_every("20:45:03.200", "20:45:53.200", 10_000)  # reads its answer and diff
fx.pointer("20:46:00.200")                         # the browser
fx.pointer_every("20:46:07.700", "20:46:57.700", 10_000)  # the desktop view
fx.pointer("20:47:00.200", "20:47:08.800", "20:47:21.500")  # back in the terminal, thinks
fx.typing("20:47:30.000", "20:48:59.600", 1_300)   # types T21
fx.pointer_every("20:49:04.600", "20:50:54.600", 10_000)  # watches T21
fx.pointer_every("20:51:03.200", "20:52:53.200", 10_000)  # reads its answer and diff
fx.pointer("20:53:00.200")                         # the browser
fx.pointer_every("20:53:08.100", "20:53:58.100", 10_000)  # the desktop view again
fx.pointer("20:54:00.200")                         # VS Code
fx.pointer_every("20:54:06.500", "20:54:56.500", 10_000)  # reads Hero.css
fx.typing("20:54:59.900", "20:56:59.900", 700)     # a small hand edit; saves at 20:57:00
fx.pointer("20:57:06.300", "20:57:14.800")         # source control
fx.typing("20:57:20.000", "20:57:55.000", 700)     # the commit message
fx.pointer("20:57:59.800", "20:58:00.300")         # Commit, then the terminal
fx.pointer("20:58:09.200", "20:58:23.700", "20:58:38.100", "20:58:52.600")  # thinks about the next thing
fx.typing("20:59:00.000", "21:00:29.600", 1_400)   # types T22
fx.pointer("21:00:41.300", "21:00:55.600")
fx.inputs = [(t + 50 if t % tl.SLOT_MS == 0 else t, k) for t, k in fx.inputs]
fx.ticks("19:57:45", "21:01:00", tl.SLOT_MS)


def focus(t, cls, app, project=None):
    data = {"app_class": cls, "app_hash": app}
    if cls == "terminal" and project:
        data["hosted_sessions"] = [S]
    if project:
        data["focus_project"] = project
    fx.emit(at(t) if isinstance(t, str) else t, "os.focus", data, source="os", via="os", sample=True)


def terminal(t):
    focus(t, "terminal", TERMINAL, P)


def browser(t):
    focus(t, "browser", BROWSER)


def vscode(t):
    focus(t, "editor", CODE, P)


handles = {}


def prompt(k):
    n = LEN[k]
    low = len(TEXT[k]) < 20
    handles[("prompt", k)] = fx.emit(
        SENT[k], "prompt.submit",
        {"turn_key": TURN[k], "len_chars": n, "pasted_chars": 0, "typed_len_est": n, "origin_declared": "none",
         "matches_pending_cron": False, "answers_question": False, "low_effort": low},
        source="hook", via="hook", sample=True, **CC)


def run(k):
    stop, steps = RUNS[k]
    for step in steps:
        t = at(step[0])
        if step[1] == "read":
            fx.emit(t, "agent.tool", {"tool_class": "read", "files": [step[2]], "lines_added": 0, "lines_removed": 0,
                                      "visible_output_lines": 0, "t_start": t - 600, "t_end": t},
                    source="hook", via="hook", sample=True, **CC)
        elif step[1] == "edit":
            _, _, f, added, removed, hunk = step
            handles[("edit", k)] = fx.emit(
                t, "agent.tool", {"tool_class": "edit", "files": [f], "lines_added": added, "lines_removed": removed,
                                  "visible_output_lines": 0, "t_start": t - 400, "t_end": t},
                source="hook", via="hook", sample=True, **CC)
            handles[("hunk", k)] = (f, hunk)
        else:
            fx.emit(t, "agent.output", {"turn_key": TURN[k], "words": step[2], "code_lines": 0, "is_final": True,
                                        "asks_question": False, "offers_options": False},
                    source="log", via="log", **CC)
    fx.emit(stop, "agent.stop", {"turn_key": TURN[k], "stop_reason": "end_turn", "stop_hook_active": False},
            source="hook", via="hook", sample=True, **CC)


def heartbeat(t, act, f, lines):
    handles[("editor", t)] = fx.emit(at(t), "editor.activity",
                                     {"editor": "vscode", "act": act, "file_hash": f, "lines_changed": lines,
                                      "edit_source": "human"},
                                     source="editor", via="editor", project=P)


# 19:57: he starts Claude Code in Windows Terminal. acceptEdits, because
# SM 6.5 has no permission prompts in the loop.
focus("19:57:35.000", "terminal", TERMINAL)
fx.emit(at("19:57:44.000"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "acceptEdits"},
        source="hook", via="hook", sample=True, **CC)
terminal("19:57:44.000")
prompt(1)
run(1)
browser("20:02:30.000")
terminal("20:02:50.000")
prompt(2)
run(2)
browser("20:04:00.300")
terminal("20:04:30.300")
for k in range(3, 19):
    prompt(k)
    run(k)
    browser(SENT[k] + 80_000)
    terminal(SENT[k] + 95_000)
prompt(19)
run(19)
browser("20:38:00.300")
terminal("20:38:30.300")
vscode("20:39:15.300")
# WakaTime sends a heartbeat when the file in front changes and at a save
# (HC 1.5): opening Hero.css and saving the fix in it. DESIGN 5.7 makes the
# first a navigate, so it is a NAV anchor.
heartbeat("20:39:20.000", "navigate", HERO, 0)
heartbeat("20:41:00.000", "save", HERO, 1)
browser("20:41:10.000")
terminal("20:41:40.000")
prompt(20)
run(20)
browser("20:46:00.300")
terminal("20:47:00.300")
prompt(21)
run(21)
browser("20:53:00.300")
vscode("20:54:00.300")
heartbeat("20:55:00.000", "edit", HERO, 2)
heartbeat("20:57:00.000", "save", HERO, 3)
handles["commit"] = fx.emit(at("20:58:00.000"), "git.change",
                            {"op": "commit", "sha": tl.git_sha("6.5 commit"), "parents": [tl.git_sha("6.5 parent")],
                             "files_changed": 3, "insertions": 45, "deletions": 14, "actor": "human"},
                            source="git", via="git", project=P)
terminal("20:58:00.400")
prompt(22)
fx.emit(at("21:00:44.000"), "agent.output",
        {"turn_key": TURN[22], "words": 25, "code_lines": 0, "is_final": False, "asks_question": False,
         "offers_options": False},
        source="log", via="log", **CC)

events = fx.build()
built = {id(e["data"]): e for e in events}
ev = {name: built[id(h[4])] for name, h in handles.items() if name[0] != "hunk"}

# Checks of the timing choices against SM's rows (README.md).
WINDOW = (at("20:00:00"), at("21:00:00"))


def slot_inputs(s):
    return [k for t, k in fx.inputs if s <= t < s + tl.SLOT_MS]


tl.check(all(t % tl.SLOT_MS for t, _ in fx.inputs), "no input on a slot boundary")
for s in range(WINDOW[0], WINDOW[1], tl.SLOT_MS):
    tl.check(slot_inputs(s), f"input in slot {fx.hhmmss(s)}")
focus_events = [(e["t"], e["data"]["app_class"]) for e in events if e["kind"] == "os.focus"]


def slot_focus(s):
    """The app class that held focus longest in the slot (SM 2.5)."""
    held = {}
    cur = max((f for f in focus_events if f[0] <= s), default=(0, "none"))[1]
    t0 = s
    for t, cls in focus_events:
        if s < t < s + tl.SLOT_MS:
            held[cur] = held.get(cur, 0) + t - t0
            cur, t0 = cls, t
    held[cur] = held.get(cur, 0) + s + tl.SLOT_MS - t0
    return max(held, key=lambda c: held[c])


browser_slots = [s for s in range(WINDOW[0], WINDOW[1], tl.SLOT_MS) if slot_focus(s) == "browser"]
for s in ("20:02:30", "20:04:00", "20:04:15", "20:38:00", "20:38:15", "20:41:15", "20:41:30", "20:46:00", "20:46:45",
          "20:53:00", "20:53:45"):
    tl.check(at(s) in browser_slots, f"slot {s} is the browser's")
for s in ("20:02:45", "20:04:30", "20:38:30", "20:41:00", "20:41:45", "20:47:00", "20:54:00"):
    tl.check(at(s) not in browser_slots, f"slot {s} is the project's")
# The 20:02 browser check covers 20 s but holds only the slot 20:02:30 (SM 2.5).
tl.check(at("20:02:15") not in browser_slots and at("20:02:45") not in browser_slots, "20:02's check is one slot")


def typing_s(n):
    """The typing part of a lead, n / COMPOSE_CPS rounded to the nearest
    second, halves up (SM 1.3): 35 characters give 23 s in SM's 143 s for
    T2, and 61 give 41 s in 6.4."""
    return Fraction(int(Fraction(n) / COMPOSE_CPS + Fraction(1, 2)))


def lead(k):
    if len(TEXT[k]) < 20:
        return Fraction(LOW_LEAD_S)
    return THINK_LEAD_S + typing_s(LEN[k])


tl.check(lead(2) == 143 and SENT[2] - lead(2) * 1000 < at("20:01:00"), "T2's lead, SM's 143 s, has started by 20:01:00")
tl.check(SENT[20] - lead(20) * 1000 < at("20:41:00"), "T20's lead covers 20:41:00 to 20:43:00")
tl.check(SENT[21] - lead(21) * 1000 < at("20:47:00"), "T21's lead covers 20:47:00 to 20:49:00")
tl.check(SENT[22] - lead(22) * 1000 <= at("20:58:00"), "T22's lead covers 20:58:00 to 21:00:00")
tl.check(at("20:39:20") - EDIT_LEAD_S * 1000 < RUNS[19][0], "the NAV lead would reach back past T19's stop")
tl.check(at("20:41:00") - EDIT_LEAD_S * 1000 == at("20:39:00"), "the 20:41:00 edit's lead starts at 20:39:00")
tl.check(at("20:58:00") - COMMIT_LEAD_S * 1000 == at("20:57:00"), "the commit's lead starts at 20:57:00")
for name in [("editor", "20:39:20.000"), ("editor", "20:41:00.000"),
             ("editor", "20:55:00.000"), ("editor", "20:57:00.000"), "commit"]:
    e = ev[name]
    tl.check(slot_inputs(e["t"] - e["t"] % tl.SLOT_MS), f"input in the slot of the {e['kind']} at {fx.hhmmss(e['t'])}")
for k, t in SENT.items():
    tl.check(t - fx.last_input_before(t - 1) <= 10_000, f"T{k}: input in the 10 s before submit")

# The reading bucket (SM 2.4) for the credited turns: T2's 20 s base runs
# out at 20:03:20 and nothing new comes until the edit at 20:03:40; T20's and
# T21's run out 20 s after their submits and refill with the edit 40 s
# later. The turns between are discounted and leave it empty.
bucket = tl.Bucket()
fills = [(e["t"], "reset" if e["kind"] == "prompt.submit" else bucket.amount(e["kind"], e["data"]),
          e["data"].get("turn_key")) for e in events if e["kind"] in ("prompt.submit", "agent.output", "agent.tool")]
discounted = {TURN[k] for k in LOOP}
turn_of = {}
for t, kind, tk in fills:
    if kind == "reset":
        cur = tk
    turn_of[t] = cur
drain = [("20:03:00", "20:03:20"), ("20:03:40", "20:04:00"), ("20:04:30", "20:05:00"), ("20:43:00", "20:43:20"),
         ("20:44:00", "20:46:00"), ("20:49:00", "20:49:20"), ("20:50:00", "20:53:00")]
empty = [("20:03:20", "20:03:40"), ("20:43:20", "20:44:00"), ("20:49:20", "20:50:00")]
drain = [(at(a), at(b)) for a, b in drain]
empty = [(at(a), at(b)) for a, b in empty]
i = 0
for tau in range(at("20:03:00"), at("20:53:00"), 1000):
    while i < len(fills) and fills[i][0] <= tau:
        t, amount, _ = fills[i]
        if amount == "reset":
            bucket.reset(t, 0 if turn_of[t] in discounted else tl.Bucket.BASE_S)
        elif turn_of[t] not in discounted:
            bucket.fill(t, amount)
        i += 1
    if any(a <= tau < b for a, b in drain):
        tl.check(bucket.drain(1) == 1, f"the bucket has something to read at {fx.hhmmss(tau)}")
    if any(a <= tau < b for a, b in empty):
        tl.check(bucket.total() == 0, f"nothing new on screen at {fx.hhmmss(tau)}")

# The detectors (SM 4.2), from the texts and hunks: re-roll attempts, rewrite
# repeats, and the discount each loop turn gets. Change events: his typed
# edits and the commit.
changes = [ev[("editor", t)]["t"] for t in ("20:41:00.000", "20:55:00.000", "20:57:00.000")] + [ev["commit"]["t"]]
order = sorted(SENT)
attempt = dict(zip(order, neardup.attempts([(SENT[k], TEXT[k]) for k in order], changes)))
tl.check([attempt[k] for k in order] == [1, 1, 2, 3, 1, 1, 4, 1, 1, 1, 1, 5, 1, 1, 1, 6, 1, 1, 1, 1, 1, 1],
         f"re-roll attempts {attempt}")
tl.check(not neardup.far_apart(list(dict.fromkeys(TEXT.values()))), f"close pairs {neardup.far_apart(list(TEXT.values()))}")
# Edge punctuation goes (SM 4.2, ledger-v1.md 10.4 step 6), so T4's "fix it!!" and T16's comma repeat T2.
tl.check({neardup.normalize(TEXT[k]) for k in (2, 3, 4, 7, 12, 16)} == {"still overlapping on mobile <retry>"},
         "T2, T3, T4, T7, T12 and T16 normalize to one text")


def changed_lines(k):
    f, (old_start, old_lines, new_start, new_lines) = handles[("hunk", k)]
    return f, set(range(old_start, old_start + old_lines)) or {old_start}, set(range(new_start, new_start + new_lines))


repeat = {}
prev = None
for k in range(1, 22):
    f, old, new = changed_lines(k)
    if prev is None or any(RUNS[prev[0]][0] < c < SENT[k] for c in changes):
        repeat[k] = 0
    else:
        pk, pf, pnew = prev
        overlap = Fraction(len(old & pnew), len(old)) if f == pf else Fraction(0)
        repeat[k] = repeat[pk] + 1 if overlap >= Fraction(1, 2) else 0
    prev = (k, f, new)
tl.check([repeat[k] for k in range(1, 22)] == [0] + list(range(1, 19)) + [0, 0], f"rewrite repeats {repeat}")
# Where both rules fire, X.reroll comes first (SM 4.2: X.reroll, X.same_error, X.autopilot, X.rewrite_loop).
code = {}
for k in range(1, 23):
    if attempt[k] >= 3:
        code[k] = "X.reroll"
    elif repeat.get(k, 0) >= 2:
        code[k] = "X.rewrite_loop"
tl.check(set(code) == set(LOOP), f"discounted turns {sorted(code)}")
tl.check(code[3] == "X.rewrite_loop" and code[4] == "X.reroll" and code[19] == "X.rewrite_loop",
         "T3, T4 and T19 get the codes of SM's 20:05, 20:07 and 20:38 rows")
# Autopilot: no answer of 100 words or more is followed by a prompt in under
# a quarter of the time it takes to read at 238 wpm.
for k in range(1, 22):
    words = RUNS[k][1][-1][2]
    gap = SENT[k + 1] - RUNS[k][0]
    tl.check(words < 100 or gap >= Fraction(1, 4) * 60_000 * words / 238, f"T{k}'s answer is read")
tl.check(Fraction(1, 4) * 60 * 110 / 238 < 7, "a 110-word answer is unread under 7 s")

runs = [(SENT[k], RUNS[k][0]) for k in range(1, 22)] + [(SENT[22], at("21:01:00"))]


def agent_seconds(a, b):
    return sum(max(0, min(y, b) - max(x, a)) for x, y in runs) // 1000


for a, b in [("20:04:00", "20:04:30"), ("20:38:00", "20:38:30"), ("20:41:15", "20:41:45"), ("20:46:00", "20:47:00"),
             ("20:53:00", "20:54:00"), ("20:02:30", "20:02:45")]:
    tl.check(agent_seconds(at(a), at(b)) == 0, f"no agent runs during the lookup {a}-{b}")

# SM 4.2's second scoring, with every discount removed: for each second the
# rows leave uncredited, 20:03:20-:40 and 20:05:00 to 20:38:30, what would
# have credited it had no turn been discounted: the lead of the next anchor
# (SM 3.3 rule 3), else the session's bucket, else a bracketed lookup (SM
# 2.5).
# Without T19's discount the 20:39:20 NAV lead isn't cut at T19's stop.
UNCREDITED = [(at("20:03:20"), at("20:03:40")), (at("20:05:00"), at("20:38:30"))]
leads = [(SENT[k] - lead(k) * 1000, SENT[k], k) for k in range(2, 20)] + [(at("20:37:20"), at("20:39:20"), 19)]
would = {}  # second -> the discounted turn that keeps it from being credited
undiscounted = tl.Bucket()
j = 0
for tau in range(at("20:03:00"), at("20:39:00"), 1000):
    while j < len(fills) and fills[j][0] <= tau:
        t, amount, tk = fills[j]
        if amount == "reset":
            undiscounted.reset(t, tl.Bucket.BASE_S)
            owner = TURN.index(tk)
        else:
            undiscounted.fill(t, amount)
        j += 1
    if tau - tau % tl.SLOT_MS in browser_slots:
        continue
    covering = [(b, k) for a, b, k in leads if a <= tau < b]
    if covering:
        would[tau] = min(covering)[1]
        continue
    got = undiscounted.drain(1)
    tl.check(got in (0, 1), f"whole seconds from the bucket at {fx.hhmmss(tau)}")
    if got:
        would[tau] = owner
looks = [s for s in browser_slots if at("20:03:00") <= s < at("20:39:00")]
for s in looks:
    if s - tl.SLOT_MS in looks:
        continue
    end = s
    while end in looks:
        end += tl.SLOT_MS
    if s - 1000 in would and end in would and agent_seconds(s, end) == 0:
        for tau in range(s, end, 1000):
            would[tau] = would[s - 1000]
unc_seconds = [tau for a, b in UNCREDITED for tau in range(a, b, 1000)]
nothing = [tau for tau in unc_seconds if tau not in would]
tl.check(all(would[tau] in code for tau in unc_seconds if tau in would),
         "only discounted turns stand behind the uncredited seconds")
# T7's bucket holds only its 20 s base until its edit at 20:13:40, and T8,
# "pls fix the overlap", is a go-ahead with a 20 s lead, so 20:13:20-:40 is
# the one stretch nothing would have credited. T7's agent runs through it.
tl.check(lead(8) == LOW_LEAD_S and RUNS[7][1][1][:2] == ("20:13:40", "edit"),
         "T8 is a go-ahead and T7's edit lands at 20:13:40")
tl.check(nothing == list(range(at("20:13:20"), at("20:13:40"), 1000))
         and agent_seconds(at("20:13:20"), at("20:13:40")) == 20,
         f"only 20:13:20-:40 would have had no credit: {[fx.hhmmss(t) for t in nothing]}")


def loop_codes(minute):
    """A minute's uncredited seconds by code (SM 4.2). A second the second
    scoring credits takes the code of the latest loop prompt at or before it,
    or T3's before T3; any other uncredited second is X.agent_only."""
    m = at(minute)
    got = {}
    for tau in range(m, m + tl.MINUTE_MS, 1000):
        if any(a <= tau < b for a, b in UNCREDITED):
            if tau in would:
                before = [k for k in LOOP if SENT[k] <= tau]
                c = code[before[-1]] if before else code[3]
            else:
                c = "X.agent_only"
            got[c] = got.get(c, 0) + 1
    return got


LABEL = {fx.hhmm(m): loop_codes(fx.hhmm(m)) for m in range(at("20:03"), at("20:39"), tl.MINUTE_MS)}
tl.check(LABEL["20:03"] == {"X.rewrite_loop": 20} and LABEL["20:05"] == {"X.rewrite_loop": 60}
         and LABEL["20:06"] == {"X.rewrite_loop": 60} and LABEL["20:07"] == {"X.reroll": 60}
         and LABEL["20:13"] == {"X.reroll": 40, "X.agent_only": 20} and LABEL["20:38"] == {"X.rewrite_loop": 30},
         f"SM 4.2's label gives the codes of SM's 20:03, 20:05, 20:06, 20:07 and 20:38 rows: {LABEL}")
loop_totals = {}
for codes in LABEL.values():
    for c, n in codes.items():
        if c != "X.agent_only":
            loop_totals[c] = loop_totals.get(c, 0) + n
tl.check(loop_totals == {"X.reroll": 460, "X.rewrite_loop": 1550}, f"the loop's seconds by code {loop_totals}")
# E-6.5-loop-codes's table of the loop minutes, written out here rather than
# taken from LABEL, so a change to the label rule that moves seconds between
# minutes fails: X.reroll 60 at 20:07, 20:08, 20:14, 20:23-:24 and 20:31-:32,
# X.reroll 40 and X.agent_only 20 at 20:13, and X.rewrite_loop 60 in every
# other minute from 20:05 to 20:37.
REROLL_MINUTES = {"20:07", "20:08", "20:14", "20:23", "20:24", "20:31", "20:32"}
for m in range(at("20:05"), at("20:38"), tl.MINUTE_MS):
    hm = fx.hhmm(m)
    want = ({"X.reroll": 40, "X.agent_only": 20} if hm == "20:13"
            else {("X.reroll" if hm in REROLL_MINUTES else "X.rewrite_loop"): 60})
    tl.check(LABEL[hm] == want, f"{hm}: E-6.5-loop-codes gives {want}, SM 4.2's label {LABEL[hm]}")

# He opens Hero.css at 20:39:20 and saves his fix in it at 20:41:00. The file
# in front doesn't change and the save comes 100 s later, under WakaTime's
# 120 s (HC 1.5), so no heartbeat comes between them. From 20:38:30, when he
# is back from the browser and T19 has stopped, to the 20:41:00 save, the
# lead of the next anchor wins (SM 3.3 rule 3):
EDITOR_ANCHORS = {"20:39:20.000": "NAV", "20:41:00.000": "E"}
tl.check({b: ev[("editor", b)]["data"]["act"] for b in EDITOR_ANCHORS}
         == {"20:39:20.000": "navigate", "20:41:00.000": "save"}, "a navigation and a save")
hero_beats = [e for e in events if e["kind"] == "editor.activity" and at("20:39:20") <= e["t"] <= at("20:41:00")]
tl.check([e["t"] for e in hero_beats] == [at("20:39:20"), at("20:41:00")]
         and all(e["data"]["file_hash"] == HERO for e in hero_beats)
         and at("20:41:00") - at("20:39:20") < 120_000,
         "the only heartbeats from 20:39:20 to 20:41:00 open and save Hero.css, under 120 s apart")


def editor_minutes(beats):
    """20:38:30 to 20:41:00 by minute and reason, from the leads of the
    given editor anchors and T20's prompt."""
    windows = [(ev[("editor", b)]["t"], EDITOR_ANCHORS[b], EDIT_LEAD_S * 1000) for b in beats]
    windows.append((SENT[20], "P", lead(20) * 1000))
    got = {}
    for tau in range(at("20:38:30"), at("20:41:00"), 1000):
        _, why, _ = min(w for w in windows if w[0] - w[2] <= tau < w[0])
        reasons = got.setdefault(fx.hhmm(tau), {})
        reasons[why] = reasons.get(why, 0) + 1
    return got


sm_editor = editor_minutes(list(EDITOR_ANCHORS))
tl.check(sm_editor == {"20:38": {"NAV": 30}, "20:39": {"NAV": 20, "E": 40}, "20:40": {"E": 60}},
         f"the leads give SM's rows: {sm_editor}")

# What Hackatime would log for the hour (SM 6.5's last paragraph): agent
# events as ai coding, editor heartbeats as coding, each replayed with
# min(gap, 120 s), and the handbook's third.
agent_times = [e["t"] for e in events if e["kind"] in ("prompt.submit", "agent.tool", "agent.stop")]
editor_times = [e["t"] for e in events if e["kind"] == "editor.activity"]


def replay(times):
    inside = [t for t in times if WINDOW[0] <= t < WINDOW[1]]
    return tl.hackatime_replay(inside)


ai_logged, coding_logged = replay(agent_times), replay(editor_times)

exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 6.5", evidence="B", witness="witnessed",
                  projects={P: "pantry-app"}, start="20:00:00", end="21:00:00")


def row(minute, credit=None, unc=None, codes=None, **kw):
    m = at(minute)
    exp.row(minute, credit={P: credit} if credit else {}, uncredited=unc, codes=codes,
            agent=agent_seconds(m, m + tl.MINUTE_MS), sm=kw.pop("sm", minute), **kw)


row("20:00", {"P": 60}, {})
row("20:01", {"P": 60}, {})
row("20:02", {"P": 45, "LOOKUP": 15}, {})
row("20:03", {"SUP": 40}, {"X.rewrite_loop": 20})
row("20:04", {"LOOKUP": 30, "READ": 30}, {})
row("20:05", None, {"X.rewrite_loop": 60})
row("20:06", None, {"X.rewrite_loop": 60})
row("20:07", None, {"X.reroll": 60})
for m in range(at("20:08"), at("20:38"), tl.MINUTE_MS):
    row(fx.hhmm(m), None, LABEL[fx.hhmm(m)], sm="20:08 to 20:37", derived=["uncredited"],
        note="SM gives the range's codes and its 20 s of X.agent_only. Each minute's seconds follow from this fixture's "
             "prompts and SM 4.2.")
row("20:38", {"NAV": 30}, {"X.rewrite_loop": 30})
for r in exp.rows:
    if r["local"] in LABEL:
        tl.check(r["uncredited"] == LABEL[r["local"]], f"row {r['local']} has the codes SM 4.2's label gives")
row("20:39", {"NAV": 20, "E": 40}, {})
row("20:40", {"E": 60}, {})
row("20:41", {"P": 30, "LOOKUP": 30}, {})
row("20:42", {"P": 60}, {})
exp.row("20:43", "21:00", credit={P: {"P": 240, "E": 180, "G": 60, "SUP": 160, "READ": 180, "LOOKUP": 120}},
        uncredited={"X.agent_only": 80}, agent=agent_seconds(at("20:43"), at("21:00")), sm="20:43 to 20:59",
        derived=["credit"],
        note="SM gives 940 s credited, the codes and X.agent_only 80. The split is this fixture's, from the "
             "timeline in README.md: T20 and T21 each SUP 80, X.agent_only 40, then READ; the desktop view "
             "20:46 and 20:53 (LOOKUP); T21's lead 20:47-20:49 and T22's 20:58-21:00 (P); a hand edit "
             "20:54-20:57 (E) and the commit's lead (G).")

# SM's stated numbers, tied to the rows so that an edited row can't move
# them silently: excluded_loops_s 2,010 from the rows' loop codes, X.agent_only
# 100 from the stretches where a turn runs and nothing credits a second the
# loop didn't cost, and the 20:43 to 20:59 row's 940 s credited and 80 s
# X.agent_only.
LOOP_CODES = ("X.reroll", "X.rewrite_loop", "X.same_error", "X.autopilot")  # excluded_loops_s (SM 5.3)


def loop_seconds(r):
    """A row's loop seconds."""
    return sum(n for c, n in r["uncredited"].items() if c in LOOP_CODES)


excluded = sum(loop_seconds(r) for r in exp.rows)
tl.check(excluded == 2010 == sum(loop_totals.values()), f"excluded_loops_s: the rows give {excluded}")
agent_only = exp.totals()["uncredited"]["X.agent_only"]
# 20:13:20-:40 is no loop cost: nothing would have credited it (above).
AGENT_ONLY = [(at("20:13:20"), at("20:13:40")), (at("20:43:20"), at("20:44:00")), (at("20:49:20"), at("20:50:00"))]
tl.check(agent_only == 100 == sum(b - a for a, b in AGENT_ONLY) // 1000, f"X.agent_only: the rows give {agent_only}")
for a, b in empty:
    tl.check(agent_seconds(a, b) == (b - a) // 1000, f"an agent runs while nothing is on screen at {fx.hhmmss(a)}")
rest = exp.rows[-1]
split = rest["credit"][P]
tl.check(rest["local"] == "20:43-20:59" and rest["credited_s"] == 940 and rest["uncredited"] == {"X.agent_only": 80}
         and sum(b - a for a, b in empty if a >= at("20:43")) // 1000 == 80,
         f"20:43 to 20:59: SM's 940 s credited and 80 s X.agent_only, not {rest['credited_s']} and {rest['uncredited']}")
tl.check(split["SUP"] + split["READ"] == sum(b - a for a, b in drain if a >= at("20:43")) // 1000
         and split["LOOKUP"] == 15 * sum(at("20:43") <= s < at("21:00") for s in browser_slots),
         f"20:43 to 20:59: SUP and READ are the bucket's seconds and LOOKUP the browser's: {split}")
# A lookup is a whole number of slots (SM 2.5), so every row that credits
# LOOKUP credits 15 s per browser slot in it.
for r in exp.rows:
    looked = r.get("credit", {}).get(P, {}).get("LOOKUP")
    if looked:
        m0 = at(r["local"][:5])
        tl.check(looked == 15 * sum(m0 <= s < m0 + r["minutes"] * tl.MINUTE_MS for s in browser_slots),
                 f"row {r['local']}: LOOKUP is 15 s per browser slot, not {looked}")
# Rows 20:38 to 20:40 are what the editor anchors' leads give.
for r in exp.rows:
    m = r["local"]
    if m in sm_editor:
        tl.check(r["credit"][P] == sm_editor[m] and "erratum" not in r, f"row {m} is SM's and names no erratum")

expected = exp.doc(
    extra_totals={"credited_s": 1490, "excluded_loops_s": excluded},
    stated=["credited_s", "excluded_loops_s"],
    approx={
        "ai_logged_s": {"about": 3000, "sm": "Hackatime would log about 50 minutes of ai coding"},
        "coding_logged_s": {"about": 300, "sm": "and 5 of coding for this hour"},
        "handbook_s": {"about": 1320, "sm": "the handbook's third would allow about 22 minutes"},
    },
    errata=[],
    notes=[
        "excluded_loops_s is X.reroll + X.rewrite_loop (SM 5.3): the seconds a scoring with no turn discounted "
        "credits and this one doesn't (SM 4.2), which are 20:03:20-:40 and 20:05:00 to 20:38:30 apart from "
        "20:13:20-:40. Each takes the code of the latest loop prompt at or before it, and 20:03:20-:40 takes T3's, "
        "so X.reroll 460 and X.rewrite_loop 1550.",
        "20:13:20-:40 is X.agent_only. T7's bucket holds only its 20 s base until its edit at 20:13:40, and T8, "
        "\"pls fix the overlap\", is a go-ahead with a 20 s lead, so nothing would have credited it even with no turn "
        "discounted.",
        "SM's X.agent_only total of 100 s is totals.uncredited: 20 s at 20:13 and 80 s in 20:43-20:59.",
        "T3 to T19 are discounted: T3 as rewrite repeat 2 (attempt 2), T4 as attempt 3, and from T5 on the "
        "paraphrases as rewrite repeats and the exact repeats (T7, T12, T16) as attempts 4, 5 and 6. Where both "
        "rules fire, the code is X.reroll, first in SM 4.2's order.",
        f"This fixture's events replay to {ai_logged:g} s of ai coding and {coding_logged:g} s of coding inside "
        "the hour under min(gap, 120 s); SM says about 50 and 5 minutes.",
        "agent_running_s follows from the runs in README.md: T3 to T18 run 75 s, into the minute after their "
        "prompt, so every loop minute has a running agent and a bucket (SM 5.1).",
    ],
)


def anc(e, kind, lead_s, lead_from, sm, origin=None, cls=None):
    return tl.anchor(fx, e, kind=kind, origin=origin, prompt_class=cls, lead_s=lead_s, trail_s=0,
                     lead_from=lead_from, level="B", sm=sm)


def seconds(v):
    return int(v) if v.denominator == 1 else float(v)


ROW_OF = {1: "20:00: sends at 20:01:00, 95 characters", 2: "20:01: the lead of his next prompt (143 s)",
          3: "20:05: sends the identical prompt at 20:05:00", 4: "20:07: normalizes to the same text, attempt 3",
          19: "20:08 to 20:37: the prompt at 20:37; 20:38: T19 stops at 20:38:00", 20: "20:42: sends at 20:43:00",
          21: "20:43 to 20:59: the second of two more turns", 22: "after the window; its lead covers 20:58 to 21:00"}
for k in range(5, 19):
    ROW_OF[k] = f"20:08 to 20:37: the prompt at {fx.hhmm(SENT[k])}, " + (
        "a repeat" if TEXT[k].startswith("still overlapping") else "in new words")
anchor_list = []
for k in sorted(SENT):
    e = ev[("prompt", k)]
    low = len(TEXT[k]) < 20
    if k in code:
        why = (f"discounted, {code[k]}: attempt {attempt[k]}, rewrite repeat {repeat[k]} (SM 4.2); it opens no "
               "window")
    else:
        why = "credited"
    note = ROW_OF[k] + "; " + why
    if low:
        anchor_list.append(anc(e, "go_ahead", LOW_LEAD_S, "params", note, origin="human_typed", cls="low_effort"))
    else:
        # SM's 20:01 row states T2's lead, 143 s; the others follow from SM 2.3.
        anchor_list.append(anc(e, "substantive_prompt", seconds(lead(k)), "sm" if k == 2 else "params", note,
                               origin="human_typed", cls="substantive"))
anchor_list += [
    anc(ev[("editor", "20:39:20.000")], "editor_navigation", EDIT_LEAD_S, "params",
        "20:38-20:39: opens Hero.css (heartbeat at 20:39:20); its lead can't reach back past T19's stop"),
    anc(ev[("editor", "20:41:00.000")], "typed_edit", EDIT_LEAD_S, "params",
        "20:40: edits Hero.css by hand at 20:41:00, a change event"),
    anc(ev[("editor", "20:55:00.000")], "typed_edit", EDIT_LEAD_S, "params", "20:43 to 20:59: E"),
    anc(ev[("editor", "20:57:00.000")], "typed_edit", EDIT_LEAD_S, "params", "20:43 to 20:59: E"),
    anc(ev["commit"], "human_commit", COMMIT_LEAD_S, "params", "20:43 to 20:59: a commit at 20:58"),
]
anchor_list.sort(key=lambda a: a["t"])
turns = []
for k in sorted(SENT):
    stop = RUNS[k][0] if k in RUNS else None
    turns.append(tl.turn(fx, SENT[k], stop, session=S, turn_key=TURN[k], origin="human_typed",
                         prompt_class="low_effort" if len(TEXT[k]) < 20 else "substantive",
                         sm=f"T{k}, " + ROW_OF[k] + (f"; discounted, {code[k]}" if k in code else "")))
anchors = {"fixture": "6.5", "anchors": anchor_list, "turns": turns}

prompts_doc = {"fixture": "6.5", "prompts": []}
for k, t, text, source in PROMPTS:
    p = {"turn_key": TURN[k], "t": SENT[k], "local": fx.hhmmss(SENT[k]), "text": text, "len_chars": LEN[k],
         "source": source}
    prompts_doc["prompts"].append(p)

# hunks.json: what agent.tool's missing hunks field would hold for each edit,
# in unified-diff terms (Claude Code's structuredPatch: oldStart, oldLines,
# newStart, newLines), with the file as agent.tool's files key.
hunks = {
    "fixture": "6.5",
    "field": "agent.tool hunks",
    "status": "pkg/event has no field for edit hunks yet (issue #2). Once it has one, these move into the "
              "agent.tool events of events.jsonl and this file goes.",
    "edits": [],
}
for k in range(1, 22):
    f, (old_start, old_lines, new_start, new_lines) = handles[("hunk", k)]
    e = ev[("edit", k)]
    hunks["edits"].append({"event_id": e["event_id"], "t": e["t"], "local": fx.hhmmss(e["t"]), "turn_key": TURN[k],
                           "hunks": [{"file": f, "old_start": old_start, "old_lines": old_lines,
                                      "new_start": new_start, "new_lines": new_lines}]})

out = tl.out_dir(HERE)
fx.write(out, expected, anchors, prompts_doc)
with open(os.path.join(out, "hunks.json"), "w", encoding="utf-8", newline="\n") as f:
    f.write(tl.pretty(hunks) + "\n")
