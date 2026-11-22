# SPDX-License-Identifier: AGPL-3.0-or-later
"""T3 Code messages a person typed on another computer, scored on the
server where the agent runs: SM 2.2 rule 9's relayed origin, 2.3's typing
lead and 2.4's reading after a stop, at level C. See README.md in this
directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import relayed as rl  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/t3-away"
fx = tl.Fixture(
    NAME,
    title="T3 Code messages typed on another computer, scored on the server that runs the agent",
    date="2026-11-23",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="linux",
    keys=False,
    device=tl.key("d", "relayed t3 server"),
    boot_id=tl.key("b", "relayed t3 server boot"),
    boot="06:12:40.000",
    seq0=48211,
    last_input="06:12:40.000",
)
at = fx.at
P = tl.key("p", "relayed weather-cli")
S = tl.key("s", "relayed t3-away session")
THREAD = tl.key("r", "relayed t3-away thread")
ENV = dict(harness="claude_code", version="2.1.334", session=S, project=P)
TURN = {n: tl.key("t", f"relayed t3-away {n}") for n in ("T1", "T2", "T3", "T4")}
FILE = {n: tl.key("f", f"relayed t3-away {n}") for n in ("forecast.py", "cli.py")}


def hook(t, kind, data):
    return fx.emit(at(t), kind, data, source="hook", via="hook", **ENV)


def prompt(t, turn, length, low=False):
    return hook(t, "prompt.submit", {"turn_key": TURN[turn], "len_chars": length, "pasted_chars": 0,
                                     "typed_len_est": length, "origin_declared": "none",
                                     "matches_pending_cron": False, "answers_question": False,
                                     "low_effort": low})


def relay(t, sent, length, low=False):
    """The T3 reader's record of a user-role message whose createdBy is
    user. Its prompt is filled in once the events are built, since the
    prompt.submit it names came first."""
    return fx.emit(at(t), "relay.message", {"app": "t3_code", "sent_at": at(sent), "thread_key": THREAD,
                                            "len_chars": length, "low_effort": low, "prompt": ""},
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


# T3 Code's server spawned this Claude Code session through the Agent SDK:
# no TTY, and nothing in the process tree pairtime can class.
hook("19:56:10", "session.start", {"start_kind": "startup", "entrypoint": "unknown", "parent_class": "unknown",
                                   "has_tty": False, "permission_mode": "bypassPermissions"})

# M1, typed in T3 Code's web client on a laptop and sent at 20:01:00. T3
# hands it to the agent a second later; the T3 reader sees the row two
# seconds after that.
link = []
p1 = prompt("20:01:01", "T1", 90)
r1 = relay("20:01:03", "20:01:00", 90)
link.append((r1, p1))
output("20:01:20", "T1", 60)
edit("20:02:00", "20:01:58", "forecast.py", 20, 10)
output("20:03:30", "T1", 150, final=True)
stop("20:03:30", "T1")

# M2, sent at 20:07:00, 210 s after T1 stopped.
p2 = prompt("20:07:01", "T2", 45)
r2 = relay("20:07:03", "20:07:00", 45)
link.append((r2, p2))
output("20:07:30", "T2", 45)
output("20:09:00", "T2", 30, final=True)
stop("20:09:00", "T2")

# An agent in another thread sends this thread a message through T3's MCP
# tools at 20:12:00. T3 stores it with createdBy agent, so the T3 reader
# writes nothing for it.
prompt("20:12:00", "T3", 240)
output("20:12:40", "T3", 90)
edit("20:13:30", "20:13:29", "cli.py", 5, 3)
output("20:15:00", "T3", 60, final=True)
stop("20:15:00", "T3")

# M3, "yes", sent at 20:26:00: 660 s after the agent's turn stopped and
# 1,020 s after T2 did.
p4 = prompt("20:26:01", "T4", 3, low=True)
r4 = relay("20:26:03", "20:26:00", 3, low=True)
link.append((r4, p4))
output("20:27:30", "T4", 45, final=True)
stop("20:27:30", "T4")

events = fx.build()
# Each relay record names the prompt.submit that came before it.
for r, p in link:
    re = fx.event_at("relay.message", r[0], sent_at=r[4]["sent_at"])
    pe = fx.event_at("prompt.submit", p[0], turn_key=p[4]["turn_key"])
    re["data"]["prompt"] = pe["event_id"]
for e in events:
    if e["kind"] == "relay.message":
        tl.check(e["data"]["prompt"], "every relay record here names its prompt")

T0, END = at("20:00:00"), at("20:30:00")
ER, LB, CODES = rl.ERRATUM, rl.LOOKBACK_ERRATUM, rl.CODES_ERRATUM
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rule 9, 2.3, 2.4, 2.8", evidence="C",
                  witness="witnessed", projects={P: "weather-cli"}, start="20:00:00", end="20:30:00")
LEAD = "2.3: a relayed message's lead is its typing time, ending at sent_at"
READ = "2.4: a relayed session reads after a stop, until its next relayed message within 600 s, 120 s at most"


def row(minute, credit=None, unc=None, codes=None, agent=0, sm=None, note=None, erratum=None, bucket=True,
        end=None):
    exp.row(minute, end, credit=credit, uncredited=unc, codes=codes, agent=agent, sm=sm,
            derived=DERIVED if bucket else None, note=note, erratum=erratum, bucket=bucket)


row("20:00", {P: {"P": 60}}, {}, sm=LEAD, note="M1's 90 characters type in 90 / 1.5 = 60 s, from 20:00:00 to its "
    "stamp at 20:01:00. It is the thread's first message, so nothing else caps the lead.")
row("20:01", {}, None, ["X.agent_only", "X.no_anchor"], 59, sm="2.2 rule 9: M1's prompt is relayed", erratum=LB,
    note="T3 hands M1 to the agent at 20:01:01, so 20:01:00 is in neither the lead nor the turn. T1 runs from "
         "20:01:01: X.agent_only 59.")
row("20:02", {}, {"X.agent_only": 60}, agent=60, sm="2.4: no relayed session reads while its agent runs")
row("20:03", {P: {"READ": 30}}, {"X.agent_only": 30}, agent=30, sm=READ,
    note="T1 stops at 20:03:30. M2 follows at 20:07:00, 210 s later, so the session reads from the stop. T1 "
         "filled 20 + 20 + 225 + 50 = 315 s, which the 120 s cap holds to 120.")
row("20:04", {P: {"READ": 60}}, {}, sm=READ)
row("20:05", {P: {"READ": 30}}, None, ["X.bucket_cap"], sm=READ, erratum=LB,
    note="The 120 s end at 20:05:30: 30 + 60 + 30. With BUCKET_MAX_S's 600 s the session would read on.")
row("20:06", {P: {"P": 30}}, None, ["X.bucket_cap"], sm=LEAD, erratum=LB,
    note="M2's 45 characters: 30 s, from 20:06:30. M1 came 360 s earlier, so the cap at the seconds since the "
         "thread's previous message doesn't bind.")
row("20:07", {}, None, ["X.agent_only", "X.no_anchor"], 59, sm="2.2 rule 9: M2's prompt is relayed", erratum=LB,
    note="T3 hands M2 over at 20:07:01, and T2 runs from then. The prompt resets the bucket to its 20 s base.")
row("20:08", {}, {"X.agent_only": 60}, agent=60, sm="2.4: no relayed session reads while its agent runs")
row("20:09", bucket=False, sm="5.1: no credit and no agent running",
    note="T2 stops at 20:09:00. The next relayed message is M3 at 20:26:00, 1,020 s later, so nothing reads "
         "T2's 45 s.")
row("20:10", end="20:12", bucket=False, sm="5.1: no credit and no agent running")
row("20:12", {}, None, ["X.automated"], 60, sm="2.2 rule 4: no relay record, so the agent's message is headless",
    erratum=CODES,
    note="Another agent's message reaches the agent at 20:12:00. T3 stored it with createdBy agent, so no relay "
         "record witnesses it: entrypoint unknown and no TTY make it headless, and its turn T3 is automated.")
row("20:13", {}, None, ["X.automated"], 60, sm="2.2 rule 4", erratum=CODES)
row("20:14", {}, None, ["X.automated"], 60, sm="2.2 rule 4", erratum=CODES)
row("20:15", end="20:25", bucket=False, sm="5.1: no credit and no agent running",
    note="T3 stops at 20:15:00. Its output fills the bucket, which an automated turn doesn't reset, to the cap, "
         "but M3 comes 660 s after the stop, so nothing reads it.")
row("20:25", {P: {"GO": 2}}, None, ["X.no_anchor"], sm=LEAD, erratum=LB,
    note="M3 is \"yes\": a go-ahead, whose relayed lead is its typing time alone, 3 / 1.5 = 2 s.")
row("20:26", {}, None, ["X.agent_only", "X.no_anchor"], 59, sm="2.2 rule 9: M3's prompt is relayed", erratum=LB)
row("20:27", {}, None, ["X.agent_only", "X.no_anchor"], 30, sm="2.4", erratum=LB,
    note="T4 stops at 20:27:30. Nothing follows within the window.")
row("20:28", end="20:30", bucket=False, sm="5.1: no credit and no agent running")

rp = rl.Replay(events, T0, END)
rl.check_rows(exp, rp, {S: P})
tl.check(not rp.conflicts and not rp.blocked, "one session, and no message within RELAY_MIN_GAP_S of another")
tl.check(rp.origin[fx.event_at("prompt.submit", at("20:12:00"))["event_id"]] == "headless",
         "the agent's message is headless")

totals = exp.totals()
tl.check(totals["credited_s"] == 212, f"credited {totals['credited_s']}")
tl.check(totals["credited_by_reason"] == {"P": 90, "GO": 2, "READ": 120}, f"by reason {totals['credited_by_reason']}")
agent_only = sum(1 for t in rp.running if t not in rp.credited and t not in rp.automated)
expected = exp.doc(
    extra_totals={
        "level_c_s": 212,
        "via_relay_s": 212,
        "relayed_prompts": 3,
        "headless_prompts": 1,
        "agent_only_s": agent_only,
        "automated_s": len(rp.automated),
        "capped_s": len(rp.capped),
    },
    stated=[],
    approx={},
    errata=[ER, LB, CODES],
    notes=[
        "No SM section describes this case. Every row comes from SM 2.2 rule 9, 2.3, 2.4 and 2.8 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them. Every credited second is level C with via_relay, "
        "and witnessed: the server uploads live.",
        "The server has no desktop, so pairtime samples no presence and records no focus: every slot's presence "
        "is unknown.",
        "relayed_prompts counts the prompts a relay record witnessed; headless_prompts the one rule 4 took.",
        "automated_s is the seconds only the agent's turn T3 ran in, whose code waits on WP-FX1's 6.6 erratum. "
        "capped_s is the seconds only the 120 s cap kept from being read, whose code waits on the lookback "
        "erratum's point 2, as are the seconds nothing covers.",
    ],
)

anchors, turns = [], []
for r, p in link:
    re = fx.event_at("relay.message", r[0], sent_at=r[4]["sent_at"])
    pe = fx.event_at("prompt.submit", p[0], turn_key=p[4]["turn_key"])
    low = pe["data"]["low_effort"]
    lead = rl.typing_ms(re["data"]["len_chars"]) // 1000
    a = tl.anchor(fx, re, kind="go_ahead" if low else "substantive_prompt", origin="relayed",
                  prompt_class="low_effort" if low else "substantive", lead_s=lead, trail_s=0, lead_from="params",
                  level="C", sm=f"2.3: a relayed message's lead is its typing time, {re['data']['len_chars']} / 1.5 = "
                                 f"{lead} s, ending at sent_at")
    a["sent_at"] = re["data"]["sent_at"]
    a["local_sent_at"] = fx.hhmmss(re["data"]["sent_at"])
    a["prompt"] = pe["event_id"]
    a["labels"] = ["via_relay"]
    anchors.append(a)
anchors.sort(key=lambda a: a["sent_at"])
for name, cls, origin in (("T1", "substantive", "relayed"), ("T2", "substantive", "relayed"),
                          ("T3", None, "headless"), ("T4", "low_effort", "relayed")):
    sub = [e for e in events if e["kind"] == "prompt.submit" and e["data"]["turn_key"] == TURN[name]][0]
    stp = [e for e in events if e["kind"] == "agent.stop" and e["data"]["turn_key"] == TURN[name]][0]
    turns.append(tl.turn(fx, sub["t"], stp["t"], session=S, turn_key=TURN[name], origin=origin, prompt_class=cls,
                         sm="2.2 rule 9" if origin == "relayed" else "2.2 rule 4"))

prompt_entries = []
for e in events:
    if e["kind"] == "prompt.submit":
        name = [n for n, k in TURN.items() if k == e["data"]["turn_key"]][0]
        prompt_entries.append({"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]),
                               "text": "yes" if name == "T4" else None, "len_chars": e["data"]["len_chars"],
                               "source": "invented for this case; only its length matters" if name != "T4"
                               else "invented for this case: a go-ahead on SM 2.2's list"})

readme = os.path.join(HERE, "README.md")
rl.check_readme(readme, expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
