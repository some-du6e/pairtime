# SPDX-License-Identifier: AGPL-3.0-or-later
"""Copies of one act: a typed prompt, a queued message and a scheduled
prompt, each recorded by Claude Code's hook and again by the transcript
reader. SM 2.2 rules 1 to 3 read every copy, so the scheduled prompt opens
no anchor although its hook copy declares nothing, and the two copies of
the queued message are one Q anchor, the hook's. See README.md in this
directory."""

import os
import sys
from fractions import Fraction

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402

THINK_LEAD_S, COMPOSE_CPS, R_TURN_BASE_S = 120, Fraction(3, 2), 20
KEYS_PRE_S, TYPING_MAX_CPS, SUBSTANTIVE_CHARS = 10, 15, 20
TRACK_LOG_MATCH_S, READ_HORIZON_S, PRESENCE_GAP_S = 10, 600, 120
# The transcript's copy of an act comes 25 ms after the hook's, as the
# enqueue did in WP-AD1's step 21 (docs/errata/2026-11-20-claude-code-queued-turn.md).
COPY_MS = 25
LABELS_ERRATUM = "2026-11-20-claude-code-transcript-labels"
QUEUED_ERRATUM = "2026-11-20-claude-code-queued-turn"
CODES_ERRATUM = "2026-11-19-sm-6.6-uncredited-codes"

fx = tl.Fixture(
    "copies",
    title="Hook and transcript copies of a typed prompt, a queued message and a scheduled prompt",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="macos",
    keys=True,
    device=tl.key("d", "copies macbook"),
    boot_id=tl.key("b", "copies boot"),
    boot="15:02:31.000",
    seq0=30418,
    last_input="16:19:17.900",
    last_key="16:18:58.200",
)
at = fx.at
P = tl.key("p", "copies flashcards")
S = tl.key("s", "copies claude session")
TERMINAL = tl.key("a", "copies Terminal")
SEARCH, SEARCH_TEST = tl.key("f", "copies src/search.ts"), tl.key("f", "copies src/search.test.ts")
CC = dict(harness="claude_code", version="2.1.327", session=S, project=P)

P1_TEXT = "make the search box ignore case and trim spaces in the query"
Q_TEXT = "list the files you changed too"
P2_TEXT = "check the CI run for main and fix any failing tests"
P1, Q, P2 = at("16:22:40"), at("16:23:05"), at("16:28:00")
P1_STOP, P2_STOP = at("16:23:40"), at("16:29:00")
TURN = {"P1": tl.key("t", "copies turn P1"), "P2": tl.key("t", "copies turn P2")}

# Input. He reads the code before writing P1 and types P1 in the 40 s
# before he sends it, the typing part of its lead. While the agent starts
# on it he types the queued message, then watches and reads with the
# mouse. He has begun typing his next prompt when the scheduler sends P2,
# and goes back to the mouse to watch its turn.
fx.pointer("16:19:20.300")                              # clicks Terminal
fx.typing("16:19:23.000", "16:19:26.000", 600)          # "claude", Enter
fx.pointer_every("16:19:37.300", "16:21:57.300", 10_000)  # reads src/search.ts
fx.typing("16:22:00.400", "16:22:39.600", 650)          # types P1
fx.typing("16:22:46.300", "16:23:04.600", 600)          # types the queued message
fx.pointer_every("16:23:08.300", "16:27:48.300", 10_000)  # watches the agent, reads its answer
fx.typing("16:27:50.400", "16:27:59.600", 650)          # starts typing his next prompt
fx.pointer_every("16:28:03.300", "16:29:53.300", 10_000)  # watches P2's turn, reads its answer
fx.ticks("16:19:30", "16:30:00", tl.SLOT_MS)

hook = dict(source="hook", via="hook", sample=True, **CC)
log = dict(source="log", via="log", **CC)


def prompt(t, name, text, declared, via):
    return fx.emit(t, "prompt.submit",
                   {"turn_key": TURN[name], "len_chars": len(text), "pasted_chars": 0, "typed_len_est": len(text),
                    "origin_declared": declared, "matches_pending_cron": False, "answers_question": False,
                    "low_effort": False},
                   **(hook if via == "hook" else log))


