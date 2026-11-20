# Side-door fixtures

Four paths accept claims from any process on the machine: `pairtime track`, plugins, the WakaTime bridge, and editor heartbeats that fail the peer-process check (DESIGN 5.2). On a Windows laptop without the typing module, a $10 jiggler is all the presence evidence there is, so DESIGN keeps these paths out of submissions unless something independent backs them. These seven cases are WP-FX4's oracle for those rules: DESIGN 5.2, 5.4, 6.2 D2, D3 and D19, and 6.4. `pkg/score`'s tests (WP-SC1, WP-SC2, WP-SC4) and the `track` and plugin work packages check against them.

Every expected value follows from the rule text in those sections and SM 8's parameters. No scorer produced any of it.

| Case | Machine | What happens | Expected |
|---|---|---|---|
| [a](a-track-jiggler/) | Windows, jiggler | `pairtime track` every 21 s from a session no hook or log reader sees | 387 s, all level C and `via_track`; `logged_s` and `ai_logged_s` 0; nothing submittable |
| [b](b-track-keys/) | macOS, jiggler | the same loop, with key presses in the host window in the 10 s before each call | 389 s, all level A |
| [c](c-plugin-sizes/) | Windows, jiggler | a plugin posting two turns whose outputs claim 50,000 words | 480 s, all level C; each turn fills 20 s; `logged_s` 0 |
| [d](d-editor-script/) | Windows, jiggler | a script posting editor heartbeats to the local WakaTime port | 520 s, all level C; `logged_s` 0 |
| [e](e-bridge-lines/) | Windows, jiggler | bridge records of three 5,000-line agent edits | 240 s, all level C; each turn fills 20 s; `logged_s` and `ai_logged_s` 243 s |
| [f](f-track-matched/) | Windows | Claude Code calls `pairtime track` 4 s after the hook prompt it copies | `track_matched`, opens nothing; 280 s at level B |
| [g](g-track-without-prompt/) | Windows | a track call on a hooked session, 170 s from any prompt | opens nothing, raises `track_without_prompt`; 280 s at level B |

Every Windows case has `input_any` true and `input_keys` null: `idle_keys_s` is null in every sample. In (a) to (e) a jiggler moves the pointer every 12 s; in (f) and (g) the person's own touchpad and typing do the same job. Either way every slot has input, so every slot is present, and focus stays on the project's window for the whole window. Presence and focus are never in question, which keeps each case about one rule.

## A case directory

A case has the four data files of a timeline fixture, in the format of [../README.md](../README.md), so `Load` reads it: `timelines.Load(root, "sidedoor/a-track-jiggler")`. It adds `sidedoor.json`, and like an SM fixture it has a `README.md` with the timing choices and a `gen.py` that writes the data files.

Where the side-door files differ from the SM ones:

- There is no SM row to transcribe. `expected.json`'s `sm` field names the plan entry and the DESIGN sections, and rows carry a `note` and, where it applies, an `erratum`, but no `sm`. Each `gen.py` writes its rows by hand and checks them second by second against `Sweep` in `sidedoor.py`, a direct reading of SM 2.7 for the few constructs these cases use. The two had to agree.
- `stated` lists the totals the WP-FX4 entry pins, plus DESIGN 5.2's 0 submittable seconds for (a).
- Totals add `level_a_s`, `level_b_s` and `level_c_s`, `via_track_s`, `logged_s` and `ai_logged_s`, and `level_c_cap_s`. Each case credits at one level, the one `expected.json`'s `evidence` names.
- A track anchor's record is a `cli.track`. `anchors.json` gives it a prompt kind and class, because DESIGN 5.4 classes it "like a prompt", and no origin. SM 2.2's origin rules are for `prompt.submit`, and DESIGN 5.4's four conditions take their place.
- `prompts.json` also holds track labels. A `cli.track` has no `turn_key`, so the entry's `turn_key` is this fixture's name for the track turn, the one `sidedoor.json`'s turns use.

### `sidedoor.json`

```json
{
  "fixture": "sidedoor/f-track-matched",
  "case": "f",
  "records": [
    {"event_id": "...", "t": 1795637084000, "local": "14:04:44", "kind": "cli.track", "via": "track",
     "anchor": false, "track": "matched", "flags": [], "logged_s": 0, "ai_logged_s": 0, "why": "DESIGN 5.4: ..."}
  ],
  "turns": [
    {"opened_by": "...", "turn_key": "t_...", "session_key": "s_...", "t": 1795637080000, "local": "14:04:40",
     "via": "hook", "backed": true, "output_fill_s": 40, "fill_s": 60, "why": "SM 2.4: ..."}
  ]
}
```

