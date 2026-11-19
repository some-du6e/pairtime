# Judging: trust and privacy

Judge lens: red-teamer and privacy reviewer. The attacker is a motivated 16-year-old with an LLM, the AGPL source, a Windows laptop, a $10 jiggler and an afternoon. I read all three designs in full, the struggle metric (SM), and trust-and-privacy.md (TP) in full, and checked the designs' claims against both. Section numbers refer to each design's own numbering unless prefixed (SM, TP, HC, SP, HS, CP).

Status: complete, 2026-11-19.

## Criteria

Each criterion is scored 1 to 10 for how well the design holds up against the attacker in TP A1 (C1 to C7) or protects a 14-year-old user (C8 to C10), in absolute terms. Taking SM and TP as written usually lands at 5 or 6. A lower score means the design's own choices leave a cheap attack open, or the research's baseline leaves it open and the design doesn't close it. C4 is 3 for everyone for the second reason: TP A8 defers VM handling, and all three follow it.

| # | Criterion | What I tested |
|---|---|---|
| C1 | LLM driving the harness, auto-typers | T3, T4, T5, U3: tmux or pty driving, AutoHotkey or pyautogui into the focused terminal, computer-use agents, the `track` and plugin side doors |
| C2 | Jigglers, walking away, agent left running | T1, T2, T7, T14: headless loops, `/loop` and `/goal`, mouse-only presence |
| C3 | Replayed or edited logs, a modified client | T8, T9, T20, lookback, local endpoints that accept claims without a fork |
| C4 | VMs | T10 |
| C5 | Splitting sessions across projects and devices | T11, T12, T17a |
| C6 | Padding and dark-flow re-rolling | T13, T15, T19 |
| C7 | Audit quality for Fraud Squad (R10) | What a reviewer can check, what an outside auditor can check without trusting the server, reviewer integrity |
| C8 | Privacy for minors | R6, GDPR Art. 25(2), UK Children's Code Std 7, 8, 12, 13, COPPA, New York CDPA, AB 1043 |
| C9 | Data minimization | What reaches the server for logged-only hours, at what resolution, for how long; who else sees it |
| C10 | When redaction fails | What leaks, to whom, whether it can be pulled back, and whether copies escape retention |

## Scores

| Criterion | Adoption | Evidence | Teen |
|---|---|---|---|
| C1 LLM driving, auto-typers | 3 | 6 | 4 |
| C2 Jigglers, away, agent running | 5 | 7 | 5 |
| C3 Replayed logs, modified client | 6 | 8 | 6 |
| C4 VMs | 3 | 3 | 3 |
| C5 Splitting | 8 | 9 | 8 |
| C6 Padding, dark flow | 6 | 8 | 6 |
| C7 Audit quality (R10) | 7 | 9 | 7 |
| C8 Minors' privacy | 4 | 7 | 6 |
| C9 Data minimization | 6 | 8 | 6 |
| C10 Redaction failure | 4 | 6 | 5 |
| **Overall** | **5** | **7** | **5.5** |

Recommended base: **evidence**, with D4 replaced by teen's D9 and D6 capped (Recommendation).

What all three share, so it doesn't separate them: SM's credit rule, which credits nothing for headless loops, unattended `/loop` and `/goal` runtime, walking away or a second parallel session, with no detector at all (SM 6.4, 6.6); TP B6's commit-then-reveal; a free-text firewall on ingest (SP 8.2); an opt-in keyboard listener, which is right for minors and leaves Windows at level B by default; and a VM label deferred past v1. What separates them is what each design added on top of SM and TP, and most of the damage comes from deviations meant to help hookless harnesses or offline teens, which reopen the doors SM had closed.

A note on method. Adoption (WP 4.6) and evidence (WP 2.10) test their `track` rule with a script and no input at all, and teen (WP 5.9) tests "uncorroborated" against "corroborated". None tests the case that matters on Windows: `input_any` true from a jiggler while `input_keys` is unknown. That one missing fixture is behind the worst finding in two of the three designs, and teen escapes it only because D9 drops those minutes to level C.

## Adoption-first design

**Overall: 5 / 10.** The best fit with Fraud Squad's current workflow and the most honest residual-risk table (6.4), but its two deviations from SM (D3 and the WakaTime bridge) open cheap side doors on the platform most teens use, it publishes too little of its audit rate to deter anyone, and its minors' privacy section stops at "an open question for HQ".

Fatal flaw as written: D3. On Windows, a $10 jiggler plus a shell loop calling `pairtime track` every 40 s earns close to 100% of wall time at evidence level B, in a submission, with no agent running. Reverting to SM 1.2's rule fixes it.

