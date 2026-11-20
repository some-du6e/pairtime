# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.2, five minutes before school. See README.md in this directory."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import timeline as tl  # noqa: E402

fx = tl.Fixture(
    "6.2",
    title="Five minutes before school",
    date="2026-11-24",
    timezone_name="America/New_York",
    utc_offset_min=-300,
    os_name="windows",
    keys=False,
    device=tl.key("d", "6.2 maya laptop"),
    boot_id=tl.key("b", "6.2 boot"),
    boot="-1 16:02:11.000",
    seq0=31407,
    # Her last input before she closed the lid the evening before.
    last_input="-1 21:52:31.400",
)

P = tl.key("p", "remind-bot")
S = tl.key("s", "6.2 cursor conversation")
T1 = tl.key("t", "6.2 turn 1")
F_AGENT = tl.key("f", "6.2 src/reminders.js")
F_MSG = tl.key("f", "6.2 src/messages.js")
APP = tl.key("a", "6.2 cursor.exe")
CURSOR = dict(harness="cursor", version="2.1.50", session=S, project=P)

# Input. Windows without the key counter reports any input only, so key
# presses and touchpad use differ only in this script.
fx.pointer("07:40:00.400")                       # touches the touchpad
fx.typing("07:40:10.300", "07:40:59.800", 500)   # types the 120-character prompt
fx.pointer("07:41:07.500")                       # then waits for the agent
fx.pointer("07:41:52.000")                       # the edit has landed
fx.pointer("07:42:03.100", "07:42:12.600", "07:42:21.900", "07:42:33.400", "07:42:41.800",
           "07:42:52.200", "07:43:04.700", "07:43:13.500")  # scrolls the review view
fx.typing("07:43:22.000", "07:43:39.600", 600)   # fixes the message string
fx.pointer("07:43:44.300", "07:43:51.000", "07:43:58.200")  # opens source control
fx.typing("07:44:05.000", "07:44:24.200", 600)   # types the commit message
fx.pointer("07:44:29.900", "07:44:34.500", "07:44:40.000")  # commits; last touch at 07:44:40

at = fx.at

# The lid opens: resume, the foreground window, and a sample at once.
fx.emit(at("07:40:00"), "os.power", {"op": "resume"}, source="os", via="os", sample=True)
fx.emit(at("07:40:00"), "os.focus", {"app_class": "editor", "app_hash": APP, "focus_project": P},
        source="os", via="os", sample=True)
# She opens a new agent conversation; Cursor's sessionStart hook fires and
# the agent re-reads the focused window's hosted sessions.
fx.emit(at("07:40:00.600"), "session.start",
        {"start_kind": "startup", "entrypoint": "ide", "parent_class": "ide", "has_tty": False},
        source="hook", via="hook", sample=True, **CURSOR)
fx.emit(at("07:40:00.600"), "os.focus",
        {"app_class": "editor", "app_hash": APP, "hosted_sessions": [S], "focus_project": P},
        source="os", via="os", sample=True)
fx.ticks("07:40:15", "07:44:45", tl.SLOT_MS)

# beforeSubmitPrompt at 07:41:00.
fx.emit(at("07:41:00"), "prompt.submit",
        {"turn_key": T1, "len_chars": 120, "pasted_chars": 0, "typed_len_est": 120,
         "origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
         "low_effort": False},
        source="hook", via="hook", sample=True, **CURSOR)
# The 40-line edit (afterFileEdit), then WakaTime's heartbeat for it, which
# the agent labels as the agent's because afterFileEdit reported it.
fx.emit(at("07:41:50"), "agent.tool",
        {"tool_class": "edit", "files": [F_AGENT], "lines_added": 28, "lines_removed": 12,
         "visible_output_lines": 0, "t_start": at("07:41:50"), "t_end": at("07:41:50")},
        source="hook", via="hook", sample=True, **CURSOR)
fx.emit(at("07:41:50.300"), "editor.activity",
        {"editor": "cursor", "act": "edit", "file_hash": F_AGENT, "lines_changed": 40, "edit_source": "agent"},
        source="editor", via="editor", project=P)
# The 120-word summary (afterAgentResponse) and the stop.
fx.emit(at("07:42:30"), "agent.output",
        {"turn_key": T1, "words": 120, "code_lines": 0, "is_final": True, "asks_question": False,
         "offers_options": False},
        source="hook", via="hook", sample=True, **CURSOR)
fx.emit(at("07:42:30"), "agent.stop",
        {"turn_key": T1, "stop_reason": "end_turn", "stop_hook_active": False},
        source="hook", via="hook", sample=True, **CURSOR)
# Her hand edit. Neither afterFileEdit nor afterTabFileEdit reported it.
fx.emit(at("07:43:40"), "editor.activity",
        {"editor": "cursor", "act": "edit", "file_hash": F_MSG, "lines_changed": 1, "edit_source": "human"},
        source="editor", via="editor", project=P)
# The commit from the source-control panel; no agent tool ran git.
fx.emit(at("07:44:30"), "git.change",
        {"op": "commit", "sha": tl.git_sha("6.2 commit"), "parents": [tl.git_sha("6.2 parent")],
         "files_changed": 2, "insertions": 29, "deletions": 13, "actor": "human"},
        source="git", via="git", project=P)
