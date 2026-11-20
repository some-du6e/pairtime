# SPDX-License-Identifier: AGPL-3.0-or-later
"""Builds the consistency-check fixtures in testdata/checks: copies of SM
fixtures, edited the way DESIGN 7.3's "Catches" column describes, with the
inputs each check reads and the result each check must give. README.md in
this directory is the format.

gen.py describes every scenario and states its expected results by hand.
This module holds what the scenarios share: loading an SM fixture's
events, the edits, the ledger, the keyed content values, and a reading of
each check's rule that computes its result and counts from the scenario's
own files. write() refuses to write a scenario whose hand-stated results
and computed results disagree. Neither half is pkg/check.

Standard library only. Run gen.py with the interpreter in isolated mode:

    python3 -I testdata/checks/gen.py [--out DIR]
"""

import bisect
import hashlib
import hmac
import json
import os
import re
import sys
from fractions import Fraction

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
TIMELINES = os.path.join(os.path.dirname(HERE), "timelines")
sys.path.insert(0, TIMELINES)

import timeline as tl  # noqa: E402

CHECKS = ["CC1", "CC2", "CC3", "CC4", "CC5", "CC6", "CC7", "CC8", "CC9", "CC10", "CC11"]
RESULTS = ("pass", "fail", "not_computable")

SLOT_MS = 15_000
MINUTE_MS = 60_000
# SM 2.2 rule 6: input in the KEYS_PRE_S = 10 s before submit.
KEYS_PRE_MS = 10_000
# DESIGN 7.3 CC1: the transcript's entry within 5 s of the hook's.
CC1_SKEW_MS = 5_000
# DESIGN 7.3 CC3: more than half of a commit's lines, and more than 200.
CC3_LINES = 200
# DESIGN 7.3 CC4: server_first_seen_at before observed_at plus 15 minutes;
# on App-installed repos, push time not earlier than observed_at minus 60 s.
CC4_SEEN_MS = 900_000
CC4_PUSH_MS = 60_000
# SM 2.3 and 8.2: the lead of a prompt anchor.
THINK_LEAD_S, ANSWER_LEAD_S, COMPOSE_MAX_S = 120, 60, 300
COMPOSE_CPS = Fraction(3, 2)
SUBSTANTIVE_CHARS = 20
# DESIGN 6.3: CHALLENGE_K = max(10, 10% of the pool), at most 30.
CHALLENGE_K_MIN, CHALLENGE_K_MAX = 10, 30

# DESIGN 5.1's "Second record (lookback and CC1)" column: the harnesses
# with a v1 log reader. Cursor's state.vscdb reader is v1.5, and the
# harnesses behind `pairtime track` get theirs in v1.5 too.
LOG_READERS = {"claude_code", "codex", "copilot", "gemini", "devin", "cline"}

# Ledger record types, ledger-v1.md 4.2: the 18 kinds in kindTable order.
KINDS = ["session.start", "session.end", "prompt.submit", "agent.output", "agent.tool", "agent.permission",
         "agent.interrupt", "agent.queue", "agent.stop", "agent.subagent", "editor.activity", "shell.command",
         "git.change", "os.presence", "os.focus", "os.power", "otel.active_time", "cli.track"]
RTYPE = {k: i + 1 for i, k in enumerate(KINDS)}

# The fields Event.Shareable clears, as pkg/event's TestLocalFields pins them.
LOCAL = {
    "prompt.submit": ("minhash", "template_hash", "error_sig", "text"),
    "agent.tool": ("cmd_hash",),
    "shell.command": ("cmd_hash",),
    "os.focus": ("app_hash",),
    "cli.track": ("label",),
    "agent.stop": ("pending_crons",),
}

ENVELOPE = ["event_id", "device_id", "seq", "t", "mono", "boot_id", "source", "via", "harness",
            "harness_version", "session_key", "project_key", "kind", "data"]
# Payload fields that hold times, which a shift moves with the event.
TIME_FIELDS = ("t_start", "t_end", "requested_at", "answered_at")


def check(cond, msg):
    tl.check(cond, msg)


def ms(seconds):
    """JSON seconds to integer milliseconds."""
    return round(seconds * 1000)


def u64(x):
    return x.to_bytes(8, "big")


def sha256(b):
    return hashlib.sha256(b).digest()


def hmac256(key, msg):
    return hmac.new(key, msg, hashlib.sha256).digest()


def utc(t):
    return tl.Fixture.utc(t)


def minute_of(t):
    return t - t % MINUTE_MS


def read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


# Loading and editing one device's events.


