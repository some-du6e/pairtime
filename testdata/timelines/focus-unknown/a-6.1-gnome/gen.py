# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.1's first cycle on GNOME on Wayland, where pairtime can't tell which
window is in front. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import focus as fu  # noqa: E402

NAME = "focus-unknown/a-6.1-gnome"
fx = tl.Fixture(
    NAME,
    title="SM 6.1's first cycle on GNOME on Wayland, where focus is unknown",
    date="2026-11-21",
    timezone_name="Asia/Kolkata",
    utc_offset_min=330,
    os_name="linux",
    keys=False,
    device=tl.key("d", "focus-unknown a laptop"),
    boot_id=tl.key("b", "focus-unknown a boot"),
    boot="08:02:37.000",
    seq0=41207,
    # His last input before the case starts. No project context was live
    # then, so the agent recorded nothing (DESIGN 3.3).
    last_input="13:56:51.300",
)
at = fx.at
P = tl.key("p", "focus-unknown a cs2go")
S = tl.key("s", "focus-unknown a claude session")
CC = dict(harness="claude_code", version="2.1.328", session=S, project=P)
TURN = tl.key("t", "focus-unknown a turn 1")
FILES = [tl.key("f", f"focus-unknown a file {i}") for i in range(12)]
rand = tl.Rand("focus-unknown a")
T0, END = at("14:00:00"), at("14:12:00")


def c(mss):
    """Unix ms of an "M:SS.f" offset into the cycle, which starts at 14:00."""
    m, s = mss.split(":")
    return T0 + int(m) * 60_000 + round(float(s) * 1000)


# Input. Mutter's idle monitor reports any input and can't tell keys from
# the pointer, so every sample has idle_keys_s null. No input falls on a
# slot boundary (../../README.md).

def press(t):
    tl.check(t % tl.SLOT_MS, f"input at {fx.hhmmss(t)} is off a slot boundary")
    fx.inputs.append((t, True))


def move(t):
    tl.check(t % tl.SLOT_MS, f"input at {fx.hhmmss(t)} is off a slot boundary")
    fx.inputs.append((t, False))


def presses(a, b, lo, hi):
    t = a
    while t <= b:
        if t % tl.SLOT_MS == 0:
            t += 100
        press(t)
        t += rand.between(lo, hi)


def moves(a, b, lo, hi):
    t = a
    while t <= b:
        if t % tl.SLOT_MS == 0:
            t += 100
        move(t)
        t += rand.between(lo, hi)


# Events.

def heartbeat(t, act, file):
    # WakaTime's VS Code plugin through the dual-send. On Linux the agent's
    # peer-process check finds wakatime-cli under VS Code, so the heartbeat
    # arrives via editor (DESIGN 5.7), and a verified heartbeat takes a
    # presence sample at once (DESIGN 3.3).
    return fx.emit(t, "editor.activity",
                   {"editor": "vscode", "act": act, "file_hash": file, "lines_changed": 0, "edit_source": "unknown"},
                   source="editor", via="editor", project=P, sample=True)


def output(t, words, final=False):
    return fx.emit(t, "agent.output",
                   {"turn_key": TURN, "words": words, "code_lines": 0, "is_final": final, "asks_question": False,
                    "offers_options": False},
                   source="log", via="log", **CC)


def edit(t_end, t_start, file, added):
    return fx.emit(t_end, "agent.tool",
                   {"tool_class": "edit", "files": [file], "lines_added": added, "lines_removed": 30 - added,
                    "visible_output_lines": 0, "t_start": t_start, "t_end": t_end},
                   source="hook", via="hook", sample=True, **CC)


# Before the window. The agent runs, but no project context is live: GNOME
# on Wayland tells it nothing about the windows, and no harness runs yet.
# Rowan opens a terminal with a shortcut and starts Claude Code. Its
# SessionStart hook makes a context live, so the agent records the one
# os.focus it can, app_class unknown (SM 2.5, DESIGN 3.3), and starts
# sampling.
press(at("13:57:54.800"))
fx.typing("13:58:01.000", "13:58:04.600", 600)  # claude, Enter
fx.emit(at("13:58:05.500"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "default"},
        source="hook", via="hook", sample=True, **CC)
