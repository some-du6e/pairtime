# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 4.2's fix-it rule, as the decision on
docs/errata/2026-11-19-sm-6.1-same-error.md states it: one error comes back
while the person pastes it, debugs it and waves the agent on. The error
paste and the go-ahead sent at the limit are discounted as X.same_error;
the paste that brings the count to the limit, the hypothesis sent at it,
the paste after a human commit and the go-ahead after a passing run are
credited. See README.md in this directory."""

import os
import sys
from fractions import Fraction

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "6.5"))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "sidedoor"))
sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(HERE))), "tools"))

import ledger_ref  # noqa: E402
import neardup  # noqa: E402
import sidedoor as sd  # noqa: E402
import timeline as tl  # noqa: E402

LOOP_ERRATUM = "2026-11-19-sm-6.5-loop-codes"
# SM 8's values this case uses.
THINK_LEAD_S, COMPOSE_CPS, LOW_LEAD_S, COMMIT_LEAD_S = 120, Fraction(3, 2), 20, 60
SUBSTANTIVE_CHARS, ERROR_PASTE_TEXT_MAX, SAME_ERROR_LIMIT, NOREAD_MIN_WORDS = 20, 40, 3, 100
LOOP_CODES = ("X.reroll", "X.rewrite_loop", "X.same_error", "X.autopilot")  # excluded_loops_s (SM 5.3)
# SM 2.2's go-ahead list, the entries this case types.
GO_AHEADS = ("keep going", "go on")

fx = tl.Fixture(
    "4.2-same-error",
    title="The same error again and again: what the fix-it rule discounts and what it credits",
    date="2026-11-26",
    timezone_name="Europe/Berlin",
    utc_offset_min=60,
    os_name="macos",
    keys=True,
    device=tl.key("d", "4.2-same-error macbook"),
    boot_id=tl.key("b", "4.2-same-error boot"),
    boot="09:12:05.000",
    seq0=31007,
    last_input="15:58:02.600",
    last_key="15:57:41.300",
)
at = fx.at
P = tl.key("p", "4.2-same-error habits")
S = tl.key("s", "4.2-same-error claude session")
TERMINAL = tl.key("a", "4.2-same-error Terminal")
CMD_TEST = tl.key("c", "4.2-same-error pytest tests/test_streaks.py")
CC = dict(harness="claude_code", version="2.1.328", session=S, project=P)

# What he pastes: pytest's report of the one failing test. Two pastes are
# its failure block and the FAILED line pytest prints after it, and one is
# that FAILED line alone. No line of the failure block above it matches E1
# to E6, so the FAILED line, an E4 line, is each paste's error signature
# (ledger-v1.md 10.4, 10.6). Every failing run of the agent's prints the
# same failure block and then the same FAILED line in pytest's short
# summary, so its first E1 to E6 line, and with it its signature, matches
# the pastes', and every paste and every failing run carries one error_sig.
FAILED_LINE = "FAILED tests/test_streaks.py::test_checkin_after_midnight - assert 3 == 4"
SIG = ledger_ref.error_signature(FAILED_LINE)


def sig_key(sig):
    """The fixture's stand-in for es, the keyed signature (10.6)."""
    return tl.key("e", f"4.2-same-error {sig}")


ERR = sig_key(SIG)
BLOCK = "\n" + "\n".join([
    "_______________________ test_checkin_after_midnight _______________________",
    "",
    "    def test_checkin_after_midnight():",
    "        log = CheckinLog(tz=\"Europe/Berlin\")",
    "        for day in (\"2026-11-23\", \"2026-11-24\", \"2026-11-25\"):",
    "            log.add(day + \"T21:30:00+01:00\")",
    "        log.add(\"2026-11-26T00:20:00+01:00\")",
    ">       assert log.streak(\"2026-11-26\") == 4",
    "E       assert 3 == 4",
    "",
    "tests/test_streaks.py:41: AssertionError",
    FAILED_LINE,
])
SUMMARY = "\n" + FAILED_LINE

