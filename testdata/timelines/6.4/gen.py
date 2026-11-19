# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.4, a /goal agent running unattended overnight. See README.md in
this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402

ERRATUM = "2026-11-19-sm-6.4-prompt-lengths"

fx = tl.Fixture(
    "6.4",
    title="A /goal agent running unattended overnight",
    date="2026-11-20",
    timezone_name="Europe/Berlin",
    utc_offset_min=60,
    os_name="linux",
    keys=True,
    device=tl.key("d", "6.4 jordan desktop"),
    boot_id=tl.key("b", "6.4 boot"),
    boot="17:12:40.000",
    seq0=88214,
    # He was in the browser before the fixture starts.
    last_input="23:29:46.200",
    last_key="23:27:13.500",
)

at = fx.at
P = tl.key("p", "pixel-forge")
S = tl.key("s", "6.4 claude session")
TURN = [None] + [tl.key("t", f"6.4 turn {i}") for i in range(1, 11)]
FILES = [tl.key("f", f"6.4 file {i}") for i in range(1, 9)]
APP = tl.key("a", "6.4 gnome-terminal")
CC = dict(harness="claude_code", version="2.1.328", session=S, project=P)
CMD_TEST = tl.key("c", "6.4 npm test")
CMD_LIGHTHOUSE = tl.key("c", "6.4 lighthouse")
CMD_COMMIT = tl.key("c", "6.4 git commit")
ERRORS = [tl.key("e", f"6.4 error {i}") for i in range(1, 4)]

# The turns: T1 is Jordan's /goal prompt, T2 to T9 are stop-hook
# continuations, each starting when the one before stops. Claude Code caps
# stop-hook continuations at 8 in a row (HS 3.1), so the night has nine
# turns, the last stopping at 06:10:00 after 23,400 s.
RUN_START, RUN_END = at("23:40:00"), at("+1 06:10:00")
STOPS = [RUN_START + k * 2_600_000 for k in range(1, 10)]
tl.check(STOPS[-1] == RUN_END and RUN_END - RUN_START == 23_400_000, "nine turns, 23,400 s")

# Input. X11 with the key counter, so key presses and pointer moves differ.
fx.pointer("23:29:49.800")                        # clicks the terminal
fx.typing("23:29:51.000", "23:29:55.200", 600)    # types "claude", Enter
fx.pointer("23:30:03.200")                        # then leaves
fx.pointer("23:37:00.400")                        # sits down
fx.pointer("23:37:12.700", "23:37:26.100", "23:37:39.500", "23:37:52.800")  # looks at the repo
fx.typing("23:38:00.400", "23:39:59.800", 1500)   # types the /goal prompt, sends at 23:40:00
fx.pointer_every("23:40:08.300", "23:43:08.300", 10_000)  # watches the plan and first edits
fx.pointer("23:43:10.000")                        # last touch, then bed
fx.typing("+1 07:29:52.000", "+1 07:29:59.400", 600)  # the password on the lock screen
fx.pointer_every("+1 07:30:03.500", "+1 07:35:27.500", 8_000)  # reads and scrolls the diffs
fx.typing("+1 07:35:30.400", "+1 07:36:59.800", 1400)  # types the follow-up, sends at 07:37:00
fx.lock("23:53:10", "+1 07:30:00")

# Presence samples: every 15 s while a project context is live; once a
# minute after 600 s with no input and no running turn (DESIGN 3.3), which
# is 06:10 to 07:30; and one at each hook and power event.
fx.ticks("23:30:00", "+1 06:10:00", tl.SLOT_MS)
fx.ticks("+1 06:11:00", "+1 07:29:00", tl.MINUTE_MS)
fx.ticks("+1 07:30:15", "+1 07:37:00", tl.SLOT_MS)

# He switches to the terminal and starts Claude Code.
fx.emit(at("23:29:50"), "os.focus", {"app_class": "terminal", "app_hash": APP}, source="os", via="os", sample=True)
fx.emit(at("23:29:58"), "session.start",
        {"start_kind": "startup", "entrypoint": "cli", "parent_class": "terminal_app", "has_tty": True,
         "permission_mode": "auto"},
        source="hook", via="hook", sample=True, **CC)
