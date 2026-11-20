<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { Result } from '$shared/api/client.ts';
	import AppShell from '$shared/components/AppShell.svelte';
	import { api, describeMode, type Status } from './api.ts';

	// The skeleton of the local dashboard (DESIGN 3.9). It asks the agent
	// how it is; Today, Projects, Privacy and Settings grow from here
	// (WP-UI1), which also adds the sign-in this page checks for. Until
	// then no command opens the page signed in, so the signed-out card names
	// none; WP-UI1 names `pairtime dashboard` there when it adds it.
	let status: Result<Status> | undefined = $state();

	onMount(() => {
		const stop = new AbortController();
		api.get('/v1/status', { signal: stop.signal }).then(
			(result) => {
				status = result;
			},
			() => {
				// Aborted: the page is going away.
			},
		);
		return () => stop.abort();
	});
</script>

<AppShell where="on this computer">
	<h1>Your dashboard</h1>
	<div aria-live="polite">
		{#if status === undefined}
			<p>Asking the pairtime agent…</p>
		{:else if status.ok}
			<section class="pt-card" aria-labelledby="agent-title" data-testid="agent-status">
				<h2 id="agent-title">The pairtime agent</h2>
				<dl>
					<dt>Mode</dt>
					<dd>{describeMode(status.data.mode)}</dd>
					<dt>Version</dt>
					<dd>{status.data.version.version}</dd>
					<dt>Running since</dt>
					<dd><time datetime={status.data.started}>{new Date(status.data.started).toLocaleString()}</time></dd>
				</dl>
				{#if status.data.problems}
					<h3>Needs your attention</h3>
					<ul>
						{#each Object.entries(status.data.problems) as [source, message] (source)}
							<li>{message}</li>
						{/each}
					</ul>
				{/if}
			</section>
		{:else if status.problem === 'signed-out'}
			<section class="pt-card" aria-labelledby="signin-title" data-testid="signed-out">
				<h2 id="signin-title">This version of pairtime can't open your dashboard yet</h2>
				<p>
					A later version adds the command that opens this page signed in. Until then the page shows nothing, so
					other websites, and other people who use this computer, can't read your time.
				</p>
			</section>
		{:else if status.problem === 'unreachable'}
			<section class="pt-card" aria-labelledby="down-title" data-testid="unreachable">
				<h2 id="down-title">The pairtime agent isn't answering</h2>
				<p>Start it with <code>pairtime agent</code>, then reload this page.</p>
			</section>
		{:else}
			<section class="pt-card" aria-labelledby="error-title" data-testid="error">
				<h2 id="error-title">The agent gave an answer this page doesn't understand</h2>
				<p>It said {status.status || 'nothing'}: {status.detail}</p>
			</section>
		{/if}
	</div>
</AppShell>

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
</style>