fx.emit(at("13:58:05.500"), "os.focus", {"app_class": "unknown"}, source="os", via="os", sample=True)
fx.ticks("13:58:15", "14:12:45", tl.SLOT_MS)
move(at("13:58:41.200"))
move(at("13:59:20.700"))
press(at("13:59:44.900"))  # Alt-Tab to VS Code, which pairtime can't see
presses(at("13:59:46.300"), at("13:59:58.000"), 1_800, 4_200)  # the explorer, no heartbeat

# 0:00 to 2:00, VS Code. He moves through the explorer and quick open with
# the keyboard; a file opens at 0:30 and another at 1:50, the minutes' only
# heartbeats, as in SM 6.1.
presses(c("0:01.2"), c("0:28.0"), 1_800, 4_200)
press(c("0:29.6"))
heartbeat(c("0:30"), "navigate", FILES[3])
presses(c("0:32.4"), c("1:46.0"), 2_500, 6_000)
press(c("1:47.2"))
presses(c("1:47.8"), c("1:49.3"), 400, 600)
press(c("1:49.6"))
heartbeat(c("1:50"), "navigate", FILES[7])
press(c("1:53.1"))
press(c("1:56.4"))

# 2:00 to 4:00, the terminal: the 300-character prompt, typed.
press(c("1:59.9"))  # Alt-Tab
presses(c("2:00.8"), c("3:59.4"), 300, 500)
press(c("3:59.8"))
fx.emit(c("4:00"), "prompt.submit",
        {"turn_key": TURN, "len_chars": 300, "pasted_chars": 0, "typed_len_est": 300, "origin_declared": "none",
         "matches_pending_cron": False, "answers_question": False, "low_effort": False},
        source="hook", via="hook", sample=True, **CC)

# 4:00 to 6:30, the agent's turn, which he watches.
output(c("4:30"), 45)
move(c("4:33.4"))
output(c("5:00"), 45)
move(c("5:02.6"))
press(c("5:24.6"))  # picks Yes in the permission dialog
fx.emit(c("5:25"), "agent.permission",
        {"requested_at": c("5:10"), "answered_at": c("5:25"), "decision": "accept", "decided_by": "user"},
        source="hook", via="hook", sample=True, **CC)
a1 = rand.between(16, 24)
edit(c("5:30"), c("5:10"), FILES[2], a1)
move(c("5:41.2"))
a2 = rand.between(16, 24)
edit(c("6:00"), c("5:59.6"), FILES[9], a2)
move(c("6:09.8"))
output(c("6:30"), 160, final=True)
fx.emit(c("6:30"), "agent.stop", {"turn_key": TURN, "stop_reason": "end_turn", "stop_hook_active": False},
        source="hook", via="hook", sample=True, **CC)

# 6:30 to 9:00, reads the summary and scrolls back through both diffs.
moves(c("6:33.0"), c("8:56.0"), 5_000, 9_000)
move(c("8:59.9"))  # clicks the browser

# 9:00 to 11:00, plays the change in the browser on localhost.
presses(c("9:01.5"), c("10:58.5"), 700, 2_200)
press(c("10:59.9"))  # Alt-Tab to the terminal

# 11:00 to 11:40, git add -p and git commit.
presses(c("11:01.0"), c("11:39.4"), 450, 750)
press(c("11:39.8"))
fx.emit(c("11:40"), "git.change",
        {"op": "commit", "sha": tl.git_sha("focus-unknown a commit"), "parents": [tl.git_sha("focus-unknown a base")],
         "files_changed": 2, "insertions": a1 + a2, "deletions": 60 - a1 - a2, "actor": "human"},
        source="git", via="git", project=P)

