<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Result } from '$shared/api/client.ts';
	import AppShell from '$shared/components/AppShell.svelte';
	import { api, type Day, type LastSession, type Status } from './api.ts';
	import SignedOut from './components/SignedOut.svelte';
	import { addDays, dates, mayChange, monday } from './model.ts';
	import Privacy from './pages/Privacy.svelte';
	import Projects from './pages/Projects.svelte';
	import Settings from './pages/Settings.svelte';
	import Today from './pages/Today.svelte';
	import { home, href, pages, parseRoute, type Route, titleOf } from './route.ts';

	// The local dashboard (DESIGN 3.9; docs/PLAN.md, WP-UI1). `pairtime
	// dashboard` signs this browser in with a session cookie; until it has,
	// GET /v1/status answers 401 and the page shows nothing but how to open
	// it. Signed in, it asks the agent for today and the last session once a
	// minute while the tab is visible, so pending minutes turn final on their
	// own (DESIGN 3.7), and never while it is hidden. It reads the routes
	// `pairtime today` and `recap` read (WP-CO0), GET /v1/days/{date} and
	// GET /v1/sessions/last, and asks for each earlier day of the span once,
	// and again each minute while that day can still change (model.ts's
	// mayChange): while it has pending minutes, or, for yesterday, while it
	// has minutes progress can still bring back, as a restore reaches back
	// PROGRESS_RESTORE_S, 86,400 s (SM 4.3), or while calibrating hides
	// whether it has. A day whose answer never came back is asked again too,
	// and the page names it as left out until it comes; one that came back
	// before keeps those numbers meanwhile.
	const refreshMs = 60_000;

	// Each is replaced whole and never changed in place, so none needs a
	// deep proxy.
	let route: Route = $state.raw(parseRoute(location.hash) ?? home);
	let status: Result<Status> | undefined = $state.raw();
	let today: Result<Day> | undefined = $state.raw();
	let session: Result<LastSession> | undefined = $state.raw();
	let past: ReadonlyMap<string, Result<Day>> = $state.raw(new Map());

	// GET /signin sends a used or old link to /?signin=expired. The page says
	// so once, and takes it out of the address bar.
	const expired = new URLSearchParams(location.search).get('signin') === 'expired';
	if (expired) {
		history.replaceState(null, '', location.pathname + location.hash);
	}

	const signedIn = $derived(status?.ok === true);

	/** The first day of the span a route shows: this week's Monday, or 29 days before today. */
	function firstDay(r: Route, date: string): string {
		return r.page === 'projects' && r.range === 'month' ? addDays(date, -29) : monday(date);
	}

	/**
	 * The span's days that came back, in date order, today last, and its
	 * earlier dates whose answer didn't. A date not asked about yet, its
	 * answer on the way, is in neither.
	 */
	function span(first: string, t: Day): { days: Day[]; unread: string[] } {
		const days: Day[] = [];
		const unread: string[] = [];
		for (const date of dates(first, addDays(t.date, -1))) {
			const got = past.get(date);
			if (got?.ok) {
				days.push(got.data);
			} else if (got) {
				unread.push(date);
			}
		}
		days.push(t);
		return { days, unread };
	}

	const from = $derived(today?.ok ? firstDay(route, today.data.date) : '');
	const shown = $derived(today?.ok ? span(from, today.data) : { days: [], unread: [] });

	async function refresh(r: Route, signal: AbortSignal): Promise<void> {
		const s = await api.get('/v1/status', { signal });
		status = s;
		if (!s.ok) {
			return;
		}
		const [t, last] = await Promise.all([
			api.get('/v1/days/{date}', { params: { date: 'today' }, signal }),
			api.get('/v1/sessions/last', { signal }),
		]);
		today = t;
		session = last;
		if (!t.ok || (r.page !== 'today' && r.page !== 'projects')) {
			return;
		}
		// An earlier day is asked again while it can still change, and when
		// its answer didn't come back.
		const yesterday = addDays(t.data.date, -1);
		const ask = dates(firstDay(r, t.data.date), yesterday).filter((date) => {
			const got = past.get(date);
			return !got?.ok || mayChange(got.data, date === yesterday, t.data.shadow);
		});
		if (ask.length === 0) {
			return;
		}
		const got = await Promise.all(ask.map((date) => api.get('/v1/days/{date}', { params: { date }, signal })));
		const next = new Map(past);
		ask.forEach((date, i) => {
			const day = got[i];
			// An answer that didn't come back doesn't replace one that did: the
			// day keeps its last numbers until the next ask.
			if (day && (day.ok || !next.get(date)?.ok)) {
				next.set(date, day);
			}
		});
		past = next;
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
		<Settings status={status.data} shadow={today?.ok ? today.data.shadow : undefined} />
	{:else if today === undefined}
		<h1>{titleOf(route)}</h1>
		<p aria-live="polite">Reading your time…</p>
	{:else if !today.ok}
		<h1>{titleOf(route)}</h1>
		{#if today.problem === 'signed-out'}
			<SignedOut expired={false} />
		{:else}
			<section class="pt-card" aria-labelledby="days-error-title" data-testid="days-error">
				<h2 id="days-error-title">pairtime couldn't read your time</h2>
				<p>{today.detail || `The agent said ${today.status}.`}</p>
			</section>
		{/if}
	{:else if route.page === 'projects'}
		<Projects days={shown.days} unread={shown.unread} range={route.range} {from} to={today.data.date} />
	{:else}
		<Today
			today={today.data}
			week={shown.days}
			unread={shown.unread}
			session={session?.ok ? session.data : undefined}
			focus={status.data.focus}
		/>
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
