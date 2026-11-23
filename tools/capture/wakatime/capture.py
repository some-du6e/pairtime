#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Record what the real wakatime-cli sends to the servers it reports to.

    python3 -I capture.py CLI OUT --ad1 DIR

e2e-wakatime.yml's capture job runs this on a CI runner, and its output
becomes testdata/fixtures/wakatime/<version>/linux/ (docs/PLAN.md, WP-AD16;
DESIGN 5.6, 5.7). CLI is the wakatime-cli binary, OUT an empty folder, and
DIR WP-AD1's Claude Code capture, testdata/fixtures/claude_code/2.1.327/
linux/cli, whose transcripts the ai-sync run gives wakatime-cli to parse.

Two recording servers listen on loopback, in the roles DESIGN 5.6 gives
them: a stand-in for Hackatime at http://127.0.0.2:7248/api/hackatime/v1,
which wakatime-cli has as its api_url, and a stand-in for the pairtime
agent at http://127.0.0.1:7247/api/v1, reached through the [api_urls]
line pairtime's installer adds. wakatime-cli sends to each destination in
the order of its "url|key" string, so these addresses keep the real order,
where http://127.0.0.1 comes before https://hackatime.hackclub.com.

Each run writes OUT/<run>/:

    capture.json        the config files wakatime-cli read, and every call:
                        its arguments, its stdin, its exit code, and the
                        numbers of the requests it made
    requests/NNNNNN.json  a request body as it arrived, byte for byte
    requests/NNNNNN.meta  the request: when it came, which server took it,
                        method, path and query, the values of the headers
                        that carry no secret, the names of all of them, how
                        it authenticated, and the status it got back
    logs/               wakatime-cli's own log, for reading a failed run;
                        not part of a fixture, because it holds the keys

The runs:

    dual-send   simulated editor plugin calls with the Hackatime installer's
                settings and pairtime's [api_urls] line: navigation, edits,
                a save, a test file, a doc, another category, a non-file
                entity and a plugin's batch of extra heartbeats, with
                heartbeat_rate_limit_seconds = 30, so wakatime-cli queues
                and batches the way it does for a person
    replay      the stand-in Hackatime fails one batch after the stand-in
                pairtime took it; wakatime-cli queues the whole batch, and
                the next call sends it to both again, as it does when
                Hackatime is down
    ai-sync     AD1's Claude Code transcripts in CLAUDE_CONFIG_DIR, with no
                ai_logs_last_parsed_at, and one plugin call whose extra
                heartbeats sit around the agent's edits, so the requests
                hold the transcript-derived heartbeats, editor heartbeats
                that wakatime-cli relabels ai coding, and one it drops
    local-only  no other server: api_url is pairtime's, as the installer
                writes it when there is no ~/.wakatime.cfg (DESIGN 5.6),
                with a heartbeat and the status bar's GET of today

