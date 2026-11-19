# Trust and privacy: how teens would fake hours, what stops them, and how to do it privately

Status: draft in progress

Research for pairtime (AGPL-3.0), started 2026-11-19. Requirement IDs (R1-R11, A) refer to [docs/BOUNTY.md](../BOUNTY.md). This doc covers R2 (accuracy), R6 (privacy), R9 (trustworthy) and R10 (auditable). Every claim that isn't common knowledge cites a URL or repo path; "(unverified)" marks what I could not confirm from a primary source. Nothing here is legal advice.

Sibling docs this one builds on, not repeats: [hackatime-and-hack-club.md](hackatime-and-hack-club.md) (Hackatime anti-fraud, Telescreen/Robert, YSWS review rules), [ai-harness-signals.md](ai-harness-signals.md) (what each harness logs), [prior-art-and-measurement.md](prior-art-and-measurement.md) (measurement research), [client-platform.md](client-platform.md), [server-platform.md](server-platform.md).

## 0. Sources and versions seen

Everything was read on 2026-11-19 unless a row gives another date. Full citations sit next to each claim; this table is the index a fact-checker starts from. "Secondary" means I read a summary, not the primary text.

| Group | Source | Version or date seen | Used in |
|---|---|---|---|
| Hack Club, public | [fraud.hackclub.com](https://fraud.hackclub.com/) FAQ and [/fairplay](https://fraud.hackclub.com/fairplay) | Undated pages, fetched 2026-11-19 | A2 |
| | [jackpot.hackclub.com/rules](https://jackpot.hackclub.com/rules) | Fetched 2026-11-19 | A2 |
| | [hackclub.com/privacy](https://hackclub.com/privacy/) | Undated page, fetched 2026-11-19 | B3 |
| | [readme.hackclub.com/nutshells](https://readme.hackclub.com/nutshells) | Via [hackatime-and-hack-club.md](hackatime-and-hack-club.md) 3.2 | A1 |
| | hackclub/hackatime: `docs/configuration/privacy.md`, `config/routes.rb`, `Privacy::Token`, `Privacy::Eraser` | Commit `43bde10b02db` | B3 |
| | [heliodex.cf/post/hackatimeBanned](https://heliodex.cf/post/hackatimeBanned) (first-hand account, not Hack Club's) | Posted 2026-08-16 | A2 |
| Loops | [ghuntley.com/ralph](https://ghuntley.com/ralph/) | 2025-07-14 | A3 T2 |
| Trackers and telemetry | wakatime/wakatime-cli `USAGE.md` | `develop` at `7ab26b5db6fe` (2026-11-05) | B1 |
| | [ActivityWatch FAQ](https://docs.activitywatch.net/en/latest/faq.html) | `latest` docs | B1 |
| | [VS Code telemetry docs](https://code.visualstudio.com/docs/configure/telemetry); [VSCodium README](https://github.com/VSCodium/vscodium) | Docs page dated 2026-11-18 | B1 |
| | [go.dev/doc/telemetry](https://go.dev/doc/telemetry); [Russ Cox, "Telemetry opt-in"](https://research.swtch.com/telemetry-opt-in) | Post 2023-02-24; docs current | B1 |
| | [GitHub blog on Copilot interaction data](https://github.blog/news-insights/company-news/updates-to-github-copilot-interaction-data-usage-policy/); [Copilot policy settings](https://docs.github.com/en/copilot/how-tos/manage-your-account/managing-copilot-policies-as-an-individual-subscriber); [community discussion 188488](https://github.com/orgs/community/discussions/188488); [Morse](https://trymorse.ai/blog/does-github-copilot-train-on-your-code) (secondary) | Blog 2026-03-25; Morse 2026-11-01 | B1 |
| | [Microsoft Recall architecture](https://blogs.windows.com/windowsexperience/2024/09/27/update-on-recall-security-and-privacy-architecture/); [Signal](https://signal.org/blog/signal-doesnt-recall/); [Simon Willison](https://simonwillison.net/2024/Jun/1/stealing-everything-youve-ever-typed/) | 2024-09-27; 2025-05-21; 2024-06-01 | B1 |
| Law (not legal advice) | [FTC COPPA amendments](https://www.ftc.gov/legal-library/browse/federal-register-notices/16-cfr-part-312-coppa-final-rule-amendments); [Latham & Watkins](https://www.lw.com/en/insights/ftc-publishes-updates-to-coppa-rule) (secondary) | Published 2025-04-22, effective 2025-06-23, compliance 2026-04-22; L&W 2025-05-14 | B2 |
| | [euCONSENT, digital age of consent](https://euconsent.eu/digital-age-of-consent-under-the-gdpr/); [gdpr-info.eu Art. 25](https://gdpr-info.eu/art-25-gdpr/) | euCONSENT dated 2021-10-26 | B2, B7 |
| | [GOV.UK child online safety guidance](https://www.gov.uk/guidance/child-online-safety-data-protection-and-privacy); [Data (Use and Access) Act 2025 s. 81](https://www.legislation.gov.uk/ukpga/2025/18/section/81) | GOV.UK revised 2021-09-27; s. 81 in force 2026-02-05 | B2 |
| | [Cooley on NetChoice v. Bonta](https://www.cooley.com/news/insight/2026/2026-03-30-netchoice-v-bonta-ninth-circuit-narrows-injunction-against-californias-ageappropriate-design-code-act); [FKKS on AB 1043](https://fkks.com/?passle-post=102o301) (both secondary) | Cooley 2026-03-24 | B2 |
| | [N.Y. GBL § 899-ee](https://www.nysenate.gov/legislation/laws/GBS/899-EE) and [§ 899-ff](https://www.nysenate.gov/legislation/laws/GBS/899-FF); [NY AG CDPA guidance](https://ag.ny.gov/child-data-protection-act-guidance); [9 V.S.A. § 2449a](https://legislature.vermont.gov/statutes/section/09/062/02449a) | § 899-ee version 2025-06-27; guidance updated 2025-05-19; Vermont section effective 2027-01-01 | B2 |
| | [dpdprules.org on DPDP children's rules](https://dpdprules.org/blog/dpdp-childrens-data-rules) (secondary) | Sources checked there 2026-10-28 | B2 |
| Secrets and PII tools | gitleaks `config/gitleaks.toml`; detect-secrets; TruffleHog; Presidio `conf/default.yaml`; spaCy model `meta.json`; GLiNER; `knowledgator/gliner-pii-edge-v1.0` card and file list (Hugging Face API); gline-rs README | gitleaks v8.30.1 (2026-03-21); detect-secrets v1.5.0 (2024-05-06); TruffleHog v3.99.9 (2026-11-12); Presidio 2.2.366 (2026-10-27); spaCy 3.8.0; GLiNER v0.2.30 (2026-11-02); gline-rs v1.1.0 (2026-08-05) | B4 |
| Code similarity | [JPlag](https://github.com/jplag/JPlag); [Dolos](https://github.com/dodona-edu/dolos); Sağlam et al. ICSE-Companion 2024; Schleimer, Wilkerson and Aiken SIGMOD 2003; Biderman and Raff CIKM 2022 | JPlag v6.3.0 (2025-12-11); Dolos v2.10.0 (2026-10-19) | A4 |
| OS input provenance | Microsoft Learn `KBDLLHOOKSTRUCT`; Apple `CGEventSourceStateID` and `secondsSinceLastEventType` (developer.apple.com JSON); Karabiner-Elements PR #4414; [AutoPTT](https://autoptt.com/posts/simulating-a-real-keyboard-with-faker-input/); Ryochan7/FakerInput; pqrs-org/Karabiner-DriverKit-VirtualHIDDevice; ReimuNotMoe/ydotool `Daemon/ydotoold.c`; MDN `Event.isTrusted`; [Clearcote](https://www.clearcotelabs.com/audit/checks/event-istrusted-provenance) (secondary); [macos-use.dev](https://macos-use.dev/t/macos-computer-use) (vendor) | MS page updated 2024-02-22; PR merged 2026-03-14 (`a83446cc8d8e`); AutoPTT 2026-01-11; FakerInput v0.1.1 (2025-12-19, archived); ydotool `708e96ff27e3`; MDN 2026-04-08; macos-use 2026-06-15 | A4 |
| Detector papers | Barabási 2005; Goh and Barabási 2008; Dhakal et al. CHI 2018; DeAlcala et al. CVPRW 2023 (arXiv 2207.13394v3); Stefan, Shu and Yao 2012; Song, Wagner and Tian 2001; Broder 1997; Manku, Jain and Das Sarma 2007 | Published versions; DOIs inline | A4 |
| Anti-cheat, proctoring, click fraud | McDonald, GDC 2018, via [Edgegap](https://edgegap.com/blog/anti-cheat-deep-dive-counter-strike-global-offensive) (secondary); [Riot](https://www.leagueoflegends.com/en-us/news/dev/dev-null-anti-cheat-kernel-driver/); [Bergmans et al. CSEDU 2021](https://www.scitepress.org/PublishedPapers/2021/103996/); [Sydney two-lane FAQ](https://educational-innovation.sydney.edu.au/teaching@sydney/frequently-asked-questions-about-the-two-lane-approach-to-assessment-in-the-age-of-ai/); [MRC IVT addendum](https://www.mediaratingcouncil.org/sites/default/files/Standards/IVT%20Addendum%20Update%20062520.pdf); [CISA TA18-331A](https://www.cisa.gov/news-events/alerts/2018/11/27/3ve-major-online-ad-fraud-operation); [CBS News](https://www.cbsnews.com/news/wells-fargo-fires-employees-faking-work/); [Oleson et al. HCOMP 2011](https://cdn.aaai.org/ocs/ws/ws0800/3995-16701-1-PB.pdf) | Riot 2020-02-03; Sydney 2024-07-02, revised 2026-04-21; MRC June 2020; CISA 2018-11-27; CBS 2024-06-17 | A5 |
| LLM redaction | [Singhal et al. EDM 2024](https://educationaldatamining.org/edm2024/proceedings/2024.EDM-short-papers.57/index.html); [Garza et al. arXiv:2508.05545](https://arxiv.org/html/2508.05545) | 2024; 2025 | B5 |
| Commitments | [RFC 9162](https://www.rfc-editor.org/rfc/rfc9162) section 2.1; [Johnson, Molnar, Song and Wagner, CT-RSA 2002](https://people.eecs.berkeley.edu/~daw/papers/hom-rsa02.pdf) | | B6 |

## Part A. Red team

### A1. Threat model and attacker profile

**The attacker.** A motivated 16-year-old with an afternoon, an LLM subscription, the AGPL source of the client and server, and a normal laptop. They can run scripts, AutoHotkey, `pyautogui` or `xdotool`, `claude -p`, a VM, a friend's laptop, and can buy a $10 USB "mouse jiggler". They are not a nation-state: they won't write a kernel driver or build a custom USB HID board from scratch, but an LLM will happily walk them through flashing a $5 microcontroller as a keyboard if the payoff justifies it.

**The payoff.** "One hour of shipped work is $5 for most YSWSs" ([readme.hackclub.com/nutshells](https://readme.hackclub.com/nutshells), via [hackatime-and-hack-club.md](hackatime-and-hack-club.md) 3.2), and programs get 85 USD of budget per weighted project of 10 hours. Stardance caps a devlog at 10 paid hours (`MAX_PAYOUT_HOURS_PER_DEVLOG`, sibling doc 3.3). So a successful fake 40-hour project is worth about $200 in prizes, and a fake hour is worth about the minimum wage a 16-year-old could earn doing anything else. That matters: attacks costing more than an hour of attacker time per fake hour are not worth running for money, though some teens will do it for status (leaderboards, streaks) or for the challenge.

**What the attacker wants**, from most to least common (ordering is my judgment from R9's own text: "Most of this isn't fraud. Many were hours where we felt the person wasn't pushing themselves"):

1. **Padding a real project**: leave things running, re-roll prompts, count idle or low-effort time. This is the 123,400 to 27,600 Stardance gap, and it is mostly an accuracy problem (R2), not a fraud problem.
2. **Making hours appear without being there**: scripted prompting, an LLM driving the agent, auto-typers, jigglers.
3. **Fabricating the record**: editing or replaying local logs, or a modified client sending invented events.
4. **Fabricating the project**: copying someone else's project, or having someone else build it.
5. **Scaling**: alt accounts, account sharing, second machines, colluding reviewers.

**Trust boundaries.** Under AGPL the client is fully in the attacker's hands: every byte it sends is a claim, not a measurement. What the attacker does not control is:

| Thing the attacker can't control | What it gives the defense |
|---|---|
| The pairtime server's clock | When a record was first seen. Anything committed late is "backfilled", never "witnessed" (grit's model, [prior-art-and-measurement.md](prior-art-and-measurement.md) 1.14) |
| Third-party servers: GitHub push and Actions timestamps, CI results, package registries | Independent timestamps and outcomes (a test that went green on GitHub's runners) |
| The physical fact of one human with one attention stream | Two machines or two projects can't both have a human's full attention in the same minute |
| Cross-user data on the server | Duplicate prompts, duplicate repos, shared devices and IPs, impossible distributions |
| Human reviewers and live conversation | The teen has to explain the work; Fraud Squad already does this ("We want to talk to you", [fraud.hackclub.com](https://fraud.hackclub.com/)) |
| Hidden server-side rules | Whatever the attacker can't read can't be tuned against, as long as it isn't in the AGPL client |

Everything in this part follows from that table. Client-side checks are worth having because they catch honest automation and lazy cheating cheaply, but they are not where trust comes from.

### A2. What Hack Club has said publicly about Hackatime fraud

Hackatime's own tooling (trust levels, poison, alt detection by machine and IP, JA4 fingerprints, the quantized `lineno`/`cursorpos` plot), Stardance's integrity flags, Robert's 1-10 score and Ari's open-core split are documented in [hackatime-and-hack-club.md](hackatime-and-hack-club.md) 1.9, 3.3, 3.5 and 3.6. This section adds only what Hack Club says in public, plus one public first-hand account.

**Fraud Squad FAQ** ([fraud.hackclub.com](https://fraud.hackclub.com/), fetched 2026-11-19, no date on page):

- A ban "means someone in the fraud department suspects you may have been dishonestly sending data to Hackatime." "The fraud department uses certain metrics to attempt to detect people unfairly gaining hours on Hackatime."
- Secrecy is policy: "We don't share the specific patterns we look for, because that would only help people who abuse the system." They concede "sometimes we misjudge Hackatime data."
- A flag blocks one project from one event ("A flag is not a ban"); "Flags are usually not something you can fix by resubmitting the same project."
- Appeal evidence: a zip of VS Code's local history folder (`%APPDATA%\Code\User\History`, `~/.config/Code/User/History`, `~/Library/Application Support/Code/User/History`) or, for other editors, "a timelapse of your screen while coding", and "your Hackatime activity should line up with your previous activity."
- "If you use AI to write or hold the conversation for you, we will turn down your appeal and ban you permanently." The reviewers describe themselves as "all teens".

**Fair Play Policy** ([fraud.hackclub.com/fairplay](https://fraud.hackclub.com/fairplay), fetched 2026-11-19, no date): "Only log time you genuinely spend coding." "Don't use scripts, bots, or tools to artificially inflate your coding time." "Don't manipulate heartbeats, extensions, or API calls to game the leaderboards." "Don't attempt to spoof editor activity or idle with the intent of logging hours." It is a "zero-tolerance policy" and "bans are non-negotiable unless determined to have been issued incorrectly." It says nothing about alt accounts, shared accounts, VMs or AI-assisted coding.

**Program rules** say the same in short form. Jackpot ([jackpot.hackclub.com/rules](https://jackpot.hackclub.com/rules), read-only, fetched 2026-11-19): "Don't cheat the time tracking system. No bots, no fake key presses, no UI manipulation", a required "Github opensource page with frequent commits (every 30min - 1h)", and a ban "from Hackatime and other participating YSWS / events / programs." The YSWS handbook's Override Hours Spent page names "heartbeats generated by a script rather than actual coding activity" and "A commit history that was manufactured after the fact" as fraud that never enters the Unified DB (sibling doc 3.7).

**A public account from the other side.** "So you've been Hackatime banned" ([heliodex.cf/post/hackatimeBanned](https://heliodex.cf/post/hackatimeBanned), posted 2026-08-16) is written by someone who was banned and later reinstated. It is one person's view, but it is the only detailed public description of the process I found:

- The ban message claims "we've found clear evidence of hour manipulation and inflation"; reasons given rarely go beyond "matched patterns strongly indicative of fraud" or "discrepancies in the evidence provided."
- Things the author says draw suspicion: account sharing ("Boom, double heartbeats, instant suspicion."), fewer than one commit per coding hour, always hitting the 10-hour devlog cap, and time attributed to AI agents.
- Evidence that works on appeal: "Editor history and videos will be the most useful forms of evidence." Agent transcripts and screenshots may be refused.
- The author's main complaint is false positives from thin data: "Heartbeats don't contain enough information for the Fraud Squad to make useful or informed decisions from", and shared PCs, buggy plugins or double-logged time can trigger flags. A frozen account blocks every YSWS while the case is open, so a false positive costs deadlines and streaks, not just hours.
- They also report that reputation buys leniency ("significant leniency to community members with stronger reputations").

What this means for pairtime:

1. **Fraud Squad already distrusts agent transcripts and trusts editor history and screen video.** A transcript is easy for an LLM to produce (BOUNTY.md thread). Pairtime's evidence must be something a transcript alone can't fake: commitments the server saw at the time, plus human-side signals, plus outcomes on third-party servers.
2. **Secret rules are the house style.** Hack Club's fraud tooling lives in private repos (Stardance's secrets submodule, Ari's `private/`). Pairtime's server should keep the same split (A8, and the AGPL question in Open questions).
3. **False positives are expensive and the community notices them.** Every detector below gets a false-positive cost, and none of them should ban on its own. Flags route to people.
4. **Commit cadence is already a heuristic** (Jackpot's "every 30min - 1h", the author's "one commit per coding hour"). Pairtime can show commits as anchors, but must not turn commit counts into a target: agents commit as often as asked.

### A3. Attack catalogue

**How to read the ratings.**

- **Cost** is the attacker's setup effort. Low: under an hour with free tools and an LLM writing the script. Medium: an afternoon, or up to about $60 of hardware or API spend. High: days of work or several people.
- **Gain** is credited hours per account per week if nobody catches it. "Naive" means a tracker that credits agent activity or editor heartbeats (WakaTime's `ai coding`, Hackatime today). "Pairtime rules" means the credit rule the sibling docs recommend: credit only minutes with human-side evidence, with capped reading and think windows, agent-only spans at zero, mouse-only input never counting, and one human-minute per wall-clock minute across all devices and projects ([ai-harness-signals.md](ai-harness-signals.md) Design implications 2 and 3; [prior-art-and-measurement.md](prior-art-and-measurement.md) 3.3).
- **Detect** is how reliably it gets caught: automatically by the client (C), by the server (S), or only at review (R). High, Med, Low, None.

The point of the "Pairtime rules" column: the cheapest attacks earn nothing by construction, so detection effort can go to the expensive ones.

| ID | Attack | Cost | Gain: naive | Gain: pairtime rules | Detect |
|---|---|---|---|---|---|
| T1 | **Headless loop**: `claude -p`, `codex exec`, `gemini -p` or an Agent SDK script in a `while` loop or cron job | Low | Up to 24 h/day, limited only by program caps | ~0: no OS input, no focused window | High (C: entrypoint and process tree; S: agent-only spans) |
| T2 | **Self-continuing modes**: `/loop`, `/goal`, Copilot autopilot, Cursor `loop_count` follow-ups, "Ralph" loops (`while :; do cat PROMPT.md \| claude-code ; done`, Geoffrey Huntley, [ghuntley.com/ralph](https://ghuntley.com/ralph/), 2025-07-14, which cites a hackathon team that "Shipped 6 Repos Overnight") | Low | 8+ h/day | Unsupervised cap only (about 1 min per run, grit's default) | High where the harness flags it (`Stop.session_crons`, `stop_hook_active`, `loop_count`); Med otherwise (regular timing, no input) |
| T3 | **Agent drives agent through the TUI**: `tmux send-keys`, `expect`, a pty wrapper, an MCP terminal server, or Claude Code typing into Codex | Low to Med | Unbounded | ~0 unless combined with T5/T6: these write to the pty, not the OS input queue, so OS idle keeps rising | High (C: prompt submit with no OS keystrokes in the seconds before it) |
| T4 | **Computer-use or browser automation**: an LLM agent that takes screenshots and clicks and types (computer-use APIs, Playwright on web chat or app builders) | Med (API spend roughly a few dollars per hour of continuous screenshots, my estimate) | Unbounded | High if its injected input passes as human | High on Windows and X11 if it injects through `SendInput`/XTEST (C: injected flags); Low if run inside a VM (T10) |
| T5 | **Software auto-typer** with human-like cadence (AutoHotkey, `pyautogui`, Duey-style typers) typing LLM-written prompts | Low | n/a (naive trackers don't need it) | High if undetected: it produces typing, focus and varied prompts | High on Windows (`LLKHF_INJECTED`) and X11 (XTEST device); Med from cadence (C); Wayland: pairtime can't see keystrokes at all |
| T6 | **Hardware keyboard injector**: a $5 Pi Pico or Digispark running HID firmware, or a ~$60 "key-pressing gadget" | Med | n/a | High if undetected | Low to Med: no injected flag; an extra keyboard device appears in Raw Input; cadence; content and consistency checks; review |
| T7 | **Mouse jiggler**: software (Move Mouse; Caffeine sends an F15 key-up every 59 s), USB HID jiggler, or a mechanical one under an optical mouse | Low ($0-20) | Extends "present" time on presence-based trackers by hours | ~0: mouse-only input never earns credit | High for software (injected flag, fixed period); Med for USB (periodic micro-motion, no keys); mechanical: pattern only |
| T8 | **Replaying or editing local logs**: rewrite transcript timestamps, duplicate sessions, import someone else's transcripts | Low | Unbounded on lookback readers that trust transcripts | 0 witnessed hours; only "backfilled" tier | High (S: chain heads uploaded every few minutes; anything first seen later is backfilled) |
| T9 | **Modified client or raw API calls** sending fabricated events live | Med (an evening forking the AGPL client with an LLM) | Unbounded | Whatever the server-side checks and review let through | Med (S: consistency with forge-verified commits, physical plausibility, cross-user distributions; R: random reveal, interview) |
| T10 | **VM laundering**: run the harness in a VM and inject keys from the host (`VBoxManage controlvm <vm> keyboardputstring`, QEMU `sendkey`, VNC/RDP), so input looks like hardware inside the guest | Med | n/a | High if undetected | Med (C: hypervisor CPUID leaf, SMBIOS strings, MAC OUI, all spoofable); cadence; review |
| T11 | **Second machine**: (a) same account on two machines; (b) an alt account on the second machine | Low | 2x or more | (a) 0: one human-minute per minute across devices; (b) alt hours can't be paid without a second verified identity | High for (a) (S); Med for (b) (S: shared IP, device, GitHub account, repos) |
| T12 | **Splitting one session across projects**: parallel agents in several repos, or moving time to whichever project a program pays for | Low | 2-5x | 0 extra: each human-minute goes to one project | High (S: overlap check; Unified DB already submits only new hours per Code URL, sibling doc 3.3) |
| T13 | **Padding prompts**: LLM-expanded or deliberately long prompts, slow typing, typing then deleting, mashing keys in a scratch buffer | Low | n/a | Small if compose credit is capped and sublinear | Med (C: paste vs typed ratio, typing outside relevant windows; R: prompt sample) |
| T14 | **Walking away** with the agent running | None | Hours per day | Unsupervised cap only | High (C: OS idle, screen lock, sleep) |
| T15 | **Dark-flow re-rolling**: same prompt again, "try again", pasting errors back with no reasoning | None (often not deliberate) | Full time | Down-weighted | Med to High (C: near-duplicate prompts with no edits, tests or reading between them) |
| T16 | **Copying a project**: (a) fork or clone and commit it as your own; (b) "reconstruction", where an agent rebuilds a finished repo commit by commit while T5/T6 feeds prompts | (a) Low; (b) Med | (a) all the source project's hours; (b) unbounded | (a) small: no timeline produced the code; (b) high if undetected | (a) High (S: shared root commits, code similarity against Unified DB code URLs); (b) Low to Med (R: similarity of the final code, interview) |
| T17 | **Account sharing**: (a) two people on one account at once; (b) someone else does the work on your account; (c) borrowing a friend's verified identity for hours you made | Low | (a) 2x; (b), (c) all of it | (a) 0 extra; (b), (c) all of it | (a) High (S: concurrent human input on two devices; "double heartbeats"); (b) Low (C: local typing profile shift; R: interview); (c) Med (S: device and IP overlap) |
| T18 | **Colluding reviewers**: a friend approves inflated hours, reviewer rings, picking friends' submissions off a claim queue | Med (needs a reviewer) | Whatever is submitted | Whatever is submitted | Med (S: random assignment, reviewer statistics, seeded test cases; R: HQ spot-checks) |
| T19 | **Staged struggle**: asking the agent to break things so they can be "debugged", gold-plating, churn | Med | Full time | Partially credited: these are real hours of low-value work | Med (S: churn, reverted code, tests that fail only because of the same session's changes; R: is the project's complexity worth the hours?) |
| T20 | **Clock games**: system clock set back; events queued offline and uploaded later | Low | Unbounded on trackers that trust client time | 0: server receipt time is the clock; offline time is marked as such | High (S) |
| T21 | **Replaying your own recorded typing**: capture a real session's key timings once (pairtime's own capture code would do it), then replay them through T6 under new LLM prompts | High | n/a | High if undetected | Low to Med: timings recorded on one text don't fit another text's digraphs; content and review checks |

**Countermeasures, with their privacy and false-positive cost.** "FP" means a false positive: an honest teen flagged or under-credited.

| ID | Client | Server | Review | Privacy cost | FP cost and mitigation |
|---|---|---|---|---|---|
| T1, T2 | Label automated turns with every harness flag (sibling signals doc 12.1); record the agent's parent process chain locally | Agent-only spans are shown as agent time, never submitted as struggle | None needed | None beyond local process names | Honest automation (CI agents, scheduled jobs) is labeled, not punished |
| T3 | Require OS keyboard input in the focused terminal or harness window in the N seconds before a prompt submit | Flag `no-input-prompts` share (grit's rule: at least 2 prompts and a quarter of all prompts) | Look at the prompt sample | Idle counter only; no keys recorded | **SSH and cloud dev boxes**: a human typing over SSH produces no OS input on the remote machine. Needs a local daemon whose "terminal focused + typing" stream the server joins with the remote daemon's prompts (Open questions) |
| T4, T5 | Read `LLKHF_INJECTED` (bit 4) and `LLMHF_INJECTED` on Windows, the XTEST source device on X11; local cadence classifier trained on known auto-typers | Injected-input share per submission | Prompt sample: is the voice consistent with the person's devlogs and messages? | Low-level hooks see every key; process them in memory, never store key identities, and keep only per-minute counts and a cadence score | **Accessibility**: dictation (Windows Voice Access, Dragon, Talon), on-screen keyboards, switch access, text expanders and password managers inject input too. Never treat injection as fraud. Injected minutes simply don't count as typing evidence; the user can declare assistive input, and the reviewer then weighs other evidence |
| T6, T21 | Raw Input device inventory: a new keyboard `hDevice` that types while the built-in keyboard is silent, with its VID/PID kept locally; per-user typing profile kept locally | Share of credited minutes from a newly seen input device | Interview; live change | Device inventory is fingerprinting. Keep it local and upload only "new input device: yes/no" | External keyboards are normal; this can only ever raise a question |
| T7 | Mouse-only input never counts as presence. Scrolling in the focused agent window counts as reading only inside a window a human act opened, capped by output length | None needed | None | None | Reading long output with only the scroll wheel is legitimate; the reading cap is what handles it |
| T8, T20 | Hash-chain every event; send the chain head every 5 minutes (B6) | Store `received_at` for every head. Time first seen more than 15 minutes after it happened is "backfilled" (grit's window); install-time lookback is backfilled by definition | Backfilled hours need corroboration (forge push times) or get deflated | Heads are random-looking; no content | **Offline work** (school Wi-Fi, flights) gets backfilled. Show it as "offline, verified on reconnect" and let commits pushed later corroborate it, instead of zeroing it |
| T9 | Official signed builds report a build id; custom builds are allowed and labeled (Hackatime accepts custom clients too) | Consistency checks: commits must verify on the forge with matching diffstats and push times; event sequences must be physically plausible; distributions compared across users; JA4 TLS fingerprint against the claimed client | Server-chosen random reveal of committed items (B6) and an interview | Reveals only the sampled items | A custom client isn't fraud; label it and require more evidence |
| T10 | Hypervisor present bit (`CPUID.01h:ECX[31]`) plus vendor leaf `0x40000000` (`VBoxVBoxVBox`, `VMwareVMware`, `KVMKVMKVM`, guest `Microsoft Hv`), SMBIOS product strings, MAC OUIs (`08:00:27` VirtualBox; `00:05:69`, `00:0C:29`, `00:50:56` VMware) | VM sessions get a lower trust tier | More evidence needed | Hardware fingerprinting; upload only a "VM: yes/no/unknown" label | **High FP**: Windows with VBS or Hyper-V on runs the host itself in Hyper-V's root partition, so the hypervisor bit is set on bare metal (OpenJDK bug JDK-8250598, "Hyper-V is detected in spite of running on host OS"); check the root-partition privilege bits in leaf `0x40000003` instead. ChromeOS Linux (Crostini) runs in a VM by design, and some Hack Clubbers only have a school Chromebook. Label, never ban |
| T11, T12, T17a | Machine id per device (random, local) | One human-minute per wall-clock minute per account across all devices and projects; Hackatime-style alt candidates (shared IP plus device plus repo within 30 days); payouts only to HCA `verified` identities | Alt cases go to Fraud Squad | IP addresses kept short-term (B8) | Siblings sharing a family PC, or a whole club on school Wi-Fi, share IPs. Shared IP alone must never flag |
| T13 | Compose credit capped and sublinear; pasted text never counts as typing; typing only counts in relevant windows; text typed then deleted is churn | Distribution of prompt lengths against the user's own history | Prompt sample | Local only | Long, careful prompts are good practice; the fix is not rewarding length at all, rather than penalizing it |
| T14 | Unsupervised cap; screen lock, sleep and lid events end credit | None | Reviewer may add think time with a reason | None | Thinking away from the keyboard is real struggle; a capped think gap (grit: 10 min) plus reviewer additions handle it |
| T15 | Local MinHash of normalized prompts; down-weight near-duplicates with no edit, test or reading in between; show it to the user | Upload only the re-roll share | Reviewer sees the share and the sample | Local only | "Still failing: <new error>" looks repetitive but is progress. Count a changed error message or test result as progress |
| T16 | Root commit set; time-to-code ratio (large code appearing with little timeline) | Root commits matched against public repos and other users' projects (server doc 4.2); code similarity against Unified DB code URLs (B-list tools in A4) | "The project must do what it says it does"; interview; ask for a small live change | Code similarity only runs on public repos | Templates, starter kits and tutorials share root commits and code. Flag for review, don't reject |
| T17b, c | Local typing profile drift (B4 privacy rules apply) | Device and IP overlap between the two identities | Interview | Typing profile stays local | Typing changes with keyboards, injuries and fatigue; drift alone means nothing |
| T18 | None | Random assignment instead of a claim queue; no reviewing anyone from your own club; reviewer statistics (approval rate, hours per review, seconds per decision); seeded test submissions with known answers | HQ spot-checks (handbook) | Reviewer actions are logged and visible to HQ | Reviewer statistics need context: some queues are harder than others |
| T19 | Churn: lines added then removed in the same session, tests broken and fixed by the same session | Hours per surviving line, against peers on similar projects | Is the project's complexity worth the hours? | Local diff stats only | Refactors and rewrites look like churn; a reviewer decides |

### A4. Detectors in detail

Signal capture per harness and OS is in [ai-harness-signals.md](ai-harness-signals.md) 12-13, and the engagement anti-patterns in [prior-art-and-measurement.md](prior-art-and-measurement.md) 3.3. This section gives the detector math, the thresholds to start from, and where each detector breaks. Every threshold is a starting point to fit against reviewer decisions (prior-art doc 3.5), not a constant.

#### Inter-event timing

Human activity is bursty: short gaps cluster and long gaps are heavy-tailed ([Barabási 2005, Nature](https://doi.org/10.1038/nature03459), "The origin of bursts and heavy tails in human dynamics"). Goh and Barabási's burstiness parameter gives one number per series of gaps with mean μ and standard deviation σ: `B = (σ − μ) / (σ + μ)`, which is −1 for a perfectly periodic signal, 0 for a Poisson process and approaches 1 for very bursty activity ([Goh and Barabási 2008, EPL 81, 48002](https://iopscience.iop.org/article/10.1209/0295-5075/81/48002); [arXiv:physics/0610233](https://arxiv.org/abs/physics/0610233)).

| Series | Flag | Catches | Breaks on |
|---|---|---|---|
| Gaps between prompt submits in a session (6 or more prompts) | `B < −0.5`, or coefficient of variation σ/μ < 0.1 (grit's `regular-timing`) | `/loop`, cron, `sleep 1200` loops, Karim's "check something every 20 minutes" | Scripts that add random jitter: uniform or Poisson jitter gives B near 0, which is still well below typical human values, so also flag `B < 0.1` over long sessions as weak evidence |
| Spectral peak of the per-minute event count (autocorrelation at lag k) | Strong peak at a fixed period | Periodic schedulers that hide among other events | Real periodic habits (Pomodoro timers) |
| Keystroke-to-submit lag | Prompt submit less than 300 ms after the last keystroke, repeatedly, or with no keystroke at all | Pasted or injected prompts | Fast typists hitting Enter; use it as a weight |
| Session shape | More than about 4 h of credited time with no gap over 10 min | Marathon farming (Ari has a `MARATHON_SESSION` flag, sibling doc 3.6) | Real flow states; R4's "intense flow state for hours" must still count, so this only raises review priority |
| Time of day | Credited activity in every hour of a 24 h day across several days | Shift-sharing (T17) and automation | Insomnia, time zones, travel |

#### Injected input, per OS

Where input came from is the cheapest strong signal against T4 and T5, and what an unprivileged client can learn differs by OS. Capture APIs are in [ai-harness-signals.md](ai-harness-signals.md) 13.4; this table is only about provenance.

| OS | Without a keyboard listener | With a keyboard listener | What still looks like hardware |
|---|---|---|---|
| Windows | `GetLastInputInfo`: any input, injected included, with no keyboard/mouse split | Low-level hook: `LLKHF_INJECTED` (0x10, bit 4) for injection "from any process", `LLKHF_LOWER_IL_INJECTED` (0x02) for injection from a lower integrity level ([KBDLLHOOKSTRUCT](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-kbdllhookstruct), updated 2024-02-22). Raw Input: `RAWINPUTHEADER.hDevice` names each physical device and "is `0` for input generated with `SendInput`" ([AutoPTT](https://autoptt.com/posts/simulating-a-real-keyboard-with-faker-input/), 2026-01-11; the author's observation, not documented by Microsoft) | Virtual HID drivers. [FakerInput](https://github.com/Ryochan7/FakerInput) (MIT, archived, v0.1.1 2025-12-19) is "a driver that shows up as a keyboard and mouse that can be controlled by software", and "As far as the system was concerned, FakerInput and Sidekick *were* physical keyboards" (AutoPTT). Its Raw Input device path reads `HID#SYSTEM&Col0x` instead of a USB VID/PID, so a device inventory can label it. USB HID boards (T6) look like any keyboard |
| macOS | `CGEventSourceSecondsSinceLastEventType(state, eventType)` takes an event type ("To get the elapsed time since the previous input event—keyboard, mouse, or tablet—specify `kCGAnyInputEventType`", [Apple](https://developer.apple.com/documentation/coregraphics/cgeventsource/secondssincelasteventtype(_:eventtype:))), so passing `kCGEventKeyDown` gives key-only idle with no permission. Apple tells "a user space device driver" to post events with `kCGEventSourceStateHIDSystemState` ([CGEventSourceStateID](https://developer.apple.com/documentation/coregraphics/cgeventsourcestateid)), so a low HID-state idle time is not proof of hardware (whether `CGEventPost` resets it is unverified; test on a Mac) | `CGEventTap`, which needs the Input Monitoring permission: an event posted with `CGEventPost` carries the poster's `kCGEventSourceUnixProcessID`. Karabiner's author notes that with synthetic CGEvents "we could identify and ignore re-entrant events via kCGEventSourceUnixProcessID" ([Karabiner-Elements PR #4414](https://github.com/pqrs-org/Karabiner-Elements/pull/4414), merged 2026-03-14). A computer-use vendor reports that its synthetic events carry a non-zero `eventSourceStateID` and hardware events zero ([macos-use.dev](https://macos-use.dev/t/macos-computer-use), 2026-06-15; vendor claim) | DriverKit virtual HID devices: "Both kCGEventSourceUnixProcessID and kCGEventSourceUserData become 0 after passing through the virtual device" (same PR). [Karabiner-DriverKit-VirtualHIDDevice](https://github.com/pqrs-org/Karabiner-DriverKit-VirtualHIDDevice) is Unlicense and ships signed, so any teen can install one |
| Linux X11 | XInput2 raw events reach any X client, no permission | Same: XTEST input (xdotool, pyautogui) arrives from the "Virtual core XTEST" device (signals doc 13.4) | uinput devices, which need root or a udev rule on `/dev/uinput`. `ydotoold` names its device "ydotoold virtual device" with `BUS_VIRTUAL`, vendor 0x2333, product 0x6666 (`Daemon/ydotoold.c` in ReimuNotMoe/ydotool at `708e96ff27e3`, AGPL-3.0), and `/proc/bus/input/devices` lists it to any user; a one-line patch makes it claim to be a USB keyboard |
| Linux Wayland | Idle only (`ext-idle-notify-v1`, Mutter `IdleMonitor`) | Not possible unprivileged. Reading `/dev/input/event*` needs the `input` group, which is a system-wide keylogger and too much to ask | Everything (`wtype`, libei, uinput) |
| Browser extension (web chat, app builders) | n/a | A content script sees DOM events. `isTrusted` is `false` only for events "dispatched via `EventTarget.dispatchEvent()`" or `HTMLElement.click()` ([MDN](https://developer.mozilla.org/en-US/docs/Web/API/Event/isTrusted), 2026-04-08) | Puppeteer and Playwright drive Chrome DevTools Protocol `Input` commands, which the browser turns into events that report `isTrusted=true` ([Clearcote](https://www.clearcotelabs.com/audit/checks/event-istrusted-provenance), secondary); `navigator.webdriver` only flags browsers launched for automation |

What this decides:

1. **Without a keyboard listener, a software auto-typer looks like a person on Windows** (T5): `GetLastInputInfo` only says input happened. The listener is the only client check that labels AutoHotkey `Send`, `pyautogui`, Duey-style typers and computer-use agents (T4) on Windows, macOS and X11 at no measurable CPU cost.
2. **Every OS has a free route to input that looks like hardware**: FakerInput on Windows, Karabiner's signed DriverKit device on macOS, uinput on Linux. The injected-input label catches the lazy majority and forces the rest to install a driver, which a device inventory (T6) can see as a new virtual keyboard. It never proves the input was human.
3. **The listener costs privacy and trust.** On macOS it triggers the Input Monitoring prompt ("receive keystrokes from any application"). On Windows a low-level hook in an unsigned binary that also uploads data looks like a keylogger to antivirus heuristics (signals doc 13.4), which is why [client-platform.md](client-platform.md) 3.3 leaves keystroke counts out of v1. This doc's A8 #5 needs them. The two fit together if the listener is an **opt-in "typing evidence" module**: off by default, explained in one screen, counting only (key-down count, injected flag, hold-time median, rollover share per minute; never key identities), signed binaries only. Minutes with it are labeled "typing checked"; minutes without it are "typing unknown" and are judged on E1-E8 like everyone else's. Whether a program may require it for submitted hours is an Open question.
4. **Mouse-only input never earns credit, even without a listener.** On Windows, `GetLastInputInfo` can't tell a jiggler from a keyboard, but under the credit rule (A8 #1) OS input only extends a capped reading window that a harness or editor act opened. A jiggler can't open one.

#### Typing cadence

Reference numbers from 168,000 typists ([Dhakal et al., CHI 2018](https://doi.org/10.1145/3173574.3174220), Table 1): mean 51.56 WPM (σ 20.20), mean inter-key interval 238.66 ms (σ 111.60), mean **keypress duration 116.25 ms (σ 23.88)**, 6.31% of keystrokes are error corrections, and a **rollover ratio of 25% (σ 17)**, meaning a quarter of keystrokes begin before the previous key is released; rollover correlates with speed at r = 0.73. Their timestamps carry about 10 ms of jitter from USB polling.

Cheap features that separate commodity auto-typers from people, computed in memory per minute:

| Feature | Human | Naive auto-typer | Notes |
|---|---|---|---|
| Keypress hold time | ~116 ms, varies by key | Constant, or ~0 ms when key-down and key-up go out in one `SendInput` call | Free to compute from key-down/key-up pairs |
| Rollover share | ~25% of keystrokes, more for fast typists | ~0%: typers emit strictly serial down/up pairs | Strong and cheap |
| Inter-key interval by bigram | Different-hand bigrams 30-60 ms faster than same-hand (Dhakal) | IKIs drawn independently of the key pair | Needs the key pair class (left/right/alternation), not the keys themselves |
| Pauses at word and line boundaries | Longer pauses before words and lines than inside them | i.i.d. delays, or pauses in the wrong places | |
| Backspace and correction bursts | ~6% of keystrokes | 0, or typos at a fixed rate (Duey's "typos it fixes") | |

How well this works, from the bot-detection literature. [BeCAPTCHA-Type (DeAlcala et al., CVPR Workshops 2023)](https://arxiv.org/html/2207.13394v3) trained detectors on the Dhakal corpus against three keystroke generators. With enough training data, random forests and SVMs reached 0.94-1.00 accuracy against the generator they were trained on. Against a generator they hadn't seen, accuracy fell to 0.47-0.84 for most models, with only the 500-subject LSTM recovering to 0.99. A human-only one-class model was near chance (0.43-0.56). [Stefan, Shu and Yao (Computers & Security 31, 2012)](https://doi.org/10.1016/j.cose.2011.10.001) found per-user models robust against bots that sample other people's timings, but explicitly excluded replay of the owner's own keystrokes from their attack model.

So: a cadence classifier, trained on recordings of the auto-typers teens actually use (pairtime can generate those itself), catches commodity tools well. It will not catch a new generator or a replay of the user's own timings (T21). It raises cost; it doesn't prove anything. It is a client-side check, which a modified client can skip, so its output only means something when the server sees it change from one week to the next on the same device.

**The privacy problem with keystroke timing.** Timing alone leaks content: [Song, Wagner and Tian (USENIX Security 2001)](https://www.usenix.org/conference/10th-usenix-security-symposium/timing-analysis-keystrokes-and-timing-attacks-ssh) showed inter-keystroke timings "speed up exhaustive search for passwords by a factor of 50." Typing rhythm is also behavioral biometric data under GDPR Art. 4(14) when used to identify a person. Rules for pairtime: compute cadence features in memory, keep only per-minute aggregates (count, hold-time median, rollover share, a score), never write per-keystroke timestamps to disk, never upload them, and pause capture entirely while a password field or a password manager has focus (Windows exposes `ES_PASSWORD` on edit controls; browsers mark `type=password`; pairtime should simply stop counting when focus is outside its relevant-window list).

#### Prompt-to-prompt similarity

| Method | Size and cost | Use |
|---|---|---|
| Normalize (lowercase, collapse whitespace, replace numbers, paths and identifiers with placeholders), then exact hash | Free | grit's "same template with numbers changed" rule |
| MinHash over word 3-shingles or character 5-shingles, 64-128 permutations ([Broder 1997](https://doi.org/10.1109/SEQUEN.1997.666900)) | 256-512 bytes per prompt; microseconds | Jaccard similarity ≥ 0.8 with the previous 1-3 prompts, and no edit, test run or reading window in between, marks a re-roll (T15) |
| 64-bit SimHash ([Manku, Jain and Das Sarma, WWW 2007](https://doi.org/10.1145/1242572.1242592): near-duplicates at Hamming distance ≤ 3 over 8 billion pages) | 8 bytes per prompt | Cheapest near-duplicate index, including for cross-session repetition |
| Small sentence-embedding model (all-MiniLM-L6-v2 class: about 22M parameters, 384 dimensions, roughly 23 MB as int8 ONNX; size from the model card, unverified for int8) | Tens of ms per prompt on an old dual-core CPU (unverified; measure) | Semantic re-rolls ("fix it" vs "make it work please"). Optional, v2 |

All of this stays on the device. What uploads is a count: re-roll share per session and per submission. A re-roll is not fraud; it is the vision post's "doing the same thing over and over again," which simply shouldn't earn credit.

#### Progress signals

Progress signals bound hours; they don't prove them. A teen who spent 30 honest hours on a hard bug may have three commits to show for it, so these feed review priority and the deflation math, never an automatic reject.

| Signal | How | What it catches | Unforgeable by the client? |
|---|---|---|---|
| Commits, witnessed and forge-verified | `server_first_seen_at`, GitHub push timestamps (server doc 4.3) | Backdated history (T8, T20), hours with nothing to show | The push time is GitHub's. The commit content is the attacker's |
| Surviving lines | At submission, `git blame` the submitted commit and attribute surviving lines to the sessions whose commits introduced them | Churn and staged struggle (T19); "loss disguised as a win" | Computable by the server for public repos |
| Tests that go green | Exit codes of test commands the human ran in the project terminal (shell integration or the harness's `PostToolUse` for commands the human approved); CI results via GitHub Checks with GitHub's timestamps | Sessions with no verification at all; the handbook's "no meaningful iteration, testing, or refinement" | CI runs on GitHub's machines, but the attacker writes the tests |
| Edits by hand | Editor change events with typed input in the focused editor (signals doc 13.1) | Pure agent output with no human shaping | No |
| Hours per surviving line, per commit and per feature | Against the distribution for similar projects in the same program | Outliers in both directions: 40 h for a to-do app, 2 h for a game engine | Server-side, so yes |

#### Cross-user anomalies

Server-side only, on data already uploaded, so no extra privacy cost beyond retention (B8).

- **Shared artifacts**: the same root commit set, repo, device id or IP plus JA4 fingerprint within a window across accounts. Hackatime's alt tools already do the IP and machine part (sibling doc 1.9).
- **Lockstep activity**: two or more accounts whose credited minutes coincide far more than chance (a script farming several accounts from one machine, or a shared driver).
- **Distribution outliers**: top 0.1% of credited hours per day, injected-input share, automation-label share, re-roll share, backfilled share. Each alone is weak; a submission high on several goes to the front of the review queue.
- **Graph clusters**: accounts linked by shared devices, IPs, repos and reviewers form connected components. Click-fraud detection works the same way (A5): single events look clean, the farm shows in the graph.
- **Duplicate prompt sets across accounts**: only possible if the server can compare prompts. With salted commitments it can't, by design; see B6 for the opt-in sketch that allows it for submitted hours only.

#### Code similarity (T16)

Run at review time on public repos only, against the Unified DB's Code URLs for the same and other programs:

| Tool | License | Version seen | Notes |
|---|---|---|---|
| [JPlag](https://github.com/jplag/JPlag) | GPL-3.0 | v6.3.0 (2025-12-11) | Token-based, 17+ languages. Since v5.0, token-sequence normalization resists automated insertion and reordering: on PROGpedia with automatically obfuscated plagiarism, median similarity of plagiarized pairs was 100% for JPlag, 26.7% for Dolos and 7.5% for MOSS ([Sağlam et al., ICSE-Companion 2024](https://doi.org/10.1145/3639478.3643074)) |
| [Dolos](https://github.com/dodona-edu/dolos) | MIT | v2.10.0 (2026-10-19) | Tree-sitter based, web UI, easy to run as a service |
| MOSS | Free service, closed source | n/a | Winnowing fingerprints ([Schleimer, Wilkerson and Aiken, SIGMOD 2003](https://doi.org/10.1145/872757.872770)); with GPT-J's help, students can solve tasks "without triggering suspicion from MOSS" ([Biderman and Raff, CIKM 2022](https://arxiv.org/abs/2201.07406)) |

An LLM asked to rewrite a copied project defeats MOSS and probably weakens JPlag too (JPlag's published resilience covers automated insertion and reordering, not full LLM rewrites; unverified). Code similarity catches lazy copying. Reconstruction (T16b) is caught, if at all, by the timeline (the code appears with less human time than its size implies) and by the interview.

### A5. Lessons from game anti-cheat, proctoring and click fraud

| Field | What they learned | Source | Transfers to pairtime as |
|---|---|---|---|
| Game anti-cheat: VACnet and Overwatch (CS:GO) | A model flags, humans decide. VACnet scored sequences of 140 shots (view-angle deltas around each shot) and submitted high-confidence suspects to Overwatch, where player jurors review an anonymized 8-round demo. Reported conviction rates: 15-30% for human-submitted cases, 80-95% for VACnet-submitted ones. Juror verdicts become the next training labels | John McDonald, GDC 2018 talk "Robocalypse Now" ([video](https://www.youtube.com/watch?v=kTiP0zKF9bc)), as summarized by [Edgegap](https://edgegap.com/blog/anti-cheat-deep-dive-counter-strike-global-offensive) (secondary; figures not checked against the video) | Detectors rank the review queue; they never reject. Reviewer decisions are the labels the detectors are fitted to (prior-art doc 3.5) |
| Game anti-cheat: Trust Score | A trust score predicted from account history "doesn't reduce the rate of cheating" but limits "the blast radius" | Same source (secondary) | Trust tiers decide how much evidence a submission needs, not whether the person is guilty |
| Game anti-cheat: secrecy | Publicly revealing a detection method invalidated it | Same source (secondary); Hack Club says the same (A2) | Keep detection thresholds and models server-side and unpublished (A8 #9) |
| Game anti-cheat: kernel drivers | Riot moved Vanguard into the kernel because "an abundance of cheats currently run at a higher privilege level than our anti-cheat does", and had to argue publicly that "This isn't giving us any surveillance capability we didn't already have" after a backlash | [Riot, "/dev/null: Anti-Cheat Kernel Driver"](https://www.leagueoflegends.com/en-us/news/dev/dev-null-anti-cheat-kernel-driver/), 2020-02-03 | The arms race ends in the kernel, and pairtime can't go there: privacy-conscious users (R6) won't install it, macOS and Linux won't allow it in the same way, and an AGPL client can be rebuilt without it anyway. Accept that client checks are cost-raisers |
| Proctoring | Of 30 computer science students, 6 were told to cheat and 5 to act nervous while taking the exam honestly. Proctorio flagged none of the 6 cheaters; manual review of the recordings found 1. The authors rate its sensitivity "very close to zero" and compare it to "taking a placebo": the benefit is that students believe it works | [Bergmans, Bouali, Luttikhuis and Rensink, CSEDU 2021](https://www.scitepress.org/PublishedPapers/2021/103996/) | Heavy surveillance (camera, screen recording, room scans) has a high privacy cost and low efficacy against a motivated cheater. Don't make it the default. Deterrence comes from the expectation of review, so tell users plainly that submitted hours are sampled and interviewed |
| Assessment design in the AI era | The University of Sydney's "two-lane" model: Lane 1 is "Secured, in person" ("interactive oral assessments; viva voces"), used mainly at program level; Lane 2 is open, with AI allowed. Most assessment is expected in Lane 2 | [Teaching@Sydney FAQ](https://educational-innovation.sydney.edu.au/teaching@sydney/frequently-asked-questions-about-the-two-lane-approach-to-assessment-in-the-age-of-ai/) (posted 2024-07-02, revised 2026-04-21) | Most hours go through automated evidence plus deflation (open lane). A risk-weighted sample goes through a live conversation with a reviewer (secure lane). Fraud Squad's "We want to talk to you" is already Lane 1 |
| Click fraud: the MRC standard | Two tiers. General invalid traffic (GIVT) is filtered with "list-based common filtration procedures and parameter-based techniques" that every vendor must apply. Sophisticated invalid traffic (SIVT) needs "advanced analytics, multi-point corroboration/coordination, significant human intervention", and explicitly includes "Incentivized human invalid activity" such as click farms of real people | [MRC Invalid Traffic Detection and Filtration Standards Addendum](https://www.mediaratingcouncil.org/sites/default/files/Standards/IVT%20Addendum%20Update%20062520.pdf), June 2020 update | Tier 1: deterministic labels for known automation (harness flags, injected input, headless entrypoints), applied automatically and publicly documented. Tier 2: multi-signal scoring plus review for the rest, kept private. "Incentivized human" fraud (paid typists, account sharing) is a named category that only tier 2 can touch |
| Click fraud: 3ve | 3ve controlled "over 1.7 million unique Internet Protocol (IP) addresses", ran a hidden browser on infected home PCs, and relayed data-center traffic through infected bots "in an effort to hide their true data center IPs" | [CISA alert TA18-331A](https://www.cisa.gov/news-events/alerts/2018/11/27/3ve-major-online-ad-fraud-operation), 2018-11-27 | Sophisticated fraud runs on real consumer machines, so per-event checks pass. It shows up in aggregate: shared infrastructure, lockstep timing, graph clusters (A4 cross-user) |
| Employee monitoring ("bossware") | Wells Fargo fired more than a dozen wealth-management employees in 2024 over "simulation of keyboard activity creating impression of active work" (FINRA disclosures). Mouse jigglers cost about $20 and key-pressing gadgets about $60; the EFF notes such monitoring rewards "the wrong kinds of productivity" | [CBS News, 2024-06-17](https://www.cbsnews.com/news/wells-fargo-fires-employees-faking-work/), citing Bloomberg | Measuring presence invites cheap presence fakes, even among adults with careers at stake. Credit human acts and outcomes, not activity (Design implication 1) |
| Crowdsourcing quality control | Hidden "gold" tasks with known answers measure each worker; "verifiable questions must be included into otherwise subjective tasks and answering the verifiable part correctly should take as much effort as doing the whole task" (Kittur, Chi and Suh 2008, as summarized there) | [Oleson et al., "Programmatic Gold", AAAI HCOMP 2011](https://cdn.aaai.org/ocs/ws/ws0800/3995-16701-1-PB.pdf) | Two uses. Seed the review queue with known-answer submissions to measure reviewers (T18). And design the verifiable part of the evidence (random reveal against commitments, B6) so that faking it costs about as much as doing the work |

Three principles come out of all four fields:

1. **The client is the attacker's.** Every field that tried to win on the client ended up in the kernel (anti-cheat) or the webcam (proctoring), and lost on privacy, false positives, or both. Pairtime should put its trust in what the client can't control (A1 table).
2. **Cheap deterministic filters plus expensive human judgment**, with a model in between that only decides who gets looked at.
3. **Deterrence is a product of probability and penalty, and the probability must be visible.** Proctoring's only measurable benefit was perceived risk. Pairtime should make the sampling and interview rate public, and keep the detectors private.

### A6. Attacks no design can stop, and how sampling and review handle them

| ID | Attack | Why no tracker can stop it |
|---|---|---|
| U1 | **Someone else at the keyboard**: an older sibling, a parent, a paid freelancer, a friend (T17b) | Every signal is a real human's. A local typing profile can notice a change; it can't say who |
| U2 | **Performative struggle**: the teen is present, typing real prompts and reading output, but padding or doing low-value work (T13, T19, and R9's "not pushing themselves") | Engagement is real; only its value is in question, and value is a judgment |
| U3 | **The full puppeteer**: an LLM writes prompts, a hardware HID device (or a VM's virtual keyboard) types them with timings trained on the user's own recordings, a real agent builds a real project, and real commits go to GitHub | Every per-event signal reads human. What's left is content, consistency over weeks, and the person's ability to explain the work |
| U4 | **Reconstructing a project from outside Hack Club**, paraphrased by an LLM (T16b) | Code similarity fails after a rewrite (A4); the timeline is plausible if U3 also runs |
| U5 | **Collusion at the top**: a reviewer, a program author, or the team running a program (Zach allows running your own programs, BOUNTY.md) | A governance problem. Only HQ spot-checks and outside audit reach it |
| U6 | **Fabricated offline sessions**: time claimed while "offline", invented after the fact with commits made later | Nothing witnesses offline time. It can be bounded, not verified |

**Handling them: bound the gain, sample the claims, interview the sample.**

1. **Bound the gain per account.** Cap credited hours per day (Stardance caps a devlog at 10 paid hours, sibling doc 3.3) and cap uncommitted and offline time per submission. A cap turns an unbounded attack into a bounded one, and the bound is what the sampling rate has to beat.
2. **Pay after review, claw back on fraud.** Stardance already deducts paid currency after an integrity deduction, and a failed HQ spot-check removes the project from the Unified DB (sibling doc 3.3, 3.7). Pairtime's server should keep every submitted hour revocable for the spot-check window.
3. **Risk-weighted random audits.** Each submission gets a deep-audit probability `p = base + risk`, where risk rises with claimed hours, flags, trust tier, first submission and backfilled share. Make the base rate public (A5 principle 3); keep the risk model private.
4. **The deep audit is a short live conversation** (Sydney's Lane 1, Fraud Squad's "We want to talk to you"): walk through two server-chosen commits, explain one design decision, and make one small change live on a screen share. A teen who built the project with an agent can do this. Someone who never touched it usually can't. Pair it with the server-chosen random reveal of committed prompts (B6).
5. **Penalties propagate.** A confirmed fraud flag in pairtime should reach Fraud Squad (Robert, sibling doc 3.5), so the penalty is Hack Club-wide, as Jackpot's rules already say ("banned from Hackatime and other participating YSWS / events / programs").

**Sizing the audit rate.** A fake claim with value `g` succeeds with probability `1 − p·q`, where `q` is the chance an audit catches it, and fails with loss `L` (forfeited prizes plus a Hack Club-wide ban). Fraud stops paying when `p·q > g / (g + L)`. Example: a fake 40-hour project is worth `g` ≈ $200 at $5 per hour (A1). If the ban costs the teen `L` ≈ $1,000 in future prizes and trips, and a live interview catches a puppeteer `q` ≈ 0.6 of the time (my guess; measure it), then `p` > 0.28 is needed. For a 10-hour claim (`g` ≈ $50), `p` > 0.08 suffices. So the audit rate should rise with claimed hours. These inputs are guesses; the formula is the point, and the inputs should be re-estimated from audit outcomes.

**What it costs reviewers.** At the acceptance target (about 1,000 weighted projects, 10,000 hours), auditing 15% of projects for 15 minutes each is 150 × 0.25 = 37.5 reviewer-hours in total. Stardance reviewers spent far more than that deflating 123,400 hours by hand. Auditing a sample deeply is affordable only if the other 85% need almost no reviewer time, which is what the evidence in A7 is for.

**Due process.** No detector bans anyone. A flag changes the review path and the evidence required. The teen sees which category of evidence was missing ("most prompts in this window had no typing before them") without the threshold, can add evidence, and can appeal to a person. Heliodex's account (A2) shows what happens without this: frozen accounts, missed deadlines, and a community that assumes the process is unfair.

### A7. Minimum evidence to approve an hour

A credited hour can be approved without an interview when all of the following hold. Each item names what the reviewer sees.

| # | Requirement | What the reviewer sees | If it fails |
|---|---|---|---|
| E1 | **Witnessed.** At least 90% of the hour's credited minutes are covered by chain heads the server received within 15 minutes, or the hour is marked offline and corroborated by commits pushed within 24 hours | Witness lag per minute; offline spans | Backfilled minutes need corroboration or are deflated |
| E2 | **Human evidence per minute.** Each credited minute contains a qualifying human act (a prompt typed in the focused harness window, a permission decision, an interrupt, a hand edit in the project, a command typed in the project's terminal, a diff reviewed in the editor) or falls in a capped reading or think window that such an act opened. Injected input and mouse-only input don't qualify | Activity mix per minute (prompting, reading, reviewing, testing, debugging, editing by hand); human-evidence coverage %; injected-input share | Credit only covered minutes |
| E3 | **One project, once.** The minutes are credited to the submitted project only, not to another project, device, account or program | Overlap check result | Overlapping minutes are removed |
| E4 | **Attached to work.** The hour falls in a commit interval ending in a commit reachable from the submitted commit (forge-verified for public repos), or in the capped "after the last commit" bucket | Commit, diffstat, witnessed and verified marks; surviving lines | Uncommitted time over the cap is deflated |
| E5 | **Automation separated.** Agent-only and automated spans are excluded and labeled | Agent time vs credited human time; automation labels | Already excluded by the credit rule |
| E6 | **Sample revealed.** If the server's random challenge picked any committed item in this hour, the user revealed it and it verified against the commitment | Revealed prompts (redacted), with "verified" marks | Unrevealed hour is not credited |
| E7 | **No open high-severity flags** | Flag list with reasons | Goes to the audit path (A6) |
| E8 | **The project works** ("The project must do what it says it does", vision post). Checked once per submission | Demo link, reviewer note | Whole submission returned |

The output per submission is what programs already file (sibling doc 3.7): a credited number with a deflation justification, generated from E1-E8, plus a pairtime review URL for the handbook's "Alternate Tracking Method" and "Additional Justification" fields.

### A8. Ranked v1 defenses

Ranked by fraud and inflation removed per unit of engineering and privacy cost.

| Rank | Defense | Stops or bounds | Where |
|---|---|---|---|
| 1 | **Credit only human-evidenced minutes**: agent-only spans and mouse-only input earn 0; reading and think windows capped; one human-minute per wall-clock minute per account across devices and projects | T1, T2, T3, T7, T11a, T12, T14 for free, and most of R9's inflation | Client scoring, server dedupe |
| 2 | **Witnessing**: hash-chained local log, chain head to the server every 5 minutes, server receipt time as the clock, "backfilled" tier for anything late (B6) | T8, T20; bounds lookback and U6 | Client + server |
| 3 | **Commits as anchors**: witnessed and forge-verified commits, time attached to commits, uncommitted time capped (server doc 4.3-4.4) | T9, T16a, T19; makes review fast | Client + server |
| 4 | **Public tier-1 automation labels**: harness flags, headless entrypoints, process tree, injected-input flags, Caffeine-style periodic keys (the MRC GIVT tier) | T1-T5 commodity tools | Client |
| 5 | **Opt-in typing evidence**: a keyboard listener, off by default, that reads injected-input provenance (A4, per OS) and computes per-minute hold-time median and rollover share in memory; injected or suspicious minutes don't count as typing; no keystroke timestamps or key identities stored. Minutes without it are "typing unknown", not suspect | T4, T5, part of T6 | Client |
| 6 | **Server risk score**: overlaps, alt candidates, shared root commits, client build and JA4, distribution outliers, graph clusters; orders the review queue and sets audit probability | T9, T11b, T12, T16a, T17a, T17c | Server, private module |
| 7 | **Review protocol**: deflate by default, server-chosen random reveal, risk-weighted live interviews, pay after review, revocable hours, penalties shared with Fraud Squad | U1-U4, T6, T10, T21 | Review tooling |
| 8 | **Reviewer integrity**: random assignment instead of claim queues, conflict-of-interest rules, seeded known-answer cases, per-reviewer statistics, access logs | T18, U5 (partly) | Server |
| 9 | **Open core for detection**: tier-2 thresholds and models in a private module run as a separate process (the Ari pattern, sibling doc 3.6); tier-1 rules public | Keeps tier 2 from being tuned against | Server |
| 10 | **Local re-roll and dark-flow detection** (MinHash), shown to the user | T15; mostly accuracy (R2) and motivation (R3) | Client |

Deferred past v1: VM labels (T10, high false-positive risk), input device inventory (T6), per-user typing profiles (T17b, T21), JPlag at review time (T16; Hack Club may already run similar checks), and opt-in Lapse-style timelapse for high-value submissions (sibling doc 4).

## Part B. Privacy

### B1. What privacy-conscious developers accept and reject

| Product | What it does | The reaction | Lesson for pairtime |
|---|---|---|---|
| **WakaTime** (wakatime-cli `develop` at `7ab26b5db6fe`, 2026-11-05, `USAGE.md`) | Sends file paths, project, branch, language and dependencies with every heartbeat. Privacy switches exist but all default to `false`: `hide_file_names` ("Will not send file names to api"), `hide_project_names` (writes a `.wakatime-project` file with a random name), `hide_branch_names`, `hide_dependencies`, `hide_project_folder` (send paths relative to the project), `exclude`/`include` regexes, `include_only_with_project_file`, `exclude_unknown_project`, and the new `exclude_ai`. Offline queue on by default at `~/.wakatime/offline_heartbeats.bdb` | Widely used, but the privacy-conscious have to opt into each switch, and Hackatime inherits the same defaults: a folder named `college-essay` appears on a public profile ([server-platform.md](server-platform.md) 5.1) | Invert the defaults: names and paths never leave the machine unless the user publishes a project. Keep a WakaTime-style `exclude` list and a per-folder opt-out file (`.pairtime-ignore`) because people expect them |
| **ActivityWatch** ([FAQ](https://docs.activitywatch.net/en/latest/faq.html), latest docs, fetched 2026-11-19) | Window titles, app names and AFK state in a local SQLite database; "local-first", data "never sent to any server"; on Linux, AFK detection records only that input happened, not what it was | The reference for privacy-first tracking among developers; its weakness is that there is no server, so nothing is shareable or auditable | Its local model is the right home for raw data (Tier 0, B6). Pairtime adds a server only for derived numbers and commitments |
| **VS Code telemetry** ([docs](https://code.visualstudio.com/docs/configure/telemetry), dated 2026-11-18) | `telemetry.telemetryLevel` = `all`, `error`, `crash` or `off`, collected unless changed; machine id from "a hashed Mac Address"; extensions "may be collecting their own usage data" outside the setting; `code --telemetry` lists every event the build can send | [VSCodium](https://github.com/VSCodium/vscodium) exists largely because Microsoft's build is under a "not-FLOSS license and contain[s] telemetry/tracking"; its builds state "Telemetry is disabled" | People respect a published event list (`code --telemetry`) and resent default-on collection plus hardware-derived identifiers. Ship `pairtime privacy show` that prints every field the client can upload; use a random install id, never a hardware hash |
| **Go toolchain telemetry** ([go.dev/doc/telemetry](https://go.dev/doc/telemetry); [Russ Cox, 2023-02-24](https://research.swtch.com/telemetry-opt-in)) | Proposed opt-out in early 2023; after feedback Cox "revised the design to be opt-in (default off)". Shipped with mode `local` as the default ("collected and stored on the local computer, but never uploaded"), `on` to upload weekly counter reports, `off`. Counters only; new uploaded counters go through a public proposal process; uploaded data is published at telemetry.go.dev; uploads are sampled "to reduce collection amounts and increase privacy" | The most-quoted reader comment: "this is the first system I've seen that I'd actually opt in to, but I'd still turn it off if it was on by default." Cox expected opt-in rates of "a few percent" to 20% | The closest model to what pairtime needs. Default: compute locally and show the user. Upload only named, reviewed, aggregate fields. Publish the schema. Treat any new uploaded field as a public change |
| **GitHub Copilot** ([GitHub blog, 2026-03-25](https://github.blog/news-insights/company-news/updates-to-github-copilot-interaction-data-usage-policy/); [settings docs](https://docs.github.com/en/copilot/how-tos/manage-your-account/managing-copilot-policies-as-an-individual-subscriber)) | From 2026-04-24, "interaction data—specifically inputs, outputs, code snippets, and associated context" from Copilot Free, Pro and Pro+ users trains models unless the user sets "Allow GitHub to use my data for AI model training" to "Disabled". "Copilot Business and Copilot Enterprise users are not affected". For Business and Enterprise, IDE prompts and suggestions are not retained and other surfaces keep them 28 days (per [Morse's summary of GitHub's FAQ](https://trymorse.ai/blog/does-github-copilot-train-on-your-code), 2026-11-01, secondary) | Default-on training on prompts drew wide coverage and an official FAQ thread ([community discussion 188488](https://github.com/orgs/community/discussions/188488)) | Teens' prompts already go to model vendors, often under training-by-default terms. Pairtime must not become another place that keeps them. Raw prompts stay on the device, with no exceptions in the default path |
| **Windows Recall** ([Microsoft, 2024-09-27](https://blogs.windows.com/windowsexperience/2024/09/27/update-on-recall-security-and-privacy-architecture/); [Signal, 2025-05-21](https://signal.org/blog/signal-doesnt-recall/)) | The first preview kept OCR'd screen text in a SQLite database that Kevin Beaumont showed could be stolen with "two lines of code" ([Simon Willison's summary, 2024-06-01](https://simonwillison.net/2024/Jun/1/stealing-everything-youve-ever-typed/)). The relaunch: "Recall is always opt-in", removable, snapshots "encrypted by individual keys and those keys are protected within the VBS Enclave", Windows Hello to view, filters that "help reduce passwords, national ID numbers and credit card numbers from being stored", app and site exclusions, retention controls | Signal turned on "Screen security" (a DRM flag) by default on Windows 11 so Recall can't capture its window, because Microsoft gave apps no finer opt-out | Screen capture is the most rejected form of tracking among privacy-conscious users. Lapse-style timelapse can only ever be opt-in evidence for specific submitted hours. A local store of sensitive activity is a target for infostealers: encrypt it at rest with an OS-protected key (DPAPI on Windows, Keychain on macOS, Secret Service on Linux), keep it short-lived, and keep content out of it where possible |

**What privacy-conscious developers accept**, pulling the table together:

- Local collection that the user can inspect, with a short published list of what leaves the machine.
- Aggregates and counters, not content.
- Opt-in for anything content-bearing, asked once, with two equal buttons (Cox's "no click-through default").
- Random install ids, not hardware hashes.
- Encryption at rest, deletion that works, and a clear retention period.
- An open-source client whose upload code can be read, which pairtime gets from R1.

**What they reject**: screen capture by default, keystroke content, default-on reuse of prompts or code, kernel drivers (A5), hardware fingerprints, and anything that sends file or project names without asking.

### B2. Obligations for users aged 13 to 18

Not legal advice. This is what an engineer needs to know to avoid designing something counsel will have to tear out. Who the legal operator or controller is (Hack Club HQ, Karim personally, or a program) changes which of these apply; that is an Open question.

| Regime | Who it protects | What it requires that touches pairtime's design | Status (checked 2026-11-19) |
|---|---|---|---|
| **COPPA** (US, 16 CFR Part 312) | Under 13, on services "directed to children" or with actual knowledge of an under-13 user | Verifiable parental consent before collection; separate consent before disclosure to third parties; a written retention policy and no indefinite retention ("for as long as is reasonably necessary to fulfill the specific purposes for which it was collected", §312.10); a written children's information security program; "personal information" now includes biometric identifiers | Amendments published 2025-04-22, effective 2025-06-23, full compliance by 2026-04-22 ([FTC](https://www.ftc.gov/legal-library/browse/federal-register-notices/16-cfr-part-312-coppa-final-rule-amendments); details from [Latham & Watkins, 2025-05-14](https://www.lw.com/en/insights/ftc-publishes-updates-to-coppa-rule)) |
| **GDPR Art. 8** (EU) | Children below the national "digital age of consent", only where consent is the lawful basis for an information society service | Parental consent below that age. The age is 16 by default and 13-16 by country: 13 in Belgium, Denmark, Estonia, Finland, Latvia, Malta, Portugal, Sweden; 14 in Austria, Bulgaria, Cyprus, Italy, Lithuania, Spain; 15 in Czechia, France, Greece; 16 in Croatia, Germany, Hungary, Ireland, Luxembourg, Netherlands, Poland, Romania, Slovakia, Slovenia | Ages from [euCONSENT](https://euconsent.eu/digital-age-of-consent-under-the-gdpr/), dated 2021-10-26; Slovenia had a bill to lower it to 15, so re-check before launch. Basing the core tracking on contract or legitimate interest instead of consent avoids Art. 8, but the rest of GDPR (minimisation, Art. 25 by design and by default, rights) still applies |
| **UK GDPR and the Children's Code** (Age Appropriate Design Code) | Under 18 (the code's scope; unverified here because ico.org.uk returned 403 to my fetches) on services "likely to be accessed by children" | Std 7: "Settings must be 'high privacy' by default for children." Std 8: "Only collect and retain the minimum amount of personal data you need from children." Std 10: geolocation off by default with "an obvious sign" when tracking. Std 12: profiling "off" by default. Std 13: no nudges "to encourage children to provide unnecessary personal data or turn off privacy protections." Parental authorisation for consent-based processing under 13 ([GOV.UK summary](https://www.gov.uk/guidance/child-online-safety-data-protection-and-privacy), revised 2021-09-27) | In force since 2021. The Data (Use and Access) Act 2025 s. 81 adds UK GDPR Art. 25(1A)-(1B): controllers of services "likely to be accessed by children" "must take into account the children's higher protection matters", including that children "have different needs at different ages and at different stages of development"; in force 2026-02-05 ([legislation.gov.uk](https://www.legislation.gov.uk/ukpga/2025/18/section/81)) |
| **California** | Under 16 (CCPA sale/share), under 18 (AADC, minors' eraser law) | CCPA: no selling or sharing of under-16s' data without opt-in. The CA Age-Appropriate Design Code Act: on 2026-03-12 the Ninth Circuit left the DPIA duty, the profiling and data-minimisation limits and the dark-patterns ban enjoined, but lifted the injunction on "configure default privacy settings for children to a high level of privacy", the geolocation signal, age-suited notices, rights tools and age estimation ([Cooley, 2026-03-24](https://www.cooley.com/news/insight/2026/2026-03-30-netchoice-v-bonta-ninth-circuit-narrows-injunction-against-californias-ageappropriate-design-code-act)). Both CCPA and the AADC apply to a "business", which CCPA defines as an entity operated "for the profit or financial benefit of its shareholders or other owners" (Cal. Civ. Code §1798.140(d)), so a nonprofit like Hack Club is probably outside both (unverified; counsel). The minors' eraser law (Bus. & Prof. Code §22581) lets registered users under 18 remove content they posted, and isn't limited to businesses (unverified; the statute page returned 403) | The **Digital Age Assurance Act (AB 1043)**: from 2027-01-01 for new accounts (existing accounts by 2027-07-01), OS providers collect age at account setup and give apps an age-band signal, and "developers are deemed to have 'actual knowledge' of a user's age range once they receive those signals" ([FKKS](https://fkks.com/?passle-post=102o301)). Whether it reaches desktop Windows, macOS and Linux apps like pairtime is unverified |
| **New York Child Data Protection Act** (N.Y. Gen. Bus. Law §§ 899-ee to 899-mm) | Under 18 in New York, when the operator actually knows the user is a minor or the service is "primarily directed to minors" (§ 899-ee(1)); a Hack Club-run pairtime is plainly the second | For ages 13-17, no processing unless "strictly necessary" for a listed purpose or with informed consent (§ 899-ff(1)). The purposes include "providing or maintaining a specific product or service requested by the covered user" and "protecting against malicious, fraudulent, or illegal activity"; "internal business operations" excludes "research and development" (§ 899-ff(2)). A consent request must be separate from any other transaction, say that the user "may decline without preventing continued use", and "clearly present an option to refuse to provide consent as the most prominent option"; after a refusal, no new request "for the following calendar year" (§ 899-ff(3)). An operator "may not withhold, degrade, lower the quality, or increase the price" of a feature because consent was refused (§ 899-ff(4)). No buying or selling of the data (§ 899-ff(5)) | In effect since 2025-06-20; the Attorney General's guidance (last updated 2025-05-19) says OAG will weigh "good-faith efforts to comply" until rules are final ([ag.ny.gov](https://ag.ny.gov/child-data-protection-act-guidance)). "Operator" means "any person who operates or provides" a service ([§ 899-ee](https://www.nysenate.gov/legislation/laws/GBS/899-EE), version 2025-06-27) and names no nonprofit exemption, so it probably reaches Hack Club (unverified; counsel) |
| **Vermont Age-Appropriate Design Code** (9 V.S.A. § 2449a ff., Act 63 of 2025) | Under 18 in Vermont, where Hack Club is based | Applies only to a "covered business" that, among other tests, "generates a majority of its annual revenue from online services" ([§ 2449a](https://legislature.vermont.gov/statutes/section/09/062/02449a)) | Effective 2027-01-01. A donor-funded nonprofit probably fails the revenue test, so it likely doesn't apply (unverified; counsel) |
| **India: DPDP Act 2023 and DPDP Rules 2025** | Under 18 ("an individual who has not completed the age of eighteen years", s. 2(f)) | s. 9(1): "verifiable consent of the parent" before processing a child's data. s. 9(3): a data fiduciary must not undertake "tracking or behavioural monitoring of children or targeted advertising directed at children", and parental consent does not unlock it. Rule 10 sets how to verify the parent. Exemptions (Rule 12, Fourth Schedule) cover narrow classes such as schools "restricted to their educational activities" | Rules published 2025-11-13; s. 9 and Rules 10-12 start 18 months later, about **2027-05-13** ([dpdprules.org](https://dpdprules.org/blog/dpdp-childrens-data-rules), secondary, sources checked there 2026-10-28). A 2026 Removal of Difficulties Order (S.O. 5458(E)) reportedly touches ss. 9-10; contents unverified |

What follows for the design, whoever turns out to be the operator:

1. **Treat every user as a child for defaults.** Hack Club's programs are for ages 13-18 (B3), so a Hack Club-run pairtime is a service "likely to be accessed by children" almost by definition. The cheapest compliant design is one where the defaults are already the high-privacy ones for everyone, so age doesn't have to be known to get them right. That matches the UK code, the enforceable parts of the CA AADC, and R6.
2. **Under-13s.** Hack Club says users under 13 "MAY NOT USE OR ACCESS HACK CLUB SERVICES" (B3). Pairtime should rely on HCA, which already records birthdate and verification (`verified_eligible` means "Verified and eligible for YSWS (under 18)", [server-platform.md](server-platform.md) 3.1), and refuse accounts HCA marks under 13, rather than collect ages itself.
3. **Retention must be written down and finite** for anything tied to a person (COPPA §312.10 for under-13s; UK Std 8 and GDPR minimisation for everyone). B8 gives a period for every field.
4. **Keystroke timing is close to biometric.** COPPA's new definition lists biometric identifiers, GDPR treats behavioural characteristics used to identify a person as biometric data (Art. 4(14)), and timing leaks content (A4). Keeping cadence features in memory and uploading only scores keeps pairtime out of that category.
5. **India's s. 9(3) is the hardest constraint.** A tracker of engagement is, on its face, "behavioural monitoring". Two features of pairtime's design help: the monitoring runs on the user's own device under their control, and the server receives only aggregates the user's own client computed. Whether that is enough, or whether Indian under-18s need a different mode (local-only, no uploaded activity, submission only with a parent's verified consent) from May 2027, is a question for counsel before launch in India (Open questions).
6. **Profiling off by default.** Fraud scoring is profiling. The UK code permits it only with a compelling reason in the child's interest. Keep risk scoring to submitted hours (where the user has asked for review and there is a compelling reason), and don't score logged-only hours at all.
7. **Sort every processing purpose into "requested service", "fraud protection" or "consent"** (New York). Tracking and the user's own dashboard are the service the user asked for; scoring submitted hours is fraud protection. Everything else needs informed consent from New York minors under § 899-ff(3): the opt-in typing listener (A4) unless counsel agrees it counts as fraud protection, the public profile and leaderboards unless they count as part of the requested service, and above all any use of minors' data to calibrate detectors (Open question 15), because "research and development" is expressly not an internal operation. On each consent screen, "No" is the most prominent button, declining never degrades the product, and a refusal isn't asked again for a calendar year. That is stricter than the "two equal buttons" in B1 and B7, so where the two differ, New York's rule wins.

### B3. Hack Club's privacy policy and Hackatime data

**Hack Club's privacy notice** ([hackclub.com/privacy](https://hackclub.com/privacy/), fetched 2026-11-19; the page shows no date):

- Ages: "Hack Club's core programs are for middle school and high school students (ages 13-18)." "IF YOU ARE UNDER 13 YEARS OF AGE, THEN YOU MAY NOT USE OR ACCESS HACK CLUB SERVICES AT ANY TIME OR IN ANY MANNER." Hack Club "does not knowingly collect or maintain personal information" from under-13s and will delete it if found. Where it learns it is processing data of users under a local age of consent, it will "halt such processing" and delete the records.
- But also: "HCB and Hackatime are available to people of all ages." That sentence sits awkwardly with the under-13 ban; for pairtime the safe reading is the ban.
- Collection includes name, contact details and "identification documents", plus IP, "a unique user ID, device and browser types and identifiers", and usage. Activity is used to "track your entries, submissions, and status in challenges".
- "We do not (and never will) sell your personal data." Vendors named include Slack and Stripe.
- Retention: "for as long as it is required in order to fulfill the relevant purposes", and after account closure and a deletion request "we will delete all information that we are not required or permitted to retain by law."
- Rights (access, correction, deletion, portability, withdrawal of consent) through one form, `https://forms.hackclub.com/t/qjFxdohV4Rus`, which also reaches the Data Protection Officer; response "within 30 days". Controller address: 212 Battery St Ste 3, Burlington, VT 05401.
- It says nothing specific about time-tracking data. The Terms add that "work, submissions, hours, or other progress made on or after their 19th birthday" earns no Challenge credit, which a tracker that feeds programs has to respect.

**Hackatime's own data statement** (`docs/configuration/privacy.md` in hackclub/hackatime at `43bde10b02db`): a heartbeat can carry "the file path or other entity name", project, branch, language, dependencies, editor, OS, machine, plugin, category, timestamps, line counts, "AI coding metadata", and "request metadata such as IP address and user agent". It does not receive "screenshots, a stream of your keystrokes, password values or the contents of the file". Then the key sentence:

> **Warning for Hack Club programme submissions:** Hiding file names or other activity metadata can prevent reviewers from verifying your work and may cause your submission to be rejected.

That is the trade Hackatime forces today: privacy or auditability, chosen per user, for all of their activity at once. Pairtime's tiers (B6) exist to remove that trade: everything private by default, with evidence revealed only for the hours a user submits.

**Hackatime's settings** are two checkboxes, `allow_public_stats_lookup` (default `true`) and `leaderboard_opt_out` ([server-platform.md](server-platform.md) 5.1).

**Deletion and export through Hack Club's privacy hub.** Hackatime implements `POST /api/privacy/delete` and `POST /api/privacy/export` (`config/routes.rb`). The caller sends `Authorization: Bearer <JWT>`. `Privacy::Token.verify` requires `alg` `ES256` with a `kid`, `iss` `hackclub-privacy`, the service's own `aud`, required claims `iat exp jti request_id action dry_run subject`, a lifetime of at most 300 s, and a P-256 key from the hub's JWKS. Each `jti` is single-use (`PrivacyTokenUse.consume!`). `Privacy::Eraser` physically deletes the account's rows across a fixed table list (heartbeats, API keys, rollups, goals, `trust_level_audit_logs`, commits and more) and reports what it had to retain and why (for example "author identity copied in another user's imported commits"). A `dry_run` claim runs the same code and rolls back. Lapse implements the same contract (sibling doc 3.4).

What pairtime should take from this:

1. **Implement the same two endpoints with the same token checks**, so Hack Club's privacy hub can delete or export a pairtime account the way it does Hackatime's. That is the cheapest way to make a Hack Club-hosted pairtime fit the DPO's existing process.
2. **Decide what survives deletion, and say so.** Hackatime's eraser deletes `trust_level_audit_logs`, so a convicted account's history goes with it. Pairtime should keep a minimal fraud record (HCA id, verdict, date, reason category, no activity data) for a stated period after deletion, under the fraud-prevention purpose the Hack Club notice already lists, and report it in the eraser's `retained` output.
3. **Respect the 19th birthday cutoff** when building submissions: hours logged on or after it aren't eligible. HCA's birthdate scope is HQ-only ([server-platform.md](server-platform.md) 3.1), so pairtime should ask HCA for eligibility, not the birthdate itself.

### B4. On-device PII and secret stripping

**Where redaction sits.** In the design in B6, raw prompts never leave the device in the normal path. Redaction matters in three places: (1) anything the client writes outside the encrypted spool (logs, crash reports, the local UI's cached summaries), (2) prompts revealed for submitted hours, and (3) any summary or label derived from prompt text. So the heavy work runs on a handful of revealed items, not on every prompt, which is what makes it affordable on an old laptop.

**Tools, measured from their repos and model cards (2026-11-19):**

| Tool | License | Version | What it is | Size and cost | Fit for pairtime |
|---|---|---|---|---|---|
| [gitleaks](https://github.com/gitleaks/gitleaks) rules (`config/gitleaks.toml`) | MIT | v8.30.1 (2026-03-21) | 222 `[[rules]]`, 130 with an entropy floor, each with keywords for a fast prefilter. Includes `anthropic-api-key` (`sk-ant-api03-[a-zA-Z0-9_\-]{93}AA`), `openai-api-key` (matches the `T3BlbkFJ` marker), `github-pat`, `github-fine-grained-pat`, `aws-access-token`, `discord-*`, `slack-bot-token`, `stripe-access-token`, `huggingface-access-token`, `npm-access-token`, `jwt`, `private-key`, and `generic-api-key` (entropy 3.5) | 98 KB of TOML; regexes are RE2 syntax, so they port as data to Go's `regexp` or Rust's `regex` crate (neither supports lookaround, which RE2 rules avoid) | **Yes, v1.** Run on every string before it is stored outside the spool or revealed |
| [detect-secrets](https://github.com/Yelp/detect-secrets) | Apache-2.0 | v1.5.0 (2024-05-06) | Shannon-entropy plugins for base64 and hex strings, configurable limit 0-8 | Python | Borrow the entropy approach only; gitleaks covers the rest |
| [TruffleHog](https://github.com/trufflesecurity/trufflehog) | AGPL-3.0 | v3.99.9 (2026-11-12) | Hundreds of detectors, many with live **verification** against the provider's API | Go | **No verification**: it would send a teen's leaked key to a third party to test it. Detector regexes are reusable (AGPL is compatible with pairtime's license) |
| [Presidio](https://github.com/data-privacy-stack/presidio) (the `microsoft/presidio` URL now redirects here) | MIT | 2.2.366 (2026-10-27) | PII analyzer: pattern recognizers with checksum validation (credit cards, IBAN, `in_aadhaar_recognizer`, `in_pan_recognizer`, `uk_nino_recognizer`, `us_ssn_recognizer` and dozens more), NER via spaCy `en_core_web_lg` by default (`conf/default.yaml`, which also ignores `ORGANIZATION` as having "many false positives"), plus GLiNER, transformers and LLM recognizers | Python plus a 400 MB default model | **Not in the native client.** Port its pattern recognizers and checksums as data; use Presidio itself in server-side or reviewer tooling if ever needed |
| spaCy English pipelines | MIT | 3.8.0 | NER F-score from each model's `meta.json`: `en_core_web_sm` 0.843, `en_core_web_lg` 0.855, `en_core_web_trf` 0.899 | Wheels: sm 12.8 MB, md 33.5 MB, lg 400.7 MB, trf 457.4 MB; all need a Python runtime | No: the client is one native binary with no Python ([client-platform.md](client-platform.md) 1) |
| [GLiNER](https://github.com/urchade/GLiNER) PII models | Apache-2.0 | GLiNER v0.2.30 (2026-11-02) | Zero-shot NER for arbitrary labels. [`knowledgator/gliner-pii-edge-v1.0`](https://huggingface.co/knowledgator/gliner-pii-edge-v1.0): "60+ predefined PII categories"; precision 78.96%, recall 72.34%, F1 75.50% on its synthetic benchmark (small 76.84%, base 80.99%, large 83.25%) | Edge ONNX files: `model.onnx` 181 MB, `model_fp16.onnx` 91 MB, `model_quint8.onnx` 46 MB (Hugging Face API, 2026-11-19). Needs ONNX Runtime | **Optional, at reveal time only**, as a downloadable component, to highlight names and places for the user to confirm |
| [gline-rs](https://github.com/fbilhaut/gline-rs) | Apache-2.0 | v1.1.0 (2026-08-05) | Rust inference for GLiNER over ONNX Runtime (`ort`) | README: 6.67 sequences/s vs 1.61 for the Python original, with the *large* multitask model on an 8-core i9 | The way to run GLiNER from a Rust client. A Go client would need ONNX Runtime bindings (cgo or `purego`) |

**CPU cost on an old laptop.** I measured nothing (no installs on this machine), so these are estimates to verify on a real 2015-2016 dual-core Windows laptop:

- Regex rules with a keyword prefilter: well under a millisecond per prompt. Run always.
- GLiNER PII edge, uint8: about 45M parameters (181 MB of fp32 weights / 4 bytes), roughly a tenth of the large model gline-rs benchmarked, on a CPU with a quarter of the cores. Plausibly 0.1-1 s per short prompt and a few hundred MB of RAM while loaded (unverified). Fine for revealing 20 prompts once at submission; wrong for the always-on daemon. Run it in a separate short-lived process.

**Pipeline**, in order:

1. **Known-value redaction (exact, high precision, free).** The client knows the exact strings that identify this user: OS account name, home directory, hostname, `git config user.name` and `user.email`, GitHub and HCA usernames, the names of private repos and folders, and a user-supplied deny list (friends' names, school name). Replace them with stable placeholders (`~`, `[HOST]`, `[ME]`, `[PRIVATE_REPO_1]`). Regex can't find a friend's name; the user's own deny list can.
2. **Secrets**: gitleaks rules plus PEM blocks, `KEY=VALUE` lines from `.env`-like content, URLs with credentials (`scheme://user:pass@`) or token-like query parameters. When one fires in a prompt, also tell the user: the secret has already gone to the model provider, so it should be rotated. That is useful for its own sake (R3).
3. **Structured PII** with validators: email, phone (libphonenumber ports exist for Go and Rust), IPv4 and IPv6, MAC, credit card (Luhn), IBAN (mod-97), national IDs with checksums ported from Presidio (US SSN, UK NINO, Aadhaar with its Verhoeff check digit), dates of birth near words like "born" or "DOB".
4. **Names and places (optional, reveal time only)**: GLiNER PII edge on the items being revealed, shown to the user as suggestions.
5. **The user previews and edits** every revealed item, sees exactly what the reviewer will see, and can withhold any item (withheld items count against the hours they support; B6).

Redaction removes spans and never rewrites text. Each removed span becomes a typed marker (`[EMAIL]`, `[NAME_1]`) whose position and length the commitment still proves (B6). Rewriting would break the link between what was committed and what is revealed.

**Assume redaction fails sometimes.** Even good NER misses names (the GLiNER edge model's recall is 72%), so the rest of the design has to limit the damage: reveal a random sample rather than everything, keep revealed evidence behind reviewer sign-in, log every reviewer view, delete revealed evidence when the spot-check window closes (B8), and give the user a "this revealed something it shouldn't have" button that purges an item immediately.

### B5. Asking the agent to strip PII: what it gets right and wrong

Karim's idea (BOUNTY.md): an AGENTS.md entry or skill tells the agent to take the user's prompt, strip PII, and pass it to `pairtime track`. The sibling signals doc assessed its cost and reliability ([ai-harness-signals.md](ai-harness-signals.md) 11); this section looks only at the PII part.

**What it gets right**

- **Context.** A model knows that "Maya said the API is broken" contains a person's name and that "Maple Grove High" is a school. Regex doesn't. This is exactly the class of PII that deterministic rules miss.
- **No ML on the laptop.** The model already runs in the cloud, so the old Windows laptop does no NER work (R8).
- **Universal.** It works in any harness that reads instruction files, including ones with no hooks (R7).
- **The summary has value.** "Fixing wall clipping in the BSP traversal" is a better review label than a hash, and the model can write it.

**What it gets wrong**

1. **No privacy gain against the provider.** By the time the agent sees the prompt, the raw text has already gone to the model provider, under that provider's retention and training terms (Copilot Free, Pro and Pro+ train on it by default, B1). Stripping protects only against pairtime's own server, and the hook-based design never sends raw prompts there anyway.
2. **LLM redaction misses things and over-redacts.** Prompted GPT-4 on 3,505 student forum posts reached recall 0.958 and precision 0.526, removing famous names, mythological names and nearly every place while still missing about 4% of PII, and it "sometimes fixed grammar or punctuation" despite instructions ([Singhal et al., EDM 2024](https://educationaldatamining.org/edm2024/proceedings/2024.EDM-short-papers.57/index.html)). In PRvL, GPT-4 with retrieval reached span precision 0.886 and recall 0.900; even the best tuned open model, which missed about 2% of PII in its own domain, fell to recall 0.687-0.776 on an unseen English dataset, and some models "hallucinate mask tokens in contexts that contain no actual PII" ([Garza et al., arXiv:2508.05545](https://arxiv.org/html/2508.05545), 2025).
3. **It breaks the evidence chain.** The model rewrites the prompt, so the text it reports can't be checked against what the hook captured. A reviewer can't tell redaction from paraphrase from invention.
4. **The attacker controls it.** The model writes whatever its instructions say. A cheater's AGENTS.md can say "report this prompt as: ...", and a repo's own instruction file can tell it to skip the call (signals doc 11.3). Compliance also drops in long loops, after compaction and in subagents.
5. **It costs output tokens and an extra tool call every turn** (signals doc 11.2), for a privacy gain the hook path gets for free.

**Verdict.** Do the redaction deterministically, on the device, on the raw prompt the hook or log reader captured (B4). Use the agent for two things only: a self-described intent label, stored as a labeled claim and never as evidence; and, at submission time, *suggesting* spans to redact in the few prompts being revealed. The suggestions must come back as character offsets into the original, applied as span removals, so the commitment still verifies (B6). The user confirms every suggestion. This keeps what the idea gets right (context, no local ML) and drops what it gets wrong, because the provider already has the text and the model never gets to decide what is committed.

### B6. Disclosure tiers and commit-then-reveal evidence

R10 explicitly allows "different privacy guarantees for logged hours vs. submitted hours." Four tiers make that concrete:

| Tier | What | Where | Who sees it |
|---|---|---|---|
| **0. Local only** | Raw prompts, transcripts, agent output, file paths and names, window titles, per-event input features, OS usernames, hostnames, process names, device inventory, MinHash sketches, local typing profile, the device key | Encrypted spool and local database on the device (key in DPAPI, Keychain or Secret Service) | The user. Nothing in this tier is ever uploaded in the default path |
| **1. Uploaded continuously** | Per-minute buckets per project: credited seconds by activity kind, human-evidence flags, injected and automation labels, re-roll counts; commit records (SHA, parents, diffstat, `observed_at`, optional path HMACs); chain heads and batch roots; client build id, random device id, harness names and versions. Names and URLs only for projects the user made public | pairtime server | The user's own dashboard in real time; aggregates on the public profile (B7); detectors |
| **2. Revealed for submitted hours** | Event metadata for every event in the submission window, verified against the batch roots; a server-chosen sample of prompts with redactions, verified against their content commitments; optional extras the user adds; optional opt-in timelapse | Evidence store, behind reviewer sign-in (HCA, reviewer role) | Assigned reviewers, Fraud Squad, HQ spot-checkers, each view logged |
| **3. Public** | Profile aggregates, public projects, forge-verified commits of public repos ([server-platform.md](server-platform.md) 5.2) | Public pages | Anyone |

**Committing early so the client can reveal later without being trusted.** The goal: when a teen submits hours, a reviewer can check that what is revealed now is exactly what the client recorded then, and the server learns nothing about unrevealed content in the meantime. The construction uses only hashes, HMAC and a Merkle tree (no new cryptography):

```text
Install:  K_dev  = 32 random bytes, stored with DPAPI / Keychain / Secret Service
          K_leaf = HKDF-SHA256(K_dev, info = "pairtime/v1/leaf")
          K_tok  = HKDF-SHA256(K_dev, info = "pairtime/v1/token")

Event i (sequence number s_i), with content split into tokens w_1..w_n (whitespace-separated, separators kept):
  t_ij = leaf( HMAC(K_tok, s_i || j) || w_j )            per-token salted leaf
  C_i  = MerkleRoot(t_i1 .. t_in)                         content commitment
  E_i  = canonical CBOR { s_i, t_client, kind, harness, session_h, project_h,
                          len_chars, typed_chars, pasted_chars, flags, C_i }
  L_i  = leaf( HMAC(K_leaf, s_i) || E_i )                 salted event leaf

Every 5 minutes, batch b on this device:
  R_b  = MerkleRoot(L_i for the batch's events)
  H_b  = SHA-256( 0x03 || H_(b-1) || R_b || n_b || b )    hash chain across batches
  POST {device_id, b, n_b, R_b, H_b}  ->  server stores it with received_at
```

`leaf(x) = SHA-256(0x00 || x)` and interior nodes are `SHA-256(0x01 || left || right)`, as in Certificate Transparency's Merkle tree ([RFC 9162](https://www.rfc-editor.org/rfc/rfc9162) section 2.1). The per-token tree is the classic redactable-signature construction ([Johnson, Molnar, Song and Wagner, "Homomorphic Signature Schemes", CT-RSA 2002](https://people.eecs.berkeley.edu/~daw/papers/hom-rsa02.pdf)), used here as a commitment rather than a signature.

Why each piece is there:

- **Salts on every leaf and token.** Short prompts ("yes", "continue", "fix it") and event metadata are guessable. Without salts, the server could confirm guesses against the roots, or against the sibling hashes that come with any inclusion proof. HMAC under a device key gives each leaf its own salt, so revealing one item tells nothing about the others.
- **The chain and `received_at`.** The client can't add, drop, reorder or edit events after the batch is uploaded. Anything first seen late is "backfilled" (A3, T8).
- **Per-token commitments.** The user can hide a span (an email, a friend's name) and still prove the rest of the text is exactly what was typed. The reviewer sees where hidden spans are and how many tokens each covers.

**Reveal at submission**, for the window W the user submits:

1. The client sends `E_i` and its leaf salt for every event in W, with the batches' leaves. The server recomputes each `R_b`, checks it against the stored roots and chain, and records each event's witness lag. Event metadata for W is now visible to reviewers: kinds, times, lengths, typed vs pasted counts, flags. Content is not.
2. Only then does the server draw a challenge: `k` content-bearing events chosen by a server CSPRNG (start with `k = max(10, 10% of prompts)`, capped at 30). Because the draw comes after step 1, the client can't prepare only the items it knows will be checked.
3. For each challenged event, the client reveals the token values and salts for the tokens it keeps, and only the leaf hashes for redacted spans, plus a type marker for each span. The server recomputes `C_i` and checks it matches the `C_i` inside `E_i`.
4. A challenged item the user won't reveal is allowed, but the minutes it supports are not credited (A7, E6).

If a fraction `f` of a submission's prompts are bad (fabricated, re-rolled, LLM-written), a sample of `k` contains at least one with probability `1 − (1 − f)^k`: for `f` = 0.3 and `k` = 10 that is 0.97. The reveal catches later editing and content-free fabrication mechanically (the content must match a commitment made at the time); judging whether a revealed prompt is real struggle is the reviewer's job.

**What commitments do not do.** They prove that a record existed at a time and hasn't changed. They don't prove that it describes something real: a modified client fabricating events live (T9) commits to its fabrications on time. Commitments raise the cost of fabrication, since it has to happen live with content behind every event, and they make the sample checkable. The rest is A4-A7.

**Not trusting the server either.** R10 asks for audit "by an external party". A pairtime server could, in principle, insert or backdate roots. Once a day the server should publish one Merkle root over every `(device_id, b, H_b, received_at)` it received that day to a public append-only place: a commit to a public git repository is enough, and a transparency log such as Sigstore's Rekor is an option. An auditor can then check any submission's roots against the published daily root. The cost is one small commit a day.

**Cost (R11, R8).** One upload of about 150 bytes per active device every 5 minutes, and a few hashes per event on the client. Revealing a submission costs one request per batch plus `k` content reveals.

**Deletion.** Deleting the user's account deletes the stored roots; deleting `K_dev` on the device makes the commitments unopenable ("crypto-shredding"). Neither needs the server to have seen any content.

### B7. Public-by-default profiles (R5) vs privacy for minors

R5 asks for "public-by-default user profiles". Three rules pull the other way for a service whose users are 13 to 18:

- GDPR and UK GDPR Art. 25(2): by default, "personal data are not made accessible without the individual's intervention to an indefinite number of natural persons" ([gdpr-info.eu](https://gdpr-info.eu/art-25-gdpr/)). A public profile is exactly that kind of access. This applies at every age, not just to children.
- UK Children's Code Std 7: "Settings must be 'high privacy' by default for children", unless there is a compelling reason in the child's best interests. Std 13 bans nudging children to switch protections off (B2).
- California AADC: "configure default privacy settings for children to a high level of privacy" is no longer enjoined (B2), if the law applies to the operator at all.

The sibling server doc already proposes a visibility model in which only aggregates are public by default and anything that names a thing is public only if it is already public elsewhere ([server-platform.md](server-platform.md) 5.2). Seen from the minors' side, that model needs four additions:

1. **An explicit choice at onboarding.** Show the actual profile preview and two buttons, "Public profile" and "Private profile", with nothing preselected (Cox's "no click-through default", B1). If counsel treats the public profile as consent-based processing under New York's CDPA (B2 point 7), "Private profile" must be the more prominent of the two for New York minors, and the simplest way to comply is to make it the more prominent one for everyone. That choice is the "individual's intervention" Art. 25(2) asks for, and it keeps R5's spirit: most teens will choose public, as most Hackatime users leave it on. If Hack Club decides R5 means literally pre-public, then it should be public for the aggregate-only page, and the decision should be written up as the Children's Code's "compelling reason" assessment (Open questions).
2. **No routine on the public page.** Public numbers are daily totals with no hour-of-day heatmap, and the public view updates at most hourly. An hour-by-hour activity graph of a 15-year-old shows when they sleep and when they are at school. The owner's own dashboard stays real-time (R5's "updates in real-time" is about the dashboard). Hackatime's "currently hacking" list (users active in the last 5 minutes, server doc 5.4) is live presence of minors and should be opt-in.
3. **Nothing that locates a person.** No real name (handles can be pseudonymous; HCA keeps the legal identity), no school, no city, no age, no timezone shown. Std 10 asks for geolocation off by default, and a timezone plus a school name is close enough to geolocation.
4. **No public trace of review.** Flags, trust tiers, deflations and rejected hours are never public (Hackatime masks `yellow` as `blue`, sibling doc 1.9). A public "hours deflated" counter would shame teens for a reviewer's caution. And a minor can delete anything they made public, at any time, which California's eraser law also asks for (B2).

### B8. Data inventory

Every field the client collects or derives, where it lives, who can see it, and how long it is kept. Retention periods are proposals to make B2's "written and finite" rule concrete; the spot-check window they depend on is an Open question. "Submission window" means the period a submitted claim covers.

**On the device (Tier 0).** Stored in the encrypted spool or local database; visible to the user only.

| Field | Source | Kept |
|---|---|---|
| Raw prompt text | Hooks, log readers, AGENTS.md `track` calls | 30 days, or until any submission covering it is decided, whichever is later; user can shorten |
| Agent output and transcripts | Not copied: read in place from the harness's own logs; pairtime stores only offsets and hashes | The harness's own retention (Claude Code 30 days by default, signals doc 3.2) |
| Tool events: tool name, command, exit code, files touched | Hooks (`PostToolUse` etc.), log readers | 30 days |
| File paths and names | Editor, git, hooks | 30 days (only HMACs are ever uploaded) |
| Foreground app executable; whether the window is "relevant" to a project | OS focus watcher | 30 days. **Window titles are matched in memory and never stored** |
| Per-minute presence bits (any input / key input where the OS separates them) | OS idle APIs, no listener | 90 days |
| Per-minute input counts (keys, clicks, scroll) | Opt-in typing-evidence listener only (A4) | 90 days |
| Per-minute cadence features: hold-time median, rollover share, injected share, auto-typer score | Opt-in listener, in-memory processing; **no per-keystroke timestamps or key identities are ever written** | 90 days |
| Per-user typing profile (v2) | Rolling aggregate of cadence features | Rolling 90 days |
| Prompt MinHash and SimHash sketches | Derived from prompts | 90 days |
| Process chain at session start; harness env flags (`CLAUDE_CODE_CHILD_SESSION`, entrypoint, `originator`) | OS, harness | 30 days |
| VM label, input-device inventory (v1.5) | CPUID, SMBIOS, Raw Input device list | 30 days |
| Event leaves `E_i` and the device key `K_dev` (salts are derived from it) | Client | `E_i`: as long as the hours could still be submitted (proposal: 180 days). `K_dev`: until uninstall or rotation, in DPAPI, Keychain or Secret Service |
| Local git data: SHAs, branch names, file names in diffs | git | As long as the repo exists; pairtime keeps only what it uploads |

**On the server (Tiers 1 and 3).**

| Field | Source | Who can see it | Kept |
|---|---|---|---|
| `hca_sub`, `slack_id` | HCA sign-in | User, admins, programs the user authorized | Account lifetime |
| Email | HCA (`email` scope) | User, admins; programs only with user authorization | Account lifetime, encrypted at rest |
| GitHub uid and username | GitHub link | User, admins; public if the user shows it | Account lifetime |
| Handle and avatar | User | Public | Until changed |
| Random `device_id`, OS family, client version and build id, harness names and versions | Client | User, admins, detectors | Device lifetime plus 90 days |
| Uploading IP address and JA4 TLS fingerprint | Request metadata | Fraud tooling and admins only | 30 days (Hackatime's alt tools look back 30 days, sibling doc 1.9), then deleted |
| Per-minute buckets per project: credited seconds by activity kind, human-evidence and automation flags, re-roll counts | Client | User (real time); public as daily aggregates (B7); reviewers for submission windows | Per-minute detail 13 months (enough for a year-in-review), then daily totals per project for the account's lifetime |
| Commit records: SHA, parents, diffstat, `observed_at`, `server_first_seen_at`, optional path HMACs | Client, forge API | User; public for public repos; reviewers | Account lifetime |
| Project fingerprint `HMAC(pepper, root commit)`; names and repo URLs of public projects only | Client | System; public projects are public | Account lifetime |
| Batch roots and chain heads `(device_id, b, n_b, R_b, H_b, received_at)` | Client | System; auditors through the daily published root | Until the spot-check window closes for every submission that used them; unsubmitted roots after 180 days |
| Risk scores and flags | Private detection module, **for submitted windows only** (B2 point 6) | Reviewers, Fraud Squad | Same as the submission's evidence |
| Minimal fraud record after account deletion: `hca_sub`, verdict, date, reason category | Review outcome | Fraud Squad | A period Hack Club sets (Open questions), reported in the eraser's `retained` output |

**In the evidence store (Tier 2).** Behind HCA sign-in with a reviewer role; every view logged.

| Field | Who can see it | Kept |
|---|---|---|
| Event metadata for every event in a submission window, verified against roots | Assigned reviewers, Fraud Squad, HQ spot-checkers | Until the spot-check window closes (proposal: 12 months after the decision), then deleted; the verified summary numbers stay with the submission |
| `k` revealed prompts, redacted, with markers | Same | Same; the user can purge one immediately if it exposed something, which re-opens the hours it supported |
| Optional extras and opt-in timelapse | Same | Same |
| Reviewer decisions, deflation justification, notes (written without personal data) | Program, HQ | As long as the Unified DB record exists |
| Reviewer access log: who opened which evidence, when | HQ admins, **and the user** | 2 years |
| Interviews | Notes only, no recordings | With the decision |

Two cross-cutting rules: the client's `pairtime privacy show` prints every Tier 1 field exactly as the next upload will send it (B1's VS Code lesson), and Hack Club's privacy hub can export or delete all of the server-side rows above through the B3 endpoints.

## Design implications

Each item is tagged with the requirements it serves.

1. **Make the credit rule the first anti-fraud layer.** Credit only minutes with human-side evidence; agent-only spans and mouse-only input earn zero; reading and think windows are capped; one human-minute per wall-clock minute per account across all devices and projects. This removes T1-T3, T7, T11a, T12 and T14 with no detector at all (A3, A8 #1). [R2, R9]
2. **Server receipt time is the clock.** Hash-chain the local log, upload a batch root every 5 minutes, and classify every minute as witnessed, backfilled or offline. Lookback hours are backfilled by definition: show them to the user (R3, R4) and require corroboration, such as forge push times, before they can be submitted (A3 T8, T20). [R4, R9, R10]
3. **Commit, then reveal.** Use salted per-event leaves and per-token content trees (B6). At submission, verify all event metadata in the window against the stored roots, then draw a server-side random sample of `k` prompts to reveal with redactions. A challenged item the user withholds doesn't credit the minutes it supports. [R6, R10]
4. **Publish one daily root of everything received** to a public git repository, so an external auditor doesn't have to trust the pairtime server not to backdate roots. About one small commit a day. [R10, R11]
5. **Two detection tiers, as in the MRC invalid-traffic standard.** Tier 1: deterministic, documented labels for known automation (harness flags, headless entrypoints, process tree, `LLKHF_INJECTED`/`LLMHF_INJECTED`, XTEST, fixed-period keys like Caffeine's F15). Tier 2: private scoring in a separate server process (the Ari pattern), whose thresholds are never published (A5, A8 #9). [R9, R1]
6. **Detectors rank; people decide.** No detector bans anyone. A flag changes the evidence required and the audit probability. The user sees which category of evidence was missing and can appeal to a person (A6). [R9, R3]
7. **Risk-weighted audits with a short live interview**: two server-chosen commits, one design decision, one small live change. Publish the base audit rate; raise the probability with claimed hours so that `p·q > g/(g+L)` holds (A6). At the acceptance target this costs tens of reviewer-hours in total. [R9, R10]
8. **Pay after review and keep hours revocable** through the spot-check window. Send confirmed fraud to Fraud Squad through Robert, so the penalty is Hack Club-wide. [R9]
9. **Keystroke features stay in memory, and the listener is opt-in.** Ship the keyboard listener as an opt-in "typing evidence" module (Input Monitoring on macOS, a low-level hook in a signed binary on Windows, XInput2 on X11, nothing on Wayland). Store per-minute aggregates (counts, hold-time median, rollover share, injected share, a score); never write per-keystroke timestamps or key identities; stop counting while focus is outside the relevant-window list, which covers password fields. Minutes without it are "typing unknown" and judged on E1-E8 (A4). [R6, R8, R9]
10. **Injected input is not typing evidence, and not fraud.** Offer an assistive-input declaration (dictation, switch access, on-screen keyboards) that routes the user to other evidence instead of a flag (A3 T4-T5). [R2, R6, R9]
11. **VM detection is a label, never a flag.** Use the hypervisor's root-partition privilege bits so Windows with VBS or Hyper-V isn't misread as a VM, and allow ChromeOS Linux (A3 T10). Defer it past v1. [R8, R9]
12. **Reviewer integrity is part of the product.** Random assignment instead of claim queues, no reviewing your own club, seeded known-answer submissions, per-reviewer statistics, and a reviewer access log that the reviewed user can see (A3 T18). [R9, R10, R6]
13. **Redact on the device, deterministically, by removing spans.** Known values first (the client knows the user's names, paths, hostname and private repo names), then gitleaks' 222 rules, then validated PII patterns ported from Presidio, then optional GLiNER PII edge (46 MB uint8) at reveal time only, then a user preview. Never rewrite text (B4). [R6, R8]
14. **Use the agent only for suggestions and labels.** At reveal time the agent may propose redaction spans as character offsets for the user to confirm; an agent-written summary is stored as a labeled claim, never as evidence (B5). [R6, R7, R9]
15. **Fix the Tier 1 field list and make it printable.** `pairtime privacy show` prints exactly what the next upload will contain. Any new uploaded field goes through a public change process, as Go's telemetry does (B1, B6). [R6, R10]
16. **High-privacy defaults for everyone, so age doesn't have to be known.** Public/private choice at onboarding with nothing preselected; on every consent screen (typing listener, leaderboards, any research use), "No" is the most prominent option, declining degrades nothing, and a refusal isn't re-asked for a year, which is New York's CDPA rule (B2 point 7); an aggregate-only public page; daily public totals with no hour-of-day heatmap; live presence opt-in; nothing that locates a person; no public trace of review outcomes (B7). [R5, R6]
17. **Write down a finite retention period for every field** (B8), and implement Hack Club's privacy-hub contract: `POST /api/privacy/delete` and `POST /api/privacy/export` with the same ES256 JWT checks Hackatime uses (B3). Keep a minimal fraud record after deletion and report it as retained. [R6, R5]
18. **Rely on HCA for age and eligibility.** Refuse under-13 accounts, apply the 19th-birthday cutoff from HCA's eligibility rather than storing birthdates (B2, B3). [R6, A]
19. **Score only submitted hours.** Fraud scoring is profiling; logged-only hours are never scored (B2 point 6). [R6, R9]
20. **Encrypt the local store** with an OS-protected key and keep raw prompts for 30 days by default. Windows Recall's first preview showed what infostealers do with an unencrypted local activity database (B1). [R6, R8]
21. **Check code similarity at review time** with JPlag (GPL-3.0, run as a separate tool) or Dolos (MIT) for public repos, against the Unified DB's code URLs (A4). [R9, R10]
22. **Fit thresholds to review outcomes.** Audit verdicts and reviewer decisions become the labels detectors are trained on, the way Overwatch verdicts train VACnet (A5). [R2, R9]
23. **Keep the trust layer cheap.** Roots are about 150 bytes per active device every 5 minutes; evidence moves only for submissions (B6). [R11]
24. **Cap the bounded attacks.** Cap credited hours per day, uncommitted time and offline time per submission, so U3-U6 have a known maximum payoff for the audit rate to beat (A6). [R9]

## Open questions

For Karim or Hack Club staff:

1. **Who is the legal operator** of a hosted pairtime: Hack Club HQ, Karim, or each program? That decides which of COPPA, GDPR, the UK code, CCPA and DPDP apply (B2), and whether Hack Club's privacy notice and DPO cover pairtime.
2. **How long is the spot-check window?** It sets how long Tier 2 evidence and roots must stay reachable (B8). The handbook doesn't say (sibling doc 3.7).
3. **Will Fraud Squad accept** commit-then-reveal metadata, a random prompt sample and a short interview in place of editor history or screen video, which they trust today (A2)? Would the Telescreen team add a pairtime `kind` to Robert?
4. **Interviews with minors.** Who may hold a 15-minute live call with a 15-year-old, in what channel, and is it recorded? Hack Club's safeguarding rules decide this, not pairtime.
5. **R5 wording.** Does "public-by-default" allow an onboarding choice with nothing preselected, which GDPR Art. 25(2) and the UK code favour (B7)? If not, who writes the Children's Code "compelling reason" assessment?
6. **India from about May 2027.** Will Hack Club get counsel's view on whether a self-installed tracker that uploads only aggregates is "tracking or behavioural monitoring of children" under DPDP s. 9(3), and whether Indian under-18s need a local-only mode (B2)? The 2026 Removal of Difficulties Order's effect on s. 9 is unverified here.
7. **AGPL and private detection.** Is an open-core server acceptable under R1, with tier-2 rules in a private module run as a separate process, as Ari and Stardance do? The GPL FAQ treats programs that talk over pipes, sockets or command-line arguments as normally separate, unless they exchange intimate internal data structures (paraphrased; gnu.org was unreachable from this machine, so unverified). Third-party AGPL contributions to the public server don't change that, but a private module that links the server's code would.
8. **Penalties and propagation.** Is a pairtime fraud verdict a Hack Club-wide ban, as Jackpot's rules imply? The audit-rate math (A6) needs that number.
9. **Payout timing.** Can programs delay payouts until review and claw back hours later, as Stardance did with stardust?
10. **Assistive input.** What should reviewers do with a declared dictation or switch-access user whose typing evidence is all injected (A3)?
11. **Shared devices and networks.** What is the policy for siblings on one family PC, or a whole club on school Wi-Fi, so pairtime avoids the false positives described in A2?
12. **Fraud record after deletion.** How long may Hack Club keep a minimal verdict record after a user deletes their account (B3)?
13. **Remote development.** Are SSH sessions, Codespaces and other cloud dev boxes in scope? They need a local daemon paired with the remote one, because the remote machine sees no keyboard (A3 T3).
14. **Who reviews.** Teen volunteers, paid staff or program authors? It changes conflict-of-interest rules and who may read minors' revealed prompts.
15. **Calibration data.** Can pairtime get Stardance's per-devlog approved and original minutes with the matching Hackatime heartbeats under a data-sharing agreement, to fit thresholds (Design implication 22)? It's minors' data, so it needs the operator's sign-off, and New York's CDPA excludes "research and development" from the purposes that need no consent (B2 point 7), so counsel has to say whether fitting fraud detectors counts as fraud protection or as research.
16. **The public audit rate.** What base rate is Hack Club willing to publish, and staff?
17. **Requiring the typing listener.** May a program require the opt-in typing-evidence module (A4) for submitted hours, or for hours above a cap? Requiring it makes T4 and T5 much harder, but it puts an Input Monitoring prompt or a keyboard hook between a privacy-conscious teen and their prize, and the UK code's Std 13 forbids nudging children to give up protections.
