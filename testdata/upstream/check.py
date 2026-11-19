#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Checks the upstream contract captures under testdata/upstream.

    python3 -I testdata/upstream/check.py

Three checks, each explained in README.md next to this file:

1. Provenance. Every capture directory has a PROVENANCE.md whose front
   matter pins each source to a commit or a retrieval date and names its
   license. A directory that copies upstream text carries that upstream's
   LICENSE and copies only from licensed sources.
2. Schemas. Every JSON file validates against the schema its name points
   to, in the same directory: foo.json and foo.<label>.json against
   foo.schema.json. Every schema has at least one such file, and every
   value in a schema's "examples" validates too. Schemas may use only the
   JSON Schema 2020-12 keywords implemented here. Any other keyword is an
   error, so no schema can lean on a keyword that nothing checks.
3. Robert's webhook vectors. Each signature in robert/webhook.json is
   recomputed from its inputs, and each verification case must get the
   verdict it expects from the scheme written down in that file.

Standard library only. CI runs it with the runner's python3 and nothing
installed. It exits 1 when anything fails.
"""

import hashlib
import hmac
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))

# WP-PG0 created these. A later WP may add a directory, and it needs a
# PROVENANCE.md like theirs.
REQUIRED_DIRS = ("airtable", "ari", "hackatime", "handbook", "robert", "stardance")

METHODS = ("copied", "described", "reconstructed")
PROVENANCE_KEYS = ("method", "captured_on", "captured_by", "source")
GITHUB_SOURCE = re.compile(r"https://github\.com/[\w.-]+/[\w.-]+/(?:tree|blob)/[0-9a-f]{40}(?:/\S*)?")
LICENSE_ID = re.compile(r"none|[A-Za-z0-9][A-Za-z0-9.+-]*")
DATE = re.compile(r"\d{4}-\d{2}-\d{2}")

DRAFT = "https://json-schema.org/draft/2020-12/schema"
TYPES = ("object", "array", "string", "integer", "number", "boolean", "null")
FORMATS = ("date", "date-time", "email", "uri", "uuid")

# Keyword -> what its value must be. Annotations are listed too, so a typo
# in one is caught. Keywords starting with "x-" are free-form annotations,
# except x-file-ref, which asserts that a string names a file next to the
# instance.
KEYWORDS = {
    "$schema": "draft",
    "$id": "string",
    "$comment": "string",
    "$ref": "ref",
    "$defs": "schema_map",
    "title": "string",
    "description": "string",
    "examples": "array",
    "default": "any",
    "deprecated": "bool",
    "format": "format",
    "type": "type",
    "enum": "nonempty_array",
    "const": "any",
    "properties": "schema_map",
    "required": "string_list",
    "additionalProperties": "schema",
    "minProperties": "count",
    "maxProperties": "count",
    "items": "schema",
    "minItems": "count",
    "maxItems": "count",
    "uniqueItems": "bool",
    "minLength": "count",
    "maxLength": "count",
    "pattern": "regex",
    "minimum": "number",
    "maximum": "number",
    "allOf": "schema_list",
    "anyOf": "schema_list",
    "oneOf": "schema_list",
    "not": "schema",
    "x-file-ref": "bool",
}


def json_type(value):
    """The JSON Schema type of a decoded JSON value."""
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, float):
        # 2020-12: a number with a zero fractional part is an integer.
        return "integer" if value.is_integer() else "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    raise TypeError(f"not a JSON value: {value!r}")


def same(a, b):
    """JSON equality: 1 equals 1.0, and true never equals 1."""
    ta, tb = json_type(a), json_type(b)
    if ta in ("integer", "number") and tb in ("integer", "number"):
        return a == b
    if ta != tb:
        return False
    if ta == "array":
        return len(a) == len(b) and all(same(x, y) for x, y in zip(a, b))
    if ta == "object":
        return a.keys() == b.keys() and all(same(a[k], b[k]) for k in a)
    return a == b


def short(value):
    text = json.dumps(value, ensure_ascii=False)
    return text if len(text) <= 80 else text[:77] + "..."


def resolve(root, ref):
    """The schema a local $ref such as #/$defs/name points to, or None."""
    if not isinstance(ref, str) or not ref.startswith("#/"):
        return None
    node = root
    for part in ref[2:].split("/"):
        part = part.replace("~1", "/").replace("~0", "~")
        if not isinstance(node, dict) or part not in node:
            return None
        node = node[part]
    return node


