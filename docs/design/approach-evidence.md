# pairtime design: evidence first

Status: complete, ready for judging

One of three independent end-to-end designs for pairtime, written 2026-11-19 for the judging panel. This one is optimized for R9 (trustworthy) and R10 (auditable), which here means hours that a skeptical Fraud Squad reviewer trusts even when an LLM is driving the agent. It still has to meet every requirement and the acceptance criterion, and section 2 says how.

In one line, the thesis is that every credited minute ships with a receipt that was written while the minute happened, by parties the teen doesn't control, and the receipt stays on the teen's laptop unless they submit that minute.

Requirement IDs (R1-R11, A) are from [docs/BOUNTY.md](../BOUNTY.md). Research docs are cited by short name and section, the same keys the metric spec uses:

| Key | File |
|---|---|
| B | [BOUNTY.md](../BOUNTY.md) |
| SM | [design/struggle-metric.md](struggle-metric.md) |
| TP | [research/trust-and-privacy.md](../research/trust-and-privacy.md) (complete; its fact-check is still pending, so TP claims carry that caveat) |
| HS | [research/ai-harness-signals.md](../research/ai-harness-signals.md) |
| HC | [research/hackatime-and-hack-club.md](../research/hackatime-and-hack-club.md) |
| CP | [research/client-platform.md](../research/client-platform.md) |
| SP | [research/server-platform.md](../research/server-platform.md) |
| PA | [research/prior-art-and-measurement.md](../research/prior-art-and-measurement.md) |

## 1. Pitch

### What a teen experiences

Rowan installs pairtime with one command (`irm https://<karim's domain>/install.ps1 | iex` on Windows, `curl -fsSL .../install.sh | sh` elsewhere) or by installing the VS Code extension, which brings the signed binary with it (CP 4.2). `pairtime install` finds his harnesses and shows one screen listing every file it will touch: hook entries in `~/.claude/settings.json`, `~/.cursor/hooks.json`, `~/.codex/hooks.json`, `~/.copilot/hooks/pairtime.json` and `~/.gemini/settings.json`, one `[api_urls]` line in `~/.wakatime.cfg`, and an autostart entry (HS 16, SP 8.8, CP 2). He signs in by typing a code into a browser page that is already signed in to Hack Club Auth (SP 3.5). His profile starts public, as R5 asks, and the onboarding screen shows exactly what it will show (daily totals, streaks, harnesses, public repos by name, nothing else) next to a "Make it private" button (TP B7). He is offered the typing-evidence module and the bigger button is "No" (TP B2 point 7).

After that he never starts or stops anything. The tray shows today's split, "you 1h 12m, agent 2h 40m", the chess clock from mod-crono (PA 1.13, 4.3). When a work session ends, the local dashboard writes a recap from data that never left the laptop: "you fixed two errors yourself, rejected three agent edits, and asked for the same navbar fix 7 times between 21:10 and 21:40 before your own CSS edit fixed it" (SM 4.2, 6.5). His own dashboard moves within a minute of each credited minute (SP 6.4); his public profile shows daily totals and refreshes hourly, so it never shows when he sleeps or is in class (TP B7). Streaks count credited minutes, not heartbeats, and forgive a missed day (PA 4.3). None of this needs a program. It is a tracker that tells him what he did, which is R3.

When he ships cs2go to a program, he clicks "Connect pairtime" on the program's site, or runs `pairtime submit`. pairtime shows him exactly what will leave his machine: the timeline metadata for that one project in that window, his commits, and then, after the server draws its random sample, 10 of his prompts with the agent message each one answered, already redacted, with every redaction highlighted. He can remove more or withhold a whole item; a withheld item doesn't credit the minutes it supports (TP B6). After he submits, his submission page lists every reviewer who opened his evidence and when (SP 10.3). He knows, because the site publishes the formula, that at least 8% of submissions, and more for big or unusual claims, get a 15-minute call in which the teen walks a reviewer through two commits the server picked and makes one small change live (TP A6).

### What a reviewer sees

A reviewer opens `/review/submissions/:id` and the first line answers the question they are paid to answer: "10.4 h claimed by the program, 9.6 h credited by pairtime: 8.9 h corroborated, 0.5 h witnessed, 0.2 h backfilled. Handbook third from Hackatime-equivalent data would allow 6.1 h." Below it, one strip per hour of work, sixty cells wide, colored by activity and evidence class, worst hours first. Every typed prompt, permission answer, test run and commit is a marker on the strip. The 10 revealed turns sit on their markers, each stamped "matches commitment witnessed 14:03:12". Every commit shows three clocks: when the laptop saw it, when pairtime's server first saw it, and when GitHub received the push. A consistency panel lists checks such as "prompts with keyboard input before them: 212 of 214" and "agent edit lines vs git diffstat: within 3%". Approving an hour is one key. Deflating is a drag across the minutes to cut, with a reason picked from a list. The handbook justification text is generated. A clean 10-hour submission should take a few minutes, and the page is built so that a reviewer spends their time on the 10% of hours that look wrong.

A Fraud Squad member who doesn't trust pairtime's server or its UI downloads the signed bundle and runs `pairtime verify bundle.tar` on their own machine. It recomputes every Merkle root, checks the server's signature and the day's root published in a public git repository, re-runs the scoring version on the revealed metadata and prints the same numbers or a list of differences (SP 10.4, TP B6).

### What a program author does

The zero-code path works today. The program's reviewers read the pairtime review URL, and the Airtable gets `Override Hours Spent` plus the handbook's `Alternate Tracking Method` and `Additional Justification` fields, filled from `GET /api/v1/submissions/:id/justification?format=handbook` (HC 3.7, SP 10.6). The small-code path is what Stardance-style apps already do with Hackatime: register an OAuth client on pairtime, add a "Connect pairtime" button, call `POST /api/v1/submissions` with the program's `external_id` and the devlog window, and receive `submission.reviewed` and `submission.decided` webhooks signed the way Robert signs his (SP 10.1-10.2, HC 3.5). A program whose code only knows Hackatime can point a second `HackatimeService` at pairtime's base URL and get credited seconds in the same JSON shapes (SP 10.5). Once Ari accepts a `pairtime` time source, programs on Ari get pairtime's commit-interval rows as cuttable rows with evidence links and no glue code (HC 3.6).

## 2. Requirements

Each row says how the design meets the requirement and why I believe it will, including where the belief is weakest. Section 11 has the risks in full.

| ID | How this design meets it | Why I believe it |
|---|---|---|
| R1. AGPL-3.0 | The repo's `LICENSE` is already the AGPL-3.0 text (B, checklist). Every client and server dependency is permissive and AGPL-compatible: x/sys, x/crypto, fsnotify, modernc sqlite (BSD-3), godbus (BSD-2), purego (Apache-2.0), go-keyring (MIT) (CP 7); a CI job checks transitive licenses (WP0.1). Ledger test vectors and the Python reference verifier are in the repo, so anyone can implement a compatible verifier | Only one part is uncertain: the private sentinel. It runs as a separate process over a socket, the pattern Ari and Stardance use for their fraud rules (HC 3.6), and whether that fits R1 is TP Open question 7. If it doesn't, the sentinel is published and the design leans harder on sampling (11.1, risk 4) |
| R2. Accurate hours of productive struggle | The SM credit rule as adopted (5.1): only human acts open windows, only with OS presence and project focus; reading is bounded by what was on screen; dark-flow loops earn nothing; two hours without progress earn half. SM 6's worked examples show the R4 stories, an overnight `/goal` run (810 s credited of 6.5 hours) and a scripted loop (0 s) coming out as a reviewer would want | The rule is reasoned, not yet measured. SM 7's pilot fits it against two-reviewer labels with ship targets of minute-level κ ≥ 0.6, session error ≤ 15% and bias between −5% and +3%, against four baselines including the handbook third (SM 7.3). This design adds a steady stream of labels from every reviewer cut (5.4). Until the pilot reports, R2 is a hypothesis with a test plan, and I'd rather say so |
| R3. Worth using for its own sake | Nothing about daily use needs a program. The tray's split of your time and the agent's, recaps written locally ("you asked for the same navbar fix 7 times; your own CSS edit fixed it"), streaks on credited minutes with freezes, consistency-based leaderboards, and a local-only mode that uploads nothing (1, 7.1). The evidence machinery is invisible day to day: it costs a few hashes per event and shows up only when the teen submits | Zach's own reason for loving Hackatime is tracking personal projects he never submits (B, R3). Positive informational feedback raises intrinsic motivation, while engagement-contingent rewards undermine it (PA 4.2), so the daily surface shows what you did rather than a prize meter. And privacy is part of the motivation case: 58% of monitored students say they hold back their real thoughts (PA 4.4), which is why nothing content-bearing leaves the laptop |
| R4. Passive | No start or stop, ever. Hooks fire on their own, transcripts and `.git` are tailed, presence and focus are sampled while a project is open (3.3). SM 6.1 credits three hours of flow at 93% of wall time, SM 6.2 credits 265 of the 285 seconds a lid was open before school, and SM 6.3 splits a five-project afternoon with every second going to exactly one project. Lookback shows 30 days of history on day one (4.2) | The R4 stories are worked through minute by minute in SM 6 and become CI fixtures (WP0.3). The one-time steps are install consent, Claude Code's folder trust dialog and Codex's hook trust, which HS Open question 6 flags as a question for Hack Club; until a hook is trusted, the log reader covers the gap (4.1) |
| R5. Server with accounts, public-by-default profiles, projects, git integration, live dashboard | HCA accounts, the device grant, public profiles of aggregates, projects keyed by root commits, a commit list with three clocks, GitHub App and forge verification, and an owner dashboard pushed over SSE within a minute (3.6, 4.6) | Every piece is specified down to endpoints in SP, and the stack is SP's recommendation. Profiles are public by default, as R5 says, and the public page carries only aggregates: no routine, no location, no private project names, no trace of review (7.3). The tension with high-privacy defaults for minors (TP B7) is handled by what the page shows, a written Children's Code assessment, and a deployment switch to ask instead if counsel requires it (11.1, risk 5) |
| R6. Privacy friendly | Four tiers plus a local-only mode (7.1). Logged hours upload numbers and Merkle roots, never content; submitted hours reveal one project's metadata and a sample of 10 to 30 turns, drawn by a seed neither side controls, redacted on the device and previewed by the teen (6.5). No window titles are ever read. A schema firewall rejects free text (SP 8.2), `pairtime privacy show` prints the next upload, every reviewer view is logged for the teen to see, retention is finite for every field (7.2), and deletion works through Hack Club's privacy hub (7.5) | It matches what privacy-conscious developers accept and avoids what they reject (TP B1): local collection the user can inspect, aggregates instead of content, opt-in for anything content-bearing, random install ids, encryption at rest, finite retention. The typing and screen modules, which those users reject by default, are opt-in with "No" as the most prominent answer |
| R7. All models and harnesses | Model-agnostic by construction, since pairtime never talks to a model. Hooks and log readers for Claude Code, Cursor, Copilot, Codex, Gemini CLI and Devin; Karim's AGENTS.md entry plus `pairtime track` for every harness that reads instruction files (4.4); a plugin interface in-tree or in any language, whose events go through the same evidence rules (4.3) | HS 15 maps every major harness to at least one capture method. The gap is browser chat and app builders, which leave no local signal except git until the v2 browser extension (4.1). That is a real hole, and R7's "or at least support the most popular ones starting out and have a plugin approach" is the clause this design leans on |
| R8. Windows, Mac, Linux on low-spec machines | One pure-Go binary per OS, including `windows/386`; no Node, Python or resident webview; event-driven focus and file watching; budgets of under 25 MB RSS, under 0.1% idle CPU and a hook round trip under 50 ms, checked in CI (WP5.5). macOS builds stay on Go 1.26 until February 2027 for Monterey Macs (CP 1.5) | The footprint numbers come from CP's measurements of comparable runtimes (CP 1.2, 1.3). The evidence layer adds a few hashes per event and a Merkle root a minute. The budgets are estimates until they're measured on a real 2016 laptop, which the pilot's quota of pre-2017 Windows machines covers (SM 7.2) |
| R9. Trustworthy: a submitted hour equals an hour of struggle | The credit rule earns nothing for the cheap attacks (6.12); witnessing makes the record tamper-evident; eleven consistency checks force a forger to agree with independent records; a sample is checked against commitments; a published, hours-weighted audit rate gives every claim a known chance of a live conversation; hours stay revocable (6.2-6.9) | The TP A6 arithmetic: with its example inputs, the audit rate at every claim size sits 10% above the point where fraud stops paying (6.9). The R9 test itself is SM 7.3's: reviewer-approved over credited hours, mean ≥ 0.9 and 80% of submissions between 0.8 and 1.05, measured in the winter YSWS (8.3) |
| R10. Auditable by an external party | A signed, self-verifying bundle per submission; `pairtime verify` re-runs everything offline at the bundle's scoring version; a daily Merkle root of all witness rows in a public git repository, so even the pairtime server is checkable; every evidence view logged; logged-only data never on the server (6.3, 6.6) | Each check is mechanical, with nothing to trust but SHA-256, Ed25519 and GitHub's commit history. R10's own example, logged hours local and submitted hours uploaded, is the tier model. The evidence still has to be accepted by the people who audit, which is risk 1 |
| R11. Under $500 a month at 15,488 MAU and 109,759 hours a month | One small box: about $49 a month on a Hetzner CCX13 with object storage, $150 with a warm standby, $94 on the dedicated fallback (9). Evidence is stored only for submitted hours, and no LLM runs on the server | SP and PA built the load model from Hackatime's own numbers. The extra load from this design is one Ed25519 check per request and one witness row per device-minute, about 0.5 GB a month. A `loadtest` workflow proves it at the 8x peak (WP3.8) |
| A. 1,000 weighted projects in the Unified DB | Plug into each layer of the pipeline (8.2): the handbook's `Alternate Tracking Method` and `Additional Justification` fields today, a `PairtimeService` client and the submissions API for program apps, an Ari time source and a Robert `kind` next. Two pairtime-native YSWS programs plus Ari, Robert and Airtable-form programs, aiming at 11,000 approved hours by the end of April 2027 (8.3, 8.4) | The handbook path needs no HQ change, so the native YSWS can start putting rows in the Unified DB in January whatever else happens. The rest depends on HQ guidance, which Zach offered (B), and on whether Fraud Squad trusts this evidence. That's exactly the bet this design makes |

## 3. Architecture

### 3.1 Shape

```text
 teen's laptop                                                     pairtimed (one Go process, Karim's domain)
+-------------------------------------------------------------+   +-------------------------------------------+
| harness hooks ------> pairtime hook --+                     |   | ingest: signature, chain link, receipt    |
| harness logs -------------------------+                     |   | witness log (device, b, H_b, received_at) |
| Claude Code OTLP ---------------------+                     |   | rollups (additive) --> SSE dashboards     |
| wakatime-cli via [api_urls] ----------+--> pairtime agent   |   | accounts (HCA), devices, projects, git    |
| OS presence, focus, power ------------+    capture          |==>| submissions: reveal, re-score, challenge  |
| .git watcher -------------------------+    ledger + vault   |   | review UI, program API, Hackatime reads   |
| pairtime track (AGENTS.md) -----------+    scorer           |   | bundle signer, privacy hub endpoints      |
|                                            uploader         |   | SQLite --Litestream--> R2                 |
|                                            local dashboard  |   | sentinel (private, separate process)      |
+-------------------------------------------------------------+   +-------------------------------------------+
 Tier 1 every 60 s; Tier 2 only on submit                           | once a day: Merkle root of all heads
                                                                    v
                                                                   public git repo `pairtime-witness`
```

