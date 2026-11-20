<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Days } from '../api.ts';
	import Hands from '../components/Hands.svelte';
	import Rows from '../components/Rows.svelte';
	import { shadowWords } from '../copy.ts';
	import { longDate } from '../format.ts';
	import { clocks, kindRows, reasonRows, scaleOf, soleAgent } from '../model.ts';
	import { href, type Range } from '../route.ts';

	// Projects (docs/PLAN.md, WP-UI1): each project's clock over this week or
	// the last 30 days, your time by kind, and what pairtime didn't count,
	// by reason, in plain words. While shadow is on only the agents' time
	// shows (DESIGN 6.7).
	interface Props {
		days: Days;
		range: Range;
	}

	let { days, range }: Props = $props();
	const uid = $props.id();

	const list = $derived(clocks(days.days, days.projects, days.shadow));
	const scale = $derived(scaleOf(list));
	const spans: { range: Range; label: string }[] = [
		{ range: 'week', label: 'This week' },
		{ range: 'month', label: 'Last 30 days' },
	];
</script>

<h1>Projects</h1>
<nav class="spans" aria-label="Span of days">
	{#each spans as s (s.range)}
		<a href={href({ page: 'projects', range: s.range })} aria-current={range === s.range ? 'page' : undefined}>{s.label}</a>
	{/each}
</nav>
<p class="pt-note" data-testid="span">
	From <time datetime={days.from}>{longDate(days.from)}</time> to <time datetime={days.to}>{longDate(days.to)}</time>
</p>

{#if days.shadow}
	<div class="pt-card note" data-testid="calibrating">
		<p>{shadowWords}</p>
	</div>
{/if}

{#each list as c, i (c.key)}
	{@const left = reasonRows(c.uncredited, soleAgent(c))}
	{@const byKind = kindRows(c.byKind)}
	<section class="pt-card project" aria-labelledby="{uid}-{i}" data-testid="project">
		<h2 id="{uid}-{i}">{c.name}</h2>
		<Hands clock={c} {scale} shadow={days.shadow} />
		{#if !days.shadow && byKind.length > 0}
			<h3>Your time by kind</h3>
			<Rows rows={byKind} testid="kinds" />
		{/if}
		{#if !days.shadow && left.length > 0}
			<h3>What pairtime didn't count</h3>
			<Rows rows={left} testid="reasons" />
		{/if}
	</section>
{:else}
	<p data-testid="no-projects">
		{range === 'week' ? 'No projects yet this week.' : 'No projects in the last 30 days.'} When you work on a project with
		an AI agent, it shows up here.
	</p>
{/each}

<style>
	.spans {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem 1.25rem;
		margin: 0 0 0.5rem;
	}

	.spans a[aria-current='page'] {
		font-weight: 700;
		color: var(--pt-text);
		text-decoration-thickness: 3px;
	}

	.note,
	.project {
		max-width: none;
	}

	.note p:last-child {
		margin: 0;
	}

	h2 {
		overflow-wrap: anywhere;
	}

	h3 {
		margin-top: 1.25rem;
		font-size: 1rem;
	}
</style>