def schema_problems(schema, root, at):
    """What is wrong with one schema node, at JSON pointer `at` in its file."""
    if isinstance(schema, bool):
        return []
    if not isinstance(schema, dict):
        return [f"{at}: a schema must be an object or a boolean"]
    out = []
    for key, value in schema.items():
        kind = KEYWORDS.get(key)
        where = f"{at}/{key}"
        if kind is None:
            if not key.startswith("x-"):
                out.append(f"{where}: keyword {key!r} is not implemented by check.py")
        elif kind == "draft":
            if at != "#" or value != DRAFT:
                out.append(f"{where}: must be {DRAFT!r}, and only at the root")
        elif kind == "string":
            if not isinstance(value, str):
                out.append(f"{where}: must be a string")
        elif kind == "bool":
            if not isinstance(value, bool):
                out.append(f"{where}: must be true or false")
        elif kind == "count":
            if json_type(value) != "integer" or value < 0:
                out.append(f"{where}: must be a non-negative integer")
        elif kind == "number":
            if json_type(value) not in ("integer", "number"):
                out.append(f"{where}: must be a number")
        elif kind == "array":
            if not isinstance(value, list):
                out.append(f"{where}: must be an array")
        elif kind == "nonempty_array":
            if not isinstance(value, list) or not value:
                out.append(f"{where}: must be a non-empty array")
        elif kind == "string_list":
            if (not isinstance(value, list) or not all(isinstance(v, str) for v in value)
                    or len(set(value)) != len(value)):
                out.append(f"{where}: must be an array of distinct strings")
        elif kind == "type":
            names = value if isinstance(value, list) else [value]
            if (not names or not all(isinstance(n, str) and n in TYPES for n in names)
                    or len(set(names)) != len(names)):
                out.append(f"{where}: must name distinct types from {', '.join(TYPES)}")
        elif kind == "format":
            if value not in FORMATS:
                out.append(f"{where}: must be one of {', '.join(FORMATS)}")
        elif kind == "regex":
            try:
                re.compile(value, re.ASCII)
            except (re.error, TypeError) as err:
                out.append(f"{where}: bad pattern: {err}")
        elif kind == "ref":
            if resolve(root, value) is None:
                out.append(f"{where}: must be a local reference like #/$defs/name that resolves")
        elif kind == "schema":
            out += schema_problems(value, root, where)
        elif kind == "schema_map":
            if not isinstance(value, dict):
                out.append(f"{where}: must be an object of schemas")
            else:
                for name, sub in value.items():
                    out += schema_problems(sub, root, f"{where}/{name}")
        elif kind == "schema_list":
            if not isinstance(value, list) or not value:
                out.append(f"{where}: must be a non-empty array of schemas")
            else:
                for i, sub in enumerate(value):
                    out += schema_problems(sub, root, f"{where}/{i}")
    return out


def subschemas(node, at):
    """Every schema directly inside a valid schema node, with its pointer."""
    if not isinstance(node, dict):
        return
    for key in ("properties", "$defs"):
        for name, sub in node.get(key, {}).items():
            yield sub, f"{at}/{key}/{name}"
    for key in ("items", "additionalProperties", "not"):
        if key in node:
            yield node[key], f"{at}/{key}"
    for key in ("allOf", "anyOf", "oneOf"):
        for i, sub in enumerate(node.get(key, [])):
            yield sub, f"{at}/{key}/{i}"


