# SPDX-License-Identifier: AGPL-3.0-or-later
"""A T3 Code message that pairs in time for its own lead but too late to
vouch for the reading before it. M2, queued behind an agent's turn, pairs
591 s after its stamp, inside RELAY_MATCH_S, but 1,181 s after the stop
before it, past PENDING_WINDOW_S - 60 s, so nothing reads what that stop's
turn left. M3 then pairs at once and vouches for the next stop's reading:
SM 2.2 rules 4 and 9, 2.3 and 2.4, at level C. See README.md in this
directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import relayed as rl  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/t3-late"
fx = tl.Fixture(
    NAME,
    title="A T3 Code message that pairs in time for its own lead but too late to vouch for the reading before it",
    date="2026-11-25",
    timezone_name="America/Denver",
    utc_offset_min=-420,
    os_name="linux",
    keys=False,
    device=tl.key("d", "relayed t3-late server"),
    boot_id=tl.key("b", "relayed t3-late server boot"),
    boot="17:02:44.000",
    seq0=9140,
    last_input="17:02:44.000",
)
at = fx.at
P = tl.key("p", "relayed t3-late trail-log")
S = tl.key("s", "relayed t3-late session")
THREAD = tl.key("r", "relayed t3-late thread")
ENV = dict(harness="claude_code", version="2.1.334", session=S, project=P)
# M3's turn gets no T name: "T3" would read as T3 Code.
TURN = {n: tl.key("t", f"relayed t3-late {n}") for n in ("T1", "A1", "T2", "M3's turn")}
FILE = {n: tl.key("f", f"relayed t3-late {n}") for n in ("elevation.py", "errors.py", "test_errors.py", "gpx.py")}


def hook(t, kind, data):
    return fx.emit(at(t), kind, data, source="hook", via="hook", **ENV)


def prompt(t, turn, length):
    return hook(t, "prompt.submit", {"turn_key": TURN[turn], "len_chars": length, "pasted_chars": 0,
                                     "typed_len_est": length, "origin_declared": "none",
                                     "matches_pending_cron": False, "answers_question": False,
                                     "low_effort": False})


def relay(t, sent, length):
    """The T3 reader's record of a message Tessa sent from T3's web client on
    her laptop: createdBy user, creationSource web, none of T3's other marks,
    and a run of its own. Its prompt is filled in, or removed, once the
    events are built."""
    return fx.emit(at(t), "relay.message", {"app": "t3_code", "client": "web", "sent_at": at(sent),
                                            "thread_key": THREAD, "declared": "none", "len_chars": length,
                                            "low_effort": False, "prompt": ""},
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


# T3 Code's server, `t3 serve`, started this Claude Code session through the
# Agent SDK: no TTY, and the walk names T3's server as its nearest non-shell
# ancestor, agent_app (DESIGN 5.1's app-table entry).
hook("18:57:20", "session.start", {"start_kind": "startup", "entrypoint": "unknown", "parent_class": "agent_app",
                                   "has_tty": False, "permission_mode": "bypassPermissions"})

# M1, sent from the laptop at 19:01:30 with the agent idle. T3 hands it over
# a second later, and the reader sees the row two seconds after that. T1's
# output fills the bucket to its 120 s cap: 20 + 20 + 60 + 20.
p1 = prompt("19:01:31", "T1", 135)
r1 = relay("19:01:33", "19:01:30", 135)
output("19:02:00", "T1", 60)
edit("19:03:30", "19:03:28", "elevation.py", 6, 2)
output("19:06:00", "T1", 60, final=True)
stop("19:06:00", "T1")

# 19:10:00, 240 s after T1's stop. An agent in the docs thread sends this
# thread a message through T3's MCP tools. T3 stores it with createdBy agent,
# so the reader writes nothing for it, and starts its run at once. The docs
# thread runs on a provider pairtime has no hooks for, so this device records
# nothing of the sender.
pa = prompt("19:10:00", "A1", 420)
output("19:11:00", "A1", 90)
edit("19:14:00", "19:13:58", "errors.py", 12, 4)
# M2, sent at 19:15:50, 590 s after T1's stop, while A1 runs. T3's follow-up
# setting is on queue, so M2 gets a queued run of its own, which T3 hands to
# the agent when A1 ends. The reader sees the row at 19:15:53, before any
# prompt, so the prompt will name the record.
r2 = relay("19:15:53", "19:15:50", 105)
edit("19:19:30", "19:19:29", "test_errors.py", 8, 0)
output("19:25:40", "A1", 60, final=True)
stop("19:25:40", "A1")

# T2, M2's run: 591 s after M2's stamp, and 1,181 s after T1's stop.
p2 = prompt("19:25:41", "T2", 105)
output("19:26:30", "T2", 60)
edit("19:29:00", "19:28:58", "gpx.py", 4, 2)
output("19:34:00", "T2", 105, final=True)
stop("19:34:00", "T2")

# M3, sent at 19:37:00, 180 s after T2's stop, with the agent idle.
p3 = prompt("19:37:01", "M3's turn", 90)
r3 = relay("19:37:03", "19:37:00", 90)
output("19:38:30", "M3's turn", 45)
output("19:40:30", "M3's turn", 30, final=True)
stop("19:40:30", "M3's turn")

events = fx.build()
R, E = {}, {}
for name, r, p in (("M1", r1, p1), ("M2", r2, p2), ("M3", r3, p3)):
    R[name] = fx.event_at("relay.message", r[0], sent_at=r[4]["sent_at"])
    E[name] = fx.event_at("prompt.submit", p[0], turn_key=p[4]["turn_key"])
    # The agent writes the pairing on whichever record arrives second.
    if R[name]["seq"] > E[name]["seq"]:
        R[name]["data"]["prompt"] = E[name]["event_id"]
    else:
        del R[name]["data"]["prompt"]
        E[name]["data"]["relay"] = R[name]["event_id"]
E["A1"] = fx.event_at("prompt.submit", pa[0], turn_key=TURN["A1"])

# The records' shapes: a relay record's data in the order the reader writes
# them, with prompt only when the prompt came first, and relay after
# low_effort on the one prompt that came after its record.
RELAY_KEYS = ["app", "client", "sent_at", "thread_key", "declared", "len_chars", "low_effort"]
PROMPT_KEYS = ["turn_key", "len_chars", "pasted_chars", "typed_len_est", "origin_declared", "matches_pending_cron",
               "answers_question", "low_effort"]
for name in ("M1", "M3"):
    tl.check(list(R[name]["data"]) == RELAY_KEYS + ["prompt"] and list(E[name]["data"]) == PROMPT_KEYS,
             f"{name}'s prompt came first, so its record names it")
tl.check(list(R["M2"]["data"]) == RELAY_KEYS and list(E["M2"]["data"]) == PROMPT_KEYS + ["relay"],
         "M2's record came first, so its prompt names the record, after low_effort")
tl.check(sum(1 for e in events if "relay" in e["data"]) == 1, "only M2's prompt carries relay")
tl.check(not [e for e in events if e["kind"] == "relay.message" and e["data"].get("prompt") == ""],
         "no relay record carries an empty prompt")
for name, rec in R.items():
    tl.check(rec["source"] == "log" and rec["via"] == "app" and rec["data"]["client"] == "web",
             f"{name}'s record comes from T3's store, typed in the web client")
    tl.check(rec["data"]["len_chars"] % 3 == 0, f"{name}'s length is divisible by 3")
tl.check(sum(1 for e in events if e["kind"] == "relay.message") == 3, "the agent's message has no record")

# The times the case turns on.
S1, A1_STOP, S2 = at("19:06:00"), at("19:25:40"), at("19:34:00")
SENT = {n: R[n]["data"]["sent_at"] for n in R}
PAIRED = {n: max(R[n]["t"], E[n]["t"]) for n in R}


def final_at(t):
    """When the minute holding t becomes final: PENDING_WINDOW_S after it
    ends (DESIGN 6.2 D1)."""
    return t - t % tl.MINUTE_MS + tl.MINUTE_MS + rl.PENDING_WINDOW_S * 1000


tl.check(E["A1"]["t"] - S1 == 240_000, "the agent's message comes 240 s after T1's stop")
tl.check(SENT["M2"] - S1 == 590_000 <= rl.READ_FOLLOW_S * 1000, "M2 is sent 590 s after T1's stop, inside 600 s")
tl.check(E["M2"]["t"] - SENT["M2"] == 591_000 <= rl.RELAY_MATCH_S * 1000,
         "M2's prompt comes 591 s after its stamp, inside RELAY_MATCH_S")
tl.check(PAIRED["M2"] - S1 == 1_181_000 > rl.VOUCH_PAIR_S * 1000, "M2 pairs 1,181 s after T1's stop, past 840 s")
tl.check(S1 + rl.VOUCH_PAIR_S * 1000 == at("19:20:00"), "the deadline to vouch for T1's stop is 19:20:00")
tl.check(final_at(S1) == at("19:22:00") and final_at(S1 + tl.MINUTE_MS) == at("19:23:00")
         and PAIRED["M2"] - final_at(S1) == 221_000,
         "T1's stop minute is final at 19:22:00, and the next at 19:23:00, before M2 pairs")
tl.check(SENT["M2"] + rl.RELAY_MATCH_S * 1000 == at("19:25:50"), "M2 could have paired until 19:25:50")
LEAD2 = rl.typing_ms(R["M2"]["data"]["len_chars"])
tl.check(LEAD2 == 70_000 and final_at(SENT["M2"] - LEAD2) == at("19:30:00") > PAIRED["M2"]
         and final_at(SENT["M2"] - 1000) == at("19:31:00"),
         "M2's lead minutes are pending until 19:30:00 and 19:31:00, after M2 pairs")
tl.check(SENT["M3"] - S2 == 180_000 and PAIRED["M3"] - S2 == 183_000, "M3 is sent 180 s and pairs 183 s after T2's stop")
tl.check(SENT["M3"] - A1_STOP == 680_000 > rl.READ_FOLLOW_S * 1000, "M3 comes 680 s after A1's stop, past 600 s")
tl.check(SENT["M2"] < A1_STOP, "M2 was sent before A1's stop")
tl.check(SENT["M2"] - SENT["M1"] == 860_000 and SENT["M3"] - SENT["M2"] == 1_270_000,
         "no earlier message caps a lead")
tl.check(final_at(S2) == at("19:50:00"), "T2's stop minute is final at 19:50:00")

T0, END = at("19:00:00"), at("19:42:00")
ER = rl.ERRATUM
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rules 4 and 9, 2.3, 2.4, 5.2", evidence="C",
                  witness="witnessed", projects={P: "trail-log"}, start="19:00:00", end="19:42:00")
LEAD = "2.3: a relayed message's lead is its typing time, ending at sent_at"
READ = ("2.4: a relayed session reads after a stop, until the thread's next relayed message, sent within 600 s "
        "and paired within 840 s of the stop")
LATE = "2.4: a message that pairs more than PENDING_WINDOW_S - 60 s after a stop vouches for no reading of it"
RUN = "2.4: no relayed session reads while its agent runs"
AGENT = "2.2 rule 4: no relay record, so the agent's message is headless"
IDLE = "5.1: no credit and no agent running"


def row(minute, credit=None, unc=None, agent=0, sm=None, note=None, bucket=True, end=None):
    exp.row(minute, end, credit=credit, uncredited=unc, agent=agent, sm=sm,
            derived=DERIVED if bucket else None, note=note, bucket=bucket)


row("19:00", {P: {"P": 60}}, {}, sm=LEAD,
    note="M1's 135 characters type in 135 / 1.5 = 90 s, from 19:00:00 to its stamp at 19:01:30. It is the "
         "thread's first message, so no earlier one caps the lead.")
row("19:01", {P: {"P": 30}}, {"X.no_anchor": 1, "X.agent_only": 29}, 29, sm=LEAD,
    note="T3 hands M1 to the agent at 19:01:31, so 19:01:30 is in neither the lead nor a turn. T1 runs from "
         "19:01:31.")
row("19:02", end="19:06", credit={}, unc={"X.agent_only": 240}, agent=240, sm=RUN)
row("19:06", end="19:08", bucket=False, sm=LATE,
    note="T1 stops at 19:06:00 with 20 + 20 + 60 + 20 = 120 s in its bucket, the cap. M2, the thread's next "
         "relayed message, is sent 590 s after the stop, but its prompt comes at 19:25:41, so it pairs 1,181 s "
         "after the stop, past 840 s, and vouches for no reading of it. Without that deadline the session would "
         "read 19:06:00 to 19:08:00 here, READ 60 in each minute, though both minutes are final, at 19:22:00 and "
         "19:23:00, before M2 pairs. No credit and no agent running.")
row("19:08", end="19:10", bucket=False, sm=IDLE,
    note="Even without the deadline the bucket would be empty from 19:08:00, and the reading would end at "
         "19:10:00, when A1 starts.")
row("19:10", {}, {"X.automated": 60}, 60, sm=AGENT,
    note="An agent in the docs thread sends this thread a message through T3's MCP tools. T3 stores it with "
         "createdBy agent, so the reader writes no record, nothing witnesses its prompt at 19:10:00, and rule 4 "
         "makes it headless: A1 runs, automated, from 19:10:00.")
row("19:11", end="19:14", credit={}, unc={"X.automated": 180}, agent=180, sm="2.2 rule 4")
row("19:14", {P: {"P": 20}}, {"X.automated": 40}, 60, sm=LEAD,
    note="M2's 105 characters type in 105 / 1.5 = 70 s, from 19:14:40 to its stamp at 19:15:50, under A1's run. "
         "M1 was sent 860 s before M2, so the cap at the seconds since the thread's previous message doesn't "
         "bind.")
row("19:15", {P: {"P": 50}}, {"X.automated": 10}, 60, sm=LEAD,
    note="M2 is sent at 19:15:50, and T3 queues it behind A1. The reader records it at 19:15:53, before any "
         "prompt, so the record names none. The lead's minutes wait for the pairing, and they stay pending "
         "until 19:30:00 and 19:31:00.")
row("19:16", end="19:25", credit={}, unc={"X.automated": 540}, agent=540, sm="2.2 rule 4")
row("19:25", {}, {"X.no_anchor": 1, "X.agent_only": 19, "X.automated": 40}, 59,
    sm="2.2 rule 9: M2's prompt is relayed",
    note="A1 stops at 19:25:40, and T3 hands M2's run to the agent at 19:25:41, 591 s after M2's stamp, inside "
         "RELAY_MATCH_S. The prompt names M2's record, so M2 pairs and its lead stands. 19:25:40 is between the "
         "turns. T2 runs from 19:25:41, and its prompt resets the bucket to its 20 s base.")
row("19:26", end="19:34", credit={}, unc={"X.agent_only": 480}, agent=480, sm=RUN)
row("19:34", {P: {"READ": 60}}, {}, sm=READ,
    note="T2 stops at 19:34:00 with 20 + 20 + 45 + 35 = 120 s in its bucket. M3 is sent at 19:37:00, 180 s after "
         "the stop, and pairs at 19:37:03, 183 s after it, so the session reads from the stop.")
row("19:35", {P: {"READ": 60}}, {}, sm=READ,
    note="The 120 s end at 19:36:00. T2 filled the cap exactly, so no second is X.bucket_cap.")
row("19:36", {P: {"P": 60}}, {}, sm=LEAD,
    note="M3's 90 characters type in 90 / 1.5 = 60 s, from 19:36:00 to its stamp at 19:37:00. M2 was sent "
         "1,270 s earlier.")
row("19:37", {}, {"X.no_anchor": 1, "X.agent_only": 59}, 59, sm="2.2 rule 9: M3's prompt is relayed",
    note="T3 hands M3 over at 19:37:01, and the reader records it at 19:37:03, naming the prompt. M3's turn runs "
         "from 19:37:01.")
row("19:38", end="19:40", credit={}, unc={"X.agent_only": 120}, agent=120, sm=RUN)
row("19:40", {}, {"X.no_anchor": 30, "X.agent_only": 30}, 30, sm="2.4",
    note="M3's turn stops at 19:40:30, and no relayed message follows it, so nothing reads its 45 s.")
row("19:41", bucket=False, sm=IDLE)

rp = rl.Replay(events, T0, END)
rl.check_rows(exp, rp, {S: P})
tl.check(rp.origin == {E["M1"]["event_id"]: "relayed", E["A1"]["event_id"]: "headless",
                       E["M2"]["event_id"]: "relayed", E["M3"]["event_id"]: "relayed"},
         "M1's, M2's and M3's prompts are relayed, and the agent's is headless")
# Replay sets late only when a pairing comes too late to vouch.
tl.check(getattr(rp, "late", []) == [(S, S1, SENT["M2"])], "M2's is the one pairing too late to vouch for a stop")
tl.check(rp.spans == {S: [(S2, SENT["M3"])]}, "the session reads after T2's stop alone, until M3 is sent")
tl.check(not rp.capped, "no second is X.bucket_cap")
tl.check({a["id"]: a["lead_ms"] for a in rp.anchors} == {R["M1"]["event_id"]: 90_000, R["M2"]["event_id"]: 70_000,
                                                        R["M3"]["event_id"]: 60_000},
         "the leads are 90 s, 70 s and 60 s")

# The counterfactual: the same records with no deadline on the pairing that
# vouches for a stop's reading. M2 then vouches for T1's stop, and the
# session reads T1's 120 s from 19:06:00, before A1 starts at 19:10:00. The
# deadline is relayed.py's VOUCH_PAIR_S, which Replay reads when it runs.
vouch = rl.VOUCH_PAIR_S
rl.VOUCH_PAIR_S = 10 ** 9
try:
    cf = rl.Replay(events, T0, END)
finally:
    rl.VOUCH_PAIR_S = vouch
tl.check(cf.spans == {S: [(S1, E["A1"]["t"]), (S2, SENT["M3"])]},
         "without the deadline the session reads after T1's stop too, until A1 starts")
tl.check(all(cf.credited.get(t) == c for t, c in rp.credited.items()), "the deadline takes credit away, never adds")
gained = {t: c for t, c in cf.credited.items() if t not in rp.credited}
tl.check(gained == {t: (S, "READ") for t in range(S1, S1 + 120_000, 1000)},
         "without the deadline the case gains READ from 19:06:00 to 19:08:00 and nothing else")
tl.check(not cf.capped and len(cf.credited) == 460, "without the deadline the case credits 460 s")
tl.check(all(final_at(t) < PAIRED["M2"] for t in gained), "every second the deadline takes is final before M2 pairs")

totals = exp.totals()
tl.check(totals["credited_s"] == 340 == len(rp.credited), f"credited {totals['credited_s']}")
tl.check(totals["credited_by_reason"] == {"P": 220, "READ": 120}, f"by reason {totals['credited_by_reason']}")
tl.check(totals["uncredited"] == {"X.no_anchor": 33, "X.agent_only": 977, "X.automated": 870},
         f"uncredited {totals['uncredited']}")
tl.check(totals["agent_running_s"] == 1917, f"agent {totals['agent_running_s']}")
expected = exp.doc(
    extra_totals={
        "level_c_s": 340,
        "via_relay_s": 340,
        "relayed_prompts": 3,
        "headless_prompts": 1,
    },
    stated=[],
    approx={},
    errata=[ER],
    notes=[
        "No SM section describes this case. Every row comes from SM 2.2 rules 4 and 9, 2.3, 2.4 and 5.2 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them, with 2.4's deadline for the pairing that vouches "
        "for a stop's reading: PENDING_WINDOW_S - 60 s = 840 s after the stop. Every credited second is level C "
        "with via_relay, and witnessed: the server uploads live.",
        "The server has no desktop, so pairtime samples no presence and records no focus: every slot's presence "
        "is unknown, and uncredited seconds take SM 5.2's codes for such a slot. No second is X.bucket_cap: the "
        "one reading drains a bucket T2 filled to exactly the 120 s cap.",
        "The agent's message comes from a T3 thread on a provider pairtime has no hooks for, so this device "
        "records nothing of the agent that sent it. T3 stores the message with createdBy agent, so the T3 reader "
        "writes no record for it.",
        "Without the 840 s deadline, M2, sent 590 s after T1's stop, would vouch for T1's reading: READ 60 in "
        "19:06 and 60 in 19:07, and 460 s in all. Both minutes are final, at 19:22:00 and 19:23:00, before M2 "
        "pairs at 19:25:41.",
        "relayed_prompts counts the prompts a relay record witnessed; headless_prompts the agent's message, which "
        "rule 4 took.",
    ],
)

anchors = []
for name in ("M1", "M2", "M3"):
    rec, pe = R[name], E[name]
    lead = rl.typing_ms(rec["data"]["len_chars"]) // 1000
    sm = f"2.3: typing time, {rec['data']['len_chars']} / 1.5 = {lead} s, ending at sent_at"
    if name == "M2":
        sm += ("; it pairs 591 s after sent_at, inside RELAY_MATCH_S, but 1,181 s after T1's stop, too late to "
               "vouch for that stop's reading (2.4)")
    anchors.append(rl.anchor_entry(fx, rec, pe, kind="substantive_prompt", prompt_class="substantive", lead_s=lead,
                                   sm=sm))
anchors.sort(key=lambda a: a["t"])
turns = []
for name, origin in (("T1", "relayed"), ("A1", "headless"), ("T2", "relayed"), ("M3's turn", "relayed")):
    sub = [e for e in events if e["kind"] == "prompt.submit" and e["data"]["turn_key"] == TURN[name]][0]
    stp = [e for e in events if e["kind"] == "agent.stop" and e["data"]["turn_key"] == TURN[name]][0]
    turns.append(tl.turn(fx, sub["t"], stp["t"], session=S, turn_key=TURN[name], origin=origin,
                         prompt_class="substantive" if origin == "relayed" else None,
                         sm="2.2 rule 9" if origin == "relayed"
                         else "2.2 rule 4: T3 stores the agent's message with createdBy agent, so no record witnesses it"))

prompt_entries = [{"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]), "text": None,
                   "len_chars": e["data"]["len_chars"], "source": "invented for this case; only its length matters"}
                  for e in events if e["kind"] == "prompt.submit"]

readme = os.path.join(HERE, "README.md")
rl.check_readme(readme, expected, exp.projects)
# check_readme compares the codes a cell names. This case also states every
# uncredited second of every bucket row, and none in a row without one.
by_local = {r["local"]: r for r in expected["rows"]}
for cells in rl.readme_rows(readme, "| Minute | Credited | Reasons |"):
    r = by_local[cells[0]]
    want = r["uncredited"] if r["bucket"] else {}
    got = rl.readme_codes(cells[3])
    tl.check(got == want, f"README {cells[0]} states {got}, expected.json has {want}")

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
print(f"{NAME}: {len(events)} events, credited {totals['credited_s']} s {totals['credited_by_reason']}, "
      f"{len(cf.credited)} s without the 840 s deadline; wrote {tl.out_dir(HERE)}")
