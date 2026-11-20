# SPDX-License-Identifier: AGPL-3.0-or-later
"""Checks that release archives carry the notices of the dashboard's npm code.

    python3 -I tools/ci/npm_notices.py web/dist/bundle-agent.json
    python3 -I tools/ci/npm_notices.py selftest

Release archives embed the local dashboard, which `vite build --mode agent`
writes into internal/webui/dist, and carry THIRD_PARTY_NOTICES.txt, which
`tools/release notices` writes from committed files alone. So the license and
notice files of every npm package with code in the dashboard are copied into
internal/webui/notices/<package>/, and tools/release/extra-notices.txt lists
each copy against internal/webui.

Run it from the repository root after the build, on the bundle report that
vite.config.ts writes beside it. A package has code in the build when one of
its modules renders code into a chunk or is a stylesheet, whose rules go to a
CSS file, or when the build emits one of its files as an asset. It fails when:

- the build has code from a module that is neither pairtime's own, in web/src
  or in a directory OWN_OUTSIDE names, such as internal/coach/text, nor a
  package's in web/node_modules, such as one a bundler or plugin writes
  itself;
- a package with code in the build is a dev dependency in
  web/package-lock.json, which npm_licenses.py holds only to the licenses
  build tools may have, or the lockfile has another version of it;
- such a package has no license file at its root, or a license or notice
  file of it, at its root or in a directory between the root and code the
  build took, has no copy, or a copy that isn't the same bytes;
- internal/webui/notices holds any other file;
- extra-notices.txt doesn't list each copy against internal/webui with the
  title "<name> <version> from npm, bundled into the local dashboard in
  internal/webui", or lists a copy no package needs.

License and notice files have the names tools/release/notices.go reads in Go
modules: LICENSE, LICENCE, COPYING, UNLICENSE, MIT-LICENSE, NOTICE or
PATENTS, in any case, alone or followed by ".", "-" or "_" and more, unless
the name ends in a code extension, as license.js does. Exit status 1 means a
problem, each printed as a GitHub error annotation, and 2 a wrong command.
"""

import json
import os
import re
import sys
import tempfile

# The build internal/webui embeds, and where its notices go.
BUILD = "agent"
WEB = "web"
GO_PACKAGE = "internal/webui"
NOTICES = "internal/webui/notices"
EXTRA = "tools/release/extra-notices.txt"
LOCK = "web/package-lock.json"
TITLE = "{name} {version} from npm, bundled into the local dashboard in internal/webui"

# The ids of modules a bundler or plugin writes itself start with NUL.
NUL = chr(0)

# licenseStems and noticeStems in tools/release/notices.go. Its codeExts are
# the files the go command compiles; these are the ones a JavaScript build
# reads as code or data.
LICENSE_STEMS = (
    "license", "licence", "copying", "unlicense", "unlicence",
    "mit-license", "mit-licence", "mit_license", "mit_licence",
)
NOTICE_STEMS = ("notice", "patents")
CODE_EXTS = (
    ".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx",
    ".svelte", ".css", ".json", ".map", ".wasm",
)

STYLE = "(css|less|sass|scss|styl|stylus|pcss|postcss|sss)"
STYLESHEET_PATH = re.compile(r"\." + STYLE + "$")
# Svelte's component styles are modules like App.svelte?svelte&type=style&lang.css.
STYLESHEET_QUERY = re.compile(r"(^|&)lang\." + STYLE + "($|&)")
# npm's rules for a package name, loosely: enough that it's one path, or a
# scope and a path, inside internal/webui/notices.
PACKAGE_NAME = re.compile(r"(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*")


def has_stem(name, stems):
    lower = name.lower()
    if lower.endswith(CODE_EXTS):
        return False
    for stem in stems:
        if lower.startswith(stem):
            rest = lower[len(stem):]
            if rest == "" or rest[0] in ".-_":
                return True
    return False


def is_license(name):
    return has_stem(name, LICENSE_STEMS)


