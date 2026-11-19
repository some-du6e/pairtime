# Timeline fixtures

The struggle-metric spec's worked examples (docs/design/struggle-metric.md section 6, SM 6 below) as the events a real client would record, with the minute rows, totals, anchors and prompt texts SM states for them. They are the scorer's oracle: `pkg/score`'s tests load a fixture, feed its events to the scorer in order and compare what comes out with the rows. They were written before the scorer, from SM alone.

These files are oracle paths (docs/PLAN.md, ground rule 5). A PR that changes a fixture together with code under `cmd/`, `internal/`, `pkg/`, `web/` or `extension/` needs Karim's `oracle-change` label. When a worked example doesn't reproduce under SM's own rules, the fix is an erratum in `docs/errata/` for Karim, never a quiet change to a number (ground rule 6).

| Fixture | SM | What it is | Credited | Errata |
|---|---|---|---|---|
| [6.2](6.2/) | 6.2 | Five minutes before school: Cursor on a Windows 10 laptop without the key counter, level B | 265 s | none |
| [6.4](6.4/) | 6.4 | A `/goal` agent running unattended overnight: Claude Code on Linux X11 with the key counter, level A | 810 s; agent 23,400 s, 23,180 s of it agent-only | [prompt lengths](../../docs/errata/2026-11-19-sm-6.4-prompt-lengths.md) |
| [6.6](6.6/) | 6.6 | A scripted `claude -p` loop with a mouse jiggler on Windows 10 | 0 s | [uncredited codes](../../docs/errata/2026-11-19-sm-6.6-uncredited-codes.md) |

WP-FX2 adds 6.3, its two-tab variant, 6.5 and SM 4.2's alternation case; WP-FX3 adds 6.1; WP-FX4 adds `sidedoor/`.

## A fixture directory

| File | What it holds |
|---|---|
| `events.jsonl` | `pkg/event` events in arrival order, one per line |
| `expected.json` | The minute rows and totals SM states |
| `anchors.json` | Every human act SM names, and every turn with the origin SM gives it |
| `prompts.json` | The prompt texts SM quotes, keyed by turn, for features computed from content |
| `README.md` | The SM rows used, the date and UTC offset chosen, the OS and evidence level, and every timing choice SM leaves open |
| `gen.py` | The script that writes the four data files |

### `events.jsonl`

One device's events, each line exactly what `pkg/event` writes for the event (`json.Marshal` of an `event.Event`): compact JSON, envelope fields first, then `kind`, then `data`. `seq` is gapless and `t` never goes back, so arrival order is time order; events of the same millisecond keep the order in which the agent wrote them. `mono` counts milliseconds since the boot, through sleep, as Go's clock does on Windows. No fixture suspends a Linux or macOS machine, where it wouldn't.

Records before the window (below) set up state the window needs, such as a session started a few minutes earlier, and aren't checked.

Conventions the scorer relies on:

