# SPDX-License-Identifier: AGPL-3.0-or-later
"""Writes the records Claude Code's hook adapter should make from a capture.

    python3 -I internal/adapters/claudecode/testdata/gen_expected.py \
        testdata/fixtures/claude_code/2.1.327/linux/cli \
        internal/adapters/claudecode/fixtures/2.1.327/linux/cli/expected.jsonl

This is the conformance oracle's second implementation: it reads the
captured hook calls and applies SM 1.2, SM 2.2 rules 1 and 3, D-spool-via,
E-6.1-permission and E-cc-queued-turn to them in Python, apart from the Go
adapter, with
tools/ledger_ref.py for what ledger-v1 10.2 and 10.6 define (words, error
signatures, hunks, the path form, command text). The Go conformance test
replays the same calls through the adapter, as walked socket calls, and
diffs the two (internal/adapters, Conform).

Keyed values are written as symbols: the first session key is "session-1",
the next new one "session-2", and likewise for turn, agent, project, file,
command, error and cron keys, so this file needs no key and the expected
records read as what they stand for.

The capture's calls all come from claude processes, so the walk reaches a
host for every one and every record is via hook. Only the four parent
chains the capture has are classified here (SM 2.2 rule 4), and any other
stops the script.
"""

import json
import os
import re
import sys
import unicodedata

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))
sys.path.insert(0, os.path.join(REPO, "tools"))
import ledger_ref  # noqa: E402

COMMON = {"session_id", "transcript_path", "cwd", "hook_event_name", "prompt_id",
          "permission_mode", "effort", "agent_id", "agent_type", "scratchpad_dir"}
OWN = {
    "SessionStart": {"source", "model", "session_title", "seconds_since_last_response", "context_tokens"},
    "UserPromptSubmit": {"prompt", "session_title"},
    "Stop": {"stop_hook_active", "last_assistant_message", "background_tasks", "session_crons"},
    "StopFailure": {"error", "error_details", "last_assistant_message"},
    "Notification": {"message", "title", "notification_type"},
    "PermissionRequest": {"tool_name", "tool_input", "permission_suggestions"},
    "PostToolUse": {"tool_name", "tool_input", "tool_response", "tool_use_id", "duration_ms"},
    "PostToolUseFailure": {"tool_name", "tool_input", "tool_use_id", "error", "is_interrupt", "duration_ms"},
    "SubagentStart": set(),
    "SubagentStop": {"stop_hook_active", "last_assistant_message", "background_tasks", "session_crons",
                     "agent_transcript_path"},
    "SessionEnd": {"reason"},
}

# How many turn ids the adapter remembers per session (claudecode.go).
MAX_TURNS = 64

START_KINDS = {"startup": "startup", "resume": "resume", "clear": "clear", "compact": "compact", "fork": "fork"}
SHELLS = {"bash", "sh", "dash", "zsh", "ksh", "mksh", "ash", "fish", "tcsh", "csh"}

# SM 2.2's go-ahead list, as pkg/score's LowEffort reads it.
GO_AHEADS = ["yes", "y", "ok", "okay", "sure", "continue", "go", "go on", "go ahead", "do it", "proceed",
             "next", "keep going", "lgtm", "looks good", "sounds good", "thanks", "try again", "again",
             "retry", "fix it", "more"]

# Go's unicode.IsSpace: Latin-1 spaces, and White_Space beyond Latin-1.
GO_SPACE_LATIN1 = {0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0}


def go_is_space(ch):
    o = ord(ch)
    if o <= 0xff:
        return o in GO_SPACE_LATIN1
    return ledger_ref.is_space(ch)


def go_is_punct(ch):
    return unicodedata.category(ch).startswith("P")


def normalize(text):
    return "".join(c for c in text.lower() if not go_is_punct(c) and not go_is_space(c))


NORMALIZED_GO_AHEADS = {normalize(g) for g in GO_AHEADS}


