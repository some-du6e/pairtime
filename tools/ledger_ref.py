#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Reference implementation of pairtime's ledger format, part 1.

docs/spec/ledger-v1.md is the specification, and the section numbers in
this file are its sections. The code uses only the Python standard library
and shares nothing with pkg/ledger, which is what lets it check pkg/ledger.

    python3 -I tools/ledger_ref.py selftest
        Check the RFC 5869, RFC 4231 and Certificate Transparency vectors,
        the rtype table against pkg/event, the spec's worked example, and
        that testdata/ledger/vectors.json regenerates byte for byte and
        passes check.

    python3 -I tools/ledger_ref.py vectors [--out PATH]
        Write the vectors from fixed seeds, by default to
        testdata/ledger/vectors.json.

    python3 -I tools/ledger_ref.py check FILE
        Recompute every key, digest, nonce, entry, root, head, signed
        message and inclusion proof in FILE, a file in the vectors format
        (spec section 11), and print each mismatch.

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
    if rtype not in RTYPES:
        raise LedgerError("rtype 0x%02x isn't in the table" % rtype)
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
            if body.startswith(BODY_PREFIX):
                inner = body[len(BODY_PREFIX):len(BODY_PREFIX) + 64]
                if inner != want_nonce.hex().encode("ascii"):
                    rep.fail(rp + ".body", "has an \"n\" other than this record's nonce")
            try:
                rep.same(rp + ".entry", got_entry, entry(header(seq, rtype, project_key, session_key), body))
            except LedgerError as err:
                rep.fail(rp, str(err))
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


def check_document(doc):
    """Check a parsed vectors file and return a Report. Raises FormatError
    when the file doesn't follow the format."""
    rep = Report()
    fields(doc, "file", ("format",), ("spec", "generator", "note", "keys", "digests", "chains", "days"))
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
    if not rep.counts:
        raise FormatError("file: nothing to check")
    return rep


def summary(counts):
    order = ("key sets", "digests", "chains", "batches", "records", "days", "day rows", "inclusion proofs")
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
    cases = load_json("testdata", "ledger", "rfc4231.json")["cases"]
    for c in cases:
        want = bytes.fromhex(c["hmac_sha256"])
        got = hmac_sha256(bytes.fromhex(c["key"]), bytes.fromhex(c["data"]))[:len(want)]
        expect(got == want, "%s: got %s" % (c["name"], got.hex()))
    return "%d cases" % len(cases)


def test_rfc5869():
    cases = load_json("testdata", "ledger", "rfc5869.json")["cases"]
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
        ("an entry", setter(rec, "entry", flip)),
        ("a seq", setter(lambda d: rec(d, 1, 0), "seq", lambda s: s + 1)),
        ("a reserved rtype", setter(rec, "rtype", lambda _: 0x13)),
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
       ledger_ref.py check FILE"""


def main(argv):
    if argv[:1] == ["selftest"] and len(argv) == 1:
        return cmd_selftest()
    if argv[:1] == ["vectors"] and len(argv) in (1, 3) and (len(argv) == 1 or argv[1] == "--out"):
        return cmd_vectors(argv[2] if len(argv) == 3 else repo_path("testdata", "ledger", "vectors.json"))
    if argv[:1] == ["check"] and len(argv) == 2:
        return cmd_check(argv[1])
    print(USAGE, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
