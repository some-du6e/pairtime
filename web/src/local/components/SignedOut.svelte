<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	// What a browser that isn't signed in sees (DESIGN 3.9). The page shows
	// nothing else, so other websites, and other people who use this
	// computer, can't read anyone's time.
	interface Props {
		/** The sign-in link was used already or is too old (/?signin=expired). */
		expired: boolean;
	}

	let { expired }: Props = $props();
</script>

<section class="pt-card" aria-labelledby="signin-title" data-testid="signed-out">
	<h2 id="signin-title">Open your dashboard from a terminal</h2>
	{#if expired}
		<p data-testid="link-expired">
			That link was used already, or it's more than two minutes old. Each link works once, for two minutes.
		</p>
	{/if}
	<p>Run <code>pairtime dashboard</code>. It opens this page in your browser, signed in.</p>
	<p class="pt-note">
		Until then this page shows nothing, so other websites, and other people who use this computer, can't see your time.
	</p>
</section>
