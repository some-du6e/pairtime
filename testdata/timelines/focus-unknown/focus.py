# SPDX-License-Identifier: AGPL-3.0-or-later
"""Shared code for the focus-unknown fixtures. README.md in this directory
says what they are.

A case's gen.py builds its events with ../timeline.py and writes down, by
hand, what SM 2.5 credits: runs of seconds, each with its project and
reason or its uncredited code, and the runs whose credit is level C. From
those runs it writes the rows of expected.json and the slots of slots.json.
Before it writes anything it checks them against Replay, a second-by-second
reading of SM 2.1 to 2.7 and 5.2 for the few constructs these cases use,
written only to catch slips in the hand arithmetic. Replay isn't the
scorer, and the runs gen.py writes, not Replay, are the oracle.

Standard library only.
"""

import os
import re
from fractions import Fraction

import timeline as tl

HERE = os.path.dirname(os.path.abspath(__file__))

# The SM 8 parameters these cases read.
SLOT_MS = tl.SLOT_MS
SECOND = 1000
PRESENCE_GAP_S = 120           # SM 8.1
SLEEP_GAP_S = 30               # SM 8.1
KEYS_PRE_S = 10                # SM 8.1
TYPING_MAX_CPS = 15            # SM 8.1
PA_MIN_LATENCY_MS = 300        # SM 8.1
THINK_LEAD_S = 120             # SM 8.2
COMPOSE_CPS = Fraction(3, 2)   # SM 8.2
COMPOSE_MAX_S = 300            # SM 8.2
PA_MAX_S, PA_TRAIL_S = 120, 10  # SM 8.2
EDIT_LEAD_S = 120              # SM 8.2
COMMIT_LEAD_S = 60             # SM 8.2
R_TURN_BASE_S = 20             # SM 8.3
READ_WPM, DIFF_LPM, TOOL_LINES_MAX = 180, 8, 4  # SM 8.3
BUCKET_MAX_S = 600             # SM 8.3
BUCKET_TTL_S = 7_200           # SM 8.3
READ_HORIZON_S = 600           # SM 8.3

# The apps whose windows SM 2.5 realizes windows in, once they resolve to a
# project, and the label of credit that is level C because focus was
# unknown.
PROJECT_APPS = ("terminal", "editor", "agent_app")
FOCUS_UNKNOWN = "focus_unknown"
HUMAN = ("human_typed", "human_pasted")
AUTOMATED = ("scheduled", "self_continue", "subagent", "headless", "cloud")


def ms_of(v):
    """A presence field as integer milliseconds."""
    if isinstance(v, tl.Seconds):
        return v.ms
    return round(v * 1000)


def typing_ms(typed_len_est):
    """A lead's typing part: typed_len_est / COMPOSE_CPS, capped at
    COMPOSE_MAX_S, rounded to the nearest second, halves up (SM 1.3)."""
    s = min(Fraction(typed_len_est) / COMPOSE_CPS, COMPOSE_MAX_S)
    return int(s + Fraction(1, 2)) * SECOND


def fill_s(kind, data):
    """SM 2.4's fill for one output or tool record, in seconds."""
    if kind == "agent.output":
        return Fraction(60 * data["words"], READ_WPM) + Fraction(60 * data["code_lines"], DIFF_LPM)
    if kind == "agent.tool":
        lines = data["lines_added"] + data["lines_removed"] + min(data["visible_output_lines"], TOOL_LINES_MAX)
        return Fraction(60 * lines, DIFF_LPM)
    return Fraction(0)


class Anchor:
    """A human act that passed its evidence test (SM 2.3)."""

    def __init__(self, e, kind, reason, *, lead_ms, typing_ms=0, trail_ms=0, unknown_only=False, origin=None,
                 prompt_class=None):
        self.event = e
        self.t = e["t"]
        self.project = e["project_key"]
        self.session = e["session_key"]
        self.kind, self.reason = kind, reason
        self.lead_ms, self.typing_ms, self.trail_ms = lead_ms, typing_ms, trail_ms
        self.origin, self.prompt_class = origin, prompt_class
        # An anchor whose evidence test names focus and passed only because
        # the window in front was unknown is level C and labeled so (SM 2.5).
        # Every other anchor here is level B: these machines report no
        # keyboard-specific input (SM 5.1, DESIGN 6.4).
        self.labels = [FOCUS_UNKNOWN] if unknown_only else []
        self.level = "C" if unknown_only else "B"

    def in_lead(self, tau):
        return self.t - self.lead_ms <= tau < self.t

    def in_trail(self, tau):
        return self.t <= tau < self.t + self.trail_ms


class Turn:
    def __init__(self, session, project, turn_key, start, stop, origin):
        self.session, self.project, self.turn_key = session, project, turn_key
        self.start, self.stop, self.origin = start, stop, origin

    def running(self, tau):
        return self.start <= tau < self.stop


