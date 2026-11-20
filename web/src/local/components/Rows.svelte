<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import * as text from '../text.ts';

	// Time by kind or by reason, each in en.json's words, as `pairtime today`
	// lists them. Amounts under a second, which would read "0m", are left
	// out, as the CLI leaves them out.
	interface Props {
		rows: { key: string; words: string; s: number }[];
		testid?: string;
	}

	let { rows, testid }: Props = $props();
	const shown = $derived(rows.filter((r) => r.s >= 1));
</script>

{#if shown.length > 0}
	<ul class="rows" data-testid={testid}>
		{#each shown as r (r.key)}
			<li data-key={r.key}>
				<span class="words">{text.sentence(r.words)}</span>
				<span class="time">{text.duration(r.s)}</span>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.rows {
		margin: 0;
		padding: 0;
		list-style: none;
	}

	li {
		display: flex;
		justify-content: space-between;
		gap: 1rem;
		padding: 0.375rem 0;
		border-top: 1px solid var(--pt-track);
	}

	li:first-child {
		border-top: 0;
	}

	.time {
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
</style>
