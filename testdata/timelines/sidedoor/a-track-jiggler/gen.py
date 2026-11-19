# SPDX-License-Identifier: AGPL-3.0-or-later
"""WP-FX4 (a): a pairtime track loop and a mouse jiggler on Windows. See
README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import sidedoor as sd  # noqa: E402

tl = sd.tl
ERRATUM = "2026-11-19-turns-without-a-stop"

fx = tl.Fixture(
    "sidedoor/a-track-jiggler",
    title="A pairtime track loop and a mouse jiggler on Windows",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="windows",
    keys=False,
    device=tl.key("d", "sidedoor a laptop"),
    boot_id=tl.key("b", "sidedoor a boot"),
    boot="08:12:40.000",
    seq0=20411,
    last_input="13:57:58.200",
)
at = fx.at

P = tl.key("p", "sidedoor a project")
S = tl.key("s", "sidedoor a goose session")
APP = tl.key("a", "sidedoor a WindowsTerminal.exe")
TRACK = dict(harness="other", session=S, project=P)

# What the loop's LLM writes, one line per call. Each is 90 characters, so
# every track anchor has the same 60 s typing part.
LABELS = [
    "make the leaderboard command page results ten at a time and add next and previous buttons.",
    "the reminder job fires twice after a restart, store the last run time and skip duplicates.",
    "add a slash command that shows the five most recent reminders for whoever runs it, please.",
    "refactor the database helpers into one module and give every query a short docstring, too.",
    "write tests for the time parser that cover tomorrow, next friday and dates without a year.",
    "the bot crashes when a channel is deleted, so catch that and remove its saved settings too",
    "add a config option for the default time zone and use it when a user has not set one, okay",
    "make the help command list commands by category and hide the admin ones from normal users.",
    "cache the guild settings for a minute so we stop hitting the database on every new message",
    "add logging around the scheduler so we can see when each job starts, ends, and its runtime",
]
# The loop: an LLM call and pairtime track take a second, then sleep 20.
CALLS = [at("14:04:00") + 21_000 * k for k in range(len(LABELS))]

# The USB jiggler moves the pointer every 12 s; nobody touches the keyboard.
fx.pointer_every("13:58:00.500", "14:09:00", 12_000)
fx.ticks("13:58:00", "14:09:00", tl.SLOT_MS)

# Windows Terminal comes to the front hosting the Goose session, which runs
# from before the fixture. v1 has no hooks or log reader for Goose (DESIGN
# 5.1), so nothing else of the session reaches pairtime.
fx.emit(at("13:58:00"), "os.focus",
        {"app_class": "terminal", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
for t, label in zip(CALLS, LABELS):
    # pairtime track takes the hook path, which samples presence at once
    # (DESIGN 3.2, 3.3). The process chain gives the session and project
    # (DESIGN 4.1 E-8). The label is local and stays out of the event.
    fx.emit(t, "cli.track", {"text_len": len(label), "log_match": "no_reader"},
            source="cli", via="track", sample=True, **TRACK)

events = fx.build()
tracks = [fx.event_at("cli.track", t) for t in CALLS]

# Checks of the timing choices (README.md).
tl.check(all(len(x) == 90 for x in LABELS), "every label is 90 characters")
tl.check(sd.input_in_every_slot(fx, at("13:58:00"), at("14:09:00")), "the jiggler puts input in every slot")
tl.check(not any(k for _, k in fx.inputs), "no key press: Windows couldn't tell one anyway")
for t in CALLS:
    tl.check(t - fx.last_input_before(t) <= sd.TRACK_INPUT_PRE_S * 1000,
             f"input in the 30 s before the call at {fx.hhmmss(t)} (DESIGN 5.4 condition 2)")
for a, b in zip(CALLS, CALLS[1:]):
    tl.check(b - a > sd.TRACK_MIN_GAP_S * 1000, "every call is more than TRACK_MIN_GAP_S after the last (condition 3)")

typing = sd.typing_s(90)
lead = sd.THINK_LEAD_S + typing
tl.check(typing == 60 and lead == 180, "lead 120 s think + 60 s typing")

sweep = sd.Sweep(at("14:00:00"), at("14:09:00"))
for t in CALLS:
    # DESIGN 5.4: the typing part is realized only in slots with input_keys,
    # which this machine never has; the think part needs presence only.
    sweep.anchor(t, lead_s=lead, typing_part_s=typing, reason="P", level="C", typing_ok=lambda slot: False)
    # Each anchor starts a human-owned turn, whose fill is the base alone.
    sweep.reset(t, sd.R_TURN_BASE_S)
# The track turns have no agent records, so none runs (the erratum).

exp = tl.Expected(fx, sm="docs/PLAN.md WP-FX4 (a); DESIGN 5.2, 5.4, 6.2 D2, D3 and D19, 6.4", evidence="C",
                  witness="witnessed", projects={P: "the project the Goose session runs in"},
                  start="14:00:00", end="14:09:00")
exp.row("14:00", bucket=False, codes=["X.no_anchor"],
        note="The jiggler on a focused terminal, before any lead reaches back this far.")
for m in ("14:01", "14:02", "14:03", "14:04", "14:05"):
    exp.row(m, credit={P: {"P": 60}}, uncredited={}, agent=0,
            note="Inside the think parts of the track anchors, which need presence only.")
exp.row("14:06", credit={P: {"P": 9, "READ": 50}}, uncredited={"X.no_anchor": 1}, agent=0, erratum=ERRATUM,
        note="The last think part ends at 14:06:09, 60 s before the last call. Then each call's 20 s base "
             "drains: 18 s left of the 14:06:06 call's, 20 s of the 14:06:27 call's, and 12 s of the 14:06:48 "
             "call's; 14:06:47 has nothing left to drain.")
exp.row("14:07", credit={P: {"READ": 28}}, uncredited={"X.no_anchor": 32}, agent=0, erratum=ERRATUM,
        note="The rest of the 14:06:48 base until 14:07:08, then the 14:07:09 call's 20 s until 14:07:29.")
exp.row("14:08", bucket=False, codes=["X.no_anchor"], erratum=ERRATUM,
        note="Nothing credits the jiggler; no bucket, since no agent turn runs.")
sd.check_rows(exp, sweep, P)

side = sd.SideDoor(fx, exp, case="a", wp="docs/PLAN.md WP-FX4 (a)",
                   rules=["DESIGN 5.2", "DESIGN 5.4", "DESIGN 6.2 D3", "DESIGN 6.2 D19", "DESIGN 6.4"])
turn_keys = [tl.key("t", f"sidedoor a track turn {k}") for k in range(1, len(CALLS) + 1)]
for e, k in zip(tracks, turn_keys):
    side.record(e, anchor=True, level="C", labels=["via_track"], track="anchor",
                why="DESIGN 5.4's four conditions hold: the chain reaches the session the focused terminal hosts, "
                    "the jiggler gave input in the 30 s before, the last anchor is more than 20 s back, and no hook "
                    "or log reader sees the session. Level C: no keyboard input in the 10 s before (6.4). A track "
                    "record never counts toward logged_s or ai_logged_s (D19).")
    side.turn(e, turn_key=k, backed=False, output_fill_s=0, fill_s=sd.R_TURN_BASE_S,
              why="DESIGN 5.4: a track anchor's reading fill is R_TURN_BASE_S, because no record shows the "
                  "agent's output.")

log_ms, ai_ms = side.totals()
lv = sd.levels(sweep)
tl.check(lv == {"A": 0, "B": 0, "C": 387}, f"levels {lv}")
# The closed form in README.md: think parts from the first call minus 180 s
# to the last call minus 60 s, then 18 + 20 + 20 + 20 s of bases.
tl.check((CALLS[-1] - 60_000 - (CALLS[0] - 180_000)) // 1000 + 18 + 20 + 20 + 20 == 387, "closed form")
cap = min(lv["C"] * 1000, ai_ms // 3)

expected = exp.doc(
    extra_totals={
        "credited_s": 387,
        "level_a_s": 0,
        "level_b_s": 0,
        "level_c_s": lv["C"],
        "via_track_s": 387,
        "logged_s": tl.Seconds(log_ms),
        "ai_logged_s": tl.Seconds(ai_ms),
        "level_c_cap_s": tl.Seconds(cap),
    },
    stated=["level_a_s", "level_b_s", "logged_s", "ai_logged_s", "level_c_cap_s"],
    approx={},
    errata=[ERRATUM],
    notes=[
        "Every credited second rests on a track anchor, so all 387 s are level C and labeled via_track.",
        "logged_s and ai_logged_s are 0: cli.track records never count (DESIGN 6.2 D19), and nothing else of "
        "the session reaches pairtime.",
        "level_c_cap_s is min(level_c_s, ai_logged_s / 3), the most a program that opts in to level C could "
        "take inside forge-verified commit intervals (DESIGN 5.2, 6.2 D5; SM 5.4). It is 0, so the loop earns "
        "0 submittable seconds whatever the program's policy, as DESIGN 5.2 requires.",
        "The READ seconds and the X.no_anchor seconds after 14:06:09 depend on the erratum: a track turn has no "
        "agent record, so the rules don't say whether it is running, which would make them SUP and X.agent_only.",
    ],
)

anchors = {
    "fixture": fx.name,
    "anchors": [
        tl.anchor(fx, e, kind="substantive_prompt", prompt_class="substantive", lead_s=180, trail_s=0,
                  lead_from="params", level="C",
                  sm="DESIGN 5.4: classed like a prompt (SM 2.2): 90 characters, none of them a go-ahead, so "
                     "substantive, with a lead of 120 + 90 / 1.5 = 180 s whose 60 s typing part is never realized "
                     "here")
        for e in tracks
    ],
    "turns": [],
}

prompts = {
    "fixture": fx.name,
    "prompts": [
        {"turn_key": k, "t": e["t"], "local": fx.hhmmss(e["t"]), "text": label, "len_chars": len(label),
         "source": "invented: the loop's LLM line, which the agent keeps as the cli.track label; turn_key is "
                   "this fixture's name for the track turn, as in sidedoor.json"}
        for e, k, label in zip(tracks, turn_keys, LABELS)
    ],
}

sidedoor = side.doc(
    errata=[ERRATUM],
    notes=[
        "Each call opens an anchor and starts a turn whose fill is the 20 s base and nothing more.",
    ],
)

sd.write(fx, tl.out_dir(HERE), expected, anchors, prompts, sidedoor)