# (name, sent, what he types, what he pastes after it)
PROMPTS = [
    ("P1", "16:02:00", "make the streak counter treat a check-in made after midnight as part of the new day, okay?",
     None),
    ("P2", "16:05:00", "the streak test still fails here:", BLOCK),
    ("P3", "16:08:00", "maybe today() reads UTC while check-ins keep local time, so compare both in one zone", None),
    ("P4", "16:11:00", "keep going", None),
    ("P5", "16:17:00", "committed the rest, this one's left:", SUMMARY),
    ("P6", "16:20:00", "no luck, still failing the same way:", BLOCK),
    ("P7", "16:24:00", "go on", None),
]
NAMES = [name for name, _, _, _ in PROMPTS]
SENT = {name: at(sent) for name, sent, _, _ in PROMPTS}
TYPED = {name: typed for name, _, typed, _ in PROMPTS}
PASTED = {name: pasted for name, _, _, pasted in PROMPTS}
TEXT = {name: typed + (pasted or "") for name, _, typed, pasted in PROMPTS}
TURN = {name: tl.key("t", f"4.2-same-error turn {name}") for name in NAMES}
COMMIT = at("16:14:36")


def file_key(n):
    return tl.key("f", f"4.2-same-error file {n}")


# Each turn's work, as offsets in seconds from its prompt: ("plan", t, words),
# ("edit", t, file, added, removed), ("test", t_start, t_end, exit code),
# ("final", t, words) and ("stop", t). Every edit is of a file no other turn
# touches, so the rewrite rule never fires, and every answer has under
# NOREAD_MIN_WORDS words, so no turn is unread. P2's and P3's turns change
# code without running the test, so P3 and P4 are judged at the count P2's
# paste reached.
WORK = {
    "P1": [("plan", 5, 30), ("edit", 8, 0, 6, 6), ("test", 10, 22, 1), ("edit", 28, 1, 4, 4), ("test", 30, 42, 1),
           ("final", 56, 45), ("stop", 60)],
    "P2": [("plan", 5, 30), ("edit", 15, 2, 12, 8), ("final", 58, 45), ("stop", 60)],
    "P3": [("plan", 5, 30), ("edit", 15, 3, 12, 8), ("final", 58, 45), ("stop", 60)],
    "P4": [("plan", 5, 30), ("edit", 15, 4, 12, 8), ("test", 20, 40, 1), ("plan", 80, 30), ("edit", 100, 5, 4, 4),
           ("test", 105, 130, 1), ("final", 151, 45), ("stop", 156)],
    "P5": [("plan", 5, 30), ("edit", 10, 6, 4, 4), ("test", 12, 25, 1), ("edit", 30, 7, 4, 4), ("test", 32, 45, 1),
           ("final", 58, 45), ("stop", 60)],
    "P6": [("plan", 5, 30), ("edit", 10, 8, 4, 4), ("test", 12, 25, 0), ("edit", 30, 9, 4, 4), ("test", 32, 45, 1),
           ("plan", 120, 30), ("edit", 150, 10, 6, 6), ("final", 215, 45), ("stop", 220)],
    "P7": [("plan", 5, 30), ("edit", 15, 11, 12, 8), ("test", 20, 40, 0), ("final", 58, 45), ("stop", 60)],
}
STOP = {name: SENT[name] + 1000 * [w for w in WORK[name] if w[0] == "stop"][0][1] for name in NAMES}


def typing_part(name):
    """SM 2.3: a go-ahead's lead has no typing part; any other prompt's is
    its typed length at COMPOSE_CPS. Every one here is under COMPOSE_MAX_S."""
    return Fraction(0) if TYPED[name] in GO_AHEADS else Fraction(len(TYPED[name])) / COMPOSE_CPS


def lead(name):
    return LOW_LEAD_S if TYPED[name] in GO_AHEADS else THINK_LEAD_S + typing_part(name)


