#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Run a command as the capture's throwaway account, with a filtered environment.

    python3 -I runas.py --account NAME --uid UID [NAME=VALUE ...] -- ARGV...

Run it as root. The command gets the account's home, a PATH with the scratch
copies in /home/NAME/bin first, the scratch T3 Code home, T3 Code's telemetry
off and Claude Code's auto-updater off. The model's credentials are passed on
from this process's environment when set, and never printed. Extra NAME=VALUE
arguments before -- add or replace variables. See README.md.
"""

import argparse
import os
import sys

PASSED_ON = ("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--account", required=True)
    ap.add_argument("--uid", type=int, required=True)
    ap.add_argument("rest", nargs=argparse.REMAINDER)
    args = ap.parse_args()
    rest = args.rest
    extra = {}
    while rest and rest[0] != "--" and "=" in rest[0]:
        k, v = rest.pop(0).split("=", 1)
        extra[k] = v
    if rest and rest[0] == "--":
        rest = rest[1:]
    if not rest:
        sys.exit("runas.py: no command after --")
    home = os.path.join("/home", args.account)
    env = {
        "HOME": home,
        "USER": args.account,
        "LOGNAME": args.account,
        "PATH": os.path.join(home, "bin") + ":/usr/bin:/bin",
        "LANG": "C.UTF-8",
        "TERM": "xterm-256color",
        "T3CODE_HOME": os.path.join(home, "t3home"),
        "T3CODE_TELEMETRY_ENABLED": "false",
        "DISABLE_AUTOUPDATER": "1",
    }
    for k in PASSED_ON:
        if k in os.environ:
            env[k] = os.environ[k]
    env.update(extra)
    os.chdir(home)
    ids = str(args.uid)
    os.execvpe("setpriv", ["setpriv", "--reuid=" + ids, "--regid=" + ids, "--init-groups", "--"] + rest, env)


if __name__ == "__main__":
    main()
