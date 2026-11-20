# SPDX-License-Identifier: AGPL-3.0-or-later
"""The consistency-check fixtures: every scenario in testdata/checks, each
a copy of an SM fixture edited as DESIGN 7.3's "Catches" column describes.
README.md in this directory is the format and says which scenario proves
what.

    python3 -I testdata/checks/gen.py            # rewrite the checked-in files
    python3 -I testdata/checks/gen.py --out DIR
"""

import json
import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import checks as ck  # noqa: E402

tl = ck.tl
check = ck.check

# Every scenario rests on at least one reading DESIGN 7.3 leaves open.
READINGS = "2026-11-19-check-readings"
CC8_SPEC = ("ledger-v1.md 10.8 in PR #12 (WP-LG3, not merged yet). Issue #15: CC8 is weaker than DESIGN 7.3 "
            "claims, and these cases follow the spec's stated limit")

# 4.2-alternation: Claude Code on macOS, one project, five prompts.
ALT = "4.2-alternation"
DASH = "p_b2738424d7c2"
ALT_SESSION = "s_45dccad56214"
ALT_W = (1795422660000, 1795423560000)  # 19:31 to 19:46 at UTC+11, 2026-11-23
A1, B1, A2, B2, A3 = 1795422780000, 1795422960000, 1795423140000, 1795423320000, 1795423500000
TEXT_A = "make the sidebar collapse into an icon rail on small screens"
TEXT_B = "keep the top bar pinned to the top of the page as you scroll"
TEXTS = {A1: TEXT_A, B1: TEXT_B, A2: TEXT_A, B2: TEXT_B, A3: TEXT_A}
# What a client that wants A3 credited commits instead of A3's sketch: an
# unrelated request's, so A3 is no near-duplicate of A1 or A2.
DECOY = "add a dark mode toggle to the settings page header"
# The made-up key of 4.2-cc8-made-up-key, the client's own, not K_feat.
FAKE_KEY = ck.sha256(b"pairtime check fixtures: a made-up feature key")
# Typing-module counts in each prompt's three typing slots (the 40 s before
# it): 60 characters, three backspaces, three retyped, and the Enter.
TYPING_SLOTS = ((-45_000, 15), (-30_000, 25), (-15_000, 26))
CLAUDE_TYPED = (1795422570000, 7)  # `claude` and Enter, in the slot from 19:29:30

# 6.2: Cursor on Windows 10, no key counter, one project, one commit.
S62 = "6.2"
REMIND = "p_be4901c04a84"
W62 = (1795524000000, 1795524300000)  # 07:40 to 07:45 at UTC-05:00, 2026-11-24
COMMIT62 = "99d45dfe857548da524bb234c7ce7c29f267c9df"
# 6.2's credited seconds by slot, from its rows and README: 07:40 P; 07:41
# SUP to :20, X.agent_only to :40, then the hand edit's E lead; 07:42 E;
# 07:43 E to :40 and the commit's G; 07:44 G to :30, READ to :45, and the
# slot that overlaps the suspend, X.absent.
SLOTS62 = [15, 15, 15, 15, 15, 5, 5, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 0]

# 6.3: four projects on macOS, Claude Code, Codex, Cursor and VS Code.
S63 = "6.3"
A, B, D, E = "p_0e6a8e0b849e", "p_a73dbaeaf36c", "p_dedc43f1d319", "p_418d8413d1cf"
W63 = (1795903200000, 1795905000000)  # 14:00 to 14:30 at UTC-08:00, 2026-11-28
COMMIT63 = "b578278d53a73afd97d403fe61ab940c4cff4183"
A_TURN2 = "t_95161e03c19a"


def iso(t):
    return ck.utc(t)


