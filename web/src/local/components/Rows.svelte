<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { duration } from '../format.ts';
	import type { Row } from '../model.ts';

	// Seconds by kind or by reason, each with what it was in plain words.
	interface Props {
		rows: Row<string>[];
		testid?: string;
	}

	let { rows, testid }: Props = $props();
</script>

<ul class="rows" data-testid={testid}>
	{#each rows as r (r.key)}
		<li data-key={r.key}>
			<span class="label">{r.label}</span>
			<span class="time">{duration(r.seconds)}</span>
			<span class="about">{r.about}</span>
		</li>
	{/each}
</ul>

<style>
	.rows {
		margin: 0;
		padding: 0;
		list-style: none;
	}

	li {
		display: grid;
		grid-template-columns: 1fr auto;
		gap: 0 1rem;
		padding: 0.5rem 0;
		border-top: 1px solid var(--pt-track);
	}

	li:first-child {
		border-top: 0;
	}

	.label {
		font-weight: 600;
	}

	.time {
		font-variant-numeric: tabular-nums;
		text-align: right;
	}

	.about {
		grid-column: 1 / -1;
		color: var(--pt-muted);
	}
</style>
