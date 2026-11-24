#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Build the T3 Code fixture's store.sql and hooks.jsonl from a capture.

    python3 -I mkfixture.py --store COPY --hooks HOOKS --home HOME --out DIR [--to 2030-01-07]

COPY is a snapshot of the scratch T3 Code's statev2.sqlite (README.md, step
5), HOOKS the capture's hooks.jsonl, HOME the scratch home, and DIR the
fixture's web/ folder. Only the tables internal/adapters/t3code/testdata/
schema.sql creates are kept, row by row with T3 Code's own rowids. Every UUID
becomes 00000000-0000-4000-8000-NNNNNNNNNNNN, numbered in the order met and
shared by both files, so a hook's session_id still names its run attempt's
nativeThreadId; every msg_ id becomes msg_fixtureNNNN; every other hex run of
8 or more digits becomes a numbered one of the same length; HOME becomes
/home/capture. Assistant, notification and thread title text becomes a
placeholder that gives its length, and the owner's probe text stays. Every
time moves by the same whole number of days, so that the capture's first day
becomes --to. The map from old to new values goes next to COPY, never into
DIR.
"""

import argparse
import datetime
import json
import os
import re
import sqlite3

KIT = os.path.dirname(os.path.abspath(__file__))
SCHEMA = os.path.join(KIT, "..", "..", "..", "internal", "adapters", "t3code", "testdata", "schema.sql")

U = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
MSG = re.compile(r"msg_[a-z0-9]{20,}")
HEX = re.compile(r"(?<![0-9a-f])[0-9a-f]{8,}(?![0-9a-f])")
ISO = re.compile(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z")
SQLDT = re.compile(r"^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$")


class Scrub:
    def __init__(self, home, days):
        self.home, self.days, self.ms = home, days, days * 86400 * 1000
        self.uuid, self.msg, self.hex = {}, {}, {}

    def fake_uuid(self, u):
        return self.uuid.setdefault(u, "00000000-0000-4000-8000-%012d" % (len(self.uuid) + 1))

    def fake_msg(self, m):
        return self.msg.setdefault(m, "msg_fixture%04d" % (len(self.msg) + 1))

    def fake_hex(self, h):
        if re.fullmatch(r"0+[0-9]*", h):
            return h
        return self.hex.setdefault(h, ("%0" + str(len(h)) + "x") % (len(self.hex) + 1))

    def shift_iso(self, s):
        frac = s[19:-1]
        dt = datetime.datetime.strptime(s[:19], "%Y-%m-%dT%H:%M:%S") + datetime.timedelta(days=self.days)
        return dt.strftime("%Y-%m-%dT%H:%M:%S") + frac + "Z"

    def text(self, s):
        s = s.replace(self.home, "/home/capture")
        s = U.sub(lambda m: self.fake_uuid(m.group(0)), s)
        s = MSG.sub(lambda m: self.fake_msg(m.group(0)), s)
        s = HEX.sub(lambda m: self.fake_hex(m.group(0)), s)
        s = ISO.sub(lambda m: self.shift_iso(m.group(0)), s)
        if SQLDT.match(s):
            s = (datetime.datetime.strptime(s, "%Y-%m-%d %H:%M:%S") + datetime.timedelta(days=self.days)).strftime(
                "%Y-%m-%d %H:%M:%S")
        return s

    def value(self, v):
        if isinstance(v, str):
            return self.text(v)
        if isinstance(v, bool):
            return v
        if isinstance(v, int) and 1_700_000_000_000 <= v <= 1_900_000_000_000:
            return v + self.ms
        if isinstance(v, dict):
            return {k: self.value(x) for k, x in v.items()}
        if isinstance(v, list):
            return [self.value(x) for x in v]
        return v


def placeholder(what, s):
    return "[%s, removed: %d characters]" % (what, len(s))


def dump(v):
    return json.dumps(v, ensure_ascii=False, separators=(",", ":"))


def sql(v):
    if v is None:
        return "NULL"
    if isinstance(v, (int, float)):
        return str(v)
    return "'" + str(v).replace("'", "''") + "'"


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--store", required=True)
    ap.add_argument("--hooks", required=True)
    ap.add_argument("--home", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--to", default="2030-01-07", help="the day the capture's first day becomes")
    args = ap.parse_args()

    db = sqlite3.connect("file:" + args.store + "?mode=ro", uri=True)
    ref = sqlite3.connect(":memory:")
    ref.executescript(open(SCHEMA).read())
    tables = [r[0] for r in ref.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY rowid")]
    hooks = [json.loads(line) for line in open(args.hooks)]
    first = datetime.datetime.fromtimestamp(hooks[0]["t_ns"] / 1e9, datetime.timezone.utc).date()
    days = (datetime.date.fromisoformat(args.to) - first).days
    sc = Scrub(os.path.abspath(args.home), days)

    lines, counts = [], {}
    for t in tables:
        cols = [r[1] for r in ref.execute(f"PRAGMA table_info({t})")]
        rows = db.execute(f"SELECT rowid, {', '.join(cols)} FROM {t} ORDER BY rowid").fetchall()
        counts[t] = len(rows)
        for row in rows:
            rec = dict(zip(["rowid"] + cols, row))
            if t == "orchestration_v2_projection_messages":
                p = json.loads(rec["payload_json"])
                if rec["role"] != "user" or p.get("createdBy") != "user":
                    what = "assistant text" if rec["role"] == "assistant" else "T3 notification text"
                    if isinstance(p.get("text"), str):
                        p["text"] = placeholder(what, p["text"])
                if isinstance(p.get("notification"), dict) and isinstance(p["notification"].get("summary"), str):
                    p["notification"]["summary"] = placeholder("T3 notification summary", p["notification"]["summary"])
                rec["payload_json"] = dump(p)
                if rec["search_text"] is not None:
                    rec["search_text"] = placeholder("search text", rec["search_text"])
            if t == "orchestration_v2_projection_threads":
                p = json.loads(rec["payload_json"])
                if isinstance(p.get("title"), str):
                    p["title"] = placeholder("thread title", p["title"])
                rec["title"] = placeholder("thread title", rec["title"])
                rec["payload_json"] = dump(p)
            out = []
            for c in ["rowid"] + cols:
                v = rec[c]
                if c.endswith("_json") and isinstance(v, str):
                    v = dump(sc.value(json.loads(v)))
                elif isinstance(v, str):
                    v = sc.text(v)
                elif isinstance(v, int) and c != "rowid":
                    v = sc.value(v)
                out.append(sql(v))
            lines.append(f"INSERT INTO {t} ({', '.join(['rowid'] + cols)}) VALUES ({', '.join(out)});")
    header = [
        "-- T3 Code's statev2.sqlite after a scratch capture (see PROVENANCE.md and tools/capture/t3code),",
        "-- reduced to the rows of the tables internal/adapters/t3code/testdata/schema.sql",
        "-- creates, which are the tables the T3 Code reader reads. Load schema.sql first,",
        "-- then this file. Rowids are T3's own. Ids, paths and times are replaced as",
        "-- PROVENANCE.md says; assistant, notification and title text are placeholders.",
        "-- Row counts: " + ", ".join("%s %d" % (t, counts[t]) for t in tables),
        "",
    ]
    os.makedirs(args.out, exist_ok=True)
    with open(os.path.join(args.out, "store.sql"), "w") as f:
        f.write("\n".join(header + lines) + "\n")
    with open(os.path.join(args.out, "hooks.jsonl"), "w") as f:
        for h in hooks:
            if h.get("hook_event_name") not in (None, h["event"]):
                raise SystemExit("mkfixture.py: a hook line's event and hook_event_name differ")
            r = {"event": h["event"],
                 "session_id": sc.fake_uuid(h["session_id"]) if h.get("session_id") else None,
                 "prompt_id": sc.fake_uuid(h["prompt_id"]) if h.get("prompt_id") else None,
                 "t_ns": h["t_ns"] + sc.ms * 1_000_000}
            if h["event"] == "UserPromptSubmit":
                r["guest_prefix"] = h.get("guest_prefix")
            f.write(json.dumps(r) + "\n")
    scrub_map = os.path.join(os.path.dirname(os.path.abspath(args.store)), "scrub-map.json")
    with open(scrub_map, "w") as f:
        json.dump({"uuid": sc.uuid, "msg": sc.msg, "hex": sc.hex, "days": days}, f)
    print("days", days, "uuids", len(sc.uuid), "msgs", len(sc.msg), "hex", len(sc.hex), counts)


if __name__ == "__main__":
    main()
