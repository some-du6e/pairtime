<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { ProfileMode, ProfileVisibility } from '../choices.ts';
	import Button from './Button.svelte';

	// The profile card (DESIGN 8.3). Nothing is public until the teen answers
	// it, and nothing on it is preselected or focused.
	//
	// - mode "public", the code default: "Keep my profile public" and "Make
	//   it private" have equal visual weight, the same style and the same
	//   size.
	// - mode "ask": "Private profile" is the more prominent button, and comes
	//   first.
	//
	// The preview of the public profile comes from the caller: local
	// aggregates on the device (WP-UI3), the server's own data in the server
	// UI (WP-SV9). Every caller gives one, because the copy in both modes
	// introduces it and DESIGN 8.3 has the card show the real preview.
	interface Props {
		mode: ProfileMode;
		/** Gets the choice each time the teen presses a button. */
		onchoose: (choice: ProfileVisibility) => void;
		/** What the public profile would show. */
		preview: Snippet;
	}

	let { mode, onchoose, preview }: Props = $props();
	const uid = $props.id();
</script>

<section class="pt-card" data-profile-choice data-mode={mode} aria-labelledby="{uid}-title">
	<h2 id="{uid}-title">Your profile</h2>
	{#if mode === 'public'}
		<p>Nothing is public until you choose. If you keep your profile public, anyone can see this:</p>
	{:else}
		<p>Nothing is public until you choose. A public profile shows anyone this:</p>
	{/if}
	<div class="preview">{@render preview()}</div>
	<p class="pt-note">
		It never shows your name, school, city, age, time zone, prompts, file names or private projects.
	</p>
	<div class="choices" role="group" aria-labelledby="{uid}-title">
		{#if mode === 'public'}
			<Button variant="secondary" onclick={() => onchoose('public')}>Keep my profile public</Button>
			<Button variant="secondary" onclick={() => onchoose('private')}>Make it private</Button>
		{:else}
			<Button variant="primary" onclick={() => onchoose('private')}>Private profile</Button>
			<Button variant="secondary" onclick={() => onchoose('public')}>Public profile</Button>
		{/if}
	</div>
	<p class="pt-note">You can change this any time in Settings, with one click.</p>
</section>

<style>
	.preview {
		margin: 0 0 0.75rem;
		padding: 0.75rem;
		border: 1px dashed var(--pt-border);
		border-radius: var(--pt-radius);
	}

	/* Two equal columns, so both choices are the same size; one column on
	   narrow screens, where both span the full width. */
	.choices {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 0.75rem;
		margin: 1rem 0;
	}

	@media (max-width: 30rem) {
		.choices {
			grid-template-columns: 1fr;
		}
	}
</style>
