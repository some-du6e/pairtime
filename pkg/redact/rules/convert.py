#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Convert gitleaks' default rules into the JSON that pkg/redact embeds.

Needs Python 3.11 or later and nothing else:

    python3 -I pkg/redact/rules/convert.py --check   # fail if gitleaks.json.gz is stale
    python3 -I pkg/redact/rules/convert.py --write   # rewrite gitleaks.json.gz

The input is gitleaks.toml.gz: gitleaks' config/gitleaks.toml at the tag
below, byte for byte, then gzipped (README.md says why). The script refuses
any other bytes and any key it doesn't know, so a gitleaks feature the Go
side doesn't implement can't be dropped without anyone noticing.
"""

import argparse
import gzip
import hashlib
import json
import os
import sys
import tomllib

TAG = "v8.30.1"
COMMIT = "83d9cd684c87d95d656c1458ef04895a7f1cbd8e"
SHA256 = "e163e53b9e7e8a8511e77271e2b323ed057759542a6d988258afe3a1fa329caf"

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(HERE, "gitleaks.toml.gz")
OUT = os.path.join(HERE, "gitleaks.json.gz")

# The keys gitleaks' config loader (config/config.go, ViperConfig) reads,
# minus the ones pkg/redact doesn't implement: extend, required, skipReport
# and an allowlist's targetRules. The pinned file uses none of those.
TOP_KEYS = {"title", "description", "minVersion", "allowlist", "allowlists", "rules"}
RULE_KEYS = {
    "id", "description", "regex", "path", "secretGroup", "entropy",
    "keywords", "tags", "allowlist", "allowlists",
}
ALLOWLIST_KEYS = {
    "description", "condition", "commits", "paths", "regexTarget",
    "regexes", "stopwords",
}


def fail(msg):
    sys.exit("convert.py: " + msg)


def check_keys(where, table, allowed):
    if not isinstance(table, dict):
        fail("%s: want a table" % where)
    extra = sorted(set(table) - allowed)
    if extra:
        fail("%s: unsupported key(s): %s" % (where, ", ".join(extra)))


def strings(where, value):
    if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
        fail("%s: want a list of strings" % where)
    return list(value)


def number(where, value, kind):
    if isinstance(value, bool) or not isinstance(value, kind):
        fail("%s: want a number" % where)
    return value


def allowlist(where, a):
    """One allowlist, normalized the way gitleaks' parseAllowlist does."""
    check_keys(where, a, ALLOWLIST_KEYS)
    cond = a.get("condition", "").upper()
    if cond in ("", "OR", "||"):
        cond = "OR"
    elif cond in ("AND", "&&"):
        cond = "AND"
    else:
        fail("%s: unknown condition %r" % (where, a["condition"]))
    target = a.get("regexTarget", "")
    if target == "secret":
        target = ""
    if target not in ("", "match", "line"):
        fail("%s: unknown regexTarget %r" % (where, target))
    out = {
        "condition": cond,
        "regex_target": target,
        "regexes": strings(where + ".regexes", a.get("regexes", [])),
        "stopwords": strings(where + ".stopwords", a.get("stopwords", [])),
        "paths": strings(where + ".paths", a.get("paths", [])),
        "commits": strings(where + ".commits", a.get("commits", [])),
    }
    if not (out["regexes"] or out["stopwords"] or out["paths"] or out["commits"]):
        fail("%s: an allowlist needs commits, paths, regexes or stopwords" % where)
    return out


def convert(toml_bytes):
    digest = hashlib.sha256(toml_bytes).hexdigest()
    if digest != SHA256:
        fail("gitleaks.toml.gz holds a file with SHA-256 %s, want %s, "
             "gitleaks %s config/gitleaks.toml" % (digest, SHA256, TAG))
    cfg = tomllib.loads(toml_bytes.decode("utf-8"))
    check_keys("top level", cfg, TOP_KEYS)

    if "allowlist" in cfg:
        if "allowlists" in cfg:
            fail("[allowlist] and [[allowlists]] can't be used together")
        lists = [cfg["allowlist"]]
    else:
        lists = cfg.get("allowlists", [])
    global_lists = [allowlist("global allowlist %d" % i, a) for i, a in enumerate(lists)]

    rules, seen = [], set()
    for i, r in enumerate(cfg.get("rules", [])):
        where = "rule %d" % i
        check_keys(where, r, RULE_KEYS)
        rid = r.get("id", "")
        where = "rule %s" % rid
        if not rid or rid in seen:
            fail("%s: missing or repeated id" % where)
        seen.add(rid)
        rlists = r.get("allowlists", [])
        if "allowlist" in r:
            if rlists:
                fail("%s: [rules.allowlist] and [[rules.allowlists]] together" % where)
            rlists = [r["allowlist"]]
        if not r.get("regex") and not r.get("path"):
            fail("%s: needs a regex or a path" % where)
        group = number(where + ".secretGroup", r.get("secretGroup", 0), int)
        if group < 0:
            fail("%s: negative secretGroup" % where)
        rules.append({
            "id": rid,
            "description": r.get("description", ""),
            "regex": r.get("regex", ""),
            "path": r.get("path", ""),
            "secret_group": group,
            "entropy": float(number(where + ".entropy", r.get("entropy", 0), (int, float))),
            "keywords": strings(where + ".keywords", r.get("keywords", [])),
            "allowlists": [allowlist("%s allowlist %d" % (where, j), a)
                           for j, a in enumerate(rlists)],
        })

    doc = {
        "source": {
            "repo": "https://github.com/gitleaks/gitleaks",
            "tag": TAG,
            "commit": COMMIT,
            "path": "config/gitleaks.toml",
            "sha256": SHA256,
        },
        "allowlists": global_lists,
        "rules": rules,
    }
    return (json.dumps(doc, indent=1, ensure_ascii=True) + "\n").encode("ascii"), len(rules)


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    mode = p.add_mutually_exclusive_group(required=True)
    mode.add_argument("--check", action="store_true",
                      help="fail unless gitleaks.json.gz holds exactly what --write would write")
    mode.add_argument("--write", action="store_true", help="rewrite gitleaks.json.gz")
    args = p.parse_args()

    with open(SRC, "rb") as f:
        want, n = convert(gzip.decompress(f.read()))
    if args.write:
        with open(OUT, "wb") as f:
            f.write(gzip.compress(want, compresslevel=9, mtime=0))
        print("convert.py: wrote gitleaks.json.gz, %d rules from gitleaks %s" % (n, TAG))
        return
    try:
        with open(OUT, "rb") as f:
            have = gzip.decompress(f.read())
    except FileNotFoundError:
        fail("gitleaks.json.gz is missing; run convert.py --write")
    if have != want:
        fail("gitleaks.json.gz is stale; run python3 -I pkg/redact/rules/convert.py --write")
    print("convert.py: gitleaks.json.gz matches gitleaks %s, %d rules" % (TAG, n))


if __name__ == "__main__":
    main()
