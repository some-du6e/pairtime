// SPDX-License-Identifier: AGPL-3.0-or-later

// Pictures for review, not checks: the shared components and the dashboard
// in both color schemes, written to web/screens/. web.yml keeps them for 7
// days as the "screens" artifact, so a reviewer can see the UI without
// building it. The dashboard's pages are shown twice: as the real agent
// serves them, whose days are empty until WP-AG4, and with the fake
// agent's week (support/fake-days.ts), so there is something to look at.

import { test } from '@playwright/test';
import { agentToken, agentURL, dashboardURL, galleryURL } from './support/env.ts';
import { type FakeOptions, fakeAgent } from './support/fake-agent.ts';

const fakePages: { name: string; hash: string; wait: string; options?: FakeOptions }[] = [
	{ name: 'today', hash: '#/today', wait: 'today' },
	{ name: 'today-focus-unknown', hash: '#/today', wait: 'focus-unknown', options: { focus: { known: false, gnome_extension: 'needs_login' } } },
	{ name: 'today-calibrating', hash: '#/today', wait: 'calibrating', options: { shadow: true } },
	{ name: 'projects', hash: '#/projects', wait: 'project' },
	{ name: 'privacy', hash: '#/privacy', wait: 'kept' },
	{ name: 'settings', hash: '#/settings', wait: 'agent-status' },
];

for (const colorScheme of ['light', 'dark'] as const) {
	test.describe(`screens, ${colorScheme} scheme`, () => {
		test.use({ colorScheme, viewport: { width: 960, height: 720 } });

		test('the component gallery', async ({ page }) => {
			await page.goto(galleryURL);
			await page.screenshot({ path: `screens/gallery-${colorScheme}.png`, fullPage: true });
		});

		test('the dashboard, signed out and signed in', async ({ browser }) => {
			test.skip(!agentURL, 'set PAIRTIME_BIN to a pairtime binary built after `npm run build`');
			const base = agentURL ?? '';
			const out = await browser.newContext({ colorScheme, viewport: { width: 960, height: 540 } });
			const signedOut = await out.newPage();
			await signedOut.goto(base);
			await signedOut.getByTestId('signed-out').waitFor();
			await signedOut.screenshot({ path: `screens/dashboard-signed-out-${colorScheme}.png` });
			await out.close();
			const signedIn = await browser.newContext({
				colorScheme,
				viewport: { width: 960, height: 540 },
				extraHTTPHeaders: { Authorization: `Bearer ${agentToken()}` },
			});
			const page = await signedIn.newPage();
			await page.goto(base);
			await page.getByTestId('today').waitFor();
			await page.screenshot({ path: `screens/dashboard-signed-in-${colorScheme}.png` });
			await signedIn.close();
		});

		for (const p of fakePages) {
			test(`the dashboard's ${p.name} page with the fake agent's week`, async ({ page }) => {
				await fakeAgent(page, p.options);
				await page.goto(dashboardURL + p.hash);
				await page.getByTestId(p.wait).first().waitFor();
				await page.screenshot({ path: `screens/fake-${p.name}-${colorScheme}.png`, fullPage: true });
			});
		}

		test("the dashboard's Today page on a phone", async ({ browser }) => {
			const context = await browser.newContext({ colorScheme, viewport: { width: 360, height: 780 } });
			const page = await context.newPage();
			await fakeAgent(page);
			await page.goto(dashboardURL);
			await page.getByTestId('today').waitFor();
			await page.screenshot({ path: `screens/fake-today-phone-${colorScheme}.png`, fullPage: true });
			await context.close();
		});
	});
}
