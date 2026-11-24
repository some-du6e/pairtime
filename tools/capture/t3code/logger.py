#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""The capture's Claude Code hook: one JSON line per call, with no payload text.

    python3 -I logger.py EVENT < payload

prepare.py registers this for every event in tools/capture/prepare.py's
CLAUDE_CODE_HOOKS. Each call appends to log/hooks.jsonl, in the folder next to
this file that prepare.py gives the throwaway account, since this file's own
folder is root's: the event, session_id, prompt_id and the arrival time, and
for UserPromptSubmit whether the prompt opens with T3 Code's guest wrapper. It
never writes the prompt or any other payload field. It also appends to
log/snap.jsonl what the scratch T3 Code store held at that moment about the
session's runs (ids, times and booleans only), and the entrypoint and Agent
SDK version variables (short, version-shaped values only). See README.md.
"""

import json
import os
import re
import sqlite3
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
LOG = os.path.join(HERE, "log")
HOME = os.path.dirname(HERE)
STORE = os.path.join(HOME, "t3home", "userdata", "statev2.sqlite")
CLAUDE_HOME = os.path.join(HOME, "claude-home")


def guest(text):
    """Whether text opens with T3 Code's guest wrapper, past white space and
    an Ultrathink: line, the way the reader's device test reads it."""
    if not isinstance(text, str):
        return None
    t = text.strip()
    if t[:11].lower() == "ultrathink:":
        line, nl, after = t[11:].partition("\n")
        if line.strip() == "":
            t = after.strip() if nl else ""
    return t.startswith("Message from guest ")


def short(v):
    if v is None:
        return None
    return v if isinstance(v, str) and re.fullmatch(r"[A-Za-z0-9._+-]{1,40}", v) else "other"


RUNS_SQL = (
    "SELECT r.rowid, r.run_id, r.status, json_extract(r.payload_json,'$.startedAt'), r.requested_at, r.completed_at,"
    " json_extract(r.payload_json,'$.userMessageId'),"
    " (SELECT count(*) FROM orchestration_v2_projection_run_attempts a WHERE a.run_id = r.run_id),"
    " (SELECT count(*) FROM orchestration_v2_projection_run_attempts a WHERE a.run_id = r.run_id"
    "   AND json_extract(a.payload_json,'$.nativeThreadId') = ?),"
    " (SELECT count(*) FROM orchestration_v2_projection_provider_threads pt WHERE pt.provider_thread_id = r.provider_thread_id"
    "   AND json_extract(pt.payload_json,'$.nativeThreadRef.nativeId') = ?)"
    " FROM orchestration_v2_projection_runs r ORDER BY r.rowid DESC LIMIT 4"
)


def main() -> None:
    t_ns = time.time_ns()
    mono_ns = time.monotonic_ns()
    ev = sys.argv[1] if len(sys.argv) > 1 else "?"
    try:
        p = json.load(sys.stdin)
    except Exception:
        p = {}
    rec = {"event": ev, "hook_event_name": p.get("hook_event_name"),
           "session_id": p.get("session_id"), "prompt_id": p.get("prompt_id"), "t_ns": t_ns}
    if ev == "UserPromptSubmit":
        rec["guest_prefix"] = guest(p.get("prompt"))
    p = None
    snap = {"t_ns": t_ns, "event": ev, "session_id": rec["session_id"],
            "entrypoint": short(os.environ.get("CLAUDE_CODE_ENTRYPOINT")),
            "sdk_version": short(os.environ.get("CLAUDE_AGENT_SDK_VERSION")),
            "config_dir_is_scratch": os.environ.get("CLAUDE_CONFIG_DIR") == CLAUDE_HOME}
    try:
        db = sqlite3.connect("file:" + STORE + "?mode=ro", uri=True, timeout=2)
        sid = rec["session_id"]
        rows = db.execute(RUNS_SQL, (sid, sid)).fetchall()
        snap["runs"] = [{"rowid": r[0], "run_id": r[1], "status": r[2], "startedAt": r[3], "requested_at": r[4],
                         "completed_at": r[5], "userMessageId": r[6], "attempts": r[7],
                         "attempt_native_is_session": r[8], "provider_thread_native_is_session": r[9]}
                        for r in rows]
        db.close()
    except Exception as e:
        snap["store_error"] = type(e).__name__
    snap["snap_ms_after_arrival"] = (time.monotonic_ns() - mono_ns) / 1e6
    with open(os.path.join(LOG, "hooks.jsonl"), "a") as f:
        f.write(json.dumps(rec) + "\n")
    with open(os.path.join(LOG, "snap.jsonl"), "a") as f:
        f.write(json.dumps(snap) + "\n")


if __name__ == "__main__":
    main()
    sys.exit(0)
