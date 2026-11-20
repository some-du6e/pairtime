#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Copy the harness's own session files for the sessions a capture saw.

    python3 collect.py RUN_DIR [--history FILE]... [--extra PATH]... [--yes]

Run it after the harness has exited. It reads every hook payload under
RUN_DIR/hooks/ and copies exactly the files those payloads name, into
RUN_DIR/transcripts/:

  - each transcript_path, agent_transcript_path or transcriptPath;
  - for a Claude Code transcript <dir>/<session>.jsonl, the folder
    <dir>/<session>/ beside it, which holds that session's subagents.

It never searches the harness's folders, so your other sessions stay where
they are. A history file (--history, such as ~/.claude/history.jsonl or
~/.codex/history.jsonl) is copied only line by line: a line is kept when its
project or cwd is a folder the hooks saw, or its session id is one the hooks
saw, and every other line is left out. --extra copies a file or folder you
name, for harnesses whose hooks don't say where their logs are, after showing
you the list and asking.

When prepare.py gave the harness a settings folder of its own (capture.json
in RUN_DIR says so), every file collected must come from inside it. A
session file from anywhere else means that session ran with your own
settings, instructions and plugins, so collect.py copies nothing and says
so.

transcripts/INDEX.txt lists every copied file with where it came from, its
size and its modification time, since some harnesses' logs carry no
timestamps of their own. Nothing leaves this machine. Standard library only,
Python 3.8+.
"""

import argparse
import json
import os
import shutil
import sys
import time

TRANSCRIPT_KEYS = ("transcript_path", "agent_transcript_path", "transcriptPath", "agentTranscriptPath")
SESSION_KEYS = ("session_id", "sessionId", "conversation_id", "conversationId")
FOLDER_KEYS = ("cwd", "project", "workspace_roots", "workspaceRoots")


def walk(value, key=None):
    """Yield (key, string) for every string in a JSON value, with the key of
    the object member it sits in (list items inherit their list's key)."""
    if isinstance(value, dict):
        for k, v in value.items():
            yield from walk(v, k)
    elif isinstance(value, list):
        for v in value:
            yield from walk(v, key)
    elif isinstance(value, str):
        yield key, value


def payloads(run_dir):
    hooks = os.path.join(run_dir, "hooks")
    if not os.path.isdir(hooks):
        return
    for name in sorted(os.listdir(hooks)):
        path = os.path.join(hooks, name, "stdin")
        try:
            with open(path, "rb") as f:
                doc = json.loads(f.read().decode("utf-8"))
        except (OSError, UnicodeDecodeError, ValueError):
            continue
        yield name, doc


def norm(path):
    return os.path.normcase(os.path.normpath(path))


def inside(path, root):
    path, root = os.path.realpath(path), os.path.realpath(root)
    return path == root or path.startswith(root.rstrip(os.sep) + os.sep)


def load_capture(run_dir):
    """capture.json, which prepare.py writes into the run, or None."""
    try:
        with open(os.path.join(run_dir, "capture.json"), "rb") as f:
            doc = json.loads(f.read().decode("utf-8"))
    except (OSError, UnicodeDecodeError, ValueError):
        return None
    return doc if isinstance(doc, dict) else None


def isolation_problems(capture, transcripts, histories, extras):
    """Why the files to collect can't be from the captured sessions, when the
    run's harness had a settings folder of its own: one line per file that
    sits outside it."""
    folders = [f for f in (capture.get("settings_folder") or {}).values() if isinstance(f, str) and f]
    if not capture.get("strict") or not folders:
        return []
    where = " or ".join(folders)
    problems = []
    for t in transcripts:
        if not any(inside(t, f) for f in folders):
            problems.append("a hook names %s, outside %s, the settings folder prepare.py gave the harness. That session "
                            "ran with your own settings, so your instructions, hooks or plugins may be in it." % (t, where))
    for h in histories:
        if not any(inside(h, f) for f in folders):
            problems.append("--history %s is outside %s, so it isn't this run's history. prepare.py printed the right "
                            "path." % (h, where))
    for e in extras:
        if not any(inside(e, f) for f in folders):
            problems.append("--extra %s is outside %s, so it isn't from the captured sessions." % (e, where))
    return problems


def seen(run_dir):
    """What the hook payloads name: transcript files, session ids, folders."""
    transcripts, sessions, folders = [], set(), set()
    for _, doc in payloads(run_dir):
        for key, s in walk(doc):
            if key in TRANSCRIPT_KEYS and s and s not in transcripts:
                transcripts.append(s)
            elif key in SESSION_KEYS and s:
                sessions.add(s)
            elif key in FOLDER_KEYS and s:
                folders.add(norm(s))
    return transcripts, sessions, folders


def destination(run_dir, source):
    """Where a source file goes under RUN_DIR/transcripts: its path below the
    home folder under home/, anything else under root/."""
    home = os.path.expanduser("~")
    absolute = os.path.abspath(source)
    try:
        rel = os.path.relpath(absolute, home)
    except ValueError:
        rel = os.pardir
    if rel != os.pardir and not rel.startswith(os.pardir + os.sep):
        parts = ["home"] + rel.split(os.sep)
    else:
        drive, rest = os.path.splitdrive(absolute)
        parts = ["root"] + [p for p in (drive.rstrip(":\\/"),) if p] + [p for p in rest.split(os.sep) if p]
    return os.path.join(run_dir, "transcripts", *parts)


def files_under(path):
    if os.path.isfile(path):
        return [path]
    out = []
    for root, dirs, names in os.walk(path):
        dirs.sort()
        for n in sorted(names):
            out.append(os.path.join(root, n))
    return out


def plan(run_dir, transcripts):
    """The files to copy for the transcripts the hooks named, and the named
    transcripts that don't exist (any more)."""
    copies, missing = [], []
    for t in transcripts:
        if not os.path.isfile(t):
            missing.append(t)
            continue
        copies.append(t)
        stem, ext = os.path.splitext(t)
        if ext == ".jsonl" and os.path.isdir(stem):
            copies.extend(files_under(stem))
    unique = []
    for c in copies:
        if c not in unique:
            unique.append(c)
    return unique, missing


def filter_history(path, sessions, folders):
    kept, total = [], 0
    with open(path, "rb") as f:
        for raw in f:
            if not raw.strip():
                continue
            total += 1
            try:
                doc = json.loads(raw.decode("utf-8"))
            except (UnicodeDecodeError, ValueError):
                continue
            for key, s in walk(doc):
                if (key in SESSION_KEYS and s in sessions) or (key in FOLDER_KEYS and norm(s) in folders):
                    kept.append(raw if raw.endswith(b"\n") else raw + b"\n")
                    break
    return kept, total


def utc(ts):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(ts))


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("run_dir", help="the capture run directory")
    ap.add_argument("--history", action="append", default=[], metavar="FILE",
                    help="a history file to copy, keeping only lines from the captured sessions or folders")
    ap.add_argument("--extra", action="append", default=[], metavar="PATH",
                    help="a file or folder to copy whole, after confirmation")
    ap.add_argument("--yes", action="store_true", help="copy --extra paths without asking")
    args = ap.parse_args(argv)
    run_dir = os.path.abspath(args.run_dir)
    capture = load_capture(run_dir)
    if capture is None:
        ap.error("%s has no capture.json, so prepare.py didn't make it. Set the run up with prepare.py" % run_dir)
    if not os.path.isdir(os.path.join(run_dir, "hooks")):
        ap.error("%s has no hooks/ folder: run the capture first" % run_dir)

    transcripts, sessions, folders = seen(run_dir)
    for p in args.extra:
        if not os.path.exists(p):
            ap.error("--extra %s doesn't exist" % p)
    problems = isolation_problems(capture, transcripts, [os.path.abspath(h) for h in args.history],
                                  [os.path.abspath(e) for e in args.extra])
    if problems:
        ap.error("nothing was copied.\n\n" + "\n\n".join(problems) + "\n\nStart over with a new ROOT, and set the "
                 "variables prepare.py prints in the terminal you start the harness from (SCRIPTS.md, Before any capture).")
    copies, missing = plan(run_dir, transcripts)

    extras = []
    for p in args.extra:
        extras.extend(files_under(os.path.abspath(p)))
    if extras and not args.yes:
        print("--extra would copy these %d files in full:" % len(extras))
        for e in extras:
            print("  " + e)
        if input("Copy them? [y/N] ").strip().lower() not in ("y", "yes"):
            print("Left the --extra files out.")
            extras = []
    copies.extend(e for e in extras if e not in copies)

    index = []
    for src in copies:
        dst = destination(run_dir, src)
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        shutil.copyfile(src, dst)
        st = os.stat(src)
        index.append((os.path.relpath(dst, run_dir), src, st.st_size, utc(st.st_mtime)))

    for h in args.history:
        if not os.path.isfile(h):
            print("history file %s doesn't exist, skipped" % h)
            continue
        kept, total = filter_history(h, sessions, folders)
        dst = destination(run_dir, h)
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        with open(dst, "wb") as f:
            f.writelines(kept)
        st = os.stat(h)
        index.append((os.path.relpath(dst, run_dir), h + " (%d of %d lines)" % (len(kept), total),
                      sum(len(k) for k in kept), utc(st.st_mtime)))
        print("history %s: kept %d of %d lines, the ones from the captured sessions" % (h, len(kept), total))

    if index:
        with open(os.path.join(run_dir, "transcripts", "INDEX.txt"), "w", encoding="utf-8", newline="\n") as f:
            f.write("# copied file\tsource\tbytes\tsource mtime (UTC)\n")
            for row in index:
                f.write("%s\t%s\t%d\t%s\n" % (row[0].replace(os.sep, "/"), row[1], row[2], row[3]))

    print("%d hook calls named %d transcripts and %d sessions." % (
        sum(1 for _ in payloads(run_dir)), len(transcripts), len(sessions)))
    for row in index:
        print("  copied %-70s %9d bytes" % (row[0].replace(os.sep, "/"), row[2]))
    for m in missing:
        print("  missing %s (named by a hook, not on disk)" % m)
    if not index:
        print("Nothing copied.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
