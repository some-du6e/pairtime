# SPDX-License-Identifier: AGPL-3.0-or-later
"""T3 Code messages that T3 marks createdBy user though nobody typed them: a
scheduled task, a webhook task, an automatic /compact, a guest's message and
a message from another environment's thread. None of them earns anything,
and the person's own two messages among them still do: SM 2.2 rules 1, 3, 4
and 9, 2.3's typing lead and 2.4's reading after a stop, at level C. See
README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import relayed as rl  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/t3-unattended"
fx = tl.Fixture(
    NAME,
    title="T3 Code messages marked createdBy user that nobody typed, and a person's two messages among them",
    date="2026-11-28",
    timezone_name="America/New_York",
    utc_offset_min=-300,
    os_name="linux",
    keys=False,
    device=tl.key("d", "relayed t3-unattended server"),
    boot_id=tl.key("b", "relayed t3-unattended server boot"),
    boot="07:58:21.000",
    seq0=26530,
    last_input="07:58:21.000",
)
at = fx.at
P = tl.key("p", "relayed t3-unattended bus-times")
THREADS = ("checks", "deploys", "main", "review")
S = {n: tl.key("s", f"relayed t3-unattended {n} session") for n in THREADS}
THREAD = {n: tl.key("r", f"relayed t3-unattended {n} thread") for n in THREADS}
ENV = {n: dict(harness="claude_code", version="2.1.334", session=S[n], project=P) for n in THREADS}
# The task fires on every tenth minute, 16:00 to 17:50: C1 to C12.
TASK_AT = [f"{16 + m // 60}:{m % 60:02d}" for m in range(0, 120, 10)]
CRONS = [f"C{i}" for i in range(1, 13)]
TURN = {n: tl.key("t", f"relayed t3-unattended {n}") for n in CRONS + ["W1", "K", "M1", "M2", "G", "L", "W2"]}
FILE = {n: tl.key("f", f"relayed t3-unattended {n}")
        for n in ("service_day.js", "service_day.test.js", "README.md", "package.json")}

# The texts, invented for this case. Only their lengths reach the records:
# T3's own text gives the relay record's len_chars and the hook's text the
# prompt's, and here the two are the same. G carries the prefix T3 puts on
# a guest's message that the owner sends on.
TEXT = {
    "task": "Check the transit agency's service alerts for every route listed in routes.json and add each new "
            "alert to ALERTS.md as one line. Change no other file.",
    "W1": "Deploy hook: preview build 412 of branch night-routes finished with status success. Open the preview, "
          "load the arrivals page for stop 1187, and check whether the next three buses show plausible minutes. "
          "Report only; fix nothing.",
    "K": "/compact",
    "M1": "Night buses after midnight still show on the next day. Group them under the service day they belong "
          "to instead, then add a test for it.",
    "M2": "That looks right. Add two sentences to README.md about service days and why we group them.",
    "G": "Message from guest Ines, sent by the owner: could the arrivals page show 24-hour times when the browser "
         "asks for them? My phone does.",
    "L": "From the docs thread on the laptop: I rewrote the install section of README.md. Check that every command "
         "in it still matches a script in package.json, and fix any that don't.",
    "W2": "Deploy hook: preview build 413 of branch night-routes finished with status success. Open the preview, "
          "load the arrivals page for stop 2240, and check whether the buses after midnight show sensible minutes. "
          "Report only; fix nothing.",
}
LEN = {k: len(v) for k, v in TEXT.items()}
tl.check(LEN == {"task": 150, "W1": 228, "K": 8, "M1": 135, "M2": 90, "G": 133, "L": 174, "W2": 231},
         f"the texts have the case's lengths: {LEN}")


def hook(thread, t, kind, data):
    return fx.emit(at(t), kind, data, source="hook", via="hook", **ENV[thread])


def start(thread, t):
    # T3 Code's server, `t3 serve`, starts every session here through the
    # Agent SDK: no TTY, and the walk names T3's server as the nearest
    # non-shell ancestor, agent_app (DESIGN 5.1's app-table entry).
    hook(thread, t, "session.start", {"start_kind": "startup", "entrypoint": "unknown", "parent_class": "agent_app",
                                      "has_tty": False, "permission_mode": "bypassPermissions"})


def prompt(thread, t, turn, length, slash=None, guest=False):
    data = {"turn_key": TURN[turn], "len_chars": length, "pasted_chars": 0, "typed_len_est": length}
    if slash:
        data["slash_command"] = slash
    data.update({"origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
                 "low_effort": False})
    if guest:
        data["guest"] = True
    return hook(thread, t, "prompt.submit", data)


def relay(thread, t, sent, length, declared):
    """The T3 reader's record of a user-role message: declared none for one
    that passed SM 2.2's witness test (T3's own test for a message a person
    sent, and none of the other marks), cron for one with a scheduledTaskId,
    cross_session for one with linkedSender. Its prompt is filled in, or
    removed, once the events are built."""
    return fx.emit(at(t), "relay.message", {"app": "t3_code", "client": "web", "sent_at": at(sent), "thread_key": THREAD[thread],
                                            "declared": declared, "len_chars": length, "low_effort": False,
                                            "prompt": ""},
                   source="log", via="app", **ENV[thread])


def output(thread, t, turn, words, final=False):
    hook(thread, t, "agent.output", {"turn_key": TURN[turn], "words": words, "code_lines": 0, "is_final": final,
                                     "asks_question": False, "offers_options": False})


def tool(thread, t, t_start, cls, files=(), added=0, removed=0, visible=0):
    data = {"tool_class": cls}
    if files:
        data["files"] = [FILE[f] for f in files]
    data.update({"lines_added": added, "lines_removed": removed, "visible_output_lines": visible,
                 "t_start": at(t_start), "t_end": at(t)})
    hook(thread, t, "agent.tool", data)


def stop(thread, t, turn):
    hook(thread, t, "agent.stop", {"turn_key": TURN[turn], "stop_reason": "end_turn", "stop_hook_active": False})


pairs = []  # (relay record, prompt): every pairing the agent writes


def task(i):
    """Firing i of the task Mateo made in T3's UI: createdBy user,
    creationSource web and a scheduledTaskId. T3 stamps it on the minute and
    hands it to the agent a second later; the reader, set off by the
    prompt, writes its record two seconds after that."""
    hm, name = TASK_AT[i - 1], CRONS[i - 1]
    if i == 1:
        start("checks", f"{hm}:00")
    p = prompt("checks", f"{hm}:01", name, LEN["task"])
    r = relay("checks", f"{hm}:03", f"{hm}:00", LEN["task"], "cron")
    pairs.append((r, p))
    tool("checks", f"{hm}:30", f"{hm}:08", "web", visible=4)
    output("checks", f"{hm}:51", name, 30, final=True)
    stop("checks", f"{hm}:51", name)


# Mateo's own thread. T3's server started its session after lunch; its
# earlier turns ended before 14:00 and aren't in the case.
start("main", "13:02:14")

task(1)
task(2)
task(3)

# W1: his CI posts a finished preview deploy to the webhook task at
# 16:27:00. T3 renders the request into a message with a scheduledTaskId
# and starts the deploys thread's session for its run.
start("deploys", "16:27:00")
w1 = prompt("deploys", "16:27:01", "W1", LEN["W1"])
pairs.append((relay("deploys", "16:27:03", "16:27:00", LEN["W1"], "cron"), w1))
tool("deploys", "16:27:40", "16:27:12", "web", visible=4)
output("deploys", "16:28:31", "W1", 40, final=True)
stop("deploys", "16:28:31", "W1")

task(4)
task(5)

# 16:44:00. Mateo sends M1 from T3's web client. Compaction is due, so the
# composer first sends /compact, marked idleCompaction onReturn, and T3 runs
# M1 only after the compaction's run. The /compact's prompt sets the reader
# reading at once: it turns the /compact's row away for its idleCompaction
# and writes M1's record at 16:44:03, before M1's prompt. Whether Claude Code
# fires UserPromptSubmit for the /compact is unverified; the case assumes it
# does, the stricter reading.
k = prompt("main", "16:44:01", "K", LEN["K"], slash="/compact")
r1 = relay("main", "16:44:03", "16:44:00", LEN["M1"], "none")
stop("main", "16:44:40", "K")
p1 = prompt("main", "16:44:41", "M1", LEN["M1"])
pairs.append((r1, p1))
output("main", "16:45:20", "M1", 45)
tool("main", "16:46:30", "16:46:28", "edit", files=["service_day.js"], added=6, removed=2)
tool("main", "16:47:40", "16:47:39", "edit", files=["service_day.test.js"], added=6)
output("main", "16:48:15", "M1", 75, final=True)
stop("main", "16:48:15", "M1")

# C6 runs in the checks session while the main session reads M1's output.
task(6)

# M2 at 16:55:00, 405 s after M1's turn stopped. The agent is idle, so T3
# hands it over at once.
p2 = prompt("main", "16:55:01", "M2", LEN["M2"])
pairs.append((relay("main", "16:55:03", "16:55:00", LEN["M2"], "none"), p2))
output("main", "16:55:40", "M2", 30)
tool("main", "16:56:50", "16:56:49", "edit", files=["README.md"], added=4)
output("main", "16:57:30", "M2", 60, final=True)
stop("main", "16:57:30", "M2")

task(7)
task(8)

# G: Ines, a friend Mateo let into the review thread as a guest, writes a
# message, and he sends it on. T3 stores it with context.guest, and the
# reader's query, whose WHERE clause holds the guest test, never selects
# it: no record. The query does return the run it started, so the agent
# marks the prompt guest and keeps no text for it, and rule 4 takes it.
start("review", "17:14:00")
g = prompt("review", "17:14:01", "G", LEN["G"], guest=True)
output("review", "17:14:56", "G", 50, final=True)
stop("review", "17:14:56", "G")

task(9)

# L: an agent in a thread of the T3 environment on Mateo's laptop, linked to
# the server's, sends the review thread a message. T3 marks it linkedSender,
# and the reader records it as declared cross_session.
lp = prompt("review", "17:25:01", "L", LEN["L"])
pairs.append((relay("review", "17:25:03", "17:25:00", LEN["L"], "cross_session"), lp))
tool("review", "17:25:20", "17:25:19", "read", files=["README.md", "package.json"])
tool("review", "17:26:00", "17:25:59", "edit", files=["README.md"], added=1, removed=1)
output("review", "17:26:31", "L", 40, final=True)
stop("review", "17:26:31", "L")

# W2 at 17:30:20, while C10 runs in the checks session.
task(10)
w2 = prompt("deploys", "17:30:21", "W2", LEN["W2"])
pairs.append((relay("deploys", "17:30:23", "17:30:20", LEN["W2"], "cron"), w2))
tool("deploys", "17:31:00", "17:30:40", "web", visible=4)
output("deploys", "17:31:51", "W2", 35, final=True)
stop("deploys", "17:31:51", "W2")

task(11)
task(12)

events = fx.build()
# The agent writes each pairing on whichever record arrived second.
for r, p in pairs:
    re = fx.event_at("relay.message", r[0], sent_at=r[4]["sent_at"], thread_key=r[4]["thread_key"])
    pe = fx.event_at("prompt.submit", p[0], turn_key=p[4]["turn_key"])
    if re["seq"] > pe["seq"]:
        re["data"]["prompt"] = pe["event_id"]
    else:
        del re["data"]["prompt"]
        pe["data"]["relay"] = re["event_id"]
for e in events:
    tl.check(e["data"].get("prompt", "x") != "", "no relay record carries an empty prompt")
E = {name: fx.event_at("prompt.submit", raw[0], turn_key=raw[4]["turn_key"])
     for name, raw in (("K", k), ("M1", p1), ("M2", p2), ("G", g), ("L", lp))}
R1 = fx.event_at("relay.message", r1[0], sent_at=r1[4]["sent_at"])
tl.check(E["M1"]["data"].get("relay") == R1["event_id"] and "prompt" not in R1["data"],
         "M1's record came before its prompt, so the prompt names the record")
tl.check(sum(1 for e in events if "relay" in e["data"]) == 1, "only M1's prompt carries relay")
main = [e for e in events if e["kind"] == "prompt.submit" and e["session_key"] == S["main"]
        and e["t"] >= R1["data"]["sent_at"]]
tl.check(main[0] is E["K"] and E["K"]["t"] < R1["t"] < E["M1"]["t"],
         "the /compact's prompt is the main session's first after M1's stamp, and M1's record comes between them")

T0, END = at("16:00:00"), at("18:00:00")
ER = rl.ERRATUM
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rules 1, 3, 4 and 9, 2.3, 2.4, 5.2", evidence="C",
                  witness="witnessed", projects={P: "bus-times"}, start="16:00:00", end="18:00:00")
TASK = "2.2 rule 1: the relay record declares cron, so the task's prompt is scheduled"
HOOKED = "2.2 rule 1: a webhook task's message has a scheduledTaskId too, so its record declares cron"
LEAD = "2.3: a relayed message's lead is its typing time, ending at sent_at"
READ = "2.4: a relayed session reads after a stop, until its next relayed message within 600 s, 120 s at most"
IDLE = "5.1: no credit and no agent running"


def row(minute, credit=None, unc=None, agent=0, sm=None, note=None, bucket=True, end=None):
    exp.row(minute, end, credit=credit, uncredited=unc, agent=agent, sm=sm,
            derived=DERIVED if bucket else None, note=note, bucket=bucket)


def task_row(i, note=None):
    hm = TASK_AT[i - 1]
    row(hm, {}, {"X.no_anchor": 10, "X.automated": 50}, 50, sm=TASK,
        note=note or f"{CRONS[i - 1]}'s prompt reaches the agent at {hm}:01 and its turn runs until {hm}:51.")


def idle(start, end=None):
    row(start, end=end, bucket=False, sm=IDLE)


task_row(1, "The task fires at 16:00:00, and T3 starts the checks thread's session for it. C1's prompt reaches the "
            "agent at 16:00:01 and its relay record, declared cron, follows at 16:00:03, so rule 1 makes it "
            "scheduled. C1 runs until 16:00:51.")
idle("16:01", "16:10")
task_row(2)
idle("16:11", "16:20")
task_row(3)
idle("16:21", "16:27")
row("16:27", {}, {"X.no_anchor": 1, "X.automated": 59}, 59, sm=HOOKED,
    note="The webhook fires at 16:27:00 and T3 starts the deploys thread's session. W1's prompt, scheduled by "
         "its cron record, runs from 16:27:01.")
row("16:28", {}, {"X.no_anchor": 29, "X.automated": 31}, 31, sm=HOOKED, note="W1 runs until 16:28:31.")
idle("16:29")
task_row(4)
idle("16:31", "16:40")
task_row(5)
idle("16:41")
row("16:42", {P: {"P": 30}}, {"X.no_anchor": 30}, sm=LEAD,
    note="M1's 135 characters type in 135 / 1.5 = 90 s, from 16:42:30 to its stamp at 16:44:00. It is the main "
         "thread's first message in the case, so nothing caps the lead.")
row("16:43", {P: {"P": 60}}, {}, sm=LEAD)
row("16:44", {}, {"X.no_anchor": 2, "X.agent_only": 19, "X.automated": 39}, 58,
    sm="2.2 rule 4: the /compact has no relay record; 2.2 rule 9: M1's prompt is relayed",
    note="16:44:00 is in neither the lead nor a turn. The /compact's prompt at 16:44:01 has no witness, so rule 4 "
         "makes it headless, and its turn runs until 16:44:40. 16:44:40 is between the turns. M1's prompt comes at "
         "16:44:41, 41 s after M1's stamp, and names M1's record; M1's turn runs from then.")
row("16:45", end="16:48", credit={}, unc={"X.agent_only": 180}, agent=180,
    sm="2.4: no relayed session reads while its agent runs")
row("16:48", {P: {"READ": 45}}, {"X.agent_only": 15}, 15, sm=READ,
    note="M1's turn stops at 16:48:15. M2 follows at 16:55:00, 405 s later, so the session reads from the stop. "
         "M1's turn filled 20 + 15 + 60 + 45 + 25 = 165 s, which the 120 s cap holds to 120.")
row("16:49", {P: {"READ": 60}}, {}, sm=READ)
row("16:50", {P: {"READ": 15}}, {"X.bucket_cap": 45}, 50, sm=READ,
    note="The 120 s end at 16:50:15: 45 + 60 + 15. BUCKET_MAX_S's 600 s would have read the other 45 s of the 165, "
         "so they are X.bucket_cap (5.2), which comes before X.automated though C6 runs in the checks session "
         "from 16:50:01 to 16:50:51. C6 is no turn of the main session, so the reading goes on through it.")
row("16:51", end="16:54", bucket=False, sm=IDLE,
    note="The main session's reading span runs until M2 is sent, but nothing is left to read and no agent runs.")
row("16:54", {P: {"P": 60}}, {}, sm=LEAD,
    note="M2's 90 characters type in 90 / 1.5 = 60 s, from 16:54:00 to its stamp at 16:55:00. M1 came 660 s "
         "earlier, so the cap at the seconds since the thread's previous message doesn't bind.")
row("16:55", {}, {"X.no_anchor": 1, "X.agent_only": 59}, 59, sm="2.2 rule 9: M2's prompt is relayed",
    note="T3 hands M2 over at 16:55:01, and M2's turn runs from then. The prompt resets the bucket to its 20 s "
         "base.")
row("16:56", {}, {"X.agent_only": 60}, 60, sm="2.4: no relayed session reads while its agent runs")
row("16:57", {}, {"X.no_anchor": 30, "X.agent_only": 30}, 30, sm="2.4",
    note="M2's turn stops at 16:57:30, and no relayed message follows it in the window, so nothing reads its 80 s.")
idle("16:58", "17:00")
task_row(7)
idle("17:01", "17:10")
task_row(8)
idle("17:11", "17:14")
row("17:14", {}, {"X.no_anchor": 5, "X.automated": 55}, 55, sm="2.2 rule 4: the guest's message has no relay record",
    note="The guest's message reaches the agent at 17:14:01. The reader never selected its row, so nothing "
         "witnesses the prompt, and rule 4 makes it headless. Its turn runs until 17:14:56.")
idle("17:15", "17:20")
task_row(9)
idle("17:21", "17:25")
row("17:25", {}, {"X.no_anchor": 1, "X.automated": 59}, 59,
    sm="2.2 rule 3: the relay record declares cross_session, so the prompt is a cross-session subagent",
    note="The laptop thread's message reaches the agent at 17:25:01, and its record follows at 17:25:03.")
row("17:26", {}, {"X.no_anchor": 29, "X.automated": 31}, 31, sm="2.2 rule 3", note="Its turn runs until 17:26:31.")
idle("17:27", "17:30")
row("17:30", {}, {"X.no_anchor": 1, "X.automated": 59}, 59, sm=TASK + "; " + HOOKED,
    note="C10 runs from 17:30:01 to 17:30:51 and W2 from 17:30:21, so an agent runs from 17:30:01 to the end of "
         "the minute. A second two turns share counts once.")
row("17:31", {}, {"X.no_anchor": 9, "X.automated": 51}, 51, sm=HOOKED, note="W2 runs until 17:31:51.")
idle("17:32", "17:40")
task_row(11)
idle("17:41", "17:50")
task_row(12)
idle("17:51", "18:00")

rp = rl.Replay(events, T0, END)
rl.check_rows(exp, rp, {S[n]: P for n in THREADS})
want = {E["K"]["event_id"]: "headless", E["M1"]["event_id"]: "relayed", E["M2"]["event_id"]: "relayed",
        E["G"]["event_id"]: "headless", E["L"]["event_id"]: "subagent"}
for e in events:
    if e["kind"] == "prompt.submit" and e["data"]["turn_key"] in [TURN[n] for n in CRONS + ["W1", "W2"]]:
        want[e["event_id"]] = "scheduled"
tl.check(rp.origin == want, "every prompt has the origin the case gives it")
counts = {o: sum(1 for v in rp.origin.values() if v == o) for o in ("relayed", "scheduled", "headless", "subagent")}
tl.check(counts == {"relayed": 2, "scheduled": 14, "headless": 2, "subagent": 1}, f"origins {counts}")

totals = exp.totals()
tl.check(totals["credited_s"] == 270, f"credited {totals['credited_s']}")
tl.check(totals["credited_by_reason"] == {"P": 150, "READ": 120}, f"by reason {totals['credited_by_reason']}")
tl.check(totals["uncredited"] == {"X.no_anchor": 238, "X.agent_only": 363, "X.automated": 884, "X.bucket_cap": 45},
         f"uncredited {totals['uncredited']}")
tl.check(totals["agent_running_s"] == 1297, f"agent {totals['agent_running_s']}")
expected = exp.doc(
    extra_totals={
        "level_c_s": 270,
        "via_relay_s": 270,
        "relayed_prompts": counts["relayed"],
        "scheduled_prompts": counts["scheduled"],
        "headless_prompts": counts["headless"],
        "cross_session_prompts": counts["subagent"],
    },
    stated=[],
    approx={},
    errata=[ER],
    notes=[
        "No SM section describes this case. Every row comes from SM 2.2 rules 1, 3, 4 and 9, 2.3, 2.4 and 5.2 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them. Every credited second is level C with via_relay, "
        "and witnessed: the server uploads live.",
        "The server has no desktop, so pairtime samples no presence and records no focus: every slot's presence "
        "is unknown.",
        "T3 stores every message here with createdBy user. relayed_prompts counts the prompts a relay record of "
        "declared none witnessed; scheduled_prompts the task's twelve firings and the webhook's two, whose records "
        "declare cron; cross_session_prompts the laptop thread's message, whose record declares cross_session; "
        "headless_prompts the /compact and the guest's message, which have no record.",
        "Every slot's presence is unknown, so uncredited seconds take SM 5.2's codes for such a slot: "
        "X.bucket_cap where only the 120 s cap kept the session from reading, X.agent_only or X.automated while a "
        "turn runs, else X.no_anchor.",
        "The /compact's prompt assumes Claude Code fires UserPromptSubmit for the command T3's composer sends. "
        "That is unverified, and the case credits the same either way.",
    ],
)

anchors = []
for name, rec in (("M1", R1), ("M2", fx.event_at("relay.message", at("16:55:03")))):
    lead = rl.typing_ms(rec["data"]["len_chars"]) // 1000
    anchors.append(rl.anchor_entry(fx, rec, E[name], kind="substantive_prompt", prompt_class="substantive",
                                   lead_s=lead,
                                   sm=f"2.3: a relayed message's lead is its typing time, "
                                      f"{rec['data']['len_chars']} / 1.5 = {lead} s, ending at sent_at"))
ORIGIN = {"K": ("headless", "2.2 rule 4: no relay record, the /compact carries idleCompaction"),
          "G": ("headless", "2.2 rule 4: the prompt is marked guest, and no relay record witnesses it"),
          "M1": ("relayed", "2.2 rule 9"), "M2": ("relayed", "2.2 rule 9"),
          "L": ("subagent", "2.2 rule 3: the relay record declares cross_session (linkedSender)")}
turns = []
for e in events:
    if e["kind"] != "prompt.submit":
        continue
    name = [n for n, key in TURN.items() if key == e["data"]["turn_key"]][0]
    origin, sm = ORIGIN.get(name, ("scheduled", "2.2 rule 1: the relay record declares cron (scheduledTaskId)"))
    stp = [x for x in events if x["kind"] == "agent.stop" and x["data"]["turn_key"] == TURN[name]][0]
    turns.append(tl.turn(fx, e["t"], stp["t"], session=e["session_key"], turn_key=TURN[name], origin=origin,
                         prompt_class="substantive" if origin == "relayed" else None, sm=sm))

prompt_entries = []
for e in events:
    if e["kind"] == "prompt.submit":
        compact = e is E["K"]
        prompt_entries.append({"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]),
                               "text": "/compact" if compact else None, "len_chars": e["data"]["len_chars"],
                               "source": "the command T3 Code's web composer sends when compaction is due" if compact
                               else "invented for this case; only its length matters"})

rl.check_readme(os.path.join(HERE, "README.md"), expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
