<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Result } from '$shared/api/client.ts';
	import AppShell from '$shared/components/AppShell.svelte';
	import { api, type Days, type Status } from './api.ts';
	import SignedOut from './components/SignedOut.svelte';
	import { addDays } from './format.ts';
	import Privacy from './pages/Privacy.svelte';
	import Projects from './pages/Projects.svelte';
	import Settings from './pages/Settings.svelte';
	import Today from './pages/Today.svelte';
	import { home, href, pages, parseRoute, type Route, titleOf } from './route.ts';

	// The local dashboard (DESIGN 3.9; docs/PLAN.md, WP-UI1). `pairtime
	// dashboard` signs this browser in with a session cookie; until it has,
	// GET /v1/status answers 401 and the page shows nothing but how to open
	// it. Signed in, it reads the agent once a minute while the tab is
	// visible, so pending minutes turn final on their own (DESIGN 3.7), and
	// never while it is hidden.
	const refreshMs = 60_000;

	// Each is replaced whole and never changed in place, so none needs a
	// deep proxy.
	let route: Route = $state.raw(parseRoute(location.hash) ?? home);
	let status: Result<Status> | undefined = $state.raw();
	let week: Result<Days> | undefined = $state.raw();
	let month: Result<Days> | undefined = $state.raw();

	// GET /signin sends a used or old link to /?signin=expired. The page says
	// so once, and takes it out of the address bar.
	const expired = new URLSearchParams(location.search).get('signin') === 'expired';
	if (expired) {
		history.replaceState(null, '', location.pathname + location.hash);
	}

	const signedIn = $derived(status?.ok === true);
	// The days the page shows: the last 30 for Projects' longer span, this
	// week for everything else.
	const shown = $derived(route.page === 'projects' && route.range === 'month' ? month : week);

	async function refresh(r: Route, signal: AbortSignal): Promise<void> {
		const s = await api.get('/v1/status', { signal });
		status = s;
		if (!s.ok) {
			return;
		}
		const w = await api.get('/v1/days', { signal });
		week = w;
		if (r.page === 'projects' && r.range === 'month' && w.ok) {
			const today = w.data.today;
			month = await api.get('/v1/days', { signal, query: { from: addDays(today, -29), to: today } });
		}
	}

	$effect(() => {
		const r = route;
		const stop = new AbortController();
		const run = () => {
			refresh(r, stop.signal).catch(() => {
				// Aborted: the page changed, and its own refresh has started.
			});
		};
		run();
		const timer = setInterval(() => {
			if (document.visibilityState !== 'hidden') {
				run();
			}
		}, refreshMs);
		const onVisible = () => {
			if (document.visibilityState === 'visible') {
				run();
			}
		};
		document.addEventListener('visibilitychange', onVisible);
		return () => {
			stop.abort();
			clearInterval(timer);
			document.removeEventListener('visibilitychange', onVisible);
		};
	});

	$effect(() => {
		const onHash = () => {
			const r = parseRoute(location.hash);
			// A fragment that names no page, such as the skip link's, leaves
			// the page as it is.
			if (r) {
				route = r;
				document.getElementById('main')?.focus();
			}
		};
		window.addEventListener('hashchange', onHash);
		return () => window.removeEventListener('hashchange', onHash);
	});

	$effect(() => {
		document.title = signedIn ? `${titleOf(route)} · pairtime` : 'pairtime';
	});
</script>

{#snippet links()}
	<ul class="links">
		{#each pages as p (p.page)}
			<li>
				<a href={href({ page: p.page, range: 'week' })} aria-current={route.page === p.page ? 'page' : undefined}>{p.label}</a>
			</li>
		{/each}
	</ul>
{/snippet}

<AppShell where="on this computer" nav={signedIn ? links : undefined}>
	{#if status === undefined}
		<h1>Your dashboard</h1>
		<p aria-live="polite">Asking the pairtime agent…</p>
	{:else if !status.ok}
		<h1>Your dashboard</h1>
		{#if status.problem === 'signed-out'}
			<SignedOut {expired} />
		{:else if status.problem === 'unreachable'}
			<section class="pt-card" aria-labelledby="down-title" data-testid="unreachable">
				<h2 id="down-title">The pairtime agent isn't answering</h2>
				<p>Run <code>pairtime dashboard</code> again. It starts the agent if it isn't running.</p>
			</section>
		{:else}
			<section class="pt-card" aria-labelledby="error-title" data-testid="error">
				<h2 id="error-title">The agent gave an answer this page doesn't understand</h2>
				<p>It said {status.status || 'nothing'}: {status.detail}</p>
			</section>
		{/if}
	{:else if route.page === 'privacy'}
		<Privacy mode={status.data.mode} />
	{:else if route.page === 'settings'}
		<Settings status={status.data} shadow={week?.ok ? week.data.shadow : undefined} />
	{:else if shown === undefined}
		<h1>{titleOf(route)}</h1>
		<p aria-live="polite">Reading your time…</p>
	{:else if !shown.ok}
		<h1>{titleOf(route)}</h1>
		{#if shown.problem === 'signed-out'}
			<SignedOut expired={false} />
		{:else}
			<section class="pt-card" aria-labelledby="days-error-title" data-testid="days-error">
				<h2 id="days-error-title">pairtime couldn't read your time</h2>
				<p>{shown.detail || `The agent said ${shown.status}.`}</p>
			</section>
		{/if}
	{:else if route.page === 'projects'}
		<Projects days={shown.data} range={route.range} />
	{:else}
		<Today days={shown.data} focus={status.data.focus} />
	{/if}
</AppShell>

<style>
	.links {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1.25rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.links a {
		display: inline-block;
		padding: 0.5rem 0;
		color: var(--pt-text);
		text-decoration: none;
	}

	.links a:hover {
		text-decoration: underline;
	}

	.links a[aria-current='page'] {
		font-weight: 700;
		text-decoration: underline;
		text-decoration-thickness: 3px;
		text-underline-offset: 0.35em;
		text-decoration-color: var(--pt-accent);
	}
</style>
