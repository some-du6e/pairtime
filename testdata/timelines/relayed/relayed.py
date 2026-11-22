# SPDX-License-Identifier: AGPL-3.0-or-later
"""Shared code for the relayed fixtures. README.md in this directory says
what they are.

A case's gen.py builds its events with ../timeline.py, writes the rows it
computed by hand from SM 2.2 rule 9, 2.3 and 2.8 as D-relayed has them,
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
# What SM 2.8 leaves open for presence-unknown seconds: the codes of seconds
# the 120 s cap cuts and of seconds nothing covers (its point 2).
LOOKBACK_ERRATUM = "2026-11-19-sm-2.8-lookback"
# Which seconds of an automated turn are X.automated (WP-FX1's 6.6 erratum,
# point 1).
CODES_ERRATUM = "2026-11-19-sm-6.6-uncredited-codes"

# The SM 8 parameters the relayed rules read.
COMPOSE_CPS = Fraction(3, 2)   # characters a second (SM 8.2)
COMPOSE_MAX_S = 300            # cap on a lead's typing part (SM 8.2)
R_TURN_BASE_S = 20             # SM 8.3
READ_WPM, DIFF_LPM, TOOL_LINES_MAX = 180, 8, 4  # SM 8.3
BUCKET_MAX_S = 600             # the cap with presence (SM 8.3)
UNKNOWN_BUCKET_MAX_S = 120     # the cap with presence unknown (SM 8.6)
READ_FOLLOW_S = 600            # the next relayed message within 600 s of the stop (SM 2.8)
RELAY_MATCH_S = 1800           # SM 8.1
RELAY_MIN_GAP_S = 20           # SM 8.1

# Credit reasons a relayed anchor and its reading can give: a prompt's,
# an answer's or a go-ahead's typing lead, and reading after a stop. Never
# SUP: with presence unknown, reading waits for the stop (SM 2.8).
RELAYED_REASONS = ("P", "ANS", "GO", "READ")


def typing_ms(chars):
    """SM 2.8's lead for a relayed message: typing time only,
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
    """SM 2.2 rules 1 to 3, which come before the relay check: the origin a
    prompt's own record declares, or None."""
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
    if ep in ("sdk", "exec") or parent in ("script_runtime", "scheduler", "agent"):
        return True
    return not start["has_tty"] and ep not in ("ide", "desktop") and parent != "ide"


