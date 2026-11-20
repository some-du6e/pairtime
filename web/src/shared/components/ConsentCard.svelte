<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { ConsentAnswer } from '../choices.ts';
	import Button from './Button.svelte';

	// One consent-based item on its own card (DESIGN 8.4). The card says what
	// the item does, that saying no doesn't stop anyone from using pairtime,
	// and how to turn it off later. "Not now" is the most prominent button
	// and comes first; nothing is preselected or focused. Those parts are
	// fixed here, so no caller can change them, and a Playwright test checks
	// them on the rendered card (web/tests/consent.spec.ts).
	//
	// The fixed text claims nothing about what else saying no changes,
	// because for some items it changes something. Without the typing module
	// most Windows and X11 minutes are level B (DESIGN D16), and the audit
	// weight that follows is a question for counsel (7.9). Without a backup,
	// a lost computer takes the evidence for its hours with it (7.11). An
	// item like that says so in `declining`.
	//
	// Remembering a refusal for a year, the revoke toggle in Settings and the
	// device's refusal signal belong to the onboarding flow (WP-UI3).
	interface Props {
		/** The item, as a short name: "Typing evidence". */
		title: string;
		/** Where it can be turned off later: "Settings, under Typing evidence". */
		turnOff: string;
		/** The label of the button that says yes: "Turn on typing evidence". */
		accept: string;
		/**
		 * What saying no changes, in one plain and true sentence, for an item
		 * where it changes something: "Without a backup, if you lose this
		 * computer or wipe it, the evidence for the time you tracked on it is
		 * gone." Leave it out otherwise, and the card says nothing either way.
		 */
		declining?: string;
		/** Gets the answer each time the teen presses a button. */
		onanswer: (answer: ConsentAnswer) => void;
		/** What the item does, in a sentence or two. */
		children: Snippet;
	}

	let { title, turnOff, accept, declining, onanswer, children }: Props = $props();
	const uid = $props.id();
</script>

<section class="pt-card" data-consent-card aria-labelledby="{uid}-title">
	<h2 id="{uid}-title">{title}</h2>
	<div class="what">{@render children()}</div>
	<p class="pt-note">Saying no doesn't stop you from using pairtime.</p>
	{#if declining}
		<p class="pt-note" data-declining>{declining}</p>
	{/if}
	<p class="pt-note">If you turn it on, you can turn it off any time in {turnOff}.</p>
	<div class="pt-actions" role="group" aria-labelledby="{uid}-title">
		<Button variant="primary" onclick={() => onanswer('not_now')}>Not now</Button>
		<Button variant="secondary" onclick={() => onanswer('yes')}>{accept}</Button>
	</div>
</section>

<style>
	.what {
		margin: 0 0 0.75rem;
	}
</style>
