// SPDX-License-Identifier: AGPL-3.0-or-later

// The built dashboard as the agent serves it. web.yml runs `npm run build`,
// which writes the real UI into internal/webui/dist, builds `pairtime` with
// it embedded, and points PAIRTIME_BIN at that binary;
// playwright.config.ts runs `pairtime agent` in a fresh data directory.
// The spec in docs/api/local.openapi.yaml is held to the agent's real
// answers here, since the UI's types come from it, and so is DESIGN 3.9's
// sign-in: a one-time URL that sets a SameSite=Strict, HttpOnly cookie,
// without which no API call works.

import { readFileSync } from 'node:fs';
import { type APIRequestContext, type Browser, expect, test } from '@playwright/test';
import { parse } from 'yaml';
import { agentToken, agentURL } from './support/env.ts';
import { check, type Schema } from './support/schema.ts';

test.skip(!agentURL, 'set PAIRTIME_BIN to a pairtime binary built after `npm run build`');

const base = agentURL ?? 'http://127.0.0.1/';
const origin = new URL(base).origin;
const spec = parse(readFileSync(new URL('../../docs/api/local.openapi.yaml', import.meta.url), 'utf8')) as Schema;

type Operation = {
	security?: unknown[];
	responses?: Record<string, { content?: Record<string, { schema?: Schema }> }>;
};

/**
 * Every GET operation in the spec that needs a credential, with its path.
 * GET /signin takes none: its code is the credential.
 */
function gets(): [string, Operation][] {
	const paths = (spec.paths ?? {}) as Record<string, { get?: Operation }>;
	return Object.entries(paths).flatMap(([path, item]) =>
		item.get && !(Array.isArray(item.get.security) && item.get.security.length === 0)
			? [[path, item.get] as [string, Operation]]
			: [],
	);
}

function bearer(): { Authorization: string } {
	return { Authorization: `Bearer ${agentToken()}` };
}

/** A one-time sign-in URL, as `pairtime dashboard` asks for one. */
async function signInURL(request: APIRequestContext): Promise<string> {
	const res = await request.post(new URL('v1/dashboard/link', base).href, { headers: bearer() });
	expect(res.status()).toBe(201);
	const link: unknown = await res.json();
	expect(check(link, { $ref: '#/components/schemas/DashboardLink' }, spec)).toEqual([]);
	const url = (link as { url: string }).url;
	expect(url.startsWith(`${origin}/signin?code=`), url).toBe(true);
	return url;
}

/** A new browser context signed in the way `pairtime dashboard` signs one in. */
async function signedIn(browser: Browser, request: APIRequestContext) {
	const url = await signInURL(request);
	const context = await browser.newContext();
	const page = await context.newPage();
	await page.goto(url);
	await expect(page).toHaveURL(base);
	return { context, page };
}

test('the agent serves the built dashboard, not the placeholder', async ({ page }) => {
	const res = await page.goto(base);
	expect(res?.status()).toBe(200);
	expect(res?.headers()['x-frame-options']).toBe('DENY');
	await expect(page.locator('meta[name="pairtime-ui"]')).toHaveAttribute('content', 'local');
	await expect(page.locator('meta[http-equiv="Content-Security-Policy"]')).toHaveAttribute(
		'content',
		/default-src 'none'.*script-src 'self'/,
	);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Your dashboard');
});

test('everything the page loads comes from the agent, with no errors, signed out and signed in', async ({
	browser,
	page,
	request,
}) => {
	const elsewhere: string[] = [];
	const errors: string[] = [];
	const watch = (p: typeof page, signedOut: boolean) => {
		p.on('request', (r) => {
			if (new URL(r.url()).origin !== origin) {
				elsewhere.push(r.url());
			}
		});
		p.on('pageerror', (e) => errors.push(e.message));
		p.on('console', (m) => {
			// Signed out, GET /v1/status answers 401, which the browser logs.
			if (m.type() === 'error' && !(signedOut && /status of 401/.test(m.text()))) {
				errors.push(m.text());
			}
		});
	};
	watch(page, true);
	await page.goto(base);
	await expect(page.getByTestId('signed-out')).toBeVisible();

	const { context, page: inside } = await signedIn(browser, request);
	watch(inside, false);
	for (const [name, heading] of [
		['Today', 'Today'],
		['Projects', 'Projects'],
		['Privacy', 'Privacy'],
		['Settings', 'Settings'],
	] as const) {
		await inside.getByRole('navigation', { name: 'Pages' }).getByRole('link', { name }).click();
		await expect(inside.getByRole('heading', { level: 1 })).toHaveText(heading);
	}
	await context.close();
	expect(elsewhere).toEqual([]);
	expect(errors).toEqual([]);
});

test('signed out, the page says to run `pairtime dashboard` and shows no data', async ({ page }) => {
	await page.goto(base);
	const signedOut = page.getByTestId('signed-out');
	await expect(signedOut.locator('code')).toHaveText('pairtime dashboard');
	await expect(page.getByRole('navigation', { name: 'Pages' })).toHaveCount(0);
	await expect(page.getByTestId('today')).toHaveCount(0);
	await expect(page.getByTestId('agent-status')).toHaveCount(0);
});

