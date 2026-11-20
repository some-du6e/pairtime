// SPDX-License-Identifier: AGPL-3.0-or-later

// The oracle for WP-UI0: DESIGN 8.4's consent rule on the rendered card in
// Chromium. The primary-styled button on every consent card is "Not now",
// and nothing is preselected (TE WP4.13). "Primary-styled" is read from the
// computed styles: the button whose fill stands out most from the card
// behind it, whatever the markup calls it.

import { expect, test } from '@playwright/test';
import { galleryURL } from './support/env.ts';
import { look, prominence } from './support/style.ts';

for (const colorScheme of ['light', 'dark'] as const) {
	test.describe(`consent cards, ${colorScheme} scheme`, () => {
		test.use({ colorScheme });

		test.beforeEach(async ({ page }) => {
			await page.goto(galleryURL);
			await expect(page.locator('[data-consent-card]').first()).toBeVisible();
		});

		test('the primary-styled button on every consent card is "Not now"', async ({ page }) => {
			const cards = await page.locator('[data-consent-card]').all();
			expect(cards.length).toBeGreaterThan(0);
			for (const card of cards) {
				const looks = await Promise.all((await card.getByRole('button').all()).map((b) => b.evaluate(look)));
				expect(looks.length).toBeGreaterThan(1);
				const scores = looks.map(prominence);
				const top = Math.max(...scores);
				// Exactly one button has the most prominent fill, it says "Not now",
				// and it clearly stands out (WCAG's 3:1 for non-text contrast).
				expect(looks.filter((_, i) => scores[i] === top).map((l) => l.text)).toEqual(['Not now']);
				expect(top).toBeGreaterThanOrEqual(3);
				// The markup agrees with the styles.
				await expect(card.locator('[data-variant="primary"]')).toHaveText(['Not now']);
			}
		});

		test('nothing on a consent card is preselected or focused', async ({ page }) => {
			const cards = page.locator('[data-consent-card]');
			expect(await page.evaluate(() => document.activeElement === document.body)).toBe(true);
			await expect(
				cards.locator(
					'input:checked, option:checked, [autofocus], [aria-pressed="true"], [aria-checked="true"], [aria-selected="true"]',
				),
			).toHaveCount(0);
			await expect(cards.locator('button:not([type="button"])')).toHaveCount(0);
			// Enter before the teen picks anything answers nothing.
			await page.keyboard.press('Enter');
			await expect(page.getByTestId('consent-answer')).toHaveText('none');
		});

		test('each button reports its own answer', async ({ page }) => {
			const card = page.getByTestId('consent');
			await card.getByRole('button', { name: 'Not now' }).click();
			await expect(page.getByTestId('consent-answer')).toHaveText('not_now');
			await card.getByRole('button', { name: 'Turn on typing evidence' }).click();
			await expect(page.getByTestId('consent-answer')).toHaveText('yes');
		});
	});
}