The keys are fixed values of the right shape, written here, so the requests
carry no secret. HOME must be /home/capture, the home folder testdata/
fixtures/README.md gives captures and AD1's transcripts name, so the paths
in the requests are the fixtures' own and the files the agent edited exist
where wakatime-cli looks for them: it drops a file heartbeat whose file
isn't there. Standard library only, Python 3.8+.
"""

import argparse
import base64
import datetime
import http.server
import json
import os
import shutil
import subprocess
import sys
import threading
import time
import urllib.parse

# Keys in wakatime-cli's format (DESIGN 5.6). They are public test values.
PAIRTIME_KEY = "00000000-0000-4000-8000-00000000a9e7"
HACKATIME_KEY = "00000000-0000-4000-8000-0000000048c1"

PAIRTIME = ("pairtime", "127.0.0.1", 7247, "/api/v1")
HACKATIME = ("hackatime", "127.0.0.2", 7248, "/api/hackatime/v1")

# A made-up plugin string in VS Code's form: editor/version plugin/version.
PLUGIN = "vscode/1.106.3 vscode-wakatime/25.5.1"

HOME = "/home/capture"
ROOT = HOME + "/pairtime-capture"

# The request headers whose values are kept. Every other header keeps only
# its name, as tools/capture/otlp_sink.py does, because headers can carry
# keys; Authorization is described instead (auth= and key= in the .meta).
KEPT_HEADERS = ["Content-Type", "Accept", "Accept-Encoding", "User-Agent", "Timezone", "X-Machine-Name"]

MAX_BODY = 8 << 20


class Recorder:
    """Numbers the requests of the current run and writes them. One lock,
    so the two servers never give two requests one number."""

    def __init__(self):
        self.lock = threading.Lock()
        self.dir = None
        self.n = 0
        self.fail_hackatime_bulk = 0

    def start(self, run_dir):
        with self.lock:
            self.dir = os.path.join(run_dir, "requests")
            os.makedirs(self.dir, exist_ok=True)
            self.n = 0

    def count(self):
        with self.lock:
            return self.n

    def take_failure(self, server):
        """Whether this request is one the run fails on purpose."""
        with self.lock:
            if server == "hackatime" and self.fail_hackatime_bulk > 0:
                self.fail_hackatime_bulk -= 1
                return True
            return False

    def save(self, meta, body):
        with self.lock:
            self.n += 1
            stem = os.path.join(self.dir, "%06d" % self.n)
            if body is not None:
                with open(stem + ".json", "wb") as f:
                    f.write(body)
            with open(stem + ".meta", "w", encoding="utf-8", newline="\n") as f:
                for k, v in meta:
                    f.write("%s=%s\n" % (k, str(v).replace("\n", " ")))
            return self.n


def auth_of(headers, query, key):
    """How a request authenticated, and with which of the two keys."""
    keys = {PAIRTIME_KEY: "pairtime", HACKATIME_KEY: "hackatime"}
    value = headers.get("Authorization", "")
    scheme, _, cred = value.partition(" ")
    if scheme.lower() == "basic":
        try:
            decoded = base64.b64decode(cred.strip(), validate=True).decode("utf-8")
        except ValueError:
            return "basic", "unreadable", False
        # wakatime-cli sends the key alone; a client may send key: too.
        token = decoded.split(":", 1)[0]
        return "basic", keys.get(token, "other"), token == key
    if scheme.lower() == "bearer":
        token = cred.strip()
        return "bearer", keys.get(token, "other"), token == key
    if value:
        return "other", "other", False
    token = (query.get("api_key") or [""])[0]
    if token:
        return "query", keys.get(token, "other"), token == key
    return "none", "none", False


def make_handler(recorder, role, out):
    name, _, _, prefix = role
    key = PAIRTIME_KEY if name == "pairtime" else HACKATIME_KEY

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        server_version = "pairtime-wakatime-capture"

        def do_GET(self):
            self.handle_request(None)

        def do_POST(self):
            try:
                length = int(self.headers.get("Content-Length") or 0)
            except ValueError:
                length = -1
            if length < 0 or length > MAX_BODY:
                self.close_connection = True
                self.answer(413, {"error": "body too large"})
                return
            self.handle_request(self.rfile.read(length))

        def handle_request(self, body):
            arrived = time.time_ns()
            url = urllib.parse.urlsplit(self.path)
            query = urllib.parse.parse_qs(url.query)
            auth, which, ok = auth_of(self.headers, query, key)
            meta = [
                ("unix_ns", arrived),
                ("utc", datetime.datetime.fromtimestamp(arrived / 1e9, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")),
                ("server", name),
                ("method", self.command),
                ("path", url.path),
                ("query", "" if not url.query else ",".join(sorted(query))),
            ]
            for h in KEPT_HEADERS:
                meta.append((h.lower().replace("-", "_"), self.headers.get(h, "")))
            meta += [
                ("auth", auth),
                ("key", which),
                ("header_names", ",".join(sorted({k.lower() for k in self.headers.keys()}))),
                ("bytes", 0 if body is None else len(body)),
            ]
            status, reply, note = self.decide(url.path, body, ok)
            meta.append(("status", status))
            if note:
                meta.append(("note", note))
            n = recorder.save(meta, body)
            print("%-9s %06d %s %s -> %d%s" % (name, n, self.command, url.path, status, " (" + note + ")" if note else ""),
                  file=out, flush=True)
            self.answer(status, reply)

        def decide(self, path, body, ok):
            """The status and body the stand-in answers with: Hackatime's
            shapes (testdata/upstream/hackatime), which are what wakatime-cli
            reads."""
            if not path.startswith(prefix + "/"):
                return 404, {"error": "Not found"}, ""
            rest = path[len(prefix):]
            if not ok:
                return 401, {"error": "Unauthorized"}, ""
            if self.command == "GET" and rest == "/users/current/statusbar/today":
                return 200, {"data": {"grand_total": {"text": "1 min", "total_seconds": 60}}}, ""
            if self.command != "POST" or rest not in ("/users/current/heartbeats", "/users/current/heartbeats.bulk"):
                return 404, {"error": "Not found"}, ""
            try:
                doc = json.loads(body.decode("utf-8"))
            except (UnicodeDecodeError, ValueError):
                return 400, {"error": "No data provided..."}, "body isn't JSON"
            items = doc if isinstance(doc, list) else [doc]
            if rest.endswith(".bulk"):
                if recorder.take_failure(name):
                    return 500, {"error": "Internal Server Error"}, "failed on purpose"
                responses = [[self.row(i, h), 201] for i, h in enumerate(items)]
                return 201, {"responses": responses}, ""
            return 202, self.row(0, items[0] if items else {}), ""

        def row(self, i, h):
            h = h if isinstance(h, dict) else {}
            return {"id": recorder.count() * 1000 + i + 1, "entity": h.get("entity"), "type": h.get("type"), "time": h.get("time")}

        def answer(self, status, reply):
            payload = json.dumps(reply).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, fmt, *args):
            pass

    return Handler


def serve(recorder, role, out):
    _, host, port, _ = role
    srv = http.server.ThreadingHTTPServer((host, port), make_handler(recorder, role, out))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


def url(role):
    _, host, port, prefix = role
    return "http://%s:%d%s" % (host, port, prefix)


def write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)


def utc(ts):
    return datetime.datetime.fromtimestamp(ts, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")


def rfc3339(ts):
    return datetime.datetime.fromtimestamp(ts, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class Run:
    """One run: its config files, its calls and its folder."""

    def __init__(self, name, out, cli, recorder, log):
        self.name = name
        self.dir = os.path.join(out, name)
        self.cli = cli
        self.recorder = recorder
        self.log = log
        self.calls = []
        self.config = ""
        self.internal = ""
        self.env = {}
        # Each run starts from a home with no wakatime state: no config, no
        # offline queue, no rate-limit or AI-sync watermark.
        shutil.rmtree(os.path.join(HOME, ".wakatime"), ignore_errors=True)
        if os.path.exists(os.path.join(HOME, ".wakatime.cfg")):
            os.remove(os.path.join(HOME, ".wakatime.cfg"))
        recorder.start(self.dir)
        print("== run %s" % name, file=log, flush=True)

    def configure(self, config, internal=""):
        self.config = config
        self.internal = internal
        write(os.path.join(HOME, ".wakatime.cfg"), config)
        if internal:
            write(os.path.join(HOME, ".wakatime", "wakatime-internal.cfg"), internal)

    def call(self, args, stdin=None, note=""):
        before = self.recorder.count()
        argv = [self.cli] + args + ["--log-file", os.path.join(self.dir, "logs", "wakatime.log"), "--verbose"]
        os.makedirs(os.path.join(self.dir, "logs"), exist_ok=True)
        env = dict(os.environ)
        env["HOME"] = HOME
        env.update(self.env)
        started = time.time()
        r = subprocess.run(argv, input=stdin.encode("utf-8") if stdin is not None else None, env=env, cwd=HOME,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180)
        # Give a request still in flight a moment to be numbered.
        time.sleep(0.3)
        after = self.recorder.count()
        call = {
            "n": len(self.calls) + 1,
            "started_utc": utc(started),
            "args": [a.replace(self.cli, "wakatime-cli") for a in args],
            "exit": r.returncode,
            "requests": list(range(before + 1, after + 1)),
        }
        if stdin is not None:
            call["stdin"] = json.loads(stdin)
        if self.env:
            call["env"] = dict(self.env)
        if note:
            call["note"] = note
        out = r.stdout.decode("utf-8", "replace").strip()
        if out:
            call["stdout"] = out
        self.calls.append(call)
        print("call %d exit %d, requests %s%s" % (call["n"], r.returncode, call["requests"], ": " + out if out else ""),
              file=self.log, flush=True)
        if r.stderr:
            print(r.stderr.decode("utf-8", "replace"), file=self.log, flush=True)
        return call

    def finish(self, version):
        doc = {
            "tool": "wakatime-cli",
            "version": version,
            "run": self.name,
            "home": HOME,
            "servers": {"pairtime": url(PAIRTIME), "hackatime": url(HACKATIME)},
            "keys": {"pairtime": PAIRTIME_KEY, "hackatime": HACKATIME_KEY},
            "wakatime_cfg": self.config,
            "wakatime_internal_cfg": self.internal,
            "calls": self.calls,
        }
        write(os.path.join(self.dir, "capture.json"), json.dumps(doc, indent=2) + "\n")


def plugin_args(entity, *extra):
    return ["--entity", entity, "--plugin", PLUGIN] + list(extra)


def dual_send_config(rate_limit):
    return (
        "[settings]\n"
        "api_url = %s\n"
        "api_key = %s\n"
        "heartbeat_rate_limit_seconds = %d\n"
        "hostname = capture-host\n"
        "\n"
        "[api_urls]\n"
        ".+ = %s|%s\n"
    ) % (url(HACKATIME), HACKATIME_KEY, rate_limit, url(PAIRTIME), PAIRTIME_KEY)


def seeded_internal(now):
    """What hackatime-setup and pairtime's installer seed, so AI sync starts
    now instead of from 2025-02-24 (HC 1.5)."""
    return "[internal]\nai_logs_last_parsed_at = %s\n" % rfc3339(now)


def project(name):
    """A scratch project made by the capture kit's prepare.py, so it holds
    the same files as AD1's."""
    sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    import prepare  # noqa: E402  tools/capture/prepare.py
    path = os.path.join(ROOT, "projects", name)
    prepare.make_project(path)
    return path