- **Presence.** `os.presence` every 15 s on :00, :15, :30 and :45 UTC while a project context is live, once a minute on :00 after 600 s with no input and no running turn, and one more at each hook, focus and power event (DESIGN 3.3, 6.2 D14). A sample taken at an event comes after it, at the same millisecond. A sample at time `s` with `idle_any_s` x says the last input was at `s − x`, and `idle_keys_s` the same for key presses; it is null on machines that can't tell keys apart. No input falls on a slot boundary, so the sample at a slot's end says whether the slot had input.
- **Focus.** `os.focus` when the foreground app changes, at a resume, and whenever the focused window's `hosted_sessions` change because a harness session starts or ends under it. Without the last case, a session started in an already focused terminal would never be in `hosted_sessions`, and SM 2.2 rule 6 couldn't find it at submit. `focus_project` is set when the window resolves to one project (SM 3.2): an editor or a terminal hosting one session.
- **Turns.** A prompt's turn has the `turn_key` of its `prompt.submit`. A turn a stop hook continues has no `prompt.submit` (HS 3.5); it gets its own `turn_key`, starts when the turn before it stops, and ends with an `agent.stop` whose `stop_hook_active` is true.
- **Local fields.** Hashes the scorer reads (`app_hash`, `cmd_hash`) are present. Prompt text and its sketches (`text`, `minhash`, `template_hash`, a pasted error's `error_sig`) are not: SM quotes some texts and none of their sketches, so content-derived features come from `prompts.json`.
- **Keys** are `<letter>_` and 12 hex digits of a SHA-256 over a label, standing in for the per-device keys a real client derives. They mean nothing outside the fixture.

### `expected.json`

```json
{
  "fixture": "6.2",
  "window": {"start": "2026-11-24T12:40:00Z", "end": "2026-11-24T12:45:00Z", "local": "07:40-07:45"},
  "rows": [
    {"start": "2026-11-24T12:41:00Z", "minutes": 1, "local": "07:41", "bucket": true, "bucket_minutes": 1,
     "credited_s": 40, "credit": {"p_be4901c04a84": {"E": 20, "SUP": 20}},
     "uncredited": {"X.agent_only": 20}, "agent_running_s": 60, "sm": "07:41"}
  ],
  "totals": {"credited_s": 265, "credited_by_reason": {"P": 60, "E": 120, "G": 50, "SUP": 20, "READ": 15}},
  "stated": ["credited_s", "lid_open_s", "bucket_left_s"]
}
```

- `window` is the span the rows cover, in UTC, from the first minute SM describes to the end of the last. The other top-level fields name the SM section, date, time zone, UTC offset, OS, whether `input_keys` exists, the evidence level and witness label of every credited second, the device and the projects by key.
- `rows` tile the window in order. A row is one minute, or a run of minutes where SM gives only a range (`minutes` > 1, `local` "23:44-06:09").
  - `bucket` says whether the scorer writes a minute bucket for the row, which it does only for a minute with credit or a running agent (SM 5.1). `bucket_minutes` says how many of a run's minutes have one; credited and uncredited seconds cover those minutes and no others.
  - `credit` holds credited seconds by project, then by reason code (SM 5.2). `uncredited` holds uncredited seconds by code, and with `credited_s` adds up to 60 for every bucket minute. When SM lists a minute's codes without their seconds, `uncredited` is null and `codes` lists them.
  - `agent_running_s` is the seconds an agent turn ran in the row. SM rarely states it; it follows from the times SM gives.
  - `sm` names the SM row transcribed, `derived` the fields worked out from SM's times rather than stated in its row, `erratum` the erratum that questions the row, and `note` anything else.
- `totals` sums the rows (`credited_s`, `credited_by_project`, `credited_by_reason`, `uncredited`, `agent_running_s`) and adds what else SM states, such as `agent_only_s` or `bucket_left_s`. `stated` lists the totals SM's text gives; the rest are sums. `approx` holds the ones SM gives only roughly ("about 23,500 s"), each with the sentence it comes from.
- `errata` lists the erratum files, without `.md`, that apply. `notes` explains totals and rows that need it.

A scorer test feeds every event to the scorer, takes the buckets for minutes inside the window, and checks each row: no bucket where `bucket` is false; for a single minute, the credited seconds by project and reason, the uncredited seconds by code (when not null) and `agent_running_s`; for a run, the same summed over its minutes, and the number of minutes with a bucket.

### `anchors.json`

`anchors` lists the human acts in time order: the event behind each (`event_id`, `t`, `project_key`, `via`), its kind (SM 2.3), its origin and prompt class for prompts (SM 2.2), `lead_s` and `trail_s`, and `level` (SM 5.1, DESIGN 6.4). `lead_from` is `sm` when SM states the lead and `params` when it comes from SM 2.3's table and SM 8's parameters. `turns` lists every turn: session, `turn_key`, submit and stop times, and the origin and class SM gives.

### `prompts.json`

One entry per human or scripted prompt: `turn_key`, `t`, `len_chars`, and `text` where SM quotes it, else null. `source` says where the text comes from. When a quote and the length SM gives disagree, `len_chars` is SM's length, as in the event, and `erratum` names the erratum.

## Generating

Each fixture's `gen.py` describes its timeline in local time and writes the four data files with `timeline.py`, a small standard-library module in this directory:

```sh
python3 -I testdata/timelines/6.4/gen.py            # rewrite the checked-in files
python3 -I testdata/timelines/6.4/gen.py --out /tmp/x
```

Output is deterministic: event ids are UUIDv7s with the event's time and bits from a hash, and anything that varies comes from a hash of a fixed seed. Each `gen.py` also checks its timing choices against SM's rows before it writes, for example that the reading bucket never runs dry while SM credits reading. `internal/timelines` runs every `gen.py` in CI and fails if its output differs from the checked-in files by a byte.

## The loader

`internal/timelines` loads fixtures for tests: `Root` finds this directory from any package's tests, `List` names the fixtures, `Load` and `LoadAll` decode them. Its own tests check that every event passes `Event.Validate`, matches `docs/schema/event.schema.json` (with a validator for the keywords that schema uses) and re-encodes to its line byte for byte; that rows tile the window and account for every second; that totals are the sums of the rows; that anchors, turns and prompts match the events; and that each SM total the plan names is there.
