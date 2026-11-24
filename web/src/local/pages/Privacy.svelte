<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Mode } from '../api.ts';
	import { keptOnDevice } from '../retention.ts';

	// Privacy (DESIGN 3.9, 8.8; docs/PLAN.md, WP-UI1): what has left this
	// computer, what pairtime keeps here and for how long (DESIGN 3.5, 4.4),
	// what it never records, and who can read what it keeps (CP 5.2).
	// "Delete everything" is pairtime privacy delete for now (WP-LG6), and
	// its button comes in a later WP.
	//
	// The periods are docs/retention.json's device entries, in the file's
	// order, the inbox's included (retention.ts). The vault keeps prompt
	// text encrypted (DESIGN 3.5; WP-LG6), and the inbox and the hook calls
	// the agent holds keep it as plain text until the agent has read them.
	// Who can read it says which is which.
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
		pairtime deletes each of these once it has been kept as long as the table says. Who can read it, below, says how it
		keeps your prompts.
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
	<p data-testid="vault">
		pairtime keeps the text of your prompts, the agent's final reply to each one and the file paths for your recaps in
		a vault, which it encrypts with a key only this computer has. That keeps them from other people who use this
		computer and from a quick look, but not from a program running as you. It keeps them from cloud backups of your
		home folder too, unless pairtime has to keep its key in a file there, which pairtime doctor tells you. To
		keep no prompt text at all, type <code>pairtime privacy keep-text off</code> in a terminal. Or, by hand, set
		<code>keep_prompt_text = false</code> under <code>[privacy]</code> in pairtime's config.toml; if the file has no
		<code>[privacy]</code> line, as one written before this version has, add these two lines at the end:
	</p>
	<pre data-testid="keep-off"><code>[privacy]
keep_prompt_text = false</code></pre>
	<p>
		pairtime then deletes the text it kept, and a spot check of your hours goes on the rest of what it recorded.
	</p>
	<p>
		Until the agent has read them, the inbox and the hook calls the agent holds keep your prompts too, with what your AI
		tool sent with them, as the table above says. Those two are plain text, not encrypted. They're in a folder that
		other people's accounts on this computer can't open, but an administrator can, and so can any program running as
		you. A backup of your home folder can copy them as they are.
	</p>
	<p data-testid="sketches">
		The prompt similarity sketches (MinHash) in the table are not encrypted either. They're numbers worked out from your
		prompts' words, not the words, and they're in the same pairtime folder as the inbox.
	</p>
	<p>
		This dashboard opens only on this computer, in the browser that <code>pairtime dashboard</code> signed in.
	</p>
</section>

<section class="pt-card" aria-labelledby="delete-title" data-testid="delete">
	<h2 id="delete-title">Delete everything</h2>
	<p>
		<code>pairtime privacy delete</code>, typed in a terminal, deletes pairtime's key and everything pairtime keeps on
		this computer, apart from pairtime itself and its settings. The hours it recorded can't be submitted after that.
		Copies a backup or a synced folder already holds stay as they are, the key's file too when pairtime doctor says
		pairtime keeps its key in a file. A button here comes in a later version of pairtime.
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
