# SPDX-License-Identifier: AGPL-3.0-or-later
"""T3 Code messages sent from T3's phone app to a Windows PC where a mouse
jiggler moves the pointer and T3's window, which hosts the session, stays in
front. SM 2.2 rules 6 and 7, tested at each stamp, would make the prompts
human_typed at level B, but a message whose record says client mobile skips
them, so both prompts are relayed at level C. See README.md in this
directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(HERE)), "sidedoor"))

import relayed as rl  # noqa: E402
import sidedoor as sd  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/t3-phone"
fx = tl.Fixture(
    NAME,
    title="T3 Code messages sent from its phone app to a PC with a jiggler and T3's window in front",
    date="2026-11-28",
    timezone_name="Europe/Madrid",
    utc_offset_min=60,
    os_name="windows",
    keys=False,
    device=tl.key("d", "relayed t3 phone pc"),
    boot_id=tl.key("b", "relayed t3 phone pc boot"),
    boot="09:12:30.000",
    seq0=27904,
    # The jiggler's move before the records below start.
    last_input="12:54:56.900",
)
at = fx.at
P = tl.key("p", "relayed t3-phone tide-tables")
S = tl.key("s", "relayed t3-phone session")
THREAD = tl.key("r", "relayed t3-phone thread")
APP = tl.key("a", "relayed t3-phone T3 Code desktop app")
ENV = dict(harness="claude_code", version="2.1.334", session=S, project=P)
TURN = {n: tl.key("t", f"relayed t3-phone {n}") for n in ("T1", "T2")}
FILE = {"tides.py": tl.key("f", "relayed t3-phone tides.py")}

# SM 8.1 values the checks of rules 6 and 7 read that sidedoor.py doesn't
# carry.
TYPING_MAX_CPS = 15
SLEEP_GAP_S = 30


def hook(t, kind, data):
    """A hook record. The agent takes a presence sample at each one (DESIGN
    3.3)."""
    return fx.emit(at(t), kind, data, source="hook", via="hook", sample=True, **ENV)


def prompt(t, turn, length):
    return hook(t, "prompt.submit", {"turn_key": TURN[turn], "len_chars": length, "pasted_chars": 0,
                                     "typed_len_est": length, "origin_declared": "none",
                                     "matches_pending_cron": False, "answers_question": False,
                                     "low_effort": False})


def relay(t, sent, length):
    """The T3 reader's record of a user-role message T3 stores with
    createdBy user and creationSource mobile, its phone app. Its prompt is
    filled in once the events are built, since the prompt.submit it names
    came first."""
    return fx.emit(at(t), "relay.message", {"app": "t3_code", "client": "mobile", "sent_at": at(sent),
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


# Mateo is away from the PC all afternoon. A USB mouse jiggler, which keeps
# it from sleeping while agents run, moves the pointer every 12 s, at :08.9,
# :20.9, :32.9, :44.9 and :56.9 of every minute: input in every slot, never
# on a slot boundary, and 3.1 s before every whole minute. Windows without
# the key counter reports it as any input, so idle_keys_s is null.
fx.pointer_every("12:55:08.900", "13:14:56.900", 12_000)
fx.ticks("12:56:10", "13:15:00", tl.SLOT_MS)

# T3's desktop app runs its own server, which starts Claude Code for the
# thread Mateo opened in T3's phone app: no TTY, and T3's server for its
# nearest non-shell ancestor. T3's window is in front and hosts the session
# (SM 3.2 rule 3), so the agent records the window again with the session in
# hosted_sessions. It stays in front to the end.
hook("12:56:10", "session.start", {"start_kind": "startup", "entrypoint": "unknown", "parent_class": "agent_app",
                                   "has_tty": False, "permission_mode": "default"})
fx.emit(at("12:56:10"), "os.focus",
        {"app_class": "agent_app", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)

# M1, typed in T3's phone app and sent at 13:03:00. T3 hands it to the agent
# a second later; the reader sees the row two seconds after that.
link = []
p1 = prompt("13:03:01", "T1", 90)
r1 = relay("13:03:03", "13:03:00", 90)
link.append((r1, p1))
output("13:03:30", "T1", 45)
edit("13:04:30", "13:04:28", "tides.py", 6, 2)
output("13:06:00", "T1", 30, final=True)
stop("13:06:00", "T1")

# M2, from the phone again at 13:10:00, 240 s after T1 stopped.
p2 = prompt("13:10:01", "T2", 48)
r2 = relay("13:10:03", "13:10:00", 48)
link.append((r2, p2))
output("13:10:40", "T2", 30)
output("13:12:00", "T2", 60, final=True)
stop("13:12:00", "T2")

events = fx.build()
# Each relay record names the prompt.submit that came before it.
pairs = []
for r, p in link:
    re = fx.event_at("relay.message", r[0], sent_at=r[4]["sent_at"])
    pe = fx.event_at("prompt.submit", p[0], turn_key=p[4]["turn_key"])
    re["data"]["prompt"] = pe["event_id"]
    pairs.append((re, pe))

T0, END = at("13:00:00"), at("13:15:00")

# The PC's own evidence. Relayed credit reads none of it (SM 2.3) and Replay
# ignores it, so the case checks here that no presence or focus code can
# apply: input in every slot, no lock, no sleep and no gap in the samples,
# and T3's window, which hosts the session and resolves to the project, in
# front from before the window to its end.
tl.check(sd.input_in_every_slot(fx, T0, END), "input in every slot")
sd.check_inputs_off_boundaries(fx)
samples = [e for e in events if e["kind"] == "os.presence"]
tl.check(all(e["data"]["idle_keys_s"] is None and not e["data"]["locked"] for e in samples),
         "no key counter and no lock in any sample")
tl.check(not fx.locks and not any(e["kind"] == "os.power" for e in events), "no lock, suspend or resume")
ts = [e["t"] for e in samples if T0 - tl.SLOT_MS <= e["t"] <= END]
tl.check(ts[0] <= T0 and ts[-1] == END and all(b - a <= SLEEP_GAP_S * 1000 for a, b in zip(ts, ts[1:])),
         "samples through the window, none more than SLEEP_GAP_S apart")
focus = [e for e in events if e["kind"] == "os.focus" and e["t"] < END]
before = [e for e in focus if e["t"] <= T0]
tl.check(before, "a window is in front when the window starts")
for e in before[-1:] + [e for e in focus if e["t"] > T0]:
    d = e["data"]
    tl.check(d["app_class"] == "agent_app" and d.get("hosted_sessions") == [S] and d.get("focus_project") == P,
             f"T3's window, hosting the session, is in front from {fx.hhmmss(e['t'])}")


def input_slots(a, b):
    """The number of slots that overlap [a, b) and have input."""
    n, s = 0, a - a % tl.SLOT_MS
    while s < b:
        n += any(s <= u < s + tl.SLOT_MS for u, _ in fx.inputs)
        s += tl.SLOT_MS
    return n


def check_rule6_at_stamp(re, pe):
    """What SM 2.2 rules 6 and 7, tested at the app's stamp, would see for a
    witnessed prompt, as t3-here's gen.py checks it: input in the
    KEYS_PRE_S before sent_at (input_any, since this PC has no key counter),
    in the inputs and in the samples a scorer reads; focus at sent_at on a
    window that hosts the session; and a plausible typed length over the
    lead rule 6 would give the prompt. With pasted_chars 0 that is rule 6,
    human_typed. Only the record's client keeps the prompt from it."""
    sa = re["data"]["sent_at"]
    when = fx.hhmmss(sa)
    pre = sd.KEYS_PRE_S * 1000
    tl.check(any(sa - pre <= t < sa for t, _ in fx.inputs), f"input in the {sd.KEYS_PRE_S} s before {when}")
    tl.check(sd.samples_show_input_before(fx, sa, pre), f"a sample shows the input before {when}")
    front = [e for e in events if e["kind"] == "os.focus" and e["t"] <= sa][-1]["data"]
    tl.check(front["app_class"] == "agent_app" and pe["session_key"] in front["hosted_sessions"],
             f"T3's window, in front at {when}, hosts the session")
    lead = sd.THINK_LEAD_S * 1000 + rl.typing_ms(pe["data"]["typed_len_est"])
    active_s = 15 * input_slots(sa - lead, sa)
    tl.check(pe["data"]["typed_len_est"] <= TYPING_MAX_CPS * active_s,
             f"{pe['data']['typed_len_est']} characters are plausible over {active_s} s of input before {when}")
    tl.check(pe["data"]["pasted_chars"] == 0, f"nothing pasted at {when}, so rule 6, not rule 7")
    tl.check(re["data"]["client"] == "mobile", f"the record of the message sent at {when} says T3's phone app")
    return active_s


