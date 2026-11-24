# SPDX-License-Identifier: AGPL-3.0-or-later
"""A script drives Claude Code through tmux every 5 minutes while the person
plays a browser game, on GNOME on Wayland, where pairtime can't tell which
window is in front. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import focus as fu  # noqa: E402
import ptygame  # noqa: E402

NAME = "focus-unknown/b-pty-game"
fx = tl.Fixture(
    NAME,
    title="A pty driver prompting every 5 minutes during a browser game, on GNOME on Wayland",
    date="2026-11-25",
    timezone_name="Europe/London",
    utc_offset_min=0,
    os_name="linux",
    keys=False,
    device=tl.key("d", "focus-unknown b gnome desktop"),
    boot_id=tl.key("b", "focus-unknown b gnome boot"),
    boot="17:12:09.000",
    seq0=8812,
    last_input="19:54:40.000",
)
at = fx.at
k = ptygame.build(fx, x11=False)
P, S = k["project"], k["session"]
events = fx.build()
T0, END = at("20:00:00"), at("20:20:00")

# The credit, worked out by hand from SM 2.2 and 2.5 (README.md). Each of
# the four cycles: the prompt's 180 s lead, then its turn's reading.
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 and 2.5", evidence="C", witness="witnessed",
                  projects={P: "idle-farm"}, start="20:00", end="20:20")
RUNS = []
for local, _ in ptygame.PROMPTS:
    T = at(local)
    RUNS += [
        (fx.hhmmss(T - 180_000), local, "P"),
        (local, fx.hhmmss(T + 35_000), "SUP"),
        (fx.hhmmss(T + 35_000), fx.hhmmss(T + 50_000), "X.agent_only"),
        (fx.hhmmss(T + 50_000), fx.hhmmss(T + 100_000), "SUP"),
        (fx.hhmmss(T + 100_000), fx.hhmmss(T + 120_000), "READ"),
    ]
TURNS = [(at(local), at(local) + 100_000) for local, _ in ptygame.PROMPTS]
case = fu.Case(fx, exp, runs=RUNS, level="C", level_c=[("20:00:00", "20:20:00")], turns=TURNS)
rp = fu.Replay(events, T0, END)
case.check(rp)
tl.check(all(f == ("unknown", None) for f in rp.slot_focus.values()), "focus is unknown in every slot")
tl.check(not any(v[2] in ("A", "B") for v in rp.credit.values()), "no second at level A or B")

LEAD = ("2.2 rule 6 and 2.5: the script's prompt is human_typed",
        "The prompt's lead, 120 + 90 / 1.5 = 180 s. The game's input passes rule 6's input test, and its focus "
        "test passes for every session because focus is unknown, so the pty driver's prompt is human_typed.")
RUN1 = ("2.4 and 2.5: the turn's reading drains in a slot of unknown focus",
        "The bucket resets to 20 s at the prompt and 45 words add 15 s at T + 20: SUP to T + 35. Nothing to read "
        "until the edit at T + 50 while the turn runs, the turn the rules say a person started.")
RUN2 = ("2.4 and 2.5", "The 16-line edit adds 120 s: SUP to the stop at T + 100, then READ until the next "
                       "prompt's lead starts at T + 120 (SM 3.3 rule 2).")
RUN2_LAST = ("2.4 and 2.5", "The 16-line edit adds 120 s: SUP to the stop at T + 100, then READ to the window's "
                            "end at T + 120. No prompt follows, so the 70 s left at 20:20:00 lie after the window.")
NOTES = {}
for local, _ in ptygame.PROMPTS:
    T = at(local)
    for m in range(T - 180_000, T, 60_000):
        NOTES[fx.hhmm(m)] = LEAD
    NOTES[fx.hhmm(T)] = RUN1
    NOTES[fx.hhmm(T + 60_000)] = RUN2 if local != ptygame.PROMPTS[-1][0] else RUN2_LAST
case.rows(NOTES)
lv = case.level_totals()
agent_only = sum(1 for t in rp.running if t not in rp.credit)
expected = exp.doc(
    extra_totals={**lv, "agent_only_s": agent_only, "ai_credited_s": lv["level_c_s"], "hand_credited_s": 0,
                  "human_typed_prompts": 4, "no_input_prompts": 0},
    stated=[],
    approx={},
    errata=[],
    notes=[
        "WP-FX8's (b): a script sends Claude Code a prompt every 300 s through tmux send-keys, which writes to "
        "the pty, while the person plays a game in a browser. GNOME on Wayland gives pairtime no focus, so the "
        "only os.focus says app_class unknown, and SM 2.2 rule 6's focus test passes for every session: the "
        "game's input makes each scripted prompt human_typed (SM 2.2, 2.5).",
        "1,140 of the 1,200 s are credited, every one at level C with focus_unknown, and none at level A or B. "
        "The X11 twin, b-pty-game-x11, has the same acts with the browser in front and credits nothing.",
        "Each cycle: the prompt's 180 s lead, SUP 85, READ 20 and X.agent_only 15 while the turn runs with "
        "nothing new on screen. In the first three cycles the 70 s left in the bucket at T + 120 are never "
        "read, because the next prompt's lead beats the bucket (SM 3.3 rule 2) and the next prompt resets it. "
        "The last cycle's 70 s lie after the window.",
    ],
)
tl.check(expected["totals"]["credited_s"] == 1140, f"credited {expected['totals']['credited_s']}")

anchors = fu.anchors_doc(fx, rp, [
    (local, "substantive_prompt", 180, 0, "C", ["focus_unknown"],
     "2.2 rule 6: the game's input in the 10 s before submit, and the focus test passes only because focus is "
     "unknown (2.5); lead 120 + 90 / 1.5 = 180 s")
    for local, _ in ptygame.PROMPTS
])
turns = fu.turns_doc(fx, rp, [
    (S, k["turns"][i], local, fx.hhmmss(at(local) + 100_000), "human_typed", "substantive",
     "2.2 rule 6, passing on unknown focus (2.5): human_typed, though a script typed it")
    for i, (local, _) in enumerate(ptygame.PROMPTS)
])
prompts = ptygame.prompts_doc(fx, events, k, NAME)
slots = case.slots(rp)

fu.check_readme(os.path.join(HERE, "README.md"), expected)
fu.write(fx, tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns}, prompts, slots)