class Scenario:
    def __init__(self, name, title, story, *, base, projects, window):
        self.name = name
        self.title = title
        self.story = story
        self.base = base
        self.edits = []
        self.devices = []
        self.sub = {"projects": projects, "start": window[0], "end": window[1]}
        self.commits = None
        self.challenge = None
        self.typing = None
        self.mutations = []
        self.extras = {}
        self.hide = ()
        self.omit = ()
        self.b0 = {}
        self.classes = {}
        self.stated = {}
        self.slots = {}
        self.notes = []
        self.errata = [READINGS]
        self.known_limit = None
        self.fails = []

    def device(self, dev, meta, reveals=True):
        self.devices.append((dev, meta, reveals))
        return dev

    def edit(self, text):
        self.edits.append(text)

    def state(self, cc, result, **counts):
        self.stated[cc] = (result, counts)

    def not_exercised(self, cc, why):
        self.stated[cc] = ("not_exercised", why)

    def build(self, out):
        check(len([d for d in self.devices if d[2]]) == 1, f"{self.name}: one device reveals")
        dev = [d for d, _, r in self.devices if r][0]
        led = [ck.ledger(dev, dev.seq0 // 3, ck.rescore_range(self.sub), self.extras, self.hide, self.omit)]
        sub = self.sub
        computed = {
            "CC1": ck.cc1(dev.events, sub),
            "CC2": ck.cc2(dev.events, sub, dev.keys),
            "CC3": ck.cc3(dev.events, sub),
            "CC4": ck.cc4(dev.events, sub, self.commits or []),
            "CC5": self.cc5(),
            "CC6": self.cc6(),
            "CC7": ck.cc7(led, sub),
            "CC8": ck.cc8(self.challenge, led) if self.challenge is not None else None,
            "CC9": ck.cc9(dev.events, sub),
            "CC10": ck.cc10(dev.events, sub),
            "CC11": ck.cc11(dev.events, sub, dev.keys, self.typing, self.classes),
        }
        results, not_exercised = {}, {}
        for cc in ck.CHECKS:
            want = self.stated.get(cc)
            check(want is not None, f"{self.name}: {cc} is stated")
            if want[0] == "not_exercised":
                not_exercised[cc] = want[1]
                continue
            got = computed[cc]
            check(got is not None, f"{self.name}: {cc} has inputs")
            result, counts = want
            check(got["result"] == result, f"{self.name}: {cc} is {got['result']}, stated {result}: {got}")
            for k, v in counts.items():
                check(got["counts"].get(k) == v, f"{self.name}: {cc} {k} is {got['counts'].get(k)}, stated {v}")
            results[cc] = got
        fails = [cc for cc, r in results.items() if r["result"] == "fail"]
        check(fails == self.fails, f"{self.name}: fails {fails}, stated {self.fails}")
        files = {}
        events = sorted((e for d, _, _ in self.devices for e in d.events), key=lambda e: e["t"])
        files["events.jsonl"] = events
        files["ledger.json"] = {"note": LEDGER_NOTE, "devices": led}
        if self.commits is not None:
            files["commits.json"] = {"note": COMMITS_NOTE, "commits": self.commits}
        if self.challenge is not None:
            files["challenge.json"] = self.challenge
        if self.typing is not None:
            files["typing.json"] = self.typing
        doc = {
            "scenario": self.name,
            "title": self.title,
            "story": self.story,
            "base": self.base,
            "edits": self.edits,
            "devices": [{"device_id": d.id, "base": d.base, **meta, "reveals": r} for d, meta, r in self.devices],
            "submission": {"projects": sub["projects"],
                           "window": {"start": iso(sub["start"]), "end": iso(sub["end"])}},
            "live": {"from": "rescore", "mutations": self.mutations},
            "fails": self.fails,
            "checks": results,
            "not_exercised": not_exercised,
            "known_limit": self.known_limit,
            "errata": self.errata,
            "notes": self.notes,
        }
        files["expected.json"] = doc
        ck.write(out, self.name, files)

    def cc5(self):
        """One attention stream, from each device's credited slots: the
        scenario states them where the scorer's output follows from SM's
        rows, and otherwise only that one device credits each slot once."""
        counts = {"devices": len(self.devices), "slots_credited_twice": 0}
        if not self.slots:
            result, why = self.cc5_unknown
            if result == "not_computable":
                return ck.outcome(result, counts, reason=why)
            out = ck.outcome(result, counts)
            out["basis"] = why
            return out
        merged = {}
        for d, _, _ in self.devices:
            for s, cr in self.slots[d.id].items():
                if cr:
                    merged.setdefault(s, []).append((d.id, cr))
        counts["slots_credited"] = sum(len(v) for v in merged.values())
        dropped, items = 0, []
        for s, credits in sorted(merged.items()):
            if len(credits) < 2:
                continue
            counts["slots_credited_twice"] += 1
            # SM 3.4: keep more credited seconds, then input_keys, then the
            # lower device_id. Every device here that keeps a slot has both.
            keep = sorted(credits, key=lambda c: (-c[1], not self.keys_of(c[0]), c[0]))[0]
            for c in credits:
                if c != keep:
                    dropped += c[1]
            items.append({"slot": iso(s), "devices": [c[0] for c in credits], "kept": keep[0]})
        counts["merge_dropped_s"] = dropped
        if not counts["slots_credited"]:
            return ck.outcome("not_computable", counts, reason="no slot credited")
        return ck.outcome("fail" if items else "pass", counts, items)

    cc5_unknown = ("pass", "one device, whose scorer credits each slot to at most one project (SM 3.1)")

    def keys_of(self, device_id):
        return [d for d, _, _ in self.devices if d.id == device_id][0].keys

    def cc6(self):
        """Live against re-score, stated, not recomputed: no pkg/score
        exists yet. The live buckets are each device's own re-score with the
        mutations applied, so the minutes that differ are the mutated ones,
        and they are held. basis says what the result rests on."""
        counts = {"minutes_mismatched": len(self.mutations), "held_s": sum(m["held_s"] for m in self.mutations)}
        items = [{"minute": m["minute"], "held_s": m["held_s"]} for m in self.mutations]
        out = ck.outcome("fail" if self.mutations else "pass", counts, items)
        out["basis"] = self.cc6_basis or (CC6_MUTATED if self.mutations else CC6_SAME)
        return out

    cc6_basis = None


CC6_SAME = ("Stated, not recomputed: the live buckets are pkg/score over this device's events.jsonl, which the "
            "reveal holds in full, so the server's re-score is the same computation and every minute reproduces.")
CC6_MUTATED = ("Stated, not recomputed: the live buckets are pkg/score over this device's events.jsonl, which the "
               "reveal holds in full, with live.mutations applied, so the mutated minutes alone differ.")

LEDGER_NOTE = ("devices[].batches is the client's metadata reveal: each batch's records with their header fields "
               "and stored body. devices[].witnessed is what the server stored when each batch arrived. The device "
               "keys are test keys from checks.device_keys; the body's nonce is the only value that needs them.")
COMMITS_NOTE = ("What the server holds for each commit: the observed_at the laptop sent (the git.change's t), "
                "server_first_seen_at by its own clock, and what the forge returned for the SHA.")


# Bases.


def alt(sc, *, transcripts=True, typing=True, features=True, skip_turns=()):
    """4.2-alternation with what a real client also records: the log
    reader's copy of each prompt from the transcript, the typing module's
    counts, and each prompt's features and commitment in its body."""
    dev = ck.Device(ALT)
    if transcripts:
        for h, delay in zip(dev.turn_prompts("hook"), (700, 900, 1100, 4500, 800)):
            if h["t"] in skip_turns:
                continue
            dev.insert(ck.transcript_copy(dev, h, delay))
        sc.edit("The transcript's user entry for each prompt, read by the log reader 0.7 to 4.5 s after the hook "
                "(via log, same turn_key and lengths), as a real Claude Code client records it.")
    sc.device(dev, {"os": "macos", "input_keys": True, "typing_module": typing, "vm": False})
    sc.classes = {e["data"]["turn_key"]: "substantive" for e in dev.turn_prompts("hook")}
    return dev


def alt_typing(dev, pasted=None):
    """The typing module's per-slot key-down counts for ALT, matching the
    slots the samples show key presses in."""
    counts = {CLAUDE_TYPED[0]: CLAUDE_TYPED[1]}
    for t in (A1, B1, A2, B2, A3):
        for off, n in TYPING_SLOTS:
            counts[t + off] = n
    if pasted is not None:
        for off, _ in TYPING_SLOTS:
            counts.pop(pasted + off)
        counts[pasted - 15_000] = 3
    slots = [{"start": s, "keys": n, "injected": 0} for s, n in sorted(counts.items())]
    first, last = dev.events[0]["t"], dev.events[-1]["t"]
    s = first - first % ck.SLOT_MS
    while s <= last:
        check(dev.keys_in_slot(s) == (s in counts), f"typing counts and key presses agree in the slot at {s}")
        s += ck.SLOT_MS
    return {"device_id": dev.id, "module": "on", "vm": False, "note": TYPING_NOTE, "slots": slots}


TYPING_NOTE = ("A stand-in: pkg/event has no field for the typing module's per-15-second key-down counts and "
               "injected share yet (DESIGN 7.2 rank 5, M5). Each slot is {start, keys, injected}; slots not listed "
               "have no key-down. keys counts every key-down, injected the ones the module saw injected.")


def alt_features(sc, dev, *, a3=None):
    """Each prompt.submit's f and c in its ledger body (ledger-v1.md 10.7,
    10.2), under the device's K_feat and K_tok: the hook's record and the
    log reader's copy alike, each committing the prompt's text under its own
    seq. a3 replaces the f of both of A3's records."""
    keys = ck.device_keys(dev.id)
    text_of = {h["data"]["turn_key"]: TEXTS[h["t"]] for h in dev.turn_prompts("hook")}
    a3_turn = dev.one("prompt.submit", A3, via="hook")["data"]["turn_key"]
    for p in dev.find("prompt.submit"):
        turn = p["data"]["turn_key"]
        text = text_of[turn]
        f = ck.features(keys["k_feat"], text, "substantive")
        if turn == a3_turn and a3 is not None:
            f = a3
        sc.extras[p["seq"]] = {"f": f, "c": ck.commitment(keys["k_tok"], p["seq"], text)["c"]}
    return keys


def alt_challenge(sc, dev, keys, drawn, *, a3_key=None, a3_hidden=()):
    """The draw and the reveals: every pool item is drawn, because the
    pool is under CHALLENGE_K = 10."""
    hooks = {h["t"]: h for h in dev.turn_prompts("hook")}
    items = []
    for t in drawn:
        h = hooks[t]
        com = ck.commitment(keys["k_tok"], h["seq"], TEXTS[t])
        key = a3_key if t == A3 and a3_key is not None else keys["k_feat"]
        hidden = a3_hidden if t == A3 else ()
        items.append({"seq": h["seq"], "turn_key": h["data"]["turn_key"],
                      "text": ck.text_reveal(com, hidden), "cc8": ck.shingle_reveal(key, TEXTS[t], hidden)})
    pool = [hooks[t]["seq"] for t in drawn]
    sc.challenge = {"device_id": dev.id, "pool": pool, "k": ck.CHALLENGE_K_MIN, "drawn": items,
                    "note": CHALLENGE_NOTE}


CHALLENGE_NOTE = ("The pool is every human prompt in the submission that opened a credited anchor (DESIGN 7.5 step "
                  "4). It is under CHALLENGE_K, so the draw takes all of it and the seed doesn't matter here. For "
                  "each drawn prompt: text is its redacted reveal (ledger-v1.md 10.3), cc8 its per-shingle values "
                  "(10.8). The committed f and c are in the prompt's body in ledger.json. The reveal of the agent "
                  "message each prompt answered isn't here: the fixtures have no agent text, and CC8 doesn't read it.")


def alt_clean_state(sc, *, cc1=None, cc5=True):
    """What every ALT scenario shares with 4.2-clean unless it says
    otherwise."""
    if cc1 is None:
        sc.state("CC1", "pass", hook_prompts=5, log_entries=5, matched=5)
    sc.state("CC2", "pass", prompts=5, with_input_and_focus=5)
    sc.state("CC3", "not_computable", commits=0)
    sc.state("CC4", "not_computable", commits=0)
    sc.state("CC6", "pass", minutes_mismatched=0, held_s=0)
    sc.state("CC7", "pass", root_mismatches=0, head_mismatches=0, seq_gaps=0)
    sc.state("CC8", "pass", challenged=4, sketch_ok=4)
    sc.state("CC9", "pass", sessions=1)
    sc.state("CC10", "not_computable", editor_acts=0)
    sc.state("CC11", "pass", prompts=5, typed_chars=300, key_downs=330, too_few_keys=0)
    if cc5:
        sc.state("CC5", "pass", devices=1, slots_credited=56, slots_credited_twice=0)


def alt_slots():
    """ALT's credited slots: every slot of 19:31 to 19:44 credits 15 s, and
    19:45 is X.reroll."""
    return {ALT_W[0] + i * ck.SLOT_MS: (15 if i < 56 else 0) for i in range(60)}


# The scenarios.


def s_42_clean(out):
    sc = Scenario("4.2-clean", "SM 4.2's alternation, as a real client records it",
                  "The base timeline with the transcript, the typing module and the ledger features a client "
                  "writes. Every check that has inputs here passes.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    dev.finish()
    keys = alt_features(sc, dev)
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2))
    sc.slots = {dev.id: alt_slots()}
    alt_clean_state(sc)
    sc.notes.append("A3 is attempt 3 of A, discounted as X.reroll, so it opened no credited anchor and isn't in "
                    "the pool. The other four are drawn.")
    sc.build(out)


def s_42_cc1(out):
    sc = Scenario("4.2-cc1-no-transcript", "A hook prompt with no transcript entry",
                  "B1's whole turn came from a script calling `pairtime hook` with invented payloads: its prompt, "
                  "tool call and stop reached the hook, and Claude Code's transcript has no trace of it.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc, skip_turns=(B1,))
    b1 = dev.one("prompt.submit", B1, via="hook")["data"]["turn_key"]
    dropped = dev.remove(lambda e: e["kind"] == "agent.output" and e["data"]["turn_key"] == b1)
    check(dropped == 2, "B1's plan and answer came from the transcript")
    sc.edit("No transcript entry for B1's prompt, and B1's two agent.output records, which came from the "
            "transcript, are gone too: the transcript has nothing of B1's turn.")
    dev.finish()
    keys = alt_features(sc, dev)
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2))
    sc.fails = ["CC1"]
    sc.state("CC1", "fail", hook_prompts=5, log_entries=4, matched=4, hook_only=1, log_only=0)
    alt_clean_state(sc, cc1=False, cc5=False)
    sc.state("CC5", "pass", devices=1, slots_credited_twice=0)
    sc.notes.append("B1's turn still has its hook prompt, tool call and stop, so CC9 finds its prompt. B1 still "
                    "opens a credited anchor, so it is still drawn.")
    sc.build(out)


