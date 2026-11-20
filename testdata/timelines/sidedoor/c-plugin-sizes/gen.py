# SPDX-License-Identifier: AGPL-3.0-or-later
"""WP-FX4 (c): a plugin posting turns whose outputs claim 50,000 words, with
a mouse jiggler on Windows. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import sidedoor as sd  # noqa: E402

tl = sd.tl

fx = tl.Fixture(
    "sidedoor/c-plugin-sizes",
    title="A plugin reporting 50,000-word outputs, with a jiggler on Windows",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="windows",
    keys=False,
    device=tl.key("d", "sidedoor c laptop"),
    boot_id=tl.key("b", "sidedoor c boot"),
    boot="09:31:05.000",
    seq0=44730,
    last_input="13:57:51.400",
)
at = fx.at

P = tl.key("p", "sidedoor c project")
S = tl.key("s", "sidedoor c plugin session")
APP = tl.key("a", "sidedoor c WindowsTerminal.exe")
# The plugin writes "source": "hook" into its events; the agent records how
# they actually arrived, over HTTP, as via plugin (DESIGN 5.3, 4.1 E-2).
PLUGIN = dict(source="hook", via="plugin", harness="other", session=S, project=P)

PROMPTS = [
    "the scoreboard flickers when two players score in the same frame, can you batch the updates so it "
    "redraws once per tick and add a test that covers it.",
    "add an options menu with a volume slider and a key rebinding screen, then save the settings to a json "
    "file next to the saves and load them at startup.",
]
TURNS = [at("14:04:40"), at("14:10:40")]
WORDS = 50_000

# The jiggler moves every 12 s, 2 s before each prompt among other times.
fx.pointer_every("13:58:02", "14:12:00", 12_000)
fx.ticks("13:58:00", "14:12:00", tl.SLOT_MS)

# The plugin's manifest names the terminal's harness process among its
# hosts, so the agent's process walk lists the plugin's session under the
# focused window (DESIGN 5.3).
fx.emit(at("13:58:00"), "os.focus",
        {"app_class": "terminal", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
turn_keys = [tl.key("t", f"sidedoor c turn {k}") for k in range(1, len(TURNS) + 1)]
for t, k, text in zip(TURNS, turn_keys, PROMPTS):
    # HTTP events take no presence sample (DESIGN 3.3 samples at hook, focus
    # and power events).
    fx.emit(t, "prompt.submit",
            {"turn_key": k, "len_chars": len(text), "pasted_chars": 0, "typed_len_est": len(text),
             "origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
             "low_effort": False},
            **PLUGIN)
    fx.emit(t + 40_000, "agent.output",
            {"turn_key": k, "words": WORDS, "code_lines": 0, "is_final": True, "asks_question": False,
             "offers_options": False},
            **PLUGIN)
    fx.emit(t + 40_000, "agent.stop", {"turn_key": k, "stop_reason": "end_turn", "stop_hook_active": False},
            **PLUGIN)

events = fx.build()
prompts_ev = [fx.event_at("prompt.submit", t) for t in TURNS]
outputs = [fx.event_at("agent.output", t + 40_000) for t in TURNS]
stops = [fx.event_at("agent.stop", t + 40_000) for t in TURNS]

# Checks of the timing choices (README.md).
tl.check(all(len(x) == 150 for x in PROMPTS), "every prompt is 150 characters")
tl.check(sd.input_in_every_slot(fx, at("13:58:00"), at("14:12:00")), "the jiggler puts input in every slot")
for t in TURNS:
    tl.check(sd.samples_show_input_before(fx, t, sd.KEYS_PRE_S * 1000),
             f"the samples show input in the 10 s before the {fx.hhmmss(t)} prompt (SM 2.2 rule 6)")

typing = sd.typing_s(150)
lead = sd.THINK_LEAD_S + typing
tl.check(typing == 100 and lead == 220, "lead 120 s think + 100 s typing")
claimed = sd.output_fill(words=WORDS)

sweep = sd.Sweep(at("14:00:00"), at("14:12:00"))
for t in TURNS:
    # SM 2.3: with no keyboard signal on this machine, the whole lead needs
    # presence only. DESIGN restricts the typing part for track anchors
    # (5.4), not for plugin ones.
    sweep.anchor(t, lead_s=lead, typing_part_s=typing, reason="P", level="C")
    sweep.reset(t, sd.R_TURN_BASE_S)
    # DESIGN 5.2: the turn's fill stops at R_TURN_BASE_S, which its base
    # already reaches, so the 50,000 words add nothing.
    sweep.fill(t + 40_000, 0)
    sweep.run(t, t + 40_000)

exp = tl.Expected(fx, sm="docs/PLAN.md WP-FX4 (c); DESIGN 5.2, 5.3, 6.2 D2 and D19, 6.4", evidence="C",
                  witness="witnessed", projects={P: "the project the plugin's session claims"},
                  start="14:00:00", end="14:12:00")
exp.row("14:00", bucket=False, codes=["X.no_anchor"])
for m in ("14:01", "14:02", "14:03"):
    exp.row(m, credit={P: {"P": 60}}, uncredited={}, agent=0, note="The first prompt's 220 s lead, from 14:01:00.")
exp.row("14:04", credit={P: {"P": 40, "SUP": 20}}, uncredited={}, agent=20,
        note="The prompt at 14:04:40, then its turn's 20 s base while the claimed agent runs.")
exp.row("14:05", credit={}, uncredited={"X.agent_only": 20, "X.no_anchor": 40}, agent=20,
        note="The bucket is empty at 14:05:00 and the 50,000 words at 14:05:20 add nothing to it.")
exp.row("14:06", bucket=False, codes=["X.no_anchor"])
for m in ("14:07", "14:08", "14:09"):
    exp.row(m, credit={P: {"P": 60}}, uncredited={}, agent=0, note="The second prompt's lead, from 14:07:00.")
exp.row("14:10", credit={P: {"P": 40, "SUP": 20}}, uncredited={}, agent=20)
exp.row("14:11", credit={}, uncredited={"X.agent_only": 20, "X.no_anchor": 40}, agent=20)
sd.check_rows(exp, sweep, P)

side = sd.SideDoor(fx, exp, case="c", wp="docs/PLAN.md WP-FX4 (c)",
                   rules=["DESIGN 5.2", "DESIGN 5.3", "DESIGN 6.2 D2", "DESIGN 6.2 D19", "DESIGN 6.4"])
for p, o, s in zip(prompts_ev, outputs, stops):
    side.record(p, anchor=True, level="C", labels=["via_plugin"],
                why="SM 2.2 rule 6 holds, as DESIGN 5.3 requires of a plugin prompt: the jiggler gave input in the "
                    "10 s before it and the focused window hosts the session. 150 typed characters, substantive. "
                    "Level C: a plugin anchor without keyboard input (5.2, 6.4). Plugin records never count "
                    "toward logged_s or ai_logged_s (D19).")
    side.record(o, why="Claims 50,000 words, which would fill 16,666.667 s. DESIGN 5.2 stops a plugin turn's "
                       "fill at R_TURN_BASE_S, which the base already reaches, so it adds nothing. Never counts "
                       "toward logged_s (D19).")
    side.record(s, why="Ends the turn the plugin claims. Never counts toward logged_s (D19).")
for p, k in zip(prompts_ev, turn_keys):
    side.turn(p, turn_key=k, backed=False, output_fill_s=claimed, fill_s=sd.R_TURN_BASE_S,
              why="DESIGN 5.2: output sizes from via plugin fill the bucket only up to R_TURN_BASE_S = 20 s per "
                  "turn when no hook or transcript record backs the turn, and none does.")

log_ms, ai_ms = side.totals()
lv = sd.levels(sweep)
tl.check(lv == {"A": 0, "B": 0, "C": 480}, f"levels {lv}")
tl.check(2 * (220 + 20) == 480, "closed form: two leads and two bases")

expected = exp.doc(
    extra_totals={
        "credited_s": 480,
        "level_a_s": 0,
        "level_b_s": 0,
        "level_c_s": lv["C"],
        "via_track_s": 0,
        "logged_s": tl.Seconds(log_ms),
        "ai_logged_s": tl.Seconds(ai_ms),
        "level_c_cap_s": tl.Seconds(min(lv["C"] * 1000, ai_ms // 3)),
    },
    stated=["level_a_s", "level_b_s", "logged_s", "ai_logged_s"],
    approx={},
    errata=[],
    notes=[
        "Every credited second rests on a plugin anchor: level C, labeled via_plugin.",
        "Uncapped, each 50,000-word output would refill the bucket to its 600 s cap and turn the 100 s from "
        "14:05:20 to 14:07:00, and the 40 s from 14:11:20 to 14:12:00, into READ. The side-door cap turns that "
        "into nothing.",
        "logged_s and ai_logged_s are 0: every record but the OS ones arrived via plugin (D19).",
    ],
)

anchors = {
    "fixture": fx.name,
    "anchors": [
        tl.anchor(fx, p, kind="substantive_prompt", origin="human_typed", prompt_class="substantive", lead_s=220,
                  trail_s=0, lead_from="params", level="C",
                  sm="SM 2.2 rule 6 and SM 2.3: lead 120 + 150 / 1.5 = 220 s; level C by DESIGN 6.4")
        for p in prompts_ev
    ],
    "turns": [
        tl.turn(fx, p["t"], s["t"], session=S, turn_key=k, origin="human_typed", prompt_class="substantive",
                sm="the plugin's own prompt, output and stop")
        for p, s, k in zip(prompts_ev, stops, turn_keys)
    ],
}

prompts = {
    "fixture": fx.name,
    "prompts": [
        {"turn_key": k, "t": p["t"], "local": fx.hhmmss(p["t"]), "text": text, "len_chars": len(text),
         "source": "invented: the prompt the plugin forwards"}
        for p, k, text in zip(prompts_ev, turn_keys, PROMPTS)
    ],
}

sidedoor = side.doc(errata=[], notes=[
    "fill_s is each turn's whole fill, the 20 s base included; output_fill_s is what the claimed words alone "
    "would add without the side-door cap, rounded to the millisecond.",
])

sd.write(fx, tl.out_dir(HERE), expected, anchors, prompts, sidedoor)