ACTIVE = [check_rule6_at_stamp(re, pe) for re, pe in pairs]
tl.check(ACTIVE == [180, 165], f"active input seconds over each lead rule 6 would give: {ACTIVE}")

# The rows, by hand from SM 2.2 rule 9, 2.3, 2.4 and 5.2.
ER = rl.ERRATUM
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rules 6, 7 and 9, 2.3, 2.4, 5.2", evidence="C",
                  witness="witnessed", projects={P: "tide-tables"}, start="13:00:00", end="13:15:00")
LEAD = "2.3: a relayed message's lead is its typing time, ending at sent_at"
READ = "2.4: a relayed session reads after a stop, until its next relayed message within 600 s, 120 s at most"
EMPTY = "5.1: no credit and no agent running"
RUNS = "2.4: no relayed session reads while its agent runs"


def row(minute, credit=None, unc=None, agent=0, sm=None, note=None, bucket=True, end=None):
    exp.row(minute, end, credit=credit, uncredited=unc, agent=agent, sm=sm,
            derived=DERIVED if bucket else None, note=note, bucket=bucket)


row("13:00", bucket=False, sm=EMPTY,
    note="The jiggler's input and T3's window in front open no window: mouse-only presence never does (2.1), and a "
         "relayed lead is typing time only (2.3). With client web, M1's think part would credit this minute.")
