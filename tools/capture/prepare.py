#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Set up a capture: a scratch project, a run directory and the hook config.

    python3 prepare.py HARNESS ROOT RUN [--shell-form]

HARNESS is the harness to capture: claude_code, codex, cursor, copilot,
gemini, devin or cline. ROOT is a folder outside any repo, such as
~/pairtime-capture. RUN names the run after the surface it captures, such as
cli or vscode. This makes:

  ROOT/project/      a small made-up git project for the harness to work in,
                     so nothing it reads or edits is yours
  ROOT/runs/RUN/     where the dumper, the sink and the collector write, with
                     capture.json saying how the run was set up
  ROOT/claude-home/  the harness's own settings folder for the capture
                     (codex-home, copilot-home and gemini-home for the
                     harnesses that have one)

The settings folder keeps your own instructions, hooks, plugins, skills and
MCP servers out of the captured sessions, where their text, or what they
print, would end up in a public fixture. You sign in to the harness once
more inside it. For Claude Code, CLAUDE_CODE_DISABLE_CLAUDE_MDS also stops
it reading CLAUDE.md files from the folders above the project, which it
otherwise does all the way up to the top of the disk. Cursor, Copilot in VS
Code, Devin and Cline can't be pointed at a settings folder, so for them
prepare.py refuses to start while any file of yours that they would load
holds something, and prints the commands to move each one aside and back.

For the harnesses that read Claude Code's hook format (claude_code, cursor,
devin), it registers the dumper for every hook DESIGN 5.1 installs, in the
scratch project's own settings (ROOT/project/.claude/settings.local.json),
so only sessions in that folder are captured. SCRIPTS.md has the hook config
the other harnesses read.

On macOS and Linux, hooks are registered in exec form, the way pairtime
installs its own: the command is dump-hook.sh itself and the run and label
are its "args". A harness that ignores "args" then runs the dumper with no
arguments, and it exits 0 without reading the payload; a shell given the
payload as its script would run any $(...) or `...` in it. --shell-form
writes one command string instead, for a harness version that rejects
"args". On Windows, exec form would need powershell.exe as the command,
which reads the payload as commands when "args" is dropped, so every hook is
written as one command string there.

Then it prints the commands for the rest of the capture. Standard library
only, Python 3.8+.
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

# The suffix of a file or folder moved aside for a capture.
ASIDE = ".pairtime-aside"

# What ROOT may hold: what prepare.py makes there, and the files a file
# manager adds when you look at a folder.
ROOT_ENTRIES = {"project", "projects", "runs", "claude-home", "codex-home", "copilot-home", "gemini-home",
                ".DS_Store", "Thumbs.db", "desktop.ini"}


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

# Claude Code-format hooks in your own settings, which Cursor and Devin run
# in every session (HS 4.1, 8.1).
CLAUDE_USER_HOOKS = [
    (".claude/settings.json", "hooks", "hooks"),
    (".claude/settings.local.json", "hooks", "hooks"),
]


class Harness:
    """What prepare.py needs to know about one harness.

    home is the variable that points the harness at a settings folder of its
    own, with that folder's name under ROOT; history is the history file
    inside that folder. user_files are the files of yours the harness loads
    when it can't be pointed elsewhere: (path below the home folder, what it
    holds, the JSON key that must be empty, or None for a file or folder
    that must hold nothing). A path starting with %APPDATA%/ is below that
    folder on Windows. strict means every surface of the run uses the
    settings folder, so collect.py refuses a session file from outside it.
    """

    def __init__(self, title, claude_hooks, home=None, env=(), history=None, version=None, user_files=(),
                 strict=False):
        self.title = title
        self.claude_hooks = claude_hooks
        self.home = home
        self.env = list(env)
        self.history = history
        self.version = version
        self.user_files = list(user_files)
        self.strict = strict


