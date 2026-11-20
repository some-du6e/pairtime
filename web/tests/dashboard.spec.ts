// SPDX-License-Identifier: AGPL-3.0-or-later

// The built dashboard in Chromium, served by `vite preview --mode agent`,
// against the fake agent in support/fake-agent.ts, whose answers keep to
// docs/api/local.openapi.yaml (support/fake.test.ts). It checks WP-UI1's
// pages as a teen sees them: the chess clock's totals equal the agent's
// numbers, pending minutes are drawn lighter, level-C time is labeled,
// D-focus-unknown's sentence shows while focus is unknown, calibration hides
// your time and keeps the agents', and the page refreshes itself.

import { expect, type Page, test } from '@playwright/test';
import { dashboardURL } from './support/env.ts';
import { fakeAgent } from './support/fake-agent.ts';
import { weekDays, weekFinal } from './support/fake-days.ts';
import { contrast, look, type RGB } from './support/style.ts';

const origin = new URL(dashboardURL).origin;

// Dates are checked as American English writes them.
test.use({ locale: 'en-US' });

const focusSentence =
	"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.";

/** The fill of each part of the first bar inside a test id, as RGB. */
async function fills(page: Page, testid: string): Promise<Record<string, RGB>> {
	const bar = page.getByTestId(testid).getByTestId('bar').first();
	const parts = await bar.locator('rect[data-part]').evaluateAll((rects) =>
		rects.map((r) => [r.getAttribute('data-part') ?? '', getComputedStyle(r).fill] as const),
	);
	const out: Record<string, RGB> = {};
	for (const [part, fill] of parts) {
		const m = /^rgb\((\d+), (\d+), (\d+)\)$/.exec(fill);
		if (!m) {
			throw new Error(`can't read the fill ${fill} of ${part}`);
		}
		out[part] = [Number(m[1]), Number(m[2]), Number(m[3])];
	}
	return out;
}

test.describe('signed out', () => {
	test('a browser without the cookie sees how to open the dashboard, and none of its data', async ({ page }) => {
		const fake = await fakeAgent(page, { signedIn: false });
		await page.goto(dashboardURL);
		const card = page.getByTestId('signed-out');
		await expect(card.locator('code')).toHaveText('pairtime dashboard');
		await expect(page.getByRole('navigation', { name: 'Pages' })).toHaveCount(0);
		await expect(page.getByTestId('link-expired')).toHaveCount(0);
		// It asked the agent how it is, and for nothing else.
		expect(fake.requests.map((u) => u.pathname)).toEqual(['/v1/status']);
	});

	test('a used or old sign-in link says so, and leaves ?signin=expired out of the address bar', async ({ page }) => {
		await fakeAgent(page, { signedIn: false });
		await page.goto(`${dashboardURL}?signin=expired`);
		await expect(page.getByTestId('link-expired')).toContainText('That link was used already');
		await expect(page).toHaveURL(dashboardURL);
	});
});

