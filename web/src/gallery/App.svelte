<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { ConsentAnswer, ProfileVisibility } from '$shared/choices.ts';
	import AppShell from '$shared/components/AppShell.svelte';
	import ConsentCard from '$shared/components/ConsentCard.svelte';
	import ProfileChoice from '$shared/components/ProfileChoice.svelte';

	// Every shared component with sample content, for Playwright
	// (web/tests/) and for whoever works on them: `npm run dev:gallery`.
	// Never shipped. Each <output> shows the last answer its component
	// reported.
	let consent: ConsentAnswer | undefined = $state();
	let publicMode: ProfileVisibility | undefined = $state();
	let askMode: ProfileVisibility | undefined = $state();
</script>

<AppShell where="components">
	<h1>Shared components</h1>

	<div data-testid="consent">
		<ConsentCard
			title="Typing evidence"
			turnOff="Settings, under Typing evidence"
			accept="Turn on typing evidence"
			onanswer={(answer) => (consent = answer)}
		>
			<p>
				Counts how many keys you press every 15 seconds, never which keys, so a reviewer can see the hours were
				typed by a person.
			</p>
		</ConsentCard>
		<p>Answer: <output data-testid="consent-answer">{consent ?? 'none'}</output></p>
	</div>

	<div data-testid="profile-public">
		<ProfileChoice mode="public" onchoose={(choice) => (publicMode = choice)}>
			{#snippet preview()}
				<p><strong>@sample</strong>: 4 build days this week, Python and Svelte, 2 public projects.</p>
			{/snippet}
		</ProfileChoice>
		<p>Choice: <output data-testid="profile-public-choice">{publicMode ?? 'none'}</output></p>
	</div>

	<div data-testid="profile-ask">
		<ProfileChoice mode="ask" onchoose={(choice) => (askMode = choice)} />
		<p>Choice: <output data-testid="profile-ask-choice">{askMode ?? 'none'}</output></p>
	</div>
</AppShell>