class Replay:
    """D-relayed applied second by second over [start, end), Unix ms on
    whole seconds, on a device whose presence is unknown in every slot: no
    sampler ran, as on a server with no desktop. It knows prompts, relay
    records, outputs, tools and stops.

    A relay record names the prompt it matched in its data's prompt. The
    prompt is relayed when rules 1 to 3 don't label it, the record's session
    is the prompt's (or the record has none), and the prompt came no
    earlier than the record's sent_at and at most RELAY_MATCH_S after it.
    No other prompt in these cases has local input, so rules 6 and 7 never
    hold. A prompt no record names gets rule 4, or human_typed if rule 4
    doesn't take it, which no case has.

    A relayed prompt opens an anchor at its record's sent_at, unless the
    session's last relayed anchor is under RELAY_MIN_GAP_S older. A record
    of app chat_bot that names no prompt opens one by itself, in its thread.
    The anchor's lead is its typing time, capped at the seconds since the
    thread's previous relay record, and it ends at sent_at. Its turn's
    prompt resets the session's bucket to R_TURN_BASE_S; a prompt the gap
    rule kept from opening an anchor resets nothing. Output fills the bucket
    up to UNKNOWN_BUCKET_MAX_S. Reading drains it after one of the
    session's stops, until the session's next relayed prompt is sent or its
    next turn starts, when that prompt is sent within READ_FOLLOW_S of the
    stop. A lead beats reading (SM 3.3 rule 2). With two sessions wanting a
    second, the one whose latest relayed message is the most recent takes
    it (SM 2.8).

    credited maps each credited second to (session or thread, reason).
    running holds the seconds an agent turn ran, automated those in which
    every running turn was automated, capped the seconds nobody wanted that
    only UNKNOWN_BUCKET_MAX_S kept a session from reading, and conflicts
    the seconds two sessions wanted."""

    def __init__(self, events, start, end):
        tl.check(start % 1000 == 0 and end % 1000 == 0, "the replay runs on whole seconds")
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
        tl.check(not any(e["kind"].startswith("os.") for e in events), "no presence or focus record")

        # Which prompt each relay record names, and the origin of every
        # prompt.
        named = {}
        for r in relays:
            p = r["data"].get("prompt")
            if not p:
                continue
            tl.check(p in by_id and by_id[p]["kind"] == "prompt.submit", f"relay {r['event_id']} names a prompt")
            q = by_id[p]
            tl.check(p not in named, f"one relay record names prompt {p}")
            ok = (r["session_key"] in ("none", q["session_key"])
                  and r["data"]["sent_at"] <= q["t"] <= r["data"]["sent_at"] + RELAY_MATCH_S * 1000)
            tl.check(ok, f"relay {r['event_id']} names a prompt of its session within RELAY_MATCH_S")
            named[p] = r
        self.origin, self.relay_of = {}, {}
        for s, ps in prompts.items():
            for q in ps:
                d = declared(q["data"])
                if d:
                    self.origin[q["event_id"]] = d
                elif q["event_id"] in named:
                    self.origin[q["event_id"]] = "relayed"
                    self.relay_of[q["event_id"]] = named[q["event_id"]]
                elif headless(starts[s]):
                    self.origin[q["event_id"]] = "headless"
                else:
                    self.origin[q["event_id"]] = "human_typed"
                    tl.check(False, "no case has a prompt that is neither relayed nor automated")

        # Turns: each prompt runs to the session's next stop.
        self.turns = []  # (session, t_submit, t_stop or None, human)
        for s, ps in prompts.items():
            for q in ps:
                st = [t for t in stops.get(s, []) if t > q["t"]]
                self.turns.append((s, q["t"], st[0] if st else None, self.origin[q["event_id"]] == "relayed"))

        # Anchors, in sent_at order.
        relayed_prompts = sorted(((self.relay_of[p]["data"]["sent_at"], p) for p in self.relay_of))
        last_anchor = {}
        thread_prev = {}
        for r in sorted(relays, key=lambda r: (r["data"]["sent_at"], r["event_id"])):
            thread_prev.setdefault(r["data"]["thread_key"], []).append(r["data"]["sent_at"])
        self.anchors = []  # dicts: id, sent_at, lead_ms, session, thread, prompt, reason
        self.blocked = []
        candidates = [(sa, p, self.relay_of[p]) for sa, p in relayed_prompts]
        candidates += [(r["data"]["sent_at"], None, r) for r in relays
                       if not r["data"].get("prompt") and r["data"]["app"] == "chat_bot"]
        candidates.sort(key=lambda c: (c[0], c[2]["event_id"]))
        for sa, p, r in candidates:
            q = by_id[p] if p else None
            who = q["session_key"] if q else r["data"]["thread_key"]
            if who in last_anchor and sa - last_anchor[who] < RELAY_MIN_GAP_S * 1000:
                self.blocked.append(r["event_id"])
                continue
            last_anchor[who] = sa
            earlier = [t for t in thread_prev[r["data"]["thread_key"]] if t < sa]
            lead = typing_ms(r["data"]["len_chars"])
            if earlier:
                lead = min(lead, sa - earlier[-1])
            tl.check(sa % 1000 == 0, "every relayed message is sent on a whole second")
            reason = "P"
            if q:
                reason = {True: "GO", False: "P"}[q["data"]["low_effort"]]
            else:
                reason = {True: "GO", False: "P"}[r["data"]["low_effort"]]
            self.anchors.append({"id": r["event_id"], "sent_at": sa, "lead_ms": lead, "who": who,
                                 "thread": r["data"]["thread_key"], "prompt": p, "reason": reason})

        # Reading spans: after a stop, to the session's next relayed message
        # or its next turn, when that message comes within READ_FOLLOW_S.
        self.spans = {}
        for s, ts in stops.items():
            sends = sorted(self.relay_of[p]["data"]["sent_at"] for p in self.relay_of
                           if by_id[p]["session_key"] == s)
            starts_after = sorted(t for (s2, t, _, _) in self.turns if s2 == s)
            for stop in ts:
                nxt = [t for t in sends if t > stop]
                if not nxt or nxt[0] - stop > READ_FOLLOW_S * 1000:
                    continue
                turn_next = [t for t in starts_after if t > stop]
                end_at = min([nxt[0]] + turn_next)
                self.spans.setdefault(s, []).append((stop, end_at))

        # Bucket changes in time order: an anchor's prompt resets its
        # session's bucket when the agent takes the prompt up; output fills
        # it up to the cap.
        changes = []
        for a in self.anchors:
            if a["prompt"]:
                changes.append((by_id[a["prompt"]]["t"], 0, by_id[a["prompt"]]["session_key"], None))
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

        sessions = set(prompts)
        bucket = {k: Fraction(0) for k in sessions}
        full = {k: Fraction(0) for k in sessions}
        self.credited, self.conflicts, self.capped = {}, [], set()
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
                        cands.setdefault(a["who"], []).append((1, a["sent_at"], a["reason"]))
                for k, sp in self.spans.items():
                    if bucket[k] > 0 and any(x <= tau < y for x, y in sp):
                        cands.setdefault(k, []).append((2, 0, "READ"))
                if cands:
                    win = next(iter(cands))
                    if len(cands) > 1:
                        latest = {}
                        for k in cands:
                            ts = [a["sent_at"] for a in self.anchors if a["who"] == k and a["sent_at"] <= tau]
                            latest[k] = max(ts) if ts else -1
                        win = max(latest, key=latest.get)
                        self.conflicts.append((tau, sorted(cands), win))
                    best = sorted(cands[win])[0]
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


