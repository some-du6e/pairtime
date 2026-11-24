// SPDX-License-Identifier: AGPL-3.0-or-later

// WP-SV8's check of DESIGN 2's R5 row: "a delta reaches an open dashboard
// within 2 s of ingest", by Playwright's clock. It runs against
// TestLiveServer (cmd/pairtimed/live_test.go): pairtimed serve with the fake
// Hack Club Auth, behind a TLS proxy where Cloudflare's edge would be. The
// browser signs in itself, and the harness's control server sends signed
// uploads from a computer linked to the same account through the device
// grant. playwright.live.config.ts starts the harness; rehearsal.yml runs
// this file.

import { type APIRequestContext, expect, type Page, test } from '@playwright/test';

const control = `http://127.0.0.1:${process.env.PAIRTIME_LIVE_CONTROL ?? '4181'}`;

// The specs share one account and run in order: the first one's uploads
// are the numbers the others see.
test.describe.configure({ mode: 'serial' });

type Upload = { minute: number; rev: number; seconds: number; state: 'final' | 'pending' };

/** site is the origin the harness serves pairtimed at, behind its proxy. */
async function site(request: APIRequestContext): Promise<string> {
	const r = await request.get(`${control}/ready`);
	expect(r.ok()).toBe(true);
	const { base } = (await r.json()) as { base: string };
	return base;
}

/** signIn signs the browser in through the fake Hack Club Auth. */
async function signIn(page: Page, base: string): Promise<void> {
	await page.goto(`${base}/auth/hca/login?return_to=${encodeURIComponent('/dashboard')}`);
	await expect(page).toHaveURL(`${base}/dashboard`);
}

/** upload sends one minute's revision through the harness's computer. */
async function upload(request: APIRequestContext, u: Upload): Promise<void> {
	const r = await request.post(`${control}/upload`, { data: u });
	expect(r.ok(), await r.text()).toBe(true);
	const answer = (await r.json()) as { status: number; body: { buckets?: { code: string }[] } };
	expect(answer.status).toBe(200);
	expect(answer.body.buckets?.[0]?.code).toBe('accepted');
}

test('an upload reaches an open dashboard within 2 s of ingest, and its pending minutes are drawn lighter', async ({
	page,
	request,
}) => {
	const base = await site(request);
	await signIn(page, base);
	const live = page.locator('#live');
	await expect(live).toHaveAttribute('data-stream', 'open');
	await expect(live).toContainText('Nothing counted yet.');
	const hero = live.locator('.hero strong');

	let start = Date.now();
	await upload(request, { minute: 0, rev: 1, seconds: 60, state: 'final' });
	await expect(hero).toHaveText('1m', { timeout: 2000 });
	expect(Date.now() - start).toBeLessThan(2000);

	start = Date.now();
	await upload(request, { minute: 1, rev: 1, seconds: 60, state: 'pending' });
	await expect(hero).toHaveText('2m', { timeout: 2000 });
	expect(Date.now() - start).toBeLessThan(2000);
	await expect(live.locator('.lede').first()).toContainText('1m of your time is still pending.');
	const faded = live.locator('.seg.pend').first();
	await expect(faded).toBeVisible();
	const opacity = Number(await faded.evaluate((el) => getComputedStyle(el).opacity));
	expect(opacity).toBeLessThan(1);
});

test('a dashboard whose stream stays silent for 45 s polls the snapshot every 30 s with If-None-Match, and gets 304', async ({
	page,
	request,
}) => {
	const base = await site(request);
	await page.clock.install();
	// A proxy that holds the stream back: nothing of it ever arrives.
	await page.route('**/api/v1/stream**', (route) => route.abort());
	const polls: { ifNoneMatch: string | undefined; status: number }[] = [];
	page.on('response', (r) => {
		if (new URL(r.url()).pathname === '/api/v1/users/me/dashboard') {
			polls.push({ ifNoneMatch: r.request().headers()['if-none-match'], status: r.status() });
		}
	});
	await signIn(page, base);
	await expect(page.locator('#live .hero strong')).toHaveText('2m');
	// From here the page's time moves only when the spec moves it.
	const now = await page.evaluate(() => Date.now());
	await page.clock.pauseAt(now + 1000);
	await page.clock.fastForward(40_000);
	expect(polls).toHaveLength(0);
	await page.clock.fastForward(6_000);
	await expect.poll(() => polls.length).toBe(1);
	await page.clock.fastForward(30_000);
	await expect.poll(() => polls.length).toBe(2);
	for (const p of polls) {
		expect(p.ifNoneMatch).toMatch(/^W\/"[0-9a-f]{32}"$/);
		expect(p.status).toBe(304);
	}
});

test.describe('with JavaScript off', () => {
	test.use({ javaScriptEnabled: false });

	test('the page still shows every number, drawn by the server', async ({ page, request }) => {
		const base = await site(request);
		await signIn(page, base);
		await expect(page.locator('#live .hero strong')).toHaveText('2m');
		await expect(page.locator('#live')).not.toHaveAttribute('data-stream', /.+/);
	});
});

test("the time zone form's error page follows the stream too, refetching the dashboard rather than the form's address", async ({
	page,
	request,
}) => {
	const base = await site(request);
	await signIn(page, base);
	await page.locator('#zone-name').fill('Nowhere/Land');
	await page.locator('form.zone button[type="submit"]').click();
	await expect(page).toHaveURL(`${base}/dashboard/zone`);
	await expect(page.locator('p.error[role="alert"]')).toBeVisible();
	const live = page.locator('#live');
	await expect(live).toHaveAttribute('data-stream', 'open');
	const start = Date.now();
	await upload(request, { minute: 2, rev: 1, seconds: 60, state: 'final' });
	await expect(live.locator('.hero strong')).toHaveText('3m', { timeout: 2000 });
	expect(Date.now() - start).toBeLessThan(2000);
});

test("an open dashboard fetches itself again at its account's next local midnight", async ({ page, request }) => {
	const base = await site(request);
	await page.clock.install();
	const refetches: string[] = [];
	page.on('request', (r) => {
		if (r.resourceType() === 'fetch' && new URL(r.url()).pathname === '/dashboard') {
			refetches.push(r.url());
		}
	});
	await signIn(page, base);
	const live = page.locator('#live');
	await expect(live).toHaveAttribute('data-stream', 'open');
	// The first open's refetch, for what changed since the page was drawn.
	await expect.poll(() => refetches.length).toBe(1);
	const next = Number(await live.getAttribute('data-next-day'));
	const now = await page.evaluate(() => Date.now());
	expect(next - now).toBeGreaterThan(0);
	expect(next - now).toBeLessThanOrEqual(25 * 3600 * 1000);
	await page.clock.pauseAt(now + 1000);
	await page.clock.fastForward(next - now - 2000);
	expect(refetches).toHaveLength(1);
	// Past midnight and the minute of spread the refetch may wait.
	await page.clock.fastForward(2000 + 60_000 + 1000);
	await expect.poll(() => refetches.length).toBeGreaterThan(1);
});