- `records` lists every record of the window except the `os.*` ones, in arrival order. `anchor` says whether it opens an anchor, which `anchors.json` then lists, and an anchor has its `level` and `labels`. A `cli.track` has `track`: `anchor` when DESIGN 5.4's four conditions open one, `matched` for `track_matched`, `without_prompt` when it raises `track_without_prompt`. `flags` are the tier-2 flags a record raises. `logged_s` and `ai_logged_s` are what it adds to each total, and `why` is the rule.
- `turns` lists every turn whose reading fill the case pins: the prompt or track call that starts it (`opened_by`), whether a hook or transcript record backs it (`backed`), what its reported output would add without the side-door cap (`output_fill_s`, to the millisecond), and its whole fill, base included (`fill_s`).

## Readings the cases share

- **The typing part of a lead.** SM 2.3 realizes it with presence alone where `input_keys` doesn't exist. DESIGN 5.4 is stricter for a track anchor: "Its typing part is realized only in slots with `input_keys`". On Windows that means never (a); on macOS it means slots with key presses (b). Plugin and bridge prompts keep SM 2.3's rule (c, e).
- **The side-door fill cap.** DESIGN 5.2 lets plugin and bridge output "fill a session's reading bucket only up to `R_TURN_BASE_S` = 20 s per turn". The cases count the turn's 20 s base in that, as the WP's "fill at most 20 s per turn" does, so the reported output adds nothing. A track turn's fill is the base alone (DESIGN 5.4).
- **Bridge prompts** carry no text. One of 20 characters or more is substantive, since every go-ahead on SM 2.2's list is shorter (AD D2), and it gets the typing lead only (D4).
- **`logged_s` and `ai_logged_s`.** SM 5.3's min(gap, 120 s) over editor heartbeats and agent events, with D19's via filter: `hook`, `log`, `otel`, `editor` and verified `bridge` records count, and `track`, `plugin`, `editor_unverified` and `bridge_unverified` records never do. SM 5.4's agent events are prompt submits, tool calls and stops, the three kinds FX1's 6.6 replays; agent edits are tool calls. OS records are neither. A record's share follows Hackatime: each counted record after the first adds the gap since the one before it, capped at 120 s (HC 1.4), so the shares sum to the total.
- **`level_c_cap_s`** is min(`level_c_s`, `ai_logged_s` / 3), SM 5.4's ceiling for level-C seconds. D5 adds that a program may take them only if it opts in to level C, and only inside commit intervals whose closing commit is forge-verified. So the cap bounds what any program's policy could submit.
- **No sample at HTTP records.** DESIGN 3.3 samples presence at hook, focus and power events, and `pairtime track` takes the hook path (DESIGN 3.2), so each track call has a sample. Plugin and bridge records arrive over HTTP and get none. Cases (c) and (e) time the jiggler so that a regular sample shows input in the 10 s before each prompt, as SM 2.2 rule 6 asks.

## Errata

- [Turns without a stop](../../../docs/errata/2026-11-19-turns-without-a-stop.md): nothing says whether a track or bridge turn is running, which decides `SUP` against `READ`, `X.agent_only` against `X.no_anchor`, and `agent_running_s`. Cases (a), (b) and (e) use the proposed rule and name the erratum on the rows that depend on it. Their credited seconds, levels, fills and `logged_s` don't depend on it.
- [Track match windows](../../../docs/errata/2026-11-19-track-match-windows.md): a track call 10 to 30 s from a hook prompt is both `track_matched` and `track_without_prompt`. No case sits in that gap; WP-SC1 needs the answer.

## Generating

```sh
python3 -I testdata/timelines/sidedoor/a-track-jiggler/gen.py            # rewrite the checked-in files
python3 -I testdata/timelines/sidedoor/a-track-jiggler/gen.py --out /tmp/x
```

`sidedoor.py` is the code the cases share: SM 8's values they use, the sweep, the `logged_s` series, the checks on presence and samples, and the `sidedoor.json` writer. It imports `../timeline.py`, which it doesn't change. `internal/timelines/sidedoor_test.go` checks every case the way that package checks the SM fixtures: events against `Event.Validate`, the JSON Schema and their own encoding; rows and totals; anchors, turns and prompts against the events; `sidedoor.json` against the events and the rules each outcome rests on; the acceptance values in the WP-FX4 entry; and each `gen.py` reproducing its files byte for byte.
