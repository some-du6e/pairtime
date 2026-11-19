# The bounty: AI time tracking

This file records what the bounty asks for, word for word, so design and review can check against the source instead of memory. Text in quote blocks is verbatim from Hack Club Slack, with Slack link markup turned into Markdown links. Everything outside a quote block is ours.

Zach Latta, Hack Club's founder, posted it in #zrl-land on 2026-11-18 at 23:38 UTC ([message](https://hackclub.slack.com/archives/C01FXNNF6F2/p1795045121671029)). We read it at 00:10 UTC the next day, when the thread had about 115 replies. The prize is $10,000 USD.

> So, to that end, I’m excited to announce the biggest bounty in Hack Club’s history: $10,000 USD to build an open source AI time tracking tool.

The question it asks:

> The hardest problem to solve is AI time tracking. How can we figure out how many hours of productive struggle someone spends toward a project when building it with AI?

## Requirements

### R1. License

> It must be open source under the AGPLv3 license.

### R2. Accuracy

> It must accurately track the hours of productive struggle that teenagers spend building awesome projects with AI, like this [Counter Strike 2](https://cs2go.3kh0.net/) browser port by Rowan, age 17.

### R3. Worth using for its own sake

> It must be meaningful and motivating to use in it and of itself, and not just an anti-fraud tool. Many teenagers are interested in self-improvement. This should be an amazing tool they want to install and use to self-improve, regardless of any You Ship, We Ship incentives. One of the things I love about [Hackatime](https://hackatime.hackclub.com/) is that it helps me track and feel motivated to work on personal projects I never submit to Hack Club programs.

### R4. Passive

> It must track time passively. The best thing about Hackatime is that you don’t need to think about time tracking. When you are in an intense flow state for hours working on your project, Hackatime counts that time. When you jump into your project for 5 minutes before school, Hackatime counts that time. When you are multitasking and jump between 5 different projects in an afternoon, Hackatime accurately counts that time for each project. This solution must work passively too.

### R5. Server

> It must have a centralized server with accounts, public-by-default user profiles, projects, git integration, and a stats dashboard that updates in real-time as time is logged.

### R6. Privacy

> It must be privacy friendly in its approach and be something most privacy conscious users are comfortable installing and using.

### R7. Every model and harness

> It must work with all AI models and all harnesses (or at least support the most popular ones starting out and have a plugin approach where integrations can be built for every use case in the future).

### R8. Platforms and old hardware

> It must support all 3 popular operating systems (Windows, Mac, Linux) and work on low spec computers. The average Hack Clubber is on a 10 year old Windows laptop!

### R9. Trustworthy

> It must be trustworthy. When we ran [Hack Club High Seas](https://highseas.hackclub.com/), the first event with Hackatime, most hours submitted were valid. In [Hack Club Stardance](https://stardance.hackclub.com/), which ran this past summer, 123,400 Hackatime hours were submitted and so far we’ve only been able to approve 27,600 (which means we hand-reviewed and validated them as hours of productive struggle). Most of this isn’t fraud. Many were hours where we felt the person wasn’t pushing themselves, so we had to deflate to only the hours we felt could be justified as productive struggle. Another big contributor was people using AI without proper self-declaration. But the point holds: for the most part, an hour submitted via this new system should equal 1 hour of actual productive struggle toward a shipped project.

### R10. Auditable

> Submitted hours must be auditable by an external party (ex. program authors, reviewers, and Fraud Squad) to validate there is no time exaggeration, fraud, and that each hour is an actual hour of productive struggle. There can be different privacy guarantees for logged hours vs. submitted hours. For example, if it uses a timelapsing approach, perhaps the timelapses for all logged hours stay locally on your computer and only submitted hours are uploaded to the server.

### R11. Hosting cost

> The hosting cost cannot exceed $500 USD/month for 15,488 monthly active users logging 109,759 hours per month (these are Hackatime’s stats for the last 30 days). Hackatime’s hosting bill for its overpowered server is currently $213 USD/month. I just SSHed into its server and for the past hour it has averaged 10% of CPU use on a machine with 32 cores, 65 out of 192 GB of RAM, 1 out of 4 TB of storage, 1 MB/s of network activity, and 60 MB/s of disk read - so it still has plenty of headroom. The effective monthly hosting cost for Hackatime based on its resource usage is $55/month.

## Acceptance

> Criteria for acceptance: The first time tracking system that meets the above requirements and is used to track 1,000 weighted projects in the Hack Club Unified Projects DB wins this bounty. This can be a completely new piece of software, an improved version of Hackatime or [Lapse](https://lapse.hackclub.com/), or an approach we haven’t even thought of yet. This doesn’t need to be built on top of Hackatime.

Eligibility:

> This bounty is open to current Hack Clubbers, alumni, teen contractors, and staff. It can be won by an individual or a group, but if it’s won by a group there must be a group leader who submits on behalf of the group and is responsible for communicating the final split.

The rest of that paragraph covers rules for Hack Club contractors and staff.

## What the organizer added in the thread

All from Zach Latta. Each line gives the Slack message timestamp in the announcement thread.

Who wins (1795045523.264749), answering whether it goes to the first entry or the best one:

> First person to meet the criteria, but note you actually need to get 1,000 weighted projects in the YSWS DB. Not just hours logged. So you need to actually get programs to use it.

How many hours (1795045778.156379), answering whether 1,000 weighted projects means 10,000 hours:

> Yes, 10,000 hours that actually make it into the YSWS DB. So your submission needs to actually be used by programs to track 10,000 hours that make it into the YSWS DB.

Program guidance (1795045557.054889), answering a worry that YSWS authors won't accept hours from new tools:

> We will work with Quality and Integrity to provide good guidance around how to use new time tracking tools

Your own programs (1795045597.390149), answering whether you can run programs that use your own tracker:

> Yes!

Passive, and Lapse (1795045616.011869):

> Yes, currently Lapse is not passive. You need to turn it on to start tracking

Passive, and lookback (1795045644.256149):

> A sufficiently high quality implementation of a lookback mode could count as passive. Just think through that user story in point #4. Can your new software make that same user story possible? If yes, then that counts as passive.

## From the linked vision post

The announcement opens with a link to [A vision for an AI native Hack Club](https://hackclub.slack.com/archives/C01FXNNF6F2/p1794951285026219) (ts 1794951285.026219). Its first point says what the tracker should count:

> Hours of productive struggle toward shipped projects is still the right metric because one-shotting takes 2 minutes of productive struggle while intense reverse engineering takes dozens of hours, but [Hackatime](https://hackatime.hackclub.com/) doesn’t currently count how much time you are actively solving problems when building with AI. It just counts how active your AI agent is.

> Importantly, not all hours of screen time or time prompting is productive struggle - if you are in [dark flow](https://www.fast.ai/posts/2026-01-28-dark-flow/) or doing the same thing over and over again, that shouldn’t count.

Its third point changes what counts as a shipped project:

> “The project must do what it says it does”

## Themes in the thread

Other replies, paraphrased and without names:

- Parsing agent transcripts isn't enough by itself, because an LLM can drive the agent and produce believable transcripts.
- Many people won't share their chats, and keeping transcripts at all is a security risk.
- Fraud reviewers need tooling that works with whatever tracker wins.
- Adoption is the hard part. YSWS authors have to accept the hours, and they won't if the tool makes fraud review harder.
- AI-text detectors such as Pangram aren't reliable on short prompts.
- WakaTime's existing AI tracking is widely seen as poor.
- A Hackatime maintainer said its Postgres is deliberately overprovisioned, so the real minimum spec is lower than the numbers in R11.

## Karim's direction

Karim leads this repo. Below are their messages, paraphrased, with timestamps that point back to Slack.

- Make it universal with an AGENTS.md entry (or a skill) plus a CLI. The entry tells the agent to take the user's prompt, strip PII, and pass it to a `track` command. Karim puts the cost at about 30 input tokens and one tool call. (C01FXNNF6F2 1795045417.261689; C0CK2BQVCG2 1795046571.667749 and 1795046697.781239)
- Agent activity can't be the measure, because a user can tell an agent to /goal or to check something every 20 minutes. (C0CK2BQVCG2 1795046888.459639)
- Fraud is the hard part, because agents make fraud easy for anyone. (C0CK2BQVCG2 1795046443.887509)
- The repo is public, and a fleet agent will self-host the server on Karim's domain. (C0CK2BQVCG2 1795046634.964429)

## Checklist

| ID | Requirement | Passing looks like | Status |
|---|---|---|---|
| R1 | AGPLv3 | LICENSE is the AGPL-3.0 text | done |
| R2 | Accurate hours of productive struggle | On a labeled set of real sessions, our hours match what human reviewers would approve | open |
| R3 | Worth using for its own sake | Teens keep it installed for projects they never submit | open |
| R4 | Passive | The three stories in R4 (hours of flow, 5 minutes before school, 5 projects in an afternoon) all count with no per-session setup | open |
| R5 | Server | Accounts, public profiles, projects, git integration, live dashboard | open |
| R6 | Privacy | Raw prompts and code stay on the machine unless the user submits them | open |
| R7 | All models and harnesses | The popular harnesses work out of the box, plus a plugin API and an AGENTS.md fallback | open |
| R8 | Windows, Mac, Linux on low-spec machines | One small binary per OS, near-zero idle CPU on a 10-year-old laptop | open |
| R9 | Trustworthy | One submitted hour ≈ one reviewed hour of productive struggle | open |
| R10 | Auditable | Reviewers can check submitted hours against evidence without seeing logged-only data | open |
| R11 | Hosting cost | A load model shows ≤ $500/month at 15,488 MAU and 109,759 hours/month | open |
| A | Adoption | 1,000 weighted projects (about 10,000 hours) tracked in the Unified YSWS DB | open |
