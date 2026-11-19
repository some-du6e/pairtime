# pairtime design: teen first

Status: complete, ready for judging

One of three independent end-to-end designs for pairtime, written 2026-11-19 for the judging panel. This one optimizes for R3 (worth using for its own sake) and R4 (passive), and still has to meet every other requirement and the acceptance criterion (A). Requirement IDs refer to [docs/BOUNTY.md](../BOUNTY.md).

The bet behind it is that the hours that reach the Unified DB are a side effect of teens keeping pairtime installed. A tool teens keep for itself collects witnessed, presence-backed evidence every day, including on projects that never get submitted. When a teen does submit, the evidence is months deep, the devlogs are already written, and the reviewer's job is short. A tool installed only to get paid collects evidence for a few weeks before a deadline and gets uninstalled afterwards.

Sources are cited by short name and section:

| Short name | File |
|---|---|
| PA | [research/prior-art-and-measurement.md](../research/prior-art-and-measurement.md) |
| HS | [research/ai-harness-signals.md](../research/ai-harness-signals.md) |
| HC | [research/hackatime-and-hack-club.md](../research/hackatime-and-hack-club.md) |
| CP | [research/client-platform.md](../research/client-platform.md) |
| SP | [research/server-platform.md](../research/server-platform.md) |
| TP | [research/trust-and-privacy.md](../research/trust-and-privacy.md) (complete, but its fact-check is still pending, so claims that rest on TP alone could change) |
| SM | [design/struggle-metric.md](struggle-metric.md) |

## 1. Pitch

pairtime is a coach that lives in the tray of a teen's laptop and notices what they did while building with AI: the prompts they wrote, the agent edits they read and rejected, the bugs they fixed with their own hands, the tests they got passing. It turns that into a recap after every session, a project story over weeks, devlog drafts in the teen's own voice, and a public profile of consistent building. The number underneath all of it is credited human seconds of productive struggle, computed by the rulebook in SM, and that same number is what a YSWS program receives when the teen chooses to submit.

### What a teen experiences

**Install, once.** Maya is 14 and has a 2016 Windows 10 laptop with 4 GB of RAM and a spinning disk, the machine CP plans for. She finds pairtime in her club channel or on a YSWS page and either clicks Install on the VS Code/Cursor extension, which bundles the binary (CP 4.2), or pastes `irm https://pairtime.<domain>/install.ps1 | iex` into PowerShell. Neither needs admin rights. The installer finds Claude Code, Codex, Cursor, Copilot and Gemini CLI on her machine and writes their hooks (HS 16). It adds pairtime as a second destination in `~/.wakatime.cfg` if she already uses Hackatime, so she loses nothing (HC 1.5). It registers an HKCU Run key so the agent starts at logon (CP 2.1). Then it shows one screen. On the left is a live preview of her public profile, which by default shows only aggregates (daily totals, build days, languages, and projects whose repos are already public), next to a one-click "Make private" (R5; TP B7; section 7). On the right are two opt-ins whose most prominent button is "Not now", as New York's rule for minors' consent asks (TP B2 point 7): counting keystrokes as numbers only, which makes her minutes evidence level A (SM 5.1), and joining her club's board. She signs in with Hack Club Auth through a device code (SP 3.1, 3.5). That is the last setup she ever does.

**The first minute pays off.** Before she has done anything new, pairtime reads the last 30 days of her Claude Code and Gemini CLI transcripts and her older Codex and Copilot logs (SM 2.8, HS 2) and shows a welcome recap: "Since October 20 you built on 4 projects on 17 days. Your clock: about 23 h. Claude's clock: 61 h." Lookback minutes are evidence level C and are labeled as such (SM 2.8). They make the dashboard useful on day one, but they never count toward public consistency or submissions unless a program says it accepts them (section 5).

**Every day, nothing to start.** She opens the lid at 07:40, types a prompt into Cursor, fixes a string by hand, commits, and closes the lid at 07:44:45 to catch the bus. pairtime credits 4 min 25 s to `remind-bot` (SM 6.2). That evening she jumps between three projects and two agents, and each second goes to exactly one project (SM 3). The tray icon's tooltip shows today's time. She never presses start or stop, and she never picks a project from a list.

**After each session, a recap.** When a work session ends (no credited minute for 20 minutes, a lock or a suspend; SM 5.3 `SESSION_GAP_S`), one quiet Windows notification appears: "52 min on remind-bot. You fixed the timezone bug yourself and asked 3 why-questions. See recap." The recap opens in the browser she already has open, served by the local agent, so nothing with a webview stays in memory (CP 5.3). It shows:

- **The chess clock**: her time next to the agent's, as information, never as a target ratio (PA 4.3 item 5).
- **Moments**: the first green test, the agent edit she rejected, the error she fixed by hand, the commit.
- **What you learned**: the error families she beat (`TypeError`, Rust `E0502`) and the questions she asked that began with "why" or "how does". These come from her own prompts, and the prompts never leave the laptop (section 7).
- **Loops caught**, framed as coaching rather than judgment: "The agent rewrote the same 8 lines of `Navbar.css` 17 times between 20:05 and 20:38. Your own edit at 20:41 fixed it. Next time, try editing it yourself after the second retry." (SM 6.5; PA 4.4 on shame).

**Every week, a project story and a devlog draft.** On Sunday, or when she clicks "New devlog", pairtime puts the week's recaps together into a draft for each project. The draft lists facts (what changed, the bug that fought back, the first passing test, credited time since her last devlog) and leaves the sentences to her. She edits it into her own words and posts it to her pairtime profile, and to her YSWS program if she is in one. Hack Club programs already review time devlog by devlog (HC 3.3), so a devlog drafted from evidence is both the thing she wanted to write and the unit a reviewer approves. If she wants help with the wording, she can ask her own agent ("write my devlog"). The pairtime skill tells it to run `pairtime story --json` and draft from that output, and the post is then labeled AI-assisted (section 4).