class Replay:
    """SM 2.1 to 2.7 and 5.2 applied second by second over [start, end),
    Unix ms on whole minutes, to the records of one case.

    What it reads: presence from the samples, one present value a slot, yes
    or bridged, because no case lets a silence reach PRESENCE_GAP_S; focus
    a slot from the os.focus records, the window that held the slot longest
    and the earlier on a tie, with os.focus records of one app_class and
    focus_project counting as one window (SM 2.5); prompts and their origin
    by SM 2.2 rules 4, 6 and 8, with rule 6's focus test passing on unknown
    focus; editor, permission and commit anchors by SM 2.3's tests; turns
    from each prompt to its stop; and each session's reading bucket by SM
    2.4.

    For each second it collects the candidates SM 2.7 lists, applies SM 3.3
    rule 1 unless the slot's focus is unknown, then rule 2, then rule 3 for
    anchor windows (the lead of the next anchor at or after the second),
    and for buckets of different projects SM 2.5's rule: the project whose
    qualifying human anchor is nearest the second, the earlier anchor on a
    tie, then the lower project_key. It measures nearest both from the
    second's start and from its middle, and fails where the two readings
    pick different projects. An uncredited second takes SM 5.2's first code
    that applies, with X.other_focus and X.ambiguous skipped and every
    project's running turns read where focus is unknown.

    credit maps each credited second to (project, reason, level, labels),
    code each uncredited second to its code, running holds the seconds an
    agent turn ran, and contested lists the seconds several projects
    wanted, as (second, {project: kinds}, winner, rule)."""

    def __init__(self, events, start, end):
        tl.check(start % tl.MINUTE_MS == 0 and end % tl.MINUTE_MS == 0, "the replay covers whole minutes")
        self.start, self.end = start, end
        self.samples = []   # (t, last input)
        self.focus = []     # (t, app_class, focus_project, hosted_sessions)
        self.sessions = {}  # session_key -> session.start fields and project
        for e in events:
            k, d = e["kind"], e["data"]
            if k == "os.presence":
                tl.check(d["idle_keys_s"] is None, "no case's machine reports keyboard input apart (idle_keys_s null)")
                tl.check(not d["locked"], "no case locks the screen")
                self.samples.append((e["t"], e["t"] - ms_of(d["idle_any_s"])))
            elif k == "os.focus":
                self.focus.append((e["t"], d["app_class"], d.get("focus_project"), tuple(d.get("hosted_sessions", ()))))
            elif k == "session.start":
                self.sessions[e["session_key"]] = dict(d, project=e["project_key"])
            elif k in ("os.power", "agent.queue", "agent.interrupt", "shell.command", "cli.track"):
                raise SystemExit(f"check failed: no case has {k}")
        tl.check(self.focus and self.focus[0][0] < start, "an os.focus is in force before the window")
        self.input_slots = {t - t % SLOT_MS for _, t in self.samples}
        self._check_samples()
        self.present = {s: self._present(s) for s in range(start, end, SLOT_MS)}
        self.slot_focus = {s: self._slot_focus(s) for s in range(start, end, SLOT_MS)}
        self._anchors(events)
        self._turns(events)
        self._score(events)

    # Presence and focus.

    def _check_samples(self):
        times = [t for t, _ in self.samples]
        for s in range(self.start, self.end + SLOT_MS, SLOT_MS):
            tl.check(s in times, f"a sample at the slot boundary {s}")
        near = [t for t in times if self.start - PRESENCE_GAP_S * SECOND <= t <= self.end]
        for a, b in zip(near, near[1:]):
            tl.check(b - a <= SLEEP_GAP_S * SECOND, f"no two samples near the window are more than 30 s apart ({a}, {b})")

    def _present(self, slot):
        if slot in self.input_slots:
            return "yes"
        a = b = slot
        while a - SLOT_MS not in self.input_slots and slot - a < 12 * SLOT_MS:
            a -= SLOT_MS
        while b + SLOT_MS not in self.input_slots and b - slot < 12 * SLOT_MS:
            b += SLOT_MS
        run = (b - a) // SLOT_MS + 1
        # Six whole slots without input is 90 s, so the silence between the
        # inputs on either side is under 120 s however it is measured.
        tl.check(run <= 6, f"the no-input run around slot {slot} is {run} slots; no case lets one reach 120 s")
        return "bridged"

    def _slot_focus(self, slot):
        held = {}
        for i, (t, cls, project, _) in enumerate(self.focus):
            nxt = self.focus[i + 1][0] if i + 1 < len(self.focus) else slot + SLOT_MS
            a, b = max(t, slot), min(nxt, slot + SLOT_MS)
            if a < b:
                w = held.setdefault((cls, project), [0, a])
                w[0] += b - a
        tl.check(sum(w[0] for w in held.values()) == SLOT_MS, f"os.focus covers slot {slot}")
        return min(held, key=lambda k: (-held[k][0], held[k][1]))

    def focus_at(self, t):
        tl.check(all(f[0] != t for f in self.focus), f"no os.focus in the millisecond of an act at {t}")
        recs = [f for f in self.focus if f[0] < t]
        tl.check(recs, f"an os.focus is in force at {t}")
        return recs[-1]

    def last_input_at(self, t):
        got = [last for s, last in self.samples if s == t]
        tl.check(got, f"a sample at {t}, as at every hook (DESIGN 3.3)")
        return got[-1]

    def has_input(self, slot):
        return slot in self.input_slots

    # Anchors and turns.

    def _origin(self, e):
        d, s = e["data"], self.sessions[e["session_key"]]
        tl.check(d["origin_declared"] == "none" and not d["matches_pending_cron"], "no prompt declares an origin")
        if s["entrypoint"] in ("sdk", "exec") or s["parent_class"] in ("script_runtime", "scheduler", "agent",
                                                                        "agent_app"):
            return "headless", False
        if not s["has_tty"] and s["entrypoint"] not in ("ide", "desktop"):
            return "headless", False
        t = e["t"]
        lead = THINK_LEAD_S * SECOND + typing_ms(d["typed_len_est"])
        typed_in = t - self.last_input_at(t) <= KEYS_PRE_S * SECOND
        rec = self.focus_at(t)
        unknown = rec[1] == "unknown"
        hosts = rec[1] in ("terminal", "agent_app") and e["session_key"] in rec[3]
        first = (t - lead) - (t - lead) % SLOT_MS
        slots = sum(1 for sl in range(first, t, SLOT_MS) if self.has_input(sl))
        plausible = d["typed_len_est"] <= TYPING_MAX_CPS * 15 * slots
        if typed_in and (hosts or unknown):
            human = "human_typed" if plausible and d["pasted_chars"] == 0 else "human_pasted"
            return human, unknown and not hosts
        return "no_input", False

    def _anchors(self, events):
        self.anchors, self.origin = [], {}
        for e in events:
            k, d, t = e["kind"], e["data"], e["t"]
            if k == "prompt.submit":
                o, unknown_only = self._origin(e)
                self.origin[(e["session_key"], d["turn_key"])] = o
                if o not in HUMAN:
                    continue
                tl.check(not d["low_effort"] and not d["answers_question"] and d["typed_len_est"] >= 20,
                         "every human prompt here is substantive")
                typing = typing_ms(d["typed_len_est"])
                self.anchors.append(Anchor(e, "substantive_prompt", "P", lead_ms=THINK_LEAD_S * SECOND + typing,
                                           typing_ms=typing, unknown_only=unknown_only, origin=o,
                                           prompt_class="substantive"))
            elif k == "editor.activity":
                rec = self.focus_at(t)
                editor = rec[1] == "editor" and rec[2] == e["project_key"]
                unknown = rec[1] == "unknown"
                if not self.has_input(t - t % SLOT_MS) or not (editor or unknown):
                    continue
                if d["act"] in ("edit", "save"):
                    tl.check(d["edit_source"] in ("human", "tab", "unknown"), "editor edits here are the person's")
                    kind, reason = "typed_edit", "E"
                else:
                    kind, reason = "editor_navigation", "NAV"
                self.anchors.append(Anchor(e, kind, reason, lead_ms=EDIT_LEAD_S * SECOND,
                                           unknown_only=unknown and not editor))
            elif k == "agent.permission":
                wait = d["answered_at"] - d["requested_at"]
                ans = d["answered_at"]
                if d["decided_by"] == "user" and wait >= PA_MIN_LATENCY_MS and ans - ans % SLOT_MS in self.present:
                    tl.check(ans == t, "a permission record is dated at its answer")
                    self.anchors.append(Anchor(e, "permission_answer", "PA", lead_ms=min(wait, PA_MAX_S * SECOND),
                                               trail_ms=PA_TRAIL_S * SECOND))
            elif k == "git.change":
                if d["op"] == "commit" and d["actor"] == "human" and self.has_input(t - t % SLOT_MS):
                    self.anchors.append(Anchor(e, "human_commit", "G", lead_ms=COMMIT_LEAD_S * SECOND))
        for a in self.anchors:
            tl.check(a.lead_ms % SECOND == 0 and a.t % SECOND == 0, "anchors and leads on whole seconds")

    def _turns(self, events):
        self.turns = []
        open_ = {}
        for e in events:
            k, d = e["kind"], e["data"]
            if k == "prompt.submit":
                s = e["session_key"]
                tl.check(s not in open_, f"session {s}: a prompt while its turn runs")
                open_[s] = (d["turn_key"], e["t"], e["project_key"])
            elif k == "agent.stop":
                s = e["session_key"]
                tl.check(s in open_ and open_[s][0] == d["turn_key"] and not d["stop_hook_active"],
                         f"session {s}: the stop at {e['t']} ends its turn")
                key, t0, p = open_.pop(s)
                self.turns.append(Turn(s, p, key, t0, e["t"], self.origin[(s, key)]))
        for s, (key, t0, p) in open_.items():
            self.turns.append(Turn(s, p, key, t0, float("inf"), self.origin[(s, key)]))

    # Credit.

    def _score(self, events):
        start, end = self.start, self.end
        changes = []
        for e in events:
            k, t = e["kind"], e["t"]
            if k == "prompt.submit" and self.origin[(e["session_key"], e["data"]["turn_key"])] in HUMAN:
                changes.append((t, 0, e["session_key"], None))
            elif k in ("agent.output", "agent.tool"):
                changes.append((t, 1, e["session_key"], fill_s(k, e["data"])))
        changes.sort(key=lambda c: (c[0], c[1]))
        tl.check(all(start <= c[0] < end for c in changes), "every fill and reset is inside the window")
        bucket = {s: [] for s in self.sessions}  # [fill time, seconds], oldest first
        # What BUCKET_MAX_S evicted, kept aside under the same resets (SM 2.4).
        self.evicted = {s: [] for s in self.sessions}
        self.credit, self.code, self.running, self.contested = {}, {}, set(), []
        self.peak = {s: Fraction(0) for s in self.sessions}
        i = 0
        for tau in range(start, end, SECOND):
            # Records in a second come before its credit.
            while i < len(changes) and changes[i][0] <= tau:
                t, what, s, x = changes[i]
                if what == 0:
                    bucket[s] = [[t, Fraction(R_TURN_BASE_S)]]
                    self.evicted[s] = []
                elif x > 0:
                    bucket[s].append([t, x])
                    over = sum(v for _, v in bucket[s]) - BUCKET_MAX_S
                    while over > 0:
                        take = min(over, bucket[s][0][1])
                        self.evicted[s].append([bucket[s][0][0], take])
                        bucket[s][0][1] -= take
                        over -= take
                        if bucket[s][0][1] == 0:
                            bucket[s].pop(0)
                self.peak[s] = max(self.peak[s], sum(v for _, v in bucket[s]))
                i += 1
            for s in bucket:
                tl.check(all(tau - t <= BUCKET_TTL_S * SECOND for t, _ in bucket[s] + self.evicted[s]),
                         "nothing expires")
            if any(t.running(tau) for t in self.turns):
                self.running.add(tau)
            self._second(tau, bucket)

    def _second(self, tau, bucket):
        slot = tau - tau % SLOT_MS
        cls, fp = self.slot_focus[slot]
        unknown = cls == "unknown"

        def on(p):
            return unknown or (cls in PROJECT_APPS and fp == p)

        windows, buckets = [], []
        for a in self.anchors:
            if (a.in_lead(tau) or a.in_trail(tau)) and on(a.project):
                windows.append(a)
        for s, entries in bucket.items():
            p = self.sessions[s]["project"]
            near = [a for a in self.anchors if a.project == p and abs(a.t - tau) <= READ_HORIZON_S * SECOND]
            left = sum(v for _, v in entries)
            if left == 0:
                # A second that would drain an empty bucket drains what the
                # cap evicted, as X.bucket_cap (SM 2.4). No case gets there.
                tl.check(not (near and on(p) and self.evicted[s]), f"no second at {tau} is X.bucket_cap")
                continue
            tl.check(left >= 1, f"session {s} never has less than a second left to read ({left} at {tau})")
            # The reading horizon's own end never decides a second here: where
            # a bucket with something in it could take the second, an anchor
            # exactly 600 s away is never the only one that qualifies, so it
            # is never the nearest either. Where an anchor window holds the
            # second, the window wins whatever the bucket's horizon (SM 3.3
            # rule 2).
            tl.check(windows or not near or any(abs(a.t - tau) < READ_HORIZON_S * SECOND for a in near),
                     f"no bucket qualifies at {tau} only through an anchor exactly 600 s away")
            if near and on(p):
                buckets.append((s, p, near))
        projects = {a.project for a in windows} | {p for _, p, _ in buckets}
        if windows:
            leads = [a for a in windows if a.in_lead(tau)]
            if leads:
                # SM 3.3 rule 3: the lead of the next anchor at or after the
                # second, then rule 6, the lower project_key.
                win = min(leads, key=lambda a: (a.t, a.project))
            else:
                tl.check(len(windows) == 1, f"one trail at most at {tau}")
                win = windows[0]
            self.credit[tau] = (win.project, win.reason, "C" if unknown else win.level,
                                [FOCUS_UNKNOWN] if unknown else win.labels)
            if len(projects) > 1:
                rule = "3" if len({a.project for a in windows}) > 1 else "2"
                self.contested.append((tau, self._kinds(windows, buckets), win.project, rule))
            return
        if buckets:
            by_project = {}
            for s, p, near in buckets:
                by_project.setdefault(p, []).append((s, near))
            for p, ss in by_project.items():
                tl.check(len(ss) == 1, f"one session a project wants {tau}, so SM 3.3 rule 4 never applies")

            def nearest(p, mid):
                near = by_project[p][0][1]
                d = min(abs(a.t - (tau + mid)) for a in near)
                first = min(a.t for a in near if abs(a.t - (tau + mid)) == d)
                return d, first, p

            win = min(by_project, key=lambda p: nearest(p, 0))
            tl.check(win == min(by_project, key=lambda p: nearest(p, 500)),
                     f"the second's start and its middle pick the same project at {tau}")
            s, near = by_project[win][0]
            d = min(abs(a.t - tau) for a in near)
            qual = [a for a in near if abs(a.t - tau) == d]
            tl.check(len({(a.level, tuple(a.labels)) for a in qual}) == 1, "the nearest anchor's level is one level")
            turn = [t for t in self.turns if t.session == s and t.running(tau)]
            reason = "SUP" if turn else "READ"
            self.credit[tau] = (win, reason, "C" if unknown else qual[0].level,
                                [FOCUS_UNKNOWN] if unknown else qual[0].labels)
            take = Fraction(1)
            while take > 0:
                got = min(take, bucket[s][0][1])
                bucket[s][0][1] -= got
                take -= got
                if bucket[s][0][1] == 0:
                    bucket[s].pop(0)
            if len(projects) > 1:
                self.contested.append((tau, self._kinds(windows, buckets), win, "2.5"))
            return
        self.code[tau] = self._uncredited(tau, cls, fp, unknown)

    @staticmethod
    def _kinds(windows, buckets):
        kinds = {}
        for a in windows:
            kinds.setdefault(a.project, set()).add(a.reason)
        for _, p, _ in buckets:
            kinds.setdefault(p, set()).add("bucket")
        return {p: sorted(k) for p, k in kinds.items()}

    def _uncredited(self, tau, cls, fp, unknown):
        """SM 5.2's order for a second nothing credits. Presence is yes or
        bridged in every case, so never X.absent."""
        if not unknown and cls not in PROJECT_APPS:
            return "X.other_focus"
        if not unknown and fp is None:
            return "X.ambiguous"
        turns = [t for t in self.turns if t.running(tau) and (unknown or t.project == fp)]
        if any(t.origin in HUMAN for t in turns):
            return "X.agent_only"
        if any(t.origin in AUTOMATED for t in turns):
            return "X.automated"
        if any(t.origin == "no_input" for t in turns):
            return "X.no_input"
        return "X.no_anchor"

    # Views.

    def bucket_minute(self, m):
        return any(t in self.credit or t in self.running for t in range(m, m + tl.MINUTE_MS, SECOND))