def low_effort(text):
    n = normalize(text)
    if len(n) < 4 or n in NORMALIZED_GO_AHEADS:
        return True
    if n.startswith("option"):
        n = n[len("option"):]
    return n != "" and all("0" <= c <= "9" for c in n)


def parents_of(text):
    out = []
    for line in text.splitlines():
        f = line.strip().split(" ", 2)
        if len(f) == 3:
            name = f[2].split(": ", 1)[0]
            if ": " not in f[2] and f[2].endswith(".sh"):
                name = "sh"  # a shell script, such as the kit's allow-once.sh
            out.append({"pid": int(f[0]), "tty": f[1] not in ("?", "??"), "name": name})
    return out


def lineage(parents):
    """SM 2.2 rule 4's parent_class and has_tty for the chains the capture
    has: claude under shells under tmux, or under another claude."""
    # The hook runs under the nearest ancestor that isn't a shell
    # (platform.HookHost).
    while parents and parents[0]["name"] in SHELLS:
        parents = parents[1:]
    if not parents or parents[0]["name"] != "claude":
        raise SystemExit("a hook not run by claude: %r" % parents[:1])
    for p in parents[1:]:
        if p["name"] in SHELLS:
            continue
        if p["name"] == "tmux":
            return "multiplexer", parents[0]["tty"]
        if p["name"] == "claude":
            return "agent", parents[0]["tty"]
        raise SystemExit("a parent this script doesn't class: %s" % p["name"])
    raise SystemExit("a chain that runs out")


def slash_command(text):
    if not text.startswith("/"):
        return ""
    name = text
    for i, c in enumerate(text):
        if c in " \t\r\n":
            name = text[:i]
            break
    if not 2 <= len(name) <= 65:
        return ""
    for c in name[1:]:
        if not (c.isascii() and (c.isalnum() or c in "_:.-")):
            return ""
    return name


CUT_MARKER = "\u2026 [+"


def scheduled(prompt):
    cut = False
    i = prompt.rfind(CUT_MARKER)
    if i >= 0 and prompt.endswith(" chars]"):
        digits = prompt[i + len(CUT_MARKER):-len(" chars]")]
        if digits and digits.isdigit() and digits.isascii():
            prompt, cut = prompt[:i].rstrip("\ufffd"), True
    return {"prefix": prompt[:1000], "cut": cut}


def matches_cron(crons, text):
    head = text[:1000]
    for sc in crons:
        if not sc["prefix"]:
            continue
        if sc["cut"] and text.startswith(sc["prefix"]):
            return True
        if not sc["cut"] and head == sc["prefix"]:
            return True
    return False


# commandClass's tables, in Go's words (command.go).
TEST_PROGRAMS = {"pytest", "py.test", "tox", "nox", "jest", "vitest", "mocha", "ava", "rspec", "phpunit", "ctest"}
TEST_MODULES = {"pytest", "unittest", "nose2", "tox"}
TEST_VERBS = {"go": {"test"}, "cargo": {"test", "nextest"}, "npm": {"test", "t"}, "yarn": {"test"},
              "pnpm": {"test"}, "bun": {"test"}, "deno": {"test"}, "dotnet": {"test"}, "mvn": {"test", "verify"},
              "gradle": {"test", "check"}, "gradlew": {"test", "check"}, "make": {"test", "check"},
              "rake": {"test", "spec"}, "mix": {"test"}, "swift": {"test"}}
BUILD_PROGRAMS = {"tsc", "gcc", "g++", "clang", "clang++", "cc", "c++", "javac", "rustc", "ninja", "webpack",
                  "esbuild", "msbuild"}
BUILD_VERBS = {"go": {"build", "install", "vet"}, "cargo": {"build", "check", "clippy"},
               "npm": {"run-script build", "build"}, "yarn": {"build"}, "pnpm": {"build"}, "bun": {"build"},
               "dotnet": {"build", "publish"}, "mvn": {"compile", "package", "install"},
               "gradle": {"build", "assemble"}, "gradlew": {"build", "assemble"}, "make": {"build", "all"},
               "cmake": {"--build"}, "swift": {"build"}, "zig": {"build"}, "bazel": {"build"},
               "docker": {"build"}, "vite": {"build"}}