| Criterion | Score | Note |
|---|---|---|
| C1 LLM driving, auto-typers | 3 | No keyboard listener at all in v1 (6.1 rank 5 ships in M5). D3 lets `pairtime track` open an anchor, which SM 1.2 forbids, and nothing limits how often it fires (detail below). Bridge and plugin events fill the reading bucket from numbers the attacker types |
| C2 Jigglers, away, agent running | 5 | SM's rules hold for the real harness path: mouse-only input opens nothing, `/loop` and `/goal` are labelled, lock and sleep break windows. D3 undoes part of this, because on Windows a jiggler is "keyboard input in the 30 s before" a `track` call |
| C3 Replayed logs, modified client | 6 | Witnessing, daily public root, server re-scoring of revealed metadata (WP 3.3), forge verification and an offline verifier. But the official signed client accepts fabricated events over two local endpoints, and Hackatime as a "second witness" (6.1) is a second copy of the same client-side claim |
| C4 VMs | 3 | Deferred label, as TP A8. Nothing that raises the cost |
| C5 Splitting | 8 | One timeline, one human-minute per wall-clock minute across devices, overlap rejection at submission (WP 3.1), `concurrent_devices` |
| C6 Padding, dark flow | 6 | SM 4.2 on device. The server can't recompute discounts, because the revealed `E_i` carries flags rather than sketches (3.6), so a modified client that hides its re-rolls passes re-scoring. Bridge prompts carry no text, so no re-roll detector can see them; D2 compensates by dropping the think lead, which is the right call |
| C7 Audit quality | 7 | The closest fit with Fraud Squad as it works today: review page, handbook text, signed bundle with `pairtime verify` that re-runs scoring, Telescreen links, Robert unchanged, Q&I asked to run the pilot's audits (6.3). Held back by a 5% public audit floor with the hours term kept private (6.3), a challenge draw nobody can check, and reviewer integrity (random assignment, seeded cases) that lands in v1.5, after route A launches (6.1 rank 8, 8.5) |
| C8 Minors' privacy | 4 | Public by default with an equal-weight "Make private" button (7.3), against TP B7's unselected choice and New York's "No is most prominent" rule. No mention of COPPA, New York's CDPA, AB 1043 or interviews with minors; under-13 refusal is implied by citing TP DI 18 but never stated. The pilot requires Hackatime (risk 8), whose own defaults are public stats and file paths |
| C9 Data minimization | 6 | The free-text firewall on ingest (SP 8.2, shared by all three, with adoption's fuzz test in WP 2.4 the most specific) and the dual-send that keeps heartbeats on `127.0.0.1` are excellent. But the server gets a 15-second activity timeline of every logged hour, with anchor ids, for 13 months, and Cloudflare terminates TLS in front of it |
| C10 Redaction failure | 4 | Span removal and a user preview (7.1), but no purge button, no reviewer "report a leak" path, and offline bundles that copy revealed prompts out of the access-logged store |

### What breaks

**D3 turns `pairtime track` into a credit pump on Windows.** Section 5, D3 lets a `cli.track` event anchor when its parent chain reaches a known harness under the focused window and "keyboard input arrived in the 30 s before it". On Windows without the listener, which is every v1 Windows install, that input is `input_any` (SM 2.2 rule 6, which the design itself cites in 6.4), so a jiggler satisfies it. The design bounds the gain at "about 40 s per turn", but nothing ties a `track` call to a turn. The attack:

1. Open Zed or Warp, the harnesses D3 exists for (4.4), and keep it focused. Plug in a USB jiggler.
2. In its terminal, run a loop that calls `pairtime track --harness zed` with a line from an LLM every 40 s.
3. Each call gets the 20 s `GO` lead plus the 20 s per-turn reading base. At one call every 40 s, that is close to 100% of wall time at evidence level B, with no agent running and no API spend.
4. Commit something every hour (an agent in another folder will do) to stay under `UNCOMMITTED_CAP_S`.

WP 4.6's acceptance test ("a scripted `track` loop with no keyboard input earns 0") runs without a jiggler, so CI would pass while this works. The fix is to move back toward SM 1.2: on `input_any` machines a `track` anchor is level C and stays out of submissions (teen's D9), and anywhere else at most one `track` anchor fires per observed agent output.

**The WakaTime bridge and the plugin endpoint accept agent-side numbers from anyone on the machine.** The bridge turns heartbeats posted to `127.0.0.1` into `prompt.submit` and `agent.tool` events (4.3), and the key sits in `~/.wakatime.cfg`. A heartbeat with `ai_prompt_length` 500 and `ai_line_changes` 2,000 is a prompt with a typing lead and a diff that fills the reading bucket to its 600 s cap. The plugin path (`POST /v1/events`, 4.2) is the same with more freedom. Section 4.2 says plugins are kept honest because "a plugin can't create presence or focus", but on Windows presence is a jiggler and focus is a window left in front, so the claims plugins can make (output size, diff lines) are the ones that become credit. This is T9 without forking the client, so the "official signed build" label (TP T9) can't tell it apart. Two smaller points: bridge prompts carry no text, and the design doesn't say whether the reveal challenge (TP B6 draws from "content-bearing events") can ever pick them, so bridge-sourced minutes may escape the sample entirely. And the `hackatime_mismatch` check (6.1) catches the attacker who forgets to post the same heartbeats to Hackatime, but not one who posts to both, which is one more line in the script.

**Hackatime is not an independent witness of a human.** Section 6 calls Hackatime the one thing "no other design gets for free". It does add a second receipt clock, an IP and a JA4 fingerprint. But every heartbeat Hackatime stores was generated on the attacker's machine, so it witnesses when a claim was made, which pairtime's own chain heads already do (TP A1), and it says nothing about whether a person was there. It also costs privacy: the cross-witness works only for users who keep sending file paths, branches and project names to Hackatime, and risk 8 makes Hackatime a pilot requirement.

