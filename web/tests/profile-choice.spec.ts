// SPDX-License-Identifier: AGPL-3.0-or-later

// DESIGN 8.3's profile card on the rendered page. In public mode the two
// choices have equal visual weight: the same computed styles and the same
// size, on a wide screen and a narrow one. In ask mode "Private profile" is
// the more prominent one. Nothing is preselected in either.

import { expect, test } from '@playwright/test';
import { galleryURL } from './support/env.ts';
import { look, prominence } from './support/style.ts';

for (const colorScheme of ['light', 'dark'] as const) {
	test.describe(`profile choice, ${colorScheme} scheme`, () => {
		test.use({ colorScheme });

		for (const viewport of [
			{ width: 1280, height: 800 },
			{ width: 360, height: 780 },
		]) {
			test(`in public mode both choices look the same and are the same size, ${viewport.width} px wide`, async ({
				page,
			}) => {
				await page.setViewportSize(viewport);
				await page.goto(galleryURL);
				const buttons = page.getByTestId('profile-public').getByRole('button');
				await expect(buttons).toHaveCount(2);
				const [a, b] = await Promise.all((await buttons.all()).map((x) => x.evaluate(look)));
				if (!a || !b) {
					throw new Error('expected two buttons');
				}
				expect([a.text, b.text]).toEqual(['Keep my profile public', 'Make it private']);
				expect(b.styles).toEqual(a.styles);
				expect(Math.abs(a.width - b.width)).toBeLessThan(1);
				expect(Math.abs(a.height - b.height)).toBeLessThan(1);
			});
		}

		test('in ask mode "Private profile" is the primary-styled choice', async ({ page }) => {
			await page.goto(galleryURL);
			const looks = await Promise.all(
				(await page.getByTestId('profile-ask').getByRole('button').all()).map((x) => x.evaluate(look)),
			);
			expect(looks.map((l) => l.text)).toEqual(['Private profile', 'Public profile']);
			const scores = looks.map(prominence);
			const top = Math.max(...scores);
			expect(looks.filter((_, i) => scores[i] === top).map((l) => l.text)).toEqual(['Private profile']);
			expect(top).toBeGreaterThanOrEqual(3);
		});

		test('nothing on a profile card is preselected or focused', async ({ page }) => {
			await page.goto(galleryURL);
			await expect(page.locator('[data-profile-choice]')).toHaveCount(2);
			expect(await page.evaluate(() => document.activeElement === document.body)).toBe(true);
			await expect(
				page
					.locator('[data-profile-choice]')
					.locator('input:checked, [autofocus], [aria-pressed="true"], [aria-checked="true"], [aria-selected="true"]'),
			).toHaveCount(0);
			await page.keyboard.press('Enter');
			await expect(page.getByTestId('profile-public-choice')).toHaveText('none');
			await expect(page.getByTestId('profile-ask-choice')).toHaveText('none');
		});

		test('each choice reports itself', async ({ page }) => {
			await page.goto(galleryURL);
			const card = page.getByTestId('profile-public');
			await card.getByRole('button', { name: 'Make it private' }).click();
			await expect(page.getByTestId('profile-public-choice')).toHaveText('private');
			await card.getByRole('button', { name: 'Keep my profile public' }).click();
			await expect(page.getByTestId('profile-public-choice')).toHaveText('public');
			await page.getByTestId('profile-ask').getByRole('button', { name: 'Private profile' }).click();
			await expect(page.getByTestId('profile-ask-choice')).toHaveText('private');
		});
	});
}
