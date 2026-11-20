#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Reference implementation of pairtime's ledger format, parts 1 and 2.

docs/spec/ledger-v1.md is the specification, and the section numbers in
this file are its sections. The code uses only the Python standard library
and shares nothing with pkg/ledger, which is what lets it check pkg/ledger.

    python3 -I tools/ledger_ref.py selftest
        Check the RFC 5869, RFC 4231 and Certificate Transparency vectors,
        the rtype table against pkg/event, the whitespace and lowercase
        tables against Python's Unicode database, the spec's worked
        examples, and that testdata/ledger/vectors.json and
        vectors-content.json regenerate byte for byte and pass check.

    python3 -I tools/ledger_ref.py vectors [--out PATH]
        Write the part 1 vectors from fixed seeds, by default to
        testdata/ledger/vectors.json.

    python3 -I tools/ledger_ref.py vectors-content [--out PATH]
        Write the part 2 vectors from fixed seeds, by default to
        testdata/ledger/vectors-content.json.

    python3 -I tools/ledger_ref.py check FILE
        Recompute every value in FILE, a file in the vectors format (spec
        section 11): keys, digests, nonces, entries, roots, heads, signed
        messages and inclusion proofs; content commitments, reveal
        verdicts, normalizations, features, CC8 reveals and draws. Print
        each mismatch.

Exit status: 0 when everything matches, 1 when something doesn't, 2 for a
usage error or a file that isn't in the vectors format.

Python's standard library has no Ed25519, so this reference builds the
exact bytes each signature covers, and pkg/ledger signs and verifies them.
"""

import base64
import hashlib
import hmac
import json
import os
import re
import sys
from datetime import datetime, timedelta, timezone

# ---------------------------------------------------------------------------
# 1. Conventions

ZERO32 = bytes(32)


class LedgerError(ValueError):
    """An input the spec doesn't allow, such as an unknown rtype."""


def sha256(data):
    return hashlib.sha256(data).digest()


def hmac_sha256(key, msg):
    return hmac.new(key, msg, hashlib.sha256).digest()


def u8(x):
    if not 0 <= x <= 0xFF:
        raise LedgerError("%d doesn't fit in one byte" % x)
    return bytes([x])


def u64(x):
    if not 0 <= x < 1 << 64:
        raise LedgerError("%d doesn't fit in eight bytes" % x)
    return x.to_bytes(8, "big")


def hkdf_extract(salt, ikm):
    """RFC 5869 section 2.2. A salt that isn't provided is 32 zero bytes."""
    return hmac_sha256(salt or ZERO32, ikm)


def hkdf_expand(prk, info, length):
    """RFC 5869 section 2.3."""
    if not 0 <= length <= 255 * 32:
        raise LedgerError("HKDF-SHA256 can't produce %d bytes" % length)
    okm, t, i = b"", b"", 1
    while len(okm) < length:
        t = hmac_sha256(prk, t + info + bytes([i]))
        okm += t
        i += 1
    return okm[:length]


# ---------------------------------------------------------------------------
# 3. Device keys

KEY_INFO = (
    ("k_leaf", b"pairtime/v1/leaf"),
    ("k_tok", b"pairtime/v1/token"),
    ("k_feat", b"pairtime/v1/feat"),
    ("k_vault", b"pairtime/v1/vault"),
)


def derive_keys(k_dev):
    """Each key is HKDF-SHA256(K_dev, salt = 32 zero bytes, info, 32 bytes).
    "prk" is the shared HKDF-Extract output."""
    if len(k_dev) != 32:
        raise LedgerError("K_dev is 32 bytes, got %d" % len(k_dev))
    prk = hkdf_extract(ZERO32, k_dev)
    keys = {"prk": prk}
    for name, info in KEY_INFO:
        keys[name] = hkdf_expand(prk, info, 32)
    return keys


# ---------------------------------------------------------------------------
# 4. Records

# 4.2. The 18 event kinds took 0x01 to 0x12 in PR1's kindTable order. The
# table is frozen: a code never changes meaning, and a new kind takes the
# next free code.
RTYPES = {
    0x01: "session.start",
    0x02: "session.end",
    0x03: "prompt.submit",
    0x04: "agent.output",
    0x05: "agent.tool",
    0x06: "agent.permission",
    0x07: "agent.interrupt",
    0x08: "agent.queue",
    0x09: "agent.stop",
    0x0A: "agent.subagent",
    0x0B: "editor.activity",
    0x0C: "shell.command",
    0x0D: "git.change",
    0x0E: "os.presence",
    0x0F: "os.focus",
    0x10: "os.power",
    0x11: "otel.active_time",
    0x12: "cli.track",
    0x40: "checkpoint",
    0x41: "upload_failure",
}
EVENT_RTYPES = tuple(range(0x01, 0x13))
# Codes reserved for event kinds added later. A verifier that meets one
# reports its kind as unknown and still checks the batch; 0x00 and 0x42 to
# 0xff fail.
LATER_RTYPES = range(0x13, 0x40)
OS_RTYPES = (0x0E, 0x0F, 0x10)
CHECKPOINT = 0x40
UPLOAD_FAILURE = 0x41

# 4.3. pkg/event's opaque keys: 1 to 128 ASCII letters, digits, '_' or '-'.
KEY_RE = re.compile(r"[A-Za-z0-9_-]{1,128}\Z")
NO_SESSION = "none"


def key_bytes(what, s):
    if not isinstance(s, str) or not KEY_RE.match(s):
        raise LedgerError("%s %s isn't an opaque key (1 to 128 of A-Z a-z 0-9 _ -)" % (what, json.dumps(s)))
    return s.encode("ascii")


def project_digest(project_key):
    """First 16 bytes of SHA-256(project_key); 16 zero bytes for no project."""
    if project_key == "":
        return bytes(16)
    return sha256(key_bytes("project_key", project_key))[:16]


def session_digest(session_key):
    """First 8 bytes of SHA-256(session_key); 8 zero bytes for "none"."""
    if session_key == NO_SESSION:
        return bytes(8)
    return sha256(key_bytes("session_key", session_key))[:8]


def header(seq, rtype, project_key, session_key):
    """hdr = u64(seq) || u8(rtype) || project (16) || session (8): 33 bytes."""
    if not 1 <= seq < 1 << 64:
        raise LedgerError("seq %d is out of range; sequence numbers start at 1" % seq)
    if rtype not in RTYPES and rtype not in LATER_RTYPES:
        raise LedgerError("rtype 0x%02x is reserved; version 1 never assigns it" % rtype)
    if rtype in OS_RTYPES and project_key != "":
        raise LedgerError("%s records have no project" % RTYPES[rtype])
    if rtype == CHECKPOINT and (project_key == "" or session_key != NO_SESSION):
        raise LedgerError("a checkpoint has a project and no session")
    if rtype == UPLOAD_FAILURE and (project_key != "" or session_key != NO_SESSION):
        raise LedgerError("an upload-failure note has no project and no session")
    return u64(seq) + u8(rtype) + project_digest(project_key) + session_digest(session_key)


NONCE_TAG = b"\x01"


def nonce(k_leaf, seq):
    """4.4: HMAC-SHA256(K_leaf, 0x01 || u64(seq))."""
    return hmac_sha256(k_leaf, NONCE_TAG + u64(seq))


def entry(hdr, body):
    """4.6: hdr || SHA-256(body), 65 bytes. body is the stored bytes."""
    if len(hdr) != 33:
        raise LedgerError("a header is 33 bytes, got %d" % len(hdr))
    return hdr + sha256(body)


# ---------------------------------------------------------------------------
# 5. Merkle trees, RFC 9162 section 2.1

LEAF_PREFIX = b"\x00"
NODE_PREFIX = b"\x01"


def leaf_hash(data):
    return sha256(LEAF_PREFIX + data)


def node_hash(left, right):
    return sha256(NODE_PREFIX + left + right)


def split_point(n):
    """The largest power of two smaller than n, for n >= 2."""
    k = 1
    while k << 1 < n:
        k <<= 1
    return k


def tree_root(hashes):
    """The root of the tree whose leaf hashes are given (5.1's ROOT)."""
    n = len(hashes)
    if n == 0:
        return sha256(b"")
    if n == 1:
        return hashes[0]
    k = split_point(n)
    return node_hash(tree_root(hashes[:k]), tree_root(hashes[k:]))


def mth(entries):
    """RFC 9162 section 2.1.1's MTH, which applies the leaf prefix itself."""
    return tree_root([leaf_hash(e) for e in entries])


def mth_by_stack(entries):
    """MTH again, by RFC 9162 section 2.1.2's stack algorithm. selftest
    checks that the two agree."""
    if not entries:
        return sha256(b"")
    stack = []
    for i, e in enumerate(entries):
        stack.append(leaf_hash(e))
        m = i
        while m & 1:
            right, left = stack.pop(), stack.pop()
            stack.append(node_hash(left, right))
            m >>= 1
    while len(stack) > 1:
        right, left = stack.pop(), stack.pop()
        stack.append(node_hash(left, right))
    return stack[0]


def inclusion_path(index, hashes):
    """RFC 9162 section 2.1.3.1's PATH(index, D_n) over leaf hashes, ordered
    from the leaf's sibling up to the child of the root."""
    n = len(hashes)
    if not 0 <= index < n:
        raise LedgerError("leaf %d isn't in a tree of %d leaves" % (index, n))
    path = []
    lo, hi = 0, n
    while hi - lo > 1:
        k = split_point(hi - lo)
        if index - lo < k:
            path.append(tree_root(hashes[lo + k:hi]))
            hi = lo + k
        else:
            path.append(tree_root(hashes[lo:lo + k]))
            lo += k
    path.reverse()
    return path


def verify_inclusion(index, size, leaf, path, root):
    """RFC 9162 section 2.1.3.2, plus 5.2's length checks: the leaf hash,
    the root and every path element are 32 bytes."""
    if not 0 <= index < size:
        return False
    if len(leaf) != 32 or len(root) != 32 or any(len(p) != 32 for p in path):
        return False
    fn, sn, r = index, size - 1, leaf
    for p in path:
        if sn == 0:
            return False
        if fn & 1 or fn == sn:
            r = node_hash(p, r)
            while not fn & 1 and fn != 0:
                fn >>= 1
                sn >>= 1
        else:
            r = node_hash(r, p)
        fn >>= 1
        sn >>= 1
    return sn == 0 and r == root


# ---------------------------------------------------------------------------
# 6. Batches and the chain, 7. receipts

CHAIN_PREFIX = b"\x03"
RECEIPT_CONTEXT = b"pairtime-receipt-v1"


def batch_head(prev, root, n, b):
    """H_b = SHA-256(0x03 || H_(b-1) || R_b || u64(n_b) || u64(b))."""
    if len(prev) != 32 or len(root) != 32:
        raise LedgerError("prev and root are 32 bytes each")
    if n < 1:
        raise LedgerError("a batch has at least one record")
    if b < 1:
        raise LedgerError("batch numbers start at 1")
    return sha256(CHAIN_PREFIX + prev + root + u64(n) + u64(b))


def device_field(device_id):
    """u8(length) || the device id's ASCII bytes."""
    d = key_bytes("device_id", device_id)
    return u8(len(d)) + d


def receipt_message(device_id, b, head, received_at):
    """The bytes the server's receipt signature covers."""
    if b < 1:
        raise LedgerError("batch numbers start at 1")
    if len(head) != 32:
        raise LedgerError("a head is 32 bytes")
    return RECEIPT_CONTEXT + device_field(device_id) + u64(b) + head + u64(received_at)


# ---------------------------------------------------------------------------
# 8. Day trees

DAY_MS = 86400000
DAY_PREFIX = b"\x04"
DAY_CONTEXT = b"pairtime-day-v1"
EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def day_of(received_at):
    """The UTC day number, days since 1970-01-01, of a time in Unix ms."""
    return received_at // DAY_MS


def day_date(day):
    return (EPOCH + timedelta(days=day)).date().isoformat()


def day_leaf(device_id, b, head, received_at):
    if len(head) != 32:
        raise LedgerError("a head is 32 bytes")
    return device_field(device_id) + u64(b) + head + u64(received_at)


def day_order(device_id, b, received_at):
    """Rows sort by received_at, then device_id's bytes, then b."""
    return (received_at, key_bytes("device_id", device_id), b)


def day_head(prev, root, count, day):
    """G_d = SHA-256(0x04 || G_(d-1) || T_d || u64(count_d) || u64(d))."""
    if len(prev) != 32 or len(root) != 32:
        raise LedgerError("prev and root are 32 bytes each")
    return sha256(DAY_PREFIX + prev + root + u64(count) + u64(day))


def day_message(day, head):
    """The bytes the server's day signature covers."""
    return DAY_CONTEXT + u64(day) + head


# ---------------------------------------------------------------------------
# 10. Content: token commitments, reveals, keyed features and the draw
#
# Part 2 of the spec. Section numbers below are its subsections of 10.

# 10.1 Which records commit content, and to which text.

PROMPT_SUBMIT = 0x03
AGENT_OUTPUT = 0x04
AGENT_TOOL = 0x05
SHELL_COMMAND = 0x0C
CLI_TRACK = 0x12
CONTENT_RTYPES = (PROMPT_SUBMIT, AGENT_OUTPUT, CLI_TRACK)
MESSAGE_MAX = 16384


def committed_message(raw):
    """The text a turn's final agent message commits: its longest prefix of
    at most 16,384 bytes that ends on a character boundary."""
    if len(raw) <= MESSAGE_MAX:
        return raw
    k = MESSAGE_MAX
    while k > 0 and raw[k] & 0xC0 == 0x80:
        k -= 1
    return raw[:k]


def utf8_text(data, what="text"):
    """Strict UTF-8: no surrogates, no overlong forms, nothing above U+10FFFF."""
    try:
        return data.decode("utf-8")
    except UnicodeDecodeError:
        raise LedgerError("%s isn't valid UTF-8" % what)


# 10.2 Tokens and the commitment. Whitespace is Unicode's White_Space
# property, written out so no implementation leans on its library's tables.
# Python's str.isspace() is not this set: it adds U+001C to U+001F.

WHITESPACE = frozenset([0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0x85, 0xA0, 0x1680]
                       + list(range(0x2000, 0x200B)) + [0x2028, 0x2029, 0x202F, 0x205F, 0x3000])


def is_space(ch):
    return ord(ch) in WHITESPACE


def tokenize(text):
    """Maximal runs of whitespace and of non-whitespace, in order. They
    concatenate to text, and neighbours are always of different kinds."""
    out, start = [], 0
    for i in range(1, len(text) + 1):
        if i == len(text) or is_space(text[i]) != is_space(text[start]):
            out.append(text[start:i])
            start = i
    return out


def words_of(text):
    """The non-whitespace tokens of text."""
    return [t for t in tokenize(text) if not is_space(t[0])]


def token_salt(k_tok, seq, j):
    """HMAC-SHA256(K_tok, u64(s) || u64(j)); tokens are numbered from 1."""
    if not 1 <= seq < 1 << 64:
        raise LedgerError("seq %d is out of range" % seq)
    if j < 1:
        raise LedgerError("tokens are numbered from 1")
    return hmac_sha256(k_tok, u64(seq) + u64(j))


def token_leaf(salt, token):
    """t = SHA-256(0x00 || salt || token), which is LEAF(salt || token)."""
    if len(salt) != 32:
        raise LedgerError("a token salt is 32 bytes")
    return leaf_hash(salt + token)


def commit_text(k_tok, seq, text):
    """The tokens of text (UTF-8 bytes) as bytes, their salts and leaves, and
    the commitment (n, C)."""
    toks = [t.encode("utf-8") for t in tokenize(utf8_text(text))]
    salts = [token_salt(k_tok, seq, j) for j in range(1, len(toks) + 1)]
    leaves = [token_leaf(s, t) for s, t in zip(salts, toks)]
    return toks, salts, leaves, len(toks), tree_root(leaves)


# 10.3 The redacted reveal. A kept item is ("kept", salt, token); a removed
# span is ("removed", type, count, leaves, lines), lines None unless given.

SPAN_TYPES = ("known", "secret", "pii", "code", "teen")
REVEAL_VERDICTS = ("ok", "bad_item", "count", "alternation", "root")


def token_kind(token):
    """True for a whitespace token, False for a word, None for a byte string
    that isn't one token: empty, not UTF-8, or both kinds at once."""
    try:
        text = token.decode("utf-8")
    except UnicodeDecodeError:
        return None
    kinds = {is_space(c) for c in text}
    return kinds.pop() if len(kinds) == 1 else None


def verify_reveal(n, root, items):
    """Checks a reveal against the commitment (n, C) in the order the spec
    gives and returns the first verdict that applies, with the index of the
    offending item for bad_item and alternation."""
    leaves, kept, pos = [], [], 0
    for i, item in enumerate(items):
        if item[0] == "kept":
            _, salt, token = item
            kind = token_kind(token)
            if len(salt) != 32 or kind is None:
                return "bad_item", i
            kept.append((pos, kind, i))
            leaves.append(token_leaf(salt, token))
            pos += 1
        else:
            _, span_type, count, hashes, lines = item
            if span_type not in SPAN_TYPES or count < 1 or len(hashes) != count:
                return "bad_item", i
            if any(len(h) != 32 for h in hashes):
                return "bad_item", i
            if lines is not None and (span_type != "code" or lines < 1):
                return "bad_item", i
            leaves.extend(hashes)
            pos += count
    if pos != n:
        return "count", None
    for (p0, k0, _), (p1, k1, i1) in zip(kept, kept[1:]):
        if (k0 == k1) != ((p1 - p0) % 2 == 0):
            return "alternation", i1
    if tree_root(leaves) != root:
        return "root", None
    return "ok", None


def reveal_items(salts, toks, leaves, spans):
    """A reveal that removes the given spans, each (first, count, type,
    lines) with first counted from 0, and keeps every other token."""
    items, j = [], 0
    for first, count, span_type, lines in sorted(spans):
        if first < j or first + count > len(toks):
            raise LedgerError("spans overlap or run past the text")
        items += [("kept", salts[k], toks[k]) for k in range(j, first)]
        items.append(("removed", span_type, count, leaves[first:first + count], lines))
        j = first + count
    items += [("kept", salts[k], toks[k]) for k in range(j, len(toks))]
    return items


# 10.4 Normalization (SM 4.2): drop pasted blocks and error text by line,
# lowercase, substitute, mark retries, collapse whitespace.

TRACEBACK = "Traceback (most recent call last):"
FENCE_OPEN_RE = re.compile(r" {0,3}(`{3,}|~{3,})(.*)")
FENCE_CLOSE_RE = re.compile(r" {0,3}(`{3,}|~{3,})[ \t]*")
PASTE_OPEN_RE = re.compile(r"<pasted_content(?: [^>]*)?>")
# P2. Claude Code 2.1.327 closes a paste with </pasted_content id="1a2b">,
# repeating the opener's id; older sessions wrote </pasted_content>. Ids
# aren't compared.
PASTE_CLOSE_RE = re.compile(r"</pasted_content(?: [^>]*)?>")
# SM 4.2's first error lines, matched against a whole line with its leading
# and trailing whitespace removed. ASCII classes only; nothing here needs
# lookaround or backreferences, so Go's regexp runs the same patterns.
ERROR_LINE_RES = tuple(re.compile(p) for p in (
    r"(?:Uncaught )?(?:[A-Za-z_$][A-Za-z0-9_$]*\.)*[A-Za-z0-9_$]*(?:Error|Exception)(?::.*)?",
    r"error\[E[0-9]{4}\](?::.*)?",
    r"[^ \t]+:[0-9]+:[0-9]+: (?:fatal )?error:.*",
    r"FAILED .+",
    r"--- FAIL: .+",
    r"panic: .+",
))


def split_lines(text):
    """Lines split at LF, each without one trailing CR."""
    return [line[:-1] if line.endswith("\r") else line for line in text.split("\n")]


def strip_space(s):
    i, j = 0, len(s)
    while i < j and is_space(s[i]):
        i += 1
    while j > i and is_space(s[j - 1]):
        j -= 1
    return s[i:j]


