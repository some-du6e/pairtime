<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { counts, levels, pendingWords } from '../copy.ts';
	import { minutesWords } from '../format.ts';

	// What pairtime counts, why a minute counts at level A, B or C, and what
	// pending means, in the words of copy.ts.
	interface Props {
		/** PENDING_WINDOW_S, from GET /v1/days. */
		pendingWindowS: number;
	}

	let { pendingWindowS }: Props = $props();
	const uid = $props.id();
</script>

<section class="pt-card how" aria-labelledby="{uid}-title" data-testid="how-it-counts">
	<h2 id="{uid}-title">How pairtime counts</h2>
	{#each counts as paragraph (paragraph)}
		<p>{paragraph}</p>
	{/each}
	<h3>Why a minute counts at level A, B or C</h3>
	<dl class="levels">
		{#each levels as l (l.level)}
			<div data-level={l.level}>
				<dt><span class="pt-swatch pt-swatch-{l.level}" aria-hidden="true"></span>Level {l.level}, {l.name.toLowerCase()}</dt>
				<dd>{l.why}</dd>
			</div>
		{/each}
	</dl>
	<h3>Pending minutes</h3>
	<p data-testid="pending-words">
		<span class="pt-swatch pt-swatch-pending" aria-hidden="true"></span>{pendingWords(minutesWords(pendingWindowS))}
	</p>
</section>

<style>
	.how {
		max-width: none;
	}

	.levels {
		margin: 0 0 1rem;
	}

	.levels div {
		margin: 0 0 0.75rem;
	}

	dt {
		font-weight: 600;
	}

	dd {
		margin: 0.125rem 0 0;
	}
</style>
