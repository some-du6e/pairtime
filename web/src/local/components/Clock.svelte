<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { ProjectClock } from '../model.ts';
	import Hands from './Hands.svelte';

	// One project's chess clock, on a card of its own.
	interface Props {
		clock: ProjectClock;
		scale: number;
		shadow: boolean;
	}

	let { clock, scale, shadow }: Props = $props();
	const uid = $props.id();
</script>

<article class="pt-card clock" aria-labelledby="{uid}-name" data-testid="clock">
	<h3 id="{uid}-name">{clock.name}</h3>
	<Hands {clock} {scale} {shadow} />
</article>

<style>
	.clock {
		max-width: none;
	}

	h3 {
		overflow-wrap: anywhere;
	}
</style>
