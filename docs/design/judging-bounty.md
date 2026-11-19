# Judging: the bounty lens

I judged the three designs from Hack Club's side of the table: Zach Latta, who wrote the bounty; Quality and Integrity (QI), who wrote the handbook's one-third rule for "ai coding" time; the YSWS program authors who would have to adopt pairtime; and Fraud Squad as a customer.

I read each of R1 to R11 and A literally against the verbatim text in [BOUNTY.md](../BOUNTY.md), and a design that wouldn't satisfy the words loses points even when its intent is good. Two more criteria come from this lens: how likely programs are to adopt it soon, and how long it takes to reach 1,000 weighted projects. grit is already ahead ([prior-art 1.14](../research/prior-art-and-measurement.md)), and the bounty goes to the first system that meets the criteria, so speed to adoption carries the most weight.

Status: complete. I read all three designs in full and checked the claims they depend on against the research. I treat TP's numeric inputs as illustrative, because its fact-check is still running.

## Scores

Scores run from 1 to 10. R1 to R11 each have weight 1. A, program adoption soon and time to 1,000 each have weight 3, so together they carry 45% of the overall score. The overall score is the weighted mean.

| Criterion | Weight | Adoption | Evidence | Teen |
|---|---|---|---|---|
| R1 License | 1 | 9 | 9 | **10** |
| R2 Accuracy | 1 | 5 | **7** | **7** |
| R3 Worth using | 1 | 6 | 6 | **9** |
| R4 Passive | 1 | 8 | 8 | 8 |
| R5 Server | 1 | 8 | 8 | 8 |
| R6 Privacy | 1 | 7 | **8** | **8** |
| R7 Harnesses | 1 | **8** | 7 | **8** |
| R8 Platforms | 1 | 7 | 8 | **9** |
| R9 Trustworthy | 1 | 6 | **8** | 6 |
| R10 Auditable | 1 | 8 | **9** | 8 |
| R11 Cost | 1 | 9 | 9 | 9 |
| A Acceptance | 3 | **8** | 7 | 7 |
| Program adoption soon | 3 | **8** | 6 | 6 |
| Time to 1,000 | 3 | **7** | 6 | 5 |
| Mean of R1 to R11 alone | | 7.4 | 7.9 | **8.2** |
| **Weighted overall** | | **7.5** | 7.2 | 7.2 |

Read the last two rows together. On the requirements alone the ranking reverses: teen first, evidence second, adoption last. Adoption comes out ahead only because this lens weighs the race. That doesn't make it the right design as written. It makes it the right base, provided it takes in what the other two do better. The bounty goes to the first system that "meets the above requirements" and reaches 1,000 weighted projects, so a fast design that fails R2, R3 or R9 under Zach's reading wins nothing.

Some weaknesses don't show up in the scores because all three designs share them:

- **The native program is everyone's critical path.** All three lean on a pairtime-run YSWS for 200 to 450 weighted projects (adoption 8.3, evidence 8.3, teen 8.2). Zach's "Yes!" allows it, but it still needs an HQ sponsor and per-weighted-project grants (HC 3.2, HC open question 9). No design has a funded plan B.
- **Level B is the common case, and nobody has QI's answer.** On the "average Hack Clubber's" 10-year-old Windows laptop, minutes are level B unless the opt-in key counter is on (SM 2.1, SM risk 1). Whether that counts as "significant evidence" decides whether pairtime can ever credit more than a third (SM open question 1).
- **Thin margins.** Adoption plans exactly 1,000 weighted projects, teen exactly 1,000, evidence 1,100. grit's repo was created at 00:24 UTC (PA 1.13), about 45 minutes after the announcement at 23:38:41 UTC (adoption risk 4 says 47). Against a competitor that fast, there is no room for even one program to slip.
- **The drop-in isn't a drop-in yet.** None of the three answers every call that Stardance's `HackatimeService` makes (HC 1.8, HC design implication 9). The adoption section has the details.

## Adoption first (approach-adoption.md)

**The bet in one line.** Install next to Hackatime, take nothing away from any program, and hand reviewers a pre-deflated number in the handbook's own justification format.

The claims this design stands on hold up. The `[api_urls]` dual-send is confirmed from wakatime-cli v2.28.4 source: every matching pattern gets a copy, transcript-derived AI heartbeats are copied too, and a malformed key breaks Hackatime for the whole run (HC 1.5; SP 8.8, fact-check row 31). The handbook's `Alternate Tracking Method` field does cover "something other than Hackatime or Lapse", and HC 3.7 point 1 concludes that no HQ change is needed to start. HC 3.4 says outright that "a pairtime client that answers the same calls [as `HackatimeService`] ... gets adopted." Robert takes only `kind: "hackatime"` plus project names (HC 3.5), so keeping the Hackatime half alive does leave Fraud Squad's desk unchanged.

