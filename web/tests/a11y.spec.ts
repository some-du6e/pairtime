// SPDX-License-Identifier: AGPL-3.0-or-later

// Accessibility basics on the shared components (WCAG 2.2 AA): readable
// button text in both color schemes, a visible keyboard focus ring, and a
// skip link to the main content.

import { expect, test } from '@playwright/test';
import { galleryURL } from './support/env.ts';
import { contrast, look } from './support/style.ts';

for (const colorScheme of ['light', 'dark'] as const) {
	test.describe(`accessibility, ${colorScheme} scheme`, () => {
		test.use({ colorScheme });

		test('every button has text contrast of at least 4.5:1', async ({ page }) => {
			await page.goto(galleryURL);
			const looks = await Promise.all((await page.getByRole('button').all()).map((b) => b.evaluate(look)));
			expect(looks.length).toBeGreaterThan(0);
			for (const l of looks) {
				expect(contrast(l.color, l.fill), l.text).toBeGreaterThanOrEqual(4.5);
			}
		});

		test('keyboard focus shows a ring at least 2 px wide on every button', async ({ page }) => {
			await page.goto(galleryURL);
			const count = await page.getByRole('button').count();
			let seen = 0;
			// The skip link comes first, then every button in page order.
			for (let i = 0; i < count + 1; i++) {
				await page.keyboard.press('Tab');
				const focused = await page.evaluate(() => document.activeElement?.tagName);
				if (focused !== 'BUTTON') {
					continue;
				}
				const l = await page.locator(':focus').evaluate(look);
				expect(l.outline.style, l.text).not.toBe('none');
				expect(l.outline.width, l.text).toBeGreaterThanOrEqual(2);
				seen++;
			}
			expect(seen).toBe(count);
		});
	});
}

test('the first Tab reaches a skip link that moves focus to the main content', async ({ page }) => {
	await page.goto(galleryURL);
	await page.keyboard.press('Tab');
	await expect(page.locator(':focus')).toHaveText('Skip to content');
	await page.keyboard.press('Enter');
	await expect(page.locator(':focus')).toHaveAttribute('id', 'main');
});
