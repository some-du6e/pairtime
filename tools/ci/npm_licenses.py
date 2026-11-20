# SPDX-License-Identifier: AGPL-3.0-or-later
"""Checks the license of every package in npm lockfiles.

    python3 -I tools/ci/npm_licenses.py web/package-lock.json [...]
    python3 -I tools/ci/npm_licenses.py selftest

The npm counterpart of tools/ci/licensecheck, which checks Go modules. It
reads only the lockfile, which records each package's license, so it needs
neither Node nor an install.

- Packages that ship, meaning the "dependencies" closure, which Vite bundles
  into the UI the binaries embed, must be MIT, BSD-2-Clause, BSD-3-Clause,
  Apache-2.0 or ISC, the list DESIGN 3.12 and docs/PLAN.md (ground rule 4)
  give for Go modules.
- Build and test tools ("dev": true in the lockfile) never ship. They may
  also use the other permissive licenses in BUILD_ONLY, and the packages in
  EXCEPTIONS may use the license named there, for the reason given.

SPDX expressions are evaluated: OR needs one allowed side, AND needs both,
and "X WITH exception" counts as X. Exit status 1 means a license isn't
allowed or is missing, and each is printed as a GitHub error annotation.
"""

import json
import re
import sys

SHIPPED = {"MIT", "BSD-2-Clause", "BSD-3-Clause", "Apache-2.0", "ISC"}

# Permissive licenses that are fine for tools that never ship.
BUILD_ONLY = SHIPPED | {"0BSD", "BlueOak-1.0.0", "CC0-1.0", "Python-2.0", "Unlicense"}

# Named build tools with a license outside BUILD_ONLY. Each needs a reason.
EXCEPTIONS = {
    # Vite 8 minifies CSS with lightningcss. MPL-2.0's terms are file-level
    # and apply to lightningcss's own files, which pairtime doesn't ship.
    r"lightningcss(-[a-z0-9-]+)?": (
        "MPL-2.0",
        "Vite's CSS minifier; MPL-2.0 covers its own files, which never ship",
    ),
}

TOKEN = re.compile(r"\s*(\(|\)|[A-Za-z0-9.+:-]+)")


def tokens(expression):
    out, pos = [], 0
    expression = expression.strip()
    while pos < len(expression):
        m = TOKEN.match(expression, pos)
        if not m:
            raise ValueError("can't read the license expression %r" % expression)
        out.append(m.group(1))
        pos = m.end()
    return out


def allowed(expression, licenses):
    """Reports whether the SPDX expression holds with only licenses allowed."""
    toks = tokens(expression)
    pos = [0]

    def peek():
        return toks[pos[0]] if pos[0] < len(toks) else None

    def take():
        tok = peek()
        if tok is None:
            raise ValueError("the license expression %r ends early" % expression)
        pos[0] += 1
        return tok

    def any_of():
        ok = all_of()
        while peek() == "OR":
            take()
            ok = all_of() or ok
        return ok

    def all_of():
        ok = one()
        while peek() == "AND":
            take()
            ok = one() and ok
        return ok

    def one():
        tok = take()
        if tok == "(":
            ok = any_of()
            if take() != ")":
                raise ValueError("unbalanced parentheses in %r" % expression)
            return ok
        if tok in ("AND", "OR", "WITH", ")"):
            raise ValueError("unexpected %r in %r" % (tok, expression))
        if peek() == "WITH":
            take()
            take()
        return tok in licenses

    ok = any_of()
    if peek() is not None:
        raise ValueError("unexpected %r in %r" % (peek(), expression))
    return ok


def problems(lock):
    """Lists every package in a parsed lockfile whose license isn't allowed."""
    out = []
    for path, entry in sorted(lock.get("packages", {}).items()):
        if path == "":
            continue  # the project itself
        name = path.rsplit("node_modules/", 1)[-1]
        what = "%s@%s" % (name, entry.get("version", "?"))
        license = entry.get("license")
        if not isinstance(license, str) or not license.strip():
            out.append("%s has no license in the lockfile; read the package before allowing it" % what)
            continue
        dev = entry.get("dev") is True
        try:
            if allowed(license, SHIPPED):
                continue
            if dev and allowed(license, BUILD_ONLY):
                continue
            if dev and any(
                re.fullmatch(pattern, name) and allowed(license, BUILD_ONLY | {extra})
                for pattern, (extra, _) in EXCEPTIONS.items()
            ):
                continue
        except ValueError as err:
            out.append("%s: %s" % (what, err))
            continue
        if dev:
            out.append("%s is licensed %s, which isn't allowed even for build tools" % (what, license))
        else:
            out.append(
                "%s is licensed %s, but it ships in the UI, so it must be one of %s"
                % (what, license, ", ".join(sorted(SHIPPED)))
            )
    return out


