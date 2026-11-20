# SPDX-License-Identifier: AGPL-3.0-or-later
"""Shared code for the side-door cases (WP-FX4). README.md in this directory
is the format; ../timeline.py builds the events and the FX1 files.

A case's gen.py imports this module, which puts ../ on sys.path so that
timeline.py imports as tl. Standard library only."""

import os
import sys
from fractions import Fraction

sys.dont_write_bytecode = True
SIDEDOOR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(SIDEDOOR))

import timeline as tl  # noqa: E402

# The SM 8 and DESIGN 6.3 values the cases use. WP-PA1's pkg/params will hold
# the table; until it merges, the cases carry what they need, as
# timeline.py's bucket check does.
THINK_LEAD_S = 120
COMPOSE_CPS = Fraction(3, 2)
COMPOSE_MAX_S = 300
EDIT_LEAD_S = 120
R_TURN_BASE_S = 20
READ_WPM = 180
DIFF_LPM = 8
BUCKET_MAX_S = 600
READ_HORIZON_S = 600
KEYS_PRE_S = 10
TRACK_INPUT_PRE_S = 30
TRACK_MIN_GAP_S = 20
TRACK_LOG_MATCH_S = 10
TRACK_DEDUPE_S = 30
GAP_CAP_MS = 120_000

# DESIGN 6.2 D19: only these vias count toward logged_s and ai_logged_s.
# bridge_unverified, editor_unverified, track and plugin never do.
LOGGED_VIAS = ("hook", "log", "otel", "editor", "bridge")
# SM 5.4's agent events: prompt submits, tool calls (agent edits are tool
# calls) and stops. FX1's 6.6 replays the same three kinds.
AGENT_KINDS = ("prompt.submit", "agent.tool", "agent.stop")
# SM 5.3: logged_s adds every editor heartbeat.
HEARTBEAT_KINDS = ("editor.activity",)

SECOND = 1000


def typing_s(chars):
    """SM 2.3's typing part: min(COMPOSE_MAX_S, typed_len_est / COMPOSE_CPS)."""
    return min(Fraction(COMPOSE_MAX_S), Fraction(chars) / COMPOSE_CPS)


def output_fill(words=0, code_lines=0, diff_lines=0):
    """SM 2.4's fill for output, without the base: seconds as a Fraction."""
    return Fraction(60 * words, READ_WPM) + Fraction(60 * (code_lines + diff_lines), DIFF_LPM)


def ms(seconds):
    """A Fraction of seconds as tl.Seconds, rounded to the millisecond."""
    return tl.Seconds(round(Fraction(seconds) * 1000))


def keys_in_slot(fx, slot_start):
    return any(k and slot_start <= t < slot_start + tl.SLOT_MS for t, k in fx.inputs)


def input_in_every_slot(fx, start, end):
    """Every slot from start to end has input, so every one is present (SM
    2.1). The cases keep presence and focus out of the question."""
    t = start
    while t < end:
        if not any(t <= u < t + tl.SLOT_MS for u, _ in fx.inputs):
            return False
        t += tl.SLOT_MS
    return True


def samples_show_input_before(fx, t, window_ms):
    """Some presence sample reports a last input in [t - window, t). Plugin
    and bridge records arrive over HTTP, which takes no sample (DESIGN 3.3
    samples at hook, focus and power events), so the regular samples have to
    show the input SM 2.2 rule 6 asks for."""
    for e in fx.events:
        if e["kind"] != "os.presence":
            continue
        last = e["t"] - e["data"]["idle_any_s"].ms
        if t - window_ms <= last < t:
            return True
    return False


