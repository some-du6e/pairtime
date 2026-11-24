# Relayed fixtures

A person who drives agents through an agent app types on another device: T3 Code's web client in a browser, or its phone app. The machine that runs the agent sees prompts of sessions T3's server started, and SM 2.2 rule 4, which lists their `parent_class` `agent_app`, takes them. D-relayed counts the ones the app's own store says a person sent: SM 2.2 rule 9's `relayed` origin. It credits SM 2.8's shapes at evidence level C, a typing lead ending at the app's stamp and at most 120 s of reading per turn ([the erratum](../../../docs/errata/2026-11-22-relayed-prompts.md)).

These cases are the oracle for the scorer PR that builds rule 9, and for the T3 Code reader after it. The credit in them is worked out by hand from the SM text, minute by minute, in each case's README, before any scorer code exists.

| Case | Machine | What it is | Credited |
|---|---|---|---|
| [`t3-away`](t3-away/) | T3 Code's Linux server, no desktop | Three messages typed in T3's web client on a laptop, and an agent's message between them | 212 s, level C |
| [`t3-here`](t3-here/) | A MacBook running T3 Code's desktop app | t3-away's first message, typed into T3's window on this computer | 495 s, level A |
| [`t3-queued`](t3-queued/) | T3 Code's server | Messages sent while a turn runs: one queued 5 s after another, one steered, one queued past `RELAY_MATCH_S` | 186 s, level C |
| [`t3-unattended`](t3-unattended/) | T3 Code's server | Messages T3 marks `createdBy` `user` that nobody typed: a scheduled task, a webhook, an automatic `/compact`, a guest's message, another environment's thread; and one person's message among them | 270 s, level C |
| [`agent-day`](agent-day/) | T3 Code's server | A day of prompts agents sent each other through T3, and a person's message whose run no session pairtime sees took | 0 s |
| [`t3-late`](t3-late/) | T3 Code's server | A message queued behind an agent's long turn that pairs in time for its own lead but too late, past 840 s after a stop, to vouch for the reading before it | 340 s, level C |
| [`t3-phone`](t3-phone/) | A Windows PC running T3 Code's desktop app, no key counter | Two messages from T3's phone app, with a jiggler at the desk and T3's window in front | 197 s, level C |
| [`t3-here-agent`](t3-here-agent/) | t3-here's MacBook | An agent's message through T3's MCP tools, delivered while the owner types a draft in T3's window | 0 s |

## Against the timeline format

Each case is a directory with the files of [`../README.md`](../README.md)'s format, which `internal/timelines` loads as `relayed/<case>`. `List` doesn't descend into this directory. The scorer PR adds the test that checks these cases the way `lookback_test.go` checks the lookback ones, and their goldens. The differences:

- **Events.** The hook records of the sessions, plus the reader's `relay.message` records (SM 1.2), with `source` `log` and `via` `app`. A record's data are `app`, `client` (`web` or `mobile`, T3's `creationSource`), `sent_at`, `thread_key`, `declared`, `len_chars`, `low_effort` and, when its prompt came first, `prompt`, that prompt's event id. A prompt that came after its record carries `relay`, the record's event id, after `low_effort`, and the prompt a guest's run delivers carries `guest` there instead. The cases on the server have no `os.presence`, `os.focus` or `os.power` record at all, since a server with no desktop samples nothing. The desktop cases, [`t3-here`](t3-here/), [`t3-phone`](t3-phone/) and [`t3-here-agent`](t3-here-agent/), have presence and focus like the SM fixtures.
- **Sessions T3 Code's server starts** have `entrypoint` `unknown`, `parent_class` `agent_app` and no TTY, as a hook recorded them before it read `CLAUDE_CODE_ENTRYPOINT`: DESIGN 5.1's app table names T3's server, `t3 serve`, as an agent app's server, and SM 2.2 rule 4 lists the class, so every prompt nothing witnesses is `headless`. Once the hook reads that variable, T3's `t3-code` gives such a session's start `sdk` (SM 1.2). These cases score the same either way, because `agent_app` alone puts every prompt nothing witnesses under rule 4, and a witnessed prompt skips rule 4.
- **expected.json** has `evidence` `C` and `witness` `witnessed` for the relayed cases, the server's and t3-phone's, and `A` for t3-here. The two cases that credit nothing, agent-day and t3-here-agent, say `C`, which binds nothing. `stated` is empty, because SM states no relayed numbers. Every uncredited second has a code: SM 5.2 settles them for live slots whose presence is unknown (`X.bucket_cap`, then `X.agent_only` while a person's turn runs, `X.automated` while only automated turns do, then `X.no_anchor`), and on the desktop cases the person is present with a window that hosts the session in front throughout, so the codes come out the same. Totals add `level_c_s`, `via_relay_s` and counts of relayed, headless and other prompts.
- **anchors.json.** A relayed anchor's event is its relay record, and, unlike any other anchor's, its `t` and `local` are the record's `sent_at`, the app's stamp, where the lead ends, not the record's own `t` (SM 2.3). It names `prompt`, the prompt it paired with, and carries `labels`. Its `lead_s` is typing time only. t3-here's anchor is a `human_typed` prompt's and keeps the prompt's `t`, with the stamp in `sent_at` and `local_sent_at` and the record in `relay`.
- **README.md** works every row out by hand in two kinds of table, which gen.py parses and checks: `| Minute | Credited | Reasons | Arithmetic |` and `| Total | Seconds | Arithmetic |`, as in the lookback cases. For each minute row it checks the credit and reasons, the seconds the Arithmetic cell gives each uncredited code it names (`` `X.agent_only` 59 ``, `2 × 60`, `30 + 24 × 60 + 22 = 1,492`), and what the cell says about buckets (`no bucket` in a one-minute row, `N bucket minutes`). For each total it checks the arithmetic and expected.json's number.
- **No texts.** No case quotes a real message, and `prompts.json` holds lengths. Every text is invented, and only its length and its go-ahead test matter.

## Readings the cases share

- **The lead ends at the app's stamp**, `sent_at`, not at the prompt's `t`, and the second or two between them belongs to no window.
- **A relayed lead is typing time only**, from the app's own text, capped at the seconds since the thread's previous `relay.message` of `declared` `none`, paired or not. Every relayed message has a length divisible by 3, so its typing time is whole seconds and no rounding rule matters.
- **Relayed reading** runs from one of the session's stops until the relayed message that vouches for it is sent or the session's next turn starts. The one that vouches is the first of the thread's relayed messages sent after the stop among those that pair, the later of its record and its prompt, within `PENDING_WINDOW_S` − 60 s = 840 s of the stop, and it vouches only when it comes within 600 s of the stop. One that pairs later keeps its own lead but neither vouches for that stop's reading nor ends it. Relayed reading draws on at most 120 s of the bucket from the session's first fill, whatever reset came last, and a relayed prompt resets the bucket to its 20 s base at pickup. Local reading in a session an agent app's server runs keeps the usual 600 s cap, as t3-here's does.
- **One session at a time.** No second here is wanted by two relayed sessions, and Replay checks that, so 2.7's rule for two relayed sessions isn't exercised.

## Generating

```sh
python3 -I testdata/timelines/relayed/t3-away/gen.py           # rewrite the checked-in files
python3 -I testdata/timelines/relayed/t3-away/gen.py --out DIR
```

Each gen.py builds its events with [`../timeline.py`](../timeline.py), writes the rows worked out by hand, and checks them before writing against [`relayed.py`](relayed.py)'s `Replay`, a second-by-second reading of the relayed rules written only to catch slips in the hand arithmetic. t3-here, whose prompt scores by the live rules, checks against [`../sidedoor/sidedoor.py`](../sidedoor/sidedoor.py)'s `Sweep` instead. t3-phone and t3-here-agent check their rows against `Replay(..., desktop=True)` and the counterfactual their README works out against `Sweep`. The rows are the oracle, not the replays. gen.py also checks its README's tables against the rows it writes.