row("13:01", bucket=False, sm=EMPTY)
row("13:02", {P: {"P": 60}}, {}, sm=LEAD,
    note="M1's 90 characters type in 90 / 1.5 = 60 s, from 13:02:00 to its stamp at 13:03:00. It is the thread's "
         "first message, so nothing else caps the lead.")
row("13:03", {}, {"X.no_anchor": 1, "X.agent_only": 59}, agent=59,
    sm="2.2 rule 9: M1's record says client mobile, so rules 6 and 7 are skipped and the prompt is relayed",
    note="Rules 6 and 7 at the stamp would see the jiggler's input at 13:02:56.9 and T3's window, hosting the "
         "session, in front. T3 hands M1 to the agent at 13:03:01, so 13:03:00 is in neither the lead nor a turn. "
         "T1 runs from 13:03:01.")
row("13:04", {}, {"X.agent_only": 60}, agent=60, sm=RUNS)
row("13:05", {}, {"X.agent_only": 60}, agent=60, sm=RUNS)
row("13:06", {P: {"READ": 60}}, {}, sm=READ,
    note="T1 stops at 13:06:00. M2 is sent at 13:10:00, 240 s later, and pairs at 13:10:03, 243 s after the stop, "
         "inside PENDING_WINDOW_S - 60 s = 840 s, so the session reads from the stop. T1 filled "
         "20 + 15 + 60 + 10 = 105 s, under the 120 s cap.")
row("13:07", {P: {"READ": 45}}, {"X.no_anchor": 15}, sm=READ,
    note="The bucket empties at 13:07:45, before the reading would end at M2's stamp, so no second is "
         "X.bucket_cap.")
