# SPDX-License-Identifier: AGPL-3.0-or-later
"""Two projects' Claude Code sessions on GNOME on Wayland, where a slot
after the two prompts' midpoint goes to the project whose prompt lies
ahead. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import focus as fu  # noqa: E402

NAME = "focus-unknown/f-nearest-ahead"
fx = tl.Fixture(
    NAME,
    title="Two projects' sessions on GNOME on Wayland, where the nearer prompt lies ahead",
    date="2026-11-28",
    timezone_name="Europe/Berlin",
    utc_offset_min=60,
    os_name="linux",
    keys=False,
    device=tl.key("d", "focus-unknown f laptop"),
    boot_id=tl.key("b", "focus-unknown f boot"),
    boot="13:12:09.000",
    seq0=8841,
    last_input="13:49:41.300",
)
at = fx.at
A, B = tl.key("p", "focus-unknown f notes"), tl.key("p", "focus-unknown f game")
SA, SB = tl.key("s", "focus-unknown f notes session"), tl.key("s", "focus-unknown f game session")
ENV = {SA: dict(harness="claude_code", version="2.1.328", session=SA, project=A),
       SB: dict(harness="claude_code", version="2.1.328", session=SB, project=B)}
TURN = {n: tl.key("t", f"focus-unknown f {n}") for n in ("B0", "A1", "B1")}
FILE = {n: tl.key("f", f"focus-unknown f {n}") for n in ("level.lua", "export.py", "menu.lua")}
rand = tl.Rand("focus-unknown f")
T0, END = at("13:55:00"), at("14:10:00")


def inp(t, is_key):
    tl.check(t % tl.SLOT_MS, f"input at {fx.hhmmss(t)} is off a slot boundary")
    fx.inputs.append((t, is_key))


def every(a, b, lo, hi, is_key):
    t = at(a)
    while t <= at(b):
        if t % tl.SLOT_MS == 0:
            t += 100
        inp(t, is_key)
        t += rand.between(lo, hi)


def start(s, t):
    fx.emit(at(t), "session.start",
            {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
             "permission_mode": "acceptEdits"},
            source="hook", via="hook", sample=True, **ENV[s])


def prompt(s, t, name, typed):
    fx.emit(at(t), "prompt.submit",
            {"turn_key": TURN[name], "len_chars": typed, "pasted_chars": 0, "typed_len_est": typed,
             "origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
             "low_effort": False},
            source="hook", via="hook", sample=True, **ENV[s])


def output(s, t, name, words, final=False):
    fx.emit(at(t), "agent.output",
            {"turn_key": TURN[name], "words": words, "code_lines": 0, "is_final": final, "asks_question": False,
             "offers_options": False},
            source="log", via="log", **ENV[s])


def edit(s, t, t_start, file, lines):
    added = rand.between(lines // 2, lines - 2)
    fx.emit(at(t), "agent.tool",
            {"tool_class": "edit", "files": [FILE[file]], "lines_added": added, "lines_removed": lines - added,
             "visible_output_lines": 0, "t_start": at(t_start), "t_end": at(t)},
            source="hook", via="hook", sample=True, **ENV[s])


def stop(s, t, name):
    fx.emit(at(t), "agent.stop", {"turn_key": TURN[name], "stop_reason": "end_turn", "stop_hook_active": False},
            source="hook", via="hook", sample=True, **ENV[s])


# Before the window. Lee starts Claude Code in a terminal for game, which
# makes a project context live, so the agent records that focus is unknown
# (SM 2.5, DESIGN 3.3), then in a second terminal for notes.
every("13:49:51.200", "13:49:58.000", 300, 600, True)
start(SB, "13:50:00")
fx.emit(at("13:50:00"), "os.focus", {"app_class": "unknown"}, source="os", via="os", sample=True)
fx.ticks("13:50:15", "14:10:15", tl.SLOT_MS)
for t in ("13:50:07.300", "13:50:17.600", "13:50:26.900"):  # a second terminal, and cd to notes
    inp(at(t), True)
every("13:50:31.400", "13:50:38.000", 300, 600, True)
start(SA, "13:50:40")
every("13:50:47.300", "13:53:58.000", 6_000, 12_000, False)

# 13:54 to 13:55: she types B0, 60 characters, and sends it at 13:55:00.
every("13:54:00.600", "13:54:59.400", 300, 500, True)
inp(at("13:54:59.800"), True)
prompt(SB, "13:55:00", "B0", 60)
output(SB, "13:55:30", "B0", 30)
edit(SB, "13:56:00", "13:55:51.500", "level.lua", 60)
output(SB, "13:56:30", "B0", 60, final=True)
stop(SB, "13:56:30", "B0")
# She watches game's turn and reads its edit, scrolling, until she turns to
# notes and thinks about A1, still scrolling; then she types it, 45
# characters, and sends it at 14:00:00.
every("13:55:04.300", "13:59:28.000", 5_000, 9_000, False)
every("13:59:30.700", "13:59:59.400", 300, 500, True)
inp(at("13:59:59.800"), True)
prompt(SA, "14:00:00", "A1", 45)
output(SA, "14:01:00", "A1", 45)
edit(SA, "14:02:00", "14:01:52.200", "export.py", 40)
output(SA, "14:03:00", "A1", 60, final=True)
stop(SA, "14:03:00", "A1")
# She reads both projects' output, scrolling, then thinks about B1, and
# types it, 24 characters, in its last 16 s; it goes at 14:09:01.
every("14:00:03.900", "14:08:44.000", 5_000, 9_000, False)
every("14:08:45.600", "14:09:00.400", 300, 500, True)
inp(at("14:09:00.800"), True)
prompt(SB, "14:09:01", "B1", 24)
output(SB, "14:09:20", "B1", 30)
edit(SB, "14:09:30", "14:09:24.400", "menu.lua", 24)
output(SB, "14:09:50", "B1", 30, final=True)
stop(SB, "14:09:50", "B1")
every("14:09:04.100", "14:10:12.000", 5_000, 9_000, False)

events = fx.build()

# The credit, worked out by hand from SM 2.5, and from D-split-slots where a
# slot would split between the projects (README.md). "notes" is A and
# "game" is B.
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.5", evidence="C", witness="witnessed",
                  projects={A: "notes", B: "game"}, start="13:55", end="14:10")
RUNS = [
    ("13:55:00", "13:55:20", "game SUP"),
    ("13:55:20", "13:55:30", "X.agent_only"),
    ("13:55:30", "13:55:40", "game SUP"),
    ("13:55:40", "13:56:00", "X.agent_only"),
    ("13:56:00", "13:56:30", "game SUP"),
    ("13:56:30", "13:57:30", "game READ"),
    ("13:57:30", "14:00:00", "notes P"),
    ("14:00:00", "14:00:20", "notes SUP"),
    ("14:00:20", "14:00:30", "X.agent_only"),
    ("14:00:30", "14:01:00", "game READ"),
    ("14:01:00", "14:01:15", "notes SUP"),
    ("14:01:15", "14:02:00", "game READ"),
    ("14:02:00", "14:03:00", "notes SUP"),
    ("14:03:00", "14:04:45", "notes READ"),
    ("14:04:45", "14:06:45", "game READ"),
    ("14:06:45", "14:09:01", "game P"),
    ("14:09:01", "14:09:50", "game SUP"),
    ("14:09:50", "14:10:00", "game READ"),
]
TURNS = [(at("13:55:00"), at("13:56:30")), (at("14:00:00"), at("14:03:00")), (at("14:09:01"), at("14:09:50"))]
case = fu.Case(fx, exp, runs=RUNS, level="C", level_c=[("13:55:00", "14:10:00")], turns=TURNS)
rp = fu.Replay(events, T0, END)
case.check(rp)
tl.check(all(f == ("unknown", None) for f in rp.slot_focus.values()), "focus is unknown in every slot")

# The two prompts the case turns on are 541 s apart, an odd number of
# seconds, so their midpoint, 14:04:30.5, is the middle of slot 14:04:30's
# first second.
tl.check(at("14:09:01") - at("14:00:00") == 541_000, "A1 and B1 are 541 s apart")
# The seconds both projects wanted, by what wanted them: one project's lead
# against the other's bucket (SM 3.3 rule 2), and the two projects' buckets
# (SM 2.5's nearest qualifying anchor).
NAMES = exp.projects
got = {}
for tau, kinds, win, rule in rp.contested:
    got.setdefault((rule, NAMES[win]), set()).add(tau)
spans = {k: fu.spans_of(fx, v) for k, v in got.items()}
tl.check(spans == {("2", "notes"): [("13:57:30", "14:00:00")],
                   ("2.5", "notes"): [("14:00:00", "14:00:20"), ("14:01:00", "14:01:15"), ("14:02:00", "14:04:45")],
                   ("2.5", "game"): [("14:04:45", "14:06:45"), ("14:09:01", "14:10:00")],
                   ("2", "game"): [("14:06:45", "14:09:01")]},
         f"the contested seconds {spans}")
contested_buckets = sum(len(v) for (rule, _), v in got.items() if rule == "2.5")
tl.check(contested_buckets == 20 + 15 + 165 + 120 + 59, f"contested buckets {contested_buckets}")

# The buckets' levels at 14:04:45, the first second of the first slot that
# goes to the prompt ahead: what each was filled with, less what it drained.
FILLS = {SB: 20 + 30 // 3 + 60 * 15 // 2 + 60 // 3, SA: 20 + 45 // 3 + 40 * 15 // 2 + 60 // 3}
tl.check(FILLS == {SB: 500, SA: 355}, f"fills {FILLS}")


def drained(project, before):
    return sum(1 for tau, c in rp.credit.items() if tau < at(before) and c[0] == project and c[1] in ("SUP", "READ"))


LEVEL = {"game": FILLS[SB] - drained(B, "14:04:45"), "notes": FILLS[SA] - drained(A, "14:04:45")}
tl.check(LEVEL == {"game": 305, "notes": 155}, f"bucket levels at 14:04:45 {LEVEL}")
# Each contested slot by the nearest qualifying anchor (SM 2.5) and by the
# most recent human prompt, which a look-back rule would read.
for slot in range(at("14:04:45"), at("14:06:45"), tl.SLOT_MS):
    tl.check(rp.owner[slot] == B and at("14:09:01") - slot < slot - at("14:00:00"),
             f"slot {fx.hhmmss(slot)} goes to game, whose B1 ahead is nearer than A1 behind")
tl.check(rp.owner[at("14:04:30")] == A, "slot 14:04:30 goes to notes: A1 is 270 s back and B1 271 s ahead")
# D-bucket-agent-only's per-project numbers, worked by hand in README.md.
AGENT = {f"agent_running_s.{A}": 180, f"agent_only_s.{A}": 180 - (20 + 15 + 60),
         f"agent_running_s.{B}": 90 + 49, f"agent_only_s.{B}": 90 - (20 + 10 + 30) + 49 - 49}
tl.check(fu.project_agent_time(rp, [A, B]) == AGENT, f"per-project agent time {fu.project_agent_time(rp, [A, B])}")

SPLIT = "2026-11-24-sm-2.5-split-slots"
NOTES = {
    "13:55": ("2.4 and 5.2",
              "B0 is sent at 13:55:00 and resets game's bucket to 20 s; 30 words add 10 s at 13:55:30. While B0 "
              "runs with nothing to read, X.agent_only."),
    "13:56": ("2.4", "game's 60-line edit lands at 13:56:00 and adds 450 s; B0 stops at 13:56:30 and its summary "
                     "adds 20 s."),
    "13:57": ("3.3 rule 2", "game reads to 13:57:30, where A1's lead begins and beats game's bucket."),
    "13:58": ("2.3", None),
    "13:59": ("2.3", "The typing part, 45 / 1.5 = 30 s, from 13:59:30."),
    "14:00": ("2.5 and D-split-slots: a slot of unknown focus keeps its first credited second's project",
              "A1 is sent at 14:00:00 and resets notes's bucket to 20 s. Both buckets have output, and A1 is the "
              "nearer anchor, so notes reads. The slot from 14:00:15 is notes's by its first second, so when notes's "
              "bucket runs dry at 14:00:20 game's can't take the rest: X.agent_only while A1 runs. From 14:00:30 "
              "only game's bucket has output.", SPLIT),
    "14:01": ("2.5", "notes's 45 words land at 14:01:00, 15 s, and A1 is nearer, so notes reads them; from "
                     "14:01:15 only game's bucket has output again."),
    "14:02": ("2.5", "notes's 40-line edit lands at 14:02:00 and adds 300 s; A1 is nearer than B0 or B1."),
    "14:03": ("2.5", "A1 stops at 14:03:00 and its summary adds 20 s."),
    "14:04": ("2.5 and D-split-slots: the nearest anchor can lie ahead",
              "At 14:04:30, the first second of its slot, A1 is 270 s back and B1 271 s ahead, so notes reads to "
              "14:04:45. At 14:04:45, a new slot, B1 is 256 s ahead and A1 285 s back, so game reads, with 305 s "
              "in its bucket against notes's 155.", SPLIT),
    "14:05": ("2.5", "B1 ahead is the nearer anchor."),
    "14:06": ("2.5 and 3.3 rule 2", "game reads to 14:06:45, where B1's lead begins and beats notes's bucket."),
    "14:07": ("2.3", None),
    "14:08": ("2.3", "The typing part, 24 / 1.5 = 16 s, from 14:08:45."),
    "14:09": ("2.4 and 2.5", "B1 is sent at 14:09:01 and resets game's bucket to 20 s; 30 words add 10 s at "
                             "14:09:20 and a 24-line edit 180 s at 14:09:30. B1 is the nearer anchor."),
}
case.rows(NOTES)
lv = case.level_totals()
agent_only = sum(1 for t in rp.running if t not in rp.credit)
expected = exp.doc(
    extra_totals={**lv, "agent_only_s": agent_only, **AGENT, "ai_credited_s": lv["level_c_s"],
                  "hand_credited_s": 0, "contested_buckets_s": contested_buckets},
    stated=[],
    approx={},
    errata=[SPLIT],
    notes=[
        "WP-FX8's (f), the case d-two-projects/README.md says it lacks (\"a slot starts while both buckets still "
        "hold output after the midpoint\"), for WP-SC8 (#377): two Claude Code sessions in two projects on GNOME "
        "on Wayland without pairtime's extension, so every slot's focus is unknown and both projects' windows "
        "and buckets compete for every second (SM 2.5, 3.3 rule 1).",
        "Buckets of the two projects go to the project whose qualifying human anchor is nearest the second, the "
        "earlier anchor on a tie, then the lower project_key (SM 2.5), and D-split-slots gives a slot the project "
        "of its first credited second. From 14:04:45 to 14:06:45 both buckets hold output, and B1, sent at "
        "14:09:01, is nearer than A1, sent at 14:00:00, so the eight slots go to game, whose prompt lies ahead. "
        "A1 and B1 are 541 s apart, an odd number of seconds: measured from a second's middle, 14:04:30 is "
        "270.5 s from both, and the earlier anchor, A1, takes it, so both readings of nearest give the same side.",
        "Read by the most recent human prompt instead, as a look-back rule would, those eight slots go to notes, "
        "whose A1 is the most recent prompt: notes would get 470 s and game 390, not 350 and 510.",
        "contested_buckets_s counts the seconds both buckets wanted: 200 to notes and 179 to game.",
        "agent_only_s, 40, is SM 5.3's count across projects. agent_running_s.<p> and agent_only_s.<p> are "
        "D-bucket-agent-only's per-project numbers.",
        "Every credited second is level C with focus_unknown.",
    ],
)
tl.check(expected["totals"]["credited_s"] == 860, f"credited {expected['totals']['credited_s']}")
tl.check(expected["totals"]["credited_by_project"] == {A: 350, B: 510}, f"{expected['totals']['credited_by_project']}")

UNK = ["focus_unknown"]
RULE6 = "2.2 rule 6: input before submit, and the focus test passes only because focus is unknown (2.5); "
anchors = fu.anchors_doc(fx, rp, [
    ("13:55:00", "substantive_prompt", 160, 0, "C", UNK, RULE6 + "lead 120 + 60 / 1.5 = 160 s"),
    ("14:00:00", "substantive_prompt", 150, 0, "C", UNK, RULE6 + "lead 120 + 45 / 1.5 = 150 s"),
    ("14:09:01", "substantive_prompt", 136, 0, "C", UNK, RULE6 + "lead 120 + 24 / 1.5 = 136 s"),
])
turns = fu.turns_doc(fx, rp, [
    (SB, TURN["B0"], "13:55:00", "13:56:30", "human_typed", "substantive", "2.2 rule 6 on unknown focus"),
    (SA, TURN["A1"], "14:00:00", "14:03:00", "human_typed", "substantive", "2.2 rule 6 on unknown focus"),
    (SB, TURN["B1"], "14:09:01", "14:09:50", "human_typed", "substantive", "2.2 rule 6 on unknown focus"),
])
turns.sort(key=lambda t: t["t_submit"])
prompts = {"fixture": NAME, "prompts": []}
for name, local in (("B0", "13:55:00"), ("A1", "14:00:00"), ("B1", "14:09:01")):
    e = fx.event_at("prompt.submit", at(local))
    prompts["prompts"].append({"turn_key": TURN[name], "t": e["t"], "local": fx.hhmmss(e["t"]), "text": None,
                               "len_chars": e["data"]["len_chars"],
                               "source": "invented for WP-FX8; only its typed length matters"})
slots = case.slots(rp)
owners = {s["local"]: (NAMES[s["p"]], s["credited_s"]) for s in slots if s["credited_s"]}
tl.check([owners[k] for k in ("14:00:15", "14:04:30", "14:04:45", "14:06:30")]
         == [("notes", 5), ("notes", 15), ("game", 15), ("game", 15)], f"slot owners {owners}")

fu.check_readme(os.path.join(HERE, "README.md"), expected)
fu.write(fx, tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns}, prompts, slots)