def s_42_cc2(out):
    sc = Scenario("4.2-cc2-send-keys", "Prompts typed into the TUI with tmux send-keys",
                  "Claude Code runs inside tmux, and a script in another pane types all five prompts into it with "
                  "`tmux send-keys`. The person only moves the mouse, so the OS sees no key press after `claude` "
                  "is started.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    start = dev.one("session.start")
    start["data"]["parent_class"] = "multiplexer"
    gone = dev.drop_keys(1795422582000, dev.events[-1]["t"])
    check(len(gone) == 15, "the typing before the five prompts is all that goes")
    sc.edit("session.start's parent_class is multiplexer (tmux), which SM 2.2 rule 4 doesn't count as headless.")
    sc.edit("No key press after 19:29:42: every typing run before a prompt is gone from the samples' idle_keys_s. "
            "Pointer input stays, so presence and the 15 s sampling don't change.")
    dev.finish()
    keys = alt_features(sc, dev)
    sc.typing = {"device_id": dev.id, "module": "on", "vm": False, "note": TYPING_NOTE,
                 "slots": [{"start": CLAUDE_TYPED[0], "keys": CLAUDE_TYPED[1], "injected": 0}]}
    sc.challenge = {"device_id": dev.id, "pool": [], "k": ck.CHALLENGE_K_MIN, "drawn": [], "note": CHALLENGE_NOTE}
    sc.fails = ["CC2"]
    sc.state("CC1", "pass", hook_prompts=5, log_entries=5, matched=5)
    sc.state("CC2", "fail", prompts=5, with_input_and_focus=0, no_input=5, no_focus=0)
    sc.state("CC3", "not_computable", commits=0)
    sc.state("CC4", "not_computable", commits=0)
    sc.cc5_unknown = ("not_computable", "no slot credited: every prompt is no_input, so nothing opened a window")
    sc.state("CC5", "not_computable", devices=1, slots_credited_twice=0)
    sc.state("CC6", "pass", minutes_mismatched=0, held_s=0)
    sc.state("CC7", "pass", root_mismatches=0)
    sc.state("CC8", "not_computable", challenged=0)
    sc.state("CC9", "pass", sessions=1, parent_class_changes=0)
    sc.state("CC10", "not_computable", editor_acts=0)
    sc.state("CC11", "not_computable", prompts=0)
    sc.notes.append("tmux send-keys writes to the pty, so neither the OS idle time nor the typing module sees the "
                    "keys. The module's only count is `claude` and its Enter.")
    sc.notes.append("Every prompt is origin no_input (SM 2.2 rule 8), so no anchor opens and nothing is credited: "
                    "CC5, CC8 and CC11 have nothing to compare. The buckets that exist hold agent_running_s only, "
                    "and the live ones equal the re-score.")
    sc.build(out)


def s_42_cc5(out):
    sc = Scenario("4.2-cc5-second-device", "A second device credits the same slots",
                  "While the person works through 4.2-alternation on the Mac, someone signed in to the same account "
                  "runs 6.2's five minutes on a Windows laptop, shifted to 19:36. Both devices credit every slot "
                  "from 19:36:00 to 19:40:45.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    dev.finish()
    keys = alt_features(sc, dev)
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2))
    other = ck.Device(S62)
    delta = B1 - W62[0]  # 6.2's 07:40:00 at 4.2's 19:36:00
    other.shift(delta)
    other.finish()
    sc.device(other, {"os": "windows", "input_keys": False, "typing_module": False, "vm": False}, reveals=False)
    sc.edit(f"A second device: 6.2's events, moved by {delta} ms so its 07:40:00 falls on 4.2's 19:36:00 "
            "(08:36:00Z). It submits nothing and reveals nothing; the server has its live buckets.")
    sc.slots = {dev.id: alt_slots(),
                other.id: {B1 + i * ck.SLOT_MS: cr for i, cr in enumerate(SLOTS62)}}
    sc.fails = ["CC5"]
    alt_clean_state(sc, cc5=False)
    sc.state("CC5", "fail", devices=2, slots_credited=75, slots_credited_twice=19, merge_dropped_s=265)
    sc.notes.append("SM 3.4's merge keeps the Mac's slot every time: it credits 15 s in each, the laptop at most "
                    "as much, and where both credit 15 s the Mac has input_keys and the lower device_id. The "
                    "laptop's 265 s are dropped and concurrent_devices is raised. That is WP-SV7's merge; CC5 "
                    "reports the conflict and changes no credit (DESIGN 7.3).")
    sc.notes.append("The laptop's slots are 6.2's: 15 s in each slot of 07:40, 07:42 and 07:43, 15, 5, 5 and 15 "
                    "in 07:41, and 15, 15, 15 and 0 in 07:44, which overlaps the suspend.")
    sc.build(out)