row("13:08", bucket=False, sm=EMPTY)
row("13:09", {P: {"P": 32}}, {"X.no_anchor": 28}, sm=LEAD,
    note="M2's 48 characters: 32 s, from 13:09:28. M1 was sent 420 s earlier, so the cap at the seconds since the "
         "thread's previous message doesn't bind.")
row("13:10", {}, {"X.no_anchor": 1, "X.agent_only": 59}, agent=59,
    sm="2.2 rule 9: M2's record says client mobile too",
    note="Rule 6 at the stamp would again hold, with the jiggler's input at 13:09:56.9. T2 runs from 13:10:01, and "
         "its prompt resets the bucket to the 20 s base.")
row("13:11", {}, {"X.agent_only": 60}, agent=60, sm=RUNS)
row("13:12", bucket=False, sm=EMPTY,
    note="T2 stops at 13:12:00 and no relayed message follows, so nothing reads its 20 + 10 + 20 = 50 s.")
row("13:13", end="13:15", bucket=False, sm=EMPTY)

rp = rl.Replay(events, T0, END, desktop=True)
rl.check_rows(exp, rp, {S: P})
for re, pe in pairs:
    tl.check(rp.origin[pe["event_id"]] == "relayed", f"the prompt at {fx.hhmmss(pe['t'])} is relayed")
tl.check([(a["sent_at"], a["lead_ms"]) for a in rp.anchors] == [(at("13:03:00"), 60_000), (at("13:10:00"), 32_000)],
         "the leads are M1's 60 s and M2's 32 s of typing")
tl.check(rp.spans == {S: [(at("13:06:00"), at("13:10:00"))]}, f"T1's stop reads until M2 is sent: {rp.spans}")
tl.check(not getattr(rp, "late", None), "no pairing comes too late to vouch for a reading")
tl.check(not rp.capped, "no second is X.bucket_cap")

totals = exp.totals()
tl.check(totals["credited_s"] == 197, f"credited {totals['credited_s']}")
tl.check(totals["credited_by_reason"] == {"P": 92, "READ": 105}, f"by reason {totals['credited_by_reason']}")
expected = exp.doc(
    extra_totals={"level_c_s": 197, "via_relay_s": 197, "relayed_prompts": 2, "typed_prompts": 0},
    stated=[],
    approx={},
    errata=[ER],
    notes=[
        "No SM section describes this case. Every row comes from SM 2.2 rules 4, 6, 7 and 9, 2.3, 2.4 and 5.2 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them. Every credited second is level C with via_relay, "
        "and witnessed.",
        "A mouse jiggler gives input in every slot, so every slot is present by input_any; the PC has no key counter "
        "and idle_keys_s is null. T3 Code's window, which hosts the session and resolves to tide-tables, is in front "
        "throughout. No presence or focus code applies, so an uncredited second is X.agent_only while a turn runs "
        "and X.no_anchor otherwise. T1 fills 105 s, under the 120 s cap, so none is X.bucket_cap.",
        "Rules 6 and 7, tested at each stamp, would hold: the jiggler's input 3.1 s before it, T3's window hosting "
        "the session in front at it, and a plausible length. Both records say client mobile, T3's phone app, so both "
        "prompts skip rules 6 and 7 and are relayed (SM 2.2). relayed_prompts counts them; typed_prompts counts the "
        "human_typed prompts, of which there are none.",
        "With client web in both records, both prompts would be human_typed at level B, and the case would credit "
        "487 s, P 332, SUP 125 and READ 30, where it credits 197 s at level C. README.md works it out minute by "
        "minute.",
    ],
)