def run_dual_send(out, cli, recorder, log, version):
    r = Run("dual-send", out, cli, recorder, log)
    p = project("dual-send")
    r.configure(dual_send_config(30), seeded_internal(time.time()))
    f = os.path.join(p, "wordcount.py")
    # The first call sends at once; the next ones fall inside the 30 s rate
    # limit and wait in the offline queue.
    r.call(plugin_args(f, "--lineno", "1", "--cursorpos", "1", "--lines-in-file", "6"), note="opened the file: no change, no save")
    time.sleep(2)
    r.call(plugin_args(f, "--lineno", "6", "--cursorpos", "31", "--lines-in-file", "6", "--human-line-changes", "1"), note="typed a line")
    time.sleep(2)
    r.call(plugin_args(f, "--lineno", "6", "--cursorpos", "31", "--lines-in-file", "6", "--write"), note="saved")
    time.sleep(2)
    r.call(plugin_args(os.path.join(p, "test_wordcount.py"), "--lineno", "9", "--cursorpos", "5", "--human-line-changes", "2"),
           note="typed in a test file")
    time.sleep(2)
    r.call(plugin_args(os.path.join(p, "README.md"), "--lineno", "3"), note="moved to a doc")
    time.sleep(2)
    r.call(plugin_args(f, "--category", "debugging", "--lineno", "6"), note="a debug session")
    time.sleep(2)
    r.call(["--entity", "terminal", "--entity-type", "app", "--plugin", PLUGIN, "--category", "running tests"],
           note="a non-file entity")
    # Past the rate limit, a call sends its heartbeat and then the queue.
    time.sleep(31)
    t = time.time()
    extra = [
        {"entity": f, "type": "file", "time": t - 20, "lineno": 2, "cursorpos": 4, "lines": 6, "human_line_changes": 1},
        {"entity": f, "type": "file", "time": t - 10, "lineno": 2, "cursorpos": 4, "lines": 6, "is_write": True},
        {"entity": os.path.join(p, "test_wordcount.py"), "type": "file", "time": t - 5, "lineno": 12, "cursorpos": 1},
    ]
    r.call(plugin_args(f, "--lineno", "2", "--cursorpos", "4", "--lines-in-file", "6", "--extra-heartbeats"),
           stdin=json.dumps(extra), note="past the rate limit, with a plugin's batch of three extra heartbeats")
    r.finish(version)