class Sweep:
    """SM 2.7 for the constructs these cases use, one second at a time, to
    check the rows each gen.py writes by hand. It isn't a scorer. It knows
    anchors with a think part and a typing part, one reading bucket per
    session with resets and capped fills, and turns' running spans, and it
    takes presence and focus on the case's one project as given for every
    second from start to end, which each gen.py checks separately."""

    def __init__(self, start, end):
        tl.check(start % SECOND == 0 and end % SECOND == 0, "sweep on whole seconds")
        self.start, self.end = start, end
        self.anchors = []
        self.bucket_ops = []
        self.runs = []

    def anchor(self, t, *, lead_s, typing_part_s, reason, level, typing_ok=None):
        """An anchor at t whose lead is lead_s long and ends with a typing
        part of typing_part_s. typing_ok(slot_start) says whether a second
        of the typing part in that slot is realized; None means always."""
        lead = round(Fraction(lead_s) * 1000)
        typing = round(Fraction(typing_part_s) * 1000)
        self.anchors.append({"t": t, "think": (t - lead, t - typing), "typing": (t - typing, t),
                             "ok": typing_ok, "reason": reason, "level": level})

    def reset(self, t, base_s):
        self.bucket_ops.append((t, 0, "reset", Fraction(base_s)))

    def fill(self, t, seconds):
        self.bucket_ops.append((t, 1, "fill", Fraction(seconds)))

    def run(self, a, b):
        self.runs.append((a, b))

    def running(self, t):
        return any(a <= t < b for a, b in self.runs)

    def candidate(self, t):
        best = None
        for a in self.anchors:
            in_think = a["think"][0] <= t < a["think"][1]
            in_typing = a["typing"][0] <= t < a["typing"][1]
            if in_typing and a["ok"] is not None:
                in_typing = a["ok"](t - t % tl.SLOT_MS)
            if not (in_think or in_typing):
                continue
            # SM 3.3 rule 3: the lead of the next anchor at or after the
            # second wins. These cases have leads only.
            if a["t"] >= t and (best is None or a["t"] < best["t"]):
                best = a
        return best

    def seconds(self):
        """Yields (t, reason, level) for every second; level is None for an
        uncredited second."""
        entries = []
        ops = sorted(self.bucket_ops)
        i = 0
        for t in range(self.start, self.end, SECOND):
            while i < len(ops) and ops[i][0] <= t:
                _, _, op, s = ops[i]
                if op == "reset":
                    entries = [s] if s else []
                elif s:
                    entries.append(s)
                    over = sum(entries) - BUCKET_MAX_S
                    while over > 0:
                        take = min(over, entries[0])
                        entries[0] -= take
                        over -= take
                        if entries[0] == 0:
                            entries.pop(0)
                i += 1
            a = self.candidate(t)
            if a:
                yield t, a["reason"], a["level"]
                continue
            near = [x for x in self.anchors if abs(x["t"] - t) <= READ_HORIZON_S * SECOND]
            if sum(entries) > 0 and near:
                need = Fraction(1)
                while need and entries:
                    take = min(need, entries[0])
                    entries[0] -= take
                    need -= take
                    if entries[0] == 0:
                        entries.pop(0)
                tl.check(need == 0, "the sweep drains whole seconds")
                level = min(near, key=lambda x: abs(x["t"] - t))["level"]
                yield t, "SUP" if self.running(t) else "READ", level
                continue
            yield t, "X.agent_only" if self.running(t) else "X.no_anchor", None

    def minutes(self):
        """Per minute: credit by reason, uncredited by code, agent seconds,
        and credit by level."""
        out = {}
        for t, why, level in self.seconds():
            m = t - t % tl.MINUTE_MS
            row = out.setdefault(m, {"credit": {}, "uncredited": {}, "agent": 0, "levels": {}})
            if level:
                row["credit"][why] = row["credit"].get(why, 0) + 1
                row["levels"][level] = row["levels"].get(level, 0) + 1
            else:
                row["uncredited"][why] = row["uncredited"].get(why, 0) + 1
            if self.running(t):
                row["agent"] += 1
        return out


def check_rows(exp, sweep, project):
    """The hand-written rows agree with the sweep, second for second."""
    got = sweep.minutes()
    for r in exp.rows:
        start = r["_end"] - r["minutes"] * tl.MINUTE_MS
        for m in range(start, r["_end"], tl.MINUTE_MS):
            g = got.get(m, {"credit": {}, "uncredited": {}, "agent": 0})
            has_bucket = bool(g["credit"]) or g["agent"] > 0
            if not r["bucket"]:
                tl.check(not has_bucket, f"{r['local']}: the sweep credits {g['credit']} or runs an agent")
                continue
            tl.check(r["minutes"] == 1, f"{r['local']}: bucket rows here are single minutes")
            want = r["credit"].get(project, {})
            tl.check(g["credit"] == want, f"{r['local']}: credit {want}, the sweep gives {g['credit']}")
            if r["uncredited"] is not None:
                tl.check(g["uncredited"] == r["uncredited"],
                         f"{r['local']}: uncredited {r['uncredited']}, the sweep gives {g['uncredited']}")
            tl.check(g["agent"] == r["agent_running_s"],
                     f"{r['local']}: agent_running_s {r['agent_running_s']}, the sweep gives {g['agent']}")


def levels(sweep):
    """Credited seconds by level over the sweep."""
    out = {"A": 0, "B": 0, "C": 0}
    for _, _, level in sweep.seconds():
        if level:
            out[level] += 1
    return out