def problems(inst, schema, at, root, base):
    """Why `inst` fails `schema`; empty when it validates.

    `root` is the schema document, for $ref, and `base` is the directory
    that x-file-ref paths are relative to.
    """
    if schema is True:
        return []
    if schema is False:
        return [f"{at}: no value is allowed here"]
    out = []
    if "$ref" in schema:
        out += problems(inst, resolve(root, schema["$ref"]), at, root, base)
    t = json_type(inst)
    if "type" in schema:
        allowed = schema["type"] if isinstance(schema["type"], list) else [schema["type"]]
        if t not in allowed and not (t == "integer" and "number" in allowed):
            return out + [f"{at}: is {t}, want {' or '.join(allowed)}"]
    if "enum" in schema and not any(same(inst, e) for e in schema["enum"]):
        out.append(f"{at}: {short(inst)} is not one of {short(schema['enum'])}")
    if "const" in schema and not same(inst, schema["const"]):
        out.append(f"{at}: {short(inst)} is not {short(schema['const'])}")
    if t == "object":
        props = schema.get("properties", {})
        for name in schema.get("required", []):
            if name not in inst:
                out.append(f"{at}: missing {name!r}")
        for name, value in inst.items():
            if name in props:
                out += problems(value, props[name], f"{at}/{name}", root, base)
            elif "additionalProperties" in schema:
                out += problems(value, schema["additionalProperties"], f"{at}/{name}", root, base)
        if len(inst) < schema.get("minProperties", 0):
            out.append(f"{at}: has {len(inst)} properties, want at least {schema['minProperties']}")
        if "maxProperties" in schema and len(inst) > schema["maxProperties"]:
            out.append(f"{at}: has {len(inst)} properties, want at most {schema['maxProperties']}")
    elif t == "array":
        if "items" in schema:
            for i, value in enumerate(inst):
                out += problems(value, schema["items"], f"{at}/{i}", root, base)
        if len(inst) < schema.get("minItems", 0):
            out.append(f"{at}: has {len(inst)} items, want at least {schema['minItems']}")
        if "maxItems" in schema and len(inst) > schema["maxItems"]:
            out.append(f"{at}: has {len(inst)} items, want at most {schema['maxItems']}")
        if schema.get("uniqueItems"):
            for i in range(len(inst)):
                for j in range(i):
                    if same(inst[i], inst[j]):
                        out.append(f"{at}: items {j} and {i} are equal")
    elif t == "string":
        if len(inst) < schema.get("minLength", 0):
            out.append(f"{at}: is {len(inst)} characters, want at least {schema['minLength']}")
        if "maxLength" in schema and len(inst) > schema["maxLength"]:
            out.append(f"{at}: is {len(inst)} characters, want at most {schema['maxLength']}")
        if "pattern" in schema and not re.search(schema["pattern"], inst, re.ASCII):
            out.append(f"{at}: {short(inst)} does not match {schema['pattern']!r}")
        if schema.get("x-file-ref"):
            parts = inst.split("/")
            if os.path.isabs(inst) or ".." in parts or not os.path.isfile(os.path.join(base, *parts)):
                out.append(f"{at}: {inst!r} is not a file in this directory or below it")
    elif t in ("integer", "number"):
        if "minimum" in schema and inst < schema["minimum"]:
            out.append(f"{at}: {inst} is below the minimum {schema['minimum']}")
        if "maximum" in schema and inst > schema["maximum"]:
            out.append(f"{at}: {inst} is above the maximum {schema['maximum']}")
    for sub in schema.get("allOf", []):
        out += problems(inst, sub, at, root, base)
    if "anyOf" in schema and all(problems(inst, sub, at, root, base) for sub in schema["anyOf"]):
        out.append(f"{at}: matches none of anyOf")
    if "oneOf" in schema:
        matched = sum(1 for sub in schema["oneOf"] if not problems(inst, sub, at, root, base))
        if matched != 1:
            out.append(f"{at}: matches {matched} branches of oneOf, want exactly 1")
    if "not" in schema and not problems(inst, schema["not"], at, root, base):
        out.append(f"{at}: matches a schema it must not")
    return out


# The validator is checked before it checks anything else. Each case names
# the rule it pins: (name, schema, instance, valid).
SELFTEST = [
    ("type rejects a string where an integer is wanted", {"type": "integer"}, "1", False),
    ("a float with no fraction counts as an integer", {"type": "integer"}, 1.0, True),
    ("a boolean is not an integer", {"type": "integer"}, True, False),
    ("an integer is a number", {"type": "number"}, 3, True),
    ("required reports a missing property", {"type": "object", "required": ["a"]}, {}, False),
    ("additionalProperties false rejects an extra key",
     {"properties": {"a": {}}, "additionalProperties": False}, {"a": 1, "b": 2}, False),
    ("properties are open when additionalProperties is absent",
     {"properties": {"a": {"type": "integer"}}}, {"a": 1, "b": "x"}, True),
    ("enum never equates true with 1", {"enum": [1]}, True, False),
    ("const compares nested values", {"const": {"a": [1, 2]}}, {"a": [1, 2.0]}, True),
    ("pattern searches, so anchors are needed for a full match", {"pattern": "^ab$"}, "xab", False),
    ("\\d in a pattern matches ASCII digits only", {"pattern": "^\\d+$"}, "١٢", False),
    ("oneOf fails when two branches match", {"oneOf": [{"type": "integer"}, {"type": "number"}]}, 1, False),
    ("anyOf passes when one branch matches", {"anyOf": [{"type": "string"}, {"type": "null"}]}, None, True),
    ("not rejects a matching value", {"not": {"const": "x"}}, "x", False),
    ("$ref follows a local definition", {"$defs": {"n": {"type": "null"}}, "$ref": "#/$defs/n"}, 0, False),
    ("maxItems caps an array", {"maxItems": 1}, [1, 2], False),
    ("minimum is inclusive", {"minimum": 0}, 0, True),
    ("maximum is inclusive", {"maximum": 300}, 301, False),
    ("uniqueItems treats 1 and 1.0 as equal", {"uniqueItems": True}, [1, 1.0], False),
    ("minLength counts characters, not bytes", {"minLength": 2}, "éé", True),
    ("maxProperties caps an object", {"maxProperties": 1}, {"a": 1, "b": 2}, False),
    ("x-file-ref refuses a path outside the directory", {"x-file-ref": True}, "../check.py", False),
]


