# Erratum: an hourly public profile shows when in the day its owner works, and where

Date 2026-11-24. Raised by WP-SV8's self-review of #286. Status: decided 2026-11-24 by the orchestrator for Karim (D-public-daily), as the Decision section below records. The PR that adds this file applies it to DESIGN 8.3, the R5 row, the 3.10 row, the tier table and PLAN's WP-SV8 and WP-SV9.

## Input

- DESIGN 8.3's public-page bullet (DESIGN.md:1069): the page shows "daily credited totals at level A or B (6.2 D11), with no hour-of-day detail", and never "a real name, school, city, age or time zone". "It refreshes at most hourly; the owner's dashboard is the real-time one."
- DESIGN 8.3's written assessment. Item 3 (:1077): "What a stranger can learn: on which days a handle built and for how long; which public repositories it pushed to. Not when in the day, not where, not who." Item 4 (:1078) lists "hourly refresh" among the mitigations.
- The R5 row (:118): "The public view refreshes hourly; the owner's view is real time". DESIGN 3.10's "Rollups and live dashboard" row (:313): "`profile:` topics carry coalesced hourly totals (SP 6.4)". The tier table (:420): "public aggregates hourly (8.3)".
- PLAN's WP-SV8 (:787 and :788): "`profile:` topics carry totals coalesced to once an hour", which its CI checks hold to "a public topic never emits twice within an hour". WP-SV9 (:812) builds the page with an "hourly refresh".

## What an hourly watcher learns

A viewer who keeps a `profile:` topic open, or reloads the page every hour, reads today's total once an hour. The difference between two readings is the time the owner added in that UTC hour, so a few days of readings give the hours they work, to the hour. That is when in the day, which item 3 says a stranger can't learn.

The days are local dates in the profile's time zone (6.2 D10). The first reading in which a new date appears, or the oldest date leaves the 30-day window, puts the owner's local midnight between two UTC hours, and that gives their UTC offset to within an hour. That is close to where, and the page never shows a time zone. One night the owner works across their midnight is enough. A reading at 13:00 UTC that already holds tomorrow's UTC date shows an offset of +11 or more at once.

## Options

1. Keep the hourly refresh, and have items 3 and 4 say what an hourly watcher learns.
2. Change the public page once a day at one fixed UTC hour, and show a local date only once it has ended in every time zone, so nothing on the page moves at the owner's own hours or at their midnight.

## Decision

Option 2. The public profile changes once a day, at 12:00 UTC, and shows a local date only once that date has ended in every time zone, which is by 12:00 UTC on the next day (UTC-12 ends last). Daily totals, build days and the weekly-goal streak follow that schedule. A date's later changes, such as a revision received within D10's 72 hours or a minute going final, show at the next 12:00 UTC. What the teen adds to the page, a devlog published, a badge shown or a project made public, also appears at the next 12:00 UTC, and the owner's dashboard says when it will. Removals take effect at once, as 8.3 requires: a deletion, a hide, a badge removed, a project made private and "Make it private". Their timing is the teen's own deliberate act. "Currently building" presence stays opt-in and live, as 8.3 has it, because the teen chooses to show live activity. The `profile:` topic emits only at 12:00 UTC, at most once a day, when what the page shows changed, and never a date that hasn't ended in every zone, plus at once for a removal.

BOUNTY's R5 asks for real-time updates only on the stats dashboard, which the owner's dashboard meets, and DESIGN 8.3 already allows "at most hourly". So a once-a-day public page meets R5 and keeps 8.3's promise for minors that the page shows neither when in the day they work nor where they live. Publishing a date when it ends in the owner's own zone would itself give the zone away, so every date waits until it has ended everywhere. Publishing at one fixed UTC hour means nothing moves at the owner's own hours. A teen's own additions follow the same schedule, because an addition appearing at 21:14 shows the teen was online then. Removals can't wait, by law and by 8.3, and they show only that the teen chose to take something down.

## Applied

- DESIGN 8.3's public-page bullet: the page changes once a day at 12:00 UTC, shows a local date only once it has ended in every time zone, shows what the teen adds at the next 12:00 UTC and takes every removal at once. The owner's dashboard is the real-time one.
- Item 4's "hourly refresh" becomes "a once-a-day refresh at a fixed UTC hour that shows only dates ended in every zone". Item 3 stays as written, since it now holds.
- The R5 row: "The public view changes once a day". The 3.10 row: "`profile:` topics carry what the public page shows, once a day at 12:00 UTC and at once for a removal". The tier table: "public aggregates once a day".
- PLAN's WP-SV8: its Build line and the CI check above say the new schedule. WP-SV9: its Build line follows this schedule, and a new CI check holds a devlog published at any hour to the next 12:00 UTC while a deletion disappears at once.
- WP-SV8 (#286) implements `stream.PublicTotals` and the `profile:` topic to it. `profile:` topics answer 404 until WP-SV9 gives pairtimed its profiles, and WP-SV9 builds the page to it.
