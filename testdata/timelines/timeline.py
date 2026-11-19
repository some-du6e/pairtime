# SPDX-License-Identifier: AGPL-3.0-or-later
"""Builds pairtime timeline fixtures. testdata/timelines/README.md is the format.

A fixture's gen.py imports this module, describes its timeline in local
time and calls Fixture.write. The output is deterministic: event ids come
from a hash of the fixture name and seq, never from a random source, so
running gen.py again reproduces the checked-in files byte for byte.
internal/timelines tests that.

Run a generator with the interpreter in isolated mode:

    python3 -I testdata/timelines/6.2/gen.py [--out DIR]

Standard library only.
"""

import hashlib
import json
import os
import sys
from datetime import datetime, timedelta, timezone

SLOT_MS = 15_000
MINUTE_MS = 60_000

# Uncredited reason codes in SM 5.2 order, and credit reasons in SM 5.2
# order. Rows list their reasons in these orders.
CREDIT_REASONS = ["P", "ANS", "GO", "Q", "PA", "INT", "E", "NAV", "CMD", "TEST", "G", "SUP", "READ", "LOOKUP"]
UNCREDITED = [
    "X.absent", "X.other_focus", "X.ambiguous", "X.no_anchor", "X.agent_only", "X.automated",
    "X.no_input", "X.reroll", "X.rewrite_loop", "X.same_error", "X.autopilot", "X.noprog",
    "X.lookup_cap", "X.bucket_cap", "X.day_cap",
]


def seconds_text(ms):
    """Writes ms as decimal seconds the way Go's encoding/json writes the
    float64: no trailing zeros, no exponent."""
    sign = "-" if ms < 0 else ""
    ms = abs(ms)
    whole, frac = divmod(ms, 1000)
    if frac == 0:
        return f"{sign}{whole}"
    return f"{sign}{whole}.{frac:03d}".rstrip("0")


class Seconds:
    """A float64 field held as integer milliseconds, so it encodes exactly."""

    def __init__(self, ms):
        self.ms = ms


def encode(v):
    """Compact JSON as Go's json.Marshal writes it for these fixtures."""
    if v is None:
        return "null"
    if v is True:
        return "true"
    if v is False:
        return "false"
    if isinstance(v, Seconds):
        return seconds_text(v.ms)
    if isinstance(v, int):
        return str(v)
    if isinstance(v, float):
        # Only the hand-read files hold floats, such as an approximate
        # ratio. Event fields use Seconds, which encodes exactly.
        return str(int(v)) if v.is_integer() else repr(v)
    if isinstance(v, str):
        return json.dumps(v, ensure_ascii=False)
    if isinstance(v, list):
        return "[" + ",".join(encode(x) for x in v) + "]"
    if isinstance(v, dict):
        return "{" + ",".join(json.dumps(k) + ":" + encode(x) for k, x in v.items()) + "}"
    raise TypeError(f"can't encode {type(v)}")


def pretty(v, indent=0):
    """Indented JSON for the hand-read files: two spaces, keys in order,
    short lists and objects of scalars on one line."""
    pad = "  " * indent
    inner = "  " * (indent + 1)
    if isinstance(v, dict):
        if not v:
            return "{}"
        if all(not isinstance(x, (dict, list)) for x in v.values()) and len(encode(v)) <= 100:
            return "{" + ", ".join(json.dumps(k) + ": " + encode(x) for k, x in v.items()) + "}"
        items = [inner + json.dumps(k) + ": " + pretty(x, indent + 1) for k, x in v.items()]
        return "{\n" + ",\n".join(items) + "\n" + pad + "}"
    if isinstance(v, list):
        if not v:
            return "[]"
        if all(not isinstance(x, (dict, list)) for x in v) and len(encode(v)) <= 100:
            return "[" + ", ".join(encode(x) for x in v) + "]"
        items = [inner + pretty(x, indent + 1) for x in v]
        return "[\n" + ",\n".join(items) + "\n" + pad + "]"
    return encode(v)


