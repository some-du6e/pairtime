# SPDX-License-Identifier: AGPL-3.0-or-later
"""Shared code for the relayed fixtures. README.md in this directory says
what they are.

A case's gen.py builds its events with ../timeline.py, writes the rows it
computed by hand from SM 2.2 rule 9, 2.3, 2.4 and 5.2 as D-relayed has them,
and checks them against Replay before it writes anything. Replay is a
second, mechanical reading of the same rules, second by second, written
only to catch slips in the hand arithmetic. It isn't the scorer, and the
rows, not Replay, are the oracle.

Standard library only.
"""

import json
import os
import sys
from fractions import Fraction

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
TIMELINES = os.path.dirname(HERE)
sys.path.insert(0, TIMELINES)
sys.path.insert(0, os.path.join(TIMELINES, "lookback"))

import timeline as tl  # noqa: E402
import lookback as lb  # noqa: E402  (its README arithmetic parser)

# The decision these cases are the oracle for.
ERRATUM = "2026-11-22-relayed-prompts"

# The SM 8 parameters the relayed rules read.
COMPOSE_CPS = Fraction(3, 2)   # characters a second (SM 8.2)
COMPOSE_MAX_S = 300            # cap on a lead's typing part (SM 8.2)
R_TURN_BASE_S = 20             # SM 8.3
READ_WPM, DIFF_LPM, TOOL_LINES_MAX = 180, 8, 4  # SM 8.3
BUCKET_MAX_S = 600             # the cap with presence (SM 8.3)
UNKNOWN_BUCKET_MAX_S = 120     # the cap for a relayed session (SM 8.6)
READ_FOLLOW_S = 600            # the next relayed message within 600 s of the stop (SM 2.4)
RELAY_MATCH_S = 600            # SM 8.1
RELAY_SEEN_LAG_S = 60          # SM 8.1

# Credit reasons relayed credit can give: a prompt's, an answer's or a
# go-ahead's typing lead, and reading after a stop. Never SUP: a relayed
# session reads only after a stop (SM 2.4).
RELAYED_REASONS = ("P", "ANS", "GO", "READ")
# SM 5.2's codes for a slot whose presence is unknown, in their order.
UNKNOWN_CODES = ("X.bucket_cap", "X.agent_only", "X.automated", "X.no_anchor")


def typing_ms(chars):
    """SM 2.3's lead for a relayed message: typing time only,
    chars / COMPOSE_CPS capped at COMPOSE_MAX_S. The cases give every
    message a length divisible by 3, so the time is whole seconds and no
    rounding rule is needed."""
    s = min(Fraction(chars) / COMPOSE_CPS, COMPOSE_MAX_S)
    tl.check(s.denominator == 1, f"{chars} characters type in whole seconds")
    return int(s) * 1000


def fill_s(kind, data):
    """SM 2.4's fill for one output or tool record, in seconds."""
    if kind == "agent.output":
        return Fraction(60 * data["words"], READ_WPM) + Fraction(60 * data["code_lines"], DIFF_LPM)
    if kind == "agent.tool":
        lines = data["lines_added"] + data["lines_removed"] + min(data["visible_output_lines"], TOOL_LINES_MAX)
        return Fraction(60 * lines, DIFF_LPM)
    return Fraction(0)


def declared(ps):
    """SM 2.2 rules 1 to 3 as a prompt's own record declares them, or None."""
    if ps["matches_pending_cron"] or ps["origin_declared"] in ("cron", "loop"):
        return "scheduled"
    if ps["origin_declared"] in ("goal", "followup"):
        return "self_continue"
    if ps["origin_declared"] in ("subagent", "cross_session"):
        return "subagent"
    return None


def headless(start):
    """SM 2.2 rule 4 for a session.start, on a machine whose OS reports no
    key presses before it."""
    ep, parent = start["entrypoint"], start["parent_class"]
    if ep in ("sdk", "exec", "mcp") or parent in ("script_runtime", "scheduler", "agent"):
        return True
    return not start["has_tty"] and ep not in ("ide", "desktop") and parent != "ide"


