# SPDX-License-Identifier: AGPL-3.0-or-later
"""Shared code for the lookback fixtures. README.md in this directory says
what they are.

A case's gen.py builds its events with ../timeline.py, writes the rows it
computed by hand from SM 2.8, and checks them against replay() before it
writes anything. replay() is a second, mechanical reading of the same
rules, second by second, written only to catch slips in the hand
arithmetic. It isn't the scorer, and the rows, not replay(), are the
oracle.

Standard library only.
"""

import json
import os
from fractions import Fraction

import timeline as tl

HERE = os.path.dirname(os.path.abspath(__file__))
TIMELINES = os.path.dirname(HERE)

# The erratum for what SM 2.8 leaves open. Rows that depend on it name it.
ERRATUM = "2026-11-19-sm-2.8-lookback"
# WP-FX1's erratum on which seconds are X.automated (its point 1).
CODES_ERRATUM = "2026-11-19-sm-6.6-uncredited-codes"

# The SM 8 parameters the lookback rules read.
COMPOSE_CPS = Fraction(3, 2)   # characters a second (SM 8.2)
COMPOSE_MAX_S = 300            # cap on a lead's typing part (SM 8.2)
UNKNOWN_PA_MAX_S = 60          # permission-wait cap in lookback (SM 8.6)
PA_TRAIL_S = 10                # SM 8.2
PA_MIN_LATENCY_MS = 300        # SM 8.1
UNKNOWN_BUCKET_MAX_S = 120     # reading cap per turn in lookback (SM 8.6)
READ_FOLLOW_S = 600            # "a human prompt that follows within 600 s" (SM 2.8)
R_TURN_BASE_S = 20             # SM 8.3
READ_WPM, DIFF_LPM, TOOL_LINES_MAX = 180, 8, 4  # SM 8.3

# The event kinds a transcript reader can write (SM 1.2, "Filled from").
HARNESS_KINDS = ("session.start", "session.end", "prompt.submit", "agent.output", "agent.tool",
                 "agent.permission", "agent.interrupt", "agent.queue", "agent.stop", "agent.subagent")

# Credit reasons lookback can give: prompt leads, permission answers and
# reading after a stop. No editor, shell or git events, no presence, so no
# E, NAV, CMD, TEST, G, LOOKUP, and no reading while the agent runs (SUP).
LOOKBACK_REASONS = ("P", "ANS", "GO", "Q", "PA", "INT", "READ")


def read_events(name):
    """The events of the live fixture testdata/timelines/<name>, in order."""
    with open(os.path.join(TIMELINES, name, "events.jsonl"), encoding="utf-8") as f:
        return [json.loads(line) for line in f]


def read_prompts(name):
    with open(os.path.join(TIMELINES, name, "prompts.json"), encoding="utf-8") as f:
        return json.load(f)["prompts"]


def transcript_only(fx, live):
    """Adds to fx the harness events of a live fixture as the transcript
    reader writes them: the same times and data, with source and via log,
    because no hook ran before pairtime was installed. OS, editor and git
    events are left out: no transcript holds them. Returns how many events
    it kept."""
    kept = 0
    for e in live:
        if e["harness"] == "none":
            continue
        tl.check(e["kind"] in HARNESS_KINDS, f"{e['kind']} is a harness event")
        fx.emit(e["t"], e["kind"], e["data"], source="log", via="log", harness=e["harness"],
                session=e["session_key"], project=e.get("project_key"), version=e.get("harness_version"))
        kept += 1
    return kept