| Criterion | Score | Note |
|---|---|---|
| R1 License | 9 | AGPL everywhere, plus a CI license gate (WP 0.1). Private detection rules run as a separate process, the Ari pattern, and every requirement is met without them (3.2) |
| R2 Accuracy | 5 | It adopts SM 1-5 unchanged. Its own calibration plan (section 5) replaces SM 7's labeled set, where two raters label against a timelapse, with reviewer cuts from the pilot program. That has two problems. Reviewers see pairtime's number first, so their cuts are anchored on it. And rows can only be cut (6.2), so the labels can show over-crediting but never under-crediting. The first refit is in late January (8.5), after route A has already decided submissions on uncalibrated guesses. The design was written while SM 7 was unfinished, so it never adopts SM 7's κ, bias and 0.9 approved-to-credited targets |
| R3 Worth using | 6 | Chess clock, a weekly recap of loops, streaks with freezes, a devlog draft, and lookback that fills the dashboard on day one (section 2). But the pitch partly leans on Hackatime for the motivation R3 asks pairtime to provide "in and of itself" ("pairtime keeps all of that by not replacing Hackatime"). macOS gets no tray in v1 (3.3). The cut list drops leaderboards, badges, OG images and weekly emails first (11.2). Read literally, it is the closest of the three to "just an anti-fraud tool" |
| R4 Passive | 8 | Login autostart, hooks registered once, editor time arriving through the dual-send, and 30-day lookback at install (section 2). All three R4 stories are worked through. Codex's one-time `/hooks` trust is setup, not a per-session step |
| R5 Server | 8 | Accounts, `/@username`, projects keyed by root commit, git with forge verification, and an SSE dashboard (3.4). Profiles are public by default, aggregate-only, with an equal-weight "Make private" (7.3). The owner's view updates in real time and the public view hourly, which satisfies "a stats dashboard that updates in real-time" |
| R6 Privacy | 7 | Logged hours upload numbers only. Heartbeats stop at `127.0.0.1`. A schema firewall rejects free text with a 422, and `privacy show` prints the next upload byte for byte (3.6). But a submission reveals event metadata for every event in the window, other projects included (3.6, 7.1), which the evidence design's project-scoped reveal avoids. "Pairtime's server gets less than Hackatime does" is still a pitch privacy-minded users can check |
| R7 Harnesses | 8 | Six native hook adapters, the WakaTime bridge reaching wakatime-cli's 46 transcript parsers at reduced strength, and Karim's AGENTS.md entry (4.1 to 4.4). This is the widest day-one coverage of the three. D3 lets `track` anchor weakly, so a hookless harness isn't worse off than under Hackatime |
| R8 Platforms | 7 | Pure Go, seven cross-compiled targets including `windows/386`, footprint budgets enforced in CI, and a stated caveat that a CI runner isn't a 2016 laptop (WP 4.11). But it builds everything on Go 1.27 (3.2), which drops macOS 12 Monterey, the last release for roughly 2015 and 2016 Macs (CP 1.5). Both other designs keep darwin builds on Go 1.26 until February 2027 |
| R9 Trustworthy | 6 | SM's credit rule, witnessing, commit anchors, random reveals, risk-weighted interviews, and Hackatime as a second receipt clock (6.1). Two weak spots. Most minutes on Windows are level B until typing evidence ships in v1.5, after code signing (D6, risk 3), and the 10-year-old Windows laptop is the common machine. The Hackatime cross-witness adds an independent clock, not independent proof that a human was there, because the same attacker controls what wakatime-cli sends |
| R10 Auditable | 8 | Frozen submissions, a signed bundle, offline `pairtime verify`, and daily roots published to a public git repo (6.2). It meets the handbook's spot-check standard of "follow the links, and reach the same conclusion". It proves the number follows from the events, not that the events are true, which is also true of every design here |
| R11 Cost | 9 | $48.94 a month minimal and $149.68 with a standby (section 9). Heartbeats are never stored on the server |
| A Acceptance | 8 | The best-specified path into `approved_projects`. It has the program kit, the Airtable script, every handbook field mapped, the Ari `journals[]` bridge instead of `program_seconds` (which Ari treats as evidence-free), and a mechanical counting rule proposed to HQ (8.4) |
| Program adoption soon | 8 | Tier 0 is one form field. Tier 1 is a second `HackatimeService` pointed at a new base URL. Robert and Telescreen keep working unchanged. It does overclaim that tier 0 needs "nobody" to agree (4.7). The program author and its reviewers still have to accept pairtime numbers, and a reviewer used to Ari's flat third will push back |
| Time to 1,000 | 7 | A dated plan reaching 1,000 by 2027-03-31 across three routes (8.3, 8.5). Route A's 350 weighted projects means 3,500 approved hours from a first-time program team over winter break. That needs an HQ sponsor and about $30,000 of grants at $85 per weighted project (HC 3.2), and the design lists it as risk 5 without solving it. M0 to M3 is 47 work packages in about three and a half weeks |

One more literal gap, which the other two designs share. The tier-1 drop-in says a program can "point a second instance of the existing `HackatimeService`" at pairtime (section 1). But Stardance's client sends `filter_by_project[]=a&filter_by_project[]=b`, and it also calls `authenticated/api_keys`, `admin/v1/user/get_user_by_email` and `my/heartbeats` (HC 1.8). WP 3.6 specifies only the comma-string form, and 4.7 lists neither the email lookup nor `my/heartbeats`. HC design implication 9 already says to accept "an array or a comma string" and to serve `get_user_by_email`. Without those, "a day" of integration becomes a debugging session in someone else's Rails app.

