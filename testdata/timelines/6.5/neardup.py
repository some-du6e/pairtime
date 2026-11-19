# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 4.2's prompt normalization, shingles and re-roll count, for checking
that a fixture's prompt texts give the attempts the fixture claims. Exact
Jaccard similarity stands in for the 64-permutation MinHash estimate, so
gen.py also checks that every pair it calls different is far below
SIM_JACCARD, where an estimate can't cross it. It isn't the scorer.

Used by 6.5/gen.py and 4.2-alternation/gen.py.
"""

import re

SIM_JACCARD = 0.8
REROLL_WINDOW_MS = 1_800_000
# SM 4.2's retry-type go-aheads, which all become the token <retry>.
RETRY = ["still not working", "still broken", "doesn't work", "try again", "fix it", "retry", "again"]


def normalize(text):
    """SM 4.2's normalization: lowercase, numbers to <n>, retry phrases to
    <retry>, whitespace collapsed. SM's list doesn't mention punctuation,
    but its 20:07 row needs "fix it!!" and "fix it" to normalize the same,
    so punctuation goes too, as in SM 2.2's prompt-class rule (erratum
    2026-11-19-sm-6.5-loop-codes). Pasted blocks, paths, URLs, hex and
    backticked identifiers would be replaced as well; no fixture prompt has
    any."""
    s = text.lower()
    s = re.sub(r"\d+(?:\.\d+)?", "<n>", s)
    for phrase in sorted(RETRY, key=len, reverse=True):
        s = re.sub(r"(?<![\w'])" + re.escape(phrase) + r"(?![\w'])", "<retry>", s)
    s = re.sub(r"[^\w\s<>]", " ", s)
    return " ".join(s.split())


def shingles(text):
    """Word 3-shingles, or character 5-shingles under 6 words (SM 8.5)."""
    norm = normalize(text)
    words = norm.split()
    if len(words) >= 6:
        return {tuple(words[i:i + 3]) for i in range(len(words) - 2)}
    return {norm[i:i + 5] for i in range(max(1, len(norm) - 4))}


def jaccard(a, b):
    x, y = shingles(a), shingles(b)
    return len(x & y) / len(x | y)


def near_duplicate(a, b):
    return normalize(a) == normalize(b) or jaccard(a, b) >= SIM_JACCARD


def attempts(prompts, change_events):
    """SM 4.2's attempt number for each (t, text) in prompts, in order:
    1 + the earlier prompts after the last change event, at most
    REROLL_WINDOW_S before, that are near-duplicates."""
    out = []
    for i, (t, text) in enumerate(prompts):
        last_change = max((c for c in change_events if c < t), default=None)
        n = 1
        for tq, q in prompts[:i]:
            if last_change is not None and tq <= last_change:
                continue
            if t - tq <= REROLL_WINDOW_MS and near_duplicate(text, q):
                n += 1
        out.append(n)
    return out


def far_apart(prompts, limit=0.5):
    """Pairs of prompts with different normalized texts whose exact Jaccard
    similarity reaches limit: with 64 permutations, a MinHash estimate of a
    pair below 0.5 practically never reaches 0.8."""
    bad = []
    for i, a in enumerate(prompts):
        for b in prompts[i + 1:]:
            if normalize(a) != normalize(b) and jaccard(a, b) >= limit:
                bad.append((a, b, round(jaccard(a, b), 2)))
    return bad