def typing_ms(typed_len_est):
    """SM 2.8: a prompt's lead is its typing time only, typed_len_est /
    COMPOSE_CPS capped at COMPOSE_MAX_S, with no think lead. Rounded up to
    a whole second, so a lead ending on a whole second starts on one: SM
    rounds 6.4's 160.67 s lead to 161 s the same way (WP-FX1's 6.4
    prompt-lengths erratum asks Karim which way edges round)."""
    s = min(Fraction(typed_len_est) / COMPOSE_CPS, COMPOSE_MAX_S)
    return -((-s.numerator) // s.denominator) * 1000


def fill_s(kind, data):
    """SM 2.4's fill for one output or tool record, in seconds."""
    if kind == "agent.output":
        return Fraction(60 * data["words"], READ_WPM) + Fraction(60 * data["code_lines"], DIFF_LPM)
    if kind == "agent.tool":
        lines = data["lines_added"] + data["lines_removed"] + min(data["visible_output_lines"], TOOL_LINES_MAX)
        return Fraction(60 * lines, DIFF_LPM)
    return Fraction(0)


def origin(start, ps):
    """SM 2.2 rules 1, 3, 4 and 5 for a prompt.submit, then human_typed for
    every other prompt (SM 2.8). Rule 2's continuations have no prompt of
    their own; Session finds them from agent.stop."""
    if ps["matches_pending_cron"] or ps["origin_declared"] in ("cron", "loop"):
        return "scheduled"
    if ps["origin_declared"] == "subagent":
        return "subagent"
    ep, parent = start["entrypoint"], start["parent_class"]
    if ep in ("sdk", "exec") or parent in ("script_runtime", "scheduler", "agent"):
        return "headless"
    if not start["has_tty"] and ep not in ("ide", "desktop"):
        return "headless"
    if ep == "cloud":
        return "cloud"
    return "human_typed"


class Session:
    """One harness session's records, as the lookback rules read them."""

    def __init__(self, events, key):
        mine = [e for e in events if e["session_key"] == key]
        starts = [e for e in mine if e["kind"] == "session.start"]
        tl.check(len(starts) == 1, f"session {key} has one session.start")
        self.key = key
        self.project = starts[0]["project_key"]
        self.start = starts[0]["data"]
        self.prompts = []      # (t, typed_len_est) of human prompts
        self.automated = []    # (t, origin) of prompts rules 1 to 5 label
        self.turns = []        # [t_start, t_stop or None, human]
        self.stops = []
        self.permissions = []  # (requested_at, answered_at), decided by the user
        self.fills = []        # (t, seconds)
        running = None
        for e in mine:
            k, d, t = e["kind"], e["data"], e["t"]
            if k == "prompt.submit":
                o = origin(self.start, d)
                if o == "human_typed":
                    self.prompts.append((t, d["typed_len_est"]))
                else:
                    self.automated.append((t, o))
                tl.check(running is None, f"session {key}: a prompt at {t} while a turn runs")
                running = [t, None, o == "human_typed"]
            elif k == "agent.stop":
                self.stops.append(t)
                if d["stop_hook_active"]:
                    # SM 2.2 rule 2: a stop-hook continuation began at the stop
                    # before it, with no prompt.
                    tl.check(running is None and self.turns, f"session {key}: a continuation stops at {t}")
                    self.turns.append([self.turns[-1][1], t, False])
                else:
                    tl.check(running is not None, f"session {key}: a stop at {t} ends no turn")
                    running[1] = t
                    self.turns.append(running)
                    running = None
            elif k == "agent.permission":
                if d["decided_by"] == "user" and d["answered_at"] - d["requested_at"] >= PA_MIN_LATENCY_MS:
                    self.permissions.append((d["requested_at"], d["answered_at"]))
            elif k in ("agent.output", "agent.tool"):
                self.fills.append((t, fill_s(k, d)))
        if running is not None:
            self.turns.append(running)

    def last_prompt(self, tau):
        ts = [t for t, _ in self.prompts if t <= tau]
        return max(ts) if ts else None


def sessions_of(events):
    keys = []
    for e in events:
        if e["session_key"] != "none" and e["session_key"] not in keys:
            keys.append(e["session_key"])
    return {k: Session(events, k) for k in keys}


class Replay:
    """SM 2.8 applied second by second over [start, end), Unix ms on whole
    seconds, to events whose slots all have presence unknown.

    Candidates for a second: the typing lead of a human prompt; a permission
    answer's wait, up to UNKNOWN_PA_MAX_S before the answer, and its
    PA_TRAIL_S trail; and reading from the session's bucket, which holds at
    most UNKNOWN_BUCKET_MAX_S of a turn's output, between one of its stops
    and its next human prompt when that prompt comes within READ_FOLLOW_S of
    the stop. When two sessions have candidates, the one with the most
    recent human prompt wins. Within a session an anchor window beats
    reading, and the lead of the next anchor beats the trail of an earlier
    one (SM 3.3).

    credited maps each credited second to (session, reason). running holds
    the seconds an agent turn ran, automated the seconds in which every
    running turn was automated, and conflicts every second two sessions
    wanted, as (second, {session: [reason, part]}, winner)."""

    def __init__(self, events, start, end):
        tl.check(start % 1000 == 0 and end % 1000 == 0, "the replay runs on whole seconds")
        self.sessions = sessions_of(events)
        S = self.sessions
        windows = []  # (from, to, session, reason, part)
        for s in S.values():
            for t, typed in s.prompts:
                windows.append((t - typing_ms(typed), t, s.key, "P", "lead"))
            for req, ans in s.permissions:
                wait = min(ans - req, UNKNOWN_PA_MAX_S * 1000)
                windows.append((ans - wait, ans, s.key, "PA", "lead"))
                windows.append((ans, ans + PA_TRAIL_S * 1000, s.key, "PA", "trail"))
        for a, b, *_ in windows:
            tl.check(a % 1000 == 0 and b % 1000 == 0, f"window {a} to {b} on whole seconds")
        spans = {}
        for s in S.values():
            spans[s.key] = []
            for stop in s.stops:
                later = [t for t, _ in s.prompts if t > stop]
                if later and later[0] - stop <= READ_FOLLOW_S * 1000:
                    spans[s.key].append((stop, later[0]))
        self.spans = spans
        self.running, self.automated = set(), set()
        for tau in range(start, end, 1000):
            turns = [human for s in S.values() for a, b, human in s.turns if a <= tau < (b if b else end)]
            if turns:
                self.running.add(tau)
                if not any(turns):
                    self.automated.add(tau)
        # Bucket changes in time order: a human prompt resets its session's
        # bucket to the base, output fills it up to the per-turn cap.
        changes = []
        for s in S.values():
            changes += [(t, 0, s.key, None) for t, _ in s.prompts]
            changes += [(t, 1, s.key, x) for t, x in s.fills]
        changes.sort(key=lambda c: (c[0], c[1]))
        bucket = {k: Fraction(0) for k in S}
        self.credited, self.conflicts = {}, []
        i = 0
        tau = min([start] + [c[0] - c[0] % 1000 for c in changes])
        while tau < end:
            # Records in a second come before its credit: a fill at a stop is
            # in the bucket when reading starts there.
            while i < len(changes) and changes[i][0] <= tau:
                _, what, k, x = changes[i]
                bucket[k] = Fraction(R_TURN_BASE_S) if what == 0 else min(bucket[k] + x, UNKNOWN_BUCKET_MAX_S)
                i += 1
            if tau >= start:
                cands = {}
                for a, b, k, reason, part in windows:
                    if a <= tau < b:
                        cands.setdefault(k, []).append((1, 0 if part == "lead" else 1, reason, part))
                for k, sp in spans.items():
                    if bucket[k] > 0 and any(a <= tau < b for a, b in sp):
                        cands.setdefault(k, []).append((2, 0, "READ", "bucket"))
                if cands:
                    win = next(iter(cands))
                    if len(cands) > 1:
                        last = {k: S[k].last_prompt(tau) for k in cands}
                        tl.check(None not in last.values() and len(set(last.values())) == len(last),
                                 f"at {tau} every session that wants the second has a prompt, and no two tie")
                        win = max(last, key=last.get)
                        self.conflicts.append((tau, {k: [c[2:] for c in sorted(v)] for k, v in cands.items()},
                                               win))
                    best = sorted(cands[win])[0]
                    self.credited[tau] = (win, best[2])
                    if best[2] == "READ":
                        tl.check(bucket[win] >= 1, f"a whole second to read at {tau}")
                        bucket[win] -= 1
            tau += 1000
        self.left = bucket


def check_rows(exp, rp, project_of_session):
    """Checks every row of exp (a timeline.Expected) against the replay:
    credit by project and reason, the agent's running seconds, which minutes
    have a bucket, and the uncredited seconds. Uncredited seconds are
    X.agent_only when an agent turn a person started ran and nothing
    credited the second; a row with any other uncredited second has
    uncredited None, because SM decides no code for it (README.md)."""
    for r in exp.rows:
        a = r["_end"] - r["minutes"] * tl.MINUTE_MS
        credit, agent, with_bucket, idle, open_s = {}, 0, 0, 0, 0
        for m in range(a, r["_end"], tl.MINUTE_MS):
            secs = range(m, m + tl.MINUTE_MS, 1000)
            got = [rp.credited[t] for t in secs if t in rp.credited]
            g = sum(1 for t in secs if t in rp.running)
            if not got and not g:
                continue
            with_bucket += 1
            agent += g
            for k, reason in got:
                p = credit.setdefault(project_of_session[k], {})
                p[reason] = p.get(reason, 0) + 1
            for t in secs:
                if t in rp.credited:
                    continue
                if t in rp.running and t not in rp.automated:
                    idle += 1
                else:
                    open_s += 1
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
                tl.check(k in LOOKBACK_REASONS, f"row {r['local']}: {k} is a reason lookback can credit")
        if open_s:
            tl.check(r["uncredited"] is None and r.get("erratum"),
                     f"row {r['local']}: {open_s} s have no code SM decides, so uncredited is null and the "
                     f"row names an erratum")
        else:
            want_unc = {"X.agent_only": idle} if idle else {}
            tl.check(r["uncredited"] == want_unc,
                     f"row {r['local']}: uncredited {r['uncredited']}, the replay {want_unc}")


def readme_minute_rows(path):
    """The rows of the README's minute tables: (minute, credited, reasons)."""
    rows = []
    with open(path, encoding="utf-8") as f:
        table = False
        for line in f:
            cells = [c.strip().replace("`", "") for c in line.strip().strip("|").split("|")]
            if line.startswith("| Minute | Credited | Reasons |"):
                table = True
                continue
            if not line.startswith("|"):
                table = False
                continue
            if table and not set(cells[0]) <= set("-: "):
                rows.append((cells[0], cells[1], cells[2]))
    return rows


def check_readme(path, exp_doc, names):
    """Checks that every row of the README's minute tables says what
    expected.json says for that minute. names maps project keys to the
    names the README uses. The Go test in internal/timelines checks the
    same, and the totals arithmetic."""
    by_local = {r["local"]: r for r in exp_doc["rows"]}
    rows = readme_minute_rows(path)
    tl.check(rows, f"{path} has a minute table")
    for minute, credited, reasons in rows:
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