**Over months, growth and consistency.** Her logbook records firsts (first test written, first bug fixed after the agent gave up, first agent edit rejected) and badges for the behaviors the research ties to learning: asking conceptual questions, fixing errors by hand, verifying before accepting (PA 2.2, 4.3). There are never badges for hours. Consistency is counted in build days. A build day is a local day with at least 15 credited minutes backed by presence, which is Hackatime's 15-minute rule applied to credited time instead of heartbeats (SM 8.6 `STREAK_DAY_S`). Her profile says "built on 18 of the last 30 days". There is no daily streak counter, because GitHub's natural experiment showed that streak counters produce one-contribution token days (PA 4.1). Instead she picks a weekly goal (say 3 build days), and pairtime counts the weeks in a row she meets it. Every 7 build days earn a freeze that covers one missed week, and she can hold 2 (Duolingo's forgiveness; PA 4.1). Her club's board ranks build days over a rolling 90 days, which is Strava's Local Legend, and never ranks hours (PA 4.1, 4.3). A jiggler earns nothing toward any of this, because credit needs a human act (SM 2.1). A missed day can't be filled in later either: build days count only presence-backed minutes the server received within 72 hours, so lookback and after-the-fact edits never count, while a teen with no internet at home still gets the days she synced at school the next morning (section 5, D1).

**Privacy she can see.** The dashboard has a "what left this laptop" page that lists every upload in plain words: minute counts, reason codes, hashes, and nothing else (TP B6). Before any submission, `pairtime submit --dry-run` shows the exact bundle (PA 4.4). Her public profile shows daily totals, projects linked to public repos, her build days and the devlogs she chose to post. It never shows prompts, file names, code, hour-of-day patterns, her real name, school or timezone (TP B7).

### What a reviewer sees

The reviewer follows the pairtime link in the program's `Additional Justification` field (HC 3.7) or a Robert/Ari deep link, signed in with Hack Club Auth with a reviewer role (SP 5.3). The submission page shows:

1. **A header with three numbers**: credited hours, what Hackatime would have logged, and what the handbook's one-third rule would allow. For example: "pairtime 5.9 h; Hackatime-equivalent 9.1 h; handbook third 4.2 h; 97% witnessed; 62% of AI-assisted time credited, backed by 212 typed prompts, 38 permission decisions and 9 interrupts" (SM 5.4).
2. **The teen's devlogs down the left side.** Each claim in them ("fought the collision bug for 40 minutes") links to the minutes behind it. The story is the reviewer's index into the evidence, so they read a narrative first and drill down only where it doesn't add up.
3. **Rows a reviewer can cut**, one per commit interval, shaped like Ari's settlement rows (HC 3.6): captured seconds, activity mix, anchor counts, flags, evidence level, and a "why these seconds" drawer that replays the reason codes minute by minute (SM 5.2).
4. **Flags and the audit draw**: tier-2 flags (public code, private thresholds; section 5, D13), and whether this submission was drawn for a deep audit (section 6).
5. **Revealed evidence**: the prompts the server challenged after the metadata reveal, redacted by the teen and checked against commitments the server stamped at the time, plus any extras the teen chose to attach (TP B6).

The reviewer approves as credited or cuts rows with a reason. The result goes back to the program in the handbook's own format.

### What a program author does

There are three levels, and each works with today's handbook (HC 3.7, 6):

1. **No code.** Add a "pairtime link" field to the submission form. The link's page generates the `Alternate Tracking Method` text and the review URL for `Additional Justification`, ready to paste into the Airtable fields programs already have (HC 3.7). This is the route for the low-ceremony Airtable-form programs that collect only self-reported hours today (HC 3.2).
2. **A few lines.** Point a second instance of the program's existing `HackatimeService` at pairtime's Hackatime-shaped endpoints (`stats`, `authenticated/me`, `trust_factor`) through HCA OAuth with scoped tokens (HC design implication 9).
3. **Full.** Use the submissions API with `externalId` idempotency and HMAC-signed webhooks in Robert's conventions (HC 3.5, SP 10.2). Later, use a `pairtime` source in Ari and a `pairtime` kind in Robert, once those teams accept them (HC 3.5, 3.6).

The first program is a pairtime-native YSWS, working name **Hard Mode** ("ship something that fought back"), run on the standard stack, which Zach allows (BOUNTY.md, "Yes!"). Section 8 has the rest.

## 2. Requirements

Each requirement gets how the design meets it, why I believe it will, and what would prove it. R3 and R4 get the most space because this design stands or falls on them.

### R1. AGPL-3.0

- **How.** Client, server, coach, scorer, adapters, VS Code extension and web UI all live in one AGPL-3.0 repo, and the LICENSE is already there (BOUNTY.md checklist). Every dependency the client stack needs is permissive (BSD, MIT, Apache-2.0) and can be combined into an AGPL binary (CP 7). A CI job fails the build on any dependency whose license isn't on an allowlist (section 10, WP0.2).
- **One deliberate choice.** The trust research puts tier-2 detection in a private module run as a separate process, the way Ari does (TP A8 #9; HC 3.6). I keep all detector code public and make only the thresholds and weights private. They are deployment configuration loaded at runtime, so they hold no code. An attacker who can read the code still can't tune against values they can't see, and no reviewer of R1 can point at closed code. Hack Club may still run extra private detectors of its own as a separate process. That is a deployment choice, and the shipped product doesn't need it (TP open question 7).
- **Why I believe it.** The only open question is the private-module pattern, and this design avoids it.

### R2. Accurate hours of productive struggle

- **How.** I adopt the struggle-metric spec as the credit rule (SM; section 5 lists my deviations). A second counts only when a human act opened a window over it, the OS saw the person present, and focus was on that project. Reading is credited from a bucket bounded by output size, and four dark-flow detectors plus a no-progress half rate remove the slot-machine loops (SM summary). Every constant is fitted to reviewer labels in the pilot, with these ship targets: minute-level κ ≥ 0.6, session mean absolute error ≤ 15%, bias between −5% and +3%, and reviewer-approved over credited ≥ 0.9 (SM 7.3).
- **Teen-first addition.** Every recap has a "Was this right?" control with four answers ("about right", "I was thinking away from the keyboard", "I wasn't really working then", "it missed something"). The answers stay on the device unless the teen opts in to share them as labels, which is consent-gated (TP B2 point 7). They never change credit, because people misjudge their own AI-assisted time by about 40 points (PA 2.2, METR). What they do is point the labeling effort at the sessions where the rules and people disagree most, which is where R2 is won or lost.
- **Why I believe it.** The rule credits the activities the research identifies as the human's part (steering, verifying, integrating, debugging; PA 2.2, 3.1) and zeroes the agent's runtime, which is what the vision post asks for (BOUNTY.md). The worked examples in SM 6 come out where a reviewer would land. Three hours of real flow credits 93%, an overnight `/goal` credits 13 minutes, and a scripted loop credits 0. A teen-first tool also gets more labeled data, because teens who keep it installed volunteer more sessions.
- **Proof.** The pilot report in SM 7.3 against the four baselines (Hackatime raw, the handbook third, grit, Claude Code `active_time`).

### R3. Worth using for its own sake

- **How.** The daily product is a coach. It gives a recap after every session, a project story, devlog drafts, a logbook of firsts and beaten error families, habit trends, build days with a weekly goal and freezes, club boards ranked on consistency, a chess clock of your time against the agent's, and a welcome recap built from lookback on the first run (section 1). All of it is computed on the device from data that never leaves it (section 7). None of it shows a prize meter.
- **Why I believe it.** Each feature is the research's own recommendation for motivating teens (PA 4.3, items 1 to 6, and PA design implication 15):
  - Positive informational feedback raises free-choice motivation (d = 0.33), and engagement-contingent tangible rewards, which is what YSWS hour prizes are, lower it (d = −0.40), most of all for children (PA 4.2, Deci, Koestner and Ryan). So the coach talks about what the teen did and learned and never about hours earned.
  - Consistency awards over a rolling window reach more people and are harder to farm than hour totals (PA 4.1, Strava Local Legend). Forgiveness keeps streaks from turning into anxiety (PA 4.1, Duolingo).
  - Devlogs are already homework for every devlog-based program (HC 3.3). A tool that drafts them from evidence saves the teen real time, and the saved time is the reason to keep it installed between programs.
  - The welcome recap gives value in the first minute, before the teen has changed anything (SM 2.8, HS 2 finding 5).
  - The feeling of being watched is what gets monitoring tools uninstalled. 58% of monitored students self-censor (PA 4.4, CDT). Coaching that runs locally, plus a page that shows exactly what left the laptop, is the countermeasure.
- **What would prove it.** Two numbers the server already sees in tier-1 data, with no extra telemetry: 4-week retention of devices that sync, and the share of credited time on projects that are never submitted. That second number is the BOUNTY.md checklist item ("Teens keep it installed for projects they never submit"). Targets for the alpha cohort: 50% of devices still syncing after 4 weeks, and at least half of credited hours on never-submitted projects.

### R4. Passive

- **How.** Nothing happens per session. One install writes the autostart entry (HKCU Run, a LaunchAgent, or XDG autostart, none of which needs admin; CP 2.1), harness hooks (HS 16), the WakaTime second destination (SP 8.8) and `.git` watchers for every repo a session touches (CP 3.2). Projects come from the repo's root commits, so the teen never picks one (SP 4.2). Each second is attributed to exactly one project from focus, with look-ahead for terminal tabs (SM 3.2). Sleep, lock and lid-close end credit and flush the spool (CP 2.2). On the first run, lookback over 30 days of transcripts covers the time before install (SM 2.8).
- **The three stories.** Each is a worked example in SM, with nothing started or stopped. Three hours of flow credits 2 h 48 min (SM 6.1). Five minutes before school credits 4 min 25 s on a level-B Windows laptop and is witnessed because the suspend triggers a flush (SM 6.2). Five projects in an afternoon split 1,800 s of wall time into 510/450/540/300 s with nothing counted twice (SM 6.3).
- **Why I believe it.** Every capture path is something the OS or the harness pushes to us: hooks, file notifications, focus events, an idle sample every 15 s (CP 3.3). None needs the teen to remember anything. Three one-time steps remain, and they belong to the tools, not to us: Codex asks the user to trust new hooks once in `/hooks`, and until they do, the rollout reader covers it (HS 6.1, 16). Claude Code fires hooks in a folder only after its own trust dialog, which the teen answers anyway (HS 16). Cline needs an "Enable Hooks" tick (HS 17.13). Zach's "lookback mode counts as passive" covers the rest (BOUNTY.md).

### R5. Server with accounts, public profiles, projects, git, live dashboard

- **How.** One Go server, `pairtimed` (SP 1). Accounts come from Hack Club Auth OIDC, and the desktop signs in through a device grant (SP 3.1, 3.5). Profiles are public by default and aggregate-only: daily totals, build days, languages, harnesses, and projects whose repos are public (SP 5.2, TP B7). Projects are keyed by a root-commit fingerprint (SP 4.2). Git integration means commit records with credited time since the parent, server first-seen times, forge verification for public repos and an optional GitHub App (SP 4.1, 4.3). The owner's dashboard updates over SSE within about a minute of activity (SP 6.4; real-time caveat in section 5, deviation D6).
- **"Public by default" and minors.** TP B7 prefers an onboarding choice with nothing preselected. R5 asks for public by default. I read the aggregate-only page as part of the service the teen asked for, so it is public by default, with the preview and a one-click "Make private" on the onboarding screen. Hack Club records the UK Children's Code "compelling reason" assessment that TP B7 point 1 calls for. Its case is that relatedness is one of the three needs behind R3 (PA 4.2), and that the page reveals no routine, place or name (TP B7 points 2 and 3). Everything beyond aggregates is opt-in: live "currently building" presence, club boards, project names of private repos, badges and devlogs (TP B7). If counsel rules the other way, the fallback is the unselected choice, which is one config flag.
- **Why I believe it.** Every piece is a known pattern sized in SP: about 5,000 SSE streams in about 100 MB, writes batched to at most 20 commits/s, and dashboards reading rollups (SP 6, 7.4). grit already runs the same shape in Go with SQLite and SSE (PA 1.14).

### R6. Privacy

- **How.** The four disclosure tiers in TP B6. Raw prompts, transcripts, file paths, window titles and per-event input features never leave the device in the default path. The server rejects any upload field that could carry free text with a 422, so the promise doesn't rest on the client alone (SP 8.2). There are no screenshots, no keystroke content and no hardware ids (TP B1). `pairtime privacy show` prints every tier-1 field of the next upload (TP B8). Raw prompt text is kept for 30 days and encrypted per field with a key from DPAPI, the Keychain or Secret Service (CP 5.2, TP design implication 20). Reviewers' access to submitted evidence is logged, and the teen can see that log (SP 10.3).
- **Teen-first strengthening.** The most personal analysis in the product happens on the device and stays there: classifying prompts as conceptual questions or delegation, tracking skills, building recaps and drafting devlogs. The server never profiles logged-only hours. Risk scoring runs only on windows the teen submits, as TP B2 point 6 asks. A teen who never submits shares numbers and hashes and nothing else.
- **Why I believe it.** This is the list privacy-conscious developers accept: local collection they can inspect, aggregates instead of content, opt-in for anything content-bearing, random install ids, and readable open-source upload code (TP B1, the Go telemetry model). Section 7 has the inventory.

### R7. Every model and harness

- **How.** pairtime never looks at the model. It reads what the harness does. v1 ships hook and log adapters for Claude Code, Cursor, Copilot, Codex, Gemini CLI and Devin Desktop, a Claude Code OTel receiver, and WakaTime dual-send for every editor with a WakaTime plugin (HS 16; section 4). New harnesses plug in three ways. An in-tree Go adapter implements one interface. An out-of-process plugin in any language writes normalized events to a local inbox. Any harness with Claude-style hooks can use the generic hook mode with no code. Karim's AGENTS.md line plus `pairtime track` covers the agents that have none of these (HS 11.4).
- **Why I believe it.** All six top harnesses now have prompt-submit hooks shaped like Claude Code's, and several read Claude Code's own settings file (HS 2 finding 1). The normalized event schema means a new harness never touches the scorer (SM 1). Coverage of the long tail is mapped in HS 15.

### R8. Windows, macOS, Linux on a 10-year-old laptop

- **How.** One pure-Go binary per OS and architecture, cross-compiled from one Linux job (CP 1.6, 7). Nothing with a webview stays resident. The dashboard opens in the teen's own browser instead (CP 5.3). The agent opts into EcoQoS on Windows (CP 2.2). It watches only harness log roots and each repo's `.git`, never working trees (CP 3.2). Hooks are native exec-form binaries, never `npx` or an interpreter (CP 6.1). The coach adds nothing resident: recaps are a SQL pass over one session's rows when the session ends, and lookback runs once at low priority with a progress bar.
- **Budgets, as release gates** (CP 7, CP 9.2): binary under 15 MB, agent RSS under 25 MB on Windows, idle CPU under 0.1% averaged over an hour at about 10 wakeups a minute, and a warm hook round trip under 50 ms on a 2016 dual-core with a hard drive.
- **Why I believe it.** Go starts at 3.6 MB RSS, and the sampling loop is a handful of syscalls a minute (CP 1.2, 3.3). CI runners aren't old laptops, so the pilot's strata put at least 40% of sessions on Windows, including machines from before 2017 (SM 7.2). The budgets are measured there before launch.

### R9. Trustworthy

- **How.** The credit rule is the first defense, because agent-only time, mouse-only input and automated turns earn nothing under it (TP A8 #1). Hash-chained, witnessed records defeat replay and backdating (TP A8 #2). Commits anchor submissions, and uncommitted time is capped at 60 minutes (TP A8 #3; SM 4.3). Tier-1 automation labels (TP A8 #4) and the opt-in typing evidence (TP A8 #5) handle commodity tools. Risk-weighted audits with a short live interview, payment after review, and hours that stay revocable handle what no rule can see (TP A6). Section 6 has the details.
- **Teen-first contribution.** A teen who has used pairtime for months has a public build-day history behind every submission. A fabricated project has to fake that history live, witnessed, for weeks, which multiplies the cost of the full puppeteer (TP A6 U3, "consistency over weeks"). Reviewers see that history because it is already public. No profiling of logged-only hours is needed to use it.
- **Why I believe it.** In SM 6 the worked examples credit 0 for a scripted `claude -p` loop with a jiggler, 13 minutes for an overnight `/goal`, and only the work outside the loop for an hour of dark-flow re-rolling, while three hours of real flow gets 93%. The R9 test is approved over credited ≥ 0.9 per submission (SM 7.3), measured in the Hard Mode pilot.

### R10. Auditable

- **How.** Every credited second carries a reason code and the ids of the anchors behind it, so a reviewer can recompute any minute by hand from the public parameter table (SM 5.2). The server re-scores every submitted window from the revealed metadata and flags any mismatch (SM 7.6). Each submission produces a signed, self-verifying bundle, and an open-source `pairtime verify` re-runs the chain and the scorer offline (SP 10.4). A daily Merkle root of everything received goes to a public git repo, so auditors don't have to trust pairtime's server either (TP B6). Logged hours and submitted hours get different guarantees, as R10 allows. Logged data stays local, and submitted windows reveal metadata plus a server-chosen sample of prompts (TP B6).
- **Teen-first contribution.** The teen's own devlogs become the reviewer's table of contents. Each claim links to the minutes behind it, so an outside party reads a narrative and drills down to the reason codes only where something doesn't add up (section 6).
- **Why I believe it.** Fraud Squad, program reviewers and HQ spot-checkers get the same bundle, and the handbook's spot-check rule ("Required evidence ... cannot be located") is met by keeping bundles for the spot-check window (HC 3.7; SP 10.4).

### R11. Hosting under $500/month at 15,488 MAU

- **How.** The client computes credit, and the server stores derived rows, about 1 GB a month (PA 5.2). One small Go process with SQLite and Litestream serves it. The coach costs the server almost nothing, because recaps, skills and drafts are computed on the device. The server stores only published devlog text, enum badge codes and club memberships.
- **Why I believe it.** About $49 a month minimal or $150 with a warm standby (SP 9.4), which is 10 to 30% of the cap. The teen layer adds about 100 MB a month of devlogs and badges and no measurable compute. With code-signing fees, the recommended setup comes to about $168 a month, 34% of the cap (section 9). There are no server-side LLM calls (the teen's own agent polishes devlogs, if they want), no stored transcripts for logged hours, and no screen capture, which are the three things that could break the budget (PA 5.5).
- **Proof.** A load-test workflow in GitHub Actions at the 8x event peak, against a container limited to 2 vCPU and 8 GB (WP7.7).

### A. 1,000 weighted projects in the Unified DB

- **How.** This needs about 10,000 approved hours (BOUNTY.md). There are three program routes, all of which work under today's handbook through `Alternate Tracking Method` and `Additional Justification` (HC 3.7): Hard Mode, the pairtime-native YSWS, which Zach allows; existing programs that already have to defend the one-third rule; and the low-ceremony Airtable-form programs that have no tracker at all (HC 6, step 6). Section 8 has the steps, targets and timeline.
- **Why I believe it.** Three reasons, all specific to the teen-first angle:
  1. Programs adopt what their participants already have installed, and the install base is the asset.
  2. A tracker whose number is already close to what a reviewer would approve saves the reviewer most of the work that turned Stardance's 123,400 hours into 27,600 (HC 3.2). At Stardance's 22% approval rate, 10,000 approved hours would need about 45,000 submitted. At pairtime's target of 90%, it needs about 11,000.
  3. Devlogs drafted from evidence match the unit programs already review (HC 3.3), and they map onto Ari's `journals[]` ingest without any HQ change (HC 3.6; section 8).

## 3. Architecture

### 3.1 Shape

```text
 teen's laptop (one Go binary, pairtime)                         pairtime server (one Go binary, pairtimed)
 ┌───────────────────────────────────────────────────┐           ┌──────────────────────────────────────────┐
 │ harness hooks ──exec──> pairtime hook ─┐          │           │ ingest: dedupe, witness, re-anchor time  │
 │ AGENTS.md ─────────────> pairtime track┤ UDS      │  HTTPS    │ rollups + build days + boards            │
 │ wakatime-cli ──HTTP 127.0.0.1─────────┐│          │  batches  │ SSE hub (owner, profile, club topics)    │
 │ VS Code ext ───HTTP 127.0.0.1────────┐││          │ ────────> │ accounts (HCA OIDC), device grant        │
 │ Claude OTel ───OTLP 127.0.0.1───────┐│││          │  roots    │ projects, commits, forge verification    │
 │                                     vvvv          │  /5 min   │ devlogs, badges, clubs                   │
 │ pairtime agent (resident, per user session)       │           │ submissions, evidence, review desk       │
 │  presence 15 s · focus events · power · procscan  │  evidence │ program OAuth, webhooks, Hackatime reads │
 │  log tailers · .git watchers · spool + chain      │  on submit│ re-scorer (same pkg/score), verifier     │
 │  scorer (pkg/score) → minute buckets              │ ────────> │ daily public root → public git repo      │
 │  coach → recaps, moments, logbook, story, devlogs │           │ risk flags (public code, private params) │
 │  uploader · notifier · tray · local dashboard     │ <──────── │ SQLite WAL ─Litestream─> object storage  │
 └───────────────────────────────────────────────────┘  SSE      └──────────────────────────────────────────┘
          browser tab (local dashboard, 127.0.0.1)                 browsers · programs · reviewers · auditors
```

The design rule behind the split is that content stays where it was produced and only numbers travel. The client sees everything, so it scores and coaches. The server sees numbers, hashes and what the teen publishes, so it witnesses, aggregates, displays, and turns submitted windows into evidence (SP 1; TP B6).

### 3.2 Client processes

All roles are subcommands of one signed binary, so there is one thing to ship, sign and update (CP 7).

| Process | Lifetime | What it does |
|---|---|---|
| `pairtime agent` | Resident, one per user session, started at logon (HKCU Run, a LaunchAgent, or XDG autostart; CP 2.1). Single-instance lock: a named mutex on Windows, `flock` elsewhere | Everything in 3.3 to 3.7. Opts into EcoQoS on Windows; `ProcessType` unset or `Background` on macOS (CP 2.2) |
| `pairtime hook --harness <h> --event <e>` | Milliseconds, spawned by harness hooks in exec form | Reads stdin (size cap), stamps arrival time, applies the privacy policy, sends one framed message over the per-user Unix domain socket with a 100 ms timeout, falls back to an `O_APPEND` spool line, starts the agent if it's down, exits 0 and prints nothing (CP 6.2) |
| `pairtime track` | Milliseconds, run by an agent following the AGENTS.md line | Same path as `hook`, with `source = cli` (section 4.4) |
| `pairtime recap`, `story`, `devlog` | On demand: run by the teen or by the teen's agent through the coach skill | Print the latest recap or story, or a devlog draft, as text or `--json` (section 4.5) |
| `pairtime install`, `uninstall`, `doctor` | Once | Writes autostart, harness hooks, the WakaTime second destination and the coach skill files. Merges idempotently with other tools' entries and backs up every file it touches. `doctor` reports idle detection, hook latency, watcher state, signing and the adapters found (CP 7, CP 9.18) |
| `pairtime dashboard` | On demand | Opens `http://127.0.0.1:<port>/` with a one-time token that becomes a `SameSite=Strict`, `HttpOnly` cookie (CP 6.3) |
| `pairtime submit [--dry-run]` | On demand | Builds a submission's evidence, shows exactly what will be revealed, and uploads it (section 6) |
| `pairtime privacy show` | On demand | Prints every tier-1 field of the next upload (TP B8) |
| `pairtime verify <bundle>` | On demand, by anyone | Offline verification of a submission bundle (SP 10.4) |

Goroutines inside the agent:

| Component | Trigger | Output |
|---|---|---|
| `presence` | Every 15 s (`SAMPLE_S`): `GetLastInputInfo`, `CGEventSourceSecondsSinceLastEventType`, X11 `XScreenSaverQueryInfo`, Mutter `IdleMonitor` or `ext-idle-notify-v1` (CP 3.3) | `os.presence`. Gaps are computed from wall-clock values, never from `time.Since` (CP 2.2) |
| `focus` | Event-driven: `SetWinEventHook(EVENT_SYSTEM_FOREGROUND)`, `NSWorkspace` activation notifications, X11 `_NET_ACTIVE_WINDOW` (CP 3.3) | `os.focus` with `app_class` and `hosted_sessions`, from a process-tree walk on each focus change (SM 3.2). Window titles are never read unless the teen opts in |
| `power` | `PowerRegisterSuspendResumeNotification`, IOKit `IORegisterForSystemPower`, logind `PrepareForSleep`, session lock notifications (CP 2.2) | `os.power`. Suspend triggers a spool flush, a chain head upload and recap scheduling |
| `procscan` | Every 60 s and on focus change (`PROC_SCAN_S`) | Harness sessions that fire no hooks (`--bare`), with `parent_class` (SM 1.2) |
| `tailers` | File notifications plus a slow `stat` poll of hot files (CP 3.2) | `log` events from transcripts and rollouts, with a cursor per file |
| `gitwatch` | Non-recursive watch on each known repo's `.git` (CP 3.2) | `git.change` and commit records (SP 4.3) |
| `ipc` | Unix domain socket (all three OSes) and `127.0.0.1` HTTP with a bearer token and `Host`/`Origin` checks (CP 6.3) | Hook messages, OTLP, WakaTime heartbeats, plugin events, the dashboard API |
| `keycount` (opt-in) | Raw Input with `RIDEV_INPUTSINK` on Windows, XI2 on X11, a listen-only `CGEventTap` on macOS | Per-15-second key counts and an injected-input share. Key identities and per-key timestamps are never written (TP A8 #5, design implication 9) |
| `scorer` | Every 60 s, over a trailing 15-minute window | Minute buckets in `pending` or `final` state (section 5, D6) |
| `coach` | On each work-session end, nightly and weekly | Recaps, moments, logbook, habit trends, story, devlog drafts (3.6) |
| `uploader` | Every 60 s while active; within 10 s after a prompt or stop hook (coalesced); a batch root every 5 min | Signed batches (SP 8.2), roots (TP B6) |
| `notifier`, `tray` | On recap; on weekly story | A Windows tray balloon (`Shell_NotifyIcon` with `NIIF_INFO`, which I expect Windows 10 to show as a toast; not in the research, so verify on target builds), a Linux `org.freedesktop.Notifications` call over D-Bus, a macOS `osascript -e 'display notification'` (one short process per session end, never a polling loop; CP 3.3 warns only against AppleScript polling). The tray uses `fyne-io/systray` on Windows and Linux, whose non-darwin files need no cgo (CP 5.3). The macOS menu-bar helper waits for a Developer ID |
| `updater` | Daily | Signed manifest check, verify, swap (CP 4.6) |

### 3.3 Capture adapters

Every adapter turns what one tool exposes (hooks, logs, telemetry) into the normalized events of SM 1.2 and never touches the scorer (SM 1). The v1 set and how each one captures are in section 4.1. All adapters share one Go interface, so in-tree and community adapters look the same:

```go
type Adapter interface {
    Name() string                       // "claude_code", "codex", ...
    Detect(env Env) Detection           // installed? versions? config paths?
    Plan(env Env) []ConfigEdit          // idempotent edits for install; inverse for uninstall
    Sources() []Source                  // hook events, log roots + glob, OTLP, HTTP routes
    Parse(raw RawRecord) ([]event.Event, error) // pure; pinned per harness_version
    Fixtures() fs.FS                    // golden inputs and outputs per supported version
}
```

`Parse` is a pure function, so adapters are tested entirely from fixtures in CI, one directory per harness version (HS 17.7).

### 3.4 Local store

Paths are the ones in CP 7: `%LOCALAPPDATA%\pairtime\` on Windows, `~/Library/Application Support/pairtime/` on macOS, `${XDG_DATA_HOME:-~/.local/share}/pairtime/` on Linux.

| File | Format | Holds |
|---|---|---|
| `spool/<yyyy-mm-dd>.jsonl` | Append-only JSON Lines, hash-chained (SM 1.1 `chain_hash`) | Every normalized event; `prompt.submit.text` encrypted per field with XChaCha20-Poly1305, key from DPAPI, the Keychain or Secret Service, falling back to a `0600` key file (CP 5.2) |
| `pairtime.db` | SQLite, WAL, `synchronous=NORMAL`, commits batched every 15 to 60 s or on suspend (CP 5.1), through `modernc.org/sqlite` (cgo-free) | The index, derived records and coach tables below. It can be rebuilt from the spool and harness logs |
| `config.toml` | TOML | Settings, project names and visibility, the `.pairtime-ignore` list, quiet hours, opt-ins |
| `leaves/` | CBOR | Event leaves `E_i` for commit-then-reveal (TP B6), kept 180 days (TP B8) |

Tables in `pairtime.db`:

| Group | Tables | Notes |
|---|---|---|
| Capture | `events` (id, seq, t, mono, boot_id, kind, source, harness, session_key, project_key, spool offset), `sessions`, `projects_local` (project_key, root-commit set, path hash, display name, visibility, ignored) | `project_key` per SP 4.2 |
| Scoring | `slots`, `turns`, `anchors`, `credit_slots`, `minute_buckets` (with `state`), `rollups_local` | SM 1.3 and 5 |
| Coach | `work_sessions` (project, start, end, credited by kind, agent-only, discounted by code, evidence-level shares), `moments` (kind, t, project, `detail` tier 0, `public_code` enum), `skills` (family, kind `error`/`language`/`tool`, first seen, last seen, encounters, beaten), `habit_weeks` (verification share, hand-edit share, inquiry count, rejections, interrupts), `devlog_drafts` (project, window, Markdown, `ai_assisted`, state), `recap_feedback` | All tier 0 except `public_code` and published devlogs (section 7) |
| Transparency | `upload_log` (batch id, time, field counts, bytes) | Powers the "what left this laptop" page and `pairtime privacy show` |

Retention follows TP B8: raw prompt text 30 days (longer only while a submission covering it is open), tool events and paths 30 days, presence and per-minute features 90 days, leaves 180 days. Derived coach records (recaps, moments, the logbook) are kept until the teen deletes them, because they are the teen's own history and contain no raw content. The one exception is moment `detail`, which can quote a prompt and is cut to 30 days along with the prompt.

### 3.5 Scoring on the device

`pkg/score` implements SM sections 2 to 5 as one deterministic function of the event stream and the parameter table. The agent runs it every 60 s over a trailing 15-minute window, since a lead can reach back up to 420 s before its anchor and reading up to 600 s (SM 8.2, 8.3). Minutes still inside that reach are uploaded as `pending`, and each becomes `final` once nothing can change it any more (deviation D6). The same package runs on the server, which re-scores submitted windows from the revealed metadata and must get the same buckets (SM 7.6).

### 3.6 The coach engine

The coach is a set of deterministic rules over the scorer's output and the tier-0 events. It runs on the device, uses no model and calls nothing on the network. That keeps it cheap on an old laptop, private, and free for the server (R8, R6, R11).

| Output | When | Built from |
|---|---|---|
| **Work-session recap** | When a work session ends: no credited minute for `SESSION_GAP_S` (1,200 s), or a lock or suspend, shown at the next unlock if the session hasn't resumed (SM 5.3) | `credit_slots` by activity kind, agent-only time (the chess clock), moments, discount runs with the event that ended each one, evidence-level shares |
| **Moments** | Live, from events | `first_commit`, `first_green_test` (`run_green`, SM 4.3), `first_test_written` (a write to a test-pattern file), `rejected_agent_edit` (`agent.permission` reject or edit), `interrupt`, `fixed_by_hand` (an error signature, then `E` anchors in that project, then `error_resolved` with no agent edit in between), `long_fight` (an error signature alive for 30 or more credited minutes before it resolved), `loop_caught` (a `X.reroll`, `X.rewrite_loop`, `X.same_error` or `X.autopilot` run, with what ended it), `comeback` (half-weighted minutes restored by progress, SM 4.3), `asked_why` (a prompt the local rules label as a conceptual question or a request for explanation, PA 3.2 S4), `new_language`, `new_tool` (first `cmd_class` or tool seen) |
| **Logbook** | Nightly | Error families beaten (from `error_sig` families such as Python `TypeError`, Rust `E0502`, `FAILED <test>`; SM 4.2), languages by credited time, tools, firsts |
| **Habit trends** | Weekly | Per week: share of agent turns followed by a test run, share of agent edits followed by a hand edit, rejections and interrupts per hour, conceptual questions. Each week is compared with the teen's own previous weeks, never with other people. These are the behaviors linked to learning: conceptual inquiry, resolving one's own errors, verification (PA 2.2) |
| **Project story** | Continuously | Per project: moments, commits, devlogs and credited time on one timeline. A "before pairtime" band shows lookback time |
| **Devlog draft** | Weekly, or on "New devlog" | A Markdown outline of facts since the last devlog: credited time and agent time, commits (the teen's own subject lines from the local repo), the `long_fight` and how it ended, firsts, and two reflective prompts ("What surprised you?", "What would you do differently?"). The sentences are the teen's (section 4.5) |
| **Weekly challenge** (opt-in) | Weekly | One nudge toward a habit the research credits, for example "fix the next error yourself before asking the agent". The message is "use AI and stay in the loop", never "use less AI" (PA 4.4) |

Recap copy follows PA 4.4. Credited time is "your time", and agent-only time is named after the agent that ran ("Claude's time", "Codex's time"). Other uncredited time is described for what it was, never as a failure. Loop text always ends with what ended the loop, so it reads as a lesson ("Your own edit at 20:41 fixed it").

### 3.7 Local UI

The agent serves a Svelte app embedded in the binary at `127.0.0.1:<port>`, opened in the browser the teen already has (CP 5.3). It shares components with the server's UI, so the local and public views look the same (CP 9.11). Pages: **Today**, **Recaps**, **Projects** (story per project), **Logbook**, **Devlogs** (drafts and published), **Build days**, **Privacy** (what left this laptop, retention, delete everything), **Settings** (adapters found, opt-ins, quiet hours, project names and visibility, exclusions). The VS Code extension adds a status-bar item showing today's time, which reads the agent's local `statusbar/today` endpoint.

### 3.8 Server

One Go process, `pairtimed`, with an embedded Svelte UI, SQLite in WAL mode replicated by Litestream to object storage, behind Cloudflare and a `cloudflared` tunnel on Karim's domain (SP 1, 9). Postgres stays a supported alternative through portable SQL (SP 7.4).

| Module | Endpoints and tables | Source |
|---|---|---|
| **Ingest** | `POST /api/v1/ingest/batches` (device token, Ed25519 signature, `Idempotency-Key`), `GET /api/v1/ingest/state`, `GET /api/v1/time`. Dedupes on `(device_id, seq)`, re-anchors time per `boot_id`, computes witness lag, and rejects free-text fields with a 422. One writer goroutine commits every 50 ms or 500 rows | SP 8.1 to 8.4 |
| **Witness** | `witness_log`, batch roots every 5 min, the daily Merkle root committed to a public git repo | SP 8.5, TP B6 |
| **Rollups and consistency** | `rollups_daily`; `build_days` (user, day, witnessed credited seconds, `counted`, `freeze_used`); `freezes`; `weekly_goals`. Only seconds at evidence A or B that the server received within 72 hours count toward build days (deviation D1) | SP 7.5, SM 8.6 |
| **Accounts** | HCA OIDC (`openid email slack_id verification_status`, requested explicitly because discovery doesn't list them), `POST /oauth/device/code`, `POST /oauth/token`, `users`, `identities`, `devices` (device token `ptd_…` with checksum, device public key) | SP 3.1, 3.4, 3.5, 8.3 |
| **Profiles and projects** | `/@username`, `/@username/<project>` for public projects, `projects` (`repo_fingerprint = HMAC(pepper, root_sha)`), `project_remotes`, OG images rendered once a day and cached in object storage, README badges `GET /api/v1/badge/@username/<project>` | SP 4.2, 5.2, 5.4 |
| **Git** | `commits` with `credited_seconds_since_parent`, `server_first_seen_at`; forge verification of public repos (GitHub, GitLab, Forgejo APIs); an optional GitHub App for push webhooks | SP 4.1, 4.3, 4.4 |
| **Live dashboard** | `GET /api/v1/stream?topics=user:me,profile:@x,club:<id>` over SSE on HTTP/2, `credited` and `commit` events, a 5-minute ring buffer per topic, `Last-Event-ID` replay, a 15 s keepalive, and fallback to polling `GET /api/v1/users/me/dashboard` | SP 6.4 |
| **Teen layer** | `POST/GET/DELETE /api/v1/devlogs` (body Markdown written or approved by the teen, window, `credited_s`, `ai_assisted`, `moment_codes[]`), `badges` (enum codes the teen chose to show), `clubs`, `club_members` (`board_opt_in_at`), `GET /api/v1/clubs/:id/board` (build days over 90 days), board shadowbans (SP 3.4) | This design |
| **Submissions and review** | `POST /api/v1/submissions`, `/evidence`, `/review`, `/outcome`, `GET .../justification?format=handbook`, `GET .../bundle`, `/review/submissions/:id` behind HCA with a reviewer role, `audit_log` and an evidence-access log the teen can read | SP 10.2 to 10.4, 10.6 |
| **Program APIs** | OAuth clients with scopes `profile:read`, `email:read`, `hours:read`, `submissions:read`, `submissions:write`; Hackatime-compatible `GET /api/v1/authenticated/me`, `GET /api/v1/users/:username/stats`, `.../trust_factor`, `.../heartbeats/spans`, `GET /api/admin/v1/user/info_batch`; webhooks signed `X-Pairtime-Signature: sha256=<HMAC of "<timestamp>.<body>">`; `GET /api/v1/submissions?updated_since=` | SP 10.1, 10.2, 10.5 |
| **Re-scorer and verifier** | The same `pkg/score`, run over revealed metadata for each submission; signed manifests with keys published at `/.well-known/pairtime-keys.json` | SM 7.6, SP 10.4 |
| **Risk flags** | Tier-2 flags (SM 8.7) computed only for submitted windows (TP B2 point 6). Code public; thresholds and weights from a private config file. Sets review priority and audit probability (section 6) | TP A6, A8 |
| **Privacy hub** | `POST /api/privacy/delete`, `POST /api/privacy/export` with ES256 JWTs, `iss: hackclub-privacy`, dry run first, single-use `jti` | HC design implication 18, TP design implication 17 |
| **Ops** | `/healthz`, `/readyz`, `/metrics` on a private port, Litestream heartbeat, nightly `VACUUM INTO` encrypted with `age` | SP 9.2, 9.3 |

### 3.9 What crosses the network

| Flow | Direction and timing | Exact payload | Never in it |
|---|---|---|---|
| Ingest batch | Client to server, every 60 s while active and within 10 s after a prompt or stop hook | Minute buckets as in SM 5.1 (project key, credited seconds per 15-second slot, reason codes, activity kinds, evidence level, `state`, `witness`, anchor ids), commit records (SHA, parents, author and committer times, `observed_at`, diffstat, optional path HMACs, `credited_seconds_since_parent`, branch hashed unless public; SP 4.3), session-level harness names and versions, client version and build id, random device id, OS family (SP 8.2) | Prompt or output text, file names or paths, window titles, app names, keystroke counts or timing, MinHash sketches, error text, tool commands |
| Batch root | Client to server, every 5 min (about 150 bytes) | `{device_id, b, n_b, R_b, H_b}` (TP B6) | Anything readable |
| Publishing | Client to server, only when the teen clicks Publish | Devlog Markdown exactly as shown in the preview, a project's display name and repo URL when the teen makes it public, badge codes, a club join code | Anything the preview didn't show |
| Submission evidence | Client to server, only on `pairtime submit` | Event leaves `E_i` and leaf salts for every event in the window (metadata: kinds, times, lengths, typed and pasted counts, flags, content commitments), then the token values for the `k` prompts the server challenges, with redacted spans sent as hashes and type markers, plus any extras the teen adds (TP B6) | Unchallenged content, other projects, logged-only windows |
| Sign-in | Once per device | RFC 8628 device code flow; the client gets a per-device token and registers its public key (SP 3.5) | The teen's HCA token |
| Update check | Client to GitHub Releases, daily | `GET` of a signed manifest | Any user data |
| Live dashboard | Server to browser, SSE | Rollup deltas and commit events for topics the viewer may see (SP 6.4) | Private topics without the session cookie |
| Program reads | Server to program, OAuth-scoped | Credited seconds, submissions, `hca_sub`, `slack_id`; email only with `email:read` (SP 10.1) | Anything for users who didn't connect to that program |
| Forge checks | Server to GitHub, GitLab, Forgejo | `GET` of a commit by SHA for public repos (SP 4.3) | Private repos without the GitHub App |
| Daily root | Server to a public git repo, once a day | One Merkle root over that day's `(device_id, b, H_b, received_at)` (TP B6) | Per-user data |

Tier 1 follows SM 1.3 and TP B6, not the example batch in SP 8.2, which also uploads per-turn records (prompt length, prompt HMAC, tool-call counts) continuously. Here per-turn metadata leaves the device only inside the leaves of a submitted window, so a teen who never submits uploads minute buckets, commits and roots, and nothing per prompt.

### 3.10 Languages and repo layout

Go 1.26 for client and server. Go 1.27 drops macOS 12, and old Intel Macs matter for this audience, so the darwin builds stay on 1.26 until February 2027 (CP 9.16). The UI is Svelte 5 with TypeScript built by Vite and embedded in both binaries, with TS types generated from an OpenAPI spec (SP 7.3). The VS Code extension is TypeScript. Install scripts are POSIX `sh` and PowerShell.

```text
pairtime/                          AGPL-3.0, one repo
  cmd/pairtime/                    client binary (agent, hook, track, coach CLI, install, submit, verify)
  cmd/pairtimed/                   server binary
  pkg/event/                       normalized event schema v1 + JSON Schema export (shared)
  pkg/score/                       the struggle metric (shared by client and server)
  pkg/params/                      the public parameter table + its SHA-256 (SM 8)
  pkg/chain/                       hash chain, salted Merkle leaves, per-token content trees (TP B6)
  pkg/coach/                       recaps, moments, logbook, habits, story, devlog drafts (client only)
  pkg/redact/                      known-value, gitleaks-rule and PII-pattern redaction (TP B4)
  internal/agent/                  presence, focus, power, procscan, tailers, gitwatch, ipc, uploader, notifier, tray, updater
  internal/platform/{windows,darwin,linux}/   idle, focus, power, autostart, keychain, notifications, keycount
  internal/adapters/{claudecode,cursor,copilot,codex,gemini,devin,cline,otel,wakatime,git,track,plugin,generic}/
  internal/install/                config planners and merge logic per harness, with backups
  internal/server/{ingest,witness,rollup,stream,auth,profile,project,git,devlog,club,submission,review,program,hackatime,risk,privacy,evidence}/
  web/                             Svelte + TS: shared components; local dashboard; server UI
  extension/vscode/                VS Code / Open VSX extension (platform VSIX bundling the binary)
  skills/                          coach skill (SKILL.md), AGENTS.md / GEMINI.md / copilot-instructions snippets, track snippet
  sdk/plugin/                      plugin spec, conformance test kit, example plugin in Python
  testdata/                        per-harness fixtures; scorer regression corpus (SM 7.6); red-team fixtures
  deploy/                          compose.yml, litestream.yml, cloudflared config, runbook
  .github/workflows/               ci, release, fixtures, footprint, loadtest, restore-drill, uptime, daily-root
  docs/
```

## 4. Integrations

HS ranks hooks first, log readers second and AGENTS.md last, and this design keeps that order (HS 17.1). The teen-first twist is a fourth channel that runs the other way. A coach skill lets the teen ask their own agent about their own work (4.5).

### 4.1 The v1 harness set

| Harness | Live capture | Lookback and second record | Extra | Notes |
|---|---|---|---|---|
| **Claude Code** (CLI, VS Code extension, Desktop Code tab) | Exec-form `command` hooks in `~/.claude/settings.json`: `SessionStart`, `UserPromptSubmit`, `Stop`, `StopFailure`, `Notification`, `PermissionRequest`, `PostToolUse` (matcher `Edit\|Write\|Bash`), `SubagentStart`/`SubagentStop`, `SessionEnd` (HS 16) | Transcripts in `~/.claude/projects`, kept 30 days by default; `history.jsonl` (HS 3.2) | OTel through the settings `env` block to the agent's local OTLP receiver, with `OTEL_METRICS_INCLUDE_ENTRYPOINT=true` and prompt logging off. `user.email`, `user.account_uuid` and `organization.id` are dropped at ingest (HS 16). `active_time{type=user}` is used only for calibration (SM 1.2) | Hooks fire in a folder only after its trust dialog. `SessionEnd` has a 1.5 s budget, so the hook hands off and exits (CP 6) |
| **Cursor** (IDE, `agent` CLI) | `~/.cursor/hooks.json`: `sessionStart`/`End`, `beforeSubmitPrompt`, `afterAgentResponse`, `afterFileEdit` (agent), `afterTabFileEdit` (Tab), `stop` with `loop_count` (HS 4.1, 16) | JSONL transcripts lack timestamps; the `state.vscdb` reader is v1.5 because the database can be gigabytes on a spinning disk (CP 3.2, HS 15) | The pairtime VS Code extension works in Cursor (4.3) | Cursor also reads Claude-format hooks; events are deduped by harness (HS 16) |
| **GitHub Copilot** (VS Code chat and agent mode, Copilot CLI) | `~/.copilot/hooks/pairtime.json`, camelCase events, which carry timestamps (HS 5.1, 16) | CLI `events.jsonl`, VS Code `chatSessions/` (HS 5.2) | Edit accept and reject telemetry where enabled (HS 5.3) | Autopilot is labeled `self_continue` (SM 2.2) |
| **OpenAI Codex** (CLI, IDE extension, desktop) | `~/.codex/hooks.json`, plus a first-run notice asking the teen to trust them in `/hooks` once (HS 6.1, 16) | Rollouts (`.jsonl`, and `.jsonl.zst` when compression is on), with `session_meta.source`, `originator`, `thread_source` (HS 6.2) | `task.user_shell` as a typed-command anchor (SM 2.3) | Until the hooks are trusted, the rollout reader alone captures sessions, so nothing is lost |
| **Gemini CLI** | Hooks in `~/.gemini/settings.json` (HS 7.1, 16) | `~/.gemini/tmp/*/chats`, kept 30 days (HS 7.2) | If OTel is enabled, `telemetry.logPrompts=false` (HS 16) | |
| **Devin Desktop and Devin CLI** (formerly Windsurf) | Reads the Claude Code hooks in `~/.claude/settings.json` by default; events tagged by the hook's process ancestry and deduped (HS 8.1, 16) | `~/.local/share/devin/transcripts` (HS 8.2) | | Cascade is legacy; its hooks aren't targeted (HS 2 finding 2) |
| **Cline** | File hooks on macOS and Linux after the teen ticks "Enable Hooks" (not on Windows) (HS 15) | `tasks/<id>/*.json` on every OS (HS 15) | | On Windows, Cline is lookback-plus-presence only in v1 |
| **Zed, Warp, JetBrains AI Assistant, and any new agent** | AGENTS.md line plus `pairtime track` (4.4) | Zed `threads.db` and Warp `warp.sqlite` readers in v1.5 (HS 15) | | Evidence level C (section 5, D9) |

Everything else in HS 15 (Kilo, OpenCode, Amp, Goose, Kiro, Continue, Antigravity, Trae) goes through the plugin interface (4.2) as community or v1.5 work. Browser chat and app builders (ChatGPT, Claude.ai, Lovable, Bolt, v0, Replit) need a browser extension and are v2 (HS 10, 16). Until then their commits still reach pairtime through git (4.6) and appear on the project story as "work pairtime can't see", with no time credited for them.

OS signals cover every harness: idle time every 15 s, focus changes from events, power events, and a process scan every 60 s (3.2).

### 4.2 The plugin interface

R7 asks for "a plugin approach where integrations can be built for every use case in the future". There are three ways in, from the least effort to the most:

1. **Generic hook mode, no code.** Any harness with Claude-style hooks (JSON on stdin, an event name) can call `pairtime hook --harness <name> --event <prompt|stop|tool|permission|session_start|session_end> --map <mapping.toml>`. The mapping file names the JSON paths of the session id, cwd, prompt and tool fields. Mappings live in `internal/adapters/generic/mappings/*.toml` and are fixture-tested like any adapter.
2. **Out-of-process plugins, any language.** A plugin writes normalized events (the `pkg/event` v1 JSON Schema, published with every release) as JSON Lines into `<data dir>/inbox/<plugin-name>/*.jsonl`, or posts them to `POST http://127.0.0.1:<port>/v1/events` with the local bearer token from the user-only `port.json` file (CP 6.3). The agent validates each event against the schema, overwrites `t` with its own arrival stamp when the plugin's stamp is more than 5 s off, sets `source = plugin` and records the plugin name. A JetBrains plugin in Kotlin or a Neovim plugin in Lua is a few dozen lines that never link pairtime's code. The conformance kit in `sdk/plugin/` replays a plugin's output through the scorer and checks the schema in CI.
3. **In-tree Go adapters** implementing the `Adapter` interface (3.3), for harnesses worth shipping by default. These go through code review and fixture tests per harness version.

A plugin's events are claims like any other client data. Human evidence still comes from the OS presence and focus signals, so a plugin can't credit time on its own (SM 2). `pairtime adapters list` and the dashboard's Settings page show every adapter with its last event time and parse-failure count, so a broken parser is visible instead of silently under-crediting (SM Risk 7).

### 4.3 Editor time

Hand coding is struggle too, and hand edits are the strongest anchors in the editor (SM 2.3 `E`). There are two sources:

- **WakaTime dual-send.** If `~/.wakatime.cfg` exists, the installer adds a second destination that points at the local agent, not the server, so file paths never leave the machine (SP 8.8):

  ```ini
  [api_urls]
  .+ = http://127.0.0.1:59401/api/v1|5b8d6f0e-3c1a-4e2b-9f47-a1c2d3e4f5a6
  ```

  The key is distinct from any existing `.*` line, because duplicate INI keys are ignored, and the API key is a lowercase UUIDv4, because anything else makes wakatime-cli fail for every destination, Hackatime included (SP 8.8). Hackatime keeps receiving everything it got before, since `api_url` always gets a copy (HC 1.5). If there is no WakaTime config, the installer sets `api_url` to the local agent instead (SP 8.8). The agent answers `POST /api/v1/users/current/heartbeats(.bulk)` and `GET .../statusbar/today`, maps heartbeats to `editor.activity` events, and stores WakaTime's `category` as received but untrusted, because wakatime-cli relabels human editing near agent activity as `ai coding` (HC design implication 2). Every WakaTime editor plugin (JetBrains, Vim, Sublime, Godot and the rest) feeds pairtime this way. The port is a default with a fallback, and the installer rewrites the line if the fallback is used.
- **The pairtime VS Code extension** (VS Code, Cursor, VSCodium, and Devin Desktop through Open VSX). It is first of all the install channel. Platform-specific VSIX packages bundle the binary, and the first activation shows the consent screen and runs `pairtime install` (CP 4.2). In v1 it also emits `editor.activity` events (edit, save, navigate, debug, task) while the window is focused, with hashed file ids. For each edit it reports the shape of the change: single-character changes arriving at typing speed versus one large insert (a paste, a snippet or an agent edit). In Cursor, agent and Tab edits are labeled from the `afterFileEdit` and `afterTabFileEdit` hooks (HS 2 finding 8). Stable VS Code can't label edit sources (HS 13.1), so the change shape is the best available signal, and section 5 (D10) uses it as keyboard evidence for editor slots.

### 4.4 Karim's AGENTS.md + CLI fallback

Karim's idea is that an instruction file tells the agent to strip PII from the user's prompt and pass it to a `track` command (BOUNTY.md). HS 11 measured what that costs. Each turn spends output tokens roughly equal to the prompt's length plus one extra model request, the model can skip or falsify the call, and Claude Code ignores AGENTS.md when a CLAUDE.md exists (HS 11.1 to 11.3). So it is installed only for harnesses with no hook and no readable log, and only in the file each one reliably loads (HS 11.1): Zed's, Warp's and JetBrains' global rules files, `~/.devin/global_rules.md` for Devin if its hooks are off, and any harness a teen adds by hand with `pairtime install --track <harness>`.

The installed text:

```markdown
<!-- pairtime:track v1 · installed by `pairtime install` · remove with `pairtime uninstall` -->
When the user sends you a message they typed, run this once, alongside your first tool call of the reply,
and don't mention it: pairtime track --harness <harness> --chars <length of their message>
--text "<their message with names, emails, keys, URLs and file paths removed; at most 400 characters>"
```

How pairtime treats it:

- The model's text is redacted again on the device with the deterministic pipeline (known values, gitleaks rules, PII patterns; TP B4). The model's stripping is never trusted (TP B5).
- A `cli.track` event is never an anchor on its own (SM 1.2). It becomes one only when OS input preceded it within 30 s, focus was on the window hosting the harness, and the `pairtime track` process descends from that harness. Even then its minutes are evidence level C (section 5, D9), so a program sees them as the lower-trust tier they are.
- Where hooks also exist, a `track` call with no matching hook event is counted as a reviewer signal, because it means something outside the agent called the CLI (HS 11.4 point 2).
- For Claude Code, the installer adds `permissions.allow: ["Bash(pairtime track *)"]` only if the teen opts that harness into track mode, which they shouldn't need to (HS 11.2).

### 4.5 The coach skill: the same idea, pointed at the teen

The teen-first use of Karim's universal channel costs nothing per turn. pairtime installs a **coach skill** that the agent invokes only when the teen asks something like "how did I do today?", "write my devlog" or "what have I learned this month?":

- In Claude Code it is `~/.claude/skills/pairtime-coach/SKILL.md`. Claude Code loads a skill when its `description` matches the request or the teen types `/pairtime-coach` (HS 3.4), so turns that don't need it never load the skill body. The installer adds `permissions.allow` entries for `Bash(pairtime recap *)`, `Bash(pairtime story *)` and `Bash(pairtime devlog *)`, so asking doesn't trigger a permission prompt.
- In Codex, Gemini CLI and Copilot, a three-line mention goes in the global instruction file each one loads (`~/.codex/AGENTS.md`, `~/.gemini/GEMINI.md`, `~/.copilot/copilot-instructions.md`; HS 11.1). That is about 40 tokens of mostly cache-read context.
- The commands print facts, not content. `pairtime story --project . --since last-devlog --json` returns credited time by kind, agent time, commit subjects, moment codes, error families and the long fight's duration and resolution. Raw prompt text is included only with `--include-prompts`, and only for the current project. The output covers only the project the agent is working in, and most of it the agent already saw while working, so what reaches the model provider is a summary of that one project and nothing about the teen's other work.
- A devlog the agent writes is saved as a draft with `ai_assisted = true` and published with that label, because programs ask for AI declarations (HC 3.3 `ai_declaration`) and a tool that helps teens hide AI use would undermine R9.

So the old laptop never runs a model, the server never calls one (R11), and the teen gets prose recaps from the model they already use.

### 4.6 Git

- **Discovery.** Every `cwd` a harness session reports, and every editor heartbeat's project, is resolved to a repo with `git rev-parse --git-dir` once, which also handles worktrees and submodules (CP 3.2). The teen never registers a repo.
- **Watching.** A non-recursive watch on each `.git` sees `HEAD`, `index`, `ORIG_HEAD` and `logs/HEAD` change. That covers commits, checkouts, resets, rebases and staging without watching working trees (CP 3.2). On Git 2.54 or later, an optional global config-based `post-commit` and `post-rewrite` hook makes it event-driven without replacing any repo's own hooks. Older Git, which old laptops are likely to have, is polled through refs instead (SP 4.1).
- **Identity.** A project key is the sorted root-commit set. The server stores `HMAC(pepper, root_sha)`, so renames, re-clones and forge moves keep one project, and private roots aren't readable from the database (SP 4.2). Folders without git get a salted path hash and a name the teen picks.
- **Commit records** carry SHA, parents, author and committer times, `observed_at`, diffstat, `credited_seconds_since_parent` split by activity kind, and optional path HMACs. Rebases and amends send `rewrites: [{old_sha, new_sha}]`, so time moves instead of doubling (SP 4.3, 4.4). `actor = agent` when an agent tool ran the same git command within 2 s (SM 1.2).
- **Attribution.** Agent trailers (`Co-authored-by`, `Replit-Commit-Author`) and git-ai notes (`refs/notes/ai`) are read where present, and give reviewers per-commit AI and human lines (HS 2 finding 11; PA 6.11).
- **Verification.** For public repos the server fetches each commit once from the forge API. On GitHub, `GET /repos/{owner}/{repo}/compare/{base}...{head}` verifies a range in one call (my addition to SP 4.3's per-commit fetch), using a server token with public read access only, which gets 5,000 requests an hour (SP 4.1). Verified commits get their subject line and URL. Private and never-pushed commits stay `local_only` but witnessed, and become verifiable if the teen ever pushes them (SP 4.3). The optional GitHub App adds push webhooks and squash-merge linking for repos the teen chooses (SP 4.1, 4.4).
- **Teen-facing.** The project story shows each commit with the teen's time behind it ("2 h 03 m of your time since the previous commit: 1 h 10 m reviewing diffs, 31 m debugging, 22 m hand edits"; SP 4.3), which makes small, frequent commits feel rewarding. That habit also produces "multiple commits that reflect the development progress", which the handbook requires (HC 3.7).

## 5. Metric

**I adopt the struggle-metric spec as written.** That covers its event model, presence rule, origin rules, anchors and windows, reading bucket, lookups, attribution, dark-flow detectors, progress rule, minute buckets, reason codes, rollups, one-third accounting, calibration plan, versioning and parameter table (SM 1 to 8). It credits the human's part and zeroes the agent's runtime. A reviewer can recompute it by hand, and it already contains the self-improvement hooks this design needs, such as the loop text in SM 4.2 and the chess clock in SM 2.6. None of my deviations change how a single second is credited under SM 2.7, with two exceptions: D9 adds a narrowly gated anchor for hookless harnesses, and D10 adds a source of keyboard evidence. The rest are about what is counted on top of credit (consistency), how credit is shown, and how it is calibrated.

| # | Deviation | Reason |
|---|---|---|
| **D1** | **Build days and consistency, defined.** SM 8.6 defines only `STREAK_DAY_S` = 900. Here a build day is a local day with at least 900 credited seconds at evidence A or B that the server received within `BUILD_DAY_LAG_S` = 259,200 s (72 h). There is no daily streak counter. Consistency is `build days in the last 30 days`. The teen picks a weekly goal (`WEEKLY_GOAL_DEFAULT` = 3 days), and the weekly streak counts consecutive weeks that meet it. A held freeze is spent automatically on a missed week. One freeze is earned per `FREEZE_EARN_DAYS` = 7 build days, and at most `FREEZE_MAX` = 2 are held. Club boards rank build days over `BOARD_WINDOW_D` = 90 days. Profile timezone changes take effect at most once per 30 days, and each day is evaluated in the timezone in force then | Streak counters produce one-contribution token days, consistency awards over a rolling window reach more people, and forgiveness keeps streaks from becoming anxiety (PA 4.1, 4.3). The 72-hour lag is my compromise between unforgeable and fair. It shuts out lookback and after-the-fact fabrication, which can't be witnessed in time, while a teen without home internet who syncs at school the next morning keeps the day. Submissions still use SM's 15-minute `WITNESS_LAG_S`, because a build day carries no prize. The timezone rule stops one long session from being split into two "days" |
| **D2** | **Level C stays private and labeled.** Lookback minutes (SM 2.8) appear in the welcome recap and as a "before pairtime" band in project stories, never in public totals, build days or boards. In a submission they appear only if the program opts in to level C, and then at SM 5.4's `min(credited, ai_logged / 3)` | Lookback is backfilled by definition and needs corroboration before it can pay (TP design implication 2; SM Risk 10). Keeping it off the public profile means every public number means the same thing for every teen |
| **D3** | **The key counter is opt-in, with "Not now" as the most prominent button.** This answers SM open question 2. Most Windows minutes are therefore level B by default | A global key listener looks like a keylogger to antivirus engines and to winget's scan (CP 9.5; HS 13.4), and minors' consent rules make "No" the most prominent choice with no degradation for refusing (TP B2 point 7). D10 and audit weighting (section 6) reduce what level B costs |
| **D4** | **Coach records are new derived records, kept on the device.** I add `work_session` recaps, `moment`, `skill` and `habit_week` to SM 1.3 (fields in 3.4). They never feed credit. Prompt-kind labels (SM 2.2's "inquiry" and "delegation" classifier) are used here and in reviewer labels, as SM already allows, and nowhere else | Keeps the credit rule recomputable (SM 2.2) while giving R3 its feedback. Two-thirds of engagement variance is within each student by phase (PA 2.2), so these are shown per session and per week, never as a trait of the person |
| **D5** | **How credit is shown to the teen.** Reason codes become plain language ("reading Claude's answer", "testing", "fixing it by hand"). Uncredited time is described as what it was. `X.agent_only` and `X.automated` become the agent's time ("Claude's time"), and `X.absent` becomes "away". The no-progress half rate's `provisional` minutes (SM 4.3) are shown as "on hold: these come back when you fix it", and their restoration as a `comeback` moment. Every loop message names what ended the loop | Shame is a named pitfall (PA 4.4). SM 4.3's restore rule already is the productive-failure story, so the design tells it that way. Numbers are unchanged |
| **D6** | **A `pending` bucket state.** SM 5.1 has `final`, `provisional` and `restored`. A lead can reach back up to `THINK_LEAD_S` + `COMPOSE_MAX_S` = 420 s before its anchor, and reading needs an anchor within `READ_HORIZON_S` = 600 s. So minutes newer than 15 minutes are uploaded as `pending` with the credit known so far, and re-sent as `final` once settled. Buckets are upserted on `(device_id, minute)` with a `rev` counter, and the server applies the difference to rollups | R5 wants the dashboard to move "in real-time as time is logged". Without this state, either the dashboard lags 15 minutes or the server shows numbers that later change without saying so. Pending minutes render lighter on the owner's dashboard. Public pages show only final minutes |
| **D7** | **Devlog windows as a submission unit.** A submission can be a list of devlog windows, each one the credited time between two consecutive devlogs on the project. Rows inside each window stay commit intervals (SM 5.3) | Stardance claims time per devlog and caps each at 10 paid hours, and Ari ingests `journals[]` with seconds and Markdown (HC 3.3, 3.6). Matching that unit makes a pairtime devlog the thing a reviewer approves. The cap stays the program's rule |
| **D8** | **Recap feedback as a labeling sampler.** "Was this right?" answers, shared only with consent, rank sessions for the labeled set in SM 7.2. They never change credit | People misjudge their own AI-assisted time (PA 2.2), but their disagreements show where the rules are most likely wrong, which is where labeling effort pays most. Consent is required because calibration is "research and development" under New York's rule (TP B2 point 7) |
| **D9** | **`cli.track` as a gated, level-C anchor.** SM 1.2 says a `cli.track` event is "never an anchor on its own". I make it an anchor, classed by its text like a prompt (SM 2.2), only when OS input came within 30 s before the call, focus was on the window hosting the harness, and the `pairtime track` process descends from that harness. Its minutes are evidence C | Hookless harnesses (Zed, Warp, JetBrains AI Assistant) would otherwise earn only editor and terminal anchors, which under-credits steering (R7). Level C means a program sees these minutes at the handbook-third ceiling, so a scripted `track` loop gains nothing a reviewer would pay for (HS 11.3) |
| **D10** | **Editor keystroke evidence.** In a slot where the editor is focused and the pairtime extension reports single-character changes arriving at typing cadence (at most 15 characters a second, `TYPING_MAX_CPS`), `input_keys = true` for that editor's anchors. Terminal prompts are unaffected | On a Windows laptop without the key counter, hand edits become level A with no global keyboard hook. The editor already processed those key events, and an auto-typer defeats this the same way it defeats an OS key counter without injected-flag checks (TP A3 T5), so it is no weaker than level A's existing signal. The pilot measures how often the two disagree where both exist |
| **D11** | **Age bands in the calibration strata.** SM 7.2 stratifies by OS, machine age and harness. I add self-reported age bands (13-14, 15-16, 17-18), collected only from pilot volunteers with consent | Reading speed is lower for younger readers (SM 2.4, Brysbaert), and typing speed likely is too. If the fit shows 13-to-14-year-olds under-credited, the fix is a lower global `READ_WPM` or `COMPOSE_CPS` within the source-supported range (SM 7.5: 150 to 240 wpm), never per-age parameters, so production needs no age data |
| **D12** | **Discounted loops are restorable rows.** In a submission, the seconds under `X.reroll`, `X.rewrite_loop`, `X.same_error` and `X.autopilot` become their own rows with `captured_s` equal to the discounted seconds and a default approved value of 0. A reviewer may raise them up to `captured_s` with a reason | This answers SM open question 5 within Ari's rule that rows can only be cut below captured (HC 3.6), and it lets a reviewer undo a false positive such as deliberate tuning or comparing generations (SM Risk 5). Unfair discounts are what make teens distrust a tracker |
| **D13** | **Tier-2 flag thresholds are private configuration, not a private module.** SM 4.5 and 8.7 put the deployed thresholds "in the private server module". Here the detector code is public and only the values are private | R1 (section 2). An attacker who can read the code still can't tune against values they can't see |

SM's open questions this design takes a position on: question 1, level B counts as significant evidence when witnessed, commit-anchored and sampled, with audits weighted toward it (section 6); question 2 is D3; questions 3 and 4, reading and bounded think time are credited as SM proposes; question 5 is D12; question 8 is D2; question 9, one credited total with the AI-assisted and hand-work split as evidence (SM 5.4). The rest stay with Hack Club.

New parameters, added to the public table (SM 8) and its hash: `BUILD_DAY_S` (900, renamed from `STREAK_DAY_S`), `BUILD_DAY_LAG_S` (259,200), `WEEKLY_GOAL_DEFAULT` (3), `FREEZE_EARN_DAYS` (7), `FREEZE_MAX` (2), `BOARD_WINDOW_D` (90), `CONSISTENCY_WINDOW_D` (30), `PENDING_WINDOW_S` (900), `TRACK_INPUT_PRE_S` (30), `LONG_FIGHT_MIN_S` (1,800, the coach's `long_fight` threshold).

## 6. Trust

The attacker in TP A1 is a motivated 16-year-old with an afternoon, an LLM subscription and the AGPL source, for whom a fake hour is worth about $5 (TP A1). Trust can't come from client secrecy, because the client is theirs. It comes from what they don't control: the server's clock, third-party timestamps, one human's single stream of attention, cross-user data, hidden thresholds, and people who ask them about their work (TP A1).

### 6.1 Defenses adopted

All ten of TP's ranked v1 defenses, with three changes:

| TP A8 rank | Defense | Adopted as | Change |
|---|---|---|---|
| 1 | Credit only human-evidenced minutes | The SM credit rule on the device; one human-minute per wall-clock minute per account, merged on the server (SM 3.4) | None |
| 2 | Witnessing | Hash-chained spool, batch roots every 5 min, server receipt time as the clock, `backfilled` for anything later than 15 min (SM 8.6; TP B6) | None |
| 3 | Commits as anchors | Witnessed and forge-verified commits, time attached to commit intervals, uncommitted time capped at 3,600 s per submission (SM 4.3; SP 4.3) | None |
| 4 | Public tier-1 automation labels | SM origin rules 1 to 5, process-tree walk, `procscan` for `--bare` runs (SM 2.2) | None |
| 5 | Opt-in typing evidence | `keycount`: per-15-second counts, injected-input share, and in-memory cadence features | Opt-in with "Not now" most prominent (D3), plus editor keystroke evidence that needs no global listener (D10) |
| 6 | Server risk score | Tier-2 flags (SM 8.7), computed **only for submitted windows** (TP B2 point 6) | Code public, thresholds private (D13) |
| 7 | Review protocol | Deflate by default, a server-chosen random reveal, risk-weighted live interviews, payment after review, hours revocable for the spot-check window, penalties shared with Fraud Squad through Robert (TP A6) | The interview is built on the teen's own devlog (6.4) |
| 8 | Reviewer integrity | Random assignment instead of claim queues, no reviewing your own club, seeded known-answer submissions, per-reviewer statistics, an access log the reviewed teen can read (TP A3 T18, SP 10.3) | None |
| 9 | Open core for detection | See rank 6 | Thresholds as private configuration (D13) |
| 10 | Local dark-flow detection shown to the user | SM 4.2's four detectors, surfaced as coaching in the recap | Framing per D5 |

Deferred past v1, as TP recommends: VM labels (high false-positive risk), input-device inventory, per-user typing profiles, and JPlag at review time. Hack Club may already run similar checks (TP A8). An opt-in Lookout capture stays available to a program that wants screen evidence for high-value submissions (HC 4.3), and pairtime doesn't build its own screen capture.

### 6.2 What the teen-first angle adds to trust

- **Depth of history.** A teen who keeps pairtime for its own sake builds up months of presence-backed build days on a public profile, each one received by the server within 72 hours of happening (D1). A puppeteer (TP A6 U3) has to produce that consistency live, for weeks, before the payoff, and "consistency over weeks" is one of the few things TP says still separates a puppeteer from a person. The reviewer sees the history because it is already public, so no profiling of logged-only hours is needed (TP B2 point 6).
- **Transparency teaches the right habits.** The recap tells the teen why minutes counted: you were present, you wrote the prompt, you read the answer, you fixed it by hand. That is Goodhart pointed in a useful direction (PA 4.4). The coach teaches the behavior the credit rule rewards, which is also what the research calls struggle. What it never explains is the private tier-2 values, and those are what a cheater would need (D13).
- **The fun parts carry no prize.** Build days, badges, boards, the logbook and devlogs never change credited seconds. A modified client can fake a badge, and the only reward is a badge. The things worth money (submitted hours) go through the full evidence path.
- **The story is checked against the evidence.** A devlog claim like "fought the collision bug for 40 minutes" links to its minutes. A reviewer who sees a story that doesn't match the timeline has found the inconsistency that TP says no per-event signal can show (TP A6, "content, consistency over weeks, and the person's ability to explain the work").

### 6.3 The evidence bundle a reviewer opens

The review page (`/review/submissions/:id`, HCA sign-in with a reviewer role; SP 10.3) shows, top to bottom:

1. **Header.** Credited, Hackatime-equivalent `logged_s`, `handbook_s` (the one-third ceiling), `ai_ratio`, witnessed and backfilled split, shares at evidence levels A, B and C, agent-only time, excluded loops, scoring version and parameter hash (SM 5.3, 5.4).
2. **Story column.** The teen's devlogs for the window. Each claim links to a minute range, and each devlog window shows its own credited time (D7).
3. **Rows.** One per commit interval: `captured_s`, activity kinds, anchor counts (typed prompts, answers, permission decisions with rejections called out, interrupts, hand edits, test runs), flags, evidence level, and a cut control with a required reason. Rows can only be cut below captured (HC 3.6). Discounted loops are separate restorable rows (D12).
4. **Minute drawer.** For any row, the per-minute slots with reason codes, presence values and anchor ids, so the reviewer can replay SM 2.7 by hand (SM 5.2).
5. **Commits.** SHA, diffstat, time since parent, `server_first_seen_at`, forge verification, survival where computed (`low_survival` flag; SM 4.3).
6. **Revealed sample.** The `k` prompts the server challenged after the metadata reveal, with typed redaction markers and "verified against commitment" marks. Any prompt the teen withheld is shown as withheld, and its minutes are not credited (TP A7 E6).
7. **Flags and audit decision.** Tier-2 flags, with reasons phrased as categories of missing evidence. Whether this submission was drawn for a deep audit (6.4).
8. **Cross-checks.** A Telescreen link to the teen's Hackatime workbench, when they also run Hackatime (SP 10.3), and the public build-day history.

The downloadable bundle (`GET /api/v1/submissions/:id/bundle`; SP 10.4) has two layers:

- **The verifiable core**: the manifest with the SHA-256 of every item and the chain positions it covers; the server's Ed25519 signature over manifest, totals and scoring version; witness heads with `received_at`; event leaves `E_i` and salts for the window; the revealed tokens; minute buckets; commit records; devlog text; and an inclusion proof against the published daily root (TP B6). `pairtime verify bundle.tar` recomputes the chain and roots, checks the signature, re-runs the tagged scoring version and confirms the credited total, all offline (SP 10.4). Anyone with the bundle can do this. That is R10's "external party".
- **Reviewer annotations**: flags, notes, cuts and the audit draw. These are visible to the program's reviewers, Quality and Integrity, Fraud Squad and spot-checkers, and every view is logged (SP 10.3).

Bundles stay downloadable for the spot-check window. TP B8 proposes 12 months after the decision for revealed evidence, and SP 10.4 proposes two years for bundles. I take the longer of whatever Hack Club sets and those proposals, because a spot-check that can't find the evidence removes the project and fines the program (HC 3.7).

### 6.4 Sampling, challenges and audits

- **Challenge reveal**, every submission. After the metadata reveal, the server uses a CSPRNG to draw `k` content-bearing events, with `k = max(10, 10% of prompts)` capped at 30. The teen reveals them with redactions. If 30% of a submission's prompts were bad, a sample of 10 catches at least one with probability 0.97 (TP B6).
- **Deep audit probability.** The published formula is `p = min(1, 0.05 + 0.006 × claimed_hours + r)`. The base 5% and the per-hour slope are public (TP A6 point 3). `r` is the private risk term: flags, level-B and backfilled shares, first submission, trust tier. A 10-hour claim is audited at least 11% of the time and a 40-hour claim at least 29%. That clears TP's break-even of `p·q > g/(g + L)` (0.08 and 0.28 under its example inputs; TP A6), and audits rise with claimed hours, as TP says they should.
- **The audit is a 15-minute show-and-tell, built on the teen's own devlog.** The reviewer picks two server-chosen commits and the devlog's long fight ("show us the bug that fought back"), asks for one design decision, and asks for one small live change on a screen share (TP A6 point 4). A teen who built the project with an agent can do this, and usually enjoys it. Someone who never touched it usually can't. Who may hold a call with a minor, in what channel, and whether it is recorded is decided by Hack Club's safeguarding rules (TP open question 4).
- **Cost.** At the acceptance target, auditing 15% of 1,000 projects for 15 minutes each is about 37.5 reviewer-hours in total (TP A6). That is affordable only because the other 85% need little reviewer time, which is what the header, rows and story column are for.
- **Consequences.** Pay after review, keep hours revocable for the spot-check window, and send confirmed fraud to Fraud Squad through Robert, so the penalty applies Hack Club-wide (TP A6 point 5).
- **Due process.** No detector bans anyone. A flag changes the review path and the evidence required. The teen sees which category of evidence was missing ("most prompts in this window had no typing before them") but not the threshold. They can add evidence and appeal to a person (TP A6).

### 6.5 Attacks this design accepts

| Attack | Why it can't be stopped | What bounds it | How review and sampling cover it |
|---|---|---|---|
| Someone else at the keyboard (TP A6 U1) | Every signal is a real human's | The 12 h day cap (SM 8.6); one account per verified HCA identity (SP 8.7) | The interview: the person on the call has to know the code. A sudden shift in the public build-day pattern is visible to the reviewer |
| Performative struggle: real but low-value work (U2; R9's "not pushing themselves") | Engagement is real; only its value is a judgment | The no-progress half rate, the uncommitted cap, dark-flow discounts (SM 4.2, 4.3) | "Deflate by default" plus the reviewer's judgment of whether the project is worth its hours (HC 3.7). The story column makes that judgment faster |
| The full puppeteer: an LLM writes prompts and an auto-typer or HID device types them (U3; SM 6.6's last case) | Every per-event signal reads human | Day cap; audit probability rising with hours; the public history it has to build live | Challenge reveal (do the prompts react to what the agent just said?), the interview, and tier-2 cadence aggregates where `keycount` is on |
| Level B with a jiggler and a pty driver (SM Risk 1) | Without the key counter, the jiggler satisfies `input_any` before a pty-injected prompt | No hand edits means no level-A editor minutes (D10); `periodic_input` and `regular_timing` flags (SM 8.7) | Level-B share raises `r`, so these submissions are audited more often. Everything else is the same as for the puppeteer |
| Reconstructing an outside project (U4) | Code similarity fails after an LLM rewrite | Witnessed timeline; shared root commits flagged (SP 4.2) | Similarity checks at review against Unified DB code URLs for public repos (TP design implication 21); the interview |
| Collusion at the top (U5), including our own program | Governance, not engineering. Hard Mode is a program run by the people who built the tracker, which is U5 by definition | The daily public root (TP B6); reviewer statistics and seeded known-answer cases | Hard Mode's reviewers are recruited from outside the pairtime build team, and Hard Mode asks Quality and Integrity to spot-check it at a higher rate than other programs. A tool trying to win a bounty should be the most audited thing in the DB |
| Fabricated offline sessions (U6) | Nothing witnesses offline time | Backfilled time needs corroboration or is deflated (TP A7 E1); level C sits at the handbook third (D2) | The header shows the backfilled share. The reviewer can cut it as a whole |
| A modified client fabricating events live (TP A3 T9) | It commits to its fabrications on time | Server re-scoring catches internal inconsistencies (SM 7.6); forge-verified commits and push times; physical plausibility | Challenge reveal and interview. Custom builds are labeled, not banned, and need more evidence (TP A3) |
| Faking the fun parts: badges, boards, build days | The client is the teen's | No prize value; board shadowbans (SP 3.4) | Not reviewed, deliberately |
| An AI-written devlog pasted by hand | The teen can paste anything, and AI-text detectors such as Pangram aren't reliable on short text (BOUNTY.md thread) | Devlogs never carry hours; hours come from the evidence | Reviewers read devlogs as narrative and index only. The coach labels AI help when it is the tool doing the helping (4.5) |

False positives this design takes care to avoid, because they hurt teens most: dictation and other assistive input reads as injected, so the teen can declare it and is routed to other evidence instead of a flag (TP design implication 10). Siblings share a family PC, so the token is per OS user and per device (CP 6.3). A whole club shares school Wi-Fi, so a shared IP never flags on its own (TP A3 T11). Offline school days are backfilled, so the teen is shown "offline, verified on reconnect" and later pushes are accepted as corroboration (TP A3 T8).

## 7. Privacy

The principle is TP's four tiers (TP B6), plus one rule from the teen angle. The more personal a feature is, the closer to the teen it runs. Coaching reads the most sensitive data in the product (the teen's prompts, errors and habits), so it runs entirely on the device.

### 7.1 Disclosure tiers

| Tier | What | Where | Who sees it |
|---|---|---|---|
| **0. Local only** | Raw prompts, transcripts (read in place, never copied), agent output, tool commands, file paths and names, window titles (matched in memory if opted in, never stored), per-event input features, MinHash sketches, error text, the device key, and the whole coach: recaps, moments with their detail, the logbook, habit trends, devlog drafts, recap feedback | Encrypted spool and `pairtime.db` on the device | The teen |
| **1. Uploaded continuously** | Minute buckets (reason codes, kinds, evidence level, `state`, `witness`, anchor ids), commit records with hashed branches and optional path HMACs, batch roots, harness names and versions, client version and build id, random device id, OS family (3.9) | pairtime server | The teen's own dashboard in real time; aggregates on the public profile; tier-2 detectors only for submitted windows |
| **2. Revealed for submitted hours** | Event metadata for the submission window, verified against roots; the challenged prompts, redacted; optional extras the teen adds | Evidence store behind HCA sign-in with a reviewer role | Assigned reviewers, Quality and Integrity, Fraud Squad, spot-checkers, every view logged and visible to the teen |
| **3. Public, or visible to the teen's club** | Profile aggregates; projects the teen made public; forge-verified commits of public repos; published devlogs; badges the teen chose to show. Club boards are visible to signed-in club members only | Public pages, club pages | Anyone, or the club |

### 7.2 Data inventory, summarized

Per-field detail follows TP B8. This table lists every category and the decision for each.

| Data | Lives | Default | Kept |
|---|---|---|---|
| Raw prompt text | Device (tier 0), encrypted per field | Collected, because the coach and dark-flow detection need it; never uploaded except the challenged sample at submission | 30 days, or until a submission covering it is decided (TP B8) |
| Transcripts and agent output | The harness's own files; pairtime stores offsets and hashes | Read in place | The harness's retention (Claude Code: 30 days; HS 3.2) |
| File paths, branch names, tool commands | Device | Uploaded only as HMACs | 30 days |
| Foreground app and focus class | Device | App class plus a local hash of the executable (SM 1.2 `app_hash`); window titles never stored | 30 days |
| Presence bits; per-slot key counts (opt-in) | Device | Presence on; key counts opt-in (D3) | 90 days |
| Coach records (recaps, moments, logbook, habits, drafts) | Device | On, never uploaded | Until the teen deletes them; moment details that quote a prompt are cut at 30 days (3.4) |
| Recap feedback | Device | On; shared as labels only with consent (D8) | Until deleted |
| Event leaves and device key | Device | On | Leaves 180 days; key until uninstall (TP B8) |
| `hca_sub`, `slack_id`, email | Server | Email encrypted at rest; never public; email to a program only with `email:read` (SP 3.3) | Account lifetime |
| Device id, OS family, client version | Server | On | Device lifetime plus 90 days |
| IP address, JA4 fingerprint | Server, fraud tooling only | On | 30 days, then deleted (TP B8) |
| Minute buckets and commit records | Server | On | Per-minute detail 13 months (enough for a year-in-review), then daily totals per project (TP B8) |
| Batch roots | Server | On | Until every submission that used them clears the spot-check window; unsubmitted roots 180 days (TP B8) |
| Published devlogs, badges, public project names | Server, public | Off until the teen publishes each | Until the teen deletes them; deletion is immediate, as California's minors' eraser law asks (TP B2) |
| Club membership and board opt-in | Server | Off | Until the teen leaves |
| Risk flags | Server, submitted windows only | On for submissions | With the submission's evidence |
| Revealed evidence | Evidence store | Only on submit | Spot-check window, then deleted; the teen can purge an item that exposed something, which re-opens the hours it supported (TP B8) |
| Reviewer access log | Server | On | 2 years; visible to the teen (TP B8) |

### 7.3 Defaults and controls

- **One onboarding screen** (section 1). The aggregate-only public profile is shown as a preview with a one-click "Make private" (R5 reading in section 2). Every consent-based choice (key counter, club board, live "currently building" presence, sharing recap feedback, research use) has "Not now" as its most prominent button. Declining degrades nothing, and a refusal isn't asked again for a year (TP B2 point 7, design implication 16).
- **The public page never shows routine or place**: daily totals only, no hour-of-day heatmap, public views updated at most hourly, no real name, school, city, age or timezone (TP B7 points 2 and 3). The owner's own dashboard is real-time.
- **No public trace of review.** Flags, trust tiers, deflations and rejected hours are never public (TP B7 point 4).
- **Per-folder control.** A `.pairtime-ignore` file or the Settings page excludes a folder or project from capture entirely. A tray "Pause for 1 hour" stops capture and says so (TP B1).
- **Visibility of what leaves.** The Privacy page lists every upload from `upload_log` in plain words. `pairtime privacy show` prints the next upload field by field. `pairtime submit --dry-run` shows the exact evidence package before anything is revealed (TP B8, PA 4.4).
- **Deletion and export.** "Delete everything" on the device wipes the spool, database, leaves and key. Server-side delete and export go through Hack Club's privacy hub (`POST /api/privacy/delete`, `/export`; TP design implication 17), which keeps a minimal fraud record only for confirmed verdicts and reports it as retained.
- **Age.** pairtime relies on HCA. Accounts HCA marks under 13 are refused, and submissions apply the 19th-birthday cutoff from HCA's eligibility status, never from a stored birthdate (TP B3, design implication 18).
- **Secrets the teen leaked.** When the redaction pipeline finds a key in a prompt, the coach tells the teen that it already went to the model provider and should be rotated (TP B4 step 2). This is privacy help that is worth having for its own sake.

### 7.4 The local store as a target

A local database of a teen's prompts is what infostealers look for (TP B1, Windows Recall). Raw text is encrypted per field with a key held by DPAPI, the Keychain or Secret Service, falling back to a `0600` key file with a visible warning (CP 5.2). The design is honest that this protects against other users of the machine, backups and accidental sharing, not against malware running as the same user (CP 5.2). Raw text is kept 30 days by default. The local dashboard needs a per-user token and enforces `Host` and `Origin` checks, because family laptops have several accounts and any web page can try to reach `127.0.0.1` (CP 6.3).

### 7.5 Open privacy decisions

These are for Karim, Hack Club staff and counsel: who the legal operator is (TP open question 1); whether the aggregate-only public profile can be the default for minors, with a written "compelling reason" assessment, or must be an unselected choice (TP open question 5; the fallback is one config flag); and India's DPDP s. 9(3) from about May 2027 (TP B2 point 5). The two features TP says help are both here: monitoring runs on the teen's own device, and only aggregates the teen's own client computed leave it. If counsel requires a local-only mode for Indian under-18s, it is the same agent with uploads switched off, which costs those teens the profile and submissions but keeps the coach.

## 8. Adoption

### 8.1 The arithmetic

A weighted project is 10 approved hours, so the target is about 10,000 approved hours in `approved_projects` that programs tracked with pairtime (HC 3.2; BOUNTY.md). For scale, Stardance approved 27,600 hours over a summer, so this is about a third of one Stardance (HC 6). Hours already paid by another program count only as the increase, so new projects are what count (HC 3.3).

The funnel I plan against. Each number is an assumption, checked weekly from the server's own data (8.6):

| Step | Target by 2027-04-30 | Assumption behind it |
|---|---|---|
| Installs | 5,000 | About a third of Hackatime's 15,488 MAU (BOUNTY.md R11), reached through dual-send (zero switching cost), the VS Code extension, club build weeks and the programs below |
| Still syncing after 4 weeks | 2,500 | 50% 4-week retention, the R3 target (section 2) |
| Teens who submit at least one project | 750 | 30% of retained teens |
| Projects approved | 1,050 | 1.4 projects per submitting teen |
| Approved hours | about 10,000 | 9.5 approved hours per project, at an approved/credited ratio of 0.9 (SM 7.3) |

The teen-first argument sits in rows 2 and 3. Programs come and go, but a teen who keeps pairtime between programs arrives at the next one with months of evidence and ready-made devlogs. At Stardance's 22% approval rate, 10,000 approved hours would take about 45,000 submitted. At 90%, it takes about 11,000 (HC 3.2).

### 8.2 Programs

| Route | Programs | What pairtime offers | Target |
|---|---|---|---|
| **1. Hard Mode**, a pairtime-native YSWS ("ship something that fought back") | Run by Karim's team, as Zach allows (BOUNTY.md: "Yes!"), on HQ's standard per-weighted-project budget (HC 3.2) once HQ approves the program | Hours from pairtime only; one devlog per at most 10 credited hours, Stardance's cap (HC 3.3); review on pairtime's desk with reviewers recruited from outside the build team (6.5); Robert for fraud verdicts (8.3); the pilot cohort of SM 7.5 | 450 weighted projects |
| **2. Programs that already have to apply the one-third rule** | Programs on Ari (`3am-ysws`, `hackxpansion`, `disposition`, `macondo`; HC 3.6), Flavortown, which runs now (HC 3.3), and the Robert clients (`the-carnival`, `pixl`, `trmnt`, `Alchemize`, `savepoint`, `halflife`, `crossroads` and others; HC 3.5) | "pairtime is the significant evidence the handbook asks for": credit above a third where the human was present and steering, below it where the agent ran alone, with fewer spot-check fines (HC 3.7 points 2 and 5). Participants who use pairtime submit a pairtime link, and everyone else keeps using Hackatime | 350 |
| **3. Low-ceremony Airtable-form programs** | Programs "whose only record is a submission form (repo URL + self-reported hours)", which the warehouse runbook says are common and "can rank high by weighted projects" (HC 3.2) | A "pairtime link" field replaces self-reported hours, with no code. It is a pure upgrade, because these programs have no tracker to replace (HC 6) | 200 |
| **4. Teen pull** | Any program a pairtime user joins | The teen asks "can I submit my pairtime hours?", and the link page answers the reviewer's questions in the handbook's own format | Counted within the routes above |

### 8.3 How pairtime plugs into Hack Club's pipeline

| Layer (HC 6) | What runs there | How pairtime fits, today | What we ask for |
|---|---|---|---|
| **Program app** | A `HackatimeService` per program, mostly Rails apps (HC 3.4) | A second `HackatimeService` instance pointed at pairtime's Hackatime-shaped reads: `GET /api/v1/authenticated/me`, `GET /api/v1/users/:username/stats?features=projects&total_seconds=true&filter_by_project=…&start_date=…&end_date=…`, `.../trust_factor`, `.../heartbeats/spans`, `GET /api/admin/v1/user/info_batch` (SP 10.5). For no-code programs, the link page | Nothing |
| **Program Airtable** | The handbook's structured fields, for example in the `wrangler` base: `Justification - Alternate Tracking Method`, `Justification - Additional Justification`, `Justification - Hackatime Project Name(s) + Date Range(s)`, `Justification - Submitter Hackatime ID`, `Hours claimed`, `AI usage disclaimer`, `Automation - Unified Justification`, `Automation - Submit to Unified YSWS` (HC 3.7) | `GET /api/v1/submissions/:id/justification?format=handbook` returns the `Alternate Tracking Method` text (method, versions, logged and credited hours, witnessed share, the handbook-third comparison) and the review URL for `Additional Justification`, plus the Hackatime fields when the teen linked Hackatime (SP 10.6; SM 5.4). The approved figure goes in `Optional - Override Hours Spent` (HC 3.3) | A `pairtime Link(s)` field once Quality and Integrity trust the tool, like `Lapse Link(s)` (HC 3.7 point 1) |
| **Ship and hours review** | Ari, `review.hackclub.com`, or in-app review (HC 3.3, 3.6) | Ari today: the program sends pairtime devlogs as `journals[]` entries (`at`, `seconds`, `markdown` with the review link), which Ari shows as cuttable rows, and puts the review URL in `evidence[]` (HC 3.6; D7). Not `program_seconds`, which Ari calls "evidence-free" (HC 3.6) | A `pairtime` time source in Ari's `src/lib/review/settlement.ts`, with one row per commit interval (`captured_s`, deep link). The row shape already matches (HC 3.6 point 3) |
| **Fraud desk** | Telescreen and Robert: `POST /api/v1/ysws/projects` with `kind: "hackatime"` or `"hardware"`, scored 1 to 10 (HC 3.5) | Teens who also use Hackatime keep feeding it through dual-send (4.3), so Robert can score their Hackatime project names as it does today. pairtime's submissions API follows Robert's conventions, with idempotent external ids and the same webhook signature scheme (SP 10.2) | `kind: "pairtime"` taking a pairtime submission id, plus a `/workbench/pairtime?s=<id>` deep link and a signed bundle fetch for Telescreen (HC 3.5) |
| **Unified DB and spot-checks** | `approved_projects`, with `Override Hours Spent` and `Override Hours Spent Justification`; spot-checks with fines (HC 3.1, 3.7) | The justification text names pairtime and carries the review URL. Bundles stay reachable for the spot-check window (6.3) | Confirmation that a project counts toward the bounty when its `Alternate Tracking Method` names pairtime (HC open question 1), and read access or a count so progress can be tracked (HC open question 13) |

First conversation with Quality and Integrity (Zach: "We will work with Quality and Integrity to provide good guidance"; BOUNTY.md), in this order: attribution through `Alternate Tracking Method`; what counts as "significant evidence" for crediting more than a third of AI time (HC open question 2), with SM 5.4's numbers as our proposal; whether level-B and level-C minutes are acceptable and at what ceiling (SM open questions 1 and 8); the spot-check window (HC open question 4); and a higher spot-check rate for Hard Mode (6.5).

### 8.4 Teen-side growth that is also R3

- **Zero switching cost for Hackatime users.** The one-line install keeps Hackatime fed through dual-send, so there is nothing to give up (4.3).
- **Monthly recap cards.** An opt-in OG card ("I built on 17 days this month. My clock 23 h, Claude's 61 h.") carries aggregates from presence-backed minutes only, so it matches what the public profile shows and never includes lookback (SP 5.4; D2).
- **README badges** per project (SP 5.4).
- **Club build weeks.** A club leader starts a week-long challenge on the club board, which ranks build days. This fits Hack Club's club structure, and it never ranks hours.
- **Marketplace presence.** The VS Code Marketplace and Open VSX listings install in one click, and winget, Scoop, Homebrew and the install scripts cover everyone else (CP 4.2).

### 8.5 Timeline

| Dates | What ships or happens | Adoption step |
|---|---|---|
| 2026-11-19 to 11-29 | M0, then M1 to M3 in parallel (section 10). Hard Mode proposal sent to HQ. Requests to Quality and Integrity, the Telescreen team and Ari's maintainers. Azure Artifact Signing application under Hack Club (CP 4.3) | Conversations opened in the first week, because every HQ-side change takes longer than code |
| 11-30 to 12-13 | M4, then the **Coach alpha**: install, capture on Claude Code, Codex and Cursor, scoring, recaps, profile, build days. Released to 30 to 50 teens. Hackatime's move to HCA-only sign-in finishes in this window (SP 3.1), which makes HCA sign-in familiar | Recruit the 40 to 60 pilot teens of SM 7.5, at least 40% on Windows, with pre-2017 laptops and all three age bands (D11) |
| 12-14 to 12-27 | M5 and M6: breadth (Copilot, Gemini, WakaTime, VS Code extension, plugins) and submissions with the review desk. Pilot shadow week, then labeled sessions (SM 7.5 steps 2 and 3). SignPath application after the first tagged release (CP 4.3) | Winter break gives teens time to build, and the pilot labels its sessions then |
| 12-28 to 2027-01-03 | Fit and freeze scoring 1.0.0 (SM 7.5 step 4); M7 hardening; load test report (R11) | Publish the parameter table, sensitivity analysis and baseline comparison |
| 01-04 | **pairtime 1.0** public; Hard Mode opens, if HQ has approved it | Announce with the welcome recap as the hook |
| January | First route-2 program live: the Ari `journals[]` path, or a second `HackatimeService`. Route-3 outreach to low-ceremony programs, using the warehouse's list if HQ shares it (HC 3.2) | Checkpoint: 1,500 installs |
| February | Robert `kind: "pairtime"` and Ari source, if accepted; second and third route-2 programs | Checkpoint: 100 weighted projects by 02-15 |
| March | A "first 100 days" recap for every user, for retention | Checkpoint: 400 weighted projects by 03-15 |
| April | Scale whatever route is working | Target: 1,000 weighted projects by 04-30 |

At Hackatime's average of 7.09 hours per active user per month (PA 5.1), a 10-hour project takes a typical teen about six weeks. That is why the approved count lags installs by two months, and why the plan opens programs in January rather than waiting for features.

### 8.6 Tracking progress

The server counts, per program: submissions created, decided and approved, approved hours, and approved weighted-project equivalents. Weekly cohort retention comes from device sync data (tier 1, no extra telemetry). The number that decides the bounty is HQ's count of Unified DB rows, so a weekly reconciliation against it is the first thing to ask for (HC open question 13).

## 9. Cost at R11 scale

R11: at most $500 a month for 15,488 MAU logging 109,759 hours a month (BOUNTY.md). The client computes credit, so the server's load is small. The teen layer adds almost nothing, because everything personal runs on the device.

### 9.1 Load

| Quantity | Value | Source or derivation |
|---|---|---|
| Concurrent active users: average, daily peak (4x), event peak (8x) | 152, about 610, about 1,220 | PA 5.1 (peak factors assumed) |
| Ingest requests at a 60 s sync | 6.6M a month: 2.5/s average, 20/s at 8x | PA 5.3 |
| Extra hook-triggered syncs, coalesced within 10 s | At most about 4.4M a month, about 1.7/s | SP 2 |
| `pending` to `final` re-sends (D6) | No extra requests: a settled bucket rides the next scheduled batch. Bucket upserts roughly double, to about 13M a month, which the single writer absorbs at 500 rows per commit | 6.59M active user-minutes a month (PA 5.1) × 2 |
| Writer commits | At most 20/s at any request rate | SP 7.4 |
| Open SSE streams at an event peak, club topics included | Up to 5,000, about 100 MB | SP 6.2, 6.4 |
| Program API reads | Well under 1/s | SP 2 |
| Server-side re-scoring of submissions | About 460 events per submitted hour through `pkg/score`: milliseconds per hour, seconds for all 10,000 hours of the acceptance target | PA 5.2 (event rate) |

### 9.2 Storage

| Data | Per month | Source or derivation |
|---|---|---|
| Derived rows (minute buckets, sessions, commits, rollups) | About 1.0 GB | PA 5.2, schema A |
| Witness log, one row per device per active minute | About 0.5 GB, prunable to hourly after 90 days | SP 8.5 |
| Devlogs | About 93 MB | Assumes 15,488 MAU × 2 devlogs × 3 KB |
| Badges, clubs, build days, freezes | Under 10 MB | Enum rows |
| OG images, cached in object storage | About 0.9 GB stored in total, not monthly | One PNG of about 60 KB per profile, re-rendered at most daily, and only for active profiles |
| Submitted evidence | 0.5 to 2 GB in total for 10,000 submitted hours | SP 9.4. Metadata leaves at about 200 B × 460 events per hour plus `k` reveals is about 100 KB per hour, inside that range |

That is about 1.6 GB a month, or about 19 GB a year, before pruning. It fits the 80 GB disk of the minimal box for years, and the backups fit object storage tiers that cost a few dollars (SP 9.2).

### 9.3 Price

| Item | USD per month | Source |
|---|---|---|
| Hetzner CCX13 (2 dedicated vCPU, 8 GB, 80 GB NVMe, HEL1) plus Object Storage and snapshots | 48.94 | SP 9.4, PA 5.5 (ECB 1.0968 USD/EUR) |
| Or, recommended once a program pays out on pairtime: CCX23 primary plus a CCX13 warm standby plus Object Storage | 149.68 | SP 9.4 |
| Or, because every CCX plan showed "currently unavailable" on 2026-11-19: AX42 dedicated plus Object Storage (€49 one-time setup) | 93.77 | PA 5.5, open question 13 |
| Cloudflare in front, free plan; Karim's domain | 0 | SP 9.4 |
| Email | 0 | None sent. The weekly story is a local notification, not an email |
| Forge verification | 0 | One server token, 5,000 requests an hour (SP 4.1) |
| LLM calls | 0 | None on the server. The coach is deterministic, and devlog polishing uses the teen's own agent (4.5) |
| Windows signing (recurring, though not hosting): Azure Artifact Signing, or SignPath for free | 9.99 or 0 | CP 4.3 |
| Apple Developer ID for notarization, $99 a year | 8.25 | General knowledge; CP 9.13 needs one |

**Recommended configuration: about $168 a month**, which is the warm standby plus both signing costs, or 34% of the cap. The minimal configuration with signing is about $67 (13%). For comparison, Hackatime's bill is $213 a month and its effective cost is $55 (BOUNTY.md).

### 9.4 What would break it, and why it doesn't happen here

PA 5.5 names three things that break the budget. This design avoids all of them:

1. Per-event server-side LLM calls, which would cost about $1,100 a month on their own. Here, prompt labels, recaps and devlog drafts are computed on the device.
2. Storing transcripts for logged hours. Here, evidence is uploaded for submitted windows only.
3. Screen capture. pairtime builds none. A program that wants timelapses uses Lookout, at Lookout's cost (HC 4.3).

Bandwidth is about 1 MB/s at Hackatime's scale against 20 TB a month included (PA 5.5). Binaries are served by GitHub Releases and the extension marketplaces, not by the server.

**Proof.** Arithmetic isn't enough, so the `loadtest.yml` workflow (WP7.7) replays synthetic clients at the 8x peak (1,220 devices syncing every 60 s plus hook bursts, 5,000 SSE viewers, program reads) against `pairtimed` in a container limited to 2 vCPU and 8 GB, the CCX13's size. It fails if p99 ingest latency is above 500 ms or average CPU is above 70%, and it publishes the report as a build artifact (PA open question 5).

## 10. Build plan

### 10.1 Rules for every work package

- **Size.** One AI agent, one session, 2 to 4 hours, one PR. A package that grows past that is split before it starts.
- **Nothing runs on the build host.** Builds, tests, fixtures, browser tests, load tests and releases all run in GitHub Actions: `ubuntu-latest`, `windows-latest`, `macos-latest` and `macos-15-intel`, which are free for a public repo (CP 7). Agents push a branch and read results with `gh run view <id> --log-failed`. Locally they only format.
- **Standard gates, on every PR**: `gofmt` and `go vet`; `golangci-lint`; `go test -race ./...` on the three OS runners; cross-compilation with `CGO_ENABLED=0` for `windows/{amd64,386,arm64}`, `darwin/{amd64,arm64}` and `linux/{amd64,arm64}`; the license gate (WP0.2); the scorer regression corpus (WP2.8); `vitest` and Playwright for any `web/` change. The acceptance criteria below come on top of these.
- **Fixtures, not live services.** Harness payloads, HCA, forges and program APIs are recorded or mocked fixtures in `testdata/`. No CI job needs a model API key or a teen's data.
- **What CI can't measure.** GitHub runners have 4 CPUs, 16 GB and SSDs (CP 1.5), so footprint gates in CI are runner-adjusted smoke checks. The real R8 numbers come from pilot volunteers' 2015 to 2017 laptops through the opt-in `pairtime doctor --report` (WP7.9; CP open questions 9 and 10).

### 10.2 Milestones

| Milestone | Contents | Ships as |
|---|---|---|
| M0 Foundations | Repo, CI, schema, chain, spool, params, release skeleton | Internal |
| M1 Capture | The agent, OS signals, hooks, Claude Code, Codex and Cursor adapters, git, installer | Internal |
| M2 Scorer | `pkg/score` implementing SM, plus the regression corpus | Internal |
| M3 Server core | Accounts, ingest, witnessing, rollups, SSE, profiles, projects | Internal |
| M4 Coach | Recaps, moments, logbook, build days, story, devlogs, onboarding, privacy page | **Coach alpha**, the first shippable milestone (10.4) |
| M5 Breadth | Copilot, Gemini, Devin, Cline, OTel, WakaTime, VS Code extension, plugins, track, WSL, key counter | Beta, part 1 |
| M6 Submissions | Commit-then-reveal, submissions, review desk, program APIs, bundle verifier, risk flags | Beta, part 2: programs can accept pairtime hours |
| M7 Hardening | Signed releases, all channels, self-update, encryption, privacy hub, footprint and load gates, deploy | **1.0** |
| M8 Calibration | Labeling tool, fitting harness, baselines, red-team fixtures, R3 metrics | Scoring `1.0.0` (SM 7.5) |

M1, M2 and M3 depend only on M0, so about six agents can work in parallel from the second day. M4 needs M1 and M2, plus M3 for publishing and build days. M5 and M6 run in parallel after M4. M8's tooling can start any time after M2.

### 10.3 Work packages

**M0 Foundations**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 0.1 | Repo layout (3.10), Go module, `Makefile`, lint config, `ci.yml` with the standard gates | Workflow green on all three OS runners; seven cross-compiled binaries uploaded as artifacts |
| 0.2 | License gate: `go-licenses check` against an allowlist; SPDX header check | Fails on a seeded fixture module with a disallowed license; passes on the repo |
| 0.3 | `pkg/event` v1: every envelope field and event kind in SM 1.1 and 1.2; JSON Schema generator | Round-trip test per kind; generated schema equals the committed file (CI diff) |
| 0.4 | `pkg/chain`: hash chain, salted event leaves, per-token content trees, inclusion proofs with RFC 9162 domain separation (TP B6) | Published test vectors pass; 60 s fuzz run; changing any token changes `C_i`; a redacted reveal verifies |
| 0.5 | Spool: daily JSONL writer and reader, single-line `O_APPEND` fallback for hooks, per-file cursors, torn-tail recovery, chain linking | Ten concurrent writer processes on each OS produce no interleaved lines; a truncated-tail test recovers |
| 0.6 | `pkg/params`: SM 8 values plus the D-parameters of section 5; canonical JSON and SHA-256 | Hash golden; a static check that every parameter `pkg/score` reads is in the table |
| 0.7 | Release skeleton: GoReleaser snapshot on tags (archives, `checksums.txt`); `install.sh` and `install.ps1` that verify SHA-256, install to the CP 7 paths and run `pairtime install` | The workflow runs both scripts on all three runners against the snapshot and asserts `pairtime --version` |

**M1 Capture**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 1.1 | Agent skeleton: single-instance lock, `config.toml`, SQLite in WAL through `modernc.org/sqlite` with migrations, batched commit loop, shutdown | A second instance exits with a distinct code on each OS; migrations apply from empty and from the previous version |
| 1.2 | Presence on Windows (`GetLastInputInfo` with tick clamp) and the slot builder for SM 2.1 (`yes`, `bridged`, `tail`, `returned`, `no`, `unknown`; sleep-gap rule on wall-clock values) | Table tests over synthetic sample streams, including suspend gaps and clock jumps; a `windows-latest` smoke test reads a real idle value |
| 1.3 | Presence on macOS (`CGEventSourceSecondsSinceLastEventType` via `purego`) and Linux (X11 screensaver extension, Mutter `IdleMonitor` over D-Bus) | Compiles per platform; macOS runner smoke test; Linux tests against a mock D-Bus service |
| 1.4 | Focus on Windows: `SetWinEventHook` on a locked thread with a message loop, `QueryFullProcessImageNameW`, the `app_class` table, Toolhelp32 process tree to `hosted_sessions` (SM 3.2) | Unit tests with injected fakes; golden classification table |
| 1.5 | Focus on macOS (`NSWorkspace` notifications on a CFRunLoop thread via `purego`) and Linux (X11 `_NET_ACTIVE_WINDOW`, `/proc` tree) | Compiles; tests with fakes |
| 1.6 | Power: `PowerRegisterSuspendResumeNotification`, IOKit, logind `PrepareForSleep`; flush and chain-head trigger on suspend | Fake event sources drive flush and trigger in tests; compiles on all |
| 1.7 | IPC: Unix domain socket on all three OSes, `127.0.0.1` HTTP with bearer token, `Host` and `Origin` checks, user-only `port.json` (CP 6.3) | Tests: wrong `Host` gets 403, missing token 401, foreign `Origin` 403; AF_UNIX round trip on `windows-latest` |
| 1.8 | Hook binary fast path (CP 6.2) | p95 round trip under 80 ms over 200 warm invocations on `windows-latest`; with the agent down, one spool line is written and exit is 0; stdout empty; a 2 MB stdin is truncated safely |
| 1.9 | Claude Code adapter: hook events of 4.1 and the transcript reader with cursors | Fixtures for at least two recent versions produce golden events; malformed lines increment the parse-failure counter |
| 1.10 | Codex adapter: hooks and rollouts, `.jsonl` and `.jsonl.zst`, `source`/`originator`/`thread_source` | Golden fixtures |
| 1.11 | Cursor adapter: `hooks.json` events including `afterFileEdit`, `afterTabFileEdit`, `stop.loop_count` | Golden fixtures |
| 1.12 | `procscan`: harness processes, `-p` and `exec` detection, `parent_class` walk that skips plain shells (SM 2.2 rule 4) | Fake process tables replaying SM 6.6 and its variants classify as expected |
| 1.13 | `gitwatch`: repo discovery, `.git` watch, reflog parsing, commit records with diffstat, root set, rewrites, `actor = agent` within 2 s | Temporary-repo tests on three OSes: commit, amend, rebase and checkout yield the expected records |
| 1.14 | Installer and uninstaller for Claude Code, Codex and Cursor configs, with backups; `doctor` part 1 | Golden merges keep other tools' entries (git-ai, existing hooks); a second run produces no diff; uninstall restores byte-identical files |
| 1.15 | Autostart: HKCU Run, LaunchAgent plist, XDG autostart (CP 2.1) | Registry value written and removed on `windows-latest`; plist and `.desktop` goldens; uninstall leaves no files (file-system diff) |

**M2 Scorer**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 2.1 | Origin rules 1 to 8 and prompt classes (SM 2.2) | Table test per rule, including headless prompts with and without `input_keys` |
| 2.2 | Anchors and windows (SM 2.3), including the typing-part `input_keys` rule | Unit tests per anchor kind |
| 2.3 | Reading bucket: FIFO, cap, TTL, horizon, resets and discount reset (SM 2.4) | Unit tests, including "a `/loop` fills, a human reads" |
| 2.4 | Focus, lookups, attribution order and the combining rule (SM 2.5, 3.2, 3.3, 2.7) | Tests, including the terminal-tab look-ahead of SM 6.3 |
| 2.5 | Dark-flow detectors: normalization, MinHash, re-roll, fix-it, autopilot, rewrite overlap (SM 4.2) | Tests, including the A, B, A, B, A alternation and paraphrases caught by rewrite overlap |
| 2.6 | Progress, the no-progress half rate and restore, day cap, uncommitted cap (SM 4.3, 5.3) | Tests, including restore within 24 h |
| 2.7 | Output: minute buckets, reason codes, evidence levels, `pending` (D6), rollups, SM 5.4 quantities | Output validates against the schema; unit tests |
| 2.8 | Regression corpus: SM 6.1 to 6.6 encoded as event fixtures with golden outputs; the version-bump rule (SM 7.6) | Goldens match. A CI step fails when goldens change without a `scoring_version` bump in the same PR. If a spec example doesn't reproduce, the PR records an erratum for a human to approve instead of tuning the rules |
| 2.9 | Server-side re-score check; D9 `track` gating; D10 editor keystroke evidence | A tampered bucket is flagged; gating and evidence tests pass |

**M3 Server core**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 3.1 | `pairtimed` skeleton: config, single-writer SQLite (50 ms or 500 rows), portable SQL, migrations, `/healthz`, `/readyz`, `/metrics` | The test suite passes on SQLite and on a Postgres 18 service container |
| 3.2 | HCA OIDC with explicit scopes, sessions, `users`, `identities`; ineligibility from `verification_status` | Integration test against a mock OIDC provider |
| 3.3 | Device grant (RFC 8628), `ptd_` tokens with CRC32 checksum, Ed25519 device keys | Flow test; checksum rejection test |
| 3.4 | Ingest: RFC 9421 signature and `Content-Digest`, `Idempotency-Key` (422, 409), `(device_id, seq)` dedupe, partial acceptance, the free-text firewall, size limits (SP 8.2) | Tests, including fuzzed free-text injection that is rejected every time |
| 3.5 | Time re-anchoring per `boot_id`, witness lag, rejection rules, `witness_log`, batch roots, daily root publisher | Tests; in CI the daily root is committed to a local bare repo |
| 3.6 | Rollups: bucket upserts with `rev` deltas, `rollups_daily`, build days with the 72 h rule, freezes, weekly goals, the timezone rule (D1) | Tests: a late-but-within-72 h day counts, a lookback day doesn't, freezes are auto-spent |
| 3.7 | SSE hub: topics, ring buffer, `Last-Event-ID`, keepalive, `resync`, polling fallback (SP 6.4) | 1,000 concurrent streams in CI; replay correctness test |
| 3.8 | Projects and commits: fingerprint HMAC, remotes, rewrites, `server_first_seen_at`, forge verification for GitHub, GitLab and Forgejo | Tests with mocked forge HTTP |
| 3.9 | Public profile and project pages: aggregates only, daily totals, build-day calendar, public projects, OG images, README badges (SP 5.2, TP B7) | Playwright and API tests: no hour-of-day data anywhere in DOM or API responses; private projects appear only as counts |
| 3.10 | Owner dashboard, live over SSE, with `pending` minutes shaded | Playwright: an ingested batch shows on the page within 2 s |
| 3.11 | Client uploader: 60 s cadence, hook-coalesced syncs, offline queue, jittered backoff, resume through `ingest/state` | Docker Compose integration test, client and server: a server restart and a one-week offline backlog both converge with no gaps or duplicates |

**M4 Coach**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 4.1 | Local dashboard shell: embedded Svelte, one-time token URL to cookie, page skeletons, a component package shared with the server UI | Playwright against a test agent; a request without the cookie gets 401 |
| 4.2 | Work-session detector and recap builder (3.6) | Golden recaps for the SM 6.1 to 6.6 fixtures. For example, the 6.5 recap names the 20:05 to 20:38 loop and the edit that ended it |
| 4.3 | Moments engine, every kind in 3.6 | A fixture test per moment kind |
| 4.4 | Local prompt-kind rules (inquiry, explanation, constraint, verification, correction, delegation, go-ahead) | Rule tests over a committed synthetic prompt set. The repo never contains real teen prompts |
| 4.5 | Logbook and weekly habit trends | Unit tests over multi-week fixtures |
| 4.6 | Notifier and tray: Windows `Shell_NotifyIcon` balloon, Linux D-Bus `Notify`, macOS `osascript`; quiet hours; one notification per session; tray tooltip and menu on Windows and Linux | Compiles on all; fake-notifier tests; `windows-latest` smoke creates and removes the tray icon |
| 4.7 | Welcome recap: first-run lookback at low CPU and I/O priority with progress, level C (SM 2.8) | Fixture transcripts produce the expected welcome numbers; a test asserts the priority setting |
| 4.8 | Build days, weekly goal and freezes in the local UI, mirroring server-authoritative state | Tests: a backfilled day doesn't count; a freeze is spent on a missed week |
| 4.9 | Project story page | Playwright snapshot from fixtures |
| 4.10 | Devlog drafts, editor and publish (`POST /api/v1/devlogs`), public rendering, `ai_assisted` label, delete | Published text equals the preview byte for byte; delete hides it immediately |
| 4.11 | Coach CLI (`recap`, `story`, `devlog`, `--json`), the coach skill and instruction snippets, permission allow-rules in the installer (4.5) | Golden JSON; a test that `--json` output contains no prompt text without `--include-prompts` |
| 4.12 | Clubs and boards: hashed join codes, consent opt-in, board API over 90 days, shadowban admin | API tests |
| 4.13 | Onboarding: public-profile preview with "Make private"; consent cards with "Not now" most prominent and nothing preselected; device-code sign-in | Playwright asserts the primary-styled button on every consent card is "Not now" and that "Make private" flips visibility through the API |
| 4.14 | Recap feedback control, local storage, consented sharing path (D8) | Sharing is refused without a consent record |
| 4.15 | Privacy page ("what left this laptop") and `pairtime privacy show` | Test that `privacy show` output equals the field list of the next batch |

**M5 Breadth**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 5.1 | Copilot adapter: hooks, CLI `events.jsonl`, VS Code `chatSessions` | Golden fixtures |
| 5.2 | Gemini CLI adapter: hooks and chats reader | Golden fixtures |
| 5.3 | Devin dedupe by process ancestry; Cline file hooks (macOS, Linux) and task reader (all OSes) | Golden fixtures; dedupe test where one prompt arrives through two consumers |
| 5.4 | Local OTLP/HTTP receiver (JSON and protobuf); `env` block installer; dropping `user.email`, account and organization ids | Fixture OTLP payloads; dropped attributes never reach the store |
| 5.5 | WakaTime-compatible local endpoint (`heartbeats`, `.bulk`, `statusbar/today`); `~/.wakatime.cfg` installer (4.3) | A pinned wakatime-cli release, downloaded in CI, sends one heartbeat that reaches both a mock Hackatime and the agent; config goldens for "has Hackatime" and "no config" |
| 5.6 | VS Code extension: platform VSIX packages bundling the binary, first-run consent and install, editor events with change shapes, status-bar item | `vsce package --target` for every target; extension tests under `@vscode/test-electron` in CI |
| 5.7 | Plugin SDK: inbox directory and `POST /v1/events`, schema validation, `adapters list`, conformance kit, example Python plugin | The example plugin passes the conformance kit; invalid events are rejected and counted |
| 5.8 | Generic hook mode with TOML mappings | Mapping fixtures produce golden events |
| 5.9 | AGENTS.md `track` snippet installer and `pairtime track` with D9 corroboration | Tests: an uncorroborated call is not an anchor; a corroborated one is level C |
| 5.10 | WSL mode: a Linux agent in WSL spooling to `/mnt/c/...`; the Windows agent ingesting it and supplying presence (CP 6.4) | Simulated-path tests; detection through `WSL_DISTRO_NAME` |
| 5.11 | Opt-in `keycount`: Raw Input on Windows, XI2 on X11, listen-only event tap on macOS; per-slot counts and injected share only | Compiles on all; a struct-level test that no field can hold a key identity or a per-key timestamp |

**M6 Submissions and review**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 6.1 | Client leaves and batch roots (TP B6); `K_dev` in DPAPI, Keychain or Secret Service through `go-keyring`, with a `0600` fallback | Roots recomputed by an independent test implementation match |
| 6.2 | Submissions API: create (idempotent `external_id`), metadata reveal, verify against roots, CSPRNG challenge after the reveal, content reveal check, states, overlap rejection | End-to-end test; a challenge is never issued before the metadata reveal |
| 6.3 | Client submit flow: window choice including devlog windows (D7); redaction (known values, gitleaks rules as data, PII patterns with validators); preview, withhold, `--dry-run` | Redaction tests over gitleaks' rule test cases; `--dry-run` output equals the real upload |
| 6.4 | Review page: header, story column, rows with cut and restore (D12), minute drawer, commits, revealed sample, flags, access log | Playwright; an API test that approved seconds never exceed captured |
| 6.5 | Handbook justification generator; the link page for no-code programs | Golden text for a fixture submission in the SP 10.6 shape |
| 6.6 | Program OAuth with scopes; Hackatime-compatible reads with Hackatime's identifier resolution | Contract tests replaying the request shapes programs send today (HC 3.3, SP 10.5); response JSON matches Hackatime's shapes |
| 6.7 | Webhooks with `X-Pairtime-Signature`, retries, `updated_since` | A verifier written to Robert's scheme accepts our signatures |
| 6.8 | Evidence bundle, `pairtime verify`, daily-root inclusion proof | `verify` passes on a fixture bundle and fails on any single-byte change (fuzz) |
| 6.9 | Tier-2 flags for submitted windows (SM 8.7), thresholds from a private config file with SM's public starting points as defaults, the audit probability of 6.4, seeded known-answer submissions | A test that flags never run on unsubmitted windows; audit draws match `p` within tolerance over many trials |
| 6.10 | Program kit: Ari `journals[]` exporter, a sample `HackatimeService` configuration, an Airtable automation snippet, docs | Exporter output validates against the field list Ari's ingest accepts (HC 3.6) |
| 6.11 | Cross-device merge (SM 3.4) and overlap checks (SP 10.2) on the server | Tests with two devices crediting the same slot |

**M7 Hardening**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 7.1 | Full GoReleaser: Scoop bucket, Homebrew tap, winget manifest, npm per-platform packages, Ed25519-signed update manifest | A release dry run produces every artifact; the manifest signature verifies |
| 7.2 | Self-update: daily check, verify, swap, the Windows rename dance, no self-update for package-managed installs (CP 4.6) | Tests against a local manifest server on three OSes |
| 7.3 | Windows signing (SignPath or Artifact Signing) and macOS notarization with quill, gated on secrets | With secrets present, `Get-AuthenticodeSignature` reports valid on `windows-latest`; skipped cleanly without them |
| 7.4 | Encryption at rest for prompt text, retention jobs, "delete everything" | Raw text never appears in the database file's bytes; retention purges at the configured ages |
| 7.5 | Privacy hub endpoints: ES256 JWT, dry run, single-use `jti` | Tests with generated keys, including replay rejection |
| 7.6 | Footprint gates: binary under 15 MB; agent RSS and CPU sampled over 10 idle minutes on `windows-latest` and `ubuntu-latest`; hook p95 | Runner-adjusted thresholds enforced |
| 7.7 | `loadtest.yml`: synthetic clients at the 8x peak against a 2 vCPU, 8 GB container (9.4) | p99 under 500 ms, CPU under 70%, report uploaded |
| 7.8 | Deploy bundle: `compose.yml` (pairtimed, Litestream, cloudflared), runbook, scheduled restore drill, uptime check | The scheduled restore drill passes `PRAGMA integrity_check` and the smoke tests |
| 7.9 | `pairtime doctor --report`: a redacted footprint report the teen can choose to share | A test with planted usernames and paths finds none in the report |

**M8 Calibration**

| WP | Scope | Acceptance (CI) |
|---|---|---|
| 8.1 | Labeling tool: a Lookout timelapse (by session token; HC 4.2) beside the pairtime timeline, minute labels, two-rater workflow, κ | Playwright; κ unit tests |
| 8.2 | Fitting harness: ±25% sensitivity, coordinate descent with the asymmetric loss, leave-one-participant-out CV, parameter ranges from SM 7.5. Real labels run only in a private repo's `workflow_dispatch` on encrypted data | On a synthetic labeled corpus in the public repo, it recovers planted parameter values within tolerance |
| 8.3 | Baseline report: Hackatime raw, handbook third, grit rules, `active_time` (SM 7.3) | Produces the comparison table on the synthetic corpus; output is schema-checked |
| 8.4 | Red-team fixtures T1 to T15 with expected credit (SM 7.3's adversarial row) | 0 for T1, T2, T3 with `input_keys`, T7 and T14; 0 witnessed for T8; at most the free attempts for T15 |
| 8.5 | R3 metrics job: weekly cohort retention and the never-submitted share of credited time (section 2) | Tests over fixture rollups |

### 10.4 The first shippable milestone: Coach alpha

M0 to M3 in full, plus M4 packages 4.1 to 4.11, 4.13 and 4.15. A teen on Windows 10, macOS or Linux runs one install command (unsigned or SignPath-pending, with the SmartScreen caveat in the README; CP 4.3). From then on, Claude Code, Codex and Cursor sessions are captured through hooks and logs, along with presence, focus, power and git, and scored on the device by the SM rules, which pass the regression corpus. The teen gets the welcome recap from lookback, a recap after every session with a notification, the local dashboard (Today, Recaps, Projects, Logbook, Devlogs, Build days, Privacy), the coach skill for their agent, and devlog publishing. They sign in with HCA through a device code, and uploads are witnessed. The public profile is aggregate-only, with build days, a weekly goal and freezes, and the owner's dashboard is live. There are no submissions yet, so nothing in the alpha is worth cheating for. That makes it the right moment to learn whether teens keep it installed (R3), before prizes distort the signal.

## 11. Risks and cuts

### 11.1 Risks

| # | Risk | Signal that it's happening | Response |
|---|---|---|---|
| 1 | **The bet fails: teens don't keep it.** Recaps get ignored, and the tool is installed for a deadline and removed afterwards | Alpha 4-week retention under 30%, or under a quarter of credited time on never-submitted projects (section 2, R3) | Interview the alpha cohort and iterate on recap content before 1.0. The weekly story may turn out to be the real hook, rather than the per-session recap. Adoption still works through programs, only more slowly |
| 2 | **Notification fatigue, or feeling watched.** 58% of monitored students self-censor (PA 4.4) | Teens muting notifications, or the Pause button pressed often | One notification per session, quiet hours, a single off switch, recap copy written with teens. No streak-loss alarms of any kind |
| 3 | **Level B is the Windows default** (D3). If Quality and Integrity treat level B as weak, Windows teens get the handbook third, and that hits the "average Hack Clubber" hardest (BOUNTY.md R8) | Q&I's answer to SM open question 1 | D10 lifts editor work to level A without a keyboard hook. Audit weighting replaces blanket deflation. The key counter stays offered neutrally, never pushed, because the UK code bans nudging children to give up protections (TP B2) |
| 4 | **Counsel overrules the public-by-default profile for minors** (TP open question 5) | Legal review | A config flag switches to the unselected choice. R5 then rests on the reading that an aggregate profile that is one click from public is still public by default, which Hack Club has to accept |
| 5 | **HQ-side dependencies are slow**: Hard Mode approval, attribution through `Alternate Tracking Method`, Robert's `kind`, Ari's source | Weeks pass without answers | Every route in 8.2 has a path that needs no HQ change: the link page, Ari `journals[]`, a second `HackatimeService`, and low-ceremony forms. Requests go out in week 1 (8.5) |
| 6 | **grit gets there first.** It already implements the core thesis (PA 1.14), and the first entry to 1,000 weighted projects wins | grit announces program adoption | Ship the alpha in two weeks; compete on what grit barely has, which is the coach (R3), calibration (R2) and the program path (A); and stay open to collaborating, since both are AGPL (PA open question 8) |
| 7 | **The coach says something false** ("you fixed it by hand" when the agent did) and teens stop trusting the numbers | "It missed something" or "about right" answers drop | Moment rules only claim what the events show, and they err toward saying nothing. Feedback answers flag the rules that misfire |
| 8 | **The old laptop suffers**: lookback on a spinning disk, antivirus false positives on a Go binary (CP 1.6) | `doctor --report` data from pilot volunteers; support requests | Low-priority lookback with a progress bar; Cursor's database reader deferred; signing as early as an identity allows; no global keyboard hook by default; footprint gates on every release |
| 9 | **Signing is late.** SignPath wants an existing release and some reputation, and Smart App Control on Windows 11 blocks unsigned binaries (CP 4.3) | Users blocked at install | Apply for Azure Artifact Signing under Hack Club in week 1 (1 to 30 business days of validation; CP 4.3). Use the VS Code extension channel, where the editor unpacks the binary. Most target laptops run Windows 10, which has no Smart App Control |
| 10 | **Programs object to AI help with devlogs** | Program rules or reviewer comments | Drafts are outlines of facts, and the sentences are the teen's. Agent help is labeled `ai_assisted`, and a program can require that it stay off (4.5) |
| 11 | **Calibration data is too thin to show R2** (SM Risk 9) | Fewer than 300 labeled sessions by early January | Run the pilot over winter break, use Lookout instead of building capture, use the D8 sampler to label the most informative sessions, and ask Hack Club for Stardance's per-devlog review data (SM open question 6) |
| 12 | **Hard Mode looks like self-dealing** (TP A6 U5) | Community or HQ concern | Outside reviewers, a higher spot-check rate requested from Q&I, and the daily public root (6.5) |
| 13 | **A revealed prompt exposes something** (redaction recall is imperfect; TP B4) | A purge request | A small `k`, reviewer-only access with logging, and a one-click purge that re-opens the affected hours (TP B4, B8) |
| 14 | **Uncovered setups**: SSH and cloud dev boxes (SM 3.4), Chromebooks (CP open question 3), browser-only builders (HS 10) | Support requests | Stated as gaps. v1.5 adds the SSH join, and v2 the browser extension. Commits from these setups still show on the project story as work pairtime can't see |
| 15 | **The home server goes down near a program deadline** (SP 9.3) | Uptime check alerts | Move the same compose file to a VPS before Hard Mode pays out (9.3) |

### 11.2 What to cut first

Cut in this order. Each cut keeps every requirement met, and costs R3 polish or breadth rather than a requirement:

1. Club boards (WP4.12). Public profiles and build days stay.
2. Habit trends and weekly challenges (part of WP4.5). The logbook of firsts and beaten errors stays.
3. Recap feedback (WP4.14, D8). Calibration falls back to SM 7.2's self-labels.
4. WSL mode (WP5.10). Teens run the harness on the Windows side in the meantime.
5. Cline and Devin dedupe (WP5.3) and generic hook mode (WP5.8). The plugin inbox (WP5.7) still satisfies R7's plugin approach.
6. The opt-in key counter (WP5.11). Windows stays at level B, D10 still lifts editor work, and audit weighting covers the rest.
7. OG images and README badges.
8. Self-update (WP7.2). Package managers and re-running the install script cover updates.
9. Hackatime-compatible reads (WP6.6). The link page and handbook text (WP6.5) keep the no-code route open, and the submissions API keeps the full route open.

Never cut, because each one carries a requirement:

- The SM scorer and its regression corpus (R2, R9).
- Witnessing and the chain (R9, R10).
- The welcome recap and the session recap (R3).
- Autostart, hooks and lookback (R4).
- The server privacy firewall and the Privacy page (R6).
- Submissions, the review page, the handbook text and `pairtime verify` (R9, R10, A).
- The load test (R11).
- The license gate (R1).