def s_42_cc7(out):
    sc = Scenario("4.2-cc7-dropped-record", "A record left out of the reveal",
                  "The client's metadata reveal leaves out one record, the 30-word plan A1's agent showed at "
                  "19:33:05. The server stored the batch's root with that record in it.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    dev.finish()
    plan = dev.one("agent.output", A1 + 5000)
    sc.hide = (plan["seq"],)
    sc.edit(f"The reveal has no record {plan['seq']}, the agent.output at 19:33:05. events.jsonl keeps it, as "
            "the client recorded it.")
    keys = alt_features(sc, dev)
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2))
    sc.slots = {dev.id: alt_slots()}
    sc.fails = ["CC7"]
    alt_clean_state(sc)
    sc.state("CC7", "fail", missing_batches=0, seq_gaps=1, count_mismatches=1, root_mismatches=1,
             head_mismatches=1, body_mismatches=0, held_minutes=2)
    sc.cc6_basis = ("Stated, not recomputed: the server's re-score lacks the plan, which only fills A1's reading "
                    "bucket, and that bucket is drained in 19:33, which CC7 holds. Nothing drains it past its 20 s "
                    "base anyway, because B1's lead starts at 19:33:20, so every other minute reproduces.")
    sc.notes.append("The batch from 19:33:00.7 to 19:33:58 doesn't reproduce: one record short, so its root and "
                    "head differ from the server's, and seq skips. Every other batch reproduces against the "
                    "server's stored prev. Its records are unverified, so CC7 holds every minute from the last "
                    "verified record before it, A1's prompt at 19:33:00, to the first after it, A1's stop at "
                    "19:34:00: 19:33 and 19:34, 120 s.")
    sc.build(out)


def s_42_cc7_batch(out):
    sc = Scenario("4.2-cc7-dropped-batch", "A batch left out of the reveal",
                  "The client's metadata reveal stops one batch short. It leaves out the last batch the server "
                  "stored for the re-score range, which holds A3's stop at 19:46:00 and the presence sample taken "
                  "with it. Every batch it reveals reproduces, and b and seq rise with no gap, so only the "
                  "server's own rows show what is missing.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    dev.finish()
    keys = alt_features(sc, dev)
    whole = ck.ledger(dev, dev.seq0 // 3, ck.rescore_range(sc.sub), sc.extras)
    last = whole["batches"][-1]
    kinds = [json.loads(r["body"])["e"]["kind"] for r in last["records"]]
    check(kinds == ["agent.stop", "os.presence"], f"the last batch is A3's stop and its sample, got {kinds}")
    check(whole["rescore_range"]["last_b"] == last["b"], "the last batch is in the re-score range")
    sc.omit = (last["b"],)
    sc.edit(f"The reveal has no batch {last['b']}, the last one the server stored for the re-score range: A3's "
            "agent.stop at 19:46:00 and its presence sample. events.jsonl keeps both, as the client recorded "
            "them.")
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2))
    sc.slots = {dev.id: alt_slots()}
    sc.fails = ["CC7"]
    alt_clean_state(sc)
    sc.state("CC7", "fail", missing_batches=1, batch_gaps=0, seq_gaps=0, count_mismatches=0, root_mismatches=0,
             head_mismatches=0, held_minutes=1)
    sc.cc6_basis = ("Stated, not recomputed: the server's re-score lacks A3's stop and the 19:46:00 sample, which "
                    "bear only on 19:45: the sample closes its last slot, and the stop ends a turn that 19:45 "
                    "discounts as X.reroll. CC7 holds 19:45, and every other minute of W depends only on revealed "
                    "records, so it reproduces.")
    sc.notes.append("A reveal that drops batches at either end of the range shows no gap in b or seq and nothing "
                    "that fails to reproduce. Only the server's rows for the re-score range, 19:16 to 20:01, show "
                    "that a batch is missing (DESIGN 7.5 step 3).")
    sc.notes.append("The missing batch's records lie between the last verified record, A3's answer at 19:45:58, "
                    "and the time the server received the batch, 19:46:01, so CC7 holds 19:45 and 19:46. Only "
                    "19:45 is in W, and it credits nothing: A3 is the third A.")
    sc.notes.append("The other checks read records in W, and the missing batch has none: its two records are at "
                    "19:46:00, where W ends.")
    sc.build(out)