# The counterfactual the case exists to show: the same records with client
# web. Rules 6 and 7 then read the stamps, rule 6 holds there (checked
# above), and both prompts are human_typed, at level B on a PC with input_any
# only (SM 5.1). Each lead is 2.3's substantive prompt row, think part and
# typing part, ending at the stamp, and with no key counter the whole lead
# needs presence only. Reading follows 2.4's presence rules: SUP while the
# turn runs, READ after, up to BUCKET_MAX_S, within READ_HORIZON_S of a human
# anchor. By hand, minute by minute, then checked against sidedoor's Sweep,
# which takes presence and focus on the project as given, as checked above.
CF = {
    "13:00": ({"P": 60}, {}, 0),
    "13:01": ({"P": 60}, {}, 0),
    "13:02": ({"P": 60}, {}, 0),
    "13:03": ({"SUP": 35}, {"X.no_anchor": 1, "X.agent_only": 24}, 59),
    "13:04": ({"SUP": 30}, {"X.agent_only": 30}, 60),
    "13:05": ({"SUP": 30}, {"X.agent_only": 30}, 60),
    "13:06": ({"READ": 10}, {"X.no_anchor": 50}, 0),
    "13:07": ({"P": 32}, {"X.no_anchor": 28}, 0),
    "13:08": ({"P": 60}, {}, 0),
    "13:09": ({"P": 60}, {}, 0),
    "13:10": ({"SUP": 30}, {"X.no_anchor": 1, "X.agent_only": 29}, 59),
    "13:11": ({}, {"X.agent_only": 60}, 60),
    "13:12": ({"READ": 20}, {"X.no_anchor": 40}, 0),
    "13:13": None,  # no credit and no agent: no bucket
    "13:14": None,
}
tl.check(list(CF) == [fx.hhmm(m) for m in range(T0, END, tl.MINUTE_MS)], "the counterfactual covers the window")
for minute, w in CF.items():
    if w:
        tl.check(sum(w[0].values()) + sum(w[1].values()) == 60, f"counterfactual {minute} adds up to 60 s")

cf = sd.Sweep(T0, END)
for re, pe in pairs:
    typing = rl.typing_ms(pe["data"]["typed_len_est"]) // 1000
    cf.anchor(re["data"]["sent_at"], lead_s=sd.THINK_LEAD_S + typing, typing_part_s=typing, reason="P", level="B")
    cf.reset(pe["t"], rl.R_TURN_BASE_S)
for e in events:
    if e["kind"] in ("agent.output", "agent.tool") and e["session_key"] == S:
        cf.fill(e["t"], rl.fill_s(e["kind"], e["data"]))
for _, a, b, _ in rp.turns:
    cf.run(a, b)
got = cf.minutes()
for m in range(T0, END, tl.MINUTE_MS):
    g, w = got[m], CF[fx.hhmm(m)]
    if w is None:
        tl.check(not g["credit"] and not g["agent"], f"counterfactual {fx.hhmm(m)}: the sweep credits {g['credit']}")
        continue
    tl.check((g["credit"], g["uncredited"], g["agent"]) == w,
             f"counterfactual {fx.hhmm(m)}: by hand {w}, the sweep {g['credit'], g['uncredited'], g['agent']}")
tl.check(sd.levels(cf) == {"A": 0, "B": 487, "C": 0}, f"counterfactual levels {sd.levels(cf)}")

CF_TOTALS = {"credited_s": 0, "level_b_s": 0, "via_relay_s": 0, "agent_running_s": 0, "typed_prompts": 2,
             "relayed_prompts": 0}
for w in CF.values():
    if w:
        for k, v in list(w[0].items()) + list(w[1].items()):
            CF_TOTALS[k] = CF_TOTALS.get(k, 0) + v
        CF_TOTALS["credited_s"] += sum(w[0].values())
        CF_TOTALS["agent_running_s"] += w[2]
CF_TOTALS["level_b_s"] = CF_TOTALS["credited_s"]
tl.check({k: CF_TOTALS[k] for k in ("credited_s", "P", "SUP", "READ", "X.agent_only", "X.no_anchor")} ==
         {"credited_s": 487, "P": 332, "SUP": 125, "READ": 30, "X.agent_only": 173, "X.no_anchor": 120},
         f"counterfactual totals {CF_TOTALS}")
