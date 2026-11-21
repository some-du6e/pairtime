# grit: what it is, and what it means for pairtime

Status: complete and fact-checked (2026-11-19), pending Karim's read. The fact-check log at the end lists what was corrected.

Pinned grit commit: [`aa00f4331fa81994e30473f69a884ea7dfed4d2d`](https://github.com/N1k0s1/grit/tree/aa00f4331fa81994e30473f69a884ea7dfed4d2d), "Update README, PROGRESS and TODO for Phase 2", 2026-11-19 07:57 UTC. It was still the head of `main` at 22:58 UTC the same day, with no new commits, releases, issues or pull requests.

This doc is for Karim, who has to decide whether pairtime competes with grit or works with it. I read grit at the pinned commit through the GitHub API and a source tarball: every Markdown file, the Go source under `cmd/` and `internal/`, the main web templates, the scripts, the CI workflow, the release and the CI run history. I didn't build or run it. It updates [prior-art-and-measurement.md](prior-art-and-measurement.md) 1.14, which read grit at `e7cf94e`, 3 hours 20 minutes and 5 commits earlier.

A reference like `internal/score/score.go:42-52` means lines 42 to 52 of that file at the pinned commit. pairtime references use the keys from [DESIGN.md](../DESIGN.md): DESIGN, PLAN, SM for [struggle-metric.md](../design/struggle-metric.md), B for [BOUNTY.md](../BOUNTY.md), PA for the prior-art doc.

grit is another teenager's open-source project, built for the same bounty and in the open. Where this doc says grit lacks something, that is a statement about one day of work against a plan, and the plan usually already names it.

## 1. What grit is today

grit is two pure-Go binaries. `grit` runs on the builder's machine: it reads AI harness transcripts, samples an OS idle timer, watches project folders for hand edits, scores each session with written rules and syncs the numbers. `gritd` is a Go and SQLite server with accounts, public profiles, projects, a live leaderboard and a reviewer queue. The tagline is "Credits only human-steered time." It is licensed AGPLv3, and the maintainer is Niko, `N1k0s1` on GitHub.

The repository was created at 00:24 UTC on 19 November, 46 minutes after Zach's announcement. pairtime's was created at 00:16. grit's history starts with a 4,413-line MVP commit at 00:03 UTC, authored as Claude, which PROGRESS.md says was split out of the maintainer's personal workspace with its history. By 07:57 UTC grit had finished phases 0, 1 and 2 of a plan that runs to phase 5, apart from one real-machine test of `grit install` and a by-eye check that macOS shows no permission prompt for the idle call.

### 1.1 Release status

| Item | State at the pinned commit |
|---|---|
| Releases | One prerelease, [`v0.1.0-rc.2`](https://github.com/N1k0s1/grit/releases/tag/v0.1.0-rc.2), published 01:18 UTC from `b5b2479`, the end of phase 1. Six `grit` binaries for Windows, macOS and Linux on amd64 and arm64, plus `checksums.txt`. Each asset shows 1 or 2 downloads |
| What the release contains | The phase 1 CLI: `login`, `sync`, `watch`, `sessions`, `explain`, `submit`, `hook` and `activity`. The daemon, `install`, `doctor`, `open`, `exclude`, browser login and the local dashboard came in 11 later commits that no release includes yet |
| Installers | `scripts/install.sh` and `scripts/install.ps1` download a release binary, refuse it unless its SHA-256 matches `checksums.txt`, then run `grit install`. They fetch from GitHub's `releases/latest` path, which skips prereleases, so the one-line install in the README starts working with the first full release. `GRIT_VERSION=v0.1.0-rc.2` points them at the prerelease, but that binary predates `grit install`, so until a new release a tester builds from source with Go 1.26 |
| Server | No hosted instance. `grit login` defaults to `http://localhost:8080`. TODO-NIKO.md lists a VPS, a domain and the Hack Club Auth app registration as things only Niko can do |
| CI | 22 runs: 20 on pushes to `main` and 2 on release tags (`v0.1.0-rc.1` failed, `v0.1.0-rc.2` passed). The last four are green. Runs 4 to 10 failed on Windows until git's forward-slash paths and unclosed test databases were fixed, and run 18 failed on a shellcheck warning. No issues or pull requests have been opened |
| Size | About 6,500 lines of Go outside tests, 2,200 lines of tests in 59 test functions, and 800 lines of HTML, CSS and JavaScript |

### 1.2 Architecture

| Part | What it does | Where |
|---|---|---|
| Collectors | One `Collector` per harness, `Name`, `Paths` and `Parse`, turning a log into `model.Session` values made of `prompt`, `agent` and `tool` events. The daemon reads only lines appended since its last pass, using a byte offset per file | `internal/collect/collect.go:22-31`, `internal/collect/stream.go` |
| Activity | Every 15 s, asks the OS for seconds since the last keyboard or mouse input, never which keys. The minute of the last input is set in a 180-byte bitmap per UTC day, next to a second bitmap of minutes the sampler ran, so "no input" and "no data" stay apart | `internal/activity/sampler.go:9`, `internal/activity/bitmap.go:17`, `internal/activity/bitmap.go:107-119` |
| Idle sources | macOS: `CGEventSourceSecondsSinceLastEventType` through purego, with an `ioreg` fallback. Windows: `GetLastInputInfo` minus `GetTickCount`. Linux: X11 MIT-SCREEN-SAVER, else GNOME Mutter over D-Bus, else "unknown" | `internal/activity/idle_darwin.go`, `idle_windows.go`, `idle_linux.go` |
| Hand edits | Polls the working trees of git repos that had an agent session in the last week, stat-ing modification times every 30 s and skipping hidden, dependency and build folders. Roots over 100,000 files are dropped | `internal/edits/watcher.go:33`, `internal/edits/watcher.go:83` |
| Scoring | Splits a session into turns and credits intervals by rule, then removes overlap across parallel sessions | `internal/score/score.go` |
| Git | `git rev-parse` and `git remote` for the repo and name, `git log --shortstat` for commits from 5 minutes before a session to 30 minutes after it | `internal/gitinfo/git.go` |
| Attestation | A salted SHA-256 chain over each turn's prompt time and redacted prompt text, sent with every sync. The server records when it first saw each link | `internal/attest/attest.go` |
| Daemon | Sampler, edits watcher, incremental log reading, rescoring and sync in one process, a pass every 30 s, rescoring only when something changed or every 5 minutes, with a 16 MB Go memory limit | `cmd/grit/daemon.go:21-24`, `cmd/grit/daemon.go:43-123` |
| Autostart | A LaunchAgent on macOS, an XDG autostart entry on Linux and an `HKCU\...\Run` value on Windows, written by hand with no admin rights. `grit uninstall -purge` removes everything | `internal/autostart/` |
| Local dashboard | Served by the daemon on `127.0.0.1` behind a random token: today, a weekly goal, sessions with a replay of the builder's own prompts, a weekly recap, pause, excluded folders, a byte-for-byte log of every upload, and a submit flow with a redacted preview | `cmd/grit/dashboard.go`, `cmd/grit/web/` |
| Server | `gritd`: open signup with an API key until Hack Club Auth lands, public profiles, project pages with commits, a leaderboard and live updates over server-sent events, and a review queue | `internal/server/server.go:98-122`, `internal/store/store.go:20-83` |

The daemon was measured on a synthetic 200 MB Claude Code transcript on an Apple Silicon Mac: about 1 s for the first read, 20 MB physical footprint, 30 MB RSS and 0.03% idle CPU (README "Daemon footprint", `scripts/bench-daemon.sh`).

### 1.3 Harnesses covered

| Harness | How | Status in grit's own README |
|---|---|---|
| Claude Code | Reads `~/.claude/projects/*/*.jsonl`. A user line is a prompt unless it is meta, a compaction summary, a sidechain, a tool result, local-command output, or has an `origin.kind` other than `human`. Slash commands count as prompts | Verified on a real install, macOS, 19 November |
| Codex | Reads `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`. Prompts are `event_msg` `user_message` events only, because `response_item` user messages also carry injected context | Verified on a real install: 53 rollouts |
| Anything with hooks | `grit hook -harness NAME -kind prompt\|agent\|tool\|stop` reads the hook's JSON on stdin, guesses common field names for Claude Code, Cursor and generic payloads, stamps the time it runs and appends to `~/.grit/events.jsonl`. The README gives a Cursor recipe | Fixture only |

When it finds Cursor, Gemini CLI, Windsurf or OpenCode installed, `grit doctor` reports it as "installed, not read yet". Nothing is installed into any harness (`internal/collect/collect.go:1-5`): the two log readers are fully passive, and hooks are something the user adds by hand. The prompt rules in the table are in `internal/collect/claudecode.go:78-108` and `internal/collect/codex.go:79-89`, and the hook field guessing in `internal/collect/hooks.go:97-130`.

### 1.4 How grit computes credit

A session is a list of turns, each a prompt plus the agent run that followed it, ending at the last agent or tool event before the next prompt. Credit comes from three kinds of window: before a prompt, during a run, and a tail after the last run. Every credited interval carries a plain-English rule string, `grit explain ID` prints them per turn, and the review page shows them beside each prompt.

The v1 rules use timestamps alone. These are the defaults in `score.Default`, `internal/score/score.go:42-52`:

| Rule | Constant | Value | Where |
|---|---|---|---|
| A reply within this gap after the agent finished credits the whole gap as thinking | `IdleTimeout` | 10 min | `score.go:194-197` |
| After a longer gap, or before the first prompt, credit only a typing estimate of the prompt's length at this speed | `ComposeCPS` | 3 chars/s | `score.go:188-201` |
| The typing estimate is clamped to this range | `ComposeMin`, `ComposeMax` | 20 s to 4 min | `score.go:188` |
| Think credit before a bare go-ahead is capped here. A go-ahead is one of 25 phrases such as "continue", "yes", "lgtm" and "try again", or any prompt under 4 characters | `LowEffortCap` | 90 s | `score.go:55-70`, `score.go:214-222` |
| A run counts as supervised if the next prompt came within this time of the run ending | `ReturnWindow` | 2 min | `score.go:236-240` |
| A supervised run is credited up to this length | `AgentCap` | 8 min | `score.go:239-243` |
| An unsupervised run, or the last run of a session, gets only its first minute | `Unsupervised` | 1 min | `score.go:244-251` |
| Credit after the last run, for checking the result | `ReviewTail` | 2 min | `score.go:275-283` |

v2 adds the activity bitmap. It applies when the sampler saw at least half of the session's minutes (`MinCoverage = 0.5`, `score.go:103` and `score.go:166`). The v1 windows still decide what could count, and inside them:

- Think and review windows credit only minutes with keyboard or mouse input (`score.go:204-217`, `score.go:278-281`). A go-ahead's cap keeps the input minutes closest to the prompt.
- A supervised run is credited in full, up to 8 minutes, if there was input anywhere from its prompt to 2 minutes after it ended, and gets nothing otherwise. An unsupervised run's first minute counts only where there was input (`score.go:252-269`).
- Inside a v2 session, a minute the sampler didn't see counts as no input, so a closed lid isn't supervision (PROGRESS.md, Decisions).
- Below half coverage, the session is scored by v1 and carries no activity signal, which lowers its trust tier.

After scoring, `mergeOwn` removes overlap inside a session (`score.go:388-405`), and `Dedupe` removes overlap across all of a person's sessions on the machine. The earlier-starting interval keeps the overlapping time, and the removed seconds are recorded per session, so five agents in parallel count as one person (`score.go:441-473`).

Hand-written code becomes `manual` sessions (`internal/edits/classify.go:22-25`, `52-60`, `128-166`). A changed file is the agent's if it changed during a run or up to 1 minute after it (`RunGrace`), or within 3 minutes after the agent named that file in a tool call (`ToolLag`). Otherwise it is hand-written only if there was input in that minute, and unknown if there wasn't. Each hand edit credits the input minutes in the 5 minutes before it (`HandLookback`), and edits more than 30 minutes apart start a new manual session (`HandGap`). The rules lean toward the agent on purpose: typing in one file while an agent runs in the same project counts as agent time.

Prompts also get tags (`score.go:62`, `score.go:171-185`). `low-effort` marks a go-ahead, so it travels with the 90 s cap above. The other two never change credit: `debugging`, from a regex over words like error, crash, traceback, "doesn't work", why, stuck and still, and `specific`, at 200 characters or more. Sessions get flags that never change credit either. A test checks this for the four anomaly flags, and the first three are computed after credit is final:

| Flag | Condition | Where |
|---|---|---|
| `autopilot` | At least 4 prompts, and half or more of them go-aheads | `score.go:417-419` |
| `mostly-agent` | Agent run time more than 4 times the credited time | `score.go:420-422` |
| `long-idle` | Session over 1 hour with under a fifth of it credited | `score.go:423-425` |
| `regular-timing` | 6 or more prompts whose spacing has a coefficient of variation under 0.1 | `internal/score/anomaly.go:25-27`, `62-82` |
| `repeated-prompts` | One prompt, or one template with only numbers changed, at least 3 times and in at least 30% of prompts. Go-aheads and templates under 12 characters are ignored | `anomaly.go:28`, `94-112` |
| `no-input-prompts` | With activity data, at least 2 prompts and a quarter of all prompts arrived with no input in their minute or the minute before | `anomaly.go:114-122` |
| `no-output` | An hour or more with no file touched, no edit on disk and no commit | `anomaly.go:29`, `46-48` |

Two things are worth noticing. The rules are small enough to explain on one page, which is a real asset with reviewers. The v1 windows fit in one table on grit's public "How time is counted" page, and v2 adds a few sentences in the README. That page predates v2 and doesn't describe the activity rules or the four newer flags yet (`internal/server/web/how.html`). And the rules are hand-set: `score.go:28` calls the defaults "deliberately conservative", and nothing in the repo fits them to labeled sessions or reviewer decisions yet.

### 1.5 Trust, review and privacy features

- **Witnessing.** Each turn's link is `sha256(previous link, prompt time, kind, sha256(redacted prompt))`, seeded with the session id and an HMAC salt derived from a 32-byte device secret, so the server can't guess short prompts from their hashes (`internal/attest/attest.go:55-93`). The server keeps the first time it saw each link. At submission it recomputes the chain from the bundle. A turn whose link it first saw within 15 minutes of the prompt, allowing 5 minutes of clock skew, is witnessed; a matching link seen later is backfilled; a link that differs from the first one the server saw at that index fails (`attest.go:39-43`, `109-153`).
- **Trust tiers.** High is witnessed and scored with activity data, medium is one of the two, low is neither, and a failed chain is low. Profiles, session pages and the review page show the tier (`internal/model/model.go:126-142`, `internal/server/server.go:408-429`).
- **Review.** The queue lists submissions. A submission page shows claimed, session and agent time, commits, flag and tier counts, a per-session timeline of "you" against "agent", and every redacted prompt with its think and run rules and the tools, files and commands of its turn. The server recomputes the `regular-timing`, `repeated-prompts` and `no-output` flags from the submitted bundle and marks any the client didn't report. `no-input-prompts` needs the activity data, which stays on the client, so only the client can report it (`server.go:420-427`). A reviewer approves, deflates to a number of hours, or rejects. Until reviewer roles land in 4.5, the review pages are locked with one key set on the server, and the README documents a demo mode without it.
- **Logged against submitted.** `grit sync` sends per-session numbers, per-turn timings and prompt lengths, rule strings, hash links and git metadata (project name, branch, remote, commit hashes and subjects), with prompt text, file names, commands and the salt stripped by `Result.Public()` on the client and again on the server (`model.go:111-124`, `server.go:253-260`). `grit submit PROJECT` sends that project's sessions from the last 90 days in full: prompts and shell commands pass through 11 regexes for keys, tokens, JWTs, emails and phone numbers, the file names from tool calls go as the harness logged them, and each session's salt goes along. `-dry` prints exactly what would go, and the dashboard's submit page shows the whole bundle before it sends. Without `-dry`, `grit submit` sends straight away (`internal/redact/redact.go:7-22`, `cmd/grit/main.go:345-414`).
- **Controls.** An exclude list that is checked before any git command runs in a folder (`internal/scan/scan.go:151-153`), a pause that drops every session started while paused, a copy of every upload kept locally for the privacy page, and `grit uninstall -purge`.
- **Motivation.** The local dashboard has today's credited time, a weekly goal bar, a 7-day recap with a best day and hand-written time, and session replay. The public profile adds a 14-day chart of "you (credited)" against "agent ran", a streak of days with 15 or more credited minutes, all-time hours and the share of time at each trust tier, updating live (`internal/webstatic/static/app.js:73-110`).

### 1.6 What grit says is verified

grit's README keeps a table of what has run on real hardware, and it is candid. On the maintainer's Apple Silicon Mac: both log readers, the macOS idle signal, scoring v2 against real sessions, the agent half of the edits classifier, the installer's download and checksum check, `grit daemon`, `grit login`, `grit doctor` and the local dashboard. A prompt sent by another program with nobody at the keyboard earned nothing under v2. Fixture or CI only: the hook collector, the Windows and Linux idle signals, the hand-written half of the edits classifier, attestation and trust tiers (unit, store and HTTP tests plus the demo server), `install.ps1`, and `grit install` on any real machine. pairtime should hold itself to the same standard of labeling.

## 2. Its plan and pace

### 2.1 How grit plans its work

PLAN.md calls the goal "the most polished, trustworthy entry" for the bounty. Each item has a "done when" line, and nothing is done until that holds and `go vet` and `go test` pass. Items are tagged `[core]`, the trust core, which "judges will poke at" and which the lead writes or reviews line by line, or `[ticket]`, which is "fine to hand to any contributor or model as one PR". CLAUDE.md is written for coding agents. Its rules overlap pairtime's AGENTS.md: pure Go with no cgo, table tests (for scoring, in grit's case), and content that stays on the machine unless the user submits it. It adds rules pairtime's file doesn't have: no em or en dashes anywhere, checked in CI; a rule string on every credited second; and fixtures that are always synthetic, where DESIGN 5.1 asks pairtime's adapters for scrubbed captures from real harnesses instead. TODO-NIKO.md holds the steps only the maintainer can take. PROGRESS.md records what is done, what ran on a real machine and every judgment call, under "Decisions".

### 2.2 What comes next

PROGRESS.md names two next steps: the real-Mac check of `grit install`, which waits on Niko's go-ahead because it adds a login item, and phase 3.

| Phase | Items | Notes |
|---|---|---|
| 3, collectors | Cursor, hooks first with `grit install` offering to add them, and a read-only backfill from `state.vscdb`; Copilot Chat in VS Code, by replaying its `chatSessions` JSONL mutation log; Gemini CLI; Cline and Roo Code across every VS Code fork's user-data folder; Aider's `.aider.input.history`; OpenCode; Windsurf through hooks only | All `[ticket]`. Each needs a synthetic fixture and an honest README row. "If a format cannot be confirmed, ship a hook recipe instead of a guessed parser." Continue and Zed are skipped because they have no per-message timestamps |
| 4, server and public site | 4.1 Hack Club Auth through OIDC as the only sign-in, storing `sub`, name, `slack_id` and `ysws_eligible`, showing a "verified Hack Clubber" mark, never requesting a birthdate, and removing open signup. 4.2 an optional GitHub link to verify repo ownership. 4.3 profiles public by default with a private toggle, a year heatmap, streak, weekly goal, top tools and trust-tier mix. 4.4 project pages with a shipped status and an SVG badge. 4.5 reviewer and admin roles, queue filters, a replay scrubber, a decision log that can't be edited, and a JSON export of approved hours per project. 4.6 a program API, `GET /api/v1/projects/<id>/hours`, with credited time, approved time and trust mix. 4.7 landing, install, FAQ, privacy and settings pages in light and dark at 375 px. 4.8, last and off by default: a Hackatime bridge that sends credited intervals as heartbeats with WakaTime's AI fields | 4.1 and the role and log parts of 4.5 are `[core]`; the rest are `[ticket]`. TODO-NIKO.md says to check with the Hackatime team before 4.8 is ever switched on |
| 5, scale and polish | 5.1 a load test at Hackatime's numbers, with active users syncing every 30 s, reporting CPU, RAM, disk and monthly cost. 5.2 a `deploy/` folder with a systemd unit, a Caddyfile and a nightly SQLite backup, written but not deployed. 5.3 Playwright screenshots of every page at desktop and phone width | 5.1 is `[core]` |

TODO-NIKO.md has eight open items. One is about the maintainer's own shell setup. The other seven are: register the grit app with Hack Club Auth, provide a VPS and a domain, check with the Hackatime team before enabling the bridge, confirm by eye that macOS shows no permission dialog for the idle call, try one hand edit with the daemon running, run `grit activity` on a Windows or Linux machine, and approve the real `grit install` test.

The plan has no step about getting programs to adopt grit. It doesn't mention the Unified YSWS DB, Airtable, the handbook's justification fields, Quality and Integrity, Fraud Squad or devlogs, and it doesn't plan for grit to run a program of its own. The program-facing pieces are 4.5's JSON export and 4.6's hours endpoint. It has no calibration step either: the constants in 1.4 stay hand-set through phase 5.

### 2.3 Pace

All 28 commits landed on 19 November between 00:03 and 07:57 UTC, and none in the 15 hours after that, up to 22:58 UTC.

| Stretch, UTC | Commits | What landed |
|---|---|---|
| 00:03 | 1 | The MVP import: 36 files, 4,413 lines, authored as Claude |
| 00:23 to 01:18 | 17 | Workspace notes, CI and CONTRIBUTING.md, then all of phase 1: the activity signal and sampler, scoring v2, hand edits, attestation and anomaly flags, at 350 to 830 lines a commit and a few minutes apart, then trust tiers, a flag-threshold fix, two Windows fixes and doc updates. The prerelease was tagged at 01:13 |
| 01:56 to 04:56 | 6 | The exclude list, the daemon with incremental parsing, `install` and `uninstall`, `doctor`, the install scripts and a shellcheck fix |
| 07:28 to 07:57 | 4 | Browser login, pause, the local dashboard at 1,004 lines, and the phase 2 docs update |

The commits arrive minutes apart, and PROGRESS.md speaks of "this agent session". grit, like pairtime, is built by coding agents with a human lead, and its phases are sized for that. About eight hours at that pace finished phases 0 to 2. Phase 3 and most of phase 4 are `[ticket]` items of the same size, so at that pace they are days of work, not weeks.

What could slow grit is the same thing that could slow pairtime: steps a person has to take. The VPS, the domain, the Hack Club Auth registration, the Hackatime team's view, and tests on real Windows and Linux machines all sit in TODO-NIKO.md, and grit's own rules forbid calling a collector verified from a fixture. The rest of this paragraph is my estimate; grit states no dates. What grit runs today, a local tracker plus a server with profiles and a review queue, is roughly what pairtime reaches between M1 on 12-01, M3 on 12-11 and the review desk in M4 by 12-18 (PLAN, "Milestones at a glance"), so pairtime starts about three to four weeks behind on running software, and grit keeps moving. If Niko clears the TODO-NIKO.md items this week, a hosted grit with Hack Club sign-in could exist before pairtime's M3, and possibly before its M1. Nothing in grit's plan dates the first program.

## 3. Requirement by requirement

Requirement text is in B. "Met" means the code at the pinned commit does what the requirement's words ask; "partly" means some of it is built or verified; "not yet" means it is planned or absent. Nothing in grit is deployed, so every server-side "met" is "met in code".

| ID | Where grit stands | Evidence |
|---|---|---|
| R1 License | Met | `LICENSE` is the AGPL-3.0 text, and README.md and CONTRIBUTING.md say AGPLv3, with contributions under the same license. Files carry no SPDX headers, and nothing says "or later" |
| R2 Accuracy | Not shown yet | The rules in 1.4 are explainable and lean low on purpose, but nothing compares them with reviewer judgments or labeled sessions. Tests are table tests plus real sessions on one Mac. How the rules behave: under v2, reading the agent's output counts only in minutes with some keyboard or mouse input; a supervised run counts in full up to 8 minutes; repeated prompts and go-ahead runs are flagged for the reviewer but still credited, apart from the 90 s go-ahead cap. The vision post says dark flow and "doing the same thing over and over again" shouldn't count (B, "From the linked vision post"), and grit leaves that call to the reviewer |
| R3 Worth using | Partly | The local dashboard works offline and without an account: today, a weekly goal, a 7-day recap and session replay. The public profile adds a streak, a 14-day chart of the builder's credited time against agent time, all-time hours and a live update. Each session has a replay page to open, but nothing arrives on its own when a session ends: no recap, no notification and no coaching insights yet, and no evidence from users. A year heatmap and project badges are planned in 4.3 and 4.4 |
| R4 Passive | Mostly, for two harnesses | The daemon is set to start at login with no admin rights (tested in CI on each OS, not yet on a real machine), and Claude Code and Codex need nothing installed. First run scores the last 30 days of existing transcripts, at v1 and low trust, so lookback comes for free. Hooks for other harnesses are added by hand today; 3.1 plans `grit install` offering Cursor's. Of R4's three stories, hours of flow and five minutes before school work under the rules in 1.4. Five projects in an afternoon works per session, but grit doesn't read window focus, so when two projects' windows overlap, the earlier-starting interval keeps the time rather than the project on screen |
| R5 Server | Met in code, not deployed | Accounts with open signup until Hack Club Auth (4.1). Every profile is public, with a private toggle planned (4.3). Projects are named after the git remote, or the folder when there is none. Git integration is local: repo, remote and the commits made during each session, linked to GitHub or GitLab on project pages; verifying repo ownership through GitHub is 4.2. The leaderboard and profiles update over server-sent events as sessions sync. No instance runs anywhere yet |
| R6 Privacy | Met by default | Sync sends numbers, rule strings, hashes and git metadata (project name, branch, remote and commit subjects), never prompt text, file names or commands, and the server strips those again. The idle signal is a number of seconds and stays on the machine as a per-minute bitmap. An exclude list is checked before git runs in a folder; there is a pause; every upload is kept locally for the privacy page; prompts leave only in a submission, which the dashboard previews in full and `grit submit -dry` prints. A submission uploads every prompt of the project from the last 90 days, with keys, tokens, emails and phone numbers scrubbed, rather than a sample. Public profiles and session pages show per-session times, project names, turn timings and prompt lengths, and project pages list commit subjects |
| R7 Every harness | Partly | Model-agnostic, because it reads transcripts and never talks to a model. Two log readers verified on real installs, a generic `grit hook` receiver that is fixture only, a documented `Collector` interface with a contributor guide, and eight more tools planned in phase 3's seven items. Cursor, Copilot and Gemini CLI have no native support today. The README names browser chat as a `grit hook` case, through an extension, but no extension exists or is planned |
| R8 Platforms, old laptops | Partly verified | Pure Go with `CGO_ENABLED=0`, six targets, race tests on three OSes in CI, and a measured 20 MB footprint with 0.03% idle CPU on an Apple Silicon Mac. `install.ps1` turns on TLS 1.2 for Windows PowerShell 5.1 on older systems, and the macOS LaunchAgent runs as a low-priority background process. The Windows and Linux idle signals, `install.ps1` and `grit install` haven't run on a real machine. There is no 32-bit Windows build, and `install.ps1` stops on a 32-bit processor. No code signing is in CI or the plan. Windows shows a console window for a moment at login. The edits watcher stats every file in watched repos every 30 s, which hasn't been measured on a spinning disk. On Wayland desktops other than GNOME the idle signal is unknown, so sessions there fall back to v1 |
| R9 Trustworthy | Partly | Under v2, a prompt with no keyboard or mouse input before it earns no think credit, and its run counts only if there was input during or just after it. Witnessing, trust tiers and seven flags point reviewers at weak sessions, the server recomputes three of the four anomaly flags from the submitted bundle, and a reviewer reads every prompt. grit's README lists its own gaps: scripted prompts plus a mouse jiggler are caught only by the timing and repetition flags and a reviewer, witnessing needs the daemon running, and hand edits are judged by the minute. No ratio of approved to credited hours has been measured |
| R10 Auditable | Partly | Submissions carry redacted prompts with the rule behind every credited interval, and the server checks each turn's hash link against the time it first saw it. Logged and submitted hours get the different privacy treatment R10 suggests. A decision log that can't be edited and a JSON export of approved hours are planned in 4.5. An outside auditor works through the grit server's review pages; there is no bundle to check offline |
| R11 Hosting cost | Estimated | The README estimates 150,000 to 250,000 sessions and about 1 GB of new data a month, on one small VPS with SQLite in WAL mode, for "well under $50", and says "To be proven with a load test before submission" (5.1). The design is cheap in the same way pairtime's is: clients score, the server stores rows |
| A Adoption | Not started | No hosted server, no program, no counting rule with HQ, and no integration with the Unified YSWS DB or program review flows beyond the planned hours endpoint (4.6). pairtime is at the same point today |

Read together: grit already does, in code, most of what R1, R4, R5, R6 and R7 ask. Its open questions are the ones that decide the race: whether reviewers will accept its hours (R2, R9, R10) and whether programs will use it (A).

## 4. Where grit is ahead, and where pairtime's design is ahead

The comparison is lopsided in a way that matters. grit is software that runs. pairtime is a design that has been judged and planned but not built: PR #1 merged the event schema and CI, PRs #4 to #7 (event provenance, the ledger byte spec, more CI workflows and the parameter table) are open, and the 140 work packages of PLAN start on 11-20. So every "pairtime is ahead" below is a claim about a design, and every "grit is ahead" is a claim about code.

pairtime already builds on grit's work. SM takes grit's go-ahead list and extends it, adopts its `regular-timing`, `repeated-prompts` and `no-input-prompts` flags with grit's thresholds, and uses its 15-minute witness window, 15 s sample interval and 10-minute think limit (SM 2.2, 4.2, 8). DESIGN 6.7 plans to report grit's published rules as one of four baselines in the R2 report.

### 4.1 Where grit is ahead

| Area | grit | pairtime |
|---|---|---|
| Working software | Client and server, phases 0 to 2, verified on one real Mac with two harnesses | An event model and CI. The first installable alpha is M1, targeted for 12-01 |
| A release | `v0.1.0-rc.2`, six checksummed binaries, and install scripts that verify the checksum | None. WP-RL1 and WP-RL2 are in M1 |
| Pace | Phases 0 to 2 in about eight hours of commits | Three agent slots landing about four WPs a day, by PLAN's own assumption |
| Measured footprint | 20 MB and 0.03% idle CPU on a 200 MB transcript, on a Mac | Gates are planned from WP-AG1, under 25 MB idle RSS on Windows; nothing is measured yet |
| A reviewer UI to show people | A review queue and submission page with timelines, per-turn rules, flags, tiers and approve, deflate or reject, plus `gritd -demo` with generated builders | WP-RV1 and WP-RV2 in M4, with `pairtimed seed --demo` |
| How little it touches | The Claude Code and Codex readers install nothing into the harness | `pairtime install` writes hook entries into each harness's settings, Karim's AGENTS.md entry and a `~/.wakatime.cfg` line (DESIGN 1.2, 5.9) |
| Explainability | The v1 rules fit in one table on a public page, v2 adds a few sentences, and every interval carries an English sentence | SM is a 969-line spec with anchors, leads, a reading bucket, focus slots and four dark-flow detectors. Each minute can be recomputed by hand (SM 5.2), but explaining the rules to a program author takes longer |
| Size of the trust machinery | One 174-line file, `internal/attest/attest.go`: a salted hash chain over prompts, first-seen times and three statuses, plus a 17-line tier function and about 75 lines of server code that stores links and checks them | A ledger with keyed Merkle batches every 60 s, signed receipts, public day roots, commit-then-reveal with a committed challenge, checkpoints, sealing and an offline verifier (DESIGN 3.6, 7). Of the evidence-first design this layer comes from, the engineering judge wrote that much of it "gets built before anyone knows whether Fraud Squad will accept it" (JE, Evidence first) |
| What a reviewer reads | Every redacted prompt in the submitted project | A random sample of 10 to 30 prompts per submission, `CHALLENGE_K`, each beside the agent message it answered (DESIGN 6.3, 7.5). More private and less to read; whether reviewers find a sample enough is untested |
| Native harness support planned | Phase 3 lists Cursor, Copilot Chat, Gemini CLI, Cline, Roo Code, Aider, OpenCode and Windsurf, each as a log reader or a hook recipe | v1 lists Cursor, Copilot, Gemini CLI, Devin and Cline next to Claude Code and Codex (DESIGN 5.1). grit's list is slightly longer |
| Honest status labels | "Verified on a real install" against "fixture only", per part, in the README | The same rule exists for fixtures in DESIGN 5.1, but there is nothing to label yet |

### 4.2 Where pairtime's design is ahead

| Area | pairtime's design | grit at the pinned commit |
|---|---|---|
| Accuracy method (R2) | A labeled pilot of 40 to 60 teens over winter break, two raters per minute, Cohen's κ of at least 0.6, parameters fitted inside their source ranges, scoring frozen at 1.0.0 before any program decision, and an R2 report against four baselines (DESIGN 6.7; SM 7) | Hand-set constants and no calibration step in the plan |
| What counts as engaged | A second counts only if a human act opened a window over it, the OS saw the person present, and focus was on that project (SM 2.7). Reading is credited from a bucket filled by the agent's actual output; agent run time is never credited as such; prompt-origin rules and harness automation labels zero out `/loop`, `/goal`, `-p` runs and background agents (SM 2.2, 2.4, 2.6) | Session windows from timestamps, narrowed by any keyboard or mouse input per minute. A supervised run counts in full up to 8 minutes; silent reading after a run counts only in minutes with input |
| Multitasking (R4 story 3) | One attention timeline per user, with focus deciding which project gets each second (SM 3) | Overlap goes to the earlier-starting interval; focus isn't read |
| Dark flow | Four detectors that discount re-rolls, rewrite loops, repeated errors and unread go-ahead runs, a no-progress half rate that progress restores, and loop rows a reviewer can raise back (SM 4; DESIGN 6.2 D12) | Flags for the reviewer, which never change credit. The one built-in discount is the 90 s cap on think time before a go-ahead |
| Evidence a reviewer can check | Exact server re-scoring from committed records with held minutes, eleven consistency checks, commits with three clocks including the forge's, published evidence levels and bands, a published audit formula, and `pairtime verify` running offline on a signed bundle (DESIGN 6.4, 6.5, 7.3, 7.8, 7.9) | A hash chain over each prompt's time and redacted text, checked against first-seen times; flags recomputed on the server; trust tiers |
| The program path (A) | Three adoption tiers that work under today's handbook, generated text for the handbook's justification fields, an Airtable script, Hackatime-shaped reads for Rails programs, a counting rule asked of HQ in week one, a first conversation with Quality and Integrity, and a native program with outside reviewers (DESIGN 1.4, 9) | A planned hours endpoint and JSON export. No step about program adoption, HQ or the YSWS DB |
| Hackatime | Lives beside it: a second destination in `~/.wakatime.cfg`, a release gate that a teen's Hackatime keeps working, and, for teens who also send to Hackatime, Hackatime-equivalent seconds beside pairtime's numbers on the review page (DESIGN 5.6, 6.2 D15, 7.2) | A bridge that would send credited intervals to Hackatime as heartbeats, last in the plan, off by default and waiting on the Hackatime team |
| R3 for its own sake | A coach on the device: a recap after every work session, the chess clock, moments, build days with a weekly goal and freezes, a logbook and devlog drafts. It deliberately has no daily streak counter (DESIGN 3.8, 6.2 D10; PA 4.1) | Today, a weekly goal, a weekly recap and replay locally; a daily streak and a you-against-agent chart on the profile |
| Privacy for minors | Everyone treated as a minor, consent cards with "Not now" most prominent, an aggregate-only public profile with no hour-of-day data, an ingest firewall that rejects free text, sealed reveals, published retention (DESIGN 4, 8) | Strong local privacy controls. Public profiles and session pages show per-session detail, and a submission uploads the whole project's prompts |
| Reach beyond native support (R7) | Live hooks as well as log readers, a generic hook mode driven by mapping files, out-of-process plugins in any language, the WakaTime bridge for 46 agents, and Karim's AGENTS.md entry with `pairtime track` as the universal fallback, each with a provenance level that sets how much its claims count (DESIGN 5.1 to 5.4) | Log readers, plus one generic hook receiver that guesses field names. No fallback for harnesses without logs or hooks |
| Old Windows laptops | A `windows/386` target, EcoQoS, footprint gates in CI, code signing applied for in week one, a VSIX channel, and real pre-2017 laptop reports as a 1.0 gate (DESIGN 2, R8; 3.12; 9.6) | amd64 and arm64 only, unsigned, not yet run on Windows |

### 4.3 What the comparison says

grit is ahead on time, which Karim's team can't buy back: it exists, it already has the screens a program author would want to see, and it will likely have a server up first. pairtime's design is ahead on the parts Hack Club has to sign off on: calibration against reviewer judgments, evidence a skeptical reviewer can check, and a path into the forms programs already use. Those are the parts R2, R9, R10 and A turn on, and grit's plan schedules only a small part of them.

The risk on pairtime's side is its own size. A plan of 140 work packages has to land M4 by 12-18 and the calibration freeze by 01-04 (PLAN) to keep the advantage it has on paper. The risk on grit's side is that being first to a server isn't the same as being first to 1,000 weighted projects, which needs programs and HQ, and nothing in grit's plan dates either.

## 5. Options for Karim

This section lays out the choices and what each costs. DESIGN 13.1 item 7 records the choice, made on 2026-11-20: compete (5.2).

### 5.1 Facts every option has to live with

- **One winner.** The prize goes to the first system that meets the requirements and has 1,000 weighted projects, about 10,000 approved hours, in the Unified YSWS DB. A group can win, but a group leader submits for it and is responsible for communicating the split (B, Acceptance and Eligibility). Any shared entry needs that agreed in writing before it matters.
- **Programs decide.** Zach: "So you need to actually get programs to use it." Programs and Quality and Integrity have limited attention for new trackers, and two new trackers asking them for the same things at the same time compete for it.
- **Licenses fit.** Both are AGPLv3. pairtime's files say AGPL-3.0-or-later, so its code can go into grit as is. grit says "AGPLv3" with no "or later", so grit code copied into pairtime is safest treated as version 3 only unless Niko says otherwise. That changes nothing in practice today. Either way the copyright notices stay, changes are marked, and a modified server offers its source to its users (AGPL sections 4, 5 and 13).
- **grit has no contribution barrier.** No CLA. CONTRIBUTING.md asks that contributions be AGPLv3 and follow CLAUDE.md's hard rules (no cgo, no dashes, synthetic fixtures, no secrets). Phase 3's collectors are `[ticket]` items written to be handed to "any contributor or model". The `[core]` items, scoring and trust, are written or reviewed line by line by grit's lead, so scoring changes go through Niko.
- **The two scorers don't merge cleanly.** grit credits windows around turns; SM credits seconds by anchor, presence and focus. A combined system would pick one, or report both.

### 5.2 Compete

Build pairtime as planned and race grit to programs.

What pairtime has to ship first to win, in the order the race needs it:

1. **The HQ asks, which need no code.** The counting rule, the Quality and Integrity brief with the evidence-level question, and the native-program proposal are all week-one items (DESIGN 9.2, 9.5, 9.6). grit's plan has none of them, so this is the one place pairtime can be first without out-building anyone.
2. **A program-ready path before grit grows one.** M4's submission-to-Airtable round trip with the handbook fields filled (PLAN, by 12-18), the review desk, and the native program opening 12-14 (DESIGN 9.4). This is what grit lacks most.
3. **Parity on the basics grit will have first.** An installable Windows client with Claude Code (M1, 12-01) and Codex (M2, 12-08), a hosted server with Hack Club sign-in (M3, 12-11), and signed Windows binaries. These don't win the race, but missing them loses it.
4. **The accuracy evidence.** Scoring 1.0.0 and the R2 report by 01-04, with grit's rules as a published baseline (DESIGN 6.7). If pairtime's hours match reviewers better than grit's, that table is the argument a program author can repeat.
5. **The coach in the first alpha**, for R3 and for the installs routes B and C need (DESIGN 3.8, 9.1). PLAN already splits it: alpha 0 on 12-01 has `today`, `recap` and `statusline` in the terminal, and the session recaps, moments, notifications and welcome recap come with alpha 1 on 12-08.

DESIGN 9.7 already sets the alarm: if the program path slips past mid-December, the coach misses the first alpha, or the calibration report misses the first January decisions, plan as if grit reaches a program first.

| For | Against |
|---|---|
| Karim keeps the design, the direction in B and the whole prize | pairtime starts about three to four weeks behind on running software, by my estimate in 2.3, against a rival that finished three phases on its first day |
| The pieces grit's plan lacks, the program path, calibration and checkable evidence, are pairtime's strongest | 140 work packages, and the dates only hold at about four a day |
| A rival with a published baseline makes pairtime's R2 report more convincing | Two trackers pitching the same programs and HQ split their attention, and whoever reaches a program first may set the expectations for both |
| | Both teams build collectors, installers and dashboards twice |

### 5.3 Contribute to grit

pairtime's team works upstream, either alongside pairtime or instead of it.

The most useful contributions, roughly from cheapest to most involved: phase 3 collectors, where pairtime's harness research already maps the Cursor, Copilot, Gemini CLI and Windsurf formats, and Codex's hooks (HS 4 to 8); harness automation labels such as `/loop`, `/goal` and `-p` runs as extra flags, which fit grit's rule that flags never change credit; the program kit, meaning the handbook text, the Airtable script and the counting rule, which grit's plan has nothing for; and SM's ideas as scoring proposals, which need Niko's agreement because scoring is `[core]`.

| For | Against |
|---|---|
| The fastest way to put a working tracker with a program path in front of programs | pairtime's team doesn't control grit's scoring, trust model or plan |
| No duplicated work, and the commons gets better whoever wins | Unless a group is agreed first, a grit win pays pairtime's team nothing |
| Goodwill with another builder and with Hack Club, which values open source | Karim's direction in B, the AGENTS.md entry and the server on Karim's domain, may not fit grit's plan |
| | Contributing collectors while competing hands the rival the breadth it lacks |

### 5.4 Share adapters, fixtures or calibration, and keep two entries

- **Adapters.** Both are Go, but the interfaces differ: grit's `Collector.Parse` returns sessions of three event kinds, and pairtime's `Adapter.Parse` returns events of the 18 kinds in PR1's model, each with a provenance value (DESIGN 3.4). A shared library would need a shared event type. Sharing format notes and synthetic fixtures is cheap; sharing parser code costs a negotiation over the model.
- **Calibration.** pairtime's pilot will already score grit's rules against labeled minutes. Publishing per-rule results, for example which of grit's windows over- or under-credit against raters, helps both trackers and Hack Club's reviewers. Raw labels and recordings can't be shared under the consent the pilot plans to ask for, because participants opt in for pairtime's purpose and recordings are deleted 30 days after labeling (DESIGN 6.7). Aggregates and the fitting code can be.
- **Red-team findings.** Each team can test the other's rules with its own fixtures and report privately first.

| For | Against |
|---|---|
| Low cost, and both entries get more accurate | Calibration results are pairtime's best argument; published before pairtime's 1.0, they help grit catch up on the one axis where pairtime leads |
| Keeps the full-prize path open | Nothing guarantees the sharing goes both ways |
| Gives Hack Club better evidence about AI time tracking in general | |

### 5.5 Merge efforts

One entry, one group leader, an agreed split. Three shapes:

1. **grit as the base.** grit's running client and server, with pairtime's program path, calibration, coach and evidence model added over time. Fastest to a hosted tracker with a program plan.
2. **pairtime's design as the base.** grit's collectors, idle samplers, autostart and dashboard code ported into pairtime's architecture. Keeps pairtime's model, and throws away much of grit's head start.
3. **One program path and evidence spec, two clients.** The server, review desk and program kit are shared and each client keeps its scorer. Probably the most coordination for the least speed.

| For | Against |
|---|---|
| Combines grit's lead on running code with pairtime's lead on the parts HQ has to approve | The scoring models, submission models and trust designs conflict, and someone has to give ground on each |
| One tracker asks programs and HQ for one set of things | Needs agreement on governance, on Karim's direction in B, and on the split, before the work starts |
| A larger team for a 1,000-project target | Coordination slows whoever is currently faster, which today is grit |

### 5.6 In between

- **Compete on the program path, cooperate on the commons.** Open a friendly line to Niko now, trade harness-format notes and synthetic fixtures, and keep separate scorers, servers and programs. It is cheap and keeps every later option open, at the price of helping the entry that is ahead.
- **One vocabulary for HQ.** Zach said HQ "will work with Quality and Integrity to provide good guidance around how to use new time tracking tools" (B). Both trackers label time witnessed or backfilled and grade their evidence, grit in code with its trust tiers and pairtime in its design with levels and bands. Agreeing on terms, or asking Quality and Integrity together, could mean HQ writes one guidance page for new trackers instead of two. It reveals some of pairtime's adoption plan to a rival.
- **Decide on a trigger.** Compete now and revisit on a date or event: DESIGN 9.7's mid-December alarm, grit announcing a program, or HQ publishing guidance. It costs nothing today. A merge gets harder the longer the codebases grow apart, and programs that pick a tracker in December may not switch in January.

### 5.7 What would make the choice easier

Three answers would narrow this down. Does Niko plan to run a program, or to approach HQ, and when? Would Niko consider a group entry, and on what terms? And what does Quality and Integrity say about evidence for new trackers: if HQ asks for the kind of checkable evidence in pairtime's design, competing gets more attractive; if it accepts grit's tiers and full prompt review, the case for joining forces gets stronger.

## Fact-check log

Checked on 2026-11-19, 22:30 to 22:58 UTC, against grit's source at the pinned commit (read from a tarball through `gh api`) and its commits, releases, tags, issues and CI runs, and against DESIGN, PLAN, SM, AGENTS.md, JE and pairtime's PR list. grit's `main` was still at the pinned commit, so nothing changed upstream. About 57 claims were checked. These were wrong or incomplete and are fixed above:

| Where | The doc said | The source says | Fix |
|---|---|---|---|
| Header | PA 1.14 read grit 3 h 21 min earlier | `e7cf94e` 04:36:45, pinned commit 07:57:14: 3 h 20 min | Corrected |
| 1, 2.2 | Phases 0 to 2 done apart from the `grit install` test | TODO-NIKO.md also has the by-eye check for a macOS permission prompt, part of 1.1's "done when" | Added |
| 1.1 | 22 CI runs on `main` | 20 on `main`, 2 on the `rc.1` and `rc.2` tags | Corrected |
| 1.1 | Until a full release, testers build from source | True for current features; `GRIT_VERSION` reaches the prerelease, which lacks `install` | Added |
| 1.3 | Collector line ranges cited for "nothing is installed" | Those lines are the prompt rules; `collect.go:1-5` says nothing is installed | Citations moved |
| 1.4 | The `low-effort` tag never changes credit | It marks the prompts the 90 s cap applies to | Corrected |
| 1.4, 4.1 | The whole rulebook fits in one table on the public page | The page has the v1 rules and three flags; v2 and the four newer flags are in the README | Corrected |
| 1.5, R9 | The server recomputes the anomaly flags | Three of four; `no-input-prompts` needs client-side activity data | Corrected |
| 1.5, R6 | Sync sends numbers, rule strings and hashes; prompts leave after a preview | Sync also carries project name, branch, remote, commit subjects and prompt lengths. A submission covers 90 days and includes file names; the dashboard previews it, `grit submit` without `-dry` doesn't | Added |
| 1.6 | Verified and fixture-only lists | `grit doctor` ran on the Mac; attestation and tiers are tests and demo only | Added |
| R7 | Browser chat has none planned | No extension is planned, but the README names browser chat as a `grit hook` case | Reworded |
| R9 | A prompt with no input earns nothing under v2 | It earns no think credit; its run still counts if there was input during or just after it | Corrected |
| 4 | PR #4 is open | PRs #4 to #7 are open | Updated |
| 4.1 | JE said the trust layer is built before Fraud Squad wants it | JE said it of the evidence-first design: "built before anyone knows whether Fraud Squad will accept it" | Quoted exactly |
| 4.2 | grit's dark-flow credit is unchanged | Flags don't change credit, but the 90 s go-ahead cap does | Added |
| 4.2 | Hackatime-equivalent seconds sit next to every credited figure | Only on the review page, for teens who also send to Hackatime (DESIGN 7.2) | Corrected |
| 5.2 | Claude Code and Codex in M1 | Codex is in M2, by 12-08 (PLAN) | Corrected |
| 5.2 | The coach ships in the first alpha | DESIGN says so, but PLAN puts most of it in alpha 1, 12-08 | Added |
| 5.2 | grit "ships a phase a day" | A forecast; the fact is three phases on day one | Reworded |
| 5.6 | Both trackers already use witnessed time and evidence tiers | pairtime's are design only | Reworded |

Confirmed as written: every v1 constant and line reference in 1.4, the 25 go-ahead phrases, the seven flags and their thresholds, the attestation construction with its 15-minute window and 5-minute skew, the 11 redaction rules, the sampler and bitmap, the idle sources, the daemon's intervals and memory limit, the watcher limits, the commit window, the release, its assets and its contents, the commit timeline and line counts, code size and the 59 test functions, PLAN.md's phases, the eight TODO-NIKO.md items, the absence of any program or YSWS DB step, the license facts, and on pairtime's side the milestone dates, WP-AG1's gate, the review desk WPs, `CHALLENGE_K`, the pilot, D10, the v1 harness list and the grit-derived parameters in SM.

Tone: the doc credits grit where it leads and keeps its gaps to what grit's code, PLAN.md and README show. A few phrasings that forecast or claimed more than the evidence were softened, on both projects' sides.