class Device:
    """One device's events, loaded from an SM fixture and edited in place.
    Times are Unix ms. The events stay in arrival order; finish() numbers
    them."""

    def __init__(self, base):
        self.base = base
        path = os.path.join(TIMELINES, base, "events.jsonl")
        with open(path, encoding="utf-8") as f:
            lines = f.read().splitlines()
        self.events = [json.loads(line) for line in lines]
        for line, e in zip(lines, self.events):
            check(tl.encode(e) == line, f"{base}: an event re-encodes to its line")
        self.expected = read_json(os.path.join(TIMELINES, base, "expected.json"))
        self.prompt_texts = read_json(os.path.join(TIMELINES, base, "prompts.json"))
        first = self.events[0]
        self.id = first["device_id"]
        self.seq0 = first["seq"]
        self.boots = {}
        for e in self.events:
            self.boots.setdefault(e["boot_id"], e["t"] - e["mono"])
        samples = [e for e in self.events if e["kind"] == "os.presence"]
        self.keys = samples[0]["data"]["idle_keys_s"] is not None
        # The input times the samples show: each sample says when the last
        # input before it was. Recomputing every sample from these gives
        # the samples back exactly; an edit changes the times and then the
        # samples.
        self.any_in = sorted({e["t"] - ms(e["data"]["idle_any_s"]) for e in samples})
        self.key_in = sorted({e["t"] - ms(e["data"]["idle_keys_s"]) for e in samples}) if self.keys else []
        self.presence_dirty = False
        check(self.resampled() == [e["data"] for e in samples], f"{base}: the input times give back every sample")

    # Finding events.

    def find(self, kind, t=None, **match):
        """The events of kind at t whose envelope or payload has the given
        values: via, source, harness, session_key and project_key are
        envelope fields, anything else a payload field."""
        def field(e, k):
            return e.get(k) if k in ("via", "source", "harness", "session_key", "project_key") else e["data"].get(k)
        return [e for e in self.events if e["kind"] == kind and (t is None or e["t"] == t)
                and all(field(e, k) == v for k, v in match.items())]

    def one(self, kind, t=None, **match):
        found = self.find(kind, t, **match)
        check(len(found) == 1, f"{self.base}: {len(found)} {kind} events at {t} matching {match}")
        return found[0]

    def turn_prompts(self, via="hook"):
        return [e for e in self.events if e["kind"] == "prompt.submit" and e["via"] == via]

    # Editing events.

    def insert(self, e):
        """Adds e after every event at or before its time."""
        ts = [x["t"] for x in self.events]
        self.events.insert(bisect.bisect_right(ts, e["t"]), e)
        return e

    def remove(self, pred):
        before = len(self.events)
        self.events = [e for e in self.events if not pred(e)]
        return before - len(self.events)

    def like(self, proto, t, kind, data, **env):
        """A new event at t with proto's envelope, changed by env."""
        e = {"event_id": "", "device_id": proto["device_id"], "seq": 0, "t": t, "mono": 0,
             "boot_id": proto["boot_id"]}
        for k in ("source", "via", "harness", "harness_version", "session_key", "project_key"):
            v = env[k] if k in env else proto.get(k)
            if v is not None:
                e[k] = v
        e["kind"] = kind
        e["data"] = data
        return e

    def shift(self, delta):
        """Moves every event and input by delta ms, as if the same timeline
        happened at another time."""
        for e in self.events:
            e["t"] += delta
            for k in TIME_FIELDS:
                if k in e["data"]:
                    e["data"][k] += delta
        self.boots = {b: t + delta for b, t in self.boots.items()}
        self.any_in = [t + delta for t in self.any_in]
        self.key_in = [t + delta for t in self.key_in]

    # Editing input. Presence samples follow at finish().

    def drop_keys(self, a, b):
        """Removes the key presses from a to b inclusive: the person didn't
        type then."""
        gone = {t for t in self.key_in if a <= t <= b}
        self.key_in = [t for t in self.key_in if t not in gone]
        self.any_in = [t for t in self.any_in if t not in gone]
        self.presence_dirty = True
        return sorted(gone)

    def drop_input(self, a, b):
        """Removes all input from a to b inclusive."""
        self.key_in = [t for t in self.key_in if not a <= t <= b]
        self.any_in = [t for t in self.any_in if not a <= t <= b]
        self.presence_dirty = True

    def add_keys(self, *times):
        self.key_in = sorted(set(self.key_in) | set(times))
        self.any_in = sorted(set(self.any_in) | set(times))
        self.presence_dirty = True

    def resampled(self):
        out = []
        for e in self.events:
            if e["kind"] != "os.presence":
                continue
            s = e["t"]
            i = bisect.bisect_right(self.any_in, s)
            check(i > 0, f"{self.base}: an input before the sample at {s}")
            data = {"idle_any_s": tl.Seconds(s - self.any_in[i - 1])}
            if self.keys:
                j = bisect.bisect_right(self.key_in, s)
                check(j > 0, f"{self.base}: a key press before the sample at {s}")
                data["idle_keys_s"] = tl.Seconds(s - self.key_in[j - 1])
            else:
                data["idle_keys_s"] = None
            data["locked"] = e["data"]["locked"]
            out.append(json.loads(tl.encode(data)))
        return out

    def keys_in_slot(self, start):
        """Whether the samples show a key press in the slot starting at
        start: the sample at its end reports one since then."""
        end = start + SLOT_MS
        j = bisect.bisect_left(self.key_in, end)
        return j > 0 and self.key_in[j - 1] >= start

    def finish(self):
        """Recomputes presence, then numbers the events: seq from the base's
        first seq, mono from the boot, and event ids from the base, the
        device and seq, so scenarios with the same records write the same
        bytes."""
        if self.presence_dirty:
            samples = [e for e in self.events if e["kind"] == "os.presence"]
            for e, data in zip(samples, self.resampled()):
                e["data"] = data
        self.events.sort(key=lambda e: e["t"])  # stable: same-ms events keep their order
        for i, e in enumerate(self.events):
            e["seq"] = self.seq0 + i
            e["event_id"] = tl.uuid7(e["t"], f"checks/{self.base}/{self.id}:{e['seq']}")
            e["mono"] = e["t"] - self.boots[e["boot_id"]]
            for k in list(e):
                check(k in ENVELOPE, f"envelope field {k}")
            ordered = {k: e[k] for k in ENVELOPE if k in e}
            e.clear()
            e.update(ordered)


