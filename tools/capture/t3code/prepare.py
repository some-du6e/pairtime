#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Lay out the T3 Code capture's scratch home, with real copies of the runtimes.

    python3 -I prepare.py --home HOME --uid UID --t3-runtime DIR --claude BIN

Run it as root, after making the throwaway account (README.md, step 1). It
writes, under HOME:

    bin/t3rt/            a copy of T3 Code's runtime folder DIR
    bin/t3               a link to bin/t3rt/t3
    bin/claude           a copy of the Claude Code binary BIN
    t3home/userdata/     the scratch T3 Code home's data folder
    claude-home/         the scratch CLAUDE_CONFIG_DIR, the account's
    claude-home/settings.json
                         root's, so the account can't edit it in place: it runs
                         logger.py for every event of tools/capture/prepare.py's
                         CLAUDE_CODE_HOOKS
    cap/                 root's, mode 0755, so the account can't rename what is
                         in it
    cap/logger.py        this kit's hook logger, root's too, so the account can't
                         change it in place
    cap/log/             the account's: logger.py writes hooks.jsonl and
                         snap.jsonl there
    repo/                a two-file git repository, the project T3 Code opens

HOME and claude-home/ stay the account's, so it can still move cap/ or
settings.json aside and put its own in their place. mkfixture.py therefore
takes from hooks.jsonl only values logger.py writes.

Every copy is checked against its source: a file that shares an inode with
the file it was copied from is a hard link, and the script stops.
"""

import argparse
import importlib.util
import json
import os
import shutil
import subprocess
import sys

KIT = os.path.dirname(os.path.abspath(__file__))


def claude_code_hooks():
    spec = importlib.util.spec_from_file_location("capture_prepare", os.path.join(os.path.dirname(KIT), "prepare.py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return [(event, matcher) for event, matcher, _timeout, _script in mod.CLAUDE_CODE_HOOKS]


def same_file(a, b):
    sa, sb = os.lstat(a), os.lstat(b)
    return (sa.st_dev, sa.st_ino) == (sb.st_dev, sb.st_ino)


def check_copies(src, dst):
    """Stop if any regular file under dst is the same inode as its source."""
    for root, _dirs, files in os.walk(dst):
        for f in files:
            d = os.path.join(root, f)
            s = os.path.join(src, os.path.relpath(d, dst))
            if os.path.isfile(d) and not os.path.islink(d) and os.path.exists(s) and same_file(s, d):
                sys.exit(f"prepare.py: {d} is a hard link to its source; copy it instead")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--home", required=True)
    ap.add_argument("--uid", type=int, required=True)
    ap.add_argument("--t3-runtime", required=True)
    ap.add_argument("--claude", required=True)
    args = ap.parse_args()
    home = os.path.abspath(args.home)
    if os.geteuid() != 0:
        sys.exit("prepare.py: run it as root")
    for d in ("bin", "t3home/userdata", "claude-home", "cap/log", "repo"):
        os.makedirs(os.path.join(home, d), exist_ok=True)

    rt = os.path.join(home, "bin", "t3rt")
    if os.path.exists(rt):
        sys.exit(f"prepare.py: {rt} exists; start from an empty home")
    shutil.copytree(args.t3_runtime, rt, symlinks=True, copy_function=shutil.copy2)
    check_copies(args.t3_runtime, rt)
    os.symlink(os.path.join("t3rt", "t3"), os.path.join(home, "bin", "t3"))
    claude = os.path.join(home, "bin", "claude")
    shutil.copy2(args.claude, claude)
    if same_file(args.claude, claude):
        sys.exit(f"prepare.py: {claude} is a hard link to its source; copy it instead")

    logger = os.path.join(home, "cap", "logger.py")
    shutil.copy2(os.path.join(KIT, "logger.py"), logger)
    hooks = {}
    for event, matcher in claude_code_hooks():
        entry = {"hooks": [{"type": "command", "command": f"/usr/bin/python3 -I {logger} {event}"}]}
        if matcher:
            entry["matcher"] = matcher
        hooks[event] = [entry]
    with open(os.path.join(home, "claude-home", "settings.json"), "w") as f:
        json.dump({"hooks": hooks}, f, indent=2)

    repo = os.path.join(home, "repo")
    for name, text in (("README.md", "A scratch project for the T3 Code capture.\n"),
                       ("main.py", "print('hello')\n")):
        with open(os.path.join(repo, name), "w") as f:
            f.write(text)
    git = ["git", "-C", repo, "-c", "user.name=Capture User", "-c", "user.email=capture@example.invalid"]
    subprocess.run(["git", "init", "-q", repo], check=True)
    subprocess.run(git + ["add", "."], check=True)
    subprocess.run(git + ["commit", "-q", "-m", "scratch project"], check=True)

    uid = str(args.uid)
    for d in ("cap/log", "claude-home", "t3home", "repo"):
        subprocess.run(["chown", "-R", f"{uid}:{uid}", os.path.join(home, d)], check=True)
    # The logger, its folder and the hook commands stay root's, after the
    # chown above gave settings.json to the account with its folder.
    for path, mode in (("cap", 0o755), ("cap/logger.py", 0o755), ("claude-home/settings.json", 0o644)):
        os.chown(os.path.join(home, path), 0, 0)
        os.chmod(os.path.join(home, path), mode)
    print("prepared", home)


if __name__ == "__main__":
    main()
