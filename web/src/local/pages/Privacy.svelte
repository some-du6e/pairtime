<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Mode } from '../api.ts';
	import { keptOnDevice } from '../retention.ts';

	// Privacy (DESIGN 3.9, 8.8; docs/PLAN.md, WP-UI1): what has left this
	// computer, what pairtime keeps here and for how long (DESIGN 3.5, 4.4),
	// what it never records, and who can read what it keeps (CP 5.2).
	// "Delete everything" comes with the agent's wipe, in a later WP.
	//
	// The periods are docs/retention.json's device entries, in the file's
	// order, then the inbox's row (retention.ts). This version has no vault
	// (DESIGN 3.5), so the only prompt text it keeps is the inbox's, as
	// plain text. Who can read it says so, and speaks of the vault's
	// encryption only as what a later version, which keeps prompt text
	// there, will do.
	interface Props {
		mode: Mode;
	}

	let { mode }: Props = $props();
</script>

<h1>Privacy</h1>

<section class="pt-card" aria-labelledby="left-title" data-testid="left-computer">
	<h2 id="left-title">What has left this computer</h2>
	{#if mode === 'local'}
		<p>Nothing. pairtime is in local-only mode, so it sends nothing anywhere: no times, no numbers, no names.</p>
	{:else}
		<p>pairtime lists every upload here in plain words.</p>
	{/if}
</section>

<section class="pt-card wide" aria-labelledby="kept-title" data-testid="kept">
	<h2 id="kept-title">What pairtime keeps on this computer</h2>
	<p>
		Some of these, and deleting each on time, come with a later version of pairtime. Who can read it, below, says what
		this one keeps of your prompts.
	</p>
	<table>
		<thead>
			<tr>
				<th scope="col">What</th>
				<th scope="col">How long</th>
			</tr>
		</thead>
		<tbody>
			{#each keptOnDevice as row (row.id)}
				<tr data-id={row.id}>
					<th scope="row">{row.data}</th>
					<td>{row.kept}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</section>

<section class="pt-card" aria-labelledby="never-title" data-testid="never">
	<h2 id="never-title">What pairtime never records</h2>
	<ul>
		<li>Which keys you press</li>
		<li>Window titles</li>
		<li>Your screen</li>
		<li>Anything while you aren't working in a project</li>
	</ul>
</section>

<section class="pt-card" aria-labelledby="who-title" data-testid="who">
	<h2 id="who-title">Who can read it</h2>
	<p>
		This version of pairtime keeps the text of your prompts, and what your AI tool sent with them, in one place only:
		the inbox, until the agent reads it, as the table above says. The inbox is plain text, not encrypted. It's in a folder that other people's accounts on this
		computer can't open, but an administrator can, and so can any program running as you. A backup of your home folder
		can copy it as it is.
	</p>
	<p>
		A later version will keep your prompts, the agent's final reply to each one and the file paths for your recaps in a
		vault, which it encrypts. That will keep them from other people who use this computer, from cloud backups of your
		home folder, and from a quick look, but not from a program running as you.
	</p>
	<p>
		This dashboard opens only on this computer, in the browser that <code>pairtime dashboard</code> signed in.
	</p>
</section>

<section class="pt-card" aria-labelledby="delete-title" data-testid="delete">
	<h2 id="delete-title">Delete everything</h2>
	<p>
		A button here will delete everything pairtime keeps on this computer, its key included. It comes in a later version
		of pairtime.
	</p>
</section>

<style>
	.wide {
		max-width: none;
	}

	table {
		width: 100%;
		border-collapse: collapse;
	}

	th,
	td {
		padding: 0.5rem 0.75rem 0.5rem 0;
		text-align: left;
		vertical-align: top;
		border-top: 1px solid var(--pt-track);
	}

	thead th {
		border-top: 0;
		color: var(--pt-muted);
	}

	tbody th {
		font-weight: 400;
		width: 55%;
	}

	ul {
		margin: 0;
		padding-left: 1.25rem;
	}
</style>
