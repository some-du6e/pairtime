# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 4.2's alternation case: five prompts A, B, A, B, A with no change
event between them. The third A is attempt 3 and discounted as X.reroll;
the others are credited. See README.md in this directory."""

import os
import sys
from fractions import Fraction

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "6.5"))

import neardup  # noqa: E402
import timeline as tl  # noqa: E402

THINK_LEAD_S, COMPOSE_CPS = 120, Fraction(3, 2)

fx = tl.Fixture(
    "4.2-alternation",
    title="Alternating two prompts: A, B, A, B, A",
    date="2026-11-23",
    timezone_name="Australia/Melbourne",
    utc_offset_min=660,
    os_name="macos",
    keys=True,
    device=tl.key("d", "4.2-alternation macbook"),
    boot_id=tl.key("b", "4.2-alternation boot"),
    boot="08:02:11.000",
    seq0=9012,
    last_input="19:29:31.400",
    last_key="19:29:12.800",
)
at = fx.at
P = tl.key("p", "4.2-alternation dashboard")
S = tl.key("s", "4.2-alternation claude session")
TERMINAL = tl.key("a", "4.2-alternation Terminal")
SIDEBAR, TOPBAR = tl.key("f", "4.2-alternation src/sidebar.css"), tl.key("f", "4.2-alternation src/topbar.css")
CC = dict(harness="claude_code", version="2.1.328", session=S, project=P)

A = "make the sidebar collapse into an icon rail on small screens"
B = "keep the top bar pinned to the top of the page as you scroll"
# (name, sent, text, the file the agent edits)
PROMPTS = [("A1", "19:33:00", A, SIDEBAR), ("B1", "19:36:00", B, TOPBAR), ("A2", "19:39:00", A, SIDEBAR),
           ("B2", "19:42:00", B, TOPBAR), ("A3", "19:45:00", A, SIDEBAR)]
TURN = {name: tl.key("t", f"4.2-alternation turn {name}") for name, _, _, _ in PROMPTS}

# Input: he reads and scrolls while each answer arrives, then types the next
# prompt in the 40 s before he sends it, which is its lead's typing part.
fx.pointer("19:29:34.600")                         # clicks the terminal
fx.typing("19:29:38.000", "19:29:41.000", 600)     # "claude", Enter
fx.pointer_every("19:29:52.300", "19:32:12.300", 10_000)  # reads the sidebar code
fx.typing("19:32:20.400", "19:32:59.600", 650)     # types A
for name, sent, text, _ in PROMPTS:
    t = at(sent)
    fx.pointer_every(fx.hhmmss(t + 4_300), fx.hhmmss(t + 59_300), 11_000)  # watches the agent
    if name != "A3":
        fx.pointer_every(fx.hhmmss(t + 66_200), fx.hhmmss(t + 136_200), 10_000)  # reads, thinks
        fx.typing(fx.hhmmss(t + 140_400), fx.hhmmss(t + 179_600), 650)  # types the next prompt
fx.inputs = [(t + 50 if t % tl.SLOT_MS == 0 else t, k) for t, k in fx.inputs]
fx.ticks("19:29:45", "19:46:00", tl.SLOT_MS)

fx.emit(at("19:29:35.000"), "os.focus", {"app_class": "terminal", "app_hash": TERMINAL}, source="os", via="os",
        sample=True)
fx.emit(at("19:29:42.000"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "acceptEdits"},
        source="hook", via="hook", sample=True, **CC)
fx.emit(at("19:29:42.000"), "os.focus",
        {"app_class": "terminal", "app_hash": TERMINAL, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
handles = {}
for name, sent, text, f in PROMPTS:
    t = at(sent)
    handles[name] = fx.emit(t, "prompt.submit",
                            {"turn_key": TURN[name], "len_chars": len(text), "pasted_chars": 0,
                             "typed_len_est": len(text), "origin_declared": "none", "matches_pending_cron": False,
                             "answers_question": False, "low_effort": False},
                            source="hook", via="hook", sample=True, **CC)
    # Every turn: a short plan, one 20-line edit of its own file, a 110-word
    # answer, the stop a minute after the prompt.
    fx.emit(t + 5_000, "agent.output", {"turn_key": TURN[name], "words": 30, "code_lines": 0, "is_final": False,
                                        "asks_question": False, "offers_options": False},
            source="log", via="log", **CC)
    fx.emit(t + 25_000, "agent.tool", {"tool_class": "edit", "files": [f], "lines_added": 12, "lines_removed": 8,
                                       "visible_output_lines": 0, "t_start": t + 24_600, "t_end": t + 25_000},
            source="hook", via="hook", sample=True, **CC)
    fx.emit(t + 58_000, "agent.output", {"turn_key": TURN[name], "words": 110, "code_lines": 0, "is_final": True,
                                         "asks_question": False, "offers_options": False},
            source="log", via="log", **CC)
    fx.emit(t + 60_000, "agent.stop", {"turn_key": TURN[name], "stop_reason": "end_turn", "stop_hook_active": False},
            source="hook", via="hook", sample=True, **CC)

events = fx.build()
built = {id(e["data"]): e for e in events}
ev = {name: built[id(h[4])] for name, h in handles.items()}
SENT = {name: at(sent) for name, sent, _, _ in PROMPTS}


# Checks of the timing choices against SM 4.2 (README.md).
def slot_inputs(s):
    return [k for t, k in fx.inputs if s <= t < s + tl.SLOT_MS]


WINDOW = (at("19:31:00"), at("19:46:00"))
tl.check(all(t % tl.SLOT_MS for t, _ in fx.inputs), "no input on a slot boundary")
for s in range(WINDOW[0], WINDOW[1], tl.SLOT_MS):
    tl.check(slot_inputs(s), f"input in slot {fx.hhmmss(s)}")
lead = THINK_LEAD_S + Fraction(60) / COMPOSE_CPS
tl.check(lead == 160 and all(len(text) == 60 for _, _, text, _ in PROMPTS), "every lead is 120 + 60 / 1.5 = 160 s")
names = [name for name, _, _, _ in PROMPTS]
for prev, name in zip(names, names[1:]):
    tl.check(SENT[name] - lead * 1000 == SENT[prev] + 20_000, f"{name}'s lead starts 20 s after {prev}")
for name in names:
    t = SENT[name]
    for s in range(t - 40_000, t, tl.SLOT_MS):
        tl.check(any(slot_inputs(s)), f"{name}: key presses in slot {fx.hhmmss(s)} of the typing part")
    tl.check(t - fx.last_input_before(t - 1, keys_only=True) <= 10_000, f"{name}: keyboard input before submit")

# The detectors (SM 4.2). No change event: no typed edit, command, commit,
# permission answer, interrupt, error or test run. Re-roll attempts count
# every earlier near-duplicate, so the third A is attempt 3. The rewrite
# rule compares a turn with the one before, which always edited the other
# file, so it never fires. The answers are read: the next prompt comes 120 s
# after a 110-word answer.
texts = [(SENT[name], text) for name, _, text, _ in PROMPTS]
attempt = dict(zip(names, neardup.attempts(texts, [])))
tl.check([attempt[n] for n in names] == [1, 1, 2, 2, 3], f"attempts {attempt}")
tl.check(neardup.normalize(A) != neardup.normalize(B) and neardup.jaccard(A, B) < 0.1,
         f"A and B are far apart: {neardup.jaccard(A, B)}")
files = [f for _, _, _, f in PROMPTS]
tl.check(all(a != b for a, b in zip(files, files[1:])), "consecutive turns edit different files")
tl.check(not any(e["kind"] in ("editor.activity", "shell.command", "git.change", "agent.permission",
                               "agent.interrupt") for e in events), "no change event")
tl.check(Fraction(1, 4) * 60 * 110 / 238 < 120, "every answer is read before the next prompt")

# The reading bucket (SM 2.4): each turn's 20 s base covers its first 20 s,
# after which the next prompt's lead takes over. A3 is discounted, so its
# lead opens no window and B2's bucket is read from 19:42:00 to 19:45:00;
# A3 then resets the bucket to zero.
bucket = tl.Bucket()
fills = [(e["t"], "reset" if e["kind"] == "prompt.submit" else bucket.amount(e["kind"], e["data"]))
         for e in events if e["kind"] in ("prompt.submit", "agent.output", "agent.tool")]
i = 0
for tau in range(at("19:33:00"), at("19:46:00"), 1000):
    while i < len(fills) and fills[i][0] <= tau:
        t, amount = fills[i]
        if amount == "reset":
            bucket.reset(t, 0 if t == SENT["A3"] else tl.Bucket.BASE_S)
        elif t < SENT["A3"]:
            # A discounted turn adds nothing to the bucket.
            bucket.fill(t, amount)
        i += 1
    if any(0 <= tau - SENT[n] < 20_000 for n in names[:4]) or SENT["B2"] <= tau < SENT["A3"]:
        tl.check(bucket.drain(1) == 1, f"the bucket has something to read at {fx.hhmmss(tau)}")
    if tau >= SENT["A3"]:
        tl.check(bucket.total() == 0, f"A3 resets the bucket to zero: {fx.hhmmss(tau)}")

exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 4.2", evidence="A", witness="witnessed",
                  projects={P: "dashboard"}, start="19:31:00", end="19:46:00")
DERIVED = ["credit", "uncredited", "agent_running_s"]


def row(minute, credit, unc, agent, sm=None):
    exp.row(minute, credit={P: credit} if credit else {}, uncredited=unc, agent=agent, sm=sm, derived=DERIVED)


row("19:31", {"P": 60}, {}, 0)
row("19:32", {"P": 60}, {}, 0)
for name, start in (("A1", "19:33"), ("B1", "19:36"), ("A2", "19:39")):
    row(start, {"P": 40, "SUP": 20}, {}, 60)
    m = at(start)
    row(fx.hhmm(m + 60_000), {"P": 60}, {}, 0)
    row(fx.hhmm(m + 120_000), {"P": 60}, {}, 0)
row("19:42", {"SUP": 60}, {}, 60, sm="4.2, What a discount does: a discounted turn opens no anchor windows")
row("19:43", {"READ": 60}, {}, 0, sm="4.2, What a discount does")
row("19:44", {"READ": 60}, {}, 0, sm="4.2, What a discount does")
row("19:45", None, {"X.reroll": 60}, 60, sm="4.2, Re-roll: alternating A, B, A, B, A is caught at the third A")

expected = exp.doc(
    extra_totals={"credited_s": 840, "excluded_loops_s": 60},
    stated=[],
    approx={},
    errata=[],
    notes=[
        "SM 4.2 states the outcome, not minute rows: the third A is attempt 3 and discounted as X.reroll, the "
        "others are credited. Every row here follows from SM's rules applied to this timeline (README.md).",
        "Each lead is 120 + 60 / 1.5 = 160 s, so it starts 20 s after the prompt before it, and the turn's 20 s "
        "bucket base covers those 20 s as SUP.",
        "A3 is discounted, so its lead from 19:42:20 opens no window and B2's bucket is read instead: SUP while "
        "B2 runs, READ after. A3 resets the bucket to zero, so its own minute is X.reroll.",
        "excluded_loops_s is the X.reroll seconds (SM 5.3).",
    ],
)

anchor_list = []
for name in names:
    note = (f"{name}: attempt {attempt[name]}" + ("; discounted, X.reroll (SM 4.2): it opens no window"
                                                    if attempt[name] >= 3 else "; credited"))
    anchor_list.append(tl.anchor(fx, ev[name], kind="substantive_prompt", origin="human_typed",
                                 prompt_class="substantive", lead_s=160, trail_s=0, lead_from="params", level="A",
                                 sm=note))
turns = [tl.turn(fx, SENT[n], SENT[n] + 60_000, session=S, turn_key=TURN[n], origin="human_typed",
                 prompt_class="substantive", sm=f"{n}, attempt {attempt[n]}") for n in names]
anchors = {"fixture": fx.name, "anchors": anchor_list, "turns": turns}
prompts = {"fixture": fx.name, "prompts": [
    {"turn_key": TURN[n], "t": SENT[n], "local": fx.hhmmss(SENT[n]), "text": text, "len_chars": len(text),
     "source": "invented: SM 4.2 names the prompts A and B without texts"} for n, _, text, _ in PROMPTS]}

fx.write(tl.out_dir(HERE), expected, anchors, prompts)