def transcript_copy(dev, hook, delay_ms):
    """The log reader's record of the user entry a hook prompt.submit has
    in the transcript: the same turn and lengths, via log."""
    return dev.like(hook, hook["t"] + delay_ms, "prompt.submit", dict(hook["data"]), source="log", via="log")


# The ledger: ledger-v1.md part 1, and part 2's f and c for prompts.


def device_keys(device_id):
    """Test keys for a fixture device, published here and used nowhere
    else: K_dev from a label, the rest by ledger-v1.md 3."""
    k_dev = sha256(f"pairtime check fixtures: K_dev of {device_id}".encode())
    prk = hmac256(bytes(32), k_dev)
    keys = {"k_dev": k_dev}
    for name, label in (("k_leaf", "leaf"), ("k_tok", "token"), ("k_feat", "feat")):
        keys[name] = hmac256(prk, f"pairtime/v1/{label}".encode() + b"\x01")
    return keys


def project_digest(key):
    return sha256(key.encode())[:16] if key else bytes(16)


def session_digest(key):
    return bytes(8) if key == "none" else sha256(key.encode())[:8]


def header(e):
    project = None if e["kind"].startswith("os.") else e.get("project_key")
    return u64(e["seq"]) + bytes([RTYPE[e["kind"]]]) + project_digest(project) + session_digest(e["session_key"])


def shareable(e):
    data = {k: v for k, v in e["data"].items() if k not in LOCAL.get(e["kind"], ())}
    return {**e, "data": data}


def body(e, k_leaf, extra=None):
    nonce = hmac256(k_leaf, b"\x01" + u64(e["seq"]))
    b = '{"n":"' + nonce.hex() + '","e":' + tl.encode(shareable(e))
    for k, v in (extra or {}).items():
        b += "," + json.dumps(k) + ":" + tl.encode(v)
    return b + "}"


def leaf(data):
    return sha256(b"\x00" + data)


def node(a, b):
    return sha256(b"\x01" + a + b)


def root(hashes):
    """RFC 9162's tree over leaf hashes (ledger-v1.md 5.1)."""
    if not hashes:
        return sha256(b"")
    if len(hashes) == 1:
        return hashes[0]
    k = 1
    while k * 2 < len(hashes):
        k *= 2
    return node(root(hashes[:k]), root(hashes[k:]))


def entry(hdr, body_bytes):
    return hdr + sha256(body_bytes)


def batch_head(prev, r, n, b):
    return sha256(b"\x03" + prev + r + u64(n) + u64(b))


def seals_at_once(e):
    """The writer seals a batch at once on lock, suspend, prompt and stop
    (ledger-v1.md 6). Here: the hook's prompt.submit and agent.stop, and
    os.power lock and suspend."""
    if e["kind"] in ("prompt.submit", "agent.stop"):
        return e["via"] == "hook"
    return e["kind"] == "os.power" and e["data"]["op"] in ("lock", "suspend")


def ledger(dev, b0, extras=None, hide=(), lag=None):
    """Seals dev's events into batches and returns the reveal and the rows
    the server witnessed. Batches close at every UTC minute and at once
    after a record seals_at_once names, so no batch spans a minute. extras
    maps a seq to more body members (f, c). hide lists seqs the client
    leaves out of its reveal: the server's rows still cover them."""
    keys = device_keys(dev.id)
    extras = extras or {}
    batches, cur = [], []
    for e in dev.events:
        if cur and minute_of(e["t"]) != minute_of(cur[-1]["t"]):
            batches.append((cur, minute_of(cur[-1]["t"]) + MINUTE_MS))
            cur = []
        cur.append(e)
        if seals_at_once(e):
            batches.append((cur, e["t"]))
            cur = []
    if cur:
        batches.append((cur, minute_of(cur[-1]["t"]) + MINUTE_MS))
    rand = tl.Rand(f"checks/{dev.base}/{dev.id}/upload")
    prev = sha256(f"stand-in head of batch {b0 - 1} of {dev.id}".encode())
    revealed, witnessed = [], []
    for i, (recs, sealed) in enumerate(batches):
        b = b0 + i
        bodies = [body(e, keys["k_leaf"], extras.get(e["seq"])) for e in recs]
        entries = [entry(header(e), bd.encode()) for e, bd in zip(recs, bodies)]
        r = root([leaf(x) for x in entries])
        h = batch_head(prev, r, len(recs), b)
        received = sealed + (lag(b, sealed) if lag else rand.between(250, 1500))
        witnessed.append({"b": b, "n": len(recs), "prev": prev.hex(), "root": r.hex(), "head": h.hex(),
                          "received_at": received})
        records = []
        for e, bd in zip(recs, bodies):
            if e["seq"] in hide:
                continue
            records.append({"seq": e["seq"], "rtype": RTYPE[e["kind"]], "project_key": header_project(e),
                            "session_key": e["session_key"], "body": bd})
        revealed.append({"b": b, "records": records})
        prev = h
    return {"device_id": dev.id, "batches": revealed, "witnessed": witnessed}


def header_project(e):
    if e["kind"].startswith("os."):
        return ""
    return e.get("project_key") or ""


# Content: ledger-v1.md 10 (PR #12), for texts of plain lowercase words.

WHITESPACE = {chr(c) for c in [*range(0x09, 0x0E), 0x20, 0x85, 0xA0, 0x1680, *range(0x2000, 0x200B),
                               0x2028, 0x2029, 0x202F, 0x205F, 0x3000]}