def uuid7(t_ms, seed):
    """A UUIDv7 (RFC 9562) whose timestamp is t_ms and whose other bits
    come from SHA-256 of seed."""
    h = hashlib.sha256(seed.encode()).digest()
    b = bytearray(16)
    for i in range(6):
        b[i] = (t_ms >> (40 - 8 * i)) & 0xFF
    b[6:16] = h[:10]
    b[6] = (b[6] & 0x0F) | 0x70
    b[8] = (b[8] & 0x3F) | 0x80
    x = b.hex()
    return f"{x[0:8]}-{x[8:12]}-{x[12:16]}-{x[16:20]}-{x[20:32]}"


def key(prefix, name):
    """An opaque key: prefix, then 12 hex digits of SHA-256 of name. A real
    client keys these per device (AGENTS.md); in a fixture they only have
    to be stable and distinct."""
    return prefix + "_" + hashlib.sha256(name.encode()).hexdigest()[:12]


def git_sha(name):
    return hashlib.sha1(name.encode()).hexdigest()


class Fixture:
    """One device's records for one SM worked example."""

    def __init__(self, name, *, title, date, timezone_name, utc_offset_min, os_name, keys,
                 device, boot_id, boot, seq0, last_input, last_key=None):
        self.name = name
        self.title = title
        self.date = date
        self.timezone_name = timezone_name
        self.utc_offset_min = utc_offset_min
        self.os_name = os_name
        self.keys = keys
        self.device = device
        self.boot_id = boot_id
        self.seq0 = seq0
        self._base = datetime.fromisoformat(date).replace(tzinfo=timezone(timedelta(minutes=utc_offset_min)))
        self.boot = self.at(boot)
        self.raw = []
        self.inputs = []
        self.locks = []
        self.samples = set()
        self.anchors = []
        self.turns = []
        self.prompts = {}
        self.last_input = self.at(last_input)
        self.last_key = self.at(last_key) if last_key else None

    # Time.

    def at(self, s):
        """Unix ms of a local time "HH:MM:SS[.fff]" on the fixture's date,
        "-1 HH:MM..." on the day before or "+1 HH:MM..." on the day after."""
        day = 0
        if s[0] in "+-":
            d, s = s.split(" ", 1)
            day = int(d)
        hms, _, frac = s.partition(".")
        parts = [int(x) for x in hms.split(":")]
        h, m, sec = (parts + [0])[:3]
        ms = int((frac + "000")[:3]) if frac else 0
        dt = self._base + timedelta(days=day, hours=h, minutes=m, seconds=sec, milliseconds=ms)
        return int(dt.timestamp() * 1000)

    def local(self, t):
        dt = datetime.fromtimestamp(t / 1000, tz=timezone(timedelta(minutes=self.utc_offset_min)))
        return dt

    def hhmm(self, t):
        return self.local(t).strftime("%H:%M")

    def hhmmss(self, t):
        dt = self.local(t)
        s = dt.strftime("%H:%M:%S")
        if t % 1000:
            s += f".{t % 1000:03d}".rstrip("0")
        return s

    @staticmethod
    def utc(t):
        dt = datetime.fromtimestamp(t / 1000, tz=timezone.utc)
        s = dt.strftime("%Y-%m-%dT%H:%M:%S")
        if t % 1000:
            s += f".{t % 1000:03d}"
        return s + "Z"

    def offset_text(self):
        sign = "+" if self.utc_offset_min >= 0 else "-"
        h, m = divmod(abs(self.utc_offset_min), 60)
        return f"{sign}{h:02d}:{m:02d}"

    # Input and locks, from which presence samples are computed.

    def pointer(self, *times):
        for s in times:
            self.inputs.append((self.at(s), False))

    def keys_at(self, *times):
        for s in times:
            self.inputs.append((self.at(s), True))

    def typing(self, start, end, every_ms):
        """Key presses from start to end inclusive, every every_ms."""
        t, stop = self.at(start), self.at(end)
        while t <= stop:
            self.inputs.append((t, True))
            t += every_ms
        if self.inputs[-1][0] != stop:
            self.inputs.append((stop, True))

    def pointer_every(self, start, end, every_ms):
        t, stop = self.at(start), self.at(end)
        while t <= stop:
            self.inputs.append((t, False))
            t += every_ms

    def lock(self, start, end):
        self.locks.append((self.at(start), self.at(end)))

    def ticks(self, start, end, every_ms):
        """Regular presence samples from start to end inclusive, on multiples
        of every_ms in UTC."""
        t, stop = self.at(start), self.at(end)
        t += (-t) % every_ms
        while t <= stop:
            self.samples.add(t)
            t += every_ms

    def sample(self, t):
        self.samples.add(t)

    # Events.

    def emit(self, t, kind, data, *, source, via, harness="none", session="none", project=None,
             version=None, sample=False):
        """Adds an event at Unix ms t. sample takes a presence sample at the
        same moment, as the agent does on every hook, focus and power event
        (DESIGN 3.3)."""
        env = {"source": source, "via": via, "harness": harness}
        if version:
            env["harness_version"] = version
        env["session_key"] = session
        if project:
            env["project_key"] = project
        self.raw.append([t, len(self.raw), env, kind, data])
        if sample:
            self.samples.add(t)
        return self.raw[-1]

    def presence(self, s):
        last_any = self.last_input
        last_key = self.last_key
        for t, is_key in self.inputs:
            if t > s:
                break
            last_any = max(last_any, t)
            if is_key:
                last_key = t if last_key is None else max(last_key, t)
        locked = any(a <= s < b for a, b in self.locks)
        data = {"idle_any_s": Seconds(s - last_any)}
        if self.keys:
            data["idle_keys_s"] = Seconds(s - last_key)
        else:
            data["idle_keys_s"] = None
        data["locked"] = locked
        return data

    def last_input_before(self, s, keys_only=False):
        best = self.last_key if keys_only else self.last_input
        for t, is_key in self.inputs:
            if t > s:
                break
            if is_key or not keys_only:
                best = t if best is None else max(best, t)
        return best

    def build(self):
        """Sorts the events, adds presence samples and numbers them."""
        self.inputs.sort()
        rows = [list(r) for r in self.raw]
        for s in sorted(self.samples):
            # A sample comes after the other events of its millisecond: they
            # are what triggered it.
            rows.append([s, 1 << 40, {"source": "os", "via": "os", "harness": "none", "session_key": "none"},
                         "os.presence", self.presence(s)])
        rows.sort(key=lambda r: (r[0], r[1]))
        events = []
        for i, (t, _, env, kind, data) in enumerate(rows):
            seq = self.seq0 + i
            ev = {
                "event_id": uuid7(t, f"{self.name}:{seq}"),
                "device_id": self.device,
                "seq": seq,
                "t": t,
                "mono": t - self.boot,
                "boot_id": self.boot_id,
            }
            ev.update(env)
            ev["kind"] = kind
            ev["data"] = data
            events.append(ev)
        self.events = events
        return events

    def event_at(self, kind, t, **match):
        """The event of kind at Unix ms t whose data has the given values."""
        found = [e for e in self.events if e["kind"] == kind and e["t"] == t
                 and all(e["data"].get(k) == v for k, v in match.items())]
        if len(found) != 1:
            raise SystemExit(f"{self.name}: {len(found)} {kind} events at {self.hhmmss(t)} matching {match}")
        return found[0]

    # Writing.

    def write(self, out, expected, anchors, prompts):
        os.makedirs(out, exist_ok=True)
        with open(os.path.join(out, "events.jsonl"), "w", encoding="utf-8", newline="\n") as f:
            for e in self.events:
                f.write(encode(e) + "\n")
        for name, doc in (("expected.json", expected), ("anchors.json", anchors), ("prompts.json", prompts)):
            with open(os.path.join(out, name), "w", encoding="utf-8", newline="\n") as f:
                f.write(pretty(doc) + "\n")


