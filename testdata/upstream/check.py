#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Checks the upstream contract captures under testdata/upstream.

    python3 -I testdata/upstream/check.py

Three checks, each explained in README.md next to this file:

1. Provenance. Every capture directory has a PROVENANCE.md whose front
   matter pins each source to a commit or a retrieval date and names its
   license. A directory that copies upstream text carries that upstream's
   LICENSE and copies only from sources under a license in COPYABLE.
2. Schemas. Every JSON file validates against the schema its name points
   to, in the same directory: foo.json and foo.<label>.json against
   foo.schema.json. Every schema has at least one such file, every value
   in a schema's "examples" validates too, and every value in its
   "x-invalid-examples" fails. Schemas may use only the
   JSON Schema 2020-12 keywords implemented here. Any other keyword is an
   error, so no schema can lean on a keyword that nothing checks.
3. Robert's webhook vectors. Each signature in robert/webhook.json is
   recomputed from its inputs, and each verification case must get the
   verdict it expects from the scheme written down in that file and say
   rightly, in mac_valid, whether its signature is its inputs' MAC.

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
# A capture may quote an upstream only under one of these, as SPDX ids, which
# SPDX matches without regard to case. A source under anything else, none,
# NOASSERTION and UNLICENSED included, is described in our own words.
COPYABLE = ("MIT", "BSD-2-Clause", "BSD-3-Clause", "Apache-2.0", "ISC", "0BSD", "CC0-1.0", "CC-BY-4.0")
DATE = re.compile(r"\d{4}-\d{2}-\d{2}")

DRAFT = "https://json-schema.org/draft/2020-12/schema"
DEF_REF = re.compile(r"#/\$defs/[^/]+")
TYPES = ("object", "array", "string", "integer", "number", "boolean", "null")
FORMATS = ("date", "date-time", "email", "uri", "uuid")

# Keyword -> what its value must be. Annotations are listed too, so a typo
# in one is caught. Keywords starting with "x-" are free-form annotations,
# except x-file-ref, which asserts that a string names a file next to the
# instance, and x-invalid-examples, values that must fail the schema they
# sit in.
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
    "x-invalid-examples": "array",
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
            # Only a $defs entry is sure to be a schema this function checks.
            # "#/$defs" or "#/properties" resolve to a map of schemas, which
            # problems() would take for a schema with no keywords, one that
            # lets every value through.
            if not (isinstance(value, str) and DEF_REF.fullmatch(value)) or resolve(root, value) is None:
                out.append(f"{where}: must be #/$defs/<name>, naming an entry of this file's $defs")
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


def walk(node, at):
    """Every schema in a valid schema document, with its pointer."""
    yield node, at
    for sub, where in subschemas(node, at):
        yield from walk(sub, where)


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
        if "pattern" in schema and not re.search(strict_end(schema["pattern"]), inst, re.ASCII):
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


def example_problems(doc, base):
    """Every value in an examples list must pass the schema it sits in, and
    every value in an x-invalid-examples list must fail it."""
    out = []
    for sub, at in walk(doc, "#"):
        if not isinstance(sub, dict):
            continue
        for i, example in enumerate(sub.get("examples", [])):
            out += problems(example, sub, f"{at}/examples/{i}", doc, base)
        for i, example in enumerate(sub.get("x-invalid-examples", [])):
            if not problems(example, sub, f"{at}/x-invalid-examples/{i}", doc, base):
                out.append(f"{at}/x-invalid-examples/{i}: {short(example)} is valid here, but it is listed as invalid")
    return out


