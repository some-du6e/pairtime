<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Snippet } from 'svelte';

	// The frame every pairtime page sits in: a skip link, the name bar with
	// optional navigation, and one <main> that the page fills. The page
	// brings its own <h1>.
	interface Props {
		/** Where this UI runs, shown after the name: "on this computer". */
		where?: string;
		/** Links for the name bar, inside a labeled <nav>. */
		nav?: Snippet;
		children: Snippet;
	}

	let { where, nav, children }: Props = $props();
</script>

<a class="skip" href="#main">Skip to content</a>
<header class="bar">
	<p class="name">
		<span class="mark" aria-hidden="true"></span><span>pairtime</span>{#if where}{' '}<span class="where">{where}</span>{/if}
	</p>
	{#if nav}
		<nav aria-label="Pages">{@render nav()}</nav>
	{/if}
</header>
<main id="main" tabindex="-1">
	{@render children()}
</main>

<style>
	.skip {
		position: absolute;
		left: 0.5rem;
		top: -3rem;
		padding: 0.5rem 0.75rem;
		color: var(--pt-accent-text);
		background: var(--pt-accent);
		border-radius: var(--pt-radius);
	}

	.skip:focus {
		top: 0.5rem;
	}

	.bar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem 1.5rem;
		padding: 0.75rem 1rem;
		border-bottom: 1px solid var(--pt-border);
	}

	.name {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin: 0;
		font-weight: 700;
	}

	.mark {
		width: 1rem;
		height: 1rem;
		border: 3px solid var(--pt-accent);
		border-radius: 50%;
		box-shadow: 0.45rem 0 0 -0.1rem var(--pt-accent);
	}

	.where {
		font-weight: 400;
		color: var(--pt-muted);
	}

	main {
		max-width: 60rem;
		padding: 1.5rem 1rem 3rem;
	}

	main:focus {
		outline: none;
	}
</style>
