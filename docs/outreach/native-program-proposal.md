# Hard Mode: a pairtime-native YSWS for winter break

Status: draft for Karim, not sent

To: [TODO: HQ sponsor's name], copying Quality and Integrity. Hard Mode is a working name (DESIGN 13.1 item 6).

## The ask

We'd like an HQ sponsor and a grant budget for a winter-break YSWS whose hours are tracked with pairtime, our open-source AI time tracker. The pitch to teens is "ship something that fought back". Zach said yes to tracker builders running their own programs. To open on 2026-12-14 we need a yes by 2026-12-07. If that's too soon, there's a fallback at the end (DESIGN 9.4).

## Goal

350 weighted projects, about 3,500 approved hours. That's roughly 320 projects of about 11 hours from 245 teens, from about 410 installs. We assume 60% of people who join end up submitting, since they install pairtime to take part (DESIGN 9.1).

It's also the first place pairtime's numbers meet real reviewers. We'll publish three results from it: approved hours over credited hours, where we're aiming for at least 0.9; the median review time, aiming for under 5 minutes on a clean 10-hour submission; and approval rates by evidence level (DESIGN 2, 6.7, 7.7).

## Dates

| Date | What happens |
|---|---|
| 2026-12-07 | Funding decision, or we switch to the fallback |
| [TODO: date between 12-07 and 12-14] | Announcement (DESIGN 9.7) |
| 2026-12-14 | Open for building, before winter break |
| 2026-12-21 | Submissions open |
| 2027-01-04 | Decisions open, after scoring 1.0.0 is frozen and the accuracy report is published (DESIGN 6.7) |
| 2027-01-31 | Shipping closes |

## Rules

- Ship a project built with AI, with its hours tracked by pairtime. The YSWS handbook applies as written, including a public Code URL whose commits show the progress, and "when in doubt, deflate".
- One devlog per at most 10 credited hours, Stardance's cap.
- The approved hours are pairtime's credited hours after the reviewer's cuts. They go in `Optional - Override Hours Spent`, with pairtime's generated justification text and review URL (DESIGN 9.2, 9.3).
- Participants also link Hackatime until Robert accepts a `kind: "pairtime"`, so Fraud Squad's current tools see them. pairtime's own Fraud Squad desk covers everyone (DESIGN 7.2).
- Hours a program already paid through Hackatime aren't submitted again, and the server refuses overlapping pairtime submissions (DESIGN 9.2).
- Ages 13 to 18, with eligibility checked through Hack Club Auth (DESIGN 8.2).
- Nobody has to turn on the opt-in typing-evidence module to take part (DESIGN 8.4, 13.2 item 15).
- Prizes: [TODO: what an approved hour or project earns, paid from the grant].

## Budget

| Item | Amount | Notes |
|---|---|---|
| Grants | 350 x [TODO: HQ fills in the grant per weighted project] | We've seen $85 per weighted project quoted, which would be about $29,750, but that figure comes from a staff member's personal blog, not policy. Please give us the real one (DESIGN 9.4) |
| Server | $48.94 a month | A Hetzner CCX13 from before the first payout, or $149.68 with a warm standby. Until then the pilot runs on a home server for a few dollars of electricity (DESIGN 10.5, 10.6). [TODO: who pays] |
| Code signing | about $9.99 a month, plus $99 a year | Azure Artifact Signing under Hack Club's organization and an Apple Developer membership, which we're applying for separately (DESIGN 9.6, 10.6). [TODO: who pays] |
| Reviewer time | about 35 hours | Our rough estimate for 320 submissions: about 15% audited at 15 minutes each, and about 5 minutes for each of the rest, scaled from DESIGN 7.9. [TODO: whether outside reviewers are paid, and from which budget] |

## Who reviews, and who watches us

We're building the tracker and we'd run the program, which is a conflict of interest by definition. A tool trying to win a bounty should be the most audited thing in the DB. So these are launch conditions, not extras (DESIGN 7.10):

- Reviewers come from outside the build team. [TODO: who recruits them, and how many]
- Quality and Integrity runs or observes the audits, and spot-checks this program at a higher rate than others. [TODO: the rate Q&I picks]
- Reviewers are assigned at random from a pool. Nobody reviews someone from their own club or someone who shares a GitHub repository or organization with them.
- About 2% of the queue is seeded known-answer submissions, so each reviewer's accuracy is measured.
- We publish per-reviewer statistics: approval rate, hours approved per review, time per decision, seeded accuracy and spot-check outcomes.
- The audit odds are published. A 10-hour claim has at least an 8.7% chance of a 15-minute audit, rising with hours claimed. Audits with minors follow HQ's safeguarding rules, and our default protocol is in DESIGN 7.9.

## What pairtime provides

None of this is built yet beyond a code skeleton. The plan has a local alpha out by 12-01, the server by 12-11, and submissions and the review page by 12-18 (DESIGN 9.7).

- The tracker for Windows, macOS and Linux. It installs without admin rights and keeps each teen's Hackatime working (DESIGN 5.6, 5.9).
- The server, self-hosted on our own domain, with Hack Club Auth sign-in, profiles and a live dashboard (DESIGN 3.10, 10).
- The program's Airtable submission table, set up with the handbook fields and the "Automation - Submit to Unified YSWS" button, and the script that fills the justification fields (DESIGN 5.10, 9.4).
- The review page, the Fraud Squad desk, and signed evidence bundles that anyone can check offline with `pairtime verify` (DESIGN 7.7, 7.8).
- Submissions to Robert for fraud verdicts, with the same Hackatime names other programs send today (DESIGN 5.10).
- The accuracy report before any decision, and weekly counts of installs, submissions, approved hours and weighted projects, reconciled with HQ's own count (DESIGN 6.7, 9.8).
- A 10-minute walkthrough for reviewers (DESIGN 9.4).

## What we need from HQ

- A sponsor, the grant amount, and a yes or no by 2026-12-07.
- Quality and Integrity's agreement to observe audits and to the higher spot-check rate.
- The privacy controller for the server named by about 2026-12-08, because sign-ups stay closed until then (DESIGN 8.6, 9.7).
- A Robert event key for Hard Mode from the Telescreen team.
- [TODO: how a new YSWS gets added to the Unified DB's program list and wired to its submission automation].

## If it isn't funded by 2026-12-07

We run an opt-in pairtime track inside an existing program that already applies the one-third rule, under the same rules. When funding comes, we run a smaller first season aimed at 150 weighted projects. A short spring ship week in March is planned separately, aimed at 150 more (DESIGN 9.1, 9.4).