Two Go binaries from one repo, `pairtime` (client) and `pairtimed` (server), sharing four packages: `pkg/event` (the normalized schema), `pkg/ledger` (leaves, batches, chain, reveal proofs), `pkg/score` (the SM rulebook) and `pkg/check` (the public consistency checks). Sharing them is what lets the server re-score a submitted window from revealed metadata and demand the numbers the client uploaded live, so a difference can only mean tampering or a version mismatch (SP 7.1, SM 7.6). Go because the client research picked pure Go for the old-laptop footprint and one-job cross-compilation (CP 1.6), and the server follows the client so scoring exists once (SP 7.3).

### 3.2 Client processes

| Process | Started by | Job | Budget |
|---|---|---|---|
| `pairtime agent` | autostart at login (HKCU Run key, LaunchAgent, XDG autostart), single-instance lock (CP 2, CP 7) | capture adapters, ledger writer, scorer, uploader, submit and reveal engine, redactor, local dashboard and endpoints, tray on Windows and Linux, self-update | under 25 MB RSS on Windows, under 0.1% idle CPU, about 10 wakeups a minute (CP 7 targets, to verify on hardware) |
| `pairtime hook --harness <h> --event <e>` | each harness, exec form | read stdin with a size cap, stamp arrival time, pass one framed message over the agent's Unix socket within 100 ms, else append one line to `inbox/`, start the agent detached, exit 0 with no output (CP 6.2). It never blocks the harness and never fails closed | tens of ms on Windows, mostly process spawn (CP 6.1) |
| `pairtime track "<label>"` | the coding agent, following the AGENTS.md entry (4.4) | same path as `hook`, with `source=cli` | one tool call |
| `pairtime submit`, `verify`, `privacy show`, `ledger show`, `doctor`, `install`, `uninstall`, `dashboard` | the user | sections 1, 6 and 7 | |
| `pairtime redact-ml` (v2, optional download) | the agent, at reveal time only | GLiNER PII edge (uint8, 46 MB) over the few items being revealed, in a short-lived process (TP B4) | a few hundred MB while it runs, then it exits |

No resident process runs Node, Python or a webview (CP 1.4, CP 5.3).

### 3.3 Capture adapters

An adapter turns one source into normalized `pkg/event` records (SM 1) and hands them to the ledger writer. The scorer never reads a harness format, which is what makes new harnesses an adapter-only change (SM 1, HS 17.6). v1 adapters:

| Adapter | Source | Emits |
|---|---|---|
| `hook/<harness>` | hook payloads from the hook binary, or HTTP hooks where every consumer of the config accepts them | `session.*`, `prompt.submit`, `agent.*` |
| `log/<harness>` | transcript tailers with per-file cursors (CP 3.2), and lookback on install | the same kinds with `source=log`: the second, independent record the consistency checks compare against (6.4) |
| `otel/claude_code` | OTLP/HTTP receiver on 127.0.0.1 (HS 16 item 4) | `otel.active_time`, permission decisions; `user.email`, account and organization ids are dropped on arrival |
| `wakatime` | local WakaTime-compatible endpoint fed through `[api_urls]` (SP 8.8) | `editor.activity`, with file paths replaced by per-device HMACs on arrival |
| `os/presence` | `GetLastInputInfo`, `CGEventSourceSecondsSinceLastEventType`, Mutter `IdleMonitor`, `ext-idle-notify-v1`, XScreenSaver, every 15 s (CP 3.3) | `os.presence` |
| `os/focus` | `SetWinEventHook(EVENT_SYSTEM_FOREGROUND)`, `NSWorkspace` activation notifications, `_NET_ACTIVE_WINDOW`; never window titles (CP 3.3) | `os.focus` with app class and hosted sessions |
| `os/power` | suspend, resume, lock and unlock notifications (CP 2.2) | `os.power`, which also forces a seal and upload |
| `os/procs` | a process snapshot every 60 s and on focus change (SM 2.2, `PROC_SCAN_S`) | `session.start` for `--bare` runs that fire no hooks; the parent-chain class |
| `git` | a non-recursive watch on each known repo's `.git` (`HEAD`, `index`, `logs/HEAD`) (CP 3.2) | `git.change` |
| `typing` (opt-in) | Windows Raw Input with `RIDEV_INPUTSINK`, macOS `CGEventTap`, X11 XInput2 raw events, nothing on Wayland (TP A4) | per-slot key-down count, injected share, hold-time median and rollover share. Never key identities, never per-key times |
| `inbox` | `inbox/*.jsonl` lines written by the hook binary while the agent was down, and by out-of-process plugins (4.3) | whatever the line holds, after validation against the public schema |

Presence and focus are sampled only while a project context is live: a harness session is running, or a terminal, editor or agent app had focus in the last 10 minutes. After 10 minutes with no input and no agent turn running, sampling pauses until the next input or hook event. Outside a project context pairtime records nothing about the computer, not even locally. Nothing creditable is lost by this, because SM credits only seconds focused on a project's terminal, editor or agent window, or a bracketed lookup between two of them (SM 2.5).

### 3.4 Local store