fx.emit(at("23:29:58"), "os.focus",
        {"app_class": "terminal", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)


def prompt(t, turn, length, slash=None):
    data = {"turn_key": turn, "len_chars": length, "pasted_chars": 0, "typed_len_est": length}
    if slash:
        data["slash_command"] = slash
    data.update({"origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
                 "low_effort": False})
    return fx.emit(t, "prompt.submit", data, source="hook", via="hook", sample=True, **CC)


def output(t, turn, words, final=False):
    # Assistant text comes from the transcript tailer.
    return fx.emit(t, "agent.output",
                   {"turn_key": turn, "words": words, "code_lines": 0, "is_final": final,
                    "asks_question": False, "offers_options": False},
                   source="log", via="log", **CC)


def tool(t_end, cls, *, start_ms=1_000, files=None, added=0, removed=0, cmd=None, exit_code=None, error=None,
         visible=0):
    data = {"tool_class": cls}
    if files:
        data["files"] = files
    data["lines_added"] = added
    data["lines_removed"] = removed
    if cmd:
        data["cmd_hash"] = cmd
    if exit_code is not None:
        data["exit_code"] = exit_code
    if error:
        data["error_sig"] = error
    data["visible_output_lines"] = visible
    data["t_start"] = t_end - start_ms
    data["t_end"] = t_end
    return fx.emit(t_end, "agent.tool", data, source="hook", via="hook", sample=True, **CC)


def stop(t, turn, hook_active):
    return fx.emit(t, "agent.stop", {"turn_key": turn, "stop_reason": "end_turn", "stop_hook_active": hook_active},
                   source="hook", via="hook", sample=True, **CC)


GREEN = at("+1 03:12:00")


def test_run(t_end):
    if t_end < GREEN:
        k = 0 if t_end < at("+1 01:00:00") else 1 if t_end < at("+1 02:20:00") else 2
        return tool(t_end, "test", start_ms=18_000, cmd=CMD_TEST, exit_code=1, error=ERRORS[k], visible=4)
    return tool(t_end, "test", start_ms=18_000, cmd=CMD_TEST, exit_code=0, visible=4)


# 23:40:00, the /goal prompt (75 characters by SM's count; see the erratum).
prompt(at("23:40:00"), TURN[1], 75, slash="/goal")
# The first four minutes, which he watches: the plan, a read, two edits and
# two failing test runs fill the bucket faster than he reads.
output(at("23:40:12"), TURN[1], 150)
tool(at("23:40:31"), "read", start_ms=800, files=[FILES[0]])
tool(at("23:40:50"), "edit", start_ms=400, files=[FILES[0]], added=12, removed=4)
test_run(at("23:41:35"))
tool(at("23:42:10"), "edit", start_ms=400, files=[FILES[1]], added=9, removed=3)
output(at("23:42:58"), TURN[1], 40)
test_run(at("23:43:30"))
fx.emit(at("23:53:10"), "os.power", {"op": "lock"}, source="os", via="os", sample=True)

# The rest of the night. Fixed points: the turn stops, 31 commits 750 s
# apart from 23:52:00 to 06:07:00, the first green test run at 03:12:00 and
# the final summary at 06:09:55. Between them the agent reads, edits, runs
# the tests and writes short messages, one tool call or message every 55 to
# 110 s, so no gap between agent events passes Hackatime's 120 s.
COMMITS = [at("23:52:00") + k * 750_000 for k in range(31)]
tl.check(COMMITS[-1] == at("+1 06:07:00"), "last commit at 06:07:00")
fixed = [(t, "commit") for t in COMMITS] + [(t, "stop") for t in STOPS] + [(GREEN, "green")]
fixed.append((at("+1 06:09:55"), "final"))
fixed.sort()
rand = tl.Rand("6.4 night")
cycle = ["read", "edit", "test", "edit", "edit", "test"]
step = 0
turn_no = 1
prev = at("23:43:30")
green_seen = False
for t_fixed, what in fixed:
    # Steps of at most 110.9 s while more than 115 s remain, so the last gap
    # before the fixed point is under 120 s and no step passes it.
    while t_fixed - prev > 115_000:
        prev += rand.between(55, 110) * 1000 + rand.below(10) * 100
        kind = cycle[step % len(cycle)]
        step += 1
        turn = TURN[turn_no]
        if kind == "read":
            tool(prev, "read", start_ms=600, files=[FILES[rand.below(len(FILES))]])
        elif kind == "edit":
            lines = rand.between(6, 24)
            removed = rand.between(0, lines // 2)
            tool(prev, "edit", start_ms=400, files=[FILES[rand.below(len(FILES))]], added=lines - removed,
                 removed=removed)
        elif prev >= GREEN and step % 2:
            tool(prev, "bash", start_ms=25_000, cmd=CMD_LIGHTHOUSE, exit_code=0, visible=4)
        else:
            test_run(prev)
        if step % 3 == 0:
            # A short message a few seconds after every third tool call.
            output(prev + rand.between(3, 8) * 1000, turn, rand.between(20, 90))
    turn = TURN[turn_no]
    if what == "commit":
        tool(t_fixed, "bash", start_ms=900, cmd=CMD_COMMIT, exit_code=0, visible=1)
        n = COMMITS.index(t_fixed)
        fx.emit(t_fixed + 300, "git.change",
                {"op": "commit", "sha": tl.git_sha(f"6.4 commit {n}"),
                 "parents": [tl.git_sha(f"6.4 commit {n - 1}" if n else "6.4 base")],
                 "files_changed": 1 + n % 3, "insertions": 8 + 3 * (n % 7), "deletions": 2 + n % 5,
                 "actor": "agent", "agent_trailer": "claude"},
                source="git", via="git", project=P)
    elif what == "green":
        test_run(t_fixed)
        green_seen = True
    elif what == "final":
        output(t_fixed, turn, 240, final=True)
    else:  # a turn's stop: its closing message, then the stop hook
        if t_fixed != RUN_END:
            output(t_fixed - 4_000, turn, rand.between(60, 120), final=True)
        stop(t_fixed, turn, hook_active=turn_no > 1)
        turn_no += 1
    prev = t_fixed
tl.check(green_seen and turn_no == 10, "nine stops and a green run")

# 07:30:00, he unlocks and reads; 07:37:00, the follow-up (61 characters by
# SM's count; see the erratum).
fx.emit(at("+1 07:30:00"), "os.power", {"op": "unlock"}, source="os", via="os", sample=True)
prompt(at("+1 07:37:00"), TURN[10], 61)

events = fx.build()


def slot_inputs(start_ms):
    return [k for t, k in fx.inputs if start_ms <= t < start_ms + tl.SLOT_MS]


# Checks of the timing choices against SM's rows (README.md).
tl.check(not slot_inputs(at("23:36:45")) and slot_inputs(at("23:37:00")), "he sits down at 23:37:00")
for s in range(at("23:37:00"), at("23:43:15"), tl.SLOT_MS):
    tl.check(slot_inputs(s), f"input in slot {fx.hhmmss(s)}")
for s in range(at("23:39:00"), at("23:40:00"), tl.SLOT_MS):
    tl.check(any(slot_inputs(s)), f"key presses in slot {fx.hhmmss(s)}, the typing part of the first lead")
for s in range(at("+1 07:36:15"), at("+1 07:37:00"), tl.SLOT_MS):
    tl.check(any(slot_inputs(s)), f"key presses in slot {fx.hhmmss(s)}, the typing part of the second lead")
for s in range(at("+1 07:30:00"), at("+1 07:37:00"), tl.SLOT_MS):
    tl.check(slot_inputs(s), f"input in slot {fx.hhmmss(s)}")
tl.check(fx.last_input_before(at("+1 07:29:50")) == at("23:43:10"), "no input from 23:43:10 to the password")
tl.check(at("23:53:10") - at("23:43:10") == 600_000, "the screen locks 600 s after his last touch")

# The bucket: reset to its 20 s base at 23:40:00, never empty while he
# watches until 23:43:40, holding only output from after 05:30:00 at
# 07:30:00, and never empty while he reads until 07:34:19.
bucket = tl.Bucket()
fills = [(e["t"], bucket.amount(e["kind"], e["data"])) for e in events if e["kind"] in ("agent.output", "agent.tool")]
drains = [(at("23:40:00"), at("23:43:40")), (at("+1 07:30:00"), at("+1 07:34:19"))]
bucket.reset(at("23:40:00"), tl.Bucket.BASE_S)
i = 0
for a, b in drains:
    for tau in range(a, b, 1000):
        while i < len(fills) and fills[i][0] <= tau:
            bucket.fill(*fills[i])
            i += 1
        bucket.expire(tau)
        tl.check(bucket.drain(1) == 1, f"the bucket has something to read at {fx.hhmmss(tau)}")
    if a == at("23:40:00"):
        while i < len(fills):
            bucket.fill(*fills[i])
            i += 1
        tl.check(bucket.total() == 600, "the night fills the bucket to its 600 s cap")
        tl.check(min(e[0] for e in bucket.entries) > at("+1 05:34:19"),
                 "at 07:30 the bucket holds only output newer than 05:34:19, so none expires while he reads")
left = bucket.total()

# Agent events as Hackatime's ai coding sees them (SM 5.4): every prompt,
# tool call and stop. The run never pauses 120 s, so the replay is the
# 23,400 s run plus the 120 s before the morning prompt.
agent_times = [e["t"] for e in events if e["kind"] in ("prompt.submit", "agent.tool", "agent.stop")]
gaps = [b - a for a, b in zip(agent_times, agent_times[1:]) if RUN_START <= a < RUN_END]
tl.check(max(gaps) <= 120_000, f"longest gap between agent events overnight is {max(gaps)} ms")
replay = tl.hackatime_replay(agent_times)
tl.check(abs(replay - 23_500) <= 100, f"Hackatime replay {replay} s is about 23,500 s")
tl.check(sum(1 for e in events if e["kind"] == "git.change") == 31, "31 commits")

goal = fx.event_at("prompt.submit", at("23:40:00"))
morning = fx.event_at("prompt.submit", at("+1 07:37:00"))

exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 6.4", evidence="A", witness="witnessed",
                  projects={P: "pixel-forge"}, start="23:37:00", end="+1 07:37:00")
exp.row("23:37", credit={P: {"P": 50}}, uncredited={"X.no_anchor": 10}, sm="23:37", erratum=ERRATUM)
exp.row("23:38", credit={P: {"P": 60}}, uncredited={}, sm="23:38")
exp.row("23:39", credit={P: {"P": 60}}, uncredited={}, sm="23:39")
exp.row("23:40", credit={P: {"SUP": 60}}, uncredited={}, agent=60, sm="23:40")
exp.row("23:41", credit={P: {"SUP": 60}}, uncredited={}, agent=60, sm="23:41")
exp.row("23:42", credit={P: {"SUP": 60}}, uncredited={}, agent=60, sm="23:42")
exp.row("23:43", credit={P: {"SUP": 40}}, uncredited={"X.absent": 20}, agent=60, sm="23:43")
exp.row("23:44", "+1 06:10", credit={}, uncredited={"X.absent": 386 * 60}, agent=386 * 60, sm="23:44 to 07:29",
        derived=["uncredited", "agent_running_s"],
        note="The agent runs alone until 06:10:00; every second is X.absent, its turns are X.automated "
             "and its time is agent-only.")
exp.row("+1 06:10", "+1 07:30", bucket=False, codes=["X.absent"], sm="23:44 to 07:29", derived=["bucket"],
        note="The agent has finished and nothing is credited, so these minutes have no bucket (SM 5.1).")
exp.row("+1 07:30", credit={P: {"READ": 60}}, uncredited={}, sm="07:30")
exp.row("+1 07:31", credit={P: {"READ": 60}}, uncredited={}, sm="07:31")
exp.row("+1 07:32", credit={P: {"READ": 60}}, uncredited={}, sm="07:32")
exp.row("+1 07:33", credit={P: {"READ": 60}}, uncredited={}, sm="07:33")
exp.row("+1 07:34", credit={P: {"READ": 19, "P": 41}}, uncredited={}, sm="07:34", erratum=ERRATUM)
exp.row("+1 07:35", credit={P: {"P": 60}}, uncredited={}, sm="07:35")
exp.row("+1 07:36", credit={P: {"P": 60}}, uncredited={}, sm="07:36")

expected = exp.doc(
    extra_totals={
        "credited_s": 810,
        "agent_running_s": 23400,
        "agent_only_s": 23180,
        "ai_credited_s": 810,
        "hand_credited_s": 0,
    },
    stated=["credited_s", "agent_running_s", "agent_only_s", "ai_credited_s", "hand_credited_s"],
    approx={
        "ai_logged_s": {"about": 23500, "sm": "Hackatime would log about 23,500 s of ai coding for the night"},
        "handbook_s": {"about": 7800, "sm": "the flat third would credit about 7,800 s (2 h 11 min)"},
        "ai_ratio": {"about": 0.03, "sm": "ai_ratio is about 0.03"},
    },
    errata=[ERRATUM],
    notes=[
        "The lead of the 23:40:00 prompt is 120 + 75 / 1.5 = 170 s and that of the 07:37:00 prompt 120 + 61 / 1.5 "
        "= 161 s, with 40.67 s of typing rounded as SM rounds it. The quoted prompts have 73 and 60 characters; "
        "see the erratum.",
        "From 23:43:40 every second is X.absent although the agent runs: no presence wins over an agent with "
        "nothing to show (SM's 23:43 row). Those seconds are agent-only time all the same, so agent_only_s is "
        "23,400 - 220 credited during the run.",
        "The overnight row's seconds follow from SM's times: the tail ends at 23:43:40, the agent finishes at "
        "06:10:00 and he unlocks at 07:30:00.",
        f"This fixture's agent events replay to {replay:g} s under min(gap, 120 s); SM says about 23,500.",
        f"The bucket holds {float(left):g} s when he starts the follow-up's lead at 07:34:19; the 07:37:00 "
        "prompt resets it.",
    ],
)

anchors = {
    "fixture": "6.4",
    "anchors": [
        tl.anchor(fx, goal, kind="substantive_prompt", origin="human_typed", prompt_class="substantive",
                  lead_s=170, trail_s=0, lead_from="sm", level="A",
                  sm="23:39: a typed /goal with an objective is a substantive prompt; lead 120 + 50 = 170 s"),
        tl.anchor(fx, morning, kind="substantive_prompt", origin="human_typed", prompt_class="substantive",
                  lead_s=161, trail_s=0, lead_from="sm", level="A", sm="07:36: lead 120 + 41 = 161 s"),
    ],
    "turns": [tl.turn(fx, RUN_START, STOPS[0], session=S, turn_key=TURN[1], origin="human_typed",
                      prompt_class="substantive", sm="23:39")]
    + [tl.turn(fx, STOPS[k - 2], STOPS[k - 1], session=S, turn_key=TURN[k], origin="self_continue",
               sm="23:44 to 07:29: every continuation is a Stop-hook turn") for k in range(2, 10)]
    + [tl.turn(fx, at("+1 07:37:00"), None, session=S, turn_key=TURN[10], origin="human_typed",
               prompt_class="substantive", sm="07:36")],
}

prompts = {
    "fixture": "6.4",
    "prompts": [
        {"turn_key": TURN[1], "t": RUN_START, "local": "23:40:00",
         "text": "/goal all tests pass and lighthouse performance above 90 on the home page", "len_chars": 75,
         "source": "quoted in SM 6.4's 23:38 row, which gives its length as 75 characters", "erratum": ERRATUM},
        {"turn_key": TURN[10], "t": at("+1 07:37:00"), "local": "07:37:00",
         "text": "nice. explain what you changed in the service worker and why", "len_chars": 61,
         "source": "quoted in SM 6.4's 07:35 row, which gives its length as 61 characters", "erratum": ERRATUM},
    ],
}

fx.write(tl.out_dir(HERE), expected, anchors, prompts)
