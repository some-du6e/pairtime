# SPDX-License-Identifier: AGPL-3.0-or-later
"""WP-FX4 (e): WakaTime bridge records of an agent edit with 5,000 changed
lines, with a jiggler on Windows. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import sidedoor as sd  # noqa: E402

tl = sd.tl
ERRATUM = "2026-11-19-turns-without-a-stop"

fx = tl.Fixture(
    "sidedoor/e-bridge-lines",
    title="Bridge records of 5,000-line agent edits, with a jiggler on Windows",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="windows",
    keys=False,
    device=tl.key("d", "sidedoor e laptop"),
    boot_id=tl.key("b", "sidedoor e boot"),
    boot="11:20:31.000",
    seq0=91207,
    last_input="13:57:59.000",
)
at = fx.at

P = tl.key("p", "sidedoor e project")
# The agent extension's session, keyed from the heartbeat's ai_session.
S = tl.key("s", "sidedoor e extension session")
APP = tl.key("a", "sidedoor e Code.exe")
# Each turn rewrites a different file, so SM 4.2's rewrite-loop rule has no
# shared lines to count, whatever it makes of edits with no hunk ranges.
FILES = [tl.key("f", f"sidedoor e src/{name}") for name in ("app.tsx", "store.ts", "api.ts")]
BRIDGE = dict(source="editor", via="bridge", harness="other", session=S, project=P)
TURNS = [at("14:03:03"), at("14:05:03"), at("14:07:03")]
EDIT_AFTER = 3_000
PROMPT_LEN = 90
ADDED, REMOVED = 3_800, 1_200

# The jiggler moves every 12 s, 4 s before each prompt among other times.
fx.pointer_every("13:58:11", "14:09:00", 12_000)
fx.ticks("13:58:00", "14:09:00", tl.SLOT_MS)

# VS Code in front, on the project. The agent runs as a VS Code extension,
# so SM 2.2 rule 6's focus test is the editor window's.
fx.emit(at("13:58:00"), "os.focus", {"app_class": "editor", "app_hash": APP, "focus_project": P},
        source="os", via="os", sample=True)
turn_keys = [tl.key("t", f"sidedoor e turn {k}") for k in range(1, len(TURNS) + 1)]
for t, k, f in zip(TURNS, turn_keys, FILES):
    # The agent's write of 5,000 changed lines changes the open file, so the
    # WakaTime plugin sends a heartbeat and wakatime-cli runs. Its AI sync
    # goes first and sends the prompt and the edit from the transcript; the
    # plugin's own heartbeat follows. wakatime-cli is the editor's child, so
    # the peer-process check passes: via bridge for the transcript-derived
    # heartbeats, via editor for the plugin's (DESIGN 5.7). The bridge keeps
    # the transcript's timestamps; nothing else is recorded between the
    # prompt and the sync.
    fx.emit(t, "prompt.submit",
            {"turn_key": k, "len_chars": PROMPT_LEN, "pasted_chars": 0, "typed_len_est": PROMPT_LEN,
             "origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
             "low_effort": False},
            **BRIDGE)
    e = t + EDIT_AFTER
    fx.emit(e, "agent.tool",
            {"tool_class": "edit", "files": [f], "lines_added": ADDED, "lines_removed": REMOVED,
             "visible_output_lines": 0, "t_start": e, "t_end": e},
            **BRIDGE)
    # The plugin's heartbeat for the same file, within 120 s of an agent edit
    # of it, so the bridge marks it the agent's (DESIGN 5.7).
    fx.emit(e, "editor.activity",
            {"editor": "vscode", "act": "edit", "file_hash": f, "lines_changed": ADDED + REMOVED,
             "edit_source": "agent"},
            source="editor", via="editor", project=P)

events = fx.build()
prompts_ev = [fx.event_at("prompt.submit", t) for t in TURNS]
tools = [fx.event_at("agent.tool", t + EDIT_AFTER) for t in TURNS]
beats = [fx.event_at("editor.activity", t + EDIT_AFTER) for t in TURNS]

# Checks of the timing choices (README.md).
tl.check(sd.input_in_every_slot(fx, at("13:58:00"), at("14:09:00")), "the jiggler puts input in every slot")
for t in TURNS:
    tl.check(sd.samples_show_input_before(fx, t, sd.KEYS_PRE_S * 1000),
             f"the samples show input in the 10 s before the {fx.hhmmss(t)} prompt (SM 2.2 rule 6)")
    nxt = t - t % tl.SLOT_MS + tl.SLOT_MS
    tl.check(t + EDIT_AFTER < nxt, "the sync lands before the next presence sample, so arrival order is time order")
for i, e in enumerate(events[1:], 1):
    tl.check(e["t"] >= events[i - 1]["t"], "t never goes back")

lead = sd.typing_s(PROMPT_LEN)
tl.check(lead == 60, "the typing lead of 90 characters")
claimed = sd.output_fill(diff_lines=ADDED + REMOVED)
tl.check(claimed == 37_500, "5,000 diff lines would fill 37,500 s")

sweep = sd.Sweep(at("14:01:00"), at("14:09:00"))
for t in TURNS:
    # DESIGN 6.2 D4: the typing lead only, never the think lead. On this
    # machine the typing part needs presence only (SM 2.3).
    sweep.anchor(t, lead_s=lead, typing_part_s=lead, reason="P", level="C")
    sweep.reset(t, sd.R_TURN_BASE_S)
    sweep.fill(t + EDIT_AFTER, 0)
    # The erratum: a turn with no agent.stop runs until the latest agent
    # record received for it, here the edit, 3 s after the prompt.
    sweep.run(t, t + EDIT_AFTER)

exp = tl.Expected(fx, sm="docs/PLAN.md WP-FX4 (e); DESIGN 5.2, 5.7, 6.2 D2, D4 and D19, 6.4", evidence="C",
                  witness="witnessed", projects={P: "the project VS Code has open"},
                  start="14:01:00", end="14:09:00")
exp.row("14:01", bucket=False, codes=["X.no_anchor"])
exp.row("14:02", credit={P: {"P": 57}}, uncredited={"X.no_anchor": 3}, agent=0,
        note="The first prompt's 60 s typing lead starts at 14:02:03.")
for m in ("14:03", "14:05", "14:07"):
    exp.row(m, credit={P: {"P": 3, "SUP": 3, "READ": 17}}, uncredited={"X.no_anchor": 37}, agent=3,
            erratum=ERRATUM,
            note="The prompt at :03, then the turn's 20 s base until :23: SUP until the edit at :06, READ after. "
                 "The 5,000 lines add nothing to the bucket.")
    if m != "14:07":
        nxt = {"14:03": "14:04", "14:05": "14:06"}[m]
        exp.row(nxt, credit={P: {"P": 57}}, uncredited={"X.no_anchor": 3}, agent=0, erratum=ERRATUM,
                note="The next prompt's lead starts at :03.")
exp.row("14:08", bucket=False, codes=["X.no_anchor"], erratum=ERRATUM)
sd.check_rows(exp, sweep, P)

side = sd.SideDoor(fx, exp, case="e", wp="docs/PLAN.md WP-FX4 (e)",
                   rules=["DESIGN 5.2", "DESIGN 5.7", "DESIGN 6.2 D2", "DESIGN 6.2 D4", "DESIGN 6.2 D19",
                          "DESIGN 6.4"])
for p, tool, beat in zip(prompts_ev, tools, beats):
    side.record(p, anchor=True, level="C", labels=["via_bridge"],
                why="A transcript-derived heartbeat with ai_prompt_length 90 becomes a prompt with no text (DESIGN "
                    "5.7). SM 2.2 rule 6 holds: the jiggler gave input in the 10 s before it and VS Code, which "
                    "hosts the extension, has focus. 90 characters: substantive, with the typing lead only (D4). "
                    "Level C: a bridge anchor with no keyboard input and no log reader for the harness (5.2, 6.4). "
                    "It passed the peer-process check, so it counts toward logged_s and ai_logged_s (D19).")
    side.record(tool, why="5,000 changed lines would fill 37,500 s. The bridge's lines fill a turn only up to "
                          "R_TURN_BASE_S, which the base already reaches (DESIGN 5.2, 5.7). An agent edit, so it "
                          "counts toward both logged_s and ai_logged_s (D19).")
    side.record(beat, why="The editor plugin's heartbeat for the agent's change: edit_source agent, so it opens "
                          "no anchor (DESIGN 5.7). An editor heartbeat that passed the check: it counts toward "
                          "logged_s, and not toward ai_logged_s, which counts agent events only (SM 5.4).")
for p, k in zip(prompts_ev, turn_keys):
    side.turn(p, turn_key=k, backed=False, output_fill_s=claimed, fill_s=sd.R_TURN_BASE_S,
              why="DESIGN 5.2 and 5.7: bridge output fills the bucket only up to R_TURN_BASE_S = 20 s per turn "
                  "when no hook or transcript record backs the turn, and no pairtime adapter reads this one.")

log_ms, ai_ms = side.totals()
lv = sd.levels(sweep)
tl.check(lv == {"A": 0, "B": 0, "C": 240}, f"levels {lv}")
tl.check(3 * (60 + 20) == 240, "closed form: three typing leads and three bases")
tl.check(log_ms == 243_000 and ai_ms == 243_000, f"logged {log_ms}, ai_logged {ai_ms}")
cap = min(lv["C"] * 1000, ai_ms // 3)
tl.check(cap == 81_000, "the handbook third of ai_logged_s")

expected = exp.doc(
    extra_totals={
        "credited_s": 240,
        "level_a_s": 0,
        "level_b_s": 0,
        "level_c_s": lv["C"],
        "via_track_s": 0,
        "logged_s": tl.Seconds(log_ms),
        "ai_logged_s": tl.Seconds(ai_ms),
        "level_c_cap_s": tl.Seconds(cap),
    },
    stated=["level_a_s", "level_b_s"],
    approx={},
    errata=[ERRATUM],
    notes=[
        "Every credited second rests on a bridge anchor: level C, labeled via_bridge.",
        "Unlike (a), (c) and (d), these records count toward logged_s and ai_logged_s: they come from "
        "heartbeats that passed the peer-process check (D19). logged_s = ai_logged_s = 3 + 117 + 3 + 117 + 3 = "
        "243 s, because the editor heartbeats share their edits' millisecond.",
        "level_c_cap_s = min(240, 243 / 3) = 81 s: the most a program that opts in to level C could take, and "
        "only inside a commit interval whose closing commit is forge-verified (DESIGN 5.2, D5). This case has no "
        "commit, so it is 0 submittable as recorded.",
        "Uncapped, each 5,000-line edit would refill the bucket to its 600 s cap and turn the 40 s from :23 to "
        "the next lead, and everything after 14:07:23, into READ.",
        "The SUP and READ split, agent_running_s and the X.no_anchor seconds after each edit depend on the "
        "erratum: no agent.stop says when a bridge turn ends.",
    ],
)

anchors = {
    "fixture": fx.name,
    "anchors": [
        tl.anchor(fx, p, kind="substantive_prompt", origin="human_typed", prompt_class="substantive", lead_s=60,
                  trail_s=0, lead_from="params", level="C",
                  sm="DESIGN 6.2 D4: a bridge prompt gets the typing lead only, 90 / 1.5 = 60 s; level C by 6.4")
        for p in prompts_ev
    ],
    "turns": [
        tl.turn(fx, p["t"], None, session=S, turn_key=k, origin="human_typed", prompt_class="substantive",
                sm="no agent.stop: the bridge maps no heartbeat to one")
        for p, k in zip(prompts_ev, turn_keys)
    ],
}

prompts = {
    "fixture": fx.name,
    "prompts": [
        {"turn_key": k, "t": p["t"], "local": fx.hhmmss(p["t"]), "text": None, "len_chars": PROMPT_LEN,
         "source": "a bridge prompt carries no text, only ai_prompt_length (DESIGN 5.7); with 20 or more "
                   "characters it is substantive (AD D2), since every go-ahead on SM 2.2's list is shorter"}
        for p, k in zip(prompts_ev, turn_keys)
    ],
}

sidedoor = side.doc(errata=[ERRATUM], notes=[
    "fill_s is each turn's whole fill, the 20 s base included; output_fill_s is what the 5,000 diff lines "
    "alone would add without the side-door cap.",
])

sd.write(fx, tl.out_dir(HERE), expected, anchors, prompts, sidedoor)
