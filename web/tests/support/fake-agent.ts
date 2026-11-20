// SPDX-License-Identifier: AGPL-3.0-or-later

// A fake pairtime agent for Playwright. The built dashboard runs under
// `vite preview --mode agent`, and this answers its requests to /v1/ with
// the answers in fake-days.ts, in the shapes docs/api/local.openapi.yaml
// gives them, display.shadow's included. It remembers every request, so a
// spec can check what the page asked for.

import type { Page } from '@playwright/test';
import type { components } from '../../src/gen/local-api.ts';
import { dayReply, lastSession, shadowDay, shadowSession, status } from './fake-days.ts';

type Schemas = components['schemas'];

export interface FakeOptions {
	/** false answers every request with 401, as to a browser without the cookie. */
	signedIn?: boolean;
	/** Status.focus, which WP-AG6 adds to the real reply. */
	focus?: Schemas['Focus'];
	/**
	 * display.shadow: each day and the session take the shape the agent
	 * gives them then, with what D-shadow-scope hides left out
	 * (fake-days.ts's shadowDay and shadowSession).
	 */
	shadow?: boolean;
	/** GET /v1/days/{date}; called for each request, so a spec can change the answer. */
	day?: (date: string) => Schemas['Day'];
	/** GET /v1/sessions/last. */
	session?: () => Schemas['LastSession'];
	/**
	 * Whether the request for this path gets a 500, as from an agent that
	 * can't read its stores (the spec's StoreError); called for each
	 * request, so a spec can mend it.
	 */
	unavailable?: (pathname: string) => boolean;
}

export interface Fake {
	/** Every request the page sent to /v1/, in order. */
	requests: URL[];
}

const text = 'text/plain; charset=utf-8';

export async function fakeAgent(page: Page, options: FakeOptions = {}): Promise<Fake> {
	const fake: Fake = { requests: [] };
	const shadow = options.shadow ?? false;
	await page.route('**/v1/**', async (route) => {
		const url = new URL(route.request().url());
		fake.requests.push(url);
		if (options.signedIn === false) {
			await route.fulfill({
				status: 401,
				headers: { 'WWW-Authenticate': 'Bearer realm="pairtime"' },
				contentType: text,
				body: 'unauthorized: open the dashboard with `pairtime dashboard`, or send the bearer token from port.json',
			});
			return;
		}
		if (options.unavailable?.(url.pathname)) {
			await route.fulfill({ status: 500, contentType: text, body: "the agent couldn't read its stores" });
			return;
		}
		const day = /^\/v1\/days\/([^/]+)$/.exec(url.pathname);
		if (url.pathname === '/v1/status') {
			await route.fulfill({ json: options.focus ? status({ focus: options.focus }) : status() });
		} else if (day?.[1]) {
			const date = decodeURIComponent(day[1]);
			if (date !== 'today' && !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
				await route.fulfill({ status: 400, contentType: text, body: 'the date must be YYYY-MM-DD or today' });
				return;
			}
			const reply = options.day ? options.day(date) : dayReply(date);
			await route.fulfill({ json: shadow ? shadowDay(reply) : { ...reply, shadow: false } });
		} else if (url.pathname === '/v1/sessions/last') {
			const reply = options.session ? options.session() : lastSession();
			await route.fulfill({ json: shadow ? shadowSession(reply) : { ...reply, shadow: false } });
		} else {
			await route.fulfill({ status: 404, contentType: text, body: '404 page not found' });
		}
	});
	return fake;
}