Everything lives in the data directory (`%LOCALAPPDATA%\pairtime\`, `~/Library/Application Support/pairtime/`, `$XDG_DATA_HOME/pairtime/`, CP 7):

| Path | Format | Holds | Kept |
|---|---|---|---|
| `ledger/YYYY-MM-DD.seg` | append-only, length-prefixed records `{hdr, body, leaf}`; `body` is deterministic CBOR (RFC 8949 core deterministic encoding) | every normalized event's metadata, including its content commitment `C`. Never content | 180 days (TP B8, `E_i`) |
| `ledger/batches.seg` | append-only `{b, first_seq, last_seq, n, R_b, H_b, sealed_at, receipt}` | batch roots, the chain, and the server's signed receipt for each batch | 180 days, or until every submission that used them is past its spot-check window |
| `vault/` | per-record XChaCha20-Poly1305; key derived from `K_dev`, which sits in DPAPI, Keychain or Secret Service through go-keyring, with a `0600` key file fallback (CP 5.2) | prompt text and each turn's final agent message; file paths for the local recap; typing aggregates and MinHash sketches | prompts and messages 30 days, or until a submission covering them is decided, whichever is later; paths 30 days; aggregates and sketches 90 days (TP B8) |
| `pairtime.db` | SQLite WAL, `synchronous=NORMAL`, commits every 15-60 s (CP 5.1) | the index: sessions, slots, credit slots, buckets, upload state, project map | rebuildable from `ledger/` and `vault/` |
| `inbox/` | JSONL | one line per hook call made while the agent was down | deleted once drained into the ledger |
| `config.toml` | TOML | settings, privacy rules, the redaction deny list, ignored folders (`.pairtime-ignore`, TP B1) | |

The split between ledger and vault is the point. The ledger is what gets proved and the vault is what gets revealed. The ledger alone contains no text, and deleting the device key `K_dev`, from which both the vault key and the token salts are derived (6.2), makes every content commitment unopenable (crypto-shredding, TP B6).

### 3.5 Local UI

The agent serves a dashboard on `127.0.0.1:<port>`, opened in the user's own browser through a one-time URL that sets a `SameSite=Strict` cookie; every request must carry a loopback `Host` and the dashboard's own `Origin` (CP 6.3). On Windows and Linux the agent shows a tray icon through fyne-io/systray, whose Windows and Linux code doesn't import C (CP 5.3 read this from the source rather than a build, so WP5.3 confirms it); the macOS menu-bar helper comes later, because it needs cgo and an app bundle (CP 5.3). Pages: today and this week, the chess clock per project, session recaps, the ledger browser ("what pairtime recorded"), the upload log ("what left this machine", the same output as `pairtime privacy show`), submission previews, and the reviewer access log of each submission.

### 3.6 Server components

One Go process with the Svelte UI embedded, SQLite in WAL mode replicated by Litestream to R2, behind `cloudflared` on Karim's domain (SP 1, SP 9.1), plus one private sidecar.

| Component | Does | Main endpoints |
|---|---|---|
| Accounts | HCA OIDC as a confidential client, asking for `openid email slack_id verification_status` explicitly because discovery doesn't advertise them; the device grant for the client; per-device `ptd_` tokens and Ed25519 device keys (SP 3.1, 3.5, 8.3) | `/auth/hca/callback`, `POST /oauth/device/code`, `POST /oauth/token` |
| Ingest and witness | checks the RFC 9421 signature and `Content-Digest`, the chain link (`prev` equals the stored head), and the batch number; stores `(device_id, b, n, R_b, H_b, received_at)`; returns a signed receipt; applies bucket deltas; dedupes on `(device_id, b)` (SP 8.2, 8.5) | `POST /api/v1/ingest/batches`, `GET /api/v1/ingest/state`, `GET /api/v1/time` |
| Rollups and live dashboard | additive upserts into `rollups_daily (user, project, day, activity_kind)`; SSE over HTTP/2 with topic ring buffers, 15 s keepalives and a polling fallback (SP 6.4) | `GET /api/v1/stream`, `GET /api/v1/users/me/dashboard` |
| Projects and git | root-commit fingerprint stored as `HMAC(pepper, root_sha)`; public remotes; commit records with `server_first_seen_at`; GitHub App push webhooks; forge verification of public repos on GitHub, GitLab and Forgejo (SP 4) | `POST /api/v1/forge/github/webhook` |
| Profiles | public aggregates with daily totals only, and project names only for public repos (TP B7, SP 5.2) | `GET /@:username`, `GET /@:username/:project` |
| Submissions and evidence | freezes a window; takes the metadata reveal; checks roots and chain; re-scores; draws the challenge; checks revealed content against commitments; runs the public checks; asks the sentinel for risk; draws the audit; holds, approves or revokes (6.5-6.9) | `POST /api/v1/submissions`, `POST /api/v1/submissions/:id/reveal`, `GET` and `POST /api/v1/submissions/:id/challenge`, `GET /api/v1/submissions/:id/bundle` |
| Review | `/review/...` pages behind HCA sign-in with a reviewer role from a pairtime allowlist, since a community HCA app can't read `groups` (SP 3.1, 10.3); the evidence access log | `GET /review/submissions/:id`, `POST /api/v1/submissions/:id/review` |
| Program API | OAuth clients with `profile:read`, `email:read`, `hours:read`, `submissions:read` and `submissions:write`; Robert-style signed webhooks; handbook justification; Hackatime-compatible reads for connected users only (SP 10) | `GET /api/v1/submissions/:id/justification`, `GET /api/v1/users/:id/stats`, `GET /api/v1/authenticated/me` |
| Publisher | once a day, a Merkle root over every `(device_id, b, H_b, received_at)` received that UTC day, committed to a public repository (TP B6) | |
| Privacy hub | `POST /api/privacy/delete` and `POST /api/privacy/export` with Hack Club's ES256 JWT contract (TP B3) | |
| `sentinel` (private, separate process) | tier-2 flags and the risk score; called over a Unix socket with a narrow JSON protocol in `pkg/sentinelapi`, never linked into `pairtimed`, the way Ari keeps `private/` out of its public image (TP A8 #9, HC 3.6) | `POST /score` on the socket |

### 3.7 Data flows

A logged minute. An adapter emits an event. The ledger writer assigns the next `seq`, computes the leaf (6.2) and appends it. Every 60 seconds, if there are new events, the writer seals a batch with Merkle root `R_b` and chain head `H_b`. The scorer turns slots into minute buckets (SM 5.1). The uploader sends the sealed batch roots and any new or revised buckets in one signed request. The server checks the chain link, stores the root with `received_at`, signs a receipt, applies the bucket deltas to `rollups_daily`, and pushes them to open dashboards over SSE. Content never moves.

A commit. The `.git` watcher sees `logs/HEAD` grow. The agent reads the new commit's SHA, parents, times and `git diff --shortstat`, writes a `git.change` event and uploads a commit record with `observed_at`. The server stamps `server_first_seen_at`. If the repo is public or has the GitHub App, the push webhook brings GitHub's timestamp, and the server checks the SHA and diffstat against the forge (SP 4.3). Each commit now carries three clocks, two of which the teen doesn't control.

A submission. The program or the user creates a submission for a set of projects and a window. The client builds the metadata reveal for every batch that overlaps the window (6.5). The server rebuilds each `R_b` and `H_b`, compares them with what it stored at the time, re-scores the window, and compares the result with the buckets it received live. Only then does it draw the content challenge. The client redacts the challenged items, shows them to the user and sends the kept tokens with their salts. The server checks them against the commitments, runs the consistency checks, asks the sentinel for a risk score, draws the audit and opens the review. Webhooks tell the program about each state change.

A day. At 00:10 UTC the publisher builds the day tree and commits `roots/YYYY/MM/DD.json` (day, leaf count, root, previous day's root, server signature) to the public `pairtime-witness` repository. GitHub's commit time is a clock the pairtime operator can't set.

### 3.8 What crosses the network

Everything that leaves a teen's machine is in this table.

| Flow | When | Fields | Size |
|---|---|---|---|
| Sign-in | once per device | the device-code flow; the device's Ed25519 public key | |
| Ingest | every 60 s while a project context is live, and at once on lock, suspend, and prompt or stop hooks (coalesced within 10 s) | batch roots `{b, n, R_b, H_b, prev}`; minute buckets `{minute, device, slots[4]{p, cr, why, kind, ev}, credited_s, uncredited{code: s}, agent_running_s, state, witness, rev}`, which is SM 5.1 without `anchors` (5.2); commit records `{sha, parents, project, observed_at, author_time, committer_time, files_changed, insertions, deletions, credited_seconds_since_parent, rewrites[]}`; labels `{client build id, scoring version, params hash, harness names and versions, typing module on or off}`; and the request metadata any server sees (IP, JA4) | about 0.5 KB gzipped |
| Project registration | first sighting of a repo | the root commit set, stored only as an HMAC; a normalized remote only for public repos or projects the user publishes (SP 4.2) | |
| Metadata reveal | at submission, for the submitted projects and window | per event in each overlapping batch, `{hdr, body hash}`; the full `body` only for the submitted projects' events and for presence, power and focus events (6.5). No text | 0.5-1 MB per 10 hours, at roughly 650 events per active hour (estimate) |
| Content reveal | at submission, after the challenge draw | for each challenged turn, the kept tokens of the redacted prompt and of the agent message it answered, their salts, and the leaf hashes and type markers of removed spans | 20-60 KB per submission (estimate) |
| Optional extras | at submission, only if the user adds them | a note, a demo link, opt-in sampled screen frames (6.8) | |
| Updates | at most daily | a signed manifest and binary from GitHub Releases (CP 4.6) | |

Never sent, on any path: file paths and names, window titles, process names other than harness names, prompt or output text outside the challenged sample, code, diffs, key identities, per-keystroke timings, hostnames, OS usernames. The server's ingest schema rejects free-text fields with a 422, so a buggy or modified client can't leak them by accident either (SP 8.2).

### 3.9 Languages and repo layout

Go 1.27 for both binaries, pure Go with `CGO_ENABLED=0` (CP 1.6); macOS release builds stay on Go 1.26 until February 2027 so that Monterey Macs keep working (CP 1.5). Svelte 5 with TypeScript for the server UI and the local dashboard, built by Vite and embedded with `embed`, with API types generated from OpenAPI (SP 7.3). SQL that runs on SQLite and Postgres, tested against both in CI (SP 7.4). Plugins can be written in any language (4.3).

```text
cmd/pairtime/          client: agent, hook, track, submit, verify, install, doctor, dashboard
cmd/pairtimed/         server
pkg/event/             normalized event schema, deterministic CBOR, exported JSON schema
pkg/ledger/            leaves, batches, chain, receipts, reveal proofs, day trees
pkg/score/             the SM rulebook and its parameter table
pkg/check/             the public consistency checks (6.4)
pkg/redact/            known-value, secret and PII span removal (TP B4)
pkg/bundle/            evidence bundle format, manifest signing, offline verification
pkg/sentinelapi/       the socket protocol to the private sentinel
internal/capture/      hook/*, log/*, otel, wakatime, os/{windows,darwin,linux}, git, typing, inbox
internal/agent/        scheduler, uploader, submit engine, local HTTP and socket
internal/server/       ingest, witness, rollup, sse, auth, projects, forge, submissions, review,
                       programapi, hackatimecompat, privacyhub, publisher
web/app/               server UI: profiles, dashboard, review
web/local/             local dashboard
agents/                AGENTS.md snippet, skill and rules files for `pairtime track`
clients/               PairtimeService clients for program apps, in Ruby and TypeScript
plugins/               plugin SDK docs, inbox format, an example plugin
testdata/              harness transcripts per version, the SM section 6 timelines, red-team sessions, golden outputs
deploy/                compose.yml, litestream.yml, cloudflared config
.github/workflows/     ci, e2e, release, restore-drill
```

The sentinel's code lives in a private repository and talks to `pairtimed` only through `pkg/sentinelapi`. Whether that split is acceptable under R1 is TP Open question 7, and section 11 covers what happens if it isn't.

## 4. Integrations

### 4.1 The v1 harness set

The order is HS 16's: hooks first, log readers second, AGENTS.md last (HS 17.1). What this design adds is the third column. For every harness I want at least two records written by different code paths, so a fabricated or replayed log has to agree with a hook stream the server witnessed at the time (6.4).

| Harness | Primary capture (installed by `pairtime install`) | Second record, for consistency checks | Automation labels read (HS 12.1) |
|---|---|---|---|
| Claude Code (CLI, VS Code extension, Desktop Code tab) | Exec-form `command` hooks in `~/.claude/settings.json`: `SessionStart`, `UserPromptSubmit`, `Stop`, `StopFailure`, `Notification`, `PermissionRequest`, `PostToolUse` (matcher `Edit\|Write\|Bash`), `SubagentStart`, `SubagentStop`, `SessionEnd`. Exec form because `SessionStart` rejects `http` and Cursor's loader runs only `command` (HS 16) | Transcript tailer on `~/.claude/projects` (HS 3.2); OTLP receiver for `active_time{type=user\|cli}` through the settings `env` block (HS 16 item 4) | `app.entrypoint`, `CLAUDE_CODE_CHILD_SESSION`, `Stop.session_crons`, `stop_hook_active`, `isSidechain`, `agent_id` |
| Cursor (IDE, `agent` CLI) | `~/.cursor/hooks.json`: `beforeSubmitPrompt`, `afterTabFileEdit`, `afterFileEdit`, `afterAgentResponse`, `stop`, `sessionStart`, `sessionEnd` (HS 16) | WakaTime heartbeats from the editor; the `state.vscdb` reader is v1.5 because it is heavy on a spinning disk (CP 3.2) | `is_background_agent`, `stop.loop_count` |
| GitHub Copilot (VS Code agent mode, CLI) | `~/.copilot/hooks/pairtime.json`, camelCase events, which carry timestamps (HS 16) | `events.jsonl` and `chatSessions/` readers (HS 15) | autopilot, `stop_hook_active`, subagent `agentId` |
| OpenAI Codex (CLI, IDE extension) | `~/.codex/hooks.json`, plus a first-run notice asking the user to trust the hook in `/hooks`; until then the rollout reader is primary (HS 16) | `~/.codex/sessions/**/rollout-*.jsonl`, an open-source format, `.jsonl.zst` accepted (HS 16 item 5) | `session_meta.source`, `originator`, `thread_source`, subagent depth |
| Gemini CLI | hooks in `~/.gemini/settings.json`, with `telemetry.logPrompts=false` if OTel is on (HS 16) | `~/.gemini/tmp/*/chats` and `logs.json` | non-TTY runs, `-p` |
| Devin Desktop and Devin CLI (formerly Windsurf) | reads the Claude-format hooks in `~/.claude/settings.json`; events are deduplicated by harness (HS 15, HS 16) | `~/.local/share/devin/transcripts` | |
| Cline, Kilo, OpenCode, Zed agent, Warp agent, JetBrains AI Assistant and Junie, Amp, Kiro, Trae, Goose, Continue | AGENTS.md plus `pairtime track` (4.4), or a plugin (4.3) | log readers where one exists: Zed `threads.db`, Goose `sessions.db`, Cline `tasks/`, Continue `dev_data` (HS 15) | per plugin |
| Browser chat and app builders (ChatGPT, Claude.ai, Lovable, Bolt, v0, Replit) | not in v1. The v2 browser extension sees prompt-submit timing and typing versus paste (HS 10, 13.5) | git only: these leave commits and nothing else locally | Lovable commits as `gpt-engineer-app[bot]`, Replit adds `Replit-Commit-Author` (HS 13.3) |

Install details that matter for evidence. The installer merges entries next to other tools' entries in the same files, because git-ai rewrites its own agent hooks daily (HS 13.3, 17.13). It writes only user-level config, never anything inside a repo (HS 17.13). Claude Code fires hooks in a new folder only after the user accepts its trust dialog (HS 16), and Codex needs a one-time trust step; until either happens the log reader fills in, and those minutes are marked `source=log` so the review page shows they had no live hook behind them.

### 4.2 Lookback

On first install the agent reads up to 30 days of Claude Code and Gemini CLI transcripts and the older Codex and Copilot logs (HS 17.14, SM 2.8). Those minutes appear on the teen's dashboard on day one (R3) at evidence level C and `witness = backfilled`. They are scored with SM 2.8's lookback rules. Section 5 says how they may be submitted, which is stricter than what SM proposes (D5).

### 4.3 The plugin interface

R7 asks for "a plugin approach where integrations can be built for every use case in the future". There are two ways in, and both end in the same normalized events, so the scorer and every check work unchanged (SM 1).

In-tree adapters implement one Go interface:

```go
type Adapter interface {
    ID() string                                   // "claude_code"
    Detect(ctx context.Context) (Install, bool)   // installed? where? which version?
    Plan(ctx context.Context, in Install) (ConfigDiff, error)  // the exact edits, shown on the consent screen
    Apply(ctx context.Context, d ConfigDiff) error             // idempotent merge into user-level config
    Revert(ctx context.Context, in Install) error              // used by `pairtime uninstall`
    Hosts() []ProcessMatch                        // executables or bundle ids that host its sessions, for focus attribution (SM 3.2)
    Run(ctx context.Context, sink Sink) error     // tail logs, receive hook payloads, emit events
    Fixtures() fs.FS                              // recorded payloads and expected events, per harness version (HS 17.7)
}
```

Out-of-process plugins can be written in any language. A plugin ships a `pairtime-plugin.toml` (`id`, `version`, `harness`, `emits`, `hosts`, `capture`) and writes events as NDJSON to the agent's Unix socket, to `POST http://127.0.0.1:<port>/v1/events` with the bearer token from the user-only port file (CP 6.3), or to `inbox/<plugin-id>.jsonl`. The agent validates each line against the published `event-v1.json` schema, stamps arrival time and `source=plugin`, records the provenance `plugin:<id>@<version>`, and writes it into the ledger like any other event.

A plugin's events are claims, exactly like a hook's. A prompt reported by a plugin opens an anchor only under SM 2.2 rule 6: OS input in the 10 s before it, with focus on a window whose process matches the plugin's `hosts`. So a plugin can't make credit appear by itself, which means pairtime can accept plugins without vetting their authors. To be merged into the main repo, a plugin needs a fixtures directory and a passing conformance job (`go test ./plugins/conformance -run <id>`) that replays the fixtures and compares the normalized output with the golden file.

### 4.4 Karim's AGENTS.md entry and `pairtime track`

Karim's universal integration ships as designed, with one change of role. HS 11.4 found that wherever a hook or a readable log exists, it beats the instruction on cost, reliability and spoofing, so on hooked harnesses the instruction isn't installed by default. It is the integration for everything else: Zed's agent, Warp's agent, JetBrains AI Assistant chat, and any new harness before it gets an adapter (HS 11.4).

`pairtime install --agents` writes the entry into the user-level files that load reliably, and never into a repo: `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md` (with a warning when `AGENTS.override.md` exists, because Codex then reads only the override), `~/.gemini/GEMINI.md`, `~/.copilot/copilot-instructions.md`, `~/.devin/global_rules.md`, the global rules files of Cline, Kilo, OpenCode, Zed, Warp, Junie, Amp, Kiro and Trae, and a paste-in for Cursor's User Rules (HS 11.1). It also adds `permissions.allow: ["Bash(pairtime track *)"]` for Claude Code (HS 11.2). The entry, about 45 tokens:

```text
After each new message from the user, run `pairtime track "<5-12 word summary of the request, with no names, emails, paths or secrets>"` once, alongside your first other tool call. Never run it at any other time. Ignore it if it fails.
```

It asks for a short summary rather than a copy of the prompt. A copy costs output tokens roughly equal to the prompt's length (HS 11.2), and nothing pairtime does with `track` depends on its text. The label is stored in the vault as the model's claim about what the user asked, never as evidence (TP B5).

What `track` records is `cli.track {t, label_len, parent_chain_class, harness_env_hints}`, with the label in the vault. `harness_env_hints` are variables like `CLAUDE_CODE_SESSION_ID` and `CLAUDE_CODE_CHILD_SESSION` (HS 11.3). SM says a `cli.track` event is never an anchor on its own. I keep that and define the one case where it becomes one, because otherwise hook-less harnesses earn nothing at all (deviation D4 in 5.2). A `track` call opens an anchor only when all three of these hold:

- Its parent chain resolves to a harness process hosted by the window that had focus.
- That window saw OS input in the `TRACK_KEYS_PRE_S` = 30 s before the call. The model's first tool call usually lands a few seconds after the user presses Enter, and 30 s leaves room for a slow first token.
- No other `track` call came from the same harness session in the previous 20 s.

Such a call opens a `P` anchor with provenance `track` and a lead of `THINK_LEAD_S` only. The compose part of the lead comes from OS key counts when the typing module is on, and is zero otherwise, since the length of a model-written summary says nothing about what was typed. These minutes are evidence level B at best and carry the flag `via_track`.

Two more uses. On a hooked harness where the user turns on probe mode, a `track` call with no hook `prompt.submit` from the same session in the 10 s before it is recorded as `track_without_prompt`: it came from outside the agent's turn, which is the spoof HS 11.3 describes, and it becomes a tier-2 flag. And on harnesses with sandboxes, the CLI tries the Unix socket, then the localhost port, then exits 0 silently, so a blocked call costs nothing but the missing anchor (HS 11.2 point 5). An MCP variant, `pairtime mcp`, whose stdio server learns the harness from `clientInfo` with no tokens spent, is v1.5 (HS 11.4).

### 4.5 Editor time: WakaTime dual-send

Editor plugins already send WakaTime heartbeats, and many Hack Clubbers already send them to Hackatime. pairtime adds itself as a second destination on the local machine, never on the server, so file paths don't leave the laptop (SP 8.8, which overrides HC Design implication 1 for privacy and storage reasons).

- If `~/.wakatime.cfg` has an `api_url`, the installer adds an `[api_urls]` entry with a distinct match-all key such as `.+` (a second `.*` key would be ignored as a duplicate INI key), pointing at `http://127.0.0.1:<port>/api/v1|<key>`. wakatime-cli sends every heartbeat to the default `api_url` and to every matching pattern, so Hackatime keeps getting everything (SP 8.8).
- If there is no `api_url`, the installer sets it to the local endpoint instead.
- The key must match `^(waka_)?[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`, because a malformed key makes wakatime-cli fail the whole run, which would also break the teen's Hackatime tracking (SP 8.8). The installer validates the line before writing it, and `pairtime doctor` sends a test heartbeat through wakatime-cli and confirms that both destinations received it.
- The local endpoint answers `POST /api/v1/users/current/heartbeats` and `.bulk` and `GET /api/v1/users/current/statusbar/today`. Each heartbeat becomes an `editor.activity` event with its path replaced by a per-device HMAC on arrival. wakatime-cli's `ai coding` category is stored as received and never trusted (HC Design implication 2). Cursor's `afterTabFileEdit` and `afterFileEdit` hooks label AI edits directly, which is the one place v1 can tell a typed edit from an agent edit without an extension (HS 13.1).

Because Hackatime keeps receiving the same heartbeats, a teen keeps their Hackatime stats, and a reviewer with the teen's linked `hackatime_uid` can open Telescreen's Hackatime workbench for the same window as a cross-check (SP 10.3). A pairtime editor extension for typed-versus-AI edits is v2 (HS 16).

### 4.6 Git

Local side, in the client:

- A non-recursive watch on each known repo's `.git`, resolved once with `git rev-parse --git-dir` so worktrees and submodules work (CP 3.2).
- Per commit: SHA, parents, author and committer time, `observed_at`, `--shortstat` against the first parent. Amends and rebases come from the reflog and are sent as `rewrites[{old_sha, new_sha}]`, so credited time moves to the new SHA instead of vanishing or doubling (SP 4.4).
- Project identity from the root commit set, stored on the server only as `HMAC(pepper, root_sha)` (SP 4.2).
- Actor hints from agent trailers, matched on the addresses `noreply@anthropic.com` and `aider@aider.chat`, plus `Replit-Commit-Author` and `gpt-engineer-app[bot]`, and from an `agent.tool` bash call that ran the same git command within 2 s (SM 1.2, HS 13.3). git-ai notes under `refs/notes/ai` are read when present (HS 13.3).
- Git 2.54 config-based `post-commit` hooks are used where available, to react faster. `core.hooksPath` is never set, because it would replace every repo's own hooks (SP 4.1).

Server side:

- A GitHub App with Metadata read and Contents read, installed only on repos the user picks. Contents read is what GitHub requires before it sends `push` webhooks (SP 4.1). The server never fetches file contents, and for private repos it drops the `added`, `removed` and `modified` path arrays of each push payload on receipt, since a webhook receiver gets file names whether it wants them or not (SP 4.1).
- Public repos on GitHub, GitLab and Forgejo are verified with one commit lookup per SHA, which confirms the SHA and the diffstat (SP 4.3). Lookups for a commit that isn't on the forge yet retry with backoff for 24 hours, using a server token at GitHub's 5,000 requests an hour (SP 4.1).
- Each commit gets a forge time: GitHub's push timestamp from the App's webhook where the App is installed, otherwise the time pairtime's server first found the commit on the forge. Only the push timestamp can show that a commit reached the forge before the laptop saw it being made, so CC4's push-order test runs only on App-installed repos (6.4).
- Codeberg OAuth tokens are discarded right after sign-in, because Forgejo tokens can act as the user in every way (SP 4.1).

What git gives the evidence model: three clocks per commit, `credited_seconds_since_parent` for every commit interval, the uncommitted cap at submission (SM 4.3), surviving lines at the submitted commit (TP A4), and the handbook's own rule that "a 20-hour project with only one commit does not follow the guidelines", which becomes the public flag `few_commits` (HC 3.7). In v1 the client computes surviving lines from local `git blame` at submission and reports them as a claim; in v1.5 the server re-checks two random commits per submission against the forge's diff API for public repos.

## 5. Metric

### 5.1 What I adopt

I adopt the struggle-metric spec (SM, marked "draft complete, ready for design review") as the credit rule, whole: human seconds as the unit, one project per second, the three-part test of a human act's window plus OS presence plus project focus (SM 2), the per-session reading bucket (SM 2.4), multi-project attribution on one timeline (SM 3), the four dark-flow detectors and the no-progress half rate (SM 4), reason codes and rollups (SM 5), the parameter table (SM 8), and the calibration plan (SM 7). I adopt it for an evidence reason as much as an accuracy one. Every credited second traces to one event and one public parameter, so a reviewer can recompute any minute by hand and the server can recompute a whole submission (SM 5.2). A credit rule that a third party can re-run is a precondition for R10. A learned model would have been harder to audit.

The spec's own split between public deterministic credit and private tier-2 flags (SM 4.5) is also the split this design needs. Everything that changes the number is public and re-runnable, and everything that only changes who gets looked at is private.

### 5.2 Deviations

Each deviation is numbered so the build plan can refer to it.

| # | SM baseline | This design | Reason |
|---|---|---|---|
| D1 | The tier-1 minute bucket carries `anchors`, the ids of the events whose windows covered it (SM 5.1) | Dropped from the upload. The client keeps them locally | Anchor ids tell the server how many acts happened in each minute of logged-only time, which is behavioral detail it doesn't need. At submission the server recomputes the buckets from the revealed metadata, which is pinned by the batch roots, and compares them with the live buckets (D7), so nothing is lost |
| D2 | A per-event `chain_hash` envelope field, with the head sent every `CHAIN_HEAD_INTERVAL_S` = 300 s (SM 1.1, 8.6) | Leaves made of a header and a nonce-protected body (6.2), a Merkle root per batch, and a chain across batches. Batches are sealed every `BATCH_S` = 60 s while there are new events | A Merkle batch allows the project-scoped reveal in 6.5, where a submission proves one project's events without exposing the others in the same minutes. A linear per-event chain would force revealing everything in the window. Sixty seconds bounds the witness lag of normal operation at one to two minutes instead of five to six, which narrows the room for "fabricate it live, a little late", and it matches the 60 s sync the live dashboard needs anyway (SP 2, PA 5.3). `WITNESS_LAG_S` = 900 s stays as the line between witnessed and backfilled, to tolerate flaky school Wi-Fi |
| D3 | `agent.output` carries counts only: `words`, `code_lines`, `is_final`, `asks_question` (SM 1.2) | The final message of each turn is also stored in the vault, and its leaf carries a content commitment `C` | A challenged turn then reveals the prompt together with the agent message it answered. The reviewer can judge whether the prompt reacts to what the agent said, which is the residual test SM 6.6 leaves for the full puppeteer, and the commitment proves the revealed message is the one that was on screen then, not one written for the review |
| D4 | `cli.track` is "a self-declared claim from the model; never an anchor on its own" (SM 1.2) | A `track` call opens a `P` anchor with a think-only lead when its parent chain resolves to a harness hosted by the focused window and that window saw OS input in the 30 s before the call (4.4). Minutes are level B at best and flagged `via_track` | Without this, hook-less harnesses (Zed, Warp, JetBrains AI chat) earn nothing, and R7 asks that every harness work. The OS-side conditions keep the call from being a free anchor: a script that calls `track` without a person at the keyboard opens nothing |
| D5 | Level-C minutes (backfilled or lookback) are recommended at `min(credited, ai_logged / 3)` (SM 5.4) | Same cap, and level-C minutes can be submitted only inside a commit interval whose closing commit is forge-verified. Otherwise they appear on the dashboard and profile but not in submissions | A forge-verified commit is the only outside evidence that anything happened in that interval at all. TP A6 calls fabricated offline sessions (U6) something that "can be bounded, not verified", and this is the bound |
| D6 | All late time is `backfilled` (SM 5.1) | A third witness label, `offline`: minutes whose batches were sealed on a continuous chain while the ledger recorded failed uploads, then flushed in one catch-up. Minutes flushed within `OFFLINE_FLUSH_S` = 36 h keep their computed evidence level, provided a commit in their interval reached the forge within 24 h of reconnecting. Offline minutes flushed later count only up to `OFFLINE_CAP_S` = 3 h per submission | TP A3 says offline work on school Wi-Fi or a flight should show as "offline, verified on reconnect" rather than being zeroed. The 36-hour window covers a teen with no internet at home who syncs at school the next day, which is the R8 audience and shouldn't be capped. Beyond it, the cap and the push rule bound what a client faking an outage can gain, since nothing witnesses offline time (TP A6 point 1, TP A7 E1)
| D7 | The server re-scores submitted windows and flags a mismatch (SM 7.6) | Mismatched minutes are held: not credited until a reviewer clears them | R9 says a submitted hour should equal an hour of struggle. A minute the server can't reproduce from evidence pinned at the time shouldn't count by default, whatever the cause |
| D8 | Reviewers can only cut, as in Ari's `clampSeconds` (SM Open question 5, HC 3.6) | Reviewers may also restore seconds that pairtime itself discounted (`X.reroll`, `X.rewrite_loop`, `X.same_error`, `X.autopilot`, `X.noprog`), never more than the seconds reported under those codes, with a required reason that HQ spot-checkers see | Dark-flow false positives are pairtime's mistakes (SM Risks 5). The restored seconds already have human acts and presence behind them; a heuristic removed them. This needs Quality and Integrity's agreement before it ships |
| D9 | Evidence levels A, B and C per slot (SM 5.1) | Unchanged in the bucket. Submissions add three bands for the reviewer (6.7): corroborated, witnessed and backfilled | The levels say what the OS could see. The bands say how much of the claim independent records back up, which is what decides how long a reviewer should spend on an hour |
| D10 | Presence sampled every 15 s at all times (SM 2.1) | Sampled only while a project context is live (3.3) | Privacy: pairtime doesn't record general computer use. No credit changes, because every creditable second is a project-focused second or a bracketed lookup within 30 s of one (SM 2.5) |

I don't deviate on credit by platform. SM's Risks 1 and 3 point out that level-B machines, the common 10-year-old Windows laptop among them, are where a mouse jiggler can satisfy presence and leads credit time nobody can see. Lowering credit there would punish the teens R8 is about for what their OS can't report. The evidence answer is to spend review effort there instead: level-B hours get a higher audit weight (6.9), never a lower number.

SM's Open question 2 asks whether pairtime may count keystrokes by default on Windows and X11. This design says no. The typing module is opt-in and off by default, with "No" as the more prominent button, as TP A4 point 3 and TP B2 point 7 recommend. Minutes without it are "typing unknown", credited the same, and sampled more.

### 5.3 New parameters

These join SM 8 and are covered by the same parameter hash on every bucket (SM 7.6).

| Parameter | v1 | Reason |
|---|---|---|
| `BATCH_S` | 60 s | Batch sealing interval while there are new events (D2) |
| `BUCKET_REVISION_MAX_S` | 900 s | A minute bucket can be revised for 15 minutes after its minute, since leads reach back up to 420 s and the reading horizon up to 600 s (SM 2.3, 2.4). After that only `+restored` revisions are accepted, for up to `PROGRESS_RESTORE_S` |
| `TRACK_KEYS_PRE_S` | 30 s | OS input in the host window needed before a `track` call for it to open an anchor (D4) |
| `TRACK_MIN_GAP_S` | 20 s | At most one `track` anchor per harness session in this span (D4) |
| `OFFLINE_FLUSH_S` | 129,600 s | Offline minutes flushed within 36 h keep their credit, given forge corroboration (D6) |
| `OFFLINE_CAP_S` | 10,800 s | Credit per submission from offline minutes flushed later than `OFFLINE_FLUSH_S` (D6) |
| `OFFLINE_PUSH_S` | 86,400 s | Window after reconnecting in which a commit must reach the forge to corroborate offline minutes (D6, TP A7 E1) |
| `CHALLENGE_K` | `max(10, 10% of challengeable turns)`, capped at 30 | Turns revealed per submission (TP B6) |
| `AUDIT_BASE_P` | 0.08 | Published floor on the probability of a live audit (6.9) |

### 5.4 Calibration additions

SM 7's plan stands: a labeled pilot set, minute-level κ, session error, the asymmetric loss and the four baselines. Two additions come from the review page. Every cut and restore on the page records the minutes, the reason code and the reviewer, so each decided submission adds labels without extra work, the way Overwatch verdicts became VACnet's training labels (TP A5). And the review queue is seeded with known-answer submissions, including the SM section 6 timelines and red-team sessions, so each reviewer's accuracy is measured too (TP A5, Oleson et al.). Whether minors' review outcomes may be used to fit parameters at all is a consent question under New York's CDPA, which excludes "research and development" from what needs no consent (TP B2 point 7, TP Open question 15). Until counsel answers, pilot labels come only from participants who opted in for that purpose.

## 6. Trust

### 6.1 Where trust comes from

Under AGPL the client belongs to the attacker, so every byte it sends is a claim (TP A1). TP's table of what the attacker can't control is the design brief: the pairtime server's clock, third-party servers such as GitHub and its push and Actions timestamps, the fact of one human with one attention stream, cross-user data, human reviewers, and rules the attacker can't read (TP A1). Every defense below is built from those parts. The client-side checks still earn their place, because they catch honest automation and lazy cheating for free, but none of them is where trust comes from (TP A5 principle 1).

Four mechanisms carry the weight:

1. Commitments made at the time and witnessed by clocks the teen doesn't control (6.2, 6.3).
2. Agreement between records that different code paths wrote, so a forger has to fake all of them consistently and live (6.4).
3. A random sample of content, drawn after the metadata is pinned and checked against the commitments (6.5).
4. A person, for a risk-weighted fraction of submissions (6.9).

One fact about Hack Club helps a lot here. The handbook requires every submitted project's Code URL to be public, with "multiple commits that reflect the development progress" (HC 3.7). So every submitted project can be forge-verified, and every commit in it can carry a forge timestamp. A design that leans on forge evidence isn't asking anything new of teens.

### 6.2 Commitments made at the time

The construction is TP B6's with four changes: a header that is revealed for every event while the body is revealed only for the submitted project's events; a secret nonce inside each body in place of TP's per-leaf salt; derived scorer inputs committed in the body; and a signed receipt for every batch.

```text
Install:  K_dev   = 32 random bytes, in DPAPI / Keychain / Secret Service
          K_leaf  = HKDF-SHA256(K_dev, info = "pairtime/v1/leaf")
          K_tok   = HKDF-SHA256(K_dev, info = "pairtime/v1/token")
          K_vault = HKDF-SHA256(K_dev, info = "pairtime/v1/vault")

Event i, sequence number s_i:
  hdr_i   = s_i (8 B) || kind (1 B) || project (16 B, zero for presence and power) || session (8 B)
  nonce_i = HMAC(K_leaf, 0x01 || s_i)
  if the event carries text (a prompt, or a turn's final agent message):
      tokens w_1..w_n, split on whitespace with separators kept
      t_ij = SHA-256(0x00 || HMAC(K_tok, s_i || j) || w_j)         salted per-token leaf
      C_i  = MerkleRoot(t_i1 .. t_in)                               content commitment
  body_i  = deterministic CBOR { nonce_i, t, mono, boot, source, harness, harness_version,
                                 the kind's fields (SM 1.2), derived scorer inputs, C_i? }
  L_i     = SHA-256(0x00 || hdr_i || SHA-256(body_i))

Batch b, sealed every BATCH_S = 60 s when there are new events:
  R_b = MerkleRoot(L_i in seq order)          RFC 9162 tree: leaves 0x00, nodes 0x01
  H_b = SHA-256(0x03 || H_(b-1) || R_b || n_b || b)
  send {device_id, b, n_b, R_b, H_b, prev = H_(b-1)}

Server:
  store (device_id, b, n_b, R_b, H_b, received_at)
  return receipt_b = Ed25519_sign(server_key, device_id || b || H_b || received_at)
```

Why each piece is there:

- The nonce. At reveal time the server sees `SHA-256(body_i)` for events whose bodies stay hidden. Many bodies would be guessable without a secret in them: a presence sample, or a tool call with a common exit code. The nonce makes every hidden body unguessable, which is the job TP B6's per-leaf salt does.
- The header. `kind` and `project` are visible for every event in a revealed batch, so the server can demand the full body of every presence, power and focus event. Without that, a modified client could hide a screen-lock event by labeling it with another project. `session` lets the server check that one harness session stays in one project (6.4, CC9). The cost is that the server learns how many events of each kind other projects had in the submitted minutes. The tier-1 buckets already tell it which project each slot went to, so the extra leak is small, and I accept it.
- Per-token commitments. The teen can remove a span, a friend's name or an email, and still prove the rest is exactly what was typed. The reviewer sees where removed spans are, what type they were and how many tokens each covered (TP B4, B6).
- Scorer inputs that depend on content. Some scorer inputs come from tier-0 content the server never sees: near-duplicate tests on prompts, error signatures, and hunk overlap between turns (SM 4.2). The body carries keyed forms of them: a 64-value MinHash sketch of the normalized prompt, computed over shingles hashed with a device key; the error signature as an HMAC under a device key; and each edit's hunks as `(file HMAC, start line, end line)`. Keyed values can be compared with each other but can't be tested against guesses, so the server can recompute every re-roll, fix-it and rewrite discount in a submitted window without seeing any text. The few inputs that can't be keyed, such as `prompt_class` and `asks_question`, are committed as computed values. For challenged turns the client also reveals the per-shingle HMACs, and the server checks each sketch and computed value against the revealed text (CC8).
- The chain and `received_at`. The client can't add, drop, reorder or edit events after a batch is uploaded. Anything first seen late is backfilled (TP A3 T8).
- The receipt. Receipts make witnessing two-sided. The server can't later deny or re-date a head the teen holds a signed receipt for, and the teen can't claim an earlier time than `received_at`.

Cost, for R8 and R11: a few SHA-256 and HMAC operations per event on the client, one Merkle root a minute, and about 150 bytes of root data per upload (TP B6).

### 6.3 Clocks the teen doesn't control

Each credited minute gets one witness label, from its batch's `received_at` minus the event's corrected time. Event times are re-anchored from the monotonic clock within one boot, and events more than 5 minutes in the future are rejected (SP 8.4).

| Label | Rule | Treatment at submission |
|---|---|---|
| `witnessed` | Batch received within `WITNESS_LAG_S` = 900 s | Normal |
| `offline` | Late, but sealed on a continuous chain while the ledger recorded failed uploads, then flushed in one catch-up (D6) | Credited as computed when flushed within 36 h and a commit in its interval reached the forge within 24 h of reconnecting; flushed later, at most 3 h per submission |
| `backfilled` | Everything else late, and all lookback | Level C: submittable only inside a forge-verified commit interval, at most at the handbook third (D5) |

Commits carry three clocks. `observed_at` is the laptop's. `server_first_seen_at` is pairtime's. The forge time is GitHub's push timestamp where the GitHub App is installed, and otherwise the time pairtime's server first found the commit on the forge (4.6). A commit whose `author_time` is far older than its `server_first_seen_at` is `backdated`, which the handbook treats as "A commit history that was manufactured after the fact" (HC 3.7).

The fourth clock covers the server. Once a day the publisher commits the root of a Merkle tree over every `(device_id, b, H_b, received_at)` it received that UTC day to the public `pairtime-witness` repository, chained to the previous day's root and signed (TP B6, "Not trusting the server either"). At submission the server stores each batch head's inclusion proof in that day's tree in the bundle. An auditor can then check that the server didn't insert or re-date roots after the fact, using GitHub's commit history as the outside timestamp. The cost is one small commit a day. Witness rows are kept per device-minute for 180 days, about 0.5 GB a month (SP 8.5), so day trees can be rebuilt for any submission made within that time.

### 6.4 Cross-source consistency checks

HS 17.8 calls cross-source consistency "the anti-fraud core": every agent-side record can be produced by a script and every OS-side signal can be faked with injected input, but they fail in different ways, and a forger has to fake all of them consistently at once (HS 14). This design turns that idea into named checks with fixed inputs. The public ones live in `pkg/check`. The client runs them before a submission, so an honest teen sees a problem before a reviewer does, and the server runs them again on the revealed metadata.

| Check | Compares | Catches | Needs |
|---|---|---|---|
| CC1 hook vs log | each hook `prompt.submit` with the transcript's user entry for the same turn: order, time within 5 s, `len_chars` after normalization; and the reverse | fabricated or imported transcripts (T8); a script calling `pairtime hook` with invented payloads, which leaves no transcript | a harness with both records: Claude Code, Codex, Copilot, Gemini CLI |
| CC2 prompt vs OS | share of human-origin prompts with OS input in the 10 s before submit and focus on the host window (SM 2.2 rule 6) | pty and `tmux send-keys` drivers (T3); LLM-controlled terminals | presence and focus |
| CC3 edits vs git | lines changed by agent edit tools plus hand-edit line deltas in a commit interval, against the commit's `--shortstat` | code that appears with no recorded edits: copied projects (T16a), reconstruction (T16b), edits made by a process pairtime can't see. Flag `unexplained_lines` when more than half of a commit's lines, and more than 200 lines, have no recorded edit | edit events and a commit |
| CC4 commit clocks | `observed_at` before `server_first_seen_at` before `observed_at` + 15 min; on App-installed repos, push time not earlier than `observed_at` minus 60 s; forge SHA and diffstat match | backdated history; commits pushed before pairtime saw them made, which means they were made elsewhere | a forge-verified commit |
| CC5 one attention stream | no slot credited twice across devices or projects (SM 3.4) | second machines (T11a), account sharing (T17a), parallel-agent splitting (T12) | tier-1 buckets, all devices |
| CC6 live vs re-scored | the server's re-score of the window from revealed metadata, at the frozen scoring version and parameter hash, against the buckets received live (SM 7.6, D7) | modified clients (T9); scorer bugs | revealed metadata |
| CC7 chain integrity | revealed leaves reproduce every `R_b` and `H_b`; no gap in `b` or in `seq` across the window | hidden, dropped or reordered events | revealed batches |
| CC8 content-derived inputs | for challenged turns, each keyed sketch is recomputed from the revealed text and its per-shingle HMACs, and `prompt_class`, `asks_question` and the typed-length test are recomputed and compared with the committed values | a client that commits false sketches or classes to dodge dark-flow discounts. If it falsifies the 10% of prompts that are re-rolls, a 10-turn sample catches at least one with probability 0.65 | the content sample |
| CC9 session integrity | one session stays in one project; every `agent.*` event's turn has a prompt or an automated origin in the same session; the parent-chain class doesn't change mid-session | events moved between projects to hide them; orphan tool events | revealed metadata |
| CC10 editor vs focus | editor heartbeats that open anchors arrive while an editor window of that project has focus | heartbeats injected by a script calling wakatime-cli | editor events and focus |
| CC11 typing (module on) | key-down counts in the lead window against the prompt's typed length; injected share | pasted or injected prompts passed off as typed (T5) | the opt-in typing module |

Each check reports pass, fail or not computable, with counts. A check that can't be computed for a harness, such as CC1 on Cursor without its database reader, is reported as such and never counts as a failure. Failures change review priority and the evidence band (6.7), never the credited number, except CC6 and CC7, whose failures hold the affected minutes under D7.

Tier-2 signals run in the private sentinel, on submitted windows only (TP B2 point 6): regular prompt timing and burstiness, periodic input, cadence scores, Claude Code's `active_time{type=user}` against pairtime's credit for the same session, cross-user duplicates and graph clusters, JA4 against the claimed client build, distribution outliers, marathon sessions (TP A4, SM 8.7). Their thresholds are never published (TP A5).

### 6.5 Commit, then reveal

A submission covers a set of projects `P` and a window `W`.

1. Freeze. The server records `P`, `W`, the scoring version and parameter hash, and a fresh random value `r`, and sends the client `SHA-256(r)`. The web flow asks for a fresh HCA login (`prompt=login`, SP 3.1).
2. Metadata reveal. For every batch that overlaps `W`, the client sends, for every event in it, `hdr_i` and `SHA-256(body_i)`, plus the full `body_i` when the header's project is in `P` or its kind is presence, power or focus. The server rebuilds every leaf, root and head and compares them with what it stored at the time (CC7), computes each event's witness lag, re-scores the window and compares with the live buckets (CC6), and runs the other metadata checks.
3. Challenge. Only after step 2 passes does the server reveal `r`. The challenge seed is `SHA-256(r || SHA-256(metadata reveal))`. From it the server draws `CHALLENGE_K` turns, `max(10, 10% of challengeable turns)` capped at 30 (TP B6), or all of them when there are fewer, from the human-origin prompts in `P` and `W` that opened credited anchors, plus two commits for the interview list. A submission made of hand coding with no prompts has nothing to draw; its evidence is editor events, focus and git (CC3, CC10). The server committed to `r` before it saw the reveal, and the client sent the reveal before it learned `r`, so neither side can steer the draw, and an auditor can recompute it from the bundle. The client also can't grind on the window's edges, because it can't evaluate the draw without `r`.
4. Content reveal. For each challenged turn, the client reveals the prompt and the agent's final message from the turn before it, the message the prompt answered (D3). It runs TP B4's pipeline first: the user's own known values (OS account name, home directory, hostname, `git config` name and email, GitHub and HCA usernames, private repo and folder names, the user's deny list), then gitleaks' rules, then validated PII patterns ported from Presidio. It shows the user exactly what the reviewer will see and lets them remove more. It then sends the kept tokens with their salts, and the leaf hashes and type markers of removed spans. The server recomputes each `C_i` and checks it against the body's commitment.
5. Withholding. The user may withhold any challenged item. The minutes its anchor supports, meaning its lead and the reading it opened, aren't credited (TP A7 E6, TP B6 step 4).

If a fraction `f` of a submission's prompts are fabricated, re-rolled or LLM-written, a sample of `k` contains at least one with probability `1 − (1 − f)^k`, which is 0.97 for `f` = 0.3 and `k` = 10 (TP B6). The reveal catches later editing and content-free fabrication mechanically. Judging whether a real prompt shows real struggle is the reviewer's job, and the agent message beside it is what makes that judgment fast.

Commitments prove that a record existed at a time and hasn't changed since, not that it describes something real (TP B6). A modified client fabricating events live commits to its fabrications on time. What it can't do cheaply is keep those fabrications consistent with a transcript, OS input in the right window, editor events, and commits that exist on GitHub with the right diffstat and push time (6.4). The cheapest way to produce all of that is to run a real agent on a real project, which leaves only the full puppeteer (6.12).

### 6.6 The evidence bundle

Every submission has one bundle, `GET /api/v1/submissions/:id/bundle`, a tar archive that verifies itself (SP 10.4):

| File | Contents |
|---|---|
| `manifest.json` | submission and program ids, `external_id`, user ids (`pairtime_id`, `hca_sub`, `slack_id`), projects, window, scoring version, parameter hash, client build ids, totals, bands, rows, check summary, the challenge value `r` and seed, and the SHA-256 of every other file. Signed with the server's Ed25519 key, published with key ids and validity dates at `/.well-known/pairtime-keys.json` |
| `batches.cbor` | every overlapping batch: `b`, `n`, `R_b`, `H_b`, `prev`, `received_at`, the receipt, and the inclusion proof of `H_b` in that day's published tree |
| `events.cbor` | the header and body hash of every event in those batches; full bodies for the revealed ones |
| `buckets.json` | the buckets as received live, with `received_at` and revision history, next to the server's re-score |
| `challenge.json` | the drawn turns; their kept tokens and salts; removed-span hashes and type markers |
| `commits.json` | commit records with their three clocks and the forge verification responses' digests |
| `checks.json` | CC1-CC11 results per row |
| `review.json` | per-row cuts and restores with reason codes, reviewer ids as opaque ids, the outcome; added once reviewed |
| `flags.json` | tier-2 flag codes and reason categories. Only in the reviewer variant: the teen's own copy leaves it out, so thresholds can't be read back from it, while the teen still sees which category of evidence was missing (TP A6, due process) |

`pairtime verify <bundle>` runs offline. It checks the manifest signature against the pinned key, rebuilds every leaf, root and head, checks each inclusion proof against the day root in the public repository (a local clone or a fetched file), recomputes the content commitments of the revealed turns, recomputes the challenge draw from `r`, and re-runs `pkg/score` and `pkg/check` at the bundle's scoring version. Every released scoring version is compiled into the verifier, because SM 7.6 requires any version to be rebuildable from its tag. It prints the totals and any differences, and exits 0 when everything reproduces, 1 when numbers differ, and 2 when a signature or proof fails. Fraud Squad never has to trust pairtime's server or UI to check that the numbers follow from the evidence.

Bundles stay downloadable until the spot-check window closes, because a project whose "Required evidence ... cannot be located" is removed from the Unified DB and the program fined (HC 3.7). The window isn't published (HC Open question 4); until HQ sets it, Tier 2 evidence is kept 12 months after the decision (TP B8). SP 10.4 suggested two years; I chose the shorter period because the evidence belongs to minors. The bundle's SHA-256 goes into the justification text, so a spot-checker can tell that the bundle they open is the one the reviewer read.

### 6.7 The review page

The page exists to make one decision fast: approve this hour, or cut it, with a reason a spot-checker will accept. Hack Club reviewers already work this way. Ari settles hours row by row (HC 3.6), and the handbook says "When in doubt, deflate" (HC 3.7). So the page is built around rows, and pairtime's rows are commit intervals (SM 5.3), which map one-to-one onto Ari's per-commit rows.

Top to bottom:

1. Headline. Claimed hours from the program, credited hours from pairtime, and three bands, computed only from public inputs so the teen's copy of the bundle can show them too:
   - Corroborated minutes are witnessed; their anchor was seen by two records written by different code paths (hook and transcript, hook and OTel, heartbeat plus focus plus input, commit plus forge); their row passes CC6, CC7 and CC9; and they are level A, or level B in a row that also has editor acts passing CC10. The level-B condition exists because on a Windows laptop without the typing module the OS can't tell a keyboard from a mouse jiggler, so a level-B prompt alone could be a jiggler plus a pty driver (SM 2.2, SM Risks 1).
   - Witnessed minutes are witnessed, or offline under D6's conditions, are level A or B, and miss one of the corroborated conditions.
   - Backfilled minutes are level C, or late without meeting D6's offline conditions, and are shown with the handbook-third cap applied.

   Then the handbook comparison from SM 5.4 (`logged_s`, `ai_logged_s`, `handbook_s`, `ai_ratio`), the held minutes, and the audit status.
2. Must-view rows. Every row with a failed check or a tier-2 flag, plus one clean row drawn at random. "Approve remaining rows" unlocks only after these are opened, so clean submissions still get looked at and rubber-stamping shows up in reviewer statistics.
3. Hour strips. Each row expands into one 60-cell strip per hour. Lanes show activity kind, evidence level and witness label. Markers show prompts (typed, pasted or automated, drawn differently), permission answers, interrupts, test runs passing or failing, and commits. Uncredited minutes are hatched, with their reason code on hover (SM 5.2).
4. Revealed turns, inline at their markers: the agent message on the left, the prompt on the right, removed spans as typed markers, and "matches commitment, witnessed 14:03:12" under each.
5. Commits: three clocks, diffstat, credited time since the parent, surviving lines (a client claim in v1), and a forge link.
6. Checks: CC1-CC11 per row with counts, such as "prompts with keyboard input before them: 212 of 214".
7. Flags: tier-2 codes and categories, never thresholds.
8. History: the user's earlier submissions and their approved-to-credited ratios (SP 10.3).

Keys: `j` and `k` move between rows, `a` approves a row, `d` starts a cut (drag across minutes or type a number), `r` restores discounted seconds under D8, `f` flags, `o` opens the evidence under the cursor. A cut needs a reason code (`not_struggle`, `off_task`, `one_shot`, `unexplained_code`, `insufficient_evidence`, `padding`, `other` with text). Ari makes `deflation_reason` and `technical_features` mandatory to approve or deflate (HC 3.6), so the page drafts the deflation text from the reason codes and asks the reviewer to write the technical-features line. A program can set a policy preset, for example "corroborated rows with no flags are pre-approved, witnessed rows need a look, backfilled rows are capped at the third", and the justification text names the policy used.

Targets, to measure in the pilot (SM 7.5): median under 5 minutes for a 10-hour submission with no flags, under 15 with flags, with deep audits counted separately. Program reviewers see their own program's submissions; Quality and Integrity, Fraud Squad and spot-checkers see all of them. Every evidence view is logged, and the teen sees the log (SP 10.3).

### 6.8 Opt-in sampled screen evidence (v2)

Fraud Squad already trusts screen video and editor history more than transcripts (TP A2), and R10's own example is timelapses that stay local except for submitted hours. This module makes that example cheaper and harder to fake. It is off by default and asked for once, with "No" the more prominent answer (TP B2 point 7).

- While a project context is live and focus is on a project's terminal, editor or agent window, the agent captures one downscaled frame every 30 s. It never captures while a browser, chat app or anything else has focus.
- Each frame becomes a `screen.frame` event whose body commits to the frame's SHA-256. The frame itself goes to the vault, encrypted, for 30 days.
- At submission the server draws 20 credited minutes, weighted toward level-B and flagged rows, and the client reveals the two frames of each. The user sees every frame before it leaves and can withhold one, which uncredits its minute.
- The reviewer sees the frames on the hour strip at their minutes.

Compared with uploading a timelapse, this reveals 20 minutes out of 10 hours, uploads a few MB per submission instead of about 66 MB per recorded hour at Lookout's rate (HC 4.2), and proves each frame existed at its minute, because its hash was witnessed within a minute or two. Capture cost on an old laptop and the per-OS capture APIs (screen-recording permission on macOS, portal consent on Wayland) are unmeasured here and are the first thing the v2 work package checks.

### 6.9 Risk-weighted audits and the interview

Each submission gets a deep-audit probability:

```text
p = min(0.6, max(AUDIT_BASE_P, 1.1 * h / (0.6 * h + 120)) + risk)
```

`h` is credited hours, `AUDIT_BASE_P` is 0.08, and `risk` comes from the sentinel. Everything but the risk weights is published (TP A5 principle 3). The middle term is TP A6's break-even rule with a 10% margin. Fraud stops paying when `p·q > g/(g + L)`. With TP's example inputs, a live interview catches a puppeteer with `q` ≈ 0.6, a Hack Club-wide ban costs `L` ≈ $1,000, and a fake hour is worth $5, so `g` = 5h and the break-even is `p* = h / (0.6h + 120)`: 0.079 for a 10-hour claim and 0.278 for a 40-hour claim, as TP computes. The formula gives 0.087 and 0.306. The inputs are guesses that audit outcomes will replace (TP A6), and when they change, the published formula changes with them. The risk inputs are named publicly and weighted privately: level-B share, `via_track` share, held minutes, failed checks, tier-2 flags, first submission, offline and backfilled share, account age and earlier outcomes.

The audit itself is TP A6's secure lane: a 15-minute call in which the teen walks through the two commits the server drew in 6.5, explains one design decision, and makes one small change live on a screen share. A teen who built the project with an agent can do this, and someone who never touched it usually can't (TP A6). Who may hold a call with a 15-year-old, and over what channel, is for Hack Club's safeguarding rules (TP Open question 4). Calls produce notes, never recordings (TP B8). Outcomes are pass, partial (a deflation with a reason) and fail. A confirmed fraud verdict goes to Fraud Squad through Robert, so the penalty is Hack Club-wide (TP A6 point 5).

Hours stay revocable through the spot-check window. A revocation fires `submission.revoked` to the program, and whether prizes are clawed back, as Stardance did with stardust, is the program's call (TP A6 point 2, TP Open question 9). No detector bans anyone. A flag changes the evidence path, and the teen sees which category of evidence was missing and can appeal to a person (TP A6).

Reviewer cost at the acceptance target, roughly 1,000 submissions: an overall audit rate near 15% at 15 minutes each is 37.5 hours (TP A6), and about 5 minutes for each of the other 850 is about 71 hours. That is around 110 reviewer-hours for 10,000 approved hours, all estimates to replace with pilot numbers.

### 6.10 Reviewer integrity

From TP A3 T18 and A8 #8:

- Random assignment from a pool, never a claim queue, so friends can't pick each other's submissions.
- Conflict rules: no reviewing someone from your own club, by self-declaration, or someone who shares a GitHub repository or organization with you.
- Seeded known-answer submissions, about 2% of the queue, built from the SM section 6 timelines and the red-team sessions, with each reviewer's accuracy on them tracked (TP A5).
- Per-reviewer statistics for HQ: approval rate, hours approved per review, seconds per decision, accuracy on seeded cases, and spot-check outcomes on their decisions.
- The reviewed teen can see the access log.

### 6.11 Defenses adopted from the trust doc

TP A8 ranks ten v1 defenses. This design adopts all ten and pushes four of them further:

| TP rank | Defense | Here | Ships in |
|---|---|---|---|
| 1 | Credit only human-evidenced minutes | SM as adopted (5.1) | M1 |
| 2 | Witnessing: hash-chained log, heads to the server, server time as the clock | Batches every 60 s instead of 5 minutes, signed receipts, the public day root (6.2, 6.3) | M1, M3 |
| 3 | Commits as anchors | Three clocks per commit, CC3 and CC4, `few_commits` (4.6, 6.4) | M2, M3 |
| 4 | Public tier-1 automation labels | SM 2.2 rules 1-5 (4.1) | M2 |
| 5 | Opt-in typing evidence | The `typing` adapter and CC11, opt-in, per-slot aggregates only (3.3) | M5 |
| 6 | Server risk score | The sentinel, on submitted windows only (6.4) | M4 |
| 7 | Review protocol: deflate by default, random reveal, risk-weighted interviews, pay after review, revocable hours, penalties shared with Fraud Squad | The challenge with a draw neither side can steer, turns revealed with their agent context, the published audit formula (6.5, 6.9) | M4 |
| 8 | Reviewer integrity | 6.10 | M4 |
| 9 | Open core for detection | The sentinel as a separate process (3.6) | M4 |
| 10 | Local re-roll and dark-flow detection | SM 4.2, with the derived inputs committed (6.2) | M1 |

From TP's deferred list: per-user typing profiles (T17b, T21) and VM labels (T10) stay deferred to v2, as labels only, and VM labels must read Hyper-V's root-partition bits so Windows with VBS isn't called a VM (TP A3 T10). The input-device inventory (T6) ships in v2 as a single "new input device: yes or no" bit, never the device list (TP A3). Code similarity moves up to v1.5: Dolos (MIT) runs as a separate review-time service on public repos against the Unified DB's Code URLs, for the copy attack T16a (TP A4). Timelapse becomes the sampled screen evidence of 6.8.

### 6.12 Attacks I accept, and how review covers them

My angle is hours a reviewer can trust while an LLM drives the agent, so here is that attacker's ladder and what each rung earns under this design.

| Step | What the attacker does | What it earns | What catches it |
|---|---|---|---|
| 1 | An LLM writes prompts and a script runs `claude -p`, `codex exec` or an Agent SDK loop | 0 | headless entrypoint and parent chain, so no prompt is human-origin (SM 2.2 rule 4, SM 6.6) |
| 2 | The LLM drives the interactive TUI through `tmux send-keys`, `expect` or a pty | 0 | no OS input before submit, origin `no_input` (SM 2.2 rule 8, CC2) |
| 2b | Step 2 plus a mouse jiggler, on a machine where the OS reports only "some input happened" (Windows or X11 without the typing module, all of Wayland) | credited at level B, if undetected | the jiggler satisfies presence, so this is SM's own soft spot (SM Risks 1). The minutes can't reach the corroborated band without editor acts (6.7); `periodic_input` and `regular_timing` flags; no editor or shell acts in the row; the higher audit weight of level-B hours; the revealed sample |
| 3 | The LLM writes a whole transcript and imports it, or edits real logs | 0 witnessed | no hook events witnessed at the time (CC1); backfilled, so only inside forge-verified commit intervals and at most at the handbook third (D5) |
| 4 | A modified client fabricates every event live | whatever survives review | must agree with a transcript, OS input in the right window, editor events and real commits on GitHub with matching diffstats and forge times (CC1-CC10); a server re-score (CC6); 10 or more revealed turns that read as real (6.5); the audit (6.9) |
| 5 | A real agent builds a real project while the LLM types prompts through AutoHotkey, `pyautogui` or a computer-use API | credited, if undetected | on Windows and X11 with the typing module on, injected input (`LLKHF_INJECTED`, `hDevice` 0, XTEST) doesn't count as typing (TP A4); without it, level-B minutes and a higher audit weight; regular timing and cadence flags; the revealed sample; the interview |
| 6 | The same, through a hardware HID board or a VM's virtual keyboard, with timing replayed from the teen's own recordings (U3, T6, T10, T21) | credited, if undetected | content, consistency over weeks, cross-user signals, and the interview, as for any design (TP A6) |

Steps 1 to 3 are the cheap attacks, and they earn nothing by construction. Step 2b is the cheap attack that does earn, and the design labels those hours for what they are. They never reach the corroborated band, and audits concentrate on them. Step 4 is much more work than step 5 for the same result, so in practice the ladder collapses to steps 2b, 5 and 6. All three need a real agent producing a real, public, working project, which then has to pass "The project must do what it says it does" (B, vision post) and a live conversation about its code. That is the most an evidence design can force without a kernel driver or a webcam, and TP A5 shows why pairtime shouldn't go there: in the CSEDU 2021 study Proctorio flagged none of six students told to cheat, and Riot had to defend Vanguard's kernel driver in public.

What I accept, with its bound and its coverage (TP A6):

| Attack | Why it can't be stopped | Bound | Coverage |
|---|---|---|---|
| U1, T17b: someone else at the keyboard | every signal is a real human's | `DAY_CAP_S` 12 h a day (SM 8.6); one attention stream (CC5) | the interview; v2 typing-profile drift, as a question for a reviewer only |
| U2: performative struggle, padding | engagement is real; its value is a judgment | dark-flow discounts and the no-progress half rate (SM 4); uncommitted cap of 1 h (SM 4.3) | the reviewer's "is this project worth these hours?"; surviving lines; `low_survival` |
| U3, T6, T10, T21: the full puppeteer | every per-event signal reads human | caps per day and per submission | step 6 above; the hours term in `p` keeps big claims expensive |
| U4: a project rebuilt from outside Hack Club and paraphrased by an LLM | code similarity fails after a rewrite (TP A4) | CC3's `unexplained_lines` when code appears faster than recorded edits | the interview's live change |
| U5: colluding reviewers or program owners | governance, not evidence | random assignment, seeded cases | per-reviewer statistics, HQ spot-checks, a bundle anyone can re-verify, the public day root |
| U6: invented offline sessions | nothing witnesses offline time | the 36-hour flush window, the 24-hour push rule and the 3-hour cap beyond it (D6) | offline and backfilled share in `risk`, which raises `p` |
| T9 residue: a modified client that gets everything consistent | possible in principle | the same caps | step 4 above, and the client build id: custom builds are allowed, labeled, and need more evidence, as Hackatime allows custom clients (TP A3 T9) |

## 7. Privacy

The privacy model is the trust model seen from the teen's side. Logged hours produce numbers and commitments, never content. Submitted hours reveal metadata for one project's window plus a small random sample of content, chosen by a draw the teen can verify, previewed and redacted by the teen before it leaves. That is R10's "different privacy guarantees for logged hours vs. submitted hours", and it removes the trade Hackatime forces today, where hiding file names "can prevent reviewers from verifying your work" (TP B3).

### 7.1 Disclosure tiers

| Tier | What | Where | Who sees it |
|---|---|---|---|
| 0. Local only | Raw prompts and final agent messages; transcripts, read in place and never copied; file paths in clear; per-slot presence and focus; typing aggregates; MinHash sketches and error signatures in clear; opt-in frames; the device key; the ledger bodies of every logged-only minute | `ledger/` and `vault/` on the device, content encrypted (3.4) | The user |
| 1. Continuous | Batch roots; minute buckets per project without anchor ids (D1); commit records; client build, scoring version and harness names; project fingerprints as HMACs | pairtime server | The user's own dashboard, live; public aggregates, daily (7.3). Nothing in this tier is scored for fraud until the user submits (TP B2 point 6) |
| 2. Revealed for submitted hours | For each batch overlapping the window, headers and body hashes; bodies of the submitted projects' events and of presence, power and focus events; `CHALLENGE_K` redacted turns with their agent messages; opt-in frames for 20 drawn minutes | Evidence store behind HCA sign-in with a reviewer role | Assigned reviewers, Fraud Squad, HQ spot-checkers; every view logged and shown to the user |
| 3. Public | Profile aggregates, public projects, forge-verified commits of public repos (SP 5.2) | Public pages | Anyone |

There is also a mode below Tier 1. With `mode = "local"`, the agent uploads nothing: no roots, no buckets, no profile. The dashboard, recaps and chess clock still work, because they never needed the server. Hours logged in local mode are unwitnessed and can never be submitted. It's for privacy-conscious users who want the self-improvement tool and nothing else (R3, R6), and it is the fallback TP B2 point 5 asks about for Indian under-18s once the DPDP children's rules start, around 2027-05-13.

### 7.2 Data inventory

TP B8 lists every field. This summary shows where this design differs or adds something.

On the device:

| Field | Store | Kept |
|---|---|---|
| Raw prompt text | vault | 30 days, or until a submission covering it is decided, whichever is later; the user can shorten it (TP B8) |
| Final agent message per turn (D3) | vault | same as prompts |
| Transcripts | not copied; offsets and hashes only | the harness's own retention (Claude Code: 30 days, HS 3.2) |
| File paths and names | vault, for the local recap; the ledger holds per-device HMACs | 30 days in the vault |
| Window titles | never read (SM 1.2) | |
| Event metadata, including presence and focus slots and committed derived inputs | ledger bodies | 180 days (TP B8, `E_i`) |
| Typing aggregates, opt-in | vault | 90 days (TP B8) |
| MinHash and SimHash sketches | vault | 90 days (TP B8) |
| Batch roots and server receipts | ledger | 180 days, or until every submission using them is past its spot-check window |
| Frames, opt-in, v2 | vault | 30 days |
| `K_dev` | DPAPI, Keychain or Secret Service | until uninstall or rotation |

On the server:

| Field | Who can see it | Kept |
|---|---|---|
| `hca_sub`, `slack_id`, email (encrypted at rest), GitHub uid | user and admins; programs the user connected; email only with `email:read` (SP 3.3) | account lifetime |
| `device_id`, client build, harness names and versions | user, admins | device lifetime plus 90 days |
| IP address and JA4 | fraud tooling and admins | 30 days, then truncated to /24 or /48 (SP 8.7) |
| Minute buckets | user live; public as daily totals; reviewers for submitted windows | per-minute 13 months, then daily totals per project (TP B8) |
| Witness rows `(device_id, b, n, R_b, H_b, received_at)` | system; auditors through the day roots | 180 days; rows used by a submission stay until its spot-check window closes |
| Commit records and project fingerprints | user; public for public repos; reviewers | account lifetime |
| Tier 2 evidence | reviewers, Fraud Squad, spot-checkers | until the spot-check window closes; 12 months after the decision until HQ sets one (6.6) |
| Risk scores and flags | reviewers, Fraud Squad | with the submission's evidence |
| Reviewer decisions and deflation text, written without personal data | program, HQ | as long as the Unified DB row exists |
| Evidence access log | HQ admins and the user | 2 years |
| Interview notes, never recordings | program, HQ | with the decision |
| Minimal fraud record after account deletion: `hca_sub`, verdict, date, reason category | Fraud Squad | a period Hack Club sets, reported as retained by the eraser (TP B3) |
| Day roots | public | permanent; only hashes and a daily count |

### 7.3 Defaults and consent

Every default except one is the high-privacy one for everyone, so pairtime doesn't need to know anyone's age to get them right (TP B2 point 1). The exception is the profile, which R5 requires to be public by default, so its content is cut down until a public default is safe for a 14-year-old.

- Profiles are public by default because R5 says so, and the public page is built so that being public costs a minor little (next bullet). Onboarding shows the actual preview with a one-click "Make it private". TP B7 point 1 says that if Hack Club reads R5 as literally pre-public, the aggregate-only page should be the public one and the decision should be written up as the Children's Code's "compelling reason" assessment, which is what this design does. For deployments where counsel says New York's CDPA or GDPR Art. 25(2) needs an explicit choice, `profile_default = "ask"` shows two buttons with nothing preselected and "Private" the more prominent one (TP B2 point 7, TP B7, TP Open question 5).
- Public pages show daily totals, no hour-of-day heatmap, and refresh at most hourly. They never show a real name, school, city, age or time zone, and never any trace of review: no flags, tiers, deflations or rejected hours (TP B7). The owner's dashboard is the real-time one, and R5's "updates in real-time as time is logged" is about the dashboard.
- "Currently pairing" live presence and leaderboards are opt-in. Leaderboards rank consistency over a rolling window rather than hours (SP 5.2, PA 4.3).
- The typing module, the screen evidence module and any use of data for calibration research are each asked once, with "No" the most prominent answer, nothing degraded on refusal, and no repeat question for a calendar year (TP B2 point 7). One nuance needs counsel: declining the typing module never lowers credit, but it does raise the audit weight of level-B minutes (5.2), and whether a higher chance of an audit counts as "degrading" under the CDPA's § 899-ff(4) is a legal question, not an engineering one.
- HCA gates age: accounts HCA marks under 13 are refused, and hours logged on or after the 19th birthday are excluded from submissions using HCA's eligibility answer, so pairtime never stores a birthdate (TP B3).

### 7.4 What the teen can see and do

- `pairtime privacy show` prints the next upload exactly as it will be sent (TP B1, the VS Code `--telemetry` lesson), and the local dashboard's upload log shows every past one.
- The ledger browser shows every event pairtime recorded, in plain JSON, and `pairtime ledger show --day 2026-11-19` prints the same.
- A submission preview shows the exact metadata reveal and, after the draw, each challenged item with its redactions. Any item can be withheld, at the cost of the minutes it supports.
- Each submission's page lists every evidence view: who, when, and in what role.
- "This revealed something it shouldn't have" purges a revealed item from the evidence store at once. The minutes it supported stop being credited, which the program sees as a revision (TP B4).
- A `.pairtime-ignore` file in a folder, or an exclude pattern in `config.toml`, keeps a project out entirely. The tray has a pause switch.
- Local deletion wipes the data directory and the device key. Server deletion runs from settings or through Hack Club's privacy hub (7.5).

### 7.5 Deletion, export and the local store

The server implements Hack Club's privacy-hub contract: `POST /api/privacy/delete` and `POST /api/privacy/export`, with `Authorization: Bearer <JWT>`, `alg` ES256 with a `kid`, `iss` `hackclub-privacy`, pairtime's own `aud`, the required claims `iat exp jti request_id action dry_run subject`, a lifetime of at most 300 s, single-use `jti`, and `dry_run` support. That is the contract Hackatime and Lapse already implement (TP B3), so Hack Club's DPO process covers pairtime without a new procedure. The eraser deletes every server row in 7.2 and reports the minimal fraud record as retained, with the reason (TP B3 point 2).

On the device, deleting `K_dev` makes every commitment unopenable, so a lost laptop's ledger proves nothing about its owner's text (TP B6). The vault's encryption protects against other users of a family computer, cloud sync of the home folder and casual reading. It doesn't protect against malware running as the same user, which can ask the keystore for the key, and the privacy page says so (CP 5.2). Keeping the ledger free of text limits what such malware could steal to 30 days of prompts, the lesson of Windows Recall's first preview (TP B1).

## 8. Adoption

### 8.1 The target and the lever

The acceptance criterion is 1,000 weighted projects in the Unified YSWS Projects DB, which Zach confirmed means about 10,000 hours that "actually make it into the YSWS DB" (B). A weighted project is 10 hours of shipped work (HC 3.2). For scale, Stardance approved 27,600 hours over a summer, about 2,760 weighted projects, so the bounty needs a bit more than a third of one Stardance (HC 6). At Stardance's approval rate of 22%, 10,000 approved hours would take about 45,000 submitted hours (HC 3.2). If pairtime's credited hours are approved at the 0.9 ratio SM 7.3 targets, it takes about 11,000.

The lever this design pulls is the gatekeeper. Zach said "We will work with Quality and Integrity to provide good guidance around how to use new time tracking tools" (B), and the thread's adoption worry was that "YSWS authors have to accept the hours, and they won't if the tool makes fraud review harder" (B, themes). Programs follow HQ guidance and fear spot-check fines: a project whose evidence can't be located, or whose hours don't match the justification, is removed from the Unified DB and the program is fined (HC 3.7). So the pitch, in order of audience:

1. To Fraud Squad and Quality and Integrity: hours whose evidence you can re-verify offline, a draw neither side can steer, a published audit rate, and a review page where the suspicious 10% of hours come first. Ask them to name pairtime bundles as "significant evidence proving that more time should be credited" under the one-third rule (HC 3.7), and to add a `pairtime_links` field to the handbook, like `Lapse Link(s)` (HC 3.6 point 2).
2. To program authors: hours that arrive pre-deflated with reasons in the handbook's own format, fewer spot-check fines, and reviews measured in minutes. A program pays out for approved hours whatever the tracker, so the number that matters to them is reviewer time per approved hour.
3. To teens: a tracker worth keeping for its own sake (R3), which is what keeps people installed between programs.

### 8.2 How pairtime plugs into the pipeline

| Layer | Today | pairtime's plug | Needs agreement from | Ready |
|---|---|---|---|---|
| Tracker on the machine | WakaTime plugins and wakatime-cli to Hackatime (HC 1.5) | the agent, plus local dual-send so Hackatime keeps working (4.5) | nobody | M2 |
| Program app | a `HackatimeService` per program (HC 3.4) | Hackatime-compatible reads for connected users (SP 10.5); the submissions API with Robert-style webhooks (SP 10.2); a drop-in `PairtimeService` client in Ruby and in TypeScript, about 200 lines each, in `clients/` | each program author | M4 |
| Hours review | in-app review (Stardance), `review.hackclub.com` (Flavortown), Ari (HC 3.3, 3.6) | the review page (6.7); a PR to Ari adding a `pairtime` time source to `src/lib/review/settlement.ts` and a `pairtime_submissions` ingest field to ari-webhooks' `internal/ingest/payload.go`, with commit-interval rows that reviewers cut as they cut Hackatime rows today | Ari's maintainers. Ari's repo has no license, so the terms of contributing are unclear (HC Open question 7) | M4 for the page; the Ari PR in January |
| Fraud desk | Telescreen and Robert, `kind: "hackatime"` or `"hardware"` (HC 3.5) | until Robert knows pairtime, the bundle link goes in the justification and programs keep sending Robert what they send now. Proposal: `kind: "pairtime"` with a `pairtimeSubmissionId`, so Robert fetches the bundle and its workbench links to the review page | the Telescreen team | proposal in December |
| Program Airtable | the handbook's justification fields (HC 3.7) | `GET /api/v1/submissions/:id/justification?format=handbook` fills `Alternate Tracking Method` (method, versions, the three bands, the bundle SHA-256) and `Additional Justification` (the review URL); with a linked Hackatime account, also `Hackatime Project Name(s) and Date Range(s)` and `Submitter Hackatime ID` for cross-checks (SP 10.6) | nobody: the handbook already allows "something other than Hackatime or Lapse" (HC 3.7 point 1) | M4 |
| Unified DB and spot-checks | `approved_projects`; evidence must be locatable (HC 3.1, 3.7) | review URLs and bundles stay reachable for the spot-check window; a spot-checker role for HQ; the bundle hash in the justification proves which evidence the reviewer read | Quality and Integrity, for reviewer roles, since a community HCA app can't read `groups` (SP 3.1, Open question 1) | M4 |
| Identity and privacy | HCA; the privacy hub at `yeet.hackclub.com` (HC 3.4) | HCA as the only sign-up on Hack Club's instance; the privacy-hub endpoints (7.5) | HQ, to register pairtime with the hub (SP Open question 10) | M4 |

The Unified DB doesn't name trackers. A project counts toward the bounty when a program puts it in `approved_projects` with hours that came from pairtime, and the record of that is the `Alternate Tracking Method` text inside `Override Hours Spent Justification` (HC 3.1). Every pairtime justification starts with the same fixed line, "Tracked with pairtime <version>", so HQ can count the rows with one search, and pairtime keeps its own count from `submission.decided` webhooks and program outcome calls (SP 10.2). HC Open question 13 asks HQ for read access or a count to confirm it.

### 8.3 Programs, in order

1. A pairtime-native YSWS during winter break. Zach said yes to running your own programs (B). It runs on the standard stack (a program Airtable with the handbook fields, the pairtime review page, Robert for fraud), and its review rules are the ones this design proposes, so it is where pairtime's evidence first meets real reviewers. Prizes follow the YSWS norm: programs get $85 per weighted project from HQ and online programs are encouraged to spend $50 of it, $5 an hour (HC 3.2). Target: 200 participants averaging 10 approved hours, 2,000 hours or 200 weighted projects. It doubles as SM 7.5's calibration pilot, for participants who opt in to labeling.
2. Programs on Ari: 3am-ysws, hackxpansion, disposition and macondo already have Ari client code (HC 3.6). One Ari PR reaches all of them, and Ari's per-row settlement is the shape pairtime's rows were designed for. Target: 2,000 hours.
3. Programs with their own apps on Telescreen: the-carnival, pixl, trmnt and Alchemize have Robert clients, and savepoint, halflife, crossroads, live, shrink and fellow-ysws link into Telescreen (HC 3.5). Each needs the `PairtimeService` client and a "Connect pairtime" button. Target: 4 programs, 3,000 hours.
4. Low-ceremony Airtable-form programs whose only record is a repo URL and self-reported hours, which the warehouse runbook says "can rank high by weighted projects" (HC 3.2). Any live tracker is an upgrade for them, and the zero-code path is enough: one form field for the review URL and one Airtable script that calls the justification endpoint. Target: 10 programs, 1,500 hours.
5. A second pairtime-native program in spring, a shorter "ship week". Target: 2,500 hours.

The targets add up to 11,000 approved hours, a 10% margin over the 10,000 needed. Two rules apply throughout. Favor new projects, because Flavortown-style syncs submit only the increase for a Code URL another program already paid (HC 3.3). And use pairtime submissions only for windows the program hasn't already paid through Hackatime, because the server rejects overlapping submissions (SP 10.2).

### 8.4 Timeline

Today is 2026-11-19. Milestones refer to section 10.

| Dates | Build | Adoption |
|---|---|---|
| Nov 19 - Dec 3 | M0, M1, M2 | Send this design's evidence model, with a clickable review page on synthetic data, to Quality and Integrity and Fraud Squad. Ask HC Open questions 1-3 and TP Open question 3 |
| Dec 3 - Dec 17 | M3, M4, with labeling mode (WP4.10) first | Closed alpha: about 20 Hack Clubbers. SM 7.5's shadow week needs only the client, so it starts Dec 3; labeled sessions start once labeling mode lands, around Dec 10. Announce the winter YSWS |
| Dec 17 - Jan 3 | M5; fit and freeze scoring 1.0.0 (SM 7.5 week 3) | Winter YSWS opens for building. Submissions open after the freeze; anything submitted earlier is re-scored when the version changes, as SM 7.6 does for open submissions. Ari PR and Robert `kind` proposal sent |
| Jan 3 - Feb 14 | M6, fixes from reviewer feedback | Winter YSWS ships and reviews; first Unified DB rows. Onboard Ari programs and the first two Robert programs |
| Feb 14 - Apr 30 | v1.5 items as needed | Quality and Integrity guidance published; Airtable-form programs; spring ship week. Cross 1,000 weighted projects |

The schedule has slack in one place only. If HQ guidance arrives late, the native YSWS still counts, because its hours enter the Unified DB through the handbook's existing `Alternate Tracking Method` path.

## 9. Cost at R11 scale

R11's load is 15,488 MAU logging 109,759 hours a month: 6.59 million active user-minutes, 152 concurrent users on average, about 610 at a daily peak and 1,220 at an event peak, with the peak factors assumed (PA 5.1). The evidence design adds work on the client, not the server: hashing, Merkle roots and redaction all happen on the laptop, and the server's extra jobs are signature checks, witness rows and verifying submissions.

| Item | Volume | Storage | Source |
|---|---|---|---|
| Ingest requests | one per active device-minute (6.6M a month) plus hook-triggered flushes coalesced within 10 s (at most 4.4M): about 11M a month, 4.2/s on average, about 34/s at the 8x peak | about 0.5 KB each gzipped, 5.5 GB a month of ingress | SP 2, this design 3.8 |
| Signature checks | one Ed25519 verification per request | a few percent of one core at peak (estimate) | SP 8.3 |
| Database writes | one writer committing every 50 ms or 500 rows: at most 20 commits/s at any request rate | | SP 7.4 |
| Witness rows | at most one per device-minute: 6.6M a month | about 0.5 GB a month, kept 180 days: about 3 GB at steady state | SP 8.5 |
| Minute buckets | at most one row per device-minute | 0.6-1 GB a month; per-minute detail kept 13 months, then daily totals: about 10 GB at steady state | PA 5.2 schema A, TP B8 |
| Rollups, commits, accounts | small | well under 1 GB a year | PA 5.2 |
| Tier 2 evidence | 50-100 KB of metadata and a few KB of content per submitted hour (3.8) | about 1 GB for the 10,000-hour acceptance target; if half of all logged hours were submitted, about 5.5 GB a month, about 66 GB at 12 months' retention, roughly $1 a month in R2 at $0.015 per GB-month | SP 9.2, 9.4 |
| Day roots | one small commit a day to a public repository | | TP B6 |
| Opt-in frames, v2 | about 3 MB per submission that opts in | a few GB a month even at high uptake | 6.8 |
| SSE | up to 5,000 open streams at an event peak | 50-100 MB of RAM | SP 6.2 |

All of it fits on the box SP and PA priced:

| Setup | Monthly cost | Share of $500 | Source |
|---|---|---|---|
| Hetzner CCX13 (2 dedicated vCPU, 8 GB, 80 GB NVMe, HEL1) plus Object Storage and snapshots | $48.94 | 10% | PA 5.5, SP 9.4 |
| CCX23 primary plus a CCX13 warm standby restoring from the replica, plus Object Storage | $149.68 | 30% | SP 9.4 |
| Fallback while Hetzner lists CCX plans as "currently unavailable", as it did on 2026-11-19: AX42 dedicated plus Object Storage, €49 one-time setup | $93.77 | 19% | PA 5.5, SP 9.4 |
| Pilot on Karim's home server, R2 inside the free tier | about $1-3 of electricity | under 1% | SP 9.4 |

Litestream to R2 with a 1 s sync interval costs about $7 a month in Class A operations; a 10 s interval stays inside the free tier, and Hetzner Object Storage charges nothing for API calls (SP 9.2). For comparison, Hackatime's bill is $213 a month and its effective cost $55 (B).

What would break the budget is excluded by design: per-event server-side LLM calls, which PA prices at about $1,100 a month for 2.2M turns at $0.0005 each; storing transcripts for logged rather than submitted hours; and full timelapse capture and encoding (PA 5.5, HC 4.2). The server never sees content it would have to process, so none of these can creep in by accident.

Two costs sit outside hosting and outside the $500. Code signing: Azure Artifact Signing at about $9.99 a month, or SignPath's free open-source program once pairtime has a release and some reputation, plus Apple's Developer Program at $99 a year for notarization (CP 4.3, 4.4). And reviewer time: about 110 reviewer-hours per 10,000 approved hours under 6.9's estimates, which is a program cost, and the one this design most tries to shrink.

CI tests the claim. A `loadtest` workflow in GitHub Actions replays synthetic traffic for 15,488 MAU at the 8x peak against a `pairtimed` running on the same runner with the CCX13's limits (`--cpus=2 --memory=8g` in Docker) and fails if p99 ingest latency passes 250 ms or the writer falls behind (WP3.8).

## 10. Build plan

AI agents build this, one work package per session of 2 to 4 hours, and nothing is built or tested on a developer machine. Every acceptance criterion below is a GitHub Actions job, and Actions is free on public repos for Linux, Windows and macOS runners (CP 1.5). An agent pushes a branch and reads failures with `gh run view <id> --log-failed`. Every workflow also has `workflow_dispatch`, so a job can be re-run by hand.

Rules for every work package: a PR with tests; CI green on `ubuntu-latest`, plus `windows-latest` and `macos-latest` for anything touching platform code; `CGO_ENABLED=0` enforced; any new dependency passes the license job; and no change to `pkg/score` output without a scoring-version bump (WP1.7). SM's worked examples were computed by hand, so where a fixture and SM's table disagree, the work package finds which one is wrong and fixes it in the same PR, with a note in SM.

The order puts the evidence core first. M1 and M4 are what this design is for, and M4 is what programs adopt, so nothing in M5 or M6 may block them. There are 48 work packages, roughly 150 agent-hours. M1 is mostly a chain, but M2's adapters and M3's server pieces run in parallel, so four to six sessions a day fit M0 to M4 into the four weeks that section 8.4 gives them.

### M0. Foundation

| WP | Scope | Depends on | Acceptance in CI |
|---|---|---|---|
| 0.1 | Go module, `cmd/pairtime` and `cmd/pairtimed` stubs, `ci.yml`: `gofmt -l`, `go vet`, staticcheck, `go test ./...` on three OSes, cross-compile of `windows/{amd64,386,arm64}`, `darwin/{amd64,arm64}`, `linux/{amd64,arm64}`, and a dependency license check | none | all jobs green; the seven binaries build with `CGO_ENABLED=0`; a size gate fails any client binary over 15 MB (CP 7) |
| 0.2 | `pkg/event`: types for every SM 1.2 kind plus the header fields of 6.2; deterministic CBOR; generated `schema/event-v1.json`; per-field allowlists marking what may ever leave the device | 0.1 | encode-decode-encode is byte-identical under property tests; the committed schema equals the generated one; `FuzzDecode` runs 60 s with no panic |
| 0.3 | The SM section 6 timelines as event fixtures (`testdata/timelines/6.1` to `6.6`), with per-minute expected credit and reason codes transcribed from SM's tables | 0.2 | fixtures validate against the schema; a test checks the fixture totals against SM's: 10,095 s (6.1), 265 s (6.2), 1,800 s for 14:00-14:30 (6.3), 810 s (6.4), 1,490 s (6.5), 0 s (6.6) |

### M1. Ledger and scorer

| WP | Scope | Depends on | Acceptance in CI |
|---|---|---|---|
| 1.1 | `pkg/ledger`: header, nonce, leaf, RFC 9162 Merkle tree, batch seal, chain head, inclusion proofs; frozen vectors in `testdata/ledger/vectors.json` | 0.2 | a stdlib-only Python reference (`tools/ledger_ref.py`) computes identical roots, heads and proofs for 1,000 random batches; any one-byte mutation fails verification |
| 1.2 | Content commitments: tokenization, per-token salts, `C_i`, reveal with removed spans, verification | 1.1 | property tests over random texts and random span removals: valid reveals verify, any changed kept token fails, span types and token counts survive; the Python reference agrees |
| 1.3 | Ledger writer: length-prefixed append-only segments, partial-tail recovery, sealing every `BATCH_S`, receipt storage, index rebuild | 1.1 | a crash loop kills the writer at random points 200 times on Linux and Windows runners; after each restart the chain verifies and `seq` has no gap except at the tail |
| 1.4 | `pkg/score`: presence, prompt origin, anchors and leads (SM 2.1-2.3) | 0.3 | table tests per rule; fixtures 6.2 and 6.6 reproduce minute by minute |
| 1.5 | `pkg/score`: reading bucket, lookups, the combine rule, attribution (SM 2.4-2.7, 3) | 1.4 | fixtures 6.1, 6.3 and 6.4 reproduce minute by minute |
| 1.6 | `pkg/score`: dark flow with committed derived inputs (6.2), progress, rollups, reason codes, canonical parameter hash (SM 4, 5, 8) | 1.5 | fixture 6.5 reproduces; the parameter hash is golden; changing any parameter changes it |
| 1.7 | `score-regression` job: all fixtures against golden outputs | 1.6 | the job fails when outputs change unless `pkg/score/VERSION` changes in the same PR (checked with `git diff` in CI) |

### M2. Client capture

| WP | Scope | Depends on | Acceptance in CI |
|---|---|---|---|
| 2.1 | Agent skeleton: single-instance lock, AF_UNIX socket on all three OSes with a named-pipe fallback for Windows builds without it (CP 6.3), loopback HTTP with token, `Host` and `Origin` checks, inbox drain; `pairtime hook` with stdin cap, 100 ms connect timeout, inbox fallback, silent exit 0 | 1.3 | with the agent down, a hook call writes one inbox line and exits 0, p95 under 100 ms over 100 runs on `windows-latest`; the agent drains the inbox on start; bad `Host` or `Origin` gets 403 |
| 2.2 | `os/presence` and `os/power` on Windows, macOS (purego), X11, Mutter and `ext-idle-notify-v1`; project-context gating (3.3) | 2.1 | fake-clock tests for gaps and sleep; samplers return values without error on Windows and macOS runners; on Linux under Xvfb, `xdotool` input resets idle |
| 2.3 | `os/focus`, `os/procs` and hosted-session resolution (SM 3.2) | 2.1 | under Xvfb with a window manager, `xdotool` focus switches between two `xterm`s produce focus events; process-tree classes come out right for spawned chains (`python` to `bash` to a child) on all three OSes |
| 2.4 | Claude Code adapter: hook plan, apply and revert, payload parsing, transcript tailer with cursors, OTLP receiver | 2.1 | recorded payloads and transcripts produce the golden events; applying twice equals applying once; revert restores semantically equal JSON; existing git-ai entries survive the merge |
| 2.5 | Codex adapter: hooks, trust notice, rollout reader including `.jsonl.zst` | 2.1 | golden fixtures |
| 2.6 | Cursor and Copilot adapters, including Copilot's `events.jsonl` reader | 2.1 | golden fixtures |
| 2.7 | Gemini CLI and Devin adapters; harness dedupe for shared Claude-format hooks | 2.4 | golden fixtures; one hook event read by two harness configs yields one event |
| 2.8 | WakaTime local endpoint and the `~/.wakatime.cfg` installer (4.5) | 2.1 | the job downloads wakatime-cli v2.28.4, points `api_url` at a fake Hackatime and `[api_urls]` at the agent, sends a heartbeat, and both receive it; a malformed key makes the installer refuse to write |
| 2.9 | `git` adapter: commits, `--shortstat`, rewrites from the reflog, root-commit set, trailer actor hints | 2.1 | scripted repos on Linux and Windows: commit, amend, a non-interactive rebase through `GIT_SEQUENCE_EDITOR`, a merge; events and `rewrites[]` match goldens |
| 2.10 | `pairtime track`, the AGENTS.md installer (4.4), the plugin inbox and HTTP endpoint, the conformance runner | 1.4, 2.3 | `track` from a parent with no OS input opens no anchor; with input in the host window within 30 s it opens `P` with `via_track`; the example plugin's fixtures pass conformance |
| 2.11 | `pairtime install`, `doctor` and `uninstall`: the consent screen built from each adapter's `ConfigDiff`, autostart per OS | 2.4-2.9 | snapshot tests of the consent output; in a temporary home on each runner, install writes the Run key, LaunchAgent or `.desktop` file and uninstall removes everything it wrote |

### M3. Server core

| WP | Scope | Depends on | Acceptance in CI |
|---|---|---|---|
| 3.1 | `pairtimed` skeleton: config, SQLite WAL through modernc with the version assertion of SP 7.4, migrations, one writer goroutine, `/healthz`, `/readyz` | 0.1 | migrations and tests pass on SQLite and on a Postgres service container; startup refuses an SQLite older than 3.51.3 |
| 3.2 | Accounts: HCA OIDC against a mock provider, the device grant (RFC 8628), `ptd_` tokens with checksum, device key registration | 3.1 | an end-to-end test runs device sign-in against the mock HCA; token checksum tests; HCA scopes are requested explicitly |
| 3.3 | Ingest and witness: RFC 9421 signature and `Content-Digest` checks, chain link, dedupe, partial acceptance, the no-free-text schema firewall, rate limits, signed receipts | 3.2, 1.1 | random valid chains are accepted and any mutation is rejected; a free-text field returns 422; receipts verify with the published key |
| 3.4 | Rollups and SSE: additive upserts, revisions within `BUCKET_REVISION_MAX_S`, topic ring buffers, `Last-Event-ID` resume, keepalives | 3.3 | 1,000 simulated SSE clients receive every delta; late revisions are rejected; dashboard totals equal the sum of stored buckets |
| 3.5 | Projects, git and forges: fingerprint HMAC, remote normalization, `server_first_seen_at`, GitHub App webhook signature checks, forge verification clients for GitHub, GitLab and Forgejo | 3.3 | recorded webhook and API fixtures; a diffstat mismatch is detected |
| 3.6 | Public profile and owner dashboard in Svelte | 3.4 | Playwright: the public page and its JSON contain no hour-of-day data and no private project names; the owner dashboard updates within 2 s of an ingest |
| 3.7 | Client uploader: signing, retries with jitter, failed-upload records, catch-up flush, receipts | 3.3, 1.3 | end to end: stopping the server mid-run and restarting it yields a catch-up flush, `offline` labels per D6, and `witnessed` everywhere else |
| 3.8 | Day-root publisher; `loadtest.yml` | 3.3 | the publisher writes a root file and inclusion proofs verify against it; `loadtest` replays the 8x R11 peak against `pairtimed` limited to 2 CPUs and 8 GB, with p99 ingest latency under 250 ms (9) |

### M4. Submissions, reveal and review

| WP | Scope | Depends on | Acceptance in CI |
|---|---|---|---|
| 4.1 | Submission API: freeze with the `r` commitment, program OAuth clients and scopes, idempotency on `(program, external_id)`, overlap rejection, signed webhooks, `updated_since` | 3.5 | API tests; webhook signatures pass a verifier written from Robert's documented scheme (HC 3.5) |
| 4.2 | Metadata reveal on client and server: project-scoped bodies, CC7, witness lag, re-score with held minutes (CC6, D7) | 4.1, 1.7, 3.7 | submitting only project D of fixture 6.3 reproduces D's credit, and the request contains no bodies of other projects' events; a tampered reveal fails CC7; a tampered live bucket holds its minutes |
| 4.3 | Challenge and content reveal: seeded draw, the redaction pipeline (known values, gitleaks rules as data, Presidio-style validators), withholding | 4.2, 1.2 | draw test vectors; a planted-secrets corpus built from gitleaks' rule examples is fully redacted; structured PII with checksums (cards, IBANs, SSNs) is caught; a withheld item removes exactly its minutes |
| 4.4 | `pkg/check`: CC1-CC11 | 4.2 | for each check, one red-team fixture fails it and one clean fixture passes; results match goldens |
| 4.5 | `pkg/sentinelapi`, a public stub sentinel with SM 8.7's starting thresholds, the audit formula | 4.4 | the stub flags fixture 6.6 for regular timing; with zero risk, `p` is 0.08 at 5 h, 0.087 at 10 h and 0.306 at 40 h |
| 4.6 | Review page in Svelte (6.7) | 4.4, 3.6 | Playwright on a synthetic submission: approve, cut with a required reason, restore capped at discounted seconds, "approve remaining" locked until must-view rows are opened, and the access log entry visible on the teen's page |
| 4.7 | Bundle and `pairtime verify` | 4.3, 3.8 | verify passes on an end-to-end submission with networking disabled (`unshare -n` on the Linux runner); a one-byte change in any file exits 2 for signatures and proofs, 1 for numbers |
| 4.8 | Handbook justification, Hackatime-compatible reads, `PairtimeService` clients in Ruby and TypeScript | 4.1 | golden justification text; Hackatime-compatible responses match recorded Hackatime JSON shapes; both clients' test suites run in CI |
| 4.9 | Privacy-hub endpoints and the eraser | 3.2 | ES256 tokens from a generated P-256 key verify; a replayed `jti` is rejected; `dry_run` leaves the database unchanged; the eraser reports the retained fraud record |
| 4.10 | Labeling mode on the review page for SM 7.2's pilot set: two labelers mark minutes in SM 7.1's categories with a volunteer's screen recording beside the timeline (a Lookout timelapse or any recorder the volunteer picks), plus an agreement report | 4.6 | Playwright: labels save and export in SM 7.1's categories; Cohen's κ between two seeded labelers is computed correctly on a fixture |

### M5. Self-improvement, typing evidence and release

| WP | Scope | Depends on | Acceptance in CI |
|---|---|---|---|
| 5.1 | Local dashboard: today and week, chess clock, recaps, ledger browser, upload log, `privacy show` | 2.11, 3.7 | Playwright against the agent on fixture data; the `privacy show` output equals the next upload body byte for byte |
| 5.2 | Typing module: Windows Raw Input counts and `hDevice` 0 share, macOS `CGEventTap` through purego, X11 XInput2 | 2.2 | on `windows-latest`, keys sent with `SendInput` count as injected; under Xvfb, `xdotool` (XTEST) input counts as injected; the store's schema has no field that can hold a key identity, enforced by a schema test |
| 5.3 | Tray on Windows and Linux; self-update from a signed manifest; `pairtime backup` of the ledger and vault to a folder the user picks (11.1, risk 7) | 2.1 | a bad manifest signature is rejected; the Windows rename-and-replace path passes on `windows-latest`; a backup restored into a fresh data directory reveals and verifies a fixture submission |
| 5.4 | Release pipeline: GoReleaser, checksums, signed update manifest, Scoop, Homebrew tap and winget manifests, per-platform VSIX packages that bring the binary | 5.3 | a tag on a fork produces every artifact; the VSIX loads in `@vscode/test-electron` and finds its binary |
| 5.5 | Footprint gates | 5.1 | on `windows-latest`, 10 minutes of synthetic traffic: agent RSS under 25 MB, idle CPU under 0.1%, hook round trip p95 under 50 ms (CP 7) |

### M6. After the pilot starts

| WP | Scope | Depends on | Acceptance in CI |
|---|---|---|---|
| 6.1 | Sampled screen evidence (6.8), Windows and X11 first | 4.3 | under Xvfb, frames are captured only while an `xterm` hosting a fixture session has focus; frame hashes are committed and reveals verify |
| 6.2 | Dolos as a review-time service for public repos | 4.6 | two fixture repos with copied code raise the similarity flag |
| 6.3 | `pairtime mcp` | 2.10 | an MCP client fixture produces a harness-identity event and no anchor |
| 6.4 | Cursor `state.vscdb` reader, rate-limited | 2.6 | a fixture database yields golden events within a CPU budget |

## 11. Risks and cuts

### 11.1 Risks

| # | Risk | Why it matters | What I'd do |
|---|---|---|---|
| 1 | Fraud Squad and Quality and Integrity don't accept commit-then-reveal metadata, a prompt sample and an interview in place of the editor history and screen video they trust today (TP Open question 3, TP A2) | Without HQ guidance, existing programs won't adopt (8.1) | Put the review page and `pairtime verify` in front of them in the first two weeks, on synthetic data (8.4). Pull 6.8's sampled screen evidence forward if they want video. The native YSWS still counts through the handbook's existing `Alternate Tracking Method` path |
| 2 | Level-B machines are the common case. "The average Hack Clubber is on a 10 year old Windows laptop" (B, R8), and without the typing module the OS can't tell a keyboard from a jiggler (SM Risks 1) | Many honest hours land in the witnessed band, and reviewers spend more time on them | Measure the approved-to-credited ratio by evidence level in the pilot. If level-B hours are approved at about the same ratio as level-A hours, the band rule is too strict and should loosen; if not, the audit weight is earning its keep |
| 3 | Harness formats drift. Claude Code's and Cursor's transcripts are internal and change between versions (HS 17.7) | A broken parser silently drops anchors, and CC1 stops being computable | Fixture tests per harness version, parse-failure counters on the user's dashboard, and CC1 reported as "not computable" instead of failing when one record is missing |
| 4 | A private sentinel is judged incompatible with R1 (TP Open question 7) | Tier-2 thresholds would become public and tunable against (TP A5) | Publish the sentinel with conservative rules and raise `AUDIT_BASE_P`. The design leans on sampling and interviews more than on secret rules, so it degrades gracefully |
| 5 | Counsel decides that a public default isn't allowed for minors under GDPR Art. 25(2), the UK Children's Code or New York's CDPA (TP B2, TP B7, TP Open question 5) | R5 asks for public-by-default profiles, so either R5 or those defaults give | Switch to `profile_default = "ask"` (7.3). Public profiles still exist for everyone who picks them, and Hack Club decides whether that meets R5, with the written assessment as the record of why |
| 6 | Live interviews with minors aren't allowed, or there are too few people to hold them (TP Open questions 4 and 14) | The audit's catch rate `q` drops, and the break-even `p` rises | An asynchronous audit: the reviewer asks for a small change in the code the server drew, and the teen pushes it within 48 hours with a written explanation. It's weaker, so `p` rises to compensate |
| 7 | Evidence is lost on the device before submission: a reinstall, a dead disk, a wiped laptop | Those hours can't be revealed, so they can't be submitted beyond their tier-1 numbers | `pairtime backup` to a folder the user picks, encrypted under the device key, and a prompt to submit devlog-sized windows often, as programs already ask |
| 8 | The server's signing key leaks, or the operator misbehaves (U5) | Forged receipts or re-dated roots | Keys rotate with validity dates at `/.well-known/pairtime-keys.json`; the public day root makes backdating visible; bundles verify offline against it |
| 9 | Re-scoring isn't bit-identical across platforms | CC6 would hold honest minutes | `pkg/score` uses integers only, with half-weight seconds kept as half-second units, and the regression corpus runs on all three OS runners in CI (WP1.7) |
| 10 | Adoption arrives too slowly: Ari has no license, a Robert `kind` needs the Telescreen team, and grit is racing for the same programs (HC Open questions 6-7, PA 1.14) | A is first to 1,000 weighted projects | The handbook path needs nobody's permission (HC 3.7), and two native YSWS programs make up 4,500 of the 11,000-hour plan (8.3) |
| 11 | Program reviewers find the evidence model too complex | They fall back to the flat third, and pairtime becomes Hackatime with extra steps | The headline line and the bands carry the decision; everything else is one click deeper. Pilot measure: median review time per submission (6.7) |

### 11.2 What I'd cut first

In this order, when time runs short:

1. All of M6: sampled screen evidence, Dolos, `pairtime mcp` and the Cursor database reader.
2. The typing module (WP5.2). Every Windows and X11 minute becomes level B, and the audit weight covers the difference.
3. The tray and self-update (WP5.3). Users update through winget, Scoop, Homebrew or the editor extension.
4. Hackatime-compatible reads (part of WP4.8). The submissions API and the handbook text cover programs.
5. Hooks for Codex, Gemini CLI and Devin (WP2.5 and 2.7), keeping only their log readers. Their minutes then carry `source=log`, and CC1 isn't computable for them.
6. The day-root publisher (part of WP3.8). Bundles still verify against the server's signature and receipts, but an outside auditor loses the check against a misbehaving server for the period with no published roots, since roots published later prove nothing about earlier days.

Never cut: the ledger and scorer (M1), ingest and witnessing (WP3.3, 3.7), the metadata and content reveal (WP4.2, 4.3), the review page (WP4.6), `pairtime verify` (WP4.7) and the handbook justification (WP4.8). Without those this is a different design, and the one-line thesis at the top stops being true.