**Overall: 7.5** (weighted; see Scores). It is the only design whose adoption story a program author could act on this week. It also does the most to blunt grit's head start, because grit asks programs to change and this design asks them to add one field.

**Fatal flaws.** None outright, but two come close.

- **Attribution ambiguity.** Route B pitches pairtime as "optional evidence for AI time above a third" (8.3), on top of Hackatime hours. If a program's number still comes from Hackatime, with pairtime only as justification, Zach can fairly say Hackatime tracked those projects and pairtime argued for them. The acceptance text is "used to track". On every counted row, pairtime's credited figure has to be the program's hours number, not a supplement to it. Question 3 in 8.6 asks about this, but the plan treats the answer as open when it should depend on a yes. It costs one sentence of policy to fix, and it puts 450 of the plan's 1,000 weighted projects at risk if left alone.
- **R3 by proxy.** Leaning on Hackatime for motivation doesn't satisfy "meaningful and motivating to use in and of itself".

**Best ideas to graft.**

- The dual-send through `[api_urls]`, with key validation, a distinct match-all pattern, and the `e2e-wakatime` CI job against the real binary (4.5, WP 1.16). This is what lets pairtime live next to every teen's existing Hackatime. The line in risk 1, that one report of "pairtime broke my Hackatime" would end route B, is the most important sentence in all three designs.
- The three adoption tiers (section 1) and the program kit in `integrations/` (8.3).
- The generated `Alternate Tracking Method` text with `handbook_s` side by side, and a `Deflation Justification` draft that a human must confirm (8.2).
- D4. Compute `hackatime_logged_s` and the `ai coding` split from the same heartbeats Hackatime received, so the one-third comparison is one a reviewer can reproduce.
- Hackatime-shaped reads that return credited seconds plus a `pairtime` extra object, with `final: false` for windows still settling (4.7).
- The Ari `journals[]` bridge and the "Robert: change nothing" note (4.7).
- The mechanical counting rule, where a row counts when its justification carries a pairtime review URL, plus pairtime's own outcome ledger as an early warning (8.4).
- The four ordered questions for QI (8.6), and making that conversation a week-one milestone.
- Asking QI to run or observe audits in a pairtime-run program, because of attack U5 (6.3).
- D5. Submission rows are work sessions, not commit intervals.
- D6. Show programs the share of credited time at evidence levels A, B and C.

## Evidence first (approach-evidence.md)

**The bet in one line.** Win the gatekeepers, Fraud Squad and QI, with hours they can re-verify offline, and let programs follow HQ's guidance.

The arithmetic and the sources check out. The audit formula reproduces TP A6's break-even (0.079 and 0.278 at 10 h and 40 h, so 0.087 and 0.306 with the 10% margin). The CC8 sample catch rate (`1 − 0.9^10` = 0.65) and the reviewer-hours estimate (37.5 h of audits plus about 71 h of plain reviews) are correct. TP labels its own inputs as guesses ("my guess; measure it", TP A6), and TP's fact-check is still running, so the formula's constants are placeholders, which the design says. The point that every submitted project's Code URL must be public is the handbook's own rule (HC 3.7), so forge evidence asks nothing new of teens. That is the best single insight in this design.

