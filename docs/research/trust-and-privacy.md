# Trust and privacy: how teens would fake hours, what stops them, and how to do it privately

Status: draft in progress

Research for pairtime (AGPL-3.0), started 2026-11-19. Requirement IDs (R1-R11, A) refer to [docs/BOUNTY.md](../BOUNTY.md). This doc covers R2 (accuracy), R6 (privacy), R9 (trustworthy) and R10 (auditable). Every claim that isn't common knowledge cites a URL or repo path; "(unverified)" marks what I could not confirm from a primary source. Nothing here is legal advice.

Sibling docs this one builds on, not repeats: [hackatime-and-hack-club.md](hackatime-and-hack-club.md) (Hackatime anti-fraud, Telescreen/Robert, YSWS review rules), [ai-harness-signals.md](ai-harness-signals.md) (what each harness logs), [prior-art-and-measurement.md](prior-art-and-measurement.md) (measurement research), [client-platform.md](client-platform.md), [server-platform.md](server-platform.md).

## 0. Sources and versions seen

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
| T2 | **Self-continuing modes**: `/loop`, `/goal`, Copilot autopilot, Cursor `loop_count` follow-ups, "Ralph" loops (`while :; do cat PROMPT.md \| claude -p; done`, unverified attribution) | Low | 8+ h/day | Unsupervised cap only (about 1 min per run, grit's default) | High where the harness flags it (`Stop.session_crons`, `stop_hook_active`, `loop_count`); Med otherwise (regular timing, no input) |
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

### A5. Lessons from game anti-cheat, proctoring and click fraud

### A6. Attacks no design can stop, and how sampling and review handle them

### A7. Minimum evidence to approve an hour

### A8. Ranked v1 defenses

## Part B. Privacy

### B1. What privacy-conscious developers accept and reject

### B2. Obligations for users aged 13 to 18

### B3. Hack Club's privacy policy and Hackatime data

### B4. On-device PII and secret stripping

### B5. Asking the agent to strip PII: what it gets right and wrong

### B6. Disclosure tiers and commit-then-reveal evidence

### B7. Public-by-default profiles (R5) vs privacy for minors

### B8. Data inventory

## Design implications

## Open questions
