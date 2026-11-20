// SPDX-License-Identifier: AGPL-3.0-or-later

// A fake pairtime agent for Playwright. The built dashboard runs under
// `vite preview --mode agent`, and this answers its requests to /v1/ with
// the answers in fake-days.ts, as the real agent will once WP-AG4's stores
// serve GET /v1/days/{date} and GET /v1/sessions/last. It remembers every
// request, so a spec can check what the page asked for.

import type { Page } from '@playwright/test';
import type { components } from '../../src/gen/local-api.ts';
import { dayReply, lastSession, status } from './fake-days.ts';

type Schemas = components['schemas'];

export interface FakeOptions {
	/** false answers every request with 401, as to a browser without the cookie. */
	signedIn?: boolean;
	/** Status.focus, which WP-AG6 adds to the real reply. */
	focus?: Schemas['Focus'];
	/** display.shadow. */
	shadow?: boolean;
	/** GET /v1/days/{date}; called for each request, so a spec can change the answer. */
	day?: (date: string) => Schemas['Day'];
	/** GET /v1/sessions/last. */
	session?: () => Schemas['LastSession'];
}

export interface Fake {
	/** Every request the page sent to /v1/, in order. */
	requests: URL[];
}

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
				contentType: 'text/plain; charset=utf-8',
				body: 'unauthorized: open the dashboard with `pairtime dashboard`, or send the bearer token from port.json',
			});
			return;
		}
		const day = /^\/v1\/days\/([^/]+)$/.exec(url.pathname);
		if (url.pathname === '/v1/status') {
			await route.fulfill({ json: options.focus ? status({ focus: options.focus }) : status() });
		} else if (day?.[1]) {
			const date = decodeURIComponent(day[1]);
			const reply = options.day ? options.day(date) : dayReply(date);
			await route.fulfill({ json: { ...reply, shadow } });
		} else if (url.pathname === '/v1/sessions/last') {
			const reply = options.session ? options.session() : lastSession();
			await route.fulfill({ json: { ...reply, shadow } });
		} else {
			await route.fulfill({ status: 404, contentType: 'text/plain; charset=utf-8', body: '404 page not found' });
		}
	});
	return fake;
}