| Criterion | Score | Note |
|---|---|---|
| R1 License | 9 | AGPL, a license check, and a stdlib-only Python reference for the ledger (WP 1.1), so an outside party can build its own verifier. The private sentinel raises the same open question as in the adoption design, with a stated fallback: publish it and raise the audit floor (risk 4) |
| R2 Accuracy | 7 | One of two designs that build SM 7's labeled set into the plan, with a labeling mode where two labelers work beside a screen recording and get a κ report (WP 4.10). Submissions open after scoring 1.0.0 is frozen (8.4). Reviewer cuts and D8 restores give labels in both directions, and seeded known-answer cases measure the reviewers too (5.4). It still has no measured number, and it says so ("R2 is a hypothesis with a test plan") |
| R3 Worth using | 6 | Chess clock in the tray, locally written recaps that name the exact loop ("asked for the same navbar fix 7 times"), streaks on credited minutes, consistency leaderboards, and a local-only mode (section 1, 7.1). But the local dashboard is WP 5.1, built after the evidence core, and the tray is the third thing cut (11.2). R3 is the last thing built here |
| R4 Passive | 8 | No start or stop, 30-day lookback, and all three R4 stories as CI fixtures (WP 0.3). D10 samples presence only in a project context, with no loss of credit (SM 2.5) |
| R5 Server | 8 | Every element, specified down to the endpoint (3.6). Public by default, aggregate-only. The `profile_default = "ask"` switch (7.3, risk 5) would not meet R5's words if it were used, and the design admits Hack Club would have to decide |
| R6 Privacy | 8 | The strongest structure of the three. The reveal is project-scoped, so other projects' events stay hidden (6.5). It never reads window titles, samples only in a project context (D10), has a true local-only mode, and crypto-shreds by deleting `K_dev` (3.4). One cost is that a challenged turn reveals the agent's final message as well as the prompt (D3), and that message can carry code. The teen previews it and can withhold it |
| R7 Harnesses | 7 | Six hooked harnesses with a second record for each, AGENTS.md for a long list of others, and a plugin interface whose events go through the same OS checks (4.1 to 4.4). There is no WakaTime AI-heartbeat bridge, so it misses the adoption design's day-one long tail. It also changes Karim's entry from "take the user's prompt, strip PII, pass it to `track`" to a 5-to-12-word summary the model writes (4.4). The cost reason holds (HS 11.2), but Karim's direction has to be honored, so this belongs in front of Karim as a proposal, not settled in the design |
| R8 Platforms | 8 | Like the teen design, it keeps macOS builds on Go 1.26 until February 2027 for Monterey-era Macs (3.9; CP 1.5). D6's `offline` label means a teen without home internet who syncs at school isn't capped, and that teen is the R8 audience. Footprint gates run in CI |
| R9 Trustworthy | 8 | Best of the three. Eleven named consistency checks (6.4), held minutes when the server's re-score disagrees (D7), level-C time submittable only inside a forge-verified commit interval (D5), and bands that keep a level-B jiggler-plus-pty attack out of "corroborated" (6.7). It adds a published audit formula that rises with claimed hours (6.9) and reviewer integrity with seeded cases (6.10). The attacker's ladder (6.12) is frank about step 2b |
| R10 Auditable | 9 | Best of the three. The bundle carries the challenge value `r`, so an auditor can recompute the draw. It has inclusion proofs into the public day root, a Python reference verifier, and the bundle's SHA-256 written into the justification so a spot-checker knows which evidence the reviewer read (6.6). This is R10's "auditable by an external party (ex. program authors, reviewers, and Fraud Squad)" taken literally |
| R11 Cost | 9 | The same box as the others, because the evidence work runs on the client (section 9) |
| A Acceptance | 7 | A handbook path with no HQ change, `PairtimeService` clients in Ruby and TypeScript, an Ari PR, a Robert `kind: "pairtime"` proposal, and a fixed first line, "Tracked with pairtime <version>", so HQ can count rows with one search (8.2). The plan sums to 11,000 hours across five routes (8.3) |
| Program adoption soon | 6 | The pitch goes to the gatekeepers first (8.1). Its results would last longest, and it is the slowest route. For program apps the integration is a new client of about 200 lines and a "Connect pairtime" button (8.2), not a base-URL swap. Hackatime-compatible reads, the cheapest integration any program author has, are fourth on the cut list (11.2). The review page asks reviewers to learn bands, checks and must-view rows. That should be faster once learned, but it is new, and risk 11 admits it |
| Time to 1,000 | 6 | First Unified DB rows come between January and mid-February, and 1,000 weighted projects by 2027-04-30 (8.4). That is a month after the adoption design's date, with grit already running. The native YSWS target of 200 weighted projects is more believable than the adoption design's 350. The second native program carries 2,500 of the 11,000 hours, in spring |

**Overall: 7.2** (weighted; see Scores). This is what Fraud Squad and QI would design for themselves, and it is the design most likely to get the "significant evidence" ruling that decides whether pairtime can credit more than the handbook's third. But it treats the cheapest adoption paths as optional, which is the wrong call in a race that goes to whoever finishes first.

**Fatal flaws.** None outright, but two come close.

- **Priorities inverted for a race.** Hackatime-compatible reads can be cut (11.2, item 4), R3's features come last (M5), and the plan starts with the slowest audience. The plan expects QI guidance between February 14 and April 30 (8.4). Until it arrives, existing programs wait, and only the native programs count.
- **Review-page complexity.** Bands, eleven checks, must-view gating and a policy preset are a lot for a volunteer program reviewer. The 5-minute median is a target with nothing measured behind it yet. If reviewers find the page heavy, they fall back to the flat third (risk 11), and pairtime loses its reason to exist.

**Best ideas to graft.**

- The project-scoped Merkle reveal, with headers for every event and bodies only for the submitted projects and for presence, power and focus (6.2, 6.5, D2).
- A challenge draw neither side can steer, because the server commits to `r` before it sees the metadata reveal (6.5, steps 1 to 3).
- Revealing the agent message that each challenged prompt answered (D3). It is the reviewer's best test for an LLM writing the prompts.
- CC1 to CC11 (6.4), especially CC1 (hook against transcript), CC3 (`unexplained_lines` against the git diffstat) and CC4 (three clocks per commit).
- The corroborated, witnessed and backfilled bands (6.7, D9), with must-view rows and "approve remaining" locked until they are opened.
- D5 (level-C time only inside a forge-verified commit interval), D6 (the `offline` label for school Wi-Fi), D7 (held minutes), D8 (reviewers may restore pairtime's own discounts, with QI's agreement) and D10 (sampling only in a project context).
- The bundle's SHA-256 in the justification text, and the fixed "Tracked with pairtime <version>" first line for counting (6.6, 8.2).
- The published audit formula with an hours term (6.9), and seeded known-answer cases for reviewers (6.10).
- Labeling mode for SM 7.2's pilot set, with κ computed in the app (WP 4.10).
- Local-only mode (7.1), `pairtime backup` (risk 7), and integer-only scoring so re-scores match across platforms (risk 9).
- The per-platform VSIX that brings the signed binary (section 1, WP 5.4; CP 4.2), an install path teens already know.
- Go 1.26 for macOS until February 2027 (3.9).
- The rule to submit through pairtime only for windows the program hasn't already paid through Hackatime (8.3).

