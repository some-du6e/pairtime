# pairtime: a brief for Quality and Integrity

Status: draft for Karim, not sent

pairtime is an open-source AI time tracker, licensed AGPL-3.0. So far it's a public design and a code skeleton, and no program uses it yet. Zach said HQ would work with you on guidance for new trackers. Here is what pairtime is designed to count, what a reviewer would get, and seven questions. Answers to 1 to 3 by [TODO: date; DESIGN 9.7 plans 2026-12-11] would keep our plan on schedule. The citations point to our public design, https://github.com/some-du6e/pairtime/blob/main/docs/DESIGN.md.

## What it credits

A second counts only when a human act opened a window over it, the OS saw the person at the machine, and focus was on that project. Where the computer can't tell which window is in front, as on some Linux desktops, a second that passes the other tests still counts, at level C. The acts are prompts, permission answers, interrupts, hand edits, typed commands and commits. Reading the agent's output counts up to the time it takes to read. Agent-only runtime, headless and scheduled runs, re-roll loops, idle time and jigglers earn nothing.

A reviewer can recompute any credited minute by hand from the public rules and the events listed against it. pairtime won't accept a program's decision on a submission until the rules are calibrated against two human raters and frozen, planned for 2027-01-04 (DESIGN 1.1, 6.1, 6.7).

## Evidence levels

Each credited second gets one level (DESIGN 6.4):

| Level | Means |
|---|---|
| A | Witnessed, meaning our server got it within 15 minutes, with OS input showing presence and the opt-in typing evidence showing the keyboard in use, with keys that other programs send left out |
| B | Witnessed, without opt-in typing evidence: the OS shows input but can't tell keys from other input, or, on a Mac, can't tell a pressed key from one a program sends. Most Windows and Mac minutes are B unless the teen opts in to typing evidence |
| C | Lookback, late uploads beyond the offline cap, side-door records with no typing evidence, or time credited where the computer can't tell which window is in front (Linux desktops such as GNOME on Wayland without our extension, and KDE). Only with program opt-in, only inside commit intervals that reached the forge, and at most at the handbook's third |

Time sent late after failed uploads keeps its A or B level for up to 3 hours per submission, and only if a commit reached the forge within 24 hours of reconnecting (DESIGN 6.2 D6).

Each minute also gets a band: corroborated if two independent records agree, otherwise witnessed, offline or backfilled. No program preset can pre-approve level-B, offline or backfilled minutes.

## The counting rule

The program puts pairtime's credited hours, after the reviewer's cuts, in `Optional - Override Hours Spent`, in place of Hackatime's figure for that window. pairtime generates the `Alternate Tracking Method` text, which starts "Tracked with pairtime <version>" and carries the evidence bundle's SHA-256, plus a review URL (DESIGN 9.2, 9.3).

We propose that an `approved_projects` row counts toward the bounty when its `Override Hours Spent Justification` contains "Tracked with pairtime", a pairtime review URL and a bundle hash. The data warehouse already copies the base into Postgres, so that's one query.

## What a reviewer sees

- A headline: "10.4 h claimed, 9.6 h credited: 7.9 h corroborated, 1.2 h witnessed, 0.3 h offline, 0.2 h backfilled. Hackatime-equivalent 12.1 h; the handbook third would allow 6.1 h."
- One row per work session, which can only be cut, with a reason. Must-view rows, meaning every flagged row plus one clean row at random, have to be opened before "Approve remaining rows" unlocks.
- Hour strips with every prompt, test run and commit. Each commit shows the laptop's, our server's and the forge's clock.
- 10 to 30 prompts drawn at random after the metadata is locked, each next to the agent message it answered, redacted by the teen and matched to a hash committed at the time.

Fraud Squad can verify the signed bundle offline with `pairtime verify` (DESIGN 7.7, 7.8).

## Questions

1. Attribution. Could you confirm the counting rule in writing, and send a weekly count we can reconcile against?
2. Two trackers, one number. Where both tracked a window, the program uses pairtime's figure with no second one-third cut. Do you agree?
3. Significant evidence. Is level A "significant evidence proving that more time should be credited"? Is level B, given the bands, the must-view rows and the higher audit chance it brings? Is level C acceptable at the third?
4. The spot-check window. How long after approval can a spot-check happen? We keep revealed evidence that long, and 12 months until you tell us (DESIGN 4.4).
5. The native program. Would you run or observe its audits, spot-check it more often than others, and, with its HQ sponsor, nominate or approve each of its outside reviewers, so nobody on our team picks one alone? We're building the tracker, so our program should be the most audited one in the DB. The proposal is attached (DESIGN 7.10).
6. Loop rows. Loop time arrives as its own row at 0, and a reviewer may raise it to its captured seconds, never past them. Does that count as cut-only, the way Ari settles rows? (DESIGN 6.2 D12)
7. Interviews with minors. Our default audit is 15 minutes in a Slack huddle with adult staff or a reviewer HQ has cleared. Nothing is recorded and the camera is never required. A parent may join, and the teen can choose an asynchronous audit instead. What do your safeguarding rules change? (DESIGN 7.9)

Around [TODO: date, about 2026-12-15] we'd like to show you a clickable review page on synthetic data and the red-team fixture results.

[TODO: sign-off, and how to reach Karim]