def is_notice(name):
    return has_stem(name, LICENSE_STEMS) or has_stem(name, NOTICE_STEMS)


def shown(module_id):
    return module_id.replace(NUL, "\\0")


def locate(path):
    """Splits a file's path from web/ into the root of the package it's in,
    such as node_modules/svelte, and its directory within that package, or
    returns None for a file outside web/node_modules."""
    parts = path.split("/")
    if parts[0] != "node_modules" or {"", ".", ".."} & set(parts):
        return None
    last = len(parts) - 1 - parts[::-1].index("node_modules")
    width = 2 if last + 1 < len(parts) and parts[last + 1].startswith("@") else 1
    if len(parts) < last + width + 2:
        return None
    return "/".join(parts[: last + 1 + width]), "/".join(parts[last + 1 + width : -1])


# Directories outside web/ whose files the dashboard may bundle as
# pairtime's own code, under its AGPL like web/src: the words the coach, the
# CLI views and the dashboard share (internal/coach/text, WP-CO0), so a
# minute reads the same everywhere. Only these: a file elsewhere in the
# repository may be another project's, such as gitleaks' rules in
# pkg/redact/rules, and needs its notice.
OWN_OUTSIDE = ("internal/coach/text/",)


def is_own(path):
    parts = path.split("/")
    if {"", "."} & set(parts):
        return False
    if parts[0] == "src":
        return ".." not in parts
    if parts[0] == ".." and ".." not in parts[1:] and "node_modules" not in parts:
        rest = "/".join(parts[1:])
        return any(rest.startswith(d) for d in OWN_OUTSIDE)
    return False


def read_report(path):
    with open(path, encoding="utf-8") as f:
        report = json.load(f)
    if not isinstance(report, dict):
        raise ValueError("it isn't a JSON object")
    if report.get("build") != BUILD:
        raise ValueError(
            "it is the %s build's, and internal/webui embeds the %s build's (vite build --mode %s)"
            % (json.dumps(report.get("build")), BUILD, BUILD)
        )
    modules, assets = report.get("modules"), report.get("assets")
    if not isinstance(modules, list) or not all(
        isinstance(m, dict)
        and isinstance(m.get("id"), str)
        and isinstance(m.get("rendered"), int)
        and not isinstance(m.get("rendered"), bool)
        and m["rendered"] >= 0
        for m in modules
    ):
        raise ValueError("its modules aren't a list of {id, rendered}")
    if not modules:
        raise ValueError("it lists no modules, so it can't be a build's")
    if not isinstance(assets, list) or not all(isinstance(a, str) for a in assets):
        raise ValueError("its assets aren't a list of paths")
    return modules, assets


def notice_files(package_dir, dirs):
    """Reads the license and notice files directly in each of dirs, the
    directories within the package that hold code the build took, and in
    each directory above them up to the package root: the walk notices.go
    makes from a linked Go package to its module root. Returns them by path
    within the package, and the paths that are links or special files, and
    the directories that don't exist."""
    found, read, links, missing = {}, set(), [], []
    for d in sorted(dirs):
        while True:
            if d not in read:
                read.add(d)
                full = os.path.join(package_dir, *d.split("/")) if d else package_dir
                try:
                    names = sorted(os.listdir(full))
                except FileNotFoundError:
                    missing.append(d)
                    names = []
                for name in names:
                    p = os.path.join(full, name)
                    if not is_notice(name) or os.path.isdir(p):
                        continue
                    rel = d + "/" + name if d else name
                    if os.path.islink(p) or not os.path.isfile(p):
                        links.append(rel)
                        continue
                    with open(p, "rb") as f:
                        found[rel] = f.read()
            if not d:
                break
            d = d.rsplit("/", 1)[0] if "/" in d else ""
    return found, links, missing


def read_tree(top):
    """Reads every file under top, by its path from top with slashes."""
    files = {}
    for dirpath, dirnames, filenames in os.walk(top):
        dirnames.sort()
        for name in sorted(filenames):
            p = os.path.join(dirpath, name)
            with open(p, "rb") as f:
                files[os.path.relpath(p, top).replace(os.sep, "/")] = f.read()
    return files


