# SPDX-License-Identifier: AGPL-3.0-or-later
"""b-pty-game's X11 twin: the same script and the same game, on a desktop
that reports the browser in front. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import focus as fu  # noqa: E402
import ptygame  # noqa: E402

NAME = "focus-unknown/b-pty-game-x11"
fx = tl.Fixture(
    NAME,
    title="b-pty-game's X11 twin, with the browser in front",
    date="2026-11-25",
    timezone_name="Europe/London",
    utc_offset_min=0,
    os_name="linux",
    keys=False,
    device=tl.key("d", "focus-unknown b x11 desktop"),
    boot_id=tl.key("b", "focus-unknown b x11 boot"),
    boot="17:12:09.000",
    seq0=8812,
    last_input="19:54:40.000",
)
at = fx.at
k = ptygame.build(fx, x11=True)
P, S = k["project"], k["session"]
events = fx.build()
T0, END = at("20:00:00"), at("20:20:00")

# The credit, worked out by hand from SM 2.2 and 5.2 (README.md): none. Each
# turn's two minutes have a bucket, because an agent runs, and every second
# of them is X.other_focus.
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2, 2.5 and 5.2", evidence="B", witness="witnessed",
                  projects={P: "idle-farm"}, start="20:00", end="20:20")
RUNS = [(local, fx.hhmmss(at(local) + 120_000), "X.other_focus") for local, _ in ptygame.PROMPTS]
TURNS = [(at(local), at(local) + 100_000) for local, _ in ptygame.PROMPTS]
case = fu.Case(fx, exp, runs=RUNS, level="B", level_c=[], turns=TURNS)
rp = fu.Replay(events, T0, END)
case.check(rp)
tl.check(all(f == ("browser", None) for f in rp.slot_focus.values()), "the browser is in front in every slot")
tl.check(not rp.anchors and not rp.credit, "no anchor and no credit")

NOTES = {}
for local, _ in ptygame.PROMPTS:
    T = at(local)
    NOTES[fx.hhmm(T)] = ("5.2: X.other_focus comes before the turn step",
                         "The script's turn runs from T, and the browser is in front: a browser slot outside a "
                         "lookup is X.other_focus.")
    NOTES[fx.hhmm(T + 60_000)] = ("5.2", "The turn stops at T + 100.")
for a, b in (("20:00", "20:02"), ("20:05", "20:07"), ("20:10", "20:12"), ("20:15", "20:17")):
    NOTES[f"{a}-{b}"] = ("5.1: no bucket, since nothing is credited and no agent runs",
                         "The person plays; the script waits for the next prompt.")
case.rows(NOTES)
lv = case.level_totals()
expected = exp.doc(
    extra_totals={**lv, "agent_only_s": len(rp.running), "ai_credited_s": 0, "hand_credited_s": 0,
                  "human_typed_prompts": 0, "no_input_prompts": 4},
    stated=[],
    approx={},
    errata=[],
    notes=[
        "b-pty-game's acts and times on X11, where _NET_ACTIVE_WINDOW reports the browser in front from "
        "19:55:20. Each scripted prompt fails SM 2.2 rule 6's focus test, since the browser hosts no session, "
        "and rule 7 needs the same focus, so each is no_input (rule 8): no anchor, and nothing is credited.",
        "Every uncredited second of a bucket minute is X.other_focus, which SM 5.2 puts before the turn step. "
        "The four no_input prompts raise the reviewer flag no_input_prompts (SM 2.2 rule 8, 8.7).",
        "evidence is B, the level this machine's credit would have; there is none.",
    ],
)
tl.check(expected["totals"]["credited_s"] == 0, f"credited {expected['totals']['credited_s']}")

anchors = fu.anchors_doc(fx, rp, [])
turns = fu.turns_doc(fx, rp, [
    (S, k["turns"][i], local, fx.hhmmss(at(local) + 100_000), "no_input", None,
     "2.2 rules 6 to 8: input before submit, but the browser in front hosts no session, so no_input")
    for i, (local, _) in enumerate(ptygame.PROMPTS)
])
prompts = ptygame.prompts_doc(fx, events, k, NAME)
slots = case.slots(rp)

fu.check_readme(os.path.join(HERE, "README.md"), expected)
fu.write(fx, tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns}, prompts, slots)
