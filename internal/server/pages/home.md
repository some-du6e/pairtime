{{- /* SPDX-License-Identifier: AGPL-3.0-or-later */ -}}
# pairtime counts the time you spend building with AI

Not the time your AI spends.

Thinking, writing prompts, reading what the agent did, testing and fixing all count. The time your agent works on its own doesn't, however long it runs. When you ship a project to a Hack Club program, the program can use the hours pairtime counted.

[See a sample dashboard](/demo)

{{if .SignupsOpen -}}
> **Early pilot.** Sign-ups on this server are open.{{if .SignIn}} Sign in with Hack Club to make your account.{{else}} Sign-in isn't set up on this server yet, though.{{end}}
{{- else -}}
> **Early pilot.** Sign-ups are closed for now, and testers join by invitation.{{if .SignIn}} Until sign-ups open, this server makes accounts only for the people who run it.{{else}} Sign-in isn't set up on this server yet.{{end}}
{{- end}}

## How it counts

pairtime runs on your computer, and hooks in your AI tools tell it each time you send a prompt or answer your agent. A second counts only when something you did started it, your computer saw you there, and that project was in front.

Each counted second gets an [evidence level](/docs/evidence): A when your keyboard backs it up, B when your computer saw you but can't tell whether you were typing, and C for time pairtime can check less closely, like time it reads back later from your tools' history.

What you type stays on your computer, apart from a few prompts you look over first if you submit hours to a program.

## Read more

- [Privacy notice](/privacy). What this server keeps about you, for how long, and how to use your rights.
- [Evidence levels](/docs/evidence). What A, B and C mean, in the same words reviewers read.
- [Source code on GitHub]({{.Source}}). pairtime is free software under the AGPL-3.0 license, so anyone can read the code and check what it does.
