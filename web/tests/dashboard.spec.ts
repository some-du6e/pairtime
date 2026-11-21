// SPDX-License-Identifier: AGPL-3.0-or-later

// The built dashboard in Chromium, served by `vite preview --mode agent`,
// against the fake agent in support/fake-agent.ts, whose answers keep to
// docs/api/local.openapi.yaml (support/fake.test.ts). It checks WP-UI1's
// pages as a teen sees them: the chess clocks equal the agent's numbers in
// the words `pairtime today` prints, pending minutes are drawn lighter,
// level-C time is labeled, D-focus-unknown's sentence shows while focus is
// unknown, calibration hides your time and everything it could be worked
// out from and keeps the agents', a day or session the agent can't read
// is named, and the page refreshes itself.

import { expect, type Page, test } from '@playwright/test';
import { dashboardURL } from './support/env.ts';
import { fakeAgent } from './support/fake-agent.ts';
import { dayReply, todayFinal } from './support/fake-days.ts';
import { contrast, look, type RGB } from './support/style.ts';

const origin = new URL(dashboardURL).origin;

// Session times are written in the browser's zone; these read them in UTC.
test.use({ locale: 'en-US', timezoneId: 'UTC' });

const focusClause = "This desktop doesn't tell pairtime which window is in front.";
const focusSentence =
	"This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs.";
const levelC = 'this shows on your own dashboard, not on your profile, build days or most programs';
const wednesdayUnread =
	"pairtime couldn't read Wednesday, November 18 from its agent, so the times here leave that day out. It asks again in a minute.";

