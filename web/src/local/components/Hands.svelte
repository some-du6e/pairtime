<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { agentParts, type ProjectView, yourParts } from '../model.ts';
	import * as text from '../text.ts';
	import Bar from './Bar.svelte';
	import Legend from './Legend.svelte';

	// A chess clock's hands (DESIGN 3.8): your time beside each agent's time
	// on its own, as information, never as a ratio to aim for (PA 4.3), each
	// with a bar drawn to the page's scale. While calibrating, your time
	// reads "calibrating" and the agents' time stays (DESIGN 6.7).
	interface Props {
		view: ProjectView;
		/** The seconds that fill a bar, the same for every clock on a page. */
		scale: number;
	}

	let { view, scale }: Props = $props();
</script>

<dl class="hands">
	<div class="hand">
		<dt>{text.sentence(text.phrase('your_time'))}</dt>
		<dd>
			{#if view.you}
				<span class="time" data-testid="you">{text.duration(view.you.s)}</span>
				<Bar parts={yourParts(view.you)} {scale} />
			{:else}
				<span class="time" data-testid="you">{text.phrase('calibrating')}</span>
			{/if}
		</dd>
	</div>
	{#each view.agents as a (a.harness)}
		<div class="hand" data-testid="agent">
			<dt>{text.sentence(text.agentTime(a.name))}</dt>
			<dd>
				<span class="time">{text.duration(a.s)}</span>
				<Bar parts={agentParts(a.s)} {scale} />
			</dd>
		</div>
	{/each}
</dl>
{#if view.you}
	<Legend you={view.you} />
{/if}

<style>
	.hands {
		display: grid;
		gap: 0.5rem;
		margin: 0;
	}

	.hand {
		display: grid;
		grid-template-columns: minmax(7rem, 14rem) 1fr;
		align-items: center;
		gap: 0.75rem;
	}

	dt {
		font-weight: 600;
		overflow-wrap: anywhere;
	}

	dd {
		display: grid;
		grid-template-columns: 4.75rem 1fr;
		align-items: center;
		gap: 0.75rem;
		margin: 0;
	}

	.time {
		font-variant-numeric: tabular-nums;
	}

	@media (max-width: 30rem) {
		.hand {
			grid-template-columns: 1fr;
			gap: 0.25rem;
		}
	}
</style>
