<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Day } from '../api.ts';
	import Hands from '../components/Hands.svelte';
	import Rows from '../components/Rows.svelte';
	import { clockLine, projects, scaleOf } from '../model.ts';
	import { href, type Range } from '../route.ts';
	import * as text from '../text.ts';

	// Projects (docs/PLAN.md, WP-UI1): each project's clock over this week or
	// the last 30 days, your time by kind, and the rest of the time by
	// reason, in en.json's words, as `pairtime today` lists them for a day.
	// While calibrating only the agents' time and the rest show (DESIGN 6.7).
	interface Props {
		/** The span's days that came back, in date order. */
		days: Day[];
		range: Range;
		/** The span's first and last dates. */
		from: string;
		to: string;
	}

	let { days, range, from, to }: Props = $props();
	const uid = $props.id();

	const calibrating = $derived(days.some((d) => d.shadow));
	const list = $derived(projects(days, calibrating));
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
	From <time datetime={from}>{text.day(from)}</time> to <time datetime={to}>{text.day(to)}</time>
</p>

{#if calibrating}
	<div class="pt-card note" data-testid="calibrating">
		<p>{text.phrase('calibrating_note')}</p>
	</div>
{/if}

{#each list as v, i (v.key)}
	<section class="pt-card project" aria-labelledby="{uid}-{i}" data-testid="project">
		<h2 id="{uid}-{i}">{v.title}</h2>
		<p class="line" data-testid="clock-line">{clockLine(v)}</p>
		<Hands view={v} {scale} />
		{#if v.you && v.you.kinds.length > 0}
			<h3>What your time went on</h3>
			<Rows rows={v.you.kinds.map((k) => ({ key: k.kind, words: k.words, s: k.s }))} testid="kinds" />
		{/if}
		{#if v.other.length > 0}
			<h3>The rest of the time</h3>
			<Rows rows={v.other.map((o) => ({ key: o.reason, words: o.words, s: o.s }))} testid="other" />
		{/if}
	</section>
{:else}
	<p data-testid="no-projects">
		{range === 'week' ? 'Nothing tracked yet this week.' : 'Nothing tracked in the last 30 days.'} When you work on a project
		with an AI agent, it shows up here.
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
		margin-bottom: 0.125rem;
		overflow-wrap: anywhere;
	}

	.line {
		color: var(--pt-muted);
	}

	h3 {
		margin-top: 1.25rem;
		font-size: 1rem;
	}
</style>