def logged(fx, start, end):
    """Each record's contribution to logged_s and ai_logged_s, in ms, by
    event_id: SM 5.3 and 5.4 with D19's via filter. Records go in arrival
    order, and each counted record after the first adds min(gap to the
    record before it, 120 s), which is how Hackatime sums (HC 1.4)."""
    inside = [e for e in fx.events if start <= e["t"] < end]

    def series(kinds):
        out, prev = {}, None
        for e in inside:
            if e["kind"] in kinds and e["via"] in LOGGED_VIAS:
                out[e["event_id"]] = 0 if prev is None else min(e["t"] - prev, GAP_CAP_MS)
                prev = e["t"]
        return out

    return series(AGENT_KINDS + HEARTBEAT_KINDS), series(AGENT_KINDS)


class SideDoor:
    """sidedoor.json: every record of the window but the OS ones, with what
    it opens, flags and adds to logged_s and ai_logged_s, and every turn with
    its reading fill."""

    def __init__(self, fx, exp, *, case, wp, rules):
        self.fx = fx
        self.exp = exp
        self.case = case
        self.wp = wp
        self.rules = rules
        self.records = {}
        self.turns = []
        self.log, self.ai_log = logged(fx, exp.start, exp.end)

    def record(self, e, *, why, anchor=False, level=None, labels=None, track=None, flags=None):
        tl.check(e["event_id"] not in self.records, f"one entry per record: {e['kind']} at {self.fx.hhmmss(e['t'])}")
        d = {"event_id": e["event_id"], "t": e["t"], "local": self.fx.hhmmss(e["t"]), "kind": e["kind"],
             "via": e["via"], "anchor": anchor}
        if anchor:
            d["level"] = level
            d["labels"] = labels or []
        if track:
            d["track"] = track
        d["flags"] = flags or []
        d["logged_s"] = tl.Seconds(self.log.get(e["event_id"], 0))
        d["ai_logged_s"] = tl.Seconds(self.ai_log.get(e["event_id"], 0))
        d["why"] = why
        self.records[e["event_id"]] = d

    def turn(self, opened_by, *, turn_key, backed, output_fill_s, fill_s, why):
        e = opened_by
        self.turns.append({"opened_by": e["event_id"], "turn_key": turn_key, "session_key": e["session_key"],
                           "t": e["t"], "local": self.fx.hhmmss(e["t"]), "via": e["via"], "backed": backed,
                           "output_fill_s": ms(output_fill_s), "fill_s": ms(fill_s), "why": why})

    def doc(self, *, errata, notes):
        fx = self.fx
        inside = [e for e in fx.events if self.exp.start <= e["t"] < self.exp.end and not e["kind"].startswith("os.")]
        tl.check([e["event_id"] for e in inside] == list(self.records),
                 "sidedoor.json lists every record of the window but the OS ones, in order")
        return {
            "fixture": fx.name,
            "case": self.case,
            "title": fx.title,
            "wp": self.wp,
            "rules": self.rules,
            "records": list(self.records.values()),
            "turns": self.turns,
            "errata": errata,
            "notes": notes,
        }

    def totals(self):
        """logged_s and ai_logged_s in ms."""
        return sum(self.log.values()), sum(self.ai_log.values())


def check_inputs_off_boundaries(fx):
    """FX1's convention (../README.md, Presence): no input falls on a slot
    boundary, so the sample at a slot's end says whether the slot had input.
    A slot starts on :00, :15, :30 or :45 (SM 1.3), so a press at 14:06:45.000
    is in the slot that starts there. The sample at 14:06:45 would report it
    as the last press, and a scorer that rebuilds slots from the samples
    would see no key in 14:06:30 to 14:06:45 even if there was one. The
    sweep reads fx.inputs, not the samples, so it can't catch this itself.
    write() runs this check for every case, inputs before the fixture
    included."""
    times = [t for t, _ in fx.inputs] + [t for t in (fx.last_input, fx.last_key) if t is not None]
    for t in times:
        tl.check(t % tl.SLOT_MS != 0, f"{fx.name}: input at {fx.hhmmss(t)} falls on a slot boundary")


def write(fx, out, expected, anchors, prompts, sidedoor):
    check_inputs_off_boundaries(fx)
    fx.write(out, expected, anchors, prompts)
    with open(os.path.join(out, "sidedoor.json"), "w", encoding="utf-8", newline="\n") as f:
        f.write(tl.pretty(sidedoor) + "\n")