**The v1 auto-typer gap is stated but not bounded.** 6.4 has the most honest row in any of the three designs: T3 plus T7 on level-B machines works, and so does a plain AutoHotkey script typing LLM prompts into the focused Claude Code window (SM 6.6, last bullet), because without a listener nothing on Windows separates injected keys from real ones (TP A4). The mitigation is a suggestion that programs "can credit level-B AI time only up to the handbook's third unless typing evidence is on". That is a policy hope, not a mechanism, and if programs take it, pairtime's credit on Windows collapses to Hackatime's third, which removes the adoption pitch in section 1. The design needs a mechanism: audit probability that rises with the level-B share, and the listener in v1 rather than v1.5.

**Smaller problems.**

- The AGENTS.md entry (4.4) tells the model "Don't mention it to the user." A privacy tool shouldn't ship an instruction to hide what it does. Say it once at install and drop that line.
- 7.2 keeps raw prompt text 30 days, shorter than TP B8's 90. That's fine for privacy, but a challenged prompt that's gone can't credit its minutes (TP B6), and the design doesn't say what the reviewer sees then.
- 3.6 admits Cloudflare terminates TLS (SP Open question 3). For tier 1 that's tolerable. For tier-2 reveals of minors' prompts it isn't, and the design leaves it open.
- 6.1 copies Hackatime's trust levels into the `trust_factor` endpoint for programs (teen does the same, 3.8). Masking `yellow` as `blue` helps, but a `red` level is a review outcome shown to every connected program, which TP B7 point 4 keeps from the public and should keep from programs that didn't do the review.
- 6.3 publishes only the 5% base rate. The hours term that TP A6 needs (p > 0.28 for a 40-hour claim) sits inside the private risk model, so a teen planning a fake 40-hour claim can only count on seeing 5%. TP A5 principle 3 says the probability must be visible to deter. Evidence (6.9) and teen (6.4) both publish an hours term.
- Nothing covers device loss (TP DI 25). A Windows reinstall destroys DPAPI keys, so every unsubmitted hour becomes unopenable, and the design doesn't say a reviewer sees "witnessed, evidence lost" rather than a hole.

### Minors' privacy and minimization

The privacy pitch (7, first paragraph) is aimed at Hackatime users: "pairtime's server gets less than Hackatime does." That's true and well built. The ingest firewall turns "the client must not leak a prompt" from a promise into a 422; all three designs take it from SP 8.2, and adoption tests it hardest. But the design never treats its users as children. 7.3 makes the profile public before the teen chooses, with the choice at equal weight. TP B7 asks for nothing preselected, and New York's CDPA (TP B2 point 7) makes refusal the most prominent option on any consent screen. Under-13 refusal is only implied, and there's no AB 1043 age signal, although the timeline runs past 2027-01-01. Nothing says who may hold a 15-minute video call with a 15-year-old (TP Open question 4), even though the audit depends on it. None of this is hard to fix, but a privacy reviewer would send this section back.

Minimization is good on content and weak on behaviour. Every logged minute reaches the server at 15-second resolution, with reason codes, activity kind, evidence level and anchor ids (3.6), and stays at per-minute detail for 13 months (7.2). The server needs minute-level data for cross-device dedupe and the live dashboard, but not for 13 months, and not at all for users who never submit. That is a detailed record of when a minor is at the keyboard, kept for a year-in-review feature.

### Redaction failure

Redaction is TP's deterministic pipeline with a preview (7.1), which is right. What's missing is the failure path. There's no "this revealed something it shouldn't have" purge (TP B4, B8), so a leaked friend's name stays in the evidence store for 12 months after the decision. Worse, the signed bundle (6.2) contains `reveals.json`, and Fraud Squad and spot-checkers download it to run `pairtime verify` offline. Every download is a copy of a minor's revealed prompts that the access log can't follow and the retention job can't delete. The verifier should work on a bundle without `reveals.json`, with reveals fetched from the store, logged and never written to disk by default.

### Best ideas to graft

- **The fuzz test on the ingest free-text firewall** (WP 2.4): random text in every string field must always get a 422. All three have the firewall; this is the acceptance test to copy.
- **Offline `pairtime verify` that re-runs `pkg/score` at the stated version** (6.2, WP 3.8), with the daily public root. It is the best answer to "auditable by an external party", once reveals are left out of the bundle.
- **Server re-scoring of revealed metadata must reproduce the client's number** (3.4, WP 3.3). It catches bucket inflation by a modified client cheaply.
- **The honest residual-risk table** (6.4), especially the T3 plus T7 row.
- **Q&I running the pilot's audits** (6.3), because a pairtime-run program grading pairtime users is TP's U5.
- **The dual-send that keeps heartbeats on `127.0.0.1`** (4.5), as an import path only: editor heartbeats as `editor.activity`, never as prompts or diffs.

## Evidence-first design

