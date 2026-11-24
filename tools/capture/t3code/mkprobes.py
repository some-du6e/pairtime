#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Write the T3 Code fixture's probes.json: what each hook prompt was.

    python3 -I mkprobes.py --fixture DIR

DIR is the fixture's web/ folder, with mkfixture.py's store.sql and
hooks.jsonl. PLAN lists the capture's prompts in the order Claude Code's
UserPromptSubmit hook saw them, each with the store row of its message and
its kind (README.md, The probe plan). For each one, probes.json records the
hook's ids, the message's row, who created it and how, its run, whether that
run is the message's own, whether the run's attempt names the hook's session,
and whether the prompt opened with T3 Code's guest wrapper. A new capture
edits PLAN to match what it sent.
"""

import argparse
import json
import os
import sqlite3

KIT = os.path.dirname(os.path.abspath(__file__))
SCHEMA = os.path.join(KIT, "..", "..", "..", "internal", "adapters", "t3code", "testdata", "schema.sql")

# The 2026-11-23 capture: (store rowid of the message, kind), one per hook prompt.
PLAN = [
    (1, "message"), (3, "message"), (5, "message_after_turn_end"),
    (7, "t3_notification_continuation"), (9, "message"), (10, "steer"),
    (12, "message"), (13, "queued"), (16, "message"),
    (17, "steer_guest_wrapper_stand_in"), (19, "message"),
    (20, "queued_then_promoted_to_steer_guest_wrapper_stand_in"), (22, "message"),
    (24, "message_after_turn_end"), (26, "message"), (27, "steer_during_final_reply"),
    (29, "message"), (30, "steer"), (32, "message"),
    (34, "steer_after_turn_end_ran_as_own_run"), (36, "message"),
    (38, "steer_after_turn_end_ran_as_own_run"), (40, "message"), (42, "steer_follow_up"),
    (44, "message"), (46, "message"), (48, "steer_during_stop_hook_guest_wrapper_stand_in"),
    (51, "message_new_worktree_thread"),
]

MESSAGE_SQL = ("SELECT message_id, run_id, json_extract(payload_json,'$.createdBy'),"
               " json_extract(payload_json,'$.creationSource')"
               " FROM orchestration_v2_projection_messages WHERE rowid = ?")
RUN_SQL = ("SELECT ordinal, json_extract(payload_json,'$.userMessageId') = ?,"
           " (SELECT json_extract(a.payload_json,'$.nativeThreadId') FROM orchestration_v2_projection_run_attempts a"
           "  WHERE a.run_id = r.run_id)"
           " FROM orchestration_v2_projection_runs r WHERE run_id = ?")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--fixture", required=True)
    args = ap.parse_args()
    db = sqlite3.connect(":memory:")
    db.executescript(open(SCHEMA).read())
    db.executescript(open(os.path.join(args.fixture, "store.sql")).read())
    hooks = [json.loads(line) for line in open(os.path.join(args.fixture, "hooks.jsonl"))]
    prompts = [h for h in hooks if h["event"] == "UserPromptSubmit"]
    if len(prompts) != len(PLAN):
        raise SystemExit(f"mkprobes.py: {len(prompts)} hook prompts, but PLAN has {len(PLAN)}")
    out = []
    for i, (h, (rowid, kind)) in enumerate(zip(prompts, PLAN)):
        m = db.execute(MESSAGE_SQL, (rowid,)).fetchone()
        run = db.execute(RUN_SQL, (m[0], m[1])).fetchone()
        out.append({"hook_prompt": i + 1, "prompt_id": h["prompt_id"], "session_id": h["session_id"], "kind": kind,
                    "message_rowid": rowid, "message_id": m[0], "created_by": m[2], "creation_source": m[3],
                    "run_id": m[1], "run_ordinal": run[0], "run_is_messages_own": bool(run[1]),
                    "run_attempt_native_thread_id_is_hook_session": run[2] == h["session_id"],
                    "guest_prefix": h["guest_prefix"]})
    with open(os.path.join(args.fixture, "probes.json"), "w") as f:
        json.dump(out, f, indent=1)
    print(len(out), "probes")


if __name__ == "__main__":
    main()
