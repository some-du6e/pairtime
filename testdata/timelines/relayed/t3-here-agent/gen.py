# SPDX-License-Identifier: AGPL-3.0-or-later
"""t3-here's MacBook the next evening. Lena types a long draft into T3
Code's window, which hosts the session, and an agent in another T3 thread
sends her thread a message through T3's MCP tools. T3 stores it with
createdBy agent, so no relay record witnesses its prompt, and SM 2.2 rule 4,
which lists parent_class agent_app, makes the prompt headless though rule 6
at its t would hold: 0 s. Without agent_app on rule 4's list it would be
human_typed at level A. See README.md in this directory."""

import json
import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(HERE)), "sidedoor"))

import relayed as rl  # noqa: E402
import sidedoor as sd  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/t3-here-agent"
fx = tl.Fixture(
    NAME,
    title="A message an agent sends through T3 Code while the person types in T3's window on this computer",
    date="2026-11-24",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="macos",
    keys=True,
    # t3-here's MacBook, started again this evening. Its seq goes on from
    # Monday's.
    device=tl.key("d", "relayed t3 macbook"),
    boot_id=tl.key("b", "relayed t3 macbook boot 2026-11-24"),
    boot="18:32:47.000",
    seq0=26871,
    last_input="20:49:12.400",
    last_key="20:48:58.100",
)
at = fx.at
# The MacBook's own keys are t3-here's: its project, its T3 Code app and its
# file. The session and the turn are this case's.
P = tl.key("p", "relayed weather-cli on the macbook")
APP = tl.key("a", "relayed t3-here T3 Code desktop app")
FILE = {"forecast.py": tl.key("f", "relayed t3-here forecast.py")}
S = tl.key("s", "relayed t3-here-agent session")
ENV = dict(harness="claude_code", version="2.1.334", session=S, project=P)
TURN = {"A1": tl.key("t", "relayed t3-here-agent A1")}

# SM 8.1 values the checks of rule 6 read that sidedoor.py doesn't carry.
TYPING_MAX_CPS = 15
SLEEP_GAP_S = 30

START, PROMPT, STOP = "20:56:10", "21:04:40", "21:08:00"
LENGTH = 240


def hook(t, kind, data):
    """A hook record. The agent takes a presence sample at each one (DESIGN
    3.3)."""
    return fx.emit(at(t), kind, data, source="hook", via="hook", sample=True, **ENV)


# T3 Code's desktop app runs its own server, which starts Claude Code for
# Lena's weather-cli thread at 20:56:10, before she sits down: no TTY, and
# T3's server for its nearest non-shell ancestor (DESIGN 5.1's app-table
# entry). Her last key press was at 20:48:58.1.
hook(START, "session.start", {"start_kind": "startup", "entrypoint": "unknown", "parent_class": "agent_app",
                              "has_tty": False, "permission_mode": "default"})

