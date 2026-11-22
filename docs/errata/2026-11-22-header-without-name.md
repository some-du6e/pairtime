# Erratum: the header falls back to an id when a sign-in brings no name

Date 2026-11-22. Raised on 2026-11-22, when the live server's owner shared a screenshot of their own dashboard in the bounty's public thread. Status: decided 2026-11-22 by the orchestrator for Karim. It changes one line of D-display-name (docs/errata/2026-11-21-design-4.4-display-name.md), which stays as it was, and nothing in DESIGN.

## Input

- The screenshot's header said "Signed in as" and then the account's Slack ID. The session was opened before pairtimed asked Hack Club Auth for the `profile` scope, so it holds no display name, and D-display-name's fallback showed the Slack ID in its place.
- D-display-name, "What changes": "The header says "Signed in as <name>", and without a name it falls back to the Slack ID, or the HCA subject, as before."
- The header is on every page a signed-in browser opens, so the id was in every screenshot of any of them. It reads like a debug page, and it ties a screenshot to one Slack account.
- The pages that ask whether an account is yours need the id, because anyone can pick a name and nobody can pick a Slack ID: `/device`, its confirmation, relink and taken pages, and `/account/devices`.

## Expected

The header names the person when their sign-in brought a name, and shows no id at all when it didn't. The pages that ask whether an account is yours still show the id in their text.

## What changes

- With a display name, the header says "Signed in as <name>", as now.
- Without one, the header shows no name and no id: the link to the dashboard and the Sign out button alone. It never puts the Slack ID, the Hack Club Auth subject or the email address in a name's place.
- `/device`, its confirmation, relink and taken pages, and `/account/devices` keep naming the account in their text as D-display-name says: "<name> (Slack ID <id>)", or "<name> (Hack Club Auth ID <sub>)" for an account without a Slack ID, and the Slack ID or subject alone when the sign-in brought no name. Their header follows the rule above.
- Signing in again brings the name, now that pairtimed asks for `profile`.

## For Karim

The orchestrator decided this for you, so nothing waits on you here. No DESIGN text names the header's fallback, so nothing else changes.
