<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Day, Focus, LastSession } from '../api.ts';
	import Bar from '../components/Bar.svelte';
	import Clock from '../components/Clock.svelte';
	import Hands from '../components/Hands.svelte';
	import HowItCounts from '../components/HowItCounts.svelte';
	import Rows from '../components/Rows.svelte';
	import Session from '../components/Session.svelte';
	import { focusWords } from '../copy.ts';
	import { clockLine, dayLabel, projects, scaleOf, whole, yourParts } from '../model.ts';
	import * as text from '../text.ts';

	// Today (DESIGN 3.9; docs/PLAN.md, WP-UI1): the chess clock for the day
	// and for each project (DESIGN 3.8) in the words `pairtime today` uses,
	// pending minutes drawn lighter, level-C time labeled, the last work
	// session, the week so far, and how pairtime counts. While the desktop
	// hides which window is in front it says so first (D-focus-unknown).
	interface Props {
		/** GET /v1/days/today. */
		today: Day;
		/** This week's days up to today, the ones that came back. */
		week: Day[];
		/** GET /v1/sessions/last, or undefined until it comes back. */
		session?: LastSession | undefined;
		/** From GET /v1/status; absent until the agent reports it (WP-AG6). */
		focus?: Focus | undefined;
	}

	let { today, week, session, focus }: Props = $props();

	const calibrating = $derived(today.shadow);
	const day = $derived(whole([today], calibrating, 'today'));
	const list = $derived(projects([today], calibrating));
	const scale = $derived(scaleOf(list));
	// Only days with something on them, so the week shows what you did and
	// never a gap to fill.
	const days = $derived(
		week.filter((d) => d.projects.length > 0).map((d) => ({ date: d.date, view: whole([d], calibrating, d.date) })),
	);
	const weekView = $derived(whole(week, calibrating, 'this week'));
	const weekScale = $derived(scaleOf(days.map((r) => r.view)));
</script>

<h1>Today</h1>
<p class="date"><time datetime={today.date}>{text.day(today.date)}</time></p>

{#if focus && !focus.known}
	<div class="pt-card note" data-testid="focus-unknown">
		{#each focusWords(focus) as line (line)}
			<p>{line}</p>
		{/each}
	</div>
{/if}

{#if calibrating}
	<div class="pt-card note" data-testid="calibrating">
		<p>{text.phrase('calibrating_note')}</p>
	</div>
{/if}

<section class="pt-card wide" aria-labelledby="today-title" data-testid="today">
	<h2 id="today-title">{text.phrase('your_time')} today</h2>
	{#if today.projects.length === 0}
		<p data-testid="nothing-yet">{text.phrase('nothing_today')}</p>
	{:else}
		<p class="big" data-testid="today-clock">{clockLine(day)}</p>
		<Hands view={day} scale={scaleOf([day])} />
		{#if day.other.length > 0}
			<h3>The rest of the time</h3>
			<Rows rows={day.other.map((o) => ({ key: o.reason, words: o.words, s: o.s }))} testid="other" />
		{/if}
	{/if}
</section>

{#if list.length > 0}
	<section aria-labelledby="projects-title" class="projects">
		<h2 id="projects-title">Project by project</h2>
		{#each list as v (v.key)}
			<Clock view={v} {scale} />
		{/each}
	</section>
{/if}

{#if session}
	<Session session={session.session} calibrating={session.shadow} />
{/if}

<section class="pt-card wide" aria-labelledby="week-title" data-testid="week">
	<h2 id="week-title">This week</h2>
	{#if days.length === 0}
		<p>Nothing tracked yet this week.</p>
	{:else}
		<p data-testid="week-clock">{clockLine(weekView)}</p>
		<table>
			<caption class="pt-visually-hidden">Each day this week with time on it</caption>
			<thead>
				<tr>
					<th scope="col">Day</th>
					<th scope="col">Clock</th>
					<th scope="col"><span class="pt-visually-hidden">Your time</span></th>
				</tr>
			</thead>
			<tbody>
				{#each days as r (r.date)}
					<tr data-date={r.date}>
						<th scope="row">{dayLabel(r.date, today.date)}</th>
						<td class="line">{clockLine(r.view)}</td>
						<td class="bar">
							{#if r.view.you}
								<Bar parts={yourParts(r.view.you)} scale={weekScale} />
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</section>

<HowItCounts />

<style>
	.date {
		color: var(--pt-muted);
	}

	.note {
		max-width: none;
		border-color: var(--pt-accent);
	}

	.note p:last-child {
		margin: 0;
	}

	.wide {
		max-width: none;
	}

	.big {
		margin: 0 0 0.75rem;
		font-size: 1.5rem;
		font-weight: 700;
		line-height: 1.2;
		font-variant-numeric: tabular-nums;
	}

	h3 {
		margin-top: 1rem;
		font-size: 1rem;
	}

	.projects {
		margin: 0 0 var(--pt-gap);
	}

	table {
		width: 100%;
		border-collapse: collapse;
	}

	th,
	td {
		padding: 0.375rem 0.75rem 0.375rem 0;
		text-align: left;
		vertical-align: middle;
	}

	thead th {
		color: var(--pt-muted);
		font-weight: 600;
	}

	tbody th {
		font-weight: 400;
		white-space: nowrap;
	}

	.line {
		font-variant-numeric: tabular-nums;
	}

	.bar {
		width: 35%;
	}
</style>
