# Relayed fixtures

A person who drives agents through an agent app types on another device: T3 Code's web client in a browser, its phone app, or a chat bot. The machine that runs the agent sees prompts with no input before them, and SM 2.2 rule 4 or rule 8 takes them. D-relayed counts the ones the app's own store, or a chat bot's `pairtime relay` call, says a person sent: SM 2.2 rule 9's `relayed` origin. It credits SM 2.8's shapes at evidence level C, a typing lead ending at the app's stamp and at most 120 s of reading per turn ([the erratum](../../../docs/errata/2026-11-22-relayed-prompts.md)).

These cases are the oracle for the scorer PR that builds rule 9, and for the T3 Code reader and `pairtime relay` after it. The credit in them is worked out by hand from the SM text, minute by minute, in each case's README, before any scorer code exists.

| Case | Machine | What it is | Credited |
|---|---|---|---|
| [`t3-away`](t3-away/) | T3 Code's Linux server, no desktop | Three messages typed in T3's web client on a laptop, and an agent's message between them | 212 s, level C |
| [`t3-here`](t3-here/) | A MacBook running T3 Code's desktop app | t3-away's first message, typed into T3's window on this computer | 495 s, level A |
| [`t3-queued`](t3-queued/) | T3 Code's server | Messages sent while a turn runs: one queued 5 s after another, one steered, one queued past `RELAY_MATCH_S` | 186 s, level C |
| [`t3-unattended`](t3-unattended/) | T3 Code's server | Messages T3 marks `createdBy` `user` that nobody typed: a scheduled task, a webhook, an automatic `/compact`, a guest's message, another environment's thread; and one person's message among them | 270 s, level C |
| [`agent-day`](agent-day/) | T3 Code's server | A day of prompts agents sent each other through T3, and a person's message whose run no session pairtime sees took | 0 s |
| [`chat-bot`](chat-bot/) | The server, with a chat bot | `pairtime relay`: two calls that pair with prompts T3 delivered, one that pairs with nothing, one beside the bot's own `claude -p` | 210 s, level C |

## Against the timeline format

Each case is a directory with the files of [`../README.md`](../README.md)'s format, which `internal/timelines` loads as `relayed/<case>`. `List` doesn't descend into this directory. The scorer PR adds the test that checks these cases the way `lookback_test.go` checks the lookback ones, and their goldens. The differences:

- **Events.** The hook records of the sessions, plus the reader's `relay.message` records (SM 1.2): `source` `log` and `via` `app` for T3 Code's store, `source` `cli` and `via` `plugin` for `pairtime relay`. A record's data are `app`, `sent_at`, `thread_key`, `declared`, `len_chars`, `low_effort` and, when its prompt came first, `prompt`, that prompt's event id. A prompt that came after its record carries `relay`, the record's event id, after `low_effort`. The cases on the server have no `os.presence`, `os.focus` or `os.power` record at all, since a server with no desktop samples nothing. [`t3-here`](t3-here/) has presence and focus like the SM fixtures.
- **Sessions T3 Code's server starts** have `entrypoint` `unknown`, `parent_class` `agent_app` and no TTY: DESIGN 5.1's app table names T3's server, `t3 serve`, as an agent app's server.
- **expected.json** has `evidence` `C` and `witness` `witnessed` for the server cases (`A` for t3-here), and `stated` is empty, because SM states no relayed numbers. Every uncredited second has a code: SM 5.2 settles them for slots whose presence is unknown (`X.bucket_cap`, then `X.agent_only` while a person's turn runs, `X.automated` while only automated turns do, then `X.no_anchor`). Totals add `level_c_s`, `via_relay_s` and counts of relayed, headless and other prompts.
- **anchors.json.** A relayed anchor's event is the relay record behind it, with `sent_at` and `local_sent_at`, the app's stamp, where the lead ends, `prompt`, the prompt it paired with, and `labels`. Its `lead_s` is typing time only.
- **README.md** works every row out by hand in two kinds of table, which gen.py parses and checks: `| Minute | Credited | Reasons | Arithmetic |` and `| Total | Seconds | Arithmetic |`, as in the lookback cases.
- **No texts.** No case quotes a real message, and `prompts.json` holds lengths. Every text is invented, and only its length and its go-ahead test matter.

## Readings the cases share

- **The lead ends at the app's stamp**, `sent_at`, not at the prompt's `t`, and the second or two between them belongs to no window.
- **A relayed lead is typing time only**, from the app's own text, capped at the seconds since the thread's previous witnessed message. Every relayed message has a length divisible by 3, so its typing time is whole seconds and no rounding rule matters.
- **Relayed reading** runs from one of the session's stops until the thread's next relayed message is sent or the session's next turn starts, and only when that message comes within 600 s of the stop. The bucket holds at most 120 s from the relayed prompt's pickup.
- **One session at a time.** No second here is wanted by two relayed sessions, and Replay checks that, so 2.7's rule for two relayed sessions isn't exercised.

## Generating

```sh
python3 -I testdata/timelines/relayed/t3-away/gen.py           # rewrite the checked-in files
python3 -I testdata/timelines/relayed/t3-away/gen.py --out DIR
```

Each gen.py builds its events with [`../timeline.py`](../timeline.py), writes the rows worked out by hand, and checks them before writing against [`relayed.py`](relayed.py)'s `Replay`, a second-by-second reading of the relayed rules written only to catch slips in the hand arithmetic. t3-here, whose prompt scores by the live rules, checks against [`../sidedoor/sidedoor.py`](../sidedoor/sidedoor.py)'s `Sweep` instead. The rows are the oracle, not the replays. gen.py also checks its README's tables against the rows it writes.
