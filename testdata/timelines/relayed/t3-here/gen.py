# SPDX-License-Identifier: AGPL-3.0-or-later
"""t3-away's first message typed into T3 Code's desktop app on this
computer instead: SM 2.2 rules 6 and 7, tested at the app's stamp, make it
human_typed at level A. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(HERE)), "sidedoor"))

import relayed as rl  # noqa: E402
import sidedoor as sd  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/t3-here"
fx = tl.Fixture(
    NAME,
    title="A T3 Code message typed into T3's desktop app on this computer",
    date="2026-11-23",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="macos",
    keys=True,
    device=tl.key("d", "relayed t3 macbook"),
    boot_id=tl.key("b", "relayed t3 macbook boot"),
    boot="08:15:40.000",
    seq0=20544,
    last_input="19:50:12.400",
    last_key="19:49:58.100",
)
at = fx.at
P = tl.key("p", "relayed weather-cli on the macbook")
S = tl.key("s", "relayed t3-here session")
THREAD = tl.key("r", "relayed t3-here thread")
APP = tl.key("a", "relayed t3-here T3 Code desktop app")
ENV = dict(harness="claude_code", version="2.1.334", session=S, project=P)
TURN = {"T1": tl.key("t", "relayed t3-here T1")}
FILE = {"forecast.py": tl.key("f", "relayed t3-here forecast.py")}

STAMP = "20:01:00"


def hook(t, kind, data):
    return fx.emit(at(t), kind, data, source="hook", via="hook", sample=True, **ENV)


# Lena sits down at the MacBook at 19:57:30 with T3 Code's window in front.
# T3's desktop app runs its own server, which runs Claude Code, so the
# window hosts the session (SM 3.2 rule 3). She reads the thread, moving the
# pointer every 12 s, and types the 90-character message from 20:00:00.3,
# a key a second, pressing Return at 20:00:59.8. T3's server stamps the
# message at 20:01:00 and hands it to Claude Code, whose hook fires at
# 20:01:01. Inputs sit on half seconds, never on a slot boundary.
fx.pointer_every("19:57:30.500", "20:06:59", 12_000)
fx.typing("20:00:00.300", "20:00:59.300", 1000)
fx.keys_at("20:00:59.800")
fx.ticks("19:57:30", "20:07:00", tl.SLOT_MS)

hook("19:56:10", "session.start", {"start_kind": "startup", "entrypoint": "unknown", "parent_class": "agent_app",
                                   "has_tty": False, "permission_mode": "default"})
fx.emit(at("19:57:30"), "os.focus",
        {"app_class": "agent_app", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
p1 = hook("20:01:01", "prompt.submit", {"turn_key": TURN["T1"], "len_chars": 90, "pasted_chars": 0,
                                        "typed_len_est": 90, "origin_declared": "none",
                                        "matches_pending_cron": False, "answers_question": False,
                                        "low_effort": False})
# The T3 reader sees the row two seconds later. It is the same witness as on
# the server: createdBy user, creationSource web (T3's desktop window runs
# the web client), a run of its own.
r1 = fx.emit(at("20:01:03"), "relay.message",
             {"app": "t3_code", "sent_at": at(STAMP), "thread_key": THREAD, "declared": "none", "len_chars": 90,
              "low_effort": False, "prompt": ""},
             source="log", via="app", **ENV)
hook("20:01:20", "agent.output", {"turn_key": TURN["T1"], "words": 60, "code_lines": 0, "is_final": False,
                                  "asks_question": False, "offers_options": False})
hook("20:02:00", "agent.tool", {"tool_class": "edit", "files": [FILE["forecast.py"]], "lines_added": 20,
                                "lines_removed": 10, "visible_output_lines": 0, "t_start": at("20:01:58"),
                                "t_end": at("20:02:00")})
hook("20:03:30", "agent.output", {"turn_key": TURN["T1"], "words": 150, "code_lines": 0, "is_final": True,
                                  "asks_question": False, "offers_options": False})
hook("20:03:30", "agent.stop", {"turn_key": TURN["T1"], "stop_reason": "end_turn", "stop_hook_active": False})

events = fx.build()
pe = fx.event_at("prompt.submit", p1[0])
re = fx.event_at("relay.message", r1[0])
re["data"]["prompt"] = pe["event_id"]

T0, END = at("19:58:00"), at("20:07:00")

# Checks of the timing choices: input in every slot, key presses in the
# KEYS_PRE_S before the stamp, and the window in front at the stamp hosts
# the session (SM 2.2 rule 6, tested at the stamp).
tl.check(sd.input_in_every_slot(fx, T0, END), "input in every slot")
tl.check(any(k and at(STAMP) - 10_000 <= t < at(STAMP) for t, k in fx.inputs), "key presses before the stamp")
front = [e for e in events if e["kind"] == "os.focus" and e["t"] <= at(STAMP)][-1]
tl.check(S in front["data"]["hosted_sessions"], "T3's window, in front at the stamp, hosts the session")
for s in range(at("20:00:00"), at(STAMP), tl.SLOT_MS):
    tl.check(sd.keys_in_slot(fx, s), f"key presses in the typing slot at {fx.hhmmss(s)}")

# The rows, by hand from SM 2.2 rules 6 and 7 at the stamp, 2.3 and 2.4.
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rules 6 and 9, 2.3, 2.4", evidence="A",
                  witness="witnessed", projects={P: "weather-cli"}, start="19:58:00", end="20:07:00")
LEAD = "2.3: a substantive prompt's lead, 120 + 90 / 1.5 = 180 s, ending at the app's stamp (2.2)"


def row(minute, credit, unc, agent=0, sm=None, note=None):
    exp.row(minute, credit=credit, uncredited=unc, agent=agent, sm=sm, derived=DERIVED, note=note)


row("19:58", {P: {"P": 60}}, {}, sm=LEAD, note="The lead runs from 19:58:00 to the stamp at 20:01:00.")
row("19:59", {P: {"P": 60}}, {}, sm=LEAD)
row("20:00", {P: {"P": 60}}, {}, sm=LEAD, note="The typing part, 60 s, with key presses in every slot.")
row("20:01", {P: {"SUP": 40}}, {"X.no_anchor": 1, "X.agent_only": 19}, 59, sm="2.4: watching the agent work",
    note="20:01:00 is after the lead and before the turn: X.no_anchor. T1 starts at 20:01:01 with the 20 s base; "
         "60 words add 20 s at 20:01:20, and both are read by 20:01:41. Nothing new until the edit at 20:02:00.")
row("20:02", {P: {"SUP": 60}}, {}, 60, sm="2.4", note="The 30-line edit adds 225 s at 20:02:00.")
row("20:03", {P: {"SUP": 30, "READ": 30}}, {}, 30, sm="2.4",
    note="T1 stops at 20:03:30 with 150 words, 50 s more: 135 + 50 = 185 s left to read.")
row("20:04", {P: {"READ": 60}}, {}, sm="2.4")
row("20:05", {P: {"READ": 60}}, {}, sm="2.4")
row("20:06", {P: {"READ": 35}}, {"X.no_anchor": 25}, sm="2.4", note="The bucket empties at 20:06:35.")

sweep = sd.Sweep(T0, END)
sweep.anchor(at(STAMP), lead_s=180, typing_part_s=60, reason="P", level="A",
             typing_ok=lambda slot: sd.keys_in_slot(fx, slot))
sweep.reset(at("20:01:01"), rl.R_TURN_BASE_S)
sweep.fill(at("20:01:20"), sd.output_fill(words=60))
sweep.fill(at("20:02:00"), sd.output_fill(diff_lines=30))
sweep.fill(at("20:03:30"), sd.output_fill(words=150))
sweep.run(at("20:01:01"), at("20:03:30"))
sd.check_rows(exp, sweep, P)
tl.check(sd.levels(sweep) == {"A": 495, "B": 0, "C": 0}, f"levels {sd.levels(sweep)}")

totals = exp.totals()
tl.check(totals["credited_s"] == 495, f"credited {totals['credited_s']}")
expected = exp.doc(
    extra_totals={"level_a_s": 495, "via_relay_s": 0, "relayed_prompts": 0, "typed_prompts": 1},
    stated=[],
    approx={},
    errata=[rl.ERRATUM],
    notes=[
        "No SM section describes this case. It is t3-away's first message, typed into T3 Code's desktop app on "
        "the computer that runs the agent. Every row comes from SM 2.2 rules 6 and 9, 2.3 and 2.4 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them.",
        "Without the relay record the prompt would be headless: no TTY, and T3's server for its nearest non-shell "
        "ancestor (SM 2.2 rule 4). The record witnesses it, so rule 4 is skipped, and rule 6, tested at the app's "
        "stamp, holds: key presses in the 10 s before 20:01:00 and T3's window, which hosts the session, in front.",
        "Every credited second is level A: key presses in the anchor's evidence window, on macOS.",
    ],
)

anchors = [tl.anchor(fx, pe, kind="substantive_prompt", origin="human_typed", prompt_class="substantive", lead_s=180,
                     trail_s=0, lead_from="params", level="A",
                     sm="2.2 rule 6 at the app's stamp; 2.3: 120 + 90 / 1.5 = 180 s, ending at the stamp")]
anchors[0]["t"] = at(STAMP)
anchors[0]["local"] = fx.hhmmss(at(STAMP))
anchors[0]["relay"] = re["event_id"]
turns = [tl.turn(fx, pe["t"], at("20:03:30"), session=S, turn_key=TURN["T1"], origin="human_typed",
                 prompt_class="substantive", sm="2.2 rule 6, tested at the app's stamp")]
prompts = [{"turn_key": TURN["T1"], "t": pe["t"], "local": fx.hhmmss(pe["t"]), "text": None, "len_chars": 90,
            "source": "invented for this case; only its length matters"}]

rl.check_readme(os.path.join(HERE, "README.md"), expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompts})
