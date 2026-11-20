# SPDX-License-Identifier: AGPL-3.0-or-later
"""Two Claude Code sessions in two projects, running in parallel, as
lookback sees them from their transcripts: SM 2.8's rule that a second two
sessions want goes to the one with the most recent human prompt. See
README.md in this directory."""

import os
import sys
from fractions import Fraction

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import lookback as lb  # noqa: E402

NAME = "lookback/two-sessions"
fx = tl.Fixture(
    NAME,
    title="Two Claude Code sessions in parallel, from their transcripts alone",
    date="2026-11-22",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="windows",
    keys=False,
    device=tl.key("d", "lookback noor laptop"),
    boot_id=tl.key("b", "lookback noor boot"),
    boot="09:41:07.000",
    seq0=1,
    last_input="09:41:07.000",
)
at = fx.at
A, B = tl.key("p", "trivia-bot"), tl.key("p", "portfolio")
SA, SB = tl.key("s", "two-sessions trivia-bot session"), tl.key("s", "two-sessions portfolio session")
PROJECT = {SA: A, SB: B}
ENV = {SA: dict(harness="claude_code", version="2.1.328", session=SA, project=A),
       SB: dict(harness="claude_code", version="2.1.328", session=SB, project=B)}
TURN = {name: tl.key("t", f"two-sessions {name}") for name in ("A1", "A2", "A3", "B1", "B2", "B3")}
FILE = {name: tl.key("f", f"two-sessions {name}") for name in ("quiz.py", "scores.py", "api.py", "index.html",
                                                                "style.css")}
BUILD = tl.key("c", "two-sessions npm run build")
TESTS = tl.key("c", "two-sessions python -m pytest")


def emit(s, t, kind, data):
    # Every record comes from a transcript, through the log reader.
    return fx.emit(at(t), kind, data, source="log", via="log", **ENV[s])


def start(s, t, mode):
    emit(s, t, "session.start", {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app",
                                 "has_tty": True, "permission_mode": mode})


def prompt(s, t, turn, length, pasted=0):
    emit(s, t, "prompt.submit", {"turn_key": TURN[turn], "len_chars": length, "pasted_chars": pasted,
                                 "typed_len_est": length - pasted, "origin_declared": "none",
                                 "matches_pending_cron": False, "answers_question": False, "low_effort": False})


def output(s, t, turn, words, final=False):
    emit(s, t, "agent.output", {"turn_key": TURN[turn], "words": words, "code_lines": 0, "is_final": final,
                                "asks_question": False, "offers_options": False})


def edit(s, t, t_start, file, added, removed):
    emit(s, t, "agent.tool", {"tool_class": "edit", "files": [FILE[file]], "lines_added": added,
                              "lines_removed": removed, "visible_output_lines": 0, "t_start": at(t_start),
                              "t_end": at(t)})


def permission(s, requested, answered):
    emit(s, answered, "agent.permission", {"requested_at": at(requested), "answered_at": at(answered),
                                           "decision": "accept", "decided_by": "user"})


def stop(s, t, turn):
    emit(s, t, "agent.stop", {"turn_key": TURN[turn], "stop_reason": "end_turn", "stop_hook_active": False})


# Noor starts Claude Code in trivia-bot, then in portfolio, a few minutes
# before the window. portfolio's session accepts edits without asking.
# trivia-bot's asks until Noor answers A1's edit with "allow all edits this
# session"; after that only its commands ask.
start(SA, "15:52:10", "default")
start(SB, "15:55:40", "acceptEdits")

# Round 1. B1 at 16:01:00, then A1 at 16:02:00. A1's edit waits 100 s for
# its permission answer.
prompt(SB, "16:01:00", "B1", 90)
output(SB, "16:01:20", "B1", 45)
prompt(SA, "16:02:00", "A1", 60)
edit(SB, "16:02:10", "16:02:09.400", "index.html", 10, 6)
output(SA, "16:02:20", "A1", 30)
output(SB, "16:03:30", "B1", 60, final=True)
stop(SB, "16:03:30", "B1")
permission(SA, "16:03:00", "16:04:40")
edit(SA, "16:04:45", "16:03:00", "quiz.py", 8, 4)
output(SA, "16:05:00", "A1", 120, final=True)
stop(SA, "16:05:00", "A1")

