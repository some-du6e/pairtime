# pairtime design

Status: final draft for build

This is the one design pairtime is built from. It was written on 2026-11-19 from the three candidate designs, the three judge verdicts, the struggle-metric spec and the research. Where this file and an input disagree, this file wins. Where this file is silent, the struggle-metric spec decides scoring and the base design decides everything else.

- **Base.** The adoption-first design. Two of three judges chose it, and the bounty goes to the first system programs actually use (JB Recommendation; JE Recommendation).
- **Trust layer.** Grafted from the evidence-first design: the ledger format, commit-then-reveal with a committed challenge value, the consistency checks, held minutes, evidence bands, receipts and the offline verifier (JB grafts E1 to E10; JT Recommendation).
- **Motivation layer.** Grafted from the teen-first design: the coach, build days, devlog drafts, the R3 proof metrics, restorable loop rows, consent screens and the `track` anchor at level C (JB grafts T1 to T11; JT grafts 1 to 7).
- **Karim's direction**, kept as written in BOUNTY.md: AGPL-3.0, the universal AGENTS.md entry that has the agent strip PII from the user's prompt and pass it to `pairtime track`, installed wherever hooks and log readers can't see the agent (13.1 item 2), human time rather than agent time, and the server self-hosted on Karim's domain. Judge proposals that would change Karim's wording went to 13.1, where items 2 and 3 were decided on 2026-11-20 under Karim's delegation.

Every must-fix from the three judges is resolved in section 12, with the section that resolves it.

## How to read this file

Requirement ids R1 to R11 and A are from BOUNTY.md. Inputs are cited by key and section:

| Key | File |
|---|---|
| B | `docs/BOUNTY.md` |
| SM | `docs/design/struggle-metric.md` |
| AD | `docs/design/approach-adoption.md` (the base) |
| EV | `docs/design/approach-evidence.md` |
| TE | `docs/design/approach-teen.md` |
| JB, JE, JT | `docs/design/judging-bounty.md`, `judging-engineering.md`, `judging-trust.md` |
| HC | `docs/research/hackatime-and-hack-club.md` |
| HS | `docs/research/ai-harness-signals.md` |
| CP | `docs/research/client-platform.md` |
| SP | `docs/research/server-platform.md` |
| PA | `docs/research/prior-art-and-measurement.md` |
| TP | `docs/research/trust-and-privacy.md` (fact-checked 2026-11-19) |
| PR1 | PR #1, branch `wp0a-skeleton` (WP-0a): the Go module, `pkg/event`, `docs/schema/event.schema.json`, CI |

`DI N` is item N of a research doc's design-implications list, and `OQ N` is item N of its open questions. "Must" marks a requirement on the build. `<host>` is the pairtime hostname on Karim's domain, which Karim picks (13.1).

Parameters in `CAPS` are public scoring parameters. SM 8 defines most of them, and 6.3 lists the ones this design adds. All of them sit in one table whose SHA-256 is stamped on every minute bucket (SM 7.6).

## 1. Overview and user stories

### 1.1 What pairtime is

pairtime counts the human seconds of productive struggle spent building with AI, and leaves out the seconds the agent spends running on its own. A second counts only when a human act opened a window over it (a typed prompt, a permission answer, an interrupt, a hand edit, a typed command, a commit), the OS saw the person present, and focus was on that project (SM 2.7). Everything is scored on the teen's machine by the public SM rulebook. Logged hours upload numbers and hash commitments, never content. Submitted hours reveal one project's metadata and a small random sample of prompts, redacted by the teen, and the server checks both against commitments it witnessed at the time.

The number a program counts is pairtime's own credited figure after reviewer cuts. It goes into the program's hours field (`Optional - Override Hours Spent`) and replaces Hackatime's hours for that project and window. It is never supporting evidence stacked on Hackatime hours (JB must-fix 1; 9.2).

Three bets, in order of what the race needs:

1. **Live next to Hackatime and never break it.** The installer adds pairtime as a second destination in `~/.wakatime.cfg` on `127.0.0.1`, so a teen's Hackatime keeps working and programs lose nothing (AD 1, 4.5). A release that could break a teen's Hackatime doesn't ship (5.6).
2. **Hand reviewers a pre-deflated number they can check.** Rows that can only be cut, evidence bands, commits with three clocks, a sticky random reveal, and a signed bundle anyone can verify offline. The justification text comes in the handbook's own format (AD 6.2; EV 6.4 to 6.7).
3. **Be worth keeping for its own sake.** A coach on the teen's own machine: a recap after every session, a chess clock of their time against the agent's, build days with a weekly goal, a logbook of bugs they beat, and devlog drafts (TE 1, 3.6). This carries R3 by itself and doesn't lean on Hackatime for motivation (JB, R3 by proxy).

### 1.2 The teen

Maya is a made-up 14-year-old with a 2016 Windows 10 laptop, 4 GB of RAM and a spinning disk, the machine CP 7 plans for.

**Install, once.** Maya pastes `irm https://<host>/install.ps1 | iex` into PowerShell, or clicks Install on the pairtime extension in VS Code or Cursor, which brings the signed binary with it (EV 1; CP 4.2). Neither needs admin rights. `pairtime install` shows one screen listing every file it will touch: hook entries for the harnesses it found, one `[api_urls]` line in `~/.wakatime.cfg`, and an HKCU Run key (5.9). pairtime sees all of her harnesses through hooks or logs, so Karim's AGENTS.md entry isn't on the list (5.4). She signs in by typing a device code into a browser page signed in to Hack Club Auth (SP 3.5). The onboarding card shows a live preview of her public profile, which carries only aggregates, and two consent cards whose most prominent button is "Not now": the typing-evidence module and her club's board (8.3, 8.4). That is the last setup she does.

**The first minute.** Before she does anything new, pairtime reads 30 days of her Claude Code and Gemini CLI transcripts and her older Codex and Copilot logs, at low priority with a progress bar, and shows a welcome recap: "Since October 20 you built on 4 projects on 17 days. Your clock: about 23 h. Claude's clock: 61 h." Lookback minutes are evidence level C and labeled as such. They never count toward build days, public totals or submissions unless a program opts in to level C (TE 1; SM 2.8; 6.2 D5 and D11).

**Every day, nothing to start.** At 07:40 she opens the lid, types a prompt into Cursor, fixes a string by hand, commits, and closes the lid at 07:44:45 to catch the bus. pairtime credits 4 min 25 s to `remind-bot`, and the suspend triggers an upload, so the minutes are witnessed (SM 6.2). That evening she jumps between three projects and two agents, and every second goes to exactly one project (SM 3, 6.3). The tray tooltip shows "you 1h 12m, Claude 2h 40m". She never presses start or stop and never picks a project from a list.

**After each session.** When a work session ends, one quiet notification appears: "52 min on remind-bot. You fixed the timezone bug yourself. See recap." The recap opens in the browser she already has open, served by the local agent. It shows the chess clock, the moments (first green test, the agent edit she rejected, the error she fixed by hand, the commit), and loops caught, framed as coaching: "The agent rewrote the same 8 lines of `Navbar.css` 17 times between 20:05 and 20:38. Your own edit at 20:41 fixed it." Uncredited time is named for what it was, and agent-only time is named after the agent ("Claude's time"), never as her failure (TE 3.6, D5; PA 4.4).

**Every week.** Her profile says "built on 18 of the last 30 days". She picked a weekly goal of 3 build days, and pairtime counts the weeks in a row she meets it, with freezes she earns for missed weeks. There is no daily streak counter (TE D1; PA 4.1). On Sunday, or when she clicks "New devlog", pairtime drafts the facts since her last devlog (credited time, commits, the bug that fought back, firsts) and leaves the sentences to her (TE 3.6).

**Shipping to a program.** When she ships `remind-bot` to a YSWS program, she clicks "Connect pairtime" on the program's site or runs `pairtime submit`. pairtime shows exactly what will leave her machine: the timeline metadata for that one project in that window and her commits. After the server draws its sample, it shows 10 of her prompts, each next to the agent message it answered, already redacted, with every redaction highlighted and code blocks collapsed to markers. She can remove more or withhold any item, and a withheld item doesn't credit the minutes it supports (7.5). Her submission page lists every reviewer who opened her evidence and when (7.7). The site publishes the audit formula, so she knows a 10-hour claim has at least an 8.7% chance of a 15-minute audit, rising with the hours claimed (7.9).

**Privacy she can check.** `pairtime privacy show` prints the next upload byte for byte. The dashboard's "what left this laptop" page lists every past upload. A `.pairtime-ignore` file keeps a folder out entirely, and the tray has a pause switch (4.2, 8.9).

### 1.3 The reviewer

A program reviewer follows the pairtime link in the submission's `Justification - Additional Justification` field and signs in with Hack Club Auth with a reviewer role (SP 10.3). The first line answers the question they are paid to answer: "10.4 h claimed, 9.6 h credited by pairtime: 7.9 h corroborated, 1.2 h witnessed, 0.3 h offline, 0.2 h backfilled. Hackatime-equivalent 12.1 h; the handbook third would allow 6.1 h." Then come the must-view rows: every row with a failed check or a flag, plus one clean row drawn at random. "Approve remaining rows" unlocks only after those are opened (EV 6.7). Rows are work sessions inside each devlog window, and they can only be cut below captured seconds, with a reason (AD D5; TE D7; HC 3.6). Discounted loops sit in their own rows at 0, and a reviewer may raise one back up to its captured seconds if the loop was legitimate tuning (TE D12). Every commit shows three clocks: when the laptop saw it, when pairtime's server first saw it, and when the forge received the push (EV 6.3). Revealed turns sit on their timeline markers, stamped "matches commitment, witnessed 14:03:12". The handbook text is generated, and the reviewer writes the technical-features line (AD 8.2).

A Fraud Squad member who trusts neither pairtime's server nor its UI downloads the signed bundle and runs `pairtime verify bundle.tar` on their own machine. It rebuilds every Merkle root, checks the server's signature, the batch receipts and the day roots published to a public git repository, recomputes the challenge draw, and re-runs the scoring version, all offline. The bundle carries no revealed text. With `--fetch-reveals` and their reviewer sign-in, verify also checks the revealed content against its commitments, fetching it from the evidence store under the access log and never writing it to disk (7.8; JT must-fix 4).

### 1.4 The program author

Adoption comes in three tiers, and each works under today's handbook (AD 1; HC 3.7):

| Tier | Effort | What changes in the program |
|---|---|---|
| 0. Link | An hour | Add a "pairtime submission link" field to the submission form. The reviewer opens it. pairtime's generated text goes into `Justification - Alternate Tracking Method`, the review URL into `Justification - Additional Justification`, and the reviewer-approved figure into `Optional - Override Hours Spent` |
| 1. Drop-in | A day | Point a second instance of the program's existing `HackatimeService` at `https://<host>`. Every call Stardance's client makes is answered or deliberately refused (5.10). The stats call returns credited seconds for the same Hackatime project names. The Airtable script in the program kit fills the justification fields |
| 2. Native | A few days | The submissions API, with webhooks signed in Robert's scheme (SP 10.2) |

The program kit in `integrations/` holds a one-page guide, the Airtable automation script, a Rails snippet for the second `HackatimeService`, the Ari `journals[]` bridge, a Robert note ("change nothing; send the same Hackatime names"), the handbook text, and a 10-minute reviewer walkthrough (AD 8.3). The first program is a pairtime-native YSWS, Hard Mode, run on the standard stack with reviewers from outside the build team, each nominated or approved by Quality and Integrity or the HQ sponsor (TE 8.2; 9.4, 7.10).

### 1.5 Where each graft came from

| Area | Source | Section here |
|---|---|---|
| Build order, program path first, dual-send, Hackatime-shaped reads, handbook text, counting rule, three tiers | AD 1, 4.5, 4.7, 8.2 to 8.4, 10 | 5.6, 5.10, 9 |
| Ledger: header plus nonce-protected body, 60 s Merkle batches, signed receipts, keyed scorer inputs | EV 6.2, D2 | 3.6 |
| Commit-then-reveal with committed `r`, project-scoped reveal | EV 6.5 | 7.5 |
| CC1 to CC11, held minutes, bands, must-view rows | EV 6.4, D7, 6.7, D9 | 7.3, 7.7 |
| Level-C time only inside forge-verified commit intervals; the `offline` label | EV D5, D6 (capped per JT) | 6.2, 7.4 |
| Agent message beside each challenged prompt, with code collapsed | EV D3; JT | 7.5 |
| Published audit formula, seeded cases, reviewer integrity | EV 6.9, 6.10 | 7.9, 7.10 |
| Labeling mode with κ; local-only mode; integer scoring; Python reference verifier | EV WP4.10, 7.1, risk 9, WP1.1 | 6.7, 4.2, 6.1, 9.7 |
| Coach, recaps, moments, logbook, welcome recap, plain-language reasons | TE 3.6, D4, D5 | 3.8 |
| Build days, weekly goal, freezes; fun parts carry no prize | TE D1, 6.2 | 3.8 |
| Devlog drafts and devlog windows as the submission unit; story column | TE D7, 6.3 | 3.8, 7.7 |
| Restorable loop rows; `track` at level C; age bands; generic hook mode; coach skill | TE D12, D9, D11, 4.2, 4.5 | 6.2, 5.4, 6.7, 5.3, 5.5 |
| Consent cards with "Not now" most prominent, tested in Playwright | TE WP4.13 | 8.4 |
| Package rules, adapter interface, Go 1.26 for both binaries, EcoQoS, `doctor --report` | TE 10.1, 3.3, 3.10, 2; JE | 3.4, 3.12, 9.7 |
| R3 proof metrics; funnel re-based on 15% install-to-submit | TE 2, 8.1 | 6.8, 9.1 |

## 2. Requirement-by-requirement compliance

Each row gives how the design meets the requirement's words, the check that proves it, and where the detail lives. A requirement that can only be proved with real teens says which measurement decides it.

| ID | How this design meets it | How it is proved | Where |
|---|---|---|---|
| R1 AGPL | Every file is AGPL-3.0-or-later with the SPDX header PR1 enforces. Dependencies are permissive and checked by a license gate. All detector code ships in the public repo; only the deployed thresholds are private configuration (TE D13). Detectors beyond SM 8.7's published set may run in an optional private process over a socket, and no requirement depends on it (JT must-fix 12). Whether that process is compatible with AGPL section 13 is Karim's call (13.1) | CI: LICENSE text check and SPDX header check (PR1), plus a transitive license allowlist from the first PR that adds a module | 3.12, 7.2 |
| R2 Accuracy | The SM rulebook credits only human-evidenced seconds and removes dark-flow loops (SM 2 to 4), with the deviations in 6.2. SM 7.5's labeled pilot runs over winter break: two raters label minutes beside a timelapse, κ must reach 0.6, and scoring 1.0.0 is frozen before any program decides on a pairtime number (JB must-fix 2) | The R2 report: minute κ ≥ 0.6, session error ≤ 15%, bias between −5% and +3%, approved over credited ≥ 0.9, against SM 7.3's four baselines. Published before the first decision | 6.7 |
| R3 Worth using | The coach on the device: a recap after every work session, the chess clock, the welcome recap from lookback, build days with a weekly goal and freezes, a logbook of beaten errors, project stories, devlog drafts, and a coach skill for the teen's own agent (TE 3.6). None of it needs a program or Hackatime. A local-only mode uploads nothing and keeps every coach feature (EV 7.1) | Two numbers from tier-1 data with no extra telemetry: at least 50% of alpha devices still syncing after 4 weeks, and at least half of credited time on projects never submitted (TE 2). Reported to HQ with the R2 report | 3.8, 6.8 |
| R4 Passive | Autostart at login without admin, hooks registered once, editor time through the WakaTime dual-send, `.git` watchers, and 30 days of lookback at install (AD 2, R4). Nothing is started or stopped per session | SM 6.1, 6.2 and 6.3 (three hours of flow, five minutes before school, five projects in an afternoon) are CI fixtures with exact totals: 10,095 s, 265 s, and 1,800 s split across projects | 5.1, 6.1 |
| R5 Server | `pairtimed`: Hack Club Auth accounts, profiles at `/@username` that are public by default and aggregate-only, projects keyed by a root-commit fingerprint, git integration (commit records, forge verification, optional GitHub App), and a dashboard pushed over SSE within seconds of an upload (SP 1). The public view refreshes hourly; the owner's view is real time | Playwright: a delta reaches an open dashboard within 2 s of ingest; the public page carries no private project name and no hour-of-day data | 3.10, 8.3 |
| R6 Privacy | Four disclosure tiers plus local-only mode. Logged hours upload numbers and roots only; an ingest firewall rejects free text with a 422; window titles are never read; presence is sampled only while a project context is live; submitted content is encrypted end to end past Cloudflare; every retention period is finite and published; deletion goes through Hack Club's privacy hub | A fuzz test puts random text in every string field and always gets a 422 (AD WP 2.4); `privacy show` output equals the next upload byte for byte; a schema test pins every local field (PR1 `TestLocalFields`) | 4, 8 |
| R7 Every harness | pairtime never talks to a model, so it is model-agnostic. Native hooks and log readers for Claude Code, Codex, Cursor, Copilot, Gemini CLI, Devin and Cline; a generic hook mode for any harness with Claude-style hooks; plugins in any language; the WakaTime bridge for wakatime-cli's 46 transcript parsers; and Karim's AGENTS.md entry with `pairtime track` for everything else | A conformance job replays captured fixtures for every adapter and harness version. Fixtures are captured from real harnesses, and synthesized ones are marked (JE must-fix 2) | 5 |
| R8 Old laptops | One pure-Go binary per OS, seven targets including `windows/386`, Go 1.26 for both binaries until Go 1.28 ships in February 2027 and Go 1.27 after it, with Macs that top out at macOS 12 kept on the last Go 1.26 build (3.12; CP 1.5). No SQLite and no resident webview on the client, EcoQoS on Windows, low-priority lookback, sampling only in a project context | CI gates from the first agent build: binary under 15 MB, idle RSS under 25 MB on Windows, idle CPU under 0.1%, and the hook's own cost on the runner: its p95 less the p95 of an empty Go program timed in the same job, at most 10 ms, with the hook's p95 at most 50 ms. The release gate for 1.0 is `pairtime doctor --report` from at least 5 pre-2017 Windows laptops showing a warm hook round trip under 50 ms | 3.12, 9.7 |
| R9 Trustworthy | The credit rule zeroes headless loops, `/loop` and `/goal` runtime, jigglers and parallel sessions (SM 6.4, 6.6). Side doors (`track`, plugins, the bridge, editor-extension claims) earn level C on keyboard-blind machines unless corroborated. Witnessing with 60 s batches, signed receipts and a public day root; commits as anchors; CC1 to CC11 with held minutes; a sticky challenge; an offline cap; bands that never pre-approve level B, offline or backfilled minutes; a published audit formula that rises with claimed hours | SM 7.3's R9 test, approved over credited hours with mean ≥ 0.9, measured in the native program. Red-team fixtures with expected credit run as CI goldens, including a `track` loop with a jiggler, forged plugin sizes, forged editor events, an offline-flush fabrication and withdraw-and-redraw (JT graft 5) | 6.4, 7 |
| R10 Auditable | A frozen submission, a signed bundle with no revealed text, and `pairtime verify` offline: it rebuilds roots, checks receipts and day roots, recomputes the draw from the committed `r`, and re-runs scoring. The bundle's SHA-256 is in the justification text, so a spot-checker knows which evidence the reviewer read (EV 6.6) | A CI job runs `verify` on an end-to-end submission with networking disabled (`unshare -n`); a one-byte change in any file fails it. A stdlib-only Python reference computes the same roots for 1,000 random batches | 7.8 |
| R11 Cost | The client scores and the server stores derived rows. No heartbeats and no LLM calls on the server. One Hetzner CCX13 with object storage is $48.94 a month; with a warm standby $149.68; with code signing about $168, 34% of the cap | `loadtest.yml` replays the 8x R11 peak against `pairtimed` limited to 2 vCPU and 8 GB: p99 ingest under 250 ms and no writer backlog | 10.8 |
| A Acceptance | pairtime's credited figure is the program's hours number on every counted row (9.2). Three adoption tiers that work under today's handbook; a mechanical counting rule; a native program with a funded fallback; existing programs fighting the one-third rule; low-ceremony Airtable programs. The plan targets 1,200 weighted projects, not 1,000 | HQ's count of `approved_projects` rows whose justification starts with "Tracked with pairtime", reconciled weekly against pairtime's own outcome ledger (9.8) | 9 |

## 3. Architecture

### 3.1 Shape

Two Go binaries from the one module PR1 created, `github.com/some-du6e/pairtime`: `pairtime` on the teen's machine and `pairtimed` on Karim's server. They share `pkg/event`, `pkg/params`, `pkg/ledger`, `pkg/score` and `pkg/check`, so the server re-runs exactly the scoring the client ran (SP 7.1). The client sees everything and scores and coaches. The server sees numbers, hashes and whatever the teen publishes or submits, and it witnesses, rolls up, streams and turns submitted windows into evidence.

```text
 TEEN'S MACHINE (pairtime)                                 KARIM'S DOMAIN (pairtimed behind cloudflared)

 harness hooks --exec--> pairtime hook --+                  ingest: device signature, chain link, receipt
 AGENTS.md entry ------> pairtime track -+ socket           witness log (device, b, n, R_b, H_b, received_at)
 harness logs (tailers) -----------------+                  rollups (additive) --> SSE dashboards
 Claude Code OTLP/JSON --127.0.0.1-------+                  accounts (HCA), devices, projects, commits, forges
 wakatime-cli --[api_urls]--127.0.0.1----+--> agent         profiles, devlogs, build days, clubs
 OS presence / focus / power / procs ----+    ledger+vault  submissions: reveal, re-score, sticky challenge
 .git watchers --------------------------+    scorer        review desk, Fraud Squad desk, program API,
                                              coach         Hackatime-shaped reads, handbook text, webhooks
 wakatime-cli --api_url--> Hackatime          uploader ===> bundle signer, evidence store (sealed content)
   (unchanged, as today)                      local UI      privacy hub endpoints, encrypted backups
                                                            SQLite WAL --Litestream--> object storage
 tier 1 every 60 s: buckets, commits, roots                  once a day: day root --> public repo pairtime-witness
 tier 2 only on submit: metadata, then sealed content
```

### 3.2 Client processes

One signed binary with subcommands, so there is one thing to ship, sign and update (CP 7).

| Process | Started by | Does |
|---|---|---|
| `pairtime agent` | Login: HKCU Run key, a LaunchAgent, or XDG autostart, none needing admin (CP 2.1); or a hook that finds it down. Single-instance lock: a named mutex on Windows, `flock` elsewhere | Everything in 3.3. Opts into EcoQoS on Windows and leaves `ProcessType` at `Background` on macOS (TE 3.2) |
| `pairtime hook --harness <h> --event <e>` | A harness, in exec form | Reads stdin up to 1 MiB, stamps arrival time, records its parent chain, sends one framed message over the agent's socket with a 100 ms timeout, else appends one line to `inbox/` and starts the agent detached. Always exits 0 and prints nothing (CP 6.2) |
| `pairtime track` | The coding agent, following Karim's AGENTS.md entry | Same path as `hook`, with `source = cli` (5.4) |
| `pairtime emit --source plugin:<name>` | An out-of-process plugin | Reads SM 1 events as JSON Lines on stdin and forwards them over the socket (5.3) |
| `pairtime install`, `uninstall`, `doctor`, `login` | The user | Install and self-test (5.9); `login` signs a local-only device in later without losing history |
| `pairtime dashboard`, `recap`, `story`, `devlog`, `statusline` | The user, or the teen's own agent through the coach skill | Open the local UI with a one-time token; print recaps, stories and devlog drafts as text or `--json`; print a one-line total for terminal status lines |
| `pairtime submit [--dry-run]`, `privacy show`, `ledger show`, `backup`, `verify` | The user, or anyone for `verify` | Submission (7.5); the next upload byte for byte (TP B8); every recorded record in plain JSON; backups (7.11); offline verification (7.8) |

Detached means a process that the hook's session, terminal and job can't end: on Linux, a transient scope of the user's systemd manager when one runs; elsewhere, a session (Unix) or a process group with no console window (Windows) of its own, outside the caller's job when the job allows breakaway.

### 3.3 Inside the agent

| Component | Trigger | Output |
|---|---|---|
| `presence` | Every `SAMPLE_S` = 15 s while a project context is live: a harness session process is running, a terminal, editor or agent app had focus in the last 600 s, or an editor heartbeat that passed the peer-process check (5.7) arrived in the last 600 s (EV 3.3, D10). After 600 s with no input and no running agent turn, one sample every 60 s. Any hook, editor heartbeat, focus or power event takes a sample at once. Outside a project context, nothing is sampled or recorded | `os.presence`. Idle APIs return time since the last input, so a sample after a pause reconstructs the gap exactly and the 10 s check before a prompt (SM 2.2 rule 6) always has data. Gaps come from wall-clock values, never `time.Since` (CP 2.2) |
| `focus` | Event-driven: `SetWinEventHook(EVENT_SYSTEM_FOREGROUND)`, `NSWorkspace` activation notifications, X11 `_NET_ACTIVE_WINDOW`, and on GNOME on Wayland pairtime's Shell extension (5.9). Wayland gives other programs no focus signal of its own, so focus is unknown on GNOME without the extension, on KDE and on wlroots; it is also unknown on X11 with no `_NET_ACTIVE_WINDOW` and while an X11 connection is re-made, and wherever a focus source failed to start | `os.focus` with `app_class` and `hosted_sessions` from a process-tree walk on each focus change. Each time focus becomes unknown, an `os.focus` with `app_class` `unknown` and no `app_hash`, `hosted_sessions` or `focus_project`, whose slots credit only at level C (SM 2.5). Window titles are never read |
| `power` | `PowerRegisterSuspendResumeNotification` and session-lock notifications, IOKit sleep, logind `PrepareForSleep` | `os.power`; suspend and lock seal a batch and upload at once |
| `procs` | Every `PROC_SCAN_S` = 60 s and on focus change | `session.start` for `--bare` runs that fire no hooks, and each session's `parent_class` (SM 1.2) |
| `tailers` | File notifications plus a slow `stat` poll of hot files (CP 3.2) | Raw records for each adapter's `Parse`, with a cursor per file |
| `apps` | Every 10 s while an agent app runs, which for T3 Code is while its store's `-wal` file exists, and at once when a hook prompt arrives from a session T3 Code's server started | `relay.message` records from the app's own store (5.1), and which runs a guest's message started, so the prompts they deliver keep no text. A message's text is read only to measure and class it, and then dropped |
| `gitwatch` | A non-recursive watch on each known repo's `.git` (`HEAD`, `index`, `ORIG_HEAD`, `logs/HEAD`) | `git.change` and commit records |
| `ipc` | The per-user socket (AF_UNIX on all three OSes, with a named-pipe fallback behind a test on Windows 10 1809 and 21H2; CP 6.3; JE graft 16) and `127.0.0.1` HTTP with a bearer token from `port.json` and `Host` and `Origin` checks | Hook and track messages, OTLP/JSON, WakaTime heartbeats, plugin events, the local UI's API |
| `ledger` | Every event | Appends records and seals batches (3.6) |
| `scorer` | Every 60 s and after each prompt or stop hook | Minute buckets in `pending` or `final` state (3.7) |
| `coach` | Work-session end, nightly, weekly | Recaps, moments, logbook, stories, drafts (3.8) |
| `uploader` | Every 60 s while there is anything new, and within 10 s after a prompt or stop hook, coalesced | Signed tier-1 batches (4.3) |
| `notifier`, `tray` | Recap ready, weekly story | One notification per work session, never a polling loop. Tray on Windows and Linux through `fyne-io/systray`, whose Windows and Linux code builds without cgo according to its source (CP 5.3; to confirm in its WP). macOS gets no menu-bar helper in v1; Mac users get the dashboard and `pairtime statusline` (AD 3.3) |
| `updater` | Daily | Signed manifest check and swap (CP 4.6), never to a binary that fails to run on this machine (3.12); skipped when a package manager owns the binary |

### 3.4 Adapters

An adapter turns what one tool exposes into SM 1 events and never touches the scorer, so a new harness is an adapter-only change (SM 1). The agent owns all I/O: the hook receiver, the tailers and the HTTP routes deliver raw records, and the adapter's `Parse` is a pure function over them (TE 3.3; JE graft 2):

```go
type Adapter interface {
    Name() string                               // "claude_code", "codex", ...
    Detect(env Env) Detection                   // installed? which version? which config paths?
    Plan(env Env) []ConfigEdit                  // idempotent install edits; Revert uses their inverse
    Sources() []Source                          // hook events, log roots and globs, HTTP routes
    Hosts() []ProcessMatch                      // executables that host its sessions, for focus attribution (SM 3.2)
    Parse(raw RawRecord) ([]event.Event, error) // pure; pinned per harness version
    Fixtures() fs.FS                            // captured inputs and expected events per supported version
}
```