# She clicks T3's icon at 20:57:29.6, and T3's window comes to the front at
# 20:57:30. It hosts the session (SM 3.2 rule 3) and stays in front to the
# end. She reads the thread, moving the pointer, and from 20:58:00.3 types a
# long message into its composer, a key a second. She hasn't sent it when
# the case ends. Inputs sit on tenths of a second, never on a slot boundary.
fx.pointer("20:57:29.600")
fx.emit(at("20:57:30"), "os.focus",
        {"app_class": "agent_app", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
fx.pointer_every("20:57:30.500", "20:57:54.500", 12_000)
fx.typing("20:58:00.300", "21:09:59.300", 1000)
fx.ticks(START, "21:10:00", tl.SLOT_MS)

# 21:04:40. An agent reviewing weather-cli in another T3 thread sends this
# thread a 240-character message through T3's MCP tools. T3 stores it with
# createdBy agent, so the T3 reader writes no relay.message for it, and
# starts its run at once, which hands it to Claude Code. The other thread
# runs on a provider pairtime has no hooks for, so this MacBook records
# nothing of the sender.
p1 = hook(PROMPT, "prompt.submit", {"turn_key": TURN["A1"], "len_chars": LENGTH, "pasted_chars": 0,
                                    "typed_len_est": LENGTH, "origin_declared": "none",
                                    "matches_pending_cron": False, "answers_question": False,
                                    "low_effort": False})
hook("21:05:10", "agent.output", {"turn_key": TURN["A1"], "words": 45, "code_lines": 0, "is_final": False,
                                  "asks_question": False, "offers_options": False})
hook("21:06:30", "agent.tool", {"tool_class": "edit", "files": [FILE["forecast.py"]], "lines_added": 6,
                                "lines_removed": 2, "visible_output_lines": 0, "t_start": at("21:06:28"),
                                "t_end": at("21:06:30")})
hook(STOP, "agent.output", {"turn_key": TURN["A1"], "words": 60, "code_lines": 0, "is_final": True,
                            "asks_question": False, "offers_options": False})
hook(STOP, "agent.stop", {"turn_key": TURN["A1"], "stop_reason": "end_turn", "stop_hook_active": False})

events = fx.build()
pe = fx.event_at("prompt.submit", p1[0])
start = fx.event_at("session.start", at(START))
T0, END = at("21:00:00"), at("21:10:00")

# The records' shapes. T3 stores the agent's message with createdBy agent
# and the reader records only createdBy user, so no relay record exists, and
# the prompt carries neither relay nor guest.
PROMPT_KEYS = ["turn_key", "len_chars", "pasted_chars", "typed_len_est", "origin_declared", "matches_pending_cron",
               "answers_question", "low_effort"]
tl.check(not any(e["kind"] == "relay.message" for e in events), "the agent's message has no relay record")
tl.check([e["event_id"] for e in events if e["kind"] == "prompt.submit"] == [pe["event_id"]], "one prompt")
tl.check(list(pe["data"]) == PROMPT_KEYS, "the prompt carries neither relay nor guest")
tl.check(LENGTH % 3 == 0 and pe["data"]["typed_len_est"] == LENGTH and pe["data"]["pasted_chars"] == 0,
         "240 characters, a length divisible by 3, none pasted")
tl.check(pe["data"]["origin_declared"] == "none" and not pe["data"]["matches_pending_cron"],
         "the hook declares no origin, so rules 1 to 3 find nothing")
d0 = start["data"]
tl.check((d0["entrypoint"], d0["parent_class"], d0["has_tty"]) == ("unknown", "agent_app", False),
         "T3's server started the session: entrypoint unknown, parent_class agent_app, no TTY")

# The MacBook is t3-here's: the same device, project and T3 app, and a seq
# that goes on from Monday's.
HERE_CASE = os.path.join(os.path.dirname(HERE), "t3-here")
here_doc = rl.read_json(os.path.join(HERE_CASE, "expected.json"))
with open(os.path.join(HERE_CASE, "events.jsonl"), encoding="utf-8") as f:
    here_events = [json.loads(line) for line in f]
tl.check(here_doc["device"] == fx.device and list(here_doc["projects"]) == [P], "t3-here's MacBook and project")
tl.check({e["data"]["app_hash"] for e in here_events if e["kind"] == "os.focus"} == {APP}, "t3-here's T3 app")
tl.check(max(e["seq"] for e in here_events) < fx.seq0, "the seq goes on from Monday's")

# The MacBook's own evidence: Lena present, by key presses in every slot,
# and T3's window, which hosts the session and resolves to weather-cli, in
# front from before the window to its end. So no presence or focus code
# applies, and Replay, which reads neither, needs nothing more.
tl.check(sd.input_in_every_slot(fx, T0, END), "input in every slot")
for s in range(T0, END, tl.SLOT_MS):
    tl.check(sd.keys_in_slot(fx, s), f"key presses in the slot at {fx.hhmmss(s)}")
sd.check_inputs_off_boundaries(fx)
samples = [e for e in events if e["kind"] == "os.presence"]
tl.check(all(e["data"]["idle_keys_s"] is not None and not e["data"]["locked"] for e in samples),
         "the key counter in every sample, and no lock")
tl.check(not fx.locks and not any(e["kind"] == "os.power" for e in events), "no lock, suspend or resume")
ts = [e["t"] for e in samples if T0 - tl.SLOT_MS <= e["t"] <= END]
tl.check(ts[0] <= T0 and ts[-1] == END and all(b - a <= SLEEP_GAP_S * 1000 for a, b in zip(ts, ts[1:])),
         "samples through the window, none more than SLEEP_GAP_S apart")
# The sample at each slot's end dates the slot's last key press inside it,
# 0.7 s before it, as a scorer that rebuilds slots from the samples reads
# them; and so does every sample from 20:58:15 on.
by_t = {e["t"]: e["data"] for e in samples}
for s in range(T0, END, tl.SLOT_MS):
    tl.check(by_t[s + tl.SLOT_MS]["idle_keys_s"].ms == 700,
             f"the sample at {fx.hhmmss(s + tl.SLOT_MS)} dates a key press 0.7 s before it")
tl.check(all(d["idle_keys_s"].ms == 700 for t, d in by_t.items() if t >= at("20:58:15")),
         "every sample from 20:58:15 dates a key press 0.7 s before it")
focus = [e for e in events if e["kind"] == "os.focus"]
tl.check([e["t"] for e in focus] == [at("20:57:30")], "one window report, at 20:57:30, before the window")
tl.check(focus[0]["data"] == {"app_class": "agent_app", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
         "T3's window, hosting the session and resolving to weather-cli, is in front to the end")

# Rule 4's exception for a headless session needs typing in the 10 s before
# session.start and an interactive shell for the parent (SM 2.2 rule 4).
# Neither holds: the last key press came 431.9 s before T3's server started
# the session, and T3's server is no shell.
PRE = sd.KEYS_PRE_S * 1000
st = start["t"]
tl.check(not any(k and st - PRE <= u < st for u, k in fx.inputs) and fx.last_key < st - PRE
         and by_t[st]["idle_keys_s"].ms == 431_900, "no key press in the 10 s before session.start: 431.9 s")
tl.check(d0["parent_class"] == "agent_app", "the session's parent is T3's server, not an interactive shell")


def input_slots(a, b):
    """The number of slots that overlap [a, b) and have input."""
    n, s = 0, a - a % tl.SLOT_MS
    while s < b:
        n += any(s <= u < s + tl.SLOT_MS for u, _ in fx.inputs)
        s += tl.SLOT_MS
    return n


def check_rule6_at_t(pe):
    """What SM 2.2 rule 6 would see at the prompt's t, were rule 4 to let it
    through. Nothing witnesses the prompt, so the test reads its own t, not
    an app's stamp: key presses in the KEYS_PRE_S before t (input_keys, since
    macOS reports key presses), in the inputs and in the hook's sample a
    scorer reads; focus at t on a window that hosts the session; and a
    plausible typed length over the lead rule 6 would give the prompt. With
    pasted_chars 0 that is rule 6, human_typed. Returns the last key press
    before t and active_input_s."""
    t, when = pe["t"], fx.hhmmss(pe["t"])
    keys = [u for u, k in fx.inputs if k and t - PRE <= u < t]
    tl.check(keys, f"key presses in the {sd.KEYS_PRE_S} s before {when}")
    tl.check(t - by_t[t]["idle_keys_s"].ms == keys[-1], f"the hook's sample at {when} dates the last of them")
    front = [e for e in events if e["kind"] == "os.focus" and e["t"] <= t][-1]["data"]
    tl.check(front["app_class"] == "agent_app" and pe["session_key"] in front["hosted_sessions"],
             f"T3's window, in front at {when}, hosts the session")
    lead = sd.THINK_LEAD_S * 1000 + rl.typing_ms(pe["data"]["typed_len_est"])
    active_s = 15 * input_slots(t - lead, t)
    tl.check(pe["data"]["typed_len_est"] <= TYPING_MAX_CPS * active_s,
             f"{pe['data']['typed_len_est']} characters are plausible over {active_s} s of input before {when}")
    tl.check(pe["data"]["pasted_chars"] == 0, f"nothing pasted at {when}, so rule 6, not rule 7")
    return keys[-1], active_s


LAST_KEY, ACTIVE = check_rule6_at_t(pe)
tl.check(pe["t"] - LAST_KEY == 700 and ACTIVE == 285,
         f"rule 6 at the prompt's t would see a key press 0.7 s before it and 285 s of input: {ACTIVE}")

# Rule 4 as it stands takes the prompt: parent_class agent_app is on its
# list, and nothing witnesses the prompt.
tl.check(rl.headless(d0), "rule 4 lists parent_class agent_app")


def rule4_without_agent_app(d, front):
    """Rule 4 with agent_app struck from its list, for the counterfactual.
    relayed.py's headless reads a desktop-app host from the entrypoint alone,
    so it would still take this session by rule 4's last clause, a session
    with neither a TTY nor an IDE or desktop-app host. T3's window is an
    agent desktop app (SM 3.2 rule 3), and the session runs under it, which
    is what puts the session in the window's hosted_sessions, so the
    counterfactual reads the window as the session's desktop-app host."""
    if d["entrypoint"] in ("sdk", "exec", "mcp") or d["parent_class"] in ("script_runtime", "scheduler", "agent"):
        return True
    hosted = front["app_class"] == "agent_app" and S in front["hosted_sessions"]
    no_host = d["entrypoint"] not in ("ide", "desktop") and d["parent_class"] != "ide" and not hosted
    return not d["has_tty"] and no_host


tl.check(not rule4_without_agent_app(d0, focus[0]["data"]),
         "without agent_app on its list, rule 4 wouldn't take the prompt of a session T3's window hosts")

# The rows, by hand from SM 2.2 rule 4, 2.4, 5.1 and 5.2.
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rules 4, 6 and 9, 2.3, 2.4, 5.1, 5.2", evidence="C",
                  witness="witnessed", projects={P: "weather-cli"}, start="21:00:00", end="21:10:00")
IDLE = "5.1: no credit and no agent running"
HEADLESS = ("2.2 rule 4: nothing witnesses the agent's prompt, and rule 4 lists parent_class agent_app, so the "
            "prompt is headless whatever window hosts the session")
RUNS = "5.2: X.automated while a headless turn runs"


def row(minute, credit=None, unc=None, agent=0, sm=None, note=None, bucket=True, end=None):
    exp.row(minute, end, credit=credit, uncredited=unc, agent=agent, sm=sm,
            derived=DERIVED if bucket else None, note=note, bucket=bucket)


row("21:00", end="21:04", bucket=False, sm=IDLE + "; 2.3: key presses are no act and open no window",
    note="Lena types her draft into T3's composer, a key a second, with T3's window, which hosts the session, in "
         "front. Without agent_app on rule 4's list, the agent's prompt at 21:04:40 would be human_typed and its "
         "280 s lead would fill these minutes.")
row("21:04", {}, {"X.no_anchor": 40, "X.automated": 20}, 20, sm=HEADLESS,
    note="Lena types with no window open until 21:04:40: X.no_anchor. Then the agent's message reaches the agent, "
         "with no relay record: T3 stores it with createdBy agent. Rule 6 at 21:04:40 would see a key press 0.7 s "
         "before it and T3's window, hosting the session, in front, but rule 4 comes first. A1 runs, automated.")
row("21:05", {}, {"X.automated": 60}, 60, sm=RUNS,
    note="A1's 45 words at 21:05:10 put 15 s in the bucket, with no base, since an automated turn doesn't reset "
         "it (2.4). Nothing drains it: no human anchor in the project within READ_HORIZON_S.")
row("21:06", {}, {"X.automated": 60}, 60, sm=RUNS, note="The 8-line edit at 21:06:30 adds 60 s.")
row("21:07", {}, {"X.automated": 60}, 60, sm=RUNS)
row("21:08", bucket=False, sm=IDLE + "; 2.4: no human anchor, so nothing reads the bucket",
    note="A1 stops at 21:08:00 with 60 words, 20 s more, so 15 + 60 + 20 = 95 s sit in the bucket and nothing "
         "reads them. A1 ran until 21:08:00, so no agent runs in 21:08.")
row("21:09", bucket=False, sm=IDLE, note="Lena types on to the end of the window and sends nothing.")

rp = rl.Replay(events, T0, END, desktop=True)
rl.check_rows(exp, rp, {S: P})
tl.check(rp.origin == {pe["event_id"]: "headless"}, "rule 4 makes the agent's prompt headless")
tl.check(not rp.relay_of and not rp.anchors and not rp.spans and not rp.credited,
         "nothing is relayed, anchored, read or credited")
tl.check(not rp.capped, "no second is X.bucket_cap")
tl.check(rp.running == rp.automated == set(range(pe["t"], at(STOP), 1000)),
         "A1 runs, automated, from 21:04:40 to 21:08:00")
tl.check(rp.left == {S: 95}, f"the bucket holds 15 + 60 + 20 = 95 s that nothing reads: {rp.left}")

totals = exp.totals()
tl.check(totals["credited_s"] == 0 == len(rp.credited) and not totals["credited_by_reason"],
         f"credited {totals['credited_s']}")
tl.check(totals["uncredited"] == {"X.no_anchor": 40, "X.automated": 200}, f"uncredited {totals['uncredited']}")
tl.check(totals["agent_running_s"] == 200, f"agent {totals['agent_running_s']}")
expected = exp.doc(
    extra_totals={"level_c_s": 0, "via_relay_s": 0, "relayed_prompts": 0, "headless_prompts": 1,
                  "typed_prompts": 0},
    stated=[],
    approx={},
    errata=[rl.ERRATUM],
    notes=[
        "No SM section describes this case. Every row comes from SM 2.2 rules 4, 6 and 9, 2.3, 2.4, 5.1 and 5.2 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them. Nothing is credited, so no second has a level; "
        "evidence C is agent-day's, the other case that credits nothing.",
        "An agent in another T3 thread sends this thread its message through T3's MCP tools, and T3 stores it with "
        "createdBy agent, so the T3 reader writes no relay record. Nothing witnesses the prompt, and rule 4, which "
        "lists parent_class agent_app, makes it headless whatever window hosts the session. headless_prompts counts "
        "it; relayed_prompts and typed_prompts count none.",
        "Lena types in T3's window with key presses in every slot, and the window, which hosts the session and "
        "resolves to weather-cli, is in front throughout. No presence or focus code applies, so an uncredited "
        "second is X.automated while the headless turn runs and X.no_anchor otherwise. No human anchor exists, so "
        "nothing reads the bucket and no second is X.bucket_cap.",
        "Rule 6 at the prompt's t would hold: a key press 0.7 s before it, T3's window hosting the session in "
        "front, and 240 characters, plausible over 285 s of input. Without agent_app on rule 4's list, with T3's "
        "window as the session's desktop-app host, the prompt would be human_typed at level A, and the case would "
        "credit 395 s: P 280, SUP 95 and READ 20. README.md works it out minute by minute.",
    ],
)

# The counterfactual the case exists to show: the same records with
# agent_app struck from rule 4's list. Rule 6 then holds at the prompt's t
# (checked above), and the prompt is human_typed and substantive, at level A
# on macOS (SM 5.1). Its lead is 2.3's substantive prompt row, think part and
# typing part, ending at the prompt, with key presses in every slot of the
# typing part. Reading follows 2.4's presence rules: the prompt resets the
# bucket to its base when A1 starts, and the bucket drains as SUP while A1
# runs and READ after, up to BUCKET_MAX_S, within READ_HORIZON_S of the
# prompt. By hand, minute by minute, then checked against sidedoor's Sweep,
# which takes presence and focus on the project as given, as checked above.
CF = {
    "21:00": ({"P": 60}, {}, 0),
    "21:01": ({"P": 60}, {}, 0),
    "21:02": ({"P": 60}, {}, 0),
    "21:03": ({"P": 60}, {}, 0),
    "21:04": ({"P": 40, "SUP": 20}, {}, 20),
    "21:05": ({"SUP": 15}, {"X.agent_only": 45}, 60),
    "21:06": ({"SUP": 30}, {"X.agent_only": 30}, 60),
    "21:07": ({"SUP": 30}, {"X.agent_only": 30}, 60),
    "21:08": ({"READ": 20}, {"X.no_anchor": 40}, 0),
    "21:09": None,  # no credit and no agent: no bucket
}
tl.check(list(CF) == [fx.hhmm(m) for m in range(T0, END, tl.MINUTE_MS)], "the counterfactual covers the window")
for minute, w in CF.items():
    if w:
        tl.check(sum(w[0].values()) + sum(w[1].values()) == 60, f"counterfactual {minute} adds up to 60 s")

TYPING_S = rl.typing_ms(pe["data"]["typed_len_est"]) // 1000
tl.check(TYPING_S == 160 and sd.THINK_LEAD_S + TYPING_S == 280, "the lead would be 120 + 240 / 1.5 = 280 s")
cf = sd.Sweep(T0, END)
cf.anchor(pe["t"], lead_s=sd.THINK_LEAD_S + TYPING_S, typing_part_s=TYPING_S, reason="P", level="A",
          typing_ok=lambda slot: sd.keys_in_slot(fx, slot))
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
tl.check(sd.levels(cf) == {"A": 395, "B": 0, "C": 0}, f"counterfactual levels {sd.levels(cf)}")

CF_TOTALS = {"credited_s": 0, "level_a_s": 0, "agent_running_s": 0, "typed_prompts": 1, "headless_prompts": 0}
for w in CF.values():
    if w:
        for k, v in list(w[0].items()) + list(w[1].items()):
            CF_TOTALS[k] = CF_TOTALS.get(k, 0) + v
        CF_TOTALS["credited_s"] += sum(w[0].values())
        CF_TOTALS["agent_running_s"] += w[2]
CF_TOTALS["level_a_s"] = CF_TOTALS["credited_s"]
tl.check({k: CF_TOTALS[k] for k in ("credited_s", "P", "SUP", "READ", "X.agent_only", "X.no_anchor")} ==
         {"credited_s": 395, "P": 280, "SUP": 95, "READ": 20, "X.agent_only": 105, "X.no_anchor": 40},
         f"counterfactual totals {CF_TOTALS}")
tl.check(CF_TOTALS["agent_running_s"] == totals["agent_running_s"], "the agent runs as long either way")
tl.check(CF_TOTALS["SUP"] + CF_TOTALS["X.agent_only"] == totals["uncredited"]["X.automated"],
         "A1's 200 s would be SUP and X.agent_only in place of X.automated")

readme = os.path.join(HERE, "README.md")
rl.check_readme(readme, expected, exp.projects)

# check_readme checks the codes a row's Arithmetic cell states, not that it
# states them all. The brief asks for every uncredited second, so this checks
# that too, and that a row without a bucket states none.
by_local = {r["local"]: r for r in expected["rows"]}
for cells in rl.readme_rows(readme, "| Minute | Credited | Reasons |"):
    r = by_local[cells[0]]
    want = r["uncredited"] if r["bucket"] else {}
    stated = rl.readme_codes(cells[3] if len(cells) > 3 else "")
    tl.check(stated == want, f"README {cells[0]} states {stated}, expected.json has {want}")

# The tables without agent_app. check_readme reads every table with the
# standard headers and holds it to expected.json, so these have headers of
# their own, and this checks them against CF the same way.
CF_MINUTES = "| Minute | Without `agent_app` | Reasons | Arithmetic |"
CF_TOTAL = "| Without `agent_app` | Seconds | Arithmetic |"
rows = rl.readme_rows(readme, CF_MINUTES)
tl.check(rows, "the README has a minute table without agent_app")
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
        tl.check(credited == "0" and not credit and not stated, f"README without agent_app: {minute} has no bucket")
        continue
    tl.check(len(mins) == 1, f"README without agent_app: {minute} is one minute")
    c, unc, _ = CF[mins[0]]
    tl.check(int(credited) == sum(c.values()) and credit == c,
             f"README without agent_app: {minute} credits {credited} {credit}, by hand {c}")
    tl.check(stated == unc, f"README without agent_app: {minute} states {stated}, by hand {unc}")
tl.check(seen == list(CF), f"README without agent_app: the table has every minute, {seen}")
stated_totals = {}
for cells in rl.readme_rows(readme, CF_TOTAL):
    key, n = cells[0], int(cells[1].replace(",", ""))
    tl.check(rl.lb.arithmetic(cells[2]) == n, f"README without agent_app: {key}: {cells[2]} is not {n}")
    stated_totals[key] = n
tl.check(stated_totals == CF_TOTALS, f"README without agent_app: totals {stated_totals}, by hand {CF_TOTALS}")

turns = [tl.turn(fx, pe["t"], at(STOP), session=S, turn_key=TURN["A1"], origin="headless",
                 sm="2.2 rule 4: T3 stores the agent's message with createdBy agent, so no record witnesses it, and "
                    "rule 4 lists parent_class agent_app")]
prompt_entries = [{"turn_key": TURN["A1"], "t": pe["t"], "local": fx.hhmmss(pe["t"]), "text": None,
                   "len_chars": LENGTH, "source": "invented for this case: an agent's message; only its length "
                                                 "matters, and only to the counterfactual"}]

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": [], "turns": turns},
         {"fixture": NAME, "prompts": prompt_entries})
print(f"{NAME}: {len(events)} events, credited {totals['credited_s']} s, uncredited {totals['uncredited']}, "
      f"agent {totals['agent_running_s']} s; without agent_app on rule 4's list {CF_TOTALS['credited_s']} s at "
      f"level A (P {CF_TOTALS['P']}, SUP {CF_TOTALS['SUP']}, READ {CF_TOTALS['READ']}); wrote {tl.out_dir(HERE)}")
