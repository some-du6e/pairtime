<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Days, Focus } from '../api.ts';
	import Bar from '../components/Bar.svelte';
	import Clock from '../components/Clock.svelte';
	import Hands from '../components/Hands.svelte';
	import HowItCounts from '../components/HowItCounts.svelte';
	import Rows from '../components/Rows.svelte';
	import { calibrating, focusWords, shadowWords } from '../copy.ts';
	import { dayLabel, duration, longDate } from '../format.ts';
	import { agentParts, clocks, combine, dayRows, reasonRows, scaleOf, yourParts } from '../model.ts';

	// Today (DESIGN 3.9; docs/PLAN.md, WP-UI1): the chess clock for the day
	// and for each project (DESIGN 3.8), pending minutes drawn lighter,
	// level-C time labeled, what pairtime didn't count in plain words, and
	// the week so far. While the desktop hides which window is in front it
	// says so first (D-focus-unknown).
	interface Props {
		days: Days;
		/** From GET /v1/status; absent until the agent reports it (WP-AG6). */
		focus?: Focus | undefined;
	}

	let { days, focus }: Props = $props();

	const day = $derived(days.days.find((d) => d.date === days.today));
	const projects = $derived(day ? clocks([day], days.projects, days.shadow) : []);
	const all = $derived(combine(projects));
	const scale = $derived(scaleOf(projects));
	const left = $derived(day ? reasonRows(day.uncredited) : []);
	const week = $derived(dayRows(days.days));
	const weekAll = $derived(combine(week.map((r) => r.clock)));
	const weekScale = $derived(Math.max(0, ...week.map((r) => Math.max(r.clock.credited, r.clock.agentRunning))));
</script>

<h1>Today</h1>
<p class="date"><time datetime={days.today}>{longDate(days.today)}</time></p>

{#if focus && !focus.known}
	<div class="pt-card note" data-testid="focus-unknown">
		{#each focusWords(focus) as line (line)}
			<p>{line}</p>
		{/each}
	</div>
{/if}

{#if days.shadow}
	<div class="pt-card note" data-testid="calibrating">
		<p>{shadowWords}</p>
	</div>
{/if}

<section class="pt-card wide" aria-labelledby="today-title" data-testid="today">
	<h2 id="today-title">Your time today</h2>
	{#if days.shadow}
		<p class="big">{calibrating}</p>
	{:else}
		<p class="big" data-testid="today-you">{duration(all.credited)}</p>
	{/if}
	{#if all.credited > 0 || all.agentRunning > 0}
		<Hands clock={all} scale={scaleOf([all])} shadow={days.shadow} />
	{/if}
</section>

<section aria-labelledby="projects-title" class="projects">
	<h2 id="projects-title">Project by project</h2>
	{#each projects as c (c.key)}
		<Clock clock={c} {scale} shadow={days.shadow} />
	{:else}
		<p data-testid="nothing-yet">
			Nothing yet today. When you work on a project with an AI agent, your time shows up here as you go.
		</p>
	{/each}
</section>

{#if !days.shadow && left.length > 0}
	<section class="pt-card wide" aria-labelledby="left-title" data-testid="left-out">
		<h2 id="left-title">What pairtime didn't count today</h2>
		<p class="pt-note">pairtime looked at this time and left it out. Here is what it was.</p>
		<Rows rows={left} />
	</section>
{/if}

<section class="pt-card wide" aria-labelledby="week-title" data-testid="week">
	<h2 id="week-title">This week</h2>
	{#if week.length === 0}
		<p>Nothing yet this week.</p>
	{:else}
		<p>
			Since <time datetime={days.from}>{longDate(days.from)}</time>: you
			<span data-testid="week-you">{days.shadow ? calibrating.toLowerCase() : duration(weekAll.credited)}</span>,
			your agents <span data-testid="week-agents">{duration(weekAll.agentRunning)}</span>.
		</p>
		<table>
			<caption class="pt-visually-hidden">Each day this week with your time or your agents' time on it</caption>
			<thead>
				<tr>
					<th scope="col">Day</th>
					<th scope="col">You</th>
					<th scope="col">Your agents</th>
				</tr>
			</thead>
			<tbody>
				{#each week as r (r.date)}
					<tr data-date={r.date}>
						<th scope="row">{dayLabel(r.date, days.today)}</th>
						<td>
							{#if days.shadow}
								{calibrating}
							{:else}
								<span class="time">{duration(r.clock.credited)}</span>
								<Bar parts={yourParts(r.clock)} scale={weekScale} />
							{/if}
						</td>
						<td>
							<span class="time">{duration(r.clock.agentRunning)}</span>
							<Bar parts={agentParts(r.clock.agentRunning)} scale={weekScale} />
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</section>

<HowItCounts pendingWindowS={days.pending_window_s} />

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
		font-size: 2.25rem;
		font-weight: 700;
		line-height: 1.1;
		font-variant-numeric: tabular-nums;
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

	td {
		width: 40%;
	}

	.time {
		display: inline-block;
		min-width: 4.5rem;
		font-variant-numeric: tabular-nums;
	}
</style>
