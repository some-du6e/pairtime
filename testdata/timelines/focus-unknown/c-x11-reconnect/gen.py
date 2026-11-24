# SPDX-License-Identifier: AGPL-3.0-or-later
"""X11, with 30 s of unknown focus inside a prompt's lead while the agent
re-makes its X connection. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import focus as fu  # noqa: E402

NAME = "focus-unknown/c-x11-reconnect"
fx = tl.Fixture(
    NAME,
    title="X11 with 30 s of unknown focus inside a prompt's lead, while the X connection is re-made",
    date="2026-11-26",
    timezone_name="Australia/Sydney",
    utc_offset_min=660,
    os_name="linux",
    keys=False,
    device=tl.key("d", "focus-unknown c desktop"),
    boot_id=tl.key("b", "focus-unknown c boot"),
    boot="07:41:52.000",
    seq0=3390,
    last_input="09:58:47.500",
)
at = fx.at
P = tl.key("p", "focus-unknown c weather-cli")
S = tl.key("s", "focus-unknown c claude session")
CC = dict(harness="claude_code", version="2.1.328", session=S, project=P)
TURN = tl.key("t", "focus-unknown c turn 1")
TERMINAL = tl.key("a", "focus-unknown c terminal")
FILES = [tl.key("f", f"focus-unknown c file {i}") for i in range(2)]
rand = tl.Rand("focus-unknown c")
T0, END = at("10:00:00"), at("10:10:00")
DROP, BACK = at("10:02:20"), at("10:02:50")


def inp(t, is_key):
    tl.check(t % tl.SLOT_MS, f"input at {fx.hhmmss(t)} is off a slot boundary")
    fx.inputs.append((t, is_key))


def every(a, b, lo, hi, is_key):
    t = a
    while t <= b:
        if t % tl.SLOT_MS == 0:
            t += 100
        inp(t, is_key)
        t += rand.between(lo, hi)


def terminal(t, hosted):
    data = {"app_class": "terminal", "app_hash": TERMINAL}
    if hosted:
        data["hosted_sessions"] = [S]
        data["focus_project"] = P
    return fx.emit(t, "os.focus", data, source="os", via="os", sample=True)


# Before the window. Lee clicks the terminal, which makes a project context
# live (DESIGN 3.3), and starts Claude Code there. The SessionStart hook
# changes what the window hosts, so the agent walks it again and writes a
# second os.focus with the session in hosted_sessions (SM 3.2).
inp(at("09:59:19.900"), False)
terminal(at("09:59:20"), hosted=False)
fx.ticks("09:59:30", "10:10:15", tl.SLOT_MS)
fx.typing("09:59:25.000", "09:59:28.600", 600)  # claude, Enter
fx.emit(at("09:59:30"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "acceptEdits"},
        source="hook", via="hook", sample=True, **CC)
terminal(at("09:59:30"), hosted=True)
inp(at("09:59:41.700"), False)
inp(at("09:59:55.200"), False)

# 10:00 to 10:02, he reads the bug report he pasted into the session's
# scrollback, scrolling. 10:02 to 10:04, he types the 180-character prompt.
every(at("10:00:02.300"), at("10:01:58.000"), 5_000, 9_000, False)
every(at("10:02:00.800"), at("10:03:59.400"), 300, 500, True)
inp(at("10:03:59.800"), True)

# 10:02:20, the X connection the agent follows focus on drops, and it
# records that focus is unknown. The new connection's first report, at
# 10:02:50, names the terminal again, and the walk finds the session.
# Idle sampling has a connection of its own, which stays up.
fx.emit(DROP, "os.focus", {"app_class": "unknown"}, source="os", via="os", sample=True)
terminal(BACK, hosted=True)

fx.emit(at("10:04:00"), "prompt.submit",
        {"turn_key": TURN, "len_chars": 180, "pasted_chars": 0, "typed_len_est": 180, "origin_declared": "none",
         "matches_pending_cron": False, "answers_question": False, "low_effort": False},
        source="hook", via="hook", sample=True, **CC)


def output(t, words, final=False):
    fx.emit(t, "agent.output",
            {"turn_key": TURN, "words": words, "code_lines": 0, "is_final": final, "asks_question": False,
             "offers_options": False},
            source="log", via="log", **CC)


def edit(t_end, t_start, file):
    added = rand.between(14, 20)
    fx.emit(t_end, "agent.tool",
            {"tool_class": "edit", "files": [file], "lines_added": added, "lines_removed": 30 - added,
             "visible_output_lines": 0, "t_start": t_start, "t_end": t_end},
            source="hook", via="hook", sample=True, **CC)


# 10:04 to 10:06, the turn, which he watches; then he reads until 10:10.
output(at("10:04:30"), 45)
edit(at("10:05:00"), at("10:04:52.500"), FILES[0])
edit(at("10:05:30"), at("10:05:24.000"), FILES[1])
output(at("10:06:00"), 120, final=True)
fx.emit(at("10:06:00"), "agent.stop", {"turn_key": TURN, "stop_reason": "end_turn", "stop_hook_active": False},
        source="hook", via="hook", sample=True, **CC)
for local in ("10:04:28.300", "10:04:57.100", "10:05:26.400", "10:05:52.900"):
    inp(at(local), False)
every(at("10:06:03.200"), at("10:10:12.000"), 5_000, 9_000, False)

events = fx.build()

# The credit, worked out by hand from SM 2.5 (README.md).
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.5", evidence="B", witness="witnessed",
                  projects={P: "weather-cli"}, start="10:00", end="10:10")
RUNS = [
    ("10:00:00", "10:04:00", "P"),
    ("10:04:00", "10:04:20", "SUP"),
    ("10:04:20", "10:04:30", "X.agent_only"),
    ("10:04:30", "10:04:45", "SUP"),
    ("10:04:45", "10:05:00", "X.agent_only"),
    ("10:05:00", "10:06:00", "SUP"),
    ("10:06:00", "10:10:00", "READ"),
]
case = fu.Case(fx, exp, runs=RUNS, level="B", level_c=[("10:02:15", "10:02:45")],
               turns=[(at("10:04:00"), at("10:06:00"))])
rp = fu.Replay(events, T0, END)
case.check(rp)
unknown_slots = [fx.hhmmss(s) for s, f in sorted(rp.slot_focus.items()) if f[0] == "unknown"]
tl.check(unknown_slots == ["10:02:15", "10:02:30"], f"the slots of unknown focus are {unknown_slots}")
tl.check(all(f == ("terminal", P) for f in rp.slot_focus.values() if f[0] != "unknown"),
         "every other slot is the terminal's")
tl.check(BACK - DROP == 30_000, "30 s of unknown focus")

NOTES = {
    "10:00": ("2.3: the think part of the prompt's lead", "120 + 180 / 1.5 = 240 s, from 10:00:00. Level B: the "
                                                          "terminal hosting the session holds every slot."),
    "10:01": ("2.3", None),
    "10:02": ("2.5: a slot that unknown focus holds longest is level C",
              "Focus is unknown from 10:02:20 to 10:02:50. It holds the slots from 10:02:15 and 10:02:30 longest, "
              "so their 30 s of the lead are level C with focus_unknown; the slot from 10:02:45 is the "
              "terminal's, 10 s to 5, and stays B."),
    "10:03": ("2.3", "The prompt is sent at 10:04:00 with the terminal in front, so rule 6 passes on known focus "
                     "and the anchor is level B, not focus_unknown."),
    "10:04": ("2.4", "Nothing new to read from 10:04:20 to 10:04:30 and from 10:04:45 to 10:05:00: X.agent_only."),
    "10:05": ("2.4", None),
    "10:06": ("2.4: reading takes the level of the anchor that qualifies it", "The prompt, level B."),
    "10:07": ("2.4", None),
    "10:08": ("2.4", None),
    "10:09": ("2.4", None),
}
case.rows(NOTES)
lv = case.level_totals()
agent_only = sum(1 for t in rp.running if t not in rp.credit)
expected = exp.doc(
    extra_totals={**lv, "agent_only_s": agent_only, "ai_credited_s": 575, "hand_credited_s": 0,
                  "unknown_focus_s": (BACK - DROP) // 1000},
    stated=[],
    approx={},
    errata=[],
    notes=[
        "WP-FX8's (c): X11 without the key counter, so every credited second is level B, except where the agent "
        "couldn't tell which window was in front. Its X connection for focus drops at 10:02:20 and is re-made "
        "by 10:02:50, inside the prompt's lead.",
        "Only the seconds of the two slots that unknown focus holds longest, 10:02:15 to 10:02:45, are level C "
        "with focus_unknown: 30 s, though the 30 s of unknown focus start and end mid-slot (SM 2.5). No act "
        "falls in them, so no anchor passed its focus test only through unknown focus, and the prompt, sent "
        "with the terminal in front, stays level B, as does the reading it qualifies.",
        "evidence is the level of every credited second outside those two slots; slots.json gives each slot's.",
    ],
)
tl.check(expected["totals"]["credited_s"] == 575, f"credited {expected['totals']['credited_s']}")
tl.check(lv == {"level_a_s": 0, "level_b_s": 545, "level_c_s": 30, "focus_unknown_s": 30}, f"levels {lv}")

anchors = fu.anchors_doc(fx, rp, [
    ("10:04:00", "substantive_prompt", 240, 0, "B", [],
     "2.2 rule 6: input before submit and the terminal hosting the session in front at submit; lead 120 + "
     "180 / 1.5 = 240 s. Level B, though 30 s of its lead are C (2.5)"),
])
turns = fu.turns_doc(fx, rp, [
    (S, TURN, "10:04:00", "10:06:00", "human_typed", "substantive", "2.2 rule 6, with focus known at submit"),
])
sub = fx.event_at("prompt.submit", at("10:04:00"))
prompts = {"fixture": NAME, "prompts": [
    {"turn_key": TURN, "t": sub["t"], "local": fx.hhmmss(sub["t"]), "text": None, "len_chars": 180,
     "source": "invented for WP-FX8; only its 180 typed characters matter"}]}
slots = case.slots(rp)

fu.check_readme(os.path.join(HERE, "README.md"), expected)
fu.write(fx, tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns}, prompts, slots)