# Input. A trackpad move every 10 s while he reads and watches, which keeps
# every slot present. He types each prompt just before he sends it, from a
# little before its lead's typing part, so the keys cover every slot of it
# (SM 2.3); a paste is one more key press, Cmd-V, before Enter. He moves to a
# second Terminal tab to commit and comes back. The OS sees only the key
# presses: a switch between tabs is no focus change (SM 3.2 rule 1), so the
# Terminal window, which hosts the session, stays in front throughout.
fx.pointer("15:58:20.300")                         # clicks the Terminal window
fx.typing("15:58:23.000", "15:58:26.000", 600)     # "claude", Enter
fx.pointer_every("15:58:31.300", "16:25:51.300", 10_000)
for name in NAMES:
    t = SENT[name]
    first = t - max(int(typing_part(name) * 1000), 8_000) - 600
    fx.typing(fx.hhmmss(first), fx.hhmmss(t - 400), 650)
fx.keys_at("16:13:39.900")                         # Cmd-Shift-] to the second tab
fx.typing("16:13:41.000", "16:14:35.600", 700)     # git commit -am "...", Enter
fx.keys_at("16:14:39.900")                         # back to Claude's tab
fx.inputs = [(t + 50 if t % tl.SLOT_MS == 0 else t, k) for t, k in fx.inputs]
fx.ticks("15:58:15", "16:26:00", tl.SLOT_MS)

fx.emit(at("15:58:20.300"), "os.focus", {"app_class": "terminal", "app_hash": TERMINAL}, source="os", via="os",
        sample=True)
fx.emit(at("15:58:27.000"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "acceptEdits"},
        source="hook", via="hook", sample=True, **CC)