RETRY_WORDS = {"again", "retry", "try", "fix", "still", "doesnt", "broken"}
GO_AHEADS = {"yes", "y", "ok", "okay", "sure", "continue", "go", "go on", "go ahead", "do it", "proceed", "next",
             "keep going", "lgtm", "looks good", "sounds good", "thanks", "try again", "again", "retry", "fix it",
             "more"}


def tokens(text):
    """Maximal runs of whitespace and of other code points (10.2)."""
    out = []
    for ch in text:
        if out and (ch in WHITESPACE) == (out[-1][0] in WHITESPACE):
            out[-1] += ch
        else:
            out.append(ch)
    return out


def commitment(k_tok, seq, text):
    """Each token's salt and leaf, and the body's c (10.2)."""
    toks = tokens(text)
    salts = [hmac256(k_tok, u64(seq) + u64(j)) for j in range(1, len(toks) + 1)]
    leaves = [sha256(b"\x00" + s + w.encode()) for s, w in zip(salts, toks)]
    return {"tokens": toks, "salts": salts, "leaves": leaves, "c": {"n": len(toks), "r": root(leaves).hex()}}


def plain_words(text):
    """10.4's normalization for the texts these fixtures commit: words of
    lowercase ASCII letters between single spaces, which every step of
    10.4 leaves as they are. Anything else is refused rather than guessed."""
    check(re.fullmatch(r"[a-z]+( [a-z]+)*", text) is not None, f"plain lowercase words: {text!r}")
    words = text.split(" ")
    check(not RETRY_WORDS & set(words), f"no retry phrase can match in {text!r}")
    return words


def shingles(words):
    """10.5: word 3-shingles from six words, else character 5-shingles."""
    if len(words) >= 6:
        return sorted({" ".join(words[i:i + 3]) for i in range(len(words) - 2)}), 0x05
    norm = " ".join(words)
    if len(norm) <= 4:
        return ([norm] if norm else []), 0x06
    return sorted({norm[i:i + 5] for i in range(len(norm) - 4)}), 0x06


def shingle_value(key, tag, x):
    return hmac256(key, bytes([tag]) + x.encode())


def sketch(values):
    """The 64 MinHash values from per-shingle values (10.5), as hex."""
    mh = [0xFFFFFFFF] * 64
    for v in values:
        for k in range(8):
            blk = sha256(b"\x05" + v + bytes([k]))
            for r in range(8):
                i = 8 * k + r
                mh[i] = min(mh[i], int.from_bytes(blk[4 * r:4 * r + 4], "big"))
    return "".join(f"{x:08x}" for x in mh)


def keyed16(key, tag, x):
    return hmac256(key, bytes([tag]) + x.encode())[:16].hex()


def prompt_class(text, typed_len, answers_question=False):
    """SM 2.2's class for a text with no error signature."""
    bare = re.sub(r"[^\w\s]", "", text.lower())
    norm = " ".join(bare.split())
    if answers_question:
        return "answer"
    if len(norm.replace(" ", "")) < 4 or norm in GO_AHEADS:
        return "low_effort"
    return "substantive" if typed_len >= SUBSTANTIVE_CHARS else "low_effort"


def features(key, text, pc):
    """A prompt's f (10.7) under key: the sketch, the template and the
    class. None of these texts has an error signature, so f has no es."""
    words = plain_words(text)
    xs, tag = shingles(words)
    mh = sketch([shingle_value(key, tag, x) for x in xs])
    return {"v": 1, "mh": mh, "th": keyed16(key, 0x02, " ".join(words)), "pc": pc}


def shingle_reveal(key, text, hidden=()):
    """What CC8 reveals for a text (10.8): each distinct shingle's value
    under key, ascending, with its text unless it touches a removed word.
    hidden holds the indexes, from 0, of the removed words."""
    words = plain_words(text)
    xs, tag = shingles(words)
    touched = set()
    if tag == 0x05:
        for i in range(len(words) - 2):
            if hidden and any(i <= h <= i + 2 for h in hidden):
                touched.add(" ".join(words[i:i + 3]))
    out = []
    for x in xs:
        v = shingle_value(key, tag, x)
        out.append((v, None if x in touched else x))
    out.sort(key=lambda p: p[0])
    return [{"v": v.hex(), "x": x.encode().hex()} if x is not None else {"v": v.hex()} for v, x in out]


def text_reveal(com, removed_words=(), kind="teen"):
    """The redacted reveal of a committed text (10.3): kept tokens with
    their salts, and each removed word as a one-token span."""
    toks = com["tokens"]
    word_index, items = -1, []
    for j, tok in enumerate(toks):
        if tok[0] not in WHITESPACE:
            word_index += 1
            if word_index in removed_words:
                items.append({"type": kind, "count": 1, "leaves": [com["leaves"][j].hex()]})
                continue
        items.append({"salt": com["salts"][j].hex(), "token": tok.encode().hex()})
    return items


# Readings of DESIGN 7.3's rules. Each returns a result, its counts and the
# items behind any failure or anything not computable.


def outcome(result, counts, items=None, reason=None):
    check(result in RESULTS, f"result {result}")
    out = {"result": result, "counts": counts}
    if items:
        out["items"] = items
    if reason:
        out["reason"] = reason
    return out


def in_window(sub, t):
    return sub["start"] <= t < sub["end"]


def sessions_of(events):
    starts = {}
    for e in events:
        if e["kind"] == "session.start":
            starts.setdefault(e["session_key"], []).append(e)
    return starts