def s_42_cc8_false(out):
    sc = Scenario("4.2-cc8-false-sketch", "A falsified sketch, true values revealed",
                  "A modified client commits A3's features as if A3 were an unrelated request, so the re-roll rule "
                  "misses it and A3 is credited. A3 is drawn, and the client reveals A3's true per-shingle values.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    dev.finish()
    k = ck.device_keys(dev.id)
    false = ck.features(k["k_feat"], DECOY, "substantive")
    keys = alt_features(sc, dev, a3=false)
    sc.edit(f"A3's f holds the sketch and template of {DECOY!r} under the device's K_feat. Its c is honest: it "
            "commits A3's real text, so the content reveal verifies.")
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2, A3))
    sc.fails = ["CC8"]
    alt_clean_state(sc, cc5=False)
    sc.state("CC5", "pass", devices=1, slots_credited_twice=0)
    sc.state("CC8", "fail", challenged=5, sketch_ok=4, sketch_mismatch=1, shingle_conflicts=0)
    sc.notes.append("The server re-scores from the bodies' f (DESIGN 6.2 D18), which say A3 isn't a re-roll, so "
                    "the re-score credits A3 as the live buckets do and CC6 passes. Only CC8 sees the lie: the "
                    "sketch from A3's revealed values is A1's, not the committed one (ledger-v1.md 10.8 `sketch`).")
    sc.notes.append(CC8_SPEC)
    sc.build(out)


def s_42_cc8_key(out):
    sc = Scenario("4.2-cc8-made-up-key", "A sketch under a made-up key, caught by a shared shingle",
                  "The client commits A3's sketch under a key of its own and reveals values under that key, so "
                  "A3's sketch reproduces. A1 and A2 are drawn too, honestly, and show the same shingles with "
                  "other values.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    dev.finish()
    keys = alt_features(sc, dev, a3=ck.features(FAKE_KEY, TEXT_A, "substantive"))
    sc.edit("A3's f is computed under a made-up key instead of K_feat. Its c is honest.")
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2, A3), a3_key=FAKE_KEY)
    sc.fails = ["CC8"]
    alt_clean_state(sc, cc5=False)
    sc.state("CC5", "pass", devices=1, slots_credited_twice=0)
    sc.state("CC8", "fail", challenged=5, sketch_ok=5, sketch_mismatch=0, shingle_mismatch=0, shingle_conflicts=9)
    sc.notes.append("Each reveal verifies on its own (ledger-v1.md 10.8 `ok`). The failure is the cross-reveal "
                    "rule: A3's nine shingles are A1's, shown with other values.")
    sc.notes.append(CC8_SPEC)
    sc.build(out)


def s_42_cc8_limit(out):
    sc = Scenario("4.2-cc8-removed-spans", "A made-up key that CC8 can't catch",
                  "As 4.2-cc8-made-up-key, but the teen removes three words of A3 by hand in its content reveal, so "
                  "every shingle of A3 touches a removed span and is shown without its text. Nothing is left to "
                  "compare across reveals, and CC8 passes. This is the limit ledger-v1.md 10.8 states.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    dev.finish()
    keys = alt_features(sc, dev, a3=ck.features(FAKE_KEY, TEXT_A, "substantive"))
    sc.edit("A3's f is computed under a made-up key. In A3's reveals, words 3, 6 and 9 (`sidebar`, `an`, `on`) "
            "are removed as type teen, which hides all nine shingles' texts.")
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2, A3), a3_key=FAKE_KEY, a3_hidden=(2, 5, 8))
    sc.known_limit = ("ledger-v1.md 10.8 (PR #12): a client that commits a sketch under a made-up key is caught only "
                      "when two revealed turns share a shingle and show different values, and a span removed by hand "
                      "over the shared words hides even that. Issue #15 asks Karim to accept, bind the key or drop "
                      "CC8's dark-flow role. DESIGN 7.3's 0.65 for a 10-turn draw doesn't hold here.")
    alt_clean_state(sc, cc5=False)
    sc.state("CC5", "pass", devices=1, slots_credited_twice=0)
    sc.state("CC8", "pass", challenged=5, sketch_ok=5, shingle_conflicts=0, hidden_shingles=9)
    sc.notes.append("A reviewer reading A3's kept text, `make the ... collapse into ... icon rail ... small "
                    "screens`, can still see the re-roll. CC8, the automatic check, can't.")
    sc.build(out)


def s_42_cc11(out):
    sc = Scenario("4.2-cc11-pasted", "A pasted prompt passed off as typed",
                  "B2 was pasted with Cmd-V, and Claude Code didn't mark the paste, so its 60 characters count as "
                  "typed. The typing module counts three key-downs in B2's lead window: Cmd, V and Enter.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    gone = dev.drop_keys(B2 - 45_000, B2 - 1000)
    dev.add_keys(B2 - 4000)
    check(len(gone) == 2, "two of B2's three typing samples' key presses go; the Enter stays")
    sc.edit("No typing before B2: the key presses from 19:41:15 to 19:41:59 are gone, and a Cmd-V at 19:41:56 "
            "joins the Enter at 19:41:59.6.")
    dev.finish()
    keys = alt_features(sc, dev)
    sc.typing = alt_typing(dev, pasted=B2)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2))
    sc.fails = ["CC11"]
    alt_clean_state(sc, cc5=False)
    sc.state("CC5", "pass", devices=1, slots_credited_twice=0)
    sc.state("CC11", "fail", prompts=5, typed_chars=300, key_downs=267, injected=0, too_few_keys=1)
    sc.notes.append("B2 still passes SM 2.2 rule 6: key presses in the 10 s before submit, focus on the terminal, "
                    "and 60 characters within 15 characters a second over its one input slot. So it is "
                    "human_typed, CC2 counts it, and it is drawn.")
    sc.notes.append("CC11's verdict needs no threshold DESIGN leaves open: 3 key-downs for 60 typed characters is "
                    "too few by any reading, and 66 for 60 isn't.")
    sc.build(out)


def s62(sc):
    dev = ck.Device(S62)
    sc.device(dev, {"os": "windows", "input_keys": False, "typing_module": False, "vm": False})
    return dev


def s62_commit(dev, led_received, *, seen=None, forge=None):
    c = dev.one("git.change")
    d = c["data"]
    f = {"host": "github", "app_installed": False, "push_time": None, "found_at": 1795525924300, "sha_found": True,
         "files_changed": d["files_changed"], "insertions": d["insertions"], "deletions": d["deletions"]}
    f.update(forge or {})
    return [{"sha": d["sha"], "project_key": c["project_key"], "observed_at": c["t"],
             "server_first_seen_at": seen if seen is not None else led_received(c), "seen_via": "device",
             "forge": f}]