# The hand-written runs.

def expand(fx, runs, names):
    """Each second of the hand-written runs, as {second: (project, reason)}
    for credit and {second: (None, code)} for an uncredited second. A run is
    (from, to, what), local times, and what is "REASON" in a case with one
    project, "<project name> REASON" in one with several, or "X.<code>"."""
    by_name = {n: k for k, n in names.items()}
    out = {}
    for a, b, what in runs:
        ta, tb = fx.at(a), fx.at(b)
        tl.check(ta < tb and ta % SECOND == 0 and tb % SECOND == 0, f"run {a} to {b} on whole seconds")
        parts = what.split()
        if parts[0].startswith("X."):
            tl.check(parts[0] in tl.UNCREDITED and len(parts) == 1, f"run {a}: {what}")
            val = (None, parts[0])
        elif len(parts) == 1:
            tl.check(len(names) == 1, f"run {a}: {what} names no project in a case with several")
            val = (next(iter(names)), parts[0])
        else:
            val = (by_name[parts[0]], parts[1])
        if val[0] is not None:
            tl.check(val[1] in tl.CREDIT_REASONS, f"run {a}: {val[1]} is a credit reason")
        for tau in range(ta, tb, SECOND):
            tl.check(tau not in out, f"the runs overlap at {fx.hhmmss(tau)}")
            out[tau] = val
    return out


