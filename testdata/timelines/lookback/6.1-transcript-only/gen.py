# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.1's afternoon as lookback sees it: the harness events of
testdata/timelines/6.1 with every OS, editor and git event removed, scored
by SM 2.8. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import lookback as lb  # noqa: E402

NAME = "lookback/6.1-transcript-only"
fx = tl.Fixture(
    NAME,
    title="Three hours of deep flow with Claude Code, from the transcript alone",
    date="2026-11-21",
    timezone_name="Asia/Kolkata",
    utc_offset_min=330,
    os_name="macos",
    # No presence sample exists, so no idle_keys_s either.
    keys=False,
    device=tl.key("d", "6.1 rowan macbook"),
    boot_id=tl.key("b", "6.1 boot"),
    boot="08:02:37.000",
    # Lookback runs on the agent's first start, so its records are the
    # device's first.
    seq0=1,
    last_input="08:02:37.000",
)
at = fx.at
P = tl.key("p", "cs2go")
S = tl.key("s", "6.1 claude session")

live = lb.read_events("6.1")
kept = lb.transcript_only(fx, live)
events = fx.build()
tl.check(len(events) == kept and all(e["via"] == "log" for e in events), "transcript records only")
tl.check({e["kind"] for e in events} == {"session.start", "prompt.submit", "agent.output", "agent.tool",
                                         "agent.permission", "agent.stop"}, "the kinds 6.1's transcript holds")

CYCLES = 15
CYCLE_MS = 720_000
T0, END = at("14:00:00"), at("17:00:00")


def start(k):
    return T0 + (k - 1) * CYCLE_MS


def off(mss):
    m, s = mss.split(":")
    return int(m) * 60_000 + round(float(s) * 1000)


# The inputs the rows rest on, read back from the events.
prompts = [e for e in events if e["kind"] == "prompt.submit"]
stops = [e for e in events if e["kind"] == "agent.stop"]
perms = [e for e in events if e["kind"] == "agent.permission"]
tl.check([e["t"] for e in prompts] == [start(k) + off("4:00") for k in range(1, CYCLES + 1)], "a prompt at 4:00")
tl.check([e["t"] for e in stops] == [start(k) + off("6:30") for k in range(1, CYCLES + 1)], "a stop at 6:30")
tl.check(all(e["data"]["typed_len_est"] == 300 and e["data"]["origin_declared"] == "none" for e in prompts),
         "every prompt is 300 typed characters, none declared automated")
tl.check([(e["data"]["requested_at"], e["data"]["answered_at"]) for e in perms]
         == [(start(k) + off("5:10"), start(k) + off("5:25")) for k in range(1, CYCLES + 1)],
         "a permission asked at 5:10 and answered at 5:25")
tl.check(all(e["data"]["decided_by"] == "user" for e in perms), "the user answers every permission")
# The reading rule: each stop is followed by the next cycle's prompt 570 s
# later, inside SM 2.8's 600 s, except the last, which nothing follows.
for k in range(1, CYCLES):
    tl.check(prompts[k]["t"] - stops[k - 1]["t"] == 570_000, f"cycle {k}: the next prompt comes 570 s after the stop")
tl.check(max(e["t"] for e in events) == stops[-1]["t"], "the transcript ends with cycle 15's stop")

# The rows, computed by hand from SM 2.8 (README.md): per cycle, the
# prompt's typing lead from 0:40 to 4:00, the permission's 15 s wait and
# 10 s trail from 5:10 to 5:35, and 120 s of reading from the stop at 6:30
# to 8:30 when the next prompt follows within 600 s. The agent runs from
# 4:00 to 6:30.
SEEN = "the events of 6.1's {} row"
SAME = "; cycles 2 to 15 have the same events, 12 minutes apart"
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
ER = lb.ERRATUM

exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.8 and 6.1", evidence="C", witness="backfilled",
                  projects={P: "cs2go"}, start="14:00:00", end="17:00:00")