def output(t, name, words, final, via):
    return fx.emit(t, "agent.output", {"turn_key": TURN[name], "words": words, "code_lines": 0, "is_final": final,
                                       "asks_question": False, "offers_options": False},
                   **(hook if via == "hook" else log))


def edit(t, f):
    return fx.emit(t, "agent.tool", {"tool_class": "edit", "files": [f], "lines_added": 12, "lines_removed": 8,
                                     "visible_output_lines": 0, "t_start": t - 400, "t_end": t}, **hook)


def stop(t, name):
    return fx.emit(t, "agent.stop", {"turn_key": TURN[name], "stop_reason": "end_turn", "stop_hook_active": False},
                   **hook)


fx.emit(at("16:19:20.400"), "os.focus", {"app_class": "terminal", "app_hash": TERMINAL}, source="os", via="os",
        sample=True)
fx.emit(at("16:19:27.000"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "acceptEdits"}, **hook)
fx.emit(at("16:19:27.000"), "os.focus",
        {"app_class": "terminal", "app_hash": TERMINAL, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
h = {}
# P1: the hook's copy, then the transcript's, neither declaring anything.
h["P1"] = prompt(P1, "P1", P1_TEXT, "none", "hook")
h["P1 log"] = prompt(P1 + COPY_MS, "P1", P1_TEXT, "none", "log")
h["P1 plan"] = output(P1 + 5_000, "P1", 30, False, "log")
# The queued message, in P1's turn: the hook as he queues it, then the
# transcript's enqueue. Claude Code hands it to the agent with the result
# of the agent's next tool call, the edit, inside P1's turn.
h["Q"] = fx.emit(Q, "agent.queue", {"turn_key": TURN["P1"], "len_chars": len(Q_TEXT)}, **hook)
h["Q log"] = fx.emit(Q + COPY_MS, "agent.queue", {"turn_key": TURN["P1"], "len_chars": len(Q_TEXT)}, **log)
h["P1 edit"] = edit(at("16:23:10"), SEARCH)
# The Stop hook: the final answer, which also lists the files, and the stop.
h["P1 final"] = output(P1_STOP, "P1", 180, True, "hook")
h["P1 stop"] = stop(P1_STOP, "P1")
# P2, which Claude Code's scheduler sends: the hook's copy declares
# nothing, the transcript's turnOrigin scheduled declares cron.
h["P2"] = prompt(P2, "P2", P2_TEXT, "none", "hook")
h["P2 log"] = prompt(P2 + COPY_MS, "P2", P2_TEXT, "cron", "log")
h["P2 plan"] = output(P2 + 5_000, "P2", 30, False, "log")
h["P2 edit"] = edit(P2 + 25_000, SEARCH_TEST)
h["P2 final"] = output(P2_STOP, "P2", 90, True, "hook")
h["P2 stop"] = stop(P2_STOP, "P2")

events = fx.build()
built = {id(e["data"]): e for e in events}
ev = {name: built[id(r[4])] for name, r in h.items()}
WINDOW = (at("16:20:00"), at("16:30:00"))


# Checks of the timing choices (README.md).
def slot(t):
    return t - t % tl.SLOT_MS


def keys_in(a, b):
    return any(a <= t <= b for t, k in fx.inputs if k)


key_slots = {slot(t) for t, k in fx.inputs if k}
input_slots = {slot(t) for t, _ in fx.inputs}
tl.check(all(t % tl.SLOT_MS for t, _ in fx.inputs), "no input on a slot boundary")
tl.check(all(s in input_slots for s in range(at("16:19:30"), WINDOW[1], tl.SLOT_MS)),
         "input in every slot from 16:19:30, so every slot is present (yes) and no silence needs SM 2.1's bridge, "
         "tail or return rule")
gaps = [b - a for (a, _), (b, _) in zip(fx.inputs, fx.inputs[1:])]
tl.check(max(gaps) < PRESENCE_GAP_S * 1000, "no silence near PRESENCE_GAP_S")

# Lengths and leads (SM 2.3, 8.2). Each typing part is a whole number of
# seconds, so no rounding comes in (E-6.4-prompt-lengths).
for text, n in ((P1_TEXT, 60), (Q_TEXT, 30), (P2_TEXT, 51)):
    tl.check(len(text) == n and n >= SUBSTANTIVE_CHARS and n % 3 == 0, f"{text!r} has {n} characters")
TYPING = {"P1": Fraction(60) / COMPOSE_CPS, "Q": Fraction(30) / COMPOSE_CPS}
LEAD = {n: THINK_LEAD_S + s for n, s in TYPING.items()}
tl.check(TYPING == {"P1": 40, "Q": 20} and LEAD == {"P1": 160, "Q": 140}, f"typing parts {TYPING}, leads {LEAD}")
TYPING, LEAD = {n: int(s) for n, s in TYPING.items()}, {n: int(s) for n, s in LEAD.items()}
T = {"P1": P1, "Q": Q}
tl.check(P1 - LEAD["P1"] * 1000 == WINDOW[0], "P1's lead starts the window")
for name in ("P1", "Q"):
    first = T[name] - TYPING[name] * 1000
    for s in range(slot(first), T[name], tl.SLOT_MS):
        tl.check(s in key_slots, f"{name}: key presses in slot {fx.hhmmss(s)} of its typing part")
# SM 2.2 rule 6 for P1, and for P2's hook copy read alone: key presses in
# the KEYS_PRE_S before the submit, on the terminal that hosts the session.
for name, t in (("P1", P1), ("P2", P2)):
    tl.check(keys_in(t - KEYS_PRE_S * 1000, t), f"{name}: key presses in the {KEYS_PRE_S} s before it")
# SM 2.3's queued-message test: input in its slot, key presses here, and
# focus on the window hosting the session. E-cc-queued-turn reads rule 6's
# input test in place of "input in its slot", key presses in the
# KEYS_PRE_S before the enqueue, which holds too.
tl.check(slot(Q) in key_slots and keys_in(slot(Q), Q), "key presses in the queued message's slot before it")
tl.check(keys_in(Q - KEYS_PRE_S * 1000, Q), f"Q: key presses in the {KEYS_PRE_S} s before it")
focus = [e for e in events if e["kind"] == "os.focus"]
tl.check(focus[-1]["t"] < WINDOW[0] and focus[-1]["data"].get("hosted_sessions") == [S],
         "Terminal hosts the session throughout")
# SM 2.2 rule 6's plausibility test: typed_len_est <= TYPING_MAX_CPS * 15 s
# per input slot in the lead.
for name, n in (("P1", 60), ("Q", 30)):
    slots = [s for s in range(slot(T[name] - LEAD[name] * 1000), T[name], tl.SLOT_MS) if s in key_slots]
    tl.check(n <= TYPING_MAX_CPS * 15 * len(slots), f"{name}: {n} characters in {len(slots)} key slots is plausible")

# The copies: same turn key, the transcript's 25 ms after the hook's, well
# inside TRACK_LOG_MATCH_S; the queued message's two with the same length.
for a, b in (("P1", "P1 log"), ("Q", "Q log"), ("P2", "P2 log")):
    x, y = ev[a], ev[b]
    tl.check(x["via"] == "hook" and y["via"] == "log" and y["source"] == "log", f"{a}: a hook copy and a log copy")
    tl.check(y["t"] - x["t"] == COPY_MS < TRACK_LOG_MATCH_S * 1000, f"{a}: the log copy {COPY_MS} ms later")
    tl.check(x["kind"] == y["kind"] and x["data"]["turn_key"] == y["data"]["turn_key"]
             and x["data"]["len_chars"] == y["data"]["len_chars"], f"{a}: one act, one turn key, one length")
tl.check([ev[n]["data"]["origin_declared"] for n in ("P1", "P1 log", "P2", "P2 log")]
         == ["none", "none", "none", "cron"], "only P2's transcript copy declares an origin")
tl.check(not any(ev[n]["data"]["matches_pending_cron"] for n in ("P1", "P1 log", "P2", "P2 log")),
         "no copy matches a pending cron")
tl.check(ev["Q"]["data"]["turn_key"] == TURN["P1"], "the queued message is queued in P1's turn")


def agent_record(e):
    return e["session_key"] == S and e["kind"] in ("agent.output", "agent.tool", "agent.stop")


# The queued message resets the bucket when the agent picks it up (SM 2.4),
# which no record states: at the session's first agent record after the
# enqueue, here the edit whose result carries the message, as pkg/score's
# doc.go reads it.
pickup = next(e for e in events if e["seq"] > ev["Q"]["seq"] and agent_record(e))
tl.check(pickup is ev["P1 edit"], "the agent's first record after the enqueue is the edit")
RUNS = {"P1": (P1, P1_STOP), "P2": (P2, P2_STOP)}


def seconds(leads, resets, human):
    """SM 2.7 over the window for this timeline, second by second. Anchor
    windows first: the lead of the next anchor at or after the second wins
    (SM 3.3 rule 3), and a typing part counts only in a slot with key
    presses (SM 2.3). Then the reading bucket (SM 2.4), while an anchor is
    within READ_HORIZON_S: SUP while a turn runs, READ after. Every slot is
    present and focused on the project, so nothing else decides a second.
    leads are (t, lead_ms, typing_ms, reason), resets the times a person's
    act puts the bucket back to its base, and human the turns a person
    started. Returns each second's reason and whether a turn ran in it, and
    what the bucket holds at the window's end."""
    b = tl.Bucket()
    ops = [(t, 0, "reset", R_TURN_BASE_S) for t in resets]
    ops += [(e["t"], 1, "fill", b.amount(e["kind"], e["data"])) for e in events
            if agent_record(e) and e["kind"] != "agent.stop"]
    ops.sort(key=lambda o: (o[0], o[1]))
    out, i = {}, 0
    for tau in range(WINDOW[0], WINDOW[1], 1000):
        while i < len(ops) and ops[i][0] <= tau:
            t, _, op, amount = ops[i]
            if op == "reset":
                b.reset(t, amount)
            else:
                b.fill(t, amount)
            i += 1
        b.expire(tau)
        running = [n for n, (x, y) in RUNS.items() if x <= tau < y]
        why = next((r for t, lead, typing, r in sorted(leads)
                    if t - lead <= tau < t and (tau < t - typing or slot(tau) in key_slots)), None)
        if why is None:
            near = any(-READ_HORIZON_S * 1000 <= tau - t < READ_HORIZON_S * 1000 for t, _, _, _ in leads)
            if b.total() > 0 and near:
                tl.check(b.drain(1) == 1, f"a whole second to read at {fx.hhmmss(tau)}")
                why = "SUP" if running else "READ"
            elif running:
                why = "X.agent_only" if running[0] in human else "X.automated"
            else:
                why = "X.no_anchor"
        out[tau] = (why, bool(running))
    return out, b.total()


def minutes(sec):
    """Each minute's seconds by reason, and its seconds with a turn running."""
    out = {}
    for tau, (why, running) in sec.items():
        m = fx.hhmm(tau)
        got, agent = out.get(m, ({}, 0))
        got[why] = got.get(why, 0) + 1
        out[m] = (got, agent + running)
    return out


# The rows. P1 and the queued message are the anchors. P1 resets the bucket
# at its submit and the message at its pickup, and P2, which the scheduler
# sent, opens nothing, resets nothing and adds no base.
LEADS = [(P1, LEAD["P1"] * 1000, TYPING["P1"] * 1000, "P"), (Q, LEAD["Q"] * 1000, TYPING["Q"] * 1000, "Q")]
sec, left = seconds(LEADS, [P1, pickup["t"]], {"P1"})
tl.check(left == 85, f"85 s left to read at 16:30:00, {left}")
ROWS = minutes(sec)

# What the two readings this fixture pins would change (README.md). Taking
# the transcript's agent.queue for a second message adds a Q anchor 25 ms
# after the first, whose typing part covers the second of 16:23:05.
two, _ = seconds(LEADS + [(Q + COPY_MS, LEAD["Q"] * 1000, TYPING["Q"] * 1000, "Q")],
                 [P1, pickup["t"], pickup["t"]], {"P1"})
tl.check({m: v for m, v in minutes(two).items() if v != ROWS[m]}
         == {"16:23": ({"Q": 6, "SUP": 34, "READ": 20}, 40)}, "two Q anchors would credit 16:23:05 as Q")
# Reading rules 1 to 3 on the hook copy alone makes P2 human_typed and
# substantive: key presses in the KEYS_PRE_S before it, Terminal hosting
# the session, and 51 characters in its one key slot, 16:27:45, under 15 *
# 15. Its lead is 120 + 51 / 1.5 = 154 s, and its submit resets the bucket.
P2_LEAD, P2_TYPING = THINK_LEAD_S + Fraction(51) / COMPOSE_CPS, Fraction(51) / COMPOSE_CPS
tl.check(P2_LEAD == 154 and P2_TYPING == 34 and 51 <= TYPING_MAX_CPS * 15 * len(
    [s for s in range(slot(P2 - 154_000), P2, tl.SLOT_MS) if s in key_slots]), "P2 read alone would be typed")
hook_only, _ = seconds(LEADS + [(P2, 154_000, 34_000, "P")], [P1, pickup["t"], P2], {"P1", "P2"})
tl.check({m: v for m, v in minutes(hook_only).items() if v != ROWS[m]} == {
    "16:25": ({"READ": 26, "P": 34}, 0), "16:26": ({"P": 60}, 0), "16:27": ({"P": 41, "READ": 19}, 0),
    "16:28": ({"SUP": 60}, 60)}, "P2 read from its hook copy alone would credit its lead and base")


exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 and 2.3", evidence="A", witness="witnessed",
                  projects={P: "flashcards"}, start="16:20:00", end="16:30:00")
DERIVED = ["credit", "uncredited", "agent_running_s"]


def row(m, credit, unc, agent, sm=None, note=None, erratum=None):
    got, ran = ROWS[m]
    want = dict(credit)
    want.update(unc)
    tl.check(got == want and ran == agent,
             f"row {m}: the seconds give {got} and {ran} s of agent, the row {want} and {agent}")
    exp.row(m, credit={P: credit} if credit else {}, uncredited=unc, agent=agent, sm=sm, note=note, derived=DERIVED,
            erratum=erratum)


row("16:20", {"P": 60}, {}, 0, sm="2.3, substantive prompt: P1's think part")
row("16:21", {"P": 60}, {}, 0, sm="2.3, substantive prompt: P1's think part")
row("16:22", {"P": 40, "Q": 20}, {}, 20, sm="2.3, substantive prompt and queued message",
    note="P1's typing part to 16:22:40, then the queued message's lead, its think part to 16:22:45 and its "
         "typing part after, since its lead is the next anchor's (SM 3.3 rule 3) and beats P1's bucket (rule 2).")
row("16:23", {"Q": 5, "SUP": 35, "READ": 20}, {}, 40, sm="2.3, queued message; 2.4",
    note="The queued message's typing part to 16:23:05, the hook copy's time. The transcript copy 25 ms later "
         "is the same message and opens no window of its own. Then P1's bucket while P1 runs, reset to its base "
         "at 16:23:10, when the agent takes the message up, and READ after P1's stop at 16:23:40.")
row("16:24", {"READ": 60}, {}, 0, sm="2.4")
row("16:25", {"READ": 60}, {}, 0, sm="2.4")
row("16:26", {"READ": 60}, {}, 0, sm="2.4", note="The bucket runs dry at 16:27:00.")
got, ran = ROWS["16:27"]
tl.check(got == {"X.no_anchor": 60} and ran == 0, f"16:27 credits nothing and runs no agent: {got}")
exp.row("16:27", bucket=False, codes=["X.no_anchor"], sm="2.2, rule 1, and 5.1", derived=["bucket"],
        note="He is at the keyboard and focused on the terminal, typing from 16:27:50, but no window covers the "
             "minute and the bucket is empty. Nothing is credited and no agent runs, so the minute has no bucket "
             "(SM 5.1). Read from its hook copy alone, P2 would be human_typed, and its 154 s lead would credit "
             "this minute.")
row("16:28", {"SUP": 45}, {"X.automated": 15}, 60, sm="2.2, rule 1; 2.4", erratum=CODES_ERRATUM,
    note="P2 is scheduled, so it opens no anchor, adds no base and resets nothing. Its own output fills the "
         "bucket, and the queued message, within READ_HORIZON_S, lets it drain as SUP from 16:28:05 to 16:28:15 "
         "and from 16:28:25. The 15 s with the bucket empty are X.automated by the uncredited-codes erratum's "
         "rule, which pkg/score applies.")
row("16:29", {"READ": 60}, {}, 0, sm="2.4", note="P2's answer and the rest of its edit, 145 s, read from P2's stop.")

expected = exp.doc(
    extra_totals={"credited_s": 525, "agent_only_s": 15, "level_a_s": 525},
    stated=[],
    approx={},
    errata=[LABELS_ERRATUM, QUEUED_ERRATUM, CODES_ERRATUM],
    notes=[
        "A derived fixture. SM states no rows for it, so every row follows from SM's rules applied to this "
        "timeline, with pkg/score's readings where SM leaves a choice (README.md).",
        "SM 2.2 rules 1 to 3 read every copy of a prompt that came through a hook or a log reader "
        "(E-cc-transcript-labels). P2's hook copy declares nothing and its transcript copy declares cron, so P2 is "
        "scheduled and opens no anchor, though he types in the 10 s before it on the terminal that hosts the session.",
        "A hook's and a transcript's agent.queue of one message, in one turn and within TRACK_LOG_MATCH_S, are one "
        "queued message (E-cc-queued-turn), with one Q anchor, the hook's, corroborated, and one reset of the bucket.",
        "Each lead is 120 s plus the typing time. P1's 60 characters give 160 s and the queued message's 30 give "
        "140 s.",
        "The bucket holds 85 s when the records end at 16:30:00, P2's 145 s less the 60 s read in 16:29.",
        "Row 16:28 names the 6.6 uncredited-codes erratum, whose rule makes the uncredited seconds of a turn that "
        "automation started X.automated. SM 5.2 alone doesn't say whether they are X.automated or X.agent_only.",
    ],
)

anchor_list = [
    tl.anchor(fx, ev["P1"], kind="substantive_prompt", origin="human_typed", prompt_class="substantive", lead_s=160,
              trail_s=0, lead_from="params", level="A",
              sm="P1's hook copy. The transcript's copy, 25 ms later, declares nothing either, so rules 1 to 3 "
                 "leave P1 the person's (SM 2.2). One anchor, corroborated"),
    tl.anchor(fx, ev["Q"], kind="queued_message", origin="human_typed", prompt_class="substantive", lead_s=140,
              trail_s=0, lead_from="params", level="A",
              sm="The hook's agent.queue of the queued message. The transcript's, 25 ms later in the same turn, is "
                 "the same message (E-cc-queued-turn), so there is one Q anchor, corroborated. It has key presses in "
                 "its slot with Terminal, which hosts the session, in focus, and its 30 characters make it "
                 "substantive, so its lead is 120 + 30 / 1.5 (SM 2.3)"),
]
turns = [
    tl.turn(fx, P1, P1_STOP, session=S, turn_key=TURN["P1"], origin="human_typed", prompt_class="substantive",
            sm="P1, whose copies both declare nothing. The agent takes the queued message up inside it"),
    tl.turn(fx, P2, P2_STOP, session=S, turn_key=TURN["P2"], origin="scheduled",
            sm="P2, scheduled by SM 2.2 rule 1 read from the transcript's copy, whose turnOrigin scheduled the "
               "reader writes as origin_declared cron"),
]
anchors = {"fixture": fx.name, "anchors": anchor_list, "turns": turns}
SOURCE = "invented: a derived fixture, which no SM example quotes"
prompts = {"fixture": fx.name, "prompts": [
    {"turn_key": TURN[n], "t": t, "local": fx.hhmmss(t), "text": text, "len_chars": len(text), "source": SOURCE}
    for n, t, text in (("P1", P1, P1_TEXT), ("P2", P2, P2_TEXT))]}

fx.write(tl.out_dir(HERE), expected, anchors, prompts)