def spans_of(fx, secs):
    """Seconds, as Unix ms, grouped into runs of local [from, to)."""
    out = []
    for t in sorted(secs):
        if out and out[-1][1] == t:
            out[-1][1] = t + SECOND
        else:
            out.append([t, t + SECOND])
    return [(fx.hhmmss(a), fx.hhmmss(b)) for a, b in out]


class Case:
    """One case's oracle: the hand-written runs, the runs whose credit is
    level C (each carrying focus_unknown, the only reason a second here is
    C), and the turns, which give each minute's running seconds."""

    def __init__(self, fx, exp, *, runs, level, level_c, turns):
        self.fx, self.exp = fx, exp
        self.names = exp.projects
        self.secs = expand(fx, runs, self.names)
        self.level = level
        # The credited seconds inside the level-C runs.
        self.c = set()
        for a, b in level_c:
            self.c.update(t for t in range(fx.at(a), fx.at(b), SECOND)
                          if t in self.secs and self.secs[t][0] is not None)
        self.turns = turns  # (from, to) of each running turn, Unix ms
        self.start, self.end = exp.start, exp.end

    def running(self, tau):
        return any(a <= tau < b for a, b in self.turns)

    def bucket_minute(self, m):
        return any((t in self.secs and self.secs[t][0] is not None) or self.running(t)
                   for t in range(m, m + tl.MINUTE_MS, SECOND))

    def level_of(self, tau):
        return "C" if tau in self.c else self.level

    def check(self, rp):
        """Every second of the window against the replay: credit, level and
        labels, uncredited code, and which minutes have a bucket."""
        fx = self.fx
        for m in range(self.start, self.end, tl.MINUTE_MS):
            mine, theirs = self.bucket_minute(m), rp.bucket_minute(m)
            tl.check(mine == theirs, f"{fx.hhmm(m)}: the runs say bucket {mine}, the replay {theirs}")
            for tau in range(m, m + tl.MINUTE_MS, SECOND):
                tl.check(self.running(tau) == (tau in rp.running), f"{fx.hhmmss(tau)}: an agent runs?")
                if not mine:
                    tl.check(tau not in self.secs, f"{fx.hhmmss(tau)}: no run covers a minute without a bucket")
                    continue
                tl.check(tau in self.secs, f"{fx.hhmmss(tau)}: the runs cover every second of a bucket minute")
                p, what = self.secs[tau]
                if p is None:
                    tl.check(tau not in rp.credit and rp.code.get(tau) == what,
                             f"{fx.hhmmss(tau)}: the runs say {what}, the replay "
                             f"{rp.credit.get(tau) or rp.code.get(tau)}")
                    continue
                got = rp.credit.get(tau)
                lvl = self.level_of(tau)
                want = (p, what, lvl, [FOCUS_UNKNOWN] if lvl == "C" else [])
                tl.check(got == want, f"{fx.hhmmss(tau)}: the runs say {want}, the replay {got or rp.code.get(tau)}")

    # Output.

    def rows(self, notes):
        """Writes expected.json's rows: each bucket minute as its own row and
        each run of minutes without one as one row. notes maps a row's local
        label to (sm, note) or (sm, note, erratum), and note may be None."""
        fx, exp = self.fx, self.exp
        m = self.start
        while m < self.end:
            if self.bucket_minute(m):
                credit, unc, agent = {}, {}, 0
                for tau in range(m, m + tl.MINUTE_MS, SECOND):
                    p, what = self.secs[tau]
                    if p is None:
                        unc[what] = unc.get(what, 0) + 1
                    else:
                        credit.setdefault(p, {})
                        credit[p][what] = credit[p].get(what, 0) + 1
                    agent += self.running(tau)
                label = fx.hhmm(m)
                sm, note, erratum = (notes[label] + (None,))[:3]
                exp.row(label, credit=credit, uncredited=unc, agent=agent, sm=sm, note=note,
                        derived=["credited_s", "credit", "uncredited", "agent_running_s"], erratum=erratum)
                m += tl.MINUTE_MS
                continue
            n = m
            while n < self.end and not self.bucket_minute(n):
                n += tl.MINUTE_MS
            label = fx.hhmm(m) if n - m == tl.MINUTE_MS else f"{fx.hhmm(m)}-{fx.hhmm(n - tl.MINUTE_MS)}"
            sm, note, erratum = (notes[label] + (None,))[:3]
            exp.row(fx.hhmm(m), fx.hhmm(n), bucket=False, sm=sm, note=note, erratum=erratum)
            m = n
        unused = set(notes) - {r["local"] for r in exp.rows}
        tl.check(not unused, f"notes for rows that don't exist: {sorted(unused)}")

    def slots(self, rp):
        """slots.json's entries: every slot of every bucket minute, with its
        focus as the replay resolves it from the os.focus records, its
        credit by project and reason, its seconds by level, and ev and lbl as
        a minute bucket's slot gives them (SM 5.1): its weakest second's
        level, and focus_unknown when some of its seconds are level C because
        the window in front was unknown."""
        fx = self.fx
        out = []
        for m in range(self.start, self.end, tl.MINUTE_MS):
            if not self.bucket_minute(m):
                continue
            for sl in range(m, m + tl.MINUTE_MS, SLOT_MS):
                credit, levels = {}, {}
                for tau in range(sl, sl + SLOT_MS, SECOND):
                    p, what = self.secs[tau]
                    if p is None:
                        continue
                    credit.setdefault(p, {})
                    credit[p][what] = credit[p].get(what, 0) + 1
                    lvl = self.level_of(tau)
                    levels[lvl] = levels.get(lvl, 0) + 1
                credit = {p: tl.ordered(c, tl.CREDIT_REASONS) for p, c in sorted(credit.items())}
                cr = sum(levels.values())
                entry = {"start": fx.utc(sl), "local": fx.hhmmss(sl), "focus": rp.slot_focus[sl][0],
                         "credited_s": cr, "credit": credit,
                         "levels": {k: levels[k] for k in ("A", "B", "C") if k in levels}}
                if cr:
                    entry["ev"] = max(levels)
                    entry["lbl"] = [FOCUS_UNKNOWN] if "C" in levels else []
                else:
                    entry["lbl"] = []
                out.append(entry)
        return out

    def level_totals(self):
        lv = {"A": 0, "B": 0, "C": 0}
        for tau, (p, _) in self.secs.items():
            if p is not None:
                lv[self.level_of(tau)] += 1
        return {"level_a_s": lv["A"], "level_b_s": lv["B"], "level_c_s": lv["C"], "focus_unknown_s": lv["C"]}