# Round 2. A2 at 16:09:40, with 300 pasted characters (a JSON sample of the
# quiz format) and 90 typed, then B2 at 16:10:40, whose build asks
# permission. A2's test run asks after that, at 16:12:00, and A2 stops at
# 16:13:30, after B2: A ends round 2 with the later stop and the later human
# act, and B with the later prompt.
prompt(SA, "16:09:40", "A2", 390, pasted=300)
output(SA, "16:10:05", "A2", 45)
prompt(SB, "16:10:40", "B2", 60)
edit(SA, "16:11:00", "16:10:59.500", "scores.py", 14, 6)
output(SB, "16:11:05", "B2", 60)
permission(SB, "16:11:20", "16:11:35")
emit(SB, "16:11:40", "agent.tool", {"tool_class": "build", "lines_added": 0, "lines_removed": 0, "cmd_hash": BUILD,
                                    "exit_code": 0, "visible_output_lines": 4, "t_start": at("16:11:20"),
                                    "t_end": at("16:11:40")})
permission(SA, "16:12:00", "16:12:20")
edit(SB, "16:12:20", "16:12:19.600", "style.css", 9, 3)
output(SB, "16:13:00", "B2", 45, final=True)
stop(SB, "16:13:00", "B2")
emit(SA, "16:13:10", "agent.tool", {"tool_class": "test", "lines_added": 0, "lines_removed": 0, "cmd_hash": TESTS,
                                    "exit_code": 0, "visible_output_lines": 4, "t_start": at("16:12:00"),
                                    "t_end": at("16:13:10")})
output(SA, "16:13:30", "A2", 75, final=True)
stop(SA, "16:13:30", "A2")

# Round 3. A3 at 16:18:20 and B3 at 16:19:00, still running when the window
# ends. Nothing follows their stops: the transcripts end there.
prompt(SA, "16:18:20", "A3", 120)
output(SA, "16:18:50", "A3", 50)
prompt(SB, "16:19:00", "B3", 45)
edit(SA, "16:19:10", "16:19:09.300", "api.py", 18, 7)
output(SB, "16:19:30", "B3", 40)
output(SA, "16:21:40", "A3", 90, final=True)
stop(SA, "16:21:40", "A3")
output(SB, "16:22:00", "B3", 70, final=True)
stop(SB, "16:22:00", "B3")

events = fx.build()
T0, END = at("16:00:00"), at("16:20:00")

# The rows, computed by hand from SM 2.8 (README.md). "A" is trivia-bot and
# "B" portfolio.
ER = lb.ERRATUM
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.8", evidence="C", witness="backfilled",
                  projects={A: "trivia-bot", B: "portfolio"}, start="16:00:00", end="16:20:00")
MOST_RECENT = "2.8: a second two sessions want goes to the one with the most recent human prompt"
READING = "2.8: reading between a stop and a human prompt that follows within 600 s, at most 120 s a turn"


def row(minute, credit, unc, agent, sm, note=None, erratum=None):
    exp.row(minute, credit=credit, uncredited=unc, agent=agent, sm=sm, derived=DERIVED, note=note,
            erratum=erratum)


row("16:00", {B: {"P": 60}}, {}, 0, "2.8: leads are typing time only",
    note="B1's 90 typed characters: 90 / 1.5 = 60 s, from 16:00:00.")
row("16:01", {A: {"P": 40}}, {"X.agent_only": 20}, 60, "2.8: leads are typing time only",
    note="B1 runs from 16:01:00. A1's 60 characters: 40 s, from 16:01:20.")
row("16:02", {}, {"X.agent_only": 60}, 60, "2.8: neither session reads while its agent runs")
row("16:03", {A: {"PA": 20}, B: {"READ": 10}}, {"X.agent_only": 30}, 60, MOST_RECENT,
    note="B1 stops at 16:03:30 and B2 follows 430 s later, so B reads from the stop. A1's permission, asked at "
         "16:03:00 and answered at 16:04:40, keeps 60 s of its 100 s wait, from 16:03:40. A's last prompt, "
         "16:02:00, is later than B's, 16:01:00, so A's PA wins the seconds both want.")