## Teen first (approach-teen.md)

**The bet in one line.** Make pairtime a coach teens keep for its own sake, so that by the time a teen submits, months of witnessed evidence and ready-made devlogs already exist.

The research claims check out. Stardance caps payouts at 10 hours per devlog (`MAX_PAYOUT_HOURS_PER_DEVLOG`, HC 3.3). After GitHub removed streak counters, one-contribution days became less common (PA 4.1). Deci, Koestner and Ryan's effect sizes are quoted correctly (PA 4.2). Smart App Control is a Windows 11 feature (CP 4.3). The audit formula gives 0.11 at 10 h and 0.29 at 40 h, which clears TP A6's illustrative break-even. One claim does not hold, and it matters to Fraud Squad (D10, under R9 below).

| Criterion | Score | Note |
|---|---|---|
| R1 License | 10 | The only design with no closed code in the shipped system. Detector code is public, and only the threshold values are private configuration (D13). That sidesteps the AGPL section 13 question the other two leave open, and it satisfies "it must be open source" under the strictest reading |
| R2 Accuracy | 7 | It adopts SM 7 whole, freezes scoring 1.0.0 before 1.0 ships (8.5), and builds the labeling tool beside a Lookout timelapse (WP 8.1). Two additions fit a requirement that is about teenagers: age bands in the calibration strata, with any fix applied globally and never per age (D11), and recap feedback used to choose which sessions get labeled, never to change credit (D8). D12's restorable loop rows make reviewer labels two-sided. Still unmeasured |
| R3 Worth using | 9 | By far the strongest. Session recaps with "moments", a logbook of beaten error families, build days with a weekly goal and freezes instead of a daily streak counter (D1), club boards ranked on consistency, devlog drafts that are also the unit programs review, a welcome recap from lookback, and the coach skill aimed at the teen's own agent (1, 3.6, 4.5). Each feature cites the research behind it (PA 4.1 to 4.4). It is also the only design that defines a test R3 can pass or fail: 50% four-week retention, and at least half of credited time on projects never submitted, both measured from tier-1 data with no extra telemetry (section 2). That is the BOUNTY.md checklist item, made measurable |
| R4 Passive | 8 | The same capture as the others, plus Cline and a WSL mode (WP 5.10, though WSL is fourth on the cut list). The three stories come from SM 6 |
| R5 Server | 8 | Every element, plus build days, README badges and OG cards on the public profile. Aggregate-only by default, with a one-flag fallback to "ask" that would not meet R5's words if used (risk 4) |
| R6 Privacy | 8 | The most personal analysis (prompt kinds, skills, habits) runs only on the device. Tier 1 carries no per-prompt records (3.9), though its minute buckets still carry anchor ids, which the evidence design drops (evidence D1). The coach warns a teen who pasted a key that the key already reached the model provider (7.3). One cost is that the coach skill sends a project summary to the teen's model provider when the teen asks for one |
| R7 Harnesses | 8 | Six hooked harnesses plus Cline, a no-code generic hook mode for any harness with Claude-style hooks (4.2), plugins in any language, and the VS Code extension. Karim's AGENTS.md entry ships closest to his wording, with the message's PII removed, cut to 400 characters and passed to `track` (4.4). The coach skill is a second use of Karim's "(or a skill)" idea. No WakaTime AI-heartbeat bridge |
| R8 Platforms | 9 | The most attention to the 10-year-old laptop: Go 1.26 for darwin, EcoQoS, a `macos-15-intel` runner, lookback at low priority with a progress bar, the Cursor database reader deferred because of spinning disks, and real-hardware footprint reports through `doctor --report` (3.10, 10.1, WP 7.9) |
| R9 Trustworthy | 6 | The SM credit rule, witnessing, reveal sampling and published audits. Hard Mode's reviewers come from outside the build team (6.5), which is the right answer to attack U5. But D10 sets `input_keys`, which is SM's definition of level A ("anchors backed by keyboard input", SM 5.1), from the shape of document changes the editor extension reports. The design says this is "no weaker than level A's existing signal", and that is wrong. Any extension, or an agent writing through the editor API, can make character-by-character edits at typing speed without touching the OS input queue. Add a mouse jiggler for `input_any`, and a Windows laptop gets level A with no keyboard involved. An OS key counter with injected-flag checks catches `SendInput` (TP A4), but nothing catches this. It overstates the evidence at exactly the level QI is being asked to rule on. Separately, the submission reveal leaves out other projects (3.9) but doesn't say how it stops a modified client from labeling a lock or suspend event with another project and dropping it, which the evidence design's leaf header prevents (evidence 6.2). "Depth of history" (6.2) only pays off months after launch |
| R10 Auditable | 8 | Signed bundle, offline verify, the daily root, a minute drawer that replays SM 2.7 by hand, and devlogs as the reviewer's table of contents (6.3). The challenge draw comes from a server CSPRNG that an auditor can't recompute, unlike the evidence design's committed `r` |
| R11 Cost | 9 | The only design that counts recurring code-signing fees in its total: about $168 a month recommended, 34% of the cap (9.3). The other two list them outside the hosting line |
| A Acceptance | 7 | The same three tiers as the adoption design, the Ari `journals[]` path with devlog windows as the unit (D7), the link page, and attribution as the first QI question (8.3). It is thinner than the adoption design on the mechanics, with no counting rule beyond asking QI and no D4-style `handbook_s` computed from real heartbeats |
| Program adoption soon | 6 | The integration options are all there, but the strategy runs through teen pull ("programs adopt what their participants already have installed", A), which takes months to build. Submissions are M6, after the coach (10.2). That reverses the order HC design implication 21 recommends: "Ship the submission, review page and handbook-shaped justification before profiles and leaderboards". Hackatime-compatible reads are on the cut list, though last (11.2, item 9). The story column, where devlogs link to their minutes, would make a reviewer's first read faster |
| Time to 1,000 | 5 | 1,000 weighted projects by 2027-04-30 with no margin (450 + 350 + 200, 8.2), and the funnel needs 5,000 installs, about a third of Hackatime's MAU (8.1). Hard Mode at 450 weighted projects is the most aggressive native-program target of the three, and it needs HQ approval and about $38,000 of grants. To its credit, it is the only plan that explains the lag, about six weeks per 10-hour project at Hackatime's average pace (8.5). Its 15% install-to-submit assumption is the most conservative of the three, and probably the most realistic |

