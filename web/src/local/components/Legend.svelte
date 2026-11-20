<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { ByEvidence } from '../api.ts';
	import { levels } from '../copy.ts';
	import { duration } from '../format.ts';

	// The figures behind a bar of your time: how much is at each evidence
	// level, and how much is still pending. Level C time is labeled as level
	// C wherever it shows (DESIGN 6.2 D11, D-focus-unknown).
	interface Props {
		evidence: ByEvidence;
		pending: number;
	}

	let { evidence, pending }: Props = $props();
	const items = $derived(
		[
			...levels.map((l) => ({
				key: String(l.level),
				label: `Level ${l.level}, ${l.name.toLowerCase()}`,
				seconds: evidence[l.level],
			})),
			{ key: 'pending', label: 'Pending', seconds: pending },
		].filter((i) => i.seconds > 0),
	);
</script>

{#if items.length > 0}
	<ul class="legend" data-testid="legend">
		{#each items as i (i.key)}
			<li data-part={i.key}>
				<span class="pt-swatch pt-swatch-{i.key}" aria-hidden="true"></span>{i.label}
				<span class="time">{duration(i.seconds)}</span>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1.25rem;
		margin: 0.5rem 0 0;
		padding: 0;
		list-style: none;
		color: var(--pt-muted);
		font-size: 0.9375rem;
	}

	.time {
		color: var(--pt-text);
		font-variant-numeric: tabular-nums;
	}
</style>
