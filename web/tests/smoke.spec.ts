// SPDX-License-Identifier: AGPL-3.0-or-later

// The built dashboard as the agent serves it. web.yml runs `npm run build`,
// which writes the real UI into internal/webui/dist, builds `pairtime` with
// it embedded, and points PAIRTIME_BIN at that binary;
// playwright.config.ts runs `pairtime agent` in a fresh data directory.
// The spec in docs/api/local.openapi.yaml is held to the agent's real
// answers here, since the UI's types come from it, and so is DESIGN 3.9's
// sign-in: a one-time URL that `pairtime dashboard` gets over the agent's
// socket and opens from a file, and that sets a SameSite=Strict, HttpOnly
// cookie, without which no API call works (D-dashboard-link).

import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { type Browser, expect, test } from '@playwright/test';
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

/** Values for the spec's path parameters, so every GET can be asked. */
const pathValues: Record<string, string> = { date: 'today' };

/** path with each {name} filled from pathValues. */
function filled(path: string): string {
	return path.replace(/\{(\w+)\}/g, (m, name: string) => {
		const value = pathValues[name];
		if (value === undefined) {
			throw new Error(`${path} takes {${name}}; give this test a value for it in pathValues`);
		}
		return value;
	});
}

function bearer(): { Authorization: string } {
	return { Authorization: `Bearer ${agentToken()}` };
}

/**
 * Runs `pairtime dashboard` with args against the agent under test, with
 * env added to this process's, and returns what it printed.
 */
function dashboard(args: string[], env: Record<string, string> = {}): string {
	const bin = process.env.PAIRTIME_BIN;
	const home = process.env.PAIRTIME_E2E_HOME;
	if (!bin || !home) {
		throw new Error('PAIRTIME_BIN and PAIRTIME_E2E_HOME must be set; playwright.config.ts sets the second from the first');
	}
	return execFileSync(bin, ['dashboard', ...args], {
		env: { ...process.env, PAIRTIME_HOME: home, ...env },
		encoding: 'utf8',
		timeout: 30_000,
	});
}

/** A one-time sign-in URL, as `pairtime dashboard --no-open` gets it from the agent's socket. */
function signInURL(): string {
	const url = dashboard(['--no-open']).trim();
	expect(url.startsWith(`${origin}/signin?code=`), url).toBe(true);
	return url;
}

