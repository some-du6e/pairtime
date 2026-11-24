# Focus-unknown fixtures

Some desktops don't tell pairtime which window is in front: GNOME on Wayland without pairtime's GNOME extension, KDE and wlroots in v1, an X11 window manager that doesn't keep `_NET_ACTIVE_WINDOW`, and X11 while the agent re-makes the connection it follows focus on. There the agent records an `os.focus` with `app_class` `unknown`, and SM 2.5's focus-unknown paragraph scores the slots it holds:

- such a slot, and an act whose window in front is unknown, passes every focus test of SM 2 and 3 for every project, while presence, input and every other test still apply;
- where candidates of several projects want a second, SM 3.3's rules 2 to 6 choose among them, and buckets of different projects go to the project whose qualifying human anchor is nearest the second, the earlier anchor on a tie, then the lower `project_key`;
- every second credited in such a slot is level C with the label `focus_unknown`, and so is every second credited from an anchor that passed its focus test only that way;
- an uncredited second there skips `X.other_focus` and `X.ambiguous`, and the turn step reads every project's running turns;
- such a slot is never a browser slot, so it holds no lookup.

These cases are WP-FX8's oracle for WP-SC8, the scorer's focus-unknown rule, and WP-SB3 and WP-SB4 re-score (d) and (e) (docs/PLAN.md). Every number in them was worked out by hand from SM 2.5 as written, by an agent that doesn't write the scorer, minute by minute in each case's README. No scorer produced any of it.

| Case | Machine | What happens | Credited |
|---|---|---|---|
| [a](a-6.1-gnome/) | GNOME on Wayland | SM 6.1's first cycle, the same acts at the same times | 695 s, all level C. SM 6.1's 120 s of `LOOKUP` become `READ` 90, `NAV` 10 and `G` 20, and 14:04's 25 s stay `X.agent_only` |
| [b](b-pty-game/) | GNOME on Wayland | A script prompts Claude Code through tmux every 5 minutes while the person plays a browser game | 1,140 s, all level C, none at A or B |
| [b, X11 twin](b-pty-game-x11/) | X11 | The same acts, with the browser in front | 0 s |
| [c](c-x11-reconnect/) | X11 | 30 s of unknown focus inside a prompt's lead while the X connection is re-made | 575 s: 545 at level B and the 30 of the two slots unknown focus holds at level C |
| [d](d-two-projects/) | GNOME on Wayland | Two projects' sessions whose buckets compete | 836 s, 351 to `site` and 485 to `api`, all level C |
| [e](e-hour-before/) | GNOME on Wayland | The one `os.focus`, `unknown`, an hour before the work, so a re-score from a checkpoint in that hour has no `os.focus` after its start | 400 s, all level C, and nothing in the hour |

The only erratum is [split slots](../../../docs/errata/2026-11-24-sm-2.5-split-slots.md): in (d) two slots of unknown focus credit both projects, which SM 5.1's minute bucket, one project a slot, can't record. Rows 16:03 and 16:08 of (d) name it, and its README works out what they would be under the other reading.

## Against the timeline format

Each case is a directory with the files of [`../README.md`](../README.md)'s format, which `internal/timelines` loads as `focus-unknown/<case>`. `List` doesn't descend into this directory, so `internal/timelines/focusunknown_test.go` checks the cases the way `timelines_test.go` checks the SM fixtures, plus the rules below. The differences:

