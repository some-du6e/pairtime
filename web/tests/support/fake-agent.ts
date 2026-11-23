// SPDX-License-Identifier: AGPL-3.0-or-later

// A fake pairtime agent for Playwright. The built dashboard runs under
// `vite preview --mode agent`, and this answers its requests to /v1/ with
// the answers in fake-days.ts, in the shapes docs/api/local.openapi.yaml
// gives them, display.shadow's included. It remembers every request, so a
// spec can check what the page asked for.

import type { Page } from '@playwright/test';
import type { components } from '../../src/gen/local-api.ts';
import type { Focus } from '../../src/local/api.ts';
import { dayReply, lastSession, nameAnswer, projectsReply, shadowDay, shadowSession, signalsFor, status } from './fake-days.ts';

type Schemas = components['schemas'];

export interface FakeOptions {
	/** false answers every request with 401, as to a browser without the cookie. */
	signedIn?: boolean;
	/** What GET /v1/status's signals say of focus (signalsFor). */
	focus?: Focus;
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
	/** GET /v1/projects at the start; PUT /v1/projects/{key}/name changes it as the agent would. */
	projects?: Schemas['Projects'];
	/** A refusal PUT /v1/projects/{key}/name gets for key, as the agent's 409 when config.toml has an error. */
	refuseName?: (key: string) => { status: number; text: string } | undefined;
}

export interface Fake {
	/** Every request the page sent to /v1/, in order. */
	requests: URL[];
	/** The body of every PUT /v1/projects/{key}/name, with its key, in order. */
	names: { key: string; body: unknown }[];
}

const text = 'text/plain; charset=utf-8';

export async function fakeAgent(page: Page, options: FakeOptions = {}): Promise<Fake> {
	const fake: Fake = { requests: [], names: [] };
	const shadow = options.shadow ?? false;
	const list = structuredClone(options.projects ?? projectsReply());
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
		const name = /^\/v1\/projects\/([^/]+)\/name$/.exec(url.pathname);
		if (url.pathname === '/v1/projects' && route.request().method() === 'GET') {
			await route.fulfill({ json: list });
		} else if (name?.[1] && route.request().method() === 'PUT') {
			const key = decodeURIComponent(name[1]);
			const body: unknown = route.request().postDataJSON();
			fake.names.push({ key, body });
			const at = list.projects.findIndex((p) => p.project === key);
			const entry = list.projects[at];
			const refused = options.refuseName?.(key);
			if (!entry) {
				await route.fulfill({ status: 404, contentType: text, body: 'no project on this computer has that key' });
			} else if (refused) {
				await route.fulfill({ status: refused.status, contentType: text, body: refused.text });
			} else {
				const answer = nameAnswer(entry, body);
				if ('entry' in answer) {
					list.projects[at] = answer.entry;
					await route.fulfill({ json: answer.entry });
				} else {
					await route.fulfill({ status: answer.status, contentType: text, body: answer.text });
				}
			}
		} else if (url.pathname === '/v1/status') {
			await route.fulfill({ json: options.focus ? status({ signals: signalsFor(options.focus) }) : status() });
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