def provenance_problems(lines, has_license_file):
    """What is wrong with a PROVENANCE.md, given its lines and whether the
    upstream's LICENSE file sits next to it."""
    if not lines or lines[0] != "---" or "---" not in lines[1:]:
        return ["needs front matter between two --- lines at the top"]
    out = []
    fields = {}
    for line in lines[1:lines.index("---", 1)]:
        if not line.strip():
            continue
        key, sep, value = line.partition(":")
        key, value = key.strip(), value.strip()
        if not sep or key not in PROVENANCE_KEYS:
            out.append(f"front matter line {line!r}: keys are {', '.join(PROVENANCE_KEYS)}")
            continue
        fields.setdefault(key, []).append(value)
    for key in ("method", "captured_on", "captured_by"):
        if len(fields.get(key, [])) != 1 or not fields[key][0]:
            out.append(f"needs exactly one non-empty {key!r} line")
    method = fields.get("method", [""])[0]
    if method not in METHODS:
        out.append(f"method {method!r}: want one of {', '.join(METHODS)}")
    captured_on = fields.get("captured_on", [""])[0]
    if not DATE.fullmatch(captured_on):
        out.append(f"captured_on {captured_on!r}: want YYYY-MM-DD")
    sources = fields.get("source", [])
    if not sources:
        out.append("needs at least one 'source' line")
    licenses = []
    for value in sources:
        parts = value.split()
        if len(parts) < 2 or not LICENSE_ID.fullmatch(parts[1]):
            out.append(f"source {value!r}: want '<url> <license or none> ...'")
            continue
        url, license_id, rest = parts[0], parts[1], parts[2:]
        licenses.append(license_id)
        if url.startswith("https://github.com/"):
            if not GITHUB_SOURCE.fullmatch(url) or rest:
                out.append(f"source {url}: a GitHub source is a tree or blob URL at a full "
                           "40-character commit, followed only by its license")
        elif not (url.startswith("https://") and len(rest) == 2 and rest[0] == "retrieved"
                  and DATE.fullmatch(rest[1])):
            out.append(f"source {url}: a web source is '<https url> <license> retrieved YYYY-MM-DD'")
    if method == "copied":
        copyable = {c.casefold() for c in COPYABLE}
        refused = [lic for lic in licenses if lic.casefold() not in copyable]
        if refused:
            out.append(f"method 'copied' needs every source under one of {', '.join(COPYABLE)}, and "
                       f"{', '.join(refused)} isn't; describe such a source in our own words instead")
        if not has_license_file:
            out.append("method 'copied' needs the upstream's LICENSE in the same directory")
    return out



def strict_end(pattern):
    """The pattern with each unescaped $ outside a character class written
    as \\Z. Python's $ also matches just before a final newline, while
    ECMA-262's, which JSON Schema 2020-12 specifies, and RE2's match only at
    the very end of the string."""
    out, i, in_class = [], 0, False
    while i < len(pattern):
        c = pattern[i]
        if c == "\\":
            out.append(pattern[i:i + 2])
            i += 2
            continue
        if in_class:
            in_class = c != "]"
        elif c == "[":
            in_class = True
        elif c == "$":
            c = "\\Z"
        out.append(c)
        i += 1
    return "".join(out)