for k in range(1, CYCLES + 1):
    C = start(k)
    last = k == CYCLES

    def row(m, *args, **kw):
        sm = "2.8; " + SEEN.format(f"14:{m:02d}") + ("" if k == 1 else SAME)
        exp.row(fx.hhmm(C + m * 60_000), *args, sm=sm, derived=kw.pop("derived", DERIVED), **kw)

    note0 = None
    if k == 1:
        note0 = ("The prompt at 14:04:00 has 300 typed characters. Its lead is typing time only, 300 / 1.5 = "
                 "200 s, from 14:00:40. No think lead, no editor anchors and no stop before it: nothing covers "
                 "14:00:00 to 14:00:40.")
    if k == 2:
        note0 = ("14:12:00 to 14:12:40 lie between cycle 1's stop and this prompt, so the session would read them "
                 "but for the 120 s cap: X.bucket_cap, as in the last 30 s of 14:08.")
    # Cycle 1's first 40 s follow no stop. Every later cycle's lie between
    # the stop before them and their prompt, where only the cap stops the
    # reading.
    row(0, credit={P: {"P": 20}}, uncredited={"X.no_anchor": 40} if k == 1 else {"X.bucket_cap": 40}, note=note0)
    row(1, credit={P: {"P": 60}}, uncredited={})
    row(2, credit={P: {"P": 60}}, uncredited={})
    row(3, credit={P: {"P": 60}}, uncredited={})
    row(4, credit={}, uncredited={"X.agent_only": 60}, agent=60,
        note="Lookback credits no reading while the agent runs (SM 2.8), so no SUP." if k == 1 else None)
    row(5, credit={P: {"PA": 25}}, uncredited={"X.agent_only": 35}, agent=60, erratum=ER,
        note="The permission asked at 14:05:10 is answered at 14:05:25: its 15 s wait, under 60 s, and its "
             "10 s trail, 14:05:10 to 14:05:35." if k == 1 else None)
    if not last:
        n6 = None
        if k == 1:
            n6 = ("The agent stops at 14:06:30 and the next prompt comes at 14:16:00, 570 s later, so the turn's "
                  "reading counts: 120 s of its 553 1/3 s of output, from the stop to 14:08:30.")
        if k == 6:
            n6 = ("The live fixture's drink, 15:06:40 to 15:12:40, leaves no record in a transcript, so this "
                  "cycle reads like the others.")
        row(6, credit={P: {"READ": 30}}, uncredited={"X.agent_only": 30}, agent=30, note=n6)
        row(7, credit={P: {"READ": 60}}, uncredited={})
        row(8, credit={P: {"READ": 30}}, uncredited={"X.bucket_cap": 30},
            note="The 120 s of reading end at 14:08:30. The turn's output would last to the next prompt's "
                 "lead, so only the cap keeps the other 30 s from being read: X.bucket_cap (SM 5.2)."
            if k == 1 else None)
        exp.row(fx.hhmm(C + 9 * 60_000), fx.hhmm(C + 12 * 60_000), bucket=False, sm="2.8; " + SEEN.format(
            "14:09 to 14:11") + ("" if k == 1 else SAME), derived=["bucket"],
            note="Nothing is credited and no agent runs, so these minutes have no bucket (SM 5.1). The live "
                 "fixture's browser run and commit or hand edit leave no record in a transcript." if k == 1 else None)
    else:
        row(6, credit={}, uncredited={"X.agent_only": 30, "X.no_anchor": 30}, agent=30,
            note="Cycle 15's turn stops at 16:54:30 and the transcript holds no later prompt, so its reading "
                 "doesn't count (SM 2.8). 16:54:00 to 16:54:30 is X.agent_only, and the 30 s after the stop "
                 "are X.no_anchor (SM 5.2).")
        exp.row(fx.hhmm(C + 7 * 60_000), fx.hhmm(C + 12 * 60_000), bucket=False,
                sm="2.8; " + SEEN.format("14:07 to 14:11") + SAME, derived=["bucket"],
                note="No reading, no anchor and no agent, so no bucket.")

# The replay of SM 2.8, second by second, must give the same rows.
rp = lb.Replay(events, T0, END)
lb.check_rows(exp, rp, {S: P})
tl.check(not rp.conflicts, "one session, so no second is wanted twice")
# SM 5.2's codes. The cap cuts 30 s after each of cycles 1 to 14's reading
# and the first 40 s of cycles 2 to 15; nothing covers cycle 1's first 40 s
# or the 30 s after cycle 15's stop.
codes = lb.uncredited_codes(rp, T0, END)
tl.check(codes == {"X.agent_only": 15 * 125, "X.bucket_cap": 14 * (30 + 40), "X.no_anchor": 40 + 30},
         f"uncredited seconds {codes}")

totals = exp.totals()
by_reason = totals["credited_by_reason"]
def credited(rows):
    return sum(sum(sum(v.values()) for v in r.get("credit", {}).values()) for r in rows)


