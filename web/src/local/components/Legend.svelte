<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { notes, type YourTime } from '../model.ts';
	import * as text from '../text.ts';

	// The key to a bar of your time: each evidence level's share in en.json's
	// words, as `pairtime recap` gives them, then `pairtime today`'s notes,
	// how much is level C and how much may still change. So level-C time is
	// labeled wherever it shows (DESIGN 6.2 D11, D-focus-unknown), and
	// pending time says it is pending (DESIGN 3.7).
	interface Props {
		you: YourTime;
	}

	let { you }: Props = $props();
	const said = $derived(notes(you));
</script>

{#if you.levels.length > 0 || said.length > 0}
	<ul class="legend" data-testid="legend">
		{#each you.levels as l (l.level)}
			<li data-part={l.level}>
				<span class="pt-swatch pt-swatch-{l.level}" aria-hidden="true"></span><span class="share">{text.share(l.share)}</span>
				{text.phrase('level_line', { level: l.level, words: l.words })}
			</li>
		{/each}
		{#each said as n (n.key)}
			<li class="note" data-note={n.key}>
				<span class="pt-swatch pt-swatch-{n.key === 'pending' ? 'pending' : 'C'}" aria-hidden="true"></span>{n.words}
			</li>
		{/each}
	</ul>
{/if}

<style>
	.legend {
		display: grid;
		gap: 0.125rem;
		margin: 0.5rem 0 0;
		padding: 0;
		list-style: none;
		color: var(--pt-muted);
		font-size: 0.9375rem;
	}

	.share {
		display: inline-block;
		min-width: 2.75rem;
		color: var(--pt-text);
		font-variant-numeric: tabular-nums;
	}

	.note {
		color: var(--pt-text);
	}
</style>
