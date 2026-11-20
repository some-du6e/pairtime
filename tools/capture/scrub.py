#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Scrub a capture run into a fixture directory, and list every replacement.

    python3 scrub.py RUN_DIR OUT_DIR [--also VALUE]... [--dry-run]

Copies every text file under RUN_DIR to OUT_DIR, replacing private values
with the fixed placeholders in testdata/fixtures/README.md, but only once the
checks below find nothing left over:

  - known values, found on this machine or given with flags: the home folder,
    the account name, the hostname, git's user.name and user.email, and
    anything passed with --also (your real name, your school, a folder name).
    Each one is replaced in every spelling the files use: as is, JSON-escaped,
    with forward slashes, URL-encoded, and the way Claude Code names project
    folders (every character that isn't a letter or digit becomes -). An
    account name, hostname or git name that is also an everyday word or a
    harness field (User, admin, laptop) or is under four characters is
    replaced only as a path or an identity (a home folder, name@host,
    HOST\\name, Name <email>), so "type":"user" stays as it is; --common
    treats another value that way;
  - anything shaped like an API key, token, password or private key;
  - email addresses, apart from a few public tool addresses;
  - the account, user and organization ids harnesses put in telemetry, and
    any JSON member named like one, anywhere in a file: Codex's
    creator_user_id, Cline's userId, machine and installation ids.

File and folder names get the same replacements. Files that aren't UTF-8
text, such as protobuf telemetry, are left out, since they can't be scrubbed.

Then it checks the result: any known value still present without a word
boundary (karim in karimsLaptop) is listed, and so is every prompt in the hook
payloads with a line that isn't in SCRIPTS.md, since every prompt should come
from the script. So is every line of your own instruction files that the
capture holds (CLAUDE.md, AGENTS.md, GEMINI.md, rules and the like, from your
home folder and from the folders above the scratch project): prepare.py's
settings folder keeps them out of the captured sessions, and a line found
here means the harness loaded them anyway. While the report lists a
leftover of either kind, the scrub writes nothing to OUT_DIR and exits 1;
--accept-leftovers writes it anyway, for someone who has checked every one
and found none private.

The report, which holds the original values, is printed and saved beside
RUN_DIR as RUN_DIR-scrub-report.txt, never inside OUT_DIR or anywhere in the
pairtime checkout: it must not be committed. Scrubbing a fixture that is
already in the repo needs --report with a path outside it. Read the report,
then read every file in OUT_DIR yourself.

This is the kit's stopgap until `pairtime fixtures scrub` (pkg/redact, WP-PV1)
exists. Standard library only, Python 3.8+.
"""

import argparse
import bisect
import getpass
import json
import os
import platform
import re
import socket
import subprocess
import sys
import urllib.parse

KIT_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_DIR = os.path.dirname(os.path.dirname(KIT_DIR))

USER = "capture"
HOST = "capture-host"
NAME = "Capture User"
EMAIL = "capture@example.invalid"
OTHER = "REDACTED"
ACCOUNT_ID = "00000000-0000-4000-8000-000000000000"

# Public addresses that tools write into commits and remotes. Keeping them
# keeps fixtures realistic; none of them names a person.
KEEP_EMAILS = {
    "noreply@anthropic.com",
    "noreply@github.com",
    "noreply@openai.com",
    "git@github.com",
    "git@gitlab.com",
    "git@bitbucket.org",
    "git@ssh.dev.azure.com",
}
KEEP_EMAIL_DOMAINS = ("example.com", "example.org", "example.net", "example.invalid")
KEEP_EMAIL_TLDS = (".example", ".invalid", ".test", ".localhost")

# Telemetry attributes that identify an account. Claude Code attaches these
# by default (docs/research/ai-harness-signals.md 3.3).
ACCOUNT_ATTRIBUTES = (
    "user.account_uuid",
    "user.account_id",
    "user.id",
    "organization.id",
    "enduser.id",
    "installation.id",
)

# JSON members anywhere in a file whose value names an account, an
# organization or a machine, so it links every fixture one person makes:
# Codex's session_meta creator ids (HS 6.2), the userId in every Cline hook
# payload (HS 9), Gemini's installation id (HS 7.3), and the like. Session
# and turn ids are random per session, so they stay.
ACCOUNT_KEYS = (
    "creator_user_id", "creator_account_id", "userId", "userID", "user_id", "accountId", "account_id",
    "accountUuid", "account_uuid", "organizationId", "organization_id", "organizationUuid", "organization_uuid",
    "orgId", "org_id", "installationId", "installation_id", "machineId", "machine_id", "deviceId", "device_id",
    "anonymousId", "anonymous_id",
)

# (kind, regex). Order matters only for ties: the first rule wins a span two
# rules match in full. A rule with a (?P<secret>...) group replaces only that
# group, so "Bearer " or "password=" stays readable. Each pattern can only
# start where a token starts (a literal, or a lookbehind), so a long base64
# run, such as an image in a transcript, costs linear time rather than
# quadratic.
#
# In JSON text a value can follow an escape such as \n, so the letter before
# it is n. ESC counts an escape as a boundary, and the email pattern can't
# start just after a backslash, so it never swallows half an escape.
ESC = r"(?<=\\[ntrbf])|(?<=\\u[0-9A-Fa-f]{4})"
B = r"(?:(?<![A-Za-z0-9])|" + ESC + ")"
SECRET_PATTERNS = [
    ("PRIVATE_KEY", r"-----BEGIN[A-Z ]{0,40}PRIVATE KEY-----.*?-----END[A-Z ]{0,40}PRIVATE KEY-----"),
    ("ANTHROPIC_KEY", B + r"sk-ant-[A-Za-z0-9_-]{16,}"),
    ("OPENAI_KEY", B + r"sk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_-]{20,}"),
    ("GITHUB_TOKEN", B + r"(?:(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})"),
    ("GITLAB_TOKEN", B + r"glpat-[A-Za-z0-9_-]{20,}"),
    ("SLACK_TOKEN", B + r"xox[abposr]-[A-Za-z0-9-]{10,}"),
    ("AWS_KEY_ID", B + r"(?:AKIA|ASIA)[0-9A-Z]{16}(?![A-Za-z0-9])"),
    ("GOOGLE_API_KEY", B + r"AIza[0-9A-Za-z_-]{35}"),
    ("STRIPE_KEY", B + r"(?:sk|rk|pk)_(?:live|test)_[A-Za-z0-9]{16,}"),
    ("HUGGINGFACE_TOKEN", B + r"hf_[A-Za-z0-9]{30,}"),
    ("NPM_TOKEN", B + r"npm_[A-Za-z0-9]{36}"),
    ("JWT", B + r"eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}"),
    ("URL_PASSWORD", r"(?i)(?<![a-z0-9+.-])[a-z][a-z0-9+.-]*://[^/\s:@\"']+:(?P<secret>[^/\s@\"']+)@"),
    ("BEARER_TOKEN", r"(?i)(?:\b|" + ESC + r")bearer\s+(?P<secret>[A-Za-z0-9._~+/-]{16,}=*)"),
    ("SECRET", r"(?i)(?:api[_-]?key|access[_-]?key|secret(?:[_-]?key)?|(?:access|auth|refresh|session|bearer)?[_-]?token"
               r"|passwd|password|pwd)[\"']?\s*[:=]\s*[\"']?(?P<secret>[A-Za-z0-9_\-./+=]{12,})"),
]

EMAIL_PATTERN = r"(?:(?<![A-Za-z0-9._%+\\-])|" + ESC + r")[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}"

SKIP_NAMES = {".DS_Store", "Thumbs.db", "desktop.ini"}

# Account names, hostnames and git names that are also everyday words or the
# values of harness fields. Replaced as whole words, an account named User,
# the default behind many a C:\Users\User, would turn every "type":"user" and
# "role":"user" in a transcript into "capture", and the key user.email in a
# telemetry body into capture.email. These, and any such value under four
# characters, are replaced only where they stand as a path or an identity.
COMMON_KINDS = ("user", "host", "git name")
COMMON_NAMES = {
    # accounts that Windows, a laptop maker, a distro or a cloud image sets up
    "user", "users", "owner", "admin", "administrator", "guest", "student", "test", "tester", "testuser", "root",
    "ubuntu", "debian", "fedora", "centos", "kali", "pi", "vagrant", "runner", "default", "defaultuser0", "public",
    "home", "dev", "developer", "demo", "temp", "work", "school", "family", "codespace", "azureuser", "ec2-user",
    "android", "chronos",
    # hostnames a machine gets when nobody names it
    "laptop", "desktop", "pc", "computer", "workstation", "server", "macbook", "macbook-pro", "macbook-air", "imac",
    "mac-mini", "mac", "raspberrypi", "penguin",
    # words harnesses use as field names and values
    "assistant", "system", "tool", "agent", "model", "human", "type", "role", "content", "text", "message", "session",
    "prompt", "source", "name", "id", "file", "input", "output", "result", "error",
}

# Instruction files the harnesses load into every session (HS 3.4, 4.4, 5.4,
# 6.4, 7.4, 8.3, 9): below your home folder, and, for Claude Code, in every
# folder above the one it runs in. prepare.py keeps them out of the captured
# sessions, and the scrub checks that it did by looking for their lines.
HOME_INSTRUCTIONS = (
    ".claude/CLAUDE.md", ".claude/rules", ".codex/AGENTS.md", ".codex/AGENTS.override.md", ".gemini/GEMINI.md",
    ".copilot/copilot-instructions.md", ".copilot/instructions", ".agents/AGENTS.md", ".devin/rules",
    "Documents/Cline/Rules",
)
FOLDER_INSTRUCTIONS = ("CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/rules", "AGENTS.md",
                       "AGENTS.override.md", "GEMINI.md")
# The settings-folder variables that move the home-folder files above.
MOVED_INSTRUCTIONS = (
    ("CLAUDE_CONFIG_DIR", ("CLAUDE.md", "rules")),
    ("CODEX_HOME", ("AGENTS.md", "AGENTS.override.md")),
    ("COPILOT_HOME", ("copilot-instructions.md", "instructions")),
    ("GEMINI_CLI_HOME", (".gemini/GEMINI.md",)),
)
# Windows gives a home folder whose name has a space or is longer than eight
# characters an 8.3 short name as well, C:\Users\KARIMA~1 for C:\Users\Karim
# Abdallah, and often sets %TEMP% in that form, which Node's os.tmpdir() and
# Python's tempfile hand on as is. SHORT_HOME is such a name, after any
# spelling of Users and a separator.
SHORT_HOME = r"[A-Za-z0-9_$&#@!'()^{}-]{1,7}~[0-9]{1,6}(?![A-Za-z0-9])"
SHORT_HOME_AFTER = (r"(?<=Users\\)", r"(?<=Users\\\\)", r"(?<=Users\\\\\\\\)", r"(?<=Users/)", r"(?<=Users%5C)",
                    r"(?<=Users%2F)")

# prepare.py's name for a file moved aside for a capture.
ASIDE = ".pairtime-aside"

# Where a history entry keeps the prompt besides prompt and display: Codex's
# history.jsonl (text) and Gemini CLI's logs.json (message).
HISTORY_PROMPT_KEYS = ("text", "message")
# What Claude Code shows in a prompt in place of a paste or an image; the
# pasted text itself is in the entry's pastedContents.
PASTE_PLACEHOLDER = re.compile(r"\[(?:Pasted text #\d+(?: \+\d+ lines)?|Image #\d+|\.\.\.Truncated text #\d+ \+\d+ lines\.\.\.)\]")
MAX_INSTRUCTION_BYTES = 1 << 20


class Rule:
    def __init__(self, kind, pattern, placeholder, secret=False, priority=1, keep=None, original=None):
        self.kind = kind
        self.re = re.compile(pattern, re.DOTALL)
        self.placeholder = placeholder
        self.secret = secret
        self.priority = priority
        self.keep = keep
        self.original = original


class Hit:
    __slots__ = ("start", "end", "rule", "text")

    def __init__(self, start, end, rule, text):
        self.start, self.end, self.rule, self.text = start, end, rule, text


def bounded(value):
    """A case-insensitive pattern for value that doesn't match inside a
    longer word: karim matches in /home/karim/ and karim@host, not in karima."""
    p = "(?i:" + re.escape(value) + ")"
    if value[:1].isalnum():
        p = B + p
    if value[-1:].isalnum():
        p = p + "(?![A-Za-z0-9])"
    return p


def spellings(value, placeholder):
    """Every spelling of value a capture file may use, each with the matching
    spelling of its placeholder."""
    out = [(value, placeholder)]

    def add(v, p):
        if v and (v, p) not in out:
            out.append((v, p))

    for ascii_only in (True, False):
        add(json.dumps(value, ensure_ascii=ascii_only)[1:-1], json.dumps(placeholder, ensure_ascii=ascii_only)[1:-1])
    if "/" in value:
        add(value.replace("/", "\\/"), placeholder.replace("/", "\\/"))
    if "\\" in value:
        fwd, pfwd = value.replace("\\", "/"), placeholder.replace("\\", "/")
        add(fwd, pfwd)
        add(value.replace("\\", "\\\\\\\\"), placeholder.replace("\\", "\\\\\\\\"))
        add(urllib.parse.quote(fwd, safe="/"), urllib.parse.quote(pfwd, safe="/"))
    if "/" in value or " " in value or ":" in value:
        add(urllib.parse.quote(value, safe="/"), urllib.parse.quote(placeholder, safe="/"))
    encoded = re.sub(r"[^A-Za-z0-9]", "-", value)
    add(encoded, re.sub(r"[^A-Za-z0-9]", "-", placeholder))
    return out


def home_placeholder(home):
    if re.match(r"^[A-Za-z]:[\\/]", home):
        sep = "\\" if "\\" in home else "/"
        return "C:" + sep + "Users" + sep + USER
    if home.startswith("/Users/"):
        return "/Users/" + USER
    return "/home/" + USER


def run_quiet(cmd):
    try:
        out = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=10, check=False)
    except (OSError, subprocess.SubprocessError):
        return ""
    return out.stdout.decode("utf-8", "replace").strip()


def windows_short_path(path):
    """What GetShortPathNameW gives for path on Windows, or None."""
    try:
        import ctypes
        buf = ctypes.create_unicode_buffer(32768)
        n = ctypes.windll.kernel32.GetShortPathNameW(path, buf, len(buf))
    except (AttributeError, ImportError, OSError, ValueError):
        return None
    return buf.value if 0 < n < len(buf) else None


def short_homes(homes, environ=None, short_path=None):
    """The 8.3 short forms of Windows home folders: what short_path gives
    for each home, and the home part of %TEMP% and %TMP%, which Windows
    often sets in short form."""
    environ = os.environ if environ is None else environ
    out = []
    for h in homes:
        s = short_path(h) if short_path else None
        if s and s.rstrip("\\/").lower() != h.rstrip("\\/").lower() and s.rstrip("\\/") not in out:
            out.append(s.rstrip("\\/"))
    for var in ("TEMP", "TMP"):
        m = re.match(r"^([A-Za-z]:\\Users\\[^\\]*~[0-9]+)(?:\\|$)", environ.get(var, ""), re.I)
        if m and m.group(1) not in out:
            out.append(m.group(1))
    return out


def detect():
    """(kind, value) for the private values this machine knows about."""
    found = []
    homes = [os.path.expanduser("~"), os.environ.get("HOME", ""), os.environ.get("USERPROFILE", "")]
    homes += [os.path.realpath(h) for h in homes if h]
    if os.name == "nt":
        shorts = short_homes([h for h in homes if h], short_path=windows_short_path)
        homes += shorts
        found += [("user", s.rstrip("\\").rsplit("\\", 1)[-1]) for s in shorts]
    for h in homes:
        h = h.rstrip("/\\")
        if h and h not in ("", "/") and len(h) > 3:
            found.append(("home", h))
    users = [os.environ.get(k, "") for k in ("USER", "USERNAME", "LOGNAME")]
    try:
        users.append(getpass.getuser())
    except (KeyError, OSError):
        pass
    users += [os.path.basename(h.rstrip("/\\")) for h in homes if h]
    for u in users:
        if u:
            found.append(("user", u))
    hosts = [socket.gethostname(), platform.node(), os.environ.get("COMPUTERNAME", ""), os.environ.get("HOSTNAME", "")]
    domain = os.environ.get("USERDOMAIN", "")
    if domain and domain.upper() not in ("WORKGROUP", "NT AUTHORITY"):
        hosts.append(domain)
    if sys.platform == "darwin":
        for key in ("ComputerName", "LocalHostName", "HostName"):
            hosts.append(run_quiet(["scutil", "--get", key]))
    for h in list(hosts):
        if h and "." in h:
            hosts.append(h.split(".", 1)[0])
    for h in hosts:
        if h and h.lower() not in ("localhost", "localhost.localdomain"):
            found.append(("host", h))
    for scope in (["--global"], []):
        for key, kind in (("user.name", "git name"), ("user.email", "git email"), ("github.user", "user")):
            v = run_quiet(["git", "config"] + scope + ["--get", key])
            if v:
                found.append((kind, v))
    return found


def placeholder_for(kind, value):
    return {
        "home": home_placeholder(value),
        "user": USER,
        "host": HOST,
        "git name": NAME,
        "git email": EMAIL,
    }.get(kind, OTHER)


def is_common(kind, value, extra=()):
    """Whether a known value is too common a word to replace on its own."""
    v = value.lower()
    return kind in COMMON_KINDS and (len(value) < 4 or v in COMMON_NAMES or v in extra)


def identity_rules(kind, value):
    """Rules that replace a common value only where it stands as a path or
    an identity: a home folder in every spelling and name@host or HOST\\name
    for an account name, @host, HOST\\ and host.local for a hostname, and
    Name <email> for a git name."""
    v = re.escape(value)
    start = r"(?:(?<![A-Za-z0-9._%+-])|" + ESC + ")"
    if kind == "user":
        rules = []
        for home in ("C:\\Users\\" + value, "/Users/" + value, "/home/" + value):
            for s, p in spellings(home, home_placeholder(home)):
                rules.append(Rule("home", bounded(s), p, priority=0, original=value))
        rules.append(Rule("user", "(?i)" + start + v + r"(?=@[A-Za-z0-9])", USER, priority=0, original=value))
        rules.append(Rule("user", r"(?i)(?:(?<=[A-Za-z0-9-]\\)|(?<=[A-Za-z0-9-]\\\\))" + v + r"(?![A-Za-z0-9])",
                          USER, priority=0, original=value))
        return rules
    if kind == "host":
        return [
            Rule("host", r"(?i)(?<=@)" + v + r"(?![A-Za-z0-9-])", HOST, priority=0, original=value),
            Rule("host", "(?i)" + start + v + r"(?=\\{1,2}[A-Za-z0-9]|\.local(?![A-Za-z0-9]))", HOST, priority=0,
                 original=value),
        ]
    return [Rule(kind, "(?i)" + start + v + r"(?=\s*<)", NAME, priority=0, original=value)]


def build_rules(known, common=()):
    """Rules for the known values (each spelling, longest first) and the
    patterns. Known values outrank patterns on a tie. A value is_common
    names gets only identity_rules."""
    rules = []
    seen = set()
    for kind, value in sorted(known, key=lambda kv: -len(kv[1])):
        if value.lower() in seen:
            continue
        seen.add(value.lower())
        if is_common(kind, value, common):
            rules.extend(identity_rules(kind, value))
            continue
        for v, p in spellings(value, placeholder_for(kind, value)):
            rules.append(Rule(kind, bounded(v), p, priority=0, original=value))

    # A value of one of ACCOUNT_KEYS, also inside JSON that is itself a JSON
    # string, where every quote has a backslash before it. A quoted one
    # becomes the fixed id and a number 0, so the JSON stays valid; a null
    # stays.
    keys = "|".join(re.escape(k) for k in ACCOUNT_KEYS)
    rules.append(Rule(
        "ACCOUNT_ID",
        r'(?P<q>\\{0,3})"(?:' + keys + r')(?P=q)"\s*:\s*(?P=q)"(?P<secret>[^"\\]{1,200})(?P=q)"',
        ACCOUNT_ID, secret=True))
    rules.append(Rule(
        "ACCOUNT_ID",
        r'(?P<q>\\{0,3})"(?:' + keys + r')(?P=q)"\s*:\s*(?P<secret>[0-9]{2,})(?![0-9.eE])',
        "0", secret=True))
    attr = "|".join(re.escape(a) for a in ACCOUNT_ATTRIBUTES)
    rules.append(Rule(
        "ACCOUNT_ID",
        r'"key"\s*:\s*"(?:' + attr + r')"\s*,\s*"value"\s*:\s*\{\s*"(?:stringValue|intValue)"\s*:\s*"?(?P<secret>[^"},]+)"?',
        ACCOUNT_ID, secret=True))

    def keep_email(text):
        t = text.lower()
        domain = t.rsplit("@", 1)[-1]
        return t in KEEP_EMAILS or domain in KEEP_EMAIL_DOMAINS or domain.endswith(KEEP_EMAIL_TLDS)

    # A Users\NAME~N folder is a home folder's 8.3 short name, whoever's it
    # is, so it goes in every spelling, even from another machine.
    rules.append(Rule("home", "(?i)(?:" + "|".join(SHORT_HOME_AFTER) + ")" + SHORT_HOME, USER, priority=0))
    rules.append(Rule("email", EMAIL_PATTERN, EMAIL, keep=keep_email))
    for kind, pattern in SECRET_PATTERNS:
        rules.append(Rule(kind, pattern, "REDACTED_" + kind, secret=True))
    return rules


def find_hits(text, rules):
    hits = []
    for rule in rules:
        for m in rule.re.finditer(text):
            if "secret" in rule.re.groupindex and m.group("secret") is not None:
                start, end = m.span("secret")
            else:
                start, end = m.span()
            found = text[start:end]
            # Never replace a placeholder, so scrubbing twice changes nothing.
            if start == end or found == rule.placeholder or found.startswith("REDACTED"):
                continue
            if rule.keep and rule.keep(found):
                continue
            hits.append(Hit(start, end, rule, found))
    hits.sort(key=lambda h: (h.start, -(h.end - h.start), h.rule.priority))
    chosen, last = [], 0
    for h in hits:
        if h.start >= last:
            chosen.append(h)
            last = h.end
    return chosen


def apply(text, hits):
    """The text with every hit replaced, and where each placeholder landed."""
    out, spans, pos, shift = [], [], 0, 0
    for h in hits:
        out.append(text[pos:h.start])
        out.append(h.rule.placeholder)
        start = h.start + shift
        spans.append((start, start + len(h.rule.placeholder)))
        shift += len(h.rule.placeholder) - (h.end - h.start)
        pos = h.end
    out.append(text[pos:])
    return "".join(out), spans


def mask(h):
    if h.rule.secret:
        return "%s... (%d chars)" % (h.text[:4], len(h.text))
    return h.text


def context(clean, span, h, width=24):
    """The replacement in its surroundings, taken from the scrubbed text, so
    the report never shows a neighbouring secret."""
    start, end = span
    shown = "[" + mask(h) + " -> " + h.rule.placeholder + "]"
    text = clean[max(0, start - width):start] + shown + clean[end:end + width]
    return text.replace("\r", "\\r").replace("\n", "\\n").replace("\t", "\\t")


def line_of(starts, offset):
    return bisect.bisect_right(starts, offset)


def files_in(root):
    out = []
    for dirpath, dirs, names in os.walk(root):
        dirs.sort()
        for n in sorted(names):
            out.append(os.path.relpath(os.path.join(dirpath, n), root))
    return out


def scripted_lines():
    try:
        with open(os.path.join(KIT_DIR, "SCRIPTS.md"), encoding="utf-8") as f:
            text = f.read()
    except OSError:
        return None
    return " ".join(text.split())


def json_docs(text):
    """The JSON documents in a file: the whole file, or each JSON line."""
    try:
        return [json.loads(text)]
    except ValueError:
        pass
    docs = []
    for line in text.splitlines():
        if line.lstrip()[:1] in ("{", "["):
            try:
                docs.append(json.loads(line))
            except ValueError:
                pass
    return docs


def strings_in(doc, key=None):
    """(key, string) for every string in a JSON value, with the key of the
    member it sits in."""
    stack = [(key, doc)]
    while stack:
        k, v = stack.pop()
        if isinstance(v, dict):
            stack.extend(v.items())
        elif isinstance(v, list):
            stack.extend((k, x) for x in v)
        elif isinstance(v, str):
            yield k, v


def readable(text):
    """The text the way a person reading it sees it: as stored, then every
    JSON string in it decoded, with each run of whitespace as one space."""
    parts = [text]
    for doc in json_docs(text):
        parts.extend(s for _, s in strings_in(doc))
    return " ".join(" ".join(parts).split())


def run_folders(run_dir):
    """The folders the captured sessions ran in: each hook call's cwd, the
    cwd fields of the payloads, and the project capture.json names."""
    found = set()
    try:
        with open(os.path.join(run_dir, "capture.json"), encoding="utf-8") as f:
            project = json.load(f).get("project")
        if isinstance(project, str) and project:
            found.add(project)
    except (OSError, ValueError, AttributeError):
        pass
    hooks = os.path.join(run_dir, "hooks")
    for name in sorted(os.listdir(hooks)) if os.path.isdir(hooks) else []:
        for leaf in ("cwd", "stdin"):
            try:
                with open(os.path.join(hooks, name, leaf), "rb") as f:
                    text = f.read().decode("utf-8")
            except (OSError, UnicodeDecodeError):
                continue
            if leaf == "cwd":
                if text.strip():
                    found.add(text.strip())
                continue
            for doc in json_docs(text):
                found.update(s for k, s in strings_in(doc) if k == "cwd" and s)
    return sorted(f for f in found if os.path.isabs(f))


def instruction_files(folders, environ=None):
    """Every instruction file on this machine that a captured session could
    have loaded, including ones prepare.py had you move aside."""
    environ = os.environ if environ is None else environ
    bases = [(os.path.expanduser("~"), HOME_INSTRUCTIONS)]
    bases += [(environ[var], rels) for var, rels in MOVED_INSTRUCTIONS if environ.get(var)]
    for folder in folders:
        d = os.path.dirname(os.path.abspath(folder))
        while True:
            bases.append((d, FOLDER_INSTRUCTIONS))
            up = os.path.dirname(d)
            if up == d:
                break
            d = up
    found, seen = [], set()
    for base, rels in bases:
        for rel in rels:
            path = os.path.join(base, *rel.split("/"))
            for p in (path, path + ASIDE):
                files = [p] if os.path.isfile(p) else [os.path.join(r, n) for r, _, ns in os.walk(p) for n in sorted(ns)]
                for f in files:
                    real = os.path.realpath(f)
                    if real not in seen and os.path.isfile(real):
                        seen.add(real)
                        found.append(f)
    return found


def instruction_lines(path, kit_text):
    """(line number, text) for each line of an instruction file long enough
    to be its own, with whitespace runs as one space. Lines the kit itself
    holds (a SCRIPTS.md prompt, the scratch project) are left out."""
    try:
        with open(path, "rb") as f:
            text = f.read(MAX_INSTRUCTION_BYTES).decode("utf-8", "replace")
    except OSError:
        return []
    out = []
    for i, line in enumerate(text.splitlines(), 1):
        s = " ".join(line.split())
        if len(s) >= 20 and sum(c.isalpha() for c in s) >= 12 and s not in kit_text:
            out.append((i, s))
    return out


def kit_text():
    """SCRIPTS.md and prepare.py, the text a capture is meant to hold."""
    parts = []
    for name in ("SCRIPTS.md", "prepare.py"):
        try:
            with open(os.path.join(KIT_DIR, name), encoding="utf-8") as f:
                parts.append(f.read())
        except OSError:
            pass
    return " ".join(" ".join(parts).split())


def prompts_in(text, extra=()):
    """Prompt texts in a hook payload, history entry or OTLP body: the
    prompt fields, the extra keys (a history file's text or message), and
    each pasted block Claude Code keeps beside a history entry."""
    keys = ("prompt", "initialPrompt", "display") + tuple(extra)
    found = []
    stack = json_docs(text)
    while stack:
        v = stack.pop()
        if isinstance(v, dict):
            for k, x in v.items():
                if k in keys and isinstance(x, str) and x.strip():
                    found.append(x)
                elif k == "pastedContents" and isinstance(x, dict):
                    found.extend(p["content"] for p in x.values()
                                 if isinstance(p, dict) and isinstance(p.get("content"), str) and p["content"].strip())
                else:
                    stack.append(x)
        elif isinstance(v, list):
            stack.extend(v)
    return found


def inside(path, root):
    path, root = os.path.realpath(path), os.path.realpath(root)
    return path == root or path.startswith(root.rstrip(os.sep) + os.sep)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("run_dir", help="the raw capture run, outside the repo")
    ap.add_argument("out_dir", help="the fixture run directory to write, such as testdata/fixtures/claude_code/2.1.328/linux/cli")
    ap.add_argument("--also", action="append", default=[], metavar="VALUE",
                    help="another private value to replace with %s: your name, your school, a folder name" % OTHER)
    ap.add_argument("--also-file", metavar="FILE", help="a file of such values, one per line")
    ap.add_argument("--home", action="append", default=[], help="a home folder to replace, if the capture came from another machine")
    ap.add_argument("--user", action="append", default=[], help="an account name to replace")
    ap.add_argument("--host", action="append", default=[], help="a hostname to replace")
    ap.add_argument("--git-name", action="append", default=[], help="a git user.name to replace")
    ap.add_argument("--git-email", action="append", default=[], help="a git user.email to replace")
    ap.add_argument("--drop", action="append", default=[], metavar="VALUE",
                    help="a detected value not to replace at all, because it isn't private")
    ap.add_argument("--common", action="append", default=[], metavar="VALUE",
                    help="an account name, hostname or git name that is also an everyday word or a harness field "
                         "name, such as will or mark: replaced only in paths and as name@host")
    ap.add_argument("--no-detect", action="store_true", help="use only the values given with flags")
    ap.add_argument("--dry-run", action="store_true", help="print the report without writing OUT_DIR")
    ap.add_argument("--accept-leftovers", action="store_true",
                    help="write OUT_DIR even though the report lists leftovers, once you've checked every one "
                         "and none of them is private")
    ap.add_argument("--report", metavar="FILE", help="where to save the report (default: beside RUN_DIR)")
    args = ap.parse_args(argv)

    run_dir = os.path.abspath(args.run_dir)
    out_dir = os.path.abspath(args.out_dir)
    if not os.path.isdir(run_dir):
        ap.error("%s isn't a directory" % run_dir)
    if inside(out_dir, run_dir) or inside(run_dir, out_dir):
        ap.error("RUN_DIR and OUT_DIR must not contain each other")
    if not args.dry_run and os.path.exists(out_dir) and os.listdir(out_dir):
        ap.error("%s already has files; scrub into a new directory" % out_dir)
    report_path = os.path.abspath(args.report or run_dir.rstrip(os.sep) + "-scrub-report.txt")
    if inside(report_path, out_dir) or inside(report_path, run_dir):
        ap.error("the report must go outside RUN_DIR and OUT_DIR")
    if inside(report_path, REPO_DIR):
        ap.error("the report would go to %s, inside the pairtime checkout, and it holds the original values. Give "
                 "--report a path outside the checkout, such as one beside your capture folder." % report_path)

    known = [] if args.no_detect else detect()
    for kind, values in (("home", args.home), ("user", args.user), ("host", args.host),
                         ("git name", args.git_name), ("git email", args.git_email), ("other", args.also)):
        known += [(kind, v) for v in values if v]
    if args.also_file:
        with open(args.also_file, encoding="utf-8") as f:
            known += [("other", line.strip()) for line in f if line.strip() and not line.startswith("#")]
    dropped = {d.lower() for d in args.drop}
    unique = {}
    for k, v in known:
        v = v.strip()
        if v and v.lower() not in dropped and v.lower() not in unique:
            unique[v.lower()] = (k, v)
    known = list(unique.values())
    extra = {c.lower() for c in args.common}
    known += [("user", c) for c in args.common if c.strip() and c.lower() not in unique and c.lower() not in dropped]
    common = sorted({v for k, v in known if is_common(k, v, extra)}, key=str.lower)
    rules = build_rules(known, extra)
    placeholders = sorted({r.placeholder.lower() for r in rules}, key=len, reverse=True)
    written = {}

    rep = []
    totals = {}
    renamed, skipped, leftovers, unscripted, borrowed, pending = [], [], [], [], [], []
    scripts = scripted_lines()
    files = 0
    kit = kit_text()
    sources = instruction_files(run_folders(run_dir))
    needles = [(src, n, line) for src in sources for n, line in instruction_lines(src, kit)]

    for rel in files_in(run_dir):
        src = os.path.join(run_dir, rel)
        if os.path.basename(rel) in SKIP_NAMES or os.path.islink(src):
            skipped.append((rel, "not a capture file" if not os.path.islink(src) else "a symlink"))
            continue
        with open(src, "rb") as f:
            data = f.read()
        try:
            text = data.decode("utf-8")
        except UnicodeDecodeError:
            skipped.append((rel, "not UTF-8 text, so it can't be scrubbed"))
            continue
        files += 1
        hits = find_hits(text, rules)
        clean, spans = apply(text, hits)
        clean_starts = [i for i, c in enumerate(clean) if c == "\n"]

        parts = rel.split(os.sep)
        new_parts = [apply(p, find_hits(p, rules))[0] for p in parts]
        new_rel = os.path.join(*new_parts)
        if new_rel.lower() in written:
            ap.error("%s and %s would both be scrubbed to %s" % (written[new_rel.lower()], rel, new_rel))
        written[new_rel.lower()] = rel
        if new_rel != rel:
            renamed.append((rel, new_rel))
        for h, span in zip(hits, spans):
            key = (h.rule.kind, mask(h), h.rule.placeholder)
            totals[key] = totals.get(key, 0) + 1
            rep.append("%s:%d  %s  %s" % (new_rel.replace(os.sep, "/"), line_of(clean_starts, span[0]) + 1, h.rule.kind,
                                          context(clean, span, h)))

        # Blank out the placeholders first, so a value that happens to be
        # part of one (an account named cap, inside capture) isn't reported.
        lower = clean.lower()
        for ph in placeholders:
            lower = lower.replace(ph, "\0" * len(ph))
        for kind, value in known:
            if is_common(kind, value, extra):
                continue
            i = lower.find(value.lower())
            while i >= 0:
                leftovers.append("%s:%d  %r in %r" % (new_rel.replace(os.sep, "/"), line_of(clean_starts, i) + 1, value,
                                                      clean[max(0, i - 20):i + len(value) + 20]))
                i = lower.find(value.lower(), i + len(value))

        # The raw text, so a line naming you is found before the scrub
        # changes it.
        if needles:
            hay = readable(text)
            for src, n, line in needles:
                if line in hay:
                    borrowed.append("%s  holds line %d of %s: %r" % (new_rel.replace(os.sep, "/"), n, src, line[:80]))

        history = "history" in parts[-1] or parts[-1] == "logs.json"
        if scripts is not None and (parts[0] == "hooks" or history):
            for p in prompts_in(clean, HISTORY_PROMPT_KEYS if history else ()):
                lines = [ln for ln in (PASTE_PLACEHOLDER.sub("", x) for x in p.splitlines())
                         if ln.strip() and " ".join(ln.split()) not in scripts]
                if lines:
                    unscripted.append("%s  %r" % (new_rel.replace(os.sep, "/"), lines[0][:120]))

        # Held back until every file is checked: a leftover in any of them
        # means nothing goes into the repo.
        pending.append((os.path.join(out_dir, new_rel), clean.encode("utf-8")))

    blocked = bool(leftovers or borrowed) and not args.accept_leftovers
    if not args.dry_run and not blocked:
        for dst, data in pending:
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            with open(dst, "wb") as f:
                f.write(data)
    if args.dry_run:
        head = "Checked %d files from %s, a dry run, so nothing was written." % (files, run_dir)
    elif blocked:
        head = ("Checked %d files from %s and wrote NOTHING to %s, because the report lists values that are still "
                "there (STILL PRESENT and FROM YOUR OWN INSTRUCTION FILES below). Fix them and scrub again, or, if "
                "you've checked every one and none is private, scrub again with --accept-leftovers."
                % (files, run_dir, out_dir))
    else:
        head = "Scrubbed %d files from %s into %s%s." % (
            files, run_dir, out_dir, ", with the leftovers below accepted" if leftovers or borrowed else "")
    summary = [head, ""]
    if totals:
        summary.append("Replaced (kind, original, placeholder, count):")
        for (kind, shown, placeholder), n in sorted(totals.items(), key=lambda kv: (-kv[1], kv[0])):
            summary.append("  %-17s %-40s -> %-28s x%d" % (kind, shown, placeholder, n))
    else:
        summary.append("Nothing was replaced.")
    if common:
        summary += ["", "Common words or short values, replaced only in home folder paths and as name@host, HOST\\name "
                        "or Name <email>, never as words on their own: " + ", ".join(common)]
    for title, rows in (
        ("Renamed paths", ["%s -> %s" % (a.replace(os.sep, "/"), b.replace(os.sep, "/")) for a, b in renamed]),
        ("Left out", ["%s: %s" % (a.replace(os.sep, "/"), why) for a, why in skipped]),
        ("STILL PRESENT inside longer words. Check each one; add --also or redo the run if it's private", leftovers),
        ("FROM YOUR OWN INSTRUCTION FILES, so the harness loaded them into the captured sessions. Don't commit this "
         "run. Start over with a new ROOT, with the settings folder prepare.py prints, or with the file moved aside",
         borrowed),
        ("Prompts with a line that isn't in SCRIPTS.md. Each should be machine text or yours to keep", unscripted),
    ):
        if rows:
            summary += ["", title + ":"] + ["  " + r for r in rows]
    if scripts is None:
        summary += ["", "SCRIPTS.md wasn't found next to scrub.py, so prompts weren't checked."]
    summary += ["", "Checked the capture against %d line%s of %d instruction file%s of yours." % (
        len(needles), "" if len(needles) == 1 else "s", len(sources), "" if len(sources) == 1 else "s")]
    summary += ["", "Every replacement, with file:line and context, is in " + report_path,
                "That report holds the original values: keep it out of the repo, and delete it with the run when you're done."]
    if not args.dry_run and not blocked:
        summary.append("Now read every file in %s yourself before committing it." % out_dir)
    if blocked and not args.dry_run:
        summary += ["", head]

    with open(report_path, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(summary) + "\n\nEvery replacement:\n" + "\n".join("  " + r for r in rep) + "\n")
    print("\n".join(summary))
    return 1 if blocked else 0


if __name__ == "__main__":
    sys.exit(main())