test.describe('Today', () => {
	test("the day's clock and each project's equal the agent's numbers", async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('Today');
		await expect(page.getByTestId('today-you')).toHaveText('3h 27m');
		const day = page.getByTestId('today');
		await expect(day.getByTestId('agent')).toHaveText([/^Claude\s*41m$/, /^Codex\s*22m$/, /^Copilot\s*18m$/, /^Cursor\s*12m$/]);
		const clocks = page.getByTestId('clock');
		await expect(clocks.locator('h3')).toHaveText(['portfolio', 'rusty-cli', 'homework-helper', 'discord-bot', 'plant-sensor']);
		await expect(clocks.getByTestId('you')).toHaveText(['52m', '47m', '41m', '38m', '29m']);
		await expect(clocks.first().getByTestId('agent')).toHaveText(/^Claude\s*41m$/);
		// plant-sensor had no agent, so its clock has your hand alone.
		await expect(clocks.last().getByTestId('agent')).toHaveCount(0);
	});

	test('level-C time is labeled as level C, and pending time says it is pending', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const legend = page.getByTestId('today').getByTestId('legend');
		await expect(legend.locator('li')).toHaveText([
			/Level A, typing seen\s*3h 5m/,
			/Level B, input seen\s*8m/,
			/Level C, less to go on\s*5m/,
			/Pending\s*9m/,
		]);
	});

	for (const colorScheme of ['light', 'dark'] as const) {
		test(`pending minutes are drawn lighter than final ones, ${colorScheme} scheme`, async ({ page }) => {
			await page.emulateMedia({ colorScheme });
			await fakeAgent(page);
			await page.goto(dashboardURL);
			const parts = await fills(page, 'today');
			const card = await page.getByTestId('today').evaluate(look);
			const { A, pending } = parts;
			if (!A || !pending) {
				throw new Error(`the bar has no A or pending part: ${JSON.stringify(parts)}`);
			}
			// Final time stands out from the card; pending time much less.
			expect(contrast(A, card.fill)).toBeGreaterThanOrEqual(3);
			expect(contrast(pending, card.fill)).toBeLessThan(contrast(A, card.fill) / 2);
		});
	}

	test('every final level has a color of its own', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const { A, B, C } = await fills(page, 'today');
		expect(new Set([A, B, C].map((c) => JSON.stringify(c))).size).toBe(3);
	});

	test("with /v1/status reporting focus unknown, Today shows D-focus-unknown's sentence", async ({ page }) => {
		await fakeAgent(page, { focus: { known: false } });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('focus-unknown')).toHaveText(focusSentence);
	});

	test('on GNOME before the extension runs, the sentence says to log out and back in', async ({ page }) => {
		await fakeAgent(page, { focus: { known: false, gnome_extension: 'needs_login' } });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('focus-unknown').locator('p')).toHaveText([
			focusSentence,
			"Log out and back in once to turn on pairtime's GNOME extension.",
		]);
	});

	test('with focus known, or not reported, there is no such sentence', async ({ page }) => {
		await fakeAgent(page, { focus: { known: true, via: 'SetWinEventHook' } });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('today-you')).toBeVisible();
		await expect(page.getByTestId('focus-unknown')).toHaveCount(0);
	});

	test("while calibrating, your time reads Calibrating everywhere and the agents' time stays", async ({ page }) => {
		await fakeAgent(page, { shadow: true });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('calibrating')).toBeVisible();
		await expect(page.getByTestId('today-you')).toHaveCount(0);
		await expect(page.getByTestId('legend')).toHaveCount(0);
		await expect(page.getByTestId('left-out')).toHaveCount(0);
		await expect(page.getByTestId('you')).toHaveText(Array(6).fill('Calibrating'));
		await expect(page.getByTestId('week-you')).toHaveText('calibrating');
		await expect(page.getByTestId('week-agents')).toHaveText('2h 43m');
		await expect(page.getByTestId('today').getByTestId('agent').first()).toHaveText(/^Claude\s*41m$/);
		// Clocks go by name, so their order says nothing about the hidden numbers.
		await expect(page.getByTestId('clock').locator('h3')).toHaveText([
			'discord-bot',
			'homework-helper',
			'plant-sensor',
			'portfolio',
			'rusty-cli',
		]);
	});

	test('the week lists only the days with time on them, with no gap to fill', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const week = page.getByTestId('week');
		await expect(week.locator('tbody tr')).toHaveCount(4);
		await expect(week.locator('tbody th')).toHaveText(['Monday, November 16', 'Wednesday, November 18', 'Yesterday', 'Today']);
		await expect(page.getByTestId('week-you')).toHaveText('5h 27m');
		await expect(page.getByTestId('week-agents')).toHaveText('2h 43m');
	});

	test("what pairtime didn't count is in plain words, most first", async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		await expect(page.getByTestId('left-out').locator('.label')).toHaveText([
			'Away',
			'In another app',
			'Long lookups',
			'The agent working on its own',
			'Nothing to count it from',
		]);
	});

	test('Today says what pairtime counts and why a minute counts at level A, B or C', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const how = page.getByTestId('how-it-counts');
		await expect(how).toContainText('pairtime counts the time you spend building with AI');
		await expect(how.locator('dt')).toHaveText(['Level A, typing seen', 'Level B, input seen', 'Level C, less to go on']);
		await expect(how.getByTestId('pending-words')).toContainText('pairtime waits 15 minutes after each minute');
	});

	test('once a minute the page asks again, so pending minutes turn final without a reload', async ({ page }) => {
		await page.clock.install();
		let final = false;
		const fake = await fakeAgent(page, { week: () => (final ? weekFinal() : weekDays()) });
		await page.goto(dashboardURL);
		const legend = page.getByTestId('today').getByTestId('legend');
		await expect(legend).toContainText('Pending');
		final = true;
		await page.clock.runFor(61_000);
		await expect(legend.locator('li')).toHaveText([/Level A, typing seen\s*3h 14m/, /Level B, input seen\s*8m/, /Level C, less to go on\s*5m/]);
		expect(fake.requests.filter((u) => u.pathname === '/v1/days').length).toBeGreaterThanOrEqual(2);
	});

	test('a hidden tab asks the agent nothing', async ({ page }) => {
		await page.clock.install();
		const fake = await fakeAgent(page);
		await page.goto(dashboardURL);
		await expect(page.getByTestId('today-you')).toBeVisible();
		await page.evaluate(() => {
			Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
			document.dispatchEvent(new Event('visibilitychange'));
		});
		const asked = fake.requests.length;
		await page.clock.runFor(5 * 61_000);
		expect(fake.requests.length).toBe(asked);
	});
});