def selftest():
    cases = [
        ("a listed license is allowed", "MIT", SHIPPED, True),
        ("an unlisted license is not", "GPL-3.0-only", SHIPPED, False),
        ("OR needs one allowed side", "(MIT OR CC0-1.0)", SHIPPED, True),
        ("OR of two unlisted licenses is not allowed", "GPL-2.0-only OR LGPL-3.0-only", SHIPPED, False),
        ("AND needs both sides", "MIT AND GPL-3.0-only", SHIPPED, False),
        ("AND of two listed licenses is allowed", "MIT AND ISC", SHIPPED, True),
        ("WITH counts as the license before it", "Apache-2.0 WITH LLVM-exception", SHIPPED, True),
        ("WITH doesn't rescue a copyleft license", "GPL-2.0-only WITH Classpath-exception-2.0", SHIPPED, False),
        ("parentheses group", "(MIT OR GPL-3.0-only) AND ISC", SHIPPED, True),
        ("a build-only license is allowed for build tools", "Python-2.0", BUILD_ONLY, True),
        ("a build-only license doesn't ship", "Python-2.0", SHIPPED, False),
    ]
    failed = 0
    for name, expression, licenses, want in cases:
        got = allowed(expression, licenses)
        if got != want:
            print("FAIL %s: allowed(%r) = %s, want %s" % (name, expression, got, want))
            failed += 1
    bad_expressions = [
        ("an operator with nothing after it is an error", "MIT OR"),
        ("unbalanced parentheses are an error", "(MIT"),
        ("two licenses with no operator are an error", "MIT ISC"),
    ]
    for name, expression in bad_expressions:
        try:
            allowed(expression, SHIPPED)
        except ValueError:
            continue
        print("FAIL %s: %r was accepted" % (name, expression))
        failed += 1

    def lock(**entry):
        return {"packages": {"": {}, "node_modules/pkg": entry}}

    lockfiles = [
        ("a shipped MIT package passes", {"packages": {"": {}, "node_modules/a": {"version": "1.0.0", "license": "MIT"}}}, 0),
        ("a shipped GPL package fails", lock(version="1.0.0", license="GPL-3.0-only"), 1),
        ("a shipped Python-2.0 package fails", lock(version="1.0.0", license="Python-2.0"), 1),
        ("a build tool may be Python-2.0", lock(version="1.0.0", license="Python-2.0", dev=True), 0),
        ("a build tool may not be GPL", lock(version="1.0.0", license="GPL-3.0-only", dev=True), 1),
        (
            "lightningcss may be MPL-2.0 as a build tool",
            {"packages": {"node_modules/lightningcss-linux-x64-gnu": {"version": "1.0.0", "license": "MPL-2.0", "dev": True}}},
            0,
        ),
        (
            "lightningcss may not be MPL-2.0 if it ships",
            {"packages": {"node_modules/lightningcss": {"version": "1.0.0", "license": "MPL-2.0"}}},
            1,
        ),
        (
            "another MPL-2.0 build tool fails",
            {"packages": {"node_modules/other": {"version": "1.0.0", "license": "MPL-2.0", "dev": True}}},
            1,
        ),
        ("a package without a license fails", lock(version="1.0.0", dev=True), 1),
        (
            "a nested package is named by its last path part",
            {"packages": {"node_modules/a/node_modules/lightningcss": {"version": "1.0.0", "license": "MPL-2.0", "dev": True}}},
            0,
        ),
    ]
    for name, parsed, want in lockfiles:
        got = len(problems(parsed))
        if got != want:
            print("FAIL %s: %d problems, want %d: %s" % (name, got, want, problems(parsed)))
            failed += 1
    total = len(cases) + len(bad_expressions) + len(lockfiles)
    print("%d of %d selftest cases passed" % (total - failed, total))
    return 1 if failed else 0


def main(args):
    if args == ["selftest"]:
        return selftest()
    if not args:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    status = 0
    for name in args:
        with open(name, encoding="utf-8") as f:
            lock = json.load(f)
        found = problems(lock)
        for p in found:
            print("::error file=%s::%s" % (name, p))
        count = len(lock.get("packages", {})) - 1
        if found:
            status = 1
        else:
            print("%s: the licenses of all %d packages are allowed" % (name, count))
    return status


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