**Overall: 7.2** (weighted; see Scores). This is the design Zach would most want to use himself, and the one most likely to pass R3 when someone tests it. Built as written, it reaches programs later than the race allows.

**Fatal flaws.** None outright, but two come close.

- **Sequencing.** The coach ships first. Submissions, the review desk and the program APIs come in M6 (10.2, 10.4). In a race decided by program adoption, the first build a program can use shouldn't wait on recaps, the logbook and clubs.
- **D10 overstates evidence.** If QI rules that level A is significant evidence and level B isn't, D10 hands level A to edits that software can produce. Fraud Squad would discover that in its first puppeteer case, and pairtime's credibility with QI would go with it.

**Best ideas to graft.**

- The coach for R3 (3.6): session recaps with moments, the logbook, project stories, a welcome recap from lookback, and plain-language reason codes that name agent time after the agent (D5).
- Build days with a weekly goal and freezes, no daily streak counter, a 72-hour witness rule for build days, and club boards over 90 days (D1).
- The R3 proof metrics from tier-1 data, four-week retention and the share of credited time on projects never submitted (section 2).
- Devlog drafts from evidence, devlog windows as a submission unit, and the story column as the reviewer's index (D7, 6.3).
- D12. Discounted loops become separate rows, with `captured_s` equal to the discounted seconds and a default of 0. Reviewers can undo a false positive and still stay inside Ari's cut-only rule. That is cleaner than the evidence design's D8, which needs QI to agree to a new kind of review action.
- D13. Public detector code with private threshold values.
- D9's conservative call that a `track` anchor counts at level C.
- D11. Age bands in the calibration strata.
- The coach skill aimed at the teen's own agent (4.5), with agent-written devlogs labeled `ai_assisted`.
- Hard Mode reviewers recruited from outside the build team, and a request that QI spot-check it at a higher rate (6.5).
- The generic hook mode with TOML mappings (4.2).
- The VSIX that bundles the binary, plus club build weeks, as the distribution plan (8.4).
- The funnel with weekly checks against server data, and the explanation of the six-week lag (8.1, 8.5, 8.6).

## Recommendation

### Base: the adoption design

1. **It is the only design built around the critical path.** Its milestones put the program path (M3: submissions, review page, handbook text, Hackatime-shaped reads, bundles) right after the server and ahead of breadth. That is the order HC design implication 21 recommends. The evidence design builds its ledger first and its self-improvement features last. The teen design builds the coach before submissions.
2. **Its program-facing contract is the hardest part to reinvent, and the cheapest for programs to adopt.** It has three tiers, a program kit, a generated `Alternate Tracking Method` text, D4's `handbook_s` computed from the same heartbeats Hackatime received, the Ari `journals[]` bridge, "Robert: change nothing", a mechanical counting rule and four ordered questions for QI. The other two designs have parts of this, specified in less detail.
3. **It protects the one thing every route depends on.** Teens' Hackatime keeps working. Risk 1 and WP 1.16 treat "pairtime broke my Hackatime" as the thing that ends adoption, and no other design tests the agent-down case against the real wakatime-cli binary.
4. **What it lacks can be grafted in.** Its low scores (R2, R3, R6, R8, R9) come from parts the other designs specify better, and each one fits into its milestones: calibration, the coach, the project-scoped reveal, the trust layer and the darwin toolchain.

