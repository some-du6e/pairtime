# SPDX-License-Identifier: AGPL-3.0-or-later
"""The timeline of focus-unknown cases (b), b-pty-game and its X11 twin
b-pty-game-x11: a script drives Claude Code through tmux while the person
plays a browser game. The two cases share every act and every time, and
differ only in what the machine can see of focus. README.md in each case
says what it is.

Standard library only.
"""

import timeline as tl

# The script's four prompts, every 300 s, each exactly 90 characters. They
# are four different tasks, so SM 4.2's re-roll count never grows.
PROMPTS = [
    ("20:03:00", "Add a settings screen with a toggle for sound effects and save the choice in localStorage."),
    ("20:08:00", "Write unit tests for the crop growth timer in farm.js, then fix any bug that they turn up."),
    ("20:13:00", "Move all the shop prices into one config object so they stop being scattered across files."),
    ("20:18:00", "Make the harvest animation smoother on slow phones by drawing fewer particles at one time."),
]


def build(fx, *, x11):
    """Adds the case's records to fx and returns its keys. On GNOME on
    Wayland (x11 False) the agent records focus as unknown; on X11 it sees
    the terminal the script ran in, then the browser."""
    at = fx.at
    k = dict(
        project=tl.key("p", "focus-unknown b idle-farm"),
        session=tl.key("s", "focus-unknown b claude session"),
        turns=[tl.key("t", f"focus-unknown b turn {i + 1}") for i in range(len(PROMPTS))],
        files=[tl.key("f", f"focus-unknown b file {i}") for i in range(4)],
        terminal=tl.key("a", "focus-unknown b terminal"),
        browser=tl.key("a", "focus-unknown b browser"),
    )
    cc = dict(harness="claude_code", version="2.1.328", session=k["session"], project=k["project"])
    rand = tl.Rand("focus-unknown b")
    for t, text in PROMPTS:
        tl.check(len(text) == 90, f"the prompt at {t} has 90 characters")

    def inp(t, is_key):
        tl.check(t % tl.SLOT_MS, f"input at {fx.hhmmss(t)} is off a slot boundary")
        fx.inputs.append((t, is_key))

    # 19:54:50 to 19:55:20: Kai runs the script in a terminal. It starts
    # Claude Code detached in tmux and sends the first prompt at 20:03:00.
    for t in range(at("19:54:51.300"), at("19:55:01.600"), 400):
        inp(t, True)  # ./drive.sh
    inp(at("19:55:01.600"), True)  # Enter
    fx.emit(at("19:55:04"), "session.start",
            {"start_kind": "startup", "entrypoint": "cli", "parent_class": "multiplexer", "has_tty": True,
             "permission_mode": "acceptEdits"},
            source="hook", via="hook", sample=True, **cc)
    # The SessionStart hook makes a project context live (DESIGN 3.3), so
    # the agent records the window in front.
    if x11:
        # The terminal hosts no session: Claude Code runs under the tmux
        # server, which isn't one of its processes.
        fx.emit(at("19:55:04"), "os.focus", {"app_class": "terminal", "app_hash": k["terminal"]},
                source="os", via="os", sample=True)
    else:
        fx.emit(at("19:55:04"), "os.focus", {"app_class": "unknown"}, source="os", via="os", sample=True)
    fx.ticks("19:55:15", "20:20:30", tl.SLOT_MS)
    inp(at("19:55:19.900"), True)  # Alt-Tab to the browser
    if x11:
        fx.emit(at("19:55:20"), "os.focus", {"app_class": "browser", "app_hash": k["browser"]},
                source="os", via="os", sample=True)

    # 19:55:20 to 20:20:30: the game, keys and pointer every 0.4 to 2.5 s.
    t = at("19:55:21.300")
    while t < at("20:20:30"):
        if t % tl.SLOT_MS == 0:
            t += 100
        inp(t, rand.below(3) > 0)
        t += rand.between(400, 2_500)

    # The script's turns. tmux send-keys writes each prompt to Claude Code's
    # pty, which the OS's idle time never sees, then Enter.
    for i, (local, _) in enumerate(PROMPTS):
        T = at(local)
        turn = k["turns"][i]
        fx.emit(T, "prompt.submit",
                {"turn_key": turn, "len_chars": 90, "pasted_chars": 0, "typed_len_est": 90,
                 "origin_declared": "none", "matches_pending_cron": False, "answers_question": False,
                 "low_effort": False},
                source="hook", via="hook", sample=True, **cc)
        fx.emit(T + 20_000, "agent.output",
                {"turn_key": turn, "words": 45, "code_lines": 0, "is_final": False, "asks_question": False,
                 "offers_options": False},
                source="log", via="log", **cc)
        added = rand.between(9, 13)
        fx.emit(T + 50_000, "agent.tool",
                {"tool_class": "edit", "files": [k["files"][i]], "lines_added": added, "lines_removed": 16 - added,
                 "visible_output_lines": 0, "t_start": T + 45_500, "t_end": T + 50_000},
                source="hook", via="hook", sample=True, **cc)
        fx.emit(T + 100_000, "agent.output",
                {"turn_key": turn, "words": 60, "code_lines": 0, "is_final": True, "asks_question": False,
                 "offers_options": False},
                source="log", via="log", **cc)
        fx.emit(T + 100_000, "agent.stop", {"turn_key": turn, "stop_reason": "end_turn", "stop_hook_active": False},
                source="hook", via="hook", sample=True, **cc)
    return k


def prompts_doc(fx, events, keys, name):
    out = []
    for i, (local, text) in enumerate(PROMPTS):
        e = fx.event_at("prompt.submit", fx.at(local))
        tl.check(e["data"]["turn_key"] == keys["turns"][i], f"the prompt at {local}")
        out.append({"turn_key": e["data"]["turn_key"], "t": e["t"], "local": fx.hhmmss(e["t"]), "text": text,
                    "len_chars": 90,
                    "source": "invented for WP-FX8: one of four tasks the script sends, all different, so no two "
                              "are near-duplicates (SM 4.2)"})
    return {"fixture": name, "prompts": out}