# The codes a row lists for each kind of uncredited second no rule settles
# yet (README.md): a second only automated turns ran in (WP-FX1's 6.6
# erratum, point 1), one the 120 s cap cut and one nothing covers (the
# lookback erratum's point 2, whose proposal for the last is X.no_anchor).
OPEN_CODES = {"automated": {"X.automated"}, "capped": {"X.bucket_cap"}, "uncovered": {"X.no_anchor"}}


def second_kind(rp, t):
    """The kind of an uncredited second t: agent when a turn a person
    started runs, automated when only automated turns run, else capped when
    the 120 s cap cut a session's reading there, else uncovered."""
    if t in rp.running:
        return "automated" if t in rp.automated else "agent"
    return "capped" if t in rp.capped else "uncovered"


def check_rows(exp, rp, project_of):
    """Checks every row of exp (a timeline.Expected) against the replay:
    credit by project and reason, the agent's running seconds, which minutes
    have a bucket, and the uncredited seconds. A second in a turn a person
    started that nothing credits is X.agent_only. A row with any other
    uncredited second has uncredited None and lists OPEN_CODES' codes for
    the kinds of second it holds."""
    for r in exp.rows:
        a = r["_end"] - r["minutes"] * tl.MINUTE_MS
        credit, agent, with_bucket, idle, kinds = {}, 0, 0, 0, set()
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
                if t in rp.credited:
                    continue
                k = second_kind(rp, t)
                if k == "agent":
                    idle += 1
                else:
                    kinds.add(k)
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
        if kinds:
            tl.check(r["uncredited"] is None and r.get("erratum"),
                     f"row {r['local']}: {sorted(kinds)} seconds have no code a rule settles, so uncredited "
                     f"is null and the row names an erratum")
            want_codes = set().union(*(OPEN_CODES[k] for k in kinds)) | ({"X.agent_only"} if idle else set())
            tl.check(set(r["codes"]) == want_codes and len(r["codes"]) == len(want_codes),
                     f"row {r['local']}: codes {r['codes']}, but its seconds are {sorted(want_codes)}")
        else:
            want_unc = {"X.agent_only": idle} if idle else {}
            tl.check(r["uncredited"] == want_unc,
                     f"row {r['local']}: uncredited {r['uncredited']}, the replay {want_unc}")


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
    for cells in rows:
        minute, credited, reasons = cells[0], cells[1], cells[2]
        tl.check(minute in by_local, f"{path}: README minute {minute} is a row of expected.json")
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


def write_extra(out, name, doc):
    """Writes one more JSON file beside the four timeline files."""
    with open(os.path.join(out, name), "w", encoding="utf-8", newline="\n") as f:
        f.write(tl.pretty(doc) + "\n")


def read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)
