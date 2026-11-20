<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { WorkSession } from '../api.ts';
	import { buildProject, clockLine, scaleOf } from '../model.ts';
	import * as text from '../text.ts';
	import Hands from './Hands.svelte';
	import Rows from './Rows.svelte';

	// The last work session, or the one still going, from GET
	// /v1/sessions/last, worded as `pairtime recap` words it. Moments and
	// loops join it with the coach (WP-CO1).
	interface Props {
		session: WorkSession | null;
		calibrating: boolean;
	}

	let { session, calibrating }: Props = $props();
	const uid = $props.id();
	const view = $derived(session ? buildProject(session.project, session.name, session, calibrating) : undefined);
</script>

<section class="pt-card session" aria-labelledby="{uid}-title" data-testid="session">
	{#if session && view}
		<h2 id="{uid}-title">
			{text.phrase(session.open ? 'session_open' : 'session_last', { project: view.title })}
		</h2>
		<p class="when" data-testid="session-span">
			{text.sessionSpan(new Date(session.start), new Date(session.end), session.open)}
		</p>
		<p class="line">{clockLine(view)}</p>
		<Hands {view} scale={scaleOf([view])} />
		{#if view.other.length > 0}
			<h3>The rest of the time</h3>
			<Rows rows={view.other.map((o) => ({ key: o.reason, words: o.words, s: o.s }))} />
		{/if}
	{:else}
		<h2 id="{uid}-title">Your last session</h2>
		<p data-testid="no-session">{text.phrase('no_session')}</p>
	{/if}
</section>

<style>
	.session {
		max-width: none;
	}

	.when,
	.line {
		color: var(--pt-muted);
	}

	h3 {
		margin-top: 1rem;
		font-size: 1rem;
	}
</style>