WRAPPERS = {"sudo", "env", "time", "nice", "nohup", "command", "exec"}


def is_assignment(w):
    eq = w.find("=")
    if eq <= 0:
        return False
    for i, c in enumerate(w[:eq]):
        if not (c == "_" or (c.isascii() and c.isalpha()) or (i > 0 and c.isascii() and c.isdigit())):
            return False
    return True


def program(w):
    w = w.replace("\\", "/").rstrip("/").rsplit("/", 1)[-1] or "/"
    for suffix in (".exe", ".bat"):
        if w.endswith(suffix):
            w = w[:-len(suffix)]
    return w


def program_class(p, args):
    if p in TEST_PROGRAMS:
        return "test"
    if p in BUILD_PROGRAMS:
        return "build"
    if p in ("python", "python3", "py"):
        for i, a in enumerate(args):
            if a == "-m" and i + 1 < len(args):
                return "test" if args[i + 1] in TEST_MODULES else "bash"
            if not a.startswith("-") or a == "-c":
                return "bash"
        return "bash"
    verb = args[0] if args else ""
    if p == "npm" and verb == "run" and len(args) > 1:
        verb = "run-script " + args[1]
    if verb in TEST_VERBS.get(p, ()) or (p == "npm" and verb.startswith("run-script test")):
        return "test"
    if verb in BUILD_VERBS.get(p, ()) or (p == "make" and (verb == "" or verb.startswith("-"))):
        return "build"
    return "bash"


def class_of(words):
    i = 0
    while i < len(words):
        w = words[i]
        if is_assignment(w):
            i += 1
        elif w in WRAPPERS:
            i += 1
            while i < len(words) and words[i].startswith("-"):
                i += 1
        elif w == "timeout":
            i += 1
            while i < len(words) and words[i].startswith("-"):
                i += 1
            i += 1
        else:
            return program_class(program(w), words[i + 1:])
    return "bash"


def go_fields(s):
    """strings.Fields: split at Go's unicode.IsSpace."""
    out, cur = [], []
    for c in s:
        if go_is_space(c):
            if cur:
                out.append("".join(cur))
                cur = []
        else:
            cur.append(c)
    if cur:
        out.append("".join(cur))
    return out


def command_class(cmd):
    cls = "bash"
    simple, cur = [], []
    for c in cmd:
        if c in "\n;&|":
            if cur:
                simple.append("".join(cur))
            cur = []
        else:
            cur.append(c)
    if cur:
        simple.append("".join(cur))
    for s in simple:
        k = class_of(go_fields(s))
        if k == "test":
            return "test"
        if k == "build":
            cls = "build"
    return cls


def exit_line(msg):
    """exitLine: the "Exit code N" line Claude Code puts first in the error
    of a command that ran and failed, as N and the rest."""
    first, _, rest = msg.partition("\n")
    if not first.startswith("Exit code "):
        return None
    v = first[len("Exit code "):]
    if not re.fullmatch(r"[+-]?[0-9]+", v):
        return None
    n = int(v)
    if not -(1 << 63) <= n < (1 << 63):
        return None
    return n, rest


def count_lines(text):
    if not text:
        return 0
    n = text.count("\n")
    return n if text.endswith("\n") else n + 1


def list_item(line):
    l = line.lstrip(" \t")
    if len(l) >= 2 and l[0] in "-*+" and l[1] == " ":
        return True
    n = 0
    while n < len(l) and n < 3 and "0" <= l[n] <= "9":
        n += 1
    return n > 0 and n + 1 < len(l) and l[n] in ".)" and l[n + 1] == " "