def prompts_in(events, sub):
    """The prompts of a submission, one per turn: the hook's record when
    there is one, else the log reader's."""
    by_turn = {}
    for e in events:
        if e["kind"] != "prompt.submit" or e.get("project_key") not in sub["projects"]:
            continue
        k = (e["session_key"], e["data"]["turn_key"])
        if k not in by_turn or (by_turn[k]["via"] != "hook" and e["via"] == "hook"):
            by_turn[k] = e
    return [e for e in sorted(by_turn.values(), key=lambda e: e["t"]) if in_window(sub, e["t"])]


def automated(e, starts):
    """Whether SM 2.2 rules 1 to 5 label the prompt from what the harness
    declares. Rule 2's continuations have no prompt, so they never reach
    here."""
    d = e["data"]
    if d["matches_pending_cron"] or d["origin_declared"] != "none":
        return True
    for st in starts.get(e["session_key"], []):
        s = st["data"]
        if s["entrypoint"] in ("sdk", "exec", "cloud") or s["parent_class"] in ("script_runtime", "scheduler",
                                                                               "agent"):
            return True
        if not s["has_tty"] and s["entrypoint"] not in ("ide", "desktop") and s["parent_class"] != "ide":
            return True
    return False


def focus_at(events, t):
    last = None
    for e in events:
        if e["t"] > t:
            break
        if e["kind"] == "os.focus":
            last = e
    return last


def sample_at(events, t):
    """The presence sample taken at a hook event: the one at the same
    millisecond, which comes after the event."""
    for e in events:
        if e["t"] == t and e["kind"] == "os.presence":
            return e
    return None


def ide_session(starts, session):
    return any(s["data"]["entrypoint"] in ("ide", "desktop") or s["data"]["parent_class"] == "ide"
               for s in starts.get(session, []))


def cc1(events, sub):
    """Hook against log: each hook prompt with the transcript's entry for
    its turn, within 5 s, the same length, in the same order; and the
    reverse. Sessions whose harness has no v1 log reader aren't
    computable."""
    hooks, logs = {}, {}
    for e in events:
        if e["kind"] != "prompt.submit" or e.get("project_key") not in sub["projects"] or not in_window(sub, e["t"]):
            continue
        side = hooks if e["via"] == "hook" else logs if e["via"] == "log" else None
        if side is not None:
            side[(e["session_key"], e["data"]["turn_key"])] = e
    counts = {"hook_prompts": len(hooks), "log_entries": len(logs), "matched": 0, "hook_only": 0, "log_only": 0,
              "not_computable": 0}
    items, matched = [], []
    for k, h in sorted(hooks.items(), key=lambda kv: kv[1]["t"]):
        if h["harness"] not in LOG_READERS:
            counts["not_computable"] += 1
            items.append({"turn_key": k[1], "t": h["t"], "harness": h["harness"], "status": "not_computable",
                          "why": "no v1 log reader for this harness (DESIGN 5.1)"})
            continue
        lg = logs.get(k)
        if lg is None or abs(lg["t"] - h["t"]) > CC1_SKEW_MS or lg["data"]["len_chars"] != h["data"]["len_chars"]:
            counts["hook_only"] += 1
            items.append({"turn_key": k[1], "t": h["t"], "status": "hook_only",
                          "why": "no transcript entry for this turn" if lg is None else "entry differs"})
            continue
        counts["matched"] += 1
        matched.append((h["t"], lg["t"], k[1]))
    for k, lg in sorted(logs.items(), key=lambda kv: kv[1]["t"]):
        if k not in hooks:
            counts["log_only"] += 1
            items.append({"turn_key": k[1], "t": lg["t"], "status": "log_only", "why": "no hook record for this turn"})
    order = sorted(matched, key=lambda m: m[1])
    if [m[2] for m in order] != [m[2] for m in sorted(matched)]:
        counts["out_of_order"] = 1
    failed = counts["hook_only"] or counts["log_only"] or counts.get("out_of_order")
    if failed:
        return outcome("fail", counts, items)
    if counts["matched"]:
        return outcome("pass", counts, items)
    return outcome("not_computable", counts, items, "no session of the submission has a v1 log reader")


def cc2(events, sub, keys):
    """Prompt against OS: of the prompts SM 2.2 rules 1 to 5 don't label,
    the ones with OS input in the 10 s before submit (keys where the
    machine has them) and focus at submit on the window hosting the
    session. DESIGN gives no share for a verdict, so a share strictly
    between none and all is refused here (README, errata)."""
    starts = sessions_of(events)
    counts = {"prompts": 0, "with_input_and_focus": 0, "no_input": 0, "no_focus": 0}
    items = []
    for p in prompts_in(events, sub):
        if automated(p, starts):
            continue
        counts["prompts"] += 1
        s = sample_at(events, p["t"])
        check(s is not None, f"a presence sample at the prompt at {p['t']}")
        idle = s["data"]["idle_keys_s"] if keys else s["data"]["idle_any_s"]
        has_input = ms(idle) <= KEYS_PRE_MS
        f = focus_at(events, p["t"])
        hosted = f is not None and p["session_key"] in f["data"].get("hosted_sessions", [])
        ide = f is not None and f["data"]["app_class"] == "editor" and ide_session(starts, p["session_key"])
        has_focus = hosted or ide
        if has_input and has_focus:
            counts["with_input_and_focus"] += 1
            continue
        if not has_input:
            counts["no_input"] += 1
        if not has_focus:
            counts["no_focus"] += 1
        items.append({"turn_key": p["data"]["turn_key"], "t": p["t"], "input": has_input, "focus": has_focus,
                      "idle_s": idle})
    if not counts["prompts"]:
        return outcome("not_computable", counts, reason="no prompt in the submission that rules 1 to 5 leave open")
    ok = counts["with_input_and_focus"]
    if ok == counts["prompts"]:
        return outcome("pass", counts)
    check(ok == 0, "CC2 has no stated share; scenarios keep to all or none")
    return outcome("fail", counts, items)