def out_dir(default):
    """The --out directory from the command line, or default."""
    args = sys.argv[1:]
    if args[:1] == ["--out"] and len(args) == 2:
        return args[1]
    if args:
        raise SystemExit("usage: gen.py [--out DIR]")
    return default


def check(cond, msg):
    if not cond:
        raise SystemExit("check failed: " + msg)


def ordered(m, order):
    """m's nonzero entries in the given key order."""
    for k in m:
        if k not in order:
            raise SystemExit(f"unknown reason code {k!r}")
    return {k: m[k] for k in order if m.get(k)}


class Expected:
    """expected.json: the minute rows and totals SM states for a fixture."""

    def __init__(self, fx, *, sm, evidence, witness, projects, start, end):
        self.fx = fx
        self.sm = sm
        self.evidence = evidence
        self.witness = witness
        self.projects = projects
        self.start = fx.at(start)
        self.end = fx.at(end)
        check(self.start % MINUTE_MS == 0 and self.end % MINUTE_MS == 0, "window on minute boundaries")
        self.rows = []

    def row(self, start, end=None, *, credit=None, uncredited=None, codes=None, agent=0, bucket=True,
            bucket_minutes=None, sm=None, note=None, derived=None, erratum=None):
        """One minute (end omitted) or a run of minutes [start, end).
        bucket_minutes says how many of a run's minutes have a bucket; by
        default all of them, or none when bucket is False."""
        a = self.fx.at(start)
        b = self.fx.at(end) if end else a + MINUTE_MS
        check(a % MINUTE_MS == 0 and b % MINUTE_MS == 0 and b > a, f"row {start} on minute boundaries")
        check(not self.rows or self.rows[-1]["_end"] == a, f"row {start} follows the previous row")
        credit = credit or {}
        credit = {p: ordered(r, CREDIT_REASONS) for p, r in credit.items()}
        credited = sum(sum(r.values()) for r in credit.values())
        n = (b - a) // MINUTE_MS
        row = {"start": self.fx.utc(a), "minutes": n,
               "local": self.fx.hhmm(a) if n == 1 else f"{self.fx.hhmm(a)}-{self.fx.hhmm(b - MINUTE_MS)}",
               "bucket": bucket}
        if bucket:
            bm = n if bucket_minutes is None else bucket_minutes
            check(1 <= bm <= n, f"row {start}: bucket_minutes {bm}")
            row["bucket_minutes"] = bm
            row["credited_s"] = credited
            row["credit"] = credit
            if uncredited is None:
                row["uncredited"] = None
                row["codes"] = codes
            else:
                unc = ordered(uncredited, UNCREDITED)
                row["uncredited"] = unc
                check(credited + sum(unc.values()) == 60 * bm,
                      f"row {start}: {credited} credited + {sum(unc.values())} uncredited != {60 * bm}")
            row["agent_running_s"] = agent
            check(0 <= agent <= 60 * bm, f"row {start}: agent_running_s {agent}")
        else:
            check(not credit and not agent, f"row {start}: a minute without a bucket has no credit or agent time")
            if codes:
                row["codes"] = codes
        if sm:
            row["sm"] = sm
        if note:
            row["note"] = note
        if derived:
            row["derived"] = derived
        if erratum:
            row["erratum"] = erratum
        row["_end"] = b
        self.rows.append(row)

    def totals(self):
        by_project, by_reason, unc = {}, {}, {}
        agent = 0
        for r in self.rows:
            if not r["bucket"]:
                continue
            for p, reasons in r["credit"].items():
                for k, s in reasons.items():
                    by_project[p] = by_project.get(p, 0) + s
                    by_reason[k] = by_reason.get(k, 0) + s
            for k, s in (r["uncredited"] or {}).items():
                unc[k] = unc.get(k, 0) + s
            agent += r["agent_running_s"]
        return {
            "credited_s": sum(by_project.values()),
            "credited_by_project": by_project,
            "credited_by_reason": ordered(by_reason, CREDIT_REASONS),
            "uncredited": ordered(unc, UNCREDITED),
            "agent_running_s": agent,
        }

    def doc(self, *, extra_totals, stated, approx, errata, notes):
        check(self.rows[0]["_end"] - MINUTE_MS * self.rows[0]["minutes"] == self.start, "rows start at the window")
        check(self.rows[-1]["_end"] == self.end, "rows end at the window")
        totals = self.totals()
        for k, v in extra_totals.items():
            if k in totals:
                check(totals[k] == v, f"total {k}: rows sum to {totals[k]}, stated {v}")
            totals[k] = v
        for k in stated:
            check(k in totals, f"stated total {k} is in totals")
        rows = [{k: v for k, v in r.items() if k != "_end"} for r in self.rows]
        fx = self.fx
        return {
            "fixture": fx.name,
            "title": fx.title,
            "sm": self.sm,
            "date": fx.date,
            "timezone": fx.timezone_name,
            "utc_offset": fx.offset_text(),
            "os": fx.os_name,
            "input_keys": fx.keys,
            "evidence": self.evidence,
            "witness": self.witness,
            "device": fx.device,
            "projects": self.projects,
            "window": {"start": fx.utc(self.start), "end": fx.utc(self.end),
                       "local": f"{fx.hhmm(self.start)}-{fx.hhmm(self.end)}"},
            "rows": rows,
            "totals": totals,
            "stated": stated,
            "approx": approx,
            "errata": errata,
            "notes": notes,
        }