def opening_fence(line):
    m = FENCE_OPEN_RE.fullmatch(line)
    if m is None or (m.group(1)[0] == "`" and "`" in m.group(2)):
        return None
    return m.group(1)


def closes_fence(line, fence):
    m = FENCE_CLOSE_RE.fullmatch(line)
    return m is not None and m.group(1)[0] == fence[0] and len(m.group(1)) >= len(fence)


def is_error_line(s):
    return any(r.fullmatch(s) is not None for r in ERROR_LINE_RES)


def indented(line):
    return line[:1] in (" ", "\t")


def removed_lines(lines):
    """Indices of the lines in fenced blocks, pasted_content blocks,
    tracebacks and error text."""
    removed, i = [], 0
    while i < len(lines):
        start, s = i, strip_space(lines[i])
        fence = opening_fence(lines[i])
        if fence is not None:
            i += 1
            while i < len(lines) and not closes_fence(lines[i], fence):
                i += 1
            i = min(i + 1, len(lines))
        elif PASTE_OPEN_RE.fullmatch(s) is not None:
            i += 1
            while i < len(lines) and PASTE_CLOSE_RE.fullmatch(strip_space(lines[i])) is None:
                i += 1
            i = min(i + 1, len(lines))
        elif s == TRACEBACK or is_error_line(s):
            i += 1
            while i < len(lines) and indented(lines[i]):
                i += 1
        else:
            i += 1
            continue
        removed.extend(range(start, i))
    return removed


# Unicode 15.0.0's simple lowercase mapping (UnicodeData.txt field 13) as
# runs (first, count, delta, stride): for i in range(count), first + i*stride
# maps to first + i*stride + delta. selftest checks it against Python's
# unicodedata when that is 15.0.0. Library case tables follow newer Unicode
# versions (Go 1.27's are 17.0.0), and f.v = 1 must never change.
LOWER_UNICODE = "15.0.0"
LOWER_RUNS = (
    (0x0041, 26, 32, 1), (0x00C0, 23, 32, 1), (0x00D8, 7, 32, 1), (0x0100, 24, 1, 2),
    (0x0130, 1, -199, 1), (0x0132, 3, 1, 2), (0x0139, 8, 1, 2), (0x014A, 23, 1, 2),
    (0x0178, 1, -121, 1), (0x0179, 3, 1, 2), (0x0181, 1, 210, 1), (0x0182, 2, 1, 2),
    (0x0186, 1, 206, 1), (0x0187, 1, 1, 1), (0x0189, 2, 205, 1), (0x018B, 1, 1, 1),
    (0x018E, 1, 79, 1), (0x018F, 1, 202, 1), (0x0190, 1, 203, 1), (0x0191, 1, 1, 1),
    (0x0193, 1, 205, 1), (0x0194, 1, 207, 1), (0x0196, 1, 211, 1), (0x0197, 1, 209, 1),
    (0x0198, 1, 1, 1), (0x019C, 1, 211, 1), (0x019D, 1, 213, 1), (0x019F, 1, 214, 1),
    (0x01A0, 3, 1, 2), (0x01A6, 1, 218, 1), (0x01A7, 1, 1, 1), (0x01A9, 1, 218, 1),
    (0x01AC, 1, 1, 1), (0x01AE, 1, 218, 1), (0x01AF, 1, 1, 1), (0x01B1, 2, 217, 1),
    (0x01B3, 2, 1, 2), (0x01B7, 1, 219, 1), (0x01B8, 1, 1, 1), (0x01BC, 1, 1, 1),
    (0x01C4, 1, 2, 1), (0x01C5, 1, 1, 1), (0x01C7, 1, 2, 1), (0x01C8, 1, 1, 1), (0x01CA, 1, 2, 1),
    (0x01CB, 9, 1, 2), (0x01DE, 9, 1, 2), (0x01F1, 1, 2, 1), (0x01F2, 2, 1, 2),
    (0x01F6, 1, -97, 1), (0x01F7, 1, -56, 1), (0x01F8, 20, 1, 2), (0x0220, 1, -130, 1),
    (0x0222, 9, 1, 2), (0x023A, 1, 10795, 1), (0x023B, 1, 1, 1), (0x023D, 1, -163, 1),
    (0x023E, 1, 10792, 1), (0x0241, 1, 1, 1), (0x0243, 1, -195, 1), (0x0244, 1, 69, 1),
    (0x0245, 1, 71, 1), (0x0246, 5, 1, 2), (0x0370, 2, 1, 2), (0x0376, 1, 1, 1),
    (0x037F, 1, 116, 1), (0x0386, 1, 38, 1), (0x0388, 3, 37, 1), (0x038C, 1, 64, 1),
    (0x038E, 2, 63, 1), (0x0391, 17, 32, 1), (0x03A3, 9, 32, 1), (0x03CF, 1, 8, 1),
    (0x03D8, 12, 1, 2), (0x03F4, 1, -60, 1), (0x03F7, 1, 1, 1), (0x03F9, 1, -7, 1),
    (0x03FA, 1, 1, 1), (0x03FD, 3, -130, 1), (0x0400, 16, 80, 1), (0x0410, 32, 32, 1),
    (0x0460, 17, 1, 2), (0x048A, 27, 1, 2), (0x04C0, 1, 15, 1), (0x04C1, 7, 1, 2),
    (0x04D0, 48, 1, 2), (0x0531, 38, 48, 1), (0x10A0, 38, 7264, 1), (0x10C7, 1, 7264, 1),
    (0x10CD, 1, 7264, 1), (0x13A0, 80, 38864, 1), (0x13F0, 6, 8, 1), (0x1C90, 43, -3008, 1),
    (0x1CBD, 3, -3008, 1), (0x1E00, 75, 1, 2), (0x1E9E, 1, -7615, 1), (0x1EA0, 48, 1, 2),
    (0x1F08, 8, -8, 1), (0x1F18, 6, -8, 1), (0x1F28, 8, -8, 1), (0x1F38, 8, -8, 1),
    (0x1F48, 6, -8, 1), (0x1F59, 4, -8, 2), (0x1F68, 8, -8, 1), (0x1F88, 8, -8, 1),
    (0x1F98, 8, -8, 1), (0x1FA8, 8, -8, 1), (0x1FB8, 2, -8, 1), (0x1FBA, 2, -74, 1),
    (0x1FBC, 1, -9, 1), (0x1FC8, 4, -86, 1), (0x1FCC, 1, -9, 1), (0x1FD8, 2, -8, 1),
    (0x1FDA, 2, -100, 1), (0x1FE8, 2, -8, 1), (0x1FEA, 2, -112, 1), (0x1FEC, 1, -7, 1),
    (0x1FF8, 2, -128, 1), (0x1FFA, 2, -126, 1), (0x1FFC, 1, -9, 1), (0x2126, 1, -7517, 1),
    (0x212A, 1, -8383, 1), (0x212B, 1, -8262, 1), (0x2132, 1, 28, 1), (0x2160, 16, 16, 1),
    (0x2183, 1, 1, 1), (0x24B6, 26, 26, 1), (0x2C00, 48, 48, 1), (0x2C60, 1, 1, 1),
    (0x2C62, 1, -10743, 1), (0x2C63, 1, -3814, 1), (0x2C64, 1, -10727, 1), (0x2C67, 3, 1, 2),
    (0x2C6D, 1, -10780, 1), (0x2C6E, 1, -10749, 1), (0x2C6F, 1, -10783, 1), (0x2C70, 1, -10782, 1),
    (0x2C72, 1, 1, 1), (0x2C75, 1, 1, 1), (0x2C7E, 2, -10815, 1), (0x2C80, 50, 1, 2),
    (0x2CEB, 2, 1, 2), (0x2CF2, 1, 1, 1), (0xA640, 23, 1, 2), (0xA680, 14, 1, 2),
    (0xA722, 7, 1, 2), (0xA732, 31, 1, 2), (0xA779, 2, 1, 2), (0xA77D, 1, -35332, 1),
    (0xA77E, 5, 1, 2), (0xA78B, 1, 1, 1), (0xA78D, 1, -42280, 1), (0xA790, 2, 1, 2),
    (0xA796, 10, 1, 2), (0xA7AA, 1, -42308, 1), (0xA7AB, 1, -42319, 1), (0xA7AC, 1, -42315, 1),
    (0xA7AD, 1, -42305, 1), (0xA7AE, 1, -42308, 1), (0xA7B0, 1, -42258, 1), (0xA7B1, 1, -42282, 1),
    (0xA7B2, 1, -42261, 1), (0xA7B3, 1, 928, 1), (0xA7B4, 8, 1, 2), (0xA7C4, 1, -48, 1),
    (0xA7C5, 1, -42307, 1), (0xA7C6, 1, -35384, 1), (0xA7C7, 2, 1, 2), (0xA7D0, 1, 1, 1),
    (0xA7D6, 2, 1, 2), (0xA7F5, 1, 1, 1), (0xFF21, 26, 32, 1), (0x10400, 40, 40, 1),
    (0x104B0, 36, 40, 1), (0x10570, 11, 39, 1), (0x1057C, 15, 39, 1), (0x1058C, 7, 39, 1),
    (0x10594, 2, 39, 1), (0x10C80, 51, 64, 1), (0x118A0, 32, 32, 1), (0x16E40, 32, 32, 1),
    (0x1E900, 34, 34, 1),
)


# The simple lowercase mappings Unicode added after 15.0.0, up to 17.0.0, as
# (code point, lowercase). They are what Go 1.27's unicode tables, which are
# 17.0.0, add to LOWER_RUNS; those tables agree with LOWER_RUNS on every
# other code point. f.v = 1 never applies them. selftest uses them to check
# LOWER_RUNS exactly against any database from 15.0.0 to 17.0.0, and the
# vectors carry them so pkg/ledger can test unicode.ToLower for drift.
LOWER_LATER_UNICODE = "17.0.0"
LOWER_LATER = (
    (0x1C89, 0x1C8A), (0xA7CB, 0x0264), (0xA7CC, 0xA7CD), (0xA7CE, 0xA7CF), (0xA7D2, 0xA7D3),
    (0xA7D4, 0xA7D5), (0xA7DA, 0xA7DB), (0xA7DC, 0x019B), (0x10D50, 0x10D70), (0x10D51, 0x10D71),
    (0x10D52, 0x10D72), (0x10D53, 0x10D73), (0x10D54, 0x10D74), (0x10D55, 0x10D75),
    (0x10D56, 0x10D76), (0x10D57, 0x10D77), (0x10D58, 0x10D78), (0x10D59, 0x10D79),
    (0x10D5A, 0x10D7A), (0x10D5B, 0x10D7B), (0x10D5C, 0x10D7C), (0x10D5D, 0x10D7D),
    (0x10D5E, 0x10D7E), (0x10D5F, 0x10D7F), (0x10D60, 0x10D80), (0x10D61, 0x10D81),
    (0x10D62, 0x10D82), (0x10D63, 0x10D83), (0x10D64, 0x10D84), (0x10D65, 0x10D85),
    (0x16EA0, 0x16EBB), (0x16EA1, 0x16EBC), (0x16EA2, 0x16EBD), (0x16EA3, 0x16EBE),
    (0x16EA4, 0x16EBF), (0x16EA5, 0x16EC0), (0x16EA6, 0x16EC1), (0x16EA7, 0x16EC2),
    (0x16EA8, 0x16EC3), (0x16EA9, 0x16EC4), (0x16EAA, 0x16EC5), (0x16EAB, 0x16EC6),
    (0x16EAC, 0x16EC7), (0x16EAD, 0x16EC8), (0x16EAE, 0x16EC9), (0x16EAF, 0x16ECA),
    (0x16EB0, 0x16ECB), (0x16EB1, 0x16ECC), (0x16EB2, 0x16ECD), (0x16EB3, 0x16ECE),
    (0x16EB4, 0x16ECF), (0x16EB5, 0x16ED0), (0x16EB6, 0x16ED1), (0x16EB7, 0x16ED2),
    (0x16EB8, 0x16ED3),
)


def lower_map(runs):
    out = {}
    for first, count, delta, stride in runs:
        for i in range(count):
            cp = first + i * stride
            out[cp] = cp + delta
    return out


LOWER = lower_map(LOWER_RUNS)


def lowercase(s):
    return "".join(chr(LOWER.get(ord(c), ord(c))) for c in s)


ALPHA = frozenset("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")
DIGIT = frozenset("0123456789")
ALNUM = ALPHA | DIGIT
HEXDIGIT = frozenset("0123456789abcdefABCDEF")
HEXLETTER = frozenset("abcdefABCDEF")
SEPARATOR = frozenset("/\\")
PATH_CHAR = ALNUM | SEPARATOR | frozenset("._-~$%+@:")
PATH_START = ("/", "\\", "./", ".\\", "../", "..\\", "~/", "~\\")
URL_RE = re.compile(r"[A-Za-z][A-Za-z0-9+.-]*://")
ID_RE = re.compile(r"`[^`]+`")
DIGITS_RE = re.compile(r"[0-9]+")


def replace_runs(word, chars, fn):
    """word with each maximal run of chars replaced by fn(run)."""
    out, i = [], 0
    while i < len(word):
        j = i
        while j < len(word) and word[j] in chars:
            j += 1
        if j > i:
            out.append(fn(word[i:j]))
            i = j
        else:
            out.append(word[i])
            i += 1
    return "".join(out)


def is_path(run):
    if not any(c in SEPARATOR for c in run) or not any(c in ALNUM for c in run):
        return False
    if run.startswith(PATH_START):
        return True
    if len(run) >= 3 and run[0] in ALPHA and run[1] == ":" and run[2] in SEPARATOR:
        return True
    if sum(1 for c in run if c in SEPARATOR) >= 2:
        return True
    last = run[max(run.rfind("/"), run.rfind("\\")) + 1:]
    return any(last[k] == "." and last[k + 1] in ALNUM for k in range(len(last) - 1))


def is_hex(run):
    if len(run) >= 3 and run[0] == "0" and run[1] in "xX" and all(c in HEXDIGIT for c in run[2:]):
        return True
    return (len(run) >= 7 and all(c in HEXDIGIT for c in run)
            and any(c in DIGIT for c in run) and any(c in HEXLETTER for c in run))


def substitute(word):
    """SM 4.2's substitutions inside one word, in this order: a URL to the end
    of the word, backticked identifiers, paths, hex strings, numbers."""
    m = URL_RE.search(word)
    if m is not None:
        word = word[:m.start()] + "<url>"
    word = ID_RE.sub("<id>", word)
    word = replace_runs(word, PATH_CHAR, lambda r: "<path>" if is_path(r) else r)
    word = replace_runs(word, ALNUM, lambda r: "<hex>" if is_hex(r) else r)
    return DIGITS_RE.sub("<n>", word)


RETRY_PHRASES = (("still", "not", "working"), ("try", "again"), ("fix", "it"), ("still", "broken"),
                 ("doesnt", "work"), ("again",), ("retry",))
EDGE_PUNCT = frozenset("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~\u2018\u2019\u201c\u201d")
PLACEHOLDERS = ("<url>", "<id>", "<path>", "<hex>", "<n>")


def bare(word):
    """Step 6: the word without its edge punctuation, except a placeholder
    of step 5 at either end, and without the apostrophes left inside."""
    i, j = 0, len(word)
    while i < j and word[i] in EDGE_PUNCT and not word[i:j].startswith(PLACEHOLDERS):
        i += 1
    while j > i and word[j - 1] in EDGE_PUNCT and not word[i:j].endswith(PLACEHOLDERS):
        j -= 1
    return word[i:j].replace("'", "").replace("\u2019", "")


def replace_retries(words, sources):
    """Step 7, carrying each word's source tokens: a <retry> comes from
    every token of the words it replaced."""
    out, out_sources, i = [], [], 0
    while i < len(words):
        for phrase in RETRY_PHRASES:
            if tuple(words[i:i + len(phrase)]) == phrase:
                out.append("<retry>")
                out_sources.append(frozenset().union(*sources[i:i + len(phrase)]))
                i += len(phrase)
                break
        else:
            out.append(words[i])
            out_sources.append(sources[i])
            i += 1
    return out, out_sources


def normalize_sources(text):
    """10.4, with the tokens (10.2, numbered from 0) each normalized word
    comes from. A word of step 3 is one token of the whole text, since LF
    and CR are whitespace, so walking the text's tokens and counting LFs
    gives the same words as walking its lines."""
    lines = split_lines(text)
    removed = removed_lines(lines)
    gone = set(removed)
    words, sources, line = [], [], 0
    for j, tok in enumerate(tokenize(text)):
        if is_space(tok[0]):
            line += tok.count("\n")
            continue
        if line in gone:
            continue
        w = bare(substitute(lowercase(tok)))
        if w:
            words.append(w)
            sources.append(frozenset([j]))
    words, sources = replace_retries(words, sources)
    return removed, words, sources


def normalize(text):
    """10.4. Returns the removed line indices and the normalized words; the
    normalized text is the words joined by single spaces. Each word is
    lowercased, substituted and stripped to its bare form, and a word left
    empty goes."""
    removed, words, _ = normalize_sources(text)
    return removed, words


def error_signature(text):
    """10.6: the first error line of text, anywhere in it, with its words
    substituted as in 10.4 but not lowercased. None when there is none."""
    for line in split_lines(text):
        s = strip_space(line)
        if is_error_line(s):
            return " ".join(substitute(w) for w in words_of(s))
    return None


# 10.5 Shingles, per-shingle values and the keyed MinHash.

WORD_SHINGLE = 0x05
CHAR_SHINGLE = 0x06
SHINGLE_KINDS = {WORD_SHINGLE: "word3", CHAR_SHINGLE: "char5"}
MINHASH_PREFIX = b"\x05"
MINHASH_SIZE = 64
NO_MIN = 0xFFFFFFFF


def shingles(words):
    """(tag, distinct shingles in ascending order): word 3-shingles from six
    words up, else character 5-shingles of the normalized text."""
    if len(words) >= 6:
        return WORD_SHINGLE, sorted({" ".join(words[i:i + 3]) for i in range(len(words) - 2)})
    text = " ".join(words)
    if len(text) >= 5:
        return CHAR_SHINGLE, sorted({text[i:i + 5] for i in range(len(text) - 4)})
    return CHAR_SHINGLE, [text] if text else []


def shingle_value(k_feat, tag, shingle):
    """v(x) = HMAC-SHA256(K_feat, tag || x), 32 bytes."""
    if tag not in SHINGLE_KINDS:
        raise LedgerError("shingle tag 0x%02x isn't 0x05 or 0x06" % tag)
    return hmac_sha256(k_feat, u8(tag) + shingle.encode("utf-8"))


def minhash_values(v):
    """The 64 values of one shingle: value 8k + r is bytes 4r to 4r+4 of
    SHA-256(0x05 || v || u8(k)), big-endian."""
    if len(v) != 32:
        raise LedgerError("a per-shingle value is 32 bytes")
    out = []
    for k in range(8):
        block = sha256(MINHASH_PREFIX + v + u8(k))
        out += [int.from_bytes(block[i:i + 4], "big") for i in range(0, 32, 4)]
    return out


def sketch(values):
    """The 64-value MinHash of a set of per-shingle values; 0xffffffff
    throughout for the empty set."""
    mh = [NO_MIN] * MINHASH_SIZE
    for v in values:
        for i, h in enumerate(minhash_values(v)):
            if h < mh[i]:
                mh[i] = h
    return mh


def sketch_bytes(mh):
    return b"".join(x.to_bytes(4, "big") for x in mh)


def sketch_from_bytes(data):
    if len(data) != 4 * MINHASH_SIZE:
        raise LedgerError("a sketch is 256 bytes")
    return [int.from_bytes(data[i:i + 4], "big") for i in range(0, len(data), 4)]


def jaccard_matches(a, b):
    """The estimate of the Jaccard similarity is matches / 64."""
    return sum(1 for x, y in zip(a, b) if x == y)


# 10.6 The 16-byte keyed values and edit hunks.

FEAT_ERROR = 0x01
FEAT_TEMPLATE = 0x02
FEAT_COMMAND = 0x03
FEAT_PATH = 0x04