# The validator is checked before it checks anything else. Each case names
# the rule it pins: (name, schema, instance, valid). Every keyword has a case
# that fails when the keyword is ignored, and every rule about how a keyword
# behaves has a case that fails when the rule changes. x-file-ref's cases
# point at this script, which is always there.
SELFTEST = [
    ("type rejects a string where an integer is wanted", {"type": "integer"}, "1", False),
    ("type takes a list of names", {"type": ["string", "null"]}, None, True),
    ("a float with no fraction counts as an integer", {"type": "integer"}, 1.0, True),
    ("a boolean is not an integer", {"type": "integer"}, True, False),
    ("an integer is a number", {"type": "number"}, 3, True),
    ("required reports a missing property", {"type": "object", "required": ["a"]}, {}, False),
    ("properties checks the value of a listed key", {"properties": {"a": {"type": "integer"}}}, {"a": "x"}, False),
    ("additionalProperties false rejects an extra key",
     {"properties": {"a": {}}, "additionalProperties": False}, {"a": 1, "b": 2}, False),
    ("additionalProperties checks the keys properties doesn't list",
     {"properties": {"a": {}}, "additionalProperties": {"type": "integer"}}, {"a": "x", "b": "y"}, False),
    ("additionalProperties leaves the listed keys alone",
     {"properties": {"a": {}}, "additionalProperties": {"type": "integer"}}, {"a": "x"}, True),
    ("properties are open when additionalProperties is absent",
     {"properties": {"a": {"type": "integer"}}}, {"a": 1, "b": "x"}, True),
    ("minProperties refuses a smaller object", {"minProperties": 1}, {}, False),
    ("maxProperties caps an object", {"maxProperties": 1}, {"a": 1, "b": 2}, False),
    ("enum never equates true with 1", {"enum": [1]}, True, False),
    ("const refuses a different value", {"const": {"a": [1, 2]}}, {"a": [1, 3]}, False),
    ("const compares nested values", {"const": {"a": [1, 2]}}, {"a": [1, 2.0]}, True),
    ("const compares array lengths", {"const": [1, 2]}, [1, 2, 3], False),
    ("const compares object keys", {"const": {"a": 1, "b": 2}}, {"a": 1}, False),
    ("pattern searches, so an unanchored pattern matches inside the string", {"pattern": "ab"}, "xab", True),
    ("$ doesn't match before a trailing newline", {"pattern": "^ab$"}, "ab\n", False),
    ("$ before a later branch doesn't match before a trailing newline", {"pattern": "^a$|^b$"}, "a\n", False),
    ("an escaped \\$ is a literal dollar", {"pattern": "^a\\$$"}, "a$", True),
    ("a $ inside a character class is a literal dollar", {"pattern": "^[$]$"}, "$", True),
    ("an anchored pattern refuses extra characters", {"pattern": "^ab$"}, "xab", False),
    ("\\d in a pattern matches ASCII digits only", {"pattern": "^\\d+$"}, "١٢", False),
    ("minLength counts characters, not bytes", {"minLength": 3}, "éé", False),
    ("maxLength counts characters, not bytes", {"maxLength": 2}, "éé", True),
    ("maxLength refuses a longer string", {"maxLength": 2}, "abc", False),
    ("items checks every element", {"items": {"type": "integer"}}, [1, "x"], False),
    ("minItems refuses a shorter array", {"minItems": 2}, [1], False),
    ("maxItems caps an array", {"maxItems": 1}, [1, 2], False),
    ("uniqueItems treats 1 and 1.0 as equal", {"uniqueItems": True}, [1, 1.0], False),
    ("minimum is inclusive", {"minimum": 0}, 0, True),
    ("minimum refuses a smaller value", {"minimum": 0}, -1, False),
    ("maximum is inclusive", {"maximum": 300}, 300, True),
    ("maximum refuses a larger value", {"maximum": 300}, 301, False),
    ("allOf needs every branch", {"allOf": [{"type": "integer"}, {"minimum": 2}]}, 1, False),
    ("anyOf passes when one branch matches", {"anyOf": [{"type": "string"}, {"type": "null"}]}, None, True),
    ("anyOf fails when no branch matches", {"anyOf": [{"type": "string"}, {"type": "null"}]}, 1, False),
    ("oneOf fails when two branches match", {"oneOf": [{"type": "integer"}, {"type": "number"}]}, 1, False),
    ("not rejects a matching value", {"not": {"const": "x"}}, "x", False),
    ("$ref follows a local definition", {"$defs": {"n": {"type": "null"}}, "$ref": "#/$defs/n"}, 0, False),
    ("x-file-ref accepts a file in the directory", {"x-file-ref": True}, os.path.basename(__file__), True),
    ("x-file-ref refuses a path through .., even to a file that exists", {"x-file-ref": True},
     f"../{os.path.basename(HERE)}/{os.path.basename(__file__)}", False),
    ("x-file-ref refuses an absolute path", {"x-file-ref": True}, "/" + os.path.basename(__file__), False),
    ("x-file-ref refuses a directory", {"x-file-ref": True}, ".", False),
]

# What a schema may say is checked the same way: (name, schema, allowed).
SCHEMA_SELFTEST = [
    ("a $ref to an entry of $defs is allowed", {"$defs": {"n": {}}, "$ref": "#/$defs/n"}, True),
    ("a $ref to the $defs map itself is refused", {"$defs": {"n": {}}, "$ref": "#/$defs"}, False),
    ("a $ref to properties is refused", {"properties": {"a": {}}, "$ref": "#/properties"}, False),
    ("a $ref past a $defs entry is refused", {"$defs": {"n": {"items": {}}}, "$ref": "#/$defs/n/items"}, False),
    ("a $ref to a missing entry is refused", {"$ref": "#/$defs/n"}, False),
    ("a keyword check.py doesn't implement is refused", {"contains": {}}, False),
    ("an x- keyword is a note", {"x-note": "anything"}, True),
]