# After the window: the next cycle starts at 14:12 in VS Code, and its first
# heartbeat at 14:12:30 is the NAV whose lead credits 14:10:30 to 14:10:40
# and 14:11:40 to 14:12:00.
press(at("14:11:59.900"))
presses(at("14:12:01.200"), at("14:12:28.000"), 1_800, 4_200)
press(at("14:12:29.600"))
heartbeat(at("14:12:30"), "navigate", FILES[5])
press(at("14:12:33.100"))
press(at("14:12:37.500"))
press(at("14:12:42.200"))

events = fx.build()

# The credit, worked out by hand from SM 2.5 (README.md).
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.5 and 6.1", evidence="C", witness="witnessed",
                  projects={P: "cs2go"}, start="14:00", end="14:12")
RUNS = [
    ("14:00:00", "14:01:50", "NAV"),
    ("14:01:50", "14:04:00", "P"),
    ("14:04:00", "14:04:20", "SUP"),
    ("14:04:20", "14:04:30", "X.agent_only"),
    ("14:04:30", "14:04:45", "SUP"),
    ("14:04:45", "14:05:00", "X.agent_only"),
    ("14:05:00", "14:05:10", "SUP"),
    ("14:05:10", "14:05:35", "PA"),
    ("14:05:35", "14:06:30", "SUP"),
    ("14:06:30", "14:10:30", "READ"),
    ("14:10:30", "14:10:40", "NAV"),
    ("14:10:40", "14:11:40", "G"),
    ("14:11:40", "14:12:00", "NAV"),
]
case = fu.Case(fx, exp, runs=RUNS, level="C", level_c=[("14:00:00", "14:12:00")],
               turns=[(c("4:00"), c("6:30"))])
rp = fu.Replay(events, T0, END)
case.check(rp)

# Every slot's focus is unknown, so every credited second is level C with
# focus_unknown, and no slot is a browser slot, so there is no lookup.
tl.check(all(f == ("unknown", None) for f in rp.slot_focus.values()), "focus is unknown in every slot")
tl.check(not rp.contested, "one project, so no second is contested")
# SM 6.1's bucket: 303 1/3 s at 14:09:00, and 213 1/3 s left at 14:12:00.
read = sum(1 for t, v in rp.credit.items() if v[1] == "READ")
tl.check(read == 240, f"READ {read}")