def cc3(events, sub):
    """Edits against git: a commit is flagged unexplained_lines when more
    than half of its --shortstat lines, and more than 200, have no
    recorded edit since the commit before it in its project."""
    commits = [e for e in events if e["kind"] == "git.change" and e["data"]["op"] == "commit"
               and e.get("project_key") in sub["projects"]]
    counts = {"commits": 0, "flagged": 0}
    items = []
    for c in commits:
        if not in_window(sub, c["t"]):
            continue
        counts["commits"] += 1
        earlier = [x["t"] for x in commits if x["t"] < c["t"] and x["project_key"] == c["project_key"]]
        since = max(earlier) if earlier else None
        recorded = 0
        for e in events:
            if e["t"] > c["t"] or since is not None and e["t"] <= since or e.get("project_key") != c["project_key"]:
                continue
            d = e["data"]
            if e["kind"] == "agent.tool" and d["tool_class"] in ("edit", "write"):
                recorded += d["lines_added"] + d["lines_removed"]
            elif e["kind"] == "editor.activity" and d["edit_source"] != "agent":
                recorded += d["lines_changed"]
        changed = c["data"]["insertions"] + c["data"]["deletions"]
        unexplained = max(0, changed - recorded)
        flagged = 2 * unexplained > changed and unexplained > CC3_LINES
        item = {"sha": c["data"]["sha"], "t": c["t"], "changed_lines": changed, "recorded_lines": recorded,
                "unexplained_lines": unexplained, "flagged": flagged}
        items.append(item)
        counts["flagged"] += flagged
    if not counts["commits"]:
        return outcome("not_computable", counts, reason="no commit in the submission")
    return outcome("fail" if counts["flagged"] else "pass", counts, items)


def cc4(events, sub, commits):
    """Commit clocks: observed_at < server_first_seen_at < observed_at +
    15 min; on App-installed repos, push time >= observed_at - 60 s; the
    forge has the SHA with the same diffstat."""
    by_sha = {c["sha"]: c for c in commits}
    counts = {"commits": 0, "seen_out_of_order": 0, "pushed_before_observed": 0, "forge_mismatch": 0}
    items = []
    for e in events:
        if e["kind"] != "git.change" or e["data"]["op"] != "commit" or e.get("project_key") not in sub["projects"]:
            continue
        if not in_window(sub, e["t"]):
            continue
        counts["commits"] += 1
        c = by_sha[e["data"]["sha"]]
        check(c["observed_at"] == e["t"], "a commit record's observed_at is its git.change's t")
        f = c["forge"]
        seen = c["observed_at"] < c["server_first_seen_at"] < c["observed_at"] + CC4_SEEN_MS
        pushed = not f["app_installed"] or f["push_time"] >= c["observed_at"] - CC4_PUSH_MS
        d = e["data"]
        forge = f["sha_found"] and (f["files_changed"], f["insertions"], f["deletions"]) == (
            d["files_changed"], d["insertions"], d["deletions"])
        counts["seen_out_of_order"] += not seen
        counts["pushed_before_observed"] += not pushed
        counts["forge_mismatch"] += not forge
        if not (seen and pushed and forge):
            items.append({"sha": d["sha"], "observed_at": c["observed_at"],
                          "server_first_seen_at": c["server_first_seen_at"], "seen_in_order": seen,
                          "push_ok": pushed, "forge_matches": forge})
    if not counts["commits"]:
        return outcome("not_computable", counts, reason="no commit in the submission")
    bad = counts["seen_out_of_order"] or counts["pushed_before_observed"] or counts["forge_mismatch"]
    return outcome("fail" if bad else "pass", counts, items)


def cc7(led):
    """Chain integrity: the revealed records reproduce every n_b, R_b and
    H_b the server stored, with no gap in b or seq, and every body agrees
    with its header. A batch that fails holds the minutes of its records."""
    counts = {"batches": 0, "records": 0, "batch_gaps": 0, "seq_gaps": 0, "count_mismatches": 0,
              "root_mismatches": 0, "head_mismatches": 0, "body_mismatches": 0}
    items, held = [], set()
    for dev in led:
        rows = {w["b"]: w for w in dev["witnessed"]}
        last_b, last_s = None, None
        for bt in dev["batches"]:
            counts["batches"] += 1
            b = bt["b"]
            w = rows[b]
            bad = []
            if last_b is not None and b != last_b + 1:
                counts["batch_gaps"] += 1
                bad.append("batch_gap")
            last_b = b
            entries = []
            for r in bt["records"]:
                counts["records"] += 1
                if last_s is not None and r["seq"] != last_s + 1:
                    counts["seq_gaps"] += 1
                    bad.append(f"seq_gap_before_{r['seq']}")
                last_s = r["seq"]
                e = json.loads(r["body"])["e"]
                kind_ok = e["kind"] == KINDS[r["rtype"] - 1] and e["seq"] == r["seq"]
                proj_ok = (e.get("project_key") or "") == r["project_key"] and e["session_key"] == r["session_key"]
                if not (kind_ok and proj_ok):
                    counts["body_mismatches"] += 1
                    bad.append(f"body_{r['seq']}")
                hdr = (u64(r["seq"]) + bytes([r["rtype"]]) + project_digest(r["project_key"])
                       + session_digest(r["session_key"]))
                entries.append(entry(hdr, r["body"].encode()))
            r_b = root([leaf(x) for x in entries])
            if len(entries) != w["n"]:
                counts["count_mismatches"] += 1
                bad.append("count")
            if r_b.hex() != w["root"]:
                counts["root_mismatches"] += 1
                bad.append("root")
            if batch_head(bytes.fromhex(w["prev"]), r_b, len(entries), b).hex() != w["head"]:
                counts["head_mismatches"] += 1
                bad.append("head")
            if bad:
                minutes = sorted({minute_of(json.loads(r["body"])["e"]["t"]) for r in bt["records"]})
                held.update(minutes)
                items.append({"device_id": dev["device_id"], "b": b, "failed": bad,
                              "held_minutes": [utc(m) for m in minutes]})
    counts["held_minutes"] = len(held)
    if not counts["batches"]:
        return outcome("not_computable", counts, reason="no batch revealed")
    return outcome("fail" if items else "pass", counts, items)


