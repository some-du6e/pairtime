{{- /* SPDX-License-Identifier: AGPL-3.0-or-later */ -}}
# pairtime privacy notice

{{if not .Named -}}
> **This notice isn't final.** It doesn't name a controller yet, the person or organization responsible for your data on this server. Until it does, this server takes no sign-ups, and the parts marked "not named yet" stay empty.

{{end -}}
This notice covers the pairtime server at {{.BaseURL}} and the pairtime program on your computer. It's written for Hack Clubbers aged 13 to 18 and their parents. If anything in it is unclear, ask the controller.

## The short version

- Your prompts, your code, your file names and your window titles stay on your computer, unless you submit hours: then a few prompts, and the agent replies they answered, go to the program's reviewers.
- This server gets your Hack Club identity and email address, the IP address each upload comes from, and numbers: for every 15 seconds you work, how many were credited, to which project (as a key, not its name), what kind of work it was, and why the rest weren't.
- Nothing about you is public until you choose what to show.
- Hours are checked for fraud only when you submit them to a program.
- Every kind of data has a time limit, listed under [How long we keep things](#how-long-we-keep-things). Deleted data is gone from the server's backups too within {{days "server.db_snapshots"}}.

## Who is responsible for your data

The controller decides what happens to your data on this server and answers for it. The controller here is {{.Controller}}. To ask a question or use your rights, contact {{.Contact}}.{{if .DPO}} You can also reach Hack Club's Data Protection Officer at {{.DPO}}.{{end}}

pairtime is open source under the AGPL-3.0 license, and [the code this server runs]({{.SourceURL}}) is public, so anyone can check this notice against it.

## What pairtime does

pairtime counts the time you spend building with AI tools: thinking, writing prompts, reading what the agent did, testing and fixing. Time your agent spends working on its own doesn't count. The pairtime program on your computer does the tracking and the scoring, and this server receives the results, mostly as numbers. That lets you see your time from anywhere, show a profile if you want one, and submit hours to a Hack Club program.

## What stays on your computer

pairtime keeps these on your computer:

- the text of your prompts and of the agent's replies
- your code, your file names and your folder paths
- your window titles, which pairtime matches to your projects in memory and doesn't even store
- the names of other apps you use
- your keystrokes. pairtime never records which keys you press. It does note, in each presence sample, how long ago you last pressed a key, and if you turn on the typing module, how many keys you pressed in each 15 seconds and what share of them a program typed rather than your keyboard. These numbers go to reviewers with your other records when you submit hours.

pairtime encrypts what it keeps of your prompts, the agent's replies and your file paths. That keeps them from other people who use this computer under their own account, from apps that sync your home folder to the cloud, and from casual reading. It doesn't protect them from malware running as you.

None of it is uploaded, except in two cases you control. When you submit hours, a few of your prompts and the agent messages they answered go to the program's reviewers, after you've seen exactly what they will see. [When you submit hours](#when-you-submit-hours) explains how. And if you turn on backups, encrypted copies go to this server, which can't open them without your recovery code.

If you also use Hackatime, your editor keeps sending Hackatime what it sent before. pairtime adds nothing to it and removes nothing from it.

## What this server receives

About once a minute while you work, your computer uploads these:

- **Minute buckets.** For each minute, how long an AI agent was running, and for each 15 seconds of it: how many seconds were credited, to which project key, at which evidence level, why they were credited (such as a prompt you typed, a command you ran or a test run), what kind of work they counted as (prompting, reading the agent's output, supervising it, reviewing, testing, debugging, editing by hand or looking something up), and whether the credit rests on `pairtime track`, an editor plugin or the WakaTime bridge. Each second that wasn't credited gets a reason code. So this server learns what kind of work you were doing every 15 seconds, though never what it was about.
- **Batch roots.** Hashes that seal what your computer recorded, so the record can't be changed later without it showing.
- **Commit records.** For each commit, its ID and its parents, its times, how many files and lines changed, the credited time since the commit before it, and whether an AI agent made it. File paths go only as keyed hashes, never as names.
- **Labels.** Your pairtime version, the scoring version, the names and versions of your AI tools, whether the typing module is on, and whether pairtime thinks it runs in a virtual machine.

Each upload also shows this server the IP address it came from. Cloudflare, which sits in front of the server, adds a fingerprint of your connection's TLS settings called JA4. Hack Club's Fraud Squad can look them up when it checks submitted hours, and the server deletes them after {{days "server.ip_ja4"}}. Copies stay in the server's backups until those expire, at most {{days "server.db_snapshots"}} later (see [How long we keep things](#how-long-we-keep-things)).

Some things reach the server only at particular moments:

- **Sign-in.** When you sign in with Hack Club Auth, the server gets your Hack Club Auth ID, your Slack ID, your email address, and whether Hack Club has verified you. It stores your email encrypted.
- **A new computer.** When you sign in on a computer, the server gives it a random device ID and stores its OS family and a device key that signs its uploads.
- **A new project.** When pairtime first sees a git repository, it sends the ID of the repository's first commit. The server turns that into a keyed hash and throws the ID away. It gets the repository's address only if the repository is public, you publish the project, or you install pairtime's GitHub App on it.
- **Programs.** When you connect a project to a program, the server stores keyed hashes of the Hackatime project names you choose to link. From then until you unlink the program, it can read, for the projects you linked: your credited time, with the start and end time of each stretch, which includes the time of day your public profile never shows; your totals and how much of them is at each evidence level, for any dates; your username and IDs; and your email address, if you allow it.
- **pairtime's GitHub App**, only if you install it on repositories you pick. GitHub then tells this server about each push to them. For a public repository the server keeps the push as GitHub sends it. For a private one it drops the lists of files each commit changed and keeps the rest: the repository's name, the branch, each commit's ID, message and times, and the names and email addresses of the people who wrote and pushed the commits.
- **Publishing.** When you publish a devlog, a project page or badges, the server gets exactly what the preview showed you.
- **Backups.** Only if you turn them on, the server gets your device key, locked with your recovery code, and encrypted copies of your ledger and vault. It can't open them without your recovery code.

Apart from a submission, and the encrypted backups if you turn them on, this server never receives your prompts or the agent's output, file names or paths, window titles, the names of other programs you run, branch names or commit messages of private repositories (unless you install pairtime's GitHub App on one), keystrokes or their timing, prompt similarity sketches, error messages, your computer's name, or your username on it.

These lists are a summary, and `pairtime privacy show` prints every field of the next upload exactly as it will be sent. There's no analytics or telemetry. Apart from what this notice lists, the only request pairtime makes is a daily check with GitHub for updates, and that carries none of your data.

If you'd rather upload nothing, use local-only mode. pairtime still tracks your time and shows it on your computer, but hours tracked that way can never be submitted to a program.

## Why we use your data

Everything pairtime does with your data falls into one of three groups.

1. **The service you asked for.** Tracking and scoring on your computer, uploads for your own dashboard, session recaps, and the totals on your public profile if you have one.
2. **Fraud protection.** Checks, risk scores and audits, and only on hours you submit. pairtime doesn't score your other hours for fraud.
3. **Everything else, which needs your consent.** That's the typing module, club boards, habit insights, showing that you're building right now, sharing feedback on recaps to improve scoring, research use of pilot data, and backups with a recovery code. Each one gets its own card. Nothing is ticked for you, and "Not now" is the most prominent button. Saying no takes no feature away, and pairtime won't ask again for a year. You can turn any of them off later with one switch in Settings.

One question about consent is still open. On Windows, and on Linux with X11, most minutes count as evidence level B unless the typing module is on, and a high share of level-B minutes is one of the things that raises the chance a person audits a submission. Whether that treats people who said no unfairly is a legal question that hasn't been answered yet.

## Your public profile

{{if .PublicDefault -}}
Profiles on this server are public by default, but nothing about you is public until you answer the profile card. The card shows a preview of your public page, with "Keep my profile public" and "Make it private" as equal buttons.
{{- else -}}
Nothing about you is public until you choose. The first time you sign in, a card asks whether you want a public profile, and "Private profile" is the more prominent button.
{{- end}} Either way, you can switch with one click at any time, and anything you delete from your profile disappears at once.

A public profile shows only your handle, your daily totals (never the time of day), your build days and weekly streak, the AI tools and languages you use, projects whose repositories are public with their verified commits, devlogs you published, and badges you picked. It never shows your real name, school, city, age or time zone, your prompts or file names, private repositories, details of your computer, or anything about reviews. It updates at most once an hour. Leaderboards, club boards and "currently building" stay off unless you turn them on.

Reviewers never use your profile, build days or badges as evidence, so keeping your profile private costs you nothing.

## When you submit hours

Submitting hours to a program is always your choice. This is what happens when you do:

- Your computer reveals the records behind those hours, so the server can check them against what it received at the time.
- The server draws some of your prompts at random. For each one, you reveal the prompt and the agent message it answered. pairtime first removes the secrets and personal details it can find, then shows you exactly what a reviewer will see. You can remove more, or withhold any item. If you withhold one, the minutes it supports aren't credited.
- Redaction sometimes misses things, names especially, so read the preview before you send it.
- The preview also says what the check reveals about your other projects. That's counts and times, never content.
- What you reveal is encrypted on your computer, to a key that belongs to this server, and the evidence store keeps it encrypted. Cloudflare, and the logs and caches along the way, only ever see it encrypted.
- This server holds the key that opens it. It decrypts each item in memory to check it, and again each time a reviewer opens it, when it encrypts the item once more for that reviewer's browser alone. So whoever runs this server could read what you reveal. The operator also keeps an encrypted copy of the key offline.
- The encryption doesn't protect against someone at Cloudflare who actively replaces the keys, because the key your computer encrypts to, and the review page reviewers open it with, both come through Cloudflare.
- The people who may open what you reveal are the reviewers assigned to your submission, Hack Club's Fraud Squad, its Quality and Integrity team and HQ spot-checkers. Every view is logged, and you can read the log.
- If a revealed item shows something it shouldn't, the "This revealed something it shouldn't have" button deletes it from the evidence store at once, every stored version of it included. The minutes it supported stop counting.
- A person may audit your submission in a 15-minute walk-through on a Slack huddle. It's never recorded, your camera is never needed, and you can bring a parent or club leader, or choose a written audit instead. Hack Club's safeguarding rules come first where they differ.

## Who else is involved

- **Hack Club Auth** signs you in.
- **Cloudflare** sits in front of this server and handles its encrypted connections, so it sees everything that passes between your computer or browser and this server, such as the numbers your computer uploads and the pages you open. What you reveal when you submit hours passes through it encrypted, with the limit [When you submit hours](#when-you-submit-hours) describes.
- **GitHub, GitLab and Forgejo.** The server looks up commits of your public repositories there to check them. Once a day it also publishes a day root to a public GitHub repository. A day root is one hash over that day's uploads, with a count and nothing about you. If you install pairtime's GitHub App, GitHub also sends this server your pushes to the repositories you picked, as [What this server receives](#what-this-server-receives) says.
- **A storage provider** keeps this server's backups, and the evidence you reveal when you submit hours. The evidence and the nightly backups are encrypted before they reach it. The continuous backup isn't encrypted with a key of the operator's own, so the storage provider could read it.
- **Programs** you connect a project to can read your time and details for the linked projects, as [What this server receives](#what-this-server-receives) lists, until you unlink them. They also get your submissions to them and their outcomes.
- **Healthchecks.io** gets a ping every 5 minutes, so the operator knows the server is up. The pings carry nothing about you.

Apart from these, this server sends your data nowhere.

## How long we keep things

Every kind of data has a time limit. These tables come from the same file this server's cleanup jobs read, [docs/retention.json](https://github.com/some-du6e/pairtime/blob/main/docs/retention.json). A spot-check window is the time after a decision during which Hack Club can check a submission again. {{if .WindowProvisional}}Until Hack Club sets its length, pairtime keeps it open for {{.WindowMonths}} months after the decision.{{else}}It stays open for {{.WindowMonths}} months after the decision.{{end}}
{{range .Sections}}
### {{.Title}}

| What | How long it's kept |
|---|---|
{{range .Rows}}| {{.Data}} | {{.Kept}} |
{{end}}{{end}}
## Your choices and rights

On your computer:

- `pairtime privacy show` prints the next upload exactly as it will be sent.
- `pairtime ledger show --day <date>` prints everything pairtime recorded that day.
- A `.pairtime-ignore` file keeps a folder out of pairtime entirely, and the tray menu can pause tracking for an hour.
- Local-only mode uploads nothing.
- "Delete everything" wipes pairtime's data on your computer, including its device key.

Through the controller, you can ask for a copy of your data, ask for it to be corrected or deleted, and withdraw any consent you gave. Deleting your account deletes everything this server holds about you, except the minimal fraud record in the table above, which the deletion report lists as kept. Copies in the server's backups go as the backups expire, within {{days "server.db_snapshots"}}. Copies that programs already received, such as the outcome of a submission, are held by those programs, not by this server.{{if .PrivacyHub}} Hack Club's privacy team can also export or delete your pairtime data for you, through the same privacy hub it uses for Hackatime.{{end}}

Parents and guardians can contact the controller with questions or requests too.

## Age

pairtime on this server is for Hack Clubbers aged 13 to 18. Hack Club Auth decides who is eligible, so pairtime never asks for your birthdate or stores it. If Hack Club Auth marks you as not eligible, sign-in is refused, and submitting hours needs a verified Hack Club account. If you're under 13, Hack Club's rules don't let you use its services, this one included. Hours you log on or after your 19th birthday don't earn credit in Hack Club programs.

From 2027, if your computer's operating system can share an age range, pairtime asks for it once. The range stays on your computer and is never sent anywhere, and you or a parent can decline to share it. If it says you're under 13, pairtime stops tracking and deletes its data on your computer.

## Changes to this notice

If pairtime ever starts uploading a new kind of data, the change goes through a public review in pairtime's repository first, and this notice changes with it. This page was built by pairtime {{.Version}} from this server's settings.