tl.check(CF_TOTALS["agent_running_s"] == totals["agent_running_s"], "the agent runs as long either way")

readme = os.path.join(HERE, "README.md")
rl.check_readme(readme, expected, exp.projects)

# check_readme checks the codes a row's Arithmetic cell states, not that it
# states them all. The brief asks for every uncredited second, so this checks
# that too.
by_local = {r["local"]: r for r in expected["rows"]}
for cells in rl.readme_rows(readme, "| Minute | Credited | Reasons |"):
    r = by_local[cells[0]]
    want = r["uncredited"] if r["bucket"] else {}
    stated = rl.readme_codes(cells[3] if len(cells) > 3 else "")
    tl.check(stated == want, f"README {cells[0]} states {stated}, expected.json has {want}")

# The tables for client web. check_readme reads every table with the
# standard headers and holds it to expected.json, so these have headers of
# their own, and this checks them against CF the same way.
CF_MINUTES = "| Minute | With client web | Reasons | Arithmetic |"
CF_TOTAL = "| With client web | Seconds | Arithmetic |"
rows = rl.readme_rows(readme, CF_MINUTES)
tl.check(rows, "the README has a minute table for client web")
seen = []
for cells in rows:
    minute, credited, reasons, arith = cells[0], cells[1], cells[2], cells[3]
    first, _, last = minute.partition("-")
    mins = [m for m in CF if first <= m <= (last or first)]
    seen += mins
    credit = {}
    if reasons != "none":
        for item in reasons.split(","):
            parts = item.split()
            credit[parts[-2]] = int(parts[-1])
    stated = rl.readme_codes(arith)
    if all(CF[m] is None for m in mins):
        tl.check(credited == "0" and not credit and not stated, f"README with client web: {minute} has no bucket")
        continue
    tl.check(len(mins) == 1, f"README with client web: {minute} is one minute")
    c, unc, _ = CF[mins[0]]
    tl.check(int(credited) == sum(c.values()) and credit == c,
             f"README with client web: {minute} credits {credited} {credit}, by hand {c}")
    tl.check(stated == unc, f"README with client web: {minute} states {stated}, by hand {unc}")
tl.check(seen == list(CF), f"README with client web: the table has every minute, {seen}")
stated_totals = {}
for cells in rl.readme_rows(readme, CF_TOTAL):
    key, n = cells[0], int(cells[1].replace(",", ""))
    tl.check(rl.lb.arithmetic(cells[2]) == n, f"README with client web: {key}: {cells[2]} is not {n}")
    stated_totals[key] = n
tl.check(stated_totals == CF_TOTALS, f"README with client web: totals {stated_totals}, by hand {CF_TOTALS}")

anchors, turns = [], []
for re, pe in pairs:
    n = min(re["data"]["len_chars"], pe["data"]["typed_len_est"])
    lead = rl.typing_ms(n) // 1000
    anchors.append(rl.anchor_entry(fx, re, pe, kind="substantive_prompt", prompt_class="substantive", lead_s=lead,
                                   sm=f"2.2 rule 9: client mobile skips rules 6 and 7; 2.3: a relayed message's lead "
                                      f"is its typing time, {n} / 1.5 = {lead} s, ending at sent_at"))
anchors.sort(key=lambda a: a["t"])
for name in ("T1", "T2"):
    sub = [e for e in events if e["kind"] == "prompt.submit" and e["data"]["turn_key"] == TURN[name]][0]
    stp = [e for e in events if e["kind"] == "agent.stop" and e["data"]["turn_key"] == TURN[name]][0]
    turns.append(tl.turn(fx, sub["t"], stp["t"], session=S, turn_key=TURN[name], origin="relayed",
                         prompt_class="substantive", sm="2.2 rule 9: client mobile skips rules 6 and 7"))

prompt_entries = [{"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]), "text": None,
                   "len_chars": e["data"]["len_chars"], "source": "invented for this case; only its length matters"}
                  for e in events if e["kind"] == "prompt.submit"]

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