def cc8(challenge, led):
    """Content-derived inputs (ledger-v1.md 10.8, PR #12): for each
    challenged turn, the revealed values reproduce the committed sketch,
    every shingle of the revealed text is shown when nothing was removed,
    no shingle shows two values across the device's reveals, and the class
    recomputed from the text is the committed one."""
    counts = {"challenged": 0, "sketch_ok": 0, "bad_entry": 0, "sketch_mismatch": 0, "shingle_mismatch": 0,
              "shingle_conflicts": 0, "class_mismatch": 0, "hidden_shingles": 0}
    if challenge is None or not challenge["drawn"]:
        return outcome("not_computable", counts, reason="nothing drawn: no human prompt opened a credited anchor")
    bodies = {}
    for dev in led:
        for bt in dev["batches"]:
            for r in bt["records"]:
                bodies[(dev["device_id"], r["seq"])] = json.loads(r["body"])
    items, seen = [], {}
    for d in challenge["drawn"]:
        counts["challenged"] += 1
        bd = bodies[(challenge["device_id"], d["seq"])]
        f = bd["f"]
        bad = []
        vs = [bytes.fromhex(x["v"]) for x in d["cc8"]]
        if any(len(v) != 32 for v in vs) or any(b <= a for a, b in zip(vs, vs[1:])):
            counts["bad_entry"] += 1
            bad.append("bad_entry")
        elif sketch(vs) != f["mh"]:
            counts["sketch_mismatch"] += 1
            bad.append("sketch")
        else:
            counts["sketch_ok"] += 1
        removed = any("type" in it for it in d["text"])
        shown = [bytes.fromhex(x["x"]).decode() for x in d["cc8"] if "x" in x]
        counts["hidden_shingles"] += sum(1 for x in d["cc8"] if "x" not in x)
        if not removed:
            text = "".join(bytes.fromhex(it["token"]).decode() for it in d["text"])
            xs, _ = shingles(plain_words(text))
            if sorted(shown) != xs or len(shown) != len(d["cc8"]):
                counts["shingle_mismatch"] += 1
                bad.append("shingles")
            ev = bd["e"]["data"]
            if prompt_class(text, ev["typed_len_est"], ev["answers_question"]) != f["pc"]:
                counts["class_mismatch"] += 1
                bad.append("class")
        for x in d["cc8"]:
            if "x" not in x:
                continue
            other = seen.setdefault(x["x"], (x["v"], d["seq"]))
            if other[0] != x["v"]:
                counts["shingle_conflicts"] += 1
                bad.append(f"conflict_with_{other[1]}")
        if bad:
            items.append({"seq": d["seq"], "turn_key": bd["e"]["data"]["turn_key"], "failed": sorted(set(bad))})
    failed = (counts["bad_entry"] or counts["sketch_mismatch"] or counts["shingle_mismatch"]
              or counts["shingle_conflicts"] or counts["class_mismatch"])
    return outcome("fail" if failed else "pass", counts, items)


def cc9(events, sub):
    """Session integrity: a session's records name one project, every turn
    an agent.* record names has a prompt in the session or is a stop-hook
    continuation, and the session's parent class doesn't change."""
    sessions = {}
    for e in events:
        if e["session_key"] == "none" or not in_window(sub, e["t"]):
            continue
        sessions.setdefault(e["session_key"], []).append(e)
    counts = {"sessions": 0, "multi_project_sessions": 0, "orphan_turns": 0, "parent_class_changes": 0}
    items = []
    for s, recs in sessions.items():
        everything = [e for e in events if e["session_key"] == s]
        projects = sorted({e.get("project_key") for e in everything})
        if not set(projects) & set(sub["projects"]):
            continue
        counts["sessions"] += 1
        bad = {}
        if len(projects) > 1:
            counts["multi_project_sessions"] += 1
            bad["projects"] = projects
        prompted = {e["data"]["turn_key"] for e in everything if e["kind"] == "prompt.submit"}
        continued = {e["data"]["turn_key"] for e in everything
                     if e["kind"] == "agent.stop" and e["data"]["stop_hook_active"]}
        turns = {e["data"]["turn_key"] for e in recs
                 if e["kind"] in ("agent.output", "agent.interrupt", "agent.queue", "agent.stop")}
        orphans = sorted(turns - prompted - continued)
        if orphans:
            counts["orphan_turns"] += len(orphans)
            bad["orphan_turns"] = orphans
        classes = {e["data"]["parent_class"] for e in everything if e["kind"] == "session.start"}
        if len(classes) > 1:
            counts["parent_class_changes"] += 1
            bad["parent_classes"] = sorted(classes)
        if bad:
            items.append({"session_key": s, **bad})
    if not counts["sessions"]:
        return outcome("not_computable", counts, reason="no harness session in the submission")
    return outcome("fail" if items else "pass", counts, items)


