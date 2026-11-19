#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Set up a capture: a scratch project, a run directory and the hook config.

    python3 prepare.py claude_code ROOT RUN [--shell-form]

ROOT is a folder outside any repo, such as ~/pairtime-capture. This makes:

  ROOT/project/   a small made-up git project for the harness to work in, so
                  nothing it reads or edits is yours
  ROOT/runs/RUN/  where the dumper and the sink write. RUN names the surface:
                  cli, vscode, desktop

and registers the dumper for every hook DESIGN 5.1 installs, in the scratch
project's own settings (ROOT/project/.claude/settings.local.json), so only
sessions in that folder are captured and your other sessions are untouched.
An existing settings.local.json there is kept as a .bak file.

Hooks are registered in exec form, the way pairtime installs its own: the
command is /bin/sh or powershell.exe and the script is an argument.
--shell-form writes one command string instead, for a harness version that
doesn't take "args".

Then it prints the commands for the rest of the capture. Only claude_code is
automated so far; SCRIPTS.md has the hook config to write by hand for the
others. Standard library only, Python 3.8+.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import time

KIT_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_DIR = os.path.dirname(os.path.dirname(KIT_DIR))
WINDOWS = os.name == "nt"

# DESIGN 5.1's Claude Code hooks, with the matcher pairtime installs.
# SessionEnd hooks share a 1.5 s budget unless a hook sets a longer timeout,
# and PowerShell can take most of that just to start.
CLAUDE_CODE_HOOKS = [
    ("SessionStart", None, None),
    ("UserPromptSubmit", None, None),
    ("Stop", None, None),
    ("StopFailure", None, None),
    ("Notification", None, None),
    ("PermissionRequest", None, None),
    ("PostToolUse", "Edit|Write|Bash", None),
    ("SubagentStart", None, None),
    ("SubagentStop", None, None),
    ("SessionEnd", None, 10),
]

OTEL_ENV = [
    ("CLAUDE_CODE_ENABLE_TELEMETRY", "1"),
    ("OTEL_METRICS_EXPORTER", "otlp"),
    ("OTEL_LOGS_EXPORTER", "otlp"),
    ("OTEL_EXPORTER_OTLP_PROTOCOL", "http/json"),
    ("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318"),
    ("OTEL_METRICS_INCLUDE_ENTRYPOINT", "true"),
    ("OTEL_METRIC_EXPORT_INTERVAL", "10000"),
    ("OTEL_LOG_USER_PROMPTS", "0"),
    ("OTEL_LOG_ASSISTANT_RESPONSES", "0"),
    ("OTEL_LOG_TOOL_DETAILS", "0"),
]

SCRATCH_FILES = {
    "README.md": """# wordcount

A scratch project for a pairtime fixture capture. Everything in it is made up.
""",
    "wordcount.py": '''"""Count things in text."""


def count_words(text):
    """Return how many words text has."""
    return len(text.split(" "))
''',
    "test_wordcount.py": '''import unittest

from wordcount import count_words


class CountWordsTest(unittest.TestCase):
    def test_simple(self):
        self.assertEqual(count_words("one two three"), 3)

    def test_double_space(self):
        self.assertEqual(count_words("one  two three"), 3)


if __name__ == "__main__":
    unittest.main()
''',
}


def inside(path, root):
    path, root = os.path.realpath(path), os.path.realpath(root)
    return path == root or path.startswith(root.rstrip(os.sep) + os.sep)


