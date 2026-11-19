# pairtime design: adoption first

Status: draft in progress

One of three independent designs for pairtime, written 2026-11-19. This one optimizes for the fastest credible path to real program use, because the bounty goes to the first system used to track 1,000 weighted projects in the Unified YSWS DB, and Zach was explicit that "you need to actually get programs to use it" (BOUNTY.md, organizer replies).

Citations use short names and section numbers:

| Short name | File |
|---|---|
| B | [BOUNTY.md](../BOUNTY.md) |
| HC | [research/hackatime-and-hack-club.md](../research/hackatime-and-hack-club.md) |
| HS | [research/ai-harness-signals.md](../research/ai-harness-signals.md) |
| CP | [research/client-platform.md](../research/client-platform.md) |
| SP | [research/server-platform.md](../research/server-platform.md) |
| PA | [research/prior-art-and-measurement.md](../research/prior-art-and-measurement.md) |
| TP | [research/trust-and-privacy.md](../research/trust-and-privacy.md) (fact-check pending, so anything load-bearing from it is marked) |
| SM | [design/struggle-metric.md](struggle-metric.md) (the metric spec; sections 1-5 are written, 6-8 are still in progress) |

## 1. Pitch

**The bet.** Every Hack Club program already runs on Hackatime. Each program app carries a `HackatimeService`, Robert scores fraud from `kind: "hackatime"` plus project names, Ari settles hours from Hackatime rows, and the YSWS handbook names Hackatime and Lapse as its evidence sources (HC 3.4 to 3.7). A tracker that asks programs to drop any of that will lose to the calendar. So pairtime installs next to Hackatime and takes nothing away. It receives the same editor and AI-transcript heartbeats through wakatime-cli's `[api_urls]` dual-send (HC 1.5), adds the human-side signals Hackatime can't see (harness hooks, OS presence and focus, git), and gives the program one new number: credited struggle hours, already deflated, with the reasons and the evidence, in the handbook's own justification format. The fraud desk keeps reading the Hackatime half of the same activity with the tools it has today.

**What a teen does.** They paste one line (`irm https://<host>/install.ps1 | iex`, or `curl -fsSL https://<host>/install.sh | sh`). The installer finds `~/.wakatime.cfg`, adds one `[api_urls]` line that copies every heartbeat to the local pairtime agent, registers hooks in the harnesses it finds, and opens a browser tab to approve the device with Hack Club Auth. It then sends a test heartbeat and checks that both Hackatime and pairtime received it. After that there is nothing to remember. The 3-hour flow session, the 5 minutes before school and the 5-project afternoon are all counted from what the person does, one second to one project (SM 3.1). What they see is a tray and status-bar total ("1h 52m struggle today, agent ran 3h 10m"), a public profile with a streak and heatmap, and a private weekly recap of how they worked: re-roll loops, go-aheads sent without reading, agent time nobody watched (SM 4.2). That recap is the self-improvement reason to keep it installed on projects they never submit (R3).

**What a reviewer sees.** One page per submission. Pairtime's credited hours sit next to what Hackatime logged for the same window and what the handbook's one-third rule would allow (`handbook_s`, SM 5.4). Below that are rows, one per work session, that can only be cut, as in Ari (HC 3.6), plus commits with server witness times, flags (one-shot session, re-roll loop, backfilled time, prompts with no input), and a Telescreen deep link to the same Hackatime projects (HC 3.5). The deflation is done before they arrive and every cut is explained. They check, cut if needed, and copy the generated justification.

**What a program author does.** Adoption comes in three tiers, and the first needs no code:

| Tier | Effort | What changes in the program |
|---|---|---|
| 0. Link | An hour | Add a "pairtime submission link" field to the submission form. Reviewers open it. The author pastes pairtime's generated text into `Justification - Alternate Tracking Method` and the review URL into `Justification - Additional Justification`. That is legal under today's handbook without any HQ change (HC 3.7, point 1) |
| 1. Drop-in | A day | Point a second instance of the existing `HackatimeService` at pairtime's base URL. The same stats call, with the same Hackatime project names, returns credited seconds (SP 10.5). An Airtable automation script that ships with pairtime fills the justification fields |
| 2. Native | A few days | Use pairtime's submission API with webhooks signed like Robert's (SP 10.2) |

**Why the handbook already creates demand.** Quality and Integrity's rule is that Hackatime `ai coding` time "must be deflated to at most ⅓ of its original amount unless there is significant evidence proving that more time should be credited" (HC 3.7). Ari applies exactly that third (HC 3.6). A teen who really did spend three hours steering and checking an agent has no way today to get more than one of them credited. Pairtime is built to be that "significant evidence", and it is just as willing to show that an agent left on `/goal` overnight deserves nothing (SM 5.4). That gives engaged AI users a reason to install it. Programs get hours that arrive pre-deflated and defensible, which means fewer spot-check fines (HC 3.7, point 5).

## 2. Requirements

