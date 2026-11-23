# SPDX-License-Identifier: AGPL-3.0-or-later
"""T3 Code messages sent while a turn runs: one queued 5 s after the message
before it, one steered into the running turn, and one queued behind a turn
that runs past RELAY_MATCH_S. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import relayed as rl  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/t3-queued"
fx = tl.Fixture(
    NAME,
    title="T3 Code messages sent while a turn runs: queued, steered, and queued too long",
    date="2026-11-24",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="linux",
    keys=False,
    device=tl.key("d", "relayed t3 queued server"),
    boot_id=tl.key("b", "relayed t3 queued server boot"),
    boot="05:40:12.000",
    seq0=7310,
    last_input="05:40:12.000",
)
at = fx.at
P = tl.key("p", "relayed t3-queued budget-app")
S = tl.key("s", "relayed t3-queued session")
THREAD = tl.key("r", "relayed t3-queued thread")
ENV = dict(harness="claude_code", version="2.1.334", session=S, project=P)
TURN = {n: tl.key("t", f"relayed t3-queued {n}") for n in ("T1", "T2", "T4", "T5")}
FILE = {n: tl.key("f", f"relayed t3-queued {n}") for n in ("ledger.ts", "report.ts")}


def hook(t, kind, data):
    return fx.emit(at(t), kind, data, source="hook", via="hook", **ENV)


def prompt(t, turn, length):
    return hook(t, "prompt.submit", {"turn_key": TURN[turn], "len_chars": length, "pasted_chars": 0,
                                     "typed_len_est": length, "origin_declared": "none",
                                     "matches_pending_cron": False, "answers_question": False,
                                     "low_effort": False})


def relay(t, sent, length):
    """The T3 reader's record of a message that passed T3's own test for a
    message a person sent from a client and started a run of its own."""
    return fx.emit(at(t), "relay.message", {"app": "t3_code", "client": "web", "sent_at": at(sent), "thread_key": THREAD,
                                            "declared": "none", "len_chars": length, "low_effort": False,
                                            "prompt": ""},
                   source="log", via="app", **ENV)


def output(t, turn, words, final=False):
    hook(t, "agent.output", {"turn_key": TURN[turn], "words": words, "code_lines": 0, "is_final": final,
                             "asks_question": False, "offers_options": False})


def edit(t, t_start, file, added, removed):
    hook(t, "agent.tool", {"tool_class": "edit", "files": [FILE[file]], "lines_added": added,
                           "lines_removed": removed, "visible_output_lines": 0, "t_start": at(t_start),
                           "t_end": at(t)})


def stop(t, turn):
    hook(t, "agent.stop", {"turn_key": TURN[turn], "stop_reason": "end_turn", "stop_hook_active": False})


hook("19:58:30", "session.start", {"start_kind": "startup", "entrypoint": "unknown", "parent_class": "agent_app",
                                   "has_tty": False, "permission_mode": "bypassPermissions"})

# M1 at 20:01:00 starts T1, a long turn.
p1 = prompt("20:01:01", "T1", 90)
r1 = relay("20:01:03", "20:01:00", 90)
# M2, 5 s after M1, sent in T3's queue mode: a queued run of its own. The
# reader sees it at 20:01:08; its run reaches the agent when T1 ends.
r2 = relay("20:01:08", "20:01:05", 45)
output("20:01:30", "T1", 60)
edit("20:03:00", "20:02:58", "ledger.ts", 16, 8)
# M3, at 20:04:00, steered into T1 (T3's default for a message sent while a
# turn runs): no run of its own, so the reader writes nothing. Whether
# Claude Code reports a steered SDK message at all is unverified; this case
# assumes it does, as a queue-operation enqueue, the stricter reading.
hook("20:04:01", "agent.queue", {"turn_key": TURN["T1"], "len_chars": 60})
output("20:06:00", "T1", 90)
output("20:09:00", "T1", 120, final=True)
stop("20:09:00", "T1")

# T2, M2's run, 476 s after M2's stamp.
p2 = prompt("20:09:01", "T2", 45)
output("20:10:00", "T2", 60)
output("20:12:00", "T2", 90, final=True)
stop("20:12:00", "T2")

# M4 at 20:14:00, with the agent idle: T3 hands it over at once.
p4 = prompt("20:14:01", "T4", 90)
r4 = relay("20:14:03", "20:14:00", 90)
# M5 at 20:15:00, queued behind T4, which runs until 20:27:00.
r5 = relay("20:15:03", "20:15:00", 60)
output("20:16:00", "T4", 60)
edit("20:20:00", "20:19:58", "report.ts", 30, 10)
output("20:27:00", "T4", 150, final=True)
stop("20:27:00", "T4")
# M5's run reaches the agent 721 s after its stamp, past RELAY_MATCH_S, so
# the agent pairs it with nothing.
p5 = prompt("20:27:01", "T5", 60)
output("20:28:30", "T5", 45, final=True)
stop("20:29:00", "T5")

events = fx.build()
ev = {e["event_id"]: e for e in events}
def find(raw, kind, **match):
    return fx.event_at(kind, raw[0], **match)
E = {name: find(raw, "prompt.submit", turn_key=raw[4]["turn_key"]) for name, raw in
     (("P1", p1), ("P2", p2), ("P4", p4), ("P5", p5))}
R = {name: find(raw, "relay.message", sent_at=raw[4]["sent_at"]) for name, raw in
     (("R1", r1), ("R2", r2), ("R4", r4), ("R5", r5))}
# The prompt came first for M1 and M4, so the relay record names it. M2's
# relay record came first, so its prompt names the record.
R["R1"]["data"]["prompt"] = E["P1"]["event_id"]
R["R4"]["data"]["prompt"] = E["P4"]["event_id"]
for r in ("R2", "R5"):
    del R[r]["data"]["prompt"]
E["P2"]["data"]["relay"] = R["R2"]["event_id"]

T0, END = at("20:00:00"), at("20:30:00")
ER = rl.ERRATUM
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rule 9, 2.3, 2.4", evidence="C",
                  witness="witnessed", projects={P: "budget-app"}, start="20:00:00", end="20:30:00")
LEAD = "2.3: a relayed message's lead is its typing time, ending at sent_at"
READ = "2.4: a relayed session reads after a stop, until its next relayed message or its next turn"


def row(minute, credit=None, unc=None, agent=0, sm=None, note=None, bucket=True, end=None):
    exp.row(minute, end, credit=credit, uncredited=unc, agent=agent, sm=sm,
            derived=DERIVED if bucket else None, note=note, bucket=bucket)


row("20:00", {P: {"P": 60}}, {}, sm=LEAD, note="M1's 90 characters: 60 s, from 20:00:00 to its stamp.")
row("20:01", {P: {"P": 5}}, {"X.agent_only": 55}, 59, sm=LEAD,
    note="M2 is sent 5 s after M1, so its lead is capped at those 5 s, 20:01:00 to 20:01:05, though 45 "
         "characters type in 30 s. Its run reaches the agent at 20:09:01, 476 s after the stamp, inside "
         "RELAY_MATCH_S, and the lead is credited at the stamp. T1 runs from 20:01:01.")
row("20:02", end="20:04", credit={}, unc={"X.agent_only": 120}, agent=120, sm="2.4: no reading while T1 runs")
row("20:04", {}, {"X.agent_only": 60}, 60, sm="2.2: a steered message has no run of its own",
    note="M3, steered into T1 at 20:04:00, earns nothing. Its agent.queue at 20:04:01 has no input in its slot, "
         "so it opens no Q anchor either.")
row("20:05", end="20:09", credit={}, unc={"X.agent_only": 240}, agent=240, sm="2.4: no reading while T1 runs")
row("20:09", {P: {"READ": 1}}, {"X.agent_only": 59}, 59, sm=READ,
    note="T1 stops at 20:09:00 and T2 starts at 20:09:01. M2 was sent before the stop, so it vouches for no "
         "reading of it, but M4, sent at 20:14:00, comes 300 s after the stop: the session reads T1's output "
         "from 20:09:00 until T2 starts, 1 s. T2's prompt then resets the bucket to its 20 s base.")
row("20:10", end="20:12", credit={}, unc={"X.agent_only": 120}, agent=120, sm="2.4: no reading while T2 runs")
row("20:12", {P: {"READ": 60}}, {}, sm=READ,
    note="T2 stops at 20:12:00 and M4 follows 120 s later. T2 filled 20 + 20 + 30 = 70 s: 60 are read here, "
         "and M4's lead takes 20:13 before the other 10.")
row("20:13", {P: {"P": 60}}, {}, sm=LEAD, note="M4's 90 characters: 60 s, from 20:13:00. M2 was sent 775 s earlier.")
row("20:14", {}, {"X.no_anchor": 1, "X.agent_only": 59}, 59, sm="2.3: a message queued past RELAY_MATCH_S opens nothing",
    note="20:14:00 is in neither a window nor a turn. T4 runs from 20:14:01. M5's lead would have run from "
         "20:14:20 to its stamp at 20:15:00, but its run reaches the agent at 20:27:01, 721 s after the stamp, "
         "so nothing pairs with it and it opens nothing.")
row("20:15", end="20:27", credit={}, unc={"X.agent_only": 720}, agent=720, sm="2.4: no reading while T4 runs")
row("20:27", {}, {"X.no_anchor": 1, "X.automated": 59}, 59, sm="2.2 rule 4: M5's prompt has no witness",
    note="T4 stops at 20:27:00, and no relayed message follows it. M5's prompt, unpaired, is headless, so T5 "
         "runs automated from 20:27:01.")
row("20:28", {}, {"X.automated": 60}, 60, sm="2.2 rule 4")
row("20:29", bucket=False, sm="5.1: no credit and no agent running", note="T5 stops at 20:29:00.")

rp = rl.Replay(events, T0, END)
rl.check_rows(exp, rp, {S: P})
tl.check(rp.origin[E["P2"]["event_id"]] == "relayed" and rp.origin[E["P5"]["event_id"]] == "headless",
         "M2's prompt is relayed and M5's headless")

totals = exp.totals()
tl.check(totals["credited_s"] == 186, f"credited {totals['credited_s']}")
expected = exp.doc(
    extra_totals={
        "level_c_s": 186,
        "via_relay_s": 186,
        "relayed_prompts": 3,
        "headless_prompts": 1,
        "unpaired_relay_records": 1,
    },
    stated=[],
    approx={},
    errata=[ER],
    notes=[
        "No SM section describes this case. Every row comes from SM 2.2 rule 9, 2.3 and 2.4 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them. Every credited second is level C with via_relay.",
        "The server has no desktop, so every slot's presence is unknown, and uncredited seconds take SM 5.2's codes "
        "for such a slot.",
        "The agent.queue at 20:04:01 assumes Claude Code reports a message T3 steers into a running turn. That is "
        "unverified, and the case credits the same either way.",
    ],
)

anchors = []
for name, rname, kind in (("P1", "R1", "substantive_prompt"), ("P2", "R2", "substantive_prompt"),
                          ("P4", "R4", "substantive_prompt")):
    re = R[rname]
    lead = min(rl.typing_ms(re["data"]["len_chars"]), 5000 if name == "P2" else 10 ** 9) // 1000
    anchors.append(rl.anchor_entry(fx, re, E[name], kind=kind, prompt_class="substantive", lead_s=lead,
                                   sm="2.3: typing time, capped at the 5 s since M1" if name == "P2"
                                   else f"2.3: typing time, {re['data']['len_chars']} / 1.5 = {lead} s"))
anchors.sort(key=lambda a: a["t"])
turns = []
for name, cls, origin in (("T1", "substantive", "relayed"), ("T2", "substantive", "relayed"),
                          ("T4", "substantive", "relayed"), ("T5", None, "headless")):
    sub = [e for e in events if e["kind"] == "prompt.submit" and e["data"]["turn_key"] == TURN[name]][0]
    stp = [e for e in events if e["kind"] == "agent.stop" and e["data"]["turn_key"] == TURN[name]][0]
    turns.append(tl.turn(fx, sub["t"], stp["t"], session=S, turn_key=TURN[name], origin=origin, prompt_class=cls,
                         sm="2.2 rule 9" if origin == "relayed" else "2.2 rule 4"))

prompt_entries = [{"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]), "text": None,
                   "len_chars": e["data"]["len_chars"], "source": "invented for this case; only its length matters"}
                  for e in events if e["kind"] == "prompt.submit"]

rl.check_readme(os.path.join(HERE, "README.md"), expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