def run_replay(out, cli, recorder, log, version):
    r = Run("replay", out, cli, recorder, log)
    p = project("replay")
    r.configure(dual_send_config(0), seeded_internal(time.time()))
    f = os.path.join(p, "wordcount.py")
    recorder.fail_hackatime_bulk = 1
    r.call(plugin_args(f, "--lineno", "3", "--cursorpos", "8", "--lines-in-file", "6", "--human-line-changes", "1"),
           note="pairtime takes the batch, then Hackatime answers 500, so wakatime-cli queues the whole batch")
    time.sleep(2)
    r.call(plugin_args(f, "--lineno", "3", "--cursorpos", "9", "--lines-in-file", "6", "--write"),
           note="both take this heartbeat, then the queued one goes to both again")
    r.call(["--offline-count"], note="the queue is empty again")
    r.finish(version)


def transcript_edits(transcripts):
    """The times and files of the agent's file edits in AD1's transcripts,
    and every file path they name."""
    edits, paths = [], set()
    for dirpath, _, names in os.walk(transcripts):
        for n in names:
            if not n.endswith(".jsonl"):
                continue
            with open(os.path.join(dirpath, n), encoding="utf-8") as f:
                for line in f:
                    try:
                        d = json.loads(line)
                    except ValueError:
                        continue
                    res = d.get("toolUseResult")
                    if not isinstance(res, dict):
                        continue
                    fp = res.get("filePath")
                    if not fp and isinstance(res.get("file"), dict):
                        fp = res["file"].get("filePath")
                    if not isinstance(fp, str) or not fp:
                        continue
                    paths.add(fp)
                    if res.get("structuredPatch") or res.get("type") == "create":
                        ts = datetime.datetime.strptime(d["timestamp"], "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=datetime.timezone.utc)
                        edits.append((ts.timestamp(), fp))
    return sorted(edits), paths


def run_ai_sync(out, cli, recorder, log, version, ad1):
    r = Run("ai-sync", out, cli, recorder, log)
    # AD1's transcripts sit under transcripts/home/ by their path below the
    # capture's home folder, which is this one.
    src = os.path.join(ad1, "transcripts", "home")
    for dirpath, _, names in os.walk(src):
        for n in names:
            rel = os.path.relpath(os.path.join(dirpath, n), src)
            dst = os.path.join(HOME, rel)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            shutil.copyfile(os.path.join(dirpath, n), dst)
    p = project("cli")
    edits, paths = transcript_edits(os.path.join(ROOT, "claude-home"))
    # wakatime-cli drops a heartbeat whose file isn't on disk, and the agent
    # made some of these files during AD1's run.
    for fp in sorted(paths):
        if fp.startswith(p + os.sep) and not os.path.exists(fp):
            write(fp, "")
    r.env = {"CLAUDE_CONFIG_DIR": os.path.join(ROOT, "claude-home")}
    # No ai_logs_last_parsed_at, so the sync reads the transcripts from the
    # start (DESIGN 5.6 seeds one only where it writes the config).
    r.configure(dual_send_config(0))
    wc = os.path.join(p, "wordcount.py")
    first = next(t for t, fp in edits if fp == wc)
    last = max(t for t, _ in edits)
    extra = [
        {"entity": wc, "type": "file", "time": first + 2, "lineno": 6, "cursorpos": 3, "lines": 6, "human_line_changes": 1},
        {"entity": wc, "type": "file", "time": first + 30, "lineno": 6, "cursorpos": 9, "lines": 6, "human_line_changes": 2},
        {"entity": os.path.join(p, "test_wordcount.py"), "type": "file", "time": first + 45, "lineno": 4, "cursorpos": 1},
        {"entity": wc, "type": "file", "time": first + 200, "lineno": 7, "cursorpos": 2, "lines": 7, "human_line_changes": 1},
        {"entity": wc, "type": "file", "time": last + 31 * 60, "lineno": 7, "cursorpos": 2, "lines": 7, "human_line_changes": 3,
         "is_write": True},
    ]
    r.call(plugin_args(wc, "--lineno", "7", "--cursorpos", "2", "--lines-in-file", "7", "--extra-heartbeats"),
           stdin=json.dumps(extra),
           note="extra heartbeats at the agent's first edit of wordcount.py +2 s, +30 s, +45 s (another file), +200 s, "
                "and 31 minutes after its last edit")
    r.call(["--offline-count"], note="what is left in the queue")
    r.finish(version)


def run_local_only(out, cli, recorder, log, version):
    r = Run("local-only", out, cli, recorder, log)
    p = project("local-only")
    now = time.time()
    r.configure(
        "[settings]\napi_url = %s\napi_key = %s\nhostname = capture-host\n" % (url(PAIRTIME), PAIRTIME_KEY),
        seeded_internal(now),
    )
    f = os.path.join(p, "wordcount.py")
    r.call(plugin_args(f, "--lineno", "4", "--cursorpos", "2", "--lines-in-file", "6", "--human-line-changes", "1"),
           note="a heartbeat to pairtime alone")
    r.call(["--today", "--plugin", PLUGIN], note="the status bar's text for today")
    r.call(["--today", "--plugin", PLUGIN, "--output", "json"], note="the same as JSON")
    r.finish(version)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("cli", help="the wakatime-cli binary")
    ap.add_argument("out", help="an empty folder for the runs")
    ap.add_argument("--ad1", required=True, help="testdata/fixtures/claude_code/2.1.327/linux/cli")
    ap.add_argument("--runs", default="dual-send,replay,ai-sync,local-only", help="which runs, comma-separated")
    a = ap.parse_args(argv)
    if os.environ.get("HOME") != HOME:
        sys.exit("HOME must be %s, the home folder the fixtures name" % HOME)
    cli = os.path.abspath(a.cli)
    out = os.path.abspath(a.out)
    ad1 = os.path.abspath(a.ad1)
    if os.path.exists(out) and os.listdir(out):
        sys.exit("%s isn't empty" % out)
    os.makedirs(out, exist_ok=True)
    version = subprocess.run([cli, "--version"], stdout=subprocess.PIPE, check=True).stdout.decode().strip()
    recorder = Recorder()
    servers = [serve(recorder, PAIRTIME, sys.stdout), serve(recorder, HACKATIME, sys.stdout)]
    runs = {
        "dual-send": lambda: run_dual_send(out, cli, recorder, sys.stdout, version),
        "replay": lambda: run_replay(out, cli, recorder, sys.stdout, version),
        "ai-sync": lambda: run_ai_sync(out, cli, recorder, sys.stdout, version, ad1),
        "local-only": lambda: run_local_only(out, cli, recorder, sys.stdout, version),
    }
    try:
        for name in a.runs.split(","):
            runs[name]()
    finally:
        for s in servers:
            s.shutdown()
    with open("/etc/os-release", encoding="utf-8") as f:
        osr = dict(line.rstrip("\n").split("=", 1) for line in f if "=" in line)
    write(os.path.join(out, "SYSTEM.txt"), "os=%s\nkernel=%s\nwakatime_cli=%s\n" % (
        osr.get("PRETTY_NAME", "").strip('"'), os.uname().release, version))
    print("done: %s" % out, flush=True)


if __name__ == "__main__":
    main()
