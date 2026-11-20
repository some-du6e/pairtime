<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { calibrating } from '../copy.ts';
	import { duration } from '../format.ts';
	import { agentHands, agentParts, type ProjectClock, yourParts } from '../model.ts';
	import Bar from './Bar.svelte';
	import Legend from './Legend.svelte';

	// A chess clock's hands (DESIGN 3.8): your time beside each agent's, as
	// information, never as a ratio to aim for (PA 4.3). While shadow is on,
	// your time reads "Calibrating" and the agents' time stays (DESIGN 6.7).
	interface Props {
		clock: ProjectClock;
		/** The seconds that fill a bar, the same for every clock on a page. */
		scale: number;
		shadow: boolean;
	}

	let { clock, scale, shadow }: Props = $props();
	const hands = $derived(agentHands(clock));
</script>

<dl class="hands">
	<div class="hand">
		<dt>You</dt>
		<dd>
			{#if shadow}
				<span class="time" data-testid="you">{calibrating}</span>
			{:else}
				<span class="time" data-testid="you">{duration(clock.credited)}</span>
				<Bar parts={yourParts(clock)} {scale} />
			{/if}
		</dd>
	</div>
	{#each hands as h (h.key)}
		<div class="hand" data-testid="agent">
			<dt>{h.name}</dt>
			<dd>
				<span class="time">{duration(h.running)}</span>
				<Bar parts={agentParts(h.running)} {scale} />
			</dd>
		</div>
	{/each}
</dl>
{#if !shadow}
	<Legend evidence={clock.evidence} pending={clock.pending} />
{/if}

<style>
	.hands {
		display: grid;
		gap: 0.5rem;
		margin: 0;
	}

	.hand {
		display: grid;
		grid-template-columns: minmax(5rem, 8rem) 1fr;
		align-items: center;
		gap: 0.75rem;
	}

	dt {
		font-weight: 600;
		overflow-wrap: anywhere;
	}

	dd {
		display: grid;
		grid-template-columns: 4.5rem 1fr;
		align-items: center;
		gap: 0.75rem;
		margin: 0;
	}

	.time {
		font-variant-numeric: tabular-nums;
	}
</style>