| ID | How this design meets it | Why I believe it will hold, and what could break it |
|---|---|---|
| R1 License | Every pairtime file is AGPL-3.0, and the LICENSE file is already in the repo (B, checklist). Dependencies are permissive (x/sys, x/crypto, fsnotify and modernc.org/sqlite are BSD-3; godbus BSD-2; purego Apache-2.0; ncruces/go-sqlite3 and go-keyring MIT; CP 7), and a CI license gate fails the build on any copyleft-incompatible transitive dependency. Code borrowed from Hackatime (MIT) or wakatime-cli (BSD-3) keeps its notice (HC 1.1). The optional private detection module (section 6) runs as a separate process, as Ari does with its `private/` image (HC 3.6), and the public build works fully without it | Mechanical, and CI checks it. The one open point is whether private detection rules are compatible with AGPL section 13, which is Karim's call (HC Open question 11). The design never depends on the private module to meet any requirement |
| R2 Accuracy | Hours are credited by the struggle-metric rules: one human second to at most one project, only when a human act opened a window over it, the person was present by OS input, and focus was on that project (SM 2.7). Reading is credited from a bucket filled by output size, not by waiting (SM 2.4). Dark-flow loops earn zero (SM 4.2). The pilot then fits the parameters to reviewer decisions (SM 7), and every number carries its `scoring_version` | The rules credit exactly the activities the research says carry the human's part of AI-assisted work: verification at 22.4% of time (PA 2.2, CUPS), steering, debugging, hand edits. What could break it: there is no labeled set yet, so the v1 parameters are guesses until the pilot. Section 8 puts the labeling inside the pilot program, where reviewer decisions arrive anyway |
| R3 Worth using | The tray, menu bar and editor status bar show the user's time next to the agent's (the chess clock, PA 4.3). A weekly recap, built on the device, shows bugs they fixed themselves, prompts that changed direction, loops they fell into and agent time nobody watched (SM 4.2; PA 4.3). Streaks need 15 credited minutes a day and allow freezes; badges reward consistency ("built on 18 of the last 30 days") rather than raw hours (PA 4.3). A devlog draft for "everything since your last devlog" saves real work, because programs ask for devlogs (HC 3.3). Lookback fills the dashboard on the first day (SM 2.8) | Hackatime's own appeal is "it helps me track and feel motivated" on projects never submitted (B, R3), and pairtime keeps all of that by not replacing Hackatime. The recap answers a question teens can't answer for themselves: METR's developers misjudged their own speed by about 40 points (PA 2.2). Risk: a tracker that "only counted 35%" reads as an accusation. The UI names uncredited time as the agent's time, not the user's failure (PA 4.4) |
| R4 Passive | The agent starts at login with no admin (HKCU Run key, a LaunchAgent, XDG autostart; CP summary). Hooks are registered once at install. Editor time arrives through the dual-send without the user doing anything (HC 1.5). On install, lookback reads up to 30 days of Claude Code and Gemini CLI transcripts and older Codex and Copilot logs (HS 2, finding 5), so a lookback mode exists from day one | All three R4 stories work without per-session setup. Flow for hours: credited while anchors keep arriving and the person is present (SM 2.3). Five minutes before school: a single prompt and the reading of its answer count (SM 2.3, 2.4). Five projects in an afternoon: one timeline, one project per second (SM 3.1). Lookback satisfies Zach's "sufficiently high quality implementation of a lookback mode" (B, organizer replies) for the days before install, marked as level C evidence |
| R5 Server | One Go server with Hack Club Auth accounts, public profiles at `/@username`, projects keyed by root-commit fingerprint, git integration (client-read refs and reflog, forge verification for public repos, optional GitHub App), and a dashboard pushed over SSE (SP 1). Profiles are public by default as an aggregate-only page (section 7) | SP sizes all of it on one 2-vCPU box (SP 2). SSE per-user deltas avoid Hackatime's 17 worker-hours a day of rollup rebuilds (SP 6.1). A dashboard tab moves within seconds of a hook-triggered sync (SP 6.2) |
| R6 Privacy | Four tiers (TP B6). Prompts, code, file paths and window titles never leave the device for logged hours. WakaTime heartbeats go to the local agent, never to pairtime's server, so file paths stay local (SP 8.8). The server's ingest schema rejects any free-text field with a 422, so a buggy client can't leak a prompt (SP 8.2). `pairtime privacy show` prints the next upload byte for byte (TP B8). The keyboard listener is off by default (TP A8 #5) | The tiers match what privacy-conscious developers accept: nothing but numbers until the user submits, and checkable claims because the client is open source (PA 4.4; TP B1). The privacy-relevant change for a Hackatime user is zero: Hackatime receives what it always did, and pairtime's server receives less than Hackatime does |
| R7 Every harness | Three layers, each wider and weaker than the last (section 4). Native hooks for Claude Code, Codex, Cursor, GitHub Copilot, Gemini CLI and Devin Desktop, which covers the six most popular harnesses (HS 16). A WakaTime bridge that turns wakatime-cli's transcript heartbeats into pairtime prompt and edit events, for every harness that wakatime-cli's 46 parsers read (HS 2, finding 10). Karim's AGENTS.md entry plus `pairtime track` for anything else. New harnesses plug in as adapters that write the documented spool format (SM 1) | Hooks have converged on Claude Code's shape, so one receiver binary covers most of the market (HS 2, finding 1). The bridge gives day-one coverage of the long tail at reduced evidence strength. Risk: undocumented transcript formats change between releases (HS 17.7), so adapters are pinned per version with fixture tests in CI |
| R8 Platforms | One pure-Go binary per OS (`CGO_ENABLED=0`), cross-compiled for `windows/{amd64,386,arm64}`, `darwin/{amd64,arm64}` and `linux/{amd64,arm64}` from one Linux job (CP 7). Targets: binary under 15 MB, agent RSS under 25 MB on Windows, idle CPU under 0.1% averaged over an hour, hook round trip under 50 ms warm (CP 7). No webview stays resident: the dashboard opens in the user's own browser (CP 5.3) | Go starts at 3.6 MB RSS and its sysmon sleeps up to 60 s when idle (CP 1.6). The 15 s idle sample and event-driven focus are cheap on every OS (CP 3.3). Risk: these targets are estimates until measured on a real 2016 laptop. A CI job measures RSS and idle wakeups on `windows-latest` on every release, and the pilot asks for real-hardware reports |
| R9 Trustworthy | The credit rule is the first defense: agent-only time, headless runs, jigglers and parallel sessions earn zero by construction (TP A8 #1). Then witnessing with server receipt time (TP B6), commits as anchors with an uncommitted cap (SM 4.3), public automation labels (SM 2.2), risk-weighted audits with a short live interview (TP A6), and per-day caps (SM 5.3). Dual-send adds an independent witness: Hackatime received the same editor and transcript heartbeats with its own clock, IP and JA4 fingerprint (HC 1.3), so reviewers and Fraud Squad can cross-check through Telescreen | Most of Stardance's 123,400-to-27,600 gap was hours where reviewers "felt the person wasn't pushing themselves" and undeclared AI use (B, R9). The credit rule removes the first by not counting idle and agent-only time, and makes AI use visible rather than self-declared. Accepted residual risk: the full puppeteer, someone else at the keyboard, and performative struggle (TP A6, U1 to U3), handled by sampling and interviews |
| R10 Auditable | A submission freezes a window, its credited seconds, the scoring version and the hashes of its evidence (SP 10.2). Reviewers open per-row evidence. External auditors download a signed bundle and run the open-source `pairtime verify` offline, which recomputes the chain and re-runs the scoring (SP 10.4). The server publishes a daily root of every chain head it received to a public git repository, so even the pairtime server can't backdate (TP B6). Logged-only data never reaches the server, so the privacy guarantee for logged hours differs from submitted hours, as R10 allows | The bundle is checkable without trusting pairtime's UI or server, which is what "an external party" needs. The handbook's spot-check standard, "someone who was not involved in the review should be able to read this justification, follow the links, and reach the same conclusion" (HC 3.7), is met by stable review URLs that stay reachable through the spot-check window |
| R11 Cost | The client computes credit and uploads derived rows (about 1 GB/month) and chain heads (about 0.5 GB/month), never heartbeats or content (SP 2). One Hetzner CCX13 plus object storage costs $48.94/month, and $149.68 with a warm standby (SP 9.4) | 10 to 30% of the $500 cap (section 9). The budget-breakers are server-side LLM calls and storing content for logged hours (PA 5.5), and this design has neither |
| A Acceptance | Programs adopt in tiers that start with no code (section 1). Hours reach the Unified DB through the handbook's existing `Alternate Tracking Method` and `Additional Justification` fields, which need no HQ change (HC 3.7). Robert keeps scoring the same activity through the Hackatime half of the dual-send (HC 3.5). A pairtime-native YSWS, which Zach allowed ("Yes!", B), is the program whose rules pairtime controls. Section 8 has the plan and dates | 1,000 weighted projects is about 10,000 approved hours, roughly a third of Stardance's 27,600 approved hours over one summer (HC 6). The main risk is not engineering but HQ attribution and program calendars (HC Open questions 1, 9). Section 8 makes the first conversation with Quality and Integrity a dated milestone |

## 2. Requirements

## 3. Architecture

### 3.1 Shape

Two Go binaries and one web UI. The client scores on the device and uploads numbers. The server witnesses, rolls up, streams, and turns submitted windows into reviewable, signed evidence. This is the stack the client and server research both recommend (CP 7; SP 1), and I keep it unchanged. wakatime-cli, grit's `gritd` and Wakapi are all Go, so contributors from Hack Club and the Hackatime ecosystem can already read it (CP 1.6; PA 1.8, 1.14).

```text
 THE TEEN'S MACHINE                                                         KARIM'S DOMAIN (Cloudflare tunnel)
                                                                            
 Claude Code / Codex / Cursor / Copilot / Gemini / Devin                    pairtime-server (one Go process)
   └─ hooks (exec form) ──> pairtime hook ──unix socket──┐                    ingest  (signed batches, seq, chain)
 Editors (WakaTime plugins) ──> wakatime-cli ─┬─> Hackatime (api_url, as today)   rollups + SSE hub
                                              └─> [api_urls] .+ = http://127.0.0.1:<port>/api/v1|<uuid>
                                                                 │            accounts (HCA OIDC), devices
 OS idle / focus / power ───────────────────────┐                ▼            projects, commits, forge verify
 .git watchers, harness log tailers ──────────> pairtime agent ───HTTPS────>  submissions, review UI, bundles
 AGENTS.md -> pairtime track ──unix socket──────┘  spool -> scorer           program API, Hackatime-shaped reads
                                                   -> minute buckets         justification text, webhooks
                                                   -> uploader                daily root -> public git repo
                                                   local dashboard (127.0.0.1, own browser)
                                                                              SQLite WAL --Litestream--> R2 / Hetzner
```

### 3.2 Languages and repo layout

Go 1.27 for both binaries, so the scoring package exists once and the server can re-run exactly what the client ran (SP 7.1, 7.3). Svelte with TypeScript for the web UI, built to static files and embedded in the server binary, with API types generated from an OpenAPI file (SP 7.3). Install scripts are POSIX `sh` and PowerShell. The Airtable automation for programs is plain JavaScript, because that is what Airtable scripts run.

```text
pairtime/
  cmd/pairtime/            client binary: agent, hook, track, install, uninstall, doctor, dashboard,
                           submit, privacy, statusline, verify
  cmd/pairtime-server/     server binary
  pkg/event/               normalized event schema (SM 1), spool format v1, JSON Schema export
  pkg/score/               the struggle metric (SM 2-5): pure functions, no I/O, shared by client and server
  pkg/chain/               hash chain, salted Merkle commitments, reveal proofs (TP B6)
  pkg/bundle/              evidence bundle format, signer, offline verifier
  pkg/wakacompat/          WakaTime heartbeat schema, key format, Hackatime JSON response shapes
  internal/agent/          sampler, focus, power, spool, SQLite index, scorer loop, uploader, local HTTP
  internal/adapters/       one package per source: claudecode, codex, cursor, copilot, gemini, devin,
                           wakabridge, track, git, osidle, osfocus, otlp
  internal/install/        per-harness config mergers, ~/.wakatime.cfg editor, autostart, AGENTS.md entry
  internal/server/         ingest, hca, devicegrant, projects, commits, rollups, sse, submissions, review,
                           programs, hackatimecompat, justification, privacyhub, webhooks, dailyroot
  web/                     Svelte UI: profile, dashboard, project, submission, review; shared with the
                           local dashboard
  integrations/            airtable-automation.js, rails-hackatime-service.md, AGENTS.md snippet,
                           ari-journals-bridge.md, robert.md
  sdk/adapters/            adapter author guide, spool JSON Schema, conformance test kit
  testdata/                hook payload fixtures per harness version, transcript fixtures, golden scoring
                           scenarios, WakaTime heartbeat captures
  deploy/                  compose.yml, litestream.yml, runbook.md
  .github/workflows/       ci, release, e2e-wakatime, e2e-install, footprint, restore-drill, license-gate
```

Private detection rules live outside this repository, built into a separate image that the public server talks to over a local socket (TP A8 #9; HC 3.6). The public server has a no-op implementation of the same interface, so every requirement is met without it.

### 3.3 Client

One binary, several roles (CP 7):

| Process | Started by | Does |
|---|---|---|
| `pairtime agent` | Login (HKCU Run key, LaunchAgent, XDG autostart; CP 2.1), or by a hook that finds it down | One per user session, single-instance lock. Samples OS idle every 15 s, listens for focus and power events, tails harness logs and `.git` directories, serves the local WakaTime endpoint, HTTP hooks, OTLP and the dashboard on `127.0.0.1`, accepts hook and `track` messages on a Unix socket, writes the spool, runs the scorer once a minute over closed slots, uploads every 60 s and right after prompt and stop hooks (SP 8.1), checks for updates once a day (CP 4.6) |
| `pairtime hook --harness <h> --event <e>` | The harness, in exec form | Reads stdin with a size cap, applies local redaction, sends one framed message over the socket with a 50 to 100 ms timeout, falls back to one spool append and starts the agent detached, exits 0 and never blocks the agent (CP 6.2) |
| `pairtime track "<text>"` | The coding agent, following the AGENTS.md entry | Same path as `hook`, with `source = cli` (SM 1.2) |
| `pairtime install`, `uninstall`, `doctor` | The user, once | Writes autostart, merges hook entries next to existing ones, edits `~/.wakatime.cfg`, runs the device sign-in, then a self-test of idle detection, hook latency, watchers and both WakaTime destinations (CP 7; section 4.6) |
| `pairtime dashboard`, `submit`, `privacy show`, `statusline`, `verify` | The user | Open the local dashboard with a one-time token; freeze and reveal a submission window; print the next upload exactly (TP B8); print a one-line total for terminal status lines; verify an evidence bundle offline (SP 10.4) |

Capture adapters, each turning one source into SM 1 events (details in section 4):

| Adapter | Source | Events it produces |
|---|---|---|
| `claudecode`, `cursor`, `copilot`, `codex`, `gemini`, `devin` | Hooks, plus each harness's transcript or log reader for lookback and cross-checks (HS 16) | `session.*`, `prompt.submit`, `agent.output`, `agent.tool`, `agent.permission`, `agent.interrupt`, `agent.queue`, `agent.stop`, `agent.subagent` |
| `otlp` | Claude Code's OpenTelemetry export to the local agent (HS 16, item 4) | `otel.active_time`, used only to calibrate (SM 1.2) |
| `wakabridge` | Heartbeats arriving on the local WakaTime endpoint | `editor.activity` from editor heartbeats; `prompt.submit` and `agent.tool` at reduced strength from wakatime-cli's transcript-derived `ai coding` heartbeats (section 4.3) |
| `track` | `pairtime track` | `cli.track`, never an anchor on its own (SM 1.2) |
| `git` | Non-recursive watch on each known repo's `.git` (`HEAD`, `index`, `logs/HEAD`; CP 3.2) | `git.change` |
| `osidle`, `osfocus`, `ospower` | `GetLastInputInfo`, `CGEventSourceSecondsSinceLastEventType`, `XScreenSaverQueryInfo`, Mutter IdleMonitor, `ext-idle-notify-v1`; `SetWinEventHook(EVENT_SYSTEM_FOREGROUND)`, `NSWorkspace`, `_NET_ACTIVE_WINDOW`; suspend and lock notifications (CP 2.2, 3.3) | `os.presence`, `os.focus`, `os.power` |

Local store (CP 5.1, 5.2):

| File | Format | Holds |
|---|---|---|
| `spool/<yyyy-mm-dd>.jsonl` | Append-only JSON Lines, one SM 1 event per line, each with its `chain_hash` | Every normalized event. Raw prompt text is a separate field encrypted with XChaCha20-Poly1305 under a key kept in DPAPI, Keychain or Secret Service (TP DI 20) |
| `pairtime.db` | SQLite, WAL, `synchronous=NORMAL`, commits batched every 15 to 60 s | Slots, turns, anchors, credit slots, minute buckets, upload cursors, log-tailer cursors, project map. Rebuildable from the spool |
| `config.toml` | TOML | Settings, project names and visibility, privacy rules, program links |

The agent serves the dashboard on `127.0.0.1`, and `pairtime dashboard` opens it in the user's own browser with a one-time URL, Host and Origin checks, and a `SameSite=Strict` cookie (CP 5.3, 6.3). A tray icon lives inside the agent on Windows and Linux (fyne-io/systray builds without cgo there; CP 5.3). macOS gets no tray in v1 because it needs cgo and an app bundle (CP 5.3). Mac users see their total in the dashboard, on the web, and through `pairtime statusline`, which a terminal status line can call. Claude Code has a `statusLine` setting for this, but HS doesn't cover it, so check it before building. The WakaTime editor status bar keeps showing Hackatime's total, because wakatime-cli asks `api_url` for it.

### 3.4 Server

One Go process with the Svelte UI embedded, SQLite in WAL mode replicated by Litestream, behind `cloudflared` on Karim's domain (SP 1, 9.1). Components:

| Component | Endpoints | Notes |
|---|---|---|
| Device sign-in | `POST /oauth/device/code`, `POST /oauth/token` | RFC 8628 device grant. The client gets a `ptd_` device token and registers an Ed25519 key; it never holds an HCA token (SP 3.5, 8.3) |
| Accounts | HCA OIDC at `https://auth.hackclub.com`, scopes `openid email slack_id verification_status` requested explicitly | The server is the confidential client because HCA has no PKCE. `hca_sub` is the identity key, and `verified` is required to submit (SP 3.1, 12.1) |
| Ingest | `POST /api/v1/ingest/batches`, `GET /api/v1/ingest/state`, `GET /api/v1/time` | Per-device sequence numbers, batch `Idempotency-Key`, RFC 9421 signatures, partial acceptance, a schema with no free-text fields, one transaction per batch, one writer committing every 50 ms or 500 rows (SP 8.2, 7.4) |
| Time and witness | inside ingest | Re-anchors event times from monotonic readings within a boot; under 15 minutes of lag is `witnessed`, more is `backfilled` (SP 8.4, 8.5) |
| Cross-device merge | inside ingest | One human minute per wall-clock minute per account; conflicts raise `concurrent_devices` (SM 3.4) |
| Rollups and live dashboard | `GET /api/v1/stream`, `GET /api/v1/users/me/dashboard` | Additive deltas per `(user, project, day, activity_kind)`, pushed over SSE with 15 s keepalives and `Last-Event-ID` resume (SP 6.4) |
| Profiles and projects | `/@username`, `/@username/p/:slug` | Public aggregates, public repos named, private projects counted but unnamed (SP 5.2; section 7) |
| Git | `commit` records in ingest; forge fetch for public repos; optional GitHub App | `server_first_seen_at` is the anti-backdating stamp (SP 4.3) |
| Submissions and review | `POST /api/v1/submissions`, `.../evidence`, `.../review`, `.../outcome`, `GET .../justification?format=handbook`, `GET .../bundle`, UI at `/review/submissions/:id` | Section 6 and SP 10.2 to 10.4. At submission, the server re-runs `pkg/score` on the revealed event metadata and must get the client's number |
| Program API | OAuth clients, client-credentials reads, Hackatime-shaped reads, signed webhooks | Section 4.7 and SP 10.1, 10.5 |
| Privacy hub | `POST /api/privacy/delete`, `POST /api/privacy/export` | ES256 JWT from Hack Club's hub, `iss: hackclub-privacy`, dry run first, single-use `jti` (HC 3.4; TP DI 17) |
| Daily root | scheduled job | One Merkle root over every chain head received that day, committed to a public git repository (TP B6) |

### 3.5 Data flows

1. **Logging.** Adapters write events to the spool. Once a minute the scorer closes the finished 15-second slots, applies SM 2 to 4, and writes minute buckets (SM 5.1). The uploader sends buckets, commit records and the batch root to the server. The server applies rollup deltas and pushes them to open dashboards. A credited minute reaches a dashboard tab within about 60 s, or a few seconds after a prompt or stop hook.
2. **Editor time.** wakatime-cli sends each heartbeat to Hackatime and to `127.0.0.1`. The `wakabridge` adapter turns it into `editor.activity`, keeps the file path local, and records the WakaTime `project` name against the pairtime project whose repo contains the heartbeat's `entity` path. That name map is what lets programs query pairtime with Hackatime project names later (4.7).
3. **Commits.** The `.git` watcher sees a commit, the client sends SHA, parents, diffstat, `observed_at` and `credited_seconds_since_parent`, the server stamps `server_first_seen_at`, and for public repos fetches the commit from the forge once to mark it `forge_verified` (SP 4.3).
4. **Submission.** The user (from the dashboard) or the program (through the API) creates a submission for a window and a set of projects. The client reveals the event metadata for that window, the server verifies it against the stored batch roots, re-scores it, then draws `k` content-bearing events at random for the client to reveal with redactions (TP B6). The review page opens. A reviewer cuts rows. The program records the outcome. Webhooks fire.
5. **Program reads.** A program's `HackatimeService`, pointed at pairtime, calls the stats endpoint with Hackatime project names and a window, and gets credited seconds for users who connected to that program (SP 10.5).

### 3.6 Exactly what crosses the network

| From, to | When | What | What never goes |
|---|---|---|---|
| wakatime-cli to Hackatime | Unchanged from today | Everything Hackatime already receives (HC 1.3). Pairtime adds and removes nothing | n/a |
| wakatime-cli to the local agent | Each heartbeat | The full heartbeat, over `127.0.0.1` only | Nothing leaves the machine on this path |
| Client to pairtime server, continuous (tier 1) | Every 60 s while active, plus after prompt and stop hooks | Minute buckets: per 15-second slot a project id, credited seconds, reason code, activity kind and evidence level; uncredited seconds by reason; agent-running seconds; anchor event ids; state; witness flag (SM 5.1). Commit records: SHA, parents, author and committer time, `observed_at`, diffstat, `credited_seconds_since_parent`, optional path HMACs (SP 4.3). Batch roots `(device_id, b, n_b, R_b, H_b)` (TP B6). Project fingerprint `HMAC(pepper, root_sha)`; remote URL only for public repos (SP 4.2). Device metadata: random device id, OS family, client version and build id, harness names and versions | Prompt text, agent output, file paths and names, branch names of private repos, window titles, hostnames, OS usernames, keystrokes |
| Client to server, at submission only (tier 2) | When a window is submitted | Event metadata `E_i` and leaf salts for every event in the window: kinds, times, lengths, typed and pasted counts, flags, content commitments. Then the `k` challenged prompts, with the user's redactions marked. Optional extras the user adds (TP B6) | Anything outside the submitted window; unchallenged prompt text |
| Client to server, on program link | When the user connects a project to a program | The WakaTime project names the user chooses to link, so the program can query by name. Stored as `HMAC(pepper, user_id, name)` for matching | Names of projects the user didn't link |
| Client to GitHub Releases | Once a day | A request for the signed update manifest (CP 4.6) | Any user data |
| Server to HCA, forges, programs, public git | Sign-in; public-repo commit checks; webhooks; once a day | OIDC; `GET /repos/{owner}/{repo}/commits/{sha}` and equivalents; signed submission events; the daily root | Evidence blobs, which are fetched by reviewers through 10-minute presigned URLs (SP 10.3) |

There is no telemetry or analytics channel. Cloudflare terminates TLS at the edge, as it does for Hackatime (HC 1.1), which is listed as an open privacy question (SP Open question 3).

## 4. Integrations

Every integration follows one rule. Use what already runs on the teen's machine and in the program's code, and add the smallest possible thing next to it. Three capture layers cover R7, and each is wider and weaker than the one before. Native hooks for the top six harnesses carry full evidence. The WakaTime bridge reaches every harness wakatime-cli already reads, at reduced strength. Karim's AGENTS.md entry reaches anything with a shell tool, at the lowest strength. A harness can sit in more than one layer, and the strongest wins.

### 4.1 v1 harness set

All hooks are registered in exec form (`command` plus `args` pointing at the real `pairtime` executable), because that is the one hook type every consumer of these files runs: Claude Code's `SessionStart` rejects `http`, and Cursor's loader for Claude-format hooks lists only `command` and `prompt` (HS 2, finding 1; HS 16). Entries are merged next to existing ones, never replacing them, because git-ai rewrites its own entries in the same files daily (HS 13.3).

| Harness | Capture method | Config the installer writes | Events used | Lookback and cross-check reader | Caveats |
|---|---|---|---|---|---|
| Claude Code (CLI, VS Code extension, Desktop Code tab) | Hooks, plus OTLP to the local agent | `~/.claude/settings.json`: `SessionStart`, `UserPromptSubmit`, `Stop`, `StopFailure`, `Notification`, `PermissionRequest`, `PostToolUse` (matcher `Edit\|Write\|Bash`), `SubagentStart`/`SubagentStop`, `SessionEnd`; `env` block for OTel with prompt logging off (HS 16, items 3 and 4) | All SM 1.2 agent kinds. `Stop.session_crons[]` and `stop_hook_active` feed origin rules 1 and 2 (SM 2.2) | `~/.claude/projects/**/*.jsonl`, `history.jsonl` (30 days by default) | Hooks wait for the folder's trust dialog in interactive sessions (HS 3.1). `--bare` runs skip hooks, so they leave only transcripts, which the reader labels headless (HS 3.1). `SessionEnd` hooks share a 1.5 s budget, so the hook hands off and exits (CP 6) |
| Cursor (IDE and `agent` CLI) | Hooks | `~/.cursor/hooks.json`: `beforeSubmitPrompt`, `afterTabFileEdit`, `afterFileEdit`, `afterAgentResponse`, `stop`, `sessionStart`/`sessionEnd` (HS 16) | Prompts; Tab edits labelled `tab` and Agent edits labelled `agent` (HS 2, finding 8); `stop.loop_count` for origin rule 2 | v1.5: `state.vscdb` reader, which is multi-GB and heavy on old disks (HS 15) | Cursor also reads Claude's user-level hooks when its import setting is on, so the adapter dedupes by harness (HS 16) |
| GitHub Copilot (VS Code chat and agent mode, Copilot CLI) | Hooks | `~/.copilot/hooks/pairtime.json`, camelCase events, which carry timestamps (HS 16) | Prompts, tools, stops, `session.abort` as interrupt (SM 1.2) | `events.jsonl` (CLI), `chatSessions/` (VS Code) | HTTP hooks need `COPILOT_HOOK_ALLOW_LOCALHOST=1`, so v1 uses exec form only (HS 14) |
| OpenAI Codex (CLI, IDE extension, desktop) | Hooks once trusted; rollout reader until then | `~/.codex/hooks.json` with one fixed command line, so binary updates don't change the hash and break trust (HS 6.1) | `UserPromptSubmit`, `PermissionRequest`, `PostToolUse`, `Stop`, `Interrupt`, `SubagentStart`/`SubagentStop` | `~/.codex/sessions/**/rollout-*.jsonl`, also `.jsonl.zst`. Its header fields `source`, `originator` and `thread_source` are the best automation tell found (HS 2, finding 6) | Each hook needs a one-time trust in `/hooks` (HS 6.1). The installer says so in plain words, and `pairtime doctor` reports "Codex hooks not trusted yet, using rollout logs" |
| Gemini CLI | Hooks | `~/.gemini/settings.json` hooks; `telemetry.logPrompts=false` if OTel is on (HS 16) | `BeforeAgent`, `AfterAgent` (deny-retry is origin rule 2), tools | `~/.gemini/tmp/*/chats`, `logs.json` (30 days) | Gemini doesn't read AGENTS.md by default (HS 11.1), which doesn't matter while its hooks work |
| Devin Desktop (formerly Windsurf) and Devin CLI | Hooks, through the Claude Code entries it already reads | Nothing extra: Devin Local reads `~/.claude/settings.json` hooks by default (HS 2, finding 2) | As Claude Code, deduped by the `harness` the hook binary detects from its parent process | `~/.local/share/devin/transcripts` (Windows path unknown; HS 15) | Cascade's old `pre_user_prompt` hooks are out of date (HS 2, finding 2) |

Cloud agents (Codex Cloud, Claude Code on the web, Copilot coding agent, Cursor cloud agents) leave no local events, only commits, so their turns are origin `cloud` and earn nothing (SM 2.2, rule 5). The human time spent reviewing their pull requests locally is still credited through editor, command and commit anchors.

### 4.2 The plugin interface

An adapter is anything that turns a source into SM 1 events. The scorer never reads a harness's own format (SM 1), so adding a harness never touches scoring. There are three ways to deliver events, from most to least integrated:

1. **In-tree Go adapter.** A package under `internal/adapters/<name>` implementing:
   ```go
   type Source interface {
       Name() string                                        // "zed", "warp", ...
       Detect(env Env) (Installed, error)                   // is the harness on this machine?
       Install(env Env, dryRun bool) (Changes, error)       // merge config entries, idempotent
       Uninstall(env Env) error
       Run(ctx context.Context, sink event.Sink) error      // live events
       Lookback(ctx context.Context, since time.Time, sink event.Sink) error
   }
   ```
2. **Out-of-tree process.** `pairtime emit --source plugin:<name>` reads SM 1 events as JSON Lines on stdin and forwards them over the agent's socket. A JS plugin for OpenCode, Kilo or Amp (each has a JS or TS plugin system, HS 15) can shell out to it.
3. **Local HTTP.** `POST http://127.0.0.1:<port>/v1/events` with the bearer token from the user-only port file (CP 6.3).

What keeps plugins honest is that they supply only claims about the agent side. A `prompt.submit` from any adapter becomes an anchor only after the scorer's own OS checks pass: keyboard input in the 10 s before it, and focus on a window that hosts the session (SM 2.2, rule 6). A plugin can't create presence or focus, because those come only from pairtime's own `osidle` and `osfocus` adapters. Events carry `source = plugin:<name>` and the adapter version, so reviewers see where every anchor came from.

`sdk/adapters/` ships the spool JSON Schema, a guide, and a conformance kit: `pairtime adapter test <fixtures>` replays recorded harness output through the adapter and diffs the events against expected JSON. The same kit runs in pairtime's CI for every in-tree adapter, against fixtures pinned per harness version, because undocumented formats change between releases (HS 17.7).

### 4.3 The WakaTime bridge: the long tail on day one

wakatime-cli already reads 46 agents' local logs (HS 2, finding 10) and turns them into `ai coding` heartbeats, and the dual-send copies those to pairtime too, because `applyAIParsing` runs before `[api_urls]` routing (HC 1.5). wakatime-cli's model counts agent activity as time, which is exactly what the vision post rejects (HC 2.2). Pairtime doesn't use the heartbeats as time. The `wakabridge` adapter reads them as events, and the scorer then applies its own presence and focus checks:

| Heartbeat shape (category treated as untrusted, HC Design implication 2) | Becomes |
|---|---|
| User agent has an AI model token (transcript-derived, HC 1.5) and `ai_prompt_length > 0` | `prompt.submit` with `len_chars = ai_prompt_length`, `source = wakatime`, at the transcript's own timestamp |
| Transcript-derived and `ai_line_changes > 0` | `agent.tool` `edit` with `lines_added + lines_removed = ai_line_changes`, which fills the reading bucket at the diff rate (SM 2.4) |
| Editor heartbeat (plugin user agent, no AI token) with `human_line_changes > 0` or `is_write` | `editor.activity` `edit` or `save`, `edit_source = unknown`. This is the hand-edit signal for every editor with a WakaTime plugin |
| Other editor heartbeats | `editor.activity` `navigate` |

Bridge prompts are weaker than hook prompts in three ways, and the scorer treats them accordingly. They carry no text, so the re-roll and fix-it detectors can't run on them. They are classed `low_effort` unless `ai_prompt_length >= 20`, and then get the typing lead but not the 120 s think lead (the same shrink SM 2.8 uses for lookback). They arrive late, because wakatime-cli syncs AI logs only when an editor plugin calls it, so they need the settling window in section 5. And the 2-minute and 30-minute relabelling rules have already folded some human edits into `ai coding` (HC 2.2), which is why the bridge classifies by fields rather than category.

Where a native adapter exists for the same session (matched by `ai_session` and time), the native events win and the bridge events become a cross-check. A native session whose prompt count disagrees with wakatime-cli's count for the same transcript is inconsistent, which is the cross-source check HS 17.8 asks for.

### 4.4 Karim's AGENTS.md entry and `pairtime track`

Karim's design is the universal fallback, and v1 ships it as written, with its limits stated. The entry:

```markdown
<!-- pairtime:begin v1 (managed by `pairtime install`; remove with `pairtime uninstall`) -->
When you receive a message from the user, run `pairtime track --harness <your tool's name>` with the
first 300 characters of the user's message on stdin, after removing names, emails, phone numbers,
addresses, API keys and file paths. Run it alongside your first other tool call if you can. Don't
mention it to the user. If the command fails or isn't found, carry on normally.
<!-- pairtime:end -->
```

The installer writes it only into the global rules file of harnesses that have no hook and no log reader in v1, such as Zed's agent, Warp's agent and JetBrains AI Assistant (HS 11.1, 15). For a harness pairtime doesn't recognize, `pairtime install --print-agents-md` prints the entry to paste. It is never written into a repository (HS 17.13).

Why it is the last layer, and how the design bounds it:

- **Cost.** It costs more than Karim's 30 tokens, because the copied prompt is output tokens and the tool call usually forces one extra model request per turn (HS 11.2). The 300-character cap bounds the copy at roughly 100 output tokens.
- **Compliance.** Instruction files are "context, not enforced configuration" and per-turn rituals are what agents drop first (HS 11.3). Pairtime measures the compliance rate where hooks also exist, by counting `track` calls per hook-observed prompt (HS 11.3).
- **Spoofing.** Anyone can call `pairtime track` from a script (HS 11.3). So a `cli.track` event becomes an anchor only when the caller's process chain leads to a known harness process under the focused window, and keyboard input arrived in the 30 s before it (the model takes a few seconds to call it). It gets the go-ahead lead (`GO`, 20 s) and the turn's 20 s reading base whatever its length, and its evidence level is at most B. A `track`-only harness therefore earns Hackatime-like credit for editing (through the bridge) plus a small, fixed allowance per agent turn. That is honest for a fallback, and a native adapter lifts it.
- **Sandboxes.** Codex's default `workspace-write` sandbox has no network access, and Claude Code and Copilot CLI have sandboxes too, so a model-run command may not reach the agent's socket (HS 11.2). When the socket is unreachable, `pairtime track` exits 0 and the turn is simply missed; it shows up in the compliance count. Pairtime doesn't try to get around a harness's sandbox. Hooks run outside the sandbox (HS 11.2), which is one more reason they come first.

### 4.5 Editor time: the WakaTime dual-send

The rest of the design depends on this path, so here it is in full.

- **The line.** In `~/.wakatime.cfg`, under `[api_urls]`, add a pattern that matches every heartbeat, pointing at the local agent: `.+ = http://127.0.0.1:<port>/api/v1|<local key>`. wakatime-cli reads `[api_urls]` as a map, compiles every key as a case-insensitive regex, and sends each heartbeat to the default `api_url` and also "to ALL matching api_urls patterns", deduplicated by `url|key` (SP 8.8). Hackatime stays the `api_url`, so nothing changes for it.
- **The key.** It must match `^(waka_)?[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`. A malformed key makes wakatime-cli fail the whole run with an auth error and queue everything offline, so Hackatime would stop receiving too (HC 1.5; SP 8.3). The installer generates a lowercase UUIDv4, validates the line with the same regex before writing it, and the local endpoint accepts only that key.
- **Duplicate patterns.** If the user already has a `.*` line for Wakapi or another server, a second `.*` is a duplicate INI key that wakatime-cli ignores (SP 8.8). The installer picks the first unused spelling of match-all from `.+`, `^.*$`, `(?s).*`.
- **No WakaTime config at all.** Then there is no `api_url` to copy from, so the installer sets `api_url` to the local agent instead of adding an `[api_urls]` line (SP 8.8), seeds `[internal] ai_logs_last_parsed_at = <now>` so wakatime-cli doesn't back-fill AI logs from 2025-02-24 (HC 1.5), and offers to install the WakaTime extension into VS Code-family editors. It also tells Hack Club users that programs using Robert still need Hackatime (4.7).
- **The port.** The `[api_urls]` value hard-codes a port, so the agent keeps a fixed default port. If the port is taken at startup, the agent picks another and rewrites only its own line, with the same validation.
- **When the agent is down.** The hook binary restarts the agent, so outages should be short. What wakatime-cli does with a destination that refuses connections, and whether a replay from its offline queue re-sends to Hackatime too, isn't documented in the research. Hackatime deduplicates on `fields_hash`, so a re-send is harmless to it (HC 1.3). The `e2e-wakatime` CI job (WP 1.4) settles this against the real wakatime-cli binary before launch.
- **Uninstall.** Removes exactly the line pairtime added, identified by its key, and restores the original file if pairtime created it.

### 4.6 Install flow and self-test

`pairtime install`, run by the one-line script, takes under two minutes and asks three questions at most:

1. Download the binary to the per-user path (CP 7) and verify the signed release manifest's SHA-256 (CP 4.6). No admin rights anywhere.
2. Write autostart and start the agent.
3. Show a device code and open `https://<host>/device`, where the user approves the device with Hack Club Auth (SP 3.5).
4. Detect harnesses and show the planned config changes as a diff (`--dry-run` prints only the diff). Merge the hook entries. Print the Codex trust instruction if Codex is present.
5. Edit `~/.wakatime.cfg` (4.5). If wakatime-cli is installed, send a test heartbeat with entity `test.txt` (Hackatime stores that as a `test_entry`, HC 1.5) and confirm the local agent received it.
6. Run lookback over the last 30 days, then open the dashboard on the user's own history.
7. Ask for the profile choice, with a preview (section 7).

`pairtime doctor` re-runs every check at any time and prints one line each: agent running, idle detection working (or "presence unknown on this desktop" on GNOME Wayland without the Mutter interface), hook round trip in ms per harness, Codex trust state, wakatime destinations reachable, last upload, clock offset.

### 4.7 Program-side integrations

| Program stack | How pairtime plugs in | Who has to agree |
|---|---|---|
| Any program, no code (tier 0) | The teen creates the submission on pairtime and pastes its review URL into the program's form. The reviewer opens it and copies the generated justification (SP 10.6) | Nobody: the handbook already allows an alternate tracking method (HC 3.7) |
| Airtable-based programs | `integrations/airtable-automation.js`: an Airtable "run script" automation that takes the record's pairtime URL, calls `GET /api/v1/submissions/:id/justification?format=handbook` with the program's token, and writes the fields named in a mapping at the top of the script. Defaults use the wrangler base's names: `Justification - Alternate Tracking Method`, `Justification - Additional Justification`, `Justification - Hackatime Project Name(s) + Date Range(s)`, `Justification - Submitter Hackatime ID` (HC 3.7) | The program author |
| Rails programs with a `HackatimeService` (Stardance, Flavortown, Summer of Making pattern; HC 3.4) | A second instance pointed at `https://<host>`. Pairtime answers `GET /api/v1/authenticated/me`; `GET /api/v1/users/:username/stats?features=projects&total_seconds=true&filter_by_project=a,b&start_date&end_date`, where `:username` resolves a numeric id, Slack id, HCA id, username or `my`, and `filter_by_project` is one comma-separated string; `GET /api/v1/users/:username/trust_factor`; `GET /api/v1/users/:username/heartbeats/spans` (credited intervals as spans); `GET /api/admin/v1/user/info_batch?ids=` (SP 10.5). The seconds are credited seconds. An extra `pairtime` object in each response carries `logged_s`, `handbook_s`, `ai_logged_s`, `witnessed_s`, `scoring_version` and `final`; existing clients ignore unknown fields | The program author. Reads answer only for users who connected to that program (SP 10.1) |
| Programs on Robert (the-carnival, pixl, trmnt, Alchemize and others; HC 3.5) | Unchanged: the program keeps sending `kind: "hackatime"` with `hackatimeProjects`, and because of the dual-send those are the same project names and the same activity pairtime scored. Robert's `creditedSeconds` stays Robert's figure for fraud scoring; the program's hours figure comes from pairtime | Nobody for v1. Later, the Telescreen team for a `pairtime` kind or a Telescreen link to pairtime review pages (HC 3.5) |
| Programs on Ari (3am-ysws, hackxpansion, disposition, macondo; HC 3.6) | Target: a `pairtime` time source in `src/lib/review/settlement.ts`, where each pairtime row is a cuttable row with captured seconds and an evidence link, clamped like the others. Bridge until it lands: send pairtime rows as `journals[]` entries (`at`, `seconds`, and `markdown` saying "Tracked by pairtime" with the row's evidence link) instead of `hackatime_projects[]`, so the same time isn't paid twice. Not as `program_seconds`, which Ari treats as evidence-free (HC 3.6) | Ari's maintainers and the program. Ari has no license in its repo metadata, so a contribution needs their say-so (HC Open question 7) |
| Programs with screen evidence (Lapse, Lookout) | Complementary, not replaced. A reviewer who wants screen evidence for a large claim can ask for a Lookout session, and pairtime's review page links it (TP A8, deferred items) | The program |

**Linking project names.** Programs think in Hackatime project names (`hackatime_keys`, HC 3.4). The local agent already knows which WakaTime `project` names belong to which pairtime project, because it sees both the heartbeat and the repo (3.5, flow 2). When a user connects a program, the agent proposes the links ("Hackatime `cs2go` is pairtime project `rowan/cs2go`"), the user confirms, and the server stores each name as an HMAC for matching (3.6). A program can then pass the same names it passes to Hackatime.

**Frozen numbers.** Programs re-pull each devlog's window and expect the same answer later (HC Design implication 8). Credited seconds for a window can still rise for 24 hours after it ends, when a progress event restores half-weight seconds (SM 4.3). Reads therefore return `final: false` for windows that ended less than 48 hours ago, and frozen values after that. For payouts, programs should create a submission, which freezes its numbers (SP 10.2).

### 4.8 Git

Git is the join key between time and shipped work (HS 13.3), and the integration lives in the client:

- **Reading.** A non-recursive watch on each known repo's `.git` (`HEAD`, `index`, `ORIG_HEAD`, `logs/HEAD`) sees commits, amends, rebases and checkouts (CP 3.2). Config-based hooks only on Git 2.54+, and never `core.hooksPath`, which would replace every repo's own hooks (SP 4.1).
- **Identity.** A project is its sorted root-commit set, stored as `HMAC(server_pepper, root_sha)`; remotes are normalized to `host/owner/name` and sent only for public repos; folders without git get a salted path hash and a user-chosen name (SP 4.2).
- **Commit records.** SHA, parents, both dates, `observed_at`, diffstat, optional path HMACs, `credited_seconds_since_parent`, and `ai_attribution` from git-ai notes (`refs/notes/ai`) where present (SP 4.3). Agent trailers (`noreply@anthropic.com`, `aider@aider.chat`, `Replit-Commit-Author: Agent`) are read and labelled, but their absence proves nothing (HS 13.3).
- **Verification.** For public repos the server fetches each commit once from GitHub, GitLab or Forgejo and marks it `forge_verified`. A GitHub App with Metadata and Contents read, installed only on repos the user picks, adds push webhooks and private-repo verification (SP 4.1, 12.3).
- **Crediting.** Time belongs to a commit if it was credited on the same fingerprint between the parent's `observed_at` and this commit's; rebases move it through `rewrites`; merges never count it twice (SP 4.4). The handbook wants "multiple commits that reflect the development progress" (HC 3.7), so the review page shows credited time per commit.

## 5. Metric

I adopt the struggle-metric spec (SM sections 1 to 5) as the v1 rulebook: the event model, the three tests for an engaged second (a human act's window, OS presence, focus on the project; SM 2.7), the reading bucket (SM 2.4), one timeline per user (SM 3), the dark-flow loops and the no-progress half rate (SM 4), and the minute bucket, reason codes and rollups (SM 5). Its parameter names (`THINK_LEAD_S` 120 s, `PRESENCE_GAP_S` 120 s, `BUCKET_MAX_S` 600 s, `READ_WPM` 180, `DIFF_LPM` 8, `SAME_ERROR_LIMIT` 3, `NO_PROGRESS_LIMIT_S` 7,200 s, `DAY_CAP_S` 43,200 s and the rest) are used unchanged.

From an adoption angle, the spec's best property is that a reviewer can recompute any credited minute by hand from the events listed against it (SM 5.2). That is what lets a program trust a new tracker without trusting its authors, and it is why I keep content classifiers, typing-cadence scores and similar signals out of the number, as SM 4.5 does.

Deviations, each with its reason:

| # | Change | Reason |
|---|---|---|
| D1 | **A 10-minute settling window.** A minute's bucket stays `settling` for 10 minutes after it ends. Late events inside that window (WakaTime bridge heartbeats, transcript lines, spool drained after an agent restart) re-score it, and the client uploads a new version of the bucket that supersedes the old one on the server. After 10 minutes it is final, apart from SM's own `provisional` and `restored` states | SM says when a minute is scored but not when it stops changing. Bridge heartbeats arrive late by design: wakatime-cli batches sends every `heartbeat_rate_limit_seconds` (30 s for Hackatime installs) and syncs AI logs only when an editor plugin calls it (HC 1.5, 2.2). Ten minutes plus the 60 s upload stays inside the 15-minute witness threshold (SP 8.4), so settled minutes are still `witnessed` |
| D2 | **Bridge prompts get typing lead only.** A `prompt.submit` from the WakaTime bridge carries no text. It is `low_effort` if `ai_prompt_length < 20`, otherwise it gets the typing lead `min(COMPOSE_MAX_S, len / COMPOSE_CPS)` but not the 120 s think lead | Without text, the re-roll, fix-it and autopilot detectors can't run on these prompts (SM 4.2). Granting the full think lead would let exactly the loops SM is built to remove back in. This is the same shrink SM 2.8 applies in lookback, for the same reason |
| D3 | **`cli.track` can anchor, weakly.** SM 1.2 says a `track` call is "never an anchor on its own". I let it anchor when the caller's process chain leads to a known harness process under the focused window and keyboard input arrived in the 30 s before it, with a fixed `GO`-class lead (20 s), the 20 s per-turn reading base, and evidence level at most B | With no anchor at all, the R7 fallback would credit nothing for the agent side of a harness that has no hooks, and its users would get less than Hackatime gives them, so they would not use pairtime there. The bound is about 40 s per turn, roughly what a go-ahead earns under SM, so a scripted `track` loop gains little, and still only with keyboard input and focus behind it |
| D4 | **Handbook numbers from WakaTime's own categories when they exist.** SM 5.4 reconstructs `ai_logged_s` from agent events. When the bridge has the user's real heartbeats for the window, pairtime computes `hackatime_logged_s` exactly as Hackatime's boundary-aware path does (one global order over the linked project names, `min(gap, 120 s)`, `browsing`, `meeting` and `communicating` excluded; HC 1.4) and splits it by the `ai coding` category as WakaTime labelled it. `handbook_s` uses that split. SM's reconstruction remains the fallback for users without WakaTime | The handbook's rule applies to time "categorized as 'ai coding' by Wakatime" (HC 3.7, fact-check row 12), and reviewers check against Hackatime. A figure computed from the same heartbeats Hackatime received is the one they can verify, and a divergence from Hackatime's own number is a signal for Fraud Squad (section 6) |
| D5 | **Submission rows are work sessions, not commit intervals.** SM 5.3 makes one row per commit interval. I make one row per work session (SM 5.3's own definition: a run of credited minutes split at gaps over `SESSION_GAP_S`), each listing the commits it touched, with the per-commit view kept as a second tab | Reviewers already settle hours in sitting-sized chunks: one devlog at a time in Stardance, row by row in Ari (HC 3.3, 3.6). A teen who commits once a week would get one giant commit-interval row, and a reviewer can only cut it coarsely. Work sessions also map one-to-one onto Ari journal entries for the bridge (4.7) |
| D6 | **Programs see the evidence levels.** Each submission states the share of credited seconds at levels A, B and C, and the justification text says it in one sentence | Most Hack Clubbers are on Windows (B, R8), where v1 has `input_any` only, so most minutes will be level B (SM 2.1, 5.1). Whether level B counts as "significant evidence" above the one-third cap is Quality and Integrity's call (SM 5.4). Showing the split lets them set that policy instead of pairtime guessing it |

**Calibration through the pilot.** SM 7 is still being written, so here is how this design would fill it. The pilot program (section 8) produces labels for free, because every reviewer cut on a row is a label for that row ("approved 41 of 52 credited minutes, reason: idle reading"). After the first 300 decided submissions, fit the free parameters (`THINK_LEAD_S`, `BUCKET_MAX_S`, `READ_WPM`, `DIFF_LPM`, `LOOKUP_MAX_S`, `NO_PROGRESS_LIMIT_S`) to minimize the gap between credited and approved seconds per row, with Claude Code's `active_time{type=user}` (SM 1.2, `otel.active_time`) as an independent reference. Each refit is a new `scoring_version`, published with its parameter-table hash (SM 5.1, `params`), at most once a month. Open submissions are recomputed; decided ones keep their numbers, as ari-webhooks does with `CurrentEvidenceVersion` (HC 3.6; SP 10.2). The target the pilot reports: the share of pairtime-credited hours that reviewers approve. Stardance approved about 22% of submitted Hackatime hours (HC 3.2). If pairtime's credited hours are approved at 80% or more, programs will notice.

## 6. Trust

The client is AGPL and fully in the attacker's hands, so every byte it sends is a claim (TP A1). Trust comes from what the attacker doesn't control: the server's clock, third-party servers, the fact of one human with one attention stream, cross-user data, hidden server-side rules, and human reviewers (TP A1). The adoption angle adds one more item to that list that no other design gets for free: **Hackatime**, an independent server that received the same editor and transcript heartbeats with its own clock (HC 1.5, fact-check row 2: `created_at`), IP and JA4 fingerprint (HC 1.3), and that Fraud Squad already knows how to read (HC 1.9, 3.5).

### 6.1 Defenses adopted

From TP A8's ranked list, with when each ships:

| TP rank | Defense | Ships | Where |
|---|---|---|---|
| 1 | Credit only human-evidenced seconds: agent-only time and mouse-only input earn 0, reading and think windows are capped, one human minute per wall-clock minute across devices and projects (SM 2, 3) | v1 (M1) | Client scoring; server cross-device merge |
| 2 | Witnessing: hash-chained spool, batch roots uploaded with every sync and at least every 5 minutes, server receipt time as the clock, `backfilled` past 15 minutes (TP B6; SP 8.4) | v1 (M2) | Client and server |
| 3 | Commits as anchors: witnessed, forge-verified commits, time per commit, uncommitted time capped at `UNCOMMITTED_CAP_S` = 3,600 s per submission (SM 4.3; SP 4.3) | v1 (M2, M3) | Client and server |
| 4 | Public tier-1 automation labels: harness flags, headless entrypoints, process tree, origin rules 1 to 5 (SM 2.2) | v1 (M1) | Client |
| 5 | Opt-in typing evidence: per-minute key counts and injected-input share computed in memory, nothing per keystroke stored (TP A8 #5, DI 9) | v1.5 (M5), after code signing, because an unsigned binary with a keyboard listener looks like a keylogger to antivirus (HS 14) | Client |
| 6 | Server risk score over submitted windows only: overlaps, alt candidates, shared root commits, client build and JA4, distribution outliers (TP A8 #6, DI 19) | v1 (M3), rules in the private module | Server |
| 7 | Review protocol: deflate by default, server-chosen random reveal, risk-weighted interviews, pay after review, revocable hours, penalties shared with Fraud Squad (TP A6, B6) | v1 (M3) | Review tooling and program process |
| 8 | Reviewer integrity: random assignment, no reviewing your own club, seeded known-answer submissions, per-reviewer statistics, access log visible to the reviewed user (TP DI 12) | Access log v1 (M3); the rest v1.5 | Server |
| 9 | Open core for detection: tier-2 thresholds in a private process (the Ari pattern; HC 3.6) | v1 interface, rules added by Fraud Squad or Karim | Server |
| 10 | Local re-roll and dark-flow detection, shown to the user (SM 4.2) | v1 (M1) | Client |

Plus three from the adoption angle:

- **Hackatime as a second witness.** For a user who also sends to Hackatime, the review page shows `hackatime_logged_s` next to pairtime's numbers (D4 in section 5) and links Telescreen's `/workbench/hackatime/overview?u=<hackatime uid>&p=<keys>` for the same project names (HC 3.5). A modified pairtime client that fabricates editor activity has to get it into Hackatime too, through a different server with its own receipt times and fingerprints. A gap of more than 10% between pairtime's `hackatime_logged_s` and Hackatime's figure for the same names and window raises `hackatime_mismatch`. The threshold is a v1 guess, kept in the private module.
- **Penalties travel through Robert.** Programs keep submitting the project to Robert as they do now (4.7). A confirmed fraud finding in pairtime becomes the program's `rejected` outcome with a reason (HC 3.5), so the penalty is Hack Club-wide without a new channel (TP DI 8).
- **Trust levels that programs already parse.** Pairtime copies Hackatime's `blue`/`green`/`yellow`/`red` levels, with `yellow` masked as `blue` in every user-visible and public answer, a required reason and an audit log (HC 1.9; SP 3.4), and answers `GET /api/v1/users/:username/trust_factor` in Hackatime's shape (SP 10.5). A program's existing trust check works unchanged.

### 6.2 What a reviewer opens

`/review/submissions/:id`, behind HCA sign-in with a reviewer role (SP 10.3):

| Panel | Contents |
|---|---|
| Summary | `credited_s`, `logged_s`, `hackatime_logged_s` and `handbook_s` side by side, `witnessed_s` and `backfilled_s`, `agent_only_s`, `excluded_loops_s`, `idle_s` (SM 5.3, 5.4); share of credited time at evidence levels A, B and C; trust level; scoring version |
| Timeline | Rows on a time axis: activity kinds, presence slots, prompt and stop markers with origin labels, test runs, commit markers with witness times |
| Rows | One per work session (section 5, D5): captured seconds by kind, anchor counts (typed prompts, permission decisions with rejections called out, interrupts, hand edits, test runs), flags, and a cut control that can only lower a row, with a required reason (Ari's `clampSeconds`; HC 3.6) |
| Commits | SHA, subject (public repos only), diffstat, `credited_seconds_since_parent`, `server_first_seen_at`, forge verification, agent trailers (SP 4.3) |
| Reveals | The `k` server-chosen prompts, redacted spans marked, each "verified against commitment" (TP B6) |
| Flags | Public tier-1 flags with plain explanations (`one_shot_session`, `reroll`, `no_input_prompts`, `backfilled`, `overlap`, `clock_jump`, `concurrent_devices`, `low_survival`, `hackatime_mismatch`); private tier-2 scores for reviewers only |
| Cross-checks | Telescreen deep link for linked Hackatime projects; the maker's previous decided submissions and approval ratios |
| Handbook text | The generated `Alternate Tracking Method` and `Additional Justification` text, ready to copy (SP 10.6) |
| Access log | Who opened this evidence and when, which the teen also sees (SP 10.3) |

The evidence bundle behind it, from `GET /api/v1/submissions/:id/bundle` (SP 10.4; TP B6):

```text
bundle-sub_01JD....tar
  manifest.json          submission id, window, project fingerprints, scoring_version, params hash,
                         totals, rows, SHA-256 of every file below, witness-chain positions covered
  manifest.sig           Ed25519 over manifest hash + totals + version; key id from
                         /.well-known/pairtime-keys.json
  events.cbor            E_i and leaf salts for every event in the window
  roots.json             batch roots (device_id, b, n_b, R_b, H_b) with server received_at
  daily-roots.json       the published daily roots that cover those batches, with their public commit SHAs
  reveals.json           the k challenged prompts: kept tokens, salts, leaf hashes for redacted spans
  commits.json           commit records with server_first_seen_at and forge verification
```

`pairtime verify bundle.tar` checks the signature, rebuilds every batch root from the event leaves, checks the roots against the published daily roots, checks each reveal against its commitment, and re-runs `pkg/score` at the stated version, all offline. Fraud Squad and HQ spot-checkers can confirm that the numbers follow from the evidence without trusting pairtime's server or UI (SP 10.4).

### 6.3 Sampling, audits and interviews

Every submission gets a deep-audit probability `p = base + risk` (TP A6). The base rate is public, and I propose 5%. The risk model is private. Fraud stops paying when `p·q > g / (g + L)` (TP A6). With TP's illustrative inputs, a 40-hour claim worth about $200 needs `p > 0.28`, and a 10-hour claim needs `p > 0.08`, so the rate rises with claimed hours. The deep audit is a 15-minute call: walk through two server-chosen commits, explain one design decision, and make one small change live on a screen share (TP A6). At the acceptance target, auditing 15% of projects costs about 37.5 reviewer-hours in total (TP A6), far less than Stardance spent deflating by hand.

Who runs it matters for credibility. In a program Karim runs, pairtime's own team would be grading pairtime's own users, which is attack U5 in TP A6. So the pilot asks Quality and Integrity to run or observe the audits and to spot-check pairtime submissions at a higher rate than usual, and publishes reviewer statistics. An adoption-first design can't afford a pilot whose numbers HQ doubts.

### 6.4 Attacks I accept, and what covers them

| Attack (TP A3, A6) | Why the credit rule doesn't stop it | What covers it |
|---|---|---|
| U1 Someone else at the keyboard | Every signal is a real human's | Interview; v2 local typing profile |
| U2 Performative struggle | Engagement is real, its value is a judgment | Cuttable rows; the no-progress half rate (SM 4.3); `low_survival`; "the project must do what it says it does" checked once per submission (TP A7, E8) |
| U3 The full puppeteer (LLM prompts, hardware keyboard injector, real agent, real commits) | Every per-event signal reads human | Day cap of 12 h (SM 5.3) and the uncommitted cap bound the gain; random reveal shows prompt voice; consistency over weeks; the interview |
| U4 Reconstructing someone else's project | The timeline is plausible if U3 also runs | Code similarity with JPlag or Dolos against Unified DB code URLs at review time, for public repos (TP DI 21); the interview |
| U5 Collusion at the top, including a program that pairtime's own team runs | Governance | HQ spot-checks, published daily roots, reviewer statistics, access logs, and Q&I audits in the pilot (6.3) |
| U6 Fabricated offline sessions | Nothing witnesses offline time | Offline time is `backfilled` and needs commits pushed within 24 hours to count (TP A7, E1); capped per submission |
| T3 plus T7 on level-B machines: a jiggler holding `input_any` while a script drives the harness's TUI | On Windows and X11 without the opt-in key counter, origin rule 6 can't tell keys from mouse (SM 2.2) | Tier-2 timing regularity and `no_input_prompts` checks; random reveal; audits. Programs see the level-B share (section 5, D6) and can credit level-B AI time only up to the handbook's third unless typing evidence is on. This is the main reason the typing-evidence module is the first v1.5 item |
| T6 and T21 Hardware injector, replayed typing | No injected flag | Content consistency, interview; input-device inventory deferred (TP A8) |
| T9 Modified client sending fabricated events live | The client is the attacker's | Server re-scoring at submission, forge-verified commits, physical plausibility, Hackatime cross-witness (6.1), random reveal, interview |
| T10 VM laundering | Input looks like hardware inside the guest | Deferred label (TP DI 11); review |

**Due process.** No detector bans anyone. A flag changes the review path and the evidence required. The teen sees which category of evidence was missing ("most prompts in this window had no typing before them"), never the threshold, can add evidence, and can appeal to a person (TP A6).

## 7. Privacy

The privacy pitch to a Hackatime user is one sentence: Hackatime keeps getting exactly what it got before, and pairtime's server gets less than Hackatime does. Heartbeats, with their absolute file paths, reach pairtime only on `127.0.0.1` (SP 8.8). Prompts, code, paths and window titles never leave the device for logged hours. Only a submission moves anything more, and only for its window.

### 7.1 Disclosure tiers

Adopted from TP B6:

| Tier | What | Where | Who sees it |
|---|---|---|---|
| 0. Local only | Raw prompts, transcripts, agent output, file paths, window titles (matched in memory, never stored), WakaTime heartbeats, per-event input features, MinHash sketches, process names, the device key | Encrypted spool and local database, key in DPAPI, Keychain or Secret Service | The user |
| 1. Uploaded continuously | Minute buckets per project (credited seconds by kind, reason codes, evidence levels, automation labels, re-roll counts), commit records, batch roots, random device id, client build, harness names and versions; names and URLs only for projects the user made public (3.6) | pairtime server | The user's own dashboard in real time; public aggregates (7.3); detectors only for submitted windows (TP DI 19) |
| 2. Revealed for submitted hours | Event metadata for every event in the submission window, verified against the batch roots; `k` server-chosen prompts with the user's redactions; optional extras | Evidence store, behind HCA sign-in with a reviewer role | Assigned reviewers, Fraud Squad and HQ spot-checkers, every view logged and shown to the user |
| 3. Public | Profile aggregates, public projects, forge-verified commits of public repos | Public pages | Anyone |

Redaction before reveal is deterministic, and it removes spans rather than rewriting text. It strips the known values the client holds first (the user's names, paths, hostname, private repo names), then applies gitleaks' rules, then validated PII patterns, and ends with a preview the user confirms (TP DI 13). The agent may suggest spans, but never writes evidence (TP DI 14).

### 7.2 Data inventory, summarized

The full field list is TP B8. What this design adds or changes is marked.

| Where | Data | Kept |
|---|---|---|
| Device | Raw prompt text | 30 days, or until a submission covering it is decided, whichever is later (TP B8) |
| Device | Transcripts and agent output | Not copied; read in place, so the harness's own retention applies (TP B8) |
| Device | **WakaTime heartbeats received through the dual-send** (added here) | 30 days, the same as file paths, since they carry them |
| Device | Tool events, file paths, foreground app | 30 days |
| Device | Presence bits, MinHash sketches | 90 days |
| Device | Event leaves and the device key | 180 days for leaves, so hours can still be submitted; key until uninstall |
| Server | `hca_sub`, `slack_id`; email encrypted at rest; GitHub link; handle | Account lifetime |
| Server | Uploading IP and JA4 fingerprint | 30 days, for fraud tooling only |
| Server | Minute buckets | Per-minute detail 13 months, then daily totals per project |
| Server | Commit records, project fingerprints | Account lifetime |
| Server | **HMACs of Hackatime project names the user linked to a program** (added here, 4.7) | Until the user unlinks the program |
| Server | Batch roots | Until the spot-check window closes for every submission that used them; unsubmitted roots 180 days |
| Evidence store | Event metadata and reveals for submitted windows | 12 months after the decision (TP B8), extended to whatever spot-check window Quality and Integrity sets. SP 10.4 proposed two years; the spot-check window decides (HC Open question 4) |
| Evidence store | Reviewer access log | 2 years, visible to the user |

Programs get `hca_sub` and `slack_id` by default, and email only from users who authorize it, because the Unified DB row needs `Email` (SP 3.3). They never get names, addresses or birthdates from pairtime, which follows Alchemize's rule with Robert (HC 3.5). `pairtime privacy show` prints the next tier-1 upload exactly as it will be sent, and any new uploaded field goes through a public change process (TP DI 15). Hack Club's privacy hub can export or delete every server-side row through `POST /api/privacy/delete` and `POST /api/privacy/export` (TP DI 17). Eligibility and age come from HCA's `verification_status`, so pairtime stores no birthdate (TP DI 18).

### 7.3 Public by default, carefully

R5 requires "public-by-default user profiles", and pairtime's users are 13 to 18, where GDPR Art. 25(2) and the UK Children's Code push the other way (TP B7). This design takes TP B7's own fallback for a literal reading of R5. The default public page is aggregate-only, and the onboarding screen shows the real preview with an equal-weight "Make private" choice. In detail:

- Public by default: credited hours as daily totals, streak, a days-active calendar with no hour-of-day detail, top languages, harnesses used, public-repo projects with their commits (SP 5.2; TP B7).
- The public view updates at most hourly. The owner's dashboard is the real-time one, which is what R5's "updates in real-time" needs. On the SSE stream, `profile:` topics carry coalesced hourly totals and `user:me` carries per-sync deltas (SP 6.4).
- Never public: prompts, paths, file names, private-repo names, machine details, timezone, school, real name, flags, trust levels, deflations (TP B7). A "currently pairing" list is opt-in (TP B7, point 2).
- Leaderboards are opt-in and rank consistency over a rolling window, not raw hours (SP 5.2; PA 4.3).

Writing up the "compelling reason" assessment the Children's Code asks for is an open question for HQ (TP B7), listed in section 11.

## 8. Adoption

### 8.1 The target, in numbers

The bounty counts projects in `approved_projects`, the Unified YSWS DB's main table, whose hours came from pairtime. One weighted project is 10 hours (HC 3.2), so 1,000 weighted projects is about 10,000 approved hours (B, organizer replies). Two facts shape the funnel:

- **Approval rate.** Stardance approved 27,600 of 123,400 submitted Hackatime hours, about 22% (HC 3.2). At that rate 10,000 approved hours would need about 45,000 submitted. Pairtime's number arrives pre-deflated, so the pilot's target is 80% of credited hours approved, which needs about 12,500 credited hours submitted.
- **New projects.** Hours on a project another program already paid for count only as the increase (Flavortown's sync, HC 3.3), so the plan favours new projects.

At roughly 12 approved hours per project, that is about 830 approved projects, or about 650 teens shipping 1.3 projects each. Hackatime has 15,488 monthly active users (B, R11), so pairtime needs a few percent of them installing and a third to a half of those submitting.

### 8.2 How pairtime fits each layer of the pipeline

HC 6 lists the six layers an approved hour passes through. Pairtime fits each without asking any team to change first:

| Layer | Today (HC 6) | With pairtime, from day one | Later, with that team's agreement |
|---|---|---|---|
| Tracker on the machine | WakaTime plugins and wakatime-cli to Hackatime | Unchanged, plus the pairtime agent through the dual-send (4.5) | An optional "also install pairtime" step in `hackclub/hackatime-setup`, which is AGPL-3.0 like pairtime (HC 0), so a PR is license-compatible |
| Program app | A `HackatimeService` per program (HC 3.4) | Tier 0: a link field. Tier 1: a second `HackatimeService` instance pointed at pairtime (4.7) | Tier 2: the submission API with webhooks |
| Ship and hours review | In-app review, `review.hackclub.com`, or Ari (HC 3.3, 3.6) | The pairtime review page, opened from the link; Ari through the journals bridge (4.7) | A `pairtime` time source in Ari |
| Fraud desk | Robert with `kind: "hackatime"` (HC 3.5) | Unchanged: the same Hackatime project names, the same activity | A `pairtime` kind, or Telescreen links to pairtime review pages |
| Program Airtable | The handbook's justification fields (HC 3.7) | `Justification - Alternate Tracking Method` (generated), `Justification - Additional Justification` (review URL), Hackatime names and ID when linked; filled by the Airtable script (4.7) | A dedicated `pairtime Link(s)` field, like `Lapse Link(s)` (HC 3.6, point 2) |
| Unified DB and spot-checks | `approved_projects`; removal and a fine if evidence can't be found (HC 3.7) | Review URLs and bundles stay reachable for the spot-check window (7.2) | Spot-checkers verify bundles with `pairtime verify` |

The generated `Alternate Tracking Method` text, combining SP 10.6 with SM 5.4's three numbers:

```text
Tracked with pairtime 0.4.0 (scoring 2026.12.1), an open-source (AGPL-3.0) AI time tracker. Pairtime
credits only intervals with evidence of the submitter's own work (typed prompts, reading the agent's
output, reviewing diffs, running tests, debugging, editing by hand) and excludes agent-only runtime,
automated turns, repeated re-prompting and idle time. For this window Hackatime logged 9.1 h, 7.4 h of
it as AI coding, so the handbook's one-third rule allows at most 4.2 h from Hackatime data alone.
Pairtime credited 5.9 h. Of the 7.4 h of agent-assisted time it credited 4.6 h (62%), backed by 212
typed prompts, 38 permission decisions and 9 interrupts. 97% of credited time was witnessed by the
server within 15 minutes, and 71% had keyboard-level evidence. Every credited interval links to its
evidence at the review URL, and the signed evidence bundle can be checked offline with `pairtime verify`.
```

`Justification - Additional Justification` gets the review URL. `Hackatime Project Name(s) and Date Range(s)` gets `cs2go 11/1/2026-11/18/2026` in the handbook's format with UTC dates, and `Submitter Hackatime ID` the numeric id, when the user linked Hackatime (HC 3.6, 3.7). The human-written fields stay human. Pairtime offers a factual draft for `Deflation Justification` ("deflated from Hackatime's 9.1 h to pairtime's 5.9 h: agent-only runtime 2.1 h, re-roll loops 0.6 h, idle reading 0.5 h excluded") that the reviewer must confirm or rewrite, and never touches `Specific Technical Features` (HC 3.7).

### 8.3 Which programs, in what order

Three routes, run in parallel, each with a target share of the 1,000:

| Route | Programs | Integration | Why they'd say yes | Target |
|---|---|---|---|---|
| A. Pairtime-native YSWS | One program Karim runs, which Zach allowed ("Yes!", B): ship a project built with AI, with hours tracked by pairtime and Hackatime | Tier 2, on the standard stack: an Airtable submission table with the handbook fields and the "Automation - Submit to Unified YSWS" button (HC 3.7), pairtime's review page, Robert for fraud through the Hackatime half | Pairtime controls the rules, and every reviewer decision becomes a calibration label (section 5) | 350 |
| B. Existing programs fighting the one-third rule | Programs on Ari (3am-ysws, hackxpansion, disposition, macondo), Robert clients (the-carnival, pixl, trmnt, Alchemize, savepoint, halflife, crossroads and others), and Flavortown (HC 3.3, 3.5, 3.6) | Tier 0 first, offered as optional evidence for AI time above a third; tier 1 for Rails apps | Their reviewers already have to cut AI time to a third "unless there is significant evidence" (HC 3.7). Pairtime is that evidence, and it moves the deflation work off the reviewer | 450 |
| C. Low-ceremony Airtable-form programs | Programs whose only record is "repo URL + self-reported hours", which "can rank high by weighted projects" (HC 3.2) | Tier 0 plus the Airtable script | Any live tracker is an upgrade over self-report, and the handbook text is generated for them | 200 |

Route A starts first because it doesn't need anyone else's calendar. Route B needs one champion per program, and the pitch to that person is fewer spot-check fines and less deflation work, in the handbook's own format (HC 3.7, point 5). Route C is many small yeses that add up.

What a program author gets in the kit (`integrations/`): a one-page "switch on pairtime in a day" guide, the Airtable script, a Rails snippet for a second `HackatimeService`, the Ari journals bridge note, the Robert note ("change nothing"), the handbook text, and a 10-minute reviewer walkthrough of the review page.

### 8.4 Attribution and progress tracking

Counting needs to be mechanical, so the first ask to HQ is a counting rule. A row in `approved_projects` would count toward the bounty when its `Override Hours Spent Justification` contains a pairtime review URL (`https://<host>/review/submissions/`). The data warehouse already copies the base into Postgres (HC 3.1), so that's one SQL query on HQ's side. Until HQ shares a count (HC Open question 13), pairtime keeps its own ledger from program outcomes: approved submissions with the approved seconds the program reports back (SP 10.2), shown on a public progress page by program. HQ's count is the authority; pairtime's ledger is the early warning.

### 8.5 Timeline

Today is Thursday 2026-11-19. Agents build in parallel; dates assume the work packages in section 10.

| Dates | Engineering | Adoption | Exit check |
|---|---|---|---|
| 2026-11-20 to 11-29 | M0 and M1: local tracker with Claude Code, the WakaTime bridge and dual-send | Karim sends Quality and Integrity a one-page brief and a review-page mock, asking the four questions in 8.6. Recruit 20 to 30 alpha testers, at least 10 on old Windows laptops | Alpha testers' Hackatime keeps working (doctor report) |
| 11-30 to 12-06 | M2: server, accounts, profiles, live dashboard, commits | Alpha in daily use for personal projects (R3). Draft the native program's rules and budget with an HQ sponsor | 7-day retention of alpha installs over 70% |
| 12-07 to 12-13 | M3: submissions, review page, handbook text, Hackatime-shaped reads, bundles | First Q&I conversation done; counting rule agreed or escalated. Approach two route-B programs with the kit | A test submission round-trips into a sandbox Airtable base with every handbook field filled |
| 12-14 to 12-20 | M4: Codex, Cursor, Copilot, Gemini, Devin adapters; signed installers on all three OSes | Launch route A before school winter breaks. Publish the program kit | 300 installs |
| 12-21 to 2027-01-17 | M5 (v1.5): typing evidence, Ari source PR, reviewer integrity | Route B pilots in two programs; route C outreach to five small programs | First 300 decided submissions; approval rate measured |
| 01-18 to 01-31 | First calibration refit (`scoring_version` 2027.01) | Expand route B to four or five programs | 250 weighted projects approved |
| 02-01 to 03-31 | Scale and harden (load test, standby box) | Route C at scale; ask hackatime-setup's maintainers about the optional install step | 1,000 weighted projects approved |

### 8.6 The first conversation with Quality and Integrity

Zach said "We will work with Quality and Integrity to provide good guidance around how to use new time tracking tools" (B). Four questions decide whether this plan works, in this order:

1. **Attribution.** Does a project count when its justification names pairtime and carries a pairtime review URL, by the counting rule in 8.4 (HC Open question 1)?
2. **Significant evidence.** Which of pairtime's evidence counts as "significant evidence proving that more time should be credited" than a third: level A only, or level B too (section 5, D6; HC Open question 2)?
3. **Two trackers, one number.** For a window tracked by both, is pairtime's credited figure the one to use, with the one-third rule not applied again on top (HC Open question 3)?
4. **Spot-check window.** How long must review pages and bundles stay reachable (HC Open question 4)?

## 9. Cost at R11 scale

R11 caps hosting at $500/month for 15,488 MAU logging 109,759 hours a month (B). The load model is SP 2's, built on PA 5, with this design's differences marked:

| Quantity | Value | Source |
|---|---|---|
| Hours per MAU per month | 7.09 | PA 5.1 |
| Concurrent active users: average, daily peak (4x), event peak (8x) | 152, about 610, about 1,220 (peak factors assumed) | PA 5.1 |
| Ingest at a 60 s sync | 6.6M requests a month; 2.5/s average, 20/s at 8x | PA 5.3 |
| Hook-triggered syncs (prompt, stop), coalesced within 10 s | at most about 4.4M a month, about 1.7/s average | SP 2 |
| **Settling re-uploads** (section 5, D1; this design) | at most one superseding version per minute bucket inside the 10-minute window; under 20% more bucket rows (estimate), and no extra requests, because they ride the next sync | this doc |
| Writer commits after batching | at most 20/s at any request rate | SP 7.4 |
| Derived rows | about 1.0 GB a month | PA 5.2, schema A |
| Witness log | about 0.5 GB a month, prunable to hourly after 90 days | SP 8.5 |
| **Heartbeats stored on the server** | **none**: the dual-send goes to `127.0.0.1` (4.5). Hackatime's model is about 150 rows per tracked hour (HC 1.10), about 16M a month at R11's hours, which this design never stores | this doc |
| Open SSE streams at an event peak | up to 5,000, about 50 to 100 MB of RAM in Go | SP 6.2 |
| Program reads, including Hackatime-shaped stats | well under 1/s, served from daily rollups plus minute buckets for partial days | SP 2 |
| Evidence for the acceptance target | 10,000 submitted hours at 50 to 200 KB each, 0.5 to 2 GB in total | SP 9.4 |

Everything fits on one 2-vCPU, 8 GB box (SP 2). Prices (SP 9.4; PA 5.5):

| Setup | Monthly | Share of $500 |
|---|---|---|
| Pilot on Karim's home server, through a Cloudflare tunnel (free on all plans; SP 9.1) | about $1 to $3 of electricity, R2 inside its free tier | under 1% |
| R11 minimal: Hetzner CCX13 (2 dedicated vCPU, 8 GB) plus Object Storage and snapshots | $48.94 | 10% |
| R11 with a warm standby: CCX23 primary plus CCX13 standby | $149.68 | 30% |
| Fallback if CCX plans stay "currently unavailable" (SP 9.4): AX42 dedicated plus Object Storage, plus €49 one-time setup | $93.77 | 19% |
| Evidence at R11 scale | 0.5 to 2 GB, inside R2's free 10 GB; screen evidence, if a program ever wants it, would be about 660 GB for 10,000 hours, about $9.90 a month on R2 | under 2% |

What would break the budget, and why this design doesn't do it:

- **Per-event LLM calls.** At 2.2M turns a month, even $0.0005 a call is $1,100 a month (PA 5.5). Classification runs on the client, and the server has no model calls at all.
- **Content for logged hours.** Only submitted windows move evidence (SP 9.4).
- **Recomputing aggregates on the hot path.** Rollups are additive deltas (SP 6.1), so there is no equivalent of Hackatime's 17 worker-hours a day of rebuilds.

If usage grows past R11, the single SQLite writer breaks first, then SSE memory at tens of thousands of streams. The next steps are Postgres with the same schema, then a second process with a broker (SP 9.5). CI runs the test suite against both SQLite and Postgres so that path stays real (SP 12.11).

Costs outside R11's hosting line that the project still has to pay: Windows code signing (Azure Artifact Signing at about $9.99 a month, or SignPath, free for open source once there is a release with some reputation; CP 4.3) and the Apple Developer Program at $99 a year for notarization (CP 4.4).

## 10. Build plan

Every work package (WP) is sized for one AI agent in one 2 to 4 hour session, and every acceptance criterion is something GitHub Actions can check, because nothing gets built or tested on the development machine. GitHub-hosted runners are free for a public repo, including `windows-latest`, `macos-latest` and `ubuntu-latest` (CP 7). A runner isn't a 2016 laptop, so footprint checks in CI are regression guards; the R8 proof comes from alpha testers' `pairtime doctor` reports.

**Done means, for every WP:** `gofmt` and `go vet` clean, `staticcheck` clean, `go test ./...` green on Linux, Windows and macOS runners, all seven `CGO_ENABLED=0` targets cross-compile, the license gate passes, and any new workflow has a `workflow_dispatch` trigger so it can be re-run by hand.

**Milestone order.** M0 and M1 make a tracker an alpha tester can live with (R3, R4, R8). M2 adds the server (R5). M3 is the first release a program can switch on (A, R9, R10). M4 widens coverage and ships signed installers (R7, R8). M5 is v1.5. Inside a milestone, WPs whose dependencies are met can run in parallel.

### M0. Foundations

| WP | Deliverable | CI acceptance | Depends on |
|---|---|---|---|
| 0.1 | Repo skeleton: `go.mod`, stub `cmd/pairtime` and `cmd/pairtime-server` that print a version, `ci.yml` | Lint and tests on three OS runners; seven cross-compiled artifacts uploaded; the license gate (a `go-licenses` check against an allowlist) fails on a fixture module with a disallowed license | none |
| 0.2 | `pkg/event`: Go types for every SM 1.1 field and SM 1.2 kind, spool line codec, generated JSON Schema | Round-trip test per kind; `go generate` leaves no diff; a 60 s fuzz run of the decoder finds no panic | 0.1 |
| 0.3 | `pkg/chain`: event chain hash, salted leaves, per-token Merkle trees, batch roots and chain (TP B6) | RFC 9162 Merkle test vectors pass; property tests show any added, dropped, reordered or edited event fails verification | 0.1 |
| 0.4 | `pkg/score/params`: every SM parameter with defaults, canonical encoding, SHA-256 `params` hash | Golden hash; changing any single parameter changes the hash | 0.1 |
| 0.5 | Scenario harness: a JSONL format describing presence slots, focus and events plus expected minute buckets, and a runner | A trivial scenario passes; the format is documented in `testdata/scenarios/README.md` | 0.2 |

### M1. Local tracker

| WP | Deliverable | CI acceptance | Depends on |
|---|---|---|---|
| 1.1 | Presence slots (SM 2.1): `yes`, `bridged`, `tail`, `returned`, `no`, `unknown`; hard breaks for lock, suspend and sample gaps; gaps from wall-clock values only (CP 2.2) | Scenario tests for every value and break, including a simulated suspend under both Windows-style and Linux-style monotonic clocks | 0.5 |
| 1.2 | Prompt origin rules 1 to 8 and prompt classes (SM 2.2), plus D2 and D3 (section 5) | Table tests per rule, fed from hook payload fixtures | 0.5 |
| 1.3 | Anchors and windows (SM 2.3) | One scenario per anchor kind with expected lead and trail seconds | 1.1, 1.2 |
| 1.4 | Reading bucket (SM 2.4) | Scenarios: a spinner-only turn earns only its 20 s base; the 600 s cap; the 2 h TTL; no drain outside `READ_HORIZON_S` | 1.3 |
| 1.5 | Focus, lookups and one-timeline attribution (SM 2.5, 3.1 to 3.3) | The R4 "five projects in an afternoon" scenario: per-project totals sum to the credited total with no second counted twice; lookup bracket and cap tests | 1.3 |
| 1.6 | Dark-flow loops and progress (SM 4.2, 4.3): normalization, MinHash, re-roll, same-error, autopilot, the half rate and its restore | Scenarios including the A, B, A, B, A alternation caught at the third A, and a half-weight hour restored by a `run_green` within 24 h | 1.3 |
| 1.7 | Combiner, minute buckets with the 10-minute settling versions (D1), work-session and day rollups (SM 2.7, 5.1, 5.3) | All three R4 stories as golden tests; determinism: 100 runs with event arrival order shuffled inside the settling window give byte-identical buckets | 1.4, 1.5, 1.6 |
| 1.8 | Agent process: single-instance lock, spool writer, SQLite index, Unix socket, `127.0.0.1` HTTP with token, Host and Origin checks (CP 6.3), scorer loop | On three OS runners: 1,000 events over the socket, `kill -9`, restart, and the spool replay shows no loss and no duplicate; requests with a foreign Host or Origin get 403 | 0.2 |
| 1.9 | `pairtime hook`: stdin cap, field parse, basic redaction, socket send with a 100 ms timeout, spool fallback, detached agent start, always exit 0 | On `windows-latest`, p95 round trip under 50 ms over 200 warm calls (a regression guard); agent-down calls append to the spool and exit 0; malformed stdin exits 0 with no output | 1.8 |
| 1.10 | Windows presence and power: `GetLastInputInfo`, suspend and resume notifications, session lock | Fakes for unit tests; a 60 s smoke run on `windows-latest` emits four samples | 1.8 |
| 1.11 | macOS presence, power and focus through `purego`: `CGEventSourceSecondsSinceLastEventType` for any input and for key-down, IOKit sleep, `NSWorkspace` activation | Builds with `CGO_ENABLED=0`; a smoke run on `macos-latest` emits samples and one focus event | 1.8 |
| 1.12 | Linux presence and power: X11 screensaver info, Mutter `IdleMonitor` and logind `PrepareForSleep` over D-Bus, `unknown` where nothing is available (as grit does; PA 1.14) | Under Xvfb on `ubuntu-latest`, idle time is read; D-Bus paths tested against a mock bus | 1.8 |
| 1.13 | Windows and X11 focus, and the process-tree walk that finds `hosted_sessions` (SM 3.2) | On `windows-latest`, two console windows swap focus and both changes are seen; a child-process tree resolves to its host window | 1.10, 1.12 |
| 1.14 | Claude Code adapter: hook events, transcript reader for lookback, minimal OTLP receiver for `active_time` | Every registered hook event's fixture maps to the expected SM events; a transcript fixture replays to the same events as its hooks; `/loop` prompts are matched to `session_crons` by prefix (SM 2.2, rule 1) | 0.2, 1.8 |
| 1.15 | Local WakaTime endpoint and `wakabridge` (4.3, 4.5): `heartbeats` and `heartbeats.bulk`, Basic, Bearer and `?api_key=` auth, Hackatime's per-item bulk response shape (HC 1.3), the heartbeat-to-event rules, the project-name map | Captured heartbeats, including transcript-derived ones with a model token in the user agent, map to the expected events; bulk response shape matches Hackatime's | 1.8 |
| 1.16 | `e2e-wakatime.yml`: the real wakatime-cli v2.28.4 release binary on three OS runners, a fake Hackatime as `api_url`, the agent through `[api_urls]` | Both destinations receive a test heartbeat. With the agent stopped, the fake Hackatime still receives and wakatime-cli exits without an auth error; after restart the offline queue reaches the agent. The observed queue behaviour is written to a report artifact. A malformed key is refused by the installer before it reaches the file | 1.15, 1.17 |
| 1.17 | Installer core: INI-preserving `~/.wakatime.cfg` editor (pattern choice, key validation, the no-config path with the watermark seed), Claude Code settings merger, autostart writers, exact uninstall, `--dry-run` diff | Golden before-and-after fixtures, including an existing `.*` line and existing git-ai hook entries; uninstall restores files byte for byte; `e2e-install.yml` runs install and uninstall with a temporary home directory on all three OSes | 0.1 |
| 1.18 | `install.sh` and `install.ps1`: download, verify the release manifest's SHA-256, run `pairtime install` | `e2e-install.yml` runs both scripts against a local release fixture server; a tampered binary is refused | 1.17 |
| 1.19 | Local dashboard v0 (Svelte, shared components): today and week, the chess clock, per-project list, uncredited time by reason, the loop recap | Playwright against an agent with a fixture database: displayed totals equal the scorer's output; no page loads without the one-time token | 1.7, 1.8 |
| 1.20 | `pairtime doctor` and `pairtime privacy show` | `doctor --json` validates against its schema; `privacy show` output is byte-identical to the uploader's next batch body | 1.8 |
| 1.21 | Git adapter: `.git` watch, refs and reflog, root-commit fingerprint, commit records, rewrites | Tests drive the `git` CLI in CI: commit, amend and rebase produce the right records and `rewrites`; the fingerprint is the same across a fresh clone | 0.2 |

### M2. Server core

| WP | Deliverable | CI acceptance | Depends on |
|---|---|---|---|
| 2.1 | Server skeleton: config, migrations, single writer committing every 50 ms or 500 rows, `/healthz`, `/readyz`, `/metrics`, startup check that `sqlite_version()` is at least 3.51.3 (SP 7.4) | Migrations apply on SQLite and on a Postgres 18 service container; the startup check refuses an old version in a test | 0.1 |
| 2.2 | HCA OIDC login as a confidential client, requesting `openid email slack_id verification_status` explicitly (SP 12.1) | End-to-end against a fake OIDC provider in CI; submission is refused unless `verification_status` is `verified` | 2.1 |
| 2.3 | Device grant (RFC 8628), `ptd_` tokens with a CRC32 checksum, Ed25519 key registration (SP 8.3) | The client completes the device flow against the server and the fake provider; corrupted tokens fail their checksum offline | 2.2 |
| 2.4 | Ingest: per-device sequence dedupe, `Idempotency-Key`, RFC 9421 signature and `Content-Digest` checks, partial acceptance, the no-free-text schema firewall, size limits, rate-limit headers (SP 8.2, 8.6) | Replays are idempotent; a fuzz test that puts random text in every string field always gets 422; an unsigned or re-signed batch is refused | 2.3 |
| 2.5 | Time and witness: offsets, monotonic re-anchoring, `clock_jump`, future and too-old rejection, per-minute `witness_log`, `witnessed` versus `backfilled` (SP 8.4, 8.5) | Scenarios with a skewed and a jumped clock; an event first seen after 15 minutes is `backfilled` | 2.4 |
| 2.6 | Rollups, cross-device merge (SM 3.4) and the SSE hub: additive upserts, topic ring buffers, `Last-Event-ID`, 15 s keepalives, `resync`, hourly coalesced public topics (7.3) | A dashboard stream receives a delta within 2 s of ingest; reconnect replays the gap; 1,000 open streams add under 50 MB of memory; two devices crediting one minute keep one | 2.5 |
| 2.7 | Profiles and project pages with the visibility rules of 7.3, and the onboarding choice | Playwright: a public profile contains no private project name and no hour-of-day data in its HTML or JSON; "Make private" hides it from an anonymous browser | 2.6 |
| 2.8 | Commit ingest and forge verification for GitHub, GitLab and Forgejo public repos (SP 4.3) | Recorded HTTP fixtures; a commit first seen weeks after its author date is flagged | 2.4 |
| 2.9 | Client uploader and sign-in: batch building, signing, backoff with full jitter, `Retry-After`, never dropping events (SP 8.6) | Client against server in CI: 24 hours of synthetic events replayed fast give equal totals on both sides, including through injected 429s and a server restart | 2.4, 1.7 |
| 2.10 | Deploy: distroless image, `compose.yml` with `cloudflared`, `litestream.yml`, runbook; `restore-drill.yml` | The workflow builds the image, runs it, replicates to a MinIO service container, restores into a fresh container, passes `PRAGMA integrity_check` and the API smoke tests | 2.1 |

### M3. The program path (first release a program can switch on)

| WP | Deliverable | CI acceptance | Depends on |
|---|---|---|---|
| 3.1 | Submissions: create (idempotent on program and `external_id`), overlap rejection, frozen numbers, recompute-open-only on a new scoring version (SP 10.2) | API tests for every state transition; a second submission over the same project and window is refused | 2.6 |
| 3.2 | Reveal protocol (TP B6): event metadata for the window, root verification, server CSPRNG challenge of `k = max(10, 10% of prompts)` capped at 30, redacted token reveals, withheld items uncredited | End to end: an honest client passes; an edited event fails; a withheld reveal lowers credited seconds by exactly the minutes it supported | 3.1, 0.3 |
| 3.3 | Server re-scoring of the revealed window with `pkg/score` at the stated version | A client modified to inflate its buckets is flagged with a mismatch; an honest one matches to the second | 3.2, 1.7 |
| 3.4 | Review UI: the panels in 6.2, row cuts that can only lower a row, required reasons, the access log | Playwright: a cut below captured seconds saves; a value above is refused; the reviewed user sees the access-log entry | 3.3 |
| 3.5 | Handbook justification endpoint, the deflation draft, and `integrations/airtable-automation.js` | Golden text for a fixture submission; vitest with a mocked Airtable API shows the script filling every mapped field | 3.1 |
| 3.6 | Hackatime-shaped reads (4.7): `authenticated/me`, `stats` with comma-string `filter_by_project` and ignored extras, `trust_factor`, `heartbeats/spans`, `info_batch`, `:username` resolution, the `pairtime` extra object, `final` | Contract tests built from Hackatime's MIT source at `43bde10b02db` (HC 0): same paths, parameters and JSON shapes; a small Ruby script in CI, written from scratch to make Stardance-style calls (HC 1.8), gets the expected credited seconds | 3.1 |
| 3.7 | Program OAuth clients, client-credentials reads, user-initiated grants, project-name linking (4.7), webhooks signed like Robert's with a 300 s tolerance, the `updated_since` list | A reference verifier accepts the signature and rejects a stale timestamp; retries back off; reads for an unconnected user return 404 | 3.1 |
| 3.8 | Evidence bundle, `pairtime verify`, `/.well-known/pairtime-keys.json`, the daily-root job | `verify` passes on an honest bundle and fails on any single changed byte; the daily root is deterministic and its commit is made to a test repository | 3.2 |
| 3.9 | Trust levels, the private-module interface with a no-op default, tier-1 flags, the `hackatime_mismatch` hook | A scan of every public and program endpoint finds `yellow` always masked as `blue` | 3.1 |
| 3.10 | Privacy hub: `POST /api/privacy/delete` and `/export` with ES256 JWTs, `iss: hackclub-privacy`, dry run, single-use `jti` (TP DI 17) | Tests with generated ES256 keys; a replayed `jti` is refused; a dry run deletes nothing | 2.2 |
| 3.11 | Program kit in `integrations/`: the one-page guide, Rails snippet, Robert note, and the Ari `journals[]` bridge function | The bridge's JSON validates against a schema written from Ari's documented ingest keys (HC 3.6) | 3.5 |

### M4. Breadth and release

| WP | Deliverable | CI acceptance | Depends on |
|---|---|---|---|
| 4.1 | Codex adapter: hooks, rollout reader for `.jsonl` and `.jsonl.zst`, trust-state detection | Fixture tests; automation fields `source`, `originator`, `thread_source` map to origin rules (SM 2.2) | 0.2, 1.8 |
| 4.2 | Cursor adapter: `~/.cursor/hooks.json`, Tab versus Agent edits, dedupe against Claude-format entries | Fixture tests; one prompt seen by both paths is counted once | 0.2, 1.8 |
| 4.3 | Copilot adapter: CLI and VS Code hooks, `events.jsonl`, `chatSessions` | Fixture tests | 0.2, 1.8 |
| 4.4 | Gemini CLI adapter, hooks plus `chats` reader | Fixture tests | 0.2, 1.8 |
| 4.5 | Devin dedupe and harness detection from the hook's parent process | Fixture tests with recorded process trees | 1.14 |
| 4.6 | AGENTS.md entry writer, `pairtime track`, the D3 rule, the compliance counter | Golden rules files; a scripted `track` loop with no keyboard input earns 0 in a scenario | 1.2, 1.17 |
| 4.7 | Adapter SDK, conformance kit, `pairtime emit` | The kit passes on every in-tree adapter in CI | 4.1 to 4.4 |
| 4.8 | Release pipeline: GoReleaser OSS, checksums, Ed25519-signed update manifest, Scoop bucket, Homebrew tap, winget manifest, self-update with the Windows rename swap (CP 4.6) | A tag on a fork produces every artifact; self-update swaps the binary on all three OS runners | 1.18 |
| 4.9 | Code signing: SignPath or Artifact Signing for Windows, quill notarization for macOS (CP 4.3, 4.4) | `signtool verify` on `windows-latest` and `codesign -v` on `macos-latest` pass. Needs accounts that Karim creates | 4.8 |
| 4.10 | Tray on Windows and Linux; `pairtime statusline` | Builds without cgo for those targets; statusline format test | 1.19 |
| 4.11 | `footprint.yml`: idle RSS and CPU over 10 minutes, hook latency, binary size | Fails if Windows RSS exceeds 25 MB, average CPU exceeds 0.1%, or the binary exceeds 15 MB (CP 7 targets) | 1.8 |
| 4.12 | `load.yml` (manual and weekly): 8x-peak ingest plus 5,000 SSE streams for 15 minutes against the server image | p99 ingest under 200 ms, no 5xx, server RSS under 1 GB | 2.6 |

### M5. v1.5

| WP | Deliverable | CI acceptance | Depends on |
|---|---|---|---|
| 5.1 | Opt-in typing evidence (TP DI 9): per-minute counts and injected share, nothing per keystroke stored | On `windows-latest`, synthetic `SendInput` keys show 100% injected share; a schema test proves no per-key table exists | 4.9 |
| 5.2 | Reviewer integrity: random assignment, own-club conflicts, seeded known-answer submissions, reviewer statistics | API tests | 3.4 |
| 5.3 | Ari `pairtime` time source: a written proposal and a reference row mapping, offered to Ari's maintainers | Mapping unit tests (the upstream change itself is theirs to accept) | 3.11 |
| 5.4 | Cursor `state.vscdb` reader, incremental by rowid and rate-limited | Fixture database tests | 4.2 |
| 5.5 | Shell integration (OSC 133/633 marks, zsh and fish history for lookback) for `CMD` and `TEST` anchors | Fixture tests | 1.3 |
| 5.6 | Calibration tooling: export reviewer cuts as labels, refit parameters, emit a new `scoring_version` | On synthetic labels generated from known parameters, the fitter recovers them within 10% | 3.4 |
| 5.7 | Opt-in server-side WakaTime endpoint for machines where the client can't run, with `entity` stored as an HMAC and 90-day retention (SP 8.8) | API tests; no plaintext path is stored | 2.4 |

## 11. Risks and cuts

### 11.1 Risks

Ordered by how badly each would hurt the race to 1,000 weighted projects.

| # | Risk | Why it matters | Mitigation |
|---|---|---|---|
| 1 | **Pairtime breaks a teen's Hackatime.** A malformed `[api_urls]` key makes wakatime-cli fail the whole run, so Hackatime stops receiving too (HC 1.5), and how wakatime-cli treats a dead local destination is undocumented (4.5) | One report of "pairtime broke my Hackatime" in a program channel would end route B. Coexistence is the whole adoption bet | The installer validates the key with wakatime-cli's own regex before writing (4.5); `e2e-wakatime.yml` (WP 1.16) runs the real wakatime-cli binary on three OSes, including the agent-down case, and blocks the release on failure; `pairtime doctor` checks both destinations; uninstall restores the file byte for byte |
| 2 | **HQ doesn't agree how to count pairtime projects**, or agrees late (HC Open question 1) | Without a counting rule, approved hours may never be attributed to pairtime | The counting rule in 8.4 is the first item in the first Q&I conversation, in week one (8.6). Pairtime's own outcome ledger tracks progress meanwhile. The `Alternate Tracking Method` text names pairtime in every row regardless |
| 3 | **Level B isn't accepted as significant evidence** (section 5, D6) | Most Hack Clubbers are on Windows (B, R8), where v1 has no keyboard-specific signal. If level-B AI time stays capped at a third, pairtime's credit advantage shrinks to pre-deflation and evidence, and engaged AI users lose their main reason to install | The typing-evidence module (WP 5.1) is the first v1.5 item and needs code signing first (WP 4.9). The level split is shown on every submission so Q&I can set policy per level. Pre-deflated hours with evidence still save reviewers work, which is the program-side reason to adopt |
| 4 | **Program calendars.** No program launching in the window, or route B champions saying "next season" | Hack Club programs run on seasons, and the bounty is a race (PA 1.14: grit was created 47 minutes after the announcement) | Route A, the native program, depends on no one else's calendar. Route C is many small programs. Tier 0 asks a program for one form field |
| 5 | **Pilot funding and sponsorship** for the native program's grants (HC Open question 9) | Programs receive about $8.50 per approved hour from HQ (HC 3.2), so a 350-weighted-project program moves real money | Use the standard YSWS budget path with an HQ sponsor; a smaller first season if needed |
| 6 | **Unvalidated accuracy.** The v1 parameters are guesses until the pilot (SM summary) | If reviewers find pairtime over-crediting, trust collapses for every program at once | Conservative defaults that follow "When in doubt, deflate" (HC 3.7), evidence levels, Q&I audits in the pilot, and the first refit after 300 decided submissions (section 5) |
| 7 | **Unsigned Windows binaries.** Smart App Control blocks unsigned executables on Windows 11, and SmartScreen reputation starts from zero for every unsigned release (CP 4.3). SignPath wants an existing release with some reputation | The audience is the 10-year-old Windows laptop (B, R8) | Sign through Azure Artifact Signing under Hack Club's organization (about $9.99 a month; CP 4.3) as soon as possible; ship the first alpha through `irm ... \| iex`, whose downloads may not carry the Mark of the Web (unverified, CP 4.2) |
| 8 | **Users without Hackatime are invisible to Robert** (HC 3.5) | The fraud desk only reads Hackatime | The pilot requires Hackatime as well (most participants have it already: 41,247 users, HC 1.10); a `pairtime` kind is requested from the Telescreen team |
| 9 | **A pairtime-run program grading pairtime users** (TP A6, U5) | HQ may discount the native program's numbers | Q&I runs or observes audits and spot-checks at a higher rate; reviewer statistics are published (6.3) |
| 10 | **Harness formats change** between releases (HS 17.7), and Codex hooks need a one-time trust (HS 6.1) | Silent loss of a harness's events | Fixtures per harness version in CI; the WakaTime bridge as a floor; `pairtime doctor` reports untrusted Codex hooks |
| 11 | **Remote and locked-down machines.** SSH and cloud dev boxes earn nothing in v1 (SM 3.4); GNOME Wayland has no focus signal, so slots are `ambiguous` and reading isn't credited there (SM 3.2) | Some teens only have a school Chromebook or Codespaces | The opt-in server-side WakaTime endpoint (WP 5.7) gives those users level-C credit; the remote-input join stays an open question (SM 3.4) |
| 12 | **Home-server hosting near a program deadline** (SP 9.3) | An outage on payout day costs program trust | Clients buffer; move the same compose file to a VPS (CCX13, or AX42 if CCX stays unavailable) before route A launches (section 9) |
| 13 | **Hackatime's program API moves** (its HCA-only transition started 2026-11-13, SP 3.1; a ClickHouse move is in review, HC 1.10) | Tier 1 mirrors Hackatime's shapes | Contract tests pinned to a Hackatime commit; a watch on upstream changes to the stats and trust endpoints |
| 14 | **Ari has no license in its repo metadata** (HC Open question 7) | The `pairtime` source contribution may be blocked | The journals bridge works without any Ari change (4.7) |
| 15 | **AGPL and private detection rules** (HC Open question 11) | A legal question could delay tier-2 rules | Every requirement is met by the public build with the no-op module (3.2) |

### 11.2 What I'd cut first

If time runs short, cut from the top of this list. Everything here can come later without changing the program-facing contract.

1. Leaderboards, badges, OG images and weekly emails. The recap and chess clock carry R3.
2. Tray icons (WP 4.10). The dashboard and `pairtime statusline` remain.
3. Package-manager channels: Scoop, Homebrew and winget (part of WP 4.8). The two install scripts remain.
4. The Gemini, Devin and Copilot native adapters (WP 4.3 to 4.5). The WakaTime bridge covers them at reduced strength.
5. Lookback readers other than Claude Code's.
6. Submission webhooks and the `updated_since` list (part of WP 3.7). Tier 0 and tier 1 don't need them.
7. Everything in M5 except typing evidence (WP 5.1).

Never cut: the dual-send safety work (WP 1.15 to 1.17), the Claude Code adapter, the scorer, witnessing, submissions with the review page and handbook text, the evidence bundle with `pairtime verify`, HCA sign-in, and the ingest schema firewall. Those are the parts that get hours into the Unified DB without breaking anything a teen or program already relies on.

### 11.3 Open questions for Karim and HQ

1. The four questions for Quality and Integrity in 8.6: attribution, significant evidence by level, two trackers and one number, the spot-check window.
2. Who sponsors and funds the native program, and which reviewers staff it (HC Open question 9)?
3. Does R5's "public-by-default" mean pre-public, which needs a written Children's Code "compelling reason" assessment, or a forced choice at onboarding (TP B7)?
4. Will HQ mark pairtime an official HCA app, or accept an allowlist of reviewer HCA ids (SP Open question 1)?
5. Can Hack Club's organization hold the Windows signing account, and who holds the Apple Developer membership (CP 4.3, 4.4)?
6. Would the Telescreen team add a `pairtime` kind, and would hackatime-setup's maintainers take an optional "also install pairtime" step (HC 3.5; 8.2)?
7. Two facts to check before building: Claude Code's `statusLine` setting (3.3), which HS doesn't cover, and wakatime-cli's behaviour toward an unreachable `[api_urls]` destination (4.5), which WP 1.16 settles.