row("16:04", {A: {"PA": 50}, B: {"READ": 10}}, {}, 60, MOST_RECENT, erratum=ER,
    note="A's PA runs to 16:04:50 with its 10 s trail; B reads again until A1 stops at 16:05:00.")
row("16:05", {A: {"READ": 60}}, {}, 0, MOST_RECENT,
    note="From A1's stop at 16:05:00 both sessions have output to read. A's last prompt is the more recent, so A "
         "reads first: 120 s, to 16:07:00. B still has 100 s.")
row("16:06", {A: {"READ": 60}}, {}, 0, MOST_RECENT)
row("16:07", {B: {"READ": 60}}, {}, 0, READING,
    note="A's 120 s are read, so B reads its last 100 s, to 16:08:40.")
row("16:08", {A: {"P": 20}, B: {"READ": 40}}, {}, 0, READING, erratum=ER,
    note="A2 has 390 characters, 300 of them pasted. Its lead is typing time only, 90 / 1.5 = 60 s, from 16:08:40.")
row("16:09", {A: {"P": 40}}, {"X.agent_only": 20}, 20, "2.8: leads are typing time only")
row("16:10", {B: {"P": 40}}, {"X.agent_only": 20}, 60, "2.8: leads are typing time only",
    note="B2's 60 characters: 40 s, from 16:10:00, while A2 runs.")
row("16:11", {B: {"PA": 25}}, {"X.agent_only": 35}, 60, "2.8: permission answers keep their wait", erratum=ER,
    note="B2's build asks at 16:11:20 and is answered at 16:11:35: its 15 s wait and 10 s trail.")
row("16:12", {A: {"PA": 30}}, {"X.agent_only": 30}, 60, "2.8: permission answers keep their wait", erratum=ER,
    note="A2's test run asks at 16:12:00 and is answered at 16:12:20: its 20 s wait and 10 s trail. The answer is "
         "A's latest human act, later than B's at 16:11:35.")
row("16:13", {B: {"READ": 60}}, {}, 30, MOST_RECENT,
    note="B2 stops at 16:13:00 and B3 follows 360 s later, so B reads from its stop while A2 runs. A2 stops at "
         "16:13:30 and A3 follows 290 s later, so from then both sessions have output to read. A has the later "
         "stop and the later human act, but B's last prompt, 16:10:40, is later than A's, 16:09:40, so B reads "
         "on, to 16:15:00. A rule by the latest stop or the latest human act would give A 16:13:30 to 16:15:30.")
row("16:14", {B: {"READ": 60}}, {}, 0, MOST_RECENT)
row("16:15", {A: {"READ": 60}}, {}, 0, READING, note="B's 120 s are read, so A reads its 120 s, to 16:17:00.")
row("16:16", {A: {"READ": 60}}, {}, 0, READING)
row("16:17", {A: {"P": 60}}, {}, 0, "2.8: leads are typing time only",
    note="A3's 120 characters: 80 s, from 16:17:00, where A's reading ends.")
row("16:18", {A: {"P": 20}, B: {"P": 30}}, {"X.agent_only": 10}, 40, "2.8: leads are typing time only",
    note="A3 is sent at 16:18:20 and runs. B3's 45 characters: 30 s, from 16:18:30, while A3 runs.")
row("16:19", {}, {"X.agent_only": 60}, 60, "2.8: neither session reads while its agent runs",
    note="A3 and B3 run past the window and stop at 16:21:40 and 16:22:00. No prompt follows either, so their "
         "output is never read as far as the rules can tell.")

rp = lb.Replay(events, T0, END)
lb.check_rows(exp, rp, PROJECT)


def latest(times):
    """The session whose time is latest. One with no time, such as a session
    that hasn't stopped yet, counts as the earliest."""
    have = {k: t for k, t in times.items() if t is not None}
    return max(have, key=have.get)


def runs(secs):
    """Seconds, as Unix ms, grouped into runs of local [from, to)."""
    out = []
    for t in sorted(secs):
        if out and out[-1][1] == t:
            out[-1][1] = t + 1000
        else:
            out.append([t, t + 1000])
    return [(fx.hhmmss(a), fx.hhmmss(b)) for a, b in out]