- **Events.** Linux machines, so the records are what pairtime's Linux agent writes. `idle_keys_s` is null in every sample: Mutter's idle monitor on GNOME and the X server's idle time on X11 without the opt-in key counter report any input only. The record of unknown focus has `app_class` `unknown` and no other field, as `../README.md` says. The agent writes it once a project context is live (internal/agent/focus), so on a desktop that never reports focus it is dated at the record that made the context live, a SessionStart hook here, in the same millisecond. Every editor heartbeat that passed the peer-process check takes a presence sample, as DESIGN 3.3 has every verified heartbeat do.
- **expected.json.** `evidence` is the level of every credited second whose slot's focus is known and whose anchor passed its focus test with focus known. Where every slot's focus is unknown, as in (a), (b), (d) and (e), every credited second is C, and `evidence` says C. In (c) it is B, and `slots.json` gives the two slots at C. The X11 twin credits nothing, and its B is the level its credit would have. `stated` is empty, because SM states no focus-unknown numbers. Totals add `level_a_s`, `level_b_s`, `level_c_s`, `focus_unknown_s` (the seconds labeled `focus_unknown`, which here are every level-C second), `agent_only_s`, `ai_credited_s` and `hand_credited_s` (SM 5.4's split), and each case's own counts. Every bucket row gives its uncredited seconds.
- **anchors.json.** Every anchor lists its `labels`. An anchor whose evidence test names focus and passed only because focus was unknown carries `focus_unknown` and is level C: a prompt by SM 2.2 rule 6, an editor heartbeat by SM 2.3's editor-focus test. An anchor whose test names no focus, a permission answer or a commit, keeps the level its own evidence gives, B on these machines, and each second it credits in a slot of unknown focus is still C.
- **slots.json**, which the other fixtures don't have. It lists every slot of every bucket minute: `start` and `local`; `focus`, the slot's `focus_class`, the window SM 2.5 gives the slot from the `os.focus` records; `credited_s`; `credit`, by project and reason; `levels`, the credited seconds by level; and `ev` and `lbl` as a minute bucket's slot has them (SM 5.1; pkg/event's `BucketSlot`): the slot's weakest level, and `focus_unknown` when any of its seconds is level C because focus was unknown. A slot of unknown focus can credit two projects, as two of (d)'s do, so a slot names no `p`.
- **README.md** works every row out by hand in two kinds of table, as the lookback and relayed cases do:
  - `| Minute | Credited | Reasons | Arithmetic |`. Minute is a row's `local`. Reasons lists `REASON seconds` items, each with the project's name first when the case has more than one project, or says `none`. The Arithmetic cell names every uncredited code of a bucket row with its seconds (`` `X.agent_only` 10 + 15 = 25 ``) and no code a row doesn't have, and a row without a bucket says "no bucket".
  - `| Total | Seconds | Arithmetic |`. Total names an `expected.json` total, a credit reason, an uncredited code or a project. The arithmetic, whole numbers with `+`, `−`, `×` and parentheses, must give the seconds, and the seconds must equal `expected.json`'s.

## Readings the cases share

- **The level is a second's.** SM 2.5 sets the level per credited second: C in a slot of unknown focus, C from an anchor that passed its focus test only through unknown focus, and otherwise what DESIGN 6.4 gives it. (c) is the case where both kinds of second occur. DESIGN 6.4 doesn't say this yet. Its levels paragraph gives a second its anchor's level, and its rows don't mention focus. D-focus-unknown's edit of 6.4, which WP-SC8 carries under `oracle-change`, adds that "a second credited in a slot whose focus is unknown is level C whatever its anchor's level". Until it lands, SM 2.5 decides, as AGENTS.md says SM does for scoring.
- **No lookup, so no lookup horizon.** A slot of unknown focus is never a browser slot, and the X11 twin's browser run has nothing credited on either side. So no case has a lookup, and E-lookup-lookahead (docs/errata/2026-11-20-lookup-lookahead.md), decided 2026-11-23, which counts only acts and prompts on record `READ_HORIZON_S` after a browser run starts, decides nothing in them.
- **Leads of several projects.** Where focus is unknown, several projects' leads can hold one second. The cases read SM 3.3 rule 3 as the 6.1 fixture does: the lead of the next anchor at or after the second wins. (a) and (d) depend on it.
- **Nearest, measured either way.** SM 2.5 doesn't say whether "nearest the second" counts from the second's start or its middle. (d)'s anchors are an odd number of seconds apart, so the two readings agree on every second, and gen.py checks both.
- **Presence stays simple.** No silence in any window reaches 120 s, so every slot is `yes` or `bridged`, and no tail, return or hard break comes into play. Nothing locks or sleeps.
- **No dark flow.** No prompt repeats another, no test fails and no prompt is a go-ahead, so SM 4.2 discounts nothing.

## Generating

```sh
python3 -I testdata/timelines/focus-unknown/a-6.1-gnome/gen.py           # rewrite the checked-in files
python3 -I testdata/timelines/focus-unknown/a-6.1-gnome/gen.py --out DIR
```

Each gen.py builds its events with [`../timeline.py`](../timeline.py) and writes down, by hand, what SM 2.5 credits: runs of seconds, each with its project and reason or its uncredited code, and the runs at level C. From those it writes the rows of `expected.json` and `slots.json`. Before writing anything it checks every second of the window against `Replay` in [`focus.py`](focus.py), a second-by-second reading of SM 2.1 to 2.7 and 5.2 for the few constructs these cases use, written only to catch slips in the hand arithmetic. The runs are the oracle, not `Replay`. gen.py also checks its README's tables against the rows it writes. [`ptygame.py`](ptygame.py) builds the timeline both (b) cases share.
