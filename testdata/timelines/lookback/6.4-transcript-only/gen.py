# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.4's night as lookback sees it: the harness events of
testdata/timelines/6.4 with every OS, editor and git event removed, scored
by SM 2.8. See README.md in this directory."""

import json
import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import lookback as lb  # noqa: E402

NAME = "lookback/6.4-transcript-only"
LENGTHS = "2026-11-19-sm-6.4-prompt-lengths"
fx = tl.Fixture(
    NAME,
    title="A /goal agent running unattended overnight, from the transcript alone",
    date="2026-11-20",
    timezone_name="Europe/Berlin",
    utc_offset_min=60,
    os_name="linux",
    # No presence sample exists, so no idle_keys_s either.
    keys=False,
    device=tl.key("d", "6.4 jordan desktop"),
    boot_id=tl.key("b", "6.4 boot"),
    boot="17:12:40.000",
    seq0=1,
    last_input="17:12:40.000",
)
at = fx.at
P = tl.key("p", "pixel-forge")
S = tl.key("s", "6.4 claude session")

live = lb.read_events("6.4")
kept = lb.transcript_only(fx, live)
events = fx.build()
tl.check(len(events) == kept and all(e["via"] == "log" for e in events), "transcript records only")
tl.check({e["kind"] for e in events} == {"session.start", "prompt.submit", "agent.output", "agent.tool",
                                         "agent.stop"}, "the kinds 6.4's transcript holds")
tl.check(sum(e["kind"] == "git.change" for e in live) == 31, "the live fixture's 31 commits are gone")

T0, END = at("23:37:00"), at("+1 07:37:00")
GOAL, MORNING = at("23:40:00"), at("+1 07:37:00")
prompts = [e for e in events if e["kind"] == "prompt.submit"]
stops = [e for e in events if e["kind"] == "agent.stop"]
tl.check([(e["t"], e["data"]["typed_len_est"]) for e in prompts] == [(GOAL, 75), (MORNING, 61)],
         "his two prompts, 75 and 61 characters by SM's count")
tl.check(prompts[0]["data"]["slash_command"] == "/goal", "the first is the /goal")
tl.check(len(stops) == 9 and [e["data"]["stop_hook_active"] for e in stops] == [False] + [True] * 8,
         "T1 stops, then eight stop-hook continuations")
tl.check(stops[0]["t"] == at("+1 00:23:20") and stops[-1]["t"] == at("+1 06:10:00"), "the run's stops")
# The reading rule: no stop is followed by a human prompt within 600 s.
tl.check(MORNING - stops[-1]["t"] == 5_220_000, "the morning prompt comes 5,220 s after the last stop")

ER, CODES = lb.ERRATUM, lb.CODES_ERRATUM
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.8 and 6.4", evidence="C", witness="backfilled",
                  projects={P: "pixel-forge"}, start="23:37:00", end="+1 07:37:00")
exp.row("23:37", "23:39", bucket=False, sm="2.8; the events of 6.4's 23:37 and 23:38 rows", derived=["bucket"],
        note="No lead reaches back here and no agent runs, so no bucket. The live fixture credits these minutes "
             "from the prompt's think lead, which lookback drops.")
exp.row("23:39", credit={P: {"P": 50}}, uncredited=None, codes=["X.no_anchor"], sm="2.8; the events of 6.4's 23:39 row",
        derived=DERIVED, erratum=ER,
        note="The /goal prompt at 23:40:00 has 75 typed characters by SM's count: typing time only, 75 / 1.5 = "
             "50 s, from 23:39:10. The quoted text has 73 (WP-FX1's prompt-lengths erratum), which would make it "
             "48.67 s. The code of 23:39:00 to 23:39:10 is open.")
exp.row("23:40", "+1 00:23", credit={}, uncredited={"X.agent_only": 43 * 60}, agent=43 * 60,
        sm="2.8; the events of 6.4's 23:40 to 23:43 rows and its 23:44 to 07:29 range", derived=DERIVED,
        note="T1, his /goal turn, runs to 00:23:20. Lookback credits no reading while the agent runs, so the "
             "live fixture's SUP from 23:40:00 to 23:43:40 is gone.")
exp.row("+1 00:23", "+1 06:10", credit={}, uncredited=None, codes=["X.agent_only", "X.automated"],
        agent=347 * 60, sm="2.8; the events of 6.4's 23:44 to 07:29 range", derived=DERIVED, erratum=CODES,
        note="T1's last 20 s, then T2 to T9, stop-hook continuations (SM 2.2 rule 2, which lookback keeps), to "
             "06:10:00. Nothing is credited. Whether a continuation's seconds are X.automated or X.agent_only "
             "is point 1 of WP-FX1's 6.6 erratum.")
exp.row("+1 06:10", "+1 07:36", bucket=False, sm="2.8; the events of 6.4's 23:44 to 07:29 range and its 07:30 "
        "to 07:35 rows", derived=["bucket"],
        note="No agent runs and nothing is credited, so no bucket. The live fixture's reading from 07:30:00 needs "
             "the unlock and his scrolling; here the run's last stop, at 06:10:00, is 5,220 s before his next "
             "prompt, past SM 2.8's 600 s.")
exp.row("+1 07:36", credit={P: {"P": 41}}, uncredited=None, codes=["X.no_anchor"],
        sm="2.8; the events of 6.4's 07:36 row", derived=DERIVED, erratum=ER,
        note="The prompt at 07:37:00 has 61 typed characters by SM's count: 61 / 1.5 = 40.67 s, rounded to 41 as "
             "SM rounds the live lead's 160.67 s to 161, so it starts at 07:36:19. The quoted text has 60, which "
             "would make it 40 s (WP-FX1's prompt-lengths erratum). The code of 07:36:00 to 07:36:19 is open.")

rp = lb.Replay(events, T0, END)
lb.check_rows(exp, rp, {S: P})
tl.check(not rp.conflicts, "one session, so no second is wanted twice")
tl.check(all(not v for v in rp.spans.values()), "no stop is followed by a human prompt within 600 s")

totals = exp.totals()
tl.check(totals["credited_s"] == 50 + 41 == 91, "credited")
tl.check(totals["agent_running_s"] == 23_400, "the agent runs 23,400 s")
agent_only = len(rp.running) - sum(1 for t in rp.running if t in rp.credited)
tl.check(agent_only == 23_400, f"agent-only {agent_only}")

# As the live fixture replays it: every agent event, the morning prompt at
# the window's end included.
ai_logged = tl.hackatime_replay([e["t"] for e in events if e["kind"] in ("prompt.submit", "agent.tool",
                                                                           "agent.stop")])

expected = exp.doc(
    extra_totals={
        "ai_credited_s": 91,
        "hand_credited_s": 0,
        "agent_only_s": agent_only,
    },
    stated=[],
    approx={},
    errata=[ER, CODES, LENGTHS],
    notes=[
        "Every row is computed from SM 2.8; SM states no lookback rows, so every bucket row's fields are "
        "derived. Every credited second is level C and backfilled.",
        "input_keys is false because the fixture has no presence sample at all. The desktop's key counter "
        "(testdata/timelines/6.4) wasn't running before pairtime was.",
        "Lookback credits 91 s of the live fixture's 810: the two prompts' typing time, 50 s and 41 s. No "
        "reading counts, because no stop is followed by a human prompt within 600 s: T1 stops at 00:23:20, "
        "the run's last turn at 06:10:00, and his next prompt comes at 07:37:00.",
        "The agent runs 23,400 s and nothing credits any of it, so agent_only_s is 23,400; the live fixture's "
        "is 23,180. uncredited sums only the rows whose codes SM decides: T1's seconds to 00:23:00.",
        "The lock at 23:53:10, the unlock at 07:30:00 and the agent's 31 commits aren't in a transcript. "
        "Lookback doesn't need them: without a human prompt after the run, the night credits nothing either way.",
        f"The agent events replay to ai_logged_s {ai_logged:g} under min(gap, 120 s) (SM 5.4). DESIGN 6.2 D5's "
        f"cap, min(credited, ai_logged / 3), is the 91 s credited.",
    ],
)

with open(os.path.join(lb.TIMELINES, "6.4", "anchors.json"), encoding="utf-8") as f:
    live_turns = json.load(f)["turns"]
goal = fx.event_at("prompt.submit", GOAL)
morning = fx.event_at("prompt.submit", MORNING)
anchors = [
    tl.anchor(fx, goal, kind="substantive_prompt", origin="human_typed", prompt_class="substantive", lead_s=50,
              trail_s=0, lead_from="params", level="C",
              sm="2.8: no input check, so human_typed; a typed /goal with an objective is substantive (SM 6.4); "
                 "the lead is typing time only, 75 / 1.5 = 50 s"),
    tl.anchor(fx, morning, kind="substantive_prompt", origin="human_typed", prompt_class="substantive", lead_s=41,
              trail_s=0, lead_from="params", level="C",
              sm="2.8: the lead is typing time only, 61 / 1.5 = 40.67 s, rounded to 41 as SM 6.4 rounds 160.67"),
]
turns = []
for t in live_turns:
    t = dict(t)
    t["sm"] = "2.8 keeps origin rules 1 to 5; " + t["sm"]
    turns.append(t)

prompt_entries = [dict(p) for p in lb.read_prompts("6.4")]
# README.md works the same rows out by hand; its minute tables and totals
# must say what expected.json says. internal/timelines checks this too.
readme = os.path.join(HERE, "README.md")
lb.check_readme(readme, expected, exp.projects)
lb.check_readme_totals(readme, expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