def read_extra(path):
    """The lines of extra-notices.txt, as (line number, package, file, title)."""
    lines = []
    with open(path, encoding="utf-8") as f:
        for number, line in enumerate(f.read().split("\n"), 1):
            fields = line.split()
            if len(fields) >= 3 and not fields[0].startswith("#"):
                lines.append((number, fields[0], fields[1], " ".join(fields[2:])))
    return lines


def owner(copy):
    """The package a copy's path within internal/webui/notices is for."""
    parts = copy.split("/")
    return "/".join(parts[:2]) if parts[0].startswith("@") else parts[0]


def check(root, report_path):
    """Checks the notices for the build report_path describes, in the
    repository at root. Returns the problems, as (file, line, message),
    lines that say what the build bundles, and the lines extra-notices.txt
    should have for it."""
    problems = []

    def problem(message, file=None, line=None):
        problems.append((file, line, message))

    try:
        modules, assets = read_report(report_path)
    except (OSError, ValueError) as err:
        problem("%s can't be checked: %s" % (report_path, err), report_path)
        return problems, [], []

    # Package root -> directories within it that hold code the build took,
    # with the modules that had code and how much, and those that had none.
    packages = {}
    own = [0, 0]

    def took(package_root, d, rendered):
        p = packages.setdefault(package_root, {"dirs": set(), "modules": 0, "bytes": 0, "empty": 0})
        if rendered is None:
            p["empty"] += 1
            return
        p["dirs"].add(d)
        p["modules"] += 1
        p["bytes"] += rendered

    for m in modules:
        module_id, rendered = m["id"], m["rendered"]
        path, _, query = module_id.partition("?")
        has_code = rendered > 0 or bool(STYLESHEET_PATH.search(path) or STYLESHEET_QUERY.search(query))
        where = None if module_id.startswith(NUL) else locate(path)
        if where:
            took(where[0], where[1], rendered if has_code else None)
        elif not module_id.startswith(NUL) and is_own(path):
            own[0] += 1
            own[1] += rendered
        elif has_code:
            problem(
                "the build has %d bytes of code from %s, which is neither web/src's nor a package's in web/node_modules, "
                "so no notice covers it; find whose code it is, then turn it off or commit its license"
                % (rendered, shown(module_id))
            )
    for a in assets:
        where = locate(a)
        if where:
            took(where[0], where[1], 0)
        elif not is_own(a):
            problem(
                "the build emitted %s as an asset, and it is neither web/src's nor a package's in web/node_modules, "
                "so no notice covers it" % a
            )

    web = os.path.join(root, WEB)
    try:
        with open(os.path.join(root, LOCK), encoding="utf-8") as f:
            locked = json.load(f).get("packages", {})
    except (OSError, ValueError, AttributeError) as err:
        problem("%s can't be read: %s" % (LOCK, err), LOCK)
        locked = {}

    summary = ["%s: %d modules of web/src's with %d bytes of code" % (report_path, own[0], own[1])]
    bundled = {}  # name -> (version, package root, notice files)
    # Shallow packages first, so when two copies of one have code, the one
    # npm hoisted to web/node_modules is the one checked.
    for package_root in sorted(packages, key=lambda r: (r.split("/").count("node_modules"), r)):
        p = packages[package_root]
        try:
            with open(os.path.join(web, *package_root.split("/"), "package.json"), encoding="utf-8") as f:
                meta = json.load(f)
            name, version = meta["name"], meta["version"]
            if not isinstance(name, str) or not PACKAGE_NAME.fullmatch(name) or not isinstance(version, str):
                raise ValueError("its name or version isn't one")
        except (OSError, ValueError, KeyError, TypeError) as err:
            problem("web/%s/package.json can't be read for a name and version: %s" % (package_root, err))
            continue
        if not p["dirs"]:
            summary.append(
                "  %s %s (%s): %d modules in the chunks, none with code, so it needs no notice"
                % (name, version, package_root, p["empty"])
            )
            continue
        if name in bundled:
            problem(
                "the build has code from two copies of %s, %s in %s and %s in %s; dedupe them, "
                "since internal/webui/notices/%s holds one package's files"
                % (name, bundled[name][0], bundled[name][1], version, package_root, name)
            )
            continue
        entry = locked.get(package_root)
        if not isinstance(entry, dict):
            problem("%s has no entry for %s, whose code is in the build" % (LOCK, package_root), LOCK)
        elif entry.get("dev") is True:
            problem(
                "%s is a dev dependency in %s, but its code is in the build; make it a dependency, "
                "so npm_licenses.py holds it to the licenses that may ship" % (name, LOCK),
                LOCK,
            )
        elif entry.get("version") != version:
            problem(
                "web/%s holds %s %s, but %s says %s; check the build after npm ci"
                % (package_root, name, version, LOCK, entry.get("version")),
                LOCK,
            )
        files, links, missing = notice_files(os.path.join(web, *package_root.split("/")), p["dirs"])
        for rel in links:
            problem("web/%s/%s is a symbolic link or special file; notice files must be regular files" % (package_root, rel))
        for d in missing:
            problem(
                "the build took code from web/%s/%s, which doesn't exist here; run this check where the build ran"
                % (package_root, d)
            )
        if not any("/" not in rel and is_license(rel) for rel in files):
            problem("%s %s has no license file at its root (web/%s), so its terms are unknown" % (name, version, package_root))
        bundled[name] = (version, package_root, files)
        summary.append(
            "  %s %s (%s): %d modules with %d bytes of code; notices: %s"
            % (name, version, package_root, p["modules"], p["bytes"], ", ".join(sorted(files)) or "none")
        )

    # The copies, by path from the repository root.
    want = {}
    for name, (version, package_root, files) in bundled.items():
        entry = locked.get(package_root)
        url = entry.get("resolved") if isinstance(entry, dict) else None
        for rel, data in files.items():
            want["%s/%s/%s" % (NOTICES, name, rel)] = (name, version, package_root, rel, data, url)
    have = read_tree(os.path.join(root, NOTICES)) if os.path.isdir(os.path.join(root, NOTICES)) else {}
    have = {"%s/%s" % (NOTICES, copy): data for copy, data in have.items()}
    for copy in sorted(want):
        name, version, package_root, rel, data, url = want[copy]
        fetch = "copy web/%s/%s here" % (package_root, rel)
        if url:
            fetch += ", or take it from the package's tarball: curl -fsSL %s | tar -xzO 'package/%s' > '%s'" % (url, rel, copy)
        if copy not in have:
            problem("%s %s's %s has no copy here; %s" % (name, version, rel, fetch), copy)
        elif have[copy] != data:
            problem("isn't the same bytes as web/%s/%s, %s %s's; %s" % (package_root, rel, name, version, fetch), copy)
    for copy in sorted(have):
        if copy in want:
            continue
        rel = copy[len(NOTICES) + 1 :]
        name = owner(rel)
        if "/" not in rel[len(name) :]:
            why = "%s holds only directories named after packages" % NOTICES
        elif name in bundled:
            why = "%s %s has no license or notice file %s" % (name, bundled[name][0], rel[len(name) + 1 :])
        else:
            why = "the build has no code from %s" % name
        problem("%s; delete it, and its line in %s" % (why, EXTRA), copy)

    lines = {
        copy: "%s %s %s" % (GO_PACKAGE, copy, TITLE.format(name=w[0], version=w[1])) for copy, w in want.items()
    }
    try:
        extra = read_extra(os.path.join(root, EXTRA))
    except OSError as err:
        problem("%s can't be read: %s" % (EXTRA, err), EXTRA)
        extra = []
    listed = set()
    for number, package, copy, title in extra:
        if not copy.startswith(NOTICES + "/"):
            continue
        got = "%s %s %s" % (package, copy, title)
        if copy in listed:
            problem("line %d lists %s again" % (number, copy), EXTRA, number)
        elif copy not in lines:
            problem("line %d lists %s, which isn't a notice of a package with code in the build; delete the line" % (number, copy), EXTRA, number)
        elif got != lines[copy]:
            problem("line %d should be: %s" % (number, lines[copy]), EXTRA, number)
        listed.add(copy)
    for copy in sorted(lines):
        if copy not in listed:
            problem("no line lists %s; add: %s" % (copy, lines[copy]), EXTRA)
    return problems, summary, [lines[copy] for copy in sorted(lines)]


