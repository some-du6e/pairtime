# SPDX-License-Identifier: AGPL-3.0-or-later
"""WP-FX4 (d): a script posting editor heartbeats to the agent's WakaTime
endpoint, with a jiggler on Windows. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import sidedoor as sd  # noqa: E402

tl = sd.tl

fx = tl.Fixture(
    "sidedoor/d-editor-script",
    title="A script posting editor heartbeats, with a jiggler on Windows",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="windows",
    keys=False,
    device=tl.key("d", "sidedoor d laptop"),
    boot_id=tl.key("b", "sidedoor d boot"),
    boot="10:02:44.000",
    seq0=3318,
    last_input="13:57:58.200",
)
at = fx.at

P = tl.key("p", "sidedoor d project")
APP = tl.key("a", "sidedoor d Code.exe")
FILES = [tl.key("f", "sidedoor d src/main.js"), tl.key("f", "sidedoor d src/game.js")]
BEATS = [at("14:03:00") + 100_000 * k for k in range(5)]
LINES = [3, 1, 4, 2, 5]

fx.pointer_every("13:58:00.500", "14:11:00", 12_000)
fx.ticks("13:58:00", "14:11:00", tl.SLOT_MS)

# VS Code in front, on the project.
fx.emit(at("13:58:00"), "os.focus", {"app_class": "editor", "app_hash": APP, "focus_project": P},
        source="os", via="os", sample=True)
for k, (t, n) in enumerate(zip(BEATS, LINES)):
    # A heartbeat in the WakaTime plugin's shape with human_line_changes > 0,
    # which the bridge maps to an edit of unknown source (DESIGN 5.7). The
    # process on the other end of the connection is the script, not
    # wakatime-cli, so the peer-process check fails: editor_unverified.
    fx.emit(t, "editor.activity",
            {"editor": "vscode", "act": "edit", "file_hash": FILES[k % 2], "lines_changed": n,
             "edit_source": "unknown"},
            source="editor", via="editor_unverified", project=P)

events = fx.build()
beats = [fx.event_at("editor.activity", t) for t in BEATS]

# Checks of the timing choices (README.md).
tl.check(sd.input_in_every_slot(fx, at("13:58:00"), at("14:11:00")),
         "input in every slot, the slot of each heartbeat included (SM 2.3's typed-edit test)")

sweep = sd.Sweep(at("14:00:00"), at("14:11:00"))
for t in BEATS:
    sweep.anchor(t, lead_s=sd.EDIT_LEAD_S, typing_part_s=0, reason="E", level="C")

exp = tl.Expected(fx, sm="docs/PLAN.md WP-FX4 (d); DESIGN 5.2, 5.7, 6.2 D2 and D19, 6.4", evidence="C",
                  witness="witnessed", projects={P: "the project VS Code has open"},
                  start="14:00:00", end="14:11:00")
exp.row("14:00", bucket=False, codes=["X.no_anchor"])
for m in ("14:01", "14:02", "14:03", "14:04", "14:05", "14:06", "14:07", "14:08"):
    exp.row(m, credit={P: {"E": 60}}, uncredited={}, agent=0,
            note="Heartbeats 100 s apart, each with a 120 s lead, cover every second from 14:01:00.")
exp.row("14:09", credit={P: {"E": 40}}, uncredited={"X.no_anchor": 20}, agent=0,
        note="The last heartbeat is at 14:09:40, and an edit has no trail.")
exp.row("14:10", bucket=False, codes=["X.no_anchor"])
sd.check_rows(exp, sweep, P)

side = sd.SideDoor(fx, exp, case="d", wp="docs/PLAN.md WP-FX4 (d)",
                   rules=["DESIGN 5.2", "DESIGN 5.7", "DESIGN 6.2 D2", "DESIGN 6.2 D19", "DESIGN 6.4"])
for e in beats:
    side.record(e, anchor=True, level="C", labels=[],
                why="A typed edit by SM 2.3: an edit of unknown source with input in its slot and VS Code focused "
                    "on the project. It failed the peer-process check, so it is editor_unverified and level C "
                    "(DESIGN 5.7, 6.4), with no label (6.4 names none for it), and it never counts toward "
                    "logged_s or ai_logged_s (D19).")

log_ms, ai_ms = side.totals()
lv = sd.levels(sweep)
tl.check(lv == {"A": 0, "B": 0, "C": 520}, f"levels {lv}")
tl.check((BEATS[-1] - (BEATS[0] - 120_000)) // 1000 == 520, "closed form: the last heartbeat minus the first lead")

expected = exp.doc(
    extra_totals={
        "credited_s": 520,
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
        "Every credited second rests on an editor_unverified anchor: level C, and no side-door label, because "
        "DESIGN 6.4's labels are via_track, via_plugin and via_bridge.",
        "logged_s and ai_logged_s are 0: editor_unverified heartbeats never count (D19), and there is nothing "
        "else.",
    ],
)

anchors = {
    "fixture": fx.name,
    "anchors": [
        tl.anchor(fx, e, kind="typed_edit", lead_s=sd.EDIT_LEAD_S, trail_s=0, lead_from="params", level="C",
                  sm="SM 2.3: EDIT_LEAD_S = 120 s; level C by DESIGN 5.7 and 6.4")
        for e in beats
    ],
    "turns": [],
}

prompts = {"fixture": fx.name, "prompts": []}

sidedoor = side.doc(errata=[], notes=["No turn: nothing here fills a reading bucket."])

sd.write(fx, tl.out_dir(HERE), expected, anchors, prompts, sidedoor)
