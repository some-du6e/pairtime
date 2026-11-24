# SPDX-License-Identifier: AGPL-3.0-or-later
"""One os.focus unknown an hour before the work, on GNOME on Wayland, so a
re-score from a checkpoint in that hour has no os.focus after its start.
See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import focus as fu  # noqa: E402

NAME = "focus-unknown/e-hour-before"
fx = tl.Fixture(
    NAME,
    title="One os.focus unknown an hour before the work, on GNOME on Wayland",
    date="2026-11-29",
    timezone_name="America/New_York",
    utc_offset_min=-300,
    os_name="linux",
    keys=False,
    device=tl.key("d", "focus-unknown e laptop"),
    boot_id=tl.key("b", "focus-unknown e boot"),
    boot="12:31:06.000",
    seq0=2207,
    last_input="12:59:40.000",
)
at = fx.at
P = tl.key("p", "focus-unknown e quiz-app")
S = tl.key("s", "focus-unknown e claude session")
CC = dict(harness="claude_code", version="2.1.328", session=S, project=P)
TURN = tl.key("t", "focus-unknown e turn 1")
FILE = tl.key("f", "focus-unknown e quiz.js")
rand = tl.Rand("focus-unknown e")
T0, END = at("13:00:00"), at("14:08:00")
FOCUS = at("12:59:52")


def inp(t, is_key):
    tl.check(t % tl.SLOT_MS, f"input at {fx.hhmmss(t)} is off a slot boundary")
    fx.inputs.append((t, is_key))


def every(a, b, lo, hi, is_key):
    t = at(a)
    while t <= at(b):
        if t % tl.SLOT_MS == 0:
            t += 100
        inp(t, is_key)
        t += rand.between(lo, hi)


# 12:59:52, Sam starts Claude Code in a terminal for quiz-app. Its
# SessionStart hook makes a project context live, so the agent records the
# one os.focus it can, app_class unknown (SM 2.5, DESIGN 3.3). A harness
# session process runs from then on, so the context stays live and the
# agent samples every 15 s (DESIGN 3.3).
every("12:59:44.300", "12:59:48.000", 300, 600, True)  # claude, Enter
fx.emit(FOCUS, "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "acceptEdits"},
        source="hook", via="hook", sample=True, **CC)
fx.emit(FOCUS, "os.focus", {"app_class": "unknown"}, source="os", via="os", sample=True)
fx.ticks("13:00:00", "14:08:15", tl.SLOT_MS)

# 12:59:55 to 13:59:50, before the work: videos in the browser, which
# pairtime can't see, clicking and scrolling every 10 to 50 s. Then he
# clicks the terminal.
every("12:59:55.700", "13:59:40.000", 10_000, 50_000, False)
inp(at("13:59:49.900"), False)

# 14:00 to 14:02, he reads quiz.js in the terminal; 14:02 to 14:03, he types
# a 90-character prompt.
every("14:00:02.600", "14:01:58.000", 5_000, 9_000, False)
every("14:02:00.700", "14:02:59.400", 300, 500, True)
inp(at("14:02:59.800"), True)
fx.emit(at("14:03:00"), "prompt.submit",
        {"turn_key": TURN, "len_chars": 90, "pasted_chars": 0, "typed_len_est": 90, "origin_declared": "none",
         "matches_pending_cron": False, "answers_question": False, "low_effort": False},
        source="hook", via="hook", sample=True, **CC)
fx.emit(at("14:03:30"), "agent.output",
        {"turn_key": TURN, "words": 30, "code_lines": 0, "is_final": False, "asks_question": False,
         "offers_options": False},
        source="log", via="log", **CC)
added = rand.between(10, 16)
fx.emit(at("14:04:00"), "agent.tool",
        {"tool_class": "edit", "files": [FILE], "lines_added": added, "lines_removed": 20 - added,
         "visible_output_lines": 0, "t_start": at("14:03:51.200"), "t_end": at("14:04:00")},
        source="hook", via="hook", sample=True, **CC)
fx.emit(at("14:05:00"), "agent.output",
        {"turn_key": TURN, "words": 120, "code_lines": 0, "is_final": True, "asks_question": False,
         "offers_options": False},
        source="log", via="log", **CC)
fx.emit(at("14:05:00"), "agent.stop", {"turn_key": TURN, "stop_reason": "end_turn", "stop_hook_active": False},
        source="hook", via="hook", sample=True, **CC)
# He watches, then reads the summary and the diff, scrolling, to 14:08.
every("14:03:04.100", "14:08:12.000", 5_000, 9_000, False)

events = fx.build()

# The credit, worked out by hand from SM 2.5 (README.md).
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.5; DESIGN 6.5", evidence="C", witness="witnessed",
                  projects={P: "quiz-app"}, start="13:00", end="14:08")
RUNS = [
    ("14:00:00", "14:03:00", "P"),
    ("14:03:00", "14:03:20", "SUP"),
    ("14:03:20", "14:03:30", "X.agent_only"),
    ("14:03:30", "14:03:40", "SUP"),
    ("14:03:40", "14:04:00", "X.agent_only"),
    ("14:04:00", "14:05:00", "SUP"),
    ("14:05:00", "14:07:10", "READ"),
    ("14:07:10", "14:08:00", "X.no_anchor"),
]
case = fu.Case(fx, exp, runs=RUNS, level="C", level_c=[("14:00:00", "14:08:00")],
               turns=[(at("14:03:00"), at("14:05:00"))])
rp = fu.Replay(events, T0, END)
case.check(rp)
tl.check(all(f == ("unknown", None) for f in rp.slot_focus.values()), "focus is unknown in every slot")
focus_events = [e for e in events if e["kind"] == "os.focus"]
tl.check(len(focus_events) == 1 and focus_events[0]["t"] == FOCUS, "one os.focus, at 12:59:52")

# DESIGN 6.5's checkpoints fall on UTC minutes divisible by 15 while the
# project has records in the last 24 hours: here from 13:00 local, 18:00Z.
# Every one in the window comes after the only os.focus.
CHECKPOINTS = [t for t in range(T0, END, tl.MINUTE_MS) if (t // tl.MINUTE_MS) % 15 == 0]
tl.check([fx.hhmm(t) for t in CHECKPOINTS] == ["13:00", "13:15", "13:30", "13:45", "14:00"],
         f"checkpoints {[fx.hhmm(t) for t in CHECKPOINTS]}")
tl.check(all(t > FOCUS for t in CHECKPOINTS), "every checkpoint is after the os.focus")
tl.check(at("14:00:00") - FOCUS >= 3_600_000 - 10_000, "the os.focus is about an hour before the work")

HOUR = ("5.1: no bucket", "Videos in the browser, with input every 10 to 50 s and focus unknown. No anchor, no "
                          "reading and no running turn, so nothing is credited and no minute has a bucket.")
NOTES = {
    "13:00-13:59": HOUR,
    "14:00": ("2.5: the lead is realized in a slot of unknown focus",
              "The prompt's lead, 120 + 90 / 1.5 = 180 s, from 14:00:00. A re-score from the 14:00 checkpoint "
              "starts here, with no os.focus after it."),
    "14:01": ("2.3", None),
    "14:02": ("2.3", "The typing part."),
    "14:03": ("2.4 and 5.2", "Nothing to read from 14:03:20 to 14:03:30 and 14:03:40 to 14:04:00: X.agent_only."),
    "14:04": ("2.4", None),
    "14:05": ("2.4", None),
    "14:06": ("2.4", None),
    "14:07": ("2.4 and 5.2", "The bucket runs dry at 14:07:10, and no turn runs: X.no_anchor."),
}
case.rows(NOTES)
lv = case.level_totals()
agent_only = sum(1 for t in rp.running if t not in rp.credit)
expected = exp.doc(
    extra_totals={**lv, "agent_only_s": agent_only, "ai_credited_s": lv["level_c_s"], "hand_credited_s": 0,
                  "checkpoints_after_focus": len(CHECKPOINTS), "focus_age_at_work_s": (at("14:00:00") - FOCUS) // 1000},
    stated=[],
    approx={},
    errata=[],
    notes=[
        "WP-FX8's (e): GNOME on Wayland without pairtime's extension, where the only os.focus, app_class unknown, "
        "is recorded at 12:59:52, when Claude Code's SessionStart hook makes a project context live. Nothing "
        "else is recorded about focus, so the work an hour later is scored with that record in force.",
        "The hour before the work credits nothing: presence and unknown focus pass the focus and presence "
        "tests, but there is no anchor and nothing to read (SM 2.3, 2.4).",
        "The checkpoints at 13:00, 13:15, 13:30, 13:45 and 14:00 local, UTC minutes divisible by 15 (DESIGN 6.5), "
        "all come after the only os.focus. A re-score from any of them reproduces these rows only if the "
        "checkpoint carries the os.focus in force, app_class unknown (DESIGN 6.5, PLAN WP-SC5). One that started "
        "with focus none, as before a client's first os.focus, would credit nothing.",
        "Every credited second is level C with focus_unknown.",
    ],
)
tl.check(expected["totals"]["credited_s"] == 400, f"credited {expected['totals']['credited_s']}")

anchors = fu.anchors_doc(fx, rp, [
    ("14:03:00", "substantive_prompt", 180, 0, "C", ["focus_unknown"],
     "2.2 rule 6: input before submit, and the focus test passes only because focus is unknown (2.5); lead 120 + "
     "90 / 1.5 = 180 s"),
])
turns = fu.turns_doc(fx, rp, [
    (S, TURN, "14:03:00", "14:05:00", "human_typed", "substantive", "2.2 rule 6 on unknown focus"),
])
sub = fx.event_at("prompt.submit", at("14:03:00"))
prompts = {"fixture": NAME, "prompts": [
    {"turn_key": TURN, "t": sub["t"], "local": fx.hhmmss(sub["t"]), "text": None, "len_chars": 90,
     "source": "invented for WP-FX8; only its 90 typed characters matter"}]}
slots = case.slots(rp)

fu.check_readme(os.path.join(HERE, "README.md"), expected)
fu.write(fx, tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns}, prompts, slots)