EDITOR_ANCHOR_ACTS = {"navigate", "open", "debug", "task"}


def cc10(events, sub):
    """Editor against focus: each editor record of an anchor's kind (a
    typed edit or a navigation, SM 2.3) arrives while an editor window of
    its project has focus."""
    counts = {"editor_acts": 0, "with_focus": 0, "without_focus": 0}
    items = []
    for e in events:
        if e["kind"] != "editor.activity" or e.get("project_key") not in sub["projects"] or not in_window(sub, e["t"]):
            continue
        d = e["data"]
        typed = d["act"] in ("edit", "save") and d["edit_source"] in ("human", "tab", "unknown")
        if not typed and d["act"] not in EDITOR_ANCHOR_ACTS:
            continue
        counts["editor_acts"] += 1
        f = focus_at(events, e["t"])
        ok = f is not None and f["data"]["app_class"] == "editor" and f["data"].get("focus_project") == e["project_key"]
        if ok:
            counts["with_focus"] += 1
        else:
            counts["without_focus"] += 1
            items.append({"t": e["t"], "editor": d["editor"], "project_key": e["project_key"],
                          "focus": f["data"]["app_class"] if f else None})
    if not counts["editor_acts"]:
        return outcome("not_computable", counts, reason="no editor act in the submission")
    return outcome("fail" if items else "pass", counts, items)


def prompt_lead_ms(p, pc):
    """SM 2.3's lead for a prompt anchor of class pc, in ms."""
    typed = p["data"]["typed_len_est"]
    typing = min(Fraction(COMPOSE_MAX_S), Fraction(typed) / COMPOSE_CPS)
    if pc in ("substantive", "error_paste"):
        lead = THINK_LEAD_S + typing
    elif pc == "answer":
        lead = ANSWER_LEAD_S + typing
    else:
        return None  # a go-ahead's lead has no typing part
    return int(lead * 1000)


def cc11(events, sub, keys, typing, classes):
    """Typing, module on: in each human prompt's lead window, the key-downs
    the typing module counted, injected ones aside, against the prompt's
    typed length, and the injected share. DESIGN gives neither threshold;
    the scenarios sit where any sensible one agrees (README, errata)."""
    counts = {"prompts": 0, "typed_chars": 0, "key_downs": 0, "injected": 0, "too_few_keys": 0,
              "injected_prompts": 0}
    if typing is None:
        return outcome("not_computable", counts, reason="the typing module is off on this device")
    slots = {s["start"]: s for s in typing["slots"]}
    starts = sessions_of(events)
    items = []
    for p in prompts_in(events, sub):
        if automated(p, starts):
            continue
        s = sample_at(events, p["t"])
        idle = s["data"]["idle_keys_s"] if keys else s["data"]["idle_any_s"]
        f = focus_at(events, p["t"])
        human = ms(idle) <= KEYS_PRE_MS and f is not None and p["session_key"] in f["data"].get("hosted_sessions", [])
        typed = p["data"]["typed_len_est"]
        lead = prompt_lead_ms(p, classes[p["data"]["turn_key"]])
        if not human or not typed or lead is None:
            continue
        counts["prompts"] += 1
        a, b = p["t"] - lead, p["t"]
        first = a - a % SLOT_MS
        k = inj = 0
        for st in range(first, b, SLOT_MS):
            if st in slots:
                k += slots[st]["keys"]
                inj += slots[st]["injected"]
        hardware = k - inj
        counts["typed_chars"] += typed
        counts["key_downs"] += k
        counts["injected"] += inj
        few = hardware < typed
        injected = inj > 0
        counts["too_few_keys"] += few
        counts["injected_prompts"] += injected
        if few or injected:
            items.append({"turn_key": p["data"]["turn_key"], "t": p["t"], "typed_len_est": typed, "key_downs": k,
                          "injected": inj, "lead_from": a})
    if not counts["prompts"]:
        return outcome("not_computable", counts, reason="no human prompt with typed characters")
    return outcome("fail" if items else "pass", counts, items)


# Writing.


def write(out, name, files):
    """Writes the scenario's files: name -> a JSON document, or a list of
    events for an .jsonl file."""
    d = os.path.join(out, name)
    os.makedirs(d, exist_ok=True)
    for fname, doc in files.items():
        with open(os.path.join(d, fname), "w", encoding="utf-8", newline="\n") as f:
            if fname.endswith(".jsonl"):
                for e in doc:
                    f.write(tl.encode(e) + "\n")
            else:
                f.write(tl.pretty(doc) + "\n")


def agree(name, stated, computed):
    """Checks the hand-stated result and counts of every check against the
    computed ones. A count the scenario doesn't state isn't compared."""
    for cc in CHECKS:
        want = stated.get(cc)
        got = computed.get(cc)
        check(want is not None and got is not None, f"{name}: {cc} has a stated and a computed result")
        result, counts = want
        check(got["result"] == result, f"{name}: {cc} is {got['result']}, stated {result}: {got}")
        for k, v in counts.items():
            check(got["counts"].get(k) == v, f"{name}: {cc} {k} is {got['counts'].get(k)}, stated {v}")
