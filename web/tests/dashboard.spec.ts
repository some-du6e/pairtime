// SPDX-License-Identifier: AGPL-3.0-or-later

// The built dashboard in Chromium, served by `vite preview --mode agent`,
// against the fake agent in support/fake-agent.ts, whose answers keep to
// docs/api/local.openapi.yaml (support/fake.test.ts). It checks WP-UI1's
// pages as a teen sees them: the chess clocks equal the agent's numbers in
// the words `pairtime today` prints, pending minutes are drawn lighter,
// level-C time is labeled, D-focus-unknown's sentence shows while focus is
// unknown, calibration hides your time and keeps the agents', and the page
// refreshes itself.

import { expect, type Page, test } from '@playwright/test';
import { dashboardURL } from './support/env.ts';
import { fakeAgent } from './support/fake-agent.ts';
import { dayReply, todayFinal } from './support/fake-days.ts';
import { contrast, look, type RGB } from './support/style.ts';

const origin = new URL(dashboardURL).origin;

// Session times are written in the browser's zone; these read them in UTC.
test.use({ locale: 'en-US', timezoneId: 'UTC' });

const focusSentence =
	"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.";

/** The fill of each part of the first bar inside a test id, as RGB. */
async function fills(page: Page, testid: string): Promise<Record<string, RGB>> {
	const bar = page.getByTestId(testid).getByTestId('bar').first();
	const parts = await bar
		.locator('rect[data-part]')
		.evaluateAll((rects) => rects.map((r) => [r.getAttribute('data-part') ?? '', getComputedStyle(r).fill] as const));
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
		await expect(page.getByTestId('signed-out').locator('code')).toHaveText('pairtime dashboard');
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
	test("the day's and each project's chess clock equal the agent's numbers, in `pairtime today`'s words", async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('Today');
		await expect(page.getByTestId('today-clock')).toHaveText('you 3h 27m · Claude 41m · Codex 22m · Copilot 18m · Cursor 12m');
		const clocks = page.getByTestId('clock');
		await expect(clocks.locator('h3')).toHaveText(['portfolio', 'rusty-cli', 'homework-helper', 'discord-bot', 'plant-sensor']);
		await expect(clocks.getByTestId('clock-line')).toHaveText([
			'you 52m · Claude 41m',
			'you 47m · Cursor 12m',
			'you 41m · Copilot 18m',
			'you 38m · Codex 22m',
			'you 29m',
		]);
		// plant-sensor had no agent, so its clock has your hand alone.
		await expect(clocks.last().getByTestId('agent')).toHaveCount(0);
		await expect(page.getByTestId('today').getByTestId('agent').first()).toHaveText(/^Claude, working on its own\s*41m$/);
	});

	test('level-C time and pending time are labeled with the notes `pairtime today` prints', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const legend = page.getByTestId('today').getByTestId('legend');
		await expect(legend.locator('li')).toHaveText([
			/^94%\s*level A: pairtime saw your typing$/,
			/^4%\s*level B: pairtime saw you at the computer, not your typing$/,
			/^2%\s*level C: this counts only on your own dashboard$/,
			'5m of it is level C: it counts only on your own dashboard',
			'9m of it may still change',
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

	test('every level has a color of its own', async ({ page }) => {
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
		await expect(page.getByTestId('today-clock')).toBeVisible();
		await expect(page.getByTestId('focus-unknown')).toHaveCount(0);
	});

	test("while calibrating, your time reads calibrating everywhere and the agents' time stays", async ({ page }) => {
		await fakeAgent(page, { shadow: true });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('calibrating')).toHaveText('Your time shows as calibrating while pairtime checks how it counts.');
		await expect(page.getByTestId('today-clock')).toHaveText('you calibrating · Claude 41m · Codex 22m · Copilot 18m · Cursor 12m');
		await expect(page.getByTestId('legend')).toHaveCount(0);
		await expect(page.getByTestId('you').first()).toHaveText('calibrating');
		await expect(page.getByTestId('week-clock')).toHaveText('you calibrating · Claude 1h 21m · Copilot 33m · Codex 32m · Cursor 17m');
		// The order leaves your time out: the most agent time first.
		await expect(page.getByTestId('clock').locator('h3')).toHaveText([
			'portfolio',
			'discord-bot',
			'homework-helper',
			'rusty-cli',
			'plant-sensor',
		]);
	});

	test('the week lists only the days with time on them, and asks for each earlier day once', async ({ page }) => {
		await page.clock.install();
		const fake = await fakeAgent(page);
		await page.goto(dashboardURL);
		const week = page.getByTestId('week');
		await expect(week.locator('tbody th')).toHaveText(['Monday, November 16', 'Wednesday, November 18', 'Yesterday', 'Today']);
		await expect(page.getByTestId('week-clock')).toHaveText('you 5h 37m · Claude 1h 21m · Copilot 33m · Codex 32m · Cursor 17m');
		const asked = () => fake.requests.filter((u) => /^\/v1\/days\/2026-11-1[6-9]$/.test(u.pathname)).map((u) => u.pathname);
		expect(asked().sort()).toEqual(['/v1/days/2026-11-16', '/v1/days/2026-11-17', '/v1/days/2026-11-18', '/v1/days/2026-11-19']);
		// A minute later it asks for today again, and not for the earlier days.
		const todays = () => fake.requests.filter((u) => u.pathname === '/v1/days/today').length;
		const before = todays();
		await page.clock.runFor(61_000);
		await expect.poll(todays).toBeGreaterThan(before);
		expect(asked()).toHaveLength(4);
	});

	test('the last session reads as `pairtime recap` heads it', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const session = page.getByTestId('session');
		await expect(session.locator('h2')).toHaveText('Your last session: rusty-cli');
		await expect(page.getByTestId('session-span')).toHaveText('Friday, November 20, 14:05 to 14:14');
	});

	test('Today says what pairtime counts and why a minute counts at level A, B or C', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const how = page.getByTestId('how-it-counts');
		await expect(how).toContainText('pairtime counts the time you spend building with AI');
		await expect(how.locator('dt')).toHaveText([
			'Level A: pairtime saw your typing',
			'Level B: pairtime saw you at the computer, not your typing',
			'Level C: this counts only on your own dashboard',
		]);
		await expect(how.getByTestId('pending-words')).toContainText('pairtime waits 15 minutes after each minute');
	});

	test('once a minute the page asks again, so pending minutes turn final without a reload', async ({ page }) => {
		await page.clock.install();
		let final = false;
		await fakeAgent(page, { day: (date) => (date === 'today' && final ? todayFinal() : dayReply(date)) });
		await page.goto(dashboardURL);
		const legend = page.getByTestId('today').getByTestId('legend');
		await expect(legend).toContainText('9m of it may still change');
		final = true;
		await page.clock.runFor(61_000);
		await expect(legend).not.toContainText('may still change');
		await expect(legend).toContainText('5m of it is level C');
	});

	test('a hidden tab asks the agent nothing', async ({ page }) => {
		await page.clock.install();
		const fake = await fakeAgent(page);
		await page.goto(dashboardURL);
		await expect(page.getByTestId('today-clock')).toBeVisible();
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
	test("each project shows its clock, what your time went on and the rest, in `pairtime today`'s words", async ({ page }) => {
		await fakeAgent(page);
		await page.goto(`${dashboardURL}#/projects`);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('Projects');
		const bot = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'discord-bot' }) });
		await expect(bot.getByTestId('clock-line')).toHaveText('you 1h 23m · Codex 32m');
		await expect(bot.getByTestId('kinds').locator('li')).toHaveText([
			/^Writing prompts\s*45m$/,
			/^Reading Codex's answers\s*28m$/,
			/^Watching Codex work\s*10m$/,
		]);
		await expect(bot.getByTestId('other').locator('li')).toHaveText([/^Re-rolling the same prompt\s*4m$/]);
	});

	test('the last 30 days asks the agent for each of the 30 days up to its own today', async ({ page }) => {
		const fake = await fakeAgent(page);
		await page.goto(`${dashboardURL}#/projects`);
		await page.getByRole('link', { name: 'Last 30 days' }).click();
		await expect(page.getByRole('link', { name: 'Last 30 days' })).toHaveAttribute('aria-current', 'page');
		await expect(page.getByTestId('span')).toHaveText('From Thursday, October 22 to Friday, November 20');
		const plant = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'plant-sensor' }) });
		// 2,100 s on October 27, 1,200 s yesterday and 1,740 s today.
		await expect(plant.getByTestId('clock-line')).toHaveText('you 1h 24m');
		const days = new Set(fake.requests.map((u) => u.pathname).filter((p) => /^\/v1\/days\/\d/.test(p)));
		expect(days.has('/v1/days/2026-10-22')).toBe(true);
		expect(days.has('/v1/days/2026-10-21')).toBe(false);
		expect(days.size).toBe(29);
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