Every event an adapter emits carries a provenance value, `via`, which the scorer uses to set its evidence level (6.4): `hook` (delivered by `pairtime hook` or `pairtime emit` whose parent chain reaches a recognized harness process), `log` (read from a file the harness writes), `otel`, `os`, `git`, `editor` (an editor heartbeat that passed the peer-process check in 5.7), `editor_unverified` (one that didn't), `bridge` (prompt or tool events derived from wakatime-cli's AI heartbeats), `track`, `plugin` (events posted over HTTP or the inbox), and `app` (messages read from an agent app's own store, 5.1). `via` is set by the agent from how the record arrived, never by the adapter or the sender.

### 3.5 Local store

The client doesn't link SQLite. Every store is an append-only file or a small JSON file written by atomic rename. This is a deviation from all three designs: it keeps the client under its 15 MB budget with room for the embedded UI, avoids schema migrations on teens' machines, and suits a spinning disk, because every write is an append (JE must-fix 4). The server links `modernc.org/sqlite`.

Paths follow CP 7: `%LOCALAPPDATA%\pairtime\`, `~/Library/Application Support/pairtime/`, `${XDG_DATA_HOME:-~/.local/share}/pairtime/`.

| Path | Format | Holds | Kept |
|---|---|---|---|
| `ledger/YYYY-MM-DD.seg` | Length-prefixed records `{hdr, body}` (3.6) | Every event's shareable fields, keyed scorer features and content commitments; scorer checkpoints; upload-failure notes. No text | 180 days, the submission horizon (TP B8) |
| `ledger/batches.seg` | `{b, first_seq, last_seq, n, R_b, H_b, sealed_at, receipt}` | Batch roots, the chain, the server's signed receipt per batch | 180 days, or until every submission using them is past its spot-check window. A batch's row goes when its batch's segment goes, and a base frame then names the first batch the file keeps, or the next one to be sealed when it keeps none. While account.json links the computer, the rows of the online batches the server hasn't stored stay until it stores them, unless a fork or the server's device_mismatch has ended the chain, without their segments, which go at 180 days all the same (docs/errata/2026-11-23-unsent-batches-retention.md) |
| `vault/YYYY-MM-DD.seg` | Per-record AES-256-GCM under `K_vault` | Prompt text and each turn's final agent message (first 16 KiB); the teen's choices for each drawn item of a content reveal, until the server has stored it (7.6); file paths for local recaps | Text and reveal choices: 90 days, or until a submission covering them is decided, whichever is later (TP B8). Paths: 30 days. The teen can shorten both |
| `buckets/YYYY-MM-DD.jsonl` | Minute buckets with revisions | The scorer's output (SM 5.1) | 180 days |
| `commits/YYYY-MM-DD.jsonl` | JSONL, a file per UTC day of `observed_at` | 4.3's commit records, which the git adapter's sink appends before the watcher's cursor moves past the change: tier-1 fields only, with no commit message and no path | 180 days, like the minute buckets. The uploader sends them from here |
| `days/YYYY-MM-DD.json` | Per-project day rollups | Credited and uncredited seconds by reason and kind, agent-only time, evidence shares | Until the teen deletes them |
| `coach/` | JSONL and JSON | Work-session recaps, moments, logbook, habit weeks (opt-in), devlog drafts, recap feedback | Until the teen deletes them; a moment detail that quotes a prompt goes at 30 days (TE 3.4) |
| `inbox/` | JSONL | Hook calls made while the agent was down; plugin events | Deleted once drained into the ledger |
| `held.jsonl`, `held.jsonl.pending`, `held.jsonl.replay` | JSONL, each line a hook call with an HMAC under a key the agent's run keeps in memory | Hook calls the agent hasn't parsed yet: acked and not yet parsed or held (`.pending`), waiting for the keystore and the ledger (`held.jsonl`), being replayed (`.replay`). Whole payloads, prompts included, kept as `inbox/` keeps its own: a folder and files only this user can reach, opened with `O_NOFOLLOW` where the spool is | Deleted once drained. A call is kept 7 days at most, and no new call is held once the ledger can't open until `pairtime restore` runs |
| `state.json` | JSON, atomic rename | Scorer state, tailer cursors, agent-app reader cursors (5.1), upload cursor, project map, consent answers | Rebuildable from the ledger and the harness logs |
| `uploads.jsonl` | JSONL, appended | Each upload pairtimed answered: when, to which server, how many batch roots, minute bucket revisions and commit records it carried and whether it carried the labels, the result codes, and the SHA-256 of the body `pairtime privacy show` printed, never the body. Each project names request (4.3) the uploader sent, answered or not, the same way: when, to which server, how many names it showed and stopped showing, the answer's status and code or why none came, and the SHA-256 of its body, never a name or a project key | 90 days |
| `outbox.json` | JSON, atomic rename | The body of the request that waits for its retry, as `pairtime privacy show` printed it, so the retry sends the same bytes with the same `Idempotency-Key` | Until its answer comes |
| `project-names.json` | JSON, atomic rename, user-only permissions | The server's last answer to this computer's names requests (D-owner-names): the server and device id it answered, the label it shows by project key, its short code for each key, when it answered, the keys it still lists as hidden, the names and keys requests carried since the server last answered, until an answer settles them, and for each name hidden on the dashboard, no longer shown, or on at a sign-in to another server or when the file didn't read, its label, when pairtime found it, why, and whether it could turn it off in config.toml | Until the next answer replaces it; a hidden name's note until the server shows the name again after the teen turned it on, 1,000 notes at most; a sign-in to another server or device keeps only the notes; deleted by `pairtime uninstall` and "delete everything" (8.8) |
| `config.toml`, `port.json` | TOML; JSON with user-only permissions | Settings, `.pairtime-ignore` patterns, opt-ins; the local port and bearer token | |

The split between ledger and vault is the point (EV 3.4). The ledger is what gets proved and the vault is what gets revealed. Deleting the device key `K_dev` makes every content commitment unopenable (crypto-shredding, TP B6). The vault's encryption protects against other users of a family PC, cloud sync of the home folder and casual reading. It doesn't protect against malware running as the same user, and the privacy page says so (CP 5.2).

### 3.6 The ledger

This is EV 6.2's construction, put in M0 before any witnessed data exists, because the format can't change without breaking old bundles (JB graft E1). Three changes from EV: bodies are canonical JSON from `encoding/json` rather than CBOR, which keeps the module standard-library only (PR1 AGENTS.md); scorer checkpoints are ledger records, so the server can re-score a window exactly (6.5); and the Merkle trees are pinned the way TP's fact-check asks (TP B6, row 32).

```text
Install:   K_dev   = 32 random bytes in DPAPI, Keychain or Secret Service; a 0600 file with a visible warning if none
           K_leaf  = HKDF-SHA256(K_dev, info = "pairtime/v1/leaf")
           K_tok   = HKDF-SHA256(K_dev, info = "pairtime/v1/token")
           K_feat  = HKDF-SHA256(K_dev, info = "pairtime/v1/feat")
           K_vault = HKDF-SHA256(K_dev, info = "pairtime/v1/vault")

Record i, with ledger sequence number s_i:
  hdr_i   = s_i (8 bytes, big-endian) || rtype (1 byte) || project (16 bytes) || session (8 bytes)
            rtype: 0x01..0x12 = the 18 kinds in PR1 kindTable order; 0x13 = relay.message (4.1 E-12); 0x40 = checkpoint; 0x41 = upload failure
            project = first 16 bytes of SHA-256(project_key), zero when the record has none (all os.* events)
            session = first 8 bytes of SHA-256(session_key), zero for "none"
  nonce_i = HMAC-SHA256(K_leaf, 0x01 || s_i)
  for a prompt, or a turn's final agent message:
      tokens w_1..w_n split on whitespace, separators kept
      t_ij  = SHA-256(0x00 || HMAC-SHA256(K_tok, s_i || j) || w_j)
      C_i   = the RFC 9162 tree over t_i1..t_in, used as precomputed leaf hashes
  body_i  = canonical JSON {"n": nonce_i, "e": event.Shareable(), "f": features, "c": C_i (if any)}
  entry_i = hdr_i || SHA-256(body_i)

Batch b, sealed every BATCH_S = 60 s while there are new records, and at once on lock, suspend, prompt and stop:
  R_b = MTH(entry_1 .. entry_n)          RFC 9162 section 2.1.1; MTH applies the 0x00 leaf prefix itself
  H_b = SHA-256(0x03 || H_(b-1) || R_b || n_b (8 bytes) || b (8 bytes))
Server stores (device_id, b, n_b, R_b, H_b, received_at) and returns
  receipt_b = Ed25519(server_key, "pairtime-receipt-v1" || device_id || b || H_b || received_at)
```

Rules that go with it:

- **Body bytes are stored verbatim.** Verification hashes the stored bytes, so no one ever has to re-encode JSON identically. The writer still emits canonical JSON (struct field order, no insignificant whitespace), and a test checks that decode then encode is byte-identical.
- **The nonce** makes every hidden body unguessable, even a presence sample. **The header** is revealed for every record in a revealed batch, so the server can demand full bodies of every presence, power and focus record, and a modified client can't hide a lock event by labeling it with another project (EV 6.2).
- **Features `f`** are the keyed, content-derived scorer inputs the server needs to recompute dark-flow discounts without text (EV 6.2): for `prompt.submit` a 64-value MinHash over shingles hashed with `K_feat`, `HMAC(K_feat, error_sig)`, `HMAC(K_feat, template_hash)`, and `prompt_class` as computed; for `agent.tool` the edit hunks as `(HMAC(K_feat, path), start line, end line)` and `HMAC(K_feat, cmd_hash)`; for `shell.command` `HMAC(K_feat, cmd_hash)`. The map is versioned (`f.v`). A parameter refit never touches it. A new rule that needs a new feature adds a key and applies only to records that carry it (6.6; JE must-fix 6).
- **Checkpoints** (`rtype 0x40`): at every UTC minute divisible by 15, for each project with records in the last 24 hours, the scorer seals its serialized `score.ProjectState` for that project (6.5).
- **Upload-failure notes** (`rtype 0x41`): the uploader records each failed upload with a reason code (`dns`, `connect`, `tls`, `http_5xx`, `http_429`). They support the `offline` label (7.4) and are the client's own claim.
- **A batch's mode.** Each seal frame records whether its batch was sealed online and, for one sealed local-only, why: `config.toml` said local-only as it was sealed, a record came while it did, something made in a local-only stretch was still on its way, or the batch holds records of a stretch that came late, named by their kinds and their own times, never their content. The writer keeps every local-only stretch it knows in the seal frames, from the last moment it knew `config.toml` said online to the moment it saw the file online again, and judges a record that reaches it late by the record's own time against them. A stretch whose edges share no monotonic clock, as one left open across a reboot, holds what the wall clock alone dates from the end of the stretch before it. A retention sweep folds every stretch that began before its day into one from the beginning of time to that day or to the latest end among them, whichever is later, or into one still going on while one of them does, which only makes more of the past count as local-only (4.4). A record a log reader read (`via = log`), dated before the chain began and with no reading of a run's clock, counts by when the writer took it in; any other record counts by its own time (4.2; docs/errata/2026-11-23-late-local-records.md).
- **Error signatures** on `agent.tool` and `shell.command` are opaque keys in PR1's model. Adapters fill them with `HMAC(K_feat, signature)` truncated to 16 bytes, so revealing them leaks nothing a guess can test.
- **Signed receipts** make witnessing two-sided: the server can't later deny or re-date a head the teen holds a receipt for, and the teen can't claim an earlier time than `received_at` (EV 6.2).
- **Test vectors** in `testdata/ledger/vectors.json` are checked by `tools/ledger_ref.py`, a stdlib-only Python reference, over 1,000 random batches (EV WP1.1; JE graft 12).

Cost: a few SHA-256 and HMAC operations per event, one Merkle root a minute, and about 150 bytes of root data per upload (TP B6).

### 3.7 The scorer on the device

`pkg/score` implements SM 2 to 5 as one deterministic state machine, `Step(state, records) (state, buckets)`, with no I/O, no clock reads and integer arithmetic only: times in milliseconds and credit in half-second units, converted to PR1's float fields only at output, so a re-score matches to the bit on every platform (EV risk 9; JE graft 13). The state is serializable, which gives the client cheap incremental scoring and gives the server exact re-scoring from checkpoints (6.5).

The agent steps the scorer every 60 s over new ledger records. A minute stays `pending` for `PENDING_WINDOW_S` = 900 s after it ends (TE D6; AD D1). Every rule decides a second from what is on record within `READ_HORIZON_S` = 600 s after it: a lead reaches back at most 420 s before its anchor, reading needs an anchor within 600 s, and the return rule an anchor within 600 s of the start of the silence (SM 2.1). An act is on record at the later of its own time and its record's `t`, and it changes nothing more than 600 s before then. So a typed command, whose record is written when it ends, starts to lose its lead once it runs longer than nine minutes, and a permission answer recorded more than eight minutes after it starts to lose its wait. The other 300 s are for late events (bridge heartbeats, transcript lines, an inbox drained after an agent restart) and for a sample slowed to one a minute (D14). Late events re-score the minute, and the uploader sends a new revision that supersedes the old one on the server, keyed `(device_id, minute)` with a `rev` counter. After 900 s a bucket is `final`, apart from SM's own `provisional` and `restored` states. Three reaches are bounded by the pending window rather than by `READ_HORIZON_S`. SM 4.2's rewrite rule reads a turn's edits on record up to `READ_HORIZON_S` - `THINK_LEAD_S` - `COMPOSE_MAX_S` = 180 s after its prompt, so the reading and return credit an anchor decides before itself (SM 2.1's return rule, SM 2.4's draining within `READ_HORIZON_S` of a human anchor) is decided up to 780 s after the second when the anchor's turn is a rewrite candidate: inside the pending window, with 120 s left for late records. A relayed lead waits for its pairing up to `RELAY_MATCH_S` after `sent_at`, up to 900 s after the lead's first second with no margin for late records (SM 2.3, SM 8's `RELAY_MATCH_S` row), and a stop's relayed reading waits for a pairing within 840 s of the stop (SM 2.4). One part of SM still lacks a bound: a lookup (SM 2.5) is decided only when its browser run ends, however long the run lasts, so its minutes stay pending until then.

### 3.8 The coach

The coach carries R3. It is a set of deterministic rules over the scorer's output and tier-0 events. It runs on the device, uses no model, and calls nothing on the network (TE 3.6). Each output and when it ships:

| Output | When it runs | What it is | Ships in |
|---|---|---|---|
| Chess clock | Live | "You 1h 12m, Claude 2h 40m" per project and day, as information, never as a target ratio (PA 4.3) | Alpha (M1) |
| Welcome recap | First run, after lookback | The last 30 days from transcripts, labeled level C | Alpha (M1) |
| Work-session recap | No credited minute for `SESSION_GAP_S` = 1,200 s, or a lock or suspend | Your time by kind, the agent's time, moments, loops caught with what ended each one, evidence-level shares, and a "Was this right?" control (TE 2, R2) | Alpha (M1) |
| Moments | Live | `first_commit`, `first_green_test`, `first_test_written`, `rejected_agent_edit`, `interrupt`, `fixed_by_hand`, `long_fight` (an error alive for at least `LONG_FIGHT_MIN_S` = 1,800 credited seconds before it resolved), `loop_caught`, `comeback` (half-weighted minutes restored by progress), `new_language`, `new_tool` | Alpha (M1) |
| Build days | Server-confirmed, shown locally and on the profile | 6.2 D10: a day with at least 900 credited seconds at level A or B that the server received within 72 hours; a weekly goal (default 3), weeks in a row, freezes | Alpha (M2, once uploads exist) |
| Logbook | Nightly | Error families beaten (`TypeError`, Rust `E0502`, `FAILED <test>`), languages, tools, firsts | M5 |
| Project story | Continuously | Moments, commits, devlogs and credited time on one timeline, with a "before pairtime" band for lookback | M5 |
| Devlog draft | Weekly, or on "New devlog" | A Markdown outline of facts since the last devlog, the teen's own commit subjects, the long fight and how it ended, and two reflective prompts. The sentences are the teen's | M5 |
| Habit insights | Weekly, only if the teen opts in (8.4) | Verification share, hand-edit share, rejections and interrupts per hour, and `asked_why` moments from local prompt-kind rules, each week compared only with the teen's own previous weeks | M5 |

Recap copy follows PA 4.4 and TE D5. Credited time is "your time". Agent-only time is named after the agent that ran. Other uncredited time is described for what it was. Half-weighted minutes show as "on hold: these come back when you fix it", and their restoration as a `comeback` moment. Every loop message ends with what ended the loop, so it reads as a lesson.

The fun parts carry no prize (TE 6.2; JT graft 6). Build days, badges, boards, the logbook and devlogs never change credited seconds, and gamified surfaces count only witnessed level A or B minutes. A modified client can fake a badge, and the only reward is a badge.

### 3.9 Local UI

The agent serves a Svelte app embedded in the binary at `127.0.0.1:<port>`, opened in the teen's own browser through a one-time URL that sets a `SameSite=Strict`, `HttpOnly` cookie. Every request needs a loopback `Host` and the dashboard's own `Origin` (CP 6.3). Pages: Today, Recaps, Projects (stories), Logbook, Devlogs, Build days, Privacy (what left this laptop, retention, delete everything), Settings (adapters found with last event time and parse-failure count, opt-ins, quiet hours, project names and visibility, exclusions). Nothing with a webview stays resident (CP 5.3).

### 3.10 Server

One Go process, `pairtimed`, with the Svelte UI embedded, SQLite in WAL mode replicated by Litestream, behind `cloudflared` on Karim's domain (SP 1, 9.1). The SQL stays portable so Postgres remains the scale-out path (SP 7.4, 9.5).

| Component | Endpoints | Notes |
|---|---|---|
| Accounts | `GET /auth/hca/login`, `GET /auth/hca/callback`; `/device`, `POST /oauth/device/code`, `POST /oauth/token` | HCA OIDC with `pairtimed` as the confidential client, requesting `openid email profile slack_id verification_status` by name, since discovery lists only `openid profile phone birthdate` (SP 3.1). `hca_sub` is the identity key. The desktop signs in with the RFC 8628 device grant and gets a `ptd_` device token with a CRC32 checksum, then registers an Ed25519 device key; it never holds an HCA token (SP 3.5, 8.3) |
| Ingest and witness | `POST /api/v1/ingest/batches`, `GET /api/v1/ingest/state`, `GET /api/v1/time` | Device signature (4.5), chain link (`prev` equals the stored head), dedupe on `(device_id, b)`, partial acceptance, the free-text firewall, size limits, a signed receipt per batch. One writer goroutine commits every 50 ms or 500 rows (SP 7.4) |
| Rollups and live dashboard | `GET /api/v1/stream`, `GET /api/v1/users/me/dashboard` | Additive upserts into `rollups_daily (user, project, day, activity_kind)` from bucket revisions; SSE with 15 s keepalives, `Last-Event-ID` resume and a polling fallback. `profile:` topics carry coalesced hourly totals (SP 6.4) |
| Cross-device merge | inside ingest | One human minute per wall-clock minute per account (SM 3.4). Ingest stores no flag for a conflict. The submission freeze computes `concurrent_devices` over the submitted window, from the per-device minutes `rollup_minutes` keeps for 180 days, because only submitted windows are checked for fraud (4.2, 8.5; D-no-ingest-flags) |
| Profiles and projects | `/@:username`, `/@:username/:project` | Aggregates only (8.3) |
| Git and forges | commit records in ingest; `POST /api/v1/forge/github/webhook` | `server_first_seen_at`; one commit lookup per SHA on GitHub, GitLab and Forgejo for public repos; an optional GitHub App with Metadata and Contents read, whose push payload path arrays are dropped on receipt for private repos (EV 4.6) |
| Teen layer | `POST/GET/DELETE /api/v1/devlogs`, `GET /api/v1/clubs/:id/board` | Devlog Markdown the teen published, `ai_assisted`; build days; club boards over 90 days, visible to signed-in members, opt-in (TE 3.8) |
| Submissions and evidence | `POST /api/v1/submissions`; `POST .../:id/reveal`; `GET` and `POST .../:id/challenge`; `POST .../:id/withdraw`; `GET .../:id`; `GET .../:id/bundle`; `GET .../:id/reveals`; `POST .../:id/purge`; `POST .../:id/exposure` | Section 7.5 to 7.8 |
| Review | `/review/submissions/:id`, `POST .../:id/review`, `POST .../:id/outcome`, `/review/fraud` | HCA sign-in plus a role from pairtime's allowlist of HCA subs, because a community HCA app can't read `groups` (SP 3.1, OQ 1). Fraud Squad's desk searches across submissions by device, shared root commit, overlap and, within 30 days, IP and JA4 |
| Program API | `/oauth/authorize`, `/oauth/token`; `GET /api/v1/submissions?updated_since=`; `GET .../:id/justification?format=handbook`; webhooks | OAuth clients with scopes `profile:read`, `email:read`, `hours:read`, `submissions:read`, `submissions:write`. Webhooks signed `X-Pairtime-Signature: sha256=<hex HMAC-SHA256 of "<X-Pairtime-Timestamp>.<body>">` with a 300 s tolerance, Robert's scheme (SP 10.2) |
| Hackatime-compatible reads | the calls in 5.10 | Answer only for users connected to the calling program |
| Privacy hub | `POST /api/privacy/delete`, `POST /api/privacy/export` | Hack Club's ES256 JWT contract with pairtime's own `aud` (TP B3, DI 27) |
| Backups | `PUT /api/v1/devices/:id/recovery`, `PUT /api/v1/devices/:id/backup` | Opt-in ciphertext only: a wrapped `K_dev` and an encrypted ledger and vault backup (7.11) |
| Publisher | `/.well-known/pairtime-keys.json`; a daily job | At 00:10 UTC, one Merkle root over every `(device_id, b, H_b, received_at)` received that UTC day, chained to the previous day and signed, committed to the public repo `pairtime-witness` (EV 6.3) |
| Ops | `/healthz`, `/readyz`, `/metrics` on a private port | 10.4 |
| Sentinel (optional) | Unix socket, `POST /score` | A private process for detectors beyond SM 8.7's set, called with a narrow JSON protocol defined in `internal/server/sentinelapi`. Off unless Hack Club deploys it (7.2) |

### 3.11 Repo layout

PR1's layout stays. New paths are added beside it:

```text
cmd/pairtime/              client binary (PR1 stub): agent, hook, track, emit, install, uninstall, login, doctor,
                           dashboard, recap, story, devlog, statusline, submit, privacy, ledger, backup, verify
cmd/pairtimed/             server binary (PR1 stub): serve, init, migrate, healthcheck, doctor
internal/version/          build info (PR1)
pkg/event/                 the event model (PR1), with the changes in 4.1
pkg/params/                the public parameter table, canonical JSON, SHA-256
pkg/ledger/                headers, bodies, nonces, token commitments, batches, chain, receipts, proofs, day trees
pkg/score/                 SM 2-5 as a resumable state machine
pkg/check/                 CC1-CC11
pkg/bundle/                bundle format, manifest signing, offline verification
pkg/redact/                known values, gitleaks rules as data, validated PII patterns, code-block collapse
pkg/seal/                  ECDH P-256, HKDF-SHA256 and AES-256-GCM sealing for tier-2 content
pkg/wakacompat/            heartbeat schema, the key regex, Hackatime JSON shapes
internal/agent/            scheduler, ledger writer, scorer loop, uploader, ipc, notifier, tray, updater
internal/platform/{windows,darwin,linux}/   presence, focus, power, procs, autostart, keystore, notify, typing, peer, vm
internal/adapters/         claudecode, codex, cursor, copilot, gemini, devin, cline, otlp, wakatime, git, track, plugin, generic
internal/install/          per-harness planners and mergers, the ~/.wakatime.cfg editor, the AGENTS.md writer, file backups
internal/coach/            recaps, moments, logbook, stories, devlog drafts, habit insights
internal/webui/            go:embed of the built UI, with a checked-in placeholder so go build needs no Node
internal/server/           store, ingest, witness, rollup, stream, auth, device, profile, project, forge, devlog, club,
                           submission, reveal, review, fraud, program, hackatime, justification, privacy, backup,
                           publisher, sentinelapi
web/                       Svelte 5 and TypeScript, built by Vite: local dashboard and server UI, shared components
extension/vscode/          install channel and status bar; per-platform VSIX packages bundle the binary
extension/gnome-shell/     GNOME Shell extension in GJS: the process of the window in front, for the agent only (3.3, 5.9)
integrations/              program kit
agents/                    Karim's AGENTS.md entry; the coach skill
sdk/plugin/                plugin spec, conformance kit, example plugin in Python
tools/ledger_ref.py        stdlib-only Python reference for pkg/ledger
testdata/                  SM 6 timelines, captured harness fixtures with provenance, red-team fixtures, ledger vectors,
                           WakaTime captures, Stardance-shaped request captures
deploy/                    compose.yml, litestream.yml, cloudflared config, runbook.md
docs/schema/               event.schema.json (PR1)
.github/workflows/         see 3.13
```

### 3.12 Languages, toolchain and dependencies

- **Go 1.26 for both binaries until Go 1.28 ships in February 2027, then Go 1.27**, `CGO_ENABLED=0`, cross-compiled from one Linux job to PR1's seven targets. From then on the `go` directive follows the oldest Go release still supported (13.1 item 8; JB must-fix 11). Go 1.27 needs macOS 13, as Claude Code already does (CP 1.5, 4.1), so Macs that top out at macOS 12, the 2015 and 2016 models among them, keep the last Go 1.26 build. Nothing that installs or updates pairtime puts a binary in place before running its `version` (docs/spec/update-manifest.md, Verifying step 8): the updater and the VS Code extension keep the binary they have when that fails, a client that can't update keeps working and `doctor` says why, and `install.sh` gives macOS 12 the last Go 1.26 release. macOS 12 support ends when `minimum_version` is raised past that release. Staying on Go 1.26 would leave `pairtimed`, which holds minors' data, on an unpatched toolchain and pin every module at the last versions that build on 1.26, `golang.org/x/sys` first.
- **Svelte 5 with TypeScript**, built by Vite, embedded with `embed`, API types generated from an OpenAPI file (SP 7.3). `internal/webui` embeds `dist/`, and the repo carries a placeholder `dist/index.html`, so `go build` and every Go-only CI job run without Node. `release.yml` and `web.yml` build the real UI first (JE must-fix 8).
- **TypeScript** for the VS Code extension, **PowerShell and POSIX sh** for the install scripts, **JavaScript** for the Airtable automation script and, run by gnome-shell's GJS, the GNOME Shell extension, and **stdlib-only Python** for the ledger reference.
- **Dependencies.** Standard library first, as PR1's AGENTS.md says; each module's PR states why the standard library falls short, and the first such PR adds a transitive license check to CI. The allowed list, all cgo-free and permissive (CP 7): `golang.org/x/sys` (Win32 and Unix calls), `github.com/ebitengine/purego` (macOS frameworks), `github.com/godbus/dbus/v5` (Linux idle, logind, notifications), `github.com/fsnotify/fsnotify` (file notifications), `github.com/zalando/go-keyring` (Keychain and Secret Service), `fyne.io/systray` (tray, if its WP confirms a cgo-free build), and `modernc.org/sqlite` for `pairtimed` only. All cryptography is standard library: `crypto/ed25519`, `crypto/ecdh`, `crypto/hkdf`, `crypto/hmac`, `crypto/sha256`, and AES-256-GCM through `crypto/aes` and `crypto/cipher`. That replaces the XChaCha20-Poly1305 the designs named, so `golang.org/x/crypto` isn't needed.
- **Size decisions made now** (JE must-fix 4): the OTLP receiver accepts `http/json` only, which Claude Code supports, so there is no protobuf dependency (HS 16 item 4). The Codex rollout reader handles `.jsonl`; `.jsonl.zst` files are counted and reported by `doctor`, and a zstd decoder is added only if Codex turns compression on by default (HS 6.2).

### 3.13 CI layout

GitHub Actions is the only build and test environment, and its capacity is shared with another busy project (PR1 AGENTS.md). The rules (JE must-fix 3):

- `ci.yml` stays PR1's single `ubuntu-latest` job: license gate, gofmt, vet, `go test ./...`, the seven-target cross-compile with the 15 MB budget. New Linux checks join it as steps, except a check that needs a `services:` container, such as Postgres or MinIO, which gets a path-filtered workflow of its own. The SM fixtures, the `score-regression` check and the Python ledger reference run here.
- Windows and macOS run only in `platform.yml`, path-filtered to `internal/platform/**`, `internal/install/**`, `internal/agent/ipc/**` and `cmd/pairtime/**`, and on release tags. Platform tests use injected fakes on hosted runners; real focus switching runs only on Linux under Xvfb with `xdotool` (JE must-fix 11).
- `-race` needs cgo, so it runs only in `nightly.yml` on Linux, for `internal/agent` and `internal/server`. The PR that adds it adds this one exception to AGENTS.md's `CGO_ENABLED=0` rule. The Postgres suite also runs nightly and on PRs that touch `internal/server/store/**`.
- Other workflows, each with `workflow_dispatch` and a `concurrency` group with `cancel-in-progress`: `web.yml` (vitest and Playwright, path-filtered to `web/**` and `extension/**`), `rehearsal.yml` (the end-to-end rehearsal under Xvfb, on PRs that change what it builds or reads, every push to `main` and release tags), `e2e-wakatime.yml` (5.6), `e2e-install.yml`, `footprint.yml` (9.7), `release.yml`, `restore-drill.yml` (weekly), `loadtest.yml` (weekly and manual), `fixtures.yml` (manual, validates fixture provenance).
- The PR that adds the first workflow beyond `ci.yml` updates AGENTS.md's "one job" sentence to describe this layout.

## 4. Data model and what crosses the network

### 4.1 The event model, and what changes in PR1

PR1's `pkg/event` stays the event model: the 12 envelope fields of SM 1.1, the kinds of SM 1.2, the six derived records of SM 1.3, the reason codes of SM 5.2, `Validate()` and `Shareable()`. Grafts need these changes. Each one lands in `pkg/event` and `docs/schema/event.schema.json` in the same commit, as PR1's drift test requires, and any change to the local-field list is called out in the PR description (PR1 AGENTS.md):

| # | Change | Reason |
|---|---|---|
| E-1 | Remove `chain_hash` from the envelope | SM 1.1's per-event chain is replaced by the ledger's Merkle batches and batch chain (3.6; EV D2). A linear per-event chain forces revealing every event in a window, which rules out the project-scoped reveal |
| E-2 | Add envelope field `via` with values `hook`, `log`, `otel`, `os`, `git`, `shell`, `editor`, `editor_unverified`, `bridge`, `track`, `plugin`, set by the agent from how the record arrived, and add rules to `kindTable`: `os.*` must be `via = os`, `git.change` `git`, `cli.track` `track`, `otel.active_time` `otel` | The evidence level of an anchor depends on its provenance (6.4; JT must-fix 1). A sender can't choose its own `via` |
| E-3 | Add `offline` to `Witness` | The capped offline label (7.4; EV D6) |
| E-4 | Add `pending` to `BucketState`, and `rev` (uint32) to `MinuteBucket` | The 900 s pending window and bucket revisions (3.7; TE D6) |
| E-5 | Tag `MinuteBucket.Anchors` local | Anchor ids stay on the device; tier 1 doesn't need them (EV D1; JT must-fix 13) |
| E-6 | Add `lbl` to `BucketSlot`, a list of `via_track`, `via_plugin`, `via_bridge` | Programs and reviewers see which minutes rest on side doors (JB must-fix 3) |
| E-7 | Add `via`, `corroborated` and `labels` to `Anchor` | The scorer's provenance rule (6.4) |
| E-8 | Add to `CLITrack`: `text_len`, `env_hints` (names of harness variables present, such as `CLAUDE_CODE_SESSION_ID`), `log_match` (`matched`, `unmatched`, `no_reader`). The agent fills `harness` and `session_key` from the process tree when the call descends from a harness session | The `track` rule in 5.4 |
| E-9 | Add to `SubmissionTotals`: `offline_s`, `held_s`, `via_track_s`, credited seconds by evidence level, and the four bands of 7.7 | Reviewers and the handbook text state these (7.7, 9.3) |
| E-10 | Document that `error_sig` on `agent.tool` and `shell.command`, `files[]` and `file_hash` hold keyed opaque values | They appear in tier-2 reveals (3.6) |
| E-11 | Add `unknown` as the last `app_class` value: the client can't tell which window is in front, and the record carries no `app_hash`, `hosted_sessions` or `focus_project`. Add `focus_unknown` to the anchor labels and to `BucketSlot.lbl`, after the side-door labels | Desktops that hide focus credit at level C, and programs and reviewers see which minutes those are (SM 2.5; 6.4). A new value in an existing tier-1 field, not a new field (PLAN ground rule 10) |
| E-12 | Add the kind `relay.message`, ledger record type `0x13` (docs/spec/ledger-v1.md 4.2), with `app` (`t3_code`), `client` (`web`, `mobile`), `sent_at`, `thread_key`, `declared` (`none`, `cron`, `cross_session`), `len_chars`, `low_effort` and `prompt`. Its `kindTable` rule takes `source` `log` with `via` `app`. It carries `f` with `v` and `pc` and never `c`. Add the `via` value `app` and the `parent_class` value `agent_app`. An app's thread, run and message ids enter pairtime only as HMACs under a device key, as `thread_key` and `session_key`; no raw id, name, path or text is in any shareable field. `client` is the app's own word for where the message was typed, which SM 2.2's phone rule reads | SM 1.2, 2.2 rule 9; 5.1; D-relayed (docs/errata/2026-11-22-relayed-prompts.md) |
| E-13 | Add `relay` and `guest` to `prompt.submit`: `relay` the event id of the earlier `relay.message` the agent paired the prompt with, and `guest` true when T3 Code's reader says a guest's message started the prompt's run, so the prompt keeps no text and SM 2.2 rule 4 takes it (5.1) | The agent writes a pairing on whichever of the two records arrives second, so neither waits for the other (SM 2.2) |
| E-14 | Add the origin `relayed` after `human_pasted`, the label `via_relay` last in `Anchor.labels` and `BucketSlot.lbl`, and `via_relay_s` to `SubmissionTotals` | SM 2.2 rule 9 and 5.1; 6.4. `via_relay` is an origin label, not a side-door one: every relayed anchor carries it, and the anchor's `via` is its witness record's, `app`. `lbl` is an existing tier-1 field, so this adds a value to it, not a field (PLAN ground rule 10), and the server must accept the value before any client sends it |

Content-derived scorer features and content commitments are not `pkg/event` fields. `pkg/ledger` computes them from the event's local fields with the device keys before calling `Shareable()` (3.6).

### 4.2 Disclosure tiers

R10 allows "different privacy guarantees for logged hours vs. submitted hours" (B). Four tiers make that concrete, plus a mode below tier 1 (TP B6; EV 7.1):

| Tier | What | Where | Who sees it |
|---|---|---|---|
| Local-only mode | `mode = "local"` in `config.toml`: nothing is uploaded, no roots, no buckets, no profile. The coach, recaps and chess clock still work. Hours logged in this mode are unwitnessed and can never be submitted. A record made in a local-only stretch counts as logged in it however late it reaches the ledger: while no batch has been sealed online since the stretch ended, it joins the stretch's local-only batches, and after that the ledger drops it, counts it, and never uploads it. A stretch starts at the last moment the writer knew `config.toml` said online, and a record no stretch holds counts as online however late it comes (3.6; docs/errata/2026-11-23-late-local-records.md) | Device | The teen |
| 0. Local | Raw prompts and final agent messages; transcripts, read in place and never copied; file paths in clear; per-slot presence and focus; opt-in typing aggregates; MinHash sketches and error signatures in clear; ledger bodies of every logged-only minute; coach records; the device key | `ledger/`, `vault/`, `coach/` on the device | The teen |
| 1. Continuous | Batch roots; minute buckets without anchor ids; commit records; client build, scoring version, harness names and versions; project fingerprints as HMACs | `pairtimed` | The teen's own dashboard live; public aggregates hourly (8.3). Nothing in this tier is scored for fraud unless the teen submits (TP B2 point 6) |
| 2. Submitted | For each batch overlapping the reveal range: every header and body hash; full bodies for the reveal set in 6.5; the challenged turns' kept tokens, each prompt with the agent message it answered; extras the teen adds. Sealed to the evidence key on upload and to the viewer's key on every read (7.6) | Evidence store | Assigned reviewers, Fraud Squad, Quality and Integrity, HQ spot-checkers. Every view logged and shown to the teen |
| 3. Public | Profile aggregates, projects the teen made public, forge-verified commits of public repos, published devlogs | Public pages | Anyone, or signed-in club members for club boards |

### 4.3 What crosses the network

Everything that leaves a teen's machine is in this table. There is no telemetry or analytics channel.

| From, to | When | Exactly what | Never in it |
|---|---|---|---|
| wakatime-cli to Hackatime | As today | Whatever Hackatime already receives (HC 1.3). pairtime adds and removes nothing | n/a |
| wakatime-cli to the local agent | Each heartbeat | The full heartbeat, over `127.0.0.1` only | Nothing leaves the machine on this path |
| Client to `pairtimed`, device sign-in | Each `pairtime login`, which links the computer to an account | The device grant request (docs/spec/device-auth-v1.md 2): the random device id the ledger made at install, the OS family, the pairtime build, and the Ed25519 public key that signs the computer's uploads, with its key signature. Then the polls of `POST /oauth/token` (section 4), each carrying the device code. The request metadata any server sees: IP and, through Cloudflare, a JA4 fingerprint | Hostnames, OS usernames, hardware ids, the private key, any HCA token, since the teen signs in with HCA in the browser, on the server's own pages |
| Client to `pairtimed`, tier 1 | Every 60 s while there is anything new; within 10 s after a prompt or stop hook, coalesced; at once on lock and suspend | Batch roots `{b, n, R_b, H_b, prev}`. Minute buckets `{v, params, minute, device, slots[4]{p, cr, why, kind, ev, lbl}, credited_s, uncredited{code: s}, agent_running_s, agent_only{p: s}, state, witness, rev}`. Commit records `{sha, parents, project, observed_at, author_time, committer_time, files_changed, insertions, deletions, path_hmacs[], credited_seconds_since_parent, rewrites[], actor, agent_trailer}`. Labels `{client_build, scoring_version, params, harnesses[{name, version}], typing_module, vm}`. The request metadata any server sees: IP and, through Cloudflare, a JA4 fingerprint | Prompt or output text, file names or paths, window titles, process names other than harness names, branch names of private repos, keystrokes or key timings, MinHash sketches, error text, hostnames, OS usernames, anchor ids |
| Client to `pairtimed`, project registration | First sighting of a repo | The root-commit SHAs, which the server turns into `HMAC(pepper, root_sha)` and discards; a normalized remote only for public repos or projects the teen publishes (SP 4.2) | Private remotes, folder names |
| Client to `pairtimed`, program link | When the teen connects a project to a program | The Hackatime project names the teen chooses to link, stored as `HMAC(pepper, user_id, name)` (AD 4.7) | Names of projects the teen didn't link |
| Client to `pairtimed`, publishing | Only when the teen clicks Publish | Devlog Markdown exactly as previewed; a project's display name and repo URL when made public; badge codes; a club join code | Anything the preview didn't show |
| Client to `pairtimed`, the owner's project names | Only when the teen turns a project's name on, changes its label, or turns it off, one project at a time on the computer, or hides it on the dashboard, which the computer then turns off too; off by default. A read with no name in it also goes when the agent starts, once a day, and first whenever the computer doesn't know what the server holds, so the computer learns its projects' short codes and the names the teen hid | The label the teen saw and approved and the project key it names, in a request signed with the device key like ingest (4.5), or a delete when the teen turns the name off. The label is at most 64 characters, cleaned as the sign-in's display name is (4.4), and by default it's the name pairtime shows for the project locally: for a git repo with worktrees, the main checkout's folder name. It's the private case of the publishing row: shown only on the teen's own signed-in dashboard and `GET /api/v1/users/me/dashboard`. The dashboard can hide a name but never set or change one | Paths, remote URLs, branch names, worktree folder names, or any name the teen didn't approve. The label is no event field, ledger record or tier-1 field |
| Client to evidence store, tier 2 | Only during a submission | A fetch of `/.well-known/pairtime-keys.json` before each upload, then the metadata reveal, then the content reveal, each sealed to the evidence key the document lists (7.5, 7.6) | Anything outside the reveal set; unchallenged content |
| Client to `pairtimed`, backup | Only if the teen opted in | A wrapped `K_dev` and encrypted ledger and vault segments; the server holds ciphertext only (7.11) | The recovery code; `K_dev` itself |
| Client to GitHub Releases | Once a day | A request for the signed update manifest (CP 4.6) | Any user data |
| `pairtimed` to HCA, forges, programs, public git, Healthchecks.io | Sign-in; public-repo commit checks; webhooks; once a day; every 5 minutes | OIDC; `GET` of a commit by SHA; signed submission events; the day root; liveness pings | Per-user data in pings or the day root, which carries only hashes and a daily count. On a server with few uploaders, that count shows how many batches they uploaded that day; an instance can keep the day roots off by leaving `PAIRTIME_WITNESS_REPO` unset |

Cloudflare terminates TLS in front of `pairtimed`, as it does for Hackatime (SP OQ 3). Tier 1 carries numbers only, so that is accepted. Tier-2 content is sealed end to end past Cloudflare in both directions (7.6; JT must-fix 5).

### 4.4 Data inventory and retention

Every field has a finite retention period, published at `https://<host>/privacy` with the controller's name (8.6). This consolidates TP B8, EV 7.2 and JT must-fix 13; differences from TP B8 are marked.

| Where | Data | Kept |
|---|---|---|
| Device | Prompt text, final agent messages | 90 days, or until a submission covering them is decided, whichever is later. The teen can shorten it or turn "keep prompt text" off (TP B8) |
| Device | Transcripts and agent output | Not copied; the harness's own retention applies (Claude Code: 30 days by default; TP B8) |
| Device | File paths in the vault; tool commands | 30 days |
| Device | Presence and focus slots, opt-in typing aggregates, MinHash sketches, which the device keeps only inside ledger records | With the ledger records that hold them: 180 days, or longer while a submission using them is inside its spot-check window |
| Device | Ledger records and batch roots | 180 days, or longer while a submission using them is inside its spot-check window; while the computer is linked, the batch roots of online batches the server hasn't stored stay, without their records, until it stores them, unless a fork or the server's device_mismatch has ended the chain |
| Device | Minute buckets | 180 days; day rollups and coach records until the teen deletes them |
| Device | Hook calls the agent hasn't parsed yet, in `held.jsonl`, its pending log and its replay file: whole payloads, prompts included, unencrypted, in a folder only the teen can read | Until parsed; kept 7 days at most, and no new call is kept once the ledger can't open until `pairtime restore` runs |
| Device | Commit records waiting for the uploader, in `commits/` | 180 days |
| Device | Records of past uploads: when, where, counts of what went, result codes, body hashes; for project names requests too, never a name | 90 days |
| Device | The server's last answer to the teen's project names requests, in `project-names.json`: the label it shows by project key, its short codes, the keys it lists as hidden, what requests carried since the server last answered, until an answer settles it, and a note of each name pairtime turned off because the dashboard doesn't show it, with its label | Until the next answer replaces it; a note until the server shows the name again after the teen turned it on, 1,000 notes at most, the oldest going first; a sign-in to another server or device keeps only the notes; all of it until uninstall or "delete everything" (8.8) |
| Device | `K_dev` | Until uninstall or rotation |
| Server | `hca_sub`, `slack_id`, handle; email encrypted at rest, with a keyed index so a deletion request can find it; HCA verification status and eligibility; sign-up and last sign-in times; GitHub uid if linked | Account lifetime |
| Server | Web sign-in sessions: the SHA-256 of each session token; its sign-in, expiry and revocation times; when HCA last authenticated the person; the display name HCA sent at that sign-in (`given_name`, else `nickname`, at most 64 characters), which no account row copies | Until sign-out, or 30 days after sign-in at the latest; the row is deleted 1 day after the session ends |
| Server | `device_id`, OS family, client build, harness names and versions | Device lifetime plus 90 days |
| Server | Uploading IP address and JA4 | 30 days, then deleted, not truncated (JT must-fix 13; differs from EV 7.2) |
| Server | Minute buckets | Per-minute detail for 180 days, the submission horizon, then daily totals per project; minutes a submission uses stay until its spot-check window closes (JT must-fix 13; differs from TP B8's 13 months) |
| Server | Witness rows and batch roots, and each day's leaf hashes, which stay as long as any witness row of their day | 180 days; rows a submission uses stay until its spot-check window closes |
| Server | Drawn challenge items, keyed `(device_id, seq)`, with each one's status | 180 days after the batch that holds the record, while it can still be submitted (7.5 step 5); never deleted with a submission's evidence |
| Server | Commit records, project fingerprints | Account lifetime |
| Server | Project names the teen turned on: the label and the project key it names, shown only on the teen's own signed-in dashboard and its JSON, never on a public page, a program read, an export for anyone else or /demo | Until the teen turns the name off on the computer or hides it on the dashboard, either of which deletes the label at once; after a hide, the project key and the time of the hide stay until the device turns the name off too, 30 days after the hide at most, so the device can tell a hide from a name the server no longer holds; the device's purge 90 days after the device is removed, as its `device_id` row goes; or account deletion |
| Server | HMACs of Hackatime names linked to a program | Until the teen unlinks the program |
| Server | Wrapped `K_dev` and encrypted backups (opt-in) | Until the teen turns backup off or deletes the account; backup segments older than 180 days are deleted |
| Evidence store | Revealed metadata and content, risk flags | Until the spot-check window closes; 12 months after the decision until HQ sets a window. Never "the longer of" (JT must-fix 4; differs from TE 6.3). The teen can purge any revealed item at once (7.11) |
| Server | Reviewer decisions and deflation text, written without personal data | As long as the Unified DB row exists |
| Server | Evidence access log | 2 years, visible to the teen |
| Server | Interview notes, never recordings | With the decision |
| Server | Minimal fraud record after account deletion: `hca_sub`, verdict, date, reason category | A period HQ sets (13.2), reported as retained by the eraser (TP B3) |
| Public repo | Day roots | Permanent; only hashes and a daily count, which on a server with few uploaders shows how many batches they uploaded that day. An instance can keep them off by leaving `PAIRTIME_WITNESS_REPO` unset |

A submission's spot-check window opens at its latest decision, and a revocation doesn't move it. A submission withdrawn before any decision opens its window at the withdrawal, and one that is neither decided nor withdrawn opens it at its freeze, so nothing kept for a submission's window is kept for ever. Withdrawing deletes nothing. The purge (7.11) is how the teen deletes a revealed item at once.

### 4.5 Ingest signing and the firewall

Every ingest request is signed over a fixed envelope with the device's Ed25519 key, in place of RFC 9421, which is a general HTTP signing scheme pairtime doesn't need since it owns both ends (JE must-fix 9):

```text
POST /api/v1/ingest/batches
Authorization: Bearer ptd_<30+ base62><6-char CRC32>
Idempotency-Key: <UUIDv7>
Content-Encoding: gzip
X-Pairtime-Device: <device_id>
X-Pairtime-Req: <request counter, strictly increasing per device>
X-Pairtime-Time: <unix ms>
X-Pairtime-Body-SHA256: <hex(SHA-256(raw body))>
X-Pairtime-Signature: ed25519=<base64 Ed25519(device_key,
    "pairtime-ingest-v1\n" + device_id + "\n" + req + "\n" + time + "\n" + hex(SHA-256(raw body)))>
```

The server rejects a signature that doesn't verify, a time more than 300 s from its clock (the response carries the server time so the client can correct), a request counter at or below the stored one, and a body whose hash differs from `X-Pairtime-Body-SHA256`. The header carries the hash beside the signature, so a changed body and a signature by another key get different refusals (docs/spec/ingest-v1.md 7 item 1). The device key stops a stolen bearer token from being replayed elsewhere and binds the witness chain to one install. It doesn't stop the device's owner from forging events, which is what section 7 is for (SP 8.3).

Each batch root links to the device's stored head: batch `head + 1` needs `prev` equal to the head's `H_b`, and a skipped batch is `chain_gap`. A device's first batch, and the first after a re-link (a new token for the same `device_id`, after `pairtime logout` or a removal on the web), may take any `b` above the stored head with `prev` taken as given. `b` = `head + 1` still has to link, and a first batch 1 still has an all-zero `prev`. The batches a re-link skips become a local-only gap that the server never asks for, since local-only mode sends no roots (4.2; docs/spec/ingest-v1.md 2.7 and 7 item 6; D-local-gap).

The firewall (SP 8.2): every field has an allowlist schema; strings are ids, enums, hashes or HMACs of bounded length; any field that could carry free text gets a 422. Limits per request: 2,000 buckets, 500 commit records, 256 KB compressed, 4 MB decompressed. Partial acceptance names the rejected item and a machine-readable code. Its CI test puts random text in every string field and always gets a 422 (AD WP 2.4; JT graft 9).

## 5. Capture and integrations

Every integration follows one rule: use what already runs on the teen's machine and in the program's code, and add the smallest thing next to it (AD 4). Capture comes in layers, each wider and weaker than the one before: native hooks and log readers, the generic hook mode and plugins, the WakaTime bridge, and Karim's AGENTS.md entry. A harness can sit in several layers, and the strongest record wins.

### 5.1 The v1 harness set

All hooks are registered in exec form (`command` plus `args` pointing at the real `pairtime` executable), the one hook type every consumer of these files runs (HS 16). Entries are merged next to existing ones and never replace them, because git-ai rewrites its own entries in the same files daily (HS 13.3).

| Harness | Live capture (`via = hook`) | Second record (lookback and CC1) | Automation labels read (SM 2.2) | Caveats |
|---|---|---|---|---|
| Claude Code (CLI, VS Code extension, Desktop Code tab) | `~/.claude/settings.json`: `SessionStart`, `UserPromptSubmit`, `Stop`, `StopFailure`, `Notification`, `PermissionRequest`, `PostToolUse` (matcher `Edit\|Write\|Bash`), `SubagentStart`, `SubagentStop`, `SessionEnd`. OTLP/JSON to the agent through the settings `env` block with prompt logging off; `user.email`, account and organization ids dropped on arrival (HS 16 item 4) | Transcript tailer on `~/.claude/projects/**/*.jsonl`, 30 days by default (HS 3.2) | `app.entrypoint`, `CLAUDE_CODE_CHILD_SESSION`, `Stop.session_crons`, `stop_hook_active`, `isSidechain`, `agent_id` | Hooks fire in a folder only after its trust dialog; `--bare` runs fire none and are caught by `procs` (HS 3.1). `SessionEnd` shares a 1.5 s budget, so the hook hands off and exits |
| Cursor (IDE, `agent` CLI) | `~/.cursor/hooks.json`: `beforeSubmitPrompt`, `afterTabFileEdit`, `afterFileEdit`, `afterAgentResponse`, `stop`, `sessionStart`, `sessionEnd` | WakaTime heartbeats; the `state.vscdb` reader is v1.5 because the database can be gigabytes on a spinning disk (HS 15) | `is_background_agent`, `stop.loop_count`, `followup_message` | Cursor also reads Claude-format user hooks; events are deduplicated by the harness the hook binary finds in its parent chain |
| GitHub Copilot (VS Code agent mode, CLI) | `~/.copilot/hooks/pairtime.json`, camelCase events, which carry timestamps | CLI `events.jsonl`, VS Code `chatSessions/` | autopilot, `stop_hook_active`, subagent `agentId` | Exec form only, since HTTP hooks need `COPILOT_HOOK_ALLOW_LOCALHOST=1` (HS 14) |
| OpenAI Codex (CLI, IDE extension, desktop) | `~/.codex/hooks.json` with one fixed command line, so binary updates don't change its hash and break trust | `~/.codex/sessions/**/rollout-*.jsonl` (3.12 on `.jsonl.zst`) | `session_meta.source`, `originator`, `thread_source`, subagent depth | Each hook needs a one-time trust in `/hooks`. Until then the rollout reader is primary, its minutes carry `via = log`, and `doctor` says "Codex hooks not trusted yet, using rollout logs" (HS 6.1) |
| Gemini CLI | `~/.gemini/settings.json` hooks; `telemetry.logPrompts=false` if OTel is on | `~/.gemini/tmp/*/chats`, `logs.json` (30 days) | non-TTY runs, `-p`, `AfterAgent` deny-retry | |
| Devin Desktop and Devin CLI | Reads the Claude-format hooks in `~/.claude/settings.json` by default; deduplicated by harness | `~/.local/share/devin/transcripts` (the Windows path is unknown; HS 15) | | Cascade's old hooks aren't targeted (HS 2 finding 2) |
| Cline | File hooks on macOS and Linux after the teen ticks "Enable Hooks" | `tasks/<id>/*.json` on every OS | | On Windows, Cline is log reader plus presence only in v1 (TE 4.1) |
| Zed agent, Warp agent, JetBrains AI Assistant, Junie, Kilo, OpenCode, Amp, Kiro, Trae, Goose, Continue | Karim's AGENTS.md entry with `pairtime track` (5.4); a plugin or the generic hook mode where the harness has hooks or a plugin system (5.3) | v1.5 log readers: Zed `threads.db`, Warp `warp.sqlite`, Goose `sessions.db`, Continue `dev_data` (HS 15) | per adapter | `track` minutes are level C unless corroborated (6.4) |
| Everything wakatime-cli parses (46 agents; HS 2 finding 10) | The WakaTime bridge (5.7) | | | Level C unless corroborated |
| Browser chat and app builders (ChatGPT, Claude.ai, Lovable, Bolt, v0, Replit) | None in v1; a browser extension is v2 (HS 10) | git only: commits show on the project story as work pairtime can't see, with no time credited | Lovable commits as `gpt-engineer-app[bot]`, Replit adds `Replit-Commit-Author` | |
| Cloud agents (Codex Cloud, Claude Code on the web, Copilot coding agent, Cursor cloud agents) | None: their turns are origin `cloud` and earn nothing (SM 2.2 rule 5) | | | The teen's local time reviewing their pull requests is credited through editor, command and commit anchors |

**Agent apps** drive the harnesses above for clients elsewhere. T3 Code (pingdotgg/t3code) runs a server, `t3 serve`, on the machine where its agents run, and takes messages from its desktop app, from its web client in any browser, and from its phone app, on that machine or another. It runs Claude Code through the Claude Agent SDK, so Claude Code's hooks fire as usual. But the sessions' nearest non-shell ancestor is T3's server, an `agent_app` process that SM 2.2 rule 4 lists, so rule 4 takes every prompt, and the typing happened on another device. T3 Code's own store says which messages a person sent, and pairtime reads it to write SM 2.2 rule 9's `relay.message` records:

| App | Read from | What pairtime takes | Guards | Caveats |
|---|---|---|---|---|
| T3 Code | `<T3 home>/userdata/statev2.sqlite`, on every OS. The home is `T3CODE_HOME`, read from the environment or, for T3's background service, from its systemd unit or launchd plist, and `<home>/.t3` otherwise. A WSL backend's store is read by pairtime inside that distro, never over the `wsl$` share. The store is in WAL mode, and T3 holds it open while it runs | The `sqlite3` command-line tool, when it is on PATH: `sqlite3 -readonly -batch -bail -init <null device>`, so no `.sqliterc` runs, with every field hex-encoded behind a fixed separator, since `-json` needs 3.33 and Ubuntu 20.04 ships 3.31. The SQL goes to `sqlite3` on stdin, never as an argument, since it names the folders pairtime tracks and other accounts on the machine can read a process's arguments. One short process per read, at low CPU priority, under a timeout, and only while `statev2.sqlite-wal` exists: a closed T3 relays nothing, and a read then would create `-wal` and `-shm` files in T3's folder. Every query is a rowid range or an index lookup, never a scan, and none touches the search table `orchestration_v2_message_search`. The client still links no SQLite (3.5) | New user-role rows of `orchestration_v2_projection_messages`. A first query reads only what decides a row: its stamp and its `updated_at`, its thread's folder, its run, and the SM 2.2 test of T3's own (author, source, markers), with the guest test and the folder test in its `WHERE` clause, so the reader never reads a guest's message or the text of a folder pairtime doesn't track or ignores. The record's `client` is the row's source, `web` or `mobile`. A row whose `updated_at` is later than its `created_at` when the reader first sees it was edited before then, as an agent's `t3_queue` edit stamps it, and witnesses nothing (SM 2.2). The hook still records the prompt T3 makes of a guest's message, which carries the guest's name and words, so the first query also returns the runs guest messages started, without their text, and the agent asks the reader at once before it keeps the text of any prompt from a session T3's server started. A prompt a guest's run delivers is marked `guest` and keeps no text: it is a `prompt.submit` whose text the device never had (docs/spec/ledger-v1.md 10.1), with an `f` of only `v` and `pc`, and SM 2.2 rule 4 takes it, witnessed or not, so it opens no anchor. A client that marks other prompts `guest` gains nothing by it, because those prompts earn nothing either. That holds while the reader runs. When it can't answer, with no `sqlite3` on PATH, a schema the probe refuses or a read that times out, pairtime can't tell a guest's prompt from any other: it keeps that prompt's text as it keeps any prompt's, and the prompt carries `c` as ledger-v1 10.1 requires. Rule 4 still takes it, since no record witnesses it, so it earns nothing and the draw never picks it. A second reads the text of the rows left, by primary key, to measure and class it, and pairtime drops it there. A message's run pairs it: the run whose `userMessageId` is the message goes to a provider thread whose `nativeThreadRef.nativeId` is the Claude Code session id, keyed as the hook keys `session_id` (SM 2.2). Nothing in T3's folders is written | The reader probes the schema before it reads: the columns it needs, the payload keys it reads on a sample row, and migration 55 (`OrchestrationV2`) recorded in `effect_sql_migrations`. A newer head passes, with a `doctor` line saying T3's schema is newer than pairtime has tested; a missing store, or one below 55, turns the reader off. The cursor, in `state.json`, starts at the newest row on the first run. T3's VACUUM (`t3 doctor`'s compaction) can renumber rows, so when the row at the cursor no longer holds the message id read there, or the newest rowid falls below it, the reader resyncs by `created_at` through the `(thread_id, created_at, message_id)` index | `doctor` says when T3 Code is installed and `sqlite3` isn't, and how to get it: Debian and Ubuntu don't install it by default (`apt install sqlite3`), Fedora has `dnf install sqlite`, Windows `winget install SQLite.SQLite` and then a restart of pairtime, and macOS ships it. It also says when a T3 Claude instance has a Claude home of its own (`CLAUDE_CONFIG_DIR`), which loads none of pairtime's hooks, and when T3 is installed but no session T3 started has reached pairtime in a day. A message T3 queued longer than `RELAY_MATCH_S`, a message T3 steered into a running turn, and an approval, answer or stop given in T3's interface earn nothing (SM 2.2) |

pairtime's app table (`internal/platform/appclass.json`) gains two T3 Code entries. Its server, `t3 serve`, is an `agent_app` server, so the walk gives the sessions it starts `parent_class` `agent_app`. SM 2.2 rule 4 lists that class, so every prompt of those sessions is headless unless a T3 message witnesses it, and a message witnesses only a prompt of such a session (SM 2.2 rule 9). Its desktop app is an `agent_app` window, which hosts every session its own server drives (SM 3.2 rule 3), so SM 2.2 rule 6, tested at the message's stamp, sees a message typed into it on this computer. That is the least WP-EV2's app classes need. The class comes from the process's name, which a script can set: its own file name, or the title a Node program gives itself. So the `agent_app` test sorts honest setups and is no security boundary. A script that takes the server's name and writes T3's store itself reaches what a script driving T3 Code reaches (7.12): up to all of wall time at level C, which D11 keeps off everything public. T3 also sets `CLAUDE_CODE_ENTRYPOINT=t3-code` for every Claude it runs; reading it in the hook is a follow-up for SM 1.2's entrypoint, not part of this rule, since a witnessed prompt skips rule 4 by its ancestor either way.

OS signals cover every harness: presence, focus, power and process scans (3.3). WSL is v1.5: it needs a Linux agent inside WSL handing records to the Windows agent through a spool directory, with the Windows agent supplying presence and focus (SM 2.3; CP 6.4).

**Real fixtures** (JE must-fix 2). Every adapter's tests replay fixtures captured from the real harness. Each harness and version gets a capture work package, run by a person or a CI machine with that harness installed, with a scrub step (`pairtime fixtures scrub` runs `pkg/redact` plus a manual review) and a `PROVENANCE.md` in each fixture directory naming the harness version, OS, date and who captured it. A fixture written from documentation is marked `synthetic: true` in its provenance file, and an adapter can't ship with synthetic fixtures alone. `fixtures.yml` checks that every fixture directory has a provenance file.

### 5.2 Provenance and side doors

Hooks and log readers are written by the harness or spawned by it, so a script has to work to fake them. Four paths accept claims from any process on the machine: `pairtime track`, plugins posting over HTTP or the inbox, the WakaTime bridge, and editor-extension events. Relay records are claims too: an agent app's store holds whatever any client with the app's credentials sent, which any process running as the user can mint, and which that process can also write by hand (5.1). On Windows without the opt-in typing module, `input_any` is the only presence signal, and a $10 jiggler satisfies it (JT, What breaks). So the scorer sets these anchors to level C unless something independent backs them (6.4; JT must-fix 1):

- Anchors with `via` in `track`, `plugin` or `bridge` are level C unless the harness's own log or `input_keys` corroborates them. When a pairtime log reader holds the matching user entry for the same session within `TRACK_LOG_MATCH_S` = 10 s, the log record's own anchor (`via = log`) is the one that counts, at its level, and the side-door event becomes a cross-check. When `input_keys` shows keyboard input in the host window in the `KEYS_PRE_S` = 10 s before it, the side-door anchor is level A.
- A relay record opens no anchor unless it pairs. It can only witness a hook or log prompt of a session an agent app's server started, and the anchor of a prompt it witnesses that scores as relayed is the record itself, at its `sent_at` (SM 2.2 rule 9, 2.3). A relayed prompt is level C with `via_relay`, always, crediting SM 2.8's lead and reading shapes and counting as no human anchor for anything else. Keyboard input on this device makes a prompt `human_typed` or `human_pasted` instead, under rules 6 and 7 tested at the app's stamp, and then it isn't relayed. The first bullet's log match doesn't apply to relay records: a relay record that pairs with a hook or log prompt is that prompt's witness, not a cross-check the prompt makes redundant.
- `track`, `plugin`, `app` and `editor_unverified` records never count toward `logged_s`, `ai_logged_s` or `hackatime_logged_s`, so a fake stream can't raise the handbook ceiling that caps its own level-C credit (6.2 D19). That covers every `relay.message`. The harness's own records of a relayed turn count as any turn's do, because they are the agent's own events, and leaving them out would zero a person who works only from a phone.
- Output sizes reported by `via = plugin` or `bridge` events fill a session's reading bucket only up to `R_TURN_BASE_S` = 20 s per turn, unless a hook or transcript record backs the turn.
- SM 1.3 and 2.5 count a session as reported by the harness's own hooks or logs when it has a record with `via` `hook` or `log`. In any other session a turn runs only to its latest agent record, and nothing the session credits can open a lookup, because no side door reports a stop that anything backs: `track` and the bridge report none, and a plugin's is its own claim. So a `track` turn never runs, and a bridge turn runs to its latest edit.
- Editor heartbeats are `via = editor` only when the peer-process check passes (5.7), and `editor_unverified`, level C, otherwise.
- Editor-extension change-shape claims never set `input_keys` (JB must-fix 3). They are not built in v1 (5.11).

Level-C minutes are submittable only if the program opts in to level C, only inside a commit interval whose closing commit is forge-verified, and at most at `min(credited, ai_logged / 3)` (SM 5.4; EV D5). CI carries fixtures with `input_any` true and `input_keys` unknown for every side door, including a `track` loop with a jiggler, which must earn 0 submittable seconds (JT method note).

### 5.3 The plugin interface

R7 asks for "a plugin approach where integrations can be built for every use case in the future" (B). Three ways in, all ending in the same normalized events, so the scorer and every check work unchanged:

1. **Generic hook mode, no code** (TE 4.2). Any harness with Claude-style hooks (JSON on stdin, an event name) can run `pairtime hook --harness <name> --event <prompt|stop|tool|permission|session_start|session_end> --map <mapping.toml>`. The mapping names the JSON paths of the session id, cwd, prompt and tool fields, and lists the `hosts` executables. Mappings live in `internal/adapters/generic/mappings/*.toml` and are fixture-tested like any adapter. Events get `via = hook` when the parent chain reaches a listed host, and `via = plugin` otherwise.
2. **Out-of-process plugins, any language.** A plugin ships a `pairtime-plugin.toml` (`id`, `version`, `harness`, `emits`, `hosts`) and writes `pkg/event` JSON Lines to `pairtime emit --source plugin:<id>`, to `POST http://127.0.0.1:<port>/v1/events` with the bearer token from `port.json`, or to `inbox/<id>/*.jsonl`. The agent validates each event against `docs/schema/event.schema.json`, overwrites `t` with its own arrival stamp when the plugin's is more than 5 s off, and records `plugin:<id>@<version>`. `emit` called from a process whose parent chain reaches a listed host gets `via = hook`; HTTP and inbox events get `via = plugin`.
3. **In-tree adapters** implementing the interface in 3.4, for harnesses worth shipping by default. They need captured fixtures and a passing conformance job.

A plugin's events are claims. A prompt opens an anchor only under SM 2.2 rule 6, with OS input in the 10 s before it and focus on a window whose process matches the plugin's `hosts`, and the side-door rules in 5.2 apply on top. So pairtime can accept plugins without vetting their authors. `sdk/plugin/` holds the spec, the conformance kit (`pairtime adapters test <fixtures>` replays recorded output and diffs the events against expected JSON) and an example plugin in Python. The dashboard's Settings page lists every adapter with its last event time and parse-failure count, so a broken parser is visible instead of silently under-crediting (SM Risk 7).

### 5.4 Karim's AGENTS.md entry and `pairtime track`

Karim's direction (B, Karim's direction): make pairtime universal with an AGENTS.md entry (or a skill) plus a CLI; the entry tells the agent to take the user's prompt, strip PII, and pass it to a `track` command. This design ships it as Karim described it, as the universal fallback for agents that hooks and log readers can't see. Its credit level is set by the trust rules in 6.4. The installed text:

```markdown
<!-- pairtime:begin v1. Added by `pairtime install`; remove with `pairtime uninstall`. -->
pairtime tracks the human time spent on this work. When the user sends you a message, take their
message, remove personal information from it (names, email addresses, phone numbers, street
addresses, API keys, passwords, tokens and file paths), and pass the result to pairtime:

    pairtime track --harness <your tool's name> "<the message with personal information removed, at most 400 characters>"

Run it once for each user message, alongside your first other tool call. If it fails or isn't
found, carry on normally.
<!-- pairtime:end -->
```

The cap of 400 characters follows TE 4.4, which JB counts as honoring Karim's wording (JB must-fix 9). It can only under-credit: at `COMPOSE_CPS` = 1.5 chars/s, 400 characters give a typing part of 266.7 s, and `COMPOSE_MAX_S` = 300 s is reached at 450, so a longer prompt loses at most 33.3 s, and only in slots with `input_keys`. The entry asks for a copy rather than EV 4.4's model-written summary, because a summary would class a go-ahead as substantive and hide the near-duplicates the re-roll detector needs (SM 2.2, 4.2; 13.1 item 3). The entry has no "don't mention it" line, because a privacy tool shouldn't tell an agent to hide what it does; the install screen says once what the entry does (JT must-fix 15).

**Where it goes.** The writer knows the user-level instruction file of every harness in HS 11.1's table: `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.gemini/GEMINI.md`, `~/.copilot/copilot-instructions.md`, `~/.devin/global_rules.md`, and the global rules files of Cline, Kilo, OpenCode, Zed, Warp, Junie, JetBrains AI Assistant, Amp, Kiro and Trae. `pairtime install` writes the entry only for each detected harness that this release sees through neither hooks nor a log reader, counting its adapters and generic hook mappings (HS 11.4). With every adapter in 5.1 shipped, that is Zed's agent, Warp's agent, JetBrains AI Assistant, Junie, Kilo, OpenCode, Amp, Kiro and Trae; if 9.7's cut list drops the Gemini, Devin or Cline adapters, those harnesses join them. It never writes into a repository (HS DI 13), and without `--track` never into a file that a harness pairtime sees through hooks or a log reader also loads. An install or update removes the entry from a harness that has since gained hooks or a log reader, and says so, unless the teen added it there with `--track`. `pairtime install --track <harness>` adds it to any other harness the teen names, until the teen unticks it (TE 4.4). For Claude Code that also adds `permissions.allow: ["Bash(pairtime track *)"]`, so the call doesn't stop for a permission prompt (HS 11.2); for Codex it warns when `~/.codex/AGENTS.override.md` exists, because Codex then reads only the override; Cursor keeps User Rules in its settings, so it prints the entry to paste. `pairtime install --calibration-volunteer` offers `--track` for one hooked harness the volunteer picks, unticked, so the compliance counter can measure how often agents follow the entry (HS 11.3). For a harness pairtime doesn't recognize, `pairtime install --print-agents-md` prints the entry. The consent screen lists every file and lets the teen untick any harness. This is the install all three candidate designs proposed, decided on 2026-11-20 (13.1 item 2).

**What it costs**, stated on the consent screen. The entry adds 30 to 60 tokens of mostly cache-read context per request. Each call costs output tokens roughly equal to the copied text plus about 30, so at most about 130 with the 400-character cap, and it can add one extra model request per turn unless the model batches the call with its first real tool call, which the entry asks for (HS 11.2). Karim's estimate of about 30 input tokens and one tool call counts the instruction only.

**Beside hooks and log readers.** When pairtime already sees a session through hooks or a log reader, that record's `prompt.submit` is the anchor. A `track` call from the same session within `TRACK_DEDUPE_S` = 30 s of such a prompt is recorded as `track_matched`, opens nothing, and feeds a compliance counter per harness, which is how pairtime measures how often agents follow the entry (HS 11.3). Because the entry reaches such a session only through `--track` or by hand, these calls come from an opted-in harness, a hand-added entry or another process. The counter records, per harness, the share of hook and log prompts that got a call and how long after its session's last hook or log prompt each call came, flagged calls included; `doctor` shows it and `doctor --report` carries it. A `track` call with no hook or log prompt within `TRACK_DEDUPE_S` of it, from a session pairtime sees through hooks or a log reader, opens nothing and raises the tier-2 flag `track_without_prompt`. An agent following the entry calls `track` soon after the prompt it copies, so such a call likely came from outside the agent's turn (HS 11.4 point 2). A call's session counts as seen, here and in condition 4 below, when the call's `log_match` isn't `no_reader` or when a hook or log record of the session is dated at most `TRACK_DEDUPE_S` after the call, so no record dated later changes what a call did (3.7). The scorer matches inside the pending window (3.7), so a transcript line that a tailer reads a few seconds late still matches. Claude Code with its folder trust pending, Codex with untrusted hooks, and `--bare` runs all leave transcripts or rollouts, so their log readers cover them. The `track` call is the fallback anchor only for sessions pairtime sees through neither: Zed's agent until its v1.5 reader, Warp's agent, JetBrains AI Assistant, and harnesses pairtime doesn't recognize.

**How a fallback `track` call becomes an anchor.** The agent resolves the caller's process chain. The call opens an anchor only when all of these hold:

1. The chain reaches a harness process whose session is hosted by the window that had focus at the call, or by the focused IDE.
2. That window saw OS input in the `TRACK_INPUT_PRE_S` = 30 s before the call.
3. No other `track` anchor came from the same session in the previous `TRACK_MIN_GAP_S` = 20 s.
4. pairtime sees the session through neither hooks nor a log reader.

The anchor is classed like a prompt from its text length and content (SM 2.2). Its typing part is realized only in slots with `input_keys`, and its reading bucket fill is `R_TURN_BASE_S`, because no record shows the agent's output. No record shows the agent running or stopping either, so the turn never runs, its base drains as `READ`, and nothing the session credits opens a lookup (SM 1.3, 2.5). Its minutes are level C and carry `via_track`, unless `input_keys` shows typing in the host window in the 10 s before the call, which makes them level A (TE D9; JT graft 1). `cli.track` records never count toward `logged_s` or `ai_logged_s` (D19). The text is redacted again on the device by `pkg/redact`, because the model's stripping is never trusted (TP B5). It is stored in the vault and committed, so a `track`-anchored turn can be drawn by the challenge like any prompt.

**Sandboxes.** Codex's default `workspace-write` sandbox has no network, and Claude Code and Copilot CLI have sandboxes too (HS 11.2). `pairtime track` tries the socket, then the local port, then exits 0 silently. A blocked call costs only the missing anchor, and on a harness opted in with `--track` it shows in the compliance count. pairtime doesn't try to get around a sandbox.

### 5.5 The coach skill

Karim's "(or a skill)" also points the other way: a skill the teen's own agent uses only when the teen asks something like "how did I do today?" or "write my devlog" (TE 4.5). In Claude Code it is `~/.claude/skills/pairtime-coach/SKILL.md`, which loads only when its description matches a request (HS 3.4), plus `permissions.allow` entries for `Bash(pairtime recap *)`, `Bash(pairtime story *)` and `Bash(pairtime devlog *)`. In Codex, Gemini CLI and Copilot it is a three-line mention in the user-level instruction file each one loads (`~/.codex/AGENTS.md`, `~/.gemini/GEMINI.md`, `~/.copilot/copilot-instructions.md`; HS 11.1), written by 5.4's writer, which warns when `~/.codex/AGENTS.override.md` exists, because Codex then reads only the override. The commands print facts, not content: `pairtime story --project . --since last-devlog --json` returns credited time by kind, agent time, commit subjects, moment codes, error families and the long fight. Prompt text is included only with `--include-prompts`, for the current project only. A devlog the agent writes is saved as a draft with `ai_assisted = true` and published with that label, because programs ask for AI declarations (HC 3.3) and a tool that helped hide AI use would undermine R9. The per-message `track` instruction stays an instruction-file entry, installed only where 5.4 says, because a skill loads only when a request matches it.

### 5.6 Editor time: the WakaTime dual-send

Coexistence with Hackatime is the adoption bet, and one report of "pairtime broke my Hackatime" in a program channel would end it (AD risk 1). So this path is specified in full (AD 4.5):

- **The line.** Under `[api_urls]` in `~/.wakatime.cfg`, add a pattern matching every heartbeat, pointing at the local agent: `.+ = http://127.0.0.1:<port>/api/v1|<key>`. wakatime-cli sends every heartbeat to `api_url` and also to every matching `[api_urls]` pattern, deduplicated by `url|key` (SP 8.8; HC 1.5). Hackatime stays the `api_url`.
- **The key** must match `^(waka_)?[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`. A malformed key makes wakatime-cli fail the whole run and queue everything offline, Hackatime included (HC 1.5). The installer generates a lowercase UUIDv4, validates the whole line with the same regex before writing it, and the local endpoint accepts only that key.
- **Duplicate patterns.** If the teen already has a `.*` line for another server, a second `.*` is a duplicate INI key that wakatime-cli ignores, so the installer picks the first unused spelling of match-all from `.+`, `^.*$`, `(?s).*`.
- **No WakaTime config.** The installer sets `api_url` to the local agent instead, seeds `[internal] ai_logs_last_parsed_at = <now>` so wakatime-cli doesn't back-fill AI logs from 2025-02-24 (HC 1.5), offers to install the WakaTime extension into VS Code-family editors, and tells Hack Club users that programs using Robert still need Hackatime (5.10).
- **The port** is a fixed default. If it is taken at startup, the agent picks another and rewrites only its own line, with the same validation.
- **The local endpoint** answers `POST /api/v1/users/current/heartbeats` and `.bulk` (up to 100 per request, per-item responses in Hackatime's shape), `GET /api/v1/users/current/statusbar/today`, and Basic, Bearer and `?api_key=` auth (HC DI 1). It deduplicates on a hash of the canonical fields, so offline-queue replays are idempotent.
- **Agent down.** The hook binary restarts the agent, so outages are short. wakatime-cli requeues a heartbeat at most 3 times (HC 1.5), and what it does with a refusing destination isn't documented. `e2e-wakatime.yml` settles it against the real wakatime-cli v2.28.4 binary on all three OS runners, with a fake Hackatime as `api_url` and the agent through `[api_urls]`: both receive a test heartbeat; with the agent stopped, the fake Hackatime still receives and wakatime-cli exits without an auth error; after restart, the offline queue reaches the agent; a malformed key is refused by the installer. The observed queue behaviour is saved as a report artifact. This job blocks every release that edits `~/.wakatime.cfg` (JE must-fix 5; JB must-fix 5).
- **Hackatime out of reach.** wakatime-cli backs off after a failed send, for every destination at once, and for about the first hour in which Hackatime can't be reached it holds back all its sends, pairtime's included. The editor and bridge records of that hour reach the agent only when Hackatime answers again, after their minutes are final (3.7), and don't count. `pairtime doctor` says when wakatime-cli is backing off (docs/errata/2026-11-23-wakatime-cli-2.28.4.md, item 4).
- **Uninstall** removes exactly the line pairtime added, identified by its key, and restores the original file byte for byte if pairtime created it.

### 5.7 The WakaTime bridge

The bridge turns what the dual-send delivers into events, and treats WakaTime's `category` as untrusted input, because wakatime-cli relabels human edits near agent activity as `ai coding` (HC 2.2, DI 2):

| Heartbeat | Becomes |
|---|---|
| Editor heartbeat (no `ai_session`) with `human_line_changes > 0` or `is_write` | `editor.activity` `edit` or `save`, `edit_source = unknown`. This is the hand-edit signal for every editor with a WakaTime plugin |
| Other editor heartbeats | `editor.activity` `navigate` |
| Transcript-derived heartbeat (wakatime-cli's AI sync made it: it carries `ai_session`, and its user agent names the AI agent, such as `claude-code/2.1.327`, with a model token when the transcript line named a model) with `ai_prompt_length > 0` | `prompt.submit` with `len_chars = ai_prompt_length`, no text, `via = bridge`, at the transcript's timestamp. Typing lead only, no think lead (AD D2) |
| Transcript-derived heartbeat with `ai_line_changes > 0` | `agent.tool` `edit`, `via = bridge`; its lines fill the reading bucket only up to `R_TURN_BASE_S` (5.2) |

Paths are replaced by per-device HMACs on arrival. An editor edit within 120 s of an `agent.tool` edit on the same file hash is `edit_source = agent` and opens no anchor.

**Bridge prompt times and lengths.** wakatime-cli stamps a prompt's heartbeat with the time of a transcript entry it batches the prompt with, often a tool result written before it, and a heartbeat's `ai_prompt_length` can be the sum of several user lines, lines nobody typed among them, such as a scheduled task's prompt or a slash command's expanded instructions. So a bridge prompt's time and `len_chars` are approximate, one more reason bridge prompts stay at level C unless keyboard input in the host window corroborates them, and v1 corrects neither. A heartbeat that carries both `ai_line_changes` and `ai_prompt_length` becomes the `agent.tool` edit first, then the `prompt.submit`, because its prompt is most often the next turn's (docs/errata/2026-11-23-bridge-prompt-times.md).

**The peer-process check** gives editor heartbeats `via = editor`. On Windows the agent reads the owning PID of the connection's client port with `GetExtendedTcpTable`; on Linux it maps the socket inode through `/proc/net/tcp` and `/proc/<pid>/fd`. The heartbeat passes when that process is wakatime-cli and its parent chain reaches an editor process. Otherwise it is `editor_unverified` and level C. This keeps honest hand coding on a Windows laptop at level B, as SM intends, while a script posting heartbeats straight to the local port earns level C. macOS skips the check because its key-down idle time gives keyboard evidence directly; if that call turns out to need Input Monitoring permission (SM 1.2 leaves it to verify), the check is added for macOS through `libproc` with purego.

Where a native adapter covers the same session (matched by `ai_session` and time), native events win and the bridge events become a cross-check: a prompt-count difference raises `bridge_mismatch` (HS DI 8). Bridge prompts carry no text, so the challenge can't draw them; their minutes are level C unless corroborated, which keeps them out of submissions by default. No heartbeat becomes an `agent.stop`, so in a session only the bridge reports, a turn runs from its prompt to its latest bridge edit, and nothing the session credits opens a lookup (SM 1.3, 2.5).

### 5.8 Git

Git is the join key between time and shipped work (HS 13.3). The client side (AD 4.8; EV 4.6; TE 4.6):

- **Discovery.** Every `cwd` a harness session reports and every editor heartbeat's project is resolved to a repo once with `git rev-parse --git-dir`, which also handles worktrees and submodules. The teen never registers a repo.
- **Watching.** A non-recursive watch on each `.git` sees commits, amends, rebases, checkouts and resets without watching working trees (CP 3.2). On Git 2.54 or later, config-based `post-commit` and `post-rewrite` hooks make it event-driven; `core.hooksPath` is never set, because it would replace every repo's own hooks (SP 4.1).
- **Identity.** A project is its sorted root-commit set, stored on the server only as `HMAC(pepper, root_sha)`. Folders without git get a salted path hash and a name the teen picks (SP 4.2).
- **Commit records** carry SHA, parents, author and committer times, `observed_at`, `--shortstat` against the first parent, optional path HMACs, `credited_seconds_since_parent` by activity kind, and `rewrites[{old_sha, new_sha}]` from the reflog, so credited time moves with a rebase instead of vanishing or doubling (SP 4.4). `actor = agent` when an agent tool ran the same git command within 2 s (SM 1.2). Agent trailers and git-ai notes under `refs/notes/ai` are read and labeled; their absence proves nothing (HS 13.3).

The server side:

- Each commit gets three clocks: `observed_at` (the laptop's), `server_first_seen_at` (pairtime's), and a forge time: GitHub's push timestamp where the GitHub App is installed, otherwise the time `pairtimed` first found the commit on the forge (EV 6.3).
- Public repos on GitHub, GitLab and Forgejo are verified with one lookup per SHA, which confirms the SHA and the diffstat; GitHub ranges use the compare API. Lookups for a commit not yet on the forge retry with backoff for 24 hours, with a server token at GitHub's 5,000 requests an hour (SP 4.1). Every submitted project's Code URL must be public under the handbook (HC 3.7), so every submitted project can be forge-verified (EV 6.1).
- The GitHub App asks for Metadata and Contents read on repos the teen picks. For private repos the server drops the `added`, `removed` and `modified` path arrays of each push payload on receipt. Codeberg OAuth tokens are discarded right after sign-in (EV 4.6).
- Flags: `backdated` (author time far older than `server_first_seen_at`), `few_commits` (the handbook's "a 20-hour project with only one commit does not follow the guidelines"; HC 3.7), and `unexplained_lines` from CC3 (7.3).

### 5.9 Install flow and doctor

`pairtime install`, run by either install script or the VS Code extension, takes under two minutes:

1. Verify the signed release manifest's SHA-256 (CP 4.6). No admin rights anywhere. The install script checks the manifest's signature, and the SHA-256s it gives for this computer's archive and binary, before it runs anything it downloaded. It installs an unsigned release only when told to allow it (docs/spec/update-manifest.md). The VS Code extension (5.11) carries its binary inside the extension, so it checks that binary against the manifest's `binary_sha256` itself before it runs it.
2. Detect harnesses, then show one consent screen with every planned change as a diff (`--dry-run` prints only the diff): the autostart entry, hook entries per harness, Karim's entry for each harness no hook or log reader sees and for any harness named with `--track`, with its cost line (5.4), the `~/.wakatime.cfg` line, the coach skill files, and on GNOME pairtime's GNOME Shell extension, which tells the agent which process owns the window in front, never its title or anything else about it, answers no other program, and starts working at the next login. The teen can untick any item.
3. Put the binary at the per-user path (CP 7), write autostart and start the agent.
4. Merge hook entries. Print the Codex `/hooks` trust instruction and Cline's "Enable Hooks" step in plain words when those harnesses are present.
5. Edit `~/.wakatime.cfg` (5.6). If wakatime-cli is installed, send a test heartbeat with entity `test.txt` (Hackatime stores it as a `test_entry`), confirm the agent received it, and confirm wakatime-cli reported no error for Hackatime.
6. From 2027-01-01, request the OS age signal (8.2).
7. Sign in with a device code, unless the teen picked local-only mode or the server isn't open yet (9.7).
8. The onboarding cards: the profile choice and the consent cards (8.3, 8.4).
9. Run lookback over 30 days at low CPU and I/O priority with a progress bar, then open the welcome recap (TE WP4.7).
10. Offer the recovery code (7.11).

`pairtime doctor` prints one line per check: agent running; idle detection working with its source, or "presence unknown on this desktop"; focus detection working with its source, or "focus unknown on this desktop, so time here counts at level C"; hook round trip in ms per harness; Codex and Claude Code trust state; both WakaTime destinations reachable; last upload and clock offset; signing state of the binary; adapters found with parse-failure counts; `.jsonl.zst` rollouts skipped; for T3 Code, whether its store is being read, how many relayed messages it witnessed today and how many rows the age test turned away, or why it isn't read: no `sqlite3` on PATH, with the command that installs it on this OS, a store whose schema the probe refuses, or a T3 Claude instance whose own Claude home loads none of pairtime's hooks (5.1). The T3 line names no path, so `doctor --report` stays clean. The idle and focus lines come from the running agent's `GET /v1/status`, so `doctor` reports what the agent reads, not what the terminal it runs in can reach. "presence unknown on this desktop" appears wherever pairtime reads no idle time, such as with no graphical session or on a Wayland compositor without Mutter's `IdleMonitor` or `ext-idle-notify-v1`. On GNOME on Wayland, idle time comes from Mutter's `IdleMonitor`, and pairtime learns which window is in front only from its own Shell extension, so until that is on, the focus line there also says how to turn it on. On KDE and wlroots, focus stays unknown in v1. Where the client can't see which window is in front, `pairtime today` and the dashboard's Today page say: "This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs." On GNOME they add: "Log out and back in once to turn on pairtime's GNOME extension", or, if the extension was unticked at install, "Run pairtime install again and keep pairtime's GNOME extension." Until WP-SC8 credits that time at level C, they stop the sentence at "in front". `pairtime doctor --report` writes a redacted footprint report (RSS, idle CPU, wakeups, hook latency, disk type, OS build) that the teen can choose to share; its test plants usernames and paths and finds none in the report (TE WP7.9). Those reports are the R8 evidence.

### 5.10 Program-side integrations

| Program stack | How pairtime plugs in | Who has to agree |
|---|---|---|
| Any program, no code (tier 0) | The teen creates the submission on pairtime and pastes the review URL into the program's form; the reviewer opens it and copies the generated text (9.3) | The program author and its reviewers, who accept pairtime numbers; the handbook already allows an alternate tracking method (HC 3.7 point 1) |
| Airtable-based programs | `integrations/airtable-automation.js`: a "run script" automation that takes the record's pairtime URL, calls `GET /api/v1/submissions/:id/justification?format=handbook` with the program's token, and writes the fields named in a mapping at the top of the script. Defaults use the wrangler base's names (HC 3.7) | The program author |
| Rails programs with a `HackatimeService` (Stardance, Flavortown, Summer of Making pattern) | A second instance pointed at `https://<host>`, answered as in the table below | The program author |
| Programs on Robert (the-carnival, pixl, trmnt, Alchemize and others) | Unchanged: the program keeps sending `kind: "hackatime"` with the same Hackatime names, which describe the same activity because of the dual-send. Robert's `creditedSeconds` stays Robert's figure for fraud scoring; the program's hours figure comes from pairtime. A `kind: "pairtime"` proposal with `pairtimeSubmissionId` and a bundle fetch goes to the Telescreen team in week one (EV 8.2; JB graft E9) | Nobody for v1; the Telescreen team for the `pairtime` kind |
| Programs on Ari (3am-ysws, hackxpansion, disposition, macondo) | Until Ari has a `pairtime` source: send the submission through the journals bridge below, not as `hackatime_projects[]`, so the same time isn't paid twice, and not as `program_seconds`, which Ari treats as evidence-free (HC 3.6). Target: a `pairtime` time source in `src/lib/review/settlement.ts` | Ari's maintainers; Ari has no license in its repo metadata (HC OQ 7) |
| Programs with screen evidence (Lapse, Lookout) | Complementary. A reviewer may ask for a Lookout session for a large claim, and the review page links it | The program |

**Hackatime-compatible reads.** Every call Stardance's `HackatimeService` makes (HC 1.8) is answered or deliberately refused, and these reads are on the never-cut list (JB must-fix 4):

| Call | pairtime's answer |
|---|---|
| `GET /api/v1/authenticated/me` | The pairtime user for the program's OAuth token, in Hackatime's shape |
| `GET /api/v1/authenticated/api_keys` | A scoped, read-only token with a `ptr_` prefix that can call stats, spans and `my/stats` for this user and this program only; never an ingest credential (HC DI 9) |
| `POST /api/admin/v1/user/get_user_by_email` | With the program's admin key: the user id, only for users connected to that program who granted `email:read`; otherwise 404 |
| `GET /api/v1/users/:uid/stats`, `GET /api/v1/users/my/stats` | `features=projects`, `start_date`, `end_date`, `total_seconds=true`; `filter_by_project` as an array (`filter_by_project[]=a&filter_by_project[]=b`) or a comma string; `test_param`, `no_ai_coding`, `boundary_aware` and `filter_by_category` accepted and ignored. Seconds are credited seconds for the Hackatime names the teen linked. An extra `pairtime` object carries `logged_s`, `handbook_s`, `ai_logged_s`, `witnessed_s`, `offline_s`, the share by evidence level, `scoring_version` and `final`; existing clients ignore unknown fields. The embedded `trust_factor` is always the unscored value |
| `GET /api/v1/users/:uid/trust_factor` | Always Hackatime's unscored `blue` value, in Hackatime's shape, taken from its source at the pinned commit. pairtime shares no trust levels with programs (JT must-fix 14) |
| `GET /api/admin/v1/user/info_batch?ids=` | Up to 2,000 comma-separated ids; for connected users, ids, usernames and the unscored level |
| `GET /api/v1/users/:uid/heartbeats/spans` | Credited intervals as spans, per linked project |
| `GET /api/v1/my/heartbeats` | `200` with an empty `heartbeats` list and the header `X-Pairtime-Note: heartbeats-not-stored`. pairtime keeps no heartbeats on the server, and Stardance uses this call only to check that Lookout time arrived |
| `POST /api/hackatime/v1/users/current/heartbeats.bulk` | `403` with `{"error": "pairtime accepts time only from the teen's own client"}`. Programs can't write time into pairtime |

`:uid` resolves a numeric id, a Slack id, an HCA id, a username or `my`, in Hackatime's order (SP 10.5). Reads answer only for users connected to the calling program. Contract tests use request shapes captured from Stardance's client at `b1d3e0eac1ae` and response shapes from Hackatime's controllers at `43bde10b02db` (HC 0, 1.8).

**Linking project names.** Programs think in Hackatime project names. The agent knows which WakaTime `project` names belong to which pairtime project, because it sees both the heartbeat and the repo. When a teen connects a program, the agent proposes the links ("Hackatime `remind-bot` is pairtime project `maya/remind-bot`"), the teen confirms, and the server stores each name as an HMAC (AD 4.7).

**Frozen numbers.** Programs re-pull each devlog's window and expect the same answer later (HC DI 8). Credited seconds for a window can still rise for 24 hours after it ends, when a progress event restores half-weighted seconds (SM 4.3). Reads therefore return `final: false` for windows that ended less than 48 hours ago. For payouts, programs create a submission, which freezes its numbers (7.5).

**The Ari journals bridge.** Ari pays every `journals[]` entry in full unless its reviewer cuts it, and adds all of a ship's entries together. An entry holds at most 86,400 whole seconds and needs `text`, and a ship holds at most 200 entries (`testdata/upstream/ari/`). So the bridge sends one entry per row of a checked submission (D13; 7.5 step 9), valued as pairtime's review stands when the program builds the ship: a work-session row at its submittable seconds less any cut, a loop row at 0 until a reviewer raises it (D12), and held minutes only once a reviewer clears them (D7). A raise or clearance made after the ship went to Ari reaches it only when the program ships again. An entry has `at`, the row's end in RFC 3339 UTC; `seconds`, the row's value rounded down to a whole second, because pairtime credits in half-seconds and Ari refuses fractions; `text`, a line with "Tracked by pairtime" and the row's start, end and seconds; and `markdown`, the same with the row's evidence link. Neither holds anything SM 1 keeps on the device. A row whose value rounds down to 0 sends nothing, and the bridge refuses a submission with no row left to send. The bridge's entries are the only ones in the ship that carry seconds: a program that also lists its devlogs in `journals[]` sends them at 0 s, and they count toward the 200.

A row over 86,400 s, which takes more than 24 hours with no gap over 20 minutes between credited minutes, is cut at each UTC midnight. Each part's `text` says "part k of n" and gives the whole row's start, end and seconds. No part can pass 86,400 s, because no second is credited twice (SM 3.1, 3.4). A ship that would pass 200 entries gets one entry per UTC day instead, which fits for the same reason: `at` is the end of the day's last credited slice, `seconds` the day's half-seconds summed and then rounded down, `text` the date, the seconds and the number of rows, and `markdown` each row's link. If that still passes 200, the bridge refuses, and the program splits the window into two submissions. Midnight cuts and day totals come from the submission's frozen minute buckets, which the server serves per row and UTC day, never from a row's total split pro rata. A cut typed as a number comes off the row's last minutes. On a ship with collaborators, each entry carries the email of the person whose submission it comes from, and the fallback is one entry per person per UTC day.

### 5.11 The VS Code extension

In v1 the extension is an install channel and a status bar. Per-platform VSIX packages bring the signed binary, the first activation shows the consent screen and runs `pairtime install`, and the status-bar item reads the local `statusbar/today` endpoint (CP 4.2; EV WP5.4; JB graft T10). It reaches VS Code, Cursor, VSCodium and Devin Desktop through the Marketplace and Open VSX. TE D10's change-shape events are not in v1. If they ship later, they carry the reviewer label `editor_typing`, `via = plugin`, and never set `input_keys` or raise an evidence level (JB must-fix 3; JT, Teen-first design).

## 6. Scoring

### 6.1 What is adopted

The struggle-metric spec is the scoring rulebook, whole: the event model (SM 1), the three tests for an engaged second (SM 2.7), presence and the return rule (SM 2.1), prompt origin rules 1 to 8 (SM 2.2), anchors and leads (SM 2.3), the reading bucket (SM 2.4), focus and lookups (SM 2.5), lookback (SM 2.8), one timeline per user (SM 3), the four dark-flow detectors and the no-progress half rate (SM 4), the minute bucket, reason codes, rollups and one-third accounting (SM 5), the calibration plan and versioning (SM 7), and the parameter table (SM 8). Its best property for adoption is that a reviewer can recompute any credited minute by hand from the events listed against it (SM 5.2), which is what lets a program trust a new tracker without trusting its authors (AD 5). Content classifiers and typing-cadence scores stay out of the number (SM 4.5).

Two implementation rules apply to all of it. `pkg/score` uses integers only, with times in milliseconds and credit in half-second units, so a re-score matches to the bit on every platform (EV risk 9). And it is a resumable state machine whose state serializes deterministically (3.7, 6.5).

### 6.2 Deviations from SM

| # | SM baseline | This design | Reason and source |
|---|---|---|---|
| D1 | SM says when a minute is scored, not when it stops changing | A bucket is `pending` for `PENDING_WINDOW_S` = 900 s after its minute; late events re-score it and the client uploads a new revision (`rev`). Then it is `final`, apart from `provisional` and `restored`. Every rule decides a second from what is on record within `READ_HORIZON_S` = 600 s after it, and an act is on record at the later of its own time and its record's `t` (3.7) | Leads reach back 420 s, reading needs an anchor within 600 s, and the return rule one within 600 s of the start of the silence (SM 2.1), which leaves 300 s for bridge heartbeats, transcript lines and drained inboxes that arrive late (TE D6; AD D1). A rewrite candidate's reading and return credit (SM 4.2) and relayed pairings (SM 2.3, 2.4) reach further, inside the 900 s (3.7). Lookups (SM 2.5) still need a bound (3.7). Public pages show only final minutes; the owner's dashboard shows pending ones lighter |
| D2 | Evidence levels depend on OS signals only (SM 5.1) | Levels also depend on the anchor's provenance `via`. `track`, `plugin` and `bridge` anchors are level C unless `input_keys` corroborates them, or the harness's own log does, in which case the log record's anchor counts instead; relayed anchors are level C whatever backs them (SM 2.2 rule 9); editor heartbeats that fail the peer-process check are level C; plugin and bridge output sizes fill the reading bucket only up to `R_TURN_BASE_S` without a hook or transcript record (6.4) | Closes the credit pumps on `input_any` machines (JT must-fix 1; JT, What breaks, on AD D3 and EV D4) |
| D3 | `cli.track` is "never an anchor on its own" (SM 1.2) | A gated anchor under the four conditions in 5.4, level C by default, `via_track` on its minutes | Hookless harnesses would otherwise earn only editor and terminal anchors (R7). Level C keeps a scripted `track` loop out of submissions (TE D9; JB graft T7; JT graft 1) |
| D4 | Lookback leads shrink to typing time (SM 2.8); bridge prompts aren't covered | Bridge prompts get the typing lead only, never the think lead, and are level C unless corroborated | No text means the re-roll and fix-it detectors can't run on them, so the full lead would let loops back in (AD D2) |
| D5 | Level-C minutes are recommended at `min(credited, ai_logged / 3)` (SM 5.4) | Same cap, plus: submittable only if the program opts in to level C, and only inside a commit interval whose closing commit is forge-verified | A forge-verified commit is the only outside evidence that anything happened in that interval (EV D5; TP A6 U6) |
| D6 | All late time is `backfilled` (SM 5.1) | A third witness label, `offline` (7.4): counted at its computed level up to `OFFLINE_CAP_S` = 10,800 s per submission whatever the flush delay, only if a commit in its interval reached the forge within `OFFLINE_PUSH_S` = 86,400 s of reconnecting, banded below witnessed, never pre-approved. Beyond the cap or without the push it is backfilled | Serves the teen who syncs at school the next morning without giving a client that fakes an outage 36 hours to build a consistent fake (EV D6, capped as JT must-fix 2 asks; TP A6 point 1) |
| D7 | The server re-scores submitted windows and flags a mismatch (SM 7.6) | Minutes the server can't reproduce, or whose chain doesn't verify, are held: not credited until a reviewer clears them | A minute that can't be reproduced from evidence pinned at the time shouldn't count by default (EV D7) |
| D8 | A per-event `chain_hash`, heads every `CHAIN_HEAD_INTERVAL_S` = 300 s (SM 1.1, 8.6) | Ledger records with header and nonce-protected body, Merkle batches every `BATCH_S` = 60 s, signed receipts (3.6). `WITNESS_LAG_S` stays 900 s | Merkle batches allow the project-scoped reveal; 60 s narrows the room for "fabricate it live, a little late" and matches the 60 s sync (EV D2) |
| D9 | Buckets carry `anchors` (SM 5.1) | Anchor ids stay on the device | The server doesn't need per-minute act counts for logged-only time; it recomputes them from the reveal at submission (EV D1) |
| D10 | Only `STREAK_DAY_S` = 900 is defined (SM 8.6) | Build days: a local day with at least `BUILD_DAY_S` = 900 credited seconds at level A or B that the server received within `BUILD_DAY_LAG_S` = 259,200 s. Consistency is build days in the last 30. A weekly goal (default 3), weeks in a row, one freeze per 7 build days, at most 2 held, spent automatically on a missed week. Club boards rank build days over 90 days. A profile time zone change takes effect at most once per 30 days. No daily streak counter | Daily streak counters produce one-contribution token days, and forgiveness keeps streaks from becoming anxiety (TE D1; PA 4.1). The 72 hours shut out lookback and after-the-fact fabrication while keeping the day of a teen who syncs at school |
| D11 | Lookback minutes are visible as level C (SM 2.8) | Level C, from lookback or live (side doors without corroboration, `editor_unverified` anchors, focus unknown, relayed messages), never reaches public totals, build days or boards. Lookback shows in the welcome recap and as a "before pairtime" band; live level C shows, labeled, on the teen's own dashboard, recaps and chess clock, with 5.9's sentence where focus is unknown and with `via_relay` on relayed minutes (SM 2.2 rule 9). A submission takes relayed minutes only under a program's level-C opt-in and D5 | Every public number means the same thing for every teen (TE D2). A relayed minute has the app's word behind it and no OS evidence (D-relayed) |
| D12 | Reviewers can only cut (SM OQ 5) | In a submission, each dark-flow loop (SM 4.2) becomes a row of its own. Its `captured_s` is every second the loop cost, under `X.reroll`, `X.rewrite_loop`, `X.same_error` and `X.autopilot` together, the row shows each code's seconds, and its default approved value is 0. A reviewer may raise one up to `captured_s` with a reason | Undoes dark-flow false positives inside Ari's cut-only rule, with no new kind of review action (TE D12; JB graft T5). Replaces EV D8 |
| D13 | Submission rows are commit intervals (SM 5.3) | A submission is a list of devlog windows, each the credited time between two consecutive devlogs on the project (one window if the teen has none). Rows inside each window are work sessions (SM 5.3's own definition), each listing the commits it touched, with a per-commit tab | Programs review per devlog and Ari ingests `journals[]`; work sessions are sitting-sized rows a reviewer can cut finely (AD D5; TE D7; JB graft T4) |
| D14 | Presence sampled every 15 s at all times (SM 2.1) | Sampled only while a project context is live, slowing to one sample a minute after 600 s with no input and no running turn, with an immediate sample on any hook, editor heartbeat, focus or power event (3.3) | pairtime doesn't record general computer use. No credit changes: every creditable second is project-focused, and idle APIs report the full gap at the next sample (EV D10; JE, EV old-laptop note) |
| D15 | `ai_logged_s` is reconstructed from agent events (SM 5.4) | When the dual-send has the teen's real heartbeats for the window, `hackatime_logged_s` is computed as Hackatime's boundary-aware path does (one global order over the linked names, `min(gap, 120 s)`, `browsing`, `meeting` and `communicating` excluded) and split by the `ai coding` category as WakaTime labeled it; `handbook_s` uses that split. The agent keeps `(time, project HMAC, category)` per heartbeat for 180 days, paths for 30. SM's reconstruction is the fallback | The handbook's rule applies to time WakaTime categorizes as `ai coding`, so a figure from the same heartbeats Hackatime received is the one a reviewer can reproduce (AD D4; JB graft). Implemented in `pkg/wakacompat`, pinned to Hackatime `43bde10b02db` and tested against it |
| D16 | The key counter's default is an open question (SM OQ 2) | The typing module is opt-in and off by default, with "Not now" the most prominent answer. Most Windows and X11 minutes are level B | A global key listener looks like a keylogger to antivirus and to privacy-conscious teens, and minors' consent rules make refusal the most prominent choice (TE D3; EV 5.2; TP B2 point 7) |
| D17 | No VM rule (SM 4.5 lists VM labels as tier 2) | On a machine labeled VM, minutes that are level A only because of the typing module drop to level B. The label never lowers credit | A VM's virtual keyboard launders injected keys into "hardware" inside the guest, which the typing module makes more valuable (JT must-fix 11). The label uses the Hyper-V root-partition test (non-zero CPUID leaf `0x40000007`) so Windows with VBS isn't misread (TP DI 11) |
| D18 | Dark-flow inputs exist only on the device (SM 4.2) | Keyed forms of the content-derived inputs are committed in each record's features `f` (3.6), so the server can recompute every discount without text. They stay the client's claim, which CC8 checks against revealed text only where no key is needed (7.3) | A modified client that hides its re-rolls would otherwise pass re-scoring; with them it has to forge the committed features, which 7.12 accepts as T9 residue (EV 6.2; JT C6) |
| D19 | `logged_s` and `ai_logged_s` count every editor heartbeat and agent event in the window (SM 5.3, 5.4) | They, and D15's `hackatime_logged_s`, count only records with `via` in `hook`, `log`, `otel` or `editor`, plus bridge records from heartbeats that passed the peer-process check. `track`, `plugin`, `app` and `editor_unverified` records never count, so no `relay.message` does | Level C is capped at `ai_logged / 3`, so a fake side-door stream that counted toward `ai_logged_s` would raise the ceiling on its own credit. A `track` call every 20 s would otherwise make `ai_logged_s` close to wall time in a program that opts in to level C (this design, closing the last part of JT must-fix 1) |

Everything else in SM, including every value in SM 8, is unchanged. SM's open questions this design takes a position on: OQ 2 (D16), OQ 5 (D12), OQ 8 (level C is allowed only by program opt-in, at the third, inside forge-verified intervals: D5) and OQ 9 (one credited total, with the AI-assisted and hand-work split as evidence, SM 5.4). OQ 1, whether level B is "significant evidence", goes to Quality and Integrity in week one (13.2).

### 6.3 New parameters

These join SM 8's table, its hash and the static check that every parameter `pkg/score` reads is in the table (TE WP0.6):

| Parameter | v1 | Use |
|---|---|---|
| `PENDING_WINDOW_S` | 900 s | D1 |
| `BATCH_S` | 60 s | D8 |
| `CHECKPOINT_S` | 900 s | Scorer checkpoints (6.5) |
| `CONTEXT_IDLE_S`, `SLOW_SAMPLE_S` | 600 s, 60 s | D14 |
| `TRACK_INPUT_PRE_S`, `TRACK_MIN_GAP_S`, `TRACK_LOG_MATCH_S`, `TRACK_DEDUPE_S` | 30 s, 20 s, 10 s, 30 s | D2, D3, 5.2, 5.4 |
| `OFFLINE_CAP_S`, `OFFLINE_PUSH_S` | 10,800 s, 86,400 s | D6 |
| `BUILD_DAY_S` (renames `STREAK_DAY_S`), `BUILD_DAY_LAG_S` | 900 s, 259,200 s | D10 |
| `WEEKLY_GOAL_DEFAULT`, `FREEZE_EARN_DAYS`, `FREEZE_MAX`, `BOARD_WINDOW_D`, `CONSISTENCY_WINDOW_D` | 3, 7, 2, 90, 30 | D10 |
| `LONG_FIGHT_MIN_S` | 1,800 s | The coach's `long_fight` moment |
| `REVEAL_POST_S` | 900 s | 6.5 |
| `CHALLENGE_K_MIN`, `CHALLENGE_K_FRAC`, `CHALLENGE_K_MAX` | 10, 0.1, 30 | 7.5 (TP B6). For a pool of `P` items the draw takes `CHALLENGE_K` = `min(CHALLENGE_K_MAX, max(CHALLENGE_K_MIN, ceil(CHALLENGE_K_FRAC × P)))` of them, which is 10 up to a pool of 100 and 30 from 291. The draw computes `CHALLENGE_K` from these three, so no table holds it |
| `PREAPPROVE_ROW_MAX_S` | 3,600 s | 6.4 |
| `COMPOSE_NEEDS_KEYS` | false | When true, the typing part of a lead is credited only where `input_keys` exists, on every machine (6.7) |
| `AUDIT_BASE_P` | 0.08 | 7.9 |

### 6.4 Evidence levels and bands

This is the one published definition, used in the first Quality and Integrity conversation and on every review page (JB must-fix 3). It is served at `https://<host>/docs/evidence` and versioned with the scoring version.

**Levels, per credited second.** A second credited by an anchor's window takes that anchor's level. A second credited from the reading bucket takes the level of the human anchor that qualifies the drain (the nearest one within `READ_HORIZON_S`). A lookup takes the lower level of its two bracketing anchors.

| Level | All of these hold |
|---|---|
| A | The second is witnessed, or offline within D6's conditions. Presence comes from OS input. The anchor is keyboard-backed: `input_keys` is true in its evidence window, from the macOS key-down idle time or from the opt-in typing module with injected input excluded, and not from the typing module on a VM-labeled machine (D17). The anchor's `via` is `hook`, `log`, `shell`, `git` or `editor`, or it is a `track`, `plugin` or `bridge` anchor with keyboard input in the host window in the 10 s before it |
| B | Witnessed, or offline within D6's conditions. Presence from OS input, with keyboard-specific input unavailable (`input_any` only). The anchor's `via` is `hook`, `log`, `shell`, `git` or `editor` |
| C | Anything else: backfilled or lookback minutes, `track`, `plugin` and `bridge` anchors without keyboard corroboration, `editor_unverified` anchors, relayed anchors (SM 2.2 rule 9) and the reading they qualify, and offline minutes beyond the cap or without the forge push |

A side-door event that a pairtime log reader matches gets no level of its own: the log record's anchor counts instead, at that record's level, and the side-door event is a cross-check (5.2).

A relayed anchor is a message the person sent through an agent app, read from the app's own store with `via` `app`. Its typing time and its session's reading count whatever this device saw of the person, unless the machine was asleep, and always at level C (SM 2.2 rule 9, 2.4).

Labels ride along with levels and never change them: `via_track`, `via_plugin`, `via_bridge`, `typing_module`, `vm`, `editor_typing` (v1.5 at the earliest), and `via_relay`, an origin label that every relayed anchor carries.

**Bands, per credited minute of a submission**, computed from public inputs only, so the teen's copy of the review page shows them too (EV 6.7, D9):

| Band | Rule |
|---|---|
| Corroborated | Witnessed, not offline. Level A, or level B in a row that also has editor acts passing CC10. The anchor was seen by two records written by different code paths: hook and transcript, hook and OTel, or an editor heartbeat that passed the peer-process check plus a commit whose diffstat covers the file. The row passes CC6, CC7 and CC9 |
| Witnessed | Witnessed, level A or B, and misses a corroborated condition |
| Offline | Offline, within D6's conditions, level A or B |
| Backfilled | Level C, or late beyond D6's conditions. Shown with the handbook-third cap applied. Relayed minutes sit here because they are level C, though their witness label is the live one |

Held minutes (D7) sit outside the bands and aren't credited until a reviewer clears them.

**Program policy presets.** A program may set a preset, and the justification text names it. A preset may pre-approve only rows that are entirely corroborated and level A, with no flags and `captured_s` at most `PREAPPROVE_ROW_MAX_S` = 3,600 s. No preset pre-approves level-B, offline or backfilled minutes (JT must-fix 10; JT, Evidence-first, on the preset). Must-view rows and one random clean row are still opened before "approve remaining rows" unlocks.

### 6.5 Exact re-scoring

At submission the server re-scores the window from revealed records with the same `pkg/score` at the frozen version and parameter hash, and compares slot by slot with the buckets it received live (CC6). Exactness needs every input the rules read, including other projects' records that compete for the same seconds (SM 3.2, 3.3; JE must-fix 7). So:

- **Checkpoints.** At every UTC minute divisible by 15, for each project with records in the last 24 hours, the scorer seals its serialized `score.ProjectState` into the ledger: that project's sessions' reading buckets, the keyed sketches inside the re-roll window, error-signature counts, `since_progress`, autopilot and rewrite counters, and half-weighted seconds awaiting a restore, and the shareable fields of the `os.focus` in force (`app_class`, `hosted_sessions`, `focus_project`), so a range that starts long after the last focus change, as in SM 6.4's night or on a desktop that hides focus, knows what had focus. It also holds the state of SM 4.2's second scoring, the one with no turn discounted: its own copies of those reading buckets, its `since_progress` and half-weighted seconds, and its credited seconds that can still bracket a lookup. A re-score then gives each loop the seconds it cost live. The state uses keyed values only.
- **Range.** For each project `p` in the submitted set `P`, re-scoring starts from `p`'s latest checkpoint at or before `W.start` minus 900 s, and runs to `W.end` plus `REVEAL_POST_S` = 900 s, plus `P`'s own progress events after `W.end` up to the freeze, at most 86,400 s later, for restores (SM 4.3).
- **Reveal set.** Full bodies for: (a) records whose header project is in `P`; (b) every `os.presence`, `os.focus` and `os.power` record; (c) every `editor.activity` record of any project, because SM 3.2 resolves an editor slot to the project of the most recent heartbeat; (d) every record of a harness session listed in an `os.focus` `hosted_sessions` together with a session of `P` during the range, because SM 3.2 resolves a multi-tab terminal by the next anchor of each hosted session, plus the checkpoints of those sessions' projects; (e) the checkpoints for `P`; (f) when the range holds a slot whose focus or presence is unknown (SM 2.5, 2.8), every record of every project in the batches that overlap the range, plus those projects' checkpoints, because in such a slot every project's windows and buckets compete for each second, and the batch headers show the server which projects those are; (g) every upload-failure note in the batches that overlap the range, and, for each catch-up a revealed batch belongs to, the `os.presence`, `os.focus` and `os.power` bodies of the batch before it, and, while that batch itself came in a catch-up, of the batch before that catch-up too, and so on. A note's body holds only a time and a reason code, and those presence, focus and power bodies are the kinds (b) already opens. Every other record in an overlapping batch: header and body hash only.
- **What the teen is told.** The submission preview says what this reveals about other projects: how many records of each kind they had in the range; the times and opaque project keys of their editor heartbeats; and the full metadata, never content, of any other project's harness session that shared a terminal window with the submitted project; and, when pairtime couldn't see which window was in front, or wasn't running yet, at some time in the range, the full metadata, never content, of every record of every other project in the range.
- **Tests.** SM 6.3's fixture and a two-tab variant (two projects' Claude Code sessions in tabs of one terminal window) must re-score to the half-second, and a modified client that moves a minute from one project to another must fail CC6. WP-FX8's two projects on a desktop that hides focus must re-score to the half-second too, and so must a range that starts at a checkpoint long after the last `os.focus`.

### 6.6 Scoring-version policy

JE must-fix 6, as rules:

- `scoring_version` is `MAJOR.MINOR.PATCH` (SM 7.6). MINOR changes parameter values only: a new table in `pkg/params/tables/<version>.json` and no code. MAJOR changes a rule or adds a feature, and is rare: at most one per quarter after 1.0.0. PATCH fixes a bug with no output change on the regression corpus. While the version is 0.x, before the 1.0.0 freeze (6.7), any change that moves outputs raises MINOR, a rule's included, and PATCH stays for fixes that change no output (D-score-version-0x).
- Every binary ships every parameter table (each about 2 KB), and every bundle carries the table it used, identified by its SHA-256.
- Client and server carry the rule code for the current MAJOR and the one before it. When the version changes, open submissions are re-scored and decided ones keep their numbers (SM 7.6). `pairtime verify` handles bundles at its own MAJOR. For an older MAJOR it exits 3 and prints the release built from that version's tag, which verifies it. Until 1.0.0 it keys per MINOR instead, because a 0.x MINOR can change a rule: it handles bundles at its own 0.MINOR, and for an older MINOR it exits 3 and prints that release the same way (D-score-version-0x). The verifier never compiles in every version (JE must-fix 6, rejecting EV 6.6's approach).
- `score-regression` runs in `ci.yml`: SM 6's six timelines and the red-team fixtures against goldens, failing when outputs change without a `pkg/score/VERSION` bump in the same PR (EV WP1.7).
- When an SM worked example doesn't reproduce, the PR stops and records an erratum, which the orchestrator decides for Karim outside that PR (AGENTS.md). It never tunes the rules to the fixture and never edits SM to pass its own test (TE WP2.8; JE graft 4). This replaces the "fix whichever is wrong" sentence in PR1's AGENTS.md.

### 6.7 Calibration

SM 7 is the plan, and it gates the first decisions (JB must-fix 2):

1. **Recruit** 40 to 60 Hack Clubbers aged 13 to 18 for the labeled pilot, at least 40% on Windows including pre-2017 laptops, across Claude Code, Cursor, Codex, Copilot and Gemini CLI and all six shapes in SM 6. Self-reported age bands (13 to 14, 15 to 16, 17 to 18) are collected only from volunteers who consent; a fit that shows younger teens under-credited changes a global parameter within its source range, never a per-age one (TE D11).
2. **Shadow week** from 2026-12-01 with the alpha: score everything, show recaps only, numbers hidden until calibrated (SM 7.5).
3. **Labeled sessions** from 2026-12-07 to 12-27, over winter break. Each volunteer offers at least five sessions with a screen recording on (a Lookout timelapse or any recorder they pick). Two raters label minutes in SM 7.1's categories in pairtime's labeling mode, beside the recording and the timeline, and a third adjudicates. The mode computes Cohen's κ in the app (EV WP4.10). If κ on the shared 20-session calibration set is under 0.6, the label guide is revised before main labeling. "Was this right?" answers shared with consent rank which sessions to label first and never change credit (TE D8).
4. **Fit and freeze** from 12-28 to 2027-01-03: sensitivity analysis, then fit the parameters that move session error most, inside their source ranges, with SM 7.3's asymmetric loss and leave-one-participant-out validation. Tag scoring `1.0.0` by 2027-01-04 and publish the parameter table, the sensitivity table and the comparison against SM 7.3's four baselines: Hackatime raw, the handbook third, grit's rules and Claude Code's `active_time{type="user"}`.
5. **No decision before the freeze.** Submissions can be created and reviewed under 0.x, but `POST /api/v1/submissions/:id/outcome` returns `409 scoring_not_frozen` until the instance runs 1.0.0, and 0.x submissions are re-scored to 1.0.0 before review.
6. **After the freeze**, reviewer cuts and D12 restores add labels in both directions, and seeded known-answer cases measure the reviewers (EV 5.4). They add to the labeled set and never replace it, because cuts are anchored on pairtime's own number. MINOR refits come at most monthly, after at least 300 newly decided submissions.
7. **Level B.** The pilot reports approved over credited by evidence level. If level-B hours are approved below 0.8 times the level-A ratio, the next MINOR version sets `COMPOSE_NEEDS_KEYS` to true (JT must-fix 10).

Pilot labels come only from participants who opted in for that purpose, because New York's law treats research and development as needing consent (TP B2 point 7; EV 5.4). Timelapses and transcripts in the labeled set are encrypted, visible only to assigned labelers, and deleted 30 days after labeling (SM 7.2).

### 6.8 R3 proof metrics

Two numbers from tier-1 data, with no extra telemetry (TE 2; JB graft T3):

- **Four-week retention**: of devices whose first upload falls in week `w`, the share that uploads in week `w + 4`. Target: 50% for the alpha cohort.
- **Never-submitted share**: credited seconds on projects that never appear in a submission, over all credited seconds, per monthly cohort. Target: at least half.

The server computes both weekly, and they go to HQ with the R2 report. Local-only installs aren't counted, and the metric starts when the server opens to sign-ups (9.7).

## 7. Trust and audit

### 7.1 Where trust comes from

The attacker is a motivated 16-year-old with an LLM, the AGPL source, a Windows laptop, a $10 jiggler and an afternoon, for whom a fake hour is worth about $5 (JT; TP A1). The client belongs to the attacker, so every byte it sends is a claim. Trust comes from what the attacker doesn't control: the server's clock, third-party servers such as GitHub and their push timestamps, the fact of one human with one attention stream, cross-user data, thresholds the attacker can't read, and people who ask about the work (TP A1). Four mechanisms carry the weight (EV 6.1):

1. Commitments made at the time and witnessed by clocks the teen doesn't control (3.6, 7.4).
2. Agreement between records written by different code paths, so a forger has to fake all of them consistently and live (7.3).
3. A random sample of content, drawn after the metadata is pinned, checked against the commitments (7.5).
4. A person, for a published, risk-weighted fraction of submissions (7.9).

Client-side checks still earn their place: they catch honest automation and lazy cheating for free, and the teen sees problems before a reviewer does.

### 7.2 Defenses adopted

TP A8's ten ranked defenses, plus what the grafts and must-fixes add, with the milestone each ships in (9.7):

| TP rank | Defense | Here | Ships |
|---|---|---|---|
| 1 | Credit only human-evidenced seconds | SM as adopted; one human minute per wall-clock minute across devices and projects (SM 3.4) | M1 |
| 2 | Witnessing | 60 s batches, signed receipts, the public day root (3.6, 7.4) | M0 ledger, M2 server |
| 3 | Commits as anchors | Three clocks per commit, CC3, CC4, `few_commits`, `UNCOMMITTED_CAP_S` = 3,600 s per submission (SM 4.3) | M2, M3 |
| 4 | Public tier-1 automation labels | SM 2.2 rules 1 to 5, the process tree, `procs` for `--bare` runs | M1 |
| 5 | Opt-in typing evidence | Per-15-second key counts and injected-input share, never key identities or per-key times: Windows Raw Input with `RIDEV_INPUTSINK` and the `hDevice == 0` test for keyboards only (TP fact-check row 7), macOS listen-only `CGEventTap`, X11 XInput2 with XTEST detection, nothing on Wayland. Ships only in signed builds, before the first program payout, with the VM rule (D17) (JE must-fix 10; JT must-fix 10) | M5 |
| 6 | Server risk score, on submitted windows only | SM 8.7's detectors as public code with their public starting points; deployed thresholds in a private config file outside the repo (TE D13). Detectors with features beyond SM 8.7's set run only in the optional private sentinel (JT must-fix 12) | M3 |
| 7 | Review protocol | Deflate by default, the sticky challenge, risk-weighted audits, pay after review, hours revocable through the spot-check window, penalties through Robert (7.5, 7.9) | M3 |
| 8 | Reviewer integrity | Random assignment, conflict rules and the access log in M3; seeded cases and statistics before the first decision (7.10) | M3, M5 |
| 9 | Open core for detection | Rank 6's split | M3 |
| 10 | Local dark-flow detection, shown to the teen | SM 4.2, as coaching in the recap | M1 |
| added | Side-door rule | 5.2, 6.4 | M1, M4 |
| added | CC1 to CC11 and held minutes | 7.3 | M3 |
| added | Offline cap | 7.4 | M2 |
| added | Sealed tier-2 content | 7.6 | M3 |
| added | Fraud Squad desk | `/review/fraud`: cross-submission search by device, shared root commits, overlaps, and IP and JA4 within 30 days, so users without Hackatime aren't invisible (JB must-fix 8) | M3 |

**The Hackatime cross-check.** For a teen who also sends to Hackatime, the review page shows `hackatime_logged_s` (D15) next to pairtime's numbers and links Telescreen's workbench for the same names (HC 3.5). A gap of more than 10% between pairtime's figure and Hackatime's for the same names and window raises the tier-2 flag `hackatime_mismatch`, with its threshold in the private config. Hackatime's copy is a second clock for when a claim was made. It isn't evidence that a person was there, because every heartbeat it stores came from the same machine (JT, Adoption-first).

### 7.3 Consistency checks

The public checks live in `pkg/check`. The client runs them before a submission, so an honest teen sees a problem first, and the server runs them again on the reveal (EV 6.4):

| Check | Compares | Catches |
|---|---|---|
| CC1 hook vs log | Each hook `prompt.submit` with the transcript's user entry for the same turn (order, time within 5 s, normalized `len_chars`), and the reverse | Fabricated or imported transcripts; a script calling `pairtime hook` with invented payloads, which leaves no transcript |
| CC2 prompt vs OS | The share of human-origin prompts with OS input in the 10 s before submit or the `INPUT_RELEASE_GRACE_MS` after it (SM 2.2 rule 6's input test, D-input-release-grace) and focus on the host window; the focus half is not computable for a prompt whose window in front was unknown (SM 2.5). Relayed prompts are left out and counted on a line of their own, since the person typed them on another device (SM 2.2 rule 9) | pty and `tmux send-keys` drivers |
| CC3 edits vs git | Lines changed by agent edit tools plus hand-edit deltas in a commit interval, against the commit's `--shortstat`. `unexplained_lines` when more than half of a commit's lines, and more than 200, have no recorded edit | Copied projects, reconstruction, edits by a process pairtime can't see |
| CC4 commit clocks | `observed_at` before `server_first_seen_at` before `observed_at` plus 15 minutes; on App-installed repos, push time not earlier than `observed_at` minus 60 s; forge SHA and diffstat match | Backdated history; commits made elsewhere |
| CC5 one attention stream | No slot credited twice across devices or projects | Second machines, account sharing, parallel-agent splitting |
| CC6 live vs re-scored | The server's re-score (6.5) against the live buckets | Modified clients; scorer bugs. Mismatched minutes are held (D7) |
| CC7 chain integrity | Revealed records reproduce every `R_b` and `H_b`; no gap in `b` or `seq` | Hidden, dropped or reordered records. Affected minutes are held |
| CC8 content-derived inputs | For challenged turns, the keyed sketch recomputed from revealed text and its per-shingle HMACs, and `prompt_class` recomputed, against the committed values | A client that commits false sketches to dodge discounts; a 10-turn sample catches one that falsifies 10% of prompts with probability 0.65 |
| CC9 session integrity | One session stays in one project; every `agent.*` turn has a prompt or an automated origin in the same session; the parent class doesn't change mid-session | Records moved between projects |
| CC10 editor vs focus | Editor anchors arrive while an editor window of that project has focus; not computable where focus was unknown (SM 2.5) | Heartbeats injected by a script |
| CC11 typing, module on | Key-down counts in the lead window against the prompt's typed length; injected share | Pasted or injected prompts passed off as typed |

Each check reports pass, fail or not computable, with counts. Not computable, such as CC1 on a harness without a log reader, never counts as a failure. Failures change review priority and the band, never the credited number, except CC6 and CC7, which hold minutes.

### 7.4 Clocks and witness labels

Event times are re-anchored from the monotonic clock within one boot, against the server's receipts, and never to a time after the record's own wall time, where its credit lands, so a device clock that runs slow reads late, and one more than 5 minutes fast has its records refused: events more than 5 minutes in the future are rejected (SP 8.4). The client reads each minute by the same rule, with no correction for its clock's offset, so a teen's dashboard never reads witnessed or offline a minute whose records a submission refuses, and `pairtime doctor` warns when the clock is more than a few minutes off the server's. Each record gets one witness label from its batch's `received_at` minus its corrected time:

| Label | Rule | At submission |
|---|---|---|
| `witnessed` | Received within `WITNESS_LAG_S` = 900 s | Normal |
| `offline` | Late, but sealed on a continuous chain while the ledger holds upload-failure notes, then flushed in one catch-up | Its computed level, up to `OFFLINE_CAP_S` = 10,800 s per submission whatever the flush delay, and only if a commit in its interval reached the forge within `OFFLINE_PUSH_S` of reconnecting. Banded below witnessed and never pre-approved (D6) |
| `backfilled` | Everything else late, and all lookback | Level C: program opt-in, forge-verified commit intervals, the handbook third (D5) |

The failure notes are the client's own claim, which is why the cap applies to every offline minute (JT must-fix 2). Commits carry three clocks (5.8). The fourth clock covers the server: once a day `pairtimed` commits a signed root over every `(device_id, b, H_b, received_at)` it received that UTC day, chained to the previous day's root, to the public `pairtime-witness` repository. Each bundle carries the inclusion proof of each batch head, so an auditor can check that the server didn't insert or re-date roots, using GitHub's commit history as the outside timestamp (EV 6.3; TP B6).

### 7.5 Submission: freeze, reveal, sticky challenge

A submission covers a set of projects `P` and a window `W`, or a list of devlog windows (D13).

1. **Create.** Through the program API (`POST /api/v1/submissions`, idempotent on `(program, external_id)`) or by the teen. The server rejects a second submission of the same project and window to another program, and flags an overlap with another user's submission on the same repo fingerprint (SP 10.2).
2. **Freeze.** The server records `P`, `W`, the scoring version, the parameter hash and a fresh 32-byte random value `r`, and sends the client `SHA-256(r)`. The web flow asks for a fresh HCA login (`prompt=login`; SP 3.1).
3. **Metadata reveal.** The client sends, sealed (7.6), every header and body hash of every batch overlapping the re-score range, full bodies for the reveal set, and the checkpoints (6.5). The server rebuilds every leaf, root and head against what it stored at the time (CC7), computes each record's witness lag, re-scores the window (CC6), and runs the other metadata checks.
4. **Challenge.** Only then does the server reveal `r`. The seed is `SHA-256(r || SHA-256(metadata reveal bytes))`. The pool is every human-origin prompt and `track` call in `P` and `W` that opened a credited anchor and carries a content commitment. Relayed prompts are human-origin and in the pool; relay records, which commit no text, aren't (SM 2.2 rule 9). The draw takes the `CHALLENGE_K` (6.3) pool items with the smallest `HMAC-SHA256(seed, device_id || seq)`, or all of them when the pool is smaller, plus two commits for the interview list. The server makes the draw once, under the scoring version and parameter table frozen at step 2, and a version change that later re-scores the open submission (6.6, 6.7 item 5) never redraws it. The server committed to `r` before it saw the reveal, and the client sent the reveal before it learned `r`, so neither side can steer the draw, and an auditor recomputes it from the bundle (EV 6.5; JB graft E2). A submission of hand coding with no prompts has nothing to draw; its evidence is editor events, focus and git (CC3, CC10).
5. **The draw is sticky** (JT must-fix 3). The server records every drawn item, keyed `(device_id, seq)`, for as long as its ledger record could be submitted. Any later submission whose pool contains a drawn item must reveal it, on top of its own new draw, or the item counts as withheld. Withdrawing a submission after its draw (`POST .../withdraw`) marks every drawn, unrevealed item withheld for every future submission that covers it. So a teen who sees a fabricated prompt drawn can't walk away and re-freeze a slightly different window: the set of drawn items only grows.
6. **Content reveal.** For each drawn turn, the client reveals the prompt and the final agent message of the turn before it, the message the prompt answered (EV D3). Both go through `pkg/redact` first: the teen's known values (OS account name, home directory, hostname, `git config` name and email, GitHub and HCA usernames, private repo and folder names, the teen's deny list), then gitleaks' rules as data, then validated PII patterns ported from Presidio (TP B4). In agent messages, fenced code blocks collapse to a typed marker with a line count by default, and the teen can add any back (JT, Evidence-first). In any prompt a T3 Code record pairs with, T3's attachment lines and its captured-window block, which holds the app name, title and accessibility text of a window on the person's other device, collapse the same way, as removed spans with typed markers, and the teen can add them back. The teen sees exactly what the reviewer will see and can remove more. The client sends the kept tokens with their salts, plus the leaf hashes and type markers of removed spans, sealed. The server recomputes each `C_i` against the committed one, and CC8 compares each revealed prompt and `track` label with its record's committed features (7.3).
7. **Withholding.** The teen may withhold any drawn item. The minutes its anchor supports, its lead and the reading it opened, aren't credited (TP A7 E6).
8. **Expired or lost text.** If a drawn prompt's text has passed device retention (4.4), or the teen turned "keep prompt text" off, the client answers `expired`, and the server checks the record's age from its witnessed batch. Those minutes, like minutes on a device whose key is lost, show as "witnessed, evidence lost": they leave the corroborated band and count only inside forge-verified commit intervals (TP B6 point 3). A revealed item whose sealed blob the evidence store can no longer return or open (10.3) is `unavailable`. Before the decision the server asks the client to reveal it again, and the answer counts as in steps 6 to 8. Until then, and after the decision, an unavailable item changes no credit. An item whose blob `pairtimed` deletes itself is never `unavailable`, and the submission it was deleted from never asks for it again. A purge (7.11) shows the item as purged in every submission it was purged from, from the moment the purge is recorded, and those submissions refuse a new reveal of it. A submission whose spot-check window has closed (4.4), or whose calibration reveal has passed its 30-day delete (6.7), answers 410 for its reveals (7.8) and asks for nothing. Any other submission whose pool holds the item still asks for it, as step 5 says.
9. **Then** the checks, the optional sentinel's risk score, the audit draw (7.9), the review, and webhooks to the program for each state change.

If a fraction `f` of a submission's prompts are fabricated, re-rolled or LLM-written, a draw of `k` contains at least one with probability `1 − (1 − f)^k`, which is 0.97 for `f` = 0.3 and `k` = 10 (TP B6).

### 7.6 Sealing tier-2 content past Cloudflare

Cloudflare terminates TLS in front of `pairtimed` (SP OQ 3), so minors' revealed prompts would otherwise exist in plaintext at the edge (JT must-fix 5). Both directions are sealed with `pkg/seal`:

- **Keys.** `pairtimed init` generates the evidence key pair (ECDH P-256) on the server. The private key sits at `/data/keys/evidence.p256` with mode 0600 and is backed up encrypted to Karim's offline `age` key. The public key is published in `/.well-known/pairtime-keys.json` with a key id and validity dates. The document lists only the current evidence key, so after a rotation (10.2) it lists the new key alone.
- **Upload.** The client fetches `/.well-known/pairtime-keys.json` before each upload. It generates an ephemeral P-256 key, derives a key with ECDH against the evidence key the document lists and HKDF-SHA256 (`info = "pairtime/v1/seal" || submission_id || part`), and encrypts with AES-256-GCM. The server refuses a blob sealed to any other key with `409 evidence_key_retired`, and the answer carries the current key, so the client seals the reveal to that key and sends it again. Until the server has stored a reveal, the client can rebuild it: the metadata reveal from the same ledger batches, and the content reveal from the teen's choices, which the vault keeps (3.5). So a client that was offline or restarted during a rotation (10.2) loses nothing, and the teen is asked nothing again. The evidence store keeps the blob as uploaded until a rotation re-seals it (10.2), so a database or bucket leak alone exposes no content.
- **Viewing.** The review page's script generates an ephemeral P-256 key pair with WebCrypto and sends the public key with `GET /api/v1/submissions/:id/reveals`. The server decrypts in memory, re-seals to that key, and answers with `Cache-Control: no-store`; the browser decrypts and renders. `pairtime verify --fetch-reveals` does the same in Go. Each fetch is an access-log entry.

This covers passive exposure at the CDN, in logs and in caches. It doesn't cover an active attacker at Cloudflare who substitutes keys, and the privacy page says so.

### 7.7 The review page

`/review/submissions/:id`, behind HCA sign-in with a reviewer role. It exists to make one decision fast: approve this hour, or cut it with a reason a spot-checker will accept (EV 6.7; AD 6.2). Top to bottom:

1. **Headline.** Claimed hours from the program; pairtime's credited hours by band (corroborated, witnessed, offline, backfilled) and held minutes; the handbook comparison (`logged_s` or `hackatime_logged_s`, `ai_logged_s`, `handbook_s`, `ai_ratio`); credited seconds by evidence level and `via_track` share; audit status; scoring version and the program's preset.
2. **Must-view rows**: every row with a failed check or a flag, plus one clean row drawn at random. "Approve remaining rows" unlocks only after these are opened.
3. **Devlog windows**, each with its work-session rows, its loop rows at 0, one per loop with each discount code's seconds (D12), and a per-commit tab. Rows can only be cut below `captured_s`; loop rows can only be raised up to theirs.
4. **Hour strips**: one 60-cell strip per hour, with lanes for activity kind, evidence level and witness label, and markers for prompts (typed, pasted, automated and `track` drawn differently), permission answers, interrupts, test runs passing or failing, and commits. Uncredited minutes are hatched with their reason code on hover. A minute drawer replays SM 2.7 for any minute (TE 6.3).
5. **Revealed turns**, inline at their markers: the agent message on the left, the prompt on the right with the class the scorer gave it, removed spans as typed markers, and "matches commitment, witnessed 14:03:12" under each. Withheld, expired, purged and unavailable (7.5 step 8) items show as such.
6. **Commits**: three clocks, diffstat, credited time since the parent, surviving lines (a client claim in v1), forge link.
7. **Checks**: CC1 to CC11 per row, with counts such as "prompts with keyboard input before them: 212 of 214".
8. **Flags**: tier-2 codes and categories of missing evidence, never thresholds.
9. **Story**: the teen's devlogs for the window as a collapsible index whose claims link to minute ranges (TE 6.3). It sits below the evidence so a persuasive account doesn't come first.
10. **Cross-checks**: `hackatime_logged_s` against Hackatime's figure and the Telescreen link; earlier decided submissions with approved-to-credited ratios, only from the same program for program reviewers, and from all programs for Quality and Integrity and Fraud Squad (JT must-fix 14). The teen's public build-day history is not shown. The teen may attach it as an extra at submission, which works the same for private profiles (JT, Teen-first).
11. **Handbook text** with copy buttons, and a factual `Deflation Justification` draft the reviewer must confirm or rewrite. `Specific Technical Features` stays human-written (AD 8.2).
12. **Access log**: who opened this evidence and when, which the teen also sees.

Keys: `j` and `k` move between rows, `a` approves, `d` cuts (drag across minutes or type a number), `r` raises a loop row, `f` flags, `o` opens the evidence under the cursor. Cut reasons: `not_struggle`, `off_task`, `one_shot`, `unexplained_code`, `insufficient_evidence`, `padding`, `other` with text. Program reviewers see their own program's submissions; Quality and Integrity, Fraud Squad and HQ spot-checkers see all. Targets, measured in the native program: a median under 5 minutes for a clean 10-hour submission and under 15 with flags (EV 6.7).

### 7.8 The bundle and offline verify

`GET /api/v1/submissions/:id/bundle` returns a tar archive that verifies itself and carries no revealed text (JT must-fix 4):

| File | Contents |
|---|---|
| `manifest.json` | Submission, program and `external_id`; `pairtime_id`, `hca_sub`, `slack_id`; projects, window, scoring version, the parameter table and its hash, client build ids, totals, bands, rows, check summary, `r` and the seed, and the SHA-256 of every other file. Signed with the server's Ed25519 key, whose id and validity dates are in `/.well-known/pairtime-keys.json` |
| `batches.json` | Every overlapping batch: `b`, `n`, `R_b`, `H_b`, `prev`, `received_at`, the receipt, and the inclusion proof of `H_b` in that day's published tree |
| `records.jsonl` | Every header and body hash in those batches; full bodies, which are metadata only, for the reveal set |
| `buckets.json` | The buckets as received live, with `received_at` and revisions, next to the server's re-score |
| `challenge.json` | The pool, with the scoring version and parameter hash the draw ran under and its `CHALLENGE_K` (7.5 step 4); the drawn items, their content commitments `C_i`, and each one's status: revealed, withheld, expired or purged. No tokens |
| `commits.json` | Commit records with their three clocks and digests of the forge responses |
| `checks.json`, `review.json` | CC1 to CC11 per row; cuts and raises with reason codes, opaque reviewer ids and the outcome |
| `flags.json` | Tier-2 codes and reason categories. Only in the reviewer variant; the teen's copy leaves it out |

`pairtime verify bundle.tar` runs offline. It checks the manifest signature against the pinned key, rebuilds every leaf, root and head, checks each receipt and each inclusion proof against the day root (from a local clone of `pairtime-witness` or a fetched file), recomputes the draw from `r` over `challenge.json`'s pool with the parameter table the draw ran under, checks that pool against the records when the draw ran at verify's own MAJOR version, and re-runs `pkg/score` and `pkg/check` at the bundle's version with the bundle's parameter table. It exits 0 when everything reproduces, 1 when numbers differ, 2 when a signature or proof fails, and 3 for an older MAJOR version (6.6). With `--fetch-reveals` and a reviewer sign-in it fetches the sealed reveals (7.6), checks each against its `C_i`, keeps them in memory only, and logs each fetch. It lists an item the server reports unavailable (7.5 step 8), and that item doesn't change the exit code. Its CI test runs with networking disabled (`unshare -n`) (EV WP4.7).

The bundle's SHA-256 goes into the justification text, so a spot-checker knows which evidence the reviewer read (EV 6.6). Revealed content stays in the evidence store, under the access log, until the spot-check window closes (4.4); after that `GET .../reveals` and `GET .../bundle` answer 410.

### 7.9 Audits and interviews

Each submission gets a deep-audit probability, published with everything but the risk weights (EV 6.9; TP A5 principle 3):

```text
p = min(0.6, max(AUDIT_BASE_P, 1.1 * h / (0.6 * h + 120)) + risk)        AUDIT_BASE_P = 0.08, h = credited hours
```

The middle term is TP A6's break-even with a 10% margin. Fraud stops paying when `p·q > g / (g + L)`. With TP's example inputs (an interview catches a puppeteer with `q` ≈ 0.6, a Hack Club-wide ban costs `L` ≈ $1,000, a fake hour is worth $5), the break-even is `h / (0.6 h + 120)`: 0.079 at 10 hours and 0.278 at 40. The formula gives 0.087 and 0.306. TP's fact-check re-derived this arithmetic and found it correct; the inputs remain guesses that audit outcomes will replace, and the published formula changes with them. The risk inputs are named publicly and weighted privately: level-B share, `via_track` share, held minutes, failed checks, tier-2 flags, first submission, offline and backfilled share, account age and earlier outcomes. Whether a higher audit chance after declining the typing module counts as "degrading" under New York's § 899-ff(4) goes to counsel (13.2; EV 7.3).

**The audit** is a 15-minute walk-through of the two commits the server drew, one design decision, and one small change made live (TP A6 point 4). The default protocol for minors (JT must-fix 9), which Hack Club's safeguarding rules override where they differ (13.2):

- **Who.** A reviewer on pairtime's audit roster: an adult Hack Club staff member, or a reviewer HQ has cleared under its safeguarding rules. Never someone from the teen's club or who shares a repository with them.
- **Channel.** A Hack Club Slack huddle. The teen shares their own editor's screen; their camera is never required. No outside platforms.
- **Recording.** None. The reviewer writes notes, kept with the decision: pass, partial (a deflation with a reason) or fail (TP B8).
- **What the teen is told in advance**, on the submission page and in the Slack message: what the call covers and how long it takes, that it isn't recorded, that they may bring a parent or club leader, and that they may choose the asynchronous audit instead.
- **Asynchronous audit** (EV risk 6): the reviewer asks for a small change in code the server drew, and the teen pushes it within 48 hours with a written explanation. It catches less, so the reviewer may ask for a second change. Choosing it never changes credit by itself.
- **Outcomes** go to the program. Confirmed fraud becomes the program's `rejected` outcome with a reason, which reaches Fraud Squad through Robert (TP A6 point 5). Hours stay revocable through the spot-check window; a revocation fires `submission.revoked`, and clawback is the program's call (TP OQ 9).

Reviewer cost at the acceptance target, roughly 1,000 submissions: an overall audit rate near 15% at 15 minutes each is 37.5 hours, and about 5 minutes for each of the other 850 is about 71 hours (EV 6.9). These are estimates to replace with pilot numbers.

### 7.10 Reviewer integrity and the native program

- Random assignment from a pool, never a claim queue. Conflict rules: no reviewing someone from your own club, by self-declaration, or someone who shares a GitHub repository or organization with you.
- Seeded known-answer submissions, about 2% of the queue, built from SM 6's timelines and the red-team fixtures, with each reviewer's accuracy tracked. A reviewer who keeps diverging from seeded answers and spot-checks gets a second reviewer on every decision (TP DI 12).
- Per-reviewer statistics for HQ: approval rate, hours approved per review, seconds per decision, seeded accuracy, spot-check outcomes.
- The native program is run by the people who built the tracker, which is TP's U5 by definition. Its launch conditions (TE 6.5; AD 6.3; JB graft T9; JT graft 3): reviewers from outside the build team, each nominated or approved by Quality and Integrity or the HQ sponsor, so nobody on the build team picks one alone; Quality and Integrity asked to run or observe its audits and to spot-check it at a higher rate than other programs; reviewer statistics published. A tool trying to win a bounty should be the most audited thing in the DB.

### 7.11 Due process, purges and device loss

- **No detector bans anyone.** A flag changes the review path and the evidence required. The teen sees which category of evidence was missing ("most prompts in this window had no typing before them"), never the threshold, can add evidence, and can appeal to a person (TP A6).
- **Purge.** "This revealed something it shouldn't have" on the teen's submission page deletes the item from the evidence store at once. `pairtimed` first records the purge in one durable step, which hides the item from every viewer, stops crediting the minutes it supported in every submission that revealed it and gives each program a revision. Only then does it delete the item's blobs, so a purge cut short by a crash or a power cut stays purged and never leaves the item `unavailable` (7.5 step 8). If `pairtimed` can't reach the store, it retries at its next start and every hour until the blobs are gone (EV 7.4; TP B4).
- **Report an exposure.** Every revealed item has a reviewer-side button that hides it from all viewers at once and asks the teen to purge or keep it (JT must-fix 4).
- **Leaked secrets.** When redaction finds a key in a prompt, the coach tells the teen it already went to the model provider and should be rotated (TE 7.3; TP B4 step 2).
- **Device loss** (JT must-fix 16; TP B6 point 2). Install offers a recovery code, opt-in with "Not now" most prominent: 128 random bits shown as 26 base32 characters in groups of four. The client wraps `K_dev` with AES-256-GCM under `HKDF(recovery_code, "pairtime/v1/recovery")` and stores the wrapped key with `PUT /api/v1/devices/:id/recovery`; with backup on, it also uploads its ledger and vault segments daily, encrypted under `HKDF(K_dev, "pairtime/v1/backup")`, or writes them to a folder with `pairtime backup --to <dir>`. `pairtime restore` on a new install takes the code, unwraps the key, and verifies the chain. Its CI test restores on a fresh runner image, not into the same data directory, because DPAPI keys don't survive a Windows reinstall (JT, Evidence-first). `K_dev` itself is never escrowed. Witnessed minutes the client can't open show as "witnessed, evidence lost" (7.5 step 8).

### 7.12 Attacks, and what is accepted

The attacker's ladder after the fixes (EV 6.12, updated):

| Attack | What it earns | What stops or bounds it |
|---|---|---|
| A script runs `claude -p` or `codex exec` with LLM prompts | 0 | Headless entrypoint and parent chain (SM 2.2 rule 4, 6.6) |
| An LLM drives the TUI through `tmux send-keys`, `expect` or a pty | 0 | No OS input before submit, origin `no_input` (SM 2.2 rule 8; CC2) |
| A `track` loop with a jiggler, on Windows | 0 submittable | Level C without keyboard evidence, and `track` records never raise the handbook ceiling (5.4, D19) |
| Forged plugin output sizes, or bridge heartbeats with big line counts | 0 submittable | Level C; the bucket fill is capped at `R_TURN_BASE_S` (5.2) |
| A script posting editor heartbeats to the local port | 0 submittable | The peer-process check makes them `editor_unverified`, level C (5.7) |
| An imported or edited transcript | 0 witnessed | No hook events witnessed at the time (CC1); backfilled, so level C rules (D5) |
| A script drives real agents through T3 Code with a T3 token it mints, or fakes T3 Code: it takes the name of T3's server, so the walk classes its sessions `agent_app`, and writes T3's store itself | Up to all of wall time at level C on the owner's own dashboard. In a submission, only what D5 allows: a program's level-C opt-in, forge-verified commit intervals, at most a third of `ai_logged_s`, which the farm's own agent turns raise | Level C keeps it off public totals, build days, boards and badges (D11, 3.8); relay records never raise `ai_logged_s` (D19); `relayed_share`; the drawn sample's prompts; the audit (SM Risk 12) |
| A script writes T3 Code's store beside its own `claude -p` loop, under its own name | 0 | A message witnesses only a prompt of a session whose nearest non-shell ancestor is an agent app's server, and the loop's is the script (SM 2.2 rule 9). Under the name of T3's server it is the row above |
| A row written into T3 Code's store with an old stamp, or an imported session | 0 | `RELAY_SEEN_LAG_S`, and a pairing needs a prompt delivered within `RELAY_MATCH_S` of the stamp (SM 2.2) |
| A fabricated offline session | At most 3 hours per submission, only with a forge push within 24 hours, never pre-approved | D6; offline share raises `p` |
| Withdraw after an unlucky draw, then resubmit | Nothing | The sticky draw (7.5 step 5) |
| A modified client fabricating every record live | Whatever survives review | It must agree with a transcript, OS input in the right window, editor events and real commits with matching diffstats and forge times (CC1 to CC10), a re-score (CC6), a drawn sample whose prompts react to the agent messages beside them, and the audit |
| A modified client forges its dark-flow inputs: features under a key of its own, a fresh error signature on each agent test run, shifted hunks, or the question flags that make a go-ahead an answer | Its loop turns' credit, and answer leads for go-aheads, at their level, if undetected | CC6 holds it to the features it committed; CC8 catches a drawn turn's false length, `low_effort`, `es` or class, and revealed turns with the same text or error signature that disagree; a withheld drawn turn loses its minutes; the reviewer reads each drawn prompt, with its class, beside the message it answered (7.7); the audit. T9 residue |
| A pty driver plus a jiggler on a level-B machine | Credited at level B if undetected | It can't reach the corroborated band without editor acts passing CC10; no preset pre-approves level B; level-B share raises `p`; `periodic_input` and `regular_timing` flags; the revealed sample; the audit (AD 6.4, T3 plus T7) |
| A script types into an agent's terminal while the person plays or watches something in another app, on a desktop where the client can't see which window is in front | Level C only | SM 2.5 makes every such second level C, which never reaches public totals, build days or boards and reaches a submission only where a program opts in to level C, inside forge-verified intervals, at the handbook third (D5, D11). CC2's focus half and CC10 aren't computable there (7.3) |
| A focus spoof plus a pty driver while the person uses another app: an X client that writes the root window's `_NET_ACTIVE_WINDOW`, pairtime's GNOME extension edited, loaded at the next login and then restored on disk, or a second GNOME Shell extension that patches it | Level B if undetected | The bounds of the pty-and-jiggler row. The agent compares the extension's installed files with its own copy at every start and treats focus as unknown when they differ, which catches an edit left on disk; nothing checks the code gnome-shell has loaded. Still to study (v1.5): the X server's input focus against `_NET_ACTIVE_WINDOW` on X11, and on GNOME a flag when XWayland's `_NET_ACTIVE_WINDOW` names an X11 window while the extension reports another |
| An auto-typer (AutoHotkey, `pyautogui`, a computer-use API) into the real terminal | Level B if undetected; with the typing module on, injected keys don't count as typing | As above; injected-input flags where the module is on (TP A4) |
| A hardware HID board or a VM's virtual keyboard, with replayed timing | Credited if undetected | The VM rule keeps a VM's keys from reaching level A; content, consistency over weeks, cross-user signals, the audit |

What no tracker that respects minors' privacy can stop, said plainly (JT, closing section). The full puppeteer still works: an LLM writes prompts that react to the agent, a $5 HID board or a VM's virtual keyboard types them, a real agent builds a real project, and real commits go to GitHub. What stops it is the published, hours-weighted audit rate, the agent-message context in the reveal, weeks of witnessed history, and a person asking the teen to change their own code live. At about $5 an hour of prize value, that makes faking cost more than building.

| Accepted risk | Why it can't be stopped | Bound | Coverage |
|---|---|---|---|
| U1: someone else at the keyboard | Every signal is a real human's | `DAY_CAP_S` = 12 h; one attention stream | The audit; a v2 typing-profile drift signal as a reviewer prompt only |
| U2: performative struggle | Engagement is real; its value is a judgment | Dark-flow discounts, the no-progress half rate, the uncommitted cap | The reviewer's judgment; `low_survival` |
| U4: a project rebuilt from outside and paraphrased by an LLM | Similarity fails after a rewrite (TP A4) | CC3 `unexplained_lines` | Dolos against Unified DB code URLs at review time, v1.5 (EV 6.11); the audit's live change |
| U5: collusion at the top, including pairtime's own program | Governance | Random assignment, seeded cases | Outside reviewers, Q&I audits and spot-checks, reviewer statistics, public day roots, bundles anyone can verify |
| T9 residue: a modified client that gets everything consistent | Possible in principle | The same caps | Custom builds are labeled by client build id and need more evidence, as Hackatime allows custom clients (TP A3 T9) |

## 8. Privacy and minors

Nothing here is legal advice. It is what the build does so that counsel doesn't have to tear it out later (TP B2).

### 8.1 Everyone is treated as a minor

Hack Club's programs are for ages 13 to 18, so a Hack Club-facing pairtime is "likely to be accessed by children" almost by definition. The defaults are the high-privacy ones for everyone, so pairtime doesn't need to know anyone's age to get them right (TP B2 point 1). Every processing purpose is sorted the way New York's Child Data Protection Act requires (TP B2 point 7):

| Purpose | Basis | Examples |
|---|---|---|
| The service the teen asked for | Strictly necessary | Tracking and scoring on the device, uploads for the teen's own dashboard, the coach's session recaps, the public aggregate profile (8.3) |
| Fraud protection | Strictly necessary | Checks, risk scores and audits, only on windows the teen submits (TP B2 point 6) |
| Everything else | Informed consent (8.4) | The typing module, club boards, habit insights, "currently building" presence, sharing recap feedback as labels, research use of pilot data, backups and the recovery code |

### 8.2 Age: HCA, under-13 refusal and the OS signal

- **HCA decides eligibility**, so pairtime stores no birthdate (TP B3, DI 18). Sign-in is refused when HCA's `verification_status` is `ineligible`. Submissions require `verified`. Hack Club's notice says users under 13 may not use Hack Club services (TP B3), and how HCA marks an under-13 identity is asked of HQ (13.2). Hours logged on or after the 19th birthday earn no credit under Hack Club's terms (TP B3); HCA's `verified_but_over_18` doesn't separate 18 from 19, so until HQ provides an eligibility end date, submissions from those accounts carry an "eligibility to confirm" note for the program (13.2).
- **The OS age signal** (California AB 1043, operative 2027-01-01; JT must-fix 8; TP B2 point 8). From that date the client requests the OS age-range signal at first launch, and installs from before 2027 that update after 2026-01-01 request it before 2027-07-01 (§ 1798.502(b)). On macOS 26 and later the signal is Apple's Declared Age Range, which needs the `com.apple.developer.declared-age-range` entitlement; whether a notarized command-line binary can carry it is the first thing its work package checks, and if it can't, the client records "no signal". No Windows or Linux equivalent is known (TP B2, unverified), so those record "no signal" until one ships. The bands are under 13, 13 to 15, 16 to 17, and 18 and over (§ 1798.501(a)(2)). An under-13 band refuses sign-in on the device, stops capture and deletes local data, with a message pointing at Hack Club's policy. Other bands change nothing, because the defaults are already minors' defaults. The band stays on the device and is never sent to the server, a program or anyone else (§ 1798.501(b)(4)). A teen or parent can decline to share it, and "no signal" proceeds with HCA as the check. The work package ships before 2026-12-31.

### 8.3 The profile default and the compelling-reason assessment

R5 asks for "public-by-default user profiles" (B). GDPR Art. 25(2), the UK Children's Code Std 7 and New York's consent rule pull the other way (TP B7). The two judges split on which default Karim's instance launches with (JB must-fix 10; JT must-fix 6). This design does both, in order:

- **The product default is public**: `profile_default = "public"` is the code default, as R5 says.
- **The public page is aggregate-only**: the handle (pseudonymous handles are fine; HCA keeps the legal identity), daily credited totals at level A or B (6.2 D11), with no hour-of-day detail, build days and the weekly-goal streak, harnesses and languages, projects whose repos are public with their forge-verified commits, devlogs the teen published, and badges the teen chose. It never shows a real name, school, city, age or time zone, prompts, paths, file names, private repo names, machine details, or any trace of review: no flags, trust levels, deflations or rejected hours (TP B7). It refreshes at most hourly; the owner's dashboard is the real-time one. "Currently building" presence, leaderboards (ranked on consistency over a rolling window, never hours) and club boards are opt-in.
- **Nothing is public until the teen answers the profile card.** That answer is the "individual's intervention" Art. 25(2) asks for. In `public` mode the card shows the real preview with two buttons of equal visual weight, "Keep my profile public" and "Make it private". In `ask` mode nothing is preselected and "Private profile" is the more prominent button (TP B7 point 1). Either way the choice flips with one click later, and deleting anything published takes effect at once, as California's minors' eraser law asks (TP B2).
- **Launch gate on Karim's instance.** It runs `ask` until the named controller (8.6) signs the assessment below and counsel has looked at GDPR Art. 25(2) and New York's law. Then it switches to `public`. So minors' profiles aren't pre-public before the legal review, and the system the bounty judges is public by default as R5 says. No public profile, build-day history or badge is ever used as review evidence, so there is no reward for staying public (7.7; Std 13).

**The written assessment**, a draft for the controller and counsel to sign or amend. None of the three designs wrote one (JT must-fix 6):

1. *What is public by default*: the fields listed above and nothing else. A new field on the public page reopens this assessment.
2. *The child's interest in a public default*: the profile is part of the motivation the tool exists for. Relatedness is one of the three needs behind intrinsic motivation (PA 4.2), public consistency records are among the research's recommended motivators (PA 4.1, 4.3), and the bounty itself says Hackatime "helps me track and feel motivated to work on personal projects I never submit" (B, R3). A profile a teen must remember to switch on reaches fewer of them.
3. *What a stranger can learn*: on which days a handle built and for how long; which public repositories it pushed to. Not when in the day, not where, not who. The residual risk is linking a handle to a public GitHub account, which the teen controls by choosing the handle and which repositories are public.
4. *Mitigations*: hourly refresh, no hour-of-day data, pseudonymous handles, equal-weight "Make it private" at onboarding and in settings, immediate deletion, no trace of review, live presence opt-in.
5. *Alternatives considered*: `ask` mode, which stays a one-flag switch, and private by default. Neither meets R5's words.
6. *Review*: once a year, and whenever the public page gains a field.

### 8.4 Consent screens

Every consent-based item (8.1) has its own card, separate from any other step. The card says what the item does, that declining doesn't stop anyone from using pairtime, and how to turn it off later. "Not now" is the most prominent button and nothing is preselected. A refusal isn't asked again for a calendar year. Revoking is one toggle in Settings, as easy as consenting. If the device ever signals a refusal, pairtime doesn't ask (New York § 899-ff(3); TP B2 point 7). Declining degrades nothing in the product; the one open question, the audit weight that follows from declining the typing module, goes to counsel (7.9). A Playwright test asserts that the primary-styled button on every consent card is "Not now" (TE WP4.13; JT graft 2).

### 8.5 Profiling

Fraud scoring is profiling, so it runs only on windows a teen submits, never on logged-only hours (TP B2 point 6). The coach's analysis runs only on the device and never uploads. Session recaps describe what happened in a session the teen asked to have tracked. Multi-week habit insights and prompt-kind labels such as `asked_why` are opt-in, because the UK code sets profiling off by default without a written compelling reason (JT, Teen-first; 3.8).

### 8.6 Operator and controller

Who controls a hosted pairtime decides which laws apply (TP OQ 1). Before any minor's data is stored on Karim's server, apart from the operators' own accounts (10.2), the controller is named in a privacy notice at `https://<host>/privacy`, with a contact, the purposes in 8.1, the retention schedule in 4.4, how to exercise rights, and the route to Hack Club's Data Protection Officer if Hack Club is the controller (JT must-fix 7). The name and contact come from `[controller]` in `pairtime.toml`, and `pairtimed` won't start with `signups_open = true` until both are set (10.2). Until then the server accepts no sign-ups except from the HCA subs in `operators`, the people running the instance, so they can smoke-test it; everyone else's client runs in local-only mode, which is how the alpha starts (9.7). The choice between Hack Club HQ as controller with Karim operating the server, and Karim as controller, is decision 1 in 13.1 and 13.2.

### 8.7 Redaction and its failure path

Redaction removes spans and never rewrites text, so the commitment still proves the rest (TP B4): known values first, then gitleaks' 222 rules as data, then validated PII patterns ported from Presidio (cards with Luhn, IBAN with mod-97, US SSN, UK NINO, Aadhaar with Verhoeff, and email addresses, whose last label must look like a top-level domain, letters or `xn--` punycode, and not be a common file extension such as `png` or `json`), then the teen's preview. In a reveal and in a `track` label every email address is removed, whatever its domain. Only the fixture scrub (5.1) leaves some as the harness wrote them: the public addresses tools write into commits and remotes, such as `noreply@anthropic.com` in Claude Code's trailer, and addresses at domains reserved for examples. A fixture has to show what the harness wrote, and these addresses name nobody; a known value or a key inside one is still replaced (testdata/fixtures/README.md). TP B4's phone, IP, MAC and date-of-birth patterns are left for later. GLiNER's PII model is a v2 option at reveal time only (TP B4). The agent may suggest spans as character offsets for the teen to confirm and never writes evidence (TP B5). A CI corpus of planted secrets built from gitleaks' rule examples must come out fully redacted, and Presidio's recognizer cases, emails included, must give Presidio's spans, except that a weak SSN shape needs a context word (EV WP4.3).

Redaction will sometimes fail; recall on names is the weak part of any pipeline (GLiNER edge: 72%; TP B4). So the damage is limited: a small `k`, content sealed past Cloudflare, reviewer-only access with every view logged, the purge button, the reviewer's exposure report, code blocks collapsed in agent messages, no revealed text in bundles, and deletion when the spot-check window closes (7.5 to 7.11).

### 8.8 What the teen can see and do

- `pairtime privacy show` prints the next tier-1 upload exactly as it will be sent, and the next project names request that carries a name (4.3) before it goes, and the Privacy page lists every past upload in plain words (TP B1, B8). Any new uploaded field goes through a public change process (TP DI 15).
- `pairtime ledger show --day <date>` prints every recorded record in plain JSON.
- `pairtime submit --dry-run` shows the exact reveal before anything leaves.
- A `.pairtime-ignore` file or an exclude pattern keeps a folder out entirely; the tray has "Pause for 1 hour".
- "Delete everything" wipes the data directory and `K_dev`. Local-only mode keeps every coach feature and uploads nothing (4.2).

### 8.9 Deletion and export

`pairtimed` implements Hack Club's privacy-hub contract (TP B3, DI 27): `POST /api/privacy/delete` and `POST /api/privacy/export` with `Authorization: Bearer <JWT>`, `alg` ES256 with a `kid`, `typ` `JWT`, `iss` `hackclub-privacy`, pairtime's own `aud` registered with the hub, the claims `iat exp jti request_id action dry_run subject`, a lifetime of at most 300 s with 30 s of leeway, a single-use `jti`, keys from `https://yeet.hackclub.com/.well-known/jwks.json` cached for 300 s, `action` matching the endpoint, and `subject` looked up by `emails`, `slack_id` or `identity_id`. A `dry_run` runs the same code and rolls back, and lists the evidence blobs and backup segments it would delete without deleting any, because a blob delete can't be rolled back. The eraser deletes every server row in 4.4, the account's blobs in the evidence store and any backup segments in object storage, and reports what it retained and why: the minimal fraud record, and the leaf hashes of the account's batches, which stay until the last witness row of their day goes, because removing one would break the inclusion proofs of the day's other batches. Like the purge (7.11), it records the erasure and marks the account's blobs for deletion in one durable step before it deletes any. If it can't reach the evidence store, it still answers, and the report also lists the blobs not yet deleted as retained, with the reason. Nobody can open them through `pairtimed`, which retries at its next start and every hour until they are gone.

### 8.10 India

DPDP s. 9(3) bars "tracking or behavioural monitoring of children" from about 2027-05-13, and parental consent doesn't unlock it (TP B2). pairtime's monitoring runs on the teen's own device and the server receives only aggregates the teen's client computed, which may or may not be enough. If counsel says it isn't, Indian under-18s get local-only mode, which keeps the coach and gives up the profile and submissions (13.2).

## 9. Adoption path to 1,000 weighted projects

### 9.1 The target and the funnel

The bounty counts projects in `approved_projects`, the Unified YSWS DB's main table, whose hours came from pairtime. One weighted project is 10 hours, so 1,000 weighted projects is about 10,000 approved hours (B, organizer replies; HC 3.2). grit's repository was created about 45 minutes after the announcement (JB), so there is no margin for one program slipping. The plan targets 1,200 weighted projects, 12,000 approved hours (JB must-fix 6).

At SM 7.3's target of 0.9 approved over credited, 12,000 approved hours needs about 13,300 credited hours submitted. Each route's install-to-submit rate is an assumption, checked weekly from the server's own data (9.8). Route B uses TE 8.1's 15%, the rate JB asks the plan to re-base on (JB graft T10). Native-program participants install pairtime to take part, so their rate is assumed at 60%:

| Route | Weighted projects | Approved hours | Projects at about 11 h | Submitting teens at 1.3 projects | Install-to-submit | Installs |
|---|---|---|---|---|---|---|
| A. Native winter program (Hard Mode) | 350 | 3,500 | 320 | 245 | 60% | 410 |
| B. Existing programs fighting the one-third rule | 500 | 5,000 | 455 | 350 | 15% | 2,330 |
| C. Low-ceremony Airtable-form programs | 200 | 2,000 | 180 | 140 | 25% | 560 |
| D. Native spring ship week | 150 | 1,500 | 135 | 105 | 60% | 175 |
| Total | 1,200 | 12,000 | 1,090 | 840 | | about 3,500 |

That is about 23% of Hackatime's 15,488 monthly active users (B, R11), which makes R3 retention and the VSIX channel part of the adoption plan, not polish (JB graft T10). At Hackatime's average of 7.09 hours per active user per month, a 10-hour project takes a typical teen about six weeks, so approved projects lag installs by about two months (TE 8.5; PA 5.1). Installs have to come early: 1,500 by 2027-01-31.

### 9.2 Attribution and counting

pairtime's credited figure is the program's hours number on every counted row (JB must-fix 1). The rule the program kit states and the first Quality and Integrity conversation asks HQ to confirm in writing:

- `Optional - Override Hours Spent` is pairtime's credited figure for that project and window after the reviewer's cuts. pairtime is never offered as "evidence for AI time above a third" stacked on Hackatime hours; AD 8.3's route-B pitch is replaced by this rule.
- For a project tracked by both, the program uses pairtime's figure and doesn't apply the one-third rule again on top (HC OQ 3).
- `Justification - Alternate Tracking Method` starts with the fixed line "Tracked with pairtime <version> (scoring <version>)", and the composed `Override Hours Spent Justification` contains a `https://<host>/review/submissions/` URL and the bundle's SHA-256 (EV 8.2; JB graft E6).
- **Counting rule**, asked of HQ in week one: a row in `approved_projects` counts toward the bounty when its `Override Hours Spent Justification` contains "Tracked with pairtime", a pairtime review URL and a bundle hash. The data warehouse already copies the base into Postgres, so that is one query on HQ's side (AD 8.4; HC 3.1).
- Windows a program already paid through Hackatime aren't submitted through pairtime, and the server rejects overlapping submissions (EV 8.3).

pairtime keeps its own outcome ledger from `submission.decided` webhooks and outcome calls, shown on a public progress page by program. HQ's count is the authority, reconciled weekly (HC OQ 13).

### 9.3 The handbook text

`GET /api/v1/submissions/:id/justification?format=handbook` returns the fields Quality and Integrity's handbook defines (HC 3.7), in the shape of this golden example:

```text
Tracked with pairtime 1.0.0 (scoring 1.0.0), an open-source (AGPL-3.0) AI time tracker. pairtime credits
only intervals with evidence of the submitter's own work (typed prompts, reading the agent's output,
reviewing diffs, running tests, debugging, editing by hand) and excludes agent-only runtime, automated
turns, repeated re-prompting and idle time. For this window Hackatime logged 9.1 h, 7.4 h of it as AI
coding, so the handbook's one-third rule allows at most 4.2 h from Hackatime data alone. pairtime
credited 5.9 h: 4.8 h corroborated, 0.8 h witnessed, 0.2 h offline, 0.1 h backfilled. Of the 7.4 h of
agent-assisted time it credited 4.6 h (62%), backed by 212 typed prompts, 38 permission decisions and 9
interrupts. 71% of credited time had keyboard-level evidence (level A). Review policy: none. Every
credited interval links to its evidence at the review URL, and the signed evidence bundle
(SHA-256 3f7a...) can be checked offline with `pairtime verify`.
```

A submission with relayed credit (SM 2.2 rule 9) names it after the interrupts, as "..., and 12 messages relayed from another device, counted apart from the typed prompts", and its hours sit in the backfilled band, since they are level C.

| Handbook field | Filled with |
|---|---|
| `Optional - Override Hours Spent` | The reviewer-approved credited hours |
| `Justification - Alternate Tracking Method` | The text above |
| `Justification - Additional Justification` | The review URL |
| `Justification - Hackatime Project Name(s) + Date Range(s)` | `remind-bot 11/1/2026-11/18/2026` in the handbook's format with UTC dates, when the teen linked Hackatime |
| `Justification - Submitter Hackatime ID` | The numeric Hackatime id, when linked |
| `Deflation Justification` | A factual draft from the reason codes ("deflated from Hackatime's 9.1 h to pairtime's 5.9 h: agent-only runtime 2.1 h, re-roll loops 0.6 h, idle reading 0.5 h excluded") that the reviewer confirms or rewrites |
| `Specific Technical Features` | Left to the reviewer |

### 9.4 Routes and programs

| Route | Programs | Integration | Why they'd say yes |
|---|---|---|---|
| A. Native winter program, Hard Mode ("ship something that fought back") | One program Karim's team runs, which Zach allowed ("Yes!", B) | Tier 2 on the standard stack: an Airtable submission table with the handbook fields and the "Automation - Submit to Unified YSWS" button, pairtime's review desk, Robert for fraud verdicts | pairtime controls the rules, and the pilot's decisions measure R9 |
| B. Existing programs fighting the one-third rule | Ari programs (3am-ysws, hackxpansion, disposition, macondo), Robert clients (the-carnival, pixl, trmnt, Alchemize), other programs linking Telescreen (savepoint, halflife, crossroads and others), Flavortown (HC 3.3, 3.5, 3.6) | Tier 0 first, tier 1 for Rails apps, the Ari `journals[]` bridge | Hours arrive pre-deflated with evidence in the handbook's format, which means fewer spot-check fines and less deflation work (HC 3.7 point 5) |
| C. Low-ceremony Airtable-form programs | Programs whose only record is "repo URL plus self-reported hours", which "can rank high by weighted projects" (HC 3.2) | Tier 0 plus the Airtable script | Any live tracker is an upgrade over self-report |
| D. Native spring ship week | A short second native program in March | Same as A | Uses what the winter program taught |
| Teen pull | Any program a pairtime user joins | The link page answers the reviewer's questions | Counted within the routes above |

**The native program** is a week-one ask with a fallback (JB must-fix 6). The written proposal goes to Quality and Integrity and an HQ sponsor in week one, covering:

- **Rules.** Ship a project built with AI, with hours tracked by pairtime: one devlog per at most 10 credited hours, Stardance's cap (HC 3.3). Participants link Hackatime too until Robert accepts `kind: "pairtime"`, so Fraud Squad's existing tools see them (JB must-fix 8). pairtime-only users are also visible on pairtime's own Fraud Squad desk (7.2).
- **Budget.** HQ's per-weighted-project grants. The commonly quoted $85 per weighted project comes from a Hack Club staffer's personal blog, not an official document (TP fact-check row 29; HC 3.2), so the figure is confirmed with HQ. At $85, route A's 350 weighted projects would move about $29,750.
- **Governance** as launch conditions (7.10): outside reviewers whom Quality and Integrity or the HQ sponsor nominates or approves, Quality and Integrity running or observing audits, a higher spot-check rate.
- **Dates.** Open for building 2026-12-14, before winter break; submissions from 12-21; decisions from 2027-01-04, after the scoring freeze; shipping closes 01-31.
- **Fallback.** If the program isn't funded by 12-07: an opt-in pairtime track inside an existing route-B program under the same rules, and a smaller first season of 150 weighted projects when funding comes. Route B's target rises to absorb the gap.

Route B needs one champion per program. The pitch to that person is fewer spot-check fines and less deflation work, in the handbook's own format, and it uses the attribution rule in 9.2. Route C is many small yeses that add up.

**The program kit** in `integrations/` (AD 8.3): a one-page "switch on pairtime in a day" guide, the Airtable script, a Rails snippet for a second `HackatimeService`, the Ari `journals[]` bridge, the Robert note ("change nothing"), the handbook text, the attribution rule, and a 10-minute reviewer walkthrough of the review page.

### 9.5 The first conversation with Quality and Integrity

Zach said "We will work with Quality and Integrity to provide good guidance around how to use new time tracking tools" (B). That conversation is a week-one milestone (AD 8.6; JB graft). The material: a one-page brief, the evidence-level definition (6.4), a clickable review page on synthetic data, and the red-team fixture results. The questions, in order:

1. **Attribution.** The rule and the counting query in 9.2.
2. **Two trackers, one number.** pairtime's figure replaces Hackatime's for that window, with no second one-third cut.
3. **Significant evidence.** Is level A "significant evidence proving that more time should be credited"? Is level B, given the bands, the must-view rows and the audit weight? Is level C acceptable at the third, including witnessed minutes from desktops that hide which window is in front (SM 2.5)? (SM OQ 1, 8; JB must-fix 7)
4. **The spot-check window**, which sets evidence retention (HC OQ 4).
5. **The native program**: running or observing audits, a higher spot-check rate, and, with the HQ sponsor, nominating or approving each outside reviewer.
6. **Loop rows**: raising a D12 row within its captured seconds is a cut-only action under Ari's rule; confirm it.
7. **Interviews with minors**: the protocol in 7.9.

### 9.6 Who does what in week one

All of these go out between 2026-11-20 and 11-27, because every HQ-side step takes longer than code:

- Karim sends Quality and Integrity the brief and the questions in 9.5, and the native-program proposal to an HQ sponsor.
- Karim applies for Azure Artifact Signing under Hack Club's organization, whose identity validation takes 1 to 30 business days, and for an Apple Developer membership (CP 4.3, 4.4; JB must-fix 7; JE must-fix 10).
- Karim registers pairtime's HCA OAuth client and asks HQ to register pairtime's audience with the privacy hub (SP 3.1; TP DI 27).
- Karim and HQ name the controller (8.6).
- Karim sends the Telescreen team the `kind: "pairtime"` proposal, and Ari's maintainers a note about a `pairtime` source (EV 8.2).
- Recruit 20 to 30 alpha testers, at least 10 on pre-2017 Windows laptops, and the 40 to 60 calibration volunteers (6.7).

### 9.7 Timeline and build plan

Today is Thursday 2026-11-19. The plan assumes five agents working in parallel from day two, all queueing on the shared CI (3.13).

| Dates | Engineering | Adoption | Exit check |
|---|---|---|---|
| 11-20 to 11-27 | M0 foundations | Week-one asks (9.6) | SM 6 fixtures and ledger vectors green |
| 11-24 to 12-05 | M1 local tracker; local-only alpha by 12-01 | Alpha testers in daily use; shadow week starts 12-01 | Alpha testers' Hackatime keeps working (doctor reports) |
| 11-30 to 12-11 | M2 server core; sign-ups open once the controller is named, target 12-08 | Counting rule agreed or escalated; two route-B programs approached with the kit | 7-day retention of alpha devices over 70% |
| 12-07 to 12-18 | M3 program path | Labeled sessions from 12-07; native program announced | A test submission round-trips into a sandbox Airtable base with every handbook field filled |
| 12-14 to 12-23 | M4 breadth and release | Native program opens for building 12-14 | 300 installs |
| 12-21 to 01-17 | M5 | Labeling ends 12-27; fit and freeze 12-28 to 01-03; scoring 1.0.0 and the R2 report by 01-04; decisions open | R2 report published; 1.0 released |
| January | Fixes from reviewer feedback | Native program reviews; first Unified DB rows; route-B pilots in two programs; route-C outreach to five programs | 1,500 installs and 100 decided submissions by 01-31 |
| February | v1.5 items as needed; first MINOR refit after 300 decided submissions | Route B to four or five programs; Robert `kind` and Ari source if accepted | 150 weighted projects by 02-15; 2,500 installs by 02-28 |
| March | Scale and harden | Spring ship week; route C at scale | 500 weighted projects by 03-15 |
| April | | Scale whatever route works | 1,000 weighted projects by 04-15; 1,200 by 04-30 |

If the program path slips past mid-December, the coach misses the first alpha, or the calibration report misses the first January decisions, plan as if grit reaches a program first (JB, The race with grit).

**Rules for every work package** (TE 10.1; JE must-fixes 1, 3, 4):

- One agent, one session of 2 to 4 hours, one PR. A package that grows past that is split before it starts.
- Every acceptance check uses an oracle the agent didn't write: SM 6's fixtures with exact totals, written before any scorer code; the Python ledger reference; the real wakatime-cli binary; captured harness fixtures (5.1); pinned upstream sources (Hackatime `43bde10b02db`, Stardance `b1d3e0eac1ae`); RFC test vectors. "Unit tests" alone is not an acceptance criterion.
- Fixtures, not live services. No CI job needs a model API key or a teen's data.
- Builds and tests run only in GitHub Actions. Locally an agent only runs gofmt (PR1 AGENTS.md).
- Done means `ci.yml` green, `platform.yml` green when its paths changed, the package's own check green, and PR1's conventions kept (SPDX header, table tests, local-field tags).
- Footprint gates run from the agent skeleton on, with every heavy dependency already imported: client binary under 15 MB (`ci.yml`); idle RSS under 25 MB and idle CPU under 0.1% over 10 minutes on `windows-latest`; and on `windows-latest`, the hook's own cost over 200 warm calls: `footprint.yml` times an empty Go program, built and started the same way, in the same job, and the hook's p95 less the empty program's p95 must be at most 10 ms. The job fails outright when the hook's p95 is above R8's 50 ms, and it reports both numbers. The runner gate is a regression guard on pairtime's own work: what starting any process costs differs from one runner machine to the next by more than 7 ms, so a fixed time around the process would pass or fail with the machine a job lands on (docs/errata/2026-11-20-hook-p95-gate.md). The R8 claim itself, a warm hook round trip under 50 ms on a 2016 dual-core with a hard drive, is a 1.0 release gate checked from `doctor --report` on at least 5 pre-2017 Windows laptops.

**M0. Foundations** (11-20 to 11-27)

| WP | Deliverable | Acceptance in CI | Depends on |
|---|---|---|---|
| 0.1 | Done in PR1 (WP-0a): module, stubs, `pkg/event`, schema, `ci.yml` | Green | none |
| 0.2 | `pkg/event` changes E-1 to E-10 (4.1), schema in step | PR1's drift test; `TestLocalFields` updated and called out in the PR | 0.1 |
| 0.3 | SM 6.1 to 6.6 as event fixtures in `testdata/timelines/`, with per-minute expected credit and reason codes from SM's tables, plus the hand-computed two-tab variant of 6.3 | Fixtures validate; totals equal SM's: 10,095 s, 265 s, 1,800 s for 14:00 to 14:30, 810 s, 1,490 s, 0 s. A disagreement becomes an erratum for Karim (6.6) | 0.2 |
| 0.4 | `pkg/params`: SM 8 and 6.3's parameters, canonical JSON, SHA-256, versioned tables | Golden hash; changing any parameter changes it | 0.1 |
| 0.5 | `pkg/ledger` core: headers, nonces, bodies, RFC 9162 trees, token commitments, batches, chain, receipts, inclusion proofs; `tools/ledger_ref.py` | The Python reference agrees on 1,000 random batches; any one-byte mutation fails verification | 0.2 |
| 0.6 | `release.yml` snapshot (archives, `checksums.txt`, Ed25519-signed manifest) and both install scripts | `e2e-install.yml` runs both scripts on three runners against a local release fixture; a tampered binary is refused | 0.1 |
| 0.7 | `platform.yml`, `nightly.yml` and `web.yml` skeletons with path filters and concurrency; AGENTS.md CI section updated | Each runs on `workflow_dispatch` | 0.1 |
| 0.8 | `internal/webui` with a placeholder `dist/`; a Svelte skeleton in `web/` | `go build ./...` passes in a job with no Node installed | 0.1 |

**M1. Local tracker** (11-24 to 12-05)

| WP | Deliverable | Acceptance in CI | Depends on |
|---|---|---|---|
| 1.1 | Agent skeleton: lock, scheduler, `state.json`, local HTTP with token, `Host` and `Origin` checks; `footprint.yml` with heavy imports | A foreign `Host` or `Origin` gets 403; footprint gates pass | 0.8 |
| 1.2 | Socket IPC with the named-pipe fallback; inbox write and drain | 1,000 messages, `kill -9`, restart: no loss, no duplicate | 1.1 |
| 1.3 | `pairtime hook`: stdin cap, parent-chain capture, 100 ms timeout, inbox fallback, detached start, always exit 0 | on `windows-latest`, p95 at most 10 ms over an empty Go program's timed in the same job, and at most 50 ms; agent-down calls append and exit 0; malformed stdin exits 0 silently | 1.2 |
| 1.4 | Ledger writer: segments, tail recovery, sealing on `BATCH_S` and on lock, suspend, prompt and stop | A crash loop kills the writer 200 times on Linux and Windows; the chain verifies after each restart | 0.5, 1.1 |
| 1.5 | Vault and keystore: AES-256-GCM records, DPAPI, Keychain, Secret Service, 0600 fallback with a warning; retention sweeps | Records round-trip; a sweep with a fake clock deletes exactly what 4.4 says | 1.4 |
| 1.6 | `pkg/score` 1: presence, origin, anchors, leads, provenance levels (SM 2.1 to 2.3; 6.4) | Fixtures 6.2 and 6.6 reproduce minute by minute; side-door fixtures with `input_any` true and `input_keys` unknown earn 0 submittable seconds | 0.3, 0.4 |
| 1.7 | `pkg/score` 2: reading bucket, lookups, combine rule, attribution (SM 2.4 to 2.7, 3) | Fixtures 6.1, 6.3 and its two-tab variant, and 6.4 reproduce | 1.6 |
| 1.8 | `pkg/score` 3: dark flow with keyed features, progress, half rate (SM 4) | Fixture 6.5 reproduces, including the A, B, A, B, A alternation caught at the third A | 1.7 |
| 1.9 | `pkg/score` 4: buckets, `pending` and `rev`, reason codes, rollups, work sessions, checkpoints, state serialization; `score-regression` | 100 shuffled arrival orders inside the pending window give byte-identical buckets (AD WP 1.7); a state round-trip mid-fixture gives identical output; outputs can't change without a `VERSION` bump | 1.8 |
| 1.10 | Windows presence and power, project-context gating | Unit tests with fakes; a 60 s smoke on `windows-latest` emits samples | 1.1 |
| 1.11 | Windows focus and the process-tree walk for `hosted_sessions` | Injected fakes and a golden classification table on `windows-latest` | 1.10 |
| 1.12 | macOS presence, power and focus through purego; a check of whether key-down idle needs a permission | Builds with `CGO_ENABLED=0`; `macos-latest` smoke; the permission finding saved as an artifact | 1.1 |
| 1.13 | Linux presence, power and focus: X11, Mutter, `ext-idle-notify-v1`, logind, pairtime's GNOME Shell extension, and `os.focus` `unknown` where nothing reports focus | Under Xvfb, `xdotool` input resets idle and a focus switch between two `xterm`s is seen; a headless `gnome-shell` reports a switch through the extension | 1.1 |
| 1.14 | `procs` and the parent-chain classifier | Spawned chains (`python` to `bash` to a child) classify correctly on all three OSes | 1.1 |
| 1.15 | Claude Code fixture capture: hooks, transcripts, OTLP/JSON, with provenance | `fixtures.yml` accepts the directory | none |
| 1.16 | Claude Code adapter and the OTLP/JSON receiver | Every captured hook maps to the expected events; a transcript replays to the same events as its hooks; `/loop` prompts match `session_crons` | 1.15, 1.2 |
| 1.17 | Local WakaTime endpoint, the bridge mapping (5.7), the peer-process check on Windows and Linux, the project-name map | Captured heartbeats map to expected events; a heartbeat posted by a test script is `editor_unverified` | 1.2 |
| 1.18 | Installer core: the `~/.wakatime.cfg` editor, the Claude Code settings merger, autostart writers, exact uninstall, `--dry-run` | Golden before-and-after fixtures, including an existing `.*` line and git-ai entries; uninstall restores files byte for byte | 0.1 |
| 1.19 | `e2e-wakatime.yml` (5.6) | Passes on three OS runners, including the agent-down and offline-replay cases; marked as a release blocker | 1.17, 1.18 |
| 1.20 | Git adapter | Driving the `git` CLI in CI: commit, amend, rebase and merge produce the right records and `rewrites`; the fingerprint survives a fresh clone | 1.4 |
| 1.21 | `pkg/redact` | The planted corpus from gitleaks' rule examples is fully redacted, and Presidio's recognizer cases, emails included, give Presidio's spans; code blocks collapse to markers | 0.1 |
| 1.22 | Local UI v0 and the coach alpha: Today, the chess clock, projects, uncredited time by reason, session recaps with moments and loops, the welcome recap from low-priority lookback with progress, one notification per session | Playwright against a fixture store: displayed totals equal the scorer's; no page loads without the token; the recap for fixture 6.5 names the 20:05 to 20:38 loop and the edit that ended it; a test asserts the lookback priority | 1.9, 1.16 |
| 1.23 | `doctor`, `doctor --report`, `privacy show`, `ledger show` | `doctor --json` validates; planted usernames and paths never reach the report | 1.4 |
| 1.24 | `pairtime install`: the consent screen from adapters' plans, local-only mode, lookback | Snapshot tests of the consent screen; install and uninstall in a temporary home on all three OSes | 1.18, 1.22 |

**M2. Server core** (11-30 to 12-11)

| WP | Deliverable | Acceptance in CI | Depends on |
|---|---|---|---|
| 2.1 | `pairtimed` skeleton: config, migrations, single writer, `/healthz`, `/readyz`, `/metrics`, SQLite version check | Migrations apply on SQLite, and nightly on Postgres 18; startup refuses SQLite older than 3.51.3 (SP 7.4) | 0.1 |
| 2.2 | HCA OIDC confidential client | End to end against a fake provider; `ineligible` is refused; submitting needs `verified` | 2.1 |
| 2.3 | Device grant, `ptd_` tokens, device keys | The client completes the flow; corrupted tokens fail their checksum offline | 2.2 |
| 2.4 | Ingest: signature envelope, request counter, chain link, dedupe, partial acceptance, firewall, limits, receipts | The free-text fuzz test always gets 422; replays are idempotent; a re-signed or stale request is refused | 2.3, 0.5 |
| 2.5 | Time and witness labels | Skewed and jumped clocks; a record first seen after 15 minutes is `backfilled` | 2.4 |
| 2.6 | Rollups, SSE, cross-device merge | A delta reaches a stream within 2 s; reconnect replays the gap; 1,000 streams add under 50 MB; two devices crediting one minute keep one | 2.5 |
| 2.7 | Client uploader with failed-upload notes and catch-up | 24 hours of synthetic records give equal totals on both sides through injected 429s and a restart; a stopped server yields `offline` labels per D6 | 2.4, 1.9 |
| 2.8 | Profiles, the profile card in both modes, consent cards | Playwright: "Not now" is the primary button on every consent card; public HTML and JSON hold no private project name and no hour-of-day data; "Make it private" hides the profile from an anonymous browser | 2.6 |
| 2.9 | Commits and forge verification | Recorded fixtures for GitHub, GitLab and Forgejo; a diffstat mismatch is detected; push payload paths are dropped for private repos | 2.4 |
| 2.10 | Privacy-hub endpoints and the eraser | Tokens from a generated P-256 key verify; a replayed `jti` is refused; a dry run changes nothing; the eraser reports the retained record | 2.2 |
| 2.11 | Deploy bundle and `restore-drill.yml` (10.1) | The drill builds the image, replicates to a MinIO service container, restores into a fresh container, passes `PRAGMA integrity_check` and the smoke tests | 2.1 |
| 2.12 | Build days, devlogs, badges | Build-day rules with a fake clock, including the 72-hour lag, freezes and the time-zone rule | 2.6 |
| 2.13 | The privacy notice page and server retention jobs | A fake clock deletes exactly what 4.4 says | 2.1 |

**M3. The program path** (12-07 to 12-18)

| WP | Deliverable | Acceptance in CI | Depends on |
|---|---|---|---|
| 3.1 | Submissions API: create, freeze with `SHA-256(r)`, overlap rejection, withdraw, devlog windows, states, Robert-style webhooks, `updated_since` | Every state transition; a verifier written from Robert's scheme accepts the signatures | 2.6 |
| 3.2 | `pkg/seal`, the evidence key, `/.well-known/pairtime-keys.json`, client sealing, WebCrypto viewer sealing | Round trips in Go; Playwright decrypts a sealed reveal in the browser; plaintext never appears in server logs | 3.1 |
| 3.3 | Metadata reveal and re-score: reveal set (6.5), CC7, witness lag, checkpoints, CC6 with held minutes | Submitting one project of the 6.3 fixtures reproduces its credit with no bodies of other projects outside the reveal set; the two-tab variant reproduces; a moved minute fails CC6 | 3.2, 1.9 |
| 3.4 | Challenge, sticky draw, content reveal, withholding, expiry | Draw test vectors; the withdraw-and-redraw fixture gains nothing; a withheld item removes exactly its minutes | 3.3, 1.21 |
| 3.5 | `pkg/check` CC1 to CC11 | For each check, one red-team fixture fails and one clean fixture passes | 3.3 |
| 3.6 | Tier-2 detectors with private thresholds from config, the audit formula, the sentinel socket stub | Flags never run on unsubmitted windows; with zero risk, `p` is 0.08 at 5 h, 0.087 at 10 h and 0.306 at 40 h | 3.5 |
| 3.7 | Review page 1: headline, bands, must-view gating, rows with cuts and raises, hour strips, minute drawer | Playwright: "approve remaining" stays locked until must-view rows are opened; a cut above captured is refused; a loop row can't exceed its captured seconds | 3.5 |
| 3.8 | Review page 2: revealed turns, commits, checks, flags, story, cross-checks, access log; the Fraud Squad desk | Playwright: the teen sees the access-log entry; program reviewers see only their program's history | 3.7, 3.2 |
| 3.9 | Handbook endpoint, deflation draft, Airtable script | Golden text for a fixture submission; vitest with a mocked Airtable API fills every mapped field | 3.1 |
| 3.10 | Hackatime reads 1: `authenticated/me`, `authenticated/api_keys`, `get_user_by_email`, `:uid` resolution | Contract tests from captured Stardance requests and Hackatime's shapes at the pinned commits | 3.1 |
| 3.11 | Hackatime reads 2: `stats` with both filter forms, `my/stats`, `trust_factor`, `info_batch` | As 3.10; `trust_factor` is the unscored value for every user | 3.10 |
| 3.12 | Hackatime reads 3: `heartbeats/spans`, `my/heartbeats`, `heartbeats.bulk` refused | A Ruby script in CI, written from scratch to make Stardance's calls, gets the expected credited seconds | 3.11 |
| 3.13 | Program OAuth clients, scopes, grants, project-name linking | Reads for an unconnected user return 404 | 3.1 |
| 3.14 | Bundle, `pairtime verify`, daily-root publisher | Verify passes with networking disabled; a one-byte change exits 2 for signatures and proofs and 1 for numbers; the day root is deterministic and committed to a test repo | 3.4 |
| 3.15 | Labeling mode | Labels save and export in SM 7.1's categories; κ between two seeded labelers is computed correctly | 3.7 |
| 3.16 | Program kit in `integrations/` | The Ari bridge's JSON validates against a schema written from Ari's documented ingest keys (HC 3.6) | 3.9 |
| 3.17 | Reviewer roles, random assignment, conflicts, the outcome gate | Decisions return `409 scoring_not_frozen` before 1.0.0; a reviewer is never assigned their own club | 3.7 |
| 3.18 | Purge, exposure reports, revocation webhook | A purged item is gone from storage and its minutes are uncredited; an exposure report hides the item from all viewers | 3.8 |

**M4. Breadth and release** (12-14 to 12-23)

| WP | Deliverable | Acceptance in CI | Depends on |
|---|---|---|---|
| 4.1 to 4.5 | Fixture capture and adapters for Codex (hooks, rollout reader, trust detection), Cursor (dedupe against Claude-format hooks), Copilot, Gemini CLI, and Devin with Cline | Captured fixtures map to expected events; one prompt seen through two hook configs counts once; Codex automation fields map to SM 2.2's origin rules | 1.2 |
| 4.6 | `pairtime track`, the AGENTS.md writer, the compliance counter | The jiggler-plus-`track` loop earns 0 submittable seconds and adds nothing to `ai_logged_s`; a call matched by a hook or log prompt opens nothing and counts as `track_matched`; an unmatched call on a session seen through hooks or logs raises `track_without_prompt`; golden rules files for every file 5.4 lists; the entry is planned only for harnesses no hook or log reader sees, or that the teen named with `--track`; uninstall removes every entry | 1.6, 1.18 |
| 4.7 | Plugin SDK, `emit`, HTTP and inbox, the conformance kit, generic hook mode with TOML mappings | The example plugin passes conformance; forged plugin output sizes fill the bucket only to `R_TURN_BASE_S` | 1.2 |
| 4.8 | Release channels: Scoop bucket, Homebrew tap, winget manifest | A tag on a fork produces every artifact | 0.6 |
| 4.9 | Self-update with the Windows rename swap | A bad manifest signature is rejected; a new binary whose `version` fails is never swapped in; the swap passes on all three OS runners | 4.8 |
| 4.10 | Code signing: Azure Artifact Signing or SignPath for Windows, quill notarization for macOS | `signtool verify` and `codesign -v` pass. Needs Karim's accounts | 4.8 |
| 4.11 | VS Code extension: per-platform VSIX, status bar | The VSIX loads in `@vscode/test-electron` and finds its binary; a bundled binary that fails `version` never replaces the installed one | 4.8 |
| 4.12 | Tray on Windows and Linux; `pairtime statusline` | Builds without cgo for those targets; a `windows-latest` smoke creates and removes the icon | 1.22 |
| 4.13 | `loadtest.yml` (10.8) | p99 ingest under 250 ms, no 5xx, server RSS under 1 GB at the 8x peak with 5,000 SSE streams for 15 minutes | 2.6 |
| 4.14 | Red-team fixtures T1 to T15 plus JT's attacks, with expected credit, as CI goldens | 0 for T1, T2, T3 with `input_keys`, T7 and T14; 0 witnessed for T8; the free attempts only for T15; the side-door, offline-flush and withdraw-and-redraw cases as specified (TE WP8.4; JT graft 5) | 3.4 |
| 4.15 | Calibration tooling: the fitter, sensitivity analysis, the four baselines, the R2 report generator | On synthetic labels made from known parameters, the fitter recovers them within 10% (AD WP 5.6) | 3.15 |

**M5** (12-21 to 01-17)

| WP | Deliverable | Acceptance in CI | Depends on |
|---|---|---|---|
| 5.1 | Typing module, opt-in, signed builds only | On `windows-latest`, `SendInput` keys count as injected; under Xvfb, XTEST input counts as injected; a schema test proves no field can hold a key identity or per-key time | 4.10 |
| 5.2 | VM label and D17 | Fixture CPUID values: a guest is labeled, a Hyper-V root partition isn't; module-only level A drops to B on a labeled machine | 5.1 |
| 5.3 | OS age signal (8.2), shipped by 2026-12-31 | A fake under-13 band refuses sign-in and deletes local data; "no signal" proceeds; the band never appears in any upload | 1.24 |
| 5.4 | Recovery code, backups, `pairtime restore` | A backup restored on a fresh runner image reveals and verifies a fixture submission | 2.7 |
| 5.5 | Coach: logbook and project stories | Golden outputs for the SM 6 fixtures | 1.22 |
| 5.6 | Coach: devlog drafts, publishing, the coach skill, `ai_assisted` | Golden drafts; an agent-written draft publishes with the label | 5.5, 2.12 |
| 5.7 | Habit insights and shared recap feedback, both opt-in | Nothing runs or uploads without consent | 5.5 |
| 5.8 | Seeded known-answer cases and reviewer statistics, before 2027-01-04 | Seeded cases appear at about 2% and never reach a program | 3.17 |
| 5.9 | Ari `pairtime` source proposal with a reference mapping | Mapping unit tests; the upstream change is Ari's maintainers' to accept | 3.16 |

**v1.5**, after 01-17: Zed `threads.db` and Warp `warp.sqlite` readers, Cursor's `state.vscdb` reader, shell integration (OSC 133 and 633) for `CMD` and `TEST` anchors, WSL mode, `pairtime mcp`, Dolos for public repos, the opt-in server-side WakaTime endpoint for machines where the client can't run (AD WP 5.7), and the `editor_typing` label. **v2**: the browser extension, opt-in sampled screen evidence (EV 6.8), GLiNER redaction at reveal time, per-user typing profiles, and a macOS menu-bar app.

**Cut first**, if time runs short, in this order (AD 11.2; TE 11.2): club boards, OG images and README badges; habit insights; package-manager channels (the scripts and the VSIX remain); the tray; the Gemini, Devin and Cline native adapters (the bridge covers them at level C); webhooks and `updated_since`, which tiers 0 and 1 don't need; self-update. **Never cut**: the dual-send safety work and its e2e job, the Claude Code adapter, the scorer and its fixtures, the ledger and witnessing, the Hackatime-compatible reads (JB must-fix 4), submissions with the reveal, the review page and the handbook text, the bundle and `verify`, HCA sign-in, the firewall, the coach alpha (session recap, chess clock, welcome recap, build days), the consent-card test, the OS age signal, the load test, and the license gate.

### 9.8 Tracking progress

The server counts, per program and per week: installs (devices first seen), devices still uploading after 4 weeks, submissions created, decided and approved, approved hours and weighted-project equivalents, and each route's install-to-submit rate against 9.1's assumptions (TE 8.6). It needs no extra telemetry. The number that decides the bounty is HQ's count of Unified DB rows, so a weekly reconciliation against it is the first thing to ask HQ for (HC OQ 13).

## 10. Operations and cost

Karim's direction is that a fleet agent self-hosts the server on Karim's domain (B). This section is written for that agent. The host never builds anything: `release.yml` builds and signs the image in GitHub Actions and publishes it to GHCR, and the host only pulls pinned tags.

### 10.1 Deploy shape

One binary, `pairtimed`, in one container with Litestream, plus `cloudflared` (SP 9.1):

```yaml
# deploy/compose.yml
services:
  pairtimed:
    image: ghcr.io/some-du6e/pairtimed:<pinned version>   # distroless static: pairtimed and litestream
    restart: unless-stopped
    command: ["litestream", "replicate", "-config", "/etc/pairtime/litestream.yml"]   # its exec runs "pairtimed serve"
    volumes:
      - ./data:/data                  # SQLite database, WAL and keys; local disk only, never NFS or SMB
      - ./config:/etc/pairtime:ro     # pairtime.toml, litestream.yml, tier2.toml
    env_file: ./config/secrets.env    # mode 0600
    healthcheck:
      test: ["CMD", "/pairtimed", "healthcheck"]
  cloudflared:
    image: cloudflare/cloudflared:<pinned dated tag>
    restart: unless-stopped
    command: ["tunnel", "--no-autoupdate", "run"]
    env_file: ./config/tunnel.env     # TUNNEL_TOKEN
```

- `cloudflared` makes outbound-only connections, so a home server behind NAT gets `https://<host>` with no open ports, at no cost on any Cloudflare plan (SP 9.1).
- Litestream's `exec` runs `pairtimed` as its subprocess, and `restore-if-db-not-exists` restores the database from the replica when the local file is missing, so rebuilding the server is starting the container on a new box (SP 9.1).
- SQLite runs in WAL mode with `synchronous=NORMAL`. Clients re-send anything lost to a power cut from their own queues (SP 9.3). A batch that writes an evidence index row is the exception. It can guard a blob's PUT or delete, which a power cut can't undo and no client re-sends, so the writer commits it with `synchronous=FULL` (7.11).
- Ingest requests are capped at 256 KB compressed and evidence uploads at about 1 MB per 10 submitted hours, far under Cloudflare's 100 MB body limit (SP 9.1).
- The evidence store is a bucket prefix, `evidence/`, holding sealed blobs keyed by SHA-256 (7.6), with index rows in SQLite.

### 10.2 Configuration and keys

`config/pairtime.toml`, from the template `pairtimed init` writes:

| Key | Value at launch |
|---|---|
| `base_url` | `https://<host>` |
| `signups_open` | `false` until the controller is named and the privacy notice is published (8.6). `pairtimed` won't start with `true` while `controller.name` or `controller.contact` is empty |
| `operators` | The HCA subs of the people running this instance, who may get an account while `signups_open` is `false` so they can smoke-test it (8.6; 10.5 step 5); `pairtimed init` writes it empty. Karim decides who is on it, and alpha testers aren't. The list grants no role, and HCA's eligibility check still applies |
| `controller.name`, `controller.contact`, `controller.dpo` | Empty until the controller is named (8.6; 13.1 item 1), and the privacy notice shows "not named yet" in their place. `contact` and `dpo` are an email address or an https URL; `dpo` is the route to Hack Club's Data Protection Officer, set when Hack Club is the controller |
| `profile_default` | `"ask"` until the controller signs the assessment in 8.3; then `"public"` |
| `scoring_version` | `0.x` until the freeze; `1.0.0` from 2027-01-04 (6.7) |
| `reviewers` | HCA subs with roles: `program_reviewer:<program>`, `qi`, `fraud_squad`, `spot_checker`, `admin` |
| `hca.client_id`, `privacy_hub.aud`, `privacy_hub.jwks_url` | From HCA and HQ (10.5) |
| `sentinel.socket` | Empty: the optional private sentinel is off (7.2) |

`config/tier2.toml` holds the deployed tier-2 thresholds and weights. It is never committed to any repository; the repo holds SM 8.7's public starting points as defaults (TE D13).

`/data/keys/`, generated on the host by `pairtimed init`, and by `pairtimed keys rotate-evidence` for a new evidence key, never in git, and backed up encrypted to Karim's offline `age` key with `pairtimed keys backup --age <public key>`:

| Key | Use | Rotation |
|---|---|---|
| `server.ed25519` | Receipts, manifests, day roots | Yearly or on suspicion. The new key id goes into `/.well-known/pairtime-keys.json` with validity dates, and old public keys stay published forever so old bundles verify |
| `evidence.p256` | Sealing tier-2 content (7.6) | Only on compromise, following `deploy/runbook.md`, which first closes the way the key leaked. `pairtimed keys rotate-evidence` makes a new key and backs it up with the old one, and `pairtimed` switches to the new key when it next starts, before it serves a request. From then `/.well-known/pairtime-keys.json` lists only the new key, uploads sealed to any other are refused (7.6), and `pairtimed` won't serve with the old key again. Only then does `pairtimed` re-seal the stored blobs and delete the ones sealed to the old key. Each re-sealed copy has its index row before it is written and takes the old row's place in one transaction, and the old blob keeps its row until it is deleted, so no crash, power cut, purge, erasure or retention delete during a rotation leaves a copy that no row names. Rotation deletes no blob that no row names, and it keeps the old key until nothing is sealed to it |
| `pepper` | `HMAC(pepper, root_sha)` and name HMACs | Never; losing it breaks project identity |

### 10.3 Backups and the restore drill

| Layer | Mechanism | RPO and RTO |
|---|---|---|
| Continuous | Litestream to object storage. `sync-interval` 10 s, which stays inside R2's free request tier (1 s would cost about $7 a month on R2 and nothing on Hetzner Object Storage); snapshots every 24 h, retention 72 h (SP 9.2) | About 10 s of writes; minutes to restore on start |
| Nightly | `VACUUM INTO` a snapshot, encrypted with `age` to the offline key, uploaded to a second bucket with a 30-day lifecycle. Needed because Litestream v0.5 refuses its old encryption option, so continuous replicas are only as private as the bucket (SP 9.2) | 24 h; tens of minutes |
| Evidence | Sealed blobs, each written once (`If-None-Match: *`) under its SHA-256, which every read checks, with keys scoped to the two buckets. The buckets keep no versions, so a purge (7.11), retention (4.4), erasure (8.9) or key rotation (10.2) leaves no old version behind. R2 has none, and `pairtimed doctor` fails on a bucket that keeps them | Not backed up. Litestream and the nightly snapshot copy only the database, which holds the index rows, so a blob deleted or overwritten with the bucket's key is lost and its item shows as unavailable (7.5 step 8) |
| Drill | `restore-drill.yml`, weekly and on every image bump: restores the latest replica into a throwaway container, runs `PRAGMA integrity_check` and the API smoke tests, and fails loudly | Proves the backups work without using the home server's CPU |

### 10.4 Monitoring

- `GET /healthz` (process up) and `GET /readyz` (database writable, Litestream lag under 60 s, object storage reachable), and Prometheus metrics on a private port: ingest rate, batch latency, open SSE streams, writer queue depth, Litestream lag (SP 9.3).
- Two dead-man's switches on Healthchecks.io: Litestream's own heartbeat ping (v0.5.6 and later), and `pairtimed` pinging a second check every 5 minutes while `/readyz` passes. Both alert Karim. Push checks cost no CI capacity, so there is no scheduled uptime workflow.
- Image tags are pinned and bumped by Renovate pull requests, never `latest`, so every update is a reviewed diff whose restore drill has passed.

### 10.5 What a deploy agent needs

From Karim, once:

1. **A host**: Linux with Docker and Compose v2, at least 40 GB of local disk, outbound HTTPS, always on. Karim's home server for the pilot.
2. **DNS and tunnel**: Karim's domain on Cloudflare; a tunnel with the public hostname `<host>` pointing at `http://pairtimed:8080`; its token.
3. **HCA**: a registered OAuth client with redirect URI `https://<host>/auth/hca/callback`; its id and secret; and the HCA subs of the people who will run the instance, for `operators` (10.2).
4. **Object storage**: a bucket for Litestream and evidence, with no lifecycle rule that deletes objects; a second bucket with a 30-day lifecycle for nightly snapshots; and access keys scoped to those two buckets. Neither bucket keeps versions. R2 has none, and on a store that has them a versioned bucket would keep what purges, retention, erasure and key rotation delete and what Litestream and the lifecycle prune (10.3), so `pairtimed doctor` fails on one.
5. **An `age` key pair**: the public key on the host; the private key offline with Karim, never on the host.
6. **GitHub**: the public repository `pairtime-witness`; a fine-grained token with contents write on that repository only; a read-only token for forge checks; optionally the GitHub App's id and private key.
7. **Healthchecks.io**: two check URLs.
8. **From HQ**, once registered: the privacy hub `aud` value; the reviewer allowlist; the controller's privacy notice text.

Then, following `deploy/runbook.md`:

1. `docker compose pull` the pinned release.
2. `docker compose run --rm pairtimed pairtimed init --base-url https://<host>`: generates the keys and the pepper in `./data/keys`, writes `pairtime.toml` from the template, prints the public keys. Back up the keys: `pairtimed keys backup --age <public key>`.
3. Fill `secrets.env` and `tunnel.env`, mode 0600.
4. `docker compose up -d`, then `pairtimed doctor`, which checks HCA discovery, both buckets, that neither keeps versions, that the first refuses a second write-once put to one key, replication, a dry-run push to `pairtime-witness`, both Healthchecks URLs, and `/healthz` through the tunnel.
5. Put the operators' HCA subs in `operators`. Leave `signups_open = false` and `profile_default = "ask"` until Karim confirms the launch gates in 8.3 and 8.6; `pairtimed` won't start with `signups_open = true` until `[controller]` has a name and a contact (10.2).
6. Upgrades: merge the Renovate PR after CI passes, pull, restart. Migrations are additive in v1 and run at start; a rollback is the previous tag.

**Moving to a VPS.** Before the native program pays out, the same compose file moves to a Hetzner CCX13, or the AX42 fallback while CCX plans show "currently unavailable": restore from the replica, point the tunnel at the new host, stop the old one (SP 9.3; AD risk 12). A payout-day outage on a home connection costs program trust that a $49 box avoids.

### 10.6 Cost at R11 scale

R11 caps hosting at $500 a month for 15,488 monthly active users logging 109,759 hours a month (B). The client scores, so the server stores derived rows. The load model is SP 2's and PA 5's, with this design's differences:

| Quantity | Value | Source |
|---|---|---|
| Hours per active user per month | 7.09 | PA 5.1 |
| Concurrent users: average, daily peak (4x), event peak (8x) | 152, about 610, about 1,220 (peak factors assumed) | PA 5.1 |
| Ingest requests | 6.6M a month at a 60 s sync plus at most 4.4M hook-triggered flushes coalesced within 10 s: about 11M a month, 4.2/s average, about 34/s at the 8x peak; about 0.5 KB each gzipped, 5.5 GB a month in | SP 2; EV 9 |
| Signature checks | One Ed25519 verification per request: a few percent of one core at peak (estimate) | SP 8.3 |
| Writer commits | At most 20/s at any request rate | SP 7.4 |
| Bucket upserts | Pending-then-final revisions roughly double them, to about 13M a month, riding the same requests | TE 9.1 |
| Minute buckets | 0.6 to 1 GB a month; per-minute detail kept 180 days, then daily totals: about 6 GB at steady state | PA 5.2; 4.4 |
| Witness rows | About 0.5 GB a month, kept 180 days: about 3 GB | SP 8.5 |
| Commits, accounts, rollups, devlogs | Well under 1 GB a year, plus about 93 MB a month of devlogs | PA 5.2; TE 9.2 |
| Evidence | 50 to 100 KB of metadata and a few KB of sealed content per submitted hour: about 1 GB for the 10,000-hour target | EV 9 |
| Opt-in backups | About 3 MB per user per month before prompt text (TP B6); at 20% uptake and 180 days, about 56 GB, under $1 a month on R2 | estimate |
| Open SSE streams | Up to 5,000 at an event peak: 50 to 100 MB of RAM | SP 6.2 |
| Heartbeats stored on the server | None: the dual-send goes to `127.0.0.1` | 5.6 |
| Server-side LLM calls | None | |

| Setup | Monthly | Share of $500 |
|---|---|---|
| Pilot on Karim's home server, R2 inside its free tier | About $1 to $3 of electricity | under 1% |
| Hetzner CCX13 (2 dedicated vCPU, 8 GB, 80 GB NVMe) plus Object Storage and snapshots | $48.94 | 10% |
| CCX23 primary plus a CCX13 warm standby plus Object Storage | $149.68 | 30% |
| Fallback while CCX plans are unavailable: AX42 dedicated plus Object Storage, €49 one-time setup | $93.77 | 19% |
| Cloudflare free plan, tunnel included | $0 | |
| Code signing, outside the hosting line: Azure Artifact Signing about $9.99 a month (or SignPath, free) and Apple's Developer Program at $99 a year | about $18.24 | |
| Recommended once a program pays out: warm standby plus signing | about $168 | 34% |

Prices are SP 9.4's and PA 5.5's at ECB 1.0968 USD per EUR; for comparison, Hackatime's bill is $213 a month and its effective cost $55 (B). What would break the budget is excluded by design: per-event server-side LLM calls (about $1,100 a month at 2.2M turns and $0.0005 each), storing content for logged hours, screen capture at about 66 MB per recorded hour, and recomputing aggregates on the hot path (PA 5.5; HC DI 19).

**Proof.** `loadtest.yml` replays synthetic clients at the 8x peak (1,220 devices syncing every 60 s plus hook bursts, 5,000 SSE viewers, program reads) against `pairtimed` in a container limited to 2 vCPU and 8 GB, the CCX13's size, for 15 minutes. It fails if p99 ingest latency exceeds 250 ms, any 5xx appears, the writer falls behind, or server RSS passes 1 GB, and it publishes the report as an artifact.

**What breaks first** past R11: the single SQLite writer, then SSE memory at tens of thousands of streams. The steps are Postgres with the same schema, which the nightly suite keeps working, then a second process with a broker (SP 9.5).

## 11. Risks

Ordered by how badly each would hurt the race to 1,000 weighted projects.

| # | Risk | Why it matters | Mitigation |
|---|---|---|---|
| 1 | pairtime breaks a teen's Hackatime: a malformed key fails wakatime-cli's whole run, and its handling of a dead local destination is undocumented (HC 1.5) | One report in a program channel ends route B, and coexistence is the adoption bet (AD risk 1) | Key validation with wakatime-cli's own regex before writing; `e2e-wakatime.yml` against the real binary, agent-down case included, blocks every release; `doctor` checks both destinations; byte-for-byte uninstall (5.6) |
| 2 | HQ doesn't agree how to count pairtime projects, or agrees late (HC OQ 1) | Approved hours may never be attributed to pairtime | The counting rule is the first question in week one (9.5); every justification carries the fixed first line, the review URL and the bundle hash whatever HQ decides; pairtime's outcome ledger tracks progress meanwhile (9.2) |
| 3 | Level B isn't accepted as "significant evidence" | Most Hack Clubbers are on Windows, where v1 is level B by default (B, R8). If level-B AI time stays capped at a third, pairtime's credit advantage shrinks to pre-deflation and evidence | The question goes to Q&I in week one; the typing module ships in signed builds before the first payout; bands, must-view rows and audit weight make level B reviewable; the pilot measures approval by level, and `COMPOSE_NEEDS_KEYS` is the prepared fallback (6.7). Pre-deflated hours still save reviewer time |
| 4 | The native program isn't funded or approved in time (HC OQ 9) | Routes A and D carry 500 of the 1,200 planned weighted projects | The proposal goes out in week one with a fallback: an opt-in pairtime track inside a route-B program, then a smaller first season (9.4) |
| 5 | grit reaches programs first (PA 1.14) | The bounty goes to the first system to 1,000 | The program path ships by mid-December, the coach in the first alpha, the R2 report before the first decisions; pairtime competes and doesn't merge (13.1 item 7) |
| 6 | Calibration fails: too few labeled sessions by 12-27, or κ under 0.6 (SM Risk 9) | No accuracy claim for R2, and the first decisions wait | Recruit over winter break with Lookout as the recorder; the D8 sampler aims labeling at the sessions where rules and people disagree; ask HQ for Stardance's review data (13.2). If the set is still thin, freeze 1.0.0 with the v1 defaults, publish the R2 report with what was measured, and let Q&I decide |
| 7 | Unsigned Windows binaries: SmartScreen starts every unsigned release at zero reputation, and Smart App Control on Windows 11 blocks unsigned executables (CP 4.3) | The audience is the 10-year-old Windows laptop | Azure Artifact Signing under Hack Club applied for in week one; the VSIX channel; the alpha ships through `irm ... \| iex`; most target laptops run Windows 10, which has no Smart App Control |
| 8 | Users without Hackatime are invisible to Robert (HC 3.5) | Fraud Squad can't score them with today's tools | The native program requires a Hackatime link until Robert accepts `kind: "pairtime"`; pairtime's Fraud Squad desk covers pairtime-only users (7.2) |
| 9 | Exact re-scoring holds honest minutes (CC6 false positives) | Honest teens see held hours, and reviewers lose trust in the checks | Integer scoring, serialized checkpoints and the two-tab fixtures (6.5); reviewers can clear held minutes; the hold rate is monitored in the pilot, and above 1% of honest minutes CC6 switches to flag-only by a config flag until the cause is fixed |
| 10 | The controller isn't named, or counsel rules against a public default for minors (TP OQ 1, 5) | Sign-ups can't open, or R5's literal default can't ship | The alpha runs local-only; the assessment is already written (8.3); `ask` mode stays available, and HQ decides whether it meets R5 with the assessment as the record |
| 11 | Live interviews with minors aren't allowed, or there are too few people to hold them (TP OQ 4, 14) | The audit's catch rate drops, and the break-even `p` rises | The asynchronous audit (7.9); HQ can raise `AUDIT_BASE_P`, which is published |
| 12 | Harness formats change between releases; Codex and Claude Code need one-time trust (HS DI 7, 6.1) | A broken parser silently drops anchors | Captured fixtures per version, parse-failure counters in Settings and `doctor`, CC1 reported as not computable instead of failed, log readers covering untrusted hooks |
| 13 | A revealed prompt exposes something, because redaction recall is imperfect (TP B4) | A minor's private detail reaches a reviewer | A small `k`, sealing past Cloudflare, logged reviewer-only access, code collapsed in agent messages, the purge button, the exposure report, no text in bundles, deletion at the end of the window (8.7) |
| 14 | The home server goes down near a program deadline (SP 9.3) | An outage on payout day costs program trust | Clients buffer; the same compose file moves to a VPS before the native program pays out (10.5) |
| 15 | Hackatime's program API moves: HCA-only sign-in began 2026-11-13, and a draft PR would move heartbeats to ClickHouse (SP 3.1; HC 1.10) | Tier 1 mirrors Hackatime's shapes | Contract tests pinned to Hackatime `43bde10b02db` and Stardance `b1d3e0eac1ae`; a watch on upstream changes to the stats and trust endpoints |
| 16 | Agents skip Karim's entry, or teens object to its per-message cost (HS 11.2, 11.3) | The fallback path under-credits, or install feels expensive | Hooks and log readers stay primary, and the entry goes only where neither sees the agent (5.4); calibration volunteers who opt a hooked harness in with `--track` let the compliance counter measure how often agents follow it; the consent screen states the cost and lets the teen untick any harness |
| 17 | Program reviewers find the evidence model too heavy (EV risk 11) | They fall back to the flat third, and pairtime loses its reason to exist | The headline and the bands carry the decision and everything else is a click deeper; the 10-minute walkthrough; median review time measured in the native program |
| 18 | The old laptop suffers: lookback on a spinning disk, antivirus flags on a Go binary (CP 1.6) | The R8 audience uninstalls | Low-priority lookback with a progress bar; no keyboard listener in unsigned builds; footprint gates from the first agent build; `doctor --report` numbers from real pre-2017 laptops as a 1.0 gate |
| 19 | Teens feel watched, or get notification fatigue (PA 4.4) | R3 retention falls | One notification per session, quiet hours, a pause switch, local-only mode, recap copy that names the agent's time after the agent and never scolds |
| 20 | The coach says something false, such as "you fixed it by hand" when the agent did (TE risk 7) | Teens stop trusting the numbers | Moment rules claim only what the events show and err toward saying nothing; "Was this right?" answers flag the rules that misfire |
| 21 | Uncovered setups: SSH and cloud dev boxes, Chromebooks, browser-only builders (SM 3.4; HS 10); and desktops that hide which window is in front, KDE and wlroots in v1 and GNOME on Wayland without pairtime's extension (SM 2.5) | Some teens get no credit, and on those desktops only level C | Stated as gaps; the v1.5 server-side WakaTime endpoint gives those users level C; commits still show on the project story; pairtime's GNOME extension, a default-on line of the install diff, brings GNOME back to level B, and KDE and wlroots focus is on the v1.5 list |
| 22 | Go 1.26 leaves support in February 2027 (CP 1.5) | Staying on it means unpatched toolchain bugs; moving drops macOS 12 Macs | Both binaries move to Go 1.27 when Go 1.28 ships; Macs that top out at macOS 12 keep the last Go 1.26 build until `minimum_version` is raised past it (3.12; 13.1 item 8). |
| 23 | India's DPDP s. 9(3) from about 2027-05-13 (TP B2) | Indian under-18s may not be allowed the monitoring | Local-only mode as the fallback if counsel says so (8.10) |

## 12. Must-fix resolution table

Every must-fix from the three judges: JB "What the final design must fix, whatever the base" (12 items), JE "What the final design must fix" (11), and JT "Must fix, whatever base is chosen" (16). None is rejected. Two are sequenced against each other (JB 10 and JT 6), and the row says how.

| # | Judge | Must-fix | Resolution | Where |
|---|---|---|---|---|
| JB 1 | Bounty | Make attribution unambiguous: pairtime's credited figure is the program's hours number, not evidence on top of Hackatime hours; get the counting rule in writing in week one | Adopted. `Optional - Override Hours Spent` is pairtime's figure after cuts on every counted row; AD 8.3's "optional evidence above a third" pitch is replaced; the fixed first line, review URL and bundle hash make counting one query; asked in week one | 1.1, 9.2, 9.5 |
| JB 2 | Bounty | Calibrate before any program decides: SM 7.5's labeled pilot, two raters, κ ≥ 0.6, freeze 1.0.0, publish the R2 report; reviewer cuts can't replace the labeled set | Adopted. Labeled sessions 12-07 to 12-27, freeze by 2027-01-04, R2 report against the four baselines; outcomes return `409 scoring_not_frozen` before the freeze; cuts and raises only add labels | 6.7 |
| JB 3 | Bounty | Evidence levels mean exactly what QI is told: level A needs an OS keyboard signal; editor change-shape gets its own label and never sets `input_keys`; `track` minutes carry `via_track`; one published definition | Adopted. One definition served at `/docs/evidence`; `editor_typing` is a label only and isn't in v1; `via_track` on every `track` minute | 6.4, 5.11 |
| JB 4 | Bounty | Make the Hackatime drop-in real and never cut it: both `filter_by_project` forms, answer or refuse every Stardance call, contract-test against Stardance's real request shapes | Adopted. All ten calls answered or deliberately refused, including a scoped token for `api_keys`, `get_user_by_email`, an empty `my/heartbeats` and a refused `heartbeats.bulk`; contract tests from captured Stardance requests; on the never-cut list | 5.10, 9.7 |
| JB 5 | Bounty | Never break a teen's Hackatime: the real wakatime-cli e2e job, agent-down and offline-replay cases, blocks every release | Adopted | 5.6, WP 1.19 |
| JB 6 | Bounty | Make the native program a week-one ask with a fallback; plan for at least 1,200 weighted projects; reconcile weekly with HQ | Adopted. Written proposal in week one, a funded fallback, a 1,200 plan, weekly reconciliation | 9.1, 9.4, 9.6, 9.8 |
| JB 7 | Bounty | Settle level B early: SM OQ 1 in the first QI conversation; apply for Azure Artifact Signing under Hack Club in week one | Adopted | 9.5, 9.6 |
| JB 8 | Bounty | Give Fraud Squad a view of pairtime-only users: tell programs, require Hackatime in the native pilot, send the `kind` proposal in week one | Adopted, plus pairtime's own Fraud Squad desk so pairtime-only users are searchable on pairtime | 7.2, 9.4, 9.6 |
| JB 9 | Bounty | Honor Karim's direction exactly; the model-written summary and the hookless-only install are proposals for Karim | Adopted. The entry passes the user's prompt with PII stripped to `track`. On 2026-11-20, under Karim's delegation, the model-written summary was declined and the hookless-only install adopted, with `--track` for any other harness (13.1 items 2 and 3); AGPL, human time and self-hosting are kept | 5.4, 13.1 |
| JB 10 | Bounty | Ship R5's literal default: public by default, aggregate-only, equal-weight "Make private"; `ask` only by HQ's written decision | Adopted for the product: `profile_default = "public"` is the code default and the system the bounty judges is public by default, with an equal-weight choice. Sequenced with JT 6: Karim's instance launches in `ask` until the controller signs the assessment 8.3 supplies, because a legal review of a minors' default can't come after publication | 8.3 |
| JB 11 | Bounty | Keep darwin on Go 1.26 until February 2027 | Adopted for both binaries until Go 1.28 ships in February 2027, then Go 1.27 (13.1 item 8) | 3.12 |
| JB 12 | Bounty | Re-check every TP-derived number when TP's fact-check lands | Done: TP is fact-checked (2026-11-19). The A6 arithmetic was re-derived and stands (0.28 at 40 h, 0.08 at 10 h), so the audit formula is unchanged; the $85 per weighted project comes from a staffer's blog, so the native program's budget is confirmed with HQ; New York's consent rules were confirmed and extended (revocation, device refusal signals), and 8.4 builds them in; retention uses TP B8 with JT's changes | 7.9, 9.4, 8.4, 4.4 |
| JE 1 | Engineering | Packages sized to a session, each with an oracle the agent didn't write | Adopted. TE 10.1's rules; AD's oversized packages re-cut (the scorer into 1.6 to 1.9, the agent into 1.1 to 1.4, Hackatime reads into 3.10 to 3.12, release into 0.6, 4.8 and 4.9); every check names its oracle | 9.7 |
| JE 2 | Engineering | Real harness fixtures: a capture package per harness and version, a scrub step, provenance notes, synthetic fixtures marked | Adopted. An adapter can't ship on synthetic fixtures alone; `fixtures.yml` checks provenance | 5.1, WPs 1.15, 4.1 to 4.5 |
| JE 3 | Engineering | A CI budget: path filters, `-race` on Linux only, Postgres nightly and on SQL PRs, `concurrency` with `cancel-in-progress`, cross-compile in one job | Adopted. `ci.yml` stays PR1's single job; Windows and macOS only in path-filtered `platform.yml` | 3.13 |
| JE 4 | Engineering | Footprint gates from the first agent build with heavy dependencies imported; decide on OTLP protobuf; make the CI hook threshold and the R8 claim one number or state their relation | Adopted. Gates from WP 1.1; no SQLite on the client; OTLP JSON only; zstd deferred; the runner gate is the hook's cost over an empty Go program timed in the same job, at most 10 ms, under the R8 claim's 50 ms as a ceiling; the R8 claim itself (50 ms on old hardware) is a 1.0 gate from `doctor --report` | 3.5, 3.12, 9.7 |
| JE 5 | Engineering | The dual-send with the agent down, before any release that edits `~/.wakatime.cfg` | Adopted (as JB 5) | 5.6 |
| JE 6 | Engineering | A scoring-version policy: parameter-only refits, rare rule changes, a stated number of rule versions carried, no all-versions verifier | Adopted. MINOR is data, MAJOR is rare, two MAJORs carried, `verify` exits 3 and names the release for an older MAJOR | 6.6 |
| JE 7 | Engineering | An exact list of what a submission reveals about other projects, tested with SM 6.3's two-tab variant | Adopted. The reveal set with checkpoints, stated to the teen, with two-tab and moved-minute tests | 6.5, WP 3.3 |
| JE 8 | Engineering | `go build` and `go test` without Node | Adopted. A checked-in placeholder `dist/` | 3.12, WP 0.8 |
| JE 9 | Engineering | A simpler ingest signature than RFC 9421 | Adopted. Ed25519 over a fixed envelope with a request counter | 4.5 |
| JE 10 | Engineering | Signing and the HCA client in week one; no keyboard-listener code before signing; a prerelease pipeline and both install scripts before the first alpha | Adopted | 9.6, 7.2, WP 0.6, WP 5.1 |
| JE 11 | Engineering | No flaky platform checks: fakes on hosted Windows and macOS, Xvfb with `xdotool` on Linux | Adopted | 3.13, WPs 1.10 to 1.13 |
| JT 1 | Trust | Close the side doors on `input_any` machines: `track`, plugin, bridge and editor-extension anchors are level C unless the harness's log or `input_keys` corroborates them; plugin output sizes don't fill the reading bucket past `R_TURN_BASE_S`; CI fixtures with `input_any` true and `input_keys` unknown | Adopted, plus a peer-process check for editor heartbeats, and D19 so side-door records can't raise the handbook ceiling that caps level C | 5.2, 5.4, 5.7, 6.2 D2, D3 and D19, 6.4, WPs 1.6, 4.6, 4.7, 4.14 |
| JT 2 | Trust | Cap all offline minutes per submission whatever the flush delay, banded below witnessed | Adopted. 3 hours per submission, a forge push within 24 hours, its own band, never pre-approved | 6.2 D6, 7.4 |
| JT 3 | Trust | Make the challenge sticky: withdrawing counts drawn items as withheld; overlapping later submissions reuse the draw | Adopted. The drawn set only grows | 7.5 |
| JT 4 | Trust | Keep revealed text out of downloadable bundles; fetch it under the access log; keep it until the spot-check window closes, 12 months until HQ sets one, never "the longer of"; a purge button and a reviewer exposure report | Adopted | 7.8, 7.11, 4.4 |
| JT 5 | Trust | Encrypt tier-2 content end to end to the evidence store, past Cloudflare | Adopted and extended to the viewing direction with WebCrypto, since review pages also pass through Cloudflare | 7.6 |
| JT 6 | Trust | On Hack Club's instance, the unselected choice with "Private" more prominent until a written compelling-reason assessment exists and counsel has looked | Adopted as the launch state of Karim's instance. The assessment is written in 8.3; after the controller signs it the instance switches to R5's public default (JB 10) | 8.3 |
| JT 7 | Trust | Name the operator and controller before the pilot stores any minor's data; publish the retention schedule | Adopted. Sign-ups stay closed, except for the operators' own accounts (10.2), and the alpha runs local-only until the notice with the controller and retention schedule is published | 8.6, 4.4, 10.2, 13 |
| JT 8 | Trust | Request the OS age signal from 2027-01-01, refuse under-13s locally, never upload the bracket | Adopted, shipping before 2026-12-31 | 8.2, WP 5.3 |
| JT 9 | Trust | Write the interview protocol for minors, with the asynchronous audit as fallback | Adopted as a default protocol that Hack Club's safeguarding rules override | 7.9 |
| JT 10 | Trust | Turn the level-B gap into a mechanism: audit weight rises with level-B share; no preset pre-approves level-B or offline minutes; the typing module in signed builds before the first payout; the "degrading" question to counsel; compose credit only with `input_keys` if the pilot shows a gap | Adopted. `COMPOSE_NEEDS_KEYS` is the prepared switch | 6.4, 6.7, 7.2, 7.9 |
| JT 11 | Trust | Let the VM label gate level A, never credit | Adopted, with the Hyper-V root-partition test | 6.2 D17, WP 5.2 |
| JT 12 | Trust | Keep tier-2 feature definitions private beyond SM 8.7's published set | Adopted together with JB's and JE's choice of TE D13: SM 8.7's detectors are public code with private thresholds, and anything beyond them runs only in the optional private sentinel | 7.2 |
| JT 13 | Trust | Minimize the server's copy of logged-only hours: no anchor ids, per-minute buckets only for 180 days, IPs deleted after 30 days, presence sampled only in a project context | Adopted | 4.4, 6.2 D9 and D14 |
| JT 14 | Trust | Don't expose trust levels to programs | Adopted. `trust_factor` is always the unscored value; review history is scoped to the program | 5.10, 7.7 |
| JT 15 | Trust | Drop "don't mention it" from the AGENTS.md entry | Adopted. It was the designs' addition, not Karim's wording | 5.4 |
| JT 16 | Trust | Plan for device loss: a recovery code wrapping `K_dev`, a backup that survives a Windows reinstall, "witnessed, evidence lost" instead of a hole | Adopted, tested on a fresh runner image | 7.11, WP 5.4 |

## 13. Decisions needed

Each item says what the design does until the decision is made, so building never waits on it.

### 13.1 From Karim

1. **The controller of the instance on your domain**, decided with HQ: you, or Hack Club with you operating the server (8.6; TP OQ 1). Until then sign-ups stay closed, except for the operators' own accounts (10.2), and the alpha runs local-only.
2. **Where your AGENTS.md entry is installed.** Decided on 2026-11-20 under your delegation: in the user-level file of each harness pairtime sees through neither hooks nor a log reader, and of any other harness the teen names with `pairtime install --track` (5.4). On a hooked harness the call opens nothing, yet it costs 30 to 60 tokens of context per request, up to about 130 output tokens per message and possibly an extra model request per turn (HS 11.2), and log readers already cover untrusted Codex hooks and a pending Claude Code folder trust. Calibration volunteers can opt one hooked harness in, which keeps the compliance measurement (HS 11.3).
3. **What the entry passes.** Decided on 2026-11-20: the user's message with personal information removed, capped at 400 characters, as you described (TE 4.4). EV 4.4's model-written summary would class a go-ahead as substantive and hide the near-duplicates the re-roll detector needs, and the cap can only under-credit, by at most 33.3 s per prompt (5.4).
4. **The hostname** `<host>` on your domain, and the box: your home server for the pilot, and a VPS before the native program pays out (10.5).
5. **The optional private sentinel** for detectors beyond SM 8.7's set: may Hack Club run one beside the AGPL server, given AGPL section 13 (R1; HC OQ 11; TP OQ 7)? Nothing in this design depends on it.
6. **The native program.** Decided on 2026-11-20: your team proposes Hard Mode, under that name and with 9.4's dates, and Quality and Integrity or the HQ sponsor nominates or approves each of its outside reviewers, so nobody on the build team picks one alone (7.10). You send the proposal (9.6).
7. **grit.** Decided on 2026-11-20: compete (grit.md 5.2). pairtime doesn't merge with grit or contribute code to it, and its calibration results come out only with the R2 report at the scoring freeze, where grit's rules are a baseline (6.7).
8. **Go after February 2027.** Decided on 2026-11-20: both binaries move to Go 1.27 when Go 1.28 ships in February 2027, and Macs that top out at macOS 12 keep the last Go 1.26 build until `minimum_version` is raised past it (3.12).
9. **Repo rules this design changes**: decided on 2026-11-20 under Karim's delegation. The CI layout beyond the single `ci.yml` job (3.13), the nightly `-race` exception to `CGO_ENABLED=0` and the errata rule (6.6) are approved, and the orchestrator decides errata for Karim outside the PR that raised them. AGENTS.md carries the rules.
10. **Scope of remote and WSL setups** (SM OQ 11). This design puts WSL and the server-side WakaTime endpoint in v1.5 and leaves the SSH join open.
11. **Sign-up without HCA** (SP OQ 4). This design allows HCA only.
12. **Accounts only you can create**, all needed in week one (9.6, 10.5): Azure Artifact Signing under Hack Club (with HQ), the Apple Developer membership, the HCA OAuth client, the `pairtime-witness` repository and its token, the Cloudflare tunnel, both buckets, and the offline `age` key.

### 13.2 From Hack Club HQ

1. **The counting rule, in writing** (9.2): a row counts when its justification contains "Tracked with pairtime", a pairtime review URL and a bundle hash. Plus a weekly count or read access, so progress can be reconciled (HC OQ 1, 13).
2. **Two trackers, one number**: for a window tracked by both, the program uses pairtime's figure and doesn't apply the one-third rule again on top (HC OQ 3).
3. **Significant evidence**: whether level A, level B, or both count as "significant evidence proving that more time should be credited", and whether level C is acceptable at the third, including witnessed minutes from desktops that hide which window is in front (6.4; SM 2.5, OQ 1, 8; HC OQ 2).
4. **The spot-check window**, which sets how long revealed evidence is kept. Until then, 12 months after the decision (4.4; HC OQ 4).
5. **The native program**: a sponsor and budget, including the real per-weighted-project grant, since the $85 figure comes from a staffer's blog (TP fact-check row 29); Quality and Integrity running or observing its audits; a higher spot-check rate; Quality and Integrity or the sponsor nominating or approving each outside reviewer (9.4, 7.10; HC OQ 9).
6. **Counsel**, on: the controller question (with Karim); the compelling-reason assessment for the public profile default (8.3), GDPR Art. 25(2) and New York's law; whether a higher audit chance after declining the typing module is "degrading" under § 899-ff(4) (7.9); whether fitting detectors on minors' review outcomes is fraud protection or research (TP OQ 15); whether HCA's verified birthdate counts as "clear and convincing information" against an OS age signal (8.2); and a local-only mode for Indian under-18s from about 2027-05-13 (8.10; TP OQ 6).
7. **Interviews with minors**: who may hold a call, in what channel, and whether anything is recorded. The default in 7.9 applies until HQ's safeguarding rules say otherwise (TP OQ 4).
8. **HCA**: mark pairtime an official app, or accept its reviewer allowlist (SP OQ 1); say how HCA marks an under-13 identity; and provide an eligibility end date, so the 19th-birthday cutoff can be applied without a birthdate (8.2).
9. **The privacy hub**: register pairtime's own `aud` (TP OQ 18).
10. **Code signing**: whether Hack Club's organization holds the Azure Artifact Signing account, and who holds the Apple Developer membership (CP 4.3, 4.4).
11. **Telescreen and Ari**: a `kind: "pairtime"` in Robert with a bundle fetch (HC OQ 6), and a `pairtime` time source in Ari with clear contribution terms (HC OQ 7).
12. **Penalties**: whether a confirmed pairtime fraud verdict is a Hack Club-wide ban (TP OQ 8), whether programs may claw back (TP OQ 9), and how long the minimal fraud record is kept after an account is deleted (TP OQ 12).
13. **The audit rate**: whether HQ will staff the published formula in 7.9 (TP OQ 16).
14. **Calibration data**: Stardance's per-devlog approved and original minutes with the Hackatime heartbeats behind them, under a data-sharing agreement (SM OQ 6).
15. **Requiring the typing module**: this design lets no program require it for submitted hours, because the UK code bars nudging children to give up protections (TP OQ 17). Confirm.
16. **hackatime-setup**: whether its maintainers would take an optional "also install pairtime" step (AD 8.2).