HARNESSES = {
    # CLAUDE_CONFIG_DIR moves settings, hooks, plugins, skills, .claude.json
    # (user MCP servers and folder trust), history and transcripts (HS 3.2).
    "claude_code": Harness("Claude Code", True, home=("CLAUDE_CONFIG_DIR", "claude-home"),
                           env=[("CLAUDE_CODE_DISABLE_CLAUDE_MDS", "1")] + OTEL_ENV,
                           history="history.jsonl", version=["claude", "--version"], strict=True),
    # CODEX_HOME moves config.toml, AGENTS.md, hooks.json, sessions and
    # history.jsonl (HS 6.1, 6.2, 6.4).
    "codex": Harness("Codex", False, home=("CODEX_HOME", "codex-home"), history="history.jsonl",
                     version=["codex", "--version"], strict=True),
    "cursor": Harness("Cursor", True, version=["cursor", "--version"], user_files=[
        (".cursor/hooks.json", "hooks", "hooks"),
    ] + CLAUDE_USER_HOOKS + [
        (".cursor/mcp.json", "MCP servers", "mcpServers"),
        (".cursor/skills", "skills", None),
        (".claude/skills", "skills", None),
        (".codex/skills", "skills", None),
        (".agents/skills", "skills", None),
    ]),
    # COPILOT_HOME moves the CLI's folder; VS Code reads ~/.copilot as well,
    # so it is checked too (HS 5.1, 5.4).
    "copilot": Harness("GitHub Copilot", False, home=("COPILOT_HOME", "copilot-home"),
                       version=["copilot", "--version"], user_files=[
        (".copilot/hooks", "hooks", None),
        (".copilot/settings.json", "hooks", "hooks"),
        (".copilot/copilot-instructions.md", "instructions", None),
        (".copilot/instructions", "instructions", None),
    ]),
    # GEMINI_CLI_HOME replaces the home folder Gemini CLI keeps .gemini/ in
    # (packages/core/src/utils/paths.ts, homedir()).
    "gemini": Harness("Gemini CLI", False, home=("GEMINI_CLI_HOME", "gemini-home"),
                      version=["gemini", "--version"], strict=True),
    "devin": Harness("Devin", True, version=["devin", "--version"], user_files=[
        (".config/devin/config.json", "hooks", "hooks"),
        ("%APPDATA%/devin/config.json", "hooks", "hooks"),
        (".claude.json", "hooks", "hooks"),
    ] + CLAUDE_USER_HOOKS + [
        (".devin/rules", "rules", None),
        (".config/devin/skills", "skills", None),
        (".agents/skills", "skills", None),
        (".config/devin/mcp_config.json", "MCP servers", "mcpServers"),
    ]),
    "cline": Harness("Cline", False, user_files=[
        ("Documents/Cline/Hooks", "hooks", None),
        ("Documents/Cline/Rules", "global rules", None),
        (".agents/AGENTS.md", "instructions", None),
    ]),
}

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


def ps_quote(s):
    """A PowerShell string literal: nothing inside single quotes expands."""
    return "'" + s.replace("'", "''") + "'"


def quote(s, windows=None):
    """s as one word of a printed command: as is when every shell reads it
    as plain text, quoted otherwise, and always quoted for PowerShell."""
    if windows is None:
        windows = WINDOWS
    if windows:
        return ps_quote(s)
    return s if SHELL_SAFE.match(s) else sh_quote(s)


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


def user_path(rel, home, environ):
    """Where a user_files entry is on this machine, or None."""
    if rel.startswith("%APPDATA%/"):
        base = environ.get("APPDATA") if WINDOWS else None
        if not base:
            return None
        rel = rel[len("%APPDATA%/"):]
    else:
        base = home
    return os.path.join(base, *rel.split("/"))


def holds(path, key):
    """Whether path holds something a harness would load: any file in a
    folder, any byte in a file, or a non-empty key in a JSON file. A JSON
    file that can't be read counts, since what it holds can't be told."""
    if key is None:
        if os.path.isdir(path):
            return any(names for _, _, names in os.walk(path))
        return os.path.isfile(path) and os.path.getsize(path) > 0
    if not os.path.isfile(path):
        return False
    try:
        with open(path, "rb") as f:
            doc = json.loads(f.read().decode("utf-8-sig"))
    except (OSError, UnicodeDecodeError, ValueError):
        return True
    return not isinstance(doc, dict) or bool(doc.get(key))


def user_files_in_the_way(harness, home=None, environ=None):
    """The files of yours this harness would load into the capture:
    [(path, what it holds)], each path once."""
    home = home or os.path.expanduser("~")
    environ = os.environ if environ is None else environ
    found, seen = [], set()
    for rel, what, key in harness.user_files:
        path = user_path(rel, home, environ)
        if path and holds(path, key):
            if path in seen:
                continue
            seen.add(path)
            found.append((path, what))
    return found


def moved_aside(harness, home=None, environ=None):
    """The files of yours moved aside for a capture, still not moved back."""
    home = home or os.path.expanduser("~")
    environ = os.environ if environ is None else environ
    out = []
    for rel, _, _ in harness.user_files:
        path = user_path(rel, home, environ)
        if path and os.path.lexists(path + ASIDE) and path not in out:
            out.append(path)
    return out


def move_command(src, dst, windows=None):
    if windows is None:
        windows = WINDOWS
    if windows:
        return "Move-Item -LiteralPath %s -Destination %s" % (ps_quote(src), ps_quote(dst))
    return "mv %s %s" % (quote(src, False), quote(dst, False))