# The lid closes.
fx.emit(at("07:44:45"), "os.power", {"op": "suspend"}, source="os", via="os", sample=True)

events = fx.build()


def slot_has_input(start):
    a = at(start)
    return any(a <= t < a + tl.SLOT_MS for t, _ in fx.inputs)


# Checks of the timing choices against SM's rows (README.md).
tl.check(all(slot_has_input(s) for s in ("07:40:00", "07:40:15", "07:40:30", "07:40:45")),
         "input in every slot of 07:40, so the P lead is realized from the resume")
tl.check(at("07:41:00") - fx.last_input_before(at("07:41:00")) <= 10_000, "input in the 10 s before submit")
tl.check(not slot_has_input("07:41:15") and not slot_has_input("07:41:30"),
         "07:41:15 to 07:41:45 has no input: bridged, at most 120 s, input on both sides")
tl.check(slot_has_input("07:41:00") and slot_has_input("07:41:45"), "input on both sides of the bridge")
for m in range(int((at("07:41:45") - at("07:40:00")) / 15000), int((at("07:44:45") - at("07:40:00")) / 15000)):
    t = at("07:40:00") + m * tl.SLOT_MS
    tl.check(slot_has_input(fx.hhmmss(t)), f"input in slot {fx.hhmmss(t)}")
tl.check(fx.last_input_before(at("07:44:45")) == at("07:44:40"), "last touch at 07:44:40")
tl.check(slot_has_input("07:43:30"), "input in the slot of the 07:43:40 edit")
tl.check(slot_has_input("07:44:15") and slot_has_input("07:44:30"), "input around the 07:44:30 commit")
# The bucket: 20 s base at 07:41:00, drained by 07:41:20; +300 s for 40
# diff lines at 07:41:50; +40 s for 120 words at 07:42:30; READ drains 15 s
# from 07:44:30 to 07:44:45.
bucket = 20 - 20 + 40 * 60 // 8 + 120 * 60 // 180 - 15
tl.check(bucket == 325, f"bucket left {bucket}")

prompt = fx.event_at("prompt.submit", at("07:41:00"))
edit = fx.event_at("editor.activity", at("07:43:40"))
commit = fx.event_at("git.change", at("07:44:30"))

exp = tl.Expected(fx, sm="docs/design/struggle-metric.md 6.2", evidence="B", witness="witnessed",
                  projects={P: "remind-bot"}, start="07:40:00", end="07:45:00")
exp.row("07:40", credit={P: {"P": 60}}, uncredited={}, agent=0, sm="07:40")
exp.row("07:41", credit={P: {"SUP": 20, "E": 20}}, uncredited={"X.agent_only": 20}, agent=60, sm="07:41")
exp.row("07:42", credit={P: {"E": 60}}, uncredited={}, agent=30, sm="07:42")
exp.row("07:43", credit={P: {"E": 40, "G": 20}}, uncredited={}, agent=0, sm="07:43")
exp.row("07:44", credit={P: {"G": 30, "READ": 15}}, uncredited={"X.absent": 15}, agent=0, sm="07:44")

expected = exp.doc(
    extra_totals={
        "credited_s": 265,
        "agent_only_s": 20,
        "lid_open_s": 285,
        "bucket_left_s": 325,
    },
    stated=["credited_s", "lid_open_s", "bucket_left_s"],
    approx={},
    errata=[],
    notes=[
        "agent_running_s and agent_only_s are not in SM's table; they follow from the times it gives: "
        "the agent runs from the submit at 07:41:00 to the stop at 07:42:30, and only 07:41:20 to "
        "07:41:40 of that is uncredited.",
        "bucket_left_s is the reading bucket at 07:44:45, which SM says expires with the 2-hour TTL.",
    ],
)

anchors = {
    "fixture": "6.2",
    "anchors": [
        tl.anchor(fx, prompt, kind="substantive_prompt", origin="human_typed", prompt_class="substantive",
                  lead_s=200, trail_s=0, lead_from="sm", level="B",
                  sm="07:40: the lead is 120 + 80 = 200 s, but nothing before 07:40:00 counts"),
        tl.anchor(fx, edit, kind="typed_edit", lead_s=120, trail_s=0, lead_from="params", level="B",
                  sm="07:41 to 07:43: the E lead starts at 07:41:40"),
        tl.anchor(fx, commit, kind="human_commit", lead_s=60, trail_s=0, lead_from="sm", level="B",
                  sm="07:44: G, lead 60 s"),
    ],
    "turns": [
        tl.turn(fx, at("07:41:00"), at("07:42:30"), session=S, turn_key=T1, origin="human_typed",
                prompt_class="substantive", sm="07:40: origin human_typed"),
    ],
}

prompts = {
    "fixture": "6.2",
    "prompts": [
        {"turn_key": T1, "t": at("07:41:00"), "local": "07:41:00", "text": None, "len_chars": 120,
         "source": "SM 6.2 gives the length of Maya's prompt, not its text"},
    ],
}

fx.write(tl.out_dir(HERE), expected, anchors, prompts)