def output_of(msg):
    if "```" in msg or "~~~" in msg:
        raise SystemExit("a fenced block: this script leaves CommonMark fences to the Go tests")
    words = len(ledger_ref.words_of(msg))
    asks = False
    for line in reversed(msg.split("\n")):
        l = line.rstrip(" \t\r*_`\"')]}\u201d\u2019")
        if l.strip() == "" or list_item(line):
            continue
        asks = l.endswith("?") or l.endswith("\uff1f")
        break
    if asks:
        raise SystemExit("a question: this script leaves offers_options to the Go tests")
    return {"words": words, "code_lines": 0, "asks": False, "offers": False}


def slash_path(p):
    if len(p) >= 3 and p[0].isascii() and p[0].isalpha() and p[1] == ":" and p[2] in "\\/":
        raise SystemExit("a Windows path: this script reads a Linux capture")
    if p.startswith("/") and "\\" not in p:
        return os.path.normpath(p).replace("//", "/")
    return None


def ledger_path(root, file):
    p = slash_path(file)
    if p is None:
        return None
    r = slash_path(root) if root else None
    if r and r != "/" and p.startswith(r + "/") and len(p) > len(r) + 1:
        p = p[len(r) + 1:]
    try:
        ledger_ref.check_path_form(p)
    except ledger_ref.LedgerError:
        return None
    return p


class Symbols:
    def __init__(self):
        self.m = {}

    def of(self, family, value):
        fam = self.m.setdefault(family, {})
        if value not in fam:
            fam[value] = "%s-%d" % (family, len(fam) + 1)
        return fam[value]