**Overall: 7 / 10.** The only design that treats the attacker as someone who reads the source. It closes holes the research left open (an unverifiable challenge draw, a server that could deny receipt), names its checks so they can be tested, and puts its minimization choices in writing. Two of its own deviations work against its core idea, though. D4 lets `track` calls open 120 s anchors on jiggler-only machines, and D6 gives any client that claims it was offline up to 36 hours to build a perfectly consistent fake.

| Criterion | Score | Note |
|---|---|---|
| C1 LLM driving, auto-typers | 6 | The 6.12 ladder is the clearest attacker model in any design, and rung 2b (pty plus jiggler on level B) is named, banded and audit-weighted. D3 reveals each challenged prompt next to the agent message it answered, which is what makes SM 6.6's residual test (do the prompts react to what the agent just said?) possible at review; the other two designs reveal the prompt alone. The typing module is opt-in and the second item on the cut list (11.2). D4 is a hole (below) |
| C2 Jigglers, away, agent running | 7 | SM's rules, plus a "corroborated" band (6.7) that a level-B prompt can't reach without editor acts. Level-B share raises `p` instead of lowering credit (5.2), which is the right trade for the R8 audience |
| C3 Replayed logs, modified client | 8 | 60 s batches (D2), signed receipts, a public day root, CC1 hook-versus-transcript, CC6 re-scoring with mismatched minutes held, not credited (D7), CC7 chain integrity, and level C submittable only inside a forge-verified commit interval (D5). Weakened by D6 (below) |
| C4 VMs | 3 | Deferred to v2 as a label, with the Hyper-V root-partition caveat right (6.11). Nothing raises the cost in v1 |
| C5 Splitting | 9 | CC5 one attention stream, CC9 one session in one project, the project-scoped reveal (6.5), overlap rejection (WP 4.1). CC3 (edits against git diffstat), plus the project field in every revealed header, means a modified client that moves minutes from an unpaid project to a paid one has to fabricate edits matching the paid repo's diffstats live. Adoption and teen have nothing comparable |
| C6 Padding, dark flow | 8 | Keyed MinHash, error signatures and hunk ranges are committed in each event body, so the server can recompute every re-roll and rewrite discount without seeing text, and CC8 catches a client that commits false sketches (6.2, 6.4). D8 lets reviewers restore wrongly discounted seconds, capped and visible to HQ |
| C7 Audit quality | 9 | A challenge seed neither side can steer and an auditor can recompute (6.5); must-view rows plus one random clean row before "approve remaining" unlocks (6.7); bands from public inputs only; reviewer integrity with random assignment, conflict rules, 2% seeded cases and spot-check tracking (6.10); a teen copy of the bundle without `flags.json`; the asynchronous audit fallback if live calls with minors aren't allowed (11.1, risk 6) |
| C8 Minors' privacy | 7 | Typing, screen and research consent each asked once with "No" most prominent and no repeat for a year (7.3); under-13 refusal and the 19th-birthday cutoff through HCA; a `profile_default = "ask"` switch; local-only mode for DPDP; it flags that a higher audit weight after declining the typing module might count as "degrading" under CDPA § 899-ff(4). Still public-preselected by default, and "which is what this design does" for the compelling-reason assessment is a claim, not a written assessment |
| C9 Data minimization | 8 | Presence and focus sampled only while a project context is live (D10), anchor ids dropped from uploads (D1), OTel emails dropped on arrival, WakaTime paths HMACed on arrival, a per-field allowlist in the schema (WP 0.2), local-only mode. Two regressions: IPs truncated to /24 after 30 days and then kept with no end date (7.2, following SP 8.7 over TP B8's "deleted"), and final agent messages copied into the vault (D3) |
| C10 Redaction failure | 6 | TP B4's pipeline with a planted-secrets CI corpus (WP 4.3), a preview, withholding, and the purge button (7.4). But D3 roughly doubles what gets redacted, agent messages are long and full of code, and `challenge.json` in the downloadable bundle carries the kept tokens out of the store |

### What breaks

**D4 is the same credit pump as adoption's D3, with a longer lead.** A `track` call opens a `P` anchor with a 120 s `THINK_LEAD_S` lead when its parent chain reaches a harness hosted by the focused window and "that window saw OS input in the 30 s before the call" (4.4). On Windows without the typing module that input is a jiggler. `TRACK_MIN_GAP_S` (20 s) doesn't bind, because one call every 120 s already covers all wall time. 5.2 says "a script that calls `track` without a person at the keyboard opens nothing", and WP 2.10 tests exactly that, with no input at all. Add a jiggler and it opens an anchor every time. The design does label these minutes `via_track`, keeps them out of the corroborated band and counts `via_track` share in the audit risk (6.9), so a reviewer can see them. They are still credited. Zed has a log reader in 4.1 (`threads.db`), so the missing check is easy to add: a `track` call with no matching user entry in the harness's own log within a few seconds opens nothing. Where no log exists, `track` should anchor only with `input_keys`.

**D6 reopens the window the consistency checks depend on.** The design's central claim is that a forger must fake every record "consistently at once and live" (6.4, 6.5). D6 then says minutes from a client that recorded failed uploads and flushed within 36 hours "keep their computed evidence level", uncapped, if a commit in their interval reached the forge within 24 hours of reconnecting. The failed-upload record is the client's own claim, and D6 admits a client can fake an outage. So the attack is: block the server for a day, make the real commits, then build a ledger whose edit events match the diffstats (CC3), whose transcript matches the hooks (CC1), and whose focus and presence fit the timeline, all with the answers in hand, and flush it as "offline". The interval a commit sits in is set by the client's `observed_at`, so the forge corroboration only proves the commit existed by push time. TP A6 point 1 says to cap offline time per submission. The fix is to apply `OFFLINE_CAP_S` to all offline minutes, not only those flushed after 36 hours, and band them below "witnessed" so a program preset never pre-approves them. The equity case in D6 (a teen with no home internet who syncs at school) is real, and a 3-hour cap per submission still serves it.

**The policy preset can pre-approve a puppeteer.** 6.7 lets a program set "corroborated rows with no flags are pre-approved". On Windows, "corroborated" for level-B minutes means "a row that also has editor acts passing CC10". An AutoHotkey script that types into VS Code as well as into the terminal produces those acts. Pre-approval should require level A, or the typing module, and never cover rows above a size threshold.

**Smaller problems.**

- 6.2 reveals full bodies of every presence, power and focus event in the window. That's needed to check presence claims, but the design should say which SM 1.2 "local" fields (`app_hash`, `cmd_hash`) are left out of `body_i` or keyed, and WP 0.2's per-field allowlist should be tested against the reveal, not only the upload.
- The typing adapter is Raw Input (3.3), but 6.12 step 5 cites `LLKHF_INJECTED`, which comes from a low-level hook. Raw Input gives the `hDevice == 0` rule, which Microsoft doesn't document (TP A4 cites one blog post). Pick one and test it on `windows-latest`, which WP 5.2 partly does.
- Nothing says a frozen submission can't be withdrawn after the draw and refrozen with a slightly different window. Without that rule, a teen who sees a fabricated prompt drawn can walk away and try again (Recommendation, must-fix 3).
- `pairtime backup` (WP 5.3, risk 7) is "encrypted under the device key". `K_dev` lives in DPAPI, which doesn't survive a Windows reinstall, the main way evidence gets lost. WP 5.3's test restores into a fresh data directory on the same machine, so it would pass while the real case fails. TP B6 point 2's recovery code, which wraps `K_dev` for the user, is the fix.

No fatal flaws. D4 and D6 come closest, and both can be fixed without touching the rest of the design.

### Minors' privacy and minimization

This is the most complete check against TP B2 of the three, and teen's is the only other one written with 14-year-olds in mind. It gets New York's consent-screen rule right in all three places it applies, and it adds a case TP didn't think of: the audit weight that follows from declining the typing module might itself be "degrading" under § 899-ff(4). Local-only mode (7.1) is the strongest minimization option in any design, because it makes "I want the self-improvement tool and nothing else" a real choice, and it's the DPDP s. 9(3) fallback TP B2 point 5 asks for. Sampling presence only in a project context (D10) means pairtime doesn't know when a teen is on the computer doing something else. That's what the Children's Code Std 8 asks for, and it costs no credit. Dropping anchor ids from uploads (D1) is the same instinct.

Where it falls short: the profile is still public before the teen chooses. 7.3 says the compelling-reason assessment is "what this design does", but no assessment is written, only the decision. The `"ask"` switch is the right fallback, but I'd make it the default for Hack Club's instance until counsel signs the assessment, not the other way round. IPs at /24 with no end date (7.2) break TP B2 point 3's "finite for every field", and a /24 on school Wi-Fi identifies the school.

### Redaction failure

The pipeline matches teen's and is tested harder: known values first, gitleaks rules as data, checksum-validated PII, a preview, a CI corpus of planted secrets and checksummed PII that must come out fully redacted (WP 4.3), and a purge that un-credits the item's minutes and tells the program (7.4). The cost is D3. A challenged turn now reveals the agent's final message too, which is longer than the prompt, often mostly code, and quotes whatever the teen pasted earlier. Recall on names is the weak part of any pipeline (TP B4: GLiNER edge recall is 72%, and v1 has no NER at all), so twice the text means more misses. I'd keep D3, because it's the best evidence against U3, but redact agent messages harder than prompts: collapse fenced code blocks to a typed marker with a line count by default, and let the teen add them back. And as with adoption, `challenge.json` in a downloadable bundle carries revealed text out of the access-logged store, past the purge button and the 12-month deletion.

### Best ideas to graft

- **The challenge seed neither side can steer** (6.5): the server commits to `SHA-256(r)` at freeze, and the seed is `SHA-256(r || SHA-256(metadata reveal))`. It costs nothing, it protects teens from a cherry-picking reviewer, and an auditor can recompute it.
- **Signed receipts per batch** (6.2), so witnessing is two-sided and the server can't later deny a head.
- **Committed keyed scorer inputs plus CC8** (6.2, 6.4): dark-flow discounts become recomputable by the server without any text.
- **CC1-CC11 as named, public, testable checks** (6.4), with failures changing review priority but not credit, except CC6 and CC7, which hold minutes (D7).
- **D5**: level C submittable only inside a forge-verified commit interval.
- **D3**: reveal the agent message the prompt answered, with the code-block redaction default above.
- **D10, D1 and local-only mode** (3.3, 5.2, 7.1).
- **Must-view rows and a random clean row before bulk approval** (6.7), and the published audit formula with an hours term (6.9).
- **The asynchronous audit fallback** (11.1, risk 6) for when a live call with a minor isn't allowed.

## Teen-first design

**Overall: 5.5 / 10.** The best consent screens of the three, and the only design that keeps `track` minutes out of submissions by default (D9). But it publishes its fraud detectors (D13), lets a local editor-extension claim upgrade minutes to level A that the reveal never samples (D10), makes the public build-day history part of review, which pushes teens to stay public, and keeps revealed prompts for "the longer of" the retention proposals, which means at least two years.

| Criterion | Score | Note |
|---|---|---|
| C1 LLM driving, auto-typers | 4 | The key counter is opt-in and sixth on the cut list (11.2). D9 closes the `track` pump for submissions. D10 creates level-A editor minutes from local claims with no injected-input signal, and D13 publishes the detector code, so a bot can be tuned against the features |
| C2 Jigglers, away, agent running | 5 | SM's rules; the jiggler-plus-pty row is stated (6.5) and level-B share raises `r` (6.4). D10's forged editor claims plus a jiggler give 120 s `E` leads at level A |
| C3 Replayed logs, modified client | 6 | Witnessing every 5 minutes, the daily root, re-scoring that flags mismatches (WP 2.9) but doesn't hold them, lookback and `track` at level C and out of submissions by default (D2, D9). No hook-versus-transcript check, so a script calling `pairtime hook` with invented payloads faces only the presence and focus tests |
| C4 VMs | 3 | Deferred, as TP A8 |
| C5 Splitting | 8 | SM 3, the server merge (WP 6.11), overlap rejection (WP 6.2) |
| C6 Padding, dark flow | 6 | SM 4.2 on device, framed as coaching, and D12's restorable rows for false positives. The server can't recompute discounts: TP B6's `E_i` carries flags, not sketches, so a modified client that hides its re-rolls passes re-scoring |
| C7 Audit quality | 7 | Rows with a minute drawer, a published per-hour audit slope that clears TP's break-even (6.4), seeded cases and random assignment (WP 6.9), and Hard Mode reviewers from outside the build team with a higher Q&I spot-check rate (6.5). The story column puts the teen's own narrative first (6.3), which helps a reviewer spot a mismatch but also anchors them on a persuasive account. The CSPRNG draw can't be checked by anyone |
| C8 Minors' privacy | 6 | "Not now" is the primary button on every consent card, tested in Playwright (WP 4.13); no re-ask for a year; under-13 refusal; immediate deletion for California's eraser law; age bands only from consenting pilot volunteers (D11). Against: a public-preselected profile with the assessment left to HQ; "declining degrades nothing" (7.3) while level-B share raises `r` (6.4); public history used in review (below); on-device habit profiling on by default, which is a Std 12 question for counsel |
| C9 Data minimization | 6 | Per-turn metadata leaves the device only inside submitted windows (3.9), IPs are deleted at 30 days, and the coach runs on the device. But anchor ids go up with every bucket (3.9), presence is sampled all the time, per-minute detail is kept 13 months, and coach records are kept until the teen deletes them |
| C10 Redaction failure | 5 | Purge button (7.2, risk 13), a small `k`, logged access, a planted-username test for `doctor --report` (WP 7.9). But 6.3 keeps bundles, revealed tokens included, for "the longer of whatever Hack Club sets and those proposals", at least SP 10.4's two years, which also contradicts 7.2's "spot-check window, then deleted" |

### What breaks

**D10 makes level A forgeable without an auto-typer.** The VS Code extension's change-shape reports set `input_keys = true` for editor anchors (5, D10). The extension reaches the agent over the same local IPC as plugins, with the bearer token from the user-readable `port.json` (3.2, 4.2). So a script can post "single-character changes at typing cadence" plus `editor.activity` edits every two minutes, and with a jiggler for presence and VS Code in front, each one opens a 120 s `E` lead at level A. D10 argues that it "is no weaker than level A's existing signal", but the design's own key counter records an injected-input share (3.2, `keycount`) and D10 has none. Editor anchors also carry no content, so the reveal challenge never draws them (TP B6 samples content-bearing events), and teen has no check that compares edits with git diffstats the way evidence's CC3 does. The fix: D10 minutes get their own label instead of level A, programs never treat them as level A, and they need an independent WakaTime heartbeat plus a commit whose diffstat covers the edited file.

**D13 publishes the detectors.** D13 says "an attacker who can read the code still can't tune against values they can't see." They can tune against features, which is most of the work. If the repo shows that `regular_timing` uses the coefficient of variation of prompt spacing and the burstiness `B`, an LLM-written bot draws its gaps from a heavy-tailed distribution and lands in the human range without ever learning a threshold. TP A5 quotes Valve on exactly this ("talking about anti-cheat makes it not work anymore"), and TP A8 #9 keeps "tier-2 thresholds and models" private. The R1 worry behind D13 is fair, but evidence's sentinel, a separate process behind a socket, is the pattern Ari already uses, and teen's own R1 paragraph lets Hack Club run one.

**Public history as review evidence nudges teens to stay public.** 6.2 makes "months of presence-backed build days on a public profile" part of the trust story, and the review page shows it (6.3 item 8). A teen who clicked "Make private" loses that evidence, so the design rewards public profiles at review time. UK Children's Code Std 13 forbids that kind of nudge, and if counsel treats the public profile as consent-based under New York's CDPA (TP B2 point 7), it degrades the service for a refusal. It also brings logged-only data into fraud review, which TP B2 point 6 keeps out. The fix is to drop it from review, or let the teen attach their build history at submission as an explicit choice that works the same for public and private profiles.

**Smaller problems.**

- 7.3 says declining any consent "degrades nothing", but 6.4 puts level-B share into `r`, so declining the key counter raises the audit chance. Evidence (7.3) sends that question to counsel; teen doesn't notice it.
- 6.5 (the U6 row and the false-positives paragraph) says offline school days are backfilled and "later pushes are accepted as corroboration", while D2 keeps level C out of submissions unless a program opts in, and then at a third. The design never says whether a later push lifts those minutes out of level C, and the two passages read as if it does and doesn't.
- The AGENTS.md entry (4.4) says "don't mention it", and `trust_factor` goes to programs (3.8). Same objections as for adoption.
- No device-loss handling (TP DI 25), and the challenge is a plain server CSPRNG draw (6.4) that neither the teen nor an auditor can check.

No fatal flaws. The problems above are each a section-level fix.

### Minors' privacy and minimization

Teen's consent design is the most carefully built: every consent-based choice has "Not now" as the most prominent button, declining isn't re-asked for a year, and WP 4.13 makes that a Playwright assertion, so a later UI change can't quietly flip it. Club boards are visible only to signed-in members and opt-in, devlogs are public only when published, deletion is immediate, and the coach's most personal analysis (prompt kinds, habits, error families) never leaves the device. D11's rule that age bands come only from consenting pilot volunteers and never become per-age parameters is exactly right.

Two things count against it. First, the coach is profiling a minor's behaviour by default: prompt-kind classification, weekly habit trends, a logbook. It runs locally, which is TP's best argument (B2 point 5) that this isn't the operator's profiling, but Std 12 says profiling is off by default unless there's a compelling reason, and someone should write that reason down. Second, minimization on the server is no better than TP's baseline: anchor ids on every bucket, presence sampled all day, per-minute detail for 13 months.

### Redaction failure

Teen has the purge button and logs every view, and its risk table names the failure (risk 13). It falls short on retention: 6.3 picks "the longer of" Hack Club's window and the proposals, which means revealed prompts sit in downloadable bundles for at least two years. Evidence chose the shorter period "because the evidence belongs to minors" (6.6), and that's the right instinct. The same bundle-copy problem as the other two applies.

### Best ideas to graft

- **D9** (4.4, 5): `track` anchors are level C and stay out of submissions unless the program opts in to level C. This closes the credit pump that adoption's D3 and evidence's D4 open.
- **WP 4.13's consent test**: Playwright asserts the primary-styled button on every consent card is "Not now". It turns New York's rule into CI.
- **Outside reviewers for the native program and a higher Q&I spot-check rate** (6.5, 8.2), with adoption 6.3's request that Q&I run or observe the audits.
- **D12**: discounted loops become restorable rows within Ari's cut-only rule, which is a cleaner fit than evidence's D8.
- **WP 8.4**: red-team fixtures with expected credit as CI goldens.
- **"The fun parts carry no prize"** (6.2) with D1's build-day rule: gamified surfaces count only witnessed level A or B minutes and never change credited seconds, so nobody profits from faking them.
- **D8 and D11**: calibration labels and age bands only with consent, because fitting detectors is "research and development" under New York's CDPA.
- **The leaked-secret notice** (7.3, from TP B4 step 2).

## Recommendation

**Use the evidence-first design as the base.** It adds more defenses that TP didn't already hand it than the other two together: a challenge seed neither side can steer, signed receipts, committed keyed scorer inputs that let the server recompute dark-flow discounts without text, named consistency checks with held minutes, presence sampled only in a project context, and a local-only mode. Its two worst problems, D4 and D6, are both deviations it chose, and both can be reverted without touching the rest. The adoption design has the better fit with today's Fraud Squad workflow, but its D3 is a fatal flaw as written and its privacy section doesn't treat users as minors. The teen design has the best consent screens and the right `track` rule, but its D10 and D13 weaken exactly the layers an evidence design relies on.

### Grafts

1. **Teen 4.4 and 5 D9 replace evidence D4.** `track` anchors are level C and stay out of submissions unless a program opts in. Add a log-match rule on top: where the harness keeps a log (Zed `threads.db`, Warp `warp.sqlite`), a `track` call with no matching user entry opens nothing.
2. **Teen WP 4.13**: a Playwright assertion that "Not now" is the primary button on every consent card.
3. **Teen 6.5 and 8.2, with adoption 6.3**: the native program's reviewers come from outside the build team, Q&I runs or observes its audits, and Q&I spot-checks it at a higher rate.
4. **Teen 5 D12 replaces evidence D8**: discounted loops become their own restorable rows inside Ari's cut-only rule, rather than a free restore.
5. **Teen WP 8.4**: red-team fixtures with expected credit as CI goldens, extended with the attacks in this document: a `track` loop with a jiggler, forged plugin output sizes, forged editor-extension events, an offline-flush fabrication, and withdraw-and-redraw.
6. **Teen 6.2 and D1**: gamified surfaces never change credited seconds and count only witnessed level A or B minutes.
7. **Teen 7.3**: tell the teen to rotate a leaked secret when redaction finds one (TP B4 step 2).
8. **Adoption 6.4**: the residual-risk table, including the T3 plus T7 row, in the base's 6.12.
9. **Adoption WP 2.4**: the fuzz test on the free-text firewall.

### Must fix, whatever base is chosen

1. **Close the side doors on `input_any` machines.** Anchors from `track`, plugins, the WakaTime bridge and editor-extension claims are level C unless the harness's own log or `input_keys` corroborates them. Output sizes a plugin reports don't fill the reading bucket past `R_TURN_BASE_S` without a hook or transcript record behind them. Add CI fixtures with `input_any` true and `input_keys` unknown.
2. **Cap all offline minutes per submission**, whatever the flush delay, and band them below "witnessed" (TP A6 point 1; evidence D6).
3. **Make the challenge sticky.** Once the draw is made, withdrawing the submission counts every drawn item as withheld, and any later submission that overlaps the window reuses the same draw. None of the three designs says this, and TP B6 doesn't either. Evidence's committed seed (6.5) makes the rule checkable.
4. **Keep revealed text out of downloadable bundles.** The bundle carries commitments, metadata, roots and scores. Revealed content is fetched from the store under the access log, and `pairtime verify` checks it only when it's fetched. Keep revealed content until the spot-check window closes, 12 months after the decision until HQ sets one, never "the longer of" (teen 6.3). The purge button and a reviewer-side "report an exposure" action both have to exist.
5. **Encrypt tier-2 content end to end to the evidence store.** All three designs put Cloudflare in front of the server, and Cloudflare terminates TLS (SP Open question 3), so revealed prompts of minors would exist in plaintext at the CDN. Have the client encrypt reveals to the evidence store's public key.
6. **Profile default.** On Hack Club's instance, ship the unselected choice with "Private" more prominent until a written Children's Code compelling-reason assessment exists and counsel has looked at GDPR Art. 25(2) and New York's CDPA. None of the three wrote the assessment.
7. **Name the operator and controller** before the pilot stores any minor's data on Karim's server (TP Open question 1), and publish the retention schedule.
8. **Request the OS age signal from 2027-01-01** (AB 1043; Declared Age Range on macOS 26+), refuse under-13s locally, and never upload the bracket (TP B2 point 8). None of the three mentions it, and all three timelines run past that date.
9. **Write the interview protocol for minors**: who may hold the call, the channel, no recording, what the teen is told. Keep evidence's asynchronous audit (11.1, risk 6) as the fallback.
10. **Turn the level-B gap into a mechanism.** Audit weight rises with level-B share (evidence 6.9, teen 6.4). No program preset pre-approves level-B or offline minutes. The opt-in typing module ships in signed builds before the first program pays out. Whether a higher audit chance after declining it is "degrading" under CDPA § 899-ff(4) goes to counsel (evidence 7.3). If the pilot shows level-B hours approved well below level-A hours, credit the compose part of a lead only where `input_keys` exists, as SM 2.3 already does on keyboard-aware machines.
11. **Let the VM label gate level A, never credit.** A VM is the cheapest way to launder injected keys into "hardware" inside the guest (T10), and the opt-in typing module makes that more valuable, not less. Minutes that are level A only because of the typing module, on a machine labeled VM (using TP's root-partition check, so Windows with VBS isn't misread), drop to level B. No design notices this.
12. **Keep tier-2 feature definitions private** beyond SM 8.7's published starting set (TP A8 #9), whatever the R1 decision.
13. **Minimize the server's copy of logged-only hours.** No anchor ids in uploads (evidence D1). Per-minute buckets only for the 180-day submission horizon, then daily totals. IPs deleted after 30 days, not truncated and kept. Presence sampled only in a project context (evidence D10).
14. **Don't expose trust levels to programs** (adoption 6.1, teen 3.8). A program sees the outcomes of its own submissions and nothing else about a teen's review history.
15. **Drop "don't mention it"** from the AGENTS.md entry (adoption 4.4, teen 4.4).
16. **Plan for device loss**: TP B6 point 2's recovery code that wraps `K_dev`, a backup that survives a Windows reinstall, and "witnessed, evidence lost" on the review page instead of a hole.

### What no design can fix, and the base should say plainly

The full puppeteer (TP U3) still works on every design: an LLM writes prompts that react to the agent, a $5 HID board or a VM's virtual keyboard types them, a real agent builds a real project, and real commits go to GitHub. After the fixes above, what stops it is the published, hours-weighted audit rate, the agent-message context in the reveal (evidence D3), weeks of witnessed history, and a person asking the teen to change their own code live. At $5 an hour of prize value (TP A1), that combination makes faking cost more than building, which is the most any tracker that respects minors' privacy can promise.