def preflight(harness, home=None, environ=None, windows=None):
    """Refuse while files of yours that the harness would load hold
    something, with the commands to move them aside and back."""
    found = user_files_in_the_way(harness, home, environ)
    if not found:
        return
    taken = [p for p, _ in found if os.path.lexists(p + ASIDE)]
    lines = ["%s can't be given a settings folder of its own, so it would load these files of yours into the "
             "captured sessions, where their text, or what they run, could end up in a public fixture:" % harness.title, ""]
    lines += ["  %s (%s)" % (p, what) for p, what in found]
    if taken:
        lines += ["", "These were moved aside once already and not moved back, so there are two copies now. Look at "
                      "both, keep the one you want under its own name, and delete the other:", ""]
        lines += ["  %s and %s" % (p, p + ASIDE) for p in taken]
    else:
        lines += ["", "Move them aside while you capture:", ""]
        lines += ["  " + move_command(p, p + ASIDE, windows) for p, _ in found]
        lines += ["", "then run prepare.py again. When you're done with the capture, move them back:", ""]
        lines += ["  " + move_command(p + ASIDE, p, windows) for p, _ in found]
    raise Refusal("\n".join(lines))


def harness_version(cmd):
    """The version number cmd --version prints, such as 2.1.328 from
    "2.1.328 (Claude Code)" or 0.170.0 from "codex-cli 0.170.0"."""
    if not cmd:
        return None
    try:
        out = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=20, check=False)
    except (OSError, subprocess.SubprocessError):
        return None
    m = re.search(r"(?<![\w.])v?(\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.]+)?)", out.stdout.decode("utf-8", "replace"))
    return m.group(1) if m else None


def env_lines(pairs, windows=None):
    if windows is None:
        windows = WINDOWS
    return [("$env:%s = %s" if windows else "export %s=%s") % (k, quote(v, windows)) for k, v in pairs]


def make_private(root, windows=None):
    """Make ROOT readable by its owner only, since the harness's settings
    folder inside it will hold a sign-in. Returns what it did, for the
    person to see. On Windows a folder in your user folder is already
    yours alone. ROOT must be a folder of the kit's own: new, or holding
    only what prepare.py makes."""
    if windows is None:
        windows = WINDOWS
    if inside(os.path.expanduser("~"), root):
        raise Refusal("ROOT is your home folder or holds it. Pick a new folder for the capture, such as ~/pairtime-capture.")
    if not os.path.isdir(root):
        os.makedirs(root)
        if not windows:
            os.chmod(root, 0o700)
        return None
    others = sorted(set(os.listdir(root)) - ROOT_ENTRIES)
    if others:
        raise Refusal("%s already holds files the kit didn't make (%s). Pick a new, empty folder for ROOT."
                      % (root, ", ".join(others[:5])))
    if windows:
        return None
    st = os.stat(root)
    if st.st_uid != os.getuid():
        raise Refusal("%s belongs to another account, so it can't be kept private. Pick a new folder for ROOT." % root)
    if st.st_mode & 0o077:
        os.chmod(root, st.st_mode & 0o700)
        return "made %s readable by you only, since the harness's settings folder in it will hold a sign-in" % root
    return None