SM61 = "6.1 {}: as SM's row"
NOTES = {
    "14:00": (SM61.format("14:00"), "The heartbeats at 14:00:30 and 14:01:50 are NAV anchors: input in their slots, "
                                    "and the editor-focus test passes because focus is unknown (SM 2.5)."),
    "14:01": (SM61.format("14:01"), "From 14:01:50 the prompt's 320 s lead, from 13:58:40, is the only window."),
    "14:02": (SM61.format("14:02"), None),
    "14:03": (SM61.format("14:03"), "SM 2.2 rule 6: input before the submit, and the focus test passes on "
                                    "unknown focus, so the prompt is human_typed, level C with focus_unknown."),
    "14:04": (SM61.format("14:04"), "Nothing new to read from 14:04:20 to 14:04:30 and from 14:04:45 to 14:05:00. "
                                    "SM 5.2 skips X.other_focus and X.ambiguous where focus is unknown, so the "
                                    "running turn's X.agent_only."),
    "14:05": (SM61.format("14:05"), "The permission answer has no focus test, so it is level B itself, but every "
                                    "second it credits here is level C (SM 2.5)."),
    "14:06": (SM61.format("14:06"), None),
    "14:07": (SM61.format("14:07"), None),
    "14:08": (SM61.format("14:08"), "The bucket holds 303 1/3 s at 14:09:00."),
    "14:09": ("2.5: a slot of unknown focus is never a browser slot, so it holds no lookup",
              "SM 6.1's LOOKUP. Here the slots' focus is unknown, so the bucket drains: READ, within 600 s of the "
              "14:05:25 answer and the 14:11:40 commit."),
    "14:10": ("2.5, 3.3 rules 2 and 3",
              "READ to 14:10:30, then the lead of the 14:12:30 heartbeat, then from 14:10:40 the commit's lead: "
              "the next anchor's lead wins (SM 3.3 rule 3)."),
    "14:11": (SM61.format("14:11"), "The commit's lead to 14:11:40, then the 14:12:30 heartbeat's."),
}
case.rows(NOTES)
lv = case.level_totals()
agent_only = sum(1 for t in rp.running if t not in rp.credit)
expected = exp.doc(
    extra_totals={**lv, "agent_only_s": agent_only, "ai_credited_s": 130 + 25 + 100 + 240,
                  "hand_credited_s": 140 + 60},
    stated=[],
    approx={},
    errata=[],
    notes=[
        "SM 6.1's first cycle, its times and amounts unchanged, on GNOME on Wayland without pairtime's extension: "
        "idle_keys_s is null and the only os.focus says app_class unknown. 695 of the 720 s are credited, as in "
        "SM 6.1, and every credited second is level C with focus_unknown (SM 2.5).",
        "SM 6.1's 120 s of LOOKUP in 14:09 and 14:10 become READ 90, NAV 10 and G 20: a slot of unknown focus is "
        "never a browser slot, so the bucket's 303 1/3 s drain until the next cycle's first heartbeat's lead, "
        "and then the commit's lead takes over by SM 3.3 rule 3.",
        "14:04's 25 s stay X.agent_only. SM 5.2 skips X.other_focus and X.ambiguous where focus is unknown and "
        "reads every project's running turns.",
        "ai_credited_s and hand_credited_s are SM 5.4's split: P, PA, SUP and READ, then NAV and G.",
    ],
)
tl.check(expected["totals"]["credited_s"] == 695, f"credited {expected['totals']['credited_s']}")
tl.check(expected["totals"]["credited_by_reason"] == {"P": 130, "PA": 25, "NAV": 140, "G": 60, "SUP": 100,
                                                      "READ": 240}, f"by reason {expected['totals']['credited_by_reason']}")

anchors = fu.anchors_doc(fx, rp, [
    ("14:00:30", "editor_navigation", 120, 0, "C", ["focus_unknown"],
     "2.3: input in its slot; the editor-focus test passes only because focus is unknown (2.5)"),
    ("14:01:50", "editor_navigation", 120, 0, "C", ["focus_unknown"],
     "2.3: input in its slot; the editor-focus test passes only because focus is unknown (2.5)"),
    ("14:04:00", "substantive_prompt", 320, 0, "C", ["focus_unknown"],
     "2.2 rule 6: input before submit, and the focus test passes only because focus is unknown (2.5); "
     "lead 120 + 300 / 1.5 = 320 s"),
    ("14:05:25", "permission_answer", 15, 10, "B", [],
     "2.3: presence at the answer, 15 s of wait and a 10 s trail; no focus test, so level B, while every second "
     "it credits is level C (2.5)"),
    ("14:11:40", "human_commit", 60, 0, "B", [],
     "2.3: input in its slot and no focus test, so level B; every second it credits is level C (2.5)"),
    ("14:12:30", "editor_navigation", 120, 0, "C", ["focus_unknown"],
     "after the window: the next cycle's first heartbeat, whose lead credits 14:10:30 to 14:10:40 and 14:11:40 "
     "to 14:12:00"),
])
turns = fu.turns_doc(fx, rp, [
    (S, TURN, "14:04:00", "14:06:30", "human_typed", "substantive",
     "2.2 rule 6, passing on unknown focus (2.5): human_typed"),
])
sub = fx.event_at("prompt.submit", c("4:00"))
prompts = {"fixture": NAME, "prompts": [
    {"turn_key": TURN, "t": sub["t"], "local": fx.hhmmss(sub["t"]), "text": None, "len_chars": 300,
     "source": "SM 6.1 quotes no text for its first cycle's prompt; only its 300 typed characters matter"}]}
slots = case.slots(rp)

fu.check_readme(os.path.join(HERE, "README.md"), expected)
fu.write(fx, tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns}, prompts, slots)
