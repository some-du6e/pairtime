// SPDX-License-Identifier: AGPL-3.0-or-later

// A fake pairtime agent for Playwright. The built dashboard runs under
// `vite preview --mode agent`, and this answers its requests to /v1/ with
// the answers in fake-days.ts, as the real agent will once WP-AG4's stores
// serve GET /v1/days. It remembers every request, so a spec can check what
// the page asked for.

import type { Page } from '@playwright/test';
import type { components } from '../../src/gen/local-api.ts';
import { monthDays, status, weekDays } from './fake-days.ts';

type Schemas = components['schemas'];

export interface FakeOptions {
	/** false answers every request with 401, as to a browser without the cookie. */
	signedIn?: boolean;
	/** Status.focus, which WP-AG6 adds to the real reply. */
	focus?: Schemas['Focus'];
	/** display.shadow. */
	shadow?: boolean;
	/** GET /v1/days without parameters; called for each request, so a spec can change it. */
	week?: () => Schemas['Days'];
	/** GET /v1/days with from and to. */
	month?: () => Schemas['Days'];
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
		switch (url.pathname) {
			case '/v1/status':
				await route.fulfill({ json: options.focus ? status({ focus: options.focus }) : status() });
				return;
			case '/v1/days': {
				const ranged = url.searchParams.has('from') || url.searchParams.has('to');
				const days = ranged ? (options.month ?? monthDays)() : (options.week ?? weekDays)();
				await route.fulfill({ json: { ...days, shadow } });
				return;
			}
			default:
				await route.fulfill({ status: 404, contentType: 'text/plain; charset=utf-8', body: '404 page not found' });
		}
	});
	return fake;
}