test.describe('Projects', () => {
	test("each project shows its clock, your time by kind and what pairtime didn't count, in plain words", async ({ page }) => {
		await fakeAgent(page);
		await page.goto(`${dashboardURL}#/projects`);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('Projects');
		const bot = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'discord-bot' }) });
		await expect(bot.getByTestId('you')).toHaveText('1h 23m');
		await expect(bot.getByTestId('kinds').locator('.label')).toHaveText([
			'Writing prompts',
			"Reading the agent's answers",
			'Watching the agent work',
		]);
		await expect(bot.getByTestId('reasons').locator('li')).toHaveText([/Asking again\s*4m.*starts the count over\./]);
		const portfolio = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'portfolio' }) });
		await expect(portfolio.getByTestId('reasons')).toContainText('Claude working on its own');
	});

	test('the last 30 days asks the agent for the 30 days up to its own today', async ({ page }) => {
		const fake = await fakeAgent(page);
		await page.goto(`${dashboardURL}#/projects`);
		await page.getByRole('link', { name: 'Last 30 days' }).click();
		await expect(page.getByRole('link', { name: 'Last 30 days' })).toHaveAttribute('aria-current', 'page');
		await expect(page.getByTestId('span')).toHaveText('From Thursday, October 22 to Friday, November 20');
		const ranged = fake.requests.filter((u) => u.searchParams.has('from'));
		expect(ranged.map((u) => [u.searchParams.get('from'), u.searchParams.get('to')])).toContainEqual(['2026-10-22', '2026-11-20']);
		const plant = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'plant-sensor' }) });
		// 2,100 s on October 27, 1,200 s yesterday and 1,740 s today.
		await expect(plant.getByTestId('you')).toHaveText('1h 24m');
	});
});

test.describe('Privacy and Settings', () => {
	test('Privacy says nothing has left this computer, and what pairtime keeps for how long', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(`${dashboardURL}#/privacy`);
		await expect(page.getByTestId('left-computer')).toContainText('Nothing. pairtime is in local-only mode');
		await expect(page.getByTestId('kept').locator('tbody tr')).toHaveCount(8);
		await expect(page.getByTestId('delete')).toContainText('comes in a later version');
	});

	test("Settings shows the mode, calibration and the agent's version", async ({ page }) => {
		await fakeAgent(page);
		await page.goto(`${dashboardURL}#/settings`);
		await expect(page.getByTestId('mode')).toContainText('Local only: nothing leaves this computer');
		await expect(page.getByTestId('shadow')).toContainText('Off.');
		await expect(page.getByTestId('agent-version')).toHaveText('v0.1.0');
	});
});

test.describe('the whole dashboard', () => {
	test('the name bar changes the page, marks the current one and names it in the title', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const nav = page.getByRole('navigation', { name: 'Pages' });
		for (const name of ['Projects', 'Privacy', 'Settings', 'Today']) {
			await nav.getByRole('link', { name }).click();
			await expect(page.getByRole('heading', { level: 1 })).toHaveText(name);
			await expect(nav.getByRole('link', { name })).toHaveAttribute('aria-current', 'page');
			await expect(page).toHaveTitle(`${name} · pairtime`);
		}
	});

	test('the skip link moves focus to the page without changing it', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(`${dashboardURL}#/projects`);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('Projects');
		await page.keyboard.press('Tab');
		await expect(page.locator(':focus')).toHaveText('Skip to content');
		await page.keyboard.press('Enter');
		await expect(page.locator(':focus')).toHaveAttribute('id', 'main');
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('Projects');
	});

	test('every page loads only from its own origin, with no errors, which a CSP violation would be', async ({ page }) => {
		const elsewhere: string[] = [];
		const errors: string[] = [];
		page.on('request', (r) => {
			if (new URL(r.url()).origin !== origin) {
				elsewhere.push(r.url());
			}
		});
		page.on('pageerror', (e) => errors.push(e.message));
		page.on('console', (m) => {
			if (m.type() === 'error') {
				errors.push(m.text());
			}
		});
		await fakeAgent(page, { focus: { known: false } });
		for (const [hash, heading] of [
			['#/today', 'Today'],
			['#/projects', 'Projects'],
			['#/projects/30-days', 'Projects'],
			['#/privacy', 'Privacy'],
			['#/settings', 'Settings'],
		] as const) {
			await page.goto(dashboardURL + hash);
			await expect(page.getByRole('heading', { level: 1 })).toHaveText(heading);
			await expect(page.locator('main [aria-live]')).toHaveCount(0);
		}
		expect(elsewhere).toEqual([]);
		expect(errors).toEqual([]);
	});

	test('the agent not answering says how to start it', async ({ page }) => {
		await page.route('**/v1/**', (route) => route.abort('connectionrefused'));
		await page.goto(dashboardURL);
		await expect(page.getByTestId('unreachable').locator('code')).toHaveText('pairtime dashboard');
	});
});