def batch_received(sc, dev):
    """received_at of the batch holding an event, from the scenario's own
    ledger."""
    led = ck.ledger(dev, dev.seq0 // 3, ck.rescore_range(sc.sub), sc.extras, sc.hide)
    at = {}
    for bt, w in zip(led["batches"], led["witnessed"]):
        for r in bt["records"]:
            at[r["seq"]] = w["received_at"]
    return lambda e: at[e["seq"]]


def s62_state(sc, *, cc3=None, cc4=None):
    sc.state("CC1", "not_computable", hook_prompts=1, not_computable=1, matched=0)
    sc.state("CC2", "pass", prompts=1, with_input_and_focus=1)
    if cc3 is None:
        sc.state("CC3", "pass", commits=1, flagged=0)
    if cc4 is None:
        sc.state("CC4", "pass", commits=1, seen_out_of_order=0, pushed_before_observed=0, forge_mismatch=0)
    sc.state("CC5", "pass", devices=1, slots_credited=19, slots_credited_twice=0)
    sc.state("CC6", "pass", minutes_mismatched=0, held_s=0)
    sc.state("CC7", "pass", root_mismatches=0)
    sc.not_exercised("CC8", "SM 6.2 doesn't quote the prompt, so there is no text to commit or reveal")
    sc.state("CC9", "pass", sessions=1)
    sc.state("CC10", "pass", editor_acts=1, with_focus=1)
    sc.state("CC11", "not_computable", prompts=0)


def s_62_clean(out):
    sc = Scenario("6.2-clean", "SM 6.2, five minutes before school, on Cursor with no log reader",
                  "The base timeline with its commit's server clocks and forge lookup. CC1 is not computable: "
                  "Cursor has hooks but no v1 log reader (DESIGN 5.1), so there is no second record to compare.",
                  base=S62, projects=[REMIND], window=W62)
    dev = s62(sc)
    dev.finish()
    sc.slots = {dev.id: {W62[0] + i * ck.SLOT_MS: cr for i, cr in enumerate(SLOTS62)}}
    sc.commits = s62_commit(dev, batch_received(sc, dev))
    s62_state(sc)
    sc.notes.append("CC3: the commit's 29 + 13 lines against 41 recorded in its interval, the agent's 28 + 12 and "
                    "her 1-line fix. The heartbeat Cursor sent for the agent's edit is edit_source agent, and "
                    "counting it as well would give 81, which changes nothing here.")
    sc.notes.append("CC4: the laptop sent the commit with the batch sealed at the suspend; the forge lookup found "
                    "it at 08:12:04 local, when she pushed at school. remind-bot has no GitHub App, so the push-time "
                    "rule doesn't apply.")
    sc.build(out)


def s_62_cc3(out):
    sc = Scenario("6.2-cc3-copied-lines", "A commit with 300 lines no recorded edit explains",
                  "Before committing, she copied five files from an old project into the repo outside anything "
                  "pairtime watches. The commit has 341 changed lines; pairtime recorded 41 in its interval.",
                  base=S62, projects=[REMIND], window=W62)
    dev = s62(sc)
    c = dev.one("git.change")
    c["data"]["files_changed"] = 7
    c["data"]["insertions"] = 328
    sc.edit("The commit's --shortstat is 7 files, 328 insertions and 13 deletions: 299 copied lines on top of "
            "6.2's 29 + 13.")
    dev.finish()
    sc.slots = {dev.id: {W62[0] + i * ck.SLOT_MS: cr for i, cr in enumerate(SLOTS62)}}
    sc.commits = s62_commit(dev, batch_received(sc, dev))
    sc.fails = ["CC3"]
    s62_state(sc, cc3=False)
    sc.state("CC3", "fail", commits=1, flagged=1)
    sc.notes.append("300 of 341 lines are unexplained, more than half and more than 200, so CC3 raises "
                    "unexplained_lines. With the agent's heartbeat counted too it would be 260, still both.")
    sc.notes.append("The forge has the same 7 files and 328 + 13 lines, so CC4 passes: the commit is real, its "
                    "lines just weren't written where pairtime could see.")
    sc.build(out)


def s_62_cc3_plugin(out):
    sc = Scenario("6.2-cc3-plugin-claim", "Copied lines claimed by a plugin-posted heartbeat",
                  "As 6.2-cc3-copied-lines, and before committing she runs a script that drops one line into "
                  "pairtime's plugin inbox: an editor heartbeat for remind-bot claiming the copied files as a "
                  "300-line paste. Any process on the machine can do that (DESIGN 5.2), so the claim explains "
                  "none of the commit's lines.",
                  base=S62, projects=[REMIND], window=W62)
    dev = s62(sc)
    c = dev.one("git.change")
    c["data"]["files_changed"] = 7
    c["data"]["insertions"] = 328
    sc.edit("The commit's --shortstat is 7 files, 328 insertions and 13 deletions, as in 6.2-cc3-copied-lines.")
    fix = dev.one("editor.activity", 1795524220000)
    t = 1795524250000  # 07:44:10, 20 s before the commit
    dev.insert(dev.like(fix, t, "editor.activity",
                        {"editor": "cursor", "act": "edit", "file_hash": tl.key("f", "6.2 the copied files"),
                         "lines_changed": 300, "edit_source": "paste"}, via="plugin"))
    sc.edit("An editor.activity at 07:44:10 via plugin: an edit of remind-bot with edit_source paste and "
            "lines_changed 300. It arrives over the inbox, so the agent takes no presence sample at it.")
    dev.finish()
    sc.slots = {dev.id: {W62[0] + i * ck.SLOT_MS: cr for i, cr in enumerate(SLOTS62)}}
    sc.commits = s62_commit(dev, batch_received(sc, dev))
    sc.fails = ["CC3"]
    s62_state(sc, cc3=False)
    sc.state("CC3", "fail", commits=1, flagged=1)
    sc.notes.append("CC3 counts the lines of records from the vias D19 trusts, hook, log, otel, editor and "
                    "verified bridge records. The plugin's 300 lines are listed as ignored_lines, so 300 of the "
                    "commit's 341 lines stay unexplained and CC3 raises unexplained_lines. Counted, the claim would "
                    "explain all of them.")
    sc.notes.append("A paste isn't an anchor's kind of edit (SM 2.3), so CC10 doesn't count the record, and it "
                    "credits nothing: it opens no window, the slot it falls in already resolves to remind-bot, and "
                    "plugin records never count toward logged_s (D19). The credited slots are 6.2's.")
    sc.build(out)


def s_62_cc4_late(out):
    sc = Scenario("6.2-cc4-late", "A commit the server first saw 18 minutes after observed_at",
                  "pairtimed first heard of the commit at 08:02:30.6, from the laptop's commit record, 18 minutes "
                  "after the observed_at the record gives. The ledger batches, the commit's included, arrived on "
                  "time, so only the commit's clocks disagree.",
                  base=S62, projects=[REMIND], window=W62)
    dev = s62(sc)
    dev.finish()
    sc.slots = {dev.id: {W62[0] + i * ck.SLOT_MS: cr for i, cr in enumerate(SLOTS62)}}
    seen = 1795524270000 + 18 * 60_000 + 600
    sc.commits = s62_commit(dev, batch_received(sc, dev), seen=seen)
    sc.edit("server_first_seen_at is observed_at plus 18 minutes and 0.6 s.")
    sc.fails = ["CC4"]
    s62_state(sc, cc4=False)
    sc.state("CC4", "fail", commits=1, seen_out_of_order=1, pushed_before_observed=0, forge_mismatch=0)
    sc.build(out)


def s_62_cc4_diffstat(out):
    sc = Scenario("6.2-cc4-diffstat", "A forge diffstat that doesn't match the commit record",
                  "The laptop's commit record says 2 files, 29 insertions and 13 deletions, but GitHub's commit "
                  "has 7 files and 328 insertions: a client that understates a commit so CC3 doesn't flag it.",
                  base=S62, projects=[REMIND], window=W62)
    dev = s62(sc)
    dev.finish()
    sc.slots = {dev.id: {W62[0] + i * ck.SLOT_MS: cr for i, cr in enumerate(SLOTS62)}}
    sc.commits = s62_commit(dev, batch_received(sc, dev), forge={"files_changed": 7, "insertions": 328})
    sc.edit("The forge returns 7 files, 328 insertions and 13 deletions for the SHA.")
    sc.fails = ["CC4"]
    s62_state(sc, cc4=False)
    sc.state("CC4", "fail", commits=1, seen_out_of_order=0, pushed_before_observed=0, forge_mismatch=1)
    sc.notes.append("CC3 reads the record's --shortstat and passes. CC4 is what compares it with the forge.")
    sc.build(out)


def s63(sc):
    """6.3 with the transcript and rollout copies of A's and B's prompts."""
    dev = ck.Device(S63)
    delays = {"t_c4044fb9cf4c": 1200, "t_c66c1761dc96": 800, "t_d83dc68309f4": 1500, A_TURN2: 600,
              "t_2161b4f78872": 900}
    for h in dev.turn_prompts("hook"):
        if h["harness"] in ck.LOG_READERS:
            dev.insert(ck.transcript_copy(dev, h, delays[h["data"]["turn_key"]]))
    sc.edit("The log readers' copies of A's and B's prompts: Claude Code's transcript and Codex's rollout file, "
            "0.6 to 1.5 s after each hook. Cursor's E has no v1 log reader.")
    sc.device(dev, {"os": "macos", "input_keys": True, "typing_module": False, "vm": False})
    return dev


def s63_commit(dev):
    c = dev.one("git.change")
    d = c["data"]
    return [{"sha": d["sha"], "project_key": c["project_key"], "observed_at": c["t"],
             "server_first_seen_at": 1795904042600, "seen_via": "forge_push",
             "forge": {"host": "github", "app_installed": True, "push_time": 1795904042000, "found_at": 1795904042600,
                       "sha_found": True, "files_changed": d["files_changed"], "insertions": d["insertions"],
                       "deletions": d["deletions"]}}]


def s63_slots():
    return {W63[0] + i * ck.SLOT_MS: 15 for i in range(120)}


def s63_state(sc, *, cc5=True):
    sc.state("CC1", "pass", hook_prompts=6, log_entries=4, matched=4, not_computable=2)
    sc.state("CC2", "pass", prompts=6, with_input_and_focus=6)
    sc.state("CC3", "pass", commits=1, flagged=0)
    sc.state("CC4", "pass", commits=1, seen_out_of_order=0, pushed_before_observed=0, forge_mismatch=0)
    if cc5:
        sc.state("CC5", "pass", devices=1, slots_credited=120, slots_credited_twice=0)
    sc.state("CC6", "pass", minutes_mismatched=0, held_s=0)
    sc.state("CC7", "pass", root_mismatches=0)
    sc.not_exercised("CC8", "SM 6.3 quotes one prompt of seven, so the scenario commits and reveals no text")
    sc.state("CC9", "pass", sessions=3, multi_project_sessions=0)
    sc.state("CC10", "pass", editor_acts=10, with_focus=10)
    sc.state("CC11", "not_computable", prompts=0)


def s_63_clean(out):
    sc = Scenario("6.3-clean", "SM 6.3, the five-project afternoon",
                  "The base timeline with the log readers' copies of A's and B's prompts and E's commit clocks. "
                  "CC1 compares the Claude Code and Codex prompts; Cursor's two aren't computable.",
                  base=S63, projects=[A, B, D, E], window=W63)
    dev = s63(sc)
    dev.finish()
    sc.slots = {dev.id: s63_slots()}
    sc.commits = s63_commit(dev)
    s63_state(sc)
    sc.notes.append("CC4: rusty-cli has the GitHub App, which saw the push at 14:14:02, 2 s after the laptop "
                    "observed the commit, and that is pairtimed's first sighting of it.")
    sc.notes.append("CC11 is not computable: the Mac's input_keys comes from the key-down idle time, and the "
                    "typing module is off.")
    sc.build(out)


def s_63_cc6(out):
    sc = Scenario("6.3-cc6-moved-minute", "A live bucket that moves a minute from A to B",
                  "A modified client uploads 14:18, one of A's prompting minutes, as B's. The server's re-score of "
                  "the same records gives it to A, so the minute doesn't reproduce and is held.",
                  base=S63, projects=[A, B, D, E], window=W63)
    dev = s63(sc)
    dev.finish()
    sc.slots = {dev.id: s63_slots()}
    sc.commits = s63_commit(dev)
    minute = W63[0] + 18 * ck.MINUTE_MS
    sc.mutations = [{"device_id": dev.id, "minute": iso(minute), "op": "move_project", "from": A, "to": B,
                     "held_s": 60}]
    sc.edit("The live bucket of 14:18 credits B where the re-score credits A: every slot's p is B.")
    sc.fails = ["CC6"]
    s63_state(sc)
    sc.state("CC6", "fail", minutes_mismatched=1, held_s=60)
    sc.notes.append("Each slot still has one project, so CC5 passes on the live buckets too. 14:18 is 60 s of A's "
                    "P in SM's row, and those 60 s are held (DESIGN 6.2 D7).")
    sc.build(out)


def s_63_cc9(out):
    sc = Scenario("6.3-cc9-moved-turn", "A session that changes project",
                  "A modified client labels every record of A's second turn, from the 14:20:30 prompt to its stop, "
                  "with project D, the one Sam submits for the hand coding, to move the turn's reading into it.",
                  base=S63, projects=[A, B, D, E], window=W63)
    dev = s63(sc)
    moved = 0
    for e in dev.events:
        if e["session_key"] == "s_f9d2c9433adf" and e["t"] >= 1795904430000:
            e["project_key"] = D
            moved += 1
    check(moved == 8, f"A's second turn has 8 records, got {moved}")
    sc.edit("The 8 records of A's second turn (the hook's prompt and the transcript's copy, two outputs, three "
            "tool calls and the stop) carry project D, so session s_f9d2c9433adf names both A and D.")
    dev.finish()
    sc.commits = s63_commit(dev)
    sc.fails = ["CC9"]
    s63_state(sc, cc5=False)
    sc.state("CC5", "pass", devices=1, slots_credited_twice=0)
    sc.state("CC9", "fail", sessions=3, multi_project_sessions=1, orphan_turns=0, parent_class_changes=0)
    sc.notes.append("The live buckets are the client's score of the records as labeled, and the re-score of the "
                    "same records agrees, so CC6 passes. A session's records keep one session digest in every "
                    "header, so CC9 can see this from headers alone.")
    sc.build(out)


def s_63_cc10(out):
    sc = Scenario("6.3-cc10-injected-heartbeats", "Editor heartbeats while no editor of the project has focus",
                  "A script posts four of D's VS Code heartbeats to the local WakaTime port between 14:15 and "
                  "14:19, while Sam reads A's answer in iTerm. macOS skips the peer-process check (DESIGN 5.7), so "
                  "they arrive as via editor.",
                  base=S63, projects=[A, B, D, E], window=W63)
    dev = s63(sc)
    proto = dev.find("editor.activity")[-1]
    for t in (1795904110000, 1795904185000, 1795904260000, 1795904335000):
        dev.insert(dev.like(proto, t, "editor.activity",
                            {"editor": "vscode", "act": "edit", "file_hash": proto["data"]["file_hash"],
                             "lines_changed": 3, "edit_source": "human"}))
    sc.edit("Four editor.activity edits for D at 14:15:10, 14:16:25, 14:17:40 and 14:18:55, while iTerm's "
            "window 1, hosting A, has focus.")
    dev.finish()
    sc.slots = {dev.id: s63_slots()}
    sc.commits = s63_commit(dev)
    sc.fails = ["CC10"]
    s63_state(sc)
    sc.state("CC10", "fail", editor_acts=14, with_focus=10, without_focus=4)
    sc.notes.append("The heartbeats open no anchor, since a typed edit needs editor focus on its project (SM 2.3), "
                    "and iTerm's slots resolve through its hosted session (SM 3.2), so credit is 6.3's.")
    sc.build(out)


def s_42_cc9(out):
    sc = Scenario("4.2-cc9-parent-class", "A session whose parent class changes",
                  "At 19:40:31 a script starts a second Claude Code process inside tmux, and the client files its "
                  "SessionStart under the first session's key. The session began in Terminal, and now one of its "
                  "session.start records names a multiplexer parent.",
                  base=ALT, projects=[DASH], window=ALT_W)
    dev = alt(sc)
    st = dev.one("session.start")
    t = 1795423231200  # 19:40:31.2, between two 15 s samples
    dev.insert(dev.like(st, t, "session.start", dict(st["data"], start_kind="resume", parent_class="multiplexer")))
    # The agent takes a presence sample at every hook event (DESIGN 3.3).
    dev.insert(dev.like(dev.find("os.presence")[0], t, "os.presence",
                        {"idle_any_s": 0, "idle_keys_s": 0, "locked": False}))
    dev.presence_dirty = True
    sc.edit("A session.start at 19:40:31.2 with the session's key, start_kind resume and parent_class multiplexer, "
            "and the presence sample the agent takes at it.")
    dev.finish()
    keys = alt_features(sc, dev)
    sc.typing = alt_typing(dev)
    alt_challenge(sc, dev, keys, (A1, B1, A2, B2))
    sc.fails = ["CC9"]
    alt_clean_state(sc, cc5=False)
    sc.state("CC5", "pass", devices=1, slots_credited_twice=0)
    sc.state("CC9", "fail", sessions=1, multi_project_sessions=0, orphan_turns=0, parent_class_changes=1)
    sc.notes.append("A multiplexer isn't one of SM 2.2 rule 4's headless parents, so every prompt keeps its origin "
                    "and CC2 still counts all five.")
    sc.build(out)


def s_63_cc4_pushed(out):
    sc = Scenario("6.3-cc4-pushed-earlier", "A commit GitHub had before the laptop saw it",
                  "GitHub's push of E's commit is timestamped 14:09:00, five minutes before the laptop says it saw "
                  "the commit, so the commit was made and pushed somewhere else first. The App's webhook came late, "
                  "so pairtimed first heard of the commit from the laptop's record.",
                  base=S63, projects=[A, B, D, E], window=W63)
    dev = s63(sc)
    dev.finish()
    sc.slots = {dev.id: s63_slots()}
    seen = batch_received(sc, dev)(dev.one("git.change"))
    commits = s63_commit(dev)
    commits[0].update({"server_first_seen_at": seen, "seen_via": "device"})
    commits[0]["forge"].update({"push_time": 1795904040000 - 5 * 60_000, "found_at": seen + 2000})
    sc.commits = commits
    sc.edit("The forge's push_time for E's commit is 14:09:00. pairtimed first sees the commit in the laptop's "
            "batch sealed at 14:15:00, and the App's webhook 2 s after that.")
    sc.fails = ["CC4"]
    s63_state(sc)
    sc.state("CC4", "fail", commits=1, seen_out_of_order=0, pushed_before_observed=1, forge_mismatch=0)
    sc.notes.append("observed_at is before server_first_seen_at, which is within 15 minutes of it, and the forge's "
                    "diffstat matches. Only the push-time rule for App-installed repos fails.")
    sc.build(out)


def s_63_cc9_orphan(out):
    sc = Scenario("6.3-cc9-orphan-turn", "An agent turn with no prompt in its session",
                  "A modified client moves the agent's work on A's second turn, its outputs, tool calls and stop, "
                  "into a session it makes up for project D, so the turn's reading would count toward D. The "
                  "prompt stays in A's session, where the transcript has it.",
                  base=S63, projects=[A, B, D, E], window=W63)
    dev = s63(sc)
    fake = tl.key("s", "6.3 a session the client made up for D")
    moved = 0
    for e in dev.events:
        if e["session_key"] == "s_f9d2c9433adf" and e["t"] > 1795904431000:
            e["session_key"] = fake
            e["project_key"] = D
            moved += 1
    check(moved == 6, f"A's second turn has 6 agent records after its prompt, got {moved}")
    sc.edit(f"The 6 agent records of A's second turn (two outputs, three tool calls and the stop) carry session "
            f"{fake} and project D. Its prompt and the transcript's copy stay in A's session.")
    dev.finish()
    sc.commits = s63_commit(dev)
    sc.fails = ["CC9"]
    s63_state(sc, cc5=False)
    sc.state("CC5", "pass", devices=1, slots_credited_twice=0)
    sc.state("CC9", "fail", sessions=4, multi_project_sessions=0, orphan_turns=1, parent_class_changes=0)
    sc.notes.append("Each session names one project, so only the turn rule fails: the made-up session's outputs "
                    "and stop name a turn whose prompt is in another session.")
    sc.build(out)


SCENARIOS = [s_42_clean, s_42_cc1, s_42_cc2, s_42_cc5, s_42_cc7, s_42_cc7_batch, s_42_cc8_false, s_42_cc8_key, s_42_cc8_limit,
             s_42_cc9, s_42_cc11, s_62_clean, s_62_cc3, s_62_cc3_plugin, s_62_cc4_late, s_62_cc4_diffstat, s_63_clean, s_63_cc4_pushed,
             s_63_cc6, s_63_cc9, s_63_cc9_orphan, s_63_cc10]


def main():
    out = tl.out_dir(HERE)
    for s in SCENARIOS:
        s(out)


if __name__ == "__main__":
    main()
