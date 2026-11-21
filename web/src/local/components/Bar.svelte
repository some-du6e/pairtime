<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { type Part, segments } from '../model.ts';

	// A bar of seconds, drawn as SVG geometry. The page's Content-Security-
	// Policy allows no style attribute, so each part's width is a rect
	// attribute and its color comes from a class. Pending minutes are drawn
	// lighter, with a dashed outline (DESIGN 6.2 D1). The bar is a picture of
	// figures the text beside it gives, so assistive technology skips it.
	interface Props {
		parts: [Part, number][];
		/** The seconds that fill the bar. */
		scale: number;
	}

	let { parts, scale }: Props = $props();
	const drawn = $derived(segments(parts, scale));
</script>

<svg class="bar" viewBox="0 0 100 4" preserveAspectRatio="none" aria-hidden="true" data-testid="bar">
	<rect class="track" x="0" y="0" width="100" height="4" />
	{#each drawn as s (s.part)}
		<rect class="part {s.part}" data-part={s.part} x={s.x} y="0" width={s.width} height="4" />
	{/each}
</svg>

<style>
	.bar {
		display: block;
		width: 100%;
		height: 0.75rem;
	}

	.track {
		fill: var(--pt-track);
	}

	.A {
		fill: var(--pt-level-a);
	}

	.B {
		fill: var(--pt-level-b);
	}

	.C {
		fill: var(--pt-level-c);
	}

	.pending {
		fill: var(--pt-pending);
		stroke: var(--pt-level-a);
		stroke-width: 1px;
		stroke-dasharray: 3 2;
		vector-effect: non-scaling-stroke;
	}

	.agent {
		fill: var(--pt-agent);
	}
</style>