/** The fill of each part of the first bar inside a test id, as RGB. */
async function fills(page: Page, testid: string): Promise<Record<string, RGB>> {
	const bar = page.getByTestId(testid).getByTestId('bar').first();
	// evaluateAll doesn't wait, so wait for the agent's answer to be drawn.
	await bar.locator('rect[data-part]').first().waitFor();
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
		await expect(page.getByTestId('today-clock')).toHaveText('you 3h 27m, 5m level C · Claude 41m · Codex 22m · Copilot 18m · Cursor 12m');
		const clocks = page.getByTestId('clock');
		await expect(clocks.locator('h3')).toHaveText(['portfolio', 'rusty-cli', 'homework-helper', 'discord-bot', 'plant-sensor']);
		await expect(clocks.getByTestId('clock-line')).toHaveText([
			'you 52m · Claude 41m',
			'you 47m · Cursor 12m',
			'you 41m, 5m level C · Copilot 18m',
			'you 38m · Codex 22m',
			'you 29m',
		]);
		// plant-sensor had no agent, so its clock has your hand alone.
		await expect(clocks.last().getByTestId('agent')).toHaveCount(0);
		await expect(page.getByTestId('today').getByTestId('agent').first()).toHaveText(/Claude, working on its own\s*41m/);
	});

	test('level-C time and pending time are labeled with the notes `pairtime today` prints', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const legend = page.getByTestId('today').getByTestId('legend');
		await expect(legend.locator('li')).toHaveText([
			/94%\s*level A: pairtime saw your typing/,
			/4%\s*level B: pairtime saw you at the computer, not your typing/,
			new RegExp(`2%\\s*level C: ${levelC}`),
			'5m of it is level C: it shows on your own dashboard, not on your profile, build days or most programs',
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

	test("with /v1/status reporting focus unknown, Today says D-focus-unknown's first clause alone", async ({ page }) => {
		await fakeAgent(page, { focus: { known: false } });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('focus-unknown')).toHaveText(focusClause);
	});

	test('once the agent says such time is level C, the sentence says where it counts', async ({ page }) => {
		await fakeAgent(page, { focus: { known: false, level_c: true } });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('focus-unknown')).toHaveText(focusSentence);
	});

	test('on GNOME before the extension runs, the sentence says to log out and back in', async ({ page }) => {
		await fakeAgent(page, { focus: { known: false, gnome_extension: 'needs_login' } });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('focus-unknown').locator('p')).toHaveText([
			focusClause,
			"Log out and back in once to turn on pairtime's GNOME extension.",
		]);
	});

	test('with focus known, or not reported, there is no such sentence', async ({ page }) => {
		await fakeAgent(page, { focus: { known: true } });
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
		// The rest of the time keeps SM 4.2's loops alone, and today has none.
		await expect(page.getByTestId('today').getByTestId('other')).toHaveCount(0);
		await expect(page.getByTestId('today')).not.toContainText('Away');
	});

	test('while calibrating, the last session is its project and day, with no start or end', async ({ page }) => {
		await fakeAgent(page, { shadow: true });
		await page.goto(dashboardURL);
		const session = page.getByTestId('session');
		await expect(session.locator('h2')).toHaveText('Your last session: rusty-cli');
		await expect(session.getByTestId('session-span')).toHaveText('Friday, November 20');
		await expect(session.getByTestId('session-clock')).toHaveText('you calibrating');
		await expect(session).not.toContainText('14:05');
		await expect(session).not.toContainText('14:14');
	});

	test('the week lists only the days with time on them, asks for each earlier day once, and for yesterday, with time on hold, each minute', async ({
		page,
	}) => {
		await page.clock.install();
		const fake = await fakeAgent(page);
		await page.goto(dashboardURL);
		const week = page.getByTestId('week');
		await expect(week.getByTestId('week-day')).toHaveText(['Monday, November 16', 'Wednesday, November 18', 'Yesterday', 'Today']);
		await expect(page.getByTestId('week-clock')).toHaveText('you 5h 37m, 10m level C · Claude 1h 21m · Copilot 33m · Codex 32m · Cursor 17m');
		await expect(week.getByTestId('week-notes').locator('li')).toHaveText([
			'10m of it is level C: it shows on your own dashboard, not on your profile, build days or most programs',
			'9m of it may still change',
		]);
		const asked = (date: string) => fake.requests.filter((u) => u.pathname === `/v1/days/${date}`).length;
		const earlier = ['2026-11-16', '2026-11-17', '2026-11-18'];
		expect(earlier.map(asked)).toEqual([1, 1, 1]);
		expect(asked('2026-11-19')).toBe(1);
		// Each minute it asks for today and for yesterday, whose X.noprog
		// minutes progress can still bring back (SM 4.3), and not for the
		// days before. Two rounds, so the first round's asks have all come.
		for (const round of [2, 3]) {
			await page.clock.runFor(61_000);
			await expect.poll(() => asked('2026-11-19')).toBe(round);
		}
		expect(earlier.map(asked)).toEqual([1, 1, 1]);
	});

	test("a day the agent can't read is named as left out of the week, and asked for again a minute later", async ({ page }) => {
		await page.clock.install();
		let broken = true;
		const fake = await fakeAgent(page, { unavailable: (path) => broken && path === '/v1/days/2026-11-18' });
		await page.goto(dashboardURL);
		const week = page.getByTestId('week');
		await expect(week.getByTestId('unread-days')).toHaveText(wednesdayUnread);
		await expect(week.getByTestId('week-day')).toHaveText(['Monday, November 16', 'Yesterday', 'Today']);
		await expect(page.getByTestId('week-clock')).toHaveText('you 4h 37m, 10m level C · Claude 1h 21m · Copilot 33m · Codex 22m · Cursor 12m');
		broken = false;
		await page.clock.runFor(61_000);
		await expect(week.getByTestId('unread-days')).toHaveCount(0);
		await expect(week.getByTestId('week-day')).toHaveText(['Monday, November 16', 'Wednesday, November 18', 'Yesterday', 'Today']);
		await expect(page.getByTestId('week-clock')).toHaveText('you 5h 37m, 10m level C · Claude 1h 21m · Copilot 33m · Codex 32m · Cursor 17m');
		expect(fake.requests.filter((u) => u.pathname === '/v1/days/2026-11-18')).toHaveLength(2);
	});

	test('the last session reads as `pairtime recap` heads it', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const session = page.getByTestId('session');
		await expect(session.locator('h2')).toHaveText('Your last session: rusty-cli');
		await expect(page.getByTestId('session-span')).toHaveText('Friday, November 20, 14:05 to 14:14');
		// Cursor's turns fell inside credited minutes, so it has no hand here.
		await expect(session.getByTestId('session-clock')).toHaveText('you 9m');
		await expect(session.getByTestId('agent')).toHaveCount(0);
	});

	test("a last session the agent can't read is a card that says so, and that pairtime asks again", async ({ page }) => {
		await fakeAgent(page, { unavailable: (path) => path === '/v1/sessions/last' });
		await page.goto(dashboardURL);
		await expect(page.getByTestId('today-clock')).toBeVisible();
		await expect(page.getByTestId('session').getByTestId('session-unread')).toHaveText(
			"pairtime couldn't read your last session from its agent. It asks again in a minute.",
		);
	});

	test('Today says what pairtime counts and why a minute counts at level A, B or C', async ({ page }) => {
		await fakeAgent(page);
		await page.goto(dashboardURL);
		const how = page.getByTestId('how-it-counts');
		await expect(how).toContainText('pairtime counts the time you spend building with AI');
		await expect(how.locator('dt')).toHaveText([
			'Level A: pairtime saw your typing',
			'Level B: pairtime saw you at the computer, not your typing',
			`Level C: ${levelC}`,
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
			/Writing prompts\s*45m/,
			/Reading Codex's answers\s*28m/,
			/Watching Codex work\s*10m/,
		]);
		await expect(bot.getByTestId('other').locator('li')).toHaveText(['In other apps 6m', 'Re-rolling the same prompt 4m']);
	});

	test("X.noprog's minutes on hold are a row of their own, beside the minutes that can't come back", async ({ page }) => {
		await fakeAgent(page);
		await page.goto(`${dashboardURL}#/projects`);
		const homework = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'homework-helper' }) });
		await expect(homework.getByTestId('clock-line')).toHaveText('you 51m, 10m level C · Copilot 33m');
		await expect(homework.getByTestId('other').locator('li')).toHaveText([
			'Here, between steps 3m',
			'On hold: these come back when you fix it 1m 30s',
			'Past two hours without progress, counted at half 1m',
		]);
	});

	test("while calibrating, each project's rest of the time is SM 4.2's loops alone", async ({ page }) => {
		await fakeAgent(page, { shadow: true });
		await page.goto(`${dashboardURL}#/projects`);
		const bot = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'discord-bot' }) });
		await expect(bot.getByTestId('clock-line')).toHaveText('you calibrating · Codex 32m');
		await expect(bot.getByTestId('other').locator('li')).toHaveText(['Re-rolling the same prompt 4m']);
		await expect(bot.getByTestId('kinds')).toHaveCount(0);
		const homework = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'homework-helper' }) });
		await expect(homework.getByTestId('other')).toHaveCount(0);
	});

	test("a day the agent can't read is named under the span, and the totals leave it out", async ({ page }) => {
		await fakeAgent(page, { unavailable: (path) => path === '/v1/days/2026-11-18' });
		await page.goto(`${dashboardURL}#/projects`);
		await expect(page.getByTestId('span')).toHaveText('From Monday, November 16 to Friday, November 20');
		await expect(page.getByTestId('unread-days')).toHaveText(wednesdayUnread);
		const bot = page.getByTestId('project').filter({ has: page.getByRole('heading', { name: 'discord-bot' }) });
		await expect(bot.getByTestId('clock-line')).toHaveText('you 38m · Codex 22m');
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
		// A row for each device entry of docs/retention.json, in the file's
		// order, then the inbox's (src/local/retention.ts).
		const { readFileSync } = await import('node:fs');
		const retention = JSON.parse(readFileSync(new URL('../../docs/retention.json', import.meta.url), 'utf8')) as {
			entries: { id: string; where: string }[];
		};
		const ids = [...retention.entries.filter((e) => e.where === 'device').map((e) => e.id), 'device.inbox'];
		await fakeAgent(page);
		await page.goto(`${dashboardURL}#/privacy`);
		await expect(page.getByTestId('left-computer')).toContainText('Nothing. pairtime is in local-only mode');
		const rows = page.getByTestId('kept').locator('tbody tr');
		await expect(rows).toHaveCount(ids.length);
		expect(await rows.evaluateAll((trs) => trs.map((tr) => tr.getAttribute('data-id')))).toEqual(ids);
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

	test('on a phone no page scrolls sideways, and the week stacks each day', async ({ page }) => {
		await page.setViewportSize({ width: 360, height: 780 });
		await fakeAgent(page);
		for (const [hash, heading] of [
			['#/today', 'Today'],
			['#/projects/30-days', 'Projects'],
			['#/privacy', 'Privacy'],
			['#/settings', 'Settings'],
		] as const) {
			await page.goto(dashboardURL + hash);
			await expect(page.getByRole('heading', { level: 1 })).toHaveText(heading);
			await expect(page.locator('main [aria-live]')).toHaveCount(0);
			const wide = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
			expect(wide, hash).toBeLessThanOrEqual(0);
		}
		await page.goto(`${dashboardURL}#/today`);
		// The day's bar sits under its clock line, as wide as the card.
		const day = page.getByTestId('week').locator('li').last();
		const bar = await day.getByTestId('bar').boundingBox();
		const line = await day.locator('.line').boundingBox();
		expect(bar && line && bar.y >= line.y + line.height - 1).toBe(true);
	});

	test('the agent not answering says how to start it', async ({ page }) => {
		await page.route('**/v1/**', (route) => route.abort('connectionrefused'));
		await page.goto(dashboardURL);
		await expect(page.getByTestId('unreachable').locator('code')).toHaveText('pairtime dashboard');
	});
});
