<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { clockLine, type ProjectView } from '../model.ts';
	import Hands from './Hands.svelte';

	// One project's chess clock on a card: its name and clock line, as
	// `pairtime today` prints them ("portfolio: you 52m · Claude 41m"), and
	// the hands drawn as bars.
	interface Props {
		view: ProjectView;
		scale: number;
	}

	let { view, scale }: Props = $props();
	const uid = $props.id();
</script>

<article class="pt-card clock" aria-labelledby="{uid}-name" data-testid="clock">
	<h3 id="{uid}-name">{view.title}</h3>
	<p class="line" data-testid="clock-line">{clockLine(view)}</p>
	<Hands {view} {scale} />
</article>

<style>
	.clock {
		max-width: none;
	}

	h3 {
		margin-bottom: 0.125rem;
		overflow-wrap: anywhere;
	}

	.line {
		color: var(--pt-muted);
		font-variant-numeric: tabular-nums;
	}
</style>
