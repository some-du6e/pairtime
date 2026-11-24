# SPDX-License-Identifier: AGPL-3.0-or-later
"""Two projects' Claude Code sessions on GNOME on Wayland, whose reading
buckets compete for the same seconds. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(os.path.dirname(HERE)))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402
import focus as fu  # noqa: E402

NAME = "focus-unknown/d-two-projects"
fx = tl.Fixture(
    NAME,
    title="Two projects' sessions on GNOME on Wayland, whose buckets compete",
    date="2026-11-27",
    timezone_name="America/Sao_Paulo",
    utc_offset_min=-180,
    os_name="linux",
    keys=False,
    device=tl.key("d", "focus-unknown d laptop"),
    boot_id=tl.key("b", "focus-unknown d boot"),
    boot="13:20:44.000",
    seq0=19455,
    last_input="15:56:41.900",
)
at = fx.at
A, B = tl.key("p", "focus-unknown d site"), tl.key("p", "focus-unknown d api")
SA, SB = tl.key("s", "focus-unknown d site session"), tl.key("s", "focus-unknown d api session")
ENV = {SA: dict(harness="claude_code", version="2.1.328", session=SA, project=A),
       SB: dict(harness="claude_code", version="2.1.328", session=SB, project=B)}
TURN = {n: tl.key("t", f"focus-unknown d {n}") for n in ("A1", "B1", "B2")}
FILE = {n: tl.key("f", f"focus-unknown d {n}") for n in ("site.css", "routes.py")}
rand = tl.Rand("focus-unknown d")
T0, END = at("16:00:00"), at("16:15:00")


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


# Before the window. Ana starts Claude Code in a terminal for site, which
# makes a project context live, so the agent records that focus is unknown
# (SM 2.5, DESIGN 3.3), then in a second terminal for api.
every("15:56:50.300", "15:56:58.000", 300, 600, True)
start(SA, "15:57:00")
fx.emit(at("15:57:00"), "os.focus", {"app_class": "unknown"}, source="os", via="os", sample=True)
fx.ticks("15:57:15", "16:15:15", tl.SLOT_MS)
every("15:57:31.200", "15:57:38.000", 300, 600, True)
start(SB, "15:57:40")
every("15:57:52.600", "15:58:36.000", 6_000, 12_000, False)

# 15:58:40 to 16:02:00: she reads api's routes, scrolling, then types B1,
# 120 characters, and sends it at 16:02:00.
every("15:58:43.100", "16:00:38.000", 5_000, 9_000, False)
every("16:00:40.700", "16:01:59.400", 300, 500, True)
inp(at("16:01:59.800"), True)
prompt(SB, "16:02:00", "B1", 120)
# 16:02:00 to 16:03:00: she switches to site's terminal, looks at the page's
# styles for a few seconds, and types A1, 60 characters.
every("16:02:02.900", "16:02:17.000", 3_000, 6_000, False)
every("16:02:20.600", "16:02:59.400", 300, 500, True)
inp(at("16:02:59.800"), True)
prompt(SA, "16:03:00", "A1", 60)

# The two turns. B1 runs from 16:02:00 to 16:05:30, A1 from 16:03:00 to
# 16:04:40. She watches, then reads, scrolling.
output(SB, "16:02:40", "B1", 60)
output(SA, "16:03:20", "A1", 30)
edit(SA, "16:03:40", "16:03:33.500", "site.css", 30)
edit(SB, "16:03:50", "16:03:41.200", "routes.py", 24)
output(SA, "16:04:40", "A1", 135, final=True)
stop(SA, "16:04:40", "A1")
output(SB, "16:05:30", "B1", 120, final=True)
stop(SB, "16:05:30", "B1")
every("16:03:04.400", "16:10:00.000", 5_000, 9_000, False)

# 16:10:01 to 16:13:01: B2's lead. She thinks about api's next step, still
# scrolling, then types B2, 90 characters, sent one second past the minute.
every("16:10:04.300", "16:11:58.000", 5_000, 9_000, False)
every("16:12:01.700", "16:13:00.400", 300, 500, True)
inp(at("16:13:00.800"), True)
prompt(SB, "16:13:01", "B2", 90)
output(SB, "16:13:41", "B2", 45)
output(SB, "16:14:31", "B2", 60, final=True)
stop(SB, "16:14:31", "B2")
every("16:13:05.900", "16:15:12.000", 5_000, 9_000, False)

events = fx.build()

# The credit, worked out by hand from SM 2.5 (README.md). "site" is A and
# "api" is B.
exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 2.5", evidence="C", witness="witnessed",
                  projects={A: "site", B: "api"}, start="16:00", end="16:15")
RUNS = [
    ("16:00:00", "16:02:00", "api P"),
    ("16:02:00", "16:03:00", "site P"),
    ("16:03:00", "16:03:30", "site SUP"),
    ("16:03:30", "16:03:40", "api SUP"),
    ("16:03:40", "16:04:40", "site SUP"),
    ("16:04:40", "16:08:01", "site READ"),
    ("16:08:01", "16:10:01", "api READ"),
    ("16:10:01", "16:13:01", "api P"),
    ("16:13:01", "16:13:21", "api SUP"),
    ("16:13:21", "16:13:41", "X.agent_only"),
    ("16:13:41", "16:13:56", "api SUP"),
    ("16:13:56", "16:14:31", "X.agent_only"),
    ("16:14:31", "16:14:51", "api READ"),
    ("16:14:51", "16:15:00", "X.no_anchor"),
]
TURNS = [(at("16:02:00"), at("16:05:30")), (at("16:03:00"), at("16:04:40")), (at("16:13:01"), at("16:14:31"))]
case = fu.Case(fx, exp, runs=RUNS, level="C", level_c=[("16:00:00", "16:15:00")], turns=TURNS)
rp = fu.Replay(events, T0, END)
case.check(rp)
tl.check(all(f == ("unknown", None) for f in rp.slot_focus.values()), "focus is unknown in every slot")

# The contested seconds the case is about, by the rule that settles them:
# leads of two projects (SM 3.3 rule 3), and buckets of two projects (SM
# 2.5's nearest qualifying anchor). Seconds where one project's lead beats
# the other's bucket (rule 2) aren't listed.
NAMES = exp.projects
got = {}
for tau, kinds, win, rule in rp.contested:
    if rule in ("3", "2.5"):
        got.setdefault((rule, NAMES[win]), set()).add(tau)
spans = {k: fu.spans_of(fx, v) for k, v in got.items()}
tl.check(spans == {("3", "api"): [("16:00:20", "16:02:00")],
                   ("2.5", "site"): [("16:03:00", "16:03:30"), ("16:03:40", "16:08:01")],
                   ("2.5", "api"): [("16:08:01", "16:10:01")]},
         f"the contested seconds {spans}")
contested_buckets = sum(len(v) for (rule, _), v in got.items() if rule == "2.5")
contested_leads = sum(len(v) for (rule, _), v in got.items() if rule == "3")
# What the buckets hold: site's 9 s are never read, and api's 130 s go at
# B2's reset.
tl.check(rp.peak[SA] == 225 and rp.peak[SB] == 250, f"peaks {rp.peak}")

SPLIT = "2026-11-24-sm-2.5-split-slots"
NOTES = {
    "16:00": ("3.3 rule 3, across projects where focus is unknown",
              "B1's lead from 15:58:40. A1's lead covers 16:00:20 on as well, and the lead of the next anchor at or "
              "after the second wins: B1."),
    "16:01": ("3.3 rule 3", "B1 is the next anchor until 16:02:00."),
    "16:02": ("3.3 rule 2", "A1's lead, which beats api's bucket, reset to 20 s when B1's turn starts at 16:02:00."),
    "16:03": ("2.5: buckets of different projects go to the nearest qualifying anchor",
              "A1, at 16:03:00, is nearer every second than B1 or B2, so site reads whenever its bucket has "
              "something; api reads only while site's is empty, 16:03:30 to 16:03:40. The slot from 16:03:30 "
              "credits both projects.", SPLIT),
    "16:04": ("2.5", "site reads; A1 stops at 16:04:40, so SUP becomes READ."),
    "16:05": ("2.5", "B1 stops at 16:05:30. api's bucket holds 250 s, but A1 is still the nearer anchor."),
    "16:06": ("2.5", None),
    "16:07": ("2.5", None),
    "16:08": ("2.5: the nearest anchor can come after the second",
              "From 16:08:01 B2, at 16:13:01, is nearer than A1, 300 s against 301, so api reads, with 9 s left in "
              "site's bucket. The slot from 16:08:00 credits both projects.", SPLIT),
    "16:09": ("2.5", None),
    "16:10": ("3.3 rule 2", "api reads to 16:10:01, then B2's 180 s lead beats both buckets."),
    "16:11": ("2.3", None),
    "16:12": ("2.3", None),
    "16:13": ("2.4 and 5.2",
              "B2 resets api's bucket at 16:13:01. site's 9 s can't be read: A1 is more than 600 s back. While B2 "
              "runs with nothing to read, X.agent_only."),
    "16:14": ("2.4 and 5.2", "B2 stops at 16:14:31; api reads its summary, then nothing is left: X.no_anchor."),
}
case.rows(NOTES)
lv = case.level_totals()
agent_only = sum(1 for t in rp.running if t not in rp.credit)
expected = exp.doc(
    extra_totals={**lv, "agent_only_s": agent_only, "ai_credited_s": lv["level_c_s"], "hand_credited_s": 0,
                  "contested_buckets_s": contested_buckets, "contested_leads_s": contested_leads},
    stated=[],
    approx={},
    errata=[SPLIT],
    notes=[
        "WP-FX8's (d): two Claude Code sessions in two projects on GNOME on Wayland without pairtime's extension. "
        "Every slot's focus is unknown, so both projects' windows and buckets compete for every second (SM 2.5, "
        "3.3 rule 1).",
        "Leads of the two projects go by SM 3.3 rule 3, the lead of the next anchor at or after the second. "
        "Buckets of the two projects go to the project whose qualifying human anchor is nearest the second, the "
        "earlier anchor on a tie, then the lower project_key (SM 2.5). Neither tie arises: from 16:08:01 api's "
        "B2, 600 s after A1 and 1 s past the minute, is the nearer anchor, and the second's start and its middle "
        "give the same side.",
        "contested_buckets_s counts the seconds both buckets wanted, 291 to site and 120 to api, and "
        "contested_leads_s the seconds both projects' leads wanted, all api's.",
        "Two slots credit both projects, 16:03:30 and 16:08:00, which SM 5.1's minute bucket, one project a "
        "slot, can't record. The rows follow SM 2.5 as written, second by second. If the split-slots "
        "erratum carries D-ambiguous-anchors over to slots of unknown focus, 16:03 and 16:08 change, and site "
        "gets 360 s and api 476.",
        "Every credited second is level C with focus_unknown.",
    ],
)
tl.check(expected["totals"]["credited_s"] == 836, f"credited {expected['totals']['credited_s']}")
tl.check(expected["totals"]["credited_by_project"] == {A: 351, B: 485}, f"{expected['totals']['credited_by_project']}")

UNK = ["focus_unknown"]
RULE6 = "2.2 rule 6: input before submit, and the focus test passes only because focus is unknown (2.5); "
anchors = fu.anchors_doc(fx, rp, [
    ("16:02:00", "substantive_prompt", 200, 0, "C", UNK, RULE6 + "lead 120 + 120 / 1.5 = 200 s"),
    ("16:03:00", "substantive_prompt", 160, 0, "C", UNK, RULE6 + "lead 120 + 60 / 1.5 = 160 s"),
    ("16:13:01", "substantive_prompt", 180, 0, "C", UNK, RULE6 + "lead 120 + 90 / 1.5 = 180 s"),
])
turns = fu.turns_doc(fx, rp, [
    (SA, TURN["A1"], "16:03:00", "16:04:40", "human_typed", "substantive", "2.2 rule 6 on unknown focus"),
    (SB, TURN["B1"], "16:02:00", "16:05:30", "human_typed", "substantive", "2.2 rule 6 on unknown focus"),
    (SB, TURN["B2"], "16:13:01", "16:14:31", "human_typed", "substantive", "2.2 rule 6 on unknown focus"),
])
turns.sort(key=lambda t: t["t_submit"])
prompts = {"fixture": NAME, "prompts": []}
for name, local in (("B1", "16:02:00"), ("A1", "16:03:00"), ("B2", "16:13:01")):
    e = fx.event_at("prompt.submit", at(local))
    prompts["prompts"].append({"turn_key": TURN[name], "t": e["t"], "local": fx.hhmmss(e["t"]), "text": None,
                               "len_chars": e["data"]["len_chars"],
                               "source": "invented for WP-FX8; only its typed length matters"})
slots = case.slots(rp)
split = [s["local"] for s in slots if len(s["credit"]) > 1]
tl.check(split == ["16:03:30", "16:08:00"], f"the slots that credit both projects: {split}")

fu.check_readme(os.path.join(HERE, "README.md"), expected)
fu.write(fx, tl.out_dir(HERE), expected, {"fixture": NAME, "anchors": anchors, "turns": turns}, prompts, slots)