test('a sign-in link opens the dashboard with a SameSite=Strict, HttpOnly session cookie', async ({ browser, request }) => {
	const { context, page } = await signedIn(browser, request);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Today');
	const session = (await context.cookies(base)).find((c) => c.name === 'pairtime_session');
	expect(session?.httpOnly).toBe(true);
	expect(session?.sameSite).toBe('Strict');
	expect(session?.path).toBe('/');
	await context.close();
});

test('signed in, the page shows what GET /v1/status says', async ({ browser, request }) => {
	const res = await request.get(new URL('v1/status', base).href, { headers: bearer() });
	expect(res.status()).toBe(200);
	const body = (await res.json()) as { version: { version: string }; started: string };
	const { context, page } = await signedIn(browser, request);
	await page.getByRole('link', { name: 'Settings' }).click();
	const status = page.getByTestId('agent-status');
	await expect(page.getByTestId('mode')).toContainText('Local only: nothing leaves this computer');
	await expect(status.getByTestId('agent-version')).toHaveText(body.version.version);
	// The start time, as the page's own browser writes it.
	const time = status.locator('time');
	await expect(time).toHaveAttribute('datetime', body.started);
	const shown = await page.evaluate((started) => new Date(started).toLocaleString(), body.started);
	expect(shown).not.toBe('Invalid Date');
	await expect(time).toHaveText(shown);
	await context.close();
});

test('until WP-AG4 serves its stores, Today has nothing yet and says so', async ({ browser, request }) => {
	const { context, page } = await signedIn(browser, request);
	await expect(page.getByTestId('nothing-yet')).toBeVisible();
	await context.close();
});

test('a sign-in link works once; a second browser lands on the expired card', async ({ browser, request }) => {
	const url = await signInURL(request);
	const first = await browser.newContext();
	await (await first.newPage()).goto(url);
	const second = await browser.newContext();
	const page = await second.newPage();
	await page.goto(url);
	await expect(page.getByTestId('link-expired')).toBeVisible();
	// The page took ?signin=expired out of the address bar.
	await expect(page).toHaveURL(base);
	expect((await second.cookies(base)).filter((c) => c.name === 'pairtime_session')).toEqual([]);
	await first.close();
	await second.close();
});

test("the session cookie opens every secured GET in the spec from the dashboard's own page", async ({ browser, request }) => {
	const { context, page } = await signedIn(browser, request);
	for (const [path] of gets()) {
		const status = await page.evaluate(async (p) => (await fetch(p, { cache: 'no-store' })).status, path);
		expect(status, path).toBe(200);
	}
	await context.close();
});

test("a signed-in browser can't get a sign-in link of its own", async ({ browser, request }) => {
	const { context, page } = await signedIn(browser, request);
	const status = await page.evaluate(async () => (await fetch('/v1/dashboard/link', { method: 'POST' })).status);
	expect(status).toBe(401);
	await context.close();
});

test('every secured GET in the spec answers 200 in the shape the spec gives', async ({ request }) => {
	const ops = gets();
	expect(ops.map(([path]) => path)).toContain('/v1/days');
	for (const [path, op] of ops) {
		if (path.includes('{')) {
			throw new Error(`${path} takes parameters; give this test values for them`);
		}
		const res = await request.get(new URL(path.slice(1), base).href, { headers: bearer() });
		expect(res.status(), path).toBe(200);
		const schema = op.responses?.['200']?.content?.['application/json']?.schema;
		if (!schema) {
			throw new Error(`the spec gives ${path} no application/json schema for 200`);
		}
		expect(check(await res.json(), schema, spec), path).toEqual([]);
	}
});

test('GET /v1/days takes a range and refuses one the spec refuses', async ({ request }) => {
	const days = (q: string) => request.get(new URL(`v1/days?${q}`, base).href, { headers: bearer() });
	const res = await days('from=2026-10-22&to=2026-11-20');
	expect(res.status()).toBe(200);
	const body = (await res.json()) as { from: string; to: string; days: unknown[] };
	expect([body.from, body.to, body.days.length]).toEqual(['2026-10-22', '2026-11-20', 30]);
	expect((await days('from=2026-11-20&to=2026-11-19')).status()).toBe(400);
	expect((await days('to=tomorrow')).status()).toBe(400);
});

test('without the token or the cookie every secured GET in the spec answers 401, as the spec says', async ({ request }) => {
	for (const [path] of gets()) {
		const res = await request.get(new URL(path.slice(1), base).href);
		expect(res.status(), path).toBe(401);
		expect(res.headers()['www-authenticate'], path).toBe('Bearer realm="pairtime"');
	}
});

test('a request from another site gets 403, for the page and the API alike', async ({ request }) => {
	for (const path of ['', 'v1/status', 'v1/days']) {
		const res = await request.get(new URL(path, base).href, {
			headers: { Origin: 'https://example.com', Authorization: `Bearer ${agentToken()}` },
		});
		expect(res.status(), `/${path}`).toBe(403);
	}
});
