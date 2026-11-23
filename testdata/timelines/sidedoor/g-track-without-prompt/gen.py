# SPDX-License-Identifier: AGPL-3.0-or-later
"""WP-FX4 (g): a pairtime track call on a Claude Code session the hooks
see, with no prompt anywhere near it. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import sidedoor as sd  # noqa: E402

tl = sd.tl

fx = tl.Fixture(
    "sidedoor/g-track-without-prompt",
    title="A track call on a hooked session with no prompt near it",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="windows",
    keys=False,
    device=tl.key("d", "sidedoor g laptop"),
    boot_id=tl.key("b", "sidedoor g boot"),
    boot="13:05:52.000",
    seq0=7744,
    last_input="13:57:58.000",
)
at = fx.at

P = tl.key("p", "sidedoor g project")
S = tl.key("s", "sidedoor g claude session")
T1 = tl.key("t", "sidedoor g turn 1")
APP = tl.key("a", "sidedoor g WindowsTerminal.exe")
CC = dict(harness="claude_code", version="2.1.334", session=S, project=P)
PROMPT = ("explain why the save file gets bigger every time i load it and save again without changing anything, "
          "and tell me which function writes the extra data.")
SUBMIT = at("14:04:40")
STOP = at("14:05:30")
CALL = at("14:07:30")
WORDS = 120

# Input as in (f): the touchpad every 10 s, and the prompt typed from
# 14:03:30.7 to the Enter at 14:04:39.2, off every slot boundary.
fx.pointer_every("13:58:04", "14:09:00", 10_000)
fx.typing("14:03:30.700", "14:04:39.200", 500)
fx.ticks("13:58:00", "14:09:00", tl.SLOT_MS)

fx.emit(at("13:58:00"), "os.focus", {"app_class": "terminal", "app_hash": APP},
        source="os", via="os", sample=True)
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
# Claude answers in text, with no tool call, so it doesn't call track.
fx.emit(STOP, "agent.output",
        {"turn_key": T1, "words": WORDS, "code_lines": 0, "is_final": True, "asks_question": False,
         "offers_options": False},
        source="log", via="log", **CC)
fx.emit(STOP, "agent.stop", {"turn_key": T1, "stop_reason": "end_turn", "stop_hook_active": False},
        source="hook", via="hook", sample=True, **CC)
# Two minutes after the stop, a process started inside the session runs
# pairtime track with no turn behind it. Its chain reaches the session's
# claude process and it inherited CLAUDE_CODE_SESSION_ID; the transcript
# tailer covers the session and holds no user entry for it.
fx.emit(CALL, "cli.track",
        {"text_len": 64, "env_hints": ["CLAUDE_CODE_SESSION_ID"], "log_match": "unmatched"},
        source="cli", via="track", sample=True, **CC)

events = fx.build()
prompt = fx.event_at("prompt.submit", SUBMIT)
output = fx.event_at("agent.output", STOP)
stop = fx.event_at("agent.stop", STOP)
track = fx.event_at("cli.track", CALL)

# Checks of the timing choices (README.md).
tl.check(len(PROMPT) == 150, "the prompt is 150 characters")
tl.check(sd.input_in_every_slot(fx, at("13:58:00"), at("14:09:00")), "input in every slot")
tl.check(SUBMIT - fx.last_input_before(SUBMIT) <= sd.KEYS_PRE_S * 1000, "input in the 10 s before submit")
prompt_times = [e["t"] for e in events if e["kind"] == "prompt.submit" and e["session_key"] == S]
tl.check(all(abs(CALL - t) > sd.TRACK_DEDUPE_S * 1000 for t in prompt_times),
         "no prompt of the session within TRACK_DEDUPE_S of the call, so it isn't track_matched and raises "
         "track_without_prompt")

typing = sd.typing_s(150)
lead = sd.THINK_LEAD_S + typing
fill = sd.output_fill(words=WORDS)

sweep = sd.Sweep(at("14:00:00"), at("14:09:00"))
sweep.anchor(SUBMIT, lead_s=lead, typing_part_s=typing, reason="P", level="B")
sweep.reset(SUBMIT, sd.R_TURN_BASE_S)
sweep.fill(STOP, fill)
sweep.run(SUBMIT, STOP)

exp = tl.Expected(fx, sm="docs/PLAN.md WP-FX4 (g); DESIGN 5.2, 5.4, 6.2 D3 and D19, 6.4", evidence="B",
                  witness="witnessed", projects={P: "the project Claude Code runs in"},
                  start="14:00:00", end="14:09:00")
exp.row("14:00", bucket=False, codes=["X.no_anchor"])
for m in ("14:01", "14:02", "14:03"):
    exp.row(m, credit={P: {"P": 60}}, uncredited={}, agent=0, note="The prompt's 220 s lead, from 14:01:00.")
exp.row("14:04", credit={P: {"P": 40, "SUP": 20}}, uncredited={}, agent=20)
exp.row("14:05", credit={P: {"READ": 30}}, uncredited={"X.agent_only": 30}, agent=30)
exp.row("14:06", credit={P: {"READ": 10}}, uncredited={"X.no_anchor": 50}, agent=0)
exp.row("14:07", bucket=False, codes=["X.no_anchor"],
        note="The track call at 14:07:30 opens nothing and starts no turn, so nothing is credited around it.")
exp.row("14:08", bucket=False, codes=["X.no_anchor"])
sd.check_rows(exp, sweep, P)

side = sd.SideDoor(fx, exp, case="g", wp="docs/PLAN.md WP-FX4 (g)",
                   rules=["DESIGN 5.2", "DESIGN 5.4", "DESIGN 6.2 D19", "DESIGN 6.4"])
side.record(prompt, anchor=True, level="B", labels=[],
            why="A hook prompt that passes SM 2.2 rule 6, substantive, lead 220 s. Level B: input_any only (6.4).")
side.record(output, why="The answer from the transcript: 120 words fill 40 s. Not one of SM 5.4's agent events.")
side.record(stop, why="The Stop hook: an agent event, counted in both series.")
side.record(track, track="without_prompt", flags=["track_without_prompt"],
            why="DESIGN 5.4: pairtime sees the session through hooks and a log reader, and no hook or log prompt "
                "of the session is within TRACK_DEDUPE_S = 30 s of the call; the nearest is 170 s back. It "
                "opens nothing and raises the tier-2 flag track_without_prompt. Never counts toward logged_s "
                "(D19).")
side.turn(prompt, turn_key=T1, backed=True, output_fill_s=fill, fill_s=sd.R_TURN_BASE_S + fill,
          why="SM 2.4: the 20 s base and 40 s for the 120-word answer. The track call starts no turn.")

log_ms, ai_ms = side.totals()
lv = sd.levels(sweep)
tl.check(lv == {"A": 0, "B": 280, "C": 0}, f"levels {lv}")
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
        "The same 280 s at level B as the session would earn with no track call; the flag changes review "
        "priority, never credit (SM 4.5).",
        "logged_s = ai_logged_s = 50 s, from the prompt to the stop.",
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
    "A wrong scorer that opened an anchor for the call would credit its think part, from about 14:04:47 to "
    "14:06:47 since its 42.7 s typing part is never realized here, ahead of the bucket, and a fresh 20 s base "
    "from 14:07:30, all at level C.",
])

sd.write(fx, tl.out_dir(HERE), expected, anchors, prompts, sidedoor)