fx.emit(at("15:58:27.000"), "os.focus",
        {"app_class": "terminal", "app_hash": TERMINAL, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)

handles = {}
tests = []  # (t_end, exit code, the prompt whose turn ran it)
edits = []  # (file, added, removed)
for name in NAMES:
    t = SENT[name]
    typed, pasted = TYPED[name], PASTED[name]
    data = {"turn_key": TURN[name], "len_chars": len(TEXT[name]), "pasted_chars": len(pasted or ""),
            "typed_len_est": len(typed)}
    if pasted:
        data["error_sig"] = ERR
    data.update({"origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
                 "low_effort": typed in GO_AHEADS})
    handles[name] = fx.emit(t, "prompt.submit", data, source="hook", via="hook", sample=True, **CC)
    for w in WORK[name]:
        kind, off = w[0], w[1]
        if kind in ("plan", "final"):
            fx.emit(t + off * 1000, "agent.output",
                    {"turn_key": TURN[name], "words": w[2], "code_lines": 0, "is_final": kind == "final",
                     "asks_question": False, "offers_options": False},
                    source="log", via="log", **CC)
        elif kind == "edit":
            _, _, n, added, removed = w
            edits.append((file_key(n), added, removed))
            fx.emit(t + off * 1000, "agent.tool",
                    {"tool_class": "edit", "files": [file_key(n)], "lines_added": added, "lines_removed": removed,
                     "visible_output_lines": 0, "t_start": t + off * 1000 - 400, "t_end": t + off * 1000},
                    source="hook", via="hook", sample=True, **CC)
        elif kind == "test":
            _, start, end, code = w
            tests.append((t + end * 1000, code, name))
            tool = {"tool_class": "test", "lines_added": 0, "lines_removed": 0, "cmd_hash": CMD_TEST,
                    "exit_code": code}
            if code:
                tool["error_sig"] = ERR
            tool.update({"visible_output_lines": 4, "t_start": t + start * 1000, "t_end": t + end * 1000})
            fx.emit(t + end * 1000, "agent.tool", tool, source="hook", via="hook", sample=True, **CC)
        elif kind == "stop":
            fx.emit(t + off * 1000, "agent.stop",
                    {"turn_key": TURN[name], "stop_reason": "end_turn", "stop_hook_active": False},
                    source="hook", via="hook", sample=True, **CC)

# The commit, from a second Terminal tab in the project: everything the
# agent changed so far, the six edits of P1's to P4's turns.
committed = edits[:6]
fx.emit(COMMIT, "git.change",
        {"op": "commit", "sha": tl.git_sha("4.2-same-error commit"), "parents": [tl.git_sha("4.2-same-error base")],
         "files_changed": len(committed), "insertions": sum(a for _, a, _ in committed),
         "deletions": sum(r for _, _, r in committed), "actor": "human"},
        source="git", via="git", project=P)

events = fx.build()
built = {id(e["data"]): e for e in events}
ev = {name: built[id(h[4])] for name, h in handles.items()}
commit_ev = fx.event_at("git.change", COMMIT)
WINDOW = (at("16:00:00"), at("16:26:00"))


# Checks of the timing choices (README.md).
def slot_inputs(s):
    return [k for t, k in fx.inputs if s <= t < s + tl.SLOT_MS]


tl.check(all(t % tl.SLOT_MS for t, _ in fx.inputs), "no input on a slot boundary")
for s in range(WINDOW[0], WINDOW[1], tl.SLOT_MS):
    tl.check(slot_inputs(s), f"input in slot {fx.hhmmss(s)}")
lens = {name: len(TYPED[name]) for name in NAMES}
tl.check(lens == {"P1": 90, "P2": 33, "P3": 84, "P4": 10, "P5": 36, "P6": 36, "P7": 5}, f"typed lengths {lens}")
tl.check(all(int(typing_part(n)) == typing_part(n) for n in NAMES), "every typing part is whole seconds")
for name in NAMES:
    t = SENT[name]
    first = t - int(typing_part(name) * 1000)
    for s in range(first - first % tl.SLOT_MS, t, tl.SLOT_MS):
        tl.check(any(slot_inputs(s)), f"{name}: key presses in slot {fx.hhmmss(s)} of the typing part")
    tl.check(t - fx.last_input_before(t - 1, keys_only=True) <= 10_000, f"{name}: keyboard input before submit")
s = COMMIT - COMMIT % tl.SLOT_MS
tl.check(any(slot_inputs(s)), "key presses in the commit's slot (SM 2.3)")
tl.check(not any(e["kind"] in ("editor.activity", "shell.command", "agent.permission", "agent.interrupt",
                               "agent.queue") for e in events), "no human change event but the commit")
tl.check([e["t"] for e in events if e["kind"] == "git.change"] == [COMMIT], "one commit")
tl.check(not any(e["kind"] == "agent.output" and e["data"]["asks_question"] for e in events), "no agent question")

# Each prompt's error_sig is the key of its text's error signature, computed
# with ledger-v1.md 10.4's and 10.6's rules by tools/ledger_ref.py: the
# FAILED line for each paste, and none for a prompt without one.
tl.check(ledger_ref.error_pattern(ledger_ref.strip_space(FAILED_LINE)) == 4, "the FAILED line matches E4")
tl.check(SIG == "FAILED <path> - assert <n> == <n>", f"the signature is {SIG!r}")
tl.check(ledger_ref.error_signature(BLOCK.removesuffix("\n" + FAILED_LINE)) is None,
         "no line of the failure block above the FAILED line matches E1 to E6")
for name in NAMES:
    sig = ledger_ref.error_signature(TEXT[name])
    tl.check((sig is not None) == (PASTED[name] is not None), f"{name}: a signature exactly when it pastes")
    tl.check(ev[name]["data"].get("error_sig") == (sig_key(sig) if sig else None),
             f"{name}: error_sig is the key of its text's signature {sig!r}")
tl.check(all(e["data"].get("error_sig") == (ERR if e["data"].get("exit_code") else None)
             for e in events if e["kind"] == "agent.tool"),
         "every failing run carries the FAILED line's key, and no other tool record carries one")

# SM 2.2's classes. Each paste carries pasted text, typed words that aren't
# a go-ahead and fewer than ERROR_PASTE_TEXT_MAX typed characters, so it is
# an error_paste; P6's 36 are at least SUBSTANTIVE_CHARS, so a scorer that
# missed its signature would class it substantive. The go-aheads are on SM
# 2.2's list. The other two are substantive.
CLASS = {}
for name in NAMES:
    if TYPED[name] in GO_AHEADS:
        CLASS[name] = "low_effort"
    elif PASTED[name]:
        tl.check(len(TYPED[name]) < ERROR_PASTE_TEXT_MAX, f"{name}: fewer than 40 typed characters")
        CLASS[name] = "error_paste"
    else:
        tl.check(len(TYPED[name]) >= SUBSTANTIVE_CHARS, f"{name}: substantive")
        CLASS[name] = "substantive"
tl.check(SUBSTANTIVE_CHARS <= len(TYPED["P6"]) < ERROR_PASTE_TEXT_MAX, "P6 has 20 to 39 typed characters")
ORIGIN = {name: "human_pasted" if PASTED[name] else "human_typed" for name in NAMES}


# SM 4.2's fix-it count, as decided: occurrences of e since the later of the
# last human change event in the project and the last time e was cleared. A
# failing run of the agent's and an error pasted in a prompt count; the
# commit sets the count to 0; a run of the test command that passes after a
# failing one is a run_green, which clears e. A turn is judged by the count
# before its prompt.
def counts_before():
    marks = [(e["t"], "fail") for e in events if e["kind"] == "agent.tool" and e["data"].get("error_sig") == ERR]
    marks += [(e["t"], "paste") for e in events if e["kind"] == "prompt.submit" and e["data"].get("error_sig") == ERR]
    marks += [(COMMIT, "human change")]
    last = None
    for t_end, code, _ in tests:
        if code == 0 and last == 1:
            marks.append((t_end, "run_green"))
        last = code
    marks.sort()
    out, n, i = {}, 0, 0
    for name in NAMES:
        while i < len(marks) and marks[i][0] < SENT[name]:
            n = 0 if marks[i][1] in ("human change", "run_green") else n + 1
            i += 1
        out[name] = n
        # Its own paste counts toward later turns, not its own.
        while i < len(marks) and marks[i][0] == SENT[name]:
            n += 1
            i += 1
    return out


COUNT = counts_before()
tl.check(COUNT == {"P1": 0, "P2": 2, "P3": 3, "P4": 3, "P5": 0, "P6": 3, "P7": 1}, f"counts {COUNT}")
DISCOUNTED = {n for n in NAMES if CLASS[n] in ("error_paste", "low_effort") and COUNT[n] >= SAME_ERROR_LIMIT}
tl.check(DISCOUNTED == {"P4", "P6"}, f"discounted {DISCOUNTED}")
# The go-ahead and the paste that are discounted are judged at exactly the
# limit, so a scorer that compared with > instead of >= would credit both.
tl.check({n for n in DISCOUNTED if COUNT[n] == SAME_ERROR_LIMIT} == {"P4", "P6"}
         and {CLASS["P4"], CLASS["P6"]} == {"low_effort", "error_paste"}, "a go-ahead and a paste at the limit")
tl.check([(fx.hhmmss(t), c) for t, c, _ in tests if c == 0] == [("16:20:25", 0), ("16:24:40", 0)],
         "the passing runs")

# Keep the other detectors out (SM 4.2). Re-roll: no prompt is a
# near-duplicate of an earlier one since the last change event. Pasted text
# is removed before comparing, so only the typed words are compared; the
# change events are e's first appearance, the commit and each run whose
# outcome differs from the run before it, at 16:20:25, 16:20:45 and 16:24:40.
first_fail = min(t for t, c, _ in tests if c)
flips = [t for (t, c, _), (_, c0, _) in zip(tests[1:], tests) if c != c0]
tl.check([fx.hhmmss(t) for t in flips] == ["16:20:25", "16:20:45", "16:24:40"], "the runs whose outcome changed")
changes = [first_fail, COMMIT] + flips
attempt = dict(zip(NAMES, neardup.attempts([(SENT[n], TYPED[n]) for n in NAMES], changes)))
tl.check(all(a == 1 for a in attempt.values()), f"attempts {attempt}")
tl.check(not neardup.far_apart([TYPED[n] for n in NAMES]), f"close pairs {neardup.far_apart([TYPED[n] for n in NAMES])}")
# Rewrite: every edit is of a file no other edit touches.
tl.check(len({f for f, _, _ in edits}) == len(edits), "every edit is of its own file")
# Autopilot: no turn is unread.
tl.check(all(e["data"]["words"] < NOREAD_MIN_WORDS for e in events if e["kind"] == "agent.output"), "no unread turn")


# The rows: SM 2.7 second by second, scored twice (SM 4.2, What a discount
# does). The first scoring discounts P4 and P6: their turns open no window,
# reset the bucket to zero and add nothing to it, and they count as no
# human anchor for reading. The second scores the same records with no
# turn discounted. A second the second scoring credits and the first leaves
# uncredited is a second the loop cost, under the latest discounted turn's
# code, X.same_error.
def amount(data, kind):
    return tl.Bucket().amount(kind, data)


def scoring(discount):
    sw = sd.Sweep(WINDOW[0], WINDOW[1])
    for name in NAMES:
        t = SENT[name]
        off = discount and name in DISCOUNTED
        if not off:
            sw.anchor(t, lead_s=lead(name), typing_part_s=typing_part(name),
                      reason="GO" if CLASS[name] == "low_effort" else "P", level="A",
                      typing_ok=lambda slot: sd.keys_in_slot(fx, slot))
        sw.reset(t, 0 if off else sd.R_TURN_BASE_S)
        if not off:
            for e in events:
                if e["session_key"] == S and SENT[name] < e["t"] <= STOP[name] and e["kind"] in ("agent.output",
                                                                                                 "agent.tool"):
                    sw.fill(e["t"], amount(e["data"], e["kind"]))
        sw.run(t, STOP[name])
    sw.anchor(COMMIT, lead_s=COMMIT_LEAD_S, typing_part_s=0, reason="G", level="A")
    return {t: (why, level) for t, why, level in sw.seconds()}


real, whole = scoring(True), scoring(False)
# Each credited anchor's lead: the prompts that aren't discounted, and the
# commit. SM 4.2 says such a lead never reaches back past the stop of the
# most recent discounted turn. Every lead here starts at or after it, so the
# sweep needn't cut one.
CREDITED_LEADS = [(n, SENT[n] - lead(n) * 1000, SENT[n]) for n in NAMES if n not in DISCOUNTED]
CREDITED_LEADS.append(("the commit", COMMIT - COMMIT_LEAD_S * 1000, COMMIT))
for who, start, t in CREDITED_LEADS:
    before = [STOP[n] for n in DISCOUNTED if SENT[n] < t]
    if before:
        tl.check(start >= max(before), f"{who}'s lead doesn't reach back past the discounted turn's stop (SM 4.2)")
# A discounted prompt's loop runs from it to the first credited lead after it.
LOOP_SPANS = {d: (SENT[d], min(start for _, start, _ in CREDITED_LEADS if start > SENT[d])) for d in DISCOUNTED}
minutes = {}
loop = []
for t in range(WINDOW[0], WINDOW[1], 1000):
    why, level = real[t]
    if level is None and whole[t][1] is not None:
        why = "X.same_error"
        loop.append(t)
    m = minutes.setdefault(t - t % tl.MINUTE_MS, {"credit": {}, "uncredited": {}, "agent": 0})
    tl.check(level in (None, "A"), "every credited second is level A")
    side = "credit" if level else "uncredited"
    minutes[t - t % tl.MINUTE_MS][side][why] = minutes[t - t % tl.MINUTE_MS][side].get(why, 0) + 1
    if any(SENT[n] <= t < STOP[n] for n in NAMES):
        m["agent"] += 1
tl.check(loop == [t for t in range(WINDOW[0], WINDOW[1], 1000) if any(a <= t < b for a, b in LOOP_SPANS.values())],
         f"X.same_error is every second from P4 and from P6 to the next credited lead {LOOP_SPANS}")
tl.check(all(real[t][0] == "X.agent_only" for t in loop),
         "every loop second is inside P4's or P6's turn, where SM 5.2's wording would give X.agent_only")

exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 4.2", evidence="A", witness="witnessed",
                  projects={P: "habits"}, start="16:00:00", end="16:26:00")