**Why not the evidence design as base.** The best argument for it is that its ledger format (a header plus a nonce-protected body, 60-second Merkle batches and signed receipts; evidence 6.2) can't change once witnessed data exists without breaking old bundles, while the adoption design's program contract is endpoints and scripts that can be added at any time. Moving the evidence ledger into the adoption plan's M0, before any data exists, answers that argument (graft E1). What the base really decides is the build order and the cut list. The evidence design's cut list drops Hackatime-compatible reads, the cheapest integration any program has, and its plan reaches existing programs only after QI guidance lands, which it expects between mid-February and the end of April (evidence 8.4, 11.2).

**Why not the teen design as base.** It has the best requirements score and the best R3. But its order (coach in M4, submissions in M6) opens Hard Mode on 01-04 (8.5), after most of winter break, and its plan has no margin. Its R3 work matters more than its build order, so that work comes over as grafts T1 to T4. It should ship in the first alpha, not after the program path.

### Grafts

From the evidence design:

- **E1. The ledger format (6.2, D2).** A leaf header carrying `kind`, `project` and `session` for every event, a secret nonce in each body, Merkle batches sealed every 60 seconds, signed receipts, and keyed content-derived scorer inputs. Put it in the adoption plan's WP 0.3, in place of TP B6's plain construction, before any witnessed data is collected. The header lets the reveal stay project-scoped without letting a client hide a lock or suspend event under another project's label. The adoption design avoids that attack only by revealing every event in the window, other projects included (3.6). The teen design doesn't say how it avoids it (3.9).
- **E2. Commit-then-reveal with a committed challenge value `r` and a project-scoped reveal (6.5).** It replaces the adoption design's server-CSPRNG draw (WP 3.2), so an auditor can recompute the draw, and the server sees only headers, not bodies, for the teen's other projects.
- **E3. Consistency checks CC1 to CC11 (6.4) and held minutes (D7)**, added to the adoption design's 6.1. CC3 (`unexplained_lines`) and CC4 (three clocks per commit) are the checks Fraud Squad will use most.
- **E4. The corroborated, witnessed and backfilled bands, must-view rows and the program policy preset (6.7, D9).** The preset is the fastest route to QI's "good guidance", because QI can state its guidance as one rule, like the design's own example: "corroborated rows with no flags are pre-approved, witnessed rows need a look, backfilled rows are capped at the third" (6.7).
- **E5. D5 and D6.** Level-C time counts only inside forge-verified commit intervals, and teens who sync at school get the `offline` label.
- **E6. The bundle's SHA-256 in the justification, and the fixed first line "Tracked with pairtime <version>" (6.6, 8.2).** Make both part of the adoption design's generated text (8.2) and counting rule (8.4).
- **E7. The published audit formula with an hours term (6.9), and seeded known-answer cases for reviewers (6.10).** Recompute the constants once TP's fact-check finishes.
- **E8. Labeling mode for SM 7.2's pilot set (WP 4.10).** It replaces the adoption design's reviewer-cut calibration as the main source of labels.
- **E9. A Robert `kind: "pairtime"` proposal with a bundle fetch (8.2)**, sent in week one alongside the adoption design's "change nothing" bridge.
- **E10. Small, cheap wins.** Drop anchor ids from tier-1 buckets (D1). Add local-only mode (7.1), `pairtime backup` (risk 7), integer-only scoring so re-scores match across platforms (risk 9), and the asynchronous audit fallback in case live calls with minors aren't allowed (risk 6).

From the teen design:

- **T1. The coach (3.6, D5).** Session recaps with moments, the logbook, project stories, the welcome recap from lookback, and plain-language reason codes that name agent time after the agent. Ship a subset in the first alpha: the session recap, chess clock, welcome recap and build days. It goes ahead of the adoption design's badges and leaderboards, which stay first on the cut list.
- **T2. Build days, a weekly goal and freezes, in place of the adoption design's daily streak (D1).** Daily streak counters produce token days (PA 4.1), and the teen design is the only one that acts on that finding.
- **T3. The R3 proof metrics (section 2).** Four-week retention and the share of credited time on projects never submitted, both from tier-1 data. Report them to HQ with the R2 calibration report, because both are what "meets the above requirements" will be judged on.
- **T4. Devlog drafts, devlog windows as the submission unit (D7), and the story column on the review page (6.3).** Keep the adoption design's D5 work-session rows and its per-commit tab inside each devlog window. Programs already review per devlog (HC 3.3), and Ari already ingests `journals[]` (HC 3.6), so this is a unit teens and reviewers both already use.
- **T5. D12, restorable loop rows inside Ari's cut-only rule**, in place of evidence D8. It answers SM open question 5 without asking QI for a new kind of review action.
- **T6. D13, public detector code with private threshold values**, in place of the private module (adoption 3.2) or the sentinel (evidence 3.6). R1 then holds under the strictest reading.
- **T7. D9, a `track` anchor at level C**, in place of the adoption design's D3 at level B. Anyone can call `track` (HS 11.3), and level C caps those minutes at the handbook third, the same ceiling the handbook already puts on that harness's `ai coding` time.
- **T8. D11's age bands in the calibration strata, and the coach skill (4.5)**, with agent-written devlogs labeled `ai_assisted`.
- **T9. Hard Mode's governance (6.5).** Reviewers come from outside the build team, and QI is asked to spot-check at a higher rate. Make both launch conditions for the native program.
- **T10. The funnel with weekly checks (8.1, 8.6), and the six-week project lag (8.5).** Re-base the adoption design's install targets on the teen design's 15% install-to-submit rate. At that rate, the adoption design's 650 submitting teens need about 4,300 installs, not 1,300 to 2,000. That makes R3 retention and the VSIX channel (teen 4.3, evidence WP 5.4) part of the adoption plan, not polish.
- **T11. The generic hook mode with TOML mappings (4.2), and Go 1.26 for darwin until February 2027 (3.10, also evidence 3.9).**

