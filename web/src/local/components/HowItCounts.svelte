<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Level } from '../api.ts';
	import { counts, levelWhy, pendingWords } from '../copy.ts';
	import * as text from '../text.ts';

	// What pairtime counts, why a minute counts at level A, B or C, and what
	// pending means. Each level is headed by en.json's words for it, which
	// the CLI uses too, and explained in copy.ts's.
	const uid = $props.id();
	const levels: Level[] = ['A', 'B', 'C'];
</script>

<section class="pt-card how" aria-labelledby="{uid}-title" data-testid="how-it-counts">
	<h2 id="{uid}-title">How pairtime counts</h2>
	{#each counts as paragraph (paragraph)}
		<p>{paragraph}</p>
	{/each}
	<h3>Why a minute counts at level A, B or C</h3>
	<dl class="levels">
		{#each levels as l (l)}
			<div data-level={l}>
				<dt><span class="pt-swatch pt-swatch-{l}" aria-hidden="true"></span>Level {l}: {text.level(l)}</dt>
				<dd>{levelWhy[l]}</dd>
			</div>
		{/each}
	</dl>
	<h3>Pending minutes</h3>
	<p data-testid="pending-words"><span class="pt-swatch pt-swatch-pending" aria-hidden="true"></span>{pendingWords}</p>
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