first, lastc = credited(exp.rows[:10]), credited(exp.rows[-8:])
tl.check([r["local"] for r in exp.rows[:10]][-1] == "14:09-14:11" and exp.rows[-8]["local"] == "16:48",
         "cycle 1 is the first ten rows and cycle 15 the last eight")
tl.check(first == 345 and lastc == 225, f"cycle 1 credits {first} s and cycle 15 {lastc} s")
tl.check(totals["credited_s"] == 14 * 345 + 225 == 5055, "credited")
tl.check(by_reason == {"P": 15 * 200, "PA": 15 * 25, "READ": 14 * 120}, f"by reason {by_reason}")
tl.check(totals["agent_running_s"] == 15 * 150, "the agent runs 150 s a cycle")
agent_only = len(rp.running) - sum(1 for t in rp.running if t in rp.credited)
tl.check(agent_only == 15 * 125, f"agent-only {agent_only}")

# Hackatime's replay of the agent events (SM 5.4), for the D5 note.
in_window = [e for e in events if T0 <= e["t"] < END]
ai_logged = tl.hackatime_replay([e["t"] for e in in_window if e["kind"] in ("prompt.submit", "agent.tool",
                                                                              "agent.stop")])

expected = exp.doc(
    extra_totals={
        "wall_s": 10800,
        "ai_credited_s": 5055,
        "hand_credited_s": 0,
        "agent_only_s": agent_only,
        "first_cycle_credited_s": first,
        "last_cycle_credited_s": lastc,
    },
    stated=[],
    approx={},
    errata=[ER],
    notes=[
        "Every row is computed from SM 2.8; SM states no lookback rows, so every bucket row's fields are "
        "derived. Every credited second is level C and backfilled.",
        "input_keys is false because the fixture has no presence sample at all. The Mac reports key presses "
        "(testdata/timelines/6.1 has idle_keys_s on every sample), but no sampler ran before pairtime did.",
        "uncredited sums every bucket row. agent_only_s is the agent's 2,250 s less the 25 s of PA credited "
        "inside each turn: 1,875, all of them X.agent_only.",
        "Cycles 1 to 14 credit 120 s of reading each, 1,680 s in all, because each stop is followed by the "
        "next prompt 570 s later, inside SM 2.8's 600 s.",
        "The live fixture credits 10,095 s of the same afternoon. Lookback keeps 5,055: no think leads, no "
        "editor, browser or commit time, no reading while the agent runs, at most 120 s of reading a turn.",
        f"The agent events replay to ai_logged_s {ai_logged:g} under min(gap, 120 s) (SM 5.4), so DESIGN 6.2 "
        f"D5 would let a program that opts in to level C count at most {ai_logged / 3:g} s of this, and only "
        "inside forge-verified commit intervals.",
    ],
)

anchors, turns = [], []
for k in range(1, CYCLES + 1):
    C = start(k)
    same = "" if k == 1 else SAME
    sub = fx.event_at("prompt.submit", C + off("4:00"))
    anchors.append(tl.anchor(fx, sub, kind="substantive_prompt", origin="human_typed", prompt_class="substantive",
                             lead_s=200, trail_s=0, lead_from="params", level="C",
                             sm="2.8: no input check, so human_typed; the lead is typing time only, 300 / 1.5 = "
                                "200 s" + same))
    pa = fx.event_at("agent.permission", C + off("5:25"))
    anchors.append(tl.anchor(fx, pa, kind="permission_answer", lead_s=15, trail_s=10, lead_from="params", level="C",
                             sm="2.8: a permission answer keeps its wait, 15 s, under the 60 s cap; the 10 s "
                                "trail" + same))
    turns.append(tl.turn(fx, C + off("4:00"), C + off("6:30"), session=S, turn_key=sub["data"]["turn_key"],
                         origin="human_typed", prompt_class="substantive", sm="2.8; 6.1's 14:04 to 14:06" + same))

prompt_entries = []
for p in lb.read_prompts("6.1"):
    prompt_entries.append({"turn_key": p["turn_key"], "t": p["t"], "local": p["local"], "text": p["text"],
                           "len_chars": p["len_chars"], "source": p["source"]})

# README.md works the same rows out by hand; its minute tables and totals
# must say what expected.json says. internal/timelines checks this too.
readme = os.path.join(HERE, "README.md")
lb.check_readme(readme, expected, exp.projects)
lb.check_readme_totals(readme, expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