def anchor(fx, event, *, kind, origin=None, prompt_class=None, lead_s, trail_s, lead_from, level, sm):
    a = {"event_id": event["event_id"], "kind": kind, "t": event["t"], "local": fx.hhmmss(event["t"]),
         "project_key": event.get("project_key")}
    if origin:
        a["origin"] = origin
    if prompt_class:
        a["prompt_class"] = prompt_class
    a.update({"lead_s": lead_s, "trail_s": trail_s, "lead_from": lead_from, "via": event["via"],
              "level": level, "sm": sm})
    return a


def turn(fx, submit, stop, *, session, turn_key, origin, prompt_class=None, sm=None):
    t = {"session_key": session, "turn_key": turn_key, "t_submit": submit, "local_submit": fx.hhmmss(submit)}
    if stop is not None:
        t["t_stop"] = stop
        t["local_stop"] = fx.hhmmss(stop)
    t["origin"] = origin
    if prompt_class:
        t["prompt_class"] = prompt_class
    if sm:
        t["sm"] = sm
    return t


class Rand:
    """A deterministic number source: SHA-256 of a seed and a counter.
    Python's random module would do, but its output isn't promised across
    versions, and the generators must reproduce byte for byte."""

    def __init__(self, seed):
        self.seed = seed
        self.n = 0

    def below(self, n):
        self.n += 1
        h = hashlib.sha256(f"{self.seed}:{self.n}".encode()).digest()
        return int.from_bytes(h[:8], "big") % n

    def between(self, lo, hi):
        """An integer in [lo, hi]."""
        return lo + self.below(hi - lo + 1)