def instructions(name, h, project, run_dir, home_dir, settings, fixture, created):
    """The commands for the rest of the capture, with this run's paths."""
    py = "py" if WINDOWS else "python3"
    sep = "\n    "
    env = ([(h.home[0], home_dir)] if h.home else []) + h.env
    out = ["", "%s the scratch project %s" % ("Made" if created else "Reusing", project)]
    if settings:
        out.append("Wrote the Claude Code-format hooks to %s, pointing at %s" % (settings, run_dir))
    if home_dir:
        out.append("%s gets a settings folder of its own for the capture: %s" % (h.title, home_dir))
    out += ["", "1. In a first terminal, start the sink. It saves telemetry and records the",
            "   step numbers you type into it:", "",
            "    %s %s %s" % (py, quote(os.path.join(KIT_DIR, "otlp_sink.py")), quote(run_dir)), ""]
    if name == "claude_code":
        out += ["2. In a second terminal, set these variables and start Claude Code in the",
                "   scratch project. CLAUDE_CONFIG_DIR gives it the settings folder above, so",
                "   your own instructions, hooks, plugins and MCP servers stay out of the",
                "   capture; it asks you to sign in once there. For the VS Code run, quit VS",
                "   Code completely, then start it from this terminal with: code %s" % quote(project), ""]
    elif env:
        out += ["2. In a second terminal, set %s and go to the scratch project:" % (
            "this variable, so %s uses the settings folder above," % h.title if len(env) == 1 else "these variables"), ""]
    else:
        out += ["2. In a second terminal, go to the scratch project:", ""]
    out += ["    " + sep.join(env_lines(env) + ["cd " + quote(project)] + (["claude"] if name == "claude_code" else [])), ""]
    out += ["3. Follow the \"%s\" section of tools/capture/SCRIPTS.md, typing each step's" % h.title,
            "   number into the first terminal before you do the step.", ""]
    collect = "%s %s %s" % (py, quote(os.path.join(KIT_DIR, "collect.py")), quote(run_dir))
    if h.history and home_dir:
        collect += " --history " + quote(os.path.join(home_dir, h.history))
    out += ["4. Quit %s, stop the sink with Ctrl+C, then copy the session files the" % h.title,
            "   hooks named%s:" % (" and the matching lines of the prompt history" if h.history
                                   else ", adding the --extra files that section names"), "",
            "    " + collect, "",
            "5. Scrub into the repo, read the report, then read every file it wrote:", "",
            "    %s %s %s %s" % (py, quote(os.path.join(KIT_DIR, "scrub.py")), quote(run_dir), quote(fixture)), ""]
    return "\n".join(out)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("harness", choices=sorted(HARNESSES), help="the harness to capture")
    ap.add_argument("root", help="a folder outside any repo, such as ~/pairtime-capture")
    ap.add_argument("run", help="the run's name, after the surface it captures: cli, vscode, ...")
    ap.add_argument("--shell-form", action="store_true", help="write each hook as one command string")
    args = ap.parse_args(argv)
    h = HARNESSES[args.harness]

    root = os.path.abspath(os.path.expanduser(args.root))
    if inside(root, REPO_DIR) or inside(REPO_DIR, root):
        ap.error("ROOT must be outside the pairtime checkout, so raw captures can't be committed by accident")
    if not args.run.replace("-", "").replace("_", "").isalnum():
        ap.error("RUN must be a plain name such as cli or vscode")
    project = os.path.join(root, "project")
    run_dir = os.path.join(root, "runs", args.run)
    if os.path.isdir(os.path.join(run_dir, "hooks")) and os.listdir(os.path.join(run_dir, "hooks")):
        ap.error("%s already holds a capture; pick another RUN name or delete it" % run_dir)
    home_dir = os.path.join(root, h.home[1]) if h.home else None

    try:
        check_dumper()
        preflight(h)
        config = claude_settings(run_dir, args.shell_form) if h.claude_hooks else None
        note = make_private(root)
    except Refusal as e:
        ap.error(str(e))
    if note:
        print(note)

    created = make_project(project)
    os.makedirs(run_dir, exist_ok=True)
    if home_dir:
        os.makedirs(home_dir, exist_ok=True)
    settings = os.path.join(project, ".claude", "settings.local.json")
    if os.path.exists(settings):
        # An earlier run's hooks would write into that run, and a harness
        # that doesn't read this file in real use mustn't see it here.
        backup = settings + ".bak-" + time.strftime("%Y%m%dT%H%M%S")
        shutil.move(settings, backup)
        print("moved the old %s to %s" % (settings, backup))
    if config is not None:
        os.makedirs(os.path.dirname(settings), exist_ok=True)
        with open(settings, "w", encoding="utf-8", newline="\n") as f:
            json.dump(config, f, indent=2)
            f.write("\n")

    with open(os.path.join(run_dir, "capture.json"), "w", encoding="utf-8", newline="\n") as f:
        json.dump({
            "harness": args.harness,
            "run": args.run,
            "project": project,
            "settings_folder": {h.home[0]: home_dir} if h.home else {},
            "strict": h.strict,
            "hooks": "none" if config is None else "powershell" if WINDOWS else "shell" if args.shell_form else "exec",
            "prepared_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }, f, indent=2)
        f.write("\n")

    version = harness_version(h.version) or "<version>"
    osname = {"nt": "windows"}.get(os.name, "macos" if sys.platform == "darwin" else "linux")
    fixture = os.path.join(REPO_DIR, "testdata", "fixtures", args.harness, version, osname, args.run)
    print(instructions(args.harness, h, project, run_dir, home_dir, settings if config is not None else None,
                       fixture, created))
    if version == "<version>":
        print("Put the version number %s reports in place of <version>." % h.title)
    aside = moved_aside(h)
    if aside:
        print("\nWhen you're done with the capture, move back what you moved aside for it:\n")
        print("\n".join("    " + move_command(p + ASIDE, p) for p in aside))
    return 0


if __name__ == "__main__":
    sys.exit(main())