/** A new browser context signed in with a link from `pairtime dashboard`. */
async function signedIn(browser: Browser) {
	const url = signInURL();
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

test('everything the page loads comes from the agent, with no errors, signed out and signed in', async ({ browser, page }) => {
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

	const { context, page: inside } = await signedIn(browser);
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

test('a sign-in link opens the dashboard with a SameSite=Strict, HttpOnly session cookie', async ({ browser }) => {
	const { context, page } = await signedIn(browser);
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
	const { context, page } = await signedIn(browser);
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

test('a new agent has counted nothing yet, and Today says so', async ({ browser }) => {
	const { context, page } = await signedIn(browser);
	await expect(page.getByTestId('nothing-yet')).toBeVisible();
	await context.close();
});

test('a sign-in link works once; a second browser lands on the expired card', async ({ browser }) => {
	const url = signInURL();
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

test("the session cookie opens every secured GET in the spec from the dashboard's own page", async ({ browser }) => {
	const { context, page } = await signedIn(browser);
	for (const [path] of gets()) {
		const status = await page.evaluate(async (p) => (await fetch(p, { cache: 'no-store' })).status, filled(path));
		expect(status, path).toBe(200);
	}
	await context.close();
});

test("a signed-in browser can't get a sign-in link of its own, or stop the agent", async ({ browser }) => {
	const { context, page } = await signedIn(browser);
	const post = (path: string) => page.evaluate(async (p) => (await fetch(p, { method: 'POST' })).status, path);
	// Links come from the agent's socket alone, and only the bearer token
	// stops the agent.
	expect(await post('/v1/dashboard/link')).toBe(404);
	expect(await post('/v1/agent/stop')).toBe(401);
	await context.close();
});

test('`pairtime dashboard` opens a page only you can read, whose path holds no code, and the page signs the browser in', async ({
	browser,
}) => {
	test.skip(process.platform !== 'linux', 'the stand-in for xdg-open is a shell script');
	// A stand-in for xdg-open that writes down what it was given. Other users
	// of a Linux computer can read every process's command line in /proc.
	const opener = mkdtempSync(join(tmpdir(), 'pairtime-opener-'));
	const said = join(opener, 'argv');
	writeFileSync(join(opener, 'xdg-open'), `#!/bin/sh\nprintf '%s\\n' "$@" > '${said}'\n`, { mode: 0o755 });
	// And an xdg-mime that knows no HTML handler, so the page goes in the
	// data directory whatever browser the runner has (signInDir).
	writeFileSync(join(opener, 'xdg-mime'), '#!/bin/sh\nexit 1\n', { mode: 0o755 });
	const out = dashboard([], { PATH: `${opener}:${process.env.PATH ?? ''}` });
	expect(out).toContain('Opening your dashboard in your browser.');
	const argv = readFileSync(said, 'utf8').trim().split('\n');
	expect(argv).toHaveLength(1);
	const opened = argv[0] ?? '';
	expect(opened).not.toContain('code=');
	expect(opened.startsWith(join(process.env.PAIRTIME_E2E_HOME ?? '?', 'signin-')), opened).toBe(true);
	expect(statSync(opened).mode & 0o777).toBe(0o600);
	const context = await browser.newContext();
	const page = await context.newPage();
	await page.goto(pathToFileURL(opened).href);
	await expect(page).toHaveURL(base);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Today');
	await context.close();
});

test('every secured GET in the spec answers 200 in the shape the spec gives', async ({ request }) => {
	const ops = gets();
	expect(ops.map(([path]) => path)).toEqual(expect.arrayContaining(['/v1/days/{date}', '/v1/sessions/last']));
	for (const [path, op] of ops) {
		const res = await request.get(new URL(filled(path).slice(1), base).href, { headers: bearer() });
		expect(res.status(), path).toBe(200);
		const schema = op.responses?.['200']?.content?.['application/json']?.schema;
		if (!schema) {
			throw new Error(`the spec gives ${path} no application/json schema for 200`);
		}
		expect(check(await res.json(), schema, spec), path).toEqual([]);
	}
});

test('GET /v1/days/{date} takes a date or today, and refuses anything else', async ({ request }) => {
	const day = (date: string) => request.get(new URL(`v1/days/${date}`, base).href, { headers: bearer() });
	const res = await day('2026-11-20');
	expect(res.status()).toBe(200);
	const body: unknown = await res.json();
	expect(check(body, { $ref: '#/components/schemas/Day' }, spec)).toEqual([]);
	expect((body as { date: string }).date).toBe('2026-11-20');
	const today = (await (await day('today')).json()) as { date: string };
	expect(today.date).toMatch(/^\d{4}-\d{2}-\d{2}$/);
	expect((await day('2026-02-30')).status()).toBe(400);
	expect((await day('tomorrow')).status()).toBe(400);
});

test('without the token or the cookie every secured GET in the spec answers 401, as the spec says', async ({ request }) => {
	for (const [path] of gets()) {
		const res = await request.get(new URL(filled(path).slice(1), base).href);
		expect(res.status(), path).toBe(401);
		expect(res.headers()['www-authenticate'], path).toBe('Bearer realm="pairtime"');
	}
});

test('a request from another site gets 403, for the page and the API alike', async ({ request }) => {
	for (const path of ['', 'v1/status', 'v1/days/today']) {
		const res = await request.get(new URL(path, base).href, {
			headers: { Origin: 'https://example.com', Authorization: `Bearer ${agentToken()}` },
		});
		expect(res.status(), `/${path}`).toBe(403);
	}
});