class Bucket:
    """SM 2.4's reading bucket for one session, used only to check that a
    fixture's events give the bucket SM describes. It isn't the scorer.

    Amounts are fractions of a second (fractions.Fraction), fills happen at
    their event's time, the cap evicts the oldest entries, entries expire
    BUCKET_TTL_S after they were filled, and draining takes the oldest
    first."""

    READ_WPM, DIFF_LPM, TOOL_LINES_MAX = 180, 8, 4
    BASE_S, MAX_S, TTL_MS = 20, 600, 7_200_000

    def __init__(self):
        from fractions import Fraction
        self.F = Fraction
        self.entries = []  # [fill_ms, seconds]

    def amount(self, kind, data):
        F = self.F
        if kind == "agent.output":
            return F(60 * data["words"], self.READ_WPM) + F(60 * data["code_lines"], self.DIFF_LPM)
        if kind == "agent.tool":
            lines = data["lines_added"] + data["lines_removed"] + min(data["visible_output_lines"], self.TOOL_LINES_MAX)
            return F(60 * lines, self.DIFF_LPM)
        return F(0)

    def reset(self, t, base):
        self.entries = [[t, self.F(base)]] if base else []

    def fill(self, t, seconds):
        if seconds <= 0:
            return
        self.entries.append([t, self.F(seconds)])
        over = self.total() - self.MAX_S
        while over > 0:
            if self.entries[0][1] <= over:
                over -= self.entries[0][1]
                self.entries.pop(0)
            else:
                self.entries[0][1] -= over
                over = 0

    def expire(self, t):
        self.entries = [e for e in self.entries if t - e[0] <= self.TTL_MS]

    def total(self):
        return sum((e[1] for e in self.entries), self.F(0))

    def drain(self, seconds):
        """Drains up to seconds and returns how much it could."""
        got = self.F(0)
        need = self.F(seconds)
        while need > 0 and self.entries:
            take = min(need, self.entries[0][1])
            self.entries[0][1] -= take
            need -= take
            got += take
            if self.entries[0][1] == 0:
                self.entries.pop(0)
        return got


def hackatime_replay(times):
    """SM 5.3's logged_s rule over sorted Unix ms times: the sum of
    min(gap, 120 s) between consecutive records, in seconds."""
    total = 0
    for a, b in zip(times, times[1:]):
        total += min(b - a, 120_000)
    return total / 1000