def anchors_doc(fx, rp, want):
    """anchors.json's anchors, from the hand-written list want: (local time,
    kind, lead_s, trail_s, level, labels, sm), in time order. Each must be
    an anchor the replay found from the events, with the same lead, trail,
    level and labels, and the replay must find no other."""
    by_t = {(a.t, a.kind): a for a in rp.anchors}
    tl.check(len(by_t) == len(rp.anchors), "no two anchors of one kind share a millisecond")
    out, seen = [], set()
    for local, kind, lead_s, trail_s, level, labels, sm in want:
        t = fx.at(local)
        a = by_t.get((t, kind))
        tl.check(a is not None, f"the replay finds the {kind} at {local}")
        seen.add((t, kind))
        tl.check((a.lead_ms, a.trail_ms, a.level, a.labels) == (lead_s * SECOND, trail_s * SECOND, level, labels),
                 f"the {kind} at {local}: the replay has lead {a.lead_ms}, trail {a.trail_ms}, {a.level} {a.labels}")
        doc = tl.anchor(fx, a.event, kind=kind, origin=a.origin, prompt_class=a.prompt_class, lead_s=lead_s,
                        trail_s=trail_s, lead_from="params", level=level, sm=sm)
        doc["labels"] = labels
        out.append(doc)
    tl.check(seen == set(by_t), f"the replay finds no other anchor: {sorted(set(by_t) - seen)}")
    tl.check([a["t"] for a in out] == sorted(a["t"] for a in out), "anchors in time order")
    return out