def main(capture, out_path):
    hooks = os.path.join(capture, "hooks")
    sym = Symbols()
    sessions = {}
    lines = []
    for d in sorted(os.listdir(hooks)):
        cdir = os.path.join(hooks, d)
        stdin = open(os.path.join(cdir, "stdin"), encoding="utf-8").read()
        j = json.loads(stdin)
        argv = open(os.path.join(cdir, "argv"), encoding="utf-8").read().split()
        name = j.get("hook_event_name") or argv[-1]
        if name != argv[-1]:
            raise SystemExit("%s: --event and hook_event_name differ" % d)
        if name not in OWN:
            raise SystemExit("%s: an event pairtime doesn't install: %s" % (d, name))
        unknown = set(j) - COMMON - OWN[name]
        if unknown:
            raise SystemExit("%s: fields the adapter doesn't know: %s" % (d, sorted(unknown)))
        ns = None
        for line in open(os.path.join(cdir, "time"), encoding="utf-8"):
            if line.startswith("unix_ns="):
                ns = int(line.split("=", 1)[1])
        t = ns // 1_000_000
        parents = parents_of(open(os.path.join(cdir, "parents"), encoding="utf-8").read())
        parent_class, has_tty = lineage(parents)
        sid = j["session_id"]
        s = sessions.setdefault(sid, {"hook_start": False, "started": [], "named": {}, "turn": "", "crons": []})
        cwd = j.get("cwd", "")
        env = {"t": t, "source": "hook", "via": "hook", "harness": "claude_code",
               "session_key": ("session", sid), "project_key": ("project", cwd)}

        recs = []

        def add(kind, data, text=None, command="", edits=None):
            recs.append({"kind": kind, "data": data, "text": text, "command": command, "edits": edits or []})

        if not s["hook_start"] and name != "SessionStart":
            add("session.start", {"start_kind": "startup", "entrypoint": "unknown",
                                  "parent_class": parent_class, "has_tty": has_tty})
            s["hook_start"] = True

        def turn(pid):
            return ("turn", (sid, pid))

        def note(pid):
            # When a call first named each turn, by the calls' own times
            # (claudecode.go, session.named).
            if pid not in s["started"]:
                s["started"].append(pid)
                if len(s["started"]) > MAX_TURNS:
                    s["named"].pop(s["started"][0], None)
                    s["started"] = s["started"][1:]
                s["named"][pid] = t
            elif pid in s["named"] and t < s["named"][pid]:
                s["named"][pid] = t
            s["turn"] = pid

        if j.get("prompt_id") and name != "UserPromptSubmit":
            # Every call names its turn, and a turn that took up a queued
            # message names its own only in calls like this (E-cc-queued-turn).
            note(j["prompt_id"])

        if name == "SessionStart":
            data = {"start_kind": START_KINDS[j["source"]], "entrypoint": "unknown",
                    "parent_class": parent_class, "has_tty": has_tty}
            if j.get("permission_mode"):
                data["permission_mode"] = j["permission_mode"]
            s["hook_start"] = True
            add("session.start", data)
        elif name == "UserPromptSubmit":
            text, pid = j["prompt"], j.get("prompt_id", "")
            n = len(text)
            # Rules 1 and 3 read a queued message's text too (E-cc-queued-turn).
            cron = matches_cron(s["crons"], text)
            notification = text.lstrip(" \t\r\n").startswith("<task-notification>")
            if pid and pid in s["started"] and (pid not in s["named"] or s["named"][pid] < t):
                # A person's message queued in the running turn: an earlier
                # call, by the calls' own times, named its prompt_id. A scheduled
                # prompt or a task notification with a known prompt_id is a
                # parse failure that makes no record.
                if not (cron or notification):
                    add("agent.queue", {"turn_key": turn(pid), "len_chars": n})
            else:
                if not pid:
                    raise SystemExit("%s: a prompt without prompt_id" % d)
                note(pid)
                data = {"turn_key": turn(pid), "len_chars": n, "pasted_chars": 0, "typed_len_est": n,
                        "origin_declared": "none", "matches_pending_cron": cron,
                        "answers_question": False, "low_effort": low_effort(text)}
                sc = slash_command(text)
                if sc:
                    data["slash_command"] = sc
                if notification:
                    data["origin_declared"] = "subagent"
                add("prompt.submit", data, text=text)
        elif name in ("Stop", "StopFailure"):
            pid = j.get("prompt_id") or s["turn"]
            key = turn(pid)
            reason = "end_turn"
            msg = j.get("last_assistant_message") or ""
            if msg and name == "Stop":
                # StopFailure's is the API error's text, not Claude's reply.
                o = output_of(msg)
                add("agent.output", {"turn_key": key, "words": o["words"], "code_lines": o["code_lines"],
                                     "is_final": True, "asks_question": o["asks"],
                                     "offers_options": o["offers"]}, text=msg)
            data = {"turn_key": key, "stop_reason": reason, "stop_hook_active": bool(j.get("stop_hook_active"))}
            if name == "StopFailure":
                data["stop_reason"] = "limit" if j.get("error") in ("rate_limit", "max_output_tokens") else "error"
            else:
                s["crons"] = []
                pending = []
                for cr in j.get("session_crons") or []:
                    sc = scheduled(cr["prompt"])
                    s["crons"].append(sc)
                    pending.append({"prefix_hash": ("cron", (sid, sc["prefix"])), "schedule": cr["schedule"]})
                if pending:
                    data["pending_crons"] = pending
            add("agent.stop", data)
        elif name in ("PostToolUse", "PostToolUseFailure"):
            tool = j["tool_name"]
            tin, tresp = j.get("tool_input") or {}, j.get("tool_response") or {}
            failed = name == "PostToolUseFailure"
            if failed:
                # No tool_response, only the error's text, which is what
                # Claude Code shows for the call.
                tresp = {"stdout": j.get("error") or ""}
            dur = j.get("duration_ms")
            data = {"tool_class": "other", "lines_added": 0, "lines_removed": 0, "visible_output_lines": 0,
                    "t_start": t - dur if dur is not None else t, "t_end": t}
            command, edits = "", []
            if tool in ("Edit", "MultiEdit", "NotebookEdit", "Write") and failed:
                # A failed edit changed nothing: no file, no lines.
                data["tool_class"] = "write" if tool == "Write" else "edit"
            elif tool in ("Edit", "MultiEdit", "NotebookEdit", "Write"):
                data["tool_class"] = "write" if tool == "Write" else "edit"
                lp = ledger_path(cwd, tresp.get("filePath") or tin.get("file_path", ""))
                if lp is None:
                    raise SystemExit("%s: a path this script can't key" % d)
                data["files"] = [("file", lp)]
                for h in tresp.get("structuredPatch") or []:
                    for (os_, ol, ns_, nl) in ledger_ref.hunks_from_patch(h["oldStart"], h["oldLines"],
                                                                         h["newStart"], h["newLines"], h["lines"]):
                        edits.append({"path": lp, "old_start": os_, "old_lines": ol, "new_start": ns_,
                                      "new_lines": nl})
                if not tresp.get("structuredPatch") and tresp.get("type") == "create":
                    h = ledger_ref.hunk_from_texts("", tresp.get("content") or tin.get("content", ""))
                    if h is not None:
                        edits.append({"path": lp, "old_start": h[0], "old_lines": h[1], "new_start": h[2],
                                      "new_lines": h[3]})
                data["lines_added"] = sum(e["new_lines"] for e in edits)
                data["lines_removed"] = sum(e["old_lines"] for e in edits)
            elif tool == "Bash":
                if failed and j.get("is_interrupt"):
                    # An abort: the text is Claude Code's, not the command's.
                    tresp["stdout"] = ""
                elif failed and exit_line(tresp["stdout"]) is not None:
                    data["exit_code"], tresp["stdout"] = exit_line(tresp["stdout"])
                command = tin["command"]
                data["tool_class"] = command_class(command)
                stdout, stderr = tresp.get("stdout", ""), tresp.get("stderr", "")
                data["visible_output_lines"] = count_lines(stdout) + count_lines(stderr)
                ct = ledger_ref.command_text(command)
                if ct:
                    data["cmd_hash"] = ("command", ct)
                out = stdout + ("\n" + stderr if stderr else "")
                es = ledger_ref.error_signature(out)
                if es is not None:
                    data["error_sig"] = ("error", es)
            if j.get("agent_id"):
                data["visible_output_lines"] = 0  # a subagent's output isn't shown (SM 2.4)
            add("agent.tool", data, command=command, edits=edits)
        elif name in ("SubagentStart", "SubagentStop"):
            add("agent.subagent", {"phase": "start" if name == "SubagentStart" else "stop",
                                   "agent_key": ("agent", (sid, j["agent_id"]))})
        elif name == "SessionEnd":
            data = {}
            if j.get("reason"):
                data["reason"] = j["reason"]
            add("session.end", data)
        # PermissionRequest and Notification make no record
        # (E-6.1-permission; SM 1.2 has no kind for a notification).

        for r in recs:
            ev = dict(env)
            ev["kind"] = r["kind"]
            ev["data"] = r["data"]
            lines.append((d, ev, r))

    out = []
    for d, ev, r in lines:
        # Symbols in the order Go's Normalize visits fields: each record's
        # keys in sorted order, depth first, records in order.
        def resolve(v, key=None):
            if isinstance(v, tuple):
                return sym.of(v[0], json.dumps(v[1]))
            if isinstance(v, dict):
                return {k: resolve(v[k], k) for k in sorted(v)}
            if isinstance(v, list):
                return [resolve(x) for x in v]
            return v
        event = resolve(ev)
        line = {"from": d, "event": event}
        if r["text"] is not None:
            line["text"] = r["text"]
        if r["command"]:
            line["command"] = r["command"]
        if r["edits"]:
            line["edits"] = r["edits"]
        out.append(json.dumps(line, ensure_ascii=False, sort_keys=True, separators=(",", ":")))
    with open(out_path, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(out) + "\n")
    print("wrote %d records to %s" % (len(out), out_path))


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    main(sys.argv[1], sys.argv[2])
