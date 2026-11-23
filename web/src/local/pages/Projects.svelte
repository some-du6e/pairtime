<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Day, ProjectEntry, Projects } from '../api.ts';
	import Hands from '../components/Hands.svelte';
	import NameSwitch from '../components/NameSwitch.svelte';
	import Rows from '../components/Rows.svelte';
	import { unreadDays } from '../copy.ts';
	import { clockLine, entryTitle, otherRows, projects, projectTitle, scaleOf } from '../model.ts';
	import { href, type Range } from '../route.ts';
	import * as text from '../text.ts';

	// Projects (docs/PLAN.md, WP-UI1): each project's clock over this week or
	// the last 30 days, your time by kind, and the rest of the time by
	// reason, in en.json's words, as `pairtime today` lists them for a day.
	// While calibrating only the agents' time and SM 4.2's loops show
	// (DESIGN 6.7; D-shadow-scope). Each project has the switch that shows
	// its name on the owner's own pairtime dashboard, from GET /v1/projects
	// (D-owner-names), as do the projects on this computer the span holds
	// no time of, listed after the rest, as `pairtime projects` lists them.
	interface Props {
		/** The span's days that came back, in date order. */
		days: Day[];
		/** The span's earlier dates whose answer didn't come back, so the totals leave them out. */
		unread?: string[];
		range: Range;
		/** The span's first and last dates. */
		from: string;
		to: string;
		/** GET /v1/projects, or undefined until it comes back, when no switch shows. */
		names?: Projects | undefined;
		/** The projects whose switch's change is on its way to the agent, by key. */
		busy?: ReadonlySet<string>;
		/** Why the agent didn't take a switch's last change, by key. */
		refused?: ReadonlyMap<string, string>;
		/** Asks the agent to turn a project's name on or off. */
		onname?: (entry: ProjectEntry, online: boolean) => void;
	}

	let {
		days,
		unread = [],
		range,
		from,
		to,
		names,
		busy = new Set<string>(),
		refused = new Map<string, string>(),
		onname = () => {},
	}: Props = $props();
	const uid = $props.id();
	const entries = $derived(new Map((names?.projects ?? []).map((e): [string, ProjectEntry] => [e.project, e])));

	const calibrating = $derived(days.some((d) => d.shadow));
	const list = $derived(
		projects(days, calibrating).map((v) => ({
			v,
			kinds: (v.you?.kinds ?? []).filter((k) => k.s >= 1).map((k) => ({ key: k.kind, words: k.words, s: k.s })),
			rest: otherRows(v),
		})),
	);
	const scale = $derived(scaleOf(list.map((p) => p.v)));
	// The projects on this computer the span holds no time of.
	const others = $derived((names?.projects ?? []).filter((e) => !list.some((p) => p.v.key === e.project)));
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
{#if unread.length > 0}
	<p class="pt-note" data-testid="unread-days">{unreadDays(unread)}</p>
{/if}

{#if calibrating}
	<div class="pt-card note" data-testid="calibrating">
		<p>{text.phrase('calibrating_note')}</p>
	</div>
{/if}

{#each list as { v, kinds, rest }, i (v.key)}
	{@const entry = entries.get(v.key)}
	<section class="pt-card project" aria-labelledby="{uid}-{i}" data-testid="project">
		<h2 id="{uid}-{i}">{projectTitle(v, entry)}</h2>
		<p class="line" data-testid="clock-line">{clockLine(v)}</p>
		<Hands view={v} {scale} />
		{#if entry && names}
			<NameSwitch {entry} online={names.online} busy={busy.has(entry.project)} problem={refused.get(entry.project) ?? ''} onchange={onname} />
		{/if}
		{#if kinds.length > 0}
			<h3>What your time went on</h3>
			<Rows rows={kinds} testid="kinds" />
		{/if}
		{#if rest.length > 0}
			<h3>The rest of the time</h3>
			<Rows rows={rest} testid="other" />
		{/if}
	</section>
{:else}
	<p data-testid="no-projects">
		{range === 'week' ? 'Nothing tracked yet this week.' : 'Nothing tracked in the last 30 days.'} When you work on a project
		with an AI agent, it shows up here.
	</p>
{/each}

{#if names && others.length > 0}
	<section class="pt-card others" aria-labelledby="{uid}-others" data-testid="other-projects">
		<h2 id="{uid}-others">Other projects on this computer</h2>
		<ul>
			{#each others as entry (entry.project)}
				<li data-testid="other-project">
					<h3>{entryTitle(entry)}</h3>
					<NameSwitch {entry} online={names.online} busy={busy.has(entry.project)} problem={refused.get(entry.project) ?? ''} onchange={onname} />
				</li>
			{/each}
		</ul>
	</section>
{/if}

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
	.project,
	.others {
		max-width: none;
	}

	.others ul {
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.others li {
		padding: 0.75rem 0;
		border-top: 1px solid var(--pt-track);
	}

	.others li:first-child {
		border-top: 0;
	}

	.others h3 {
		margin: 0;
		overflow-wrap: anywhere;
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