# The seconds both sessions wanted: A's PA against B's reading, then reading
# against reading twice, with a different winner each time. Each span also
# names the session that the latest stop and the latest human act would pick
# instead of SM 2.8's latest prompt. From 16:13:30 both pick A while the
# prompt picks B, so those 90 s tell SM 2.8's rule from the other two. At
# 16:03:40 A hasn't stopped yet, and at 16:05:00 all three pick A. No typing
# lead is ever contested (README.md: SM 2.8 doesn't say how a lead's seconds
# compare with another session's, see the erratum's point 3).
spans, cur = [], None
for tau, cands, win in rp.conflicts:
    by_stop = latest({k: rp.sessions[k].last_stop(tau) for k in cands})
    by_act = latest({k: rp.sessions[k].last_anchor(tau) for k in cands})
    what = (tuple(sorted((PROJECT[k], tuple(r for r, _ in c)) for k, c in cands.items())),
            PROJECT[win], PROJECT[by_stop], PROJECT[by_act])
    if cur and cur[1] == tau and cur[2] == what:
        cur[1] = tau + 1000
    else:
        cur = [tau, tau + 1000, what]
        spans.append(cur)
got = [(fx.hhmmss(a), fx.hhmmss(b), *(exp.projects[p] for p in w[1:])) for a, b, w in spans]
tl.check(got == [("16:03:40", "16:04:50", "trivia-bot", "portfolio", "trivia-bot"),
                 ("16:05:00", "16:07:00", "trivia-bot", "trivia-bot", "trivia-bot"),
                 ("16:13:30", "16:15:00", "portfolio", "trivia-bot", "trivia-bot")],
         f"contested seconds, each with the sessions the latest prompt, stop and human act pick: {got}")
tl.check(all(r != "P" for _, cands, _ in rp.conflicts for c in cands.values() for r, _ in c),
         "no typing lead is contested")

# Every turn a prompt follows within 600 s fills its bucket past the 120 s
# cap, and reads exactly 120 s between its stop and that prompt. A3 and B3,
# which nothing follows, fill 254 1/6 s and 56 2/3 s.
NAME_OF = {k: n for n, k in TURN.items()}
SUBMIT = {(e["session_key"], e["t"]): e["data"]["turn_key"] for e in events if e["kind"] == "prompt.submit"}
fills, reads = {}, {}
for s in rp.sessions.values():
    for a, b, _ in s.turns:
        name = NAME_OF[SUBMIT[(s.key, a)]]
        fills[name] = lb.R_TURN_BASE_S + sum(x for t, x in s.fills if a < t <= b)
        nxt = [p for t0, p in rp.spans[s.key] if t0 == b]
        if nxt:
            reads[name] = sum(1 for t in range(b, nxt[0], 1000) if rp.credited.get(t) == (s.key, "READ"))
tl.check(fills == {"B1": 175, "A1": 160, "A2": 240, "B2": 175, "A3": Fraction(1525, 6), "B3": Fraction(170, 3)},
         f"each turn's fill {fills}")
tl.check(reads == {"B1": 120, "A1": 120, "A2": 120, "B2": 120}, f"each turn's reading {reads}")

# The erratum's point 2: the cap also cuts B's reading while A's agent runs,
# 30 s in all. They are X.agent_only, since an agent turn runs.
tl.check(runs(rp.capped) == [("16:09:40", "16:10:00"), ("16:18:20", "16:18:30")] and rp.capped <= rp.running,
         f"the cap cuts B's reading in {runs(rp.capped)}, all while an agent runs")

totals = exp.totals()
tl.check(totals["credited_s"] == 915, f"credited {totals['credited_s']}")
tl.check(totals["credited_by_project"] == {A: 520, B: 395}, f"by project {totals['credited_by_project']}")
by = {p: {} for p in (A, B)}
for r in exp.rows:
    for p, reasons in r.get("credit", {}).items():
        for k, s in reasons.items():
            by[p][k] = by[p].get(k, 0) + s