def keyed16(k_feat, tag, text):
    """HMAC-SHA256(K_feat, tag || text)[0:16]."""
    if tag not in (FEAT_ERROR, FEAT_TEMPLATE, FEAT_COMMAND, FEAT_PATH):
        raise LedgerError("feature tag 0x%02x isn't 0x01 to 0x04" % tag)
    return hmac_sha256(k_feat, u8(tag) + text.encode("utf-8"))[:16]


def command_text(cmd):
    """A command line with its whitespace collapsed to single spaces."""
    return " ".join(words_of(cmd))


def check_path_form(path):
    """10.6's path form: '/' separators and never '\\', no empty, '.' or '..'
    component, no trailing '/'; relative to the project root, or absolute
    from '/' or from an upper-case drive letter, ':' and '/'."""
    body = path
    if len(path) >= 2 and path[0] in ALPHA and path[1] == ":":
        body = path[3:] if "A" <= path[0] <= "Z" and path[2:3] == "/" else ""
    elif path.startswith("/"):
        body = path[1:]
    if "\\" in path or any(part in ("", ".", "..") for part in body.split("/")):
        raise LedgerError("path %s isn't in the form of spec 10.6" % json.dumps(path))
    return path


def hunk(k_feat, path, old_start, old_lines, new_start, new_lines):
    """One edit hunk as f carries it: the path's keyed value and the hunk
    header's four numbers, @@ -old_start,old_lines +new_start,new_lines @@."""
    for name, v in (("old_start", old_start), ("old_lines", old_lines),
                    ("new_start", new_start), ("new_lines", new_lines)):
        if not 0 <= v < 1 << 31:
            raise LedgerError("%s %d is out of range" % (name, v))
    if (old_lines and not old_start) or (new_lines and not new_start):
        raise LedgerError("a hunk with lines starts at line 1 or later")
    return {"p": keyed16(k_feat, FEAT_PATH, check_path_form(path)).hex(),
            "os": old_start, "ol": old_lines, "ns": new_start, "nl": new_lines}


def file_lines(text):
    """A file's lines: each piece up to and including an LF, and a last
    piece without one when the text doesn't end with LF."""
    out, start = [], 0
    while start < len(text):
        end = text.find("\n", start)
        if end < 0:
            out.append(text[start:])
            break
        out.append(text[start:end + 1])
        start = end + 1
    return out


def hunk_from_texts(before, after):
    """The zero-context hunk of one edit, from the file's text before and
    after it: what is left once the longest shared leading run of lines,
    then the longest shared trailing run, are dropped. None when no line
    changed."""
    a, b = file_lines(before), file_lines(after)
    p = 0
    while p < len(a) and p < len(b) and a[p] == b[p]:
        p += 1
    q = 0
    while q < len(a) - p and q < len(b) - p and a[len(a) - 1 - q] == b[len(b) - 1 - q]:
        q += 1
    old_lines, new_lines = len(a) - p - q, len(b) - p - q
    if not old_lines and not new_lines:
        return None
    return (p + 1 if old_lines else p, old_lines, p + 1 if new_lines else p, new_lines)


def hunks_from_patch(old_start, old_lines, new_start, new_lines, lines):
    """The zero-context hunks of one unified-diff hunk, from its header's
    four numbers and its lines, each starting with ' ', '-', '+' or '\\'.
    Each maximal run of '-' and '+' lines is one hunk."""
    o = old_start if old_lines else old_start + 1
    n = new_start if new_lines else new_start + 1
    seen_old = seen_new = 0
    out, run = [], None

    def close():
        ro, rn, removed, added = run
        out.append((ro if removed else ro - 1, removed, rn if added else rn - 1, added))

    for line in lines:
        op = line[:1]
        if op == "\\":
            continue
        if op not in (" ", "-", "+"):
            raise LedgerError("a patch line starts with ' ', '-', '+' or '\\'")
        if op == " ":
            if run is not None:
                close()
                run = None
            o, n, seen_old, seen_new = o + 1, n + 1, seen_old + 1, seen_new + 1
            continue
        if run is None:
            run = [o, n, 0, 0]
        if op == "-":
            run[2] += 1
            o, seen_old = o + 1, seen_old + 1
        else:
            run[3] += 1
            n, seen_new = n + 1, seen_new + 1
    if run is not None:
        close()
    if (seen_old, seen_new) != (old_lines, new_lines):
        raise LedgerError("the patch hunk's lines don't add up to its header's counts")
    return out


# 10.7 The feature map f, version 1. Members in this order: v, mh, th, es,
# pc, h, ch, each present only where 10.7 says.

FEATURES_V = 1
PROMPT_CLASSES = ("substantive", "low_effort", "answer", "error_paste")
FEATURE_RTYPES = (PROMPT_SUBMIT, AGENT_TOOL, SHELL_COMMAND, CLI_TRACK)


def prompt_features(k_feat, text, prompt_class):
    """f for prompt.submit and cli.track, and the per-shingle values. text
    is None for a prompt whose text the device never had (10.1), whose f
    holds only v and pc."""
    if prompt_class not in PROMPT_CLASSES:
        raise LedgerError("prompt class %s isn't one of %s" % (json.dumps(prompt_class), ", ".join(PROMPT_CLASSES)))
    if text is None:
        return {"v": FEATURES_V, "pc": prompt_class}, []
    _, words = normalize(text)
    tag, xs = shingles(words)
    values = [shingle_value(k_feat, tag, x) for x in xs]
    f = {"v": FEATURES_V, "mh": sketch_bytes(sketch(values)).hex(),
         "th": keyed16(k_feat, FEAT_TEMPLATE, " ".join(words)).hex()}
    sig = error_signature(text)
    if sig is not None:
        f["es"] = keyed16(k_feat, FEAT_ERROR, sig).hex()
    f["pc"] = prompt_class
    return f, values


def tool_features(k_feat, hunks, command):
    """f for agent.tool (hunks may be empty) and shell.command (hunks None)."""
    f = {"v": FEATURES_V}
    if hunks:
        f["h"] = [hunk(k_feat, *h) for h in hunks]
    cmd = command_text(command or "")
    if cmd:
        f["ch"] = keyed16(k_feat, FEAT_COMMAND, cmd).hex()
    return f


# 10.8 What CC8 reveals: the value and text of every distinct shingle that
# touches no removed span, ascending by value. A shingle that touches one
# sends nothing.

SHINGLE_VERDICTS = ("ok", "bad_entry", "sketch")


def shown_shingles(words, sources, removed_tokens):
    """(tag, shown, hidden): the text's shingles split by whether they touch
    a removed span. A shingle touches one when every place it occurs holds
    a code point of a word that comes from a removed token; the spaces
    between words come from no token."""
    dirty = [bool(src & removed_tokens) for src in sources]
    tag, xs = shingles(words)
    clean = set()
    if tag == WORD_SHINGLE:
        for i in range(len(words) - 2):
            if not any(dirty[i:i + 3]):
                clean.add(" ".join(words[i:i + 3]))
    else:
        owner = []
        for i, w in enumerate(words):
            if i:
                owner.append(None)
            owner += [i] * len(w)
        text = " ".join(words)
        size = min(5, len(text))
        for i in range(len(text) - size + 1 if text else 0):
            if not any(owner[k] is not None and dirty[owner[k]] for k in range(i, i + size)):
                clean.add(text[i:i + size])
    return tag, [x for x in xs if x in clean], [x for x in xs if x not in clean]


def shingle_reveal(k_feat, text, removed_tokens):
    """The CC8 reveal the client sends for text whose reveal removed the
    tokens removed_tokens (numbered from 0): (kind, [(v, x)]) ascending by
    v, for the shingles that touch no removed span."""
    _, words, sources = normalize_sources(text)
    tag, shown, _ = shown_shingles(words, sources, frozenset(removed_tokens))
    return SHINGLE_KINDS[tag], sorted((shingle_value(k_feat, tag, x), x.encode("utf-8")) for x in shown)


def verify_shingle_reveal(mh, entries, removed):
    """entries are (v, x). removed says whether the text's reveal removed a
    span. Returns (verdict, index, unexplained): the index of the bad
    entry, and for ok the number of places no shown value reaches."""
    prev = None
    for i, (v, x) in enumerate(entries):
        if len(v) != 32 or (prev is not None and not prev < v):
            return "bad_entry", i, None
        if not x or utf8_or_none(x) is None:
            return "bad_entry", i, None
        prev = v
    shown = sketch([v for v, _ in entries])
    if (any(a < b for a, b in zip(shown, mh)) if removed else shown != mh):
        return "sketch", None, None
    return "ok", None, sum(1 for a, b in zip(shown, mh) if a != b)


def utf8_or_none(x):
    """x decoded, or None when it isn't UTF-8."""
    try:
        return x.decode("utf-8")
    except UnicodeDecodeError:
        return None


# 10.9 The challenge draw of DESIGN 7.5 step 4.

COMMIT_DRAW = 2


def draw_seed(r, metadata):
    """seed = SHA-256(r || SHA-256(metadata reveal bytes))."""
    if len(r) != 32:
        raise LedgerError("r is 32 bytes")
    return sha256(r + sha256(metadata))


def draw_score(seed, device_id, seq):
    """HMAC-SHA256(seed, u8(len(device_id)) || device_id || u64(seq))."""
    if not 1 <= seq < 1 << 64:
        raise LedgerError("seq %d is out of range" % seq)
    return hmac_sha256(seed, device_field(device_id) + u64(seq))


def commit_score(seed, commit_id):
    """HMAC-SHA256(seed, 0x00 || the commit id's bytes)."""
    if len(commit_id) not in (20, 32):
        raise LedgerError("a commit id is 20 or 32 bytes")
    return hmac_sha256(seed, b"\x00" + commit_id)


