#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Save every OTLP/HTTP request a harness sends, for harness fixtures.

    python3 otlp_sink.py RUN_DIR [--port 4318]

Listens on 127.0.0.1 only, the way pairtime's own receiver will, and writes
each request under RUN_DIR/otlp/ as two files:

    NNNNNN.json   the body, as sent (gunzipped if the exporter compressed it)
    NNNNNN.meta   arrival time, path, content type, encoding, sizes, and the
                  names of the request headers. Header values are never saved,
                  because exporters can carry API keys in them

A body that isn't JSON (OTLP protobuf) is saved as NNNNNN.bin, and the sink
warns: set OTEL_EXPORTER_OTLP_PROTOCOL=http/json, because the scrub can't read
protobuf and won't copy it into a fixture.

Every request gets an empty success reply, so the exporter never retries or
backs off. One line per request on the terminal shows what arrived. Nothing is
forwarded anywhere. Stop it with Ctrl+C. Standard library only, Python 3.8+.

It also keeps the step log: each line you type into its terminal, such as a
SCRIPTS.md step number, is appended to RUN_DIR/STEPS.txt with the time, so
the fixture records which step every hook call and request belongs to.
"""

import argparse
import gzip
import http.server
import ipaddress
import json
import os
import socket
import sys
import threading
import time
import zlib

MAX_BODY = 32 << 20


class Sink:
    """Numbers and writes the requests. One lock, so concurrent exporters
    (logs and metrics arrive on separate connections) never share a number."""

    def __init__(self, run_dir):
        self.dir = os.path.join(run_dir, "otlp")
        os.makedirs(self.dir, exist_ok=True)
        self.lock = threading.Lock()
        # Continue the numbering if the sink is restarted into the same run.
        self.n = len([f for f in os.listdir(self.dir) if f.endswith(".meta")])

    def save(self, arrived_ns, path, headers, body, decoded, note):
        with self.lock:
            self.n += 1
            stem = os.path.join(self.dir, "%06d" % self.n)
            is_json = looks_like_json(decoded)
            ext = ".json" if is_json else ".bin"
            with open(stem + ext, "wb") as f:
                f.write(decoded)
            meta = [
                "unix_ns=%d" % arrived_ns,
                "utc=" + time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(arrived_ns / 1e9)),
                "method=POST",
                "path=" + path,
                "content_type=" + headers.get("Content-Type", ""),
                "content_encoding=" + headers.get("Content-Encoding", ""),
                "bytes_received=%d" % len(body),
                "bytes_saved=%d" % len(decoded),
                "header_names=" + ",".join(sorted({k.lower() for k in headers.keys()})),
            ]
            if note:
                meta.append("note=" + note)
            with open(stem + ".meta", "w", encoding="utf-8", newline="\n") as f:
                f.write("\n".join(meta) + "\n")
            return self.n, ext, is_json


def looks_like_json(data):
    try:
        json.loads(data.decode("utf-8"))
        return True
    except (UnicodeDecodeError, ValueError):
        return False


def summarize(decoded):
    """A few words about what a JSON body holds, for the terminal line."""
    try:
        doc = json.loads(decoded.decode("utf-8"))
    except (UnicodeDecodeError, ValueError):
        return "not JSON: set OTEL_EXPORTER_OTLP_PROTOCOL=http/json"
    counts = {"logs": 0, "metrics": 0, "spans": 0}
    names = []
    for rl in doc.get("resourceLogs", []) or []:
        for sl in rl.get("scopeLogs", []) or []:
            for rec in sl.get("logRecords", []) or []:
                counts["logs"] += 1
                body = (rec.get("body") or {}).get("stringValue")
                if isinstance(body, str) and len(names) < 4:
                    names.append(body[:40])
    for rm in doc.get("resourceMetrics", []) or []:
        for sm in rm.get("scopeMetrics", []) or []:
            for m in sm.get("metrics", []) or []:
                counts["metrics"] += 1
                if len(names) < 4 and isinstance(m.get("name"), str):
                    names.append(m["name"][:40])
    for rs in doc.get("resourceSpans", []) or []:
        for ss in rs.get("scopeSpans", []) or []:
            counts["spans"] += len(ss.get("spans", []) or [])
    parts = ["%d %s" % (v, k) for k, v in counts.items() if v]
    if names:
        parts.append(", ".join(names))
    return "; ".join(parts) or "empty"


def read_chunked(rfile):
    out = bytearray()
    while True:
        line = rfile.readline(1024)
        size = int(line.split(b";", 1)[0].strip() or b"0", 16)
        if size == 0:
            # Trailers, if any, end with an empty line.
            while rfile.readline(1024).strip():
                pass
            return bytes(out)
        if len(out) + size > MAX_BODY:
            raise ValueError("body over %d bytes" % MAX_BODY)
        out += rfile.read(size)
        rfile.readline(16)


def decode_body(body, encoding):
    encoding = (encoding or "").strip().lower()
    if encoding in ("", "identity"):
        return body, ""
    if encoding == "gzip":
        return gzip.decompress(body), "gunzipped"
    if encoding == "deflate":
        return zlib.decompress(body), "inflated"
    return body, "unknown content-encoding %s, saved as sent" % encoding


def make_handler(sink, out):
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        server_version = "pairtime-capture-sink"

        def do_POST(self):
            arrived_ns = time.time_ns()
            try:
                if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                    body = read_chunked(self.rfile)
                else:
                    length = int(self.headers.get("Content-Length") or 0)
                    if length > MAX_BODY:
                        self.reply(413, b"", "text/plain")
                        return
                    body = self.rfile.read(length)
                decoded, note = decode_body(body, self.headers.get("Content-Encoding"))
            except (ValueError, OSError, EOFError, zlib.error) as e:
                print("rejected %s: %s" % (self.path, e), file=out, flush=True)
                self.reply(400, b"", "text/plain")
                return
            n, ext, is_json = sink.save(arrived_ns, self.path, self.headers, body, decoded, note)
            print("%06d%s %-11s %7d bytes  %s" % (n, ext, self.path, len(decoded), summarize(decoded)),
                  file=out, flush=True)
            # An empty Export*ServiceResponse means full success in both encodings.
            if is_json:
                self.reply(200, b"{}", "application/json")
            else:
                self.reply(200, b"", "application/x-protobuf")

        def do_GET(self):
            self.reply(404, b"", "text/plain")

        def reply(self, status, payload, ctype):
            self.send_response(status)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, fmt, *args):
            pass

    return Handler


def log_steps(run_dir, stdin, out):
    """Append each line typed on stdin to RUN_DIR/STEPS.txt, stamped."""
    path = os.path.join(run_dir, "STEPS.txt")
    for line in stdin:
        text = line.strip()
        if not text:
            continue
        now = time.time_ns()
        with open(path, "a", encoding="utf-8", newline="\n") as f:
            f.write("%d\t%s\t%s\n" % (now, time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now / 1e9)), text))
        print("noted step %s" % text, file=out, flush=True)


def loopback(host):
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True


class Server6(Server):
    address_family = socket.AF_INET6


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("run_dir", help="the capture run directory; bodies go to RUN_DIR/otlp/")
    ap.add_argument("--port", type=int, default=4318, help="port to listen on (default 4318, OTLP/HTTP's own; 0 picks a free one)")
    ap.add_argument("--host", default="127.0.0.1", help="loopback address to listen on (default 127.0.0.1)")
    args = ap.parse_args(argv)
    if not loopback(args.host):
        ap.error("--host must be a loopback address: the sink never listens beyond this machine")
    sink = Sink(args.run_dir)
    cls = Server6 if ":" in args.host else Server
    server = cls((args.host, args.port), make_handler(sink, sys.stdout))
    host, port = server.server_address[:2]
    print("listening on %s:%d, saving to %s" % (host, port, sink.dir), flush=True)
    print("point the harness at it with OTEL_EXPORTER_OTLP_ENDPOINT=http://%s:%d and "
          "OTEL_EXPORTER_OTLP_PROTOCOL=http/json; Ctrl+C stops" % (host if ":" not in host else "[%s]" % host, port),
          flush=True)
    if sys.stdin is not None and sys.stdin.isatty():
        print("type a step number and Enter before each step; it goes to STEPS.txt", flush=True)
        threading.Thread(target=log_steps, args=(args.run_dir, sys.stdin, sys.stdout), daemon=True).start()
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    print("stopped after %d requests" % sink.n, flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