def turns_doc(fx, rp, want):
    """anchors.json's turns, from the hand-written list want: (session,
    turn_key, submit, stop, origin, prompt_class, sm), local times. Each must
    be a turn the replay found, with the origin it gives."""
    by_key = {(t.session, t.turn_key): t for t in rp.turns}
    out = []
    for s, key, submit, stop, origin, cls, sm in want:
        t = by_key.pop((s, key), None)
        tl.check(t is not None and t.start == fx.at(submit) and t.stop == fx.at(stop),
                 f"the replay has the turn at {submit}")
        tl.check(t.origin == origin, f"the turn at {submit}: the replay gives origin {t.origin}, not {origin}")
        out.append(tl.turn(fx, t.start, t.stop, session=s, turn_key=key, origin=origin, prompt_class=cls, sm=sm))
    tl.check(not by_key, "the replay finds no other turn")
    return out


# The README's tables, which the Go test in internal/timelines reads too.

MINUTE_HEADER = "| Minute | Credited | Reasons | Arithmetic |"
TOTAL_HEADER = "| Total | Seconds | Arithmetic |"
NUM = r"\d{1,3}(?:,\d{3})+|\d+"
CODE_ITEM = re.compile(r"(X\.[a-z_]+) ((?:" + NUM + r")(?: ?[+×] ?(?:" + NUM + r"))*)(?: ?= ?(" + NUM + r"))?")
BUCKET_MINUTES = re.compile(r"(\d+) bucket minutes")
NO_BUCKET = re.compile(r"\bno bucket\b", re.IGNORECASE)