def selftest():
    failed = 0
    total = 0

    def expect(name, got, want):
        nonlocal failed, total
        total += 1
        if got != want:
            print("FAIL %s: got %r, want %r" % (name, got, want))
            failed += 1

    for name, file, want in [
        ("LICENSE is a license", "LICENSE", True),
        ("case doesn't matter", "License.txt", True),
        ("a suffix after a hyphen counts", "LICENSE-MIT", True),
        ("clsx's lower-case license counts", "license", True),
        ("COPYING is a license", "COPYING", True),
        ("NOTICE is a notice", "NOTICE.md", True),
        ("code named like a license isn't one", "license.js", False),
        ("types named like a license aren't one", "license.d.ts", False),
        ("a name that only starts with the stem isn't one", "licensed.txt", False),
        ("LICENSES, plural, isn't one", "LICENSES", False),
        ("README.md isn't one", "README.md", False),
    ]:
        expect("is_notice: " + name, is_notice(file), want)

    for name, path, want in [
        ("a package's file is at its directory in the package", "node_modules/svelte/src/internal/a.js", ("node_modules/svelte", "src/internal")),
        ("a file at a package's root is in its root", "node_modules/clsx/dist.js", ("node_modules/clsx", "")),
        ("a scoped package is two segments", "node_modules/@scope/pkg/index.js", ("node_modules/@scope/pkg", "")),
        ("a package nested in another is the inner one", "node_modules/svelte/node_modules/esrap/src/x.js", ("node_modules/svelte/node_modules/esrap", "src")),
        ("web/src's code is in no package", "src/local/main.ts", None),
        ("a package directory alone isn't a file in it", "node_modules/svelte", None),
        ("a path out of web/ is in no package", "../node_modules/x/y.js", None),
    ]:
        expect("locate: " + name, locate(path), want)

    def world():
        """A repository with a build that bundles svelte, which vendors a
        library with its own license, and @scope/pkg, which has a NOTICE."""
        svelte_license = b"svelte's MIT license\n"
        vendored = b"the vendored library's license\n"
        scope_license, scope_notice = b"Apache License 2.0\n", b"pkg's NOTICE\n"
        lines = [
            "# a comment, and a line for something else",
            "pkg/redact pkg/redact/rules/LICENSE gitleaks' rules",
            "internal/webui internal/webui/notices/svelte/LICENSE.md " + TITLE.format(name="svelte", version="5.0.0"),
            "internal/webui internal/webui/notices/svelte/src/internal/vendored/LICENSE " + TITLE.format(name="svelte", version="5.0.0"),
            "internal/webui internal/webui/notices/@scope/pkg/LICENSE " + TITLE.format(name="@scope/pkg", version="1.0.0"),
            "internal/webui internal/webui/notices/@scope/pkg/NOTICE " + TITLE.format(name="@scope/pkg", version="1.0.0"),
        ]
        return {
            "lock": {
                "lockfileVersion": 3,
                "packages": {
                    "": {},
                    "node_modules/svelte": {"version": "5.0.0", "resolved": "https://registry.npmjs.org/svelte/-/svelte-5.0.0.tgz"},
                    "node_modules/@scope/pkg": {"version": "1.0.0"},
                },
            },
            "report": {
                "build": "agent",
                "modules": [
                    {"id": "src/local/main.ts", "rendered": 120},
                    {"id": "src/local/App.svelte?svelte&type=style&lang.css", "rendered": 0},
                    {"id": "node_modules/svelte/src/internal/client.js", "rendered": 5000},
                    {"id": "node_modules/svelte/src/internal/vendored/lib.js", "rendered": 40},
                    {"id": "node_modules/@scope/pkg/index.js", "rendered": 7},
                ],
                "assets": ["src/shared/favicon.svg"],
            },
            "files": {
                "web/node_modules/svelte/package.json": b'{"name": "svelte", "version": "5.0.0"}',
                "web/node_modules/svelte/LICENSE.md": svelte_license,
                "web/node_modules/svelte/src/internal/client.js": b"code",
                "web/node_modules/svelte/src/internal/vendored/lib.js": b"code",
                "web/node_modules/svelte/src/internal/vendored/LICENSE": vendored,
                "web/node_modules/@scope/pkg/package.json": b'{"name": "@scope/pkg", "version": "1.0.0"}',
                "web/node_modules/@scope/pkg/LICENSE": scope_license,
                "web/node_modules/@scope/pkg/NOTICE": scope_notice,
                "web/node_modules/@scope/pkg/index.js": b"code",
                "internal/webui/notices/svelte/LICENSE.md": svelte_license,
                "internal/webui/notices/svelte/src/internal/vendored/LICENSE": vendored,
                "internal/webui/notices/@scope/pkg/LICENSE": scope_license,
                "internal/webui/notices/@scope/pkg/NOTICE": scope_notice,
                "tools/release/extra-notices.txt": ("\n".join(lines) + "\n").encode(),
            },
        }

    def package(w, name, version, dev=False, files=None):
        root = "node_modules/" + name
        w["lock"]["packages"][root] = {"version": version, "dev": True} if dev else {"version": version}
        w["files"]["web/%s/package.json" % root] = json.dumps({"name": name, "version": version}).encode()
        for rel, data in (files or {"LICENSE": b"MIT\n"}).items():
            w["files"]["web/%s/%s" % (root, rel)] = data

    def module(w, module_id, rendered):
        w["report"]["modules"].append({"id": module_id, "rendered": rendered})

    def edit_line(w, old, new):
        path = "tools/release/extra-notices.txt"
        text = w["files"][path].decode()
        if old not in text:
            raise AssertionError("no %r in extra-notices.txt" % old)
        w["files"][path] = text.replace(old, new).encode()

    def drop(w, *paths):
        for p in paths:
            del w["files"][p]

    def add_line(w, line):
        w["files"]["tools/release/extra-notices.txt"] += (line + "\n").encode()

    svelte_line = "internal/webui internal/webui/notices/svelte/LICENSE.md " + TITLE.format(name="svelte", version="5.0.0")
    cases = [
        ("a build whose packages have their copies and lines passes", lambda w: None, []),
        (
            "a copy that isn't the package's bytes fails",
            lambda w: w["files"].update({"internal/webui/notices/svelte/LICENSE.md": b"svelte's MIT license, 2024\n"}),
            ["isn't the same bytes as web/node_modules/svelte/LICENSE.md, svelte 5.0.0's"],
        ),
        (
            "a missing copy fails, and says where to get the file",
            lambda w: drop(w, "internal/webui/notices/svelte/LICENSE.md"),
            ["svelte 5.0.0's LICENSE.md has no copy here; copy web/node_modules/svelte/LICENSE.md here, or take it from the package's "
             "tarball: curl -fsSL https://registry.npmjs.org/svelte/-/svelte-5.0.0.tgz | tar -xzO 'package/LICENSE.md' > "
             "'internal/webui/notices/svelte/LICENSE.md'"],
        ),
        (
            "a license beside code the build took deeper in a package needs a copy too",
            lambda w: drop(w, "internal/webui/notices/svelte/src/internal/vendored/LICENSE"),
            ["svelte 5.0.0's src/internal/vendored/LICENSE has no copy here"],
        ),
        (
            "a license beside code the build didn't take needs no copy",
            lambda w: w["files"].update({"web/node_modules/svelte/src/compiler/LICENSE": b"the compiler's\n"}),
            [],
        ),
        (
            "code named like a license isn't a notice",
            lambda w: w["files"].update({"web/node_modules/svelte/license.js": b"code"}),
            [],
        ),
        (
            "a package whose modules render no code needs no notice",
            lambda w: (package(w, "esm-env", "1.2.2"), module(w, "node_modules/esm-env/false.js", 0)),
            [],
        ),
        (
            "a module a bundler writes itself may be in the chunks without code",
            lambda w: module(w, NUL + "rolldown/runtime.js", 0),
            [],
        ),
        (
            "code from a module a bundler or plugin writes itself fails",
            lambda w: module(w, NUL + "vite/modulepreload-polyfill.js", 600),
            ["600 bytes of code from \\0vite/modulepreload-polyfill.js"],
        ),
        (
            "code from outside web/src and web/node_modules fails",
            lambda w: module(w, "../docs/x.js", 10),
            ["10 bytes of code from ../docs/x.js"],
        ),
        (
            "the words in internal/coach/text, which the dashboard shares with the CLI, are pairtime's own",
            lambda w: module(w, "../internal/coach/text/en.json", 900),
            [],
        ),
        (
            "code from beside internal/coach/text fails",
            lambda w: module(w, "../internal/coach/x.js", 10),
            ["10 bytes of code from ../internal/coach/x.js"],
        ),
        (
            "a path that climbs out of the repository fails, even through internal/coach/text",
            lambda w: module(w, "../../internal/coach/text/en.json", 10),
            ["10 bytes of code from ../../internal/coach/text/en.json"],
        ),
        (
            "a stylesheet from a package needs its notice though it renders no JavaScript",
            lambda w: (package(w, "normalize", "1.0.0"), module(w, "node_modules/normalize/normalize.css", 0)),
            ["normalize 1.0.0's LICENSE has no copy here", "no line lists internal/webui/notices/normalize/LICENSE"],
        ),
        (
            "a package file the build emits as an asset needs its notice",
            lambda w: (
                package(w, "font", "1.0.0", files={"LICENSE": b"OFL\n", "files/x.woff2": b"font"}),
                w["report"]["assets"].append("node_modules/font/files/x.woff2"),
            ),
            ["font 1.0.0's LICENSE has no copy here", "no line lists internal/webui/notices/font/LICENSE"],
        ),
        (
            "code from a directory node_modules doesn't have fails",
            lambda w: module(w, "node_modules/svelte/gone/x.js", 4),
            ["the build took code from web/node_modules/svelte/gone, which doesn't exist here"],
        ),
        (
            "a file at the root of internal/webui/notices fails",
            lambda w: w["files"].update({"internal/webui/notices/README.md": b"hi\n"}),
            ["internal/webui/notices holds only directories named after packages"],
        ),
        (
            "a dev dependency with code in the build fails",
            lambda w: (
                package(w, "devtool", "1.0.0", dev=True),
                module(w, "node_modules/devtool/index.js", 5),
                w["files"].update({"internal/webui/notices/devtool/LICENSE": b"MIT\n"}),
                add_line(w, "internal/webui internal/webui/notices/devtool/LICENSE " + TITLE.format(name="devtool", version="1.0.0")),
            ),
            ["devtool is a dev dependency in web/package-lock.json, but its code is in the build"],
        ),
        (
            "a package the lockfile has at another version fails",
            lambda w: w["lock"]["packages"]["node_modules/svelte"].update({"version": "5.0.1"}),
            ["web/node_modules/svelte holds svelte 5.0.0, but web/package-lock.json says 5.0.1"],
        ),
        (
            "a package with no license file at its root fails, though it has a NOTICE",
            lambda w: (
                drop(w, "web/node_modules/@scope/pkg/LICENSE", "internal/webui/notices/@scope/pkg/LICENSE"),
                edit_line(w, "internal/webui internal/webui/notices/@scope/pkg/LICENSE " + TITLE.format(name="@scope/pkg", version="1.0.0") + "\n", ""),
            ),
            ["@scope/pkg 1.0.0 has no license file at its root"],
        ),
        (
            "a copy for a package the build has no code from fails, and so does its line",
            lambda w: (
                w["files"].update({"internal/webui/notices/clsx/license": b"MIT\n"}),
                add_line(w, "internal/webui internal/webui/notices/clsx/license " + TITLE.format(name="clsx", version="2.1.1")),
            ),
            [
                "the build has no code from clsx; delete it",
                "lists internal/webui/notices/clsx/license, which isn't a notice of a package with code in the build",
            ],
        ),
        (
            "a file among a package's copies that isn't one of its notices fails",
            lambda w: w["files"].update({"internal/webui/notices/svelte/README.md": b"hi\n"}),
            ["svelte 5.0.0 has no license or notice file README.md"],
        ),
        (
            "a copy no line lists fails",
            lambda w: edit_line(w, svelte_line + "\n", ""),
            ["no line lists internal/webui/notices/svelte/LICENSE.md; add: " + svelte_line],
        ),
        (
            "a line with another version in its title fails, and gives the line to write",
            lambda w: edit_line(w, svelte_line, svelte_line.replace("5.0.0", "4.2.0")),
            ["line 3 should be: " + svelte_line],
        ),
        (
            "a line against another package fails",
            lambda w: edit_line(w, svelte_line, svelte_line.replace("internal/webui internal", "internal/agent internal")),
            ["line 3 should be: " + svelte_line],
        ),
        (
            "a line for a file outside internal/webui/notices is left alone",
            lambda w: add_line(w, "internal/webui internal/webui/fonts/LICENSE A font"),
            [],
        ),
        (
            "two versions of one package in the build fail",
            lambda w: (
                package(w, "other/node_modules/svelte", "4.0.0", files={"LICENSE.md": b"svelte's MIT license\n"}),
                w["files"].update({"web/node_modules/other/node_modules/svelte/package.json": b'{"name": "svelte", "version": "4.0.0"}'}),
                module(w, "node_modules/other/node_modules/svelte/index.js", 3),
            ),
            ["the build has code from two copies of svelte"],
        ),
        (
            "a report of another build is refused",
            lambda w: w["report"].update({"build": "gallery"}),
            ['it is the "gallery" build\'s, and internal/webui embeds the agent build\'s'],
        ),
        (
            "a report that lists no modules is refused",
            lambda w: w["report"].update({"modules": []}),
            ["it lists no modules"],
        ),
    ]
    for name, edit, want in cases:
        w = world()
        edit(w)
        with tempfile.TemporaryDirectory() as root:
            for rel, data in w["files"].items():
                p = os.path.join(root, *rel.split("/"))
                os.makedirs(os.path.dirname(p), exist_ok=True)
                with open(p, "wb") as f:
                    f.write(data)
            with open(os.path.join(root, *LOCK.split("/")), "w", encoding="utf-8") as f:
                json.dump(w["lock"], f)
            report = os.path.join(root, "bundle-agent.json")
            with open(report, "w", encoding="utf-8") as f:
                json.dump(w["report"], f)
            got = [message for _, _, message in check(root, report)[0]]
        total += 1
        missing = [s for s in want if not any(s in g for g in got)]
        if missing or len(got) != len(want):
            print("FAIL %s: problems %r, want ones containing %r" % (name, got, want))
            failed += 1

    print("%d of %d selftest cases passed" % (total - failed, total))
    return 1 if failed else 0


def main(args):
    if args == ["selftest"]:
        return selftest()
    if len(args) != 1:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    problems, summary, lines = check(".", args[0])
    for line in summary:
        print(line)
    for file, line, message in problems:
        where = ""
        if file:
            where = " file=%s" % file + (",line=%d" % line if line else "")
        print("::error%s::%s" % (where, message))
    if problems:
        if lines:
            print("%s's lines for the dashboard's npm code should be:" % EXTRA)
            for line in lines:
                print("  " + line)
        return 1
    print("every npm package with code in the dashboard has its notices in %s, listed in %s" % (NOTICES, EXTRA))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
