<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Mode } from '../api.ts';

	// Privacy (DESIGN 3.9, 8.8; docs/PLAN.md, WP-UI1): what has left this
	// computer, what pairtime keeps here and for how long (DESIGN 3.5, 4.4),
	// what it never records, and who can read what it keeps (CP 5.2).
	// "Delete everything" comes with the agent's wipe, in a later WP.
	interface Props {
		mode: Mode;
	}

	let { mode }: Props = $props();

	// DESIGN 4.4's device rows, in words.
	const kept: { what: string; kept: string }[] = [
		{
			what: "Your prompts, and the agent's last answer in each turn, encrypted",
			kept: '90 days. If you send time to a program, until it decides, when that is later',
		},
		{ what: 'File paths, for your recaps, and the commands your tools ran', kept: '30 days' },
		{
			what: 'Whether you were at the keyboard and what kind of app was in front, every 15 seconds while you work in a project',
			kept: '90 days',
		},
		{ what: 'A record of each event without its text, which can prove your time later', kept: '180 days' },
		{ what: 'Your time minute by minute', kept: '180 days' },
		{ what: 'Your time day by day, and your recaps', kept: 'Until you delete them' },
		{ what: "This computer's pairtime key", kept: 'Until you uninstall pairtime' },
		{
			what: 'Transcripts and what the agent wrote',
			kept: "pairtime reads them where your agent keeps them and doesn't copy them, so your agent's settings decide. Claude Code keeps them 30 days unless you change it",
		},
	];
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
	<table>
		<thead>
			<tr>
				<th scope="col">What</th>
				<th scope="col">How long</th>
			</tr>
		</thead>
		<tbody>
			{#each kept as row (row.what)}
				<tr>
					<th scope="row">{row.what}</th>
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

<section class="pt-card" aria-labelledby="who-title">
	<h2 id="who-title">Who can read it</h2>
	<p>
		pairtime encrypts your prompts on this computer. That keeps them from other people who use it, from cloud backups of
		your home folder, and from a quick look. It can't keep them from a program running as you.
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
