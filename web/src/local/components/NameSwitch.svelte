<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { ProjectEntry } from '../api.ts';
	import { nameRefused, nameWho, nameWords, noNameWords } from '../copy.ts';
	import { nameShown, projectArg } from '../model.ts';
	import * as text from '../text.ts';

	// A project's switch for its name on the owner's own pairtime dashboard
	// (D-owner-names; docs/spec/project-names-v1.md): on while the name
	// shows there or is on its way, with the label it shows or would show,
	// who sees it, and where it stands. The switch shows the agent's answer,
	// not the click: changing it calls onchange, which asks the agent (PUT
	// /v1/projects/{key}/name), and the answer sets it. A project with no
	// name on this computer and none kept has no label to show, so it says
	// how to give it one instead.
	interface Props {
		entry: ProjectEntry;
		/** GET /v1/projects's online: the names turned on go to the server now. */
		online: boolean;
		/** A change of this switch is on its way to the agent. */
		busy?: boolean;
		/** Why the agent didn't take the last change, in its words; "" when it did. */
		problem?: string;
		onchange: (entry: ProjectEntry, online: boolean) => void;
	}

	let { entry, online, busy = false, problem = '', onchange }: Props = $props();
	const uid = $props.id();
	const on = $derived(nameShown(entry.state));

	function change(e: Event & { currentTarget: HTMLInputElement }): void {
		const want = e.currentTarget.checked;
		// Back to what the agent says until its answer comes.
		e.currentTarget.checked = on;
		onchange(entry, want);
	}
</script>

<div class="name" data-testid="name" data-project={entry.project} data-state={entry.state}>
	{#if entry.label === ''}
		<p class="pt-note" data-testid="no-name">{noNameWords(projectArg(entry))}</p>
	{:else}
		<p class="switch">
			<input id="{uid}-show" type="checkbox" checked={on} disabled={busy} onchange={change} />
			<label for="{uid}-show">Show this name on my dashboard: <span data-testid="name-label">"{text.printable(entry.label)}"</span></label>
		</p>
		<p class="pt-note" data-testid="name-state">{nameWords(entry.state, entry.code, online, entry.why, entry.sent)} {nameWho}</p>
	{/if}
	{#if problem !== ''}
		<p class="problem" role="alert" data-testid="name-problem">{nameRefused(problem)}</p>
	{/if}
</div>

<style>
	.name {
		margin: 0.75rem 0 0;
	}

	.switch {
		display: flex;
		align-items: flex-start;
		gap: 0.5rem;
		margin: 0 0 0.25rem;
	}

	input {
		width: 1.25rem;
		height: 1.25rem;
		margin: 0.125rem 0 0;
		flex: none;
		accent-color: var(--pt-accent);
	}

	label {
		overflow-wrap: anywhere;
	}

	.pt-note,
	.problem {
		margin: 0;
	}

	.problem {
		margin-top: 0.25rem;
		font-weight: 600;
	}
</style>