class Replay:
    """D-relayed applied second by second over [start, end), Unix ms on
    whole seconds, on a device that sampled no presence and recorded no
    focus, as a server with no desktop. It knows sessions, prompts, relay
    records, outputs, tools and stops.

    Pairing (SM 2.2). A relay record and a prompt.submit pair when one names
    the other (the record's prompt or the prompt's relay), the record's
    session is the prompt's or none, the prompt came no earlier than the
    record's sent_at and at most RELAY_MATCH_S after it, and the record came
    at most RELAY_SEEN_LAG_S after sent_at. A record of declared cron or
    cross_session gives its prompt rule 1's or rule 3's origin. One of
    declared none witnesses it: rule 4 is skipped when the session's
    parent_class is agent_app, and with no local input, rules 6 and 7 never
    hold, so the prompt is relayed. A record that pairs with nothing opens
    nothing. Every other prompt goes by rules 1 to 4, and the cases have no
    prompt that rule 4 lets through.

    A relayed prompt opens an anchor at its record's sent_at. Its lead is
    the typing time of the record's len_chars, capped at the seconds since
    the thread's previous record of declared none, and it ends at sent_at.
    The prompt resets the session's bucket to R_TURN_BASE_S; output fills
    it up to UNKNOWN_BUCKET_MAX_S. Reading drains it after one of the
    session's stops, until the session's next relayed message is sent or
    its next turn starts, whichever is first, when that message's sent_at
    comes within READ_FOLLOW_S after the stop. A lead beats reading (SM 3.3
    rule 2). Two sessions never want one second here; Replay checks that.

    credited maps each credited second to (session, reason). running holds
    the seconds an agent turn ran, automated those in which every running
    turn was automated, and capped the seconds nobody wanted that only
    UNKNOWN_BUCKET_MAX_S kept a session from reading."""

    def __init__(self, events, start, end):
        tl.check(start % 1000 == 0 and end % 1000 == 0, "the replay runs on whole seconds")
        tl.check(not any(e["kind"].startswith("os.") for e in events), "no presence, focus or power record")
        by_id = {e["event_id"]: e for e in events}
        starts, prompts, stops, fills, relays = {}, {}, {}, {}, []
        for e in events:
            k, s, d = e["kind"], e["session_key"], e["data"]
            if k == "session.start":
                starts[s] = d
            elif k == "prompt.submit":
                prompts.setdefault(s, []).append(e)
            elif k == "agent.stop":
                stops.setdefault(s, []).append(e["t"])
            elif k in ("agent.output", "agent.tool"):
                fills.setdefault(s, []).append((e["t"], fill_s(k, d)))
            elif k == "relay.message":
                relays.append(e)

        # Pairings, from whichever record names the other.
        pair = {}  # prompt event id -> relay record
        for r in relays:
            p = r["data"].get("prompt")
            if p:
                tl.check(p in by_id and by_id[p]["kind"] == "prompt.submit", f"relay {r['event_id']} names a prompt")
                tl.check(p not in pair, f"one relay record pairs with prompt {p}")
                pair[p] = r
        for ps in prompts.values():
            for q in ps:
                rid = q["data"].get("relay")
                if rid:
                    tl.check(rid in by_id and by_id[rid]["kind"] == "relay.message",
                             f"prompt {q['event_id']} names a relay record")
                    tl.check(q["event_id"] not in pair, f"prompt {q['event_id']} pairs once")
                    pair[q["event_id"]] = by_id[rid]
        tl.check(len({r["event_id"] for r in pair.values()}) == len(pair), "one prompt per relay record")
        for pid, r in pair.items():
            q = by_id[pid]
            d = r["data"]
            tl.check(r["session_key"] in ("none", q["session_key"]), f"relay {r['event_id']} is in its prompt's session")
            tl.check(d["sent_at"] <= q["t"] <= d["sent_at"] + RELAY_MATCH_S * 1000,
                     f"relay {r['event_id']}'s prompt comes within RELAY_MATCH_S of sent_at")
            tl.check(d["sent_at"] <= r["t"] <= d["sent_at"] + RELAY_SEEN_LAG_S * 1000,
                     f"relay {r['event_id']} was seen within RELAY_SEEN_LAG_S of sent_at")
            tl.check(d["len_chars"] <= q["data"]["typed_len_est"], "the app's typed length is at most the hook's")

        # Origins.
        self.origin, self.relay_of = {}, {}
        for s, ps in prompts.items():
            for q in ps:
                r = pair.get(q["event_id"])
                own = declared(q["data"])
                app = {"cron": "scheduled", "cross_session": "subagent"}.get(r["data"]["declared"]) if r else None
                if own or app:
                    self.origin[q["event_id"]] = own or app
                elif r and starts[s]["parent_class"] == "agent_app":
                    self.origin[q["event_id"]] = "relayed"
                    self.relay_of[q["event_id"]] = r
                elif headless(starts[s]):
                    self.origin[q["event_id"]] = "headless"
                else:
                    tl.check(False, "no case has a prompt that rule 4 lets through")

        # Turns: each prompt runs to the session's next stop.
        self.turns = []  # (session, t_submit, t_stop or None, human)
        for s, ps in prompts.items():
            for q in ps:
                st = [t for t in stops.get(s, []) if t > q["t"]]
                self.turns.append((s, q["t"], st[0] if st else None, self.origin[q["event_id"]] == "relayed"))

        # Anchors, in sent_at order, and each thread's earlier messages.
        witnesses = sorted((r for r in relays if r["data"]["declared"] == "none"),
                           key=lambda r: (r["data"]["sent_at"], r["event_id"]))
        self.anchors = []
        for pid, r in sorted(self.relay_of.items(), key=lambda kv: kv[1]["data"]["sent_at"]):
            q = by_id[pid]
            sa = r["data"]["sent_at"]
            tl.check(sa % 1000 == 0, "every relayed message is sent on a whole second")
            earlier = [w["data"]["sent_at"] for w in witnesses
                       if w["data"]["thread_key"] == r["data"]["thread_key"] and w["data"]["sent_at"] < sa]
            lead = typing_ms(r["data"]["len_chars"])
            if earlier:
                lead = min(lead, sa - earlier[-1])
            reason = "GO" if r["data"]["low_effort"] else "P"
            self.anchors.append({"id": r["event_id"], "sent_at": sa, "lead_ms": lead, "session": q["session_key"],
                                 "prompt": pid, "reason": reason, "thread": r["data"]["thread_key"]})

        # Reading spans.
        self.spans = {}
        for s, ts in stops.items():
            sends = sorted(a["sent_at"] for a in self.anchors if a["session"] == s)
            turn_starts = sorted(t for (s2, t, _, _) in self.turns if s2 == s)
            for stop in ts:
                nxt = [t for t in sends if t > stop]
                if not nxt or nxt[0] - stop > READ_FOLLOW_S * 1000:
                    continue
                end_at = min([nxt[0]] + [t for t in turn_starts if t >= stop])
                self.spans.setdefault(s, []).append((stop, end_at))

        # Bucket changes in time order: a relayed prompt resets its session's
        # bucket when the agent takes it up; output fills it up to the cap.
        changes = [(by_id[a["prompt"]]["t"], 0, a["session"], None) for a in self.anchors]
        for s, fs in fills.items():
            changes += [(t, 1, s, x) for t, x in fs]
        changes.sort(key=lambda c: (c[0], c[1]))

        self.running, self.automated = set(), set()
        for tau in range(start, end, 1000):
            on = [human for (_, a, b, human) in self.turns if a <= tau < (b if b else end)]
            if on:
                self.running.add(tau)
                if not any(on):
                    self.automated.add(tau)

        bucket = {k: Fraction(0) for k in prompts}
        full = {k: Fraction(0) for k in prompts}
        self.credited, self.capped = {}, set()
        i = 0
        tau = min([start] + [c[0] - c[0] % 1000 for c in changes])
        while tau < end:
            while i < len(changes) and changes[i][0] <= tau:
                _, what, k, x = changes[i]
                if what == 0:
                    bucket[k] = full[k] = Fraction(R_TURN_BASE_S)
                else:
                    bucket[k] = min(bucket[k] + x, UNKNOWN_BUCKET_MAX_S)
                    full[k] = min(full[k] + x, BUCKET_MAX_S)
                i += 1
            if tau >= start:
                cands = {}
                for a in self.anchors:
                    if a["sent_at"] - a["lead_ms"] <= tau < a["sent_at"]:
                        cands.setdefault(a["session"], []).append((1, a["sent_at"], a["reason"]))
                for k, sp in self.spans.items():
                    if bucket[k] > 0 and any(x <= tau < y for x, y in sp):
                        cands.setdefault(k, []).append((2, 0, "READ"))
                tl.check(len(cands) <= 1, f"at {tau} at most one session wants the second")
                if cands:
                    win, c = next(iter(cands.items()))
                    best = sorted(c)[0]
                    self.credited[tau] = (win, best[2])
                    if best[2] == "READ":
                        tl.check(bucket[win] >= 1, f"a whole second to read at {tau}")
                        bucket[win] -= 1
                        full[win] -= 1
                else:
                    cut = [k for k, sp in self.spans.items()
                           if bucket[k] == 0 and full[k] >= 1 and any(x <= tau < y for x, y in sp)]
                    for k in cut:
                        self.capped.add(tau)
                        full[k] -= 1
            tau += 1000
        self.left = bucket

    def code(self, t):
        """SM 5.2's code for an uncredited second t of a presence-unknown
        slot: X.bucket_cap where only the 120 s cap kept a session from
        reading, X.agent_only or X.automated while a turn runs, else
        X.no_anchor."""
        if t in self.capped:
            return "X.bucket_cap"
        if t in self.running:
            return "X.automated" if t in self.automated else "X.agent_only"
        return "X.no_anchor"