# Examples: (name, schema, whether its examples sit on the right side).
EXAMPLES_SELFTEST = [
    ("an example has to pass its schema", {"type": "integer", "examples": ["x"]}, False),
    ("an invalid example has to fail its schema", {"type": "integer", "x-invalid-examples": [1]}, False),
    ("examples that pass and invalid examples that fail are right",
     {"type": "integer", "examples": [1], "x-invalid-examples": ["x"]}, True),
    ("an invalid example deep inside a schema is checked too",
     {"properties": {"a": {"type": "integer", "x-invalid-examples": [1]}}}, False),
]


def front_matter(method, licenses):
    """A PROVENANCE.md head with one GitHub source per license, for the selftest."""
    return (["---", f"method: {method}", "captured_on: 2026-11-19", "captured_by: selftest"]
            + [f"source: https://github.com/example/repo{i}/tree/{'0' * 40} {lic}" for i, lic in enumerate(licenses)]
            + ["---"])


# And the provenance rules: (name, method, each source's license, whether the
# LICENSE file is there, allowed).
PROVENANCE_SELFTEST = [
    ("copied from an MIT source with its LICENSE file is allowed", "copied", ["MIT"], True, True),
    ("copied takes an SPDX id in any case", "copied", ["mit"], True, True),
    ("copied needs the upstream's LICENSE file", "copied", ["MIT"], False, False),
    ("copied refuses a source with no license", "copied", ["none"], True, False),
    ("copied refuses None, whatever its case", "copied", ["None"], True, False),
    ("copied refuses NOASSERTION, GitHub's id for a license it can't identify", "copied", ["NOASSERTION"], True, False),
    ("copied refuses UNLICENSED", "copied", ["UNLICENSED"], True, False),
    ("copied refuses other", "copied", ["other"], True, False),
    ("copied refuses a license outside the list", "copied", ["CC-BY-NC-4.0"], True, False),
    ("copied needs every source licensed, not just one", "copied", ["MIT", "none"], True, False),
    ("described may cite an unlicensed source", "described", ["none"], False, True),
]


def robert_signature(secret, timestamp, body):
    """X-Robert-Signature for a delivery: sha256= and the lowercase hex
    HMAC-SHA256 of "<timestamp>.<raw body>", keyed by the UTF-8 secret."""
    message = f"{timestamp}.{body}".encode("utf-8")
    return "sha256=" + hmac.new(secret.encode("utf-8"), message, hashlib.sha256).hexdigest()


def mac_matches(case):
    """Whether a verify case's signature is the right MAC for its secret,
    timestamp and body, or None when a header is missing."""
    if case["timestamp"] is None or case["signature"] is None:
        return None
    expected = robert_signature(case["secret"], case["timestamp"], case["body"])
    return hmac.compare_digest(expected.encode("utf-8"), case["signature"].encode("utf-8"))


def robert_accepts(case, tolerance):
    """The receiver rule robert/webhook.json writes down, applied to a case."""
    secret, timestamp, signature = case["secret"], case["timestamp"], case["signature"]
    if not secret or timestamp is None or signature is None:
        return False
    if not re.fullmatch(r"[0-9]+", timestamp):
        return False
    if abs(case["now"] - int(timestamp)) > tolerance:
        return False
    return mac_matches(case)