### What the final design must fix, whatever the base

1. **Make attribution unambiguous.** On every row counted toward the bounty, pairtime's credited figure must be the program's hours number, not supporting evidence on top of Hackatime hours. The adoption design's route B pitch (8.3) currently says the opposite. Get QI's counting rule in writing in week one: the fixed first line, the review URL and the bundle hash in `Override Hours Spent Justification`.
2. **Calibrate before any program decides on pairtime numbers.** Run SM 7.5's labeled pilot over winter break, with two raters labeling against a timelapse and κ ≥ 0.6. Freeze scoring 1.0.0, and publish the R2 report against SM 7.3's four baselines. Building and submitting can start earlier, but decisions wait for the freeze. Reviewer cuts can add labels, but they can't replace the labeled set, because they're anchored on pairtime's own number and can only go down.
3. **Evidence levels must mean exactly what QI is told they mean.** Level A requires an OS keyboard signal. Teen D10's editor change-shape gets its own label, for example `editor_typing`, and never sets `input_keys`. Minutes anchored by `track` carry `via_track`. Publish one definition of levels A, B and C and of the band rules, and use it in the first QI conversation.
4. **Make the Hackatime drop-in real, and never cut it.** Accept `filter_by_project` both as an array and as a comma string. Answer or deliberately refuse every call Stardance's `HackatimeService` makes: `authenticated/api_keys` should return a scoped, read-only token (HC design implication 9), and `admin/v1/user/get_user_by_email` and `my/heartbeats` need an answer too. Contract-test against that client's real request shapes, not only Hackatime's controllers (HC 1.8). Move Hackatime-compatible reads to the never-cut list.
5. **Never break a teen's Hackatime.** The `e2e-wakatime` job against the real wakatime-cli binary, including the agent-down and offline-replay cases, blocks every release (adoption WP 1.16, risk 1).
6. **Make the native program a week-one ask, with a fallback.** Send QI and an HQ sponsor a written proposal covering the budget, outside reviewers and a higher spot-check rate. Keep a smaller first season, or an opt-in track inside an existing program, ready in case it isn't funded. Plan for at least 1,200 weighted projects, and reconcile weekly against HQ's count (HC open question 13).
7. **Settle level B early.** Put SM open question 1 in the first QI conversation. Apply for Azure Artifact Signing under Hack Club in week one, because the opt-in typing module, SmartScreen reputation and Smart App Control all depend on a signed binary (CP 4.3).
8. **Give Fraud Squad a view of pairtime-only users.** Until Robert has a `pairtime` kind, users without Hackatime are invisible to Telescreen (adoption risk 8). Tell programs so, require Hackatime in the native program's pilot as the adoption design does, and send the `kind` proposal in week one.
9. **Honor Karim's direction exactly, and ask before changing it.** Keep the AGENTS.md entry as Karim described it, the user's prompt with PII stripped and passed to `track`, which adoption 4.4 and teen 4.4 already do. The evidence design's model-written summary, and the decision all three share to install the entry only on harnesses without hooks, are proposals for Karim, not settled design. All three honor the AGPL repo, human time rather than agent time, and self-hosting on Karim's domain.
10. **Ship R5's literal default.** Profiles are public by default and aggregate-only, with an equal-weight "Make private". The `ask` fallback (evidence 7.3, teen risk 4) would not meet R5's words, so it switches on only by HQ's written decision, recorded with the Children's Code assessment (TP B7).
11. **Keep darwin on Go 1.26 until February 2027** (CP 1.5). The adoption base currently drops Monterey Macs.
12. **Re-check every TP-derived number when TP's fact-check lands.** That covers audit-rate inputs, retention periods and consent rules. All three designs carry TP's illustrative inputs as constants.

### The race with grit

grit already has the core thesis, a Go server, SSE and a review queue (PA 1.14). By PA 1.14's own assessment, it lacks calibration against reviewer judgments, a motivation layer, broad harness coverage and program adoption. The merged design beats grit on all four only if it ships the program path by mid-December, the coach in the first alpha, and a published calibration report before the first decisions in January. If any of those slips into February, plan as if grit will reach a program first. Both projects are AGPL, and the teen design suggests staying open to collaboration (teen risk 6). That is a strategic choice for Karim, not something a design should decide.