def challenge_k(pool):
    """CHALLENGE_K = max(10, ceil(pool / 10)), capped at 30."""
    return min(30, max(10, (pool + 9) // 10))


def draw(seed, pool):
    """The drawn (device_id, seq) items, smallest score first."""
    if len(set(pool)) != len(pool):
        raise LedgerError("a pool lists each (device_id, seq) once")
    ranked = sorted(pool, key=lambda item: (draw_score(seed, *item), key_bytes("device_id", item[0]), item[1]))
    return ranked[:min(challenge_k(len(pool)), len(pool))]


def draw_commits(seed, commits):
    """The interview commits: the two with the smallest scores."""
    if len(set(commits)) != len(commits):
        raise LedgerError("a commit pool lists each commit once")
    return sorted(commits, key=lambda c: commit_score(seed, c))[:COMMIT_DRAW]


# ---------------------------------------------------------------------------
# 11. check

FORMAT = "pairtime-ledger-vectors-v1"
BODY_PREFIX = b'{"n":"'


class FormatError(Exception):
    """The file doesn't follow spec section 11's vectors format."""


class Report:
    def __init__(self):
        self.errors = []
        self.counts = {}

    def count(self, what):
        self.counts[what] = self.counts.get(what, 0) + 1

    def fail(self, path, msg):
        self.errors.append("%s: %s" % (path, msg))

    def same(self, path, got, want):
        if got != want:
            self.fail(path, "file has %s, reference computes %s" % (show(got), show(want)))

    def same_list(self, path, got, want):
        if len(got) != len(want):
            self.fail(path, "file has %d hashes, reference computes %d" % (len(got), len(want)))
            return
        for i, (g, w) in enumerate(zip(got, want)):
            self.same("%s[%d]" % (path, i), g, w)


def show(v):
    if isinstance(v, bytes):
        return v.hex() if v else "no bytes"
    return json.dumps(v)


def fields(obj, path, required, optional=()):
    if not isinstance(obj, dict):
        raise FormatError("%s: want an object" % path)
    for k in obj:
        if k not in required and k not in optional:
            raise FormatError("%s: unknown field %s" % (path, json.dumps(k)))
    for k in required:
        if k not in obj:
            raise FormatError("%s: missing field %s" % (path, json.dumps(k)))
    return obj


def get_int(obj, key, path, lo=0, hi=(1 << 64) - 1):
    v = obj[key]
    if isinstance(v, bool) or not isinstance(v, int) or not lo <= v <= hi:
        raise FormatError("%s.%s: want an integer from %d to %d, got %s" % (path, key, lo, hi, json.dumps(v)))
    return v


def get_str(obj, key, path):
    v = obj[key]
    if not isinstance(v, str):
        raise FormatError("%s.%s: want a string" % (path, key))
    return v


def get_list(obj, key, path):
    """A list; null means an empty one, as Go writes a nil slice."""
    v = obj[key]
    if v is None:
        return []
    if not isinstance(v, list):
        raise FormatError("%s.%s: want a list" % (path, key))
    return v


HEX_RE = re.compile(r"(?:[0-9a-f]{2})*\Z")


def from_hex(v, path, size=None):
    if not isinstance(v, str) or not HEX_RE.match(v):
        raise FormatError("%s: want lowercase hex" % path)
    b = bytes.fromhex(v)
    if size is not None and len(b) != size:
        raise FormatError("%s: want %d bytes, got %d" % (path, size, len(b)))
    return b


def get_hex(obj, key, path, size=None):
    return from_hex(obj[key], "%s.%s" % (path, key), size)


def check_keys(rep, item, path):
    fields(item, path, ("k_dev", "k_leaf", "k_tok", "k_feat", "k_vault"), ("prk",))
    want = derive_keys(get_hex(item, "k_dev", path, 32))
    for name in ("prk", "k_leaf", "k_tok", "k_feat", "k_vault"):
        if name in item:
            rep.same(path + "." + name, get_hex(item, name, path, 32), want[name])
    rep.count("key sets")


def check_digest(rep, item, path):
    if isinstance(item, dict) and "project_key" in item:
        fields(item, path, ("project_key", "project"))
        key, size, fn, out = get_str(item, "project_key", path), 16, project_digest, "project"
    else:
        fields(item, path, ("session_key", "session"))
        key, size, fn, out = get_str(item, "session_key", path), 8, session_digest, "session"
    got = get_hex(item, out, path, size)
    try:
        rep.same(path + "." + out, got, fn(key))
    except LedgerError as err:
        rep.fail(path, str(err))
    rep.count("digests")


def check_proofs(rep, proofs, path, hashes, root):
    for i, p in enumerate(proofs):
        pp = "%s[%d]" % (path, i)
        fields(p, pp, ("index", "path"))
        index = get_int(p, "index", pp)
        given = [from_hex(h, "%s.path[%d]" % (pp, j), 32) for j, h in enumerate(get_list(p, "path", pp))]
        rep.count("inclusion proofs")
        if index >= len(hashes):
            rep.fail(pp + ".index", "is %d, but the tree has %d leaves" % (index, len(hashes)))
            continue
        rep.same_list(pp + ".path", given, inclusion_path(index, hashes))
        if not verify_inclusion(index, len(hashes), hashes[index], given, root):
            rep.fail(pp, "doesn't verify against the root in the file")


def check_chain(rep, chain, path, received):
    fields(chain, path, ("device_id", "k_dev", "batches"))
    device_id = get_str(chain, "device_id", path)
    try:
        key_bytes("device_id", device_id)
    except LedgerError as err:
        rep.fail(path + ".device_id", str(err))
    keys = derive_keys(get_hex(chain, "k_dev", path, 32))
    batches = get_list(chain, "batches", path)
    if not batches:
        raise FormatError("%s.batches: empty" % path)
    last_b = last_head = next_seq = None
    for j, batch in enumerate(batches):
        bp = "%s.batches[%d]" % (path, j)
        fields(batch, bp, ("b", "n", "prev", "root", "head", "records"), ("received_at", "receipt_msg", "proofs"))
        b = get_int(batch, "b", bp, 1)
        n = get_int(batch, "n", bp)
        prev = get_hex(batch, "prev", bp, 32)
        root = get_hex(batch, "root", bp, 32)
        head = get_hex(batch, "head", bp, 32)
        records = get_list(batch, "records", bp)
        if last_b is None:
            if b == 1:
                rep.same(bp + ".prev", prev, ZERO32)
        else:
            if b != last_b + 1:
                rep.fail(bp + ".b", "is %d after batch %d; batch numbers have no gaps" % (b, last_b))
            rep.same(bp + ".prev", prev, last_head)
        if n != len(records):
            rep.fail(bp + ".n", "is %d, but the batch lists %d records" % (n, len(records)))
        entries = []
        for k, rec in enumerate(records):
            rp = "%s.records[%d]" % (bp, k)
            fields(rec, rp, ("seq", "rtype", "project_key", "session_key", "body", "nonce", "entry"))
            seq = get_int(rec, "seq", rp, 1)
            if next_seq is None:
                if b == 1 and seq != 1:
                    rep.fail(rp + ".seq", "is %d, but batch 1 starts at seq 1" % seq)
            elif seq != next_seq:
                rep.fail(rp + ".seq", "is %d after seq %d; sequence numbers have no gaps" % (seq, next_seq - 1))
            next_seq = seq + 1
            rtype = get_int(rec, "rtype", rp, 0, 0xFF)
            project_key = get_str(rec, "project_key", rp)
            session_key = get_str(rec, "session_key", rp)
            body = get_hex(rec, "body", rp)
            got_nonce = get_hex(rec, "nonce", rp, 32)
            got_entry = get_hex(rec, "entry", rp, 65)
            entries.append(got_entry)
            want_nonce = nonce(keys["k_leaf"], seq)
            rep.same(rp + ".nonce", got_nonce, want_nonce)
            if not body.startswith(BODY_PREFIX + want_nonce.hex().encode("ascii") + b'","e":'):
                rep.fail(rp + ".body", "doesn't start with {\"n\":\", this record's nonce in hex and \",\"e\":")
            try:
                rep.same(rp + ".entry", got_entry, entry(header(seq, rtype, project_key, session_key), body))
            except LedgerError as err:
                rep.fail(rp, str(err))
            if rtype in LATER_RTYPES:
                rep.count("records of later kinds")
            rep.count("records")
        if not entries:
            rep.fail(bp + ".records", "a batch has at least one record")
        else:
            rep.same(bp + ".root", root, mth(entries))
            rep.same(bp + ".head", head, batch_head(prev, root, len(entries), b))
        if ("received_at" in batch) != ("receipt_msg" in batch):
            raise FormatError("%s: received_at and receipt_msg come together" % bp)
        if "received_at" in batch:
            received_at = get_int(batch, "received_at", bp)
            msg = get_hex(batch, "receipt_msg", bp)
            try:
                rep.same(bp + ".receipt_msg", msg, receipt_message(device_id, b, head, received_at))
            except LedgerError as err:
                rep.fail(bp, str(err))
            received.append((bp, device_id, b, head, received_at))
        if "proofs" in batch:
            check_proofs(rep, get_list(batch, "proofs", bp), bp + ".proofs", [leaf_hash(e) for e in entries], root)
        rep.count("batches")
        last_b, last_head = b, head
    rep.count("chains")


def check_days(rep, days, rows_by_day):
    last_day = last_head = None
    witnessed = set()
    for i, day in enumerate(days):
        dp = "days[%d]" % i
        fields(day, dp, ("day", "prev", "count", "root", "head", "signed_msg", "rows"), ("date", "proofs"))
        d = get_int(day, "day", dp)
        if "date" in day:
            rep.same(dp + ".date", get_str(day, "date", dp), day_date(d))
        prev = get_hex(day, "prev", dp, 32)
        count = get_int(day, "count", dp)
        root = get_hex(day, "root", dp, 32)
        head = get_hex(day, "head", dp, 32)
        msg = get_hex(day, "signed_msg", dp)
        rows = get_list(day, "rows", dp)
        if last_day is not None:
            if d != last_day + 1:
                rep.fail(dp + ".day", "is %d after day %d; every day has a head" % (d, last_day))
            rep.same(dp + ".prev", prev, last_head)
        leaves, last_order = [], None
        for j, row in enumerate(rows):
            rp = "%s.rows[%d]" % (dp, j)
            fields(row, rp, ("device_id", "b", "head", "received_at", "leaf"))
            device_id = get_str(row, "device_id", rp)
            b = get_int(row, "b", rp, 1)
            h = get_hex(row, "head", rp, 32)
            received_at = get_int(row, "received_at", rp)
            leaves.append(get_hex(row, "leaf", rp))
            rep.count("day rows")
            if day_of(received_at) != d:
                rep.fail(rp + ".received_at", "falls on day %d, not day %d" % (day_of(received_at), d))
            if (device_id, b) in witnessed:
                rep.fail(rp, "repeats batch %d of %s; the server stores each batch once" % (b, device_id))
            witnessed.add((device_id, b))
            try:
                order = day_order(device_id, b, received_at)
                rep.same(rp + ".leaf", leaves[-1], day_leaf(device_id, b, h, received_at))
            except LedgerError as err:
                rep.fail(rp, str(err))
                continue
            if last_order is not None and not last_order < order:
                rep.fail(rp, "is out of order; rows sort by received_at, then device_id, then b")
            last_order = order
            rows_by_day.setdefault(d, {})[(device_id, b)] = (h, received_at)
        if count != len(rows):
            rep.fail(dp + ".count", "is %d, but the day lists %d rows" % (count, len(rows)))
        rep.same(dp + ".root", root, mth(leaves))
        rep.same(dp + ".head", head, day_head(prev, root, len(rows), d))
        rep.same(dp + ".signed_msg", msg, day_message(d, head))
        if "proofs" in day:
            check_proofs(rep, get_list(day, "proofs", dp), dp + ".proofs", [leaf_hash(x) for x in leaves], root)
        rows_by_day.setdefault(d, {})
        rep.count("days")
        last_day, last_head = d, head


def get_hex_text(obj, key, path):
    """A UTF-8 byte string written as hex, decoded. Bytes that aren't UTF-8
    are a format error: every text in the format is valid UTF-8."""
    data = get_hex(obj, key, path)
    try:
        return data, data.decode("utf-8")
    except UnicodeDecodeError:
        raise FormatError("%s.%s: isn't UTF-8" % (path, key))


def get_choice(obj, key, path, choices):
    v = get_str(obj, key, path)
    if v not in choices:
        raise FormatError("%s.%s: want one of %s, got %s" % (path, key, ", ".join(choices), json.dumps(v)))
    return v


def check_lowercase(rep, obj, path):
    fields(obj, path, ("unicode", "runs"), ("later_unicode", "later"))
    if get_str(obj, "unicode", path) != LOWER_UNICODE:
        rep.fail(path + ".unicode", "is %s; f.v = 1 lowercases with Unicode %s" % (json.dumps(obj["unicode"]), LOWER_UNICODE))
    runs = []
    for i, run in enumerate(get_list(obj, "runs", path)):
        rp = "%s.runs[%d]" % (path, i)
        fields(run, rp, ("first", "count", "delta", "stride"))
        runs.append((get_int(run, "first", rp, 0, 0x10FFFF), get_int(run, "count", rp, 1, 0x110000),
                     get_int(run, "delta", rp, -0x10FFFF, 0x10FFFF), get_int(run, "stride", rp, 1, 0x110000)))
    got, want = lower_map(runs), LOWER
    for cp in sorted(set(got) | set(want)):
        if got.get(cp) != want.get(cp):
            rep.fail(path, "maps U+%04X to %s, reference maps it to %s" % (
                cp, "U+%04X" % got[cp] if cp in got else "itself", "U+%04X" % want[cp] if cp in want else "itself"))
            break
    if ("later_unicode" in obj) != ("later" in obj):
        raise FormatError("%s: later_unicode and later come together" % path)
    if "later" in obj:
        if get_str(obj, "later_unicode", path) != LOWER_LATER_UNICODE:
            rep.fail(path + ".later_unicode", "is %s; the reference lists additions up to %s" % (
                json.dumps(obj["later_unicode"]), LOWER_LATER_UNICODE))
        given = []
        for i, item in enumerate(get_list(obj, "later", path)):
            ip = "%s.later[%d]" % (path, i)
            fields(item, ip, ("code", "lower"))
            given.append((get_int(item, "code", ip, 0, 0x10FFFF), get_int(item, "lower", ip, 0, 0x10FFFF)))
        rep.same(path + ".later", sorted(given), sorted(LOWER_LATER))
    rep.count("lowercase tables")


def check_commitment(rep, obj, path):
    fields(obj, path, ("k_dev", "seq", "rtype", "text", "n", "root"), ("raw", "tokens", "note"))
    keys = derive_keys(get_hex(obj, "k_dev", path, 32))
    seq = get_int(obj, "seq", path, 1)
    rtype = get_int(obj, "rtype", path, 0, 0xFF)
    text = get_hex(obj, "text", path)
    n = get_int(obj, "n", path)
    root = get_hex(obj, "root", path, 32)
    rep.count("commitments")
    if rtype not in CONTENT_RTYPES:
        rep.fail(path + ".rtype", "is 0x%02x; only prompt.submit, agent.output and cli.track commit text" % rtype)
        return
    if "raw" in obj:
        if rtype != AGENT_OUTPUT:
            raise FormatError("%s: only an agent message has a raw text to cut" % path)
        rep.same(path + ".text", text, committed_message(get_hex_text(obj, "raw", path)[0]))
    elif rtype == AGENT_OUTPUT and len(text) > MESSAGE_MAX:
        rep.fail(path + ".text", "is %d bytes; an agent message commits at most %d" % (len(text), MESSAGE_MAX))
    try:
        toks, salts, leaves, want_n, want_root = commit_text(keys["k_tok"], seq, text)
    except LedgerError as err:
        rep.fail(path, str(err))
        return
    rep.same(path + ".n", n, want_n)
    rep.same(path + ".root", root, want_root)
    if "tokens" in obj:
        given = get_list(obj, "tokens", path)
        if len(given) != want_n:
            rep.fail(path + ".tokens", "lists %d tokens, the text has %d" % (len(given), want_n))
            return
        for j, tok in enumerate(given):
            tp = "%s.tokens[%d]" % (path, j)
            fields(tok, tp, ("token", "salt", "leaf"))
            rep.same(tp + ".token", get_hex(tok, "token", tp), toks[j])
            rep.same(tp + ".salt", get_hex(tok, "salt", tp, 32), salts[j])
            rep.same(tp + ".leaf", get_hex(tok, "leaf", tp, 32), leaves[j])


def parse_reveal_items(items, path):
    out = []
    for i, item in enumerate(items):
        ip = "%s[%d]" % (path, i)
        if isinstance(item, dict) and "salt" in item:
            fields(item, ip, ("salt", "token"))
            out.append(("kept", get_hex(item, "salt", ip), get_hex(item, "token", ip)))
        else:
            fields(item, ip, ("type", "count", "leaves"), ("lines",))
            leaves = [from_hex(h, "%s.leaves[%d]" % (ip, j)) for j, h in enumerate(get_list(item, "leaves", ip))]
            lines = get_int(item, "lines", ip) if "lines" in item else None
            out.append(("removed", get_str(item, "type", ip), get_int(item, "count", ip), leaves, lines))
    return out


def check_reveal(rep, obj, path):
    fields(obj, path, ("n", "root", "items", "verdict"), ("note",))
    n = get_int(obj, "n", path)
    root = get_hex(obj, "root", path, 32)
    items = parse_reveal_items(get_list(obj, "items", path), path + ".items")
    verdict = get_choice(obj, "verdict", path, REVEAL_VERDICTS)
    got, _ = verify_reveal(n, root, items)
    rep.same(path + ".verdict", verdict, got)
    rep.count("reveals")


def check_normalization(rep, obj, path):
    fields(obj, path, ("text", "removed_lines", "normalized", "shingle_kind", "shingles", "signature"), ("note",))
    _, text = get_hex_text(obj, "text", path)
    removed, words = normalize(text)
    rep.same(path + ".removed_lines", get_list(obj, "removed_lines", path), removed)
    rep.same(path + ".normalized", get_hex(obj, "normalized", path), " ".join(words).encode("utf-8"))
    tag, xs = shingles(words)
    rep.same(path + ".shingle_kind", get_choice(obj, "shingle_kind", path, ("word3", "char5")), SHINGLE_KINDS[tag])
    given = [from_hex(x, "%s.shingles[%d]" % (path, i)) for i, x in enumerate(get_list(obj, "shingles", path))]
    rep.same_list(path + ".shingles", given, [x.encode("utf-8") for x in xs])
    sig = error_signature(text)
    got_sig = None if obj["signature"] is None else get_hex(obj, "signature", path)
    rep.same(path + ".signature", got_sig, None if sig is None else sig.encode("utf-8"))
    rep.count("normalizations")


def check_features(rep, obj, path):
    if not isinstance(obj, dict) or "rtype" not in obj:
        raise FormatError("%s: want an object with an rtype" % path)
    rtype = get_int(obj, "rtype", path, 0, 0xFF)
    common = ("k_dev", "rtype", "f")
    if rtype in (PROMPT_SUBMIT, CLI_TRACK):
        fields(obj, path, common + ("prompt_class",), ("text", "shingle_values", "note"))
        if ("text" in obj) != ("shingle_values" in obj):
            raise FormatError("%s: text and shingle_values come together" % path)
        if rtype == CLI_TRACK and "text" not in obj:
            raise FormatError("%s: a track label always has its text (10.1)" % path)
    elif rtype == AGENT_TOOL:
        fields(obj, path, common + ("hunks",), ("command", "note"))
    elif rtype == SHELL_COMMAND:
        fields(obj, path, common, ("command", "note"))
    else:
        raise FormatError("%s.rtype: 0x%02x records carry no features" % (path, rtype))
    keys = derive_keys(get_hex(obj, "k_dev", path, 32))
    if not isinstance(obj["f"], dict):
        raise FormatError("%s.f: want an object" % path)
    rep.count("feature sets")
    try:
        if rtype in (PROMPT_SUBMIT, CLI_TRACK):
            text = get_hex_text(obj, "text", path)[1] if "text" in obj else None
            want, values = prompt_features(keys["k_feat"], text, get_str(obj, "prompt_class", path))
            given = [from_hex(v, "%s.shingle_values[%d]" % (path, i), 32)
                     for i, v in enumerate(get_list(obj, "shingle_values", path))] if "text" in obj else []
            rep.same_list(path + ".shingle_values", given, values)
        else:
            hunks = None
            if rtype == AGENT_TOOL:
                hunks = []
                for i, h in enumerate(get_list(obj, "hunks", path)):
                    hp = "%s.hunks[%d]" % (path, i)
                    fields(h, hp, ("path", "old_start", "old_lines", "new_start", "new_lines"))
                    hunks.append((get_str(h, "path", hp),) + tuple(
                        get_int(h, k, hp) for k in ("old_start", "old_lines", "new_start", "new_lines")))
            command = get_hex_text(obj, "command", path)[1] if "command" in obj else None
            want = tool_features(keys["k_feat"], hunks, command)
    except LedgerError as err:
        rep.fail(path, str(err))
        return
    rep.same(path + ".f", obj["f"], want)


HUNK_FIELDS = ("old_start", "old_lines", "new_start", "new_lines")


def check_diff(rep, obj, path):
    fields(obj, path, (), ("patch", "before", "after", "hunks", "malformed", "note"))
    if ("hunks" in obj) == ("malformed" in obj):
        raise FormatError("%s: a diff has hunks or malformed, not both" % path)
    rep.count("diffs")
    if "patch" in obj:
        if "before" in obj or "after" in obj:
            raise FormatError("%s: a diff is a patch or a before and after, not both" % path)
        pp = path + ".patch"
        patch = fields(obj["patch"], pp, HUNK_FIELDS + ("lines",))
        lines = [get_hex_text({"line": x}, "line", "%s.lines[%d]" % (pp, i))[1]
                 for i, x in enumerate(get_list(patch, "lines", pp))]
        try:
            want = hunks_from_patch(*[get_int(patch, k, pp, 0, (1 << 31) - 1) for k in HUNK_FIELDS], lines)
        except LedgerError:
            want = None
    else:
        fields(obj, path, ("before", "after"), ("hunks", "malformed", "note"))
        h = hunk_from_texts(get_hex_text(obj, "before", path)[1], get_hex_text(obj, "after", path)[1])
        want = [] if h is None else [h]
    if "malformed" in obj:
        if obj["malformed"] is not True:
            raise FormatError("%s.malformed: want true" % path)
        if want is not None:
            rep.fail(path + ".malformed", "the reference reads the patch as %s" % show([list(h) for h in want]))
        return
    given = []
    for i, h in enumerate(get_list(obj, "hunks", path)):
        hp = "%s.hunks[%d]" % (path, i)
        fields(h, hp, HUNK_FIELDS)
        given.append(tuple(get_int(h, k, hp) for k in HUNK_FIELDS))
    if want is None:
        rep.fail(path + ".hunks", "the reference finds the patch malformed")
    else:
        rep.same(path + ".hunks", [list(h) for h in given], [list(h) for h in want])


def get_sketch(obj, key, path):
    return sketch_from_bytes(get_hex(obj, key, path, 4 * MINHASH_SIZE))


def check_jaccard(rep, obj, path):
    fields(obj, path, ("a", "b", "matches"), ("note",))
    a, b = get_sketch(obj, "a", path), get_sketch(obj, "b", path)
    rep.same(path + ".matches", get_int(obj, "matches", path, 0, MINHASH_SIZE), jaccard_matches(a, b))
    rep.count("jaccard pairs")


def check_shingle_reveal(rep, obj, path):
    fields(obj, path, ("mh", "kind", "removed", "entries", "verdict"),
           ("unexplained", "k_dev", "text", "removed_tokens", "note"))
    mh = get_sketch(obj, "mh", path)
    kind = get_choice(obj, "kind", path, tuple(SHINGLE_KINDS.values()))
    removed = obj["removed"]
    if not isinstance(removed, bool):
        raise FormatError("%s.removed: want true or false" % path)
    entries = []
    for i, e in enumerate(get_list(obj, "entries", path)):
        ep = "%s.entries[%d]" % (path, i)
        fields(e, ep, ("v", "x"))
        entries.append((get_hex(e, "v", ep), get_hex(e, "x", ep)))
    got, _, unexplained = verify_shingle_reveal(mh, entries, removed)
    rep.same(path + ".verdict", get_choice(obj, "verdict", path, SHINGLE_VERDICTS), got)
    if ("unexplained" in obj) != (got == "ok"):
        rep.fail(path + ".unexplained", "comes with an ok verdict, and only with one")
    elif got == "ok":
        rep.same(path + ".unexplained", get_int(obj, "unexplained", path, 0, MINHASH_SIZE), unexplained)
    build = ("k_dev", "text", "removed_tokens")
    if any(k in obj for k in build):
        if not all(k in obj for k in build):
            raise FormatError("%s: k_dev, text and removed_tokens come together" % path)
        k_feat = derive_keys(get_hex(obj, "k_dev", path, 32))["k_feat"]
        text = get_hex_text(obj, "text", path)[1]
        tokens = [get_int({"t": t}, "t", "%s.removed_tokens[%d]" % (path, i)) for i, t in
                  enumerate(get_list(obj, "removed_tokens", path))]
        want_kind, want = shingle_reveal(k_feat, text, tokens)
        rep.same(path + ".kind", kind, want_kind)
        rep.same(path + ".entries", [[v.hex(), x.hex()] for v, x in entries], [[v.hex(), x.hex()] for v, x in want])
        rep.same(path + ".removed", removed, bool(tokens))
    rep.count("shingle reveals")


def check_draw(rep, obj, path):
    fields(obj, path, ("r", "metadata", "seed", "pool", "drawn"), ("commits", "drawn_commits", "note"))
    seed = draw_seed(get_hex(obj, "r", path, 32), get_hex(obj, "metadata", path))
    rep.same(path + ".seed", get_hex(obj, "seed", path, 32), seed)
    pool = []
    for i, item in enumerate(get_list(obj, "pool", path)):
        ip = "%s.pool[%d]" % (path, i)
        fields(item, ip, ("device_id", "seq", "score"))
        device_id, seq = get_str(item, "device_id", ip), get_int(item, "seq", ip, 1)
        pool.append((device_id, seq))
        try:
            rep.same(ip + ".score", get_hex(item, "score", ip, 32), draw_score(seed, device_id, seq))
        except LedgerError as err:
            rep.fail(ip, str(err))
    drawn = []
    for i, item in enumerate(get_list(obj, "drawn", path)):
        ip = "%s.drawn[%d]" % (path, i)
        fields(item, ip, ("device_id", "seq"))
        drawn.append((get_str(item, "device_id", ip), get_int(item, "seq", ip, 1)))
    try:
        rep.same(path + ".drawn", drawn, draw(seed, pool))
    except LedgerError as err:
        rep.fail(path + ".pool", str(err))
    if ("commits" in obj) != ("drawn_commits" in obj):
        raise FormatError("%s: commits and drawn_commits come together" % path)
    if "commits" in obj:
        commits = []
        for i, c in enumerate(get_list(obj, "commits", path)):
            cp = "%s.commits[%d]" % (path, i)
            fields(c, cp, ("commit", "score"))
            commit = get_hex(c, "commit", cp)
            commits.append(commit)
            try:
                rep.same(cp + ".score", get_hex(c, "score", cp, 32), commit_score(seed, commit))
            except LedgerError as err:
                rep.fail(cp, str(err))
        given = []
        for i, c in enumerate(get_list(obj, "drawn_commits", path)):
            cp = "%s.drawn_commits[%d]" % (path, i)
            fields(c, cp, ("commit",))
            given.append(get_hex(c, "commit", cp))
        try:
            rep.same(path + ".drawn_commits", [c.hex() for c in given], [c.hex() for c in draw_commits(seed, commits)])
        except LedgerError as err:
            rep.fail(path + ".commits", str(err))
    rep.count("draws")


def check_challenge_k(rep, obj, path):
    fields(obj, path, ("pool", "k", "drawn"))
    pool = get_int(obj, "pool", path)
    rep.same(path + ".k", get_int(obj, "k", path), challenge_k(pool))
    rep.same(path + ".drawn", get_int(obj, "drawn", path), min(challenge_k(pool), pool))
    rep.count("challenge sizes")


CONTENT_SECTIONS = (
    ("commitments", check_commitment),
    ("reveals", check_reveal),
    ("normalizations", check_normalization),
    ("features", check_features),
    ("diffs", check_diff),
    ("jaccard", check_jaccard),
    ("shingle_reveals", check_shingle_reveal),
    ("draws", check_draw),
    ("challenge_k", check_challenge_k),
)


def check_document(doc):
    """Check a parsed vectors file and return a Report. Raises FormatError
    when the file doesn't follow the format."""
    rep = Report()
    fields(doc, "file", ("format",), ("spec", "generator", "note", "keys", "digests", "chains", "days", "lowercase")
           + tuple(name for name, _ in CONTENT_SECTIONS))
    if doc["format"] != FORMAT:
        raise FormatError("file: format is %s, want %s" % (json.dumps(doc["format"]), json.dumps(FORMAT)))
    for i, item in enumerate(get_list(doc, "keys", "file") if "keys" in doc else []):
        check_keys(rep, item, "keys[%d]" % i)
    for i, item in enumerate(get_list(doc, "digests", "file") if "digests" in doc else []):
        check_digest(rep, item, "digests[%d]" % i)
    received = []
    for i, chain in enumerate(get_list(doc, "chains", "file") if "chains" in doc else []):
        check_chain(rep, chain, "chains[%d]" % i, received)
    rows_by_day = {}
    check_days(rep, get_list(doc, "days", "file") if "days" in doc else [], rows_by_day)
    for bp, device_id, b, head, received_at in received:
        rows = rows_by_day.get(day_of(received_at))
        if rows is None:
            continue
        row = rows.get((device_id, b))
        if row is None:
            rep.fail(bp, "is missing from the rows of day %d" % day_of(received_at))
        elif row != (head, received_at):
            rep.fail(bp, "has a head or received_at other than its row in day %d" % day_of(received_at))
    if "lowercase" in doc:
        check_lowercase(rep, doc["lowercase"], "lowercase")
    for name, fn in CONTENT_SECTIONS:
        for i, item in enumerate(get_list(doc, name, "file") if name in doc else []):
            fn(rep, item, "%s[%d]" % (name, i))
    if not rep.counts:
        raise FormatError("file: nothing to check")
    return rep


def summary(counts):
    order = ("key sets", "digests", "chains", "batches", "records", "records of later kinds", "days", "day rows", "inclusion proofs",
             "lowercase tables", "commitments", "reveals", "normalizations", "feature sets", "diffs", "jaccard pairs",
             "shingle reveals", "draws", "challenge sizes")
    return ", ".join("%d %s" % (counts[k], k) for k in order if k in counts)


def cmd_check(path):
    try:
        with open(path, "rb") as f:
            doc = json.loads(f.read().decode("utf-8"))
        rep = check_document(doc)
    except (OSError, ValueError, FormatError) as err:
        print("check: %s: %s" % (path, err), file=sys.stderr)
        return 2
    for line in rep.errors[:100]:
        print(line)
    if len(rep.errors) > 100:
        print("... and %d more" % (len(rep.errors) - 100))
    print("check: %s: %s: %s" % (path, summary(rep.counts), "%d mismatches" % len(rep.errors) if rep.errors else "ok"))
    return 1 if rep.errors else 0


# ---------------------------------------------------------------------------
# 11. vectors

class Stream:
    """A deterministic byte stream: block i is
    SHA-256("pairtime-ledger-vectors" || 0x00 || seed || 0x00 || u64(i)).
    It stands in for a random source so the vectors never depend on
    Python's random module, whose output may change between versions."""

    def __init__(self, seed):
        self.seed = seed.encode("ascii")
        self.counter = 0
        self.pool = b""

    def take(self, n):
        while len(self.pool) < n:
            self.pool += sha256(b"pairtime-ledger-vectors\x00" + self.seed + b"\x00" + u64(self.counter))
            self.counter += 1
        out, self.pool = self.pool[:n], self.pool[n:]
        return out

    def below(self, n):
        """A uniform integer in [0, n), by rejection sampling."""
        limit = (1 << 64) - (1 << 64) % n
        while True:
            x = int.from_bytes(self.take(8), "big")
            if x < limit:
                return x % n

    def pick(self, items):
        return items[self.below(len(items))]

    def shuffle(self, items):
        for i in range(len(items) - 1, 0, -1):
            j = self.below(i + 1)
            items[i], items[j] = items[j], items[i]


def utc_ms(text):
    """Unix ms of a time written as 2026-11-19T23:20:00.000Z."""
    t = datetime.strptime(text, "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=timezone.utc)
    return (t - EPOCH) // timedelta(milliseconds=1)


def stand_in_body(nonce_bytes, rtype, seq):
    """A body with the real envelope and a stand-in record, which is all the
    vectors need: hashing never parses a body (4.5)."""
    return ('{"n":"%s","e":{"stand_in":"%s","seq":%d}}' % (nonce_bytes.hex(), RTYPES[rtype], seq)).encode("ascii")


def record_keys(s, rtype, projects, sessions):
    """A project_key and session_key a real record of this rtype could have."""
    if rtype in OS_RTYPES or rtype == UPLOAD_FAILURE:
        return "", NO_SESSION
    if rtype in (0x0D, CHECKPOINT):
        return s.pick(projects), NO_SESSION
    if rtype <= 0x0A:
        return s.pick(projects + [""]), s.pick(sessions)
    return s.pick(projects + [""]), s.pick(sessions + [NO_SESSION])


def proof_indices(s, n, every=9):
    if n <= every:
        return list(range(n))
    picks = {0, 1, n // 2, n - 2, n - 1}
    while len(picks) < 8:
        picks.add(s.below(n))
    return sorted(picks)


def make_chain(s, device_id, k_dev, sizes, received_ats):
    keys = derive_keys(k_dev)
    projects = ["p_" + s.take(6).hex() for _ in range(3)]
    sessions = ["s_" + s.take(6).hex() for _ in range(4)]
    codes = sorted(RTYPES)
    seq, prev, batches = 1, ZERO32, []
    for b, (n, received_at) in enumerate(zip(sizes, received_ats), 1):
        records, entries = [], []
        for _ in range(n):
            rtype = s.pick(codes)
            project_key, session_key = record_keys(s, rtype, projects, sessions)
            nonce_bytes = nonce(keys["k_leaf"], seq)
            body = stand_in_body(nonce_bytes, rtype, seq)
            e = entry(header(seq, rtype, project_key, session_key), body)
            entries.append(e)
            records.append({
                "seq": seq, "rtype": rtype, "project_key": project_key, "session_key": session_key,
                "body": body.hex(), "nonce": nonce_bytes.hex(), "entry": e.hex(),
            })
            seq += 1
        root = mth(entries)
        head = batch_head(prev, root, n, b)
        hashes = [leaf_hash(e) for e in entries]
        batches.append({
            "b": b, "n": n, "received_at": received_at, "prev": prev.hex(), "root": root.hex(), "head": head.hex(),
            "receipt_msg": receipt_message(device_id, b, head, received_at).hex(),
            "records": records,
            "proofs": [{"index": i, "path": [h.hex() for h in inclusion_path(i, hashes)]} for i in proof_indices(s, n)],
        })
        prev = head
    return {"device_id": device_id, "k_dev": k_dev.hex(), "batches": batches}


def make_days(s, chains, first_day, days):
    rows = []
    for chain in chains:
        for batch in chain["batches"]:
            rows.append((chain["device_id"], batch["b"], bytes.fromhex(batch["head"]), batch["received_at"]))
    # Batches from devices whose chains aren't in the file: only their heads
    # matter here. The times cover ties and both edges of a UTC day.
    d1, d2 = first_day * DAY_MS, (first_day + 1) * DAY_MS
    tie = chains[0]["batches"][4]["received_at"]
    before, mid, after = "C_" + s.take(4).hex(), "d_" + s.take(8).hex(), "z-" + s.take(5).hex()
    for device_id, b, received_at in (
        ("x", 1, d1 + s.below(DAY_MS)),
        ("x", 2, d2 - 1),
        ("x", 3, d2),
        (before, 41, tie),
        (after, 7, tie),
        (mid, 1, d1 + s.below(DAY_MS)),
        (mid, 2, d2 + s.below(DAY_MS)),
    ):
        rows.append((device_id, b, s.take(32), received_at))
    out, prev = [], ZERO32
    for d in range(first_day, first_day + days):
        today = sorted((r for r in rows if day_of(r[3]) == d), key=lambda r: day_order(r[0], r[1], r[3]))
        leaves = [day_leaf(*r) for r in today]
        root = mth(leaves)
        head = day_head(prev, root, len(leaves), d)
        hashes = [leaf_hash(x) for x in leaves]
        out.append({
            "day": d, "date": day_date(d), "prev": prev.hex(), "count": len(leaves), "root": root.hex(),
            "head": head.hex(), "signed_msg": day_message(d, head).hex(),
            "rows": [{"device_id": r[0], "b": r[1], "head": r[2].hex(), "received_at": r[3], "leaf": x.hex()}
                     for r, x in zip(today, leaves)],
            "proofs": [{"index": i, "path": [h.hex() for h in inclusion_path(i, hashes)]}
                       for i in proof_indices(s, len(leaves), every=64)],
        })
        prev = head
    return out


def make_vectors():
    keys = []
    s = Stream("keys")
    for k_dev in (bytes(32), bytes(range(32)), b"\xff" * 32, s.take(32)):
        derived = derive_keys(k_dev)
        item = {"k_dev": k_dev.hex()}
        item.update((name, derived[name].hex()) for name in ("prk", "k_leaf", "k_tok", "k_feat", "k_vault"))
        keys.append(item)

    s = Stream("digests")
    long_key = "".join(s.pick("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") for _ in range(128))
    digests = [{"project_key": k, "project": project_digest(k).hex()} for k in ("", "p", "p_3f9a2c", "none", long_key)]
    digests += [{"session_key": k, "session": session_digest(k).hex()} for k in ("none", "s", "s_51c2e07d", long_key)]

    # Chain A: 50 batches, among them sizes 1, 2, 3, 7, 8, 9, 100 and 1,000,
    # uploaded about a minute apart from 23:20 UTC, so they span midnight.
    s = Stream("chain-a")
    k_dev = s.take(32)
    device_id = "d_" + s.take(8).hex()
    sizes = [1, 2, 3, 7, 8, 9, 100, 1000] + [1 + s.below(16) for _ in range(42)]
    s.shuffle(sizes)
    t, times = utc_ms("2026-11-19T23:20:00.000Z"), []
    for _ in sizes:
        t += 55000 + s.below(10001)
        times.append(t)
    chain_a = make_chain(s, device_id, k_dev, sizes, times)

    # Chain B: a 128-character device id, and four batches sealed offline
    # and flushed in one catch-up, so all four share one received_at.
    s = Stream("chain-b")
    k_dev = s.take(32)
    device_id = "".join(s.pick("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") for _ in range(128))
    flushed = utc_ms("2026-11-20T00:05:00.000Z") + s.below(60000)
    chain_b = make_chain(s, device_id, k_dev, [2, 1, 5, 3], [flushed] * 4)

    first_day = day_of(chain_a["batches"][0]["received_at"])
    days = make_days(Stream("days"), [chain_a, chain_b], first_day, 3)

    return {
        "format": FORMAT,
        "spec": "docs/spec/ledger-v1.md",
        "generator": "python3 -I tools/ledger_ref.py vectors",
        "note": "Test keys and stand-in bodies; see testdata/ledger/README.md.",
        "keys": keys,
        "digests": digests,
        "chains": [chain_a, chain_b],
        "days": days,
    }


def dumps(v, indent=0):
    """JSON with one record, row, proof or key set per line."""
    def scalar(x):
        return x is None or isinstance(x, (str, int, bool))

    def flat(x):
        if scalar(x):
            return True
        if isinstance(x, list):
            return all(scalar(y) for y in x)
        if isinstance(x, dict):
            return all(scalar(y) or (isinstance(y, list) and all(scalar(z) for z in y)) for y in x.values())
        return False

    if flat(v):
        return json.dumps(v, separators=(", ", ": "))
    pad, end = "  " * (indent + 1), "  " * indent
    if isinstance(v, list):
        return "[\n" + ",\n".join(pad + dumps(x, indent + 1) for x in v) + "\n" + end + "]"
    return "{\n" + ",\n".join(pad + json.dumps(k) + ": " + dumps(x, indent + 1) for k, x in v.items()) + "\n" + end + "}"


def vectors_text():
    return dumps(make_vectors()) + "\n"


def repo_path(*parts):
    return os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), *parts)


def cmd_vectors(out):
    text = vectors_text()
    with open(out, "w", encoding="ascii", newline="\n") as f:
        f.write(text)
    print("vectors: wrote %s (%d bytes)" % (out, len(text)))
    return 0


# ---------------------------------------------------------------------------
# 11. vectors-content: the part 2 vectors

SAMPLE_WORDS = (
    "fix", "the", "navbar", "on", "mobile,", "it", "still", "broken", "try", "again", "please", "why", "does",
    "src/app/main.go", "`useEffect`", "https://example.com/x?y=1", "3f9a2c1d", "0x1F", "42", "line", "ÉCOLE",
    "straße", "İstanbul", "Ещё", "раз", "修复这个错误", "🙏", "naïve", "ΣΑΣ", "café,", "(see", "TypeError:", "x",
    "is", "undefined", "doesn’t", "work!!", "sk-test-51Hx", "teen@example.com", "—", "```", "1.",
)
SAMPLE_SPACES = (" ", " ", " ", " ", "  ", "\t", "\n", "\r\n", "\r\n\r\n", "\u00a0", "\u3000", "\u2028", " \t ")


def random_text(s, max_words):
    parts = [s.pick(SAMPLE_SPACES)] if s.below(4) == 0 else []
    for i in range(s.below(max_words + 1)):
        if i:
            parts.append(s.pick(SAMPLE_SPACES))
        parts.append(s.pick(SAMPLE_WORDS))
    if s.below(4) == 0:
        parts.append(s.pick(SAMPLE_SPACES))
    return "".join(parts)


def random_spans(s, n):
    """Up to three disjoint removed spans over n tokens, random types."""
    spans, j = [], 0
    while j < n and len(spans) < 3:
        j += s.below(4)
        if j >= n:
            break
        count = 1 + s.below(min(3, n - j))
        span_type = s.pick(SPAN_TYPES)
        spans.append((j, count, span_type, 1 + s.below(9) if span_type == "code" else None))
        j += count + 1
    return spans


def item_json(item):
    if item[0] == "kept":
        return {"salt": item[1].hex(), "token": item[2].hex()}
    out = {"type": item[1], "count": item[2], "leaves": [h.hex() for h in item[3]]}
    if item[4] is not None:
        out["lines"] = item[4]
    return out


def commitment_json(k_dev, seq, rtype, text, note, raw=None, with_tokens=True):
    keys = derive_keys(k_dev)
    toks, salts, leaves, n, root = commit_text(keys["k_tok"], seq, text)
    out = {"note": note, "k_dev": k_dev.hex(), "seq": seq, "rtype": rtype}
    if raw is not None:
        out["raw"] = raw.hex()
    out.update(text=text.hex(), n=n, root=root.hex())
    if with_tokens:
        out["tokens"] = [{"token": t.hex(), "salt": a.hex(), "leaf": h.hex()} for t, a, h in zip(toks, salts, leaves)]
    return out, (toks, salts, leaves, n, root)


def reveal_json(note, n, root, items, verdict=None):
    got, _ = verify_reveal(n, root, items)
    if verdict is not None and got != verdict:
        raise LedgerError("the %s vector is %s, not %s" % (note, got, verdict))
    return {"note": note, "n": n, "root": root.hex(), "items": [item_json(i) for i in items], "verdict": got}


def make_reveals(k_dev, commitments):
    """Positive reveals of the crafted texts and of random ones, then one
    vector for each way a reveal can fail."""
    out = []
    by_note = {c[0]["note"]: c[1] for c in commitments}

    def spans_of(note, spans, label):
        toks, salts, leaves, n, root = by_note[note]
        out.append(reveal_json("%s: %s" % (note, label), n, root, reveal_items(salts, toks, leaves, spans), "ok"))

    spans_of("short prompt", [], "everything kept")
    spans_of("short prompt", [(0, 1, "teen", None)], "removal at the start")
    spans_of("short prompt", [(4, 1, "teen", None)], "removal in the middle")
    spans_of("short prompt", [(6, 1, "teen", None)], "removal at the end")
    spans_of("short prompt", [(0, 7, "teen", None)], "everything removed")
    spans_of("empty prompt", [], "no tokens")
    spans_of("CRLF", [(2, 2, "known", None)], "a word and its CRLF removed")
    spans_of("non-ASCII", [(8, 1, "known", None), (14, 1, "pii", None)], "two removals, one at the end")
    spans_of("secret and email", [(6, 1, "secret", None), (7, 1, "teen", None), (16, 1, "pii", None)],
             "adjacent spans of two types, and an email")
    spans_of("whitespace only", [(0, 1, "teen", None)], "the one whitespace token removed")
    spans_of("agent message with code", [(6, 11, "code", 3)], "a fenced block collapsed to a code marker")
    spans_of("leading and trailing whitespace", [(0, 1, "teen", None), (8, 1, "teen", None)],
             "both whitespace edges removed")
    s = Stream("content-reveals")
    for c, data in commitments:
        if c["note"].startswith("random"):
            toks, salts, leaves, n, root = data
            for k in range(2):
                spans = random_spans(s, n)
                out.append(reveal_json("%s, random removals %d" % (c["note"], k + 1), n, root,
                                       reveal_items(salts, toks, leaves, spans), "ok"))

    # Failures. The first two kinds use the short prompt's own commitment.
    toks, salts, leaves, n, root = by_note["short prompt"]
    keep = reveal_items(salts, toks, leaves, [])
    k_tok = derive_keys(k_dev)["k_tok"]

    def fail(note, items, verdict, commit=(n, root)):
        out.append(reveal_json(note, commit[0], commit[1], items, verdict))

    fail("a kept token changed", keep[:4] + [("kept", salts[4], b"logon")] + keep[5:], "root")
    fail("a salt changed", keep[:2] + [("kept", bytes([salts[2][0] ^ 1]) + salts[2][1:], toks[2])] + keep[3:], "root")
    fail("two words swapped", [keep[4]] + keep[1:4] + [keep[0]] + keep[5:], "root")
    fail("a removed leaf changed", keep[:6] + [("removed", "teen", 1, [bytes([leaves[6][0] ^ 1]) + leaves[6][1:]], None)],
         "root")
    fail("the last token dropped", keep[:6], "count")
    # The last three tokens collapsed into their subtree's hash: the items
    # still hash to C, and only the committed n shows that tokens are missing.
    collapsed = keep[:4] + [("removed", "teen", 1, [tree_root(leaves[4:7])], None)]
    if tree_root(leaves[:4] + [tree_root(leaves[4:7])]) != root:
        raise LedgerError("the collapse vector no longer reaches C")
    fail("three tokens collapsed into their subtree hash: C matches, n doesn't", collapsed, "count")
    fail("a span that claims an extra token", keep[:6] + [("removed", "teen", 2, [leaves[6], leaves[6]], None)], "count")
    fail("an empty token", keep[:1] + [("kept", salts[1], b"")] + keep[2:], "bad_item")
    fail("a token that isn't UTF-8", keep[:2] + [("kept", salts[2], b"\xff")] + keep[3:], "bad_item")
    fail("a token mixing a word and whitespace", keep[:2] + [("kept", salts[2], b"the ")] + keep[3:], "bad_item")
    fail("a 31-byte salt", keep[:2] + [("kept", salts[2][:31], toks[2])] + keep[3:], "bad_item")
    fail("an unknown span type", keep[:6] + [("removed", "email", 1, [leaves[6]], None)], "bad_item")
    fail("a span of no tokens", keep + [("removed", "teen", 0, [], None)], "bad_item")
    fail("a span whose leaves don't match its count", keep[:5] + [("removed", "teen", 2, [leaves[5]], None)], "bad_item")
    fail("a 31-byte leaf", keep[:6] + [("removed", "teen", 1, [leaves[6][:31]], None)], "bad_item")
    fail("lines on a span that isn't code", keep[:6] + [("removed", "secret", 1, [leaves[6]], 1)], "bad_item")
    fail("a code span of 0 lines", keep[:6] + [("removed", "code", 1, [leaves[6]], 0)], "bad_item")
    # A client that committed tokens the tokenizer would never make: two
    # words with nothing between them, and a word two places from whitespace.
    salt = [token_salt(k_tok, 99, j) for j in (1, 2, 3)]
    bad = [token_leaf(salt[0], b"fix"), token_leaf(salt[1], b"it")]
    fail("two words side by side", [("kept", salt[0], b"fix"), ("kept", salt[1], b"it")], "alternation",
         (2, tree_root(bad)))
    bad = [token_leaf(salt[0], b"fix"), token_leaf(salt[1], b" "), token_leaf(salt[2], b" ")]
    fail("whitespace two places after a word", [("kept", salt[0], b"fix"), ("removed", "teen", 1, [bad[1]], None),
                                                 ("kept", salt[2], b" ")], "alternation", (3, tree_root(bad)))
    return out


NORMALIZATION_TEXTS = (
    ("plain prompt of six words or more", "Fix the login bug please, it crashes on submit"),
    ("the substitutions", "Open https://example.com/a?b=1, then edit src/app/main.go line 42 and `handleSubmit` "
                          "at 0x7ffd5e8c with commit 3f9a2c1d"),
    ("retry phrases", "doesn\u2019t work!! Still not working. fix it again? please retry"),
    ("doesnt without an apostrophe, and a quoted again", "it doesnt work \u2018again\u2019"),
    ("retry is the whole prompt", "try again"),
    ("under five characters", "ok"),
    ("empty", ""),
    ("whitespace only", " \r\n\t\u3000"),
    ("simple lowercase mapping, not full", "\u00c9COLE Stra\u00dfe \u0130stanbul \u03a3\u0391\u03a3 \u0415\u0449\u0451 \u0440\u0430\u0437"),
    ("every kind of whitespace", "fix\r\nthe\u00a0bug\u3000now\u2028and\u0085then\u000bthe\u000cother\u2009one"),
    ("paths and things that aren't", "and/or src/components a/b/c ./run.sh ~/notes.txt C:\\Users\\me\\x.txt "
                                     "localhost:8080/api e.g./i.e. $HOME/.config/app"),
    ("hex strings and numbers", "deadbeef facade1 abc123 0x1F 0x 1234567 a1b2c3d v1.2.3 2026-11-19"),
    ("URL schemes and punctuation", "(see https://a.example/x), <ftp://b.example>, HTTP://C.EXAMPLE/Y and git+ssh://h/r"),
    ("backticked identifiers", "rename `fooBar` to `foo_bar` and ``x`` then `two words` stay"),
    ("a fenced block", "here is my code\n```python\nprint(1)\n```\nwhy does it fail"),
    ("a tilde fence closed only by a long enough fence", "before\n~~~~\ncode\n~~~\nstill code\n~~~~~\nafter"),
    ("an unclosed fence runs to the end", "look\n```\nall of this\nis code"),
    ("backticks on one line aren't a fence", "```js const x = 1``` still here"),
    ("four spaces of indent aren't a fence", "    ```\nindented\n   ```\ninside"),
    ("pasted_content blocks", "<pasted_content id=\"1\">\nfoo bar\n</pasted_content>\nstill broken\n"
                              "  <pasted_content id=\"2\" lines=\"3\">  \nbaz"),
    # The wrapper as Claude Code 2.1.327 writes it: the paste starts on its
    # own line after a blank one, and the closer repeats the opener's id.
    ("Claude Code's paste wrapper, then typed text", "here's the error:\n\n<pasted_content id=\"1a2b\">\n"
                                                    "TypeError: x is undefined\n    at parse (src/parse.js:4:11)\n"
                                                    "</pasted_content id=\"1a2b\">\nwhy does it fail on empty input?"),
    ("two pastes with ids, text between and after", "compare\n\n<pasted_content id=\"00ff\">\nfirst log\n"
                                                    "</pasted_content id=\"00ff\">\nwith\n\n<pasted_content id=\"c0de\">\n"
                                                    "second log\n</pasted_content id=\"c0de\">\nand tell me which is right"),
    ("a closer with another id still closes a paste", "<pasted_content id=\"aaaa\">\nlog\n"
                                                      "</pasted_content id=\"bbbb\">\nthe typed part stays"),
    ("a Python traceback", "still broken\nTraceback (most recent call last):\n  File \"app.py\", line 3, in <module>\n"
                           "    main()\nValueError: invalid literal for int() with base 10: 'abc'\nany ideas?"),
    ("a JavaScript error and its stack", "TypeError: Cannot read properties of undefined (reading 'map')\n"
                                         "    at App (App.jsx:12:5)\n    at render (react-dom.js:99:1)\nwhy"),
    ("Rust", "error[E0308]: mismatched types\n --> src/main.rs:4:5\n  |\nhelp me"),
    ("a C compiler", "main.c:3:5: error: 'x' undeclared (first use in this function)\nwhat now"),
    ("pytest", "FAILED tests/test_api.py::test_login - AssertionError: 401 != 200\nfix it"),
    ("go test", "--- FAIL: TestParse (0.00s)\n    parse_test.go:14: got 3, want 4\nhmm"),
    ("a Go panic", "panic: runtime error: index out of range [5] with length 3\n\ngoroutine 1 [running]:"),
    ("an uncaught browser error", "Uncaught ReferenceError: foo is not defined\nat line 3"),
    ("the error only inside a fence", "it fails:\n```\nKeyError: 'user_id'\n```\nhelp"),
    ("an error line with CRLF", "still broken\r\nModuleNotFoundError: No module named 'requests'\r\n"),
    ("prose that isn't an error", "the error is that terror and Errors happen, error: none"),
    ("five words, so character shingles", "make the button bigger please"),
    ("SM 6.5's 20:02 prompt", "still overlapping on mobile. fix it"),
    ("SM 6.5's 20:07 prompt, one text with 20:02's", "still overlapping on mobile, fix it!!"),
    ("edge punctuation goes, a placeholder's stays, and a word of punctuation goes",
     "(see \u201cmain.go:12\u201d) -- it's 42. \u2018quoted\u2019, e.g. [x], <tag> and !!"),
    ("six words, so word shingles", "make the button a bit bigger"),
    ("the worked example of 10.10", "still broken, see src/nav.js:12\r\nTypeError: x is undefined"),
)


def normalization_json(note, text):
    removed, words = normalize(text)
    tag, xs = shingles(words)
    sig = error_signature(text)
    return {"note": note, "text": text.encode("utf-8").hex(), "removed_lines": removed,
            "normalized": " ".join(words).encode("utf-8").hex(), "shingle_kind": SHINGLE_KINDS[tag],
            "shingles": [x.encode("utf-8").hex() for x in xs],
            "signature": None if sig is None else sig.encode("utf-8").hex()}


def prompt_feature_json(k_dev, rtype, note, text, prompt_class):
    f, values = prompt_features(derive_keys(k_dev)["k_feat"], text, prompt_class)
    out = {"note": note, "k_dev": k_dev.hex(), "rtype": rtype}
    if text is not None:
        out["text"] = text.encode("utf-8").hex()
    out["prompt_class"] = prompt_class
    if text is not None:
        out["shingle_values"] = [v.hex() for v in values]
    out["f"] = f
    return out


def tool_feature_json(k_dev, rtype, note, hunks, command):
    out = {"note": note, "k_dev": k_dev.hex(), "rtype": rtype}
    if rtype == AGENT_TOOL:
        out["hunks"] = [dict(zip(("path", "old_start", "old_lines", "new_start", "new_lines"), h)) for h in hunks]
    if command is not None:
        out["command"] = command.encode("utf-8").hex()
    out["f"] = tool_features(derive_keys(k_dev)["k_feat"], hunks if rtype == AGENT_TOOL else None, command)
    return out


FEATURE_PROMPTS = (
    ("the worked example of 10.10", "still broken, see src/nav.js:12\r\nTypeError: x is undefined", "error_paste"),
    ("a re-roll, first", "the navbar still overlaps the hero on mobile, can you fix the css", "substantive"),
    ("a re-roll, same words in other case", "The navbar STILL overlaps the hero on mobile, can you fix the CSS", "substantive"),
    ("a re-roll, one word changed", "the navbar still overlaps the hero on mobile, can you fix the styles", "substantive"),
    ("unrelated", "add a dark mode toggle to the settings page and remember the choice", "substantive"),
    ("a go-ahead", "try again", "low_effort"),
    ("an answer", "2", "answer"),
    ("empty", "", "low_effort"),
    ("only pasted content", "<pasted_content id=\"7\">\nTraceback (most recent call last):\n  File \"x.py\", line 1\n"
                            "NameError: name 'y' is not defined\n</pasted_content>", "error_paste"),
    ("a paste in Claude Code's wrapper, then a question", "it fails like this\n\n<pasted_content id=\"9e41\">\n"
                                                         "Traceback (most recent call last):\n  File \"app.py\", line 3, in <module>\n"
                                                         "KeyError: 'user_id'\n</pasted_content id=\"9e41\">\n"
                                                         "why is user_id missing after login?", "error_paste"),
    ("non-ASCII", "\u0415\u0449\u0451 \u0440\u0430\u0437 \u2014 \u043f\u043e\u0447\u0438\u043d\u0438 "
                  "\u043a\u043d\u043e\u043f\u043a\u0443 \U0001f64f", "substantive"),
    ("SM 6.5's 20:02 prompt", "still overlapping on mobile. fix it", "substantive"),
    ("SM 6.5's 20:07 prompt", "still overlapping on mobile, fix it!!", "substantive"),
)


def make_features(k_dev):
    out = [prompt_feature_json(k_dev, PROMPT_SUBMIT, note, text, pc) for note, text, pc in FEATURE_PROMPTS]
    out.append(prompt_feature_json(k_dev, PROMPT_SUBMIT, "a bridge prompt, whose text the device never had", None,
                                   "substantive"))
    out.append(prompt_feature_json(k_dev, CLI_TRACK, "a track label", "make the navbar sticky on mobile", "substantive"))
    out.append(tool_feature_json(k_dev, AGENT_TOOL, "an edit across two files, with an insertion and a deletion", [
        ("src/nav.js", 10, 3, 10, 5), ("src/nav.js", 40, 0, 43, 2), ("web/app.tsx", 1, 4, 0, 0)], None))
    out.append(tool_feature_json(k_dev, AGENT_TOOL, "a new file outside the project, and a Windows path", [
        ("/tmp/scratch.txt", 0, 0, 1, 12), ("C:/Users/teen/notes.txt", 7, 1, 7, 1)], None))
    out.append(tool_feature_json(k_dev, AGENT_TOOL, "a command run by the agent", [], "npm  test\t-- --watch\n"))
    out.append(tool_feature_json(k_dev, AGENT_TOOL, "a read: no hunks, no command", [], None))
    out.append(tool_feature_json(k_dev, SHELL_COMMAND, "a command typed in a shell", None, "  pytest -k  test_login "))
    out.append(tool_feature_json(k_dev, SHELL_COMMAND, "a shell event with no command line", None, None))
    return out


def shingle_reveal_json(note, mh, kind, removed, entries, verdict, build=None):
    """A CC8 reveal vector. build is (k_dev, text, removed_tokens) for a
    reveal that is what the client would send for that text."""
    got, _, unexplained = verify_shingle_reveal(mh, entries, removed)
    if got != verdict:
        raise LedgerError("shingle reveal %s is %s, not %s" % (note, got, verdict))
    out = {"note": note, "mh": sketch_bytes(mh).hex(), "kind": kind, "removed": removed}
    if build is not None:
        k_dev, text, tokens = build
        out.update(k_dev=k_dev.hex(), text=text.encode("utf-8").hex(), removed_tokens=sorted(tokens))
    out["entries"] = [{"v": v.hex(), "x": x.hex()} for v, x in entries]
    out["verdict"] = got
    if got == "ok":
        out["unexplained"] = unexplained
    return out


def make_shingle_reveals(features):
    """CC8 reveals of some of the prompts above, whole and with spans
    removed, and one of each failure."""
    out = []
    k_dev = bytes.fromhex(features[0]["k_dev"])
    k_feat = derive_keys(k_dev)["k_feat"]

    def client(note, text, tokens, prompt_class="substantive"):
        f, _ = prompt_features(k_feat, text, prompt_class)
        kind, entries = shingle_reveal(k_feat, text, tokens)
        mh = sketch_from_bytes(bytes.fromhex(f["mh"]))
        out.append(shingle_reveal_json(note, mh, kind, bool(tokens), entries, "ok", (k_dev, text, tokens)))
        return mh, kind, entries

    for item in features[:5] + features[7:8]:
        client("%s: every shingle shown" % item["note"], bytes.fromhex(item["text"]).decode("utf-8"), [])
    example = bytes.fromhex(features[0]["text"]).decode("utf-8")
    client("the worked example with src/nav.js:12, token 7, removed: shingles touching <path> send nothing",
           example, [6])
    client("fix it for bob with bob removed", "fix it for bob", [6])
    client("a shingle shown because one place it occurs is made of kept tokens", "hi bob hi bob", [6])
    client("a word shingle next to a removed token goes, and the rest stay",
           "please make the header sticky and ask bob about the footer", [14])
    client("a removed token on a line that step 2 removes touches nothing",
           "fix the login page\nTypeError: bob is undefined", [10])
    client("everything removed: nothing is shown", example, list(range(15)))

    # Failures, on the worked example shown whole.
    mh, kind = sketch_from_bytes(bytes.fromhex(features[0]["f"]["mh"])), "char5"
    _, entries = shingle_reveal(k_feat, example, [])

    def fail(note, entries, verdict, removed=False):
        out.append(shingle_reveal_json(note, mh, kind, removed, entries, verdict))

    # A value that is the minimum somewhere, so changing it shows.
    pick = next(i for i, (v, _) in enumerate(entries) if any(
        h == m for h, m in zip(minhash_values(v), mh)))
    changed = sorted(entries[:pick] + [(sha256(b"not the shingle's value"), entries[pick][1])] + entries[pick + 1:])
    fail("a value that sets a minimum, replaced", changed, "sketch")
    fail("out of order", entries[1:2] + entries[:1] + entries[2:], "bad_entry")
    fail("a value twice", entries[:1] + entries[:1] + entries[1:], "bad_entry")
    fail("a 31-byte value", [(entries[0][0][:31], entries[0][1])] + entries[1:], "bad_entry")
    fail("an empty x", entries[:3] + [(entries[3][0], b"")] + entries[4:], "bad_entry")
    fail("an x that isn't UTF-8", entries[:3] + [(entries[3][0], b"\xff")] + entries[4:], "bad_entry")
    fail("a shingle missing, with nothing removed", entries[:pick] + entries[pick + 1:], "sketch")
    fail("a shingle missing, with a span removed: its minima are unexplained, not wrong",
         entries[:pick] + entries[pick + 1:], "ok", removed=True)
    # A value whose hash is below the committed minimum somewhere can't
    # belong to the text, removed spans or not.
    low = next(v for v in (sha256(b"too low %d" % i) for i in range(1000))
               if any(h < m for h, m in zip(minhash_values(v), mh)) and v not in [e[0] for e in entries])
    fail("a value below a committed minimum, with a span removed", sorted(entries + [(low, b"made up")]), "sketch",
         removed=True)
    return out


def context(first, last, text="line %d"):
    return [" " + text % i + "\n" for i in range(first, last)]


def patch_diff(note, old_start, old_lines, new_start, new_lines, lines):
    out = {"note": note, "patch": {"old_start": old_start, "old_lines": old_lines, "new_start": new_start,
                                   "new_lines": new_lines, "lines": [x.encode("utf-8").hex() for x in lines]}}
    try:
        out["hunks"] = [dict(zip(HUNK_FIELDS, h)) for h in hunks_from_patch(old_start, old_lines, new_start, new_lines, lines)]
    except LedgerError:
        out["malformed"] = True
    return out


def text_diff(note, before, after):
    h = hunk_from_texts(before, after)
    return {"note": note, "before": before.encode("utf-8").hex(), "after": after.encode("utf-8").hex(),
            "hunks": [] if h is None else [dict(zip(HUNK_FIELDS, h))]}


def make_diffs():
    """Zero-context hunks from patches with context and from the text
    before and after an edit (10.6)."""
    file20 = "".join("line %d\n" % i for i in range(1, 21))
    return [
        patch_diff("three lines of context around three lines added after line 9", 7, 6, 7, 9,
                   context(7, 10) + ["+added %d\n" % i for i in (1, 2, 3)] + context(10, 13)),
        patch_diff("the next turn appends three more below them: no line overlaps once context is gone", 10, 6, 10, 9,
                   [" added %d\n" % i for i in (1, 2, 3)] + ["+added %d\n" % i for i in (4, 5, 6)] + context(10, 13)),
        patch_diff("a run of removed and added lines is one hunk", 17, 8, 17, 7,
                   context(17, 20) + ["-old 20\n", "-old 21\n", "+new 20\n"] + context(22, 25)),
        patch_diff("two runs in one patch hunk, split at the unchanged line between them", 4, 7, 4, 8,
                   context(4, 6) + ["-line 6\n", "+line six\n"] + context(7, 8) + ["+inserted\n"] + context(8, 11)),
        patch_diff("a run that adds before it removes is still one hunk", 1, 3, 1, 3,
                   [" a\n", "+b2\n", "-b\n", " c\n"]),
        patch_diff("a new file", 0, 0, 1, 3, ["+one\n", "+two\n", "+three\n"]),
        patch_diff("a deleted file", 1, 2, 0, 0, ["-one\n", "-two\n"]),
        patch_diff("a last line without its LF, and the marker line counts in neither file", 8, 3, 8, 3,
                   [" eight\n", " nine\n", "-ten", "\\ No newline at end of file", "+ten\n"]),
        patch_diff("a patch whose lines don't add up to its header is malformed", 1, 3, 1, 3,
                   [" a\n", "-b\n", "+B\n"]),
        patch_diff("a patch line with no marker is malformed", 1, 1, 1, 1, ["a\n"]),
        text_diff("an edit inside one line", file20, file20.replace("line 12\n", "line twelve\n")),
        text_diff("an edit that removes two lines and adds one", file20,
                  file20.replace("line 5\nline 6\n", "lines 5 and 6\n")),
        text_diff("an insertion between two equal lines starts after the shared leading lines", "a\nb\n", "a\nb\nb\n"),
        text_diff("an edit that adds the final LF changes the last line", "a\nb", "a\nb\n"),
        text_diff("CRLF lines compare by their bytes", "a\r\nb\r\n", "a\r\nB\r\n"),
        text_diff("an edit that changes nothing has no hunk", file20, file20),
        text_diff("an edit of an empty file", "", "first\n"),
    ]


def make_jaccard(features):
    by = {f["note"]: sketch_from_bytes(bytes.fromhex(f["f"]["mh"])) for f in features if "mh" in f["f"]}
    pairs = (
        ("identical after normalization", "a re-roll, first", "a re-roll, same words in other case"),
        ("one word changed", "a re-roll, first", "a re-roll, one word changed"),
        ("SM 6.5's 20:02 and 20:07 prompts, one text after edge punctuation goes", "SM 6.5's 20:02 prompt",
         "SM 6.5's 20:07 prompt"),
        ("unrelated prompts", "a re-roll, first", "unrelated"),
        ("two empty sets", "empty", "only pasted content"),
        ("an empty set and a prompt", "empty", "a go-ahead"),
    )
    return [{"note": note, "a": sketch_bytes(by[a]).hex(), "b": sketch_bytes(by[b]).hex(),
             "matches": jaccard_matches(by[a], by[b])} for note, a, b in pairs]


def make_draw(s, note, pool_size, devices, commits):
    r = s.take(32)
    metadata = ("stand-in metadata reveal: %s" % note).encode("ascii")
    seed = draw_seed(r, metadata)
    pool = []
    for i in range(pool_size):
        device_id = devices[i % len(devices)]
        pool.append((device_id, 1 + 37 * i + s.below(37)))
    out = {"note": note, "r": r.hex(), "metadata": metadata.hex(), "seed": seed.hex(),
           "pool": [{"device_id": d, "seq": q, "score": draw_score(seed, d, q).hex()} for d, q in pool],
           "drawn": [{"device_id": d, "seq": q} for d, q in draw(seed, pool)]}
    if commits:
        ids = [s.take(20 if i % 2 else 32) for i in range(commits)]
        out["commits"] = [{"commit": c.hex(), "score": commit_score(seed, c).hex()} for c in ids]
        out["drawn_commits"] = [{"commit": c.hex()} for c in draw_commits(seed, ids)]
    return out


def make_content_vectors():
    k_dev = Stream("content-key").take(32)
    s = Stream("content-texts")
    crafted = (
        (PROMPT_SUBMIT, "empty prompt", ""),
        (PROMPT_SUBMIT, "short prompt", "fix the login bug"),
        (PROMPT_SUBMIT, "CRLF", "line one\r\nline two\r\n"),
        (PROMPT_SUBMIT, "leading and trailing whitespace", "  leading and trailing whitespace \t\n"),
        (PROMPT_SUBMIT, "non-ASCII", "\u0415\u0449\u0451 \u0440\u0430\u0437 \u2014 \u043f\u043e\u0447\u0438\u043d\u0438 "
                                     "\u043a\u043d\u043e\u043f\u043a\u0443 \U0001f64f\u00a0\u0441\u0435\u0439\u0447\u0430\u0441"
                                     "\u3000\u4fee\u590d\u8fd9\u4e2a\u9519\u8bef"),
        (PROMPT_SUBMIT, "secret and email", "my key is sk-test-51Hx and my email is teen@example.com, why does it fail?"),
        (PROMPT_SUBMIT, "whitespace only", "\n\n  \t"),
        (AGENT_OUTPUT, "agent message with code", "Here's the fix:\n```js\nconst x = 1;\n```\nRun the tests again."),
        (CLI_TRACK, "track label", "make the navbar sticky on mobile"),
    )
    commitments = []
    for seq, (rtype, note, text) in enumerate(crafted, 1):
        commitments.append(commitment_json(k_dev, 1000 + seq, rtype, text.encode("utf-8"), note))
    # An agent message over 16 KiB whose cut falls inside a three-byte
    # character, so the committed text stops before it.
    raw = ("word " * 3276 + "ab\u4e2dcd").encode("utf-8")
    commitments.append(commitment_json(k_dev, 2000, AGENT_OUTPUT, committed_message(raw),
                                       "agent message over 16 KiB, cut before a split character", raw=raw,
                                       with_tokens=False))
    for i in range(12):
        text = random_text(s, 24).encode("utf-8")
        commitments.append(commitment_json(k_dev, 3000 + s.below(1 << 20), s.pick(CONTENT_RTYPES), text,
                                           "random text %d" % (i + 1)))
    reveals = make_reveals(k_dev, commitments)
    normalizations = [normalization_json(note, text) for note, text in NORMALIZATION_TEXTS]
    s = Stream("content-normalizations")
    normalizations += [normalization_json("random text %d" % (i + 1), random_text(s, 30)) for i in range(10)]
    features = make_features(k_dev)
    s = Stream("content-draws")
    draws = [
        make_draw(s, "a pool of 12 on two devices", 12, ["d_example", "d_other"], 5),
        make_draw(s, "a pool smaller than CHALLENGE_K", 4, ["d_example"], 1),
        make_draw(s, "a pool of 137", 137, ["d_example", "d_laptop", "d_" + "x" * 126], 0),
        make_draw(s, "a pool of 300, where the cap applies", 300, ["d_example"], 3),
        make_draw(s, "an empty pool", 0, ["d_example"], 2),
    ]
    sizes = [{"pool": p, "k": challenge_k(p), "drawn": min(challenge_k(p), p)}
             for p in (0, 1, 9, 10, 11, 99, 100, 101, 109, 110, 111, 200, 289, 290, 291, 299, 300, 1000)]
    return {
        "format": FORMAT,
        "spec": "docs/spec/ledger-v1.md",
        "generator": "python3 -I tools/ledger_ref.py vectors-content",
        "note": "Part 2 vectors: commitments, reveals, normalization, keyed features, CC8 reveals and draws. "
                "Test keys; see testdata/ledger/README.md.",
        "lowercase": {"unicode": LOWER_UNICODE, "runs": [
            {"first": a, "count": b, "delta": c, "stride": d} for a, b, c, d in LOWER_RUNS],
            "later_unicode": LOWER_LATER_UNICODE,
            "later": [{"code": a, "lower": b} for a, b in LOWER_LATER]},
        "commitments": [c[0] for c in commitments],
        "reveals": reveals,
        "normalizations": normalizations,
        "features": features,
        "diffs": make_diffs(),
        "jaccard": make_jaccard(features),
        "shingle_reveals": make_shingle_reveals(features),
        "draws": draws,
        "challenge_k": sizes,
    }


def content_vectors_text():
    return dumps(make_content_vectors()) + "\n"


def cmd_vectors_content(out):
    text = content_vectors_text()
    with open(out, "w", encoding="ascii", newline="\n") as f:
        f.write(text)
    print("vectors-content: wrote %s (%d bytes)" % (out, len(text)))
    return 0


# ---------------------------------------------------------------------------
# 12. The worked example

def example():
    """The worked example in spec section 12, as (label, value) pairs. selftest
    checks that every value appears in the spec."""
    out = []
    k_dev = bytes(range(32))
    keys = derive_keys(k_dev)
    out += [("K_dev", k_dev), ("PRK", keys["prk"]), ("K_leaf", keys["k_leaf"]), ("K_tok", keys["k_tok"]),
            ("K_feat", keys["k_feat"]), ("K_vault", keys["k_vault"])]
    out += [("project digest of p_example", project_digest("p_example")),
            ("session digest of s_example", session_digest("s_example"))]
    entries = []
    for seq, rtype, project_key, session_key in (
        (1, 0x0E, "", NO_SESSION),
        (2, 0x03, "p_example", "s_example"),
        (3, CHECKPOINT, "p_example", NO_SESSION),
    ):
        n = nonce(keys["k_leaf"], seq)
        body = stand_in_body(n, rtype, seq)
        hdr = header(seq, rtype, project_key, session_key)
        entries.append(entry(hdr, body))
        out += [("nonce %d" % seq, n), ("header %d" % seq, hdr), ("body %d" % seq, body.decode("ascii")),
                ("body hash %d" % seq, sha256(body)), ("entry %d" % seq, entries[-1])]
    hashes = [leaf_hash(e) for e in entries]
    root = mth(entries)
    head = batch_head(ZERO32, root, 3, 1)
    received_at = utc_ms("2026-11-19T12:01:00.250Z")
    out += [("leaf hash %d" % (i + 1), h) for i, h in enumerate(hashes)]
    out += [("node over leaves 1 and 2", node_hash(hashes[0], hashes[1])), ("R_1", root), ("H_1", head)]
    out += [("path of record 2, element %d" % i, h) for i, h in enumerate(inclusion_path(1, hashes))]
    out += [("received_at", received_at), ("receipt message", receipt_message("d_example", 1, head, received_at))]
    other = ("d_other", 7, sha256(b"stand-in head of batch 7 of d_other"), received_at - 30000)
    mine = ("d_example", 1, head, received_at)
    d = day_of(received_at)
    rows = sorted([mine, other], key=lambda r: day_order(r[0], r[1], r[3]))
    leaves = [day_leaf(*r) for r in rows]
    day_hashes = [leaf_hash(x) for x in leaves]
    t = mth(leaves)
    g = day_head(ZERO32, t, 2, d)
    out += [("other head", other[2]), ("day", d), ("day leaf 1", leaves[0]), ("day leaf 2", leaves[1]),
            ("day leaf hash 1", day_hashes[0]), ("day leaf hash 2", day_hashes[1]),
            ("T_d", t), ("G_d", g), ("day message", day_message(d, g))]
    out += [("day path of d_example, element %d" % i, h) for i, h in enumerate(inclusion_path(1, day_hashes))]
    return out


# ---------------------------------------------------------------------------
# selftest

class SelftestError(Exception):
    pass


def expect(ok, msg):
    if not ok:
        raise SelftestError(msg)


def load_json(*parts):
    with open(repo_path(*parts), "rb") as f:
        return json.loads(f.read().decode("utf-8"))


def test_ct_sums():
    base = repo_path("testdata", "ledger", "ct")
    listed = set()
    with open(os.path.join(base, "SHA256SUMS"), encoding="ascii") as f:
        for line in f:
            want, name = line.rstrip("\n").split("  ", 1)
            with open(os.path.join(base, name), "rb") as g:
                expect(hashlib.sha256(g.read()).hexdigest() == want, "%s doesn't match SHA256SUMS" % name)
            listed.add(name)
    for dirpath, _, names in os.walk(base):
        for name in names:
            rel = os.path.relpath(os.path.join(dirpath, name), base).replace(os.sep, "/")
            expect(rel in listed or rel in ("SHA256SUMS", "README.md"), "%s isn't in SHA256SUMS" % rel)
    return "%d files" % len(listed)


def test_rfc4231():
    """RFC 4231's seven HMAC-SHA-256 cases. Only test case 5 is truncated,
    to 16 bytes, and says so; every other case gives all 32 bytes, so a
    re-extraction that lost a line of a value can't pass."""
    cases = load_json("testdata", "ledger", "rfc4231.json")["cases"]
    expect(len(cases) == 7, "rfc4231.json has %d cases, want test cases 1 to 7" % len(cases))
    for i, c in enumerate(cases, 1):
        want = bytes.fromhex(c["hmac_sha256"])
        size = c.get("truncate_bytes", 32)
        expect((size == 16) == (i == 5), "%s: truncate_bytes is %s; only test case 5 is truncated" % (c["name"], size))
        expect(len(want) == size, "%s: the expected value is %d bytes, want %d" % (c["name"], len(want), size))
        got = hmac_sha256(bytes.fromhex(c["key"]), bytes.fromhex(c["data"]))[:size]
        expect(got == want, "%s: got %s" % (c["name"], got.hex()))
    return "%d cases" % len(cases)


def test_rfc5869():
    cases = load_json("testdata", "ledger", "rfc5869.json")["cases"]
    expect(len(cases) == 3, "rfc5869.json has %d cases, want test cases 1 to 3" % len(cases))
    for c in cases:
        prk = hkdf_extract(bytes.fromhex(c["salt"]), bytes.fromhex(c["ikm"]))
        expect(prk.hex() == c["prk"], "%s: PRK is %s" % (c["name"], prk.hex()))
        okm = hkdf_expand(prk, bytes.fromhex(c["info"]), c["l"])
        expect(okm.hex() == c["okm"], "%s: OKM is %s" % (c["name"], okm.hex()))
    return "%d cases" % len(cases)


def ct_constants():
    """Leaf inputs, subtree hashes and roots from constants.go.txt."""
    with open(repo_path("testdata", "ledger", "ct", "constants.go.txt"), encoding="utf-8") as f:
        text = f.read()

    def body(name):
        m = re.search(r"^func %s\(\) [^\n]*\{\n(.*?)^\}\n" % name, text, re.S | re.M)
        expect(m is not None, "constants.go.txt has no func %s" % name)
        return m.group(1)

    def hexes(src):
        return [bytes.fromhex(h) for h in re.findall(r'hd\("([0-9a-f]*)"\)', src)]

    leaves = hexes(body("LeafInputs"))
    (empty,) = hexes(body("EmptyRootHash"))
    roots = [empty if m.group(0) == "EmptyRootHash()" else bytes.fromhex(m.group(1))
             for m in re.finditer(r'EmptyRootHash\(\)|hd\("([0-9a-f]*)"\)', body("RootHashes"))]
    levels = [hexes(level) for level in body("NodeHashes").split("}, {")]
    expect(len(leaves) == 8 and len(roots) == 9 and [len(x) for x in levels] == [8, 4, 2, 1],
           "constants.go.txt doesn't have the expected shape")
    return leaves, levels, roots


def test_ct_trees():
    leaves, levels, roots = ct_constants()
    for n, want in enumerate(roots):
        expect(mth(leaves[:n]) == want, "MTH of the first %d leaf inputs is wrong" % n)
        expect(mth_by_stack(leaves[:n]) == want, "the stack algorithm gets the first %d leaf inputs wrong" % n)
    for level, hashes in enumerate(levels):
        for i, want in enumerate(hashes):
            span = leaves[i << level:(i + 1) << level]
            expect(mth(span) == want, "subtree %d at level %d is wrong" % (i, level))
    return "%d roots, %d subtree hashes" % (len(roots), sum(len(x) for x in levels))


def test_ct_inclusion():
    leaves, _, roots = ct_constants()
    base = repo_path("testdata", "ledger", "ct", "inclusion")
    valid = invalid = regenerated = 0
    for dirpath, _, names in sorted(os.walk(base)):
        for name in sorted(names):
            path = os.path.join(dirpath, name)
            with open(path, "rb") as f:
                p = json.loads(f.read().decode("utf-8"))

            def b64(v):
                return base64.b64decode(v, validate=True)

            proof = [b64(x) for x in (p["proof"] or [])]
            root, leaf = b64(p["root"]), b64(p["leafHash"])
            ok = verify_inclusion(p["leafIdx"], p["treeSize"], leaf, proof, root)
            rel = os.path.relpath(path, base)
            expect(ok != p["wantErr"], "%s (%s): verification %s" % (rel, p["desc"], "passed" if ok else "failed"))
            if p["wantErr"]:
                invalid += 1
                continue
            valid += 1
            n = p["treeSize"]
            if n < len(roots) and root == roots[n]:
                path_hashes = inclusion_path(p["leafIdx"], [leaf_hash(x) for x in leaves[:n]])
                expect(path_hashes == proof, "%s: PATH differs from the published proof" % rel)
                regenerated += 1
    expect(valid + invalid == 98, "want the 98 published probes, found %d" % (valid + invalid))
    return "%d probes (%d valid, %d invalid), %d paths regenerated" % (valid + invalid, valid, invalid, regenerated)


def test_merkle():
    """Our own properties: MTH equals the stack algorithm, every PATH
    verifies, and each kind of tampering fails."""
    for n in range(0, 300):
        entries = [u64(i)[i % 9:] for i in range(n)]
        expect(mth(entries) == mth_by_stack(entries), "MTH and the stack algorithm disagree at %d entries" % n)
    proofs = 0
    for n in range(1, 41):
        hashes = [leaf_hash(u64(i)) for i in range(n)]
        root = tree_root(hashes)
        for m in range(n):
            path = inclusion_path(m, hashes)
            expect(verify_inclusion(m, n, hashes[m], path, root), "PATH(%d) of %d doesn't verify" % (m, n))
            flipped = bytes([hashes[m][0] ^ 1]) + hashes[m][1:]
            bad = [(m, n, flipped, path, root), (m, n, hashes[m], path, flipped)]
            bad.append((m, n, hashes[m], path + [root], root))
            if path:
                bad.append((m, n, hashes[m], path[:-1], root))
                bad.append((m, n, hashes[m], [bytes([path[0][0] ^ 1]) + path[0][1:]] + path[1:], root))
            if n > 1:
                bad.append((m + 1 if m + 1 < n else m - 1, n, hashes[m], path, root))
            for case in bad:
                expect(not verify_inclusion(*case), "a tampered proof for leaf %d of %d verifies" % (m, n))
            proofs += 1
    return "300 trees, %d proofs and their tamperings" % proofs


def test_rtypes():
    """Every kind in pkg/event's kindTable has an rtype, and nothing else."""
    base = repo_path("pkg", "event")
    src = ""
    for name in sorted(os.listdir(base)):
        if name.endswith(".go") and not name.endswith("_test.go"):
            with open(os.path.join(base, name), encoding="utf-8") as f:
                src += f.read() + "\n"
    names = dict(re.findall(r'^\s*(Kind\w+)\s+Kind\s*=\s*"([^"]+)"', src, re.M))
    m = re.search(r"^var kindTable = \[\]struct \{.*?^\}\{\n(.*?)^\}\n", src, re.S | re.M)
    expect(m is not None, "pkg/event has no kindTable")
    table = []
    for ident in re.findall(r"\b(Kind\w+)\b", m.group(1)):
        if ident in names and names[ident] not in table:
            table.append(names[ident])
    want = sorted(RTYPES[c] for c in EVENT_RTYPES)
    expect(sorted(table) == want, "kindTable has %s; the rtype table has %s" % (sorted(table), want))
    return "%d kinds" % len(table)


def test_example():
    with open(repo_path("docs", "spec", "ledger-v1.md"), encoding="utf-8") as f:
        spec = f.read()
    values = example()
    for label, v in values:
        text = v.hex() if isinstance(v, bytes) else str(v)
        expect(text in spec, "the spec doesn't show the example's %s, %s" % (label, text))
    return "%d values" % len(values)


def test_vectors_regenerate():
    with open(repo_path("testdata", "ledger", "vectors.json"), encoding="ascii", newline="") as f:
        published = f.read()
    expect(published == vectors_text(), "testdata/ledger/vectors.json isn't what `vectors` writes")
    return "%d bytes" % len(published)


def test_vectors_check():
    rep = check_document(load_json("testdata", "ledger", "vectors.json"))
    expect(not rep.errors, "check finds %d mismatches, the first: %s" % (len(rep.errors), rep.errors[:1]))
    return summary(rep.counts)


def flip(hex_text):
    """hex_text with its last digit changed."""
    return hex_text[:-1] + ("1" if hex_text[-1] == "0" else "0")


def test_check_catches():
    """check reports each kind of tampering, on a small slice of the vectors."""
    full = load_json("testdata", "ledger", "vectors.json")
    base = {"format": FORMAT, "keys": full["keys"][:1], "digests": full["digests"][:2],
            "chains": [full["chains"][1]], "days": full["days"][1:]}
    expect(not check_document(json.loads(json.dumps(base))).errors, "the slice of the vectors doesn't pass check")

    def batch(doc, j=0):
        return doc["chains"][0]["batches"][j]

    def rec(doc, j=0, k=0):
        return batch(doc, j)["records"][k]

    def day_row(doc, j=0):
        return doc["days"][0]["rows"][j]

    def setter(get, key, fn):
        def apply(doc):
            obj = get(doc)
            obj[key] = fn(obj[key])
        return apply

    def swap_rows(doc):
        rows = doc["days"][0]["rows"]
        rows[0], rows[1] = rows[1], rows[0]

    def os_with_project(doc):
        rec(doc).update(rtype=0x0E, project_key="p_x", session_key=NO_SESSION)

    mutations = (
        ("a derived key", setter(lambda d: d["keys"][0], "k_leaf", flip)),
        ("a project digest", setter(lambda d: d["digests"][1], "project", flip)),
        ("a body byte", setter(rec, "body", flip)),
        ("a nonce", setter(rec, "nonce", flip)),
        ("a body without its nonce", setter(rec, "body", lambda b: b'{"e":{}}'.hex())),
        ("an entry", setter(rec, "entry", flip)),
        ("a seq", setter(lambda d: rec(d, 1, 0), "seq", lambda s: s + 1)),
        ("a reserved rtype", setter(rec, "rtype", lambda _: 0x42)),
        ("rtype 0x00", setter(rec, "rtype", lambda _: 0x00)),
        ("an os.* record with a project", os_with_project),
        ("a root", setter(batch, "root", flip)),
        ("a head", setter(batch, "head", flip)),
        ("a prev", setter(lambda d: batch(d, 1), "prev", flip)),
        ("a batch number", setter(lambda d: batch(d, 1), "b", lambda b: b + 1)),
        ("n", setter(batch, "n", lambda n: n + 1)),
        ("a receipt message", setter(batch, "receipt_msg", flip)),
        ("a received_at", setter(batch, "received_at", lambda t: t + 1)),
        ("a batch proof", setter(lambda d: batch(d)["proofs"][0], "path", lambda p: [flip(p[0])] + p[1:])),
        ("a day leaf", setter(day_row, "leaf", flip)),
        ("the order of day rows", swap_rows),
        ("a day count", setter(lambda d: d["days"][0], "count", lambda n: n + 1)),
        ("a day root", setter(lambda d: d["days"][0], "root", flip)),
        ("a day head", setter(lambda d: d["days"][0], "head", flip)),
        ("a day's signed message", setter(lambda d: d["days"][0], "signed_msg", flip)),
        ("a day proof", setter(lambda d: d["days"][0]["proofs"][1], "path", lambda p: p[:-1])),
        ("a missing day row", setter(lambda d: d["days"][0], "rows", lambda rows: rows[1:])),
    )
    for name, mutate in mutations:
        doc = json.loads(json.dumps(base))
        mutate(doc)
        try:
            caught = bool(check_document(doc).errors)
        except FormatError:
            caught = True
        expect(caught, "check doesn't notice a changed %s" % name)
    return "%d kinds of tampering caught" % len(mutations)


def content_example():
    """The worked example in spec section 10.10, as (label, value) pairs.
    selftest checks that every value appears in the spec."""
    out = []
    keys = derive_keys(bytes(range(32)))
    text = "still broken, see src/nav.js:12\r\nTypeError: x is undefined"
    toks, salts, leaves, n, root = commit_text(keys["k_tok"], 2, text.encode("utf-8"))
    for j in (1, 2, 7, 15):
        out += [("salt %d" % j, salts[j - 1]), ("leaf %d" % j, leaves[j - 1])]
    out += [("n", n), ("C", root)]
    items = reveal_items(salts, toks, leaves, [(6, 1, "known", None)])
    out.append(("reveal verdict", verify_reveal(n, root, items)[0]))
    removed, words = normalize(text)
    out += [("normalized", " ".join(words)), ("signature", error_signature(text))]
    tag, xs = shingles(words)
    out += [("shingle %d" % i, json.dumps(x)) for i, x in enumerate(xs)]
    values = [shingle_value(keys["k_feat"], tag, x) for x in xs]
    out += [("value of %s" % json.dumps(xs[0]), values[0])]
    out += [("value %d of %s" % (i, json.dumps(xs[0])), "%08x" % h) for i, h in enumerate(minhash_values(values[0])[:8])]
    f, _ = prompt_features(keys["k_feat"], text, "error_paste")
    out += [("mh", f["mh"]), ("th", f["th"]), ("es", f["es"]),
            ("f", json.dumps(f, separators=(",", ":"))),
            ("c", json.dumps({"n": n, "r": root.hex()}, separators=(",", ":")))]
    tool = tool_features(keys["k_feat"], [("src/nav.js", 10, 3, 10, 5)], "npm test")
    out += [("tool f", json.dumps(tool, separators=(",", ":")))]
    r = bytes(range(32, 64))
    metadata = b"stand-in metadata reveal"
    seed = draw_seed(r, metadata)
    pool = [("d_example", q) for q in (2, 9, 14, 23, 31, 40, 52, 61)] + [("d_other", q) for q in (7, 18, 26, 33)]
    out += [("sha of metadata", sha256(metadata)), ("seed", seed),
            ("score of d_example 2", draw_score(seed, "d_example", 2)),
            ("drawn", ", ".join("%s %d" % item for item in draw(seed, pool)))]
    return out


def test_content_example():
    with open(repo_path("docs", "spec", "ledger-v1.md"), encoding="utf-8") as f:
        spec = f.read()
    values = content_example()
    for label, v in values:
        text = v.hex() if isinstance(v, bytes) else str(v)
        expect(text in spec, "the spec doesn't show part 2's %s, %s" % (label, text))
    return "%d values" % len(values)


def test_whitespace_set():
    """10.2's whitespace is Python's isspace() less U+001C to U+001F, which
    have bidirectional class B or S but aren't White_Space."""
    python = {cp for cp in range(0x110000) if not 0xD800 <= cp <= 0xDFFF and chr(cp).isspace()}
    want = python - {0x1C, 0x1D, 0x1E, 0x1F}
    expect(WHITESPACE == frozenset(want), "WHITESPACE differs from isspace() less U+001C..U+001F: %s" %
           sorted(WHITESPACE ^ want))
    return "%d code points" % len(WHITESPACE)


def python_lower(cp):
    """Python's lowercase of one code point, which is the simple mapping
    apart from U+0130, whose full mapping adds U+0307."""
    lower = chr(cp).lower()
    return 0x69 if cp == 0x130 else ord(lower) if len(lower) == 1 else None


def unicode_version(text):
    return tuple(int(x) for x in text.split("."))


def test_lowercase_table():
    """LOWER_RUNS against Python's unicodedata: equal for 15.0.0, equal less
    LOWER_LATER for 15.1.0 to 17.0.0, and each pair still held after that."""
    import unicodedata
    version = unicodedata.unidata_version
    later = dict(LOWER_LATER)
    expect(not set(later) & set(LOWER), "a later mapping is already in the 15.0.0 table")
    expect(all(cp not in WHITESPACE and lo not in WHITESPACE for cp, lo in LOWER.items()), "lowercasing touches whitespace")
    if unicode_version(version) > unicode_version(LOWER_LATER_UNICODE):
        # Unicode keeps every case pair, so each pinned mapping must still hold.
        for cp, lo in LOWER.items():
            expect(python_lower(cp) == lo, "U+%04X no longer lowercases to U+%04X in unicodedata %s" % (cp, lo, version))
        return "%d mappings; unicodedata %s is past %s, so only checked that each still holds" % (
            len(LOWER), version, LOWER_LATER_UNICODE)
    derived = {}
    for cp in range(0x110000):
        if 0xD800 <= cp <= 0xDFFF:
            continue
        lo = python_lower(cp)
        expect(lo is not None, "U+%04X lowercases to more than one code point in unicodedata %s" % (cp, version))
        if lo != cp:
            derived[cp] = lo
    extra = {cp: lo for cp, lo in derived.items() if cp not in LOWER}
    for cp, lo in LOWER.items():
        expect(derived.get(cp) == lo, "unicodedata %s doesn't lowercase U+%04X to U+%04X" % (version, cp, lo))
    for cp, lo in extra.items():
        expect(later.get(cp) == lo, "unicodedata %s lowercases U+%04X to U+%04X, which is in neither table" % (version, cp, lo))
    if version == LOWER_UNICODE:
        expect(not extra, "unicodedata 15.0.0 has mappings the table lacks")
        return "%d mappings, equal to unicodedata %s" % (len(LOWER), version)
    return "%d mappings, equal to unicodedata %s less %d later ones" % (len(LOWER), version, len(extra))


def test_tokens():
    """Tokens rebuild the text, alternate in kind, and the 16 KiB cut stops
    at a character boundary."""
    s = Stream("selftest-tokens")
    for _ in range(500):
        text = random_text(s, 40)
        toks = tokenize(text)
        expect("".join(toks) == text, "tokens don't rebuild %s" % json.dumps(text))
        expect(all(toks), "an empty token")
        expect(all(len({is_space(c) for c in t}) == 1 for t in toks), "a token of both kinds")
        expect(all(is_space(a[0]) != is_space(b[0]) for a, b in zip(toks, toks[1:])), "two tokens of one kind side by side")
    expect(tokenize("") == [], "the empty text has tokens")
    cases = 0
    for ch in ("a", "é", "中", "\U0001f64f"):
        width = len(ch.encode("utf-8"))
        for pad in range(MESSAGE_MAX - 4, MESSAGE_MAX + 1):
            raw = (b"x" * pad) + (ch * 3).encode("utf-8")
            cut = committed_message(raw)
            expect(len(cut) <= MESSAGE_MAX and raw.startswith(cut), "the cut isn't a prefix of at most 16 KiB")
            expect(len(raw) <= MESSAGE_MAX or MESSAGE_MAX - len(cut) < width, "the cut drops a whole character")
            cut.decode("utf-8")
            cases += 1
    return "500 random texts, %d cuts" % cases


def test_reveal_properties():
    """Random removals verify; any change to a kept token, a salt or a
    removed leaf doesn't."""
    s = Stream("selftest-reveals")
    k_tok = derive_keys(s.take(32))["k_tok"]
    checked = tampered = 0
    for i in range(100):
        text = random_text(s, 20).encode("utf-8")
        toks, salts, leaves, n, root = commit_text(k_tok, 1 + i, text)
        items = reveal_items(salts, toks, leaves, random_spans(s, n))
        expect(verify_reveal(n, root, items) == ("ok", None), "a random reveal of %s fails" % json.dumps(text.decode()))
        checked += 1
        for k, item in enumerate(items):
            if item[0] == "kept":
                changed = item[2][:-1] + bytes([item[2][-1] ^ 1])
                variants = [("kept", item[1], changed), ("kept", bytes([item[1][0] ^ 1]) + item[1][1:], item[2])]
            else:
                variants = [item[:3] + ([bytes([item[3][0][0] ^ 1]) + item[3][0][1:]] + item[3][1:],) + item[4:]]
            for v in variants:
                expect(verify_reveal(n, root, items[:k] + [v] + items[k + 1:])[0] != "ok",
                       "a changed item %d of a reveal still verifies" % k)
                tampered += 1
    return "%d reveals, %d tamperings" % (checked, tampered)


def test_minhash_estimates():
    """The hash family behaves like MinHash: over 100 pairs of random sets
    the estimate is unbiased and close to the true Jaccard similarity. With
    64 values its standard error is up to 0.0625, so the mean absolute error
    should sit near 0.04."""
    s = Stream("selftest-minhash")
    k_feat = derive_keys(s.take(32))["k_feat"]
    total = signed = 0.0
    for _ in range(100):
        universe = ["w%d" % s.below(1 << 30) for _ in range(40)]
        a = set(universe[:10 + s.below(25)])
        b = set(universe[s.below(25):])
        true = len(a & b) / len(a | b)
        sa = sketch([shingle_value(k_feat, WORD_SHINGLE, x) for x in a])
        sb = sketch([shingle_value(k_feat, WORD_SHINGLE, x) for x in b])
        err = jaccard_matches(sa, sb) / MINHASH_SIZE - true
        total += abs(err)
        signed += err
        expect(sa == sketch([shingle_value(k_feat, WORD_SHINGLE, x) for x in sorted(a, reverse=True)]),
               "the sketch depends on the order of the shingles")
    expect(total / 100 < 0.07, "the estimates are off by %.3f on average" % (total / 100))
    expect(abs(signed / 100) < 0.02, "the estimates are biased by %.3f" % (signed / 100))
    return "mean error %.3f, bias %+.3f over 100 pairs" % (total / 100, signed / 100)


def test_hunk_properties():
    """For random single edits of random files, the hunks of a patch with
    three lines of context, as Claude Code writes it, are the one hunk the
    text before and after gives, and the hunk counts changed lines only."""
    s = Stream("selftest-hunks")
    checked = 0
    for _ in range(300):
        before = ["line %d %s\n" % (i, s.take(2).hex()) for i in range(s.below(40))]
        at = s.below(len(before) + 1)
        removed = s.below(min(4, len(before) - at) + 1)
        added = ["new %s\n" % s.take(3).hex() for _ in range(s.below(4))]
        if not removed and not added:
            continue
        after = before[:at] + added + before[at + removed:]
        lo, hi = max(0, at - 3), min(len(before), at + removed + 3)
        lines = ([" " + x for x in before[lo:at]] + ["-" + x for x in before[at:at + removed]]
                 + ["+" + x for x in added] + [" " + x for x in before[at + removed:hi]])
        old_lines, new_lines = hi - lo, hi - lo - removed + len(added)
        old_start = lo + 1 if old_lines else lo
        new_start = lo + 1 if new_lines else lo
        got = hunks_from_patch(old_start, old_lines, new_start, new_lines, lines)
        want = hunk_from_texts("".join(before), "".join(after))
        expect(got == [want], "a patch's hunks %s aren't the text's %s" % (got, want))
        expect(want[1] == removed and want[3] == len(added), "the hunk %s counts lines that didn't change" % (want,))
        checked += 1
    return "%d random edits" % checked


def test_draw_properties():
    """The draw takes the CHALLENGE_K smallest scores whatever the pool's
    order, and CHALLENGE_K steps at the rounding boundaries."""
    for pool, k in ((0, 10), (100, 10), (101, 11), (110, 11), (111, 12), (290, 29), (291, 30), (5000, 30)):
        expect(challenge_k(pool) == k, "CHALLENGE_K(%d) is %d, want %d" % (pool, challenge_k(pool), k))
    s = Stream("selftest-draws")
    for size in (0, 1, 9, 10, 11, 57, 150):
        seed = s.take(32)
        pool = [("d_" + s.take(3).hex(), 1 + s.below(1 << 40)) for _ in range(size)]
        drawn = draw(seed, pool)
        shuffled = list(pool)
        s.shuffle(shuffled)
        expect(draw(seed, shuffled) == drawn, "the draw depends on the pool's order")
        everything = sorted(pool, key=lambda item: draw_score(seed, *item))
        expect(drawn == everything[:len(drawn)] and len(drawn) == min(size, challenge_k(size)),
               "the draw isn't the smallest scores")
    return "7 pools"


def test_content_vectors_regenerate():
    with open(repo_path("testdata", "ledger", "vectors-content.json"), encoding="ascii", newline="") as f:
        published = f.read()
    expect(published == content_vectors_text(), "testdata/ledger/vectors-content.json isn't what `vectors-content` writes")
    return "%d bytes" % len(published)


def test_content_vectors_check():
    rep = check_document(load_json("testdata", "ledger", "vectors-content.json"))
    expect(not rep.errors, "check finds %d mismatches, the first: %s" % (len(rep.errors), rep.errors[:1]))
    return summary(rep.counts)


def test_check_catches_content():
    """check reports each kind of tampering with the part 2 vectors."""
    full = load_json("testdata", "ledger", "vectors-content.json")

    def note_index(section, note):
        return next(i for i, item in enumerate(full[section]) if item.get("note") == note)

    # A slice of the vectors, so each mutation checks a few items.
    picks = {
        "commitments": ["short prompt"],
        "reveals": ["short prompt: everything kept", "short prompt: removal at the start"],
        "normalizations": ["the worked example of 10.10"],
        "features": ["the worked example of 10.10", "an edit across two files, with an insertion and a deletion",
                     "a command typed in a shell"],
        "diffs": ["three lines of context around three lines added after line 9", "an edit inside one line",
                  "a patch whose lines don't add up to its header is malformed"],
        "jaccard": ["one word changed"],
        "shingle_reveals": ["the worked example of 10.10: every shingle shown", "fix it for bob with bob removed"],
        "draws": ["a pool of 12 on two devices"],
    }
    base = {"format": FORMAT, "lowercase": full["lowercase"], "challenge_k": full["challenge_k"]}
    for name, notes in picks.items():
        base[name] = [full[name][note_index(name, note)] for note in notes]
    expect(not check_document(json.loads(json.dumps(base))).errors, "the slice of the content vectors doesn't pass check")

    def at(section, i, *keys):
        def get(doc):
            obj = doc[section][i]
            for k in keys:
                obj = obj[k]
            return obj
        return get

    def setter(get, key, fn):
        def apply(doc):
            obj = get(doc)
            obj[key] = fn(obj[key])
        return apply

    short, example, tool, shell = 0, 0, 1, 2
    long_msg = full["commitments"][note_index("commitments", "agent message over 16 KiB, cut before a split character")]

    def swap(key):
        def apply(doc):
            lst = doc["draws"][0][key]
            lst[0], lst[1] = lst[1], lst[0]
        return apply

    mutations = (
        ("a lowercase run", setter(lambda d: d["lowercase"]["runs"][0], "delta", lambda x: x + 1)),
        ("a committed text", setter(at("commitments", short), "text", flip)),
        ("a commitment root", setter(at("commitments", short), "root", flip)),
        ("a token count", setter(at("commitments", short), "n", lambda x: x + 1)),
        ("a token salt", setter(at("commitments", short, "tokens", 2), "salt", flip)),
        ("a token leaf", setter(at("commitments", short, "tokens", 2), "leaf", flip)),
        ("a token's bytes", setter(at("commitments", short, "tokens", 2), "token", flip)),
        ("a commitment's rtype", setter(at("commitments", short), "rtype", lambda _: AGENT_TOOL)),
        ("an agent message left uncut", lambda d: d.update(commitments=[
            dict(long_msg, text=long_msg["raw"][:2 * MESSAGE_MAX])])),
        ("a reveal's verdict", setter(at("reveals", 0), "verdict", lambda _: "root")),
        ("a kept token in a reveal", setter(at("reveals", 0, "items", 0), "token", flip)),
        ("a removed leaf in a reveal", setter(at("reveals", 1, "items", 0, "leaves"), 0, flip)),
        ("a normalized text", setter(at("normalizations", example), "normalized", flip)),
        ("the removed lines", setter(at("normalizations", example), "removed_lines", lambda _: [])),
        ("the shingle kind", setter(at("normalizations", example), "shingle_kind", lambda _: "word3")),
        ("an error signature", setter(at("normalizations", example), "signature", flip)),
        ("a missing shingle", setter(at("normalizations", example), "shingles", lambda xs: xs[1:])),
        ("a sketch", setter(at("features", 0, "f"), "mh", flip)),
        ("a template hash", setter(at("features", 0, "f"), "th", flip)),
        ("a dropped error signature", lambda d: d["features"][0]["f"].pop("es")),
        ("a prompt class", setter(at("features", 0, "f"), "pc", lambda _: "substantive")),
        ("a per-shingle value", setter(at("features", 0, "shingle_values"), 0, flip)),
        ("a hunk's path", setter(at("features", tool, "f", "h", 0), "p", flip)),
        ("a hunk's numbers", setter(at("features", tool, "f", "h", 0), "nl", lambda x: x + 1)),
        ("a command hash", setter(at("features", shell, "f"), "ch", flip)),
        ("a hunk from a patch", setter(at("diffs", 0, "hunks", 0), "new_start", lambda x: x + 1)),
        ("a hunk from the text before and after", setter(at("diffs", 1, "hunks", 0), "old_lines", lambda x: x + 1)),
        ("a malformed patch read as well formed", lambda d: d["diffs"][2].pop("malformed") and d["diffs"][2].update(hunks=[])),
        ("a Jaccard count", setter(at("jaccard", 0), "matches", lambda x: x + 1)),
        ("a shingle reveal's verdict", setter(at("shingle_reveals", 0), "verdict", lambda _: "sketch")),
        ("a shingle reveal's count of unexplained places", setter(at("shingle_reveals", 1), "unexplained", lambda x: x + 1)),
        ("a hidden shingle sent anyway", lambda d: d["shingle_reveals"][1].update(entries=sorted(
            d["shingle_reveals"][1]["entries"] + [{"v": d["shingle_reveals"][0]["entries"][0]["v"], "x": "666f722062"}],
            key=lambda e: e["v"]))),
        ("a shingle reveal's kind", setter(at("shingle_reveals", 0), "kind", lambda _: "word3")),
        ("a draw seed", setter(at("draws", 0), "seed", flip)),
        ("a pool score", setter(at("draws", 0, "pool", 3), "score", flip)),
        ("the drawn order", swap("drawn")),
        ("the drawn commits", swap("drawn_commits")),
        ("a CHALLENGE_K", setter(at("challenge_k", 7), "k", lambda x: x - 1)),
    )
    for name, mutate in mutations:
        doc = json.loads(json.dumps(base))
        mutate(doc)
        try:
            caught = bool(check_document(doc).errors)
        except FormatError:
            caught = True
        expect(caught, "check doesn't notice a changed %s" % name)
    return "%d kinds of tampering caught" % len(mutations)


SELFTESTS = (
    ("CT file checksums", test_ct_sums),
    ("RFC 4231 HMAC-SHA256", test_rfc4231),
    ("RFC 5869 HKDF-SHA256", test_rfc5869),
    ("CT roots and subtree hashes", test_ct_trees),
    ("CT inclusion proofs", test_ct_inclusion),
    ("Merkle properties", test_merkle),
    ("rtype table against pkg/event", test_rtypes),
    ("spec worked example", test_example),
    ("vectors regenerate", test_vectors_regenerate),
    ("vectors pass check", test_vectors_check),
    ("check catches tampering", test_check_catches),
    ("whitespace set", test_whitespace_set),
    ("Unicode 15.0.0 lowercase table", test_lowercase_table),
    ("tokens and the 16 KiB cut", test_tokens),
    ("reveal properties", test_reveal_properties),
    ("MinHash estimates", test_minhash_estimates),
    ("hunk properties", test_hunk_properties),
    ("draw properties", test_draw_properties),
    ("part 2 worked example", test_content_example),
    ("content vectors regenerate", test_content_vectors_regenerate),
    ("content vectors pass check", test_content_vectors_check),
    ("check catches content changes", test_check_catches_content),
)


def cmd_selftest():
    failed = 0
    for name, fn in SELFTESTS:
        try:
            print("ok    %-30s %s" % (name, fn()))
        except (SelftestError, LedgerError, FormatError, OSError, ValueError, KeyError) as err:
            failed += 1
            print("FAIL  %-30s %s" % (name, err))
    print("selftest: %s" % ("%d of %d failed" % (failed, len(SELFTESTS)) if failed else "all %d ok" % len(SELFTESTS)))
    return 1 if failed else 0


# ---------------------------------------------------------------------------

USAGE = """usage: ledger_ref.py selftest
       ledger_ref.py vectors [--out PATH]
       ledger_ref.py vectors-content [--out PATH]
       ledger_ref.py check FILE"""


def main(argv):
    if argv[:1] == ["selftest"] and len(argv) == 1:
        return cmd_selftest()
    if argv[:1] == ["vectors"] and len(argv) in (1, 3) and (len(argv) == 1 or argv[1] == "--out"):
        return cmd_vectors(argv[2] if len(argv) == 3 else repo_path("testdata", "ledger", "vectors.json"))
    if argv[:1] == ["vectors-content"] and len(argv) in (1, 3) and (len(argv) == 1 or argv[1] == "--out"):
        return cmd_vectors_content(argv[2] if len(argv) == 3 else repo_path("testdata", "ledger", "vectors-content.json"))
    if argv[:1] == ["check"] and len(argv) == 2:
        return cmd_check(argv[1])
    print(USAGE, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
