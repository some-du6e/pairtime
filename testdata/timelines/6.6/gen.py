# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.6, a scripted claude -p loop pretending to be a person. See
README.md in this directory."""

import math
import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402

ERRATUM = "2026-11-19-sm-6.6-uncredited-codes"

fx = tl.Fixture(
    "6.6",
    title="A scripted claude -p loop pretending to be a person",
    date="2026-11-21",
    timezone_name="Asia/Kolkata",
    utc_offset_min=330,
    os_name="windows",
    keys=False,
    device=tl.key("d", "6.6 casey laptop"),
    boot_id=tl.key("b", "6.6 boot"),
    boot="18:41:07.000",
    seq0=61028,
    # The jiggler's move before the fixture starts.
    last_input="21:59:32.900",
)

at = fx.at
P = tl.key("p", "6.6 project")
APP = tl.key("a", "6.6 WindowsTerminal.exe")
CMD_TEST = tl.key("c", "6.6 test command")
CMD_COMMIT = tl.key("c", "6.6 git commit")
FILES = [tl.key("f", f"6.6 file {i}") for i in range(1, 7)]
FIRST_PROMPT = "the leaderboard sorts ties wrong, make it stable and add a test"
END = at("+1 02:00:00")

# Input: Casey clicks the terminal and starts the script, then only the
# USB jiggler moves the pointer, every 12 s all night. Windows without the
# key counter can't tell the two apart.
fx.pointer("21:59:49.700")
fx.typing("21:59:51.000", "21:59:59.600", 700)  # "bash farm.sh", Enter
fx.pointer_every("21:59:44.900", "+1 01:59:56.900", 12_000)
fx.ticks("22:00:00", "+1 02:00:00", tl.SLOT_MS)

fx.emit(at("21:59:50"), "os.focus", {"app_class": "terminal", "app_hash": APP}, source="os", via="os", sample=True)


def schedule(seed):
    """The loop's iterations: (claude start, claude stop) pairs. The first
    two are SM's: claude starts at 22:00:05 and stops at 22:02:10, the
    script sleeps 167 s, and the next iteration starts at 22:04:57 with
    claude 5 s later, after gen_prompt.py. After that gen_prompt.py takes 3
    to 8 s, claude runs 70 to 160 s, and the script sleeps $RANDOM % 240 +
    60 s."""
    rand = tl.Rand(seed)
    runs = [(at("22:00:05"), at("22:02:10"))]
    loop = at("22:04:57")
    gen = 5_000
    while True:
        start = loop + gen
        stop = start + rand.between(70, 160) * 1000
        if stop >= END:
            break
        runs.append((start, stop))
        loop = stop + rand.between(60, 299) * 1000
        if loop >= END:
            break
        gen = rand.between(3, 8) * 1000
    return runs


def spacing_stats(runs):
    gaps = [(b[0] - a[0]) / 1000 for a, b in zip(runs, runs[1:])]
    mean = sum(gaps) / len(gaps)
    sd = math.sqrt(sum((g - mean) ** 2 for g in gaps) / len(gaps))
    return mean, sd, (sd - mean) / (sd + mean)


def acceptable(runs):
    """SM's numbers: 49 iterations (one at 22:00, the next at 22:04:57 and 47
    more from 22:06 to 01:59), prompt spacing with a mean near 300 s and a
    standard deviation near 75 s, burstiness about -0.6."""
    if len(runs) != 49 or runs[1][0] != at("22:05:02") or runs[2][0] < at("22:06:00"):
        return False
    mean, sd, b = spacing_stats(runs)
    return abs(mean - 300) <= 10 and abs(sd - 75) <= 8 and abs(b + 0.6) <= 0.04


seed = next(f"6.6 loop {k}" for k in range(1000) if acceptable(schedule(f"6.6 loop {k}")))
runs = schedule(seed)
mean, sd, burst = spacing_stats(runs)

# Each iteration: SessionStart and UserPromptSubmit as claude -p starts,
# with the agent re-reading the focused window, which now hosts the
# session; tool calls and transcript text; the final message and Stop;
# SessionEnd as claude exits, and the window hosts nothing again. Commits
# once an hour, in the first iteration that starts after each :50.
rand = tl.Rand(seed + " work")
commit_after = [at("22:50"), at("23:50"), at("+1 00:50"), at("+1 01:50")]
commit_runs = {next(i for i, r in enumerate(runs) if r[0] >= c) for c in commit_after}
turns = []
prompts = []
for i, (start, stop) in enumerate(runs, 1):
    s = tl.key("s", f"6.6 session {i}")
    k = tl.key("t", f"6.6 turn {i}")
    cc = dict(harness="claude_code", version="2.1.328", session=s, project=P)
    length = len(FIRST_PROMPT) if i == 1 else rand.between(45, 140)
    fx.emit(start, "session.start",
            {"start_kind": "startup", "entrypoint": "sdk", "parent_class": "script_runtime", "has_tty": True,
             "permission_mode": "acceptEdits"},
            source="hook", via="hook", sample=True, **cc)
    fx.emit(start, "os.focus", {"app_class": "terminal", "app_hash": APP, "hosted_sessions": [s], "focus_project": P},
            source="os", via="os", sample=True)
    fx.emit(start, "prompt.submit",
            {"turn_key": k, "len_chars": length, "pasted_chars": 0, "typed_len_est": length,
             "origin_declared": "none", "matches_pending_cron": False, "answers_question": False, "low_effort": False},
            source="hook", via="hook", sample=True, **cc)
    # Tool calls every 15 to 45 s until 10 s before the stop: a read, then
    # edits, then a test run, and a commit in the hourly iterations.
    t = start + rand.between(8, 15) * 1000
    plan = ["read", "edit", "edit", "test", "edit", "test", "edit", "test"]
    # SM's 22:01 row: in the first iteration the agent edits two files in
    # that minute.
    fixed_times = [at("22:00:14"), at("22:01:05"), at("22:01:38"), at("22:01:58")] if i == 1 else None
    n = 0
    while (n < len(fixed_times)) if fixed_times else (t < stop - 10_000):
        if fixed_times:
            t = fixed_times[n]
        what = plan[n % len(plan)]
        if what == "read":
            data = {"tool_class": "read", "files": [FILES[rand.below(len(FILES))]], "lines_added": 0, "lines_removed": 0,
                    "visible_output_lines": 0, "t_start": t - 500, "t_end": t}
        elif what == "edit":
            lines = rand.between(4, 30)
            removed = rand.between(0, lines // 2)
            f = FILES[n] if fixed_times else FILES[rand.below(len(FILES))]
            data = {"tool_class": "edit", "files": [f], "lines_added": lines - removed,
                    "lines_removed": removed, "visible_output_lines": 0, "t_start": t - 300, "t_end": t}
        else:
            data = {"tool_class": "test", "lines_added": 0, "lines_removed": 0, "cmd_hash": CMD_TEST,
                    "exit_code": rand.below(2), "visible_output_lines": 4, "t_start": t - 6_000, "t_end": t}
        fx.emit(t, "agent.tool", data, source="hook", via="hook", sample=True, **cc)
        if n == 1:
            fx.emit(t + 2_000, "agent.output",
                    {"turn_key": k, "words": rand.between(15, 60), "code_lines": 0, "is_final": False,
                     "asks_question": False, "offers_options": False},
                    source="log", via="log", **cc)
        n += 1
        t += rand.between(15, 45) * 1000
    if i - 1 in commit_runs:
        c = stop - 6_000
        fx.emit(c, "agent.tool",
                {"tool_class": "bash", "lines_added": 0, "lines_removed": 0, "cmd_hash": CMD_COMMIT, "exit_code": 0,
                 "visible_output_lines": 1, "t_start": c - 900, "t_end": c},
                source="hook", via="hook", sample=True, **cc)
        m = sorted(commit_runs).index(i - 1)
        fx.emit(c + 300, "git.change",
                {"op": "commit", "sha": tl.git_sha(f"6.6 commit {m}"),
                 "parents": [tl.git_sha(f"6.6 commit {m - 1}" if m else "6.6 base")],
                 "files_changed": 2, "insertions": 14 + m, "deletions": 3, "actor": "agent", "agent_trailer": "claude"},
                source="git", via="git", project=P)
    fx.emit(stop - 400, "agent.output",
            {"turn_key": k, "words": rand.between(60, 200), "code_lines": 0, "is_final": True, "asks_question": False,
             "offers_options": False},
            source="log", via="log", **cc)
    fx.emit(stop, "agent.stop", {"turn_key": k, "stop_reason": "end_turn", "stop_hook_active": False},
            source="hook", via="hook", sample=True, **cc)
    fx.emit(stop + 300, "session.end", {"reason": "other"}, source="hook", via="hook", sample=True, **cc)
    fx.emit(stop + 300, "os.focus", {"app_class": "terminal", "app_hash": APP}, source="os", via="os", sample=True)
    turns.append(tl.turn(fx, start, stop, session=s, turn_key=k, origin="headless",
                         sm="22:00: entrypoint sdk, parent_class script_runtime, so origin headless (SM 2.2 rule 4)"))
    prompts.append({"turn_key": k, "t": start, "local": fx.hhmmss(start), "text": FIRST_PROMPT if i == 1 else None,
                    "len_chars": length,
                    "source": "quoted in SM 6.6's 22:00 row" if i == 1 else
                    "SM: an LLM writes a varied, plausible prompt; the fixture chose the length"})

events = fx.build()

# Checks.
tl.check(len(commit_runs) == 4, "a commit each hour")
tl.check(all(t - fx.last_input_before(t) < 12_100 for t in range(at("22:00:00"), END, tl.SLOT_MS)),
         "the jiggler moves at least once in every slot")
tl.check(not any(k for t, k in fx.inputs if t >= at("22:00:00")), "no key press after the script starts")
agent_times = [e["t"] for e in events if e["kind"] in ("prompt.submit", "agent.tool", "agent.stop")]
replay = tl.hackatime_replay(agent_times)
tl.check(abs(replay - 11_400) <= 600, f"Hackatime replay {replay} s is about 3 h 10 min")
running = sum(b - a for a, b in runs) // 1000


def running_in(a, b):
    return sum(max(0, min(b, r1) - max(a, r0)) for r0, r1 in runs) // 1000


minutes = range(at("22:06"), END, tl.MINUTE_MS)
bucket_minutes = sum(1 for m in minutes if running_in(m, m + tl.MINUTE_MS) > 0)

exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 6.6", evidence="B", witness="witnessed",
                  projects={P: "Casey's project, which SM doesn't name"}, start="22:00", end="+1 02:00")
exp.row("22:00", codes=["X.automated"], agent=running_in(at("22:00"), at("22:01")), sm="22:00", derived=["agent_running_s"],
        erratum=ERRATUM)
exp.row("22:01", codes=["X.agent_only"], agent=running_in(at("22:01"), at("22:02")), sm="22:01",
        derived=["agent_running_s"], erratum=ERRATUM)
exp.row("22:02", codes=["X.agent_only", "X.no_anchor"], agent=running_in(at("22:02"), at("22:03")), sm="22:02",
        derived=["agent_running_s"], erratum=ERRATUM)
exp.row("22:03", "22:05", bucket=False, codes=["X.no_anchor"], sm="22:03 to 22:04", derived=["bucket"], erratum=ERRATUM,
        note="Nothing is credited and no agent runs while the script sleeps, so these minutes have no bucket (SM 5.1).")
exp.row("22:05", codes=["X.automated"], agent=running_in(at("22:05"), at("22:06")), sm="22:05",
        derived=["agent_running_s"], erratum=ERRATUM)
exp.row("22:06", "+1 02:00", codes=["X.automated", "X.agent_only", "X.no_anchor"], agent=running_in(at("22:06"), END),
        bucket_minutes=bucket_minutes, sm="22:06 to 01:59", derived=["agent_running_s", "bucket_minutes"],
        erratum=ERRATUM,
        note="47 more iterations. Minutes in which the script only sleeps have no bucket.")
tl.check(exp.rows[0]["agent_running_s"] == 55 and exp.rows[2]["agent_running_s"] == 10, "SM's first iteration")

expected = exp.doc(
    extra_totals={"credited_s": 0, "agent_only_s": running},
    stated=["credited_s"],
    approx={
        "ai_logged_s": {"about": 11400, "sm": "Hackatime would log about 3 h 10 min of ai coding"},
        "handbook_s": {"about": 3840, "sm": "the flat third would pay about 64 minutes"},
        "prompt_spacing_mean_s": {"about": 300, "sm": "the prompt spacing has a mean near 300 s"},
        "prompt_spacing_sd_s": {"about": 75, "sm": "and a standard deviation near 75 s"},
        "burstiness": {"about": -0.6, "sm": "so burstiness B is about -0.6, past the -0.5 line"},
    },
    errata=[ERRATUM],
    notes=[
        "SM gives each minute's codes but not their seconds, and its rules don't decide the split; see the "
        "erratum. The rows pin what SM does decide: nothing is credited, which minutes have a bucket and how "
        "long the agent runs in each.",
        "evidence is the machine's level, B: Windows without the key counter. Nothing is credited, because "
        "headless prompts never count on such a machine (SM 2.2 rule 4).",
        f"agent_only_s is all {running} s the agent ran, since nothing is credited.",
        f"This fixture's 49 prompts are spaced {mean:.1f} s apart on average with a standard deviation of "
        f"{sd:.1f} s, so B is {burst:.2f}; its agent events replay to {replay:g} s under min(gap, 120 s).",
    ],
)

anchors = {"fixture": "6.6", "anchors": [], "turns": turns}
prompts_doc = {"fixture": "6.6", "prompts": prompts}

fx.write(tl.out_dir(HERE), expected, anchors, prompts_doc)