def robert_problems(doc):
    """What is wrong with webhook.json's vectors and cases, and how many
    there are. Each case's mac_valid is checked as well as its verdict. The
    receiver rule stops at the first test that fails, so a case refused for
    its clock is refused just the same with a wrong MAC, and only mac_valid
    notices."""
    out, count = [], 0
    tolerance = doc["scheme"]["tolerance_s"]
    for v in doc["signing_vectors"]:
        got = robert_signature(v["secret"], v["timestamp"], v["body"])
        if got != v["signature"]:
            out.append(f"signing vector {v['name']!r}: inputs give {got}, the file says {v['signature']}")
        count += 1
    for case in doc["verify_cases"]:
        mac = mac_matches(case)
        if mac is not case["mac_valid"]:
            out.append(f"verify case {case['name']!r}: the file says mac_valid {json.dumps(case['mac_valid'])}, "
                       f"its inputs give {json.dumps(mac)}")
        got = "accept" if robert_accepts(case, tolerance) else "reject"
        if got != case["expect"]:
            out.append(f"verify case {case['name']!r}: the scheme gives {got}, the file says {case['expect']}")
        count += 1
    return out, count


def robert_doc(vector_changes=None, **case_changes):
    """A webhook.json with one signing vector and one verify case, both
    right unless changed, for the selftest."""
    vector = {"name": "selftest", "secret": "s", "timestamp": "100", "body": "{}",
              "signature": robert_signature("s", "100", "{}")}
    case = dict(vector, now=100, mac_valid=True, expect="accept")
    vector.update(vector_changes or {})
    case.update(case_changes)
    return {"scheme": {"tolerance_s": 300}, "signing_vectors": [vector], "verify_cases": [case]}


WRONG_MAC = "sha256=" + "0" * 64

# The Robert checks: (name, webhook.json, whether it passes).
ROBERT_SELFTEST = [
    ("a right MAC inside the window is accepted", robert_doc(), True),
    ("a signing vector whose signature isn't its inputs' MAC is caught", robert_doc({"signature": WRONG_MAC}), False),
    ("a verify case whose verdict isn't the scheme's is caught", robert_doc(expect="reject"), False),
    ("a wrong MAC is caught in a case refused for its clock anyway",
     robert_doc(signature=WRONG_MAC, now=1000, expect="reject"), False),
    ("a right MAC marked mac_valid false is caught", robert_doc(mac_valid=False), False),
    ("a wrong MAC marked mac_valid false is refused", robert_doc(signature=WRONG_MAC, mac_valid=False, expect="reject"), True),
    ("a case with a missing header has mac_valid null", robert_doc(signature=None, mac_valid=None, expect="reject"), True),
    ("mac_valid true on a case with a missing header is caught", robert_doc(signature=None, expect="reject"), False),
]


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
        for name, schema, allowed in SCHEMA_SELFTEST:
            got = not schema_problems(schema, schema, "#")
            if got != allowed:
                self.fail(__file__, f"schema selftest {name!r}: got allowed={got}, want {allowed}")
        for name, schema, right in EXAMPLES_SELFTEST:
            got = not example_problems(schema, HERE)
            if got != right:
                self.fail(__file__, f"examples selftest {name!r}: got right={got}, want {right}")
        for name, method, licenses, has_license_file, allowed in PROVENANCE_SELFTEST:
            got = not provenance_problems(front_matter(method, licenses), has_license_file)
            if got != allowed:
                self.fail(__file__, f"provenance selftest {name!r}: got allowed={got}, want {allowed}")
        for name, doc, passes in ROBERT_SELFTEST:
            got = not robert_problems(doc)[0]
            if got != passes:
                self.fail(__file__, f"Robert selftest {name!r}: got passes={got}, want {passes}")

    def provenance(self, top):
        path = os.path.join(top, "PROVENANCE.md")
        if not os.path.isfile(path):
            self.fail(top, "has no PROVENANCE.md; every capture directory needs one")
            return
        with open(path, encoding="utf-8") as f:
            lines = f.read().splitlines()
        for message in provenance_problems(lines, os.path.isfile(os.path.join(top, "LICENSE"))):
            self.fail(path, message)

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
                if not found:
                    found = example_problems(doc, dirpath)
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

    def robert(self):
        path = os.path.join(HERE, "robert", "webhook.json")
        if not os.path.isfile(path):
            self.fail(path, "missing")
            return
        doc = self.load(path)
        try:
            found, self.vectors = robert_problems(doc)
        except (KeyError, TypeError, ValueError) as err:
            self.fail(path, f"can't read the vectors: {err!r}")
            return
        for message in found:
            self.fail(path, message)


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