def check_rows(exp, rp, project_of):
    """Checks every row of exp (a timeline.Expected) against the replay:
    credit by project and reason, uncredited seconds by code, the agent's
    running seconds and which minutes have a bucket."""
    for r in exp.rows:
        a = r["_end"] - r["minutes"] * tl.MINUTE_MS
        credit, unc, agent, with_bucket = {}, {}, 0, 0
        for m in range(a, r["_end"], tl.MINUTE_MS):
            secs = range(m, m + tl.MINUTE_MS, 1000)
            got = [rp.credited[t] for t in secs if t in rp.credited]
            g = sum(1 for t in secs if t in rp.running)
            if not got and not g:
                continue
            with_bucket += 1
            agent += g
            for k, reason in got:
                p = credit.setdefault(project_of[k], {})
                p[reason] = p.get(reason, 0) + 1
            for t in secs:
                if t not in rp.credited:
                    c = rp.code(t)
                    unc[c] = unc.get(c, 0) + 1
        credit = {p: tl.ordered(c, tl.CREDIT_REASONS) for p, c in credit.items()}
        want = r["credit"] if r["bucket"] else {}
        tl.check(credit == want, f"row {r['local']}: the replay credits {credit}, the row {want}")
        tl.check(with_bucket == r.get("bucket_minutes", 0),
                 f"row {r['local']}: {with_bucket} minutes have credit or an agent, the row says "
                 f"{r.get('bucket_minutes', 0)}")
        if not r["bucket"]:
            continue
        tl.check(agent == r["agent_running_s"],
                 f"row {r['local']}: an agent runs {agent} s in the replay, {r['agent_running_s']} s in the row")
        for p, reasons in r["credit"].items():
            for k in reasons:
                tl.check(k in RELAYED_REASONS, f"row {r['local']}: {k} is a reason relayed credit can give")
        unc = tl.ordered(unc, tl.UNCREDITED)
        tl.check(r["uncredited"] == unc, f"row {r['local']}: uncredited {r['uncredited']}, the replay {unc}")