def tables(path, header):
    rows = []
    with open(path, encoding="utf-8") as f:
        inside = False
        for line in f:
            line = line.rstrip("\n")
            if line.startswith(header):
                inside = True
                continue
            if not line.startswith("|"):
                inside = False
                continue
            if not inside:
                continue
            cells = [c.strip().replace("`", "") for c in line.strip().strip("|").split("|")]
            if set(cells[0]) <= set("-: "):
                continue
            rows.append(cells)
    return rows


def arithmetic(expr):
    """Whole numbers with thousands commas, joined by +, −, × and
    parentheses."""
    s = expr.replace("×", "*").replace("−", "-").replace(",", "")
    tl.check(re.fullmatch(r"[0-9+\-* ()]+", s) is not None, f"arithmetic {expr!r} uses only + − × and parentheses")
    toks = re.findall(r"\d+|[+\-*()]", s)
    pos = [0]

    def peek():
        return toks[pos[0]] if pos[0] < len(toks) else None

    def take():
        pos[0] += 1
        return toks[pos[0] - 1]

    def factor():
        t = take()
        if t == "(":
            v = total()
            tl.check(take() == ")", f"arithmetic {expr!r}: a closing parenthesis")
            return v
        tl.check(t is not None and t.isdigit(), f"arithmetic {expr!r}: a number, not {t!r}")
        return int(t)

    def term():
        v = factor()
        while peek() == "*":
            take()
            v *= factor()
        return v

    def total():
        v = term()
        while peek() in ("+", "-"):
            v = v + term() if take() == "+" else v - term()
        return v

    v = total()
    tl.check(pos[0] == len(toks), f"arithmetic {expr!r}: nothing after the expression")
    return v