def robert_signature(secret, timestamp, body):
    """X-Robert-Signature for a delivery: sha256= and the lowercase hex
    HMAC-SHA256 of "<timestamp>.<raw body>", keyed by the UTF-8 secret."""
    message = f"{timestamp}.{body}".encode("utf-8")
    return "sha256=" + hmac.new(secret.encode("utf-8"), message, hashlib.sha256).hexdigest()


def robert_accepts(case, tolerance):
    """The receiver rule robert/webhook.json writes down, applied to a case."""
    secret, timestamp, signature = case["secret"], case["timestamp"], case["signature"]
    if not secret or timestamp is None or signature is None:
        return False
    if not re.fullmatch(r"[0-9]+", timestamp):
        return False
    if abs(case["now"] - int(timestamp)) > tolerance:
        return False
    expected = robert_signature(secret, timestamp, case["body"]).encode("utf-8")
    return hmac.compare_digest(expected, signature.encode("utf-8"))


def no_duplicate_keys(pairs):
    seen = {}
    for key, value in pairs:
        if key in seen:
            raise ValueError(f"duplicate key {key!r}")
        seen[key] = value
    return seen


def no_constants(name):
    raise ValueError(f"{name} is not JSON")


class Checker:
    def __init__(self):
        self.errors = []
        self.files = 0
        self.schemas = 0
        self.vectors = 0

    def fail(self, path, message):
        self.errors.append((os.path.relpath(path, REPO), message))

    def load(self, path):
        try:
            with open(path, encoding="utf-8") as f:
                return json.load(f, object_pairs_hook=no_duplicate_keys, parse_constant=no_constants)
        except (OSError, ValueError) as err:
            self.fail(path, f"not valid JSON: {err}")
            return None

    def selftest(self):
        for name, schema, inst, valid in SELFTEST:
            got = not problems(inst, schema, "#", schema, HERE)
            if got != valid:
                self.fail(__file__, f"validator selftest {name!r}: got valid={got}, want {valid}")

    def provenance(self, top):
        path = os.path.join(top, "PROVENANCE.md")
        if not os.path.isfile(path):
            self.fail(top, "has no PROVENANCE.md; every capture directory needs one")
            return
        with open(path, encoding="utf-8") as f:
            lines = f.read().splitlines()
        if not lines or lines[0] != "---" or "---" not in lines[1:]:
            self.fail(path, "needs front matter between two --- lines at the top")
            return
        fields = {}
        for line in lines[1:lines.index("---", 1)]:
            if not line.strip():
                continue
            key, sep, value = line.partition(":")
            key, value = key.strip(), value.strip()
            if not sep or key not in PROVENANCE_KEYS:
                self.fail(path, f"front matter line {line!r}: keys are {', '.join(PROVENANCE_KEYS)}")
                continue
            fields.setdefault(key, []).append(value)
        for key in ("method", "captured_on", "captured_by"):
            if len(fields.get(key, [])) != 1 or not fields[key][0]:
                self.fail(path, f"needs exactly one non-empty {key!r} line")
        method = fields.get("method", [""])[0]
        if method not in METHODS:
            self.fail(path, f"method {method!r}: want one of {', '.join(METHODS)}")
        captured_on = fields.get("captured_on", [""])[0]
        if not DATE.fullmatch(captured_on):
            self.fail(path, f"captured_on {captured_on!r}: want YYYY-MM-DD")
        sources = fields.get("source", [])
        if not sources:
            self.fail(path, "needs at least one 'source' line")
        licenses = []
        for value in sources:
            parts = value.split()
            if len(parts) < 2 or not LICENSE_ID.fullmatch(parts[1]):
                self.fail(path, f"source {value!r}: want '<url> <license or none> ...'")
                continue
            url, license_id, rest = parts[0], parts[1], parts[2:]
            licenses.append(license_id)
            if url.startswith("https://github.com/"):
                if not GITHUB_SOURCE.fullmatch(url) or rest:
                    self.fail(path, f"source {url}: a GitHub source is a tree or blob URL at a full "
                                    "40-character commit, followed only by its license")
            elif not (url.startswith("https://") and len(rest) == 2 and rest[0] == "retrieved"
                      and DATE.fullmatch(rest[1])):
                self.fail(path, f"source {url}: a web source is '<https url> <license> retrieved YYYY-MM-DD'")
        if method == "copied":
            if "none" in licenses:
                self.fail(path, "method 'copied' needs a license on every source; "
                                "describe an unlicensed source in our own words instead")
            if not os.path.isfile(os.path.join(top, "LICENSE")):
                self.fail(path, "method 'copied' needs the upstream's LICENSE in the same directory")

    def json_files(self, top):
        for dirpath, dirnames, filenames in os.walk(top):
            dirnames[:] = sorted(d for d in dirnames if not d.startswith((".", "_")))
            names = sorted(f for f in filenames if f.endswith(".json"))
            schemas = {}
            for name in names:
                if not name.endswith(".schema.json"):
                    continue
                path = os.path.join(dirpath, name)
                doc = self.load(path)
                if doc is None:
                    continue
                found = schema_problems(doc, doc, "#")
                if not isinstance(doc, dict) or doc.get("$schema") != DRAFT or "title" not in doc:
                    found.append(f"#: the root needs \"$schema\": {DRAFT!r} and a title")
                for sub, at in self.walk(doc, "#") if not found else []:
                    for i, example in enumerate(sub.get("examples", []) if isinstance(sub, dict) else []):
                        found += problems(example, sub, f"{at}/examples/{i}", doc, dirpath)
                for message in found:
                    self.fail(path, message)
                if not found:
                    schemas[name] = doc
                    self.schemas += 1
            used = set()
            for name in names:
                if name.endswith(".schema.json"):
                    continue
                path = os.path.join(dirpath, name)
                want = name.split(".", 1)[0] + ".schema.json"
                if want not in names:
                    self.fail(path, f"has no {want} next to it")
                    continue
                used.add(want)
                doc = self.load(path)
                if doc is None or want not in schemas:
                    continue
                self.files += 1
                for message in problems(doc, schemas[want], "#", schemas[want], dirpath):
                    self.fail(path, message)
            for name in names:
                if name.endswith(".schema.json") and name not in used:
                    self.fail(os.path.join(dirpath, name), "no JSON file next to it validates against it")

    def walk(self, node, at):
        yield node, at
        for sub, where in subschemas(node, at):
            yield from self.walk(sub, where)

    def robert(self):
        path = os.path.join(HERE, "robert", "webhook.json")
        if not os.path.isfile(path):
            self.fail(path, "missing")
            return
        doc = self.load(path)
        try:
            tolerance = doc["scheme"]["tolerance_s"]
            for v in doc["signing_vectors"]:
                got = robert_signature(v["secret"], v["timestamp"], v["body"])
                if got != v["signature"]:
                    self.fail(path, f"signing vector {v['name']!r}: inputs give {got}, the file says {v['signature']}")
                self.vectors += 1
            for case in doc["verify_cases"]:
                got = "accept" if robert_accepts(case, tolerance) else "reject"
                if got != case["expect"]:
                    self.fail(path, f"verify case {case['name']!r}: the scheme gives {got}, "
                                    f"the file says {case['expect']}")
                self.vectors += 1
        except (KeyError, TypeError, ValueError) as err:
            self.fail(path, f"can't read the vectors: {err!r}")


def main():
    c = Checker()
    c.selftest()
    # Skips __pycache__ and dot directories, which aren't captures.
    dirs = sorted(e.name for e in os.scandir(HERE) if e.is_dir() and not e.name.startswith((".", "_")))
    for name in REQUIRED_DIRS:
        if name not in dirs:
            c.fail(os.path.join(HERE, name), "capture directory is missing")
    for name in dirs:
        c.provenance(os.path.join(HERE, name))
        c.json_files(os.path.join(HERE, name))
    c.robert()
    for path, message in c.errors:
        print(f"::error file={path}::{message}")
    if c.errors:
        print(f"{len(c.errors)} problem(s) in testdata/upstream")
        return 1
    print(f"testdata/upstream: {len(dirs)} directories, {c.files} JSON files against "
          f"{c.schemas} schemas, {c.vectors} Robert vectors and cases, all good")
    return 0


if __name__ == "__main__":
    sys.exit(main())