def git(project, *args):
    subprocess.run(["git"] + list(args), cwd=project, check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def make_project(project):
    if os.path.isdir(os.path.join(project, ".git")):
        return False
    os.makedirs(project, exist_ok=True)
    for name, text in SCRATCH_FILES.items():
        with open(os.path.join(project, name), "w", encoding="utf-8", newline="\n") as f:
            f.write(text)
    with open(os.path.join(project, ".gitignore"), "w", encoding="utf-8", newline="\n") as f:
        f.write("__pycache__/\n.claude/settings.local.json\n")
    git(project, "init", "-q")
    # A made-up identity, so commits made during the capture name nobody.
    git(project, "config", "user.name", "Capture User")
    git(project, "config", "user.email", "capture@example.invalid")
    git(project, "config", "commit.gpgsign", "false")
    git(project, "add", ".")
    git(project, "commit", "-q", "-m", "Start wordcount")
    return True


def hook_command(run_dir, label, shell_form):
    if WINDOWS:
        ps = os.path.join(os.environ.get("SystemRoot", r"C:\Windows"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
        args = ["-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File",
                os.path.join(KIT_DIR, "dump-hook.ps1"), run_dir, label]
        if shell_form:
            return {"command": " ".join(['"%s"' % ps] + ['"%s"' % a if " " in a or "\\" in a else a for a in args])}
        return {"command": ps, "args": args}
    script = os.path.join(KIT_DIR, "dump-hook.sh")
    if shell_form:
        quote = lambda s: "'" + s.replace("'", "'\\''") + "'"
        return {"command": " ".join(["/bin/sh", quote(script), quote(run_dir), label])}
    return {"command": "/bin/sh", "args": [script, run_dir, label]}


def claude_settings(run_dir, shell_form):
    hooks = {}
    for event, matcher, timeout in CLAUDE_CODE_HOOKS:
        handler = {"type": "command"}
        handler.update(hook_command(run_dir, event, shell_form))
        if timeout:
            handler["timeout"] = timeout
        entry = {"hooks": [handler]}
        if matcher:
            entry = {"matcher": matcher, "hooks": [handler]}
        hooks[event] = [entry]
    return {"hooks": hooks}


def harness_version(cmd):
    try:
        out = subprocess.run([cmd, "--version"], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=20, check=False)
    except (OSError, subprocess.SubprocessError):
        return None
    words = out.stdout.decode("utf-8", "replace").split()
    return words[0] if words else None


def env_lines():
    if WINDOWS:
        return ['$env:%s = "%s"' % (k, v) for k, v in OTEL_ENV]
    return ["export %s=%s" % (k, v) for k, v in OTEL_ENV]


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("harness", choices=["claude_code"], help="the harness to capture")
    ap.add_argument("root", help="a folder outside any repo, such as ~/pairtime-capture")
    ap.add_argument("run", help="the run's name: cli, vscode, desktop")
    ap.add_argument("--shell-form", action="store_true", help="write each hook as one command string")
    args = ap.parse_args(argv)

    root = os.path.abspath(os.path.expanduser(args.root))
    if inside(root, REPO_DIR) or inside(REPO_DIR, root):
        ap.error("ROOT must be outside the pairtime checkout, so raw captures can't be committed by accident")
    if not args.run.replace("-", "").replace("_", "").isalnum():
        ap.error("RUN must be a plain name such as cli or vscode")
    project = os.path.join(root, "project")
    run_dir = os.path.join(root, "runs", args.run)
    if os.path.isdir(os.path.join(run_dir, "hooks")) and os.listdir(os.path.join(run_dir, "hooks")):
        ap.error("%s already holds a capture; pick another RUN name or delete it" % run_dir)

    created = make_project(project)
    os.makedirs(run_dir, exist_ok=True)
    settings_dir = os.path.join(project, ".claude")
    os.makedirs(settings_dir, exist_ok=True)
    settings = os.path.join(settings_dir, "settings.local.json")
    if os.path.exists(settings):
        backup = settings + ".bak-" + time.strftime("%Y%m%dT%H%M%S")
        shutil.copyfile(settings, backup)
        print("kept the old %s as %s" % (settings, backup))
    with open(settings, "w", encoding="utf-8", newline="\n") as f:
        json.dump(claude_settings(run_dir, args.shell_form), f, indent=2)
        f.write("\n")

    version = harness_version("claude") or "<version from claude --version>"
    osname = {"nt": "windows"}.get(os.name, "macos" if sys.platform == "darwin" else "linux")
    py = "py" if WINDOWS else "python3"
    fixture = os.path.join(REPO_DIR, "testdata", "fixtures", "claude_code", version, osname, args.run)
    sep = "\n    "
    print("""
%s the scratch project %s
Wrote the hooks to %s, pointing at %s

1. In a first terminal, start the sink. It saves telemetry and records the
   step numbers you type into it:

    %s "%s" "%s"

2. In a second terminal, set the telemetry variables and start Claude Code in
   the scratch project (for the VS Code run, quit VS Code completely, then
   start it from this terminal with: code "%s"):

    %s
    cd "%s"
    claude

3. Follow tools/capture/SCRIPTS.md, typing each step's number into the first
   terminal before you do the step.

4. Quit Claude Code, stop the sink with Ctrl+C, then copy the transcripts the
   hooks named and the matching lines of your prompt history:

    %s "%s" "%s" --history "%s"

5. Scrub into the repo, read the report, then read every file it wrote:

    %s "%s" "%s" "%s"
""" % ("Made" if created else "Reusing", project, settings, run_dir,
       py, os.path.join(KIT_DIR, "otlp_sink.py"), run_dir,
       project,
       sep.join(env_lines()), project,
       py, os.path.join(KIT_DIR, "collect.py"), run_dir, os.path.join(os.path.expanduser("~"), ".claude", "history.jsonl"),
       py, os.path.join(KIT_DIR, "scrub.py"), run_dir, fixture))
    return 0


if __name__ == "__main__":
    sys.exit(main())
