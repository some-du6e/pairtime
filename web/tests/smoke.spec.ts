// SPDX-License-Identifier: AGPL-3.0-or-later

// The built dashboard as the agent serves it. web.yml runs `npm run build`,
// which writes the real UI into internal/webui/dist, builds `pairtime` with
// it embedded, and points PAIRTIME_BIN at that binary;
// playwright.config.ts runs `pairtime agent` in a fresh data directory.
// The spec in docs/api/local.openapi.yaml is held to the agent's real
// answers here, since the UI's types come from it.

import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { parse } from 'yaml';
import { agentToken, agentURL } from './support/env.ts';
import { check, type Schema } from './support/schema.ts';

test.skip(!agentURL, 'set PAIRTIME_BIN to a pairtime binary built after `npm run build`');

const base = agentURL ?? 'http://127.0.0.1/';
const origin = new URL(base).origin;
const spec = parse(readFileSync(new URL('../../docs/api/local.openapi.yaml', import.meta.url), 'utf8')) as Schema;

type Operation = { responses?: Record<string, { content?: Record<string, { schema?: Schema }> }> };

/** Every GET operation in the spec, with its path. */
function gets(): [string, Operation][] {
	const paths = (spec.paths ?? {}) as Record<string, { get?: Operation }>;
	return Object.entries(paths).flatMap(([path, item]) => (item.get ? [[path, item.get] as [string, Operation]] : []));
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

test('everything the page loads comes from the agent, with no errors', async ({ page }) => {
	const elsewhere: string[] = [];
	const errors: string[] = [];
	page.on('request', (r) => {
		if (new URL(r.url()).origin !== origin) {
			elsewhere.push(r.url());
		}
	});
	page.on('pageerror', (e) => errors.push(e.message));
	page.on('console', (m) => {
		// The one expected error: the signed-out check gets 401 from
		// GET /v1/status until WP-UI1 adds the dashboard's sign-in.
		if (m.type() === 'error' && !/status of 401/.test(m.text())) {
			errors.push(m.text());
		}
	});
	await page.goto(base);
	await expect(page.getByTestId('signed-out')).toBeVisible();
	expect(elsewhere).toEqual([]);
	expect(errors).toEqual([]);
});

test('signed out, the page says how to open it and shows no data', async ({ page }) => {
	await page.goto(base);
	await expect(page.getByTestId('signed-out')).toContainText('pairtime dashboard');
	await expect(page.getByTestId('agent-status')).toHaveCount(0);
});

test('signed in, the page shows what GET /v1/status says', async ({ browser }) => {
	// The bearer token stands in for the sign-in cookie WP-UI1 adds.
	const context = await browser.newContext({ extraHTTPHeaders: { Authorization: `Bearer ${agentToken()}` } });
	const page = await context.newPage();
	await page.goto(base);
	const status = page.getByTestId('agent-status');
	await expect(status).toContainText('Local only: nothing leaves this computer');
	await expect(status).toContainText('Running since');
	await context.close();
});

test('every GET in the spec answers 200 in the shape the spec gives', async ({ request }) => {
	const ops = gets();
	expect(ops.length).toBeGreaterThan(0);
	for (const [path, op] of ops) {
		if (path.includes('{')) {
			throw new Error(`${path} takes parameters; give this test values for them`);
		}
		const res = await request.get(new URL(path.slice(1), base).href, {
			headers: { Authorization: `Bearer ${agentToken()}` },
		});
		expect(res.status(), path).toBe(200);
		const schema = op.responses?.['200']?.content?.['application/json']?.schema;
		if (!schema) {
			throw new Error(`the spec gives ${path} no application/json schema for 200`);
		}
		expect(check(await res.json(), schema, spec), path).toEqual([]);
	}
});

test('without the token every GET in the spec answers 401, as the spec says', async ({ request }) => {
	for (const [path] of gets()) {
		const res = await request.get(new URL(path.slice(1), base).href);
		expect(res.status(), path).toBe(401);
		expect(res.headers()['www-authenticate'], path).toBe('Bearer realm="pairtime"');
	}
});

test('a request from another site gets 403, for the page and the API alike', async ({ request }) => {
	for (const path of ['', 'v1/status']) {
		const res = await request.get(new URL(path, base).href, {
			headers: { Origin: 'https://example.com', Authorization: `Bearer ${agentToken()}` },
		});
		expect(res.status(), `/${path}`).toBe(403);
	}
});