def codes_in(cell):
    out = {}
    for code, expr, stated in CODE_ITEM.findall(cell):
        n = arithmetic(expr)
        if stated:
            tl.check(int(stated.replace(",", "")) == n, f"{code} {expr} is {n}, not {stated}")
        out[code] = out.get(code, 0) + n
    return out


def check_readme(path, doc):
    """The README's minute tables work out every row of expected.json:
    each table row credits what the row with its Minute does, by project
    and reason; names every uncredited code of a bucket row with its
    seconds, and no code a row doesn't have; and says what its bucket is.
    Every total's arithmetic gives its seconds, which are expected.json's."""
    names = doc["projects"]
    by_name = {n: k for k, n in names.items()}
    by_local = {r["local"]: r for r in doc["rows"]}
    rows = tables(path, MINUTE_HEADER)
    tl.check(rows, f"{path} has a minute table")
    seen = set()
    for cells in rows:
        tl.check(len(cells) == 4, f"{path}: minute row {cells} has 4 cells")
        minute, credited, reasons, arith = cells
        tl.check(minute in by_local, f"{path}: minute {minute} is a row of expected.json")
        seen.add(minute)
        r = by_local[minute]
        tl.check(float(credited.replace(",", "")) == r.get("credited_s", 0),
                 f"{path}: {minute} credits {credited}, expected.json {r.get('credited_s', 0)}")
        got = {}
        if reasons != "none":
            for item in reasons.split(","):
                parts = item.split()
                if len(parts) == 2:
                    tl.check(len(names) == 1, f"{path}: {minute}: {item} names no project")
                    p = next(iter(names))
                else:
                    tl.check(len(parts) == 3 and parts[0] in by_name, f"{path}: {minute}: {item}")
                    p = by_name[parts[0]]
                got.setdefault(p, {})
                got[p][parts[-2]] = got[p].get(parts[-2], 0) + int(parts[-1])
        want = r.get("credit", {}) if r["bucket"] else {}
        tl.check(got == {p: c for p, c in want.items() if c}, f"{path}: {minute}: {got} against {want}")
        codes = codes_in(arith)
        tl.check(codes == ((r["uncredited"] or {}) if r["bucket"] else {}),
                 f"{path}: {minute} gives the codes {codes}, expected.json {r.get('uncredited')}")
        if r["bucket"]:
            tl.check(r["minutes"] > 1 or not NO_BUCKET.search(arith), f"{path}: {minute} has a bucket")
        else:
            tl.check(NO_BUCKET.search(arith), f"{path}: {minute} says it has no bucket")
        for n in BUCKET_MINUTES.findall(arith):
            tl.check(int(n) == r.get("bucket_minutes", 0), f"{path}: {minute}: {n} bucket minutes")
    tl.check(seen == set(by_local), f"{path}: the minute tables work out every row: {sorted(set(by_local) - seen)}")
    tot = doc["totals"]
    rows = tables(path, TOTAL_HEADER)
    tl.check(any(c[0] == "credited_s" for c in rows), f"{path} works out credited_s")
    for cells in rows:
        tl.check(len(cells) == 3, f"{path}: total row {cells} has 3 cells")
        key, seconds, expr = cells
        n = int(seconds.replace(",", ""))
        tl.check(arithmetic(expr) == n, f"{path}: {key}: {expr} is {arithmetic(expr)}, not {n}")
        if key in tot and not isinstance(tot[key], dict):
            want = tot[key]
        elif key in tot["credited_by_reason"]:
            want = tot["credited_by_reason"][key]
        elif key in tot["uncredited"]:
            want = tot["uncredited"][key]
        elif key in by_name:
            want = tot["credited_by_project"][by_name[key]]
        else:
            raise SystemExit(f"check failed: {path}: total {key} isn't in expected.json")
        tl.check(want == n, f"{path}: {key} is {n} in the README, {want} in expected.json")


def write(fx, out, expected, anchors, prompts, slots):
    fx.write(out, expected, anchors, prompts)
    with open(os.path.join(out, "slots.json"), "w", encoding="utf-8", newline="\n") as f:
        f.write(tl.pretty({"fixture": fx.name, "slots": slots}) + "\n")