tl.check(by[A] == {"P": 40 + 60 + 80, "PA": 60 + 10 + 20 + 10, "READ": 120 + 120}
         and by[B] == {"P": 60 + 40 + 30, "PA": 15 + 10, "READ": 120 + 120},
         f"by project and reason {by}")
agent_only = len(rp.running) - sum(1 for t in rp.running if t in rp.credited)
tl.check(agent_only == totals["uncredited"]["X.agent_only"] == 285, f"agent-only {agent_only}")

expected = exp.doc(
    extra_totals={
        "ai_credited_s": 915,
        "hand_credited_s": 0,
        "agent_only_s": agent_only,
        "trivia_bot_p_s": by[A]["P"],
        "trivia_bot_pa_s": by[A]["PA"],
        "trivia_bot_read_s": by[A]["READ"],
        "portfolio_p_s": by[B]["P"],
        "portfolio_pa_s": by[B]["PA"],
        "portfolio_read_s": by[B]["READ"],
        "contested_s": len(rp.conflicts),
    },
    stated=[],
    approx={},
    errata=[ER],
    notes=[
        "Every row is computed from SM 2.8; no SM section describes this case. Every credited second is level C "
        "and backfilled.",
        "The six turns are in two Claude Code sessions in two projects, three in each, so a second both sessions "
        "want is wanted by two projects, and SM 3.3's FIFO rule for buckets of one project never applies.",
        "contested_s counts the seconds both sessions wanted: 70 of A's PA against B's reading, and 210 of reading "
        "against reading. The session with the most recent human prompt takes each (SM 2.8). In the last 90, from "
        "16:13:30, A has the later stop and the later human act, so only the prompt rule gives them to B.",
        "Every uncredited second here is X.agent_only: an agent turn ran and nothing credited it. Every turn a "
        "prompt follows within 600 s fills its bucket past the 120 s cap and reads exactly 120 s.",
    ],
)

anchors, turns = [], []
for e in events:
    if e["kind"] == "prompt.submit":
        d = e["data"]
        lead = lb.typing_ms(d["typed_len_est"]) // 1000
        anchors.append(tl.anchor(fx, e, kind="substantive_prompt", origin="human_typed", prompt_class="substantive",
                                 lead_s=lead, trail_s=0, lead_from="params", level="C",
                                 sm=f"2.8: no input check, so human_typed; the lead is typing time only, "
                                    f"{d['typed_len_est']} / 1.5 = {lead} s"))
    elif e["kind"] == "agent.permission":
        d = e["data"]
        wait = (d["answered_at"] - d["requested_at"]) // 1000
        anchors.append(tl.anchor(fx, e, kind="permission_answer", lead_s=min(wait, 60), trail_s=10,
                                 lead_from="params", level="C",
                                 sm=f"2.8: a permission answer keeps its wait, {wait} s, up to UNKNOWN_PA_MAX_S = 60 s; "
                                    "the 10 s trail"))
for s, names in ((SA, ("A1", "A2", "A3")), (SB, ("B1", "B2", "B3"))):
    for name in names:
        sub = [e for e in events if e["kind"] == "prompt.submit" and e["data"]["turn_key"] == TURN[name]][0]
        stp = [e for e in events if e["kind"] == "agent.stop" and e["data"]["turn_key"] == TURN[name]][0]
        turns.append(tl.turn(fx, sub["t"], stp["t"], session=s, turn_key=TURN[name], origin="human_typed",
                             prompt_class="substantive", sm="2.8: every prompt rules 1 to 5 don't label is human_typed"))
turns.sort(key=lambda t: t["t_submit"])

prompt_entries = []
for e in events:
    if e["kind"] == "prompt.submit":
        name = [n for n, k in TURN.items() if k == e["data"]["turn_key"]][0]
        src = "invented for this case, which no SM section describes; only its length matters"
        if name == "A2":
            src += ": 90 typed characters and a pasted 300-character JSON sample"
        prompt_entries.append({"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]),
                               "text": None, "len_chars": e["data"]["len_chars"], "source": src})

# README.md works the same rows out by hand; its minute tables and totals
# must say what expected.json says. internal/timelines checks this too.
readme = os.path.join(HERE, "README.md")
lb.check_readme(readme, expected, exp.projects)
lb.check_readme_totals(readme, expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
