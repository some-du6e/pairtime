<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { WorkSession } from '../api.ts';
	import { unreadSession } from '../copy.ts';
	import { buildProject, clockLine, otherRows, scaleOf, sessionHead, sessionWhen } from '../model.ts';
	import * as text from '../text.ts';
	import Hands from './Hands.svelte';
	import Rows from './Rows.svelte';

	// The last work session, or the one still going, from GET
	// /v1/sessions/last, worded as `pairtime recap` words it. While
	// calibrating, a session is its project and day alone, and the rest of
	// its time SM 4.2's loops alone: with when it started and ended, the rest
	// would give its hidden credited time away (D-shadow-scope; DESIGN 6.7
	// step 2). Moments and loops join it with the coach (WP-CO1).
	interface Props {
		/**
		 * GET /v1/sessions/last's session: null before the first one, and
		 * undefined when the agent's answer didn't come back.
		 */
		session: WorkSession | null | undefined;
		/** display.shadow, from the session's answer or the day's. */
		calibrating: boolean;
	}

	let { session, calibrating }: Props = $props();
	const uid = $props.id();
	const view = $derived(session ? buildProject(session.project, session.name, session, calibrating) : undefined);
	const when = $derived(session ? sessionWhen(session, calibrating) : '');
	const rest = $derived(view ? otherRows(view) : []);
</script>

<section class="pt-card session" aria-labelledby="{uid}-title" data-testid="session">
	{#if session && view}
		<h2 id="{uid}-title">{sessionHead(session, view.title, calibrating)}</h2>
		{#if when !== ''}
			<p class="when" data-testid="session-span">{when}</p>
		{/if}
		<p class="line" data-testid="session-clock">{clockLine(view)}</p>
		<Hands {view} scale={scaleOf([view])} />
		{#if rest.length > 0}
			<h3>The rest of the time</h3>
			<Rows rows={rest} />
		{/if}
	{:else if session === null}
		<h2 id="{uid}-title">Your last session</h2>
		<p data-testid="no-session">{text.phrase('no_session')}</p>
	{:else}
		<h2 id="{uid}-title">Your last session</h2>
		<p data-testid="session-unread">{unreadSession()}</p>
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
