<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLButtonAttributes } from 'svelte/elements';

	// "primary" marks the one most prominent choice on a screen and
	// "secondary" everything else. Two choices of equal weight are both
	// secondary (DESIGN 8.3). The button is always type="button", so Enter in
	// a form never picks a choice the teen didn't press.
	interface Props extends Omit<HTMLButtonAttributes, 'type' | 'class' | 'style' | 'children'> {
		variant: 'primary' | 'secondary';
		children: Snippet;
	}

	let { variant, children, ...rest }: Props = $props();
</script>

<button {...rest} type="button" class="pt-button" data-variant={variant}>{@render children()}</button>

<style>
	.pt-button {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		/* 44 px, WCAG 2.2's enhanced target size, for touchpads and touch. */
		min-height: 2.75rem;
		padding: 0.5rem 1.25rem;
		font: inherit;
		font-weight: 600;
		line-height: 1.25;
		text-align: center;
		color: var(--pt-text);
		background: transparent;
		border: 2px solid var(--pt-border);
		border-radius: var(--pt-radius);
		cursor: pointer;
	}

	.pt-button[data-variant='primary'] {
		color: var(--pt-accent-text);
		background: var(--pt-accent);
		border-color: var(--pt-accent);
	}

	.pt-button:disabled {
		cursor: not-allowed;
		opacity: 0.6;
	}
</style>
