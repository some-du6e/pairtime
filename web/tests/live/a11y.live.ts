// SPDX-License-Identifier: AGPL-3.0-or-later

// D-server-ui's test 7: pairtimed's own pages keep the accessibility the
// shared components keep (a11y.spec.ts), as TestLiveServer
// (cmd/pairtimed/live_test.go) serves them. Link and button text has
// contrast of at least 4.5:1, keyboard focus shows a ring at least 2 px
// wide on every link and button Tab reaches, in light and in dark, and the
// first Tab reaches "Skip to content", whose Enter moves focus to <main>.
// Each later WP adds its pages to the list. rehearsal.yml runs this file.

import { type APIRequestContext, expect, type Page, test } from '@playwright/test';
import { contrast, look } from '../support/style.ts';

const control = `http://127.0.0.1:${process.env.PAIRTIME_LIVE_CONTROL ?? '4181'}`;

/** A page the harness serves, and whether the browser signs in for it. */
type Where = { name: string; path: string; signedIn: boolean };

const pages: Where[] = [
	{ name: 'the home page', path: '/', signedIn: false },
	{ name: 'the privacy notice', path: '/privacy', signedIn: false },
	{ name: 'the evidence page', path: '/docs/evidence', signedIn: false },
	{ name: "the owner's dashboard", path: '/dashboard', signedIn: true },
	{ name: '/device', path: '/device', signedIn: true },
];

// The links and buttons Tab reaches, and everything it can reach.
const links = 'a[href]:not([tabindex="-1"]):visible, button:not([disabled]):not([tabindex="-1"]):visible';
const stops = `${links}, input:visible, select:visible, textarea:visible, summary:visible, [tabindex]:not([tabindex="-1"]):visible`;

/** site is the origin the harness serves pairtimed at, behind its proxy. */
async function site(request: APIRequestContext): Promise<string> {
	const r = await request.get(`${control}/ready`);
	expect(r.ok()).toBe(true);
	const { base } = (await r.json()) as { base: string };
	return base;
}

/** open loads where, signing in through the fake Hack Club Auth first when it needs a session. */
async function open(page: Page, request: APIRequestContext, where: Where): Promise<void> {
	const base = await site(request);
	if (where.signedIn) {
		await page.goto(`${base}/auth/hca/login?return_to=${encodeURIComponent(where.path)}`);
		await expect(page).toHaveURL(`${base}${where.path}`);
	} else {
		await page.goto(`${base}${where.path}`);
	}
	if (where.path === '/dashboard') {
		// The page's script follows its stream from here on.
		await expect(page.locator('#live')).toHaveAttribute('data-stream', 'open');
	}
}

for (const colorScheme of ['light', 'dark'] as const) {
	test.describe(`pairtimed's pages, ${colorScheme} scheme`, () => {
		test.use({ colorScheme });

		for (const where of pages) {
			test(`${where.name}: every link and button has text contrast of at least 4.5:1`, async ({ page, request }) => {
				await open(page, request, where);
				const looks = await Promise.all((await page.locator(links).all()).map((el) => el.evaluate(look)));
				expect(looks.length).toBeGreaterThan(0);
				for (const l of looks) {
					if (l.text !== '') {
						expect(contrast(l.color, l.fill), `"${l.text}" on ${where.path}`).toBeGreaterThanOrEqual(4.5);
					}
				}
			});

			test(`${where.name}: keyboard focus shows a ring at least 2 px wide on every link and button Tab reaches`, async ({
				page,
				request,
			}) => {
				await open(page, request, where);
				const count = await page.locator(links).count();
				const tabs = await page.locator(stops).count();
				let seen = 0;
				// Tab goes once round the page: each element it reaches is
				// marked, and the walk ends at the first one it reaches again.
				for (let i = 0; i < tabs + 2; i++) {
					await page.keyboard.press('Tab');
					const tag = await page.evaluate(() => {
						const el = document.activeElement;
						if (!el || el === document.body) {
							return '';
						}
						if (el.hasAttribute('data-tabbed')) {
							return 'again';
						}
						el.setAttribute('data-tabbed', '');
						return el.tagName;
					});
					if (tag === 'again') {
						break;
					}
					if (tag !== 'A' && tag !== 'BUTTON') {
						continue;
					}
					const l = await page.locator(':focus').evaluate(look);
					expect(l.outline.style, `"${l.text}" on ${where.path}`).not.toBe('none');
					expect(l.outline.width, `"${l.text}" on ${where.path}`).toBeGreaterThanOrEqual(2);
					seen++;
				}
				expect(seen, `the links and buttons Tab reached on ${where.path}`).toBe(count);
			});
		}
	});
}

for (const where of pages) {
	test(`${where.name}: the first Tab reaches a skip link that moves focus to the main content`, async ({ page, request }) => {
		await open(page, request, where);
		await page.keyboard.press('Tab');
		await expect(page.locator(':focus')).toHaveText('Skip to content');
		await page.keyboard.press('Enter');
		await expect(page.locator(':focus')).toHaveAttribute('id', 'main');
	});
}
