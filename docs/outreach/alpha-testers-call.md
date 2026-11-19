# Call for pairtime alpha testers and calibration volunteers

Status: draft for Karim, not sent

[TODO: where this gets posted, and where replies go]

pairtime is an open-source tracker for building with AI. It counts your time, not the agent's: prompting, reading what the agent did, testing, and fixing things by hand. Nothing is built yet beyond a code skeleton. The first build is due 2026-12-01, and its scoring isn't calibrated until early January. We need two groups, and you can join one, both or neither.

## Read this first

- It's your choice. Saying no, or stopping later, costs you nothing, and you don't have to say why.
- You need to be 13 to 18. [TODO: whether younger testers need a parent's or guardian's permission, and how we collect it]
- Until you choose to sign in, pairtime sends none of your data anywhere. There's no account, no upload and no analytics, and sign-in isn't open yet (DESIGN 4.3, 8.6).
- pairtime never records which keys you press, and it never reads window titles. Unsigned builds have no keyboard listener at all. A later signed build may offer one that only counts key presses, and it stays off unless you turn it on (DESIGN 3.3, 7.2, 11 risk 18).
- Calibration means sharing screen recordings. It has its own consent step, and it doesn't start until our privacy notice says who is responsible for your data. [TODO: name the controller first (DESIGN 8.6)]
- Send questions or worries to [TODO: contact].

## Alpha testers: 20 to 30 people, from 2026-12-01

We're starting with Claude Code users and adding Codex and Cursor in the second build, around 2026-12-08. We need at least 10 of you on Windows laptops from before 2017. If yours has 4 GB of RAM and a spinning disk, it's the machine we're building for.

You'd install with one command, build as usual for at least four weeks, and tell us what breaks. Old-laptop testers also send us `pairtime doctor --report` after a normal day. It lists memory, idle CPU, hook speed, disk type and Windows build, with no usernames or paths, and it's how we show pairtime is light enough (DESIGN 5.9).

**What gets installed.** It needs no admin rights. The install command puts the `pairtime` program in your user folder. Before it changes anything else, it lists every change, and you can untick any of them. `pairtime install --dry-run` shows the list and changes nothing (DESIGN 5.9).

- A startup entry, so it runs when you log in.
- Hook entries in Claude Code's settings, next to any hooks you already have.
- From the second build, hook entries for Codex and Cursor if you use them, and a Claude Code setting that sends its activity data to pairtime on your laptop, with prompt logging off (DESIGN 5.1).
- From the second build, one line in `~/.wakatime.cfg` so your editor also reports to pairtime on your own machine. Hackatime still gets exactly what it gets now (DESIGN 5.6).

Early builds may be unsigned, so Windows may warn "Windows protected your PC". Click "More info", then "Run anyway", but only for pairtime from github.com/some-du6e/pairtime/releases. On Windows 11 with Smart App Control on, an unsigned build may not run at all, so tell us if that happens. [TODO: say whether the alpha build is signed]

**What stays on your laptop.** Everything pairtime records, including your prompts and the agent's replies, when you were active but not what you typed, which app had focus, which files changed, and your commits. It reads Claude Code's transcripts where they are, including the last 30 days once for a welcome recap, and doesn't copy them. `pairtime ledger show --day <date>` prints everything it recorded that day (DESIGN 4.2, 8.8).

**What gets collected.** Only what you send us: your sign-up answers, bug reports, and the doctor report on old laptops. Once our privacy notice is up, you can choose to sign in with Hack Club Auth for a live dashboard. Signing in gives our server your Hack Club Auth ID, your Slack ID and your email address, stored encrypted. pairtime then uploads numbers and hashes only, such as seconds credited per minute, commit hashes and line counts, and which AI tools you use. Those uploads never include prompts, code, file names or keystrokes. The server keeps your IP address and a fingerprint of your connection for 30 days, then deletes them. Your profile stays private unless you make it public (DESIGN 4.3, 4.4, 8.3). We'd use the uploads to see whether testers still use pairtime after one week and after four (DESIGN 6.8, 9.7). Staying local-only is fine.

**How to stop.** A `.pairtime-ignore` file keeps a folder out. `pairtime uninstall` removes everything pairtime added and puts your files back exactly as they were. [TODO: confirm uninstall also deletes pairtime's data folder, or give its path]

We may later ask a few of you to record test data on a throwaway project. That will be a separate opt-in.

## Calibration volunteers: 40 to 60 people, 2026-12-01 to 12-27

Before any program counts a pairtime hour, we want to check its numbers against what human reviewers would approve (DESIGN 6.7). We need people aged 13 to 18 who use Claude Code, Cursor, Codex, Copilot or Gemini CLI, with at least 40% on Windows and some on old laptops. You can be an alpha tester too.

- From 2026-12-01, pairtime runs in calibration mode. Your totals show as "calibrating" until the rules are final, so nobody learns to steer them.
- Between 12-07 and 12-27, you record at least five work sessions of your choice with a screen recorder on, such as Lookout. [TODO: Lookout sessions have to come from a program (HC 4.2). Set one up or drop the example]
- For each session, you send its timeline, its prompts with the agent replies they answered, and a link to the recording. You see every prompt first, already redacted, and you can remove anything. pairtime doesn't redact the recording, so close chats and anything private before you start.

**Who sees it.** Two raters watch the recording next to the timeline and label each minute, and a third settles disagreements. Only the raters assigned to your session can open what you send. It's encrypted, and no program ever sees it. We delete our copies 30 days after labeling. We keep the labels, such as "debugging, 20:14 to 20:31", and numbers worked out from the session (DESIGN 6.7; SM 7.2). [TODO: who the raters are, and whether HQ clears them as it does audit reviewers (DESIGN 7.9). Where recordings are stored, and who deletes them]

**Consent.** This is research use, so it gets its own consent screen, separate from installing, with "Not now" as the main button. If you agree, we'd also ask your age band, 13 to 14, 15 to 16 or 17 to 18, to check pairtime isn't unfair to younger builders. You can stop at any time. [TODO: how to ask us to delete a session, and until when]

## How to join

[TODO: form or channel]. Tell us which group you want, your operating system, roughly how old your laptop is, and which AI tools you use.
