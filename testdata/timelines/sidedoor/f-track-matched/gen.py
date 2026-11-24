# SPDX-License-Identifier: AGPL-3.0-or-later
"""WP-FX4 (f): Claude Code on Windows calls pairtime track 4 s after the
hook prompt it copies. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import sidedoor as sd  # noqa: E402

tl = sd.tl

fx = tl.Fixture(
    "sidedoor/f-track-matched",
    title="A track call 4 s after the hook prompt it copies",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="windows",
    keys=False,
    device=tl.key("d", "sidedoor f laptop"),
    boot_id=tl.key("b", "sidedoor f boot"),
    boot="12:47:19.000",
    seq0=51630,
    last_input="13:57:58.000",
)
at = fx.at

P = tl.key("p", "sidedoor f project")
S = tl.key("s", "sidedoor f claude session")
T1 = tl.key("t", "sidedoor f turn 1")
APP = tl.key("a", "sidedoor f WindowsTerminal.exe")
CMD = tl.key("c", "sidedoor f pairtime track command")
CC = dict(harness="claude_code", version="2.1.334", session=S, project=P)
PROMPT = ("the pause menu ignores the escape key while a dialog is open, find out why and fix it so escape "
          "closes the dialog first and then opens the pause menu.")
# What the model passes to pairtime track: the message, with nothing to
# remove, cut at a clause. text_len is what the agent records.
TRACK_LEN = 141
SUBMIT = at("14:04:40")
CALL = SUBMIT + 4_000
STOP = at("14:05:30")
WORDS = 120

# The person's own input: the touchpad every 10 s, and the prompt typed
# from 14:03:30.7 to the Enter at 14:04:39.2. Windows can't tell the two
# apart. The key presses land on .2 and .7, so none falls on a slot
# boundary (sidedoor.py checks).
fx.pointer_every("13:58:04", "14:08:00", 10_000)
fx.typing("14:03:30.700", "14:04:39.200", 500)
fx.ticks("13:58:00", "14:08:00", tl.SLOT_MS)

fx.emit(at("13:58:00"), "os.focus", {"app_class": "terminal", "app_hash": APP},
        source="os", via="os", sample=True)
# claude starts in the terminal: SessionStart, then the agent walks the
# window again, which now hosts the session (testdata/timelines/README.md).
fx.emit(at("13:58:30"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "default"},
        source="hook", via="hook", sample=True, **CC)
fx.emit(at("13:58:30"), "os.focus",
        {"app_class": "terminal", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
fx.emit(SUBMIT, "prompt.submit",
        {"turn_key": T1, "len_chars": len(PROMPT), "pasted_chars": 0, "typed_len_est": len(PROMPT),
         "origin_declared": "none", "matches_pending_cron": False, "answers_question": False, "low_effort": False},
        source="hook", via="hook", sample=True, **CC)
# Claude follows the AGENTS.md entry with a Bash call: pairtime track runs
# inside it, so its environment has Claude Code's tool variables (HS 11.3),
# and the transcript tailer has already read the user entry.
fx.emit(CALL, "cli.track",
        {"text_len": TRACK_LEN, "env_hints": ["CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID"],
         "log_match": "matched"},
        source="cli", via="track", sample=True, **CC)
# The Bash call's PostToolUse hook (DESIGN 5.1 matches Edit, Write and Bash).
# pairtime track prints nothing.
fx.emit(CALL + 300, "agent.tool",
        {"tool_class": "bash", "lines_added": 0, "lines_removed": 0, "cmd_hash": CMD, "exit_code": 0,
         "visible_output_lines": 0, "t_start": CALL - 100, "t_end": CALL + 300},
        source="hook", via="hook", sample=True, **CC)
# The answer, from the transcript, and the Stop hook.
fx.emit(STOP, "agent.output",
        {"turn_key": T1, "words": WORDS, "code_lines": 0, "is_final": True, "asks_question": False,
         "offers_options": False},
        source="log", via="log", **CC)
fx.emit(STOP, "agent.stop", {"turn_key": T1, "stop_reason": "end_turn", "stop_hook_active": False},
        source="hook", via="hook", sample=True, **CC)

events = fx.build()
prompt = fx.event_at("prompt.submit", SUBMIT)
track = fx.event_at("cli.track", CALL)
tool = fx.event_at("agent.tool", CALL + 300)
output = fx.event_at("agent.output", STOP)
stop = fx.event_at("agent.stop", STOP)

# Checks of the timing choices (README.md).
tl.check(len(PROMPT) == 150, "the prompt is 150 characters")
tl.check(sd.input_in_every_slot(fx, at("13:58:00"), at("14:08:00")), "input in every slot")
tl.check(SUBMIT - fx.last_input_before(SUBMIT) <= sd.KEYS_PRE_S * 1000, "input in the 10 s before submit")
tl.check(CALL - SUBMIT <= sd.TRACK_DEDUPE_S * 1000,
         "the call is within TRACK_DEDUPE_S of the prompt, so it is track_matched and raises no "
         "track_without_prompt")
tl.check(CALL - SUBMIT <= sd.TRACK_LOG_MATCH_S * 1000,
         "and within TRACK_LOG_MATCH_S of it, as its log_match matched says (DESIGN 5.2)")

typing = sd.typing_s(150)
lead = sd.THINK_LEAD_S + typing
tl.check(lead == 220, "lead 120 + 100 s")
fill = sd.output_fill(words=WORDS)
tl.check(fill == 40, "120 words fill 40 s")

sweep = sd.Sweep(at("14:00:00"), at("14:08:00"))
# The hook prompt is the anchor (DESIGN 5.4). The track call opens nothing,
# resets nothing and fills nothing, so the sweep doesn't see it.
sweep.anchor(SUBMIT, lead_s=lead, typing_part_s=typing, reason="P", level="B")
sweep.reset(SUBMIT, sd.R_TURN_BASE_S)
sweep.fill(STOP, fill)
sweep.run(SUBMIT, STOP)

exp = tl.Expected(fx, sm="docs/PLAN.md WP-FX4 (f); DESIGN 5.2, 5.4, 6.2 D3 and D19, 6.4", evidence="B",
                  witness="witnessed", projects={P: "the project Claude Code runs in"},
                  start="14:00:00", end="14:08:00")
exp.row("14:00", bucket=False, codes=["X.no_anchor"])
for m in ("14:01", "14:02", "14:03"):
    exp.row(m, credit={P: {"P": 60}}, uncredited={}, agent=0, note="The prompt's 220 s lead, from 14:01:00.")
exp.row("14:04", credit={P: {"P": 40, "SUP": 20}}, uncredited={}, agent=20,
        note="The prompt at 14:04:40, then its turn's 20 s base. The track call at 14:04:44 changes nothing.")
exp.row("14:05", credit={P: {"READ": 30}}, uncredited={"X.agent_only": 30}, agent=30,
        note="Nothing to read until the 120-word answer at 14:05:30 adds 40 s.")
exp.row("14:06", credit={P: {"READ": 10}}, uncredited={"X.no_anchor": 50}, agent=0)
exp.row("14:07", bucket=False, codes=["X.no_anchor"])
sd.check_rows(exp, sweep, P)

side = sd.SideDoor(fx, exp, case="f", wp="docs/PLAN.md WP-FX4 (f)",
                   rules=["DESIGN 5.2", "DESIGN 5.4", "DESIGN 6.2 D19", "DESIGN 6.4"])
side.record(prompt, anchor=True, level="B", labels=[],
            why="A hook prompt that passes SM 2.2 rule 6, substantive, lead 120 + 150 / 1.5 = 220 s. Level B: "
                "presence from input_any only (6.4).")
side.record(track, track="matched",
            why="DESIGN 5.4: pairtime sees the session through hooks and a log reader, and the call comes 4 s "
                "after the session's hook prompt, within TRACK_DEDUPE_S = 30 s: track_matched. It opens nothing, "
                "resets nothing and fills nothing; it feeds the compliance counter. Never counts toward logged_s "
                "(D19).")
side.record(tool, why="The Bash call that ran pairtime track, from the PostToolUse hook: an agent event, so it "
                      "counts toward logged_s and ai_logged_s. No output lines, so it fills nothing.")
side.record(output, why="The answer from the transcript: 120 words fill 40 s. Not one of SM 5.4's agent events "
                        "(prompt submits, tool calls, agent edits, stops), and it shares the stop's millisecond.")
side.record(stop, why="The Stop hook: an agent event, counted in both series.")
side.turn(prompt, turn_key=T1, backed=True, output_fill_s=fill, fill_s=sd.R_TURN_BASE_S + fill,
          why="SM 2.4: the 20 s base, then 60 * 120 / 180 = 40 s for the answer. Hooks and the transcript back "
              "the turn, so no side-door cap applies, and the track call starts no turn.")

log_ms, ai_ms = side.totals()
lv = sd.levels(sweep)
tl.check(lv == {"A": 0, "B": 280, "C": 0}, f"levels {lv}")
tl.check(220 + 20 + 40 == 280, "closed form: the lead, the base and the answer")
tl.check(log_ms == 50_000 and ai_ms == 50_000, f"logged {log_ms}, ai_logged {ai_ms}")

expected = exp.doc(
    extra_totals={
        "credited_s": 280,
        "level_a_s": 0,
        "level_b_s": lv["B"],
        "level_c_s": 0,
        "via_track_s": 0,
        "logged_s": tl.Seconds(log_ms),
        "ai_logged_s": tl.Seconds(ai_ms),
        "level_c_cap_s": 0,
    },
    stated=["level_c_s", "via_track_s"],
    approx={},
    errata=[],
    notes=[
        "Everything is what the same session would earn with no track call: 280 s at level B from the hook "
        "prompt, nothing at level C and nothing labeled via_track.",
        "logged_s = ai_logged_s = 4.3 + 45.7 = 50 s: the prompt, the Bash call's PostToolUse and the stop. The "
        "cli.track record isn't in either series (D19).",
    ],
)

anchors = {
    "fixture": fx.name,
    "anchors": [
        tl.anchor(fx, prompt, kind="substantive_prompt", origin="human_typed", prompt_class="substantive",
                  lead_s=220, trail_s=0, lead_from="params", level="B",
                  sm="SM 2.2 rule 6 and SM 2.3; level B by DESIGN 6.4"),
    ],
    "turns": [
        tl.turn(fx, SUBMIT, STOP, session=S, turn_key=T1, origin="human_typed", prompt_class="substantive",
                sm="UserPromptSubmit and Stop"),
    ],
}

prompts = {
    "fixture": fx.name,
    "prompts": [
        {"turn_key": T1, "t": SUBMIT, "local": fx.hhmmss(SUBMIT), "text": PROMPT, "len_chars": len(PROMPT),
         "source": "invented"},
    ],
}

sidedoor = side.doc(errata=[], notes=[
    "A wrong scorer that opened an anchor for the call would reset the bucket at 14:04:44 and credit the "
    "drain after it at level C, the level of the nearest anchor (DESIGN 6.4).",
])

sd.write(fx, tl.out_dir(HERE), expected, anchors, prompts, sidedoor)