def readme_rows(path, header):
    """The rows of the README's tables that start with header."""
    rows = []
    with open(path, encoding="utf-8") as f:
        table = False
        for line in f:
            cells = [c.strip().replace("`", "") for c in line.strip().strip("|").split("|")]
            if line.startswith(header):
                table = True
                continue
            if not line.startswith("|"):
                table = False
                continue
            if table and not set(cells[0]) <= set("-: "):
                rows.append(cells)
    return rows


def check_readme(path, exp_doc, names):
    """Checks that every row of the README's minute tables says what
    expected.json says for that minute, and that every total's arithmetic
    gives its seconds and the seconds are expected.json's. names maps
    project keys to the names the README uses."""
    by_local = {r["local"]: r for r in exp_doc["rows"]}
    rows = readme_rows(path, "| Minute | Credited | Reasons |")
    tl.check(rows, f"{path} has a minute table")
    seen = set()
    for cells in rows:
        minute, credited, reasons = cells[0], cells[1], cells[2]
        tl.check(minute in by_local, f"{path}: README minute {minute} is a row of expected.json")
        seen.add(minute)
        r = by_local[minute]
        tl.check(float(credited.replace(",", "")) == r.get("credited_s", 0),
                 f"{path}: {minute} credits {credited}, expected.json {r.get('credited_s', 0)}")
        got = {}
        if reasons != "none":
            for item in reasons.split(","):
                parts = item.split()
                p = parts[0] if len(parts) == 3 else None
                key = [k for k, n in names.items() if n == p][0] if p else next(iter(names))
                got.setdefault(key, {})[parts[-2]] = float(parts[-1])
        want = {p: {k: float(v) for k, v in c.items()} for p, c in r.get("credit", {}).items()}
        tl.check(got == want, f"{path}: {minute} has {got} in the README, {want} in expected.json")
    tl.check(seen == set(by_local), f"{path}: the minute table has every row of expected.json")
    tot = exp_doc["totals"]
    trows = readme_rows(path, "| Total | Seconds | Arithmetic |")
    tl.check(any(c[0] == "credited_s" for c in trows), f"{path} has a credited_s total")
    for key, seconds, expr in ((c[0], c[1], c[2]) for c in trows):
        n = int(seconds.replace(",", ""))
        tl.check(lb.arithmetic(expr) == n, f"{path}: {key}: {expr} is {lb.arithmetic(expr)}, not {n}")
        if key in tot and not isinstance(tot[key], dict):
            want = tot[key]
        elif key in tot["credited_by_reason"]:
            want = tot["credited_by_reason"][key]
        elif key in tot["uncredited"]:
            want = tot["uncredited"][key]
        elif key in names.values():
            want = tot["credited_by_project"][[k for k, v in names.items() if v == key][0]]
        else:
            raise SystemExit(f"{path}: total {key} isn't in expected.json")
        tl.check(want == n, f"{path}: total {key} is {n} in the README, {want} in expected.json")


def anchor_entry(fx, relay_event, prompt_event, *, kind, prompt_class, lead_s, sm):
    """anchors.json's entry for a relayed anchor: the relay record behind
    it, its stamp, the prompt it paired with and its label."""
    a = tl.anchor(fx, relay_event, kind=kind, origin="relayed", prompt_class=prompt_class, lead_s=lead_s,
                  trail_s=0, lead_from="params", level="C", sm=sm)
    a["sent_at"] = relay_event["data"]["sent_at"]
    a["local_sent_at"] = fx.hhmmss(relay_event["data"]["sent_at"])
    a["prompt"] = prompt_event["event_id"]
    a["labels"] = ["via_relay"]
    return a


def read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)
