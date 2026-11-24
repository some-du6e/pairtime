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
import re
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
PENDING_WINDOW_S = 900         # DESIGN 6.2 D1: a minute stays open 900 s after it ends
# SM 2.4: a message vouches for a stop's reading only if it pairs while the
# stop's minute is still pending, within PENDING_WINDOW_S - 60 s of the stop.
VOUCH_PAIR_S = PENDING_WINDOW_S - 60

# Credit reasons relayed credit can give: a prompt's, an answer's or a
# go-ahead's typing lead, and reading after a stop. Never SUP: a relayed
# session reads only after a stop (SM 2.4).
RELAYED_REASONS = ("P", "ANS", "GO", "READ")
# SM 5.2's codes for a slot whose presence is unknown, in their order, but
# X.no_input, which needs a no_input turn, and no case has one.
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
    key presses before it. agent_app is on rule 4's list: a session an agent
    app's server runs is headless unless an app message witnesses its
    prompt."""
    ep, parent = start["entrypoint"], start["parent_class"]
    if ep in ("sdk", "exec", "mcp") or parent in ("script_runtime", "scheduler", "agent", "agent_app"):
        return True
    return not start["has_tty"] and ep not in ("ide", "desktop") and parent != "ide"


class Replay:
    """D-relayed applied second by second over [start, end), Unix ms on
    whole seconds, on a device that sampled no presence and recorded no
    focus, as a server with no desktop. It knows sessions, prompts, relay
    records, outputs, tools and stops. With desktop=True the device has
    presence and focus records, which Replay ignores: relayed credit reads
    none of them (SM 2.3), every relay record there must come from T3's
    phone app, which skips rules 6 and 7, and every other prompt must be
    one rule 4 takes. The case itself checks that the person was present
    with a window hosting the session in front throughout, so no presence
    or focus code applies, and that no second is X.bucket_cap.

    Pairing (SM 2.2). A relay record and a prompt.submit pair when one names
    the other (the record's prompt or the prompt's relay), the record's
    session is the prompt's or none, the prompt came no earlier than the
    record's sent_at and at most RELAY_MATCH_S after it, and the record came
    at most RELAY_SEEN_LAG_S after sent_at. A record of declared cron or
    cross_session gives its prompt rule 1's or rule 3's origin. One of
    declared none witnesses it only in a session whose parent_class is
    agent_app (rule 9), and with no local input, or from the phone app,
    rules 6 and 7 never hold, so the prompt is relayed. A record that pairs
    with nothing opens nothing. Every other prompt goes by rules 1 to 4,
    and agent_app is on rule 4's list, so the cases have no prompt that
    rule 4 lets through.

    A relayed prompt's anchor is its record, at the record's sent_at. Its
    lead is the typing time of the record's len_chars, at most the hook's
    typed_len_est, capped at the seconds since the thread's previous record
    of declared none, and it ends at sent_at. The prompt resets the
    session's bucket to R_TURN_BASE_S. Output fills the bucket up to
    UNKNOWN_BUCKET_MAX_S from the session's first fill, whatever reset came
    last, and full beside it up to BUCKET_MAX_S, so what full holds past the
    bucket is what only the 120 s cap keeps from reading (SM 2.4, 5.2).
    Reading drains the bucket after one of the session's stops, until the
    relayed message that vouches for the stop is sent or the session's next
    turn starts, whichever is first. The one that vouches is the first of
    the thread's relayed messages sent after the stop whose pairing, the
    later of its record and its prompt, comes within VOUCH_PAIR_S of the
    stop, and it vouches only when its sent_at comes within READ_FOLLOW_S
    after the stop. One that pairs later keeps its own lead, but it neither
    vouches for the stop's reading nor ends it: late lists it as (session,
    stop, sent_at) when it is sent within READ_FOLLOW_S after the stop and
    before the one that vouches, if any. A lead beats reading (SM 3.3 rule
    2). Two sessions never want one second here; Replay checks that.

    credited maps each credited second to (session, reason). running holds
    the seconds an agent turn ran, automated those in which every running
    turn was automated, and capped the seconds nobody wanted that only
    UNKNOWN_BUCKET_MAX_S kept a session from reading."""

    def __init__(self, events, start, end, desktop=False):
        tl.check(start % 1000 == 0 and end % 1000 == 0, "the replay runs on whole seconds")
        if not desktop:
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

        # Origins.
        self.origin, self.relay_of = {}, {}
        for s, ps in prompts.items():
            for q in ps:
                r = pair.get(q["event_id"])
                own = declared(q["data"])
                app = {"cron": "scheduled", "cross_session": "subagent"}.get(r["data"]["declared"]) if r else None
                if own or app:
                    self.origin[q["event_id"]] = own or app
                elif q["data"].get("guest"):
                    # SM 2.2 rule 4 takes a guest's prompt whatever its
                    # session, even when a message witnesses it, and rule 9
                    # needs a prompt that isn't marked guest.
                    self.origin[q["event_id"]] = "headless"
                elif r and starts.get(s, {}).get("parent_class") == "agent_app":
                    tl.check(r["data"]["client"] in ("web", "mobile"), f"relay {r['event_id']} names its client")
                    tl.check(not desktop or r["data"]["client"] == "mobile",
                             "with presence records, Replay scores only phone messages; rules 6 and 7 decide the rest")
                    self.origin[q["event_id"]] = "relayed"
                    self.relay_of[q["event_id"]] = r
                elif s in starts and headless(starts[s]):
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
            # SM 2.2: the length never exceeds the hook's typed_len_est.
            lead = typing_ms(min(r["data"]["len_chars"], q["data"]["typed_len_est"]))
            if earlier:
                lead = min(lead, sa - earlier[-1])
            reason = "GO" if r["data"]["low_effort"] else "P"
            self.anchors.append({"id": r["event_id"], "sent_at": sa, "lead_ms": lead, "session": q["session_key"],
                                 "prompt": pid, "reason": reason, "thread": r["data"]["thread_key"],
                                 "paired_at": max(r["t"], q["t"])})

        # Reading spans. SM 2.4: a stop's reading is vouched for by the first
        # relayed anchor of the thread sent after the stop among those that
        # pair within VOUCH_PAIR_S of it, if that one is sent within
        # READ_FOLLOW_S of the stop. One that pairs later neither vouches nor
        # ends the reading.
        self.spans, self.late = {}, []
        for s, ts in stops.items():
            # The thread's relayed anchors, whichever session their prompts are in.
            threads = {a["thread"] for a in self.anchors if a["session"] == s}
            sends = sorted((a["sent_at"], a["paired_at"]) for a in self.anchors if a["thread"] in threads)
            turn_starts = sorted(t for (s2, t, _, _) in self.turns if s2 == s)
            for stop in ts:
                vouch = None
                for t, p in sends:
                    if t <= stop:
                        continue
                    if t - stop > READ_FOLLOW_S * 1000:
                        break
                    if p - stop > VOUCH_PAIR_S * 1000:
                        self.late.append((s, stop, t))
                        continue
                    vouch = t
                    break
                if vouch is None:
                    continue
                end_at = min([vouch] + [t for t in turn_starts if t >= stop])
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
        """SM 5.2's code for an uncredited second t of a live slot whose
        presence is unknown: X.bucket_cap where only the 120 s cap kept a
        session from reading, else X.agent_only while a turn a person started
        runs, else X.automated while an automated turn runs, else X.no_anchor.
        SM 5.2 puts X.no_input before X.no_anchor, for a no_input turn's run,
        but no case has one: Replay refuses a prompt that rule 4 lets
        through."""
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


# An uncredited code with its seconds in a README's Arithmetic cell:
# "X.agent_only 59", "X.agent_only 2 × 60", "X.no_anchor 1 + 9",
# "X.automated 30 + 24 × 60 + 22 = 1,492".
_NUM = r"\d{1,3}(?:,\d{3})+|\d+"
_CODE_ITEM = re.compile(r"(X\.[a-z_]+) ((?:%s)(?: ?[+×] ?(?:%s))*)(?: ?= ?(%s))?" % (_NUM, _NUM, _NUM))
_BUCKET_MINUTES = re.compile(r"(?:(\d+) bucket minutes|[Bb]uckets in the (\d+) minutes)")


def readme_codes(cell):
    """The uncredited seconds an Arithmetic cell states, by code, summed
    over its mentions. A code named without seconds states nothing."""
    out = {}
    for code, expr, total in _CODE_ITEM.findall(cell):
        n = lb.arithmetic(expr)
        if total:
            tl.check(n == int(total.replace(",", "")), f"README arithmetic {expr} = {total}")
        out[code] = out.get(code, 0) + n
    return out


def check_readme(path, exp_doc, names):
    """Checks that every row of the README's minute tables says what
    expected.json says for that minute: its credit and reasons, the seconds
    its Arithmetic cell gives any uncredited code, and its claims about
    buckets ("no bucket" in a one-minute row, "N bucket minutes" or
    "Buckets in the N minutes"). It also checks that every total's
    arithmetic gives its seconds and the seconds are expected.json's. names
    maps project keys to the names the README uses."""
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
        arith = cells[3] if len(cells) > 3 else ""
        unc = r.get("uncredited", {}) if r.get("bucket") else {}
        for code, n in readme_codes(arith).items():
            tl.check(unc.get(code, 0) == n,
                     f"{path}: {minute} gives {code} {n} s in the README, {unc.get(code, 0)} in expected.json")
        if r.get("minutes", 1) == 1 and re.search(r"\bno bucket\b", arith):
            tl.check(not r.get("bucket"), f"{path}: {minute} has a bucket, though the README says it has none")
        for a, b in _BUCKET_MINUTES.findall(arith):
            n = int(a or b)
            tl.check(r.get("bucket_minutes", 0) == n,
                     f"{path}: {minute} has {r.get('bucket_minutes', 0)} bucket minutes, the README says {n}")
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
    """anchors.json's entry for a relayed anchor. Its event is the relay
    record, and its t is the record's sent_at, the app's stamp, where the
    lead ends (SM 2.3), not the record's own t. It names the prompt it
    paired with and carries its label."""
    a = tl.anchor(fx, relay_event, kind=kind, origin="relayed", prompt_class=prompt_class, lead_s=lead_s,
                  trail_s=0, lead_from="params", level="C", sm=sm)
    a["t"] = relay_event["data"]["sent_at"]
    a["local"] = fx.hhmmss(relay_event["data"]["sent_at"])
    a["prompt"] = prompt_event["event_id"]
    a["labels"] = ["via_relay"]
    return a


def read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)
