{{- /* SPDX-License-Identifier: AGPL-3.0-or-later */ -}}
# pairtime privacy notice

{{if not .Named -}}
> **This notice isn't final.** It doesn't name a controller yet, the person or organization responsible for your data on this server. Until it does, this server takes no sign-ups except test accounts for the people who run it, and the parts marked "not named yet" stay empty.

{{end -}}
{{with .NotRunning -}}
> **Not everything here runs yet.** This server doesn't yet {{.}}. This notice describes each as it will work once it runs, and until then nothing reaches the server through it.

{{end -}}
This notice covers the pairtime server at {{.BaseURL}} and the pairtime program on your computer. It's written for Hack Clubbers aged 13 to 18 and their parents. If anything in it is unclear, ask the controller.

## The short version

- Your prompts, your code, your file names and your window titles stay on your computer, unless you submit hours or publish a devlog. When you submit hours, a few prompts, and the agent replies they answered, go to the program's reviewers. A devlog goes to this server exactly as its preview showed it.
- This server gets your Hack Club identity, first name and email address, the IP address each upload comes from, and numbers: for every 15 seconds you work, how many were credited, to which project (as a key, not its name, unless you choose to show that name on your own dashboard), what kind of work it was, and why the rest weren't.
- Nothing about you is public until you choose what to show.
- Hours are checked for fraud only when you submit them to a program.
- Every kind of data has a time limit, listed under [How long we keep things](#how-long-we-keep-things). Deleted data is gone from the server's backups too within {{days "server.db_snapshots"}}.

## Who is responsible for your data

The controller decides what happens to your data on this server and answers for it. The controller here is {{.Controller}}. To ask a question or use your rights, contact {{.Contact}}.{{if .DPO}} You can also reach Hack Club's Data Protection Officer at {{.DPO}}.{{end}}

If the controller is based outside the UK and the EU, UK and EU law may require it to name a representative in each, whom you could also contact. The representative is **\[not named yet\]**.

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

The exception is a hook call, what your AI tool sends pairtime as you work: your prompts, the agent's replies, the commands it ran with their output, and the files it edited. It waits unencrypted in pairtime's folder, where other people's accounts on this computer can't read it, though an administrator can, and so can any program running as you, until the agent has read it. That usually takes a moment. It takes longer while the agent isn't running, doesn't answer in time, can't open its keys or write its ledger, or is looking through a big project for the first time. A call a crash left waiting stays until the agent's next start. One the agent hasn't read is deleted after {{days "device.held_calls"}} at most. [How long we keep things](#how-long-we-keep-things) says how long the agent keeps the calls it holds.

None of it is uploaded, except in four cases you control. When you submit hours, a few of your prompts and the agent messages they answered go to the program's reviewers, after you've seen exactly what they will see. [When you submit hours](#when-you-submit-hours) explains how. When you publish a devlog, this server gets exactly the text the preview showed you, with any code, file names or error messages you left in it. When you turn on a project's name, this server gets the name you approved, often the project's folder name, and only your own dashboard shows it. And if you turn on backups, encrypted copies go to this server, which can't open them without your recovery code.

If you also use Hackatime, your editor keeps sending Hackatime what it sent before. pairtime adds nothing to it and removes nothing from it.

If you use T3 Code, pairtime also reads the messages T3 Code keeps on your computer, so it can credit the ones you send from T3 Code's desktop app, a browser or your phone. It reads a message's text only to measure its length and what kind of prompt it is, and keeps none of it. While T3 Code runs and you aren't working in a project, pairtime still notes once a minute whether you're at the computer, so the messages you send from your phone or a browser can count. Then it notes nothing about which app is in front. The Claude Code that T3 Code keeps open between your messages isn't work in a project, but a reply it's writing is, and so is T3 Code's own window in front, until 10 minutes after the reply ends or you switch away.

A message from a guest you shared a T3 Code thread with reaches your agent as a prompt with the guest's name and words in it, and T3 Code starts that prompt with `Message from guest`. pairtime keeps no text of any prompt that starts that way, from any AI tool, even when it can't read T3 Code's messages. So a prompt of your own that starts with those words keeps no text either, and doesn't count. While pairtime can read T3 Code's messages, it also keeps no text of any other prompt a guest's message started. When it can't read them, as on a computer without the `sqlite3` tool, or can't tell that a prompt came from T3 Code, as with one sent while pairtime's agent wasn't running, it keeps such a prompt as it keeps any other.

## What this server receives

About once a minute while you work, your computer uploads these:

- **Minute buckets.** For each minute, how long an AI agent was running, and for each 15 seconds of it: how many seconds were credited, to which project key, at which evidence level, why they were credited (such as a prompt you typed, a command you ran or a test run), what kind of work they counted as (prompting, reading the agent's output, supervising it, reviewing, testing, debugging, editing by hand or looking something up), and whether the credit rests on `pairtime track`, an editor plugin or the WakaTime bridge, or on a message you sent through an agent app. Each second that wasn't credited gets a reason code. For each project key, a bucket also says how long an AI agent ran in that project without you getting credit there. So this server learns what kind of work you were doing every 15 seconds, though never what it was about.
- **Batch roots.** Hashes that seal what your computer recorded, so the record can't be changed later without it showing.
- **Commit records.** For each commit, its ID and its parents, its times, how many files and lines changed, the credited time since the commit before it, and whether an AI agent made it. File paths go only as keyed hashes, never as names.
- **Labels.** Your pairtime version, the scoring version, the names and versions of your AI tools, whether the typing module is on, and whether pairtime thinks it runs in a virtual machine.

Each upload also shows this server the IP address it came from. Cloudflare, which sits in front of the server, adds a fingerprint of your connection's TLS settings called JA4. Hack Club's Fraud Squad can look them up when it checks submitted hours, and the server deletes them after {{days "server.ip_ja4"}}. Copies stay in the server's backups until those expire, at most {{days "server.db_snapshots"}} later (see [How long we keep things](#how-long-we-keep-things)).

Some things reach the server only at particular moments:

- **Sign-in.** When you sign in with Hack Club Auth, the server gets your Hack Club Auth ID, your Slack ID, your email address, your name, and whether Hack Club has verified you. It stores your email encrypted. Of your name it keeps only your first name, and only with that sign-in, so its pages can show who you're signed in as. It never stores your full name or your last name, and your first name goes when that sign-in's record does, as [How long we keep things](#how-long-we-keep-things) says.
- **A new computer.** When you link a computer with `pairtime login`, it sends this server the random device ID pairtime made for it when it was installed, its OS family and pairtime version, and the public half of the key that signs its uploads. The server keeps them with a hash of the computer's sign-in token, when you linked it, when it last connected and how many times it has been linked, and how far its uploads have got: the number of its last upload, the number, hash and arrival time of the last batch root it sent, the batch its record starts at, and the numbers of any batches it kept to itself in local-only mode between two sign-ins. Those batches themselves never reach the server.
- **A new project.** When pairtime first sees a git repository, it sends the ID of the repository's first commit. The server turns that into a keyed hash and throws the ID away. It gets the repository's address only if the repository is public, you publish the project, or you install pairtime's GitHub App on it.
- **Project names you choose to show.** A project's name stays on your computer unless you turn it on for that project, with `pairtime projects show-name` or on the Projects page of the pairtime app on your computer. Then your computer sends the name you approved and the project's key, and only your own signed-in dashboard shows the name, in place of the project's code. Turning it off on your computer deletes it from this server at once. Hiding it on your dashboard deletes the name at once too, and the server keeps the project's key and when you hid it until your computer turns the name off as well, {{days "server.project_name_hides"}} at most. When it starts and once a day, your computer also asks which names your dashboard shows, with a request that carries no name.
- **Programs.** When you connect a project to a program, the server stores keyed hashes of the Hackatime project names you choose to link. From then until you unlink the program, it can read, for the projects you linked: your credited time, with the start and end time of each stretch, which includes the time of day your public profile never shows; your totals and how much of them is at each evidence level, for any dates; your username and IDs; and your email address, if you allow it.
- **pairtime's GitHub App**, only if you install it on repositories you pick. GitHub then tells this server about each push to them. For a public repository the server keeps the push as GitHub sends it. For a private one it drops the lists of files each commit changed and keeps the rest: the repository's name, the branch, each commit's ID, message and times, and the names and email addresses of the people who wrote and pushed the commits.
- **Publishing.** When you publish a devlog, a project page or badges, the server gets exactly what the preview showed you. A devlog names its project by the project's key, not its name, and each devlog and badge carries a random ID your computer made for it. When you delete a devlog or remove a badge, the server deletes it at once and keeps only that ID and when you removed it, for {{days "server.removed_items"}}, so a computer of yours that missed the removal can't publish it again.
- **Backups.** Only if you turn them on, the server gets your device key, locked with your recovery code, and encrypted copies of your ledger and vault. It can't open them without your recovery code.

Apart from a submission, what you publish, the encrypted backups if you turn them on, and the project names you choose to show, this server never receives your prompts or the agent's output, file names or paths, window titles, the names of other programs you run, branch names or commit messages of private repositories (unless you install pairtime's GitHub App on one), keystrokes or their timing, prompt similarity sketches, error messages, your computer's name, or your username on it.

These lists are a summary, and `pairtime privacy show` prints every field of the next upload, and all of the next project names request that carries a name, exactly as they will be sent. There's no analytics or telemetry. Apart from what this notice lists, including the project names request your computer makes when the agent starts and once a day, which carries no name unless you turned one on or off, the only request pairtime makes is a daily check with GitHub for updates, and that carries none of your data. `pairtime doctor` also asks the WakaTime server in your `~/.wakatime.cfg` whether it answers, with no key and none of your data.

If you'd rather upload nothing, use local-only mode. pairtime still tracks your time and shows it on your computer, but hours tracked that way can never be submitted to a program.

## Why we use your data

Everything pairtime does with your data falls into one of three groups, each with its own legal basis. A legal basis is the reason data protection laws, such as the EU's GDPR and the UK GDPR, allow a use of your data.

1. **The service you asked for.** Tracking and scoring on your computer, uploads for your own dashboard, session recaps, and the totals on your public profile if you have one. The legal basis is contract, because pairtime needs the data to give you the service you signed up for.
2. **Fraud protection.** Checks, risk scores and audits, and only on hours you submit. pairtime doesn't score your other hours for fraud. The legal basis is legitimate interests. Hack Club and its programs need to know that the hours they reward were really spent building, which keeps the rewards fair for everyone who submits honestly. You can object, as [Your right to object](#your-right-to-object) explains.
3. **Everything else, which needs your consent.** That's the typing module, club boards, habit insights, showing that you're building right now, sharing feedback on recaps to improve scoring, research use of pilot data, and backups with a recovery code. The legal basis is your consent. Each one gets its own card. Nothing is ticked for you, and "Not now" is the most prominent button. Saying no doesn't stop you from using pairtime, and pairtime won't ask again for a year. You can turn any of them off later with one switch in Settings.

One question about consent is still open. On Windows, and on Linux with X11, most minutes count as evidence level B unless the typing module is on, and a high share of level-B minutes is one of the things that raises the chance a person audits a submission. Whether that treats people who said no unfairly is a legal question that hasn't been answered yet.

## Your public profile

{{if .PublicDefault -}}
Profiles on this server are public by default, but nothing about you is public until you answer the profile card. The card shows a preview of your public page, with "Keep my profile public" and "Make it private" as equal buttons.
{{- else -}}
Nothing about you is public until you choose. The first time you sign in, a card asks whether you want a public profile, and "Private profile" is the more prominent button.
{{- end}} Either way, you can switch with one click at any time.

A public profile shows only your handle, your daily totals (never the time of day), your build days and weekly streak, the AI tools and languages you use, projects whose repositories are public with their verified commits, devlogs you published, and badges you picked. It never shows your real name, school, city, age or time zone, your prompts or file names, private repositories, details of your computer, or anything about reviews. It updates at most once an hour. Leaderboards, club boards and "currently building" stay off unless you turn them on.

To take everything down at once, make your profile private. To remove one thing, use the pairtime dashboard on your computer: delete the devlog, remove the badge, or set the project back to private. You can also ask the controller to remove anything you published. Whatever you remove stops being public at once. That doesn't ensure it's gone completely, though. A person, a program or a search engine that copied it while it was public may still have the copy.

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
- If a revealed item shows something it shouldn't, the "This revealed something it shouldn't have" button deletes it from the evidence store at once. If the server can't reach the evidence store just then, reviewers can't open the item any more, and the server deletes it as soon as it can. The minutes it supported stop counting.
- Withdrawing a submission doesn't delete what you revealed. It stays for the spot-check window, counted from when you withdraw, so a withdrawal can't cut short a check that's under way. The button above deletes an item at once.
- A person may audit your submission in a 15-minute walk-through on a Slack huddle. It's never recorded, your camera is never needed, and you can bring a parent or club leader, or choose a written audit instead. Hack Club's safeguarding rules come first where they differ.

## Who else is involved

- **Hack Club Auth** signs you in.
- **Cloudflare** sits in front of this server and handles its encrypted connections, so it sees everything that passes between your computer or browser and this server, such as the numbers your computer uploads and the pages you open. It also runs automated bot detection on requests to this server, and pairtime's pages block the script it adds to them for that. And it asks your browser to report requests to this server that fail, a browser feature called Network Error Logging, so a browser that can't load a page here may send Cloudflare that page's address and the kind of error. What you reveal when you submit hours passes through it encrypted, with the limit [When you submit hours](#when-you-submit-hours) describes.
- **GitHub, GitLab and Forgejo.** The server looks up commits of your public repositories there to check them. Once a day it also publishes a day root to a public GitHub repository. A day root is one hash over that day's uploads and a count of the batches in them, with no name, account or device in it. On a server few people use, that count still shows how many batches they uploaded that day. The people who run a server can leave day roots off. If you install pairtime's GitHub App, GitHub also sends this server your pushes to the repositories you picked, as [What this server receives](#what-this-server-receives) says.
- **A storage provider** keeps this server's backups, and the evidence you reveal when you submit hours. The evidence and the nightly backups are encrypted before they reach it. The continuous backup isn't encrypted with a key of the operator's own, so the storage provider could read it.
- **Programs** you connect a project to can read your time and details for the linked projects, as [What this server receives](#what-this-server-receives) lists, until you unlink them. They also get your submissions to them and their outcomes.
- **A monitoring service** the operator chooses gets a ping every 5 minutes while this server works, and one after each nightly backup, so the operator hears when something stops. The pings carry nothing about you.

Apart from these, this server sends your data nowhere.

Hack Club Auth, Cloudflare and GitHub are based in the United States, and Cloudflare runs data centers around the world. So some of your data is handled outside the UK and the EU, in the United States and elsewhere. When personal data leaves the UK or the EU, the law asks for a safeguard, such as the standard contractual clauses the UK and the EU publish. The safeguards the controller relies on are **\[not named yet\]**.

## How long we keep things

Every kind of data has a time limit. These tables come from the same file this server's cleanup jobs read, {{.RetentionFile}}, as it was when this server was built. A spot-check window is the time after a decision during which Hack Club can check a submission again. {{if .WindowProvisional}}Until Hack Club sets its length, pairtime keeps it open for {{.WindowMonths}} months after the decision.{{else}}It stays open for {{.WindowMonths}} months after the decision.{{end}} If you withdraw a submission before it's decided, its window starts when you withdraw it, and if it's never decided or withdrawn, when you submitted it.
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
- "Delete everything" wipes pairtime's data on your computer, including its device key, and the names you gave the projects that computer tracked. Names that another computer sharing your config.toml wrote stay, and pairtime says how many. Your other settings stay, and so does install's record of what it changed, with its copies of the files it edited, so that uninstall can put them back. It sends nothing to this server, so a name your dashboard shows stays here until you hide it on your dashboard or remove that computer there.

Through the controller, you can:

- get a copy of your data, in a file you can take to another service
- have it corrected if it's wrong
- have it deleted
- have the controller stop using it, apart from keeping it, while a question about it is sorted out
- object to its use for fraud protection, as [Your right to object](#your-right-to-object) explains
- withdraw any consent you gave, which stops that use from then on

Deleting your account deletes everything this server holds about you, except the minimal fraud record and the hashes behind the day roots of your batches, both in the table above, which the deletion report lists as kept. If the server can't reach the evidence store just then, the report also lists what you revealed as not deleted yet, and the server deletes it as soon as it can. Copies in the server's backups go as the backups expire, within {{days "server.db_snapshots"}}. Copies that programs already received, such as the outcome of a submission, are held by those programs, not by this server.{{if .PrivacyHub}} Hack Club's privacy team can also delete your pairtime data for you, or tell you what this server holds of it, through the same privacy hub it uses for Hackatime.{{end}}

Parents and guardians can contact the controller with questions or requests too.

If you think the controller is handling your data wrongly, you can complain to a data protection regulator. In the UK that's the Information Commission, and in the EU it's the data protection authority of the country you live in.

## Your right to object

Fraud protection rests on legitimate interests, not on your consent, so you have the right to object to it. Tell the controller why. The controller then has to stop using your data for it, unless it has compelling reasons that outweigh your interests and rights, or needs the data to make or defend a legal claim. Fraud checks only ever run on hours you submit, so hours you never submit are never checked.

## Age

pairtime on this server is for Hack Clubbers aged 13 to 18. Hack Club Auth decides who is eligible, so pairtime never asks for your birthdate or stores it. If Hack Club Auth marks you as not eligible, sign-in is refused, and submitting hours needs a verified Hack Club account. If you're under 13, Hack Club's rules don't let you use its services, this one included. Hours you log on or after your 19th birthday don't earn credit in Hack Club programs.

From 2027, if your computer's operating system can share an age range, pairtime asks for it once. The range stays on your computer and is never sent anywhere, and you or a parent can decline to share it. If it says you're under 13, pairtime stops tracking and deletes its data on your computer.

## Changes to this notice

If pairtime ever starts uploading a new kind of data, the change goes through a public review in pairtime's repository first, and this notice changes with it. This page was built by pairtime {{.Version}} from this server's settings.
