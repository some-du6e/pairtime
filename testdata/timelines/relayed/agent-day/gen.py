# SPDX-License-Identifier: AGPL-3.0-or-later
"""A day of prompts that agents in T3 Code threads send each other, on the
server that runs them, and one message a person sent whose run reached no
session pairtime sees. SM 2.2 rule 4 makes every prompt headless, and the
one relay record pairs with nothing. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import relayed as rl  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/agent-day"
fx = tl.Fixture(
    NAME,
    title="A day of prompts agents send each other through T3 Code, and a message no session took",
    date="2026-11-23",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="linux",
    keys=False,
    device=tl.key("d", "relayed agent-day server"),
    boot_id=tl.key("b", "relayed agent-day server boot"),
    boot="-1 05:31:44.000",
    seq0=403118,
    last_input="-1 05:31:44.000",
)
at = fx.at
DESK = tl.key("p", "relayed agent-day agent-desk")
TIDE = tl.key("p", "relayed agent-day tidepool")
KELP = tl.key("p", "relayed agent-day kelp-cli")
PROJECTS = {DESK: "agent-desk", TIDE: "tidepool", KELP: "kelp-cli"}
PLANNER = tl.key("s", "relayed agent-day planner")
THREAD = tl.key("r", "relayed agent-day unhooked provider thread")
FILE = {DESK: tl.key("f", "relayed agent-day agent-desk queue.md"),
        TIDE: tl.key("f", "relayed agent-day tidepool tides.py"),
        KELP: tl.key("f", "relayed agent-day kelp-cli main.go")}
TESTS = {TIDE: tl.key("c", "relayed agent-day tidepool pytest"),
         KELP: tl.key("c", "relayed agent-day kelp-cli go test")}
VERSION = "2.1.334"

# One hour's turns, the same every hour: name, session, prompt and stop as
# minutes:seconds past the hour, and the message's length. Each message
# reaches its agent while the turn that sent it still runs, so an agent
# runs without a break from :00:30 to :25:22.
HOUR = [
    ("P.1", "planner", "00:30", "02:00", 420),
    ("A.1", "builder", "01:12", "11:12", 1380),
    ("B.1", "reviewer", "01:42", "05:42", 640),
    ("B.2", "reviewer", "11:08", "15:08", 510),
    ("A.2", "builder", "15:04", "21:04", 780),
    ("B.3", "reviewer", "21:00", "23:00", 330),
    ("A.3", "builder", "22:56", "23:56", 120),
    ("P.2", "planner", "23:52", "25:22", 450),
]
WORDS = {"P.1": 70, "A.1": 120, "B.1": 90, "B.2": 150, "A.2": 80, "B.3": 40, "A.3": 30, "P.2": 50}
# Tool calls the hooks record (Edit, Write and Bash): an edit with its
# start, end and lines added and removed, or a test run with its start and
# end.
TOOLS = {
    "A.1": [("edit", "06:10", "06:12", 48, 12), ("test", "10:12", "10:40")],
    "B.2": [("test", "13:30", "13:58")],
    "A.2": [("edit", "18:02", "18:04", 20, 9), ("test", "20:10", "20:40")],
    "B.3": [("test", "22:00", "22:30")],
    "P.2": [("edit", "24:49", "24:50", 6, 1)],
}


def hook(t, kind, data, session, project):
    return fx.emit(t, kind, data, source="hook", via="hook", harness="claude_code", version=VERSION,
                   session=session, project=project)


def t3_start(t, session, project):
    """T3's server, `t3 serve`, starts the session through the Agent SDK: no
    TTY, and the walk names T3's server as its nearest non-shell ancestor,
    agent_app (DESIGN 5.1's app-table entry)."""
    hook(t, "session.start", {"start_kind": "startup", "entrypoint": "unknown", "parent_class": "agent_app",
                              "has_tty": False, "permission_mode": "bypassPermissions"}, session, project)


def hour(h):
    """Hour h's records. Every prompt carries a message an agent sent
    through T3's MCP tools, which T3 stores with createdBy agent, so the T3
    reader writes no relay record for any of them."""
    work = TIDE if h % 2 == 0 else KELP
    session = {"planner": PLANNER,
               "builder": tl.key("s", f"relayed agent-day {h:02d} builder"),
               "reviewer": tl.key("s", f"relayed agent-day {h:02d} reviewer")}
    project = {"planner": DESK, "builder": work, "reviewer": work}

    def t(mmss):
        return at(f"{h:02d}:{mmss}")

    if h == 0:
        t3_start(t("00:28"), PLANNER, DESK)
    t3_start(t("01:10"), session["builder"], work)
    t3_start(t("01:40"), session["reviewer"], work)
    for name, who, start, end, length in HOUR:
        s, p = session[who], project[who]
        turn = tl.key("t", f"relayed agent-day {h:02d} {name}")
        hook(t(start), "prompt.submit", {"turn_key": turn, "len_chars": length, "pasted_chars": 0,
                                         "typed_len_est": length, "origin_declared": "none",
                                         "matches_pending_cron": False, "answers_question": False,
                                         "low_effort": False}, s, p)
        for tool in TOOLS.get(name, []):
            if tool[0] == "edit":
                _, a, b, added, removed = tool
                data = {"tool_class": "edit", "files": [FILE[p]], "lines_added": added, "lines_removed": removed,
                        "visible_output_lines": 0, "t_start": t(a), "t_end": t(b)}
            else:
                _, a, b = tool
                data = {"tool_class": "test", "lines_added": 0, "lines_removed": 0, "cmd_hash": TESTS[p],
                        "exit_code": 0, "visible_output_lines": 4, "t_start": t(a), "t_end": t(b)}
            hook(t(b), "agent.tool", data, s, p)
        hook(t(end), "agent.output", {"turn_key": turn, "words": WORDS[name], "code_lines": 0, "is_final": True,
                                      "asks_question": False, "offers_options": False}, s, p)
        hook(t(end), "agent.stop", {"turn_key": turn, "stop_reason": "end_turn", "stop_hook_active": False}, s, p)


for h in range(24):
    hour(h)

# M1. Noor types a message in T3's web client on her laptop at 14:59:00, to
# a thread in agent-desk that T3 runs on a provider pairtime has no hooks
# for. It passes T3's own test for a message a person sent, so the reader
# records it 4 s later, but no session pairtime sees took its run: the
# record names no session, and no prompt ever pairs with it.
fx.emit(at("14:59:04"), "relay.message", {"app": "t3_code", "sent_at": at("14:59:00"), "thread_key": THREAD,
                                          "declared": "none", "len_chars": 90, "low_effort": False,
                                          "prompt": ""},
        source="log", via="app", project=DESK)

events = fx.build()
# An empty link never appears: M1 pairs with nothing, so its record names
# no prompt, and no prompt names it.
for e in events:
    if e["kind"] == "relay.message" and not e["data"]["prompt"]:
        del e["data"]["prompt"]

prompts = [e for e in events if e["kind"] == "prompt.submit"]
starts = [e for e in events if e["kind"] == "session.start"]
relays = [e for e in events if e["kind"] == "relay.message"]
tl.check(len(prompts) == 192, f"{len(prompts)} prompts, not 24 × 8")
tl.check(len(starts) == 49 and len({e["session_key"] for e in starts}) == 49, "49 sessions, each started once")
tl.check(not any("relay" in e["data"] for e in prompts), "no prompt names a relay record")
tl.check(len(relays) == 1 and "prompt" not in relays[0]["data"], "the one relay record names no prompt")
tl.check(relays[0]["session_key"] == "none" and relays[0]["harness"] == "none", "M1's record names no session")

T0, END = at("00:00:00"), at("24:00:00")
DERIVED = ["bucket_minutes", "credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rules 4 and 9, 2.3, 5.1, 5.2", evidence="C",
                  witness="witnessed", projects=PROJECTS, start="00:00:00", end="24:00:00")
HEADLESS = "2.2 rule 4: no relay record witnesses any prompt, so every turn is headless; 5.2's codes"


def hour_row(start, end, note):
    """The hour's numbers, worked by hand in README.md: an agent runs from
    :00:30 to :25:22 without a break, 30 + 24 × 60 + 22 = 1,492 s, all
    X.automated, in the 26 minutes :00 to :25, and the other 30 + 38 = 68 s
    of those minutes are X.no_anchor."""
    exp.row(start, end, credit={}, uncredited={"X.no_anchor": 68, "X.automated": 1492}, agent=1492,
            bucket_minutes=26, sm=HEADLESS, derived=DERIVED, note=note)


for h in range(24):
    a, b = f"{h:02d}:00", f"{h + 1:02d}:00"
    if h == 0:
        hour_row(a, b, "The planner's session starts at 00:00:28. An agent runs from P.1 at 00:00:30 until P.2 stops "
                       "at 00:25:22: the turns run 1,800 s between them and overlap for 308. The 30 s before P.1 and "
                       "the 38 s after P.2 are X.no_anchor. 00:26 to 00:59 have no credit and no agent, so no bucket.")
    elif h == 14:
        hour_row(a, "14:58", "The same hour in 14:00 to 14:25. 14:26 to 14:57 have no bucket.")
        exp.row("14:58", "15:00", bucket=False,
                sm="2.3: a relayed anchor that never pairs opens nothing; 5.1: no credit and no agent running",
                note="Noor sends M1 at 14:59:00 to a thread whose run went to a provider pairtime has no hooks for, "
                     "and the reader records it at 14:59:04 with session_key none. Had it paired, its lead would "
                     "have been 90 / 1.5 = 60 s from 14:58:00. It pairs with nothing, so it opens nothing, and no "
                     "agent runs.")
    elif h == 15:
        hour_row(a, b, "The same hour. P.1 at 15:00:30 comes 90 s after M1's stamp, in M1's project, but no record "
                       "names it and it names none, so it is headless like every prompt here.")
    else:
        hour_row(a, b, None)

rp = rl.Replay(events, T0, END)
rl.check_rows(exp, rp, {e["session_key"]: e["project_key"] for e in starts})
tl.check(len(rp.origin) == 192 and set(rp.origin.values()) == {"headless"}, "every prompt is headless")
tl.check(not rp.relay_of and not rp.anchors and not rp.credited, "nothing is relayed, anchored or credited")

totals = exp.totals()
tl.check(totals["credited_s"] == 0, f"credited {totals['credited_s']}")
tl.check(totals["agent_running_s"] == 35808, f"agent {totals['agent_running_s']}")
tl.check(totals["uncredited"] == {"X.no_anchor": 1632, "X.automated": 35808}, f"uncredited {totals['uncredited']}")
expected = exp.doc(
    extra_totals={
        "level_c_s": 0,
        "via_relay_s": 0,
        "relayed_prompts": 0,
        "headless_prompts": 192,
        "unpaired_relay_records": 1,
    },
    stated=[],
    approx={},
    errata=[rl.ERRATUM],
    notes=[
        "No SM section describes this case. Every row comes from SM 2.2 rules 4 and 9, 2.3, 5.1 and 5.2 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them. Nothing is credited.",
        "The server has no desktop, so pairtime samples no presence and records no focus: every slot's presence "
        "is unknown, and uncredited seconds take SM 5.2's codes for such a slot.",
        "Every prompt carries a message an agent sent through T3's MCP tools, which T3 stores with createdBy agent, "
        "so the T3 reader writes no relay record for it and rule 4 makes the prompt headless. headless_prompts "
        "counts them; relayed_prompts the prompts a relay record witnessed, none.",
        "unpaired_relay_records counts M1, the one relay record: a message the owner sent to a thread whose run "
        "went to a provider pairtime has no hooks for, so the reader couldn't tie it to a session and it pairs "
        "with nothing.",
        "The day repeats one hour's shape, so every hour's row is the same. Hour 14 has two rows, so that M1's "
        "minutes have one of their own.",
        "Read SM 2.2's declarations as recording a message another thread sent (senderThreadId) as declared "
        "cross_session, and every prompt here would be a cross-session subagent by rule 3 instead of headless: "
        "the same rows, but no prompt for rule 4. The case reads it as relayed/t3-away does: createdBy agent, no "
        "record.",
    ],
)

turns = []
for e in prompts:
    stp = [x for x in events if x["kind"] == "agent.stop" and x["data"]["turn_key"] == e["data"]["turn_key"]]
    tl.check(len(stp) == 1, f"one stop for the turn at {fx.hhmmss(e['t'])}")
    turns.append(tl.turn(fx, e["t"], stp[0]["t"], session=e["session_key"], turn_key=e["data"]["turn_key"],
                         origin="headless", sm="2.2 rule 4: no witness, no TTY and no IDE or desktop-app host"))

prompt_entries = [{"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]), "text": None,
                   "len_chars": e["data"]["len_chars"],
                   "source": "invented for this case: an agent's message, which no rule here reads"}
                  for e in prompts]

rl.check_readme(os.path.join(HERE, "README.md"), expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": [], "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
