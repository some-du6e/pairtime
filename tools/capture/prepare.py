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

On macOS and Linux, hooks are registered in exec form, the way pairtime
installs its own: the command is dump-hook.sh itself and the run and label
are its "args". A harness that ignores "args" then runs the dumper with no
arguments, and it exits 0 without reading the payload; a shell given the
payload as its script would run any $(...) or `...` in it. --shell-form
writes one command string instead, for a harness version that rejects
"args". On Windows, exec form would need powershell.exe as the command,
which reads the payload as commands when "args" is dropped, so every hook is
written as one command string there.

Then it prints the commands for the rest of the capture. Only claude_code is
automated so far; SCRIPTS.md has the hook config to write by hand for the
others. Standard library only, Python 3.8+.
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import time

KIT_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_DIR = os.path.dirname(os.path.dirname(KIT_DIR))
WINDOWS = os.name == "nt"

# Characters every shell reads as plain text. A harness that drops "args"
# may still run "command" through a shell, so the dumper's path must come
# through that unchanged.
SHELL_SAFE = re.compile(r"^[A-Za-z0-9_@%+=:,./-]+$")

# Characters that bash, PowerShell or cmd would still expand inside double
# quotes, which is how a Windows hook command quotes its paths.
WINDOWS_UNSAFE = re.compile(r'["$`%!\r\n]')


class Refusal(Exception):
    """A setup prepare.py won't write, with the reason to show the person."""

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


def powershell_command(script, run_dir, label):
    """One command string that runs a PowerShell script with the run and the
    label, and that bash (Git Bash), PowerShell and cmd all parse the same
    way. It starts with the bare executable name: PowerShell reads a quoted
    string at the start of a line as a value, not as a command to run."""
    for path in (script, run_dir):
        if WINDOWS_UNSAFE.search(path) or path.startswith("\\\\"):
            raise Refusal("%s has a character that a shell would change inside double quotes (one of \" $ ` %% !), "
                          "or is a network path. Use a folder whose path has none of them." % path)
    return 'powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%s" "%s" %s' % (script, run_dir, label)


def sh_quote(s):
    return "'" + s.replace("'", "'\\''") + "'"


def hook_command(run_dir, label, shell_form, windows=None):
    """The handler fields that run the dumper for one hook event."""
    if windows is None:
        windows = WINDOWS
    if windows:
        return {"command": powershell_command(os.path.join(KIT_DIR, "dump-hook.ps1"), run_dir, label)}
    script = os.path.join(KIT_DIR, "dump-hook.sh")
    if shell_form:
        return {"command": " ".join(["/bin/sh", sh_quote(script), sh_quote(run_dir), label])}
    # Exec form names the dumper itself. If a harness drops "args", it runs
    # the dumper with no arguments, and the dumper exits 0 before reading
    # the payload. Through a shell, only a plain path stays one word.
    if not SHELL_SAFE.match(script):
        raise Refusal("The kit's path, %s, has a character a shell would split or expand. A harness that runs the hook "
                      "command through a shell would then break it. Clone the repo into a folder whose path has only "
                      "letters, digits and - _ . /, or run prepare.py again with --shell-form." % script)
    return {"command": script, "args": [run_dir, label]}


def check_dumper(windows=None):
    """Refuse a dumper that can't run as a hook command: one that lost its
    executable bit (a ZIP download) or got Windows line endings (a checkout
    with core.autocrlf), which would make it fail instead of exiting 0."""
    if windows is None:
        windows = WINDOWS
    if windows:
        return
    script = os.path.join(KIT_DIR, "dump-hook.sh")
    with open(script, "rb") as f:
        if b"\r" in f.read():
            raise Refusal("%s has Windows line endings, so sh can't run it. Pull the latest kit, then check the file out "
                          "again with Unix line endings: cd %s && rm tools/capture/dump-hook.sh && git checkout -- tools/capture/dump-hook.sh"
                          % (script, sh_quote(REPO_DIR)))
    if not os.access(script, os.X_OK):
        raise Refusal("%s isn't executable, so a harness can't run it as a hook. Run: chmod +x %s" % (script, sh_quote(script)))


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

    try:
        check_dumper()
        config = claude_settings(run_dir, args.shell_form)
    except Refusal as e:
        ap.error(str(e))

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
        json.dump(config, f, indent=2)
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