DERIVED = ["credit", "uncredited", "agent_running_s"]
ROWS = [
    ("16:00", {"P": 60}, {}, 0, None),
    ("16:01", {"P": 60}, {}, 0, None),
    ("16:02", {"P": 22, "SUP": 38}, {}, 60, None),
    ("16:03", {"P": 60}, {}, 0, None),
    ("16:04", {"P": 60}, {}, 0, None),
    ("16:05", {"P": 56, "SUP": 4}, {}, 60, "4.2: P2 is an error paste with the count at 2, under the limit"),
    ("16:06", {"P": 60}, {}, 0, None),
    ("16:07", {"P": 60}, {}, 0, None),
    ("16:08", {"SUP": 60}, {}, 60, None),
    ("16:09", {"READ": 60}, {}, 0, None),
    ("16:10", {"READ": 60}, {}, 0, "4.2, What a discount does: P4's lead opens no window, so P3's bucket is read"),
    ("16:11", {}, {"X.same_error": 60}, 60, "4.2, Fix-it loop: P4 is a go-ahead with the count at 3, the limit"),
    ("16:12", {}, {"X.same_error": 60}, 60, "4.2, Fix-it loop"),
    ("16:13", {"G": 24}, {"X.same_error": 36}, 36, "4.2, Fix-it loop; the commit is a human change event"),
    ("16:14", {"P": 24, "G": 36}, {}, 0, "4.2: the count is 0 after the commit, so P5's paste is credited"),
    ("16:15", {"P": 60}, {}, 0, None),
    ("16:16", {"P": 60}, {}, 0, None),
    ("16:17", {"SUP": 60}, {}, 60, "4.2, What a discount does: P6's lead opens no window, so P5's bucket is read"),
    ("16:18", {"READ": 60}, {}, 0, "4.2, What a discount does"),
    ("16:19", {"READ": 60}, {}, 0, "4.2, What a discount does"),
    ("16:20", {}, {"X.same_error": 60}, 60, "4.2, Fix-it loop: P6 is an error paste with the count at 3, the limit"),
    ("16:21", {}, {"X.same_error": 60}, 60, "4.2, Fix-it loop"),
    ("16:22", {}, {"X.same_error": 60}, 60, "4.2, Fix-it loop"),
    ("16:23", {"GO": 20}, {"X.same_error": 40}, 40,
     "4.2, Fix-it loop; the passing run at 16:20:25 cleared e, so P7's count is 1 and its lead is credited"),
    ("16:24", {"SUP": 60}, {}, 60, None),
    ("16:25", {"READ": 60}, {}, 0, None),
]
for minute, credit, unc, agent, sm in ROWS:
    got = minutes[at(minute)]
    tl.check(got == {"credit": credit, "uncredited": unc, "agent": agent},
             f"{minute}: the row says {credit} {unc} {agent}, the scorings give {got}")
    # Every row whose seconds carry a discount code rests on the loop-codes
    # erratum: the discounted turn's agent runs and nothing credits a second,
    # so SM 5.2's wording gives X.agent_only where SM 4.2's "reported under
    # their discount code" gives X.same_error.
    erratum = LOOP_ERRATUM if any(c in LOOP_CODES for c in unc) else None
    exp.row(minute, credit={P: credit} if credit else {}, uncredited=unc, agent=agent, sm=sm, derived=DERIVED,
            erratum=erratum)
