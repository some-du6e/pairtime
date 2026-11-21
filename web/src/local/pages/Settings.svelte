<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { describeMode, type Status } from '../api.ts';

	// Settings (DESIGN 3.9; docs/PLAN.md, WP-UI1), read-only in v0: the
	// mode, whether calibration hides your numbers (display.shadow, DESIGN
	// 6.7), and how the agent is doing. Pausing for an hour, exclusions and
	// the adapters list need the agent's side first and come later.
	interface Props {
		status: Status;
		/** display.shadow from GET /v1/days, or undefined when that failed. */
		shadow: boolean | undefined;
	}

	let { status, shadow }: Props = $props();
	const problems = $derived(Object.entries(status.problems ?? {}));
</script>

<h1>Settings</h1>

<section class="pt-card" aria-labelledby="mode-title" data-testid="mode">
	<h2 id="mode-title">Mode</h2>
	<p>{describeMode(status.mode)}</p>
	<p class="pt-note">Signing in, so your time can show on a profile or go to a program, comes in a later version.</p>
</section>

<section class="pt-card" aria-labelledby="shadow-title" data-testid="shadow">
	<h2 id="shadow-title">Calibration</h2>
	{#if shadow === undefined}
		<p>pairtime couldn't say right now.</p>
	{:else if shadow}
		<p>On. You're helping tune pairtime's numbers, so your time stays hidden for now. Your agents' time still shows.</p>
	{:else}
		<p>Off. Your time shows as pairtime counts it.</p>
	{/if}
</section>

<section class="pt-card" aria-labelledby="agent-title" data-testid="agent-status">
	<h2 id="agent-title">The pairtime agent</h2>
	<dl>
		<dt>Version</dt>
		<dd data-testid="agent-version">{status.version.version}</dd>
		<dt>Running since</dt>
		<dd><time datetime={status.started}>{new Date(status.started).toLocaleString()}</time></dd>
	</dl>
	{#if problems.length > 0}
		<h3>Needs your attention</h3>
		<ul data-testid="problems">
			{#each problems as [source, message] (source)}
				<li>{message}</li>
			{/each}
		</ul>
	{/if}
</section>

<section class="pt-card" aria-labelledby="later-title">
	<h2 id="later-title">Coming later</h2>
	<p>
		Pausing pairtime for an hour, choosing folders it leaves out, and the list of tools it reads, with when each last
		sent something, come in a later version.
	</p>
</section>

<style>
	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: 0.25rem 1rem;
		margin: 0;
	}

	dt {
		color: var(--pt-muted);
	}

	dd {
		margin: 0;
	}

	h3 {
		margin-top: 1rem;
		font-size: 1rem;
	}
</style>
