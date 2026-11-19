# SPDX-License-Identifier: AGPL-3.0-or-later
"""WP-FX4 (b): the track loop of (a) on macOS, with key presses in the host
window before each call. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import sidedoor as sd  # noqa: E402

tl = sd.tl
ERRATUM = "2026-11-19-turns-without-a-stop"

fx = tl.Fixture(
    "sidedoor/b-track-keys",
    title="The track loop on macOS, with key presses before each call",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="macos",
    keys=True,
    device=tl.key("d", "sidedoor b macbook"),
    boot_id=tl.key("b", "sidedoor b boot"),
    boot="07:55:12.000",
    seq0=8102,
    last_input="13:57:58.200",
    # The last key press before the fixture.
    last_key="13:41:07.300",
)
at = fx.at

P = tl.key("p", "sidedoor b project")
S = tl.key("s", "sidedoor b goose session")
APP = tl.key("a", "sidedoor b Terminal.app")
TRACK = dict(harness="other", session=S, project=P)

# The same ten lines as (a), each 90 characters.
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
CALLS = [at("14:04:00") + 21_000 * k for k in range(len(LABELS))]

# The jiggler as in (a), and a key press every second for the 8 s before
# each call: 8 to 1 s before it. Key-down idle time is macOS's own and needs
# no typing module (SM 1.2, os.presence).
fx.pointer_every("13:58:00.500", "14:09:00", 12_000)
for t in CALLS:
    for back in range(8, 0, -1):
        fx.inputs.append((t - back * 1000, True))
fx.ticks("13:58:00", "14:09:00", tl.SLOT_MS)

fx.emit(at("13:58:00"), "os.focus",
        {"app_class": "terminal", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
for t, label in zip(CALLS, LABELS):
    fx.emit(t, "cli.track", {"text_len": len(label), "log_match": "no_reader"},
            source="cli", via="track", sample=True, **TRACK)

events = fx.build()
tracks = [fx.event_at("cli.track", t) for t in CALLS]

# Checks of the timing choices (README.md).
tl.check(all(len(x) == 90 for x in LABELS), "every label is 90 characters")
tl.check(sd.input_in_every_slot(fx, at("13:58:00"), at("14:09:00")), "input in every slot")
for t in CALLS:
    sample = next(e for e in events if e["kind"] == "os.presence" and e["t"] == t)
    tl.check(sample["data"]["idle_keys_s"].ms <= sd.KEYS_PRE_S * 1000,
             f"the sample at the {fx.hhmmss(t)} call shows a key press in the 10 s before it")
first_key_slot = CALLS[0] - 8_000 - (CALLS[0] - 8_000) % tl.SLOT_MS
for slot in range(first_key_slot, CALLS[-1], tl.SLOT_MS):
    tl.check(sd.keys_in_slot(fx, slot), f"keys in the slot at {fx.hhmmss(slot)}: 14 s at most between presses")
for a, b in zip(CALLS, CALLS[1:]):
    tl.check(b - a > sd.TRACK_MIN_GAP_S * 1000, "condition 3 never stops a call")

typing = sd.typing_s(90)
lead = sd.THINK_LEAD_S + typing

sweep = sd.Sweep(at("14:00:00"), at("14:09:00"))
for t in CALLS:
    # The typing part is realized only in slots with key input (DESIGN 5.4).
    sweep.anchor(t, lead_s=lead, typing_part_s=typing, reason="P", level="A",
                 typing_ok=lambda slot: sd.keys_in_slot(fx, slot))
    sweep.reset(t, sd.R_TURN_BASE_S)

exp = tl.Expected(fx, sm="docs/PLAN.md WP-FX4 (b); DESIGN 5.2, 5.4, 6.2 D2 and D3, 6.4", evidence="A",
                  witness="witnessed", projects={P: "the project the Goose session runs in"},
                  start="14:00:00", end="14:09:00")
exp.row("14:00", bucket=False, codes=["X.no_anchor"], note="As in (a): before any lead reaches back.")
for m in ("14:01", "14:02", "14:03", "14:04", "14:05", "14:06"):
    exp.row(m, credit={P: {"P": 60}}, uncredited={}, agent=0,
            note="Think parts from 14:01:00, and every typing part from 14:03:45 on, where every slot has "
                 "key presses.")
exp.row("14:07", credit={P: {"P": 9, "READ": 20}}, uncredited={"X.no_anchor": 31}, agent=0, erratum=ERRATUM,
        note="The last lead ends at the 14:07:09 call; its 20 s base drains until 14:07:29.")
exp.row("14:08", bucket=False, codes=["X.no_anchor"], erratum=ERRATUM)
sd.check_rows(exp, sweep, P)

side = sd.SideDoor(fx, exp, case="b", wp="docs/PLAN.md WP-FX4 (b)",
                   rules=["DESIGN 5.2", "DESIGN 5.4", "DESIGN 6.2 D3", "DESIGN 6.4"])
turn_keys = [tl.key("t", f"sidedoor b track turn {k}") for k in range(1, len(CALLS) + 1)]
for e, k in zip(tracks, turn_keys):
    side.record(e, anchor=True, level="A", labels=["via_track"], track="anchor",
                why="DESIGN 5.4's four conditions hold as in (a), and input_keys shows key presses in the host "
                    "window in the 10 s before the call, so the anchor is level A (5.4, 6.4). It keeps its "
                    "via_track label, and the record still never counts toward logged_s (D19).")
    side.turn(e, turn_key=k, backed=False, output_fill_s=0, fill_s=sd.R_TURN_BASE_S,
              why="DESIGN 5.4: a track anchor's reading fill is R_TURN_BASE_S.")

log_ms, ai_ms = side.totals()
lv = sd.levels(sweep)
tl.check(lv == {"A": 389, "B": 0, "C": 0}, f"levels {lv}")
# The closed form: leads from the first call minus 180 s to the last call,
# then the last call's 20 s base.
tl.check((CALLS[-1] - (CALLS[0] - 180_000)) // 1000 + 20 == 389, "closed form")

expected = exp.doc(
    extra_totals={
        "credited_s": 389,
        "level_a_s": lv["A"],
        "level_b_s": 0,
        "level_c_s": 0,
        "via_track_s": 389,
        "logged_s": tl.Seconds(log_ms),
        "ai_logged_s": tl.Seconds(ai_ms),
        "level_c_cap_s": 0,
    },
    stated=["level_a_s", "level_b_s", "level_c_s"],
    approx={},
    errata=[ERRATUM],
    notes=[
        "Every credited second rests on a keyboard-backed track anchor: level A, labeled via_track. A level-A "
        "minute is recommended as credited (SM 5.4), so these are submittable; the keyboard is the evidence.",
        "logged_s and ai_logged_s are still 0: D19 drops cli.track records whatever their level.",
        "The 20 s READ in 14:07 and its X.no_anchor seconds depend on the erratum, as in (a).",
    ],
)

anchors = {
    "fixture": fx.name,
    "anchors": [
        tl.anchor(fx, e, kind="substantive_prompt", prompt_class="substantive", lead_s=180, trail_s=0,
                  lead_from="params", level="A",
                  sm="DESIGN 5.4: classed like a prompt, lead 120 + 90 / 1.5 = 180 s, typing part realized in "
                     "slots with key input")
        for e in tracks
    ],
    "turns": [],
}

prompts = {
    "fixture": fx.name,
    "prompts": [
        {"turn_key": k, "t": e["t"], "local": fx.hhmmss(e["t"]), "text": label, "len_chars": len(label),
         "source": "invented, as in (a); turn_key is this fixture's name for the track turn"}
        for e, k, label in zip(tracks, turn_keys, LABELS)
    ],
}

sidedoor = side.doc(errata=[ERRATUM], notes=[
    "The records are (a)'s; only the keyboard evidence before each call differs, and with it the level.",
])

sd.write(fx, tl.out_dir(HERE), expected, anchors, prompts, sidedoor)
