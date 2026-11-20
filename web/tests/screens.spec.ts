// SPDX-License-Identifier: AGPL-3.0-or-later

// Pictures for review, not checks: the shared components and the dashboard
// in both color schemes, written to web/screens/. web.yml keeps them for 7
// days as the "screens" artifact, so a reviewer can see the UI without
// building it.

import { test } from '@playwright/test';
import { agentToken, agentURL, galleryURL } from './support/env.ts';

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
			await page.getByTestId('agent-status').waitFor();
			await page.screenshot({ path: `screens/dashboard-signed-in-${colorScheme}.png` });
			await signedIn.close();
		});
	});
}