tl.check(len(ROWS) == (WINDOW[1] - WINDOW[0]) // tl.MINUTE_MS, "a row a minute")
tl.check(all(m["credit"] or m["agent"] for m in minutes.values()),
         "every minute has credit or a running agent, so each has a bucket that reports its loop seconds (SM 5.1)")

excluded = sum(n for r in exp.rows for c, n in (r["uncredited"] or {}).items() if c in LOOP_CODES)
tl.check(excluded == len(loop) == 376, f"excluded_loops_s {excluded}")
expected = exp.doc(
    extra_totals={"credited_s": 1184, "excluded_loops_s": excluded},
    stated=[],
    approx={},
    errata=[LOOP_ERRATUM],
    notes=[
        "SM 4.2 states the rule, not minute rows. Every row here follows from SM's rules applied to this timeline "
        "(README.md), as the decision on docs/errata/2026-11-19-sm-6.1-same-error.md states them.",
        "The count of the error before each prompt is 0, 2, 3, 3, 0, 3 and 1. P4, a go-ahead, and P6, an error "
        "paste, come at SAME_ERROR_LIMIT and are X.same_error. P2 brings the count to the limit and is credited, "
        "P3 is substantive and never discounted, the commit at 16:14:36 sets the count to 0 before P5, P5's paste "
        "and its turn's two failing runs bring it back to the limit before P6, and the passing run at 16:20:25 "
        "clears the error before it comes back once ahead of P7.",
        "P4's and P6's leads open no window, so P3's bucket is read through P4's lead, 16:10:40 to 16:11:00, and "
        "P5's through P6's, 16:17:36 to 16:20:00. P4 and P6 reset the bucket to zero and add nothing to it, so "
        "nothing is credited from 16:11:00 until the commit's lead at 16:13:36, P4's stop, or from 16:20:00 until "
        "P7's lead at 16:23:40.",
        "excluded_loops_s is the X.same_error seconds (SM 5.3): the seconds that scoring the same records with no "
        "turn discounted credits and this scoring doesn't, 16:11:00 to 16:13:36 and 16:20:00 to 16:23:40.",
        "Rows 16:11 to 16:13 and 16:20 to 16:23 name 6.5's loop-codes erratum: inside a discounted turn, where "
        "its agent runs and nothing credits a second, SM 5.2's wording gives X.agent_only, and SM 4.2's "
        "\"reported under their discount code\" gives X.same_error. The rows give X.same_error.",
    ],
)

NOTE = {
    "P1": "count 0: credited",
    "P2": "an error paste with the count at 2, under SAME_ERROR_LIMIT: credited; its paste brings the count to 3",
    "P3": "the count is 3, the limit, but a substantive prompt is never discounted: credited",
    "P4": "a go-ahead with the count at 3, the limit: X.same_error (SM 4.2), so it opens no window",
    "P5": "an error paste with the count at 0, after the commit: credited; its paste and its turn's two failing "
          "runs bring the count back to 3",
    "P6": "an error paste with the count at 3, the limit: X.same_error (SM 4.2), so it opens no window",
    "P7": "a go-ahead with the count at 1: the passing run at 16:20:25 cleared the error, which came back once",
}
anchor_list = []
for name in NAMES:
    kind = "go_ahead" if CLASS[name] == "low_effort" else "substantive_prompt"
    lead_s = lead(name)
    anchor_list.append(tl.anchor(fx, ev[name], kind=kind, origin=ORIGIN[name], prompt_class=CLASS[name],
                                 lead_s=int(lead_s), trail_s=0, lead_from="params", level="A",
                                 sm=f"{name}: {NOTE[name]}"))
    if name == "P4":
        anchor_list.append(tl.anchor(fx, commit_ev, kind="human_commit", lead_s=COMMIT_LEAD_S, trail_s=0,
                                     lead_from="params", level="A",
                                     sm="a human commit with key presses in its slot: a human change event, so the "
                                        "error's count goes back to 0 (SM 4.2)"))
turns = [tl.turn(fx, SENT[n], STOP[n], session=S, turn_key=TURN[n], origin=ORIGIN[n], prompt_class=CLASS[n],
                 sm=f"{n}, count {COUNT[n]}" + (", X.same_error" if n in DISCOUNTED else ""))
         for n in NAMES]
anchors = {"fixture": fx.name, "anchors": anchor_list, "turns": turns}
prompts = {"fixture": fx.name, "prompts": [
    {"turn_key": TURN[n], "t": SENT[n], "local": fx.hhmmss(SENT[n]), "text": TEXT[n], "len_chars": len(TEXT[n]),
     "source": "invented: SM 4.2 quotes no prompt for the fix-it rule" + (
         "; the typed words, then the pasted pytest output" if PASTED[n] else "")} for n in NAMES]}

fx.write(tl.out_dir(HERE), expected, anchors, prompts)
