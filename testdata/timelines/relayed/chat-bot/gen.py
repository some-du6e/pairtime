# SPDX-License-Identifier: AGPL-3.0-or-later
"""A chat bot on the server that relays the owner's chat messages with
`pairtime relay`: two pair with prompts T3 Code delivered, one pairs with
nothing, and one pairs with a `claude -p` the bot ran itself, which rule 4
still takes. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import relayed as rl  # noqa: E402
import timeline as tl  # noqa: E402

NAME = "relayed/chat-bot"
fx = tl.Fixture(
    NAME,
    title="A chat bot relays the owner's messages with pairtime relay",
    date="2026-11-25",
    timezone_name="America/Chicago",
    utc_offset_min=-360,
    os_name="linux",
    keys=False,
    device=tl.key("d", "relayed chat-bot server"),
    boot_id=tl.key("b", "relayed chat-bot server boot"),
    boot="04:02:51.000",
    seq0=91200,
    last_input="04:02:51.000",
)
at = fx.at
P = tl.key("p", "relayed chat-bot recipe-site")
S1 = tl.key("s", "relayed chat-bot t3 session")
S2 = tl.key("s", "relayed chat-bot claude -p session")
THREAD = tl.key("r", "relayed chat-bot slack and recipe-site")
ENV1 = dict(harness="claude_code", version="2.1.334", session=S1, project=P)
ENV2 = dict(harness="claude_code", version="2.1.334", session=S2, project=P)
TURN = {n: tl.key("t", f"relayed chat-bot {n}") for n in ("T1", "T2", "T4")}
FILE = {"search.js": tl.key("f", "relayed chat-bot search.js")}


def hook(env, t, kind, data):
    return fx.emit(at(t), kind, data, source="hook", via="hook", **env)


def prompt(env, t, turn, length):
    return hook(env, t, "prompt.submit", {"turn_key": TURN[turn], "len_chars": length, "pasted_chars": 0,
                                          "typed_len_est": length, "origin_declared": "none",
                                          "matches_pending_cron": False, "answers_question": False,
                                          "low_effort": False})


def relay(t, sent, length):
    """A `pairtime relay` call: the bot's own claim, through the plugin path,
    with no session. The text came on stdin from the pinned sender, and the
    agent keeps only its length and class."""
    return fx.emit(at(t), "relay.message", {"app": "chat_bot", "sent_at": at(sent), "thread_key": THREAD,
                                            "declared": "none", "len_chars": length, "low_effort": False},
                   source="cli", via="plugin", project=P)


def output(env, t, turn, words, final=False):
    hook(env, t, "agent.output", {"turn_key": TURN[turn], "words": words, "code_lines": 0, "is_final": final,
                                  "asks_question": False, "offers_options": False})


def stop(env, t, turn):
    hook(env, t, "agent.stop", {"turn_key": TURN[turn], "stop_reason": "end_turn", "stop_hook_active": False})


# The session T3 Code's server runs for the bot's thread.
hook(ENV1, "20:50:05", "session.start", {"start_kind": "startup", "entrypoint": "unknown",
                                         "parent_class": "agent_app", "has_tty": False,
                                         "permission_mode": "bypassPermissions"})

# B1. The owner sends a chat message at 21:00:00. The bot calls pairtime
# relay a second later with the message, personal information removed, 120
# characters, then hands the whole message to the thread through T3's MCP
# tools. T3 stores that copy as createdBy agent, so the T3 reader writes
# nothing for it, and the prompt reaches Claude Code at 21:00:03, 126
# characters: the bot removed an address from what it relayed.
r1 = relay("21:00:01", "21:00:00", 120)
p1 = prompt(ENV1, "21:00:03", "T1", 126)
output(ENV1, "21:01:00", "T1", 90)
output(ENV1, "21:02:00", "T1", 120, final=True)
stop(ENV1, "21:02:00", "T1")

# B2 at 21:05:00, 60 characters both ways.
r2 = relay("21:05:01", "21:05:00", 60)
p2 = prompt(ENV1, "21:05:03", "T2", 60)
output(ENV1, "21:06:30", "T2", 75, final=True)
stop(ENV1, "21:06:30", "T2")

# B3 at 21:10:00. The bot relays it, but hands it to no agent pairtime sees:
# nothing pairs with it.
r3 = relay("21:10:01", "21:10:00", 90)

# B4 at 21:15:00. For a quick question the bot runs `claude -p` itself, a
# child of the bot's own Node process.
r4 = relay("21:15:01", "21:15:00", 45)
hook(ENV2, "21:15:02", "session.start", {"start_kind": "startup", "entrypoint": "sdk",
                                         "parent_class": "script_runtime", "has_tty": False})
p4 = prompt(ENV2, "21:15:03", "T4", 45)
output(ENV2, "21:16:00", "T4", 30, final=True)
stop(ENV2, "21:16:00", "T4")

events = fx.build()
R = {n: fx.event_at("relay.message", raw[0], sent_at=raw[4]["sent_at"])
     for n, raw in (("R1", r1), ("R2", r2), ("R3", r3), ("R4", r4))}
E = {n: fx.event_at("prompt.submit", raw[0], turn_key=raw[4]["turn_key"])
     for n, raw in (("P1", p1), ("P2", p2), ("P4", p4))}
# Every call came before its prompt, so each prompt names its call.
for p, r in (("P1", "R1"), ("P2", "R2"), ("P4", "R4")):
    E[p]["data"]["relay"] = R[r]["event_id"]

T0, END = at("20:58:00"), at("21:18:00")
DERIVED = ["credited_s", "credit", "uncredited", "agent_running_s"]
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.2 rule 9, 2.3, 2.4", evidence="C",
                  witness="witnessed", projects={P: "recipe-site"}, start="20:58:00", end="21:18:00")
LEAD = "2.3: a relayed message's lead is its typing time from the call's own text, ending at sent_at"


def row(minute, credit=None, unc=None, agent=0, sm=None, note=None, bucket=True, end=None):
    exp.row(minute, end, credit=credit, uncredited=unc, agent=agent, sm=sm,
            derived=DERIVED if bucket else None, note=note, bucket=bucket)


row("20:58", {P: {"P": 20}}, {"X.no_anchor": 40}, sm=LEAD,
    note="B1's 120 relayed characters: 80 s, from 20:58:40. The prompt's 126 aren't the typed length: the call's "
         "own text is, and it is never longer than the hook's (SM 2.2).")
row("20:59", {P: {"P": 60}}, {}, sm=LEAD)
row("21:00", {}, {"X.no_anchor": 3, "X.agent_only": 57}, 57, sm="2.2 rule 9: B1's prompt is relayed",
    note="The prompt reaches Claude Code at 21:00:03. Its session's nearest non-shell ancestor is T3's server, so "
         "the witnessed prompt skips rule 4.")
row("21:01", {}, {"X.agent_only": 60}, 60, sm="2.4: no relayed session reads while its agent runs")
row("21:02", {P: {"READ": 60}}, {}, sm="2.4",
    note="T1 stops at 21:02:00 and B2 follows 180 s later. T1 filled 20 + 30 + 40 = 90 s.")
row("21:03", {P: {"READ": 30}}, {"X.no_anchor": 30}, sm="2.4", note="The 90 s are read by 21:03:30.")
row("21:04", {P: {"P": 40}}, {"X.no_anchor": 20}, sm=LEAD, note="B2's 60 characters: 40 s, from 21:04:20.")
row("21:05", {}, {"X.no_anchor": 3, "X.agent_only": 57}, 57, sm="2.2 rule 9: B2's prompt is relayed")
row("21:06", {}, {"X.agent_only": 30, "X.no_anchor": 30}, 30, sm="2.4",
    note="T2 stops at 21:06:30. No relayed message follows it, so nothing reads its output.")
row("21:07", end="21:15", bucket=False, sm="5.1: no credit and no agent running",
    note="B3, relayed at 21:10:00, pairs with no prompt within RELAY_MATCH_S, so it opens nothing and raises "
         "relay_without_prompt. Its lead would have been 21:09:00 to 21:10:00.")
row("21:15", {}, {"X.no_anchor": 3, "X.automated": 57}, 57, sm="2.2 rule 4: a script's claude -p stays headless",
    note="B4 pairs with the prompt the bot's own claude -p reached at 21:15:03, but that session's nearest "
         "non-shell ancestor is the bot's Node process, script_runtime, so rule 4 still takes it.")
row("21:16", end="21:18", bucket=False, sm="5.1: no credit and no agent running")

rp = rl.Replay(events, T0, END)
rl.check_rows(exp, rp, {S1: P, S2: P})
tl.check(rp.origin[E["P4"]["event_id"]] == "headless", "B4's prompt stays headless")
paired = {r["event_id"] for r in rp.relay_of.values()}
tl.check(R["R3"]["event_id"] not in paired, "B3 pairs with nothing")

totals = exp.totals()
tl.check(totals["credited_s"] == 210, f"credited {totals['credited_s']}")
expected = exp.doc(
    extra_totals={
        "level_c_s": 210,
        "via_relay_s": 210,
        "relayed_prompts": 2,
        "headless_prompts": 1,
        "relay_without_prompt": 1,
    },
    stated=[],
    approx={},
    errata=[rl.ERRATUM],
    notes=[
        "No SM section describes this case. Every row comes from SM 2.2 rule 9, 2.3 and 2.4 as "
        "docs/errata/2026-11-22-relayed-prompts.md applies them. Every credited second is level C with via_relay, "
        "and these anchors carry via_plugin too (DESIGN 6.4): only the bot's call vouches for them.",
        "relay_without_prompt counts the review flags the case raises: B3's call, which paired with nothing.",
        "The server has no desktop, so every slot's presence is unknown and uncredited seconds take SM 5.2's codes "
        "for such a slot.",
    ],
)

anchors = []
for p, r in (("P1", "R1"), ("P2", "R2")):
    lead = rl.typing_ms(R[r]["data"]["len_chars"]) // 1000
    a = rl.anchor_entry(fx, R[r], E[p], kind="substantive_prompt", prompt_class="substantive", lead_s=lead,
                        sm=f"2.3: typing time of the call's own text, {R[r]['data']['len_chars']} / 1.5 = {lead} s")
    a["labels"] = ["via_plugin", "via_relay"]
    anchors.append(a)
turns = []
for name, env, origin in (("T1", S1, "relayed"), ("T2", S1, "relayed"), ("T4", S2, "headless")):
    sub = [e for e in events if e["kind"] == "prompt.submit" and e["data"]["turn_key"] == TURN[name]][0]
    stp = [e for e in events if e["kind"] == "agent.stop" and e["data"]["turn_key"] == TURN[name]][0]
    turns.append(tl.turn(fx, sub["t"], stp["t"], session=env, turn_key=TURN[name], origin=origin,
                         prompt_class="substantive" if origin == "relayed" else None,
                         sm="2.2 rule 9" if origin == "relayed" else "2.2 rule 4"))
prompts = [{"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]), "text": None,
            "len_chars": e["data"]["len_chars"], "source": "invented for this case; only its length matters"}
           for e in events if e["kind"] == "prompt.submit"]

rl.check_readme(os.path.join(HERE, "README.md"), expected, exp.projects)

fx.write(tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns},
         {"fixture": NAME, "prompts": prompts})
